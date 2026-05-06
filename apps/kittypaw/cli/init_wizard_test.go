package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
)

func TestSetupLLMProviderChoicesOrder(t *testing.T) {
	got := setupLLMProviderChoices()
	want := []string{"Anthropic (Claude)", "OpenAI", "Gemini", "OpenRouter", "Local (Ollama)"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("setupLLMProviderChoices() = %#v, want %#v", got, want)
	}
}

func TestSetupLLMDefaultIndex(t *testing.T) {
	cases := []struct {
		name string
		cfg  core.LLMConfig
		want int
	}{
		{"anthropic", core.LLMConfig{Provider: "anthropic"}, 1},
		{"openai", core.LLMConfig{Provider: "openai"}, 2},
		{"gemini", core.LLMConfig{Provider: "gemini"}, 3},
		{"openrouter", core.LLMConfig{Provider: "openai", BaseURL: core.OpenRouterBaseURL}, 4},
		{"local", core.LLMConfig{Provider: "openai", BaseURL: "http://localhost:11434/v1/chat/completions"}, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := setupLLMDefaultIndex(&core.Config{LLM: tc.cfg})
			if got != tc.want {
				t.Fatalf("setupLLMDefaultIndex(%+v) = %d, want %d", tc.cfg, got, tc.want)
			}
		})
	}
}

func TestSetupLLMModelChoices(t *testing.T) {
	if got := setupLLMModelChoices("anthropic")[0]; got != core.ClaudeDefaultModel {
		t.Fatalf("anthropic default model = %q, want %q", got, core.ClaudeDefaultModel)
	}
	if got := setupLLMModelChoices("openai")[0]; got != core.OpenAIDefaultModel {
		t.Fatalf("openai default model = %q, want %q", got, core.OpenAIDefaultModel)
	}
	if got := setupLLMModelChoices("gemini")[0]; got != core.GeminiDefaultModel {
		t.Fatalf("gemini default model = %q, want %q", got, core.GeminiDefaultModel)
	}
}

func TestParseSetupExtraModelSpec(t *testing.T) {
	got, err := parseSetupExtraModelSpec("id=groq-qwen,provider=openai,model=qwen/qwen3-32b,base_url=https://api.groq.com/openai/v1/chat/completions")
	if err != nil {
		t.Fatalf("parseSetupExtraModelSpec: %v", err)
	}
	if got.ID != "groq-qwen" || got.Provider != "openai" || got.Model != "qwen/qwen3-32b" {
		t.Fatalf("model = %#v", got)
	}
	if got.BaseURL != "https://api.groq.com/openai/v1/chat/completions" {
		t.Fatalf("BaseURL = %q", got.BaseURL)
	}
	if got.Credential != "groq-qwen" {
		t.Fatalf("Credential = %q, want groq-qwen for custom base_url", got.Credential)
	}
}

func TestParseSetupExtraModelSpecAllowsColonInModel(t *testing.T) {
	got, err := parseSetupExtraModelSpec("id=openrouter-qwen,provider=openrouter,model=qwen/qwen3-235b-a22b:free")
	if err != nil {
		t.Fatalf("parseSetupExtraModelSpec: %v", err)
	}
	if got.Model != "qwen/qwen3-235b-a22b:free" {
		t.Fatalf("Model = %q", got.Model)
	}
	if got.BaseURL != core.OpenRouterBaseURL || got.Credential != "openrouter" {
		t.Fatalf("openrouter model = %#v", got)
	}
}

func TestParseSetupExtraModelSpecKeepsCustomOpenRouterModel(t *testing.T) {
	got, err := parseSetupExtraModelSpec("id=openrouter-llama,provider=openrouter,model=meta-llama/llama-3.3-70b-instruct")
	if err != nil {
		t.Fatalf("parseSetupExtraModelSpec: %v", err)
	}
	if got.Model != "meta-llama/llama-3.3-70b-instruct" {
		t.Fatalf("Model = %q", got.Model)
	}
	if got.Provider != "openai" || got.BaseURL != core.OpenRouterBaseURL || got.Credential != "openrouter" {
		t.Fatalf("openrouter model = %#v", got)
	}
}

