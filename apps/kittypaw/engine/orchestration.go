package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/llm"
	"github.com/jinto/kittypaw/store"
)

// maxDelegateTaskLen caps task description size to prevent prompt explosion.
const maxDelegateTaskLen = 4096

// backgroundTokenCap is the hard cap for background delegate tasks.
const backgroundTokenCap = 2048

// ---------------------------------------------------------------------------
// PM decision types (JSON format)
// ---------------------------------------------------------------------------

// PMDecision is the JSON response from the PM runner.
type PMDecision struct {
	Kind   string       `json:"kind"`   // "direct" or "delegate"
	Reason string       `json:"reason"` // why this routing was chosen
	Tasks  []PMTaskSpec `json:"tasks"`  // non-empty when kind=="delegate"
}

// PMTaskSpec describes a single delegation target.
type PMTaskSpec struct {
	StaffID    string `json:"staff_id"`
	Task       string `json:"task"`
	Background bool   `json:"background,omitempty"`
}

// DelegateResult holds the outcome of a single delegation.
type DelegateResult struct {
	StaffID        string `json:"staff_id"`
	Task           string `json:"task"`
	Result         string `json:"result"`
	Success        bool   `json:"success"`
	TokenUsage     int64  `json:"token_usage"`
	ConversationID string `json:"conversation_id,omitempty"`
	DurationMs     int64  `json:"duration_ms,omitempty"`
}

type delegateSessionRunner interface {
	Run(context.Context, core.Event, *RunOptions) (string, error)
}

// ---------------------------------------------------------------------------
// OrchestrateRequest
// ---------------------------------------------------------------------------

// OrchestrateRequest routes a user message through the PM (Project Manager)
// runner which decides whether to handle directly or delegate to staff.
// Returns (response, handled, error). When handled is false, the caller
// should fall through to the default runner loop.
func OrchestrateRequest(
	ctx context.Context,
	text string,
	s *Session,
) (string, bool, error) {
	if s == nil || s.Config == nil {
		return "", false, nil
	}
	config := &s.Config.Orchestration
	if !config.Enabled {
		return "", false, nil
	}
	if s.Provider == nil {
		return "", false, nil
	}

	base, err := core.ResolveBaseDir(s.BaseDir)
	if err != nil {
		return "", false, nil
	}
	staff, err := core.ListStaffRecords(base)
	if err != nil || len(staff) == 0 {
		return "", false, nil
	}

	// PM decision.
	decision, err := pmDecide(ctx, text, staff, s.Provider)
	if err != nil {
		slog.Warn("orchestration: PM decision failed", "error", err)
		return "", false, nil
	}

	if decision.Kind == "direct" {
		return "", false, nil
	}

	if len(decision.Tasks) == 0 {
		return "", false, nil
	}

	maxDepth := int(config.MaxDepth)
	if maxDepth == 0 {
		maxDepth = 3 // default
	}

	// Execute delegations in parallel.
	results, err := fanOutDelegations(ctx, decision.Tasks, s, maxDepth, config, ConversationIDFromContext(ctx), EventFromContext(ctx))
	if err != nil {
		return "", false, fmt.Errorf("delegation fan-out: %w", err)
	}

	// Synthesize results.
	response, err := pmSynthesize(ctx, decision.Tasks, results, s.Provider)
	if err != nil {
		return "", false, fmt.Errorf("synthesis: %w", err)
	}

	return response, true, nil
}

// ---------------------------------------------------------------------------
// PM Decision (JSON)
// ---------------------------------------------------------------------------

func pmDecide(
	ctx context.Context,
	text string,
	staff []core.StaffRecord,
	provider llm.Provider,
) (*PMDecision, error) {
	var staffList strings.Builder
	for _, s := range staff {
		staffList.WriteString(fmt.Sprintf("- %s: %s\n", s.ID, s.Description))
	}

	pmPrompt := fmt.Sprintf(`You are a PM (Project Manager) runner. A user sent this message:

"%s"

Available specialist staff:
%s
Respond with a JSON object (no markdown fences):
- If the request is simple or doesn't need a specialist:
  {"kind":"direct","reason":"..."}
- If one or more specialists should handle it:
  {"kind":"delegate","reason":"...","tasks":[{"staff_id":"...","task":"..."}]}

Output ONLY valid JSON.`, text, staffList.String())

	messages := []core.LlmMessage{
		{Role: core.RoleUser, Content: pmPrompt},
	}

	resp, err := provider.Generate(WithLLMCallKind(ctx, "orchestration.route"), messages)
	if err != nil {
		return nil, err
	}

	raw := strings.TrimSpace(resp.Content)
	raw = stripFences(raw)

	var decision PMDecision
	if err := json.Unmarshal([]byte(raw), &decision); err != nil {
		slog.Warn("orchestration: JSON parse failed, falling through", "raw", raw, "error", err)
		return &PMDecision{Kind: "direct", Reason: "JSON parse failure"}, nil
	}

	return &decision, nil
}

