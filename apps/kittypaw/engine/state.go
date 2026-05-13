package engine

import (
	"sync"
	"time"

	"github.com/jinto/kittypaw/core"
)

// PipelineState carries multi-turn state that the deterministic-branch
// pipeline needs to make routing decisions without consulting the LLM.
//
// Today this is just the most recent Skill.search results — used by the
// classifier to disambiguate a bare "네" between "yes, install" and
// other affirmatives, and by InstallConsentBranch to recall the exact
// skill id without LLM hallucination. Future state (PendingClarification,
// recent tool calls, etc.) lands here.
//
// One PipelineState per AccountRuntime — same isolation boundary as the rest
// of the account. All access is mutex-guarded; the AccountRuntime.Run loop is
// single-goroutine per event but server-side reload could race on init.
type PipelineState struct {
	mu                     sync.Mutex
	lastSkillSearchResults []core.RegistryEntry
	lastSearchAt           time.Time
	pendingClarification   PendingClarification
	pendingClarificationAt time.Time
	pendingSkillRunID      string
	pendingSkillRunParams  map[string]any
	pendingSkillRunAt      time.Time
	pendingPreference      PendingPreferenceConfirmation
	pendingPreferenceAt    time.Time

	// lastSkillOutput is the raw user-facing output from the most recent
	// deterministic skill execution (InstallConsentBranch +
	// RunInstalledSkillBranch). Used by runAgentLoop to augment the
	// system prompt when a short follow-up arrives — the LLM's "ignore
	// history" prior is observably stronger than its "use history"
	// prior, so we re-surface the data inside the system message
	// instead of relying on the conversation transcript alone.
	lastSkillOutput        string
	lastSkillOutputSkillID string
	lastSkillOutputAt      time.Time
}

// skillSearchResultsTTL is how long an unused search result hangs
// around before it stops counting as "the most recent suggestion".
// 5 minutes covers a normal think-then-reply pause; longer windows
// risk pairing a stale offer with an unrelated later "네".
const skillSearchResultsTTL = 5 * time.Minute

// skillOutputTTL is how long a skill's raw output stays available for
// cross-turn augmentation. Same 5 min budget as skillSearchResultsTTL
// — a longer window risks pairing a stale rate table with an unrelated
// later "계산해줘".
const skillOutputTTL = 5 * time.Minute

// clarificationTTL is how long a yes/no clarification can be answered with a
// short affirmative such as "ㅇㅇ" before it becomes too stale to trust.
const clarificationTTL = 5 * time.Minute

type PendingClarification struct {
	Kind  string
	Query string
}

type PendingPreferenceConfirmation struct {
	Key   string
	Value string
}

// NewPipelineState returns an empty pipeline state.
func NewPipelineState() *PipelineState {
	return &PipelineState{}
}

