package chatrelay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

func TestBuildDaemonConnectURL(t *testing.T) {
	tests := []struct {
		name string
		base string
		want string
	}{
		{
			name: "https base",
			base: "https://chat.kittypaw.app",
			want: "wss://chat.kittypaw.app/daemon/connect",
		},
		{
			name: "http local base",
			base: "http://localhost:8080",
			want: "ws://localhost:8080/daemon/connect",
		},
		{
			name: "wss path base",
			base: "wss://chat.kittypaw.app/base/",
			want: "wss://chat.kittypaw.app/base/daemon/connect",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BuildDaemonConnectURL(tt.base)
			if err != nil {
				t.Fatalf("BuildDaemonConnectURL: %v", err)
			}
			if got != tt.want {
				t.Fatalf("BuildDaemonConnectURL(%q) = %q, want %q", tt.base, got, tt.want)
			}
		})
	}
}

func TestDialAndSendHelloSendsAuthorizationAndHello(t *testing.T) {
	helloCh := make(chan HelloFrame, 1)
	errCh := make(chan error, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/daemon/connect" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer device-token-1" {
			http.Error(w, "bad authorization", http.StatusUnauthorized)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			errCh <- err
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "bye")

		_, data, err := conn.Read(r.Context())
		if err != nil {
			errCh <- err
			return
		}
		var hello HelloFrame
		if err := json.Unmarshal(data, &hello); err != nil {
			errCh <- err
			return
		}
		helloCh <- hello
	}))
	defer ts.Close()

	connector := &Connector{Config: ConnectorConfig{
		RelayURL:      ts.URL,
		Credential:    "device-token-1",
		DeviceID:      "dev_1",
		LocalAccounts: []string{"alice"},
		DaemonVersion: "0.1.5",
	}}
	conn, err := connector.DialAndSendHello(context.Background())
	if err != nil {
		t.Fatalf("DialAndSendHello: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "test done")

	select {
	case err := <-errCh:
		t.Fatalf("server read hello: %v", err)
	case hello := <-helloCh:
		if hello.Type != FrameHello || hello.DeviceID != "dev_1" || hello.ProtocolVersion != ProtocolVersion {
			t.Fatalf("hello = %#v", hello)
		}
		if len(hello.LocalAccounts) != 1 || hello.LocalAccounts[0] != "alice" {
			t.Fatalf("hello local accounts = %#v", hello.LocalAccounts)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for hello frame")
	}
}

func TestDialAndSendHelloRejectsMissingInputs(t *testing.T) {
	tests := []struct {
		name string
		cfg  ConnectorConfig
		want string
	}{
		{name: "missing relay url", cfg: ConnectorConfig{Credential: "tok", DeviceID: "dev"}, want: "relay url"},
		{name: "missing credential", cfg: ConnectorConfig{RelayURL: "https://chat.kittypaw.app", DeviceID: "dev"}, want: "credential"},
		{name: "missing device id", cfg: ConnectorConfig{RelayURL: "https://chat.kittypaw.app", Credential: "tok"}, want: "device id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, err := (&Connector{Config: tt.cfg}).DialAndSendHello(context.Background())
			if conn != nil {
				conn.CloseNow()
			}
			if err == nil {
				t.Fatal("DialAndSendHello error = nil")
			}
			if !strings.Contains(strings.ToLower(err.Error()), tt.want) {
				t.Fatalf("DialAndSendHello error = %q, want containing %q", err.Error(), tt.want)
			}
		})
	}
}

func TestRunRetriesUntilRelayAccepts(t *testing.T) {
	var attempts atomic.Int32
	helloCh := make(chan HelloFrame, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/daemon/connect" {
			http.NotFound(w, r)
			return
		}
		if attempts.Add(1) == 1 {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Logf("accept: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "bye")

		_, data, err := conn.Read(r.Context())
		if err != nil {
			t.Logf("read hello: %v", err)
			return
		}
		var hello HelloFrame
		if err := json.Unmarshal(data, &hello); err != nil {
			t.Logf("decode hello: %v", err)
			return
		}
		helloCh <- hello
	}))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	connector := &Connector{Config: ConnectorConfig{
		RelayURL:      ts.URL,
		Credential:    "device-token-1",
		DeviceID:      "dev_1",
		LocalAccounts: []string{"alice"},
		DaemonVersion: "0.1.5",
	}}
	go connector.Run(ctx, RunOptions{
		RetryInitialDelay: time.Millisecond,
		RetryMaxDelay:     time.Millisecond,
	})

	select {
	case hello := <-helloCh:
		if hello.DeviceID != "dev_1" {
			t.Fatalf("hello device id = %q", hello.DeviceID)
		}
		if attempts.Load() < 2 {
			t.Fatalf("attempts = %d, want retry after first failure", attempts.Load())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for retried hello")
	}
}

func TestRunStopsAfterDeviceConnectionIsReplaced(t *testing.T) {
	var attempts atomic.Int32
	helloCh := make(chan struct{}, 4)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/daemon/connect" {
			http.NotFound(w, r)
			return
		}
		attempts.Add(1)
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Logf("accept: %v", err)
			return
		}
		defer conn.CloseNow()
		_, data, err := conn.Read(r.Context())
		if err != nil {
			t.Logf("read hello: %v", err)
			return
		}
		var hello HelloFrame
		if err := json.Unmarshal(data, &hello); err != nil {
			t.Logf("decode hello: %v", err)
			return
		}
		helloCh <- struct{}{}
		_ = conn.Close(websocket.StatusNormalClosure, "device disconnected")
	}))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	connector := &Connector{Config: ConnectorConfig{
		RelayURL:      ts.URL,
		Credential:    "device-token-1",
		DeviceID:      "dev_1",
		LocalAccounts: []string{"alice"},
		DaemonVersion: "0.1.5",
	}}
	go connector.Run(ctx, RunOptions{
		RetryInitialDelay: 5 * time.Millisecond,
		RetryMaxDelay:     5 * time.Millisecond,
	})

	select {
	case <-helloCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for first relay connection")
	}
	time.Sleep(50 * time.Millisecond)
	if got := attempts.Load(); got != 1 {
		t.Fatalf("relay attempts = %d, want connector to stop after device replacement", got)
	}
}

