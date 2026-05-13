package server

import (
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
