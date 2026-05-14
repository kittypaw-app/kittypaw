package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/llm"
	"github.com/jinto/kittypaw/sandbox"
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
	result := executeDelegateTask(context.Background(), spec, nil, 0, 3, "", nil)
	if result.Success {
		t.Fatal("expected failure for oversized task")
	}
}

func TestDelegateTask_DepthExceeded(t *testing.T) {
	spec := PMTaskSpec{StaffID: "test", Task: "do something"}
	result := executeDelegateTask(context.Background(), spec, nil, 3, 3, "", nil)
	if result.Success {
		t.Fatal("expected failure when depth >= maxDepth")
	}
}

func TestDelegateTask_DepthZeroMaxZero(t *testing.T) {
	// Delegation structurally disabled when maxDepth=0.
	spec := PMTaskSpec{StaffID: "test", Task: "do something"}
	result := executeDelegateTask(context.Background(), spec, nil, 0, 0, "", nil)
	if result.Success {
		t.Fatal("expected failure when maxDepth=0")
	}
}

func TestDelegateTask_StaffNotFound(t *testing.T) {
	cfg := core.DefaultConfig()
	sess := &AccountRuntime{Config: &cfg, Store: newDelegateTestStore(t), BaseDir: t.TempDir()}
	spec := PMTaskSpec{StaffID: "nonexistent", Task: "do something"}
	result := executeDelegateTask(context.Background(), spec, sess, 0, 3, "", nil)
	if result.Success {
		t.Fatal("expected failure for missing staff")
	}
}

