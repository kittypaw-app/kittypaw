package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/store"
)

// Scheduler runs scheduled skills at their configured intervals.
type Scheduler struct {
	runtime    *AccountRuntime
	pkgManager *core.PackageManager
	stop       chan struct{}
	stopOnce   sync.Once
	startOnce  sync.Once
	loops      sync.WaitGroup
	inflight   sync.Map       // skill name → struct{}: prevents concurrent runs of the same skill
	wg         sync.WaitGroup // tracks in-flight runSkill goroutines for graceful drain
	jobSlots   chan struct{}
}

type SkillScheduleState struct {
	LastRun      *time.Time
	FailureCount int
	NextRun      *time.Time
	Due          bool
}

type SchedulerSnapshot struct {
	Running  uint32   `json:"running"`
	Capacity uint32   `json:"capacity"`
	Inflight []string `json:"inflight"`
}

const scheduledRunLeaseDuration = 2 * turnRunMaxTime

// NewScheduler creates a scheduler that uses the given account runtime for execution.
// pkgManager may be nil if packages are not configured.
func NewScheduler(runtime *AccountRuntime, pkgManager *core.PackageManager) *Scheduler {
	jobLimit := uint32(2)
	if runtime != nil && runtime.Config != nil && runtime.Config.Runtime.MaxConcurrentScheduledJobs != 0 {
		jobLimit = runtime.Config.Runtime.MaxConcurrentScheduledJobs
	}
	return &Scheduler{
		runtime:    runtime,
		pkgManager: pkgManager,
		stop:       make(chan struct{}),
		jobSlots:   make(chan struct{}, jobLimit),
	}
}

// Start begins the scheduling loop, checking every minute for due skills.
// Also starts a separate goroutine for the daily reflection cycle.
func (s *Scheduler) Start(ctx context.Context) {
	started := false
	s.startOnce.Do(func() {
		started = true
		s.loops.Add(1)
	})
	if !started {
		return
	}
	s.run(ctx)
}

// StartAsync starts the scheduler loop in a goroutine while registering it
// synchronously, so Wait cannot miss a just-started loop.
func (s *Scheduler) StartAsync(ctx context.Context) {
	s.startOnce.Do(func() {
		s.loops.Add(1)
		go s.run(ctx)
	})
}

func (s *Scheduler) run(ctx context.Context) {
	defer s.loops.Done()
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	s.startReflectionLoop(ctx)

	slog.Info("scheduler started")

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		case <-ticker.C:
			s.tickOnce(ctx)
		}
	}
}

func (s *Scheduler) Snapshot() SchedulerSnapshot {
	if s == nil {
		return SchedulerSnapshot{Inflight: []string{}}
	}
	snapshot := SchedulerSnapshot{Inflight: []string{}}
	if s.jobSlots != nil {
		snapshot.Running = uint32(len(s.jobSlots))
		snapshot.Capacity = uint32(cap(s.jobSlots))
	}
	s.inflight.Range(func(key, _ any) bool {
		name, ok := key.(string)
		if ok {
			snapshot.Inflight = append(snapshot.Inflight, name)
		}
		return true
	})
	sort.Strings(snapshot.Inflight)
	return snapshot
}

func (s *Scheduler) scheduleTimezone() core.UserTimezone {
	if s == nil || s.runtime == nil {
		return core.ResolveUserTimezone(nil)
	}
	return core.ResolveUserTimezone(s.runtime.Config)
}

func scheduleTimezoneForRuntime(runtime *AccountRuntime) core.UserTimezone {
	if runtime == nil {
		return core.ResolveUserTimezone(nil)
	}
	return core.ResolveUserTimezone(runtime.Config)
}

func (s *Scheduler) startReflectionLoop(ctx context.Context) {
	if s.runtime == nil || s.runtime.Config == nil || !s.runtime.Config.Reflection.Enabled {
		return
	}
	s.loops.Add(1)
	go func() {
		defer s.loops.Done()
		s.runReflectionLoop(ctx)
	}()
}

// tickOnce wraps a single checkAndRun invocation in a recover block so a
// panic in skill loading or dispatch does not exit the scheduler loop.
// Per-skill and per-package goroutines have their own recover; this one
// guards the tick dispatcher itself.
func (s *Scheduler) tickOnce(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			RecoverAccountPanic(s.runtime, "scheduler.tick", r)
		}
	}()
	s.checkAndRun(ctx)
}

