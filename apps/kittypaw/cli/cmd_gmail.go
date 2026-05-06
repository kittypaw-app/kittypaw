package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jinto/kittypaw/core"
)

var gmailHTTPClient = http.DefaultClient

const defaultGmailLatestQuery = "in:inbox category:primary"

func newGmailCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gmail",
		Short: "Inspect the connected Gmail account",
	}
	addPersistentAccountFlag(cmd)
	cmd.AddCommand(newGmailLatestCmd())
	cmd.AddCommand(newGmailListCmd())
	cmd.AddCommand(newGmailSearchCmd())
	cmd.AddCommand(newGmailReadCmd())
	return cmd
}

func newGmailLatestCmd() *cobra.Command {
	var query string
	cmd := &cobra.Command{
		Use:   "latest",
		Short: "Read the latest primary inbox Gmail message",
		RunE: func(cmd *cobra.Command, args []string) error {
			accountID, err := resolveCLIAccountWithContext(flagAccount)
			if err != nil {
				return err
			}
			return runGmailLatest(cmd.Context(), accountID, query, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&query, "query", defaultGmailLatestQuery, "Gmail search query for latest message")
	return cmd
}

func runGmailLatest(ctx context.Context, accountID, query string, out io.Writer) error {
	client, accessToken, err := gmailClientForAccount(accountID)
	if err != nil {
		return err
	}
	ctx, cancel := gmailCommandContext(ctx)
	defer cancel()

	refs, err := client.SearchMessages(ctx, accessToken, 1, query)
	if err != nil {
		return err
	}
	if len(refs) == 0 {
		_, _ = fmt.Fprintln(out, "No Gmail messages found.")
		return nil
	}
	msg, err := client.GetMessage(ctx, accessToken, refs[0].ID)
	if err != nil {
		return err
	}
	renderGmailMessage(out, msg)
	return nil
}

func newGmailListCmd() *cobra.Command {
	var (
		query string
		limit int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recent primary inbox Gmail messages",
		RunE: func(cmd *cobra.Command, args []string) error {
			accountID, err := resolveCLIAccountWithContext(flagAccount)
			if err != nil {
				return err
			}
			return runGmailList(cmd.Context(), accountID, query, limit, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&query, "query", defaultGmailLatestQuery, "Gmail search query")
	cmd.Flags().IntVar(&limit, "limit", 10, "maximum messages to list")
	return cmd
}

func newGmailSearchCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "search <query...>",
		Short: "Search Gmail messages",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			accountID, err := resolveCLIAccountWithContext(flagAccount)
			if err != nil {
				return err
			}
			return runGmailList(cmd.Context(), accountID, strings.Join(args, " "), limit, cmd.OutOrStdout())
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 10, "maximum messages to list")
	return cmd
}

func newGmailReadCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "read <message-id>",
		Short: "Read a Gmail message by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			accountID, err := resolveCLIAccountWithContext(flagAccount)
			if err != nil {
				return err
			}
			return runGmailRead(cmd.Context(), accountID, args[0], cmd.OutOrStdout())
		},
	}
	return cmd
}

func runGmailList(ctx context.Context, accountID, query string, limit int, out io.Writer) error {
	client, accessToken, err := gmailClientForAccount(accountID)
	if err != nil {
		return err
	}
	ctx, cancel := gmailCommandContext(ctx)
	defer cancel()

	refs, err := client.SearchMessages(ctx, accessToken, clampGmailLimit(limit), query)
	if err != nil {
		return err
	}
	if len(refs) == 0 {
		_, _ = fmt.Fprintln(out, "No Gmail messages found.")
		return nil
	}
	for i, ref := range refs {
		msg, err := client.GetMessage(ctx, accessToken, ref.ID)
		if err != nil {
			return err
		}
		if i > 0 {
			_, _ = fmt.Fprintln(out)
		}
		renderGmailMessageSummary(out, msg)
	}
	return nil
}

func runGmailRead(ctx context.Context, accountID, messageID string, out io.Writer) error {
	client, accessToken, err := gmailClientForAccount(accountID)
	if err != nil {
		return err
	}
	ctx, cancel := gmailCommandContext(ctx)
	defer cancel()

	msg, err := client.GetMessage(ctx, accessToken, strings.TrimSpace(messageID))
	if err != nil {
		return err
	}
	renderGmailMessage(out, msg)
	return nil
}

func gmailClientForAccount(accountID string) (*core.GmailClient, string, error) {
	secrets, err := core.LoadAccountSecrets(accountID)
	if err != nil {
		return nil, "", fmt.Errorf("load account secrets: %w", err)
	}
	tokenMgr := core.NewServiceTokenManager(secrets)
	accessToken, err := tokenMgr.LoadAccessToken("gmail")
	if err != nil {
		return nil, "", err
	}
	if accessToken == "" {
		return nil, "", fmt.Errorf("missing Gmail connection; run: kittypaw connect gmail")
	}
	return core.NewGmailClient(os.Getenv("KITTYPAW_GMAIL_BASE_URL"), gmailHTTPClient), accessToken, nil
}

func gmailCommandContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, 30*time.Second)
}

func clampGmailLimit(limit int) int {
	if limit <= 0 {
		return 10
	}
	if limit > 25 {
		return 25
	}
	return limit
}

func renderGmailMessage(out io.Writer, msg core.GmailMessage) {
	_, _ = fmt.Fprintf(out, "From: %s\n", emptyDash(msg.From))
	_, _ = fmt.Fprintf(out, "Subject: %s\n", emptyDash(msg.Subject))
	_, _ = fmt.Fprintf(out, "Date: %s\n", emptyDash(msg.Date))
	_, _ = fmt.Fprintf(out, "Snippet: %s\n", emptyDash(msg.Snippet))
	if body := strings.TrimSpace(msg.BodyText); body != "" {
		_, _ = fmt.Fprintf(out, "\n%s\n", truncateRunes(body, 1200))
	}
}

func renderGmailMessageSummary(out io.Writer, msg core.GmailMessage) {
	_, _ = fmt.Fprintf(out, "ID: %s\n", emptyDash(msg.ID))
	_, _ = fmt.Fprintf(out, "From: %s\n", emptyDash(msg.From))
	_, _ = fmt.Fprintf(out, "Subject: %s\n", emptyDash(msg.Subject))
	_, _ = fmt.Fprintf(out, "Date: %s\n", emptyDash(msg.Date))
	_, _ = fmt.Fprintf(out, "Snippet: %s\n", emptyDash(msg.Snippet))
}

func emptyDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}
