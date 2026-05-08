package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/llm"
)

// IntentKind classifies a user message into a deterministic branch or
// the legacy LLM runner loop. Each non-fallback kind is owned by a single
// Branch implementation. Adding a new behavioral case becomes "add a
// constant + classifier rule + Branch", not "grow the system prompt".
type IntentKind string

const (
	IntentChitchat               IntentKind = "chitchat"
	IntentBrowse                 IntentKind = "browse"
	IntentConfirmClarification   IntentKind = "confirm_clarification"
	IntentExchangeRateLookup     IntentKind = "exchange_rate_lookup"
	IntentWeatherNowLookup       IntentKind = "weather_now_lookup"
	IntentInstallConsentReply    IntentKind = "install_consent_reply"
	IntentPreferenceConfirmation IntentKind = "preference_confirmation"
	IntentRunInstalledSkill      IntentKind = "run_installed_skill"
	IntentModifierFollowup       IntentKind = "modifier_followup"
	IntentAmbiguousFollowup      IntentKind = "ambiguous_followup"
	IntentStaffCreateRequest     IntentKind = "staff_create_request"
	IntentStaffCreateOptIn       IntentKind = "staff_create_opt_in"
	IntentStaffDraftApprove      IntentKind = "staff_draft_approve"
	IntentStaffDraftCancel       IntentKind = "staff_draft_cancel"
	IntentStaffPostCreateSwitch  IntentKind = "staff_post_create_switch"
	IntentLegacyFallback         IntentKind = "legacy_fallback"
)

// Intent is the classifier's output. Params carry branch-specific state
// (e.g. the chitchat trigger phrase, the suggested skill id from the
// previous turn). Confidence is reserved for future LLM-fallback
// classifiers — rule-first matches are 1.0.
type Intent struct {
	Kind       IntentKind
	Params     map[string]any
	Confidence float64
}

// Branch handles one intent kind end-to-end. Implementations must be
// safe to call from any account Session without sharing state across
// accounts — branch-local state lives on PipelineState (per-Session).
type Branch interface {
	Execute(ctx context.Context, sess *Session, event core.Event, intent Intent) (string, error)
}

// classifyIntent runs the rule-first classifier. Phase 1-3 cover
// chitchat / browse / install-consent-reply. Everything else falls
// back to the legacy LLM runner loop. Phase 4 will add LLM-fallback
// classification for ambiguous queries (clarify trigger).
//
// install-consent-reply is the only state-aware rule today: it fires
// only when (a) a recent Skill.search exists in PipelineState (i.e.
// the legacy LLM path just made an offer) AND (b) the reply looks like
// consent. This keeps a bare "네" off the consent branch when there's
// no offer to consent to.
func classifyIntent(text string, state *PipelineState, sess *Session) Intent {
	t := strings.TrimSpace(text)
	if t == "" {
		return Intent{Kind: IntentLegacyFallback}
	}
	if sess != nil && sess.Store != nil {
		convKey := conversationKey(sess)
		if staffID, ok, _ := loadPendingStaffSwitch(sess.Store, convKey); ok && staffID != "" && (isStaffAffirmative(t) || isBareNegative(t) || isStaffDraftCancel(t)) {
			return Intent{
				Kind: IntentStaffPostCreateSwitch,
				Params: map[string]any{
					"staff_id": staffID,
					"accept":   isStaffAffirmative(t),
				},
				Confidence: 1.0,
			}
		}
		if _, ok, _ := loadPendingStaffDraft(sess.BaseDir, convKey); ok {
			if role, ok := staffCreateRoleFromText(t); ok {
				return Intent{
					Kind: IntentStaffCreateRequest,
					Params: map[string]any{
						"role": role,
					},
					Confidence: 1.0,
				}
			}
			if isStaffDraftCancel(t) {
				return Intent{Kind: IntentStaffDraftCancel, Confidence: 1.0}
			}
			if isStaffDraftApproval(t) {
				return Intent{Kind: IntentStaffDraftApprove, Confidence: 1.0}
			}
		}
		if role, ok, _ := loadPendingStaffOffer(sess.Store, convKey); ok && role != "" && isStaffAffirmative(t) {
			return Intent{
				Kind: IntentStaffCreateOptIn,
				Params: map[string]any{
					"role": role,
				},
				Confidence: 1.0,
			}
		}
	}
	if role, ok := staffCreateRoleFromText(t); ok {
		return Intent{
			Kind: IntentStaffCreateRequest,
			Params: map[string]any{
				"role": role,
			},
			Confidence: 1.0,
		}
	}
	if isChitchat(t) {
		return Intent{Kind: IntentChitchat, Confidence: 1.0}
	}
	if isBrowse(t) {
		return Intent{Kind: IntentBrowse, Confidence: 1.0}
	}
	if state != nil {
		if pending, ok := state.RecentPendingClarification(); ok && isBareAffirmative(t) {
			return Intent{
				Kind: IntentConfirmClarification,
				Params: map[string]any{
					"kind":  pending.Kind,
					"query": pending.Query,
				},
				Confidence: 1.0,
			}
		}
		if pending, ok := state.RecentPendingClarification(); ok && pending.Kind == "weather_now_location" && looksLikeWeatherLocationReply(t) {
			return Intent{
				Kind: IntentConfirmClarification,
				Params: map[string]any{
					"kind":  pending.Kind,
					"query": pending.Query,
				},
				Confidence: 1.0,
			}
		}
		recentSkills := state.RecentSkillSearch()
		if len(recentSkills) > 0 {
			if selected, ok := selectRecentSkillCandidate(t, recentSkills); ok {
				return Intent{
					Kind: IntentInstallConsentReply,
					Params: map[string]any{
						"skill_id": selected.ID,
					},
					Confidence: 1.0,
				}
			}
			if isInstallConsent(t) {
				return Intent{Kind: IntentInstallConsentReply, Confidence: 1.0}
			}
		}
	}
	if pending, ok := loadPendingPreferenceConfirmation(sess); ok && (isBareAffirmative(t) || isBareNegative(t)) {
		return Intent{
			Kind: IntentPreferenceConfirmation,
			Params: map[string]any{
				"key": pending.Key,
			},
			Confidence: 1.0,
		}
	}
	// Modifier follow-up dispatch: short modifier-shaped query +
	// recent skill output cached → mediation tool-use loop. The
	// mediation prompt sees the raw output as load-bearing context
	// + Code.exec available as a callable tool, routing around the
	// legacy LLM's "ignore-history → re-search" prior. Phase 11 of
	// the level3 plan; closes the 2026-04-28 transcript T4 fail
	// ("아니 원화를 기준으로 하면?" → web search 잔존).
	if state != nil && queryHasModifier(t) && state.RecentSkillOutput() != "" && runeCount(t) <= followupQueryRuneCap {
		return Intent{
			Kind: IntentModifierFollowup,
			Params: map[string]any{
				"raw_output": state.RecentSkillOutput(),
				"skill_id":   state.RecentSkillOutputSkillID(),
			},
			Confidence: 1.0,
		}
	}
	if state != nil && state.RecentSkillOutputSkillID() == "weather-now" && looksLikeWeatherLocationFollowup(t) {
		return Intent{
			Kind: IntentAmbiguousFollowup,
			Params: map[string]any{
				"raw_output": state.RecentSkillOutput(),
				"skill_id":   state.RecentSkillOutputSkillID(),
			},
			Confidence: 1.0,
		}
	}
	if isExchangeRateLookupQuery(t) {
		return Intent{Kind: IntentExchangeRateLookup, Confidence: 1.0}
	}
	if isWeatherNowLookupQuery(t) {
		return Intent{Kind: IntentWeatherNowLookup, Confidence: 1.0}
	}
	// Installed-skill dispatch: when the query keyword appears in an
	// already-installed package's name, run that skill directly. This
	// closes the regression where the LLM, despite the
	// "PRIORITY: installed → Skill.run" prompt rule, still went out
	// to Web.search + suggested re-installing an already-present
	// skill (turn 5 of the 2026-04-27 transcript).
	if pkg := matchInstalledSkill(t, sess); pkg != nil {
		return Intent{
			Kind: IntentRunInstalledSkill,
			Params: map[string]any{
				"skill_id":   pkg.Meta.ID,
				"skill_name": pkg.Meta.Name,
			},
			Confidence: 1.0,
		}
	}
	return Intent{Kind: IntentLegacyFallback}
}

// matchInstalledSkill returns an installed package whose name keywords
// appear in the user query. Single-word match (e.g. "환율 조회" -> "환율")
// is the common case; multi-skill ambiguity ("주식" matches both
// "주식 알림" and "주가 조회") falls through to legacy LLM since picking
// without context is the wrong call.
func matchInstalledSkill(text string, sess *Session) *core.SkillPackage {
	// 1-char query is too noisy for substring matching against installed
	// package metadata — let the legacy LLM clarify it instead.
	if runeCount(text) < 2 {
		return nil
	}
	// Modifier-shaped queries ("원화로 환율", "간단히 환율", "다시")
	// signal a *transform of prior data*, not a fresh skill run.
	// Defer to the legacy LLM so the cross-turn augmentation block
	// (Phase 10) can reframe the cached raw output. RunInstalled's
	// single-shot mediation cannot reliably do active arithmetic
	// (USD-base → KRW-base), so routing modifier queries through it
	// produces the "어설픈" 2026-04-28 transcript ("원화로 환율" →
	// USD-base paraphrase).
	if queryHasModifier(text) {
		return nil
	}
	if sess == nil || sess.PackageManager == nil {
		return nil
	}
	packages, err := sess.PackageManager.ListInstalled()
	if err != nil || len(packages) == 0 {
		return nil
	}
	lowered := strings.ToLower(text)
	type scoredPackage struct {
		pkg   core.SkillPackage
		score int
	}
	var matches []scoredPackage
	for _, pkg := range packages {
		score := installedSkillMatchScore(lowered, pkg)
		if score > 0 {
			matches = append(matches, scoredPackage{pkg: pkg, score: score})
		}
	}
	if len(matches) == 0 {
		return nil
	}
	best := matches[0]
	tie := false
	for _, m := range matches[1:] {
		if m.score > best.score {
			best = m
			tie = false
			continue
		}
		if m.score == best.score {
			tie = true
		}
	}
	if tie {
		// Ambiguous at the same relevance level — let the legacy LLM ask
		// or use richer context rather than picking by package order.
		return nil
	}
	return &best.pkg
}

func installedPackageByID(id string, sess *Session) *core.SkillPackage {
	if sess == nil || sess.PackageManager == nil {
		return nil
	}
	pkg, _, err := sess.PackageManager.LoadPackage(id)
	if err != nil {
		return nil
	}
	return pkg
}

