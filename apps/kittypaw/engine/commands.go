package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/store"
)

type slashCommand struct {
	Name          string
	Usage         string
	Summary       string
	Aliases       []string
	HiddenAliases []string
	Risk          string
	History       bool
	Details       []string
	Handler       slashCommandHandler
}

type slashCommandResult struct {
	Text          string
	RecordHistory bool
}

type slashCommandHandler func(context.Context, []string, *AccountRuntime) slashCommandResult

var slashCommandRegistry = []slashCommand{
	{
		Name:    "/help",
		Usage:   "/help",
		Summary: "도움말 표시",
		Aliases: []string{"/?"},
		Risk:    "safe",
	},
	{
		Name:    "/status",
		Usage:   "/status",
		Summary: "오늘 실행 통계 확인",
		Risk:    "read",
		Handler: func(_ context.Context, _ []string, s *AccountRuntime) slashCommandResult {
			return slashCommandText(handleStatus(s))
		},
	},
	{
		Name:    "/skills",
		Usage:   "/skills",
		Summary: "스킬 목록 표시",
		Risk:    "read",
		Handler: func(_ context.Context, _ []string, s *AccountRuntime) slashCommandResult {
			return slashCommandText(handleSkills(s))
		},
	},
	{
		Name:    "/schedule",
		Usage:   "/schedule <list|show|pause|resume|delete>",
		Summary: "예약 스킬 조회 및 운영",
		Risk:    "mixed",
		History: true,
		Details: []string{
			"/schedule list",
			"/schedule show <name>",
			"/schedule pause <name>",
			"/schedule resume <name>",
			"/schedule delete <name>",
		},
		Handler: func(_ context.Context, args []string, s *AccountRuntime) slashCommandResult {
			return slashCommandResult{Text: handleScheduleCommand(args, s), RecordHistory: scheduleCommandRecordsHistory(args)}
		},
	},
	{
		Name:    "/run",
		Usage:   "/run <name>",
		Summary: "스킬 또는 패키지 실행",
		Risk:    "execute",
		History: true,
		Handler: func(ctx context.Context, args []string, s *AccountRuntime) slashCommandResult {
			if len(args) == 0 {
				return slashCommandText("사용법: /run <skill-name>")
			}
			return slashCommandResult{Text: handleRun(ctx, args[0], s), RecordHistory: true}
		},
	},
	{
		Name:    "/teach",
		Usage:   "/teach <설명>",
		Summary: "새 스킬 생성",
		Risk:    "write",
		History: true,
		Handler: func(ctx context.Context, args []string, s *AccountRuntime) slashCommandResult {
			if len(args) == 0 {
				return slashCommandText("사용법: /teach <설명>")
			}
			return slashCommandResult{Text: handleTeach(ctx, strings.Join(args, " "), s), RecordHistory: true}
		},
	},
	{
		Name:    "/staff",
		Usage:   "/staff <current|list|show|use|routes|route|source-routes|source-route|hire|cancel>",
		Summary: "staff 상태 조회 및 전환",
		Risk:    "mixed",
		History: true,
		Details: []string{
			"/staff current",
			"/staff list",
			"/staff show <staff-id>",
			"/staff use <staff-id>",
			"/staff routes",
			"/staff route <conversation-id> <staff-id>",
			"/staff source-routes",
			"/staff source-route add <source> <chat_id|source_session_id> <exact|prefix|glob> <pattern> <staff-id>",
			"/staff source-route delete <id>",
			"/staff hire <역할>",
			"/staff cancel",
		},
		Handler: func(ctx context.Context, args []string, s *AccountRuntime) slashCommandResult {
			return slashCommandResult{Text: handleStaffCommand(ctx, args, s), RecordHistory: staffCommandRecordsHistory(args)}
		},
	},
	{
		Name:    "/model",
		Usage:   "/model [id]",
		Summary: "현재 LLM 정보 표시 또는 런타임 모델 변경",
		Risk:    "runtime",
		History: true,
		Handler: func(_ context.Context, args []string, s *AccountRuntime) slashCommandResult {
			record := modelCommandRecordsHistory(args, s)
			return slashCommandResult{Text: handleModel(args, s), RecordHistory: record}
		},
	},
	{
		Name:          "/conversation",
		Usage:         "/conversation [rename <title>]",
		Summary:       "현재 대화 진단 정보 및 제목 변경",
		HiddenAliases: []string{"/session"},
		Risk:          "mixed",
		History:       true,
		Details: []string{
			"/conversation",
			"/conversation rename <title>",
		},
		Handler: func(ctx context.Context, args []string, s *AccountRuntime) slashCommandResult {
			return slashCommandResult{Text: handleConversationCommand(ctx, args, s), RecordHistory: conversationCommandRecordsHistory(args)}
		},
	},
	{
		Name:    "/context",
		Usage:   "/context",
		Summary: "현재 prompt/context 크기 진단",
		Risk:    "read",
		Handler: func(ctx context.Context, _ []string, s *AccountRuntime) slashCommandResult {
			return slashCommandText(handleContext(ctx, s))
		},
	},
	{
		Name:    "/compact",
		Usage:   "/compact [keep_recent]",
		Summary: "현재 대화 prompt compaction 실행",
		Risk:    "write",
		History: true,
		Handler: func(ctx context.Context, args []string, s *AccountRuntime) slashCommandResult {
			text, record := handleCompactCommand(ctx, args, s)
			return slashCommandResult{Text: text, RecordHistory: record}
		},
	},
	{
		Name:    "/projects",
		Usage:   "/projects",
		Summary: "프로젝트 목록",
		Risk:    "read",
		Handler: func(_ context.Context, _ []string, s *AccountRuntime) slashCommandResult {
			return slashCommandText(handleProjectsCommand(s))
		},
	},
	{
		Name:    "/project",
		Usage:   "/project <current|show|use|new|settings>",
		Summary: "프로젝트 조회 및 현재 프로젝트 선택",
		Risk:    "mixed",
		History: true,
		Details: []string{
			"/project current",
			"/project show <key>",
			"/project use <key>",
			"/project new",
			"/project settings",
		},
		Handler: func(ctx context.Context, args []string, s *AccountRuntime) slashCommandResult {
			return slashCommandResult{Text: handleProjectCommand(ctx, args, s), RecordHistory: projectCommandRecordsHistory(args)}
		},
	},
	{
		Name:    "/tickets",
		Usage:   "/tickets [project-key]",
		Summary: "현재 또는 지정 project의 ticket 목록",
		Risk:    "read",
		Handler: func(ctx context.Context, args []string, s *AccountRuntime) slashCommandResult {
			return slashCommandText(handleTicketsCommand(ctx, args, s))
		},
	},
	{
		Name:    "/ticket",
		Usage:   "/ticket <show|chat|job|move|block|done>",
		Summary: "ticket 조회 및 상태 변경",
		Risk:    "mixed",
		History: true,
		Details: []string{
			"/ticket show <key>",
			"/ticket chat <key>",
			"/ticket job <key>",
			"/ticket move <key> <status>",
			"/ticket block <key> <reason>",
			"/ticket done <key>",
		},
		Handler: func(_ context.Context, args []string, s *AccountRuntime) slashCommandResult {
			return slashCommandResult{Text: handleTicketCommand(args, s), RecordHistory: ticketCommandRecordsHistory(args)}
		},
	},
}

func registeredSlashCommands() []slashCommand {
	out := make([]slashCommand, len(slashCommandRegistry))
	copy(out, slashCommandRegistry)
	return out
}

func slashCommandText(text string) slashCommandResult {
	return slashCommandResult{Text: text}
}

// tryHandleCommand checks if the event text is a slash command.
// Returns (response, true) if handled, ("", false) otherwise.
func tryHandleCommand(ctx context.Context, text string, s *AccountRuntime) (string, bool) {
	result, handled := tryHandleCommandResult(ctx, text, s)
	return result.Text, handled
}

