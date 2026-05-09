# Project Job Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Approved project Jobs can run through a one-shot Driver in a per-job git worktree, record bounded logs, and move the owning Ticket to the next reviewable state.

**Architecture:** Keep the source of truth in the local SQLite store, and put all process/git orchestration behind a single `engine.ProjectJobRuntime` used by both API handlers and the `Projects` chat tool. API and UI surfaces become thin callers of the same runtime, so chat-triggered and button-triggered execution share status transitions, git safety checks, cancellation, and log bounds.

**Tech Stack:** Go, SQLite migrations, `os/exec`, git worktrees, existing `store`, `engine`, `server`, and static web UI files.

---

## File Map

- Create: `store/migrations/028_project_job_runtime.sql`
  - Adds durable runtime fields and the one-running-job guard.
- Modify: `store/projects.go`
  - Adds `Job.ExitCode`, job list/filter APIs, lifecycle transition methods, bounded log updates, and startup recovery.
- Modify: `store/projects_test.go`
  - Adds deterministic store lifecycle tests.
- Modify: `store/store_test.go`
  - Bumps migration count from `28` to `29`.
- Create: `engine/project_job_runtime.go`
  - Owns git readiness, worktree creation, driver command construction, async start, cancellation, and finish transitions.
- Create: `engine/project_job_runtime_test.go`
  - Tests git safety and fake driver execution without invoking Codex or Claude.
- Modify: `engine/session.go`
  - Adds the shared `ProjectJobRuntime` dependency.
- Modify: `engine/executor.go`
  - Adds `Projects.initProjectGit`, `Projects.startJob`, and `Projects.jobLogs`.
- Modify: `engine/projects_scope_test.go`
  - Adds tool-path coverage for the new Projects runtime calls.
- Modify: `server/account_deps.go`
  - Constructs and closes the account runtime; marks interrupted Jobs failed on startup.
- Modify: `server/account_config.go`
  - Preserves or rebuilds the runtime when account config is reloaded.
- Modify: `server/api_projects.go`
  - Adds git init, start, cancel, logs, and job list handlers against the runtime.
- Modify: `server/server.go`
  - Adds `POST /api/v1/projects/{project}/git/init`.
- Modify: `server/api_projects_test.go`
  - Adds API tests with a fake command runner.
- Modify: `server/web/projects.js`
  - Adds Job detail controls, start/cancel/log polling, and git init prompt flow.
- Modify: `server/web_projects_test.go`
  - Adds static web behavior tests for the new controls where existing harness supports it.
- Modify: `TASKS.md` and `../../TASKS.md`
  - Moves Phase 1.5 from review gate into implementation progress after the first code commit.

---

## Task 1: Store Runtime Schema And Lifecycle

**Files:**
- Create: `store/migrations/028_project_job_runtime.sql`
- Modify: `store/projects.go`
- Modify: `store/projects_test.go`
- Modify: `store/store_test.go`

- [ ] **Step 1: Write store migration tests first**

Add these tests to `store/projects_test.go` near the current Job tests:

```go
func TestProjectJobRuntimeSchemaAddsExitCodeAndRunningGuard(t *testing.T) {
	st := openTestStore(t)
	project := createProjectForProjectsTest(t, st, "kitty")
	ticket, err := st.CreateTicket(CreateTicketRequest{ProjectID: project.ID, Title: "Run me"})
	if err != nil {
		t.Fatalf("CreateTicket() error = %v", err)
	}
	if err := st.EnsureDefaultDrivers(); err != nil {
		t.Fatalf("EnsureDefaultDrivers() error = %v", err)
	}
	first := planApprovedJobForProjectsTest(t, st, project.ID, ticket.ID, "First")
	second := planApprovedJobForProjectsTest(t, st, project.ID, ticket.ID, "Second")

	started, err := st.StartJob(first.ID, StartJobRequest{
		ActorID:      "pm",
		WorktreePath: "/tmp/kittypaw/job-1",
		BranchName:   "kittypaw/KITTY-001/job-1",
	})
	if err != nil {
		t.Fatalf("StartJob(first) error = %v", err)
	}
	if started.Status != JobStatusRunning || started.ExitCode != 0 {
		t.Fatalf("started first = %+v, want running exit_code 0", started)
	}
	if _, err := st.StartJob(second.ID, StartJobRequest{
		ActorID:      "pm",
		WorktreePath: "/tmp/kittypaw/job-2",
		BranchName:   "kittypaw/KITTY-001/job-2",
	}); !IsProjectJobError(err, ProjectJobErrTicketHasRunningJob) {
		t.Fatalf("StartJob(second) error = %v, want %s", err, ProjectJobErrTicketHasRunningJob)
	}
}

func TestProjectJobLifecycleMovesTicketByOutcome(t *testing.T) {
	st := openTestStore(t)
	project := createProjectForProjectsTest(t, st, "kitty")
	ticket, err := st.CreateTicket(CreateTicketRequest{ProjectID: project.ID, Title: "Lifecycle"})
	if err != nil {
		t.Fatalf("CreateTicket() error = %v", err)
	}
	if err := st.EnsureDefaultDrivers(); err != nil {
		t.Fatalf("EnsureDefaultDrivers() error = %v", err)
	}

	success := planApprovedJobForProjectsTest(t, st, project.ID, ticket.ID, "Success")
	if _, err := st.StartJob(success.ID, StartJobRequest{ActorID: "pm", WorktreePath: "/tmp/success", BranchName: "kittypaw/KITTY-001/success"}); err != nil {
		t.Fatalf("StartJob(success) error = %v", err)
	}
	done, err := st.SucceedJob(success.ID, FinishJobRequest{
		ActorID:       "runtime",
		ResultSummary: "implemented",
		LogTail:       "ok",
		ExitCode:      0,
		MetadataJSON:  `{"exit_code":0}`,
	})
	if err != nil {
		t.Fatalf("SucceedJob() error = %v", err)
	}
	if done.Status != JobStatusSucceeded || done.ExitCode != 0 {
		t.Fatalf("succeeded job = %+v", done)
	}
	ticket, err = st.GetTicket(ticket.ID)
	if err != nil {
		t.Fatalf("GetTicket(after success) error = %v", err)
	}
	if ticket.Status != TicketStatusReview {
		t.Fatalf("ticket status after success = %q, want review", ticket.Status)
	}

	failure := planApprovedJobForProjectsTest(t, st, project.ID, ticket.ID, "Failure")
	if _, err := st.StartJob(failure.ID, StartJobRequest{ActorID: "pm", WorktreePath: "/tmp/failure", BranchName: "kittypaw/KITTY-001/failure"}); err != nil {
		t.Fatalf("StartJob(failure) error = %v", err)
	}
	failed, err := st.FailJob(failure.ID, FinishJobRequest{
		ActorID:       "runtime",
		ResultSummary: "failed",
		LogTail:       "bad",
		ErrorExcerpt:  "exit status 2",
		ExitCode:      2,
		MetadataJSON:  `{"exit_code":2}`,
	})
	if err != nil {
		t.Fatalf("FailJob() error = %v", err)
	}
	if failed.Status != JobStatusFailed || failed.ExitCode != 2 {
		t.Fatalf("failed job = %+v", failed)
	}
	ticket, err = st.GetTicket(ticket.ID)
	if err != nil {
		t.Fatalf("GetTicket(after failure) error = %v", err)
	}
	if ticket.Status != TicketStatusBlocked {
		t.Fatalf("ticket status after failure = %q, want blocked", ticket.Status)
	}
}

func TestCancelRunningProjectJobMovesTicketBacklog(t *testing.T) {
	st := openTestStore(t)
	project := createProjectForProjectsTest(t, st, "kitty")
	ticket, err := st.CreateTicket(CreateTicketRequest{ProjectID: project.ID, Title: "Cancel"})
	if err != nil {
		t.Fatalf("CreateTicket() error = %v", err)
	}
	if err := st.EnsureDefaultDrivers(); err != nil {
		t.Fatalf("EnsureDefaultDrivers() error = %v", err)
	}
	job := planApprovedJobForProjectsTest(t, st, project.ID, ticket.ID, "Cancel")
	if _, err := st.StartJob(job.ID, StartJobRequest{ActorID: "pm", WorktreePath: "/tmp/cancel", BranchName: "kittypaw/KITTY-001/cancel"}); err != nil {
		t.Fatalf("StartJob() error = %v", err)
	}
	canceled, err := st.CancelJob(job.ID, "alice", "stop requested")
	if err != nil {
		t.Fatalf("CancelJob() error = %v", err)
	}
	if canceled.Status != JobStatusCanceled || canceled.FinishedAt == "" {
		t.Fatalf("canceled = %+v", canceled)
	}
	ticket, err = st.GetTicket(ticket.ID)
	if err != nil {
		t.Fatalf("GetTicket() error = %v", err)
	}
	if ticket.Status != TicketStatusBacklog {
		t.Fatalf("ticket status = %q, want backlog", ticket.Status)
	}
}

func TestMarkRunningProjectJobsFailedOnStartup(t *testing.T) {
	st := openTestStore(t)
	project := createProjectForProjectsTest(t, st, "kitty")
	ticket, err := st.CreateTicket(CreateTicketRequest{ProjectID: project.ID, Title: "Interrupted"})
	if err != nil {
		t.Fatalf("CreateTicket() error = %v", err)
	}
	if err := st.EnsureDefaultDrivers(); err != nil {
		t.Fatalf("EnsureDefaultDrivers() error = %v", err)
	}
	job := planApprovedJobForProjectsTest(t, st, project.ID, ticket.ID, "Interrupted")
	if _, err := st.StartJob(job.ID, StartJobRequest{ActorID: "pm", WorktreePath: "/tmp/interrupted", BranchName: "kittypaw/KITTY-001/interrupted"}); err != nil {
		t.Fatalf("StartJob() error = %v", err)
	}
	count, err := st.MarkRunningJobsFailedOnStartup("daemon stopped while the job was running")
	if err != nil {
		t.Fatalf("MarkRunningJobsFailedOnStartup() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
	got, err := st.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if got.Status != JobStatusFailed || !strings.Contains(got.ErrorExcerpt, "daemon stopped") {
		t.Fatalf("job after startup recovery = %+v", got)
	}
}
```

