package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/llm"
	"github.com/jinto/kittypaw/sandbox"
	"github.com/jinto/kittypaw/store"
)

// --- test helpers ---

const testWebChatConversationID = "general:web_chat:test-session"

func skipWithoutRuntime(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("deno"); err == nil {
		return
	}
	if _, err := exec.LookPath("node"); err == nil {
		return
	}
	t.Skip("no JS runtime (deno or node) available")
}

// mockProvider is a queue-based mock that pops responses on each Generate call.
type mockProvider struct {
	responses []*llm.Response
	callIdx   int
}

func (m *mockProvider) Generate(ctx context.Context, msgs []core.LlmMessage) (*llm.Response, error) {
	if m.callIdx >= len(m.responses) {
		return nil, context.DeadlineExceeded
	}
	resp := m.responses[m.callIdx]
	m.callIdx++
	return resp, nil
}

func (m *mockProvider) GenerateWithTools(ctx context.Context, msgs []core.LlmMessage, _ []llm.Tool) (*llm.Response, error) {
	return m.Generate(ctx, msgs)
}

func (m *mockProvider) ContextWindow() int { return 128_000 }
func (m *mockProvider) MaxTokens() int     { return 4096 }

func mockResp(code string) *llm.Response {
	return &llm.Response{
		Content: code,
		Usage:   &llm.TokenUsage{InputTokens: 10, OutputTokens: 5, Model: "mock"},
	}
}

func newTestSession(t *testing.T, responses ...*llm.Response) *Session {
	t.Helper()

	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	cfg := core.DefaultConfig()

	return &Session{
		Provider: &mockProvider{responses: responses},
		Sandbox:  sandbox.New(cfg.Sandbox),
		Store:    st,
		Config:   &cfg,
	}
}

// installTestPackage creates a temp dir with package.toml + main.js,
// installs it via PackageManager, and returns the manager.
func installTestPackage(t *testing.T, baseDir, tomlContent, jsContent string) *core.PackageManager {
	t.Helper()
	srcDir := t.TempDir()
	os.WriteFile(filepath.Join(srcDir, "package.toml"), []byte(tomlContent), 0o644)
	os.WriteFile(filepath.Join(srcDir, "main.js"), []byte(jsContent), 0o644)

	pm := core.NewPackageManagerFrom(baseDir, nil)
	if _, err := pm.Install(srcDir); err != nil {
		t.Fatalf("install test package: %v", err)
	}
	return pm
}

func webChatEvent(text string) core.Event {
	payload, _ := json.Marshal(core.ChatPayload{
		ChatID:    "test-chat",
		Text:      text,
		SessionID: "test-session",
	})
	return core.Event{Type: core.EventWebChat, Payload: payload}
}

// --- E2E tests ---

func TestE2ESimpleReturn(t *testing.T) {
	skipWithoutRuntime(t)

	sess := newTestSession(t, mockResp(`return "Hello from runner";`))
	event := webChatEvent("say hello")

	output, err := sess.Run(context.Background(), event, nil)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if output != "Hello from runner" {
		t.Errorf("output = %q, want %q", output, "Hello from runner")
	}
}

func TestE2EStripsMarkdownFenceBeforeExecution(t *testing.T) {
	skipWithoutRuntime(t)

	sess := newTestSession(t, mockResp("```javascript\nreturn \"Hello from fenced code\";\n```"))
	event := webChatEvent("say hello")

	output, err := sess.Run(context.Background(), event, nil)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if output != "Hello from fenced code" {
		t.Errorf("output = %q, want %q", output, "Hello from fenced code")
	}
}

func TestE2EWrapsPlainTextBeforeExecution(t *testing.T) {
	skipWithoutRuntime(t)

	sess := newTestSession(t, mockResp("안녕하세요! 무엇을 도와드릴까요?"))
	event := webChatEvent("say hello")

	output, err := sess.Run(context.Background(), event, nil)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if output != "안녕하세요! 무엇을 도와드릴까요?" {
		t.Errorf("output = %q, want plain text", output)
	}
}

func TestE2ERecordsPendingClarification(t *testing.T) {
	skipWithoutRuntime(t)

	sess := newTestSession(t, mockResp("환율 말씀이세요? 맞으면 지금 기준으로 찾아볼게요."))
	event := webChatEvent("달러")

	if _, err := sess.Run(context.Background(), event, nil); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	pending, ok := sess.Pipeline.RecentPendingClarification()
	if !ok {
		t.Fatal("expected pending clarification")
	}
	if pending.Kind != "exchange_rate" || pending.Query != "달러" {
		t.Fatalf("pending = %+v", pending)
	}
}