// ---------------------------------------------------------------------------
// Fan-Out Delegations
// ---------------------------------------------------------------------------

func fanOutDelegations(
	ctx context.Context,
	tasks []PMTaskSpec,
	s *Session,
	maxDepth int,
	config *core.OrchestrationConfig,
	parentConversationID string,
	parentEvent *core.Event,
) ([]DelegateResult, error) {
	maxDelegates := int(config.MaxDelegates)
	if maxDelegates == 0 {
		maxDelegates = 5
	}
	if len(tasks) > maxDelegates {
		tasks = tasks[:maxDelegates]
	}

	results := make([]DelegateResult, len(tasks))

	// Wrap context so we can cancel all siblings when budget is exhausted.
	allCtx, cancelAll := context.WithCancel(ctx)
	defer cancelAll()

	g, gCtx := errgroup.WithContext(allCtx)

	for i, task := range tasks {
		g.Go(func() error {
			// Per-child timeout.
			childCtx, cancel := context.WithTimeout(gCtx, 60*time.Second)
			defer cancel()

			result := executeDelegateTask(childCtx, task, s, 1, maxDepth, parentConversationID, parentEvent)
			results[i] = result

			// If budget exhausted, cancel all remaining siblings.
			if s != nil && s.Budget != nil && s.Budget.Remaining() == 0 {
				slog.Warn("orchestration: budget exhausted, canceling remaining", "staff", task.StaffID)
				cancelAll()
			}

			return nil // never fail the group — we collect results
		})
	}

	_ = g.Wait()
	return results, nil
}

// executeDelegateTask runs a single delegation against a staff member.
func executeDelegateTask(
	ctx context.Context,
	task PMTaskSpec,
	s *Session,
	depth, maxDepth int,
	parentConversationID string,
	parentEvent *core.Event,
) DelegateResult {
	start := time.Now()
	result := DelegateResult{
		StaffID: task.StaffID,
		Task:    task.Task,
	}

	// Validate inputs.
	if err := core.ValidateStaffID(task.StaffID); err != nil {
		result.Result = fmt.Sprintf("invalid staff ID: %s", err)
		return result
	}
	if len(task.Task) > maxDelegateTaskLen {
		result.Result = fmt.Sprintf("task too long (%d > %d chars)", len(task.Task), maxDelegateTaskLen)
		return result
	}
	if depth >= maxDepth {
		result.Result = fmt.Sprintf("max delegation depth reached (%d)", maxDepth)
		return result
	}
	if s == nil {
		result.Result = "no session available"
		return result
	}
	if s.Store == nil {
		result.Result = "no store available"
		return result
	}

	// Load staff from the file registry. SOUL.md is the existence signal.
	base, err := core.ResolveBaseDir(s.BaseDir)
	if err != nil {
		result.Result = fmt.Sprintf("staff base error: %s", err)
		return result
	}
	canonicalID, ok, err := core.ResolveStaffReference(base, task.StaffID)
	if err != nil {
		result.Result = fmt.Sprintf("staff lookup error: %s", err)
		return result
	}
	if !ok {
		result.Result = fmt.Sprintf("staff %q not found", task.StaffID)
		return result
	}
	if _, err := core.ReadStaffMetaFile(base, canonicalID); err != nil {
		result.Result = fmt.Sprintf("staff metadata error: %s", err)
		return result
	}
	task.StaffID = canonicalID
	result.StaffID = canonicalID

	if s.Provider == nil {
		result.Result = "no LLM provider available"
		return result
	}
	if s.Sandbox == nil {
		result.Result = "no sandbox available"
		return result
	}

	if parentConversationID == "" {
		parentConversationID = store.DefaultConversationID
	}
	delegateConvID := delegateConversationID(parentConversationID, task.StaffID)
	result.ConversationID = delegateConvID
	packedTask := packDelegateTask(ctx, s, parentConversationID, task)
	delegateEvent := buildDelegateEvent(parentEvent, packedTask, delegateConvID, task.StaffID)
	usage := &RunUsageResult{}
	opts := &RunOptions{
		StaffOverride:        task.StaffID,
		ForceAgentLoop:       true,
		DisableOrchestration: true,
		Usage:                usage,
		Delegation: &DelegationRunOptions{
			ParentConversationID:   parentConversationID,
			DelegateConversationID: delegateConvID,
			StaffID:                task.StaffID,
			Task:                   task.Task,
			Depth:                  depth,
			MaxDepth:               maxDepth,
		},
	}
	if parentInfo, ok := DelegationInfoFromContext(ctx); ok {
		opts.Delegation.ParentStaffID = parentInfo.StaffID
	}

	// Determine token cap for background tasks.
	var maxTokens int
	if task.Background {
		maxTokens = backgroundTokenCap
	}
	_ = maxTokens // TODO: pass to provider when token limit per-call is supported

	var runner delegateSessionRunner = s
	output, err := runner.Run(WithLLMCallKind(ctx, "orchestration.delegate"), delegateEvent, opts)
	result.TokenUsage = usage.TokenUsage
	if err != nil {
		if usage.BudgetExhausted {
			result.Result = "token budget exhausted"
		} else {
			result.Result = fmt.Sprintf("delegate error: %s", err)
		}
		result.DurationMs = time.Since(start).Milliseconds()
		recordDelegationExecution(s, task, result, parentConversationID, delegateConvID, start, false)
		return result
	}

	result.Result = output
	result.Success = true
	result.DurationMs = time.Since(start).Milliseconds()
	recordDelegationExecution(s, task, result, parentConversationID, delegateConvID, start, true)
	return result
}

