package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/jinto/kittypaw/channel"
	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/engine"
	"github.com/jinto/kittypaw/store"
)

// Server is the HTTP/WebSocket gateway that bridges REST clients and browsers
// to the runner engine. It owns the chi router, the default-account engine
// session, account schedulers, channel spawner, account router, and all
// handler state.
type Server struct {
	config             *core.Config
	configMu           sync.RWMutex // protects config during hot-reload
	store              *store.Store
	session            *engine.Session // default-account session; HTTP handlers use this
	schedulers         *AccountSchedulers
	router             chi.Router
	spawner            *ChannelSpawner       // manages channel lifecycle for hot-reload
	accounts           *AccountRouter        // routes channel events to account-scoped sessions
	accountList        []*core.Account       // ordered account metadata for startup validation
	accountRegistry    *core.AccountRegistry // shared cross-account registry (Share.read / Fanout)
	eventCh            chan core.Event       // shared event channel between channels and dispatch loop
	accountMu          sync.Mutex            // serializes AddAccount/RemoveAccount — validation→register→reconcile must not interleave
	channelWorkersMu   sync.Mutex
	channelWorkers     map[string]*channelEventWorker
	channelTurnTimeout time.Duration
	accountDeps        map[string]*AccountDeps // retained close-targets (Store+MCP) for RemoveAccount; populated on successful AddAccount
	removingAccount    map[string]bool         // account IDs detached from routing but still draining scheduler/deps
	localAuth          *core.LocalAuthStore
	webSessionKey      []byte
	masterAPIKey       string
	version            string
	pkgManager         *core.PackageManager // default-account package manager for API handlers
	liveIndexer        *engine.LiveIndexer  // default-account live indexer (nil if lazy mode)

	// reloadReconcile, if non-nil, replaces s.spawner.Reconcile inside
	// handleReload. Test-only hook that lets AC-RELOAD-SYNC inject a barrier
	// to observe the synchronous contract; production always leaves this nil
	// and falls through to the live spawner.
	reloadReconcile func(accountID string, cfgs []core.ChannelConfig) error
	postReloadHook  func(context.Context) error
}

// DefaultAccountID is the legacy account ID retained for migrated installs.
// Fresh installs may use any account ID; server.New chooses the configured
// default when present, otherwise this legacy ID when present, otherwise the
// first loaded account.
const DefaultAccountID = "default"

// New wires together all dependencies and returns a ready-to-serve Server.
// Callers must pass at least one AccountDeps; New panics on an empty slice
// because a server with no accounts has nothing to route to.
//
// One engine.Session is built per account. Team-space accounts
// (Config.IsTeamSpaceAccount()) receive a ChannelFanout wired to the shared
// eventCh so their skills can push to configured members via Fanout.send;
// personal accounts keep Fanout nil so the JS global stays hidden
// (I5 — personal cannot reach personal).
// Every session shares the same *core.AccountRegistry pointer so Share.read
// can resolve peer accounts by ID.
//
// The HTTP handler surface (/api/v1, secrets) remains bound to the default
// account in PR-1; multi-account HTTP routing is scoped to a follow-up. Call
// StartChannels before ListenAndServe to activate messaging.
func New(accounts []*AccountDeps, version string, configuredDefault ...string) *Server {
	sc := core.TopLevelServerConfig{}
	if len(configuredDefault) > 0 {
		sc.DefaultAccount = configuredDefault[0]
	}
	return NewWithServerConfig(accounts, version, sc)
}

