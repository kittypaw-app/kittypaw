// Command kittypaw is the CLI for the KittyPaw AI runner platform.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/mattn/go-isatty"
	"github.com/mattn/go-runewidth"
	"github.com/spf13/cobra"

	"github.com/jinto/kittypaw/client"
	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/server"
	"github.com/jinto/kittypaw/store"
)

// version is set via ldflags at build time.
var version = "dev"

// flags
var (
	flagRemote  string // --remote: connect to a server instead of local discovery
	flagAccount string // --account: local account for account-scoped CLI operations
	flagBind    string // server start --bind
	flagDryRun  bool   // run --dry-run
	flagSkill   string // log --skill
	flagLimit   int    // log --limit

	accountContextPrinted bool
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	root := newRootCmd()
	if err := root.ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// Table helpers — CJK-aware padding and truncation
// ---------------------------------------------------------------------------

// padW right-pads s to exactly w display columns (CJK = 2 cols).
func padW(s string, w int) string {
	sw := runewidth.StringWidth(s)
	if sw >= w {
		return s
	}
	return s + strings.Repeat(" ", w-sw)
}

// truncW truncates s to at most w display columns, appending ".." if cut.
func truncW(s string, w int) string {
	if runewidth.StringWidth(s) <= w {
		return s
	}
	return runewidth.Truncate(s, w, "..")
}

// setupBoxInner is the inner column count of the setup completion box (CJK-aware).
const setupBoxInner = 56

// renderSetupBox writes the setup completion box to w with CJK-aware right-edge alignment.
// Each line's right `│` lands at the same column regardless of mixed Korean/ASCII content.
func renderSetupBox(w io.Writer, cfgPath string) {
	lines := []string{
		"",
		"✓ 설정 완료",
		truncW(cfgPath, setupBoxInner),
		"",
		"다음 단계",
		"  kittypaw server start    # 메시지 수신 서버 실행",
		"  kittypaw chat            # 터미널에서 바로 대화",
		"",
	}
	border := strings.Repeat("─", setupBoxInner+2)
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "  ╭"+border+"╮")
	for _, l := range lines {
		_, _ = fmt.Fprintf(w, "  │ %s │\n", padW(l, setupBoxInner))
	}
	_, _ = fmt.Fprintln(w, "  ╰"+border+"╯")
	_, _ = fmt.Fprintln(w)
}

// printSetupBox renders the setup completion box to stdout.
func printSetupBox(cfgPath string) {
	renderSetupBox(os.Stdout, cfgPath)
}

// ---------------------------------------------------------------------------
// Root
// ---------------------------------------------------------------------------

func newRootCmd() *cobra.Command {
	accountContextPrinted = false

	cmd := &cobra.Command{
		Use:          "kittypaw",
		Short:        "KittyPaw — AI runner platform",
		Version:      version,
		SilenceUsage: true,
	}

	cmd.PersistentFlags().StringVar(&flagRemote, "remote", "", "connect to remote server instead of local discovery")

	cmd.AddCommand(
		newServerCmd(),
		newSetupCmd(),
		newChatCmd(),
		newModelCmd(),
		newSkillCmd(),
		newConfigCmd(),
		newMemoryCmd(),
		newChannelsCmd(),
		newLoginCmd(),
		newConnectCmd(),
		newGmailCmd(),
		newChatRelayCmd(),
		newAccountCmd(),
		newProjectCmd(),
		newKanbanCmd(),
	)

	return cmd
}

func addAccountFlag(cmd *cobra.Command) {
	cmd.Flags().StringVar(&flagAccount, "account", "", "use this local account")
}

func addPersistentAccountFlag(cmd *cobra.Command) {
	cmd.PersistentFlags().StringVar(&flagAccount, "account", "", "use this local account")
}

// ---------------------------------------------------------------------------
// server
// ---------------------------------------------------------------------------

func newServerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Manage the local KittyPaw server",
		Long: `Manage the local KittyPaw server.

Use "server start" to run it in the current terminal. Use "server install"
to register it with the OS as a per-user background service.`,
	}
	cmd.AddCommand(
		newServerStartCmd(),
		newServerStopCmd(),
		newServerReloadCmd(),
	)
	addServerServiceCommands(cmd)
	return cmd
}

func newServerStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the local server in this terminal",
		RunE:  runServe,
	}
	cmd.Flags().StringVar(&flagBind, "bind", ":3000", "address to bind")
	return cmd
}

func runServe(cmd *cobra.Command, _ []string) error {
	deps, serverCfg, err := bootstrap()
	if err != nil {
		return err
	}
	defer func() {
		for _, td := range deps {
			_ = td.Close()
		}
	}()
	bind := resolveServeBind(cmd, serverCfg, deps)

	// Check port availability before starting channels.
	if err := checkPort(bind); err != nil {
		return err
	}

	// Write PID file so `kittypaw server stop` can find us.
	writePidFile()
	defer removePidFile()

	// The server owns the engine session, channel spawner, and dispatch loop.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	srv := server.NewWithServerConfig(deps, version, serverCfg)
	if err := srv.StartChannels(ctx); err != nil {
		return fmt.Errorf("start channels: %w", err)
	}
	startChatRelayConnectors(ctx, deps, version, server.NewChatRelayDispatcher(srv))

	// Start HTTP server (blocks until shutdown signal).
	slog.Info("kittypaw serving", "bind", bind)
	return srv.ListenAndServe(bind)
}

func resolveServeBind(cmd *cobra.Command, serverCfg core.TopLevelServerConfig, deps []*server.AccountDeps) string {
	if cmd != nil && cmd.Flags().Changed("bind") {
		return flagBind
	}
	if serverCfg.Bind != "" {
		return serverCfg.Bind
	}
	if selected := server.SelectDefaultAccountDeps(deps, serverCfg.DefaultAccount); selected != nil && selected.Account != nil && selected.Account.Config != nil {
		return selected.Account.Config.Server.BindOrDefault()
	}
	return flagBind
}

// ---------------------------------------------------------------------------
// init
// ---------------------------------------------------------------------------

func newSetupCmd() *cobra.Command {
	flags := &setupFlags{}
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Set up or reconfigure KittyPaw",
		Long:  "Set up KittyPaw interactively (LLM, channels, web search, workspace) or via flags for CI.",
		RunE: func(c *cobra.Command, _ []string) error {
			return runSetup(c, flags)
		},
	}
	cmd.Flags().StringVar(&flags.provider, "provider", "", "LLM provider (anthropic|openai|gemini|openrouter|local)")
	cmd.Flags().StringVar(&flags.accountID, "account", "", "local account id")
	cmd.Flags().BoolVar(&flags.passwordStdin, "password-stdin", false, "read local Web UI password from stdin")
	cmd.Flags().StringVar(&flags.apiKey, "api-key", "", "LLM API key")
	cmd.Flags().StringVar(&flags.localURL, "local-url", "", "Local LLM URL (default: http://localhost:11434/v1)")
	cmd.Flags().StringVar(&flags.localModel, "local-model", "", "Local LLM model name")
	cmd.Flags().StringArrayVar(&flags.extraModels, "extra-model", nil, "Additional named model for /model, format: id=<id>,provider=<provider>,model=<model>[,base_url=<url>] (repeatable)")
	cmd.Flags().StringArrayVar(&flags.extraModelKeys, "extra-model-api-key", nil, "API key for an --extra-model, format: <id>=<api-key> (repeatable; visible in ps like --api-key)")
	cmd.Flags().StringVar(&flags.telegramToken, "telegram-token", "", "Telegram bot token")
	cmd.Flags().StringVar(&flags.telegramChatID, "telegram-chat-id", "", "Telegram chat ID")
	cmd.Flags().StringVar(&flags.firecrawlKey, "firecrawl-api-key", "", "Firecrawl API key for web search")
	cmd.Flags().StringVar(&flags.workspace, "workspace", "", "Workspace directory path")
	cmd.Flags().BoolVar(&flags.httpAccess, "http-access", false, "Grant HTTP access capability")
	cmd.Flags().BoolVar(&flags.force, "force", false, "Overwrite existing config without confirmation")
	cmd.Flags().BoolVar(&flags.noChat, "no-chat", false, "Skip the post-setup chat REPL prompt (auto-entry)")
	cmd.Flags().BoolVar(&flags.noService, "no-service", false, "Skip the post-setup service-install prompt")
	return cmd
}

