# Kanban Run Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add heartbeat, cancel, and reclaim transitions for durable Kanban task Runs.

**Architecture:** Extend the existing `apps/kittypaw/store` Kanban kernel first, then expose the same transitions through server handlers and Cobra commands. The existing `kanban_task_runs` schema already contains `heartbeat_at`, `finished_at`, and the needed outcome constants, so no migration is required.

**Tech Stack:** Go, SQLite through `database/sql`, chi HTTP routes, Cobra CLI.

---

## File Map

- Modify `apps/kittypaw/store/kanban.go`
  - Add request structs for heartbeat, cancel, and reclaim.
  - Add `HeartbeatKanbanTask`, `CancelKanbanTask`, and `ReclaimKanbanTask`.
  - Add a helper for finding the latest running Run inside a transaction.
- Modify `apps/kittypaw/store/kanban_test.go`
  - Add store transition tests.
- Modify `apps/kittypaw/server/api_kanban.go`
  - Add handlers for `/heartbeat`, `/cancel`, and `/reclaim`.
- Modify `apps/kittypaw/server/server.go`
  - Register the three routes.
- Modify `apps/kittypaw/server/api_kanban_test.go`
  - Add API transition and validation tests.
- Modify `apps/kittypaw/cli/cmd_kanban.go`
  - Add `kanban heartbeat`, `kanban cancel`, and `kanban reclaim`.
- Modify `apps/kittypaw/cli/cmd_kanban_test.go`
  - Add CLI command/flag and behavior tests.

---

### Task 1: Store Run Lifecycle

**Files:**
- Modify: `apps/kittypaw/store/kanban.go`
- Modify: `apps/kittypaw/store/kanban_test.go`

- [ ] **Step 1: Write failing store tests**

Add tests named:

```go
func TestKanbanHeartbeatUpdatesRunningRun(t *testing.T)
func TestKanbanHeartbeatRequiresRunningRun(t *testing.T)
func TestKanbanCancelClosesRunAndReturnsTaskToTodo(t *testing.T)
func TestKanbanReclaimClosesOldRunAndStartsNewRun(t *testing.T)
func TestKanbanReclaimRequiresActorReasonAndRunningRun(t *testing.T)
```

The tests should create a project, create a task, claim it where needed, then
assert:

- heartbeat changes the running Run's `heartbeat_at` and keeps outcome
  `running`;
- heartbeat without a running Run returns an error;
- cancel sets the Run outcome to `canceled`, writes the reason to summary,
  sets `finished_at`, moves task status to `todo`, and records a `canceled`
  event;
- reclaim creates two Run rows: first `reclaimed`, second `running`;
- reclaim returns the new running Run and keeps task status `running`;
- reclaim rejects empty actor, empty reason, and tasks with no running Run.

- [ ] **Step 2: Run store tests to verify RED**

Run:

```bash
cd apps/kittypaw
go test ./store -run 'TestKanban.*(Heartbeat|Cancel|Reclaim)' -count=1
```

Expected: compile failure because the new request types and methods do not
exist.

- [ ] **Step 3: Implement store transitions**

In `apps/kittypaw/store/kanban.go`, add:

```go
type HeartbeatKanbanTaskRequest struct {
    Actor string
}

type CancelKanbanTaskRequest struct {
    Actor        string
    Reason       string
    MetadataJSON string
}

type ReclaimKanbanTaskRequest struct {
    Actor           string
    Reason          string
    WorkDir         string
    WorkDirProvider string
    MetadataJSON    string
}
```

Implement:

```go
func (s *Store) HeartbeatKanbanTask(taskID string, req HeartbeatKanbanTaskRequest) (*KanbanRun, error)
func (s *Store) CancelKanbanTask(taskID string, req CancelKanbanTaskRequest) (*KanbanTask, error)
func (s *Store) ReclaimKanbanTask(taskID string, req ReclaimKanbanTaskRequest) (*KanbanRun, error)
```

Use these rules:

- update/select the latest running Run with `ORDER BY started_at DESC LIMIT 1`;
- cancel requires trimmed `Reason`;
- reclaim requires trimmed `Actor` and `Reason`;
- cancel returns the task after moving it to `todo`;
- reclaim closes the old run as `reclaimed`, then inserts a new `running` Run;
- reclaim reuses the old Run work dir/provider unless request values override
  them;
- record `canceled` and `reclaimed` task events;
- heartbeat does not record a task event.

- [ ] **Step 4: Run store tests to verify GREEN**

Run:

```bash
cd apps/kittypaw
go test ./store -run 'TestKanban.*(Heartbeat|Cancel|Reclaim|TaskClaimComplete|Fail)' -count=1
```

Expected: pass.

- [ ] **Step 5: Commit store changes**

Run:

```bash
git add apps/kittypaw/store/kanban.go apps/kittypaw/store/kanban_test.go
git commit -m "feat(store): add kanban run lifecycle"
```

---

### Task 2: Server API Run Lifecycle

**Files:**
- Modify: `apps/kittypaw/server/api_kanban.go`
- Modify: `apps/kittypaw/server/server.go`
- Modify: `apps/kittypaw/server/api_kanban_test.go`

- [ ] **Step 1: Write failing API tests**

Add tests for:

```go
func TestKanbanAPIRunLifecycleHeartbeatCancelReclaim(t *testing.T)
```

Extend existing validation and missing-task route tests to include:

```go
POST /api/v1/kanban/tasks/{task}/heartbeat
POST /api/v1/kanban/tasks/{task}/cancel
POST /api/v1/kanban/tasks/{task}/reclaim
```

