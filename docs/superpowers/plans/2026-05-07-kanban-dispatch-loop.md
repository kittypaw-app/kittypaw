# Kanban Dispatch Loop Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `kittypaw kanban dispatch` so the CLI can find ready project tasks, claim them, run a command or worker, and record completion or failure.

**Architecture:** Keep this as a CLI composition over the existing durable Kanban store. Reuse `ListKanbanTasks`, `ClaimKanbanTask`, `CompleteKanbanTask`, `FailKanbanTask`, `normalizeRunWorkDir`, and `kanbanExecMetadata`; add only the project-scoped ready-task loop and worker environment.

**Tech Stack:** Go, Cobra CLI, existing SQLite-backed `store` package, standard `os/exec`, existing `apps/kittypaw/cli` tests.

---

### Task 1: Command Surface Tests

**Files:**
- Modify: `apps/kittypaw/cli/cmd_kanban_test.go`

- [ ] **Step 1: Add failing command exposure assertions**

Update `TestKanbanCommandExposesTaskWorkflow` so the command list includes:

```go
{"kanban", "dispatch"},
```

Place it next to `{"kanban", "exec"}` because both commands run workers.

- [ ] **Step 2: Add failing flag assertions**

In `TestKanbanCommandFlags`, after the `execCmd` block, add:

```go
dispatch := mustFindCommand(t, root, []string{"kanban", "dispatch"})
for _, flag := range []string{"project", "limit", "loop", "interval", "actor", "work-dir", "summary", "account"} {
	if dispatch.Flag(flag) == nil {
		t.Fatalf("kanban dispatch missing --%s", flag)
	}
}
```

- [ ] **Step 3: Run the focused failing test**

Run:

```bash
cd apps/kittypaw
go test ./cli -run 'TestKanbanCommand' -count=1
```

Expected: FAIL because `kanban dispatch` is not registered.

### Task 2: Dispatch Behavior Tests

**Files:**
- Modify: `apps/kittypaw/cli/cmd_kanban_test.go`

- [ ] **Step 1: Add successful dispatch test**

Add this test after `TestKanbanExecRecordsFailedRunAfterCommandFailure`:

```go
func TestKanbanDispatchRunsReadyTaskCommand(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))
	projectRoot := t.TempDir()

	st, err := openStoreForAccount("alice")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	project, err := st.CreateKanbanProject(store.CreateKanbanProjectRequest{Slug: "kitty", Name: "KittyPaw", RootPath: projectRoot})
	if err != nil {
		t.Fatalf("CreateKanbanProject: %v", err)
	}
	task, err := st.CreateKanbanTask(store.CreateKanbanTaskRequest{ProjectID: project.ID, Title: "Dispatch ready task", Status: store.KanbanStatusReady})
	if err != nil {
		t.Fatalf("CreateKanbanTask: %v", err)
	}
	_ = st.Close()

	err = runKanbanDispatch(
		t.Context(),
		[]string{"sh", "-c", "printf dispatch-ok > dispatch-output.txt"},
		&kanbanDispatchFlags{
			shared:  &kanbanSharedFlags{accountID: "alice"},
			project: "kitty",
			actor:   "dispatcher",
			limit:   1,
		},
	)
	if err != nil {
		t.Fatalf("runKanbanDispatch: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(projectRoot, "dispatch-output.txt")); err != nil || string(data) != "dispatch-ok" {
		t.Fatalf("dispatch output = %q err=%v", string(data), err)
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
	if len(runs) != 1 || runs[0].Actor != "dispatcher" || runs[0].Outcome != store.KanbanRunCompleted || !strings.Contains(runs[0].MetadataJSON, `"exit_code":0`) {
		t.Fatalf("runs = %+v", runs)
	}
}
```

- [ ] **Step 2: Add environment propagation test**

Add:

```go
func TestKanbanDispatchExposesWorkerEnvironment(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))
	projectRoot := t.TempDir()

	st, err := openStoreForAccount("alice")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	project, err := st.CreateKanbanProject(store.CreateKanbanProjectRequest{Slug: "kitty", Name: "KittyPaw", RootPath: projectRoot})
	if err != nil {
		t.Fatalf("CreateKanbanProject: %v", err)
	}
	task, err := st.CreateKanbanTask(store.CreateKanbanTaskRequest{ProjectID: project.ID, Title: "Env task", Status: store.KanbanStatusReady})
	if err != nil {
		t.Fatalf("CreateKanbanTask: %v", err)
	}
	_ = st.Close()

	err = runKanbanDispatch(
		t.Context(),
		[]string{"sh", "-c", "printf '%s|%s|%s|%s|%s' \"$KITTYPAW_KANBAN_TASK_ID\" \"$KITTYPAW_KANBAN_RUN_ID\" \"$KITTYPAW_KANBAN_PROJECT_ID\" \"$KITTYPAW_KANBAN_PROJECT_SLUG\" \"$KITTYPAW_KANBAN_TASK_TITLE\" > dispatch-env.txt"},
		&kanbanDispatchFlags{
			shared:  &kanbanSharedFlags{accountID: "alice"},
			project: "kitty",
			actor:   "dispatcher",
			limit:   1,
		},
	)
	if err != nil {
		t.Fatalf("runKanbanDispatch: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(projectRoot, "dispatch-env.txt"))
	if err != nil {
		t.Fatalf("read dispatch-env.txt: %v", err)
	}
	parts := strings.Split(string(data), "|")
	if len(parts) != 5 {
		t.Fatalf("env output = %q", string(data))
	}
	if parts[0] != task.ID || parts[1] == "" || parts[2] != project.ID || parts[3] != "kitty" || parts[4] != "Env task" {
		t.Fatalf("env output = %q", string(data))
	}
}
```

- [ ] **Step 3: Add failure and empty queue tests**

Add:

```go
func TestKanbanDispatchRecordsFailedRun(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))
	projectRoot := t.TempDir()

	st, err := openStoreForAccount("alice")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	project, err := st.CreateKanbanProject(store.CreateKanbanProjectRequest{Slug: "kitty", Name: "KittyPaw", RootPath: projectRoot})
	if err != nil {
		t.Fatalf("CreateKanbanProject: %v", err)
	}
	task, err := st.CreateKanbanTask(store.CreateKanbanTaskRequest{ProjectID: project.ID, Title: "Fail dispatch", Status: store.KanbanStatusReady})
	if err != nil {
		t.Fatalf("CreateKanbanTask: %v", err)
	}
	_ = st.Close()

	err = runKanbanDispatch(
		t.Context(),
		[]string{"sh", "-c", "exit 7"},
		&kanbanDispatchFlags{
			shared:  &kanbanSharedFlags{accountID: "alice"},
			project: "kitty",
			actor:   "dispatcher",
			limit:   1,
		},
	)
	if err == nil {
		t.Fatal("expected runKanbanDispatch to return command failure")
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
	if len(runs) != 1 || runs[0].Outcome != store.KanbanRunFailed || !strings.Contains(runs[0].MetadataJSON, `"exit_code":7`) {
		t.Fatalf("runs = %+v", runs)
	}
}

func TestKanbanDispatchPrintsEmptyReadyQueue(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))

	st, err := openStoreForAccount("alice")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := st.CreateKanbanProject(store.CreateKanbanProjectRequest{Slug: "kitty", Name: "KittyPaw", RootPath: t.TempDir()}); err != nil {
		t.Fatalf("CreateKanbanProject: %v", err)
	}
	_ = st.Close()

	var runErr error
	out := captureStdout(t, func() {
		runErr = runKanbanDispatch(
			t.Context(),
			[]string{"sh", "-c", "exit 99"},
			&kanbanDispatchFlags{
				shared:  &kanbanSharedFlags{accountID: "alice"},
				project: "kitty",
				limit:   1,
			},
		)
	})
	if runErr != nil {
		t.Fatalf("runKanbanDispatch: %v", runErr)
	}
	if !strings.Contains(out, "No ready tasks.") {
		t.Fatalf("dispatch output = %q", out)
	}
}
```

- [ ] **Step 4: Add validation tests**

Add:

```go
func TestKanbanDispatchValidatesInputs(t *testing.T) {
	tests := []struct {
		name    string
		command []string
		flags   *kanbanDispatchFlags
		want    string
	}{
		{name: "missing command", command: nil, flags: &kanbanDispatchFlags{project: "kitty", limit: 1}, want: "command is required"},
		{name: "missing project", command: []string{"sh"}, flags: &kanbanDispatchFlags{limit: 1}, want: "--project is required"},
		{name: "bad limit", command: []string{"sh"}, flags: &kanbanDispatchFlags{project: "kitty", limit: 0}, want: "--limit must be positive"},
		{name: "bad interval", command: []string{"sh"}, flags: &kanbanDispatchFlags{project: "kitty", limit: 1, interval: "0s"}, want: "positive --interval duration is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runKanbanDispatch(t.Context(), tt.command, tt.flags)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("runKanbanDispatch error = %v, want %q", err, tt.want)
			}
		})
	}
}
```

- [ ] **Step 5: Run the focused failing behavior tests**

Run:

```bash
cd apps/kittypaw
go test ./cli -run 'TestKanbanDispatch' -count=1
```

Expected: FAIL because `runKanbanDispatch` and `kanbanDispatchFlags` are not defined.

### Task 3: Implement Dispatch Command

**Files:**
- Modify: `apps/kittypaw/cli/cmd_kanban.go`

- [ ] **Step 1: Add context import and dispatch flags**

Add `context` to the import block. Add this struct near `kanbanExecFlags`:

```go
type kanbanDispatchFlags struct {
	shared   *kanbanSharedFlags
	project  string
	actor    string
	workDir  string
	summary  string
	limit    int
	loop     bool
	interval string
}
```

- [ ] **Step 2: Register `kanban dispatch`**

Add `newKanbanDispatchCmd(flags)` to `newKanbanCmd`, next to `newKanbanExecCmd(flags)`.

Add:

```go
func newKanbanDispatchCmd(shared *kanbanSharedFlags) *cobra.Command {
	flags := &kanbanDispatchFlags{shared: shared, limit: 1, interval: "30s"}
	cmd := &cobra.Command{
		Use:   "dispatch --project <project> -- <command> [args...]",
		Short: "Dispatch ready Kanban tasks to a command",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("usage: kittypaw kanban dispatch --project <project> -- <command> [args...]")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKanbanDispatch(cmd.Context(), args, flags)
		},
	}
	cmd.Flags().StringVar(&flags.project, "project", "", "project id or slug")
	cmd.Flags().IntVar(&flags.limit, "limit", 1, "maximum ready tasks to dispatch per cycle")
	cmd.Flags().BoolVar(&flags.loop, "loop", false, "keep polling for ready tasks")
	cmd.Flags().StringVar(&flags.interval, "interval", "30s", "poll interval when --loop is set")
	cmd.Flags().StringVar(&flags.actor, "actor", "", "actor name")
	cmd.Flags().StringVar(&flags.workDir, "work-dir", "", "run working directory")
	cmd.Flags().StringVar(&flags.summary, "summary", "", "completion summary")
	return cmd
}
```

- [ ] **Step 3: Add dispatch implementation helpers**

Add these helpers near `runKanbanExec`:

