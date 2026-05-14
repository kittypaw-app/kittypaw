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

func slashCommandEvent(t *testing.T, text string) core.Event {
	t.Helper()
	payload, err := json.Marshal(core.ChatPayload{
		ChatID:          "test-chat",
		SourceSessionID: "test-session",
		Text:            text,
	})
	if err != nil {
		t.Fatal(err)
	}
	return core.Event{Type: core.EventWebChat, Payload: payload}
}

func TestUnknownSlashCommandIsHandledDeterministically(t *testing.T) {
	st := openTestStore(t)
	cfg := core.DefaultConfig()
	provider := &promptCaptureProvider{response: "llm fallback should not run"}
	sess := &AccountRuntime{Store: st, Config: &cfg, Provider: provider}

	out, err := sess.Run(context.Background(), slashCommandEvent(t, "/stats"), nil)
	if err != nil {
		t.Fatalf("Run unknown slash: %v", err)
	}
	if !strings.Contains(out, "알 수 없는 명령") || !strings.Contains(out, "/help") {
		t.Fatalf("unknown slash output = %q, want deterministic help", out)
	}
	if strings.Contains(out, "llm fallback") || len(provider.messages) != 0 {
		t.Fatalf("unknown slash fell through to provider: output=%q messages=%d", out, len(provider.messages))
	}
}

func TestHelpIsGeneratedFromRegisteredCommands(t *testing.T) {
	cfg := core.DefaultConfig()
	sess := &AccountRuntime{Config: &cfg}

	out, handled := tryHandleCommand(context.Background(), "/help", sess)
	if !handled {
		t.Fatal("/help was not handled")
	}
	for _, cmd := range registeredSlashCommands() {
		if !strings.Contains(out, cmd.Usage) {
			t.Fatalf("/help missing registered command usage %q:\n%s", cmd.Usage, out)
		}
		if cmd.Risk == "" {
			t.Fatalf("registered command %q has empty risk metadata", cmd.Name)
		}
	}
	if !strings.Contains(out, "기록") {
		t.Fatalf("/help should expose history/audit metadata, got:\n%s", out)
	}
}

