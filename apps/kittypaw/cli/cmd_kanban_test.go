package main

import "testing"

func TestKanbanCommandExposesTaskWorkflow(t *testing.T) {
	root := newRootCmd()

	for _, path := range [][]string{
		{"kanban"},
		{"kanban", "create"},
		{"kanban", "list"},
		{"kanban", "show"},
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

	complete := mustFindCommand(t, root, []string{"kanban", "complete"})
	for _, flag := range []string{"summary", "metadata", "actor", "account"} {
		if complete.Flag(flag) == nil {
			t.Fatalf("kanban complete missing --%s", flag)
		}
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