func NewWithServerConfig(accounts []*AccountDeps, version string, sc core.TopLevelServerConfig) *Server {
	if len(accounts) == 0 {
		panic("server.New: accounts slice must be non-empty")
	}

	defaultDeps := SelectDefaultAccountDeps(accounts, sc.DefaultAccount)
	cfg := defaultDeps.Account.Config

	// eventCh MUST exist before Fanout construction — ChannelFanout retains
	// a reference for every future send.
	eventCh := make(chan core.Event, 64)

	// Shared cross-account registry. BaseDir points at the accounts/ root
	// (the parent of each account's BaseDir) so Share.read and future
	// listing operations have a consistent anchor.
	accountsRoot := filepath.Dir(defaultDeps.Account.BaseDir)
	registry := core.NewAccountRegistry(accountsRoot, defaultDeps.Account.ID)
	accountList := make([]*core.Account, 0, len(accounts))
	for _, td := range accounts {
		registry.Register(td.Account)
		accountList = append(accountList, td.Account)
	}

	router := NewAccountRouter()
	schedulers := NewAccountSchedulers()
	var defaultSession *engine.Session
	depsByID := make(map[string]*AccountDeps, len(accounts))
	for _, td := range accounts {
		sess := buildAccountSession(td, registry, eventCh)
		router.Register(td.Account.ID, sess)
		schedulers.Register(td.Account.ID, engine.NewScheduler(sess, td.PkgMgr))
		depsByID[td.Account.ID] = td
		if td == defaultDeps {
			defaultSession = sess
		}
	}

	s := &Server{
		config:          cfg,
		store:           defaultDeps.Store,
		session:         defaultSession,
		schedulers:      schedulers,
		accounts:        router,
		accountList:     accountList,
		accountRegistry: registry,
		eventCh:         eventCh,
		channelWorkers:  make(map[string]*channelEventWorker),
		accountDeps:     depsByID,
		removingAccount: make(map[string]bool),
		localAuth:       newLocalAuthStore(),
		webSessionKey:   newWebSessionKey(),
		masterAPIKey:    sc.MasterAPIKey,
		version:         version,
		pkgManager:      defaultDeps.PkgMgr,
		liveIndexer:     defaultDeps.LiveIndexer,
	}
	s.router = s.setupRoutes()
	return s
}

// SetPostReloadHook registers extra work that must happen synchronously after
// /api/v1/reload swaps config and reconciles channels. The CLI server uses
// this to refresh outbound hosted-service connectors after login updates
// secrets on disk while the daemon is already running.
func (s *Server) SetPostReloadHook(hook func(context.Context) error) {
	s.postReloadHook = hook
}

func SelectDefaultAccountDeps(accounts []*AccountDeps, configuredDefault string) *AccountDeps {
	if len(accounts) == 0 {
		return nil
	}
	if configuredDefault != "" {
		for _, td := range accounts {
			if td.Account.ID == configuredDefault {
				return td
			}
		}
	}
	for _, td := range accounts {
		if td.Account.ID == DefaultAccountID {
			return td
		}
	}
	return accounts[0]
}

func (s *Server) defaultAccountID() string {
	if s.accountRegistry != nil {
		if id := s.accountRegistry.DefaultID(); id != "" {
			return id
		}
	}
	if s.session != nil && s.session.AccountID != "" {
		return s.session.AccountID
	}
	for _, account := range s.accountList {
		if account != nil && account.ID == DefaultAccountID {
			return DefaultAccountID
		}
	}
	if len(s.accountList) == 1 && s.accountList[0] != nil {
		return s.accountList[0].ID
	}
	return DefaultAccountID
}

func (s *Server) effectiveAPIKey() string {
	if s.masterAPIKey != "" {
		return s.masterAPIKey
	}
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	return s.config.Server.APIKey
}

type requestAccount struct {
	ID      string
	Deps    *AccountDeps
	Session *engine.Session
}

func (s *Server) requestAccount(r *http.Request) (*requestAccount, error) {
	if accountID, ok := s.webSessionAccountID(r); ok {
		return s.requestAccountByID(accountID)
	}

	token := requestAuthToken(r)
	if token != "" {
		if accountID, ok := s.webSessionTokenAccountID(token); ok {
			return s.requestAccountByID(accountID)
		}
		if acct, ok, err := s.requestAccountByAPIKey(token); err != nil || ok {
			return acct, err
		}
	}

	authRequired, err := s.localAuthRequired()
	if err != nil {
		return nil, fmt.Errorf("read local auth store: %w", err)
	}
	deps := s.activeAccountDeps()
	if !authRequired && s.effectiveAPIKey() == "" && len(deps) == 1 {
		return s.requestAccountFromDeps(deps[0])
	}
	return nil, fmt.Errorf("unauthorized")
}

