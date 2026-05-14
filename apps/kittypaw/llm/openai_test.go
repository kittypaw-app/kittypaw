package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
)

func newOpenAITestServer(handler http.HandlerFunc) (*httptest.Server, *OpenAIProvider) {
	srv := httptest.NewServer(handler)
	p := NewOpenAI("test-key", "gpt-4", 1024,
		WithHTTPClient(srv.Client()),
		WithBaseURL(srv.URL),
	)
	return srv, p
}

func TestOpenAIJSONResponse(t *testing.T) {
	body := `{
		"choices": [{"message": {"content": "Hello!"}}],
		"usage": {"prompt_tokens": 12, "completion_tokens": 3},
		"model": "gpt-4"
	}`

	srv, p := newOpenAITestServer(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-key" {
			t.Errorf("Authorization = %q, want %q", auth, "Bearer test-key")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	})
	defer srv.Close()

	resp, err := p.Generate(context.Background(), []core.LlmMessage{
		{Role: core.RoleUser, Content: "Hi"},
	})
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}
	if resp.Content != "Hello!" {
		t.Errorf("Content = %q, want %q", resp.Content, "Hello!")
	}
	if resp.Usage == nil {
		t.Fatal("Usage is nil")
	}
	if resp.Usage.InputTokens != 12 {
		t.Errorf("InputTokens = %d, want 12", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 3 {
		t.Errorf("OutputTokens = %d, want 3", resp.Usage.OutputTokens)
	}
}

func TestOpenAIBuildRequestBodyShape(t *testing.T) {
	// After Phase 13.3 the wire is plain non-streaming JSON. Pin
	// that — no stream/stream_options keys leak into the body.
	p := NewOpenAI("key", "gpt-4", 1024, WithBaseURL("http://example.com/v1/chat/completions"))
	body := p.buildRequestBody([]core.LlmMessage{{Role: core.RoleUser, Content: "Hi"}})
	if _, ok := body["stream"]; ok {
		t.Error("stream key must not appear in non-streaming request")
	}
	if _, ok := body["stream_options"]; ok {
		t.Error("stream_options must not appear in non-streaming request")
	}
	if body["model"] != "gpt-4" {
		t.Errorf("model = %v, want gpt-4", body["model"])
	}
	if body["max_tokens"] != 1024 {
		t.Errorf("max_tokens = %v, want 1024", body["max_tokens"])
	}
	// AC-10: tools key must not leak when caller did not pass any tools.
	if _, ok := body["tools"]; ok {
		t.Error("tools key must not appear when caller did not pass any tools")
	}
}

func TestOpenAIResponsesRequestBodyShape(t *testing.T) {
	p := NewOpenAI("key", "gpt-5.5", 1024)
	body := p.buildRequestBody([]core.LlmMessage{
		{Role: core.RoleSystem, Content: "You are concise."},
		{Role: core.RoleUser, Content: "Hi"},
	})
	if _, ok := body["messages"]; ok {
		t.Error("messages key must not appear in Responses API request")
	}
	if body["model"] != "gpt-5.5" {
		t.Errorf("model = %v, want gpt-5.5", body["model"])
	}
	if body["max_output_tokens"] != 1024 {
		t.Errorf("max_output_tokens = %v, want 1024", body["max_output_tokens"])
	}
	if body["instructions"] != "You are concise." {
		t.Errorf("instructions = %v, want system text", body["instructions"])
	}
	input, ok := body["input"].([]openAIResponsesInput)
	if !ok {
		t.Fatalf("input = %T, want []openAIResponsesInput", body["input"])
	}
	if len(input) != 1 || input[0].Role != "user" {
		t.Fatalf("input = %+v, want user role only", input)
	}
}

func TestOpenAIResponsesJSONResponse(t *testing.T) {
	body := `{
		"output": [{
			"type": "message",
			"role": "assistant",
			"content": [{"type": "output_text", "text": "Hello from Responses!"}]
		}],
		"usage": {"input_tokens": 12, "output_tokens": 3},
		"model": "gpt-5.5"
	}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("path = %q, want /v1/responses", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-key" {
			t.Errorf("Authorization = %q, want %q", auth, "Bearer test-key")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	p := NewOpenAI("test-key", "gpt-5.5", 1024,
		WithHTTPClient(srv.Client()),
		WithResponsesBaseURL(srv.URL+"/v1/responses"),
	)

	resp, err := p.Generate(context.Background(), []core.LlmMessage{
		{Role: core.RoleUser, Content: "Hi"},
	})
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}
	if resp.Content != "Hello from Responses!" {
		t.Errorf("Content = %q, want %q", resp.Content, "Hello from Responses!")
	}
	if resp.Usage == nil {
		t.Fatal("Usage is nil")
	}
	if resp.Usage.InputTokens != 12 {
		t.Errorf("InputTokens = %d, want 12", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 3 {
		t.Errorf("OutputTokens = %d, want 3", resp.Usage.OutputTokens)
	}
}

func TestOpenAIRetryOn429(t *testing.T) {
	attempts := 0
	srv, p := newOpenAITestServer(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1},"model":"gpt-4"}`)
	})
	defer srv.Close()

	resp, err := p.Generate(context.Background(), []core.LlmMessage{
		{Role: core.RoleUser, Content: "Hi"},
	})
	if err != nil {
		t.Fatalf("Generate() after retries error: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("Content = %q, want %q", resp.Content, "ok")
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
}

func TestOpenAIRetryOn503(t *testing.T) {
	attempts := 0
	srv, p := newOpenAITestServer(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts <= 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1},"model":"gpt-4"}`)
	})
	defer srv.Close()

	resp, err := p.Generate(context.Background(), []core.LlmMessage{
		{Role: core.RoleUser, Content: "Hi"},
	})
	if err != nil {
		t.Fatalf("Generate() after 503 retry error: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("Content = %q, want %q", resp.Content, "ok")
	}
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2", attempts)
	}
}

func TestOpenAIRetryExhausted(t *testing.T) {
	attempts := 0
	srv, p := newOpenAITestServer(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusTooManyRequests)
	})
	defer srv.Close()

	_, err := p.Generate(context.Background(), []core.LlmMessage{
		{Role: core.RoleUser, Content: "Hi"},
	})
	if err == nil {
		t.Fatal("expected error after retry exhaustion")
	}
	if !strings.Contains(err.Error(), "retries exhausted") {
		t.Errorf("err = %q, want to contain 'retries exhausted'", err.Error())
	}
	// 1 initial + 3 retries = 4 attempts
	if attempts != 4 {
		t.Errorf("attempts = %d, want 4", attempts)
	}
}

func TestOpenAIRetryCancelledContext(t *testing.T) {
	srv, p := newOpenAITestServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := p.Generate(ctx, []core.LlmMessage{
		{Role: core.RoleUser, Content: "Hi"},
	})
	if err == nil {
		t.Fatal("expected error from canceled context")
	}
	if err != context.Canceled {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestOpenAINoAuthHeaderWhenKeyEmpty(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}],"model":"test"}`)
	}))
	defer srv.Close()

	p := NewOpenAI("", "llama3", 1024,
		WithHTTPClient(srv.Client()),
		WithBaseURL(srv.URL),
	)

	_, err := p.Generate(context.Background(), []core.LlmMessage{
		{Role: core.RoleUser, Content: "Hi"},
	})
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}
	if gotAuth != "" {
		t.Errorf("Authorization header should be empty for no API key, got %q", gotAuth)
	}
}