func TestRunRefreshesCredentialAfterUnauthorizedDial(t *testing.T) {
	var attempts atomic.Int32
	var refreshes atomic.Int32
	helloCh := make(chan HelloFrame, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/daemon/connect" {
			http.NotFound(w, r)
			return
		}
		attempts.Add(1)
		if got := r.Header.Get("Authorization"); got != "Bearer access-2" {
			http.Error(w, "expired", http.StatusUnauthorized)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Logf("accept: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "bye")

		_, data, err := conn.Read(r.Context())
		if err != nil {
			t.Logf("read hello: %v", err)
			return
		}
		var hello HelloFrame
		if err := json.Unmarshal(data, &hello); err != nil {
			t.Logf("decode hello: %v", err)
			return
		}
		helloCh <- hello
	}))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	connector := &Connector{
		Config: ConnectorConfig{
			RelayURL:      ts.URL,
			Credential:    "access-1",
			DeviceID:      "dev_1",
			LocalAccounts: []string{"alice"},
			DaemonVersion: "0.1.5",
		},
		RefreshCredential: func(context.Context) (string, error) {
			refreshes.Add(1)
			return "access-2", nil
		},
	}
	go connector.Run(ctx, RunOptions{
		RetryInitialDelay: time.Hour,
		RetryMaxDelay:     time.Hour,
	})

	select {
	case hello := <-helloCh:
		if hello.DeviceID != "dev_1" {
			t.Fatalf("hello device id = %q", hello.DeviceID)
		}
		if got := refreshes.Load(); got != 1 {
			t.Fatalf("refreshes = %d, want 1", got)
		}
		if got := attempts.Load(); got != 2 {
			t.Fatalf("attempts = %d, want initial 401 plus refreshed retry", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for refreshed hello")
	}
}

func TestRunStopsWhenCredentialRefreshIsInvalid(t *testing.T) {
	var attempts atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/daemon/connect" {
			http.NotFound(w, r)
			return
		}
		attempts.Add(1)
		http.Error(w, "expired", http.StatusUnauthorized)
	}))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logCh := make(chan string, 4)
	done := make(chan struct{})
	connector := &Connector{
		Config: ConnectorConfig{
			RelayURL:      ts.URL,
			Credential:    "access-1",
			DeviceID:      "dev_1",
			LocalAccounts: []string{"alice"},
			DaemonVersion: "0.1.5",
		},
		RefreshCredential: func(context.Context) (string, error) {
			return "", ErrCredentialInvalid
		},
	}
	go func() {
		connector.Run(ctx, RunOptions{
			RetryInitialDelay: time.Hour,
			RetryMaxDelay:     time.Hour,
			Logf: func(format string, args ...any) {
				logCh <- fmt.Sprintf(format, args...)
			},
		})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("connector did not stop after terminal invalid credential")
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("dial attempts = %d, want 1", got)
	}
	select {
	case msg := <-logCh:
		if !strings.Contains(msg, "credential invalid") {
			t.Fatalf("log = %q, want credential invalid", msg)
		}
	default:
		t.Fatal("missing terminal credential log")
	}
}

