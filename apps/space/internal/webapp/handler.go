package webapp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kittypaw-app/kittyspace/internal/identity"
)

const (
	sessionCookieName  = "kittyspace_session"
	loginStateTTL      = 10 * time.Minute
	sessionTTL         = 30 * 24 * time.Hour
	refreshSkew        = 30 * time.Second
	defaultAccessTTL   = 15 * time.Minute
	maxProxyBodyBytes  = 1 << 20
	codeChallengeS256  = "S256"
	defaultTokenType   = "Bearer"
	defaultRedirectURI = "/auth/callback"
	defaultPublicBase  = "https://space.kittypaw.app"
	defaultAPIAuthBase = "https://portal.kittypaw.app/auth"
)

type Config struct {
	PublicBaseURL  string
	APIAuthBaseURL string
	Verifier       identity.CredentialVerifier
	OpenAIHandler  http.Handler
	HTTPClient     *http.Client
	Now            func() time.Time
	Rand           io.Reader
}

type Handler struct {
	publicBaseURL  string
	apiAuthBaseURL string
	redirectURI    string
	secureCookie   bool
	verifier       identity.CredentialVerifier
	openAIHandler  http.Handler
	httpClient     *http.Client
	now            func() time.Time
	rand           io.Reader
	logins         *loginStore
	sessions       *sessionStore
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

func New(cfg Config) (*Handler, error) {
	if cfg.PublicBaseURL == "" {
		cfg.PublicBaseURL = defaultPublicBase
	}
	if cfg.APIAuthBaseURL == "" {
		cfg.APIAuthBaseURL = defaultAPIAuthBase
	}
	if cfg.Verifier == nil {
		return nil, fmt.Errorf("credential verifier is required")
	}
	if cfg.OpenAIHandler == nil {
		return nil, fmt.Errorf("openai handler is required")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Rand == nil {
		cfg.Rand = rand.Reader
	}
	publicBase := strings.TrimRight(cfg.PublicBaseURL, "/")
	apiAuthBase := strings.TrimRight(cfg.APIAuthBaseURL, "/")
	if _, err := url.ParseRequestURI(publicBase); err != nil {
		return nil, fmt.Errorf("public base url: %w", err)
	}
	if _, err := url.ParseRequestURI(apiAuthBase); err != nil {
		return nil, fmt.Errorf("api auth base url: %w", err)
	}
	return &Handler{
		publicBaseURL:  publicBase,
		apiAuthBaseURL: apiAuthBase,
		redirectURI:    publicBase + defaultRedirectURI,
		secureCookie:   strings.HasPrefix(publicBase, "https://"),
		verifier:       cfg.Verifier,
		openAIHandler:  cfg.OpenAIHandler,
		httpClient:     cfg.HTTPClient,
		now:            cfg.Now,
		rand:           cfg.Rand,
		logins:         newLoginStore(cfg.Rand, cfg.Now),
		sessions:       newSessionStore(cfg.Rand, cfg.Now),
	}, nil
}

func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	h.MountRoutes(r)
	return r
}

func (h *Handler) MountRoutes(r chi.Router) {
	r.Get("/auth/login/google", h.handleLoginGoogle)
	r.Get("/auth/callback", h.handleCallback)
	r.Post("/auth/logout", h.handleLogout)
	r.Get("/chat/api/session", h.handleSession)
	r.Get("/chat/api/routes", h.handleAppAPI)
	r.Get("/chat/api/nodes/*", h.handleAppAPI)
	r.Post("/chat/api/nodes/*", h.handleAppAPI)
	r.Get("/kanban/api/session", h.handleSession)
	r.Get("/kanban/api/routes", h.handleAppAPI)
	r.Get("/kanban/api/nodes/*", h.handleAppAPI)
	r.Post("/kanban/api/nodes/*", h.handleAppAPI)
	r.Patch("/kanban/api/nodes/*", h.handleAppAPI)
}

func (h *Handler) handleLoginGoogle(w http.ResponseWriter, r *http.Request) {
	verifier, err := randomURLToken(h.rand)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "login unavailable")
		return
	}
	state, err := h.logins.Create(pendingLogin{
		CodeVerifier: verifier,
		RedirectURI:  h.redirectURI,
		ExpiresAt:    h.now().Add(loginStateTTL),
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "login unavailable")
		return
	}

	u, err := url.Parse(h.apiAuthBaseURL + "/web/google")
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "login unavailable")
		return
	}
	q := u.Query()
	q.Set("redirect_uri", h.redirectURI)
	q.Set("state", state)
	q.Set("code_challenge", codeChallenge(verifier))
	q.Set("code_challenge_method", codeChallengeS256)
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func (h *Handler) handleCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		writeHTMLMessage(w, http.StatusBadRequest, "Login callback is missing code or state.")
		return
	}
	login, ok := h.logins.Consume(state)
	if !ok {
		writeHTMLMessage(w, http.StatusUnauthorized, "Login session expired. Please start again.")
		return
	}
	token, err := h.exchangeCode(r.Context(), code, login)
	if err != nil {
		writeHTMLMessage(w, http.StatusBadGateway, "Login exchange failed. Please try again.")
		return
	}
	if err := h.verifyAccessToken(r.Context(), token.AccessToken); err != nil {
		writeHTMLMessage(w, http.StatusUnauthorized, "Login token was rejected.")
		return
	}
	sessionID, session, err := h.createSession(token)
	if err != nil {
		writeHTMLMessage(w, http.StatusInternalServerError, "Login session could not be created.")
		return
	}
	h.setSessionCookie(w, sessionID, session)
	http.Redirect(w, r, "/chat/", http.StatusFound)
}