// --- T1: Tool definition serialization (AC-1, AC-10) ---

func TestOpenAIToolDefinitionWireShape(t *testing.T) {
	p := NewOpenAI("key", "qwen3-32b", 1024,
		WithBaseURL("http://example.com/v1/chat/completions"))

	tools := []Tool{{
		Name:        "search",
		Description: "Web search",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
			},
			"required": []string{"query"},
		},
	}}

	body := p.buildChatRequestBodyWithTools(
		[]core.LlmMessage{{Role: core.RoleUser, Content: "search for cats"}},
		tools,
	)

	wireTools, ok := body["tools"].([]map[string]any)
	if !ok {
		t.Fatalf("body[tools] = %T, want []map[string]any", body["tools"])
	}
	if len(wireTools) != 1 {
		t.Fatalf("len(tools) = %d, want 1", len(wireTools))
	}
	if wireTools[0]["type"] != "function" {
		t.Errorf("type = %v, want function", wireTools[0]["type"])
	}
	fn, ok := wireTools[0]["function"].(map[string]any)
	if !ok {
		t.Fatalf("function = %T, want map[string]any", wireTools[0]["function"])
	}
	if fn["name"] != "search" {
		t.Errorf("name = %v, want search", fn["name"])
	}
	if fn["description"] != "Web search" {
		t.Errorf("description = %v, want \"Web search\"", fn["description"])
	}
	if fn["parameters"] == nil {
		t.Error("parameters must not be nil")
	}
}

func TestOpenAIToolDefinitionPreservesRegistrySchema(t *testing.T) {
	p := NewOpenAI("key", "qwen3-32b", 1024,
		WithBaseURL("http://example.com/v1/chat/completions"))
	edit := registryMethodForTest(t, "File", "edit")

	body := p.buildChatRequestBodyWithTools(
		[]core.LlmMessage{{Role: core.RoleUser, Content: "edit file"}},
		[]Tool{{
			Name:        "File__edit",
			Description: edit.Signature,
			InputSchema: edit.ParametersSchema,
		}},
	)

	wireTools := body["tools"].([]map[string]any)
	fn := wireTools[0]["function"].(map[string]any)
	params := fn["parameters"].(map[string]any)
	required := params["required"].([]string)
	for _, want := range []string{"path", "old_text", "new_text"} {
		if !testStringSliceContains(required, want) {
			t.Fatalf("OpenAI tool required = %#v, missing %q", required, want)
		}
	}
}

