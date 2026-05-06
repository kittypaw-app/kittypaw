# Project-Aware Durable Kanban Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the first local Project-aware durable Kanban MVP for `apps/kittypaw`: schema, store kernel, and CLI for Project/Board/Milestone/Task/Run.

**Architecture:** Add account-local Kanban persistence to the existing `store.Store` SQLite layer, then expose it through Cobra commands in `apps/kittypaw/cli`. Board and milestone commands live under `kittypaw project`, while task workflow commands live under `kittypaw kanban`.

**Tech Stack:** Go, Cobra, existing `modernc.org/sqlite` store, embedded SQL migrations, `go test`.

---

## File Structure

- Create `apps/kittypaw/store/migrations/023_kanban.sql`
  - Owns the Kanban schema.
- Create `apps/kittypaw/store/kanban.go`
  - Owns Kanban DTOs, IDs, validation constants, and store methods.
- Create `apps/kittypaw/store/kanban_test.go`
  - Owns persistence and transition tests.
- Create `apps/kittypaw/cli/cmd_project.go`
  - Owns `kittypaw project ...`, including `board` and `milestone` subcommands.
- Create `apps/kittypaw/cli/cmd_project_test.go`
  - Owns Project CLI tests.
- Create `apps/kittypaw/cli/cmd_kanban.go`
  - Owns `kittypaw kanban ...` task workflow commands.
- Create `apps/kittypaw/cli/cmd_kanban_test.go`
  - Owns Kanban task CLI tests.
- Modify `apps/kittypaw/cli/main.go`
  - Register `newProjectCmd()` and `newKanbanCmd()` with the root command.
- Modify `apps/kittypaw/store/store_test.go`
  - Update migration count from 23 to 24.

## Task 1: Schema And Migration Baseline

**Files:**
- Create: `apps/kittypaw/store/migrations/023_kanban.sql`
- Modify: `apps/kittypaw/store/store_test.go`
- Test: `apps/kittypaw/store/kanban_test.go`

- [ ] **Step 1: Write the failing schema test**

Create `apps/kittypaw/store/kanban_test.go`:

```go
package store

import "testing"

func TestKanbanMigrationCreatesTables(t *testing.T) {
	st := openTestStore(t)

	for _, table := range []string{
		"kanban_projects",
		"kanban_boards",
		"kanban_milestones",
		"kanban_tasks",
		"kanban_task_links",
		"kanban_task_comments",
		"kanban_task_events",
		"kanban_task_runs",
	} {
		var count int
		if err := st.db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table,
		).Scan(&count); err != nil {
			t.Fatalf("query table %s: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("table %s count = %d, want 1", table, count)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
cd apps/kittypaw
go test ./store -run 'TestKanbanMigrationCreatesTables|TestOpenAndMigrate' -count=1
```

Expected: `TestKanbanMigrationCreatesTables` fails because tables do not exist.

- [ ] **Step 3: Add migration**

Create `apps/kittypaw/store/migrations/023_kanban.sql` with these tables and indexes:

```sql
CREATE TABLE IF NOT EXISTS kanban_projects (
    id TEXT PRIMARY KEY,
    slug TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    root_path TEXT NOT NULL,
    archived INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS kanban_boards (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES kanban_projects(id) ON DELETE CASCADE,
    slug TEXT NOT NULL,
    name TEXT NOT NULL,
    is_default INTEGER NOT NULL DEFAULT 0,
    archived INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(project_id, slug)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_kanban_boards_default
    ON kanban_boards(project_id)
    WHERE is_default = 1;

CREATE TABLE IF NOT EXISTS kanban_milestones (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES kanban_projects(id) ON DELETE CASCADE,
    slug TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'open',
    target_date TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(project_id, slug)
);

CREATE INDEX IF NOT EXISTS idx_kanban_milestones_project
    ON kanban_milestones(project_id, status);

CREATE TABLE IF NOT EXISTS kanban_tasks (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES kanban_projects(id) ON DELETE CASCADE,
    board_id TEXT NOT NULL REFERENCES kanban_boards(id) ON DELETE RESTRICT,
    milestone_id TEXT REFERENCES kanban_milestones(id) ON DELETE SET NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    priority INTEGER NOT NULL DEFAULT 0,
    assignee TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    completed_at TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_kanban_tasks_project_status
    ON kanban_tasks(project_id, status, priority DESC, created_at);

CREATE INDEX IF NOT EXISTS idx_kanban_tasks_board_status
    ON kanban_tasks(board_id, status, priority DESC, created_at);

CREATE INDEX IF NOT EXISTS idx_kanban_tasks_milestone
    ON kanban_tasks(milestone_id, status);

CREATE TABLE IF NOT EXISTS kanban_task_links (
    parent_id TEXT NOT NULL REFERENCES kanban_tasks(id) ON DELETE CASCADE,
    child_id TEXT NOT NULL REFERENCES kanban_tasks(id) ON DELETE CASCADE,
    link_type TEXT NOT NULL DEFAULT 'blocks',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY(parent_id, child_id, link_type)
);

CREATE INDEX IF NOT EXISTS idx_kanban_task_links_child
    ON kanban_task_links(child_id, link_type);

CREATE TABLE IF NOT EXISTS kanban_task_comments (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES kanban_tasks(id) ON DELETE CASCADE,
    author TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_kanban_task_comments_task
    ON kanban_task_comments(task_id, created_at);

CREATE TABLE IF NOT EXISTS kanban_task_events (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES kanban_tasks(id) ON DELETE CASCADE,
    actor TEXT NOT NULL DEFAULT '',
    event_type TEXT NOT NULL,
    detail TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_kanban_task_events_task
    ON kanban_task_events(task_id, created_at);

CREATE TABLE IF NOT EXISTS kanban_task_runs (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES kanban_tasks(id) ON DELETE CASCADE,
    actor TEXT NOT NULL DEFAULT '',
    work_dir TEXT NOT NULL,
    work_dir_provider TEXT NOT NULL,
    outcome TEXT NOT NULL,
    summary TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    error TEXT NOT NULL DEFAULT '',
    started_at TEXT NOT NULL DEFAULT (datetime('now')),
    heartbeat_at TEXT NOT NULL DEFAULT (datetime('now')),
    finished_at TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_kanban_task_runs_task
    ON kanban_task_runs(task_id, started_at);
```

Modify `apps/kittypaw/store/store_test.go`:

```go
if count != 24 {
	t.Fatalf("expected 24 migrations, got %d", count)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run:

```bash
cd apps/kittypaw
go test ./store -run 'TestKanbanMigrationCreatesTables|TestOpenAndMigrate' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/kittypaw/store/migrations/023_kanban.sql apps/kittypaw/store/store_test.go apps/kittypaw/store/kanban_test.go
git commit -m "feat(store): add kanban schema"
```

## Task 2: Project, Board, And Milestone Store Kernel

**Files:**
- Modify: `apps/kittypaw/store/kanban.go`
- Modify: `apps/kittypaw/store/kanban_test.go`

- [ ] **Step 1: Add failing store tests**

Append these tests to `apps/kittypaw/store/kanban_test.go`:

```go
func TestKanbanCreateProjectCreatesDefaultBoard(t *testing.T) {
	st := openTestStore(t)

	project, err := st.CreateKanbanProject(CreateKanbanProjectRequest{
		Slug:     "kitty",
		Name:     "KittyPaw",
		RootPath: "/repo/kitty",
	})
	if err != nil {
		t.Fatalf("CreateKanbanProject: %v", err)
	}
	if project.ID == "" || project.Slug != "kitty" || project.Name != "KittyPaw" || project.RootPath != "/repo/kitty" {
		t.Fatalf("project = %+v", project)
	}

	boards, err := st.ListKanbanBoards(project.ID)
	if err != nil {
		t.Fatalf("ListKanbanBoards: %v", err)
	}
	if len(boards) != 1 {
		t.Fatalf("boards len = %d, want 1", len(boards))
	}
	if !boards[0].IsDefault || boards[0].Slug != "default" || boards[0].ProjectID != project.ID {
		t.Fatalf("default board = %+v", boards[0])
	}
}

