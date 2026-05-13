package core

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// newFanoutFixture wires a Fanout against a small registry. Tests that
// need the real AccountRouter downstream live in the server package — this
// stays focused on the publishing contract.
func newFanoutFixture(t *testing.T, source string, members []string, peers ...string) (*ChannelFanout, chan Event, *AccountRegistry) {
	t.Helper()
	reg := NewAccountRegistry(t.TempDir(), source)
	reg.Register(&Account{ID: source, Config: &Config{
		IsShared:  true,
		TeamSpace: TeamSpaceConfig{Members: members},
	}})
	for _, id := range peers {
		reg.Register(&Account{ID: id, Config: &Config{}})
	}
	eventCh := make(chan Event, 4)
	return NewChannelFanout(eventCh, reg, source), eventCh, reg
}

// TestChannelFanout_SendEmitsEvent pins the wire shape of a fanout push.
// The whole cross-account flow assumes team_space.push events arrive at the
// AccountRouter carrying the *target* account's ID (not the sender's) —
// reversing the direction would dispatch the push back into the team-space
// AccountRuntime, looping.
func TestChannelFanout_SendEmitsEvent(t *testing.T) {
	f, ch, _ := newFanoutFixture(t, "team", []string{"alice"}, "alice")

	err := f.Send(context.Background(), "alice", FanoutPayload{Text: "비 온대!"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	select {
	case ev := <-ch:
		if ev.Type != EventTeamSpacePush {
			t.Errorf("type = %q, want %q", ev.Type, EventTeamSpacePush)
		}
		if ev.AccountID != "alice" {
			t.Errorf("target account = %q, want alice", ev.AccountID)
		}
		var p FanoutPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			t.Fatalf("payload unmarshal: %v", err)
		}
		if p.Text != "비 온대!" {
			t.Errorf("payload text = %q", p.Text)
		}
	case <-time.After(time.Second):
		t.Fatal("event not delivered")
	}
}

// TestChannelFanout_RejectsSelfTarget blocks the self-loop — if a team space
// sends to itself, AccountRouter would dispatch the push into the team-space
// AccountRuntime, which would (if it has a skill handling team_space.push)
// run again and potentially fanout again. Refuse at the boundary.
func TestChannelFanout_RejectsSelfTarget(t *testing.T) {
	f, _, _ := newFanoutFixture(t, "team", []string{"alice"}, "alice")
	err := f.Send(context.Background(), "team", FanoutPayload{Text: "x"})
	if !errors.Is(err, ErrFanoutSelfTarget) {
		t.Errorf("expected ErrFanoutSelfTarget, got %v", err)
	}
}

// TestChannelFanout_RejectsUnknownTarget keeps account IDs honest —
// pushing to a non-registered ID is a skill bug (typo) or hostile. Either
// way, return a clear error so the skill author sees the problem
// immediately instead of silently dropping messages.
func TestChannelFanout_RejectsUnknownTarget(t *testing.T) {
	f, _, _ := newFanoutFixture(t, "team", []string{"alice"}, "alice")
	err := f.Send(context.Background(), "bob", FanoutPayload{Text: "x"})
	if !errors.Is(err, ErrFanoutUnknownAccount) {
		t.Errorf("expected ErrFanoutUnknownAccount, got %v", err)
	}
}

func TestChannelFanout_RejectsNonMemberTarget(t *testing.T) {
	f, _, _ := newFanoutFixture(t, "team", []string{"alice"}, "alice", "bob")
	err := f.Send(context.Background(), "bob", FanoutPayload{Text: "x"})
	if !errors.Is(err, ErrFanoutUnauthorizedTarget) {
		t.Errorf("expected ErrFanoutUnauthorizedTarget, got %v", err)
	}
}

// TestChannelFanout_InvalidAccountID rejects hostile account names at the
// fanout boundary for the same reason ValidateAccountID rejects them at
// intake — the AccountID doubles as a filesystem key downstream, so
// traversal/case-collisions must fail here even if they'd also fail later.
func TestChannelFanout_InvalidAccountID(t *testing.T) {
	f, _, _ := newFanoutFixture(t, "team", nil)
	err := f.Send(context.Background(), "../evil", FanoutPayload{Text: "x"})
	if err == nil {
		t.Fatal("expected validation error for hostile account id")
	}
}

// TestChannelFanout_Broadcast sends to configured team-space members in config
// order. Skill authors write `Fanout.broadcast({text: ...})` for daily morning
// briefs — this is the main usage pattern so it must land on every member.
func TestChannelFanout_Broadcast(t *testing.T) {
	f, ch, _ := newFanoutFixture(t, "team", []string{"alice", "bob"}, "alice", "bob")

	if err := f.Broadcast(context.Background(), FanoutPayload{Text: "hi"}); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	var got []string
	for i := 0; i < 2; i++ {
		select {
		case ev := <-ch:
			got = append(got, ev.AccountID)
		case <-time.After(time.Second):
			t.Fatalf("only %d events received, want 2", i)
		}
	}
	if len(got) != 2 || got[0] != "alice" || got[1] != "bob" {
		t.Errorf("broadcast targets = %v, want alice+bob", got)
	}
	for _, id := range got {
		if id == "team" {
			t.Error("broadcast must exclude source account")
		}
	}
	select {
	case ev := <-ch:
		t.Fatalf("unexpected extra event: %#v", ev)
	default:
	}
}

func TestChannelFanout_BroadcastSendsOnlyToMembers(t *testing.T) {
	f, ch, _ := newFanoutFixture(t, "team", []string{"alice"}, "alice", "bob")

	if err := f.Broadcast(context.Background(), FanoutPayload{Text: "hi"}); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	select {
	case ev := <-ch:
		if ev.AccountID != "alice" {
			t.Fatalf("broadcast target = %q, want alice", ev.AccountID)
		}
	case <-time.After(time.Second):
		t.Fatal("event not delivered")
	}
	select {
	case ev := <-ch:
		t.Fatalf("unexpected second event: %#v", ev)
	default:
	}
}

func TestChannelFanout_BroadcastUnknownMemberEmitsNoEvents(t *testing.T) {
	f, ch, _ := newFanoutFixture(t, "team", []string{"alice", "ghost"}, "alice")

	err := f.Broadcast(context.Background(), FanoutPayload{Text: "hi"})
	if !errors.Is(err, ErrFanoutUnknownAccount) {
		t.Fatalf("Broadcast err = %v, want ErrFanoutUnknownAccount", err)
	}
	assertNoFanoutEvent(t, ch)
}

func TestChannelFanout_BroadcastDeduplicatesMembers(t *testing.T) {
	f, ch, _ := newFanoutFixture(t, "team", []string{"alice", "alice"}, "alice")

	if err := f.Broadcast(context.Background(), FanoutPayload{Text: "hi"}); err != nil {
		t.Fatalf("Broadcast: %v", err)
	}

	select {
	case ev := <-ch:
		if ev.AccountID != "alice" {
			t.Fatalf("broadcast target = %q, want alice", ev.AccountID)
		}
	case <-time.After(time.Second):
		t.Fatal("event not delivered")
	}
	assertNoFanoutEvent(t, ch)
}

func TestChannelFanout_BroadcastInvalidMemberIDEmitsNoEvents(t *testing.T) {
	f, ch, _ := newFanoutFixture(t, "team", []string{"alice", "../evil"}, "alice")

	err := f.Broadcast(context.Background(), FanoutPayload{Text: "hi"})
	if err == nil {
		t.Fatal("expected validation error")
	}
	assertNoFanoutEvent(t, ch)
}

func TestChannelFanout_BroadcastSelfMemberEmitsNoEvents(t *testing.T) {
	f, ch, _ := newFanoutFixture(t, "team", []string{"alice", "team"}, "alice")

	err := f.Broadcast(context.Background(), FanoutPayload{Text: "hi"})
	if !errors.Is(err, ErrFanoutSelfTarget) {
		t.Fatalf("Broadcast err = %v, want ErrFanoutSelfTarget", err)
	}
	assertNoFanoutEvent(t, ch)
}

func assertNoFanoutEvent(t *testing.T, ch <-chan Event) {
	t.Helper()
	select {
	case ev := <-ch:
		t.Fatalf("unexpected event: %#v", ev)
	default:
	}
}

// TestChannelFanout_ContextCancelled enforces cooperative cancellation —
// a buffered eventCh that is full must not wedge the caller forever.
// The sandbox call site will pass the skill's execution context, so a
// shutdown cleanly unblocks the goja VM.
func TestChannelFanout_ContextCancelled(t *testing.T) {
	reg := NewAccountRegistry(t.TempDir(), "family")
	reg.Register(&Account{ID: "family", Config: &Config{
		IsShared:  true,
		TeamSpace: TeamSpaceConfig{Members: []string{"alice"}},
	}})
	reg.Register(&Account{ID: "alice", Config: &Config{}})
	full := make(chan Event) // unbuffered; send will block forever
	f := NewChannelFanout(full, reg, "family")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := f.Send(ctx, "alice", FanoutPayload{Text: "x"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}
