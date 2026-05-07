package connect

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kittypaw-app/kittyportal/internal/auth"
	"github.com/kittypaw-app/kittyportal/internal/model"
)

func testHandler(t *testing.T) (*Handler, *auth.StateStore, *CodeStore, *httptest.Server) {
	t.Helper()
	fakeOAuth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			switch r.Form.Get("grant_type") {
			case "authorization_code":
				if strings.Contains(r.Form.Get("redirect_uri"), "/connect/x/callback") {
					fmt.Fprint(w, `{"access_token":"x-access","refresh_token":"x-refresh","token_type":"bearer","expires_in":7200,"scope":"`+XReadOnlyScope+`"}`)
				} else {
					fmt.Fprint(w, `{"access_token":"gmail-access","refresh_token":"gmail-refresh","token_type":"Bearer","expires_in":3600,"scope":"`+GmailReadOnlyScope+`"}`)
				}
			case "refresh_token":
				if r.Form.Get("refresh_token") == "x-refresh" {
					fmt.Fprint(w, `{"access_token":"x-access-2","refresh_token":"x-refresh-2","token_type":"bearer","expires_in":7200,"scope":"`+XReadOnlyScope+`"}`)
				} else {
					fmt.Fprint(w, `{"access_token":"gmail-access-2","token_type":"Bearer","expires_in":3600,"scope":"`+GmailReadOnlyScope+`"}`)
				}
			default:
				t.Fatalf("grant_type = %q", r.Form.Get("grant_type"))
			}
		case "/userinfo":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"email":"alice@example.com"}`)
		case "/users/me":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":{"id":"123","name":"Jay Park","username":"jaypark"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fakeOAuth.Close)

	states := auth.NewStateStore()
	t.Cleanup(states.Close)
	codes := NewCodeStore(CodeStoreOptions{
		TTL:        time.Minute,
		MaxEntries: 10,
	})
	provider := NewGmailProvider(GmailConfig{
		ClientID:     "connect-client-id",
		ClientSecret: "connect-secret",
		BaseURL:      "https://connect.kittypaw.app",
		AuthURL:      fakeOAuth.URL + "/auth",
		TokenURL:     fakeOAuth.URL + "/token",
		UserInfoURL:  fakeOAuth.URL + "/userinfo",
	}, fakeOAuth.Client())
	xProvider := NewXProvider(XConfig{
		ClientID:     "x-client-id",
		ClientSecret: "x-secret",
		BaseURL:      "https://connect.kittypaw.app",
		AuthURL:      fakeOAuth.URL + "/auth",
		TokenURL:     fakeOAuth.URL + "/token",
		UserInfoURL:  fakeOAuth.URL + "/users/me",
	}, fakeOAuth.Client())
	return NewHandler(provider, xProvider, states, codes), states, codes, fakeOAuth
}

type fakeEntitlementChecker struct {
	allowed bool
	err     error
}

func (c fakeEntitlementChecker) UserAllowed(context.Context, string, string) (bool, error) {
	if c.err != nil {
		return false, c.err
	}
	return c.allowed, nil
}

type fakeQuotaEntitlementChecker struct {
	allowed bool
	quota   map[string]any
	err     error
}

func (c fakeQuotaEntitlementChecker) UserAllowed(context.Context, string, string) (bool, error) {
	if c.err != nil {
		return false, c.err
	}
	return c.allowed, nil
}

func (c fakeQuotaEntitlementChecker) UserQuotaJSON(context.Context, string, string) (map[string]any, error) {
	if c.err != nil {
		return nil, c.err
	}
	return c.quota, nil
}

func TestHandlerGmailLoginRedirectsToGoogle(t *testing.T) {
	h, _, _, fakeGoogle := testHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/connect/gmail/login?mode=http&port=12345", nil)
	w := httptest.NewRecorder()
	h.HandleGmailLogin()(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body=%s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, fakeGoogle.URL+"/auth?") {
		t.Fatalf("Location = %q", loc)
	}
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if u.Query().Get("state") == "" {
		t.Fatalf("state missing: %s", loc)
	}
}

func TestHandlerGmailLoginRejectsInvalidModeAndPort(t *testing.T) {
	h, _, _, _ := testHandler(t)
	for _, rawURL := range []string{
		"/connect/gmail/login?mode=bad",
		"/connect/gmail/login?mode=http&port=abc",
		"/connect/gmail/login?mode=http&port=80",
	} {
		t.Run(rawURL, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, rawURL, nil)
			w := httptest.NewRecorder()
			h.HandleGmailLogin()(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", w.Code)
			}
		})
	}
}

func TestHandlerGmailCallbackReturnsOnlyOneTimeCode(t *testing.T) {
	h, states, _, _ := testHandler(t)
	state, err := states.CreateWithMeta("verifier-1", map[string]string{"mode": "http", "port": "12345", "provider": GmailProviderID})
	if err != nil {
		t.Fatalf("CreateWithMeta: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/connect/gmail/callback?code=google-code&state="+url.QueryEscape(state), nil)
	w := httptest.NewRecorder()
	h.HandleGmailCallback()(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body=%s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if u.Scheme != "http" || u.Host != "127.0.0.1:12345" || u.Path != "/callback" {
		t.Fatalf("Location = %q", loc)
	}
	q := u.Query()
	if q.Get("code") == "" {
		t.Fatalf("one-time code missing: %s", loc)
	}
	if q.Get("access_token") != "" || q.Get("refresh_token") != "" {
		t.Fatalf("tokens leaked in redirect: %s", loc)
	}
}

func TestHandlerCodeModeAndExchangeConsumeOnce(t *testing.T) {
	h, states, _, _ := testHandler(t)
	state, err := states.CreateWithMeta("verifier-1", map[string]string{"mode": "code", "provider": GmailProviderID})
	if err != nil {
		t.Fatalf("CreateWithMeta: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/connect/gmail/callback?code=google-code&state="+url.QueryEscape(state), nil)
	w := httptest.NewRecorder()
	h.HandleGmailCallback()(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("callback status = %d; body=%s", w.Code, w.Body.String())
	}

	displayCode := extractDisplayCode(t, w.Body.String())
	body := []byte(fmt.Sprintf(`{"code":%q}`, displayCode))
	exchange := httptest.NewRecorder()
	h.HandleCLIExchange()(exchange, httptest.NewRequest(http.MethodPost, "/connect/cli/exchange", bytes.NewReader(body)))
	if exchange.Code != http.StatusOK {
		t.Fatalf("exchange status = %d; body=%s", exchange.Code, exchange.Body.String())
	}
	var tokens TokenSet
	if err := json.NewDecoder(exchange.Body).Decode(&tokens); err != nil {
		t.Fatalf("decode exchange: %v", err)
	}
	if tokens.AccessToken != "gmail-access" || tokens.RefreshToken != "gmail-refresh" || tokens.Email != "alice@example.com" {
		t.Fatalf("tokens = %#v", tokens)
	}

	replay := httptest.NewRecorder()
	h.HandleCLIExchange()(replay, httptest.NewRequest(http.MethodPost, "/connect/cli/exchange", bytes.NewReader(body)))
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("replay status = %d, want 401", replay.Code)
	}
}

func TestHandlerRefresh(t *testing.T) {
	h, _, _, _ := testHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/connect/gmail/refresh", strings.NewReader(`{"refresh_token":"gmail-refresh"}`))
	w := httptest.NewRecorder()
	h.HandleGmailRefresh()(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var tokens TokenSet
	if err := json.NewDecoder(w.Body).Decode(&tokens); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if tokens.AccessToken != "gmail-access-2" || tokens.RefreshToken != "" {
		t.Fatalf("tokens = %#v", tokens)
	}
}

func TestXBrokerSearchRecentRecordsUsage(t *testing.T) {
	fakeX := newFakeXAPIServer(t, 2)
	tokenStore := NewMemoryTokenStore(time.Now())
	if err := tokenStore.SaveProviderToken(context.Background(), ProviderTokenRecord{
		UserID:      "user-1",
		ProviderID:  XProviderID,
		AccessToken: "x-access",
		TokenType:   "Bearer",
	}); err != nil {
		t.Fatalf("SaveProviderToken: %v", err)
	}

	h, _, _, _ := testHandler(t)
	h.X = NewXProvider(XConfig{APIBaseURL: fakeX.URL + "/2"}, fakeX.Client())
	h.TokenStore = tokenStore
	h.Entitlements = fakeQuotaEntitlementChecker{
		allowed: true,
		quota:   map[string]any{"monthly_post_reads": float64(5)},
	}
	req := httptest.NewRequest(http.MethodGet, "/connect/x/broker/search/recent?query=kittypaw&limit=10", nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{ID: "user-1", Email: "alice@example.com"}))
	w := httptest.NewRecorder()

	h.HandleXBrokerSearchRecent()(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
	var body XPostsResult
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Posts) != 2 {
		t.Fatalf("posts len = %d, want 2", len(body.Posts))
	}
}

func TestXBrokerSearchRecentBlocksOverMonthlyQuota(t *testing.T) {
	fakeX := newFakeXAPIServer(t, 2)
	tokenStore := NewMemoryTokenStore(time.Now())
	if err := tokenStore.SaveProviderToken(context.Background(), ProviderTokenRecord{
		UserID:      "user-1",
		ProviderID:  XProviderID,
		AccessToken: "x-access",
		TokenType:   "Bearer",
	}); err != nil {
		t.Fatalf("SaveProviderToken: %v", err)
	}

	h, _, _, _ := testHandler(t)
	h.X = NewXProvider(XConfig{APIBaseURL: fakeX.URL + "/2"}, fakeX.Client())
	h.TokenStore = tokenStore
	h.Entitlements = fakeQuotaEntitlementChecker{
		allowed: true,
		quota:   map[string]any{"monthly_post_reads": float64(1)},
	}
	req := httptest.NewRequest(http.MethodGet, "/connect/x/broker/search/recent?query=kittypaw&limit=10", nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{ID: "user-1", Email: "alice@example.com"}))
	w := httptest.NewRecorder()

	h.HandleXBrokerSearchRecent()(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body=%s", w.Code, w.Body.String())
	}
}

func TestXBrokerSearchRecentRequiresAuthenticatedUser(t *testing.T) {
	h, _, _, _ := testHandler(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/connect/x/broker/search/recent?query=kittypaw", nil)

	h.HandleXBrokerSearchRecent()(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", w.Code, w.Body.String())
	}
}

func TestHandlerXSessionRequiresEntitlement(t *testing.T) {
	h, _, _, _ := testHandler(t)
	h.PreauthStore = NewPreauthStore(PreauthStoreOptions{})
	h.Entitlements = fakeEntitlementChecker{allowed: false}

	req := httptest.NewRequest(http.MethodPost, "/connect/x/sessions", strings.NewReader(`{"mode":"code"}`))
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{ID: "user-1"}))
	w := httptest.NewRecorder()
	h.HandleXSession()(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
}

func TestHandlerXSessionReturnsLoginURL(t *testing.T) {
	h, _, _, _ := testHandler(t)
	h.PreauthStore = NewPreauthStore(PreauthStoreOptions{})
	h.Entitlements = fakeEntitlementChecker{allowed: true}

	req := httptest.NewRequest(http.MethodPost, "/connect/x/sessions", strings.NewReader(`{"mode":"http","port":"12345"}`))
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{ID: "user-1"}))
	w := httptest.NewRecorder()
	h.HandleXSession()(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "" {
		t.Fatalf("Location = %q, want no redirect", loc)
	}
	var body struct {
		LoginURL string `json:"login_url"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(body.LoginURL, "https://connect.kittypaw.app/connect/x/login?session=") {
		t.Fatalf("login_url = %q", body.LoginURL)
	}
	assertSensitiveResponseHeaders(t, w.Header())
}