func requestAuthToken(r *http.Request) string {
	if token := r.URL.Query().Get("token"); token != "" {
		return token
	}
	if key := r.Header.Get("x-api-key"); key != "" {
		return key
	}
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

func (s *Server) requestAccountByID(accountID string) (*requestAccount, error) {
	return s.requestAccountFromDeps(s.accountDepsForID(accountID))
}

func (s *Server) requestAccountByAPIKey(token string) (*requestAccount, bool, error) {
	activeDeps := s.activeAccountDeps()
	var match *AccountDeps
	for _, deps := range activeDeps {
		if deps == nil || deps.Account == nil || deps.Account.Config == nil {
			continue
		}
		apiKey := deps.Account.Config.Server.APIKey
		if fixedLenEqual(token, apiKey) {
			if match != nil {
				return nil, true, fmt.Errorf("ambiguous account api key")
			}
			match = deps
		}
	}
	if match == nil {
		if len(activeDeps) == 1 {
			if apiKey := s.effectiveAPIKey(); fixedLenEqual(token, apiKey) {
				acct, err := s.requestAccountFromDeps(activeDeps[0])
				return acct, true, err
			}
		}
		return nil, false, nil
	}
	acct, err := s.requestAccountFromDeps(match)
	return acct, true, err
}

func (s *Server) requestAccountFromDeps(deps *AccountDeps) (*requestAccount, error) {
	if deps == nil || deps.Account == nil {
		return nil, fmt.Errorf("unauthorized")
	}
	sess := s.accounts.Session(deps.Account.ID)
	if sess == nil {
		return nil, fmt.Errorf("unauthorized")
	}
	return &requestAccount{
		ID:      deps.Account.ID,
		Deps:    deps,
		Session: sess,
	}, nil
}

func (s *Server) accountDepsForID(accountID string) *AccountDeps {
	s.accountMu.Lock()
	defer s.accountMu.Unlock()
	if s.accountDeps == nil {
		return nil
	}
	return s.accountDeps[accountID]
}

func (s *Server) activeAccountDeps() []*AccountDeps {
	s.accountMu.Lock()
	defer s.accountMu.Unlock()
	deps := make([]*AccountDeps, 0, len(s.accountDeps))
	for _, td := range s.accountDeps {
		if td != nil {
			deps = append(deps, td)
		}
	}
	return deps
}

func (s *Server) allowedOriginsForAccount(acct *requestAccount) []string {
	var origins []string
	if acct != nil && acct.ID == s.defaultAccountID() {
		s.configMu.RLock()
		origins = append([]string(nil), s.config.Server.AllowedOrigins...)
		s.configMu.RUnlock()
		return origins
	}
	if acct != nil && acct.Session != nil && acct.Session.Config != nil {
		return append([]string(nil), acct.Session.Config.Server.AllowedOrigins...)
	}
	if acct != nil && acct.Deps != nil && acct.Deps.Account != nil && acct.Deps.Account.Config != nil {
		return append([]string(nil), acct.Deps.Account.Config.Server.AllowedOrigins...)
	}
	return nil
}

// setupRoutes builds the full route tree. API routes live under /api/v1 and
// are optionally gated by an API key. The WebSocket endpoint sits at /ws.
// Setup and bootstrap endpoints are unauthenticated so the onboarding
// wizard can run before an API key exists.
func (s *Server) setupRoutes() chi.Router {
	return s.setupRoutesWithTimeout(60 * time.Second)
}

func (s *Server) setupRoutesWithTimeout(requestTimeout time.Duration) chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(s.corsMiddleware)

	r.Group(func(r chi.Router) {
		r.Use(middleware.Timeout(requestTimeout))

		// Health check (unauthenticated — server liveness probe).
		r.Get("/health", s.handleHealth)

		r.Route("/api/auth", func(r chi.Router) {
			r.Post("/login", s.handleAuthLogin)
			r.Post("/logout", s.handleAuthLogout)
			r.Get("/me", s.handleAuthMe)
		})

		// Bootstrap is open only before local Web UI users exist; once setup has
		// created per-account auth files it requires a browser session before
		// returning api_key.
		r.With(s.requireWebSessionIfAuthUsers).Get("/api/bootstrap", s.handleBootstrap)

		// Chat bootstrap is the narrow browser chat surface. It does not return
		// the /api/v1 control token and points clients at /chat/ws.
		r.Get("/api/chat/bootstrap", s.handleChatBootstrap)

		// Telegram pairing is used by CLI setup/account flows as well as settings.
		// The handler resolves the account from a web session, per-account API key,
		// or server master key because /api/v1 auth is default-account oriented.
		r.Post("/api/telegram/pairing/chat-id", s.handleTelegramPairingChatID)

		// Authenticated post-setup settings. First-run setup stays in the CLI;
		// these endpoints only mutate already configured accounts.
		r.Route("/api/settings", func(r chi.Router) {
			r.Get("/locale", s.handleSettingsLocaleGet)
			r.Post("/locale", s.handleSettingsLocalePost)
			r.Post("/llm", s.handleSettingsLLM)
			r.Post("/telegram", s.handleSettingsTelegram)
			r.Post("/telegram/chat-id", s.handleSettingsTelegramChatID)
			r.Get("/directories", s.handleSettingsDirectoriesBrowse)
			r.Get("/workspaces", s.handleSettingsWorkspacesList)
			r.Post("/workspaces", s.handleSettingsWorkspacesCreate)
			r.Delete("/workspaces/{id}", s.handleSettingsWorkspacesDelete)
		})

		// Setup / onboarding routes are open for first-run setup only. Existing
		// installs require the local Web UI session because localhost checks are
		// not enough when the server is reached through a tunnel.
		r.Route("/api/setup", func(r chi.Router) {
			r.Use(s.requireWebSessionIfAuthUsers)

			// Always accessible.
			r.Get("/status", s.handleSetupStatus)
			r.Get("/kakao/pair-status", s.handleSetupKakaoPairStatus)

			// Localhost only.
			r.Post("/reset", s.handleSetupReset)

			// Guarded — localhost during first-run, otherwise authenticated by
			// the local Web UI session for the account being configured.
			r.Group(func(r chi.Router) {
				r.Use(s.requireSetupMutationAccess)
				r.Use(s.requireOnboardingIncomplete)
				r.Post("/llm", s.handleSetupLlm)
				r.Post("/telegram", s.handleSetupTelegram)
				r.Post("/telegram/chat-id", s.handleSetupTelegramChatID)
				r.Post("/kakao/register", s.handleSetupKakaoRegister)
				r.Post("/api-server", s.handleSetupAPIServer)
				r.Post("/workspace", s.handleSetupWorkspace)
				r.Post("/http-access", s.handleSetupHttpAccess)
				r.Post("/complete", s.handleSetupComplete)
			})
		})

		r.Route("/api/v1", func(r chi.Router) {
			r.Group(func(r chi.Router) {
				r.Use(s.requireAPIKey)

				// Status / history
				r.Get("/status", s.handleStatus)
				r.Get("/executions", s.handleExecutions)
				r.Post("/telegram/pairing/chat-id", s.handleTelegramPairingChatID)

				// Skills
				r.Get("/skills", s.handleSkills)
				r.Post("/skills/run", s.handleSkillsRun)
				r.Post("/skills/teach", s.handleSkillsTeach)
				r.Post("/skills/teach/approve", s.handleTeachApprove)
				r.Delete("/skills/{name}", s.handleSkillsDelete)
				r.Post("/skills/{name}/enable", s.handleSkillEnable)
				r.Post("/skills/{name}/disable", s.handleSkillDisable)
				r.Post("/skills/{name}/explain", s.handleSkillExplain)

				// Checkpoints
				r.Post("/checkpoints/{id}/rollback", s.handleCheckpointRollback)

				// Chat
				r.Post("/chat", s.handleChat)
				r.Get("/chat/history", s.handleChatHistory)
				r.Post("/chat/forget", s.handleChatForget)
				r.Post("/chat/compact", s.handleChatCompact)
				r.Get("/chat/checkpoints", s.handleCheckpointsList)
				r.Post("/chat/checkpoints", s.handleCheckpointsCreate)
				r.Get("/conversations", s.handleConversationsList)
				r.Post("/conversations", s.handleConversationsCreate)
				r.Get("/conversations/{id}", s.handleConversationInfo)
				r.Get("/conversations/{id}/messages", s.handleConversationMessages)

				// Config
				r.Get("/config/check", s.handleConfigCheck)
				r.Post("/reload", s.handleReload)

				// Admin — runtime account lifecycle. Localhost-only on top of the
				// /api/v1 requireAPIKey gate: the server binds to 127.0.0.1 by
				// default, but if a future deployment exposes it, admin mutations
				// still require local access.
				r.Route("/admin", func(r chi.Router) {
					r.Use(s.requireLocalhost)
					r.Post("/accounts", s.handleAdminAccountAdd)
					r.Post("/accounts/{id}/delete", s.handleAdminAccountRemove)
				})

				// Install
				r.Post("/install", s.handleInstall)

				// Search
				r.Get("/search", s.handleSearch)

				// Packages (gallery)
				r.Get("/packages", s.handlePackagesList)
				r.Post("/packages/install-from-registry", s.handlePackageInstallFromRegistry)
				r.Get("/packages/{id}", s.handlePackageDetail)
				r.Delete("/packages/{id}", s.handlePackageUninstall)
				r.Post("/packages/{id}/config", s.handlePackageConfigSet)

				// Channels
				r.Get("/channels", s.handleChannels)

				// Memory
				r.Get("/memory/search", s.handleMemorySearch)

				// Staff
				r.Get("/staff", s.handleStaffList)
				r.Post("/staff", s.handleStaffCreate)
				r.Post("/staff/{id}/activate", s.handleStaffActivate)

			})

			r.Group(func(r chi.Router) {
				r.Use(s.requireProjectsAPIAccess)

				// Projects
				r.Get("/projects", s.handleProjectsList)
				r.Post("/projects", s.handleProjectsCreate)
				r.Get("/projects/{project}", s.handleProjectShow)
				r.Get("/projects/{project}/board", s.handleProjectBoard)
				r.Post("/projects/{project}/git/init", s.handleProjectGitInit)
				r.Get("/projects/{project}/brief-drafts", s.handleProjectBriefDraftsList)
				r.Post("/projects/{project}/brief-drafts", s.handleProjectBriefDraftsCreate)
				r.Patch("/projects/{project}/brief-drafts/{draft}", s.handleProjectBriefDraftUpdate)
				r.Post("/projects/{project}/brief-drafts/{draft}/commit", s.handleProjectBriefDraftCommit)

				// Tickets
				r.Get("/tickets", s.handleTicketsList)
				r.Post("/tickets", s.handleTicketsCreate)
				r.Get("/tickets/{ticket}", s.handleTicketShow)
				r.Post("/tickets/{ticket}/actions", s.handleTicketActionsCreate)
				r.Post("/tickets/{ticket}/archive", s.handleTicketArchive)
				r.Get("/tickets/{ticket}/jobs", s.handleTicketJobsList)
				r.Post("/tickets/{ticket}/jobs/plan", s.handleTicketJobsPlan)

				// Jobs
				r.Get("/jobs/{job}", s.handleJobShow)
				r.Post("/jobs/{job}/approve", s.handleJobApprove)
				r.Post("/jobs/{job}/start", s.handleJobStart)
				r.Post("/jobs/{job}/cancel", s.handleJobCancel)
				r.Post("/jobs/{job}/input", s.handleJobInput)
				r.Get("/jobs/{job}/logs", s.handleJobLogs)

				// Drivers
				r.Get("/drivers", s.handleDriversList)
				r.Post("/drivers", s.handleDriversCreate)
				r.Patch("/drivers/{driver}", s.handleDriverUpdate)
			})
		})
	})

	// WebSocket sits outside /api/v1 — auth is done via query param or header.
	// Keep it outside the HTTP request timeout middleware: local LLM turns can
	// legitimately run longer than 60s, while wsMaxLifetime + heartbeat still
	// bound dead sessions.
	r.HandleFunc("/ws", s.handleWebSocket)
	r.HandleFunc("/chat/ws", s.handleChatWebSocket)

	// Static web assets with SPA fallback — must be last (catch-all).
	r.Handle("/*", staticHandler())

	return r
}

