package core

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// APITokenManager handles storage and auto-refresh of kittypaw-api tokens.
type APITokenManager struct {
	secrets *SecretsStore
	mu      sync.Mutex
	client  *http.Client
}

// NewAPITokenManager creates a manager backed by the given secrets store.
func NewAPITokenManager(_ string, secrets *SecretsStore) *APITokenManager {
	return &APITokenManager{
		secrets: secrets,
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

func (m *APITokenManager) ResolveAPIURL() string {
	if m == nil || m.secrets == nil {
		return DefaultAPIServerURL
	}
	if got, ok := m.secrets.Get("kittypaw-api", "api_url"); ok && strings.TrimSpace(got) != "" {
		return strings.TrimRight(strings.TrimSpace(got), "/")
	}
	return DefaultAPIServerURL
}

// NamespaceForURL converts an API URL to a secrets namespace.
// "http://localhost:8080" → "kittypaw-api/localhost:8080"
func NamespaceForURL(apiURL string) string {
	parsed, err := url.Parse(apiURL)
	if err != nil {
		return "kittypaw-api/unknown"
	}
	return "kittypaw-api/" + parsed.Host
}

// SaveTokens stores the access token, refresh token, and API URL.
func (m *APITokenManager) SaveTokens(apiURL, accessToken, refreshToken string) error {
	ns := NamespaceForURL(apiURL)
	if err := m.secrets.Set(ns, "access_token", accessToken); err != nil {
		return fmt.Errorf("save access_token: %w", err)
	}
	if err := m.secrets.Set(ns, "refresh_token", refreshToken); err != nil {
		return fmt.Errorf("save refresh_token: %w", err)
	}
	if err := m.secrets.Set(ns, "api_url", apiURL); err != nil {
		return fmt.Errorf("save api_url: %w", err)
	}
	if err := m.secrets.Set("kittypaw-api", "api_url", apiURL); err != nil {
		return fmt.Errorf("save default api_url: %w", err)
	}
	return nil
}

// LoadAccessToken returns a valid access token, refreshing if expired.
// Returns ("", nil) if not logged in.
func (m *APITokenManager) LoadAccessToken(apiURL string) (string, error) {
	ns := NamespaceForURL(apiURL)

	accessToken, ok := m.secrets.Get(ns, "access_token")
	if !ok || accessToken == "" {
		return "", nil // not logged in
	}

	if !isJWTExpired(accessToken) {
		return accessToken, nil
	}

	// Token expired — refresh under mutex (single-flight).
	m.mu.Lock()
	defer m.mu.Unlock()

	// Re-check after acquiring lock (another goroutine may have refreshed).
	accessToken, _ = m.secrets.Get(ns, "access_token")
	if accessToken != "" && !isJWTExpired(accessToken) {
		return accessToken, nil
	}

	refreshToken, ok := m.secrets.Get(ns, "refresh_token")
	if !ok || refreshToken == "" {
		return "", fmt.Errorf("no refresh token available, please run: kittypaw login")
	}

	newAccess, err := m.refreshTokens(apiURL, refreshToken)
	if err != nil {
		return "", fmt.Errorf("token refresh failed: %w (please run: kittypaw login)", err)
	}
	return newAccess, nil
}

// Namespace invariant: service URLs are stored under the portal host
// (NamespaceForURL(apiURL)). The stored api_base_url may point to a different
// host — that's intentional. The namespace tracks auth identity, not service
// topology, so token refresh keeps working unchanged across relay migrations.

// saveOrDelete writes value under (ns, key), or deletes the key when value is
// empty. Used by Save*URL helpers so that a /discovery response with an empty
// field erases a stale value instead of persisting "".
func (m *APITokenManager) saveOrDelete(ns, key, value string) error {
	if value == "" {
		return m.secrets.Delete(ns, key)
	}
	return m.secrets.Set(ns, key, value)
}

const (
	chatRelayURLKey         = "chat_relay_url"
	chatRelayDeviceIDKey    = "chat_relay_device_id"
	chatRelayAccessTokenKey = "chat_relay_access_token"
	chatRelayRefreshKey     = "chat_relay_refresh_token"
	kakaoRelayURLKey        = "kakao_relay_url"
	kakaoRelayWSURLKey      = "kakao_relay_ws_url"
	authBaseURLKey          = "auth_base_url"
	connectBaseURLKey       = "connect_base_url"
	spaceBaseURLKey         = "space_base_url"
)

// ErrChatRelayDeviceRefreshInvalid means the hosted relay rejected the stored
// device refresh token, so the local device credential has been cleared.
var ErrChatRelayDeviceRefreshInvalid = errors.New("chat relay device refresh token invalid")

// SaveChatRelayURL stores the chat relay server base URL from GET /discovery.
// Empty value deletes the key so stale URLs don't survive relay migrations.
func (m *APITokenManager) SaveChatRelayURL(apiURL, chatRelayURL string) error {
	return m.saveOrDelete(NamespaceForURL(apiURL), chatRelayURLKey, chatRelayURL)
}

// LoadChatRelayURL returns the stored chat relay server base URL.
func (m *APITokenManager) LoadChatRelayURL(apiURL string) (string, bool) {
	return m.secrets.Get(NamespaceForURL(apiURL), chatRelayURLKey)
}

// SaveChatRelayDeviceID stores the API-issued device ID used for the chat
// relay hello frame. Empty value deletes the key.
func (m *APITokenManager) SaveChatRelayDeviceID(apiURL, deviceID string) error {
	return m.saveOrDelete(NamespaceForURL(apiURL), chatRelayDeviceIDKey, deviceID)
}

// LoadChatRelayDeviceID returns the stored chat relay device ID.
func (m *APITokenManager) LoadChatRelayDeviceID(apiURL string) (string, bool) {
	return m.secrets.Get(NamespaceForURL(apiURL), chatRelayDeviceIDKey)
}

// ChatRelayDeviceTokens are the API-issued credentials the local server uses
// to connect outbound to the hosted chat relay. The server treats AccessToken
// as an opaque Bearer token; kittychat validates the RS256 JWT.
type ChatRelayDeviceTokens struct {
	DeviceID     string
	AccessToken  string
	RefreshToken string
}

// ChatRelayDevicePairRequest is sent to POST {auth_base_url}/devices/pair.
type ChatRelayDevicePairRequest struct {
	Name         string   `json:"name,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// SaveChatRelayDeviceTokens stores the API-issued device id and rotating
// access/refresh tokens used by the chat relay connector.
func (m *APITokenManager) SaveChatRelayDeviceTokens(apiURL string, tokens ChatRelayDeviceTokens) error {
	if err := m.SaveChatRelayDeviceID(apiURL, tokens.DeviceID); err != nil {
		return err
	}
	ns := NamespaceForURL(apiURL)
	if err := m.saveOrDelete(ns, chatRelayAccessTokenKey, tokens.AccessToken); err != nil {
		return err
	}
	return m.saveOrDelete(ns, chatRelayRefreshKey, tokens.RefreshToken)
}

// LoadChatRelayDeviceTokens returns a complete stored device credential set.
func (m *APITokenManager) LoadChatRelayDeviceTokens(apiURL string) (ChatRelayDeviceTokens, bool) {
	deviceID, okDevice := m.LoadChatRelayDeviceID(apiURL)
	ns := NamespaceForURL(apiURL)
	access, okAccess := m.secrets.Get(ns, chatRelayAccessTokenKey)
	refresh, okRefresh := m.secrets.Get(ns, chatRelayRefreshKey)
	if !okDevice || !okAccess || !okRefresh || deviceID == "" || access == "" || refresh == "" {
		return ChatRelayDeviceTokens{}, false
	}
	return ChatRelayDeviceTokens{DeviceID: deviceID, AccessToken: access, RefreshToken: refresh}, true
}

// ClearChatRelayDeviceTokens removes locally stored chat relay device tokens.
func (m *APITokenManager) ClearChatRelayDeviceTokens(apiURL string) error {
	ns := NamespaceForURL(apiURL)
	for _, key := range []string{chatRelayDeviceIDKey, chatRelayAccessTokenKey, chatRelayRefreshKey} {
		if err := m.secrets.Delete(ns, key); err != nil {
			return err
		}
	}
	return nil
}

// SaveAuthBaseURL stores the auth service base URL from GET /discovery.
// Empty value deletes the key so stale topology does not persist.
func (m *APITokenManager) SaveAuthBaseURL(apiURL, authBaseURL string) error {
	return m.saveOrDelete(NamespaceForURL(apiURL), authBaseURLKey, strings.TrimRight(authBaseURL, "/"))
}

// LoadAuthBaseURL returns the stored auth base URL.
func (m *APITokenManager) LoadAuthBaseURL(apiURL string) (string, bool) {
	return m.secrets.Get(NamespaceForURL(apiURL), authBaseURLKey)
}

// ResolveAuthBaseURL returns the stored auth_base_url or the default
// <api_url>/auth topology used by collapsed local/dev deployments.
func (m *APITokenManager) ResolveAuthBaseURL(apiURL string) string {
	if got, ok := m.LoadAuthBaseURL(apiURL); ok && got != "" {
		return strings.TrimRight(got, "/")
	}
	return strings.TrimRight(apiURL, "/") + "/auth"
}

// SaveConnectBaseURL stores the Connect surface base URL from GET /discovery.
// Empty value deletes the key so stale topology does not persist.
func (m *APITokenManager) SaveConnectBaseURL(apiURL, connectBaseURL string) error {
	return m.saveOrDelete(NamespaceForURL(apiURL), connectBaseURLKey, strings.TrimRight(connectBaseURL, "/"))
}

// LoadConnectBaseURL returns the stored Connect surface base URL.
func (m *APITokenManager) LoadConnectBaseURL(apiURL string) (string, bool) {
	return m.secrets.Get(NamespaceForURL(apiURL), connectBaseURLKey)
}

// ResolveConnectBaseURL returns the discovered Connect URL, a conservative
// portal.* -> connect.* production fallback, or the API URL for collapsed
// local/dev deployments.
func (m *APITokenManager) ResolveConnectBaseURL(apiURL string) string {
	if got, ok := m.LoadConnectBaseURL(apiURL); ok && got != "" {
		return strings.TrimRight(got, "/")
	}
	trimmed := strings.TrimRight(apiURL, "/")
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return trimmed
	}
	if parsed.Scheme == "https" && strings.HasPrefix(parsed.Host, "portal.") {
		parsed.Host = "connect." + strings.TrimPrefix(parsed.Host, "portal.")
		return strings.TrimRight(parsed.String(), "/")
	}
	return trimmed
}

// SaveSpaceBaseURL stores the Space surface base URL from GET /discovery.
// Empty value deletes the key so stale topology does not persist.
func (m *APITokenManager) SaveSpaceBaseURL(apiURL, spaceBaseURL string) error {
	return m.saveOrDelete(NamespaceForURL(apiURL), spaceBaseURLKey, strings.TrimRight(spaceBaseURL, "/"))
}

// LoadSpaceBaseURL returns the stored Space surface base URL.
func (m *APITokenManager) LoadSpaceBaseURL(apiURL string) (string, bool) {
	return m.secrets.Get(NamespaceForURL(apiURL), spaceBaseURLKey)
}

// ResolveSpaceBaseURL returns the discovered Space URL, a conservative
// portal.* -> space.* production fallback, or the API URL for collapsed
// local/dev deployments.
func (m *APITokenManager) ResolveSpaceBaseURL(apiURL string) string {
	if got, ok := m.LoadSpaceBaseURL(apiURL); ok && got != "" {
		return strings.TrimRight(got, "/")
	}
	trimmed := strings.TrimRight(apiURL, "/")
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return trimmed
	}
	if parsed.Scheme == "https" && strings.HasPrefix(parsed.Host, "portal.") {
		parsed.Host = "space." + strings.TrimPrefix(parsed.Host, "portal.")
		return strings.TrimRight(parsed.String(), "/")
	}
	return trimmed
}

// PairChatRelayDevice calls POST {auth_base_url}/devices/pair with the user's
// access token and stores the returned device credential set.
func (m *APITokenManager) PairChatRelayDevice(authBaseURL, apiURL, userAccessToken string, body ChatRelayDevicePairRequest) (ChatRelayDeviceTokens, error) {
	payload, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(authBaseURL, "/")+"/devices/pair", strings.NewReader(string(payload)))
	if err != nil {
		return ChatRelayDeviceTokens{}, fmt.Errorf("build pair request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if userAccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+userAccessToken)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return ChatRelayDeviceTokens{}, fmt.Errorf("pair request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return ChatRelayDeviceTokens{}, fmt.Errorf("pair failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	tokens, err := decodeChatRelayDeviceTokenResponse(resp.Body)
	if err != nil {
		return ChatRelayDeviceTokens{}, err
	}
	if tokens.DeviceID == "" {
		return ChatRelayDeviceTokens{}, fmt.Errorf("device token response missing device id")
	}
	if err := m.SaveChatRelayDeviceTokens(apiURL, tokens); err != nil {
		return ChatRelayDeviceTokens{}, err
	}
	return tokens, nil
}

// RefreshChatRelayDeviceToken rotates the stored chat relay access/refresh
// token pair by calling POST {auth_base_url}/devices/refresh.
func (m *APITokenManager) RefreshChatRelayDeviceToken(authBaseURL, apiURL string) (ChatRelayDeviceTokens, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	current, ok := m.LoadChatRelayDeviceTokens(apiURL)
	if !ok {
		return ChatRelayDeviceTokens{}, fmt.Errorf("hosted chat is not configured; run `kittypaw login`")
	}
	payload, _ := json.Marshal(map[string]string{"refresh_token": current.RefreshToken})
	resp, err := m.client.Post(
		strings.TrimRight(authBaseURL, "/")+"/devices/refresh",
		"application/json",
		strings.NewReader(string(payload)),
	)
	if err != nil {
		return ChatRelayDeviceTokens{}, fmt.Errorf("device refresh request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		body := strings.TrimSpace(string(b))
		if resp.StatusCode == http.StatusUnauthorized {
			if err := m.ClearChatRelayDeviceTokens(apiURL); err != nil {
				return ChatRelayDeviceTokens{}, fmt.Errorf("%w: device refresh failed (%d): %s; clear local device tokens: %v", ErrChatRelayDeviceRefreshInvalid, resp.StatusCode, body, err)
			}
			return ChatRelayDeviceTokens{}, fmt.Errorf("%w: device refresh failed (%d): %s", ErrChatRelayDeviceRefreshInvalid, resp.StatusCode, body)
		}
		return ChatRelayDeviceTokens{}, fmt.Errorf("device refresh failed (%d): %s", resp.StatusCode, body)
	}
	rotated, err := decodeChatRelayDeviceTokenResponse(resp.Body)
	if err != nil {
		return ChatRelayDeviceTokens{}, err
	}
	if rotated.DeviceID == "" {
		rotated.DeviceID = current.DeviceID
	}
	if err := m.SaveChatRelayDeviceTokens(apiURL, rotated); err != nil {
		return ChatRelayDeviceTokens{}, err
	}
	return rotated, nil
}

// EnsureChatRelayDeviceAccessToken returns stored chat relay device tokens,
// refreshing them first when the access JWT is expired or near expiry.
func (m *APITokenManager) EnsureChatRelayDeviceAccessToken(authBaseURL, apiURL string) (ChatRelayDeviceTokens, error) {
	tokens, ok := m.LoadChatRelayDeviceTokens(apiURL)
	if !ok {
		return ChatRelayDeviceTokens{}, fmt.Errorf("hosted chat is not configured; run `kittypaw login`")
	}
	if !isJWTExpired(tokens.AccessToken) {
		return tokens, nil
	}
	return m.RefreshChatRelayDeviceToken(authBaseURL, apiURL)
}

// ChatRelayDeviceAccessTokenExpired reports whether the stored chat relay
// access JWT is expired or within the refresh grace window.
func (m *APITokenManager) ChatRelayDeviceAccessTokenExpired(apiURL string) (bool, bool) {
	tokens, ok := m.LoadChatRelayDeviceTokens(apiURL)
	if !ok {
		return false, false
	}
	return isJWTExpired(tokens.AccessToken), true
}

func decodeChatRelayDeviceTokenResponse(r io.Reader) (ChatRelayDeviceTokens, error) {
	var result struct {
		DeviceID           string `json:"device_id"`
		DeviceAccessToken  string `json:"device_access_token"`
		DeviceRefreshToken string `json:"device_refresh_token"`
		ExpiresIn          int    `json:"expires_in"`
	}
	if err := json.NewDecoder(r).Decode(&result); err != nil {
		return ChatRelayDeviceTokens{}, fmt.Errorf("decode device token response: %w", err)
	}
	if result.DeviceAccessToken == "" || result.DeviceRefreshToken == "" {
		return ChatRelayDeviceTokens{}, fmt.Errorf("device token response missing access or refresh token")
	}
	return ChatRelayDeviceTokens{
		DeviceID:     result.DeviceID,
		AccessToken:  result.DeviceAccessToken,
		RefreshToken: result.DeviceRefreshToken,
	}, nil
}

// SaveKakaoRelayBaseURL stores the KakaoTalk relay server base URL from GET /discovery.
// Empty value deletes the key so stale URLs don't survive relay migrations.
func (m *APITokenManager) SaveKakaoRelayBaseURL(apiURL, relayURL string) error {
	return m.saveOrDelete(NamespaceForURL(apiURL), kakaoRelayURLKey, relayURL)
}

// LoadKakaoRelayBaseURL returns the stored KakaoTalk relay server base URL.
func (m *APITokenManager) LoadKakaoRelayBaseURL(apiURL string) (string, bool) {
	return m.secrets.Get(NamespaceForURL(apiURL), kakaoRelayURLKey)
}

// SaveAPIBaseURL stores the API base URL from GET /discovery.
// Save-only for now (see plan D5/D6); reserved for future exchange/refresh routing.
// Empty value deletes the key.
func (m *APITokenManager) SaveAPIBaseURL(apiURL, apiBaseURL string) error {
	return m.saveOrDelete(NamespaceForURL(apiURL), "api_base_url", apiBaseURL)
}

// LoadAPIBaseURL returns the stored API base URL.
func (m *APITokenManager) LoadAPIBaseURL(apiURL string) (string, bool) {
	return m.secrets.Get(NamespaceForURL(apiURL), "api_base_url")
}

// SaveSkillsRegistryURL stores the skills registry URL from GET /discovery.
// Save-only for now (see plan D6); not yet routed into registryClient.
// Empty value deletes the key.
func (m *APITokenManager) SaveSkillsRegistryURL(apiURL, skillsRegistryURL string) error {
	return m.saveOrDelete(NamespaceForURL(apiURL), "skills_registry_url", skillsRegistryURL)
}

// LoadSkillsRegistryURL returns the stored skills registry URL.
func (m *APITokenManager) LoadSkillsRegistryURL(apiURL string) (string, bool) {
	return m.secrets.Get(NamespaceForURL(apiURL), "skills_registry_url")
}

// SaveKakaoRelayWSURL stores the full Kakao relay WebSocket URL built from
// a client-side relay registration (baseURL + /ws/{token}).
func (m *APITokenManager) SaveKakaoRelayWSURL(apiURL, wsURL string) error {
	ns := NamespaceForURL(apiURL)
	return m.secrets.Set(ns, kakaoRelayWSURLKey, wsURL)
}

// LoadKakaoRelayWSURL returns the stored Kakao relay WebSocket URL.
func (m *APITokenManager) LoadKakaoRelayWSURL(apiURL string) (string, bool) {
	ns := NamespaceForURL(apiURL)
	return m.secrets.Get(ns, kakaoRelayWSURLKey)
}

// ClearTokens removes stored tokens for an API.
func (m *APITokenManager) ClearTokens(apiURL string) error {
	ns := NamespaceForURL(apiURL)
	return m.secrets.DeletePackage(ns)
}

// refreshTokens calls POST /auth/token/refresh and saves new tokens.
func (m *APITokenManager) refreshTokens(apiURL, refreshToken string) (string, error) {
	payload, _ := json.Marshal(map[string]string{"refresh_token": refreshToken})
	resp, err := m.client.Post(
		apiURL+"/auth/token/refresh",
		"application/json",
		strings.NewReader(string(payload)),
	)
	if err != nil {
		return "", fmt.Errorf("refresh request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("refresh failed (%d): %s", resp.StatusCode, string(b))
	}

	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode refresh response: %w", err)
	}

	ns := NamespaceForURL(apiURL)
	if err := m.secrets.Set(ns, "access_token", result.AccessToken); err != nil {
		return "", fmt.Errorf("save refreshed access_token: %w", err)
	}
	if result.RefreshToken != "" {
		if err := m.secrets.Set(ns, "refresh_token", result.RefreshToken); err != nil {
			return "", fmt.Errorf("save refreshed refresh_token: %w", err)
		}
	}
	return result.AccessToken, nil
}

// isJWTExpired checks the exp claim without verifying the signature.
// Returns true if the token is expired or within 30 seconds of expiry.
func isJWTExpired(token string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return true
	}

	// Decode the payload (middle segment).
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return true
	}

	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp == 0 {
		return true
	}

	// 30-second grace window.
	return time.Now().Unix()+30 >= claims.Exp
}
