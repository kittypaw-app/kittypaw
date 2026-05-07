package server

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jinto/kittypaw/channel"
	"github.com/jinto/kittypaw/core"
)

// pushCall captures one SendResponse invocation on mockPushChannel.
type pushCall struct {
	ChatID   string
	Response string
}

// mockPushChannel is a channel.Channel stub used to observe dispatchLoop
// delivering EventTeamSpacePush without relying on a live Telegram/Slack/etc.
// backend. Start blocks on ctx so ChannelSpawner's lifecycle is satisfied.
type mockPushChannel struct {
	name string
	mu   sync.Mutex
	sent []pushCall
}

func (m *mockPushChannel) Start(ctx context.Context, _ chan<- core.Event) error {
	<-ctx.Done()
	return nil
}

func (m *mockPushChannel) SendResponse(_ context.Context, chatID, response, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, pushCall{ChatID: chatID, Response: response})
	return nil
}

func (m *mockPushChannel) Name() string { return m.name }

func (m *mockPushChannel) calls() []pushCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]pushCall, len(m.sent))
	copy(out, m.sent)
	return out
}

// Compile-time interface check — if channel.Channel gains a required method
// we want the test stub to fail to compile rather than pass vacuously.
var _ channel.Channel = (*mockPushChannel)(nil)

// waitForCalls polls until the mock has >= n recorded calls or the deadline
// fires. Returns the captured calls. Used instead of time.Sleep because the
// dispatch goroutine is asynchronous and test flakes on slow CI.
func waitForCalls(t *testing.T, m *mockPushChannel, n int, d time.Duration) []pushCall {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if c := m.calls(); len(c) >= n {
			return c
		}
		time.Sleep(5 * time.Millisecond)
	}
	return m.calls()
}

// buildFamilyPushServer wires a Server + spawner with a team-space coordinator and a
// personal account whose Config declares the supplied channels. Returns the
// server, a shutdown func, and the personal account's registered mock channels
// keyed by EventType for assertion access.
func buildFamilyPushServer(t *testing.T, personalCfg *core.Config, mocks map[core.EventType]*mockPushChannel) (*Server, context.CancelFunc) {
	t.Helper()
	root := t.TempDir()

	familyDeps := buildAccountDeps(t, root, "family", &core.Config{
		IsShared:  true,
		TeamSpace: core.TeamSpaceConfig{Members: []string{"alice"}},
	})
	aliceDeps := buildAccountDeps(t, root, "alice", personalCfg)

	srv := New([]*AccountDeps{familyDeps, aliceDeps}, "test")

	ctx, cancel := context.WithCancel(context.Background())
	srv.spawner = NewChannelSpawner(ctx, srv.eventCh)

	// Register each mock under alice's account ID keyed by its Name() (which
	// must match the resolved EventType string). TrySpawn's running map key
	// is `spawnerKey{AccountID: "alice", ChannelType: ch.Name()}`.
	for evType, m := range mocks {
		if m.name == "" {
			m.name = string(evType)
		}
		if err := srv.spawner.TrySpawn("alice", m, core.ChannelConfig{ChannelType: core.ChannelType(evType)}); err != nil {
			cancel()
			t.Fatalf("TrySpawn %s: %v", evType, err)
		}
	}

	go srv.dispatchLoop(ctx)

	return srv, func() {
		cancel()
		srv.spawner.StopAll()
	}
}

