package server

import (
	"context"
	"testing"
	"time"

	"github.com/jinto/kittypaw/core"
)

func TestServerNotifierPersistsBeforeSending(t *testing.T) {
	root := t.TempDir()
	cfg := &core.Config{
		AllowedChatIDs: []string{"chat-1"},
		Channels: []core.ChannelConfig{
			{ChannelType: core.ChannelTelegram, Token: "tok"},
		},
	}
	deps := buildAccountDeps(t, root, "alice", cfg)
	srv := New([]*AccountDeps{deps}, "test", "alice")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.spawner = NewChannelSpawner(ctx, srv.eventCh)
	defer srv.spawner.StopAll()

	ch := newOutboxInspectingChannel(srv, string(core.EventTelegram), "alice")
	if err := srv.spawner.TrySpawn("alice", ch, cfg.Channels[0]); err != nil {
		t.Fatalf("TrySpawn: %v", err)
	}

	if err := srv.accounts.Session("alice").Notifier.SendNotification(ctx, core.DeliveryTarget{
		AccountID:      "alice",
		Channel:        string(core.EventTelegram),
		ChatID:         "chat-1",
		ReplyToMessage: "reply-1",
	}, "hello"); err != nil {
		t.Fatalf("SendNotification: %v", err)
	}

	select {
	case got := <-ch.responses:
		if got.chatID != "chat-1" || got.response != "hello" || got.replyToMessageID != "reply-1" {
			t.Fatalf("sent response = %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for channel send")
	}
	if !ch.sawOutbox.Load() {
		t.Fatal("notification was not present in pending_responses before channel send")
	}
	pending, err := srv.store.DequeuePendingResponses(10)
	if err != nil {
		t.Fatalf("DequeuePendingResponses: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending responses after successful send = %+v, want empty", pending)
	}
}

func TestServerNotifierQueuesWhenChannelNotRunning(t *testing.T) {
	root := t.TempDir()
	cfg := &core.Config{
		AllowedChatIDs: []string{"chat-1"},
		Channels: []core.ChannelConfig{
			{ChannelType: core.ChannelTelegram, Token: "tok"},
		},
	}
	deps := buildAccountDeps(t, root, "alice", cfg)
	srv := New([]*AccountDeps{deps}, "test", "alice")

	if err := srv.accounts.Session("alice").Notifier.SendNotification(context.Background(), core.DeliveryTarget{
		AccountID: "alice",
		Channel:   string(core.EventTelegram),
		ChatID:    "chat-1",
	}, "queued"); err != nil {
		t.Fatalf("SendNotification: %v", err)
	}

	pending, err := srv.store.DequeuePendingResponses(10)
	if err != nil {
		t.Fatalf("DequeuePendingResponses: %v", err)
	}
	if len(pending) != 1 || pending[0].AccountID != "alice" || pending[0].EventType != string(core.EventTelegram) || pending[0].ChatID != "chat-1" || pending[0].Response != "queued" {
		t.Fatalf("pending = %+v", pending)
	}
}

func TestServerNotifierRejectsExplicitChatOutsideAccount(t *testing.T) {
	root := t.TempDir()
	cfg := &core.Config{
		AllowedChatIDs: []string{"chat-1"},
		Channels: []core.ChannelConfig{
			{ChannelType: core.ChannelTelegram, Token: "tok"},
		},
	}
	deps := buildAccountDeps(t, root, "alice", cfg)
	srv := New([]*AccountDeps{deps}, "test", "alice")

	err := srv.accounts.Session("alice").Notifier.SendNotification(context.Background(), core.DeliveryTarget{
		AccountID: "alice",
		Channel:   string(core.EventTelegram),
		ChatID:    "not-alice-chat",
	}, "blocked")
	if err == nil {
		t.Fatal("expected explicit unauthorized chat_id to be rejected")
	}
	pending, qErr := srv.store.DequeuePendingResponses(10)
	if qErr != nil {
		t.Fatalf("DequeuePendingResponses: %v", qErr)
	}
	if len(pending) != 0 {
		t.Fatalf("unauthorized notification was queued: %+v", pending)
	}
}

func TestServerNotifierRejectsUnconfiguredChannel(t *testing.T) {
	root := t.TempDir()
	cfg := &core.Config{
		AllowedChatIDs: []string{"chat-1"},
		Channels: []core.ChannelConfig{
			{ChannelType: core.ChannelSlack, Token: "slack-token"},
		},
	}
	deps := buildAccountDeps(t, root, "alice", cfg)
	srv := New([]*AccountDeps{deps}, "test", "alice")

	err := srv.accounts.Session("alice").Notifier.SendNotification(context.Background(), core.DeliveryTarget{
		AccountID: "alice",
		Channel:   string(core.EventTelegram),
		ChatID:    "chat-1",
	}, "undeliverable")
	if err == nil {
		t.Fatal("expected unconfigured channel to be rejected")
	}
	pending, qErr := srv.store.DequeuePendingResponses(10)
	if qErr != nil {
		t.Fatalf("DequeuePendingResponses: %v", qErr)
	}
	if len(pending) != 0 {
		t.Fatalf("unconfigured channel notification was queued: %+v", pending)
	}
}

func TestServerNotifierRejectsCrossAccountTarget(t *testing.T) {
	root := t.TempDir()
	aliceCfg := &core.Config{
		AllowedChatIDs: []string{"alice-chat"},
		Channels: []core.ChannelConfig{
			{ChannelType: core.ChannelTelegram, Token: "alice-token"},
		},
	}
	bobCfg := &core.Config{
		AllowedChatIDs: []string{"bob-chat"},
		Channels: []core.ChannelConfig{
			{ChannelType: core.ChannelTelegram, Token: "bob-token"},
		},
	}
	aliceDeps := buildAccountDeps(t, root, "alice", aliceCfg)
	bobDeps := buildAccountDeps(t, root, "bob", bobCfg)
	srv := New([]*AccountDeps{aliceDeps, bobDeps}, "test", "alice")

	err := srv.accounts.Session("alice").Notifier.SendNotification(context.Background(), core.DeliveryTarget{
		AccountID: "bob",
		Channel:   string(core.EventTelegram),
	}, "cross-account")
	if err == nil {
		t.Fatal("expected cross-account notification to be rejected")
	}
	pending, qErr := srv.store.DequeuePendingResponses(10)
	if qErr != nil {
		t.Fatalf("DequeuePendingResponses: %v", qErr)
	}
	if len(pending) != 0 {
		t.Fatalf("cross-account notification was queued: %+v", pending)
	}
}
