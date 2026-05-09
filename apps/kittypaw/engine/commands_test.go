package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/sandbox"
	"github.com/jinto/kittypaw/store"
)

func TestSlashStaffSwitchesAccountConversationStaff(t *testing.T) {
	st := openTestStore(t)
	baseDir := t.TempDir()
	seedActiveStaffFile(t, baseDir, "finance", "", "재무담당 스태프")
	cfg := core.DefaultConfig()
	sess := &Session{
		Store:     st,
		Config:    &cfg,
		AccountID: "alice",
		BaseDir:   baseDir,
	}

	out, handled := tryHandleCommand(context.Background(), "/staff finance", sess)
	if !handled {
		t.Fatal("/staff command was not handled")
	}
	if !strings.Contains(out, "finance") {
		t.Fatalf("response should mention selected staff, got %q", out)
	}
	if got, ok, err := st.ConversationStaff(); err != nil || !ok || got != "finance" {
		t.Fatalf("conversation staff = %q ok=%v err=%v, want finance", got, ok, err)
	}
}

func TestSlashStaffUseMissingDoesNotSwitchThroughFallbackSoul(t *testing.T) {
	st := openTestStore(t)
	cfg := core.DefaultConfig()
	sess := &Session{
		Store:     st,
		Config:    &cfg,
		AccountID: "alice",
		BaseDir:   t.TempDir(),
	}

	out, handled := tryHandleCommand(context.Background(), "/staff use paw", sess)
	if !handled {
		t.Fatal("/staff use command was not handled")
	}
	if !strings.Contains(out, "찾지 못했습니다") {
		t.Fatalf("response = %q, want missing staff message", out)
	}
	if got, ok, err := st.ConversationStaff(); err != nil || ok {
		t.Fatalf("conversation staff = %q ok=%v err=%v, want unset", got, ok, err)
	}
}

func TestSlashStaffCurrentListShowHireCancel(t *testing.T) {
	st := openTestStore(t)
	baseDir := t.TempDir()
	seedActiveStaffFile(t, baseDir, "finance", "재무", "재무 정리", "재무")
	cfg := core.DefaultConfig()
	sess := &Session{
		Store:     st,
		Config:    &cfg,
		AccountID: "alice",
		BaseDir:   baseDir,
	}

	out, handled := tryHandleCommand(context.Background(), "/staff", sess)
	if !handled || !strings.Contains(out, "current") || !strings.Contains(out, "finance") {
		t.Fatalf("/staff output = %q handled=%v, want current/list usage", out, handled)
	}

	out, _ = tryHandleCommand(context.Background(), "/staff current", sess)
	if !strings.Contains(out, "default") {
		t.Fatalf("/staff current = %q, want default staff", out)
	}

	out, _ = tryHandleCommand(context.Background(), "/staff list", sess)
	if !strings.Contains(out, "finance") || !strings.Contains(out, "재무") {
		t.Fatalf("/staff list = %q, want seeded staff", out)
	}

	out, _ = tryHandleCommand(context.Background(), "/staff show 재무", sess)
	for _, want := range []string{"finance", "재무", "SOUL.md: yes"} {
		if !strings.Contains(out, want) {
			t.Fatalf("/staff show missing %q in %q", want, out)
		}
	}

	out, _ = tryHandleCommand(context.Background(), "/staff hire 개발PM", sess)
	if !strings.Contains(out, "초안") || !strings.Contains(out, "dev-pm") {
		t.Fatalf("/staff hire = %q, want draft preview", out)
	}
	if _, ok, err := loadPendingStaffDraft(baseDir, "alice"); err != nil || !ok {
		t.Fatalf("pending draft ok=%v err=%v, want ok true nil", ok, err)
	}
	if base, err := core.ResolveBaseDir(baseDir); err != nil || core.StaffHasSoul(base, "dev-pm") {
		t.Fatalf("unexpected active staff from draft: base err=%v", err)
	}

	out, _ = tryHandleCommand(context.Background(), "/staff cancel", sess)
	if !strings.Contains(out, "취소") {
		t.Fatalf("/staff cancel = %q, want cancel message", out)
	}
	if _, ok, err := loadPendingStaffDraft(baseDir, "alice"); err != nil || ok {
		t.Fatalf("pending draft after cancel ok=%v err=%v, want ok false nil", ok, err)
	}
}

