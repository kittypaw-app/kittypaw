package server

import (
	"context"
	"testing"

	"github.com/jinto/kittypaw/core"
)

type plainResponseChannel struct{ sent string }

func (p *plainResponseChannel) Start(context.Context, chan<- core.Event) error { return nil }
func (p *plainResponseChannel) Name() string                                   { return "plain" }
func (p *plainResponseChannel) SendResponse(_ context.Context, _, response, _ string) error {
	p.sent = response
	return nil
}

type richResponseChannel struct {
	plainResponseChannel
	rich core.OutboundResponse
}

func (r *richResponseChannel) SendRichResponse(_ context.Context, _ string, response core.OutboundResponse, _ string) error {
	r.rich = response
	return nil
}

func TestSendChannelResponseUsesRichResponder(t *testing.T) {
	ch := &richResponseChannel{}
	out := core.OutboundResponse{Text: "fallback", Image: &core.ImageAttachment{URL: "https://cdn.example.com/cat.png"}}
	if err := sendChannelResponse(context.Background(), ch, "chat", out, "reply"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if ch.rich.Image == nil || ch.rich.Image.URL != out.Image.URL {
		t.Fatalf("rich response = %#v", ch.rich)
	}
	if ch.sent != "" {
		t.Fatalf("plain response should not be used, got %q", ch.sent)
	}
}

func TestSendChannelResponseFallsBackToText(t *testing.T) {
	ch := &plainResponseChannel{}
	out := core.OutboundResponse{Text: "fallback", Image: &core.ImageAttachment{URL: "https://cdn.example.com/cat.png"}}
	if err := sendChannelResponse(context.Background(), ch, "chat", out, "reply"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if ch.sent != "fallback" {
		t.Fatalf("sent = %q", ch.sent)
	}
}

type limitedResponseChannel struct {
	sent []string
}

func (l *limitedResponseChannel) Start(context.Context, chan<- core.Event) error { return nil }
func (l *limitedResponseChannel) Name() string                                   { return "limited" }
func (l *limitedResponseChannel) MaxResponseLength() int                         { return 5 }
func (l *limitedResponseChannel) SendResponse(_ context.Context, _, response, _ string) error {
	l.sent = append(l.sent, response)
	return nil
}

func TestSendChannelResponseChunksLimitedText(t *testing.T) {
	ch := &limitedResponseChannel{}
	out := core.OutboundResponse{Text: "hello\nworld!"}
	if err := sendChannelResponse(context.Background(), ch, "chat", out, "reply"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(ch.sent) != 3 {
		t.Fatalf("sent chunks = %v, want 3 chunks", ch.sent)
	}
	for i, chunk := range ch.sent {
		if len(chunk) > ch.MaxResponseLength() {
			t.Fatalf("chunk %d length = %d, want <= %d", i, len(chunk), ch.MaxResponseLength())
		}
	}
	if got := ch.sent[0] + ch.sent[1] + ch.sent[2]; got != out.Text {
		t.Fatalf("reassembled = %q, want %q", got, out.Text)
	}
}
