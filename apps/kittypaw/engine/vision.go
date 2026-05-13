package engine

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jinto/kittypaw/core"
)

// Vision/Image skill constants.
const (
	visionMaxTokens     = 1024
	visionDefaultPrompt = "Describe this image in detail."

	maxImageDownloadSize = 10 * 1024 * 1024 // 10 MB

	visionMaxRetries = 3
	visionBaseDelay  = 1 * time.Second

	// Default models (hard-coded per spec; config override is non-goal).
	claudeVisionModel = "claude-sonnet-4-20250514"
	openAIVisionModel = "gpt-4o"
	geminiVisionModel = "gemini-2.0-flash"
	dalleModel        = "dall-e-3"
	imagenModel       = "imagen-3.0-generate-002"

	// API endpoints.
	anthropicMessagesURL = "https://api.anthropic.com/v1/messages"
	anthropicAPIVersion  = "2023-06-01"
	openAIChatURL        = "https://api.openai.com/v1/chat/completions"
	openAIImagesURL      = "https://api.openai.com/v1/images/generations"
	geminiBaseURL        = "https://generativelanguage.googleapis.com/v1beta/models"
)

// visionClient is a shared HTTP client for all vision/image API calls.
// CheckRedirect validates each redirect target against private IP ranges
// to prevent SSRF via open redirects (e.g., 302 → 169.254.169.254).
var visionClient = &http.Client{
	Timeout: 2 * time.Minute,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		host := req.URL.Hostname()
		if core.IsPrivateIP(host) {
			return fmt.Errorf("redirect to private address %q blocked", host)
		}
		return nil
	},
}

// --- Provider Resolution ---

// resolveAPIKey returns the API key for a provider, checking config first then
// environment variables. Returns empty string if no key is found.
func resolveAPIKey(provider string, cfg *core.Config) string {
	switch strings.ToLower(provider) {
	case "anthropic", "claude":
		if cfg.LLM.Provider == "anthropic" || cfg.LLM.Provider == "claude" {
			if cfg.LLM.APIKey != "" {
				return cfg.LLM.APIKey
			}
		}
		return os.Getenv("ANTHROPIC_API_KEY")
	case "openai", "gpt":
		if cfg.LLM.Provider == "openai" || cfg.LLM.Provider == "gpt" {
			if cfg.LLM.APIKey != "" {
				return cfg.LLM.APIKey
			}
		}
		return os.Getenv("OPENAI_API_KEY")
	case "gemini":
		if cfg.LLM.Provider == "gemini" || cfg.LLM.Provider == "google" {
			if cfg.LLM.APIKey != "" {
				return cfg.LLM.APIKey
			}
		}
		return os.Getenv("GEMINI_API_KEY")
	}
	return ""
}

// resolveVisionProvider selects the best available provider for Vision.analyze.
// Priority: config LLM provider (if key available) → env fallback chain.
func resolveVisionProvider(cfg *core.Config) (provider, apiKey string, err error) {
	// First: honor the user's configured LLM provider.
	switch strings.ToLower(cfg.LLM.Provider) {
	case "anthropic", "claude":
		if key := resolveAPIKey("anthropic", cfg); key != "" {
			return "anthropic", key, nil
		}
	case "openai", "gpt":
		if key := resolveAPIKey("openai", cfg); key != "" {
			return "openai", key, nil
		}
	case "gemini", "google":
		if key := resolveAPIKey("gemini", cfg); key != "" {
			return "gemini", key, nil
		}
	}

	// Fallback: try each provider via env vars.
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		return "anthropic", key, nil
	}
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		return "openai", key, nil
	}
	if key := os.Getenv("GEMINI_API_KEY"); key != "" {
		return "gemini", key, nil
	}

	return "", "", fmt.Errorf("no vision provider API key configured; set ANTHROPIC_API_KEY, OPENAI_API_KEY, or GEMINI_API_KEY")
}

