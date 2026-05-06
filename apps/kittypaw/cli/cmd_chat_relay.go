package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jinto/kittypaw/core"
)

type chatRelayFlags struct {
	account string
	apiURL  string
	name    string
}

func newChatRelayCmd() *cobra.Command {
	flags := &chatRelayFlags{}
	cmd := &cobra.Command{
		Use:    "chat-relay",
		Short:  "Internal hosted chat relay diagnostics",
		Hidden: true,
	}
	cmd.PersistentFlags().StringVar(&flags.account, "account", "", "use this local account")
	cmd.PersistentFlags().StringVar(&flags.apiURL, "api-url", "", "API server URL (default "+core.DefaultAPIServerURL+")")

	pairCmd := &cobra.Command{
		Use:   "pair",
		Short: "Pair this server with the hosted chat relay",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChatRelayPair(flags)
		},
	}
	pairCmd.Flags().StringVar(&flags.name, "name", "", "device display name")

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show local chat relay credential status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChatRelayStatus(flags)
		},
	}

	cmd.AddCommand(pairCmd, statusCmd)
	return cmd
}

func runChatRelayPair(flags *chatRelayFlags) error {
	accountID, mgr, apiURL, err := chatRelayAccountManager(flags)
	if err != nil {
		return err
	}
	_ = applyDiscovery(apiURL, mgr)

	accessToken, err := mgr.LoadAccessToken(apiURL)
	if err != nil {
		return err
	}
	if accessToken == "" {
		return fmt.Errorf("not logged in to %s for account %q; run `kittypaw setup` or `kittypaw login` first", apiURL, accountID)
	}

	name := strings.TrimSpace(flags.name)
	if name == "" {
		if host, err := os.Hostname(); err == nil {
			name = host
		}
	}
	if _, err := mgr.PairChatRelayDevice(mgr.ResolveAuthBaseURL(apiURL), apiURL, accessToken, core.ChatRelayDevicePairRequest{Name: name}); err != nil {
		return err
	}
	fmt.Printf("Hosted chat ready for account %s\n", accountID)
	return nil
}

func runChatRelayStatus(flags *chatRelayFlags) error {
	accountID, mgr, apiURL, err := chatRelayAccountManager(flags)
	if err != nil {
		return err
	}
	relayURL, relayOK := mgr.LoadHomeBaseURL(apiURL)
	if !relayOK || relayURL == "" {
		relayURL, relayOK = mgr.LoadChatRelayURL(apiURL)
	}
	_, tokenOK := mgr.LoadChatRelayDeviceTokens(apiURL)

	fmt.Printf("account: %s\n", accountID)
	if tokenOK {
		if expired, ok := mgr.ChatRelayDeviceAccessTokenExpired(apiURL); ok && expired {
			fmt.Println("hosted_chat: ready (refresh pending)")
		} else {
			fmt.Println("hosted_chat: ready")
		}
	} else if relayOK && relayURL != "" {
		fmt.Println("hosted_chat: login needed")
	} else {
		fmt.Println("hosted_chat: unavailable")
	}
	return nil
}

func chatRelayAccountManager(flags *chatRelayFlags) (string, *core.APITokenManager, string, error) {
	accountID, err := resolveCLIAccount(flags.account)
	if err != nil {
		return "", nil, "", err
	}
	secrets, err := core.LoadAccountSecrets(accountID)
	if err != nil {
		return "", nil, "", fmt.Errorf("load account secrets: %w", err)
	}
	apiURL := strings.TrimRight(flags.apiURL, "/")
	if apiURL == "" {
		apiURL = accountAPIURL(secrets)
	}
	return accountID, core.NewAPITokenManager("", secrets), apiURL, nil
}
