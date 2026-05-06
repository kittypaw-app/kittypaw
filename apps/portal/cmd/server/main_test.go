package main

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kittypaw-app/kittyportal/internal/auth"
	"github.com/kittypaw-app/kittyportal/internal/auth/testfixture"
	"github.com/kittypaw-app/kittyportal/internal/config"
	"github.com/kittypaw-app/kittyportal/internal/connectadmin"
	"github.com/kittypaw-app/kittyportal/internal/model"
)

func testJWTKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	cfg := config.LoadForTest()
	return cfg.JWTPrivateKey, cfg.JWTKID
}

func testRouter(t *testing.T) http.Handler {
	t.Helper()
	cfg := config.LoadForTest()
	r, cleanup := NewRouter(cfg, nil, nil, nil, nil)
	t.Cleanup(cleanup)
	return r
}

func TestHealthEndpoint(t *testing.T) {
	r := testRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if body["status"] != "healthy" {
		t.Fatalf("expected status=healthy, got %q", body["status"])
	}
	if body["version"] == "" {
		t.Fatalf("expected non-empty version in health body: %v", body)
	}
	if body["commit"] == "" {
		t.Fatalf("expected non-empty commit in health body: %v", body)
	}
}

func TestServeHTTPListensOnUnixSocket(t *testing.T) {
	dir, err := os.MkdirTemp("/private/tmp", "kittyportal-socket-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "kittyportal.sock")
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- serveHTTP(srv, socketPath)
	}()
	waitForSocket(t, socketPath)

	client := &http.Client{Transport: unixSocketTransport(socketPath)}
	resp, err := client.Get("http://unix/health")
	if err != nil {
		t.Fatalf("GET over unix socket: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}

	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("serveHTTP returned %v", err)
	}
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("socket should be removed after shutdown, stat err=%v", err)
	}
}

func TestDiscoveryReturnsKakaoRelayURL(t *testing.T) {
	cfg := config.LoadForTest()
	cfg.KakaoRelayURL = "https://kakao.kittypaw.app"
	r, cleanup := NewRouter(cfg, nil, nil, nil, nil)
	t.Cleanup(cleanup)

	req := httptest.NewRequest(http.MethodGet, "/discovery", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if got := body["kakao_relay_url"]; got != "https://kakao.kittypaw.app" {
		t.Fatalf("expected kakao_relay_url=https://kakao.kittypaw.app, got %q", got)
	}
	if _, ok := body["relay_url"]; ok {
		t.Fatalf("legacy relay_url key must not be present: %v", body)
	}
}

func waitForSocket(t *testing.T, socketPath string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socketPath); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("socket %s was not created", socketPath)
}

func unixSocketTransport(socketPath string) http.RoundTripper {
	return &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("unix", socketPath)
		},
	}
}