func TestRunNonInteractiveAddsExtraModels(t *testing.T) {
	w, err := runNonInteractive(setupFlags{
		provider:       "anthropic",
		apiKey:         "sk-test",
		extraModels:    []string{"id=openai-fast,provider=openai,model=gpt-5.5"},
		extraModelKeys: []string{"openai-fast=sk-openai"},
	})
	if err != nil {
		t.Fatalf("runNonInteractive: %v", err)
	}
	if len(w.LLMExtraModels) != 1 {
		t.Fatalf("LLMExtraModels len = %d", len(w.LLMExtraModels))
	}
	if got := w.LLMExtraModels[0]; got.ID != "openai-fast" || got.Provider != "openai" || got.Credential != "openai" {
		t.Fatalf("extra model = %#v", got)
	}
	if w.LLMExtraAPIKeys["openai-fast"] != "sk-openai" {
		t.Fatalf("extra model key not captured: %#v", w.LLMExtraAPIKeys)
	}
}

func TestRunNonInteractiveRejectsUnknownExtraModelAPIKeyID(t *testing.T) {
	_, err := runNonInteractive(setupFlags{
		provider:       "anthropic",
		apiKey:         "sk-test",
		extraModels:    []string{"id=openai-fast,provider=openai,model=gpt-5.5"},
		extraModelKeys: []string{"openai-fsat=sk-openai"},
	})
	if err == nil {
		t.Fatal("expected unknown extra model key error")
	}
	if !strings.Contains(err.Error(), "unknown extra model id") {
		t.Fatalf("error = %q", err)
	}
}

func TestReadPasswordMaskedLoopCtrlCAborts(t *testing.T) {
	input := []byte{3}
	pos := 0

	got, err := readPasswordMaskedLoop(func(p []byte) (int, error) {
		if pos >= len(input) {
			return 0, io.EOF
		}
		p[0] = input[pos]
		pos++
		return 1, nil
	}, io.Discard)

	if !errors.Is(err, errPromptCanceled) {
		t.Fatalf("err = %v, want errPromptCanceled", err)
	}
	if got != "" {
		t.Fatalf("password = %q, want empty", got)
	}
}

func TestReadPasswordMaskedLoopEmptyEnterAllowsRetry(t *testing.T) {
	input := []byte{'\n'}
	pos := 0

	got, err := readPasswordMaskedLoop(func(p []byte) (int, error) {
		if pos >= len(input) {
			return 0, io.EOF
		}
		p[0] = input[pos]
		pos++
		return 1, nil
	}, io.Discard)

	if err != nil {
		t.Fatalf("err = %v, want nil for empty Enter", err)
	}
	if got != "" {
		t.Fatalf("password = %q, want empty", got)
	}
}

func TestWizardLLMDoesNotPrintAPIKeyPreview(t *testing.T) {
	oldCheck := wizardLLMConnectionCheck
	wizardLLMConnectionCheck = func(context.Context, core.LLMConfig) (bool, error) {
		return true, nil
	}
	t.Cleanup(func() { wizardLLMConnectionCheck = oldCheck })

	withTestStdin(t, "sk-proj-abcdefghijklmnopqrstuvwxyz1234\n", func() {
		scanner := bufio.NewScanner(strings.NewReader("2\n\n"))
		var w core.WizardResult

		out := captureStdout(t, func() {
			if err := wizardLLM(scanner, nil, &w); err != nil {
				t.Fatalf("wizardLLM: %v", err)
			}
		})

		assertNoSecretFragment(t, out, "sk-pro", "1234")
	})
}

func TestWizardLLMReconfigureExplainsDefaultModelOnlyAndKeepsExtraModels(t *testing.T) {
	oldCheck := wizardLLMConnectionCheck
	wizardLLMConnectionCheck = func(context.Context, core.LLMConfig) (bool, error) {
		return true, nil
	}
	t.Cleanup(func() { wizardLLMConnectionCheck = oldCheck })

	existing := core.DefaultConfig()
	existing.LLM.Provider = "openai"
	existing.LLM.Model = core.OpenAIDefaultModel
	existing.LLM.APIKey = "sk-existing"
	existing.LLM.Default = "main"
	existing.LLM.Models = []core.ModelConfig{
		{ID: "main", Provider: "openai", Model: core.OpenAIDefaultModel, Credential: "openai"},
		{ID: "opus", Provider: "anthropic", Model: "claude-opus-4-7", Credential: "anthropic"},
		{ID: "haiku", Provider: "anthropic", Model: "claude-haiku-4-5-20251001", Credential: "anthropic"},
	}

	withTestStdin(t, "\n", func() {
		scanner := bufio.NewScanner(strings.NewReader("y\n2\n\n"))
		var w core.WizardResult

		out := captureStdout(t, func() {
			if err := wizardLLM(scanner, &existing, &w); err != nil {
				t.Fatalf("wizardLLM: %v", err)
			}
		})

		for _, want := range []string{
			"Default model configured: openai (gpt-5.5)",
			"Extra models kept: opus, haiku",
			"Change default model?",
			"This changes only the default `main` model.",
			"API Key (Enter=keep existing):",
			"(keeping existing OpenAI key)",
		} {
			if !strings.Contains(out, want) {
				t.Fatalf("output missing %q:\n%s", want, out)
			}
		}
	})
}

