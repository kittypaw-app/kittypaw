package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kittypaw-app/kittyportal/internal/auth"
	"github.com/kittypaw-app/kittyportal/internal/config"
	"github.com/kittypaw-app/kittyportal/internal/model"
)

const (
	testChatRedirectURI = "https://chat.kittypaw.app/auth/callback"
	testChatState       = "chat-csrf-state-abc"
	// pre-computed S256 of testVerifier ("test-verifier-12345...") so test
	// doesn't depend on auth.ChallengeS256 internals to compute it. We use
	// the helper to fill in the actual challenge dynamically below.
	testVerifier = "test-verifier-12345678901234567890123456789012345"
)

func setupWebTest(t *testing.T) (*auth.OAuthHandler, auth.WebLoginConfig, *mockUserStore) {
	t.Helper()
	cfg := config.LoadForTest()

	userStore := newMockUserStore()
	// Pre-create a user for exchange happy-path.
	_, _ = userStore.CreateOrUpdate(context.Background(), "google", "g-1", "u@t.com", "U", "")

	states := auth.NewStateStore()
	t.Cleanup(states.Close)
	codes := auth.NewWebCodeStore()
	t.Cleanup(codes.Close)

	h := &auth.OAuthHandler{
		UserStore:         userStore,
		RefreshTokenStore: &mockRefreshTokenStore{},
		StateStore:        states,
		WebCodeStore:      codes,
		JWTPrivateKey:     cfg.JWTPrivateKey,
		JWTKID:            cfg.JWTKID,
	}
	webCfg := auth.WebLoginConfig{
		GoogleCfg: auth.GoogleConfig{
			ClientID:     "test-client",
			ClientSecret: "test-secret",
			RedirectURL:  "http://localhost:8080/auth/google/callback",
		},
		CodeStore:            codes,
		RedirectURIAllowlist: []string{testChatRedirectURI},
	}
	return h, webCfg, userStore
}