func installedSkillMatchScore(loweredQuery string, pkg core.SkillPackage) int {
	if !pkgKeywordMatches(loweredQuery, pkg) {
		return 0
	}

	if isBriefingWeatherQuery(loweredQuery) && isScheduledWeatherPackage(pkg) {
		return 100
	}
	if isCurrentWeatherQuery(loweredQuery) {
		if isNowWeatherPackage(pkg) {
			return 100
		}
		if isScheduledWeatherPackage(pkg) {
			return 0
		}
	}

	return 10
}

func isCurrentWeatherQuery(loweredQuery string) bool {
	return containsAny(loweredQuery,
		"현재", "지금", "방금", "막 ", "비오", "비 오", "비와", "비 와",
		"몇 도", "기온", "날씨",
	)
}

func isBriefingWeatherQuery(loweredQuery string) bool {
	return containsAny(loweredQuery,
		"매일", "아침", "브리핑", "요약", "알림", "텔레그램", "보내",
	)
}

func isExchangeRateLookupQuery(loweredQuery string) bool {
	return containsAny(loweredQuery, "환율", "usd/krw", "usd krw", "달러 환율", "원달러")
}

func isWeatherNowLookupQuery(loweredQuery string) bool {
	if containsAny(loweredQuery, "내일", "모레", "주말", "다음", "오후", "저녁", "몇 시간", "1시간", "2시간", "3시간") {
		return false
	}
	return isCurrentWeatherQuery(loweredQuery) && !isBriefingWeatherQuery(loweredQuery)
}

func isNowWeatherPackage(pkg core.SkillPackage) bool {
	t := strings.ToLower(pkg.Meta.ID + " " + pkg.Meta.Name + " " + pkg.Meta.Description)
	return containsAny(t, "weather-now", "현재 날씨", "지금 시점", "즉답", "비 여부")
}

func isScheduledWeatherPackage(pkg core.SkillPackage) bool {
	t := strings.ToLower(pkg.Meta.ID + " " + pkg.Meta.Name + " " + pkg.Meta.Description)
	return pkg.Meta.Cron != "" ||
		containsAny(t, "weather-briefing", "매일", "아침", "브리핑", "알림", "텔레그램", "보내")
}

// queryHasModifier returns true when the user's query contains a
// generic Korean transform-modifier — a pattern that signals "do
// something with the prior data" rather than "fetch fresh data". The
// list is intentionally limited to *generic* transform shapes (unit
// conversion, base reframe, verbosity, repeat, substitution, explicit
// transform) so it does not overlap with raw-retrieval phrasing
// ("오늘 환율", "내일 날씨" — those are time-stamped fresh requests,
// not transforms of prior data).
//
// When this returns true, matchInstalledSkill defers to the legacy
// LLM so the Phase 10 cross-turn augmentation can apply. The
// hardcoded list is the smallest practical compromise — Korean
// morphology is too varied for a regex without false positives, and
// LLM-driven classification on every turn is over-engineered.
func queryHasModifier(query string) bool {
	lowered := strings.ToLower(query)
	modifiers := []string{
		// Unit / currency conversion
		"원화로", "원으로", "엔으로", "엔화로", "달러로", "유로로", "위안으로", "파운드로",
		// Base reframe ("X 기준으로", "기준의")
		"기준", "기준으로", "기준의",
		// Verbosity / format
		"간단히", "자세히", "짧게", "길게", "요약",
		// Repeat / explicit recompute
		"다시", "재계산",
		// Substitution / comparison
		"대신", "외에",
		// Explicit transform vocabulary
		"변환", "환산", "전환",
	}
	for _, m := range modifiers {
		if strings.Contains(lowered, m) {
			return true
		}
	}
	return false
}

// pkgKeywordMatches checks whether a query keyword appears in the
// package's name or description. Description is the Korean source
// since installed package names are often ASCII slugs (e.g.
// "exchange-rate") while descriptions carry "환율" / "주가" / "날씨"
// as natural keywords.
//
// A small stop-word list filters generic description tokens that
// would otherwise match every domain query (e.g. "조회", "확인").
func pkgKeywordMatches(loweredQuery string, pkg core.SkillPackage) bool {
	candidates := strings.ToLower(pkg.Meta.Name) + " " + strings.ToLower(pkg.Meta.Description)
	for _, raw := range strings.Fields(candidates) {
		word := strings.Trim(raw, ".,()[]{}:;!?-_/\"'")
		if runeCount(word) < 2 {
			continue
		}
		if pkgKeywordStopWord(word) {
			continue
		}
		// Bidirectional match — Korean attaches particles ("환율" + "을"
		// → "환율을") so the description token is often longer than the
		// query keyword. Both directions catch this without
		// overgenerating: query "환율" matches description token
		// "환율을", query "오늘 환율 어때" matches token "환율".
		if strings.Contains(loweredQuery, word) || strings.Contains(word, loweredQuery) {
			return true
		}
	}
	return false
}

// pkgKeywordStopWord skips description tokens that are too generic to
// signal an installed-skill match. The list intentionally stays short;
// extending it is cheap but each addition narrows the dispatch.
func pkgKeywordStopWord(w string) bool {
	switch w {
	case "조회", "확인", "알림", "정보", "데이터", "api", "free", "skill",
		"실시간", "현재", "오늘", "기준", "별개의", "별개",
		"및", "을", "를", "의", "에", "이", "가", "로", "과",
		"주요", "전용", "제공", "사용", "키", "불필요",
		"the", "and", "for", "with", "from", "into",
		"into.", "텔레그램으로", "발송합니다", "알려줍니다",
		"보내줍니다", "확인하고", "조회합니다", "관리합니다":
		return true
	}
	return false
}

// isInstallConsent matches the user's reply to a suffix offer. Two
// shapes:
//
//  1. Any phrase containing "설치" (설치해줘요/설치해주세요/설치할게요/...)
//     — strong signal regardless of length.
//  2. A short bare affirmative (네/응/그래/yes/ok/...) — only fires on
//     short replies because longer messages with the same prefix
//     ("네, 그런데 다른 거 알려줘") aren't consent.
func isInstallConsent(text string) bool {
	if strings.Contains(text, "설치") {
		return true
	}
	return isBareAffirmative(text)
}

func isBareAffirmative(text string) bool {
	if runeCount(text) > 8 {
		return false
	}
	lowered := strings.ToLower(text)
	// Strip a trailing punctuation set used in casual Korean replies.
	trimmed := strings.TrimRight(lowered, ".,!?~ ")
	bareAffirmatives := []string{
		"네", "네네", "넵", "응", "어", "그래", "그래요", "좋아", "좋아요",
		"ㅇ", "ㅇㅇ", "ㅇㅋ", "오케이", "예",
		"yes", "y", "ok", "okay", "sure", "yep", "yeah",
	}
	for _, a := range bareAffirmatives {
		if trimmed == a {
			return true
		}
	}
	return false
}

func isBareNegative(text string) bool {
	if runeCount(text) > 12 {
		return false
	}
	lowered := strings.ToLower(text)
	trimmed := strings.TrimRight(lowered, ".,!?~ ")
	bareNegatives := []string{
		"아니", "아니요", "아뇨", "ㄴㄴ", "싫어", "괜찮아", "괜찮아요",
		"no", "n", "nope", "nah",
	}
	for _, n := range bareNegatives {
		if trimmed == n {
			return true
		}
	}
	return false
}

func looksLikeWeatherLocationReply(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" || isChitchat(text) {
		return false
	}
	if isWeatherNowLookupQuery(text) {
		return true
	}
	return runeCount(text) <= 24
}

func looksLikeWeatherLocationFollowup(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" || runeCount(text) > 24 {
		return false
	}
	if isChitchat(text) || isExchangeRateLookupQuery(text) || isBrowse(text) {
		return false
	}
	loc := inferWeatherLocationFromText(text)
	if loc != "" {
		return true
	}
	normalized := normalizeLocationSlot(text)
	return looksLikePlainLocationSlot(normalized)
}

func detectPendingClarification(userText, assistantText string) (PendingClarification, bool) {
	userText = strings.TrimSpace(userText)
	assistantText = strings.TrimSpace(assistantText)
	if userText == "" || assistantText == "" {
		return PendingClarification{}, false
	}
	if !looksLikeClarificationQuestion(assistantText) {
		return PendingClarification{}, false
	}
	combined := strings.ToLower(userText + " " + assistantText)
	if containsAny(combined, "환율", "달러", "usd", "원화", "krw") {
		return PendingClarification{Kind: "exchange_rate", Query: userText}, true
	}
	if containsAny(combined, "날씨", "비오", "비 오", "비와", "비 와", "강수", "weather", "rain") {
		return PendingClarification{Kind: "weather_now", Query: userText}, true
	}
	return PendingClarification{}, false
}

func looksLikeClarificationQuestion(text string) bool {
	return containsAny(text,
		"맞으면", "맞나요", "맞아요", "말씀이세요", "말인가요",
		"찾아볼게요", "확인할까요", "조회할까요",
	)
}

func exchangeRateRegistryEntry() core.RegistryEntry {
	return core.RegistryEntry{
		ID:          "exchange-rate",
		Name:        "환율 조회",
		Version:     "1.0.0",
		Description: "키 없이 환율 표를 바로 조회합니다.",
		Author:      "KittyPaw Team",
	}
}

func weatherNowRegistryEntry() core.RegistryEntry {
	return core.RegistryEntry{
		ID:          "weather-now",
		Name:        "날씨 조회",
		Version:     "1.0.0",
		Description: "wttr.in으로 현재 날씨와 비 여부를 즉답합니다.",
		Author:      "KittyPaw Team",
	}
}

func selectRecentSkillCandidate(text string, entries []core.RegistryEntry) (core.RegistryEntry, bool) {
	q := strings.ToLower(strings.TrimSpace(text))
	if q == "" || len(entries) == 0 {
		return core.RegistryEntry{}, false
	}

	var best core.RegistryEntry
	bestScore := 0
	tie := false
	for _, entry := range entries {
		score := registryEntryIntentScore(q, entry)
		if score > bestScore {
			best = entry
			bestScore = score
			tie = false
			continue
		}
		if score == bestScore && score > 0 {
			tie = true
		}
	}
	if bestScore == 0 || tie {
		return core.RegistryEntry{}, false
	}
	return best, true
}

