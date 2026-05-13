package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"nhooyr.io/websocket"

	"github.com/jinto/kittypaw/core"
)

const kakaoMaxResponseChunk = 900

// --- Kakao relay DTOs ---

// kakaoRelayMessage is a message frame from the relay WebSocket.
// Matches the JSON the Kakao relay sends: {id, text, user_id, attachments?}.
type kakaoRelayMessage struct {
	ID          string                `json:"id"`
	Text        string                `json:"text"`
	UserID      string                `json:"user_id,omitempty"`
	Attachments []core.ChatAttachment `json:"attachments,omitempty"`
}

// kakaoReplyMessage is sent back to the relay to dispatch to Kakao callback.
type kakaoReplyMessage struct {
	ID       string `json:"id"`
	Text     string `json:"text"`
	ImageURL string `json:"image_url,omitempty"`
	ImageAlt string `json:"image_alt,omitempty"`
}

// --- KakaoChannel ---

// KakaoChannel implements Channel by maintaining a WebSocket connection to a
// relay server that bridges KakaoTalk messages.
//
// Protocol:
//
//	WS — receive messages, send replies
//	Recv: {"id":"action_id","text":"utterance","user_id":"kakao_user_id","attachments":[...]}
//	Send: {"id":"action_id","text":"response_text"}
type KakaoChannel struct {
	accountID  string
	wsEndpoint string // full wss:// URL from login
	conn       *websocket.Conn
	mu         sync.Mutex
}

// TestKakaoRelay attempts a WebSocket connection to the relay and immediately
// closes it. Returns nil on success.
func TestKakaoRelay(ctx context.Context, wsURL string) error {
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("relay connection failed: %w", err)
	}
	conn.Close(websocket.StatusNormalClosure, "connection test")
	return nil
}

// NewKakao creates a KakaoChannel that connects via WebSocket to the relay.
// wsURL is the full WebSocket URL (e.g. wss://kakao.kittypaw.app/ws/{token}).
// accountID is stamped on every emitted Event for AccountRouter dispatch.
func NewKakao(accountID, wsURL string) *KakaoChannel {
	return &KakaoChannel{accountID: accountID, wsEndpoint: wsURL}
}

func (k *KakaoChannel) Name() string { return "kakao_talk" }

func (k *KakaoChannel) MaxResponseLength() int { return kakaoMaxResponseChunk }

func (k *KakaoChannel) wsURL() string { return k.wsEndpoint }

// relayHost returns the host portion of a ws(s):// URL, or "unknown" if parsing fails.
// Used to keep session tokens out of log output.
func relayHost(wsURL string) string {
	u, err := url.Parse(wsURL)
	if err != nil || u.Host == "" {
		return "unknown"
	}
	return u.Host
}

// Start connects to the relay via WebSocket and emits incoming messages as events.
// Reconnects automatically on connection loss.
func (k *KakaoChannel) Start(ctx context.Context, eventCh chan<- core.Event) error {
	// The WS endpoint embeds a session bearer token in its path; log host only.
	slog.Info("kakao: connecting to relay", "host", relayHost(k.wsEndpoint))

	backoff := time.Second
	maxBackoff := 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			slog.Info("kakao: shutting down")
			return ctx.Err()
		default:
		}

		connStart := time.Now()
		err := k.connectAndListen(ctx, eventCh)
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Reset backoff only if the connection was alive long enough to be useful.
		if time.Since(connStart) > 30*time.Second {
			backoff = time.Second
		}

		slog.Warn("kakao: connection lost, reconnecting", "error", err, "backoff", backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}

		backoff = min(backoff*2, maxBackoff)
	}
}

// connectAndListen establishes a WebSocket connection and reads messages until
// the connection drops or context is canceled.
func (k *KakaoChannel) connectAndListen(ctx context.Context, eventCh chan<- core.Event) error {
	conn, _, err := websocket.Dial(ctx, k.wsURL(), nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(1 << 20) // 1 MiB max frame

	k.mu.Lock()
	k.conn = conn
	k.mu.Unlock()

	slog.Info("kakao: connected to relay")

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			k.mu.Lock()
			k.conn = nil
			k.mu.Unlock()
			return fmt.Errorf("read: %w", err)
		}

		var msg kakaoRelayMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			slog.Warn("kakao: malformed frame", "error", err)
			continue
		}

		event, ok := kakaoRelayEvent(k.accountID, msg)
		if !ok {
			continue
		}

		select {
		case eventCh <- event:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func kakaoRelayEvent(accountID string, msg kakaoRelayMessage) (core.Event, bool) {
	if msg.Text == "" && len(msg.Attachments) == 0 {
		return core.Event{}, false
	}
	payload := core.ChatPayload{
		ChatID:      msg.ID,
		Text:        msg.Text,
		SessionID:   msg.UserID,
		Attachments: msg.Attachments,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		slog.Error("kakao: marshal payload", "error", err)
		return core.Event{}, false
	}

	return core.Event{
		Type:      core.EventKakaoTalk,
		AccountID: accountID,
		Payload:   raw,
	}, true
}

// SendResponse sends a reply frame through the WebSocket connection.
// The relay's Durable Object matches the ID to the pending Kakao callback.
// replyToMessageID is unused on Kakao (relay protocol has no reply-quote concept).
func (k *KakaoChannel) SendResponse(ctx context.Context, actionID, response, _ string) error {
	return k.sendReply(ctx, kakaoReplyMessage{
		ID:   actionID,
		Text: response,
	})
}

// SendRichResponse sends an image URL to the relay when Kakao can render it.
func (k *KakaoChannel) SendRichResponse(ctx context.Context, actionID string, response core.OutboundResponse, _ string) error {
	reply := kakaoReplyMessage{
		ID:   actionID,
		Text: response.Text,
	}
	if response.Image != nil && isPublicHTTPSImageURL(response.Image.URL) {
		reply.ImageURL = response.Image.URL
		reply.ImageAlt = response.Image.Alt
	}
	return k.sendReply(ctx, reply)
}

func (k *KakaoChannel) sendReply(ctx context.Context, reply kakaoReplyMessage) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.conn == nil {
		return fmt.Errorf("kakao: not connected to relay")
	}

	data, err := json.Marshal(reply)
	if err != nil {
		return err
	}

	if err := k.conn.Write(ctx, websocket.MessageText, data); err != nil {
		return fmt.Errorf("kakao reply: %w", err)
	}
	return nil
}

func isPublicHTTPSImageURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != ""
}
