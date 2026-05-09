package server

import (
	"fmt"
	"log/slog"

	"golang.org/x/sync/singleflight"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/engine"
	"github.com/jinto/kittypaw/llm"
	"github.com/jinto/kittypaw/sandbox"
)

func (s *Server) validateAccountConfigUpdateWithKakaoAPIURL(accountID string, cfg *core.Config, apiURL string) error {
	s.accountMu.Lock()
	defer s.accountMu.Unlock()
	return s.validateAccountConfigUpdateWithKakaoAPIURLLocked(accountID, cfg, apiURL)
}

func (s *Server) validateAccountConfigUpdateWithKakaoAPIURLLocked(accountID string, cfg *core.Config, apiURL string) error {
	if cfg == nil {
		return fmt.Errorf("account config is nil")
	}
	snapshot := make(map[string][]core.ChannelConfig, len(s.accountList)+1)
	accounts := make([]*core.Account, 0, len(s.accountList)+1)
	seen := false
	for _, peer := range s.accountList {
		if peer == nil || peer.Config == nil {
			continue
		}
		if peer.ID == accountID {
			proposedAccount := *peer
			proposedCfg := *cfg
			proposedAccount.Config = &proposedCfg
			accounts = append(accounts, &proposedAccount)
			snapshot[peer.ID] = accountChannelsForValidation(peer.ID, proposedCfg.Channels, apiURL)
			seen = true
			continue
		}
		accounts = append(accounts, peer)
		snapshot[peer.ID] = accountChannelsForValidation(peer.ID, peer.Config.Channels, "")
	}
	if !seen {
		accounts = append(accounts, &core.Account{ID: accountID, Config: cfg})
		snapshot[accountID] = accountChannelsForValidation(accountID, cfg.Channels, apiURL)
	}

	if err := core.ValidateAccountChannels(snapshot); err != nil {
		return fmt.Errorf("channel validation: %w", err)
	}
	if err := core.ValidateTeamSpaceAccounts(accounts); err != nil {
		return fmt.Errorf("team space validation: %w", err)
	}
	if err := core.ValidateTeamSpaceMemberships(accounts); err != nil {
		return fmt.Errorf("team-space membership validation: %w", err)
	}
	return nil
}

func accountChannelsForValidation(accountID string, channels []core.ChannelConfig, apiURL string) []core.ChannelConfig {
	copied := append([]core.ChannelConfig(nil), channels...)
	if apiURL != "" {
		injectKakaoWSURLForAPIURL(accountID, copied, apiURL)
	} else {
		core.InjectKakaoWSURL(accountID, copied)
	}
	return copied
}

func injectKakaoWSURLForAPIURL(accountID string, channels []core.ChannelConfig, apiURL string) {
	secrets, err := core.LoadAccountSecrets(accountID)
	if err != nil {
		return
	}
	mgr := core.NewAPITokenManager("", secrets)
	wsURL, ok := mgr.LoadKakaoRelayWSURL(apiURL)
	if !ok || wsURL == "" {
		return
	}
	for i := range channels {
		if channels[i].ChannelType == core.ChannelKakaoTalk {
			channels[i].KakaoWSURL = wsURL
		}
	}
}

