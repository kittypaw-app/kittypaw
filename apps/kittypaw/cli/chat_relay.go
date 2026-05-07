package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/remote/chatrelay"
	"github.com/jinto/kittypaw/server"
)

func chatRelayConnectorConfigs(deps []*server.AccountDeps, daemonVersion string, dispatchReady bool) []chatrelay.ConnectorConfig {
	runtimeConfigs := chatRelayConnectorRuntimeConfigs(deps, daemonVersion, dispatchReady)
	configs := make([]chatrelay.ConnectorConfig, 0, len(runtimeConfigs))
	for _, runtimeCfg := range runtimeConfigs {
		configs = append(configs, runtimeCfg.Config)
	}
	return configs
}

func chatRelayConnectorRuntimeConfigs(deps []*server.AccountDeps, daemonVersion string, dispatchReady bool) []chatRelayConnectorRuntimeConfig {
	deps = freshChatRelayAccountDeps(deps)
	configs := make([]chatRelayConnectorRuntimeConfig, 0, len(deps))
	groupIndex := make(map[chatRelayConnectorKey]int)
	for _, dep := range deps {
		runtimeCfg, ok := buildChatRelayConnectorRuntimeConfig(dep, daemonVersion, dispatchReady)
		if !ok {
			continue
		}
		cfg := runtimeCfg.Config
		key := chatRelayConnectorKey{
			RelayURL:   cfg.RelayURL,
			Credential: cfg.Credential,
			DeviceID:   cfg.DeviceID,
		}
		if idx, exists := groupIndex[key]; exists {
			for _, account := range cfg.LocalAccounts {
				if !containsString(configs[idx].Config.LocalAccounts, account) {
					configs[idx].Config.LocalAccounts = append(configs[idx].Config.LocalAccounts, account)
				}
			}
			configs[idx].sources = append(configs[idx].sources, runtimeCfg.sources...)
		} else {
			groupIndex[key] = len(configs)
			configs = append(configs, runtimeCfg)
		}
	}
	for i := range configs {
		if credential, ok := configs[i].EnsureFreshCredential(); ok {
			configs[i].Config.Credential = credential
		}
	}
	return configs
}

func freshChatRelayAccountDeps(deps []*server.AccountDeps) []*server.AccountDeps {
	out := make([]*server.AccountDeps, 0, len(deps))
	for _, dep := range deps {
		if dep == nil || dep.Account == nil || dep.Account.BaseDir == "" {
			out = append(out, dep)
			continue
		}
		freshSecrets, err := core.LoadSecretsFrom(dep.Account.SecretsPath())
		if err != nil {
			slog.Warn("chat relay: failed to reload account secrets", "account", dep.Account.ID, "error", err)
			out = append(out, dep)
			continue
		}
		copyDep := *dep
		copyDep.Secrets = freshSecrets
		copyDep.APITokenMgr = core.NewAPITokenManager(dep.Account.BaseDir, freshSecrets)
		out = append(out, &copyDep)
	}
	return out
}

type chatRelayConnectorKey struct {
	RelayURL   string
	Credential string
	DeviceID   string
}

type chatRelayConnectorRuntimeConfig struct {
	Config  chatrelay.ConnectorConfig
	sources []chatRelayCredentialSource
}

type chatRelayCredentialSource struct {
	apiURL      string
	authBaseURL string
	mgr         *core.APITokenManager
}

