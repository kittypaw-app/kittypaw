package engine

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

// --- Provider Resolution Tests ---

func TestResolveVisionProvider(t *testing.T) {
	tests := []struct {
		name     string
		cfg      core.Config
		envVars  map[string]string
		wantProv string
		wantErr  bool
	}{
		{
			name:     "config anthropic with key",
			cfg:      core.Config{LLM: core.LLMConfig{Provider: "anthropic", APIKey: "sk-ant"}},
			wantProv: "anthropic",
		},
		{
			name:     "config openai with key",
			cfg:      core.Config{LLM: core.LLMConfig{Provider: "openai", APIKey: "sk-oai"}},
			wantProv: "openai",
		},
		{
			name:     "config gemini with key",
			cfg:      core.Config{LLM: core.LLMConfig{Provider: "gemini", APIKey: "gem-key"}},
			wantProv: "gemini",
		},
		{
			name:     "env fallback anthropic",
			cfg:      core.Config{},
			envVars:  map[string]string{"ANTHROPIC_API_KEY": "sk-env-ant"},
			wantProv: "anthropic",
		},
		{
			name:     "env fallback openai (no anthropic)",
			cfg:      core.Config{},
			envVars:  map[string]string{"OPENAI_API_KEY": "sk-env-oai"},
			wantProv: "openai",
		},
		{
			name:     "env fallback gemini (no others)",
			cfg:      core.Config{},
			envVars:  map[string]string{"GEMINI_API_KEY": "gem-key"},
			wantProv: "gemini",
		},
		{
			name:    "no keys anywhere",
			cfg:     core.Config{},
			wantErr: true,
		},
		{
			name:     "config anthropic without key falls back to env",
			cfg:      core.Config{LLM: core.LLMConfig{Provider: "anthropic"}},
			envVars:  map[string]string{"OPENAI_API_KEY": "sk-env-oai"},
			wantProv: "openai",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear env.
			for _, k := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY"} {
				t.Setenv(k, "")
			}
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			prov, key, err := resolveVisionProvider(&tt.cfg)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if prov != tt.wantProv {
				t.Errorf("provider = %q, want %q", prov, tt.wantProv)
			}
			if key == "" {
				t.Error("expected non-empty key")
			}
		})
	}
}

func TestResolveImageProvider(t *testing.T) {
	tests := []struct {
		name     string
		cfg      core.Config
		envVars  map[string]string
		wantProv string
		wantErr  bool
	}{
		{
			name:     "config openai with key",
			cfg:      core.Config{LLM: core.LLMConfig{Provider: "openai", APIKey: "sk-oai"}},
			wantProv: "openai",
		},
		{
			name:     "config gemini with key",
			cfg:      core.Config{LLM: core.LLMConfig{Provider: "gemini", APIKey: "gem-key"}},
			wantProv: "gemini",
		},
		{
			name:     "env openai",
			cfg:      core.Config{},
			envVars:  map[string]string{"OPENAI_API_KEY": "sk-env"},
			wantProv: "openai",
		},
		{
			name:     "env gemini only",
			cfg:      core.Config{},
			envVars:  map[string]string{"GEMINI_API_KEY": "gem-key"},
			wantProv: "gemini",
		},
		{
			name:    "anthropic only — error",
			cfg:     core.Config{LLM: core.LLMConfig{Provider: "anthropic", APIKey: "sk-ant"}},
			wantErr: true,
		},
		{
			name:    "no keys",
			cfg:     core.Config{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY"} {
				t.Setenv(k, "")
			}
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			prov, key, err := resolveImageProvider(&tt.cfg)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if prov != tt.wantProv {
				t.Errorf("provider = %q, want %q", prov, tt.wantProv)
			}
			if key == "" {
				t.Error("expected non-empty key")
			}
		})
	}
}

// --- Image Download Tests ---

