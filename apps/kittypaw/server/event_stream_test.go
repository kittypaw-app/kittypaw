package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jinto/kittypaw/core"
)

type sseFrame struct {
	Event string
	Data  string
}

func TestEventStreamStreamsOnlyAuthenticatedAccountEvents(t *testing.T) {
	aliceCfg := core.DefaultConfig()
	aliceCfg.Server.APIKey = "alice-key"
	bobCfg := core.DefaultConfig()
	bobCfg.Server.APIKey = "bob-key"
	srv := newMultiAccountAuthTestServer(t, "alice", map[string]string{
		"alice": "alice-pw",
		"bob":   "bob-pw",
	}, map[string]*core.Config{
		"alice": &aliceCfg,
		"bob":   &bobCfg,
	})
	httpServer := httptest.NewServer(srv.setupRoutes())
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, httpServer.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer bob-key")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}

	reader := bufio.NewReader(resp.Body)
	ready := readSSEFrame(t, reader)
	if ready.Event != EventStreamReady {
		t.Fatalf("first event = %q, want %q; data=%s", ready.Event, EventStreamReady, ready.Data)
	}

	srv.publishAccountEvent("alice", AccountEvent{
		Type:    EventStreamTurnStarted,
		Channel: string(core.EventTelegram),
		ChatID:  "alice-chat-secret",
	})
	srv.publishAccountEvent("bob", AccountEvent{
		Type:    EventStreamTurnStarted,
		Channel: string(core.EventTelegram),
		ChatID:  "bob-chat-secret",
	})

	frame := readSSEFrame(t, reader)
	if frame.Event != EventStreamTurnStarted {
		t.Fatalf("event = %q, want %q; data=%s", frame.Event, EventStreamTurnStarted, frame.Data)
	}
	if strings.Contains(frame.Data, "bob-chat-secret") || strings.Contains(frame.Data, "alice-chat-secret") {
		t.Fatalf("SSE leaked raw chat id: %s", frame.Data)
	}
	var event AccountEvent
	if err := json.Unmarshal([]byte(frame.Data), &event); err != nil {
		t.Fatalf("decode event data: %v; data=%s", err, frame.Data)
	}
	if event.AccountID != "bob" {
		t.Fatalf("account_id = %q, want bob", event.AccountID)
	}
	if event.Metadata["chat_id_hash"] == "" {
		t.Fatalf("metadata = %+v, want chat_id_hash", event.Metadata)
	}
	if event.Metadata["chat_id_hash"] == "bob-chat-secret" {
		t.Fatalf("chat_id_hash was not redacted: %+v", event.Metadata)
	}
}

func TestEventStreamIsNotBoundByRequestTimeout(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Server.APIKey = "alice-key"
	srv := newServerWithLocalUserAndConfig(t, "alice", "alice-pw", &cfg)
	httpServer := httptest.NewServer(srv.setupRoutesWithTimeout(20 * time.Millisecond))
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, httpServer.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer alice-key")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	reader := bufio.NewReader(resp.Body)
	ready := readSSEFrame(t, reader)
	if ready.Event != EventStreamReady {
		t.Fatalf("first event = %q, want %q; data=%s", ready.Event, EventStreamReady, ready.Data)
	}

	time.Sleep(60 * time.Millisecond)
	srv.publishAccountEvent("alice", AccountEvent{
		Type:    EventStreamTurnStarted,
		Channel: string(core.EventWebChat),
		ChatID:  "alice-chat-secret",
	})

	frame := readSSEFrame(t, reader)
	if frame.Event != EventStreamTurnStarted {
		t.Fatalf("event after request timeout = %q, want %q; data=%s", frame.Event, EventStreamTurnStarted, frame.Data)
	}
}

func readSSEFrame(t *testing.T, reader *bufio.Reader) sseFrame {
	t.Helper()
	var frame sseFrame
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE line: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if frame.Event != "" || frame.Data != "" {
				return frame
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "event:"):
			frame.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			if frame.Data != "" {
				frame.Data += "\n"
			}
			frame.Data += strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}
}