Assert heartbeat returns a running Run, cancel returns a `todo` task, reclaim
returns a new running Run, invalid cancel/reclaim bodies return 400, and missing
task routes return 404.

- [ ] **Step 2: Run API tests to verify RED**

Run:

```bash
cd apps/kittypaw
go test ./server -run 'TestKanbanAPI.*(Heartbeat|Cancel|Reclaim|Validation|Missing)' -count=1
```

Expected: 404/405 or compile failure because the handlers are not registered.

- [ ] **Step 3: Implement API handlers and routes**

Register:

```go
r.Post("/kanban/tasks/{task}/heartbeat", s.handleKanbanTaskHeartbeat)
r.Post("/kanban/tasks/{task}/cancel", s.handleKanbanTaskCancel)
r.Post("/kanban/tasks/{task}/reclaim", s.handleKanbanTaskReclaim)
```

Handlers should validate the task exists, decode optional JSON bodies where
allowed, reuse `kanbanMetadataJSON`, normalize `work_dir` with `filepath.Clean`
when supplied, call store methods, and write `{"run": run}` or `{"task": task}`.

- [ ] **Step 4: Run API tests to verify GREEN**

Run:

```bash
cd apps/kittypaw
go test ./server -run 'TestKanbanAPI.*(Heartbeat|Cancel|Reclaim|Validation|Missing|TaskActions)' -count=1
```

Expected: pass.

- [ ] **Step 5: Commit API changes**

Run:

```bash
git add apps/kittypaw/server/api_kanban.go apps/kittypaw/server/server.go apps/kittypaw/server/api_kanban_test.go
git commit -m "feat(server): expose kanban run lifecycle"
```

---

### Task 3: CLI Run Lifecycle

**Files:**
- Modify: `apps/kittypaw/cli/cmd_kanban.go`
- Modify: `apps/kittypaw/cli/cmd_kanban_test.go`

- [ ] **Step 1: Write failing CLI tests**

Extend command exposure tests for:

```text
kanban heartbeat
kanban cancel
kanban reclaim
```

Add behavior tests:

```go
func TestKanbanHeartbeatUpdatesRun(t *testing.T)
func TestKanbanCancelCancelsRun(t *testing.T)
func TestKanbanReclaimStartsReplacementRun(t *testing.T)
```

Each behavior test should use a temporary `KITTYPAW_CONFIG_DIR`, create a local
project/task/run, call the run helper directly, reopen the account store, and
assert persisted task/run state.

- [ ] **Step 2: Run CLI tests to verify RED**

Run:

```bash
cd apps/kittypaw
go test ./cli -run 'TestKanbanCommand|TestKanbanHeartbeat|TestKanbanCancel|TestKanbanReclaim' -count=1
```

Expected: compile failure because commands and run helpers do not exist.

- [ ] **Step 3: Implement CLI commands**

Add flag structs:

```go
type kanbanHeartbeatFlags struct { shared *kanbanSharedFlags; actor string }
type kanbanCancelFlags struct { shared *kanbanSharedFlags; actor string; metadata string }
type kanbanReclaimFlags struct { shared *kanbanSharedFlags; actor string; workDir string; metadata string }
```

Add commands:

```bash
kanban heartbeat <task> [--actor]
kanban cancel <task> <reason> [--actor] [--metadata]
kanban reclaim <task> <reason> [--actor] [--work-dir] [--metadata]
```

Use existing helpers `validateKanbanMetadata` and `normalizeRunWorkDir`.

- [ ] **Step 4: Run CLI tests to verify GREEN**

Run:

```bash
cd apps/kittypaw
go test ./cli -run 'TestKanbanCommand|TestKanbanHeartbeat|TestKanbanCancel|TestKanbanReclaim' -count=1
```

Expected: pass.

- [ ] **Step 5: Commit CLI changes**

Run:

```bash
git add apps/kittypaw/cli/cmd_kanban.go apps/kittypaw/cli/cmd_kanban_test.go
git commit -m "feat(cli): add kanban run lifecycle"
```

---

### Task 4: Review And Verification

**Files:**
- Review all changed files.

- [ ] **Step 1: Run focused verification**

Run:

```bash
cd apps/kittypaw
go test ./store -run 'TestKanban.*(Heartbeat|Cancel|Reclaim|TaskClaimComplete|Fail)' -count=1
go test ./server -run 'TestKanbanAPI.*(Heartbeat|Cancel|Reclaim|Validation|Missing|TaskActions)' -count=1
go test ./cli -run 'TestKanbanCommand|TestKanbanHeartbeat|TestKanbanCancel|TestKanbanReclaim' -count=1
```

Expected: all pass.

- [ ] **Step 2: Review diff locally**

Run:

```bash
git diff --stat main...HEAD
git diff main...HEAD -- apps/kittypaw/store/kanban.go apps/kittypaw/server/api_kanban.go apps/kittypaw/cli/cmd_kanban.go
```

Check:

- no product-facing use of the reserved word for Git working directories;
- API and CLI call store methods rather than duplicating SQL;
- heartbeat does not create event spam;
- cancel and reclaim cannot run without a current running Run;
- reclaim always leaves exactly one running Run for the task.

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
git commit -m "fix: tighten kanban run lifecycle"
```

- [ ] **Step 5: Final status**

Run:

```bash
git status --short --branch
git log --oneline --max-count=8 main..HEAD
```

Expected: clean branch with design, plan, store, API, CLI, and optional review
fix commits.
