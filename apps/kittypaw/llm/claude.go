package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jinto/kittypaw/core"
)

const (
	claudeBaseURL        = "https://api.anthropic.com/v1/messages"
	claudeAPIVersion     = "2023-06-01"
	claudeDefaultWindow  = 200_000
	claudeFallbackWindow = 8192
	claudeMaxRetries     = 3
	claudeBaseDelay      = 1 * time.Second
)

// ClaudeProvider implements Provider for the Anthropic Messages API.
type ClaudeProvider struct {
	apiKey        string
	model         string
	maxTokens     int
	contextWindow int
	baseURL       string
	client        *http.Client
}

// ClaudeOption is a functional option for NewClaude.
type ClaudeOption func(*ClaudeProvider)

// WithClaudeHTTPClient overrides the default HTTP client.
func WithClaudeHTTPClient(c *http.Client) ClaudeOption {
	return func(p *ClaudeProvider) {
		p.client = c
	}
}

// WithClaudeBaseURL overrides the default Anthropic API endpoint.
func WithClaudeBaseURL(url string) ClaudeOption {
	return func(p *ClaudeProvider) {
		p.baseURL = url
	}
}

// NewClaude creates a ClaudeProvider for the given model.
func NewClaude(apiKey, model string, maxTokens int, opts ...ClaudeOption) *ClaudeProvider {
	window := claudeFallbackWindow
	if isLargeContextModel(model) {
		window = claudeDefaultWindow
	}
	p := &ClaudeProvider{
		apiKey:        apiKey,
		model:         model,
		maxTokens:     maxTokens,
		contextWindow: window,
		baseURL:       claudeBaseURL,
		client:        &http.Client{Timeout: 5 * time.Minute},
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// ContextWindow returns the model's context window size in tokens.
func (c *ClaudeProvider) ContextWindow() int { return c.contextWindow }

// MaxTokens returns the maximum output tokens.
func (c *ClaudeProvider) MaxTokens() int { return c.maxTokens }

// Generate sends messages and returns a complete response. Wire is
// plain JSON (`stream: false`) — see Provider docs for why streaming
// was removed in Phase 13.3.
func (c *ClaudeProvider) Generate(ctx context.Context, messages []core.LlmMessage) (*Response, error) {
	system, msgs := splitSystemMessages(messages)
	body := c.buildRequestBody(system, msgs)
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("claude: marshal request: %w", err)
	}
	resp, err := c.doWithRetry(ctx, payload)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return c.parseJSONResponse(resp.Body)
}

// GenerateWithTools sends messages along with a tool definition list.
// When tools is non-empty the response carries ContentBlocks (text +
// tool_use) and StopReason so a caller can drive a tool-use loop. Falls
// back to plain Generate semantics when tools is nil/empty.
func (c *ClaudeProvider) GenerateWithTools(ctx context.Context, messages []core.LlmMessage, tools []Tool) (*Response, error) {
	if len(tools) == 0 {
		return c.Generate(ctx, messages)
	}
	system, msgs := splitSystemMessages(messages)
	body := c.buildRequestBodyWithTools(system, msgs, tools)
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("claude: marshal request: %w", err)
	}
	resp, err := c.doWithRetry(ctx, payload)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return c.parseJSONResponse(resp.Body)
}

// splitSystemMessages separates system messages from the conversation.
// The Anthropic API expects system content as a top-level field, not in the
// messages array.
func splitSystemMessages(messages []core.LlmMessage) (string, []core.LlmMessage) {
	var systemParts []string
	var conversation []core.LlmMessage

	for _, m := range messages {
		if m.Role == core.RoleSystem {
			systemParts = append(systemParts, systemTextFrom(m))
		} else {
			conversation = append(conversation, m)
		}
	}
	return strings.Join(systemParts, "\n\n"), conversation
}

