package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/store"
)

type memoryToolOptions struct {
	Scope      string  `json:"scope"`
	Kind       string  `json:"kind"`
	Confidence float64 `json:"confidence"`
}

func memoryScopesForContext(ctx context.Context, s *AccountRuntime) []store.MemoryScope {
	var scopes []store.MemoryScope
	conversationID := strings.TrimSpace(ConversationIDFromContext(ctx))
	if conversationID != "" {
		scopes = append(scopes, store.MemoryScope{Type: store.MemoryScopeConversation, ID: conversationID})
	}
	if scope, ok := projectMemoryScopeForConversation(s, conversationID); ok {
		scopes = append(scopes, scope)
	}
	if scope, ok := channelMemoryScopeForEvent(EventFromContext(ctx)); ok {
		scopes = append(scopes, scope)
	}
	return scopes
}

func memoryWriteFromToolArgs(ctx context.Context, s *AccountRuntime, key, value, source string, args []json.RawMessage) (store.UserMemoryWrite, error) {
	opts := memoryToolOptions{}
	if len(args) > 0 && len(args[0]) > 0 && string(args[0]) != "null" {
		if err := json.Unmarshal(args[0], &opts); err != nil {
			return store.UserMemoryWrite{}, fmt.Errorf("invalid memory options")
		}
	}
	scopeType, scopeID, err := resolveMemoryToolScope(ctx, s, opts.Scope)
	if err != nil {
		return store.UserMemoryWrite{}, err
	}
	return store.UserMemoryWrite{
		Key:        key,
		Value:      value,
		Kind:       opts.Kind,
		ScopeType:  scopeType,
		ScopeID:    scopeID,
		Source:     source,
		Confidence: opts.Confidence,
	}, nil
}

func resolveMemoryToolScope(ctx context.Context, s *AccountRuntime, scope string) (string, string, error) {
	scope = strings.ToLower(strings.TrimSpace(scope))
	switch scope {
	case "", "global", "user":
		return store.MemoryScopeGlobal, "", nil
	case "conversation", "chat":
		conversationID := strings.TrimSpace(ConversationIDFromContext(ctx))
		if conversationID == "" {
			return "", "", fmt.Errorf("conversation scope unavailable")
		}
		return store.MemoryScopeConversation, conversationID, nil
	case "project":
		if projectScope, ok := projectMemoryScopeForConversation(s, ConversationIDFromContext(ctx)); ok {
			return projectScope.Type, projectScope.ID, nil
		}
		return "", "", fmt.Errorf("project scope unavailable")
	case "channel":
		if channelScope, ok := channelMemoryScopeForEvent(EventFromContext(ctx)); ok {
			return channelScope.Type, channelScope.ID, nil
		}
		return "", "", fmt.Errorf("channel scope unavailable")
	default:
		return "", "", fmt.Errorf("invalid memory scope")
	}
}

func projectMemoryScopeForConversation(s *AccountRuntime, conversationID string) (store.MemoryScope, bool) {
	if s == nil || s.Store == nil || strings.TrimSpace(conversationID) == "" {
		return store.MemoryScope{}, false
	}
	scope, ok, err := s.Store.ConversationScope(conversationID)
	if err != nil || !ok {
		return store.MemoryScope{}, false
	}
	switch scope.ScopeType {
	case "project":
		if strings.TrimSpace(scope.ScopeID) == "" {
			return store.MemoryScope{}, false
		}
		return store.MemoryScope{Type: store.MemoryScopeProject, ID: scope.ScopeID}, true
	case "ticket":
		ticket, err := s.Store.GetTicket(scope.ScopeID)
		if err != nil || strings.TrimSpace(ticket.ProjectID) == "" {
			return store.MemoryScope{}, false
		}
		return store.MemoryScope{Type: store.MemoryScopeProject, ID: ticket.ProjectID}, true
	default:
		return store.MemoryScope{}, false
	}
}

func channelMemoryScopeForEvent(event *core.Event) (store.MemoryScope, bool) {
	if event == nil {
		return store.MemoryScope{}, false
	}
	payload, err := event.ParsePayload()
	if err != nil {
		return store.MemoryScope{}, false
	}
	routeKey, _ := conversationRouteKey(event.Type, payload)
	if strings.TrimSpace(routeKey) == "" {
		return store.MemoryScope{}, false
	}
	return store.MemoryScope{Type: store.MemoryScopeChannel, ID: routeKey}, true
}
