package engine

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/llm"
	mcpreg "github.com/jinto/kittypaw/mcp"
	"github.com/jinto/kittypaw/sandbox"
	"github.com/jinto/kittypaw/store"
)

const maxRetries = 3

// turnCacheTTL is how long a completed turn's result is retained.
// After this window a retry with the same turn_id re-executes — the
// Phase 12 ErrServerSide guard remains the safety net for that cold
// path.
const turnCacheTTL = 10 * time.Minute

// turnRunMaxTime caps owner execution. The owner runs on a context
// detached from the caller's (so a transport drop doesn't abort the
// in-flight LLM call — that's the dedup contract), so a separate
// upper bound keeps abandoned turns from running forever.
const turnRunMaxTime = 5 * time.Minute

// turnState tracks an in-flight or completed turn keyed by client-
// supplied turn_id. The done channel is closed when result/err are
// filled; concurrent or sequential retries with the same turn_id wait
// on done and read the cached values, so the server never executes
// the same turn twice.
type turnState struct {
	done   chan struct{}
	result string
	err    error
}

// PermissionCallback is called when the runner needs user permission for an action.
type PermissionCallback func(ctx context.Context, description, resource string) (bool, error)

// BrowserController executes built-in Browser.* calls. It is an interface so
// engine tests can use fakes without launching Chrome.
type BrowserController interface {
	Execute(context.Context, core.SkillCall) (string, error)
	Close() error
}

// RunOptions holds per-call options for Session.Run. Callbacks are scoped to
// a single Run invocation, avoiding shared mutable state across concurrent calls.
type RunOptions struct {
	OnPermission  PermissionCallback
	ModelOverride string // use a named model from config [[models]] for this run
}

// SetActiveModel records the user's `/model <id>` choice for subsequent chat
// turns. Empty string clears the override (revert to config default).
func (s *Session) SetActiveModel(id string) {
	s.activeModelOverride.Store(&id)
}

// GetActiveModel returns the chat-set model override, or "" when none is set.
func (s *Session) GetActiveModel() string {
	if p := s.activeModelOverride.Load(); p != nil {
		return *p
	}
	return ""
}

// ApplyActiveModel folds the chat-path /model override into RunOptions.
// Caller contract: chat-path dispatchers (server.go:dispatchLoop chat case,
// ws.go chat handler, chat_relay_dispatcher) MUST call this after building
// RunOptions and before invoking Session.Run; the schedule path
// (schedule.go:tickOnce / reflectionTick) MUST NOT call this — schedule
// jobs always run with the configured default, never inheriting a chat-set
// override.
//
// Precedence: explicit RunOptions.ModelOverride wins over the chat
// override. This keeps schedule.go's per-job model selection unchanged
// even when a chat user has set a /model override in the same session.
func (s *Session) ApplyActiveModel(opts *RunOptions) *RunOptions {
	id := s.GetActiveModel()
	if id == "" {
		return opts
	}
	if opts == nil {
		return &RunOptions{ModelOverride: id}
	}
	if opts.ModelOverride != "" {
		return opts
	}
	out := *opts
	out.ModelOverride = id
	return &out
}

// Session holds the injected dependencies for processing events.
// Create once, call Run() for each event. Session is safe for concurrent use;
// per-call state is passed via RunOptions.
type Session struct {
	Provider          llm.Provider
	FallbackProvider  llm.Provider
	Sandbox           *sandbox.Sandbox
	Store             *store.Store
	Config            *core.Config
	BaseDir           string           // account base directory (e.g. ~/.kittypaw/accounts/alice/)
	McpRegistry       *mcpreg.Registry // nil when no MCP servers configured
	BrowserController BrowserController
	Budget            *SharedTokenBudget // shared across orchestration, MoA, File.summary
	// SummaryFlight dedups concurrent File.summary misses; nil → per-call group.
	SummaryFlight     *singleflight.Group
	Indexer           Indexer                   // nil when workspace indexer is not initialized
	PackageManager    *core.PackageManager      // nil when packages are not configured
	APITokenMgr       *core.APITokenManager     // nil when API token management is not configured
	ServiceTokenMgr   *core.ServiceTokenManager // nil when external OAuth services are not configured
	ProjectJobRuntime *ProjectJobRuntime
	allowedPaths      atomic.Pointer[[]string] // cached workspace paths for isPathAllowed

	// activeModelOverride: turn-level model swap from the chat REPL `/model`
	// command. Read by chat-path handlers via GetActiveModel and forwarded
	// into RunOptions.ModelOverride before Run is invoked. atomic.Pointer
	// keeps this safe for the concurrent-use Session contract (line 66) —
	// scheduler tick goroutines and dispatchLoop never read this field;
	// the schedule path manufactures its own RunOptions (schedule.go:428).
	// Reset to default on daemon restart (no config persistence).
	activeModelOverride atomic.Pointer[string]

	// AccountID is the account this Session belongs to. Empty only for
	// legacy single-account callers that haven't migrated to the account
	// router yet. Share.read / Fanout.* use this as the *reader* identity
	// when consulting the owner account's Share allowlist.
	AccountID string
	// AccountRegistry lets the Session look up peer accounts (for cross-
	// account reads + fanout) without coupling to server state. Nil in
	// single-account mode; Share.read returns an "unavailable" error
	// rather than panicking when the field is unset.
	AccountRegistry *core.AccountRegistry
	// Fanout is non-nil only for the team-space account. When nil, the sandbox
	// skips the Fanout global so personal skills see
	// `typeof Fanout === "undefined"`.
	Fanout core.Fanout

	// Health tracks account-level liveness for panic isolation (AC-T8).
	// nil on bare-struct test fixtures; RecoverAccountPanic /
	// MarkAccountReady are nil-safe, so callers don't need to guard.
	Health *core.HealthState

	// Pipeline carries multi-turn state for the deterministic-branch
	// pipeline (most recent skill search results, etc). Lazily
	// initialized on first dispatch; nil-safe via the methods on
	// PipelineState.
	Pipeline *PipelineState

	// turnCache deduplicates retries of the same turn_id across the
	// transport-drop reconnect path (Phase 12). Keys are client-
	// allocated UUIDs; values are *turnState. Zero-value sync.Map is
	// usable as-is — no init needed. Entries are evicted by a
	// time.AfterFunc set at completion (TTL = turnCacheTTL).
	turnCache sync.Map
}

