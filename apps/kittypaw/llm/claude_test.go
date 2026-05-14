package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
)

func newClaudeTestServer(handler http.HandlerFunc) (*httptest.Server, *ClaudeProvider) {
	srv := httptest.NewServer(handler)
	p := NewClaude("test-key", "claude-3-opus-20240229", 1024,
		WithClaudeHTTPClient(srv.Client()),
		WithClaudeBaseURL(srv.URL),
	)
	return srv, p
}

func TestClaudeJSONResponse(t *testing.T) {
	body := `{
		"content": [{"type":"text","text":"Hello, world!"}],
		"usage": {"input_tokens": 10, "output_tokens": 5},
		"model": "claude-3-opus-20240229"
	}`

	srv, p := newClaudeTestServer(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Errorf("x-api-key = %q, want %q", got, "test-key")
		}
		if got := r.Header.Get("anthropic-version"); got != claudeAPIVersion {
			t.Errorf("anthropic-version = %q, want %q", got, claudeAPIVersion)
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
	if resp.Content != "Hello, world!" {
		t.Errorf("Content = %q, want %q", resp.Content, "Hello, world!")
	}
	if resp.Usage == nil {
		t.Fatal("Usage is nil")
	}
	if resp.Usage.InputTokens != 10 {
		t.Errorf("InputTokens = %d, want 10", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 5 {
		t.Errorf("OutputTokens = %d, want 5", resp.Usage.OutputTokens)
	}
	if resp.Usage.Model != "claude-3-opus-20240229" {
		t.Errorf("Model = %q, want %q", resp.Usage.Model, "claude-3-opus-20240229")
	}
}

func TestClaudeJSONResponseWithCacheMetrics(t *testing.T) {
	body := `{
		"content": [{"type":"text","text":"cached reply"}],
		"usage": {
			"input_tokens": 15,
			"output_tokens": 7,
			"cache_creation_input_tokens": 2500,
			"cache_read_input_tokens": 0
		},
		"model": "claude-sonnet-4-20250514"
	}`

	srv, p := newClaudeTestServer(func(w http.ResponseWriter, r *http.Request) {
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
	if resp.Usage.CacheCreationInputTokens != 2500 {
		t.Errorf("CacheCreationInputTokens = %d, want 2500", resp.Usage.CacheCreationInputTokens)
	}
	if resp.Usage.CacheReadInputTokens != 0 {
		t.Errorf("CacheReadInputTokens = %d, want 0", resp.Usage.CacheReadInputTokens)
	}
}

func TestClaudeJSONResponseBackwardCompat(t *testing.T) {
	// Responses without cache fields must still parse cleanly with zero values.
	body := `{
		"content": [{"type":"text","text":"ok"}],
		"usage": {"input_tokens": 10, "output_tokens": 5},
		"model": "claude-3-opus-20240229"
	}`

	srv, p := newClaudeTestServer(func(w http.ResponseWriter, r *http.Request) {
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
	if resp.Usage.CacheCreationInputTokens != 0 {
		t.Errorf("CacheCreationInputTokens = %d, want 0", resp.Usage.CacheCreationInputTokens)
	}
	if resp.Usage.CacheReadInputTokens != 0 {
		t.Errorf("CacheReadInputTokens = %d, want 0", resp.Usage.CacheReadInputTokens)
	}
}

func TestClaudeSystemMessageSplit(t *testing.T) {
	var receivedBody string
	srv, p := newClaudeTestServer(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"content":[{"text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1},"model":"claude-3-opus-20240229"}`)
	})
	defer srv.Close()

	_, err := p.Generate(context.Background(), []core.LlmMessage{
		{Role: core.RoleSystem, Content: "You are helpful."},
		{Role: core.RoleUser, Content: "Hi"},
	})
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}
	if !strings.Contains(receivedBody, `"system"`) {
		t.Error("request body missing 'system' field")
	}
	// System messages should NOT appear in the messages array
	if strings.Contains(receivedBody, `"role":"system"`) {
		t.Error("system role should not be in messages array for Claude API")
	}
	// System must be content blocks with cache_control, not a plain string.
	if !strings.Contains(receivedBody, `"cache_control"`) {
		t.Error("system blocks missing 'cache_control'")
	}
	if !strings.Contains(receivedBody, `"ephemeral"`) {
		t.Error("cache_control should be ephemeral type")
	}
	// Verify it's an array (content blocks format), not a string.
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal([]byte(receivedBody), &parsed); err != nil {
		t.Fatalf("parse request body: %v", err)
	}
	sysRaw := parsed["system"]
	if len(sysRaw) == 0 || sysRaw[0] != '[' {
		t.Errorf("system should be a JSON array (content blocks), got: %s", string(sysRaw))
	}
}

func TestClaudeRetryOn429(t *testing.T) {
	attempts := 0
	srv, p := newClaudeTestServer(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"content":[{"text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1},"model":"test"}`)
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

func TestClaudeRetryCanceledContext(t *testing.T) {
	// A canceled context during backoff must return ctx.Err() promptly,
	// not block until the full delay elapses.
	srv, p := newClaudeTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately so the first backoff sleep is interrupted.
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

func TestClaudeContextWindow(t *testing.T) {
	p := NewClaude("key", "claude-3-opus-20240229", 1024)
	if p.ContextWindow() != claudeDefaultWindow {
		t.Errorf("ContextWindow() = %d, want %d", p.ContextWindow(), claudeDefaultWindow)
	}
	if p.MaxTokens() != 1024 {
		t.Errorf("MaxTokens() = %d, want 1024", p.MaxTokens())
	}

	// Non-claude model gets fallback window
	p2 := NewClaude("key", "some-other-model", 512)
	if p2.ContextWindow() != claudeFallbackWindow {
		t.Errorf("ContextWindow() = %d, want %d", p2.ContextWindow(), claudeFallbackWindow)
	}
}

// TestClaudeContentBlocksWire captures the outbound request body and asserts
// that messages carrying ContentBlocks land on the wire as Anthropic's native
// content array (tool_use + tool_result), not as a stringified placeholder.
// This is the wire-level contract the Phase A fix relies on to stop the model
// from attributing tool output to the user.
func TestClaudeContentBlocksWire(t *testing.T) {
	type capturedRequest struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		System []struct {
			Text string `json:"text"`
		} `json:"system"`
	}

	var captured capturedRequest
	srv, p := newClaudeTestServer(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1},"model":"claude-3-opus-20240229"}`)
	})
	defer srv.Close()

	_, err := p.Generate(context.Background(), []core.LlmMessage{
		{Role: core.RoleSystem, Content: "you are a helper"},
		{Role: core.RoleAssistant, ContentBlocks: []core.ContentBlock{
			{Type: core.BlockTypeToolUse, ID: "toolu_1", Name: "search", Input: map[string]any{"q": "weather"}},
		}},
		{Role: core.RoleUser, ContentBlocks: []core.ContentBlock{
			{Type: core.BlockTypeToolResult, ToolUseID: "toolu_1", Content: "Seoul 12C cloudy"},
		}},
		{Role: core.RoleUser, Content: "summarize the result"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if len(captured.Messages) != 3 {
		t.Fatalf("expected 3 conversation messages (system hoisted), got %d", len(captured.Messages))
	}
	if len(captured.System) != 1 || captured.System[0].Text != "you are a helper" {
		t.Errorf("system not hoisted to top-level: %+v", captured.System)
	}

	// First conversation message: assistant tool_use as content array.
	first := captured.Messages[0]
	if first.Role != "assistant" {
		t.Errorf("msg[0].role = %q, want assistant", first.Role)
	}
	firstStr := string(first.Content)
	for _, want := range []string{`"type":"tool_use"`, `"id":"toolu_1"`, `"name":"search"`, `"input":{"q":"weather"}`} {
		if !strings.Contains(firstStr, want) {
			t.Errorf("msg[0].content missing %q in %s", want, firstStr)
		}
	}
	if !strings.HasPrefix(strings.TrimSpace(firstStr), "[") {
		t.Errorf("msg[0].content should be JSON array, got %s", firstStr)
	}

	// Second conversation message: user tool_result.
	second := captured.Messages[1]
	if second.Role != "user" {
		t.Errorf("msg[1].role = %q, want user", second.Role)
	}
	secondStr := string(second.Content)
	for _, want := range []string{`"type":"tool_result"`, `"tool_use_id":"toolu_1"`, `"content":"Seoul 12C cloudy"`} {
		if !strings.Contains(secondStr, want) {
			t.Errorf("msg[1].content missing %q in %s", want, secondStr)
		}
	}

	// Third conversation message: plain user instruction as string content.
	third := captured.Messages[2]
	if third.Role != "user" {
		t.Errorf("msg[2].role = %q, want user", third.Role)
	}
	if got := strings.TrimSpace(string(third.Content)); got != `"summarize the result"` {
		t.Errorf("msg[2].content should be JSON string, got %s", got)
	}

	// Critical: the raw tool_result payload must NOT appear inside any
	// string-form user message. If it does, the model has been re-fed the
	// payload as if the user typed it, which is the mis-attribution bug.
	if strings.Contains(string(third.Content), "Seoul 12C cloudy") {
		t.Error("tool_result payload leaked into a string-form user message")
	}
}

func TestClaudeToolDefinitionPreservesRegistrySchema(t *testing.T) {
	p := NewClaude("key", "claude-3-opus-20240229", 1024)
	edit := registryMethodForTest(t, "File", "edit")

	body := p.buildRequestBodyWithTools("", []core.LlmMessage{{Role: core.RoleUser, Content: "edit file"}}, []Tool{{
		Name:        "File__edit",
		Description: edit.Signature,
		InputSchema: edit.ParametersSchema,
	}})

	wireTools := body["tools"].([]map[string]any)
	schema := wireTools[0]["input_schema"].(map[string]any)
	required := schema["required"].([]string)
	for _, want := range []string{"path", "old_text", "new_text"} {
		if !testStringSliceContains(required, want) {
			t.Fatalf("Claude tool required = %#v, missing %q", required, want)
		}
	}
}

// TestClaudeBackwardCompatStringContent verifies that legacy callers passing
// only a string Content land on the wire as the original {"role":..., "content":"..."}
// shape — no structural drift for the 30+ callsites that haven't migrated.
func TestClaudeBackwardCompatStringContent(t *testing.T) {
	var raw []byte
	srv, p := newClaudeTestServer(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1},"model":"claude-3-opus-20240229"}`)
	})
	defer srv.Close()

	_, err := p.Generate(context.Background(), []core.LlmMessage{
		{Role: core.RoleUser, Content: "Hi"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	got := string(raw)
	if !strings.Contains(got, `"role":"user"`) {
		t.Errorf("missing user role in: %s", got)
	}
	if !strings.Contains(got, `"content":"Hi"`) {
		t.Errorf("string content not preserved: %s", got)
	}
	if strings.Contains(got, `"content":[`) {
		t.Errorf("unexpected content-array shape for string-only message: %s", got)
	}
}