func registryEntryIntentScore(loweredQuery string, entry core.RegistryEntry) int {
	id := strings.ToLower(entry.ID)
	name := strings.ToLower(entry.Name)
	desc := strings.ToLower(entry.Description)

	switch {
	case name != "" && strings.Contains(loweredQuery, name):
		return 200
	case id != "" && strings.Contains(loweredQuery, id):
		return 190
	}

	score := 0
	if isBriefingWeatherQuery(loweredQuery) && isScheduledWeatherEntry(entry) {
		score += 100
	}
	if isCurrentWeatherQuery(loweredQuery) {
		if isNowWeatherEntry(entry) {
			score += 100
		} else if isScheduledWeatherEntry(entry) {
			score -= 100
		}
	}

	for _, raw := range strings.Fields(id + " " + name) {
		word := strings.Trim(raw, ".,()[]{}:;!?-_/\"'")
		if runeCount(word) >= 2 && strings.Contains(loweredQuery, word) {
			score += 20
		}
	}
	for _, raw := range strings.Fields(desc) {
		word := strings.Trim(raw, ".,()[]{}:;!?-_/\"'")
		if runeCount(word) >= 2 && !pkgKeywordStopWord(word) && strings.Contains(loweredQuery, word) {
			score += 5
		}
	}

	if score <= 0 {
		return 0
	}
	return score
}

func isNowWeatherEntry(entry core.RegistryEntry) bool {
	t := strings.ToLower(entry.ID + " " + entry.Name + " " + entry.Description)
	return containsAny(t, "weather-now", "현재 날씨", "지금 시점", "즉답", "비 여부")
}

func isScheduledWeatherEntry(entry core.RegistryEntry) bool {
	t := strings.ToLower(entry.ID + " " + entry.Name + " " + entry.Description)
	return containsAny(t, "weather-briefing", "매일", "아침", "브리핑", "알림", "텔레그램", "보내")
}

// isBrowse detects "show me what's available" queries — the user wants
// a registry overview, not a specific install or a general recommendation.
// Keep this intentionally narrow: general requests like "요즘 드라마 추천해줘"
// must fall through to the LLM so it can decide whether freshness/search is
// needed. Only explicit skill/function/tool-list phrasing belongs here.
func isBrowse(text string) bool {
	const browseMaxRunes = 30
	if runeCount(text) > browseMaxRunes {
		return false
	}
	lowered := strings.ToLower(text)
	if isSkillRemovalRequest(lowered) {
		return false
	}
	patterns := []string{
		"어떤 스킬", "무슨 스킬", "사용 가능한 스킬",
		"스킬 목록", "스킬 뭐", "스킬은 뭐", "스킬들",
		"스킬 추천", "추천 스킬",
		"어떤 기능", "무슨 기능", "사용 가능한 기능",
		"기능 목록", "기능 뭐", "할 수 있는 기능",
		"what skills", "available skills", "list skills",
		"what can you do", "available tools", "list tools",
		"browse skills",
	}
	for _, p := range patterns {
		if strings.Contains(lowered, p) {
			return true
		}
	}
	return false
}

func isSkillRemovalRequest(lowered string) bool {
	if !containsAny(lowered, "스킬", "skill", "package", "패키지") {
		return false
	}
	return containsAny(lowered,
		"지워", "지우", "삭제", "제거", "없애", "날려", "버려",
		"uninstall", "remove", "delete",
	)
}

// isChitchat detects short reactive utterances that don't carry a new
// request — "오 잘하네!", "고마워", "thanks", etc. The length cap keeps
// real questions ("이게 잘하는 건가요?") out of the chitchat branch.
func isChitchat(text string) bool {
	const chitchatMaxRunes = 25
	if runeCount(text) > chitchatMaxRunes {
		return false
	}
	lowered := strings.ToLower(text)
	patterns := []string{
		"잘하네", "잘하는", "잘해", "잘하시",
		"고마워", "고맙", "감사",
		"좋네", "좋아", "굿", "최고",
		"thanks", "thank you", "thx",
		"nice", "good job", "great",
		"멋져", "멋지", "굳",
	}
	for _, p := range patterns {
		if strings.Contains(lowered, p) {
			return true
		}
	}
	return false
}

