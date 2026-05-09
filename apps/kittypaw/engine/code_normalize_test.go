package engine

import "testing"

func TestNormalizeGeneratedCodeKeepsBareProjectsCall(t *testing.T) {
	got := normalizeGeneratedCode(`Projects.showTicket("KITTY-001");`)
	if got != `Projects.showTicket("KITTY-001");` {
		t.Fatalf("normalizeGeneratedCode() = %q, want bare Projects call unchanged", got)
	}
}

func TestNormalizeGeneratedCodeDoesNotSpecialCaseBareKanbanCall(t *testing.T) {
	got := normalizeGeneratedCode(`Kanban.show("tsk_123");`)
	want := `return "Kanban.show(\"tsk_123\");";`
	if got != want {
		t.Fatalf("normalizeGeneratedCode() = %q, want %q", got, want)
	}
}
