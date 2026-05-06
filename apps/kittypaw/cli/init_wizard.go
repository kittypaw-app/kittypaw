package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/mattn/go-isatty"
	"golang.org/x/term"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/llm"
)

var errPromptCanceled = errors.New("prompt canceled")

// setupFlags holds the cobra flag values for `kittypaw setup`.
type setupFlags struct {
	accountID      string
	passwordStdin  bool
	validate       setupConfigValidator
	provider       string
	apiKey         string
	localURL       string
	localModel     string
	extraModels    []string
	extraModelKeys []string
	telegramToken  string
	telegramChatID string
	firecrawlKey   string
	workspace      string
	httpAccess     bool
	force          bool
	noChat         bool
	noService      bool
}

type setupConfigValidator func(core.WizardResult) error

// runWizard drives the 6-step interactive wizard or applies flags.
// Returns a WizardResult. Never writes files.
func runWizard(flags setupFlags, existing *core.Config) (core.WizardResult, error) {
	var w core.WizardResult
	accountID := flags.accountID

	// Non-interactive: populate from flags.
	if flags.provider != "" {
		return runNonInteractive(flags)
	}

	// TTY check: if not a terminal and no --provider flag, bail.
	if !isTTY() {
		return w, fmt.Errorf("not a terminal — use flags for non-interactive setup\n" +
			"  example: kittypaw setup --provider anthropic --api-key $ANTHROPIC_API_KEY\n" +
			"  run kittypaw setup --help for all options")
	}

	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println()
	fmt.Println("  KittyPaw — AI Automation Framework")
	fmt.Println()

	// [1/6] LLM
	if err := wizardLLM(scanner, existing, &w); err != nil {
		return w, err
	}

	// [2/6] Telegram
	wizardTelegram(scanner, flags.accountID, existing, &w)
	if err := validateWizardResult(flags.validate, w); err != nil {
		return w, err
	}

	// [3/6] KakaoTalk
	if err := wizardKakao(scanner, accountID, existing, &w, flags.validate); err != nil {
		return w, err
	}

	// [4/6] Web Search
	wizardWebSearch(scanner, existing, &w)

	// [5/6] Workspace & HTTP
	wizardWorkspaceHTTP(scanner, accountID, existing, &w)
	if err := validateWizardResult(flags.validate, w); err != nil {
		return w, err
	}

	// [6/6] KittyPaw API Server
	wizardAPIServer(scanner, accountID, &w)

	return w, nil
}

func validateWizardResult(validate setupConfigValidator, w core.WizardResult) error {
	if validate == nil {
		return nil
	}
	return validate(w)
}

func runNonInteractive(flags setupFlags) (core.WizardResult, error) {
	var w core.WizardResult

	provider, model, baseURL := core.ResolveLLMConfig(flags.provider, flags.localURL, flags.localModel)
	if provider == "" {
		return w, fmt.Errorf("unknown provider: %s", flags.provider)
	}
	w.LLMProvider = provider
	w.LLMModel = model
	w.LLMBaseURL = baseURL

	switch strings.ToLower(flags.provider) {
	case "local", "ollama":
		if flags.localModel == "" {
			return w, fmt.Errorf("--local-model is required for provider %q", flags.provider)
		}
	default:
		if flags.apiKey == "" {
			return w, fmt.Errorf("--api-key is required for provider %q", flags.provider)
		}
		w.LLMAPIKey = flags.apiKey
	}

	if flags.telegramToken != "" {
		if !core.ValidateTelegramToken(flags.telegramToken) {
			return w, fmt.Errorf("invalid telegram bot token format")
		}
		w.TelegramBotToken = flags.telegramToken
		w.TelegramChatID = flags.telegramChatID
	}

	if flags.workspace != "" {
		abs, err := filepath.Abs(flags.workspace)
		if err != nil {
			return w, fmt.Errorf("invalid workspace path: %w", err)
		}
		w.WorkspacePath = abs
	}

	w.FirecrawlKey = flags.firecrawlKey
	w.HTTPAccess = flags.httpAccess
	extraKeys, err := parseExtraModelAPIKeys(flags.extraModelKeys)
	if err != nil {
		return w, err
	}
	if len(extraKeys) > 0 {
		w.LLMExtraAPIKeys = extraKeys
	}
	for _, spec := range flags.extraModels {
		model, err := parseSetupExtraModelSpec(spec)
		if err != nil {
			return w, err
		}
		w.LLMExtraModels = append(w.LLMExtraModels, model)
	}
	if err := validateExtraModelAPIKeys(w.LLMExtraModels, extraKeys); err != nil {
		return w, err
	}
	return w, nil
}