// TestFamilyMorningBrief_FansOutToAllPersonalAccounts enforces AC-U1: the
// team-space coordinator's skills (here simulated by direct Fanout.Send calls, one
// per target) deliver to each personal account's own channel with that
// account's own chat_id. A regression here is the defining team-space
// failure mode — either the wrong target gets the wrong message, or the
// chat_id falls back to the coordinator's own (non-existent) AllowedChatIDs.
//
// The scheduled-skill trigger is exercised elsewhere in engine/schedule;
// this test narrows in on the Fanout → dispatchLoop → channel.SendResponse
// leg, which is the delivery surface the AC actually describes.
func TestFamilyMorningBrief_FansOutToAllPersonalAccounts(t *testing.T) {
	root := t.TempDir()

	// Family drives fanout; three personal accounts receive their own tailored text.
	familyDeps := buildAccountDeps(t, root, "family", &core.Config{
		IsShared:  true,
		TeamSpace: core.TeamSpaceConfig{Members: []string{"alice", "bob", "charlie"}},
	})
	aliceDeps := buildAccountDeps(t, root, "alice", &core.Config{
		Channels:       []core.ChannelConfig{{ChannelType: core.ChannelTelegram}},
		AllowedChatIDs: []string{"alice-chat"},
	})
	bobDeps := buildAccountDeps(t, root, "bob", &core.Config{
		Channels:       []core.ChannelConfig{{ChannelType: core.ChannelTelegram}},
		AllowedChatIDs: []string{"bob-chat"},
	})
	charlieDeps := buildAccountDeps(t, root, "charlie", &core.Config{
		Channels:       []core.ChannelConfig{{ChannelType: core.ChannelTelegram}},
		AllowedChatIDs: []string{"charlie-chat"},
	})
	srv := New([]*AccountDeps{familyDeps, aliceDeps, bobDeps, charlieDeps}, "test")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.spawner = NewChannelSpawner(ctx, srv.eventCh)
	defer srv.spawner.StopAll()

	// Wire a mockPushChannel per personal account so SendResponse calls are
	// captured per destination. Each mock's Name() must match the telegram
	// EventType string to match ChannelSpawner's lookup key.
	mocks := map[string]*mockPushChannel{
		"alice":   {name: string(core.EventTelegram)},
		"bob":     {name: string(core.EventTelegram)},
		"charlie": {name: string(core.EventTelegram)},
	}
	for id, m := range mocks {
		if err := srv.spawner.TrySpawn(id, m, core.ChannelConfig{
			ChannelType: core.ChannelTelegram,
		}); err != nil {
			t.Fatalf("TrySpawn %s: %v", id, err)
		}
	}

	go srv.dispatchLoop(ctx)

	familySess := srv.accounts.Session("family")
	if familySess == nil {
		t.Fatal("team-space coordinator session not registered")
	}
	if familySess.Fanout == nil {
		t.Fatal("team-space coordinator session's Fanout is nil (IsFamily wiring regression)")
	}

	// Simulated morning-brief skill output — three account-specific texts.
	// In production these would be LLM-generated; here the test controls
	// the exact strings so assertion failures point to delivery bugs, not
	// prompt changes.
	pushes := map[string]string{
		"alice":   "🍚 알리스 오늘 숙제: 수학 드릴",
		"bob":     "🏀 봅 오늘 농구 연습 5시",
		"charlie": "📚 찰리 오늘 독서 30분",
	}
	for target, text := range pushes {
		if err := familySess.Fanout.Send(ctx, target, core.FanoutPayload{Text: text}); err != nil {
			t.Fatalf("Fanout.Send(%s): %v", target, err)
		}
	}

	for id, m := range mocks {
		calls := waitForCalls(t, m, 1, 2*time.Second)
		if len(calls) != 1 {
			t.Fatalf("%s: expected 1 SendResponse, got %d", id, len(calls))
		}
		wantChat := id + "-chat"
		if calls[0].ChatID != wantChat {
			t.Errorf("%s: chat_id = %q, want %q", id, calls[0].ChatID, wantChat)
		}
		if calls[0].Response != pushes[id] {
			t.Errorf("%s: response = %q, want %q", id, calls[0].Response, pushes[id])
		}
	}

	// Negative: coordinator itself must not receive any push (self-loop guard).
	// No coordinator mock registered, so spawner lookup would fail; but we can
	// also confirm no cross-pollution by inspecting each personal mock for
	// exactly-one call, which the loop above already does.
}