func runSetup(cmd *cobra.Command, flags *setupFlags) error {
	cfgDir, err := core.ConfigDir()
	if err != nil {
		return err
	}

	accountID, err := resolveSetupAccount(*flags, cfgDir)
	if err != nil {
		return err
	}
	cfgPath, err := core.ConfigPathForAccount(accountID)
	if err != nil {
		return err
	}
	accountDir := filepath.Dir(cfgPath)

	authPath, err := core.LocalAuthPath()
	if err != nil {
		return err
	}
	authStore := core.NewLocalAuthStore(authPath)
	hasAuth, err := authStore.HasUser(accountID)
	if err != nil {
		return err
	}
	var localPassword string
	if !hasAuth {
		localPassword, err = resolveSetupPassword(*flags, cmd.InOrStdin())
		if err != nil {
			return err
		}
	}

	var existing *core.Config
	if cfg, err := core.LoadConfig(cfgPath); err == nil {
		existing = cfg
	}

	base := core.DefaultConfig()
	if existing != nil {
		base = *existing
	}
	validateResult := func(w core.WizardResult) error {
		merged := core.MergeWizardSettings(&base, w)
		return validateSetupConfig(cfgDir, accountID, merged)
	}
	wizardFlags := *flags
	wizardFlags.accountID = accountID
	wizardFlags.validate = validateResult

	// Run wizard.
	result, err := setupWizardRunner(wizardFlags, existing)
	if err != nil {
		return err
	}

	// Merge and write config.
	merged := core.MergeWizardSettings(&base, result)
	if _, err := core.EnsureServerAPIKey(merged); err != nil {
		return err
	}
	if err := validateSetupConfig(cfgDir, accountID, merged); err != nil {
		return err
	}
	if err := core.SaveWizardSecrets(accountID, result, merged); err != nil {
		return fmt.Errorf("save setup secrets: %w", err)
	}
	if err := os.MkdirAll(accountDir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", accountDir, err)
	}
	if !hasAuth {
		if err := authStore.CreateUser(accountID, localPassword); err != nil {
			return fmt.Errorf("create local auth user: %w", err)
		}
	}
	if err := core.WriteConfigAtomic(merged, cfgPath); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	// HTTP access grant (requires store).
	if result.HTTPAccess {
		if st, err := openStoreForAccount(accountID); err == nil {
			_ = st.GrantCapability("http")
			_ = st.Close()
		}
	}

	// Save API server URL to per-account secrets for package source bindings.
	if result.APIServerURL != "" {
		if secrets, err := core.LoadAccountSecrets(accountID); err == nil {
			_ = secrets.Set("kittypaw-api", "api_url", result.APIServerURL)
		}
	}

	if err := core.EnsureDefaultStaff(accountDir); err != nil {
		return fmt.Errorf("ensure default staff: %w", err)
	}

	// Ask a live server to reload before we display the completion box — a
	// subsequent `kittypaw chat` connects to a server that already sees the
	// new config (AC-RELOAD-SYNC). Outcome gates auto-entry below.
	reloadRes := maybeReloadServer(defaultServerDial, os.Stdout, os.Stderr)

	printSetupBox(cfgPath)

	// Share a single stdin scanner across the service-install and chat
	// prompts so one prompt's unread bytes don't get swallowed by a fresh
	// bufio.Scanner on the next.
	stdinTTY := isatty.IsTerminal(os.Stdin.Fd())
	stdoutTTY := isatty.IsTerminal(os.Stdout.Fd())
	serviceEligible := serviceInstallEligible(*flags, stdinTTY, stdoutTTY)
	chatEligible := autoChatEligible(*flags, stdinTTY, stdoutTTY)

	var scanner *bufio.Scanner
	if serviceEligible || chatEligible {
		scanner = bufio.NewScanner(os.Stdin)
	}

	if serviceEligible {
		_ = maybeInstallService(scanner, os.Stdout, os.Stderr)
	}

	// Auto-entry: when setup ran interactively, offer to drop straight into
	// the chat REPL. Non-interactive (provider flag set) and explicit
	// --no-chat paths skip this entirely (AC-1 / AC-2 / AC-3).
	if !chatEligible {
		return nil
	}
	// If a live server refused our reload, chat would attach to a server that
	// still holds the PREVIOUS config — silently running the old LLM key /
	// channels. Surface that and bail out instead of auto-entering.
	if reloadRes == reloadOutcomeFailed {
		_, _ = fmt.Fprintln(os.Stderr, setupMsgAutoChatBlocked)
		return nil
	}
	if !promptYesNo(scanner, setupPromptAutoChat, true) {
		return nil
	}
	return runChat(cmd, nil)
}

var setupWizardRunner = runWizard

func validateSetupConfig(cfgDir, accountID string, cfg *core.Config) error {
	accounts, err := core.DiscoverAccounts(filepath.Join(cfgDir, "accounts"))
	if err != nil {
		return err
	}

	snapshot := make(map[string][]core.ChannelConfig, len(accounts)+1)
	finalAccounts := make([]*core.Account, 0, len(accounts)+1)
	seen := false
	for _, peer := range accounts {
		if peer == nil || peer.Config == nil {
			continue
		}
		if peer.ID == accountID {
			proposed := *peer
			proposed.Config = cfg
			finalAccounts = append(finalAccounts, &proposed)
			snapshot[accountID] = cfg.Channels
			seen = true
			continue
		}
		finalAccounts = append(finalAccounts, peer)
		channels := append([]core.ChannelConfig(nil), peer.Config.Channels...)
		core.InjectChannelSecrets(peer.ID, channels)
		snapshot[peer.ID] = channels
	}
	if !seen {
		finalAccounts = append(finalAccounts, &core.Account{
			ID:      accountID,
			BaseDir: filepath.Join(cfgDir, "accounts", accountID),
			Config:  cfg,
		})
		snapshot[accountID] = cfg.Channels
	}

	if err := core.ValidateAccountChannels(snapshot); err != nil {
		return fmt.Errorf("channel validation: %w", err)
	}
	if err := core.ValidateTeamSpaceAccounts(finalAccounts); err != nil {
		return fmt.Errorf("team space validation: %w", err)
	}
	if err := core.ValidateTeamSpaceMemberships(finalAccounts); err != nil {
		return fmt.Errorf("team-space membership validation: %w", err)
	}
	return nil
}

func resolveSetupAccount(flags setupFlags, cfgDir string) (string, error) {
	if flags.accountID != "" {
		if err := core.ValidateAccountID(flags.accountID); err != nil {
			return "", err
		}
		return flags.accountID, nil
	}

	accounts, err := core.DiscoverAccounts(filepath.Join(cfgDir, "accounts"))
	if err != nil {
		return "", err
	}
	if len(accounts) == 0 {
		if flags.provider != "" || !isTTY() {
			return "", errors.New("--account is required for first setup")
		}
		return promptSetupAccountID()
	}

	if env := strings.TrimSpace(os.Getenv("KITTYPAW_ACCOUNT")); env != "" {
		if err := core.ValidateAccountID(env); err != nil {
			return "", err
		}
		if !setupAccountExists(accounts, env) {
			return "", fmt.Errorf("KITTYPAW_ACCOUNT %q does not match an existing account; pass --account to create a new account", env)
		}
		return env, nil
	}
	if len(accounts) == 1 {
		return accounts[0].ID, nil
	}

	ids := make([]string, 0, len(accounts))
	for _, a := range accounts {
		ids = append(ids, a.ID)
	}
	sort.Strings(ids)
	return "", fmt.Errorf("multiple accounts found (%s); pass --account or set KITTYPAW_ACCOUNT", strings.Join(ids, ", "))
}

func setupAccountExists(accounts []*core.Account, id string) bool {
	for _, account := range accounts {
		if account.ID == id {
			return true
		}
	}
	return false
}

func promptSetupAccountID() (string, error) {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println()
	fmt.Println(accountCredentialsIntroMessage(""))
	fmt.Println()
	for {
		id := promptLine(scanner, "  Account ID", "")
		if id == "" {
			fmt.Println("  Account ID is required.")
			continue
		}
		if err := core.ValidateAccountID(id); err != nil {
			fmt.Printf("  %v\n", err)
			continue
		}
		return id, nil
	}
}

func resolveSetupPassword(flags setupFlags, stdin io.Reader) (string, error) {
	if flags.passwordStdin {
		scanner := bufio.NewScanner(stdin)
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return "", fmt.Errorf("read password from stdin: %w", err)
			}
			return "", errors.New("password is required")
		}
		password := scanner.Text()
		if password == "" {
			return "", errors.New("password is required")
		}
		return password, nil
	}
	if flags.provider != "" || !isTTY() {
		return "", errors.New("--password-stdin is required for first setup")
	}
	return promptLocalPassword()
}

func promptLocalPassword() (string, error) {
	for {
		password, err := promptPassword("  Local Web UI password: ")
		if err != nil {
			return "", err
		}
		if password == "" {
			fmt.Println("  Password is required.")
			continue
		}
		confirm, err := promptPassword("  Confirm password: ")
		if err != nil {
			return "", err
		}
		if password != confirm {
			fmt.Println("  Passwords do not match.")
			continue
		}
		return password, nil
	}
}

// ---------------------------------------------------------------------------
// chat
// ---------------------------------------------------------------------------

func newChatCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "chat [message]",
		Short: "Interactive chat in terminal (or one-shot with argument)",
		Args:  cobra.ArbitraryArgs,
		RunE:  runChat,
	}
	addPersistentAccountFlag(cmd)
	cmd.AddCommand(
		newChatHistoryCmd(),
		newChatForgetCmd(),
		newChatCompactCmd(),
	)
	return cmd
}