func TestOpenAIToolDefinitionNilSchema(t *testing.T) {
	p := NewOpenAI("key", "x", 1024,
		WithBaseURL("http://example.com/v1/chat/completions"))

	tools := []Tool{{Name: "ping", Description: "Health check", InputSchema: nil}}
	body := p.buildChatRequestBodyWithTools(
		[]core.LlmMessage{{Role: core.RoleUser, Content: "hi"}},
		tools,
	)

	wireTools := body["tools"].([]map[string]any)
	fn := wireTools[0]["function"].(map[string]any)
	params, ok := fn["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("parameters = %T, want map[string]any (nil → empty object schema)", fn["parameters"])
	}
	if params["type"] != "object" {
		t.Errorf("parameters.type = %v, want object", params["type"])
	}
	if _, ok := params["properties"]; !ok {
		t.Error("parameters.properties must be present (empty object)")
	}
}

func TestOpenAIToolsEmptyOmitted(t *testing.T) {
	p := NewOpenAI("key", "x", 1024,
		WithBaseURL("http://example.com/v1/chat/completions"))

	body := p.buildChatRequestBodyWithTools(
		[]core.LlmMessage{{Role: core.RoleUser, Content: "hi"}},
		nil,
	)
	if _, ok := body["tools"]; ok {
		t.Error("nil tools must not emit a tools key")
	}

	body = p.buildChatRequestBodyWithTools(
		[]core.LlmMessage{{Role: core.RoleUser, Content: "hi"}},
		[]Tool{},
	)
	if _, ok := body["tools"]; ok {
		t.Error("empty tools slice must not emit a tools key")
	}
}

// --- T2a: assistant message conversion (AC-2) ---

// chatMessagesFromBody marshals through JSON to assert the wire shape, since
// the in-memory representation may use typed structs we don't want tests to
// depend on. Returns []map[string]any decoded from body["messages"].
func chatMessagesFromBody(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	raw, err := json.Marshal(body["messages"])
	if err != nil {
		t.Fatalf("marshal messages: %v", err)
	}
	var out []map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal messages: %v", err)
	}
	return out
}

func TestOpenAIChatMessages_AssistantToolUseOnly(t *testing.T) {
	p := NewOpenAI("key", "x", 1024,
		WithBaseURL("http://example.com/v1/chat/completions"))

	body := p.buildChatRequestBodyWithTools([]core.LlmMessage{
		{Role: core.RoleUser, Content: "search cats"},
		{Role: core.RoleAssistant, ContentBlocks: []core.ContentBlock{
			{Type: core.BlockTypeToolUse, ID: "call_1", Name: "search",
				Input: map[string]any{"q": "cats"}},
		}},
	}, nil)

	msgs := chatMessagesFromBody(t, body)
	if len(msgs) != 2 {
		t.Fatalf("len(messages) = %d, want 2", len(msgs))
	}
	asst := msgs[1]
	if asst["role"] != "assistant" {
		t.Errorf("role = %v, want assistant", asst["role"])
	}
	calls, ok := asst["tool_calls"].([]any)
	if !ok || len(calls) != 1 {
		t.Fatalf("tool_calls = %#v, want 1 entry", asst["tool_calls"])
	}
	call := calls[0].(map[string]any)
	if call["id"] != "call_1" || call["type"] != "function" {
		t.Errorf("call meta = %v", call)
	}
	fn := call["function"].(map[string]any)
	if fn["name"] != "search" {
		t.Errorf("function.name = %v, want search", fn["name"])
	}
	if args, ok := fn["arguments"].(string); !ok || !strings.Contains(args, "cats") {
		t.Errorf("arguments = %v, want JSON string containing cats", fn["arguments"])
	}
}

func TestOpenAIChatMessages_AssistantMixedTextAndToolUse(t *testing.T) {
	p := NewOpenAI("key", "x", 1024,
		WithBaseURL("http://example.com/v1/chat/completions"))

	body := p.buildChatRequestBodyWithTools([]core.LlmMessage{
		{Role: core.RoleAssistant, ContentBlocks: []core.ContentBlock{
			{Type: core.BlockTypeText, Text: "Let me look that up."},
			{Type: core.BlockTypeToolUse, ID: "call_2", Name: "search",
				Input: map[string]any{"q": "x"}},
		}},
	}, nil)

	msgs := chatMessagesFromBody(t, body)
	asst := msgs[0]
	if got := asst["content"]; got != "Let me look that up." {
		t.Errorf("content = %v, want preserved text", got)
	}
	calls := asst["tool_calls"].([]any)
	if len(calls) != 1 {
		t.Fatalf("len(tool_calls) = %d, want 1", len(calls))
	}
}