// runReflectionLoop checks once per hour whether the daily reflection cycle
// should run. Default schedule: 03:00 daily.
func (s *Scheduler) runReflectionLoop(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		case <-ticker.C:
			s.reflectionTick(ctx)
		}
	}
}

// reflectionTick runs one reflection + evolution check with panic recovery
// so a failure in the daily cycle does not exit the reflection goroutine
// for the whole process lifetime.
func (s *Scheduler) reflectionTick(ctx context.Context) {
	s.runReflectionTick(ctx)
}

func (s *Scheduler) runReflectionTick(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			RecoverAccountPanic(s.runtime, "scheduler.reflection", r)
		}
	}()
	if !s.isReflectionDue() {
		return
	}
	slog.Info("scheduler: running reflection cycle")
	if err := RunReflectionCycle(ctx, s.runtime, &s.runtime.Config.Reflection); err != nil {
		slog.Error("scheduler: reflection cycle failed", "error", err)
	}

	// After reflection, check evolution trigger conditions.
	if s.runtime.Config.Evolution.Enabled {
		base, err := core.ResolveBaseDir(s.runtime.BaseDir)
		if err == nil {
			staffList, err := core.ListStaffRecords(base)
			if err == nil {
				for _, p := range staffList {
					_ = TriggerEvolution(ctx, p.ID, s.runtime, &s.runtime.Config.Evolution)
				}
			}
		}
	}

	// Weekly report: if today matches WeeklyReportDay, emit the report
	// to the account's first configured Telegram channel and allowed chat id.
	s.deliverWeeklyReport(ctx)

	// Record last run.
	_ = s.runtime.Store.SetLastRun("__reflection__", time.Now())
}

// deliverWeeklyReport sends the topic-preference summary on the configured
// weekday. Idempotency is anchored on `__weekly_report__` last-run so a
// server restart on the same day does not double-send. Best-effort: any
// dispatch failure is logged warn and does not abort the reflection cycle.
func (s *Scheduler) deliverWeeklyReport(ctx context.Context) {
	s.deliverWeeklyReportAt(ctx, time.Now())
}

func (s *Scheduler) deliverWeeklyReportAt(ctx context.Context, now time.Time) {
	cfg := s.runtime.Config.Reflection
	tz := s.scheduleTimezone()
	loc := tz.Location
	if loc == nil {
		loc = time.Local
	}
	// time.Weekday: Sunday=0..Saturday=6. Default 0 (Sunday) matches the
	// docs/index.html promise "매주 일요일 주간 관심사 리포트".
	if int(now.In(loc).Weekday()) != int(cfg.WeeklyReportDay) {
		return
	}
	lastRun, _ := s.runtime.Store.GetLastRun("__weekly_report__")
	if lastRun != nil && now.Sub(*lastRun) < 23*time.Hour {
		return
	}

	prefs, err := s.runtime.Store.ListUserContextPrefix("topic_pref:")
	if err != nil {
		slog.Warn("weekly report: load prefs failed", "error", err)
		return
	}
	if len(prefs) == 0 {
		// No data yet — skip rather than send an empty report.
		return
	}

	report := BuildWeeklyReport(prefs)
	if strings.TrimSpace(report) == "" {
		// Topic prefs existed but BuildWeeklyReport produced an empty
		// summary (e.g. all topics filtered out). Silent skip — better
		// than mailing a blank message. No last-run record so the next
		// hourly tick re-evaluates once prefs grow.
		return
	}
	token, chatID := s.firstTelegramTarget()
	if token == "" || chatID == "" {
		// No telegram channel configured. Don't burn the dedup window —
		// when the user wires up telegram later, the next tick should
		// still attempt delivery within the same weekday window.
		slog.Info("weekly report: no telegram channel configured, skipping dispatch")
		return
	}
	target := core.DeliveryTarget{
		AccountID: s.runtime.AccountID,
		Channel:   string(core.EventTelegram),
		ChatID:    chatID,
	}
	if s.runtime.Notifier != nil {
		if err := s.runtime.Notifier.SendNotification(ctx, target, report); err != nil {
			slog.Warn("weekly report: telegram dispatch failed", "error", err)
			return
		}
	} else if err := SendTelegramText(ctx, token, chatID, report); err != nil {
		slog.Warn("weekly report: telegram dispatch failed", "error", err)
		return
	}
	slog.Info("weekly report: delivered")
	_ = s.runtime.Store.SetLastRun("__weekly_report__", now)
}

