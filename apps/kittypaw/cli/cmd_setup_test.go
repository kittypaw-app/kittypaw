package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"

	"github.com/jinto/kittypaw/core"
)

// AC-BOX: setup completion box's right `│` border must land at the same column
// for every content line, regardless of mixed Korean (2-col) / ASCII (1-col) /
// emoji content. Failing this means runewidth-aware padding is broken.
func TestRenderSetupBox_RightBorderAligns(t *testing.T) {
	var buf bytes.Buffer
	renderSetupBox(&buf, "/Users/jinto/.kittypaw/accounts/default/config.toml")

	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")

	// Collect every content line that starts with the box's left "  │ " and
	// verify its trailing "│" appears at the same display column as its peers.
	type rowMetric struct {
		idx      int
		raw      string
		rightOk  bool
		rightCol int
	}
	var rows []rowMetric
	for i, ln := range lines {
		if !strings.HasPrefix(ln, "  │") || !strings.HasSuffix(ln, "│") {
			continue
		}
		// Skip top/bottom borders (start with ╭ or ╰)
		if strings.Contains(ln, "╭") || strings.Contains(ln, "╰") {
			continue
		}
		// Display column = runewidth.StringWidth of the entire line minus 1
		// (the trailing │ itself spans 1 col but its left edge is what we care
		// about; equivalently, the *length-up-to-and-including-│* should match).
		col := displayWidth(ln)
		rows = append(rows, rowMetric{idx: i, raw: ln, rightCol: col, rightOk: true})
	}

	if len(rows) < 3 {
		t.Fatalf("expected at least 3 box content rows, got %d:\n%s", len(rows), out)
	}

	want := rows[0].rightCol
	for _, r := range rows {
		if r.rightCol != want {
			t.Errorf("line %d right `│` at col %d, want %d. line=%q", r.idx, r.rightCol, want, r.raw)
		}
	}
}

// displayWidth returns the runewidth-based display column count of s.
func displayWidth(s string) int {
	return runewidth.StringWidth(s)
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = old
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close stdout pipe writer: %v", err)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("read stdout pipe: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close stdout pipe reader: %v", err)
	}
	return buf.String()
}

// AC-1: autoChatEligible truth table — gates auto-entry on (stdin+stdout TTY)
// AND (provider=="") AND (!noChat). Non-interactive or opt-out paths must
// return false without prompting the user.
func TestAutoChatEligible_TruthTable(t *testing.T) {
	tests := []struct {
		name      string
		provider  string
		noChat    bool
		stdinTTY  bool
		stdoutTTY bool
		want      bool
	}{
		{"all on, tty", "", false, true, true, true},
		{"stdin not tty", "", false, false, true, false},
		{"stdout not tty", "", false, true, false, false},
		{"both not tty", "", false, false, false, false},
		{"provider set", "anthropic", false, true, true, false},
		{"noChat set", "", true, true, true, false},
		{"provider + noChat", "anthropic", true, true, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := setupFlags{provider: tc.provider, noChat: tc.noChat}
			got := autoChatEligible(f, tc.stdinTTY, tc.stdoutTTY)
			if got != tc.want {
				t.Fatalf("autoChatEligible(%+v, stdin=%v, stdout=%v) = %v, want %v",
					f, tc.stdinTTY, tc.stdoutTTY, got, tc.want)
			}
		})
	}
}