// RecordSkillSearch caches the entries returned by the most recent
// Skill.search call. Called from executeSkillSearch — every search,
// not just the ones that lead to an install offer (the classifier
// uses the *presence* of recent results plus a consent-shaped reply
// to gate routing; see classifyIntent).
func (ps *PipelineState) RecordSkillSearch(entries []core.RegistryEntry) {
	if ps == nil {
		return
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.lastSkillSearchResults = entries
	ps.lastSearchAt = time.Now()
}

// RecentSkillSearch returns the cached entries if they were recorded
// within skillSearchResultsTTL, or nil otherwise. The returned slice
// is the live cache — callers should treat it as read-only.
func (ps *PipelineState) RecentSkillSearch() []core.RegistryEntry {
	if ps == nil {
		return nil
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if time.Since(ps.lastSearchAt) > skillSearchResultsTTL {
		return nil
	}
	return ps.lastSkillSearchResults
}

// ClearSkillSearch drops the cached search results. Called after a
// successful install so an unrelated later "네" doesn't re-trigger
// install consent against the stale offer.
func (ps *PipelineState) ClearSkillSearch() {
	if ps == nil {
		return
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.lastSkillSearchResults = nil
	ps.pendingSkillRunID = ""
	ps.pendingSkillRunParams = nil
	ps.pendingSkillRunAt = time.Time{}
}

func (ps *PipelineState) RecordPendingSkillRun(skillID string, params map[string]any) {
	if ps == nil || skillID == "" || len(params) == 0 {
		return
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.pendingSkillRunID = skillID
	ps.pendingSkillRunParams = cloneParams(params)
	ps.pendingSkillRunAt = time.Now()
}

func (ps *PipelineState) RecentPendingSkillRun(skillID string) (map[string]any, bool) {
	if ps == nil || skillID == "" {
		return nil, false
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.pendingSkillRunID != skillID || time.Since(ps.pendingSkillRunAt) > skillSearchResultsTTL {
		return nil, false
	}
	return cloneParams(ps.pendingSkillRunParams), len(ps.pendingSkillRunParams) > 0
}

func cloneParams(params map[string]any) map[string]any {
	if len(params) == 0 {
		return nil
	}
	out := make(map[string]any, len(params))
	for k, v := range params {
		if nested, ok := v.(map[string]any); ok {
			out[k] = cloneParams(nested)
			continue
		}
		out[k] = v
	}
	return out
}

func (ps *PipelineState) RecordPendingClarification(p PendingClarification) {
	if ps == nil || p.Kind == "" {
		return
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.pendingClarification = p
	ps.pendingClarificationAt = time.Now()
}

func (ps *PipelineState) RecentPendingClarification() (PendingClarification, bool) {
	if ps == nil {
		return PendingClarification{}, false
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.pendingClarification.Kind == "" || time.Since(ps.pendingClarificationAt) > clarificationTTL {
		return PendingClarification{}, false
	}
	return ps.pendingClarification, true
}

func (ps *PipelineState) ClearPendingClarification() {
	if ps == nil {
		return
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.pendingClarification = PendingClarification{}
}

func (ps *PipelineState) RecordPendingPreferenceConfirmation(p PendingPreferenceConfirmation) {
	if ps == nil || p.Key == "" || p.Value == "" {
		return
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.pendingPreference = p
	ps.pendingPreferenceAt = time.Now()
}

func (ps *PipelineState) RecentPendingPreferenceConfirmation() (PendingPreferenceConfirmation, bool) {
	if ps == nil {
		return PendingPreferenceConfirmation{}, false
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.pendingPreference.Key == "" || time.Since(ps.pendingPreferenceAt) > clarificationTTL {
		return PendingPreferenceConfirmation{}, false
	}
	return ps.pendingPreference, true
}

func (ps *PipelineState) ClearPendingPreferenceConfirmation() {
	if ps == nil {
		return
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.pendingPreference = PendingPreferenceConfirmation{}
	ps.pendingPreferenceAt = time.Time{}
}

// RecordSkillOutput stores the raw user-facing output from a
// deterministic skill execution. Called by InstallConsentBranch and
// RunInstalledSkillBranch right before they return, so the next
// short follow-up turn can augment its system prompt with the data
// the user is most likely referencing.
func (ps *PipelineState) RecordSkillOutput(output string) {
	ps.RecordSkillOutputForSkill("", output)
}

func (ps *PipelineState) RecordSkillOutputForSkill(skillID, output string) {
	if ps == nil || output == "" {
		return
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.lastSkillOutput = output
	ps.lastSkillOutputSkillID = skillID
	ps.lastSkillOutputAt = time.Now()
}

// RecentSkillOutput returns the cached skill output if recorded
// within skillOutputTTL, or "" otherwise. Cheap to call on every
// turn — caller decides whether to augment.
func (ps *PipelineState) RecentSkillOutput() string {
	if ps == nil {
		return ""
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if time.Since(ps.lastSkillOutputAt) > skillOutputTTL {
		return ""
	}
	return ps.lastSkillOutput
}

func (ps *PipelineState) RecentSkillOutputSkillID() string {
	if ps == nil {
		return ""
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if time.Since(ps.lastSkillOutputAt) > skillOutputTTL {
		return ""
	}
	return ps.lastSkillOutputSkillID
}

// ClearSkillOutput drops the cached skill output. Called by
// runAgentLoop after a successful legacy-LLM turn so the next short
// follow-up does not get a stale augmentation block. Without this,
// the cache could still carry a 3-turn-old USD-base raw rate while
// history's most-recent assistant turn already shows the KRW-base
// transform — the conflicting signals freeze the LLM (2026-04-28
// transcript T5a "지금 답변을 만들지 못했어요" regression).
func (ps *PipelineState) ClearSkillOutput() {
	if ps == nil {
		return
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.lastSkillOutput = ""
	ps.lastSkillOutputSkillID = ""
}
