package server

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/jinto/kittypaw/store"
)

func TestMemoryAPIManagesPromptSafeUserMemory(t *testing.T) {
	srv := newProjectsAPITestServer(t)
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	if err := srv.store.SetUserContext("memory:preference:lang", "Korean replies", "runner"); err != nil {
		t.Fatalf("SetUserContext language: %v", err)
	}
	if err := srv.store.SetUserContext("fact.name", "Jinto", "runner"); err != nil {
		t.Fatalf("SetUserContext name: %v", err)
	}
	if err := srv.store.SetUserContext("setup:llm_api_key", "sk-secret", "setup"); err != nil {
		t.Fatalf("SetUserContext setup key: %v", err)
	}
	if err := srv.store.SetUserContext("pref.api_key", "should-not-leak", "runner"); err != nil {
		t.Fatalf("SetUserContext sensitive pref: %v", err)
	}
	if err := srv.store.RecordExecution(&store.ExecutionRecord{
		SkillID:       "exec-1",
		SkillName:     "korean-history",
		StartedAt:     now,
		FinishedAt:    now,
		ResultSummary: "Korean execution history",
		Success:       true,
	}); err != nil {
		t.Fatalf("RecordExecution: %v", err)
	}

	var list struct {
		Memory []store.KeyValue `json:"memory"`
	}
	projectsAPIRequest(t, srv, http.MethodGet, "/api/v1/memory?limit=10", nil, http.StatusOK, &list)
	if len(list.Memory) != 2 {
		t.Fatalf("memory list = %+v, want two prompt-safe rows", list.Memory)
	}
	for _, kv := range list.Memory {
		if kv.Key == "setup:llm_api_key" || kv.Key == "pref.api_key" {
			t.Fatalf("memory list leaked unsafe key: %+v", list.Memory)
		}
	}

	var search struct {
		Results []store.KeyValue `json:"results"`
	}
	projectsAPIRequest(t, srv, http.MethodGet, "/api/v1/memory/search?q=Korean", nil, http.StatusOK, &search)
	if len(search.Results) != 1 || search.Results[0].Key != "memory:preference:lang" {
		t.Fatalf("memory search = %+v, want user memory result", search.Results)
	}
	for _, kv := range search.Results {
		if kv.Key == "korean-history" || kv.Value == "Korean execution history" {
			t.Fatalf("memory search leaked execution history: %+v", search.Results)
		}
	}

	var export struct {
		Memory []store.KeyValue `json:"memory"`
	}
	projectsAPIRequest(t, srv, http.MethodGet, "/api/v1/memory/export", nil, http.StatusOK, &export)
	if len(export.Memory) != 2 {
		t.Fatalf("memory export = %+v, want two prompt-safe rows", export.Memory)
	}

	var deleted struct {
		Success bool `json:"success"`
		Deleted bool `json:"deleted"`
	}
	projectsAPIRequest(t, srv, http.MethodDelete, "/api/v1/memory/"+url.PathEscape("fact.name"), nil, http.StatusOK, &deleted)
	if !deleted.Success || !deleted.Deleted {
		t.Fatalf("delete response = %+v, want success/deleted", deleted)
	}
	if _, ok, _ := srv.store.GetUserContext("fact.name"); ok {
		t.Fatal("fact.name still exists after delete")
	}

	projectsAPIRequest(t, srv, http.MethodDelete, "/api/v1/memory/"+url.PathEscape("setup:llm_api_key"), nil, http.StatusNotFound, nil)
	if _, ok, _ := srv.store.GetUserContext("setup:llm_api_key"); !ok {
		t.Fatal("unsafe setup row should not be deleted via memory API")
	}

	var forget struct {
		Success bool `json:"success"`
		Deleted int  `json:"deleted"`
	}
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/memory/forget-all", nil, http.StatusOK, &forget)
	if !forget.Success || forget.Deleted != 1 {
		t.Fatalf("forget-all response = %+v, want one remaining safe row deleted", forget)
	}
	if _, ok, _ := srv.store.GetUserContext("memory:preference:lang"); ok {
		t.Fatal("safe memory row still exists after forget-all")
	}
	if _, ok, _ := srv.store.GetUserContext("setup:llm_api_key"); !ok {
		t.Fatal("forget-all must not delete setup rows")
	}
}