func delegateConversationID(parentConversationID, staffID string) string {
	parentPart := conversationKeyPart(parentConversationID)
	if parentPart == "" {
		parentPart = "default"
	}
	staffPart := conversationKeyPart(staffID)
	if staffPart == "" {
		staffPart = "staff"
	}
	return "delegation:" + parentPart + ":" + staffPart
}

func packDelegateTask(ctx context.Context, s *Session, parentConversationID string, task PMTaskSpec) string {
	var lines []string
	lines = append(lines,
		fmt.Sprintf("Delegated staff: %s", task.StaffID),
		fmt.Sprintf("Parent conversation: %s", parentConversationID),
		"",
		"Current delegated task:",
		strings.TrimSpace(task.Task),
	)
	if s != nil && s.Store != nil && parentConversationID != "" {
		if turns, err := s.Store.ListConversationTurnsForConversation(parentConversationID, 8); err == nil && len(turns) > 0 {
			lines = append(lines, "", "Parent conversation context:")
			for _, turn := range turns {
				content := sanitizeDelegationContext(turn.Content, 700)
				if content == "" {
					continue
				}
				role := sanitizeDelegationContext(string(turn.Role), 32)
				staff := sanitizeDelegationContext(turn.StaffID, 80)
				prefix := role
				if staff != "" {
					prefix += "/" + staff
				}
				lines = append(lines, fmt.Sprintf("- %s: %s", prefix, content))
			}
		}
	}
	if deadline, ok := ctx.Deadline(); ok {
		lines = append(lines, "", "Deadline: "+deadline.UTC().Format(time.RFC3339))
	}
	return strings.Join(lines, "\n")
}

func sanitizeDelegationContext(value string, limit int) string {
	value = strings.TrimSpace(strings.Join(strings.Fields(value), " "))
	if value == "" {
		return ""
	}
	runes := []rune(value)
	if limit > 0 && len(runes) > limit {
		return string(runes[:limit]) + "..."
	}
	return value
}

func buildDelegateEvent(parent *core.Event, text, conversationID, staffID string) core.Event {
	eventType := core.EventWebChat
	accountID := ""
	payload := core.ChatPayload{
		ChatID:         "delegate",
		Text:           text,
		SessionID:      "delegate-" + staffID,
		ConversationID: conversationID,
	}
	if parent != nil {
		eventType = parent.Type
		accountID = parent.AccountID
		if parsed, err := parent.ParsePayload(); err == nil {
			payload = parsed
			payload.Text = text
			payload.ConversationID = conversationID
			if payload.ChatID == "" {
				payload.ChatID = "delegate"
			}
			if payload.SessionID == "" {
				payload.SessionID = "delegate-" + staffID
			}
		}
	}
	raw, _ := json.Marshal(payload)
	return core.Event{Type: eventType, AccountID: accountID, Payload: raw}
}

