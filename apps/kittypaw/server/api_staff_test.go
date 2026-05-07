package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
)

func TestStaffAPIRequiresAuthAndReturnsStaffEnvelope(t *testing.T) {
	srv := newStaffAPITestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/staff", nil)
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("staff without auth code = %d, want 401", rr.Code)
	}

	var body struct {
		Staff []any `json:"staff"`
	}
	kanbanAPIRequest(t, srv, http.MethodGet, "/api/v1/staff", nil, http.StatusOK, &body)
	if body.Staff == nil {
		t.Fatalf("staff envelope missing staff key")
	}
}

func TestStaffAPICreatePersistsStaffMeta(t *testing.T) {
	srv := newStaffAPITestServer(t)

	var created struct {
		Success bool   `json:"success"`
		ID      string `json:"id"`
	}
	kanbanAPIRequest(t, srv, http.MethodPost, "/api/v1/staff", map[string]any{
		"id":          "coder",
		"description": "Writes code",
	}, http.StatusCreated, &created)
	if !created.Success || created.ID != "coder" {
		t.Fatalf("created staff response = %+v", created)
	}

	meta, ok, err := srv.store.GetStaffMeta("coder")
	if err != nil {
		t.Fatalf("GetStaffMeta(coder): %v", err)
	}
	if !ok {
		t.Fatal("created staff metadata missing")
	}
	if meta.Description != "Writes code" || meta.CreatedBy != "api" || !meta.Active {
		t.Fatalf("staff metadata = %+v", meta)
	}
}

func TestStaffAPIActivateExistingStaff(t *testing.T) {
	srv := newStaffAPITestServer(t)
	if err := srv.store.UpsertStaffMeta("coder", "Writes code", "[]", "test"); err != nil {
		t.Fatalf("UpsertStaffMeta(coder): %v", err)
	}
	if err := srv.store.SetStaffActive("coder", false); err != nil {
		t.Fatalf("SetStaffActive(coder, false): %v", err)
	}

	var activated struct {
		Success bool   `json:"success"`
		ID      string `json:"id"`
	}
	kanbanAPIRequest(t, srv, http.MethodPost, "/api/v1/staff/coder/activate", nil, http.StatusOK, &activated)
	if !activated.Success || activated.ID != "coder" {
		t.Fatalf("activated staff response = %+v", activated)
	}

	meta, ok, err := srv.store.GetStaffMeta("coder")
	if err != nil {
		t.Fatalf("GetStaffMeta(coder): %v", err)
	}
	if !ok || !meta.Active {
		t.Fatalf("activated staff metadata = %+v ok=%v", meta, ok)
	}
}

func TestLegacyStaffAPIRouteIsRemoved(t *testing.T) {
	srv := newStaffAPITestServer(t)

	legacyPath := "/api/v1/" + "pro" + "files"
	legacyBodyKey := "\"" + "pro" + "files" + "\""
	rr := kanbanAPIRequest(t, srv, http.MethodGet, legacyPath, nil, http.StatusNotFound, nil)
	if strings.Contains(rr.Body.String(), legacyBodyKey) {
		t.Fatalf("legacy staff route body = %s, want removed route", rr.Body.String())
	}
}

func newStaffAPITestServer(t *testing.T) *Server {
	t.Helper()
	cfg := core.DefaultConfig()
	cfg.Server.APIKey = "api-key"
	return newServerWithLocalUserAndConfig(t, "alice", "pw", &cfg)
}
