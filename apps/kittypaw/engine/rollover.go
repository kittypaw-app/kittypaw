package engine

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/llm"
	"github.com/jinto/kittypaw/store"
)

const (
	rolloverReasonLengthTurns  = "length_turns"
	rolloverReasonLengthTokens = "length_tokens"
	rolloverNoticeHeader       = "* Conversation rolled over"

	rolloverMemoryMinConfidence = 0.75
	rolloverMemoryValueLimit    = 500
	rolloverMemoryKeySlugLimit  = 48
	rolloverDistillTurnLimit    = 40
)

type rolloverPolicy struct {
	Enabled                 bool
	MaxTurns                int
	MaxEstimatedTokensRatio float64
	MinTurnsBeforeRollover  int
}

var defaultRolloverPolicy = rolloverPolicy{
	Enabled:                 true,
	MaxTurns:                80,
	MaxEstimatedTokensRatio: 0.65,
	MinTurnsBeforeRollover:  20,
}

type conversationResolution struct {
	ConversationID string
	Route          *store.ConversationRoute
	RolledOver     bool
	Notice         string
}

var rolloverAllowedMemoryCategories = map[string]bool{
	"preference":    true,
	"decision":      true,
	"ongoing_task":  true,
	"open_question": true,
	"state":         true,
}