Add this helper below `createProjectForProjectsTest`:

```go
func planApprovedJobForProjectsTest(t *testing.T, st *Store, projectID, ticketID, summary string) *Job {
	t.Helper()
	job, err := st.PlanJob(PlanJobRequest{
		ProjectID:     projectID,
		TicketID:      ticketID,
		DriverID:      "codex",
		Mode:          JobModeOneShot,
		PromptSummary: summary,
		PromptText:    "Run " + summary,
		CreatedBy:     "pm",
	})
	if err != nil {
		t.Fatalf("PlanJob(%s) error = %v", summary, err)
	}
	approved, err := st.ApproveJob(job.ID, "pm")
	if err != nil {
		t.Fatalf("ApproveJob(%s) error = %v", summary, err)
	}
	return approved
}
```

- [ ] **Step 2: Run store tests to verify failure**

Run:

```bash
go test ./store -run 'TestProjectJobRuntimeSchemaAddsExitCodeAndRunningGuard|TestProjectJobLifecycleMovesTicketByOutcome|TestCancelRunningProjectJobMovesTicketBacklog|TestMarkRunningProjectJobsFailedOnStartup' -count=1
```

Expected: FAIL with undefined `StartJobRequest`, `FinishJobRequest`, `IsProjectJobError`, and missing `ExitCode`.

- [ ] **Step 3: Add migration**

Create `store/migrations/028_project_job_runtime.sql`:

```sql
ALTER TABLE jobs ADD COLUMN exit_code INTEGER NOT NULL DEFAULT 0;

CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_one_running_per_ticket
    ON jobs(ticket_id)
    WHERE status = 'running';
```

In `store/store_test.go`, change:

```go
if count != 29 {
	t.Fatalf("expected 29 migrations, got %d", count)
}
```

- [ ] **Step 4: Add store runtime types and scan `exit_code`**

In `store/projects.go`, add constants:

```go
const (
	ProjectJobErrJobNotApproved       = "job_not_approved"
	ProjectJobErrJobAlreadyStarted    = "job_already_started"
	ProjectJobErrTicketHasRunningJob  = "ticket_has_running_job"
	ProjectJobErrDriverModeUnsupported = "driver_mode_unsupported"
)
```

Add the error type near the Project structs:

```go
type ProjectJobError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *ProjectJobError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	return e.Code
}

func projectJobError(code, message string) error {
	return &ProjectJobError{Code: code, Message: message}
}

func IsProjectJobError(err error, code string) bool {
	var jobErr *ProjectJobError
	return errors.As(err, &jobErr) && jobErr.Code == code
}
```

Add to `Job`:

```go
ExitCode int `json:"exit_code"`
```

Update `GetJob` and `scanJob` so the SELECT includes `exit_code` between `log_truncated` and `driver_snapshot_json`:

```sql
result_summary, log_tail, error_excerpt, log_truncated, exit_code,
driver_snapshot_json, created_by, approved_by, started_at,
```

and scan into:

```go
&job.ResultSummary, &job.LogTail, &job.ErrorExcerpt, &logTruncated, &job.ExitCode,
```

- [ ] **Step 5: Implement lifecycle store methods**

Add these request types near `PlanJobRequest`:

```go
type JobListFilter struct {
	ProjectID string
	TicketID  string
	Status    string
}

type StartJobRequest struct {
	ActorID      string
	WorktreePath string
	BranchName   string
	MetadataJSON string
}

type FinishJobRequest struct {
	ActorID       string
	ResultSummary string
	LogTail       string
	ErrorExcerpt  string
	LogTruncated  bool
	ExitCode      int
	MetadataJSON  string
}

type UpdateJobLogRequest struct {
	LogTail      string
	LogTruncated bool
}
```

Add these public methods with transaction-backed status updates:

```go
func (s *Store) ListJobs(filter JobListFilter) ([]Job, error)
func (s *Store) StartJob(jobID string, req StartJobRequest) (*Job, error)
func (s *Store) UpdateJobLog(jobID string, req UpdateJobLogRequest) (*Job, error)
func (s *Store) SucceedJob(jobID string, req FinishJobRequest) (*Job, error)
func (s *Store) FailJob(jobID string, req FinishJobRequest) (*Job, error)
func (s *Store) MarkRunningJobsFailedOnStartup(message string) (int, error)
```

`StartJob` must:

```go
// Inside BEGIN IMMEDIATE transaction:
// 1. SELECT the job.
// 2. Return job_not_approved when status == planned.
// 3. Return job_already_started when status is running/succeeded/failed/canceled.
// 4. SELECT COUNT(*) FROM jobs WHERE ticket_id = ? AND status = 'running' AND id <> ?.
// 5. Return ticket_has_running_job when count > 0.
// 6. UPDATE jobs SET status='running', worktree_path=?, branch_name=?, started_at=?, updated_at=?.
// 7. Move ticket to in_progress and insert ticket action.
// 8. Insert job_events type='started' with metadata.
```

`SucceedJob` must set `status='succeeded'`, `finished_at`, `result_summary`, `log_tail`, `error_excerpt=''`, `log_truncated`, `exit_code`, insert `succeeded`, and move the ticket to `review`.

`FailJob` must set `status='failed'`, `finished_at`, `result_summary`, `log_tail`, `error_excerpt`, `log_truncated`, `exit_code`, insert `failed`, and move the ticket to `blocked`.

`CancelJob` must continue to support planned/approved cancellation, but when canceling any Job it also moves the owning Ticket to `backlog`, sets `exit_code=-1` for running Jobs, and records `canceled`.

Add small internal helpers so lifecycle methods do not duplicate ticket-action SQL:

```go
func insertTicketActionTx(tx *sql.Tx, ticketID, actorID, actionType, fromStatus, toStatus, message, metadataJSON, now string) error
func moveTicketStatusTx(tx *sql.Tx, ticketID, actorID, status, message, now string) error
```

Keep the existing `MoveTicket` behavior by changing it to call `moveTicketStatusTx` inside its own transaction.

- [ ] **Step 6: Run store package**

Run:

```bash
go test ./store -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit store runtime foundation**

Run:

```bash
git add store/migrations/028_project_job_runtime.sql store/projects.go store/projects_test.go store/store_test.go
git commit -m "feat(projects): add job runtime store lifecycle"
```

Expected: commit succeeds.

---

## Task 2: Git Readiness And Worktree Runtime

**Files:**
- Create: `engine/project_job_runtime.go`
- Create: `engine/project_job_runtime_test.go`

- [ ] **Step 1: Write git readiness tests**

Create `engine/project_job_runtime_test.go` with these tests:

```go
package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/store"
)

func TestProjectJobRuntimeRequiresGitRepository(t *testing.T) {
	st := openProjectJobRuntimeStore(t)
	project := createRuntimeProject(t, st, t.TempDir())
	rt := NewProjectJobRuntime(ProjectJobRuntimeOptions{Store: st, AccountID: "alice", BaseDir: t.TempDir()})
	_, err := rt.prepareApprovedJob(context.Background(), planApprovedRuntimeJob(t, st, project.ID), StartProjectJobOptions{ActorID: "pm"})
	if !store.IsProjectJobError(err, ProjectJobErrProjectNotGitRepository) {
		t.Fatalf("prepareApprovedJob() error = %v, want %s", err, ProjectJobErrProjectNotGitRepository)
	}
}