func parseExtraModelAPIKeys(values []string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(values))
	for _, raw := range values {
		id, key, ok := strings.Cut(strings.TrimSpace(raw), "=")
		id = strings.TrimSpace(id)
		key = strings.TrimSpace(key)
		if !ok || id == "" || key == "" {
			return nil, fmt.Errorf("--extra-model-api-key must be id=api_key")
		}
		out[id] = key
	}
	return out, nil
}

func validateExtraModelAPIKeys(models []core.ModelConfig, keys map[string]string) error {
	if len(keys) == 0 {
		return nil
	}
	ids := make(map[string]bool, len(models))
	for _, model := range models {
		if id := model.ModelID(); id != "" {
			ids[id] = true
		}
	}
	for id := range keys {
		if !ids[id] {
			return fmt.Errorf("--extra-model-api-key references unknown extra model id %q", id)
		}
	}
	return nil
}

func parseSetupExtraModelSpec(spec string) (core.ModelConfig, error) {
	fields, err := parseExtraModelFields(spec)
	if err != nil {
		return core.ModelConfig{}, err
	}
	id := fields["id"]
	provider := fields["provider"]
	modelName := fields["model"]
	baseURL := fields["base_url"]
	if id == "" || provider == "" || modelName == "" {
		return core.ModelConfig{}, fmt.Errorf("--extra-model must include id, provider, and model")
	}
	if id == "main" {
		return core.ModelConfig{}, fmt.Errorf("--extra-model id %q is reserved for the setup default model", id)
	}
	resolvedProvider, model, resolvedBaseURL := core.ResolveLLMConfig(provider, baseURL, modelName)
	if strings.EqualFold(provider, "openrouter") {
		resolvedProvider = "openai"
		model = modelName
		resolvedBaseURL = core.OpenRouterBaseURL
	}
	if resolvedProvider == "" || model == "" {
		return core.ModelConfig{}, fmt.Errorf("--extra-model has unknown provider/model: %s", spec)
	}
	if baseURL != "" {
		resolvedBaseURL = strings.TrimRight(baseURL, "/")
		if !strings.HasSuffix(resolvedBaseURL, "/chat/completions") {
			resolvedBaseURL += "/chat/completions"
		}
	}
	credential := resolvedProvider
	if resolvedBaseURL == core.OpenRouterBaseURL {
		credential = "openrouter"
	} else if resolvedBaseURL != "" {
		credential = id
	}
	return core.ModelConfig{
		ID:         id,
		Provider:   resolvedProvider,
		Model:      model,
		Credential: credential,
		BaseURL:    resolvedBaseURL,
		MaxTokens:  4096,
	}, nil
}

