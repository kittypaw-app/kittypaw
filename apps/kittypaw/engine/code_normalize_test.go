package engine

import "testing"

func TestNormalizeGeneratedCodeKeepsBareKanbanCall(t *testing.T) {
	got := normalizeGeneratedCode(`Kanban.show("tsk_123");`)
	if got != `Kanban.show("tsk_123");` {
		t.Fatalf("normalizeGeneratedCode() = %q, want bare Kanban call unchanged", got)
	}
}