func TestHandlerXSessionFailsClosedOnEntitlementError(t *testing.T) {
	h, _, _, _ := testHandler(t)
	h.PreauthStore = NewPreauthStore(PreauthStoreOptions{})
	h.Entitlements = fakeEntitlementChecker{allowed: true, err: errors.New("boom")}

	req := httptest.NewRequest(http.MethodPost, "/connect/x/sessions", strings.NewReader(`{"mode":"code"}`))
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{ID: "user-1"}))
	w := httptest.NewRecorder()
	h.HandleXSession()(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
}

func TestHandlerXLoginRequiresPreauthSession(t *testing.T) {
	h, _, _, _ := testHandler(t)
	h.PreauthStore = NewPreauthStore(PreauthStoreOptions{})

	login := httptest.NewRecorder()
	h.HandleXLogin()(login, httptest.NewRequest(http.MethodGet, "/connect/x/login?mode=code", nil))
	if login.Code != http.StatusUnauthorized && login.Code != http.StatusForbidden {
		t.Fatalf("login status = %d, want 401 or 403; body=%s", login.Code, login.Body.String())
	}
	if loc := login.Header().Get("Location"); loc != "" {
		t.Fatalf("Location = %q, want no redirect", loc)
	}
}

func TestHandlerXCallbackStoresServerTokenAndReturnsBrokerMarker(t *testing.T) {
	h, states, _, _ := testHandler(t)
	tokenStore := NewMemoryTokenStore(time.Now())
	h.TokenStore = tokenStore
	state, err := states.CreateWithMeta("verifier-1", map[string]string{"mode": "code", "provider": XProviderID, "user_id": "user-1"})
	if err != nil {
		t.Fatalf("CreateWithMeta: %v", err)
	}

	callback := httptest.NewRecorder()
	h.HandleXCallback()(callback, httptest.NewRequest(http.MethodGet, "/connect/x/callback?code=x-code&state="+url.QueryEscape(state), nil))
	if callback.Code != http.StatusOK {
		t.Fatalf("callback status = %d; body=%s", callback.Code, callback.Body.String())
	}

	stored, err := tokenStore.LoadProviderToken(context.Background(), "user-1", XProviderID)
	if err != nil {
		t.Fatalf("LoadProviderToken: %v", err)
	}
	if stored.AccessToken != "x-access" || stored.RefreshToken != "x-refresh" || stored.Username != "jaypark" {
		t.Fatalf("stored token = %#v", stored)
	}

	displayCode := extractDisplayCode(t, callback.Body.String())
	exchange := httptest.NewRecorder()
	h.HandleCLIExchange()(exchange, httptest.NewRequest(http.MethodPost, "/connect/cli/exchange", strings.NewReader(fmt.Sprintf(`{"code":%q}`, displayCode))))
	if exchange.Code != http.StatusOK {
		t.Fatalf("exchange status = %d; body=%s", exchange.Code, exchange.Body.String())
	}
	var tokens TokenSet
	if err := json.NewDecoder(exchange.Body).Decode(&tokens); err != nil {
		t.Fatalf("decode exchange: %v", err)
	}
	if tokens.Provider != XProviderID || tokens.TokenType != "broker" || tokens.Username != "jaypark" {
		t.Fatalf("tokens = %#v, want x broker marker", tokens)
	}
	if tokens.AccessToken != "" || tokens.RefreshToken != "" {
		t.Fatalf("exchange leaked X token: %#v", tokens)
	}
}