// systemTextFrom flattens a system message into a plain string. Callers that
// used the new ContentBlocks shape for a system message (text blocks only) get
// their text concatenated — anything else is dropped, since system role does
// not accept tool_use / tool_result on the wire.
func systemTextFrom(m core.LlmMessage) string {
	if m.Content != "" {
		return m.Content
	}
	var parts []string
	for _, b := range m.ContentBlocks {
		if b.Type == core.BlockTypeText && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// claudeMessage is the wire format for a single message in the API request.
//
// Content is typed `any` because Anthropic accepts either a string or a
// content-block array. buildRequestBody picks the shape per LlmMessage.
type claudeMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

func (c *ClaudeProvider) buildRequestBody(system string, msgs []core.LlmMessage) map[string]any {
	return c.buildRequestBodyWithTools(system, msgs, nil)
}

// buildRequestBodyWithTools is the same as buildRequestBody but emits
// the Anthropic-required `tools` field when tools is non-empty. The
// loop in mediateSkillOutputWithTools relies on the model returning
// stop_reason="tool_use" when it picks a tool — that only happens
// when `tools` is on the wire.
func (c *ClaudeProvider) buildRequestBodyWithTools(system string, msgs []core.LlmMessage, tools []Tool) map[string]any {
	apiMsgs := make([]claudeMessage, len(msgs))
	for i, m := range msgs {
		// ContentBlocks wins when present so callers that set both (e.g. a
		// stale Content="" placeholder) still get the structured shape on the
		// wire. This is the only path for tool_use / tool_result blocks.
		if len(m.ContentBlocks) > 0 {
			apiMsgs[i] = claudeMessage{Role: string(m.Role), Content: m.ContentBlocks}
		} else {
			apiMsgs[i] = claudeMessage{Role: string(m.Role), Content: m.Content}
		}
	}

	body := map[string]any{
		"model":      c.model,
		"max_tokens": c.maxTokens,
		"messages":   apiMsgs,
	}
	if system != "" {
		body["system"] = []map[string]any{{
			"type":          "text",
			"text":          system,
			"cache_control": map[string]string{"type": "ephemeral"},
		}}
	}
	if len(tools) > 0 {
		wireTools := make([]map[string]any, 0, len(tools))
		for _, t := range tools {
			schema := t.InputSchema
			if schema == nil {
				schema = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			wireTools = append(wireTools, map[string]any{
				"name":         t.Name,
				"description":  t.Description,
				"input_schema": schema,
			})
		}
		body["tools"] = wireTools
	}
	return body
}

func (c *ClaudeProvider) newRequest(ctx context.Context, payload []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", claudeAPIVersion)
	return req, nil
}

// doWithRetry executes the HTTP request with exponential backoff on
// 429 (rate limit) and 529 (overloaded) responses.
func (c *ClaudeProvider) doWithRetry(ctx context.Context, payload []byte) (*http.Response, error) {
	var lastErr error
	retryAfter := ""

	for attempt := 0; attempt <= claudeMaxRetries; attempt++ {
		if attempt > 0 {
			delay := providerRetryDelay(claudeBaseDelay, attempt, retryAfter, time.Now())
			retryAfter = ""
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		req, err := c.newRequest(ctx, payload)
		if err != nil {
			return nil, fmt.Errorf("claude: build request: %w", err)
		}

		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("claude: http request: %w", err)
			retryAfter = ""
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == 529 {
			retryAfter = resp.Header.Get("Retry-After")
			resp.Body.Close()
			lastErr = fmt.Errorf("claude: server returned %d", resp.StatusCode)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("claude: API error %d: %s", resp.StatusCode, string(body))
		}

		return resp, nil
	}

	return nil, fmt.Errorf("claude: retries exhausted: %w", lastErr)
}

// --- JSON (non-streaming) response parsing ---

// claudeResponse mirrors the shapes Anthropic returns for a single
// completion. Content is heterogeneous — text blocks carry .text,
// tool_use blocks carry id/name/input. We decode into a flat struct
// covering both so the parser can route per Type without a second
// round of JSON.
type claudeResponse struct {
	Content []struct {
		Type  string         `json:"type"`
		Text  string         `json:"text,omitempty"`
		ID    string         `json:"id,omitempty"`
		Name  string         `json:"name,omitempty"`
		Input map[string]any `json:"input,omitempty"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens              int64 `json:"input_tokens"`
		OutputTokens             int64 `json:"output_tokens"`
		CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	} `json:"usage"`
	Model string `json:"model"`
}

func (c *ClaudeProvider) parseJSONResponse(r io.Reader) (*Response, error) {
	var resp claudeResponse
	// Cap response body. Anthropic's normal output stays well under
	// max_tokens × ~4 bytes, but a misconfigured proxy or malicious
	// upstream could otherwise pump arbitrary bytes into json.Decode
	// and exhaust memory.
	if err := json.NewDecoder(io.LimitReader(r, llmMaxResponseBytes)).Decode(&resp); err != nil {
		return nil, fmt.Errorf("claude: decode response: %w", err)
	}

	var content strings.Builder
	blocks := make([]core.ContentBlock, 0, len(resp.Content))
	for _, b := range resp.Content {
		switch b.Type {
		case core.BlockTypeText, "":
			// "" handles Anthropic responses that elide type for text-only.
			content.WriteString(b.Text)
			blocks = append(blocks, core.ContentBlock{
				Type: core.BlockTypeText,
				Text: b.Text,
			})
		case core.BlockTypeToolUse:
			blocks = append(blocks, core.ContentBlock{
				Type:  core.BlockTypeToolUse,
				ID:    b.ID,
				Name:  b.Name,
				Input: b.Input,
			})
		}
	}

	return &Response{
		Content:       content.String(),
		ContentBlocks: blocks,
		StopReason:    resp.StopReason,
		Usage: &TokenUsage{
			InputTokens:              resp.Usage.InputTokens,
			OutputTokens:             resp.Usage.OutputTokens,
			CacheCreationInputTokens: resp.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     resp.Usage.CacheReadInputTokens,
			Model:                    resp.Model,
		},
	}, nil
}

// isLargeContextModel returns true for Claude models with a 200k context window.
func isLargeContextModel(model string) bool {
	return strings.Contains(model, "claude")
}
