# Kanban Task Controls Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add edit/archive controls for durable Kanban tasks across store, API, CLI, and the local Web UI.

**Architecture:** Extend the existing `apps/kittypaw/store` Kanban kernel first, then expose the same operations through server handlers, Cobra commands, and the Web drawer. No schema migration is needed because `kanban_tasks` already has the fields this phase edits.

**Tech Stack:** Go, SQLite through `database/sql`, chi HTTP routes, Cobra CLI, vanilla JS Web UI.

---

## File Map

- Modify `apps/kittypaw/store/kanban.go`
  - Add `UpdateKanbanTaskRequest`.
  - Add `UpdateKanbanTask`.
  - Add `ArchiveKanbanTask`.
  - Add blocker checks and archived-default list filtering.
- Modify `apps/kittypaw/store/kanban_test.go`
  - Add store coverage for update, archive, archived filtering, and status guards.
- Modify `apps/kittypaw/server/api_kanban.go`
  - Add `handleKanbanTaskUpdate`.
  - Add `handleKanbanTaskArchive`.
  - Add request-body pointer handling for optional fields.
- Modify `apps/kittypaw/server/server.go`
  - Register `PATCH /api/v1/kanban/tasks/{task}`.
  - Register `POST /api/v1/kanban/tasks/{task}/archive`.
- Modify `apps/kittypaw/server/api_kanban_test.go`
  - Add API coverage for update/archive and validation/missing-task paths.
- Modify `apps/kittypaw/cli/cmd_kanban.go`
  - Add `kanban edit`.
  - Add `kanban archive`.
  - Track whether edit flags were supplied.
- Modify `apps/kittypaw/cli/cmd_kanban_test.go`
  - Add CLI command/flag and behavior coverage.
- Modify `apps/kittypaw/server/web/kanban.js`
  - Add drawer edit form.
  - Add `PATCH` update request.
  - Add archive request.
- Modify `apps/kittypaw/server/web_kanban_test.go`
  - Add static coverage for edit/archive controls and endpoints.
- Modify `apps/kittypaw/server/web/style.css`
  - Add compact drawer edit-form spacing.

---

### Task 1: Store Task Update And Archive

**Files:**
- Modify: `apps/kittypaw/store/kanban.go`
- Modify: `apps/kittypaw/store/kanban_test.go`

- [ ] **Step 1: Write failing store tests**

Add tests to `apps/kittypaw/store/kanban_test.go`:

```go
func TestKanbanUpdateTaskEditsFieldsAndMilestone(t *testing.T) {
	st := openTestStore(t)
	project, err := st.CreateKanbanProject(CreateKanbanProjectRequest{Slug: "kitty", Name: "KittyPaw", RootPath: "/repo/kitty"})
	if err != nil {
		t.Fatalf("CreateKanbanProject: %v", err)
	}
	ms, err := st.CreateKanbanMilestone(CreateKanbanMilestoneRequest{ProjectID: project.ID, Title: "Release One"})
	if err != nil {
		t.Fatalf("CreateKanbanMilestone: %v", err)
	}
	task, err := st.CreateKanbanTask(CreateKanbanTaskRequest{ProjectID: project.ID, Title: "Old title", Body: "old", Status: KanbanStatusTodo, Priority: 1, Assignee: "alice"})
	if err != nil {
		t.Fatalf("CreateKanbanTask: %v", err)
	}

	title := "New title"
	body := ""
	priority := 5
	assignee := "bob"
	status := KanbanStatusReady
	milestoneID := ms.ID
	updated, err := st.UpdateKanbanTask(task.ID, UpdateKanbanTaskRequest{
		Actor:       "carol",
		Title:       &title,
		Body:        &body,
		Priority:    &priority,
		Assignee:    &assignee,
		Status:      &status,
		MilestoneID: &milestoneID,
	})
	if err != nil {
		t.Fatalf("UpdateKanbanTask: %v", err)
	}
	if updated.Title != title || updated.Body != "" || updated.Priority != priority || updated.Assignee != assignee || updated.Status != KanbanStatusReady || updated.MilestoneID != ms.ID {
		t.Fatalf("updated task = %+v", updated)
	}
	events, err := st.ListKanbanEvents(task.ID)
	if err != nil {
		t.Fatalf("ListKanbanEvents: %v", err)
	}
	if len(events) < 2 || events[len(events)-1].EventType != "updated" || events[len(events)-1].Actor != "carol" {
		t.Fatalf("events = %+v", events)
	}
}

func TestKanbanUpdateTaskClearsMilestoneAndCompletedAt(t *testing.T) {
	st := openTestStore(t)
	project, err := st.CreateKanbanProject(CreateKanbanProjectRequest{Slug: "kitty", Name: "KittyPaw", RootPath: "/repo/kitty"})
	if err != nil {
		t.Fatalf("CreateKanbanProject: %v", err)
	}
	ms, err := st.CreateKanbanMilestone(CreateKanbanMilestoneRequest{ProjectID: project.ID, Title: "Release One"})
	if err != nil {
		t.Fatalf("CreateKanbanMilestone: %v", err)
	}
	task, err := st.CreateKanbanTask(CreateKanbanTaskRequest{ProjectID: project.ID, MilestoneID: ms.ID, Title: "Done task", Status: KanbanStatusTodo})
	if err != nil {
		t.Fatalf("CreateKanbanTask: %v", err)
	}
	if _, err := st.ClaimKanbanTask(task.ID, ClaimKanbanTaskRequest{Actor: "alice"}); err != nil {
		t.Fatalf("ClaimKanbanTask: %v", err)
	}
	if err := st.CompleteKanbanTask(task.ID, CompleteKanbanTaskRequest{Actor: "alice", Summary: "done"}); err != nil {
		t.Fatalf("CompleteKanbanTask: %v", err)
	}

	status := KanbanStatusTodo
	updated, err := st.UpdateKanbanTask(task.ID, UpdateKanbanTaskRequest{Actor: "alice", Status: &status, ClearMilestone: true})
	if err != nil {
		t.Fatalf("UpdateKanbanTask: %v", err)
	}
	if updated.Status != KanbanStatusTodo || updated.CompletedAt != "" || updated.MilestoneID != "" {
		t.Fatalf("updated task = %+v", updated)
	}
}

func TestKanbanUpdateTaskRejectsRunningAndBlockedReady(t *testing.T) {
	st := openTestStore(t)
	project, err := st.CreateKanbanProject(CreateKanbanProjectRequest{Slug: "kitty", Name: "KittyPaw", RootPath: "/repo/kitty"})
	if err != nil {
		t.Fatalf("CreateKanbanProject: %v", err)
	}
	parent, err := st.CreateKanbanTask(CreateKanbanTaskRequest{ProjectID: project.ID, Title: "Parent", Status: KanbanStatusTodo})
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}
	child, err := st.CreateKanbanTask(CreateKanbanTaskRequest{ProjectID: project.ID, Title: "Child", Status: KanbanStatusTodo})
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}
	if err := st.LinkKanbanTasks(parent.ID, child.ID); err != nil {
		t.Fatalf("LinkKanbanTasks: %v", err)
	}
	ready := KanbanStatusReady
	if _, err := st.UpdateKanbanTask(child.ID, UpdateKanbanTaskRequest{Status: &ready}); err == nil {
		t.Fatal("expected ready move with incomplete blocker to fail")
	}
	running := KanbanStatusRunning
	if _, err := st.UpdateKanbanTask(parent.ID, UpdateKanbanTaskRequest{Status: &running}); err == nil {
		t.Fatal("expected direct running move to fail")
	}
	if _, err := st.ClaimKanbanTask(parent.ID, ClaimKanbanTaskRequest{Actor: "alice"}); err != nil {
		t.Fatalf("ClaimKanbanTask: %v", err)
	}
	todo := KanbanStatusTodo
	if _, err := st.UpdateKanbanTask(parent.ID, UpdateKanbanTaskRequest{Status: &todo}); err == nil {
		t.Fatal("expected update from running to fail")
	}
}

func TestKanbanArchiveHidesTaskFromDefaultList(t *testing.T) {
	st := openTestStore(t)
	project, err := st.CreateKanbanProject(CreateKanbanProjectRequest{Slug: "kitty", Name: "KittyPaw", RootPath: "/repo/kitty"})
	if err != nil {
		t.Fatalf("CreateKanbanProject: %v", err)
	}
	task, err := st.CreateKanbanTask(CreateKanbanTaskRequest{ProjectID: project.ID, Title: "Archive me", Status: KanbanStatusTodo})
	if err != nil {
		t.Fatalf("CreateKanbanTask: %v", err)
	}
	archived, err := st.ArchiveKanbanTask(task.ID, "alice")
	if err != nil {
		t.Fatalf("ArchiveKanbanTask: %v", err)
	}
	if archived.Status != KanbanStatusArchived {
		t.Fatalf("archived status = %q", archived.Status)
	}
	tasks, err := st.ListKanbanTasks(KanbanTaskListFilter{ProjectID: project.ID})
	if err != nil {
		t.Fatalf("ListKanbanTasks default: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("default list = %+v, want archived hidden", tasks)
	}
	tasks, err = st.ListKanbanTasks(KanbanTaskListFilter{ProjectID: project.ID, Status: KanbanStatusArchived})
	if err != nil {
		t.Fatalf("ListKanbanTasks archived: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != task.ID {
		t.Fatalf("archived list = %+v", tasks)
	}
}

func TestKanbanArchiveRejectsRunningTask(t *testing.T) {
	st := openTestStore(t)
	project, err := st.CreateKanbanProject(CreateKanbanProjectRequest{Slug: "kitty", Name: "KittyPaw", RootPath: "/repo/kitty"})
	if err != nil {
		t.Fatalf("CreateKanbanProject: %v", err)
	}
	task, err := st.CreateKanbanTask(CreateKanbanTaskRequest{ProjectID: project.ID, Title: "Running", Status: KanbanStatusTodo})
	if err != nil {
		t.Fatalf("CreateKanbanTask: %v", err)
	}
	if _, err := st.ClaimKanbanTask(task.ID, ClaimKanbanTaskRequest{Actor: "alice"}); err != nil {
		t.Fatalf("ClaimKanbanTask: %v", err)
	}
	if _, err := st.ArchiveKanbanTask(task.ID, "alice"); err == nil {
		t.Fatal("expected archiving running task to fail")
	}
}
```

