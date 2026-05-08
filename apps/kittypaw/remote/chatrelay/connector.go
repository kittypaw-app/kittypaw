package chatrelay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"
)

const maxRelayFrameBytes = 1 << 20

var ErrUnauthorized = errors.New("chat relay unauthorized")

// ErrCredentialInvalid tells the connector that refreshing credentials cannot
// recover without user login or device re-pairing.
var ErrCredentialInvalid = errors.New("chat relay credential invalid")

type ConnectorConfig struct {
	RelayURL      string
	Credential    string
	DeviceID      string
	LocalAccounts []string
	DaemonVersion string
	Capabilities  []string
}

type Connector struct {
	Config            ConnectorConfig
	Dispatcher        Dispatcher
	RefreshCredential func(context.Context) (string, error)
}

type RunOptions struct {
	RetryInitialDelay time.Duration
	RetryMaxDelay     time.Duration
	Logf              func(format string, args ...any)
}

type Dispatcher interface {
	Dispatch(ctx context.Context, req RequestFrame) (DispatchResult, error)
}

type DispatchResult struct {
	Status  int
	Headers map[string]string
	Body    []byte
}

type DispatchError struct {
	Code    string
	Message string
}

func (e DispatchError) Error() string {
	return e.Message
}

func BuildDaemonConnectURL(base string) (string, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		return "", fmt.Errorf("chat relay url is required")
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse chat relay url: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("chat relay url must include scheme and host")
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported chat relay url scheme %q", u.Scheme)
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

func (c *Connector) DialAndSendHello(ctx context.Context) (*websocket.Conn, error) {
	cfg := c.Config
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	endpoint, err := BuildDaemonConnectURL(cfg.RelayURL)
	if err != nil {
		return nil, err
	}

	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+cfg.Credential)
	conn, resp, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{
		HTTPHeader: headers,
	})
	if err != nil {
		if resp != nil {
			if resp.Body != nil {
				_ = resp.Body.Close()
			}
			if resp.StatusCode == http.StatusUnauthorized {
				return nil, fmt.Errorf("%w (%s): %v", ErrUnauthorized, resp.Status, err)
			}
			return nil, fmt.Errorf("chat relay dial failed (%s): %w", resp.Status, err)
		}
		return nil, fmt.Errorf("chat relay dial: %w", err)
	}
	conn.SetReadLimit(maxRelayFrameBytes)

	hello := NewHelloFrame(cfg.DeviceID, cfg.LocalAccounts, cfg.DaemonVersion, cfg.Capabilities)
	raw, err := json.Marshal(hello)
	if err != nil {
		conn.CloseNow()
		return nil, fmt.Errorf("marshal hello: %w", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, raw); err != nil {
		conn.CloseNow()
		return nil, fmt.Errorf("write hello: %w", err)
	}
	return conn, nil
}

func (c *Connector) Run(ctx context.Context, opts RunOptions) {
	delay := opts.RetryInitialDelay
	if delay <= 0 {
		delay = time.Second
	}
	maxDelay := opts.RetryMaxDelay
	if maxDelay <= 0 {
		maxDelay = 30 * time.Second
	}
	for {
		if ctx.Err() != nil {
			return
		}

		conn, err := c.DialAndSendHello(ctx)
		if err == nil {
			delay = opts.RetryInitialDelay
			if delay <= 0 {
				delay = time.Second
			}
			readErr := c.readLoop(ctx, conn)
			conn.CloseNow()
			if ctx.Err() != nil {
				return
			}
			if isDeviceConnectionReplaced(readErr) {
				if opts.Logf != nil {
					opts.Logf("chat relay stopped: device connection replaced by another server using the same device credential")
				}
				return
			}
			if readErr != nil && opts.Logf != nil {
				opts.Logf("chat relay disconnected: %v", readErr)
			}
		} else {
			if errors.Is(err, ErrUnauthorized) && c.RefreshCredential != nil {
				nextCredential, refreshErr := c.RefreshCredential(ctx)
				if ctx.Err() != nil {
					return
				}
				if errors.Is(refreshErr, ErrCredentialInvalid) {
					if opts.Logf != nil {
						opts.Logf("chat relay credential invalid for local accounts %s; run `kittypaw login` to reconnect hosted chat", strings.Join(c.Config.LocalAccounts, ","))
					}
					return
				}
				if refreshErr == nil && strings.TrimSpace(nextCredential) != "" {
					c.Config.Credential = nextCredential
					delay = opts.RetryInitialDelay
					if delay <= 0 {
						delay = time.Second
					}
					if opts.Logf != nil {
						opts.Logf("chat relay credential refreshed after unauthorized dial")
					}
					continue
				}
				if refreshErr != nil {
					err = fmt.Errorf("%w; credential refresh failed: %w", err, refreshErr)
				} else {
					err = fmt.Errorf("%w; credential refresh returned empty token", err)
				}
			}
			if opts.Logf != nil {
				opts.Logf("chat relay connect failed: %v", err)
			}
		}

		if !sleepContext(ctx, delay) {
			return
		}
		delay *= 2
		if delay > maxDelay {
			delay = maxDelay
		}
	}
}

