package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

	meta, err := core.ReadStaffMetaFile(srv.session.BaseDir, "coder")
	if err != nil {
		t.Fatalf("ReadStaffMetaFile(coder): %v", err)
	}
	if meta.Description != "Writes code" {
		t.Fatalf("staff metadata = %+v", meta)
	}
	if !core.StaffHasSoul(srv.session.BaseDir, "coder") {
		t.Fatal("created staff SOUL.md missing")
	}
}

func TestStaffAPIActivateExistingStaff(t *testing.T) {
	srv := newStaffAPITestServer(t)
	if err := core.WriteStaffMetaFile(srv.session.BaseDir, core.StaffMetaFile{
		ID:          "coder",
		Description: "Writes code",
	}); err != nil {
		t.Fatalf("WriteStaffMetaFile(coder): %v", err)
	}
	if err := os.WriteFile(filepath.Join(srv.session.BaseDir, "staff", "coder", "SOUL.md"), []byte("coder soul"), 0o644); err != nil {
		t.Fatalf("write coder soul: %v", err)
	}

	var activated struct {
		Success bool   `json:"success"`
		ID      string `json:"id"`
	}
	kanbanAPIRequest(t, srv, http.MethodPost, "/api/v1/staff/coder/activate", nil, http.StatusOK, &activated)
	if !activated.Success || activated.ID != "coder" {
		t.Fatalf("activated staff response = %+v", activated)
	}

	if !core.StaffHasSoul(srv.session.BaseDir, "coder") {
		t.Fatal("activated staff SOUL.md missing")
	}
}

func TestOldProfilesAPIRouteIsRemoved(t *testing.T) {
	srv := newStaffAPITestServer(t)

	legacyPath := "/api/v1/profiles"
	legacyBodyKey := `"profiles"`
	rr := kanbanAPIRequest(t, srv, http.MethodGet, legacyPath, nil, http.StatusNotFound, nil)
	if strings.Contains(rr.Body.String(), legacyBodyKey) {
		t.Fatalf("old profiles route body = %s, want removed route", rr.Body.String())
	}
}

func newStaffAPITestServer(t *testing.T) *Server {
	t.Helper()
	cfg := core.DefaultConfig()
	cfg.Server.APIKey = "api-key"
	return newServerWithLocalUserAndConfig(t, "alice", "pw", &cfg)
}
