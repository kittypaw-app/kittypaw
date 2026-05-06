package smoke

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/kittypaw-app/kittyhome/internal/protocol"
)

const defaultRemoteSmokeTimeout = 15 * time.Second

type RemoteConfig struct {
	BaseURL        string
	UserToken      string
	DeviceToken    string
	DeviceID       string
	LocalAccountID string
	UserID         string
	Timeout        time.Duration
}

const (
	remoteSmokeUserText = "hello from cutover smoke"
	remoteSmokeReply    = "hello from cutover smoke"
)

func RunRemote(ctx context.Context, cfg RemoteConfig, out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}
	wsURL, err := remoteWebSocketURL(cfg.BaseURL)
	if err != nil {
		return fmt.Errorf("derive daemon websocket url: %w", err)
	}
	daemonReady := make(chan struct{})
	daemonDone := make(chan error, 1)
	go func() {
		daemonDone <- runRemoteFakeDaemon(ctx, cfg, wsURL, daemonReady)
	}()

	select {
	case <-daemonReady:
		if err := writeProgress(out, "ok daemon connected"); err != nil {
			return err
		}
	case err := <-daemonDone:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}

	if err := waitForRemoteRoute(ctx, cfg); err != nil {
		return err
	}
	if err := writeProgress(out, fmt.Sprintf("ok route discovery %s/%s", cfg.DeviceID, cfg.LocalAccountID)); err != nil {
		return err
	}

	if err := runRemoteChatCompletion(ctx, cfg); err != nil {
		return err
	}
	if err := writeProgress(out, "ok chat completion relayed"); err != nil {
		return err
	}

	select {
	case err := <-daemonDone:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func LoadRemoteConfig() (RemoteConfig, error) {
	cfg := RemoteConfig{
		BaseURL:        strings.TrimRight(strings.TrimSpace(os.Getenv("HOME_BASE_URL")), "/"),
		UserToken:      strings.TrimSpace(os.Getenv("HOME_USER_TOKEN")),
		DeviceToken:    strings.TrimSpace(os.Getenv("HOME_DEVICE_TOKEN")),
		DeviceID:       strings.TrimSpace(os.Getenv("HOME_DEVICE_ID")),
		LocalAccountID: strings.TrimSpace(os.Getenv("HOME_LOCAL_ACCOUNT_ID")),
		UserID:         strings.TrimSpace(os.Getenv("HOME_SMOKE_USER_ID")),
		Timeout:        defaultRemoteSmokeTimeout,
	}
	required := []struct {
		name  string
		value string
	}{
		{name: "HOME_BASE_URL", value: cfg.BaseURL},
		{name: "HOME_USER_TOKEN", value: cfg.UserToken},
		{name: "HOME_DEVICE_TOKEN", value: cfg.DeviceToken},
		{name: "HOME_DEVICE_ID", value: cfg.DeviceID},
		{name: "HOME_LOCAL_ACCOUNT_ID", value: cfg.LocalAccountID},
	}
	for _, item := range required {
		if item.value == "" {
			return RemoteConfig{}, fmt.Errorf("%s is required", item.name)
		}
	}
	if raw := strings.TrimSpace(os.Getenv("HOME_SMOKE_TIMEOUT")); raw != "" {
		timeout, err := time.ParseDuration(raw)
		if err != nil {
			return RemoteConfig{}, fmt.Errorf("HOME_SMOKE_TIMEOUT: %w", err)
		}
		if timeout <= 0 {
			return RemoteConfig{}, fmt.Errorf("HOME_SMOKE_TIMEOUT must be greater than 0")
		}
		cfg.Timeout = timeout
	}
	if _, err := remoteWebSocketURL(cfg.BaseURL); err != nil {
		return RemoteConfig{}, fmt.Errorf("HOME_BASE_URL: %w", err)
	}
	return cfg, nil
}

func runRemoteFakeDaemon(ctx context.Context, cfg RemoteConfig, wsURL string, ready chan<- struct{}) error {
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + cfg.DeviceToken}},
	})
	if err != nil {
		return fmt.Errorf("connect fake daemon %s: %w", wsURL, err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "cutover smoke done") }()

	if err := wsjson.Write(ctx, conn, protocol.Frame{
		Type:            protocol.FrameHello,
		DeviceID:        cfg.DeviceID,
		LocalAccounts:   []string{cfg.LocalAccountID},
		DaemonVersion:   "cutover-smoke",
		ProtocolVersion: protocol.ProtocolVersion1,
		Capabilities:    []protocol.Operation{protocol.OperationOpenAIChatCompletions},
	}); err != nil {
		return fmt.Errorf("write hello: %w", err)
	}
	close(ready)

	var req protocol.Frame
	if err := wsjson.Read(ctx, conn, &req); err != nil {
		return fmt.Errorf("read relay request: %w", err)
	}
	if err := validateRemoteSmokeRequest(req, cfg); err != nil {
		return err
	}
	if err := writeRemoteSmokeResponse(ctx, conn, req); err != nil {
		return err
	}
	return nil
}

