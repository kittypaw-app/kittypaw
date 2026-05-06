package daemonws

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/go-chi/chi/v5"
	"github.com/kittypaw-app/kittyspace/internal/broker"
	"github.com/kittypaw-app/kittyspace/internal/openai"
	"github.com/kittypaw-app/kittyspace/internal/protocol"
)

func TestDaemonWebSocketRelaysOpenAIRequestToDaemon(t *testing.T) {
	b := broker.New(broker.Config{
		RequestTimeout:       2 * time.Second,
		MaxInflightPerDevice: 4,
	})
	principal := broker.DevicePrincipal{
		UserID:          "user_1",
		DeviceID:        "dev_1",
		LocalAccountIDs: []string{"alice"},
	}

	r := chi.NewRouter()
	r.Mount("/daemon", NewHandler(StaticTokenAuthenticator{
		Token:     "dev_secret",
		Principal: principal,
	}, b).Routes())
	r.Mount("/", openai.NewHandler(openai.StaticTokenAuthenticator{
		Token: "api_secret",
		Principal: openai.Principal{
			UserID:    "user_1",
			DeviceID:  "dev_1",
			AccountID: "alice",
			Scopes:    []string{"models:read"},
		},
	}, b).Routes())

	srv := httptest.NewServer(r)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/daemon/connect"
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer dev_secret"}},
	})
	if err != nil {
		t.Fatalf("dial daemon websocket: %v", err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "test done") }()

	if err := wsjson.Write(ctx, conn, protocol.Frame{
		Type:            protocol.FrameHello,
		DeviceID:        "dev_1",
		LocalAccounts:   []string{"alice"},
		DaemonVersion:   "test",
		ProtocolVersion: protocol.ProtocolVersion1,
		Capabilities:    []protocol.Operation{protocol.OperationOpenAIModels, protocol.OperationOpenAIChatCompletions},
	}); err != nil {
		t.Fatalf("write hello: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		var reqFrame protocol.Frame
		if err := wsjson.Read(ctx, conn, &reqFrame); err != nil {
			errCh <- err
			return
		}
		if reqFrame.Type != protocol.FrameRequest || reqFrame.Operation != protocol.OperationOpenAIModels || reqFrame.Path != "/v1/models" {
			errCh <- &unexpectedFrameError{frame: reqFrame}
			return
		}
		frames := []protocol.Frame{
			{
				Type:    protocol.FrameResponseHeaders,
				ID:      reqFrame.ID,
				Status:  http.StatusOK,
				Headers: map[string]string{"content-type": "application/json"},
			},
			{
				Type: protocol.FrameResponseChunk,
				ID:   reqFrame.ID,
				Data: `{"object":"list","data":[{"id":"kittypaw","object":"model","owned_by":"kittypaw"}]}`,
			},
			{Type: protocol.FrameResponseEnd, ID: reqFrame.ID},
		}
		for _, frame := range frames {
			if err := wsjson.Write(ctx, conn, frame); err != nil {
				errCh <- err
				return
			}
		}
		errCh <- nil
	}()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/nodes/dev_1/v1/models", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	httpReq.Header.Set("Authorization", "Bearer api_secret")
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("openai client request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, raw)
	}
	var body struct {
		Object string `json:"object"`
		Data   []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode body %q: %v", raw, err)
	}
	if body.Object != "list" || len(body.Data) != 1 || body.Data[0].ID != "kittypaw" {
		t.Fatalf("body = %+v", body)
	}

	if err := <-errCh; err != nil {
		t.Fatalf("daemon goroutine: %v", err)
	}
}

func TestDaemonWebSocketRejectsBadToken(t *testing.T) {
	b := broker.New(broker.Config{})
	r := chi.NewRouter()
	r.Mount("/daemon", NewHandler(StaticTokenAuthenticator{
		Token: "dev_secret",
		Principal: broker.DevicePrincipal{
			UserID:          "user_1",
			DeviceID:        "dev_1",
			LocalAccountIDs: []string{"alice"},
		},
	}, b).Routes())
	srv := httptest.NewServer(r)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/daemon/connect"
	_, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer wrong"}},
	})
	if err == nil {
		t.Fatal("dial succeeded with wrong token")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %v, want 401", responseStatus(resp))
	}
}