func TestProjectJobRuntimeRequiresGitHead(t *testing.T) {
	st := openProjectJobRuntimeStore(t)
	root := t.TempDir()
	gitInit(t, root)
	project := createRuntimeProject(t, st, root)
	rt := NewProjectJobRuntime(ProjectJobRuntimeOptions{Store: st, AccountID: "alice", BaseDir: t.TempDir()})
	_, err := rt.prepareApprovedJob(context.Background(), planApprovedRuntimeJob(t, st, project.ID), StartProjectJobOptions{ActorID: "pm"})
	if !store.IsProjectJobError(err, ProjectJobErrProjectGitHeadMissing) {
		t.Fatalf("prepareApprovedJob() error = %v, want %s", err, ProjectJobErrProjectGitHeadMissing)
	}
}

func TestProjectJobRuntimeRejectsDirtyRoot(t *testing.T) {
	st := openProjectJobRuntimeStore(t)
	root := t.TempDir()
	gitInit(t, root)
	gitCommitFile(t, root, "README.md", "clean\n")
	if err := os.WriteFile(filepath.Join(root, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}
	project := createRuntimeProject(t, st, root)
	rt := NewProjectJobRuntime(ProjectJobRuntimeOptions{Store: st, AccountID: "alice", BaseDir: t.TempDir()})
	_, err := rt.prepareApprovedJob(context.Background(), planApprovedRuntimeJob(t, st, project.ID), StartProjectJobOptions{ActorID: "pm"})
	if !store.IsProjectJobError(err, ProjectJobErrProjectGitDirty) {
		t.Fatalf("prepareApprovedJob() error = %v, want %s", err, ProjectJobErrProjectGitDirty)
	}
}

func TestProjectJobRuntimeCreatesWorktreeForCleanGitRoot(t *testing.T) {
	st := openProjectJobRuntimeStore(t)
	root := t.TempDir()
	gitInit(t, root)
	gitCommitFile(t, root, "README.md", "clean\n")
	project := createRuntimeProject(t, st, root)
	job := planApprovedRuntimeJob(t, st, project.ID)
	baseDir := t.TempDir()
	rt := NewProjectJobRuntime(ProjectJobRuntimeOptions{Store: st, AccountID: "alice", BaseDir: baseDir})

	prepared, err := rt.prepareApprovedJob(context.Background(), job, StartProjectJobOptions{ActorID: "pm"})
	if err != nil {
		t.Fatalf("prepareApprovedJob() error = %v", err)
	}
	if !strings.Contains(prepared.WorktreePath, filepath.Join(baseDir, "worktrees", project.ID, job.TicketID, job.ID)) {
		t.Fatalf("worktree path = %q, want account managed path", prepared.WorktreePath)
	}
	if prepared.BranchName == "" || !strings.HasPrefix(prepared.BranchName, "kittypaw/") {
		t.Fatalf("branch name = %q, want kittypaw prefix", prepared.BranchName)
	}
	if _, err := os.Stat(filepath.Join(prepared.WorktreePath, "README.md")); err != nil {
		t.Fatalf("worktree README missing: %v", err)
	}
}
```

Add helpers in the same file:

```go
func openProjectJobRuntimeStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func createRuntimeProject(t *testing.T, st *store.Store, root string) *store.Project {
	t.Helper()
	project, err := st.CreateProject(store.CreateProjectRequest{Key: "KITTY", Name: "Kitty", RootPath: root})
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	return project
}

func planApprovedRuntimeJob(t *testing.T, st *store.Store, projectID string) *store.Job {
	t.Helper()
	if err := st.EnsureDefaultDrivers(); err != nil {
		t.Fatalf("EnsureDefaultDrivers() error = %v", err)
	}
	ticket, err := st.CreateTicket(store.CreateTicketRequest{ProjectID: projectID, Title: "Run driver"})
	if err != nil {
		t.Fatalf("CreateTicket() error = %v", err)
	}
	job, err := st.PlanJob(store.PlanJobRequest{
		ProjectID:     projectID,
		TicketID:      ticket.ID,
		DriverID:      "shell",
		Mode:          store.JobModeOneShot,
		PromptSummary: "Run driver",
		PromptText:    "echo ok",
		CreatedBy:     "pm",
	})
	if err != nil {
		t.Fatalf("PlanJob() error = %v", err)
	}
	approved, err := st.ApproveJob(job.ID, "pm")
	if err != nil {
		t.Fatalf("ApproveJob() error = %v", err)
	}
	return approved
}

func gitInit(t *testing.T, root string) {
	t.Helper()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "kittypaw@example.test")
	runGit(t, root, "config", "user.name", "KittyPaw Test")
}

func gitCommitFile(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	runGit(t, root, "add", name)
	runGit(t, root, "commit", "-m", "initial")
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}
```

- [ ] **Step 2: Run git readiness tests to verify failure**

Run:

```bash
go test ./engine -run 'TestProjectJobRuntimeRequiresGitRepository|TestProjectJobRuntimeRequiresGitHead|TestProjectJobRuntimeRejectsDirtyRoot|TestProjectJobRuntimeCreatesWorktreeForCleanGitRoot' -count=1
```

Expected: FAIL with undefined `ProjectJobRuntime`, `ProjectJobRuntimeOptions`, `StartProjectJobOptions`, and git error constants.

- [ ] **Step 3: Add runtime error constants**

In `store/projects.go`, add:

```go
const (
	ProjectJobErrProjectNotGitRepository = "project_not_git_repository"
	ProjectJobErrProjectGitHeadMissing   = "project_git_head_missing"
	ProjectJobErrProjectGitDirty         = "project_git_dirty"
	ProjectJobErrWorktreeCreateFailed    = "worktree_create_failed"
	ProjectJobErrDriverNotFound          = "driver_not_found"
	ProjectJobErrDriverProcessFailed     = "driver_process_failed"
)
```

- [ ] **Step 4: Implement git status and worktree creation**

Create `engine/project_job_runtime.go`:

```go
package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jinto/kittypaw/store"
)

const (
	projectJobLogTailLimit      = 64 * 1024
	projectJobEventLogLimit     = 8 * 1024
	projectJobErrorExcerptLimit = 4 * 1024
)

type ProjectJobRuntimeOptions struct {
	Store     *store.Store
	AccountID string
	BaseDir   string
	Runner    JobCommandRunner
}

type ProjectJobRuntime struct {
	store     *store.Store
	accountID string
	baseDir   string
	runner    JobCommandRunner

	mu      sync.Mutex
	running map[string]*runningProjectJob
	done    map[string]chan struct{}
}

type runningProjectJob struct {
	cancel context.CancelFunc
}

type StartProjectJobOptions struct {
	ActorID string `json:"actor_id"`
}

type preparedProjectJob struct {
	Job          *store.Job
	Project      *store.Project
	Ticket       *store.Ticket
	Driver       store.DriverDefinition
	WorktreePath string
	BranchName   string
	Prompt       string
}

func NewProjectJobRuntime(opts ProjectJobRuntimeOptions) *ProjectJobRuntime {
	runner := opts.Runner
	if runner == nil {
		runner = OSJobCommandRunner{}
	}
	return &ProjectJobRuntime{
		store:     opts.Store,
		accountID: strings.TrimSpace(opts.AccountID),
		baseDir:   strings.TrimSpace(opts.BaseDir),
		runner:    runner,
		running:   map[string]*runningProjectJob{},
		done:      map[string]chan struct{}{},
	}
}
```

Implement:

```go
func (r *ProjectJobRuntime) ProjectGitStatus(ctx context.Context, projectID string) (ProjectGitStatus, error)
func (r *ProjectJobRuntime) InitProjectGit(ctx context.Context, projectID string) (ProjectGitStatus, error)
func (r *ProjectJobRuntime) prepareApprovedJob(ctx context.Context, job *store.Job, opts StartProjectJobOptions) (*preparedProjectJob, error)
```

Use these data structures:

```go
type ProjectGitStatus struct {
	ProjectID       string `json:"project_id"`
	RootPath        string `json:"root_path"`
	IsGitRepository bool   `json:"is_git_repository"`
	HasHead         bool   `json:"has_head"`
	IsDirty         bool   `json:"is_dirty"`
	Message         string `json:"message"`
}
```

`ProjectGitStatus` command rules:

```go
// Non-git: git -C <root> rev-parse --is-inside-work-tree fails.
// Missing HEAD: git -C <root> rev-parse --verify HEAD fails.
// Dirty: git -C <root> status --porcelain returns non-empty output.
```

`InitProjectGit` must run only:

```go
git -C <root> init
```

It must not run `git add`, `git commit`, or any staging command.

`prepareApprovedJob` must:

```go
// 1. Load project, ticket, and driver snapshot from job.DriverSnapshotJSON.
// 2. Reject non one_shot mode with driver_mode_unsupported.
// 3. Check git status and return typed ProjectJobError codes.
// 4. Build worktree path: <baseDir>/worktrees/<project_id>/<ticket_id>/<job_id>.
// 5. Build branch: kittypaw/<ticket-key>/<first-8-job-id>.
// 6. Run: git -C <project.RootPath> worktree add -b <branch> <worktreePath> HEAD.
// 7. Return the prepared data and prompt text.
```

Use this branch sanitizer:

```go
func projectJobBranchName(ticketKey, jobID string) string {
	short := strings.TrimPrefix(jobID, "job_")
	if len(short) > 8 {
		short = short[:8]
	}
	replacer := strings.NewReplacer(" ", "-", "_", "-", "/", "-", "\\", "-", ":", "-")
	key := strings.Trim(replacer.Replace(ticketKey), "-")
	if key == "" {
		key = "ticket"
	}
	if short == "" {
		short = "job"
	}
	return "kittypaw/" + key + "/" + short
}
```

- [ ] **Step 5: Run engine git tests**

Run:

```bash
go test ./engine -run 'TestProjectJobRuntimeRequiresGitRepository|TestProjectJobRuntimeRequiresGitHead|TestProjectJobRuntimeRejectsDirtyRoot|TestProjectJobRuntimeCreatesWorktreeForCleanGitRoot' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit git/worktree runtime**