func TestRunRejectsRequestForUnadvertisedCapability(t *testing.T) {
	errCh := make(chan error, 1)
	errorFrameCh := make(chan ErrorFrame, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			errCh <- err
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "bye")

		if _, _, err := conn.Read(r.Context()); err != nil {
			errCh <- err
			return
		}
		req := RequestFrame{
			Type:      FrameRequest,
			ID:        "req_1",
			Operation: OperationOpenAIModels,
			AccountID: "alice",
		}
		raw, err := json.Marshal(req)
		if err != nil {
			errCh <- err
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageText, raw); err != nil {
			errCh <- err
			return
		}
		_, data, err := conn.Read(r.Context())
		if err != nil {
			errCh <- err
			return
		}
		var got ErrorFrame
		if err := json.Unmarshal(data, &got); err != nil {
			errCh <- err
			return
		}
		errorFrameCh <- got
	}))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	connector := &Connector{Config: ConnectorConfig{
		RelayURL:      ts.URL,
		Credential:    "device-token-1",
		DeviceID:      "dev_1",
		LocalAccounts: []string{"alice"},
		DaemonVersion: "0.1.5",
		Capabilities:  []string{},
	}}
	go connector.Run(ctx, RunOptions{
		RetryInitialDelay: time.Millisecond,
		RetryMaxDelay:     time.Millisecond,
	})

	select {
	case err := <-errCh:
		t.Fatalf("relay server: %v", err)
	case got := <-errorFrameCh:
		if got.Type != FrameError || got.ID != "req_1" || got.Code != "unsupported_capability" {
			t.Fatalf("error frame = %#v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for request rejection")
	}
}

