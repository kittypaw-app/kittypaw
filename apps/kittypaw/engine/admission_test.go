package engine

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRuntimeAdmissionAccountLimitBusy(t *testing.T) {
	a := NewRuntimeAdmission(RuntimeAdmissionConfig{
		MaxConcurrentAccount: 1,
		MaxQueuedAccount:     0,
		MaxConcurrentScope:   0,
	})
	lease, err := a.Acquire(context.Background(), RuntimeAdmissionRequest{
		AccountID: "alice",
		ScopeKey:  "general:web_chat:one",
		Class:     AdmissionForeground,
	})
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer lease.Release()

	_, err = a.Acquire(context.Background(), RuntimeAdmissionRequest{
		AccountID: "alice",
		ScopeKey:  "general:web_chat:two",
		Class:     AdmissionForeground,
	})
	if !errors.Is(err, ErrRuntimeAdmissionBusy) {
		t.Fatalf("second Acquire err = %v, want ErrRuntimeAdmissionBusy", err)
	}
}

func TestRuntimeAdmissionQueuesUntilRelease(t *testing.T) {
	a := NewRuntimeAdmission(RuntimeAdmissionConfig{
		MaxConcurrentAccount: 1,
		MaxQueuedAccount:     1,
		MaxConcurrentScope:   0,
	})
	first, err := a.Acquire(context.Background(), RuntimeAdmissionRequest{AccountID: "alice", ScopeKey: "one"})
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}

	acquired := make(chan error, 1)
	go func() {
		lease, err := a.Acquire(context.Background(), RuntimeAdmissionRequest{AccountID: "alice", ScopeKey: "two"})
		if err == nil {
			lease.Release()
		}
		acquired <- err
	}()

	waitUntilAdmissionQueued(t, a, 1)
	select {
	case err := <-acquired:
		t.Fatalf("second acquire finished before release: %v", err)
	default:
	}

	first.Release()
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatalf("second Acquire after release: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second Acquire did not finish after release")
	}
}

func TestRuntimeAdmissionScopeLimitBusy(t *testing.T) {
	a := NewRuntimeAdmission(RuntimeAdmissionConfig{
		MaxConcurrentAccount: 2,
		MaxQueuedAccount:     0,
		MaxConcurrentScope:   1,
	})
	first, err := a.Acquire(context.Background(), RuntimeAdmissionRequest{
		AccountID: "alice",
		ScopeKey:  "general:slack:C123",
	})
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer first.Release()

	_, err = a.Acquire(context.Background(), RuntimeAdmissionRequest{
		AccountID: "alice",
		ScopeKey:  "general:slack:C123",
	})
	if !errors.Is(err, ErrRuntimeAdmissionBusy) {
		t.Fatalf("same-scope Acquire err = %v, want ErrRuntimeAdmissionBusy", err)
	}

	second, err := a.Acquire(context.Background(), RuntimeAdmissionRequest{
		AccountID: "alice",
		ScopeKey:  "general:slack:C999",
	})
	if err != nil {
		t.Fatalf("different-scope Acquire: %v", err)
	}
	second.Release()
}

func TestRuntimeAdmissionAccountQueueCapsScopeWaiters(t *testing.T) {
	a := NewRuntimeAdmission(RuntimeAdmissionConfig{
		MaxConcurrentAccount: 1,
		MaxQueuedAccount:     1,
		MaxConcurrentScope:   1,
	})
	first, err := a.Acquire(context.Background(), RuntimeAdmissionRequest{
		AccountID: "alice",
		ScopeKey:  "general:slack:C123",
	})
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer first.Release()

	secondDone := make(chan error, 1)
	go func() {
		lease, err := a.Acquire(context.Background(), RuntimeAdmissionRequest{
			AccountID: "alice",
			ScopeKey:  "general:slack:C123",
		})
		if err == nil {
			lease.Release()
		}
		secondDone <- err
	}()

	waitUntilAdmissionQueued(t, a, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = a.Acquire(ctx, RuntimeAdmissionRequest{
		AccountID: "alice",
		ScopeKey:  "general:slack:C999",
	})
	if !errors.Is(err, ErrRuntimeAdmissionBusy) {
		t.Fatalf("third Acquire err = %v, want ErrRuntimeAdmissionBusy", err)
	}
}

func waitUntilAdmissionQueued(t *testing.T, a *RuntimeAdmission, want uint32) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if got := a.Snapshot().AccountQueued; got == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("AccountQueued did not reach %d, snapshot=%#v", want, a.Snapshot())
}
