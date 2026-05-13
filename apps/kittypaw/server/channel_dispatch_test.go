package server

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/llm"
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

func dispatchTestEvent(t *testing.T, eventType core.EventType, accountID, chatID, text string) core.Event {
	t.Helper()
	payload, err := json.Marshal(core.ChatPayload{ChatID: chatID, Text: text})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return core.Event{Type: eventType, AccountID: accountID, Payload: payload}
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