// getConfig returns the current server config under RWMutex for hot-reload safety.
func (s *Server) getConfig() *core.Config {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	return s.config
}

// ProcessEvent runs a single event through the engine session and returns
// the runner response. This is used by the channel dispatch loop to bridge
// inbound channel messages to the runner engine.
func (s *Server) ProcessEvent(ctx context.Context, event core.Event) (string, error) {
	return s.session.Run(ctx, event, nil)
}

// StartChannels creates the ChannelSpawner, reconciles each account's
// channel configs, and starts the dispatch and retry goroutines. Must be
// called before ListenAndServe.
//
// Startup validations run BEFORE any channel spawns:
//   - ValidateAccountChannels: a single Telegram bot token / Kakao relay
//     URL cannot be claimed by two accounts (C3 — prevents silent update
//     races where one account's bot steals another's messages).
//   - ValidateTeamSpaceAccounts: team-space accounts must not declare channels
//     (C10 — team-space is a coordinator, not a channel owner; a misconfigured
//     [telegram] on team-space would race the real personal bot for updates).
//   - ValidateTeamSpaceMemberships: configured members must resolve to
//     existing accounts before Fanout.send can target them.
func (s *Server) StartChannels(ctx context.Context) error {
	accountChannels := make(map[string][]core.ChannelConfig, len(s.accountList))
	for _, t := range s.accountList {
		if t.Config == nil {
			continue
		}
		accountChannels[t.ID] = t.Config.Channels
	}

	if err := core.ValidateAccountChannels(accountChannels); err != nil {
		return fmt.Errorf("channel config validation: %w", err)
	}
	if err := core.ValidateTeamSpaceAccounts(s.accountList); err != nil {
		return fmt.Errorf("team space validation: %w", err)
	}
	if err := core.ValidateTeamSpaceMemberships(s.accountList); err != nil {
		return fmt.Errorf("team-space membership validation: %w", err)
	}

	s.spawner = NewChannelSpawner(ctx, s.eventCh)
	for accountID, configs := range accountChannels {
		if len(configs) == 0 {
			continue
		}
		if err := s.spawner.Reconcile(accountID, configs); err != nil {
			slog.Warn("initial channel reconcile: some channels failed",
				"account", accountID, "error", err)
		}
	}
	go s.dispatchLoop(ctx)
	go s.retryPendingResponses(ctx)
	return nil
}