// AC-STRINGS: Korean user-facing strings are pinned so a casual rewording
// doesn't silently break UX or downstream doc references. setupPromptAutoChat
// is the base prompt; promptYesNo() appends " (Y/n): " at render time.
func TestSetupStrings_Golden(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"prompt base", setupPromptAutoChat, "> 지금 바로 대화를 시작할까요?"},
		{"reloaded", setupMsgReloaded, "✓ 서버 설정 재적용"},
		{"server off", setupMsgServerOff, "다음 단계: 'kittypaw server start' 로 서버를 시작하거나 'kittypaw chat' 이 자동으로 기동합니다."},
		{"reload failed", setupMsgReloadFailedFmt, "경고: 서버 reload 실패: %v — 'kittypaw server stop && kittypaw server start' 로 재시작하세요."},
		{"auto-chat blocked", setupMsgAutoChatBlocked, "자동 채팅 진입을 건너뜁니다 — 현재 서버가 이전 설정을 그대로 쓰고 있습니다. 재시작 후 'kittypaw chat' 으로 다시 시도하세요."},
		{"account credentials intro ko", accountCredentialsIntroKo, "KittyPaw 사용자 계정을 설정합니다.\n계정 ID와 비밀번호를 입력해주세요.\n계정 ID와 비밀번호 정보는 이 컴퓨터에만 저장됩니다."},
		{"account credentials intro en", accountCredentialsIntroEn, "Set up a KittyPaw user account.\nEnter an account ID and password to continue.\nYour account ID and password data are stored only on this computer."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("got %q, want %q", c.got, c.want)
			}
		})
	}
}

func TestPromptChoiceAcceptsArrowSequences(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		def     int
		want    int
		options []string
	}{
		{
			name:    "down selects next option",
			input:   "\x1b[B\n",
			def:     1,
			want:    2,
			options: []string{"Anthropic", "OpenAI", "Gemini"},
		},
		{
			name:    "up wraps to previous option",
			input:   "\x1b[A\n",
			def:     1,
			want:    3,
			options: []string{"Anthropic", "OpenAI", "Gemini"},
		},
		{
			name:    "multiple arrows accumulate",
			input:   "\x1b[B\x1b[B\n",
			def:     1,
			want:    3,
			options: []string{"Anthropic", "OpenAI", "Gemini"},
		},
		{
			name:    "number still selects directly",
			input:   "2\n",
			def:     1,
			want:    2,
			options: []string{"Anthropic", "OpenAI", "Gemini"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scanner := bufio.NewScanner(strings.NewReader(tc.input))
			got := 0
			captureStdout(t, func() {
				got = promptChoice(scanner, "  > ", tc.options, tc.def)
			})
			if got != tc.want {
				t.Fatalf("promptChoice(%q, default=%d) = %d, want %d", tc.input, tc.def, got, tc.want)
			}
		})
	}
}

func TestPromptChoiceRefreshRowsMatchesOptionCount(t *testing.T) {
	if got := choiceRefreshRows(5); got != 5 {
		t.Fatalf("choiceRefreshRows(5) = %d, want 5", got)
	}
}

func TestAccountCredentialsIntroLocalizedWithoutAccountID(t *testing.T) {
	t.Run("korean", func(t *testing.T) {
		t.Setenv("LC_ALL", "")
		t.Setenv("LANG", "ko_KR.UTF-8")

		if got := accountCredentialsIntroMessage(""); got != accountCredentialsIntroKo {
			t.Fatalf("accountCredentialsIntroMessage() = %q, want %q", got, accountCredentialsIntroKo)
		}
	})

	t.Run("english default", func(t *testing.T) {
		t.Setenv("LC_ALL", "")
		t.Setenv("LANG", "en_US.UTF-8")

		if got := accountCredentialsIntroMessage(""); got != accountCredentialsIntroEn {
			t.Fatalf("accountCredentialsIntroMessage() = %q, want %q", got, accountCredentialsIntroEn)
		}
	})
}

// fakeServer implements serverSession for maybeReloadServer tests.
type fakeServer struct {
	running   bool
	reloadErr error
	reloadN   int
}

func (f *fakeServer) IsRunning() bool { return f.running }
func (f *fakeServer) Reload() error {
	f.reloadN++
	return f.reloadErr
}

// AC-5: server not running -> print hint, don't attempt reload, return
// reloadOutcomeServerOff so runSetup still allows auto-entry (a fresh server
// will pick up the new config when chat spawns it).
func TestMaybeReloadServer_Off(t *testing.T) {
	var out, errBuf bytes.Buffer
	fd := &fakeServer{running: false}
	dial := func() (serverSession, error) { return fd, nil }

	got := maybeReloadServer(dial, &out, &errBuf)

	if got != reloadOutcomeServerOff {
		t.Fatalf("outcome = %v, want reloadOutcomeServerOff", got)
	}
	if fd.reloadN != 0 {
		t.Fatalf("Reload called %d times, expected 0", fd.reloadN)
	}
	if !strings.Contains(errBuf.String(), "kittypaw server start") {
		t.Fatalf("stderr missing hint: %q", errBuf.String())
	}
	if out.Len() != 0 {
		t.Fatalf("unexpected stdout: %q", out.String())
	}
}