func TestOpenAIChatMessages_ParallelToolUse(t *testing.T) {
	p := NewOpenAI("key", "x", 1024,
		WithBaseURL("http://example.com/v1/chat/completions"))

	body := p.buildChatRequestBodyWithTools([]core.LlmMessage{
		{Role: core.RoleAssistant, ContentBlocks: []core.ContentBlock{
			{Type: core.BlockTypeToolUse, ID: "a", Name: "f1", Input: map[string]any{"k": 1}},
			{Type: core.BlockTypeToolUse, ID: "b", Name: "f2", Input: map[string]any{"k": 2}},
		}},
	}, nil)

	msgs := chatMessagesFromBody(t, body)
	calls := msgs[0]["tool_calls"].([]any)
	if len(calls) != 2 {
		t.Fatalf("len(tool_calls) = %d, want 2", len(calls))
	}
	if calls[0].(map[string]any)["id"] != "a" || calls[1].(map[string]any)["id"] != "b" {
		t.Errorf("parallel tool_call order not preserved")
	}
}

func TestOpenAIChatMessages_MarshalErrorPanics(t *testing.T) {
	// json.Marshal can only fail on unsupported types (chan/func) or cyclic
	// refs — never on validly-decoded LLM payloads. A Marshal error means
	// the caller put bad data into ContentBlock.Input. Fail loud rather
	// than silently rewriting Arguments to "{}" (which would let the model
	// see Arguments different from what it emitted last turn).
	p := NewOpenAI("key", "x", 1024,
		WithBaseURL("http://example.com/v1/chat/completions"))

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for unmarshalable Input")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "tool_use Input marshal failed") {
			t.Errorf("panic message = %v, want to contain 'tool_use Input marshal failed'", r)
		}
	}()

	p.buildChatRequestBodyWithTools([]core.LlmMessage{
		{Role: core.RoleAssistant, ContentBlocks: []core.ContentBlock{
			{Type: core.BlockTypeToolUse, ID: "x", Name: "f",
				Input: map[string]any{"bad": make(chan int)}},
		}},
	}, nil)
}

func TestOpenAIChatMessages_NilArgumentsBecomeEmptyJSONObject(t *testing.T) {
	p := NewOpenAI("key", "x", 1024,
		WithBaseURL("http://example.com/v1/chat/completions"))

	body := p.buildChatRequestBodyWithTools([]core.LlmMessage{
		{Role: core.RoleAssistant, ContentBlocks: []core.ContentBlock{
			{Type: core.BlockTypeToolUse, ID: "z", Name: "ping", Input: nil},
		}},
	}, nil)

	msgs := chatMessagesFromBody(t, body)
	calls := msgs[0]["tool_calls"].([]any)
	args := calls[0].(map[string]any)["function"].(map[string]any)["arguments"]
	if args != "{}" {
		t.Errorf("arguments = %q, want \"{}\" for nil input", args)
	}
}

// --- T2b: user message conversion (AC-2, AC-12) ---

func TestOpenAIChatMessages_UserToolResultSingle(t *testing.T) {
	p := NewOpenAI("key", "x", 1024,
		WithBaseURL("http://example.com/v1/chat/completions"))

	body := p.buildChatRequestBodyWithTools([]core.LlmMessage{
		{Role: core.RoleUser, ContentBlocks: []core.ContentBlock{
			{Type: core.BlockTypeToolResult, ToolUseID: "call_1", Content: "found 42"},
		}},
	}, nil)

	msgs := chatMessagesFromBody(t, body)
	if len(msgs) != 1 {
		t.Fatalf("len(messages) = %d, want 1", len(msgs))
	}
	m := msgs[0]
	if m["role"] != "tool" {
		t.Errorf("role = %v, want tool", m["role"])
	}
	if m["tool_call_id"] != "call_1" {
		t.Errorf("tool_call_id = %v, want call_1", m["tool_call_id"])
	}
	if m["content"] != "found 42" {
		t.Errorf("content = %v, want \"found 42\"", m["content"])
	}
}

func TestOpenAIChatMessages_MultipleToolResultsOrderPreserved(t *testing.T) {
	p := NewOpenAI("key", "x", 1024,
		WithBaseURL("http://example.com/v1/chat/completions"))

	body := p.buildChatRequestBodyWithTools([]core.LlmMessage{
		{Role: core.RoleUser, ContentBlocks: []core.ContentBlock{
			{Type: core.BlockTypeToolResult, ToolUseID: "a", Content: "A"},
			{Type: core.BlockTypeToolResult, ToolUseID: "b", Content: "B"},
			{Type: core.BlockTypeToolResult, ToolUseID: "c", Content: "C"},
		}},
	}, nil)

	msgs := chatMessagesFromBody(t, body)
	if len(msgs) != 3 {
		t.Fatalf("len(messages) = %d, want 3 (split per tool_result)", len(msgs))
	}
	wantIDs := []string{"a", "b", "c"}
	for i, want := range wantIDs {
		if msgs[i]["tool_call_id"] != want {
			t.Errorf("msg[%d].tool_call_id = %v, want %s", i, msgs[i]["tool_call_id"], want)
		}
	}
}