func TestKanbanMilestoneBelongsToProject(t *testing.T) {
	st := openTestStore(t)
	project, err := st.CreateKanbanProject(CreateKanbanProjectRequest{
		Slug: "kitty", Name: "KittyPaw", RootPath: "/repo/kitty",
	})
	if err != nil {
		t.Fatalf("CreateKanbanProject: %v", err)
	}

	ms, err := st.CreateKanbanMilestone(CreateKanbanMilestoneRequest{
		ProjectID:  project.ID,
		Title:      "Kanban MVP",
		TargetDate: "2026-05-31",
	})
	if err != nil {
		t.Fatalf("CreateKanbanMilestone: %v", err)
	}
	if ms.ID == "" || ms.Slug != "kanban-mvp" || ms.ProjectID != project.ID || ms.Status != "open" {
		t.Fatalf("milestone = %+v", ms)
	}

	milestones, err := st.ListKanbanMilestones(project.ID)
	if err != nil {
		t.Fatalf("ListKanbanMilestones: %v", err)
	}
	if len(milestones) != 1 || milestones[0].ID != ms.ID {
		t.Fatalf("milestones = %+v", milestones)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
cd apps/kittypaw
go test ./store -run 'TestKanban(CreateProjectCreatesDefaultBoard|MilestoneBelongsToProject)' -count=1
```

Expected: compile fails because request types and methods do not exist.

- [ ] **Step 3: Implement project, board, milestone methods**

Create `apps/kittypaw/store/kanban.go` with:

```go
package store

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

type KanbanProject struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	RootPath  string `json:"root_path"`
	Archived  bool   `json:"archived"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type KanbanBoard struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default"`
	Archived  bool   `json:"archived"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type KanbanMilestone struct {
	ID          string `json:"id"`
	ProjectID   string `json:"project_id"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
	TargetDate  string `json:"target_date"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type CreateKanbanProjectRequest struct {
	Slug     string
	Name     string
	RootPath string
}

type CreateKanbanMilestoneRequest struct {
	ProjectID   string
	Title       string
	Description string
	TargetDate  string
}

var kanbanSlugUnsafe = regexp.MustCompile(`[^a-z0-9]+`)

func newKanbanID(prefix string) string {
	return prefix + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
}

func kanbanSlug(input string) string {
	s := strings.ToLower(strings.TrimSpace(input))
	s = kanbanSlugUnsafe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "item"
	}
	return s
}

func (s *Store) CreateKanbanProject(req CreateKanbanProjectRequest) (*KanbanProject, error) {
	slug := kanbanSlug(req.Slug)
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = slug
	}
	rootPath := strings.TrimSpace(req.RootPath)
	if rootPath == "" {
		return nil, fmt.Errorf("root path is required")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	projectID := newKanbanID("prj_")
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	if _, err := tx.Exec(`
		INSERT INTO kanban_projects (id, slug, name, root_path, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		projectID, slug, name, rootPath, now, now); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`
		INSERT INTO kanban_boards (id, project_id, slug, name, is_default, created_at, updated_at)
		VALUES (?, ?, 'default', 'Default', 1, ?, ?)`,
		newKanbanID("brd_"), projectID, now, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetKanbanProject(projectID)
}

func (s *Store) GetKanbanProject(idOrSlug string) (*KanbanProject, error) {
	var p KanbanProject
	err := s.db.QueryRow(`
		SELECT id, slug, name, root_path, archived, created_at, updated_at
		FROM kanban_projects
		WHERE id = ? OR slug = ?`, idOrSlug, idOrSlug).
		Scan(&p.ID, &p.Slug, &p.Name, &p.RootPath, &p.Archived, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Store) ListKanbanProjects(includeArchived bool) ([]KanbanProject, error) {
	query := `SELECT id, slug, name, root_path, archived, created_at, updated_at FROM kanban_projects`
	if !includeArchived {
		query += ` WHERE archived = 0`
	}
	query += ` ORDER BY created_at`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KanbanProject
	for rows.Next() {
		var p KanbanProject
		if err := rows.Scan(&p.ID, &p.Slug, &p.Name, &p.RootPath, &p.Archived, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetDefaultKanbanBoard(projectID string) (*KanbanBoard, error) {
	return s.scanKanbanBoard(s.db.QueryRow(`
		SELECT id, project_id, slug, name, is_default, archived, created_at, updated_at
		FROM kanban_boards WHERE project_id = ? AND is_default = 1`, projectID))
}

func (s *Store) GetKanbanBoard(projectID, idOrSlug string) (*KanbanBoard, error) {
	return s.scanKanbanBoard(s.db.QueryRow(`
		SELECT id, project_id, slug, name, is_default, archived, created_at, updated_at
		FROM kanban_boards
		WHERE project_id = ? AND (id = ? OR slug = ?)`, projectID, idOrSlug, idOrSlug))
}

func (s *Store) ListKanbanBoards(projectID string) ([]KanbanBoard, error) {
	rows, err := s.db.Query(`
		SELECT id, project_id, slug, name, is_default, archived, created_at, updated_at
		FROM kanban_boards WHERE project_id = ? AND archived = 0 ORDER BY is_default DESC, created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KanbanBoard
	for rows.Next() {
		var b KanbanBoard
		if err := rows.Scan(&b.ID, &b.ProjectID, &b.Slug, &b.Name, &b.IsDefault, &b.Archived, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) scanKanbanBoard(row *sql.Row) (*KanbanBoard, error) {
	var b KanbanBoard
	if err := row.Scan(&b.ID, &b.ProjectID, &b.Slug, &b.Name, &b.IsDefault, &b.Archived, &b.CreatedAt, &b.UpdatedAt); err != nil {
		return nil, err
	}
	return &b, nil
}

func (s *Store) CreateKanbanMilestone(req CreateKanbanMilestoneRequest) (*KanbanMilestone, error) {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return nil, fmt.Errorf("title is required")
	}
	id := newKanbanID("ms_")
	slug := kanbanSlug(title)
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	if _, err := s.db.Exec(`
		INSERT INTO kanban_milestones (id, project_id, slug, title, description, status, target_date, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'open', ?, ?, ?)`,
		id, req.ProjectID, slug, title, req.Description, req.TargetDate, now, now); err != nil {
		return nil, err
	}
	return s.GetKanbanMilestone(req.ProjectID, id)
}

func (s *Store) GetKanbanMilestone(projectID, idOrSlug string) (*KanbanMilestone, error) {
	var m KanbanMilestone
	err := s.db.QueryRow(`
		SELECT id, project_id, slug, title, description, status, target_date, created_at, updated_at
		FROM kanban_milestones
		WHERE project_id = ? AND (id = ? OR slug = ?)`, projectID, idOrSlug, idOrSlug).
		Scan(&m.ID, &m.ProjectID, &m.Slug, &m.Title, &m.Description, &m.Status, &m.TargetDate, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *Store) ListKanbanMilestones(projectID string) ([]KanbanMilestone, error) {
	rows, err := s.db.Query(`
		SELECT id, project_id, slug, title, description, status, target_date, created_at, updated_at
		FROM kanban_milestones WHERE project_id = ? ORDER BY created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KanbanMilestone
	for rows.Next() {
		var m KanbanMilestone
		if err := rows.Scan(&m.ID, &m.ProjectID, &m.Slug, &m.Title, &m.Description, &m.Status, &m.TargetDate, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:

```bash
cd apps/kittypaw
go test ./store -run 'TestKanban(CreateProjectCreatesDefaultBoard|MilestoneBelongsToProject)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/kittypaw/store/kanban.go apps/kittypaw/store/kanban_test.go
git commit -m "feat(store): add kanban project kernel"
```

## Task 3: Task, Run, Comment, Link Store Kernel

**Files:**
- Modify: `apps/kittypaw/store/kanban.go`
- Modify: `apps/kittypaw/store/kanban_test.go`

- [ ] **Step 1: Add failing task workflow tests**

Append tests covering:

```go
func TestKanbanTaskClaimCompleteRecordsRun(t *testing.T) {
	st := openTestStore(t)
	project, _ := st.CreateKanbanProject(CreateKanbanProjectRequest{Slug: "kitty", Name: "KittyPaw", RootPath: "/repo/kitty"})
	task, err := st.CreateKanbanTask(CreateKanbanTaskRequest{
		ProjectID: project.ID,
		Title:     "Add task runs",
		Status:    KanbanStatusTodo,
	})
	if err != nil {
		t.Fatalf("CreateKanbanTask: %v", err)
	}

	run, err := st.ClaimKanbanTask(task.ID, ClaimKanbanTaskRequest{Actor: "alice"})
	if err != nil {
		t.Fatalf("ClaimKanbanTask: %v", err)
	}
	if run.WorkDir != "/repo/kitty" || run.WorkDirProvider != KanbanWorkDirProjectRoot || run.Outcome != KanbanRunRunning {
		t.Fatalf("run = %+v", run)
	}

	if err := st.CompleteKanbanTask(task.ID, CompleteKanbanTaskRequest{Actor: "alice", Summary: "done", MetadataJSON: `{"tests":1}`}); err != nil {
		t.Fatalf("CompleteKanbanTask: %v", err)
	}
	got, err := st.GetKanbanTask(task.ID)
	if err != nil {
		t.Fatalf("GetKanbanTask: %v", err)
	}
	if got.Status != KanbanStatusDone || got.CompletedAt == "" {
		t.Fatalf("task after complete = %+v", got)
	}
	runs, err := st.ListKanbanRuns(task.ID)
	if err != nil {
		t.Fatalf("ListKanbanRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].Outcome != KanbanRunCompleted || runs[0].Summary != "done" || runs[0].MetadataJSON != `{"tests":1}` {
		t.Fatalf("runs = %+v", runs)
	}
}
```

Append these additional tests:

```go
func TestKanbanBlockUnblockAndComment(t *testing.T) {
	st := openTestStore(t)
	project, _ := st.CreateKanbanProject(CreateKanbanProjectRequest{Slug: "kitty", Name: "KittyPaw", RootPath: "/repo/kitty"})
	task, err := st.CreateKanbanTask(CreateKanbanTaskRequest{ProjectID: project.ID, Title: "Clarify API", Status: KanbanStatusTodo})
	if err != nil {
		t.Fatalf("CreateKanbanTask: %v", err)
	}

	if err := st.BlockKanbanTask(task.ID, BlockKanbanTaskRequest{Actor: "alice", Reason: "Need API shape"}); err != nil {
		t.Fatalf("BlockKanbanTask: %v", err)
	}
	blocked, _ := st.GetKanbanTask(task.ID)
	if blocked.Status != KanbanStatusBlocked {
		t.Fatalf("blocked status = %q", blocked.Status)
	}

	comment, err := st.AddKanbanTaskComment(task.ID, "alice", "Use /api/v1/kanban/tasks.")
	if err != nil {
		t.Fatalf("AddKanbanTaskComment: %v", err)
	}
	if comment.ID == "" || comment.Body == "" {
		t.Fatalf("comment = %+v", comment)
	}

	if err := st.UnblockKanbanTask(task.ID, UnblockKanbanTaskRequest{Actor: "bob", Comment: "API shape decided"}); err != nil {
		t.Fatalf("UnblockKanbanTask: %v", err)
	}
	unblocked, _ := st.GetKanbanTask(task.ID)
	if unblocked.Status != KanbanStatusTodo {
		t.Fatalf("unblocked status = %q", unblocked.Status)
	}
}

func TestKanbanDependencyRejectsCycleAndPromotesChild(t *testing.T) {
	st := openTestStore(t)
	project, _ := st.CreateKanbanProject(CreateKanbanProjectRequest{Slug: "kitty", Name: "KittyPaw", RootPath: "/repo/kitty"})
	parent, err := st.CreateKanbanTask(CreateKanbanTaskRequest{ProjectID: project.ID, Title: "Schema", Status: KanbanStatusTodo})
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}
	child, err := st.CreateKanbanTask(CreateKanbanTaskRequest{ProjectID: project.ID, Title: "CLI", Status: KanbanStatusTodo})
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}

	if err := st.LinkKanbanTasks(parent.ID, child.ID); err != nil {
		t.Fatalf("LinkKanbanTasks parent->child: %v", err)
	}
	if err := st.LinkKanbanTasks(child.ID, parent.ID); err == nil {
		t.Fatal("expected cycle rejection")
	}

	if _, err := st.ClaimKanbanTask(parent.ID, ClaimKanbanTaskRequest{Actor: "alice"}); err != nil {
		t.Fatalf("Claim parent: %v", err)
	}
	if err := st.CompleteKanbanTask(parent.ID, CompleteKanbanTaskRequest{Actor: "alice", Summary: "schema done"}); err != nil {
		t.Fatalf("Complete parent: %v", err)
	}
	promoted, err := st.GetKanbanTask(child.ID)
	if err != nil {
		t.Fatalf("Get child: %v", err)
	}
	if promoted.Status != KanbanStatusReady {
		t.Fatalf("child status = %q, want %q", promoted.Status, KanbanStatusReady)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
cd apps/kittypaw
go test ./store -run 'TestKanban(Task|Dependency|Comment|Block)' -count=1
```

Expected: compile fails because task methods do not exist.

- [ ] **Step 3: Implement task kernel**

Add DTOs, constants, and methods to `apps/kittypaw/store/kanban.go`:

- `KanbanTask`
- `KanbanRun`
- `KanbanComment`
- `KanbanEvent`
- `CreateKanbanTaskRequest`
- `ClaimKanbanTaskRequest`
- `CompleteKanbanTaskRequest`
- `BlockKanbanTaskRequest`
- `UnblockKanbanTaskRequest`
- `KanbanTaskListFilter`
- constants for statuses, run outcomes, and work-dir providers.

Required methods:

- `CreateKanbanTask`
- `GetKanbanTask`
- `ListKanbanTasks`
- `ClaimKanbanTask`
- `CompleteKanbanTask`
- `BlockKanbanTask`
- `UnblockKanbanTask`
- `AddKanbanTaskComment`
- `ListKanbanComments`
- `ListKanbanEvents`
- `ListKanbanRuns`
- `LinkKanbanTasks`
- `hasKanbanPath`
- `promoteUnblockedChildren`
- `recordKanbanEventTx`

- [ ] **Step 4: Run store tests**

Run:

```bash
cd apps/kittypaw
go test ./store -run 'TestKanban' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/kittypaw/store/kanban.go apps/kittypaw/store/kanban_test.go
git commit -m "feat(store): add kanban task workflow"
```

## Task 4: Project CLI With Board And Milestone Subcommands

**Files:**
- Create: `apps/kittypaw/cli/cmd_project.go`
- Create: `apps/kittypaw/cli/cmd_project_test.go`
- Modify: `apps/kittypaw/cli/main.go`

- [ ] **Step 1: Write failing CLI tests**

Create `apps/kittypaw/cli/cmd_project_test.go` with tests that:

- `newRootCmd()` exposes `project`.
- `project` exposes `create`, `list`, `show`, `board`, and `milestone`.
- `project board list <project>` exists.
- `project milestone create <project> <title>` exists.
- `project milestone list <project>` exists.

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
cd apps/kittypaw
go test ./cli -run 'TestProjectCommand' -count=1
```

Expected: FAIL because `project` command is not registered.

- [ ] **Step 3: Implement Project CLI**

Create `cmd_project.go` with `newProjectCmd`, `newProjectCreateCmd`, `newProjectListCmd`, `newProjectShowCmd`, `newProjectBoardCmd`, and `newProjectMilestoneCmd`. Commands open the local account store by resolving `--account` / `KITTYPAW_ACCOUNT`, then call store methods.

Register in `newRootCmd()`:

```go
newProjectCmd(),
```

- [ ] **Step 4: Run CLI tests**

Run:

```bash
cd apps/kittypaw
go test ./cli -run 'TestProjectCommand' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/kittypaw/cli/main.go apps/kittypaw/cli/cmd_project.go apps/kittypaw/cli/cmd_project_test.go
git commit -m "feat(cli): add project kanban commands"
```

## Task 5: Kanban Task CLI

**Files:**
- Create: `apps/kittypaw/cli/cmd_kanban.go`
- Create: `apps/kittypaw/cli/cmd_kanban_test.go`
- Modify: `apps/kittypaw/cli/main.go`

- [ ] **Step 1: Write failing CLI command shape tests**

Create `apps/kittypaw/cli/cmd_kanban_test.go` with tests that:

- `newRootCmd()` exposes `kanban`.
- `kanban` exposes `create`, `list`, `show`, `claim`, `complete`, `block`, `unblock`, `comment`, `link`, and `runs`.
- `kanban create` has `--project`, `--board`, `--milestone`, `--body`, and `--assignee`.
- `kanban complete` has `--summary` and `--metadata`.
- no top-level `board` or `milestone` command exists.

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
cd apps/kittypaw
go test ./cli -run 'TestKanbanCommand' -count=1
```

Expected: FAIL because `kanban` command is not registered.

- [ ] **Step 3: Implement Kanban CLI**

Create `cmd_kanban.go` with command constructors and small run helpers. Resolve project slug to project ID, board slug to board ID, and milestone slug to milestone ID before calling store methods.

Register in `newRootCmd()`:

```go
newKanbanCmd(),
```

- [ ] **Step 4: Run CLI tests**

Run:

```bash
cd apps/kittypaw
go test ./cli -run 'TestKanbanCommand' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/kittypaw/cli/main.go apps/kittypaw/cli/cmd_kanban.go apps/kittypaw/cli/cmd_kanban_test.go
git commit -m "feat(cli): add kanban task commands"
```

## Task 6: Verification

**Files:**
- All files changed in previous tasks.

- [ ] **Step 1: Format**

Run:

```bash
cd apps/kittypaw
gofmt -w store/kanban.go store/kanban_test.go cli/cmd_project.go cli/cmd_project_test.go cli/cmd_kanban.go cli/cmd_kanban_test.go cli/main.go
```

- [ ] **Step 2: Run focused tests**

Run:

```bash
cd apps/kittypaw
go test ./store ./cli -run 'TestKanban|TestProjectCommand' -count=1
```

Expected: PASS.

- [ ] **Step 3: Run app short tests**

Run:

```bash
cd apps/kittypaw
go test ./... -short -count=1
```

Expected: PASS.

- [ ] **Step 4: Final status**

Run:

```bash
git status --short
```

Expected: only intentional changes are present. Do not commit `go.work.sum` checksum churn unless implementation actually requires it.
