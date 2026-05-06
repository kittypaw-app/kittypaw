package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/jinto/kittypaw/core"
)

var fetchDiscovery = core.FetchDiscovery

func newLoginCmd() *cobra.Command {
	var (
		flagCode   bool
		flagAPIURL string
	)

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with kittypaw API server",
		RunE: func(cmd *cobra.Command, args []string) error {
			apiURL := flagAPIURL
			if apiURL == "" {
				apiURL = core.DefaultAPIServerURL
			}
			apiURL = strings.TrimRight(apiURL, "/")

			secrets, err := core.LoadAccountSecrets(core.DefaultAccountID)
			if err != nil {
				return fmt.Errorf("load secrets: %w", err)
			}
			mgr := core.NewAPITokenManager("", secrets)

			// Auto-detect: use code mode if no TTY or flag set.
			useCode := flagCode || !term.IsTerminal(int(os.Stdin.Fd()))

			if useCode {
				return loginCode(apiURL, mgr)
			}
			return loginHTTP(apiURL, mgr)
		},
	}

	cmd.Flags().BoolVar(&flagCode, "code", false, "use code-paste mode (for SSH/remote)")
	cmd.Flags().StringVar(&flagAPIURL, "api-url", "", "API server URL (default "+core.DefaultAPIServerURL+")")
	return cmd
}

// applyDiscovery fetches GET {apiURL}/discovery and persists service
// URLs under the portal host's secrets namespace. Never returns an error:
// degraded discovery logs a warning and returns apiURL as the effective
// api_base_url so login continues to work for collapsed deployments.
//
// TODO(discovery-url-wiring): the returned api_base_url is currently unused.
// See .claude/plans/discovery-url-wiring.md for the follow-up that routes
// exchange/refresh through api_base_url and registryClient through
// skills_registry_url.
func applyDiscovery(apiURL string, mgr *core.APITokenManager) string {
	d, err := fetchDiscovery(apiURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "discovery: %v — falling back to %s\n", err, apiURL)
		return apiURL
	}
	if err := mgr.SaveAPIBaseURL(apiURL, d.APIBaseURL); err != nil {
		fmt.Fprintf(os.Stderr, "discovery: save api_base_url: %v\n", err)
	}
	if err := mgr.SaveAuthBaseURL(apiURL, d.AuthBaseURL); err != nil {
		fmt.Fprintf(os.Stderr, "discovery: save auth_base_url: %v\n", err)
	}
	if err := mgr.SaveConnectBaseURL(apiURL, d.ConnectBaseURL); err != nil {
		fmt.Fprintf(os.Stderr, "discovery: save connect_base_url: %v\n", err)
	}
	if err := mgr.SaveChatRelayURL(apiURL, d.ChatRelayURL); err != nil {
		fmt.Fprintf(os.Stderr, "discovery: save chat_relay_url: %v\n", err)
	}
	if err := mgr.SaveSpaceBaseURL(apiURL, d.SpaceBaseURL); err != nil {
		fmt.Fprintf(os.Stderr, "discovery: save space_base_url: %v\n", err)
	}
	if err := mgr.SaveKakaoRelayBaseURL(apiURL, d.KakaoRelayURL); err != nil {
		fmt.Fprintf(os.Stderr, "discovery: save kakao_relay_url: %v\n", err)
	}
	if err := mgr.SaveSkillsRegistryURL(apiURL, d.SkillsRegistryURL); err != nil {
		fmt.Fprintf(os.Stderr, "discovery: save skills_registry_url: %v\n", err)
	}
	return d.APIBaseURL
}

func maybePairChatRelayDevice(apiURL string, mgr *core.APITokenManager, accessToken string, out io.Writer) bool {
	if mgr == nil || strings.TrimSpace(accessToken) == "" {
		return false
	}
	if _, ok := mgr.LoadChatRelayDeviceTokens(apiURL); ok {
		return false
	}
	relayURL, ok := mgr.LoadSpaceBaseURL(apiURL)
	if !ok || relayURL == "" {
		relayURL, ok = mgr.LoadChatRelayURL(apiURL)
	}
	if !ok || relayURL == "" {
		return false
	}
	if out == nil {
		out = io.Discard
	}

	name := "kittypaw-server"
	if host, err := os.Hostname(); err == nil && strings.TrimSpace(host) != "" {
		name = strings.TrimSpace(host)
	}
	if _, err := mgr.PairChatRelayDevice(
		mgr.ResolveAuthBaseURL(apiURL),
		apiURL,
		accessToken,
		core.ChatRelayDevicePairRequest{Name: name},
	); err != nil {
		_, _ = fmt.Fprintf(out, "Hosted chat setup skipped: %v\n", err)
		_, _ = fmt.Fprintln(out, "Hosted chat will be configured automatically the next time you log in.")
		return false
	}
	_, _ = fmt.Fprintln(out, "Hosted chat ready.")
	return true
}

