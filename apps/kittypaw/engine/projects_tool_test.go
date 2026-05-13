package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/store"
)

func TestExecuteProjectsCreateShowTicketMoveCommentAndBriefCommit(t *testing.T) {
	st := openTestStore(t)
	cfg := core.DefaultConfig()
	cfg.AutonomyLevel = core.AutonomyFull
	sess := &AccountRuntime{Store: st, Config: &cfg}
	project, err := st.CreateProject(store.CreateProjectRequest{Key: "kitty", Name: "KittyPaw", RootPath: t.TempDir()})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	createRes := resolveProjectsTool(t, sess, "createTicket", map[string]any{
		"project":    "KITTY",
		"title":      "Runner task",
		"body":       "from tool",
		"status":     store.TicketStatusReady,
		"priority":   7,
		"created_by": "runner",
	})
	ticket := createRes["ticket"].(map[string]any)
	ticketID := ticket["id"].(string)
	if ticket["project_id"] != project.ID || ticket["title"] != "Runner task" || ticket["status"] != store.TicketStatusReady {
		t.Fatalf("created ticket = %+v", ticket)
	}

	showRes := resolveProjectsTool(t, sess, "showTicket", ticketID)
	shown := showRes["ticket"].(map[string]any)
	if shown["id"] != ticketID || shown["title"] != "Runner task" {
		t.Fatalf("shown ticket = %+v", shown)
	}

	moveRes := resolveProjectsTool(t, sess, "moveTicket", ticketID, map[string]any{
		"status":   store.TicketStatusInProgress,
		"actor_id": "runner",
		"message":  "starting",
	})
	moved := moveRes["ticket"].(map[string]any)
	if moved["status"] != store.TicketStatusInProgress {
		t.Fatalf("moved ticket = %+v", moved)
	}

	commentRes := resolveProjectsTool(t, sess, "commentTicket", ticketID, map[string]any{
		"author_id": "runner",
		"body":      "note",
	})
	comment := commentRes["message"].(map[string]any)
	if comment["ticket_id"] != ticketID || comment["body"] != "note" {
		t.Fatalf("comment = %+v", comment)
	}

	draftRes := resolveProjectsTool(t, sess, "createBriefDraft", project.ID, map[string]any{
		"title":                 "Brief",
		"brief_json":            `{"summary":"scan"}`,
		"proposed_tickets_json": `[{"temp_id":"a","title":"A","priority":9}]`,
	})
	draft := draftRes["draft"].(map[string]any)
	updateRes := resolveProjectsTool(t, sess, "updateBriefDraft", draft["id"], map[string]any{
		"title": "Brief v2",
	})
	if updateRes["draft"].(map[string]any)["title"] != "Brief v2" {
		t.Fatalf("updated draft = %+v", updateRes["draft"])
	}
	commitRes := resolveProjectsTool(t, sess, "commitBriefDraft", draft["id"], map[string]any{"actor_id": "pm"})
	result := commitRes["result"].(map[string]any)
	if len(result["tickets"].([]any)) != 1 {
		t.Fatalf("commit result = %+v", result)
	}
}

func TestExecuteProjectsPlanJobAndRejectStart(t *testing.T) {
	st := openTestStore(t)
	cfg := core.DefaultConfig()
	cfg.AutonomyLevel = core.AutonomyFull
	sess := &AccountRuntime{Store: st, Config: &cfg}
	project, err := st.CreateProject(store.CreateProjectRequest{Key: "kitty", Name: "KittyPaw", RootPath: t.TempDir()})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	ticket, err := st.CreateTicket(store.CreateTicketRequest{ProjectID: project.ID, Title: "Run job"})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	planRes := resolveProjectsTool(t, sess, "planJob", ticket.ID, map[string]any{
		"driver_id":      "codex",
		"mode":           store.JobModeOneShot,
		"prompt_summary": "Run job",
		"prompt_text":    "Run this ticket.",
		"created_by":     "pm",
	})
	job := planRes["job"].(map[string]any)
	if job["status"] != store.JobStatusPlanned || job["driver_id"] != "codex" {
		t.Fatalf("planned job = %+v", job)
	}
	showRes := resolveProjectsTool(t, sess, "showJob", job["id"])
	if showRes["job"].(map[string]any)["id"] != job["id"] {
		t.Fatalf("shown job = %+v", showRes)
	}
	cancelRes := resolveProjectsTool(t, sess, "cancelJob", job["id"], map[string]any{"actor_id": "pm", "reason": "not now"})
	if cancelRes["job"].(map[string]any)["status"] != store.JobStatusCanceled {
		t.Fatalf("canceled job = %+v", cancelRes)
	}
}

func TestExecuteKanbanToolIsUnknown(t *testing.T) {
	st := openTestStore(t)
	cfg := core.DefaultConfig()
	cfg.AutonomyLevel = core.AutonomyFull
	sess := &AccountRuntime{Store: st, Config: &cfg}
	raw, err := resolveSkillCall(context.Background(), core.SkillCall{SkillName: "Kanban", Method: "show"}, sess, nil)
	if err != nil {
		t.Fatalf("resolveSkillCall(Kanban): %v", err)
	}
	if !strings.Contains(raw, "unknown skill: Kanban") {
		t.Fatalf("Kanban result = %s, want unknown skill", raw)
	}
}

func resolveProjectsTool(t *testing.T, sess *AccountRuntime, method string, args ...any) map[string]any {
	t.Helper()
	rawArgs := make([]json.RawMessage, len(args))
	for i, arg := range args {
		data, err := json.Marshal(arg)
		if err != nil {
			t.Fatalf("marshal arg %d: %v", i, err)
		}
		rawArgs[i] = data
	}
	raw, err := resolveSkillCall(context.Background(), core.SkillCall{SkillName: "Projects", Method: method, Args: rawArgs}, sess, nil)
	if err != nil {
		t.Fatalf("resolveSkillCall %s: %v", method, err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal %s result %q: %v", method, raw, err)
	}
	if errText, _ := out["error"].(string); errText != "" {
		t.Fatalf("Projects.%s returned error: %s", method, errText)
	}
	return out
}
