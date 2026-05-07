package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jinto/kittypaw/core"
)

const testWebSessionCookieName = "kittypaw_session"

func TestAuthLoginSetsSessionCookie(t *testing.T) {
	srv := newServerWithLocalUser(t, "alice", "pw")

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"account_id":"alice","password":"pw"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("login code = %d body=%s", rr.Code, rr.Body.String())
	}
	cookie := findCookie(rr.Result().Cookies(), testWebSessionCookieName)
	if cookie == nil || cookie.Value == "" {
		t.Fatal("expected session cookie")
	}

	req = httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.AddCookie(cookie)
	rr = httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("me code = %d body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		AccountID string `json:"account_id"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode me response: %v", err)
	}
	if body.AccountID != "alice" {
		t.Fatalf("account_id = %q, want alice", body.AccountID)
	}
}

func TestAuthLoginRejectsWrongPassword(t *testing.T) {
	srv := newServerWithLocalUser(t, "alice", "pw")

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"account_id":"alice","password":"bad"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("login code = %d, want 401", rr.Code)
	}
	if cookie := findCookie(rr.Result().Cookies(), testWebSessionCookieName); cookie != nil {
		t.Fatalf("unexpected session cookie on failed login: %+v", cookie)
	}
}

func TestAuthMeRejectsMissingSessionWhenUsersExist(t *testing.T) {
	srv := newServerWithLocalUser(t, "alice", "pw")

	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("me code = %d, want 401", rr.Code)
	}
}

func TestAuthMeAllowsFirstRunWhenNoUsersExist(t *testing.T) {
	srv := newAuthTestServer(t, t.TempDir(), "alice")

	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("me code = %d body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		AuthRequired bool `json:"auth_required"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode me response: %v", err)
	}
	if body.AuthRequired {
		t.Fatal("auth_required = true, want false for first-run setup")
	}
}

func TestAuthLogoutClearsSessionCookie(t *testing.T) {
	srv := newServerWithLocalUser(t, "alice", "pw")
	cookie := loginSessionCookie(t, srv, "alice", "pw")

	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("logout code = %d body=%s", rr.Code, rr.Body.String())
	}
	cleared := findCookie(rr.Result().Cookies(), testWebSessionCookieName)
	if cleared == nil || cleared.MaxAge >= 0 {
		t.Fatalf("logout cookie = %+v, want MaxAge < 0", cleared)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.AddCookie(cleared)
	rr = httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("me after logout code = %d, want 401", rr.Code)
	}
}

func TestAuthMeRejectsDisabledUserSession(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	auth := core.NewLocalAuthStore(filepath.Join(root, "accounts"))
	if err := auth.CreateUser("alice", "pw"); err != nil {
		t.Fatalf("create local auth user: %v", err)
	}
	srv := newAuthTestServer(t, root, "alice")
	cookie := loginSessionCookie(t, srv, "alice", "pw")
	if err := auth.SetDisabled("alice", true); err != nil {
		t.Fatalf("disable local auth user: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("me code = %d, want 401", rr.Code)
	}
}

func TestBootstrapRequiresSessionWhenLocalUsersExist(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Server.APIKey = "account-key"
	srv := newServerWithLocalUserAndConfig(t, "alice", "pw", &cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("bootstrap without session code = %d, want 401", rr.Code)
	}

	cookie := loginSessionCookie(t, srv, "alice", "pw")
	req = httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Host = "127.0.0.1:1234"
	req.AddCookie(cookie)
	rr = httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("bootstrap with session code = %d body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), `"api_key":"account-key"`) {
		t.Fatalf("bootstrap leaked static account api key: %q", rr.Body.String())
	}
}

func TestBootstrapAllowsRemoteDefaultAccountSession(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Server.APIKey = "account-key"
	srv := newServerWithLocalUserAndConfig(t, "alice", "pw", &cfg)

	req := httptest.NewRequest(http.MethodGet, "http://l.kittym3.com:3000/api/bootstrap", nil)
	req.RemoteAddr = "192.168.0.13:65483"
	req.Host = "l.kittym3.com:3000"
	req.AddCookie(loginSessionCookie(t, srv, "alice", "pw"))
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("remote bootstrap with session code = %d body=%s, want 200", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), `"api_key":"account-key"`) {
		t.Fatalf("remote bootstrap leaked static account api key: %q", rr.Body.String())
	}
}

