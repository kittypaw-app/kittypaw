package engine

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jinto/kittypaw/core"
)

const (
	promptRecentTurnContentLimit = 8000
	promptRecentSkillOutputLimit = 8000
	promptObservationDataLimit   = 12000
	promptTruncationMarkerFormat = "\n...[truncated, original_chars=%d]"
)

// CompactionConfig controls 3-stage context window management.
type CompactionConfig struct {
	RecentWindow int // recent turns kept in full
	MiddleWindow int // middle turns kept but truncated
	TruncateLen  int // max chars for truncated content
}

// DefaultCompaction returns the default compaction settings.
func DefaultCompaction() CompactionConfig {
	return CompactionConfig{
		RecentWindow: 20,
		MiddleWindow: 30,
		TruncateLen:  100,
	}
}

// CompactionForAttempt returns progressively tighter compaction for retries.
func CompactionForAttempt(attempt int) CompactionConfig {
	switch attempt {
	case 0:
		return DefaultCompaction()
	case 1:
		return CompactionConfig{RecentWindow: 10, MiddleWindow: 10, TruncateLen: 50}
	default:
		return CompactionConfig{RecentWindow: 5, MiddleWindow: 0, TruncateLen: 50}
	}
}

// EstimateTokens gives a rough token count. ASCII ~ 1 token/4 chars, CJK ~ 1 token/1.5 chars.
func EstimateTokens(text string) int {
	count := 0
	for _, r := range text {
		if r < 128 {
			count++
		} else {
			count += 2
		}
	}
	return count / 3
}

// CompactTurns applies 3-stage compaction to conversation turns.
//
// Stages:
//   - Old (beyond middle+recent): collapsed into a summary system message
//   - Middle: each turn kept but truncated to TruncateLen chars
//   - Recent (last RecentWindow): full content preserved
func CompactTurns(turns []core.ConversationTurn, cfg CompactionConfig) []core.LlmMessage {
	total := len(turns)
	recentStart := max(0, total-cfg.RecentWindow)
	middleStart := max(0, recentStart-cfg.MiddleWindow)

	oldZone := turns[:middleStart]
	middleZone := turns[middleStart:recentStart]
	recentZone := turns[recentStart:]

	var messages []core.LlmMessage

	// Stage 3: old zone → summary
	if len(oldZone) > 0 {
		messages = append(messages, summarizeOldTurns(oldZone))
	}

	// Stage 2: middle zone → truncated
	for i := range middleZone {
		if msg, ok := turnToMessage(&middleZone[i], cfg.TruncateLen); ok {
			messages = append(messages, msg)
		}
	}

	// Stage 1: recent zone → full
	for i := range recentZone {
		if msg, ok := turnToMessage(&recentZone[i], 0); ok {
			messages = append(messages, msg)
		}
	}

	return messages
}

func turnToMessage(turn *core.ConversationTurn, truncateTo int) (core.LlmMessage, bool) {
	switch turn.Role {
	case core.RoleSystem:
		return core.LlmMessage{}, false
	case core.RoleUser:
		content := turn.Content
		if turn.Result != "" {
			result := turn.Result
			if truncateTo > 0 {
				result = truncate(result, truncateTo)
			}
			content += fmt.Sprintf("\n[Previous result: %s]", result)
		}
		if truncateTo > 0 {
			content = truncate(content, truncateTo)
		} else {
			content = capPromptPayload(content, promptRecentTurnContentLimit)
		}
		return core.LlmMessage{Role: core.RoleUser, Content: content}, true
	case core.RoleAssistant:
		content := turn.Content
		if truncateTo > 0 {
			content = truncate(content, truncateTo)
		} else {
			content = capPromptPayload(content, promptRecentTurnContentLimit)
		}
		return core.LlmMessage{Role: core.RoleAssistant, Content: content}, true
	}
	return core.LlmMessage{}, false
}

func summarizeOldTurns(turns []core.ConversationTurn) core.LlmMessage {
	var userCount, assistantCount, codeCount, successCount, failureCount int
	var userSnippets []string
	var correctionSnippets []string
	var errorSnippets []string

	for i := range turns {
		switch turns[i].Role {
		case core.RoleUser:
			userCount++
			if snippet := compactionSummarySnippet(turns[i].Content); snippet != "" {
				userSnippets = appendLimited(userSnippets, snippet, 6)
				if looksLikeCorrection(snippet) {
					correctionSnippets = appendLimited(correctionSnippets, snippet, 4)
				}
			}
		case core.RoleAssistant:
			assistantCount++
			if turns[i].Code != "" {
				codeCount++
			}
			r := strings.ToLower(turns[i].Result)
			if strings.Contains(r, "success") || strings.Contains(r, "output:") {
				successCount++
			} else if strings.Contains(r, "error") || strings.Contains(r, "fail") {
				failureCount++
				if snippet := compactionSummarySnippet(turns[i].Result); snippet != "" {
					errorSnippets = appendLimited(errorSnippets, snippet, 4)
				}
			}
		}
	}

	total := userCount + assistantCount
	var sb strings.Builder
	fmt.Fprintf(&sb,
		"[이전 대화 요약] 지금까지 %d번 대화 (%d번 사용자, %d번 어시스턴트), 코드 실행 %d번, 성공 %d번, 실패 %d번.",
		total, userCount, assistantCount, codeCount, successCount, failureCount,
	)
	appendCompactionSummarySection(&sb, "사용자 목표/요청", userSnippets)
	appendCompactionSummarySection(&sb, "사용자 수정/제약", correctionSnippets)
	appendCompactionSummarySection(&sb, "오류/실패", errorSnippets)
	return core.LlmMessage{
		Role:    core.RoleSystem,
		Content: sb.String(),
	}
}

func appendCompactionSummarySection(sb *strings.Builder, title string, items []string) {
	if len(items) == 0 {
		return
	}
	sb.WriteString("\n")
	sb.WriteString(title)
	sb.WriteString(":")
	for _, item := range items {
		sb.WriteString("\n- ")
		sb.WriteString(item)
	}
}

func appendLimited(items []string, item string, limit int) []string {
	if item == "" || len(items) >= limit {
		return items
	}
	for _, existing := range items {
		if existing == item {
			return items
		}
	}
	return append(items, item)
}

func compactionSummarySnippet(text string) string {
	return core.SafePromptSummarySnippet(text, 180)
}

func looksLikeCorrection(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range []string{
		"correction",
		"constraint",
		"do not",
		"don't",
		"수정",
		"아니",
		"하지 말",
		"말고",
		"제약",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func truncate(s string, maxLen int) string {
	if utf8.RuneCountInString(s) <= maxLen {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxLen]) + "…"
}

func capPromptPayload(s string, maxRunes int) string {
	if maxRunes <= 0 || utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	marker := fmt.Sprintf(promptTruncationMarkerFormat, len(runes))
	markerRunes := []rune(marker)
	if len(markerRunes) >= maxRunes {
		return string(markerRunes[:maxRunes])
	}
	return string(runes[:maxRunes-len(markerRunes)]) + marker
}
