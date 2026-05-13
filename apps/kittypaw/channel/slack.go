package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"nhooyr.io/websocket"

	"github.com/jinto/kittypaw/core"
)

const (
	slackAPI              = "https://slack.com/api/"
	slackMaxResponseChunk = 39000
)

// --- Slack API DTOs ---

type slackWSURL struct {
	OK  bool   `json:"ok"`
	URL string `json:"url"`
}

type slackEnvelope struct {
	Type       string          `json:"type"`
	EnvelopeID string          `json:"envelope_id,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`

	// For events_api envelopes, the event is nested.
	Event *slackEvent `json:"event,omitempty"`
}

type slackEvent struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	User    string `json:"user"`
	Channel string `json:"channel"`
}

// --- SlackChannel ---

// SlackChannel implements Channel using Slack's Web API and Socket Mode.
type SlackChannel struct {
	accountID string
	botToken  string
	appToken  string
	client    *http.Client
	channelID string
	mu        sync.Mutex
}

// NewSlack creates a SlackChannel. botToken is for Web API calls,
// appToken (xapp-...) is for Socket Mode connections. accountID is stamped
// on every emitted Event for AccountRouter dispatch.
func NewSlack(accountID, botToken, appToken string) *SlackChannel {
	return &SlackChannel{
		accountID: accountID,
		botToken:  botToken,
		appToken:  appToken,
		client:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (s *SlackChannel) Name() string { return "slack" }

func (s *SlackChannel) MaxResponseLength() int { return slackMaxResponseChunk }

// Start connects to Slack via Socket Mode and listens for message events.
func (s *SlackChannel) Start(ctx context.Context, eventCh chan<- core.Event) error {
	slog.Info("slack: connecting via socket mode")

	for {
		err := s.runSocketMode(ctx, eventCh)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slog.Warn("slack: socket mode disconnected, reconnecting", "error", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

// SendResponse posts a message to the given Slack channel.
// Falls back to the most recently cached channel ID if chatID is empty.
// replyToMessageID is currently unused on Slack — see Issue #N for thread-reply support.
func (s *SlackChannel) SendResponse(ctx context.Context, chatID, response, _ string) error {
	ch := chatID
	if ch == "" {
		s.mu.Lock()
		ch = s.channelID
		s.mu.Unlock()
	}

	if ch == "" {
		return fmt.Errorf("slack: no channel to respond to")
	}

	return s.postMessage(ctx, ch, response)
}

// --- internal ---

func (s *SlackChannel) runSocketMode(ctx context.Context, eventCh chan<- core.Event) error {
	// Step 1: get a WebSocket URL via apps.connections.open.
	wsURL, err := s.connectionsOpen(ctx)
	if err != nil {
		return fmt.Errorf("connections.open: %w", err)
	}

	// Step 2: dial the WebSocket.
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("websocket dial: %w", err)
	}
	defer conn.CloseNow()

	conn.SetReadLimit(1 << 20) // 1 MiB

	slog.Info("slack: socket mode connected")

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("websocket read: %w", err)
		}

		var env slackEnvelope
		if err := json.Unmarshal(data, &env); err != nil {
			slog.Warn("slack: unmarshal envelope", "error", err)
			continue
		}

		// Acknowledge every envelope that has an ID.
		if env.EnvelopeID != "" {
			ack, _ := json.Marshal(map[string]string{"envelope_id": env.EnvelopeID})
			_ = conn.Write(ctx, websocket.MessageText, ack)
		}

		switch env.Type {
		case "hello":
			slog.Info("slack: received hello")

		case "disconnect":
			slog.Info("slack: received disconnect, will reconnect")
			return nil

		case "events_api":
			s.handleEventPayload(ctx, env, eventCh)

		default:
			// Ignore interactive, slash_commands, etc. for now.
		}
	}
}

func (s *SlackChannel) handleEventPayload(ctx context.Context, env slackEnvelope, eventCh chan<- core.Event) {
	// The event can be at the top level or nested inside payload.
	evt := env.Event
	if evt == nil && len(env.Payload) > 0 {
		var inner struct {
			Event *slackEvent `json:"event"`
		}
		if json.Unmarshal(env.Payload, &inner) == nil {
			evt = inner.Event
		}
	}

	if evt == nil || evt.Type != "message" {
		return
	}

	// Skip bot messages (no user field).
	if evt.User == "" || evt.Text == "" {
		return
	}

	s.mu.Lock()
	s.channelID = evt.Channel
	s.mu.Unlock()

	payload := core.ChatPayload{
		ChatID:          evt.Channel,
		Text:            evt.Text,
		FromName:        evt.User,
		WorkspaceID:     "", // TODO: extract team_id if needed
		SourceSessionID: evt.User,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		slog.Error("slack: marshal payload", "error", err)
		return
	}

	event := core.Event{
		Type:      core.EventSlack,
		AccountID: s.accountID,
		Payload:   raw,
	}

	select {
	case eventCh <- event:
	case <-ctx.Done():
	}
}

func (s *SlackChannel) connectionsOpen(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		slackAPI+"apps.connections.open", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+s.appToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var result slackWSURL
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	if !result.OK {
		return "", fmt.Errorf("connections.open failed: %s", string(body))
	}
	return result.URL, nil
}

func (s *SlackChannel) postMessage(ctx context.Context, channel, text string) error {
	body := map[string]string{
		"channel": channel,
		"text":    text,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		slackAPI+"chat.postMessage", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.botToken)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode postMessage: %w", err)
	}
	if !result.OK {
		return fmt.Errorf("chat.postMessage: %s", result.Error)
	}
	return nil
}
