package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/jinto/kittypaw/client"
	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/server"
)

func TestRootCommandPropagatesContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	root := newRootCmd()
	var sawCanceled bool
	root.AddCommand(&cobra.Command{
		Use: "context-probe",
		RunE: func(cmd *cobra.Command, args []string) error {
			sawCanceled = errors.Is(cmd.Context().Err(), context.Canceled)
			return nil
		},
	})
	root.SetArgs([]string{"context-probe"})
	if err := root.ExecuteContext(ctx); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}
	if !sawCanceled {
		t.Fatal("command did not observe canceled context")
	}
}

func TestExecuteRootCommandDoesNotInstallGlobalSignalContext(t *testing.T) {
	var done <-chan struct{}
	root := &cobra.Command{
		Use: "root",
		RunE: func(cmd *cobra.Command, args []string) error {
			done = cmd.Context().Done()
			return nil
		},
	}
	if err := executeRootCommand(root); err != nil {
		t.Fatalf("executeRootCommand: %v", err)
	}
	if done != nil {
		t.Fatal("root command installed a cancelable global context")
	}
}

func TestIsTransportDropErr_StringMatches(t *testing.T) {
	cases := []string{
		"EOF",
		"unexpected EOF",
		"write tcp 127.0.0.1:57428->127.0.0.1:3000: write: broken pipe",
		"failed to flush: write: broken pipe",
		"use of closed network connection",
		"read: connection reset by peer",
	}
	for _, msg := range cases {
		t.Run(msg, func(t *testing.T) {
			if !isTransportDropErr(errors.New(msg)) {
				t.Errorf("expected transport-drop classification for %q", msg)
			}
		})
	}
}

func TestIsTransportDropErr_TypedSentinels(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"io.EOF", io.EOF},
		{"io.ErrUnexpectedEOF", io.ErrUnexpectedEOF},
		{"syscall.EPIPE", syscall.EPIPE},
		{"syscall.ECONNRESET", syscall.ECONNRESET},
		{"net.ErrClosed", net.ErrClosed},
		{"wrapped io.EOF", fmt.Errorf("read ws msg: %w", io.EOF)},
		{"wrapped EPIPE", fmt.Errorf("write chat msg: %w", syscall.EPIPE)},
		{"deeply wrapped ECONNRESET", fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", syscall.ECONNRESET))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !isTransportDropErr(tc.err) {
				t.Errorf("expected transport-drop for %v", tc.err)
			}
		})
	}
}

func TestIsTransportDropErr_RejectsServerSide(t *testing.T) {
	// Server errors carry ErrServerSide via client/ws.go. Even when the
	// embedded message contains "EOF" / "broken pipe" / etc. the silent-
	// reconnect path must not retry — replaying would double-charge the
	// user without healing the underlying server failure.
	cases := []error{
		fmt.Errorf("%w: decode response: unexpected EOF", client.ErrServerSide),
		fmt.Errorf("%w: upstream returned broken pipe", client.ErrServerSide),
		fmt.Errorf("%w: connection reset by peer in tool result", client.ErrServerSide),
		fmt.Errorf("%w: use of closed network connection from skill", client.ErrServerSide),
		client.ErrServerSide,
	}
	for _, err := range cases {
		t.Run(err.Error(), func(t *testing.T) {
			if isTransportDropErr(err) {
				t.Errorf("server-side error %q must NOT classify as transport drop", err)
			}
		})
	}
}

func TestIsTransportDropErr_NegativeCases(t *testing.T) {
	cases := []string{
		"server failed to start",
		"chat protocol invalid",
		"unauthorized: 401",
		"timeout exceeded",
	}
	for _, msg := range cases {
		t.Run(msg, func(t *testing.T) {
			if isTransportDropErr(errors.New(msg)) {
				t.Errorf("non-transport error %q must not classify as drop", msg)
			}
		})
	}
}

func TestIsTransportDropErr_NilSafe(t *testing.T) {
	if isTransportDropErr(nil) {
		t.Fatal("nil error must not classify as transport drop")
	}
}