// AC-4: server running + reload OK -> 1 Reload call + success line on stdout
// + reloadOutcomeReloaded so runSetup may auto-enter chat.
func TestMaybeReloadServer_Happy(t *testing.T) {
	var out, errBuf bytes.Buffer
	fd := &fakeServer{running: true}
	dial := func() (serverSession, error) { return fd, nil }

	got := maybeReloadServer(dial, &out, &errBuf)

	if got != reloadOutcomeReloaded {
		t.Fatalf("outcome = %v, want reloadOutcomeReloaded", got)
	}
	if fd.reloadN != 1 {
		t.Fatalf("Reload called %d times, expected 1", fd.reloadN)
	}
	if !strings.Contains(out.String(), setupMsgReloaded) {
		t.Fatalf("stdout missing success line: %q", out.String())
	}
	if errBuf.Len() != 0 {
		t.Fatalf("unexpected stderr: %q", errBuf.String())
	}
}

// AC-6: server running + reload err -> warning on stderr + recovery hint, no
// success on stdout, and reloadOutcomeFailed so runSetup blocks auto-entry
// (chat would otherwise attach to a server still holding the previous
// config — stale LLM key / channels). Closes the adversarial-review finding
// that stale state was silently sent.
func TestMaybeReloadServer_Error(t *testing.T) {
	var out, errBuf bytes.Buffer
	fd := &fakeServer{running: true, reloadErr: errors.New("boom")}
	dial := func() (serverSession, error) { return fd, nil }

	got := maybeReloadServer(dial, &out, &errBuf)

	if got != reloadOutcomeFailed {
		t.Fatalf("outcome = %v, want reloadOutcomeFailed", got)
	}
	if fd.reloadN != 1 {
		t.Fatalf("Reload called %d times, expected 1", fd.reloadN)
	}
	if !strings.Contains(errBuf.String(), "경고: 서버 reload 실패") {
		t.Fatalf("stderr missing warning: %q", errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "kittypaw server stop && kittypaw server start") {
		t.Fatalf("stderr missing recovery hint: %q", errBuf.String())
	}
	if strings.Contains(out.String(), setupMsgReloaded) {
		t.Fatalf("unexpected success on stdout: %q", out.String())
	}
}

// dial error is treated as "server off" — same hint, no Reload attempt,
// reloadOutcomeServerOff so auto-entry still works. Protects against a
// transient dial failure silently skipping the hint.
func TestMaybeReloadServer_DialError(t *testing.T) {
	var out, errBuf bytes.Buffer
	dial := func() (serverSession, error) { return nil, errors.New("no config") }

	got := maybeReloadServer(dial, &out, &errBuf)

	if got != reloadOutcomeServerOff {
		t.Fatalf("outcome = %v, want reloadOutcomeServerOff", got)
	}
	if !strings.Contains(errBuf.String(), "kittypaw server start") {
		t.Fatalf("stderr missing hint: %q", errBuf.String())
	}
	if out.Len() != 0 {
		t.Fatalf("unexpected stdout: %q", out.String())
	}
}

// AC-2 regression: `kittypaw setup --no-chat` must register the flag so users
// can opt out of the auto-entry prompt. A missing flag would quietly reintroduce
// the old "just exit after setup" behavior for every user.
func TestNewSetupCmd_RegistersNoChatFlag(t *testing.T) {
	cmd := newSetupCmd()
	f := cmd.Flags().Lookup("no-chat")
	if f == nil {
		t.Fatal("--no-chat flag not registered on `kittypaw setup`")
	}
	if f.DefValue != "false" {
		t.Errorf("--no-chat default = %q, want \"false\"", f.DefValue)
	}
}

func TestSetupWebFlagIsRemoved(t *testing.T) {
	cmd := newSetupCmd()
	if f := cmd.Flags().Lookup("web"); f != nil {
		t.Fatalf("--web flag should not be registered: %+v", f)
	}
}

// AC-3 regression: `--provider` (non-interactive mode) must NOT prompt for
// auto-entry. This drives autoChatEligible directly to double-pin the gate
// — the T1 truth table covers the helper in isolation; this test covers the
// call-site wiring via the same public flag name end users see.
func TestAutoChatEligible_ProviderFlagSkipsPrompt(t *testing.T) {
	f := setupFlags{provider: "anthropic"}
	if autoChatEligible(f, true, true) {
		t.Fatal("non-interactive (--provider set) must not trigger auto-entry")
	}
}

func TestNewSetupCmd_RegistersAccountCredentialFlags(t *testing.T) {
	cmd := newSetupCmd()
	if f := cmd.Flags().Lookup("account"); f == nil {
		t.Fatal("--account flag not registered on `kittypaw setup`")
	}
	if f := cmd.Flags().Lookup("password-stdin"); f == nil {
		t.Fatal("--password-stdin flag not registered on `kittypaw setup`")
	}
}

func TestSetupWritesNamedAccount(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")

	cmd := newSetupCmd()
	cmd.SetIn(strings.NewReader("secret-password\n"))
	flags := setupTestFlags("alice")
	flags.passwordStdin = true

	if err := runSetup(cmd, &flags); err != nil {
		t.Fatalf("runSetup: %v", err)
	}

	cfgPath := filepath.Join(root, "accounts", "alice", "config.toml")
	cfg, err := core.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("load named account config: %v", err)
	}
	if cfg.LLM.Provider != "openai" || cfg.LLM.Model != "llama3" || cfg.LLM.BaseURL != "http://localhost:11434/v1/chat/completions" {
		t.Fatalf("LLM config = provider=%q model=%q base=%q", cfg.LLM.Provider, cfg.LLM.Model, cfg.LLM.BaseURL)
	}
	secrets, err := core.LoadSecretsFrom(filepath.Join(root, "accounts", "alice", "secrets.json"))
	if err != nil {
		t.Fatalf("load account secrets: %v", err)
	}
	if key, ok := secrets.Get("local-server", "api_key"); !ok || key == "" {
		t.Fatal("setup must write local-server api key secret so local CLI chat can authenticate to /ws")
	}

	auth := core.NewLocalAuthStore(filepath.Join(root, "accounts"))
	if ok, err := auth.VerifyPassword("alice", "secret-password"); err != nil || !ok {
		t.Fatalf("VerifyPassword alice = (%v, %v), want true nil", ok, err)
	}
	mustExist(t, filepath.Join(root, "accounts", "alice", "profiles", "default", "SOUL.md"))
	mustExist(t, filepath.Join(root, "accounts", "alice", "data", "kittypaw.db"))
	mustNotExist(t, filepath.Join(root, "data"))
	mustNotExist(t, filepath.Join(root, "skills"))
	mustNotExist(t, filepath.Join(root, "accounts", "default"))
}