func runChat(cmd *cobra.Command, args []string) error {
	oneShot := strings.Join(args, " ")

	accountID := ""
	if flagRemote == "" {
		var err error
		accountID, err = resolveCLIAccountWithContext(flagAccount)
		if err != nil {
			return err
		}
	}

	conn, err := client.NewDaemonConnForAccount(flagRemote, accountID)
	if err != nil {
		return err
	}
	if accountID == "" {
		accountID = conn.AccountID
	}

	// Auto-start the local server if needed; it stays resident across chat
	// sessions. Users free resources via `kittypaw server stop`.
	if _, err := conn.Connect(); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Show server info.
	cl := client.New(conn.BaseURL, conn.APIKey)
	srvVer, model, channels, _ := cl.ServerInfo()
	header := formatChatHeader(version, srvVer, model, accountID, channels)
	warning := versionMismatchWarning(version, srvVer)

	cs, err := client.DialChat(ctx, conn.WebSocketURL(), conn.APIKey)
	if err != nil {
		return err
	}

	// Per-account history so a household using multiple
	// accounts (one human user per account per CLAUDE.md) does not leak chat
	// fragments from one person's REPL into another's. Falls back to the
	// top-level path before migration.
	historyFile := ""
	if base, err := defaultAccountBase(); err == nil {
		historyFile = filepath.Join(base, "chat_history")
	}

	// One-shot mode: send the message, print result, exit.
	if oneShot != "" {
		defer cs.Close()
		fmt.Println(header)
		if warning != "" {
			fmt.Println("  " + warning)
		}
		fmt.Println()
		spin := newSpinner("paw> ")
		spin.Start()
		var chatErr error
		sendErr := cs.Send(oneShot, client.ChatOptions{
			OnDone: func(result string, _ *int64) {
				spin.Stop()
				fmt.Println(result)
			},
			OnError: func(msg string) {
				spin.Stop()
				chatErr = fmt.Errorf("%s", msg)
			},
		})
		spin.Stop()
		if sendErr != nil {
			return sendErr
		}
		return chatErr
	}

	return runInteractiveChatTUI(ctx, conn, cs, header, warning, historyFile)
}

func newChatHistoryCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "history",
		Short: "Show account conversation history",
		RunE: func(_ *cobra.Command, _ []string) error {
			return runChatHistory(limit)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 50, "number of turns to show")
	return cmd
}

func runChatHistory(limit int) error {
	if flagRemote != "" {
		cl, err := connectServerForCLIAccount()
		if err != nil {
			return err
		}
		res, err := cl.ChatHistory(limit)
		if err != nil {
			return fmt.Errorf("chat history: %w", err)
		}
		return printConversationMapRows(jsonSlice(res, "turns"))
	}

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close() //nolint:errcheck

	turns, err := st.ListConversationTurns(limit)
	if err != nil {
		return fmt.Errorf("chat history: %w", err)
	}
	return printConversationRecords(turns)
}

func newChatForgetCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "forget",
		Short: "Forget account conversation history",
		RunE: func(_ *cobra.Command, _ []string) error {
			return runChatForget(yes)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip confirmation")
	return cmd
}

func runChatForget(yes bool) error {
	if !yes {
		if !isTTY() {
			return errors.New("refusing to forget conversation history without --yes in non-interactive mode")
		}
		scanner := bufio.NewScanner(os.Stdin)
		if !promptYesNo(scanner, "Forget all account conversation history?", false) {
			fmt.Println("Canceled.")
			return nil
		}
	}

	if flagRemote != "" {
		cl, err := connectServerForCLIAccount()
		if err != nil {
			return err
		}
		res, err := cl.ChatForget()
		if err != nil {
			return fmt.Errorf("chat forget: %w", err)
		}
		fmt.Printf("Forgot %d conversation turns.\n", jsonInt(res, "turns_deleted"))
		return nil
	}

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close() //nolint:errcheck
	deleted, err := st.ForgetConversation()
	if err != nil {
		return fmt.Errorf("chat forget: %w", err)
	}
	fmt.Printf("Forgot %d conversation turns.\n", deleted)
	return nil
}

func newChatCompactCmd() *cobra.Command {
	var keepRecent int
	cmd := &cobra.Command{
		Use:   "compact",
		Short: "Compact older account conversation history",
		RunE: func(_ *cobra.Command, _ []string) error {
			return runChatCompact(keepRecent)
		},
	}
	cmd.Flags().IntVar(&keepRecent, "keep-recent", 40, "recent turns to keep verbatim")
	return cmd
}

func runChatCompact(keepRecent int) error {
	if flagRemote != "" {
		cl, err := connectServerForCLIAccount()
		if err != nil {
			return err
		}
		res, err := cl.ChatCompact(keepRecent)
		if err != nil {
			return fmt.Errorf("chat compact: %w", err)
		}
		fmt.Printf("Compacted %d older conversation turns.\n", jsonInt(res, "turns_compacted"))
		return nil
	}

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close() //nolint:errcheck
	compacted, err := st.CompactConversation(keepRecent)
	if err != nil {
		return fmt.Errorf("chat compact: %w", err)
	}
	fmt.Printf("Compacted %d older conversation turns.\n", compacted)
	return nil
}

func printConversationRecords(turns []store.ConversationTurnRecord) error {
	if len(turns) == 0 {
		fmt.Println("No conversation turns found.")
		return nil
	}
	fmt.Printf("%s %s %s %s %s\n", padW("ID", 6), padW("ROLE", 10), padW("CHANNEL", 12), padW("TIME", 16), "CONTENT")
	fmt.Println(strings.Repeat("-", 88))
	for _, t := range turns {
		fmt.Printf("%s %s %s %s %s\n",
			padW(fmt.Sprintf("%d", t.ID), 6),
			padW(string(t.Role), 10),
			padW(truncW(t.Channel, 12), 12),
			padW(truncW(t.Timestamp, 16), 16),
			truncW(t.Content, 80),
		)
	}
	return nil
}

func printConversationMapRows(turns []map[string]any) error {
	if len(turns) == 0 {
		fmt.Println("No conversation turns found.")
		return nil
	}
	fmt.Printf("%s %s %s %s %s\n", padW("ID", 6), padW("ROLE", 10), padW("CHANNEL", 12), padW("TIME", 16), "CONTENT")
	fmt.Println(strings.Repeat("-", 88))
	for _, t := range turns {
		fmt.Printf("%s %s %s %s %s\n",
			padW(fmt.Sprintf("%d", jsonInt(t, "id")), 6),
			padW(jsonStr(t, "role"), 10),
			padW(truncW(jsonStr(t, "channel"), 12), 12),
			padW(truncW(jsonStr(t, "timestamp"), 16), 16),
			truncW(jsonStr(t, "content"), 80),
		)
	}
	return nil
}

func formatChatHeader(cliVersion, serverVersion, model, accountID string, channels []string) string {
	parts := []string{"KittyPaw chat"}
	if accountID != "" {
		parts = append(parts, accountID)
	}
	if model != "" {
		parts = append(parts, model)
	}
	if len(channels) > 0 {
		parts = append(parts, strings.Join(channels, ","))
	}
	if len(parts) == 1 {
		if serverVersion != "" {
			parts = append(parts, "server "+serverVersion)
		} else if cliVersion != "" {
			parts = append(parts, "cli "+cliVersion)
		}
	}
	return strings.Join(parts, " · ")
}

func versionMismatchWarning(cliVersion, serverVersion string) string {
	if serverVersion != "" && cliVersion != serverVersion {
		return fmt.Sprintf("CLI/server versions differ (cli %s, server %s). Restart: kittypaw server stop && kittypaw server start", cliVersion, serverVersion)
	}
	return ""
}

// isTransportDropErr reports whether err is a transient WebSocket
// teardown that the silent-reconnect path should swallow (EOF,
// broken pipe, closed conn, reset). Errors carrying client.ErrServerSide
// are application-layer failures and never qualify — replaying them
// would double-charge the user.
func isTransportDropErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, client.ErrServerSide) {
		return false
	}
	if errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, net.ErrClosed) {
		return true
	}
	// Substring fallback for libraries that surface raw text errors
	// without wrapping a typed sentinel.
	msg := err.Error()
	return strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "connection reset by peer")
}

// ---------------------------------------------------------------------------
// skill stats
// ---------------------------------------------------------------------------

func newSkillStatsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show skill execution stats",
		RunE:  runSkillStats,
	}
	return cmd
}

func runSkillStats(_ *cobra.Command, _ []string) error {
	cl, err := connectServerForCLIAccount()
	if err != nil {
		return err
	}

	res, err := cl.Status()
	if err != nil {
		return fmt.Errorf("query stats: %w", err)
	}

	fmt.Println("Today's execution stats")
	fmt.Println("-----------------------")
	fmt.Printf("  Total runs:   %d\n", jsonInt(res, "total_runs"))
	fmt.Printf("  Successful:   %d\n", jsonInt(res, "successful"))
	fmt.Printf("  Failed:       %d\n", jsonInt(res, "failed"))
	fmt.Printf("  Auto-retries: %d\n", jsonInt(res, "auto_retries"))
	fmt.Printf("  Total tokens: %d\n", jsonInt(res, "total_tokens"))
	fmt.Printf("  Est. cost:    $%.6f\n", jsonFloat(res, "estimated_cost_usd"))
	return nil
}

