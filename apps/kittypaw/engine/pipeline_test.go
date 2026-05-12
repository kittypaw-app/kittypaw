package engine

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/llm"
	"github.com/jinto/kittypaw/sandbox"
)

// mediateMockProvider is a minimal llm.Provider used to drive
// mediateSkillOutput tests without spinning up a real backend. The
// streaming path delegates to Generate so a single response/err is
// enough to cover all routes.
type mediateMockProvider struct {
	response string
	err      error
	calls    int
}

func (m *mediateMockProvider) Generate(_ context.Context, _ []core.LlmMessage) (*llm.Response, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	return &llm.Response{Content: m.response}, nil
}

func (m *mediateMockProvider) GenerateWithTools(ctx context.Context, msgs []core.LlmMessage, _ []llm.Tool) (*llm.Response, error) {
	return m.Generate(ctx, msgs)
}

func (m *mediateMockProvider) ContextWindow() int { return 200000 }
func (m *mediateMockProvider) MaxTokens() int     { return 4096 }

// toolUseScriptedProvider scripts a tool-use loop: the first N calls
// return a tool_use response (with the supplied tool_use input), the
// rest return a final text response. This shape is what
// mediateSkillOutputWithTools drives, so the test can assert the loop
// terminates correctly and the tool input is forwarded.
type toolUseScriptedProvider struct {
	// scripted tool_use turns (one entry consumed per GenerateWithTools call)
	toolUses []map[string]any
	// final text returned once toolUses is exhausted
	finalText string
	// when nonzero, GenerateWithTools always returns tool_use (loop cap)
	infinite bool
	calls    int
}

func (p *toolUseScriptedProvider) Generate(_ context.Context, _ []core.LlmMessage) (*llm.Response, error) {
	return &llm.Response{Content: p.finalText, StopReason: "end_turn"}, nil
}

func (p *toolUseScriptedProvider) GenerateWithTools(_ context.Context, _ []core.LlmMessage, _ []llm.Tool) (*llm.Response, error) {
	p.calls++
	if p.infinite {
		return &llm.Response{
			ContentBlocks: []core.ContentBlock{{
				Type:  core.BlockTypeToolUse,
				ID:    "toolu_inf",
				Name:  "code_exec",
				Input: map[string]any{"code": "1+1"},
			}},
			StopReason: "tool_use",
		}, nil
	}
	if len(p.toolUses) > 0 {
		input := p.toolUses[0]
		p.toolUses = p.toolUses[1:]
		return &llm.Response{
			ContentBlocks: []core.ContentBlock{{
				Type:  core.BlockTypeToolUse,
				ID:    "toolu_test",
				Name:  "code_exec",
				Input: input,
			}},
			StopReason: "tool_use",
		}, nil
	}
	return &llm.Response{
		Content:    p.finalText,
		StopReason: "end_turn",
	}, nil
}

func (p *toolUseScriptedProvider) ContextWindow() int { return 200000 }
func (p *toolUseScriptedProvider) MaxTokens() int     { return 4096 }

func TestMediateSkillOutput_NilProvider(t *testing.T) {
	sess := &Session{Provider: nil}
	got := mediateSkillOutput(context.Background(), sess, "exchange-rate", "원화로 환율", "raw output")
	if got != "raw output" {
		t.Fatalf("nil provider must return raw output verbatim, got %q", got)
	}
}

func TestMediateSkillOutput_NilSession(t *testing.T) {
	got := mediateSkillOutput(context.Background(), nil, "exchange-rate", "원화로 환율", "raw output")
	if got != "raw output" {
		t.Fatalf("nil session must return raw output verbatim, got %q", got)
	}
}

func TestMediateSkillOutput_EmptyUserText(t *testing.T) {
	mock := &mediateMockProvider{response: "should not be reached"}
	sess := &Session{Provider: mock}
	got := mediateSkillOutput(context.Background(), sess, "exchange-rate", "", "raw")
	if got != "raw" {
		t.Fatalf("empty user text must skip LLM and return raw, got %q", got)
	}
	if mock.calls != 0 {
		t.Fatalf("LLM must not be called when user text is empty, got %d calls", mock.calls)
	}
}

func TestMediateSkillOutput_EmptyRawOutput(t *testing.T) {
	mock := &mediateMockProvider{response: "x"}
	sess := &Session{Provider: mock}
	got := mediateSkillOutput(context.Background(), sess, "exchange-rate", "원화로", "")
	if got != "" {
		t.Fatalf("empty raw must return empty (caller already handled this), got %q", got)
	}
	if mock.calls != 0 {
		t.Fatalf("LLM must not be called when raw output is empty, got %d calls", mock.calls)
	}
}

func TestMediateSkillOutput_LLMError(t *testing.T) {
	mock := &mediateMockProvider{err: errors.New("provider down")}
	sess := &Session{Provider: mock}
	got := mediateSkillOutput(context.Background(), sess, "exchange-rate", "원화로 환율", "raw EUR-base")
	if got != "raw EUR-base" {
		t.Fatalf("LLM error must fall back to raw, got %q", got)
	}
	if mock.calls != 1 {
		t.Fatalf("expected exactly 1 call, got %d", mock.calls)
	}
}

func TestMediateSkillOutput_EmptyResponseFallsBack(t *testing.T) {
	mock := &mediateMockProvider{response: "   \n   "}
	sess := &Session{Provider: mock}
	got := mediateSkillOutput(context.Background(), sess, "exchange-rate", "원화로", "raw")
	if got != "raw" {
		t.Fatalf("whitespace-only LLM response must fall back to raw, got %q", got)
	}
}

func TestMediateSkillOutput_PassThrough(t *testing.T) {
	mock := &mediateMockProvider{response: "1 USD = 1477원\n1 EUR = 1684원"}
	sess := &Session{Provider: mock}
	got := mediateSkillOutput(context.Background(), sess, "exchange-rate", "원화로 환율", "raw EUR-base output")
	if !strings.Contains(got, "1477원") {
		t.Fatalf("expected LLM-mediated response, got %q", got)
	}
	if mock.calls != 1 {
		t.Fatalf("expected exactly 1 LLM call, got %d", mock.calls)
	}
}

func TestBuildMediatePrompt_ContainsContractRules(t *testing.T) {
	prompt := buildMediatePrompt("exchange-rate", "원화로 환율", "1 USD = 1477 KRW")
	checks := []string{
		"exchange-rate",
		"원화로 환율",
		"1 USD = 1477 KRW",
		"수치/사실은 변경 X",
		"fabrication 금지",
		"메타 안내",
		"부족할 때만",
	}
	for _, want := range checks {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q\n--- prompt ---\n%s\n----", want, prompt)
		}
	}
}

func TestMediateSkillOutput_LongRawTruncated(t *testing.T) {
	// Raw output well past the 8 kB cap still produces a valid LLM call
	// (truncation marker added). Use raw with no numbers so the
	// fact-preservation guard short-circuits to true (overlap N/A).
	long := strings.Repeat("A", mediateSkillRawOutputCap+500)
	mock := &mediateMockProvider{response: "summary"}
	sess := &Session{Provider: mock}
	got := mediateSkillOutput(context.Background(), sess, "exchange-rate", "환율", long)
	if got != "summary" {
		t.Fatalf("expected mediated summary, got %q", got)
	}
}

func TestMediateSkillOutput_FabricationFallsBack(t *testing.T) {
	// Raw has numbers; LLM response has none → fabrication signature.
	// Caller must receive raw, not the LLM hallucination.
	raw := "1 USD = 1477.04 KRW\n1 EUR = 1684.32 KRW"
	mock := &mediateMockProvider{response: "환율 정보를 가져오지 못했습니다. 다른 사이트를 확인해주세요."}
	sess := &Session{Provider: mock}
	got := mediateSkillOutput(context.Background(), sess, "exchange-rate", "환율", raw)
	if got != raw {
		t.Fatalf("fabrication (zero numeric overlap) must fall back to raw\n  got: %q\n  raw: %q", got, raw)
	}
}

func TestMediateSkillOutput_PartialNumberOverlapPasses(t *testing.T) {
	// LLM kept some raw numbers but reformatted units — should pass.
	raw := "1 USD = 1477.04 KRW"
	mock := &mediateMockProvider{response: "1 USD = 1477.04원"}
	sess := &Session{Provider: mock}
	got := mediateSkillOutput(context.Background(), sess, "exchange-rate", "원화로 환율", raw)
	if got != "1 USD = 1477.04원" {
		t.Fatalf("LLM kept the raw number — expected mediated response, got %q", got)
	}
}

func TestMediationPreservesFacts_RawHasNoNumbers(t *testing.T) {
	if !mediationPreservesFacts("hello world", "anything goes") {
		t.Fatal("when raw has no numbers, guard must abstain (return true)")
	}
}

func TestMediationPreservesFacts_ZeroOverlap(t *testing.T) {
	if mediationPreservesFacts("1 2 3", "9 8 7") {
		t.Fatal("disjoint numbers must signal fabrication (return false)")
	}
}

func TestMediationPreservesFacts_AnyOverlap(t *testing.T) {
	if !mediationPreservesFacts("1 2 3", "9 8 3") {
		t.Fatal("any shared number must pass (return true)")
	}
}