- [ ] **Step 2: Run store tests to verify RED**

Run:

```bash
cd apps/kittypaw
go test ./store -run 'TestKanban.*(Update|Archive|List)' -count=1
```

Expected: fail because `UpdateKanbanTaskRequest`, `UpdateKanbanTask`, and
`ArchiveKanbanTask` are undefined.

- [ ] **Step 3: Implement store behavior**

In `apps/kittypaw/store/kanban.go`:

- Add `UpdateKanbanTaskRequest` near existing request structs.
- Add `IncludeArchived bool` to `KanbanTaskListFilter`; default behavior should
  be `status != archived` when no explicit status is supplied.
- Add `UpdateKanbanTask`.
- Add `ArchiveKanbanTask`.
- Add helpers:
  - `kanbanTaskHasIncompleteBlockers`
  - `kanbanUpdateMetadata`
  - `kanbanValidateTaskStatus`

Implementation details:

```go
if filter.Status != "" {
	query += ` AND status = ?`
	args = append(args, filter.Status)
} else if !filter.IncludeArchived {
	query += ` AND status != ?`
	args = append(args, KanbanStatusArchived)
}
```

Status rules:

```go
if current.Status == KanbanStatusRunning {
	return nil, fmt.Errorf("task %s is running", taskID)
}
if nextStatus == KanbanStatusRunning {
	return nil, fmt.Errorf("task %s cannot move to running directly", taskID)
}
if nextStatus == KanbanStatusReady && hasIncompleteBlockers {
	return nil, fmt.Errorf("task %s has incomplete blockers", taskID)
}
```

Completion timestamp rules:

```go
if nextStatus == KanbanStatusDone && current.CompletedAt == "" {
	completedAtValue = now
}
if current.Status == KanbanStatusDone && nextStatus != KanbanStatusDone {
	completedAtValue = nil
}
```

Event rules:

```go
recordKanbanEventTx(tx, taskID, req.Actor, "updated", strings.Join(changedFields, ","), metadataJSON)
recordKanbanEventTx(tx, taskID, actor, "archived", "", "{}")
```

- [ ] **Step 4: Run store tests to verify GREEN**

Run:

```bash
cd apps/kittypaw
go test ./store -run 'TestKanban.*(Update|Archive|List|Dependency|TaskClaimComplete)' -count=1
```