// ---------------------------------------------------------------------------
// skill — unified skill management
// ---------------------------------------------------------------------------

func newSkillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Manage skills",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath())
			}
			return cmd.Help()
		},
	}
	addPersistentAccountFlag(cmd)
	cmd.AddCommand(
		newSkillListCmd(),
		newSkillSearchCmd(),
		newSkillInstallCmd(),
		newSkillUninstallCmd(),
		newSkillInfoCmd(),
		newSkillCreateCmd(),
		newSkillEnableCmd(),
		newSkillDisableCmd(),
		newSkillExplainCmd(),
		newSkillRunCmd(),
		newSkillLogCmd(),
		newSkillStatsCmd(),
		newSkillConfigCmd(),
	)
	return cmd
}

// --- skill list ---

func newSkillListCmd() *cobra.Command {
	var filterType string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all skills",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := resolveCLIAccountWithContext(flagAccount)
			if err != nil {
				return err
			}

			var skills []map[string]any
			if filterType == "" || filterType == "skill" {
				if cl, err := connectServerForCLIAccount(); err == nil {
					if res, err := cl.Skills(); err == nil {
						skills = jsonSlice(res, "skills")
					}
				}
			}

			var packages []core.SkillPackage
			if filterType == "" || filterType == "package" {
				if pm, err := localPackageManager(); err == nil {
					packages, _ = pm.ListInstalled()
				}
			}

			if len(skills) == 0 && len(packages) == 0 {
				fmt.Println("No skills found.")
				return nil
			}

			fmt.Printf("%s %s %s %s %s\n", padW("NAME", 20), padW("TYPE", 10), padW("VERSION", 10), padW("STATUS", 10), "DESCRIPTION")
			fmt.Println(strings.Repeat("-", 85))

			for _, s := range skills {
				status := "enabled"
				if !jsonBool(s, "enabled") {
					status = "disabled"
				}
				desc := truncW(jsonStr(s, "description"), 30)
				fmt.Printf("%s %s %s %s %s\n",
					padW(truncW(jsonStr(s, "name"), 20), 20), padW("skill", 10), padW(jsonStr(s, "version"), 10), padW(status, 10), desc)
			}

			for _, p := range packages {
				status := "installed"
				if p.Meta.Cron != "" {
					status = "cron"
				}
				desc := truncW(p.Meta.Description, 30)
				fmt.Printf("%s %s %s %s %s\n",
					padW(truncW(p.Meta.ID, 20), 20), padW("package", 10), padW(p.Meta.Version, 10), padW(status, 10), desc)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&filterType, "type", "", "filter by type: skill or package")
	return cmd
}

// --- skill search ---

func newSkillSearchCmd() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "search [query]",
		Short: "Search the skill registry",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			var query string
			if len(args) > 0 {
				query = args[0]
			}
			rc, err := registryClient()
			if err != nil {
				return err
			}
			idx, err := rc.FetchIndexWithMeta()
			if err != nil {
				return err
			}
			results := core.FilterEntries(idx.Entries, query)

			if jsonOutput {
				out := map[string]any{"results": results, "from_cache": idx.FromCache}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}

			if idx.FromCache {
				ts := "unknown"
				if !idx.CachedAt.IsZero() {
					ts = idx.CachedAt.Format("2006-01-02 15:04")
				}
				fmt.Printf("(cached results -- last updated: %s)\n\n", ts)
			}

			if len(results) == 0 {
				fmt.Println("No results found.")
				return nil
			}

			fmt.Printf("%s %s %s %s %s\n", padW("ID", 20), padW("NAME", 25), padW("VERSION", 10), padW("AUTHOR", 12), "DESCRIPTION")
			fmt.Println(strings.Repeat("-", 95))
			for _, e := range results {
				desc := truncW(e.Description, 40)
				name := truncW(e.Name, 25)
				author := truncW(e.Author, 12)
				fmt.Printf("%s %s %s %s %s\n", padW(e.ID, 20), padW(name, 25), padW(e.Version, 10), padW(author, 12), desc)
			}
			fmt.Printf("\nFound %d package(s).\n", len(results))
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON")
	return cmd
}

// --- skill install ---

func newSkillInstallCmd() *cobra.Command {
	var mdMode string
	cmd := &cobra.Command{
		Use:   "install <source>",
		Short: "Install a skill from GitHub, local path, or registry",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			arg := args[0]

			// GitHub URL or local path -> server install.
			if strings.HasPrefix(arg, "https://") || strings.HasPrefix(arg, "http://") {
				return installViaServer(arg, mdMode)
			}

			fi, statErr := os.Stat(arg)
			if statErr != nil && !os.IsNotExist(statErr) {
				return statErr
			}
			if statErr == nil && fi.IsDir() {
				absPath, _ := filepath.Abs(arg)
				return installViaServer(absPath, mdMode)
			}

			// Otherwise treat as a registry package ID.
			accountID, err := resolveCLIAccountWithContext(flagAccount)
			if err != nil {
				return err
			}
			secrets, err := core.LoadAccountSecrets(accountID)
			if err != nil {
				return err
			}
			pm, err := localPackageManager()
			if err != nil {
				return err
			}
			rc, err := registryClient()
			if err != nil {
				return err
			}
			entry, err := rc.FindEntry(arg)
			if err != nil {
				return err
			}

			// Check for conflicting user skill before install.
			skillBase, baseErr := accountBaseForID(accountID)
			if baseErr != nil {
				return fmt.Errorf("resolve account base: %w", baseErr)
			}
			if skillBase != "" {
				if existingSkill, _, loadErr := core.LoadSkillFrom(skillBase, entry.ID); loadErr == nil && existingSkill != nil {
					fmt.Println("⚠  같은 이름의 사용자 스킬이 이미 존재합니다.")
					fmt.Println()
					fmt.Printf("  [사용자 스킬]  %s\n", existingSkill.Name)
					fmt.Printf("    설명: %s\n", existingSkill.Description)
					fmt.Printf("    생성: %s\n", existingSkill.CreatedAt)
					fmt.Println()
					fmt.Printf("  [패키지]  %s (%s) v%s\n", entry.Name, entry.ID, entry.Version)
					fmt.Printf("    설명: %s\n", entry.Description)
					fmt.Println()
					fmt.Println("  사용자 스킬이 패키지보다 우선 실행됩니다.")
					fmt.Println("  A. 사용자 스킬을 삭제하고 패키지 설치")
					fmt.Println("  B. 사용자 스킬 유지 (패키지도 설치하되 실행되지 않음)")
					fmt.Println("  C. 설치 취소")
					fmt.Print("  선택 [A/B/C]: ")

					var choice string
					fmt.Scanln(&choice)
					choice = strings.TrimSpace(strings.ToUpper(choice))

					switch choice {
					case "A":
						if delErr := core.DeleteSkillFrom(skillBase, entry.ID); delErr != nil {
							return fmt.Errorf("사용자 스킬 삭제 실패: %w", delErr)
						}
						fmt.Printf("  사용자 스킬 %q 삭제 완료.\n", entry.ID)
					case "B":
						fmt.Println("  사용자 스킬 유지. 패키지 설치를 계속합니다.")
					default:
						fmt.Println("  설치 취소.")
						return nil
					}
				}
			}

			pkg, err := pm.InstallFromRegistry(rc, *entry)
			if err != nil {
				return err
			}
			fmt.Printf("Installed package %q (%s) v%s from registry\n",
				pkg.Meta.Name, pkg.Meta.ID, pkg.Meta.Version)

			// API-bound skills need a login. Install still succeeds — we just
			// warn early so the user sees the requirement instead of a
			// cryptic 401 at run time.
			if pkg.RequiresAPILogin() {
				mgr := core.NewAPITokenManager("", secrets)
				if tok, _ := mgr.LoadAccessToken(core.DefaultAPIServerURL); tok == "" {
					fmt.Fprintln(os.Stderr)
					fmt.Fprintln(os.Stderr, "  ℹ  이 스킬은 KittyPaw API 로그인이 필요합니다.")
					fmt.Fprintln(os.Stderr, "     kittypaw login 으로 로그인해 주세요.")
				}
			}

			return promptPackageConfig(pm, pkg)
		},
	}
	cmd.Flags().StringVar(&mdMode, "mode", "", "SKILL.md execution mode (prompt or native)")
	return cmd
}

func installViaServer(source, mdMode string) error {
	cl, err := connectServerForCLIAccount()
	if err != nil {
		return err
	}
	result, err := cl.Install(source, mdMode)
	if err != nil {
		return err
	}
	fmt.Printf("Installed: %s (format: %s)\n",
		jsonStr(result, "SkillName"), jsonStr(result, "Format"))
	return nil
}

// --- skill uninstall ---

func newSkillUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall <name>",
		Short: "Uninstall a skill or package",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]

			// Try package first (local, fast).
			if pm, err := localPackageManager(); err == nil {
				if err := pm.Uninstall(name); err == nil {
					fmt.Printf("Package %q uninstalled.\n", name)
					return nil
				}
			}

			// Fall back to skill deletion via server.
			cl, err := connectServerForCLIAccount()
			if err != nil {
				return err
			}
			if _, err := cl.DeleteSkill(name); err != nil {
				return err
			}
			fmt.Printf("Skill %q deleted.\n", name)
			return nil
		},
	}
}

// --- skill info ---

func newSkillInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info <name>",
		Short: "Show details of an installed skill or package",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]

			// Try package first.
			if pm, err := localPackageManager(); err == nil {
				if pkg, _, loadErr := pm.LoadPackage(name); loadErr == nil {
					printPackageInfo(pm, pkg)
					return nil
				}
			}

			// Fall back to skill via server.
			cl, err := connectServerForCLIAccount()
			if err != nil {
				return err
			}
			res, err := cl.Skills()
			if err != nil {
				return err
			}
			for _, s := range jsonSlice(res, "skills") {
				if jsonStr(s, "name") == name {
					fmt.Printf("Name:        %s\n", jsonStr(s, "name"))
					fmt.Printf("Type:        skill\n")
					if v := jsonStr(s, "version"); v != "" {
						fmt.Printf("Version:     %s\n", v)
					}
					if d := jsonStr(s, "description"); d != "" {
						fmt.Printf("Description: %s\n", d)
					}
					fmt.Printf("Enabled:     %v\n", jsonBool(s, "enabled"))
					if t := jsonStr(s, "trigger"); t != "" {
						fmt.Printf("Trigger:     %s\n", t)
					}
					return nil
				}
			}
			return fmt.Errorf("skill %q not found", name)
		},
	}
}

func printPackageInfo(pm *core.PackageManager, pkg *core.SkillPackage) {
	fmt.Printf("ID:          %s\n", pkg.Meta.ID)
	fmt.Printf("Name:        %s\n", pkg.Meta.Name)
	fmt.Printf("Type:        package\n")
	fmt.Printf("Version:     %s\n", pkg.Meta.Version)
	if pkg.Meta.Description != "" {
		fmt.Printf("Description: %s\n", pkg.Meta.Description)
	}
	if pkg.Meta.Author != "" {
		fmt.Printf("Author:      %s\n", pkg.Meta.Author)
	}
	if pkg.Meta.Cron != "" {
		fmt.Printf("Cron:        %s\n", pkg.Meta.Cron)
	}
	if pkg.Meta.Model != "" {
		fmt.Printf("Model:       %s\n", pkg.Meta.Model)
	}
	if len(pkg.Config) > 0 {
		fmt.Println("\nConfig Fields:")
		cfg, _ := pm.GetConfig(pkg.Meta.ID)
		for _, f := range pkg.Config {
			val := cfg[f.Key]
			if f.Secret && val != "" {
				val = "****"
			}
			req := ""
			if f.Required {
				req = " (required)"
			}
			fmt.Printf("  %-20s %s%s\n", f.Key, val, req)
		}
	}
	if len(pkg.Permissions.Primitives) > 0 {
		fmt.Printf("\nPermissions: %s\n", strings.Join(pkg.Permissions.Primitives, ", "))
	}
	if len(pkg.Permissions.AllowedHosts) > 0 {
		fmt.Printf("Hosts:       %s\n", strings.Join(pkg.Permissions.AllowedHosts, ", "))
	}
}

// --- skill create ---

func newSkillCreateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create <description...>",
		Short: "Create a new skill from description",
		Args:  cobra.MinimumNArgs(1),
		RunE:  runTeach,
	}
}

// --- skill enable / disable / delete / explain ---

func newSkillEnableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "enable <name>",
		Short: "Enable a skill",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cl, err := connectServerForCLIAccount()
			if err != nil {
				return err
			}
			if _, err := cl.EnableSkill(args[0]); err != nil {
				return err
			}
			fmt.Printf("Skill %q enabled.\n", args[0])
			return nil
		},
	}
}

func newSkillDisableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "disable <name>",
		Short: "Disable a skill",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cl, err := connectServerForCLIAccount()
			if err != nil {
				return err
			}
			if _, err := cl.DisableSkill(args[0]); err != nil {
				return err
			}
			fmt.Printf("Skill %q disabled.\n", args[0])
			return nil
		},
	}
}

func newSkillExplainCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "explain <name>",
		Short: "Explain what a skill does",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cl, err := connectServerForCLIAccount()
			if err != nil {
				return err
			}
			res, err := cl.ExplainSkill(args[0])
			if err != nil {
				return err
			}
			fmt.Println(jsonStr(res, "explanation"))
			return nil
		},
	}
}

// --- skill config ---

func newSkillConfigCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "config <name> [key] [value]",
		Short: "Get or set skill configuration",
		Args:  cobra.RangeArgs(1, 3),
		RunE: func(_ *cobra.Command, args []string) error {
			pm, err := localPackageManager()
			if err != nil {
				return err
			}
			id := args[0]

			if len(args) == 1 {
				cfg, err := pm.GetConfig(id)
				if err != nil {
					return err
				}
				if len(cfg) == 0 {
					fmt.Println("No configuration fields.")
					return nil
				}
				for k, v := range cfg {
					fmt.Printf("  %s = %s\n", k, v)
				}
				return nil
			}
			if len(args) == 3 {
				return pm.SetConfig(id, args[1], args[2])
			}
			return fmt.Errorf("usage: kittypaw skill config <name> [key value]")
		},
	}
}

// ---------------------------------------------------------------------------
// run
// ---------------------------------------------------------------------------

func newSkillRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <name>",
		Short: "Run a skill by name",
		Args:  cobra.ExactArgs(1),
		RunE:  runSkill,
	}
	cmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "show what would happen without executing")
	return cmd
}

func runSkill(_ *cobra.Command, args []string) error {
	name := args[0]

	if flagDryRun {
		// Dry run stays local; no server needed.
		base, err := defaultAccountBaseWithContext()
		if err != nil {
			return err
		}
		skill, code, err := core.LoadSkillFrom(base, name)
		if err != nil {
			return fmt.Errorf("load skill: %w", err)
		}
		if skill == nil {
			return fmt.Errorf("skill %q not found", name)
		}
		fmt.Printf("Skill:       %s\n", skill.Name)
		fmt.Printf("Description: %s\n", skill.Description)
		fmt.Printf("Trigger:     %s\n", skill.Trigger.Type)
		fmt.Printf("Code length: %d bytes\n", len(code))
		fmt.Println("\n(dry run — not executed)")
		return nil
	}

	cl, err := connectServerForCLIAccount()
	if err != nil {
		return err
	}

	res, err := cl.RunSkill(name)
	if err != nil {
		return fmt.Errorf("run skill: %w", err)
	}
	if output := jsonStr(res, "output"); output != "" {
		fmt.Println(output)
	}
	return nil
}

// ---------------------------------------------------------------------------
// teach (shared logic, used by skill create)
// ---------------------------------------------------------------------------

func runTeach(_ *cobra.Command, args []string) error {
	description := strings.Join(args, " ")

	cl, err := connectServerForCLIAccount()
	if err != nil {
		return err
	}

	res, err := cl.Teach(description)
	if err != nil {
		return fmt.Errorf("teach skill: %w", err)
	}

	name := jsonStr(res, "skill_name")
	desc := jsonStr(res, "description")
	code := jsonStr(res, "code")
	syntaxOK := jsonBool(res, "syntax_ok")
	syntaxErr := jsonStr(res, "syntax_error")

	triggerType := ""
	if trig, ok := res["trigger"].(map[string]any); ok {
		triggerType = jsonStr(trig, "type")
	}

	// Print preview.
	fmt.Printf("스킬명: %s\n", name)
	fmt.Printf("설명:  %s\n", desc)
	fmt.Printf("트리거: %s\n", triggerType)

	if perms, ok := res["permissions"].([]any); ok && len(perms) > 0 {
		var permStrs []string
		for _, p := range perms {
			if s, ok := p.(string); ok {
				permStrs = append(permStrs, s)
			}
		}
		fmt.Printf("권한:  %s\n", strings.Join(permStrs, ", "))
	}

	fmt.Printf("\n--- 생성된 코드 ---\n%s\n--- 코드 끝 ---\n\n", code)

	if !syntaxOK {
		return fmt.Errorf("구문 오류: %s", syntaxErr)
	}

	// Interactive approval.
	fmt.Print("이 스킬을 저장하시겠습니까? (y/n): ")
	var answer string
	fmt.Scanln(&answer)
	if answer != "y" && answer != "Y" {
		fmt.Println("취소되었습니다.")
		return nil
	}

	if _, err := cl.TeachApprove(name, desc, code, triggerType, ""); err != nil {
		return fmt.Errorf("스킬 저장 실패: %w", err)
	}
	fmt.Printf("스킬 '%s' 저장 완료!\n", name)
	return nil
}