// TestRecordPipelineTurn_AppendsBothTurns guards the cross-turn fix
// from the 2026-04-27 transcript: a follow-up legacy-LLM turn must
// see the prior branch dispatch in conversation history.
func TestRecordPipelineTurn_AppendsBothTurns(t *testing.T) {
	st := openTestStore(t)
	sess := &Session{Store: st}

	payload, _ := json.Marshal(core.ChatPayload{
		ChatID:    "test-chat",
		Text:      "환율 알려줘",
		SessionID: "test-session",
	})
	event := core.Event{Type: core.EventWebChat, Payload: payload}

	if err := sess.recordPipelineTurn(event, "환율 알려줘", "1 USD = 1477.04 KRW"); err != nil {
		t.Fatalf("recordPipelineTurn: %v", err)
	}

	state, err := st.LoadConversationStateForChat(testWebChatConversationID)
	if err != nil {
		t.Fatalf("LoadConversationState: %v", err)
	}
	if len(state.Turns) != 2 {
		t.Fatalf("expected 2 turns (user + assistant), got %d", len(state.Turns))
	}
	if state.Turns[0].Role != core.RoleUser || state.Turns[0].Content != "환율 알려줘" {
		t.Errorf("turn 0 not user query: %+v", state.Turns[0])
	}
	if state.Turns[0].Channel != "web" || state.Turns[0].ChannelUserID != "test-session" || state.Turns[0].ChatID != "test-chat" {
		t.Errorf("turn 0 metadata mismatch: %+v", state.Turns[0])
	}
	if state.Turns[1].Role != core.RoleAssistant || state.Turns[1].Content != "1 USD = 1477.04 KRW" {
		t.Errorf("turn 1 not branch response: %+v", state.Turns[1])
	}
}

func TestStripBranchControlMarker_RemovesInstallAck(t *testing.T) {
	in := "✅ '환율 조회' 스킬을 설치했어요.\n\n📈 환율\n1 USD = 1477 KRW"
	want := "📈 환율\n1 USD = 1477 KRW"
	got := stripBranchControlMarker(in)
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestStripBranchControlMarker_NoMarkerPassThrough(t *testing.T) {
	in := "📈 환율\n1 USD = 1477 KRW"
	if got := stripBranchControlMarker(in); got != in {
		t.Errorf("untouched response should pass through, got %q", got)
	}
}

func TestRecordPipelineTurn_StripsAckBeforeStoring(t *testing.T) {
	// History append must not propagate the install-ack marker to the
	// next turn's legacy-LLM context — otherwise the LLM sees the ack
	// pattern and copies it back into its own response (2026-04-27
	// regression: '스킬을 설치했어요' count=2 in flow_installed_dispatch).
	st := openTestStore(t)
	sess := &Session{Store: st}
	payload, _ := json.Marshal(core.ChatPayload{
		ChatID:    "test-chat",
		Text:      "네",
		SessionID: "test-session",
	})
	event := core.Event{Type: core.EventWebChat, Payload: payload}
	if err := sess.recordPipelineTurn(event, "네", "✅ '환율 조회' 스킬을 설치했어요.\n\n📈 환율\n1 USD = 1477.04 KRW"); err != nil {
		t.Fatal(err)
	}
	state, _ := st.LoadConversationStateForChat(testWebChatConversationID)
	if len(state.Turns) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(state.Turns))
	}
	stored := state.Turns[1].Content
	if strings.Contains(stored, "스킬을 설치했어요") {
		t.Errorf("ack marker leaked into history: %q", stored)
	}
	if !strings.Contains(stored, "1477.04") {
		t.Errorf("data part dropped from history: %q", stored)
	}
}

func TestMediateWithTools_CodeExecLoop(t *testing.T) {
	// Provider scripts: 1) tool_use with arithmetic on raw, 2) final text.
	// Asserts the loop forwards the LLM-issued code through executeCode
	// and the final response reaches the caller even when the final computed
	// number does not overlap with the raw input numbers.
	p := &toolUseScriptedProvider{
		toolUses: []map[string]any{
			{"code": "const u=1477.04, e=0.85383; (u/e).toFixed(2)"},
		},
		finalText: "EUR/KRW = 1729.90",
	}
	sess := &Session{Provider: p}
	out := mediateSkillOutputWithTools(context.Background(), sess, "exchange-rate", "원화로 환율", "1 USD = 1477.04 KRW, 1 USD = 0.85383 EUR")
	if !strings.Contains(out, "1729.90") {
		t.Fatalf("final text not delivered, got: %q", out)
	}
	if strings.Contains(out, "1477.04") || strings.Contains(out, "0.85383") {
		t.Errorf("final answer should not need to preserve raw numbers after tool arithmetic: %q", out)
	}
	if p.calls != 2 {
		t.Errorf("expected 2 GenerateWithTools calls (tool_use + final), got %d", p.calls)
	}
}

func TestMediateWithTools_LoopCapped(t *testing.T) {
	// Provider keeps returning tool_use forever. After the cap the
	// loop must fall back to the raw output rather than spin.
	p := &toolUseScriptedProvider{infinite: true}
	sess := &Session{Provider: p}
	raw := "1 USD = 1477.04 KRW"
	out := mediateSkillOutputWithTools(context.Background(), sess, "exchange-rate", "원화로", raw)
	if out != raw {
		t.Fatalf("loop cap should fall back to raw, got %q", out)
	}
	if p.calls != mediateMaxToolIterations {
		t.Errorf("expected exactly %d calls before cap, got %d", mediateMaxToolIterations, p.calls)
	}
}

func TestMediateWithTools_FabricationGuardFalls(t *testing.T) {
	// Provider goes straight to a final text with zero numeric overlap
	// with the raw — fabrication signature. Caller must receive raw.
	p := &toolUseScriptedProvider{
		finalText: "환율 정보를 가져오지 못했습니다. 사이트를 확인하세요.",
	}
	sess := &Session{Provider: p}
	raw := "1 USD = 1477.04 KRW"
	out := mediateSkillOutputWithTools(context.Background(), sess, "exchange-rate", "원화로", raw)
	if out != raw {
		t.Fatalf("zero-overlap final text must fall back to raw, got %q", out)
	}
}

func TestMediateWithTools_NilProviderFallsBack(t *testing.T) {
	sess := &Session{Provider: nil}
	out := mediateSkillOutputWithTools(context.Background(), sess, "x", "원화로", "1 USD = 1477 KRW")
	if out != "1 USD = 1477 KRW" {
		t.Fatalf("nil provider must return raw, got %q", out)
	}
}

func TestClassifyIntent_ModifierFollowupRouting(t *testing.T) {
	// queryHasModifier=true + RecentSkillOutput populated + short →
	// IntentModifierFollowup with cached raw in Params.
	state := NewPipelineState()
	state.RecordSkillOutputForSkill("exchange-rate", "1 USD = 1477.04 KRW")
	intent := classifyIntent("원화로 환율", state, nil)
	if intent.Kind != IntentModifierFollowup {
		t.Fatalf("expected IntentModifierFollowup, got %v", intent.Kind)
	}
	raw, _ := intent.Params["raw_output"].(string)
	if raw == "" {
		t.Errorf("raw_output not propagated to intent.Params")
	}
	if got, _ := intent.Params["skill_id"].(string); got != "exchange-rate" {
		t.Errorf("skill_id = %q, want exchange-rate", got)
	}
}

func TestClassifyIntent_ModifierFollowup_NoCacheBypasses(t *testing.T) {
	// queryHasModifier=true but cache empty → must NOT route to
	// ModifierFollowup; falls through to legacy fallback (or other
	// branches if applicable). Without raw output the mediation has
	// nothing to mediate.
	state := NewPipelineState()
	intent := classifyIntent("원화로 환율", state, nil)
	if intent.Kind == IntentModifierFollowup {
		t.Fatalf("empty cache must not route to ModifierFollowup")
	}
}

func TestClassifyIntent_RecentWeatherLocationFollowupRoutesToAmbiguousFollowup(t *testing.T) {
	state := NewPipelineState()
	state.RecordSkillOutputForSkill("weather-now", "강남역 현재 날씨\n1시간 강수: 없음")

	intent := classifyIntent("장안동은?", state, &Session{})

	if intent.Kind != IntentAmbiguousFollowup {
		t.Fatalf("expected IntentAmbiguousFollowup, got %v", intent.Kind)
	}
	if got, _ := intent.Params["skill_id"].(string); got != "weather-now" {
		t.Fatalf("skill_id = %q, want weather-now", got)
	}
}

func TestClassifyIntent_LongModifierBypassesFollowup(t *testing.T) {
	// Modifier in a long sentence is a fresh request, not a follow-up.
	state := NewPipelineState()
	state.RecordSkillOutput("data")
	long := strings.Repeat("원화로 환율 한 번 알려줘 자세히 ", 3) // > 30 runes
	intent := classifyIntent(long, state, nil)
	if intent.Kind == IntentModifierFollowup {
		t.Fatalf("long modifier query should not route to ModifierFollowup, got %+v", intent)
	}
}

func TestExchangeRateParamsForText_KRWBase(t *testing.T) {
	params := exchangeRateParamsForText("원화기준... 으로요..")
	if params == nil {
		t.Fatal("expected exchange-rate params")
	}
	if got := params["base"]; got != "KRW" {
		t.Fatalf("base = %v, want KRW", got)
	}
	symbols, ok := params["symbols"].([]any)
	if !ok || len(symbols) == 0 {
		t.Fatalf("symbols missing: %+v", params)
	}
	for _, s := range symbols {
		if s == "KRW" {
			t.Fatalf("symbols must exclude base KRW: %+v", symbols)
		}
	}
}

