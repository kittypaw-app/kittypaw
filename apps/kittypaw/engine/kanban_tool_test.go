package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/store"
)

func TestExecuteKanbanCreateShowCommentAndLink(t *testing.T) {
	st := openTestStore(t)
	cfg := core.DefaultConfig()
	cfg.AutonomyLevel = core.AutonomyFull
	sess := &Session{Store: st, Config: &cfg}
	project, err := st.CreateKanbanProject(store.CreateKanbanProjectRequest{Slug: "kitty", Name: "KittyPaw", RootPath: t.TempDir()})
	if err != nil {
		t.Fatalf("CreateKanbanProject: %v", err)
	}

	createRes := resolveKanbanTool(t, sess, "create", map[string]any{
		"project":    "kitty",
		"title":      "Runner task",
		"body":       "from tool",
		"status":     store.KanbanStatusReady,
		"priority":   7,
		"assignee":   "coder",
		"created_by": "runner",
	})
	task := createRes["task"].(map[string]any)
	taskID := task["id"].(string)
	if task["project_id"] != project.ID || task["title"] != "Runner task" || task["status"] != store.KanbanStatusReady {
		t.Fatalf("created task = %+v", task)
	}

	showRes := resolveKanbanTool(t, sess, "show", taskID)
	shown := showRes["task"].(map[string]any)
	if shown["id"] != taskID || shown["title"] != "Runner task" {
		t.Fatalf("shown task = %+v", shown)
	}

	commentRes := resolveKanbanTool(t, sess, "comment", taskID, map[string]any{"author": "agent", "body": "note"})
	comment := commentRes["comment"].(map[string]any)
	if comment["task_id"] != taskID || comment["body"] != "note" {
		t.Fatalf("comment = %+v", comment)
	}

	parent, err := st.CreateKanbanTask(store.CreateKanbanTaskRequest{ProjectID: project.ID, Title: "Parent"})
	if err != nil {
		t.Fatalf("CreateKanbanTask parent: %v", err)
	}
	linkRes := resolveKanbanTool(t, sess, "link", parent.ID, taskID)
	if linkRes["success"] != true {
		t.Fatalf("link result = %+v", linkRes)
	}
	events, err := st.ListKanbanEvents(taskID)
	if err != nil {
		t.Fatalf("ListKanbanEvents: %v", err)
	}
	var linked bool
	for _, event := range events {
		if event.EventType == "linked" && event.Detail == parent.ID {
			linked = true
		}
	}
	if !linked {
		t.Fatalf("link event missing: %+v", events)
	}
}

func TestExecuteKanbanRunLifecycleTools(t *testing.T) {
	st := openTestStore(t)
	cfg := core.DefaultConfig()
	cfg.AutonomyLevel = core.AutonomyFull
	sess := &Session{Store: st, Config: &cfg}
	project, err := st.CreateKanbanProject(store.CreateKanbanProjectRequest{Slug: "kitty", Name: "KittyPaw", RootPath: t.TempDir()})
	if err != nil {
		t.Fatalf("CreateKanbanProject: %v", err)
	}
	task, err := st.CreateKanbanTask(store.CreateKanbanTaskRequest{ProjectID: project.ID, Title: "Lifecycle", Status: store.KanbanStatusReady})
	if err != nil {
		t.Fatalf("CreateKanbanTask: %v", err)
	}

	claimRes := resolveKanbanTool(t, sess, "claim", task.ID, map[string]any{"actor": "agent"})
	run := claimRes["run"].(map[string]any)
	if run["task_id"] != task.ID || run["actor"] != "agent" {
		t.Fatalf("claim run = %+v", run)
	}
	heartbeatRes := resolveKanbanTool(t, sess, "heartbeat", task.ID, map[string]any{"actor": "agent"})
	if heartbeatRes["run"].(map[string]any)["task_id"] != task.ID {
		t.Fatalf("heartbeat = %+v", heartbeatRes)
	}
	completeRes := resolveKanbanTool(t, sess, "complete", task.ID, map[string]any{"actor": "agent", "summary": "done", "metadata": map[string]any{"source": "test"}})
	if completeRes["success"] != true {
		t.Fatalf("complete = %+v", completeRes)
	}
	got, err := st.GetKanbanTask(task.ID)
	if err != nil {
		t.Fatalf("GetKanbanTask: %v", err)
	}
	if got.Status != store.KanbanStatusDone {
		t.Fatalf("status = %q, want done", got.Status)
	}

	blockedTask, err := st.CreateKanbanTask(store.CreateKanbanTaskRequest{ProjectID: project.ID, Title: "Block me", Status: store.KanbanStatusReady})
	if err != nil {
		t.Fatalf("CreateKanbanTask blocked: %v", err)
	}
	resolveKanbanTool(t, sess, "claim", blockedTask.ID, map[string]any{"actor": "agent"})
	blockRes := resolveKanbanTool(t, sess, "block", blockedTask.ID, map[string]any{"actor": "agent", "reason": "waiting"})
	if blockRes["success"] != true {
		t.Fatalf("block = %+v", blockRes)
	}
	got, err = st.GetKanbanTask(blockedTask.ID)
	if err != nil {
		t.Fatalf("GetKanbanTask blocked: %v", err)
	}
	if got.Status != store.KanbanStatusBlocked {
		t.Fatalf("status = %q, want blocked", got.Status)
	}
}

func TestExecuteKanbanValidationErrors(t *testing.T) {
	st := openTestStore(t)
	cfg := core.DefaultConfig()
	cfg.AutonomyLevel = core.AutonomyFull
	sess := &Session{Store: st, Config: &cfg}

	createRes := resolveKanbanTool(t, sess, "create", map[string]any{"project": "missing", "title": "x", "status": "bogus"})
	if errText, _ := createRes["error"].(string); !strings.Contains(errText, "invalid status") {
		t.Fatalf("create error = %+v", createRes)
	}
	completeRes := resolveKanbanTool(t, sess, "complete")
	if errText, _ := completeRes["error"].(string); !strings.Contains(errText, "task id required") {
		t.Fatalf("complete error = %+v", completeRes)
	}
}

func resolveKanbanTool(t *testing.T, sess *Session, method string, args ...any) map[string]any {
	t.Helper()
	rawArgs := make([]json.RawMessage, len(args))
	for i, arg := range args {
		data, err := json.Marshal(arg)
		if err != nil {
			t.Fatalf("marshal arg %d: %v", i, err)
		}
		rawArgs[i] = data
	}
	raw, err := resolveSkillCall(context.Background(), core.SkillCall{SkillName: "Kanban", Method: method, Args: rawArgs}, sess, nil)
	if err != nil {
		t.Fatalf("resolveSkillCall %s: %v", method, err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal %s result %q: %v", method, raw, err)
	}
	return out
}
