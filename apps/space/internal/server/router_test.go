package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	for _, want := range []string{`id="deviceSelect"`, `id="composer"`, `id="clearChatButton"`, `/assets/shared.js`, `/assets/chat.js`} {
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

func TestKanbanRouteServesSpaceKanbanHTML(t *testing.T) {
	r := NewRouter(Config{})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/kanban/", nil)

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	for _, want := range []string{"space-kanban-root", `id="kanbanRouteStatus"`, `/assets/kanban.js`, `/assets/kanban-page.js`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("space kanban app HTML missing %q:\n%s", want, w.Body.String())
		}
	}
}

func TestKanbanRouteRedirectsToSlash(t *testing.T) {
	r := NewRouter(Config{})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/kanban", nil)

	r.ServeHTTP(w, req)

	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want 301", w.Code)
	}
	if got := w.Header().Get("Location"); got != "/kanban/" {
		t.Fatalf("Location = %q, want /kanban/", got)
	}
}

func TestKittyPawStableMetadataRouteServesConfiguredFile(t *testing.T) {
	dir := t.TempDir()
	stablePath := filepath.Join(dir, "stable.json")
	body := `{"channel":"stable","version":"0.5.9","tag":"kittypaw/v0.5.9","commit":"abc123"}` + "\n"
	if err := os.WriteFile(stablePath, []byte(body), 0o600); err != nil {
		t.Fatalf("write stable metadata: %v", err)
	}
	r := NewRouter(Config{KittyPawStableFile: stablePath})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/downloads/kittypaw/stable.json", nil)

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if w.Body.String() != body {
		t.Fatalf("body = %q, want %q", w.Body.String(), body)
	}
}

func TestKittyPawStableMetadataRoute404WhenMissing(t *testing.T) {
	r := NewRouter(Config{KittyPawStableFile: filepath.Join(t.TempDir(), "stable.json")})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/downloads/kittypaw/stable.json", nil)

	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
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

func TestChatScriptCanClearPersistedMessages(t *testing.T) {
	r := NewRouter(Config{})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/assets/chat.js", nil)

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		"clearChat: document.getElementById(\"clearChatButton\")",
		"function clearChat()",
		"state.messages = []",
		"els.clearChat.addEventListener(\"click\", clearChat)",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("chat.js missing persisted-message clear behavior %q:\n%s", want, body)
		}
	}
}

func TestKanbanPageScriptProxiesThroughKanbanBFFRoutes(t *testing.T) {
	r := NewRouter(Config{})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/assets/kanban-page.js", nil)

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		"/kanban/api/routes",
		"/kanban/api/nodes/",
		"window.KittyPawKanbanAPI",
		"Kanban.mount",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("kanban-page.js missing %q:\n%s", want, body)
		}
	}
}

func TestSharedScriptLabelsRelayErrorSource(t *testing.T) {
	r := NewRouter(Config{})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/assets/shared.js", nil)

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		"X-KittySpace-Relay-Source",
		"daemon/provider",
		"space relay",
		"body.title",
		"body.detail",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("shared.js must include %q for readable relay errors:\n%s", want, body)
		}
	}
}