// sandboxOptions returns the sandbox.Options that govern which account-scoped
// JS globals (Share, Fanout) are exposed for this session. Centralized so
// every sandbox callsite stays consistent — a personal session that wires
// Share but not Fanout (or vice versa) would silently break the I5 invariant.
func (s *Session) sandboxOptions() sandbox.Options {
	return sandbox.Options{
		ExposeFanout: s.Fanout != nil,
		ExposeShare:  s.Config != nil && !s.Config.IsSharedAccount(),
	}
}

// AllowedPaths returns the cached list of file-visible project/folder roots.
// Returns nil when no roots are registered (deny-all by default).
func (s *Session) AllowedPaths() []string {
	if p := s.allowedPaths.Load(); p != nil {
		return *p
	}
	return nil
}

// ClearAllowedPaths sets the cache to empty, denying all file operations.
// Used as a fail-closed fallback when RefreshAllowedPaths fails after a delete.
func (s *Session) ClearAllowedPaths() {
	empty := []string{}
	s.allowedPaths.Store(&empty)
}

// RefreshAllowedPaths reloads file-visible roots from the database into the
// atomic cache. Paths are pre-resolved (Abs + EvalSymlinks) so isPathAllowedResolved
// can do a fast prefix match without syscalls. Call after any project/folder CRUD.
// Returns an error so callers (e.g., delete handler) can fail-closed.
func (s *Session) RefreshAllowedPaths() error {
	raw, err := s.Store.ListFileIndexRootPaths()
	if err != nil {
		slog.Error("failed to refresh allowed paths", "error", err)
		return err
	}
	resolved := make([]string, 0, len(raw))
	for _, p := range raw {
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		if r, err := filepath.EvalSymlinks(abs); err == nil {
			abs = r
		}
		resolved = append(resolved, abs)
	}
	s.allowedPaths.Store(&resolved)
	return nil
}

// RunTurn is an idempotent variant of Run keyed by a client-supplied
// turn_id. Concurrent or sequential retries with the same turn_id are
// deduped: the first caller executes Run, subsequent callers wait on
// the shared done channel and read the cached result. Empty turnID
// falls through to plain Run for legacy clients with no idempotency.
//
// Owner execution runs on a context detached from the caller's (so a
// transport drop on the WS handler does not abort the in-flight LLM
// call — *that* is the dedup contract; without detachment the very
// retry case the cache exists for would observe a context-canceled
// result), bounded by turnRunMaxTime so an abandoned turn cannot run
// forever. Owner panics are recovered, surface as state.err for
// waiters, evict the poisoned entry, and re-panic so upstream account
// panic recovery records the failure.
//
// The cache entry is evicted after turnCacheTTL by a time.AfterFunc;
// retries arriving later re-execute (Phase 12 client.ErrServerSide
// guard catches the false-positive cases). Server restart wipes the
// in-memory cache — true cross-restart idempotency is deferred to a
// future store-backed phase.
func (s *Session) RunTurn(ctx context.Context, turnID string, event core.Event, opts *RunOptions) (string, error) {
	if turnID == "" {
		return s.Run(ctx, event, opts)
	}

	candidate := &turnState{done: make(chan struct{})}
	actual, loaded := s.turnCache.LoadOrStore(turnID, candidate)
	state := actual.(*turnState)

	if loaded {
		select {
		case <-state.done:
			return state.result, state.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	// Cache miss: own the turn. The exec closure encapsulates Run so
	// runTurnOwner stays testable with a synthetic exec function.
	s.runTurnOwner(ctx, turnID, state, func(c context.Context) (string, error) {
		return s.Run(c, event, opts)
	})
	return state.result, state.err
}

// runTurnOwner executes the owner side of a RunTurn — context
// detachment, panic recovery, done-channel signaling, and TTL
// scheduling. Split from RunTurn so unit tests can inject a synthetic
// exec function without constructing a full Session. See RunTurn for
// the contract.
func (s *Session) runTurnOwner(ctx context.Context, turnID string, state *turnState, exec func(context.Context) (string, error)) {
	ownerCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), turnRunMaxTime)
	defer cancel()

	defer func() {
		if r := recover(); r != nil {
			state.err = fmt.Errorf("turn panicked: %v", r)
			close(state.done)
			// Poisoned entry — evict immediately. Future retries take
			// the cold path (re-execute under ErrServerSide guard).
			s.turnCache.Delete(turnID)
			panic(r)
		}
		close(state.done)
		time.AfterFunc(turnCacheTTL, func() {
			s.turnCache.Delete(turnID)
		})
	}()

	state.result, state.err = exec(ownerCtx)
}

// Run processes a single event through the runner loop.
func (s *Session) Run(ctx context.Context, event core.Event, opts *RunOptions) (string, error) {
	// Fast path: slash commands
	eventText := FormatEvent(&event)
	ctx = ContextWithEvent(ctx, &event)
	ctx = ContextWithConversationID(ctx, conversationKeyForEvent(s, &event))
	if result, handled := tryHandleCommandResult(ctx, eventText, s); handled {
		if result.RecordHistory && s.Store != nil {
			if err := s.recordPipelineTurn(event, eventText, result.Text); err != nil {
				slog.Warn("slash command turn record failed", "error", err)
			}
		}
		return result.Text, nil
	}
	if response, handled := tryHandleProjectBriefDraftApproval(s, event, eventText); handled {
		if err := s.recordPipelineTurn(event, eventText, response); err != nil {
			slog.Warn("project brief draft approval turn record failed", "error", err)
		}
		return response, nil
	}
	if response, handled := tryHandleProjectKickoffApproval(s, event, eventText); handled {
		if err := s.recordPipelineTurn(event, eventText, response); err != nil {
			slog.Warn("project kickoff turn record failed", "error", err)
		}
		return response, nil
	}

	// Pipeline dispatch — deterministic branches before the LLM runner
	// loop. Legacy fallback when classifyIntent returns
	// IntentLegacyFallback or a branch errors.
	if s.Pipeline == nil {
		s.Pipeline = NewPipelineState()
	}
	if response, handled := dispatchPipeline(ctx, s, event, eventText); handled {
		// Branch dispatch bypasses runAgentLoop — and therefore would
		// also bypass conversation-history persistence unless we record
		// the turn here. Without this, a follow-up turn that *does* hit
		// the legacy LLM (e.g. "지금 계산결과를 원화 기준으로 다시")
		// finds no record of the prior branch response and reports
		// "맥락이 없어서…" — the 2026-04-27 transcript turn 4
		// regression. Errors are logged but do not fail the response;
		// the user-visible reply already exists and history loss is
		// graceful.
		//
		// First-turn proactive suggestion is appended BEFORE the turn
		// record. The runner-loop path lets the LLM weave suggestions
		// into its reply via system-prompt augmentation; the branch
		// path returns a deterministic skill output with no LLM in the
		// response loop, so we surface the suggestion as a literal
		// suffix instead. Without this, the most likely use case for
		// the "AI가 먼저 자동화 제안" promise — a user asking "환율"
		// and being routed to RunInstalledSkillBranch — would never
		// see the suggestion at all.
		response = appendSuggestionForBranchResponse(s, event, response)
		if err := s.recordPipelineTurn(event, eventText, response); err != nil {
			slog.Warn("pipeline turn record failed", "error", err)
		}
		return response, nil
	}

	return s.runAgentLoop(ctx, event, eventText, opts)
}

