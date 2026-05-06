package connect

import (
	"testing"
	"time"
)

func TestPreauthStoreConsumesOnce(t *testing.T) {
	store := NewPreauthStore(PreauthStoreOptions{
		TTL:        time.Minute,
		MaxEntries: 10,
	})
	session := PreauthSession{
		UserID:   "user-1",
		Provider: XProviderID,
		Mode:     "code",
	}
	code, err := store.Create(session)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.Consume(code)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if got != session {
		t.Fatalf("session = %#v, want %#v", got, session)
	}

	if _, err := store.Consume(code); err == nil {
		t.Fatal("second Consume succeeded, want error")
	}
}

func TestPreauthStoreExpires(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	store := NewPreauthStore(PreauthStoreOptions{
		TTL:        time.Minute,
		MaxEntries: 10,
		Now: func() time.Time {
			return now
		},
	})
	code, err := store.Create(PreauthSession{
		UserID:   "user-1",
		Provider: XProviderID,
		Mode:     "http",
		Port:     "12345",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	now = now.Add(time.Minute + time.Nanosecond)
	if _, err := store.Consume(code); err == nil {
		t.Fatal("Consume after TTL succeeded, want error")
	}
}