Run:

```bash
git add engine/project_job_runtime.go engine/project_job_runtime_test.go store/projects.go
git commit -m "feat(projects): prepare job git worktrees"
```

Expected: commit succeeds.

---

## Task 3: One-Shot Driver Execution And Log Bounds

**Files:**
- Modify: `engine/project_job_runtime.go`
- Modify: `engine/project_job_runtime_test.go`

- [ ] **Step 1: Add fake runner execution tests**

Append to `engine/project_job_runtime_test.go`:

```go
func TestProjectJobRuntimeRunsShellDriverAndRecordsSuccess(t *testing.T) {
	st := openProjectJobRuntimeStore(t)
	root := t.TempDir()
	gitInit(t, root)
	gitCommitFile(t, root, "README.md", "clean\n")
	project := createRuntimeProject(t, st, root)
	job := planApprovedRuntimeJob(t, st, project.ID)
	rt := NewProjectJobRuntime(ProjectJobRuntimeOptions{
		Store:     st,
		AccountID: "alice",
		BaseDir:   t.TempDir(),
		Runner: fakeJobCommandRunner{
			Stdout:      "driver output\n",
			ResultText:  "changed README",
			ExitCode:    0,
		},
	})
	started, err := rt.StartJob(context.Background(), job.ID, StartProjectJobOptions{ActorID: "pm"})
	if err != nil {
		t.Fatalf("StartJob() error = %v", err)
	}
	if started.Status != store.JobStatusRunning {
		t.Fatalf("started = %+v, want running", started)
	}
	if !rt.WaitForJob(job.ID, 2*time.Second) {
		t.Fatal("job did not finish")
	}
	got, err := st.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if got.Status != store.JobStatusSucceeded || got.ExitCode != 0 || got.ResultSummary != "changed README" {
		t.Fatalf("job after success = %+v", got)
	}
	ticket, err := st.GetTicket(job.TicketID)
	if err != nil {
		t.Fatalf("GetTicket() error = %v", err)
	}
	if ticket.Status != store.TicketStatusReview {
		t.Fatalf("ticket status = %q, want review", ticket.Status)
	}
}

func TestProjectJobRuntimeRecordsFailureExitCodeAndBoundedLogs(t *testing.T) {
	st := openProjectJobRuntimeStore(t)
	root := t.TempDir()
	gitInit(t, root)
	gitCommitFile(t, root, "README.md", "clean\n")
	project := createRuntimeProject(t, st, root)
	job := planApprovedRuntimeJob(t, st, project.ID)
	rt := NewProjectJobRuntime(ProjectJobRuntimeOptions{
		Store:     st,
		AccountID: "alice",
		BaseDir:   t.TempDir(),
		Runner: fakeJobCommandRunner{
			Stdout:     strings.Repeat("x", projectJobLogTailLimit+512),
			Stderr:     "exit status 9",
			ResultText: "failed",
			ExitCode:   9,
		},
	})
	if _, err := rt.StartJob(context.Background(), job.ID, StartProjectJobOptions{ActorID: "pm"}); err != nil {
		t.Fatalf("StartJob() error = %v", err)
	}
	if !rt.WaitForJob(job.ID, 2*time.Second) {
		t.Fatal("job did not finish")
	}
	got, err := st.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if got.Status != store.JobStatusFailed || got.ExitCode != 9 || !got.LogTruncated {
		t.Fatalf("job after failure = %+v", got)
	}
	if len(got.LogTail) > projectJobLogTailLimit {
		t.Fatalf("log tail length = %d, want <= %d", len(got.LogTail), projectJobLogTailLimit)
	}
	if len(got.ErrorExcerpt) > projectJobErrorExcerptLimit {
		t.Fatalf("error excerpt length = %d, want <= %d", len(got.ErrorExcerpt), projectJobErrorExcerptLimit)
	}
}

func TestProjectJobRuntimeCancelBestEffort(t *testing.T) {
	st := openProjectJobRuntimeStore(t)
	root := t.TempDir()
	gitInit(t, root)
	gitCommitFile(t, root, "README.md", "clean\n")
	project := createRuntimeProject(t, st, root)
	job := planApprovedRuntimeJob(t, st, project.ID)
	block := make(chan struct{})
	rt := NewProjectJobRuntime(ProjectJobRuntimeOptions{
		Store:     st,
		AccountID: "alice",
		BaseDir:   t.TempDir(),
		Runner: fakeBlockingJobCommandRunner{Block: block},
	})
	if _, err := rt.StartJob(context.Background(), job.ID, StartProjectJobOptions{ActorID: "pm"}); err != nil {
		t.Fatalf("StartJob() error = %v", err)
	}
	canceled, err := rt.CancelJob(context.Background(), job.ID, "alice", "stop")
	if err != nil {
		t.Fatalf("CancelJob() error = %v", err)
	}
	close(block)
	if canceled.Status != store.JobStatusCanceled {
		t.Fatalf("canceled = %+v", canceled)
	}
	ticket, err := st.GetTicket(job.TicketID)
	if err != nil {
		t.Fatalf("GetTicket() error = %v", err)
	}
	if ticket.Status != store.TicketStatusBacklog {
		t.Fatalf("ticket status = %q, want backlog", ticket.Status)
	}
}
```

Add fake runners:

```go
type fakeJobCommandRunner struct {
	Stdout     string
	Stderr     string
	ResultText string
	ExitCode   int
}

func (r fakeJobCommandRunner) Run(ctx context.Context, spec JobCommandSpec) JobCommandResult {
	if r.Stdout != "" {
		spec.Emit([]byte(r.Stdout))
	}
	if r.Stderr != "" {
		spec.Emit([]byte(r.Stderr))
	}
	return JobCommandResult{ExitCode: r.ExitCode, Summary: r.ResultText, ErrorText: r.Stderr}
}

type fakeBlockingJobCommandRunner struct {
	Block chan struct{}
}

func (r fakeBlockingJobCommandRunner) Run(ctx context.Context, spec JobCommandSpec) JobCommandResult {
	select {
	case <-ctx.Done():
		return JobCommandResult{ExitCode: -1, ErrorText: ctx.Err().Error()}
	case <-r.Block:
		return JobCommandResult{ExitCode: 0, Summary: "released"}
	}
}
```

- [ ] **Step 2: Run execution tests to verify failure**

Run:

```bash
go test ./engine -run 'TestProjectJobRuntimeRunsShellDriverAndRecordsSuccess|TestProjectJobRuntimeRecordsFailureExitCodeAndBoundedLogs|TestProjectJobRuntimeCancelBestEffort' -count=1
```

Expected: FAIL with undefined `StartJob`, `CancelJob`, `WaitForJob`, `JobCommandSpec`, and `JobCommandResult`.

- [ ] **Step 3: Add command runner abstractions**

In `engine/project_job_runtime.go`, add:

```go
type JobCommandSpec struct {
	Command string
	Args    []string
	Dir     string
	Env     []string
	Stdin   string
	Emit    func([]byte)
}

type JobCommandResult struct {
	ExitCode  int
	Summary   string
	ErrorText string
}

type JobCommandRunner interface {
	Run(ctx context.Context, spec JobCommandSpec) JobCommandResult
}

type OSJobCommandRunner struct{}

func (OSJobCommandRunner) Run(ctx context.Context, spec JobCommandSpec) JobCommandResult {
	cmd := exec.CommandContext(ctx, spec.Command, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = append(os.Environ(), spec.Env...)
	if spec.Stdin != "" {
		cmd.Stdin = strings.NewReader(spec.Stdin)
	}
	var combined bytes.Buffer
	cmd.Stdout = emitWriter{emit: spec.Emit, mirror: &combined}
	cmd.Stderr = emitWriter{emit: spec.Emit, mirror: &combined}
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		exitCode = 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}
	return JobCommandResult{ExitCode: exitCode, ErrorText: strings.TrimSpace(combined.String())}
}

type emitWriter struct {
	emit   func([]byte)
	mirror *bytes.Buffer
}

func (w emitWriter) Write(p []byte) (int, error) {
	if w.mirror != nil {
		_, _ = w.mirror.Write(p)
	}
	if w.emit != nil {
		cp := append([]byte(nil), p...)
		w.emit(cp)
	}
	return len(p), nil
}
```

- [ ] **Step 4: Add prompt and driver command builders**

Implement:

```go
func buildProjectJobPrompt(p *preparedProjectJob) string
func buildProjectJobCommand(p *preparedProjectJob) (JobCommandSpec, error)
func decodeJobDriver(job *store.Job) (store.DriverDefinition, error)
```

`buildProjectJobPrompt` must include:

```text
Project: <key> - <name>
Project root: <root_path>
Ticket: <key> - <title>
Ticket status: <status>
Ticket priority: <priority>
Job: <job_id>
Driver: <driver_id>
Mode: one_shot

User-approved prompt:
<prompt_text>

Leave all changes in this worktree:
<worktree_path>

Do not commit, push, or open a pull request.
```

`buildProjectJobCommand` must produce:

```go
// Codex
JobCommandSpec{
	Command: driver.Command,
	Args: []string{"exec", "-C", p.WorktreePath, "--json", "--sandbox", "workspace-write", "--ask-for-approval", "never", p.Prompt},
	Dir: p.WorktreePath,
}

// Claude
JobCommandSpec{
	Command: driver.Command,
	Args: []string{"-p", "--output-format", "stream-json", "--permission-mode", "acceptEdits", p.Prompt},
	Dir: p.WorktreePath,
}

// Shell
JobCommandSpec{
	Command: driver.Command,
	Args: parsedDefaultArgs,
	Dir: p.WorktreePath,
	Stdin: p.Prompt,
	Env: []string{"KITTYPAW_JOB_PROMPT=" + p.Prompt},
}
```

Reject `pty` and `tmux` with `ProjectJobErrDriverModeUnsupported`.

- [ ] **Step 5: Implement async start, finish, and cancel**

Add methods:

```go
func (r *ProjectJobRuntime) StartJob(ctx context.Context, jobID string, opts StartProjectJobOptions) (*store.Job, error)
func (r *ProjectJobRuntime) CancelJob(ctx context.Context, jobID, actorID, reason string) (*store.Job, error)
func (r *ProjectJobRuntime) JobLogs(jobID string) (*ProjectJobLogs, error)
func (r *ProjectJobRuntime) WaitForJob(jobID string, timeout time.Duration) bool
func (r *ProjectJobRuntime) Close()
```

Use this response type:

```go
type ProjectJobLogs struct {
	Job     *store.Job        `json:"job"`
	LogTail string            `json:"log_tail"`
	Events  []store.JobEvent  `json:"events"`
}
```

`StartJob` flow:

```go
// 1. Load job and prepare git worktree.
// 2. Call store.StartJob with worktree path, branch, and metadata.
// 3. Create context.WithCancel and register it in r.running[jobID].
// 4. Start goroutine r.runPreparedJob(runCtx, prepared).
// 5. Return the running job immediately.
```

`runPreparedJob` flow:

```go
// 1. Build command spec.
// 2. For each emitted chunk, append to a bounded in-memory tail, call store.UpdateJobLog,
//    and insert job_events type='log' with an 8 KiB capped message.
// 3. Run the command.
// 4. If the durable job status is already canceled, only record cleanup and return.
// 5. exit_code == 0: store.SucceedJob(...), ticket moves review.
// 6. exit_code != 0: store.FailJob(...), ticket moves blocked.
// 7. Delete r.running[jobID], close r.done[jobID], insert cleanup event.
```

`CancelJob` flow:

```go
// 1. If a live cancel func exists, call it.
// 2. Call store.CancelJob(jobID, actorID, reason).
// 3. Return the durable canceled job.
```

- [ ] **Step 6: Run engine execution tests**

Run:

```bash
go test ./engine -run 'TestProjectJobRuntime' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit one-shot runtime execution**

Run:

```bash
git add engine/project_job_runtime.go engine/project_job_runtime_test.go
git commit -m "feat(projects): run one-shot project jobs"
```

Expected: commit succeeds.

---

## Task 4: Wire Runtime Into Sessions And API

**Files:**
- Modify: `engine/session.go`
- Modify: `server/account_deps.go`
- Modify: `server/account_config.go`
- Modify: `server/api_projects.go`
- Modify: `server/server.go`
- Modify: `server/api_projects_test.go`

- [ ] **Step 1: Write API tests first**

Replace `TestProjectsAPIJobPlanApprovalNoExecutionStart` in `server/api_projects_test.go` with:

```go
func TestProjectsAPIJobStartAndLogsUseRuntime(t *testing.T) {
	srv := newProjectsAPITestServerWithRunner(t, fakeServerJobRunner{
		Stdout:     "api job log\n",
		ResultText: "api done",
		ExitCode:   0,
	})
	project := projectsAPICreateGitProject(t, srv, "kitty")
	ticket := projectsAPICreateTicket(t, srv, project.ID, "Run job")
	planned := projectsAPIPlanJob(t, srv, ticket.ID, "shell", "echo ok")

	var approved struct {
		Job struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"job"`
	}
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/jobs/"+planned.ID+"/approve", map[string]any{"actor_id": "alice"}, http.StatusOK, &approved)

	var started struct {
		Job struct {
			ID           string `json:"id"`
			Status       string `json:"status"`
			WorktreePath string `json:"worktree_path"`
		} `json:"job"`
	}
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/jobs/"+planned.ID+"/start", map[string]any{"actor_id": "alice"}, http.StatusAccepted, &started)
	if started.Job.Status != "running" || started.Job.WorktreePath == "" {
		t.Fatalf("started = %+v", started.Job)
	}
	if !srv.session.ProjectJobRuntime.WaitForJob(planned.ID, 2*time.Second) {
		t.Fatal("job did not finish")
	}

	var logs struct {
		Job struct {
			Status        string `json:"status"`
			ResultSummary string `json:"result_summary"`
			ExitCode      int    `json:"exit_code"`
		} `json:"job"`
		LogTail string `json:"log_tail"`
		Events  []struct {
			Type string `json:"type"`
		} `json:"events"`
	}
	projectsAPIRequest(t, srv, http.MethodGet, "/api/v1/jobs/"+planned.ID+"/logs", nil, http.StatusOK, &logs)
	if logs.Job.Status != "succeeded" || logs.Job.ResultSummary != "api done" || logs.Job.ExitCode != 0 {
		t.Fatalf("logs job = %+v", logs.Job)
	}
	if !strings.Contains(logs.LogTail, "api job log") || len(logs.Events) == 0 {
		t.Fatalf("logs = %+v", logs)
	}
}

func TestProjectsAPIStartNonGitReturnsStructuredCodeAndGitInitDoesNotStage(t *testing.T) {
	srv := newProjectsAPITestServerWithRunner(t, fakeServerJobRunner{ExitCode: 0})
	project := projectsAPICreateProject(t, srv, "kitty")
	ticket := projectsAPICreateTicket(t, srv, project.ID, "Run job")
	planned := projectsAPIPlanJob(t, srv, ticket.ID, "shell", "echo ok")
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/jobs/"+planned.ID+"/approve", map[string]any{"actor_id": "alice"}, http.StatusOK, nil)

	var startErr struct {
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/jobs/"+planned.ID+"/start", map[string]any{"actor_id": "alice"}, http.StatusConflict, &startErr)
	if startErr.Code != store.ProjectJobErrProjectNotGitRepository {
		t.Fatalf("startErr = %+v", startErr)
	}

	var initResp struct {
		Git struct {
			IsGitRepository bool `json:"is_git_repository"`
			HasHead         bool `json:"has_head"`
		} `json:"git"`
	}
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/projects/"+project.ID+"/git/init", nil, http.StatusOK, &initResp)
	if !initResp.Git.IsGitRepository || initResp.Git.HasHead {
		t.Fatalf("init git status = %+v", initResp.Git)
	}
}
```

Add helpers:

```go
type fakeServerJobRunner struct {
	Stdout     string
	ResultText string
	ExitCode   int
}

func (r fakeServerJobRunner) Run(ctx context.Context, spec engine.JobCommandSpec) engine.JobCommandResult {
	if r.Stdout != "" {
		spec.Emit([]byte(r.Stdout))
	}
	return engine.JobCommandResult{ExitCode: r.ExitCode, Summary: r.ResultText}
}

func newProjectsAPITestServerWithRunner(t *testing.T, runner engine.JobCommandRunner) *Server {
	t.Helper()
	cfg := core.DefaultConfig()
	cfg.Server.APIKey = "api-key"
	deps := buildAccountDeps(t, filepath.Join(t.TempDir(), "accounts"), "alice", &cfg)
	deps.JobRuntime = engine.NewProjectJobRuntime(engine.ProjectJobRuntimeOptions{
		Store:     deps.Store,
		AccountID: deps.Account.ID,
		BaseDir:   deps.Account.BaseDir,
		Runner:    runner,
	})
	return New([]*AccountDeps{deps}, "test")
}

func projectsAPICreateGitProject(t *testing.T, srv *Server, key string) struct {
	ID  string
	Key string
} {
	t.Helper()
	root := t.TempDir()
	gitInitForServerTest(t, root)
	gitCommitFileForServerTest(t, root, "README.md", "clean\n")
	var created struct {
		Project struct {
			ID  string `json:"id"`
			Key string `json:"key"`
		} `json:"project"`
	}
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/projects", map[string]any{
		"key":       key,
		"name":      key,
		"root_path": root,
	}, http.StatusCreated, &created)
	return struct {
		ID  string
		Key string
	}{ID: created.Project.ID, Key: created.Project.Key}
}

func gitInitForServerTest(t *testing.T, root string) {
	t.Helper()
	runGitForServerTest(t, root, "init")
	runGitForServerTest(t, root, "config", "user.email", "kittypaw@example.test")
	runGitForServerTest(t, root, "config", "user.name", "KittyPaw Test")
}

func gitCommitFileForServerTest(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	runGitForServerTest(t, root, "add", name)
	runGitForServerTest(t, root, "commit", "-m", "initial")
}

func runGitForServerTest(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}

func projectsAPIPlanJob(t *testing.T, srv *Server, ticketID, driverID, prompt string) struct{ ID string } {
	t.Helper()
	var planned struct {
		Job struct {
			ID string `json:"id"`
		} `json:"job"`
	}
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/tickets/"+ticketID+"/jobs/plan", map[string]any{
		"driver_id":      driverID,
		"mode":           "one_shot",
		"prompt_summary": "Run job",
		"prompt_text":    prompt,
	}, http.StatusCreated, &planned)
	return struct{ ID string }{ID: planned.Job.ID}
}
```

- [ ] **Step 2: Run API tests to verify failure**

Run:

```bash
go test ./server -run 'TestProjectsAPIJobStartAndLogsUseRuntime|TestProjectsAPIStartNonGitReturnsStructuredCodeAndGitInitDoesNotStage' -count=1
```

Expected: FAIL with missing `AccountDeps.JobRuntime`, `Session.ProjectJobRuntime`, route, and handler behavior.

- [ ] **Step 3: Add runtime dependency to Session and AccountDeps**

In `engine/session.go`, add to `Session`:

```go
ProjectJobRuntime *ProjectJobRuntime
```

In `server/account_deps.go`, add to `AccountDeps`:

```go
JobRuntime *engine.ProjectJobRuntime
```

In `OpenAccountDeps`, set:

```go
jobRuntime := engine.NewProjectJobRuntime(engine.ProjectJobRuntimeOptions{
	Store:     st,
	AccountID: t.ID,
	BaseDir:   t.BaseDir,
})
```

and include `JobRuntime: jobRuntime`.

In `AccountDeps.Close`, call before closing the store:

```go
if td.JobRuntime != nil {
	td.JobRuntime.Close()
}
```

In `buildAccountSession`, ensure runtime exists and mark interrupted jobs failed:

```go
if td.JobRuntime == nil {
	td.JobRuntime = engine.NewProjectJobRuntime(engine.ProjectJobRuntimeOptions{
		Store:     td.Store,
		AccountID: td.Account.ID,
		BaseDir:   td.Account.BaseDir,
	})
}
if count, err := td.Store.MarkRunningJobsFailedOnStartup("daemon stopped while the job was running"); err != nil {
	slog.Warn("mark interrupted project jobs failed", "account", td.Account.ID, "error", err)
} else if count > 0 {
	slog.Warn("marked interrupted project jobs failed", "account", td.Account.ID, "count", count)
}
```

Set `ProjectJobRuntime: td.JobRuntime` in the Session literal.

In `server/account_config.go`, reuse `td.JobRuntime`:

```go
if td.JobRuntime == nil {
	td.JobRuntime = engine.NewProjectJobRuntime(engine.ProjectJobRuntimeOptions{
		Store:     td.Store,
		AccountID: td.Account.ID,
		BaseDir:   td.Account.BaseDir,
	})
}
```

and set `ProjectJobRuntime: td.JobRuntime` in the rebuilt Session.

- [ ] **Step 4: Preserve request account context for runtime API**

In `server/api_projects.go`, replace the store-only context value with:

```go
type projectsRequestContext struct {
	Store   *store.Store
	Session *engine.Session
	Account *core.Account
}
```

Update middleware:

```go
ctxValue := projectsRequestContext{Store: s.store, Session: s.session}
if required {
	acct, acctErr := s.requestAccount(r)
	if acctErr == nil {
		ctxValue = projectsRequestContext{Store: acct.Deps.Store, Session: acct.Session, Account: acct.Deps.Account}
	} else if !s.apiTokenAccepted(requestAuthToken(r)) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
}
ctx := context.WithValue(r.Context(), projectsStoreContextKey{}, ctxValue)
next.ServeHTTP(w, r.WithContext(ctx))
```

Keep `projectsStore` backward compatible:

```go
func (s *Server) projectsStore(r *http.Request) *store.Store {
	if ctxValue, ok := r.Context().Value(projectsStoreContextKey{}).(projectsRequestContext); ok && ctxValue.Store != nil {
		return ctxValue.Store
	}
	if st, ok := r.Context().Value(projectsStoreContextKey{}).(*store.Store); ok && st != nil {
		return st
	}
	return s.store
}

func (s *Server) projectsSession(r *http.Request) *engine.Session {
	if ctxValue, ok := r.Context().Value(projectsStoreContextKey{}).(projectsRequestContext); ok && ctxValue.Session != nil {
		return ctxValue.Session
	}
	return s.session
}
```

- [ ] **Step 5: Add API routes and handlers**

In `server/server.go`, under Projects routes add:

```go
r.Post("/projects/{project}/git/init", s.handleProjectGitInit)
```

In `server/api_projects.go`, add structured project job error output:

```go
func writeProjectJobAPIError(w http.ResponseWriter, err error) {
	var jobErr *store.ProjectJobError
	if errors.As(err, &jobErr) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": jobErr.Code, "error": jobErr.Error()})
		return
	}
	projectsWriteStoreError(w, err)
}
```

Implement handlers:

```go
func (s *Server) handleProjectGitInit(w http.ResponseWriter, r *http.Request) {
	runtime := s.projectsSession(r).ProjectJobRuntime
	if runtime == nil {
		writeError(w, http.StatusInternalServerError, "project job runtime unavailable")
		return
	}
	status, err := runtime.InitProjectGit(r.Context(), chi.URLParam(r, "project"))
	if err != nil {
		writeProjectJobAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"git": status})
}

func (s *Server) handleJobStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ActorID string `json:"actor_id"`
	}
	if r.Body != nil && r.ContentLength != 0 && !decodeBody(w, r, &body) {
		return
	}
	runtime := s.projectsSession(r).ProjectJobRuntime
	if runtime == nil {
		writeError(w, http.StatusInternalServerError, "project job runtime unavailable")
		return
	}
	job, err := runtime.StartJob(r.Context(), chi.URLParam(r, "job"), engine.StartProjectJobOptions{ActorID: body.ActorID})
	if err != nil {
		writeProjectJobAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"job": job})
}
```

Update:

```go
func (s *Server) handleTicketJobsList(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.projectsStore(r).ListJobs(store.JobListFilter{TicketID: chi.URLParam(r, "ticket")})
	if err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	if jobs == nil {
		jobs = []store.Job{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func (s *Server) handleJobCancel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ActorID string `json:"actor_id"`
		Reason  string `json:"reason"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	runtime := s.projectsSession(r).ProjectJobRuntime
	if runtime != nil {
		job, err := runtime.CancelJob(r.Context(), chi.URLParam(r, "job"), body.ActorID, body.Reason)
		if err != nil {
			writeProjectJobAPIError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"job": job})
		return
	}
	job, err := s.projectsStore(r).CancelJob(chi.URLParam(r, "job"), body.ActorID, body.Reason)
	if err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": job})
}