// followupQueryRuneCap caps the rune length of a "short follow-up" —
// queries longer than this are treated as fresh requests, not implicit
// references. 30 runes covers "원화로 환율 알려줘 자세히" / "그건 어떻게
// 계산해야 하는지" — anything longer reads as a self-contained query.
const followupQueryRuneCap = 30

// augmentSystemPromptWithRecentSkillOutput appends a cross-turn
// context block to the first system message when (a) the current
// user turn is short enough to plausibly be a follow-up, and (b)
// PipelineState has a fresh skill output cached. Mutates messages in
// place; no-op when either condition fails or messages is empty / has
// no leading system message.
//
// The conversation transcript also carries the same data via Phase 8
// history append, but the LLM's "ignore history → re-search" prior
// is observably stronger than its "use history → transform" prior
// (Phase 10 prompt-only attempt: ROI 0). System-prompt augmentation
// re-surfaces the data inside a message the model treats as
// authoritative, which routes around the history-ignoring prior.
func augmentSystemPromptWithRecentSkillOutput(messages []core.LlmMessage, userText string, ps *PipelineState) {
	if len(messages) == 0 || messages[0].Role != core.RoleSystem {
		return
	}
	if runeCount(userText) > followupQueryRuneCap {
		return
	}
	if ps == nil {
		return
	}
	recent := ps.RecentSkillOutput()
	if recent == "" {
		return
	}
	const augmentTemplate = `

## Cross-turn context — recent skill output (load-bearing)
The user's previous turn produced this raw output from a deterministic skill:

---
%s
---

The current user turn is short (≤30 chars) and may reference this data implicitly.
- If it shapes like a transform / modifier ("원화로", "엔으로", "간단히", "다시", "계산해", "그것"), apply JS arithmetic on the prior numbers (use ` + "`Code.exec(jsCode)`" + ` if uncertain). Do NOT call Web.search or Skill.search for the same domain — the data is right above.
- Do NOT reverse-clarify ("무엇을?") when a plausible interpretation exists.
- Do NOT offer to install a skill that already produced this output.`

	messages[0].Content += fmt.Sprintf(augmentTemplate, recent)
}

// suggestionSilenceWindow is how long a surfaced suggestion stays
// suppressed before becoming eligible to surface again. Chosen to match
// the "weekly reflection" cadence — a candidate seen Sunday will not
// re-surface until at least the next Sunday, even if the user has many
// chat sessions in between.
const suggestionSilenceWindow = 7 * 24 * time.Hour

// augmentSystemPromptWithSuggestion injects an active reflection
// suggestion into the system prompt on the FIRST turn of a session,
// then records the surface time so the same candidate stays silent for
// suggestionSilenceWindow.
//
// This is the chat-side delivery path the cases/landing page promises
// ("AI가 먼저 자동화 제안"). Without it the reflection cycle's
// suggest_candidate:* rows never reach the user — they're queryable on
// admin-API only. The injection is a soft instruction; the LLM decides
// whether and how to surface the suggestion in its natural reply.
//
// "First turn of a session" is detected by the absence of any prior
// assistant turn — the just-added user turn is already in state.Turns
// at the call site. Mutates messages in place; no-op when there's no
// leading system message, the turn is not the first, the store probe
// fails, no candidate is ready to surface, or any candidate's value is
// malformed.
func augmentSystemPromptWithSuggestion(
	messages []core.LlmMessage,
	st *store.Store,
	turns []core.ConversationTurn,
) {
	if len(messages) == 0 || messages[0].Role != core.RoleSystem {
		return
	}
	for _, t := range turns {
		if t.Role == core.RoleAssistant {
			return // not the first turn
		}
	}
	if st == nil {
		return
	}
	candidate, hash := pickActiveSuggestion(st)
	if candidate == "" {
		return
	}

	const augmentTemplate = `

## Optional proactive suggestion
Reflection has detected a repeating user intent: %q.
Skip this entirely if the current message is unrelated. Otherwise, at a
natural beat after answering the user's actual question, you MAY append
a short one-line proposal to automate the recurring intent — e.g.
"이거 자주 보시는 것 같아요. 매일 아침 자동으로 받으시겠어요?". The
proposal is a coda, not the main course; never derail the answer.`

	messages[0].Content += fmt.Sprintf(augmentTemplate, candidate)

	// Record the surface time so this candidate stays silent for
	// suggestionSilenceWindow, even across server restarts. Best-effort:
	// a write failure is logged warn but does not block delivery — at
	// worst the same suggestion surfaces again on the next session.
	if err := st.SetUserContext(
		"surfaced_at:"+hash,
		time.Now().UTC().Format(time.RFC3339),
		"suggestion",
	); err != nil {
		slog.Warn("suggestion: failed to record surface time", "hash", hash, "error", err)
	}
}

