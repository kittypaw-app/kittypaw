package connect

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kittypaw-app/kittyportal/internal/auth"
)

const maxConnectBodyBytes = 1024

type Handler struct {
	Gmail        *GmailProvider
	X            *XProvider
	StateStore   *auth.StateStore
	CodeStore    *CodeStore
	PreauthStore *PreauthStore
	TokenStore   BrokerTokenStore
	Entitlements EntitlementChecker
}

type EntitlementChecker interface {
	UserAllowed(context.Context, string, string) (bool, error)
}

func NewHandler(gmail *GmailProvider, x *XProvider, states *auth.StateStore, codes *CodeStore) *Handler {
	return &Handler{Gmail: gmail, X: x, StateStore: states, CodeStore: codes}
}

func (h *Handler) HandleGmailLogin() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mode, port, ok := parseLoginMode(w, r)
		if !ok {
			return
		}
		h.startOAuthLogin(w, r, GmailProviderID, mode, port, "", h.Gmail.AuthURL)
	}
}

func (h *Handler) HandleXLogin() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.PreauthStore == nil {
			http.Error(w, "preauth store unavailable", http.StatusForbidden)
			return
		}
		code := r.URL.Query().Get("session")
		if code == "" {
			http.Error(w, "session required", http.StatusUnauthorized)
			return
		}
		session, err := h.PreauthStore.Consume(code)
		if err != nil || session.Provider != XProviderID {
			http.Error(w, "invalid or expired session", http.StatusUnauthorized)
			return
		}
		if h.X == nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		h.startOAuthLogin(w, r, XProviderID, session.Mode, session.Port, session.UserID, h.X.AuthURL)
	}
}