// firstTelegramTarget returns the (bot_token, chat_id) of the account's
// first telegram channel, or ("", "") when none is configured.
func (s *Scheduler) firstTelegramTarget() (string, string) {
	cfg := s.runtime.Config
	chatID := core.FirstAllowedChatID(cfg)
	if chatID == "" {
		return "", ""
	}
	for _, ch := range cfg.Channels {
		if ch.ChannelType == core.ChannelTelegram && ch.Token != "" {
			return ch.Token, chatID
		}
	}
	return "", ""
}

// isReflectionDue returns true if the reflection cycle should run inside the
// configured cron window. When no cron is configured, it uses 03:00 daily.
func (s *Scheduler) isReflectionDue() bool {
	lastRun, _ := s.runtime.Store.GetLastRun("__reflection__")
	return reflectionDueAtInLocation(s.runtime.Config.Reflection, lastRun, time.Now(), s.scheduleTimezone().Location)
}

func reflectionDueAt(cfg core.ReflectionConfig, lastRun *time.Time, now time.Time) bool {
	return reflectionDueAtInLocation(cfg, lastRun, now, now.Location())
}

func reflectionDueAtInLocation(cfg core.ReflectionConfig, lastRun *time.Time, now time.Time, loc *time.Location) bool {
	expr := strings.TrimSpace(cfg.Cron)
	if expr == "" {
		expr = "0 0 3 * * *"
	}
	if strings.HasPrefix(expr, "every ") {
		return cronIsDueAtInLocation(expr, lastRun, now, loc)
	}
	schedule, err := parseCronSchedule(expr)
	if err != nil {
		return false
	}
	if loc == nil {
		loc = time.Local
	}
	fire := schedule.Next(now.In(loc).Add(-1 * time.Hour)).UTC()
	if fire.After(now.UTC()) {
		return false
	}
	return lastRun == nil || lastRun.Before(fire)
}

// Stop signals the scheduler to exit. Safe to call multiple times.
func (s *Scheduler) Stop() {
	s.stopOnce.Do(func() { close(s.stop) })
}

// Wait blocks until scheduler loops and all in-flight skill executions complete.
func (s *Scheduler) Wait() {
	s.loops.Wait()
	s.wg.Wait()
}

func (s *Scheduler) checkAndRun(ctx context.Context) {
	skills, err := core.LoadAllSkillsFrom(s.runtime.BaseDir)
	if err != nil {
		slog.Error("scheduler: load skills failed", "error", err)
		return
	}

	for _, sk := range skills {
		if !sk.Manifest.Enabled {
			continue
		}
		if sk.Manifest.Trigger.Type != "schedule" && sk.Manifest.Trigger.Type != "once" {
			continue
		}

		state := s.skillScheduleState(&sk.Manifest, time.Now())
		if state.Due && state.NextRun != nil {
			if _, loaded := s.inflight.LoadOrStore(sk.Manifest.Name, struct{}{}); loaded {
				slog.Debug("scheduler: skill still running, skipping", "name", sk.Manifest.Name)
				continue
			}
			releaseSlot, ok := s.acquireScheduledSlot()
			if !ok {
				s.inflight.Delete(sk.Manifest.Name)
				slog.Warn("scheduler: account scheduled job cap reached, skipping tick", "name", sk.Manifest.Name)
				continue
			}
			run, claimed, err := s.claimScheduledRun(sk.Manifest.Name, "skill", sk.Manifest.Name, sk.Manifest.Trigger.Type, *state.NextRun)
			if err != nil {
				releaseSlot()
				s.inflight.Delete(sk.Manifest.Name)
				slog.Error("scheduler: scheduled run claim failed", "name", sk.Manifest.Name, "error", err)
				continue
			}
			if !claimed {
				releaseSlot()
				s.inflight.Delete(sk.Manifest.Name)
				slog.Debug("scheduler: scheduled run already claimed", "name", sk.Manifest.Name)
				continue
			}
			slog.Info("scheduler: running skill", "name", sk.Manifest.Name, "trigger", sk.Manifest.Trigger.Type)
			s.wg.Add(1)
			go func(sk core.SkillManifestWithCode, run *store.ScheduledRun) {
				defer s.wg.Done()
				defer releaseSlot()
				defer s.inflight.Delete(sk.Manifest.Name)
				runWithAccountRecover(s.runtime, "scheduler.runSkill", func() {
					s.runSkillWithScheduledRun(ctx, &sk, run)
				})
			}(sk, run)
		}
	}

	// Check installed packages with cron schedules.
	s.checkPackages(ctx)
}