func tryHandleCommandResult(ctx context.Context, text string, s *AccountRuntime) (slashCommandResult, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return slashCommandResult{}, false
	}

	parts := strings.Fields(text)
	if len(parts) == 0 {
		return slashCommandText(unknownSlashCommandMessage("/")), true
	}
	cmd := strings.ToLower(parts[0])
	if spec, ok := lookupSlashCommand(cmd); ok {
		if spec.Name == "/help" {
			return slashCommandText(handleHelp()), true
		}
		if spec.Handler == nil {
			return slashCommandText(fmt.Sprintf("%s 명령은 아직 사용할 수 없습니다.", spec.Name)), true
		}
		return spec.Handler(ctx, parts[1:], s), true
	}
	return slashCommandText(unknownSlashCommandMessage(cmd)), true
}

func handleHelp() string {
	var sb strings.Builder
	sb.WriteString("KittyPaw 명령어:\n")
	for _, cmd := range slashCommandRegistry {
		history := "기록: no"
		if cmd.History {
			history = "기록: 조건부"
		}
		fmt.Fprintf(&sb, "%s — %s (risk: %s, %s)\n", cmd.Usage, cmd.Summary, cmd.Risk, history)
		if len(cmd.Aliases) > 0 {
			fmt.Fprintf(&sb, "  aliases: %s\n", strings.Join(cmd.Aliases, ", "))
		}
		for _, detail := range cmd.Details {
			fmt.Fprintf(&sb, "  - %s\n", detail)
		}
	}
	sb.WriteString("\n알 수 없는 /명령은 실행하지 않고 이 도움말을 안내합니다.")
	return strings.TrimRight(sb.String(), "\n")
}

func lookupSlashCommand(name string) (slashCommand, bool) {
	for _, cmd := range slashCommandRegistry {
		if cmd.Name == name {
			return cmd, true
		}
		for _, alias := range cmd.Aliases {
			if alias == name {
				return cmd, true
			}
		}
		for _, alias := range cmd.HiddenAliases {
			if alias == name {
				return cmd, true
			}
		}
	}
	return slashCommand{}, false
}

func unknownSlashCommandMessage(name string) string {
	msg := fmt.Sprintf("알 수 없는 명령입니다: %s", name)
	if suggestion := suggestSlashCommand(name); suggestion != "" {
		msg += fmt.Sprintf("\n혹시 %s 명령을 찾으셨나요?", suggestion)
	}
	return msg + "\n/help로 사용 가능한 명령을 확인하세요."
}

func suggestSlashCommand(name string) string {
	best := ""
	bestDistance := 99
	for _, cmd := range slashCommandRegistry {
		for _, candidate := range append([]string{cmd.Name}, cmd.Aliases...) {
			d := slashCommandDistance(name, candidate)
			if strings.HasPrefix(candidate, name) || strings.HasPrefix(name, candidate) {
				d--
			}
			if d < bestDistance {
				bestDistance = d
				best = cmd.Name
			}
		}
	}
	if bestDistance <= 2 {
		return best
	}
	return ""
}

func slashCommandDistance(a, b string) int {
	ar := []rune(a)
	br := []rune(b)
	prev := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i, ra := range ar {
		cur := make([]int, len(br)+1)
		cur[0] = i + 1
		for j, rb := range br {
			cost := 0
			if ra != rb {
				cost = 1
			}
			cur[j+1] = minInt(cur[j]+1, prev[j+1]+1, prev[j]+cost)
		}
		prev = cur
	}
	return prev[len(br)]
}

func minInt(values ...int) int {
	min := values[0]
	for _, value := range values[1:] {
		if value < min {
			min = value
		}
	}
	return min
}

func staffCommandRecordsHistory(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "current", "list", "show", "routes", "source-routes":
		return false
	case "source-route":
		if len(args) < 2 {
			return false
		}
		switch strings.ToLower(strings.TrimSpace(args[1])) {
		case "list", "ls":
			return false
		default:
			return true
		}
	default:
		return true
	}
}

func projectCommandRecordsHistory(args []string) bool {
	return len(args) > 0 && strings.EqualFold(args[0], "use") && len(args) >= 2
}

func modelCommandRecordsHistory(args []string, s *AccountRuntime) bool {
	if len(args) != 1 || s == nil || s.Config == nil {
		return false
	}
	id := strings.TrimSpace(args[0])
	if id == "" || id == currentModelID(s) {
		return false
	}
	return modelIDExists(id, s.Config.LLM.Models)
}

func ticketCommandRecordsHistory(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "job", "move", "block", "done":
		return true
	default:
		return false
	}
}

func handleStatus(s *AccountRuntime) string {
	stats, err := s.Store.TodayStats()
	if err != nil {
		return fmt.Sprintf("통계 조회 실패: %s", err)
	}
	runtimeLine := "runtime: unavailable"
	if s.Admission != nil {
		snapshot := s.Admission.Snapshot()
		runtimeLine = fmt.Sprintf(
			"runtime: running=%d queued=%d scope_running=%d scope_queued=%d",
			snapshot.AccountRunning,
			snapshot.AccountQueued,
			snapshot.ScopeRunning,
			snapshot.ScopeQueued,
		)
	}
	return fmt.Sprintf(
		"📊 오늘 실행 통계\n총 실행: %d\n성공: %d\n실패: %d\n토큰: %d\n%s",
		stats.TotalRuns, stats.Successful, stats.Failed, stats.TotalTokens, runtimeLine,
	)
}

func handleSkills(s *AccountRuntime) string {
	skills, err := core.LoadAllSkillsFrom(s.BaseDir)
	if err != nil {
		return fmt.Sprintf("스킬 목록 조회 실패: %s", err)
	}
	if len(skills) == 0 {
		return "등록된 스킬이 없습니다."
	}
	var sb strings.Builder
	sb.WriteString("📋 스킬 목록:\n")
	for _, s := range skills {
		status := "✅"
		if !s.Manifest.Enabled {
			status = "⛔"
		}
		sb.WriteString(fmt.Sprintf("  %s %s — %s\n", status, s.Manifest.Name, s.Manifest.Description))
	}
	return sb.String()
}

func handleScheduleCommand(args []string, s *AccountRuntime) string {
	if s == nil || strings.TrimSpace(s.BaseDir) == "" {
		return "schedule 관리를 위한 runtime이 준비되지 않았습니다."
	}
	if len(args) == 0 {
		return scheduleUsage()
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "list", "ls":
		return handleScheduleList(s)
	case "show":
		if len(args) != 2 {
			return "사용법: /schedule show <name>"
		}
		return handleScheduleShow(strings.TrimSpace(args[1]), s)
	case "pause":
		if len(args) != 2 {
			return "사용법: /schedule pause <name>"
		}
		return handleSchedulePause(strings.TrimSpace(args[1]), s)
	case "resume":
		if len(args) != 2 {
			return "사용법: /schedule resume <name>"
		}
		return handleScheduleResume(strings.TrimSpace(args[1]), s)
	case "delete", "remove":
		if len(args) != 2 {
			return "사용법: /schedule delete <name>"
		}
		return handleScheduleDelete(strings.TrimSpace(args[1]), s)
	default:
		return scheduleUsage()
	}
}

func scheduleUsage() string {
	return "사용법: /schedule <list|show|pause|resume|delete>"
}

func handleScheduleList(s *AccountRuntime) string {
	items, err := scheduledSkillItems(s)
	if err != nil {
		return fmt.Sprintf("schedule 목록 조회 실패: %s", err)
	}
	if len(items) == 0 {
		return "예약된 스킬이 없습니다."
	}
	tz := scheduleTimezoneForRuntime(s)
	var sb strings.Builder
	sb.WriteString("예약 스킬 목록:\n")
	fmt.Fprintf(&sb, "timezone: %s\n", tz.Name)
	now := time.Now()
	for _, item := range items {
		status := "enabled"
		if !item.Manifest.Enabled {
			status = "paused"
		}
		lastRun, failCount := scheduleStoreState(s, item.Manifest.Name)
		state := SkillScheduleStateForLocation(&item.Manifest, lastRun, failCount, now, tz.Location)
		fmt.Fprintf(&sb, "- %s [%s] trigger=%s next_run=%s last_run=%s failure_count=%d\n",
			item.Manifest.Name,
			status,
			item.Manifest.Trigger.Type,
			formatOptionalScheduleTimeInTimezone(state.NextRun, tz),
			formatOptionalScheduleTimeInTimezone(state.LastRun, tz),
			state.FailureCount,
		)
	}
	return strings.TrimRight(sb.String(), "\n")
}

