package store

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jinto/kittypaw/core"
)

func TestInboundEventsDedupeClaimAndComplete(t *testing.T) {
	st := openTestStore(t)
	event := inboundEventForTest(t, "telegram:update:42", "hello")

	id, inserted, err := st.EnqueueInboundEvent(event)
	if err != nil {
		t.Fatalf("enqueue inbound: %v", err)
	}
	if id == 0 || !inserted {
		t.Fatalf("first enqueue id=%d inserted=%v, want inserted row", id, inserted)
	}
	dupID, dupInserted, err := st.EnqueueInboundEvent(event)
	if err != nil {
		t.Fatalf("enqueue duplicate inbound: %v", err)
	}
	if dupInserted || dupID != id {
		t.Fatalf("duplicate enqueue id=%d inserted=%v, want existing id %d", dupID, dupInserted, id)
	}

	claimed, err := st.ClaimInboundEvents(10, time.Minute)
	if err != nil {
		t.Fatalf("claim inbound: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed = %+v, want one event", claimed)
	}
	if claimed[0].ID != id || claimed[0].Event.SourceEventID != "telegram:update:42" {
		t.Fatalf("claimed row = %+v, want source event id", claimed[0])
	}

	claimedAgain, err := st.ClaimInboundEvents(10, time.Minute)
	if err != nil {
		t.Fatalf("claim inbound again: %v", err)
	}
	if len(claimedAgain) != 0 {
		t.Fatalf("claimed again = %+v, want none while lease is active", claimedAgain)
	}

	if err := st.CompleteInboundEvent(id); err != nil {
		t.Fatalf("complete inbound: %v", err)
	}
	afterComplete, err := st.ClaimInboundEvents(10, time.Millisecond)
	if err != nil {
		t.Fatalf("claim after complete: %v", err)
	}
	if len(afterComplete) != 0 {
		t.Fatalf("after complete claimed = %+v, want none", afterComplete)
	}
}

func TestInboundEventsLeaseExpiryAllowsRetry(t *testing.T) {
	st := openTestStore(t)
	id, inserted, err := st.EnqueueInboundEvent(inboundEventForTest(t, "slack:envelope:1", "retry me"))
	if err != nil {
		t.Fatalf("enqueue inbound: %v", err)
	}
	if !inserted || id == 0 {
		t.Fatalf("enqueue id=%d inserted=%v, want inserted", id, inserted)
	}

	if claimed, err := st.ClaimInboundEvents(1, time.Millisecond); err != nil {
		t.Fatalf("claim inbound: %v", err)
	} else if len(claimed) != 1 {
		t.Fatalf("claimed = %+v, want one", claimed)
	}

	time.Sleep(5 * time.Millisecond)
	reclaimed, err := st.ClaimInboundEvents(1, time.Second)
	if err != nil {
		t.Fatalf("reclaim inbound: %v", err)
	}
	if len(reclaimed) != 1 || reclaimed[0].ID != id || reclaimed[0].Attempts != 2 {
		t.Fatalf("reclaimed = %+v, want same row on second attempt", reclaimed)
	}
}

func TestInboundEventsRefreshLeasePreventsRetry(t *testing.T) {
	st := openTestStore(t)
	id, inserted, err := st.EnqueueInboundEvent(inboundEventForTest(t, "discord:message:1", "keep alive"))
	if err != nil {
		t.Fatalf("enqueue inbound: %v", err)
	}
	if !inserted || id == 0 {
		t.Fatalf("enqueue id=%d inserted=%v, want inserted", id, inserted)
	}

	if claimed, err := st.ClaimInboundEvents(1, time.Millisecond); err != nil {
		t.Fatalf("claim inbound: %v", err)
	} else if len(claimed) != 1 {
		t.Fatalf("claimed = %+v, want one", claimed)
	}
	if err := st.RefreshInboundEventLease(id, time.Second); err != nil {
		t.Fatalf("refresh inbound lease: %v", err)
	}

	time.Sleep(5 * time.Millisecond)
	reclaimed, err := st.ClaimInboundEvents(1, time.Millisecond)
	if err != nil {
		t.Fatalf("claim after refresh: %v", err)
	}
	if len(reclaimed) != 0 {
		t.Fatalf("reclaimed after refresh = %+v, want none while refreshed lease is active", reclaimed)
	}
}

func inboundEventForTest(t *testing.T, sourceEventID, text string) core.Event {
	t.Helper()
	payload, err := json.Marshal(core.ChatPayload{
		ChatID:          "chat-1",
		Text:            text,
		SourceSessionID: "user-1",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return core.Event{
		Type:          core.EventTelegram,
		AccountID:     "alice",
		SourceEventID: sourceEventID,
		Payload:       payload,
	}
}