// TestWebGoogleLogin_Validations pins all the 400-return paths in one
// table — these are the auth-layer guard rails the chat backend will
// hit during integration. Each missing/malformed parameter must surface
// as a distinct 400 rather than silently passing the bad request along
// to Google.
func TestWebGoogleLogin_Validations(t *testing.T) {
	h, webCfg, _ := setupWebTest(t)

	challenge := auth.ChallengeS256(testVerifier)
	cases := []struct {
		name  string
		query url.Values
	}{
		{
			name: "missing redirect_uri",
			query: url.Values{
				"state":                 {testChatState},
				"code_challenge":        {challenge},
				"code_challenge_method": {"S256"},
			},
		},
		{
			name: "missing state",
			query: url.Values{
				"redirect_uri":          {testChatRedirectURI},
				"code_challenge":        {challenge},
				"code_challenge_method": {"S256"},
			},
		},
		{
			name: "missing code_challenge",
			query: url.Values{
				"redirect_uri":          {testChatRedirectURI},
				"state":                 {testChatState},
				"code_challenge_method": {"S256"},
			},
		},
		{
			name: "plain method rejected",
			query: url.Values{
				"redirect_uri":          {testChatRedirectURI},
				"state":                 {testChatState},
				"code_challenge":        {challenge},
				"code_challenge_method": {"plain"},
			},
		},
		{
			name: "redirect_uri not in allowlist",
			query: url.Values{
				"redirect_uri":          {"https://attacker.example/auth/callback"},
				"state":                 {testChatState},
				"code_challenge":        {challenge},
				"code_challenge_method": {"S256"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/auth/web/google?"+tc.query.Encode(), nil)
			w := httptest.NewRecorder()
			h.HandleWebGoogleLogin(webCfg).ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

// TestWebGoogleLogin_Happy: a valid request 302s to Google with our
// own state + verifier-derived code_challenge. Google's PKCE chain is
// independent from chat's — verify both that the chat redirect_uri is
// NOT leaked to Google and that we generate our own state.
func TestWebGoogleLogin_Happy(t *testing.T) {
	h, webCfg, _ := setupWebTest(t)
	challenge := auth.ChallengeS256(testVerifier)

	q := url.Values{
		"redirect_uri":          {testChatRedirectURI},
		"state":                 {testChatState},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	req := httptest.NewRequest(http.MethodGet, "/auth/web/google?"+q.Encode(), nil)
	w := httptest.NewRecorder()
	h.HandleWebGoogleLogin(webCfg).ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "accounts.google.com") {
		t.Fatalf("redirect not to Google: %s", loc)
	}
	// chat_state must NOT appear in the Google redirect (we generate our own).
	if strings.Contains(loc, testChatState) {
		t.Fatal("chat state must not leak to Google redirect URL")
	}
	// chat redirect_uri must NOT appear in Google params (Google sees our callback).
	if strings.Contains(loc, url.QueryEscape(testChatRedirectURI)) {
		t.Fatal("chat redirect_uri must not leak to Google redirect URL")
	}
}

func TestWebGoogleLogin_UsesConfiguredAuthURL(t *testing.T) {
	h, webCfg, _ := setupWebTest(t)
	h.GoogleAuthURL = "http://oauth.local/google/auth"
	challenge := auth.ChallengeS256(testVerifier)

	q := url.Values{
		"redirect_uri":          {testChatRedirectURI},
		"state":                 {testChatState},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	req := httptest.NewRequest(http.MethodGet, "/auth/web/google?"+q.Encode(), nil)
	w := httptest.NewRecorder()
	h.HandleWebGoogleLogin(webCfg).ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "http://oauth.local/google/auth?") {
		t.Fatalf("redirect URL = %q, want configured auth URL", loc)
	}
}

// TestWebExchange_Happy: the round-trip from Create → Consume.
// Verifier matches the stored S256 challenge → token issued.
//
// Beyond shape: pin the multi-aud + scope contract so a future change
// to DefaultAPIClientAudiences/Scopes can't silently break what chat
// expects. Without this, scopes.go drift would leave the test green
// while chat fails verifier in prod.
func TestWebExchange_Happy(t *testing.T) {
	cfg := config.LoadForTest()
	h, _, userStore := setupWebTest(t)

	// Pre-seed a code as if the callback already ran.
	user := userStore.users["google:g-1"]
	code, err := h.WebCodeStore.Create(auth.WebCodeEntry{
		UserID:        user.ID,
		RedirectURI:   testChatRedirectURI,
		CodeChallenge: auth.ChallengeS256(testVerifier),
	})
	if err != nil {
		t.Fatalf("Create code: %v", err)
	}

	body, _ := json.Marshal(map[string]string{
		"code":          code,
		"code_verifier": testVerifier,
		"redirect_uri":  testChatRedirectURI,
	})
	req := httptest.NewRequest(http.MethodPost, "/auth/web/exchange", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.HandleWebExchange().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store (RFC 6749 §5.1)", got)
	}
	var resp auth.TokenResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.AccessToken == "" || resp.RefreshToken == "" {
		t.Fatal("expected non-empty token pair")
	}
	if resp.TokenType != "Bearer" || resp.ExpiresIn != int(auth.AccessTokenTTL.Seconds()) {
		t.Fatalf("unexpected token shape: %+v", resp)
	}

	// Pin multi-aud contract — token MUST verify under API, legacy Chat,
	// and Home during the Home migration.
	provider := auth.NewSingleKeyProvider(&cfg.JWTPrivateKey.PublicKey, cfg.JWTKID)
	for _, aud := range []string{auth.AudienceAPI, auth.AudienceChat, auth.AudienceHome} {
		claims, verr := auth.Verify(resp.AccessToken, provider, aud)
		if verr != nil {
			t.Fatalf("Verify(aud=%s): %v", aud, verr)
		}
		if claims.UserID != user.ID {
			t.Errorf("aud=%s: sub = %q, want %q", aud, claims.UserID, user.ID)
		}
	}

	// Pin scope contract — chat verifier checks scope presence.
	claims, _ := auth.Verify(resp.AccessToken, provider, auth.AudienceChat)
	for _, want := range []string{auth.ScopeChatRelay, auth.ScopeModelsRead} {
		found := false
		for _, got := range claims.Scope {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("scope %q missing; got %v", want, claims.Scope)
		}
	}
}

// TestWebExchange_OriginRejected pins the BFF boundary: a request
// carrying ANY Origin header is rejected with 403, regardless of value.
// This defends against an operator misconfiguring CORS_ORIGINS to
// include chat.kittypaw.app — without this gate, a browser at chat
// could call /auth/web/exchange directly and the refresh_token would
// land in browser-side JS, undoing the entire BFF design.
func TestWebExchange_OriginRejected(t *testing.T) {
	h, _, userStore := setupWebTest(t)
	user := userStore.users["google:g-1"]
	code, _ := h.WebCodeStore.Create(auth.WebCodeEntry{
		UserID:        user.ID,
		RedirectURI:   testChatRedirectURI,
		CodeChallenge: auth.ChallengeS256(testVerifier),
	})

	body, _ := json.Marshal(map[string]string{
		"code":          code,
		"code_verifier": testVerifier,
		"redirect_uri":  testChatRedirectURI,
	})
	req := httptest.NewRequest(http.MethodPost, "/auth/web/exchange", bytes.NewReader(body))
	req.Header.Set("Origin", "https://chat.kittypaw.app")
	w := httptest.NewRecorder()
	h.HandleWebExchange().ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 (browser-direct blocked), got %d", w.Code)
	}
}

// TestWebGoogleLogin_LengthCaps: oversized state or code_challenge
// must be rejected before allocating a state-store entry. Without
// caps, a misbehaving client could push multi-KB strings into our
// in-process state map.
func TestWebGoogleLogin_LengthCaps(t *testing.T) {
	h, webCfg, _ := setupWebTest(t)
	challenge := auth.ChallengeS256(testVerifier)
	huge := strings.Repeat("a", 2048)

	cases := []struct {
		name  string
		query url.Values
	}{
		{
			name: "state too long",
			query: url.Values{
				"redirect_uri":          {testChatRedirectURI},
				"state":                 {huge},
				"code_challenge":        {challenge},
				"code_challenge_method": {"S256"},
			},
		},
		{
			name: "code_challenge too long",
			query: url.Values{
				"redirect_uri":          {testChatRedirectURI},
				"state":                 {testChatState},
				"code_challenge":        {huge},
				"code_challenge_method": {"S256"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/auth/web/google?"+tc.query.Encode(), nil)
			w := httptest.NewRecorder()
			h.HandleWebGoogleLogin(webCfg).ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

// TestWebExchange_VerifierMismatch: an attacker who steals the code but
// not the verifier MUST be rejected. This is the entire point of PKCE.
func TestWebExchange_VerifierMismatch(t *testing.T) {
	h, _, userStore := setupWebTest(t)
	user := userStore.users["google:g-1"]
	code, _ := h.WebCodeStore.Create(auth.WebCodeEntry{
		UserID:        user.ID,
		RedirectURI:   testChatRedirectURI,
		CodeChallenge: auth.ChallengeS256(testVerifier),
	})

	body, _ := json.Marshal(map[string]string{
		"code":          code,
		"code_verifier": "wrong-verifier-but-correct-length-12345678901234567",
		"redirect_uri":  testChatRedirectURI,
	})
	req := httptest.NewRequest(http.MethodPost, "/auth/web/exchange", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.HandleWebExchange().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

// TestWebExchange_RedirectURIMismatch: redirect_uri rebinding. Even
// with the right verifier, an attacker swapping redirect_uri at exchange
// time gets 400. Defends a code-leak scenario where the attacker tries
// to redirect tokens to a different host.
func TestWebExchange_RedirectURIMismatch(t *testing.T) {
	h, _, userStore := setupWebTest(t)
	user := userStore.users["google:g-1"]
	code, _ := h.WebCodeStore.Create(auth.WebCodeEntry{
		UserID:        user.ID,
		RedirectURI:   testChatRedirectURI,
		CodeChallenge: auth.ChallengeS256(testVerifier),
	})

	body, _ := json.Marshal(map[string]string{
		"code":          code,
		"code_verifier": testVerifier,
		"redirect_uri":  "https://attacker.example/auth/callback",
	})
	req := httptest.NewRequest(http.MethodPost, "/auth/web/exchange", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.HandleWebExchange().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// TestWebExchange_UnknownCode: replay attempt or pure guess returns
// silent 401 (don't disclose code-store contents).
func TestWebExchange_UnknownCode(t *testing.T) {
	h, _, _ := setupWebTest(t)

	body, _ := json.Marshal(map[string]string{
		"code":          "fabricated-code",
		"code_verifier": testVerifier,
		"redirect_uri":  testChatRedirectURI,
	})
	req := httptest.NewRequest(http.MethodPost, "/auth/web/exchange", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.HandleWebExchange().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

// TestWebExchange_MissingFields covers all three required body fields.
func TestWebExchange_MissingFields(t *testing.T) {
	h, _, _ := setupWebTest(t)

	cases := []map[string]string{
		{"code_verifier": testVerifier, "redirect_uri": testChatRedirectURI}, // missing code
		{"code": "x", "redirect_uri": testChatRedirectURI},                   // missing verifier
		{"code": "x", "code_verifier": testVerifier},                         // missing redirect_uri
	}
	for i, c := range cases {
		body, _ := json.Marshal(c)
		req := httptest.NewRequest(http.MethodPost, "/auth/web/exchange", bytes.NewReader(body))
		w := httptest.NewRecorder()
		h.HandleWebExchange().ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("case %d: expected 400, got %d", i, w.Code)
		}
	}
}

// TestWebFlow_PKCEChainSeparation: portal MUST generate its OWN
// verifier for the Google round-trip — using chat's challenge as our
// verifier (the natural-looking shortcut) would mean Google receives
// chat's challenge as our challenge. Pinning here so a future
// "simplification" doesn't conflate the two PKCE chains.
//
// Mechanic: HandleWebGoogleLogin's redirect to Google must include a
// code_challenge that is NOT chat's challenge. Test asserts inequality
// between the chat-supplied challenge and what we send to Google.
func TestWebFlow_PKCEChainSeparation(t *testing.T) {
	h, webCfg, _ := setupWebTest(t)
	chatChallenge := auth.ChallengeS256(testVerifier)

	q := url.Values{
		"redirect_uri":          {testChatRedirectURI},
		"state":                 {testChatState},
		"code_challenge":        {chatChallenge},
		"code_challenge_method": {"S256"},
	}
	req := httptest.NewRequest(http.MethodGet, "/auth/web/google?"+q.Encode(), nil)
	w := httptest.NewRecorder()
	h.HandleWebGoogleLogin(webCfg).ServeHTTP(w, req)

	loc := w.Header().Get("Location")
	parsed, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	googleChallenge := parsed.Query().Get("code_challenge")
	if googleChallenge == chatChallenge {
		t.Fatal("Google challenge must differ from chat challenge — independent PKCE chains")
	}
	if googleChallenge == "" {
		t.Fatal("Google redirect missing code_challenge")
	}
}

// Helper: ensure model package types resolve. Compile-time check only —
// keeps the import non-stale if other tests are removed later.
var _ = model.User{}
