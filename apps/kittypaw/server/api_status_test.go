package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jinto/kittypaw/core"
)

func TestHandleStatusIncludesSchedulerSnapshots(t *testing.T) {
	root := t.TempDir()
	cfg := core.DefaultConfig()
	cfg.Runtime.MaxConcurrentScheduledJobs = 3
	deps := buildAccountDeps(t, root, "alice", &cfg)
	srv := New([]*AccountDeps{deps}, "test", "alice")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	rec := httptest.NewRecorder()
	srv.handleStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var body struct {
		Scheduler struct {
			Capacity uint32 `json:"capacity"`
		} `json:"scheduler"`
		AccountSchedulers map[string]struct {
			Capacity uint32 `json:"capacity"`
		} `json:"account_schedulers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Scheduler.Capacity != 3 {
		t.Fatalf("scheduler capacity = %d, want 3", body.Scheduler.Capacity)
	}
	if got := body.AccountSchedulers["alice"].Capacity; got != 3 {
		t.Fatalf("account_schedulers alice capacity = %d, want 3", got)
	}
}