func TestDiscoveryReturnsChatRelayURL(t *testing.T) {
	cfg := config.LoadForTest()
	cfg.ChatRelayURL = "https://chat.kittypaw.app"
	r, cleanup := NewRouter(cfg, nil, nil, nil, nil)
	t.Cleanup(cleanup)

	req := httptest.NewRequest(http.MethodGet, "/discovery", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if got := body["chat_relay_url"]; got != "https://chat.kittypaw.app" {
		t.Fatalf("expected chat_relay_url=https://chat.kittypaw.app, got %q", got)
	}
}

func TestPortalHomeEndpoint(t *testing.T) {
	cfg := config.LoadForTest()
	cfg.BaseURL = "https://portal.kittypaw.app"
	cfg.APIBaseURL = "https://api.kittypaw.app"
	cfg.ChatRelayURL = "https://chat.kittypaw.app"
	r, cleanup := NewRouter(cfg, nil, nil, nil, nil)
	t.Cleanup(cleanup)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "portal.kittypaw.app"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want html", ct)
	}
	body := w.Body.String()
	for _, want := range []string{
		`id="portal-home"`,
		`KittyPaw Portal`,
		`href="https://chat.kittypaw.app"`,
		`href="/discovery"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("portal home missing %q:\n%s", want, body)
		}
	}
}

func TestConnectHomeEndpoint(t *testing.T) {
	cfg := config.LoadForTest()
	cfg.BaseURL = "https://portal.kittypaw.app"
	cfg.APIBaseURL = "https://api.kittypaw.app"
	cfg.ConnectBaseURL = "https://connect.kittypaw.app"
	r, cleanup := NewRouter(cfg, nil, nil, nil, nil)
	t.Cleanup(cleanup)

	req := httptest.NewRequest(http.MethodGet, "/connect", nil)
	req.Host = "connect.kittypaw.app"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "KittyPaw Connect") {
		t.Fatalf("connect page missing brand: %s", w.Body.String())
	}
}

func TestConnectGmailLoginRouteUsesConnectGoogleClient(t *testing.T) {
	cfg := config.LoadForTest()
	cfg.BaseURL = "https://portal.kittypaw.app"
	cfg.APIBaseURL = "https://api.kittypaw.app"
	cfg.ConnectBaseURL = "https://connect.kittypaw.app"
	cfg.GoogleClientID = "identity-client-id"
	cfg.ConnectGoogleClientID = "connect-client-id"
	cfg.ConnectGoogleAuthURL = "https://accounts.example/auth"
	r, cleanup := NewRouter(cfg, nil, nil, nil, nil)
	t.Cleanup(cleanup)

	req := httptest.NewRequest(http.MethodGet, "/connect/gmail/login?mode=code", nil)
	req.Host = "connect.kittypaw.app"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body=%s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if got := u.Query().Get("client_id"); got != "connect-client-id" {
		t.Fatalf("client_id = %q, want connect client, Location=%s", got, loc)
	}
	if got := u.Query().Get("redirect_uri"); got != "https://connect.kittypaw.app/connect/gmail/callback" {
		t.Fatalf("redirect_uri = %q", got)
	}
}

func TestConnectXLoginRouteUsesConnectXClient(t *testing.T) {
	cfg := config.LoadForTest()
	cfg.BaseURL = "https://portal.kittypaw.app"
	cfg.APIBaseURL = "https://api.kittypaw.app"
	cfg.ConnectBaseURL = "https://connect.kittypaw.app"
	cfg.ConnectXClientID = "x-connect-client-id"
	cfg.ConnectXAuthURL = "https://x.example/auth"
	users := &fakeRouterUserStore{users: map[string]*model.User{
		"user-1": {ID: "user-1", Email: "alice@example.com"},
	}}
	r, cleanup := NewRouter(cfg, users, nil, nil, &fakeConnectAdminStore{allowed: true})
	t.Cleanup(cleanup)

	req := httptest.NewRequest(http.MethodGet, "/connect/x/login?mode=code", nil)
	req.Host = "connect.kittypaw.app"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
		t.Fatalf("direct status = %d, want 401 or 403; body=%s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "" {
		t.Fatalf("direct Location = %q, want no redirect", loc)
	}

	sessionReq := httptest.NewRequest(http.MethodPost, "/connect/x/sessions", strings.NewReader(`{"mode":"code"}`))
	sessionReq.Host = "connect.kittypaw.app"
	sessionReq.Header.Set("Authorization", "Bearer "+testfixture.IssueTestJWT(t, cfg.JWTPrivateKey, cfg.JWTKID, "user-1", 15*time.Minute))
	sessionW := httptest.NewRecorder()
	r.ServeHTTP(sessionW, sessionReq)
	if sessionW.Code != http.StatusOK {
		t.Fatalf("session status = %d, want 200; body=%s", sessionW.Code, sessionW.Body.String())
	}
	var sessionBody struct {
		LoginURL string `json:"login_url"`
	}
	if err := json.NewDecoder(sessionW.Body).Decode(&sessionBody); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	loginURL, err := url.Parse(sessionBody.LoginURL)
	if err != nil {
		t.Fatalf("parse login_url: %v", err)
	}
	if loginURL.Host != "connect.kittypaw.app" || loginURL.Path != "/connect/x/login" || loginURL.Query().Get("session") == "" {
		t.Fatalf("login_url = %q", sessionBody.LoginURL)
	}

	req = httptest.NewRequest(http.MethodGet, loginURL.RequestURI(), nil)
	req.Host = "connect.kittypaw.app"
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body=%s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if got := u.Query().Get("client_id"); got != "x-connect-client-id" {
		t.Fatalf("client_id = %q, want X connect client, Location=%s", got, loc)
	}
	if got := u.Query().Get("redirect_uri"); got != "https://connect.kittypaw.app/connect/x/callback" {
		t.Fatalf("redirect_uri = %q", got)
	}
	if got := u.Query().Get("scope"); !strings.Contains(got, "offline.access") {
		t.Fatalf("scope = %q, want refresh-capable X scope", got)
	}
}

func TestConnectHostRootShowsConnectHome(t *testing.T) {
	cfg := config.LoadForTest()
	cfg.BaseURL = "https://portal.kittypaw.app"
	cfg.APIBaseURL = "https://api.kittypaw.app"
	cfg.ConnectBaseURL = "https://connect.kittypaw.app"
	r, cleanup := NewRouter(cfg, nil, nil, nil, nil)
	t.Cleanup(cleanup)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "connect.kittypaw.app"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "KittyPaw Connect") {
		t.Fatalf("connect root missing brand: %s", w.Body.String())
	}
}

func TestConnectRoutesOnlyServedOnConnectHost(t *testing.T) {
	cfg := config.LoadForTest()
	cfg.BaseURL = "https://portal.kittypaw.app"
	cfg.APIBaseURL = "https://api.kittypaw.app"
	cfg.ConnectBaseURL = "https://connect.kittypaw.app"
	r, cleanup := NewRouter(cfg, nil, nil, nil, nil)
	t.Cleanup(cleanup)

	req := httptest.NewRequest(http.MethodGet, "/connect", nil)
	req.Host = "portal.kittypaw.app"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("portal host status = %d, want 404", w.Code)
	}
}

func TestConnectRoutesStayHostBoundWhenAPIHostIsCollapsed(t *testing.T) {
	cfg := config.LoadForTest()
	cfg.BaseURL = "https://portal.kittypaw.app"
	cfg.APIBaseURL = cfg.BaseURL
	cfg.ConnectBaseURL = "https://connect.kittypaw.app"
	r, cleanup := NewRouter(cfg, nil, nil, nil, nil)
	t.Cleanup(cleanup)

	req := httptest.NewRequest(http.MethodGet, "/connect", nil)
	req.Host = "portal.kittypaw.app"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("portal host status = %d, want 404", w.Code)
	}
}

func TestIdentityRoutesNotServedOnConnectHost(t *testing.T) {
	cfg := config.LoadForTest()
	cfg.BaseURL = "https://portal.kittypaw.app"
	cfg.APIBaseURL = "https://api.kittypaw.app"
	cfg.ConnectBaseURL = "https://connect.kittypaw.app"
	r, cleanup := NewRouter(cfg, nil, nil, nil, nil)
	t.Cleanup(cleanup)

	req := httptest.NewRequest(http.MethodGet, "/discovery", nil)
	req.Host = "connect.kittypaw.app"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("connect host discovery status = %d, want 404", w.Code)
	}
}

func TestConnectAdminOnlyServedOnPortalHost(t *testing.T) {
	cfg := config.LoadForTest()
	cfg.BaseURL = "https://portal.kittypaw.app"
	cfg.APIBaseURL = "https://api.kittypaw.app"
	cfg.ConnectBaseURL = "https://connect.kittypaw.app"
	cfg.PortalAdminEmails = []string{"admin@example.com"}
	r, cleanup := NewRouter(cfg, &fakeRouterUserStore{}, nil, nil, &fakeConnectAdminStore{})
	t.Cleanup(cleanup)

	req := httptest.NewRequest(http.MethodGet, "/admin/connect", nil)
	req.Host = "connect.kittypaw.app"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("connect host status = %d, want 404", w.Code)
	}
}

func TestConnectAdminOnlyServedOnPortalHostWhenAPIHostIsCollapsed(t *testing.T) {
	cfg := config.LoadForTest()
	cfg.BaseURL = "https://portal.kittypaw.app"
	cfg.APIBaseURL = cfg.BaseURL
	cfg.ConnectBaseURL = "https://connect.kittypaw.app"
	cfg.PortalAdminEmails = []string{"admin@example.com"}
	r, cleanup := NewRouter(cfg, &fakeRouterUserStore{}, nil, nil, &fakeConnectAdminStore{})
	t.Cleanup(cleanup)

	req := httptest.NewRequest(http.MethodGet, "/admin/connect", nil)
	req.Host = "connect.kittypaw.app"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("connect host status = %d, want 404", w.Code)
	}
}

func TestConnectAdminAllowsConfiguredAdmin(t *testing.T) {
	cfg := config.LoadForTest()
	cfg.BaseURL = "https://portal.kittypaw.app"
	cfg.APIBaseURL = "https://api.kittypaw.app"
	cfg.ConnectBaseURL = "https://connect.kittypaw.app"
	cfg.PortalAdminEmails = []string{"admin@example.com"}
	users := &fakeRouterUserStore{users: map[string]*model.User{
		"user-admin": {ID: "user-admin", Email: "admin@example.com"},
	}}
	r, cleanup := NewRouter(cfg, users, nil, nil, &fakeConnectAdminStore{})
	t.Cleanup(cleanup)

	req := httptest.NewRequest(http.MethodGet, "/admin/connect", nil)
	req.Host = "portal.kittypaw.app"
	req.Header.Set("Authorization", "Bearer "+testfixture.IssueTestJWT(t, cfg.JWTPrivateKey, cfg.JWTKID, "user-admin", 15*time.Minute))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("portal host status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "KittyPaw Connect Admin") {
		t.Fatalf("admin page missing title: %s", w.Body.String())
	}
}

func TestConnectAdminRejectsAuthenticatedNonAdmin(t *testing.T) {
	cfg := config.LoadForTest()
	cfg.BaseURL = "https://portal.kittypaw.app"
	cfg.APIBaseURL = "https://api.kittypaw.app"
	cfg.ConnectBaseURL = "https://connect.kittypaw.app"
	cfg.PortalAdminEmails = []string{"admin@example.com"}
	users := &fakeRouterUserStore{users: map[string]*model.User{
		"user-basic": {ID: "user-basic", Email: "user@example.com"},
	}}
	r, cleanup := NewRouter(cfg, users, nil, nil, &fakeConnectAdminStore{})
	t.Cleanup(cleanup)

	req := httptest.NewRequest(http.MethodGet, "/admin/connect", nil)
	req.Host = "portal.kittypaw.app"
	req.Header.Set("Authorization", "Bearer "+testfixture.IssueTestJWT(t, cfg.JWTPrivateKey, cfg.JWTKID, "user-basic", 15*time.Minute))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("portal host status = %d, want 403", w.Code)
	}
}

func TestConnectAdminRejectsAnonymous(t *testing.T) {
	cfg := config.LoadForTest()
	cfg.BaseURL = "https://portal.kittypaw.app"
	cfg.APIBaseURL = "https://api.kittypaw.app"
	cfg.ConnectBaseURL = "https://connect.kittypaw.app"
	cfg.PortalAdminEmails = []string{"admin@example.com"}
	r, cleanup := NewRouter(cfg, &fakeRouterUserStore{}, nil, nil, &fakeConnectAdminStore{})
	t.Cleanup(cleanup)

	req := httptest.NewRequest(http.MethodGet, "/admin/connect", nil)
	req.Host = "portal.kittypaw.app"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("portal host status = %d, want 401", w.Code)
	}
}

func TestDiscoveryReturnsPortalAuthBaseURLAndAPIBaseURL(t *testing.T) {
	cfg := config.LoadForTest()
	cfg.BaseURL = "https://portal.kittypaw.app/"
	cfg.APIBaseURL = "https://api.kittypaw.app"
	r, cleanup := NewRouter(cfg, nil, nil, nil, nil)
	t.Cleanup(cleanup)

	req := httptest.NewRequest(http.MethodGet, "/discovery", nil)
	req.Host = "portal.kittypaw.app"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if got := body["api_base_url"]; got != "https://api.kittypaw.app" {
		t.Fatalf("api_base_url = %q, want api host", got)
	}
	if got := body["auth_base_url"]; got != "https://portal.kittypaw.app/auth" {
		t.Fatalf("auth_base_url = %q, want portal auth host", got)
	}
}

func TestDiscoveryIncludesConnectBaseURL(t *testing.T) {
	cfg := config.LoadForTest()
	cfg.BaseURL = "https://portal.kittypaw.app"
	cfg.APIBaseURL = "https://api.kittypaw.app"
	cfg.ConnectBaseURL = "https://connect.kittypaw.app"
	r, cleanup := NewRouter(cfg, nil, nil, nil, nil)
	t.Cleanup(cleanup)

	req := httptest.NewRequest(http.MethodGet, "/discovery", nil)
	req.Host = "portal.kittypaw.app"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if got := body["connect_base_url"]; got != "https://connect.kittypaw.app" {
		t.Fatalf("connect_base_url = %q", got)
	}
}

func TestSplitHostsRestrictIdentityRoutesToPortalHost(t *testing.T) {
	cfg := config.LoadForTest()
	cfg.BaseURL = "https://portal.kittypaw.app"
	cfg.APIBaseURL = "https://api.kittypaw.app"
	r, cleanup := NewRouter(cfg, nil, nil, nil, nil)
	t.Cleanup(cleanup)

	for _, tc := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/discovery"},
		{method: http.MethodGet, path: "/"},
		{method: http.MethodGet, path: "/.well-known/jwks.json"},
		{method: http.MethodGet, path: "/auth/google"},
		{method: http.MethodPost, path: "/auth/devices/refresh"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Host = "api.kittypaw.app"
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 on api host", w.Code)
			}
		})
	}

	req := httptest.NewRequest(http.MethodGet, "/discovery", nil)
	req.Host = "portal.kittypaw.app"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("portal discovery status = %d, want 200", w.Code)
	}
}

func TestPortalDoesNotServeResourceRoutes(t *testing.T) {
	r := testRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/geo/resolve", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("resource route status = %d, want 404", w.Code)
	}
}

func TestJWKSEndpointAnonymous200(t *testing.T) {
	r := testRouter(t)
	_, wantKID := testJWTKey(t)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}

	var got auth.JWKSet
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(got.Keys) != 1 {
		t.Fatalf("keys len = %d, want 1", len(got.Keys))
	}
	if got.Keys[0].Kid != wantKID {
		t.Fatalf("kid = %q, want %q", got.Keys[0].Kid, wantKID)
	}
}

func TestJWKSEndpointCacheControl(t *testing.T) {
	r := testRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	const want = "public, max-age=600"
	if got := w.Header().Get("Cache-Control"); got != want {
		t.Fatalf("Cache-Control = %q, want %q", got, want)
	}
}

func TestNewRouterCleanupReleasesStores(t *testing.T) {
	cfg := config.LoadForTest()
	r, cleanup := NewRouter(cfg, nil, nil, nil, nil)
	if r == nil {
		t.Fatal("expected non-nil router")
	}
	if cleanup == nil {
		t.Fatal("expected non-nil cleanup")
	}
	cleanup()
	cleanup()
}

func TestDevicesRoutesWiredPairNoAuth401(t *testing.T) {
	r := testRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/auth/devices/pair", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestDevicesRoutesWiredRefreshNoBody400(t *testing.T) {
	r := testRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/auth/devices/refresh", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestDevicesRoutesWiredListNoAuth401(t *testing.T) {
	r := testRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/auth/devices", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestDevicesRoutesWiredDeleteNoAuth401(t *testing.T) {
	r := testRouter(t)
	req := httptest.NewRequest(http.MethodDelete, "/auth/devices/00000000-0000-0000-0000-000000000000", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestRatelimitRefreshBucketIsolated(t *testing.T) {
	r := testRouter(t)
	const peer = "192.0.2.99:1234"

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/discovery", nil)
		req.RemoteAddr = peer
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code == http.StatusTooManyRequests {
			t.Fatalf("call #%d already throttled", i+1)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/discovery", nil)
	req.RemoteAddr = peer
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("discovery #6 status = %d, want 429", w.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/auth/devices/refresh", nil)
	req.RemoteAddr = peer
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code == http.StatusTooManyRequests {
		t.Fatal("refresh request throttled despite separate bucket")
	}
}

func TestNotFound(t *testing.T) {
	r := testRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestRouterTrueClientIPHeaderDoesNotBypassRateLimit(t *testing.T) {
	r := testRouter(t)

	const peer = "192.0.2.71:1234"
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/discovery", nil)
		req.RemoteAddr = peer
		req.Header.Set("True-Client-IP", fmt.Sprintf("198.51.100.%d", i+1))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code == http.StatusTooManyRequests {
			t.Fatalf("call #%d unexpectedly throttled: %d", i+1, w.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/discovery", nil)
	req.RemoteAddr = peer
	req.Header.Set("True-Client-IP", "198.51.100.99")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on 6th call, got %d", w.Code)
	}
}

func TestRouterXForwardedForHeaderDoesNotBypassRateLimit(t *testing.T) {
	r := testRouter(t)

	const peer = "192.0.2.72:1234"
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/discovery", nil)
		req.RemoteAddr = peer
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d, 10.0.0.1", i+1))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code == http.StatusTooManyRequests {
			t.Fatalf("call #%d unexpectedly throttled: %d", i+1, w.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/discovery", nil)
	req.RemoteAddr = peer
	req.Header.Set("X-Forwarded-For", "198.51.100.99, 10.0.0.1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on 6th call, got %d", w.Code)
	}
}

func TestRouterXRealIPHeaderTrustedForRateLimit(t *testing.T) {
	r := testRouter(t)

	const nginxPeer = "127.0.0.1:8443"
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/discovery", nil)
		req.RemoteAddr = nginxPeer
		req.Header.Set("X-Real-IP", fmt.Sprintf("198.51.100.%d", i+1))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code == http.StatusTooManyRequests {
			t.Fatalf("user %d throttled", i+1)
		}
	}

	const samePeer = "198.51.100.42"
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/discovery", nil)
		req.RemoteAddr = nginxPeer
		req.Header.Set("X-Real-IP", samePeer)
		r.ServeHTTP(httptest.NewRecorder(), req)
	}
	req := httptest.NewRequest(http.MethodGet, "/discovery", nil)
	req.RemoteAddr = nginxPeer
	req.Header.Set("X-Real-IP", samePeer)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on 6th X-Real-IP=%s call, got %d", samePeer, w.Code)
	}
}

type fakeRouterUserStore struct {
	users map[string]*model.User
}

func (s *fakeRouterUserStore) CreateOrUpdate(context.Context, string, string, string, string, string) (*model.User, error) {
	return nil, errors.New("CreateOrUpdate not implemented")
}

func (s *fakeRouterUserStore) FindByID(_ context.Context, id string) (*model.User, error) {
	if s == nil || s.users == nil {
		return nil, model.ErrNotFound
	}
	user, ok := s.users[id]
	if !ok {
		return nil, model.ErrNotFound
	}
	clone := *user
	return &clone, nil
}

type fakeConnectAdminStore struct {
	policies    []connectadmin.ProviderPolicy
	auditEvents []connectadmin.AuditEvent
	allowed     bool
}

func (s *fakeConnectAdminStore) UpsertProviderPolicy(_ context.Context, policy connectadmin.ProviderPolicy) error {
	s.policies = append(s.policies, policy)
	return nil
}

func (s *fakeConnectAdminStore) GetProviderPolicy(_ context.Context, providerID string) (connectadmin.ProviderPolicy, error) {
	for _, policy := range s.policies {
		if policy.ProviderID == providerID {
			return policy, nil
		}
	}
	return connectadmin.ProviderPolicy{}, nil
}

func (s *fakeConnectAdminStore) ListProviderPolicies(context.Context) ([]connectadmin.ProviderPolicy, error) {
	return append([]connectadmin.ProviderPolicy(nil), s.policies...), nil
}

func (s *fakeConnectAdminStore) UpsertUserEntitlement(context.Context, connectadmin.UserEntitlement) error {
	return nil
}

func (s *fakeConnectAdminStore) UpdateUserEntitlementWithAudit(_ context.Context, _ connectadmin.UserEntitlement, event connectadmin.AuditEvent) error {
	s.auditEvents = append(s.auditEvents, event)
	return nil
}

func (s *fakeConnectAdminStore) UserAllowed(context.Context, string, string) (bool, error) {
	if s == nil {
		return false, nil
	}
	return s.allowed, nil
}

func (s *fakeConnectAdminStore) AppendAuditEvent(_ context.Context, event connectadmin.AuditEvent) error {
	s.auditEvents = append(s.auditEvents, event)
	return nil
}

func (s *fakeConnectAdminStore) ListAuditEvents(context.Context, int) ([]connectadmin.AuditEvent, error) {
	return append([]connectadmin.AuditEvent(nil), s.auditEvents...), nil
}

func (s *fakeConnectAdminStore) EnsureDefaultPolicies(context.Context, connectadmin.ProviderRegistry) error {
	return nil
}