func TestWizardWorkspaceHTTPDefaultsToAccountDocumentsFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	var w core.WizardResult
	scanner := bufio.NewScanner(strings.NewReader("\ny\nn\n"))
	wizardWorkspaceHTTP(scanner, "alice", nil, &w)

	want := filepath.Join(home, "Documents", "kittypaw", "alice")
	if w.WorkspacePath != want {
		t.Fatalf("WorkspacePath = %q, want %q", w.WorkspacePath, want)
	}
	if info, err := os.Stat(want); err != nil || !info.IsDir() {
		t.Fatalf("default workspace dir stat = (%v, %v), want existing directory", info, err)
	}
	if w.HTTPAccess {
		t.Fatal("HTTPAccess = true, want false from prompt input")
	}
}

func TestRunWizardUsesNamedAccountSecrets(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	stubFetchDiscovery(t, nil, errors.New("offline"))

	secrets, err := core.LoadAccountSecrets("alice")
	if err != nil {
		t.Fatalf("LoadAccountSecrets alice: %v", err)
	}
	mgr := core.NewAPITokenManager("", secrets)
	if err := mgr.SaveTokens(core.DefaultAPIServerURL, makeSetupTestJWT(t), "refresh"); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	var w core.WizardResult
	wizardAPIServer(bufio.NewScanner(strings.NewReader("")), "alice", &w)

	if w.APIServerURL != core.DefaultAPIServerURL {
		t.Fatalf("APIServerURL = %q, want %q", w.APIServerURL, core.DefaultAPIServerURL)
	}
	mustNotExist(t, filepath.Join(root, "accounts", "default", "secrets.json"))
}