func TestBootstrapRejectsRemoteFirstRunAccess(t *testing.T) {
	root := t.TempDir()
	cfg := core.DefaultConfig()
	cfg.Server.APIKey = "account-key"
	srv := newAuthTestServer(t, root, "alice", &cfg)

	req := httptest.NewRequest(http.MethodGet, "http://l.kittym3.com:3000/api/bootstrap", nil)
	req.RemoteAddr = "192.168.0.13:65483"
	req.Host = "l.kittym3.com:3000"
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("remote first-run bootstrap code = %d body=%s, want 403", rr.Code, rr.Body.String())
	}
}

func TestSessionBoundBootstrapTokenIsRevokedWithLocalUser(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	cfg := core.DefaultConfig()
	cfg.Server.APIKey = "account-key"
	auth := core.NewLocalAuthStore(filepath.Join(root, "accounts"))
	if err := auth.CreateUser("alice", "pw"); err != nil {
		t.Fatalf("create local auth user: %v", err)
	}
	srv := newAuthTestServer(t, root, "alice", &cfg)
	token := bootstrapAPIToken(t, srv, loginSessionCookie(t, srv, "alice", "pw"))
	if token == "" {
		t.Fatal("bootstrap returned empty api token")
	}
	if token == "account-key" {
		t.Fatal("bootstrap returned static account api key, want session-bound token")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status with session token code = %d body=%s", rr.Code, rr.Body.String())
	}

	if err := auth.SetDisabled("alice", true); err != nil {
		t.Fatalf("disable local auth user: %v", err)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status with revoked session token code = %d, want 401", rr.Code)
	}
}

