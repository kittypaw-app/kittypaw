package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"nhooyr.io/websocket"

	"github.com/jinto/kittypaw/core"
)

const (
	discordGatewayURL = "wss://gateway.discord.gg/?v=10&encoding=json"
	discordAPIBase    = "https://discord.com/api/v10"
	discordMaxMessage = 1900
)

// --- Discord Gateway DTOs ---

type discordGatewayPayload struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d"`
	S  *int64          `json:"s,omitempty"`
	T  string          `json:"t,omitempty"`
}

type discordHello struct {
	HeartbeatInterval int64 `json:"heartbeat_interval"` // milliseconds
}

type discordIdentify struct {
	Token      string                    `json:"token"`
	Intents    int                       `json:"intents"`
	Properties discordIdentifyProperties `json:"properties"`
}

type discordIdentifyProperties struct {
	OS      string `json:"os"`
	Browser string `json:"browser"`
	Device  string `json:"device"`
}

type discordMessageCreate struct {
	ID        string      `json:"id"`
	ChannelID string      `json:"channel_id"`
	Content   string      `json:"content"`
	Author    discordUser `json:"author"`
}

type discordUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Bot      bool   `json:"bot"`
}

// --- DiscordChannel ---

// DiscordChannel implements Channel using the Discord Gateway WebSocket
// and REST API.
type DiscordChannel struct {
	accountID string
	botToken  string
	client    *http.Client
	channelID string
	seq       *int64 // last sequence number for heartbeats
	mu        sync.Mutex
}

// NewDiscord creates a DiscordChannel with the given bot token. accountID
// is stamped on every emitted Event for AccountRouter dispatch.
func NewDiscord(accountID, botToken string) *DiscordChannel {
	return &DiscordChannel{
		accountID: accountID,
		botToken:  botToken,
		client:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (d *DiscordChannel) Name() string { return "discord" }

func (d *DiscordChannel) MaxResponseLength() int { return discordMaxMessage }

// Start connects to the Discord Gateway and listens for MESSAGE_CREATE events.
func (d *DiscordChannel) Start(ctx context.Context, eventCh chan<- core.Event) error {
	slog.Info("discord: connecting to gateway")

	for {
		err := d.runGateway(ctx, eventCh)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slog.Warn("discord: gateway disconnected, reconnecting", "error", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

// SendResponse posts a message to the given Discord channel via REST.
// Falls back to the most recently cached channel ID if chatID is empty.
// replyToMessageID is currently unused on Discord — see Issue #N for reply-reference support.
func (d *DiscordChannel) SendResponse(ctx context.Context, chatID, response, _ string) error {
	ch := chatID
	if ch == "" {
		d.mu.Lock()
		ch = d.channelID
		d.mu.Unlock()
	}

	if ch == "" {
		return fmt.Errorf("discord: no channel to respond to")
	}

	return d.createMessage(ctx, ch, response)
}

// --- internal ---

func (d *DiscordChannel) runGateway(ctx context.Context, eventCh chan<- core.Event) error {
	conn, _, err := websocket.Dial(ctx, discordGatewayURL, nil)
	if err != nil {
		return fmt.Errorf("gateway dial: %w", err)
	}
	defer conn.CloseNow()

	conn.SetReadLimit(1 << 20)

	// Step 1: read Hello (op 10).
	hello, err := d.readPayload(ctx, conn)
	if err != nil {
		return fmt.Errorf("read hello: %w", err)
	}
	if hello.Op != 10 {
		return fmt.Errorf("expected op 10, got %d", hello.Op)
	}

	var helloData discordHello
	if err := json.Unmarshal(hello.D, &helloData); err != nil {
		return fmt.Errorf("decode hello: %w", err)
	}

	// Step 2: send Identify (op 2).
	identify := discordIdentify{
		Token:   d.botToken,
		Intents: 1<<9 | 1<<15, // GUILD_MESSAGES | MESSAGE_CONTENT
		Properties: discordIdentifyProperties{
			OS:      "linux",
			Browser: "kittypaw",
			Device:  "kittypaw",
		},
	}
	if err := d.sendPayload(ctx, conn, 2, identify); err != nil {
		return fmt.Errorf("send identify: %w", err)
	}

	slog.Info("discord: gateway connected, starting heartbeat",
		"interval_ms", helloData.HeartbeatInterval)

	// Step 3: heartbeat loop in background.
	heartbeatCtx, cancelHeartbeat := context.WithCancel(ctx)
	defer cancelHeartbeat()

	go d.heartbeatLoop(heartbeatCtx, conn,
		time.Duration(helloData.HeartbeatInterval)*time.Millisecond)

	// Step 4: read events.
	for {
		gw, err := d.readPayload(ctx, conn)
		if err != nil {
			return fmt.Errorf("read event: %w", err)
		}

		// Track sequence number for heartbeats.
		if gw.S != nil {
			d.mu.Lock()
			d.seq = gw.S
			d.mu.Unlock()
		}

		if gw.Op == 0 && gw.T == "MESSAGE_CREATE" {
			d.handleMessage(ctx, gw.D, eventCh)
		}
		// TODO: handle RESUMED, RECONNECT (op 7), INVALID_SESSION (op 9)
	}
}

func (d *DiscordChannel) handleMessage(ctx context.Context, data json.RawMessage, eventCh chan<- core.Event) {
	var msg discordMessageCreate
	if err := json.Unmarshal(data, &msg); err != nil {
		slog.Warn("discord: unmarshal message", "error", err)
		return
	}

	// Ignore bot messages.
	if msg.Author.Bot {
		return
	}
	if msg.Content == "" {
		return
	}

	d.mu.Lock()
	d.channelID = msg.ChannelID
	d.mu.Unlock()

	payload := core.ChatPayload{
		ChatID:    msg.ChannelID,
		Text:      msg.Content,
		FromName:  msg.Author.Username,
		SessionID: msg.Author.ID,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		slog.Error("discord: marshal payload", "error", err)
		return
	}

	event := core.Event{
		Type:      core.EventDiscord,
		AccountID: d.accountID,
		Payload:   raw,
	}

	select {
	case eventCh <- event:
	case <-ctx.Done():
	}
}

func (d *DiscordChannel) heartbeatLoop(ctx context.Context, conn *websocket.Conn, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.mu.Lock()
			seq := d.seq
			d.mu.Unlock()

			seqJSON, _ := json.Marshal(seq)
			payload := discordGatewayPayload{Op: 1, D: seqJSON}
			data, _ := json.Marshal(payload)

			if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
				slog.Warn("discord: heartbeat write failed", "error", err)
				return
			}
		}
	}
}

func (d *DiscordChannel) readPayload(ctx context.Context, conn *websocket.Conn) (*discordGatewayPayload, error) {
	_, data, err := conn.Read(ctx)
	if err != nil {
		return nil, err
	}
	var gw discordGatewayPayload
	if err := json.Unmarshal(data, &gw); err != nil {
		return nil, fmt.Errorf("unmarshal gateway payload: %w", err)
	}
	return &gw, nil
}

func (d *DiscordChannel) sendPayload(ctx context.Context, conn *websocket.Conn, op int, data any) error {
	d2, err := json.Marshal(data)
	if err != nil {
		return err
	}
	payload := discordGatewayPayload{Op: op, D: d2}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, raw)
}

func (d *DiscordChannel) createMessage(ctx context.Context, channelID, content string) error {
	body := map[string]string{"content": content}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/channels/%s/messages", discordAPIBase, channelID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bot "+d.botToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("discord createMessage: HTTP %d", resp.StatusCode)
	}
	return nil
}
