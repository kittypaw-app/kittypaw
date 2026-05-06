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