func TestMemoryAPIDeleteDecodesEscapedSlashKey(t *testing.T) {
	srv := newProjectsAPITestServer(t)
	key := "memory:project/foo"
	if err := srv.store.SetUserMemory(key, "Project note", "runner"); err != nil {
		t.Fatalf("SetUserMemory: %v", err)
	}

	var deleted struct {
		Success bool `json:"success"`
		Deleted bool `json:"deleted"`
	}
	projectsAPIRequest(t, srv, http.MethodDelete, "/api/v1/memory/"+url.PathEscape(key), nil, http.StatusOK, &deleted)
	if !deleted.Success || !deleted.Deleted {
		t.Fatalf("delete response = %+v, want success/deleted", deleted)
	}
	if _, ok, _ := srv.store.GetUserContext(key); ok {
		t.Fatal("slash-containing memory key still exists after delete")
	}
}

func TestMemoryAPIDeleteCanTargetOneStructuredScope(t *testing.T) {
	srv := newProjectsAPITestServer(t)
	key := "memory:shared"
	if err := srv.store.SetScopedUserMemory(store.UserMemoryWrite{
		Key:       key,
		Value:     "Global value",
		ScopeType: store.MemoryScopeGlobal,
		Source:    "test",
	}); err != nil {
		t.Fatalf("SetScopedUserMemory global: %v", err)
	}
	if err := srv.store.SetScopedUserMemory(store.UserMemoryWrite{
		Key:       key,
		Value:     "Project value",
		ScopeType: store.MemoryScopeProject,
		ScopeID:   "project-alpha",
		Source:    "test",
	}); err != nil {
		t.Fatalf("SetScopedUserMemory project: %v", err)
	}

	path := "/api/v1/memory/" + url.PathEscape(key) + "?scope_type=project&scope_id=" + url.QueryEscape("project-alpha")
	var deleted struct {
		Success bool `json:"success"`
		Deleted bool `json:"deleted"`
	}
	projectsAPIRequest(t, srv, http.MethodDelete, path, nil, http.StatusOK, &deleted)
	if !deleted.Success || !deleted.Deleted {
		t.Fatalf("delete response = %+v, want success/deleted", deleted)
	}

	rows, err := srv.store.ListMemoryRecords(10)
	if err != nil {
		t.Fatalf("ListMemoryRecords: %v", err)
	}
	var global, project bool
	for _, row := range rows {
		if row.Key != key {
			continue
		}
		if row.ScopeType == store.MemoryScopeGlobal && row.Value == "Global value" {
			global = true
		}
		if row.ScopeType == store.MemoryScopeProject && row.ScopeID == "project-alpha" {
			project = true
		}
	}
	if !global || project {
		t.Fatalf("rows after scoped delete = %+v, want global kept and project deleted", rows)
	}
}

func TestMemoryAPIDeleteRejectsScopeIDWithoutScopedType(t *testing.T) {
	srv := newProjectsAPITestServer(t)
	key := "memory:shared"
	if err := srv.store.SetScopedUserMemory(store.UserMemoryWrite{
		Key:       key,
		Value:     "Global value",
		ScopeType: store.MemoryScopeGlobal,
		Source:    "test",
	}); err != nil {
		t.Fatalf("SetScopedUserMemory global: %v", err)
	}

	for _, query := range []string{
		"?scope_id=" + url.QueryEscape("project-alpha"),
		"?scope_type=global&scope_id=" + url.QueryEscape("project-alpha"),
	} {
		projectsAPIRequest(t, srv, http.MethodDelete, "/api/v1/memory/"+url.PathEscape(key)+query, nil, http.StatusBadRequest, nil)
	}

	rows, err := srv.store.ListMemoryRecords(10)
	if err != nil {
		t.Fatalf("ListMemoryRecords: %v", err)
	}
	if len(rows) != 1 || rows[0].Key != key || rows[0].ScopeType != store.MemoryScopeGlobal {
		t.Fatalf("rows after rejected delete = %+v, want global memory kept", rows)
	}
}