func TestAPIAuthFailsClosedWhenLocalAuthStoreIsCorrupt(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	accountDir := filepath.Join(root, "accounts", "alice")
	if err := os.MkdirAll(accountDir, 0o700); err != nil {
		t.Fatalf("mkdir account auth dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(accountDir, "account.toml"), []byte("{"), 0o600); err != nil {
		t.Fatalf("write corrupt auth store: %v", err)
	}
	srv := newAuthTestServer(t, root, "alice", &core.Config{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status code = %d, want 500; body=%s", rr.Code, rr.Body.String())
	}
}

func TestWebSocketAcceptsActiveSessionBoundToken(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Server.APIKey = "account-key"
	srv := newServerWithLocalUserAndConfig(t, "alice", "pw", &cfg)
	token := bootstrapAPIToken(t, srv, loginSessionCookie(t, srv, "alice", "pw"))

	req := httptest.NewRequest(http.MethodGet, "/ws?token="+token, nil)
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code == http.StatusUnauthorized {
		t.Fatalf("websocket rejected active session-bound token")
	}
}

func TestBootstrapAllowsNonDefaultAccountSessionWithoutAPIKey(t *testing.T) {
	aliceCfg := core.DefaultConfig()
	aliceCfg.Server.APIKey = "alice-key"
	bobCfg := core.DefaultConfig()
	bobCfg.Server.APIKey = "bob-key"
	srv := newMultiAccountAuthTestServer(t, "alice", map[string]string{
		"alice": "alice-pw",
		"bob":   "bob-pw",
	}, map[string]*core.Config{
		"alice": &aliceCfg,
		"bob":   &bobCfg,
	})

	bobCookie := srv.newWebSessionCookie(httptest.NewRequest(http.MethodGet, "/", nil), "bob", time.Now().Add(webSessionTTL))
	req := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.AddCookie(bobCookie)
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("bootstrap with non-default session code = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var bobBootstrap struct {
		APIKey string `json:"api_key"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&bobBootstrap); err != nil {
		t.Fatalf("decode bob bootstrap: %v", err)
	}
	if bobBootstrap.APIKey != "" {
		t.Fatalf("bob bootstrap api_key = %q, want empty", bobBootstrap.APIKey)
	}

	aliceCookie := loginSessionCookie(t, srv, "alice", "alice-pw")
	req = httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Host = "127.0.0.1:1234"
	req.AddCookie(aliceCookie)
	rr = httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("bootstrap with default session code = %d body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), `"api_key":"alice-key"`) {
		t.Fatalf("bootstrap leaked static default api key: %q", rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("Authorization", "Bearer "+bobCookie.Value)
	rr = httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status with non-default session token code = %d, want 401", rr.Code)
	}
}

func TestAuthLoginAllowsNonDefaultAccountSession(t *testing.T) {
	srv := newMultiAccountAuthTestServer(t, "alice", map[string]string{
		"alice": "alice-pw",
		"bob":   "bob-pw",
	}, map[string]*core.Config{
		"alice": {},
		"bob":   {},
	})

	body, err := json.Marshal(map[string]string{
		"account_id": "bob",
		"password":   "bob-pw",
	})
	if err != nil {
		t.Fatalf("marshal login body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("login code = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	cookie := findCookie(rr.Result().Cookies(), testWebSessionCookieName)
	if cookie == nil || cookie.Value == "" {
		t.Fatal("expected session cookie for non-default login")
	}

	req = httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.AddCookie(cookie)
	rr = httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("me code = %d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		AccountID string `json:"account_id"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode me response: %v", err)
	}
	if resp.AccountID != "bob" {
		t.Fatalf("account_id = %q, want bob", resp.AccountID)
	}
}

func TestAuthResponsesReportDefaultAccount(t *testing.T) {
	srv := newMultiAccountAuthTestServer(t, "alice", map[string]string{
		"alice": "alice-pw",
		"bob":   "bob-pw",
	}, map[string]*core.Config{
		"alice": {},
		"bob":   {},
	})

	aliceCookie := loginSessionCookie(t, srv, "alice", "alice-pw")
	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.AddCookie(aliceCookie)
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("alice me code = %d body=%s", rr.Code, rr.Body.String())
	}
	var aliceMe struct {
		IsDefault bool `json:"is_default"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&aliceMe); err != nil {
		t.Fatalf("decode alice me: %v", err)
	}
	if !aliceMe.IsDefault {
		t.Fatal("alice is_default = false, want true")
	}

	bobCookie := loginSessionCookie(t, srv, "bob", "bob-pw")
	req = httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.AddCookie(bobCookie)
	rr = httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("bob me code = %d body=%s", rr.Code, rr.Body.String())
	}
	var bobMe struct {
		IsDefault bool `json:"is_default"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&bobMe); err != nil {
		t.Fatalf("decode bob me: %v", err)
	}
	if bobMe.IsDefault {
		t.Fatal("bob is_default = true, want false")
	}
}

func newServerWithLocalUser(t *testing.T, accountID, password string) *Server {
	t.Helper()
	return newServerWithLocalUserAndConfig(t, accountID, password, &core.Config{})
}

func newServerWithLocalUserAndConfig(t *testing.T, accountID, password string, cfg *core.Config) *Server {
	t.Helper()
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	auth := core.NewLocalAuthStore(filepath.Join(root, "accounts"))
	if err := auth.CreateUser(accountID, password); err != nil {
		t.Fatalf("create local auth user: %v", err)
	}
	return newAuthTestServer(t, root, accountID, cfg)
}

func newAuthTestServer(t *testing.T, root, accountID string, cfgs ...*core.Config) *Server {
	t.Helper()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	cfg := &core.Config{}
	if len(cfgs) > 0 && cfgs[0] != nil {
		cfg = cfgs[0]
	}
	deps := buildAccountDeps(t, filepath.Join(root, "accounts"), accountID, cfg)
	return New([]*AccountDeps{deps}, "test")
}

func newMultiAccountAuthTestServer(t *testing.T, defaultAccount string, users map[string]string, cfgs map[string]*core.Config) *Server {
	t.Helper()
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	auth := core.NewLocalAuthStore(filepath.Join(root, "accounts"))
	for accountID, password := range users {
		if err := auth.CreateUser(accountID, password); err != nil {
			t.Fatalf("create local auth user %s: %v", accountID, err)
		}
	}

	accountIDs := make([]string, 0, len(users))
	for accountID := range users {
		accountIDs = append(accountIDs, accountID)
	}
	sort.Strings(accountIDs)

	deps := make([]*AccountDeps, 0, len(accountIDs))
	for _, accountID := range accountIDs {
		cfg := cfgs[accountID]
		if cfg == nil {
			cfg = &core.Config{}
		}
		deps = append(deps, buildAccountDeps(t, filepath.Join(root, "accounts"), accountID, cfg))
	}
	return NewWithServerConfig(deps, "test", core.TopLevelServerConfig{DefaultAccount: defaultAccount})
}

func loginSessionCookie(t *testing.T, srv *Server, accountID, password string) *http.Cookie {
	t.Helper()
	body, err := json.Marshal(map[string]string{
		"account_id": accountID,
		"password":   password,
	})
	if err != nil {
		t.Fatalf("marshal login body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("login code = %d body=%s", rr.Code, rr.Body.String())
	}
	cookie := findCookie(rr.Result().Cookies(), testWebSessionCookieName)
	if cookie == nil || cookie.Value == "" {
		t.Fatal("expected session cookie")
	}
	return cookie
}

func bootstrapAPIToken(t *testing.T, srv *Server, cookie *http.Cookie) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Host = "127.0.0.1:1234"
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("bootstrap code = %d body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		APIKey string `json:"api_key"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode bootstrap: %v", err)
	}
	return body.APIKey
}

func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}