// ---------------------------------------------------------------------------
// config
// ---------------------------------------------------------------------------

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Configuration management",
	}
	cmd.AddCommand(newConfigCheckCmd())
	return cmd
}

func newConfigCheckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Show config summary",
		RunE:  runConfigCheck,
	}
	addAccountFlag(cmd)
	return cmd
}

func runConfigCheck(cmd *cobra.Command, _ []string) error {
	explicitAccount := ""
	if cmd != nil {
		if value, err := cmd.Flags().GetString("account"); err == nil {
			explicitAccount = value
		}
	}
	accountID, err := resolveCLIAccountWithContext(explicitAccount)
	if err != nil {
		return err
	}
	cfgPath, err := core.ConfigPathForAccount(accountID)
	if err != nil {
		return err
	}
	cfg, err := core.LoadConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	fmt.Printf("Config:     %s\n", cfgPath)
	fmt.Printf("Provider:   %s\n", cfg.LLM.Provider)
	fmt.Printf("Model:      %s\n", cfg.LLM.Model)
	fmt.Printf("Channels:   %d\n", len(cfg.Channels))
	fmt.Printf("Runners:    %d\n", len(cfg.Runners))
	fmt.Printf("Models:     %d\n", len(cfg.Models))
	fmt.Printf("Autonomy:   %s\n", cfg.AutonomyLevel)

	fmt.Println("\nFeatures:")
	fmt.Printf("  progressive_retry:  %v\n", cfg.Features.ProgressiveRetry)
	fmt.Printf("  context_compaction: %v\n", cfg.Features.ContextCompaction)
	fmt.Printf("  model_routing:      %v\n", cfg.Features.ModelRouting)
	fmt.Printf("  background_agents:  %v\n", cfg.Features.BackgroundAgents)
	if cfg.Features.DailyTokenLimit > 0 {
		fmt.Printf("  daily_token_limit:  %d\n", cfg.Features.DailyTokenLimit)
	}
	return nil
}

// ---------------------------------------------------------------------------
// skill log
// ---------------------------------------------------------------------------

func newSkillLogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "log",
		Short: "Show skill execution log",
		RunE:  runLog,
	}
	cmd.Flags().StringVar(&flagSkill, "skill", "", "filter by skill name")
	cmd.Flags().IntVar(&flagLimit, "limit", 20, "number of entries to show")
	return cmd
}

func runLog(_ *cobra.Command, _ []string) error {
	cl, err := connectServerForCLIAccount()
	if err != nil {
		return err
	}

	res, err := cl.Executions(flagSkill, flagLimit)
	if err != nil {
		return fmt.Errorf("query executions: %w", err)
	}

	records := jsonSlice(res, "executions")
	if len(records) == 0 {
		fmt.Println("No execution records found.")
		return nil
	}

	fmt.Printf("%s %s %s %s %s\n", padW("ID", 5), padW("SKILL", 20), padW("STARTED", 20), padW("STATUS", 7), "DURATION")
	fmt.Println(strings.Repeat("-", 80))
	for _, r := range records {
		status := "OK"
		if !jsonBool(r, "success") {
			status = "FAIL"
		}
		duration := strconv.FormatInt(jsonInt(r, "duration_ms"), 10) + "ms"
		fmt.Printf("%s %s %s %s %s\n", padW(fmt.Sprintf("%d", jsonInt(r, "id")), 5), padW(truncW(jsonStr(r, "skill_name"), 20), 20), padW(jsonStr(r, "started_at"), 20), padW(status, 7), duration)
	}
	return nil
}

func newServerStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the running server",
		RunE:  runStop,
	}
}

func runStop(_ *cobra.Command, _ []string) error {
	pidPath, err := serverPidPath()
	if err != nil {
		return err
	}

	pid, recordedStart, ok := client.ReadPidFile(pidPath)
	if !ok || !processRunning(pid) {
		lang := detectLang()
		switch {
		case strings.HasPrefix(lang, "ko"):
			fmt.Println("실행 중인 KittyPaw가 없습니다.")
		case strings.HasPrefix(lang, "ja"):
			fmt.Println("実行中のKittyPawはありません。")
		default:
			fmt.Println("No running KittyPaw found.")
		}
		if ok {
			os.Remove(pidPath)
		}
		return nil
	}

	// Phase 13.4 PID hardening: recorded start time must match the
	// live process's. If it doesn't, the PID was reused by an
	// unrelated process and we must NOT signal it.
	if !client.VerifyDaemonStartTime(pid, recordedStart) {
		fmt.Printf("PID %d does not match the recorded server (PID was reused). Cleaning up stale pid file.\n", pid)
		os.Remove(pidPath)
		return nil
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process: %w", err)
	}

	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("stop process %d: %w", pid, err)
	}
	if !waitForProcessExit(pid, stopWaitTimeout) {
		return fmt.Errorf("KittyPaw did not stop within %s (pid %d)", stopWaitTimeout, pid)
	}

	removePidFileIfMatches(pidPath, pid)
	fmt.Printf("Stopped (pid %d).\n", pid)
	return nil
}

// checkPort verifies that the bind address is available before starting
// channels. This avoids wasting time on Telegram/Slack connections only
// to fail on port conflict.
func checkPort(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if isAddrInUse(err) {
			printPortInUseMessage(addr)
		}
		return err
	}
	_ = ln.Close()
	return nil
}

func isAddrInUse(err error) bool {
	return err != nil && strings.Contains(err.Error(), "address already in use")
}

func printPortInUseMessage(addr string) {
	lang := detectLang()
	switch {
	case strings.HasPrefix(lang, "ko"):
		fmt.Printf("\n  ⚠ %s 포트가 이미 사용 중입니다.\n", addr)
		fmt.Println("    이미 실행 중인 KittyPaw가 있을 수 있습니다.")
		fmt.Println()
		fmt.Println("    kittypaw server stop     # 기존 서버 종료")
		fmt.Println("    kittypaw server start    # 다시 시작")
		fmt.Println()
	case strings.HasPrefix(lang, "ja"):
		fmt.Printf("\n  ⚠ ポート %s は既に使用中です。\n", addr)
		fmt.Println("    KittyPawが既に実行中の可能性があります。")
		fmt.Println()
		fmt.Println("    kittypaw server stop     # サーバーを停止")
		fmt.Println("    kittypaw server start    # 再起動")
		fmt.Println()
	default:
		fmt.Printf("\n  ⚠ Port %s is already in use.\n", addr)
		fmt.Println("    Another KittyPaw instance may be running.")
		fmt.Println()
		fmt.Println("    kittypaw server stop     # stop the existing server")
		fmt.Println("    kittypaw server start    # restart")
		fmt.Println()
	}
}

func writePidFile() {
	pidPath, err := serverPidPath()
	if err != nil {
		return
	}
	_ = client.WritePidFile(pidPath, os.Getpid())
}

func removePidFile() {
	pidPath, err := serverPidPath()
	if err != nil {
		return
	}
	// Only remove if it's our PID (another instance may have overwritten it).
	removePidFileIfMatches(pidPath, os.Getpid())
}

func removePidFileIfMatches(pidPath string, pid int) {
	if currentPID, _, ok := client.ReadPidFile(pidPath); ok && currentPID == pid {
		os.Remove(pidPath)
	}
}

// promptPackageConfig prompts the user to configure package settings after install.
// Skips silently if there are no config fields or stdin is not a terminal.
func promptPackageConfig(pm *core.PackageManager, pkg *core.SkillPackage) error {
	if len(pkg.Config) == 0 {
		return nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Printf("  Tip: run `kittypaw skill config %s` to configure this package.\n", pkg.Meta.ID)
		return nil
	}

	// Load existing values so we can skip already-configured fields.
	existing, _ := pm.GetConfig(pkg.Meta.ID)

	// Count fields that still need input.
	var pending []core.ConfigField
	for _, field := range pkg.Config {
		cur := existing[field.Key]
		if cur != "" && cur != field.Default {
			if field.Source != "" {
				fmt.Printf("  %s: bound from %s\n", field.Key, field.Source)
			}
			continue // already configured
		}
		if !field.Required && cur == field.Default {
			continue // optional with default — fine as-is
		}
		pending = append(pending, field)
	}
	if len(pending) == 0 {
		fmt.Println("  All configuration fields already set.")
		return nil
	}

	fmt.Printf("\nThis package needs %d configuration value(s):\n", len(pending))
	scanner := bufio.NewScanner(os.Stdin)

	for _, field := range pending {
		resolved := field.ResolvedType()
		label := field.Label
		if label == "" {
			label = field.Key
		}

		var value string
		switch resolved {
		case "boolean":
			defHint := "[y/N]"
			if strings.EqualFold(field.Default, "true") {
				defHint = "[Y/n]"
			}
			fmt.Printf("  %s %s: ", label, defHint)
			scanner.Scan()
			input := strings.TrimSpace(scanner.Text())
			if input == "" {
				value = field.Default
			} else {
				switch strings.ToLower(input) {
				case "y", "yes", "true", "1":
					value = "true"
				default:
					value = "false"
				}
			}

		case "select":
			fmt.Printf("  %s:\n", label)
			for i, opt := range field.Options {
				marker := " "
				if opt == field.Default {
					marker = "*"
				}
				fmt.Printf("    %s %d) %s\n", marker, i+1, opt)
			}
			fmt.Printf("  Choose [1-%d]: ", len(field.Options))
			scanner.Scan()
			input := strings.TrimSpace(scanner.Text())
			if input == "" && field.Default != "" {
				value = field.Default
			} else if n, err := strconv.Atoi(input); err == nil && n >= 1 && n <= len(field.Options) {
				value = field.Options[n-1]
			} else {
				fmt.Println("    Invalid selection, skipping.")
				continue
			}

		case "secret":
			fmt.Printf("  %s (hidden): ", label)
			raw, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Println()
			if err != nil {
				return fmt.Errorf("read secret: %w", err)
			}
			value = strings.TrimSpace(string(raw))

		default: // string, number
			hint := ""
			if field.Default != "" {
				hint = fmt.Sprintf(" [%s]", field.Default)
			}
			fmt.Printf("  %s%s: ", label, hint)
			scanner.Scan()
			value = strings.TrimSpace(scanner.Text())
			if value == "" {
				value = field.Default
			}
		}

		if value == "" && field.Required {
			fmt.Printf("    Skipped (required — set later with `kittypaw skill config %s %s <value>`).\n", pkg.Meta.ID, field.Key)
			continue
		}
		if value == "" {
			continue
		}

		if err := pm.SetConfig(pkg.Meta.ID, field.Key, value); err != nil {
			fmt.Printf("    Warning: failed to save %s: %v\n", field.Key, err)
		}
	}
	fmt.Println("  Configuration saved.")
	return nil
}

