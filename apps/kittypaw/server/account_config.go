package server

import (
	"fmt"
	"log/slog"

	"golang.org/x/sync/singleflight"

	"github.com/jinto/kittypaw/browser"
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

	secrets := td.Secrets
	if td.Account.SecretsPath() != "" {
		loaded, err := core.LoadSecretsFrom(td.Account.SecretsPath())
		if err != nil {
			return nil, fmt.Errorf("load account secrets: %w", err)
		}
		secrets = loaded
	}

	cfgCopy := *cfg
	core.HydrateRuntimeSecrets(&cfgCopy, secrets)
	if td.RateLimiters == nil {
		td.RateLimiters = engine.NewLLMRateLimiterRegistry()
	}
	if td.DailyTokenLimiter == nil {
		td.DailyTokenLimiter = engine.NewDailyTokenLimiter()
	}
	defaultModel, ok := cfgCopy.RuntimeDefaultModel(secrets)
	if !ok {
		return nil, fmt.Errorf("create llm provider: no default model configured")
	}
	provider, err := llm.NewProviderFromModelConfig(defaultModel)
	if err != nil {
		return nil, fmt.Errorf("create llm provider: %w", err)
	}
	provider = engine.NewRateLimitedProvider(provider, td.RateLimiters, defaultModel)
	provider = engine.NewUsageRecordingProvider(provider, td.Store, defaultModel.Provider)
	provider = engine.NewDailyTokenLimitedProvider(provider, td.DailyTokenLimiter, td.Store, cfgCopy.Features.DailyTokenLimit, defaultModel)
	var fallback llm.Provider
	if m, ok := cfgCopy.RuntimeFallbackModel(secrets); ok {
		fallback, _ = llm.NewProviderFromModelConfig(m)
		fallback = engine.NewRateLimitedProvider(fallback, td.RateLimiters, m)
		fallback = engine.NewUsageRecordingProvider(fallback, td.Store, m.Provider)
		fallback = engine.NewDailyTokenLimitedProvider(fallback, td.DailyTokenLimiter, td.Store, cfgCopy.Features.DailyTokenLimit, m)
	}

	td.Account.Config = &cfgCopy
	td.Secrets = secrets
	td.Provider = provider
	td.Fallback = fallback
	td.Sandbox = sandbox.New(cfgCopy.Sandbox)
	td.PkgMgr = core.NewPackageManagerFrom(td.Account.BaseDir, secrets)
	td.APITokenMgr = core.NewAPITokenManager(td.Account.BaseDir, secrets)
	td.ServiceTokenMgr = core.NewServiceTokenManager(secrets)
	td.BrowserController = browser.NewController(browser.ControllerOptions{
		Config:  cfgCopy.Browser,
		BaseDir: td.Account.BaseDir,
	})

	for _, peer := range s.accountList {
		if peer != nil && peer.ID == accountID {
			peer.Config = td.Account.Config
			break
		}
	}
	if s.accountRegistry != nil {
		s.accountRegistry.Register(td.Account)
	}

	oldRuntime := s.accounts.Runtime(accountID)
	newRuntime := s.rebuildRuntimeForConfigLocked(td, oldRuntime)
	var oldScheduler *engine.Scheduler
	s.accounts.Register(accountID, newRuntime)
	if s.schedulers == nil {
		s.schedulers = NewAccountSchedulers()
	}
	oldScheduler = s.schedulers.Replace(accountID, engine.NewScheduler(newRuntime, td.PkgMgr))
	if accountID == s.defaultAccountID() {
		s.configMu.Lock()
		s.config = td.Account.Config
		s.configMu.Unlock()
		s.runtime = newRuntime
		s.store = td.Store
		s.pkgManager = td.PkgMgr
	}
	return oldScheduler, nil
}

func (s *Server) rebuildRuntimeForConfigLocked(td *AccountDeps, old *engine.AccountRuntime) *engine.AccountRuntime {
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
		RateLimiters:      td.RateLimiters,
		DailyTokenLimiter: td.DailyTokenLimiter,
		AccountID:         td.Account.ID,
		AccountRegistry:   s.accountRegistry,
		Health:            health,
		SummaryFlight:     &singleflight.Group{},
		Budget:            budget,
		Indexer:           indexer,
		Pipeline:          pipeline,
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
	if td.Account.Config.IsTeamSpaceAccount() {
		runtime.Fanout = core.NewChannelFanout(s.eventCh, s.accountRegistry, td.Account.ID)
	}
	if old != nil {
		runtime.SetActiveModel(old.GetActiveModel())
	}
	s.attachRuntimeNotifier(td.Account.ID, runtime)
	s.attachRuntimeEventSink(td.Account.ID, runtime)

	if roots := td.Account.Config.WorkspaceRoots(); len(roots) > 0 {
		if err := td.Store.SeedWorkspaceRootsFromConfig(roots); err != nil {
			slog.Error("seed workspaces from config", "account", td.Account.ID, "error", err)
		}
	}
	if err := runtime.RefreshAllowedPaths(); err != nil {
		slog.Warn("setup: failed to refresh allowed paths after config update",
			"account", td.Account.ID, "error", err)
	}
	return runtime
}
