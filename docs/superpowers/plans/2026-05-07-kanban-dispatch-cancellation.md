# Kanban Dispatch Cancellation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make dispatch worker cancellation observable through the root Cobra context and record interrupted dispatch Runs as failed.

**Architecture:** Thread a signal-aware context into root command execution with `ExecuteContext`, then teach `executeDispatchedKanbanTask` to use a cancellation-specific default summary when `cmd.Run()` fails after context cancellation. Reuse the existing failed Run persistence path.

**Tech Stack:** Go, Cobra CLI, standard `context`, `os/signal`, and existing Kanban CLI/store test helpers.

---

### Task 1: Context and Cancellation Tests

**Files:**
- Modify: `apps/kittypaw/cli/main_test.go`
- Modify: `apps/kittypaw/cli/cmd_kanban_test.go`

- [ ] **Step 1: Add root context propagation test**

Add this test to `apps/kittypaw/cli/main_test.go`:

```go
func TestRootCommandPropagatesContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	root := newRootCmd()
	var sawCanceled bool
	root.AddCommand(&cobra.Command{
		Use: "context-probe",
		RunE: func(cmd *cobra.Command, args []string) error {
			sawCanceled = errors.Is(cmd.Context().Err(), context.Canceled)
			return nil
		},
	})
	root.SetArgs([]string{"context-probe"})
	if err := root.ExecuteContext(ctx); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}
	if !sawCanceled {
		t.Fatal("command did not observe canceled context")
	}
}
```

Add imports if missing:

```go
import (
	"context"
	"errors"
	"testing"

	"github.com/spf13/cobra"
)
```

- [ ] **Step 2: Add dispatch cancellation persistence test**

Add this test after the existing dispatch tests in `apps/kittypaw/cli/cmd_kanban_test.go`:

```go
func TestKanbanDispatchRecordsCanceledWorker(t *testing.T) {
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
	task, err := st.CreateKanbanTask(store.CreateKanbanTaskRequest{ProjectID: project.ID, Title: "Cancel dispatch", Status: store.KanbanStatusReady})
	if err != nil {
		t.Fatalf("CreateKanbanTask: %v", err)
	}
	_ = st.Close()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err = runKanbanDispatch(
		ctx,
		[]string{"sh", "-c", "sleep 5"},
		&kanbanDispatchFlags{
			shared:  &kanbanSharedFlags{accountID: "alice"},
			project: "kitty",
			actor:   "dispatcher",
			limit:   1,
		},
	)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("runKanbanDispatch error = %v, want context canceled", err)
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
	if len(runs) != 1 || runs[0].Outcome != store.KanbanRunFailed || !strings.Contains(runs[0].Summary, "command canceled") {
		t.Fatalf("runs = %+v", runs)
	}
}
```

Add imports if missing:

```go
import (
	"context"
	"errors"
)
```

- [ ] **Step 3: Run focused failing tests**

Run:

```bash
cd apps/kittypaw
go test ./cli -run 'TestRootCommandPropagatesContext|TestKanbanDispatchRecordsCanceledWorker' -count=1
```

Expected: FAIL because root `main` does not yet use `ExecuteContext`, imports are not yet adjusted, or dispatch does not return a context-canceled wrapped error.

### Task 2: Implement Cancellation Support

**Files:**
- Modify: `apps/kittypaw/cli/main.go`
- Modify: `apps/kittypaw/cli/cmd_kanban.go`

- [ ] **Step 1: Execute root command with a signal context**

Replace `main` in `apps/kittypaw/cli/main.go` with:

```go
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	root := newRootCmd()
	if err := root.ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}
```

- [ ] **Step 2: Return cancellation-aware dispatch errors**

In `executeDispatchedKanbanTask`, inside the `runErr != nil` block, set the
default summary and return error based on `ctx.Err()`:

```go
summaryPrefix := "command failed"
if ctxErr := ctx.Err(); ctxErr != nil {
	summaryPrefix = "command canceled"
}
summary := strings.TrimSpace(flags.summary)
if summary == "" {
	summary = kanbanExecDefaultSummary(summaryPrefix, command)
}
```

After recording failure:

```go
if ctxErr := ctx.Err(); ctxErr != nil {
	return fmt.Errorf("command canceled: %w", ctxErr)
}
return fmt.Errorf("command failed with exit code %d: %w", exitCode, runErr)
```

- [ ] **Step 3: Run focused passing tests**

Run:

```bash
cd apps/kittypaw
go test ./cli -run 'TestRootCommandPropagatesContext|TestKanbanDispatchRecordsCanceledWorker|TestKanbanDispatch' -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit implementation**

Run:

```bash
git add apps/kittypaw/cli/main.go apps/kittypaw/cli/main_test.go apps/kittypaw/cli/cmd_kanban.go apps/kittypaw/cli/cmd_kanban_test.go
git commit -m "fix(cli): record canceled kanban dispatch runs"
```

### Task 3: Verification

**Files:**
- Inspect: `apps/kittypaw/cli/main.go`
- Inspect: `apps/kittypaw/cli/cmd_kanban.go`

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
