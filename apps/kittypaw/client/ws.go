package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"nhooyr.io/websocket"

	"github.com/jinto/kittypaw/core"
)

// ErrServerSide tags errors that originated from a server-emitted
// WsMsgError frame, as opposed to a transport-level disconnect. The
// CLI's silent-reconnect path uses errors.Is to reject these —
// replaying the same prompt would not heal an application-layer
// failure and could double-charge the user.
var ErrServerSide = errors.New("server error")

// ChatOptions configures callbacks for chat responses. The server
// emits a single Done frame per turn — token-level streaming was
// removed in Phase 13.3 (no consumer was using it).
type ChatOptions struct {
	ConversationID string
	OnDone         func(fullText string, tokensUsed *int64)
	OnError        func(message string)
}

// ChatSession wraps a persistent WebSocket connection for multi-turn chat.
// Use DialChat to create, Send to exchange messages, Close when done.
type ChatSession struct {
	conn *websocket.Conn
	ctx  context.Context
}

// DialChat opens a WebSocket connection and returns a ChatSession.
// The connection stays open for multiple Send calls until Close.
func DialChat(ctx context.Context, wsURL, apiKey string) (*ChatSession, error) {
	headers := http.Header{}
	if apiKey != "" {
		headers.Set("Authorization", "Bearer "+apiKey)
	}

	conn, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: headers,
	})
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("websocket unauthorized (401): local server rejected the CLI token; run `kittypaw server stop && kittypaw chat` to restart it with the current account secrets")
		}
		return nil, fmt.Errorf("websocket dial: %w", err)
	}
	conn.SetReadLimit(64 * 1024)

	// Read session message (ignore).
	if _, _, err := conn.Read(ctx); err != nil {
		conn.CloseNow()
		return nil, fmt.Errorf("read session msg: %w", err)
	}

	return &ChatSession{conn: conn, ctx: ctx}, nil
}

// Send sends a chat message with an auto-generated turn_id. Suitable
// for one-shot calls that won't retry. Retry-aware callers should
// allocate a turn_id once per user input and call SendTurn so the
// server-side idempotency cache (Session.RunTurn) deduplicates the
// retry.
func (cs *ChatSession) Send(text string, opts ChatOptions) error {
	return cs.SendTurn(text, uuid.NewString(), opts)
}

// SendTurn sends a chat message tagged with the supplied turn_id.
// Retries that share a turn_id are deduped server-side: only the
// first reaches the LLM, subsequent retries wait on its result. Empty
// turnID is allowed for callers who explicitly opt out of idempotency.
func (cs *ChatSession) SendTurn(text string, turnID string, opts ChatOptions) error {
	chatMsg := core.WsClientMsg{
		Type:           core.WsMsgChat,
		Text:           text,
		TurnID:         turnID,
		ConversationID: opts.ConversationID,
	}
	data, err := json.Marshal(chatMsg)
	if err != nil {
		return fmt.Errorf("marshal chat msg: %w", err)
	}
	if err := cs.conn.Write(cs.ctx, websocket.MessageText, data); err != nil {
		return fmt.Errorf("write chat msg: %w", err)
	}

	// Read response frames with per-read timeout.
	for {
		readCtx, readCancel := context.WithTimeout(cs.ctx, 5*time.Minute)
		_, msgBytes, err := cs.conn.Read(readCtx)
		readCancel()
		if err != nil {
			return fmt.Errorf("read ws msg: %w", err)
		}

		var msg core.WsServerMsg
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case core.WsMsgDone:
			if opts.OnDone != nil {
				opts.OnDone(msg.FullText, msg.TokensUsed)
			}
			return nil
		case core.WsMsgError:
			errMsg := msg.Message
			if opts.OnError != nil {
				opts.OnError(errMsg)
			}
			return fmt.Errorf("%w: %s", ErrServerSide, errMsg)
		case core.WsMsgPermission:
			deny := false
			permitMsg := core.WsClientMsg{Type: core.WsMsgPermit, OK: &deny}
			d, _ := json.Marshal(permitMsg)
			if err := cs.conn.Write(cs.ctx, websocket.MessageText, d); err != nil {
				return fmt.Errorf("write permit deny: %w", err)
			}
		}
	}
}

// Close cleanly closes the WebSocket connection.
func (cs *ChatSession) Close() {
	cs.conn.Close(websocket.StatusNormalClosure, "bye")
}

// StreamChat opens a single-turn WebSocket session: dials, sends one message,
// reads the response, and closes. For multi-turn chat, use DialChat + Send.
func StreamChat(ctx context.Context, wsURL, apiKey, text string, opts ChatOptions) error {
	cs, err := DialChat(ctx, wsURL, apiKey)
	if err != nil {
		return err
	}
	defer cs.Close()
	return cs.Send(text, opts)
}