func (cfg chatRelayConnectorRuntimeConfig) RefreshCredential(ctx context.Context) (string, error) {
	var lastErr error
	for i, source := range cfg.sources {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if source.mgr == nil {
			continue
		}
		tokens, err := source.mgr.RefreshChatRelayDeviceToken(source.authBaseURL, source.apiURL)
		if err != nil {
			lastErr = err
			continue
		}
		for j, target := range cfg.sources {
			if i == j || target.mgr == nil {
				continue
			}
			if err := target.mgr.SaveChatRelayDeviceTokens(target.apiURL, tokens); err != nil {
				return "", fmt.Errorf("save refreshed chat relay token for grouped account: %w", err)
			}
		}
		return tokens.AccessToken, nil
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("no chat relay credential refresh source")
}

func (cfg chatRelayConnectorRuntimeConfig) EnsureFreshCredential() (string, bool) {
	for i, source := range cfg.sources {
		if source.mgr == nil {
			continue
		}
		tokens, err := source.mgr.EnsureChatRelayDeviceAccessToken(source.authBaseURL, source.apiURL)
		if err != nil {
			continue
		}
		for j, target := range cfg.sources {
			if i == j || target.mgr == nil {
				continue
			}
			_ = target.mgr.SaveChatRelayDeviceTokens(target.apiURL, tokens)
		}
		return tokens.AccessToken, true
	}
	return "", false
}

func buildChatRelayConnectorRuntimeConfig(dep *server.AccountDeps, daemonVersion string, dispatchReady bool) (chatRelayConnectorRuntimeConfig, bool) {
	if dep == nil || dep.Account == nil || dep.Account.ID == "" || dep.Secrets == nil || dep.APITokenMgr == nil {
		return chatRelayConnectorRuntimeConfig{}, false
	}
	apiURL := accountAPIURL(dep.Secrets)
	relayURL, ok := dep.APITokenMgr.LoadSpaceBaseURL(apiURL)
	if !ok || relayURL == "" {
		relayURL, ok = dep.APITokenMgr.LoadChatRelayURL(apiURL)
	}
	if !ok || relayURL == "" {
		return chatRelayConnectorRuntimeConfig{}, false
	}
	tokens, ok := dep.APITokenMgr.LoadChatRelayDeviceTokens(apiURL)
	if !ok {
		return chatRelayConnectorRuntimeConfig{}, false
	}
	authBaseURL := dep.APITokenMgr.ResolveAuthBaseURL(apiURL)
	cfg := chatrelay.ConnectorConfig{
		RelayURL:      relayURL,
		Credential:    tokens.AccessToken,
		DeviceID:      tokens.DeviceID,
		LocalAccounts: []string{dep.Account.ID},
		DaemonVersion: daemonVersion,
		Capabilities:  []string{},
	}
	if dispatchReady {
		cfg.Capabilities = nil
	}
	return chatRelayConnectorRuntimeConfig{
		Config: cfg,
		sources: []chatRelayCredentialSource{{
			apiURL:      apiURL,
			authBaseURL: authBaseURL,
			mgr:         dep.APITokenMgr,
		}},
	}, true
}

type chatRelayConnectorManager struct {
	parentCtx     context.Context
	deps          []*server.AccountDeps
	daemonVersion string
	dispatcher    chatrelay.Dispatcher

	mu     sync.Mutex
	cancel context.CancelFunc
}

func newChatRelayConnectorManager(ctx context.Context, deps []*server.AccountDeps, daemonVersion string, dispatcher chatrelay.Dispatcher) *chatRelayConnectorManager {
	return &chatRelayConnectorManager{
		parentCtx:     ctx,
		deps:          deps,
		daemonVersion: daemonVersion,
		dispatcher:    dispatcher,
	}
}

func (m *chatRelayConnectorManager) Reload(_ context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if m.parentCtx.Err() != nil {
		return nil
	}
	runtimeConfigs := chatRelayConnectorRuntimeConfigs(m.deps, m.daemonVersion, m.dispatcher != nil)
	if len(runtimeConfigs) == 0 {
		slog.Info("chat relay connector not configured")
		return nil
	}
	connectorCtx, cancel := context.WithCancel(m.parentCtx)
	m.cancel = cancel
	slog.Info("chat relay connector reload", "connectors", len(runtimeConfigs))
	for _, runtimeCfg := range runtimeConfigs {
		connector := &chatrelay.Connector{
			Config:            runtimeCfg.Config,
			Dispatcher:        m.dispatcher,
			RefreshCredential: runtimeCfg.RefreshCredential,
		}
		go connector.Run(connectorCtx, chatrelay.RunOptions{
			Logf: func(format string, args ...any) {
				slog.Info("chat relay connector", "message", formatLog(format, args...))
			},
		})
	}
	return nil
}

func accountAPIURL(secrets *core.SecretsStore) string {
	if secrets != nil {
		if apiURL, ok := secrets.Get("kittypaw-api", "api_url"); ok && apiURL != "" {
			return apiURL
		}
	}
	return core.DefaultAPIServerURL
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func formatLog(format string, args ...any) string {
	if len(args) == 0 {
		return format
	}
	return fmt.Sprintf(format, args...)
}
