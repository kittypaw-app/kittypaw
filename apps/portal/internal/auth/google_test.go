package auth_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kittypaw-app/kittyportal/internal/auth"
	"github.com/kittypaw-app/kittyportal/internal/config"
	"github.com/kittypaw-app/kittyportal/internal/model"
)

type mockUserStore struct {
	users map[string]*model.User
	seq   int
}

func newMockUserStore() *mockUserStore {
	return &mockUserStore{users: make(map[string]*model.User)}
}

func (m *mockUserStore) CreateOrUpdate(_ context.Context, provider, providerID, email, name, avatarURL string) (*model.User, error) {
	key := provider + ":" + providerID
	u, ok := m.users[key]
	if ok {
		u.Email = email
		u.Name = name
		u.AvatarURL = avatarURL
		u.UpdatedAt = time.Now()
		return u, nil
	}
	m.seq++
	u = &model.User{
		ID:         "user-" + provider + "-" + providerID,
		Provider:   provider,
		ProviderID: providerID,
		Email:      email,
		Name:       name,
		AvatarURL:  avatarURL,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	m.users[key] = u
	return u, nil
}

func (m *mockUserStore) FindByID(_ context.Context, id string) (*model.User, error) {
	for _, u := range m.users {
		if u.ID == id {
			return u, nil
		}
	}
	return nil, model.ErrNotFound
}

func (m *mockUserStore) FindByEmail(_ context.Context, email string) (*model.User, error) {
	for _, u := range m.users {
		if strings.EqualFold(strings.TrimSpace(email), u.Email) {
			return u, nil
		}
	}
	return nil, model.ErrNotFound
}

type mockRefreshTokenStore struct {
	tokens                  []model.RefreshToken
	createForDeviceErr      error  // forced error for pair-atomicity tests (T2)
	revokeIfActiveSeq       []bool // per-call return values (T3 race tests)
	revokeAllForDeviceCalls int    // counter for T3 reuse-detect assertions
	rotateForDeviceErr      error  // forced error for T3 race / atomicity tests
}

func (m *mockRefreshTokenStore) Create(_ context.Context, userID, tokenHash string, expiresAt time.Time) error {
	m.tokens = append(m.tokens, model.RefreshToken{
		ID:        "rt-1",
		UserID:    userID,
		TokenHash: tokenHash,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now(),
	})
	return nil
}

func (m *mockRefreshTokenStore) FindByHash(_ context.Context, hash string) (*model.RefreshToken, error) {
	for i := range m.tokens {
		if m.tokens[i].TokenHash == hash {
			return &m.tokens[i], nil
		}
	}
	return nil, model.ErrNotFound
}

func (m *mockRefreshTokenStore) CreateForDevice(_ context.Context, userID, deviceID, tokenHash string, expiresAt time.Time) error {
	if m.createForDeviceErr != nil {
		return m.createForDeviceErr
	}
	dev := deviceID
	m.tokens = append(m.tokens, model.RefreshToken{
		ID:        "rt-dev-" + tokenHash[:min(8, len(tokenHash))],
		UserID:    userID,
		DeviceID:  &dev,
		TokenHash: tokenHash,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now(),
	})
	return nil
}

func (m *mockRefreshTokenStore) RevokeIfActive(_ context.Context, id string) (bool, error) {
	// Sequence-driven for T3 race tests; default true.
	if len(m.revokeIfActiveSeq) > 0 {
		v := m.revokeIfActiveSeq[0]
		m.revokeIfActiveSeq = m.revokeIfActiveSeq[1:]
		// Mark the token as revoked when the seq says success — keeps
		// FindByHash subsequent calls consistent with reality.
		if v {
			for i := range m.tokens {
				if m.tokens[i].ID == id && m.tokens[i].RevokedAt == nil {
					now := time.Now()
					m.tokens[i].RevokedAt = &now
					break
				}
			}
		}
		return v, nil
	}
	for i := range m.tokens {
		if m.tokens[i].ID == id && m.tokens[i].RevokedAt == nil {
			now := time.Now()
			m.tokens[i].RevokedAt = &now
			return true, nil
		}
	}
	return false, nil
}

func (m *mockRefreshTokenStore) RevokeAllForUser(_ context.Context, _ string) error { return nil }

func (m *mockRefreshTokenStore) RevokeAllForDevice(_ context.Context, deviceID string) error {
	m.revokeAllForDeviceCalls++
	now := time.Now()
	for i := range m.tokens {
		if m.tokens[i].DeviceID != nil && *m.tokens[i].DeviceID == deviceID && m.tokens[i].RevokedAt == nil {
			m.tokens[i].RevokedAt = &now
		}
	}
	return nil
}

// DeleteExpiredOlderThan — janitor-only stub for handler-level tests.
// Janitor has its own dedicated mock.
func (m *mockRefreshTokenStore) DeleteExpiredOlderThan(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

// rotateForDeviceErr lets T3 race tests force RotateForDevice to fail
// without affecting the seeded refresh state.
func (m *mockRefreshTokenStore) RotateForDevice(_ context.Context, oldID, userID, deviceID, newHash string, newExpiresAt time.Time) error {
	if m.rotateForDeviceErr != nil {
		return m.rotateForDeviceErr
	}
	// Atomic semantics in the mock: revoke old + insert new only if old
	// is currently active. Mirrors the PG transaction's all-or-nothing.
	for i := range m.tokens {
		if m.tokens[i].ID == oldID {
			if m.tokens[i].RevokedAt != nil {
				return model.ErrRotationAborted
			}
			now := time.Now()
			m.tokens[i].RevokedAt = &now
			dev := deviceID
			m.tokens = append(m.tokens, model.RefreshToken{
				ID:        "rt-rot-" + newHash[:min(8, len(newHash))],
				UserID:    userID,
				DeviceID:  &dev,
				TokenHash: newHash,
				ExpiresAt: newExpiresAt,
				CreatedAt: time.Now(),
			})
			return nil
		}
	}
	return model.ErrRotationAborted
}

func setupGoogleTest(t *testing.T, googleServer *httptest.Server) (*auth.OAuthHandler, auth.GoogleConfig) {
	t.Helper()

	appCfg := config.LoadForTest()
	states := auth.NewStateStore()
	t.Cleanup(states.Close)

	h := &auth.OAuthHandler{
		UserStore:         newMockUserStore(),
		RefreshTokenStore: &mockRefreshTokenStore{},
		StateStore:        states,
		JWTPrivateKey:     appCfg.JWTPrivateKey,
		JWTKID:            appCfg.JWTKID,
		HTTPClient:        googleServer.Client(),
	}

	cfg := auth.GoogleConfig{
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
		RedirectURL:  "http://localhost:8080/auth/google/callback",
	}

	return h, cfg
}

func TestGoogleLoginRedirect(t *testing.T) {
	// No actual Google server needed for login.
	h, cfg := setupGoogleTest(t, httptest.NewServer(http.NotFoundHandler()))

	req := httptest.NewRequest(http.MethodGet, "/auth/google", nil)
	w := httptest.NewRecorder()

	h.HandleGoogleLogin(cfg).ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", w.Code)
	}

	loc := w.Header().Get("Location")
	if loc == "" {
		t.Fatal("expected Location header")
	}

	for _, param := range []string{"client_id=test-client-id", "code_challenge=", "code_challenge_method=S256", "state=", "scope=openid"} {
		if !contains(loc, param) {
			t.Fatalf("redirect URL missing %q: %s", param, loc)
		}
	}
}

func TestGoogleLoginRedirectUsesConfiguredAuthURL(t *testing.T) {
	h, cfg := setupGoogleTest(t, httptest.NewServer(http.NotFoundHandler()))
	h.GoogleAuthURL = "http://oauth.local/google/auth"

	req := httptest.NewRequest(http.MethodGet, "/auth/google", nil)
	w := httptest.NewRecorder()

	h.HandleGoogleLogin(cfg).ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "http://oauth.local/google/auth?") {
		t.Fatalf("redirect URL = %q, want configured auth URL", loc)
	}
}