func TestE2ESkillCall(t *testing.T) {
	skipWithoutRuntime(t)

	code := `
		Storage.set("greeting", "hi there");
		const result = Storage.get("greeting");
		return result;
	`
	sess := newTestSession(t, mockResp(code))
	event := webChatEvent("store something")

	output, err := sess.Run(context.Background(), event, nil)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	// The skill call chain: sandbox → resolveSkillCall → executeStorage → Store
	// Storage.get returns {value: "..."} which is then JSON.stringify'd by the sandbox.
	if output == "" || output == "null" {
		t.Errorf("expected non-empty output from Storage round-trip, got %q", output)
	}
	t.Logf("Storage round-trip output: %s", output)

	// Verify the value was persisted in the real SQLite store.
	val, ok, err := sess.Store.StorageGet("default", "greeting")
	if err != nil {
		t.Fatalf("StorageGet error: %v", err)
	}
	if !ok {
		t.Fatal("expected greeting key to exist in store")
	}
	if val == "" {
		t.Error("expected non-empty value for greeting key")
	}
	t.Logf("Store value for 'greeting': %s", val)
}

func TestE2EErrorRetry(t *testing.T) {
	skipWithoutRuntime(t)

	mock := &mockProvider{responses: []*llm.Response{
		mockResp(`throw new Error("boom");`),
		mockResp(`return "recovered";`),
	}}

	cfg := core.DefaultConfig()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	sess := &Session{
		Provider: mock,
		Sandbox:  sandbox.New(cfg.Sandbox),
		Store:    st,
		Config:   &cfg,
	}
	event := webChatEvent("try something")

	output, err := sess.Run(context.Background(), event, nil)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if output != "recovered" {
		t.Errorf("output = %q, want %q", output, "recovered")
	}
	// The LLM should have been called twice: once for the error, once for recovery.
	if mock.callIdx != 2 {
		t.Errorf("mock.callIdx = %d, want 2", mock.callIdx)
	}
}

func TestE2EFileAccessGating(t *testing.T) {
	skipWithoutRuntime(t)

	// Setup: create a real temp directory as the workspace.
	allowedDir := t.TempDir()
	forbiddenDir := t.TempDir()

	// Write a file in the allowed directory.
	allowedFile := filepath.Join(allowedDir, "data.txt")
	os.WriteFile(allowedFile, []byte("allowed content"), 0o644)

	// Write a file in the forbidden directory.
	forbiddenFile := filepath.Join(forbiddenDir, "secret.txt")
	os.WriteFile(forbiddenFile, []byte("secret"), 0o644)

	// LLM response: try to read the allowed file, then the forbidden file.
	code := fmt.Sprintf(`
		try {
			const allowed = File.read(%q);
			const forbidden = File.read(%q);
			return "should not reach: " + forbidden;
		} catch(e) {
			return "blocked:" + e;
		}
	`, allowedFile, forbiddenFile)

	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	// Register only the allowed dir as a workspace in DB.
	st.SaveWorkspace(&store.Workspace{
		ID: "ws-test", Name: "test", RootPath: allowedDir,
	})

	cfg := core.DefaultConfig()
	sess := &Session{
		Provider: &mockProvider{responses: []*llm.Response{mockResp(code)}},
		Sandbox:  sandbox.New(cfg.Sandbox),
		Store:    st,
		Config:   &cfg,
	}
	sess.RefreshAllowedPaths()

	event := webChatEvent("read files")
	output, err := sess.Run(context.Background(), event, nil)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// The forbidden read should have thrown, caught by try/catch.
	if !strings.Contains(output, "blocked:") || !strings.Contains(output, "path not allowed") {
		t.Errorf("expected blocked output, got %q", output)
	}
}