func recordDelegationExecution(s *Session, task PMTaskSpec, result DelegateResult, parentConversationID, delegateConversationID string, start time.Time, success bool) {
	if s == nil || s.Store == nil {
		return
	}
	summary := result.Result
	if len(summary) > 200 {
		summary = summary[:200]
	}
	metadata := map[string]any{
		"staff_id":                 task.StaffID,
		"task":                     task.Task,
		"parent_conversation_id":   parentConversationID,
		"delegate_conversation_id": delegateConversationID,
		"success":                  success,
		"duration_ms":              time.Since(start).Milliseconds(),
	}
	if traces := latestDelegateToolTraces(s.Store, delegateConversationID); len(traces) > 0 {
		metadata["tool_traces"] = traces
	}
	metadataJSON := ""
	if data, err := json.Marshal(metadata); err == nil {
		metadataJSON = string(data)
	}
	if err := s.Store.RecordExecution(&store.ExecutionRecord{
		SkillID:       "delegate:" + task.StaffID,
		SkillName:     "delegation",
		StartedAt:     start.UTC().Format("2006-01-02T15:04:05Z"),
		FinishedAt:    time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		DurationMs:    time.Since(start).Milliseconds(),
		InputParams:   task.Task,
		ResultSummary: summary,
		Success:       success,
		RetryCount:    0,
		MetadataJSON:  metadataJSON,
	}); err != nil {
		slog.Warn("failed to record delegation execution", "staff_id", task.StaffID, "error", err)
	}
}

func latestDelegateToolTraces(st *store.Store, conversationID string) []core.ToolTrace {
	turns, err := st.ListConversationTurnsForConversation(conversationID, 12)
	if err != nil {
		return nil
	}
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].Role == core.RoleAssistant && len(turns[i].ToolTraces) > 0 {
			return turns[i].ToolTraces
		}
	}
	return nil
}

// loadSOUL reads ~/.kittypaw/staff/{id}/SOUL.md via core.LoadStaff.
// Returns "" on any failure.
func loadSOUL(baseDir, staffID string) string {
	base, err := core.ResolveBaseDir(baseDir)
	if err != nil {
		return ""
	}
	staff, err := core.LoadStaff(base, staffID)
	if err != nil {
		return ""
	}
	return staff.Soul
}

// ---------------------------------------------------------------------------
// PM Synthesize
// ---------------------------------------------------------------------------

// pmSynthesize combines delegation results into a single response.
func pmSynthesize(
	ctx context.Context,
	tasks []PMTaskSpec,
	results []DelegateResult,
	provider llm.Provider,
) (string, error) {
	// Count successes and failures.
	successCount := 0
	for _, r := range results {
		if r.Success {
			successCount++
		}
	}

	// All failed: return error directly, no LLM call.
	if successCount == 0 {
		var errs strings.Builder
		for _, r := range results {
			errs.WriteString(fmt.Sprintf("[%s] %s\n", r.StaffID, r.Result))
		}
		return fmt.Sprintf("All delegations failed:\n%s", errs.String()), nil
	}

	// Single task: return directly without synthesis.
	if len(results) == 1 && results[0].Success {
		return results[0].Result, nil
	}

	// Build synthesis prompt.
	var sections strings.Builder
	for _, r := range results {
		marker := ""
		if !r.Success {
			marker = " [FAILED]"
		}
		sections.WriteString(fmt.Sprintf("--- %s (%s)%s ---\n%s\n\n",
			r.StaffID, r.Task, marker, r.Result))
	}

	synthPrompt := fmt.Sprintf(`You are synthesizing results from multiple specialists.
Combine these results into a single coherent response for the user.
If any section is marked [FAILED], acknowledge the failure briefly.

%s
Provide a unified, natural response.`, sections.String())

	messages := []core.LlmMessage{
		{Role: core.RoleUser, Content: synthPrompt},
	}

	resp, err := provider.Generate(WithLLMCallKind(ctx, "orchestration.synthesis"), messages)
	if err != nil {
		// Fallback: return raw sections.
		return sections.String(), nil
	}

	return resp.Content, nil
}