func loginHTTP(apiURL string, mgr *core.APITokenManager) error {
	// 1. Discovery first: resolves service topology before OAuth begins.
	_ = applyDiscovery(apiURL, mgr)

	// 2. Start local callback server on OS-assigned port.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("start callback server: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	tokenCh := make(chan *tokenResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		accessToken := r.URL.Query().Get("access_token")
		refreshToken := r.URL.Query().Get("refresh_token")

		if accessToken == "" {
			tokenCh <- &tokenResult{err: fmt.Errorf("no access_token in callback")}
			http.Error(w, "missing token", http.StatusBadRequest)
			return
		}

		tokenCh <- &tokenResult{
			accessToken:  accessToken,
			refreshToken: refreshToken,
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, loginSuccessHTML)
	})

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(listener) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	// 3. Open browser.
	loginURL := fmt.Sprintf("%s/auth/cli/google?mode=http&port=%d", apiURL, port)
	fmt.Printf("Opening browser for login...\n")
	fmt.Printf("If the browser doesn't open, visit:\n  %s\n\n", loginURL)

	if err := core.OpenBrowser(loginURL); err != nil {
		fmt.Printf("Could not open browser: %v\n", err)
	}

	// 4. Wait for callback with timeout.
	fmt.Printf("Waiting for authentication (5 minute timeout)...\n")
	select {
	case result := <-tokenCh:
		if result.err != nil {
			return result.err
		}
		if err := mgr.SaveTokens(apiURL, result.accessToken, result.refreshToken); err != nil {
			return fmt.Errorf("save tokens: %w", err)
		}
		maybePairChatRelayDevice(apiURL, mgr, result.accessToken, os.Stderr)
		return verifyAndPrint(apiURL, result.accessToken)

	case <-time.After(5 * time.Minute):
		return fmt.Errorf("login timed out (5 minutes)")
	}
}

func loginCode(apiURL string, mgr *core.APITokenManager) error {
	// 1. Discovery first: resolves service topology before user pastes code.
	_ = applyDiscovery(apiURL, mgr)

	loginURL := fmt.Sprintf("%s/auth/cli/google?mode=code", apiURL)
	fmt.Printf("Open this URL in your browser:\n\n  %s\n\n", loginURL)
	fmt.Printf("Enter the code from the browser: ")

	var code string
	if _, err := fmt.Scanln(&code); err != nil {
		return fmt.Errorf("read code: %w", err)
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return fmt.Errorf("empty code")
	}

	// 2. Exchange code for tokens.
	payload, _ := json.Marshal(map[string]string{"code": code})
	resp, err := http.Post(
		apiURL+"/auth/cli/exchange",
		"application/json",
		strings.NewReader(string(payload)),
	)
	if err != nil {
		return fmt.Errorf("exchange request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("exchange failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if err := mgr.SaveTokens(apiURL, result.AccessToken, result.RefreshToken); err != nil {
		return fmt.Errorf("save tokens: %w", err)
	}
	maybePairChatRelayDevice(apiURL, mgr, result.AccessToken, os.Stderr)
	return verifyAndPrint(apiURL, result.AccessToken)
}

func verifyAndPrint(apiURL, accessToken string) error {
	req, err := http.NewRequest("GET", apiURL+"/auth/me", nil)
	if err != nil {
		fmt.Printf("Login successful.\n")
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("Login successful (could not verify: %v)\n", err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Login successful.\n")
		return nil
	}

	var user struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		fmt.Printf("Login successful.\n")
		return nil
	}

	fmt.Printf("Logged in as %s (%s)\n", user.Name, user.Email)
	return nil
}

type tokenResult struct {
	accessToken  string
	refreshToken string
	err          error
}

const loginSuccessHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8"><title>KittyPaw Login</title>
<style>
  body { font-family: -apple-system, sans-serif; display: flex; justify-content: center; align-items: center; min-height: 100vh; margin: 0; background: #f5f5f7; }
  .card { background: white; border-radius: 12px; padding: 48px; text-align: center; box-shadow: 0 4px 24px rgba(0,0,0,0.1); }
</style>
</head>
<body>
<div class="card">
  <h2>Login complete</h2>
  <p>You can close this tab and return to the terminal.</p>
</div>
</body>
</html>`
