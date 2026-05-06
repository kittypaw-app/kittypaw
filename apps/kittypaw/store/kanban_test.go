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
		Slug:     "kitty",
		Name:     "KittyPaw",
		RootPath: "/repo/kitty",
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

func TestKanbanTaskClaimCompleteRecordsRun(t *testing.T) {
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

	if err := st.CompleteKanbanTask(task.ID, CompleteKanbanTaskRequest{
		Actor:        "alice",
		Summary:      "done",
		MetadataJSON: `{"tests":1}`,
	}); err != nil {
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

func TestKanbanBlockUnblockAndComment(t *testing.T) {
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
		Title:     "Clarify API",
		Status:    KanbanStatusTodo,
	})
	if err != nil {
		t.Fatalf("CreateKanbanTask: %v", err)
	}

	if err := st.BlockKanbanTask(task.ID, BlockKanbanTaskRequest{Actor: "alice", Reason: "Need API shape"}); err != nil {
		t.Fatalf("BlockKanbanTask: %v", err)
	}
	blocked, err := st.GetKanbanTask(task.ID)
	if err != nil {
		t.Fatalf("Get blocked task: %v", err)
	}
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
	unblocked, err := st.GetKanbanTask(task.ID)
	if err != nil {
		t.Fatalf("Get unblocked task: %v", err)
	}
	if unblocked.Status != KanbanStatusTodo {
		t.Fatalf("unblocked status = %q", unblocked.Status)
	}
}

func TestKanbanDependencyRejectsCycleAndPromotesChild(t *testing.T) {
	st := openTestStore(t)
	project, err := st.CreateKanbanProject(CreateKanbanProjectRequest{
		Slug:     "kitty",
		Name:     "KittyPaw",
		RootPath: "/repo/kitty",
	})
	if err != nil {
		t.Fatalf("CreateKanbanProject: %v", err)
	}
	parent, err := st.CreateKanbanTask(CreateKanbanTaskRequest{
		ProjectID: project.ID,
		Title:     "Schema",
		Status:    KanbanStatusTodo,
	})
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}
	child, err := st.CreateKanbanTask(CreateKanbanTaskRequest{
		ProjectID: project.ID,
		Title:     "CLI",
		Status:    KanbanStatusTodo,
	})
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
