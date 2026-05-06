package main

import (
	"bufio"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
)

func TestModelAddAutoIDPreservesModelVersion(t *testing.T) {
	stubModelAddConnectionCheck(t, nil)
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))

	flags := &modelAddFlags{
		accountID: "alice",
		provider:  "anthropic",
		model:     "claude-haiku-4-5-20251001",
	}
	if err := runModelAdd(flags); err != nil {
		t.Fatalf("runModelAdd: %v", err)
	}

	cfg, err := core.LoadConfig(filepath.Join(root, "accounts", "alice", "config.toml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	got := cfg.FindModel("anthropic-claude-haiku-4-5-20251001")
	if got == nil {
		t.Fatalf("auto id model missing: %#v", cfg.LLM.Models)
	}
	if got.Provider != "anthropic" || got.Model != "claude-haiku-4-5-20251001" {
		t.Fatalf("model = %#v", got)
	}
	if got.Credential != "anthropic" {
		t.Fatalf("credential = %q, want anthropic", got.Credential)
	}
}

func TestModelAddKnownOpenAICompatibleKeepsProviderName(t *testing.T) {
	var checked core.ModelConfig
	stubModelAddConnectionCheck(t, func(_ context.Context, model core.ModelConfig) (bool, error) {
		checked = model
		return true, nil
	})
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))

	flags := &modelAddFlags{
		accountID: "alice",
		provider:  "groq",
		model:     "qwen/qwen3-32b",
		apiKey:    "sk-groq",
	}
	out := captureStdout(t, func() {
		if err := runModelAdd(flags); err != nil {
			t.Fatalf("runModelAdd: %v", err)
		}
	})
	if !strings.Contains(out, "Account: alice") {
		t.Fatalf("output %q missing account context", out)
	}

	cfg, err := core.LoadConfig(filepath.Join(root, "accounts", "alice", "config.toml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	got := cfg.FindModel("groq-qwen3-32b")
	if got == nil {
		t.Fatalf("groq model missing: %#v", cfg.LLM.Models)
	}
	if got.Provider != "groq" {
		t.Fatalf("provider = %q, want groq", got.Provider)
	}
	if got.BaseURL != "" {
		t.Fatalf("base_url = %q, want registry default", got.BaseURL)
	}
	if got.Credential != "groq" {
		t.Fatalf("credential = %q, want groq", got.Credential)
	}
	if checked.Provider != "groq" || checked.APIKey != "sk-groq" {
		t.Fatalf("connection check model = %#v, want groq with supplied api key", checked)
	}

	secrets, err := core.LoadAccountSecrets("alice")
	if err != nil {
		t.Fatalf("LoadAccountSecrets: %v", err)
	}
	if key, ok := secrets.Get("llm/groq", "api_key"); !ok || key != "sk-groq" {
		t.Fatalf("stored key = (%q, %v), want sk-groq", key, ok)
	}
}

func TestModelAddLlamaCppDoesNotRequireUserBaseURL(t *testing.T) {
	stubModelAddConnectionCheck(t, nil)
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))

	flags := &modelAddFlags{
		accountID: "alice",
		provider:  "llamacpp",
		model:     "qwen2.5",
	}
	if err := runModelAdd(flags); err != nil {
		t.Fatalf("runModelAdd: %v", err)
	}

	cfg, err := core.LoadConfig(filepath.Join(root, "accounts", "alice", "config.toml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	got := cfg.FindModel("llamacpp-qwen2.5")
	if got == nil {
		t.Fatalf("llamacpp model missing: %#v", cfg.LLM.Models)
	}
	if got.Provider != "llamacpp" {
		t.Fatalf("provider = %q, want llamacpp", got.Provider)
	}
	if got.BaseURL != "http://localhost:8080/v1/chat/completions" {
		t.Fatalf("base_url = %q, want llama.cpp default", got.BaseURL)
	}
	if got.Credential != "" {
		t.Fatalf("credential = %q, want no credential", got.Credential)
	}
}

func TestModelAddAutoIDCollisionAddsSuffix(t *testing.T) {
	stubModelAddConnectionCheck(t, nil)
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	mustWriteTestConfigWith(t, filepath.Join(root, "accounts", "alice", "config.toml"), func(cfg *core.Config) {
		cfg.LLM.Models = append(cfg.LLM.Models, core.ModelConfig{
			ID:         "groq-qwen3-32b",
			Provider:   "groq",
			Model:      "qwen/qwen3-32b",
			Credential: "groq",
			MaxTokens:  4096,
		})
	})

	flags := &modelAddFlags{
		accountID: "alice",
		provider:  "groq",
		model:     "qwen/qwen3-32b",
	}
	if err := runModelAdd(flags); err != nil {
		t.Fatalf("runModelAdd: %v", err)
	}

	cfg, err := core.LoadConfig(filepath.Join(root, "accounts", "alice", "config.toml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.FindModel("groq-qwen3-32b-2") == nil {
		t.Fatalf("expected suffixed id, got %#v", cfg.LLM.Models)
	}
}

func TestPromptModelAddIDConflictDefaultsToAddAsNew(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.LLM.Models = append(cfg.LLM.Models, core.ModelConfig{
		ID:         "gemini-gemini-3.1-pro-preview",
		Provider:   "gemini",
		Model:      "gemini-3.1-pro-preview",
		Credential: "gemini",
		MaxTokens:  4096,
	})
	flags := &modelAddFlags{
		provider: "gemini",
		model:    "gemini-3.1-pro-preview",
	}
	scanner := bufio.NewScanner(strings.NewReader("\n"))

	captureStdout(t, func() {
		if err := promptModelAddID(scanner, flags, &cfg); err != nil {
			t.Fatalf("promptModelAddID: %v", err)
		}
	})
	if flags.id != "gemini-gemini-3.1-pro-preview-2" {
		t.Fatalf("id = %q, want suffixed add-as-new id", flags.id)
	}
	if flags.replace {
		t.Fatal("replace = true, want false for add-as-new")
	}
}

func TestPromptModelAddIDConflictCanReplaceExisting(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.LLM.Models = append(cfg.LLM.Models, core.ModelConfig{
		ID:         "gemini-gemini-3.1-pro-preview",
		Provider:   "gemini",
		Model:      "gemini-3.1-pro-preview",
		Credential: "gemini",
		MaxTokens:  4096,
	})
	flags := &modelAddFlags{
		provider: "gemini",
		model:    "gemini-3.1-pro-preview",
	}
	scanner := bufio.NewScanner(strings.NewReader("1\n"))

	captureStdout(t, func() {
		if err := promptModelAddID(scanner, flags, &cfg); err != nil {
			t.Fatalf("promptModelAddID: %v", err)
		}
	})
	if flags.id != "gemini-gemini-3.1-pro-preview" {
		t.Fatalf("id = %q, want existing id", flags.id)
	}
	if !flags.replace {
		t.Fatal("replace = false, want true")
	}
}

func TestModelAddExplicitDuplicateRequiresReplace(t *testing.T) {
	stubModelAddConnectionCheck(t, nil)
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))

	flags := &modelAddFlags{
		accountID: "alice",
		id:        "main",
		provider:  "anthropic",
		model:     "claude-haiku-4-5-20251001",
	}
	err := runModelAdd(flags)
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("runModelAdd error = %v, want reserved id", err)
	}
}