// dispatchLoop reads events from the shared eventCh, routes them to the
// account-scoped engine session, and returns responses via the spawner.
//
// Events with an empty or unknown AccountID are dropped by the AccountRouter
// (no default fallback) to avoid cross-account privacy leaks — see C1 in
// the account-routing privacy constraint.
func (s *Server) dispatchLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-s.eventCh:
			if !ok {
				return
			}
			// EventTeamSpacePush carries a FanoutPayload (not a ChatPayload) and
			// delivers an already-composed message to the target account —
			// skip the runner loop entirely. Without this branch the generic
			// ParsePayload below silently produces a zero-valued ChatPayload,
			// the event routes through session.Run, and the push text never
			// reaches the target channel.
			if core.IsTeamSpacePushEvent(event.Type) {
				s.deliverTeamSpacePush(ctx, event)
				continue
			}
			payload, err := event.ParsePayload()
			if err != nil {
				slog.Warn("channel event: bad payload", "type", event.Type, "error", err)
				continue
			}

			session := s.accounts.Route(event)
			if session == nil {
				// Drop was already logged + counted by AccountRouter.
				continue
			}

			// AC-T7: chat_id ownership check. Route() matched AccountID to a
			// Session, but a compromised/leaked bot token could still inject
			// an event whose chat_id belongs to a different account. Without
			// this gate alice's Session.Run would persist bob's conversation
			// under alice's store — a privacy breach the AccountID check
			// alone cannot catch. Permissive when no allowed chat IDs are
			// configured (web_chat-only accounts).
			//
			// Kakao is different: payload.ChatID is the relay callback action id
			// used by SendResponse, not a stable user/chat identity. Kakao account
			// ownership is established by the per-account relay token that stamped
			// Event.AccountID before this dispatch path.
			if event.Type != core.EventKakaoTalk && !core.ChatBelongsToAccount(session.Config, payload.ChatID) {
				s.accounts.RecordMismatch(event.AccountID)
				slog.Warn("account_routing_mismatch",
					"account", event.AccountID,
					"chat_id", payload.ChatID,
					"type", event.Type,
				)
				continue
			}

			slog.Info("processing channel event",
				"type", event.Type,
				"account", event.AccountID,
				"chat_id", payload.ChatID,
				"from", payload.FromName,
			)

			// Build RunOptions with Confirmer-based permission callback if available.
			var runOpts *engine.RunOptions
			ch, chOK := s.spawner.GetChannel(event.AccountID, event.Type)
			if chOK {
				if confirmer, ok := ch.(channel.Confirmer); ok {
					evType := string(event.Type)
					chatID := payload.ChatID
					runOpts = &engine.RunOptions{
						OnPermission: func(pCtx context.Context, desc, res string) (bool, error) {
							s.logPermissionEvent("requested", evType, chatID, desc, res)

							timeout := s.permissionTimeout()
							permCtx, cancel := context.WithTimeout(pCtx, timeout)
							defer cancel()

							ok, err := confirmer.AskConfirmation(permCtx, chatID, desc, res)
							var decision string
							switch {
							case err != nil:
								decision = "timeout"
							case ok:
								decision = "approved"
							default:
								decision = "denied"
							}
							s.logPermissionEvent(decision, evType, chatID, desc, res)
							return ok, err
						},
					}
				}
			}

			// Chat-path /model override fallback: when the user has set
			// `/model <id>` and this dispatched event has no explicit
			// per-event ModelOverride, use the chat-set override. Schedule
			// path (engine/schedule.go) does NOT call ApplyActiveModel and
			// keeps its per-job model.
			runOpts = session.ApplyActiveModel(runOpts)

			s.enqueueChannelEvent(ctx, channelEventJob{
				event:   event,
				payload: payload,
				session: session,
				runOpts: runOpts,
				ch:      ch,
				chOK:    chOK,
			})
		}
	}
}