func TestSlashStaffSwitchesConversationDefaultStaff(t *testing.T) {
	st := openTestStore(t)
	baseDir := t.TempDir()
	seedActiveStaffFile(t, baseDir, "finance", "", "재무담당 스태프")
	cfg := core.DefaultConfig()
	sess := &AccountRuntime{
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
	conv, ok, err := st.Conversation(store.DefaultConversationID)
	if err != nil || !ok {
		t.Fatalf("conversation ok=%v err=%v", ok, err)
	}
	if conv.DefaultStaffID != "finance" {
		t.Fatalf("default staff = %q, want finance", conv.DefaultStaffID)
	}
}

func TestSlashStaffUseMissingDoesNotSwitchThroughFallbackSoul(t *testing.T) {
	st := openTestStore(t)
	cfg := core.DefaultConfig()
	sess := &AccountRuntime{
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
	sess := &AccountRuntime{
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
	sess := &AccountRuntime{
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
	sess := &AccountRuntime{
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
	sess := &AccountRuntime{Store: st, Config: &cfg}
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

func TestSlashProjectUsePersistsCurrentProjectForTickets(t *testing.T) {
	st := openTestStore(t)
	cfg := core.DefaultConfig()
	sess := &AccountRuntime{Store: st, Config: &cfg, AccountID: "alice"}

	first, err := st.CreateProject(store.CreateProjectRequest{Key: "first", Name: "First", RootPath: t.TempDir()})
	if err != nil {
		t.Fatalf("CreateProject(first): %v", err)
	}
	second, err := st.CreateProject(store.CreateProjectRequest{Key: "second", Name: "Second", RootPath: t.TempDir()})
	if err != nil {
		t.Fatalf("CreateProject(second): %v", err)
	}
	firstTicket, err := st.CreateTicket(store.CreateTicketRequest{ProjectID: first.ID, Title: "First ticket"})
	if err != nil {
		t.Fatalf("CreateTicket(first): %v", err)
	}
	secondTicket, err := st.CreateTicket(store.CreateTicketRequest{ProjectID: second.ID, Title: "Second ticket"})
	if err != nil {
		t.Fatalf("CreateTicket(second): %v", err)
	}

	ctx := ContextWithConversationID(context.Background(), "conv-project")
	out, handled := tryHandleCommand(ctx, "/project use SECOND", sess)
	if !handled || !strings.Contains(out, "선택") || !strings.Contains(out, "SECOND") {
		t.Fatalf("/project use output = %q handled=%v", out, handled)
	}

	out, handled = tryHandleCommand(ctx, "/project current", sess)
	if !handled || !strings.Contains(out, "SECOND") {
		t.Fatalf("/project current output = %q handled=%v, want selected project", out, handled)
	}

	out, handled = tryHandleCommand(ctx, "/tickets", sess)
	if !handled || !strings.Contains(out, secondTicket.Key) || strings.Contains(out, firstTicket.Key) {
		t.Fatalf("/tickets output = %q handled=%v, want selected project only", out, handled)
	}
}

func TestTicketChatCommandIsExplicitlyAdvisory(t *testing.T) {
	st := openTestStore(t)
	cfg := core.DefaultConfig()
	sess := &AccountRuntime{Store: st, Config: &cfg}
	project, err := st.CreateProject(store.CreateProjectRequest{Key: "kitty", Name: "KittyPaw", RootPath: t.TempDir()})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	ticket, err := st.CreateTicket(store.CreateTicketRequest{ProjectID: project.ID, Title: "Chat target"})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	out, handled := tryHandleCommand(context.Background(), "/ticket chat "+ticket.Key, sess)
	if !handled {
		t.Fatal("/ticket chat was not handled")
	}
	for _, want := range []string{"안내", ticket.TicketConversationID, "전환하지"} {
		if !strings.Contains(out, want) {
			t.Fatalf("/ticket chat output missing %q:\n%s", want, out)
		}
	}
}

func TestSlashRunExecutesInstalledSkill(t *testing.T) {
	baseDir := t.TempDir()
	cfg := core.DefaultConfig()
	sess := &AccountRuntime{
		BaseDir: baseDir,
		Config:  &cfg,
		Sandbox: sandbox.New(cfg.Sandbox),
	}
	if err := core.SaveSkillTo(baseDir, &core.SkillManifest{
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

func TestSlashRunResultIsRecordedInConversationHistory(t *testing.T) {
	baseDir := t.TempDir()
	st := openTestStore(t)
	cfg := core.DefaultConfig()
	sess := &AccountRuntime{
		BaseDir: baseDir,
		Config:  &cfg,
		Store:   st,
		Sandbox: sandbox.New(cfg.Sandbox),
	}
	if err := core.SaveSkillTo(baseDir, &core.SkillManifest{
		Name:        "hello",
		Description: "test skill",
		Enabled:     true,
	}, `return "hello from slash history"`); err != nil {
		t.Fatalf("save skill: %v", err)
	}

	out, err := sess.Run(context.Background(), slashCommandEvent(t, "/run hello"), nil)
	if err != nil {
		t.Fatalf("Run(/run): %v", err)
	}
	if out != "hello from slash history" {
		t.Fatalf("/run output = %q", out)
	}

	turns, err := st.ListConversationTurnsForConversation(testWebChatConversationID, 10)
	if err != nil {
		t.Fatalf("ListConversationTurnsForConversation: %v", err)
	}
	if len(turns) != 2 {
		t.Fatalf("turns = %+v, want user and assistant turns", turns)
	}
	if turns[0].Content != "/run hello" || turns[1].Content != "hello from slash history" {
		t.Fatalf("turns = %+v, want slash command transcript", turns)
	}
}

func TestSlashHelpIsNotRecordedInConversationHistory(t *testing.T) {
	st := openTestStore(t)
	cfg := core.DefaultConfig()
	sess := &AccountRuntime{Store: st, Config: &cfg}

	if _, err := sess.Run(context.Background(), slashCommandEvent(t, "/help"), nil); err != nil {
		t.Fatalf("Run(/help): %v", err)
	}

	turns, err := st.ListConversationTurnsForConversation(testWebChatConversationID, 10)
	if err != nil {
		t.Fatalf("ListConversationTurnsForConversation: %v", err)
	}
	if len(turns) != 0 {
		t.Fatalf("/help should not be recorded, got %+v", turns)
	}
}

func TestSlashModelSwitchIsRecordedInConversationHistory(t *testing.T) {
	st := openTestStore(t)
	cfg := core.DefaultConfig()
	cfg.LLM.Default = "main"
	cfg.LLM.Models = []core.ModelConfig{
		{ID: "main", Provider: "openai", Model: "gpt-main"},
		{ID: "alt", Provider: "openai", Model: "gpt-alt"},
	}
	sess := &AccountRuntime{Store: st, Config: &cfg}

	out, err := sess.Run(context.Background(), slashCommandEvent(t, "/model alt"), nil)
	if err != nil {
		t.Fatalf("Run(/model): %v", err)
	}
	if !strings.Contains(out, "alt") {
		t.Fatalf("/model output = %q", out)
	}

	turns, err := st.ListConversationTurnsForConversation(testWebChatConversationID, 10)
	if err != nil {
		t.Fatalf("ListConversationTurnsForConversation: %v", err)
	}
	if len(turns) != 2 {
		t.Fatalf("turns = %+v, want model switch transcript", turns)
	}
	if turns[0].Content != "/model alt" || !strings.Contains(turns[1].Content, "alt") {
		t.Fatalf("turns = %+v, want model switch transcript", turns)
	}
}

func TestSlashSessionAndContextDiagnostics(t *testing.T) {
	st := openTestStore(t)
	cfg := core.DefaultConfig()
	cfg.LLM.Default = "main"
	cfg.LLM.Models = []core.ModelConfig{{
		ID:            "main",
		Provider:      "anthropic",
		Model:         "claude-sonnet-4-6",
		ContextWindow: 200000,
		MaxTokens:     4096,
	}}
	sess := &AccountRuntime{Store: st, Config: &cfg, AccountID: "alice"}
	for _, turn := range []core.ConversationTurn{
		{ConversationID: store.DefaultConversationID, Role: core.RoleUser, Content: "hello", Timestamp: "1"},
		{ConversationID: store.DefaultConversationID, Role: core.RoleAssistant, Content: "world", Timestamp: "2"},
	} {
		if err := st.AddConversationTurn(&turn); err != nil {
			t.Fatalf("AddConversationTurn: %v", err)
		}
	}
	if _, err := st.CreateCheckpoint("before diagnostics"); err != nil {
		t.Fatalf("CreateCheckpoint: %v", err)
	}

	out, handled := tryHandleCommand(ContextWithConversationID(context.Background(), store.DefaultConversationID), "/conversation", sess)
	if !handled {
		t.Fatal("/conversation was not handled")
	}
	for _, want := range []string{"conversation", store.DefaultConversationID, "alice", "turns: 2", "checkpoint"} {
		if !strings.Contains(out, want) {
			t.Fatalf("/conversation output missing %q:\n%s", want, out)
		}
	}

	out, handled = tryHandleCommand(ContextWithConversationID(context.Background(), store.DefaultConversationID), "/context", sess)
	if !handled {
		t.Fatal("/context was not handled")
	}
	for _, want := range []string{"prompt_tokens", "recent_window", "context_window", "200000"} {
		if !strings.Contains(out, want) {
			t.Fatalf("/context output missing %q:\n%s", want, out)
		}
	}
}

func TestCompactCommandCompactsCurrentConversation(t *testing.T) {
	st := openTestStore(t)
	if err := st.SetConversationScope("project:alpha", "project", "alpha"); err != nil {
		t.Fatalf("SetConversationScope: %v", err)
	}
	for _, content := range []string{"general-0", "general-1", "general-2", "general-3", "general-4"} {
		if err := st.AddConversationTurn(&core.ConversationTurn{
			ConversationID: store.DefaultConversationID,
			Role:           core.RoleUser,
			Content:        content,
			Timestamp:      content,
		}); err != nil {
			t.Fatalf("AddConversationTurn(general): %v", err)
		}
	}
	for _, content := range []string{"project-0", "project-1", "project-2", "project-3", "project-4"} {
		if err := st.AddConversationTurn(&core.ConversationTurn{
			ConversationID: "project:alpha",
			Role:           core.RoleUser,
			Content:        content,
			Timestamp:      content,
		}); err != nil {
			t.Fatalf("AddConversationTurn(project): %v", err)
		}
	}
	cfg := core.DefaultConfig()
	sess := &AccountRuntime{Store: st, Config: &cfg, AccountID: "alice"}

	out, handled := tryHandleCommand(ContextWithConversationID(context.Background(), "project:alpha"), "/compact 2", sess)
	if !handled {
		t.Fatal("/compact was not handled")
	}
	for _, want := range []string{"conversation: project:alpha", "turns_compacted: 3", "keep_recent: 2"} {
		if !strings.Contains(out, want) {
			t.Fatalf("/compact output missing %q:\n%s", want, out)
		}
	}

	projectState, err := st.LoadConversationStateForChat("project:alpha")
	if err != nil {
		t.Fatalf("LoadConversationStateForChat(project): %v", err)
	}
	if len(projectState.Turns) != 3 || !strings.Contains(projectState.Turns[0].Content, "오래된 대화 3개") {
		t.Fatalf("project state = %+v, want summary + 2 recent", projectState.Turns)
	}
	generalState, err := st.LoadConversationStateForChat(store.DefaultConversationID)
	if err != nil {
		t.Fatalf("LoadConversationStateForChat(general): %v", err)
	}
	if got := len(generalState.Turns); got != 5 {
		t.Fatalf("general turns = %d, want un-compacted 5", got)
	}
}

func TestCompactCommandStoresSemanticSummaryWhenProviderAvailable(t *testing.T) {
	st := openTestStore(t)
	cfg := core.DefaultConfig()
	provider := &mockProvider{responses: []*llm.Response{mockResp("## Current Goal\nPreserve the provider migration decision and pending tests.")}}
	sess := &AccountRuntime{Store: st, Config: &cfg, Provider: provider, AccountID: "alice"}

	for i := 0; i < 5; i++ {
		if err := st.AddConversationTurn(&core.ConversationTurn{
			ConversationID: "project:alpha",
			Role:           core.RoleUser,
			Content:        "Move all vendor API calls behind one provider boundary.",
			Timestamp:      string(rune('a' + i)),
		}); err != nil {
			t.Fatalf("AddConversationTurn(project %d): %v", i, err)
		}
	}

	out, handled := tryHandleCommand(ContextWithConversationID(context.Background(), "project:alpha"), "/compact 2", sess)
	if !handled {
		t.Fatal("/compact was not handled")
	}
	if !strings.Contains(out, "turns_compacted: 3") {
		t.Fatalf("/compact output = %q, want compacted count", out)
	}
	if provider.callIdx != 1 {
		t.Fatalf("provider calls = %d, want 1 semantic compaction call", provider.callIdx)
	}

	state, err := st.LoadConversationStateForChat("project:alpha")
	if err != nil {
		t.Fatalf("LoadConversationStateForChat(project): %v", err)
	}
	if len(state.Turns) != 3 {
		t.Fatalf("state turns = %d, want summary + 2 recent", len(state.Turns))
	}
	summary := state.Turns[0].Content
	if !strings.Contains(summary, "Preserve the provider migration decision") {
		t.Fatalf("summary = %q, want semantic provider summary", summary)
	}
	if strings.Contains(summary, "오래된 대화 3개") {
		t.Fatalf("summary = %q, want semantic summary instead of deterministic count", summary)
	}
}

func TestCompactCommandFallsBackToDeterministicSummaryWhenSemanticSummaryFails(t *testing.T) {
	st := openTestStore(t)
	cfg := core.DefaultConfig()
	sess := &AccountRuntime{Store: st, Config: &cfg, Provider: &mockProvider{}, AccountID: "alice"}

	for i := 0; i < 5; i++ {
		if err := st.AddConversationTurn(&core.ConversationTurn{
			ConversationID: store.DefaultConversationID,
			Role:           core.RoleUser,
			Content:        "keep this conversation compactable",
			Timestamp:      string(rune('a' + i)),
		}); err != nil {
			t.Fatalf("AddConversationTurn(%d): %v", i, err)
		}
	}

	out, handled := tryHandleCommand(ContextWithConversationID(context.Background(), store.DefaultConversationID), "/compact 2", sess)
	if !handled {
		t.Fatal("/compact was not handled")
	}
	if !strings.Contains(out, "turns_compacted: 3") {
		t.Fatalf("/compact output = %q, want fallback compaction success", out)
	}

	state, err := st.LoadConversationState()
	if err != nil {
		t.Fatalf("LoadConversationState: %v", err)
	}
	if len(state.Turns) != 3 ||
		!strings.Contains(state.Turns[0].Content, "오래된 대화 3개") ||
		!strings.Contains(state.Turns[0].Content, "keep this conversation compactable") {
		t.Fatalf("state turns = %+v, want semantic fallback summary + 2 recent", state.Turns)
	}
}

func TestCompactCommandRejectsInvalidKeepRecent(t *testing.T) {
	st := openTestStore(t)
	cfg := core.DefaultConfig()
	sess := &AccountRuntime{Store: st, Config: &cfg, AccountID: "alice"}

	out, handled := tryHandleCommand(context.Background(), "/compact nope", sess)
	if !handled {
		t.Fatal("/compact was not handled")
	}
	if !strings.Contains(out, "사용법: /compact [keep_recent]") {
		t.Fatalf("/compact invalid output = %q, want usage", out)
	}
}

func TestConversationCommandShowsRolloverMetadata(t *testing.T) {
	st := openTestStore(t)
	parent, err := st.CreateConversation(store.CreateConversationRequest{ScopeType: "general", ScopeID: "parent"})
	if err != nil {
		t.Fatalf("CreateConversation(parent): %v", err)
	}
	child, err := st.CreateConversation(store.CreateConversationRequest{
		ScopeType:            "general",
		ScopeID:              "child",
		ParentConversationID: parent.ID,
		RolloverReason:       rolloverReasonLengthTurns,
		RolloverFromTurnID:   12,
		SourceChannel:        "web_chat",
		SourceSessionID:      "sess-1",
		ChatID:               "chat-1",
	})
	if err != nil {
		t.Fatalf("CreateConversation(child): %v", err)
	}
	cfg := core.DefaultConfig()
	sess := &AccountRuntime{Store: st, Config: &cfg, AccountID: "alice"}

	out, handled := tryHandleCommand(ContextWithConversationID(context.Background(), child.ID), "/conversation", sess)
	if !handled {
		t.Fatal("/conversation was not handled")
	}
	for _, want := range []string{"parent_conversation", parent.ID, "rollover_reason", rolloverReasonLengthTurns, "rollover_from_turn: 12", "route_source_session_id: sess-1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("/conversation output missing %q:\n%s", want, out)
		}
	}
}

func TestConversationCommandRenamesCurrentConversation(t *testing.T) {
	st := openTestStore(t)
	cfg := core.DefaultConfig()
	sess := &AccountRuntime{Store: st, Config: &cfg, AccountID: "alice"}

	out, handled := tryHandleCommand(ContextWithConversationID(context.Background(), store.DefaultConversationID), "/conversation rename Provider Migration", sess)
	if !handled {
		t.Fatal("/conversation rename was not handled")
	}
	if !strings.Contains(out, "Provider Migration") {
		t.Fatalf("/conversation rename output = %q, want new title", out)
	}

	conv, ok, err := st.Conversation(store.DefaultConversationID)
	if err != nil || !ok {
		t.Fatalf("Conversation ok=%v err=%v", ok, err)
	}
	if conv.Title != "Provider Migration" || conv.TitleSource != "manual" {
		t.Fatalf("conversation = %+v, want manual title", conv)
	}
}

func TestConversationCommandRenamesLazilyCreatedSourceConversation(t *testing.T) {
	st := openTestStore(t)
	cfg := core.DefaultConfig()
	sess := &AccountRuntime{Store: st, Config: &cfg, AccountID: "alice"}
	event := slashCommandEvent(t, "/conversation rename Fresh Thread")
	conversationID := conversationKeyForEvent(sess, &event)

	out, err := sess.Run(context.Background(), event, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(out, "실패") || !strings.Contains(out, "Fresh Thread") {
		t.Fatalf("/conversation rename output = %q, want successful rename", out)
	}

	conv, ok, err := st.Conversation(conversationID)
	if err != nil || !ok {
		t.Fatalf("Conversation(%q) ok=%v err=%v", conversationID, ok, err)
	}
	if conv.Title != "Fresh Thread" || conv.TitleSource != "manual" {
		t.Fatalf("conversation = %+v, want manual title", conv)
	}
}

func TestConversationCommandKeepsSessionAlias(t *testing.T) {
	st := openTestStore(t)
	cfg := core.DefaultConfig()
	runtime := &AccountRuntime{Store: st, Config: &cfg, AccountID: "alice"}

	out, handled := tryHandleCommand(ContextWithConversationID(context.Background(), store.DefaultConversationID), "/session", runtime)
	if !handled {
		t.Fatal("/session alias was not handled")
	}
	if !strings.Contains(out, "conversation: "+store.DefaultConversationID) {
		t.Fatalf("/session alias output missing conversation id:\n%s", out)
	}
}

func TestContextShowsRolloverThreshold(t *testing.T) {
	st := openTestStore(t)
	cfg := core.DefaultConfig()
	sess := &AccountRuntime{Store: st, Config: &cfg}

	out, handled := tryHandleCommand(ContextWithConversationID(context.Background(), store.DefaultConversationID), "/context", sess)
	if !handled {
		t.Fatal("/context was not handled")
	}
	for _, want := range []string{"rollover_max_turns", "rollover_min_turns", "rollover_turns"} {
		if !strings.Contains(out, want) {
			t.Fatalf("/context output missing %q:\n%s", want, out)
		}
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

func newModelTestSession() *AccountRuntime {
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
	return &AccountRuntime{Config: &cfg, AccountID: "alice"}
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
	sess := &AccountRuntime{Config: &cfg}
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
