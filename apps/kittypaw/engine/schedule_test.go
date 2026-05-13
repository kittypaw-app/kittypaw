package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/llm"
	"github.com/jinto/kittypaw/sandbox"
	"github.com/jinto/kittypaw/store"
)

// ---------------------------------------------------------------------------
// parseCronInterval
// ---------------------------------------------------------------------------

func TestParseCronInterval(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{"every 10m", 10 * time.Minute},
		{"every 2h", 2 * time.Hour},
		{"every 1d", 24 * time.Hour},
		{"every 30s", 30 * time.Second},
		{"*/5 * * * *", 5 * time.Minute},
		{"*/30 * * * *", 30 * time.Minute},
		{"0 0 3 * * *", 24 * time.Hour},
		{"", 0},
		{"  ", 0},
		{"invalid", 0},
		{"0 9 * * *", 24 * time.Hour}, // Daily at 9am → 24h interval
	}
	for _, tt := range tests {
		got := parseCronInterval(tt.input)
		if got != tt.want {
			t.Errorf("parseCronInterval(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Stop() safety
// ---------------------------------------------------------------------------

func TestSchedulerStopMultipleCalls(t *testing.T) {
	sched := NewScheduler(&AccountRuntime{}, nil)
	sched.Stop()
	sched.Stop() // must not panic
	sched.Stop()
}

func TestSchedulerStartAsyncStopWaitIncludesLoops(t *testing.T) {
	st := newTestStore(t)
	cfg := &core.Config{}
	cfg.Reflection.Enabled = true
	sched := NewScheduler(&AccountRuntime{Store: st, Config: cfg}, nil)

	sched.StartAsync(context.Background())
	sched.Stop()
	sched.Wait()
}

func TestSchedulerScheduledSlotRejectsWhenFull(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Runtime.MaxConcurrentScheduledJobs = 1
	sched := NewScheduler(&AccountRuntime{Config: &cfg}, nil)

	release, ok := sched.acquireScheduledSlot()
	if !ok {
		t.Fatal("first scheduled slot should be acquired")
	}
	defer release()

	if release, ok := sched.acquireScheduledSlot(); ok {
		release()
		t.Fatal("second scheduled slot should be rejected while cap is full")
	}
}

func TestReflectionDueUsesConfiguredCron(t *testing.T) {
	cfg := core.ReflectionConfig{Cron: "0 9 * * *"}
	lastRun := time.Date(2026, 5, 12, 9, 30, 0, 0, time.UTC)

	if !reflectionDueAt(cfg, &lastRun, time.Date(2026, 5, 13, 9, 30, 0, 0, time.UTC)) {
		t.Fatal("reflection should be due inside the configured 09:00 cron window")
	}
	if reflectionDueAt(cfg, &lastRun, time.Date(2026, 5, 13, 3, 30, 0, 0, time.UTC)) {
		t.Fatal("reflection should not use hard-coded 03:00 when cron is configured for 09:00")
	}
}

func TestReflectionDueParsesDefaultSixFieldCron(t *testing.T) {
	cfg := core.ReflectionConfig{Cron: "0 0 3 * * *"}
	lastRun := time.Date(2026, 5, 12, 3, 30, 0, 0, time.UTC)

	if !reflectionDueAt(cfg, &lastRun, time.Date(2026, 5, 13, 3, 30, 0, 0, time.UTC)) {
		t.Fatal("reflection should parse default six-field cron and run in the 03:00 window")
	}
	if reflectionDueAt(cfg, &lastRun, time.Date(2026, 5, 13, 4, 0, 0, 0, time.UTC)) {
		t.Fatal("reflection should not run after the configured cron window")
	}
}

// ---------------------------------------------------------------------------
// isDue — requires a real store for GetLastRun/GetFailureCount
// ---------------------------------------------------------------------------

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func newTestScheduler(t *testing.T) (*Scheduler, *store.Store) {
	t.Helper()
	st := newTestStore(t)
	session := &AccountRuntime{Store: st, Config: &core.Config{}}
	return NewScheduler(session, nil), st
}

func TestIsDue_ScheduleFirstRun(t *testing.T) {
	sched, _ := newTestScheduler(t)
	skill := &core.Skill{
		Name:    "test-sched",
		Trigger: core.SkillTrigger{Type: "schedule", Cron: "every 5m"},
	}
	if !sched.isDue(skill) {
		t.Error("first run should be due")
	}
}

func TestIsDue_ScheduleNotYetDue(t *testing.T) {
	sched, st := newTestScheduler(t)
	_ = st.SetLastRun("test-sched", time.Now().Add(-2*time.Minute))

	skill := &core.Skill{
		Name:    "test-sched",
		Trigger: core.SkillTrigger{Type: "schedule", Cron: "every 5m"},
	}
	if sched.isDue(skill) {
		t.Error("should not be due: only 2m elapsed of 5m interval")
	}
}

func TestIsDue_ScheduleElapsed(t *testing.T) {
	sched, st := newTestScheduler(t)
	_ = st.SetLastRun("test-sched", time.Now().Add(-6*time.Minute))

	skill := &core.Skill{
		Name:    "test-sched",
		Trigger: core.SkillTrigger{Type: "schedule", Cron: "every 5m"},
	}
	if !sched.isDue(skill) {
		t.Error("should be due: 6m elapsed > 5m interval")
	}
}

func TestIsDue_OnceNeverRun(t *testing.T) {
	sched, _ := newTestScheduler(t)
	skill := &core.Skill{
		Name:    "test-once",
		Trigger: core.SkillTrigger{Type: "once"},
	}
	if !sched.isDue(skill) {
		t.Error("once trigger with no prior run should be due")
	}
}

func TestIsDue_OnceAlreadyRan(t *testing.T) {
	sched, st := newTestScheduler(t)
	_ = st.SetLastRun("test-once", time.Now())

	skill := &core.Skill{
		Name:    "test-once",
		Trigger: core.SkillTrigger{Type: "once"},
	}
	if sched.isDue(skill) {
		t.Error("once trigger should not be due after it has run")
	}
}

func TestIsDue_OnceRunAtFuture(t *testing.T) {
	sched, _ := newTestScheduler(t)
	future := time.Now().Add(1 * time.Hour).Format(time.RFC3339)
	skill := &core.Skill{
		Name:    "test-once-future",
		Trigger: core.SkillTrigger{Type: "once", RunAt: future},
	}
	if sched.isDue(skill) {
		t.Error("once trigger with RunAt in the future should not be due")
	}
}

func TestIsDue_OnceRunAtPast(t *testing.T) {
	sched, _ := newTestScheduler(t)
	past := time.Now().Add(-1 * time.Hour).Format(time.RFC3339)
	skill := &core.Skill{
		Name:    "test-once-past",
		Trigger: core.SkillTrigger{Type: "once", RunAt: past},
	}
	if !sched.isDue(skill) {
		t.Error("once trigger with RunAt in the past should be due")
	}
}

func TestIsDue_FailureBackoff(t *testing.T) {
	sched, st := newTestScheduler(t)

	// 3 consecutive failures → backoff = 2^3 = 8 minutes
	_ = st.SetLastRun("test-backoff", time.Now().Add(-5*time.Minute))
	_ = st.IncrementFailureCount("test-backoff")
	_ = st.IncrementFailureCount("test-backoff")
	_ = st.IncrementFailureCount("test-backoff")

	skill := &core.Skill{
		Name:    "test-backoff",
		Trigger: core.SkillTrigger{Type: "schedule", Cron: "every 1m"},
	}
	if sched.isDue(skill) {
		t.Error("should not be due: 5m elapsed < 8m backoff (2^3)")
	}

	// After enough time passes, it should be due again.
	_ = st.SetLastRun("test-backoff", time.Now().Add(-10*time.Minute))
	if !sched.isDue(skill) {
		t.Error("should be due: 10m elapsed > 8m backoff")
	}
}

// ---------------------------------------------------------------------------
// In-flight guard
// ---------------------------------------------------------------------------

func TestInflightGuard(t *testing.T) {
	sched, _ := newTestScheduler(t)

	// Simulate a skill already in flight.
	sched.inflight.Store("running-skill", struct{}{})

	_, loaded := sched.inflight.LoadOrStore("running-skill", struct{}{})
	if !loaded {
		t.Error("expected inflight guard to detect already-running skill")
	}

	// After clearing, it should allow again.
	sched.inflight.Delete("running-skill")
	_, loaded = sched.inflight.LoadOrStore("running-skill", struct{}{})
	if loaded {
		t.Error("expected inflight guard to allow skill after clearing")
	}
}

func TestFirstTelegramTarget_NoChannel(t *testing.T) {
	sched, _ := newTestScheduler(t)
	tok, chat := sched.firstTelegramTarget()
	if tok != "" || chat != "" {
		t.Errorf("expected empty target with no channels; got token=%q chat=%q", tok, chat)
	}
}

func TestFirstTelegramTarget_NoAdminChatID(t *testing.T) {
	st := newTestStore(t)
	cfg := &core.Config{
		Channels: []core.ChannelConfig{
			{ChannelType: core.ChannelTelegram, Token: "bot-tok"},
		},
		// AllowedChatIDs intentionally empty — without a chat target we
		// cannot dispatch, so first-channel match must short-circuit.
	}
	sched := NewScheduler(&AccountRuntime{Store: st, Config: cfg}, nil)
	if tok, chat := sched.firstTelegramTarget(); tok != "" || chat != "" {
		t.Errorf("missing allowed chat ids must yield empty target; got token=%q chat=%q", tok, chat)
	}
}

func TestFirstTelegramTarget_PicksFirstTelegram(t *testing.T) {
	st := newTestStore(t)
	cfg := &core.Config{
		AllowedChatIDs: []string{"54076829"},
		Channels: []core.ChannelConfig{
			{ChannelType: core.ChannelKakaoTalk, Token: ""},
			{ChannelType: core.ChannelTelegram, Token: "telegram-token-1"},
			{ChannelType: core.ChannelTelegram, Token: "telegram-token-2"},
		},
	}
	sched := NewScheduler(&AccountRuntime{Store: st, Config: cfg}, nil)
	tok, chat := sched.firstTelegramTarget()
	if tok != "telegram-token-1" {
		t.Errorf("expected first telegram token; got %q", tok)
	}
	if chat != "54076829" {
		t.Errorf("expected admin chat id; got %q", chat)
	}
}

func TestRunSkillAutoDeliversReturnToTriggerDeliveryTarget(t *testing.T) {
	skipWithoutRuntime(t)

	baseDir := t.TempDir()
	st := newTestStore(t)
	cfg := core.DefaultConfig()
	cfg.AutonomyLevel = core.AutonomyFull
	notifier := &captureNotifier{}
	session := &AccountRuntime{
		Provider:  &mockProvider{responses: []*llm.Response{mockResp(`return "scheduled output";`)}},
		Sandbox:   sandbox.New(cfg.Sandbox),
		Store:     st,
		Config:    &cfg,
		BaseDir:   baseDir,
		AccountID: "alice",
		Notifier:  notifier,
	}
	sched := NewScheduler(session, nil)
	sched.runSkill(context.Background(), &core.SkillWithCode{
		Skill: core.Skill{
			Name:    "scheduled-output",
			Enabled: true,
			Trigger: core.SkillTrigger{
				Type: "schedule",
				Delivery: core.DeliveryTarget{
					AccountID: "alice",
					Channel:   string(core.EventTelegram),
					ChatID:    "chat-1",
				},
			},
		},
		Code: `return "ignored";`,
	})

	if len(notifier.deliveries) != 1 {
		t.Fatalf("deliveries = %+v, want one auto-delivery", notifier.deliveries)
	}
	if notifier.deliveries[0].Text != "scheduled output" {
		t.Fatalf("delivered text = %q", notifier.deliveries[0].Text)
	}
	if notifier.deliveries[0].Target.ChatID != "chat-1" {
		t.Fatalf("delivery target = %+v, want chat-1", notifier.deliveries[0].Target)
	}
}

func TestRunSkillSkipsAutoDeliveryAfterNotifySend(t *testing.T) {
	skipWithoutRuntime(t)

	baseDir := t.TempDir()
	st := newTestStore(t)
	cfg := core.DefaultConfig()
	cfg.AutonomyLevel = core.AutonomyFull
	notifier := &captureNotifier{}
	session := &AccountRuntime{
		Provider:  &mockProvider{responses: []*llm.Response{mockResp(`Notify.send("explicit notice"); return "explicit notice";`)}},
		Sandbox:   sandbox.New(cfg.Sandbox),
		Store:     st,
		Config:    &cfg,
		BaseDir:   baseDir,
		AccountID: "alice",
		Notifier:  notifier,
	}
	sched := NewScheduler(session, nil)
	sched.runSkill(context.Background(), &core.SkillWithCode{
		Skill: core.Skill{
			Name:    "scheduled-notify",
			Enabled: true,
			Trigger: core.SkillTrigger{
				Type: "schedule",
				Delivery: core.DeliveryTarget{
					AccountID: "alice",
					Channel:   string(core.EventTelegram),
					ChatID:    "chat-1",
				},
			},
		},
		Code: `return "ignored";`,
	})

	if len(notifier.deliveries) != 1 {
		t.Fatalf("deliveries = %+v, want only explicit Notify.send delivery", notifier.deliveries)
	}
	if notifier.deliveries[0].Text != "explicit notice" {
		t.Fatalf("delivered text = %q", notifier.deliveries[0].Text)
	}
}

func TestRunOnceSkillDeletesAfterDeliveryFailure(t *testing.T) {
	skipWithoutRuntime(t)

	baseDir := t.TempDir()
	st := newTestStore(t)
	cfg := core.DefaultConfig()
	cfg.AutonomyLevel = core.AutonomyFull
	notifier := &captureNotifier{err: errors.New("delivery unavailable")}
	session := &AccountRuntime{
		Provider:  &mockProvider{responses: []*llm.Response{mockResp(`return "scheduled output";`)}},
		Sandbox:   sandbox.New(cfg.Sandbox),
		Store:     st,
		Config:    &cfg,
		BaseDir:   baseDir,
		AccountID: "alice",
		Notifier:  notifier,
	}
	skill := &core.Skill{
		Name:        "once-delivery-fails",
		Version:     1,
		Description: "one-shot reminder",
		Enabled:     true,
		Format:      core.SkillFormatNative,
		Trigger: core.SkillTrigger{
			Type: "once",
			Delivery: core.DeliveryTarget{
				AccountID: "alice",
				Channel:   string(core.EventKakaoTalk),
				ChatID:    "chat-1",
			},
		},
	}
	if err := core.SaveSkillTo(baseDir, skill, `return "scheduled output";`); err != nil {
		t.Fatalf("SaveSkillTo: %v", err)
	}

	sched := NewScheduler(session, nil)
	sched.runSkill(context.Background(), &core.SkillWithCode{
		Skill: *skill,
		Code:  `return "ignored";`,
	})

	got, _, err := core.LoadSkillFrom(baseDir, skill.Name)
	if err != nil {
		t.Fatalf("LoadSkillFrom(%s): %v", skill.Name, err)
	}
	if got != nil {
		t.Fatalf("one-shot skill remained on disk after delivery failure: %+v", got.Trigger)
	}
}

func TestDeliverWeeklyReport_WrongDay(t *testing.T) {
	sched, st := newTestScheduler(t)
	// Pick a weekday that is NOT today so the day check rejects.
	notToday := (int(time.Now().Weekday()) + 3) % 7
	sched.runtime.Config.Reflection.WeeklyReportDay = uint32(notToday)
	// Even with topic prefs and a telegram channel set up, deliver must
	// short-circuit before any network attempt — verified by absence of
	// a __weekly_report__ last-run marker afterwards.
	_ = st.SetUserContext("topic_pref:weather", "0.40", "test")
	sched.runtime.Config.AllowedChatIDs = []string{"chat-id"}
	sched.runtime.Config.Channels = []core.ChannelConfig{
		{ChannelType: core.ChannelTelegram, Token: "tok"},
	}

	sched.deliverWeeklyReport(context.Background())

	if last, _ := st.GetLastRun("__weekly_report__"); last != nil {
		t.Errorf("wrong-day delivery must not record last-run; got %v", last)
	}
}

func TestDeliverWeeklyReport_SameDayDedup(t *testing.T) {
	sched, st := newTestScheduler(t)
	today := int(time.Now().Weekday())
	sched.runtime.Config.Reflection.WeeklyReportDay = uint32(today)
	// Pretend a prior delivery happened 1 hour ago: the function must
	// refuse to redeliver within the 23h dedup window.
	_ = st.SetLastRun("__weekly_report__", time.Now().Add(-1*time.Hour))
	_ = st.SetUserContext("topic_pref:weather", "0.40", "test")

	sched.deliverWeeklyReport(context.Background())

	last, _ := st.GetLastRun("__weekly_report__")
	if last == nil {
		t.Fatal("dedup test: pre-existing last-run got cleared")
	}
	if time.Since(*last) < 30*time.Minute {
		t.Errorf("last-run must not be advanced when dedup blocked; got %v", time.Since(*last))
	}
}

func TestDeliverWeeklyReport_NoPrefsSkips(t *testing.T) {
	sched, st := newTestScheduler(t)
	today := int(time.Now().Weekday())
	sched.runtime.Config.Reflection.WeeklyReportDay = uint32(today)
	// No topic_pref:* rows — empty report would be useless. Function
	// must skip dispatch and not record a last-run.
	sched.runtime.Config.AllowedChatIDs = []string{"chat-id"}
	sched.runtime.Config.Channels = []core.ChannelConfig{
		{ChannelType: core.ChannelTelegram, Token: "tok"},
	}

	sched.deliverWeeklyReport(context.Background())

	if last, _ := st.GetLastRun("__weekly_report__"); last != nil {
		t.Errorf("empty-prefs delivery must not record last-run; got %v", last)
	}
}

// TestDeliverWeeklyReport_NoChannelPreservesLastRun pins the contract that
// when telegram is not yet configured, the dedup last-run is NOT advanced.
// The user can wire up telegram mid-weekday window and the next hourly
// reflectionTick will still attempt delivery.
func TestDeliverWeeklyReport_NoChannelPreservesLastRun(t *testing.T) {
	sched, st := newTestScheduler(t)
	today := int(time.Now().Weekday())
	sched.runtime.Config.Reflection.WeeklyReportDay = uint32(today)
	_ = st.SetUserContext("topic_pref:weather", "0.40", "test")
	// No channels, no allowed chat ids — firstTelegramTarget returns empty.

	sched.deliverWeeklyReport(context.Background())

	if last, _ := st.GetLastRun("__weekly_report__"); last != nil {
		t.Errorf("no-channel skip must NOT record last-run (would silence the next 7 days); got %v", last)
	}
}