// TestE2EPackageContextInjection tests the golden path:
//
//	kittypaw skill install weather-briefing
//	kittypaw chat "오늘 날씨 알려줘"
//
// It exercises the full package execution pipeline:
// context = ["locale", "location"] declaration → user config injection →
// Llm.generate with auto locale injection → Korean output.
//
// Uses a package that only needs Llm (no HTTP) to keep the test hermetic.
// HTTP + package round-trip is separately verified by manual E2E testing.
func TestE2EPackageContextInjection(t *testing.T) {
	skipWithoutRuntime(t)

	// Package that reads context and calls Llm.generate.
	// The engine should inject locale + location, and append
	// "Respond in Korean." to the Llm.generate prompt.
	mainJS := `
const ctx = JSON.parse(__context__);
const user = ctx.user || {};
const loc = user.location || {};
const city = loc.city || "Unknown";
const locale = user.locale || "?";

const prompt = "City: " + city + ". Summarize the weather.";
const llmRaw = await Llm.generate(prompt);
const llmData = JSON.parse(llmRaw);

return JSON.stringify({
  city: city,
  locale: locale,
  lat: loc.lat,
  lon: loc.lon,
  summary: llmData.text,
});
`
	pkgToml := `
[meta]
id = "context-test"
name = "Context Test"
version = "1.0.0"

[permissions]
primitives = ["Llm"]
context = ["locale", "location"]
`

	baseDir := t.TempDir()
	pm := installTestPackage(t, baseDir, pkgToml, mainJS)

	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	weatherSummary := "오늘 서울은 따뜻합니다."
	mock := &mockProvider{responses: []*llm.Response{
		{Content: weatherSummary, Usage: &llm.TokenUsage{InputTokens: 10, OutputTokens: 5, Model: "mock"}},
	}}

	cfg := core.DefaultConfig()
	cfg.User.Locale = "ko"
	cfg.User.City = "Seoul"
	cfg.User.Latitude = 37.57
	cfg.User.Longitude = 126.98

	sess := &Session{
		Provider:       mock,
		Sandbox:        sandbox.New(cfg.Sandbox),
		Store:          st,
		Config:         &cfg,
		PackageManager: pm,
		BaseDir:        baseDir,
	}

	// Execute with a Korean chat event.
	event := webChatEvent("오늘 날씨 알려줘")
	ctx := ContextWithEvent(context.Background(), &event)
	resultJSON, err := runSkillOrPackage(ctx, "context-test", sess)
	if err != nil {
		t.Fatalf("runSkillOrPackage error: %v", err)
	}

	var wrapper struct {
		Output string `json:"output"`
		Error  string `json:"error"`
	}
	json.Unmarshal([]byte(resultJSON), &wrapper)

	if wrapper.Error != "" {
		t.Fatalf("package error: %s", wrapper.Error)
	}

	// Parse the structured output from the package.
	var output struct {
		City    string  `json:"city"`
		Locale  string  `json:"locale"`
		Lat     float64 `json:"lat"`
		Lon     float64 `json:"lon"`
		Summary string  `json:"summary"`
	}
	if err := json.Unmarshal([]byte(wrapper.Output), &output); err != nil {
		t.Fatalf("parse output: %v (raw: %s)", err, wrapper.Output)
	}

	t.Logf("Package output: %+v", output)

	// Verify context injection.
	if output.City != "Seoul" {
		t.Errorf("city = %q, want Seoul", output.City)
	}
	if output.Locale != "ko" {
		t.Errorf("locale = %q, want ko", output.Locale)
	}
	if output.Lat != 37.57 {
		t.Errorf("lat = %v, want 37.57", output.Lat)
	}
	if output.Summary != weatherSummary {
		t.Errorf("summary = %q, want %q", output.Summary, weatherSummary)
	}
	if mock.callIdx != 1 {
		t.Errorf("expected 1 LLM call, got %d", mock.callIdx)
	}
}

