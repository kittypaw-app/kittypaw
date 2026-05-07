package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jinto/kittypaw/store"
)

func TestKanbanCommandExposesTaskWorkflow(t *testing.T) {
	root := newRootCmd()

	for _, path := range [][]string{
		{"kanban"},
		{"kanban", "create"},
		{"kanban", "list"},
		{"kanban", "stale"},
		{"kanban", "show"},
		{"kanban", "edit"},
		{"kanban", "archive"},
		{"kanban", "exec"},
		{"kanban", "dispatch"},
		{"kanban", "claim"},
		{"kanban", "heartbeat"},
		{"kanban", "complete"},
		{"kanban", "cancel"},
		{"kanban", "reclaim"},
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

	heartbeat := mustFindCommand(t, root, []string{"kanban", "heartbeat"})
	for _, flag := range []string{"actor", "account"} {
		if heartbeat.Flag(flag) == nil {
			t.Fatalf("kanban heartbeat missing --%s", flag)
		}
	}

	cancel := mustFindCommand(t, root, []string{"kanban", "cancel"})
	for _, flag := range []string{"actor", "metadata", "account"} {
		if cancel.Flag(flag) == nil {
			t.Fatalf("kanban cancel missing --%s", flag)
		}
	}

	reclaim := mustFindCommand(t, root, []string{"kanban", "reclaim"})
	for _, flag := range []string{"actor", "work-dir", "metadata", "account"} {
		if reclaim.Flag(flag) == nil {
			t.Fatalf("kanban reclaim missing --%s", flag)
		}
	}

	execCmd := mustFindCommand(t, root, []string{"kanban", "exec"})
	for _, flag := range []string{"actor", "work-dir", "summary", "account"} {
		if execCmd.Flag(flag) == nil {
			t.Fatalf("kanban exec missing --%s", flag)
		}
	}

	dispatch := mustFindCommand(t, root, []string{"kanban", "dispatch"})
	for _, flag := range []string{"project", "limit", "loop", "interval", "actor", "work-dir", "summary", "account"} {
		if dispatch.Flag(flag) == nil {
			t.Fatalf("kanban dispatch missing --%s", flag)
		}
	}

	stale := mustFindCommand(t, root, []string{"kanban", "stale"})
	for _, flag := range []string{"project", "stale-after", "limit", "account"} {
		if stale.Flag(flag) == nil {
			t.Fatalf("kanban stale missing --%s", flag)
		}
	}

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

	complete := mustFindCommand(t, root, []string{"kanban", "complete"})
	for _, flag := range []string{"summary", "metadata", "actor", "account"} {
		if complete.Flag(flag) == nil {
			t.Fatalf("kanban complete missing --%s", flag)
		}
	}
}

func TestKanbanHeartbeatUpdatesRun(t *testing.T) {
	taskID := setupKanbanCLITestTask(t, "Heartbeat")
	st, err := openStoreForAccount("alice")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := st.ClaimKanbanTask(taskID, store.ClaimKanbanTaskRequest{Actor: "alice"}); err != nil {
		t.Fatalf("ClaimKanbanTask: %v", err)
	}
	_ = st.Close()

	if err := runKanbanHeartbeat(taskID, &kanbanHeartbeatFlags{
		shared: &kanbanSharedFlags{accountID: "alice"},
		actor:  "alice",
	}); err != nil {
		t.Fatalf("runKanbanHeartbeat: %v", err)
	}

	st, err = openStoreForAccount("alice")
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer st.Close()
	runs, err := st.ListKanbanRuns(taskID)
	if err != nil {
		t.Fatalf("ListKanbanRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].Outcome != store.KanbanRunRunning || runs[0].HeartbeatAt == "" {
		t.Fatalf("runs = %+v", runs)
	}
}

func TestKanbanCancelCancelsRun(t *testing.T) {
	taskID := setupKanbanCLITestTask(t, "Cancel")
	st, err := openStoreForAccount("alice")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := st.ClaimKanbanTask(taskID, store.ClaimKanbanTaskRequest{Actor: "alice"}); err != nil {
		t.Fatalf("ClaimKanbanTask: %v", err)
	}
	_ = st.Close()

	if err := runKanbanCancel(taskID, "manual stop", &kanbanCancelFlags{
		shared:   &kanbanSharedFlags{accountID: "alice"},
		actor:    "alice",
		metadata: `{"source":"cli-test"}`,
	}); err != nil {
		t.Fatalf("runKanbanCancel: %v", err)
	}

	st, err = openStoreForAccount("alice")
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer st.Close()
	got, err := st.GetKanbanTask(taskID)
	if err != nil {
		t.Fatalf("GetKanbanTask: %v", err)
	}
	if got.Status != store.KanbanStatusTodo {
		t.Fatalf("task status = %q", got.Status)
	}
	runs, err := st.ListKanbanRuns(taskID)
	if err != nil {
		t.Fatalf("ListKanbanRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].Outcome != store.KanbanRunCanceled || runs[0].Summary != "manual stop" || !strings.Contains(runs[0].MetadataJSON, "cli-test") {
		t.Fatalf("runs = %+v", runs)
	}
}

func TestKanbanReclaimStartsReplacementRun(t *testing.T) {
	taskID := setupKanbanCLITestTask(t, "Reclaim")
	st, err := openStoreForAccount("alice")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := st.ClaimKanbanTask(taskID, store.ClaimKanbanTaskRequest{Actor: "alice"}); err != nil {
		t.Fatalf("ClaimKanbanTask: %v", err)
	}
	_ = st.Close()

	runDir := t.TempDir()
	if err := runKanbanReclaim(taskID, "stale runner", &kanbanReclaimFlags{
		shared:   &kanbanSharedFlags{accountID: "alice"},
		actor:    "bob",
		workDir:  runDir,
		metadata: `{"source":"cli-test"}`,
	}); err != nil {
		t.Fatalf("runKanbanReclaim: %v", err)
	}

	st, err = openStoreForAccount("alice")
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer st.Close()
	got, err := st.GetKanbanTask(taskID)
	if err != nil {
		t.Fatalf("GetKanbanTask: %v", err)
	}
	if got.Status != store.KanbanStatusRunning {
		t.Fatalf("task status = %q", got.Status)
	}
	runs, err := st.ListKanbanRuns(taskID)
	if err != nil {
		t.Fatalf("ListKanbanRuns: %v", err)
	}
	var reclaimed, running int
	for _, run := range runs {
		if run.Outcome == store.KanbanRunReclaimed {
			reclaimed++
		}
		if run.Outcome == store.KanbanRunRunning {
			running++
			if run.Actor != "bob" || run.WorkDir != runDir || run.WorkDirProvider != store.KanbanWorkDirManual {
				t.Fatalf("running run = %+v", run)
			}
		}
	}
	if len(runs) != 2 || reclaimed != 1 || running != 1 {
		t.Fatalf("runs = %+v", runs)
	}
}

func TestKanbanStaleListsStaleRuns(t *testing.T) {
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
	task, err := st.CreateKanbanTask(store.CreateKanbanTaskRequest{ProjectID: project.ID, Title: "Investigate stale run", Status: store.KanbanStatusTodo})
	if err != nil {
		t.Fatalf("CreateKanbanTask: %v", err)
	}
	run, err := st.ClaimKanbanTask(task.ID, store.ClaimKanbanTaskRequest{Actor: "alice"})
	if err != nil {
		t.Fatalf("ClaimKanbanTask: %v", err)
	}
	_ = st.Close()
	time.Sleep(2100 * time.Millisecond)

	var runErr error
	out := captureStdout(t, func() {
		runErr = runKanbanStale(&kanbanStaleFlags{
			shared:     &kanbanSharedFlags{accountID: "alice"},
			project:    "kitty",
			staleAfter: "1s",
			limit:      10,
		})
	})
	if runErr != nil {
		t.Fatalf("runKanbanStale: %v", runErr)
	}
	for _, want := range []string{task.ID, run.ID, "kitty", "alice", "Investigate stale run"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stale output = %q, missing %q", out, want)
		}
	}
}

func TestKanbanStaleValidatesDuration(t *testing.T) {
	err := runKanbanStale(&kanbanStaleFlags{
		shared:     &kanbanSharedFlags{accountID: "alice"},
		staleAfter: "0s",
	})
	if err == nil || !strings.Contains(err.Error(), "positive --stale-after") {
		t.Fatalf("runKanbanStale error = %v, want positive stale-after validation", err)
	}
}

func TestKanbanEditUpdatesTaskFields(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))

	st, err := openStoreForAccount("alice")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	project, err := st.CreateKanbanProject(store.CreateKanbanProjectRequest{
		Slug:     "kitty",
		Name:     "KittyPaw",
		RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("CreateKanbanProject: %v", err)
	}
	ms, err := st.CreateKanbanMilestone(store.CreateKanbanMilestoneRequest{
		ProjectID: project.ID,
		Title:     "Release One",
	})
	if err != nil {
		t.Fatalf("CreateKanbanMilestone: %v", err)
	}
	task, err := st.CreateKanbanTask(store.CreateKanbanTaskRequest{
		ProjectID: project.ID,
		Title:     "Old title",
		Status:    store.KanbanStatusTodo,
	})
	if err != nil {
		t.Fatalf("CreateKanbanTask: %v", err)
	}
	_ = st.Close()

	priority := 8
	err = runKanbanEdit(task.ID, &kanbanEditFlags{
		shared:       &kanbanSharedFlags{accountID: "alice"},
		actor:        "alice",
		title:        "New title",
		titleSet:     true,
		status:       store.KanbanStatusReady,
		statusSet:    true,
		priority:     priority,
		prioritySet:  true,
		assignee:     "bob",
		assigneeSet:  true,
		milestone:    ms.Slug,
		milestoneSet: true,
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

func TestKanbanEditClearsMilestone(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))

	st, err := openStoreForAccount("alice")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	project, err := st.CreateKanbanProject(store.CreateKanbanProjectRequest{
		Slug:     "kitty",
		Name:     "KittyPaw",
		RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("CreateKanbanProject: %v", err)
	}
	ms, err := st.CreateKanbanMilestone(store.CreateKanbanMilestoneRequest{
		ProjectID: project.ID,
		Title:     "Release One",
	})
	if err != nil {
		t.Fatalf("CreateKanbanMilestone: %v", err)
	}
	task, err := st.CreateKanbanTask(store.CreateKanbanTaskRequest{
		ProjectID:   project.ID,
		MilestoneID: ms.ID,
		Title:       "Milestoned",
		Status:      store.KanbanStatusTodo,
	})
	if err != nil {
		t.Fatalf("CreateKanbanTask: %v", err)
	}
	_ = st.Close()

	err = runKanbanEdit(task.ID, &kanbanEditFlags{
		shared:         &kanbanSharedFlags{accountID: "alice"},
		clearMilestone: true,
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
	if got.MilestoneID != "" {
		t.Fatalf("milestone id = %q, want cleared", got.MilestoneID)
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
	project, err := st.CreateKanbanProject(store.CreateKanbanProjectRequest{
		Slug:     "kitty",
		Name:     "KittyPaw",
		RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("CreateKanbanProject: %v", err)
	}
	task, err := st.CreateKanbanTask(store.CreateKanbanTaskRequest{
		ProjectID: project.ID,
		Title:     "Archive",
		Status:    store.KanbanStatusTodo,
	})
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

func TestKanbanCommandDoesNotAddTopLevelBoardOrMilestone(t *testing.T) {
	root := newRootCmd()

	for _, name := range []string{"board", "milestone"} {
		cmd, _, err := root.Find([]string{name})
		if err == nil && cmd != nil && cmd.Name() == name {
			t.Fatalf("root command must not expose top-level %q; use kittypaw project %s", name, name)
		}
	}
}

func setupKanbanCLITestTask(t *testing.T, title string) string {
	t.Helper()

	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))

	st, err := openStoreForAccount("alice")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	project, err := st.CreateKanbanProject(store.CreateKanbanProjectRequest{
		Slug:     "kitty",
		Name:     "KittyPaw",
		RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("CreateKanbanProject: %v", err)
	}
	task, err := st.CreateKanbanTask(store.CreateKanbanTaskRequest{
		ProjectID: project.ID,
		Title:     title,
		Status:    store.KanbanStatusTodo,
	})
	if err != nil {
		t.Fatalf("CreateKanbanTask: %v", err)
	}
	return task.ID
}