// TestFamilyMorningBrief_BroadcastFansOutToAllPeers is the Broadcast
// variant of AC-U1: one call, N-1 targets (the source account is excluded).
// Locks in that Broadcast's "except source" guard works in the multi-
// personal case — without it, the coordinator would push to itself and the event
// would bounce through dispatchLoop with no destination channel.
func TestFamilyMorningBrief_BroadcastFansOutToAllPeers(t *testing.T) {
	root := t.TempDir()

	familyDeps := buildAccountDeps(t, root, "family", &core.Config{
		IsShared:  true,
		TeamSpace: core.TeamSpaceConfig{Members: []string{"alice", "bob"}},
	})
	aliceDeps := buildAccountDeps(t, root, "alice", &core.Config{
		Channels:       []core.ChannelConfig{{ChannelType: core.ChannelTelegram}},
		AllowedChatIDs: []string{"alice-chat"},
	})
	bobDeps := buildAccountDeps(t, root, "bob", &core.Config{
		Channels:       []core.ChannelConfig{{ChannelType: core.ChannelTelegram}},
		AllowedChatIDs: []string{"bob-chat"},
	})
	srv := New([]*AccountDeps{familyDeps, aliceDeps, bobDeps}, "test")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.spawner = NewChannelSpawner(ctx, srv.eventCh)
	defer srv.spawner.StopAll()

	mocks := map[string]*mockPushChannel{
		"alice": {name: string(core.EventTelegram)},
		"bob":   {name: string(core.EventTelegram)},
	}
	for id, m := range mocks {
		if err := srv.spawner.TrySpawn(id, m, core.ChannelConfig{
			ChannelType: core.ChannelTelegram,
		}); err != nil {
			t.Fatalf("TrySpawn %s: %v", id, err)
		}
	}

	go srv.dispatchLoop(ctx)

	familySess := srv.accounts.Session("family")
	if familySess == nil || familySess.Fanout == nil {
		t.Fatal("team-space coordinator session / Fanout missing")
	}

	shared := "🌤 오늘 날씨 맑음 — 외출 추천"
	if err := familySess.Fanout.Broadcast(ctx, core.FanoutPayload{Text: shared}); err != nil {
		t.Fatalf("Broadcast: %v", err)
	}

	for id, m := range mocks {
		calls := waitForCalls(t, m, 1, 2*time.Second)
		if len(calls) != 1 {
			t.Fatalf("%s: expected 1 SendResponse, got %d", id, len(calls))
		}
		if calls[0].Response != shared {
			t.Errorf("%s: response = %q, want %q", id, calls[0].Response, shared)
		}
		if calls[0].ChatID != id+"-chat" {
			t.Errorf("%s: chat_id = %q, want %s-chat", id, calls[0].ChatID, id)
		}
	}
}

func TestRemoveAccountScrubsLiveTeamSpaceMembership(t *testing.T) {
	root := t.TempDir()

	teamDeps := buildAccountDeps(t, root, "team", &core.Config{
		IsShared:  true,
		TeamSpace: core.TeamSpaceConfig{Members: []string{"alice", "bob"}},
	})
	aliceDeps := buildAccountDeps(t, root, "alice", &core.Config{})
	bobDeps := buildAccountDeps(t, root, "bob", &core.Config{
		Channels:       []core.ChannelConfig{{ChannelType: core.ChannelTelegram}},
		AllowedChatIDs: []string{"bob-chat"},
	})
	srv := New([]*AccountDeps{teamDeps, aliceDeps, bobDeps}, "test")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.spawner = NewChannelSpawner(ctx, srv.eventCh)
	defer srv.spawner.StopAll()

	bobMock := &mockPushChannel{name: string(core.EventTelegram)}
	if err := srv.spawner.TrySpawn("bob", bobMock, core.ChannelConfig{
		ChannelType: core.ChannelTelegram,
	}); err != nil {
		t.Fatalf("TrySpawn bob: %v", err)
	}

	go srv.dispatchLoop(ctx)

	if err := srv.RemoveAccount("alice"); err != nil {
		t.Fatalf("RemoveAccount alice: %v", err)
	}

	teamSess := srv.accounts.Session("team")
	if teamSess == nil || teamSess.Fanout == nil {
		t.Fatal("team-space session / Fanout missing")
	}
	if teamSess.Config.TeamSpaceHasMember("alice") {
		t.Fatal("removed account still present in live team-space membership")
	}

	shared := "alice removed; bob should still receive"
	if err := teamSess.Fanout.Broadcast(ctx, core.FanoutPayload{Text: shared}); err != nil {
		t.Fatalf("Broadcast after RemoveAccount: %v", err)
	}

	calls := waitForCalls(t, bobMock, 1, 2*time.Second)
	if len(calls) != 1 {
		t.Fatalf("bob expected 1 SendResponse, got %d", len(calls))
	}
	if calls[0].ChatID != "bob-chat" {
		t.Errorf("bob chat_id = %q, want bob-chat", calls[0].ChatID)
	}
	if calls[0].Response != shared {
		t.Errorf("bob response = %q, want %q", calls[0].Response, shared)
	}
}

