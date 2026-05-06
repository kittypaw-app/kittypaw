package admin

import (
	"net/http"
	"strings"

	"github.com/kittypaw-app/kittyportal/internal/auth"
)

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