func TestDownloadImageBase64_Success(t *testing.T) {
	imgData := []byte{0x89, 0x50, 0x4E, 0x47} // PNG magic bytes
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(imgData)
	}))
	defer srv.Close()

	// Override client for test.
	old := visionClient
	visionClient = srv.Client()
	defer func() { visionClient = old }()

	mime, b64, err := downloadImageBase64(context.Background(), srv.URL+"/test.png")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mime != "image/png" {
		t.Errorf("mimeType = %q, want %q", mime, "image/png")
	}
	if b64 == "" {
		t.Error("expected non-empty base64")
	}
}

func TestDownloadImageBase64_SizeLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Write 10MB + 1 byte.
		w.Header().Set("Content-Type", "image/jpeg")
		data := make([]byte, maxImageDownloadSize+1)
		w.Write(data)
	}))
	defer srv.Close()

	old := visionClient
	visionClient = srv.Client()
	defer func() { visionClient = old }()

	_, _, err := downloadImageBase64(context.Background(), srv.URL+"/huge.jpg")
	if err == nil {
		t.Fatal("expected size limit error, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "size limit") {
		t.Errorf("error = %q, want to contain 'size limit'", got)
	}
}

func TestDownloadImageBase64_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	old := visionClient
	visionClient = srv.Client()
	defer func() { visionClient = old }()

	_, _, err := downloadImageBase64(context.Background(), srv.URL+"/missing.png")
	if err == nil {
		t.Fatal("expected error for 404")
	}
}

// --- Vision Provider Tests ---

func TestVisionClaude_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Errorf("x-api-key = %q, want %q", got, "test-key")
		}
		if got := r.Header.Get("anthropic-version"); got != anthropicAPIVersion {
			t.Errorf("anthropic-version = %q, want %q", got, anthropicAPIVersion)
		}

		// Verify request body has image content.
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != claudeVisionModel {
			t.Errorf("model = %v, want %v", body["model"], claudeVisionModel)
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"content": [{"text": "A photo of a cat"}],
			"usage": {"input_tokens": 100, "output_tokens": 20},
			"model": "claude-sonnet-4-20250514"
		}`)
	}))
	defer srv.Close()

	old := visionClient
	visionClient = srv.Client()
	defer func() { visionClient = old }()

	result, err := visionClaudeWithURL(context.Background(), srv.URL, "https://img.jpg", "describe", "test-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["text"] != "A photo of a cat" {
		t.Errorf("text = %v, want %q", result["text"], "A photo of a cat")
	}
}

func TestVisionOpenAI_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer test-key")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"choices": [{"message": {"content": "A sunset over mountains"}}],
			"usage": {"prompt_tokens": 80, "completion_tokens": 15},
			"model": "gpt-4o"
		}`)
	}))
	defer srv.Close()

	old := visionClient
	visionClient = srv.Client()
	defer func() { visionClient = old }()

	result, err := visionOpenAIWithURL(context.Background(), srv.URL, "https://img.jpg", "describe", "test-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["text"] != "A sunset over mountains" {
		t.Errorf("text = %v, want %q", result["text"], "A sunset over mountains")
	}
	if result["model"] != "gpt-4o" {
		t.Errorf("model = %v, want %q", result["model"], "gpt-4o")
	}
}

