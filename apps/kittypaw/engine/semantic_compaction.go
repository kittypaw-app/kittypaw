package engine

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/llm"
	"github.com/jinto/kittypaw/store"
)

const (
	semanticCompactionTranscriptLimit = 32000
	semanticCompactionContentLimit    = 1200
	semanticCompactionResultLimit     = 500
	semanticCompactionTailBudget      = semanticCompactionTranscriptLimit / 2
)

const semanticCompactionSystemPrompt = `You summarize older conversation turns so a future assistant can continue the same work without seeing the original turns.

Preserve concrete continuity:
- current user goal and scope
- user decisions, constraints, preferences, and corrections
- important facts discovered
- errors, failed approaches, and fixes
- completed work
- pending tasks and next steps
- current state needed to resume

Do not include secrets, credentials, raw tool arguments, raw tool results, long logs, or full source files.
Do not invent facts. If something is uncertain, say so briefly.
Use the user's primary language when clear.
Return only the summary, with concise section headings.`

func compactConversationWithSemanticSummary(ctx context.Context, s *AccountRuntime, conversationID string, keepRecent int) (int, error) {
	if s == nil || s.Store == nil {
		return 0, fmt.Errorf("compact 실행을 위한 store가 준비되지 않았습니다")
	}
	plan, err := s.Store.PrepareConversationCompaction(conversationID, keepRecent)
	if err != nil {
		return 0, err
	}
	if len(plan.OldTurns) == 0 {
		return 0, nil
	}

	summary := ""
	if s.Provider != nil {
		generated, err := generateSemanticCompactionSummary(ctx, s.Provider, plan)
		if err != nil {
			slog.Warn("semantic conversation compaction failed; using deterministic fallback",
				"conversation", plan.ConversationID,
				"end_turn_id", plan.EndTurnID,
				"error", err,
			)
		} else {
			summary = generated
		}
	}
	return s.Store.SaveConversationCompaction(plan, summary)
}

func generateSemanticCompactionSummary(ctx context.Context, provider llm.Provider, plan store.ConversationCompactionPlan) (string, error) {
	if provider == nil {
		return "", fmt.Errorf("provider is not configured")
	}
	transcript := buildSemanticCompactionTranscript(plan.OldTurns)
	if strings.TrimSpace(transcript) == "" {
		return "", fmt.Errorf("empty compaction transcript")
	}
	resp, err := provider.Generate(WithLLMCallKind(ctx, "semantic_compaction"), []core.LlmMessage{
		{Role: core.RoleSystem, Content: semanticCompactionSystemPrompt},
		{Role: core.RoleUser, Content: transcript},
	})
	if err != nil {
		return "", err
	}
	summary := strings.TrimSpace(resp.Content)
	if summary == "" {
		return "", fmt.Errorf("empty semantic compaction summary")
	}
	return summary, nil
}

func buildSemanticCompactionTranscript(records []store.ConversationTurnRecord) string {
	blocks := make([]string, 0, len(records))
	for _, rec := range records {
		if block := semanticCompactionRecordBlock(rec); strings.TrimSpace(block) != "" {
			blocks = append(blocks, block)
		}
	}
	if len(blocks) == 0 {
		return ""
	}

	tailStart := len(blocks)
	tailLen := 0
	for i := len(blocks) - 1; i >= 0; i-- {
		blockLen := len(blocks[i])
		if tailLen > 0 && tailLen+blockLen > semanticCompactionTailBudget {
			break
		}
		tailStart = i
		tailLen += blockLen
		if tailLen >= semanticCompactionTailBudget {
			break
		}
	}
	if tailStart == 0 {
		return joinCompactionBlocksWithinLimit(blocks, semanticCompactionTranscriptLimit)
	}

	marker := "\n[older compacted transcript truncated; latest compacted turns preserved below]\n\n"
	headLimit := semanticCompactionTranscriptLimit - tailLen - len(marker)
	if headLimit < 0 {
		headLimit = 0
	}
	var sb strings.Builder
	for _, block := range blocks[:tailStart] {
		if sb.Len()+len(block) > headLimit {
			break
		}
		sb.WriteString(block)
	}
	if sb.Len()+len(marker) <= semanticCompactionTranscriptLimit {
		sb.WriteString(marker)
	}
	for _, block := range blocks[tailStart:] {
		if sb.Len()+len(block) > semanticCompactionTranscriptLimit {
			break
		}
		sb.WriteString(block)
	}
	return sb.String()
}

func semanticCompactionRecordBlock(rec store.ConversationTurnRecord) string {
	var sb strings.Builder
	writeSemanticCompactionLine(&sb, fmt.Sprintf("TURN %d role=%s", rec.ID, rec.Role))
	if rec.StaffID != "" || rec.Channel != "" {
		writeSemanticCompactionLine(&sb, fmt.Sprintf("metadata: staff=%s channel=%s", rec.StaffID, rec.Channel))
	}
	if content := core.SafePromptSummarySnippet(rec.Content, semanticCompactionContentLimit); content != "" {
		writeSemanticCompactionLine(&sb, "content: "+content)
	}
	if result := summarizeExecutionResultForCompaction(rec.Result); result != "" {
		writeSemanticCompactionLine(&sb, "execution_result: "+result)
	}
	if traces := summarizeToolTraceUse(rec.ToolTraces); traces != "" {
		writeSemanticCompactionLine(&sb, "tool_trace_summary: "+traces)
	}
	writeSemanticCompactionLine(&sb, "")
	return sb.String()
}

func joinCompactionBlocksWithinLimit(blocks []string, limit int) string {
	var sb strings.Builder
	for _, block := range blocks {
		if sb.Len()+len(block) > limit {
			break
		}
		sb.WriteString(block)
	}
	return sb.String()
}

func writeSemanticCompactionLine(sb *strings.Builder, line string) {
	if sb.Len() >= semanticCompactionTranscriptLimit {
		return
	}
	if sb.Len()+len(line)+1 > semanticCompactionTranscriptLimit {
		if sb.Len()+len("[transcript truncated]\n") <= semanticCompactionTranscriptLimit {
			sb.WriteString("[transcript truncated]\n")
		}
		return
	}
	sb.WriteString(line)
	sb.WriteByte('\n')
}

func summarizeExecutionResultForCompaction(result string) string {
	result = strings.TrimSpace(result)
	if result == "" {
		return ""
	}
	lower := strings.ToLower(result)
	switch {
	case strings.HasPrefix(lower, "output:"):
		return "success"
	case strings.HasPrefix(lower, "error:"):
		safe := core.SafePromptSummarySnippet(strings.TrimSpace(result[len("error:"):]), semanticCompactionResultLimit)
		if safe == "" {
			return "error"
		}
		return "error: " + safe
	default:
		return core.SafePromptSummarySnippet(result, semanticCompactionResultLimit)
	}
}

func summarizeToolTraceUse(traces []core.ToolTrace) string {
	if len(traces) == 0 {
		return ""
	}
	counts := make(map[string]int)
	failures := 0
	for _, trace := range traces {
		name := strings.TrimSpace(trace.SkillName + "." + trace.Method)
		if name == "." {
			name = "unknown"
		}
		counts[name]++
		if !trace.Success {
			failures++
		}
	}
	parts := make([]string, 0, len(counts)+1)
	for name, count := range counts {
		parts = append(parts, fmt.Sprintf("%s=%d", name, count))
	}
	sort.Strings(parts)
	if failures > 0 {
		parts = append(parts, fmt.Sprintf("failures=%d", failures))
	}
	return strings.Join(parts, ", ")
}