func sendChannelResponse(ctx context.Context, ch channel.Channel, chatID string, outbound core.OutboundResponse, replyToMessageID string) error {
	if rich, ok := ch.(channel.RichResponder); ok {
		return rich.SendRichResponse(ctx, chatID, outbound, replyToMessageID)
	}
	return ch.SendResponse(ctx, chatID, outbound.Text, replyToMessageID)
}

// deliverTeamSpacePush routes an EventTeamSpacePush to the target account's channel
// and bypasses the runner loop. The payload is a finished outbound message
// (Fanout.send already gave a skill author's hand-authored text), so we do
// not re-invoke the LLM — doing so would paraphrase, translate, or drop the
// message entirely depending on prompt context.
//
// Routing order:
//  1. Target account must exist + have at least one declared channel.
//  2. ChannelHint picks a specific channel type; fall back to Channels[0].
//  3. First allowed chat ID is the destination chat; empty = log + drop
//     (nowhere to send).
//  4. If the channel is not currently running (hot-reload, post-restart),
//     enqueue to pending_responses so the retry loop can pick it up.
func (s *Server) deliverTeamSpacePush(ctx context.Context, event core.Event) {
	var p core.FanoutPayload
	if err := json.Unmarshal(event.Payload, &p); err != nil {
		slog.Warn("team_space_push: bad payload", "account", event.AccountID, "error", err)
		return
	}

	target := s.accountRegistry.Get(event.AccountID)
	if target == nil || target.Config == nil {
		slog.Warn("team_space_push: unknown target account", "account", event.AccountID)
		return
	}
	if len(target.Config.Channels) == 0 {
		slog.Warn("team_space_push: target has no channels configured; dropping",
			"account", event.AccountID)
		return
	}

	channelType := resolveTeamSpacePushChannel(target.Config.Channels, p.ChannelHint)

	chatID := core.FirstAllowedChatID(target.Config)
	if chatID == "" {
		slog.Warn("team_space_push: target has no admin chat; dropping",
			"account", event.AccountID, "channel", channelType)
		return
	}

	ch, chOK := s.spawner.GetChannel(event.AccountID, channelType)
	if !chOK {
		s.enqueueTeamSpacePushForRetry(event.AccountID, channelType, chatID, p.Text, "channel not running")
		return
	}

	if err := ch.SendResponse(ctx, chatID, p.Text, ""); err != nil {
		s.enqueueTeamSpacePushForRetry(event.AccountID, channelType, chatID, p.Text,
			fmt.Sprintf("send failed: %v", err))
		return
	}

	slog.Info("team_space_push_delivered",
		"from", "team_space", "to", event.AccountID, "channel", channelType, "chat_id", chatID)
}