func TestRunUsesDefaultCapabilitiesForRequestValidation(t *testing.T) {
	errCh := make(chan error, 1)
	errorFrameCh := make(chan ErrorFrame, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			errCh <- err
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "bye")

		if _, _, err := conn.Read(r.Context()); err != nil {
			errCh <- err
			return
		}
		req := RequestFrame{
			Type:      FrameRequest,
			ID:        "req_default_caps",
			Operation: OperationOpenAIModels,
			AccountID: "alice",
		}
		raw, err := json.Marshal(req)
		if err != nil {
			errCh <- err
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageText, raw); err != nil {
			errCh <- err
			return
		}
		_, data, err := conn.Read(r.Context())
		if err != nil {
			errCh <- err
			return
		}
		var got ErrorFrame
		if err := json.Unmarshal(data, &got); err != nil {
			errCh <- err
			return
		}
		errorFrameCh <- got
	}))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	connector := &Connector{Config: ConnectorConfig{
		RelayURL:      ts.URL,
		Credential:    "device-token-1",
		DeviceID:      "dev_1",
		LocalAccounts: []string{"alice"},
		DaemonVersion: "0.1.5",
	}}
	go connector.Run(ctx, RunOptions{
		RetryInitialDelay: time.Millisecond,
		RetryMaxDelay:     time.Millisecond,
	})

	select {
	case err := <-errCh:
		t.Fatalf("relay server: %v", err)
	case got := <-errorFrameCh:
		if got.Type != FrameError || got.ID != "req_default_caps" || got.Code != "not_implemented" {
			t.Fatalf("error frame = %#v, want not_implemented because nil capabilities advertise defaults", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for request handling")
	}
}

func TestRunDispatchesRequestAndWritesResponseFrames(t *testing.T) {
	errCh := make(chan error, 1)
	framesCh := make(chan []json.RawMessage, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			errCh <- err
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "bye")

		if _, _, err := conn.Read(r.Context()); err != nil {
			errCh <- err
			return
		}
		req := RequestFrame{
			Type:      FrameRequest,
			ID:        "req_dispatch",
			Operation: OperationOpenAIModels,
			AccountID: "alice",
			Method:    "GET",
			Path:      "/v1/models",
		}
		raw, err := json.Marshal(req)
		if err != nil {
			errCh <- err
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageText, raw); err != nil {
			errCh <- err
			return
		}

		var frames []json.RawMessage
		for range 3 {
			_, data, err := conn.Read(r.Context())
			if err != nil {
				errCh <- err
				return
			}
			frames = append(frames, append([]byte(nil), data...))
		}
		framesCh <- frames
	}))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	connector := &Connector{
		Config: ConnectorConfig{
			RelayURL:      ts.URL,
			Credential:    "device-token-1",
			DeviceID:      "dev_1",
			LocalAccounts: []string{"alice"},
			DaemonVersion: "0.1.5",
		},
		Dispatcher: dispatchFunc(func(_ context.Context, req RequestFrame) (DispatchResult, error) {
			if req.ID != "req_dispatch" || req.Operation != OperationOpenAIModels || req.AccountID != "alice" || req.Method != "GET" || req.Path != "/v1/models" {
				t.Fatalf("dispatch req = %#v", req)
			}
			return DispatchResult{
				Status:  200,
				Headers: map[string]string{"content-type": "application/json"},
				Body:    []byte(`{"object":"list","data":[]}`),
			}, nil
		}),
	}
	go connector.Run(ctx, RunOptions{
		RetryInitialDelay: time.Millisecond,
		RetryMaxDelay:     time.Millisecond,
	})

	select {
	case err := <-errCh:
		t.Fatalf("relay server: %v", err)
	case frames := <-framesCh:
		var headers ResponseHeadersFrame
		if err := json.Unmarshal(frames[0], &headers); err != nil {
			t.Fatal(err)
		}
		if headers.Type != FrameResponseHeaders || headers.ID != "req_dispatch" || headers.Status != 200 {
			t.Fatalf("headers frame = %#v", headers)
		}
		if headers.Headers["content-type"] != "application/json" {
			t.Fatalf("headers = %#v", headers.Headers)
		}
		var chunk ResponseChunkFrame
		if err := json.Unmarshal(frames[1], &chunk); err != nil {
			t.Fatal(err)
		}
		if chunk.Type != FrameResponseChunk || chunk.ID != "req_dispatch" || chunk.Data != `{"object":"list","data":[]}` {
			t.Fatalf("chunk frame = %#v", chunk)
		}
		var end ResponseEndFrame
		if err := json.Unmarshal(frames[2], &end); err != nil {
			t.Fatal(err)
		}
		if end.Type != FrameResponseEnd || end.ID != "req_dispatch" {
			t.Fatalf("end frame = %#v", end)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for dispatch response frames")
	}
}

