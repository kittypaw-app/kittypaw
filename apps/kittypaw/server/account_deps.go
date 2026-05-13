package server

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/jinto/kittypaw/browser"
	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/engine"
	"github.com/jinto/kittypaw/llm"
	mcpreg "github.com/jinto/kittypaw/mcp"
	"github.com/jinto/kittypaw/sandbox"
	"github.com/jinto/kittypaw/store"
)

// AccountDeps is the per-account set of dependencies server.New needs to
// build an engine.AccountRuntime for one account. The server entry point (CLI
// server start) opens these per-account resources (DB, LLM provider, sandbox)
// before handing the slice to server.New — the server package stays out
// of discovery/migration business.
//
// Fallback, McpRegistry, and LiveIndexer may be nil. Everything else is
// required. LiveIndexer is nil when [workspace] live_index = false or
// when the OS watcher could not be created (inotify limit, etc.) — the
// account is then in lazy-reindex mode.
type AccountDeps struct {
	Account           *core.Account
	Store             *store.Store
	Provider          llm.Provider
	Fallback          llm.Provider
	Sandbox           *sandbox.Sandbox
	McpRegistry       *mcpreg.Registry
	BrowserController *browser.Controller
	PkgMgr            *core.PackageManager
	APITokenMgr       *core.APITokenManager
	ServiceTokenMgr   *core.ServiceTokenManager
	Secrets           *core.SecretsStore
	LiveIndexer       *engine.LiveIndexer
	JobRuntime        *engine.ProjectJobRuntime
}

// Close releases OS-owned resources: the LiveIndexer (fsnotify watchers),
// the SQLite store, and every connected MCP server session. LiveIndexer
// closes before the store so any in-flight IndexFile call on the indexer
// finishes against a live DB. Provider/Sandbox/PkgMgr hold no file
// handles or child processes, so they are left to GC. Safe to call once;
// subsequent calls on a store that is already closed return the
// underlying error.
func (td *AccountDeps) Close() error {
	if td == nil {
		return nil
	}
	if td.LiveIndexer != nil {
		if err := td.LiveIndexer.Close(); err != nil {
			slog.Warn("close live indexer", "account", td.Account.ID, "error", err)
		}
	}
	if td.BrowserController != nil {
		if err := td.BrowserController.Close(); err != nil {
			slog.Warn("close browser controller", "account", td.Account.ID, "error", err)
		}
	}
	if td.JobRuntime != nil {
		td.JobRuntime.Close()
	}
	if td.McpRegistry != nil {
		td.McpRegistry.Shutdown()
	}
	if td.Store == nil {
		return nil
	}
	return td.Store.Close()
}