func TestModelAddConnectionFailureDoesNotWriteInNonInteractiveMode(t *testing.T) {
	stubModelAddConnectionCheck(t, func(_ context.Context, _ core.ModelConfig) (bool, error) {
		return true, errors.New("invalid key")
	})
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))

	flags := &modelAddFlags{
		accountID: "alice",
		provider:  "gemini",
		model:     "gemini-3.1-pro-preview",
		apiKey:    "bad-key",
	}
	err := runModelAdd(flags)
	if err == nil || !strings.Contains(err.Error(), "connection check failed") {
		t.Fatalf("runModelAdd error = %v, want connection check failure", err)
	}

	cfg, err := core.LoadConfig(filepath.Join(root, "accounts", "alice", "config.toml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.FindModel("gemini-gemini-3.1-pro-preview") != nil {
		t.Fatalf("failed connection wrote model: %#v", cfg.LLM.Models)
	}
}

func TestModelAddAPIKeyPromptDecisionInteractiveReplaceAsksEvenWhenSecretExists(t *testing.T) {
	decision := modelAddAPIKeyPromptDecision{
		interactive: true,
		provider:    "gemini",
		hasSecret:   true,
		hasEnvKey:   false,
		replace:     true,
	}
	if !decision.shouldPrompt() {
		t.Fatal("replace flow should prompt so the user can enter a new key or keep existing")
	}
	if got := decision.prompt(); got != "API Key for gemini (Enter=keep existing): " {
		t.Fatalf("prompt = %q, want keep-existing prompt", got)
	}
}

