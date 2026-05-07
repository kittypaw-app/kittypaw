# Kanban Dispatch Stale Recovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend `kanban dispatch` with stale-run reclaim and max-attempt blocking.

**Architecture:** Keep stale recovery inside the existing CLI dispatcher cycle. Use the store's `ListStaleKanbanRuns`, `ReclaimKanbanTask`, `ListKanbanRuns`, and `BlockKanbanTask` methods; refactor command execution so both claimed ready Runs and reclaimed stale Runs share one execution helper.

**Tech Stack:** Go, Cobra CLI, existing Kanban store APIs, `os/exec`, existing CLI test helpers.

---

### Task 1: Failing Stale Recovery Tests

**Files:**
- Modify: `apps/kittypaw/cli/cmd_kanban_test.go`

- [ ] **Step 1: Add new dispatch flag assertions**

Extend the dispatch flag list in `TestKanbanCommandFlags`:

```go
for _, flag := range []string{"project", "limit", "loop", "interval", "actor", "work-dir", "summary", "reclaim-stale-after", "max-attempts", "account"} {
```

- [ ] **Step 2: Add stale reclaim execution test**

Add after `TestKanbanDispatchRecordsCanceledWorker`:

```go
func TestKanbanDispatchReclaimsStaleRunAndExecutesCommand(t *testing.T) {
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
	task, err := st.CreateKanbanTask(store.CreateKanbanTaskRequest{ProjectID: project.ID, Title: "Recover stale", Status: store.KanbanStatusTodo})
	if err != nil {
		t.Fatalf("CreateKanbanTask: %v", err)
	}
	if _, err := st.ClaimKanbanTask(task.ID, store.ClaimKanbanTaskRequest{Actor: "old-worker"}); err != nil {
		t.Fatalf("ClaimKanbanTask: %v", err)
	}
	_ = st.Close()
	time.Sleep(1100 * time.Millisecond)

	err = runKanbanDispatch(
		t.Context(),
		[]string{"sh", "-c", "printf recovered > recovered.txt"},
		&kanbanDispatchFlags{
			shared:            &kanbanSharedFlags{accountID: "alice"},
			project:           "kitty",
			limit:             1,
			reclaimStaleAfter: "500ms",
		},
	)
	if err != nil {
		t.Fatalf("runKanbanDispatch: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(projectRoot, "recovered.txt")); err != nil || string(data) != "recovered" {
		t.Fatalf("recovered output = %q err=%v", string(data), err)
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
	var reclaimed, completed int
	for _, run := range runs {
		if run.Outcome == store.KanbanRunReclaimed {
			reclaimed++
		}
		if run.Outcome == store.KanbanRunCompleted {
			completed++
			if run.Actor != "dispatcher" {
				t.Fatalf("completed run actor = %q, want dispatcher", run.Actor)
			}
		}
	}
	if len(runs) != 2 || reclaimed != 1 || completed != 1 {
		t.Fatalf("runs = %+v", runs)
	}
}
```

- [ ] **Step 3: Add max-attempt blocking test**

Add:

```go
func TestKanbanDispatchBlocksReadyTaskAtMaxAttempts(t *testing.T) {
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
	task, err := st.CreateKanbanTask(store.CreateKanbanTaskRequest{ProjectID: project.ID, Title: "Too many failures", Status: store.KanbanStatusReady})
	if err != nil {
		t.Fatalf("CreateKanbanTask: %v", err)
	}
	if _, err := st.ClaimKanbanTask(task.ID, store.ClaimKanbanTaskRequest{Actor: "worker"}); err != nil {
		t.Fatalf("ClaimKanbanTask: %v", err)
	}
	if err := st.FailKanbanTask(task.ID, store.FailKanbanTaskRequest{Actor: "worker", Summary: "failed once", Error: "exit 1"}); err != nil {
		t.Fatalf("FailKanbanTask: %v", err)
	}
	ready := store.KanbanStatusReady
	if _, err := st.UpdateKanbanTask(task.ID, store.UpdateKanbanTaskRequest{Actor: "alice", Status: &ready}); err != nil {
		t.Fatalf("UpdateKanbanTask: %v", err)
	}
	_ = st.Close()

	err = runKanbanDispatch(
		t.Context(),
		[]string{"sh", "-c", "printf should-not-run > blocked.txt"},
		&kanbanDispatchFlags{
			shared:      &kanbanSharedFlags{accountID: "alice"},
			project:     "kitty",
			limit:       1,
			maxAttempts: 1,
		},
	)
	if err != nil {
		t.Fatalf("runKanbanDispatch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectRoot, "blocked.txt")); err == nil {
		t.Fatal("worker command ran despite max attempts")
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
	if got.Status != store.KanbanStatusBlocked {
		t.Fatalf("task status = %q, want blocked", got.Status)
	}
	runs, err := st.ListKanbanRuns(task.ID)
	if err != nil {
		t.Fatalf("ListKanbanRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].Outcome != store.KanbanRunFailed {
		t.Fatalf("runs = %+v", runs)
	}
}
```

- [ ] **Step 4: Extend validation test**

Add cases to `TestKanbanDispatchValidatesInputs`:

```go
{name: "bad stale duration", command: []string{"sh"}, flags: &kanbanDispatchFlags{project: "kitty", limit: 1, reclaimStaleAfter: "0s"}, want: "positive --reclaim-stale-after duration is required"},
{name: "bad max attempts", command: []string{"sh"}, flags: &kanbanDispatchFlags{project: "kitty", limit: 1, maxAttempts: -1}, want: "--max-attempts must not be negative"},
```

- [ ] **Step 5: Run focused failing tests**

Run:

```bash
cd apps/kittypaw
go test ./cli -run 'TestKanbanCommand|TestKanbanDispatch' -count=1
```

Expected: FAIL because `kanbanDispatchFlags` has no stale recovery fields and dispatch has no stale recovery behavior.

### Task 2: Implement Stale Recovery

**Files:**
- Modify: `apps/kittypaw/cli/cmd_kanban.go`

- [ ] **Step 1: Extend flags and command registration**

Add fields to `kanbanDispatchFlags`:

```go
reclaimStaleAfter string
maxAttempts       int
```

Add command flags in `newKanbanDispatchCmd`:

```go
cmd.Flags().StringVar(&flags.reclaimStaleAfter, "reclaim-stale-after", "", "reclaim and dispatch running tasks stale beyond this duration")
cmd.Flags().IntVar(&flags.maxAttempts, "max-attempts", 0, "block tasks with at least this many failed runs; 0 disables the limit")
```

- [ ] **Step 2: Parse stale recovery options**

Add:

```go
type kanbanDispatchOptions struct {
	staleAfter time.Duration
	stale      bool
}
```

In `runKanbanDispatch`, validate:

```go
opts, err := parseKanbanDispatchOptions(flags)
if err != nil {
	return err
}
```

Add helper:

```go
func parseKanbanDispatchOptions(flags *kanbanDispatchFlags) (kanbanDispatchOptions, error) {
	if flags.maxAttempts < 0 {
		return kanbanDispatchOptions{}, fmt.Errorf("--max-attempts must not be negative")
	}
	raw := strings.TrimSpace(flags.reclaimStaleAfter)
	if raw == "" {
		return kanbanDispatchOptions{}, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return kanbanDispatchOptions{}, fmt.Errorf("positive --reclaim-stale-after duration is required")
	}
	return kanbanDispatchOptions{staleAfter: d, stale: true}, nil
}
```

- [ ] **Step 3: Process stale tasks before ready tasks**

Change `runKanbanDispatchCycle` to accept options and process stale Runs first:

```go
func runKanbanDispatchCycle(ctx context.Context, st *store.Store, project *store.KanbanProject, command []string, flags *kanbanDispatchFlags, opts kanbanDispatchOptions, workDir, provider string) (int, error)
```

Inside it:

```go
processed := 0
if opts.stale {
	cutoff := time.Now().UTC().Add(-opts.staleAfter).Format("2006-01-02T15:04:05Z")
	staleRuns, err := st.ListStaleKanbanRuns(store.KanbanStaleRunFilter{ProjectID: project.ID, StaleBefore: cutoff, Limit: flags.limit})
	if err != nil {
		return 0, err
	}
	for _, item := range staleRuns {
		if processed >= flags.limit {
			break
		}
		if handled, err := maybeBlockKanbanDispatchTask(st, item.Task.ID, flags); err != nil {
			return processed, err
		} else if handled {
			processed++
			continue
		}
		if err := reclaimAndExecuteKanbanTask(ctx, st, project, item.Task, command, flags, workDir, provider); err != nil {
			return processed, err
		}
		processed++
	}
}
```

Then keep ready task handling, but skip if `processed >= flags.limit`, and call
`maybeBlockKanbanDispatchTask` before claiming.

- [ ] **Step 4: Add actor, block, attempt, and execution helpers**

Add:

```go
func kanbanDispatchActor(flags *kanbanDispatchFlags) string {
	actor := strings.TrimSpace(flags.actor)
	if actor == "" && strings.TrimSpace(flags.reclaimStaleAfter) != "" {
		return "dispatcher"
	}
	if actor == "" && flags.maxAttempts > 0 {
		return "dispatcher"
	}
	return actor
}

func maybeBlockKanbanDispatchTask(st *store.Store, taskID string, flags *kanbanDispatchFlags) (bool, error) {
	if flags.maxAttempts <= 0 {
		return false, nil
	}
	attempts, err := failedKanbanDispatchAttempts(st, taskID)
	if err != nil {
		return false, err
	}
	if attempts < flags.maxAttempts {
		return false, nil
	}
	if err := st.BlockKanbanTask(taskID, store.BlockKanbanTaskRequest{
		Actor:  kanbanDispatchActor(flags),
		Reason: "max attempts reached",
	}); err != nil {
		return false, err
	}
	fmt.Printf("Blocked task: %s\n", taskID)
	fmt.Println("Reason: max attempts reached")
	return true, nil
}

func failedKanbanDispatchAttempts(st *store.Store, taskID string) (int, error) {
	runs, err := st.ListKanbanRuns(taskID)
	if err != nil {
		return 0, err
	}
	var attempts int
	for _, run := range runs {
		if run.Outcome == store.KanbanRunFailed {
			attempts++
		}
	}
	return attempts, nil
}
```

Refactor `executeDispatchedKanbanTask` into:

```go
func claimAndExecuteKanbanTask(...)
func reclaimAndExecuteKanbanTask(...)
func executeDispatchedKanbanRun(...)
```

Use `kanbanDispatchActor(flags)` for claim/reclaim/complete/fail/block actor.

- [ ] **Step 5: Run focused passing tests**

Run:

```bash
cd apps/kittypaw
go test ./cli -run 'TestKanbanCommand|TestKanbanDispatch' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit implementation**

Run:

```bash
git add apps/kittypaw/cli/cmd_kanban.go apps/kittypaw/cli/cmd_kanban_test.go
git commit -m "feat(cli): recover stale kanban dispatch runs"
```

### Task 3: Verification

**Files:**
- Inspect: `apps/kittypaw/cli/cmd_kanban.go`
- Inspect: `apps/kittypaw/cli/cmd_kanban_test.go`

- [ ] **Step 1: Run CLI tests**

Run:

```bash
cd apps/kittypaw
go test ./cli -count=1
```

Expected: PASS.

- [ ] **Step 2: Run app short tests**

Run:

```bash
cd apps/kittypaw
go test ./... -short -count=1
```

Expected: PASS.
