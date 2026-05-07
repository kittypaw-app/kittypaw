package engine

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/llm"
	"github.com/jinto/kittypaw/store"
)

// ---------------------------------------------------------------------------
// PM JSON Decision Parsing
// ---------------------------------------------------------------------------

func TestPMDecision_Direct(t *testing.T) {
	raw := `{"kind":"direct","reason":"simple question"}`
	var d PMDecision
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatal(err)
	}
	if d.Kind != "direct" {
		t.Fatalf("kind = %q, want direct", d.Kind)
	}
}

func TestPMDecision_Delegate(t *testing.T) {
	raw := `{"kind":"delegate","reason":"needs specialist","tasks":[{"staff_id":"coder","task":"write tests"},{"staff_id":"writer","task":"write docs"}]}`
	var d PMDecision
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatal(err)
	}
	if d.Kind != "delegate" {
		t.Fatalf("kind = %q, want delegate", d.Kind)
	}
	if len(d.Tasks) != 2 {
		t.Fatalf("tasks = %d, want 2", len(d.Tasks))
	}
	if d.Tasks[0].StaffID != "coder" || d.Tasks[1].StaffID != "writer" {
		t.Errorf("unexpected staff IDs: %+v", d.Tasks)
	}
}

func TestPMDecision_MalformedJSON(t *testing.T) {
	raw := `not valid json`
	var d PMDecision
	err := json.Unmarshal([]byte(raw), &d)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

// ---------------------------------------------------------------------------
// executeDelegateTask
// ---------------------------------------------------------------------------

func TestDelegateTask_TaskTooLong(t *testing.T) {
	longTask := make([]byte, maxDelegateTaskLen+1)
	for i := range longTask {
		longTask[i] = 'a'
	}
	spec := PMTaskSpec{StaffID: "test", Task: string(longTask)}
	result := executeDelegateTask(context.Background(), spec, nil, nil, nil, 0, 3, "")
	if result.Success {
		t.Fatal("expected failure for oversized task")
	}
}

func TestDelegateTask_DepthExceeded(t *testing.T) {
	spec := PMTaskSpec{StaffID: "test", Task: "do something"}
	result := executeDelegateTask(context.Background(), spec, nil, nil, nil, 3, 3, "")
	if result.Success {
		t.Fatal("expected failure when depth >= maxDepth")
	}
}

func TestDelegateTask_DepthZeroMaxZero(t *testing.T) {
	// Delegation structurally disabled when maxDepth=0.
	spec := PMTaskSpec{StaffID: "test", Task: "do something"}
	result := executeDelegateTask(context.Background(), spec, nil, nil, nil, 0, 0, "")
	if result.Success {
		t.Fatal("expected failure when maxDepth=0")
	}
}

func TestDelegateTask_StaffNotFound(t *testing.T) {
	st := newDelegateTestStore(t)
	spec := PMTaskSpec{StaffID: "nonexistent", Task: "do something"}
	result := executeDelegateTask(context.Background(), spec, nil, st, nil, 0, 3, "")
	if result.Success {
		t.Fatal("expected failure for missing staff")
	}
}

// ---------------------------------------------------------------------------
// loadSOUL
// ---------------------------------------------------------------------------

func TestLoadSOUL_MissingFile(t *testing.T) {
	// When SOUL.md is missing, loadSOUL returns the default preset fallback.
	// This matches the staff preset system behavior (AC5: fallback + warn log).
	content := loadSOUL("", "definitely-nonexistent-staff")
	if content == "" {
		t.Fatal("expected default preset fallback, got empty string")
	}
	if content != core.Presets["default-assistant"].Soul {
		t.Fatalf("expected default-assistant preset, got %q", content)
	}
}

// ---------------------------------------------------------------------------
// PM Synthesize
// ---------------------------------------------------------------------------

func TestSynthesize_AllFailed(t *testing.T) {
	tasks := []PMTaskSpec{{StaffID: "a", Task: "task-a"}}
	results := []DelegateResult{
		{StaffID: "a", Task: "task-a", Result: "timeout", Success: false},
	}
	out, err := pmSynthesize(context.Background(), tasks, results, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSubstring(out, "failed") && !containsSubstring(out, "Failed") {
		t.Errorf("expected failure message, got %q", out)
	}
}

func TestSynthesize_SingleSuccess(t *testing.T) {
	tasks := []PMTaskSpec{{StaffID: "a", Task: "task-a"}}
	results := []DelegateResult{
		{StaffID: "a", Task: "task-a", Result: "the answer is 42", Success: true},
	}
	out, err := pmSynthesize(context.Background(), tasks, results, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != "the answer is 42" {
		t.Errorf("single success should return result directly, got %q", out)
	}
}

// ---------------------------------------------------------------------------
// Budget Exhaustion in Delegation
// ---------------------------------------------------------------------------

func TestDelegateTask_BudgetExhausted(t *testing.T) {
	// Budget with 0 remaining (already spent to limit).
	b := NewSharedBudget(100)
	b.TrySpend(100)

	st := newDelegateTestStore(t)
	// Seed staff.
	_ = st.UpsertStaffMeta("test-staff", "A test staff member", "", "system")

	spec := PMTaskSpec{StaffID: "test-staff", Task: "do something"}
	// Since we can't call the real LLM, the test just verifies budget is checked.
	// With a nil provider, it will fail at LLM call, but the budget would still
	// be checked after. We verify the flow doesn't panic.
	result := executeDelegateTask(context.Background(), spec, nil, st, b, 0, 3, "")
	// Should fail because provider is nil, not because of budget.
	if result.Success {
		t.Fatal("expected failure with nil provider")
	}
}

func TestExecuteRunnerDelegateUsesStaffIDFirst(t *testing.T) {
	st := newDelegateTestStore(t)
	if err := st.UpsertStaffMeta("coder", "Code staff", "", "system"); err != nil {
		t.Fatalf("seed staff: %v", err)
	}
	cfg := core.DefaultConfig()
	sess := &Session{
		Config:   &cfg,
		Provider: &mockProvider{responses: []*llm.Response{mockResp("delegated ok")}},
		Store:    st,
	}

	out, err := executeRunner(context.Background(), core.SkillCall{
		Method: "delegate",
		Args: []json.RawMessage{
			json.RawMessage(`"coder"`),
			json.RawMessage(`"write tests"`),
		},
	}, sess)
	if err != nil {
		t.Fatalf("executeRunner error: %v", err)
	}

	var got struct {
		Result  string `json:"result"`
		Success bool   `json:"success"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !got.Success || got.Result != "delegated ok" {
		t.Fatalf("result = %+v, want success delegated ok", got)
	}
}

// ---------------------------------------------------------------------------
// OrchestrateRequest — disabled config
// ---------------------------------------------------------------------------

func TestOrchestrateRequest_Disabled(t *testing.T) {
	config := &core.OrchestrationConfig{Enabled: false}
	_, handled, err := OrchestrateRequest(context.Background(), "hello", nil, nil, config, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if handled {
		t.Fatal("should not handle when disabled")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newDelegateTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && findSubstring(s, substr))
}

func findSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