func TestModelAddAPIKeyPromptDecisionNonInteractiveNeverPrompts(t *testing.T) {
	decision := modelAddAPIKeyPromptDecision{
		interactive: false,
		provider:    "gemini",
		hasSecret:   true,
		replace:     true,
	}
	if decision.shouldPrompt() {
		t.Fatal("non-interactive flow must not prompt")
	}
}

func TestRootCommandRegistersModelAdd(t *testing.T) {
	root := newRootCmd()
	modelCmd, _, err := root.Find([]string{"model", "add"})
	if err != nil {
		t.Fatalf("Find model add: %v", err)
	}
	if modelCmd == nil || modelCmd.Name() != "add" {
		t.Fatalf("model add command not registered: %#v", modelCmd)
	}
}

func TestModelAddProviderHelpListsAllSupportedProviders(t *testing.T) {
	cmd := newModelAddCmd()
	providerFlag := cmd.Flags().Lookup("provider")
	if providerFlag == nil {
		t.Fatal("provider flag missing")
	}
	for _, want := range []string{"deepseek", "cerebras"} {
		if !strings.Contains(providerFlag.Usage, want) {
			t.Fatalf("provider help %q missing %q", providerFlag.Usage, want)
		}
	}
}

func TestModelListPrintsAccountAndModels(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	mustWriteTestConfigWith(t, filepath.Join(root, "accounts", "alice", "config.toml"), func(cfg *core.Config) {
		cfg.LLM.Models = append(cfg.LLM.Models, core.ModelConfig{
			ID:         "groq-qwen3-32b",
			Provider:   "groq",
			Model:      "qwen/qwen3-32b",
			Credential: "groq",
			MaxTokens:  4096,
		})
	})

	out := captureStdout(t, func() {
		if err := runModelList(&modelListFlags{accountID: "alice"}); err != nil {
			t.Fatalf("runModelList: %v", err)
		}
	})
	for _, want := range []string{"Account: alice", "main", "groq-qwen3-32b", "groq", "qwen/qwen3-32b"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestModelRemoveDeletesNamedModel(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	mustWriteTestConfigWith(t, filepath.Join(root, "accounts", "alice", "config.toml"), func(cfg *core.Config) {
		cfg.LLM.Models = append(cfg.LLM.Models, core.ModelConfig{
			ID:         "gemini-gemini-3.1-pro-preview-2",
			Provider:   "gemini",
			Model:      "gemini-3.1-pro-preview",
			Credential: "gemini",
			MaxTokens:  4096,
		})
	})

	out := captureStdout(t, func() {
		if err := runModelRemove(&modelRemoveFlags{accountID: "alice", id: "gemini-gemini-3.1-pro-preview-2", force: true}); err != nil {
			t.Fatalf("runModelRemove: %v", err)
		}
	})
	if !strings.Contains(out, "Account: alice") || !strings.Contains(out, "Removed model: gemini-gemini-3.1-pro-preview-2") {
		t.Fatalf("unexpected output:\n%s", out)
	}

	cfg, err := core.LoadConfig(filepath.Join(root, "accounts", "alice", "config.toml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.FindModel("gemini-gemini-3.1-pro-preview-2") != nil {
		t.Fatalf("model was not removed: %#v", cfg.LLM.Models)
	}
}

func TestModelRemoveRejectsMain(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))

	err := runModelRemove(&modelRemoveFlags{accountID: "alice", id: "main", force: true})
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("runModelRemove error = %v, want reserved main", err)
	}
}

func stubModelAddConnectionCheck(t *testing.T, fn func(context.Context, core.ModelConfig) (bool, error)) {
	t.Helper()
	old := modelAddConnectionCheck
	if fn == nil {
		fn = func(context.Context, core.ModelConfig) (bool, error) {
			return true, nil
		}
	}
	modelAddConnectionCheck = fn
	t.Cleanup(func() {
		modelAddConnectionCheck = old
	})
}