func TestDaemonWebSocketRegistersOnlyHelloAdvertisedAccounts(t *testing.T) {
	fb := &captureBroker{registered: make(chan broker.DevicePrincipal, 1)}
	r := chi.NewRouter()
	r.Mount("/daemon", NewHandler(StaticTokenAuthenticator{
		Token: "dev_secret",
		Principal: broker.DevicePrincipal{
			UserID:          "user_1",
			DeviceID:        "dev_1",
			LocalAccountIDs: []string{"alice", "bob"},
		},
	}, fb).Routes())
	srv := httptest.NewServer(r)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/daemon/connect"
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer dev_secret"}},
	})
	if err != nil {
		t.Fatalf("dial daemon websocket: %v", err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "test done") }()

	if err := wsjson.Write(ctx, conn, protocol.Frame{
		Type:            protocol.FrameHello,
		DeviceID:        "dev_1",
		LocalAccounts:   []string{"alice"},
		DaemonVersion:   "test",
		ProtocolVersion: protocol.ProtocolVersion1,
		Capabilities:    []protocol.Operation{protocol.OperationOpenAIModels},
	}); err != nil {
		t.Fatalf("write hello: %v", err)
	}

	select {
	case got := <-fb.registered:
		if got.UserID != "user_1" || got.DeviceID != "dev_1" {
			t.Fatalf("registered identity = %+v", got)
		}
		if len(got.LocalAccountIDs) != 1 || got.LocalAccountIDs[0] != "alice" {
			t.Fatalf("registered accounts = %+v, want [alice]", got.LocalAccountIDs)
		}
		if len(got.Capabilities) != 1 || got.Capabilities[0] != protocol.OperationOpenAIModels {
			t.Fatalf("registered capabilities = %+v, want [openai.models]", got.Capabilities)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for broker registration")
	}
}

func TestDaemonWebSocketAllowsJWTScopedHelloAccounts(t *testing.T) {
	fb := &captureBroker{registered: make(chan broker.DevicePrincipal, 1)}
	r := chi.NewRouter()
	r.Mount("/daemon", NewHandler(StaticTokenAuthenticator{
		Token: "dev_secret",
		Principal: broker.DevicePrincipal{
			UserID:   "user_1",
			DeviceID: "dev_1",
		},
	}, fb).Routes())
	srv := httptest.NewServer(r)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/daemon/connect"
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer dev_secret"}},
	})
	if err != nil {
		t.Fatalf("dial daemon websocket: %v", err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "test done") }()

	if err := wsjson.Write(ctx, conn, protocol.Frame{
		Type:            protocol.FrameHello,
		DeviceID:        "dev_1",
		LocalAccounts:   []string{"alice", "bob"},
		DaemonVersion:   "test",
		ProtocolVersion: protocol.ProtocolVersion1,
		Capabilities:    []protocol.Operation{protocol.OperationOpenAIModels},
	}); err != nil {
		t.Fatalf("write hello: %v", err)
	}

	select {
	case got := <-fb.registered:
		if got.UserID != "user_1" || got.DeviceID != "dev_1" {
			t.Fatalf("registered identity = %+v", got)
		}
		if len(got.LocalAccountIDs) != 2 || got.LocalAccountIDs[0] != "alice" || got.LocalAccountIDs[1] != "bob" {
			t.Fatalf("registered accounts = %+v, want [alice bob]", got.LocalAccountIDs)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for broker registration")
	}
}

