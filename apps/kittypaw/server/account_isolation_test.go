package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/engine"
)

// emittingStub is a test Channel that emits a single tagged Event on
// request and then blocks until ctx is canceled. It mirrors what a real
// channel does (Telegram, Slack, …) but without the network I/O, so we
// can verify the full event→router→session dispatch path.
type emittingStub struct {
	name      string
	accountID string
	fire      chan core.Event
	responses chan sentResponse
}

func newEmittingStub(name, accountID string) *emittingStub {
	return &emittingStub{
		name:      name,
		accountID: accountID,
		fire:      make(chan core.Event, 1),
		responses: make(chan sentResponse, 4),
	}
}

type sentResponse struct {
	chatID           string
	response         string
	replyToMessageID string
}

func (e *emittingStub) Start(ctx context.Context, eventCh chan<- core.Event) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev := <-e.fire:
			select {
			case eventCh <- ev:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func (e *emittingStub) SendResponse(ctx context.Context, chatID, response, replyToMessageID string) error {
	select {
	case e.responses <- sentResponse{chatID: chatID, response: response, replyToMessageID: replyToMessageID}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (e *emittingStub) Name() string { return e.name }

// emit tells the stub to produce an Event tagged with the stub's accountID.
func (e *emittingStub) emit(text string) {
	payload, _ := json.Marshal(core.ChatPayload{ChatID: "c1", Text: text})
	e.fire <- core.Event{
		Type:      core.EventType(e.name),
		AccountID: e.accountID,
		Payload:   payload,
	}
}

// TestAccountIsolation_EndToEnd enforces AC-T3: a message that enters via
// alice's channel lands on alice's session and never on bob's. A regression
// here would be a cross-account leak — the primary privacy risk the
// AccountRouter is designed to prevent.
func TestAccountIsolation_EndToEnd(t *testing.T) {
	aliceSess := &engine.AccountRuntime{BaseDir: "/tmp/alice"}
	bobSess := &engine.AccountRuntime{BaseDir: "/tmp/bob"}

	router := NewAccountRouter()
	router.Register("alice", aliceSess)
	router.Register("bob", bobSess)

	// Alice's event hits alice only.
	alicePayload, _ := json.Marshal(core.ChatPayload{Text: "alice msg"})
	got := router.Route(core.Event{
		Type:      core.EventTelegram,
		AccountID: "alice",
		Payload:   alicePayload,
	})
	if got != aliceSess {
		t.Errorf("alice event routed to %p, want aliceSess %p", got, aliceSess)
	}

	// Bob's event hits bob only.
	got = router.Route(core.Event{
		Type:      core.EventTelegram,
		AccountID: "bob",
	})
	if got != bobSess {
		t.Errorf("bob event routed to %p, want bobSess %p", got, bobSess)
	}

	// Unknown account drops — no fallback to alice even though alice was
	// registered first.
	if got := router.Route(core.Event{AccountID: "charlie"}); got != nil {
		t.Error("unknown account must drop (C1 no-fallback)")
	}
}

// TestAccountIsolation_ChannelSpawner_SameTypeTwoAccounts enforces AC-T3
// from the spawner angle: two accounts can have telegram bots whose tokens
// differ, and each routes back to its owner's channel for SendResponse.
// Without composite-key isolation, bob's TrySpawn would silently skip
// because "telegram" is already registered under alice.
func TestAccountIsolation_ChannelSpawner_SameTypeTwoAccounts(t *testing.T) {
	eventCh := make(chan core.Event, 8)
	sp := NewChannelSpawner(context.Background(), eventCh)

	alice := newEmittingStub("telegram", "alice")
	bob := newEmittingStub("telegram", "bob")

	if err := sp.TrySpawn("alice", alice, core.ChannelConfig{
		ChannelType: core.ChannelTelegram, Token: "alice-tok",
	}); err != nil {
		t.Fatalf("alice TrySpawn: %v", err)
	}
	if err := sp.TrySpawn("bob", bob, core.ChannelConfig{
		ChannelType: core.ChannelTelegram, Token: "bob-tok",
	}); err != nil {
		t.Fatalf("bob TrySpawn: %v", err)
	}

	if ch, ok := sp.GetChannel("alice", core.EventTelegram); !ok || ch != alice {
		t.Errorf("alice GetChannel mismatch: got %v", ch)
	}
	if ch, ok := sp.GetChannel("bob", core.EventTelegram); !ok || ch != bob {
		t.Errorf("bob GetChannel mismatch: got %v", ch)
	}

	// Verify events emitted by each channel carry the right AccountID.
	alice.emit("from alice")
	bob.emit("from bob")

	got := map[string]string{}
	timeout := time.After(2 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case ev := <-eventCh:
			got[ev.AccountID] = string(ev.Payload)
		case <-timeout:
			t.Fatalf("timed out after %d events", i)
		}
	}
	if _, ok := got["alice"]; !ok {
		t.Error("alice's event never arrived on eventCh")
	}
	if _, ok := got["bob"]; !ok {
		t.Error("bob's event never arrived on eventCh")
	}

	sp.StopAll()
}

// TestDispatchLoop_ChatIDMismatch_Drops enforces AC-T7: even after a
// successful AccountID→runtime route, the payload's chat_id must belong to
// that account's AllowedChatIDs. A mismatch is the exact bot-token-leak
// scenario — alice's bot token gets stolen, the attacker crafts an update
// carrying bob's chat_id to write bob's conversation into alice's store.
// The event must be dropped before AccountRuntime.Run and the mismatch counter
// must bump so ops can alert on `account_routing_mismatch_total{from=alice}`.
func TestDispatchLoop_ChatIDMismatch_Drops(t *testing.T) {
	root := t.TempDir()
	aliceDeps := buildAccountDeps(t, root, "alice", &core.Config{
		AllowedChatIDs: []string{"alice-chat-1"},
	})
	bobDeps := buildAccountDeps(t, root, "bob", &core.Config{
		AllowedChatIDs: []string{"bob-chat-1"},
	})
	srv := New([]*AccountDeps{aliceDeps, bobDeps}, "test")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.spawner = NewChannelSpawner(ctx, srv.eventCh)
	defer srv.spawner.StopAll()

	go srv.dispatchLoop(ctx)

	// alice's bot receives an event whose chat_id belongs to bob — the
	// attack shape AC-T7 exists to block. AccountID is alice (matching a
	// registered session) but the payload carries bob's chat_id.
	payload, err := json.Marshal(core.ChatPayload{
		ChatID: "bob-chat-1",
		Text:   "cross-routing attack",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	srv.eventCh <- core.Event{
		Type:      core.EventTelegram,
		AccountID: "alice",
		Payload:   payload,
	}

	// Poll until the mismatch is recorded or the deadline fires. Polling
	// avoids a sleep-based flake on slow CI while still bounded to 2s.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if srv.accounts.MismatchCount("alice") >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if got := srv.accounts.MismatchCount("alice"); got != 1 {
		t.Errorf("MismatchCount(alice) = %d, want 1 (AC-T7 mismatch must be counted)", got)
	}
	if got := srv.accounts.MismatchCount("bob"); got != 0 {
		t.Errorf("MismatchCount(bob) = %d, want 0 (bob was the impersonated chat_id owner, not the event source)", got)
	}
	// DropCount tracks empty/unknown AccountID — mismatches are a separate
	// class of drop so ops can tell "wrong account id" from "stolen token".
	if got := srv.accounts.DropCount(); got != 0 {
		t.Errorf("DropCount = %d, want 0 (Route() succeeded; mismatch is a post-route drop)", got)
	}
}

// TestDispatchLoop_ChatIDMatch_NoMismatch is the negative control: when
// alice's bot emits alice's own chat_id the mismatch counter must stay at
// zero. Without this counterpart test, a buggy ChatBelongsToAccount that
// always returns false would look "safe" in the mismatch test above but
// would drop every legitimate message silently.
func TestDispatchLoop_ChatIDMatch_NoMismatch(t *testing.T) {
	root := t.TempDir()
	aliceDeps := buildAccountDeps(t, root, "alice", &core.Config{
		AllowedChatIDs: []string{"alice-chat-1"},
	})
	srv := New([]*AccountDeps{aliceDeps}, "test")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.spawner = NewChannelSpawner(ctx, srv.eventCh)
	defer srv.spawner.StopAll()

	payload, err := json.Marshal(core.ChatPayload{
		ChatID: "alice-chat-1",
		Text:   "legit",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	// Route + ownership check in isolation — don't run dispatchLoop because
	// a nil Provider would explode on AccountRuntime.Run. The ownership gate lives
	// in dispatchLoop but is testable directly on the router + helper.
	sess := srv.accounts.Route(core.Event{
		Type:      core.EventTelegram,
		AccountID: "alice",
		Payload:   payload,
	})
	if sess == nil {
		t.Fatal("alice session should be routable")
	}
	if !core.ChatBelongsToAccount(sess.Config, "alice-chat-1") {
		t.Error("alice's own chat_id must pass the ownership check")
	}
	if got := srv.accounts.MismatchCount("alice"); got != 0 {
		t.Errorf("MismatchCount(alice) = %d, want 0 on legitimate traffic", got)
	}
}

// TestDispatchLoop_KakaoActionIDSkipsAdminChatOwnershipCheck is the Kakao
// counterpart to AC-T7. Kakao ChatID is the relay callback action id used for
// SendResponse, not a stable owner chat id. The account boundary is the
// per-account relay token that stamped Event.AccountID, so Telegram-style
// AllowedChatIDs matching must not drop a legitimate Kakao action id.
func TestDispatchLoop_KakaoActionIDSkipsAdminChatOwnershipCheck(t *testing.T) {
	root := t.TempDir()
	deps := buildAccountDeps(t, root, "jinto", &core.Config{
		AllowedChatIDs: []string{"telegram-chat-id"},
	})
	provider := &chatRelayMockProvider{content: "kakao reply"}
	deps.Provider = provider

	srv := New([]*AccountDeps{deps}, "test")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.spawner = NewChannelSpawner(ctx, srv.eventCh)
	defer srv.spawner.StopAll()

	kakao := newEmittingStub(string(core.EventKakaoTalk), "jinto")
	if err := srv.spawner.TrySpawn("jinto", kakao, core.ChannelConfig{
		ChannelType: core.ChannelKakaoTalk,
	}); err != nil {
		t.Fatalf("spawn kakao: %v", err)
	}

	go srv.dispatchLoop(ctx)

	payload, err := json.Marshal(core.ChatPayload{
		ChatID:          "kakao-action-id",
		Text:            "hello from kakao",
		SourceSessionID: "kakao-user-id",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	srv.eventCh <- core.Event{
		Type:      core.EventKakaoTalk,
		AccountID: "jinto",
		Payload:   payload,
	}

	select {
	case got := <-kakao.responses:
		if got.chatID != "kakao-action-id" {
			t.Fatalf("response chatID = %q, want relay action id", got.chatID)
		}
		if got.response != "kakao reply" {
			t.Fatalf("response = %q", got.response)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Kakao response; event was likely dropped")
	}

	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
	if got := srv.accounts.MismatchCount("jinto"); got != 0 {
		t.Fatalf("MismatchCount(jinto) = %d, want 0 for Kakao action id", got)
	}
}

// TestAccountIsolation_DuplicateTokenRejected enforces C3: two accounts
// declaring the same Telegram bot token must be flagged at config
// validation, not after both bots are started and racing on getUpdates.
func TestAccountIsolation_DuplicateTokenRejected(t *testing.T) {
	accountChannels := map[string][]core.ChannelConfig{
		"alice": {{ChannelType: core.ChannelTelegram, Token: "shared"}},
		"bob":   {{ChannelType: core.ChannelTelegram, Token: "shared"}},
	}
	if err := core.ValidateAccountChannels(accountChannels); err == nil {
		t.Error("duplicate bot_token across accounts should have been rejected")
	}
}
