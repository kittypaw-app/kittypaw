package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/llm"
	"github.com/jinto/kittypaw/store"
)

type blockingDispatchProvider struct {
	content string
	started chan struct{}
	release chan struct{}
	done    chan error
	once    sync.Once
}

func newBlockingDispatchProvider(content string) *blockingDispatchProvider {
	return &blockingDispatchProvider{
		content: content,
		started: make(chan struct{}),
		release: make(chan struct{}),
		done:    make(chan error, 1),
	}
}

func (p *blockingDispatchProvider) Generate(ctx context.Context, _ []core.LlmMessage) (*llm.Response, error) {
	p.once.Do(func() { close(p.started) })
	select {
	case <-p.release:
		return &llm.Response{Content: p.content}, nil
	case <-ctx.Done():
		select {
		case p.done <- ctx.Err():
		default:
		}
		return nil, ctx.Err()
	}
}

func (p *blockingDispatchProvider) GenerateWithTools(ctx context.Context, msgs []core.LlmMessage, _ []llm.Tool) (*llm.Response, error) {
	return p.Generate(ctx, msgs)
}

func (p *blockingDispatchProvider) ContextWindow() int { return 200000 }
func (p *blockingDispatchProvider) MaxTokens() int     { return 4096 }

type firstBlockingDispatchProvider struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	calls   atomic.Int64
}

func newFirstBlockingDispatchProvider() *firstBlockingDispatchProvider {
	return &firstBlockingDispatchProvider{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (p *firstBlockingDispatchProvider) Generate(ctx context.Context, _ []core.LlmMessage) (*llm.Response, error) {
	call := p.calls.Add(1)
	if call == 1 {
		p.once.Do(func() { close(p.started) })
		select {
		case <-p.release:
			return &llm.Response{Content: `return "block done";`}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &llm.Response{Content: `return "default reply";`}, nil
}

func (p *firstBlockingDispatchProvider) GenerateWithTools(ctx context.Context, msgs []core.LlmMessage, _ []llm.Tool) (*llm.Response, error) {
	return p.Generate(ctx, msgs)
}

func (p *firstBlockingDispatchProvider) ContextWindow() int { return 200000 }
func (p *firstBlockingDispatchProvider) MaxTokens() int     { return 4096 }

type atomicDispatchProvider struct {
	content string
	calls   atomic.Int64
}

func (p *atomicDispatchProvider) Generate(_ context.Context, _ []core.LlmMessage) (*llm.Response, error) {
	p.calls.Add(1)
	return &llm.Response{Content: p.content}, nil
}

func (p *atomicDispatchProvider) GenerateWithTools(ctx context.Context, msgs []core.LlmMessage, _ []llm.Tool) (*llm.Response, error) {
	return p.Generate(ctx, msgs)
}

func (p *atomicDispatchProvider) ContextWindow() int { return 200000 }
func (p *atomicDispatchProvider) MaxTokens() int     { return 4096 }

func dispatchTestEvent(t *testing.T, eventType core.EventType, accountID, chatID, text string) core.Event {
	return dispatchTestChatEvent(t, eventType, accountID, chatID, "", text)
}

func dispatchTestChatEvent(t *testing.T, eventType core.EventType, accountID, chatID, sessionID, text string) core.Event {
	t.Helper()
	payload, err := json.Marshal(core.ChatPayload{ChatID: chatID, SourceSessionID: sessionID, Text: text})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return core.Event{Type: eventType, AccountID: accountID, Payload: payload}
}

func waitForChannelQueueDepth(t *testing.T, srv *Server, event core.Event, want int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-deadline:
			t.Fatalf("channel worker queue depth did not reach %d", want)
		case <-tick.C:
			srv.channelWorkersMu.Lock()
			worker := srv.channelWorkers[channelWorkerKey(event)]
			depth := 0
			if worker != nil {
				depth = len(worker.jobs)
			}
			srv.channelWorkersMu.Unlock()
			if depth >= want {
				return
			}
		}
	}
}

func readDispatchResponse(t *testing.T, responses <-chan sentResponse) sentResponse {
	t.Helper()
	select {
	case got := <-responses:
		return got
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for channel response")
		return sentResponse{}
	}
}

func TestDispatchLoop_AppliesModelOverrideAfterQueuedSlashCommand(t *testing.T) {
	var altCalls atomic.Int64
	altServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		altCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"return \"alt reply\";"},"finish_reason":"stop"}],"model":"gpt-alt","usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer altServer.Close()

	cfg := core.DefaultConfig()
	cfg.LLM.Default = "main"
	cfg.LLM.Models = []core.ModelConfig{
		{ID: "main", Provider: "openai", Model: "gpt-main", MaxTokens: 4096},
		{ID: "alt", Provider: "openai", Model: "gpt-alt", APIKey: "test-key", BaseURL: altServer.URL, MaxTokens: 4096},
	}
	root := t.TempDir()
	provider := newFirstBlockingDispatchProvider()
	deps := buildAccountDeps(t, root, "alice", &cfg)
	deps.Provider = provider
	srv := New([]*AccountDeps{deps}, "test")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.spawner = NewChannelSpawner(ctx, srv.eventCh)
	defer srv.spawner.StopAll()
	telegram := newEmittingStub("telegram", "alice")
	if err := srv.spawner.TrySpawn("alice", telegram, core.ChannelConfig{ChannelType: core.ChannelTelegram, Token: "alice-token"}); err != nil {
		t.Fatalf("spawn telegram: %v", err)
	}
	go srv.dispatchLoop(ctx)

	block := dispatchTestEvent(t, core.EventTelegram, "alice", "alice-chat", "block")
	srv.eventCh <- block
	select {
	case <-provider.started:
	case <-time.After(2 * time.Second):
		t.Fatal("blocking provider did not start")
	}

	srv.eventCh <- dispatchTestEvent(t, core.EventTelegram, "alice", "alice-chat", "/model alt")
	srv.eventCh <- dispatchTestEvent(t, core.EventTelegram, "alice", "alice-chat", "after model switch")
	waitForChannelQueueDepth(t, srv, block, 2)
	close(provider.release)

	if got := readDispatchResponse(t, telegram.responses); got.response != "block done" {
		t.Fatalf("first response = %q, want block done", got.response)
	}
	if got := readDispatchResponse(t, telegram.responses); !strings.Contains(got.response, "alt") {
		t.Fatalf("model switch response = %q, want alt", got.response)
	}
	if got := readDispatchResponse(t, telegram.responses); got.response != "alt reply" {
		t.Fatalf("post-/model response = %q, want alt reply", got.response)
	}
	if got := altCalls.Load(); got != 1 {
		t.Fatalf("alt provider calls = %d, want 1", got)
	}
}

