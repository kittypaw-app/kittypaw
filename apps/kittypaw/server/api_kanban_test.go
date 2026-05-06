package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
)

func TestKanbanAPIRequiresAuthAndRegistersProjectRoutes(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Server.APIKey = "api-key"
	srv := newServerWithLocalUserAndConfig(t, "alice", "pw", &cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("projects without auth code = %d, want 401", rr.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	req.Header.Set("x-api-key", "api-key")
	rr = httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("projects with auth code = %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"projects":[]`) {
		t.Fatalf("projects body = %s, want empty projects envelope", rr.Body.String())
	}
}