func TestModifierFollowupBranch_ExchangeRateRerunsWithStructuredBase(t *testing.T) {
	skipWithoutRuntime(t)

	state := NewPipelineState()
	state.RecordSkillOutputForSkill("exchange-rate", "1 USD = 1477 KRW")
	baseDir := t.TempDir()
	pm := installTestPackage(t, baseDir, `[meta]
id = "exchange-rate"
name = "환율 조회"
version = "1.0.0"
description = "환율을 조회합니다."
`, `
const ctx = JSON.parse(__context__);
return "1 " + ctx.params.base + " = 0.00068 USD";
`)
	cfg := core.DefaultConfig()
	sess := &Session{
		Pipeline:       state,
		PackageManager: pm,
		Sandbox:        sandbox.New(cfg.Sandbox),
		Config:         &cfg,
	}
	intent := classifyIntent("원화기준... 으로요..", state, sess)
	if intent.Kind != IntentModifierFollowup {
		t.Fatalf("intent = %v, want modifier followup", intent.Kind)
	}

	out, err := (&ModifierFollowupBranch{}).Execute(context.Background(), sess, webChatEvent("원화기준... 으로요.."), intent)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, "1 KRW = 0.00068 USD") {
		t.Fatalf("out = %q, want KRW-base package output", out)
	}
}

func TestModifierFollowupBranch_ExchangeRateRebasesOldPackageOutput(t *testing.T) {
	skipWithoutRuntime(t)

	state := NewPipelineState()
	raw := "📈 환율 (2026-04-30)\n\n1 USD = 0.85383 EUR\n1 USD = 156.56 JPY\n1 USD = 1477 KRW\n\n_Source: Frankfurter (ECB) · Powered by KittyPaw_"
	state.RecordSkillOutputForSkill("exchange-rate", raw)
	baseDir := t.TempDir()
	pm := installTestPackage(t, baseDir, `[meta]
id = "exchange-rate"
name = "환율 조회"
version = "1.0.0"
description = "환율을 조회합니다."
`, `
// Simulates the already-installed 1.0.0 package that ignores ctx.params.
return "📈 환율 (2026-04-30)\n\n1 USD = 0.85383 EUR\n1 USD = 156.56 JPY\n1 USD = 1477 KRW\n\n_Source: Frankfurter (ECB) · Powered by KittyPaw_";
`)
	cfg := core.DefaultConfig()
	sess := &Session{
		Pipeline:       state,
		PackageManager: pm,
		Sandbox:        sandbox.New(cfg.Sandbox),
		Config:         &cfg,
	}
	intent := classifyIntent("원화기준으로 다시", state, sess)

	out, err := (&ModifierFollowupBranch{}).Execute(context.Background(), sess, webChatEvent("원화기준으로 다시"), intent)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, "1 KRW =") || !strings.Contains(out, "USD") || !strings.Contains(out, "JPY") {
		t.Fatalf("expected KRW-base rebased table, got:\n%s", out)
	}
	if strings.Contains(out, "1 USD = 1477 KRW") {
		t.Fatalf("must not repeat USD-base raw output:\n%s", out)
	}
}

