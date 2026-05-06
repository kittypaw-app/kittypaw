package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestAdminGoogleLoginCapsReturnToBeforeStateStore(t *testing.T) {
	states := NewStateStore()
	t.Cleanup(states.Close)

	h := &OAuthHandler{
		StateStore:    states,
		GoogleAuthURL: "https://google.example/auth",
	}
	handler := h.HandleAdminGoogleLogin(GoogleConfig{
		ClientID:    "client-id",
		RedirectURL: "https://portal.kittypaw.app/auth/google/callback",
	})

	longReturnTo := "/admin/" + strings.Repeat("x", maxAdminReturnToLen)
	req := httptest.NewRequest(http.MethodGet, "/admin/login?return_to="+url.QueryEscape(longReturnTo), nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body=%s", w.Code, w.Body.String())
	}
	redirect, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	state := redirect.Query().Get("state")
	if state == "" {
		t.Fatal("Location missing state")
	}

	states.mu.Lock()
	entry, ok := states.entries[state]
	states.mu.Unlock()
	if !ok {
		t.Fatalf("state %q was not stored", state)
	}
	if got := entry.metadata[stateMetaKeyAdminReturnTo]; got != defaultAdminReturnTo {
		t.Fatalf("stored admin return_to length=%d value prefix=%q, want default %q", len(got), got[:min(len(got), 32)], defaultAdminReturnTo)
	}
}

func TestSanitizeAdminReturnToAllowsBoundedAdminPath(t *testing.T) {
	got := sanitizeAdminReturnTo("/admin/connect/users")
	if got != "/admin/connect/users" {
		t.Fatalf("sanitizeAdminReturnTo = %q, want admin path", got)
	}
}