func TestRunDispatchesRequestsWithoutHeadOfLineBlocking(t *testing.T) {
	errCh := make(chan error, 1)
	fastHeaderCh := make(chan ResponseHeadersFrame, 1)
	releaseSlow := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseSlow) })

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			errCh <- err
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "bye")

		if _, _, err := conn.Read(r.Context()); err != nil {
			errCh <- err
			return
		}
		for _, req := range []RequestFrame{
			{Type: FrameRequest, ID: "slow", Operation: OperationOpenAIChatCompletions, AccountID: "alice"},
			{Type: FrameRequest, ID: "fast", Operation: OperationOpenAIModels, AccountID: "alice"},
		} {
			raw, err := json.Marshal(req)
			if err != nil {
				errCh <- err
				return
			}
			if err := conn.Write(r.Context(), websocket.MessageText, raw); err != nil {
				errCh <- err
				return
			}
		}

		for {
			_, data, err := conn.Read(r.Context())
			if err != nil {
				errCh <- err
				return
			}
			var header ResponseHeadersFrame
			if err := json.Unmarshal(data, &header); err != nil {
				continue
			}
			if header.Type == FrameResponseHeaders && header.ID == "fast" {
				fastHeaderCh <- header
				releaseOnce.Do(func() { close(releaseSlow) })
				return
			}
		}
	}))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	connector := &Connector{
		Config: ConnectorConfig{
			RelayURL:      ts.URL,
			Credential:    "device-token-1",
			DeviceID:      "dev_1",
			LocalAccounts: []string{"alice"},
			DaemonVersion: "0.1.5",
		},
		Dispatcher: dispatchFunc(func(ctx context.Context, req RequestFrame) (DispatchResult, error) {
			if req.ID == "slow" {
				select {
				case <-releaseSlow:
				case <-ctx.Done():
					return DispatchResult{}, ctx.Err()
				}
			}
			return DispatchResult{
				Status:  200,
				Headers: map[string]string{"content-type": "application/json"},
				Body:    []byte(`{"ok":true}`),
			}, nil
		}),
	}
	go connector.Run(ctx, RunOptions{
		RetryInitialDelay: time.Millisecond,
		RetryMaxDelay:     time.Millisecond,
	})

	select {
	case err := <-errCh:
		t.Fatalf("relay server: %v", err)
	case header := <-fastHeaderCh:
		if header.Status != 200 {
			t.Fatalf("fast status = %d", header.Status)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("fast request was blocked behind slow request on same connection")
	}
}

func TestRunClosesConnectionForOversizedInboundFrame(t *testing.T) {
	errCh := make(chan error, 1)
	closedCh := make(chan struct{}, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			errCh <- err
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "bye")

		if _, _, err := conn.Read(r.Context()); err != nil {
			errCh <- err
			return
		}
		oversizedBody, err := json.Marshal(map[string]string{
			"payload": strings.Repeat("x", 2*1024*1024),
		})
		if err != nil {
			errCh <- err
			return
		}
		raw, err := json.Marshal(RequestFrame{
			Type:      FrameRequest,
			ID:        "too_big",
			Operation: OperationOpenAIModels,
			AccountID: "alice",
			Body:      oversizedBody,
		})
		if err != nil {
			errCh <- err
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageText, raw); err != nil {
			closedCh <- struct{}{}
			return
		}
		_, data, err := conn.Read(r.Context())
		if err == nil {
			errCh <- errUnexpectedFrame(data)
			return
		}
		closedCh <- struct{}{}
	}))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	connector := &Connector{
		Config: ConnectorConfig{
			RelayURL:      ts.URL,
			Credential:    "device-token-1",
			DeviceID:      "dev_1",
			LocalAccounts: []string{"alice"},
			DaemonVersion: "0.1.5",
		},
		Dispatcher: dispatchFunc(func(context.Context, RequestFrame) (DispatchResult, error) {
			return DispatchResult{
				Status:  200,
				Headers: map[string]string{"content-type": "application/json"},
				Body:    []byte(`{"unexpected":true}`),
			}, nil
		}),
	}
	go connector.Run(ctx, RunOptions{
		RetryInitialDelay: time.Hour,
		RetryMaxDelay:     time.Hour,
	})

	select {
	case err := <-errCh:
		t.Fatalf("relay server: %v", err)
	case <-closedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for oversized frame connection close")
	}
}