// checkPackages iterates installed packages and runs those with due cron schedules.
func (s *Scheduler) checkPackages(ctx context.Context) {
	if s.pkgManager == nil {
		return
	}

	packages, err := s.pkgManager.ListInstalled()
	if err != nil {
		slog.Error("scheduler: list packages failed", "error", err)
		return
	}

	tz := s.scheduleTimezone()
	now := time.Now()
	for _, pkg := range packages {
		if pkg.Meta.Cron == "" {
			continue
		}

		schedName := "pkg:" + pkg.Meta.ID
		lastRun, _ := s.runtime.Store.GetLastRun(schedName)
		nextRun, ok := nextScheduledRunInLocation(pkg.Meta.Cron, lastRun, now, tz.Location)
		if !ok || now.UTC().Before(nextRun) {
			continue
		}

		if _, loaded := s.inflight.LoadOrStore(schedName, struct{}{}); loaded {
			continue
		}
		releaseSlot, ok := s.acquireScheduledSlot()
		if !ok {
			s.inflight.Delete(schedName)
			slog.Warn("scheduler: account scheduled job cap reached, skipping tick", "id", pkg.Meta.ID)
			continue
		}
		run, claimed, err := s.claimScheduledRun(schedName, "package", pkg.Meta.ID, "package", nextRun)
		if err != nil {
			releaseSlot()
			s.inflight.Delete(schedName)
			slog.Error("scheduler: package scheduled run claim failed", "id", pkg.Meta.ID, "error", err)
			continue
		}
		if !claimed {
			releaseSlot()
			s.inflight.Delete(schedName)
			continue
		}

		slog.Info("scheduler: running package", "id", pkg.Meta.ID)
		s.wg.Add(1)
		go func(pkg core.SkillPackage, run *store.ScheduledRun) {
			defer s.wg.Done()
			defer releaseSlot()
			defer s.inflight.Delete("pkg:" + pkg.Meta.ID)
			runWithAccountRecover(s.runtime, "scheduler.runPackage", func() {
				s.runPackageWithScheduledRun(ctx, &pkg, run)
			})
		}(pkg, run)
	}
}

func (s *Scheduler) acquireScheduledSlot() (func(), bool) {
	if s == nil || s.jobSlots == nil {
		return func() {}, true
	}
	select {
	case s.jobSlots <- struct{}{}:
		return func() { <-s.jobSlots }, true
	default:
		return nil, false
	}
}

// runPackage executes a package's main.js directly, then dispatches the output
// to Telegram if the package has telegram config. Packages return their formatted
// message — the scheduler handles delivery.
func (s *Scheduler) runPackage(ctx context.Context, pkg *core.SkillPackage) {
	s.runPackageWithScheduledRun(ctx, pkg, nil)
}

func (s *Scheduler) runPackageWithScheduledRun(ctx context.Context, pkg *core.SkillPackage, run *store.ScheduledRun) {
	schedName := "pkg:" + pkg.Meta.ID
	if run == nil {
		if err := s.runtime.Store.SetLastRun(schedName, time.Now()); err != nil {
			slog.Error("scheduler: SetLastRun failed for package", "id", pkg.Meta.ID, "error", err)
			return
		}
	}

	// Execute package directly (no LLM loop).
	resultJSON, err := runSkillOrPackage(ctx, pkg.Meta.ID, s.runtime)
	if err != nil {
		slog.Error("scheduler: package execution failed", "id", pkg.Meta.ID, "error", err)
		if s.finishScheduledRun(run, store.ScheduledRunStatusFailed, err) {
			s.setLastRunFromScheduledRun(schedName, run)
			_ = s.runtime.Store.IncrementFailureCount(schedName)
		}
		return
	}

	// Parse the result to get the output message.
	var result map[string]any
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		slog.Error("scheduler: parse result failed", "id", pkg.Meta.ID, "error", err)
		if s.finishScheduledRun(run, store.ScheduledRunStatusFailed, err) {
			s.setLastRunFromScheduledRun(schedName, run)
			_ = s.runtime.Store.IncrementFailureCount(schedName)
		}
		return
	}
	if errMsg, ok := result["error"].(string); ok && errMsg != "" {
		slog.Error("scheduler: package returned error", "id", pkg.Meta.ID, "error", errMsg)
		if s.finishScheduledRun(run, store.ScheduledRunStatusFailed, errors.New(errMsg)) {
			s.setLastRunFromScheduledRun(schedName, run)
			_ = s.runtime.Store.IncrementFailureCount(schedName)
		}
		return
	}

	output, _ := result["output"].(string)

	// Dispatch output to Telegram if configured.
	if output != "" && s.pkgManager != nil {
		config, _ := s.pkgManager.GetConfig(pkg.Meta.ID)
		token := config["telegram_token"]
		chatID := config["chat_id"]
		if token != "" && chatID != "" {
			target := core.DeliveryTarget{
				AccountID: s.runtime.AccountID,
				Channel:   string(core.EventTelegram),
				ChatID:    chatID,
			}
			if s.runtime.Notifier != nil {
				if err := s.runtime.Notifier.SendNotification(ctx, target, output); err != nil {
					slog.Error("scheduler: telegram dispatch failed", "id", pkg.Meta.ID, "error", err)
				}
			} else if err := SendTelegramText(ctx, token, chatID, output); err != nil {
				slog.Error("scheduler: telegram dispatch failed", "id", pkg.Meta.ID, "error", err)
			}
		}
	}

	// Execute chain steps if defined.
	if len(pkg.Chain) > 0 {
		if chainErr := s.executeChainSteps(ctx, pkg, output); chainErr != nil {
			slog.Error("scheduler: chain execution failed", "id", pkg.Meta.ID, "error", chainErr)
		}
	}
	if s.finishScheduledRun(run, store.ScheduledRunStatusSucceeded, nil) {
		s.setLastRunFromScheduledRun(schedName, run)
		_ = s.runtime.Store.ResetFailureCount(schedName)
	}
}