func TestVisionGemini_Success(t *testing.T) {
	// Image download server.
	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte{0xFF, 0xD8, 0xFF}) // JPEG magic
	}))
	defer imgSrv.Close()

	// Gemini API server.
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"candidates": [{"content": {"parts": [{"text": "A landscape"}]}}],
			"usageMetadata": {"promptTokenCount": 50, "candidatesTokenCount": 10},
			"modelVersion": "gemini-2.0-flash"
		}`)
	}))
	defer apiSrv.Close()

	old := visionClient
	visionClient = imgSrv.Client()
	defer func() { visionClient = old }()

	result, err := visionGeminiWithURL(context.Background(), apiSrv.URL, imgSrv.URL+"/photo.jpg", "describe", "gem-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["text"] != "A landscape" {
		t.Errorf("text = %v, want %q", result["text"], "A landscape")
	}
}

// --- Image Generation Tests ---

func TestImageOpenAI_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != dalleModel {
			t.Errorf("model = %v, want %v", body["model"], dalleModel)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data": [{"url": "https://cdn.openai.com/generated.png"}]}`)
	}))
	defer srv.Close()

	old := visionClient
	visionClient = srv.Client()
	defer func() { visionClient = old }()

	result, err := imageOpenAIWithURL(context.Background(), srv.URL, "a sunset", "test-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["url"] != "https://cdn.openai.com/generated.png" {
		t.Errorf("url = %v", result["url"])
	}
	if result["imageUrl"] != result["url"] {
		t.Errorf("imageUrl = %v, want %v", result["imageUrl"], result["url"])
	}
}

func TestImageOpenAI_Base64Response(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data": [{"b64_json": "aW1hZ2VkYXRh"}]}`)
	}))
	defer srv.Close()

	old := visionClient
	visionClient = srv.Client()
	defer func() { visionClient = old }()

	result, err := imageOpenAIWithURL(context.Background(), srv.URL, "a sunset", "test-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "data:image/png;base64,aW1hZ2VkYXRh"
	if result["url"] != want {
		t.Errorf("url = %v, want %q", result["url"], want)
	}
	if result["imageUrl"] != want {
		t.Errorf("imageUrl = %v, want %q", result["imageUrl"], want)
	}
}

func TestImageOpenAI_MissingPayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data": [{"revised_prompt": "a better prompt"}]}`)
	}))
	defer srv.Close()

	old := visionClient
	visionClient = srv.Client()
	defer func() { visionClient = old }()

	_, err := imageOpenAIWithURL(context.Background(), srv.URL, "test", "key")
	if err == nil {
		t.Fatal("expected error for missing image payload")
	}
	if !strings.Contains(err.Error(), "missing image payload") {
		t.Errorf("error = %q, want to contain 'missing image payload'", err.Error())
	}
}

func TestImageGemini_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"predictions": [{"bytesBase64Encoded": "aW1hZ2VkYXRh", "mimeType": "image/png"}]}`)
	}))
	defer srv.Close()

	old := visionClient
	visionClient = srv.Client()
	defer func() { visionClient = old }()

	result, err := imageGeminiWithURL(context.Background(), srv.URL, "a sunset", "gem-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	url, ok := result["url"].(string)
	if !ok || !strings.Contains(url, "data:image/png;base64,") {
		t.Errorf("url = %v, want data URI", result["url"])
	}
	if result["imageUrl"] != result["url"] {
		t.Errorf("imageUrl = %v, want %v", result["imageUrl"], result["url"])
	}
}

func TestImageGenerate_ClaudeOnlyError(t *testing.T) {
	for _, k := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY"} {
		t.Setenv(k, "")
	}
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant")

	_, _, err := resolveImageProvider(&core.Config{LLM: core.LLMConfig{Provider: "anthropic", APIKey: "sk-ant"}})
	if err == nil {
		t.Fatal("expected error for Claude-only")
	}
	if !strings.Contains(err.Error(), "image generation requires") {
		t.Errorf("error = %q, want to contain provider requirement message", err.Error())
	}
}

// --- Retry Tests ---

func TestDoVisionRequest_Retry429(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok": true}`)
	}))
	defer srv.Close()

	old := visionClient
	visionClient = srv.Client()
	defer func() { visionClient = old }()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL, nil)
	resp, err := doVisionRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
}

// --- Argument Validation Tests ---