func TestRootCommandGroupsServerLifecycleCommands(t *testing.T) {
	root := newRootCmd()
	topLevel := map[string]bool{}
	for _, cmd := range root.Commands() {
		topLevel[cmd.Name()] = true
	}
	for _, hidden := range []string{"serve", "stop", "service", "daemon", "reload"} {
		if topLevel[hidden] {
			t.Fatalf("root command must not expose legacy %q command", hidden)
		}
	}
	serverCmd, _, err := root.Find([]string{"server"})
	if err != nil || serverCmd == nil || serverCmd.Name() != "server" {
		t.Fatalf("root Find(server) = %v, %v; want server command", serverCmd, err)
	}
	children := map[string]bool{}
	for _, cmd := range serverCmd.Commands() {
		children[cmd.Name()] = true
	}
	for _, want := range []string{"start", "stop", "reload", "install", "uninstall", "status", "logs"} {
		if !children[want] {
			t.Fatalf("server command missing %q child; got %#v", want, children)
		}
	}
}

func TestRootCommandDoesNotExposeFamilyCommand(t *testing.T) {
	root := newRootCmd()

	for _, cmd := range root.Commands() {
		if cmd.Name() == "family" {
			t.Fatal("root command must not expose family; team-space accounts are managed through account commands")
		}
	}
}

func TestRootCommandPlacesRunUnderSkill(t *testing.T) {
	root := newRootCmd()

	for _, cmd := range root.Commands() {
		if cmd.Name() == "run" {
			t.Fatal("root command must not expose legacy run alias")
		}
	}

	skillCmd, _, err := root.Find([]string{"skill"})
	if err != nil || skillCmd == nil || skillCmd.Name() != "skill" {
		t.Fatalf("root Find(skill) = %v, %v; want skill command", skillCmd, err)
	}
	children := map[string]*cobra.Command{}
	for _, cmd := range skillCmd.Commands() {
		children[cmd.Name()] = cmd
	}
	runCmd := children["run"]
	if runCmd == nil {
		t.Fatalf("skill command missing run child; got %#v", children)
	}
	if runCmd.Hidden {
		t.Fatal("skill run command must be visible")
	}
	if runCmd.Short != "Run a skill by name" {
		t.Fatalf("skill run short = %q", runCmd.Short)
	}
}

func TestRootCommandPlacesLogUnderSkill(t *testing.T) {
	root := newRootCmd()

	for _, cmd := range root.Commands() {
		if cmd.Name() == "log" {
			t.Fatal("root command must not expose log; use kittypaw skill log instead")
		}
	}

	skillCmd, _, err := root.Find([]string{"skill"})
	if err != nil || skillCmd == nil || skillCmd.Name() != "skill" {
		t.Fatalf("root Find(skill) = %v, %v; want skill command", skillCmd, err)
	}
	children := map[string]*cobra.Command{}
	for _, cmd := range skillCmd.Commands() {
		children[cmd.Name()] = cmd
	}
	logCmd := children["log"]
	if logCmd == nil {
		t.Fatalf("skill command missing log child; got %#v", children)
	}
	if logCmd.Hidden {
		t.Fatal("skill log command must be visible")
	}
	if logCmd.Short != "Show skill execution log" {
		t.Fatalf("skill log short = %q", logCmd.Short)
	}
}

func TestRootCommandDoesNotExposeReflection(t *testing.T) {
	root := newRootCmd()

	for _, cmd := range root.Commands() {
		if cmd.Name() == "reflection" {
			t.Fatal("root command must not expose reflection internals")
		}
	}
}

func TestRootCommandDoesNotExposePersona(t *testing.T) {
	root := newRootCmd()

	for _, cmd := range root.Commands() {
		if cmd.Name() == "persona" {
			t.Fatal("root command must not expose persona internals")
		}
	}
}

