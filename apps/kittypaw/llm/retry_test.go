package llm

import (
	"testing"
	"time"
)

func TestParseRetryAfterDelaySeconds(t *testing.T) {
	delay, ok := parseRetryAfterDelay("3", time.Unix(0, 0))
	if !ok {
		t.Fatal("parseRetryAfterDelay ok = false, want true")
	}
	if delay != 3*time.Second {
		t.Fatalf("delay = %s, want 3s", delay)
	}
}

func TestParseRetryAfterDelayHTTPDate(t *testing.T) {
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	delay, ok := parseRetryAfterDelay("Thu, 14 May 2026 12:00:05 GMT", now)
	if !ok {
		t.Fatal("parseRetryAfterDelay ok = false, want true")
	}
	if delay != 5*time.Second {
		t.Fatalf("delay = %s, want 5s", delay)
	}
}

func TestProviderRetryDelayCapsRetryAfter(t *testing.T) {
	delay := providerRetryDelay(time.Second, 1, "120", time.Unix(0, 0))
	if delay != providerRetryAfterMaxDelay {
		t.Fatalf("delay = %s, want %s", delay, providerRetryAfterMaxDelay)
	}
}
