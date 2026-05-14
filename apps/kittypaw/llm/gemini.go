package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jinto/kittypaw/core"
)

const (
	geminiDefaultBaseURL = "https://generativelanguage.googleapis.com/v1beta/models"
	geminiDefaultWindow  = 1_048_576
	geminiMaxRetries     = 3
	geminiBaseDelay      = 1 * time.Second
)

// GeminiProvider implements Provider for the Gemini GenerateContent API.
type GeminiProvider struct {
	apiKey        string
	model         string
	maxTokens     int
	contextWindow int
	baseURL       string
	client        *http.Client
}

type GeminiOption func(*GeminiProvider)

func WithGeminiHTTPClient(c *http.Client) GeminiOption {
	return func(p *GeminiProvider) {
		p.client = c
	}
}

func WithGeminiBaseURL(baseURL string) GeminiOption {
	return func(p *GeminiProvider) {
		p.baseURL = baseURL
	}
}

func NewGemini(apiKey, model string, maxTokens int, opts ...GeminiOption) *GeminiProvider {
	p := &GeminiProvider{
		apiKey:        apiKey,
		model:         model,
		maxTokens:     maxTokens,
		contextWindow: geminiDefaultWindow,
		baseURL:       geminiDefaultBaseURL,
		client:        &http.Client{Timeout: 5 * time.Minute},
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func (g *GeminiProvider) ContextWindow() int { return g.contextWindow }

func (g *GeminiProvider) MaxTokens() int { return g.maxTokens }

func (g *GeminiProvider) Generate(ctx context.Context, messages []core.LlmMessage) (*Response, error) {
	system, conversation := splitSystemMessages(messages)
	body := g.buildRequestBody(system, conversation)
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("gemini: marshal request: %w", err)
	}
	resp, err := g.doWithRetry(ctx, payload)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return g.parseJSONResponse(resp.Body)
}

func (g *GeminiProvider) GenerateWithTools(ctx context.Context, messages []core.LlmMessage, _ []Tool) (*Response, error) {
	return g.Generate(ctx, messages)
}

type geminiGenerateContentRequest struct {
	SystemInstruction *geminiContent          `json:"systemInstruction,omitempty"`
	Contents          []geminiContent         `json:"contents"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiGenerationConfig struct {
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text,omitempty"`
}

func (g *GeminiProvider) buildRequestBody(system string, messages []core.LlmMessage) geminiGenerateContentRequest {
	req := geminiGenerateContentRequest{
		Contents: make([]geminiContent, 0, len(messages)),
		GenerationConfig: &geminiGenerationConfig{
			MaxOutputTokens: g.maxTokens,
		},
	}
	if system != "" {
		req.SystemInstruction = &geminiContent{Parts: []geminiPart{{Text: system}}}
	}
	for _, m := range messages {
		text := textFromMessage(m)
		if text == "" {
			continue
		}
		req.Contents = append(req.Contents, geminiContent{
			Role:  geminiRole(m.Role),
			Parts: []geminiPart{{Text: text}},
		})
	}
	return req
}

func geminiRole(role core.Role) string {
	if role == core.RoleAssistant {
		return "model"
	}
	return "user"
}

func (g *GeminiProvider) newRequest(ctx context.Context, payload []byte) (*http.Request, error) {
	endpoint, err := g.endpointURL()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (g *GeminiProvider) endpointURL() (string, error) {
	raw := strings.TrimRight(g.baseURL, "/") + "/" + url.PathEscape(g.model) + ":generateContent"
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	q := u.Query()
	if g.apiKey != "" {
		q.Set("key", g.apiKey)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (g *GeminiProvider) doWithRetry(ctx context.Context, payload []byte) (*http.Response, error) {
	var lastErr error
	retryAfter := ""
	for attempt := 0; attempt <= geminiMaxRetries; attempt++ {
		if attempt > 0 {
			delay := providerRetryDelay(geminiBaseDelay, attempt, retryAfter, time.Now())
			retryAfter = ""
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		req, err := g.newRequest(ctx, payload)
		if err != nil {
			return nil, fmt.Errorf("gemini: build request: %w", err)
		}
		resp, err := g.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("gemini: http request: %w", err)
			retryAfter = ""
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
			retryAfter = resp.Header.Get("Retry-After")
			resp.Body.Close()
			lastErr = fmt.Errorf("gemini: server returned %d", resp.StatusCode)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("gemini: API error %d: %s", resp.StatusCode, string(body))
		}
		return resp, nil
	}
	return nil, fmt.Errorf("gemini: retries exhausted: %w", lastErr)
}

type geminiGenerateContentResponse struct {
	Candidates []struct {
		Content      geminiContent `json:"content"`
		FinishReason string        `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata *struct {
		PromptTokenCount     int64 `json:"promptTokenCount"`
		CandidatesTokenCount int64 `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
	ModelVersion string `json:"modelVersion"`
}

func (g *GeminiProvider) parseJSONResponse(r io.Reader) (*Response, error) {
	var resp geminiGenerateContentResponse
	if err := json.NewDecoder(io.LimitReader(r, llmMaxResponseBytes)).Decode(&resp); err != nil {
		return nil, fmt.Errorf("gemini: decode response: %w", err)
	}

	var contentParts []string
	stopReason := ""
	if len(resp.Candidates) > 0 {
		stopReason = resp.Candidates[0].FinishReason
		for _, part := range resp.Candidates[0].Content.Parts {
			if part.Text != "" {
				contentParts = append(contentParts, part.Text)
			}
		}
	}
	result := &Response{
		Content:    strings.Join(contentParts, ""),
		StopReason: stopReason,
	}
	if resp.UsageMetadata != nil {
		result.Usage = &TokenUsage{
			InputTokens:  resp.UsageMetadata.PromptTokenCount,
			OutputTokens: resp.UsageMetadata.CandidatesTokenCount,
			Model:        resp.ModelVersion,
		}
	}
	return result, nil
}