// enqueueTeamSpacePushForRetry parks an undelivered team-space push in pending_responses
// so the retry loop can pick it up after the channel comes back. Kakao is
// excluded because its action IDs are ephemeral — by the time the retry fires,
// the originating action no longer exists, so re-sending would 4xx-loop forever.
func (s *Server) enqueueTeamSpacePushForRetry(accountID string, channelType core.EventType, chatID, text, reason string) {
	slog.Warn("team_space_push: deferred to retry queue",
		"account", accountID, "channel", channelType, "reason", reason)
	if channelType == core.EventKakaoTalk {
		return
	}
	if qErr := s.store.EnqueueResponse(accountID, string(channelType), chatID, text); qErr != nil {
		slog.Error("team_space_push: enqueue failed", "account", accountID, "channel", channelType, "error", qErr)
	}
}

// resolveTeamSpacePushChannel picks which target channel a team-space push lands on.
// Hint matching is exact on the ChannelType string ("telegram", "slack",
// "kakao_talk"); a miss falls back to the first persistent push channel so
// delivery degrades instead of dropping.
//
// web_chat is excluded from the fallback (but honored if explicitly hinted
// — caller's explicit ask wins). web_chat is per-WebSocket-session: there
// is no durable destination to push to in the background, so silently
// landing every "no hint" team-space push on it would simply discard the
// message. Persistent channels (telegram/slack/discord/kakao_talk) own
// their own queueing semantics and are safe defaults.
func resolveTeamSpacePushChannel(channels []core.ChannelConfig, hint string) core.EventType {
	if hint != "" {
		for _, c := range channels {
			if string(c.ChannelType) == hint {
				return c.ChannelType.ToEventType()
			}
		}
	}
	for _, c := range channels {
		if c.ChannelType == core.ChannelWeb {
			continue
		}
		return c.ChannelType.ToEventType()
	}
	// Only web_chat configured — return it so the caller's "no channel
	// running" branch can enqueue to pending_responses rather than crashing.
	return channels[0].ChannelType.ToEventType()
}

