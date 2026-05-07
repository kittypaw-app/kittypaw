# Kanban Agent Toolset Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose durable Kanban operations as `Kanban.*` JavaScript skill calls.

**Architecture:** Add `Kanban` to `core.SkillRegistry`, let the sandbox automatically expose stubs, and route `Kanban` calls in `engine.resolveSkillCall` to a new `executeKanban` helper backed by existing `store` Kanban APIs.

**Tech Stack:** Go, goja sandbox stubs, existing engine skill resolver, existing SQLite-backed Kanban store.

---

### Task 1: Failing Sandbox and Engine Tests

**Files:**
- Modify: `apps/kittypaw/sandbox/sandbox_test.go`
- Create: `apps/kittypaw/engine/kanban_tool_test.go`

- [ ] **Step 1: Add sandbox skill-call exposure test**

Add to `apps/kittypaw/sandbox/sandbox_test.go`:

```go
func TestExecuteKanbanSkillCall(t *testing.T) {
	sb := New(core.SandboxConfig{TimeoutSecs: 5})
	code := `
		Kanban.show("tsk_123");
		Kanban.complete("tsk_123", {summary: "done"});
		return "ok";
	`
	result, err := sb.Execute(context.Background(), code, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	if len(result.SkillCalls) != 2 {
		t.Fatalf("expected 2 skill calls, got %d", len(result.SkillCalls))
	}
	if result.SkillCalls[0].SkillName != "Kanban" || result.SkillCalls[0].Method != "show" {
		t.Fatalf("call 0 = %+v", result.SkillCalls[0])
	}
	if result.SkillCalls[1].SkillName != "Kanban" || result.SkillCalls[1].Method != "complete" {
		t.Fatalf("call 1 = %+v", result.SkillCalls[1])
	}
}
```

- [ ] **Step 2: Add engine create/show/comment/link test**

Create `apps/kittypaw/engine/kanban_tool_test.go`:

```go
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
		"title":      "Agent task",
		"body":       "from tool",
		"status":     store.KanbanStatusReady,
		"priority":   7,
		"assignee":   "coder",
		"created_by": "agent",
	})
	task := createRes["task"].(map[string]any)
	taskID := task["id"].(string)
	if task["project_id"] != project.ID || task["title"] != "Agent task" || task["status"] != store.KanbanStatusReady {
		t.Fatalf("created task = %+v", task)
	}

	showRes := resolveKanbanTool(t, sess, "show", taskID)
	shown := showRes["task"].(map[string]any)
	if shown["id"] != taskID || shown["title"] != "Agent task" {
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
```

- [ ] **Step 3: Add lifecycle and validation tests**

Append to `apps/kittypaw/engine/kanban_tool_test.go`:

```go
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
```

- [ ] **Step 4: Run failing tests**

Run:

```bash
cd apps/kittypaw
go test ./sandbox -run 'TestExecuteKanbanSkillCall' -count=1
go test ./engine -run 'TestExecuteKanban' -count=1
```

Expected: FAIL because `Kanban` is not in `SkillRegistry` and `resolveSkillCall` does not dispatch it.

### Task 2: Implement Kanban Skill Registry and Resolver

**Files:**
- Modify: `apps/kittypaw/core/skillmeta.go`
- Modify: `apps/kittypaw/engine/executor.go`

- [ ] **Step 1: Add Kanban to SkillRegistry**

Add after `Todo`:

```go
{Name: "Kanban", Methods: []SkillMethodMeta{
	{Name: "show", Signature: "Kanban.show(taskId) — returns {task, comments, runs, events}"},
	{Name: "create", Signature: "Kanban.create({project, title, body?, status?, priority?, assignee?, milestone?, created_by?}) — creates a task and returns {task}"},
	{Name: "claim", Signature: "Kanban.claim(taskId, options?) — starts a Run; options: {actor, work_dir}. Returns {run}"},
	{Name: "complete", Signature: "Kanban.complete(taskId, options?) — completes a running task; options: {actor, summary, metadata}. Returns {success}"},
	{Name: "block", Signature: "Kanban.block(taskId, reasonOrOptions) — blocks a task; options: {actor, reason}. Returns {success}"},
	{Name: "comment", Signature: "Kanban.comment(taskId, bodyOrOptions) — adds a comment; options: {author, body}. Returns {comment}"},
	{Name: "link", Signature: "Kanban.link(parentTaskId, childTaskId) — marks parent as blocking child. Returns {success}"},
	{Name: "heartbeat", Signature: "Kanban.heartbeat(taskId, options?) — refreshes a running Run heartbeat. Returns {run}"},
}},
```

- [ ] **Step 2: Route Kanban calls**

In `resolveSkillCall`, add:

```go
case "Kanban":
	return executeKanban(ctx, call, s)
```

Import `github.com/jinto/kittypaw/store` in `apps/kittypaw/engine/executor.go`.

- [ ] **Step 3: Add executeKanban and helpers**

Add near `executeTodo` or before Profile:

```go
func executeKanban(ctx context.Context, call core.SkillCall, s *Session) (string, error) {
	if s.Store == nil {
		return jsonResult(map[string]any{"error": "kanban store not configured"})
	}
	switch call.Method {
	case "show":
		taskID, err := kanbanToolStringArg(call, 0, "task id")
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		task, err := s.Store.GetKanbanTask(taskID)
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		comments, err := s.Store.ListKanbanComments(taskID)
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		runs, err := s.Store.ListKanbanRuns(taskID)
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		events, err := s.Store.ListKanbanEvents(taskID)
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{"task": task, "comments": comments, "runs": runs, "events": events})
	case "create":
		return executeKanbanCreate(call, s)
	case "claim":
		return executeKanbanClaim(call, s)
	case "complete":
		return executeKanbanComplete(call, s)
	case "block":
		return executeKanbanBlock(call, s)
	case "comment":
		return executeKanbanComment(call, s)
	case "link":
		parentID, err := kanbanToolStringArg(call, 0, "parent task id")
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		childID, err := kanbanToolStringArg(call, 1, "child task id")
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		if err := s.Store.LinkKanbanTasks(parentID, childID); err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{"success": true})
	case "heartbeat":
		return executeKanbanHeartbeat(call, s)
	default:
		return jsonResult(map[string]any{"error": fmt.Sprintf("unknown Kanban method: %s", call.Method)})
	}
	_ = ctx
}
```

Add helper implementations for the method-specific calls using existing store
request structs.

- [ ] **Step 4: Run focused passing tests**

Run:

```bash
cd apps/kittypaw
go test ./sandbox -run 'TestExecuteKanbanSkillCall' -count=1
go test ./engine -run 'TestExecuteKanban' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit implementation**

Run:

```bash
git add apps/kittypaw/core/skillmeta.go apps/kittypaw/sandbox/sandbox_test.go apps/kittypaw/engine/executor.go apps/kittypaw/engine/kanban_tool_test.go
git commit -m "feat(engine): expose kanban agent tools"
```

### Task 3: Verification

**Files:**
- Inspect: `apps/kittypaw/core/skillmeta.go`
- Inspect: `apps/kittypaw/engine/executor.go`
- Inspect: `apps/kittypaw/engine/kanban_tool_test.go`
- Inspect: `apps/kittypaw/sandbox/sandbox_test.go`

- [ ] **Step 1: Run focused package tests**

Run:

```bash
cd apps/kittypaw
go test ./engine ./sandbox -count=1
```

Expected: PASS.

- [ ] **Step 2: Run app short tests**

Run:

```bash
cd apps/kittypaw
go test ./... -short -count=1
```

Expected: PASS.
