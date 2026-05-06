package webapp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kittypaw-app/kittyhome/internal/identity"
)

func TestLoginGoogleRedirectsToAPIWithPKCE(t *testing.T) {
	handler := newTestHandler(t, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/auth/login/google", nil)
	rr := httptest.NewRecorder()

	handler.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body=%s", rr.Code, rr.Body.String())
	}
	location := rr.Header().Get("Location")
	u, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse redirect location: %v", err)
	}
	if got := u.Scheme + "://" + u.Host + u.Path; got != handler.apiAuthBaseURL+"/web/google" {
		t.Fatalf("redirect base = %q, want API web google", got)
	}
	q := u.Query()
	if got := q.Get("redirect_uri"); got != "https://chat.test/auth/callback" {
		t.Fatalf("redirect_uri = %q", got)
	}
	if q.Get("state") == "" {
		t.Fatal("state missing")
	}
	if q.Get("code_challenge") == "" {
		t.Fatal("code_challenge missing")
	}
	if got := q.Get("code_challenge_method"); got != "S256" {
		t.Fatalf("code_challenge_method = %q, want S256", got)
	}
}

func TestNewDefaultsToPortalAuthBase(t *testing.T) {
	handler, err := New(Config{
		PublicBaseURL: "https://chat.test",
		Verifier:      testVerifier{},
		OpenAIHandler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if handler.apiAuthBaseURL != "https://portal.kittypaw.app/auth" {
		t.Fatalf("apiAuthBaseURL = %q, want portal auth base", handler.apiAuthBaseURL)
	}
}

func TestCallbackExchangesCodeAndSetsHttpOnlySessionCookie(t *testing.T) {
	var exchangeCalled bool
	handler := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/web/exchange" {
			t.Fatalf("exchange path = %q", r.URL.Path)
		}
		var body struct {
			Code         string `json:"code"`
			CodeVerifier string `json:"code_verifier"`
			RedirectURI  string `json:"redirect_uri"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode exchange body: %v", err)
		}
		if body.Code != "oauth-code" || body.CodeVerifier == "" || body.RedirectURI != "https://chat.test/auth/callback" {
			t.Fatalf("bad exchange body: %+v", body)
		}
		exchangeCalled = true
		_ = json.NewEncoder(w).Encode(tokenResponse{
			AccessToken:  "api-token",
			RefreshToken: "refresh-token",
			TokenType:    "Bearer",
			ExpiresIn:    900,
		})
	}, nil)

	state := startLogin(t, handler)
	req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=oauth-code&state="+url.QueryEscape(state), nil)
	rr := httptest.NewRecorder()

	handler.Routes().ServeHTTP(rr, req)

	if !exchangeCalled {
		t.Fatal("exchange endpoint was not called")
	}
	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Location"); got != "/chat/" {
		t.Fatalf("Location = %q, want /chat/", got)
	}
	cookie := findCookie(rr.Result().Cookies(), sessionCookieName)
	if cookie == nil {
		t.Fatal("session cookie missing")
	}
	if !cookie.HttpOnly {
		t.Fatal("session cookie must be HttpOnly")
	}
	if !cookie.Secure {
		t.Fatal("session cookie must be Secure for https public base URL")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("SameSite = %v, want Lax", cookie.SameSite)
	}
}

func TestCallbackFailurePageUsesStyledShell(t *testing.T) {
	handler := newTestHandler(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(tokenResponse{
			AccessToken:  "rejected-token",
			RefreshToken: "refresh-token",
			TokenType:    "Bearer",
			ExpiresIn:    900,
		})
	}, nil)

	state := startLogin(t, handler)
	req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=oauth-code&state="+url.QueryEscape(state), nil)
	rr := httptest.NewRecorder()

	handler.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("content-type = %q, want html", ct)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`class="entry-panel auth-message-panel"`,
		`Login token was rejected.`,
		`Return to KittyPaw Home`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("styled callback page missing %q:\n%s", want, body)
		}
	}
}

func TestAppAPIRoutesProxiesWithServerSideBearerToken(t *testing.T) {
	var authHeader string
	openAI := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/routes" {
			t.Fatalf("proxied path = %q, want /v1/routes", r.URL.Path)
		}
		authHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	})
	handler := newTestHandler(t, nil, openAI)
	cookie := createSessionCookie(t, handler, "api-token", "refresh-token", 15*time.Minute)
	req := httptest.NewRequest(http.MethodGet, "/chat/api/routes", nil)
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()

	handler.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if authHeader != "Bearer api-token" {
		t.Fatalf("Authorization = %q, want server-side bearer token", authHeader)
	}
}

func TestAppAPIRefreshesExpiredSessionBeforeProxy(t *testing.T) {
	var refreshCalled bool
	handler := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/token/refresh" {
			t.Fatalf("refresh path = %q", r.URL.Path)
		}
		refreshCalled = true
		_ = json.NewEncoder(w).Encode(tokenResponse{
			AccessToken:  "fresh-token",
			RefreshToken: "fresh-refresh",
			TokenType:    "Bearer",
			ExpiresIn:    900,
		})
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer fresh-token" {
			t.Fatalf("Authorization = %q, want fresh token", got)
		}
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	cookie := createSessionCookie(t, handler, "old-token", "old-refresh", -time.Minute)
	req := httptest.NewRequest(http.MethodGet, "/chat/api/routes", nil)
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()

	handler.Routes().ServeHTTP(rr, req)

	if !refreshCalled {
		t.Fatal("refresh endpoint was not called")
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	handler.sessions.mu.Lock()
	defer handler.sessions.mu.Unlock()
	if len(handler.sessions.data) != 1 {
		t.Fatalf("session count = %d, want refresh to update existing session only", len(handler.sessions.data))
	}
	if got := handler.sessions.data[cookie.Value].AccessToken; got != "fresh-token" {
		t.Fatalf("stored access token = %q, want fresh-token", got)
	}
}

func TestAppAPIRetriesOnceAfterProxyUnauthorized(t *testing.T) {
	var refreshCalled bool
	var proxyCalls int
	handler := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/token/refresh" {
			t.Fatalf("refresh path = %q", r.URL.Path)
		}
		refreshCalled = true
		_ = json.NewEncoder(w).Encode(tokenResponse{
			AccessToken:  "fresh-token",
			RefreshToken: "fresh-refresh",
			TokenType:    "Bearer",
			ExpiresIn:    900,
		})
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyCalls++
		if proxyCalls == 1 {
			writeJSONError(w, http.StatusUnauthorized, "expired")
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer fresh-token" {
			t.Fatalf("Authorization = %q, want fresh token", got)
		}
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	cookie := createSessionCookie(t, handler, "api-token", "refresh-token", 15*time.Minute)
	req := httptest.NewRequest(http.MethodGet, "/chat/api/routes", nil)
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()

	handler.Routes().ServeHTTP(rr, req)

	if !refreshCalled {
		t.Fatal("refresh endpoint was not called")
	}
	if proxyCalls != 2 {
		t.Fatalf("proxy calls = %d, want 2", proxyCalls)
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

func newTestHandler(t *testing.T, apiHandler http.HandlerFunc, openAI http.Handler) *Handler {
	t.Helper()
	if apiHandler == nil {
		apiHandler = func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}
	}
	apiServer := httptest.NewServer(apiHandler)
	t.Cleanup(apiServer.Close)
	if openAI == nil {
		openAI = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
	}
	verifier := testVerifier{validAPI: map[string]bool{
		"api-token":   true,
		"old-token":   true,
		"fresh-token": true,
	}}
	handler, err := New(Config{
		PublicBaseURL:  "https://chat.test",
		APIAuthBaseURL: strings.TrimSuffix(apiServer.URL, "/") + "/auth",
		Verifier:       verifier,
		OpenAIHandler:  openAI,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return handler
}

func startLogin(t *testing.T, handler *Handler) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/auth/login/google", nil)
	rr := httptest.NewRecorder()
	handler.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("login status = %d, want 302", rr.Code)
	}
	u, err := url.Parse(rr.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse login redirect: %v", err)
	}
	state := u.Query().Get("state")
	if state == "" {
		t.Fatal("state missing from login redirect")
	}
	return state
}

func createSessionCookie(t *testing.T, handler *Handler, accessToken, refreshToken string, ttl time.Duration) *http.Cookie {
	t.Helper()
	id, err := handler.sessions.Create(tokenSession{
		AccessToken:      accessToken,
		RefreshToken:     refreshToken,
		TokenType:        "Bearer",
		AccessExpiresAt:  handler.now().Add(ttl),
		SessionExpiresAt: handler.now().Add(30 * 24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return &http.Cookie{Name: sessionCookieName, Value: id}
}

func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

type testVerifier struct {
	validAPI map[string]bool
}

func (v testVerifier) VerifyAPIClient(_ context.Context, token string) (identity.APIClientClaims, error) {
	if !v.validAPI[token] {
		return identity.APIClientClaims{}, identity.ErrUnauthorized
	}
	return identity.APIClientClaims{
		Subject:   "user_1",
		Audiences: []string{identity.AudienceKittyHome},
		Version:   identity.CredentialVersion2,
		Scopes:    []identity.Scope{identity.ScopeChatRelay, identity.ScopeModelsRead},
		UserID:    "user_1",
	}, nil
}

func (v testVerifier) VerifyDevice(_ context.Context, _ string) (identity.DeviceClaims, error) {
	return identity.DeviceClaims{}, identity.ErrUnauthorized
}