func TestTeamSpaceFanoutRejectsNonMember(t *testing.T) {
	root := t.TempDir()
	teamDeps := buildAccountDeps(t, root, "team", &core.Config{
		IsShared:  true,
		TeamSpace: core.TeamSpaceConfig{Members: []string{"alice"}},
	})
	aliceDeps := buildAccountDeps(t, root, "alice", &core.Config{})
	bobDeps := buildAccountDeps(t, root, "bob", &core.Config{})
	srv := New([]*AccountDeps{teamDeps, aliceDeps, bobDeps}, "test")

	teamSess := srv.accounts.Session("team")
	if teamSess == nil || teamSess.Fanout == nil {
		t.Fatal("team-space session / Fanout missing")
	}
	err := teamSess.Fanout.Send(context.Background(), "bob", core.FanoutPayload{Text: "x"})
	if !errors.Is(err, core.ErrFanoutUnauthorizedTarget) {
		t.Fatalf("Send to non-member err = %v, want ErrFanoutUnauthorizedTarget", err)
	}
}

func pushEvent(t *testing.T, target string, p core.FanoutPayload) core.Event {
	t.Helper()
	body, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return core.Event{Type: core.EventTeamSpacePush, AccountID: target, Payload: body}
}

func legacyFamilyPushEvent(t *testing.T, target string, p core.FanoutPayload) core.Event {
	t.Helper()
	ev := pushEvent(t, target, p)
	ev.Type = core.EventType("family.push")
	return ev
}

// TestDispatchLoop_FamilyPush_DeliversToTargetChannel is the happy path — a
// team-space fanout push to alice with one telegram channel configured lands on
// that telegram channel's SendResponse with alice's AllowedChatIDs[0] as the
// chat ID. Critically, the runner loop must NOT run (payload.Text is a
// finished outbound message, not an inbound chat that needs LLM processing).
// The mock's backing Session has Provider=nil — if the dispatch loop ever
// routed through session.Run, the test would error or panic instead of
// passing cleanly.
func TestDispatchLoop_FamilyPush_DeliversToTargetChannel(t *testing.T) {
	tg := &mockPushChannel{}
	srv, shutdown := buildFamilyPushServer(t, &core.Config{
		Channels:       []core.ChannelConfig{{ChannelType: core.ChannelTelegram}},
		AllowedChatIDs: []string{"99999"},
	}, map[core.EventType]*mockPushChannel{core.EventTelegram: tg})
	defer shutdown()

	srv.eventCh <- pushEvent(t, "alice", core.FanoutPayload{Text: "🍚 저녁 준비됐어!"})

	calls := waitForCalls(t, tg, 1, 2*time.Second)
	if len(calls) != 1 {
		t.Fatalf("expected 1 SendResponse, got %d", len(calls))
	}
	if calls[0].ChatID != "99999" {
		t.Errorf("expected chatID 99999 (alice AllowedChatIDs[0]), got %q", calls[0].ChatID)
	}
	if calls[0].Response != "🍚 저녁 준비됐어!" {
		t.Errorf("expected push text, got %q", calls[0].Response)
	}
}

