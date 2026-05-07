package engine

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
	mcpreg "github.com/jinto/kittypaw/mcp"
)

func TestBuildSkillsSection(t *testing.T) {
	section := buildSkillsSection("")

	// Must start with the header
	if !strings.HasPrefix(section, "## Available skill globals") {
		t.Error("buildSkillsSection missing header")
	}

	// Must contain every skill from the registry
	for _, skill := range core.SkillRegistry {
		for _, m := range skill.Methods {
			if !strings.Contains(section, m.Signature) {
				t.Errorf("buildSkillsSection missing signature: %s", m.Signature)
			}
		}
	}

	// Must contain console.log
	if !strings.Contains(section, "console.log") {
		t.Error("buildSkillsSection missing console.log")
	}

	// Must be deterministic
	section2 := buildSkillsSection("")
	if section != section2 {
		t.Error("buildSkillsSection is not deterministic")
	}
}

func TestBuildSkillsSection_ImageGuardGuidance(t *testing.T) {
	section := buildSkillsSection("")
	for _, phrase := range []string{
		"Image.generate",
		"img.error || !img.url",
		"![generated image]",
		"img.imageUrl",
		"Do not claim image generation is unavailable",
		"unless Image.generate returns an error",
	} {
		if !strings.Contains(section, phrase) {
			t.Fatalf("buildSkillsSection missing image guidance phrase %q", phrase)
		}
	}
}

func TestBuildSkillsSection_FileWorkspaceGuidance(t *testing.T) {
	section := buildSkillsSection("")
	for _, phrase := range []string{
		"Relative File paths are inside the configured workspace",
		"File.write(\"memo.txt\", content)",
	} {
		if !strings.Contains(section, phrase) {
			t.Fatalf("buildSkillsSection missing file guidance phrase %q", phrase)
		}
	}
}

func TestChannelDeliverySection_KakaoTalkReplyOnly(t *testing.T) {
	section := buildChannelDeliverySection(&core.Config{
		Channels: []core.ChannelConfig{
			{ChannelType: core.ChannelTelegram},
			{ChannelType: core.ChannelKakaoTalk},
		},
	})
	for _, phrase := range []string{
		"## Configured channel delivery",
		"telegram",
		"kakao_talk",
		"reply-only",
		"not a stable chat_id",
		"Do not say KakaoTalk is disconnected",
		"scheduled KakaoTalk delivery",
	} {
		if !strings.Contains(section, phrase) {
			t.Fatalf("channel delivery section missing %q:\n%s", phrase, section)
		}
	}
}

func TestChannelDeliverySection_NoChannels(t *testing.T) {
	if got := buildChannelDeliverySection(&core.Config{}); got != "" {
		t.Fatalf("expected empty section without configured channels, got:\n%s", got)
	}
	if got := buildChannelDeliverySection(nil); got != "" {
		t.Fatalf("expected empty section without config, got:\n%s", got)
	}
}

