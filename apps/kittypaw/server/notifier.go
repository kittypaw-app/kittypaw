package server

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/engine"
	"github.com/jinto/kittypaw/store"
)

type serverNotifier struct {
	server    *Server
	accountID string
}

func (n *serverNotifier) SendNotification(ctx context.Context, target core.DeliveryTarget, text string) error {
	if n == nil || n.server == nil {
		return fmt.Errorf("delivery not configured")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("text required")
	}

	s := n.server
	withOrigin := func(req store.OutboundDeliveryWrite) store.OutboundDeliveryWrite {
		return outboundDeliveryWithOrigin(ctx, req)
	}
	accountID := strings.TrimSpace(n.accountID)
	if accountID == "" {
		accountID = s.defaultAccountID()
	}
	if requested := strings.TrimSpace(target.AccountID); requested != "" && requested != accountID {
		return fmt.Errorf("delivery target account %q does not match session account %q", requested, accountID)
	}

	cfg := s.notifyAccountConfig(accountID)
	if cfg == nil {
		return fmt.Errorf("delivery account %q is not configured", accountID)
	}
	channelType := core.EventType(strings.TrimSpace(target.Channel))
	if channelType == "" {
		channelType = defaultNotifyChannel(cfg.Channels)
	}
	if channelType == "" {
		return fmt.Errorf("delivery target missing channel")
	}
	if channelType == core.EventWebChat || channelType == core.EventDesktop {
		return fmt.Errorf("delivery channel %s is not durable", channelType)
	}
	if !notifyChannelConfigured(cfg, channelType) {
		return fmt.Errorf("delivery channel %s is not configured", channelType)
	}

	chatID := strings.TrimSpace(target.ChatID)
	if chatID == "" {
		chatID = core.FirstAllowedChatID(cfg)
	}
	if chatID == "" {
		return fmt.Errorf("delivery target missing chat_id")
	}
	if channelType != core.EventKakaoTalk && !core.ChatBelongsToAccount(cfg, chatID) {
		return fmt.Errorf("delivery chat_id %q is not allowed for account %q", chatID, accountID)
	}

	if s.spawner == nil {
		if channelType == core.EventKakaoTalk {
			return fmt.Errorf("delivery channel %s is not running", channelType)
		}
		id, err := s.store.EnqueueResponseWithID(accountID, string(channelType), chatID, text)
		if err != nil {
			return err
		}
		s.recordOutboundDelivery(withOrigin(store.OutboundDeliveryWrite{
			AccountID:         accountID,
			EventType:         string(channelType),
			ChatID:            chatID,
			Source:            store.OutboundDeliverySourceNotify,
			Status:            store.OutboundDeliveryStatusQueued,
			Response:          text,
			PendingResponseID: id,
		}))
		publishDeliveryEvent(s, accountID, EventStreamDeliveryQueued, channelType, chatID, map[string]string{
			"reason": "spawner_unavailable",
		})
		return nil
	}
	ch, ok := s.spawner.GetChannel(accountID, channelType)
	if !ok {
		if channelType == core.EventKakaoTalk {
			return fmt.Errorf("delivery channel %s is not running", channelType)
		}
		id, err := s.store.EnqueueResponseWithID(accountID, string(channelType), chatID, text)
		if err != nil {
			return err
		}
		s.recordOutboundDelivery(withOrigin(store.OutboundDeliveryWrite{
			AccountID:         accountID,
			EventType:         string(channelType),
			ChatID:            chatID,
			Source:            store.OutboundDeliverySourceNotify,
			Status:            store.OutboundDeliveryStatusQueued,
			Response:          text,
			PendingResponseID: id,
		}))
		publishDeliveryEvent(s, accountID, EventStreamDeliveryQueued, channelType, chatID, map[string]string{
			"reason": "channel_not_running",
		})
		return nil
	}

	outbound := core.ParseOutboundResponse(text)
	var pendingID int64
	var pendingQueued bool
	var deliveryID int64
	if channelType != core.EventKakaoTalk {
		id, err := s.store.EnqueueResponseWithID(accountID, string(channelType), chatID, outbound.Text)
		if err != nil {
			slog.Error("notify: enqueue response before send failed",
				"account", accountID, "channel", channelType, "chat_id", chatID, "error", err)
		} else {
			pendingID = id
			pendingQueued = true
			deliveryID = s.recordOutboundDelivery(withOrigin(store.OutboundDeliveryWrite{
				AccountID:         accountID,
				EventType:         string(channelType),
				ChatID:            chatID,
				Source:            store.OutboundDeliverySourceNotify,
				Status:            store.OutboundDeliveryStatusQueued,
				Response:          outbound.Text,
				PendingResponseID: id,
			}))
			publishDeliveryEvent(s, accountID, EventStreamDeliveryQueued, channelType, chatID, nil)
		}
	} else {
		deliveryID = s.recordOutboundDelivery(withOrigin(store.OutboundDeliveryWrite{
			AccountID: accountID,
			EventType: string(channelType),
			ChatID:    chatID,
			Source:    store.OutboundDeliverySourceNotify,
			Status:    store.OutboundDeliveryStatusSending,
			Response:  outbound.Text,
		}))
	}

	if err := sendChannelResponse(ctx, ch, chatID, outbound, target.ReplyToMessage); err != nil {
		slog.Error("notify: send failed",
			"account", accountID, "channel", channelType, "chat_id", chatID, "error", err)
		if channelType != core.EventKakaoTalk && !pendingQueued {
			id, qErr := s.store.EnqueueResponseWithID(accountID, string(channelType), chatID, outbound.Text)
			if qErr != nil {
				s.markOutboundDelivery(deliveryID, accountID, store.OutboundDeliveryStatusFailed, "send_failed", err.Error())
				return fmt.Errorf("send failed: %v; enqueue failed: %w", err, qErr)
			}
			deliveryID = s.recordOutboundDelivery(withOrigin(store.OutboundDeliveryWrite{
				AccountID:         accountID,
				EventType:         string(channelType),
				ChatID:            chatID,
				Source:            store.OutboundDeliverySourceNotify,
				Status:            store.OutboundDeliveryStatusQueued,
				Response:          outbound.Text,
				PendingResponseID: id,
				ErrorClass:        "send_failed",
				ErrorMessage:      err.Error(),
			}))
			publishDeliveryEvent(s, accountID, EventStreamDeliveryQueued, channelType, chatID, map[string]string{
				"reason": "send_failed",
			})
			publishDeliveryEvent(s, accountID, EventStreamDeliveryFailed, channelType, chatID, map[string]string{
				"error_class": "send_failed",
			})
			return nil
		}
		s.markOutboundDelivery(deliveryID, accountID, store.OutboundDeliveryStatusFailed, "send_failed", err.Error())
		if pendingQueued {
			publishDeliveryEvent(s, accountID, EventStreamDeliveryFailed, channelType, chatID, map[string]string{
				"error_class": "send_failed",
			})
			return nil
		}
		return err
	}
	if pendingQueued {
		if err := s.store.MarkResponseDelivered(pendingID); err != nil {
			slog.Error("notify: clear delivered outbox response failed",
				"id", pendingID, "account", accountID, "channel", channelType,
				"chat_id", chatID, "error", err)
		}
		publishDeliveryEvent(s, accountID, EventStreamDeliveryDelivered, channelType, chatID, nil)
	}
	s.markOutboundDelivery(deliveryID, accountID, store.OutboundDeliveryStatusDelivered, "", "")
	return nil
}