func TestWizardAPIServerAutoPairsChatRelayWhenAlreadyLoggedIn(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/devices/pair" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"device_id":"dev_123","device_access_token":"access-1","device_refresh_token":"refresh-1","expires_in":900}`)
	}))
	defer ts.Close()
	stubFetchDiscovery(t, &core.DiscoveryResponse{
		APIBaseURL:   core.DefaultAPIServerURL,
		AuthBaseURL:  ts.URL + "/auth",
		ChatRelayURL: "https://chat.kittypaw.app",
	}, nil)

	secrets, err := core.LoadAccountSecrets("alice")
	if err != nil {
		t.Fatalf("LoadAccountSecrets alice: %v", err)
	}
	mgr := core.NewAPITokenManager("", secrets)
	if err := mgr.SaveTokens(core.DefaultAPIServerURL, makeSetupTestJWT(t), "refresh"); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	var w core.WizardResult
	wizardAPIServer(bufio.NewScanner(strings.NewReader("")), "alice", &w)

	reloaded, err := core.LoadAccountSecrets("alice")
	if err != nil {
		t.Fatal(err)
	}
	tokens, ok := core.NewAPITokenManager("", reloaded).LoadChatRelayDeviceTokens(core.DefaultAPIServerURL)
	if !ok || tokens.DeviceID != "dev_123" || tokens.AccessToken != "access-1" || tokens.RefreshToken != "refresh-1" {
		t.Fatalf("chat relay tokens = (%#v, %v), want paired tokens", tokens, ok)
	}
}

func TestWizardAPIServerLoginDefaultsToYes(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)

	out := captureStdout(t, func() {
		var w core.WizardResult
		wizardAPIServer(bufio.NewScanner(strings.NewReader("n\n")), "alice", &w)
		if w.APIServerURL != "" {
			t.Fatalf("APIServerURL = %q, want empty when login is declined", w.APIServerURL)
		}
	})

	if !strings.Contains(out, "  > Login? (Y/n):") {
		t.Fatalf("login prompt = %q, want default-yes hint", out)
	}
}

func TestRunSetupPassesResolvedAccountToWizard(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))
	auth := core.NewLocalAuthStore(filepath.Join(root, "accounts"))
	if err := auth.CreateUser("alice", "existing-password"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	original := setupWizardRunner
	t.Cleanup(func() { setupWizardRunner = original })
	called := false
	setupWizardRunner = func(flags setupFlags, existing *core.Config) (core.WizardResult, error) {
		called = true
		if flags.accountID != "alice" {
			t.Fatalf("wizard accountID = %q, want alice", flags.accountID)
		}
		if flags.validate == nil {
			t.Fatal("wizard validate hook is nil")
		}
		result := core.WizardResult{LLMProvider: "openai", LLMModel: "stub-model"}
		if err := flags.validate(result); err != nil {
			t.Fatalf("validate hook: %v", err)
		}
		return result, nil
	}

	cmd := newSetupCmd()
	flags := setupFlags{noChat: true, noService: true}
	if err := runSetup(cmd, &flags); err != nil {
		t.Fatalf("runSetup: %v", err)
	}
	if !called {
		t.Fatal("setup wizard was not called")
	}
}