// OpenAccountDeps opens every per-account dependency needed to build an
// engine.AccountRuntime: filesystem layout, SQLite store, LLM provider (plus
// optional fallback), sandbox, secrets store, package manager, API token
// manager, and — when [mcp] is declared in config — a connected MCP
// registry.
//
// Used by both the CLI server start path (cli/main.go bootstrap) and the
// runtime account-add path (Server.AddAccount). Keeping the construction
// in one place ensures hot-added accounts are indistinguishable from
// accounts loaded at startup.
//
// On error, any resource already opened (notably the SQLite store) is
// closed before returning so callers never see a half-initialized
// AccountDeps. LoadSecretsFrom failures do NOT abort: pkgMgr is still
// constructed with a nil secrets store, preserving prior bootstrap
// behavior for accounts missing a secrets.json.
func OpenAccountDeps(t *core.Account) (*AccountDeps, error) {
	if t == nil || t.Config == nil {
		return nil, fmt.Errorf("open account deps: account or config is nil")
	}

	if err := t.EnsureDirs(); err != nil {
		return nil, fmt.Errorf("ensure dirs for %s: %w", t.ID, err)
	}

	st, err := store.Open(t.DBPath())
	if err != nil {
		return nil, fmt.Errorf("open store for %s: %w", t.ID, err)
	}

	secrets, secretsErr := core.LoadSecretsFrom(t.SecretsPath())
	if secretsErr != nil {
		slog.Warn("failed to load secrets store, package config will be limited",
			"account", t.ID, "error", secretsErr)
	}
	core.HydrateRuntimeSecrets(t.Config, secrets)

	defaultModel, ok := t.Config.RuntimeDefaultModel(secrets)
	if !ok {
		_ = st.Close()
		return nil, fmt.Errorf("create llm provider for %s: no default model configured", t.ID)
	}
	provider, err := llm.NewProviderFromModelConfig(defaultModel)
	if err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("create llm provider for %s: %w", t.ID, err)
	}
	provider = engine.NewUsageRecordingProvider(provider, st, defaultModel.Provider)

	var fallback llm.Provider
	if m, ok := t.Config.RuntimeFallbackModel(secrets); ok {
		fallback, _ = llm.NewProviderFromModelConfig(m)
		fallback = engine.NewUsageRecordingProvider(fallback, st, m.Provider)
	}

	sbox := sandbox.New(t.Config.Sandbox)
	pkgMgr := core.NewPackageManagerFrom(t.BaseDir, secrets)
	apiTokenMgr := core.NewAPITokenManager(t.BaseDir, secrets)
	serviceTokenMgr := core.NewServiceTokenManager(secrets)
	browserController := browser.NewController(browser.ControllerOptions{
		Config:  t.Config.Browser,
		BaseDir: t.BaseDir,
	})
	jobRuntime := engine.NewProjectJobRuntime(engine.ProjectJobRuntimeOptions{
		Store:     st,
		AccountID: t.ID,
		BaseDir:   t.BaseDir,
	})

	var mcpReg *mcpreg.Registry
	if len(t.Config.MCPServers) > 0 {
		if err := mcpreg.ValidateConfig(t.Config.MCPServers); err != nil {
			_ = st.Close()
			return nil, fmt.Errorf("MCP config for %s: %w", t.ID, err)
		}
		mcpReg = mcpreg.NewRegistry(t.Config.MCPServers)
		mcpReg.SetEnvResolver(func(source string) (string, error) {
			return resolveMCPEnvSource(serviceTokenMgr, source)
		})
		connectCtx, connectCancel := context.WithTimeout(context.Background(), 15*time.Second)
		if errs := mcpReg.ConnectAll(connectCtx); len(errs) > 0 {
			slog.Warn("some MCP servers failed to connect",
				"account", t.ID, "failures", len(errs))
		}
		connectCancel()
	}

	return &AccountDeps{
		Account:           t,
		Store:             st,
		Provider:          provider,
		Fallback:          fallback,
		Sandbox:           sbox,
		McpRegistry:       mcpReg,
		BrowserController: browserController,
		PkgMgr:            pkgMgr,
		APITokenMgr:       apiTokenMgr,
		ServiceTokenMgr:   serviceTokenMgr,
		Secrets:           secrets,
		JobRuntime:        jobRuntime,
	}, nil
}

func resolveMCPEnvSource(tokens *core.ServiceTokenManager, source string) (string, error) {
	ns, key, ok := strings.Cut(source, "/")
	if !ok || key != "access_token" || !strings.HasPrefix(ns, "oauth-") {
		return "", fmt.Errorf("unsupported MCP env source %q", source)
	}
	provider := strings.TrimPrefix(ns, "oauth-")
	if provider != "gmail" && provider != "x" {
		return "", fmt.Errorf("unsupported MCP env source %q", source)
	}
	if tokens == nil {
		return "", fmt.Errorf("missing %s connection — run: kittypaw connect %s", connectProviderLabel(provider), provider)
	}
	token, err := tokens.LoadAccessToken(provider)
	if err != nil {
		return "", err
	}
	if token == "" {
		return "", fmt.Errorf("missing %s connection — run: kittypaw connect %s", connectProviderLabel(provider), provider)
	}
	return token, nil
}

func connectProviderLabel(provider string) string {
	switch provider {
	case "gmail":
		return "Gmail"
	case "x":
		return "X"
	default:
		return provider
	}
}