func TestExecuteVision_MissingArgs(t *testing.T) {
	s := &AccountRuntime{Config: &core.Config{}}
	call := core.SkillCall{SkillName: "Vision", Method: "analyze", Args: nil}
	result, err := executeVision(context.Background(), call, s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed map[string]any
	json.Unmarshal([]byte(result), &parsed)
	if parsed["error"] == nil {
		t.Error("expected error in result")
	}
}

func TestExecuteVision_UnknownMethod(t *testing.T) {
	s := &AccountRuntime{Config: &core.Config{}}
	call := core.SkillCall{SkillName: "Vision", Method: "unknown", Args: nil}
	result, err := executeVision(context.Background(), call, s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed map[string]any
	json.Unmarshal([]byte(result), &parsed)
	errMsg, _ := parsed["error"].(string)
	if !strings.Contains(errMsg, "unknown Vision method") {
		t.Errorf("error = %q, want 'unknown Vision method'", errMsg)
	}
}

func TestExecuteVisionAnalyzeAttachmentUsesCurrentEvent(t *testing.T) {
	eventPayload, _ := json.Marshal(core.ChatPayload{
		Text: "이 사진 설명해줘",
		Attachments: []core.ChatAttachment{{
			ID:     "tg_42_0",
			Type:   "image",
			Source: "telegram",
			URL:    "https://api.telegram.org/file/botsecret-token/photos/cat.jpg",
		}},
	})
	event := core.Event{Type: core.EventTelegram, Payload: eventPayload}
	ctx := ContextWithEvent(context.Background(), &event)

	s := &AccountRuntime{Config: &core.Config{}}
	args := []json.RawMessage{
		json.RawMessage(`"tg_42_0"`),
		json.RawMessage(`"Describe this image"`),
	}
	call := core.SkillCall{SkillName: "Vision", Method: "analyzeAttachment", Args: args}
	result, err := executeVision(ctx, call, s)
	if err != nil {
		t.Fatalf("executeVision: %v", err)
	}
	if strings.Contains(result, "attachment not found") || strings.Contains(result, "unknown Vision method") {
		t.Fatalf("attachment was not resolved: %s", result)
	}
	if !strings.Contains(result, "no vision provider API key configured") {
		t.Fatalf("result = %s, want provider configuration error after attachment resolution", result)
	}
}

func TestExecuteImage_MissingArgs(t *testing.T) {
	s := &AccountRuntime{Config: &core.Config{}}
	call := core.SkillCall{SkillName: "Image", Method: "generate", Args: nil}
	result, err := executeImage(context.Background(), call, s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed map[string]any
	json.Unmarshal([]byte(result), &parsed)
	if parsed["error"] == nil {
		t.Error("expected error in result")
	}
}

func TestExecuteImage_UnknownMethod(t *testing.T) {
	s := &AccountRuntime{Config: &core.Config{}}
	call := core.SkillCall{SkillName: "Image", Method: "unknown", Args: nil}
	result, err := executeImage(context.Background(), call, s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed map[string]any
	json.Unmarshal([]byte(result), &parsed)
	errMsg, _ := parsed["error"].(string)
	if !strings.Contains(errMsg, "unknown Image method") {
		t.Errorf("error = %q, want 'unknown Image method'", errMsg)
	}
}

func TestExecuteVision_EmptyURL(t *testing.T) {
	s := &AccountRuntime{Config: &core.Config{}}
	args := []json.RawMessage{json.RawMessage(`""`)}
	call := core.SkillCall{SkillName: "Vision", Method: "analyze", Args: args}
	result, err := executeVision(context.Background(), call, s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed map[string]any
	json.Unmarshal([]byte(result), &parsed)
	if parsed["error"] == nil {
		t.Error("expected error for empty URL")
	}
}

func TestExecuteVision_SSRFBlocked(t *testing.T) {
	for _, k := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY"} {
		t.Setenv(k, "")
	}
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")

	s := &AccountRuntime{Config: &core.Config{}}
	tests := []struct {
		name string
		url  string
	}{
		{"localhost", `"http://127.0.0.1:8080/admin"`},
		{"private IP", `"http://192.168.1.1/secret"`},
		{"metadata", `"http://169.254.169.254/latest/meta-data/"`},
		{"file scheme", `"file:///etc/passwd"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := []json.RawMessage{json.RawMessage(tt.url)}
			call := core.SkillCall{SkillName: "Vision", Method: "analyze", Args: args}
			result, err := executeVision(context.Background(), call, s)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var parsed map[string]any
			json.Unmarshal([]byte(result), &parsed)
			if parsed["error"] == nil {
				t.Errorf("expected SSRF error for %s", tt.name)
			}
		})
	}
}

func TestExecuteImage_EmptyPrompt(t *testing.T) {
	s := &AccountRuntime{Config: &core.Config{}}
	args := []json.RawMessage{json.RawMessage(`""`)}
	call := core.SkillCall{SkillName: "Image", Method: "generate", Args: args}
	result, err := executeImage(context.Background(), call, s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed map[string]any
	json.Unmarshal([]byte(result), &parsed)
	if parsed["error"] == nil {
		t.Error("expected error for empty prompt")
	}
}

func TestExecuteVision_DefaultPrompt(t *testing.T) {
	// Verify default prompt is used when only imageUrl is provided.
	var receivedPrompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		msgs := body["messages"].([]any)
		content := msgs[0].(map[string]any)["content"].([]any)
		for _, part := range content {
			p := part.(map[string]any)
			if p["type"] == "text" {
				receivedPrompt = p["text"].(string)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"content": [{"text": "test"}],
			"usage": {"input_tokens": 1, "output_tokens": 1},
			"model": "test"
		}`)
	}))
	defer srv.Close()

	old := visionClient
	visionClient = srv.Client()
	defer func() { visionClient = old }()

	_, err := visionClaudeWithURL(context.Background(), srv.URL, "https://img.jpg", visionDefaultPrompt, "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedPrompt != visionDefaultPrompt {
		t.Errorf("prompt = %q, want %q", receivedPrompt, visionDefaultPrompt)
	}
}

// --- API Error Tests ---

func TestVisionClaude_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error": {"message": "invalid image"}}`)
	}))
	defer srv.Close()

	old := visionClient
	visionClient = srv.Client()
	defer func() { visionClient = old }()

	_, err := visionClaudeWithURL(context.Background(), srv.URL, "https://bad.jpg", "describe", "key")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "API error 400") {
		t.Errorf("error = %q, want to contain 'API error 400'", err.Error())
	}
}

