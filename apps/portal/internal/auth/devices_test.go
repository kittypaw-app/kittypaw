package auth_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kittypaw-app/kittyportal/internal/auth"
	"github.com/kittypaw-app/kittyportal/internal/auth/testfixture"
	"github.com/kittypaw-app/kittyportal/internal/config"
	"github.com/kittypaw-app/kittyportal/internal/model"
)

// mockDeviceStore — in-memory DeviceStore for unit tests of pair/refresh
// handlers. T4/T5 (list/delete) skip mocks per CEO scope cut and use
// real-DB integration only.
type mockDeviceStore struct {
	devices     map[string]*model.Device
	createErr   error // forced error for compensating-revoke tests
	revokeCalls int   // count for atomicity assertions
	touchCalls  int   // count for Plan 24 T1 refresh-side wiring assertion
	touchErr    error // forced error to verify Touch failure stays best-effort
}

func newMockDeviceStore() *mockDeviceStore {
	return &mockDeviceStore{devices: make(map[string]*model.Device)}
}

func (m *mockDeviceStore) Create(_ context.Context, userID, name string, capabilities map[string]any) (*model.Device, error) {
	if m.createErr != nil {
		return nil, m.createErr
	}
	if capabilities == nil {
		capabilities = map[string]any{}
	}
	dev := &model.Device{
		ID:           "dev-mock-" + name,
		UserID:       userID,
		Name:         name,
		Capabilities: capabilities,
		PairedAt:     time.Now(),
	}
	m.devices[dev.ID] = dev
	return dev, nil
}

func (m *mockDeviceStore) FindByID(_ context.Context, id string) (*model.Device, error) {
	dev, ok := m.devices[id]
	if !ok {
		return nil, model.ErrNotFound
	}
	return dev, nil
}

func (m *mockDeviceStore) ListActiveForUser(_ context.Context, userID string) ([]*model.Device, error) {
	var out []*model.Device
	for _, d := range m.devices {
		if d.UserID == userID && d.RevokedAt == nil {
			out = append(out, d)
		}
	}
	return out, nil
}

func (m *mockDeviceStore) Revoke(_ context.Context, id string) error {
	m.revokeCalls++
	dev, ok := m.devices[id]
	if !ok {
		return model.ErrNotFound
	}
	if dev.RevokedAt == nil {
		now := time.Now()
		dev.RevokedAt = &now
	}
	return nil
}

func (m *mockDeviceStore) Touch(_ context.Context, id string) error {
	m.touchCalls++
	if m.touchErr != nil {
		return m.touchErr
	}
	if dev, ok := m.devices[id]; ok && dev.RevokedAt == nil {
		now := time.Now()
		dev.LastUsedAt = &now
	}
	return nil
}