func TestDelegateTask_MetaOnlyStaffFails(t *testing.T) {
	cfg := core.DefaultConfig()
	st := newDelegateTestStore(t)
	baseDir := t.TempDir()
	if err := core.WriteStaffMetaFile(baseDir, core.StaffMetaFile{
		ID:          "inactive",
		Description: "Inactive staff",
	}); err != nil {
		t.Fatalf("seed staff meta: %v", err)
	}

	spec := PMTaskSpec{StaffID: "inactive", Task: "do something"}
	sess := &AccountRuntime{Config: &cfg, Store: st, BaseDir: baseDir}
	result := executeDelegateTask(context.Background(), spec, sess, 0, 3, "", nil)
	if result.Success {
		t.Fatal("expected failure for staff without SOUL.md")
	}
	if result.Result != `staff "inactive" not found` {
		t.Fatalf("result = %q, want missing staff error", result.Result)
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

	cfg := core.DefaultConfig()
	st := newDelegateTestStore(t)
	baseDir := t.TempDir()
	seedActiveStaffFile(t, baseDir, "test-staff", "", "A test staff member")
	sess := &AccountRuntime{
		Config:  &cfg,
		Store:   st,
		BaseDir: baseDir,
		Budget:  b,
	}

	spec := PMTaskSpec{StaffID: "test-staff", Task: "do something"}
	// Since we can't call the real LLM, the test just verifies budget is checked.
	// With a nil provider, it will fail at LLM call, but the budget would still
	// be checked after. We verify the flow doesn't panic.
	result := executeDelegateTask(context.Background(), spec, sess, 0, 3, "", nil)
	// Should fail because provider is nil, not because of budget.
	if result.Success {
		t.Fatal("expected failure with nil provider")
	}
}

func TestExecuteRunnerDelegateUsesStaffIDFirst(t *testing.T) {
	skipWithoutRuntime(t)
	st := newDelegateTestStore(t)
	baseDir := t.TempDir()
	seedActiveStaffFile(t, baseDir, "coder", "", "Code staff")
	cfg := core.DefaultConfig()
	sess := &AccountRuntime{
		Config:   &cfg,
		Provider: &mockProvider{responses: []*llm.Response{mockResp(`return "delegated ok";`)}},
		Sandbox:  sandbox.New(cfg.Sandbox),
		Store:    st,
		BaseDir:  baseDir,
		Pipeline: NewPipelineState(),
	}

	ctx := ContextWithConversationID(context.Background(), "general:web_chat:test-session")
	ctx = ContextWithEvent(ctx, ptrEvent(webChatEvent("parent")))
	out, err := executeRunner(ctx, core.SkillCall{
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

func TestExecuteRunnerDelegateRunsFullToolLoopAndAudit(t *testing.T) {
	skipWithoutRuntime(t)
	t.Setenv("KITTYPAW_DELEGATE_TOOL_TEST", "delegate-tool-ok")

	st := newDelegateTestStore(t)
	baseDir := t.TempDir()
	seedActiveStaffFile(t, baseDir, "coder", "", "Code staff")
	cfg := core.DefaultConfig()
	sess := &AccountRuntime{
		Config: &cfg,
		Provider: &mockProvider{responses: []*llm.Response{mockResp(`
			const env = Env.get("KITTYPAW_DELEGATE_TOOL_TEST");
			return env.value;
		`)}},
		Sandbox:  sandbox.New(cfg.Sandbox),
		Store:    st,
		BaseDir:  baseDir,
		Pipeline: NewPipelineState(),
	}
	parentEvent := webChatEvent("parent asks")
	ctx := ContextWithConversationID(ContextWithEvent(context.Background(), &parentEvent), testWebChatConversationID)

	out, err := executeRunner(ctx, core.SkillCall{
		Method: "delegate",
		Args: []json.RawMessage{
			json.RawMessage(`"coder"`),
			json.RawMessage(`"check env"`),
		},
	}, sess)
	if err != nil {
		t.Fatalf("executeRunner error: %v", err)
	}
	var got DelegateResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !got.Success || got.Result != "delegate-tool-ok" {
		t.Fatalf("result = %+v, want delegated tool output", got)
	}

	delegateConv := delegateConversationID(testWebChatConversationID, "coder")
	turns, err := st.ListConversationTurnsForConversation(delegateConv, 10)
	if err != nil {
		t.Fatalf("ListConversationTurnsForConversation: %v", err)
	}
	if len(turns) < 2 {
		t.Fatalf("delegate turns = %d, want user and assistant", len(turns))
	}
	assistant := turns[len(turns)-1]
	if assistant.Role != core.RoleAssistant || len(assistant.ToolTraces) != 1 {
		t.Fatalf("assistant turn = %+v, want one delegated tool trace", assistant)
	}
	if trace := assistant.ToolTraces[0]; trace.SkillName != "Env" || trace.Method != "get" || !trace.Success {
		t.Fatalf("tool trace = %+v, want successful Env.get", trace)
	}

	execs, err := st.RecentExecutions(10)
	if err != nil {
		t.Fatalf("RecentExecutions: %v", err)
	}
	var audit *store.ExecutionRecord
	for i := range execs {
		if execs[i].SkillID == "delegate:coder" {
			audit = &execs[i]
			break
		}
	}
	if audit == nil {
		t.Fatalf("missing delegation audit in executions: %+v", execs)
	}
	for _, want := range []string{
		`"staff_id":"coder"`,
		`"parent_conversation_id":"` + testWebChatConversationID + `"`,
		`"delegate_conversation_id":"` + delegateConv + `"`,
		`"tool_traces"`,
	} {
		if !strings.Contains(audit.MetadataJSON, want) {
			t.Fatalf("audit metadata missing %q: %s", want, audit.MetadataJSON)
		}
	}
	if strings.Contains(audit.MetadataJSON, "delegate-tool-ok") || !strings.Contains(audit.MetadataJSON, `"redacted":true`) {
		t.Fatalf("audit metadata should contain redacted tool trace, got: %s", audit.MetadataJSON)
	}
}

func TestExecuteRunnerDelegateAppliesStaffAllowedSkills(t *testing.T) {
	skipWithoutRuntime(t)

	st := newDelegateTestStore(t)
	baseDir := t.TempDir()
	seedActiveStaffFile(t, baseDir, "memory-only", "", "Memory-only staff")
	meta, err := core.ReadStaffMetaFile(baseDir, "memory-only")
	if err != nil {
		t.Fatal(err)
	}
	meta.AllowedSkills = []string{"Memory"}
	if err := core.WriteStaffMetaFile(baseDir, meta); err != nil {
		t.Fatal(err)
	}

	cfg := core.DefaultConfig()
	sess := &AccountRuntime{
		Config: &cfg,
		Provider: &mockProvider{responses: []*llm.Response{mockResp(`
			const env = Env.get("KITTYPAW_DELEGATE_TOOL_TEST");
			return env.error || env.value;
		`)}},
		Sandbox:  sandbox.New(cfg.Sandbox),
		Store:    st,
		BaseDir:  baseDir,
		Pipeline: NewPipelineState(),
	}
	ctx := ContextWithConversationID(context.Background(), testWebChatConversationID)

	out, err := executeRunner(ctx, core.SkillCall{
		Method: "delegate",
		Args: []json.RawMessage{
			json.RawMessage(`"memory-only"`),
			json.RawMessage(`"try env"`),
		},
	}, sess)
	if err != nil {
		t.Fatalf("executeRunner error: %v", err)
	}
	var got DelegateResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !got.Success || !strings.Contains(got.Result, "not allowed for staff") {
		t.Fatalf("result = %+v, want staff policy rejection output", got)
	}
}

func TestExecuteRunnerDelegatePacksParentConversationContext(t *testing.T) {
	skipWithoutRuntime(t)

	st := newDelegateTestStore(t)
	baseDir := t.TempDir()
	seedActiveStaffFile(t, baseDir, "researcher", "", "Research staff")
	parentConv := "general:web_chat:parent"
	if err := st.AddConversationTurn(&core.ConversationTurn{
		ConversationID: parentConv,
		Role:           core.RoleUser,
		Content:        "previous requirement: answer in Korean",
		Timestamp:      core.NowTimestamp(),
	}); err != nil {
		t.Fatalf("AddConversationTurn: %v", err)
	}

	cfg := core.DefaultConfig()
	provider := &promptCaptureProvider{response: `return "packed-ok";`}
	sess := &AccountRuntime{
		Config:   &cfg,
		Provider: provider,
		Sandbox:  sandbox.New(cfg.Sandbox),
		Store:    st,
		BaseDir:  baseDir,
		Pipeline: NewPipelineState(),
	}
	ctx := ContextWithConversationID(context.Background(), parentConv)

	out, err := executeRunner(ctx, core.SkillCall{
		Method: "delegate",
		Args: []json.RawMessage{
			json.RawMessage(`"researcher"`),
			json.RawMessage(`"summarize the context"`),
		},
	}, sess)
	if err != nil {
		t.Fatalf("executeRunner error: %v", err)
	}
	if !strings.Contains(out, "packed-ok") {
		t.Fatalf("delegate output = %s", out)
	}

	var joined strings.Builder
	for _, msg := range provider.messages {
		joined.WriteString(msg.Content)
		joined.WriteByte('\n')
	}
	for _, want := range []string{
		"Parent conversation context",
		"previous requirement: answer in Korean",
		"Current delegated task",
		"summarize the context",
	} {
		if !strings.Contains(joined.String(), want) {
			t.Fatalf("delegate prompt missing %q:\n%s", want, joined.String())
		}
	}
}

func TestExecuteRunnerDelegateChargesSharedBudget(t *testing.T) {
	skipWithoutRuntime(t)

	st := newDelegateTestStore(t)
	baseDir := t.TempDir()
	seedActiveStaffFile(t, baseDir, "coder", "", "Code staff")
	cfg := core.DefaultConfig()
	budget := NewSharedBudget(15)
	sess := &AccountRuntime{
		Config:   &cfg,
		Provider: &mockProvider{responses: []*llm.Response{mockResp(`return "budgeted";`)}},
		Sandbox:  sandbox.New(cfg.Sandbox),
		Store:    st,
		BaseDir:  baseDir,
		Budget:   budget,
		Pipeline: NewPipelineState(),
	}
	ctx := ContextWithConversationID(context.Background(), testWebChatConversationID)

	out, err := executeRunner(ctx, core.SkillCall{
		Method: "delegate",
		Args: []json.RawMessage{
			json.RawMessage(`"coder"`),
			json.RawMessage(`"budget accounting"`),
		},
	}, sess)
	if err != nil {
		t.Fatalf("executeRunner error: %v", err)
	}
	var got DelegateResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !got.Success || got.Result != "budgeted" {
		t.Fatalf("result = %+v, want budgeted success", got)
	}
	if got.TokenUsage != 15 {
		t.Fatalf("token_usage = %d, want 15", got.TokenUsage)
	}
	if used := budget.Used(); used != 15 {
		t.Fatalf("budget used = %d, want 15", used)
	}
}

func TestExecuteRunnerDelegateFailsWhenSharedBudgetExceeded(t *testing.T) {
	skipWithoutRuntime(t)

	st := newDelegateTestStore(t)
	baseDir := t.TempDir()
	seedActiveStaffFile(t, baseDir, "coder", "", "Code staff")
	cfg := core.DefaultConfig()
	budget := NewSharedBudget(10)
	sess := &AccountRuntime{
		Config:   &cfg,
		Provider: &mockProvider{responses: []*llm.Response{mockResp(`return "over budget";`)}},
		Sandbox:  sandbox.New(cfg.Sandbox),
		Store:    st,
		BaseDir:  baseDir,
		Budget:   budget,
		Pipeline: NewPipelineState(),
	}
	ctx := ContextWithConversationID(context.Background(), testWebChatConversationID)

	out, err := executeRunner(ctx, core.SkillCall{
		Method: "delegate",
		Args: []json.RawMessage{
			json.RawMessage(`"coder"`),
			json.RawMessage(`"budget exceeded"`),
		},
	}, sess)
	if err != nil {
		t.Fatalf("executeRunner error: %v", err)
	}
	var got DelegateResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if got.Success || !strings.Contains(got.Result, "token budget exhausted") {
		t.Fatalf("result = %+v, want budget exhaustion failure", got)
	}
	if got.TokenUsage != 15 {
		t.Fatalf("token_usage = %d, want attempted 15", got.TokenUsage)
	}
	if used := budget.Used(); used != 0 {
		t.Fatalf("budget used = %d, want 0 after rejected spend", used)
	}
}

// ---------------------------------------------------------------------------
// OrchestrateRequest — disabled config
// ---------------------------------------------------------------------------

func TestOrchestrateRequest_Disabled(t *testing.T) {
	config := &core.OrchestrationConfig{Enabled: false}
	cfg := core.DefaultConfig()
	cfg.Orchestration = *config
	_, handled, err := OrchestrateRequest(context.Background(), "hello", &AccountRuntime{Config: &cfg})
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

func ptrEvent(event core.Event) *core.Event {
	return &event
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
