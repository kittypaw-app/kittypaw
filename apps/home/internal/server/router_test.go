package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

func TestChatRouteServesHomeChatHTML(t *testing.T) {
	r := NewRouter(Config{})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/chat/", nil)

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "home-chat-root") {
		t.Fatalf("home chat marker missing from body:\n%s", w.Body.String())
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