func TestRunAcceptsLargeButBoundedInboundFrame(t *testing.T) {
	errCh := make(chan error, 1)
	headerCh := make(chan ResponseHeadersFrame, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			errCh <- err
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "bye")

		if _, _, err := conn.Read(r.Context()); err != nil {
			errCh <- err
			return
		}
		boundedBody, err := json.Marshal(map[string]string{
			"payload": strings.Repeat("x", 128*1024),
		})
		if err != nil {
			errCh <- err
			return
		}
		raw, err := json.Marshal(RequestFrame{
			Type:      FrameRequest,
			ID:        "large_but_ok",
			Operation: OperationOpenAIModels,
			AccountID: "alice",
			Body:      boundedBody,
		})
		if err != nil {
			errCh <- err
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageText, raw); err != nil {
			errCh <- err
			return
		}
		_, data, err := conn.Read(r.Context())
		if err != nil {
			errCh <- err
			return
		}
		var header ResponseHeadersFrame
		if err := json.Unmarshal(data, &header); err != nil {
			errCh <- err
			return
		}
		headerCh <- header
	}))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	connector := &Connector{
		Config: ConnectorConfig{
			RelayURL:      ts.URL,
			Credential:    "device-token-1",
			DeviceID:      "dev_1",
			LocalAccounts: []string{"alice"},
			DaemonVersion: "0.1.5",
		},
		Dispatcher: dispatchFunc(func(context.Context, RequestFrame) (DispatchResult, error) {
			return DispatchResult{
				Status:  200,
				Headers: map[string]string{"content-type": "application/json"},
				Body:    []byte(`{"ok":true}`),
			}, nil
		}),
	}
	go connector.Run(ctx, RunOptions{
		RetryInitialDelay: time.Millisecond,
		RetryMaxDelay:     time.Millisecond,
	})

	select {
	case err := <-errCh:
		t.Fatalf("relay server: %v", err)
	case header := <-headerCh:
		if header.ID != "large_but_ok" || header.Status != 200 {
			t.Fatalf("headers = %#v", header)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for bounded large frame response")
	}
}

func TestRunRejectsRequestForInactiveAccount(t *testing.T) {
	errCh := make(chan error, 1)
	errorFrameCh := make(chan ErrorFrame, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			errCh <- err
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "bye")

		if _, _, err := conn.Read(r.Context()); err != nil {
			errCh <- err
			return
		}
		req := RequestFrame{
			Type:      FrameRequest,
			ID:        "req_2",
			Operation: OperationOpenAIModels,
			AccountID: "bob",
		}
		raw, err := json.Marshal(req)
		if err != nil {
			errCh <- err
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageText, raw); err != nil {
			errCh <- err
			return
		}
		_, data, err := conn.Read(r.Context())
		if err != nil {
			errCh <- err
			return
		}
		var got ErrorFrame
		if err := json.Unmarshal(data, &got); err != nil {
			errCh <- err
			return
		}
		errorFrameCh <- got
	}))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	connector := &Connector{Config: ConnectorConfig{
		RelayURL:      ts.URL,
		Credential:    "device-token-1",
		DeviceID:      "dev_1",
		LocalAccounts: []string{"alice"},
		DaemonVersion: "0.1.5",
		Capabilities:  DefaultCapabilities(),
	}}
	go connector.Run(ctx, RunOptions{
		RetryInitialDelay: time.Millisecond,
		RetryMaxDelay:     time.Millisecond,
	})

	select {
	case err := <-errCh:
		t.Fatalf("relay server: %v", err)
	case got := <-errorFrameCh:
		if got.Type != FrameError || got.ID != "req_2" || got.Code != "unknown_account" {
			t.Fatalf("error frame = %#v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for request rejection")
	}
}

type dispatchFunc func(context.Context, RequestFrame) (DispatchResult, error)

func (f dispatchFunc) Dispatch(ctx context.Context, req RequestFrame) (DispatchResult, error) {
	return f(ctx, req)
}

type errUnexpectedFrame []byte

func (e errUnexpectedFrame) Error() string {
	return "unexpected response frame for oversized request: " + string(e)
}