func TestRunSetupWritesExtraModelsAndSecrets(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))
	auth := core.NewLocalAuthStore(filepath.Join(root, "accounts"))
	if err := auth.CreateUser("alice", "existing-password"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	cmd := newSetupCmd()
	flags := setupTestFlags("alice")
	flags.extraModels = []string{"id=openai-fast,provider=openai,model=gpt-5.5"}
	flags.extraModelKeys = []string{"openai-fast=sk-openai"}

	if err := runSetup(cmd, &flags); err != nil {
		t.Fatalf("runSetup: %v", err)
	}
	cfg, err := core.LoadConfig(filepath.Join(root, "accounts", "alice", "config.toml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.LLM.Models) != 2 {
		t.Fatalf("models len = %d, want 2: %#v", len(cfg.LLM.Models), cfg.LLM.Models)
	}
	if cfg.LLM.Models[0].ID != "main" || cfg.LLM.Models[1].ID != "openai-fast" {
		t.Fatalf("models = %#v", cfg.LLM.Models)
	}
	secrets, err := core.LoadAccountSecrets("alice")
	if err != nil {
		t.Fatalf("LoadAccountSecrets: %v", err)
	}
	if got, ok := secrets.Get("llm/openai", "api_key"); !ok || got != "sk-openai" {
		t.Fatalf("extra model key = (%q, %v)", got, ok)
	}
}

func TestSetupMultipleAccountsRequiresExplicitBeforeWriting(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "bob", "config.toml"))

	cmd := newSetupCmd()
	cmd.SetIn(strings.NewReader("secret-password\n"))
	flags := setupTestFlags("")
	flags.passwordStdin = true

	err := runSetup(cmd, &flags)
	if err == nil {
		t.Fatal("expected multiple account error")
	}
	for _, want := range []string{"alice", "bob", "--account", "KITTYPAW_ACCOUNT"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
	mustNotExist(t, filepath.Join(root, "auth.json"))
}

func TestSetupRejectsDuplicateTelegramBeforeWriting(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	token := "123456:ABCDefghijklmnopqrstuvwxyz012345"
	mustWriteTestConfigWithChannels(t, filepath.Join(root, "accounts", "bob", "config.toml"), []core.ChannelConfig{
		{ChannelType: core.ChannelTelegram, Token: token},
	})

	cmd := newSetupCmd()
	cmd.SetIn(strings.NewReader("secret-password\n"))
	flags := setupTestFlags("alice")
	flags.passwordStdin = true
	flags.telegramToken = token
	flags.telegramChatID = "100"

	err := runSetup(cmd, &flags)
	if err == nil || !strings.Contains(err.Error(), "channel validation") {
		t.Fatalf("runSetup error = %v, want channel validation", err)
	}
	mustNotExist(t, filepath.Join(root, "accounts", "alice"))
	mustNotExist(t, filepath.Join(root, "accounts", "alice", "config.toml"))
	mustNotExist(t, filepath.Join(root, "auth.json"))
}

func TestSetupRejectsSharedAccountWithChannelBeforeWriting(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	cfgPath := filepath.Join(root, "accounts", "shared", "config.toml")
	mustWriteTestConfigWith(t, cfgPath, func(cfg *core.Config) {
		cfg.IsShared = true
	})
	token := "123456:ABCDefghijklmnopqrstuvwxyz012345"

	cmd := newSetupCmd()
	cmd.SetIn(strings.NewReader("secret-password\n"))
	flags := setupTestFlags("shared")
	flags.passwordStdin = true
	flags.telegramToken = token
	flags.telegramChatID = "100"

	err := runSetup(cmd, &flags)
	if err == nil || !strings.Contains(err.Error(), "team space validation") {
		t.Fatalf("runSetup error = %v, want team space validation", err)
	}
	mustNotExist(t, filepath.Join(root, "auth.json"))

	cfg, err := core.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("load shared config: %v", err)
	}
	if len(cfg.Channels) != 0 {
		t.Fatalf("shared config channels = %v, want unchanged empty", cfg.Channels)
	}
}

