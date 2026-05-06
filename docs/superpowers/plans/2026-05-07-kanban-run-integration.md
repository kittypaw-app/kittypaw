# Kanban Run Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a local `kittypaw kanban exec` path that records command success or failure as durable Kanban Run outcomes.

**Architecture:** Extend the Kanban store with a failed-run transition, expose it through the server API, and add a CLI command that claims a task, runs an external command in the Run work dir, then completes or fails the Run. Keep execution synchronous and local; do not add dispatcher or LLM worker behavior.

**Tech Stack:** Go, SQLite-backed store, Cobra CLI, existing `/api/v1` server handlers, Go tests.

---

## File Structure

- Modify `apps/kittypaw/store/kanban.go`: add `FailKanbanTaskRequest` and `FailKanbanTask`.
- Modify `apps/kittypaw/store/kanban_test.go`: add failed-run transition tests.
- Modify `apps/kittypaw/server/api_kanban.go`: add `/fail` handler.
- Modify `apps/kittypaw/server/server.go`: register `/api/v1/kanban/tasks/{task}/fail`.
- Modify `apps/kittypaw/server/api_kanban_test.go`: add API fail endpoint test.
- Modify `apps/kittypaw/cli/cmd_kanban.go`: add `kanban exec`.
- Modify `apps/kittypaw/cli/cmd_kanban_test.go`: add command exposure/flag tests and exec integration tests.

## Task 1: Store Failed-Run Transition

**Files:**
- Modify: `apps/kittypaw/store/kanban.go`
- Modify: `apps/kittypaw/store/kanban_test.go`

- [ ] **Step 1: Write failing store tests**

Append to `apps/kittypaw/store/kanban_test.go`:

```go
func TestKanbanFailRecordsRunAndReturnsTaskToTodo(t *testing.T) {
	st := openTestStore(t)
	project, err := st.CreateKanbanProject(CreateKanbanProjectRequest{
		Slug:     "kitty",
		Name:     "KittyPaw",
		RootPath: "/repo/kitty",
	})
	if err != nil {
		t.Fatalf("CreateKanbanProject: %v", err)
	}
	task, err := st.CreateKanbanTask(CreateKanbanTaskRequest{
		ProjectID: project.ID,
		Title:     "Run tests",
		Status:    KanbanStatusTodo,
	})
	if err != nil {
		t.Fatalf("CreateKanbanTask: %v", err)
	}
	if _, err := st.ClaimKanbanTask(task.ID, ClaimKanbanTaskRequest{Actor: "alice"}); err != nil {
		t.Fatalf("ClaimKanbanTask: %v", err)
	}

	if err := st.FailKanbanTask(task.ID, FailKanbanTaskRequest{
		Actor:        "alice",
		Summary:      "tests failed",
		Error:        "exit status 1",
		MetadataJSON: `{"exit_code":1}`,
	}); err != nil {
		t.Fatalf("FailKanbanTask: %v", err)
	}

	got, err := st.GetKanbanTask(task.ID)
	if err != nil {
		t.Fatalf("GetKanbanTask: %v", err)
	}
	if got.Status != KanbanStatusTodo || got.CompletedAt != "" {
		t.Fatalf("task after fail = %+v", got)
	}
	runs, err := st.ListKanbanRuns(task.ID)
	if err != nil {
		t.Fatalf("ListKanbanRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].Outcome != KanbanRunFailed || runs[0].Summary != "tests failed" || runs[0].Error != "exit status 1" || runs[0].MetadataJSON != `{"exit_code":1}` || runs[0].FinishedAt == "" {
		t.Fatalf("runs = %+v", runs)
	}
}

func TestKanbanFailRequiresRunningRun(t *testing.T) {
	st := openTestStore(t)
	project, err := st.CreateKanbanProject(CreateKanbanProjectRequest{
		Slug:     "kitty",
		Name:     "KittyPaw",
		RootPath: "/repo/kitty",
	})
	if err != nil {
		t.Fatalf("CreateKanbanProject: %v", err)
	}
	task, err := st.CreateKanbanTask(CreateKanbanTaskRequest{
		ProjectID: project.ID,
		Title:     "Run without claim",
		Status:    KanbanStatusTodo,
	})
	if err != nil {
		t.Fatalf("CreateKanbanTask: %v", err)
	}

	if err := st.FailKanbanTask(task.ID, FailKanbanTaskRequest{Actor: "alice", Error: "boom"}); err == nil {
		t.Fatal("expected failing an unclaimed task to fail")
	}
	got, err := st.GetKanbanTask(task.ID)
	if err != nil {
		t.Fatalf("GetKanbanTask: %v", err)
	}
	if got.Status != KanbanStatusTodo || got.CompletedAt != "" {
		t.Fatalf("task after rejected fail = %+v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd apps/kittypaw && go test ./store -run 'TestKanban.*Fail' -count=1`