func TestMemoryAPIExposesStructuredMemoryAndPendingConfirmation(t *testing.T) {
	srv := newProjectsAPITestServer(t)
	if err := srv.store.SetScopedUserMemory(store.UserMemoryWrite{
		Key:       "memory:preference:lang",
		Value:     "Korean replies",
		Kind:      "preference",
		ScopeType: store.MemoryScopeGlobal,
		Source:    "test",
	}); err != nil {
		t.Fatalf("SetScopedUserMemory: %v", err)
	}
	pending, err := srv.store.CreatePendingUserMemory(store.UserMemoryWrite{
		Key:       "user.email",
		Value:     "jinto@example.com",
		ScopeType: store.MemoryScopeGlobal,
		Source:    "runner",
	}, "email")
	if err != nil {
		t.Fatalf("CreatePendingUserMemory: %v", err)
	}

	var list struct {
		Memory []store.MemoryRecord `json:"memory"`
	}
	projectsAPIRequest(t, srv, http.MethodGet, "/api/v1/memory?limit=10", nil, http.StatusOK, &list)
	if len(list.Memory) != 1 || list.Memory[0].Kind != "preference" || list.Memory[0].ScopeType != store.MemoryScopeGlobal {
		t.Fatalf("memory list = %+v, want structured memory metadata", list.Memory)
	}

	var pendingList struct {
		Pending []store.PendingMemoryRecord `json:"pending"`
	}
	projectsAPIRequest(t, srv, http.MethodGet, "/api/v1/memory/pending", nil, http.StatusOK, &pendingList)
	if len(pendingList.Pending) != 1 || pendingList.Pending[0].ID != pending.ID || pendingList.Pending[0].Reason != "email" {
		t.Fatalf("pending list = %+v, want pending email row", pendingList.Pending)
	}

	var confirmed struct {
		Success bool               `json:"success"`
		Memory  store.MemoryRecord `json:"memory"`
	}
	projectsAPIRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/memory/pending/%d/confirm", pending.ID), nil, http.StatusOK, &confirmed)
	if !confirmed.Success || confirmed.Memory.Key != "user.email" || confirmed.Memory.Value != "jinto@example.com" {
		t.Fatalf("confirm response = %+v", confirmed)
	}
	if got, ok, err := srv.store.GetUserMemory("user.email"); err != nil || !ok || got != "jinto@example.com" {
		t.Fatalf("confirmed user memory = %q ok=%v err=%v", got, ok, err)
	}
}

func TestMemoryAPICuratesAndAppliesReviewableSuggestions(t *testing.T) {
	srv := newProjectsAPITestServer(t)
	if err := srv.store.SetScopedUserMemory(store.UserMemoryWrite{
		Key:       "memory:nickname",
		Value:     "Kitty",
		ScopeType: store.MemoryScopeGlobal,
		Source:    "test",
	}); err != nil {
		t.Fatalf("SetScopedUserMemory keep: %v", err)
	}
	if err := srv.store.SetScopedUserMemory(store.UserMemoryWrite{
		Key:       "fact.nickname",
		Value:     "Kitty",
		ScopeType: store.MemoryScopeGlobal,
		Source:    "test",
	}); err != nil {
		t.Fatalf("SetScopedUserMemory duplicate: %v", err)
	}
	var curated struct {
		Candidates []store.MemoryCurationCandidate `json:"candidates"`
	}
	projectsAPIRequest(t, srv, http.MethodGet, "/api/v1/memory/curate", nil, http.StatusOK, &curated)
	if len(curated.Candidates) == 0 || curated.Candidates[0].Type != "duplicate" || !curated.Candidates[0].Applyable {
		t.Fatalf("curation candidates = %+v, want applyable duplicate", curated.Candidates)
	}

	var applied struct {
		Success   bool                          `json:"success"`
		Candidate store.MemoryCurationCandidate `json:"candidate"`
	}
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/memory/curate/"+curated.Candidates[0].ID+"/apply", nil, http.StatusOK, &applied)
	if !applied.Success || applied.Candidate.ID != curated.Candidates[0].ID {
		t.Fatalf("apply response = %+v", applied)
	}
	rows, err := srv.store.ListMemoryRecords(10)
	if err != nil {
		t.Fatalf("ListMemoryRecords: %v", err)
	}
	var kittyRows int
	for _, row := range rows {
		if row.Value == "Kitty" {
			kittyRows++
		}
	}
	if kittyRows != 1 {
		t.Fatalf("Kitty memory rows after API apply = %d rows=%+v, want one", kittyRows, rows)
	}
}