func (h *Handler) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		h.sessions.Delete(cookie.Value)
	}
	http.SetCookie(w, h.clearSessionCookie())
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleSession(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := h.sessionFromRequest(r); !ok {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"authenticated":true}` + "\n"))
}

func (h *Handler) handleAppAPI(w http.ResponseWriter, r *http.Request) {
	sessionID, session, ok := h.sessionFromRequest(r)
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	targetPath, ok := appAPIPath(r.URL.Path)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	body, err := proxyBody(w, r)
	if err != nil {
		writeJSONError(w, http.StatusRequestEntityTooLarge, err.Error())
		return
	}
	if h.shouldRefresh(session) {
		refreshed, err := h.refreshToken(r.Context(), session)
		if err != nil {
			h.sessions.Delete(sessionID)
			http.SetCookie(w, h.clearSessionCookie())
			writeJSONError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		session = refreshed
		h.sessions.Update(sessionID, session)
		h.setSessionCookie(w, sessionID, session)
	}

	rr := h.proxyOpenAI(r, session, targetPath, body)
	status := rr.StatusCode()
	if status != http.StatusUnauthorized {
		rr.FlushTo(w)
		return
	}

	refreshed, err := h.refreshToken(r.Context(), session)
	if err != nil {
		h.sessions.Delete(sessionID)
		http.SetCookie(w, h.clearSessionCookie())
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	h.sessions.Update(sessionID, refreshed)
	h.setSessionCookie(w, sessionID, refreshed)
	rr = h.proxyOpenAI(r, refreshed, targetPath, body)
	rr.FlushTo(w)
}

func (h *Handler) exchangeCode(ctx context.Context, code string, login pendingLogin) (tokenResponse, error) {
	body, err := json.Marshal(map[string]string{
		"code":          code,
		"code_verifier": login.CodeVerifier,
		"redirect_uri":  login.RedirectURI,
	})
	if err != nil {
		return tokenResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.apiAuthBaseURL+"/web/exchange", bytes.NewReader(body))
	if err != nil {
		return tokenResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	return h.doTokenRequest(req)
}

func (h *Handler) refreshToken(ctx context.Context, session tokenSession) (tokenSession, error) {
	if session.RefreshToken == "" {
		return tokenSession{}, errors.New("refresh token missing")
	}
	body, err := json.Marshal(map[string]string{"refresh_token": session.RefreshToken})
	if err != nil {
		return tokenSession{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.apiAuthBaseURL+"/token/refresh", bytes.NewReader(body))
	if err != nil {
		return tokenSession{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	token, err := h.doTokenRequest(req)
	if err != nil {
		return tokenSession{}, err
	}
	if err := h.verifyAccessToken(ctx, token.AccessToken); err != nil {
		return tokenSession{}, err
	}
	return h.sessionFromToken(token), nil
}

func (h *Handler) doTokenRequest(req *http.Request) (tokenResponse, error) {
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return tokenResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxProxyBodyBytes))
	if err != nil {
		return tokenResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return tokenResponse{}, fmt.Errorf("token endpoint status %d", resp.StatusCode)
	}
	var token tokenResponse
	if err := json.Unmarshal(raw, &token); err != nil {
		return tokenResponse{}, err
	}
	if token.AccessToken == "" || token.RefreshToken == "" {
		return tokenResponse{}, errors.New("token response missing access or refresh token")
	}
	return token, nil
}

func (h *Handler) verifyAccessToken(ctx context.Context, token string) error {
	_, err := h.verifier.VerifyAPIClient(ctx, token)
	return err
}

func (h *Handler) createSession(token tokenResponse) (string, tokenSession, error) {
	session := h.sessionFromToken(token)
	id, err := h.sessions.Create(session)
	return id, session, err
}

func (h *Handler) sessionFromToken(token tokenResponse) tokenSession {
	expiresIn := token.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = int(defaultAccessTTL.Seconds())
	}
	tokenType := token.TokenType
	if tokenType == "" {
		tokenType = defaultTokenType
	}
	session := tokenSession{
		AccessToken:      token.AccessToken,
		RefreshToken:     token.RefreshToken,
		TokenType:        tokenType,
		AccessExpiresAt:  h.now().Add(time.Duration(expiresIn) * time.Second),
		SessionExpiresAt: h.now().Add(sessionTTL),
	}
	return session
}

func (h *Handler) sessionFromRequest(r *http.Request) (string, tokenSession, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return "", tokenSession{}, false
	}
	session, ok := h.sessions.Get(cookie.Value)
	return cookie.Value, session, ok
}

func (h *Handler) shouldRefresh(session tokenSession) bool {
	return session.AccessExpiresAt.IsZero() || !session.AccessExpiresAt.After(h.now().Add(refreshSkew))
}

func (h *Handler) setSessionCookie(w http.ResponseWriter, id string, session tokenSession) {
	maxAge := int(session.SessionExpiresAt.Sub(h.now()).Seconds())
	if maxAge < 0 {
		maxAge = 0
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    id,
		Path:     "/",
		Expires:  session.SessionExpiresAt,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   h.secureCookie,
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *Handler) clearSessionCookie() *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.secureCookie,
		SameSite: http.SameSiteLaxMode,
	}
}

func (h *Handler) proxyOpenAI(r *http.Request, session tokenSession, targetPath string, body []byte) *proxyRecorder {
	proxied := r.Clone(r.Context())
	proxied.URL = cloneURL(r.URL)
	proxied.URL.Path = targetPath
	proxied.URL.RawPath = ""
	proxied.RequestURI = ""
	proxied.Header = r.Header.Clone()
	proxied.Header.Del("Cookie")
	proxied.Header.Set("Authorization", session.TokenType+" "+session.AccessToken)
	proxied.Body = io.NopCloser(bytes.NewReader(body))

	rr := newProxyRecorder()
	h.openAIHandler.ServeHTTP(rr, proxied)
	return rr
}

func proxyBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	defer func() { _ = r.Body.Close() }()
	return io.ReadAll(http.MaxBytesReader(w, r.Body, maxProxyBodyBytes))
}

func appAPIPath(path string) (string, bool) {
	switch {
	case path == "/chat/api/routes":
		return "/v1/routes", true
	case strings.HasPrefix(path, "/chat/api/nodes/"):
		return strings.TrimPrefix(path, "/chat/api"), true
	case path == "/kanban/api/routes":
		return "/v1/routes", true
	case strings.HasPrefix(path, "/kanban/api/nodes/"):
		return strings.TrimPrefix(path, "/kanban/api"), true
	default:
		return "", false
	}
}

func cloneURL(src *url.URL) *url.URL {
	out := *src
	return &out
}

func codeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func writeHTMLMessage(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>KittyPaw Space sign-in</title>
  <link rel="stylesheet" href="/assets/style.css?v=20260504-1">
</head>
<body>
  <main class="entry-shell auth-shell">
    <section class="entry-panel auth-message-panel">
      <p class="eyebrow">KittyPaw Space</p>
      <h1>Sign-in needs attention</h1>
      <p class="lede">%s</p>
      <div class="entry-actions">
        <a class="primary-action" href="/">Return to KittyPaw Space</a>
      </div>
    </section>
  </main>
</body>
</html>`, html.EscapeString(message))
}

type proxyRecorder struct {
	header http.Header
	body   bytes.Buffer
	code   int
}

func newProxyRecorder() *proxyRecorder {
	return &proxyRecorder{header: make(http.Header)}
}

func (r *proxyRecorder) Header() http.Header {
	return r.header
}

func (r *proxyRecorder) WriteHeader(status int) {
	if r.code != 0 {
		return
	}
	r.code = status
}

func (r *proxyRecorder) Write(data []byte) (int, error) {
	if r.code == 0 {
		r.code = http.StatusOK
	}
	return r.body.Write(data)
}

func (r *proxyRecorder) StatusCode() int {
	if r.code == 0 {
		return http.StatusOK
	}
	return r.code
}

func (r *proxyRecorder) FlushTo(w http.ResponseWriter) {
	for key, values := range r.header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(r.StatusCode())
	_, _ = r.body.WriteTo(w)
}
