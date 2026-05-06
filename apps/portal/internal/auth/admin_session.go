package auth

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kittypaw-app/kittyportal/internal/model"
)

const (
	AdminSessionCookieName = "kp_portal_admin_session"
	AdminSessionTTL        = 8 * time.Hour

	stateMetaModeAdmin        = "admin"
	stateMetaKeyAdminReturnTo = "admin_return_to"
	defaultAdminReturnTo      = "/admin/connect"

	// maxAdminReturnToLen bounds the public /admin/login return target before
	// storing it in OAuth state metadata. Legitimate admin paths are short; 1KiB
	// is intentionally aligned with the web OAuth state cap.
	maxAdminReturnToLen = 1024
)

// HandleAdminGoogleLogin starts a browser session flow for portal operators.
// The callback reuses the standard Google callback route and switches into
// admin-session handling via state metadata.
func (h *OAuthHandler) HandleAdminGoogleLogin(cfg GoogleConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		verifier, err := GenerateVerifier()
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		meta := map[string]string{
			stateMetaKeyMode:          stateMetaModeAdmin,
			stateMetaKeyAdminReturnTo: sanitizeAdminReturnTo(r.URL.Query().Get("return_to")),
		}
		state, err := h.StateStore.CreateWithMeta(verifier, meta)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		params := url.Values{
			"client_id":             {cfg.ClientID},
			"redirect_uri":          {cfg.RedirectURL},
			"response_type":         {"code"},
			"scope":                 {"openid email profile"},
			"state":                 {state},
			"code_challenge":        {ChallengeS256(verifier)},
			"code_challenge_method": {"S256"},
			"access_type":           {"offline"},
		}
		http.Redirect(w, r, h.googleAuthURL()+"?"+params.Encode(), http.StatusFound)
	}
}

func (h *OAuthHandler) emitAdminCallback(w http.ResponseWriter, r *http.Request, user *model.User, meta map[string]string) {
	token, err := SignForAudiences(user.ID, []string{AudiencePortalAdmin}, []string{ScopePortalAdmin}, h.JWTPrivateKey, h.JWTKID, AdminSessionTTL)
	if err != nil {
		slog.Error("admin callback: session token sign failed", "user_id", user.ID)
		http.Error(w, "token generation failed", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     AdminSessionCookieName,
		Value:    token,
		Path:     "/admin",
		MaxAge:   int(AdminSessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   h.AdminSessionCookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, sanitizeAdminReturnTo(meta[stateMetaKeyAdminReturnTo]), http.StatusFound)
}

func sanitizeAdminReturnTo(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultAdminReturnTo
	}
	if len(raw) > maxAdminReturnToLen {
		return defaultAdminReturnTo
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" {
		return defaultAdminReturnTo
	}
	if raw == "/admin" || strings.HasPrefix(raw, "/admin/") {
		return raw
	}
	return defaultAdminReturnTo
}
