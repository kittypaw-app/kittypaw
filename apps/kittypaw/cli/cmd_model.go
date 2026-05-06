package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/llm"
)

type modelAddFlags struct {
	accountID string
	id        string
	provider  string
	model     string
	baseURL   string
	apiKey    string
	replace   bool
}

type modelListFlags struct {
	accountID string
}

type modelRemoveFlags struct {
	accountID string
	id        string
	force     bool
}

const modelAddConnectionTimeout = 20 * time.Second

func newModelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "model",
		Short: "Manage named LLM models",
	}
	cmd.AddCommand(
		newModelAddCmd(),
		newModelListCmd(),
		newModelRemoveCmd(),
	)
	return cmd
}

func newModelListCmd() *cobra.Command {
	flags := &modelListFlags{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List configured models for an account",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runModelList(flags)
		},
	}
	cmd.Flags().StringVar(&flags.accountID, "account", "", "local account id")
	return cmd
}

func newModelRemoveCmd() *cobra.Command {
	flags := &modelRemoveFlags{}
	cmd := &cobra.Command{
		Use:   "remove <id>",
		Short: "Remove a named model from an account",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.id = args[0]
			return runModelRemove(flags)
		},
	}
	cmd.Flags().StringVar(&flags.accountID, "account", "", "local account id")
	cmd.Flags().BoolVar(&flags.force, "force", false, "remove without confirmation")
	return cmd
}