// executeChainSteps runs chain steps sequentially, passing each step's output
// as prev_output to the next.
func (s *Scheduler) executeChainSteps(ctx context.Context, pkg *core.SkillPackage, initialOutput string) error {
	chain, err := s.pkgManager.LoadChain(pkg)
	if err != nil {
		return fmt.Errorf("load chain: %w", err)
	}

	prevOutput := initialOutput
	for _, step := range chain {
		// Model priority: chain step > chain package meta > session default.
		model := step.Model
		if model == "" {
			model = step.Package.Meta.Model
		}
		output, err := s.executePackageCode(ctx, &step.Package, step.Code, prevOutput, model)
		if err != nil {
			return fmt.Errorf("chain step %q failed: %w", step.Package.Meta.ID, err)
		}
		prevOutput = output
	}
	return nil
}

// executePackageCode runs a package's JavaScript code through the engine.
// prevOutput is injected as context for chain step execution.
// model overrides the session's default LLM model when non-empty.
func (s *Scheduler) executePackageCode(ctx context.Context, pkg *core.SkillPackage, code, prevOutput, model string) (string, error) {
	text := "skill:pkg:" + pkg.Meta.ID
	if prevOutput != "" {
		text += "\nprev_output:" + prevOutput
	}

	payload, _ := json.Marshal(core.ChatPayload{
		Text:   text,
		ChatID: "scheduler",
	})
	event := core.Event{
		Type:    core.EventDesktop,
		Payload: payload,
	}

	var opts *RunOptions
	if model != "" {
		opts = &RunOptions{ModelOverride: model}
	}
	return s.runtime.Run(ctx, event, opts)
}

func (s *Scheduler) isDue(skill *core.SkillManifest) bool {
	return s.skillScheduleState(skill, time.Now()).Due
}

func (s *Scheduler) skillScheduleState(skill *core.SkillManifest, now time.Time) SkillScheduleState {
	lastRun, err := s.runtime.Store.GetLastRun(skill.Name)
	if err != nil {
		return SkillScheduleState{}
	}

	failCount, _ := s.runtime.Store.GetFailureCount(skill.Name)
	return SkillScheduleStateForLocation(skill, lastRun, failCount, now, s.scheduleTimezone().Location)
}

func (s *Scheduler) claimScheduledRun(jobKey, jobType, jobID, triggerType string, dueAt time.Time) (*store.ScheduledRun, bool, error) {
	if s == nil || s.runtime == nil || s.runtime.Store == nil {
		return nil, true, nil
	}
	return s.runtime.Store.ClaimScheduledRun(store.ClaimScheduledRunRequest{
		JobKey:        jobKey,
		JobType:       jobType,
		JobID:         jobID,
		TriggerType:   triggerType,
		DueAt:         dueAt,
		Now:           time.Now(),
		LeaseDuration: scheduledRunLeaseDuration,
	})
}

func (s *Scheduler) releaseScheduledRun(run *store.ScheduledRun, reason string) {
	if run == nil || s == nil || s.runtime == nil || s.runtime.Store == nil {
		return
	}
	if ok, err := s.runtime.Store.ReleaseScheduledRunClaim(run.ID, run.ClaimToken, time.Now(), reason); err != nil {
		slog.Error("scheduler: release scheduled run claim failed", "run_id", run.ID, "error", err)
	} else if !ok {
		slog.Warn("scheduler: scheduled run claim was not released", "run_id", run.ID)
	}
}