Expected: FAIL because `FailKanbanTaskRequest` and `FailKanbanTask` are undefined.

- [ ] **Step 3: Implement store transition**

In `apps/kittypaw/store/kanban.go`, add:

```go
type FailKanbanTaskRequest struct {
	Actor        string
	Summary      string
	Error        string
	MetadataJSON string
}
```

After `CompleteKanbanTask`, add:

```go
func (s *Store) FailKanbanTask(taskID string, req FailKanbanTaskRequest) error {
	errorText := strings.TrimSpace(req.Error)
	if errorText == "" {
		return fmt.Errorf("error is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := kanbanNow()
	metadata := normalizeKanbanJSON(req.MetadataJSON)
	res, err := tx.Exec(`
		UPDATE kanban_task_runs
		SET outcome = ?, summary = ?, error = ?, metadata_json = ?, finished_at = ?, heartbeat_at = ?
		WHERE id = (
			SELECT id FROM kanban_task_runs
			WHERE task_id = ? AND outcome = ?
			ORDER BY started_at DESC
			LIMIT 1
		)`,
		KanbanRunFailed, strings.TrimSpace(req.Summary), errorText, metadata, now, now, taskID, KanbanRunRunning)
	if err != nil {
		return err
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return fmt.Errorf("task %s has no running run", taskID)
	}
	if _, err := tx.Exec(`
		UPDATE kanban_tasks
		SET status = ?, updated_at = ?
		WHERE id = ?`,
		KanbanStatusTodo, now, taskID); err != nil {
		return err
	}
	detail := strings.TrimSpace(req.Summary)
	if detail == "" {
		detail = errorText
	}
	if err := recordKanbanEventTx(tx, taskID, req.Actor, "failed", detail, metadata); err != nil {
		return err
	}
	return tx.Commit()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd apps/kittypaw && go test ./store -run 'TestKanban.*Fail' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/kittypaw/store/kanban.go apps/kittypaw/store/kanban_test.go
git commit -m "feat(store): record failed kanban runs"
```

## Task 2: Server Fail Endpoint

**Files:**
- Modify: `apps/kittypaw/server/api_kanban.go`
- Modify: `apps/kittypaw/server/server.go`
- Modify: `apps/kittypaw/server/api_kanban_test.go`

- [ ] **Step 1: Write failing API test**

Append to `apps/kittypaw/server/api_kanban_test.go`:

```go
func TestKanbanAPITaskFailRecordsFailedRun(t *testing.T) {
	srv := newKanbanAPITestServer(t)
	kanbanAPICreateProject(t, srv, "kitty")
	taskID := kanbanAPICreateTask(t, srv, "Failing command")

	kanbanAPIRequest(t, srv, http.MethodPost, "/api/v1/kanban/tasks/"+taskID+"/claim", map[string]any{
		"actor": "alice",
	}, http.StatusOK, nil)

	var failed struct {
		Task struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"task"`
	}
	kanbanAPIRequest(t, srv, http.MethodPost, "/api/v1/kanban/tasks/"+taskID+"/fail", map[string]any{
		"actor":   "alice",
		"summary": "command failed",
		"error":   "exit status 7",
		"metadata": map[string]any{
			"exit_code": 7,
		},
	}, http.StatusOK, &failed)
	if failed.Task.ID != taskID || failed.Task.Status != "todo" {
		t.Fatalf("failed task = %+v", failed.Task)
	}

	var runs struct {
		Runs []struct {
			Outcome      string `json:"outcome"`
			Summary      string `json:"summary"`
			Error        string `json:"error"`
			MetadataJSON string `json:"metadata_json"`
		} `json:"runs"`
	}
	kanbanAPIRequest(t, srv, http.MethodGet, "/api/v1/kanban/tasks/"+taskID+"/runs", nil, http.StatusOK, &runs)
	if len(runs.Runs) != 1 || runs.Runs[0].Outcome != "failed" || runs.Runs[0].Summary != "command failed" || runs.Runs[0].Error != "exit status 7" || !strings.Contains(runs.Runs[0].MetadataJSON, `"exit_code":7`) {
		t.Fatalf("runs = %+v", runs.Runs)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd apps/kittypaw && go test ./server -run TestKanbanAPITaskFailRecordsFailedRun -count=1`

Expected: FAIL with 404 or missing route.

- [ ] **Step 3: Implement handler and route**

In `apps/kittypaw/server/api_kanban.go`, add a handler after complete:

```go
func (s *Server) handleKanbanTaskFail(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(chi.URLParam(r, "task"))
	if _, err := kanbanResolveTask(s.store, taskID); err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	var body struct {
		Actor        string          `json:"actor"`
		Summary      string          `json:"summary"`
		Error        string          `json:"error"`
		Metadata     json.RawMessage `json:"metadata"`
		MetadataJSON string          `json:"metadata_json"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	errorText := strings.TrimSpace(body.Error)
	if errorText == "" {
		writeError(w, http.StatusBadRequest, "error is required")
		return
	}
	metadata, ok := kanbanMetadataJSON(w, body.Metadata, body.MetadataJSON)
	if !ok {
		return
	}
	if err := s.store.FailKanbanTask(taskID, store.FailKanbanTaskRequest{
		Actor:        strings.TrimSpace(body.Actor),
		Summary:      strings.TrimSpace(body.Summary),
		Error:        errorText,
		MetadataJSON: metadata,
	}); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	task, err := s.store.GetKanbanTask(taskID)
	if err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": task})
}
```

In `apps/kittypaw/server/server.go`, register:

```go
r.Post("/kanban/tasks/{task}/fail", s.handleKanbanTaskFail)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd apps/kittypaw && go test ./server -run TestKanbanAPITaskFailRecordsFailedRun -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/kittypaw/server/api_kanban.go apps/kittypaw/server/server.go apps/kittypaw/server/api_kanban_test.go
git commit -m "feat(server): expose kanban run failure"
```

## Task 3: CLI Exec Command

**Files:**
- Modify: `apps/kittypaw/cli/cmd_kanban.go`
- Modify: `apps/kittypaw/cli/cmd_kanban_test.go`

- [ ] **Step 1: Write failing CLI tests**

Update `TestKanbanCommandExposesTaskWorkflow` to include `{"kanban", "exec"}`.

Update `TestKanbanCommandFlags` with:

```go
execCmd := mustFindCommand(t, root, []string{"kanban", "exec"})
for _, flag := range []string{"actor", "work-dir", "summary", "account"} {
	if execCmd.Flag(flag) == nil {
		t.Fatalf("kanban exec missing --%s", flag)
	}
}
```

Append integration tests:

```go
func TestKanbanExecCompletesTaskAfterSuccessfulCommand(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))
	projectRoot := t.TempDir()

	st, err := openStoreForAccount("alice")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	project, err := st.CreateKanbanProject(store.CreateKanbanProjectRequest{
		Slug:     "kitty",
		Name:     "KittyPaw",
		RootPath: projectRoot,
	})
	if err != nil {
		t.Fatalf("CreateKanbanProject: %v", err)
	}
	task, err := st.CreateKanbanTask(store.CreateKanbanTaskRequest{
		ProjectID: project.ID,
		Title:     "Write output",
		Status:    store.KanbanStatusTodo,
	})
	if err != nil {
		t.Fatalf("CreateKanbanTask: %v", err)
	}
	_ = st.Close()

	err = runKanbanExec(task.ID, []string{"sh", "-c", "printf ok > exec-output.txt"}, &kanbanExecFlags{
		shared: &kanbanSharedFlags{accountID: "alice"},
		actor:  "alice",
	})
	if err != nil {
		t.Fatalf("runKanbanExec: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(projectRoot, "exec-output.txt")); err != nil || string(data) != "ok" {
		t.Fatalf("exec output = %q err=%v", string(data), err)
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
	if got.Status != store.KanbanStatusDone {
		t.Fatalf("task status = %q, want done", got.Status)
	}
	runs, err := st.ListKanbanRuns(task.ID)
	if err != nil {
		t.Fatalf("ListKanbanRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].Outcome != store.KanbanRunCompleted || !strings.Contains(runs[0].MetadataJSON, `"exit_code":0`) || !strings.Contains(runs[0].MetadataJSON, `"command":["sh","-c","printf ok > exec-output.txt"]`) {
		t.Fatalf("runs = %+v", runs)
	}
}

func TestKanbanExecRecordsFailedRunAfterCommandFailure(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))
	projectRoot := t.TempDir()

	st, err := openStoreForAccount("alice")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	project, err := st.CreateKanbanProject(store.CreateKanbanProjectRequest{
		Slug:     "kitty",
		Name:     "KittyPaw",
		RootPath: projectRoot,
	})
	if err != nil {
		t.Fatalf("CreateKanbanProject: %v", err)
	}
	task, err := st.CreateKanbanTask(store.CreateKanbanTaskRequest{
		ProjectID: project.ID,
		Title:     "Fail command",
		Status:    store.KanbanStatusTodo,
	})
	if err != nil {
		t.Fatalf("CreateKanbanTask: %v", err)
	}
	_ = st.Close()

	err = runKanbanExec(task.ID, []string{"sh", "-c", "exit 7"}, &kanbanExecFlags{
		shared: &kanbanSharedFlags{accountID: "alice"},
		actor:  "alice",
	})
	if err == nil {
		t.Fatal("expected runKanbanExec to return command failure")
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
	if got.Status != store.KanbanStatusTodo {
		t.Fatalf("task status = %q, want todo", got.Status)
	}
	runs, err := st.ListKanbanRuns(task.ID)
	if err != nil {
		t.Fatalf("ListKanbanRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].Outcome != store.KanbanRunFailed || runs[0].Error == "" || !strings.Contains(runs[0].MetadataJSON, `"exit_code":7`) {
		t.Fatalf("runs = %+v", runs)
	}
}
```

Add imports to `cmd_kanban_test.go`:

```go
import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/store"
)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd apps/kittypaw && go test ./cli -run 'TestKanbanCommand|TestKanbanExec' -count=1`

Expected: FAIL because `kanban exec` and `runKanbanExec` are missing.

- [ ] **Step 3: Implement command**

In `cmd_kanban.go`, add imports:

```go
"errors"
osexec "os/exec"
```

Add flags type:

```go
type kanbanExecFlags struct {
	shared  *kanbanSharedFlags
	actor   string
	workDir string
	summary string
}
```

Add `newKanbanExecCmd(flags)` to `newKanbanCmd`.

Define:

```go
func newKanbanExecCmd(shared *kanbanSharedFlags) *cobra.Command {
	flags := &kanbanExecFlags{shared: shared}
	cmd := &cobra.Command{
		Use:   "exec <task> -- <command> [args...]",
		Short: "Execute a command for a Kanban task",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) < 2 {
				return fmt.Errorf("usage: kittypaw kanban exec <task> -- <command> [args...]")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKanbanExec(args[0], args[1:], flags)
		},
	}
	cmd.Flags().StringVar(&flags.actor, "actor", "", "actor name")
	cmd.Flags().StringVar(&flags.workDir, "work-dir", "", "run working directory")
	cmd.Flags().StringVar(&flags.summary, "summary", "", "completion summary")
	return cmd
}
```

Add `runKanbanExec`, metadata helper, default summary helper, and exit-code helper:

```go
func runKanbanExec(taskID string, command []string, flags *kanbanExecFlags) error {
	if flags == nil {
		flags = &kanbanExecFlags{shared: &kanbanSharedFlags{}}
	}
	if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
		return fmt.Errorf("command is required")
	}
	workDir, provider, err := normalizeRunWorkDir(flags.workDir)
	if err != nil {
		return err
	}
	st, err := openKanbanCommandStore(kanbanAccountID(flags.shared))
	if err != nil {
		return err
	}
	defer st.Close()

	taskID = strings.TrimSpace(taskID)
	run, err := st.ClaimKanbanTask(taskID, store.ClaimKanbanTaskRequest{
		Actor:           strings.TrimSpace(flags.actor),
		WorkDir:         workDir,
		WorkDirProvider: provider,
	})
	if err != nil {
		return err
	}

	started := time.Now()
	cmd := osexec.Command(command[0], command[1:]...)
	cmd.Dir = run.WorkDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	runErr := cmd.Run()
	duration := time.Since(started)
	exitCode := kanbanExecExitCode(runErr)
	metadata := kanbanExecMetadata(command, run.ID, exitCode, duration)
	if runErr != nil {
		summary := strings.TrimSpace(flags.summary)
		if summary == "" {
			summary = kanbanExecDefaultSummary("command failed", command)
		}
		recordErr := st.FailKanbanTask(taskID, store.FailKanbanTaskRequest{
			Actor:        strings.TrimSpace(flags.actor),
			Summary:      summary,
			Error:        runErr.Error(),
			MetadataJSON: metadata,
		})
		if recordErr != nil {
			return fmt.Errorf("command failed (%v); record kanban failure: %w", runErr, recordErr)
		}
		return fmt.Errorf("command failed with exit code %d: %w", exitCode, runErr)
	}

	summary := strings.TrimSpace(flags.summary)
	if summary == "" {
		summary = kanbanExecDefaultSummary("command completed", command)
	}
	if err := st.CompleteKanbanTask(taskID, store.CompleteKanbanTaskRequest{
		Actor:        strings.TrimSpace(flags.actor),
		Summary:      summary,
		MetadataJSON: metadata,
	}); err != nil {
		return err
	}
	fmt.Printf("Executed task: %s\n", taskID)
	fmt.Printf("Run: %s\n", run.ID)
	fmt.Printf("Work dir: %s\n", run.WorkDir)
	return nil
}

func kanbanExecMetadata(command []string, runID string, exitCode int, duration time.Duration) string {
	raw, err := json.Marshal(map[string]any{
		"command":     command,
		"duration_ms": duration.Milliseconds(),
		"exit_code":   exitCode,
		"run_id":      runID,
	})
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func kanbanExecDefaultSummary(prefix string, command []string) string {
	return prefix + ": " + strings.Join(command, " ")
}

func kanbanExecExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *osexec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd apps/kittypaw && go test ./cli -run 'TestKanbanCommand|TestKanbanExec' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/kittypaw/cli/cmd_kanban.go apps/kittypaw/cli/cmd_kanban_test.go
git commit -m "feat(cli): execute kanban task commands"
```

## Task 4: Verification And Review

**Files:**
- Review all branch changes.

- [ ] **Step 1: Run focused tests**

Run:

```bash
cd apps/kittypaw
go test ./store -run 'TestKanban.*Fail' -count=1
go test ./server -run 'TestKanbanAPI.*Fail' -count=1
go test ./cli -run 'TestKanbanCommand|TestKanbanExec' -count=1
```

Expected: PASS.

- [ ] **Step 2: Run full short suite**

Run: `cd apps/kittypaw && go test ./... -short -count=1`

Expected: PASS.

- [ ] **Step 3: Local code review**

Review:

```bash
git diff main...HEAD -- apps/kittypaw/store/kanban.go apps/kittypaw/store/kanban_test.go apps/kittypaw/server/api_kanban.go apps/kittypaw/server/server.go apps/kittypaw/server/api_kanban_test.go apps/kittypaw/cli/cmd_kanban.go apps/kittypaw/cli/cmd_kanban_test.go
```

Check:

- Failed command always closes the running Run.
- No task can be failed without a running Run.
- Command metadata is valid JSON and includes command, run id, duration, exit code.
- CLI uses the recorded Run work dir, not the current process directory.
- API missing task routes still return 404.
- The product terminology stays `Project root` and `Run work dir`.

- [ ] **Step 4: Fix review findings with tests first**

For each important finding, add or adjust a test, run it red, implement the fix,
and run it green.

- [ ] **Step 5: Final verification**

Run: `cd apps/kittypaw && go test ./... -short -count=1`

Expected: PASS.

- [ ] **Step 6: Commit review fixes if any**

```bash
git add apps/kittypaw/store/kanban.go apps/kittypaw/store/kanban_test.go apps/kittypaw/server/api_kanban.go apps/kittypaw/server/server.go apps/kittypaw/server/api_kanban_test.go apps/kittypaw/cli/cmd_kanban.go apps/kittypaw/cli/cmd_kanban_test.go
git commit -m "fix: address kanban run integration review"
```

Skip this commit only if the review finds no code changes are needed.
