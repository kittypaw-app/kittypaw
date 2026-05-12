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

type rolloverMemoryProvider struct {
	content string
}

func (p *rolloverMemoryProvider) Generate(_ context.Context, _ []core.LlmMessage) (*llm.Response, error) {
	return &llm.Response{Content: p.content}, nil
}

func (p *rolloverMemoryProvider) GenerateWithTools(ctx context.Context, messages []core.LlmMessage, _ []llm.Tool) (*llm.Response, error) {
	return p.Generate(ctx, messages)
}

func (p *rolloverMemoryProvider) ContextWindow() int { return 200000 }
func (p *rolloverMemoryProvider) MaxTokens() int     { return 4096 }

func rolloverChatEvent(t *testing.T, text, chatID, sessionID, conversationID string) core.Event {
	t.Helper()
	payload, err := json.Marshal(core.ChatPayload{
		ChatID:         chatID,
		Text:           text,
		SessionID:      sessionID,
		ConversationID: conversationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return core.Event{Type: core.EventWebChat, AccountID: "alice", Payload: payload}
}

func useTestRolloverPolicy(t *testing.T, policy rolloverPolicy) {
	t.Helper()
	old := defaultRolloverPolicy
	defaultRolloverPolicy = policy
	t.Cleanup(func() { defaultRolloverPolicy = old })
}

func seedConversationTurns(t *testing.T, st *store.Store, conversationID string, count int) {
	t.Helper()
	for i := range count {
		if err := st.AddConversationTurn(&core.ConversationTurn{
			ConversationID: conversationID,
			Role:           core.RoleUser,
			Content:        "seed turn",
			Timestamp:      string(rune('a' + i)),
		}); err != nil {
			t.Fatalf("AddConversationTurn(%d): %v", i, err)
		}
	}
}

func TestLengthRolloverCreatesChildAndRecordsCurrentTurnThere(t *testing.T) {
	useTestRolloverPolicy(t, rolloverPolicy{Enabled: true, MaxTurns: 4, MinTurnsBeforeRollover: 2, MaxEstimatedTokensRatio: 1})
	st := openTestStore(t)
	parent, err := st.CreateConversation(store.CreateConversationRequest{ScopeType: "general", ScopeID: "parent"})
	if err != nil {
		t.Fatalf("CreateConversation(parent): %v", err)
	}
	seedConversationTurns(t, st, parent.ID, 5)
	payload := core.ChatPayload{ChatID: "chat-1", SessionID: "sess-rollover"}
	routeKey, route := conversationRouteKey(core.EventWebChat, payload)
	route.ConversationID = parent.ID
	if err := st.UpsertConversationRoute(route); err != nil {
		t.Fatalf("UpsertConversationRoute: %v", err)
	}
	cfg := core.DefaultConfig()
	sess := &Session{
		Store:    st,
		Config:   &cfg,
		Provider: &promptCaptureProvider{response: `return "done"`},
		Sandbox:  sandbox.New(cfg.Sandbox),
	}

	out, err := sess.Run(context.Background(), rolloverChatEvent(t, "continue", "chat-1", "sess-rollover", ""), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "Conversation rolled over") || !strings.Contains(out, "done") {
		t.Fatalf("output missing rollover notice or answer: %q", out)
	}
	gotRoute, ok, err := st.ConversationRoute(routeKey)
	if err != nil || !ok {
		t.Fatalf("ConversationRoute ok=%v err=%v", ok, err)
	}
	if gotRoute.ConversationID == parent.ID {
		t.Fatalf("route still points to parent: %+v", gotRoute)
	}
	child, ok, err := st.Conversation(gotRoute.ConversationID)
	if err != nil || !ok {
		t.Fatalf("Conversation(child) ok=%v err=%v", ok, err)
	}
	if child.ParentConversationID != parent.ID || child.RolloverReason != rolloverReasonLengthTurns {
		t.Fatalf("child metadata = %+v", child)
	}
	childTurns, err := st.ListConversationTurnsForConversation(child.ID, 10)
	if err != nil {
		t.Fatalf("List child turns: %v", err)
	}
	if len(childTurns) != 2 || childTurns[0].Content != "continue" || !strings.Contains(childTurns[1].Content, "Conversation rolled over") {
		t.Fatalf("child turns = %+v", childTurns)
	}
	parentTurns, err := st.ListConversationTurnsForConversation(parent.ID, 20)
	if err != nil {
		t.Fatalf("List parent turns: %v", err)
	}
	if len(parentTurns) != 5 {
		t.Fatalf("parent turns = %d, want original 5", len(parentTurns))
	}
}

func TestLengthRolloverIsIdempotentForNextRun(t *testing.T) {
	useTestRolloverPolicy(t, rolloverPolicy{Enabled: true, MaxTurns: 4, MinTurnsBeforeRollover: 2, MaxEstimatedTokensRatio: 1})
	st := openTestStore(t)
	parent, err := st.CreateConversation(store.CreateConversationRequest{ScopeType: "general", ScopeID: "parent"})
	if err != nil {
		t.Fatalf("CreateConversation(parent): %v", err)
	}
	seedConversationTurns(t, st, parent.ID, 5)
	payload := core.ChatPayload{ChatID: "chat-1", SessionID: "sess-rollover"}
	routeKey, route := conversationRouteKey(core.EventWebChat, payload)
	route.ConversationID = parent.ID
	if err := st.UpsertConversationRoute(route); err != nil {
		t.Fatalf("UpsertConversationRoute: %v", err)
	}
	cfg := core.DefaultConfig()
	sess := &Session{
		Store:    st,
		Config:   &cfg,
		Provider: &promptCaptureProvider{response: `return "done"`},
		Sandbox:  sandbox.New(cfg.Sandbox),
	}

	if _, err := sess.Run(context.Background(), rolloverChatEvent(t, "first", "chat-1", "sess-rollover", ""), nil); err != nil {
		t.Fatalf("Run first: %v", err)
	}
	firstRoute, _, _ := st.ConversationRoute(routeKey)
	if _, err := sess.Run(context.Background(), rolloverChatEvent(t, "second", "chat-1", "sess-rollover", ""), nil); err != nil {
		t.Fatalf("Run second: %v", err)
	}
	secondRoute, _, _ := st.ConversationRoute(routeKey)
	if firstRoute.ConversationID != secondRoute.ConversationID {
		t.Fatalf("route changed again: first=%+v second=%+v", firstRoute, secondRoute)
	}
}

func TestProjectConversationDoesNotAutoRollover(t *testing.T) {
	useTestRolloverPolicy(t, rolloverPolicy{Enabled: true, MaxTurns: 4, MinTurnsBeforeRollover: 2, MaxEstimatedTokensRatio: 1})
	st := openTestStore(t)
	if err := st.SetConversationScope("project:alpha", "project", "alpha"); err != nil {
		t.Fatalf("SetConversationScope: %v", err)
	}
	seedConversationTurns(t, st, "project:alpha", 5)
	cfg := core.DefaultConfig()
	sess := &Session{
		Store:    st,
		Config:   &cfg,
		Provider: &promptCaptureProvider{response: `return "done"`},
		Sandbox:  sandbox.New(cfg.Sandbox),
	}

	out, err := sess.Run(context.Background(), rolloverChatEvent(t, "continue", "project:alpha", "sess", "project:alpha"), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(out, "Conversation rolled over") {
		t.Fatalf("project conversation rolled over: %q", out)
	}
	turns, err := st.ListConversationTurnsForConversation("project:alpha", 10)
	if err != nil {
		t.Fatalf("List project turns: %v", err)
	}
	if len(turns) != 7 {
		t.Fatalf("project turns = %d, want seed + user + assistant", len(turns))
	}
}

func TestRolloverNoticeAppearsOnce(t *testing.T) {
	useTestRolloverPolicy(t, rolloverPolicy{Enabled: true, MaxTurns: 4, MinTurnsBeforeRollover: 2, MaxEstimatedTokensRatio: 1})
	st := openTestStore(t)
	parent, err := st.CreateConversation(store.CreateConversationRequest{ScopeType: "general", ScopeID: "parent"})
	if err != nil {
		t.Fatalf("CreateConversation(parent): %v", err)
	}
	seedConversationTurns(t, st, parent.ID, 5)
	payload := core.ChatPayload{ChatID: "chat-1", SessionID: "sess-rollover"}
	_, route := conversationRouteKey(core.EventWebChat, payload)
	route.ConversationID = parent.ID
	if err := st.UpsertConversationRoute(route); err != nil {
		t.Fatalf("UpsertConversationRoute: %v", err)
	}
	cfg := core.DefaultConfig()
	sess := &Session{
		Store:    st,
		Config:   &cfg,
		Provider: &promptCaptureProvider{response: `return "done"`},
		Sandbox:  sandbox.New(cfg.Sandbox),
	}

	out, err := sess.Run(context.Background(), rolloverChatEvent(t, "continue", "chat-1", "sess-rollover", ""), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if count := strings.Count(out, "Conversation rolled over"); count != 1 {
		t.Fatalf("notice count = %d in %q", count, out)
	}
}

func TestTopicShiftSuggestsWithoutSwitching(t *testing.T) {
	useTestRolloverPolicy(t, rolloverPolicy{Enabled: true, MaxTurns: 100, MinTurnsBeforeRollover: 2, MaxEstimatedTokensRatio: 1})
	st := openTestStore(t)
	parent, err := st.CreateConversation(store.CreateConversationRequest{ScopeType: "general", ScopeID: "parent"})
	if err != nil {
		t.Fatalf("CreateConversation(parent): %v", err)
	}
	seedConversationTurns(t, st, parent.ID, 3)
	payload := core.ChatPayload{ChatID: "chat-1", SessionID: "sess-topic"}
	routeKey, route := conversationRouteKey(core.EventWebChat, payload)
	route.ConversationID = parent.ID
	if err := st.UpsertConversationRoute(route); err != nil {
		t.Fatalf("UpsertConversationRoute: %v", err)
	}
	cfg := core.DefaultConfig()
	sess := &Session{
		Store:    st,
		Config:   &cfg,
		Provider: &promptCaptureProvider{response: `return "answer"`},
		Sandbox:  sandbox.New(cfg.Sandbox),
	}

	out, err := sess.Run(context.Background(), rolloverChatEvent(t, "다른 얘기인데 점심 뭐 먹지?", "chat-1", "sess-topic", ""), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "새 대화로 분리") || strings.Contains(out, "Conversation rolled over") {
		t.Fatalf("topic shift output = %q", out)
	}
	gotRoute, _, _ := st.ConversationRoute(routeKey)
	if gotRoute.ConversationID != parent.ID {
		t.Fatalf("topic shift changed route: %+v", gotRoute)
	}
}

func TestRolloverDistillerStoresAllowedMemoryOnly(t *testing.T) {
	st := openTestStore(t)
	for _, turn := range []core.ConversationTurn{
		{ConversationID: "general:parent", Role: core.RoleUser, Content: "나는 한국어 답변을 선호해", Timestamp: "1"},
		{ConversationID: "general:parent", Role: core.RoleAssistant, Content: "알겠습니다.", Timestamp: "2"},
	} {
		if err := st.AddConversationTurn(&turn); err != nil {
			t.Fatalf("AddConversationTurn: %v", err)
		}
	}
	provider := &rolloverMemoryProvider{content: `{
		"memories": [
			{"category":"preference","key":"reply_language","value":"User prefers Korean replies.","confidence":0.93,"reason":"explicit preference"},
			{"category":"decision","key":"rollover_policy","value":"Length rollover is automatic.","confidence":0.85,"reason":"explicit decision"},
			{"category":"secret","key":"api_key","value":"sk-secret","confidence":0.99,"reason":"not allowed"},
			{"category":"open_question","key":"weak","value":"Maybe later","confidence":0.40,"reason":"too weak"}
		]
	}`}

	if err := distillRolloverMemory(context.Background(), st, provider, "general:parent"); err != nil {
		t.Fatalf("distillRolloverMemory: %v", err)
	}
	preferences, err := st.ListUserContextPrefix("memory:preference:")
	if err != nil {
		t.Fatalf("ListUserContextPrefix(preference): %v", err)
	}
	decisions, err := st.ListUserContextPrefix("memory:decision:")
	if err != nil {
		t.Fatalf("ListUserContextPrefix(decision): %v", err)
	}
	if len(preferences) != 1 || !strings.Contains(preferences[0].Value, "Korean") {
		t.Fatalf("preferences = %+v", preferences)
	}
	if len(decisions) != 1 || !strings.Contains(decisions[0].Value, "automatic") {
		t.Fatalf("decisions = %+v", decisions)
	}
	for _, prefix := range []string{"memory:secret:", "memory:open_question:"} {
		rows, err := st.ListUserContextPrefix(prefix)
		if err != nil {
			t.Fatalf("ListUserContextPrefix(%s): %v", prefix, err)
		}
		if len(rows) != 0 {
			t.Fatalf("unexpected rows for %s: %+v", prefix, rows)
		}
	}
}

func TestRolloverDistillerIgnoresInvalidJSON(t *testing.T) {
	st := openTestStore(t)
	if err := st.AddConversationTurn(&core.ConversationTurn{
		ConversationID: "general:parent",
		Role:           core.RoleUser,
		Content:        "remember this",
		Timestamp:      "1",
	}); err != nil {
		t.Fatalf("AddConversationTurn: %v", err)
	}
	provider := &rolloverMemoryProvider{content: `not json`}

	if err := distillRolloverMemory(context.Background(), st, provider, "general:parent"); err == nil {
		t.Fatal("distillRolloverMemory succeeded for invalid JSON")
	}
	rows, err := st.ListUserContextPrefix("memory:")
	if err != nil {
		t.Fatalf("ListUserContextPrefix: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("memory rows after invalid JSON = %+v", rows)
	}
}

func TestRolloverMemoryKeyIsStableAndCapped(t *testing.T) {
	longKey := strings.Repeat("very-long-key-", 20)
	key1 := rolloverMemoryContextKey("preference", longKey, "Korean replies")
	key2 := rolloverMemoryContextKey("preference", longKey, "Korean replies")
	key3 := rolloverMemoryContextKey("preference", longKey, "English replies")

	if key1 != key2 {
		t.Fatalf("key not stable: %q != %q", key1, key2)
	}
	if key1 == key3 {
		t.Fatalf("different values produced same key: %q", key1)
	}
	if !strings.HasPrefix(key1, "memory:preference:") {
		t.Fatalf("key prefix = %q", key1)
	}
	if len(key1) > len("memory:preference:")+80 {
		t.Fatalf("key too long: %d %q", len(key1), key1)
	}
}