func TestHandlerXLoginCallbackAndRefresh(t *testing.T) {
	h, states, _, fakeOAuth := testHandler(t)
	h.PreauthStore = NewPreauthStore(PreauthStoreOptions{})
	session, err := h.PreauthStore.Create(PreauthSession{
		UserID:   "user-1",
		Provider: XProviderID,
		Mode:     "code",
	})
	if err != nil {
		t.Fatalf("Create preauth: %v", err)
	}

	login := httptest.NewRecorder()
	h.HandleXLogin()(login, httptest.NewRequest(http.MethodGet, "/connect/x/login?session="+url.QueryEscape(session), nil))
	if login.Code != http.StatusFound {
		t.Fatalf("login status = %d; body=%s", login.Code, login.Body.String())
	}
	if loc := login.Header().Get("Location"); !strings.HasPrefix(loc, fakeOAuth.URL+"/auth?") {
		t.Fatalf("login Location = %q", loc)
	}
	replay := httptest.NewRecorder()
	h.HandleXLogin()(replay, httptest.NewRequest(http.MethodGet, "/connect/x/login?session="+url.QueryEscape(session), nil))
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("replay status = %d, want 401; body=%s", replay.Code, replay.Body.String())
	}

	state, err := states.CreateWithMeta("verifier-1", map[string]string{"mode": "code", "provider": XProviderID})
	if err != nil {
		t.Fatalf("CreateWithMeta: %v", err)
	}
	callback := httptest.NewRecorder()
	h.HandleXCallback()(callback, httptest.NewRequest(http.MethodGet, "/connect/x/callback?code=x-code&state="+url.QueryEscape(state), nil))
	if callback.Code != http.StatusOK {
		t.Fatalf("callback status = %d; body=%s", callback.Code, callback.Body.String())
	}
	displayCode := extractDisplayCode(t, callback.Body.String())

	exchange := httptest.NewRecorder()
	h.HandleCLIExchange()(exchange, httptest.NewRequest(http.MethodPost, "/connect/cli/exchange", strings.NewReader(fmt.Sprintf(`{"code":%q}`, displayCode))))
	if exchange.Code != http.StatusOK {
		t.Fatalf("exchange status = %d; body=%s", exchange.Code, exchange.Body.String())
	}
	var tokens TokenSet
	if err := json.NewDecoder(exchange.Body).Decode(&tokens); err != nil {
		t.Fatalf("decode exchange: %v", err)
	}
	if tokens.Provider != "x" || tokens.AccessToken != "x-access" || tokens.RefreshToken != "x-refresh" || tokens.Username != "jaypark" {
		t.Fatalf("tokens = %#v", tokens)
	}

	refresh := httptest.NewRecorder()
	h.HandleXRefresh()(refresh, httptest.NewRequest(http.MethodPost, "/connect/x/refresh", strings.NewReader(`{"refresh_token":"x-refresh"}`)))
	if refresh.Code != http.StatusOK {
		t.Fatalf("refresh status = %d; body=%s", refresh.Code, refresh.Body.String())
	}
	tokens = TokenSet{}
	if err := json.NewDecoder(refresh.Body).Decode(&tokens); err != nil {
		t.Fatalf("decode refresh: %v", err)
	}
	if tokens.Provider != "x" || tokens.AccessToken != "x-access-2" || tokens.RefreshToken != "x-refresh-2" {
		t.Fatalf("refreshed = %#v", tokens)
	}
}