Expected: pass.

- [ ] **Step 5: Commit store changes**

Run:

```bash
git add apps/kittypaw/store/kanban.go apps/kittypaw/store/kanban_test.go
git commit -m "feat(store): add kanban task controls"
```

---

### Task 2: Server API Update And Archive

**Files:**
- Modify: `apps/kittypaw/server/api_kanban.go`
- Modify: `apps/kittypaw/server/server.go`
- Modify: `apps/kittypaw/server/api_kanban_test.go`

- [ ] **Step 1: Write failing API tests**

Add tests:

```go
func TestKanbanAPITaskUpdateAndArchive(t *testing.T) {
	srv := newKanbanAPITestServer(t)
	kanbanAPICreateProject(t, srv, "kitty")
	kanbanAPICreateMilestone(t, srv, "kitty", "Release One")
	taskID := kanbanAPICreateTask(t, srv, "kitty", "Old title")

	var updated struct {
		Task struct {
			ID          string `json:"id"`
			Title       string `json:"title"`
			Body        string `json:"body"`
			Status      string `json:"status"`
			Priority    int    `json:"priority"`
			Assignee    string `json:"assignee"`
			MilestoneID string `json:"milestone_id"`
		} `json:"task"`
	}
	kanbanAPIRequest(t, srv, http.MethodPatch, "/api/v1/kanban/tasks/"+taskID, map[string]any{
		"actor":     "alice",
		"title":     "New title",
		"body":      "",
		"status":    "ready",
		"priority":  5,
		"assignee":  "bob",
		"milestone": "release-one",
	}, http.StatusOK, &updated)
	if updated.Task.ID != taskID || updated.Task.Title != "New title" || updated.Task.Body != "" || updated.Task.Status != "ready" || updated.Task.Priority != 5 || updated.Task.Assignee != "bob" || updated.Task.MilestoneID == "" {
		t.Fatalf("updated task = %+v", updated.Task)
	}

	kanbanAPIRequest(t, srv, http.MethodPatch, "/api/v1/kanban/tasks/"+taskID, map[string]any{
		"clear_milestone": true,
	}, http.StatusOK, &updated)
	if updated.Task.MilestoneID != "" {
		t.Fatalf("milestone not cleared: %+v", updated.Task)
	}

	var archived struct {
		Task struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"task"`
	}
	kanbanAPIRequest(t, srv, http.MethodPost, "/api/v1/kanban/tasks/"+taskID+"/archive", map[string]any{
		"actor": "alice",
	}, http.StatusOK, &archived)
	if archived.Task.ID != taskID || archived.Task.Status != "archived" {
		t.Fatalf("archived task = %+v", archived.Task)
	}

	var listed struct {
		Tasks []struct {
			ID string `json:"id"`
		} `json:"tasks"`
	}
	kanbanAPIRequest(t, srv, http.MethodGet, "/api/v1/kanban/tasks?project=kitty", nil, http.StatusOK, &listed)
	if len(listed.Tasks) != 0 {
		t.Fatalf("default listed tasks = %+v, want archived hidden", listed.Tasks)
	}
	kanbanAPIRequest(t, srv, http.MethodGet, "/api/v1/kanban/tasks?project=kitty&status=archived", nil, http.StatusOK, &listed)
	if len(listed.Tasks) != 1 || listed.Tasks[0].ID != taskID {
		t.Fatalf("archived listed tasks = %+v", listed.Tasks)
	}
}
```

Extend existing validation and missing-route tests with:

```go
kanbanAPIRequest(t, srv, http.MethodPatch, "/api/v1/kanban/tasks/"+taskID, map[string]any{
	"title": " ",
}, http.StatusBadRequest, nil)
kanbanAPIRequest(t, srv, http.MethodPatch, "/api/v1/kanban/tasks/"+taskID, map[string]any{
	"status": "running",
}, http.StatusBadRequest, nil)
kanbanAPIRequest(t, srv, http.MethodPatch, "/api/v1/kanban/tasks/"+taskID, map[string]any{
	"milestone": "release-one",
	"clear_milestone": true,
}, http.StatusBadRequest, nil)
```

And missing routes:

```go
{"update", http.MethodPatch, "/api/v1/kanban/tasks/" + missingTaskID, map[string]any{"title": "x"}},
{"archive", http.MethodPost, "/api/v1/kanban/tasks/" + missingTaskID + "/archive", map[string]any{}},
```

- [ ] **Step 2: Run API tests to verify RED**

Run:

```bash
cd apps/kittypaw
go test ./server -run 'TestKanbanAPI.*(Update|Archive|Validation|Missing)' -count=1
```

Expected: fail because routes and handlers are missing.

- [ ] **Step 3: Implement API handlers and routes**

In `apps/kittypaw/server/server.go` register:

```go
r.Patch("/kanban/tasks/{task}", s.handleKanbanTaskUpdate)
r.Post("/kanban/tasks/{task}/archive", s.handleKanbanTaskArchive)
```

In `apps/kittypaw/server/api_kanban.go` add:

```go
func (s *Server) handleKanbanTaskUpdate(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(chi.URLParam(r, "task"))
	task, err := kanbanResolveTask(s.store, taskID)
	if err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	var body struct {
		Actor          string  `json:"actor"`
		Title          *string `json:"title"`
		Body           *string `json:"body"`
		Status         *string `json:"status"`
		Priority       *int    `json:"priority"`
		Assignee       *string `json:"assignee"`
		Milestone      *string `json:"milestone"`
		ClearMilestone bool    `json:"clear_milestone"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	// Trim/validate pointer fields, resolve milestone against task.ProjectID,
	// then call s.store.UpdateKanbanTask.
	writeJSON(w, http.StatusOK, map[string]any{"task": updated})
}
```

Use existing helpers `kanbanValidateStatus`, `kanbanResolveMilestoneID`, and
`kanbanWriteStoreError`. Convert store validation failures to HTTP 400.

Add:

```go
func (s *Server) handleKanbanTaskArchive(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(chi.URLParam(r, "task"))
	if _, err := kanbanResolveTask(s.store, taskID); err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	var body struct{ Actor string `json:"actor"` }
	if r.Body != nil && r.ContentLength != 0 {
		if !decodeBody(w, r, &body) {
			return
		}
	}
	task, err := s.store.ArchiveKanbanTask(taskID, strings.TrimSpace(body.Actor))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": task})
}
```

- [ ] **Step 4: Run API tests to verify GREEN**

Run:

```bash
cd apps/kittypaw
go test ./server -run 'TestKanbanAPI.*(Update|Archive|Validation|Missing|TaskCreateListShow)' -count=1
```

Expected: pass.

- [ ] **Step 5: Commit API changes**

Run:

```bash
git add apps/kittypaw/server/api_kanban.go apps/kittypaw/server/server.go apps/kittypaw/server/api_kanban_test.go
git commit -m "feat(server): expose kanban task controls"
```

---

### Task 3: CLI Edit And Archive

**Files:**
- Modify: `apps/kittypaw/cli/cmd_kanban.go`
- Modify: `apps/kittypaw/cli/cmd_kanban_test.go`

- [ ] **Step 1: Write failing CLI tests**

Extend command exposure:

```go
{"kanban", "edit"},
{"kanban", "archive"},
```

Extend flag test:

```go
edit := mustFindCommand(t, root, []string{"kanban", "edit"})
for _, flag := range []string{"actor", "title", "body", "status", "priority", "assignee", "milestone", "clear-milestone", "account"} {
	if edit.Flag(flag) == nil {
		t.Fatalf("kanban edit missing --%s", flag)
	}
}
archive := mustFindCommand(t, root, []string{"kanban", "archive"})
for _, flag := range []string{"actor", "account"} {
	if archive.Flag(flag) == nil {
		t.Fatalf("kanban archive missing --%s", flag)
	}
}
```

Add behavior tests:

```go
func TestKanbanEditUpdatesTaskFields(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))

	st, err := openStoreForAccount("alice")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	project, err := st.CreateKanbanProject(store.CreateKanbanProjectRequest{Slug: "kitty", Name: "KittyPaw", RootPath: t.TempDir()})
	if err != nil {
		t.Fatalf("CreateKanbanProject: %v", err)
	}
	ms, err := st.CreateKanbanMilestone(store.CreateKanbanMilestoneRequest{ProjectID: project.ID, Title: "Release One"})
	if err != nil {
		t.Fatalf("CreateKanbanMilestone: %v", err)
	}
	task, err := st.CreateKanbanTask(store.CreateKanbanTaskRequest{ProjectID: project.ID, Title: "Old title", Status: store.KanbanStatusTodo})
	if err != nil {
		t.Fatalf("CreateKanbanTask: %v", err)
	}
	_ = st.Close()

	priority := 8
	err = runKanbanEdit(task.ID, &kanbanEditFlags{
		shared:          &kanbanSharedFlags{accountID: "alice"},
		actor:           "alice",
		title:           "New title",
		titleSet:        true,
		status:          store.KanbanStatusReady,
		statusSet:       true,
		priority:        priority,
		prioritySet:     true,
		assignee:        "bob",
		assigneeSet:     true,
		milestone:       ms.Slug,
		milestoneSet:    true,
		clearMilestone:  false,
	})
	if err != nil {
		t.Fatalf("runKanbanEdit: %v", err)
	}

	st, err = openStoreForAccount("alice")
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer st.Close()
	got, err := st.GetKanbanTask(task.ID)
	if err != nil {
		t.Fatalf("GetKanbanTask: %v", err)
	}
	if got.Title != "New title" || got.Status != store.KanbanStatusReady || got.Priority != priority || got.Assignee != "bob" || got.MilestoneID != ms.ID {
		t.Fatalf("task = %+v", got)
	}
}

func TestKanbanArchiveArchivesTask(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))

	st, err := openStoreForAccount("alice")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	project, err := st.CreateKanbanProject(store.CreateKanbanProjectRequest{Slug: "kitty", Name: "KittyPaw", RootPath: t.TempDir()})
	if err != nil {
		t.Fatalf("CreateKanbanProject: %v", err)
	}
	task, err := st.CreateKanbanTask(store.CreateKanbanTaskRequest{ProjectID: project.ID, Title: "Archive", Status: store.KanbanStatusTodo})
	if err != nil {
		t.Fatalf("CreateKanbanTask: %v", err)
	}
	_ = st.Close()

	if err := runKanbanArchive(task.ID, &kanbanArchiveFlags{shared: &kanbanSharedFlags{accountID: "alice"}, actor: "alice"}); err != nil {
		t.Fatalf("runKanbanArchive: %v", err)
	}

	st, err = openStoreForAccount("alice")
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer st.Close()
	got, err := st.GetKanbanTask(task.ID)
	if err != nil {
		t.Fatalf("GetKanbanTask: %v", err)
	}
	if got.Status != store.KanbanStatusArchived {
		t.Fatalf("status = %q", got.Status)
	}
}
```

- [ ] **Step 2: Run CLI tests to verify RED**

Run:

```bash
cd apps/kittypaw
go test ./cli -run 'TestKanbanCommand|TestKanbanEdit|TestKanbanArchive' -count=1
```

Expected: fail because commands and helpers are missing.

- [ ] **Step 3: Implement CLI commands**

In `apps/kittypaw/cli/cmd_kanban.go`:

- Add `kanbanEditFlags`.
- Add `kanbanArchiveFlags`.
- Add `newKanbanEditCmd`.
- Add `newKanbanArchiveCmd`.
- Add `runKanbanEdit`.
- Add `runKanbanArchive`.

Use Cobra `cmd.Flags().Changed("<flag>")` to set `titleSet`, `bodySet`,
`statusSet`, `prioritySet`, `assigneeSet`, and `milestoneSet` before invoking
`runKanbanEdit`.

Resolve milestone using the existing task's `ProjectID`:

```go
task, err := st.GetKanbanTask(strings.TrimSpace(taskID))
milestoneID, err := resolveKanbanMilestoneID(st, task.ProjectID, flags.milestone)
```

Call:

```go
updated, err := st.UpdateKanbanTask(task.ID, store.UpdateKanbanTaskRequest{...})
```

Reject no-op edit if no editable flag is present.

- [ ] **Step 4: Run CLI tests to verify GREEN**

Run:

```bash
cd apps/kittypaw
go test ./cli -run 'TestKanbanCommand|TestKanbanEdit|TestKanbanArchive' -count=1
```

Expected: pass.

- [ ] **Step 5: Commit CLI changes**

Run:

```bash
git add apps/kittypaw/cli/cmd_kanban.go apps/kittypaw/cli/cmd_kanban_test.go
git commit -m "feat(cli): add kanban task controls"
```

---

### Task 4: Web Drawer Controls

**Files:**
- Modify: `apps/kittypaw/server/web/kanban.js`
- Modify: `apps/kittypaw/server/web/style.css`
- Modify: `apps/kittypaw/server/web_kanban_test.go`

- [ ] **Step 1: Write failing Web static tests**

Extend `TestKanbanWebModuleSupportsCreateDetailActionsAndRuns` tokens:

```go
"id=\"kanban-edit-form\"",
"id=\"kanban-archive-task\"",
"method: method",
"'/api/v1/kanban/tasks/' + encodeURIComponent(this._selectedTaskID)",
"'/archive'",
"_updateTask",
"_archiveTask",
```

- [ ] **Step 2: Run Web tests to verify RED**

Run:

```bash
cd apps/kittypaw
go test ./server -run 'TestKanbanWeb' -count=1
```

Expected: fail because drawer edit/archive controls are missing.

- [ ] **Step 3: Implement Web drawer edit/archive**

In `apps/kittypaw/server/web/kanban.js`:

- Add `_editFormHTML(task)` after `_actionRowHTML()`.
- Add `_updateTask(form)`.
- Add `_archiveTask()`.
- Add `_requestJSON(url, method, body)` and make `_postJSON` call it.
- Bind `kanban-edit-form` submit and `kanban-archive-task` click.

The request method helper should be:

```js
async _requestJSON(url, method, body) {
  return api(url, {
    method: method,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
}
```

After archive:

```js
this._selectedTaskID = '';
this._detail = null;
await this._loadProjectData();
this._render();
```

In `apps/kittypaw/server/web/style.css`, add compact rules only if the drawer
form needs spacing:

```css
.kanban-edit-form {
  margin-top: 14px;
  padding-top: 14px;
  border-top: 1px solid #E2E8F0;
}

.kanban-edit-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
```

- [ ] **Step 4: Run Web tests and JS syntax check**

Run:

```bash
cd apps/kittypaw
go test ./server -run 'TestKanbanWeb' -count=1
node --check server/web/kanban.js
```

Expected: pass.

- [ ] **Step 5: Commit Web changes**

Run:

```bash
git add apps/kittypaw/server/web/kanban.js apps/kittypaw/server/web/style.css apps/kittypaw/server/web_kanban_test.go
git commit -m "feat(web): add kanban task controls"
```

---

### Task 5: Review And Verification

**Files:**
- Review all changed files.

- [ ] **Step 1: Run focused verification**

Run:

```bash
cd apps/kittypaw
go test ./store -run 'TestKanban.*(Update|Archive|List|Dependency|TaskClaimComplete)' -count=1
go test ./server -run 'TestKanbanAPI.*(Update|Archive|Validation|Missing|TaskCreateListShow)|TestKanbanWeb' -count=1
go test ./cli -run 'TestKanbanCommand|TestKanbanEdit|TestKanbanArchive' -count=1
node --check server/web/kanban.js
```

Expected: all pass.

- [ ] **Step 2: Review diff locally**

Run:

```bash
git diff --stat main...HEAD
git diff main...HEAD -- apps/kittypaw/store/kanban.go apps/kittypaw/server/api_kanban.go apps/kittypaw/cli/cmd_kanban.go apps/kittypaw/server/web/kanban.js
```

Check:

- No product-facing use of "worktree" was introduced.
- API and CLI both use store operations, not duplicated SQL.
- Running tasks cannot be edited or archived into inconsistent states.
- Archived tasks are hidden from default lists and still reachable by explicit
  `status=archived`.
- Web UI uses `api()`, not `apiRaw()` or `apiPost()`, for `/api/v1` routes.

- [ ] **Step 3: Run full verification**

Run:

```bash
cd apps/kittypaw
go test ./... -short -count=1
```

Expected: all packages pass.

- [ ] **Step 4: Commit any review fixes**

If review finds issues, fix them with focused tests first, then commit:

```bash
git add <changed-files>
git commit -m "fix: tighten kanban task controls"
```

- [ ] **Step 5: Final status**

Run:

```bash
git status --short --branch
git log --oneline --max-count=8 main..HEAD
```

Expected: clean branch with the design, plan, store, API, CLI, Web, and optional
review-fix commits.
