package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/llm"
	"github.com/jinto/kittypaw/remote/chatrelay"
	"github.com/jinto/kittypaw/store"
)

func TestChatRelayDispatcherModelsListsDefaultAndNamedModels(t *testing.T) {
	root := t.TempDir()
	cfg := core.DefaultConfig()
	cfg.LLM.Default = "main"
	cfg.LLM.Models = []core.ModelConfig{
		{ID: "main", Model: "default-provider-model", Provider: "anthropic"},
		{ID: "fast", Model: "fast-provider-model", Provider: "openai"},
	}
	deps := buildAccountDeps(t, root, "alice", &cfg)
	srv := New([]*AccountDeps{deps}, "test")

	result, err := NewChatRelayDispatcher(srv).Dispatch(context.Background(), chatrelay.RequestFrame{
		ID:        "req_models",
		Operation: chatrelay.OperationOpenAIModels,
		AccountID: "alice",
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if result.Status != 200 {
		t.Fatalf("Status = %d", result.Status)
	}
	if result.Headers["content-type"] != "application/json" {
		t.Fatalf("Headers = %#v", result.Headers)
	}

	var body struct {
		Object string `json:"object"`
		Data   []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(result.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Object != "list" {
		t.Fatalf("object = %q", body.Object)
	}
	if got := modelIDs(body.Data); !reflect.DeepEqual(got, []string{"main", "fast"}) {
		t.Fatalf("model ids = %#v", got)
	}
}

func TestChatRelayDispatcherChatCompletionsRoutesToAccountSession(t *testing.T) {
	root := t.TempDir()
	aliceCfg := core.DefaultConfig()
	aliceProvider := &chatRelayMockProvider{content: "alice reply"}
	aliceDeps := buildAccountDeps(t, root, "alice", &aliceCfg)
	aliceDeps.Provider = aliceProvider

	bobCfg := core.DefaultConfig()
	bobProvider := &chatRelayMockProvider{content: "bob reply"}
	bobDeps := buildAccountDeps(t, root, "bob", &bobCfg)
	bobDeps.Provider = bobProvider

	srv := New([]*AccountDeps{aliceDeps, bobDeps}, "test")
	body := map[string]any{
		"model": "default",
		"messages": []map[string]any{
			{"role": "system", "content": "ignored here"},
			{"role": "user", "content": "hello alice"},
		},
		"metadata": map[string]any{"kittypaw_session_id": "remote-chat-1"},
	}
	raw, _ := json.Marshal(body)

	result, err := NewChatRelayDispatcher(srv).Dispatch(context.Background(), chatrelay.RequestFrame{
		ID:        "turn-1",
		Operation: chatrelay.OperationOpenAIChatCompletions,
		AccountID: "alice",
		Body:      raw,
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if result.Status != 200 {
		t.Fatalf("Status = %d body=%s", result.Status, result.Body)
	}

	var decoded struct {
		Object  string `json:"object"`
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(result.Body, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Object != "chat.completion" {
		t.Fatalf("object = %q", decoded.Object)
	}
	if got := decoded.Choices[0].Message.Content; got != "alice reply" {
		t.Fatalf("content = %q", got)
	}
	if aliceProvider.calls != 1 || bobProvider.calls != 0 {
		t.Fatalf("provider calls alice=%d bob=%d", aliceProvider.calls, bobProvider.calls)
	}
	if !strings.Contains(aliceProvider.lastUserContent, "hello alice") {
		t.Fatalf("last user content = %q", aliceProvider.lastUserContent)
	}
}

func TestChatRelayDispatcherChatCompletionsIncludesProvidedTranscript(t *testing.T) {
	root := t.TempDir()
	cfg := core.DefaultConfig()
	provider := &chatRelayMockProvider{content: "contextual reply"}
	deps := buildAccountDeps(t, root, "alice", &cfg)
	deps.Provider = provider
	srv := New([]*AccountDeps{deps}, "test")
	body := map[string]any{
		"messages": []map[string]any{
			{"role": "user", "content": "first question"},
			{"role": "assistant", "content": "first answer"},
			{"role": "user", "content": "follow up"},
		},
	}
	raw, _ := json.Marshal(body)

	_, err := NewChatRelayDispatcher(srv).Dispatch(context.Background(), chatrelay.RequestFrame{
		ID:        "turn-context",
		Operation: chatrelay.OperationOpenAIChatCompletions,
		AccountID: "alice",
		Body:      raw,
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	for _, want := range []string{"first question", "first answer", "follow up"} {
		if !strings.Contains(provider.lastUserContent, want) {
			t.Fatalf("last user content = %q, want containing %q", provider.lastUserContent, want)
		}
	}
}

func TestChatRelayDispatcherChatCompletionsCanReturnSSE(t *testing.T) {
	root := t.TempDir()
	cfg := core.DefaultConfig()
	deps := buildAccountDeps(t, root, "alice", &cfg)
	deps.Provider = &chatRelayMockProvider{content: "stream reply"}
	srv := New([]*AccountDeps{deps}, "test")
	body := map[string]any{
		"stream": true,
		"messages": []map[string]any{
			{"role": "user", "content": "hello"},
		},
		"user": "remote-chat-2",
	}
	raw, _ := json.Marshal(body)

	result, err := NewChatRelayDispatcher(srv).Dispatch(context.Background(), chatrelay.RequestFrame{
		ID:        "turn-stream",
		Operation: chatrelay.OperationOpenAIChatCompletions,
		AccountID: "alice",
		Body:      raw,
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if result.Headers["content-type"] != "text/event-stream" {
		t.Fatalf("Headers = %#v", result.Headers)
	}
	bodyText := string(result.Body)
	if !strings.Contains(bodyText, `"object":"chat.completion.chunk"`) {
		t.Fatalf("SSE body missing chunk: %s", bodyText)
	}
	if !strings.Contains(bodyText, `"content":"stream reply"`) {
		t.Fatalf("SSE body missing content: %s", bodyText)
	}
	if !strings.Contains(bodyText, "data: [DONE]\n\n") {
		t.Fatalf("SSE body missing done: %s", bodyText)
	}
}

func TestChatRelayDispatcherChatCompletionsReturnsServerErrorShape(t *testing.T) {
	root := t.TempDir()
	cfg := core.DefaultConfig()
	deps := buildAccountDeps(t, root, "alice", &cfg)
	deps.Provider = &chatRelayMockProvider{err: errors.New("provider bad gateway")}
	srv := New([]*AccountDeps{deps}, "test")
	body := map[string]any{
		"messages": []map[string]any{
			{"role": "user", "content": "hello"},
		},
	}
	raw, _ := json.Marshal(body)

	result, err := NewChatRelayDispatcher(srv).Dispatch(context.Background(), chatrelay.RequestFrame{
		ID:        "turn-error",
		Operation: chatrelay.OperationOpenAIChatCompletions,
		AccountID: "alice",
		Body:      raw,
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if result.Status != 500 {
		t.Fatalf("Status = %d body=%s", result.Status, result.Body)
	}
	var decoded struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(result.Body, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Error.Type != "server_error" || decoded.Error.Code != "kittypaw_turn_failed" {
		t.Fatalf("error = %+v, want server_error/kittypaw_turn_failed", decoded.Error)
	}
	if strings.TrimSpace(decoded.Error.Message) == "" {
		t.Fatalf("error message is empty: %+v", decoded.Error)
	}
}

func TestChatRelayDispatcherLocalAPIRoutesProjectsToAccountStore(t *testing.T) {
	aliceCfg := core.DefaultConfig()
	bobCfg := core.DefaultConfig()
	srv := newMultiAccountAuthTestServer(t, "alice", map[string]string{
		"alice": "alice-pw",
		"bob":   "bob-pw",
	}, map[string]*core.Config{
		"alice": &aliceCfg,
		"bob":   &bobCfg,
	})
	bobDeps := srv.accountDepsForID("bob")
	if _, err := bobDeps.Store.CreateProject(store.CreateProjectRequest{
		Key:      "bob",
		Name:     "Bob Work",
		RootPath: t.TempDir(),
	}); err != nil {
		t.Fatalf("seed bob project: %v", err)
	}

	result, err := NewChatRelayDispatcher(srv).Dispatch(context.Background(), chatrelay.RequestFrame{
		ID:        "req_local_api",
		Operation: chatrelay.OperationKittyPawAPI,
		AccountID: "bob",
		Method:    http.MethodGet,
		Path:      "/api/v1/projects",
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if result.Status != http.StatusOK {
		t.Fatalf("Status = %d body=%s", result.Status, result.Body)
	}
	var decoded struct {
		Projects []struct {
			Key string `json:"key"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(result.Body, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Projects) != 1 || decoded.Projects[0].Key != "BOB" {
		t.Fatalf("projects = %+v, want BOB from bob account store", decoded.Projects)
	}
}

func TestChatRelayDispatcherLocalAPIRejectsNonAllowlistedPath(t *testing.T) {
	root := t.TempDir()
	cfg := core.DefaultConfig()
	deps := buildAccountDeps(t, root, "alice", &cfg)
	srv := New([]*AccountDeps{deps}, "test")

	_, err := NewChatRelayDispatcher(srv).Dispatch(context.Background(), chatrelay.RequestFrame{
		ID:        "req_forbidden_api",
		Operation: chatrelay.OperationKittyPawAPI,
		AccountID: "alice",
		Method:    http.MethodPost,
		Path:      "/api/v1/chat",
		Body:      []byte(`{}`),
	})
	if err == nil || !strings.Contains(err.Error(), "local API path is not supported") {
		t.Fatalf("Dispatch error = %v, want local API path rejection", err)
	}
}

func modelIDs(items []struct {
	ID string `json:"id"`
}) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

type chatRelayMockProvider struct {
	content         string
	err             error
	calls           int
	lastUserContent string
}

func (p *chatRelayMockProvider) Generate(_ context.Context, msgs []core.LlmMessage) (*llm.Response, error) {
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == core.RoleUser {
			p.lastUserContent = msgs[i].Content
			break
		}
	}
	return &llm.Response{Content: p.content}, nil
}

func (p *chatRelayMockProvider) GenerateWithTools(ctx context.Context, msgs []core.LlmMessage, _ []llm.Tool) (*llm.Response, error) {
	return p.Generate(ctx, msgs)
}

func (p *chatRelayMockProvider) ContextWindow() int { return 200000 }

func (p *chatRelayMockProvider) MaxTokens() int { return 4096 }
