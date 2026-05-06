package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

type ServiceTokenSet struct {
	Provider       string
	AccessToken    string
	RefreshToken   string
	TokenType      string
	ExpiresIn      int
	ExpiresAt      time.Time
	Scope          string
	Email          string
	Username       string
	ConnectBaseURL string
}

type ServiceTokenManager struct {
	secrets *SecretsStore
	client  *http.Client
	now     func() time.Time
	mu      sync.Mutex
}

func NewServiceTokenManager(secrets *SecretsStore) *ServiceTokenManager {
	return &ServiceTokenManager{
		secrets: secrets,
		client:  &http.Client{Timeout: 10 * time.Second},
		now:     time.Now,
	}
}

func ServiceTokenNamespace(provider string) string {
	return "oauth-" + strings.TrimSpace(provider)
}

func (m *ServiceTokenManager) Save(provider string, tokens ServiceTokenSet) error {
	ns := ServiceTokenNamespace(provider)
	if tokens.Provider == "" {
		tokens.Provider = provider
	}
	expiresAt := tokens.ExpiresAt
	if expiresAt.IsZero() && tokens.ExpiresIn > 0 {
		expiresAt = m.now().Add(time.Duration(tokens.ExpiresIn) * time.Second)
	}
	for _, pair := range []struct {
		key   string
		value string
	}{
		{"provider", tokens.Provider},
		{"access_token", tokens.AccessToken},
		{"refresh_token", tokens.RefreshToken},
		{"token_type", tokens.TokenType},
		{"scope", tokens.Scope},
		{"email", tokens.Email},
		{"username", tokens.Username},
		{"connect_base_url", strings.TrimRight(tokens.ConnectBaseURL, "/")},
	} {
		if err := m.saveOrDelete(ns, pair.key, pair.value); err != nil {
			return fmt.Errorf("save %s: %w", pair.key, err)
		}
	}
	if expiresAt.IsZero() {
		return m.secrets.Delete(ns, "expires_at")
	}
	return m.secrets.Set(ns, "expires_at", expiresAt.UTC().Format(time.RFC3339))
}

func (m *ServiceTokenManager) LoadAccessToken(provider string) (string, error) {
	ns := ServiceTokenNamespace(provider)
	accessToken, ok := m.secrets.Get(ns, "access_token")
	if !ok || accessToken == "" {
		return "", nil
	}
	expiresAt, ok := m.loadExpiresAt(ns)
	if !ok || m.now().Before(expiresAt.Add(-5*time.Minute)) {
		return accessToken, nil
	}
	return m.refresh(provider)
}

func (m *ServiceTokenManager) Refresh(provider string) (ServiceTokenSet, error) {
	access, err := m.refresh(provider)
	if err != nil {
		return ServiceTokenSet{}, err
	}
	ns := ServiceTokenNamespace(provider)
	tokens := ServiceTokenSet{Provider: provider, AccessToken: access}
	if refresh, ok := m.secrets.Get(ns, "refresh_token"); ok {
		tokens.RefreshToken = refresh
	}
	if base, ok := m.secrets.Get(ns, "connect_base_url"); ok {
		tokens.ConnectBaseURL = base
	}
	if scope, ok := m.secrets.Get(ns, "scope"); ok {
		tokens.Scope = scope
	}
	if email, ok := m.secrets.Get(ns, "email"); ok {
		tokens.Email = email
	}
	if username, ok := m.secrets.Get(ns, "username"); ok {
		tokens.Username = username
	}
	if expiresAt, ok := m.loadExpiresAt(ns); ok {
		tokens.ExpiresAt = expiresAt
	}
	return tokens, nil
}

func (m *ServiceTokenManager) refresh(provider string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ns := ServiceTokenNamespace(provider)
	accessToken, _ := m.secrets.Get(ns, "access_token")
	if expiresAt, ok := m.loadExpiresAt(ns); ok && accessToken != "" && m.now().Before(expiresAt.Add(-5*time.Minute)) {
		return accessToken, nil
	}

	refreshToken, ok := m.secrets.Get(ns, "refresh_token")
	if !ok || refreshToken == "" {
		return "", fmt.Errorf("missing %s refresh token — run: kittypaw connect %s", provider, provider)
	}
	connectBaseURL, ok := m.secrets.Get(ns, "connect_base_url")
	if !ok || connectBaseURL == "" {
		return "", fmt.Errorf("missing %s connect endpoint — run: kittypaw connect %s", provider, provider)
	}
	providerPath := strings.TrimSpace(provider)
	if providerPath == "" || strings.Contains(providerPath, "/") {
		return "", fmt.Errorf("invalid service provider %q", provider)
	}

	payload, _ := json.Marshal(map[string]string{"refresh_token": refreshToken})
	resp, err := m.client.Post(strings.TrimRight(connectBaseURL, "/")+"/connect/"+providerPath+"/refresh", "application/json", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("refresh request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("refresh failed with status %d — run: kittypaw connect %s", resp.StatusCode, provider)
	}
	var result struct {
		Provider     string `json:"provider"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
		Email        string `json:"email"`
		Username     string `json:"username"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode refresh response: %w", err)
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("refresh response missing access_token — run: kittypaw connect %s", provider)
	}

	existingEmail, _ := m.secrets.Get(ns, "email")
	if result.Email == "" {
		result.Email = existingEmail
	}
	existingUsername, _ := m.secrets.Get(ns, "username")
	if result.Username == "" {
		result.Username = existingUsername
	}
	if result.RefreshToken == "" {
		result.RefreshToken = refreshToken
	}
	if result.Provider == "" {
		result.Provider = provider
	}
	if err := m.Save(provider, ServiceTokenSet{
		Provider:       result.Provider,
		AccessToken:    result.AccessToken,
		RefreshToken:   result.RefreshToken,
		TokenType:      result.TokenType,
		ExpiresIn:      result.ExpiresIn,
		Scope:          result.Scope,
		Email:          result.Email,
		Username:       result.Username,
		ConnectBaseURL: connectBaseURL,
	}); err != nil {
		return "", err
	}
	return result.AccessToken, nil
}

func (m *ServiceTokenManager) loadExpiresAt(ns string) (time.Time, bool) {
	raw, ok := m.secrets.Get(ns, "expires_at")
	if !ok || raw == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func (m *ServiceTokenManager) saveOrDelete(ns, key, value string) error {
	if value == "" {
		return m.secrets.Delete(ns, key)
	}
	return m.secrets.Set(ns, key, value)
}