func runeCount(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

// dispatchPipeline runs the rule-first classifier and, if the intent
// has a deterministic branch, executes it and returns (response, true).
// On (legacy_fallback OR branch error), it returns (_, false) so the
// caller falls through to the legacy LLM runner loop.
//
// Returning a bool instead of a sentinel error keeps the legacy path
// untouched — callers can wire this in with a single if-statement.
func dispatchPipeline(ctx context.Context, sess *Session, event core.Event, eventText string) (string, bool) {
	intent := classifyIntent(eventText, sess.Pipeline, sess)
	branch := getBranch(intent.Kind)
	if branch == nil {
		return "", false
	}
	resp, err := branch.Execute(ctx, sess, event, intent)
	if err != nil {
		return "", false
	}
	return resp, true
}

// getBranch returns the Branch implementation for an intent kind, or
// nil for legacy_fallback / unmapped kinds.
func getBranch(kind IntentKind) Branch {
	switch kind {
	case IntentChitchat:
		return &ChitchatBranch{}
	case IntentBrowse:
		return &BrowseBranch{}
	case IntentConfirmClarification:
		return &ConfirmClarificationBranch{}
	case IntentExchangeRateLookup:
		return &ExchangeRateLookupBranch{}
	case IntentWeatherNowLookup:
		return &WeatherNowLookupBranch{}
	case IntentInstallConsentReply:
		return &InstallConsentBranch{}
	case IntentPreferenceConfirmation:
		return &PreferenceConfirmationBranch{}
	case IntentRunInstalledSkill:
		return &RunInstalledSkillBranch{}
	case IntentModifierFollowup:
		return &ModifierFollowupBranch{}
	case IntentAmbiguousFollowup:
		return &AmbiguousFollowupBranch{}
	case IntentStaffCreateRequest:
		return &StaffCreateRequestBranch{}
	case IntentStaffCreateOptIn:
		return &StaffCreateOptInBranch{}
	case IntentStaffDraftApprove:
		return &StaffDraftApproveBranch{}
	case IntentStaffDraftCancel:
		return &StaffDraftCancelBranch{}
	case IntentStaffPostCreateSwitch:
		return &StaffPostCreateSwitchBranch{}
	}
	return nil
}

// ChitchatBranch returns a deterministic ack — no LLM call, no tool
// call. Reproduces the user-vision pattern (4) "친화 비서 staff identity": a
// short reactive utterance gets a short reactive reply, never re-runs
// the prior tool or re-emits the prior result.
type ChitchatBranch struct{}

func (b *ChitchatBranch) Execute(ctx context.Context, sess *Session, event core.Event, intent Intent) (string, error) {
	return "알겠습니다.", nil
}

type StaffCreateRequestBranch struct{}

func (b *StaffCreateRequestBranch) Execute(ctx context.Context, sess *Session, event core.Event, intent Intent) (string, error) {
	if sess == nil || sess.Store == nil {
		return "staff 생성을 위한 저장소가 준비되지 않았어요.", nil
	}
	if existing, ok, err := loadPendingStaffDraft(sess.BaseDir, conversationKey(sess)); err != nil {
		return "staff 초안을 확인하지 못했어요. 잠시 후 다시 시도해 주세요.", nil
	} else if ok {
		return formatPendingStaffDraftNotice(existing), nil
	}
	role := strings.TrimSpace(fmt.Sprint(intent.Params["role"]))
	if role == "" {
		role = strings.TrimSpace(FormatEvent(&event))
	}
	request := strings.TrimSpace(FormatEvent(&event))
	if request == "" {
		request = role
	}
	if err := savePendingStaffOffer(sess.Store, conversationKey(sess), request); err != nil {
		return "staff 생성 제안을 저장하지 못했어요. 잠시 후 다시 시도해 주세요.", nil
	}
	return "Staff 기능으로 새 역할을 만들까요?", nil
}

type StaffCreateOptInBranch struct{}

func (b *StaffCreateOptInBranch) Execute(ctx context.Context, sess *Session, event core.Event, intent Intent) (string, error) {
	if sess == nil || sess.Store == nil {
		return "staff 생성을 위한 저장소가 준비되지 않았어요.", nil
	}
	role := strings.TrimSpace(fmt.Sprint(intent.Params["role"]))
	if role == "" {
		var ok bool
		var err error
		role, ok, err = loadPendingStaffOffer(sess.Store, conversationKey(sess))
		if err != nil || !ok {
			return "진행할 staff 생성 요청을 찾지 못했어요.", nil
		}
	}
	draft, clarification, err := buildStaffDraftFromRequest(ctx, sess, role)
	if err != nil {
		return "staff 초안을 만들지 못했어요. 역할을 조금 더 구체적으로 말해 주세요.", nil
	}
	if clarification != "" {
		return clarification, nil
	}
	base, err := core.ResolveBaseDir(sess.BaseDir)
	if err != nil {
		return "staff 정보를 확인하지 못했어요. 잠시 후 다시 시도해 주세요.", nil
	}
	if core.StaffHasSoul(base, draft.ID) {
		_ = clearPendingStaffOffer(sess.Store, conversationKey(sess))
		return fmt.Sprintf("staff %q는 이미 존재합니다.", draft.ID), nil
	}
	if err := savePendingStaffDraft(sess.BaseDir, conversationKey(sess), draft); err != nil {
		return "staff 초안을 저장하지 못했어요. 잠시 후 다시 시도해 주세요.", nil
	}
	_ = clearPendingStaffOffer(sess.Store, conversationKey(sess))
	return formatStaffDraftPreview(draft), nil
}

type StaffDraftApproveBranch struct{}

func (b *StaffDraftApproveBranch) Execute(ctx context.Context, sess *Session, event core.Event, intent Intent) (string, error) {
	if sess == nil || sess.Store == nil {
		return "staff 생성을 위한 저장소가 준비되지 않았어요.", nil
	}
	draft, ok, err := loadPendingStaffDraft(sess.BaseDir, conversationKey(sess))
	if err != nil {
		return "staff 초안을 읽지 못했어요. 잠시 후 다시 시도해 주세요.", nil
	}
	if !ok {
		return "생성할 staff 초안이 없습니다.", nil
	}
	if err := commitStaffDraft(sess.BaseDir, draft); err != nil {
		return fmt.Sprintf("staff 생성 실패: %s", err), nil
	}
	_ = clearPendingStaffDraft(sess.BaseDir, conversationKey(sess))
	if err := savePendingStaffSwitch(sess.Store, conversationKey(sess), draft.ID); err != nil {
		return fmt.Sprintf("%s staff를 만들었어요.\n\n시스템 이름은 %s 입니다.", draft.DisplayName, draft.ID), nil
	}
	return fmt.Sprintf("%s staff를 만들었어요.\n\n시스템 이름은 %s 입니다.\n지금 이 대화에서 사용할까요?", draft.DisplayName, draft.ID), nil
}

type StaffDraftCancelBranch struct{}

func (b *StaffDraftCancelBranch) Execute(ctx context.Context, sess *Session, event core.Event, intent Intent) (string, error) {
	if sess == nil || sess.Store == nil {
		return "staff 초안을 위한 저장소가 준비되지 않았어요.", nil
	}
	_ = clearPendingStaffDraft(sess.BaseDir, conversationKey(sess))
	_ = clearPendingStaffOffer(sess.Store, conversationKey(sess))
	_ = clearPendingStaffSwitch(sess.Store, conversationKey(sess))
	return "staff 초안을 취소했습니다.", nil
}

type StaffPostCreateSwitchBranch struct{}

func (b *StaffPostCreateSwitchBranch) Execute(ctx context.Context, sess *Session, event core.Event, intent Intent) (string, error) {
	if sess == nil || sess.Store == nil {
		return "staff 전환을 위한 저장소가 준비되지 않았어요.", nil
	}
	staffID := strings.TrimSpace(fmt.Sprint(intent.Params["staff_id"]))
	accept, _ := intent.Params["accept"].(bool)
	if staffID == "" {
		return "전환할 staff를 찾지 못했어요.", nil
	}
	_ = clearPendingStaffSwitch(sess.Store, conversationKey(sess))
	if !accept {
		return "알겠습니다. 현재 staff를 유지합니다.", nil
	}
	if _, err := setConversationStaff(sess.BaseDir, sess.Store, staffID); err != nil {
		return fmt.Sprintf("staff 전환 실패: %s", err), nil
	}
	return fmt.Sprintf("이 대화에서 %s staff를 사용합니다.", staffID), nil
}

// BrowseBranch lists registry skills grouped by domain. No LLM call —
// the previous LLM-driven implementation produced the same shape via
// emergent grouping; this branch reproduces it deterministically.
//
// Reproduces user-vision pattern (2) "도구 부족 가시화": when the user
// asks "어떤 스킬?", they get the full registry surface, not a
// guess-and-suggest from one search keyword.
type BrowseBranch struct{}

func (b *BrowseBranch) Execute(ctx context.Context, sess *Session, event core.Event, intent Intent) (string, error) {
	rc, err := newRegistryClient(sess.Config)
	if err != nil {
		return "지금 스킬 레지스트리에 연결하지 못했어요. 잠시 후 다시 시도해 주세요.", nil
	}
	entries, err := rc.SearchEntries("")
	if err != nil {
		return "지금 스킬 목록을 가져오지 못했어요. 잠시 후 다시 시도해 주세요.", nil
	}
	if len(entries) == 0 {
		return "현재 등록된 스킬이 없어요.", nil
	}
	return formatBrowseResponse(entries), nil
}

// formatBrowseResponse groups entries into a small number of named
// categories using keyword inference on name + description. Hard-coded
// category mapping is a known antipattern (Phase 6 will revisit) but
// keeps Phase 2 within the "no LLM, no new state" boundary. New skills
// land under "기타" until the mapping is updated.
func formatBrowseResponse(entries []core.RegistryEntry) string {
	type bucket struct {
		name  string
		items []core.RegistryEntry
	}
	buckets := []*bucket{
		{name: "금융"}, {name: "날씨"}, {name: "뉴스"},
		{name: "환경"}, {name: "할일"}, {name: "기타"},
	}
	idx := map[string]*bucket{}
	for _, b := range buckets {
		idx[b.name] = b
	}
	for _, e := range entries {
		idx[categorize(e.Name, e.Description)].items = append(idx[categorize(e.Name, e.Description)].items, e)
	}
	var sb strings.Builder
	sb.WriteString("## 사용 가능한 스킬들 (")
	sb.WriteString(strconv.Itoa(len(entries)))
	sb.WriteString("개)\n")
	for _, b := range buckets {
		if len(b.items) == 0 {
			continue
		}
		sb.WriteString("\n### ")
		sb.WriteString(b.name)
		sb.WriteString("\n")
		for _, e := range b.items {
			sb.WriteString("• **")
			sb.WriteString(e.Name)
			sb.WriteString("** — ")
			sb.WriteString(e.Description)
			sb.WriteString("\n")
		}
	}
	sb.WriteString("\n관심 있는 스킬이 있으면 말씀해 주세요. 설치를 도와드릴게요.")
	return sb.String()
}

func categorize(name, desc string) string {
	t := strings.ToLower(name + " " + desc)
	switch {
	case containsAny(t, "환율", "주가", "주식", "exchange", "stock"):
		return "금융"
	case containsAny(t, "날씨", "weather"):
		return "날씨"
	case containsAny(t, "뉴스", "rss", "news"):
		return "뉴스"
	case containsAny(t, "미세먼지", "air", "환경"):
		return "환경"
	case containsAny(t, "리마인더", "remind", "todo", "할일"):
		return "할일"
	}
	return "기타"
}

type ExchangeRateLookupBranch struct{}

func (b *ExchangeRateLookupBranch) Execute(ctx context.Context, sess *Session, event core.Event, intent Intent) (string, error) {
	if sess == nil || sess.Pipeline == nil {
		return "", errBranchFallback
	}
	if pkg := matchInstalledSkill("환율", sess); pkg != nil {
		userText := FormatEvent(&event)
		params := exchangeRateParamsForText(userText)
		explicitParams := len(params) > 0
		if !explicitParams {
			if pref, ok := loadExchangeRateDisplayPreference(sess); ok {
				params = exchangeRatePreferenceParams(pref)
			}
		}
		rawJSON, _ := runSkillOrPackageWithParams(ctx, pkg.Meta.ID, sess, params)
		output := extractOutputField(rawJSON)
		if output == "" {
			return "", errBranchFallback
		}
		output = applyExchangeRateDisplayParams(output, params)
		sess.Pipeline.RecordSkillOutputForSkill(pkg.Meta.ID, output)
		if !explicitParams {
			output = maybeAppendExchangeRatePreferenceConfirmation(sess, output)
		}
		return output, nil
	}
	entry := exchangeRateRegistryEntry()
	sess.Pipeline.RecordSkillSearch([]core.RegistryEntry{entry})
	return formatExchangeRateLookupResponse(exchangeRateSearchResults(ctx, sess), entry), nil
}

type WeatherNowLookupBranch struct{}

func (b *WeatherNowLookupBranch) Execute(ctx context.Context, sess *Session, event core.Event, intent Intent) (string, error) {
	if sess == nil || sess.Pipeline == nil {
		return "", errBranchFallback
	}
	if pkg := installedPackageByID("weather-now", sess); pkg != nil {
		userText := FormatEvent(&event)
		params, msg := weatherNowParamsForIntentOrText(ctx, sess, intent, userText)
		if msg != "" {
			sess.Pipeline.RecordPendingClarification(PendingClarification{
				Kind:  "weather_now_location",
				Query: userText,
			})
			return msg, nil
		}
		rawJSON, _ := runSkillOrPackageWithParams(ctx, pkg.Meta.ID, sess, params)
		output := extractOutputField(rawJSON)
		if output == "" {
			return "", errBranchFallback
		}
		sess.Pipeline.RecordSkillOutputForSkill(pkg.Meta.ID, output)
		return mediateSkillOutput(ctx, sess, pkg.Meta.ID, userText, output), nil
	}
	entry := weatherNowRegistryEntry()
	sess.Pipeline.RecordSkillSearch([]core.RegistryEntry{entry})
	if params, msg := weatherNowParamsForIntentOrText(ctx, sess, intent, FormatEvent(&event)); msg != "" {
		sess.Pipeline.RecordPendingClarification(PendingClarification{
			Kind:  "weather_now_location",
			Query: FormatEvent(&event),
		})
		return msg, nil
	} else if len(params) > 0 {
		sess.Pipeline.RecordPendingSkillRun(entry.ID, params)
	}
	return "날씨 조회 스킬을 설치하면 방금 말한 위치의 현재 날씨와 비 여부를 바로 확인할 수 있어요. 설치해서 지금 실행할까요?", nil
}

func weatherNowParamsForIntentOrText(ctx context.Context, sess *Session, intent Intent, userText string) (map[string]any, string) {
	if intent.Params != nil {
		locationQuery, _ := intent.Params["location_query"].(string)
		locationQuery = normalizeLocationSlot(locationQuery)
		if locationQuery != "" {
			loc, err := resolveStructuredLocation(ctx, sess, locationQuery)
			if err != nil {
				return nil, fmt.Sprintf("%s 위치를 확인하지 못했어요. 역 이름, 동 이름, 도시 이름처럼 조금 더 명확한 위치로 다시 말씀해 주세요.", locationQuery)
			}
			return weatherLocationParams(loc), ""
		}
	}
	return weatherNowParamsForText(ctx, sess, userText)
}

func weatherNowParamsForText(ctx context.Context, sess *Session, userText string) (map[string]any, string) {
	slots, err := extractWeatherNowSlots(ctx, sess, userText)
	if err != nil {
		return nil, "날씨를 확인하기 전에 말씀하신 위치를 정리하지 못했어요. 위치를 한 번만 더 구체적으로 말씀해 주세요."
	}
	if slots.LocationQuery == "" {
		return nil, ""
	}
	loc, err := resolveStructuredLocation(ctx, sess, slots.LocationQuery)
	if err != nil {
		return nil, fmt.Sprintf("%s 위치를 확인하지 못했어요. 역 이름, 동 이름, 도시 이름처럼 조금 더 명확한 위치로 다시 말씀해 주세요.", slots.LocationQuery)
	}
	return weatherLocationParams(loc), ""
}

func exchangeRateSearchResults(ctx context.Context, sess *Session) []WebSearchResult {
	if sess == nil || sess.Config == nil {
		return nil
	}
	raw, err := webSearch(ctx, "USD KRW 환율 실시간", sess.Config)
	if err != nil {
		return nil
	}
	var payload struct {
		Results []WebSearchResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil
	}
	return payload.Results
}

func formatExchangeRateLookupResponse(results []WebSearchResult, entry core.RegistryEntry) string {
	var sb strings.Builder
	sources := summarizeExchangeRateSources(results)
	if len(sources) == 0 {
		sb.WriteString("웹 검색만으로는 바로 읽을 수 있는 현재 환율 숫자를 찾지 못했습니다.\n")
	} else {
		sb.WriteString("웹 검색에서 환율 관련 페이지를 찾았습니다:\n")
		for _, source := range sources {
			sb.WriteString("• ")
			sb.WriteString(source)
			sb.WriteString("\n")
		}
		sb.WriteString("\n다만 이건 검색 결과 후보이고, 제목과 요약만으로는 현재 환율 숫자를 안전하게 확정하지 못했습니다.\n")
	}
	name := entry.Name
	if name == "" {
		name = "환율 조회"
	}
	sb.WriteString("\n제가 보기엔 ")
	sb.WriteString(name)
	sb.WriteString(" 스킬을 쓰는 편이 낫습니다. 이 스킬을 설치하면 키 없이 현재 환율표를 바로 확인할 수 있어요. 설치해서 지금 실행할까요?")
	return strings.TrimSpace(sb.String())
}

func exchangeRateParamsForText(text string) map[string]any {
	code := currencyCodeFromText(text)
	if code == "" {
		return nil
	}
	t := strings.ToLower(text)
	if strings.Contains(t, "기준") || strings.Contains(t, "base") ||
		strings.Contains(t, "기준으로") || strings.Contains(t, "기준의") ||
		containsAny(t, "원화로", "원으로", "엔화로", "엔으로", "달러로", "유로로", "위안으로", "파운드로") {
		params := map[string]any{
			"base":    code,
			"symbols": defaultExchangeRateSymbols(code),
		}
		if unit := exchangeRateDisplayUnitForText(text); unit > 1 {
			params["unit"] = unit
		}
		return params
	}
	if containsAny(t, "만", "만요", "만.", "only") {
		return map[string]any{
			"symbols": []any{code},
		}
	}
	return nil
}

func currencyCodeFromText(text string) string {
	t := strings.ToLower(text)
	currencies := []struct {
		code    string
		aliases []string
	}{
		{"KRW", []string{"krw", "원화", "원"}},
		{"USD", []string{"usd", "달러", "미달러", "불"}},
		{"JPY", []string{"jpy", "엔화", "엔"}},
		{"EUR", []string{"eur", "유로"}},
		{"CNY", []string{"cny", "위안", "위안화", "중국돈"}},
		{"GBP", []string{"gbp", "파운드"}},
	}
	for _, c := range currencies {
		if containsAny(t, c.aliases...) {
			return c.code
		}
	}
	return ""
}

func defaultExchangeRateSymbols(base string) []any {
	codes := []string{"USD", "EUR", "JPY", "CNY", "GBP", "KRW"}
	out := make([]any, 0, len(codes)-1)
	for _, code := range codes {
		if code != base {
			out = append(out, code)
		}
	}
	return out
}

func exchangeRateRequestedBase(params map[string]any) string {
	base, _ := params["base"].(string)
	return strings.ToUpper(strings.TrimSpace(base))
}

type exchangeRateParsedTable struct {
	header string
	source string
	base   string
	rates  map[string]float64
	order  []string
}

var exchangeRateRowRe = regexp.MustCompile(`(?m)^\s*1\s+([A-Z]{3})\s*=\s*([0-9][0-9,]*(?:\.[0-9]+)?)\s+([A-Z]{3})\s*$`)

func exchangeRateOutputBase(output string) string {
	table, ok := parseExchangeRateOutput(output)
	if !ok {
		return ""
	}
	return table.base
}

func rebaseExchangeRateOutput(output, newBase string) (string, bool) {
	newBase = strings.ToUpper(strings.TrimSpace(newBase))
	if newBase == "" {
		return "", false
	}
	table, ok := parseExchangeRateOutput(output)
	if !ok {
		return "", false
	}
	if table.base == newBase {
		return output, true
	}
	baseRate, ok := table.rates[newBase]
	if !ok || baseRate == 0 {
		return "", false
	}

	symbols := make([]string, 0, len(table.order))
	seen := map[string]bool{newBase: true}
	for _, v := range defaultExchangeRateSymbols(newBase) {
		code, _ := v.(string)
		if code == "" || seen[code] {
			continue
		}
		if _, ok := table.rates[code]; ok {
			symbols = append(symbols, code)
			seen[code] = true
		}
	}
	for _, code := range table.order {
		if code == "" || seen[code] {
			continue
		}
		if _, ok := table.rates[code]; ok {
			symbols = append(symbols, code)
			seen[code] = true
		}
	}
	if len(symbols) == 0 {
		return "", false
	}

	header := table.header
	if header == "" {
		header = "📈 환율"
	}
	var sb strings.Builder
	sb.WriteString(header)
	sb.WriteString("\n\n")
	for i, code := range symbols {
		if i > 0 {
			sb.WriteString("\n")
		}
		rate := table.rates[code] / baseRate
		sb.WriteString("1 ")
		sb.WriteString(newBase)
		sb.WriteString(" = ")
		sb.WriteString(formatExchangeRateNumber(rate))
		sb.WriteString(" ")
		sb.WriteString(code)
	}
	if table.source != "" {
		sb.WriteString("\n\n")
		sb.WriteString(table.source)
	}
	return sb.String(), true
}

func parseExchangeRateOutput(output string) (exchangeRateParsedTable, bool) {
	table := exchangeRateParsedTable{rates: map[string]float64{}}
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if table.header == "" && strings.Contains(trimmed, "환율") && !exchangeRateRowRe.MatchString(trimmed) {
			table.header = trimmed
		}
		if table.source == "" && strings.Contains(trimmed, "Source:") {
			table.source = trimmed
		}
	}

	matches := exchangeRateRowRe.FindAllStringSubmatch(output, -1)
	if len(matches) == 0 {
		return table, false
	}
	seenTargets := map[string]bool{}
	for _, m := range matches {
		base := m[1]
		target := m[3]
		if table.base == "" {
			table.base = base
			table.rates[base] = 1
		} else if table.base != base {
			return table, false
		}
		value, err := strconv.ParseFloat(strings.ReplaceAll(m[2], ",", ""), 64)
		if err != nil || value <= 0 {
			return table, false
		}
		table.rates[target] = value
		if !seenTargets[target] {
			table.order = append(table.order, target)
			seenTargets[target] = true
		}
	}
	if table.base == "" || len(table.rates) < 2 {
		return table, false
	}
	return table, true
}

