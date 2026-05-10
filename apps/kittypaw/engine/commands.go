package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/store"
)

type slashCommand struct {
	Name    string
	Usage   string
	Summary string
	Aliases []string
	Risk    string
	History bool
	Details []string
	Handler slashCommandHandler
}

type slashCommandResult struct {
	Text          string
	RecordHistory bool
}

type slashCommandHandler func(context.Context, []string, *Session) slashCommandResult

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
		Handler: func(_ context.Context, _ []string, s *Session) slashCommandResult {
			return slashCommandText(handleStatus(s))
		},
	},
	{
		Name:    "/skills",
		Usage:   "/skills",
		Summary: "스킬 목록 표시",
		Risk:    "read",
		Handler: func(_ context.Context, _ []string, s *Session) slashCommandResult {
			return slashCommandText(handleSkills(s))
		},
	},
	{
		Name:    "/run",
		Usage:   "/run <name>",
		Summary: "스킬 또는 패키지 실행",
		Risk:    "execute",
		History: true,
		Handler: func(ctx context.Context, args []string, s *Session) slashCommandResult {
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
		Handler: func(ctx context.Context, args []string, s *Session) slashCommandResult {
			if len(args) == 0 {
				return slashCommandText("사용법: /teach <설명>")
			}
			return slashCommandResult{Text: handleTeach(ctx, strings.Join(args, " "), s), RecordHistory: true}
		},
	},
	{
		Name:    "/staff",
		Usage:   "/staff <current|list|show|use|hire|cancel>",
		Summary: "staff 상태 조회 및 전환",
		Risk:    "mixed",
		History: true,
		Details: []string{
			"/staff current",
			"/staff list",
			"/staff show <staff-id>",
			"/staff use <staff-id>",
			"/staff hire <역할>",
			"/staff cancel",
		},
		Handler: func(_ context.Context, args []string, s *Session) slashCommandResult {
			return slashCommandResult{Text: handleStaffCommand(args, s), RecordHistory: staffCommandRecordsHistory(args)}
		},
	},
	{
		Name:    "/model",
		Usage:   "/model [id]",
		Summary: "현재 LLM 정보 표시 또는 이번 채팅 모델 변경",
		Risk:    "session",
		History: true,
		Handler: func(_ context.Context, args []string, s *Session) slashCommandResult {
			record := modelCommandRecordsHistory(args, s)
			return slashCommandResult{Text: handleModel(args, s), RecordHistory: record}
		},
	},
	{
		Name:    "/session",
		Usage:   "/session",
		Summary: "현재 conversation/session 진단 정보",
		Risk:    "read",
		Handler: func(ctx context.Context, _ []string, s *Session) slashCommandResult {
			return slashCommandText(handleSession(ctx, s))
		},
	},
	{
		Name:    "/context",
		Usage:   "/context",
		Summary: "현재 prompt/context 크기 진단",
		Risk:    "read",
		Handler: func(ctx context.Context, _ []string, s *Session) slashCommandResult {
			return slashCommandText(handleContext(ctx, s))
		},
	},
	{
		Name:    "/projects",
		Usage:   "/projects",
		Summary: "프로젝트 목록",
		Risk:    "read",
		Handler: func(_ context.Context, _ []string, s *Session) slashCommandResult {
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
		Handler: func(ctx context.Context, args []string, s *Session) slashCommandResult {
			return slashCommandResult{Text: handleProjectCommand(ctx, args, s), RecordHistory: projectCommandRecordsHistory(args)}
		},
	},
	{
		Name:    "/tickets",
		Usage:   "/tickets [project-key]",
		Summary: "현재 또는 지정 project의 ticket 목록",
		Risk:    "read",
		Handler: func(ctx context.Context, args []string, s *Session) slashCommandResult {
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
		Handler: func(_ context.Context, args []string, s *Session) slashCommandResult {
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
func tryHandleCommand(ctx context.Context, text string, s *Session) (string, bool) {
	result, handled := tryHandleCommandResult(ctx, text, s)
	return result.Text, handled
}

func tryHandleCommandResult(ctx context.Context, text string, s *Session) (slashCommandResult, bool) {
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
	case "current", "list", "show":
		return false
	default:
		return true
	}
}

func projectCommandRecordsHistory(args []string) bool {
	return len(args) > 0 && strings.EqualFold(args[0], "use") && len(args) >= 2
}

func modelCommandRecordsHistory(args []string, s *Session) bool {
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

func handleStatus(s *Session) string {
	stats, err := s.Store.TodayStats()
	if err != nil {
		return fmt.Sprintf("통계 조회 실패: %s", err)
	}
	return fmt.Sprintf(
		"📊 오늘 실행 통계\n총 실행: %d\n성공: %d\n실패: %d\n토큰: %d",
		stats.TotalRuns, stats.Successful, stats.Failed, stats.TotalTokens,
	)
}

func handleSkills(s *Session) string {
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
		if !s.Skill.Enabled {
			status = "⛔"
		}
		sb.WriteString(fmt.Sprintf("  %s %s — %s\n", status, s.Skill.Name, s.Skill.Description))
	}
	return sb.String()
}

func handleSession(ctx context.Context, s *Session) string {
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
			if payload.SessionID != "" {
				fmt.Fprintf(&sb, "session_id: %s\n", payload.SessionID)
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

	sb.WriteString("staff: ")
	sb.WriteString(handleStaffCurrent(s))
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

func handleContext(ctx context.Context, s *Session) string {
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
	fmt.Fprintf(&sb, "recent_window: %d\n", compaction.RecentWindow)
	fmt.Fprintf(&sb, "middle_window: %d\n", compaction.MiddleWindow)
	fmt.Fprintf(&sb, "truncate_len: %d\n", compaction.TruncateLen)
	fmt.Fprintf(&sb, "context_window: %d\n", contextWindow)
	fmt.Fprintf(&sb, "max_tokens: %d", maxTokens)
	return sb.String()
}

func currentModelLimits(s *Session) (int, int) {
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

func handleRun(ctx context.Context, name string, s *Session) string {
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

func handleTeach(ctx context.Context, description string, s *Session) string {
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

func handleProjectsCommand(s *Session) string {
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

func handleProjectCommand(ctx context.Context, args []string, s *Session) string {
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

func handleTicketsCommand(ctx context.Context, args []string, s *Session) string {
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

func handleTicketCommand(args []string, s *Session) string {
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

func firstProject(s *Session) (*store.Project, bool, error) {
	projects, err := s.Store.ListProjects(false)
	if err != nil {
		return nil, false, err
	}
	if len(projects) == 0 {
		return nil, false, nil
	}
	return &projects[0], true, nil
}

func currentProject(ctx context.Context, s *Session) (*store.Project, bool, error) {
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

func saveCurrentProject(ctx context.Context, s *Session, project *store.Project) error {
	if s == nil || s.Store == nil || project == nil {
		return nil
	}
	return s.Store.SetUserContext(currentProjectContextKey(ctx, s), project.ID, "slash_command")
}

func currentProjectContextKey(ctx context.Context, s *Session) string {
	return currentProjectContextPrefix + commandConversationID(ctx, s)
}

func commandConversationID(ctx context.Context, s *Session) string {
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
// Switch state lives in Session.activeModelOverride (atomic.Pointer) and
// resets on daemon restart — no config.toml mutation. ID match is
// case-sensitive (config IDs are user-authored exact strings; coercion
// would mask typos rather than help). Special characters are not
// validated — IDs come from config which is the trust boundary.
func handleModel(args []string, s *Session) string {
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
func currentModelID(s *Session) string {
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
func formatModelInfo(current string, models []core.ModelConfig, s *Session) string {
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

func handleStaffCommand(args []string, s *Session) string {
	if len(args) == 0 {
		return handleStaffOverview(s)
	}
	subcmd := strings.ToLower(strings.TrimSpace(args[0]))
	switch subcmd {
	case "current":
		return handleStaffCurrent(s)
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
		return handleStaffUse(strings.Join(args[1:], " "), s)
	case "hire":
		if len(args) < 2 {
			return "사용법: /staff hire <역할>"
		}
		return handleStaffHire(strings.Join(args[1:], " "), s)
	case "cancel":
		return handleStaffCancel(s)
	default:
		// Backward-compatible form: /staff <id> means /staff use <id>.
		return handleStaffUse(strings.Join(args, " "), s)
	}
}

func handleStaffOverview(s *Session) string {
	var sb strings.Builder
	sb.WriteString(handleStaffCurrent(s))
	sb.WriteString("\n\n")
	sb.WriteString(handleStaffList(s))
	sb.WriteString("\n\n명령어: /staff current | list | show <id> | use <id> | hire <역할> | cancel")
	return sb.String()
}

func handleStaffCurrent(s *Session) string {
	if s == nil || s.Config == nil {
		return "현재 staff 정보를 위한 세션이 준비되지 않았습니다."
	}
	current := s.Config.DefaultStaff
	source := "default"
	if current == "" {
		current = "default"
	}
	if s.Store != nil {
		if val, ok, err := s.Store.ConversationStaff(); err == nil && ok && val != "" {
			current = val
			source = "conversation"
		}
	}
	return fmt.Sprintf("current staff: %s (%s)", current, source)
}

func handleStaffList(s *Session) string {
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

func handleStaffShow(idOrAlias string, s *Session) string {
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

func handleStaffUse(idOrAlias string, s *Session) string {
	if s == nil || s.Store == nil {
		return "staff 변경을 위한 저장소가 준비되지 않았습니다."
	}
	id, err := setConversationStaff(s.BaseDir, s.Store, idOrAlias)
	if err != nil {
		return fmt.Sprintf("staff 변경 실패: %s", err)
	}
	return fmt.Sprintf("기본 staff를 %q로 변경했습니다.", id)
}

func handleStaffHire(role string, s *Session) string {
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

func handleStaffCancel(s *Session) string {
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

func resolveActiveStaffRecord(idOrAlias string, s *Session) (core.StaffRecord, bool, error) {
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