// resolveImageProvider selects the best available provider for Image.generate.
// Claude does not support image generation, so only OpenAI and Gemini are candidates.
func resolveImageProvider(cfg *core.Config) (provider, apiKey string, err error) {
	// Prefer OpenAI if configured or available.
	if key := resolveAPIKey("openai", cfg); key != "" {
		return "openai", key, nil
	}
	if key := resolveAPIKey("gemini", cfg); key != "" {
		return "gemini", key, nil
	}

	return "", "", fmt.Errorf("image generation requires OpenAI or Gemini API key")
}

// --- URL Validation ---

// validateImageURL validates an image URL for scheme and SSRF prevention.
func validateImageURL(imageURL string, allowedHosts []string) error {
	parsed, err := url.Parse(imageURL)
	if err != nil {
		return fmt.Errorf("invalid image URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("unsupported URL scheme %q; only http and https are allowed", parsed.Scheme)
	}
	return validateHTTPTarget(imageURL, allowedHosts)
}

// --- Image Download (for Gemini base64 requirement) ---

// downloadImageBase64 fetches an image from a URL and returns its MIME type and
// base64-encoded data. Enforces a 10 MB size limit.
func downloadImageBase64(ctx context.Context, imageURL string) (mimeType, b64 string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return "", "", fmt.Errorf("invalid image URL: %w", err)
	}

	resp, err := visionClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("failed to download image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("image download returned HTTP %d", resp.StatusCode)
	}

	// Read with size limit.
	limited := io.LimitReader(resp.Body, maxImageDownloadSize+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return "", "", fmt.Errorf("failed to read image: %w", err)
	}
	if len(data) > maxImageDownloadSize {
		return "", "", fmt.Errorf("image exceeds %d MB size limit", maxImageDownloadSize/(1024*1024))
	}

	// Detect MIME type from Content-Type header, fallback to sniffing.
	ct := resp.Header.Get("Content-Type")
	if ct == "" || ct == "application/octet-stream" {
		ct = http.DetectContentType(data)
	}
	// Normalize to just the media type (strip parameters).
	if idx := strings.Index(ct, ";"); idx != -1 {
		ct = strings.TrimSpace(ct[:idx])
	}

	return ct, base64.StdEncoding.EncodeToString(data), nil
}

// --- Retry Helper ---

// doVisionRequest executes an HTTP request with exponential backoff on
// 429 (rate limit) and 503 (service unavailable) responses.
func doVisionRequest(ctx context.Context, req *http.Request) (*http.Response, error) {
	// We need to buffer the body for retries.
	var bodyBytes []byte
	if req.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("read request body: %w", err)
		}
		req.Body.Close()
	}

	var lastErr error
	for attempt := 0; attempt <= visionMaxRetries; attempt++ {
		if attempt > 0 {
			delay := time.Duration(float64(visionBaseDelay) * math.Pow(2, float64(attempt-1)) * (0.5 + rand.Float64()))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		// Rebuild body reader for each attempt.
		if bodyBytes != nil {
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		resp, err := visionClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("http request: %w", err)
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
			resp.Body.Close()
			lastErr = fmt.Errorf("server returned %d", resp.StatusCode)
			continue
		}

		return resp, nil
	}

	return nil, fmt.Errorf("retries exhausted: %w", lastErr)
}

// readErrorBody reads the response body (capped at 64KB) and returns a descriptive error.
// Does NOT close the body — callers use defer resp.Body.Close().
func readErrorBody(resp *http.Response, provider string) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	return fmt.Errorf("%s: API error %d: %s", provider, resp.StatusCode, string(body))
}

// sanitizeError strips API keys from error messages (e.g., Gemini ?key=... in URLs).
func sanitizeError(err error) error {
	msg := err.Error()
	if i := strings.Index(msg, "?key="); i != -1 {
		end := strings.IndexAny(msg[i:], " \t\n\"')")
		if end == -1 {
			msg = msg[:i] + "?key=REDACTED"
		} else {
			msg = msg[:i] + "?key=REDACTED" + msg[i+end:]
		}
		return fmt.Errorf("%s", msg)
	}
	return err
}

// --- Skill Handlers ---

