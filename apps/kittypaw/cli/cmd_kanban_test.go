package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/store"
)

func TestKanbanCommandExposesTaskWorkflow(t *testing.T) {
	root := newRootCmd()

	for _, path := range [][]string{
		{"kanban"},
		{"kanban", "create"},
		{"kanban", "list"},
		{"kanban", "show"},
		{"kanban", "exec"},
		{"kanban", "claim"},
		{"kanban", "complete"},
		{"kanban", "block"},
		{"kanban", "unblock"},
		{"kanban", "comment"},
		{"kanban", "link"},
		{"kanban", "runs"},
	} {
		mustFindCommand(t, root, path)
	}
}

func TestKanbanCommandFlags(t *testing.T) {
	root := newRootCmd()

	create := mustFindCommand(t, root, []string{"kanban", "create"})
	for _, flag := range []string{"project", "board", "milestone", "body", "assignee", "account"} {
		if create.Flag(flag) == nil {
			t.Fatalf("kanban create missing --%s", flag)
		}
	}

	claim := mustFindCommand(t, root, []string{"kanban", "claim"})
	for _, flag := range []string{"actor", "work-dir", "account"} {
		if claim.Flag(flag) == nil {
			t.Fatalf("kanban claim missing --%s", flag)
		}
	}

	execCmd := mustFindCommand(t, root, []string{"kanban", "exec"})
	for _, flag := range []string{"actor", "work-dir", "summary", "account"} {
		if execCmd.Flag(flag) == nil {
			t.Fatalf("kanban exec missing --%s", flag)
		}
	}

	complete := mustFindCommand(t, root, []string{"kanban", "complete"})
	for _, flag := range []string{"summary", "metadata", "actor", "account"} {
		if complete.Flag(flag) == nil {
			t.Fatalf("kanban complete missing --%s", flag)
		}
	}
}

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

	err = runKanbanExec(task.ID, []string{"sh", "-c", "printf ok | tee exec-output.txt"}, &kanbanExecFlags{
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
	if len(runs) != 1 || runs[0].Outcome != store.KanbanRunCompleted || !strings.Contains(runs[0].MetadataJSON, `"exit_code":0`) || !strings.Contains(runs[0].MetadataJSON, `"command":["sh","-c","printf ok | tee exec-output.txt"]`) {
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

func TestKanbanCommandDoesNotAddTopLevelBoardOrMilestone(t *testing.T) {
	root := newRootCmd()

	for _, name := range []string{"board", "milestone"} {
		cmd, _, err := root.Find([]string{name})
		if err == nil && cmd != nil && cmd.Name() == name {
			t.Fatalf("root command must not expose top-level %q; use kittypaw project %s", name, name)
		}
	}
}