// pickActiveSuggestion returns the label of the first reflection
// candidate that has not been surfaced within suggestionSilenceWindow,
// along with the hash used for dedup keying. ("", "") when none.
//
// Candidate value layout (from RunReflectionCycle): "label|count|cron".
// We only need the label here; count and cron are dropped silently.
func pickActiveSuggestion(st *store.Store) (label, hash string) {
	candidates, err := st.ListUserContextPrefix("suggest_candidate:")
	if err != nil || len(candidates) == 0 {
		return "", ""
	}
	cutoff := time.Now().Add(-suggestionSilenceWindow)
	for _, kv := range candidates {
		h := strings.TrimPrefix(kv.Key, "suggest_candidate:")
		if h == kv.Key {
			continue // malformed prefix
		}
		// Skip if surfaced within the silence window. A corrupt
		// surfaced_at value (parse error) is treated as expired —
		// surface again so the user is not silenced indefinitely by a
		// bad write.
		if rawTS, ok, _ := st.GetUserContext("surfaced_at:" + h); ok {
			if t, err := time.Parse(time.RFC3339, rawTS); err == nil && t.After(cutoff) {
				continue
			}
		}
		// Value layout "label|count|cron" — split on first '|' only;
		// labels in user prose may contain bars in rare cases.
		parts := strings.SplitN(kv.Value, "|", 2)
		if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
			continue
		}
		return strings.TrimSpace(parts[0]), h
	}
	return "", ""
}

// appendSuggestionForBranchResponse adds a literal suggestion suffix to
// a deterministic branch (RunInstalledSkillBranch / InstallConsentBranch
// / etc) response when this is the account conversation's first turn AND there is an
// active reflection candidate outside the silence window. Branch paths
// don't run an LLM after the skill executes, so the
// system-prompt-augmentation path used by the runner loop has no effect
// here — we have to compose the proposal text ourselves.
//
// First-turn detection probes the account-wide store: zero prior assistant
// turns means first turn. Branch dispatch hasn't yet recorded the just-arrived
// user turn (recordPipelineTurn runs after this), so the transcript will show
// role=assistant only if a previous channel already reached this account.
//
// Best-effort: any store error or missing candidate returns response
// unchanged. surfaced_at is recorded on success so the same candidate
// stays silent for suggestionSilenceWindow.
func appendSuggestionForBranchResponse(s *Session, event core.Event, response string) string {
	if s == nil || s.Store == nil {
		return response
	}
	state, err := s.Store.LoadConversationState()
	if err != nil {
		return response
	}
	// state == nil ⇒ never seen this account conversation ⇒ definitely first turn.
	// state != nil but no assistant role yet ⇒ also first turn (the
	// just-arrived user turn isn't yet recorded; recordPipelineTurn
	// runs after this).
	if state != nil {
		for _, t := range state.Turns {
			if t.Role == core.RoleAssistant {
				return response // not first turn
			}
		}
	}
	label, hash := pickActiveSuggestion(s.Store)
	if label == "" {
		return response
	}
	suffix := fmt.Sprintf(
		"\n\n💡 \"%s\"을(를) 자주 보시는 것 같아요. 매일 아침 자동으로 받으시겠어요? 원하시면 \"네\"라고 답해주세요.",
		label,
	)
	if err := s.Store.SetUserContext(
		"surfaced_at:"+hash,
		time.Now().UTC().Format(time.RFC3339),
		"suggestion",
	); err != nil {
		slog.Warn("suggestion(branch): failed to record surface time", "hash", hash, "error", err)
	}
	return response + suffix
}

// stripBranchControlMarker removes engine-emitted control prefixes
// (currently just the InstallConsentBranch ack) from a branch response
// before it lands in conversation history. The user-visible reply
// keeps the marker — the strip is only so a follow-up turn dispatched
// to the legacy LLM does not see the ack pattern in history and copy
// it back into its own response. This caused the 2026-04-27
// regression where a third-turn "환율" routed to the legacy LLM and
// re-emitted "✅ '환율 조회' 스킬을 설치했어요." verbatim from history.
//
// The strip is line-prefix only (we do not parse the response body),
// so it is safe to extend with new markers without rewriting callers.
func stripBranchControlMarker(response string) string {
	const ackTail = "스킬을 설치했어요.\n\n"
	if i := strings.Index(response, ackTail); i >= 0 {
		// Find the line start of the ack — usually "✅ '...' " before
		// "스킬을 설치했어요." Drop the entire ack line plus the blank
		// separator after it, keep whatever the branch appended below.
		lineStart := strings.LastIndex(response[:i], "\n")
		if lineStart < 0 {
			lineStart = 0
		} else {
			lineStart++ // skip the newline itself
		}
		return response[:lineStart] + response[i+len(ackTail):]
	}
	return response
}

// recordPipelineTurn persists the user query + branch response onto the
// account conversation history so subsequent turns (whether dispatched
// by another branch or by the legacy LLM) see the cross-turn context.
// Mirrors the user-turn / assistant-turn pair that runAgentLoop emits
// at lines 215-246; the duplication is small and the alternative —
// extracting a shared helper — would entangle the legacy loop with the
// branch path more than is worth the saved lines.
func (s *Session) recordPipelineTurn(event core.Event, eventText, response string) error {
	convKey := conversationKeyForEvent(s, &event)
	meta := conversationTurnSource(&event)
	state, err := s.loadConversationStateForRun(convKey)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	if state == nil {
		// First-ever turn for this account — runAgentLoop creates the row
		// lazily on the same path. Mirror that here so a branch dispatch
		// is allowed to be the account's very first interaction.
		state = &core.ConversationState{
			ConversationID: convKey,
			SystemPrompt:   SystemPrompt,
		}
		if err := s.Store.SaveConversationState(state); err != nil {
			return fmt.Errorf("save initial state: %w", err)
		}
	}
	now := core.NowTimestamp()
	userTurn := core.ConversationTurn{
		ConversationID: convKey,
		Role:           core.RoleUser,
		Content:        eventText,
		Channel:        meta.Channel,
		ChannelUserID:  meta.ChannelUserID,
		ChatID:         meta.ChatID,
		MessageID:      meta.MessageID,
		Timestamp:      now,
	}
	state.Turns = append(state.Turns, userTurn)
	if err := s.Store.AddConversationTurn(&userTurn); err != nil {
		return fmt.Errorf("add user turn: %w", err)
	}
	assistantTurn := core.ConversationTurn{
		ConversationID: convKey,
		Role:           core.RoleAssistant,
		Content:        stripBranchControlMarker(response),
		Channel:        meta.Channel,
		ChannelUserID:  meta.ChannelUserID,
		ChatID:         meta.ChatID,
		Timestamp:      now,
	}
	state.Turns = append(state.Turns, assistantTurn)
	if err := s.Store.AddConversationTurn(&assistantTurn); err != nil {
		return fmt.Errorf("add assistant turn: %w", err)
	}
	return s.Store.SaveConversationState(state)
}