func TestPersonaCommandRejected(t *testing.T) {
	root := newRootCmd()
	root.SetArgs([]string{"persona"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)

	err := root.Execute()
	if err == nil {
		t.Fatal("kittypaw persona must fail after staff management moves to chat")
	}
	if !strings.Contains(err.Error(), `unknown command "persona" for "kittypaw"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRootCommandPlacesStatsUnderSkill(t *testing.T) {
	root := newRootCmd()

	for _, cmd := range root.Commands() {
		if cmd.Name() == "status" {
			t.Fatal("root command must not expose status; use kittypaw skill stats instead")
		}
	}

	skillCmd, _, err := root.Find([]string{"skill"})
	if err != nil || skillCmd == nil || skillCmd.Name() != "skill" {
		t.Fatalf("root Find(skill) = %v, %v; want skill command", skillCmd, err)
	}
	children := map[string]*cobra.Command{}
	for _, cmd := range skillCmd.Commands() {
		children[cmd.Name()] = cmd
	}
	if _, ok := children["reset"]; ok {
		t.Fatal("skill command must not expose reset hint command")
	}
	statsCmd := children["stats"]
	if statsCmd == nil {
		t.Fatalf("skill command missing stats child; got %#v", children)
	}
	if statsCmd.Short != "Show skill execution stats" {
		t.Fatalf("skill stats short = %q", statsCmd.Short)
	}
	if _, ok := children["suggest"]; ok {
		t.Fatal("skill command must not expose unused suggestion management")
	}
}

func TestSkillCommandRejectsRemovedSuggest(t *testing.T) {
	root := newRootCmd()
	root.SetArgs([]string{"skill", "suggest"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)

	err := root.Execute()
	if err == nil {
		t.Fatal("kittypaw skill suggest must fail after suggestion management is removed")
	}
	if !strings.Contains(err.Error(), `unknown command "suggest" for "kittypaw skill"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRootCommandRemovesAgentAndAddsChatHistoryControls(t *testing.T) {
	root := newRootCmd()

	if cmd, _, err := root.Find([]string{"agent"}); err == nil && cmd != nil && cmd.Name() == "agent" {
		t.Fatal("root command must not expose agent management")
	}

	chatCmd, _, err := root.Find([]string{"chat"})
	if err != nil || chatCmd == nil || chatCmd.Name() != "chat" {
		t.Fatalf("root Find(chat) = %v, %v; want chat command", chatCmd, err)
	}

	children := map[string]*cobra.Command{}
	for _, cmd := range chatCmd.Commands() {
		children[cmd.Name()] = cmd
	}
	for _, name := range []string{"history", "forget", "compact"} {
		if children[name] == nil {
			t.Fatalf("chat command missing %q child; got %#v", name, children)
		}
	}
	for _, name := range []string{"history", "forget", "compact"} {
		if children[name].Flags().Lookup("conversation-id") == nil {
			t.Fatalf("chat %s missing --conversation-id flag", name)
		}
	}
}

func TestRunSkillDryRunUsesSelectedAccount(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "bob", "config.toml"))

	oldDryRun := flagDryRun
	oldAccount := flagAccount
	oldRemote := flagRemote
	t.Cleanup(func() {
		flagDryRun = oldDryRun
		flagAccount = oldAccount
		flagRemote = oldRemote
	})
	flagDryRun = true
	flagAccount = "bob"
	flagRemote = ""

	skill := &core.Skill{
		Name:        "account-skill",
		Version:     1,
		Description: "bob-only skill",
		Enabled:     true,
		Format:      core.SkillFormatNative,
		Trigger:     core.SkillTrigger{Type: "manual"},
	}
	if err := core.SaveSkillTo(filepath.Join(root, "accounts", "bob"), skill, `return "bob"`); err != nil {
		t.Fatalf("SaveSkillTo: %v", err)
	}

	var runErr error
	out := captureStdout(t, func() {
		runErr = runSkill(nil, []string{"account-skill"})
	})
	if runErr != nil {
		t.Fatalf("runSkill dry-run with selected account: %v", runErr)
	}
	if !strings.Contains(out, "Account: bob\n") {
		t.Fatalf("runSkill output %q missing selected account", out)
	}
}

func TestAccountScopedCommandsExposeAccountFlag(t *testing.T) {
	root := newRootCmd()
	for _, path := range [][]string{
		{"chat"},
		{"skill"},
		{"skill", "stats"},
		{"skill", "log"},
		{"config", "check"},
		{"chat", "history"},
		{"chat", "forget"},
		{"chat", "compact"},
		{"memory"},
		{"channels"},
	} {
		cmd, _, err := root.Find(path)
		if err != nil || cmd == nil {
			t.Fatalf("Find(%v) = %v, %v", path, cmd, err)
		}
		if cmd.Flag("account") == nil {
			t.Fatalf("%q must expose --account", strings.Join(path, " "))
		}
	}
}

func TestResolveCLIAccountExplicit(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))

	got, err := resolveCLIAccount("alice")
	if err != nil || got != "alice" {
		t.Fatalf("resolveCLIAccount explicit = %q, %v; want alice nil", got, err)
	}
	if _, err := resolveCLIAccount("../bad"); err == nil {
		t.Fatal("expected invalid explicit account error")
	}
}