// permissionTimeout returns the configured permission timeout duration.
func (s *Server) permissionTimeout() time.Duration {
	s.configMu.RLock()
	secs := s.config.Permissions.TimeoutSeconds
	s.configMu.RUnlock()
	if secs <= 0 {
		secs = 120
	}
	return time.Duration(secs) * time.Second
}

// logPermissionEvent records a permission decision to the audit log.
func (s *Server) logPermissionEvent(decision, channelType, chatID, desc, resource string) {
	if err := s.store.LogPermissionEvent(decision, channelType, chatID, desc, resource); err != nil {
		slog.Warn("permission audit log failed", "error", err)
	}
}

// retryPendingResponses periodically retries failed response deliveries.
// Uses no-drop semantics: if a channel is absent (e.g., mid-ReplaceSpawn),
// the response stays in the queue for the next tick.
func (s *Server) retryPendingResponses(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	cleanupTicker := time.NewTicker(1 * time.Hour)
	defer cleanupTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pending, err := s.store.DequeuePendingResponses(10)
			if err != nil {
				slog.Warn("retry: dequeue failed", "error", err)
				continue
			}
			for _, p := range pending {
				accountID := p.AccountID
				if accountID == "" {
					// Pre-migration rows: safe to route to default ONLY while
					// the server is single-account. Once a second account is
					// registered, an empty account_id is ambiguous and could
					// leak across the privacy boundary (spec C1) — drop it
					// instead of guessing. Uses MarkResponseDelivered for
					// cleanup until a dedicated dropped-audit table is
					// introduced (Plan B).
					if len(s.accounts.Sessions()) > 1 {
						slog.Warn("retry: PERMANENTLY dropping pending row with empty account_id (C1 privacy guard)",
							"id", p.ID, "chat_id", p.ChatID, "accounts", len(s.accounts.Sessions()))
						_ = s.store.MarkResponseDelivered(p.ID)
						continue
					}
					accountID = DefaultAccountID
				}
				ch, ok := s.spawner.GetChannel(accountID, core.EventType(p.EventType))
				if !ok {
					// Channel absent — do NOT drop. Leave in queue for next tick.
					continue
				}
				if err := ch.SendResponse(ctx, p.ChatID, p.Response, ""); err != nil {
					slog.Warn("retry: send failed",
						"id", p.ID, "retry", p.RetryCount, "error", err)
					if kept, rErr := s.store.IncrementResponseRetry(p.ID); rErr != nil {
						slog.Error("retry: increment failed", "id", p.ID, "error", rErr)
					} else if !kept {
						slog.Warn("retry: max retries exceeded, dropping", "id", p.ID)
					}
				} else {
					slog.Info("retry: delivered pending response",
						"id", p.ID, "chat_id", p.ChatID)
					_ = s.store.MarkResponseDelivered(p.ID)
				}
			}
		case <-cleanupTicker.C:
			if n, err := s.store.CleanupExpiredResponses(24); err != nil {
				slog.Warn("retry: cleanup failed", "error", err)
			} else if n > 0 {
				slog.Info("retry: cleaned up expired responses", "count", n)
			}
		}
	}
}

// ListenAndServe starts the HTTP server and account schedulers, blocking until a
// SIGINT or SIGTERM triggers graceful shutdown of both.
func (s *Server) ListenAndServe(addr string) error {
	srv := &http.Server{
		Addr:         addr,
		Handler:      s.router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Cancelable context for account scheduler goroutines.
	schedCtx, schedCancel := context.WithCancel(context.Background())
	if s.schedulers != nil {
		s.schedulers.StartAll(schedCtx)
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-done
		slog.Info("shutting down server")

		// Stop all channels first (parallel cancel + wait).
		if s.spawner != nil {
			s.spawner.StopAll()
		}

		// Stop scheduler tick loops and cancel context, then wait for
		// in-flight skill goroutines to drain before shutting down HTTP.
		if s.schedulers != nil {
			s.schedulers.StopAll()
		}
		schedCancel()
		if s.schedulers != nil {
			s.schedulers.WaitAll()
		}

		// Close MCP server connections (CommandTransport handles 5s → SIGTERM).
		if s.session.McpRegistry != nil {
			s.session.McpRegistry.Shutdown()
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	slog.Info("server listening", "addr", addr)
	return normalizeListenAndServeError(srv.ListenAndServe())
}

func normalizeListenAndServeError(err error) error {
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