func (s *Scheduler) finishScheduledRun(run *store.ScheduledRun, status string, runErr error) bool {
	if run == nil || s == nil || s.runtime == nil || s.runtime.Store == nil {
		return true
	}
	errorClass, errorMessage := scheduledRunErrorDetails(runErr)
	if ok, err := s.runtime.Store.FinishScheduledRun(run.ID, run.ClaimToken, status, errorClass, errorMessage, time.Now()); err != nil {
		slog.Error("scheduler: finish scheduled run failed", "run_id", run.ID, "status", status, "error", err)
		return false
	} else if !ok {
		slog.Warn("scheduler: scheduled run claim was not finished", "run_id", run.ID, "status", status)
		return false
	}
	return true
}

func (s *Scheduler) setLastRunFromScheduledRun(jobKey string, run *store.ScheduledRun) {
	if run == nil || s == nil || s.runtime == nil || s.runtime.Store == nil {
		return
	}
	lastRunAt := time.Now().UTC()
	if run.StartedAt != nil {
		lastRunAt = run.StartedAt.UTC()
	} else if run.ClaimedAt != nil {
		lastRunAt = run.ClaimedAt.UTC()
	}
	if err := s.runtime.Store.SetLastRun(jobKey, lastRunAt); err != nil {
		slog.Error("scheduler: SetLastRun failed after scheduled run",
			"job_key", jobKey, "run_id", run.ID, "last_run_at", lastRunAt, "due_at", run.DueAt, "error", err)
	}
}

func scheduledRunErrorDetails(err error) (string, string) {
	if err == nil {
		return "", ""
	}
	if errors.Is(err, ErrRuntimeAdmissionBusy) {
		return "runtime_admission_busy", err.Error()
	}
	return strings.TrimPrefix(fmt.Sprintf("%T", err), "*"), err.Error()
}

func (s *Scheduler) runSkill(ctx context.Context, sk *core.SkillManifestWithCode) {
	s.runSkillWithScheduledRun(ctx, sk, nil)
}

func (s *Scheduler) runSkillWithScheduledRun(ctx context.Context, sk *core.SkillManifestWithCode, run *store.ScheduledRun) {
	// Create a synthetic event
	payload, _ := json.Marshal(core.ChatPayload{
		Text:   "skill:" + sk.Manifest.Name,
		ChatID: "scheduler",
	})
	event := core.Event{
		Type:    core.EventDesktop,
		Payload: payload,
	}

	admissionCtx, admissionLease, err := s.runtime.acquireTurnAdmission(ctx, event)
	if err != nil {
		if errors.Is(err, ErrRuntimeAdmissionBusy) {
			slog.Warn("scheduler: account runtime busy, leaving skill due", "name", sk.Manifest.Name)
		} else {
			slog.Error("scheduler: admission failed, leaving skill due", "name", sk.Manifest.Name, "error", err)
		}
		s.releaseScheduledRun(run, err.Error())
		return
	}
	defer admissionLease.Release()

	if run == nil {
		if err := s.runtime.Store.SetLastRun(sk.Manifest.Name, time.Now()); err != nil {
			slog.Error("scheduler: SetLastRun failed, aborting to prevent duplicate execution",
				"name", sk.Manifest.Name, "trigger", sk.Manifest.Trigger.Type, "error", err)
			return
		}
	}

	target := sk.Manifest.Trigger.Delivery
	runCtx := ContextWithDeliveryTarget(admissionCtx, target)
	state := &deliveryState{}
	runCtx = contextWithDeliveryState(runCtx, state)
	output, err := s.runtime.Run(runCtx, event, nil)
	if err != nil {
		slog.Error("scheduler: skill execution failed", "name", sk.Manifest.Name, "error", err)
		if s.finishScheduledRun(run, store.ScheduledRunStatusFailed, err) {
			s.setLastRunFromScheduledRun(sk.Manifest.Name, run)
			_ = s.runtime.Store.IncrementFailureCount(sk.Manifest.Name)
		}

		// Auto-delete one-shot skills even on failure.
		if sk.Manifest.Trigger.Type == "once" {
			if delErr := core.DeleteSkillFrom(s.runtime.BaseDir, sk.Manifest.Name); delErr != nil {
				slog.Error("scheduler: failed to delete one-shot skill after failure", "name", sk.Manifest.Name, "error", delErr)
			}
			return
		}
		return
	}

	if strings.TrimSpace(output) != "" && !notificationSent(runCtx) && s.runtime.Notifier != nil && !target.IsZero() {
		if err := s.runtime.Notifier.SendNotification(ctx, target, output); err != nil {
			slog.Error("scheduler: skill output delivery failed", "name", sk.Manifest.Name, "error", err)
			if s.finishScheduledRun(run, store.ScheduledRunStatusFailed, err) {
				s.setLastRunFromScheduledRun(sk.Manifest.Name, run)
				_ = s.runtime.Store.IncrementFailureCount(sk.Manifest.Name)
			}
			if sk.Manifest.Trigger.Type == "once" {
				if delErr := core.DeleteSkillFrom(s.runtime.BaseDir, sk.Manifest.Name); delErr != nil {
					slog.Error("scheduler: failed to delete one-shot skill after delivery failure", "name", sk.Manifest.Name, "error", delErr)
				}
			}
			return
		}
	}
	if s.finishScheduledRun(run, store.ScheduledRunStatusSucceeded, nil) {
		s.setLastRunFromScheduledRun(sk.Manifest.Name, run)
		_ = s.runtime.Store.ResetFailureCount(sk.Manifest.Name)
	}

	// Delete one-shot skills after successful execution
	if sk.Manifest.Trigger.Type == "once" {
		if delErr := core.DeleteSkillFrom(s.runtime.BaseDir, sk.Manifest.Name); delErr != nil {
			slog.Error("scheduler: failed to delete one-shot skill after success", "name", sk.Manifest.Name, "error", delErr)
		} else {
			slog.Info("scheduler: one-shot skill completed and deleted", "name", sk.Manifest.Name)
		}
	}
}

