package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jinto/kittypaw/core"
)

// These tests exercise AccountRuntime.RunTurn's idempotency cache without
// going through AccountRuntime.Run. Two layers:
//
//  1. Pre-populated turnCache + RunTurn — verifies dedup / waiter /
//     ctx-cancel logic in the cache-hit path.
//  2. Direct runTurnOwner with synthetic exec functions — verifies
//     ctx detachment + panic recovery + TTL eviction in the owner
//     path (where Run's full dependency graph would otherwise be in
//     the way).
//
// The empty-turnID fall-through to Run is covered by the higher-level
// server/ws integration tests.

func TestRunTurn_DedupCachedResult(t *testing.T) {
	s := &AccountRuntime{}

	closedDone := make(chan struct{})
	close(closedDone)
	cached := &turnState{
		done:   closedDone,
		result: "cached response",
	}
	s.turnCache.Store("turn-1", cached)

	got, err := s.RunTurn(context.Background(), "turn-1", core.Event{}, nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "cached response" {
		t.Errorf("got %q, want cached response", got)
	}
}

func TestRunTurn_DedupCachedError(t *testing.T) {
	s := &AccountRuntime{}

	closedDone := make(chan struct{})
	close(closedDone)
	cached := &turnState{
		done: closedDone,
		err:  errors.New("upstream LLM failure"),
	}
	s.turnCache.Store("turn-2", cached)

	_, err := s.RunTurn(context.Background(), "turn-2", core.Event{}, nil)
	if err == nil || err.Error() != "upstream LLM failure" {
		t.Fatalf("expected cached error, got %v", err)
	}
}

func TestRunTurn_InFlightWait(t *testing.T) {
	s := &AccountRuntime{}

	pending := &turnState{done: make(chan struct{})}
	s.turnCache.Store("turn-3", pending)

	// Owner finishes the in-flight after a short delay.
	go func() {
		time.Sleep(20 * time.Millisecond)
		pending.result = "in-flight result"
		close(pending.done)
	}()

	start := time.Now()
	got, err := s.RunTurn(context.Background(), "turn-3", core.Event{}, nil)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "in-flight result" {
		t.Errorf("got %q, want in-flight result", got)
	}
	if elapsed < 15*time.Millisecond {
		t.Errorf("returned too quickly (%v) — should have waited on in-flight", elapsed)
	}
}

func TestRunTurn_CtxCancelDuringInFlight(t *testing.T) {
	s := &AccountRuntime{}

	pending := &turnState{done: make(chan struct{})} // never closed
	s.turnCache.Store("turn-4", pending)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := s.RunTurn(ctx, "turn-4", core.Event{}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestRunTurn_DedupConcurrentRetries(t *testing.T) {
	// Simulates the Phase 12 silent-reconnect path: a single user input
	// drives N concurrent RunTurn calls with the same turn_id (the
	// transport drop + retry race). Only one should drive execution;
	// the others must see the cached result.
	s := &AccountRuntime{}

	pending := &turnState{done: make(chan struct{})}
	s.turnCache.Store("turn-5", pending)

	const waiters = 8
	results := make(chan string, waiters)
	var wakeups atomic.Int32

	for i := 0; i < waiters; i++ {
		go func() {
			r, _ := s.RunTurn(context.Background(), "turn-5", core.Event{}, nil)
			wakeups.Add(1)
			results <- r
		}()
	}

	// Let the goroutines park on <-pending.done.
	time.Sleep(5 * time.Millisecond)
	if got := wakeups.Load(); got != 0 {
		t.Fatalf("waiters woke prematurely: %d", got)
	}

	pending.result = "shared"
	close(pending.done)

	for i := 0; i < waiters; i++ {
		select {
		case r := <-results:
			if r != "shared" {
				t.Errorf("waiter got %q, want shared", r)
			}
		case <-time.After(time.Second):
			t.Fatal("waiter did not wake within 1s")
		}
	}
}

func TestRunTurnOwner_DetachesFromCallerContext(t *testing.T) {
	// The owner runs on a context that's detached from the caller's
	// — a transport drop on the caller side must NOT abort the
	// in-flight LLM call. Without detachment, the very retry case
	// the cache exists for would observe a context-canceled result.
	s := &AccountRuntime{}
	state := &turnState{done: make(chan struct{})}
	s.turnCache.Store("owner-detach", state)

	parentCtx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled before runTurnOwner is called

	execObserved := make(chan error, 1)
	s.runTurnOwner(parentCtx, "owner-detach", state, func(c context.Context) (string, error) {
		// Simulate a short LLM call and check whether our ctx is
		// alive despite the caller's cancellation.
		select {
		case <-c.Done():
			execObserved <- c.Err()
		case <-time.After(20 * time.Millisecond):
			execObserved <- nil
		}
		return "ok", nil
	})

	got := <-execObserved
	if got != nil {
		t.Errorf("owner ctx leaked caller cancel: %v — RunTurn idempotency contract broken", got)
	}
	if state.result != "ok" || state.err != nil {
		t.Errorf("owner exec did not store result/err correctly: got %q / %v", state.result, state.err)
	}
}

func TestRunTurnOwner_PanicEvictsAndRePanics(t *testing.T) {
	// An owner panic must (a) record the panic as state.err so
	// waiters see a real error, (b) close state.done so they wake,
	// (c) evict the poisoned cache entry so retries take the cold
	// path instead of inheriting the empty-result poison, and (d)
	// re-panic so upstream RecoverAccountPanic surfaces the failure.
	s := &AccountRuntime{}
	state := &turnState{done: make(chan struct{})}
	s.turnCache.Store("owner-panic", state)

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic to propagate from runTurnOwner")
		}

		// done must be closed.
		select {
		case <-state.done:
		default:
			t.Error("state.done was not closed after panic")
		}

		// state.err must capture the panic (so waiters get a real error).
		if state.err == nil || !strings.Contains(state.err.Error(), "synthetic boom") {
			t.Errorf("state.err did not capture panic: %v", state.err)
		}

		// Cache entry must be evicted (no waiter inherits poison after TTL).
		if _, exists := s.turnCache.Load("owner-panic"); exists {
			t.Error("cache entry should be evicted on owner panic")
		}
	}()

	s.runTurnOwner(context.Background(), "owner-panic", state, func(c context.Context) (string, error) {
		panic("synthetic boom")
	})
}

func TestRunTurnOwner_NormalCompletionSchedulesEviction(t *testing.T) {
	// Sanity check: the owner happy path closes done, stores
	// result/err, and the entry stays in the cache under TTL.
	s := &AccountRuntime{}
	state := &turnState{done: make(chan struct{})}
	s.turnCache.Store("owner-ok", state)

	s.runTurnOwner(context.Background(), "owner-ok", state, func(c context.Context) (string, error) {
		return "happy", nil
	})

	select {
	case <-state.done:
	default:
		t.Fatal("state.done should be closed after exec")
	}
	if state.result != "happy" || state.err != nil {
		t.Errorf("got %q / %v, want happy / nil", state.result, state.err)
	}
	if _, exists := s.turnCache.Load("owner-ok"); !exists {
		t.Error("cache entry evicted prematurely (TTL not yet expired)")
	}
}

func TestAccountRuntimeAcquireTurnAdmissionBusy(t *testing.T) {
	s := &AccountRuntime{
		AccountID: "alice",
		Admission: NewRuntimeAdmission(RuntimeAdmissionConfig{
			MaxConcurrentAccount: 1,
			MaxQueuedAccount:     0,
			MaxConcurrentScope:   0,
		}),
	}

	_, first, err := s.acquireTurnAdmission(context.Background(), core.Event{
		Type: core.EventWebChat,
		Payload: marshalRunTurnPayload(t, core.ChatPayload{
			SourceSessionID: "one",
		}),
	})
	if err != nil {
		t.Fatalf("first acquireTurnAdmission: %v", err)
	}
	defer first.Release()

	_, _, err = s.acquireTurnAdmission(context.Background(), core.Event{
		Type: core.EventWebChat,
		Payload: marshalRunTurnPayload(t, core.ChatPayload{
			SourceSessionID: "two",
		}),
	})
	if !errors.Is(err, ErrRuntimeAdmissionBusy) {
		t.Fatalf("second acquireTurnAdmission err = %v, want ErrRuntimeAdmissionBusy", err)
	}
}

func TestAccountRuntimeAcquireTurnAdmissionReentrant(t *testing.T) {
	s := &AccountRuntime{
		AccountID: "alice",
		Admission: NewRuntimeAdmission(RuntimeAdmissionConfig{
			MaxConcurrentAccount: 1,
			MaxQueuedAccount:     0,
			MaxConcurrentScope:   0,
		}),
	}

	ctx, first, err := s.acquireTurnAdmission(context.Background(), core.Event{})
	if err != nil {
		t.Fatalf("first acquireTurnAdmission: %v", err)
	}
	defer first.Release()

	_, second, err := s.acquireTurnAdmission(ctx, core.Event{})
	if err != nil {
		t.Fatalf("reentrant acquireTurnAdmission: %v", err)
	}
	if second != nil {
		t.Fatal("reentrant acquireTurnAdmission should not allocate another lease")
	}
}

func marshalRunTurnPayload(t *testing.T, payload core.ChatPayload) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