func TestResolveCLIAccountEnv(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))

	t.Setenv("KITTYPAW_ACCOUNT", "alice")
	got, err := resolveCLIAccount("")
	if err != nil || got != "alice" {
		t.Fatalf("resolveCLIAccount env = %q, %v; want alice nil", got, err)
	}
	t.Setenv("KITTYPAW_ACCOUNT", "../bad")
	if _, err := resolveCLIAccount(""); err == nil {
		t.Fatal("expected invalid env account error")
	}
}

func TestResolveCLIAccountExplicitMissingAccount(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))

	_, err := resolveCLIAccount("missing")
	if err == nil {
		t.Fatal("expected missing explicit account error")
	}
	for _, want := range []string{"missing", "alice"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestResolveCLIAccountSingleAccount(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))

	got, err := resolveCLIAccount("")
	if err != nil || got != "alice" {
		t.Fatalf("resolveCLIAccount = %q, %v; want alice nil", got, err)
	}
}

func TestResolveCLIAccountMultipleRequiresExplicit(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "bob", "config.toml"))

	_, err := resolveCLIAccount("")
	if err == nil {
		t.Fatal("expected multiple account error")
	}
	for _, want := range []string{"alice", "bob", "--account", "KITTYPAW_ACCOUNT"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestResolveCLIAccountUsesServerDefaultAccount(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "bob", "config.toml"))
	if err := os.WriteFile(filepath.Join(root, "server.toml"), []byte("default_account = \"bob\"\n"), 0o600); err != nil {
		t.Fatalf("write server.toml: %v", err)
	}

	got, err := resolveCLIAccount("")
	if err != nil || got != "bob" {
		t.Fatalf("resolveCLIAccount = %q, %v; want bob nil", got, err)
	}
}

func TestResolveCLIAccountNoAccounts(t *testing.T) {
	t.Setenv("KITTYPAW_CONFIG_DIR", t.TempDir())
	t.Setenv("KITTYPAW_ACCOUNT", "")
	if _, err := resolveCLIAccount(""); err == nil {
		t.Fatal("expected no accounts error")
	}
}

func TestPrintAccountContext(t *testing.T) {
	var b strings.Builder
	printAccountContext(&b, "jinto", "kittypaw chat")
	if got, want := b.String(), "Account: jinto\n"; got != want {
		t.Fatalf("account context = %q, want %q", got, want)
	}
}

func TestFormatChatHeaderUsesCompactAccountFirstShape(t *testing.T) {
	got := formatChatHeader("dev-cli", "dev-server", "claude-test", "jinto", []string{"telegram"})
	want := "KittyPaw chat · jinto · claude-test · telegram"
	if got != want {
		t.Fatalf("formatChatHeader = %q, want %q", got, want)
	}
}

func TestDefaultAccountBaseUsesResolvedAccount(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	flagAccount = ""
	if err := os.MkdirAll(filepath.Join(root, "accounts", "default"), 0o700); err != nil {
		t.Fatalf("mkdir incomplete default account: %v", err)
	}
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "jinto", "config.toml"))

	got, err := defaultAccountBase()
	if err != nil {
		t.Fatalf("defaultAccountBase: %v", err)
	}
	want := filepath.Join(root, "accounts", "jinto")
	if got != want {
		t.Fatalf("defaultAccountBase = %q, want %q", got, want)
	}
}

func TestOpenStoreUsesResolvedAccountDB(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	flagAccount = ""
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "jinto", "config.toml"))

	st, err := openStore()
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	_ = st.Close()

	if _, err := os.Stat(filepath.Join(root, "accounts", "jinto", "data", "kittypaw.db")); err != nil {
		t.Fatalf("expected account db: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "data", "kittypaw.db")); !os.IsNotExist(err) {
		t.Fatalf("legacy db should not be created; stat err = %v", err)
	}
}