// buildAccountRuntime wires a single AccountDeps into a ready-to-dispatch
// engine.AccountRuntime. Used both by server.New at boot and by Server.AddAccount
// at runtime so hot-added accounts are indistinguishable from those loaded
// at startup.
//
// Side effects (all best-effort, logged on failure — none abort):
//   - Seeds workspace_files from config.Sandbox.AllowedPaths.
//   - Populates AccountRuntime.AllowedPaths via RefreshAllowedPaths.
//   - Spawns a background goroutine that runs the FTS5 indexer over every
//     registered file root for this account.
//
// Team-space accounts receive a ChannelFanout wired to the shared eventCh;
// personal accounts leave runtime.Fanout == nil so the sandbox hides the
// Fanout JS global (I5 — personal cannot reach personal).
func buildAccountRuntime(td *AccountDeps, registry *core.AccountRegistry, eventCh chan<- core.Event) *engine.AccountRuntime {
	if roots := td.Account.Config.WorkspaceRoots(); len(roots) > 0 {
		if err := td.Store.SeedWorkspaceRootsFromConfig(roots); err != nil {
			slog.Error("seed workspaces from config", "account", td.Account.ID, "error", err)
		}
	}

	runtime := &engine.AccountRuntime{
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
		AccountRegistry:   registry,
		Health:            core.NewHealthState(),
		SummaryFlight:     &singleflight.Group{},
		Admission:         engine.NewRuntimeAdmission(engine.RuntimeAdmissionConfigFromCore(td.Account.Config)),
	}
	if runtime.ProjectJobRuntime == nil {
		runtime.ProjectJobRuntime = engine.NewProjectJobRuntime(engine.ProjectJobRuntimeOptions{
			Store:     td.Store,
			AccountID: td.Account.ID,
			BaseDir:   td.Account.BaseDir,
		})
		td.JobRuntime = runtime.ProjectJobRuntime
	}
	if count, err := td.Store.MarkRunningJobsFailedOnStartup("daemon stopped while the job was running"); err != nil {
		slog.Warn("mark interrupted project jobs failed", "account", td.Account.ID, "error", err)
	} else if count > 0 {
		slog.Warn("marked interrupted project jobs failed", "account", td.Account.ID, "count", count)
	}
	if td.Account.Config.IsTeamSpaceAccount() {
		runtime.Fanout = core.NewChannelFanout(eventCh, registry, td.Account.ID)
	}

	if err := runtime.RefreshAllowedPaths(); err != nil {
		slog.Warn("startup: failed to load workspace paths, file access denied by default",
			"account", td.Account.ID, "error", err)
	}

	indexer := engine.NewFTS5Indexer(td.Store)
	runtime.Indexer = indexer

	// Live indexing is opt-out via [workspace] live_index = false. Attempt
	// to open an fsnotify watcher eagerly; a failure (OS limit, etc.)
	// drops us into lazy mode — the bulk Index still runs, File.reindex
	// still works, just no automatic re-index on filesystem changes.
	var liveIdx *engine.LiveIndexer
	if td.Account.Config.Workspace.LiveIndex {
		li, err := engine.NewLiveIndexer(indexer, engine.DefaultLiveInterval, engine.DefaultLiveCap)
		if err != nil {
			slog.Warn("workspace entering lazy index mode",
				"account", td.Account.ID, "reason", "watcher init failed", "error", err)
		} else {
			liveIdx = li
			td.LiveIndexer = li
		}
	}

	go func(accountID string, st *store.Store, idx engine.Indexer, live *engine.LiveIndexer) {
		roots, err := st.ListFileIndexRoots()
		if err != nil {
			slog.Warn("startup: failed to list file roots for indexing",
				"account", accountID, "error", err)
			return
		}
		// Watch BEFORE bulk index: a filesystem change during the initial
		// walk would otherwise land after the walker passed and before
		// fsnotify is listening, leaving FTS permanently out-of-sync.
		// IndexFile is idempotent, so overlap is safe.
		if live != nil {
			live.Start()
			for _, root := range roots {
				if err := live.AddWorkspace(root.ID, root.RootPath); err != nil {
					slog.Warn("workspace entering lazy index mode",
						"account", accountID, "root_id", root.ID, "error", err)
				}
			}
		}
		for _, root := range roots {
			if _, err := idx.Index(context.Background(), root.ID, root.RootPath); err != nil {
				slog.Warn("startup: file root indexing failed",
					"account", accountID, "root_id", root.ID,
					"root_path", root.RootPath, "error", err)
			}
		}
	}(td.Account.ID, td.Store, indexer, liveIdx)

	return runtime
}