// parseCronInterval converts cron expressions to durations.
// Supports: "every 10m", "every 2h", "every 1d", and standard 5-field cron
// expressions (parsed via robfig/cron/v3 to compute next-fire interval).
func parseCronInterval(expr string) time.Duration {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return 0
	}

	// Simple "every Xm/h/d" format — kept for backward compatibility.
	if strings.HasPrefix(expr, "every ") {
		spec := strings.TrimPrefix(expr, "every ")
		d, err := time.ParseDuration(spec)
		if err == nil {
			return d
		}
		if strings.HasSuffix(spec, "d") {
			spec = strings.TrimSuffix(spec, "d")
			d, err = time.ParseDuration(spec + "h")
			if err == nil {
				return d * 24
			}
		}
	}

	// Standard 5-field cron: use robfig/cron/v3 to compute interval
	// between next two fires.
	schedule, err := parseCronSchedule(expr)
	if err != nil {
		return 0
	}
	now := time.Now()
	next1 := schedule.Next(now)
	next2 := schedule.Next(next1)
	return next2.Sub(next1)
}

// cronIsDue returns true if the cron expression is due for execution.
// For simple "every" expressions it uses duration comparison.
// For standard 5-field cron it uses schedule.Next(lastRun) directly, which
// correctly handles non-uniform schedules (monthly, weekday-only, DST).
func cronIsDue(expr string, lastRun *time.Time) bool {
	return cronIsDueAt(expr, lastRun, time.Now())
}

func cronIsDueAt(expr string, lastRun *time.Time, now time.Time) bool {
	return cronIsDueAtInLocation(expr, lastRun, now, now.Location())
}

func cronIsDueAtInLocation(expr string, lastRun *time.Time, now time.Time, loc *time.Location) bool {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return false
	}

	if lastRun == nil {
		return true
	}

	// Simple "every" expressions — uniform interval, duration comparison is correct.
	if strings.HasPrefix(expr, "every ") {
		interval := parseCronInterval(expr)
		return interval > 0 && !now.Before(lastRun.Add(interval))
	}

	// Standard cron: compute the next fire time after lastRun and check if it's past.
	schedule, err := parseCronSchedule(expr)
	if err != nil {
		return false
	}
	if loc == nil {
		loc = time.Local
	}
	nextFire := schedule.Next(lastRun.In(loc)).UTC()
	return !now.UTC().Before(nextFire)
}

func parseCronSchedule(expr string) (cron.Schedule, error) {
	parser := cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	return parser.Parse(expr)
}

func SkillScheduleStateFor(skill *core.SkillManifest, lastRun *time.Time, failureCount int, now time.Time) SkillScheduleState {
	return SkillScheduleStateForLocation(skill, lastRun, failureCount, now, now.Location())
}