func TestBootstrapRejectsMissingConfiguredDefaultAccount(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	if err := os.WriteFile(filepath.Join(root, "server.toml"), []byte("default_account = \"charlie\"\n"), 0o600); err != nil {
		t.Fatalf("write server.toml: %v", err)
	}
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))

	_, _, err := bootstrap()
	if err == nil {
		t.Fatal("expected missing configured default_account error")
	}
	for _, want := range []string{"default_account", "charlie", "accounts"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestBootstrapRejectsUnknownTeamSpaceMemberBeforeOpeningDeps(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	mustWriteTestConfigWith(t, filepath.Join(root, "accounts", "team", "config.toml"), func(cfg *core.Config) {
		cfg.IsShared = true
		cfg.TeamSpace.Members = []string{"ghost"}
	})

	_, _, err := bootstrap()
	if err == nil {
		t.Fatal("expected membership validation error")
	}
	if !strings.Contains(err.Error(), "team-space membership validation") {
		t.Fatalf("bootstrap error = %v, want team-space membership validation", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "accounts", "team", "data")); !os.IsNotExist(statErr) {
		t.Fatalf("account deps were opened before validation; data dir stat err = %v", statErr)
	}
}

func TestResolveServeBindUsesServerTomlUnlessFlagChanged(t *testing.T) {
	flagBind = ":3000"
	cmd := newServerStartCmd()
	got := resolveServeBind(cmd, core.TopLevelServerConfig{Bind: "127.0.0.1:4567"}, nil)
	if got != "127.0.0.1:4567" {
		t.Fatalf("resolveServeBind = %q, want server.toml bind", got)
	}

	if err := cmd.Flags().Set("bind", "127.0.0.1:9999"); err != nil {
		t.Fatalf("set bind: %v", err)
	}
	got = resolveServeBind(cmd, core.TopLevelServerConfig{Bind: "127.0.0.1:4567"}, nil)
	if got != "127.0.0.1:9999" {
		t.Fatalf("resolveServeBind explicit flag = %q, want flag bind", got)
	}
}

func TestResolveServeBindFallsBackToSelectedAccount(t *testing.T) {
	flagBind = ":3000"
	cmd := newServerStartCmd()
	cfg := core.DefaultConfig()
	cfg.Server.Bind = "127.0.0.1:4567"
	deps := []*server.AccountDeps{
		{Account: &core.Account{ID: "alice", Config: &cfg}},
	}

	got := resolveServeBind(cmd, core.TopLevelServerConfig{MasterAPIKey: "server-key"}, deps)
	if got != "127.0.0.1:4567" {
		t.Fatalf("resolveServeBind = %q, want account bind", got)
	}
}

func TestBootstrapBackfillsMissingAccountAPIKey(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("HOME", t.TempDir())

	cfg := core.DefaultConfig()
	cfg.LLM.Provider = "anthropic"
	cfg.LLM.APIKey = "test-key"
	cfg.LLM.Model = "claude-test"
	cfg.Server.APIKey = ""
	cfgPath := filepath.Join(root, "accounts", "alice", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatalf("mkdir account dir: %v", err)
	}
	if err := core.WriteConfigAtomic(&cfg, cfgPath); err != nil {
		t.Fatalf("write account config: %v", err)
	}

	deps, _, err := bootstrap()
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	for _, dep := range deps {
		_ = dep.Close()
	}

	loaded, err := core.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("reload account config: %v", err)
	}
	if loaded.Server.APIKey != "" {
		t.Fatalf("bootstrap wrote server.api_key to config, want secret-only storage")
	}
	secrets, err := core.LoadSecretsFrom(filepath.Join(root, "accounts", "alice", "secrets.json"))
	if err != nil {
		t.Fatalf("load account secrets: %v", err)
	}
	if key, ok := secrets.Get("local-server", "api_key"); !ok || key == "" {
		t.Fatal("bootstrap must backfill local-server api key secret for existing accounts")
	}
}