func TestWizardKakaoValidatesBeforeWritingSecrets(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	validateErr := errors.New("reject kakao")
	called := false
	var w core.WizardResult

	err := wizardKakao(bufio.NewScanner(strings.NewReader("y\n")), "alice", nil, &w, func(candidate core.WizardResult) error {
		called = true
		if !candidate.KakaoEnabled {
			t.Fatal("candidate did not enable Kakao")
		}
		if candidate.APIServerURL != core.DefaultAPIServerURL {
			t.Fatalf("candidate APIServerURL = %q", candidate.APIServerURL)
		}
		return validateErr
	})
	if !errors.Is(err, validateErr) {
		t.Fatalf("wizardKakao error = %v, want %v", err, validateErr)
	}
	if !called {
		t.Fatal("validator was not called")
	}
	mustNotExist(t, filepath.Join(root, "accounts", "alice", "secrets.json"))
}

func TestWizardKakaoReconfigureValidatesBeforeWritingSecrets(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	validateErr := errors.New("reject kakao reconfigure")
	called := false
	existing := core.DefaultConfig()
	existing.Channels = []core.ChannelConfig{{ChannelType: core.ChannelKakaoTalk}}
	var w core.WizardResult

	err := wizardKakao(bufio.NewScanner(strings.NewReader("y\n")), "alice", &existing, &w, func(candidate core.WizardResult) error {
		called = true
		if !candidate.KakaoEnabled {
			t.Fatal("candidate did not enable Kakao")
		}
		if candidate.APIServerURL != core.DefaultAPIServerURL {
			t.Fatalf("candidate APIServerURL = %q", candidate.APIServerURL)
		}
		return validateErr
	})
	if !errors.Is(err, validateErr) {
		t.Fatalf("wizardKakao error = %v, want %v", err, validateErr)
	}
	if !called {
		t.Fatal("validator was not called")
	}
	mustNotExist(t, filepath.Join(root, "accounts", "alice", "secrets.json"))
}

func TestWizardKakaoStoresRelayResult(t *testing.T) {
	restore := stubAccountKakaoPairing(t, core.WizardResult{
		KakaoEnabled:    true,
		KakaoRelayWSURL: "wss://relay.example.com/ws/kakao-token",
		APIServerURL:    core.DefaultAPIServerURL,
	})
	defer restore()

	var w core.WizardResult
	err := wizardKakao(bufio.NewScanner(strings.NewReader("y\n")), "alice", nil, &w, nil)
	if err != nil {
		t.Fatalf("wizardKakao: %v", err)
	}
	if !w.KakaoEnabled {
		t.Fatal("KakaoEnabled = false, want true")
	}
	if w.KakaoRelayWSURL != "wss://relay.example.com/ws/kakao-token" {
		t.Fatalf("KakaoRelayWSURL = %q", w.KakaoRelayWSURL)
	}
	if w.APIServerURL != core.DefaultAPIServerURL {
		t.Fatalf("APIServerURL = %q", w.APIServerURL)
	}
}

func TestSetupEnvAccountMustExist(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "alice")
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "bob", "config.toml"))

	cmd := newSetupCmd()
	cmd.SetIn(strings.NewReader("secret-password\n"))
	flags := setupTestFlags("")
	flags.passwordStdin = true

	err := runSetup(cmd, &flags)
	if err == nil || !strings.Contains(err.Error(), "KITTYPAW_ACCOUNT") {
		t.Fatalf("runSetup error = %v, want KITTYPAW_ACCOUNT not found", err)
	}
	mustNotExist(t, filepath.Join(root, "accounts", "alice"))
}

func TestSetupExistingAccountWithAuthDoesNotRequirePasswordStdin(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))
	auth := core.NewLocalAuthStore(filepath.Join(root, "accounts"))
	if err := auth.CreateUser("alice", "existing-password"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	cmd := newSetupCmd()
	flags := setupTestFlags("alice")

	if err := runSetup(cmd, &flags); err != nil {
		t.Fatalf("runSetup with existing auth: %v", err)
	}
	if ok, err := auth.VerifyPassword("alice", "existing-password"); err != nil || !ok {
		t.Fatalf("VerifyPassword existing = (%v, %v), want true nil", ok, err)
	}
}