func handleScheduleShow(name string, s *AccountRuntime) string {
	item, ok, err := loadScheduledSkillItem(s, name)
	if err != nil {
		return fmt.Sprintf("schedule 조회 실패: %s", err)
	}
	if !ok {
		return fmt.Sprintf("예약 스킬을 찾지 못했습니다: %s", name)
	}
	tz := scheduleTimezoneForRuntime(s)
	lastRun, failCount := scheduleStoreState(s, item.Manifest.Name)
	state := SkillScheduleStateForLocation(&item.Manifest, lastRun, failCount, time.Now(), tz.Location)
	var sb strings.Builder
	fmt.Fprintf(&sb, "name: %s\n", item.Manifest.Name)
	fmt.Fprintf(&sb, "description: %s\n", item.Manifest.Description)
	fmt.Fprintf(&sb, "enabled: %t\n", item.Manifest.Enabled)
	fmt.Fprintf(&sb, "timezone: %s\n", tz.Name)
	fmt.Fprintf(&sb, "trigger: %s\n", item.Manifest.Trigger.Type)
	if item.Manifest.Trigger.Cron != "" {
		fmt.Fprintf(&sb, "cron: %s\n", item.Manifest.Trigger.Cron)
	}
	if item.Manifest.Trigger.RunAt != "" {
		fmt.Fprintf(&sb, "run_at: %s\n", item.Manifest.Trigger.RunAt)
	}
	if item.Manifest.Trigger.RunOnInstall {
		sb.WriteString("run_on_install: true\n")
	}
	fmt.Fprintf(&sb, "next_run: %s\n", formatOptionalScheduleTimeInTimezone(state.NextRun, tz))
	fmt.Fprintf(&sb, "last_run: %s\n", formatOptionalScheduleTimeInTimezone(state.LastRun, tz))
	fmt.Fprintf(&sb, "failure_count: %d\n", state.FailureCount)
	if !item.Manifest.Trigger.Delivery.IsZero() {
		fmt.Fprintf(&sb, "delivery: %s\n", summarizeDeliveryTarget(item.Manifest.Trigger.Delivery))
	}
	if s.Store != nil {
		runs, err := s.Store.ListScheduledRunsForJob(item.Manifest.Name, 5)
		if err != nil {
			fmt.Fprintf(&sb, "recent_runs: error: %s", err)
			return strings.TrimRight(sb.String(), "\n")
		}
		sb.WriteString("recent_runs:\n")
		if len(runs) == 0 {
			sb.WriteString("- none\n")
		} else {
			for _, run := range runs {
				fmt.Fprintf(&sb, "- #%d %s due_at=%s attempt=%d", run.ID, run.Status, run.DueAt.Format(time.RFC3339), run.Attempt)
				if run.ErrorClass != "" {
					fmt.Fprintf(&sb, " error_class=%s", run.ErrorClass)
				}
				if run.FinishedAt != nil {
					fmt.Fprintf(&sb, " finished_at=%s", run.FinishedAt.Format(time.RFC3339))
				}
				sb.WriteByte('\n')
			}
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func handleSchedulePause(name string, s *AccountRuntime) string {
	if _, ok, err := loadScheduledSkillItem(s, name); err != nil {
		return fmt.Sprintf("schedule pause 실패: %s", err)
	} else if !ok {
		return fmt.Sprintf("예약 스킬을 찾지 못했습니다: %s", name)
	}
	if err := core.DisableSkillFrom(s.BaseDir, name); err != nil {
		return fmt.Sprintf("schedule pause 실패: %s", err)
	}
	return fmt.Sprintf("schedule paused: %s", name)
}

func handleScheduleResume(name string, s *AccountRuntime) string {
	if _, ok, err := loadScheduledSkillItem(s, name); err != nil {
		return fmt.Sprintf("schedule resume 실패: %s", err)
	} else if !ok {
		return fmt.Sprintf("예약 스킬을 찾지 못했습니다: %s", name)
	}
	if err := core.EnableSkillFrom(s.BaseDir, name); err != nil {
		return fmt.Sprintf("schedule resume 실패: %s", err)
	}
	return fmt.Sprintf("schedule resumed: %s", name)
}

func handleScheduleDelete(name string, s *AccountRuntime) string {
	if _, ok, err := loadScheduledSkillItem(s, name); err != nil {
		return fmt.Sprintf("schedule delete 실패: %s", err)
	} else if !ok {
		return fmt.Sprintf("예약 스킬을 찾지 못했습니다: %s", name)
	}
	if err := core.DeleteSkillFrom(s.BaseDir, name); err != nil {
		return fmt.Sprintf("schedule delete 실패: %s", err)
	}
	return fmt.Sprintf("schedule deleted: %s", name)
}

func scheduledSkillItems(s *AccountRuntime) ([]core.SkillManifestWithCode, error) {
	skills, err := core.LoadAllSkillsFrom(s.BaseDir)
	if err != nil {
		return nil, err
	}
	items := make([]core.SkillManifestWithCode, 0, len(skills))
	for _, skill := range skills {
		if isScheduledTrigger(skill.Manifest.Trigger.Type) {
			items = append(items, skill)
		}
	}
	return items, nil
}

func loadScheduledSkillItem(s *AccountRuntime, name string) (core.SkillManifestWithCode, bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return core.SkillManifestWithCode{}, false, nil
	}
	skill, code, err := core.LoadSkillFrom(s.BaseDir, name)
	if err != nil {
		return core.SkillManifestWithCode{}, false, err
	}
	if skill == nil || !isScheduledTrigger(skill.Trigger.Type) {
		return core.SkillManifestWithCode{}, false, nil
	}
	return core.SkillManifestWithCode{Manifest: *skill, Code: code}, true, nil
}

func isScheduledTrigger(trigger string) bool {
	switch strings.ToLower(strings.TrimSpace(trigger)) {
	case "schedule", "once":
		return true
	default:
		return false
	}
}

func scheduleStoreState(s *AccountRuntime, name string) (*time.Time, int) {
	if s == nil || s.Store == nil {
		return nil, 0
	}
	lastRun, _ := s.Store.GetLastRun(name)
	failCount, _ := s.Store.GetFailureCount(name)
	return lastRun, failCount
}

func summarizeDeliveryTarget(target core.DeliveryTarget) string {
	parts := []string{}
	if target.Channel != "" {
		parts = append(parts, "channel="+target.Channel)
	}
	if target.ChatID != "" {
		parts = append(parts, "chat_id="+target.ChatID)
	}
	if target.ChannelUserID != "" {
		parts = append(parts, "channel_user_id="+target.ChannelUserID)
	}
	if target.ReplyToMessage != "" {
		parts = append(parts, "reply_to_message="+target.ReplyToMessage)
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, " ")
}

func handleConversation(ctx context.Context, s *AccountRuntime) string {
	conversationID := commandConversationID(ctx, s)
	var sb strings.Builder
	fmt.Fprintf(&sb, "conversation: %s\n", conversationID)

	accountID := ""
	if s != nil {
		accountID = s.AccountID
	}
	if event := EventFromContext(ctx); event != nil {
		if event.AccountID != "" {
			accountID = event.AccountID
		}
		fmt.Fprintf(&sb, "source: %s\n", event.Type)
		if payload, err := event.ParsePayload(); err == nil {
			if payload.ChatID != "" {
				fmt.Fprintf(&sb, "chat_id: %s\n", payload.ChatID)
			}
			if payload.SourceSessionID != "" {
				fmt.Fprintf(&sb, "source_session_id: %s\n", payload.SourceSessionID)
			}
		}
	}
	if accountID == "" {
		accountID = "(none)"
	}
	fmt.Fprintf(&sb, "account: %s\n", accountID)

	if s == nil || s.Store == nil {
		sb.WriteString("store: unavailable")
		return strings.TrimRight(sb.String(), "\n")
	}

	if conversation, ok, err := s.Store.Conversation(conversationID); err == nil && ok {
		if conversation.Title != "" {
			fmt.Fprintf(&sb, "title: %s\n", conversation.Title)
		}
		if conversation.TitleSource != "" {
			fmt.Fprintf(&sb, "title_source: %s\n", conversation.TitleSource)
		}
		if conversation.ParentConversationID != "" {
			fmt.Fprintf(&sb, "parent_conversation: %s\n", conversation.ParentConversationID)
		}
		if conversation.SourceChannel != "" {
			fmt.Fprintf(&sb, "source_channel: %s\n", conversation.SourceChannel)
		}
		if conversation.ChatID != "" {
			fmt.Fprintf(&sb, "route_chat_id: %s\n", conversation.ChatID)
		}
		if conversation.SourceSessionID != "" {
			fmt.Fprintf(&sb, "route_source_session_id: %s\n", conversation.SourceSessionID)
		}
		if conversation.RolloverReason != "" {
			fmt.Fprintf(&sb, "rollover_reason: %s\n", conversation.RolloverReason)
			fmt.Fprintf(&sb, "rollover_from_turn: %d\n", conversation.RolloverFromTurnID)
		}
	}

	sb.WriteString("staff: ")
	sb.WriteString(handleStaffCurrent(ctx, s))
	sb.WriteByte('\n')

	summary, err := s.Store.ConversationSummaryForConversation(conversationID)
	if err != nil {
		fmt.Fprintf(&sb, "turns: error: %s\n", err)
	} else {
		fmt.Fprintf(&sb, "turns: %d\n", summary.TurnCount)
		if summary.FirstAt != "" {
			fmt.Fprintf(&sb, "first_at: %s\n", summary.FirstAt)
		}
		if summary.LastAt != "" {
			fmt.Fprintf(&sb, "last_at: %s\n", summary.LastAt)
		}
	}

	checkpoints, err := s.Store.ListCheckpointsForConversation(conversationID)
	if err != nil {
		fmt.Fprintf(&sb, "checkpoint: error: %s", err)
		return strings.TrimRight(sb.String(), "\n")
	}
	if len(checkpoints) == 0 {
		sb.WriteString("checkpoint: none")
		return strings.TrimRight(sb.String(), "\n")
	}
	latest := checkpoints[0]
	fmt.Fprintf(&sb, "checkpoint: #%d %s (turn: %d)", latest.ID, latest.Label, latest.TurnID)
	return strings.TrimRight(sb.String(), "\n")
}

func handleConversationCommand(ctx context.Context, args []string, s *AccountRuntime) string {
	if len(args) == 0 {
		return handleConversation(ctx, s)
	}
	switch strings.ToLower(args[0]) {
	case "rename":
		if len(args) == 1 {
			return "사용법: /conversation rename <title>"
		}
		return handleConversationRename(ctx, strings.Join(args[1:], " "), s)
	default:
		return "사용법: /conversation [rename <title>]"
	}
}

func handleConversationRename(ctx context.Context, title string, s *AccountRuntime) string {
	if s == nil || s.Store == nil {
		return "store가 준비되지 않았습니다."
	}
	conversationID := commandConversationID(ctx, s)
	if err := ensureConversationExistsForRename(s, conversationID); err != nil {
		return fmt.Sprintf("대화 제목 변경 실패: %s", err)
	}
	conversation, err := s.Store.SetConversationTitle(conversationID, title)
	if err != nil {
		return fmt.Sprintf("대화 제목 변경 실패: %s", err)
	}
	return fmt.Sprintf("대화 제목을 %q로 변경했습니다.", conversation.Title)
}

func ensureConversationExistsForRename(s *AccountRuntime, conversationID string) error {
	if s == nil || s.Store == nil {
		return nil
	}
	if scope, ok, err := s.Store.ConversationScope(conversationID); err != nil {
		return err
	} else if ok {
		return s.Store.EnsureConversation(scope.ConversationID, scope.ScopeType, scope.ScopeID)
	}
	if _, ok, err := s.Store.Conversation(conversationID); err != nil {
		return err
	} else if ok {
		return nil
	}
	if strings.HasPrefix(conversationID, "general:") {
		return s.Store.EnsureConversation(conversationID, "general", strings.TrimPrefix(conversationID, "general:"))
	}
	return nil
}

func conversationCommandRecordsHistory(args []string) bool {
	return len(args) > 0 && strings.EqualFold(args[0], "rename")
}

func scheduleCommandRecordsHistory(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "pause", "resume", "delete", "remove":
		return true
	default:
		return false
	}
}

func handleContext(ctx context.Context, s *AccountRuntime) string {
	conversationID := commandConversationID(ctx, s)
	compaction := DefaultCompaction()
	if s != nil && s.Config != nil {
		compaction = s.compactionForAttempt(0)
	}
	contextWindow, maxTokens := currentModelLimits(s)

	promptTokens := 0
	historyTokens := 0
	turnCount := 0
	if s != nil && s.Store != nil {
		state, err := s.Store.LoadConversationStateForChat(conversationID)
		if err != nil {
			return fmt.Sprintf("context 조회 실패: %s", err)
		}
		if state != nil {
			promptTokens = EstimateTokens(state.SystemPrompt)
			turnCount = len(state.Turns)
			for _, turn := range state.Turns {
				historyTokens += EstimateTokens(turn.Content)
				historyTokens += EstimateTokens(turn.Code)
				historyTokens += EstimateTokens(turn.Result)
			}
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "conversation: %s\n", conversationID)
	fmt.Fprintf(&sb, "prompt_tokens: %d\n", promptTokens)
	fmt.Fprintf(&sb, "history_tokens: %d\n", historyTokens)
	fmt.Fprintf(&sb, "total_tokens: %d\n", promptTokens+historyTokens)
	fmt.Fprintf(&sb, "turns: %d\n", turnCount)
	fmt.Fprintf(&sb, "rollover_turns: %d\n", turnCount)
	fmt.Fprintf(&sb, "rollover_min_turns: %d\n", defaultRolloverPolicy.MinTurnsBeforeRollover)
	fmt.Fprintf(&sb, "rollover_max_turns: %d\n", defaultRolloverPolicy.MaxTurns)
	fmt.Fprintf(&sb, "recent_window: %d\n", compaction.RecentWindow)
	fmt.Fprintf(&sb, "middle_window: %d\n", compaction.MiddleWindow)
	fmt.Fprintf(&sb, "truncate_len: %d\n", compaction.TruncateLen)
	fmt.Fprintf(&sb, "context_window: %d\n", contextWindow)
	fmt.Fprintf(&sb, "max_tokens: %d", maxTokens)
	return sb.String()
}

func handleCompactCommand(ctx context.Context, args []string, s *AccountRuntime) (string, bool) {
	if s == nil || s.Store == nil {
		return "compact 실행을 위한 store가 준비되지 않았습니다.", false
	}
	if len(args) > 1 {
		return compactUsage(), false
	}
	keepRecent := 40
	if len(args) == 1 {
		n, err := strconv.Atoi(strings.TrimSpace(args[0]))
		if err != nil || n <= 0 {
			return compactUsage(), false
		}
		keepRecent = n
	}
	conversationID := commandConversationID(ctx, s)
	compacted, err := compactConversationWithSemanticSummary(ctx, s, conversationID, keepRecent)
	if err != nil {
		return fmt.Sprintf("compact 실행 실패: %s", err), false
	}
	return fmt.Sprintf("conversation: %s\nturns_compacted: %d\nkeep_recent: %d", conversationID, compacted, keepRecent), true
}

func compactUsage() string {
	return "사용법: /compact [keep_recent]"
}

func currentModelLimits(s *AccountRuntime) (int, int) {
	contextWindow := 0
	maxTokens := 0
	if s != nil && s.Config != nil {
		if id := currentModelID(s); id != "" {
			if model := s.Config.FindModel(id); model != nil {
				contextWindow = int(model.ContextWindow)
				maxTokens = int(model.MaxTokens)
			}
		}
	}
	if s != nil && s.Provider != nil {
		if contextWindow == 0 {
			contextWindow = s.Provider.ContextWindow()
		}
		if maxTokens == 0 {
			maxTokens = s.Provider.MaxTokens()
		}
	}
	return contextWindow, maxTokens
}

func handleRun(ctx context.Context, name string, s *AccountRuntime) string {
	if s == nil || s.Sandbox == nil {
		return "스킬 실행을 위한 세션이 준비되지 않았습니다."
	}
	resultJSON, err := runSkillOrPackage(ctx, name, s)
	if err != nil {
		return fmt.Sprintf("스킬 실행 실패: %s", err)
	}
	var result struct {
		Output string `json:"output"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		return resultJSON
	}
	if result.Error != "" {
		if result.Output != "" {
			return result.Output
		}
		return fmt.Sprintf("스킬 실행 실패: %s", result.Error)
	}
	return result.Output
}

func handleTeach(ctx context.Context, description string, s *AccountRuntime) string {
	result, err := HandleTeach(ctx, description, "chat", s)
	if err != nil {
		return fmt.Sprintf("스킬 학습 실패: %s", err)
	}
	if !result.SyntaxOK {
		return fmt.Sprintf("생성된 코드에 구문 오류가 있습니다: %s\n\n코드:\n%s", result.SyntaxError, result.Code)
	}

	// Block auto-approve for skills using dangerous permissions
	for _, perm := range result.Permissions {
		if perm == "Shell" || perm == "File" || perm == "Git" {
			return fmt.Sprintf("생성된 스킬이 위험한 권한(%s)을 사용합니다. API /skills/teach/approve를 통해 수동 승인이 필요합니다.\n\n코드:\n%s", perm, result.Code)
		}
	}

	// Auto-approve for chat entry point (no interactive mechanism for safe skills)
	if err := ApproveSkill(s.BaseDir, result); err != nil {
		return fmt.Sprintf("스킬 저장 실패: %s", err)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "스킬 '%s' 생성 완료!\n", result.SkillName)
	fmt.Fprintf(&sb, "설명: %s\n", result.Description)
	fmt.Fprintf(&sb, "트리거: %s\n", result.Trigger.Type)
	if len(result.Permissions) > 0 {
		fmt.Fprintf(&sb, "권한: %s\n", strings.Join(result.Permissions, ", "))
	}
	fmt.Fprintf(&sb, "\n코드:\n%s", result.Code)
	return sb.String()
}

func handleProjectsCommand(s *AccountRuntime) string {
	if s == nil || s.Store == nil {
		return "project 정보를 위한 세션이 준비되지 않았습니다."
	}
	projects, err := s.Store.ListProjects(false)
	if err != nil {
		return fmt.Sprintf("project 목록 조회 실패: %s", err)
	}
	if len(projects) == 0 {
		return "등록된 project가 없습니다."
	}
	var sb strings.Builder
	sb.WriteString("Projects:\n")
	for _, project := range projects {
		fmt.Fprintf(&sb, "- %s %s — %s\n", project.Key, project.Name, project.RootPath)
	}
	return strings.TrimRight(sb.String(), "\n")
}

const currentProjectContextPrefix = "current_project:"

func handleProjectCommand(ctx context.Context, args []string, s *AccountRuntime) string {
	if s == nil || s.Store == nil {
		return "project 정보를 위한 세션이 준비되지 않았습니다."
	}
	if len(args) == 0 {
		return "사용법: /project current | show <key> | use <key> | new | settings"
	}
	switch strings.ToLower(args[0]) {
	case "current":
		project, ok, err := currentProject(ctx, s)
		if err != nil {
			return fmt.Sprintf("current project 조회 실패: %s", err)
		}
		if !ok {
			return "current project가 없습니다."
		}
		return formatProjectSummary(project)
	case "show":
		if len(args) < 2 {
			return "사용법: /project show <key>"
		}
		project, err := s.Store.GetProject(args[1])
		if err != nil {
			return fmt.Sprintf("project %q를 찾지 못했습니다.", args[1])
		}
		return formatProjectSummary(project)
	case "use":
		if len(args) < 2 {
			return "사용법: /project use <key>"
		}
		project, err := s.Store.GetProject(args[1])
		if err != nil {
			return fmt.Sprintf("project %q를 찾지 못했습니다.", args[1])
		}
		if err := saveCurrentProject(ctx, s, project); err != nil {
			return fmt.Sprintf("current project 저장 실패: %s", err)
		}
		return fmt.Sprintf("%s project를 현재 conversation의 project로 선택했습니다.", project.Key)
	case "new":
		return "Projects 화면에서 새 project folder를 선택하세요."
	case "settings":
		return "Projects 화면의 Settings 탭에서 project 설정을 변경하세요."
	default:
		return "사용법: /project current | show <key> | use <key> | new | settings"
	}
}

func handleTicketsCommand(ctx context.Context, args []string, s *AccountRuntime) string {
	if s == nil || s.Store == nil {
		return "ticket 정보를 위한 세션이 준비되지 않았습니다."
	}
	project, ok, err := currentProject(ctx, s)
	if err != nil {
		return fmt.Sprintf("ticket 목록 조회 실패: %s", err)
	}
	if !ok {
		return "ticket을 볼 project가 없습니다."
	}
	if len(args) > 0 {
		if p, err := s.Store.GetProject(args[0]); err == nil {
			project = p
		}
	}
	tickets, err := s.Store.ListTickets(store.TicketListFilter{ProjectID: project.ID})
	if err != nil {
		return fmt.Sprintf("ticket 목록 조회 실패: %s", err)
	}
	if len(tickets) == 0 {
		return fmt.Sprintf("%s project에 ticket이 없습니다.", project.Key)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s tickets:\n", project.Key)
	for _, ticket := range tickets {
		fmt.Fprintf(&sb, "- %s [%s] %s\n", ticket.Key, ticket.Status, ticket.Title)
	}
	return strings.TrimRight(sb.String(), "\n")
}

func handleTicketCommand(args []string, s *AccountRuntime) string {
	if s == nil || s.Store == nil {
		return "ticket 정보를 위한 세션이 준비되지 않았습니다."
	}
	if len(args) == 0 {
		return "사용법: /ticket show <key> | chat <key> | job <key> | move <key> <status> | block <key> <reason> | done <key>"
	}
	switch strings.ToLower(args[0]) {
	case "show":
		if len(args) < 2 {
			return "사용법: /ticket show <key>"
		}
		ticket, err := s.Store.GetTicket(args[1])
		if err != nil {
			return fmt.Sprintf("ticket %q를 찾지 못했습니다.", args[1])
		}
		return formatTicketSummary(ticket)
	case "chat":
		if len(args) < 2 {
			return "사용법: /ticket chat <key>"
		}
		ticket, err := s.Store.GetTicket(args[1])
		if err != nil {
			return fmt.Sprintf("ticket %q를 찾지 못했습니다.", args[1])
		}
		return fmt.Sprintf("안내: %s ticket chat은 이 slash command가 현재 대화를 자동 전환하지 않습니다.\nconversation_id: %s\nWeb/CLI에서 해당 ticket chat을 여세요.", ticket.Key, ticket.TicketConversationID)
	case "job":
		if len(args) < 2 {
			return "사용법: /ticket job <key>"
		}
		ticket, err := s.Store.GetTicket(args[1])
		if err != nil {
			return fmt.Sprintf("ticket %q를 찾지 못했습니다.", args[1])
		}
		if err := s.Store.EnsureDefaultDrivers(); err != nil {
			return fmt.Sprintf("job driver 준비 실패: %s", err)
		}
		job, err := s.Store.PlanJob(store.PlanJobRequest{
			ProjectID:     ticket.ProjectID,
			TicketID:      ticket.ID,
			DriverID:      "codex",
			Mode:          store.JobModeOneShot,
			PromptSummary: ticket.Title,
			PromptText:    ticket.Body,
			CreatedBy:     "chat",
		})
		if err != nil {
			return fmt.Sprintf("job plan 생성 실패: %s", err)
		}
		return fmt.Sprintf("%s job plan을 만들었습니다. status: %s", job.ID, job.Status)
	case "move":
		if len(args) < 3 {
			return "사용법: /ticket move <key> <status>"
		}
		ticket, err := s.Store.MoveTicket(args[1], store.MoveTicketRequest{Status: args[2], ActorID: "chat"})
		if err != nil {
			return fmt.Sprintf("ticket 이동 실패: %s", err)
		}
		return formatTicketSummary(ticket)
	case "block":
		if len(args) < 3 {
			return "사용법: /ticket block <key> <reason>"
		}
		reason := strings.Join(args[2:], " ")
		ticket, err := s.Store.MoveTicket(args[1], store.MoveTicketRequest{Status: store.TicketStatusBlocked, ActorID: "chat", Message: reason})
		if err != nil {
			return fmt.Sprintf("ticket block 실패: %s", err)
		}
		return formatTicketSummary(ticket)
	case "done":
		if len(args) < 2 {
			return "사용법: /ticket done <key>"
		}
		ticket, err := s.Store.MoveTicket(args[1], store.MoveTicketRequest{Status: store.TicketStatusDone, ActorID: "chat"})
		if err != nil {
			return fmt.Sprintf("ticket done 실패: %s", err)
		}
		return formatTicketSummary(ticket)
	default:
		return "사용법: /ticket show <key> | chat <key> | job <key> | move <key> <status> | block <key> <reason> | done <key>"
	}
}

func firstProject(s *AccountRuntime) (*store.Project, bool, error) {
	projects, err := s.Store.ListProjects(false)
	if err != nil {
		return nil, false, err
	}
	if len(projects) == 0 {
		return nil, false, nil
	}
	return &projects[0], true, nil
}

func currentProject(ctx context.Context, s *AccountRuntime) (*store.Project, bool, error) {
	if s == nil || s.Store == nil {
		return nil, false, nil
	}
	key := currentProjectContextKey(ctx, s)
	projectID, ok, err := s.Store.GetUserContext(key)
	if err != nil {
		return nil, false, err
	}
	if ok && strings.TrimSpace(projectID) != "" {
		project, err := s.Store.GetProject(projectID)
		if err == nil {
			return project, true, nil
		}
		if _, deleteErr := s.Store.DeleteUserContext(key); deleteErr != nil {
			return nil, false, deleteErr
		}
	}
	return firstProject(s)
}

func saveCurrentProject(ctx context.Context, s *AccountRuntime, project *store.Project) error {
	if s == nil || s.Store == nil || project == nil {
		return nil
	}
	return s.Store.SetUserContext(currentProjectContextKey(ctx, s), project.ID, "slash_command")
}

func currentProjectContextKey(ctx context.Context, s *AccountRuntime) string {
	return currentProjectContextPrefix + commandConversationID(ctx, s)
}

func commandConversationID(ctx context.Context, s *AccountRuntime) string {
	if id := strings.TrimSpace(ConversationIDFromContext(ctx)); id != "" {
		return id
	}
	if event := EventFromContext(ctx); event != nil {
		if id := strings.TrimSpace(conversationKeyForEvent(s, event)); id != "" {
			return id
		}
	}
	return store.DefaultConversationID
}

func formatProjectSummary(project *store.Project) string {
	return fmt.Sprintf("%s %s\npath: %s\nstate: %s", project.Key, project.Name, project.RootPath, project.State)
}

func formatTicketSummary(ticket *store.Ticket) string {
	return fmt.Sprintf("%s [%s] %s", ticket.Key, ticket.Status, ticket.Title)
}

// handleModel implements `/model` (info) and `/model <id>` (turn-level swap).
//
// arg matrix:
//   - 0 args      → info: current active + registered models list
//   - 1 arg, blank → usage
//   - 1 arg, == current active id → "Already on <id>" (no-op, no Set)
//   - 1 arg, registered id        → Set + "Switched to <id> (this turn only — restart resets to default)"
//   - 1 arg, unknown id           → "Unknown model: <id>. Available: ..." (no Set)
//   - >=2 args    → usage
//
// Switch state lives in AccountRuntime.activeModelOverride (atomic.Pointer) and
// resets on daemon restart — no config.toml mutation. ID match is
// case-sensitive (config IDs are user-authored exact strings; coercion
// would mask typos rather than help). Special characters are not
// validated — IDs come from config which is the trust boundary.
func handleModel(args []string, s *AccountRuntime) string {
	if s == nil || s.Config == nil {
		return "model 정보를 위한 세션이 준비되지 않았습니다."
	}
	models := s.Config.LLM.Models
	current := currentModelID(s)

	if len(args) == 0 {
		return formatModelInfo(current, models, s)
	}
	if len(args) >= 2 {
		return modelUsage()
	}
	id := strings.TrimSpace(args[0])
	if id == "" {
		return modelUsage()
	}
	if id == current {
		return fmt.Sprintf("이미 %q를 사용 중입니다.", id)
	}
	if !modelIDExists(id, models) {
		available := modelIDList(models)
		if available == "" {
			return fmt.Sprintf("등록된 모델이 없습니다 — %q로 변경할 수 없습니다.", id)
		}
		return fmt.Sprintf("알 수 없는 모델: %q\n사용 가능: %s", id, available)
	}
	s.SetActiveModel(id)
	return fmt.Sprintf("%q로 변경했습니다 (이번 turn부터 적용 — 데몬 재시작 시 기본값으로 복귀).", id)
}

// currentModelID returns the active model ID — chat-set override > config
// default > first registered. Empty string when no models are registered.
func currentModelID(s *AccountRuntime) string {
	if id := s.GetActiveModel(); id != "" {
		return id
	}
	if m := s.Config.DefaultModel(); m != nil {
		return m.ID
	}
	return ""
}

func modelIDExists(id string, models []core.ModelConfig) bool {
	for i := range models {
		if models[i].ID == id {
			return true
		}
	}
	return false
}

func modelIDList(models []core.ModelConfig) string {
	if len(models) == 0 {
		return ""
	}
	ids := make([]string, len(models))
	for i := range models {
		ids[i] = models[i].ID
	}
	return strings.Join(ids, ", ")
}

func modelUsage() string {
	return "사용법: /model (정보 표시) 또는 /model <id> (모델 변경)"
}

// formatModelInfo prints the active model + the list of registered alternatives.
// Fields shown match what KittyPaw actually stores in core.ModelConfig:
// provider, model, base_url, context_window, max_tokens. Temperature and
// thinking flag are deliberately omitted — they are not config fields, and
// inferring them from model-name heuristics would lie about state.
func formatModelInfo(current string, models []core.ModelConfig, s *AccountRuntime) string {
	var sb strings.Builder
	if current == "" {
		sb.WriteString("현재 모델: (없음 — 등록된 모델이 없습니다)\n")
	} else {
		var active *core.ModelConfig
		for i := range models {
			if models[i].ID == current {
				active = &models[i]
				break
			}
		}
		if active == nil {
			fmt.Fprintf(&sb, "현재 모델: %s (config 등록 정보를 찾지 못했습니다)\n", current)
		} else {
			fmt.Fprintf(&sb, "현재 모델: %s\n", active.ID)
			fmt.Fprintf(&sb, "  provider: %s\n", active.Provider)
			fmt.Fprintf(&sb, "  model: %s\n", active.Model)
			if active.BaseURL != "" {
				fmt.Fprintf(&sb, "  base_url: %s\n", active.BaseURL)
			} else {
				sb.WriteString("  base_url: (provider 기본값)\n")
			}
			if active.ContextWindow > 0 {
				fmt.Fprintf(&sb, "  context_window: %d\n", active.ContextWindow)
			}
			if active.MaxTokens > 0 {
				fmt.Fprintf(&sb, "  max_tokens: %d\n", active.MaxTokens)
			}
			if s.GetActiveModel() != "" {
				sb.WriteString("  (이번 채팅 세션 한정 — 데몬 재시작 시 기본값으로 복귀)\n")
			}
		}
	}

	if len(models) == 0 {
		return sb.String() + "\n등록된 모델: 없음"
	}
	sb.WriteString("\n등록된 모델:\n")
	for i := range models {
		marker := "  "
		if models[i].ID == current {
			marker = "* "
		}
		fmt.Fprintf(&sb, "%s%s — %s/%s\n", marker, models[i].ID, models[i].Provider, models[i].Model)
	}
	sb.WriteString("\n변경: /model <id>")
	return sb.String()
}

func handleStaffCommand(ctx context.Context, args []string, s *AccountRuntime) string {
	if len(args) == 0 {
		return handleStaffOverview(ctx, s)
	}
	subcmd := strings.ToLower(strings.TrimSpace(args[0]))
	switch subcmd {
	case "current":
		return handleStaffCurrent(ctx, s)
	case "list":
		return handleStaffList(s)
	case "show":
		if len(args) < 2 {
			return "사용법: /staff show <staff-id>"
		}
		return handleStaffShow(strings.Join(args[1:], " "), s)
	case "use":
		if len(args) < 2 {
			return "사용법: /staff use <staff-id>"
		}
		return handleStaffUse(ctx, strings.Join(args[1:], " "), s)
	case "routes":
		return handleStaffRoutes(s)
	case "route":
		if len(args) < 3 {
			return "사용법: /staff route <conversation-id> <staff-id|clear>"
		}
		return handleStaffRoute(args[1], strings.Join(args[2:], " "), s)
	case "source-routes":
		return handleStaffSourceRoutes(s)
	case "source-route":
		return handleStaffSourceRoute(args[1:], s)
	case "hire":
		if len(args) < 2 {
			return "사용법: /staff hire <역할>"
		}
		return handleStaffHire(strings.Join(args[1:], " "), s)
	case "cancel":
		return handleStaffCancel(s)
	default:
		// Backward-compatible form: /staff <id> means /staff use <id>.
		return handleStaffUse(ctx, strings.Join(args, " "), s)
	}
}

func handleStaffOverview(ctx context.Context, s *AccountRuntime) string {
	var sb strings.Builder
	sb.WriteString(handleStaffCurrent(ctx, s))
	sb.WriteString("\n\n")
	sb.WriteString(handleStaffList(s))
	sb.WriteString("\n\n명령어: /staff current | list | show <id> | use <id> | routes | route <conversation-id> <id> | source-routes | source-route add ... | hire <역할> | cancel")
	return sb.String()
}

func handleStaffCurrent(ctx context.Context, s *AccountRuntime) string {
	if s == nil || s.Config == nil {
		return "현재 staff 정보를 위한 세션이 준비되지 않았습니다."
	}
	conversationID := commandConversationID(ctx, s)
	decision := ResolveStaffDecisionForEvent(s.Config, EventFromContext(ctx), conversationID, "", s.Store, s.BaseDir)
	if s.Store != nil {
		decision.ConversationID = conversationID
	}
	return formatStaffRouteDecision(decision)
}

func formatStaffRouteDecision(decision StaffRouteDecision) string {
	if strings.TrimSpace(decision.StaffID) == "" {
		decision.StaffID = "default"
	}
	if strings.TrimSpace(decision.Reason) == "" {
		decision.Reason = StaffRouteReasonDefault
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "current staff: %s\nreason: %s", decision.StaffID, decision.Reason)
	if decision.ConversationID != "" {
		fmt.Fprintf(&sb, "\nconversation: %s", decision.ConversationID)
	}
	if decision.Channel != "" {
		fmt.Fprintf(&sb, "\nchannel: %s", decision.Channel)
	}
	if decision.Reason == StaffRouteReasonSourceRoute {
		if decision.SourceRouteID > 0 {
			fmt.Fprintf(&sb, "\nsource_route_id: %d", decision.SourceRouteID)
		}
		fmt.Fprintf(&sb, "\nmatch: %s %s %s %q",
			decision.SourceChannel, decision.MatchField, decision.PatternKind, decision.Pattern)
	}
	return sb.String()
}

func handleStaffList(s *AccountRuntime) string {
	if s == nil {
		return "staff 목록을 위한 세션이 준비되지 않았습니다."
	}
	base, err := core.ResolveBaseDir(s.BaseDir)
	if err != nil {
		return fmt.Sprintf("staff 목록 조회 실패: %s", err)
	}
	staff, err := core.ListStaffRecords(base)
	if err != nil {
		return fmt.Sprintf("staff 목록 조회 실패: %s", err)
	}
	if len(staff) == 0 {
		return "staff 목록: 없음"
	}
	var sb strings.Builder
	sb.WriteString("staff 목록:\n")
	for _, record := range staff {
		label := record.ID
		if record.DisplayName != "" {
			label += " — " + record.DisplayName
		}
		if record.Description != "" {
			label += " (" + record.Description + ")"
		}
		sb.WriteString("- ")
		sb.WriteString(label)
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

func handleStaffShow(idOrAlias string, s *AccountRuntime) string {
	record, ok, err := resolveActiveStaffRecord(idOrAlias, s)
	if err != nil {
		return fmt.Sprintf("staff 조회 실패: %s", err)
	}
	if !ok {
		return fmt.Sprintf("staff %q를 찾지 못했습니다.", strings.TrimSpace(idOrAlias))
	}
	displayName := record.DisplayName
	if displayName == "" {
		displayName = record.ID
	}
	return fmt.Sprintf("staff: %s\n표시 이름: %s\n설명: %s\naliases: %s\nSOUL.md: %s",
		record.ID, displayName, record.Description, strings.Join(record.Aliases, ", "), yesNo(record.HasSoul))
}

func handleStaffUse(ctx context.Context, idOrAlias string, s *AccountRuntime) string {
	if s == nil || s.Store == nil {
		return "staff 변경을 위한 저장소가 준비되지 않았습니다."
	}
	conversationID := commandConversationID(ctx, s)
	id, err := setConversationStaff(s.BaseDir, s.Store, conversationID, idOrAlias)
	if err != nil {
		return fmt.Sprintf("staff 변경 실패: %s", err)
	}
	return fmt.Sprintf("이 대화(%s)의 기본 staff를 %q로 변경했습니다.", conversationID, id)
}

func handleStaffRoutes(s *AccountRuntime) string {
	if s == nil || s.Store == nil {
		return "staff route 조회를 위한 저장소가 준비되지 않았습니다."
	}
	conversations, err := s.Store.ListConversations(50)
	if err != nil {
		return fmt.Sprintf("staff route 조회 실패: %s", err)
	}
	if len(conversations) == 0 {
		return "staff routes: 없음"
	}
	var sb strings.Builder
	sb.WriteString("staff routes:\n")
	for _, conv := range conversations {
		if conv.DefaultStaffID == "" && conv.SourceChannel == "" && conv.ChatID == "" && conv.SourceSessionID == "" {
			continue
		}
		staffID := conv.DefaultStaffID
		if staffID == "" {
			staffID = "(default)"
		}
		fmt.Fprintf(&sb, "- %s -> %s", conv.ID, staffID)
		var details []string
		if conv.SourceChannel != "" {
			details = append(details, "source="+conv.SourceChannel)
		}
		if conv.ChatID != "" {
			details = append(details, "chat="+conv.ChatID)
		}
		if conv.SourceSessionID != "" {
			details = append(details, "source_session="+conv.SourceSessionID)
		}
		if len(details) > 0 {
			sb.WriteString(" (")
			sb.WriteString(strings.Join(details, ", "))
			sb.WriteString(")")
		}
		sb.WriteByte('\n')
	}
	out := strings.TrimRight(sb.String(), "\n")
	if out == "staff routes:" {
		return "staff routes: 없음"
	}
	return out
}

func handleStaffSourceRoutes(s *AccountRuntime) string {
	if s == nil || s.Store == nil {
		return "staff source route 조회를 위한 저장소가 준비되지 않았습니다."
	}
	routes, err := s.Store.ListStaffSourceRoutes()
	if err != nil {
		return fmt.Sprintf("staff source route 조회 실패: %s", err)
	}
	if len(routes) == 0 {
		return "staff source routes: 없음"
	}
	var sb strings.Builder
	sb.WriteString("staff source routes:\n")
	for _, route := range routes {
		state := ""
		if !route.Enabled {
			state = " disabled"
		}
		fmt.Fprintf(&sb, "- #%d %s %s %s %q -> %s%s\n",
			route.ID, route.SourceChannel, route.MatchField, route.PatternKind,
			route.Pattern, route.StaffID, state)
	}
	return strings.TrimRight(sb.String(), "\n")
}

func handleStaffSourceRoute(args []string, s *AccountRuntime) string {
	if len(args) == 0 {
		return staffSourceRouteUsage()
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "list", "ls":
		return handleStaffSourceRoutes(s)
	case "add", "set":
		return handleStaffSourceRouteAdd(args[1:], s)
	case "delete", "del", "remove", "rm":
		if len(args) < 2 {
			return "사용법: /staff source-route delete <id>"
		}
		return handleStaffSourceRouteDelete(args[1], s)
	default:
		return staffSourceRouteUsage()
	}
}

func handleStaffSourceRouteAdd(args []string, s *AccountRuntime) string {
	if s == nil || s.Store == nil {
		return "staff source route 변경을 위한 저장소가 준비되지 않았습니다."
	}
	if len(args) < 5 {
		return staffSourceRouteUsage()
	}
	sourceChannel := normalizeStaffSourceChannel(args[0])
	matchField, ok := normalizeStaffSourceMatchField(args[1])
	if !ok {
		return "match field는 chat_id 또는 source_session_id여야 합니다."
	}
	patternKind, ok := normalizeStaffSourcePatternKind(args[2])
	if !ok {
		return "pattern kind는 exact, prefix, glob 중 하나여야 합니다."
	}
	pattern := strings.TrimSpace(args[3])
	if pattern == "" {
		return "pattern이 비어 있습니다."
	}
	staffRef := strings.Join(args[4:], " ")
	base, err := core.ResolveBaseDir(s.BaseDir)
	if err != nil {
		return fmt.Sprintf("staff source route 변경 실패: %s", err)
	}
	staffID, ok, err := core.ResolveStaffReference(base, staffRef)
	if err != nil {
		return fmt.Sprintf("staff source route 변경 실패: %s", err)
	}
	if !ok {
		return fmt.Sprintf("staff %q를 찾지 못했습니다.", strings.TrimSpace(staffRef))
	}
	route, err := s.Store.UpsertStaffSourceRoute(store.UpsertStaffSourceRouteRequest{
		SourceChannel: sourceChannel,
		MatchField:    matchField,
		PatternKind:   patternKind,
		Pattern:       pattern,
		StaffID:       staffID,
	})
	if err != nil {
		return fmt.Sprintf("staff source route 변경 실패: %s", err)
	}
	return fmt.Sprintf("staff source route #%d: %s %s %s %q -> %s",
		route.ID, route.SourceChannel, route.MatchField, route.PatternKind, route.Pattern, route.StaffID)
}

func handleStaffSourceRouteDelete(idText string, s *AccountRuntime) string {
	if s == nil || s.Store == nil {
		return "staff source route 삭제를 위한 저장소가 준비되지 않았습니다."
	}
	id, err := strconv.ParseInt(strings.TrimSpace(idText), 10, 64)
	if err != nil || id <= 0 {
		return "staff source route id가 올바르지 않습니다."
	}
	if err := s.Store.DeleteStaffSourceRoute(id); err != nil {
		return fmt.Sprintf("staff source route 삭제 실패: %s", err)
	}
	return fmt.Sprintf("staff source route #%d를 삭제했습니다.", id)
}

func staffSourceRouteUsage() string {
	return "사용법: /staff source-route add <source> <chat_id|source_session_id> <exact|prefix|glob> <pattern> <staff-id> 또는 /staff source-route delete <id>"
}

func normalizeStaffSourceChannel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "web_chat", "websocket":
		return "web"
	case "kakao", "kakaotalk":
		return "kakao_talk"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func normalizeStaffSourceMatchField(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "chat", "chat_id":
		return store.StaffSourceMatchChatID, true
	case "session", "source_session", "source_session_id", "user", "user_id":
		return store.StaffSourceMatchSourceSessionID, true
	default:
		return "", false
	}
}

func normalizeStaffSourcePatternKind(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "exact", "=":
		return store.StaffSourcePatternExact, true
	case "prefix", "starts_with":
		return store.StaffSourcePatternPrefix, true
	case "glob", "wildcard":
		return store.StaffSourcePatternGlob, true
	default:
		return "", false
	}
}

func handleStaffRoute(conversationID, idOrAlias string, s *AccountRuntime) string {
	if s == nil || s.Store == nil {
		return "staff route 변경을 위한 저장소가 준비되지 않았습니다."
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return "사용법: /staff route <conversation-id> <staff-id|clear>"
	}
	switch strings.ToLower(strings.TrimSpace(idOrAlias)) {
	case "", "clear", "none", "default":
		if _, err := s.Store.SetConversationDefaultStaff(conversationID, ""); err != nil {
			return fmt.Sprintf("staff route 변경 실패: %s", err)
		}
		return fmt.Sprintf("대화(%s)의 staff route를 기본값으로 되돌렸습니다.", conversationID)
	}
	id, err := setConversationStaff(s.BaseDir, s.Store, conversationID, idOrAlias)
	if err != nil {
		return fmt.Sprintf("staff route 변경 실패: %s", err)
	}
	return fmt.Sprintf("대화(%s)의 기본 staff를 %q로 변경했습니다.", conversationID, id)
}

func handleStaffHire(role string, s *AccountRuntime) string {
	if s == nil {
		return "staff 생성을 위한 세션이 준비되지 않았습니다."
	}
	if existing, ok, err := loadPendingStaffDraft(s.BaseDir, conversationKey(s)); err != nil {
		return fmt.Sprintf("staff 초안 조회 실패: %s", err)
	} else if ok {
		return formatPendingStaffDraftNotice(existing)
	}
	draft := buildStaffDraft(role, "chat_command")
	base, err := core.ResolveBaseDir(s.BaseDir)
	if err != nil {
		return fmt.Sprintf("staff 조회 실패: %s", err)
	}
	if core.StaffHasSoul(base, draft.ID) {
		return fmt.Sprintf("staff %q는 이미 존재합니다.", draft.ID)
	}
	if err := savePendingStaffDraft(s.BaseDir, conversationKey(s), draft); err != nil {
		return fmt.Sprintf("staff 초안 저장 실패: %s", err)
	}
	return formatStaffDraftPreview(draft)
}

func handleStaffCancel(s *AccountRuntime) string {
	if s == nil || s.Store == nil {
		return "staff 초안을 위한 저장소가 준비되지 않았습니다."
	}
	if err := clearPendingStaffDraft(s.BaseDir, conversationKey(s)); err != nil {
		return fmt.Sprintf("staff 초안 취소 실패: %s", err)
	}
	if err := clearPendingStaffOffer(s.Store, conversationKey(s)); err != nil {
		return fmt.Sprintf("staff 생성 제안 취소 실패: %s", err)
	}
	if err := clearPendingStaffSwitch(s.Store, conversationKey(s)); err != nil {
		return fmt.Sprintf("staff 전환 제안 취소 실패: %s", err)
	}
	return "staff 초안을 취소했습니다."
}

func resolveActiveStaffRecord(idOrAlias string, s *AccountRuntime) (core.StaffRecord, bool, error) {
	if s == nil {
		return core.StaffRecord{}, false, nil
	}
	base, err := core.ResolveBaseDir(s.BaseDir)
	if err != nil {
		return core.StaffRecord{}, false, err
	}
	id, ok, err := core.ResolveStaffReference(base, idOrAlias)
	if err != nil || !ok {
		return core.StaffRecord{}, false, err
	}
	meta, err := core.ReadStaffMetaFile(base, id)
	if err != nil {
		return core.StaffRecord{}, false, err
	}
	return core.StaffRecord{StaffMetaFile: meta, HasSoul: core.StaffHasSoul(base, id), HasDraft: core.StaffHasDraft(base, id)}, true, nil
}

func formatStaffDraftPreview(draft StaffDraft) string {
	return fmt.Sprintf(`%s staff 초안입니다.

시스템 이름: %s
표시 이름: %s
역할: %s

SOUL.md:
%s
이대로 생성할까요?`, draft.DisplayName, draft.ID, draft.DisplayName, draft.Description, draft.Soul)
}

func formatPendingStaffDraftNotice(draft StaffDraft) string {
	return fmt.Sprintf("이미 생성 대기 중인 staff 초안이 있습니다: %s (%s)\n먼저 생성하거나 취소해 주세요.", draft.DisplayName, draft.ID)
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
