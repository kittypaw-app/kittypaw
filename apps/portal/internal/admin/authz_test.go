package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kittypaw-app/kittyportal/internal/auth"
	"github.com/kittypaw-app/kittyportal/internal/model"
)

func TestMiddlewareRequiresAuthenticatedUser(t *testing.T) {
	mw := Middleware([]string{"admin@example.com"})
	req := httptest.NewRequest(http.MethodGet, "/admin/connect", nil)
	w := httptest.NewRecorder()

	mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not run")
	})).ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestMiddlewareRejectsNonAdminEmail(t *testing.T) {
	mw := Middleware([]string{"admin@example.com"})
	req := httptest.NewRequest(http.MethodGet, "/admin/connect", nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{
		ID:    "user-1",
		Email: "user@example.com",
	}))
	w := httptest.NewRecorder()

	mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not run")
	})).ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestMiddlewareAllowsConfiguredAdminEmailCaseInsensitive(t *testing.T) {
	mw := Middleware([]string{"admin@example.com"})
	req := httptest.NewRequest(http.MethodGet, "/admin/connect", nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{
		ID:    "user-1",
		Email: "ADMIN@example.com",
	}))
	w := httptest.NewRecorder()

	mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
}
