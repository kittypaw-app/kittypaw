package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jinto/kittypaw/core"
)

// EvolutionProposal is the LLM's suggestion for SOUL.md changes.
type EvolutionProposal struct {
	NewSOUL string `json:"new_soul"`
	Reason  string `json:"reason"`
}

// TriggerEvolution checks conditions and generates a persona evolution
// proposal for the given profile.
func TriggerEvolution(
	ctx context.Context,
	profileID string,
	s *Session,
	config *core.EvolutionConfig,
) error {
	if err := core.ValidateStaffID(profileID); err != nil {
		return err
	}
	if !config.Enabled {
		return nil
	}

	// Check observation threshold.
	threshold := int(config.ObservationThreshold)
	if threshold == 0 {
		threshold = 20
	}
	msgCount, err := s.Store.CountUserMessagesTotal()
	if err != nil {
		return fmt.Errorf("count messages: %w", err)
	}
	if msgCount < threshold {
		slog.Debug("evolution: below observation threshold",
			"profile", profileID, "count", msgCount, "threshold", threshold)
		return nil
	}

	// Check for existing pending evolution.
	pendingKey := fmt.Sprintf("evolution:pending:%s", profileID)
	if _, exists, _ := s.Store.GetUserContext(pendingKey); exists {
		slog.Debug("evolution: pending proposal exists", "profile", profileID)
		return nil
	}

	// Load current SOUL.md.
	currentSOUL := loadSOUL(s.BaseDir, profileID)
	if currentSOUL == "" {
		currentSOUL = "(no SOUL.md found)"
	}

	// Collect patterns for analysis.
	topicPrefs, _ := s.Store.ListUserContextPrefix("topic_pref:")
	candidates, _ := s.Store.ListUserContextPrefix("suggest_candidate:")

	var patterns strings.Builder
	patterns.WriteString("## Topic Preferences\n")
	for _, tp := range topicPrefs {
		patterns.WriteString(fmt.Sprintf("- %s: %s\n", tp.Key, tp.Value))
	}
	patterns.WriteString("\n## Intent Suggestions\n")
	for _, c := range candidates {
		patterns.WriteString(fmt.Sprintf("- %s\n", c.Value))
	}

	// LLM analysis.
	prompt := fmt.Sprintf(`현재 페르소나 정의(SOUL.md)와 사용자 패턴 데이터를 분석하여,
페르소나를 더 맞춤화할 수 있는 진화 제안을 만들어주세요.

현재 SOUL.md:
%s

사용자 패턴:
%s

JSON으로 응답하세요 (마크다운 펜스 없이):
{"new_soul": "새로운 SOUL.md 전체 내용", "reason": "변경 이유"}

- 기존 페르소나의 핵심은 유지하면서 사용자 패턴에 맞게 조정하세요.
- JSON만 출력하세요.`, currentSOUL, patterns.String())

	resp, err := s.Provider.Generate(WithLLMCallKind(ctx, "evolution"), []core.LlmMessage{
		{Role: core.RoleUser, Content: prompt},
	})
	if err != nil {
		return fmt.Errorf("evolution LLM failed: %w", err)
	}

	raw := strings.TrimSpace(resp.Content)
	raw = stripFences(raw)

	var proposal EvolutionProposal
	if err := json.Unmarshal([]byte(raw), &proposal); err != nil {
		slog.Warn("evolution: JSON parse failed", "error", err)
		return nil
	}

	// Store as pending — user must approve.
	data, _ := json.Marshal(proposal)
	_ = s.Store.SetUserContext(pendingKey, string(data), "evolution")

	slog.Info("evolution: proposal stored for review", "profile", profileID)
	return nil
}
