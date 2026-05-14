package engine

import (
	"context"
	"errors"

	"github.com/jinto/kittypaw/core"
)

const (
	RuntimeEventTurnStarted  = "turn.started"
	RuntimeEventTurnFinished = "turn.finished"
	RuntimeEventTurnFailed   = "turn.failed"
	RuntimeEventTurnRejected = "turn.rejected"
)

// RuntimeEventSink receives redaction-boundary-neutral lifecycle events from
// AccountRuntime. Server-side sinks decide what can be exposed externally.
type RuntimeEventSink interface {
	PublishRuntimeEvent(context.Context, RuntimeRunEvent)
}

type RuntimeRunEvent struct {
	Type           string
	AccountID      string
	Source         string
	ConversationID string
	ChatID         string
	ErrorClass     string
}

func (s *AccountRuntime) runtimeRunEvent(event core.Event) RuntimeRunEvent {
	accountID := s.AccountID
	if accountID == "" {
		accountID = event.AccountID
	}
	runEvent := RuntimeRunEvent{
		AccountID: accountID,
		Source:    string(event.Type),
	}
	if payload, err := event.ParsePayload(); err == nil {
		runEvent.ConversationID = payload.ConversationID
		runEvent.ChatID = payload.ChatID
	}
	return runEvent
}

func (s *AccountRuntime) publishRuntimeEvent(ctx context.Context, event RuntimeRunEvent) {
	if s == nil || s.EventSink == nil || event.Type == "" {
		return
	}
	s.EventSink.PublishRuntimeEvent(ctx, event)
}

func runtimeRunEventWithError(event RuntimeRunEvent, typ string, err error) RuntimeRunEvent {
	event.Type = typ
	event.ErrorClass = runtimeRunErrorClass(err)
	return event
}

func runtimeRunErrorClass(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, ErrRuntimeAdmissionBusy):
		return "runtime_admission_busy"
	case isLLMAdmissionLimitError(err):
		return "llm_admission_limit"
	default:
		return "runtime_error"
	}
}
