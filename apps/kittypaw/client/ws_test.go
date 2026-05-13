package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/jinto/kittypaw/core"
)

func TestStreamChat_TokensAndDone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Logf("accept: %v", err)
			return
		}
		defer conn.CloseNow()

		ctx := r.Context()

		// Send session.
		sendMsg(ctx, conn, core.NewSessionMsg("test-session"))

		// Read chat message.
		_, _, err = conn.Read(ctx)
		if err != nil {
			return
		}

		// Send done — Phase 13.3 removed token streaming, server
		// emits a single Done frame per turn.
		sendMsg(ctx, conn, core.NewDoneMsg("Hello World", nil))
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

	var doneText string

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := StreamChat(ctx, wsURL, "", "hi", ChatOptions{
		OnDone: func(full string, _ *int64) { doneText = full },
	})
	if err != nil {
		t.Fatalf("StreamChat error: %v", err)
	}

	if doneText != "Hello World" {
		t.Errorf("doneText = %q, want %q", doneText, "Hello World")
	}
}

func TestStreamChat_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx := r.Context()

		sendMsg(ctx, conn, core.NewSessionMsg("s"))
		_, _, _ = conn.Read(ctx)
		sendMsg(ctx, conn, core.NewErrorMsg("something broke"))
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

	var errorMsg string
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := StreamChat(ctx, wsURL, "", "hi", ChatOptions{
		OnError: func(msg string) { errorMsg = msg },
	})
	if err == nil {
		t.Fatal("StreamChat expected error")
	}
	if errorMsg != "something broke" {
		t.Errorf("errorMsg = %q, want %q", errorMsg, "something broke")
	}
}

func TestStreamChat_WithAPIKey(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx := r.Context()

		sendMsg(ctx, conn, core.NewSessionMsg("s"))
		_, _, _ = conn.Read(ctx)
		sendMsg(ctx, conn, core.NewDoneMsg("ok", nil))
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := StreamChat(ctx, wsURL, "my-key", "hi", ChatOptions{})
	if err != nil {
		t.Fatalf("StreamChat error: %v", err)
	}
	if gotAuth != "Bearer my-key" {
		t.Errorf("auth = %q, want %q", gotAuth, "Bearer my-key")
	}
}

func TestDialChatUnauthorizedHasRestartGuidance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := DialChat(ctx, wsURL, "stale-key")
	if err == nil {
		t.Fatal("DialChat succeeded, want unauthorized error")
	}
	for _, want := range []string{"401", "kittypaw server stop", "kittypaw chat"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, missing %q", err.Error(), want)
		}
	}
}

func TestSendTurnIncludesConversationID(t *testing.T) {
	msgCh := make(chan core.WsClientMsg, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx := r.Context()

		sendMsg(ctx, conn, core.NewSessionMsg("s"))
		_, raw, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var msg core.WsClientMsg
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Errorf("unmarshal client msg: %v", err)
			return
		}
		msgCh <- msg
		sendMsg(ctx, conn, core.NewDoneMsgForTurn("turn-1", "ok", nil))
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cs, err := DialChat(ctx, wsURL, "")
	if err != nil {
		t.Fatalf("DialChat: %v", err)
	}
	defer cs.Close()
	err = cs.SendTurn("hi", "turn-1", ChatOptions{ConversationID: "project:alpha"})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	got := <-msgCh
	if got.ConversationID != "project:alpha" {
		t.Fatalf("ConversationID = %q, want project:alpha", got.ConversationID)
	}
	if got.TurnID != "turn-1" {
		t.Fatalf("TurnID = %q, want turn-1", got.TurnID)
	}
}

func sendMsg(ctx context.Context, conn *websocket.Conn, msg core.WsServerMsg) {
	data, _ := json.Marshal(msg)
	conn.Write(ctx, websocket.MessageText, data)
}
