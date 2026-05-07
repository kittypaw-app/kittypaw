package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/jinto/kittypaw/core"
)

// Scheduler runs scheduled skills at their configured intervals.
type Scheduler struct {
	session    *Session
	pkgManager *core.PackageManager
	stop       chan struct{}
	stopOnce   sync.Once
	startOnce  sync.Once
	loops      sync.WaitGroup
	inflight   sync.Map       // skill name → struct{}: prevents concurrent runs of the same skill
	wg         sync.WaitGroup // tracks in-flight runSkill goroutines for graceful drain
}

// NewScheduler creates a scheduler that uses the given session for execution.
// pkgManager may be nil if packages are not configured.
func NewScheduler(session *Session, pkgManager *core.PackageManager) *Scheduler {
	return &Scheduler{
		session:    session,
		pkgManager: pkgManager,
		stop:       make(chan struct{}),
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

func (s *Scheduler) startReflectionLoop(ctx context.Context) {
	if s.session == nil || s.session.Config == nil || !s.session.Config.Reflection.Enabled {
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
			RecoverAccountPanic(s.session, "scheduler.tick", r)
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
			RecoverAccountPanic(s.session, "scheduler.reflection", r)
		}
	}()
	if !s.isReflectionDue() {
		return
	}
	slog.Info("scheduler: running reflection cycle")
	if err := RunReflectionCycle(ctx, s.session, &s.session.Config.Reflection); err != nil {
		slog.Error("scheduler: reflection cycle failed", "error", err)
	}

	// After reflection, check evolution trigger conditions.
	if s.session.Config.Evolution.Enabled {
		profiles, err := s.session.Store.ListActiveStaff()
		if err == nil {
			for _, p := range profiles {
				_ = TriggerEvolution(ctx, p.ID, s.session, &s.session.Config.Evolution)
			}
		}
	}

	// Weekly report: if today matches WeeklyReportDay, emit the report
	// to the account's first configured Telegram channel and allowed chat id.
	s.deliverWeeklyReport(ctx)

	// Record last run.
	_ = s.session.Store.SetLastRun("__reflection__", time.Now())
}

// deliverWeeklyReport sends the topic-preference summary on the configured
// weekday. Idempotency is anchored on `__weekly_report__` last-run so a
// server restart on the same day does not double-send. Best-effort: any
// dispatch failure is logged warn and does not abort the reflection cycle.
func (s *Scheduler) deliverWeeklyReport(ctx context.Context) {
	cfg := s.session.Config.Reflection
	// time.Weekday: Sunday=0..Saturday=6. Default 0 (Sunday) matches the
	// docs/index.html promise "매주 일요일 주간 관심사 리포트".
	if int(time.Now().Weekday()) != int(cfg.WeeklyReportDay) {
		return
	}
	lastRun, _ := s.session.Store.GetLastRun("__weekly_report__")
	if lastRun != nil && time.Since(*lastRun) < 23*time.Hour {
		return
	}

	prefs, err := s.session.Store.ListUserContextPrefix("topic_pref:")
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
	if err := SendTelegramText(ctx, token, chatID, report); err != nil {
		slog.Warn("weekly report: telegram dispatch failed", "error", err)
		return
	}
	slog.Info("weekly report: delivered")
	_ = s.session.Store.SetLastRun("__weekly_report__", time.Now())
}

// firstTelegramTarget returns the (bot_token, chat_id) of the account's
// first telegram channel, or ("", "") when none is configured.
func (s *Scheduler) firstTelegramTarget() (string, string) {
	cfg := s.session.Config
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

// isReflectionDue returns true if the reflection cycle should run now.
// Checks: has it been at least 23 hours since last run, and is the current
// hour within the configured window (default: 3am).
func (s *Scheduler) isReflectionDue() bool {
	lastRun, _ := s.session.Store.GetLastRun("__reflection__")
	if lastRun != nil && time.Since(*lastRun) < 23*time.Hour {
		return false
	}

	// Default: run at 3am.
	targetHour := 3
	// TODO: parse config.Reflection.Cron for target hour
	return time.Now().Hour() == targetHour
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
	skills, err := core.LoadAllSkillsFrom(s.session.BaseDir)
	if err != nil {
		slog.Error("scheduler: load skills failed", "error", err)
		return
	}

	for _, sk := range skills {
		if !sk.Skill.Enabled {
			continue
		}
		if sk.Skill.Trigger.Type != "schedule" && sk.Skill.Trigger.Type != "once" {
			continue
		}

		if s.isDue(&sk.Skill) {
			if _, loaded := s.inflight.LoadOrStore(sk.Skill.Name, struct{}{}); loaded {
				slog.Debug("scheduler: skill still running, skipping", "name", sk.Skill.Name)
				continue
			}
			slog.Info("scheduler: running skill", "name", sk.Skill.Name, "trigger", sk.Skill.Trigger.Type)
			s.wg.Add(1)
			go func(sk core.SkillWithCode) {
				defer s.wg.Done()
				defer s.inflight.Delete(sk.Skill.Name)
				runWithAccountRecover(s.session, "scheduler.runSkill", func() {
					s.runSkill(ctx, &sk)
				})
			}(sk)
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

	for _, pkg := range packages {
		if pkg.Meta.Cron == "" {
			continue
		}

		schedName := "pkg:" + pkg.Meta.ID
		lastRun, _ := s.session.Store.GetLastRun(schedName)
		if !cronIsDue(pkg.Meta.Cron, lastRun) {
			continue
		}

		if _, loaded := s.inflight.LoadOrStore(schedName, struct{}{}); loaded {
			continue
		}

		slog.Info("scheduler: running package", "id", pkg.Meta.ID)
		s.wg.Add(1)
		go func(pkg core.SkillPackage) {
			defer s.wg.Done()
			defer s.inflight.Delete("pkg:" + pkg.Meta.ID)
			runWithAccountRecover(s.session, "scheduler.runPackage", func() {
				s.runPackage(ctx, &pkg)
			})
		}(pkg)
	}
}

// runPackage executes a package's main.js directly, then dispatches the output
// to Telegram if the package has telegram config. Packages return their formatted
// message — the scheduler handles delivery.
func (s *Scheduler) runPackage(ctx context.Context, pkg *core.SkillPackage) {
	schedName := "pkg:" + pkg.Meta.ID
	if err := s.session.Store.SetLastRun(schedName, time.Now()); err != nil {
		slog.Error("scheduler: SetLastRun failed for package", "id", pkg.Meta.ID, "error", err)
		return
	}

	// Execute package directly (no LLM loop).
	resultJSON, err := runSkillOrPackage(ctx, pkg.Meta.ID, s.session)
	if err != nil {
		slog.Error("scheduler: package execution failed", "id", pkg.Meta.ID, "error", err)
		_ = s.session.Store.IncrementFailureCount(schedName)
		return
	}

	// Parse the result to get the output message.
	var result map[string]any
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		slog.Error("scheduler: parse result failed", "id", pkg.Meta.ID, "error", err)
		return
	}
	if errMsg, ok := result["error"].(string); ok && errMsg != "" {
		slog.Error("scheduler: package returned error", "id", pkg.Meta.ID, "error", errMsg)
		_ = s.session.Store.IncrementFailureCount(schedName)
		return
	}

	output, _ := result["output"].(string)
	_ = s.session.Store.ResetFailureCount(schedName)

	// Dispatch output to Telegram if configured.
	if output != "" && s.pkgManager != nil {
		config, _ := s.pkgManager.GetConfig(pkg.Meta.ID)
		token := config["telegram_token"]
		chatID := config["chat_id"]
		if token != "" && chatID != "" {
			if err := SendTelegramText(ctx, token, chatID, output); err != nil {
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
	return s.session.Run(ctx, event, opts)
}

func (s *Scheduler) isDue(skill *core.Skill) bool {
	lastRun, err := s.session.Store.GetLastRun(skill.Name)
	if err != nil {
		return false
	}

	// Check failure backoff
	failCount, _ := s.session.Store.GetFailureCount(skill.Name)
	if failCount > 0 {
		backoff := time.Duration(1<<min(failCount, 6)) * time.Minute
		if lastRun != nil && time.Since(*lastRun) < backoff {
			return false
		}
	}

	if skill.Trigger.Type == "once" {
		// One-shot: run if never run before and RunAt has passed
		if lastRun != nil {
			return false // Already ran
		}
		if skill.Trigger.RunAt != "" {
			runAt, err := time.Parse(time.RFC3339, skill.Trigger.RunAt)
			if err != nil {
				return false
			}
			return time.Now().After(runAt)
		}
		return true
	}

	// Schedule type: use parsed cron schedule for accurate due check.
	return cronIsDue(skill.Trigger.Cron, lastRun)
}

func (s *Scheduler) runSkill(ctx context.Context, sk *core.SkillWithCode) {
	if err := s.session.Store.SetLastRun(sk.Skill.Name, time.Now()); err != nil {
		slog.Error("scheduler: SetLastRun failed, aborting to prevent duplicate execution",
			"name", sk.Skill.Name, "trigger", sk.Skill.Trigger.Type, "error", err)
		return
	}

	// Create a synthetic event
	payload, _ := json.Marshal(core.ChatPayload{
		Text:   "skill:" + sk.Skill.Name,
		ChatID: "scheduler",
	})
	event := core.Event{
		Type:    core.EventDesktop,
		Payload: payload,
	}

	_, err := s.session.Run(ctx, event, nil)
	if err != nil {
		slog.Error("scheduler: skill execution failed", "name", sk.Skill.Name, "error", err)
		_ = s.session.Store.IncrementFailureCount(sk.Skill.Name)

		// Auto-delete one-shot skills even on failure.
		if sk.Skill.Trigger.Type == "once" {
			if delErr := core.DeleteSkillFrom(s.session.BaseDir, sk.Skill.Name); delErr != nil {
				slog.Error("scheduler: failed to delete one-shot skill after failure", "name", sk.Skill.Name, "error", delErr)
			}
			return
		}
		return
	}

	_ = s.session.Store.ResetFailureCount(sk.Skill.Name)

	// Delete one-shot skills after successful execution
	if sk.Skill.Trigger.Type == "once" {
		if delErr := core.DeleteSkillFrom(s.session.BaseDir, sk.Skill.Name); delErr != nil {
			slog.Error("scheduler: failed to delete one-shot skill after success", "name", sk.Skill.Name, "error", delErr)
		} else {
			slog.Info("scheduler: one-shot skill completed and deleted", "name", sk.Skill.Name)
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
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	schedule, err := parser.Parse(expr)
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
		return interval > 0 && time.Since(*lastRun) >= interval
	}

	// Standard cron: compute the next fire time after lastRun and check if it's past.
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	schedule, err := parser.Parse(expr)
	if err != nil {
		return false
	}
	nextFire := schedule.Next(*lastRun)
	return time.Now().After(nextFire)
}