func parseExtraModelFields(spec string) (map[string]string, error) {
	out := make(map[string]string)
	for _, part := range strings.Split(strings.TrimSpace(spec), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("--extra-model must use key=value fields")
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		switch k {
		case "id", "provider", "model", "base_url":
			out[k] = v
		default:
			return nil, fmt.Errorf("--extra-model unknown field %q", k)
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Step [1/5]: LLM
// ---------------------------------------------------------------------------

func wizardLLM(scanner *bufio.Scanner, existing *core.Config, w *core.WizardResult) error {
	fmt.Println("  [1/6] LLM Selection")

	// Detect existing LLM config.
	if existing != nil && existing.LLM.Provider != "" {
		name := existing.LLM.Provider
		if existing.LLM.Model != "" {
			name += " (" + existing.LLM.Model + ")"
		}
		fmt.Printf("  ✓ Default model configured: %s\n", name)
		if ids := setupExtraModelIDs(existing); len(ids) > 0 {
			fmt.Printf("  Extra models kept: %s\n", strings.Join(ids, ", "))
		}
		if !promptYesNo(scanner, "  > Change default model?", false) {
			fmt.Println("  (keeping existing default model)")
			return nil
		}
		fmt.Println("  This changes only the default `main` model.")
		fmt.Println("  Extra models stay unchanged. Add more named models with `kittypaw model add`.")
	}

	defaultIdx := setupLLMDefaultIndex(existing)

	choice := promptChoice(scanner, "  > ", setupLLMProviderChoices(), defaultIdx)

	var provider, apiKey, localURL, localModel string
	switch choice {
	case 1:
		provider = "anthropic"
		var err error
		apiKey, err = promptPassword(setupAPIKeyPrompt(existing, provider))
		if err != nil {
			return fmt.Errorf("read API key: %w", err)
		}
		if apiKey == "" && existing != nil && existing.LLM.Provider == "anthropic" {
			apiKey = existing.LLM.APIKey
			fmt.Println("  (keeping existing Anthropic key)")
		} else if apiKey != "" {
			fmt.Println("  ✓ API key received")
		}
		claudeModels := setupLLMModelChoices(provider)
		modelIdx := promptChoice(scanner, "  Model > ", claudeModels, 1)
		localModel = claudeModels[modelIdx-1]
	case 2:
		provider = "openai"
		var err error
		apiKey, err = promptPassword(setupAPIKeyPrompt(existing, provider))
		if err != nil {
			return fmt.Errorf("read API key: %w", err)
		}
		if apiKey == "" && existing != nil && existing.LLM.Provider == "openai" && existing.LLM.BaseURL == "" {
			apiKey = existing.LLM.APIKey
			fmt.Println("  (keeping existing OpenAI key)")
		} else if apiKey != "" {
			fmt.Println("  ✓ API key received")
		}
		openAIModels := setupLLMModelChoices(provider)
		modelIdx := promptChoice(scanner, "  Model > ", openAIModels, 1)
		localModel = openAIModels[modelIdx-1]
	case 3:
		provider = "gemini"
		var err error
		apiKey, err = promptPassword(setupAPIKeyPrompt(existing, provider))
		if err != nil {
			return fmt.Errorf("read API key: %w", err)
		}
		if apiKey == "" && existing != nil && existing.LLM.Provider == "gemini" {
			apiKey = existing.LLM.APIKey
			fmt.Println("  (keeping existing Gemini key)")
		} else if apiKey != "" {
			fmt.Println("  ✓ API key received")
		}
		geminiModels := setupLLMModelChoices(provider)
		modelIdx := promptChoice(scanner, "  Model > ", geminiModels, 1)
		localModel = geminiModels[modelIdx-1]
	case 4:
		provider = "openrouter"
		var err error
		apiKey, err = promptPassword(setupAPIKeyPrompt(existing, provider))
		if err != nil {
			return fmt.Errorf("read API key: %w", err)
		}
		if apiKey == "" && existing != nil && existing.LLM.BaseURL == core.OpenRouterBaseURL {
			apiKey = existing.LLM.APIKey
			fmt.Println("  (keeping existing OpenRouter key)")
		} else if apiKey != "" {
			fmt.Println("  ✓ API key received")
		}
	case 5:
		provider = "local"
		defURL := core.OllamaDefaultBaseURL
		if existing != nil && existing.LLM.BaseURL != "" && existing.LLM.BaseURL != core.OpenRouterBaseURL {
			defURL = strings.TrimSuffix(existing.LLM.BaseURL, "/chat/completions")
		}
		localURL = promptLine(scanner, "  URL", defURL)

		defModel := ""
		if existing != nil && existing.LLM.BaseURL != "" {
			defModel = existing.LLM.Model
		}
		localModel = promptLine(scanner, "  Model", defModel)
	}

	resolvedProvider, model, baseURL := core.ResolveLLMConfig(provider, localURL, localModel)
	w.LLMProvider = resolvedProvider
	w.LLMAPIKey = apiKey
	w.LLMModel = model
	w.LLMBaseURL = baseURL

	// Connection test.
	testCfg := core.LLMConfig{
		Provider:  resolvedProvider,
		APIKey:    apiKey,
		Model:     model,
		BaseURL:   baseURL,
		MaxTokens: 128,
	}
	fmt.Print("  Connecting... ")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	keyChecked, err := wizardLLMConnectionCheck(ctx, testCfg)
	if err != nil {
		fmt.Printf("FAIL (%s)\n", redactSecretText(err.Error(), apiKey))
		prompt := "  Save anyway?"
		if keyChecked {
			prompt = "  Key may be invalid. Save anyway?"
		}
		if !promptYesNo(scanner, prompt, true) {
			return fmt.Errorf("aborted by user")
		}
		return nil
	}
	elapsed := time.Since(start)
	fmt.Printf("%s %s OK (%dms)\n", resolvedProvider, model, elapsed.Milliseconds())

	return nil
}

func setupExtraModelIDs(existing *core.Config) []string {
	if existing == nil {
		return nil
	}
	var ids []string
	for _, model := range existing.LLM.Models {
		id := model.ModelID()
		if id == "" || id == "main" {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

func setupAPIKeyPrompt(existing *core.Config, provider string) string {
	if setupCanKeepExistingAPIKey(existing, provider) {
		return "  API Key (Enter=keep existing): "
	}
	return "  API Key: "
}

func setupCanKeepExistingAPIKey(existing *core.Config, provider string) bool {
	if existing == nil {
		return false
	}
	switch provider {
	case "anthropic":
		return existing.LLM.Provider == "anthropic"
	case "openai":
		return existing.LLM.Provider == "openai" && existing.LLM.BaseURL == ""
	case "gemini":
		return existing.LLM.Provider == "gemini"
	case "openrouter":
		return existing.LLM.BaseURL == core.OpenRouterBaseURL
	default:
		return false
	}
}

var wizardLLMConnectionCheck = func(ctx context.Context, cfg core.LLMConfig) (bool, error) {
	p, err := llm.NewProviderFromConfig(cfg)
	if err != nil {
		return false, err
	}
	_, err = p.Generate(ctx, []core.LlmMessage{{Role: core.RoleUser, Content: "hi"}})
	return true, err
}

func setupLLMProviderChoices() []string {
	return []string{"Anthropic (Claude)", "OpenAI", "Gemini", "OpenRouter", "Local (Ollama)"}
}

func setupLLMDefaultIndex(existing *core.Config) int {
	if existing == nil {
		return 1
	}
	switch {
	case existing.LLM.Provider == "anthropic" || existing.LLM.Provider == "claude":
		return 1
	case (existing.LLM.Provider == "openai" || existing.LLM.Provider == "gpt") && existing.LLM.BaseURL == "":
		return 2
	case existing.LLM.Provider == "gemini" || existing.LLM.Provider == "google":
		return 3
	case existing.LLM.BaseURL == core.OpenRouterBaseURL:
		return 4
	case existing.LLM.BaseURL != "":
		return 5
	default:
		return 1
	}
}

func setupLLMModelChoices(provider string) []string {
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

// ---------------------------------------------------------------------------
// Step [2/5]: Telegram
// ---------------------------------------------------------------------------

func wizardTelegram(scanner *bufio.Scanner, accountID string, existing *core.Config, w *core.WizardResult) {
	fmt.Println()
	fmt.Println("  [2/6] Telegram (optional)")

	// Detect existing Telegram config.
	var existingToken string
	var existingChatID string
	if existing != nil {
		for _, ch := range existing.Channels {
			if ch.ChannelType == core.ChannelTelegram && ch.Token != "" {
				existingToken = ch.Token
				break
			}
		}
		if len(existing.AllowedChatIDs) > 0 {
			existingChatID = existing.AllowedChatIDs[0]
		}
	}

	if existingToken != "" {
		msg := "  ✓ Already connected"
		fmt.Println(msg)
		if !promptYesNo(scanner, "  > Reconfigure?", false) {
			fmt.Println("  (keeping existing connection)")
			return
		}
	} else {
		if !promptYesNo(scanner, "  > Connect?", false) {
			return
		}
	}

	printBotFatherGuide()

	token, err := promptPassword("  Bot Token: ")
	if err != nil || token == "" {
		fmt.Println("  Skipping Telegram.")
		return
	}

	if !core.ValidateTelegramToken(token) {
		fmt.Println("  Invalid token format. Skipping.")
		return
	}

	fmt.Println("  ✓ Bot token received")
	w.TelegramBotToken = token
	if token == existingToken && existingChatID != "" {
		w.TelegramChatID = existingChatID
		fmt.Println("  Telegram connected ✓")
		return
	}
	w.TelegramChatID = runTelegramChatIDWizard(scanner, os.Stdout, accountID, token)
}

func printBotFatherGuide() {
	lang := detectLang()
	switch {
	case strings.HasPrefix(lang, "ko"):
		fmt.Println("  1. 텔레그램에서 @BotFather 를 검색하세요")
		fmt.Println("  2. /newbot 을 보내고 안내에 따라 봇을 만드세요")
		fmt.Println("  3. BotFather가 발급한 토큰을 아래에 붙여넣으세요")
		fmt.Println()
	case strings.HasPrefix(lang, "ja"):
		fmt.Println("  1. Telegramで @BotFather を検索してください")
		fmt.Println("  2. /newbot を送信し、案内に従ってボットを作成してください")
		fmt.Println("  3. BotFatherが発行したトークンを下に貼り付けてください")
		fmt.Println()
	default:
		fmt.Println("  1. Search for @BotFather in Telegram")
		fmt.Println("  2. Send /newbot and follow the prompts to create a bot")
		fmt.Println("  3. Paste the token BotFather gives you below")
		fmt.Println()
	}
}

func detectLang() string {
	lang := os.Getenv("LANG")
	if lang == "" {
		lang = os.Getenv("LC_ALL")
	}
	return strings.ToLower(lang)
}

// ---------------------------------------------------------------------------
// Step [3/6]: KakaoTalk
// ---------------------------------------------------------------------------

func wizardKakao(scanner *bufio.Scanner, accountID string, existing *core.Config, w *core.WizardResult, validate setupConfigValidator) error {
	fmt.Println()
	fmt.Println("  [3/6] KakaoTalk (optional)")

	hasExisting := false
	if existing != nil {
		for _, ch := range existing.Channels {
			if ch.ChannelType == core.ChannelKakaoTalk {
				hasExisting = true
				break
			}
		}
	}

	if hasExisting {
		fmt.Println("  ✓ Already enabled")
		if !promptYesNo(scanner, "  > Reconfigure?", false) {
			fmt.Println("  (keeping existing KakaoTalk connection)")
			return nil
		}
	} else if !promptYesNo(scanner, "  > Enable?", false) {
		return nil
	}

	apiURL := core.DefaultAPIServerURL
	candidate := *w
	candidate.APIServerURL = apiURL
	candidate.KakaoEnabled = true
	if err := validateWizardResult(validate, candidate); err != nil {
		return err
	}

	result, err := runKakaoPairingWizard(os.Stdout)
	if err != nil {
		printKakaoPairingSkipped(err)
		return nil
	}

	w.APIServerURL = result.APIServerURL
	w.KakaoEnabled = result.KakaoEnabled
	w.KakaoRelayWSURL = result.KakaoRelayWSURL
	return nil
}

func printKakaoPairingSkipped(err error) {
	switch {
	case errors.Is(err, errKakaoRelayUnavailable):
		fmt.Println("  Discovery 응답에 kakao_relay_url이 없어 페어링을 건너뜁니다.")
	case strings.Contains(err.Error(), "fetch kakao discovery"):
		fmt.Printf("  Discovery 실패: %v\n", err)
		fmt.Println("  KakaoTalk 활성화를 건너뜁니다.")
	case strings.Contains(err.Error(), "register kakao relay"):
		fmt.Printf("  릴레이 등록 실패: %v\n", err)
		fmt.Println("  KakaoTalk 활성화를 건너뜁니다. 네트워크 확인 후 재실행하세요.")
	default:
		fmt.Printf("  KakaoTalk 활성화 실패: %v\n", err)
		fmt.Println("  KakaoTalk 활성화를 건너뜁니다.")
	}
}

func copyToClipboard(text string) error {
	switch runtime.GOOS {
	case "darwin":
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(text)
		return cmd.Run()
	case "linux":
		cmd := exec.Command("xclip", "-selection", "clipboard")
		cmd.Stdin = strings.NewReader(text)
		return cmd.Run()
	default:
		return fmt.Errorf("unsupported")
	}
}

// ---------------------------------------------------------------------------
// Step [4/5]: Web Search
// ---------------------------------------------------------------------------

func wizardWebSearch(scanner *bufio.Scanner, existing *core.Config, w *core.WizardResult) {
	fmt.Println()
	fmt.Println("  [4/6] Web Search (optional)")

	hasExisting := existing != nil && existing.Web.FirecrawlKey != ""
	if hasExisting {
		fmt.Println("  ✓ Firecrawl configured")
		if !promptYesNo(scanner, "  > Reconfigure?", false) {
			fmt.Println("  (keeping existing Firecrawl key)")
			return
		}
	} else {
		fmt.Println("  Default: DuckDuckGo (free, no API key)")
		fmt.Println("  Upgrade: Firecrawl (higher quality search results)")
		if !promptYesNo(scanner, "  > Configure Firecrawl?", false) {
			return
		}
	}

	key, err := promptPassword("  Firecrawl API Key: ")
	if err != nil || key == "" {
		if hasExisting {
			fmt.Println("  (keeping existing key)")
		} else {
			fmt.Println("  Skipping — using DuckDuckGo.")
		}
		return
	}

	fmt.Println("  ✓ Firecrawl key received")
	w.FirecrawlKey = key
}

// ---------------------------------------------------------------------------
// Step [5/5]: Workspace & HTTP
// ---------------------------------------------------------------------------

func wizardWorkspaceHTTP(scanner *bufio.Scanner, accountID string, existing *core.Config, w *core.WizardResult) {
	fmt.Println()
	fmt.Println("  [5/6] Workspace & Permissions")

	defWS := ""
	if existing != nil && len(existing.Sandbox.AllowedPaths) > 0 {
		defWS = existing.Sandbox.AllowedPaths[0]
	} else if p, err := core.DefaultWorkspacePath(accountID); err == nil {
		defWS = p
	}
	ws := promptLine(scanner, "  Workspace path (empty=skip)", defWS)
	if ws != "" {
		abs, err := filepath.Abs(ws)
		if err == nil {
			ws = abs
		}
		if info, err := os.Stat(ws); err != nil || !info.IsDir() {
			if promptYesNo(scanner, fmt.Sprintf("  %s does not exist. Create?", ws), true) {
				if err := os.MkdirAll(ws, 0o755); err != nil {
					fmt.Printf("  Failed to create: %v\n", err)
					ws = ""
				}
			} else {
				ws = ""
			}
		}
		w.WorkspacePath = ws
	}

	w.HTTPAccess = promptYesNo(scanner, "  Allow HTTP access?", true)
}

// ---------------------------------------------------------------------------
// Step [6/6]: KittyPaw API Server
// ---------------------------------------------------------------------------

func wizardAPIServer(scanner *bufio.Scanner, accountID string, w *core.WizardResult) {
	fmt.Println()
	fmt.Println("  [6/6] KittyPaw API Server (optional)")
	fmt.Println("  KittyPaw 는 사용자 편의를 위해 몇 가지 API 를 제공하고 있습니다.")
	fmt.Println("  대기질·날씨 등 인증이 필요한 스킬을 사용하시려면 로그인 해주세요.")

	secrets, err := core.LoadAccountSecrets(accountID)
	if err != nil {
		fmt.Printf("  secrets 로드 실패: %v\n", err)
		return
	}
	mgr := core.NewAPITokenManager("", secrets)
	apiURL := core.DefaultAPIServerURL

	// Already logged in? LoadAccessToken auto-refreshes an expired token using
	// the stored refresh_token, so a live refresh session counts as "logged in".
	if tok, err := mgr.LoadAccessToken(apiURL); err == nil && tok != "" {
		fmt.Printf("  ✓ Logged in (%s)\n", apiURL)
		_ = applyDiscovery(apiURL, mgr)
		maybePairChatRelayDevice(apiURL, mgr, tok, os.Stderr)
		w.APIServerURL = apiURL
		return
	}

	if !promptYesNo(scanner, "  > Login?", true) {
		return
	}

	if isTTY() {
		err = loginHTTP(apiURL, mgr)
	} else {
		err = loginCode(apiURL, mgr)
	}
	if err != nil {
		fmt.Printf("  로그인 실패: %v\n", err)
		return
	}

	w.APIServerURL = apiURL
}

// ---------------------------------------------------------------------------
// Input helpers
// ---------------------------------------------------------------------------

func isTTY() bool {
	return isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
}

// promptLine reads a single line. Shows defaultVal in brackets; Enter returns it.
func promptLine(scanner *bufio.Scanner, prompt, defaultVal string) string {
	if defaultVal != "" {
		fmt.Printf("%s [%s]: ", prompt, defaultVal)
	} else {
		fmt.Printf("%s: ", prompt)
	}
	if !scanner.Scan() {
		return defaultVal
	}
	line := strings.TrimSpace(scanner.Text())
	if line == "" {
		return defaultVal
	}
	return line
}

// promptPassword reads input showing * for each character typed.
func promptPassword(prompt string) (string, error) {
	fmt.Print(prompt)
	if !isTTY() {
		// Fallback for non-TTY (piped input).
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() {
			return "", nil
		}
		return strings.TrimSpace(scanner.Text()), nil
	}

	fd := int(syscall.Stdin)
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		// Fall back to hidden input if raw mode fails.
		b, err := term.ReadPassword(fd)
		fmt.Println()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}
	defer term.Restore(fd, oldState) //nolint:errcheck

	password, err := readPasswordMaskedLoop(os.Stdin.Read, os.Stdout)
	return strings.TrimSpace(password), err
}

func readPasswordMaskedLoop(read func([]byte) (int, error), stdout io.Writer) (string, error) {
	var buf []byte
	var b [1]byte
	for {
		n, err := read(b[:])
		if err != nil {
			_, _ = fmt.Fprint(stdout, "\r\n")
			if err == io.EOF {
				return strings.TrimSpace(string(buf)), nil
			}
			return "", err
		}
		if n == 0 {
			_, _ = fmt.Fprint(stdout, "\r\n")
			return strings.TrimSpace(string(buf)), nil
		}
		switch b[0] {
		case '\r', '\n': // Enter
			_, _ = fmt.Fprint(stdout, "\r\n")
			return strings.TrimSpace(string(buf)), nil
		case 3: // Ctrl+C
			_, _ = fmt.Fprint(stdout, "\r\n")
			return "", errPromptCanceled
		case 127, 8: // DEL, Backspace
			if len(buf) > 0 {
				buf = buf[:len(buf)-1]
				_, _ = fmt.Fprint(stdout, "\b \b")
			}
		default:
			if b[0] >= 32 { // printable
				buf = append(buf, b[0])
				_, _ = fmt.Fprint(stdout, "*")
			}
		}
	}
}

// maskKey returns a masked version of an API key, e.g. "sk-ant-...x2f4".
func maskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:6] + "..." + key[len(key)-4:]
}

func redactSecretText(text string, secrets ...string) string {
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if secret == "" {
			continue
		}
		text = strings.ReplaceAll(text, secret, "[redacted]")
		if masked := maskKey(secret); masked != "****" {
			text = strings.ReplaceAll(text, masked, "[redacted]")
		}
		if len(secret) > 8 {
			text = strings.ReplaceAll(text, secret[:6], "[redacted]")
			text = strings.ReplaceAll(text, secret[len(secret)-4:], "[redacted]")
		}
	}
	return text
}

// promptYesNo asks a yes/no question. defaultYes controls Enter behavior.
func promptYesNo(scanner *bufio.Scanner, prompt string, defaultYes bool) bool {
	hint := "(y/N)"
	if defaultYes {
		hint = "(Y/n)"
	}
	fmt.Printf("%s %s: ", prompt, hint)
	if !scanner.Scan() {
		return defaultYes
	}
	ans := strings.ToLower(strings.TrimSpace(scanner.Text()))
	switch ans {
	case "y", "yes":
		return true
	case "n", "no":
		return false
	default:
		return defaultYes
	}
}

// promptChoice presents numbered options and returns a 1-indexed selection.
func promptChoice(scanner *bufio.Scanner, prompt string, options []string, defaultIdx int) int {
	defaultIdx = normalizeChoiceIndex(defaultIdx, len(options))
	if defaultIdx == 0 {
		return 0
	}
	if isTTY() {
		if idx, ok := promptChoiceInteractive(prompt, options, defaultIdx); ok {
			return idx
		}
	}

	renderChoiceBlock(prompt, options, defaultIdx, false)
	if !scanner.Scan() {
		return defaultIdx
	}
	return choiceFromInput(scanner.Text(), defaultIdx, len(options))
}

func normalizeChoiceIndex(idx, optionCount int) int {
	if optionCount <= 0 {
		return 0
	}
	if idx < 1 {
		return 1
	}
	if idx > optionCount {
		return optionCount
	}
	return idx
}

func renderChoiceBlock(prompt string, options []string, selectedIdx int, clear bool) {
	lineEnd := "\n"
	if clear {
		lineEnd = "\r\n"
	}
	for i, opt := range options {
		if clear {
			fmt.Print("\r\033[2K")
		}
		marker := "  "
		if i+1 == selectedIdx {
			marker = "> "
		}
		fmt.Printf("  %s(%d) %s%s", marker, i+1, opt, lineEnd)
	}
	if clear {
		fmt.Print("\r\033[2K")
	}
	fmt.Printf("%sSelect [%d]: ", prompt, selectedIdx)
}

func choiceFromInput(line string, defaultIdx, optionCount int) int {
	selected := normalizeChoiceIndex(defaultIdx, optionCount)
	if selected == 0 {
		return 0
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return selected
	}
	var idx int
	if _, err := fmt.Sscanf(line, "%d", &idx); err == nil && idx >= 1 && idx <= optionCount {
		return idx
	}
	for i := 0; i < len(line); i++ {
		if line[i] != '\x1b' || i+2 >= len(line) || line[i+1] != '[' {
			continue
		}
		switch line[i+2] {
		case 'A':
			selected = moveChoiceSelection(selected, -1, optionCount)
			i += 2
		case 'B':
			selected = moveChoiceSelection(selected, 1, optionCount)
			i += 2
		}
	}
	return selected
}

func promptChoiceInteractive(prompt string, options []string, defaultIdx int) (int, bool) {
	fd := int(syscall.Stdin)
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return 0, false
	}
	defer term.Restore(fd, oldState) //nolint:errcheck

	selected := defaultIdx
	renderChoiceBlock(prompt, options, selected, true)

	refresh := func() {
		fmt.Printf("\r\033[%dA", choiceRefreshRows(len(options)))
		renderChoiceBlock(prompt, options, selected, true)
	}

	var b [1]byte
	for {
		if _, err := os.Stdin.Read(b[:]); err != nil {
			fmt.Print("\r\n")
			return selected, true
		}
		switch b[0] {
		case '\r', '\n':
			fmt.Print("\r\n")
			return selected, true
		case 3: // Ctrl+C
			_ = term.Restore(fd, oldState)
			fmt.Print("\r\n")
			os.Exit(130)
		case '\x1b':
			first, err := readChoiceByte()
			if err != nil {
				continue
			}
			second, err := readChoiceByte()
			if err != nil {
				continue
			}
			if first != '[' {
				continue
			}
			switch second {
			case 'A':
				selected = moveChoiceSelection(selected, -1, len(options))
				refresh()
			case 'B':
				selected = moveChoiceSelection(selected, 1, len(options))
				refresh()
			}
		default:
			if b[0] >= '1' && b[0] <= '9' {
				if idx := int(b[0] - '0'); idx >= 1 && idx <= len(options) {
					selected = idx
					refresh()
				}
			}
		}
	}
}

func readChoiceByte() (byte, error) {
	var b [1]byte
	_, err := os.Stdin.Read(b[:])
	return b[0], err
}

func choiceRefreshRows(optionCount int) int {
	if optionCount < 0 {
		return 0
	}
	return optionCount
}

func moveChoiceSelection(current, delta, optionCount int) int {
	if optionCount <= 0 {
		return 0
	}
	next := current + delta
	if next < 1 {
		return optionCount
	}
	if next > optionCount {
		return 1
	}
	return next
}