func TestOpenAIChatMessages_TextOnlyPassesThrough(t *testing.T) {
	p := NewOpenAI("key", "x", 1024,
		WithBaseURL("http://example.com/v1/chat/completions"))

	body := p.buildChatRequestBodyWithTools([]core.LlmMessage{
		{Role: core.RoleSystem, Content: "You are a helper."},
		{Role: core.RoleUser, Content: "hi"},
	}, nil)

	msgs := chatMessagesFromBody(t, body)
	if len(msgs) != 2 {
		t.Fatalf("len(messages) = %d, want 2", len(msgs))
	}
	if msgs[0]["role"] != "system" || msgs[0]["content"] != "You are a helper." {
		t.Errorf("system message not preserved: %v", msgs[0])
	}
	if msgs[1]["role"] != "user" || msgs[1]["content"] != "hi" {
		t.Errorf("user message not preserved: %v", msgs[1])
	}
}

// --- T3: response parsing (AC-3, AC-4, AC-9, AC-11) ---

func TestOpenAIParseChatToolCallsStringArguments(t *testing.T) {
	body := `{
		"choices": [{
			"message": {"role": "assistant", "content": null, "tool_calls": [
				{"id": "call_1", "type": "function",
				 "function": {"name": "search", "arguments": "{\"q\":\"cats\"}"}}
			]},
			"finish_reason": "tool_calls"
		}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 5},
		"model": "qwen3-32b"
	}`

	srv, p := newOpenAITestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	})
	defer srv.Close()

	resp, err := p.Generate(context.Background(), []core.LlmMessage{
		{Role: core.RoleUser, Content: "search cats"},
	})
	if err != nil {
		t.Fatalf("Generate(): %v", err)
	}
	if resp.StopReason != "tool_use" {
		t.Errorf("StopReason = %q, want tool_use", resp.StopReason)
	}
	if len(resp.ContentBlocks) != 1 {
		t.Fatalf("len(ContentBlocks) = %d, want 1", len(resp.ContentBlocks))
	}
	b := resp.ContentBlocks[0]
	if b.Type != core.BlockTypeToolUse || b.ID != "call_1" || b.Name != "search" {
		t.Errorf("ContentBlock = %+v", b)
	}
	if got := b.Input["q"]; got != "cats" {
		t.Errorf("Input.q = %v, want cats", got)
	}
}

func TestOpenAIParseChatToolCallsObjectArguments(t *testing.T) {
	// Ollama (qwen2.5:7b, llama3.1) sometimes emits arguments as a JSON object,
	// not a string. Provider must decode either shape into Input map.
	body := `{
		"choices": [{
			"message": {"role": "assistant", "content": null, "tool_calls": [
				{"id": "c1", "type": "function",
				 "function": {"name": "n", "arguments": {"k": "v"}}}
			]},
			"finish_reason": "tool_calls"
		}],
		"model": "qwen2.5:7b"
	}`

	srv, p := newOpenAITestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	})
	defer srv.Close()

	resp, err := p.Generate(context.Background(), []core.LlmMessage{
		{Role: core.RoleUser, Content: "x"},
	})
	if err != nil {
		t.Fatalf("Generate(): %v", err)
	}
	if len(resp.ContentBlocks) != 1 {
		t.Fatalf("len(ContentBlocks) = %d", len(resp.ContentBlocks))
	}
	if got := resp.ContentBlocks[0].Input["k"]; got != "v" {
		t.Errorf("Input.k = %v, want v (object arguments shape)", got)
	}
}

func TestOpenAIParseChatToolCallsEmptyArguments(t *testing.T) {
	body := `{
		"choices": [{
			"message": {"role": "assistant", "tool_calls": [
				{"id": "c1", "type": "function",
				 "function": {"name": "ping", "arguments": ""}}
			]},
			"finish_reason": "tool_calls"
		}],
		"model": "x"
	}`

	srv, p := newOpenAITestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	})
	defer srv.Close()

	resp, err := p.Generate(context.Background(), []core.LlmMessage{
		{Role: core.RoleUser, Content: "x"},
	})
	if err != nil {
		t.Fatalf("Generate(): %v", err)
	}
	if resp.ContentBlocks[0].Input == nil {
		t.Error("Input must not be nil for empty arguments; want empty map")
	}
	if len(resp.ContentBlocks[0].Input) != 0 {
		t.Errorf("Input = %v, want empty map", resp.ContentBlocks[0].Input)
	}
}

