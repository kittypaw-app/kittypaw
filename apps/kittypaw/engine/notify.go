package engine

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jinto/kittypaw/core"
)

// Notifier is implemented by the server package so engine skills can request
// outbound delivery without importing channel/spawner internals.
type Notifier interface {
	SendNotification(ctx context.Context, target core.DeliveryTarget, text string) error
}

type deliveryState struct {
	sent bool
}

const (
	ctxKeyDeliveryTarget contextKey = "deliveryTarget"
	ctxKeyDeliveryState  contextKey = "deliveryState"
)

func ContextWithDeliveryTarget(ctx context.Context, target core.DeliveryTarget) context.Context {
	if target.IsZero() {
		return ctx
	}
	return context.WithValue(ctx, ctxKeyDeliveryTarget, target)
}

func DeliveryTargetFromContext(ctx context.Context) (core.DeliveryTarget, bool) {
	if v, ok := ctx.Value(ctxKeyDeliveryTarget).(core.DeliveryTarget); ok && !v.IsZero() {
		return v, true
	}
	return core.DeliveryTarget{}, false
}

func contextWithDeliveryState(ctx context.Context, state *deliveryState) context.Context {
	if state == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxKeyDeliveryState, state)
}

func ensureDeliveryState(ctx context.Context) (context.Context, *deliveryState) {
	if state, ok := ctx.Value(ctxKeyDeliveryState).(*deliveryState); ok && state != nil {
		return ctx, state
	}
	state := &deliveryState{}
	return contextWithDeliveryState(ctx, state), state
}

func markNotificationSent(ctx context.Context) {
	if state, ok := ctx.Value(ctxKeyDeliveryState).(*deliveryState); ok && state != nil {
		state.sent = true
	}
}

func notificationSent(ctx context.Context) bool {
	if state, ok := ctx.Value(ctxKeyDeliveryState).(*deliveryState); ok && state != nil {
		return state.sent
	}
	return false
}

func deliveryTargetFromContextOrEvent(ctx context.Context, s *Session) core.DeliveryTarget {
	if target, ok := DeliveryTargetFromContext(ctx); ok {
		if strings.TrimSpace(target.AccountID) == "" && s != nil {
			target.AccountID = strings.TrimSpace(s.AccountID)
		}
		return target
	}
	return deliveryTargetFromEvent(ctx, s)
}

func durableDeliveryTargetFromContextOrEvent(ctx context.Context, s *Session) core.DeliveryTarget {
	target := deliveryTargetFromContextOrEvent(ctx, s)
	if !isNonDurableDeliveryChannel(target.Channel) {
		return target
	}
	return configuredDurableDeliveryTarget(s, target.AccountID)
}

func isNonDurableDeliveryChannel(channel string) bool {
	switch strings.TrimSpace(channel) {
	case string(core.EventWebChat), string(core.EventDesktop):
		return true
	default:
		return false
	}
}

func configuredDurableDeliveryTarget(s *Session, accountID string) core.DeliveryTarget {
	if s == nil || s.Config == nil {
		return core.DeliveryTarget{}
	}
	chatID := strings.TrimSpace(core.FirstAllowedChatID(s.Config))
	if chatID == "" {
		return core.DeliveryTarget{}
	}
	for _, ch := range s.Config.Channels {
		if ch.ChannelType == core.ChannelWeb || ch.ChannelType == core.ChannelDesktop {
			continue
		}
		return core.DeliveryTarget{
			AccountID: strings.TrimSpace(firstNonEmpty(accountID, s.AccountID)),
			Channel:   string(ch.ChannelType.ToEventType()),
			ChatID:    chatID,
		}
	}
	return core.DeliveryTarget{}
}

func deliveryTargetFromEvent(ctx context.Context, s *Session) core.DeliveryTarget {
	var target core.DeliveryTarget
	if s != nil {
		target.AccountID = strings.TrimSpace(s.AccountID)
	}
	if id := strings.TrimSpace(ConversationIDFromContext(ctx)); id != "" {
		target.ConversationID = id
	}
	event := EventFromContext(ctx)
	if event == nil {
		return target
	}
	if id := strings.TrimSpace(event.AccountID); id != "" {
		target.AccountID = id
	}
	target.Channel = string(event.Type)
	var payload core.ChatPayload
	if len(event.Payload) > 0 && json.Unmarshal(event.Payload, &payload) == nil {
		target.ChatID = strings.TrimSpace(payload.ChatID)
		target.ChannelUserID = strings.TrimSpace(payload.SessionID)
		target.ReplyToMessage = strings.TrimSpace(payload.ReplyToMessageID)
		if id := strings.TrimSpace(payload.ConversationID); id != "" {
			target.ConversationID = id
		}
	}
	return target
}

func mergeDeliveryTarget(base, override core.DeliveryTarget) core.DeliveryTarget {
	if v := strings.TrimSpace(override.Channel); v != "" && strings.TrimSpace(base.Channel) != "" && v != strings.TrimSpace(base.Channel) {
		base.ChatID = ""
		base.ConversationID = ""
		base.ChannelUserID = ""
		base.ReplyToMessage = ""
	}
	if v := strings.TrimSpace(override.AccountID); v != "" {
		base.AccountID = v
	}
	if v := strings.TrimSpace(override.Channel); v != "" {
		base.Channel = v
	}
	if v := strings.TrimSpace(override.ChatID); v != "" {
		base.ChatID = v
	}
	if v := strings.TrimSpace(override.ConversationID); v != "" {
		base.ConversationID = v
	}
	if v := strings.TrimSpace(override.ChannelUserID); v != "" {
		base.ChannelUserID = v
	}
	if v := strings.TrimSpace(override.ReplyToMessage); v != "" {
		base.ReplyToMessage = v
	}
	return base
}
