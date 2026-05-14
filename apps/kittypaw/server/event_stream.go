package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jinto/kittypaw/engine"
)

const (
	EventStreamReady             = "ready"
	EventStreamChannelReceived   = "channel.received"
	EventStreamTurnStarted       = engine.RuntimeEventTurnStarted
	EventStreamTurnFinished      = engine.RuntimeEventTurnFinished
	EventStreamTurnFailed        = engine.RuntimeEventTurnFailed
	EventStreamTurnRejected      = engine.RuntimeEventTurnRejected
	EventStreamDeliveryQueued    = "delivery.queued"
	EventStreamDeliveryDelivered = "delivery.delivered"
	EventStreamDeliveryFailed    = "delivery.failed"

	eventStreamSubscriberBuffer = 64
	eventStreamHeartbeat        = 15 * time.Second
)

type AccountEvent struct {
	ID             string            `json:"id,omitempty"`
	Type           string            `json:"type"`
	AccountID      string            `json:"account_id,omitempty"`
	Timestamp      string            `json:"timestamp"`
	Channel        string            `json:"channel,omitempty"`
	ConversationID string            `json:"conversation_id,omitempty"`
	Status         string            `json:"status,omitempty"`
	ErrorClass     string            `json:"error_class,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`

	ChatID string `json:"-"`
}

type eventStreamBroker struct {
	mu          sync.Mutex
	nextID      atomic.Uint64
	subscribers map[string]map[*eventStreamSubscription]struct{}
}

type eventStreamSubscription struct {
	accountID string
	broker    *eventStreamBroker
	events    chan AccountEvent
	once      sync.Once
}

func newEventStreamBroker() *eventStreamBroker {
	return &eventStreamBroker{
		subscribers: make(map[string]map[*eventStreamSubscription]struct{}),
	}
}

func (b *eventStreamBroker) Subscribe(accountID string) *eventStreamSubscription {
	accountID = strings.TrimSpace(accountID)
	sub := &eventStreamSubscription{
		accountID: accountID,
		broker:    b,
		events:    make(chan AccountEvent, eventStreamSubscriberBuffer),
	}
	b.mu.Lock()
	if b.subscribers[accountID] == nil {
		b.subscribers[accountID] = make(map[*eventStreamSubscription]struct{})
	}
	b.subscribers[accountID][sub] = struct{}{}
	b.mu.Unlock()
	return sub
}

func (s *eventStreamSubscription) Close() {
	if s == nil || s.broker == nil {
		return
	}
	s.once.Do(func() {
		s.broker.mu.Lock()
		defer s.broker.mu.Unlock()
		subs := s.broker.subscribers[s.accountID]
		delete(subs, s)
		if len(subs) == 0 {
			delete(s.broker.subscribers, s.accountID)
		}
	})
}

func (b *eventStreamBroker) Publish(event AccountEvent) {
	accountID := strings.TrimSpace(event.AccountID)
	if accountID == "" || strings.TrimSpace(event.Type) == "" {
		return
	}
	if event.ID == "" {
		event.ID = fmt.Sprintf("%d", b.nextID.Add(1))
	}

	b.mu.Lock()
	subs := make([]*eventStreamSubscription, 0, len(b.subscribers[accountID]))
	for sub := range b.subscribers[accountID] {
		subs = append(subs, sub)
	}
	b.mu.Unlock()

	for _, sub := range subs {
		select {
		case sub.events <- event:
		default:
			// Event streams are live observability, not a durable queue. Dropping
			// preserves the producer path when a debug client is slow.
		}
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	acct, err := s.requestAccount(r)
	if err != nil {
		status := http.StatusUnauthorized
		if strings.HasPrefix(err.Error(), "read local auth store") {
			status = http.StatusInternalServerError
		}
		writeError(w, status, err.Error())
		return
	}
	if s.eventStream == nil {
		writeError(w, http.StatusServiceUnavailable, "event stream unavailable")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	sub := s.eventStream.Subscribe(acct.ID)
	defer sub.Close()

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ready := AccountEvent{
		ID:        "ready",
		Type:      EventStreamReady,
		AccountID: acct.ID,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Status:    "connected",
	}
	if err := writeAccountSSE(w, ready); err != nil {
		return
	}
	flusher.Flush()

	heartbeat := time.NewTicker(eventStreamHeartbeat)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-sub.events:
			if err := writeAccountSSE(w, event); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeAccountSSE(w http.ResponseWriter, event AccountEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if event.ID != "" {
		if _, err := fmt.Fprintf(w, "id: %s\n", sseSafeLine(event.ID)); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "event: %s\n", sseSafeLine(event.Type)); err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", data)
	return err
}

func sseSafeLine(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return strings.TrimSpace(value)
}

func (s *Server) PublishRuntimeEvent(_ context.Context, event engine.RuntimeRunEvent) {
	accountID := strings.TrimSpace(event.AccountID)
	if accountID == "" {
		accountID = s.defaultAccountID()
	}
	s.publishAccountEvent(accountID, AccountEvent{
		Type:           event.Type,
		Channel:        event.Source,
		ConversationID: event.ConversationID,
		ChatID:         event.ChatID,
		ErrorClass:     event.ErrorClass,
	})
}

func (s *Server) attachRuntimeEventSink(_ string, runtime *engine.AccountRuntime) {
	if runtime == nil {
		return
	}
	runtime.EventSink = s
}

func (s *Server) publishAccountEvent(accountID string, event AccountEvent) {
	if s == nil || s.eventStream == nil {
		return
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		accountID = strings.TrimSpace(event.AccountID)
	}
	if accountID == "" || strings.TrimSpace(event.Type) == "" {
		return
	}
	event.AccountID = accountID
	if event.Timestamp == "" {
		event.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if chatID := strings.TrimSpace(event.ChatID); chatID != "" {
		if event.Metadata == nil {
			event.Metadata = make(map[string]string, 1)
		}
		event.Metadata["chat_id_hash"] = redactedIDHash(chatID)
		event.ChatID = ""
	}
	s.eventStream.Publish(event)
}

func redactedIDHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])[:16]
}

func publishDeliveryEvent(s *Server, accountID, typ string, channel any, chatID string, metadata map[string]string) {
	if s == nil {
		return
	}
	channelText := strings.TrimSpace(fmt.Sprint(channel))
	s.publishAccountEvent(accountID, AccountEvent{
		Type:     typ,
		Channel:  channelText,
		ChatID:   chatID,
		Metadata: metadata,
	})
}
