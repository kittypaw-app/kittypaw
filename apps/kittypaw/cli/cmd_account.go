package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/jinto/kittypaw/client"
	"github.com/jinto/kittypaw/core"
)

type accountAddFlags struct {
	telegramToken           string
	telegramTokenStdin      bool
	adminChatID             string
	isShared                bool
	llmProvider             string
	llmAPIKey               string
	llmModel                string
	llmBaseURL              string
	kakaoEnabled            bool
	kakaoRelayWSURL         string
	telegramTokenFromPrompt bool
	noActivate              bool
	passwordStdin           bool
}

const accountEnvBotToken = "KITTYPAW_TELEGRAM_BOT_TOKEN"

func newAccountCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "account",
		Short: "Manage multi-account workspaces",
		Long:  "Create and inspect account workspaces under ~/.kittypaw/accounts/. Each account owns its own DB, secrets, skills, and channel bindings.",
	}
	cmd.AddCommand(newAccountAddCmd())
	cmd.AddCommand(newAccountRemoveCmd())
	return cmd
}

func newAccountAddCmd() *cobra.Command {
	f := &accountAddFlags{}
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Provision a new account directory",
		Long: `Create a new account under ~/.kittypaw/accounts/<name>/ with its own
config.toml, data/, skills/, staff/, and packages/ subtrees.

Bot-token sources (highest priority wins):
  1. --telegram-bot-token-stdin  (reads from stdin — recommended)
  2. $` + accountEnvBotToken + `
  3. --telegram-bot-token        (visible in process list; prints a warning)

Local Web UI credentials are required for every account. Use
--password-stdin in non-interactive scripts, or run from a TTY to enter and
confirm the password interactively. When both stdin flags are set, stdin is
framed as two lines: Telegram token first, local password second.

Interactive fallback: when no channel source AND no LLM key is supplied AND
stdin is a TTY, a 5-step prompt walks through channel selection, channel
credentials, LLM provider, api-key, and model. Secrets (token, api-key) are
read with masked terminal input. CI / scripted callers passing any flag/env keep the original
non-interactive path.

If a server is already running, the account is hot-activated: channels spawn
and dispatch begins without a restart (AC-U3). Pass --no-activate to skip
		the activation RPC and only stage files on disk.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			err := runAccountAdd(args[0], f, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
			return handleAccountAddExisting(
				err,
				args[0],
				cmd.InOrStdin(),
				cmd.OutOrStdout(),
				isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd()),
				runAccountSetupReconfigure,
			)
		},
	}
	cmd.Flags().StringVar(&f.telegramToken, "telegram-bot-token", "", "Telegram bot token (visible in ps; prefer --telegram-bot-token-stdin)")
	cmd.Flags().BoolVar(&f.telegramTokenStdin, "telegram-bot-token-stdin", false, "Read Telegram bot token from stdin")
	cmd.Flags().StringVar(&f.adminChatID, "admin-chat-id", "", "Telegram admin chat ID (auto-detected from getUpdates when omitted)")
	cmd.Flags().BoolVar(&f.isShared, "is-shared", false, "Mark this account as the team-space coordinator (no channels)")
	cmd.Flags().StringVar(&f.llmProvider, "llm-provider", "", "LLM provider (anthropic|openai|local)")
	cmd.Flags().StringVar(&f.llmAPIKey, "llm-api-key", "", "LLM API key")
	cmd.Flags().StringVar(&f.llmModel, "llm-model", "", "LLM model name")
	cmd.Flags().BoolVar(&f.noActivate, "no-activate", false, "Stage files only; skip hot-activation against a running server")
	cmd.Flags().BoolVar(&f.passwordStdin, "password-stdin", false, "Read local Web UI password from stdin")
	return cmd
}

// Empty return means no token configured — shared/no-token branches are validated by the caller.
func resolveAccountToken(f *accountAddFlags, stdin io.Reader, stderr io.Writer) (string, error) {
	if f.telegramTokenStdin {
		line, err := readStdinLine(stdin)
		if err != nil {
			return "", fmt.Errorf("read token from stdin: %w", err)
		}
		token := strings.TrimSpace(line)
		if token == "" {
			return "", errors.New("--telegram-bot-token-stdin was set but stdin is empty")
		}
		return token, nil
	}
	if env := strings.TrimSpace(os.Getenv(accountEnvBotToken)); env != "" {
		if f.telegramToken != "" {
			_, _ = fmt.Fprintf(stderr, "warning: --telegram-bot-token ignored ($%s is set)\n", accountEnvBotToken)
		}
		return env, nil
	}
	if f.telegramToken != "" {
		if !f.telegramTokenFromPrompt {
			_, _ = fmt.Fprintln(stderr, "warning: bot token passed via flag is visible in the process list; prefer --telegram-bot-token-stdin")
		}
		return f.telegramToken, nil
	}
	return "", nil
}

func resolveAccountPassword(f *accountAddFlags, stdin io.Reader) (string, error) {
	if f.passwordStdin {
		password, err := readStdinLine(stdin)
		if err != nil {
			return "", fmt.Errorf("read password from stdin: %w", err)
		}
		if password == "" {
			return "", errors.New("password is required")
		}
		return password, nil
	}
	if !isatty.IsTerminal(os.Stdin.Fd()) {
		return "", errors.New("--password-stdin is required")
	}
	return promptLocalPassword()
}

type stdinLineReader interface {
	ReadString(byte) (string, error)
}

func readStdinLine(stdin io.Reader) (string, error) {
	if lr, ok := stdin.(stdinLineReader); ok {
		line, err := lr.ReadString('\n')
		if err != nil && !(errors.Is(err, io.EOF) && line != "") {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}

	scanner := bufio.NewScanner(stdin)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", err
		}
		return "", nil
	}
	return scanner.Text(), nil
}

func accountAddAccountsDirForNew(name string) (string, error) {
	if err := core.ValidateAccountID(name); err != nil {
		return "", err
	}
	cfgDir, err := core.ConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve config dir: %w", err)
	}
	accountsDir := filepath.Join(cfgDir, "accounts")
	accountDir := filepath.Join(accountsDir, name)
	if _, err := os.Stat(accountDir); err == nil {
		return "", fmt.Errorf("%w: %q at %s (run `kittypaw setup --account %s` to reconfigure)", core.ErrAccountExists, name, accountDir, name)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat account dir: %w", err)
	}
	return accountsDir, nil
}

func handleAccountAddExisting(err error, name string, stdin io.Reader, stdout io.Writer, interactive bool, reconfigure func(string, io.Reader) error) error {
	if err == nil || !errors.Is(err, core.ErrAccountExists) {
		return err
	}
	if !interactive {
		return err
	}
	if stdout == nil {
		stdout = io.Discard
	}
	_, _ = fmt.Fprintf(stdout, "account %q already exists.\n", name)
	_, _ = fmt.Fprintf(stdout, "  > Reconfigure with `kittypaw setup --account %s`? (y/N): ", name)
	scanner := bufio.NewScanner(stdin)
	if !scanner.Scan() {
		if scanErr := scanner.Err(); scanErr != nil {
			return scanErr
		}
		return err
	}
	switch strings.ToLower(strings.TrimSpace(scanner.Text())) {
	case "y", "yes":
		if reconfigure == nil {
			return err
		}
		return reconfigure(name, stdin)
	default:
		return err
	}
}

var runAccountSetupReconfigure = func(name string, stdin io.Reader) error {
	cmd := &cobra.Command{}
	cmd.SetIn(stdin)
	return runSetup(cmd, &setupFlags{accountID: name})
}

func accountTelegramTokenOwner(token string) (string, bool, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", false, nil
	}
	cfgDir, err := core.ConfigDir()
	if err != nil {
		return "", false, err
	}
	accounts, err := core.DiscoverAccounts(filepath.Join(cfgDir, "accounts"))
	if err != nil {
		return "", false, err
	}
	for _, account := range accounts {
		if account == nil {
			continue
		}
		secrets, err := core.LoadSecretsFrom(filepath.Join(account.BaseDir, "secrets.json"))
		if err != nil {
			return "", false, fmt.Errorf("load account secrets for %q: %w", account.ID, err)
		}
		if existing, ok := secrets.Get("channel/telegram", "bot_token"); ok && existing == token {
			return account.ID, true, nil
		}
	}
	return "", false, nil
}

func runAccountAdd(name string, f *accountAddFlags, stdin io.Reader, stdout, stderr io.Writer) error {
	accountsDir, err := accountAddAccountsDirForNew(name)
	if err != nil {
		return err
	}

	// Interactive fallback: if neither a Telegram token source nor an LLM key
	// is in scope, walk the user through 4 quick prompts instead of erroring
	// out. CI / scripted callers (any flag/env set) keep the non-interactive
	// path. Non-TTY shells fall through to the original error so failure modes
	// stay loud.
	if needsAccountPrompt(f) && isatty.IsTerminal(os.Stdin.Fd()) {
		if err := promptAccountSetup(name, stdin, stdout, f); err != nil {
			return err
		}
	}

	stdin = bufio.NewReader(stdin)
	token, err := resolveAccountToken(f, stdin, stderr)
	if err != nil {
		return err
	}

	if f.isShared && (token != "" || f.kakaoEnabled) {
		return fmt.Errorf("--is-shared and channel credentials are mutually exclusive")
	}
	if !f.isShared && token == "" && !f.kakaoEnabled {
		return fmt.Errorf("a Telegram or KakaoTalk channel is required for personal accounts (set --telegram-bot-token-stdin, $%s, --telegram-bot-token, or use the interactive KakaoTalk setup; or pass --is-shared to create a team-space account)", accountEnvBotToken)
	}
	if token != "" && !core.ValidateTelegramToken(token) {
		return errors.New("invalid telegram bot token format")
	}
	if f.kakaoEnabled && strings.TrimSpace(f.kakaoRelayWSURL) == "" {
		return errors.New("kakao relay URL is required")
	}
	if !f.passwordStdin && isatty.IsTerminal(os.Stdin.Fd()) {
		_, _ = fmt.Fprintln(stdout)
		_, _ = fmt.Fprintln(stdout, accountCredentialsIntroMessage(name))
		_, _ = fmt.Fprintln(stdout)
	}
	password, err := resolveAccountPassword(f, stdin)
	if err != nil {
		return err
	}

	chatID := f.adminChatID
	if token != "" && chatID == "" {
		detected, derr := detectTelegramChatID(name, token)
		if derr == nil {
			chatID = detected
		} else {
			_, _ = fmt.Fprintf(stderr, "info: chat_id auto-detect skipped (%v); pass --admin-chat-id later if needed\n", derr)
		}
	}

	tt, err := core.InitAccount(accountsDir, name, core.AccountOpts{
		TelegramToken:   token,
		AdminChatID:     chatID,
		IsFamily:        f.isShared,
		LLMProvider:     f.llmProvider,
		LLMAPIKey:       f.llmAPIKey,
		LLMModel:        f.llmModel,
		LLMBaseURL:      f.llmBaseURL,
		LocalPassword:   password,
		KakaoEnabled:    f.kakaoEnabled,
		KakaoRelayWSURL: f.kakaoRelayWSURL,
	})
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(stdout, "account %q created at %s\n", tt.ID, tt.BaseDir)

	if f.noActivate {
		_, _ = fmt.Fprintln(stdout, "Skipped activation (--no-activate). Restart 'kittypaw server start' or re-run without the flag to activate.")
		return nil
	}
	if err := activateAccountOnServer(tt.ID, stdout, stderr); err != nil {
		// Don't fail the whole command — files are already on disk; the user
		// can recover with a server restart. Surface the error clearly so
		// they know hot-activate didn't take.
		_, _ = fmt.Fprintf(stderr, "warning: hot-activation failed: %v\n", err)
		_, _ = fmt.Fprintln(stdout, "Restart 'kittypaw server start' to activate, or re-run `kittypaw account add` after starting the server.")
	}
	return nil
}

func newAccountRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Decommission an account (safe, reversible via .trash/)",
		Long: `Decommission an account safely:

  1. If a server is running, deactivate the account (stops channels, drains
     sessions) via admin RPC — no restart required.
  2. If the removed account is a team-space member, remove it from
     [team_space].members and delete any legacy [share.<name>] stanza from the
     team-space account config so stale access is not re-granted if the name is
     re-used later.
  3. Move ~/.kittypaw/accounts/<name>/ to ~/.kittypaw/.trash/<name>-<ts>/.
     The move is atomic (same partition) and reversible by manual rename.
  4. Print a warning that the Telegram bot token is still valid — the admin
     must revoke it via @BotFather /revoke.

The command aborts BEFORE touching team-space membership/config or the account
directory if the server returns an error, so a failed step 1 leaves the
account fully runnable. Re-running after the server reports healthy
completes the decommission.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAccountRemove(args[0], cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	return cmd
}

func runAccountRemove(name string, stdout, stderr io.Writer) error {
	if err := core.ValidateAccountID(name); err != nil {
		return err
	}

	cfgDir, err := core.ConfigDir()
	if err != nil {
		return fmt.Errorf("resolve config dir: %w", err)
	}
	accountsDir := filepath.Join(cfgDir, "accounts")
	accountDir := filepath.Join(accountsDir, name)

	info, err := os.Stat(accountDir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("account %q does not exist at %s", name, accountDir)
	}

	// Load account's own config to learn is_shared (so we can skip the
	// self-cleanup step and surface the extra warning). A missing
	// config.toml is treated as personal — the worst case is a no-op scrub.
	selfCfg, _ := core.LoadConfig(filepath.Join(accountDir, "config.toml"))
	removedIsShared := selfCfg != nil && selfCfg.IsSharedAccount()

	if err := deactivateAccountOnServer(name, stdout, stderr); err != nil {
		return fmt.Errorf("deactivate on server: %w", err)
	}

	if !removedIsShared {
		if err := scrubTeamSpaceAccountReferences(accountsDir, name, stderr); err != nil {
			return fmt.Errorf("update team-space account config: %w", err)
		}
	}

	trashedPath, err := moveAccountToTrash(cfgDir, accountsDir, name)
	if err != nil {
		return fmt.Errorf("move to trash: %w", err)
	}

	_, _ = fmt.Fprintf(stdout, "account %q decommissioned → %s\n", name, trashedPath)
	_, _ = fmt.Fprintf(stderr, "warning: Telegram bot token for account %q is still valid. Revoke via @BotFather /revoke to fully decommission.\n", name)
	if removedIsShared {
		_, _ = fmt.Fprintln(stderr, "note: team-space account removed — members will no longer see cross-account shares or fanout until a new team-space account is provisioned.")
	}
	return nil
}

// deactivateAccountOnServer calls POST /api/v1/admin/accounts/{id}/delete when
// a server is running. Absence of a server is not an error (AC-RM2 offline
// path); 404 from the server means the account isn't currently active, which
// is also fine (already decommissioned or never booted with it).
func deactivateAccountOnServer(name string, stdout, stderr io.Writer) error {
	conn, err := client.NewDaemonConn("")
	if err != nil {
		// Missing config.toml (pre-onboarding) is treated as offline — the
		// filesystem part of decommission still matters even if the user
		// never booted the server with this account.
		_, _ = fmt.Fprintf(stdout, "Server config unavailable (%v); skipping hot-deactivation.\n", err)
		return nil
	}
	if !conn.IsRunning() {
		_, _ = fmt.Fprintln(stdout, "Server is not running; skipping hot-deactivation.")
		return nil
	}

	cl := client.New(conn.BaseURL, conn.APIKey)
	if _, err := cl.AccountRemove(name); err != nil {
		// Treat 404 as benign (already gone). Everything else aborts so the
		// CLI doesn't mutate team-space config or the filesystem while a real
		// drain error is pending — AC-RM5.
		if strings.Contains(err.Error(), "404") {
			_, _ = fmt.Fprintf(stderr, "info: server reports account %q not active (already decommissioned?); continuing.\n", name)
			return nil
		}
		return err
	}
	_, _ = fmt.Fprintf(stdout, "account %q deactivated on server\n", name)
	return nil
}

// scrubTeamSpaceAccountReferences removes the [share.<removed>] stanza and
// team_space.members entry from each team-space account's config.toml. No-op
// if no team-space account exists (AC-RM4) or the account is not referenced.
// Uses WriteConfigAtomic so a crash mid-write never leaves the file truncated
// (AC-RM6).
func scrubTeamSpaceAccountReferences(accountsDir, removed string, stderr io.Writer) error {
	accounts, err := core.DiscoverAccounts(accountsDir)
	if err != nil {
		return err
	}
	for _, account := range accounts {
		if account == nil || account.Config == nil || !account.Config.IsSharedAccount() {
			continue
		}

		changed := false
		removedLegacyShare := false
		if _, ok := account.Config.Share[removed]; ok {
			delete(account.Config.Share, removed)
			changed = true
			removedLegacyShare = true
		}

		members := account.Config.TeamSpace.Members
		if len(members) > 0 {
			filtered := members[:0]
			for _, member := range members {
				if member == removed {
					changed = true
					continue
				}
				filtered = append(filtered, member)
			}
			account.Config.TeamSpace.Members = filtered
		}

		if !changed {
			continue
		}
		cfgPath := filepath.Join(account.BaseDir, "config.toml")
		if err := core.WriteConfigAtomic(account.Config, cfgPath); err != nil {
			return fmt.Errorf("atomic write %s: %w", cfgPath, err)
		}
		if removedLegacyShare {
			_, _ = fmt.Fprintf(stderr, "info: removed legacy [share.%s] from team-space account config at %s\n", removed, cfgPath)
		}
	}
	return nil
}

// moveAccountToTrash renames accounts/<name>/ to .trash/<name>-<ts>/ atomically
// within the same filesystem. On collision (same-second re-runs or prior
// residue) it appends a -2, -3, ... suffix rather than overwriting (AC-RM8).
func moveAccountToTrash(cfgDir, accountsDir, name string) (string, error) {
	trashDir := filepath.Join(cfgDir, ".trash")
	if err := os.MkdirAll(trashDir, 0o700); err != nil {
		return "", fmt.Errorf("create trash dir: %w", err)
	}
	ts := time.Now().UTC().Format("20060102150405")
	base := filepath.Join(trashDir, name+"-"+ts)
	candidate := base
	for i := 2; ; i++ {
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			break
		}
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
	src := filepath.Join(accountsDir, name)
	if err := os.Rename(src, candidate); err != nil {
		return "", fmt.Errorf("rename %s → %s: %w", src, candidate, err)
	}
	return candidate, nil
}

// activateAccountOnServer calls POST /api/v1/admin/accounts if a server is
// already running locally. Absence of a server is not an error — the user
// may be provisioning offline before first boot — so we fall back to a
// restart hint printed by the caller.
func activateAccountOnServer(accountID string, stdout, stderr io.Writer) error {
	conn, err := client.NewDaemonConn("")
	if err != nil {
		return fmt.Errorf("read server config: %w", err)
	}
	if !conn.IsRunning() {
		_, _ = fmt.Fprintln(stdout, "Server is not running; start 'kittypaw server start' to activate this account.")
		return nil
	}

	cl := client.New(conn.BaseURL, conn.APIKey)
	resp, err := cl.AccountActivate(accountID)
	if err != nil {
		return err
	}

	channels, _ := resp["channels"].(float64)
	isShared, _ := resp["is_shared"].(bool)
	_, _ = fmt.Fprintf(stdout, "account %q activated (channels=%d, is_shared=%t)\n",
		accountID, int(channels), isShared)
	return nil
}