func TestImageOpenAI_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error": {"message": "bad prompt"}}`)
	}))
	defer srv.Close()

	old := visionClient
	visionClient = srv.Client()
	defer func() { visionClient = old }()

	_, err := imageOpenAIWithURL(context.Background(), srv.URL, "test", "key")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "API error 400") {
		t.Errorf("error = %q, want to contain 'API error 400'", err.Error())
	}
}

func TestImageOpenAI_EmptyData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data": []}`)
	}))
	defer srv.Close()

	old := visionClient
	visionClient = srv.Client()
	defer func() { visionClient = old }()

	_, err := imageOpenAIWithURL(context.Background(), srv.URL, "test", "key")
	if err == nil {
		t.Fatal("expected error for empty data")
	}
	if !strings.Contains(err.Error(), "no images") {
		t.Errorf("error = %q, want to contain 'no images'", err.Error())
	}
}

func TestImageGemini_EmptyPredictions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"predictions": []}`)
	}))
	defer srv.Close()

	old := visionClient
	visionClient = srv.Client()
	defer func() { visionClient = old }()

	_, err := imageGeminiWithURL(context.Background(), srv.URL, "test", "key")
	if err == nil {
		t.Fatal("expected error for empty predictions")
	}
	if !strings.Contains(err.Error(), "no predictions") {
		t.Errorf("error = %q, want to contain 'no predictions'", err.Error())
	}
}