func (s *Session) runAgentLoop(ctx context.Context, event core.Event, rawEventText string, opts *RunOptions) (string, error) {
	loopStart := time.Now()
	channelName := event.Type.ChannelName()
	convKey := conversationKeyForEvent(s, &event)
	meta := conversationTurnSource(&event)

	// Extract callbacks from options.
	var onPermission PermissionCallback
	if opts != nil {
		onPermission = opts.OnPermission
	}
	ctx = ContextWithPermissionCallback(ctx, onPermission)

	// Store the account conversation key and event in context for downstream handlers.
	ctx = ContextWithConversationID(ctx, convKey)
	ctx = ContextWithEvent(ctx, &event)

	// Load or create conversation state. Scoped project/ticket chats use only
	// their own turns so unrelated account history does not enter the prompt.
	state, err := s.loadConversationStateForRun(convKey)
	if err != nil {
		return "", fmt.Errorf("load state: %w", err)
	}
	if state == nil {
		state = &core.ConversationState{
			ConversationID: convKey,
			SystemPrompt:   SystemPrompt,
		}
		if err := s.Store.SaveConversationState(state); err != nil {
			return "", fmt.Errorf("save initial state: %w", err)
		}
	}

	slog.Info("conversation state ready", "phase", core.PhaseInit, "conversation", convKey)

	// Parse @mention routing
	var mentionOverride string
	eventText := rawEventText
	if staffID, remaining, matched := ParseAtMention(rawEventText); matched {
		base, err := core.ResolveBaseDir(s.BaseDir)
		if err != nil {
			return "", fmt.Errorf("resolve staff base: %w", err)
		}
		if core.StaffHasSoul(base, staffID) {
			slog.Info("@mention routing", "staff_id", staffID)
			mentionOverride = staffID
			eventText = remaining
		} else {
			response := fmt.Sprintf("staff %q를 찾지 못했습니다.", staffID)
			if core.StaffHasDraft(base, staffID) {
				response = fmt.Sprintf("staff %q는 아직 생성 중입니다. 먼저 생성 승인을 완료해 주세요.", staffID)
			}
			if err := s.recordPipelineTurn(event, rawEventText, response); err != nil {
				slog.Warn("mention rejection turn record failed", "error", err)
			}
			return response, nil
		}
	}

	// Add user turn
	userTurn := core.ConversationTurn{
		ConversationID: convKey,
		Role:           core.RoleUser,
		Content:        eventText,
		Channel:        meta.Channel,
		ChannelUserID:  meta.ChannelUserID,
		ChatID:         meta.ChatID,
		MessageID:      meta.MessageID,
		Timestamp:      core.NowTimestamp(),
	}
	state.Turns = append(state.Turns, userTurn)
	if err := s.Store.AddConversationTurn(&userTurn); err != nil {
		return "", fmt.Errorf("add turn: %w", err)
	}

	// Check daily token budget
	if s.Config.Features.DailyTokenLimit > 0 {
		stats, err := s.Store.TodayStats()
		if err == nil && stats.TotalTokens >= int64(s.Config.Features.DailyTokenLimit) {
			return "", fmt.Errorf("daily token limit reached (%d/%d)",
				stats.TotalTokens, s.Config.Features.DailyTokenLimit)
		}
	}

	// Orchestration gate: PM runner may delegate to staff.
	if response, handled, orchErr := OrchestrateRequest(
		ctx, eventText, s.Provider, s.Store, &s.Config.Orchestration, s.Budget, s.BaseDir,
	); orchErr != nil {
		slog.Warn("orchestration error, falling through", "error", orchErr)
	} else if handled {
		assistantTurn := core.ConversationTurn{
			ConversationID: convKey,
			Role:           core.RoleAssistant,
			Content:        response,
			Channel:        meta.Channel,
			ChannelUserID:  meta.ChannelUserID,
			ChatID:         meta.ChatID,
			Timestamp:      core.NowTimestamp(),
		}
		state.Turns = append(state.Turns, assistantTurn)
		_ = s.Store.AddConversationTurn(&assistantTurn)
		_ = s.Store.SaveConversationState(state)
		return response, nil
	}

	// Load memory context once before retry loop.
	memoryContext := ""
	if lines, err := s.Store.MemoryContextLines(); err != nil {
		slog.Warn("failed to load memory context", "error", err)
	} else if len(lines) > 0 {
		memoryContext = strings.Join(lines, "\n\n")
	}

	// Build MCP tools section once (AllTools returns cached data).
	var mcpToolsSection string
	if s.McpRegistry != nil {
		mcpToolsSection = BuildMCPToolsSection(s.McpRegistry.AllTools())
	}

	// Observe + retry loop.
	// Outer loop: observe rounds (Runner.observe() re-invokes LLM with observations).
	// Inner loop: retry on execution errors.
	// When observe is not used, observeRound stays 0 and behavior is unchanged.
	maxObserveRounds := s.Config.Features.MaxObserveRounds
	if maxObserveRounds <= 0 {
		maxObserveRounds = 5
	}

	var observations []core.Observation
	var turnToolTraces []core.ToolTrace
	var lastError string
	modelOverridden := opts != nil && opts.ModelOverride != ""
	activeProvider := s.Provider
	if modelOverridden {
		activeProvider = s.resolveProvider(opts.ModelOverride)
	}
	fallbackUsed := false

observeLoop:
	for observeRound := 0; observeRound <= maxObserveRounds; observeRound++ {
		lastError = ""

		if observeRound > 0 {
			slog.Info("observe round", "round", observeRound, "observations", len(observations))
		}

		for attempt := range maxRetries {
			if attempt > 0 {
				slog.Info("retry attempt", "attempt", attempt, "max", maxRetries)
			}

			// Build compaction config based on attempt and feature flags
			compaction := s.compactionForAttempt(attempt)

			// Resolve and load staff.
			staffID := ResolveStaffName(s.Config, channelName, convKey, mentionOverride, s.Store, s.BaseDir)
			staff := loadStaffForPrompt(staffID, s.Config, s.BaseDir)

			// Build prompt (observations are volatile — replaced each observe round)
			messages := BuildPrompt(state, eventText, compaction, s.Config, channelName, staff, memoryContext, mcpToolsSection, observations, s.BaseDir)

			// Cross-turn augmentation — short follow-up + recent skill output.
			// The conversation transcript already carries the prior assistant
			// output, but the LLM's "ignore-history-and-re-search" prior is
			// observably stronger than its "use-history" prior. Re-surface
			// the data inside the system message itself so the prior cannot
			// route around it.
			augmentSystemPromptWithRecentSkillOutput(messages, eventText, s.Pipeline)

			// First-turn proactive suggestion — surface a reflection
			// candidate when this is the user's first message of the
			// session. The 7-day silence window prevents the same
			// suggestion repeating across back-to-back sessions.
			augmentSystemPromptWithSuggestion(messages, s.Store, state.Turns)

			slog.Info("prompt built",
				"phase", core.PhasePrompt,
				"attempt", attempt,
				"observe_round", observeRound,
				"message_count", len(messages),
				"recent_window", compaction.RecentWindow,
			)

			// Proactive token budget check
			estTokens := 0
			for _, m := range messages {
				estTokens += EstimateTokens(m.Content)
			}
			tokenBudget := activeProvider.ContextWindow() - activeProvider.MaxTokens()
			if estTokens > tokenBudget && attempt < maxRetries-1 {
				slog.Warn("prompt exceeds token budget, tightening compaction",
					"est_tokens", estTokens, "budget", tokenBudget, "attempt", attempt)
				lastError = fmt.Sprintf("estimated %d tokens exceeds budget %d", estTokens, tokenBudget)
				continue
			}

			// Error-class-specific retry guidance. Only inject on retry, so
			// the static prompt stays lean. Each branch targets a failure
			// pattern observed in production logs.
			if lastError != "" {
				hint := fmt.Sprintf("Your previous code had an error:\n%s\n\nPlease fix the code and try again.", lastError)
				switch {
				case strings.Contains(lastError, "SyntaxError"),
					strings.Contains(lastError, "ReferenceError"):
					// Korean prose → SyntaxError, English first-word → ReferenceError.
					hint += "\n\nIMPORTANT: If your intended reply is plain prose (e.g. clarification, ack, chitchat), wrap it as a JS string return: `return \"문장\";` / `return \"text\";`. The sandbox parses your output as JavaScript, so a bare Korean/English sentence will always fail to parse."
				case strings.Contains(lastError, "TypeError"):
					// Undefined-property access — usually `sk.results[0].id`
					// when results is empty, or `r.output` when r is the
					// error shape from a missing skill.
					hint += "\n\nIMPORTANT: TypeError usually means you accessed a property on undefined. Guard tool results before drilling in: `if (!sk.results?.length) return \"...\"; const id = sk.results[0].id;`. For `Skill.run(id)`, check `.error` before `.output` — the result on a missing skill carries `.error` plus a user-facing `.output` message you can return as-is."
				case strings.Contains(lastError, "not found in registry"):
					// Wrong-id install attempt — model truncated the name.
					hint += "\n\nIMPORTANT: `Skill.installFromRegistry(id)` requires the *exact* id from the previous `Skill.search` result — never truncate or translate the skill name. The id is distinct from the name (e.g. \"currency-exchange-rates\" vs \"환율 조회\"). Recall the id from the immediate prior tool call, or re-call `Skill.search` with the same keyword to fetch it."
				}
				messages = append(messages, core.LlmMessage{
					Role:    core.RoleUser,
					Content: hint,
				})
			}

			// Call LLM
			var resp *llm.Response
			resp, err = activeProvider.Generate(WithLLMCallKind(ctx, "chat"), messages)

			if err != nil {
				// Handle retryable errors
				if attempt < maxRetries-1 {
					slog.Warn("LLM error, retrying", "attempt", attempt, "error", err)
					lastError = err.Error()
					time.Sleep(2 * time.Second)
					continue
				}
				// Try fallback on last attempt (skip when model was explicitly overridden).
				if !fallbackUsed && !modelOverridden && s.FallbackProvider != nil {
					slog.Warn("switching to fallback provider", "error", err)
					activeProvider = s.FallbackProvider
					fallbackUsed = true
					lastError = err.Error()
					continue
				}
				slog.Error("LLM call failed after retries", "conversation", convKey, "retries", maxRetries, "raw_error", err.Error())
				return "", fmt.Errorf("지금 답변을 만들지 못했어요. 잠시 후 다시 한 번 말씀해 주시겠어요?")
			}

			code := normalizeGeneratedCode(resp.Content)
			slog.Info("code generated",
				"phase", core.PhaseGenerate,
				"conversation", convKey,
				"attempt", attempt,
				"code_len", len(code),
				"code_preview", truncate(code, 500),
			)

			// Build sandbox context
			jsContext := map[string]any{
				"event":           json.RawMessage(event.Payload),
				"event_type":      string(event.Type),
				"conversation_id": convKey,
			}

			// Execute in sandbox with skill resolver
			var resolver sandbox.SkillResolver
			if s.Config.AutonomyLevel != core.AutonomyReadonly {
				resolver = func(ctx context.Context, call core.SkillCall) (string, error) {
					return resolveSkillCall(ctx, call, s, onPermission)
				}
			}

			execResult, err := s.Sandbox.ExecuteWithResolverOpts(ctx, code, jsContext, resolver, s.sandboxOptions())
			if err != nil {
				return "", fmt.Errorf("sandbox execute: %w", err)
			}
			turnToolTraces = appendTurnToolTraces(turnToolTraces, execResult.ToolTraces)

			// Runner.observe() — re-invoke LLM with observations
			if execResult.Observe {
				observations = execResult.Observations
				if observeRound < maxObserveRounds {
					continue observeLoop // → next observe round
				}
				// Max rounds reached — use whatever output we have
				slog.Warn("max observe rounds reached", "round", observeRound)
				output := execResult.Output
				if output == "" {
					output = "(max observation rounds reached)"
				}
				assistantTurn := core.ConversationTurn{
					ConversationID: convKey,
					Role:           core.RoleAssistant,
					Content:        output,
					Code:           code,
					ToolTraces:     turnToolTraces,
					Channel:        meta.Channel,
					ChannelUserID:  meta.ChannelUserID,
					ChatID:         meta.ChatID,
					Timestamp:      core.NowTimestamp(),
				}
				state.Turns = append(state.Turns, assistantTurn)
				_ = s.Store.AddConversationTurn(&assistantTurn)
				_ = s.Store.SaveConversationState(state)
				s.recordExecution(eventText, output, resp, loopStart, attempt, true)
				return output, nil
			}

			if execResult.Success {
				output := execResult.Output
				if override := staffToolOverrideOutput(s.BaseDir, convKey, execResult.SkillCalls); override != "" {
					output = override
				}
				if output == "" {
					output = "응답이 비어 있어요. 질문을 다시 한 번 말씀해 주시겠어요?"
				}

				slog.Info("execution success",
					"phase", core.PhaseFinish,
					"conversation", convKey,
					"output_len", len(output),
					"output_preview", truncate(output, 300),
					"skill_calls", len(execResult.SkillCalls),
				)

				// Save assistant turn
				assistantTurn := core.ConversationTurn{
					ConversationID: convKey,
					Role:           core.RoleAssistant,
					Content:        output,
					Code:           code,
					Result:         FormatExecResult(execResult),
					ToolTraces:     turnToolTraces,
					Channel:        meta.Channel,
					ChannelUserID:  meta.ChannelUserID,
					ChatID:         meta.ChatID,
					Timestamp:      core.NowTimestamp(),
				}
				state.Turns = append(state.Turns, assistantTurn)
				if err := s.Store.AddConversationTurn(&assistantTurn); err != nil {
					slog.Warn("failed to save assistant turn", "conversation", convKey, "error", err)
				}
				if err := s.Store.SaveConversationState(state); err != nil {
					slog.Warn("failed to save conversation state", "conversation", convKey, "error", err)
				}

				// Record execution metrics.
				s.recordExecution(eventText, output, resp, loopStart, attempt, true)

				// Cache invalidation — successful legacy-LLM turn
				// consumed (or rejected) the cached raw skill output.
				// Leaving the cache populated risks the next-turn
				// augmentation block contradicting the assistant's
				// transformed reply already in history (2026-04-28
				// transcript T5a freeze). Cache is re-filled when the
				// next deterministic skill dispatch records output.
				if s.Pipeline != nil {
					s.Pipeline.ClearSkillOutput()
					if pending, ok := detectPendingClarification(eventText, output); ok {
						s.Pipeline.RecordPendingClarification(pending)
					} else {
						s.Pipeline.ClearPendingClarification()
					}
				}

				return output, nil
			}

			// Execution failed — retry
			errMsg := execResult.Error
			if errMsg == "" {
				errMsg = "unknown error"
			}
			slog.Warn("execution failed",
				"phase", core.PhaseRetry,
				"conversation", convKey,
				"attempt", attempt,
				"error", errMsg,
			)
			lastError = errMsg
		}

		break // retry exhausted, exit observe loop
	}

	// All retries exhausted
	errMsg := lastError
	if errMsg == "" {
		errMsg = "unknown error"
	}
	slog.Info("retries exhausted",
		"phase", core.PhaseFinish,
		"conversation", convKey,
		"raw_error", errMsg,
	)

	assistantTurn := core.ConversationTurn{
		ConversationID: convKey,
		Role:           core.RoleAssistant,
		Content:        fmt.Sprintf("Error after %d retries: %s", maxRetries, errMsg),
		ToolTraces:     turnToolTraces,
		Channel:        meta.Channel,
		ChannelUserID:  meta.ChannelUserID,
		ChatID:         meta.ChatID,
		Timestamp:      core.NowTimestamp(),
	}
	state.Turns = append(state.Turns, assistantTurn)
	if err := s.Store.AddConversationTurn(&assistantTurn); err != nil {
		slog.Warn("failed to save error turn", "conversation", convKey, "error", err)
	}
	if err := s.Store.SaveConversationState(state); err != nil {
		slog.Warn("failed to save conversation state after failure", "conversation", convKey, "error", err)
	}

	s.recordExecution(eventText, errMsg, nil, loopStart, maxRetries, false)

	// User-facing fallback: the raw error (SyntaxError, undefined ident,
	// etc.) is internal noise that doesn't help the user act. Log captures
	// the technical detail; the chat surface stays in the assistant's
	// voice.
	return "", fmt.Errorf("지금 답변을 만들지 못했어요. 잠시 후 다시 한 번 말씀해 주시겠어요?")
}

