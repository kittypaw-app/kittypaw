package engine

import (
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/store"
)

func TestCompactTurnsOldZonePreservesUserIntentAndCorrections(t *testing.T) {
	turns := []core.ConversationTurn{
		{Role: core.RoleUser, Content: "Goal: move Gemini and ChatGPT calls behind a provider boundary."},
		{Role: core.RoleAssistant, Content: "I will update the handlers."},
		{Role: core.RoleUser, Content: "Correction: do not patch each handler; make the provider boundary structural."},
		{Role: core.RoleAssistant, Content: "Understood.", Result: "error: old handler patch failed"},
		{Role: core.RoleUser, Content: "recent middle message"},
		{Role: core.RoleAssistant, Content: "recent full message"},
	}

	messages := CompactTurns(turns, CompactionConfig{
		RecentWindow: 1,
		MiddleWindow: 1,
		TruncateLen:  20,
	})
	if len(messages) == 0 || messages[0].Role != core.RoleSystem {
		t.Fatalf("first message = %+v, want old-zone system summary", messages)
	}
	summary := messages[0].Content
	for _, want := range []string{
		"provider boundary",
		"Correction",
		"old handler patch failed",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, summary)
		}
	}
}

func TestCompactTurnsOldZoneSkipsSecretLookingSnippets(t *testing.T) {
	turns := []core.ConversationTurn{
		{Role: core.RoleUser, Content: "The API key is sk-secret-1234567890 and should not be repeated."},
		{Role: core.RoleUser, Content: "Goal: continue the provider migration without leaking credentials."},
		{Role: core.RoleAssistant, Content: "Understood."},
		{Role: core.RoleUser, Content: "recent middle message"},
		{Role: core.RoleAssistant, Content: "recent full message"},
	}

	messages := CompactTurns(turns, CompactionConfig{
		RecentWindow: 1,
		MiddleWindow: 1,
		TruncateLen:  20,
	})
	if len(messages) == 0 {
		t.Fatal("CompactTurns returned no messages")
	}
	summary := messages[0].Content
	if strings.Contains(summary, "sk-secret") || strings.Contains(summary, "API key") {
		t.Fatalf("summary leaked secret-looking snippet:\n%s", summary)
	}
	if !strings.Contains(summary, "provider migration") {
		t.Fatalf("summary lost safe goal snippet:\n%s", summary)
	}
}

func TestSemanticCompactionTranscriptIncludesNewestOldTurnsAtSizeCap(t *testing.T) {
	var records []store.ConversationTurnRecord
	for i := 0; i < 80; i++ {
		content := strings.Repeat("older filler ", 140)
		if i == 79 {
			content = "TAIL_DECISION_KEEP: finish the structured provider boundary tests next."
		}
		records = append(records, store.ConversationTurnRecord{
			ID:      int64(i + 1),
			Role:    core.RoleUser,
			Content: content,
		})
	}

	transcript := buildSemanticCompactionTranscript(records)
	if !strings.Contains(transcript, "TAIL_DECISION_KEEP") {
		t.Fatalf("transcript omitted newest compacted turn near recent window; len=%d", len(transcript))
	}
	if len(transcript) > semanticCompactionTranscriptLimit {
		t.Fatalf("transcript len = %d, want <= %d", len(transcript), semanticCompactionTranscriptLimit)
	}
}