func (s *Server) applyAccountConfigLocked(accountID string, cfg *core.Config) (*engine.Scheduler, error) {
	if cfg == nil {
		return nil, fmt.Errorf("account config is nil")
	}
	td := s.accountDeps[accountID]
	if td == nil || td.Account == nil {
		return nil, fmt.Errorf("account %q dependencies unavailable", accountID)
	}

	cfgCopy := *cfg
	core.HydrateRuntimeSecrets(&cfgCopy, td.Secrets)
	defaultModel, ok := cfgCopy.RuntimeDefaultModel(td.Secrets)
	if !ok {
		return nil, fmt.Errorf("create llm provider: no default model configured")
	}
	provider, err := llm.NewProviderFromModelConfig(defaultModel)
	if err != nil {
		return nil, fmt.Errorf("create llm provider: %w", err)
	}
	provider = engine.NewUsageRecordingProvider(provider, td.Store, defaultModel.Provider)
	var fallback llm.Provider
	if m, ok := cfgCopy.RuntimeFallbackModel(td.Secrets); ok {
		fallback, _ = llm.NewProviderFromModelConfig(m)
		fallback = engine.NewUsageRecordingProvider(fallback, td.Store, m.Provider)
	}

	td.Account.Config = &cfgCopy
	td.Provider = provider
	td.Fallback = fallback
	td.Sandbox = sandbox.New(cfgCopy.Sandbox)

	for _, peer := range s.accountList {
		if peer != nil && peer.ID == accountID {
			peer.Config = td.Account.Config
			break
		}
	}
	if s.accountRegistry != nil {
		s.accountRegistry.Register(td.Account)
	}

	oldSession := s.accounts.Session(accountID)
	newSession := s.rebuildSessionForConfigLocked(td, oldSession)
	var oldScheduler *engine.Scheduler
	if accountID == s.defaultAccountID() && oldSession != nil {
		oldSession.Provider = newSession.Provider
		oldSession.FallbackProvider = newSession.FallbackProvider
		oldSession.Sandbox = newSession.Sandbox
		oldSession.Store = newSession.Store
		oldSession.Config = newSession.Config
		oldSession.McpRegistry = newSession.McpRegistry
		oldSession.BrowserController = newSession.BrowserController
		oldSession.BaseDir = newSession.BaseDir
		oldSession.PackageManager = newSession.PackageManager
		oldSession.APITokenMgr = newSession.APITokenMgr
		oldSession.ServiceTokenMgr = newSession.ServiceTokenMgr
		oldSession.ProjectJobRuntime = newSession.ProjectJobRuntime
		oldSession.AccountID = newSession.AccountID
		oldSession.AccountRegistry = newSession.AccountRegistry
		oldSession.Fanout = newSession.Fanout
		if err := oldSession.RefreshAllowedPaths(); err != nil {
			slog.Warn("setup: failed to refresh allowed paths after config update",
				"account", td.Account.ID, "error", err)
		}
		newSession = oldSession
	}
	s.accounts.Register(accountID, newSession)
	if accountID == s.defaultAccountID() {
		s.configMu.Lock()
		s.config = td.Account.Config
		s.configMu.Unlock()
		s.session = newSession
		s.store = td.Store
		s.pkgManager = td.PkgMgr
	} else {
		if s.schedulers == nil {
			s.schedulers = NewAccountSchedulers()
		}
		oldScheduler = s.schedulers.Replace(accountID, engine.NewScheduler(newSession, td.PkgMgr))
	}
	return oldScheduler, nil
}

func (s *Server) rebuildSessionForConfigLocked(td *AccountDeps, old *engine.Session) *engine.Session {
	health := core.NewHealthState()
	var budget *engine.SharedTokenBudget
	var indexer engine.Indexer
	var pipeline *engine.PipelineState
	if old != nil {
		if old.Health != nil {
			health = old.Health
		}
		budget = old.Budget
		indexer = old.Indexer
		pipeline = old.Pipeline
	}

	sess := &engine.Session{
		Provider:          td.Provider,
		FallbackProvider:  td.Fallback,
		Sandbox:           td.Sandbox,
		Store:             td.Store,
		Config:            td.Account.Config,
		McpRegistry:       td.McpRegistry,
		BrowserController: td.BrowserController,
		BaseDir:           td.Account.BaseDir,
		PackageManager:    td.PkgMgr,
		APITokenMgr:       td.APITokenMgr,
		ServiceTokenMgr:   td.ServiceTokenMgr,
		ProjectJobRuntime: td.JobRuntime,
		AccountID:         td.Account.ID,
		AccountRegistry:   s.accountRegistry,
		Health:            health,
		SummaryFlight:     &singleflight.Group{},
		Budget:            budget,
		Indexer:           indexer,
		Pipeline:          pipeline,
	}
	if sess.ProjectJobRuntime == nil {
		sess.ProjectJobRuntime = engine.NewProjectJobRuntime(engine.ProjectJobRuntimeOptions{
			Store:     td.Store,
			AccountID: td.Account.ID,
			BaseDir:   td.Account.BaseDir,
		})
		td.JobRuntime = sess.ProjectJobRuntime
	}
	if td.Account.Config.IsTeamSpaceAccount() {
		sess.Fanout = core.NewChannelFanout(s.eventCh, s.accountRegistry, td.Account.ID)
	}

	if roots := td.Account.Config.WorkspaceRoots(); len(roots) > 0 {
		if err := td.Store.SeedWorkspaceRootsFromConfig(roots); err != nil {
			slog.Error("seed workspaces from config", "account", td.Account.ID, "error", err)
		}
	}
	if err := sess.RefreshAllowedPaths(); err != nil {
		slog.Warn("setup: failed to refresh allowed paths after config update",
			"account", td.Account.ID, "error", err)
	}
	return sess
}