func SkillScheduleStateForLocation(skill *core.SkillManifest, lastRun *time.Time, failureCount int, now time.Time, loc *time.Location) SkillScheduleState {
	state := SkillScheduleState{LastRun: lastRun, FailureCount: failureCount}
	if skill == nil {
		return state
	}
	if loc == nil {
		loc = time.Local
	}

	switch skill.Trigger.Type {
	case "once":
		if lastRun != nil {
			return state
		}
		if strings.TrimSpace(skill.Trigger.RunAt) == "" {
			next := now.UTC()
			state.NextRun = &next
			state.Due = true
			return state
		}
		runAt, err := time.Parse(time.RFC3339, skill.Trigger.RunAt)
		if err != nil {
			return state
		}
		runAt = runAt.UTC()
		state.NextRun = &runAt
		state.Due = !now.Before(runAt)
		return state

	case "schedule":
		next, ok := nextScheduledRunForTriggerInLocation(skill.Trigger, lastRun, now, loc)
		if !ok {
			return state
		}
		if failureCount > 0 && lastRun != nil {
			backoff := time.Duration(1<<min(failureCount, 6)) * time.Minute
			backoffUntil := lastRun.Add(backoff).UTC()
			if backoffUntil.After(next) {
				next = backoffUntil
			}
		}
		state.NextRun = &next
		state.Due = !now.Before(next)
		return state
	default:
		return state
	}
}

func nextScheduledRunForTrigger(trigger core.SkillTrigger, lastRun *time.Time, now time.Time) (time.Time, bool) {
	return nextScheduledRunForTriggerInLocation(trigger, lastRun, now, now.Location())
}

func nextScheduledRunForTriggerInLocation(trigger core.SkillTrigger, lastRun *time.Time, now time.Time, loc *time.Location) (time.Time, bool) {
	if lastRun == nil && !trigger.RunOnInstall {
		if runAt, ok := parseScheduleRunAt(trigger.RunAt); ok {
			return runAt, true
		}
		// Older and hand-authored schedule manifests do not carry a first-run
		// anchor. Keep that compatibility path runnable instead of recomputing a
		// future next-run forever on each scheduler tick.
		return nextScheduledRunInLocation(trigger.Cron, nil, now, loc)
	}
	return nextScheduledRunInLocation(trigger.Cron, lastRun, now, loc)
}

func nextScheduledRun(expr string, lastRun *time.Time, now time.Time) (time.Time, bool) {
	return nextScheduledRunInLocation(expr, lastRun, now, now.Location())
}

func nextScheduledRunInLocation(expr string, lastRun *time.Time, now time.Time, loc *time.Location) (time.Time, bool) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return time.Time{}, false
	}
	if loc == nil {
		loc = time.Local
	}
	if lastRun == nil {
		return now.UTC(), true
	}
	if strings.HasPrefix(expr, "every ") {
		interval := parseCronInterval(expr)
		if interval <= 0 {
			return time.Time{}, false
		}
		return lastRun.Add(interval).UTC(), true
	}
	schedule, err := parseCronSchedule(expr)
	if err != nil {
		return time.Time{}, false
	}
	return schedule.Next(lastRun.In(loc)).UTC(), true
}

func firstScheduledRunAfter(expr string, now time.Time) (time.Time, bool) {
	return firstScheduledRunAfterInLocation(expr, now, now.Location())
}

func firstScheduledRunAfterInLocation(expr string, now time.Time, loc *time.Location) (time.Time, bool) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return time.Time{}, false
	}
	if loc == nil {
		loc = time.Local
	}
	if strings.HasPrefix(expr, "every ") {
		interval := parseCronInterval(expr)
		if interval <= 0 {
			return time.Time{}, false
		}
		return now.Add(interval).UTC(), true
	}
	schedule, err := parseCronSchedule(expr)
	if err != nil {
		return time.Time{}, false
	}
	return schedule.Next(now.In(loc)).UTC(), true
}

// FirstScheduledRunAfter returns the first future fire time for a recurring
// schedule expression. It is used by API paths that create scheduled skills.
func FirstScheduledRunAfter(expr string, now time.Time) (time.Time, bool) {
	return firstScheduledRunAfter(expr, now)
}

func FirstScheduledRunAfterInLocation(expr string, now time.Time, loc *time.Location) (time.Time, bool) {
	return firstScheduledRunAfterInLocation(expr, now, loc)
}

func parseScheduleRunAt(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	runAt, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, false
	}
	return runAt.UTC(), true
}