func appendTurnToolTraces(dst []core.ToolTrace, src []core.ToolTrace) []core.ToolTrace {
	for _, trace := range src {
		if trace.ID == "" || toolTraceIDExists(dst, trace.ID) {
			trace.ID = fmt.Sprintf("skill_call_%d", len(dst)+1)
		}
		dst = append(dst, trace)
	}
	return dst
}

func toolTraceIDExists(traces []core.ToolTrace, id string) bool {
	for _, trace := range traces {
		if trace.ID == id {
			return true
		}
	}
	return false
}

func (s *Session) recordExecution(input, output string, resp *llm.Response, start time.Time, retries int, success bool) {
	summary := output
	if len(summary) > 200 {
		summary = summary[:200]
	}
	usageJSON := ""
	if resp != nil && resp.Usage != nil {
		if data, err := json.Marshal(resp.Usage); err == nil {
			usageJSON = string(data)
		}
	}
	if err := s.Store.RecordExecution(&store.ExecutionRecord{
		SkillID:       "chat",
		SkillName:     "chat",
		StartedAt:     start.UTC().Format("2006-01-02T15:04:05Z"),
		FinishedAt:    time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		DurationMs:    time.Since(start).Milliseconds(),
		InputParams:   input,
		ResultSummary: summary,
		Success:       success,
		RetryCount:    retries,
		UsageJSON:     usageJSON,
	}); err != nil {
		slog.Warn("failed to record execution", "skill_id", "chat", "error", err)
	}
}

