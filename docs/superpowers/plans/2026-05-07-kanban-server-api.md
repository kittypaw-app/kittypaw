# Kanban Server API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add authenticated `/api/v1` Kanban HTTP endpoints backed by the existing durable Kanban store.

**Architecture:** Implement a focused server handler file that translates HTTP JSON requests into existing `store.Store` Kanban methods. Register routes under the existing `/api/v1` API-key/session gate and keep this phase scoped to the default account store. Tests use the existing `srv.setupRoutes()` server helpers and real tempdir SQLite stores.

**Tech Stack:** Go, chi, net/http/httptest, existing `store.Store`, existing server JSON helpers, `go test`.

---

## File Structure

- Create `apps/kittypaw/server/api_kanban.go`
  - Owns Kanban request/response structs, route handlers, project/board/milestone resolution, metadata/date/status validation, and error mapping.
- Create `apps/kittypaw/server/api_kanban_test.go`
  - Owns route, lifecycle, action, and error tests for the Kanban API.
- Modify `apps/kittypaw/server/server.go`
  - Registers the Kanban routes inside the existing `/api/v1` route group.

## Task 1: Route Registration Baseline

**Files:**
- Create: `apps/kittypaw/server/api_kanban_test.go`
- Create: `apps/kittypaw/server/api_kanban.go`
- Modify: `apps/kittypaw/server/server.go`

- [ ] **Step 1: Write failing route/auth test**

Create `apps/kittypaw/server/api_kanban_test.go` with:

```go
package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
)

func TestKanbanAPIRequiresAuthAndRegistersProjectRoutes(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Server.APIKey = "api-key"
	srv := newServerWithLocalUserAndConfig(t, "alice", "pw", &cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("projects without auth code = %d, want 401", rr.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	req.Header.Set("x-api-key", "api-key")
	rr = httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("projects with auth code = %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"projects":[]`) {
		t.Fatalf("projects body = %s, want empty projects envelope", rr.Body.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd apps/kittypaw
go test ./server -run 'TestKanbanAPIRequiresAuthAndRegistersProjectRoutes' -count=1
```

Expected: FAIL with 404 or route not found for `/api/v1/projects`.

- [ ] **Step 3: Add minimal handlers and routes**

Create `apps/kittypaw/server/api_kanban.go` with a minimal `handleKanbanProjectsList` that calls `s.store.ListKanbanProjects(false)` and writes `{"projects":[]}` when empty.

Register inside `server.go` `/api/v1` group:

```go
// Kanban
r.Get("/projects", s.handleKanbanProjectsList)
```

- [ ] **Step 4: Run test to verify it passes**

```bash
cd apps/kittypaw
go test ./server -run 'TestKanbanAPIRequiresAuthAndRegistersProjectRoutes' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/kittypaw/server/api_kanban.go apps/kittypaw/server/api_kanban_test.go apps/kittypaw/server/server.go
git commit -m "feat(server): register kanban api"
```

## Task 2: Project, Board, And Milestone API

**Files:**
- Modify: `apps/kittypaw/server/api_kanban.go`
- Modify: `apps/kittypaw/server/api_kanban_test.go`
- Modify: `apps/kittypaw/server/server.go`

- [ ] **Step 1: Add failing project/milestone lifecycle test**

Append a test that:

- `POST /api/v1/projects` with `{"slug":"kitty","name":"KittyPaw","root_path":"/repo/kitty"}` returns `201`.
- `GET /api/v1/projects/kitty` returns the same project.
- `GET /api/v1/projects/kitty/boards` returns the default board.
- `POST /api/v1/projects/kitty/milestones` creates a milestone.
- `GET /api/v1/projects/kitty/milestones` lists it.

- [ ] **Step 2: Run test to verify it fails**

```bash
cd apps/kittypaw
go test ./server -run 'TestKanbanAPIProjectMilestoneLifecycle' -count=1
```

Expected: FAIL because create/show/milestone routes are missing.

- [ ] **Step 3: Implement project and milestone handlers**

Implement:

- `handleKanbanProjectsCreate`
- `handleKanbanProjectShow`
- `handleKanbanProjectBoardsList`
- `handleKanbanProjectMilestonesList`
- `handleKanbanProjectMilestonesCreate`
- helper `kanbanResolveProject`
- helper `kanbanWriteStoreError`
- helper `kanbanValidateDate`

Register:

```go
r.Post("/projects", s.handleKanbanProjectsCreate)
r.Get("/projects/{project}", s.handleKanbanProjectShow)
r.Get("/projects/{project}/boards", s.handleKanbanProjectBoardsList)
r.Get("/projects/{project}/milestones", s.handleKanbanProjectMilestonesList)
r.Post("/projects/{project}/milestones", s.handleKanbanProjectMilestonesCreate)
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd apps/kittypaw
go test ./server -run 'TestKanbanAPI(RequiresAuthAndRegistersProjectRoutes|ProjectMilestoneLifecycle)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/kittypaw/server/api_kanban.go apps/kittypaw/server/api_kanban_test.go apps/kittypaw/server/server.go
git commit -m "feat(server): add kanban project api"
```

## Task 3: Task Create, List, And Show API

**Files:**
- Modify: `apps/kittypaw/server/api_kanban.go`
- Modify: `apps/kittypaw/server/api_kanban_test.go`
- Modify: `apps/kittypaw/server/server.go`

- [ ] **Step 1: Add failing task lifecycle test**

Append a test that:

- creates a project and milestone through the API,
- `POST /api/v1/kanban/tasks` creates a task with project slug and milestone slug,
- `GET /api/v1/kanban/tasks?project=kitty&status=todo` lists it,
- `GET /api/v1/kanban/tasks/{task}` returns `task`, `comments`, `events`, and `runs`.

- [ ] **Step 2: Run test to verify it fails**

```bash
cd apps/kittypaw
go test ./server -run 'TestKanbanAPITaskCreateListShow' -count=1
```

Expected: FAIL because task routes are missing.

- [ ] **Step 3: Implement task handlers**

Implement:

- `handleKanbanTasksCreate`
- `handleKanbanTasksList`
- `handleKanbanTaskShow`
- helper `kanbanResolveBoardID`
- helper `kanbanResolveMilestoneID`
- helper `kanbanValidateStatus`

Register:

```go
r.Get("/kanban/tasks", s.handleKanbanTasksList)
r.Post("/kanban/tasks", s.handleKanbanTasksCreate)
r.Get("/kanban/tasks/{task}", s.handleKanbanTaskShow)
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd apps/kittypaw
go test ./server -run 'TestKanbanAPI(RequiresAuthAndRegistersProjectRoutes|ProjectMilestoneLifecycle|TaskCreateListShow)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/kittypaw/server/api_kanban.go apps/kittypaw/server/api_kanban_test.go apps/kittypaw/server/server.go
git commit -m "feat(server): add kanban task api"
```

## Task 4: Task Actions, Comments, Runs, And Links API

**Files:**
- Modify: `apps/kittypaw/server/api_kanban.go`
- Modify: `apps/kittypaw/server/api_kanban_test.go`
- Modify: `apps/kittypaw/server/server.go`

- [ ] **Step 1: Add failing action test**

Append a test that:

- creates a project and task through the API,
- `POST /api/v1/kanban/tasks/{task}/claim` returns a run,
- `POST /api/v1/kanban/tasks/{task}/complete` marks the task done and stores metadata,
- `GET /api/v1/kanban/tasks/{task}/runs` returns the completed run,
- `POST /api/v1/kanban/tasks/{task}/comments` creates a comment,
- `GET /api/v1/kanban/tasks/{task}/comments` lists it,
- creates a second task and `POST /api/v1/kanban/tasks/{task}/links` links parent to child.

- [ ] **Step 2: Run test to verify it fails**

```bash
cd apps/kittypaw
go test ./server -run 'TestKanbanAPITaskActionsCommentsRunsAndLinks' -count=1
```

Expected: FAIL because action/comment/run/link routes are missing.

- [ ] **Step 3: Implement action handlers**

Implement:

- `handleKanbanTaskClaim`
- `handleKanbanTaskComplete`
- `handleKanbanTaskBlock`
- `handleKanbanTaskUnblock`
- `handleKanbanTaskCommentsList`
- `handleKanbanTaskCommentsCreate`
- `handleKanbanTaskRunsList`
- `handleKanbanTaskLinksCreate`
- helper `kanbanMetadataJSON`

Register:

```go
r.Post("/kanban/tasks/{task}/claim", s.handleKanbanTaskClaim)
r.Post("/kanban/tasks/{task}/complete", s.handleKanbanTaskComplete)
r.Post("/kanban/tasks/{task}/block", s.handleKanbanTaskBlock)
r.Post("/kanban/tasks/{task}/unblock", s.handleKanbanTaskUnblock)
r.Get("/kanban/tasks/{task}/comments", s.handleKanbanTaskCommentsList)
r.Post("/kanban/tasks/{task}/comments", s.handleKanbanTaskCommentsCreate)
r.Get("/kanban/tasks/{task}/runs", s.handleKanbanTaskRunsList)
r.Post("/kanban/tasks/{task}/links", s.handleKanbanTaskLinksCreate)
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd apps/kittypaw
go test ./server -run 'TestKanbanAPI' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/kittypaw/server/api_kanban.go apps/kittypaw/server/api_kanban_test.go apps/kittypaw/server/server.go
git commit -m "feat(server): add kanban task actions api"
```

## Task 5: Error Mapping And Final Verification

**Files:**
- Modify: `apps/kittypaw/server/api_kanban.go`
- Modify: `apps/kittypaw/server/api_kanban_test.go`

- [ ] **Step 1: Add failing validation test**

Append a test that asserts:

- `POST /api/v1/projects` with relative `root_path` returns `400`.
- `GET /api/v1/projects/missing` returns `404`.
- `GET /api/v1/kanban/tasks` without `project` returns `400`.
- `POST /api/v1/kanban/tasks` with unknown status returns `400`.
- `POST /api/v1/kanban/tasks/{task}/complete` with missing `summary` returns `400`.

- [ ] **Step 2: Run test to verify it fails**

```bash
cd apps/kittypaw
go test ./server -run 'TestKanbanAPIValidationAndNotFound' -count=1
```

Expected: FAIL for any missing validation/error mapping.

- [ ] **Step 3: Implement validation fixes**

Update helper and handler validation so all cases return the expected status codes.

- [ ] **Step 4: Run focused tests**

```bash
cd apps/kittypaw
go test ./server -run 'TestKanbanAPI' -count=1
```

Expected: PASS.

- [ ] **Step 5: Run full short suite**

```bash
cd apps/kittypaw
go test ./... -short -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add apps/kittypaw/server/api_kanban.go apps/kittypaw/server/api_kanban_test.go
git commit -m "test(server): cover kanban api errors"
```
