package admin

import (
	"net/http"
	"strings"

	"github.com/kittypaw-app/kittyportal/internal/auth"
	"github.com/kittypaw-app/kittyportal/internal/model"
)

func AuthMiddleware(jwks auth.JWKSProvider, users model.UserStore) func(http.Handler) http.Handler {
	bearer := auth.Middleware(jwks, auth.AudienceAPI, users)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "" {
				bearer(next).ServeHTTP(w, r)
				return
			}

			cookie, err := r.Cookie(auth.AdminSessionCookieName)
			if err != nil || strings.TrimSpace(cookie.Value) == "" {
				next.ServeHTTP(w, r)
				return
			}

			claims, err := auth.Verify(cookie.Value, jwks, auth.AudiencePortalAdmin)
			if err != nil {
				clearAdminSessionCookie(w)
				http.Error(w, "invalid admin session", http.StatusUnauthorized)
				return
			}
			user, err := users.FindByID(r.Context(), claims.UserID)
			if err != nil {
				clearAdminSessionCookie(w)
				http.Error(w, "invalid admin session", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r.WithContext(auth.ContextWithUser(r.Context(), user)))
		})
	}
}

func clearAdminSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.AdminSessionCookieName,
		Value:    "",
		Path:     "/admin",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func Middleware(adminEmails []string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(adminEmails))
	for _, email := range adminEmails {
		if normalized := normalizeEmail(email); normalized != "" {
			allowed[normalized] = struct{}{}
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := auth.UserFromContext(r.Context())
			if user == nil {
				http.Error(w, "authentication required", http.StatusUnauthorized)
				return
			}
			if _, ok := allowed[normalizeEmail(user.Email)]; !ok {
				http.Error(w, "admin access required", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