func (s *Session) compactionForAttempt(attempt int) CompactionConfig {
	if !s.Config.Features.ContextCompaction {
		return CompactionConfig{RecentWindow: 20, MiddleWindow: 0, TruncateLen: 100}
	}
	if !s.Config.Features.ProgressiveRetry {
		return DefaultCompaction()
	}
	return CompactionForAttempt(attempt)
}

// resolveProvider returns a Provider for the given model name.
// Only named models from config ([[models]]) are accepted; arbitrary model IDs
// are rejected to prevent unintended API key leakage to unknown models.
// Falls back to the session's default provider when the model is unknown or invalid.
func (s *Session) resolveProvider(model string) llm.Provider {
	if model == "" {
		return s.Provider
	}
	mc := s.Config.FindModel(model)
	if mc == nil {
		slog.Warn("resolveProvider: model not found in config, using default", "model", model)
		return s.Provider
	}
	p, err := llm.NewProviderFromModelConfig(*mc)
	if err != nil {
		slog.Warn("resolveProvider: failed to create provider for named model", "model", model, "error", err)
		return s.Provider
	}
	return NewUsageRecordingProvider(p, s.Store, mc.Provider)
}

// ResolveStaffName determines which staff member to use for this request.
// Priority: mentionOverride > session override > channel binding > default.
func ResolveStaffName(
	config *core.Config,
	channelType string,
	conversationID string,
	mentionOverride string,
	st *store.Store,
	baseDir string,
) string {
	// 1. @mention override (highest priority).
	if mentionOverride != "" {
		return mentionOverride
	}

	base, _ := core.ResolveBaseDir(baseDir)

	// 2. Conversation staff from Staff.switch or /staff use.
	if st != nil {
		if val, ok, err := st.ConversationStaff(); err == nil && ok && val != "" && core.StaffHasSoul(base, val) {
			return val
		}
	}

	// 3. Channel binding from config.
	for _, sc := range config.Staff {
		for _, ch := range sc.Channels {
			if ch == channelType && core.StaffHasSoul(base, sc.ID) {
				return sc.ID
			}
		}
	}

	// 4. Default staff.
	if config.DefaultStaff != "" {
		return config.DefaultStaff
	}
	return "default"
}