// ReapIdle / DeleteRevokedOlderThan — janitor-only stubs. Pair/refresh
// handler tests don't exercise these; janitor has its own mock.
func (m *mockDeviceStore) ReapIdle(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}
func (m *mockDeviceStore) DeleteRevokedOlderThan(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

// TestSignDeviceJWT_RoundTrip pins the prod-issue path: SignDeviceJWT
// produces a token that auth.Verify accepts for Chat and Space audiences and
// whose claims carry sub=device:<id>, user_id=<userID>, scope=daemon:connect,
// v=2. A regression in any of those breaks the daemon ↔ kittychat WSS
// auth contract.
func TestSignDeviceJWT_RoundTrip(t *testing.T) {
	cfg := config.LoadForTest()
	provider := auth.NewSingleKeyProvider(&cfg.JWTPrivateKey.PublicKey, cfg.JWTKID)

	token, err := auth.SignDeviceJWT("user-1", "dev-abc", cfg.JWTPrivateKey, cfg.JWTKID, 15*time.Minute)
	if err != nil {
		t.Fatalf("SignDeviceJWT: %v", err)
	}

	claims, err := auth.Verify(token, provider, auth.AudienceChat)
	if err != nil {
		t.Fatalf("Verify (AudienceChat): %v", err)
	}
	if _, err := auth.Verify(token, provider, auth.AudienceSpace); err != nil {
		t.Fatalf("Verify (AudienceSpace): %v", err)
	}
	// claims.UserID is the JWT "sub" — for device JWTs this is "device:<id>",
	// not the underlying user. The user id rides in a separate "user_id"
	// claim; Claims doesn't decode it directly so we re-decode below.
	if claims.UserID != "device:dev-abc" {
		t.Fatalf("sub = %q, want device:dev-abc", claims.UserID)
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWT segments, got %d", len(parts))
	}

	hdrSeg, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	var hdr map[string]any
	if err := json.Unmarshal(hdrSeg, &hdr); err != nil {
		t.Fatalf("unmarshal header: %v", err)
	}
	if hdr["alg"] != "RS256" {
		t.Fatalf("alg = %v, want RS256", hdr["alg"])
	}
	if hdr["kid"] != cfg.JWTKID {
		t.Fatalf("kid = %v, want %q", hdr["kid"], cfg.JWTKID)
	}

	payloadSeg, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(payloadSeg, &raw); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if raw["user_id"] != "user-1" {
		t.Fatalf("user_id = %v, want user-1", raw["user_id"])
	}
	auds, _ := raw["aud"].([]any)
	if len(auds) != 2 || auds[0] != auth.AudienceChat || auds[1] != auth.AudienceSpace {
		t.Fatalf("aud = %v, want [%q %q]", raw["aud"], auth.AudienceChat, auth.AudienceSpace)
	}
	scopes, _ := raw["scope"].([]any)
	if len(scopes) != 1 || scopes[0] != auth.ScopeDaemonConnect {
		t.Fatalf("scope = %v, want [%q]", raw["scope"], auth.ScopeDaemonConnect)
	}
	if v, _ := raw["v"].(float64); v != 2 {
		t.Fatalf("v = %v, want 2", raw["v"])
	}
	if raw["iss"] != auth.Issuer {
		t.Fatalf("iss = %v, want %q", raw["iss"], auth.Issuer)
	}
}

// TestSignDeviceJWT_WireFormatMatchesIssueDeviceJWT is the cross-team
// contract anchor: production-side SignDeviceJWT and fixture-side
// IssueDeviceJWT must emit byte-compatible wire format. Byte equality
// is impossible (iat/exp drift), so we compare claim *structure*:
// header keys + payload key set + sub prefix + iss + aud[0] + scope[0]
// + v=2. Type-only check on iat/exp.
func TestSignDeviceJWT_WireFormatMatchesIssueDeviceJWT(t *testing.T) {
	cfg := config.LoadForTest()

	prodToken, err := auth.SignDeviceJWT("user-1", "dev-abc", cfg.JWTPrivateKey, cfg.JWTKID, 15*time.Minute)
	if err != nil {
		t.Fatalf("SignDeviceJWT: %v", err)
	}

	now := time.Now()
	fixtureToken, err := testfixture.IssueDeviceJWT(cfg.JWTPrivateKey, cfg.JWTKID, testfixture.DeviceClaims{
		UserID:   "user-1",
		DeviceID: "dev-abc",
		Audience: []string{auth.AudienceChat, auth.AudienceSpace},
		Scope:    []string{auth.ScopeDaemonConnect},
		Version:  2,
		IssuedAt: now,
		Expires:  now.Add(15 * time.Minute),
	})
	if err != nil {
		t.Fatalf("IssueDeviceJWT: %v", err)
	}

	prodHdr, prodPayload := decodeSegments(t, prodToken)
	fixHdr, fixPayload := decodeSegments(t, fixtureToken)

	// Header: alg + kid set match. typ is not set by golang-jwt by default
	// so don't pin it; alg/kid are the cross-team contract.
	for _, k := range []string{"alg", "kid"} {
		if prodHdr[k] != fixHdr[k] {
			t.Fatalf("header[%q] mismatch: prod=%v fixture=%v", k, prodHdr[k], fixHdr[k])
		}
	}

	// Payload key set — ignore time fields (iat/exp drift is expected).
	wantKeys := []string{"sub", "user_id", "aud", "scope", "v", "iss"}
	for _, k := range wantKeys {
		if _, ok := prodPayload[k]; !ok {
			t.Fatalf("prod payload missing key %q", k)
		}
		if _, ok := fixPayload[k]; !ok {
			t.Fatalf("fixture payload missing key %q", k)
		}
	}

	// Pin specific values.
	for _, k := range []string{"sub", "user_id", "iss", "v"} {
		if prodPayload[k] != fixPayload[k] {
			t.Fatalf("payload[%q] mismatch: prod=%v fixture=%v", k, prodPayload[k], fixPayload[k])
		}
	}
	if !reflect.DeepEqual(prodPayload["aud"], fixPayload["aud"]) {
		t.Fatalf("payload[aud] mismatch: prod=%v fixture=%v", prodPayload["aud"], fixPayload["aud"])
	}
	if !reflect.DeepEqual(prodPayload["scope"], fixPayload["scope"]) {
		t.Fatalf("payload[scope] mismatch: prod=%v fixture=%v", prodPayload["scope"], fixPayload["scope"])
	}
	// Sub must start with "device:" prefix in both.
	for _, p := range []map[string]any{prodPayload, fixPayload} {
		sub, _ := p["sub"].(string)
		if !strings.HasPrefix(sub, "device:") {
			t.Fatalf("sub = %q, missing device: prefix", sub)
		}
	}
	// iat/exp type only.
	for _, k := range []string{"iat", "exp"} {
		if _, ok := prodPayload[k].(float64); !ok {
			t.Fatalf("prod payload[%q] not number: %T", k, prodPayload[k])
		}
		if _, ok := fixPayload[k].(float64); !ok {
			t.Fatalf("fixture payload[%q] not number: %T", k, fixPayload[k])
		}
	}
}

func decodeSegments(t *testing.T, token string) (hdr, payload map[string]any) {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 segments, got %d", len(parts))
	}
	hdrBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	if err := json.Unmarshal(hdrBytes, &hdr); err != nil {
		t.Fatalf("unmarshal header: %v", err)
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	return hdr, payload
}

// --- Pair handler ---

// setupPairTest wires an OAuthHandler with mock stores and an
// authenticated context (UserFromContext returns the seeded user).
func setupPairTest(t *testing.T) (*auth.OAuthHandler, *mockDeviceStore, *mockRefreshTokenStore, *model.User) {
	t.Helper()
	cfg := config.LoadForTest()
	userStore := newMockUserStore()
	user, _ := userStore.CreateOrUpdate(context.Background(), "google", "1", "u@u.com", "U", "")
	devStore := newMockDeviceStore()
	rtStore := &mockRefreshTokenStore{}
	h := &auth.OAuthHandler{
		UserStore:         userStore,
		RefreshTokenStore: rtStore,
		DeviceStore:       devStore,
		StateStore:        auth.NewStateStore(),
		JWTPrivateKey:     cfg.JWTPrivateKey,
		JWTKID:            cfg.JWTKID,
	}
	t.Cleanup(h.StateStore.Close)
	return h, devStore, rtStore, user
}

// authedPairRequest builds a POST /auth/devices/pair with a valid
// user JWT in the Authorization header so the user middleware
// populates the request context.
func authedPairRequest(t *testing.T, h *auth.OAuthHandler, user *model.User, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	cfg := config.LoadForTest()
	provider := auth.NewSingleKeyProvider(&cfg.JWTPrivateKey.PublicKey, cfg.JWTKID)

	token := testfixture.IssueTestJWT(t, cfg.JWTPrivateKey, cfg.JWTKID, user.ID, 15*time.Minute)

	req := httptest.NewRequest(http.MethodPost, "/auth/devices/pair", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	auth.Middleware(provider, auth.AudienceAPI, h.UserStore)(h.HandlePair()).ServeHTTP(w, req)
	return w
}

// anonPairRequest — no Authorization header, exercises the auth-fail
// path of HandlePair (UserFromContext returns nil → 401).
func anonPairRequest(t *testing.T, h *auth.OAuthHandler, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	cfg := config.LoadForTest()
	provider := auth.NewSingleKeyProvider(&cfg.JWTPrivateKey.PublicKey, cfg.JWTKID)

	req := httptest.NewRequest(http.MethodPost, "/auth/devices/pair", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	auth.Middleware(provider, auth.AudienceAPI, h.UserStore)(h.HandlePair()).ServeHTTP(w, req)
	return w
}

func TestHandlePair_Happy(t *testing.T) {
	h, devStore, rtStore, user := setupPairTest(t)

	body, _ := json.Marshal(map[string]any{
		"name":         "macbook",
		"capabilities": map[string]any{"v": "0.1"},
	})
	w := authedPairRequest(t, h, user, body)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	// RFC 6749 §5.1 — token responses MUST NOT be cached. Without this
	// assertion, a regression that drops writeTokenResponse silently
	// allows intermediate proxies to cache the refresh token.
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store (RFC 6749 §5.1)", got)
	}
	if got := w.Header().Get("Pragma"); got != "no-cache" {
		t.Fatalf("Pragma = %q, want no-cache", got)
	}

	var resp struct {
		DeviceID           string `json:"device_id"`
		DeviceAccessToken  string `json:"device_access_token"`
		DeviceRefreshToken string `json:"device_refresh_token"`
		ExpiresIn          int    `json:"expires_in"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.DeviceID == "" {
		t.Fatal("expected non-empty device_id")
	}
	if resp.DeviceAccessToken == "" {
		t.Fatal("expected non-empty device_access_token")
	}
	if resp.DeviceRefreshToken == "" {
		t.Fatal("expected non-empty device_refresh_token")
	}
	if resp.ExpiresIn != 900 {
		t.Fatalf("expires_in = %d, want 900", resp.ExpiresIn)
	}

	// Verify the issued device JWT matches the wire format we sign for.
	cfg := config.LoadForTest()
	provider := auth.NewSingleKeyProvider(&cfg.JWTPrivateKey.PublicKey, cfg.JWTKID)
	claims, err := auth.Verify(resp.DeviceAccessToken, provider, auth.AudienceChat)
	if err != nil {
		t.Fatalf("Verify device JWT: %v", err)
	}
	if claims.UserID != "device:"+resp.DeviceID {
		t.Fatalf("sub = %q, want device:%s", claims.UserID, resp.DeviceID)
	}

	// DeviceStore.Create should have produced a row.
	if len(devStore.devices) != 1 {
		t.Fatalf("devices count = %d, want 1", len(devStore.devices))
	}
	// RefreshTokenStore.CreateForDevice should have been called once.
	if len(rtStore.tokens) != 1 {
		t.Fatalf("refresh tokens = %d, want 1", len(rtStore.tokens))
	}
	if rtStore.tokens[0].DeviceID == nil || *rtStore.tokens[0].DeviceID != resp.DeviceID {
		t.Fatalf("refresh device_id = %v, want %q", rtStore.tokens[0].DeviceID, resp.DeviceID)
	}
}

func TestHandlePair_Anonymous_401(t *testing.T) {
	h, _, _, _ := setupPairTest(t)
	body, _ := json.Marshal(map[string]any{"name": "x"})
	w := anonPairRequest(t, h, body)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestHandlePair_BodyTooLarge_400(t *testing.T) {
	h, _, _, user := setupPairTest(t)

	// 5KB body > 4KB cap.
	huge := make([]byte, 5*1024)
	for i := range huge {
		huge[i] = 'a'
	}
	body, _ := json.Marshal(map[string]any{"name": string(huge)})
	w := authedPairRequest(t, h, user, body)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (4KB cap)", w.Code)
	}
}

func TestHandlePair_MalformedJSON_400(t *testing.T) {
	h, _, _, user := setupPairTest(t)
	w := authedPairRequest(t, h, user, []byte("not-json"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// Empty name is allowed (silly-wiggling-balloon.md L222 — name optional).
// Daemons can pair anonymously and label the device later.
func TestHandlePair_EmptyName_200_AllowedBySpec(t *testing.T) {
	h, _, _, user := setupPairTest(t)
	body, _ := json.Marshal(map[string]any{"name": ""})
	w := authedPairRequest(t, h, user, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (empty name allowed per spec)", w.Code)
	}
}

// CapabilitiesArray test verifies that Go's typed decode rejects array
// values for the capabilities map field — server-side validation,
// independent of forward-compat (unknown fields). This catches a
// daemon sending malformed capabilities, not a daemon sending NEW
// fields.
func TestHandlePair_CapabilitiesArray_RejectedByDecode(t *testing.T) {
	h, _, _, user := setupPairTest(t)
	body := []byte(`{"name":"x","capabilities":[1,2,3]}`)
	w := authedPairRequest(t, h, user, body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (capabilities must be object)", w.Code)
	}
}

// AC7 — pair atomicity. If RefreshTokenStore.CreateForDevice fails after
// DeviceStore.Create succeeded, the handler MUST compensate by revoking
// the device. Without this guard, every retried failure produces another
// orphan row that List endpoint silently surfaces.
func TestHandlePair_RefreshCreateFails_RevokesDevice(t *testing.T) {
	h, devStore, rtStore, user := setupPairTest(t)

	rtStore.createForDeviceErr = errors.New("forced refresh failure")

	body, _ := json.Marshal(map[string]any{"name": "macbook"})
	w := authedPairRequest(t, h, user, body)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	// Exactly one Revoke call (compensating).
	if devStore.revokeCalls != 1 {
		t.Fatalf("Revoke calls = %d, want 1 (compensating revoke)", devStore.revokeCalls)
	}
	// Device row exists but is revoked.
	if len(devStore.devices) != 1 {
		t.Fatalf("devices count = %d, want 1", len(devStore.devices))
	}
	for _, d := range devStore.devices {
		if d.RevokedAt == nil {
			t.Fatal("device must be revoked after compensating cleanup")
		}
	}
}

// --- Refresh handler ---

// setupRefreshTest seeds a paired device and a refresh row so the
// handler can exercise rotation/reuse-detection branches.
func setupRefreshTest_Devices(t *testing.T) (*auth.OAuthHandler, *mockDeviceStore, *mockRefreshTokenStore, *model.User, *model.Device, string) {
	t.Helper()
	h, devStore, rtStore, user := setupPairTest(t)

	dev, _ := devStore.Create(context.Background(), user.ID, "seeded", nil)
	raw := "device-refresh-raw"
	hash := auth.HashRefreshToken(raw)
	_ = rtStore.CreateForDevice(context.Background(), user.ID, dev.ID, hash, time.Now().Add(30*24*time.Hour))
	return h, devStore, rtStore, user, dev, raw
}

func postDeviceRefresh(h *auth.OAuthHandler, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/auth/devices/refresh", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.HandleDeviceRefresh().ServeHTTP(w, req)
	return w
}

func TestHandleDeviceRefresh_Happy_Rotation(t *testing.T) {
	h, _, rtStore, _, dev, raw := setupRefreshTest_Devices(t)

	body, _ := json.Marshal(map[string]string{"refresh_token": raw})
	w := postDeviceRefresh(h, body)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	// RFC 6749 §5.1 — refresh response also MUST NOT be cached.
	// Pinned alongside pair to catch a regression that touches one
	// handler but forgets the other.
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store (RFC 6749 §5.1)", got)
	}
	var resp struct {
		DeviceID           string `json:"device_id"`
		DeviceAccessToken  string `json:"device_access_token"`
		DeviceRefreshToken string `json:"device_refresh_token"`
		ExpiresIn          int    `json:"expires_in"`
	}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp.DeviceID != dev.ID {
		t.Fatalf("device_id = %q, want %q", resp.DeviceID, dev.ID)
	}
	if resp.DeviceRefreshToken == raw {
		t.Fatal("rotation must produce a new refresh token")
	}
	// Old refresh row should be revoked.
	rt, _ := rtStore.FindByHash(context.Background(), auth.HashRefreshToken(raw))
	if rt.RevokedAt == nil {
		t.Fatal("old refresh must be revoked after rotation")
	}
}

func TestHandleDeviceRefresh_ReuseDetection_401(t *testing.T) {
	h, _, rtStore, _, dev, raw := setupRefreshTest_Devices(t)

	// Pre-revoke the refresh row to simulate a "reuse" attempt.
	hash := auth.HashRefreshToken(raw)
	rt, _ := rtStore.FindByHash(context.Background(), hash)
	now := time.Now()
	rt.RevokedAt = &now

	body, _ := json.Marshal(map[string]string{"refresh_token": raw})
	w := postDeviceRefresh(h, body)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if rtStore.revokeAllForDeviceCalls != 1 {
		t.Fatalf("RevokeAllForDevice calls = %d, want 1 (reuse detection)", rtStore.revokeAllForDeviceCalls)
	}
	// All device refresh tokens for this device should be revoked.
	for _, tk := range rtStore.tokens {
		if tk.DeviceID != nil && *tk.DeviceID == dev.ID && tk.RevokedAt == nil {
			t.Fatal("all device refresh must be revoked on reuse detection")
		}
	}
}

func TestHandleDeviceRefresh_Expired_401(t *testing.T) {
	h, _, rtStore, _, _, raw := setupRefreshTest_Devices(t)

	// Force the seeded refresh to be expired.
	hash := auth.HashRefreshToken(raw)
	rt, _ := rtStore.FindByHash(context.Background(), hash)
	rt.ExpiresAt = time.Now().Add(-time.Hour)

	body, _ := json.Marshal(map[string]string{"refresh_token": raw})
	w := postDeviceRefresh(h, body)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestHandleDeviceRefresh_UnknownHash_401(t *testing.T) {
	h, _, _, _, _, _ := setupRefreshTest_Devices(t)
	body, _ := json.Marshal(map[string]string{"refresh_token": "never-issued"})
	w := postDeviceRefresh(h, body)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestHandleDeviceRefresh_UserScopedRefresh_401(t *testing.T) {
	h, _, rtStore, user := setupPairTest(t)

	// Seed a USER-scoped refresh (device_id NULL) and try to use it
	// against the device-only endpoint.
	raw := "user-refresh"
	_ = rtStore.Create(context.Background(), user.ID, auth.HashRefreshToken(raw), time.Now().Add(time.Hour))

	body, _ := json.Marshal(map[string]string{"refresh_token": raw})
	w := postDeviceRefresh(h, body)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (device-only endpoint)", w.Code)
	}
}

func TestHandleDeviceRefresh_RevokedDevice_401(t *testing.T) {
	h, devStore, _, _, dev, raw := setupRefreshTest_Devices(t)
	// Revoke the device but leave the refresh active.
	_ = devStore.Revoke(context.Background(), dev.ID)

	body, _ := json.Marshal(map[string]string{"refresh_token": raw})
	w := postDeviceRefresh(h, body)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (revoked device)", w.Code)
	}
}

// Race-loser path: RotateForDevice returns ErrRotationAborted when a
// concurrent request rotated the same refresh row first. The loser
// must 401, not 500. Plan 23 결정 3 + follow-up review HIGH 0.85 fix
// (atomic transaction).
func TestHandleDeviceRefresh_RevokeRace_401(t *testing.T) {
	h, _, rtStore, _, _, raw := setupRefreshTest_Devices(t)
	// Force RotateForDevice to return ErrRotationAborted (race-loser).
	rtStore.rotateForDeviceErr = model.ErrRotationAborted

	body, _ := json.Marshal(map[string]string{"refresh_token": raw})
	w := postDeviceRefresh(h, body)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (race-loser)", w.Code)
	}
}

// AC7 follow-up: refresh-rotation atomicity. RotateForDevice failure
// (e.g. forced generic DB error) must NOT silently leave the old row
// revoked — the daemon's retry would then trip reuse-detection. With
// the transactional fix, RotateForDevice rolls back so the old row
// stays active.
func TestHandleDeviceRefresh_RotationFailure_500(t *testing.T) {
	h, _, rtStore, _, _, raw := setupRefreshTest_Devices(t)
	rtStore.rotateForDeviceErr = errors.New("forced rotation failure")

	body, _ := json.Marshal(map[string]string{"refresh_token": raw})
	w := postDeviceRefresh(h, body)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	// Old refresh must still be active — caller (daemon) should be
	// able to retry without tripping reuse-detection. The PG store's
	// transaction rollback enforces this; the mock test just pins the
	// 500 response shape.
}

func TestHandleDeviceRefresh_BodyTooLarge_400(t *testing.T) {
	h, _, _, _, _, _ := setupRefreshTest_Devices(t)
	huge := make([]byte, 2*1024) // > 1KiB cap
	for i := range huge {
		huge[i] = 'a'
	}
	body, _ := json.Marshal(map[string]string{"refresh_token": string(huge)})
	w := postDeviceRefresh(h, body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}