func TestExchangeRateLookupBranchUsesAcceptedDisplayPreference(t *testing.T) {
	skipWithoutRuntime(t)

	state := NewPipelineState()
	st := openTestStore(t)
	if err := st.SetUserContext(
		"preference:exchange_rate.display",
		`{"base":"KRW","unit":1000}`,
		"test",
	); err != nil {
		t.Fatal(err)
	}
	baseDir := t.TempDir()
	pm := installTestPackage(t, baseDir, `[meta]
id = "exchange-rate"
name = "환율 조회"
version = "1.0.0"
description = "환율을 조회합니다."
`, `
return "📈 환율 (2026-04-30)\n\n1 USD = 0.85383 EUR\n1 USD = 156.56 JPY\n1 USD = 1477 KRW";
`)
	cfg := core.DefaultConfig()
	sess := &Session{
		Pipeline:       state,
		Store:          st,
		PackageManager: pm,
		Sandbox:        sandbox.New(cfg.Sandbox),
		Config:         &cfg,
	}

	out, err := (&ExchangeRateLookupBranch{}).Execute(context.Background(), sess, webChatEvent("환율"), Intent{
		Kind: IntentExchangeRateLookup,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, "1,000 KRW = 0.68 USD") {
		t.Fatalf("out = %q, want accepted 1,000 KRW display preference", out)
	}
	if strings.Contains(out, "앞으로 그렇게") {
		t.Fatalf("accepted preference must not ask for confirmation again:\n%s", out)
	}
}

func TestExchangeRateLookupBranchExplicitBaseOverridesPreference(t *testing.T) {
	skipWithoutRuntime(t)

	state := NewPipelineState()
	st := openTestStore(t)
	if err := st.SetUserContext(
		"preference:exchange_rate.display",
		`{"base":"KRW","unit":1000}`,
		"test",
	); err != nil {
		t.Fatal(err)
	}
	baseDir := t.TempDir()
	pm := installTestPackage(t, baseDir, `[meta]
id = "exchange-rate"
name = "환율 조회"
version = "1.0.0"
description = "환율을 조회합니다."
`, `
return "📈 환율 (2026-04-30)\n\n1 USD = 0.85383 EUR\n1 USD = 156.56 JPY\n1 USD = 1477 KRW";
`)
	cfg := core.DefaultConfig()
	sess := &Session{
		Pipeline:       state,
		Store:          st,
		PackageManager: pm,
		Sandbox:        sandbox.New(cfg.Sandbox),
		Config:         &cfg,
	}

	out, err := (&ExchangeRateLookupBranch{}).Execute(context.Background(), sess, webChatEvent("달러 기준 환율"), Intent{
		Kind: IntentExchangeRateLookup,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if strings.Contains(out, "1,000 KRW") {
		t.Fatalf("explicit base request must override stored preference:\n%s", out)
	}
	if !strings.Contains(out, "1 USD = 1477 KRW") {
		t.Fatalf("out = %q, want USD-base package output", out)
	}
}

func TestExchangeRateLookupBranchAsksOnceForDisplayPreferenceCandidate(t *testing.T) {
	skipWithoutRuntime(t)

	state := NewPipelineState()
	st := openTestStore(t)
	if err := st.SetUserContext(
		"preference_candidate:exchange_rate.display",
		`{"base":"KRW","unit":1000,"reason":"사용자가 원화는 보통 1000원 기준이라고 반복해서 정정함"}`,
		"test",
	); err != nil {
		t.Fatal(err)
	}
	baseDir := t.TempDir()
	pm := installTestPackage(t, baseDir, `[meta]
id = "exchange-rate"
name = "환율 조회"
version = "1.0.0"
description = "환율을 조회합니다."
`, `
return "📈 환율 (2026-04-30)\n\n1 USD = 0.85383 EUR\n1 USD = 156.56 JPY\n1 USD = 1477 KRW";
`)
	cfg := core.DefaultConfig()
	sess := &Session{
		Pipeline:       state,
		Store:          st,
		PackageManager: pm,
		Sandbox:        sandbox.New(cfg.Sandbox),
		Config:         &cfg,
	}

	out, err := (&ExchangeRateLookupBranch{}).Execute(context.Background(), sess, webChatEvent("환율"), Intent{
		Kind: IntentExchangeRateLookup,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, "이전 대화 기록상 환율을 1,000 KRW 기준으로 보길 원하신 것 같아요") {
		t.Fatalf("out = %q, want one-time preference confirmation", out)
	}
	if _, ok, _ := st.GetUserContext("preference_pending_confirmation:exchange_rate.display"); !ok {
		t.Fatal("pending preference confirmation was not stored")
	}
	if _, ok, _ := st.GetUserContext("preference_candidate_surfaced:exchange_rate.display"); !ok {
		t.Fatal("candidate surface marker was not stored")
	}

	out, err = (&ExchangeRateLookupBranch{}).Execute(context.Background(), sess, webChatEvent("환율"), Intent{
		Kind: IntentExchangeRateLookup,
	})
	if err != nil {
		t.Fatalf("second Execute error: %v", err)
	}
	if strings.Contains(out, "앞으로 그렇게") {
		t.Fatalf("candidate should be surfaced only once:\n%s", out)
	}
}

func TestExchangeRateDisplayPreferenceCandidateReadsLegacyMemory(t *testing.T) {
	st := openTestStore(t)
	if err := st.SetUserContext("currency_display_preference", "1000원 기준으로 표시", "runner"); err != nil {
		t.Fatal(err)
	}
	pref, ok := exchangeRateDisplayPreferenceCandidate(&Session{Store: st})
	if !ok {
		t.Fatal("expected legacy currency display memory to become a preference candidate")
	}
	if pref.Base != "KRW" || pref.Unit != 1000 {
		t.Fatalf("pref = %+v, want KRW unit 1000", pref)
	}
}

func TestPreferenceConfirmationBranchAcceptsExchangeRateDisplayPreference(t *testing.T) {
	state := NewPipelineState()
	st := openTestStore(t)
	pref := `{"base":"KRW","unit":1000,"reason":"사용자가 원화는 보통 1000원 기준이라고 반복해서 정정함"}`
	state.RecordPendingPreferenceConfirmation(PendingPreferenceConfirmation{
		Key:   "exchange_rate.display",
		Value: pref,
	})
	if err := st.SetUserContext("preference_pending_confirmation:exchange_rate.display", pref, "test"); err != nil {
		t.Fatal(err)
	}
	sess := &Session{Pipeline: state, Store: st}

	intent := classifyIntent("네네", state, sess)
	if intent.Kind != IntentPreferenceConfirmation {
		t.Fatalf("intent = %v, want preference confirmation", intent.Kind)
	}
	out, err := (&PreferenceConfirmationBranch{}).Execute(context.Background(), sess, webChatEvent("네네"), intent)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, "앞으로 환율은 1,000 KRW 기준으로 보여드릴게요") {
		t.Fatalf("out = %q, want acceptance acknowledgement", out)
	}
	got, ok, err := st.GetUserContext("preference:exchange_rate.display")
	if err != nil || !ok {
		t.Fatalf("accepted preference missing: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(got, `"base":"KRW"`) || !strings.Contains(got, `"unit":1000`) {
		t.Fatalf("preference = %q, want KRW unit 1000", got)
	}
	if _, ok, _ := st.GetUserContext("preference_pending_confirmation:exchange_rate.display"); ok {
		t.Fatal("pending confirmation was not cleared")
	}
}

func TestPreferenceConfirmationDoesNotStealRecentSkillConsent(t *testing.T) {
	state := NewPipelineState()
	state.RecordSkillSearch([]core.RegistryEntry{
		{ID: "weather-now", Name: "현재 날씨", Description: "현재 날씨를 확인합니다."},
	})
	st := openTestStore(t)
	if err := st.SetUserContext(
		"preference_pending_confirmation:exchange_rate.display",
		`{"base":"KRW","unit":1000}`,
		"test",
	); err != nil {
		t.Fatal(err)
	}

	intent := classifyIntent("네", state, &Session{Pipeline: state, Store: st})
	if intent.Kind != IntentInstallConsentReply {
		t.Fatalf("intent = %v, want recent skill consent to win over preference confirmation", intent.Kind)
	}
}

func TestSelectRecentSkillCandidate_CurrentWeather(t *testing.T) {
	entries := []core.RegistryEntry{
		{ID: "weather-briefing", Name: "아침 날씨 요약", Description: "매일 아침 날씨를 확인하고 텔레그램으로 보내줍니다."},
		{ID: "weather-now", Name: "현재 날씨", Description: "wttr.in으로 현재 날씨를 즉답합니다."},
	}
	got, ok := selectRecentSkillCandidate("스킬. 현재 날씨.", entries)
	if !ok {
		t.Fatal("expected candidate selection")
	}
	if got.ID != "weather-now" {
		t.Fatalf("got %s, want weather-now", got.ID)
	}
}

func TestClassifyIntent_CandidateNameReplyRoutesToInstallConsent(t *testing.T) {
	state := NewPipelineState()
	state.RecordSkillSearch([]core.RegistryEntry{
		{ID: "weather-briefing", Name: "아침 날씨 요약", Description: "매일 아침 날씨를 확인하고 텔레그램으로 보내줍니다."},
		{ID: "weather-now", Name: "현재 날씨", Description: "wttr.in으로 현재 날씨를 즉답합니다."},
	})

	intent := classifyIntent("스킬. 현재 날씨.", state, nil)
	if intent.Kind != IntentInstallConsentReply {
		t.Fatalf("expected install consent route, got %v", intent.Kind)
	}
	if got, _ := intent.Params["skill_id"].(string); got != "weather-now" {
		t.Fatalf("skill_id = %q, want weather-now", got)
	}
}

func TestClassifyIntent_RecommendationDoesNotBrowseSkills(t *testing.T) {
	intent := classifyIntent("요즘 드라마 추천해줘", nil, nil)
	if intent.Kind != IntentLegacyFallback {
		t.Fatalf("intent = %v, want legacy fallback so LLM can judge search need", intent.Kind)
	}
}

func TestClassifyIntent_ExplicitSkillBrowseStillBrowses(t *testing.T) {
	cases := []string{
		"어떤 스킬 있어?",
		"스킬 목록 보여줘",
		"사용 가능한 기능 뭐야?",
	}
	for _, in := range cases {
		intent := classifyIntent(in, nil, nil)
		if intent.Kind != IntentBrowse {
			t.Fatalf("%q intent = %v, want browse", in, intent.Kind)
		}
	}
}

func TestClassifyIntent_SkillDeletionDoesNotBrowse(t *testing.T) {
	cases := []string{
		"너 설치된 날씨 스킬들 다 지워버려요. 일단.",
		"weather-now 스킬 삭제해줘",
		"설치된 스킬 제거해줘",
	}
	for _, in := range cases {
		intent := classifyIntent(in, nil, nil)
		if intent.Kind == IntentBrowse {
			t.Fatalf("%q routed to browse; deletion requests must reach the LLM/tool path", in)
		}
	}
}

func TestClassifyIntent_AffirmativeConfirmsPendingClarification(t *testing.T) {
	state := NewPipelineState()
	state.RecordPendingClarification(PendingClarification{
		Kind:  "exchange_rate",
		Query: "달러",
	})

	intent := classifyIntent("ㅇㅇ", state, nil)
	if intent.Kind != IntentConfirmClarification {
		t.Fatalf("expected IntentConfirmClarification, got %v", intent.Kind)
	}
	if got, _ := intent.Params["kind"].(string); got != "exchange_rate" {
		t.Fatalf("kind = %q, want exchange_rate", got)
	}
}

func TestClassifyIntent_ExchangeRateQueryUsesDeterministicBranch(t *testing.T) {
	intent := classifyIntent("환율.", NewPipelineState(), &Session{})
	if intent.Kind != IntentExchangeRateLookup {
		t.Fatalf("expected IntentExchangeRateLookup, got %v", intent.Kind)
	}
}

func TestClassifyIntent_WeatherNowQueryUsesDeterministicBranch(t *testing.T) {
	intent := classifyIntent("강남역에 비오나? 지금?", NewPipelineState(), &Session{})
	if intent.Kind != IntentWeatherNowLookup {
		t.Fatalf("expected IntentWeatherNowLookup, got %v", intent.Kind)
	}
}

func TestClassifyIntent_LocationReplyConfirmsPendingWeatherLocation(t *testing.T) {
	state := NewPipelineState()
	state.RecordPendingClarification(PendingClarification{
		Kind:  "weather_now_location",
		Query: "강남역이 비오나? 지금?",
	})

	intent := classifyIntent("강남역이요.", state, &Session{})
	if intent.Kind != IntentConfirmClarification {
		t.Fatalf("expected IntentConfirmClarification, got %v", intent.Kind)
	}
	if got, _ := intent.Params["kind"].(string); got != "weather_now_location" {
		t.Fatalf("kind = %q, want weather_now_location", got)
	}
}

func TestExchangeRateLookupResponseFramesSearchHitsAsCandidates(t *testing.T) {
	results := []WebSearchResult{
		{Title: "USD KRW | 미달러 원 환율 - Investing.com - USD/KRW", URL: "https://kr.investing.com/currencies/usd-krw", Snippet: "USD/KRW"},
		{Title: "USD/KRW — 환율 — TradingView - 실시간", URL: "https://kr.tradingview.com/symbols/USDKRW/", Snippet: "실시간"},
		{Title: "실시간 달러 환율 (Usd/Krw) | 알파스퀘어 - 엔비디아 NVDA 가상화폐 비트코인 KRW", URL: "https://alphasquare.co.kr/exchange/usd-krw", Snippet: "엔비디아 NVDA 가상화폐 비트코인 KRW"},
	}

	out := formatExchangeRateLookupResponse(results, exchangeRateRegistryEntry())

	for _, want := range []string{"웹 검색에서 환율 관련 페이지를 찾았습니다:", "제가 보기엔", "Investing.com", "TradingView", "알파스퀘어", "설치하면", "바로"} {
		if !strings.Contains(out, want) {
			t.Fatalf("response missing %q:\n%s", want, out)
		}
	}
	for _, banned := range []string{"확인한 출처:", "다음 단계:"} {
		if strings.Contains(out, banned) {
			t.Fatalf("response should not use mechanical/overclaimed label %q:\n%s", banned, out)
		}
	}
	if strings.Contains(out, "엔비디아 NVDA 가상화폐 비트코인") {
		t.Fatalf("response leaked noisy raw snippet:\n%s", out)
	}
	if strings.Contains(out, "USD KRW | 미달러 원 환율 - Investing.com - USD/KRW") {
		t.Fatalf("response leaked raw title:\n%s", out)
	}
	if strings.Index(out, "웹 검색에서") > strings.Index(out, "제가 보기엔") {
		t.Fatalf("search-candidate section must come before suggestion:\n%s", out)
	}
}

func TestExchangeRateLookupBranchCachesActionableSkillOffer(t *testing.T) {
	state := NewPipelineState()
	sess := &Session{Pipeline: state}

	out, err := (&ExchangeRateLookupBranch{}).Execute(context.Background(), sess, core.Event{}, Intent{
		Kind: IntentExchangeRateLookup,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	for _, want := range []string{"제가 보기엔", "환율 조회", "설치하면", "바로"} {
		if !strings.Contains(out, want) {
			t.Fatalf("response missing %q:\n%s", want, out)
		}
	}
	for _, banned := range []string{"확인한 출처:", "다음 단계:"} {
		if strings.Contains(out, banned) {
			t.Fatalf("response should not use mechanical/overclaimed label %q:\n%s", banned, out)
		}
	}
	if got := state.RecentSkillSearch(); len(got) != 1 || got[0].ID != "exchange-rate" {
		t.Fatalf("expected exchange-rate candidate cached, got %+v", got)
	}
}

func TestWeatherNowLookupBranchCachesOnlyCurrentWeatherOffer(t *testing.T) {
	state := NewPipelineState()
	sess := &Session{Pipeline: state}

	out, err := (&WeatherNowLookupBranch{}).Execute(context.Background(), sess, core.Event{}, Intent{
		Kind: IntentWeatherNowLookup,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	for _, want := range []string{"현재 날씨", "비 여부", "설치하면", "바로"} {
		if !strings.Contains(out, want) {
			t.Fatalf("response missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "아침 날씨") {
		t.Fatalf("response must not suggest scheduled briefing for current weather:\n%s", out)
	}
	if got := state.RecentSkillSearch(); len(got) != 1 || got[0].ID != "weather-now" {
		t.Fatalf("expected weather-now candidate cached, got %+v", got)
	}
}

func TestWeatherNowRegistryEntryUsesPublicDisplayName(t *testing.T) {
	entry := weatherNowRegistryEntry()

	if entry.ID != "weather-now" {
		t.Fatalf("ID = %q, want weather-now", entry.ID)
	}
	if entry.Name != "날씨 조회" {
		t.Fatalf("Name = %q, want 날씨 조회", entry.Name)
	}
}

func TestWeatherNowLookupBranchRunsInstalledPackageByID(t *testing.T) {
	skipWithoutRuntime(t)

	geo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"lat":37.4979,"lon":127.0276,"name_matched":"강남역"}`))
	}))
	defer geo.Close()
	oldBaseURL := kittypawAPIBaseURL
	kittypawAPIBaseURL = geo.URL
	t.Cleanup(func() { kittypawAPIBaseURL = oldBaseURL })

	baseDir := t.TempDir()
	pm := installTestPackage(t, baseDir, `
[meta]
id = "weather-now"
name = "기상 조회"
version = "1.0.0"
description = "강수 상태를 즉답합니다."

[permissions]
primitives = []
context = ["location"]
`, `
const ctx = JSON.parse(__context__);
return JSON.stringify(ctx.user && ctx.user.location);
`)

	cfg := core.DefaultConfig()
	sess := &Session{
		Provider:       &mockProvider{responses: []*llm.Response{mockResp(`{"location_query":"강남역"}`)}},
		Sandbox:        sandbox.New(cfg.Sandbox),
		Config:         &cfg,
		PackageManager: pm,
		BaseDir:        baseDir,
		Pipeline:       NewPipelineState(),
	}

	out, err := (&WeatherNowLookupBranch{}).Execute(context.Background(), sess, webChatEvent("강남역에 비오나? 지금?"), Intent{
		Kind: IntentWeatherNowLookup,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	var loc struct {
		City string  `json:"city"`
		Lat  float64 `json:"lat"`
		Lon  float64 `json:"lon"`
	}
	if err := json.Unmarshal([]byte(out), &loc); err != nil {
		t.Fatalf("expected installed weather-now package output, got %q: %v", out, err)
	}
	if loc.City != "강남역" || loc.Lat != 37.4979 || loc.Lon != 127.0276 {
		t.Fatalf("location = %+v, want structured 강남역", loc)
	}
}

func TestWeatherNowLookupBranchResolvesLocationBeforeInstalledPackage(t *testing.T) {
	skipWithoutRuntime(t)

	geo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/geo/resolve" {
			t.Fatalf("unexpected geo path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("q"); got != "강남역" {
			t.Fatalf("geo query = %q, want 강남역", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"lat":37.4979,"lon":127.0276,"name_matched":"강남역"}`))
	}))
	defer geo.Close()
	oldBaseURL := kittypawAPIBaseURL
	kittypawAPIBaseURL = geo.URL
	t.Cleanup(func() { kittypawAPIBaseURL = oldBaseURL })

	baseDir := t.TempDir()
	pm := installTestPackage(t, baseDir, `
[meta]
id = "weather-now"
name = "현재 날씨"
version = "1.0.0"
description = "현재 날씨와 비 여부를 즉답합니다."

[permissions]
primitives = []
context = ["location"]
`, `
const ctx = JSON.parse(__context__);
return JSON.stringify(ctx.user && ctx.user.location);
`)

	cfg := core.DefaultConfig()
	sess := &Session{
		Provider:       &mockProvider{responses: []*llm.Response{mockResp(`{"location_query":"강남역"}`)}},
		Sandbox:        sandbox.New(cfg.Sandbox),
		Config:         &cfg,
		PackageManager: pm,
		BaseDir:        baseDir,
		Pipeline:       NewPipelineState(),
	}

	out, err := (&WeatherNowLookupBranch{}).Execute(context.Background(), sess, webChatEvent("강남역에 비오나? 지금?"), Intent{
		Kind: IntentWeatherNowLookup,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	var loc struct {
		City string  `json:"city"`
		Lat  float64 `json:"lat"`
		Lon  float64 `json:"lon"`
	}
	if err := json.Unmarshal([]byte(out), &loc); err != nil {
		t.Fatalf("parse package output: %v (out: %s)", err, out)
	}
	if loc.City != "강남역" || loc.Lat != 37.4979 || loc.Lon != 127.0276 {
		t.Fatalf("location = %+v, want structured 강남역", loc)
	}
}

func TestWeatherNowLookupBranchUsesLocationFollowupText(t *testing.T) {
	skipWithoutRuntime(t)

	geo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "장안동" {
			t.Fatalf("geo query = %q, want 장안동", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"lat":37.5681,"lon":127.0719,"name_matched":"장안동"}`))
	}))
	defer geo.Close()
	oldBaseURL := kittypawAPIBaseURL
	kittypawAPIBaseURL = geo.URL
	t.Cleanup(func() { kittypawAPIBaseURL = oldBaseURL })

	baseDir := t.TempDir()
	pm := installTestPackage(t, baseDir, `
[meta]
id = "weather-now"
name = "현재 날씨"
version = "1.0.0"
description = "현재 날씨와 비 여부를 즉답합니다."

[permissions]
primitives = []
context = ["location"]
`, `
const ctx = JSON.parse(__context__);
return JSON.stringify(ctx.user && ctx.user.location);
`)

	cfg := core.DefaultConfig()
	sess := &Session{
		Provider:       &mockProvider{responses: []*llm.Response{mockResp("장안동")}},
		Sandbox:        sandbox.New(cfg.Sandbox),
		Config:         &cfg,
		PackageManager: pm,
		BaseDir:        baseDir,
		Pipeline:       NewPipelineState(),
	}
	sess.Pipeline.RecordSkillOutputForSkill("weather-now", "강남역 현재 날씨\n1시간 강수: 없음")

	out, err := (&WeatherNowLookupBranch{}).Execute(context.Background(), sess, webChatEvent("장안동은?"), Intent{
		Kind: IntentWeatherNowLookup,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	var loc struct {
		City string  `json:"city"`
		Lat  float64 `json:"lat"`
		Lon  float64 `json:"lon"`
	}
	if err := json.Unmarshal([]byte(out), &loc); err != nil {
		t.Fatalf("parse package output: %v (out: %s)", err, out)
	}
	if loc.City != "장안동" || loc.Lat != 37.5681 || loc.Lon != 127.0719 {
		t.Fatalf("location = %+v, want structured 장안동", loc)
	}
}

func TestAmbiguousFollowupBranchWeatherNowHighConfidenceRuns(t *testing.T) {
	skipWithoutRuntime(t)

	geo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "장안동" {
			t.Fatalf("geo query = %q, want 장안동", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"lat":37.5681,"lon":127.0719,"name_matched":"장안동"}`))
	}))
	defer geo.Close()
	oldBaseURL := kittypawAPIBaseURL
	kittypawAPIBaseURL = geo.URL
	t.Cleanup(func() { kittypawAPIBaseURL = oldBaseURL })

	baseDir := t.TempDir()
	pm := installTestPackage(t, baseDir, `
[meta]
id = "weather-now"
name = "현재 날씨"
version = "1.0.0"
description = "현재 날씨와 비 여부를 즉답합니다."

[permissions]
primitives = []
context = ["location"]
`, `
const ctx = JSON.parse(__context__);
return JSON.stringify(ctx.user && ctx.user.location);
`)

	cfg := core.DefaultConfig()
	state := NewPipelineState()
	state.RecordSkillOutputForSkill("weather-now", "강남역 현재 날씨\n1시간 강수: 없음")
	sess := &Session{
		Provider:       &mockProvider{responses: []*llm.Response{mockResp(`{"intent":"weather_now","confidence":0.91,"location_query":"장안동"}`)}},
		Sandbox:        sandbox.New(cfg.Sandbox),
		Config:         &cfg,
		PackageManager: pm,
		BaseDir:        baseDir,
		Pipeline:       state,
	}

	out, err := (&AmbiguousFollowupBranch{}).Execute(context.Background(), sess, webChatEvent("장안동은?"), Intent{
		Kind: IntentAmbiguousFollowup,
		Params: map[string]any{
			"skill_id":   "weather-now",
			"raw_output": state.RecentSkillOutput(),
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	var loc struct {
		City string  `json:"city"`
		Lat  float64 `json:"lat"`
		Lon  float64 `json:"lon"`
	}
	if err := json.Unmarshal([]byte(out), &loc); err != nil {
		t.Fatalf("parse package output: %v (out: %s)", err, out)
	}
	if loc.City != "장안동" || loc.Lat != 37.5681 || loc.Lon != 127.0719 {
		t.Fatalf("location = %+v, want structured 장안동", loc)
	}
}

func TestAmbiguousFollowupBranchNormalizesIntentCase(t *testing.T) {
	skipWithoutRuntime(t)

	geo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "장안동" {
			t.Fatalf("geo query = %q, want 장안동", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"lat":37.5681,"lon":127.0719,"name_matched":"장안동"}`))
	}))
	defer geo.Close()
	oldBaseURL := kittypawAPIBaseURL
	kittypawAPIBaseURL = geo.URL
	t.Cleanup(func() { kittypawAPIBaseURL = oldBaseURL })

	baseDir := t.TempDir()
	pm := installTestPackage(t, baseDir, `
[meta]
id = "weather-now"
name = "현재 날씨"
version = "1.0.0"
description = "현재 날씨와 비 여부를 즉답합니다."

[permissions]
primitives = []
context = ["location"]
`, `
const ctx = JSON.parse(__context__);
return JSON.stringify(ctx.user && ctx.user.location);
`)

	cfg := core.DefaultConfig()
	state := NewPipelineState()
	state.RecordSkillOutputForSkill("weather-now", "강남역 현재 날씨\n1시간 강수: 없음")
	sess := &Session{
		Provider:       &mockProvider{responses: []*llm.Response{mockResp(`{"intent":"Weather-Now","confidence":0.91,"location_query":"장안동"}`)}},
		Sandbox:        sandbox.New(cfg.Sandbox),
		Config:         &cfg,
		PackageManager: pm,
		BaseDir:        baseDir,
		Pipeline:       state,
	}

	out, err := (&AmbiguousFollowupBranch{}).Execute(context.Background(), sess, webChatEvent("장안동은?"), Intent{
		Kind: IntentAmbiguousFollowup,
		Params: map[string]any{
			"skill_id":   "weather-now",
			"raw_output": state.RecentSkillOutput(),
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, "장안동") {
		t.Fatalf("expected weather-now output for normalized intent, got %q", out)
	}
}

func TestAmbiguousFollowupBranchLowConfidenceAsksConfirmation(t *testing.T) {
	state := NewPipelineState()
	state.RecordSkillOutputForSkill("weather-now", "강남역 현재 날씨\n1시간 강수: 없음")
	sess := &Session{
		Provider: &mockProvider{responses: []*llm.Response{mockResp(`{"intent":"weather_now","confidence":0.42,"location_query":"장안동"}`)}},
		Pipeline: state,
	}

	out, err := (&AmbiguousFollowupBranch{}).Execute(context.Background(), sess, webChatEvent("장안동은?"), Intent{
		Kind: IntentAmbiguousFollowup,
		Params: map[string]any{
			"skill_id":   "weather-now",
			"raw_output": state.RecentSkillOutput(),
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	for _, want := range []string{"장안동", "날씨"} {
		if !strings.Contains(out, want) {
			t.Fatalf("confirmation missing %q: %q", want, out)
		}
	}
	pending, ok := state.RecentPendingClarification()
	if !ok {
		t.Fatal("expected pending clarification")
	}
	if pending.Kind != "weather_now_location" || pending.Query != "장안동" {
		t.Fatalf("pending = %+v, want weather_now_location 장안동", pending)
	}
}

func TestWeatherNowLookupBranchRecoversLocationWhenSlotLLMReturnsProse(t *testing.T) {
	skipWithoutRuntime(t)

	geo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/geo/resolve" {
			t.Fatalf("unexpected geo path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("q"); got != "강남역" {
			t.Fatalf("geo query = %q, want 강남역", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"lat":37.4979,"lon":127.0276,"name_matched":"강남역"}`))
	}))
	defer geo.Close()
	oldBaseURL := kittypawAPIBaseURL
	kittypawAPIBaseURL = geo.URL
	t.Cleanup(func() { kittypawAPIBaseURL = oldBaseURL })

	baseDir := t.TempDir()
	pm := installTestPackage(t, baseDir, `
[meta]
id = "weather-now"
name = "현재 날씨"
version = "1.0.0"
description = "현재 날씨와 비 여부를 즉답합니다."

[permissions]
primitives = []
context = ["location"]
`, `
const ctx = JSON.parse(__context__);
return JSON.stringify(ctx.user && ctx.user.location);
`)

	cfg := core.DefaultConfig()
	sess := &Session{
		Provider:       &mockProvider{responses: []*llm.Response{mockResp("사용자의 질문을 확인해보니 강남역의 현재 날씨를 문의하신 것 같습니다.")}},
		Sandbox:        sandbox.New(cfg.Sandbox),
		Config:         &cfg,
		PackageManager: pm,
		BaseDir:        baseDir,
		Pipeline:       NewPipelineState(),
	}

	out, err := (&WeatherNowLookupBranch{}).Execute(context.Background(), sess, webChatEvent("강남역이 비오나? 지금?"), Intent{
		Kind: IntentWeatherNowLookup,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	var loc struct {
		City string  `json:"city"`
		Lat  float64 `json:"lat"`
		Lon  float64 `json:"lon"`
	}
	if err := json.Unmarshal([]byte(out), &loc); err != nil {
		t.Fatalf("parse package output: %v (out: %s)", err, out)
	}
	if loc.City != "강남역" || loc.Lat != 37.4979 || loc.Lon != 127.0276 {
		t.Fatalf("location = %+v, want structured 강남역", loc)
	}
}

func TestWeatherNowLookupBranchDoesNotRunDefaultWhenExplicitLocationResolutionFails(t *testing.T) {
	skipWithoutRuntime(t)

	geo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "geo down", http.StatusBadGateway)
	}))
	defer geo.Close()
	oldBaseURL := kittypawAPIBaseURL
	kittypawAPIBaseURL = geo.URL
	t.Cleanup(func() { kittypawAPIBaseURL = oldBaseURL })

	baseDir := t.TempDir()
	pm := installTestPackage(t, baseDir, `
[meta]
id = "weather-now"
name = "현재 날씨"
version = "1.0.0"
description = "현재 날씨와 비 여부를 즉답합니다."

[permissions]
primitives = []
`, `
return "SHOULD_NOT_RUN";
`)

	cfg := core.DefaultConfig()
	sess := &Session{
		Provider:       &mockProvider{responses: []*llm.Response{mockResp(`{"location_query":"강남역"}`)}},
		Sandbox:        sandbox.New(cfg.Sandbox),
		Config:         &cfg,
		PackageManager: pm,
		BaseDir:        baseDir,
		Pipeline:       NewPipelineState(),
	}

	out, err := (&WeatherNowLookupBranch{}).Execute(context.Background(), sess, webChatEvent("강남역에 비오나? 지금?"), Intent{
		Kind: IntentWeatherNowLookup,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if strings.Contains(out, "SHOULD_NOT_RUN") {
		t.Fatalf("package default must not run after explicit location resolution failure: %s", out)
	}
	for _, want := range []string{"강남역", "위치", "확인"} {
		if !strings.Contains(out, want) {
			t.Fatalf("response missing %q: %s", want, out)
		}
	}
}

func TestWeatherNowLookupBranchRecordsPendingLocationWhenSlotExtractionFails(t *testing.T) {
	state := NewPipelineState()
	sess := &Session{
		Provider: &mockProvider{responses: []*llm.Response{mockResp(`{"location_query":`)}},
		Pipeline: state,
	}

	out, err := (&WeatherNowLookupBranch{}).Execute(context.Background(), sess, webChatEvent("강남역이 비오나? 지금?"), Intent{
		Kind: IntentWeatherNowLookup,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, "위치") {
		t.Fatalf("expected location clarification, got %q", out)
	}
	pending, ok := state.RecentPendingClarification()
	if !ok {
		t.Fatal("expected pending weather location clarification")
	}
	if pending.Kind != "weather_now_location" || pending.Query != "강남역이 비오나? 지금?" {
		t.Fatalf("pending = %+v", pending)
	}
}

func TestConfirmClarificationBranchWeatherLocationReplyRunsInstalledSkill(t *testing.T) {
	skipWithoutRuntime(t)

	geo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"lat":37.4979,"lon":127.0276,"name_matched":"강남역"}`))
	}))
	defer geo.Close()
	oldBaseURL := kittypawAPIBaseURL
	kittypawAPIBaseURL = geo.URL
	t.Cleanup(func() { kittypawAPIBaseURL = oldBaseURL })

	state := NewPipelineState()
	state.RecordPendingClarification(PendingClarification{
		Kind:  "weather_now_location",
		Query: "강남역이 비오나? 지금?",
	})
	baseDir := t.TempDir()
	pm := installTestPackage(t, baseDir, `[meta]
id = "weather-now"
name = "현재 날씨"
version = "1.0.0"
description = "현재 날씨와 비 여부를 즉답합니다."

[permissions]
primitives = []
context = ["location"]
`, `
const ctx = JSON.parse(__context__);
return JSON.stringify(ctx.user && ctx.user.location);
`)
	cfg := core.DefaultConfig()
	sess := &Session{
		Provider:       &mockProvider{responses: []*llm.Response{mockResp(`{"location_query":"강남역"}`)}},
		Pipeline:       state,
		PackageManager: pm,
		Sandbox:        sandbox.New(cfg.Sandbox),
		Config:         &cfg,
	}

	out, err := (&ConfirmClarificationBranch{}).Execute(context.Background(), sess, webChatEvent("강남역이요."), Intent{
		Kind: IntentConfirmClarification,
		Params: map[string]any{
			"kind": "weather_now_location",
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	var loc struct {
		City string  `json:"city"`
		Lat  float64 `json:"lat"`
		Lon  float64 `json:"lon"`
	}
	if err := json.Unmarshal([]byte(out), &loc); err != nil {
		t.Fatalf("parse output: %v (out: %s)", err, out)
	}
	if loc.City != "강남역" || loc.Lat != 37.4979 || loc.Lon != 127.0276 {
		t.Fatalf("location = %+v, want structured 강남역", loc)
	}
}

func TestConfirmClarificationBranchWeatherLocationReplyFallsBackToPendingQuery(t *testing.T) {
	skipWithoutRuntime(t)

	geo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "강남역" {
			t.Fatalf("geo query = %q, want 강남역", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"lat":37.4979,"lon":127.0276,"name_matched":"강남역"}`))
	}))
	defer geo.Close()
	oldBaseURL := kittypawAPIBaseURL
	kittypawAPIBaseURL = geo.URL
	t.Cleanup(func() { kittypawAPIBaseURL = oldBaseURL })

	state := NewPipelineState()
	state.RecordPendingClarification(PendingClarification{
		Kind:  "weather_now_location",
		Query: "강남역이 비오나? 지금?",
	})
	baseDir := t.TempDir()
	pm := installTestPackage(t, baseDir, `[meta]
id = "weather-now"
name = "현재 날씨"
version = "1.0.0"
description = "현재 날씨와 비 여부를 즉답합니다."

[permissions]
primitives = []
context = ["location"]
`, `
const ctx = JSON.parse(__context__);
return JSON.stringify(ctx.user && ctx.user.location);
`)
	cfg := core.DefaultConfig()
	sess := &Session{
		Provider: &mockProvider{responses: []*llm.Response{
			mockResp(`{"location_query":""}`),
			mockResp(`{"location_query":"강남역"}`),
		}},
		Pipeline:       state,
		PackageManager: pm,
		Sandbox:        sandbox.New(cfg.Sandbox),
		Config:         &cfg,
	}

	out, err := (&ConfirmClarificationBranch{}).Execute(context.Background(), sess, webChatEvent("비오냐고 지금"), Intent{
		Kind: IntentConfirmClarification,
		Params: map[string]any{
			"kind": "weather_now_location",
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	var loc struct {
		City string  `json:"city"`
		Lat  float64 `json:"lat"`
		Lon  float64 `json:"lon"`
	}
	if err := json.Unmarshal([]byte(out), &loc); err != nil {
		t.Fatalf("parse output: %v (out: %s)", err, out)
	}
	if loc.City != "강남역" || loc.Lat != 37.4979 || loc.Lon != 127.0276 {
		t.Fatalf("location = %+v, want structured 강남역", loc)
	}
}

func TestWeatherNowLookupBranchCachesPendingStructuredParamsForInstall(t *testing.T) {
	geo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"lat":37.4979,"lon":127.0276,"name_matched":"강남역"}`))
	}))
	defer geo.Close()
	oldBaseURL := kittypawAPIBaseURL
	kittypawAPIBaseURL = geo.URL
	t.Cleanup(func() { kittypawAPIBaseURL = oldBaseURL })

	state := NewPipelineState()
	cfg := core.DefaultConfig()
	sess := &Session{
		Provider: &mockProvider{responses: []*llm.Response{mockResp(`{"location_query":"강남역"}`)}},
		Config:   &cfg,
		Pipeline: state,
	}

	out, err := (&WeatherNowLookupBranch{}).Execute(context.Background(), sess, webChatEvent("강남역에 비오나? 지금?"), Intent{
		Kind: IntentWeatherNowLookup,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, "설치") {
		t.Fatalf("expected install offer, got %q", out)
	}
	params, ok := state.RecentPendingSkillRun("weather-now")
	if !ok {
		t.Fatal("expected pending weather-now params")
	}
	loc, ok := params["location"].(map[string]any)
	if !ok {
		t.Fatalf("location params missing: %+v", params)
	}
	if loc["label"] != "강남역" || loc["lat"] != 37.4979 || loc["lon"] != 127.0276 {
		t.Fatalf("location params = %+v, want 강남역 coords", loc)
	}
}

func TestConfirmClarificationBranchWeatherNowRunsInstalledWithStructuredParams(t *testing.T) {
	skipWithoutRuntime(t)

	geo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"lat":37.4979,"lon":127.0276,"name_matched":"강남역"}`))
	}))
	defer geo.Close()
	oldBaseURL := kittypawAPIBaseURL
	kittypawAPIBaseURL = geo.URL
	t.Cleanup(func() { kittypawAPIBaseURL = oldBaseURL })

	state := NewPipelineState()
	state.RecordPendingClarification(PendingClarification{
		Kind:  "weather_now",
		Query: "강남역에 비오나",
	})
	baseDir := t.TempDir()
	pm := installTestPackage(t, baseDir, `[meta]
id = "weather-now"
name = "현재 날씨"
version = "1.0.0"
description = "현재 날씨와 비 여부를 즉답합니다."

[permissions]
primitives = []
context = ["location"]
`, `
const ctx = JSON.parse(__context__);
return JSON.stringify(ctx.user && ctx.user.location);
`)
	cfg := core.DefaultConfig()
	sess := &Session{
		Provider:       &mockProvider{responses: []*llm.Response{mockResp(`{"location_query":"강남역"}`)}},
		Pipeline:       state,
		PackageManager: pm,
		Sandbox:        sandbox.New(cfg.Sandbox),
		Config:         &cfg,
	}

	out, err := (&ConfirmClarificationBranch{}).Execute(context.Background(), sess, core.Event{}, Intent{
		Kind: IntentConfirmClarification,
		Params: map[string]any{
			"kind": "weather_now",
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	var loc struct {
		City string  `json:"city"`
		Lat  float64 `json:"lat"`
		Lon  float64 `json:"lon"`
	}
	if err := json.Unmarshal([]byte(out), &loc); err != nil {
		t.Fatalf("parse output: %v (out: %s)", err, out)
	}
	if loc.City != "강남역" || loc.Lat != 37.4979 || loc.Lon != 127.0276 {
		t.Fatalf("location = %+v, want structured 강남역", loc)
	}
}

func TestDetectPendingClarification_ExchangeRate(t *testing.T) {
	pending, ok := detectPendingClarification("달러", "환율 말씀이세요? 맞으면 지금 기준으로 찾아볼게요.")
	if !ok {
		t.Fatal("expected pending clarification")
	}
	if pending.Kind != "exchange_rate" || pending.Query != "달러" {
		t.Fatalf("pending = %+v", pending)
	}
}

func TestDetectPendingClarification_WeatherNow(t *testing.T) {
	pending, ok := detectPendingClarification("장한평역에 비오나", "날씨 말씀이세요? 맞으면 지금 기준으로 확인할까요?")
	if !ok {
		t.Fatal("expected pending clarification")
	}
	if pending.Kind != "weather_now" || pending.Query != "장한평역에 비오나" {
		t.Fatalf("pending = %+v", pending)
	}
}

func TestConfirmClarificationBranch_ExchangeRateOffersActionableSkill(t *testing.T) {
	state := NewPipelineState()
	state.RecordPendingClarification(PendingClarification{
		Kind:  "exchange_rate",
		Query: "달러",
	})
	sess := &Session{Pipeline: state}

	out, err := (&ConfirmClarificationBranch{}).Execute(context.Background(), sess, core.Event{}, Intent{
		Kind: IntentConfirmClarification,
		Params: map[string]any{
			"kind": "exchange_rate",
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	for _, want := range []string{"환율 조회", "설치하면", "바로", "조회"} {
		if !strings.Contains(out, want) {
			t.Fatalf("offer missing %q: %q", want, out)
		}
	}
	if got := state.RecentSkillSearch(); len(got) != 1 || got[0].ID != "exchange-rate" {
		t.Fatalf("expected exchange-rate candidate cached, got %+v", got)
	}
}

func TestConfirmClarificationBranch_WeatherNowOffersActionableSkill(t *testing.T) {
	state := NewPipelineState()
	state.RecordPendingClarification(PendingClarification{
		Kind:  "weather_now",
		Query: "장한평역에 비오나",
	})
	sess := &Session{Pipeline: state}

	out, err := (&ConfirmClarificationBranch{}).Execute(context.Background(), sess, core.Event{}, Intent{
		Kind: IntentConfirmClarification,
		Params: map[string]any{
			"kind": "weather_now",
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	for _, want := range []string{"현재 날씨", "설치하면", "비 여부", "바로"} {
		if !strings.Contains(out, want) {
			t.Fatalf("offer missing %q: %q", want, out)
		}
	}
	if got := state.RecentSkillSearch(); len(got) != 1 || got[0].ID != "weather-now" {
		t.Fatalf("expected weather-now candidate cached, got %+v", got)
	}
}

func TestConfirmClarificationBranch_ExchangeRateRunsInstalledSkill(t *testing.T) {
	skipWithoutRuntime(t)

	state := NewPipelineState()
	state.RecordPendingClarification(PendingClarification{
		Kind:  "exchange_rate",
		Query: "달러",
	})
	baseDir := t.TempDir()
	pm := installTestPackage(t, baseDir, `[meta]
id = "exchange-rate"
name = "환율 조회"
version = "1.0.0"
description = "환율을 조회합니다."
`, `return "1 USD = 1477 KRW";`)
	cfg := core.DefaultConfig()
	sess := &Session{
		Pipeline:       state,
		PackageManager: pm,
		Sandbox:        sandbox.New(cfg.Sandbox),
		Config:         &cfg,
	}

	out, err := (&ConfirmClarificationBranch{}).Execute(context.Background(), sess, core.Event{}, Intent{
		Kind: IntentConfirmClarification,
		Params: map[string]any{
			"kind": "exchange_rate",
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if out != "1 USD = 1477 KRW" {
		t.Fatalf("out = %q, want skill output", out)
	}
}

func TestInstallConsentBranch_BareAffirmativeWithMultipleCandidatesAsksChoice(t *testing.T) {
	state := NewPipelineState()
	state.RecordSkillSearch([]core.RegistryEntry{
		{ID: "weather-briefing", Name: "아침 날씨 요약", Description: "매일 아침 날씨를 확인하고 텔레그램으로 보내줍니다."},
		{ID: "weather-now", Name: "현재 날씨", Description: "wttr.in으로 현재 날씨를 즉답합니다."},
	})
	sess := &Session{Pipeline: state}

	out, err := (&InstallConsentBranch{}).Execute(context.Background(), sess, core.Event{}, Intent{Kind: IntentInstallConsentReply})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, "현재 날씨") || !strings.Contains(out, "아침 날씨 요약") {
		t.Fatalf("choice prompt should list candidates, got %q", out)
	}
	if strings.Contains(out, "환경이 준비") {
		t.Fatalf("must ask for a candidate before checking installer environment, got %q", out)
	}
}

func TestRecordSkillOutput_RoundTrip(t *testing.T) {
	ps := NewPipelineState()
	if got := ps.RecentSkillOutput(); got != "" {
		t.Fatalf("fresh state must return empty, got %q", got)
	}
	ps.RecordSkillOutput("1 USD = 1477.04 KRW")
	if got := ps.RecentSkillOutput(); got != "1 USD = 1477.04 KRW" {
		t.Errorf("got %q, want roundtrip", got)
	}
}

func TestRecordSkillOutput_EmptyIgnored(t *testing.T) {
	ps := NewPipelineState()
	ps.RecordSkillOutput("first")
	ps.RecordSkillOutput("") // must not overwrite with empty
	if got := ps.RecentSkillOutput(); got != "first" {
		t.Errorf("empty record should be no-op, got %q", got)
	}
}

func TestQueryHasModifier_PositiveCases(t *testing.T) {
	cases := []string{
		"원화로 환율",
		"엔으로 환율",
		"기준으로 다시",
		"원화 기준의 환율",
		"간단히 환율",
		"자세히 알려줘",
		"다시 계산",
		"환산해줘",
		"USD에서 KRW로 변환",
		"원화기준... 으로요..",
	}
	for _, q := range cases {
		t.Run(q, func(t *testing.T) {
			if !queryHasModifier(q) {
				t.Errorf("expected modifier detection for %q", q)
			}
		})
	}
}

func TestQueryHasModifier_NegativeCases(t *testing.T) {
	cases := []string{
		"환율",
		"환율 알려줘",
		"오늘 환율",
		"내일 날씨",
		"엔화는?",
		"주식",
	}
	for _, q := range cases {
		t.Run(q, func(t *testing.T) {
			if queryHasModifier(q) {
				t.Errorf("unexpected modifier detection for %q (would deflect a fresh-retrieval query)", q)
			}
		})
	}
}

func TestMatchInstalledSkill_CurrentWeatherDoesNotRunScheduledBriefing(t *testing.T) {
	baseDir := t.TempDir()
	pm := installTestPackage(t, baseDir, `[meta]
id = "weather-briefing"
name = "매일 아침 날씨 브리핑"
version = "1.0.0"
description = "매일 아침 설정한 도시의 날씨 예보를 텔레그램으로 보내줍니다."
cron = "0 7 * * *"
`, `return "briefing";`)
	sess := &Session{PackageManager: pm}

	if got := matchInstalledSkill("현재 날씨", sess); got != nil {
		t.Fatalf("current weather query must not run scheduled briefing, got %s", got.Meta.ID)
	}
}

func TestMatchInstalledSkill_CurrentWeatherPrefersNowSkill(t *testing.T) {
	baseDir := t.TempDir()
	pm := installTestPackage(t, baseDir, `[meta]
id = "weather-briefing"
name = "매일 아침 날씨 브리핑"
version = "1.0.0"
description = "매일 아침 설정한 도시의 날씨 예보를 텔레그램으로 보내줍니다."
cron = "0 7 * * *"
`, `return "briefing";`)
	installTestPackage(t, baseDir, `[meta]
id = "weather-now"
name = "현재 날씨"
version = "1.0.0"
description = "지금 시점 현재 날씨와 비 여부를 즉답합니다."
`, `return "now";`)
	sess := &Session{PackageManager: pm}

	got := matchInstalledSkill("지금 강남역에 비오나? 현재 날씨", sess)
	if got == nil {
		t.Fatal("expected current weather query to match weather-now")
	}
	if got.Meta.ID != "weather-now" {
		t.Fatalf("got %s, want weather-now", got.Meta.ID)
	}
}

func TestMatchInstalledSkill_BriefingQueryMatchesScheduledBriefing(t *testing.T) {
	baseDir := t.TempDir()
	pm := installTestPackage(t, baseDir, `[meta]
id = "weather-briefing"
name = "매일 아침 날씨 브리핑"
version = "1.0.0"
description = "매일 아침 설정한 도시의 날씨 예보를 텔레그램으로 보내줍니다."
cron = "0 7 * * *"
`, `return "briefing";`)
	sess := &Session{PackageManager: pm}

	got := matchInstalledSkill("매일 아침 날씨 브리핑", sess)
	if got == nil {
		t.Fatal("expected briefing query to match weather-briefing")
	}
	if got.Meta.ID != "weather-briefing" {
		t.Fatalf("got %s, want weather-briefing", got.Meta.ID)
	}
}

func TestClearSkillOutput_RemovesCache(t *testing.T) {
	ps := NewPipelineState()
	ps.RecordSkillOutput("1 USD = 1477.04 KRW")
	if ps.RecentSkillOutput() == "" {
		t.Fatal("setup failed: cache empty after Record")
	}
	ps.ClearSkillOutput()
	if got := ps.RecentSkillOutput(); got != "" {
		t.Errorf("after Clear, cache must be empty, got %q", got)
	}
}

func TestClearSkillOutput_NilSafe(t *testing.T) {
	var ps *PipelineState
	ps.ClearSkillOutput() // must not panic
}

func TestRecordSkillOutput_NilSafe(t *testing.T) {
	var ps *PipelineState
	ps.RecordSkillOutput("x") // must not panic
	if got := ps.RecentSkillOutput(); got != "" {
		t.Errorf("nil ps must return empty, got %q", got)
	}
}

func TestAugmentSystemPromptWithRecentSkillOutput_AppendsBlock(t *testing.T) {
	ps := NewPipelineState()
	ps.RecordSkillOutput("1 USD = 1477.04 KRW")

	messages := []core.LlmMessage{
		{Role: core.RoleSystem, Content: "base prompt"},
		{Role: core.RoleUser, Content: "원화로 환율"},
	}
	augmentSystemPromptWithRecentSkillOutput(messages, "원화로 환율", ps)

	got := messages[0].Content
	if !strings.Contains(got, "Cross-turn context") {
		t.Errorf("system message must carry cross-turn block, got %q", got)
	}
	if !strings.Contains(got, "1 USD = 1477.04 KRW") {
		t.Errorf("system message must carry the cached skill output, got %q", got)
	}
	if !strings.HasPrefix(got, "base prompt") {
		t.Errorf("base prompt must be preserved as prefix, got %q", got)
	}
}

func TestAugmentSystemPromptWithRecentSkillOutput_NoOpWhenLong(t *testing.T) {
	ps := NewPipelineState()
	ps.RecordSkillOutput("data")

	long := strings.Repeat("긴 질문 ", 10) // > 30 runes
	messages := []core.LlmMessage{
		{Role: core.RoleSystem, Content: "base"},
	}
	augmentSystemPromptWithRecentSkillOutput(messages, long, ps)
	if messages[0].Content != "base" {
		t.Errorf("long query must not augment, got %q", messages[0].Content)
	}
}

func TestAugmentSystemPromptWithRecentSkillOutput_NoOpWhenNoCache(t *testing.T) {
	ps := NewPipelineState()
	messages := []core.LlmMessage{
		{Role: core.RoleSystem, Content: "base"},
	}
	augmentSystemPromptWithRecentSkillOutput(messages, "원화로 환율", ps)
	if messages[0].Content != "base" {
		t.Errorf("empty cache must not augment, got %q", messages[0].Content)
	}
}

func TestAugmentSystemPromptWithRecentSkillOutput_NoOpWhenEmptyMessages(t *testing.T) {
	ps := NewPipelineState()
	ps.RecordSkillOutput("data")
	var messages []core.LlmMessage
	augmentSystemPromptWithRecentSkillOutput(messages, "원화로", ps)
	// Just must not panic.
}

func TestRecordPipelineTurn_NextLoadSeesPriorTurns(t *testing.T) {
	// Two consecutive branch dispatches under the same agentID must
	// accumulate in history — this is what gives the 3rd turn (legacy
	// LLM) a 2-turn context.
	st := openTestStore(t)
	sess := &Session{Store: st}
	mkEvent := func(text string) core.Event {
		payload, _ := json.Marshal(core.ChatPayload{
			ChatID:    "test-chat",
			Text:      text,
			SessionID: "test-session",
		})
		return core.Event{Type: core.EventWebChat, Payload: payload}
	}

	if err := sess.recordPipelineTurn(mkEvent("환율"), "환율", "1 USD = 1477.04 KRW"); err != nil {
		t.Fatal(err)
	}
	if err := sess.recordPipelineTurn(mkEvent("원화로"), "원화로", "1 USD = 1477.04 KRW (raw)"); err != nil {
		t.Fatal(err)
	}

	state, _ := st.LoadConversationStateForChat(testWebChatConversationID)
	if len(state.Turns) != 4 {
		t.Fatalf("expected 4 turns (2 user + 2 assistant), got %d", len(state.Turns))
	}
}