func TestBootstrapPreservesExistingSecretAccountAPIKey(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("HOME", t.TempDir())

	cfg := core.DefaultConfig()
	cfg.LLM.Provider = "anthropic"
	cfg.LLM.APIKey = "test-key"
	cfg.LLM.Model = "claude-test"
	cfg.Server.APIKey = ""
	accountDir := filepath.Join(root, "accounts", "alice")
	cfgPath := filepath.Join(accountDir, "config.toml")
	if err := os.MkdirAll(accountDir, 0o700); err != nil {
		t.Fatalf("mkdir account dir: %v", err)
	}
	if err := core.WriteConfigAtomic(&cfg, cfgPath); err != nil {
		t.Fatalf("write account config: %v", err)
	}
	secrets, err := core.LoadSecretsFrom(filepath.Join(accountDir, "secrets.json"))
	if err != nil {
		t.Fatalf("load account secrets: %v", err)
	}
	if err := secrets.Set("local-server", "api_key", "existing-key"); err != nil {
		t.Fatalf("seed local-server api key: %v", err)
	}

	deps, _, err := bootstrap()
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	for _, dep := range deps {
		_ = dep.Close()
	}

	secrets, err = core.LoadSecretsFrom(filepath.Join(accountDir, "secrets.json"))
	if err != nil {
		t.Fatalf("reload account secrets: %v", err)
	}
	if key, ok := secrets.Get("local-server", "api_key"); !ok || key != "existing-key" {
		t.Fatalf("local-server api key = (%q, %v), want existing-key/true", key, ok)
	}
}

func TestWaitForProcessExitPollsUntilProcessStops(t *testing.T) {
	oldProcessRunning := processRunning
	oldPollInterval := stopWaitPollInterval
	defer func() {
		processRunning = oldProcessRunning
		stopWaitPollInterval = oldPollInterval
	}()

	calls := 0
	processRunning = func(pid int) bool {
		if pid != 123 {
			t.Fatalf("pid = %d, want 123", pid)
		}
		calls++
		return calls < 3
	}
	stopWaitPollInterval = time.Nanosecond

	if !waitForProcessExit(123, time.Second) {
		t.Fatal("waitForProcessExit returned false before process stopped")
	}
	if calls < 3 {
		t.Fatalf("processRunning called %d times, want at least 3", calls)
	}
}

func TestEnsureSingleServerProcessRejectsOtherRunningServer(t *testing.T) {
	oldProcessRunning := processRunning
	defer func() { processRunning = oldProcessRunning }()

	dir := t.TempDir()
	pidPath := filepath.Join(dir, "daemon.pid")
	if err := os.WriteFile(pidPath, []byte("12345\n"), 0o600); err != nil {
		t.Fatalf("write pid file: %v", err)
	}
	processRunning = func(pid int) bool {
		return pid == 12345
	}

	err := ensureSingleServerProcess(pidPath, 99999)
	if err == nil {
		t.Fatal("ensureSingleServerProcess returned nil for another running server")
	}
	for _, want := range []string{"already running", "kittypaw server stop"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want containing %q", err.Error(), want)
		}
	}
	if _, err := os.Stat(pidPath); err != nil {
		t.Fatalf("pid file should remain for running server: %v", err)
	}
}

func TestEnsureSingleServerProcessRemovesStalePidFile(t *testing.T) {
	oldProcessRunning := processRunning
	defer func() { processRunning = oldProcessRunning }()

	dir := t.TempDir()
	pidPath := filepath.Join(dir, "daemon.pid")
	if err := os.WriteFile(pidPath, []byte("12345\n"), 0o600); err != nil {
		t.Fatalf("write pid file: %v", err)
	}
	processRunning = func(pid int) bool {
		return false
	}

	if err := ensureSingleServerProcess(pidPath, 99999); err != nil {
		t.Fatalf("ensureSingleServerProcess returned error for stale pid: %v", err)
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("pid file should be removed for stale server, stat err=%v", err)
	}
}

func mustWriteTestConfig(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	cfg := core.DefaultConfig()
	if err := core.WriteConfigAtomic(&cfg, path); err != nil {
		t.Fatalf("write config %s: %v", path, err)
	}
}