func TestSetupFirstRunRequiresAccountAndPasswordStdin(t *testing.T) {
	t.Run("account", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv("KITTYPAW_CONFIG_DIR", root)
		t.Setenv("KITTYPAW_ACCOUNT", "")

		cmd := newSetupCmd()
		cmd.SetIn(strings.NewReader("secret-password\n"))
		flags := setupTestFlags("")
		flags.passwordStdin = true

		err := runSetup(cmd, &flags)
		if err == nil || !strings.Contains(err.Error(), "--account is required") {
			t.Fatalf("runSetup error = %v, want --account is required", err)
		}
		mustNotExist(t, filepath.Join(root, "accounts"))
	})

	t.Run("password stdin", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv("KITTYPAW_CONFIG_DIR", root)
		t.Setenv("KITTYPAW_ACCOUNT", "")

		cmd := newSetupCmd()
		flags := setupTestFlags("alice")

		err := runSetup(cmd, &flags)
		if err == nil || !strings.Contains(err.Error(), "--password-stdin is required") {
			t.Fatalf("runSetup error = %v, want --password-stdin is required", err)
		}
		mustNotExist(t, filepath.Join(root, "accounts", "alice", "config.toml"))
	})
}

func TestResolveSetupPasswordPreservesStdinWhitespace(t *testing.T) {
	got, err := resolveSetupPassword(setupFlags{passwordStdin: true}, strings.NewReader("  secret  \n"))
	if err != nil {
		t.Fatalf("resolveSetupPassword: %v", err)
	}
	if got != "  secret  " {
		t.Fatalf("password = %q, want surrounding spaces preserved", got)
	}
}

func TestConfigCheckUsesDiscoveredNamedAccount(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))

	if err := runConfigCheck(nil, nil); err != nil {
		t.Fatalf("runConfigCheck: %v", err)
	}
}

func TestConfigCheckAcceptsAccountFlag(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "bob", "config.toml"))

	cmd := newConfigCheckCmd()
	if err := cmd.Flags().Set("account", "bob"); err != nil {
		t.Fatalf("set account flag: %v", err)
	}
	if err := runConfigCheck(cmd, nil); err != nil {
		t.Fatalf("runConfigCheck: %v", err)
	}
}

func setupTestFlags(accountID string) setupFlags {
	return setupFlags{
		accountID:     accountID,
		provider:      "local",
		localURL:      "http://localhost:11434/v1",
		localModel:    "llama3",
		httpAccess:    true,
		noChat:        true,
		noService:     true,
		passwordStdin: false,
	}
}

func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
}

func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s to not exist, stat err = %v", path, err)
	}
}

func mustWriteTestConfigWithChannels(t *testing.T, path string, channels []core.ChannelConfig) {
	t.Helper()
	mustWriteTestConfigWith(t, path, func(cfg *core.Config) {
		cfg.Channels = channels
	})
}

func mustWriteTestConfigWith(t *testing.T, path string, mutate func(*core.Config)) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	cfg := core.DefaultConfig()
	mutate(&cfg)
	if cfg.IsFamily {
		cfg.IsShared = true
	}
	secrets, err := core.LoadSecretsFrom(filepath.Join(filepath.Dir(path), "secrets.json"))
	if err != nil {
		t.Fatalf("load secrets %s: %v", path, err)
	}
	for _, ch := range cfg.Channels {
		if ch.ChannelType == core.ChannelTelegram && ch.Token != "" {
			if err := secrets.Set("channel/"+ch.SecretID(), "bot_token", ch.Token); err != nil {
				t.Fatalf("save telegram secret: %v", err)
			}
		}
	}
	if err := core.WriteConfigAtomic(&cfg, path); err != nil {
		t.Fatalf("write config %s: %v", path, err)
	}
}

func makeSetupTestJWT(t *testing.T) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256"}`))
	claims, err := json.Marshal(map[string]any{
		"uid": "setup-test",
		"exp": time.Now().Add(10 * time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("marshal JWT claims: %v", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(claims)
	sig := base64.RawURLEncoding.EncodeToString([]byte("signature"))
	return header + "." + payload + "." + sig
}