func TestHandlerXCallbackRejectsGmailStateWithoutTokenExchange(t *testing.T) {
	var xTokenCalls int
	fakeOAuth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			xTokenCalls++
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"access_token":"x-access","refresh_token":"x-refresh","token_type":"bearer","expires_in":7200,"scope":"`+XReadOnlyScope+`"}`)
		case "/users/me":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":{"id":"123","name":"Jay Park","username":"jaypark"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fakeOAuth.Close)

	states := auth.NewStateStore()
	t.Cleanup(states.Close)
	h := NewHandler(nil, NewXProvider(XConfig{
		ClientID:     "x-client-id",
		ClientSecret: "x-secret",
		BaseURL:      "https://connect.kittypaw.app",
		AuthURL:      fakeOAuth.URL + "/auth",
		TokenURL:     fakeOAuth.URL + "/token",
		UserInfoURL:  fakeOAuth.URL + "/users/me",
	}, fakeOAuth.Client()), states, NewCodeStore(CodeStoreOptions{}))
	state, err := states.CreateWithMeta("verifier-1", map[string]string{"mode": "code", "provider": GmailProviderID})
	if err != nil {
		t.Fatalf("CreateWithMeta: %v", err)
	}

	callback := httptest.NewRecorder()
	h.HandleXCallback()(callback, httptest.NewRequest(http.MethodGet, "/connect/x/callback?code=x-code&state="+url.QueryEscape(state), nil))

	if callback.Code != http.StatusBadRequest {
		t.Fatalf("callback status = %d, want 400; body=%s", callback.Code, callback.Body.String())
	}
	if xTokenCalls != 0 {
		t.Fatalf("x token calls = %d, want 0", xTokenCalls)
	}
}

func assertSensitiveResponseHeaders(t *testing.T, header http.Header) {
	t.Helper()
	if got := header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := header.Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q, want no-referrer", got)
	}
}

func extractDisplayCode(t *testing.T, body string) string {
	t.Helper()
	const marker = `data-code="`
	start := strings.Index(body, marker)
	if start < 0 {
		t.Fatalf("body missing data-code marker: %s", body)
	}
	start += len(marker)
	end := strings.Index(body[start:], `"`)
	if end < 0 {
		t.Fatalf("body missing data-code terminator: %s", body)
	}
	return body[start : start+end]
}

func newFakeXAPIServer(t *testing.T, postCount int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer x-access" {
			t.Errorf("Authorization = %q, want Bearer x-access", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/2/tweets/search/recent", "/2/users/u1/tweets":
			posts := make([]string, 0, postCount)
			for i := 0; i < postCount; i++ {
				posts = append(posts, fmt.Sprintf(`{"id":"p%d","text":"post %d","author_id":"u1"}`, i+1, i+1))
			}
			fmt.Fprintf(w, `{"data":[%s],"includes":{"users":[{"id":"u1","username":"jaypark","name":"Jay Park"}]}}`, strings.Join(posts, ","))
		case "/2/users/by/username/jaypark":
			fmt.Fprint(w, `{"data":{"id":"u1","username":"jaypark","name":"Jay Park"}}`)
		case "/2/tweets/p1":
			fmt.Fprint(w, `{"data":{"id":"p1","text":"post 1","author_id":"u1"},"includes":{"users":[{"id":"u1","username":"jaypark","name":"Jay Park"}]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}