// defaultAccountBase returns the resolved account base directory CLI commands
// should use for local account-scoped files. A mere accounts/default/
// directory is not enough — it must be the selected valid account, otherwise
// stale dev folders make `skill list`, `reset`, and chat history point at a
// different account than the server.
func defaultAccountBase() (string, error) {
	accountID, err := resolveCLIAccount(flagAccount)
	if err != nil {
		return "", err
	}
	return accountBaseForID(accountID)
}

func defaultAccountBaseWithContext() (string, error) {
	accountID, err := resolveCLIAccountWithContext(flagAccount)
	if err != nil {
		return "", err
	}
	return accountBaseForID(accountID)
}

func printAccountContext(w io.Writer, accountID, commandPath string) {
	_, _ = fmt.Fprintf(w, "Account: %s\n", accountID)
}

func printAccountContextOnce(accountID string) {
	if accountID == "" || accountContextPrinted {
		return
	}
	printAccountContext(os.Stdout, accountID, "")
	accountContextPrinted = true
}

func accountBaseForID(accountID string) (string, error) {
	if err := core.ValidateAccountID(accountID); err != nil {
		return "", err
	}
	cfgDir, err := core.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfgDir, "accounts", accountID), nil
}

// localPackageManager returns a PackageManager bound to the default account's
// BaseDir so CLI commands see the same packages the server does. CLI
// commands that touch packages (list/info/config/install/uninstall) MUST go
// through this helper — the bare `core.NewPackageManager` is baseDir-empty
// and only finds packages at the legacy path, which has been wrong since
// the multi-account migration.
func localPackageManager() (*core.PackageManager, error) {
	accountID, err := resolveCLIAccountWithContext(flagAccount)
	if err != nil {
		return nil, err
	}
	secrets, err := core.LoadAccountSecrets(accountID)
	if err != nil {
		return nil, err
	}
	base, err := accountBaseForID(accountID)
	if err != nil {
		return nil, err
	}
	return core.NewPackageManagerFrom(base, secrets), nil
}

// registryClient creates a RegistryClient from config, falling back to DefaultRegistryURL.
func registryClient() (*core.RegistryClient, error) {
	registryURL := core.DefaultRegistryURL

	cfgPath, err := core.ConfigPath()
	if err == nil {
		if cfg, loadErr := core.LoadConfig(cfgPath); loadErr == nil && cfg.Registry.URL != "" {
			registryURL = cfg.Registry.URL
		}
	}

	return core.NewRegistryClient(registryURL)
}

// ---------------------------------------------------------------------------
// memory
// ---------------------------------------------------------------------------

func newMemoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "Memory operations",
	}
	addPersistentAccountFlag(cmd)
	cmd.AddCommand(newMemorySearchCmd())
	return cmd
}

func newMemorySearchCmd() *cobra.Command {
	var memLimit int
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search execution memory",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cl, err := connectServerForCLIAccount()
			if err != nil {
				return err
			}
			query := strings.Join(args, " ")
			res, err := cl.MemorySearch(query, memLimit)
			if err != nil {
				return err
			}
			results := jsonSlice(res, "results")
			if len(results) == 0 {
				fmt.Println("No results found.")
				return nil
			}
			fmt.Printf("%s %s %s %s\n", padW("ID", 6), padW("SKILL", 20), padW("DATE", 20), "INPUT")
			fmt.Println(strings.Repeat("-", 80))
			for _, r := range results {
				input := jsonStr(r, "input")
				if input == "" {
					input = jsonStr(r, "skill_name")
				}
				input = truncW(input, 30)
				fmt.Printf("%s %s %s %s\n",
					padW(fmt.Sprintf("%d", jsonInt(r, "id")), 6),
					padW(truncW(jsonStr(r, "skill_name"), 20), 20),
					padW(jsonStr(r, "started_at"), 20),
					input,
				)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&memLimit, "limit", 20, "number of results")
	return cmd
}

// ---------------------------------------------------------------------------
// channels
// ---------------------------------------------------------------------------

func newChannelsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "channels",
		Short: "Manage messaging channels",
	}
	addPersistentAccountFlag(cmd)
	cmd.AddCommand(newChannelsListCmd())
	return cmd
}

func newChannelsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List active channels",
		RunE: func(_ *cobra.Command, _ []string) error {
			cl, err := connectServerForCLIAccount()
			if err != nil {
				return err
			}
			res, err := cl.ChannelsList()
			if err != nil {
				return err
			}
			// The server returns a JSON array; client wraps it under "items".
			channels := jsonSlice(res, "items")
			if len(channels) == 0 {
				fmt.Println("No channels found.")
				return nil
			}
			fmt.Printf("%s %s %s\n", padW("NAME", 20), padW("TYPE", 12), "STATUS")
			fmt.Println(strings.Repeat("-", 50))
			for _, ch := range channels {
				status := "stopped"
				if jsonBool(ch, "running") {
					status = "running"
				}
				fmt.Printf("%s %s %s\n",
					padW(jsonStr(ch, "name"), 20),
					padW(jsonStr(ch, "type"), 12),
					status,
				)
			}
			return nil
		},
	}
}

// ---------------------------------------------------------------------------
// server reload
// ---------------------------------------------------------------------------

func newServerReloadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reload",
		Short: "Reload server configuration",
		RunE: func(_ *cobra.Command, _ []string) error {
			cl, err := connectServer()
			if err != nil {
				return err
			}
			if _, err := cl.Reload(); err != nil {
				return err
			}
			fmt.Println("Config reloaded.")
			return nil
		},
	}
}

// ---------------------------------------------------------------------------
// Thin Client helpers
// ---------------------------------------------------------------------------

// connectServer returns a Client connected through client.DaemonConn.
// Uses --remote flag if set, otherwise auto-discovers/starts the local server.
func connectServer() (*client.Client, error) {
	return connectServerForAccount("")
}

// connectServerForCLIAccount connects account-scoped CLI commands to the
// selected local account. Remote connections do not use local account names.
func connectServerForCLIAccount() (*client.Client, error) {
	accountID := ""
	if flagRemote == "" {
		var err error
		accountID, err = resolveCLIAccountWithContext(flagAccount)
		if err != nil {
			return nil, err
		}
	}
	return connectServerForAccount(accountID)
}

func connectServerForAccount(accountID string) (*client.Client, error) {
	conn, err := client.NewDaemonConnForAccount(flagRemote, accountID)
	if err != nil {
		return nil, err
	}
	return conn.Connect()
}

// jsonInt extracts an integer from a map[string]any (JSON numbers are float64).
func jsonInt(m map[string]any, key string) int64 {
	if v, ok := m[key].(float64); ok {
		return int64(v)
	}
	return 0
}

// jsonFloat extracts a float from a map[string]any.
func jsonFloat(m map[string]any, key string) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	return 0
}

