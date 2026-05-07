package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/llm"
)

func TestTriggerEvolutionStoresPendingFromConversationPatterns(t *testing.T) {
	st := openTestStore(t)
	for i := 0; i < 2; i++ {
		if err := st.AddConversationTurn(&core.ConversationTurn{
			Role:      core.RoleUser,
			Content:   "나는 재무 리포트를 자주 봐",
			Timestamp: core.NowTimestamp(),
		}); err != nil {
			t.Fatalf("seed conversation turn: %v", err)
		}
	}
	_ = st.SetUserContext("topic_pref:재무", "1.00", "reflection")
	_ = st.SetUserContext("suggest_candidate:finance", "재무 리포트|3|0 8 * * 1", "reflection")

	base := t.TempDir()
	sess := &Session{
		Store:   st,
		BaseDir: base,
		Provider: &mockProvider{responses: []*llm.Response{mockResp(`{
			"new_soul":"FINANCE_EVOLVED_SOUL",
			"reason":"사용자가 재무 리포트를 반복적으로 요청함"
		}`)}},
	}
	cfg := &core.EvolutionConfig{Enabled: true, ObservationThreshold: 2}

	if err := TriggerEvolution(context.Background(), "default", sess, cfg); err != nil {
		t.Fatalf("TriggerEvolution error: %v", err)
	}
	if raw, ok, err := st.GetUserContext("evolution:pending:default"); err != nil || !ok || !strings.Contains(raw, "FINANCE_EVOLVED_SOUL") {
		t.Fatalf("pending evolution = %q ok=%v err=%v", raw, ok, err)
	}

	// Applying/rejecting the pending proposal is intentionally not asserted
	// here: the CLI approval surface was removed when staff management moved
	// into chat/server flows. Keep CI deterministic by locking the pending
	// contract and track the approval UX in apps/kittypaw/TASKS.md.
}