func newModelAddCmd() *cobra.Command {
	flags := &modelAddFlags{}
	cmd := &cobra.Command{
		Use:   "add [id]",
		Short: "Add a named model for /model",
		Long: `Add a named model to the current account.

With no flags, KittyPaw prompts for provider and model, then generates a
stable ID from provider + model (for example:
anthropic-claude-haiku-4-5-20251001).

Known providers do not require --base-url. Use --base-url only for custom
endpoints or non-default local servers.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 && flags.id == "" {
				flags.id = args[0]
			}
			return runModelAdd(flags)
		},
	}
	cmd.Flags().StringVar(&flags.accountID, "account", "", "local account id")
	cmd.Flags().StringVar(&flags.id, "id", "", "model id (default: auto-generated from provider + model)")
	cmd.Flags().StringVar(&flags.provider, "provider", "", "API provider (anthropic|openai|gemini|openrouter|groq|mistral|ollama|lmstudio|llamacpp)")
	cmd.Flags().StringVar(&flags.model, "model", "", "provider model name")
	cmd.Flags().StringVar(&flags.baseURL, "base-url", "", "advanced: custom Chat Completions endpoint")
	cmd.Flags().StringVar(&flags.apiKey, "api-key", "", "API key for this provider (stored in account secrets; visible in ps)")
	cmd.Flags().BoolVar(&flags.replace, "replace", false, "replace an existing named model with the same --id")
	return cmd
}

func runModelAdd(flags *modelAddFlags) error {
	if flags == nil {
		flags = &modelAddFlags{}
	}
	accountID, cfg, cfgPath, err := loadModelCommandConfig(flags.accountID)
	if err != nil {
		return err
	}
	fmt.Printf("Account: %s\n", accountID)

	if flags.provider == "" || flags.model == "" {
		if !isTTY() {
			return fmt.Errorf("non-interactive model add requires --provider and --model")
		}
		if err := promptModelAdd(flags, cfg); err != nil {
			return err
		}
	}
	if flags.id == "" && isTTY() {
		scanner := bufio.NewScanner(os.Stdin)
		if err := promptModelAddID(scanner, flags, cfg); err != nil {
			return err
		}
	}

	model, autoID, err := buildModelAddConfig(flags, cfg)
	if err != nil {
		return err
	}
	if err := addModelToConfig(cfg, model, flags.replace, autoID); err != nil {
		return err
	}
	if strings.TrimSpace(flags.apiKey) == "" {
		prompt, ok := modelAddAPIKeyPrompt(accountID, model, flags.replace)
		if ok {
			key, err := promptPassword(prompt)
			if err != nil {
				return fmt.Errorf("read API key: %w", err)
			}
			flags.apiKey = key
		}
	}
	if err := checkModelAddConnection(accountID, model, strings.TrimSpace(flags.apiKey)); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		return fmt.Errorf("create account dir: %w", err)
	}
	if err := core.WriteConfigAtomic(cfg, cfgPath); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if strings.TrimSpace(flags.apiKey) != "" {
		secrets, err := core.LoadAccountSecrets(accountID)
		if err != nil {
			return fmt.Errorf("load account secrets: %w", err)
		}
		if err := secrets.Set("llm/"+model.SecretID(), "api_key", strings.TrimSpace(flags.apiKey)); err != nil {
			return fmt.Errorf("save model api key: %w", err)
		}
	}

	fmt.Printf("Added model: %s\n", model.ModelID())
	fmt.Printf("Provider: %s\n", model.Provider)
	fmt.Printf("Model: %s\n", model.Model)
	if model.BaseURL != "" {
		fmt.Printf("Base URL: %s\n", model.BaseURL)
	}
	fmt.Printf("Use in chat: /model %s\n", model.ModelID())
	maybeReloadServerAfterModelAdd(os.Stdout, os.Stderr)
	return nil
}

func runModelList(flags *modelListFlags) error {
	if flags == nil {
		flags = &modelListFlags{}
	}
	accountID, cfg, _, err := loadModelCommandConfig(flags.accountID)
	if err != nil {
		return err
	}
	fmt.Printf("Account: %s\n", accountID)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tProvider\tModel\tDefault")
	for _, model := range cfg.LLM.Models {
		def := ""
		if model.ModelID() == cfg.LLM.Default || (cfg.LLM.Default == "" && model.ModelID() == "main") {
			def = "yes"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", model.ModelID(), model.Provider, model.Model, def)
	}
	return w.Flush()
}

func runModelRemove(flags *modelRemoveFlags) error {
	if flags == nil {
		flags = &modelRemoveFlags{}
	}
	id := strings.TrimSpace(flags.id)
	if id == "" {
		return fmt.Errorf("model id is required")
	}
	if id == "main" {
		return fmt.Errorf("model id %q is reserved for the setup default model", id)
	}
	accountID, cfg, cfgPath, err := loadModelCommandConfig(flags.accountID)
	if err != nil {
		return err
	}
	fmt.Printf("Account: %s\n", accountID)

	idx := -1
	for i := range cfg.LLM.Models {
		if cfg.LLM.Models[i].ModelID() == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("model id %q not found", id)
	}
	if !flags.force && isTTY() {
		scanner := bufio.NewScanner(os.Stdin)
		if !promptYesNo(scanner, fmt.Sprintf("Remove model %s?", id), false) {
			return fmt.Errorf("aborted by user")
		}
	}
	cfg.LLM.Models = append(cfg.LLM.Models[:idx], cfg.LLM.Models[idx+1:]...)
	if err := core.WriteConfigAtomic(cfg, cfgPath); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	fmt.Printf("Removed model: %s\n", id)
	maybeReloadServerAfterModelAdd(os.Stdout, os.Stderr)
	return nil
}

func loadModelCommandConfig(accountFlag string) (string, *core.Config, string, error) {
	cfgDir, err := core.ConfigDir()
	if err != nil {
		return "", nil, "", err
	}
	accountID, err := resolveSetupAccount(setupFlags{accountID: accountFlag, provider: "model"}, cfgDir)
	if err != nil {
		return "", nil, "", err
	}
	cfgPath, err := core.ConfigPathForAccount(accountID)
	if err != nil {
		return "", nil, "", err
	}
	cfg, err := core.LoadConfig(cfgPath)
	if err != nil {
		return "", nil, "", fmt.Errorf("load account config: %w (run `kittypaw setup --account %s` first)", err, accountID)
	}
	return accountID, cfg, cfgPath, nil
}

func checkModelAddConnection(accountID string, model core.ModelConfig, apiKey string) error {
	runtimeModel := model
	if apiKey != "" {
		runtimeModel.APIKey = apiKey
	} else if secrets, err := core.LoadAccountSecrets(accountID); err == nil {
		runtimeModel = core.HydrateModelSecrets(runtimeModel, secrets)
	}

	fmt.Print("Connecting... ")
	ctx, cancel := context.WithTimeout(context.Background(), modelAddConnectionTimeout)
	defer cancel()
	_, err := modelAddConnectionCheck(ctx, runtimeModel)
	if err == nil {
		fmt.Printf("%s %s OK\n", runtimeModel.Provider, runtimeModel.Model)
		return nil
	}

	msg := redactSecretText(err.Error(), apiKey)
	fmt.Printf("FAIL (%s)\n", msg)
	if !isTTY() {
		return fmt.Errorf("connection check failed: %w", err)
	}
	prompt := "Connection failed. Save anyway?"
	scanner := bufio.NewScanner(os.Stdin)
	if !promptYesNo(scanner, prompt, true) {
		return fmt.Errorf("aborted by user")
	}
	return nil
}

var modelAddConnectionCheck = func(ctx context.Context, model core.ModelConfig) (bool, error) {
	p, err := llm.NewProviderFromModelConfig(model)
	if err != nil {
		return false, err
	}
	_, err = p.Generate(ctx, []core.LlmMessage{{Role: core.RoleUser, Content: "hi"}})
	return modelAddProviderUsesAPIKey(model.Provider), err
}

func maybeReloadServerAfterModelAdd(stdout, stderr io.Writer) {
	s, err := defaultServerDial()
	if err != nil || s == nil || !s.IsRunning() {
		return
	}
	if err := s.Reload(); err != nil {
		_, _ = fmt.Fprintf(stderr, setupMsgReloadFailedFmt+"\n", err)
		return
	}
	_, _ = fmt.Fprintln(stdout, setupMsgReloaded)
}

func promptModelAdd(flags *modelAddFlags, cfg *core.Config) error {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println()
	fmt.Println("  [Model add]")
	if strings.TrimSpace(flags.provider) == "" {
		choices := modelAddProviderChoices()
		choice := promptChoice(scanner, "  Provider > ", choices, 1)
		flags.provider = modelAddProviderValue(choice)
	}
	if strings.TrimSpace(flags.model) == "" {
		modelChoices := modelAddModelChoices(flags.provider)
		if len(modelChoices) > 0 {
			choice := promptChoice(scanner, "  Model > ", modelChoices, 1)
			flags.model = modelChoices[choice-1]
		} else {
			flags.model = promptLine(scanner, "  Model", "")
		}
	}
	if strings.TrimSpace(flags.baseURL) == "" && modelAddNeedsBaseURL(flags.provider) {
		flags.baseURL = promptLine(scanner, "  Base URL", modelAddDefaultBaseURL(flags.provider))
	}
	return promptModelAddID(scanner, flags, cfg)
}

func promptModelAddID(scanner *bufio.Scanner, flags *modelAddFlags, cfg *core.Config) error {
	if strings.TrimSpace(flags.id) != "" {
		return nil
	}
	candidateProvider, err := canonicalModelAddProvider(flags.provider)
	if err != nil {
		return err
	}
	base := autoModelAddIDBase(candidateProvider, flags.model)
	if !modelAddIDExists(cfg, base) {
		flags.id = base
		fmt.Printf("  ID: %s\n", base)
		return nil
	}

	addID := nextModelAddID(cfg, base)
	fmt.Printf("  ID already exists: %s\n", base)
	choice := promptChoice(scanner, "  Existing model > ", []string{
		"Replace existing model",
		"Add as new: " + addID,
		"Cancel",
	}, 2)
	switch choice {
	case 1:
		flags.id = base
		flags.replace = true
	case 2:
		flags.id = addID
	case 3:
		return fmt.Errorf("aborted by user")
	}
	fmt.Printf("  ID: %s\n", flags.id)
	return nil
}

func buildModelAddConfig(flags *modelAddFlags, cfg *core.Config) (core.ModelConfig, bool, error) {
	provider, err := canonicalModelAddProvider(flags.provider)
	if err != nil {
		return core.ModelConfig{}, false, err
	}
	modelName := strings.TrimSpace(flags.model)
	if modelName == "" {
		return core.ModelConfig{}, false, fmt.Errorf("model is required")
	}
	baseURL := normalizeModelAddBaseURL(provider, flags.baseURL)
	id := strings.TrimSpace(flags.id)
	autoID := false
	if id == "" {
		baseID := autoModelAddIDBase(provider, modelName)
		if flags.replace && modelAddIDExists(cfg, baseID) {
			id = baseID
		} else {
			id = nextModelAddID(cfg, baseID)
			autoID = true
		}
	} else {
		id = strings.ToLower(id)
		if err := validateModelAddID(id); err != nil {
			return core.ModelConfig{}, false, err
		}
	}
	credential := modelAddCredential(provider, baseURL, id, strings.TrimSpace(flags.apiKey) != "")
	return core.ModelConfig{
		ID:         id,
		Provider:   provider,
		Model:      modelName,
		Credential: credential,
		BaseURL:    baseURL,
		MaxTokens:  4096,
	}, autoID, nil
}

func addModelToConfig(cfg *core.Config, model core.ModelConfig, replace, autoID bool) error {
	id := model.ModelID()
	if id == "main" {
		return fmt.Errorf("model id %q is reserved for the setup default model", id)
	}
	for i := range cfg.LLM.Models {
		if cfg.LLM.Models[i].ModelID() != id {
			continue
		}
		if autoID {
			return fmt.Errorf("auto-generated model id %q unexpectedly already exists", id)
		}
		if !replace {
			return fmt.Errorf("model id %q already exists; pass --replace to update it", id)
		}
		cfg.LLM.Models[i] = model
		return nil
	}
	cfg.LLM.Models = append(cfg.LLM.Models, model)
	return nil
}

func canonicalModelAddProvider(provider string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "anthropic", "claude":
		return "anthropic", nil
	case "openai", "gpt":
		return "openai", nil
	case "gemini", "google":
		return "gemini", nil
	case "openrouter":
		return "openrouter", nil
	case "groq":
		return "groq", nil
	case "mistral":
		return "mistral", nil
	case "deepseek":
		return "deepseek", nil
	case "cerebras":
		return "cerebras", nil
	case "local", "ollama":
		return "ollama", nil
	case "lmstudio", "lms":
		return "lmstudio", nil
	case "llamacpp", "llama.cpp", "llama-cpp":
		return "llamacpp", nil
	default:
		return "", fmt.Errorf("unknown provider %q", provider)
	}
}

func modelAddCredential(provider, baseURL, id string, hasAPIKey bool) string {
	switch provider {
	case "ollama", "lmstudio", "llamacpp":
		if hasAPIKey {
			return id
		}
		return ""
	case "openai":
		if baseURL != "" {
			return id
		}
	}
	return provider
}

func modelAddAPIKeyPrompt(accountID string, model core.ModelConfig, replace bool) (string, bool) {
	hasSecret := false
	secrets, err := core.LoadAccountSecrets(accountID)
	if err == nil {
		_, hasSecret = secrets.Get("llm/"+model.SecretID(), "api_key")
	}
	decision := modelAddAPIKeyPromptDecision{
		interactive: isTTY(),
		provider:    model.Provider,
		hasSecret:   hasSecret,
		hasEnvKey:   envModelAddAPIKey(model.Provider) != "",
		replace:     replace,
	}
	if !decision.shouldPrompt() {
		return "", false
	}
	return decision.prompt(), true
}

type modelAddAPIKeyPromptDecision struct {
	interactive bool
	provider    string
	hasSecret   bool
	hasEnvKey   bool
	replace     bool
}

func (d modelAddAPIKeyPromptDecision) shouldPrompt() bool {
	return d.interactive && modelAddProviderUsesAPIKey(d.provider)
}

func (d modelAddAPIKeyPromptDecision) prompt() string {
	switch {
	case d.hasSecret && d.replace:
		return fmt.Sprintf("API Key for %s (Enter=keep existing): ", d.provider)
	case d.hasSecret:
		return fmt.Sprintf("API Key for %s (Enter=use existing): ", d.provider)
	case d.hasEnvKey:
		return fmt.Sprintf("API Key for %s (Enter=use env): ", d.provider)
	default:
		return fmt.Sprintf("API Key for %s (Enter=skip/use env): ", d.provider)
	}
}

func modelAddProviderUsesAPIKey(provider string) bool {
	switch provider {
	case "anthropic", "openai", "gemini", "openrouter", "groq", "mistral", "deepseek", "cerebras":
		return true
	default:
		return false
	}
}

func envModelAddAPIKey(provider string) string {
	switch provider {
	case "anthropic":
		return os.Getenv("ANTHROPIC_API_KEY")
	case "openai":
		return os.Getenv("OPENAI_API_KEY")
	case "gemini":
		return os.Getenv("GEMINI_API_KEY")
	case "openrouter":
		return os.Getenv("OPENROUTER_API_KEY")
	case "groq":
		return os.Getenv("GROQ_API_KEY")
	case "mistral":
		return os.Getenv("MISTRAL_API_KEY")
	case "deepseek":
		return os.Getenv("DEEPSEEK_API_KEY")
	case "cerebras":
		return os.Getenv("CEREBRAS_API_KEY")
	default:
		return ""
	}
}

func normalizeModelAddBaseURL(provider, raw string) string {
	u := strings.TrimSpace(raw)
	if u == "" && provider == "llamacpp" {
		u = modelAddDefaultBaseURL(provider)
	}
	if u == "" {
		return ""
	}
	u = strings.TrimRight(u, "/")
	u = strings.TrimSuffix(u, "/chat/completions")
	return u + "/chat/completions"
}

func modelAddNeedsBaseURL(provider string) bool {
	provider, err := canonicalModelAddProvider(provider)
	return err == nil && provider == "llamacpp"
}

func modelAddDefaultBaseURL(provider string) string {
	switch strings.ToLower(provider) {
	case "llamacpp", "llama.cpp", "llama-cpp":
		return "http://localhost:8080/v1/chat/completions"
	default:
		return ""
	}
}

func autoModelAddIDBase(provider, modelName string) string {
	name := strings.TrimSpace(modelName)
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	return slugModelAddID(provider + "-" + name)
}

func nextModelAddID(cfg *core.Config, base string) string {
	if base == "" {
		base = "model"
	}
	candidate := base
	for i := 2; modelAddIDExists(cfg, candidate); i++ {
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
	return candidate
}

func modelAddIDExists(cfg *core.Config, id string) bool {
	if cfg == nil {
		return false
	}
	return cfg.FindModel(id) != nil
}

func slugModelAddID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.':
			b.WriteRune(r)
			prevDash = false
		case r == '-' || r == '_' || r == ':' || r == '/' || r == ' ':
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-.")
}

func validateModelAddID(id string) error {
	if id == "" {
		return fmt.Errorf("model id is required")
	}
	if id == "main" {
		return fmt.Errorf("model id %q is reserved for the setup default model", id)
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return fmt.Errorf("model id %q contains invalid character %q", id, r)
	}
	return nil
}

func modelAddProviderChoices() []string {
	return []string{
		"Anthropic (Claude)",
		"OpenAI",
		"Gemini",
		"OpenRouter",
		"Groq",
		"Mistral",
		"Ollama",
		"LM Studio",
		"llama.cpp",
		"DeepSeek",
		"Cerebras",
	}
}

func modelAddProviderValue(choice int) string {
	values := []string{"anthropic", "openai", "gemini", "openrouter", "groq", "mistral", "ollama", "lmstudio", "llamacpp", "deepseek", "cerebras"}
	if choice < 1 || choice > len(values) {
		return "anthropic"
	}
	return values[choice-1]
}

func modelAddModelChoices(provider string) []string {
	switch strings.ToLower(provider) {
	case "anthropic", "claude":
		return core.ClaudeModelChoices()
	case "openai", "gpt":
		return core.OpenAIModelChoices()
	case "gemini", "google":
		return core.GeminiModelChoices()
	default:
		return nil
	}
}