// loadStaffForPrompt loads staff from disk and enriches it with config nick.
// baseDir is the account's base directory; falls back to ConfigDir() if empty.
func loadStaffForPrompt(staffID string, config *core.Config, baseDir string) *core.Staff {
	base, err := core.ResolveBaseDir(baseDir)
	if err != nil {
		slog.Warn("failed to get config dir for staff", "error", err)
		return &core.Staff{ID: staffID, Soul: core.Presets["default-assistant"].Soul}
	}
	staff, err := core.LoadStaff(base, staffID)
	if err != nil {
		slog.Warn("failed to load staff", "id", staffID, "error", err)
		return &core.Staff{ID: staffID, Soul: core.Presets["default-assistant"].Soul}
	}
	// Enrich with nick from config.
	for _, sc := range config.Staff {
		if sc.ID == staffID {
			staff.Nick = sc.Nick
			break
		}
	}
	return staff
}

type conversationTurnMetadata struct {
	Channel       string
	ChannelUserID string
	ChatID        string
	MessageID     string
}

func conversationKey(s *Session) string {
	if s != nil && s.AccountID != "" {
		return s.AccountID
	}
	return "account"
}

func (s *Session) loadConversationStateForRun(convKey string) (*core.ConversationState, error) {
	if s == nil || s.Store == nil {
		return nil, nil
	}
	if _, ok, err := s.Store.ConversationScope(convKey); err != nil {
		return nil, err
	} else if ok {
		return s.Store.LoadConversationStateForChat(convKey)
	}
	if _, ok, err := s.Store.Conversation(convKey); err != nil {
		return nil, err
	} else if ok {
		return s.Store.LoadConversationStateForChat(convKey)
	}
	if strings.HasPrefix(convKey, "general:") {
		scopeID := strings.TrimPrefix(convKey, "general:")
		if err := s.Store.EnsureConversation(convKey, "general", scopeID); err != nil {
			return nil, err
		}
		return s.Store.LoadConversationStateForChat(convKey)
	}
	return s.Store.LoadConversationStateForChat(store.DefaultConversationID)
}

func conversationKeyForEvent(s *Session, event *core.Event) string {
	if s == nil || s.Store == nil {
		return conversationKey(s)
	}
	if event == nil {
		return store.DefaultConversationID
	}
	payload, err := event.ParsePayload()
	if err != nil {
		return store.DefaultConversationID
	}
	for _, candidate := range []string{payload.ConversationID, payload.SessionID, payload.ChatID} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		ok, err := conversationKeyExists(s, candidate)
		if err == nil && ok {
			return candidate
		}
	}
	if event.AccountID != "" {
		if derived := sourceConversationKey(event.Type, payload); derived != "" {
			return derived
		}
	}
	return store.DefaultConversationID
}

func conversationKeyExists(s *Session, conversationID string) (bool, error) {
	if _, ok, err := s.Store.ConversationScope(conversationID); err != nil {
		return false, err
	} else if ok {
		return true, nil
	}
	_, ok, err := s.Store.Conversation(conversationID)
	return ok, err
}

func sourceConversationKey(eventType core.EventType, payload core.ChatPayload) string {
	source := conversationKeyPart(string(eventType))
	if source == "" {
		return ""
	}
	stableID := strings.TrimSpace(payload.ChatID)
	switch eventType {
	case core.EventKakaoTalk, core.EventWebChat, core.EventDesktop:
		stableID = firstNonEmptyConversationValue(payload.SessionID, payload.ChatID)
	default:
		stableID = firstNonEmptyConversationValue(payload.ChatID, payload.SessionID)
	}
	if stableID == "" || stableID == "api" || stableID == "scheduler" {
		return ""
	}
	part := conversationKeyPart(stableID)
	if part == "" {
		return ""
	}
	return "general:" + source + ":" + part
}

func firstNonEmptyConversationValue(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func conversationKeyPart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '.'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		sum := sha1.Sum([]byte(value))
		return hex.EncodeToString(sum[:])[:12]
	}
	if len(out) > 72 {
		sum := sha1.Sum([]byte(value))
		out = out[:56] + "-" + hex.EncodeToString(sum[:])[:12]
	}
	return out
}

func conversationTurnSource(event *core.Event) conversationTurnMetadata {
	meta := conversationTurnMetadata{Channel: event.Type.ChannelName()}
	payload, err := event.ParsePayload()
	if err != nil {
		meta.ChannelUserID = "unknown"
		return meta
	}
	meta.ChatID = payload.ChatID
	meta.MessageID = payload.ReplyToMessageID
	if payload.SessionID != "" {
		meta.ChannelUserID = payload.SessionID
	} else {
		meta.ChannelUserID = payload.ChatID
	}
	if meta.ChannelUserID == "" {
		meta.ChannelUserID = "unknown"
	}
	return meta
}