func TestE2EPackageParamsOverlayStructuredLocation(t *testing.T) {
	skipWithoutRuntime(t)

	baseDir := t.TempDir()
	pm := installTestPackage(t, baseDir, `
[meta]
id = "weather-now"
name = "현재 날씨"
version = "1.0.0"
description = "현재 날씨와 비 여부를 즉답합니다."

[permissions]
primitives = []
context = ["location"]
`, `
const ctx = JSON.parse(__context__);
return JSON.stringify({
  params: ctx.params && ctx.params.location,
  user: ctx.user && ctx.user.location
});
`)

	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	cfg := core.DefaultConfig()
	sess := &Session{
		Sandbox:        sandbox.New(cfg.Sandbox),
		Store:          st,
		Config:         &cfg,
		PackageManager: pm,
		BaseDir:        baseDir,
	}

	raw, err := runSkillOrPackageWithParams(context.Background(), "weather-now", sess, map[string]any{
		"location": map[string]any{
			"label": "강남역",
			"lat":   37.4979,
			"lon":   127.0276,
		},
	})
	if err != nil {
		t.Fatalf("runSkillOrPackageWithParams() error: %v", err)
	}
	var wrapper struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal([]byte(raw), &wrapper); err != nil {
		t.Fatalf("parse wrapper: %v (raw: %s)", err, raw)
	}
	var output struct {
		Params struct {
			Label string  `json:"label"`
			Lat   float64 `json:"lat"`
			Lon   float64 `json:"lon"`
		} `json:"params"`
		User struct {
			City string  `json:"city"`
			Lat  float64 `json:"lat"`
			Lon  float64 `json:"lon"`
		} `json:"user"`
	}
	if err := json.Unmarshal([]byte(wrapper.Output), &output); err != nil {
		t.Fatalf("parse output: %v (output: %s)", err, wrapper.Output)
	}
	if output.Params.Label != "강남역" || output.Params.Lat != 37.4979 || output.Params.Lon != 127.0276 {
		t.Fatalf("params.location = %+v, want structured 강남역", output.Params)
	}
	if output.User.City != "강남역" || output.User.Lat != 37.4979 || output.User.Lon != 127.0276 {
		t.Fatalf("user.location overlay = %+v, want structured 강남역", output.User)
	}
}

func TestE2ESkillRunPassesStructuredParams(t *testing.T) {
	skipWithoutRuntime(t)

	baseDir := t.TempDir()
	pm := installTestPackage(t, baseDir, `
[meta]
id = "echo-params"
name = "Echo Params"
version = "1.0.0"
description = "Echoes structured params."

[permissions]
primitives = []
context = ["location"]
`, `
const ctx = JSON.parse(__context__);
return JSON.stringify({
  params: ctx.params,
  user: ctx.user
});
`)

	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	cfg := core.DefaultConfig()
	sess := &Session{
		Sandbox:        sandbox.New(cfg.Sandbox),
		Store:          st,
		Config:         &cfg,
		PackageManager: pm,
		BaseDir:        baseDir,
	}

	nameArg, _ := json.Marshal("echo-params")
	paramsArg, _ := json.Marshal(map[string]any{
		"location": map[string]any{
			"label": "강남역",
			"lat":   37.4979,
			"lon":   127.0276,
		},
	})
	raw, err := resolveSkillCall(context.Background(), core.SkillCall{
		SkillName: "Skill",
		Method:    "run",
		Args:      []json.RawMessage{nameArg, paramsArg},
	}, sess, nil)
	if err != nil {
		t.Fatalf("resolveSkillCall() error: %v", err)
	}
	var wrapper struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal([]byte(raw), &wrapper); err != nil {
		t.Fatalf("parse wrapper: %v (raw: %s)", err, raw)
	}
	var got struct {
		Params struct {
			Location struct {
				Label string  `json:"label"`
				Lat   float64 `json:"lat"`
				Lon   float64 `json:"lon"`
			} `json:"location"`
		} `json:"params"`
		User struct {
			Location struct {
				City string  `json:"city"`
				Lat  float64 `json:"lat"`
				Lon  float64 `json:"lon"`
			} `json:"location"`
		} `json:"user"`
	}
	if err := json.Unmarshal([]byte(wrapper.Output), &got); err != nil {
		t.Fatalf("parse output: %v (output: %s)", err, wrapper.Output)
	}
	if got.Params.Location.Label != "강남역" || got.Params.Location.Lat != 37.4979 || got.Params.Location.Lon != 127.0276 {
		t.Fatalf("params.location = %+v, want structured 강남역", got.Params.Location)
	}
	if got.User.Location.City != "강남역" || got.User.Location.Lat != 37.4979 || got.User.Location.Lon != 127.0276 {
		t.Fatalf("user.location overlay = %+v, want structured 강남역", got.User.Location)
	}
}

// TestE2EWeatherBriefingLive hits the real Open-Meteo API (free, no key).
// Skipped unless KITTYPAW_E2E_LIVE=1 is set — requires network + LLM API key.
func TestE2EWeatherBriefingLive(t *testing.T) {
	if os.Getenv("KITTYPAW_E2E_LIVE") == "" {
		t.Skip("set KITTYPAW_E2E_LIVE=1 to run live E2E tests")
	}
	t.Log("Live E2E: kittypaw skill install weather-briefing && kittypaw chat '오늘 날씨 알려줘'")
	t.Log("This test requires a running server with a valid LLM API key.")
	t.Log("Run manually: ./kittypaw skill install weather-briefing && ./kittypaw chat '오늘 날씨 알려줘'")
}
