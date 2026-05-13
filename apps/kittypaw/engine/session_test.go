package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/llm"
	"github.com/jinto/kittypaw/sandbox"
	"github.com/jinto/kittypaw/store"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestRefreshAllowedPathsUsesProjectRoots(t *testing.T) {
	st := openTestStore(t)
	root := t.TempDir()
	if _, err := st.CreateProject(store.CreateProjectRequest{Key: "kitty", Name: "KittyPaw", RootPath: root}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	sess := &Session{Store: st}

	if err := sess.RefreshAllowedPaths(); err != nil {
		t.Fatalf("RefreshAllowedPaths: %v", err)
	}

	allowed := sess.AllowedPaths()
	if len(allowed) != 1 {
		t.Fatalf("AllowedPaths = %#v, want one project root", allowed)
	}
	want, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if allowed[0] != want {
		t.Fatalf("AllowedPaths[0] = %q, want %q", allowed[0], want)
	}
}

func TestResolveWorkspaceIDFindsProjectRoot(t *testing.T) {
	st := openTestStore(t)
	root := t.TempDir()
	project, err := st.CreateProject(store.CreateProjectRequest{Key: "kitty", Name: "KittyPaw", RootPath: root})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	sess := &Session{Store: st}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	resolvedPath := filepath.Join(resolvedRoot, "README.md")

	got, err := resolveWorkspaceID(sess, resolvedPath)
	if err != nil {
		t.Fatalf("resolveWorkspaceID: %v", err)
	}
	if got != project.ID {
		t.Fatalf("resolveWorkspaceID = %q, want project id %q", got, project.ID)
	}
}

type promptCaptureProvider struct {
	response string
	messages []core.LlmMessage
}

func (p *promptCaptureProvider) Generate(_ context.Context, msgs []core.LlmMessage) (*llm.Response, error) {
	p.messages = append([]core.LlmMessage(nil), msgs...)
	return &llm.Response{Content: p.response, Usage: &llm.TokenUsage{Model: "mock"}}, nil
}

func (p *promptCaptureProvider) GenerateWithTools(ctx context.Context, msgs []core.LlmMessage, _ []llm.Tool) (*llm.Response, error) {
	return p.Generate(ctx, msgs)
}

func (p *promptCaptureProvider) ContextWindow() int { return 128_000 }
func (p *promptCaptureProvider) MaxTokens() int     { return 4096 }

func TestResolveStaffName_MentionOverride(t *testing.T) {
	cfg := core.DefaultConfig()
	st := openTestStore(t)
	got := ResolveStaffName(&cfg, "telegram", "user-1", "english-teacher", st, t.TempDir())
	if got != "english-teacher" {
		t.Errorf("got %q, want %q", got, "english-teacher")
	}
}

func TestResolveStaffName_SessionOverride(t *testing.T) {
	cfg := core.DefaultConfig()
	st := openTestStore(t)
	baseDir := t.TempDir()
	seedActiveStaffFile(t, baseDir, "custom-bot", "", "custom staff")
	if err := st.SetConversationStaff("custom-bot"); err != nil {
		t.Fatal(err)
	}
	got := ResolveStaffName(&cfg, "telegram", "user-1", "", st, baseDir)
	if got != "custom-bot" {
		t.Errorf("got %q, want %q", got, "custom-bot")
	}
}

func TestResolveStaffName_ConversationDefaultStaff(t *testing.T) {
	cfg := core.DefaultConfig()
	st := openTestStore(t)
	baseDir := t.TempDir()
	seedActiveStaffFile(t, baseDir, "conv-bot", "", "conversation staff")
	conv, err := st.CreateConversation(store.CreateConversationRequest{
		ScopeType:      "general",
		ScopeID:        "conv-bot-thread",
		DefaultStaffID: "conv-bot",
	})
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}

	got := ResolveStaffName(&cfg, "web", conv.ID, "", st, baseDir)
	if got != "conv-bot" {
		t.Errorf("got %q, want conv-bot", got)
	}
}

func TestResolveStaffName_ConversationDefaultBeatsLegacyAccountStaff(t *testing.T) {
	cfg := core.DefaultConfig()
	st := openTestStore(t)
	baseDir := t.TempDir()
	seedActiveStaffFile(t, baseDir, "legacy-bot", "", "legacy staff")
	seedActiveStaffFile(t, baseDir, "conv-bot", "", "conversation staff")
	if err := st.SetConversationStaff("legacy-bot"); err != nil {
		t.Fatalf("SetConversationStaff: %v", err)
	}
	conv, err := st.CreateConversation(store.CreateConversationRequest{
		ScopeType:      "general",
		ScopeID:        "conv-bot-thread",
		DefaultStaffID: "conv-bot",
	})
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}

	got := ResolveStaffName(&cfg, "web", conv.ID, "", st, baseDir)
	if got != "conv-bot" {
		t.Errorf("got %q, want conv-bot", got)
	}
}

func TestResolveStaffName_ChannelBinding(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Staff = []core.StaffConfig{
		{ID: "tg-bot", Nick: "TG", Channels: []string{"telegram"}},
		{ID: "slack-bot", Nick: "SL", Channels: []string{"slack"}},
	}
	st := openTestStore(t)
	baseDir := t.TempDir()
	seedActiveStaffFile(t, baseDir, "tg-bot", "", "telegram staff")
	got := ResolveStaffName(&cfg, "telegram", "user-1", "", st, baseDir)
	if got != "tg-bot" {
		t.Errorf("got %q, want %q", got, "tg-bot")
	}
}

func TestResolveStaffName_Default(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.DefaultStaff = "my-default"
	st := openTestStore(t)
	got := ResolveStaffName(&cfg, "web", "user-1", "", st, t.TempDir())
	if got != "my-default" {
		t.Errorf("got %q, want %q", got, "my-default")
	}
}

func TestResolveStaffName_NilStore(t *testing.T) {
	cfg := core.DefaultConfig()
	// nil store should not panic, just skip session override.
	got := ResolveStaffName(&cfg, "web", "user-1", "", nil, t.TempDir())
	if got != cfg.DefaultStaff {
		t.Errorf("got %q, want %q", got, cfg.DefaultStaff)
	}
}

// --- T5: Staff.switch integration ---

func TestStaffSwitch_SetsContext(t *testing.T) {
	st := openTestStore(t)

	base := t.TempDir()
	seedActiveStaffFile(t, base, "new-staff", "", "test staff")
	if err := st.SetConversationStaff("new-staff"); err != nil {
		t.Fatal(err)
	}

	cfg := core.DefaultConfig()
	got := ResolveStaffName(&cfg, "web", "user-42", "", st, base)
	if got != "new-staff" {
		t.Errorf("got %q, want %q", got, "new-staff")
	}
}

