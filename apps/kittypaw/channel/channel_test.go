package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/jinto/kittypaw/core"
)

func TestFromConfigTelegram(t *testing.T) {
	ch, err := FromConfig("test-account", core.ChannelConfig{
		ChannelType: core.ChannelTelegram,
		Token:       "123:ABC",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := ch.(*TelegramChannel); !ok {
		t.Fatalf("expected *TelegramChannel, got %T", ch)
	}
	if ch.Name() != "telegram" {
		t.Fatalf("expected name %q, got %q", "telegram", ch.Name())
	}
}

func TestFromConfigTelegramMissingToken(t *testing.T) {
	_, err := FromConfig("test-account", core.ChannelConfig{
		ChannelType: core.ChannelTelegram,
	})
	if err == nil {
		t.Fatal("expected error for missing token")
	}
}

func TestFromConfigSlack(t *testing.T) {
	ch, err := FromConfig("test-account", core.ChannelConfig{
		ChannelType: core.ChannelSlack,
		Token:       "xoxb-test",
		BindAddr:    "xapp-test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sc, ok := ch.(*SlackChannel)
	if !ok {
		t.Fatalf("expected *SlackChannel, got %T", ch)
	}
	if sc.Name() != "slack" {
		t.Fatalf("expected name %q, got %q", "slack", sc.Name())
	}
}

func TestFromConfigSlackMissingToken(t *testing.T) {
	_, err := FromConfig("test-account", core.ChannelConfig{
		ChannelType: core.ChannelSlack,
	})
	if err == nil {
		t.Fatal("expected error for missing token")
	}
}

func TestFromConfigDiscord(t *testing.T) {
	ch, err := FromConfig("test-account", core.ChannelConfig{
		ChannelType: core.ChannelDiscord,
		Token:       "discord-bot-token",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := ch.(*DiscordChannel); !ok {
		t.Fatalf("expected *DiscordChannel, got %T", ch)
	}
	if ch.Name() != "discord" {
		t.Fatalf("expected name %q, got %q", "discord", ch.Name())
	}
}

func TestFromConfigDiscordMissingToken(t *testing.T) {
	_, err := FromConfig("test-account", core.ChannelConfig{
		ChannelType: core.ChannelDiscord,
	})
	if err == nil {
		t.Fatal("expected error for missing token")
	}
}

func TestFromConfigWeb(t *testing.T) {
	ch, err := FromConfig("test-account", core.ChannelConfig{
		ChannelType: core.ChannelWeb,
		BindAddr:    "0.0.0.0:9090",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ws, ok := ch.(*WebSocketChannel)
	if !ok {
		t.Fatalf("expected *WebSocketChannel, got %T", ch)
	}
	if ws.bindAddr != "0.0.0.0:9090" {
		t.Fatalf("expected bind addr %q, got %q", "0.0.0.0:9090", ws.bindAddr)
	}
	if ws.Name() != "web" {
		t.Fatalf("expected name %q, got %q", "web", ws.Name())
	}
}

func TestFromConfigWebDefaultAddr(t *testing.T) {
	ch, err := FromConfig("test-account", core.ChannelConfig{
		ChannelType: core.ChannelWeb,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ws, ok := ch.(*WebSocketChannel)
	if !ok {
		t.Fatalf("expected *WebSocketChannel, got %T", ch)
	}
	if ws.bindAddr != "127.0.0.1:8080" {
		t.Fatalf("expected default addr %q, got %q", "127.0.0.1:8080", ws.bindAddr)
	}
}

func TestWebSocketChannelDocumentedAsDeprecated(t *testing.T) {
	src, err := os.ReadFile("websocket.go")
	if err != nil {
		t.Fatalf("read websocket channel: %v", err)
	}
	body := string(src)
	for _, token := range []string{
		"Deprecated: use the server built-in /ws or /chat/ws endpoints instead.",
		"legacy/test channel",
		"does not implement the product WebSocket auth, heartbeat, turn_id, conversation_id, or permission flow",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("websocket channel missing deprecation token %q", token)
		}
	}
}

func TestFromConfigKakao(t *testing.T) {
	ch, err := FromConfig("test-account", core.ChannelConfig{
		ChannelType: core.ChannelKakaoTalk,
		KakaoWSURL:  "wss://relay.example.com/ws/tok123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	kc, ok := ch.(*KakaoChannel)
	if !ok {
		t.Fatalf("expected *KakaoChannel, got %T", ch)
	}
	if kc.Name() != "kakao_talk" {
		t.Fatalf("expected name %q, got %q", "kakao_talk", kc.Name())
	}
}

func TestFromConfigKakaoMissingWSURL(t *testing.T) {
	_, err := FromConfig("test-account", core.ChannelConfig{
		ChannelType: core.ChannelKakaoTalk,
	})
	if err == nil {
		t.Fatal("expected error for missing KakaoWSURL")
	}
}

func TestKakaoRichResponseIncludesHTTPSImageFrame(t *testing.T) {
	msg := kakaoReplyMessage{
		ID:       "act-1",
		Text:     "fallback",
		ImageURL: "https://cdn.example.com/cat.png",
		ImageAlt: "cat",
	}
	data, _ := json.Marshal(msg)
	got := string(data)
	if !strings.Contains(got, `"image_url":"https://cdn.example.com/cat.png"`) {
		t.Fatalf("frame missing image_url: %s", got)
	}
	if !strings.Contains(got, `"image_alt":"cat"`) {
		t.Fatalf("frame missing image_alt: %s", got)
	}
}

func TestFromConfigUnsupported(t *testing.T) {
	_, err := FromConfig("test-account", core.ChannelConfig{
		ChannelType: "carrier_pigeon",
	})
	if err == nil {
		t.Fatal("expected error for unsupported channel type")
	}
}

// --- SessionID mapping tests ---

func TestKakaoSessionIDFromUserID(t *testing.T) {
	// Simulate the payload that connectAndListen would build.
	msg := kakaoRelayMessage{
		ID:     "action-123",
		Text:   "hello",
		UserID: "kakao-user-42",
	}

	payload := core.ChatPayload{
		ChatID:    msg.ID,
		Text:      msg.Text,
		SessionID: msg.UserID,
	}

	if payload.SessionID != "kakao-user-42" {
		t.Errorf("expected SessionID %q, got %q", "kakao-user-42", payload.SessionID)
	}
	if payload.ChatID != "action-123" {
		t.Errorf("expected ChatID %q, got %q", "action-123", payload.ChatID)
	}

	// Verify it roundtrips via JSON → Event → ParsePayload.
	raw, _ := json.Marshal(payload)
	event := &core.Event{Type: core.EventKakaoTalk, Payload: raw}
	parsed, err := event.ParsePayload()
	if err != nil {
		t.Fatalf("parse payload: %v", err)
	}
	if parsed.SessionID != "kakao-user-42" {
		t.Errorf("roundtrip SessionID: got %q, want %q", parsed.SessionID, "kakao-user-42")
	}
}

func TestTelegramSessionIDFromUserID(t *testing.T) {
	// Verify telegramUser.ID is included and would become SessionID.
	user := telegramUser{
		ID:        12345678,
		FirstName: "Test",
		Username:  "testuser",
	}

	data, _ := json.Marshal(user)
	var decoded telegramUser
	json.Unmarshal(data, &decoded)

	if decoded.ID != 12345678 {
		t.Errorf("expected user ID 12345678, got %d", decoded.ID)
	}
}

func TestTelegramTextUpdateFixtureBuildsEvent(t *testing.T) {
	raw, err := os.ReadFile("testdata/telegram/text_update.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var update telegramUpdate
	if err := json.Unmarshal(raw, &update); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	event, chatID, ok := telegramMessageEvent("alice", update.Message, update.Message.Text)
	if !ok {
		t.Fatal("expected fixture to produce an event")
	}
	if chatID != 987654321 {
		t.Fatalf("chatID = %d, want 987654321", chatID)
	}
	if event.Type != core.EventTelegram || event.AccountID != "alice" {
		t.Fatalf("event identity = (%s,%s), want telegram/alice", event.Type, event.AccountID)
	}
	payload, err := event.ParsePayload()
	if err != nil {
		t.Fatalf("parse payload: %v", err)
	}
	if payload.Text != "환율 알려줘" || payload.ChatID != "987654321" {
		t.Fatalf("payload text/chat = %q/%q", payload.Text, payload.ChatID)
	}
	if payload.SessionID != "12345678" {
		t.Fatalf("SessionID = %q, want Telegram user id", payload.SessionID)
	}
	if payload.FromName != "Jin" {
		t.Fatalf("FromName = %q, want Jin", payload.FromName)
	}
	if payload.ReplyToMessageID != "42" {
		t.Fatalf("ReplyToMessageID = %q, want 42", payload.ReplyToMessageID)
	}
}

func TestKakaoIncomingFixtureBuildsEvent(t *testing.T) {
	raw, err := os.ReadFile("testdata/kakao/ws_incoming.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var msg kakaoRelayMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	event, ok := kakaoRelayEvent("alice", msg)
	if !ok {
		t.Fatal("expected fixture to produce an event")
	}
	if event.Type != core.EventKakaoTalk || event.AccountID != "alice" {
		t.Fatalf("event identity = (%s,%s), want kakao_talk/alice", event.Type, event.AccountID)
	}
	payload, err := event.ParsePayload()
	if err != nil {
		t.Fatalf("parse payload: %v", err)
	}
	if payload.ChatID != "kakao-action-123" {
		t.Fatalf("ChatID = %q, want relay action id", payload.ChatID)
	}
	if payload.SessionID != "kakao-user-42" {
		t.Fatalf("SessionID = %q, want Kakao user id", payload.SessionID)
	}
	if payload.Text != "강남역에 비오나? 지금?" {
		t.Fatalf("Text = %q", payload.Text)
	}
}

func TestKakaoChannelWebSocketRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	replies := make(chan kakaoReplyMessage, 1)
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "test done")

		if err := conn.Write(ctx, websocket.MessageText, []byte(`{"id":"kakao-action-1","text":"환율 알려줘","user_id":"kakao-user-1"}`)); err != nil {
			t.Errorf("write relay frame: %v", err)
			return
		}

		_, raw, err := conn.Read(ctx)
		if err != nil {
			t.Errorf("read reply frame: %v", err)
			return
		}
		var reply kakaoReplyMessage
		if err := json.Unmarshal(raw, &reply); err != nil {
			t.Errorf("decode reply frame: %v", err)
			return
		}
		replies <- reply
	}))
	defer relay.Close()

	events := make(chan core.Event, 1)
	ch := NewKakao("alice", "ws"+strings.TrimPrefix(relay.URL, "http"))
	errCh := make(chan error, 1)
	go func() { errCh <- ch.Start(ctx, events) }()

	var event core.Event
	select {
	case event = <-events:
	case err := <-errCh:
		t.Fatalf("channel stopped before event: %v", err)
	case <-ctx.Done():
		t.Fatal("timed out waiting for relay event")
	}

	if event.Type != core.EventKakaoTalk || event.AccountID != "alice" {
		t.Fatalf("event identity = (%s,%s), want kakao_talk/alice", event.Type, event.AccountID)
	}
	payload, err := event.ParsePayload()
	if err != nil {
		t.Fatalf("parse payload: %v", err)
	}
	if payload.ChatID != "kakao-action-1" || payload.SessionID != "kakao-user-1" || payload.Text != "환율 알려줘" {
		t.Fatalf("payload = %+v", payload)
	}

	if err := ch.SendResponse(ctx, "kakao-action-1", "1 USD = 1477 KRW", "ignored"); err != nil {
		t.Fatalf("SendResponse: %v", err)
	}

	select {
	case reply := <-replies:
		if reply.ID != "kakao-action-1" || reply.Text != "1 USD = 1477 KRW" {
			t.Fatalf("reply = %+v", reply)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for relay reply")
	}
}

func TestSlackSessionIDFromUser(t *testing.T) {
	evt := slackEvent{
		Type:    "message",
		Text:    "hello",
		User:    "U123ABC",
		Channel: "C456DEF",
	}

	payload := core.ChatPayload{
		ChatID:    evt.Channel,
		Text:      evt.Text,
		FromName:  evt.User,
		SessionID: evt.User,
	}

	if payload.SessionID != "U123ABC" {
		t.Errorf("expected SessionID %q, got %q", "U123ABC", payload.SessionID)
	}
}

func TestDiscordSessionIDFromAuthor(t *testing.T) {
	msg := discordMessageCreate{
		ID:        "msg-1",
		ChannelID: "ch-1",
		Content:   "hello",
		Author:    discordUser{ID: "discord-user-99", Username: "testbot"},
	}

	payload := core.ChatPayload{
		ChatID:    msg.ChannelID,
		Text:      msg.Content,
		FromName:  msg.Author.Username,
		SessionID: msg.Author.ID,
	}

	if payload.SessionID != "discord-user-99" {
		t.Errorf("expected SessionID %q, got %q", "discord-user-99", payload.SessionID)
	}
	if payload.FromName != "testbot" {
		t.Errorf("expected FromName %q, got %q", "testbot", payload.FromName)
	}
}

// ---------------------------------------------------------------------------
// Telegram Confirmer tests
// ---------------------------------------------------------------------------

func TestTelegramConfirmerInterface(t *testing.T) {
	ch := NewTelegram("test-account", "fake-token")
	var _ Confirmer = ch // compile-time check
	if _, ok := interface{}(ch).(Confirmer); !ok {
		t.Fatal("TelegramChannel does not implement Confirmer")
	}
}

func TestResolveCallbackApprove(t *testing.T) {
	tc := NewTelegram("test-account", "fake-token")
	reqID := "test-req-123"
	ch := make(chan bool, 1)
	tc.pending.Store(reqID, ch)

	query := &telegramCallbackQuery{
		ID:   "cb-1",
		Data: "a:" + reqID,
	}

	// Use a canceled context to prevent actual HTTP calls (answerCallbackQuery)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tc.resolveCallback(ctx, query)

	select {
	case ok := <-ch:
		if !ok {
			t.Error("expected approve (true), got deny")
		}
	default:
		t.Error("expected value on channel, got nothing")
	}

	// Verify the pending entry was cleaned up
	if _, ok := tc.pending.Load(reqID); ok {
		t.Error("expected pending entry to be deleted after resolve")
	}
}

func TestResolveCallbackDeny(t *testing.T) {
	tc := NewTelegram("test-account", "fake-token")
	reqID := "test-req-456"
	ch := make(chan bool, 1)
	tc.pending.Store(reqID, ch)

	query := &telegramCallbackQuery{
		ID:   "cb-2",
		Data: "d:" + reqID,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tc.resolveCallback(ctx, query)

	select {
	case ok := <-ch:
		if ok {
			t.Error("expected deny (false), got approve")
		}
	default:
		t.Error("expected value on channel, got nothing")
	}
}

func TestResolveCallbackStale(t *testing.T) {
	tc := NewTelegram("test-account", "fake-token")

	// No pending entry for this ID — should not panic
	query := &telegramCallbackQuery{
		ID:   "cb-stale",
		Data: "a:nonexistent-req",
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tc.resolveCallback(ctx, query) // should not panic
}

func TestResolveCallbackDuplicate(t *testing.T) {
	tc := NewTelegram("test-account", "fake-token")
	reqID := "test-req-dup"
	ch := make(chan bool, 1)
	tc.pending.Store(reqID, ch)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// First click — should resolve
	tc.resolveCallback(ctx, &telegramCallbackQuery{ID: "cb-1", Data: "a:" + reqID})
	// Second click — should be a no-op (entry already deleted by LoadAndDelete)
	tc.resolveCallback(ctx, &telegramCallbackQuery{ID: "cb-2", Data: "a:" + reqID})

	select {
	case ok := <-ch:
		if !ok {
			t.Error("expected approve from first click")
		}
	default:
		t.Error("expected value from first click")
	}
}

func TestResolveCallbackRejectsDifferentRequester(t *testing.T) {
	tc := NewTelegram("test-account", "fake-token")
	reqID := "test-req-requester"
	ch := make(chan bool, 1)
	tc.pending.Store(reqID, telegramPendingConfirmation{
		decision:    ch,
		requesterID: "111",
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tc.resolveCallback(ctx, &telegramCallbackQuery{
		ID:   "cb-wrong",
		From: &telegramUser{ID: 222},
		Data: "a:" + reqID,
	})
	select {
	case got := <-ch:
		t.Fatalf("unexpected callback decision from different requester: %v", got)
	default:
	}
	if _, ok := tc.pending.Load(reqID); !ok {
		t.Fatal("pending confirmation was removed after a different requester clicked")
	}

	tc.resolveCallback(ctx, &telegramCallbackQuery{
		ID:   "cb-right",
		From: &telegramUser{ID: 111},
		Data: "a:" + reqID,
	})
	select {
	case ok := <-ch:
		if !ok {
			t.Fatal("expected approve from original requester")
		}
	default:
		t.Fatal("expected callback decision from original requester")
	}
}

func TestAskConfirmationTimeout(t *testing.T) {
	tc := NewTelegram("test-account", "fake-token")

	// Use an already-canceled context to simulate timeout.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ok, err := tc.AskConfirmation(ctx, "12345", "Shell.exec: rm -rf /", "Shell")
	if ok {
		t.Error("expected false on timeout")
	}
	if err == nil {
		t.Error("expected error on timeout")
	}
}

func TestResolveCallbackBadFormat(t *testing.T) {
	tc := NewTelegram("test-account", "fake-token")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Short data, missing colon — should not panic
	tc.resolveCallback(ctx, &telegramCallbackQuery{ID: "cb", Data: "x"})
	tc.resolveCallback(ctx, &telegramCallbackQuery{ID: "cb", Data: ""})
	tc.resolveCallback(ctx, &telegramCallbackQuery{ID: "cb", Data: "ab"})
}

func TestAskConfirmationApproveViaResolve(t *testing.T) {
	tc := NewTelegram("test-account", "fake-token")

	// We can't make real HTTP calls, but we can test the pending map flow.
	// Simulate: store a pending entry, then resolve it from another goroutine.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	reqID := "manual-test-req"
	ch := make(chan bool, 1)
	tc.pending.Store(reqID, ch)

	// Simulate callback arriving
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancelCtx, cancelFn := context.WithCancel(context.Background())
		cancelFn()
		tc.resolveCallback(cancelCtx, &telegramCallbackQuery{
			ID:   "cb-async",
			Data: "a:" + reqID,
		})
	}()

	select {
	case ok := <-ch:
		if !ok {
			t.Error("expected approve")
		}
	case <-ctx.Done():
		t.Error("timed out waiting for callback resolution")
	}
}

func TestKakaoWsURL(t *testing.T) {
	url := "wss://relay.example.com/ws/abc123"
	k := NewKakao("test-account", url)
	got := k.wsURL()
	if got != url {
		t.Fatalf("wsURL() = %q, want %q", got, url)
	}
}
