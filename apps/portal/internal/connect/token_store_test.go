package connect

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestTokenCipherRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	cipher, err := NewTokenCipher(key)
	if err != nil {
		t.Fatalf("NewTokenCipher: %v", err)
	}

	encrypted, err := cipher.Encrypt("secret-token")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Contains(encrypted, []byte("secret-token")) {
		t.Fatalf("encrypted token contains plaintext: %q", string(encrypted))
	}

	got, err := cipher.Decrypt(encrypted)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if got != "secret-token" {
		t.Fatalf("Decrypt = %q, want secret-token", got)
	}
}

func TestTokenCipherRejectsBadKey(t *testing.T) {
	if _, err := NewTokenCipher([]byte("short")); err == nil {
		t.Fatal("expected bad key error")
	}
}

func TestMemoryConnectTokenStoreSavesAndLoadsToken(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	store := NewMemoryTokenStore(now)
	expiresAt := now.Add(2 * time.Hour)
	if err := store.SaveProviderToken(context.Background(), ProviderTokenRecord{
		UserID:       "user-1",
		ProviderID:   XProviderID,
		AccessToken:  "x-access",
		RefreshToken: "x-refresh",
		TokenType:    "bearer",
		Scope:        XReadOnlyScope,
		Username:     "jaypark",
		ExpiresAt:    &expiresAt,
	}); err != nil {
		t.Fatalf("SaveProviderToken: %v", err)
	}

	got, err := store.LoadProviderToken(context.Background(), "user-1", XProviderID)
	if err != nil {
		t.Fatalf("LoadProviderToken: %v", err)
	}
	if got.AccessToken != "x-access" || got.RefreshToken != "x-refresh" || got.Username != "jaypark" {
		t.Fatalf("loaded token = %#v", got)
	}
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("ExpiresAt = %v, want %v", got.ExpiresAt, expiresAt)
	}
}

func TestMemoryConnectTokenStoreUsageQuota(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	store := NewMemoryTokenStore(now)
	allowed, err := store.RecordUsage(context.Background(), UsageRecord{
		UserID:       "user-1",
		ProviderID:   XProviderID,
		Operation:    "search_recent",
		Quantity:     3,
		MonthlyLimit: 5,
		Now:          now,
	})
	if err != nil || !allowed {
		t.Fatalf("first RecordUsage allowed=%v err=%v, want allowed", allowed, err)
	}

	allowed, err = store.RecordUsage(context.Background(), UsageRecord{
		UserID:       "user-1",
		ProviderID:   XProviderID,
		Operation:    "search_recent",
		Quantity:     3,
		MonthlyLimit: 5,
		Now:          now,
	})
	if err != nil || allowed {
		t.Fatalf("second RecordUsage allowed=%v err=%v, want over quota", allowed, err)
	}
}

func TestMemoryConnectTokenStoreUnlimitedWhenMonthlyLimitMissing(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	store := NewMemoryTokenStore(now)
	allowed, err := store.RecordUsage(context.Background(), UsageRecord{
		UserID:     "user-1",
		ProviderID: XProviderID,
		Operation:  "search_recent",
		Quantity:   1000,
		Now:        now,
	})
	if err != nil || !allowed {
		t.Fatalf("RecordUsage allowed=%v err=%v, want unlimited allowed", allowed, err)
	}
}

func TestUsageAdvisoryLockKeyIsPostgresTextSafe(t *testing.T) {
	key := usageAdvisoryLockKey("user-1", XProviderID, "post_reads")
	if strings.ContainsRune(key, '\x00') {
		t.Fatalf("usage advisory lock key contains NUL: %q", key)
	}
	if key != "user-1|x|post_reads" {
		t.Fatalf("usage advisory lock key = %q", key)
	}
}