func TestGoogleCallbackSuccess(t *testing.T) {
	// Mock Google token + userinfo endpoints.
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "google-at"})
	})
	mux.HandleFunc("GET /userinfo", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"id":      "g-user-1",
			"email":   "test@gmail.com",
			"name":    "Test User",
			"picture": "https://avatar.example.com/1",
		})
	})
	googleServer := httptest.NewServer(mux)
	defer googleServer.Close()

	h, cfg := setupGoogleTest(t, googleServer)

	// Override Google URLs for testing.
	h.GoogleTokenURL = googleServer.URL + "/token"
	h.GoogleUserInfoURL = googleServer.URL + "/userinfo"

	// Create a valid state.
	state, err := h.StateStore.Create("test-verifier")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/auth/google/callback?code=test-code&state="+state, nil)
	w := httptest.NewRecorder()

	h.HandleGoogleCallback(cfg).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp auth.TokenResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.AccessToken == "" {
		t.Fatal("expected non-empty access_token")
	}
	if resp.RefreshToken == "" {
		t.Fatal("expected non-empty refresh_token")
	}
	if resp.TokenType != "Bearer" {
		t.Fatalf("expected Bearer, got %q", resp.TokenType)
	}

	// Plan 17 / Plan 21 PR-B wire-format guard (issueTokenPair single choke
	// point). Decode the access_token header AND payload directly and pin
	// alg=RS256, kid present, sub/iss/aud/scope, v=2.
	// If anyone reverts cli.go:27 SignForAudiences -> Sign or downgrades
	// alg/v, this assertion fires with the message below — distinguishing
	// the regression from a generic OAuth path failure.
	parts := strings.Split(resp.AccessToken, ".")
	if len(parts) != 3 {
		t.Fatalf("wire-format regression in issueTokenPair: expected 3 JWT segments, got %d", len(parts))
	}
	hdrSeg, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("wire-format regression in issueTokenPair: decode header: %v", err)
	}
	var hdr map[string]any
	if err := json.Unmarshal(hdrSeg, &hdr); err != nil {
		t.Fatalf("wire-format regression in issueTokenPair: unmarshal header: %v", err)
	}
	if hdr["alg"] != "RS256" {
		t.Fatalf("wire-format regression: alg=%v", hdr["alg"])
	}
	if hdr["kid"] == "" || hdr["kid"] == nil {
		t.Fatal("wire-format regression: missing kid header")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("wire-format regression in issueTokenPair: decode payload: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatalf("wire-format regression in issueTokenPair: unmarshal: %v", err)
	}
	if got, _ := raw["sub"].(string); got != "user-google-g-user-1" {
		t.Fatalf(`wire-format regression in issueTokenPair: sub = %v, want "user-google-g-user-1"`, raw["sub"])
	}
	if got, _ := raw["iss"].(string); got != "https://portal.kittypaw.app/auth" {
		t.Fatalf(`wire-format regression in issueTokenPair: iss = %v, want "https://portal.kittypaw.app/auth"`, raw["iss"])
	}
	if v, _ := raw["v"].(float64); v != 2 {
		t.Fatalf("wire-format regression in issueTokenPair: v = %v, want 2", raw["v"])
	}
	if _, ok := raw["uid"]; ok {
		t.Fatalf(`wire-format regression in issueTokenPair: legacy "uid" key must not appear, got: %v`, raw)
	}
	auds, _ := raw["aud"].([]any)
	if len(auds) != 2 || auds[0] != "https://api.kittypaw.app" || auds[1] != "https://chat.kittypaw.app" {
		t.Fatalf(`wire-format regression in issueTokenPair: aud = %v, want ["https://api.kittypaw.app","https://chat.kittypaw.app"]`, raw["aud"])
	}
	scopes, _ := raw["scope"].([]any)
	if len(scopes) != 2 || scopes[0] != "chat:relay" || scopes[1] != "models:read" {
		t.Fatalf(`wire-format regression in issueTokenPair: scope = %v, want ["chat:relay","models:read"]`, raw["scope"])
	}
}

func TestGoogleCallbackInvalidState(t *testing.T) {
	googleServer := httptest.NewServer(http.NotFoundHandler())
	defer googleServer.Close()

	h, cfg := setupGoogleTest(t, googleServer)

	req := httptest.NewRequest(http.MethodGet, "/auth/google/callback?code=test-code&state=invalid", nil)
	w := httptest.NewRecorder()

	h.HandleGoogleCallback(cfg).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestGoogleCallbackMissingParams(t *testing.T) {
	googleServer := httptest.NewServer(http.NotFoundHandler())
	defer googleServer.Close()

	h, cfg := setupGoogleTest(t, googleServer)

	req := httptest.NewRequest(http.MethodGet, "/auth/google/callback", nil)
	w := httptest.NewRecorder()

	h.HandleGoogleCallback(cfg).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
