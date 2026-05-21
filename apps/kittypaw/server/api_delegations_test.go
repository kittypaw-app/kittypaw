package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/store"
)

func TestDelegationsAPIAcceptsNonDefaultAccountAPIKey(t *testing.T) {
	root := t.TempDir()
	aliceCfg := &core.Config{Server: core.ServerConfig{APIKey: "alice-key"}}
	bobCfg := &core.Config{Server: core.ServerConfig{APIKey: "bob-key"}}
	aliceDeps := buildAccountDeps(t, root, "alice", aliceCfg)
	bobDeps := buildAccountDeps(t, root, "bob", bobCfg)
	srv := New([]*AccountDeps{aliceDeps, bobDeps}, "test", "alice")

	if _, err := aliceDeps.Store.CreateDelegationJob(store.CreateDelegationJobRequest{
		AccountID:            "alice",
		StaffID:              "coder",
		Task:                 "alice task",
		ParentConversationID: "general:alice",
	}); err != nil {
		t.Fatalf("seed alice delegation: %v", err)
	}
	bobJob, err := bobDeps.Store.CreateDelegationJob(store.CreateDelegationJobRequest{
		AccountID:            "bob",
		StaffID:              "coder",
		Task:                 "bob task",
		ParentConversationID: "general:bob",
	})
	if err != nil {
		t.Fatalf("seed bob delegation: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/delegations?limit=10", nil)
	req.Header.Set("x-api-key", "bob-key")
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/delegations as bob code = %d body=%s, want 200", rr.Code, rr.Body.String())
	}
	var body struct {
		Jobs []store.DelegationJob `json:"delegations"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rr.Body.String())
	}
	if len(body.Jobs) != 1 || body.Jobs[0].ID != bobJob.ID || body.Jobs[0].AccountID != "bob" {
		t.Fatalf("delegations = %+v, want bob row only", body.Jobs)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/delegations/"+bobJob.ID, nil)
	req.Header.Set("x-api-key", "bob-key")
	rr = httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/delegations/{id} code = %d body=%s, want 200", rr.Code, rr.Body.String())
	}
	var detail struct {
		Job store.DelegationJob `json:"delegation"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.Job.ID != bobJob.ID {
		t.Fatalf("detail = %+v, want bob job", detail.Job)
	}
}

func TestDelegationsAPICancelIsScopedToRequestAccount(t *testing.T) {
	root := t.TempDir()
	aliceCfg := &core.Config{Server: core.ServerConfig{APIKey: "alice-key"}}
	bobCfg := &core.Config{Server: core.ServerConfig{APIKey: "bob-key"}}
	aliceDeps := buildAccountDeps(t, root, "alice", aliceCfg)
	bobDeps := buildAccountDeps(t, root, "bob", bobCfg)
	srv := New([]*AccountDeps{aliceDeps, bobDeps}, "test", "alice")

	aliceJob, err := aliceDeps.Store.CreateDelegationJob(store.CreateDelegationJobRequest{
		AccountID:            "alice",
		StaffID:              "coder",
		Task:                 "alice task",
		ParentConversationID: "general:alice",
	})
	if err != nil {
		t.Fatalf("seed alice delegation: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/delegations/"+aliceJob.ID+"/cancel", nil)
	req.Header.Set("x-api-key", "bob-key")
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("bob cancel alice job code = %d body=%s, want 404", rr.Code, rr.Body.String())
	}

	bobJob, err := bobDeps.Store.CreateDelegationJob(store.CreateDelegationJobRequest{
		AccountID:            "bob",
		StaffID:              "coder",
		Task:                 "bob task",
		ParentConversationID: "general:bob",
	})
	if err != nil {
		t.Fatalf("seed bob delegation: %v", err)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/delegations/"+bobJob.ID+"/cancel", nil)
	req.Header.Set("x-api-key", "bob-key")
	rr = httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("bob cancel bob job code = %d body=%s, want 200", rr.Code, rr.Body.String())
	}
	canceled, ok, err := bobDeps.Store.GetDelegationJob(bobJob.ID)
	if err != nil || !ok {
		t.Fatalf("GetDelegationJob = ok %v err %v", ok, err)
	}
	if canceled.Status != store.DelegationJobStatusCanceled {
		t.Fatalf("canceled status = %q, want canceled", canceled.Status)
	}
}

func TestDelegationsAPITreeIsScopedToRequestAccount(t *testing.T) {
	root := t.TempDir()
	aliceCfg := &core.Config{Server: core.ServerConfig{APIKey: "alice-key"}}
	bobCfg := &core.Config{Server: core.ServerConfig{APIKey: "bob-key"}}
	aliceDeps := buildAccountDeps(t, root, "alice", aliceCfg)
	bobDeps := buildAccountDeps(t, root, "bob", bobCfg)
	srv := New([]*AccountDeps{aliceDeps, bobDeps}, "test", "alice")

	rootConversationID := "general:shared"
	aliceJob, err := aliceDeps.Store.CreateDelegationJob(store.CreateDelegationJobRequest{
		AccountID:            "alice",
		StaffID:              "coder",
		Task:                 "alice task",
		ParentConversationID: rootConversationID,
	})
	if err != nil {
		t.Fatalf("seed alice delegation: %v", err)
	}
	bobParent, err := bobDeps.Store.CreateDelegationJob(store.CreateDelegationJobRequest{
		AccountID:            "bob",
		StaffID:              "researcher",
		Task:                 "bob parent",
		ParentConversationID: rootConversationID,
	})
	if err != nil {
		t.Fatalf("seed bob parent: %v", err)
	}
	bobChild, err := bobDeps.Store.CreateDelegationJob(store.CreateDelegationJobRequest{
		AccountID:            "bob",
		StaffID:              "coder",
		Task:                 "bob child",
		ParentConversationID: rootConversationID,
		ParentJobID:          bobParent.ID,
	})
	if err != nil {
		t.Fatalf("seed bob child: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/delegations/tree?conversation_id="+rootConversationID+"&limit=10", nil)
	req.Header.Set("x-api-key", "bob-key")
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/delegations/tree code = %d body=%s, want 200", rr.Code, rr.Body.String())
	}
	var body struct {
		Tree store.DelegationJobTree `json:"tree"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rr.Body.String())
	}
	if body.Tree.AccountID != "bob" || body.Tree.RootConversationID != rootConversationID {
		t.Fatalf("tree metadata = %+v, want bob shared root", body.Tree)
	}
	if body.Tree.Summary.Total != 2 || len(body.Tree.Jobs) != 1 {
		t.Fatalf("tree = %+v, want bob parent with child only", body.Tree)
	}
	if body.Tree.Jobs[0].Job.ID != bobParent.ID || len(body.Tree.Jobs[0].Children) != 1 || body.Tree.Jobs[0].Children[0].Job.ID != bobChild.ID {
		t.Fatalf("tree jobs = %+v, want bob hierarchy", body.Tree.Jobs)
	}
	if body.Tree.Jobs[0].Job.ID == aliceJob.ID {
		t.Fatalf("tree leaked alice job: %+v", body.Tree.Jobs[0].Job)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/delegations/tree", nil)
	req.Header.Set("x-api-key", "bob-key")
	rr = httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("GET /api/v1/delegations/tree without conversation code = %d body=%s, want 400", rr.Code, rr.Body.String())
	}
}
