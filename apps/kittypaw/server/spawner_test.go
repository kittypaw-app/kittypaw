package server

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jinto/kittypaw/channel"
	"github.com/jinto/kittypaw/core"
)

// stubChannel is a minimal Channel implementation for testing.
// It blocks in Start until ctx is canceled.
type stubChannel struct {
	name    string
	started chan struct{} // closed when Start begins
	mu      sync.Mutex
	sends   []string // records SendResponse calls
}

func newStub(name string) *stubChannel {
	return &stubChannel{name: name, started: make(chan struct{})}
}

func (s *stubChannel) Start(ctx context.Context, eventCh chan<- core.Event) error {
	close(s.started)
	<-ctx.Done()
	return ctx.Err()
}

func (s *stubChannel) SendResponse(_ context.Context, chatID, response, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sends = append(s.sends, chatID+":"+response)
	return nil
}

func (s *stubChannel) Name() string { return s.name }

type recordingEventSink struct {
	events chan core.Event
}

func newRecordingEventSink() *recordingEventSink {
	return &recordingEventSink{events: make(chan core.Event, 1)}
}

func (s *recordingEventSink) PublishEvent(ctx context.Context, event core.Event) error {
	select {
	case s.events <- event:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type sinkAwareStubChannel struct {
	*stubChannel
}

func newSinkAwareStub(name string) *sinkAwareStubChannel {
	return &sinkAwareStubChannel{stubChannel: newStub(name)}
}

func (s *sinkAwareStubChannel) StartWithEventSink(ctx context.Context, sink channel.EventSink) error {
	close(s.started)
	if err := sink.PublishEvent(ctx, core.Event{Type: core.EventTelegram, AccountID: testAccount}); err != nil {
		return err
	}
	<-ctx.Done()
	return ctx.Err()
}

type exitingStubChannel struct {
	*stubChannel
	err error
}

func newExitingStub(name string, err error) *exitingStubChannel {
	return &exitingStubChannel{stubChannel: newStub(name), err: err}
}

func (s *exitingStubChannel) Start(_ context.Context, _ chan<- core.Event) error {
	close(s.started)
	return s.err
}

// waitStarted blocks until the stub's Start method has been entered.
func (s *stubChannel) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-s.started:
	case <-time.After(2 * time.Second):
		t.Fatal("stub channel did not start in time")
	}
}

// Test helper: the default account used for legacy single-account tests.
const testAccount = DefaultAccountID

// --- Tests ---

func TestTrySpawn_StartsChannel(t *testing.T) {
	eventCh := make(chan core.Event, 8)
	sp := NewChannelSpawner(context.Background(), eventCh)

	stub := newStub("telegram")
	cfg := core.ChannelConfig{ChannelType: core.ChannelTelegram, Token: "tok"}

	if err := sp.TrySpawn(testAccount, stub, cfg); err != nil {
		t.Fatalf("TrySpawn: %v", err)
	}
	stub.waitStarted(t)

	// Verify it appears in List and GetChannel.
	ch, ok := sp.GetChannel(testAccount, core.EventTelegram)
	if !ok || ch == nil {
		t.Fatal("GetChannel returned false after TrySpawn")
	}

	statuses := sp.List()
	if len(statuses) != 1 {
		t.Fatalf("List: got %d, want 1", len(statuses))
	}
	if statuses[0].Name != "telegram" || statuses[0].Type != "telegram" || !statuses[0].Running || statuses[0].AccountID != testAccount {
		t.Errorf("List: unexpected status %+v", statuses[0])
	}

	// Cleanup.
	sp.Stop(testAccount, "telegram")
}