func isDeviceConnectionReplaced(err error) bool {
	var closeErr websocket.CloseError
	if !errors.As(err, &closeErr) {
		return false
	}
	if closeErr.Code != websocket.StatusNormalClosure {
		return false
	}
	reason := strings.ToLower(strings.TrimSpace(closeErr.Reason))
	return reason == "device disconnected" || reason == "device connection replaced"
}

func (c *Connector) readLoop(ctx context.Context, conn *websocket.Conn) error {
	loopCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	writer := &frameWriter{conn: conn}
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		if typ != websocket.MessageText {
			if err := writer.writeError(loopCtx, "", "unsupported_frame", "chat relay frames must be text JSON"); err != nil {
				return err
			}
			continue
		}
		if err := c.handleFrame(loopCtx, writer, data); err != nil {
			return err
		}
	}
}

func (c *Connector) handleFrame(ctx context.Context, writer *frameWriter, data []byte) error {
	var envelope struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		Operation string `json:"operation"`
		AccountID string `json:"account_id"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return writer.writeError(ctx, "", "bad_frame", "invalid chat relay frame JSON")
	}
	if envelope.Type != FrameRequest {
		return writer.writeError(ctx, envelope.ID, "unsupported_frame", "unsupported chat relay frame type")
	}
	if !containsString(c.Config.LocalAccounts, envelope.AccountID) {
		return writer.writeError(ctx, envelope.ID, "unknown_account", "account is not active on this server connection")
	}
	if !SupportedOperation(envelope.Operation) {
		return writer.writeError(ctx, envelope.ID, "unsupported_operation", "unsupported chat relay operation")
	}
	if !containsString(EffectiveCapabilities(c.Config.Capabilities), envelope.Operation) {
		return writer.writeError(ctx, envelope.ID, "unsupported_capability", "operation was not advertised by this server connection")
	}
	if c.Dispatcher == nil {
		return writer.writeError(ctx, envelope.ID, "not_implemented", "chat relay operation dispatch is not implemented")
	}

	var req RequestFrame
	if err := json.Unmarshal(data, &req); err != nil {
		return writer.writeError(ctx, envelope.ID, "bad_frame", "invalid chat relay request")
	}
	go c.dispatchAndWrite(ctx, writer, req)
	return nil
}

func (c *Connector) dispatchAndWrite(ctx context.Context, writer *frameWriter, req RequestFrame) {
	result, err := c.Dispatcher.Dispatch(ctx, req)
	if err != nil {
		var dispatchErr DispatchError
		if errors.As(err, &dispatchErr) {
			_ = writer.writeError(ctx, req.ID, dispatchErr.Code, dispatchErr.Message)
			return
		}
		_ = writer.writeError(ctx, req.ID, "dispatch_error", err.Error())
		return
	}
	_ = writer.writeDispatchResult(ctx, req.ID, result)
}

type frameWriter struct {
	mu   sync.Mutex
	conn *websocket.Conn
}

func (w *frameWriter) writeJSON(ctx context.Context, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn.Write(ctx, websocket.MessageText, raw)
}

func (w *frameWriter) writeError(ctx context.Context, id, code, message string) error {
	return w.writeJSON(ctx, ErrorFrame{
		Type:    FrameError,
		ID:      id,
		Code:    code,
		Message: message,
	})
}

func (w *frameWriter) writeDispatchResult(ctx context.Context, id string, result DispatchResult) error {
	status := result.Status
	if status == 0 {
		status = http.StatusOK
	}
	headers := ResponseHeadersFrame{
		Type:    FrameResponseHeaders,
		ID:      id,
		Status:  status,
		Headers: result.Headers,
	}
	if err := w.writeJSON(ctx, headers); err != nil {
		return err
	}
	if len(result.Body) > 0 {
		chunk := ResponseChunkFrame{
			Type: FrameResponseChunk,
			ID:   id,
			Data: string(result.Body),
		}
		if err := w.writeJSON(ctx, chunk); err != nil {
			return err
		}
	}
	end := ResponseEndFrame{Type: FrameResponseEnd, ID: id}
	return w.writeJSON(ctx, end)
}

func (cfg ConnectorConfig) validate() error {
	if strings.TrimSpace(cfg.RelayURL) == "" {
		return fmt.Errorf("chat relay url is required")
	}
	if strings.TrimSpace(cfg.Credential) == "" {
		return fmt.Errorf("chat relay credential is required")
	}
	if strings.TrimSpace(cfg.DeviceID) == "" {
		return fmt.Errorf("chat relay device id is required")
	}
	if len(cfg.LocalAccounts) == 0 {
		return fmt.Errorf("chat relay local accounts are required")
	}
	return nil
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func sleepContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