func formatExchangeRateNumber(v float64) string {
	precision := 8
	switch {
	case v >= 100:
		precision = 2
	case v >= 1:
		precision = 4
	case v >= 0.01:
		precision = 6
	}
	out := strconv.FormatFloat(v, 'f', precision, 64)
	out = strings.TrimRight(out, "0")
	out = strings.TrimRight(out, ".")
	if out == "" {
		return "0"
	}
	return out
}

func summarizeExchangeRateSources(results []WebSearchResult) []string {
	seen := map[string]bool{}
	var out []string
	for _, result := range results {
		key, summary := exchangeRateSourceSummary(result)
		if key == "" || summary == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, summary)
		if len(out) >= 3 {
			break
		}
	}
	return out
}

func exchangeRateSourceSummary(result WebSearchResult) (string, string) {
	text := strings.ToLower(result.Title + " " + result.Snippet + " " + result.URL)
	host := normalizedHost(result.URL)
	switch {
	case strings.Contains(text, "investing.com") || strings.Contains(host, "investing.com"):
		return "investing.com", "Investing.com - USD/KRW 실시간 환율과 차트 페이지"
	case strings.Contains(text, "tradingview") || strings.Contains(host, "tradingview"):
		return "tradingview.com", "TradingView - USD/KRW 차트와 시장 데이터 페이지"
	case strings.Contains(text, "wise.com") || strings.Contains(host, "wise.com"):
		return "wise.com", "Wise - 달러/원 환전 기준 환율 계산기"
	case strings.Contains(text, "알파스퀘어") || strings.Contains(text, "alphasquare") || strings.Contains(host, "alphasquare"):
		return "alphasquare", "알파스퀘어 - 달러/원 환율 페이지"
	case strings.Contains(text, "yahoo") || strings.Contains(host, "yahoo"):
		return "yahoo.com", "Yahoo Finance - USD/KRW 시장 데이터 페이지"
	case strings.Contains(text, "naver") || strings.Contains(host, "naver"):
		return "naver.com", "네이버 금융 - 환율 조회 페이지"
	default:
		label := cleanSearchSourceLabel(result)
		if label == "" {
			return "", ""
		}
		if host != "" {
			return host, label + " - 환율 관련 페이지"
		}
		return label, label + " - 환율 관련 페이지"
	}
}

