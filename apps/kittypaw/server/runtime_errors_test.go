package server

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/jinto/kittypaw/engine"
)

func TestRuntimeErrorHTTPStatusMapsLLMRateLimitTo429(t *testing.T) {
	status, message, ok := runtimeErrorHTTPStatus(fmt.Errorf("wrapped: %w", engine.ErrLLMRateLimitExceeded))
	if !ok {
		t.Fatal("runtimeErrorHTTPStatus ok = false, want true")
	}
	if status != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", status, http.StatusTooManyRequests)
	}
	if message == "" {
		t.Fatal("message is empty")
	}
}

func TestRuntimeErrorHTTPStatusMapsDailyTokenLimitTo429(t *testing.T) {
	status, message, ok := runtimeErrorHTTPStatus(fmt.Errorf("wrapped: %w", engine.ErrDailyTokenLimitExceeded))
	if !ok {
		t.Fatal("runtimeErrorHTTPStatus ok = false, want true")
	}
	if status != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", status, http.StatusTooManyRequests)
	}
	if message == "" {
		t.Fatal("message is empty")
	}
}
