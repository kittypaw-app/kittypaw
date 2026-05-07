package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealth_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	if err := c.Health(); err != nil {
		t.Fatalf("Health() unexpected error: %v", err)
	}
}

func TestHealth_ServerDown(t *testing.T) {
	c := New("http://127.0.0.1:1", "")
	if err := c.Health(); err == nil {
		t.Fatal("Health() expected error for unreachable server")
	}
}

func TestHealth_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"down"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	if err := c.Health(); err == nil {
		t.Fatal("Health() expected error for 500 response")
	}
}

func TestExecutions_WithSkill(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"executions": []any{}})
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	_, err := c.Executions("greeting", 10)
	if err != nil {
		t.Fatalf("Executions() error: %v", err)
	}
	want := "/api/v1/executions?limit=10&skill=greeting"
	if gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
}

func TestExecutions_WithoutSkill(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"executions": []any{}})
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	_, err := c.Executions("", 20)
	if err != nil {
		t.Fatalf("Executions() error: %v", err)
	}
	want := "/api/v1/executions?limit=20"
	if gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
}

func TestStaffList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/staff" {
			t.Errorf("path = %q, want /api/v1/staff", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"staff": []any{}})
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	res, err := c.StaffList()
	if err != nil {
		t.Fatalf("StaffList() error: %v", err)
	}
	if res["staff"] == nil {
		t.Error("expected staff key")
	}
}

func TestStaffActivate(t *testing.T) {
	var gotPath, gotPreset string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.ContentLength > 0 {
			var body struct {
				PresetID string `json:"preset_id"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			gotPreset = body.PresetID
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	// Without preset.
	_, err := c.StaffActivate("coder", "")
	if err != nil {
		t.Fatalf("StaffActivate() error: %v", err)
	}
	if gotPath != "/api/v1/staff/coder/activate" {
		t.Errorf("path = %q, want /api/v1/staff/coder/activate", gotPath)
	}
	// With preset.
	_, err = c.StaffActivate("coder", "professional")
	if err != nil {
		t.Fatalf("StaffActivate(preset) error: %v", err)
	}
	if gotPreset != "professional" {
		t.Errorf("preset = %q, want %q", gotPreset, "professional")
	}
}

func TestTeachApprove(t *testing.T) {
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	_, err := c.TeachApprove("greet", "says hello", "code()", "manual", "")
	if err != nil {
		t.Fatalf("TeachApprove() error: %v", err)
	}
	if gotBody["name"] != "greet" || gotBody["code"] != "code()" {
		t.Errorf("body = %v, want name=greet, code=code()", gotBody)
	}
}

func TestHealth_APIKey(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer srv.Close()

	c := New(srv.URL, "test-key")
	if err := c.Health(); err != nil {
		t.Fatalf("Health() error: %v", err)
	}
	if gotKey != "test-key" {
		t.Errorf("API key = %q, want %q", gotKey, "test-key")
	}
}