func (h *Handler) HandleXSession() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setSensitiveResponseHeaders(w.Header())
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		user := auth.UserFromContext(r.Context())
		if user == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxConnectBodyBytes)
		var req struct {
			Mode string `json:"mode"`
			Port string `json:"port"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}

		port, ok := normalizeModePort(w, req.Mode, req.Port)
		if !ok {
			return
		}
		if h.Entitlements == nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		allowed, err := h.Entitlements.UserAllowed(r.Context(), user.ID, XProviderID)
		if err != nil || !allowed {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if h.PreauthStore == nil || h.X == nil || h.X.cfg.BaseURL == "" {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		code, err := h.PreauthStore.Create(PreauthSession{
			UserID:   user.ID,
			Provider: XProviderID,
			Mode:     req.Mode,
			Port:     port,
		})
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"login_url": strings.TrimRight(h.X.cfg.BaseURL, "/") + "/connect/x/login?session=" + url.QueryEscape(code),
		})
	}
}

func parseLoginMode(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	mode := r.URL.Query().Get("mode")
	port, ok := normalizeModePort(w, mode, r.URL.Query().Get("port"))
	return mode, port, ok
}

func normalizeModePort(w http.ResponseWriter, mode, port string) (string, bool) {
	if mode != "http" && mode != "code" {
		http.Error(w, "mode must be 'http' or 'code'", http.StatusBadRequest)
		return "", false
	}
	if mode != "http" {
		return "", true
	}
	portNum, err := strconv.Atoi(port)
	if err != nil || portNum < 1024 || portNum > 65535 {
		http.Error(w, "port must be a number between 1024 and 65535", http.StatusBadRequest)
		return "", false
	}
	return strconv.Itoa(portNum), true
}

func (h *Handler) startOAuthLogin(w http.ResponseWriter, r *http.Request, provider, mode, port, userID string, authURL func(state, verifier string) string) {
	meta := map[string]string{"mode": mode, "provider": provider}
	if mode == "http" {
		meta["port"] = port
	}
	if userID != "" {
		meta["user_id"] = userID
	}

	verifier, err := auth.GenerateVerifier()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	state, err := h.StateStore.CreateWithMeta(verifier, meta)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, authURL(state, verifier), http.StatusFound)
}

func (h *Handler) HandleGmailCallback() http.HandlerFunc {
	return h.handleCallback("gmail", h.Gmail.ExchangeCode)
}

func (h *Handler) HandleXCallback() http.HandlerFunc {
	return h.handleCallback("x", h.X.ExchangeCode)
}

func (h *Handler) handleCallback(provider string, exchange func(context.Context, string, string) (TokenSet, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		state := r.URL.Query().Get("state")
		if code == "" || state == "" {
			http.Error(w, "missing code or state", http.StatusBadRequest)
			return
		}

		verifier, meta, err := h.StateStore.ConsumeMeta(state)
		if err != nil {
			http.Error(w, "invalid state", http.StatusBadRequest)
			return
		}
		if meta["provider"] != provider {
			http.Error(w, "invalid state provider", http.StatusBadRequest)
			return
		}
		tokens, err := exchange(r.Context(), code, verifier)
		if err != nil {
			slog.Error("connect code exchange failed", "provider", provider, "err", err)
			http.Error(w, "authentication failed", http.StatusBadGateway)
			return
		}
		if provider == XProviderID && h.TokenStore != nil {
			expiresAt := tokenExpiresAt(tokens)
			if err := h.TokenStore.SaveProviderToken(r.Context(), ProviderTokenRecord{
				UserID:       meta["user_id"],
				ProviderID:   XProviderID,
				AccessToken:  tokens.AccessToken,
				RefreshToken: tokens.RefreshToken,
				TokenType:    tokens.TokenType,
				Scope:        tokens.Scope,
				Username:     tokens.Username,
				ExpiresAt:    expiresAt,
			}); err != nil {
				slog.Error("connect token store failed", "provider", provider, "err", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			tokens.AccessToken = ""
			tokens.RefreshToken = ""
			tokens.TokenType = "broker"
		}
		displayCode, err := h.CodeStore.Create(tokens)
		if err != nil {
			slog.Error("connect code create failed", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		switch meta["mode"] {
		case "http":
			port := meta["port"]
			redirectURL := fmt.Sprintf("http://127.0.0.1:%s/callback?code=%s", port, url.QueryEscape(displayCode))
			http.Redirect(w, r, redirectURL, http.StatusFound)
		case "code":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'; style-src 'unsafe-inline'")
			w.Header().Set("Referrer-Policy", "no-referrer")
			_, _ = fmt.Fprint(w, connectCodePage(displayCode))
		default:
			http.Error(w, "invalid mode in state", http.StatusBadRequest)
		}
	}
}

func tokenExpiresAt(tokens TokenSet) *time.Time {
	if tokens.ExpiresIn <= 0 {
		return nil
	}
	issuedAt := tokens.IssuedAt
	if issuedAt.IsZero() {
		issuedAt = time.Now().UTC()
	}
	expiresAt := issuedAt.Add(time.Duration(tokens.ExpiresIn) * time.Second).UTC()
	return &expiresAt
}

func (h *Handler) HandleCLIExchange() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxConnectBodyBytes)
		var req struct {
			Code string `json:"code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Code == "" {
			http.Error(w, "code required", http.StatusBadRequest)
			return
		}
		tokens, err := h.CodeStore.Consume(req.Code)
		if err != nil {
			http.Error(w, "invalid or expired code", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tokens)
	}
}

func (h *Handler) HandleGmailRefresh() http.HandlerFunc {
	return h.handleRefresh("gmail", h.Gmail.Refresh)
}

func (h *Handler) HandleXRefresh() http.HandlerFunc {
	return h.handleRefresh("x", h.X.Refresh)
}

func (h *Handler) handleRefresh(provider string, refresh func(context.Context, string) (TokenSet, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxConnectBodyBytes)
		var req struct {
			RefreshToken string `json:"refresh_token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RefreshToken == "" {
			http.Error(w, "refresh_token required", http.StatusBadRequest)
			return
		}
		tokens, err := refresh(r.Context(), req.RefreshToken)
		if err != nil {
			slog.Error("connect refresh failed", "provider", provider, "err", err)
			http.Error(w, "refresh failed", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tokens)
	}
}

func setSensitiveResponseHeaders(header http.Header) {
	header.Set("Cache-Control", "no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "no-referrer")
}

func connectCodePage(code string) string {
	escaped := html.EscapeString(code)
	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>KittyPaw Connect</title>
</head>
<body>
  <main>
    <p>KittyPaw Connect</p>
    <code data-code="%s">%s</code>
  </main>
</body>
</html>`, escaped, escaped)
}