// jsonStr extracts a string from a map[string]any.
func jsonStr(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// jsonBool extracts a bool from a map[string]any.
func jsonBool(m map[string]any, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

// jsonSlice extracts a slice of map items from a map[string]any.
func jsonSlice(m map[string]any, key string) []map[string]any {
	arr, ok := m[key].([]any)
	if !ok {
		return nil
	}
	result := make([]map[string]any, 0, len(arr))
	for _, item := range arr {
		if obj, ok := item.(map[string]any); ok {
			result = append(result, obj)
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// bootstrap discovers every account under ~/.kittypaw/accounts/ and opens
// per-account dependencies (store, LLM provider, sandbox, MCP, secrets,
// package manager, API token manager).
//
// Before discovery, a legacy ~/.kittypaw layout (config.toml at root, no
// accounts/) is migrated into accounts/default/ via MigrateLegacyLayout so
// v0.x installs upgrade transparently. Discovery fails loudly when no
// accounts are present; a server with nothing to route is not useful.
func bootstrap() ([]*server.AccountDeps, core.TopLevelServerConfig, error) {
	baseDir, err := core.ConfigDir()
	if err != nil {
		return nil, core.TopLevelServerConfig{}, fmt.Errorf("config dir: %w", err)
	}

	serverCfg, err := core.LoadServerConfig(filepath.Join(baseDir, "server.toml"))
	if err != nil {
		return nil, core.TopLevelServerConfig{}, fmt.Errorf("load server config: %w", err)
	}

	if err := core.MigrateTenantsToAccounts(baseDir); err != nil {
		return nil, core.TopLevelServerConfig{}, fmt.Errorf("migrate tenants→accounts: %w", err)
	}
	if err := core.MigrateLegacyLayout(baseDir); err != nil {
		return nil, core.TopLevelServerConfig{}, fmt.Errorf("migrate legacy layout: %w", err)
	}

	accountsRoot := filepath.Join(baseDir, "accounts")
	accounts, err := core.DiscoverAccounts(accountsRoot)
	if err != nil {
		return nil, core.TopLevelServerConfig{}, fmt.Errorf("discover accounts: %w", err)
	}
	if len(accounts) == 0 {
		return nil, core.TopLevelServerConfig{}, fmt.Errorf("no accounts found under %s (run `kittypaw setup` first)", accountsRoot)
	}
	if serverCfg.DefaultAccount != "" {
		foundDefault := false
		for _, account := range accounts {
			if account.ID == serverCfg.DefaultAccount {
				foundDefault = true
				break
			}
		}
		if !foundDefault {
			return nil, core.TopLevelServerConfig{}, fmt.Errorf("server.toml default_account %q not found under %s", serverCfg.DefaultAccount, accountsRoot)
		}
	}
	if err := core.ValidateTeamSpaceAccounts(accounts); err != nil {
		return nil, core.TopLevelServerConfig{}, fmt.Errorf("team space validation: %w", err)
	}
	if err := core.ValidateTeamSpaceMemberships(accounts); err != nil {
		return nil, core.TopLevelServerConfig{}, fmt.Errorf("team-space membership validation: %w", err)
	}

	deps := make([]*server.AccountDeps, 0, len(accounts))
	closeOnErr := func() {
		for _, td := range deps {
			_ = td.Close()
		}
	}

	for _, t := range accounts {
		secrets, secretErr := core.LoadSecretsFrom(filepath.Join(t.BaseDir, "secrets.json"))
		if secretErr != nil {
			slog.Warn("failed to load account secrets before server api key check",
				"account", t.ID, "error", secretErr)
		} else {
			core.HydrateRuntimeSecrets(t.Config, secrets)
		}

		if changed, err := core.EnsureServerAPIKey(t.Config); err != nil {
			closeOnErr()
			return nil, core.TopLevelServerConfig{}, err
		} else if changed {
			if secretErr != nil {
				closeOnErr()
				return nil, core.TopLevelServerConfig{}, fmt.Errorf("load account secrets for %s: %w", t.ID, secretErr)
			}
			if secretErr := secrets.Set("local-server", "api_key", t.Config.Server.APIKey); secretErr != nil {
				closeOnErr()
				return nil, core.TopLevelServerConfig{}, fmt.Errorf("backfill server api key secret for %s: %w", t.ID, secretErr)
			}
			if err := core.WriteConfigAtomic(t.Config, filepath.Join(t.BaseDir, "config.toml")); err != nil {
				closeOnErr()
				return nil, core.TopLevelServerConfig{}, fmt.Errorf("backfill server api key for %s: %w", t.ID, err)
			}
		}
		td, err := server.OpenAccountDeps(t)
		if err != nil {
			closeOnErr()
			return nil, core.TopLevelServerConfig{}, err
		}
		deps = append(deps, td)
	}
	return deps, *serverCfg, nil
}

func resolveCLIAccount(explicit string) (string, error) {
	cfgDir, err := core.ConfigDir()
	if err != nil {
		return "", err
	}
	if explicit != "" {
		return resolveNamedCLIAccount(cfgDir, explicit)
	}
	if env := strings.TrimSpace(os.Getenv("KITTYPAW_ACCOUNT")); env != "" {
		return resolveNamedCLIAccount(cfgDir, env)
	}
	accounts, err := core.DiscoverAccounts(filepath.Join(cfgDir, "accounts"))
	if err != nil {
		return "", err
	}
	if len(accounts) == 1 {
		return accounts[0].ID, nil
	}
	if len(accounts) == 0 {
		return "", errors.New("no accounts found; run kittypaw setup first")
	}
	if scPath, err := core.ServerConfigPath(); err == nil {
		if sc, err := core.LoadServerConfig(scPath); err == nil && sc.DefaultAccount != "" {
			for _, account := range accounts {
				if account.ID == sc.DefaultAccount {
					return sc.DefaultAccount, nil
				}
			}
			return "", fmt.Errorf("server.toml default_account %q not found under %s", sc.DefaultAccount, filepath.Join(cfgDir, "accounts"))
		}
	}
	ids := make([]string, 0, len(accounts))
	for _, a := range accounts {
		ids = append(ids, a.ID)
	}
	sort.Strings(ids)
	return "", fmt.Errorf("multiple accounts found (%s); pass --account, set KITTYPAW_ACCOUNT, or set default_account in server.toml", strings.Join(ids, ", "))
}

func resolveCLIAccountWithContext(explicit string) (string, error) {
	accountID, err := resolveCLIAccount(explicit)
	if err != nil {
		return "", err
	}
	printAccountContextOnce(accountID)
	return accountID, nil
}

func resolveNamedCLIAccount(cfgDir, id string) (string, error) {
	if err := core.ValidateAccountID(id); err != nil {
		return "", err
	}
	accounts, err := core.DiscoverAccounts(filepath.Join(cfgDir, "accounts"))
	if err != nil {
		return "", err
	}
	for _, account := range accounts {
		if account.ID == id {
			return id, nil
		}
	}
	ids := make([]string, 0, len(accounts))
	for _, account := range accounts {
		ids = append(ids, account.ID)
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return "", fmt.Errorf("account %q not found; run kittypaw setup first", id)
	}
	return "", fmt.Errorf("account %q not found (available: %s)", id, strings.Join(ids, ", "))
}

// openStore opens the SQLite store for the resolved local account.
func openStore() (*store.Store, error) {
	accountID, err := resolveCLIAccountWithContext(flagAccount)
	if err != nil {
		return nil, err
	}
	return openStoreForAccount(accountID)
}

func openStoreForAccount(accountID string) (*store.Store, error) {
	if err := core.ValidateAccountID(accountID); err != nil {
		return nil, err
	}
	dir, err := core.ConfigDir()
	if err != nil {
		return nil, err
	}
	account := &core.Account{
		ID:      accountID,
		BaseDir: filepath.Join(dir, "accounts", accountID),
	}
	dbPath := account.DBPath()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return nil, err
	}
	st, err := store.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open store %s: %w", dbPath, err)
	}
	return st, nil
}

// serverPidPath returns the path to the server PID file. The filename remains
// daemon.pid for compatibility with existing installs.
func serverPidPath() (string, error) {
	dir, err := core.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "daemon.pid"), nil
}

var (
	stopWaitTimeout      = 10 * time.Second
	stopWaitPollInterval = 50 * time.Millisecond
)

func waitForProcessExit(pid int, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	ticker := time.NewTicker(stopWaitPollInterval)
	defer ticker.Stop()

	for {
		if !processRunning(pid) {
			return true
		}
		select {
		case <-deadline.C:
			return !processRunning(pid)
		case <-ticker.C:
		}
	}
}

// processRunning checks whether a pid corresponds to a live process.
var processRunning = func(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 tests existence without sending a signal.
	return proc.Signal(syscall.Signal(0)) == nil
}

// ---------------------------------------------------------------------------
// spinner
// ---------------------------------------------------------------------------

type spinner struct {
	prefix string
	stop   chan struct{}
	done   chan struct{}
}

func newSpinner(prefix string) *spinner {
	return &spinner{
		prefix: prefix,
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
}

func (s *spinner) Start() {
	go func() {
		defer close(s.done)
		frames := []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}
		ticker := time.NewTicker(180 * time.Millisecond)
		defer ticker.Stop()
		fmt.Print("\033[?25l") // hide cursor
		i := 0
		for {
			fmt.Printf("\r\033[K%s%c", s.prefix, frames[i%len(frames)])
			i++
			select {
			case <-s.stop:
				fmt.Print("\r\033[K\033[?25h") // clear line + show cursor
				return
			case <-ticker.C:
			}
		}
	}()
}

func (s *spinner) Stop() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	<-s.done
}
