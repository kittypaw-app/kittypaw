package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/sandbox"
)

func TestSlashStaffSwitchesAccountConversationStaff(t *testing.T) {
	st := openTestStore(t)
	if err := st.UpsertStaffMeta("finance", "재무담당 스태프", "[]", "test"); err != nil {
		t.Fatalf("seed staff meta: %v", err)
	}
	cfg := core.DefaultConfig()
	sess := &Session{
		Store:     st,
		Config:    &cfg,
		AccountID: "alice",
	}

	out, handled := tryHandleCommand(context.Background(), "/staff finance", sess)
	if !handled {
		t.Fatal("/staff command was not handled")
	}
	if !strings.Contains(out, "finance") {
		t.Fatalf("response should mention selected staff, got %q", out)
	}
	if got, ok, err := st.GetUserContext("active_staff:alice"); err != nil || !ok || got != "finance" {
		t.Fatalf("active_staff:alice = %q ok=%v err=%v, want finance", got, ok, err)
	}
}

func TestSlashRunExecutesInstalledSkill(t *testing.T) {
	baseDir := t.TempDir()
	cfg := core.DefaultConfig()
	sess := &Session{
		BaseDir: baseDir,
		Config:  &cfg,
		Sandbox: sandbox.New(cfg.Sandbox),
	}
	if err := core.SaveSkillTo(baseDir, &core.Skill{
		Name:        "hello",
		Description: "test skill",
		Enabled:     true,
	}, `return "hello from skill"`); err != nil {
		t.Fatalf("save skill: %v", err)
	}

	out, handled := tryHandleCommand(context.Background(), "/run hello", sess)
	if !handled {
		t.Fatal("/run command was not handled")
	}
	if out != "hello from skill" {
		t.Fatalf("/run output = %q, want skill output", out)
	}
	if strings.Contains(out, "실행 요청됨") {
		t.Fatalf("/run returned a queued/requested message instead of executing: %q", out)
	}
}

// --- /model command (turn-level LLM swap) ---
//
// Matrix per plan v3 § handleModel:
//   0 args      → info
//   1 blank     → usage
//   1 == active → "Already on <id>" (no SetActiveModel)
//   1 valid     → SetActiveModel + "Switched to <id>"
//   1 unknown   → error msg, no SetActiveModel
//   >=2 args    → usage
//
// Fields shown in info match core.ModelConfig only — no temperature/thinking
// inferences (see formatModelInfo doc).

func newModelTestSession() *Session {
	cfg := core.DefaultConfig()
	cfg.LLM.Default = "main"
	cfg.LLM.Models = []core.ModelConfig{
		{
			ID:            "main",
			Provider:      "anthropic",
			Model:         "claude-sonnet-4-6",
			MaxTokens:     4096,
			ContextWindow: 200000,
		},
		{
			ID:       "groq-qwen",
			Provider: "groq",
			Model:    "qwen/qwen3-32b",
			BaseURL:  "https://api.groq.com/openai/v1/chat/completions",
		},
	}
	return &Session{Config: &cfg, AccountID: "alice"}
}

func TestHandleModel_Info(t *testing.T) {
	sess := newModelTestSession()
	out, handled := tryHandleCommand(context.Background(), "/model", sess)
	if !handled {
		t.Fatal("/model not handled")
	}
	for _, want := range []string{"main", "groq-qwen", "anthropic", "claude-sonnet-4-6", "qwen/qwen3-32b"} {
		if !strings.Contains(out, want) {
			t.Errorf("info missing %q. got: %s", want, out)
		}
	}
	// active marker
	if !strings.Contains(out, "* main") {
		t.Errorf("active marker for %q not found. got: %s", "main", out)
	}
}

func TestHandleModel_Info_NoModelsRegistered(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.LLM.Models = nil
	sess := &Session{Config: &cfg}
	out, _ := tryHandleCommand(context.Background(), "/model", sess)
	if !strings.Contains(out, "없음") && !strings.Contains(out, "없습니다") {
		t.Errorf("expected empty-list message, got: %s", out)
	}
}