func TestDispatchLoop_DoesNotBlockOtherAccountWhileRunInFlight(t *testing.T) {
	root := t.TempDir()
	aliceProvider := newBlockingDispatchProvider("alice done")
	defer close(aliceProvider.release)
	aliceDeps := buildAccountDeps(t, root, "alice", &core.Config{})
	aliceDeps.Provider = aliceProvider
	bobDeps := buildAccountDeps(t, root, "bob", &core.Config{})
	bobDeps.Provider = &chatRelayMockProvider{content: "bob reply"}
	srv := New([]*AccountDeps{aliceDeps, bobDeps}, "test")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.spawner = NewChannelSpawner(ctx, srv.eventCh)
	defer srv.spawner.StopAll()
	alice := newEmittingStub("telegram", "alice")
	bob := newEmittingStub("telegram", "bob")
	if err := srv.spawner.TrySpawn("alice", alice, core.ChannelConfig{ChannelType: core.ChannelTelegram, Token: "alice-token"}); err != nil {
		t.Fatalf("spawn alice: %v", err)
	}
	if err := srv.spawner.TrySpawn("bob", bob, core.ChannelConfig{ChannelType: core.ChannelTelegram, Token: "bob-token"}); err != nil {
		t.Fatalf("spawn bob: %v", err)
	}
	go srv.dispatchLoop(ctx)

	srv.eventCh <- dispatchTestEvent(t, core.EventTelegram, "alice", "alice-chat", "slow")
	select {
	case <-aliceProvider.started:
	case <-time.After(2 * time.Second):
		t.Fatal("alice run did not start")
	}

	srv.eventCh <- dispatchTestEvent(t, core.EventTelegram, "bob", "bob-chat", "fast")
	select {
	case got := <-bob.responses:
		if got.response != "bob reply" {
			t.Fatalf("bob response = %q, want bob reply", got.response)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("bob response was blocked behind alice's in-flight run")
	}
}

func TestDispatchLoop_DoesNotBlockOtherChatWhileRunInFlight(t *testing.T) {
	root := t.TempDir()
	provider := newFirstBlockingDispatchProvider()
	deps := buildAccountDeps(t, root, "alice", &core.Config{})
	deps.Provider = provider
	srv := New([]*AccountDeps{deps}, "test")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.spawner = NewChannelSpawner(ctx, srv.eventCh)
	defer srv.spawner.StopAll()
	telegram := newEmittingStub("telegram", "alice")
	if err := srv.spawner.TrySpawn("alice", telegram, core.ChannelConfig{ChannelType: core.ChannelTelegram, Token: "alice-token"}); err != nil {
		t.Fatalf("spawn telegram: %v", err)
	}
	go srv.dispatchLoop(ctx)

	srv.eventCh <- dispatchTestEvent(t, core.EventTelegram, "alice", "slow-chat", "slow")
	select {
	case <-provider.started:
	case <-time.After(2 * time.Second):
		t.Fatal("slow chat run did not start")
	}

	srv.eventCh <- dispatchTestEvent(t, core.EventTelegram, "alice", "fast-chat", "fast")
	select {
	case got := <-telegram.responses:
		if got.chatID != "fast-chat" || got.response != "default reply" {
			t.Fatalf("fast chat response = %+v, want fast-chat/default reply", got)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("fast chat response was blocked behind another chat's in-flight run")
	}

	close(provider.release)
}

func TestAccountRemovalCheckDoesNotWaitForAccountMu(t *testing.T) {
	root := t.TempDir()
	deps := buildAccountDeps(t, root, "alice", &core.Config{})
	srv := New([]*AccountDeps{deps}, "test")

	srv.accountMu.Lock()
	defer srv.accountMu.Unlock()

	done := make(chan bool, 1)
	go func() {
		done <- srv.isAccountRemovalInProgress("alice")
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("isAccountRemovalInProgress blocked on accountMu")
	}
}

func TestDispatchLoop_DropsUnauthorizedGroupSender(t *testing.T) {
	root := t.TempDir()
	cfg := core.Config{
		AllowedChatIDs: []string{"group-chat"},
		AllowedUserIDs: []string{"allowed-user"},
	}
	provider := &atomicDispatchProvider{content: `return "should not run";`}
	deps := buildAccountDeps(t, root, "alice", &cfg)
	deps.Provider = provider
	srv := New([]*AccountDeps{deps}, "test")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.spawner = NewChannelSpawner(ctx, srv.eventCh)
	defer srv.spawner.StopAll()
	telegram := newEmittingStub("telegram", "alice")
	if err := srv.spawner.TrySpawn("alice", telegram, core.ChannelConfig{ChannelType: core.ChannelTelegram, Token: "alice-token"}); err != nil {
		t.Fatalf("spawn telegram: %v", err)
	}
	go srv.dispatchLoop(ctx)

	srv.eventCh <- dispatchTestChatEvent(t, core.EventTelegram, "alice", "group-chat", "intruder", "blocked")
	select {
	case got := <-telegram.responses:
		t.Fatalf("unexpected response for unauthorized sender: %+v", got)
	case <-time.After(100 * time.Millisecond):
	}
	if got := provider.calls.Load(); got != 0 {
		t.Fatalf("provider calls = %d, want 0 for unauthorized sender", got)
	}
}

type outboxInspectingChannel struct {
	srv       *Server
	accountID string
	name      string
	responses chan sentResponse
	sawOutbox atomic.Bool
}

func newOutboxInspectingChannel(srv *Server, name, accountID string) *outboxInspectingChannel {
	return &outboxInspectingChannel{
		srv:       srv,
		accountID: accountID,
		name:      name,
		responses: make(chan sentResponse, 1),
	}
}

func (c *outboxInspectingChannel) Start(ctx context.Context, _ chan<- core.Event) error {
	<-ctx.Done()
	return ctx.Err()
}

func (c *outboxInspectingChannel) Name() string { return c.name }

func (c *outboxInspectingChannel) SendResponse(_ context.Context, chatID, response, replyToMessageID string) error {
	rows, err := c.srv.store.DequeuePendingResponses(10)
	if err == nil {
		for _, row := range rows {
			if row.AccountID == c.accountID && row.EventType == string(core.EventTelegram) && row.ChatID == chatID && row.Response == response {
				c.sawOutbox.Store(true)
				break
			}
		}
	}
	c.responses <- sentResponse{chatID: chatID, response: response, replyToMessageID: replyToMessageID}
	return err
}

func TestDispatchLoop_PersistsResponseBeforeSending(t *testing.T) {
	root := t.TempDir()
	deps := buildAccountDeps(t, root, "alice", &core.Config{})
	deps.Provider = &chatRelayMockProvider{content: `return "durable reply";`}
	srv := New([]*AccountDeps{deps}, "test")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.spawner = NewChannelSpawner(ctx, srv.eventCh)
	defer srv.spawner.StopAll()
	telegram := newOutboxInspectingChannel(srv, "telegram", "alice")
	if err := srv.spawner.TrySpawn("alice", telegram, core.ChannelConfig{ChannelType: core.ChannelTelegram, Token: "alice-token"}); err != nil {
		t.Fatalf("spawn telegram: %v", err)
	}
	go srv.dispatchLoop(ctx)

	srv.eventCh <- dispatchTestEvent(t, core.EventTelegram, "alice", "alice-chat", "persist first")
	if got := readDispatchResponse(t, telegram.responses); got.response != "durable reply" {
		t.Fatalf("response = %q, want durable reply", got.response)
	}
	if !telegram.sawOutbox.Load() {
		t.Fatal("response was not present in pending_responses before channel send")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rows, err := srv.store.DequeuePendingResponses(10)
		if err != nil {
			t.Fatalf("dequeue pending responses: %v", err)
		}
		if len(rows) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("pending response row was not removed after successful send")
}

func TestProcessChannelEventDoesNotEmitQueuedWhenEnqueueFails(t *testing.T) {
	root := t.TempDir()
	deps := buildAccountDeps(t, root, "alice", &core.Config{})
	srv := New([]*AccountDeps{deps}, "test", "alice")
	sub := srv.eventStream.Subscribe("alice")
	defer sub.Close()

	if err := srv.store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	event := dispatchTestEvent(t, core.EventTelegram, "alice", "alice-chat", "/help")
	payload, err := event.ParsePayload()
	if err != nil {
		t.Fatalf("parse payload: %v", err)
	}
	srv.processChannelEvent(context.Background(), "test-worker", channelEventJob{
		event:   event,
		payload: payload,
		runtime: srv.accounts.Runtime("alice"),
		chOK:    false,
	})

	events := collectAccountEvents(sub.events, 100*time.Millisecond)
	var sawQueued, sawFailed bool
	for _, event := range events {
		switch event.Type {
		case EventStreamDeliveryQueued:
			sawQueued = true
		case EventStreamDeliveryFailed:
			sawFailed = true
		}
	}
	if sawQueued {
		t.Fatalf("events = %+v, must not include delivery.queued after enqueue failure", events)
	}
	if !sawFailed {
		t.Fatalf("events = %+v, want delivery.failed after enqueue failure", events)
	}
}

func TestDurableInboundDrainProcessesQueuedEvent(t *testing.T) {
	root := t.TempDir()
	cfg := core.DefaultConfig()
	cfg.AllowedChatIDs = []string{"alice-chat"}
	deps := buildAccountDeps(t, root, "alice", &cfg)
	deps.Provider = &chatRelayMockProvider{content: `return "durable inbound reply";`}
	srv := New([]*AccountDeps{deps}, "test", "alice")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.spawner = NewChannelSpawner(ctx, srv.eventCh)
	go srv.dispatchLoop(ctx)

	event := dispatchTestEvent(t, core.EventTelegram, "alice", "alice-chat", "from durable inbox")
	event.SourceEventID = "telegram:update:777"
	if _, _, err := srv.publishInboundEvent(ctx, event); err != nil {
		t.Fatalf("publish inbound: %v", err)
	}
	if err := srv.drainInboundEventsOnce(ctx); err != nil {
		t.Fatalf("drain inbound: %v", err)
	}

	pending := waitForPendingResponse(t, srv, "durable inbound reply")

	claimed, err := srv.store.ClaimInboundEvents(10, time.Millisecond)
	if err != nil {
		t.Fatalf("claim after drain: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claimed after drain = %+v with pending %+v, want inbound marked done", claimed, pending)
	}
}

func waitForPendingResponse(t *testing.T, srv *Server, want string) []store.PendingResponse {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		pending, err := srv.store.DequeuePendingResponses(10)
		if err != nil {
			t.Fatalf("dequeue pending: %v", err)
		}
		for _, row := range pending {
			if row.Response == want {
				return pending
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for pending response %q", want)
	return nil
}

func collectAccountEvents(ch <-chan AccountEvent, wait time.Duration) []AccountEvent {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	var events []AccountEvent
	for {
		select {
		case event := <-ch:
			events = append(events, event)
		case <-timer.C:
			return events
		}
	}
}

func TestDispatchLoop_SuppressesNormalReplyAfterDirectNotifySend(t *testing.T) {
	root := t.TempDir()
	cfg := core.DefaultConfig()
	cfg.AllowedChatIDs = []string{"alice-chat"}
	cfg.Channels = []core.ChannelConfig{
		{ChannelType: core.ChannelTelegram, Token: "alice-token"},
	}
	deps := buildAccountDeps(t, root, "alice", &cfg)
	deps.Provider = &chatRelayMockProvider{content: `Notify.send("direct notice"); return null;`}
	srv := New([]*AccountDeps{deps}, "test", "alice")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.spawner = NewChannelSpawner(ctx, srv.eventCh)
	defer srv.spawner.StopAll()
	telegram := newEmittingStub("telegram", "alice")
	if err := srv.spawner.TrySpawn("alice", telegram, cfg.Channels[0]); err != nil {
		t.Fatalf("spawn telegram: %v", err)
	}
	go srv.dispatchLoop(ctx)

	srv.eventCh <- dispatchTestEvent(t, core.EventTelegram, "alice", "alice-chat", "send directly")
	if got := readDispatchResponse(t, telegram.responses); got.response != "direct notice" {
		t.Fatalf("first response = %+v, want direct notice", got)
	}
	select {
	case got := <-telegram.responses:
		t.Fatalf("unexpected normal follow-up response after direct send: %+v", got)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestDispatchLoop_SendsFailureResponseOnRunError(t *testing.T) {
	root := t.TempDir()
	deps := buildAccountDeps(t, root, "alice", &core.Config{})
	deps.Provider = &chatRelayMockProvider{content: `throw new Error("boom");`}
	srv := New([]*AccountDeps{deps}, "test")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.spawner = NewChannelSpawner(ctx, srv.eventCh)
	defer srv.spawner.StopAll()
	telegram := newEmittingStub("telegram", "alice")
	if err := srv.spawner.TrySpawn("alice", telegram, core.ChannelConfig{ChannelType: core.ChannelTelegram, Token: "alice-token"}); err != nil {
		t.Fatalf("spawn telegram: %v", err)
	}
	go srv.dispatchLoop(ctx)

	srv.eventCh <- dispatchTestEvent(t, core.EventTelegram, "alice", "alice-chat", "fail")
	select {
	case got := <-telegram.responses:
		if !strings.Contains(got.response, "처리 중 오류") {
			t.Fatalf("failure response = %q, want user-facing error", got.response)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for failure response")
	}
}

func TestDispatchLoop_ChannelRunTimeoutSendsFailureResponse(t *testing.T) {
	root := t.TempDir()
	provider := newBlockingDispatchProvider("too late")
	deps := buildAccountDeps(t, root, "alice", &core.Config{})
	deps.Provider = provider
	srv := New([]*AccountDeps{deps}, "test")
	srv.channelTurnTimeout = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.spawner = NewChannelSpawner(ctx, srv.eventCh)
	defer srv.spawner.StopAll()
	telegram := newEmittingStub("telegram", "alice")
	if err := srv.spawner.TrySpawn("alice", telegram, core.ChannelConfig{ChannelType: core.ChannelTelegram, Token: "alice-token"}); err != nil {
		t.Fatalf("spawn telegram: %v", err)
	}
	go srv.dispatchLoop(ctx)

	srv.eventCh <- dispatchTestEvent(t, core.EventTelegram, "alice", "alice-chat", "timeout")
	select {
	case <-provider.started:
	case <-time.After(2 * time.Second):
		t.Fatal("provider did not start")
	}
	select {
	case got := <-telegram.responses:
		if !strings.Contains(got.response, "처리 중 오류") {
			t.Fatalf("timeout response = %q, want user-facing error", got.response)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for timeout failure response")
	}
}

func TestRemoveAccountCancelsInFlightChannelWorker(t *testing.T) {
	root := t.TempDir()
	provider := newBlockingDispatchProvider("too late")
	deps := buildAccountDeps(t, root, "alice", &core.Config{})
	deps.Provider = provider
	srv := New([]*AccountDeps{deps}, "test")
	srv.channelTurnTimeout = -1

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.spawner = NewChannelSpawner(ctx, srv.eventCh)
	defer srv.spawner.StopAll()
	telegram := newEmittingStub("telegram", "alice")
	if err := srv.spawner.TrySpawn("alice", telegram, core.ChannelConfig{ChannelType: core.ChannelTelegram, Token: "alice-token"}); err != nil {
		t.Fatalf("spawn telegram: %v", err)
	}
	go srv.dispatchLoop(ctx)

	srv.eventCh <- dispatchTestEvent(t, core.EventTelegram, "alice", "alice-chat", "remove")
	select {
	case <-provider.started:
	case <-time.After(2 * time.Second):
		t.Fatal("provider did not start")
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.RemoveAccount("alice")
	}()

	select {
	case err := <-provider.done:
		if err == nil {
			t.Fatal("provider context error is nil, want cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RemoveAccount did not cancel the in-flight channel worker")
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("RemoveAccount: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RemoveAccount did not return after worker cancellation")
	}
	select {
	case got := <-telegram.responses:
		t.Fatalf("unexpected response after account removal: %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestRemoveAccountWorkerDrainFailureDoesNotStopChannels(t *testing.T) {
	root := t.TempDir()
	provider := newBlockingDispatchProvider("too late")
	defer close(provider.release)
	deps := buildAccountDeps(t, root, "alice", &core.Config{})
	deps.Provider = provider
	srv := New([]*AccountDeps{deps}, "test")
	srv.channelTurnTimeout = -1

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.spawner = NewChannelSpawner(ctx, srv.eventCh)
	defer srv.spawner.StopAll()
	telegram := newEmittingStub("telegram", "alice")
	if err := srv.spawner.TrySpawn("alice", telegram, core.ChannelConfig{ChannelType: core.ChannelTelegram, Token: "alice-token"}); err != nil {
		t.Fatalf("spawn telegram: %v", err)
	}
	go srv.dispatchLoop(ctx)

	srv.eventCh <- dispatchTestEvent(t, core.EventTelegram, "alice", "alice-chat", "remove")
	select {
	case <-provider.started:
	case <-time.After(2 * time.Second):
		t.Fatal("provider did not start")
	}

	oldTimeout := channelWorkerStopTimeout
	channelWorkerStopTimeout = 1 * time.Nanosecond
	t.Cleanup(func() { channelWorkerStopTimeout = oldTimeout })

	err := srv.RemoveAccount("alice")
	if err == nil {
		t.Fatal("RemoveAccount succeeded, want worker drain timeout")
	}
	if !strings.Contains(err.Error(), "drain channel workers") {
		t.Fatalf("RemoveAccount error = %v, want drain channel workers", err)
	}
	if _, ok := srv.spawner.GetChannel("alice", core.EventTelegram); !ok {
		t.Fatal("telegram channel was stopped even though worker drain failed")
	}
	if srv.accounts.Runtime("alice") == nil {
		t.Fatal("alice session should remain active after failed removal")
	}
	if srv.isAccountRemovalInProgress("alice") {
		t.Fatal("removingAccount flag should be cleared after failed removal")
	}
}