func TestWizardLLMConnectionErrorDoesNotPrintAPIKeyFragments(t *testing.T) {
	oldCheck := wizardLLMConnectionCheck
	wizardLLMConnectionCheck = func(context.Context, core.LLMConfig) (bool, error) {
		return true, errors.New("provider rejected sk-proj-abcdefghijklmnopqrstuvwxyz1234 (sk-pro...1234)")
	}
	t.Cleanup(func() { wizardLLMConnectionCheck = oldCheck })

	withTestStdin(t, "sk-proj-abcdefghijklmnopqrstuvwxyz1234\n", func() {
		scanner := bufio.NewScanner(strings.NewReader("2\n\n\n"))
		var w core.WizardResult

		out := captureStdout(t, func() {
			if err := wizardLLM(scanner, nil, &w); err != nil {
				t.Fatalf("wizardLLM: %v", err)
			}
		})

		assertNoSecretFragment(t, out, "sk-pro", "1234")
	})
}

func TestWizardTelegramDoesNotPrintBotTokenPreview(t *testing.T) {
	oldRun := runTelegramChatIDWizard
	runTelegramChatIDWizard = func(*bufio.Scanner, io.Writer, string, string) string {
		return "8172543364"
	}
	t.Cleanup(func() { runTelegramChatIDWizard = oldRun })

	withTestStdin(t, "123456:ABCDEFGHIJKLMNOPQRSTUVWXYZabcd1234\n", func() {
		scanner := bufio.NewScanner(strings.NewReader("y\n"))
		var w core.WizardResult

		out := captureStdout(t, func() {
			wizardTelegram(scanner, "alice", nil, &w)
		})

		assertNoSecretFragment(t, out, "123456", "1234")
	})
}

func TestWizardWebSearchDoesNotPrintFirecrawlKeyPreview(t *testing.T) {
	withTestStdin(t, "fc-abcdefghijklmnopqrstuvwxyz1234\n", func() {
		scanner := bufio.NewScanner(strings.NewReader("y\n"))
		var w core.WizardResult

		out := captureStdout(t, func() {
			wizardWebSearch(scanner, nil, &w)
		})

		assertNoSecretFragment(t, out, "fc-abc", "1234")
	})
}

func TestPromptTelegramChatIDDoesNotPrintDetectedChatID(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader(""))
	var stdout strings.Builder

	chatID := promptTelegramChatID(scanner, &stdout, "123:token", telegramPairingDeps{
		serverChatID: func(context.Context, string) (telegramPairingStatus, error) {
			return telegramPairingStatus{Status: "paired", ChatID: "8172543364", Source: "active_channel"}, nil
		},
	})

	if chatID != "8172543364" {
		t.Fatalf("chatID = %q, want detected id", chatID)
	}
	assertNoSecretFragment(t, stdout.String(), "817254", "3364")
}

func withTestStdin(t *testing.T, input string, fn func()) {
	t.Helper()
	old := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdin: %v", err)
	}
	if _, err := io.WriteString(w, input); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close stdin writer: %v", err)
	}
	os.Stdin = r
	defer func() {
		os.Stdin = old
		if err := r.Close(); err != nil {
			t.Fatalf("close stdin reader: %v", err)
		}
	}()
	fn()
}

func assertNoSecretFragment(t *testing.T, output string, fragments ...string) {
	t.Helper()
	for _, fragment := range fragments {
		if strings.Contains(output, fragment) {
			t.Fatalf("output leaked secret fragment %q:\n%s", fragment, output)
		}
	}
}