func normalizedHost(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

func cleanSearchSourceLabel(result WebSearchResult) string {
	title := strings.TrimSpace(result.Title)
	if title == "" {
		return ""
	}
	for _, sep := range []string{" | ", " - ", " — ", " – "} {
		if idx := strings.Index(title, sep); idx > 0 {
			title = title[:idx]
			break
		}
	}
	return strings.TrimSpace(title)
}

type ConfirmClarificationBranch struct{}

func (b *ConfirmClarificationBranch) Execute(ctx context.Context, sess *Session, event core.Event, intent Intent) (string, error) {
	if sess == nil || sess.Pipeline == nil {
		return "", errBranchFallback
	}
	pending, ok := sess.Pipeline.RecentPendingClarification()
	if !ok {
		return "", errBranchFallback
	}
	sess.Pipeline.ClearPendingClarification()

	switch pending.Kind {
	case "exchange_rate":
		if pkg := matchInstalledSkill("환율", sess); pkg != nil {
			rawJSON, _ := runSkillOrPackageWithParams(ctx, pkg.Meta.ID, sess, exchangeRateParamsForText(pending.Query))
			output := extractOutputField(rawJSON)
			if output == "" {
				return "", errBranchFallback
			}
			sess.Pipeline.RecordSkillOutputForSkill(pkg.Meta.ID, output)
			return output, nil
		}
		entry := exchangeRateRegistryEntry()
		sess.Pipeline.RecordSkillSearch([]core.RegistryEntry{entry})
		return "환율 조회 스킬을 설치하면 키 없이 현재 환율표를 바로 가져올 수 있어요. 설치해서 지금 조회할까요?", nil
	case "weather_now":
		if pkg := installedPackageByID("weather-now", sess); pkg != nil {
			params, msg := weatherNowParamsForText(ctx, sess, pending.Query)
			if msg != "" {
				return msg, nil
			}
			rawJSON, _ := runSkillOrPackageWithParams(ctx, pkg.Meta.ID, sess, params)
			output := extractOutputField(rawJSON)
			if output == "" {
				return "", errBranchFallback
			}
			sess.Pipeline.RecordSkillOutputForSkill(pkg.Meta.ID, output)
			return output, nil
		}
		entry := weatherNowRegistryEntry()
		sess.Pipeline.RecordSkillSearch([]core.RegistryEntry{entry})
		if params, msg := weatherNowParamsForText(ctx, sess, pending.Query); msg != "" {
			return msg, nil
		} else if len(params) > 0 {
			sess.Pipeline.RecordPendingSkillRun(entry.ID, params)
		}
		return "날씨 조회 스킬을 설치하면 현재 날씨와 비 여부를 바로 확인할 수 있어요. 설치해서 지금 조회할까요?", nil
	case "weather_now_location":
		replyText := ""
		if p, err := event.ParsePayload(); err == nil {
			replyText = strings.TrimSpace(p.Text)
		}
		if pkg := installedPackageByID("weather-now", sess); pkg != nil {
			params, msg := weatherNowParamsForLocationClarification(ctx, sess, replyText, pending.Query)
			if msg != "" {
				sess.Pipeline.RecordPendingClarification(pending)
				return msg, nil
			}
			if len(params) == 0 {
				sess.Pipeline.RecordPendingClarification(pending)
				return "날씨를 확인하려면 위치가 필요해요. 역 이름, 동 이름, 도시 이름처럼 위치를 한 번만 더 말씀해 주세요.", nil
			}
			rawJSON, _ := runSkillOrPackageWithParams(ctx, pkg.Meta.ID, sess, params)
			output := extractOutputField(rawJSON)
			if output == "" {
				return "", errBranchFallback
			}
			sess.Pipeline.RecordSkillOutputForSkill(pkg.Meta.ID, output)
			return output, nil
		}
		entry := weatherNowRegistryEntry()
		sess.Pipeline.RecordSkillSearch([]core.RegistryEntry{entry})
		if params, msg := weatherNowParamsForLocationClarification(ctx, sess, replyText, pending.Query); msg != "" {
			sess.Pipeline.RecordPendingClarification(pending)
			return msg, nil
		} else if len(params) > 0 {
			sess.Pipeline.RecordPendingSkillRun(entry.ID, params)
		}
		return "날씨 조회 스킬을 설치하면 현재 날씨와 비 여부를 바로 확인할 수 있어요. 설치해서 지금 조회할까요?", nil
	default:
		return "", errBranchFallback
	}
}

func weatherNowParamsForLocationClarification(ctx context.Context, sess *Session, replyText, pendingQuery string) (map[string]any, string) {
	queries := make([]string, 0, 2)
	replyText = strings.TrimSpace(replyText)
	pendingQuery = strings.TrimSpace(pendingQuery)
	if replyText != "" && !isBareAffirmative(replyText) {
		queries = append(queries, replyText)
	}
	if pendingQuery != "" && pendingQuery != replyText {
		queries = append(queries, pendingQuery)
	}
	var lastMsg string
	for _, q := range queries {
		params, msg := weatherNowParamsForText(ctx, sess, q)
		if msg != "" {
			lastMsg = msg
			continue
		}
		if len(params) > 0 {
			return params, ""
		}
	}
	return nil, lastMsg
}

func containsAny(haystack string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}

func staffCreateRoleFromText(text string) (string, bool) {
	t := strings.TrimSpace(text)
	if t == "" {
		return "", false
	}
	lower := strings.ToLower(t)
	if !(containsAny(lower, "만들", "고용", "채용", "hire", "create") &&
		containsAny(lower, "staff", "비서", "담당", "pm", "피엠", "매니저",
			"디자인", "디자이너", "재무", "법무", "마케팅", "운영", "데이터", "테스트", "qa")) {
		return "", false
	}
	role := t
	replacer := strings.NewReplacer(
		"한 명", "", "하나", "", "새", "", "staff", "", "Staff", "",
		"비서를", "", "비서", "", "스태프를", "", "스태프", "",
		"만들어줘", "", "만들어 줘", "", "만들어주세요", "", "만들어 주세요", "",
		"고용해줘", "", "고용해 줘", "", "고용해", "", "채용해줘", "", "채용해 줘", "", "채용해", "",
		"create", "", "hire", "",
	)
	role = strings.TrimSpace(replacer.Replace(role))
	role = strings.Trim(role, ".。!！?？")
	role = strings.TrimSpace(role)
	if role == "" {
		role = t
	}
	return role, true
}

func isStaffDraftApproval(text string) bool {
	t := strings.TrimSpace(strings.ToLower(text))
	if isStaffAffirmative(t) {
		return true
	}
	return containsAny(t, "생성해", "만들어", "진행", "승인", "좋아", "ok", "ㅇㅋ")
}

func isStaffAffirmative(text string) bool {
	t := strings.TrimSpace(strings.ToLower(text))
	if isBareAffirmative(t) {
		return true
	}
	if runeCount(t) > 24 {
		return false
	}
	t = strings.Trim(t, " .,!?\t\n\r~。！？")
	return containsAny(t, "좋아", "좋아요", "해주세요", "해줘", "진행", "만들", "생성", "채용", "고용", "ok", "okay", "yes")
}

func isStaffDraftCancel(text string) bool {
	t := strings.TrimSpace(strings.ToLower(text))
	return isBareNegative(t) || containsAny(t, "취소", "그만", "하지마", "나중에")
}

// InstallConsentBranch handles the user's "네" / "설치해줘요" / etc.
// reply to a previous turn's install offer. The skill id comes from
// PipelineState.RecentSkillSearch — recorded by the legacy path's
// `Skill.search` call. No LLM hallucination of the id (truncation
// regression in commit a4dc8a4 / 26d25c2).
//
// Reproduces user-vision pattern (2) "도구 부족 가시화" plus the
// friendly staff identity — the user agrees, and the system installs and
// runs without asking again.
type InstallConsentBranch struct{}

func (b *InstallConsentBranch) Execute(ctx context.Context, sess *Session, event core.Event, intent Intent) (string, error) {
	if sess == nil || sess.Pipeline == nil {
		return "", errBranchFallback
	}
	results := sess.Pipeline.RecentSkillSearch()
	if len(results) == 0 {
		// Should not happen — classifier gates on this — but be defensive.
		return "", errBranchFallback
	}
	target := results[0]
	if id, _ := intent.Params["skill_id"].(string); id != "" {
		found := false
		for _, result := range results {
			if result.ID == id {
				target = result
				found = true
				break
			}
		}
		if !found {
			return "방금 제안한 스킬 목록에서 '" + id + "' 항목을 찾지 못했어요. 다시 선택해 주세요.", nil
		}
	} else if len(results) > 1 {
		return formatSkillChoicePrompt(results), nil
	}

	// Guard: PackageManager must be wired (not in some bare test fixtures).
	if sess.PackageManager == nil {
		return "지금 스킬을 설치하기 위한 환경이 준비되지 않았어요. 잠시 후 다시 시도해 주세요.", nil
	}

	rc, err := newRegistryClient(sess.Config)
	if err != nil {
		return "지금 스킬 레지스트리에 연결하지 못했어요. 잠시 후 다시 시도해 주세요.", nil
	}
	entry, err := rc.FindEntry(target.ID)
	if err != nil || entry == nil {
		return "스킬 레지스트리에서 '" + target.Name + "' 항목을 다시 찾지 못했어요. 잠시 후 다시 시도해 주세요.", nil
	}

	pkg, err := sess.PackageManager.InstallFromRegistry(rc, *entry)
	if err != nil {
		return "'" + target.Name + "' 설치 중 문제가 발생했어요: " + err.Error(), nil
	}

	params, _ := sess.Pipeline.RecentPendingSkillRun(pkg.Meta.ID)

	// Clear so a later unrelated "네" doesn't re-install the same skill.
	sess.Pipeline.ClearSkillSearch()

	// Run immediately — match the user vision of "agree → see result".
	output, _ := runSkillOrPackageWithParams(ctx, pkg.Meta.ID, sess, params)
	runOutput := extractOutputField(output)
	if runOutput == "" {
		runOutput = "방금 설치된 스킬을 한 번 실행해 보세요. 결과가 비어 있어요."
	}
	// Cache the raw skill output (without the install ack) so a
	// follow-up turn can augment its system prompt with it. See
	// runAgentLoop's cross-turn augmentation block.
	sess.Pipeline.RecordSkillOutputForSkill(pkg.Meta.ID, runOutput)
	return "✅ '" + pkg.Meta.Name + "' 스킬을 설치했어요.\n\n" + runOutput, nil
}

func formatSkillChoicePrompt(entries []core.RegistryEntry) string {
	var sb strings.Builder
	sb.WriteString("어떤 스킬을 설치할까요?\n")
	for i, entry := range entries {
		if i >= 5 {
			break
		}
		sb.WriteString("• ")
		sb.WriteString(entry.Name)
		if entry.Description != "" {
			sb.WriteString(" — ")
			sb.WriteString(entry.Description)
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\n원하시는 스킬 이름으로 답해주세요.")
	return sb.String()
}

type AmbiguousFollowupBranch struct{}

func (b *AmbiguousFollowupBranch) Execute(ctx context.Context, sess *Session, event core.Event, intent Intent) (string, error) {
	if sess == nil || sess.Pipeline == nil || sess.Provider == nil {
		return "", errBranchFallback
	}
	userText := ""
	if p, err := event.ParsePayload(); err == nil {
		userText = strings.TrimSpace(p.Text)
	}
	if userText == "" {
		return "", errBranchFallback
	}
	skillID, _ := intent.Params["skill_id"].(string)
	rawOutput, _ := intent.Params["raw_output"].(string)
	if skillID == "" || rawOutput == "" {
		return "", errBranchFallback
	}
	decision, err := classifyAmbiguousFollowup(ctx, sess, userText, skillID, rawOutput)
	if err != nil {
		return "", errBranchFallback
	}
	locationQuery := normalizeLocationSlot(decision.LocationQuery)
	if locationQuery == "" {
		locationQuery = inferWeatherLocationFromText(userText)
	}
	if locationQuery == "" {
		candidate := normalizeLocationSlot(userText)
		if looksLikePlainLocationSlot(candidate) {
			locationQuery = candidate
		}
	}
	if decision.Intent == "weather_now" {
		if decision.Confidence >= 0.70 && locationQuery != "" {
			return (&WeatherNowLookupBranch{}).Execute(ctx, sess, event, Intent{
				Kind: IntentWeatherNowLookup,
				Params: map[string]any{
					"location_query": locationQuery,
				},
				Confidence: decision.Confidence,
			})
		}
		if locationQuery != "" {
			sess.Pipeline.RecordPendingClarification(PendingClarification{
				Kind:  "weather_now_location",
				Query: locationQuery,
			})
			return fmt.Sprintf("%s 현재 날씨를 말씀하시는 걸까요? 맞으면 \"네\"라고 답해주세요.", locationQuery), nil
		}
	}
	return "", errBranchFallback
}

type ambiguousFollowupDecision struct {
	Intent        string  `json:"intent"`
	Confidence    float64 `json:"confidence"`
	LocationQuery string  `json:"location_query"`
}

func classifyAmbiguousFollowup(ctx context.Context, sess *Session, userText, skillID, rawOutput string) (ambiguousFollowupDecision, error) {
	var decision ambiguousFollowupDecision
	prompt := buildAmbiguousFollowupPrompt(userText, skillID, rawOutput)
	resp, err := sess.Provider.Generate(WithLLMCallKind(ctx, "pipeline.followup"), buildSubLLMMessages(prompt))
	if err != nil || resp == nil {
		return decision, err
	}
	raw := strings.TrimSpace(stripFences(resp.Content))
	if start := strings.Index(raw, "{"); start >= 0 {
		if end := strings.LastIndex(raw, "}"); end >= start {
			raw = raw[start : end+1]
		}
	}
	if err := json.Unmarshal([]byte(raw), &decision); err != nil {
		return ambiguousFollowupDecision{}, err
	}
	decision.Intent = normalizeAmbiguousFollowupIntent(decision.Intent)
	decision.LocationQuery = normalizeLocationSlot(decision.LocationQuery)
	if decision.Confidence < 0 {
		decision.Confidence = 0
	}
	if decision.Confidence > 1 {
		decision.Confidence = 1
	}
	return decision, nil
}

func normalizeAmbiguousFollowupIntent(intent string) string {
	intent = strings.ToLower(strings.TrimSpace(intent))
	intent = strings.NewReplacer("-", "_", " ", "_").Replace(intent)
	return intent
}

func buildAmbiguousFollowupPrompt(userText, skillID, rawOutput string) string {
	if len(rawOutput) > 800 {
		rawOutput = rawOutput[:800] + "\n…(truncated)"
	}
	var b strings.Builder
	b.WriteString("Classify an ambiguous short follow-up in a chat with a local assistant.\n")
	b.WriteString("Return exactly one JSON object and nothing else:\n")
	b.WriteString(`{"intent":"weather_now|unknown","confidence":0.0,"location_query":""}`)
	b.WriteString("\n\nRules:\n")
	b.WriteString("- Decide what the user means from the current message and the previous skill result.\n")
	b.WriteString("- Use weather_now only if the user is likely asking for the same current-weather question about a new location.\n")
	b.WriteString("- Put the explicit new place in location_query. Do not geocode.\n")
	b.WriteString("- If confidence is below 0.70, keep the best intent but lower confidence; the caller will ask for confirmation.\n")
	b.WriteString("- If this is not a current-weather follow-up, use intent unknown.\n\n")
	b.WriteString("Previous skill id: ")
	b.WriteString(skillID)
	b.WriteString("\nPrevious skill output:\n---\n")
	b.WriteString(rawOutput)
	b.WriteString("\n---\nCurrent user message:\n")
	b.WriteString(userText)
	return b.String()
}

// ModifierFollowupBranch handles a short, modifier-shaped follow-up
// turn ("원화로 환율", "아니 원화를 기준으로 하면?", "다시", "간단히") that
// references the prior skill output cached in PipelineState. Instead of
// re-running a skill or asking the legacy LLM (whose "ignore history →
// re-search" prior dominated through Phase 10/10.1), the branch routes
// straight into mediateSkillOutputWithTools — a tool-use loop where
// Code.exec is registered as a callable, so the LLM can compute the
// requested transform on the cached raw numbers deterministically.
// Phase 11 of the level3 plan.
type ModifierFollowupBranch struct{}

func (b *ModifierFollowupBranch) Execute(ctx context.Context, sess *Session, event core.Event, intent Intent) (string, error) {
	rawOutput, _ := intent.Params["raw_output"].(string)
	if rawOutput == "" {
		// Cache emptied between classify and execute — fall back so the
		// legacy LLM can still answer with whatever history holds.
		return "", errBranchFallback
	}
	userText := ""
	if p, err := event.ParsePayload(); err == nil {
		userText = p.Text
	}
	if userText == "" {
		return "", errBranchFallback
	}
	skillID, _ := intent.Params["skill_id"].(string)
	if skillID == "exchange-rate" {
		if params := exchangeRateParamsForText(userText); len(params) > 0 {
			requestedBase := exchangeRateRequestedBase(params)
			rawJSON, _ := runSkillOrPackageWithParams(ctx, skillID, sess, params)
			output := extractOutputField(rawJSON)
			output = applyExchangeRateDisplayParams(output, params)
			if requestedBase != "" {
				if output != "" && exchangeRateOutputBase(output) == requestedBase {
					sess.Pipeline.RecordSkillOutputForSkill(skillID, output)
					return output, nil
				}
				if output != "" {
					if rebased := applyExchangeRateDisplayParams(output, params); rebased != "" {
						sess.Pipeline.RecordSkillOutputForSkill(skillID, rebased)
						return rebased, nil
					}
				}
				if rebased := applyExchangeRateDisplayParams(rawOutput, params); rebased != rawOutput || exchangeRateOutputBase(rawOutput) == requestedBase {
					sess.Pipeline.RecordSkillOutputForSkill(skillID, rebased)
					return rebased, nil
				}
			}
			if output != "" {
				sess.Pipeline.RecordSkillOutputForSkill(skillID, output)
				return output, nil
			}
		}
	}
	out := mediateSkillOutputWithTools(ctx, sess, "modifier_followup", userText, rawOutput)
	if out == "" {
		return "", errBranchFallback
	}
	// Re-record the mediated output as the new "recent" raw so a
	// chained follow-up ("그럼 EUR 만?") sees the latest table — the
	// previous turn's result is the new source of truth.
	sess.Pipeline.RecordSkillOutputForSkill(skillID, out)
	return out, nil
}

// errBranchFallback signals "this branch declined; let the legacy path
// handle it". dispatchPipeline already turns any branch error into a
// fallback, but this sentinel makes the intent explicit at the call site.
var errBranchFallback = errBranchFallbackType{}

type errBranchFallbackType struct{}

func (errBranchFallbackType) Error() string { return "branch fallback to legacy" }

// RunInstalledSkillBranch dispatches an installed skill directly when
// the user's query keyword matches the skill name. Replaces the legacy
// LLM path's "PRIORITY: installed → Skill.run" rule, which the model
// occasionally ignored — most visibly in the 2026-04-27 transcript
// where "환율" right after installing "환율 조회" still triggered
// Web.search + a duplicate install offer. The deterministic branch
// removes that drift.
type RunInstalledSkillBranch struct{}

func (b *RunInstalledSkillBranch) Execute(ctx context.Context, sess *Session, event core.Event, intent Intent) (string, error) {
	skillID, _ := intent.Params["skill_id"].(string)
	if skillID == "" {
		return "", errBranchFallback
	}
	rawJSON, _ := runSkillOrPackage(ctx, skillID, sess)
	output := extractOutputField(rawJSON)
	if output == "" {
		// Empty output is the legacy "(no output)" path — fall back rather
		// than echo a confusing "응답이 비어 있어요" template here, since
		// the legacy LLM may have a meaningful reformulation.
		return "", errBranchFallback
	}
	userText := ""
	if p, err := event.ParsePayload(); err == nil {
		userText = p.Text
	}
	// Cache the raw skill output (pre-mediation) so a follow-up turn
	// can augment its system prompt with the source of truth, not the
	// LLM's reframed paraphrase. mediation may guard-fall-back to raw
	// anyway, but caching pre-mediation keeps the augmentation honest.
	sess.Pipeline.RecordSkillOutputForSkill(skillID, output)
	return mediateSkillOutput(ctx, sess, skillID, userText, output), nil
}

// mediateSkillRawOutputCap caps how much skill output is fed to the
// reframing LLM. Mirrors moaCandidateCharLimit so a verbose skill
// (e.g. large JSON fetch) cannot blow up input tokens. Above the cap we
// truncate with a marker; truncation is rare (exchange-rate / weather /
// news outputs all sit under 2 kB) and safer than uncapped spend.
const mediateSkillRawOutputCap = 8000

// mediateSkillOutput reframes a skill's raw output through a small LLM
// call so the user query's modifier (단위/언어/scope/verbosity) lands in
// the response. The contract is reformat-only: raw numbers stay
// verbatim, no new web search, no fabrication. On any failure (nil
// provider, LLM error, empty response) returns rawOutput unchanged so
// the user never loses the underlying data.
//
// Cost trade-off: Phase 4 RunInstalledSkillBranch was 0 LLM calls per
// dispatch (verbatim output). This adds one small call per dispatch to
// align the response with query intent. The verbatim path was correct
// on shape but lost user-vision quality whenever the query carried a
// modifier the skill JS didn't parse — fixing that in skill JS would
// be case-by-case (env feedback_no_hardcoding.md). LLM mediation
// generalizes the fix to every installed skill at a measured 1-call
// cost. Cache deferred (query-dependent key gives low hit rate).
func mediateSkillOutput(ctx context.Context, sess *Session, skillID, userText, rawOutput string) string {
	if sess == nil || sess.Provider == nil || rawOutput == "" || userText == "" {
		return rawOutput
	}
	truncated := rawOutput
	if len(truncated) > mediateSkillRawOutputCap {
		truncated = truncated[:mediateSkillRawOutputCap] + "\n…(truncated)"
	}
	messages := buildSubLLMMessages(buildMediatePrompt(skillID, userText, truncated))
	resp, err := sess.Provider.Generate(WithLLMCallKind(ctx, "pipeline.mediate"), messages)
	if err != nil || resp == nil {
		return rawOutput
	}
	out := strings.TrimSpace(resp.Content)
	if out == "" {
		return rawOutput
	}
	if !mediationPreservesFacts(rawOutput, out) {
		// LLM ignored the raw and fabricated a response from priors.
		// Observed in 2026-04-27: T3 "환율" with raw "1 USD = 1477.04
		// KRW…" yielded a hallucinated "정확한 수치는 가져오지 못했습니다"
		// + spurious install offer. Numeric-overlap zero is the strongest
		// signal that the LLM didn't read the raw — fall back rather than
		// ship the fabrication.
		return rawOutput
	}
	return out
}

// mediateMaxToolIterations caps how many tool_use rounds
// mediateSkillOutputWithTools will issue per call. Two is enough for the
// observed pattern — one Code.exec to compute, one final text reply.
// More than that is almost always the model looping on its own output;
// raise the cap only with a measurement showing it's needed.
const mediateMaxToolIterations = 2

// codeExecToolDef is the Anthropic tool definition the mediation loop
// registers. The schema mirrors the Code.exec JS API (single jsCode
// string), so the LLM's tool_use call lands as a SkillCall the same
// way an in-sandbox Code.exec(jsCode) would. Keeping the surface
// minimal avoids the model picking exotic argument shapes that
// executeCode's SkillCall handler can't unmarshal.
var codeExecToolDef = llm.Tool{
	Name:        "code_exec",
	Description: "Execute pure-compute JavaScript (ES2020) in an isolated sandbox to verify or transform numbers from the prior skill output. No network, no filesystem, no other skill APIs. Returns {result, logs} on success or {error, logs} on failure.",
	InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"code": map[string]any{
				"type":        "string",
				"description": "JavaScript source. End with a value or `return` for output.",
			},
		},
		"required": []string{"code"},
	},
}

// mediateSkillOutputWithTools is the tool-use upgrade of
// mediateSkillOutput. Where the single-shot version asks the model to
// reformat the raw output and trusts whatever text comes back, this
// version registers code_exec as a callable tool, lets the model run
// arithmetic on the raw numbers when it wants to, and feeds the
// computed result back as a tool_result before the model writes the
// final reply. This is the path the 2026-04-28 transcript T4 fail
// ("아니 원화를 기준으로 하면?") routes through — instead of the
// legacy LLM scratching for fresh web data, the mediator sees the
// cached raw rates plus a deterministic computation tool and produces
// a transformed table directly.
//
// On any failure path (nil provider, error, empty response,
// fabrication-guard miss, max iterations) the function returns the
// raw output unchanged — the user never loses the underlying data.
func mediateSkillOutputWithTools(ctx context.Context, sess *Session, skillID, userText, rawOutput string) string {
	if sess == nil || sess.Provider == nil || rawOutput == "" || userText == "" {
		return rawOutput
	}
	truncated := rawOutput
	if len(truncated) > mediateSkillRawOutputCap {
		truncated = truncated[:mediateSkillRawOutputCap] + "\n…(truncated)"
	}

	prompt := buildMediatePromptWithTools(skillID, userText, truncated)
	messages := buildSubLLMMessages(prompt)

	var lastTextResp *llm.Response
	var toolResults []string
	for i := 0; i < mediateMaxToolIterations; i++ {
		resp, err := sess.Provider.GenerateWithTools(WithLLMCallKind(ctx, "pipeline.mediate.tools"), messages, []llm.Tool{codeExecToolDef})
		if err != nil || resp == nil {
			return rawOutput
		}
		if resp.StopReason != "tool_use" {
			lastTextResp = resp
			break
		}
		// Find the tool_use block(s) and execute each via executeCode.
		toolUseBlocks := make([]core.ContentBlock, 0, 1)
		for _, b := range resp.ContentBlocks {
			if b.Type == core.BlockTypeToolUse && b.Name == codeExecToolDef.Name {
				toolUseBlocks = append(toolUseBlocks, b)
			}
		}
		if len(toolUseBlocks) == 0 {
			// stop_reason said tool_use but no recognized tool block —
			// treat as completion to avoid an infinite loop.
			lastTextResp = resp
			break
		}
		// Append the assistant turn (full response blocks) so the next
		// request preserves the tool_use IDs Anthropic correlates against.
		messages = append(messages, core.LlmMessage{
			Role:          core.RoleAssistant,
			ContentBlocks: resp.ContentBlocks,
		})
		// Execute each tool_use, append a paired tool_result block.
		toolResultBlocks := make([]core.ContentBlock, 0, len(toolUseBlocks))
		for _, tu := range toolUseBlocks {
			code, _ := tu.Input["code"].(string)
			result := executeCodeForMediation(ctx, code)
			toolResults = append(toolResults, result)
			toolResultBlocks = append(toolResultBlocks, core.ContentBlock{
				Type:      core.BlockTypeToolResult,
				ToolUseID: tu.ID,
				Content:   result,
			})
		}
		messages = append(messages, core.LlmMessage{
			Role:          core.RoleUser,
			ContentBlocks: toolResultBlocks,
		})
	}

	if lastTextResp == nil {
		// Loop exited via cap without a final text response — we have
		// no answer to deliver, fall back to raw verbatim.
		return rawOutput
	}
	out := strings.TrimSpace(lastTextResp.Content)
	if out == "" {
		return rawOutput
	}
	if !mediationPreservesFacts(rawOutput, out) {
		if !mediationSharesNumber(strings.Join(toolResults, "\n"), out) {
			return rawOutput
		}
	}
	return out
}

// executeCodeForMediation invokes the existing executeCode handler
// using a SkillCall constructed from the LLM's tool input. The result
// is the JSON envelope executeCode already returns ({result, logs} or
// {error, logs}); we forward it verbatim into the tool_result block so
// the model sees the same shape its own JS would see at runtime.
func executeCodeForMediation(ctx context.Context, jsCode string) string {
	codeJSON, err := json.Marshal(jsCode)
	if err != nil {
		return jsonMarshalErrorEnvelope(err)
	}
	call := core.SkillCall{
		SkillName: "Code",
		Method:    "exec",
		Args:      []json.RawMessage{codeJSON},
	}
	out, _ := executeCode(ctx, call, nil)
	return out
}

func jsonMarshalErrorEnvelope(err error) string {
	b, _ := json.Marshal(map[string]any{"error": err.Error()})
	return string(b)
}

// buildMediatePromptWithTools mirrors buildMediatePrompt but adds the
// tool-use affordance. The model sees the raw output + the user query
// + an explicit hint that code_exec is available for arithmetic.
func buildMediatePromptWithTools(skillID, userText, rawOutput string) string {
	var b strings.Builder
	b.WriteString("사용자 query: \"")
	b.WriteString(userText)
	b.WriteString("\"\n\n설치된 스킬 \"")
	b.WriteString(skillID)
	b.WriteString("\" 의 raw 출력:\n---\n")
	b.WriteString(rawOutput)
	b.WriteString("\n---\n\n위 raw 출력을 사용자 query 의 의도에 맞게 정리해 답하세요. 규칙:\n")
	b.WriteString("- raw 의 수치/사실은 변경 X. 그대로 쓰거나 환산 가능한 단위만 환산.\n")
	b.WriteString("- 새 정보 추가 X. 추가 검색/추론 fabrication 금지.\n")
	b.WriteString("- query 의 modifier (단위/언어/scope/verbosity/negation) 를 응답 형식에 반영.\n")
	b.WriteString("- 응답은 reformatted raw 만. 메타 안내 (추가 도움 권유, 스킬 설치 제안, 후속 질문) 금지.\n")
	b.WriteString("- raw 가 query 충족에 *부족할 때만* 정직히 인정 + 다음 행동 제안.\n")
	b.WriteString("- 짧고 자연스러운 비서 톤.\n\n")
	b.WriteString("**도구**: 단위 변환 / 베이스 재구성 같은 산수가 필요하면 code_exec(code) 도구를 호출하세요. ")
	b.WriteString("e.g. {code: \"const u=1477.04, e=0.85383; ({eur_krw: u/e}).eur_krw.toFixed(2)\"}. ")
	b.WriteString("결과를 받은 뒤 최종 답을 작성하세요. 산수가 필요 없으면 도구 없이 바로 답하세요.")
	return b.String()
}

// mediationNumberRe captures numeric tokens (with optional decimal) so
// we can verify the LLM response shares at least one number with the
// raw output. Currency symbols, units, and locale-specific separators
// are intentionally not parsed — over-strict matching would false-flag
// legit reformatting (e.g. "1,477원" vs "1477"). The check is a
// fabrication floor, not a unit converter validator.
var mediationNumberRe = regexp.MustCompile(`\d+(?:\.\d+)?`)

// mediationPreservesFacts returns true when the mediated response
// shares at least one numeric token with the raw output (or when the
// raw has no numbers, in which case this guard can't speak). Zero
// overlap means the LLM authored the response from priors instead of
// the raw — a fabrication signature we never want to ship.
func mediationPreservesFacts(raw, mediated string) bool {
	rawNums := mediationNumberRe.FindAllString(raw, -1)
	if len(rawNums) == 0 {
		return true
	}
	return mediationSharesNumber(raw, mediated)
}

func mediationSharesNumber(raw, mediated string) bool {
	rawNums := mediationNumberRe.FindAllString(raw, -1)
	if len(rawNums) == 0 {
		return false
	}
	medNums := mediationNumberRe.FindAllString(mediated, -1)
	if len(medNums) == 0 {
		return false
	}
	medSet := make(map[string]struct{}, len(medNums))
	for _, n := range medNums {
		medSet[n] = struct{}{}
	}
	for _, n := range rawNums {
		if _, ok := medSet[n]; ok {
			return true
		}
	}
	return false
}

// buildMediatePrompt is the reformat-only contract sent to the
// reframing LLM. Phrased as general rules (no per-skill enumeration)
// so the same prompt works for every installed skill the user might
// dispatch through RunInstalledSkillBranch.
func buildMediatePrompt(skillID, userText, rawOutput string) string {
	var b strings.Builder
	b.WriteString("사용자 query: \"")
	b.WriteString(userText)
	b.WriteString("\"\n\n설치된 스킬 \"")
	b.WriteString(skillID)
	b.WriteString("\" 의 raw 출력:\n---\n")
	b.WriteString(rawOutput)
	b.WriteString("\n---\n\n위 raw 출력을 사용자 query 의 의도에 맞게 정리해 답하세요. 규칙:\n")
	b.WriteString("- raw 의 수치/사실은 변경 X. 그대로 쓰거나 환산 가능한 단위만 환산.\n")
	b.WriteString("- 새 정보 추가 X. 추가 검색/추론 fabrication 금지.\n")
	b.WriteString("- query 의 modifier (단위/언어/scope/verbosity) 를 응답 형식에 반영.\n")
	b.WriteString("- 응답은 reformatted raw 만. 메타 안내 (추가 도움 권유, 스킬 설치 제안, 후속 질문) 금지.\n")
	b.WriteString("- raw 가 query 충족에 *부족할 때만* 정직히 인정 + 다음 행동 제안.\n")
	b.WriteString("- 짧고 자연스러운 비서 톤.")
	return b.String()
}

// extractOutputField pulls the user-facing string out of runSkillOrPackage's
// JSON envelope. The shape is {"success":true,"output":"..."} on the happy
// path and {"error":"...","output":"..."} on the not-found path (the latter
// also carries an actionable user-facing message). Falls back to the raw
// JSON if the envelope can't be decoded — better than silently dropping.
func extractOutputField(jsonStr string) string {
	type runResult struct {
		Output string `json:"output"`
		Error  string `json:"error"`
	}
	var r runResult
	if err := json.Unmarshal([]byte(jsonStr), &r); err != nil {
		return jsonStr
	}
	if r.Output != "" {
		return r.Output
	}
	if r.Error != "" {
		return r.Error
	}
	return ""
}