func TestTrySpawn_UsesDurableEventSinkWhenAvailable(t *testing.T) {
	eventCh := make(chan core.Event, 8)
	sink := newRecordingEventSink()
	sp := NewChannelSpawner(context.Background(), eventCh, sink)

	stub := newSinkAwareStub("telegram")
	cfg := core.ChannelConfig{ChannelType: core.ChannelTelegram, Token: "tok"}
	if err := sp.TrySpawn(testAccount, stub, cfg); err != nil {
		t.Fatalf("TrySpawn: %v", err)
	}
	stub.waitStarted(t)

	select {
	case event := <-sink.events:
		if event.Type != core.EventTelegram || event.AccountID != testAccount {
			t.Fatalf("sink event = %+v, want telegram/%s", event, testAccount)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("channel did not publish through durable event sink")
	}
	select {
	case event := <-eventCh:
		t.Fatalf("legacy event channel received %+v, want durable sink path", event)
	default:
	}

	sp.Stop(testAccount, "telegram")
}

func TestTrySpawn_RemovesUnexpectedlyStoppedChannel(t *testing.T) {
	eventCh := make(chan core.Event, 8)
	sp := NewChannelSpawner(context.Background(), eventCh)

	stub := newExitingStub("telegram", errors.New("boom"))
	cfg := core.ChannelConfig{ChannelType: core.ChannelTelegram, Token: "tok"}
	if err := sp.TrySpawn(testAccount, stub, cfg); err != nil {
		t.Fatalf("TrySpawn: %v", err)
	}
	stub.waitStarted(t)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := sp.GetChannel(testAccount, core.EventTelegram); !ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("unexpectedly stopped channel remained registered")
}

func TestTrySpawn_Idempotent(t *testing.T) {
	eventCh := make(chan core.Event, 8)
	sp := NewChannelSpawner(context.Background(), eventCh)

	stub1 := newStub("telegram")
	stub2 := newStub("telegram")
	cfg := core.ChannelConfig{ChannelType: core.ChannelTelegram, Token: "tok"}

	sp.TrySpawn(testAccount, stub1, cfg)
	stub1.waitStarted(t)

	// Second TrySpawn with same (account, type) should be a no-op.
	if err := sp.TrySpawn(testAccount, stub2, cfg); err != nil {
		t.Fatalf("second TrySpawn: %v", err)
	}

	// Original stub should still be the one returned.
	ch, _ := sp.GetChannel(testAccount, core.EventTelegram)
	if ch != stub1 {
		t.Error("TrySpawn replaced existing channel — should be idempotent")
	}

	sp.Stop(testAccount, "telegram")
}

func TestStop_CloseDone(t *testing.T) {
	eventCh := make(chan core.Event, 8)
	sp := NewChannelSpawner(context.Background(), eventCh)

	stub := newStub("slack")
	cfg := core.ChannelConfig{ChannelType: core.ChannelSlack, Token: "tok"}
	sp.TrySpawn(testAccount, stub, cfg)
	stub.waitStarted(t)

	if err := sp.Stop(testAccount, "slack"); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// After Stop, GetChannel should return false.
	_, ok := sp.GetChannel(testAccount, core.EventSlack)
	if ok {
		t.Error("GetChannel returned true after Stop")
	}
}

func TestStop_NotFound(t *testing.T) {
	sp := NewChannelSpawner(context.Background(), make(chan core.Event, 1))
	if err := sp.Stop(testAccount, "nonexistent"); err != ErrChannelNotFound {
		t.Errorf("Stop nonexistent: got %v, want ErrChannelNotFound", err)
	}
}

func TestGetChannel_EmptySpawner(t *testing.T) {
	sp := NewChannelSpawner(context.Background(), make(chan core.Event, 1))
	ch, ok := sp.GetChannel(testAccount, core.EventTelegram)
	if ok || ch != nil {
		t.Error("GetChannel on empty spawner should return nil, false")
	}
}

func TestList_Empty(t *testing.T) {
	sp := NewChannelSpawner(context.Background(), make(chan core.Event, 1))
	statuses := sp.List()
	if len(statuses) != 0 {
		t.Errorf("List on empty spawner: got %d, want 0", len(statuses))
	}
}

func TestList_MultipleChannels(t *testing.T) {
	eventCh := make(chan core.Event, 8)
	sp := NewChannelSpawner(context.Background(), eventCh)

	stubs := []*stubChannel{newStub("telegram"), newStub("slack")}
	cfgs := []core.ChannelConfig{
		{ChannelType: core.ChannelTelegram, Token: "t1"},
		{ChannelType: core.ChannelSlack, Token: "t2"},
	}

	for i, stub := range stubs {
		sp.TrySpawn(testAccount, stub, cfgs[i])
		stub.waitStarted(t)
	}

	statuses := sp.List()
	if len(statuses) != 2 {
		t.Fatalf("List: got %d, want 2", len(statuses))
	}

	// Cleanup.
	sp.Stop(testAccount, "telegram")
	sp.Stop(testAccount, "slack")
}

// --- Reconcile / ReplaceSpawn / StopAll tests ---

func TestReplaceSpawn(t *testing.T) {
	eventCh := make(chan core.Event, 8)
	sp := NewChannelSpawner(context.Background(), eventCh)

	stub1 := newStub("telegram")
	cfg1 := core.ChannelConfig{ChannelType: core.ChannelTelegram, Token: "old"}
	sp.TrySpawn(testAccount, stub1, cfg1)
	stub1.waitStarted(t)

	// Replace with new stub.
	stub2 := newStub("telegram")
	cfg2 := core.ChannelConfig{ChannelType: core.ChannelTelegram, Token: "new"}
	if err := sp.ReplaceSpawn(testAccount, stub2, cfg2); err != nil {
		t.Fatalf("ReplaceSpawn: %v", err)
	}
	stub2.waitStarted(t)

	// Verify new channel is returned.
	ch, ok := sp.GetChannel(testAccount, core.EventTelegram)
	if !ok || ch != stub2 {
		t.Error("GetChannel should return the replacement channel")
	}

	sp.Stop(testAccount, "telegram")
}

func TestReconcile_AddNewChannel(t *testing.T) {
	eventCh := make(chan core.Event, 8)
	sp := NewChannelSpawner(context.Background(), eventCh)

	configs := []core.ChannelConfig{
		{ChannelType: core.ChannelTelegram, Token: "tok"},
	}
	if err := sp.Reconcile(testAccount, configs); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	// Give goroutine time to start.
	time.Sleep(50 * time.Millisecond)

	ch, ok := sp.GetChannel(testAccount, core.EventTelegram)
	if !ok || ch == nil {
		t.Error("Reconcile should have spawned telegram channel")
	}

	sp.StopAll()
}

func TestReconcile_RemoveChannel(t *testing.T) {
	eventCh := make(chan core.Event, 8)
	sp := NewChannelSpawner(context.Background(), eventCh)

	stub := newStub("telegram")
	cfg := core.ChannelConfig{ChannelType: core.ChannelTelegram, Token: "tok"}
	sp.TrySpawn(testAccount, stub, cfg)
	stub.waitStarted(t)

	// Reconcile with empty config → should stop telegram.
	if err := sp.Reconcile(testAccount, nil); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	_, ok := sp.GetChannel(testAccount, core.EventTelegram)
	if ok {
		t.Error("telegram should be removed after Reconcile with empty config")
	}
}

func TestReconcile_ReplaceChanged(t *testing.T) {
	eventCh := make(chan core.Event, 8)
	sp := NewChannelSpawner(context.Background(), eventCh)

	stub := newStub("telegram")
	cfgOld := core.ChannelConfig{ChannelType: core.ChannelTelegram, Token: "old-token"}
	sp.TrySpawn(testAccount, stub, cfgOld)
	stub.waitStarted(t)

	// Reconcile with changed token → should replace.
	cfgNew := core.ChannelConfig{ChannelType: core.ChannelTelegram, Token: "new-token"}
	if err := sp.Reconcile(testAccount, []core.ChannelConfig{cfgNew}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	ch, ok := sp.GetChannel(testAccount, core.EventTelegram)
	if !ok || ch == nil {
		t.Error("Reconcile should have spawned replacement channel")
	}
	// Original stub should no longer be the channel.
	if ch == stub {
		t.Error("Reconcile did not replace the channel despite config change")
	}

	sp.StopAll()
}

func TestReconcile_SkipUnchanged(t *testing.T) {
	eventCh := make(chan core.Event, 8)
	sp := NewChannelSpawner(context.Background(), eventCh)

	stub := newStub("telegram")
	cfg := core.ChannelConfig{ChannelType: core.ChannelTelegram, Token: "tok"}
	sp.TrySpawn(testAccount, stub, cfg)
	stub.waitStarted(t)

	// Reconcile with same config → should keep existing channel.
	if err := sp.Reconcile(testAccount, []core.ChannelConfig{cfg}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	ch, _ := sp.GetChannel(testAccount, core.EventTelegram)
	if ch != stub {
		t.Error("Reconcile replaced a channel whose config did not change")
	}

	sp.Stop(testAccount, "telegram")
}

func TestReconcile_SkipsWebSocket(t *testing.T) {
	eventCh := make(chan core.Event, 8)
	sp := NewChannelSpawner(context.Background(), eventCh)

	configs := []core.ChannelConfig{
		{ChannelType: core.ChannelWeb, BindAddr: ":8080"},
	}
	sp.Reconcile(testAccount, configs)

	_, ok := sp.GetChannel(testAccount, core.EventWebChat)
	if ok {
		t.Error("Reconcile should skip WebSocket channels")
	}
}

func TestReconcile_BestEffort(t *testing.T) {
	eventCh := make(chan core.Event, 8)
	sp := NewChannelSpawner(context.Background(), eventCh)

	// Telegram with valid token + Slack with empty token (will fail FromConfig).
	configs := []core.ChannelConfig{
		{ChannelType: core.ChannelTelegram, Token: "tok"},
		{ChannelType: core.ChannelSlack, Token: ""},
	}
	err := sp.Reconcile(testAccount, configs)
	if err == nil {
		t.Fatal("Reconcile should return error for invalid slack config")
	}

	time.Sleep(50 * time.Millisecond)

	// Telegram should still have been spawned despite Slack failure.
	_, ok := sp.GetChannel(testAccount, core.EventTelegram)
	if !ok {
		t.Error("Telegram should be running even though Slack failed")
	}

	sp.StopAll()
}

func TestStopAll_Parallel(t *testing.T) {
	eventCh := make(chan core.Event, 8)
	sp := NewChannelSpawner(context.Background(), eventCh)

	stubs := []*stubChannel{newStub("telegram"), newStub("slack"), newStub("discord")}
	cfgs := []core.ChannelConfig{
		{ChannelType: core.ChannelTelegram, Token: "t1"},
		{ChannelType: core.ChannelSlack, Token: "t2"},
		{ChannelType: core.ChannelDiscord, Token: "t3"},
	}

	for i, stub := range stubs {
		sp.TrySpawn(testAccount, stub, cfgs[i])
		stub.waitStarted(t)
	}

	start := time.Now()
	sp.StopAll()
	elapsed := time.Since(start)

	// All channels stopped.
	if len(sp.List()) != 0 {
		t.Error("StopAll should clear all channels")
	}

	// Parallel stop should complete quickly (all three in parallel, not 3x sequential).
	if elapsed > 2*time.Second {
		t.Errorf("StopAll took %v — expected parallel stop to be fast", elapsed)
	}
}

func TestConfigEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b core.ChannelConfig
		want bool
	}{
		{"identical", core.ChannelConfig{ChannelType: "t", Token: "x"}, core.ChannelConfig{ChannelType: "t", Token: "x"}, true},
		{"token differs", core.ChannelConfig{Token: "a"}, core.ChannelConfig{Token: "b"}, false},
		{"both empty", core.ChannelConfig{}, core.ChannelConfig{}, true},
		{"kakao ws url equal", core.ChannelConfig{KakaoWSURL: "wss://r.example.com/ws/t"}, core.ChannelConfig{KakaoWSURL: "wss://r.example.com/ws/t"}, true},
		{"kakao ws url differ", core.ChannelConfig{KakaoWSURL: "wss://r.example.com/ws/a"}, core.ChannelConfig{KakaoWSURL: "wss://r.example.com/ws/b"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := configEqual(tt.a, tt.b); got != tt.want {
				t.Errorf("configEqual = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStop_ConcurrentGetChannel(t *testing.T) {
	// Verify that Stop does not deadlock with concurrent GetChannel calls.
	eventCh := make(chan core.Event, 8)
	sp := NewChannelSpawner(context.Background(), eventCh)

	stub := newStub("telegram")
	cfg := core.ChannelConfig{ChannelType: core.ChannelTelegram, Token: "tok"}
	sp.TrySpawn(testAccount, stub, cfg)
	stub.waitStarted(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Rapidly call GetChannel while Stop is in progress.
		for i := 0; i < 100; i++ {
			sp.GetChannel(testAccount, core.EventTelegram)
		}
	}()

	sp.Stop(testAccount, "telegram")
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock: GetChannel blocked during Stop")
	}
}

// --- Multi-account isolation tests ---

// TestTrySpawn_SameChannelTypeDifferentAccounts enforces the composite-key
// invariant: two accounts can both run a "telegram" channel without
// collision. Without per-account keys, TrySpawn(bob) would silently skip
// because the key "telegram" would already be taken by alice.
func TestTrySpawn_SameChannelTypeDifferentAccounts(t *testing.T) {
	eventCh := make(chan core.Event, 8)
	sp := NewChannelSpawner(context.Background(), eventCh)

	aliceBot := newStub("telegram")
	bobBot := newStub("telegram")
	cfgA := core.ChannelConfig{ChannelType: core.ChannelTelegram, Token: "alice-tok"}
	cfgB := core.ChannelConfig{ChannelType: core.ChannelTelegram, Token: "bob-tok"}

	if err := sp.TrySpawn("alice", aliceBot, cfgA); err != nil {
		t.Fatalf("TrySpawn(alice): %v", err)
	}
	if err := sp.TrySpawn("bob", bobBot, cfgB); err != nil {
		t.Fatalf("TrySpawn(bob): %v", err)
	}
	aliceBot.waitStarted(t)
	bobBot.waitStarted(t)

	ch, ok := sp.GetChannel("alice", core.EventTelegram)
	if !ok || ch != aliceBot {
		t.Errorf("GetChannel(alice) = %v, want aliceBot", ch)
	}
	ch, ok = sp.GetChannel("bob", core.EventTelegram)
	if !ok || ch != bobBot {
		t.Errorf("GetChannel(bob) = %v, want bobBot", ch)
	}

	if len(sp.List()) != 2 {
		t.Errorf("List len = %d, want 2 (alice + bob)", len(sp.List()))
	}

	sp.StopAll()
}

// TestGetChannel_AccountIsolation enforces I1: a channel under account A
// must not be reachable via account B's ID.
func TestGetChannel_AccountIsolation(t *testing.T) {
	eventCh := make(chan core.Event, 8)
	sp := NewChannelSpawner(context.Background(), eventCh)

	stub := newStub("telegram")
	cfg := core.ChannelConfig{ChannelType: core.ChannelTelegram, Token: "tok"}
	sp.TrySpawn("alice", stub, cfg)
	stub.waitStarted(t)

	if _, ok := sp.GetChannel("bob", core.EventTelegram); ok {
		t.Error("GetChannel(bob) unexpectedly found alice's channel")
	}
	if _, ok := sp.GetChannel("", core.EventTelegram); ok {
		t.Error("GetChannel(\"\") unexpectedly found alice's channel")
	}

	sp.StopAll()
}

// TestReconcile_PerAccountScope enforces that Reconcile only touches the
// given account's channels. Otherwise reconciling bob with an empty config
// would stop alice's channels — catastrophic in a multi-account setup.
func TestReconcile_PerAccountScope(t *testing.T) {
	eventCh := make(chan core.Event, 8)
	sp := NewChannelSpawner(context.Background(), eventCh)

	alice := newStub("telegram")
	cfgA := core.ChannelConfig{ChannelType: core.ChannelTelegram, Token: "alice"}
	sp.TrySpawn("alice", alice, cfgA)
	alice.waitStarted(t)

	// Bob's reconcile with empty config must leave alice alone.
	if err := sp.Reconcile("bob", nil); err != nil {
		t.Fatalf("Reconcile(bob, nil): %v", err)
	}

	if _, ok := sp.GetChannel("alice", core.EventTelegram); !ok {
		t.Error("alice's channel was removed by bob's reconcile — account leak")
	}

	sp.StopAll()
}