func TestSlashStaffHireDoesNotOverwritePendingDraft(t *testing.T) {
	st := openTestStore(t)
	cfg := core.DefaultConfig()
	sess := &Session{
		Store:     st,
		Config:    &cfg,
		AccountID: "alice",
		BaseDir:   t.TempDir(),
	}

	out, _ := tryHandleCommand(context.Background(), "/staff hire 개발PM", sess)
	if !strings.Contains(out, "dev-pm") {
		t.Fatalf("first hire output = %q, want dev-pm draft", out)
	}
	out, _ = tryHandleCommand(context.Background(), "/staff hire 디자이너", sess)
	if !strings.Contains(out, "이미") || !strings.Contains(out, "dev-pm") {
		t.Fatalf("second hire output = %q, want existing draft notice", out)
	}
	draft, ok, err := loadPendingStaffDraft(sess.BaseDir, "alice")
	if err != nil || !ok {
		t.Fatalf("load pending draft ok=%v err=%v, want ok true nil", ok, err)
	}
	if draft.ID != "dev-pm" {
		t.Fatalf("pending draft ID = %q, want original dev-pm", draft.ID)
	}
}

func TestSlashStaffCancelClearsAllPendingStaffState(t *testing.T) {
	st := openTestStore(t)
	cfg := core.DefaultConfig()
	sess := &Session{
		Store:     st,
		Config:    &cfg,
		AccountID: "alice",
		BaseDir:   t.TempDir(),
	}
	if err := savePendingStaffDraft(sess.BaseDir, "alice", buildStaffDraft("개발PM", "test")); err != nil {
		t.Fatal(err)
	}
	if err := savePendingStaffOffer(st, "alice", "개발PM"); err != nil {
		t.Fatal(err)
	}
	if err := savePendingStaffSwitch(st, "alice", "dev-pm"); err != nil {
		t.Fatal(err)
	}

	out, _ := tryHandleCommand(context.Background(), "/staff cancel", sess)
	if !strings.Contains(out, "취소") {
		t.Fatalf("/staff cancel output = %q, want cancel message", out)
	}
	if _, ok, err := loadPendingStaffDraft(sess.BaseDir, "alice"); err != nil || ok {
		t.Fatalf("draft after cancel ok=%v err=%v, want false nil", ok, err)
	}
	if _, ok, err := loadPendingStaffOffer(st, "alice"); err != nil || ok {
		t.Fatalf("offer after cancel ok=%v err=%v, want false nil", ok, err)
	}
	if _, ok, err := loadPendingStaffSwitch(st, "alice"); err != nil || ok {
		t.Fatalf("switch after cancel ok=%v err=%v, want false nil", ok, err)
	}
}

func TestSlashProjectAndTicketCommands(t *testing.T) {
	st := openTestStore(t)
	cfg := core.DefaultConfig()
	sess := &Session{Store: st, Config: &cfg}
	project, err := st.CreateProject(store.CreateProjectRequest{Key: "kitty", Name: "KittyPaw", RootPath: t.TempDir()})
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	ticket, err := st.CreateTicket(store.CreateTicketRequest{ProjectID: project.ID, Title: "Wire Projects commands"})
	if err != nil {
		t.Fatalf("CreateTicket() error = %v", err)
	}

	out, handled := tryHandleCommand(context.Background(), "/projects", sess)
	if !handled || !strings.Contains(out, "KITTY") || !strings.Contains(out, "KittyPaw") {
		t.Fatalf("/projects output = %q handled=%v", out, handled)
	}
	out, handled = tryHandleCommand(context.Background(), "/project show KITTY", sess)
	if !handled || !strings.Contains(out, "KITTY") || !strings.Contains(out, project.RootPath) {
		t.Fatalf("/project show output = %q handled=%v", out, handled)
	}
	out, handled = tryHandleCommand(context.Background(), "/tickets", sess)
	if !handled || !strings.Contains(out, ticket.Key) || !strings.Contains(out, "Wire Projects commands") {
		t.Fatalf("/tickets output = %q handled=%v", out, handled)
	}
	out, handled = tryHandleCommand(context.Background(), "/ticket move "+ticket.Key+" ready", sess)
	if !handled || !strings.Contains(out, "ready") {
		t.Fatalf("/ticket move output = %q handled=%v", out, handled)
	}
	out, handled = tryHandleCommand(context.Background(), "/ticket show "+ticket.Key, sess)
	if !handled || !strings.Contains(out, ticket.Key) || !strings.Contains(out, "ready") {
		t.Fatalf("/ticket show output = %q handled=%v", out, handled)
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
