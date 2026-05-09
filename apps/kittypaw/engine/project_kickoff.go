package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/store"
)

type projectKickoffScan struct {
	Summary       string   `json:"summary"`
	RootPath      string   `json:"root_path"`
	Structure     []string `json:"structure"`
	Docs          []string `json:"docs"`
	Indicators    []string `json:"indicators"`
	GitStatus     []string `json:"git_status"`
	RecentCommits []string `json:"recent_commits"`
	BuildCommands []string `json:"build_commands"`
	TestCommands  []string `json:"test_commands"`
	Todos         []string `json:"todos"`
}

type projectKickoffTicket struct {
	TempID    string `json:"temp_id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Status    string `json:"status"`
	Priority  int    `json:"priority"`
	StaffRole string `json:"staff_role"`
}

func tryHandleProjectBriefDraftApproval(s *Session, event core.Event, eventText string) (string, bool) {
	if s == nil || s.Store == nil || !isStaffAffirmative(strings.TrimSpace(eventText)) {
		return "", false
	}
	payload, err := event.ParsePayload()
	if err != nil || strings.TrimSpace(payload.ConversationID) == "" {
		return "", false
	}
	scope, ok, err := s.Store.ConversationScope(payload.ConversationID)
	if err != nil || !ok || scope.ScopeType != "project" {
		return "", false
	}
	project, err := s.Store.GetProject(scope.ScopeID)
	if err != nil {
		return fmt.Sprintf("프로젝트를 찾는 중 오류가 났어요: %v", err), true
	}
	if !projectKickoffAwaitingDraftApproval(s.Store, project.ProjectConversationID) {
		return "", false
	}
	draft, ok, err := existingDraftProjectBrief(s.Store, project.ID)
	if err != nil {
		return fmt.Sprintf("기존 초안을 확인하는 중 오류가 났어요: %v", err), true
	}
	if !ok {
		return "승인할 티켓 초안을 찾지 못했어요.", true
	}
	result, err := s.Store.CommitProjectBriefDraft(draft.ID, "project_kickoff")
	if err != nil {
		return fmt.Sprintf("티켓 초안을 생성하는 중 오류가 났어요: %v", err), true
	}
	return formatProjectBriefCommitResponse(project, result), true
}

func tryHandleProjectKickoffApproval(s *Session, event core.Event, eventText string) (string, bool) {
	if s == nil || s.Store == nil || !isStaffAffirmative(strings.TrimSpace(eventText)) {
		return "", false
	}
	payload, err := event.ParsePayload()
	if err != nil || strings.TrimSpace(payload.ConversationID) == "" {
		return "", false
	}
	scope, ok, err := s.Store.ConversationScope(payload.ConversationID)
	if err != nil || !ok || scope.ScopeType != "project" {
		return "", false
	}
	project, err := s.Store.GetProject(scope.ScopeID)
	if err != nil {
		return fmt.Sprintf("프로젝트를 찾는 중 오류가 났어요: %v", err), true
	}
	if !projectKickoffAwaitingScan(s.Store, project.ProjectConversationID) {
		return "", false
	}
	if draft, ok, err := existingDraftProjectBrief(s.Store, project.ID); err != nil {
		return fmt.Sprintf("기존 초안을 확인하는 중 오류가 났어요: %v", err), true
	} else if ok {
		return fmt.Sprintf("%s 티켓 초안이 이미 있어요.\n\n- Draft ID: %s\n\n검토 후 승인하면 티켓으로 반영할 수 있어요.", project.Key, draft.ID), true
	}

	draft, tickets, err := createProjectKickoffDraft(s.Store, project)
	if err != nil {
		return fmt.Sprintf("프로젝트 내용을 파악하는 중 오류가 났어요: %v", err), true
	}
	return formatProjectKickoffDraftResponse(project, draft.ID, tickets), true
}

func projectKickoffAwaitingDraftApproval(st *store.Store, conversationID string) bool {
	turns, err := st.ListConversationTurnsForChat(conversationID, 20)
	if err != nil {
		return false
	}
	for i := len(turns) - 1; i >= 0; i-- {
		turn := turns[i]
		if turn.ChatID != conversationID {
			continue
		}
		if turn.Role == core.RoleAssistant &&
			strings.Contains(turn.Content, "티켓 초안") &&
			strings.Contains(turn.Content, "이대로 생성할까요") {
			return true
		}
		if turn.Role == core.RoleUser {
			return false
		}
	}
	return false
}

func projectKickoffAwaitingScan(st *store.Store, conversationID string) bool {
	turns, err := st.ListConversationTurnsForChat(conversationID, 20)
	if err != nil {
		return false
	}
	for i := len(turns) - 1; i >= 0; i-- {
		turn := turns[i]
		if turn.ChatID != conversationID {
			continue
		}
		if turn.Role == core.RoleAssistant && strings.Contains(turn.Content, "내용을 파악해서 티켓 초안을 만들까요") {
			return true
		}
		if turn.Role == core.RoleUser {
			return false
		}
	}
	return false
}

func existingDraftProjectBrief(st *store.Store, projectID string) (*store.ProjectBriefDraft, bool, error) {
	drafts, err := st.ListProjectBriefDrafts(projectID)
	if err != nil {
		return nil, false, err
	}
	for i := range drafts {
		if drafts[i].Status == "draft" {
			return &drafts[i], true, nil
		}
	}
	return nil, false, nil
}

func createProjectKickoffDraft(st *store.Store, project *store.Project) (*store.ProjectBriefDraft, []projectKickoffTicket, error) {
	scan := scanProjectForKickoff(project.RootPath)
	briefJSON, err := json.Marshal(scan)
	if err != nil {
		return nil, nil, err
	}
	tickets := projectKickoffTickets(scan)
	proposedJSON, err := json.Marshal(tickets)
	if err != nil {
		return nil, nil, err
	}
	draft, err := st.CreateProjectBriefDraft(store.CreateProjectBriefDraftRequest{
		ProjectID:           project.ID,
		Title:               project.Name + " Project Brief Draft",
		BriefJSON:           string(briefJSON),
		ProposedTicketsJSON: string(proposedJSON),
		CreatedBy:           "project_kickoff",
	})
	if err != nil {
		return nil, nil, err
	}
	return draft, tickets, nil
}

func scanProjectForKickoff(root string) projectKickoffScan {
	scan := projectKickoffScan{RootPath: root}
	scan.Structure = scanProjectStructure(root)
	scan.Docs, scan.Indicators = scanProjectTopLevelFiles(root)
	scan.GitStatus, scan.RecentCommits = scanProjectGit(root)
	scan.BuildCommands, scan.TestCommands = inferProjectCommands(scan.Indicators)
	scan.Todos = scanProjectTodos(root)
	scan.Summary = fmt.Sprintf("Scanned %d structure entries, %d docs, %d project indicators, %d git status entries, %d recent commits, and %d TODO/FIXME markers.",
		len(scan.Structure), len(scan.Docs), len(scan.Indicators), len(scan.GitStatus), len(scan.RecentCommits), len(scan.Todos))
	return scan
}

func scanProjectStructure(root string) []string {
	const maxEntries = 80
	var entries []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || path == root {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		depth := strings.Count(rel, string(filepath.Separator))
		name := d.Name()
		if d.IsDir() && (name == ".git" || name == "node_modules" || name == "vendor" || name == "build" || name == "dist") {
			return filepath.SkipDir
		}
		if depth > 2 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			rel += string(filepath.Separator)
		}
		entries = append(entries, rel)
		if len(entries) >= maxEntries {
			return filepath.SkipAll
		}
		return nil
	})
	sort.Strings(entries)
	return entries
}

func scanProjectTopLevelFiles(root string) ([]string, []string) {
	docNames := map[string]bool{"README": true, "README.md": true, "README.txt": true, "AGENTS.md": true, "CLAUDE.md": true, "CODEX.md": true}
	indicatorNames := map[string]bool{
		"package.json": true, "go.mod": true, "Cargo.toml": true, "Package.swift": true,
		"pyproject.toml": true, "requirements.txt": true, "Gemfile": true, "pom.xml": true,
		"build.gradle": true, "Makefile": true,
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, nil
	}
	var docs, indicators []string
	for _, entry := range entries {
		name := entry.Name()
		if docNames[name] {
			docs = append(docs, name)
		}
		if indicatorNames[name] {
			indicators = append(indicators, name)
		}
	}
	sort.Strings(docs)
	sort.Strings(indicators)
	return docs, indicators
}

func inferProjectCommands(indicators []string) ([]string, []string) {
	var build, test []string
	has := func(name string) bool {
		for _, indicator := range indicators {
			if indicator == name {
				return true
			}
		}
		return false
	}
	if has("go.mod") {
		build = append(build, "go build ./...")
		test = append(test, "go test ./...")
	}
	if has("package.json") {
		build = append(build, "npm run build")
		test = append(test, "npm test")
	}
	if has("Cargo.toml") {
		build = append(build, "cargo build")
		test = append(test, "cargo test")
	}
	if has("Package.swift") {
		build = append(build, "swift build")
		test = append(test, "swift test")
	}
	if has("pyproject.toml") || has("requirements.txt") {
		test = append(test, "pytest")
	}
	if has("Makefile") {
		build = append(build, "make build")
		test = append(test, "make test")
	}
	return build, test
}

func scanProjectGit(root string) ([]string, []string) {
	if lines := runProjectGitLines(root, 1, "rev-parse", "--is-inside-work-tree"); len(lines) == 0 || strings.TrimSpace(lines[0]) != "true" {
		return nil, nil
	}
	status := runProjectGitLines(root, 20, "status", "--short")
	commits := runProjectGitLines(root, 5, "log", "--oneline", "-n", "5")
	return status, commits
}

func runProjectGitLines(root string, limit int, args ...string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	output, err := cmd.Output()
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	out := make([]string, 0, min(limit, len(lines)))
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, line)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func scanProjectTodos(root string) []string {
	const maxTodos = 20
	const maxFileSize = 256 * 1024
	var todos []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if d != nil && d.IsDir() {
				switch d.Name() {
				case ".git", "node_modules", "vendor", "build", "dist":
					return filepath.SkipDir
				}
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil || info.Size() > maxFileSize {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		for i, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "TODO") || strings.Contains(line, "FIXME") {
				todos = append(todos, fmt.Sprintf("%s:%d %s", rel, i+1, strings.TrimSpace(line)))
				if len(todos) >= maxTodos {
					return filepath.SkipAll
				}
			}
		}
		return nil
	})
	return todos
}

func projectKickoffTickets(scan projectKickoffScan) []projectKickoffTicket {
	commands := append([]string{}, scan.BuildCommands...)
	commands = append(commands, scan.TestCommands...)
	commandText := "추정 명령 없음"
	if len(commands) > 0 {
		commandText = strings.Join(commands, ", ")
	}
	todoText := "TODO/FIXME marker 없음"
	if len(scan.Todos) > 0 {
		todoText = strings.Join(scan.Todos, "\n")
	}
	docText := "문서 파일 없음"
	if len(scan.Docs) > 0 {
		docText = strings.Join(scan.Docs, ", ")
	}
	indicatorText := "project indicator 없음"
	if len(scan.Indicators) > 0 {
		indicatorText = strings.Join(scan.Indicators, ", ")
	}
	return []projectKickoffTicket{
		{TempID: "structure", Title: "프로젝트 구조와 핵심 파일 파악", Body: "스캔 근거: " + strings.Join(scan.Structure, ", "), Status: store.TicketStatusBacklog, Priority: 8, StaffRole: "pm"},
		{TempID: "commands", Title: "빌드/테스트 명령 검증", Body: "추정 명령: " + commandText + "\n근거 파일: " + indicatorText, Status: store.TicketStatusBacklog, Priority: 7, StaffRole: "developer"},
		{TempID: "todos", Title: "TODO/FIXME 항목 정리", Body: todoText, Status: store.TicketStatusBacklog, Priority: 6, StaffRole: "developer"},
		{TempID: "docs", Title: "프로젝트 문서와 작업 규칙 정리", Body: "확인한 문서: " + docText, Status: store.TicketStatusBacklog, Priority: 5, StaffRole: "pm"},
		{TempID: "next-step", Title: "첫 실행 가능한 작업 선정", Body: "스캔 결과를 바탕으로 바로 처리할 첫 작업을 선택합니다.", Status: store.TicketStatusBacklog, Priority: 4, StaffRole: "pm"},
	}
}

func formatProjectKickoffDraftResponse(project *store.Project, draftID string, tickets []projectKickoffTicket) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s 티켓 초안을 만들었어요.\n\n", project.Key)
	fmt.Fprintf(&b, "- Draft ID: %s\n", draftID)
	fmt.Fprintf(&b, "- 제안 티켓: %d개\n\n", len(tickets))
	for _, ticket := range tickets {
		fmt.Fprintf(&b, "- %s: %s\n", ticket.TempID, ticket.Title)
	}
	b.WriteString("\n이대로 생성할까요?")
	return b.String()
}

func formatProjectBriefCommitResponse(project *store.Project, result *store.CommitProjectBriefDraftResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s 티켓 %d개를 생성했어요.\n\n", project.Key, len(result.Tickets))
	for _, ticket := range result.Tickets {
		fmt.Fprintf(&b, "- %s: %s\n", ticket.Key, ticket.Title)
	}
	return strings.TrimSpace(b.String())
}