func TestDaemonWebSocketRejectsUnexpectedPostHelloFrame(t *testing.T) {
	fb := &captureBroker{
		registered: make(chan broker.DevicePrincipal, 1),
		delivered:  make(chan protocol.Frame, 1),
	}
	r := chi.NewRouter()
	r.Mount("/daemon", NewHandler(StaticTokenAuthenticator{
		Token: "dev_secret",
		Principal: broker.DevicePrincipal{
			UserID:          "user_1",
			DeviceID:        "dev_1",
			LocalAccountIDs: []string{"alice"},
		},
	}, fb).Routes())
	srv := httptest.NewServer(r)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/daemon/connect"
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer dev_secret"}},
	})
	if err != nil {
		t.Fatalf("dial daemon websocket: %v", err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "test done") }()

	if err := wsjson.Write(ctx, conn, protocol.Frame{
		Type:            protocol.FrameHello,
		DeviceID:        "dev_1",
		LocalAccounts:   []string{"alice"},
		DaemonVersion:   "test",
		ProtocolVersion: protocol.ProtocolVersion1,
		Capabilities:    []protocol.Operation{protocol.OperationOpenAIModels},
	}); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	select {
	case <-fb.registered:
	case <-ctx.Done():
		t.Fatal("timed out waiting for broker registration")
	}

	err = wsjson.Write(ctx, conn, protocol.Frame{
		Type:      protocol.FrameRequest,
		ID:        "req_bad",
		AccountID: "alice",
		Operation: protocol.OperationOpenAIModels,
		Method:    http.MethodGet,
		Path:      "/v1/models",
	})
	if err != nil {
		t.Fatalf("write unexpected request frame: %v", err)
	}

	readCtx, readCancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer readCancel()
	var frame protocol.Frame
	err = wsjson.Read(readCtx, conn, &frame)
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("read error = %v, close status = %v, want policy violation", err, websocket.CloseStatus(err))
	}
	select {
	case delivered := <-fb.delivered:
		t.Fatalf("unexpected frame delivered to broker: %+v", delivered)
	default:
	}
}

func TestDaemonWebSocketRespondsToPingWithoutBrokerDelivery(t *testing.T) {
	fb := &captureBroker{
		registered: make(chan broker.DevicePrincipal, 1),
		delivered:  make(chan protocol.Frame, 1),
	}
	r := chi.NewRouter()
	r.Mount("/daemon", NewHandler(StaticTokenAuthenticator{
		Token: "dev_secret",
		Principal: broker.DevicePrincipal{
			UserID:          "user_1",
			DeviceID:        "dev_1",
			LocalAccountIDs: []string{"alice"},
		},
	}, fb).Routes())
	srv := httptest.NewServer(r)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/daemon/connect"
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer dev_secret"}},
	})
	if err != nil {
		t.Fatalf("dial daemon websocket: %v", err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "test done") }()

	if err := wsjson.Write(ctx, conn, protocol.Frame{
		Type:            protocol.FrameHello,
		DeviceID:        "dev_1",
		LocalAccounts:   []string{"alice"},
		DaemonVersion:   "test",
		ProtocolVersion: protocol.ProtocolVersion1,
		Capabilities:    []protocol.Operation{protocol.OperationOpenAIModels},
	}); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	select {
	case <-fb.registered:
	case <-ctx.Done():
		t.Fatal("timed out waiting for broker registration")
	}

	if err := wsjson.Write(ctx, conn, protocol.Frame{Type: protocol.FramePing, ID: "ping_1"}); err != nil {
		t.Fatalf("write ping: %v", err)
	}
	readCtx, readCancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer readCancel()
	var got protocol.Frame
	if err := wsjson.Read(readCtx, conn, &got); err != nil {
		t.Fatalf("read pong: %v", err)
	}
	if got.Type != protocol.FramePong || got.ID != "ping_1" {
		t.Fatalf("pong frame = %+v, want pong with matching id", got)
	}
	select {
	case delivered := <-fb.delivered:
		t.Fatalf("unexpected ping delivered to broker: %+v", delivered)
	default:
	}
}

type captureBroker struct {
	registered chan broker.DevicePrincipal
	delivered  chan protocol.Frame
}

func (b *captureBroker) Register(ctx context.Context, principal broker.DevicePrincipal, _ broker.DeviceConn) error {
	select {
	case b.registered <- principal:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *captureBroker) Deliver(_ string, _ string, frame protocol.Frame) {
	if b.delivered == nil {
		return
	}
	select {
	case b.delivered <- frame:
	default:
	}
}

func (b *captureBroker) Unregister(string, string, broker.DeviceConn) {}

type unexpectedFrameError struct {
	frame protocol.Frame
}

func (e *unexpectedFrameError) Error() string {
	raw, _ := json.Marshal(e.frame)
	return "unexpected frame: " + string(raw)
}

func responseStatus(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}