type rolloverMemory struct {
	Category   string  `json:"category"`
	Key        string  `json:"key"`
	Value      string  `json:"value"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}

type rolloverMemoryResponse struct {
	Memories []rolloverMemory `json:"memories"`
}

func resolveConversationForEvent(ctx context.Context, s *AccountRuntime, event *core.Event, provider llm.Provider) (conversationResolution, error) {
	fallback := conversationKeyForEvent(s, event)
	resolution := conversationResolution{ConversationID: fallback}
	if s == nil || s.Store == nil || event == nil {
		return resolution, nil
	}
	payload, err := event.ParsePayload()
	if err != nil {
		return resolution, nil
	}
	if explicit := strings.TrimSpace(payload.ConversationID); explicit != "" {
		resolution.ConversationID = explicit
		return resolution, nil
	}
	routeKey, route := conversationRouteKey(event.Type, payload)
	if routeKey == "" {
		return resolution, nil
	}
	if existing, ok, err := s.Store.ConversationRoute(routeKey); err != nil {
		return resolution, err
	} else if ok {
		resolution.ConversationID = existing.ConversationID
		resolution.Route = existing
		return maybeRolloverConversation(ctx, s, resolution, provider)
	}

	if selected, ok, err := existingConversationKeyFromPayload(s, payload, false); err != nil {
		return resolution, err
	} else if ok {
		resolution.ConversationID = selected
		return resolution, nil
	}

	conversationID := sourceConversationKey(event.Type, payload)
	if conversationID == "" {
		conversationID = store.DefaultConversationID
	}
	scopeID := strings.TrimPrefix(conversationID, "general:")
	if err := s.Store.EnsureConversation(conversationID, "general", scopeID); err != nil {
		return resolution, err
	}
	route.ConversationID = conversationID
	if err := s.Store.UpsertConversationRoute(route); err != nil {
		return resolution, err
	}
	created, ok, err := s.Store.ConversationRoute(routeKey)
	if err != nil {
		return resolution, err
	}
	if ok {
		resolution.ConversationID = created.ConversationID
		resolution.Route = created
		return maybeRolloverConversation(ctx, s, resolution, provider)
	}
	resolution.ConversationID = conversationID
	resolution.Route = &route
	return maybeRolloverConversation(ctx, s, resolution, provider)
}

func conversationRouteKey(eventType core.EventType, payload core.ChatPayload) (string, store.ConversationRoute) {
	source := conversationKeyPart(string(eventType))
	if source == "" {
		return "", store.ConversationRoute{}
	}
	stableID := strings.TrimSpace(payload.ChatID)
	switch eventType {
	case core.EventKakaoTalk, core.EventWebChat, core.EventDesktop:
		stableID = firstNonEmptyConversationValue(payload.SourceSessionID, payload.ChatID)
	default:
		stableID = firstNonEmptyConversationValue(payload.ChatID, payload.SourceSessionID)
	}
	if stableID == "" || stableID == "api" || stableID == "scheduler" {
		return "", store.ConversationRoute{}
	}
	part := conversationKeyPart(stableID)
	if part == "" {
		return "", store.ConversationRoute{}
	}
	routeKey := source + ":" + part
	return routeKey, store.ConversationRoute{
		RouteKey:        routeKey,
		SourceChannel:   string(eventType),
		SourceSessionID: strings.TrimSpace(payload.SourceSessionID),
		ChatID:          strings.TrimSpace(payload.ChatID),
	}
}

func maybeRolloverConversation(ctx context.Context, s *AccountRuntime, current conversationResolution, provider llm.Provider) (conversationResolution, error) {
	policy := defaultRolloverPolicy
	if !policy.Enabled || current.Route == nil {
		return current, nil
	}
	rec, ok, err := s.Store.Conversation(current.ConversationID)
	if err != nil || !ok {
		return current, err
	}
	if rec.ScopeType != "general" {
		return current, nil
	}
	summary, err := s.Store.ConversationSummaryForConversation(current.ConversationID)
	if err != nil {
		return current, err
	}
	if summary.TurnCount < policy.MinTurnsBeforeRollover {
		return current, nil
	}
	reason := ""
	if policy.MaxTurns > 0 && summary.TurnCount > policy.MaxTurns {
		reason = rolloverReasonLengthTurns
	}
	if reason == "" && policy.MaxEstimatedTokensRatio > 0 && provider != nil {
		if state, err := s.Store.LoadConversationStateForChat(current.ConversationID); err == nil && state != nil {
			estTokens := EstimateTokens(state.SystemPrompt)
			for _, turn := range state.Turns {
				estTokens += EstimateTokens(turn.Content) + EstimateTokens(turn.Code) + EstimateTokens(turn.Result)
			}
			limit := int(float64(provider.ContextWindow()) * policy.MaxEstimatedTokensRatio)
			if limit > 0 && estTokens > limit {
				reason = rolloverReasonLengthTokens
			}
		}
	}
	if reason == "" {
		return current, nil
	}
	if err := distillRolloverMemory(ctx, s.Store, provider, current.ConversationID); err != nil {
		slog.Warn("conversation rollover memory distillation failed", "conversation", current.ConversationID, "error", err)
	}
	latestTurnID, err := s.Store.LatestConversationTurnID(current.ConversationID)
	if err != nil {
		return current, err
	}
	child, err := s.Store.CreateRolloverConversation(store.CreateRolloverConversationRequest{
		ParentConversationID: current.ConversationID,
		RolloverReason:       reason,
		RolloverFromTurnID:   latestTurnID,
		Route:                *current.Route,
	})
	if err != nil {
		return current, err
	}
	route, ok, err := s.Store.ConversationRoute(current.Route.RouteKey)
	if err != nil {
		return current, err
	}
	if !ok {
		route = &store.ConversationRoute{
			RouteKey:        current.Route.RouteKey,
			ConversationID:  child.ID,
			SourceChannel:   current.Route.SourceChannel,
			SourceSessionID: current.Route.SourceSessionID,
			ChatID:          current.Route.ChatID,
		}
	}
	return conversationResolution{
		ConversationID: child.ID,
		Route:          route,
		RolledOver:     true,
		Notice:         rolloverNotice(),
	}, nil
}

func rolloverNotice() string {
	return rolloverNoticeHeader + "\n\n" +
		"────────────────────────────────\n\n" +
		"상태를 이어받았습니다. 대화가 길어져 새 대화로 정리해서 이어갑니다.\n" +
		"이전 대화의 중요한 기억만 반영했습니다."
}

func prependRolloverNotice(notice, output string) string {
	notice = strings.TrimSpace(notice)
	output = strings.TrimSpace(output)
	if notice == "" || strings.Contains(output, rolloverNoticeHeader) {
		return output
	}
	if output == "" {
		return notice
	}
	return notice + "\n\n" + output
}

func topicShiftSuggestion(text string, recent []core.ConversationTurn) bool {
	if len(recent) == 0 {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(text))
	for _, phrase := range []string{
		"다른 얘기",
		"다른 이야기",
		"다른 질문",
		"별개 질문",
		"new topic",
		"separate question",
	} {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}

func topicShiftNotice() string {
	return "이건 새 주제로 분리하는 편이 좋아 보입니다. 새 대화로 분리할까요?"
}

func prependTopicShiftNotice(notice, output string) string {
	notice = strings.TrimSpace(notice)
	output = strings.TrimSpace(output)
	if notice == "" || strings.Contains(output, notice) {
		return output
	}
	if output == "" {
		return notice
	}
	return notice + "\n\n" + output
}

func distillRolloverMemory(ctx context.Context, st *store.Store, provider llm.Provider, conversationID string) error {
	if st == nil || provider == nil {
		return nil
	}
	turns, err := st.ListConversationTurnsForConversation(conversationID, rolloverDistillTurnLimit)
	if err != nil {
		return err
	}
	if len(turns) == 0 {
		return nil
	}
	var transcript strings.Builder
	for _, turn := range turns {
		fmt.Fprintf(&transcript, "%s: %s\n", turn.Role, truncateRolloverMemoryValue(turn.Content, 1200))
	}
	resp, err := provider.Generate(ctx, []core.LlmMessage{
		{
			Role: core.RoleSystem,
			Content: strings.Join([]string{
				"Extract only conservative durable memory from this conversation.",
				"Allowed categories: preference, decision, ongoing_task, open_question, state.",
				"Do not store secrets, raw tool results, large file contents, web page bodies, or generic summaries.",
				"Return strict JSON: {\"memories\":[{\"category\":\"preference\",\"key\":\"short_key\",\"value\":\"concise durable fact\",\"confidence\":0.9,\"reason\":\"why\"}]}",
			}, "\n"),
		},
		{Role: core.RoleUser, Content: transcript.String()},
	})
	if err != nil {
		return err
	}
	var parsed rolloverMemoryResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(resp.Content)), &parsed); err != nil {
		return err
	}
	_, err = storeRolloverMemories(st, parsed.Memories)
	return err
}

func storeRolloverMemories(st *store.Store, memories []rolloverMemory) (int, error) {
	if st == nil {
		return 0, nil
	}
	written := 0
	for _, memory := range memories {
		category := strings.ToLower(strings.TrimSpace(memory.Category))
		key := strings.TrimSpace(memory.Key)
		value := truncateRolloverMemoryValue(strings.TrimSpace(memory.Value), rolloverMemoryValueLimit)
		if !rolloverAllowedMemoryCategories[category] || key == "" || value == "" || memory.Confidence < rolloverMemoryMinConfidence {
			continue
		}
		contextKey := rolloverMemoryContextKey(category, key, value)
		if err := st.SetUserContext(contextKey, value, "conversation_rollover"); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}

func rolloverMemoryContextKey(category, key, value string) string {
	category = strings.ToLower(strings.TrimSpace(category))
	if !rolloverAllowedMemoryCategories[category] {
		category = "state"
	}
	slug := conversationKeyPart(key)
	if slug == "" {
		slug = "memory"
	}
	if len(slug) > rolloverMemoryKeySlugLimit {
		slug = slug[:rolloverMemoryKeySlugLimit]
	}
	sum := sha1.Sum([]byte(category + "\x00" + strings.TrimSpace(key) + "\x00" + strings.TrimSpace(value)))
	return fmt.Sprintf("memory:%s:%s-%s", category, slug, hex.EncodeToString(sum[:])[:12])
}

func truncateRolloverMemoryValue(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return strings.TrimSpace(value[:limit])
}
