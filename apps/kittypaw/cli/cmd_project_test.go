package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestProjectCommandNestsBoardAndMilestone(t *testing.T) {
	root := newRootCmd()

	for _, name := range []string{"board", "milestone"} {
		cmd, _, err := root.Find([]string{name})
		if err == nil && cmd != nil && cmd.Name() == name {
			t.Fatalf("root command must not expose top-level %q; use kittypaw project %s", name, name)
		}
	}

	for _, path := range [][]string{
		{"project"},
		{"project", "create"},
		{"project", "list"},
		{"project", "show"},
		{"project", "board"},
		{"project", "board", "list"},
		{"project", "milestone"},
		{"project", "milestone", "create"},
		{"project", "milestone", "list"},
	} {
		mustFindCommand(t, root, path)
	}
}

func TestProjectCommandFlags(t *testing.T) {
	root := newRootCmd()

	create := mustFindCommand(t, root, []string{"project", "create"})
	for _, flag := range []string{"root", "name", "account"} {
		if create.Flag(flag) == nil {
			t.Fatalf("project create missing --%s", flag)
		}
	}

	boardList := mustFindCommand(t, root, []string{"project", "board", "list"})
	if boardList.Flag("account") == nil {
		t.Fatal("project board list missing inherited --account")
	}

	milestoneCreate := mustFindCommand(t, root, []string{"project", "milestone", "create"})
	for _, flag := range []string{"target-date", "description", "account"} {
		if milestoneCreate.Flag(flag) == nil {
			t.Fatalf("project milestone create missing --%s", flag)
		}
	}
}

func mustFindCommand(t *testing.T, root *cobra.Command, path []string) *cobra.Command {
	t.Helper()
	cmd, _, err := root.Find(path)
	if err != nil || cmd == nil || cmd.Name() != path[len(path)-1] {
		t.Fatalf("Find(%v) = %v, %v", path, commandName(cmd), err)
	}
	return cmd
}

func commandName(cmd *cobra.Command) string {
	if cmd == nil {
		return "<nil>"
	}
	return strings.Join(commandPath(cmd), " ")
}

func commandPath(cmd *cobra.Command) []string {
	if cmd == nil {
		return nil
	}
	var parts []string
	for c := cmd; c != nil; c = c.Parent() {
		parts = append([]string{c.Name()}, parts...)
	}
	return parts
}