func TestDispatchLoop_LegacyFamilyPush_DeliversToTargetChannel(t *testing.T) {
	tg := &mockPushChannel{}
	srv, shutdown := buildFamilyPushServer(t, &core.Config{
		Channels:       []core.ChannelConfig{{ChannelType: core.ChannelTelegram}},
		AllowedChatIDs: []string{"99999"},
	}, map[core.EventType]*mockPushChannel{core.EventTelegram: tg})
	defer shutdown()

	srv.eventCh <- legacyFamilyPushEvent(t, "alice", core.FanoutPayload{Text: "legacy event"})

	calls := waitForCalls(t, tg, 1, 2*time.Second)
	if len(calls) != 1 {
		t.Fatalf("expected 1 SendResponse, got %d", len(calls))
	}
	if calls[0].ChatID != "99999" {
		t.Errorf("expected chatID 99999, got %q", calls[0].ChatID)
	}
	if calls[0].Response != "legacy event" {
		t.Errorf("expected push text, got %q", calls[0].Response)
	}
}

// TestDispatchLoop_FamilyPush_ChannelHintRoutesToSpecificChannel pins the
// ChannelHint semantics: when alice has both telegram and slack wired, a
// push with ChannelHint="slack" must land on slack and NOT on telegram.
// Without this, every team-space push would default to the first-configured
// channel regardless of intent.
func TestDispatchLoop_FamilyPush_ChannelHintRoutesToSpecificChannel(t *testing.T) {
	tg := &mockPushChannel{}
	sl := &mockPushChannel{}
	srv, shutdown := buildFamilyPushServer(t, &core.Config{
		Channels: []core.ChannelConfig{
			{ChannelType: core.ChannelTelegram},
			{ChannelType: core.ChannelSlack},
		},
		AllowedChatIDs: []string{"99999"},
	}, map[core.EventType]*mockPushChannel{
		core.EventTelegram: tg,
		core.EventSlack:    sl,
	})
	defer shutdown()

	srv.eventCh <- pushEvent(t, "alice", core.FanoutPayload{
		Text:        "슬랙으로 보내",
		ChannelHint: "slack",
	})

	slCalls := waitForCalls(t, sl, 1, 2*time.Second)
	if len(slCalls) != 1 {
		t.Fatalf("slack expected 1 call, got %d", len(slCalls))
	}
	if slCalls[0].Response != "슬랙으로 보내" {
		t.Errorf("slack response = %q", slCalls[0].Response)
	}
	if tgCalls := tg.calls(); len(tgCalls) != 0 {
		t.Errorf("telegram must not receive push when hint=slack; got %d calls", len(tgCalls))
	}
}

// TestDispatchLoop_FamilyPush_NoChannel_Enqueues covers the hot-reload
// window: alice has a telegram channel in Config but the spawner has nothing
// running (simulating a reconcile-in-progress or post-restart race). The
// push must land in pending_responses so the retry loop picks it up — a
// drop would silently lose family messages.
func TestDispatchLoop_FamilyPush_NoChannel_Enqueues(t *testing.T) {
	// Pass an empty mocks map: Config declares a telegram channel but
	// spawner has none registered.
	srv, shutdown := buildFamilyPushServer(t, &core.Config{
		Channels:       []core.ChannelConfig{{ChannelType: core.ChannelTelegram}},
		AllowedChatIDs: []string{"99999"},
	}, map[core.EventType]*mockPushChannel{})
	defer shutdown()

	srv.eventCh <- pushEvent(t, "alice", core.FanoutPayload{Text: "큐에 저장돼야 함"})

	// Poll pending_responses via Store until a row appears or timeout.
	deadline := time.Now().Add(2 * time.Second)
	var pending []interface{}
	for time.Now().Before(deadline) {
		rows, err := srv.store.DequeuePendingResponses(10)
		if err != nil {
			t.Fatalf("dequeue: %v", err)
		}
		if len(rows) > 0 {
			for _, r := range rows {
				pending = append(pending, r)
				if r.AccountID != "alice" {
					t.Errorf("queued row account = %q, want alice", r.AccountID)
				}
				if r.Response != "큐에 저장돼야 함" {
					t.Errorf("queued row response = %q", r.Response)
				}
				if r.ChatID != "99999" {
					t.Errorf("queued row chatID = %q, want 99999", r.ChatID)
				}
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(pending) == 0 {
		t.Fatal("expected pending_responses row for undelivered team-space push; queue is empty")
	}
}