func executeVision(ctx context.Context, call core.SkillCall, s *AccountRuntime) (string, error) {
	switch call.Method {
	case "analyze":
		if len(call.Args) == 0 {
			return jsonResult(map[string]any{"error": "imageUrl argument required"})
		}
		var imageURL string
		if err := json.Unmarshal(call.Args[0], &imageURL); err != nil || imageURL == "" {
			return jsonResult(map[string]any{"error": "invalid imageUrl argument"})
		}
		return executeVisionAnalyzeURL(ctx, imageURL, visionPromptArg(call.Args), s)

	case "analyzeAttachment":
		if len(call.Args) == 0 {
			return jsonResult(map[string]any{"error": "attachmentId argument required"})
		}
		var attachmentID string
		if err := json.Unmarshal(call.Args[0], &attachmentID); err != nil || attachmentID == "" {
			return jsonResult(map[string]any{"error": "invalid attachmentId argument"})
		}
		att, err := currentImageAttachment(ctx, attachmentID)
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return executeVisionAnalyzeURL(ctx, att.URL, visionPromptArg(call.Args), s)

	default:
		return jsonResult(map[string]any{"error": fmt.Sprintf("unknown Vision method: %s", call.Method)})
	}
}

func executeVisionAnalyzeURL(ctx context.Context, imageURL, prompt string, s *AccountRuntime) (string, error) {
	cfg := &core.Config{}
	if s != nil && s.Config != nil {
		cfg = s.Config
	}
	// SSRF prevention: validate URL scheme and host.
	if err := validateImageURL(imageURL, cfg.Sandbox.AllowedHosts); err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}

	provider, apiKey, err := resolveVisionProvider(cfg)
	if err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}

	var result map[string]any
	switch provider {
	case "anthropic":
		result, err = visionClaude(ctx, imageURL, prompt, apiKey)
	case "openai":
		result, err = visionOpenAI(ctx, imageURL, prompt, apiKey)
	case "gemini":
		result, err = visionGemini(ctx, imageURL, prompt, apiKey)
	default:
		return jsonResult(map[string]any{"error": fmt.Sprintf("unsupported vision provider: %s", provider)})
	}
	if err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}
	return jsonResult(result)
}

func visionPromptArg(args []json.RawMessage) string {
	prompt := visionDefaultPrompt
	if len(args) > 1 {
		var p string
		if err := json.Unmarshal(args[1], &p); err == nil && p != "" {
			prompt = p
		}
	}
	return prompt
}

func currentImageAttachment(ctx context.Context, attachmentID string) (core.ChatAttachment, error) {
	event := EventFromContext(ctx)
	if event == nil {
		return core.ChatAttachment{}, fmt.Errorf("attachment %q not found: no current event", attachmentID)
	}
	payload, err := event.ParsePayload()
	if err != nil {
		return core.ChatAttachment{}, err
	}
	for _, att := range payload.Attachments {
		if att.ID != attachmentID {
			continue
		}
		if att.Type != "image" {
			return core.ChatAttachment{}, fmt.Errorf("attachment %q is %q, not image", attachmentID, att.Type)
		}
		if att.URL == "" {
			return core.ChatAttachment{}, fmt.Errorf("attachment %q has no URL", attachmentID)
		}
		return att, nil
	}
	return core.ChatAttachment{}, fmt.Errorf("attachment %q not found", attachmentID)
}

func executeImage(ctx context.Context, call core.SkillCall, s *AccountRuntime) (string, error) {
	if call.Method != "generate" {
		return jsonResult(map[string]any{"error": fmt.Sprintf("unknown Image method: %s", call.Method)})
	}
	if len(call.Args) == 0 {
		return jsonResult(map[string]any{"error": "prompt argument required"})
	}

	var prompt string
	if err := json.Unmarshal(call.Args[0], &prompt); err != nil || prompt == "" {
		return jsonResult(map[string]any{"error": "invalid prompt argument"})
	}

	provider, apiKey, err := resolveImageProvider(s.Config)
	if err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}

	var result map[string]any
	switch provider {
	case "openai":
		result, err = imageOpenAI(ctx, prompt, apiKey)
	case "gemini":
		result, err = imageGemini(ctx, prompt, apiKey)
	default:
		return jsonResult(map[string]any{"error": fmt.Sprintf("unsupported image provider: %s", provider)})
	}
	if err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}
	return jsonResult(result)
}

// --- Vision Providers ---