func validateRemoteSmokeRequest(frame protocol.Frame, cfg RemoteConfig) error {
	if frame.Type != protocol.FrameRequest {
		return fmt.Errorf("request frame type = %q, want %q", frame.Type, protocol.FrameRequest)
	}
	if frame.AccountID != cfg.LocalAccountID {
		return fmt.Errorf("request account_id = %q, want %q", frame.AccountID, cfg.LocalAccountID)
	}
	if frame.Operation != protocol.OperationOpenAIChatCompletions {
		return fmt.Errorf("request operation = %q, want %q", frame.Operation, protocol.OperationOpenAIChatCompletions)
	}
	if frame.Method != http.MethodPost || frame.Path != "/v1/chat/completions" {
		return fmt.Errorf("request method/path = %s %s, want POST /v1/chat/completions", frame.Method, frame.Path)
	}
	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(frame.Body, &body); err != nil {
		return fmt.Errorf("decode request body: %w", err)
	}
	for _, message := range body.Messages {
		if message.Role == "user" && message.Content == remoteSmokeUserText {
			return nil
		}
	}
	return fmt.Errorf("request body does not contain cutover smoke user message")
}

func writeRemoteSmokeResponse(ctx context.Context, conn *websocket.Conn, req protocol.Frame) error {
	frames := []protocol.Frame{
		{
			Type:    protocol.FrameResponseHeaders,
			ID:      req.ID,
			Status:  http.StatusOK,
			Headers: map[string]string{"content-type": "text/event-stream"},
		},
		{
			Type: protocol.FrameResponseChunk,
			ID:   req.ID,
			Data: fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", remoteSmokeReply),
		},
		{Type: protocol.FrameResponseChunk, ID: req.ID, Data: "data: [DONE]\n\n"},
		{Type: protocol.FrameResponseEnd, ID: req.ID},
	}
	for _, frame := range frames {
		if err := wsjson.Write(ctx, conn, frame); err != nil {
			return fmt.Errorf("write daemon response: %w", err)
		}
	}
	return nil
}

func waitForRemoteRoute(ctx context.Context, cfg RemoteConfig) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		ok, err := remoteRouteOnline(ctx, cfg)
		if ok {
			return nil
		}
		if err != nil {
			lastErr = err
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("wait for remote route: %w", lastErr)
			}
			return ctx.Err()
		}
	}
}

func remoteRouteOnline(ctx context.Context, cfg RemoteConfig) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.BaseURL+"/v1/routes", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.UserToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("routes status = %d; body=%s", resp.StatusCode, raw)
	}
	var body struct {
		Data []struct {
			DeviceID      string               `json:"device_id"`
			LocalAccounts []string             `json:"local_accounts"`
			Capabilities  []protocol.Operation `json:"capabilities"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return false, err
	}
	for _, route := range body.Data {
		if route.DeviceID == cfg.DeviceID &&
			hasString(route.LocalAccounts, cfg.LocalAccountID) &&
			hasOperation(route.Capabilities, protocol.OperationOpenAIChatCompletions) {
			return true, nil
		}
	}
	return false, nil
}

func runRemoteChatCompletion(ctx context.Context, cfg RemoteConfig) error {
	body := []byte(fmt.Sprintf(`{"model":"kittypaw","stream":true,"messages":[{"role":"user","content":%q}]}`, remoteSmokeUserText))
	endpoint := fmt.Sprintf("%s/nodes/%s/accounts/%s/v1/chat/completions",
		cfg.BaseURL, url.PathEscape(cfg.DeviceID), url.PathEscape(cfg.LocalAccountID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.UserToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("chat status = %d; body=%s", resp.StatusCode, raw)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		return fmt.Errorf("chat content-type = %q, want text/event-stream", got)
	}
	text := string(raw)
	if !strings.Contains(text, remoteSmokeReply) || !strings.Contains(text, "data: [DONE]") {
		return fmt.Errorf("chat body = %q, want cutover smoke reply and done marker", text)
	}
	return nil
}

func remoteWebSocketURL(baseURL string) (string, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	basePath := strings.TrimRight(u.Path, "/")
	if basePath == "" {
		u.Path = "/daemon/connect"
	} else {
		u.Path = basePath + "/daemon/connect"
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}
