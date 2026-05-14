package engine

import (
	"context"
	"testing"

	"github.com/jinto/kittypaw/core"
)

type captureRuntimeEventSink struct {
	events []RuntimeRunEvent
}

func (s *captureRuntimeEventSink) PublishRuntimeEvent(_ context.Context, event RuntimeRunEvent) {
	s.events = append(s.events, event)
}

func TestAccountRuntimePublishesTurnLifecycleEvents(t *testing.T) {
	sink := &captureRuntimeEventSink{}
	runtime := &AccountRuntime{
		AccountID: "alice",
		EventSink: sink,
	}

	output, err := runtime.Run(context.Background(), webChatEvent("/help"), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if output == "" {
		t.Fatal("expected /help output")
	}

	if len(sink.events) != 2 {
		t.Fatalf("events = %+v, want started and finished", sink.events)
	}
	started := sink.events[0]
	if started.Type != RuntimeEventTurnStarted {
		t.Fatalf("first event type = %q, want %q", started.Type, RuntimeEventTurnStarted)
	}
	if started.AccountID != "alice" || started.Source != string(core.EventWebChat) {
		t.Fatalf("started event = %+v, want alice web_chat", started)
	}
	if started.ChatID != "test-chat" {
		t.Fatalf("started chat_id = %q, want test-chat", started.ChatID)
	}
	finished := sink.events[1]
	if finished.Type != RuntimeEventTurnFinished {
		t.Fatalf("second event type = %q, want %q", finished.Type, RuntimeEventTurnFinished)
	}
	if finished.ErrorClass != "" {
		t.Fatalf("finished error_class = %q, want empty", finished.ErrorClass)
	}
}
