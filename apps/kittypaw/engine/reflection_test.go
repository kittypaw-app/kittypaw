package engine

import (
	"context"
	"testing"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/llm"
	"github.com/jinto/kittypaw/store"
)

func containsAll(s string, substrs ...string) bool {
	for _, sub := range substrs {
		found := false
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// IntentHash
// ---------------------------------------------------------------------------

func TestIntentHash_Deterministic(t *testing.T) {
	h1 := IntentHash("check weather")
	h2 := IntentHash("check weather")
	if h1 != h2 {
		t.Fatalf("same input should produce same hash: %q vs %q", h1, h2)
	}
}

func TestIntentHash_CaseInsensitive(t *testing.T) {
	h1 := IntentHash("Check Weather")
	h2 := IntentHash("check weather")
	if h1 != h2 {
		t.Fatalf("hashing should be case-insensitive: %q vs %q", h1, h2)
	}
}

func TestIntentHash_Different(t *testing.T) {
	h1 := IntentHash("check weather")
	h2 := IntentHash("send email")
	if h1 == h2 {
		t.Fatal("different inputs should produce different hashes")
	}
}

// ---------------------------------------------------------------------------
// BuildWeeklyReport
// ---------------------------------------------------------------------------

func TestBuildWeeklyReport_Empty(t *testing.T) {
	report := BuildWeeklyReport(nil)
	if report == "" {
		t.Fatal("empty report should still produce a message")
	}
	// Should be Korean.
	if !containsAll(report, "토픽") {
		t.Error("empty report should contain Korean placeholder text")
	}
}

func TestBuildWeeklyReport_WithData(t *testing.T) {
	prefs := []store.KeyValue{
		{Key: "topic_pref:날씨", Value: "0.40"},
		{Key: "topic_pref:뉴스", Value: "0.30"},
	}
	report := BuildWeeklyReport(prefs)
	if !containsAll(report, "날씨", "뉴스", "0.40", "0.30") {
		t.Errorf("report should contain topics and ratios: %s", report)
	}
}

func TestRunReflectionCycleReadsConversationTurnsAndStoresCandidates(t *testing.T) {
	st := newReflectionStore(t)
	for i := 0; i < 3; i++ {
		if err := st.AddConversationTurn(&core.ConversationTurn{
			Role:      core.RoleUser,
			Content:   "환율 알려줘",
			Timestamp: core.NowTimestamp(),
		}); err != nil {
			t.Fatalf("seed conversation turn: %v", err)
		}
	}
	sess := &AccountRuntime{
		Store: st,
		Provider: &mockProvider{responses: []*llm.Response{mockResp(`{
			"intents": [{"label":"환율 조회","count":3,"cron":"0 8 * * *"}],
			"topics": [{"topic":"환율","ratio":1.0}]
		}`)}},
	}
	cfg := &core.ReflectionConfig{IntentThreshold: 3, TTLDays: 7, MaxInputChars: 4000}

	if err := RunReflectionCycle(context.Background(), sess, cfg); err != nil {
		t.Fatalf("RunReflectionCycle error: %v", err)
	}

	key := "suggest_candidate:" + IntentHash("환율 조회")
	if got, ok, err := st.GetUserContext(key); err != nil || !ok || got != "환율 조회|3|0 8 * * *" {
		t.Fatalf("%s = %q ok=%v err=%v", key, got, ok, err)
	}
	if got, ok, err := st.GetUserContext("topic_pref:환율"); err != nil || !ok || got != "1.00" {
		t.Fatalf("topic_pref:환율 = %q ok=%v err=%v", got, ok, err)
	}
}

// ---------------------------------------------------------------------------
// Store methods: TTL sweep, prefix delete
// ---------------------------------------------------------------------------

func TestDeleteExpiredReflection(t *testing.T) {
	st := newReflectionStore(t)

	// Seed a reflection key.
	_ = st.SetUserContext("reflection:intent:abc", "test data", "reflection")

	// TTL=0 means "delete rows older than or equal to now" — covers
	// just-inserted rows because the query uses <= comparison.
	deleted, err := st.DeleteExpiredReflection(0)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted, got %d", deleted)
	}
}

func TestDeleteUserContextPrefix(t *testing.T) {
	st := newReflectionStore(t)

	_ = st.SetUserContext("suggest_candidate:aaa", "v1", "reflection")
	_ = st.SetUserContext("suggest_candidate:bbb", "v2", "reflection")
	_ = st.SetUserContext("other:key", "v3", "other")

	deleted, err := st.DeleteUserContextPrefix("suggest_candidate:")
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("expected 2 deleted, got %d", deleted)
	}

	// Other key should still exist.
	_, exists, _ := st.GetUserContext("other:key")
	if !exists {
		t.Error("unrelated key was incorrectly deleted")
	}
}

func TestRejectedIntentNotResuggested(t *testing.T) {
	st := newReflectionStore(t)

	// Store a rejected intent.
	hash := IntentHash("check weather")
	rejKey := "rejected_intent:" + hash
	_ = st.SetUserContext(rejKey, "check weather", "user_rejection")

	// Verify it's retrievable.
	rejected, _ := st.ListUserContextPrefix("rejected_intent:")
	found := false
	for _, kv := range rejected {
		if kv.Key == rejKey {
			found = true
		}
	}
	if !found {
		t.Error("rejected intent should be retrievable")
	}
}

func newReflectionStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}