func TestParseAtMention(t *testing.T) {
	tests := []struct {
		text      string
		wantID    string
		wantRest  string
		wantMatch bool
	}{
		{"@bot hello", "bot", "hello", true},
		{"@my-runner do something", "my-runner", "do something", true},
		{"@agent_1", "agent_1", "", true},
		{"hello @bot", "", "hello @bot", false},       // not at start
		{"@", "", "@", false},                         // bare @
		{"", "", "", false},                           // empty
		{"no mention", "", "no mention", false},       // no @
		{"@inv@lid rest", "", "@inv@lid rest", false}, // invalid char in ID
		{"@CamelCase text", "CamelCase", "text", true},
		{"  @spaced text", "spaced", "text", true}, // leading whitespace
	}
	for _, tt := range tests {
		id, rest, ok := ParseAtMention(tt.text)
		if id != tt.wantID || rest != tt.wantRest || ok != tt.wantMatch {
			t.Errorf("ParseAtMention(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.text, id, rest, ok, tt.wantID, tt.wantRest, tt.wantMatch)
		}
	}
}

func TestFormatEvent(t *testing.T) {
	payload := core.ChatPayload{Text: "hello world"}
	raw, _ := json.Marshal(payload)
	event := &core.Event{Type: core.EventWebChat, Payload: raw}

	got := FormatEvent(event)
	if got != "hello world" {
		t.Errorf("FormatEvent() = %q, want %q", got, "hello world")
	}
}

func TestFormatEventAttachmentDoesNotExposePrivateURL(t *testing.T) {
	payload := core.ChatPayload{
		Text: "이 사진 설명해줘",
		Attachments: []core.ChatAttachment{{
			ID:      "tg_42_0",
			Type:    "image",
			Source:  "telegram",
			URL:     "https://api.telegram.org/file/botsecret-token/photos/cat.jpg",
			Caption: "이 사진 설명해줘",
		}},
	}
	raw, _ := json.Marshal(payload)
	event := &core.Event{Type: core.EventTelegram, Payload: raw}

	got := FormatEvent(event)
	if !strings.Contains(got, "tg_42_0") || !strings.Contains(got, "image") {
		t.Fatalf("FormatEvent missing attachment handle: %q", got)
	}
	if strings.Contains(got, "secret-token") || strings.Contains(got, "api.telegram.org") {
		t.Fatalf("FormatEvent leaked private URL: %q", got)
	}
}

func TestFormatExecResult(t *testing.T) {
	tests := []struct {
		result *core.ExecutionResult
		want   string
	}{
		{&core.ExecutionResult{Success: true, Output: "42"}, "output: 42"},
		{&core.ExecutionResult{Success: false, Error: "boom"}, "error: boom"},
	}
	for _, tt := range tests {
		got := FormatExecResult(tt.result)
		if got != tt.want {
			t.Errorf("FormatExecResult(%+v) = %q, want %q", tt.result, got, tt.want)
		}
	}
}

func TestBuildMCPToolsSection(t *testing.T) {
	tools := map[string][]mcpreg.ToolInfo{
		"browser": {
			{Name: "run_session", Description: "Run a browser session"},
			{Name: "get_result", Description: "Get session result"},
		},
		"filesystem": {
			{Name: "read_file", Description: "Read a file"},
		},
	}
	section := BuildMCPToolsSection(tools)
	if !strings.HasPrefix(section, "## MCP Tools") {
		t.Error("missing ## MCP Tools header")
	}
	if !strings.Contains(section, "### browser") {
		t.Error("missing ### browser section")
	}
	if !strings.Contains(section, "### filesystem") {
		t.Error("missing ### filesystem section")
	}
	if !strings.Contains(section, "- run_session: Run a browser session") {
		t.Error("missing run_session tool line")
	}
	if !strings.Contains(section, "- read_file: Read a file") {
		t.Error("missing read_file tool line")
	}
}

func TestBuildMCPToolsSectionEmpty(t *testing.T) {
	if got := BuildMCPToolsSection(nil); got != "" {
		t.Errorf("expected empty string for nil, got %q", got)
	}
	if got := BuildMCPToolsSection(map[string][]mcpreg.ToolInfo{}); got != "" {
		t.Errorf("expected empty string for empty map, got %q", got)
	}
}

func TestBuildMCPToolsSectionSorted(t *testing.T) {
	tools := map[string][]mcpreg.ToolInfo{
		"zebra": {{Name: "z_tool", Description: "Z"}},
		"alpha": {{Name: "a_tool", Description: "A"}},
		"mid":   {{Name: "m_tool", Description: "M"}},
	}
	section := BuildMCPToolsSection(tools)
	alphaIdx := strings.Index(section, "### alpha")
	midIdx := strings.Index(section, "### mid")
	zebraIdx := strings.Index(section, "### zebra")
	if alphaIdx >= midIdx || midIdx >= zebraIdx {
		t.Errorf("servers not in alpha order: alpha=%d, mid=%d, zebra=%d", alphaIdx, midIdx, zebraIdx)
	}
}

func TestBuildMCPToolsSectionCap(t *testing.T) {
	// Create many tools that exceed 2000 chars
	tools := map[string][]mcpreg.ToolInfo{}
	for i := 0; i < 100; i++ {
		tools["server"] = append(tools["server"], mcpreg.ToolInfo{
			Name:        fmt.Sprintf("tool_%03d", i),
			Description: "A moderately long description for testing the budget cap",
		})
	}
	section := BuildMCPToolsSection(tools)
	if len(section) > 2100 { // allow small overhead for omitted message
		t.Errorf("section too long: %d chars", len(section))
	}
	if !strings.Contains(section, "more tools omitted") {
		t.Error("expected truncation message")
	}
}

// --- Block constants ---

func TestBlockConstants_NonEmpty(t *testing.T) {
	blocks := map[string]string{
		"IdentityBlock":      IdentityBlock,
		"ExecutionBlock":     ExecutionBlock,
		"QualityBlock":       QualityBlock,
		"SkillCreationBlock": SkillCreationBlock,
		"MemoryBlock":        MemoryBlock,
	}
	for name, block := range blocks {
		if len(strings.TrimSpace(block)) == 0 {
			t.Errorf("%s is empty", name)
		}
	}
}

func TestBlockConstants_KeyPhrases(t *testing.T) {
	tests := []struct {
		block  string
		name   string
		phrase string
	}{
		{IdentityBlock, "IdentityBlock", "KittyPaw"},
		{IdentityBlock, "IdentityBlock", "How you work"},
		{ExecutionBlock, "ExecutionBlock", "## Rules"},
		{ExecutionBlock, "ExecutionBlock", "Web.search query quality"},
		{QualityBlock, "QualityBlock", "## Decision"},
		{QualityBlock, "QualityBlock", "## Evidence"},
		{QualityBlock, "QualityBlock", "## Capability"},
		{QualityBlock, "QualityBlock", "never fabricate"},
		// General-principle markers — Codex push: positive framing over
		// specific phrase enumeration (which collides with LLM priors).
		{QualityBlock, "QualityBlock", "first-person"},
		{QualityBlock, "QualityBlock", "mis-attribution"},
		{SkillCreationBlock, "SkillCreationBlock", "When to create a skill"},
		{SkillCreationBlock, "SkillCreationBlock", "schedule"},
		{SkillCreationBlock, "SkillCreationBlock", "once"},
		{MemoryBlock, "MemoryBlock", "Memory.user"},
	}
	for _, tt := range tests {
		if !strings.Contains(tt.block, tt.phrase) {
			t.Errorf("%s missing phrase %q", tt.name, tt.phrase)
		}
	}
}

func TestQualityBlock_InstallOfferMustExplainValue(t *testing.T) {
	for _, phrase := range []string{"설치하면 무엇을 바로 할 수 있는지", "설치해서 지금 실행할까요"} {
		if !strings.Contains(QualityBlock, phrase) {
			t.Fatalf("QualityBlock missing install-offer value rule %q", phrase)
		}
	}
}

func TestQualityBlock_DiscouragesRawLinkDump(t *testing.T) {
	if !strings.Contains(QualityBlock, "Do NOT hand the user a list of generic links") {
		t.Fatal("QualityBlock must discourage raw link dumps")
	}
}

func TestQualityBlock_FramesSearchCandidatesWithoutOverclaiming(t *testing.T) {
	for _, phrase := range []string{"Do not call search-result candidates confirmed sources", "avoid mechanical section labels"} {
		if !strings.Contains(QualityBlock, phrase) {
			t.Fatalf("QualityBlock missing search-result framing rule %q", phrase)
		}
	}
}

func TestQualityBlock_UsesJudgmentForFreshnessDependentRecommendations(t *testing.T) {
	for _, phrase := range []string{
		"freshness-dependent recommendation",
		"Use judgment, not keyword matching",
		"stale knowledge would likely reduce answer quality",
	} {
		if !strings.Contains(QualityBlock, phrase) {
			t.Fatalf("QualityBlock missing freshness-judgment rule %q", phrase)
		}
	}
}

// --- channelHint ---

func TestChannelHint_KnownChannels(t *testing.T) {
	tests := []struct {
		channel string
		want    string
	}{
		{"telegram", "Telegram"},
		{"web", "Web"},
		{"web_chat", "Web"},
		{"cli", "CLI"},
		{"desktop", "CLI"},
		{"slack", "Slack"},
		{"discord", "Discord"},
		{"kakao_talk", "KakaoTalk"},
	}
	for _, tt := range tests {
		hint := channelHint(tt.channel)
		if hint == "" {
			t.Errorf("channelHint(%q) returned empty", tt.channel)
		}
		if !strings.Contains(hint, tt.want) {
			t.Errorf("channelHint(%q) missing %q", tt.channel, tt.want)
		}
	}
}

func TestChannelHint_UnknownChannel(t *testing.T) {
	if hint := channelHint("unknown_future_channel"); hint != "" {
		t.Errorf("unknown channel should return empty, got %q", hint)
	}
	if hint := channelHint(""); hint != "" {
		t.Errorf("empty channel should return empty, got %q", hint)
	}
}

func TestChannelHint_TelegramDispatch(t *testing.T) {
	hint := channelHint("telegram")
	if !strings.Contains(hint, "Telegram.sendMessage") {
		t.Error("telegram hint missing Telegram.sendMessage dispatch guidance")
	}
	if !strings.Contains(hint, "return null") {
		t.Error("telegram hint missing duplicate message avoidance")
	}
}

func TestChannelHint_KakaoTalkCurrentChatAndImages(t *testing.T) {
	hint := channelHint("kakao_talk")
	for _, phrase := range []string{
		"return value",
		"current KakaoTalk chat",
		"Do not say KakaoTalk is unavailable",
		"images",
		"Image.generate",
	} {
		if !strings.Contains(hint, phrase) {
			t.Fatalf("kakao_talk hint missing %q:\n%s", phrase, hint)
		}
	}
}

// --- BuildPrompt with Staff ---

func TestBuildPrompt_WithSoul(t *testing.T) {
	state := &core.ConversationState{ConversationID: "test", SystemPrompt: SystemPrompt}
	staff := &core.Staff{ID: "mybot", Soul: "I am a cheerful assistant."}
	msgs := BuildPrompt(state, "hello", CompactionConfig{RecentWindow: 5}, &core.Config{}, "telegram", staff, "", "", nil, "")

	sys := msgs[0].Content
	if !strings.Contains(sys, "## Your Identity (SOUL.md)") {
		t.Error("missing SOUL.md header in system prompt")
	}
	if !strings.Contains(sys, "I am a cheerful assistant.") {
		t.Error("soul content not injected")
	}
}

func TestBuildPrompt_SoulBeforeIdentity(t *testing.T) {
	state := &core.ConversationState{ConversationID: "test"}
	staff := &core.Staff{ID: "mybot", Soul: "I am the soul."}
	msgs := BuildPrompt(state, "hi", CompactionConfig{RecentWindow: 5}, &core.Config{}, "web", staff, "", "", nil, "")

	sys := msgs[0].Content
	soulIdx := strings.Index(sys, "## Your Identity (SOUL.md)")
	identityIdx := strings.Index(sys, "You are KittyPaw")
	if soulIdx < 0 || identityIdx < 0 {
		t.Fatal("missing soul or identity section")
	}
	if soulIdx >= identityIdx {
		t.Errorf("SOUL.md (pos %d) should appear before IdentityBlock (pos %d)", soulIdx, identityIdx)
	}
}

func TestBuildPrompt_WithNickAndUserMD(t *testing.T) {
	state := &core.ConversationState{ConversationID: "test", SystemPrompt: SystemPrompt}
	staff := &core.Staff{
		ID:     "bot",
		Nick:   "Paw",
		Soul:   "soul",
		UserMD: "User likes hiking.",
	}
	msgs := BuildPrompt(state, "hi", CompactionConfig{RecentWindow: 5}, &core.Config{}, "slack", staff, "", "", nil, "")

	sys := msgs[0].Content
	if !strings.Contains(sys, "Your name/nickname is: Paw") {
		t.Error("nick not injected")
	}
	if !strings.Contains(sys, "## User Notes (USER.md)") {
		t.Error("missing USER.md header")
	}
	if !strings.Contains(sys, "User likes hiking.") {
		t.Error("user md content not injected")
	}
}

func TestBuildPrompt_NilStaff(t *testing.T) {
	state := &core.ConversationState{ConversationID: "test", SystemPrompt: SystemPrompt}
	msgs := BuildPrompt(state, "hey", CompactionConfig{RecentWindow: 5}, &core.Config{}, "web", nil, "", "", nil, "")

	sys := msgs[0].Content
	if strings.Contains(sys, "## Your Identity (SOUL.md)") {
		t.Error("nil staff should not inject SOUL.md section")
	}
}

func TestBuildPrompt_BlockPresence(t *testing.T) {
	state := &core.ConversationState{ConversationID: "test"}
	msgs := BuildPrompt(state, "test", CompactionConfig{RecentWindow: 5}, &core.Config{}, "telegram", nil, "", "", nil, "")

	sys := msgs[0].Content
	required := []struct {
		name   string
		phrase string
	}{
		{"IdentityBlock", "You are KittyPaw"},
		{"ExecutionBlock", "## Rules"},
		{"QualityBlock", "## Decision"},
		{"SkillsBlock", "## Available skill globals"},
		{"SkillCreationBlock", "## When to create a skill"},
		{"MemoryBlock", "## Memory & Learning"},
	}
	for _, r := range required {
		if !strings.Contains(sys, r.phrase) {
			t.Errorf("assembled prompt missing %s (phrase: %q)", r.name, r.phrase)
		}
	}
}

func TestBuildPrompt_ChannelHintInjected(t *testing.T) {
	state := &core.ConversationState{ConversationID: "test"}
	msgs := BuildPrompt(state, "test", CompactionConfig{RecentWindow: 5}, &core.Config{}, "telegram", nil, "", "", nil, "")
	sys := msgs[0].Content
	if !strings.Contains(sys, "## Output format (Telegram)") {
		t.Error("telegram channel hint not injected into prompt")
	}
}

func TestBuildPrompt_IncludesConfiguredChannelDelivery(t *testing.T) {
	state := &core.ConversationState{ConversationID: "test"}
	cfg := &core.Config{
		Channels: []core.ChannelConfig{
			{ChannelType: core.ChannelTelegram},
			{ChannelType: core.ChannelKakaoTalk},
		},
	}
	msgs := BuildPrompt(state, "10:30에 카톡으로 보내줘", CompactionConfig{RecentWindow: 5}, cfg, "telegram", nil, "", "", nil, "")
	sys := msgs[0].Content
	for _, phrase := range []string{
		"## Configured channel delivery",
		"kakao_talk",
		"reply-only",
		"scheduled KakaoTalk delivery",
	} {
		if !strings.Contains(sys, phrase) {
			t.Fatalf("assembled prompt missing channel delivery phrase %q", phrase)
		}
	}
}

func TestBuildPrompt_XTwitterRequestsDoNotFallBackToGmail(t *testing.T) {
	state := &core.ConversationState{ConversationID: "test"}
	msgs := BuildPrompt(state, "트위터에 최근에 올라온 것 중 재미있는거 있나?", CompactionConfig{RecentWindow: 5}, &core.Config{}, "telegram", nil, "", "", nil, "")
	sys := msgs[0].Content
	for _, want := range []string{
		"Do not call Gmail for explicit X/Twitter requests",
		"X.homeTimeline is reverse chronological and is not the For You recommendation feed",
		"x_credits_depleted",
		"Do not substitute email results when X is empty or unavailable",
	} {
		if !strings.Contains(sys, want) {
			t.Fatalf("system prompt missing X/Twitter guard %q", want)
		}
	}
}

func TestBuildPrompt_NoChannelHintForUnknown(t *testing.T) {
	state := &core.ConversationState{ConversationID: "test"}
	msgs := BuildPrompt(state, "test", CompactionConfig{RecentWindow: 5}, &core.Config{}, "unknown", nil, "", "", nil, "")
	sys := msgs[0].Content
	if strings.Contains(sys, "## Output format") {
		t.Error("unknown channel should not inject output format section")
	}
}

func TestBuildPrompt_WithObservations(t *testing.T) {
	state := &core.ConversationState{ConversationID: "test"}
	obs := []core.Observation{
		{Label: "search_results", Data: "Found 3 articles about AI."},
		{Label: "page_content", Data: "Article body text here."},
	}
	msgs := BuildPrompt(state, "test", CompactionConfig{RecentWindow: 5}, &core.Config{}, "web", nil, "", "", obs, "")
	sys := msgs[0].Content
	if !strings.Contains(sys, "## Current Observations") {
		t.Error("missing observations section")
	}
	if !strings.Contains(sys, "### search_results") {
		t.Error("missing observation label")
	}
	if !strings.Contains(sys, "Found 3 articles about AI.") {
		t.Error("missing observation data")
	}
	if !strings.Contains(sys, "### page_content") {
		t.Error("missing second observation label")
	}
}

func TestBuildPrompt_NilObservations(t *testing.T) {
	// AC #10: When observations is nil, the prompt should be identical to before.
	state := &core.ConversationState{ConversationID: "test"}
	msgs := BuildPrompt(state, "test", CompactionConfig{RecentWindow: 5}, &core.Config{}, "web", nil, "", "", nil, "")
	sys := msgs[0].Content
	if strings.Contains(sys, "## Current Observations") {
		t.Error("nil observations should not inject observations section")
	}
}

func TestBuildPrompt_EmptyObservations(t *testing.T) {
	state := &core.ConversationState{ConversationID: "test"}
	msgs := BuildPrompt(state, "test", CompactionConfig{RecentWindow: 5}, &core.Config{}, "web", nil, "", "", []core.Observation{}, "")
	sys := msgs[0].Content
	if strings.Contains(sys, "## Current Observations") {
		t.Error("empty observations should not inject observations section")
	}
}

func TestBuildPrompt_TokenBudget(t *testing.T) {
	// Authored static text only (skills section is dynamic). Budget catches
	// accidental drift; intentional growth tied to a documented UX fix is
	// expected — see git log for each bump's rationale. The repeated bumps
	// (2400 → 2800 → 2900 → 3000 → 3250) are themselves a signal that the prompt
	// needs a structural refactor; that is its own plan.
	staticText := IdentityBlock + "\n\n" + ExecutionBlock + "\n\n" + QualityBlock + "\n\n" + SkillCreationBlock + "\n\n" + MemoryBlock
	tokens := EstimateTokens(staticText)
	const maxTokens = 3250
	if tokens > maxTokens {
		t.Errorf("static text blocks %d tokens exceeds budget %d", tokens, maxTokens)
	}
	t.Logf("static text blocks: %d tokens (budget: %d)", tokens, maxTokens)
}