func TestOpenAIParseChatToolCallsInvalidArgumentsErrors(t *testing.T) {
	body := `{
		"choices": [{
			"message": {"role": "assistant", "tool_calls": [
				{"id": "c1", "type": "function",
				 "function": {"name": "ping", "arguments": "not-json{{{"}}
			]},
			"finish_reason": "tool_calls"
		}],
		"model": "x"
	}`

	srv, p := newOpenAITestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	})
	defer srv.Close()

	_, err := p.Generate(context.Background(), []core.LlmMessage{
		{Role: core.RoleUser, Content: "x"},
	})
	if err == nil {
		t.Fatal("expected error for unparseable arguments")
	}
}

func TestOpenAIParseChatToolCallEmptyIDErrors(t *testing.T) {
	// Some Ollama models drop the id field on single-tool calls. Without an
	// id the caller can't echo tool_call_id on the next turn — surface the
	// malformed response instead of letting the loop ping back with "".
	body := `{
		"choices": [{
			"message": {"role": "assistant", "tool_calls": [
				{"id": "", "type": "function",
				 "function": {"name": "ping", "arguments": "{}"}}
			]},
			"finish_reason": "tool_calls"
		}],
		"model": "x"
	}`

	srv, p := newOpenAITestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	})
	defer srv.Close()

	_, err := p.Generate(context.Background(), []core.LlmMessage{
		{Role: core.RoleUser, Content: "x"},
	})
	if err == nil {
		t.Fatal("expected error for empty tool_call id")
	}
	if !strings.Contains(err.Error(), "missing id") {
		t.Errorf("err = %q, want to contain 'missing id'", err.Error())
	}
}

func TestOpenAIParseStopReasonMapping(t *testing.T) {
	cases := []struct {
		finish string
		want   string
	}{
		{"stop", "end_turn"},
		{"tool_calls", "tool_use"},
		{"length", "max_tokens"},
		{"content_filter", "content_filter"}, // passthrough
		{"function_call", "function_call"},   // passthrough
		{"", ""},                             // passthrough (forward-compat)
	}

	for _, tc := range cases {
		t.Run(tc.finish, func(t *testing.T) {
			body := fmt.Sprintf(`{
				"choices": [{"message": {"content": "ok"}, "finish_reason": %q}],
				"model": "x"
			}`, tc.finish)

			srv, p := newOpenAITestServer(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, body)
			})
			defer srv.Close()

			resp, err := p.Generate(context.Background(), []core.LlmMessage{
				{Role: core.RoleUser, Content: "hi"},
			})
			if err != nil {
				t.Fatalf("Generate(): %v", err)
			}
			if resp.StopReason != tc.want {
				t.Errorf("StopReason = %q, want %q", resp.StopReason, tc.want)
			}
		})
	}
}

func TestOpenAIParseChatUsageNilSafe(t *testing.T) {
	// Some Ollama models omit usage entirely; provider must not panic.
	body := `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"model":"x"}`

	srv, p := newOpenAITestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	})
	defer srv.Close()

	resp, err := p.Generate(context.Background(), []core.LlmMessage{
		{Role: core.RoleUser, Content: "hi"},
	})
	if err != nil {
		t.Fatalf("Generate(): %v", err)
	}
	if resp.Usage != nil {
		t.Errorf("Usage = %+v, want nil for missing usage field", resp.Usage)
	}
}

// --- T4: end-to-end GenerateWithTools (AC-1, AC-7) ---

func TestOpenAIGenerateWithToolsRoundTrip(t *testing.T) {
	var sentBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&sentBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"choices": [{
				"message": {"role": "assistant", "content": null, "tool_calls": [
					{"id": "c1", "type": "function",
					 "function": {"name": "search", "arguments": "{\"q\":\"cats\"}"}}
				]},
				"finish_reason": "tool_calls"
			}],
			"usage": {"prompt_tokens": 4, "completion_tokens": 2},
			"model": "qwen3-32b"
		}`)
	}))
	defer srv.Close()

	p := NewOpenAI("key", "qwen3-32b", 1024,
		WithHTTPClient(srv.Client()),
		WithBaseURL(srv.URL),
	)

	tools := []Tool{{
		Name:        "search",
		Description: "Web",
		InputSchema: map[string]any{"type": "object"},
	}}

	resp, err := p.GenerateWithTools(context.Background(), []core.LlmMessage{
		{Role: core.RoleUser, Content: "search for cats"},
	}, tools)
	if err != nil {
		t.Fatalf("GenerateWithTools: %v", err)
	}

	if _, ok := sentBody["tools"]; !ok {
		t.Error("request body missing tools key")
	}
	if resp.StopReason != "tool_use" {
		t.Errorf("StopReason = %q, want tool_use", resp.StopReason)
	}
	if len(resp.ContentBlocks) != 1 || resp.ContentBlocks[0].Name != "search" {
		t.Errorf("ContentBlocks = %+v", resp.ContentBlocks)
	}
}

func TestOpenAIGenerateWithToolsNilToolsDegrades(t *testing.T) {
	// AC-5: nil/empty tools must take the existing Generate path with no
	// tools key on the wire.
	var sentBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&sentBody)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"hi"},"finish_reason":"stop"}],"model":"x"}`)
	}))
	defer srv.Close()

	p := NewOpenAI("key", "x", 1024,
		WithHTTPClient(srv.Client()),
		WithBaseURL(srv.URL),
	)

	_, err := p.GenerateWithTools(context.Background(), []core.LlmMessage{
		{Role: core.RoleUser, Content: "hi"},
	}, nil)
	if err != nil {
		t.Fatalf("GenerateWithTools(nil tools): %v", err)
	}
	if _, ok := sentBody["tools"]; ok {
		t.Error("nil tools must not put a tools key on the wire")
	}
}

func TestOpenAIGenerateWithToolsParallelResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"choices": [{
				"message": {"role": "assistant", "tool_calls": [
					{"id": "a", "type": "function", "function": {"name": "f1", "arguments": "{}"}},
					{"id": "b", "type": "function", "function": {"name": "f2", "arguments": "{}"}}
				]},
				"finish_reason": "tool_calls"
			}],
			"model": "x"
		}`)
	}))
	defer srv.Close()

	p := NewOpenAI("key", "x", 1024,
		WithHTTPClient(srv.Client()),
		WithBaseURL(srv.URL),
	)

	tools := []Tool{{Name: "f1"}, {Name: "f2"}}
	resp, err := p.GenerateWithTools(context.Background(), []core.LlmMessage{
		{Role: core.RoleUser, Content: "x"},
	}, tools)
	if err != nil {
		t.Fatalf("GenerateWithTools: %v", err)
	}
	if len(resp.ContentBlocks) != 2 {
		t.Fatalf("len(ContentBlocks) = %d, want 2 parallel tool_use blocks", len(resp.ContentBlocks))
	}
	if resp.ContentBlocks[0].ID != "a" || resp.ContentBlocks[1].ID != "b" {
		t.Errorf("parallel order broken: %v %v", resp.ContentBlocks[0].ID, resp.ContentBlocks[1].ID)
	}
}

// --- extractContent: list-of-blocks unwrap (Mistral magistral, future
// native-reasoning models). String shape is the OpenAI standard and stays
// unchanged. See plan v3 § Out of Scope: reasoning string is recovered but
// not surfaced to caller — held for future Response struct extension.

func TestExtractContent_String(t *testing.T) {
	text, reason, err := extractContent("hello")
	if err != nil || text != "hello" || reason != "" {
		t.Errorf("got (%q, %q, %v); want (\"hello\", \"\", nil)", text, reason, err)
	}
}

func TestExtractContent_NullContent(t *testing.T) {
	text, reason, err := extractContent(nil)
	if err != nil || text != "" || reason != "" {
		t.Errorf("got (%q, %q, %v); want (\"\", \"\", nil)", text, reason, err)
	}
}

func TestExtractContent_EmptyArray(t *testing.T) {
	text, reason, err := extractContent([]any{})
	if err != nil || text != "" || reason != "" {
		t.Errorf("got (%q, %q, %v); want (\"\", \"\", nil)", text, reason, err)
	}
}

func TestExtractContent_ListOfBlocks_TextOnly(t *testing.T) {
	text, reason, err := extractContent([]any{
		map[string]any{"type": "text", "text": "hi"},
	})
	if err != nil || text != "hi" || reason != "" {
		t.Errorf("got (%q, %q, %v); want (\"hi\", \"\", nil)", text, reason, err)
	}
}

func TestExtractContent_ListOfBlocks_EmptyText(t *testing.T) {
	text, reason, err := extractContent([]any{
		map[string]any{"type": "text", "text": ""},
	})
	if err != nil || text != "" || reason != "" {
		t.Errorf("got (%q, %q, %v); want (\"\", \"\", nil)", text, reason, err)
	}
}

func TestExtractContent_ListOfBlocks_TextFieldMissing(t *testing.T) {
	text, reason, err := extractContent([]any{
		map[string]any{"type": "text"},
	})
	if err != nil || text != "" || reason != "" {
		t.Errorf("got (%q, %q, %v); want (\"\", \"\", nil)", text, reason, err)
	}
}

func TestExtractContent_ListOfBlocks_ThinkingPlusText(t *testing.T) {
	text, reason, err := extractContent([]any{
		map[string]any{
			"type": "thinking",
			"thinking": []any{
				map[string]any{"type": "text", "text": "deliberation"},
			},
		},
		map[string]any{"type": "text", "text": "final"},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if text != "final" {
		t.Errorf("text = %q, want \"final\"", text)
	}
	if reason != "deliberation" {
		t.Errorf("reasoning = %q, want \"deliberation\"", reason)
	}
}

func TestExtractContent_ListOfBlocks_MultipleText(t *testing.T) {
	text, reason, err := extractContent([]any{
		map[string]any{"type": "text", "text": "hi"},
		map[string]any{"type": "text", "text": " world"},
	})
	if err != nil || text != "hi world" || reason != "" {
		t.Errorf("got (%q, %q, %v); want (\"hi world\", \"\", nil)", text, reason, err)
	}
}

// TestExtractContent_MagistralFixture pins the exact response shape Mistral
// `magistral-medium-latest` emits today (2026-05-05 measurement). Includes
// the `closed: true` field, nested `thinking[]` array of inner text blocks,
// and a final `{type:"text", text:"..."}` block.
func TestExtractContent_MagistralFixture(t *testing.T) {
	raw := `[
		{
			"type": "thinking",
			"thinking": [
				{"type": "text", "text": "Okay, the user wants me to introduce myself in one line."}
			],
			"closed": true
		},
		{"type": "text", "text": "안녕! 나는를 도울 수 있는 인공지능 언어 모델이야. 😊"}
	]`
	var c any
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	text, reason, err := extractContent(c)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	want := "안녕! 나는를 도울 수 있는 인공지능 언어 모델이야. 😊"
	if text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
	if !strings.Contains(reason, "Okay, the user wants") {
		t.Errorf("reasoning lost magistral thinking text; got %q", reason)
	}
}

func TestExtractContent_UnknownType_EmptyFallback(t *testing.T) {
	// Unknown block type → silently skipped (slog.Warn). No JSON re-serialize
	// dump in the assistant message — that was rejected as too noisy. Caller
	// sees clean empty text rather than unexplained JSON garbage.
	text, reason, err := extractContent([]any{
		map[string]any{"type": "foo", "bar": "baz"},
	})
	if err != nil || text != "" || reason != "" {
		t.Errorf("got (%q, %q, %v); want (\"\", \"\", nil)", text, reason, err)
	}
}

func TestExtractContent_NotStringNotArray(t *testing.T) {
	// Numbers/objects are wire violations from the provider's side. Surface
	// loudly rather than coerce — the runner loop has no graceful policy for
	// "the LLM returned 42 as content".
	cases := []any{
		42,
		map[string]any{"foo": "bar"},
		true,
	}
	for _, c := range cases {
		_, _, err := extractContent(c)
		if err == nil {
			t.Errorf("extractContent(%v) err=nil, want non-nil", c)
		}
	}
}

// TestOpenAIProvider_WithReasoningFormat: sending the option injects
// `reasoning_format` into the chat completions body. Without it the
// field must NOT appear (Groq is the only provider that accepts it).
func TestOpenAIProvider_WithReasoningFormat(t *testing.T) {
	p := NewOpenAI("k", "qwen/qwen3-32b", 1024,
		WithBaseURL("http://example.com/v1/chat/completions"),
		WithReasoningFormat("parsed"))
	body := p.buildRequestBody([]core.LlmMessage{{Role: core.RoleUser, Content: "hi"}})
	if got := body["reasoning_format"]; got != "parsed" {
		t.Errorf("reasoning_format = %v, want \"parsed\"", got)
	}
}

func TestOpenAIProvider_WithoutReasoningFormat_OmittedFromBody(t *testing.T) {
	p := NewOpenAI("k", "gpt-4", 1024,
		WithBaseURL("http://example.com/v1/chat/completions"))
	body := p.buildRequestBody([]core.LlmMessage{{Role: core.RoleUser, Content: "hi"}})
	if _, ok := body["reasoning_format"]; ok {
		t.Errorf("reasoning_format must not appear when option not set")
	}
}

// TestParseChatJSONResponse_ListOfBlocks pins parseChatJSONResponse's
// integration with extractContent for the magistral wire shape — the
// caller path that flows through Generate(). Companion to the existing
// TestOpenAIJSONResponse string-shape coverage.
func TestParseChatJSONResponse_ListOfBlocks(t *testing.T) {
	body := `{
		"choices": [{"message": {"content": [
			{"type": "thinking", "thinking": [{"type": "text", "text": "deliberation"}], "closed": true},
			{"type": "text", "text": "final answer"}
		]}}],
		"usage": {"prompt_tokens": 5, "completion_tokens": 9},
		"model": "magistral-medium-latest"
	}`
	srv, p := newOpenAITestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	})
	defer srv.Close()

	resp, err := p.Generate(context.Background(), []core.LlmMessage{
		{Role: core.RoleUser, Content: "hi"},
	})
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}
	if resp.Content != "final answer" {
		t.Errorf("Content = %q, want \"final answer\"", resp.Content)
	}
}

func registryMethodForTest(t *testing.T, skillName, methodName string) core.SkillMethodMeta {
	t.Helper()
	for _, skill := range core.SkillRegistry {
		if skill.Name != skillName {
			continue
		}
		for _, method := range skill.Methods {
			if method.Name == methodName {
				return method
			}
		}
	}
	t.Fatalf("%s.%s registry method not found", skillName, methodName)
	return core.SkillMethodMeta{}
}

func testStringSliceContains(values []string, want string) bool {
	for _, got := range values {
		if got == want {
			return true
		}
	}
	return false
}