func TestHandleModel_BlankArg_Usage(t *testing.T) {
	sess := newModelTestSession()
	// extra space → strings.Fields trims, so a blank/space-only id needs
	// to come from a literal arg with whitespace inside the call to
	// handleModel directly.
	out := handleModel([]string{"   "}, sess)
	if !strings.Contains(out, "사용법") {
		t.Errorf("expected usage hint for blank arg, got: %s", out)
	}
	if sess.GetActiveModel() != "" {
		t.Errorf("blank id leaked into activeModelOverride: %q", sess.GetActiveModel())
	}
}

func TestHandleModel_TooManyArgs_Usage(t *testing.T) {
	sess := newModelTestSession()
	out, _ := tryHandleCommand(context.Background(), "/model main extra", sess)
	if !strings.Contains(out, "사용법") {
		t.Errorf("expected usage hint, got: %s", out)
	}
	if sess.GetActiveModel() != "" {
		t.Errorf(">=2 args leaked into activeModelOverride: %q", sess.GetActiveModel())
	}
}

func TestHandleModel_Switch_Valid(t *testing.T) {
	sess := newModelTestSession()
	out, _ := tryHandleCommand(context.Background(), "/model groq-qwen", sess)
	if !strings.Contains(out, "groq-qwen") {
		t.Errorf("expected confirmation containing %q, got: %s", "groq-qwen", out)
	}
	if !strings.Contains(out, "재시작") {
		t.Errorf("expected restart-resets warning in confirmation, got: %s", out)
	}
	if got := sess.GetActiveModel(); got != "groq-qwen" {
		t.Errorf("activeModelOverride = %q, want %q", got, "groq-qwen")
	}
}

func TestHandleModel_Switch_AlreadyOnCurrent_NoOp(t *testing.T) {
	sess := newModelTestSession()
	// Default is "main"; /model main should not Set anything (no-op).
	out, _ := tryHandleCommand(context.Background(), "/model main", sess)
	if !strings.Contains(out, "이미") {
		t.Errorf("expected no-op message, got: %s", out)
	}
	if sess.GetActiveModel() != "" {
		t.Errorf("no-op leaked into activeModelOverride: %q", sess.GetActiveModel())
	}
}

func TestHandleModel_Switch_Unknown_Rejected(t *testing.T) {
	sess := newModelTestSession()
	out, _ := tryHandleCommand(context.Background(), "/model nonsuch", sess)
	if !strings.Contains(out, "알 수 없는") {
		t.Errorf("expected unknown-model error, got: %s", out)
	}
	if !strings.Contains(out, "main") || !strings.Contains(out, "groq-qwen") {
		t.Errorf("expected available list in error, got: %s", out)
	}
	if sess.GetActiveModel() != "" {
		t.Errorf("unknown id leaked into activeModelOverride: %q", sess.GetActiveModel())
	}
}

// TestHandleModel_Switch_CaseSensitive: the user's authored config IDs are
// the trust boundary — coercing case would mask typos.
func TestHandleModel_Switch_CaseSensitive(t *testing.T) {
	sess := newModelTestSession()
	out, _ := tryHandleCommand(context.Background(), "/model MAIN", sess)
	if !strings.Contains(out, "알 수 없는") {
		t.Errorf("case-sensitive match required, got: %s", out)
	}
	if sess.GetActiveModel() != "" {
		t.Errorf("MAIN leaked into activeModelOverride")
	}
}

// TestSession_ActiveModelOverride_AtomicAccessors pins the Set/Get round
// trip + empty default. Keeps the atomic.Pointer wiring honest.
func TestSession_ActiveModelOverride_AtomicAccessors(t *testing.T) {
	sess := newModelTestSession()
	if got := sess.GetActiveModel(); got != "" {
		t.Errorf("default GetActiveModel = %q, want empty", got)
	}
	sess.SetActiveModel("groq-qwen")
	if got := sess.GetActiveModel(); got != "groq-qwen" {
		t.Errorf("after Set: GetActiveModel = %q, want %q", got, "groq-qwen")
	}
	sess.SetActiveModel("")
	if got := sess.GetActiveModel(); got != "" {
		t.Errorf("after Set(\"\"): GetActiveModel = %q, want empty", got)
	}
}
