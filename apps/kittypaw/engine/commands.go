package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jinto/kittypaw/core"
)

// tryHandleCommand checks if the event text is a slash command.
// Returns (response, true) if handled, ("", false) otherwise.
func tryHandleCommand(ctx context.Context, text string, s *Session) (string, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return "", false
	}

	parts := strings.Fields(text)
	cmd := strings.ToLower(parts[0])

	switch cmd {
	case "/help":
		return handleHelp(), true
	case "/status":
		return handleStatus(s), true
	case "/skills":
		return handleSkills(s), true
	case "/run":
		if len(parts) > 1 {
			return handleRun(ctx, parts[1], s), true
		}
		return "사용법: /run <skill-name>", true
	case "/teach":
		if len(parts) > 1 {
			return handleTeach(ctx, strings.Join(parts[1:], " "), s), true
		}
		return "사용법: /teach <설명>", true
	case "/staff":
		if len(parts) > 1 {
			return handleStaff(parts[1], s), true
		}
		return "사용법: /staff <staff-id>", true
	case "/model":
		return handleModel(parts[1:], s), true
	default:
		return "", false
	}
}

func handleHelp() string {
	return `KittyPaw 명령어:
/help — 도움말 표시
/status — 실행 통계 확인
/skills — 스킬 목록
/run <name> — 스킬 실행
/teach <설명> — 새 스킬 학습
/staff <staff-id> — 기본 staff 변경
/model — 현재 LLM 정보 표시
/model <id> — 채팅 중에 모델 변경 (재시작 시 기본값 복귀)`
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

func handleStaff(id string, s *Session) string {
	if s == nil || s.Store == nil {
		return "staff 변경을 위한 저장소가 준비되지 않았습니다."
	}
	if err := core.ValidateStaffID(id); err != nil {
		return fmt.Sprintf("staff id가 올바르지 않습니다: %s", err)
	}
	meta, ok, err := s.Store.GetStaffMeta(id)
	if err != nil {
		return fmt.Sprintf("staff 조회 실패: %s", err)
	}
	if ok && !meta.Active {
		return fmt.Sprintf("staff %q는 비활성화되어 있습니다.", id)
	}
	if !ok {
		base, err := core.ResolveBaseDir(s.BaseDir)
		if err != nil {
			return fmt.Sprintf("staff 조회 실패: %s", err)
		}
		if _, err := core.LoadStaff(base, id); err != nil {
			return fmt.Sprintf("staff %q를 찾지 못했습니다.", id)
		}
	}
	key := fmt.Sprintf("active_staff:%s", conversationKey(s))
	if err := s.Store.SetUserContext(key, id, "chat_command"); err != nil {
		return fmt.Sprintf("staff 변경 실패: %s", err)
	}
	return fmt.Sprintf("기본 staff를 %q로 변경했습니다.", id)
}
