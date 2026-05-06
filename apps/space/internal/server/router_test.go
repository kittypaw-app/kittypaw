package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestHealthReturnsBuildIdentity(t *testing.T) {
	r := NewRouter(Config{Version: "v1", Commit: "abc123"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "healthy" || body["version"] != "v1" || body["commit"] != "abc123" {
		t.Fatalf("body = %#v", body)
	}
}

func TestChatRouteServesSpaceChatHTML(t *testing.T) {
	r := NewRouter(Config{})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/chat/", nil)

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "space-chat-root") {
		t.Fatalf("space chat marker missing from body:\n%s", w.Body.String())
	}
	for _, want := range []string{`id="deviceSelect"`, `id="composer"`, `/assets/shared.js`, `/assets/chat.js`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("space chat app HTML missing %q:\n%s", want, w.Body.String())
		}
	}
}

func TestChatRouteRedirectsToSlash(t *testing.T) {
	r := NewRouter(Config{})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/chat", nil)

	r.ServeHTTP(w, req)

	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want 301", w.Code)
	}
	if got := w.Header().Get("Location"); got != "/chat/" {
		t.Fatalf("Location = %q, want /chat/", got)
	}
}

type fakeWebHandler struct{}

func (fakeWebHandler) MountRoutes(r chi.Router) {
	r.Get("/chat/api/session", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
}

func TestRouterMountsChatBFFRoutesBeforeStaticRoutes(t *testing.T) {
	r := NewRouter(Config{WebHandler: fakeWebHandler{}})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/chat/api/session", nil)

	r.ServeHTTP(w, req)

	if w.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want 418", w.Code)
	}
}

func TestChatScriptUsesChatBFFRoutes(t *testing.T) {
	r := NewRouter(Config{})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/assets/chat.js", nil)

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "/chat/api/routes") {
		t.Fatalf("chat.js does not call Space chat BFF routes:\n%s", body)
	}
	if strings.Contains(body, "/app/api/") {
		t.Fatalf("chat.js still references legacy app API path:\n%s", body)
	}
}