func TestStaffSwitch_ExecuteStaffSetsContext(t *testing.T) {
	st := openTestStore(t)
	baseDir := t.TempDir()
	seedActiveStaffFile(t, baseDir, "finance", "", "재무담당 스태프")
	cfg := core.DefaultConfig()
	sess := &Session{Store: st, Config: &cfg, BaseDir: baseDir}
	ctx := ContextWithConversationID(context.Background(), "conv-1")

	out, err := executeStaff(ctx, core.SkillCall{
		Method: "switch",
		Args:   []json.RawMessage{json.RawMessage(`"finance"`)},
	}, sess)
	if err != nil {
		t.Fatalf("executeStaff error: %v", err)
	}
	var result struct {
		Success bool   `json:"success"`
		Staff   string `json:"staff"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !result.Success || result.Staff != "finance" || result.Error != "" {
		t.Fatalf("result = %+v, want successful finance switch", result)
	}
	conv, ok, err := st.Conversation("conv-1")
	if err != nil || !ok {
		t.Fatalf("Conversation(conv-1) ok=%v err=%v", ok, err)
	}
	if conv.DefaultStaffID != "finance" {
		t.Fatalf("default staff = %q, want finance", conv.DefaultStaffID)
	}
}

func TestStaffSwitch_MissingStaffDoesNotSetContext(t *testing.T) {
	st := openTestStore(t)
	cfg := core.DefaultConfig()
	sess := &Session{
		Store:   st,
		Config:  &cfg,
		BaseDir: t.TempDir(),
	}
	ctx := ContextWithConversationID(context.Background(), "conv-1")

	out, err := executeStaff(ctx, core.SkillCall{
		Method: "switch",
		Args:   []json.RawMessage{json.RawMessage(`"missing-staff"`)},
	}, sess)
	if err != nil {
		t.Fatalf("executeStaff error: %v", err)
	}
	var result struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.Error != `staff "missing-staff"를 찾지 못했습니다` {
		t.Fatalf("error = %q, want missing staff error", result.Error)
	}
	if got, ok, err := st.ConversationStaff(); err != nil || ok {
		t.Fatalf("conversation staff = %q ok=%v err=%v, want unset", got, ok, err)
	}
}

func TestStaffUpdateChangesDescription(t *testing.T) {
	st := openTestStore(t)
	baseDir := t.TempDir()
	seedActiveStaffFile(t, baseDir, "finance", "", "old desc", "budget")
	cfg := core.DefaultConfig()
	sess := &Session{Store: st, Config: &cfg, BaseDir: baseDir}

	out, err := executeStaff(context.Background(), core.SkillCall{
		Method: "update",
		Args: []json.RawMessage{
			json.RawMessage(`"finance"`),
			json.RawMessage(`"new desc"`),
		},
	}, sess)
	if err != nil {
		t.Fatalf("executeStaff error: %v", err)
	}
	var result struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !result.Success || result.Error != "" {
		t.Fatalf("result = %+v, want successful update", result)
	}
	meta, err := core.ReadStaffMetaFile(baseDir, "finance")
	if err != nil {
		t.Fatalf("staff meta missing after update: %v", err)
	}
	if meta.Description != "new desc" {
		t.Fatalf("description = %q, want new desc", meta.Description)
	}
	if len(meta.Aliases) != 1 || meta.Aliases[0] != "budget" {
		t.Fatalf("aliases = %v, want preserved budget alias", meta.Aliases)
	}
}

// --- resolveProvider ---

func TestResolveProvider_EmptyReturnsDefault(t *testing.T) {
	mock := &mockProvider{}
	sess := &Session{
		Provider: mock,
		Config:   &core.Config{LLM: core.LLMConfig{Provider: "anthropic", Model: "default"}},
	}
	if got := sess.resolveProvider(""); got != mock {
		t.Error("empty model should return session default provider")
	}
}

func TestResolveProvider_NamedModel(t *testing.T) {
	mock := &mockProvider{}
	cfg := core.DefaultConfig()
	cfg.LLM = core.LLMConfig{Provider: "anthropic", APIKey: "test-key", Model: "default-model", MaxTokens: 1024}
	cfg.Models = []core.ModelConfig{
		{Name: "fast", Provider: "anthropic", APIKey: "test-key", Model: "claude-3-haiku", MaxTokens: 2048},
	}
	sess := &Session{Provider: mock, Config: &cfg}
	got := sess.resolveProvider("fast")
	if got == mock {
		t.Error("named model should create a new provider")
	}
	if got.MaxTokens() != 2048 {
		t.Errorf("MaxTokens = %d, want 2048 (from named model config)", got.MaxTokens())
	}
}

func TestResolveProvider_UnknownModelFallsBack(t *testing.T) {
	mock := &mockProvider{}
	cfg := core.DefaultConfig()
	cfg.LLM = core.LLMConfig{Provider: "anthropic", APIKey: "test-key", Model: "default-model", MaxTokens: 1024}
	sess := &Session{Provider: mock, Config: &cfg}
	// Raw model IDs not in config should fall back to default (security: no API key leakage).
	if got := sess.resolveProvider("claude-3-opus-20240229"); got != mock {
		t.Error("unknown model should fall back to session default provider")
	}
}

func TestResolveProvider_InvalidProviderFallsBack(t *testing.T) {
	mock := &mockProvider{}
	cfg := core.DefaultConfig()
	cfg.LLM = core.LLMConfig{Provider: "nonexistent", Model: "x"}
	sess := &Session{Provider: mock, Config: &cfg}
	if got := sess.resolveProvider("any-model"); got != mock {
		t.Error("invalid provider should fall back to session default")
	}
}

func TestStaffSwitch_OverriddenByMention(t *testing.T) {
	st := openTestStore(t)
	baseDir := t.TempDir()
	seedActiveStaffFile(t, baseDir, "session-staff", "", "session staff")
	if err := st.SetConversationStaff("session-staff"); err != nil {
		t.Fatal(err)
	}

	cfg := core.DefaultConfig()
	// @mention should win over session override.
	got := ResolveStaffName(&cfg, "web", "user-42", "mention-staff", st, baseDir)
	if got != "mention-staff" {
		t.Errorf("got %q, want %q", got, "mention-staff")
	}
}

func TestRunAtMentionRoutesPromptAndStoresStrippedConversationTurn(t *testing.T) {
	skipWithoutRuntime(t)

	base := t.TempDir()
	staffDir := filepath.Join(base, "staff", "finance")
	if err := os.MkdirAll(staffDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staffDir, "SOUL.md"), []byte("FINANCE_SOUL_MARKER"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := openTestStore(t)
	cfg := core.DefaultConfig()
	provider := &promptCaptureProvider{response: `return "finance ok";`}
	sess := &Session{
		Provider:  provider,
		Sandbox:   sandbox.New(cfg.Sandbox),
		Store:     st,
		Config:    &cfg,
		BaseDir:   base,
		AccountID: "alice",
		Pipeline:  NewPipelineState(),
	}

	out, err := sess.Run(context.Background(), webChatEvent("@finance 포트폴리오 정리해줘"), nil)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if out != "finance ok" {
		t.Fatalf("out = %q, want finance ok", out)
	}

	if len(provider.messages) == 0 || !strings.Contains(provider.messages[0].Content, "FINANCE_SOUL_MARKER") {
		t.Fatalf("prompt did not include mentioned staff soul: %+v", provider.messages)
	}

	turns, err := st.ListConversationTurns(10)
	if err != nil {
		t.Fatalf("list turns: %v", err)
	}
	if len(turns) < 1 {
		t.Fatal("expected stored conversation turns")
	}
	if turns[0].Role != core.RoleUser || turns[0].Content != "포트폴리오 정리해줘" {
		t.Fatalf("first turn = (%s,%q), want stripped user text", turns[0].Role, turns[0].Content)
	}
	if turns[0].Channel != "web" || turns[0].ChannelUserID != "test-session" {
		t.Fatalf("turn metadata = channel %q user %q", turns[0].Channel, turns[0].ChannelUserID)
	}
}

func TestRunAtMentionResolvesAlias(t *testing.T) {
	skipWithoutRuntime(t)

	base := t.TempDir()
	seedActiveStaffFile(t, base, "dev-pm", "개발 PM", "DEV_PM_SOUL_MARKER", "개발PM")

	st := openTestStore(t)
	cfg := core.DefaultConfig()
	provider := &promptCaptureProvider{response: `return "alias ok";`}
	sess := &Session{
		Provider:  provider,
		Sandbox:   sandbox.New(cfg.Sandbox),
		Store:     st,
		Config:    &cfg,
		BaseDir:   base,
		AccountID: "alice",
		Pipeline:  NewPipelineState(),
	}

	out, err := sess.Run(context.Background(), webChatEvent("@개발PM 일정 정리"), nil)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if out != "alias ok" {
		t.Fatalf("out = %q, want alias ok", out)
	}
	if len(provider.messages) == 0 || !strings.Contains(provider.messages[0].Content, "dev-pm soul") {
		t.Fatalf("prompt did not include alias staff soul: %+v", provider.messages)
	}
	turns, err := st.ListConversationTurns(10)
	if err != nil {
		t.Fatalf("ListConversationTurns: %v", err)
	}
	if len(turns) < 1 || turns[0].Content != "일정 정리" {
		t.Fatalf("turns = %+v, want stripped alias mention text", turns)
	}
}

func TestRunRecordsChosenStaffOnAssistantTurn(t *testing.T) {
	skipWithoutRuntime(t)

	base := t.TempDir()
	seedActiveStaffFile(t, base, "conv-bot", "", "conversation staff")
	st := openTestStore(t)
	conv, err := st.CreateConversation(store.CreateConversationRequest{
		ScopeType:      "general",
		ScopeID:        "web_chat:test-session",
		DefaultStaffID: "conv-bot",
	})
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if err := st.UpsertConversationRoute(store.ConversationRoute{
		RouteKey:       "web_chat:test-session",
		ConversationID: conv.ID,
		SourceChannel:  "web_chat",
	}); err != nil {
		t.Fatalf("UpsertConversationRoute: %v", err)
	}

	cfg := core.DefaultConfig()
	provider := &promptCaptureProvider{response: `return "staff audit";`}
	sess := &Session{
		Provider:  provider,
		Sandbox:   sandbox.New(cfg.Sandbox),
		Store:     st,
		Config:    &cfg,
		BaseDir:   base,
		AccountID: "alice",
		Pipeline:  NewPipelineState(),
	}

	out, err := sess.Run(context.Background(), webChatEvent("audit staff"), nil)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if out != "staff audit" {
		t.Fatalf("out = %q, want staff audit", out)
	}
	turns, err := st.ListConversationTurnsForConversation(conv.ID, 10)
	if err != nil {
		t.Fatalf("ListConversationTurnsForConversation: %v", err)
	}
	if len(turns) < 2 {
		t.Fatalf("turns = %+v, want user and assistant", turns)
	}
	assistant := turns[len(turns)-1]
	if assistant.Role != core.RoleAssistant || assistant.StaffID != "conv-bot" {
		t.Fatalf("assistant turn = %+v, want staff_id conv-bot", assistant)
	}
}

func TestRunRecordsPromptAuditMetadata(t *testing.T) {
	skipWithoutRuntime(t)

	st := openTestStore(t)
	cfg := core.DefaultConfig()
	cfg.User.Timezone = "Asia/Seoul"
	provider := &promptCaptureProvider{response: `return "audit ok";`}
	sess := &Session{
		Provider:  provider,
		Sandbox:   sandbox.New(cfg.Sandbox),
		Store:     st,
		Config:    &cfg,
		BaseDir:   t.TempDir(),
		AccountID: "alice",
		Pipeline:  NewPipelineState(),
	}

	out, err := sess.Run(context.Background(), webChatEvent("audit this prompt"), nil)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if out != "audit ok" {
		t.Fatalf("out = %q, want audit ok", out)
	}
	execs, err := st.RecentExecutions(1)
	if err != nil {
		t.Fatalf("RecentExecutions: %v", err)
	}
	if len(execs) != 1 {
		t.Fatalf("executions = %d, want 1", len(execs))
	}
	var meta struct {
		PromptHash     string   `json:"prompt_hash"`
		Layers         []string `json:"layers"`
		StaffID        string   `json:"staff_id"`
		ConversationID string   `json:"conversation_id"`
		Channel        string   `json:"channel"`
		Source         struct {
			ChatID        string `json:"chat_id"`
			ChannelUserID string `json:"channel_user_id"`
		} `json:"source"`
	}
	if err := json.Unmarshal([]byte(execs[0].MetadataJSON), &meta); err != nil {
		t.Fatalf("decode metadata %q: %v", execs[0].MetadataJSON, err)
	}
	if meta.PromptHash == "" {
		t.Fatalf("prompt_hash missing in metadata: %+v", meta)
	}
	if !slices.Contains(meta.Layers, "runtime_context") || !slices.Contains(meta.Layers, "skills") {
		t.Fatalf("layers missing runtime/skills: %+v", meta.Layers)
	}
	if meta.StaffID != "default" || meta.ConversationID != testWebChatConversationID || meta.Channel != "web" {
		t.Fatalf("prompt audit route metadata = %+v", meta)
	}
	if meta.Source.ChatID != "test-chat" || meta.Source.ChannelUserID != "test-session" {
		t.Fatalf("prompt audit source = %+v", meta.Source)
	}
}

func TestSlashStaffUseSetsCurrentConversationDefault(t *testing.T) {
	st := openTestStore(t)
	base := t.TempDir()
	seedActiveStaffFile(t, base, "dev-pm", "", "development pm")
	cfg := core.DefaultConfig()
	sess := &Session{Store: st, Config: &cfg, BaseDir: base}
	ctx := ContextWithConversationID(context.Background(), "general:web_chat:test-session")

	got := handleStaffCommand(ctx, []string{"use", "dev-pm"}, sess)
	if !strings.Contains(got, "dev-pm") {
		t.Fatalf("handleStaffCommand = %q, want dev-pm", got)
	}
	conv, ok, err := st.Conversation("general:web_chat:test-session")
	if err != nil || !ok {
		t.Fatalf("Conversation ok=%v err=%v", ok, err)
	}
	if conv.DefaultStaffID != "dev-pm" {
		t.Fatalf("DefaultStaffID = %q, want dev-pm", conv.DefaultStaffID)
	}
}

func TestResolveSkillCallRejectsDisallowedStaffSkill(t *testing.T) {
	cfg := core.DefaultConfig()
	sess := &Session{Config: &cfg, Store: openTestStore(t), BaseDir: t.TempDir()}
	ctx := ContextWithStaffPolicy(context.Background(), "reader", []string{"Memory"})

	out, err := resolveSkillCall(ctx, core.SkillCall{SkillName: "File", Method: "read"}, sess, nil)
	if err != nil {
		t.Fatalf("resolveSkillCall error: %v", err)
	}
	if !strings.Contains(out, "not allowed for staff reader") {
		t.Fatalf("out = %s, want staff policy rejection", out)
	}
}

func TestProviderForStaffTurnUsesStaffModelUnlessOverriddenOrFallback(t *testing.T) {
	defaultProvider := &promptCaptureProvider{response: `return "default";`}
	staffProvider := &promptCaptureProvider{response: `return "staff";`}
	staff := &core.Staff{ID: "writer", Model: "staff-fast"}
	var resolved []string
	resolve := func(model string) llm.Provider {
		resolved = append(resolved, model)
		return staffProvider
	}

	got := providerForStaffTurn(defaultProvider, staff, false, false, resolve)
	if got != staffProvider || len(resolved) != 1 || resolved[0] != "staff-fast" {
		t.Fatalf("providerForStaffTurn staff model = %T resolved=%v", got, resolved)
	}
	if got := providerForStaffTurn(defaultProvider, staff, true, false, resolve); got != defaultProvider {
		t.Fatal("explicit model override should keep active provider")
	}
	if got := providerForStaffTurn(defaultProvider, staff, false, true, resolve); got != defaultProvider {
		t.Fatal("fallback should keep active fallback provider")
	}
}

func TestRunStoresToolTraceOnAssistantTurn(t *testing.T) {
	skipWithoutRuntime(t)
	t.Setenv("KITTYPAW_TRACE_TEST", "trace-ok")

	cfg := core.DefaultConfig()
	st := openTestStore(t)
	provider := &promptCaptureProvider{response: `
		const env = Env.get("KITTYPAW_TRACE_TEST");
		return env.value;
	`}
	sess := &Session{
		Provider:  provider,
		Sandbox:   sandbox.New(cfg.Sandbox),
		Store:     st,
		Config:    &cfg,
		BaseDir:   t.TempDir(),
		AccountID: "alice",
		Pipeline:  NewPipelineState(),
	}

	out, err := sess.Run(context.Background(), webChatEvent("session-trace"), nil)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if out != "trace-ok" {
		t.Fatalf("out = %q, want trace-ok", out)
	}

	turns, err := st.ListConversationTurns(10)
	if err != nil {
		t.Fatalf("list turns: %v", err)
	}
	if len(turns) < 2 {
		t.Fatalf("turns = %d, want user and assistant turns", len(turns))
	}
	assistant := turns[len(turns)-1]
	if assistant.Role != core.RoleAssistant {
		t.Fatalf("last turn role = %s, want assistant", assistant.Role)
	}
	if len(assistant.ToolTraces) != 1 {
		t.Fatalf("assistant tool traces = %+v, want one trace", assistant.ToolTraces)
	}
	trace := assistant.ToolTraces[0]
	if trace.ID == "" || trace.SkillName != "Env" || trace.Method != "get" || !trace.Success {
		t.Fatalf("assistant trace = %+v, want successful Env.get trace", trace)
	}
	if string(trace.Result) != `{"value":"trace-ok"}` {
		t.Fatalf("trace result = %s", trace.Result)
	}
}

func TestRunPropagatesPermissionCallbackIntoSkillRunFileWrite(t *testing.T) {
	base := t.TempDir()
	workspaceRoot := resolveForValidation(t.TempDir())
	if err := core.SaveSkillTo(base, &core.Skill{
		Name:        "writer",
		Version:     1,
		Description: "writes a workspace file",
		Enabled:     true,
		Format:      core.SkillFormatNative,
		Trigger:     core.SkillTrigger{Type: "manual"},
	}, `
		const result = File.write("nested.txt", "nested ok");
		if (result.error) return result.error;
		return "wrote";
	`); err != nil {
		t.Fatalf("save skill: %v", err)
	}

	st := openTestStore(t)
	cfg := core.DefaultConfig()
	cfg.AutonomyLevel = core.AutonomySupervised
	provider := &promptCaptureProvider{response: `
		const result = Skill.run("writer");
		return result.output;
	`}
	sess := &Session{
		Provider:  provider,
		Sandbox:   sandbox.New(cfg.Sandbox),
		Store:     st,
		Config:    &cfg,
		BaseDir:   base,
		AccountID: "alice",
		Pipeline:  NewPipelineState(),
	}
	paths := []string{workspaceRoot}
	sess.allowedPaths.Store(&paths)

	var approvals []string
	out, err := sess.Run(context.Background(), webChatEvent("run writer"), &RunOptions{
		OnPermission: func(_ context.Context, description, resource string) (bool, error) {
			approvals = append(approvals, resource+":"+description)
			return true, nil
		},
	})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if out != "wrote" {
		t.Fatalf("out = %q, want nested skill output", out)
	}
	if len(approvals) != 1 || !strings.Contains(approvals[0], "File:File.write") {
		t.Fatalf("approvals = %+v, want nested File.write approval", approvals)
	}
	got, err := os.ReadFile(filepath.Join(workspaceRoot, "nested.txt"))
	if err != nil {
		t.Fatalf("read nested write: %v", err)
	}
	if string(got) != "nested ok" {
		t.Fatalf("nested file content = %q", string(got))
	}
}

func TestRunAccumulatesToolTracesAcrossRetry(t *testing.T) {
	t.Setenv("KITTYPAW_TRACE_FIRST", "first")
	t.Setenv("KITTYPAW_TRACE_SECOND", "second")

	cfg := core.DefaultConfig()
	st := openTestStore(t)
	provider := &mockProvider{responses: []*llm.Response{
		{Content: `
			Env.get("KITTYPAW_TRACE_FIRST");
			throw new Error("retry me");
		`, Usage: &llm.TokenUsage{Model: "mock"}},
		{Content: `
			const second = Env.get("KITTYPAW_TRACE_SECOND");
			return second.value;
		`, Usage: &llm.TokenUsage{Model: "mock"}},
	}}
	sess := &Session{
		Provider:  provider,
		Sandbox:   sandbox.New(cfg.Sandbox),
		Store:     st,
		Config:    &cfg,
		BaseDir:   t.TempDir(),
		AccountID: "alice",
		Pipeline:  NewPipelineState(),
	}

	out, err := sess.Run(context.Background(), webChatEvent("retry trace"), nil)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if out != "second" {
		t.Fatalf("out = %q, want second", out)
	}
	turns, err := st.ListConversationTurns(10)
	if err != nil {
		t.Fatalf("list turns: %v", err)
	}
	assistant := turns[len(turns)-1]
	if len(assistant.ToolTraces) != 2 {
		t.Fatalf("tool traces = %+v, want traces from failed attempt and retry", assistant.ToolTraces)
	}
	if string(assistant.ToolTraces[0].Args[0]) != `"KITTYPAW_TRACE_FIRST"` {
		t.Fatalf("first trace args = %s", assistant.ToolTraces[0].Args[0])
	}
	if string(assistant.ToolTraces[1].Args[0]) != `"KITTYPAW_TRACE_SECOND"` {
		t.Fatalf("second trace args = %s", assistant.ToolTraces[1].Args[0])
	}
}

func TestRunCanCreateStaffFromConversationRequest(t *testing.T) {
	skipWithoutRuntime(t)

	st := openTestStore(t)
	cfg := core.DefaultConfig()
	provider := &promptCaptureProvider{response: `
const created = Staff.create("finance", "재무담당 스태프");
return created.output || created.error || "missing draft output";
`}
	sess := &Session{
		Provider:  provider,
		Sandbox:   sandbox.New(cfg.Sandbox),
		Store:     st,
		Config:    &cfg,
		BaseDir:   t.TempDir(),
		AccountID: "alice",
		Pipeline:  NewPipelineState(),
	}

	out, err := sess.Run(context.Background(), webChatEvent("도구 테스트"), nil)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !strings.Contains(out, "초안") || !strings.Contains(out, "생성") {
		t.Fatalf("out = %q, want draft approval response", out)
	}
	if base, err := core.ResolveBaseDir(sess.BaseDir); err != nil || core.StaffHasSoul(base, "finance") {
		t.Fatalf("finance active staff = true, base err=%v, want false nil", err)
	}
	if _, ok, err := loadPendingStaffDraft(sess.BaseDir, testWebChatConversationID); err != nil || !ok {
		t.Fatalf("pending draft ok=%v err=%v, want ok true nil", ok, err)
	}
}

func TestStaffNaturalLanguageCreateFlow(t *testing.T) {
	st := openTestStore(t)
	cfg := core.DefaultConfig()
	baseDir := t.TempDir()
	sess := &Session{
		Store:     st,
		Config:    &cfg,
		BaseDir:   baseDir,
		AccountID: "alice",
		Pipeline:  NewPipelineState(),
	}

	out, err := sess.Run(context.Background(), webChatEvent("개발PM 한 명 만들어줘"), nil)
	if err != nil {
		t.Fatalf("Run request error: %v", err)
	}
	if !strings.Contains(out, "Staff 기능") {
		t.Fatalf("first response = %q, want Staff opt-in question", out)
	}
	if _, ok, err := loadPendingStaffDraft(baseDir, testWebChatConversationID); err != nil || ok {
		t.Fatalf("pending draft after opt-in question ok=%v err=%v, want none", ok, err)
	}

	out, err = sess.Run(context.Background(), webChatEvent("응"), nil)
	if err != nil {
		t.Fatalf("Run opt-in error: %v", err)
	}
	if !strings.Contains(out, "초안") || !strings.Contains(out, "dev-pm") {
		t.Fatalf("opt-in response = %q, want dev-pm draft", out)
	}
	if _, ok, err := loadPendingStaffDraft(baseDir, testWebChatConversationID); err != nil || !ok {
		t.Fatalf("pending draft after opt-in ok=%v err=%v, want ok true nil", ok, err)
	}
	if base, err := core.ResolveBaseDir(baseDir); err != nil || core.StaffHasSoul(base, "dev-pm") {
		t.Fatalf("staff active after draft err=%v, want inactive", err)
	}

	out, err = sess.Run(context.Background(), webChatEvent("생성해"), nil)
	if err != nil {
		t.Fatalf("Run approval error: %v", err)
	}
	if !strings.Contains(out, "만들었어요") || !strings.Contains(out, "지금 이 대화") {
		t.Fatalf("approval response = %q, want creation plus switch question", out)
	}
	meta, err := core.ReadStaffMetaFile(baseDir, "dev-pm")
	if err != nil {
		t.Fatalf("staff meta after approval: %v", err)
	}
	if meta.DisplayName != "개발 PM" {
		t.Fatalf("DisplayName = %q, want 개발 PM", meta.DisplayName)
	}
	if conv, ok, err := st.Conversation(testWebChatConversationID); err != nil {
		t.Fatalf("conversation before switch err=%v", err)
	} else if ok && conv.DefaultStaffID != "" {
		t.Fatalf("default staff before switch = %q, want unset", conv.DefaultStaffID)
	}

	out, err = sess.Run(context.Background(), webChatEvent("응"), nil)
	if err != nil {
		t.Fatalf("Run switch confirmation error: %v", err)
	}
	if !strings.Contains(out, "dev-pm") {
		t.Fatalf("switch response = %q, want dev-pm", out)
	}
	conv, ok, err := st.Conversation(testWebChatConversationID)
	if err != nil || !ok {
		t.Fatalf("Conversation(%s) ok=%v err=%v", testWebChatConversationID, ok, err)
	}
	if conv.DefaultStaffID != "dev-pm" {
		t.Fatalf("default staff = %q, want dev-pm", conv.DefaultStaffID)
	}
}

func TestStaffNaturalLanguageContextualRequestUsesLLMConversationForDraft(t *testing.T) {
	st := openTestStore(t)
	cfg := core.DefaultConfig()
	baseDir := t.TempDir()
	if err := st.AddConversationTurn(&core.ConversationTurn{
		Role:    core.RoleUser,
		Content: "이번 릴리즈는 요구사항 정리와 우선순위 조율이 계속 필요해요.",
	}); err != nil {
		t.Fatalf("seed user turn: %v", err)
	}
	if err := st.AddConversationTurn(&core.ConversationTurn{
		Role:    core.RoleAssistant,
		Content: "진행상황과 블로커를 정리해서 관리하는 역할이 있으면 좋겠습니다.",
	}); err != nil {
		t.Fatalf("seed assistant turn: %v", err)
	}
	provider := &promptCaptureProvider{response: `{
		"id": "pm",
		"display_name": "PM",
		"description": "요구사항 정리, 우선순위 조율, 진행상황 추적, 블로커 관리",
		"aliases": ["pm", "피엠"],
		"soul": "You are PM, a KittyPaw staff member.\n\n## Role\n요구사항 정리, 우선순위 조율, 진행상황 추적, 블로커 관리\n\n## Working Style\n- Keep plans practical.\n- Respond in Korean."
	}`}
	sess := &Session{
		Provider:  provider,
		Store:     st,
		Config:    &cfg,
		BaseDir:   baseDir,
		AccountID: "alice",
		Pipeline:  NewPipelineState(),
	}

	out, err := sess.Run(context.Background(), webChatEvent("우리 대화내용을 보고 pm 을 한사람 채용해주세요."), nil)
	if err != nil {
		t.Fatalf("Run request error: %v", err)
	}
	if !strings.Contains(out, "Staff 기능") {
		t.Fatalf("first response = %q, want Staff opt-in question", out)
	}

	out, err = sess.Run(context.Background(), webChatEvent("네네"), nil)
	if err != nil {
		t.Fatalf("Run opt-in error: %v", err)
	}
	if !strings.Contains(out, "시스템 이름: pm") || strings.Contains(out, "우리 대화내용") {
		t.Fatalf("draft response = %q, want LLM-authored pm draft without copied request preamble", out)
	}
	if len(provider.messages) == 0 {
		t.Fatal("staff draft LLM was not called")
	}
	prompt := provider.messages[len(provider.messages)-1].Content
	if !strings.Contains(prompt, "이번 릴리즈는 요구사항 정리") || !strings.Contains(prompt, "우리 대화내용을 보고 pm") {
		t.Fatalf("staff draft prompt missing conversation/request context:\n%s", prompt)
	}
	draft, ok, err := loadPendingStaffDraft(baseDir, testWebChatConversationID)
	if err != nil || !ok {
		t.Fatalf("pending draft ok=%v err=%v, want ok true nil", ok, err)
	}
	if draft.ID != "pm" || draft.DisplayName != "PM" {
		t.Fatalf("draft = %+v, want id pm display PM", draft)
	}
	if strings.Contains(draft.Description, "우리 대화내용") {
		t.Fatalf("draft description copied request preamble: %q", draft.Description)
	}
}

func TestStaffNaturalLanguageAcceptsCasualOptInAndSwitchConfirmation(t *testing.T) {
	st := openTestStore(t)
	cfg := core.DefaultConfig()
	sess := &Session{
		Store:     st,
		Config:    &cfg,
		BaseDir:   t.TempDir(),
		AccountID: "alice",
		Pipeline:  NewPipelineState(),
	}

	out, err := sess.Run(context.Background(), webChatEvent("개발PM 을 한명 채용해주세요."), nil)
	if err != nil {
		t.Fatalf("Run request error: %v", err)
	}
	if !strings.Contains(out, "Staff 기능") {
		t.Fatalf("first response = %q, want Staff opt-in question", out)
	}

	out, err = sess.Run(context.Background(), webChatEvent("오.. 좋아요."), nil)
	if err != nil {
		t.Fatalf("Run casual opt-in error: %v", err)
	}
	if !strings.Contains(out, "초안") || !strings.Contains(out, "dev-pm") {
		t.Fatalf("casual opt-in response = %q, want dev-pm draft", out)
	}

	out, err = sess.Run(context.Background(), webChatEvent("이대로 생성해주세요."), nil)
	if err != nil {
		t.Fatalf("Run approval error: %v", err)
	}
	if !strings.Contains(out, "만들었어요") || !strings.Contains(out, "지금 이 대화") {
		t.Fatalf("approval response = %q, want creation plus switch question", out)
	}

	out, err = sess.Run(context.Background(), webChatEvent("오.. 좋아요."), nil)
	if err != nil {
		t.Fatalf("Run casual switch confirmation error: %v", err)
	}
	if !strings.Contains(out, "dev-pm") {
		t.Fatalf("switch response = %q, want dev-pm", out)
	}
	conv, ok, err := st.Conversation(testWebChatConversationID)
	if err != nil || !ok {
		t.Fatalf("Conversation(%s) ok=%v err=%v", testWebChatConversationID, ok, err)
	}
	if conv.DefaultStaffID != "dev-pm" {
		t.Fatalf("default staff = %q, want dev-pm", conv.DefaultStaffID)
	}
}

func TestStaffNaturalLanguageDoesNotOverwritePendingDraft(t *testing.T) {
	st := openTestStore(t)
	cfg := core.DefaultConfig()
	sess := &Session{
		Store:     st,
		Config:    &cfg,
		BaseDir:   t.TempDir(),
		AccountID: "alice",
		Pipeline:  NewPipelineState(),
	}
	if err := savePendingStaffDraft(sess.BaseDir, testWebChatConversationID, buildStaffDraft("개발PM", "test")); err != nil {
		t.Fatal(err)
	}

	out, err := sess.Run(context.Background(), webChatEvent("디자이너 한 명 만들어줘"), nil)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !strings.Contains(out, "이미") || !strings.Contains(out, "dev-pm") {
		t.Fatalf("response = %q, want existing draft notice", out)
	}
	draft, ok, err := loadPendingStaffDraft(sess.BaseDir, testWebChatConversationID)
	if err != nil || !ok {
		t.Fatalf("pending draft ok=%v err=%v, want ok true nil", ok, err)
	}
	if draft.ID != "dev-pm" {
		t.Fatalf("pending draft ID = %q, want dev-pm", draft.ID)
	}
	if role, ok, err := loadPendingStaffOffer(st, testWebChatConversationID); err != nil || ok {
		t.Fatalf("pending offer = %q ok=%v err=%v, want none", role, ok, err)
	}
}

// ---------------------------------------------------------------------------
// augmentSystemPromptWithSuggestion
// ---------------------------------------------------------------------------

func newSuggestionTestMessages() []core.LlmMessage {
	return []core.LlmMessage{
		{Role: core.RoleSystem, Content: "## base prompt"},
	}
}

func TestAugmentSystemPromptWithSuggestion_FirstTurnInjects(t *testing.T) {
	st := openTestStore(t)
	// Reflection has detected an intent — store it the way RunReflectionCycle does.
	if err := st.SetUserContext(
		"suggest_candidate:abc123", "환율 조회|3|0 8 * * *", "reflection",
	); err != nil {
		t.Fatalf("seed candidate: %v", err)
	}
	msgs := newSuggestionTestMessages()
	turns := []core.ConversationTurn{
		{Role: core.RoleUser, Content: "hello"}, // just-added first turn
	}

	augmentSystemPromptWithSuggestion(msgs, st, turns)

	if !strings.Contains(msgs[0].Content, "환율 조회") {
		t.Errorf("expected suggestion label in system prompt; got: %q", msgs[0].Content)
	}
	// Surface time recorded so the next session does not re-surface.
	if v, ok, _ := st.GetUserContext("surfaced_at:abc123"); !ok || v == "" {
		t.Errorf("surfaced_at not recorded; got ok=%v v=%q", ok, v)
	}
}

func TestAugmentSystemPromptWithSuggestion_NotFirstTurnSkips(t *testing.T) {
	st := openTestStore(t)
	_ = st.SetUserContext("suggest_candidate:abc123", "환율 조회|3|0 8 * * *", "reflection")
	msgs := newSuggestionTestMessages()
	turns := []core.ConversationTurn{
		{Role: core.RoleUser, Content: "first"},
		{Role: core.RoleAssistant, Content: "answered"},
		{Role: core.RoleUser, Content: "second"},
	}

	augmentSystemPromptWithSuggestion(msgs, st, turns)

	if strings.Contains(msgs[0].Content, "환율 조회") {
		t.Error("mid-session turn must not surface suggestion")
	}
	if _, ok, _ := st.GetUserContext("surfaced_at:abc123"); ok {
		t.Error("surfaced_at must not be recorded when no surface happened")
	}
}

func TestAugmentSystemPromptWithSuggestion_SilenceWindowSuppresses(t *testing.T) {
	st := openTestStore(t)
	_ = st.SetUserContext("suggest_candidate:abc123", "환율 조회|3|0 8 * * *", "reflection")
	// Pretend the candidate was surfaced 1 hour ago — well within the
	// 7-day silence window. Must stay suppressed even on a first turn.
	_ = st.SetUserContext(
		"surfaced_at:abc123",
		time.Now().Add(-1*time.Hour).UTC().Format(time.RFC3339),
		"suggestion",
	)
	msgs := newSuggestionTestMessages()
	turns := []core.ConversationTurn{{Role: core.RoleUser, Content: "hi"}}

	augmentSystemPromptWithSuggestion(msgs, st, turns)

	if strings.Contains(msgs[0].Content, "환율 조회") {
		t.Error("candidate surfaced inside silence window must stay suppressed")
	}
}

func TestAugmentSystemPromptWithSuggestion_AfterSilenceResurfaces(t *testing.T) {
	st := openTestStore(t)
	_ = st.SetUserContext("suggest_candidate:abc123", "환율 조회|3|0 8 * * *", "reflection")
	// Surfaced 8 days ago — silence window has elapsed. Must surface again.
	_ = st.SetUserContext(
		"surfaced_at:abc123",
		time.Now().Add(-8*24*time.Hour).UTC().Format(time.RFC3339),
		"suggestion",
	)
	msgs := newSuggestionTestMessages()
	turns := []core.ConversationTurn{{Role: core.RoleUser, Content: "hi"}}

	augmentSystemPromptWithSuggestion(msgs, st, turns)

	if !strings.Contains(msgs[0].Content, "환율 조회") {
		t.Error("candidate past silence window must re-surface")
	}
}

func TestAugmentSystemPromptWithSuggestion_NoCandidatesNoOp(t *testing.T) {
	st := openTestStore(t)
	msgs := newSuggestionTestMessages()
	turns := []core.ConversationTurn{{Role: core.RoleUser, Content: "hi"}}

	augmentSystemPromptWithSuggestion(msgs, st, turns)

	if msgs[0].Content != "## base prompt" {
		t.Errorf("no-candidate path must not mutate prompt; got %q", msgs[0].Content)
	}
}

func TestAugmentSystemPromptWithSuggestion_MalformedValueSkipped(t *testing.T) {
	st := openTestStore(t)
	// Empty label after split — must skip this candidate but still
	// look at the next one.
	_ = st.SetUserContext("suggest_candidate:bad", "  |3|0 8 * * *", "reflection")
	_ = st.SetUserContext("suggest_candidate:good", "주가 알림|5|0 9 * * 1-5", "reflection")
	msgs := newSuggestionTestMessages()
	turns := []core.ConversationTurn{{Role: core.RoleUser, Content: "hi"}}

	augmentSystemPromptWithSuggestion(msgs, st, turns)

	if strings.Contains(msgs[0].Content, "  |") {
		t.Error("malformed candidate must not be surfaced")
	}
	if !strings.Contains(msgs[0].Content, "주가 알림") {
		t.Error("subsequent well-formed candidate must surface")
	}
}

// ---------------------------------------------------------------------------
// appendSuggestionForBranchResponse
// ---------------------------------------------------------------------------

func newSuggestionBranchTestSession(t *testing.T) *Session {
	t.Helper()
	st := openTestStore(t)
	return &Session{Store: st}
}

func newWebChatEvent(sessionID string) core.Event {
	payload, _ := json.Marshal(core.ChatPayload{
		ChatID:    sessionID,
		SessionID: sessionID,
		Text:      "환율",
	})
	return core.Event{Type: core.EventWebChat, Payload: payload}
}

func TestAppendSuggestionForBranchResponse_FirstTurnAppends(t *testing.T) {
	s := newSuggestionBranchTestSession(t)
	if err := s.Store.SetUserContext(
		"suggest_candidate:abc123", "환율 조회|3|0 8 * * *", "reflection-test",
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	event := newWebChatEvent("session-fresh")
	got := appendSuggestionForBranchResponse(s, event, "현재 환율은 1480원입니다")

	if !strings.Contains(got, "💡") {
		t.Errorf("first-turn branch must append suggestion suffix; got %q", got)
	}
	if !strings.Contains(got, "환율 조회") {
		t.Errorf("suffix must include candidate label; got %q", got)
	}
	if v, ok, _ := s.Store.GetUserContext("surfaced_at:abc123"); !ok || v == "" {
		t.Errorf("surfaced_at not recorded; ok=%v v=%q", ok, v)
	}
}

func TestAppendSuggestionForBranchResponse_NotFirstTurnSkips(t *testing.T) {
	s := newSuggestionBranchTestSession(t)
	_ = s.Store.SetUserContext("suggest_candidate:abc123", "환율 조회|3|0 8 * * *", "reflection-test")

	// Pre-existing assistant turn in the account conversation means this is
	// not the first turn, even if the channel session differs.
	state := &core.ConversationState{
		ConversationID: conversationKey(s),
		SystemPrompt:   SystemPrompt,
		Turns: []core.ConversationTurn{
			{Role: core.RoleUser, Content: "이전"},
			{Role: core.RoleAssistant, Content: "응답"},
		},
	}
	if err := s.Store.SaveConversationState(state); err != nil {
		t.Fatalf("save state: %v", err)
	}

	event := newWebChatEvent("session-existing")
	got := appendSuggestionForBranchResponse(s, event, "현재 환율은 1480원")

	if strings.Contains(got, "💡") {
		t.Errorf("subsequent turn must not append suggestion; got %q", got)
	}
	if _, ok, _ := s.Store.GetUserContext("surfaced_at:abc123"); ok {
		t.Errorf("surfaced_at must not be recorded when no surface happened")
	}
}

func TestAppendSuggestionForBranchResponse_NoCandidateUnchanged(t *testing.T) {
	s := newSuggestionBranchTestSession(t)
	event := newWebChatEvent("session-empty")
	got := appendSuggestionForBranchResponse(s, event, "현재 환율은 1480원")

	if got != "현재 환율은 1480원" {
		t.Errorf("no-candidate path must not mutate response; got %q", got)
	}
}

func TestAppendSuggestionForBranchResponse_SilenceWindowSuppresses(t *testing.T) {
	s := newSuggestionBranchTestSession(t)
	_ = s.Store.SetUserContext("suggest_candidate:abc123", "환율 조회|3|0 8 * * *", "reflection-test")
	_ = s.Store.SetUserContext(
		"surfaced_at:abc123",
		time.Now().Add(-1*time.Hour).UTC().Format(time.RFC3339),
		"suggestion",
	)

	event := newWebChatEvent("session-silenced")
	got := appendSuggestionForBranchResponse(s, event, "현재 환율은 1480원")

	if strings.Contains(got, "💡") {
		t.Errorf("silenced candidate must not surface; got %q", got)
	}
}

// --- ApplyActiveModel: chat-path /model swap fold-in ---
//
// Contract (engine/session.go ApplyActiveModel doc):
//   - active=="" → returns opts unchanged
//   - opts==nil + active=="x" → returns &RunOptions{ModelOverride:"x"}
//   - opts.ModelOverride=="" + active=="x" → returns copy with override="x"
//   - opts.ModelOverride=="y" + active=="x" → returns opts unchanged
//     (explicit per-call wins, schedule path is unaffected by chat /model)
//
// The schedule-path isolation is enforced by NOT calling ApplyActiveModel
// in engine/schedule.go (TestSchedule_DoesNotCallApplyActiveModel pins this).

func TestApplyActiveModel_NilOpts_NoActive(t *testing.T) {
	s := &Session{}
	got := s.ApplyActiveModel(nil)
	if got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}

func TestApplyActiveModel_NilOpts_WithActive(t *testing.T) {
	s := &Session{}
	s.SetActiveModel("groq-qwen")
	got := s.ApplyActiveModel(nil)
	if got == nil || got.ModelOverride != "groq-qwen" {
		t.Errorf("got %+v, want ModelOverride=groq-qwen", got)
	}
}

func TestApplyActiveModel_OptsBlank_WithActive(t *testing.T) {
	s := &Session{}
	s.SetActiveModel("groq-qwen")
	in := &RunOptions{}
	got := s.ApplyActiveModel(in)
	if got.ModelOverride != "groq-qwen" {
		t.Errorf("ModelOverride = %q, want groq-qwen", got.ModelOverride)
	}
	// Caller's input must not be mutated (callers may reuse RunOptions).
	if in.ModelOverride != "" {
		t.Errorf("input opts mutated: ModelOverride = %q", in.ModelOverride)
	}
}

func TestApplyActiveModel_OptsExplicit_WinsOverActive(t *testing.T) {
	// Explicit per-call ModelOverride (e.g. chat_relay_dispatcher's
	// body.ModelOverride or schedule.go's per-job model) must win over
	// the chat-set /model override.
	s := &Session{}
	s.SetActiveModel("groq-qwen")
	in := &RunOptions{ModelOverride: "main"}
	got := s.ApplyActiveModel(in)
	if got.ModelOverride != "main" {
		t.Errorf("ModelOverride = %q, want main (explicit wins)", got.ModelOverride)
	}
}

func TestApplyActiveModel_NoActive_ReturnsOptsUnchanged(t *testing.T) {
	s := &Session{}
	in := &RunOptions{ModelOverride: "main"}
	got := s.ApplyActiveModel(in)
	if got != in {
		t.Errorf("got %p, want unchanged %p", got, in)
	}
}

// TestSchedule_DoesNotCallApplyActiveModel: static check that schedule.go
// never funnels chat-set /model overrides into scheduler-launched runs.
// Re-pinning the contract via grep — call this whenever schedule.go is
// touched. Keeps the chat REPL `/model <id>` from contaminating cron-style
// reflectionTick / tickOnce executions, which must always honor the
// per-job model selection.
func TestSchedule_DoesNotCallApplyActiveModel(t *testing.T) {
	data, err := os.ReadFile("schedule.go")
	if err != nil {
		t.Fatalf("read schedule.go: %v", err)
	}
	src := string(data)
	for _, banned := range []string{"ApplyActiveModel", "GetActiveModel"} {
		if strings.Contains(src, banned) {
			t.Errorf("schedule.go must not call %s — chat-path only contract (see ApplyActiveModel doc)", banned)
		}
	}
}