func (s *Server) attachRuntimeNotifier(accountID string, sess *engine.AccountRuntime) {
	if sess == nil {
		return
	}
	sess.Notifier = &serverNotifier{server: s, accountID: accountID}
}

func (s *Server) notifyAccountConfig(accountID string) *core.Config {
	if td := s.accountDepsForID(accountID); td != nil && td.Account != nil && td.Account.Config != nil {
		return td.Account.Config
	}
	if s.accountRegistry != nil {
		if account := s.accountRegistry.Get(accountID); account != nil {
			return account.Config
		}
	}
	if accountID == s.defaultAccountID() {
		s.configMu.RLock()
		defer s.configMu.RUnlock()
		return s.config
	}
	return nil
}

func defaultNotifyChannel(channels []core.ChannelConfig) core.EventType {
	for _, ch := range channels {
		if ch.ChannelType == core.ChannelWeb || ch.ChannelType == core.ChannelDesktop {
			continue
		}
		return ch.ChannelType.ToEventType()
	}
	return ""
}

func notifyChannelConfigured(cfg *core.Config, channelType core.EventType) bool {
	if cfg == nil || channelType == "" {
		return false
	}
	for _, ch := range cfg.Channels {
		if ch.ChannelType.ToEventType() == channelType {
			return true
		}
	}
	return false
}