```go
func runKanbanDispatch(ctx context.Context, command []string, flags *kanbanDispatchFlags) error {
	if flags == nil {
		flags = &kanbanDispatchFlags{shared: &kanbanSharedFlags{}, limit: 1, interval: "30s"}
	}
	if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
		return fmt.Errorf("command is required")
	}
	if strings.TrimSpace(flags.project) == "" {
		return fmt.Errorf("--project is required")
	}
	if flags.limit <= 0 {
		return fmt.Errorf("--limit must be positive")
	}
	intervalRaw := strings.TrimSpace(flags.interval)
	if intervalRaw == "" {
		intervalRaw = "30s"
	}
	interval, err := time.ParseDuration(intervalRaw)
	if err != nil || interval <= 0 {
		return fmt.Errorf("positive --interval duration is required")
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

	project, err := resolveKanbanProject(st, flags.project)
	if err != nil {
		return err
	}

	for {
		processed, err := runKanbanDispatchCycle(ctx, st, project, command, flags, workDir, provider)
		if err != nil {
			return err
		}
		if processed == 0 {
			fmt.Println("No ready tasks.")
		}
		if !flags.loop {
			return nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func runKanbanDispatchCycle(ctx context.Context, st *store.Store, project *store.KanbanProject, command []string, flags *kanbanDispatchFlags, workDir, provider string) (int, error) {
	tasks, err := st.ListKanbanTasks(store.KanbanTaskListFilter{
		ProjectID: project.ID,
		Status:    store.KanbanStatusReady,
	})
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, task := range tasks {
		if processed >= flags.limit {
			break
		}
		if err := executeDispatchedKanbanTask(ctx, st, project, task, command, flags, workDir, provider); err != nil {
			return processed, err
		}
		processed++
	}
	return processed, nil
}

func executeDispatchedKanbanTask(ctx context.Context, st *store.Store, project *store.KanbanProject, task store.KanbanTask, command []string, flags *kanbanDispatchFlags, workDir, provider string) error {
	run, err := st.ClaimKanbanTask(task.ID, store.ClaimKanbanTaskRequest{
		Actor:           strings.TrimSpace(flags.actor),
		WorkDir:         workDir,
		WorkDirProvider: provider,
	})
	if err != nil {
		return err
	}
	started := time.Now()
	cmd := osexec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = run.WorkDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = kanbanDispatchCommandEnv(os.Environ(), project, task, run)
	runErr := cmd.Run()
	duration := time.Since(started)
	exitCode := kanbanExecExitCode(runErr)
	metadata := kanbanExecMetadata(command, run.ID, exitCode, duration)
	if runErr != nil {
		summary := strings.TrimSpace(flags.summary)
		if summary == "" {
			summary = kanbanExecDefaultSummary("command failed", command)
		}
		recordErr := st.FailKanbanTask(task.ID, store.FailKanbanTaskRequest{
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
	if err := st.CompleteKanbanTask(task.ID, store.CompleteKanbanTaskRequest{
		Actor:        strings.TrimSpace(flags.actor),
		Summary:      summary,
		MetadataJSON: metadata,
	}); err != nil {
		return err
	}
	fmt.Printf("Dispatched task: %s\n", task.ID)
	fmt.Printf("Run: %s\n", run.ID)
	fmt.Printf("Work dir: %s\n", run.WorkDir)
	return nil
}

func kanbanDispatchCommandEnv(base []string, project *store.KanbanProject, task store.KanbanTask, run *store.KanbanRun) []string {
	return append(base,
		"KITTYPAW_KANBAN_TASK_ID="+task.ID,
		"KITTYPAW_KANBAN_RUN_ID="+run.ID,
		"KITTYPAW_KANBAN_PROJECT_ID="+project.ID,
		"KITTYPAW_KANBAN_PROJECT_SLUG="+project.Slug,
		"KITTYPAW_KANBAN_TASK_TITLE="+task.Title,
	)
}
```

- [ ] **Step 4: Run focused tests**

Run:

```bash
cd apps/kittypaw
go test ./cli -run 'TestKanbanCommand|TestKanbanDispatch' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit implementation**

Run:

```bash
git add apps/kittypaw/cli/cmd_kanban.go apps/kittypaw/cli/cmd_kanban_test.go
git commit -m "feat(cli): add kanban dispatch loop"
```

### Task 4: Verification and Review

**Files:**
- Inspect: `apps/kittypaw/cli/cmd_kanban.go`
- Inspect: `apps/kittypaw/cli/cmd_kanban_test.go`

- [ ] **Step 1: Run full CLI package tests**

Run:

```bash
cd apps/kittypaw
go test ./cli -count=1
```

Expected: PASS.

- [ ] **Step 2: Run short app tests**

Run:

```bash
cd apps/kittypaw
go test ./... -short -count=1
```

Expected: PASS.

- [ ] **Step 3: Review diff for behavioral risks**

Run:

```bash
git diff --stat main...HEAD
git diff main...HEAD -- apps/kittypaw/cli/cmd_kanban.go apps/kittypaw/cli/cmd_kanban_test.go
```

Check:

- `dispatch` is registered under `kanban`.
- validation happens before store open where possible.
- only `ready` tasks are selected.
- command argv is unchanged.
- worker context is passed by environment.
- failure records a failed Run before returning an error.
- loop waits on `cmd.Context()`.

- [ ] **Step 4: Commit any verification-only fixes**

If Step 3 finds a defect, patch it, rerun the focused test, and commit with:

```bash
git add apps/kittypaw/cli/cmd_kanban.go apps/kittypaw/cli/cmd_kanban_test.go
git commit -m "fix(cli): harden kanban dispatch loop"
```