func visionClaude(ctx context.Context, imageURL, prompt, apiKey string) (map[string]any, error) {
	return visionClaudeWithURL(ctx, anthropicMessagesURL, imageURL, prompt, apiKey)
}

func visionClaudeWithURL(ctx context.Context, endpoint, imageURL, prompt, apiKey string) (map[string]any, error) {
	body := map[string]any{
		"model":      claudeVisionModel,
		"max_tokens": visionMaxTokens,
		"messages": []map[string]any{{
			"role": "user",
			"content": []map[string]any{
				{"type": "image", "source": map[string]any{"type": "url", "url": imageURL}},
				{"type": "text", "text": prompt},
			},
		}},
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", anthropicAPIVersion)
	req.Body = io.NopCloser(bytes.NewReader(payload))

	resp, err := doVisionRequest(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("claude vision: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readErrorBody(resp, "claude vision")
	}

	var apiResp struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
		Model string `json:"model"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("claude vision: parse response: %w", err)
	}

	text := ""
	if len(apiResp.Content) > 0 {
		text = apiResp.Content[0].Text
	}

	return map[string]any{
		"text":  text,
		"model": apiResp.Model,
		"usage": map[string]any{
			"input_tokens":  apiResp.Usage.InputTokens,
			"output_tokens": apiResp.Usage.OutputTokens,
		},
	}, nil
}

func visionOpenAI(ctx context.Context, imageURL, prompt, apiKey string) (map[string]any, error) {
	return visionOpenAIWithURL(ctx, openAIChatURL, imageURL, prompt, apiKey)
}

func visionOpenAIWithURL(ctx context.Context, endpoint, imageURL, prompt, apiKey string) (map[string]any, error) {
	body := map[string]any{
		"model":      openAIVisionModel,
		"max_tokens": visionMaxTokens,
		"messages": []map[string]any{{
			"role": "user",
			"content": []map[string]any{
				{"type": "image_url", "image_url": map[string]any{"url": imageURL}},
				{"type": "text", "text": prompt},
			},
		}},
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Body = io.NopCloser(bytes.NewReader(payload))

	resp, err := doVisionRequest(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("openai vision: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readErrorBody(resp, "openai vision")
	}

	var apiResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
		} `json:"usage"`
		Model string `json:"model"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("openai vision: parse response: %w", err)
	}

	text := ""
	if len(apiResp.Choices) > 0 {
		text = apiResp.Choices[0].Message.Content
	}

	result := map[string]any{
		"text":  text,
		"model": apiResp.Model,
	}
	if apiResp.Usage != nil {
		result["usage"] = map[string]any{
			"input_tokens":  apiResp.Usage.PromptTokens,
			"output_tokens": apiResp.Usage.CompletionTokens,
		}
	}
	return result, nil
}

func visionGemini(ctx context.Context, imageURL, prompt, apiKey string) (map[string]any, error) {
	endpoint := fmt.Sprintf("%s/%s:generateContent?key=%s", geminiBaseURL, geminiVisionModel, apiKey)
	return visionGeminiWithURL(ctx, endpoint, imageURL, prompt, apiKey)
}

func visionGeminiWithURL(ctx context.Context, endpoint, imageURL, prompt, apiKey string) (map[string]any, error) {
	// Gemini requires base64-encoded image data.
	mimeType, b64Data, err := downloadImageBase64(ctx, imageURL)
	if err != nil {
		return nil, fmt.Errorf("gemini vision: %w", err)
	}

	body := map[string]any{
		"contents": []map[string]any{{
			"parts": []map[string]any{
				{"inlineData": map[string]any{"mimeType": mimeType, "data": b64Data}},
				{"text": prompt},
			},
		}},
		"generationConfig": map[string]any{
			"maxOutputTokens": visionMaxTokens,
		},
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Body = io.NopCloser(bytes.NewReader(payload))

	resp, err := doVisionRequest(ctx, req)
	if err != nil {
		return nil, sanitizeError(fmt.Errorf("gemini vision: %w", err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, sanitizeError(readErrorBody(resp, "gemini vision"))
	}

	var apiResp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		UsageMetadata *struct {
			PromptTokenCount     int64 `json:"promptTokenCount"`
			CandidatesTokenCount int64 `json:"candidatesTokenCount"`
		} `json:"usageMetadata"`
		ModelVersion string `json:"modelVersion"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("gemini vision: parse response: %w", err)
	}

	text := ""
	if len(apiResp.Candidates) > 0 && len(apiResp.Candidates[0].Content.Parts) > 0 {
		text = apiResp.Candidates[0].Content.Parts[0].Text
	}

	result := map[string]any{
		"text":  text,
		"model": apiResp.ModelVersion,
	}
	if apiResp.UsageMetadata != nil {
		result["usage"] = map[string]any{
			"input_tokens":  apiResp.UsageMetadata.PromptTokenCount,
			"output_tokens": apiResp.UsageMetadata.CandidatesTokenCount,
		}
	}
	return result, nil
}

// --- Image Generation Providers ---

func imageOpenAI(ctx context.Context, prompt, apiKey string) (map[string]any, error) {
	return imageOpenAIWithURL(ctx, openAIImagesURL, prompt, apiKey)
}

func imageOpenAIWithURL(ctx context.Context, endpoint, prompt, apiKey string) (map[string]any, error) {
	body := map[string]any{
		"model":  dalleModel,
		"prompt": prompt,
		"n":      1,
		"size":   "1024x1024",
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Body = io.NopCloser(bytes.NewReader(payload))

	resp, err := doVisionRequest(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("openai image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readErrorBody(resp, "openai image")
	}

	var apiResp struct {
		Data []struct {
			URL          string `json:"url"`
			B64JSON      string `json:"b64_json"`
			OutputFormat string `json:"output_format"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("openai image: parse response: %w", err)
	}

	if len(apiResp.Data) == 0 {
		return nil, fmt.Errorf("openai image: no images in response")
	}

	img := apiResp.Data[0]
	imageURL := strings.TrimSpace(img.URL)
	if imageURL == "" && img.B64JSON != "" {
		imageURL = fmt.Sprintf("data:%s;base64,%s", imageFormatMIME(img.OutputFormat), img.B64JSON)
	}
	if imageURL == "" {
		return nil, fmt.Errorf("openai image: missing image payload in response")
	}

	return imageResult(imageURL, dalleModel), nil
}

func imageGemini(ctx context.Context, prompt, apiKey string) (map[string]any, error) {
	endpoint := fmt.Sprintf("%s/%s:predict?key=%s", geminiBaseURL, imagenModel, apiKey)
	return imageGeminiWithURL(ctx, endpoint, prompt, apiKey)
}

func imageGeminiWithURL(ctx context.Context, endpoint, prompt, apiKey string) (map[string]any, error) {
	body := map[string]any{
		"instances":  []map[string]any{{"prompt": prompt}},
		"parameters": map[string]any{"sampleCount": 1},
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Body = io.NopCloser(bytes.NewReader(payload))

	resp, err := doVisionRequest(ctx, req)
	if err != nil {
		return nil, sanitizeError(fmt.Errorf("gemini image: %w", err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, sanitizeError(readErrorBody(resp, "gemini image"))
	}

	var apiResp struct {
		Predictions []struct {
			BytesBase64Encoded string `json:"bytesBase64Encoded"`
			MimeType           string `json:"mimeType"`
		} `json:"predictions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("gemini image: parse response: %w", err)
	}

	if len(apiResp.Predictions) == 0 {
		return nil, fmt.Errorf("gemini image: no predictions in response")
	}

	pred := apiResp.Predictions[0]
	if pred.BytesBase64Encoded == "" {
		return nil, fmt.Errorf("gemini image: missing image payload in response")
	}
	mimeType := pred.MimeType
	if mimeType == "" {
		mimeType = "image/png"
	}

	return imageResult(fmt.Sprintf("data:%s;base64,%s", mimeType, pred.BytesBase64Encoded), imagenModel), nil
}

func imageResult(imageURL, model string) map[string]any {
	return map[string]any{
		"url":      imageURL,
		"imageUrl": imageURL,
		"model":    model,
	}
}

func imageFormatMIME(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "jpeg", "jpg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	default:
		return "image/png"
	}
}