func (s *Server) handleJobLogs(w http.ResponseWriter, r *http.Request) {
	runtime := s.projectsSession(r).ProjectJobRuntime
	if runtime != nil {
		logs, err := runtime.JobLogs(chi.URLParam(r, "job"))
		if err != nil {
			projectsWriteStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, logs)
		return
	}
	job, err := s.projectsStore(r).GetJob(chi.URLParam(r, "job"))
	if err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	events, err := s.projectsStore(r).ListJobEvents(job.ID)
	if err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": job, "log_tail": job.LogTail, "events": events})
}
```

- [ ] **Step 6: Run API package tests**

Run:

```bash
go test ./server -run 'TestProjectsAPI' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit API wiring**

Run:

```bash
git add engine/session.go server/account_deps.go server/account_config.go server/api_projects.go server/server.go server/api_projects_test.go
git commit -m "feat(projects): expose job runtime API"
```

Expected: commit succeeds.

---

## Task 5: Projects Tool And Chat Runtime Path

**Files:**
- Modify: `engine/executor.go`
- Modify: `engine/projects_scope_test.go`

- [ ] **Step 1: Add Projects tool tests**

Add tests to `engine/projects_scope_test.go`:

```go
func TestProjectsToolStartJobCallsRuntime(t *testing.T) {
	st := openTestStore(t)
	project := createProjectsScopeTestProject(t, st, "kitty")
	ticket, err := st.CreateTicket(store.CreateTicketRequest{ProjectID: project.ID, Title: "Run"})
	if err != nil {
		t.Fatalf("CreateTicket() error = %v", err)
	}
	if err := st.EnsureDefaultDrivers(); err != nil {
		t.Fatalf("EnsureDefaultDrivers() error = %v", err)
	}
	job, err := st.PlanJob(store.PlanJobRequest{
		ProjectID:     project.ID,
		TicketID:      ticket.ID,
		DriverID:      "shell",
		Mode:          store.JobModeOneShot,
		PromptSummary: "Run",
		PromptText:    "echo ok",
	})
	if err != nil {
		t.Fatalf("PlanJob() error = %v", err)
	}
	if _, err := st.ApproveJob(job.ID, "pm"); err != nil {
		t.Fatalf("ApproveJob() error = %v", err)
	}
	rt := NewProjectJobRuntime(ProjectJobRuntimeOptions{Store: st, AccountID: "alice", BaseDir: t.TempDir(), Runner: fakeProjectsToolRunner{ExitCode: 0, ResultText: "done"}})
	sess := &Session{Store: st, ProjectJobRuntime: rt}

	result, err := executeProjects(context.Background(), skillCallForProjectsTest("startJob", job.ID, map[string]any{"actor_id": "pm"}), sess)
	if err != nil {
		t.Fatalf("executeProjects(startJob) error = %v", err)
	}
	if !strings.Contains(result, `"status":"running"`) {
		t.Fatalf("result = %s, want running job", result)
	}
}

func TestProjectsToolJobLogsReturnsCurrentJobAndEvents(t *testing.T) {
	st := openTestStore(t)
	project := createProjectsScopeTestProject(t, st, "kitty")
	ticket, err := st.CreateTicket(store.CreateTicketRequest{ProjectID: project.ID, Title: "Logs"})
	if err != nil {
		t.Fatalf("CreateTicket() error = %v", err)
	}
	if err := st.EnsureDefaultDrivers(); err != nil {
		t.Fatalf("EnsureDefaultDrivers() error = %v", err)
	}
	job, err := st.PlanJob(store.PlanJobRequest{ProjectID: project.ID, TicketID: ticket.ID, DriverID: "shell", Mode: store.JobModeOneShot, PromptSummary: "Logs", PromptText: "echo ok"})
	if err != nil {
		t.Fatalf("PlanJob() error = %v", err)
	}
	if _, err := st.AddJobEvent(store.AddJobEventRequest{JobID: job.ID, Type: "log", Message: "hello"}); err != nil {
		t.Fatalf("AddJobEvent() error = %v", err)
	}
	rt := NewProjectJobRuntime(ProjectJobRuntimeOptions{Store: st, AccountID: "alice", BaseDir: t.TempDir(), Runner: fakeProjectsToolRunner{ExitCode: 0}})
	sess := &Session{Store: st, ProjectJobRuntime: rt}

	result, err := executeProjects(context.Background(), skillCallForProjectsTest("jobLogs", job.ID), sess)
	if err != nil {
		t.Fatalf("executeProjects(jobLogs) error = %v", err)
	}
	if !strings.Contains(result, `"events"`) || !strings.Contains(result, `"job"`) {
		t.Fatalf("result = %s, want job logs", result)
	}
}
```

Add helper:

```go
type fakeProjectsToolRunner struct {
	ExitCode   int
	ResultText string
}

func (r fakeProjectsToolRunner) Run(ctx context.Context, spec JobCommandSpec) JobCommandResult {
	return JobCommandResult{ExitCode: r.ExitCode, Summary: r.ResultText}
}

func createProjectsScopeTestProject(t *testing.T, st *store.Store, key string) *store.Project {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	gitCommitFile(t, root, "README.md", "clean\n")
	project, err := st.CreateProject(store.CreateProjectRequest{Key: key, Name: key, RootPath: root})
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	return project
}

func skillCallForProjectsTest(method string, args ...any) core.SkillCall {
	raw := make([]json.RawMessage, 0, len(args))
	for _, arg := range args {
		data, err := json.Marshal(arg)
		if err != nil {
			panic(err)
		}
		raw = append(raw, data)
	}
	return core.SkillCall{SkillName: "Projects", Method: method, Args: raw}
}
```

- [ ] **Step 2: Run Projects tool tests to verify failure**

Run:

```bash
go test ./engine -run 'TestProjectsToolStartJobCallsRuntime|TestProjectsToolJobLogsReturnsCurrentJobAndEvents' -count=1
```

Expected: FAIL with unknown Projects methods.

- [ ] **Step 3: Add Projects tool methods**

In `engine/executor.go`, confirm `projectsJobOptions` includes the actor field used by runtime calls:

```go
ActorID string `json:"actor_id"`
```

Add switch cases in `executeProjects`:

```go
case "initProjectGit":
	projectID, err := projectsToolStringArg(call, 0, "project")
	if err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}
	if s.ProjectJobRuntime == nil {
		return jsonResult(map[string]any{"error": "project job runtime unavailable"})
	}
	status, err := s.ProjectJobRuntime.InitProjectGit(context.Background(), projectID)
	if err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}
	return jsonResult(map[string]any{"git": status})

case "startJob":
	jobID, err := projectsToolStringArg(call, 0, "job")
	if err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}
	if s.ProjectJobRuntime == nil {
		return jsonResult(map[string]any{"error": "project job runtime unavailable"})
	}
	opts := projectsJobOptionsArg(call, 1)
	job, err := s.ProjectJobRuntime.StartJob(context.Background(), jobID, StartProjectJobOptions{ActorID: strings.TrimSpace(opts.ActorID)})
	if err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}
	return jsonResult(map[string]any{"job": job})

case "jobLogs":
	jobID, err := projectsToolStringArg(call, 0, "job")
	if err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}
	if s.ProjectJobRuntime == nil {
		return jsonResult(map[string]any{"error": "project job runtime unavailable"})
	}
	logs, err := s.ProjectJobRuntime.JobLogs(jobID)
	if err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}
	return jsonResult(logs)
```

Use the request `ctx` parameter instead of `_ context.Context` in `executeProjects`, and pass it into runtime calls.

- [ ] **Step 4: Run engine tests**

Run:

```bash
go test ./engine -run 'TestProjectsTool|TestProjectScopedChat|TestTicketScopedChat' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit chat tool runtime path**

Run:

```bash
git add engine/executor.go engine/projects_scope_test.go
git commit -m "feat(projects): add job runtime tool methods"
```

Expected: commit succeeds.

---

## Task 6: Web UI Job Controls

**Files:**
- Modify: `server/web/projects.js`
- Modify: `server/web_projects_test.go`

- [ ] **Step 1: Add web static tests**

In `server/web_projects_test.go`, add assertions that `projects.js` contains runtime controls:

```go
func TestProjectsWebIncludesJobRuntimeControls(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("web", "projects.js"))
	if err != nil {
		t.Fatalf("read projects.js: %v", err)
	}
	src := string(data)
	for _, want := range []string{
		"_startJob",
		"_cancelJob",
		"_loadJobLogs",
		"/api/v1/projects/",
		"/git/init",
		"/api/v1/jobs/",
		"/start",
		"/cancel",
		"/logs",
		"Open Worktree",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("projects.js missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Run web test to verify failure**

Run:

```bash
go test ./server -run TestProjectsWebIncludesJobRuntimeControls -count=1
```

Expected: FAIL because the static controls are missing.

- [ ] **Step 3: Add UI state and rendering**

In `server/web/projects.js`, add state fields:

```js
_selectedJob: '',
_jobLogs: null,
_jobLogTimer: null,
```

In the Jobs section renderer, show listed jobs and selected job details:

```js
html += '<div class="projects-job-list">';
(this._jobs || []).forEach(job => {
  const active = this._selectedJob === job.id ? ' projects-job--active' : '';
  html += '<button class="projects-job' + active + '" data-projects-job="' + escHTMLAttr(job.id) + '" type="button">' +
    '<span>' + esc(job.prompt_summary || job.id) + '</span>' +
    '<small>' + esc(job.status || '') + ' · ' + esc(job.driver_id || '') + '</small>' +
    '</button>';
});
html += '</div>';
html += this._jobDetailHTML();
```

Add:

```js
_jobDetailHTML() {
  const job = (this._jobs || []).find(item => item.id === this._selectedJob);
  if (!job) return '';
  const logs = this._jobLogs && this._jobLogs.job && this._jobLogs.job.id === job.id ? this._jobLogs : null;
  const current = logs && logs.job ? logs.job : job;
  let html = '<section class="projects-job-detail">';
  html += '<h4>' + esc(current.prompt_summary || current.id) + '</h4>';
  html += '<dl class="projects-job-meta">';
  html += '<dt>Status</dt><dd>' + esc(current.status || '') + '</dd>';
  html += '<dt>Driver</dt><dd>' + esc(current.driver_id || '') + ' / ' + esc(current.mode || '') + '</dd>';
  html += '<dt>Branch</dt><dd>' + esc(current.branch_name || '') + '</dd>';
  html += '<dt>Worktree</dt><dd>' + esc(current.worktree_path || '') + '</dd>';
  html += '</dl>';
  if (current.status === 'approved') html += '<button class="btn btn--primary btn--sm" id="projects-job-start" type="button">Start</button>';
  if (current.status === 'running') html += '<button class="btn btn--secondary btn--sm" id="projects-job-cancel" type="button">Cancel</button>';
  if (current.worktree_path) html += '<button class="btn btn--secondary btn--sm" id="projects-job-open-worktree" type="button">Open Worktree</button>';
  html += '<pre class="projects-job-log">' + esc((logs && logs.log_tail) || current.log_tail || '') + '</pre>';
  html += '</section>';
  return html;
}
```

- [ ] **Step 4: Add API calls and bindings**

Add methods:

```js
async _startJob() {
  if (!this._selectedJob) return;
  try {
    const res = await fetch('/api/v1/jobs/' + encodeURIComponent(this._selectedJob) + '/start', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ actor_id: 'web' })
    });
    const data = await res.json();
    if (!res.ok && data.code === 'project_not_git_repository') {
      await this._promptGitInit();
      return;
    }
    if (!res.ok) throw new Error(data.error || 'Job start failed');
    await this._loadJobLogs(this._selectedJob);
    await this._loadProjectBoard();
  } catch (err) {
    this._error = err.message || String(err);
    this._render();
  }
}

async _cancelJob() {
  if (!this._selectedJob) return;
  await fetch('/api/v1/jobs/' + encodeURIComponent(this._selectedJob) + '/cancel', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ actor_id: 'web', reason: 'canceled from Projects UI' })
  });
  await this._loadJobLogs(this._selectedJob);
  await this._loadProjectBoard();
}

async _loadJobLogs(jobID) {
  const res = await fetch('/api/v1/jobs/' + encodeURIComponent(jobID) + '/logs');
  const data = await res.json();
  if (!res.ok) throw new Error(data.error || 'Load job logs failed');
  this._jobLogs = data;
  return data;
}

async _promptGitInit() {
  const project = this._selectedProjectObject();
  if (!project) return;
  if (!window.confirm('This project is not a git repository. Initialize git for this project?')) return;
  const res = await fetch('/api/v1/projects/' + encodeURIComponent(project.id || project.key) + '/git/init', { method: 'POST' });
  const data = await res.json();
  if (!res.ok) throw new Error(data.error || 'Git init failed');
  window.alert(data.git && !data.git.has_head ? 'Create an initial commit before starting a job.' : 'Git initialized.');
}
```

Bind:

```js
document.querySelectorAll('[data-projects-job]').forEach(button => {
  button.onclick = async () => {
    this._selectedJob = button.dataset.projectsJob || '';
    await this._loadJobLogs(this._selectedJob);
    this._render();
  };
});
const startJob = document.getElementById('projects-job-start');
if (startJob) startJob.onclick = () => this._startJob();
const cancelJob = document.getElementById('projects-job-cancel');
if (cancelJob) cancelJob.onclick = () => this._cancelJob();
const openWorktree = document.getElementById('projects-job-open-worktree');
if (openWorktree) openWorktree.onclick = () => this._openSelectedJobWorktree();
```

If no file-open endpoint exists, `_openSelectedJobWorktree` should copy path into the existing visible UI status instead of inventing a filesystem capability:

```js
_openSelectedJobWorktree() {
  const logs = this._jobLogs || {};
  const job = logs.job || (this._jobs || []).find(item => item.id === this._selectedJob);
  if (job && job.worktree_path) {
    this._notice = job.worktree_path;
    this._render();
  }
}
```

- [ ] **Step 5: Run web tests**

Run:

```bash
go test ./server -run 'TestProjectsWebIncludesJobRuntimeControls|TestProjectsAPI' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit web job controls**

Run:

```bash
git add server/web/projects.js server/web_projects_test.go
git commit -m "feat(projects): add job runtime controls"
```

Expected: commit succeeds.

---

## Task 7: Integration Verification And Tracker Cleanup

**Files:**
- Modify: `TASKS.md`
- Modify: `../../TASKS.md`

- [ ] **Step 1: Run focused package tests**

Run:

```bash
go test ./store ./engine ./server -count=1
```

Expected: PASS.

- [ ] **Step 2: Run full app tests**

Run:

```bash
make test-ci
```

Expected: PASS.

- [ ] **Step 3: Optional live driver smoke tests**

Only run Codex live smoke if `KITTYPAW_LIVE_CODEX_JOB=1` is set:

```bash
KITTYPAW_LIVE_CODEX_JOB=1 go test ./engine -run TestProjectJobRuntimeLiveCodex -count=1
```

Expected when env is set and `codex` is installed: PASS.

Only run Claude live smoke if `KITTYPAW_LIVE_CLAUDE_JOB=1` is set:

```bash
KITTYPAW_LIVE_CLAUDE_JOB=1 go test ./engine -run TestProjectJobRuntimeLiveClaude -count=1
```

Expected when env is set and `claude` is installed: PASS.

- [ ] **Step 4: Update TASKS trackers**

In `TASKS.md`, update the Projects Phase 1.5 entry to mark implementation complete and review pending:

```markdown
- [ ] Review Project Job Runtime Phase 1.5 implementation and run live smoke on a disposable repo.
```

In `../../TASKS.md`, mirror the same wording for the app-level tracker.

- [ ] **Step 5: Commit tracker cleanup**

Run:

```bash
git add TASKS.md ../../TASKS.md
git commit -m "docs(projects): track job runtime implementation review"
```

Expected: commit succeeds.

- [ ] **Step 6: Request review**

Run the repository review flow requested by the user after implementation:

```bash
/review
```

Expected: reviewer returns no P1/P2 issues, or each issue is fixed in a follow-up commit before release.

- [ ] **Step 7: Final verification before release**

Run:

```bash
git status --short --branch
go test ./store ./engine ./server -count=1
make test-ci
```

Expected:

```text
## main...origin/main
```

plus passing Go and CI test output.

---

## Self-Review

- Spec coverage:
  - One-shot approved Job start is covered by Tasks 1, 3, and 4.
  - Per-job git worktree creation and branch naming are covered by Task 2.
  - Non-git, no-HEAD, and dirty-root safety checks are covered by Tasks 2 and 4.
  - Codex, Claude, and shell command shapes are covered by Task 3.
  - Bounded logs and event messages are covered by Task 3.
  - API and `Projects` tool paths are covered by Tasks 4 and 5.
  - Web UI controls are covered by Task 6.
  - Startup recovery for interrupted running Jobs is covered by Tasks 1 and 4.
- Type consistency:
  - Store exposes `StartJobRequest`, `FinishJobRequest`, `JobListFilter`, and `ProjectJobError`.
  - Engine exposes `ProjectJobRuntime`, `StartProjectJobOptions`, `ProjectGitStatus`, `ProjectJobLogs`, `JobCommandRunner`, `JobCommandSpec`, and `JobCommandResult`.
  - Server uses the same runtime instance through `Session.ProjectJobRuntime`.
- Deliberate exclusions:
  - No PTY, tmux, direct project-root execution, auto commit, push, PR, or worktree deletion is introduced in this phase.
