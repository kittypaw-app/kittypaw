package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateSkillName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"my-skill", false},
		{"my_skill", false},
		{"Skill123", false},
		{"a", false},
		{"", true},              // empty
		{"..", true},            // path traversal
		{"../etc/passwd", true}, // path traversal
		{"skill/../bad", true},  // embedded traversal
		{"skill/bad", true},     // slash
		{"skill\\bad", true},    // backslash
		{"skill name", true},    // space
		{"skill!name", true},    // special char
		{"skill@name", true},    // at sign
		{"good-skill-name", false},
		{"x-y_z-123", false},
	}
	for _, tt := range tests {
		err := ValidateSkillName(tt.name)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateSkillName(%q) error = %v, wantErr %v", tt.name, err, tt.wantErr)
		}
	}
}

func TestIsSecretEnvVar(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"API_KEY", true},
		{"SECRET_VALUE", true},
		{"AUTH_TOKEN", true},
		{"DB_PASSWORD", true},
		{"AWS_CREDENTIAL", true},
		{"HOME", false},
		{"PATH", false},
		{"LANG", false},
		{"GOPAW_PORT", false},
		{"my_secret", true}, // lowercase "secret"
		{"tokenizer", true}, // contains "token"
		{"AUTHOR", true},    // contains "AUTH"
	}
	for _, tt := range tests {
		got := IsSecretEnvVar(tt.name)
		if got != tt.want {
			t.Errorf("IsSecretEnvVar(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestIsPrivateIP(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"localhost", true},
		{"127.0.0.1", true},
		{"127.0.0.2", true},
		{"::1", true},
		{"10.0.0.1", true},
		{"10.255.255.255", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.0.1", true},
		{"192.168.1.100", true},
		{"169.254.1.1", true},
		{"0.0.0.0", true},
		// Public addresses
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"203.0.113.1", false},
		{"example.com", false},
		{"172.32.0.1", false}, // just outside 172.16-31 range
		{"11.0.0.1", false},
	}
	for _, tt := range tests {
		got := IsPrivateIP(tt.host)
		if got != tt.want {
			t.Errorf("IsPrivateIP(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

func TestSplitChunks(t *testing.T) {
	tests := []struct {
		text   string
		maxLen int
		want   int // expected number of chunks
	}{
		{"short", 100, 1},
		{"", 10, 1},
		{"hello\nworld\nfoo\nbar", 11, 2},
		// Force hard split when no newline found in first half
		{"abcdefghij", 5, 2},
	}
	for _, tt := range tests {
		chunks := SplitChunks(tt.text, tt.maxLen)
		if len(chunks) != tt.want {
			t.Errorf("SplitChunks(%q, %d) = %d chunks, want %d", tt.text, tt.maxLen, len(chunks), tt.want)
		}
		// Verify all chunks are within maxLen
		for i, c := range chunks {
			if len(c) > tt.maxLen {
				t.Errorf("SplitChunks chunk %d len %d > maxLen %d", i, len(c), tt.maxLen)
			}
		}
		// Verify reassembly
		reassembled := ""
		for _, c := range chunks {
			reassembled += c
		}
		if reassembled != tt.text {
			t.Errorf("SplitChunks reassembly mismatch: got %q, want %q", reassembled, tt.text)
		}
	}
}

func TestParsePayload(t *testing.T) {
	payload := ChatPayload{
		ChatID:    "chat123",
		Text:      "hello",
		FromName:  "alice",
		SessionID: "sess1",
	}
	raw, _ := json.Marshal(payload)
	event := &Event{Type: EventWebChat, Payload: raw}

	got, err := event.ParsePayload()
	if err != nil {
		t.Fatalf("ParsePayload() error: %v", err)
	}
	if got.ChatID != "chat123" || got.Text != "hello" || got.FromName != "alice" {
		t.Errorf("ParsePayload() = %+v, want matching fields", got)
	}
}

func TestParsePayloadInvalid(t *testing.T) {
	event := &Event{Type: EventWebChat, Payload: json.RawMessage(`{invalid`)}
	_, err := event.ParsePayload()
	if err == nil {
		t.Error("ParsePayload() expected error for invalid JSON")
	}
}

// TestEventAccountIDMarshal verifies Event.AccountID round-trips through JSON
// and is omitted when empty (backward compatibility with pre-multi-account events).
func TestEventAccountIDMarshal(t *testing.T) {
	t.Run("with_account_id", func(t *testing.T) {
		event := Event{
			Type:      EventTelegram,
			AccountID: "alice",
			Payload:   json.RawMessage(`{"chat_id":"123","text":"hi"}`),
		}
		raw, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !strings.Contains(string(raw), `"account_id":"alice"`) {
			t.Errorf("expected account_id in JSON, got %s", raw)
		}
	})

	t.Run("empty_account_id_omitted", func(t *testing.T) {
		event := Event{
			Type:    EventTelegram,
			Payload: json.RawMessage(`{"chat_id":"123"}`),
		}
		raw, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if strings.Contains(string(raw), "account_id") {
			t.Errorf("expected no account_id when empty, got %s", raw)
		}
	})

	t.Run("legacy_json_unmarshal", func(t *testing.T) {
		legacy := []byte(`{"type":"telegram","payload":{"chat_id":"1"}}`)
		var event Event
		if err := json.Unmarshal(legacy, &event); err != nil {
			t.Fatalf("unmarshal legacy: %v", err)
		}
		if event.AccountID != "" {
			t.Errorf("expected empty AccountID on legacy JSON, got %q", event.AccountID)
		}
		if event.Type != EventTelegram {
			t.Errorf("expected Type=telegram, got %q", event.Type)
		}
	})

	t.Run("roundtrip", func(t *testing.T) {
		original := Event{
			Type:      EventKakaoTalk,
			AccountID: "family",
			Payload:   json.RawMessage(`{"text":"weather"}`),
		}
		raw, err := json.Marshal(original)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var got Event
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got.AccountID != "family" || got.Type != EventKakaoTalk {
			t.Errorf("roundtrip mismatch: got %+v", got)
		}
	})
}

func TestChannelTypeToEventType(t *testing.T) {
	tests := []struct {
		ct   ChannelType
		want EventType
	}{
		{ChannelTelegram, EventTelegram},
		{ChannelSlack, EventSlack},
		{ChannelDiscord, EventDiscord},
		{ChannelWeb, EventWebChat},
		{ChannelDesktop, EventDesktop},
		{ChannelKakaoTalk, EventKakaoTalk},
	}
	for _, tt := range tests {
		got := tt.ct.ToEventType()
		if got != tt.want {
			t.Errorf("ChannelType(%q).ToEventType() = %q, want %q", tt.ct, got, tt.want)
		}
	}
}

// TestContentBlockMarshal verifies each variant of ContentBlock serializes to
// the Anthropic content-array shape and that "omitempty" hides fields belonging
// to the other variants. These wire shapes are the contract llm/claude.go
// relays straight to the API, so any change here must be intentional.
func TestContentBlockMarshal(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		raw, err := json.Marshal(ContentBlock{Type: BlockTypeText, Text: "hi"})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		got := string(raw)
		if got != `{"type":"text","text":"hi"}` {
			t.Errorf("text shape mismatch: %s", got)
		}
	})

	t.Run("tool_use", func(t *testing.T) {
		raw, err := json.Marshal(ContentBlock{
			Type:  BlockTypeToolUse,
			ID:    "toolu_abc",
			Name:  "search",
			Input: map[string]any{"q": "weather"},
		})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		got := string(raw)
		// Field order in encoding/json follows struct definition order.
		// Cross-check critical fields without locking on whitespace.
		for _, want := range []string{
			`"type":"tool_use"`,
			`"id":"toolu_abc"`,
			`"name":"search"`,
			`"input":{"q":"weather"}`,
		} {
			if !strings.Contains(got, want) {
				t.Errorf("tool_use missing %q in %s", want, got)
			}
		}
		// Tool-result-only field must NOT leak.
		if strings.Contains(got, "tool_use_id") {
			t.Errorf("tool_use leaked tool_use_id field: %s", got)
		}
	})

	// Pinned by Anthropic 400 "input: Field required" — even when a tool_use
	// carries no arguments, the wire MUST include "input": {}. The default
	// json struct-tag "omitempty" drops empty maps, so MarshalJSON has to
	// force the field. Skipping this regression silently breaks Llm.generate.
	t.Run("tool_use_empty_input_still_emits", func(t *testing.T) {
		// nil input: the Phase A executeLLM path constructs blocks with
		// map[string]any{} but a nil input must serialize the same way.
		for _, in := range []map[string]any{nil, {}} {
			raw, err := json.Marshal(ContentBlock{
				Type:  BlockTypeToolUse,
				ID:    "toolu_z",
				Name:  "framework_context",
				Input: in,
			})
			if err != nil {
				t.Fatalf("marshal (input=%v): %v", in, err)
			}
			got := string(raw)
			if !strings.Contains(got, `"input":{}`) {
				t.Errorf("tool_use with empty/nil input must serialize input as {}, got: %s", got)
			}
		}
	})

	t.Run("tool_result", func(t *testing.T) {
		raw, err := json.Marshal(ContentBlock{
			Type:      BlockTypeToolResult,
			ToolUseID: "toolu_abc",
			Content:   "search payload here",
		})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		got := string(raw)
		for _, want := range []string{
			`"type":"tool_result"`,
			`"tool_use_id":"toolu_abc"`,
			`"content":"search payload here"`,
		} {
			if !strings.Contains(got, want) {
				t.Errorf("tool_result missing %q in %s", want, got)
			}
		}
		// Tool-use-only fields must NOT leak.
		for _, leak := range []string{`"id":`, `"name":`, `"input":`} {
			if strings.Contains(got, leak) {
				t.Errorf("tool_result leaked %s field: %s", leak, got)
			}
		}
	})
}

// TestLlmMessageContentBlocksRoundtrip ensures the new ContentBlocks field
// round-trips through JSON without losing block data and without mutating the
// existing string-Content wire format used by 30+ legacy callsites.
func TestLlmMessageContentBlocksRoundtrip(t *testing.T) {
	t.Run("string_content_unchanged", func(t *testing.T) {
		msg := LlmMessage{Role: RoleUser, Content: "hi"}
		raw, err := json.Marshal(msg)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		got := string(raw)
		if got != `{"role":"user","content":"hi"}` {
			t.Errorf("backward-compat wire format changed: %s", got)
		}
		if strings.Contains(got, "content_blocks") {
			t.Errorf("empty ContentBlocks should be omitted: %s", got)
		}
	})

	t.Run("blocks_present", func(t *testing.T) {
		msg := LlmMessage{
			Role: RoleAssistant,
			ContentBlocks: []ContentBlock{
				{Type: BlockTypeToolUse, ID: "id-1", Name: "search", Input: map[string]any{"q": "x"}},
			},
		}
		raw, err := json.Marshal(msg)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var back LlmMessage
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(back.ContentBlocks) != 1 {
			t.Fatalf("blocks lost in roundtrip: %+v", back)
		}
		b := back.ContentBlocks[0]
		if b.Type != BlockTypeToolUse || b.ID != "id-1" || b.Name != "search" {
			t.Errorf("block fields wrong: %+v", b)
		}
		if got, want := b.Input["q"], "x"; got != want {
			t.Errorf("input map roundtrip: got %v want %v", got, want)
		}
	})
}

func TestSkillRegistryCompleteness(t *testing.T) {
	if len(SkillRegistry) == 0 {
		t.Fatal("SkillRegistry is empty")
	}
	seen := make(map[string]bool)
	for _, skill := range SkillRegistry {
		if skill.Name == "" {
			t.Error("SkillRegistry contains entry with empty name")
		}
		if seen[skill.Name] {
			t.Errorf("SkillRegistry has duplicate: %s", skill.Name)
		}
		seen[skill.Name] = true
		if len(skill.Methods) == 0 {
			t.Errorf("SkillRegistry[%s] has no methods", skill.Name)
		}
		for _, m := range skill.Methods {
			if m.Name == "" {
				t.Errorf("SkillRegistry[%s] has method with empty name", skill.Name)
			}
			if m.Signature == "" {
				t.Errorf("SkillRegistry[%s].%s has empty signature", skill.Name, m.Name)
			}
		}
	}
}

func TestSkillRegistryUsesProjectsInsteadOfLegacyKanban(t *testing.T) {
	var foundProjects bool
	for _, skill := range SkillRegistry {
		if skill.Name == "Kanban" {
			t.Fatal("SkillRegistry must not expose legacy Kanban tool metadata")
		}
		if skill.Name != "Projects" {
			continue
		}
		foundProjects = true
		methods := make(map[string]bool)
		for _, method := range skill.Methods {
			methods[method.Name] = true
			if strings.Contains(method.Signature, "Kanban.") {
				t.Fatalf("Projects.%s signature exposes legacy Kanban term: %q", method.Name, method.Signature)
			}
		}
		for _, want := range []string{
			"list", "current", "show", "listTickets", "createTicket", "showTicket",
			"moveTicket", "commentTicket", "createBriefDraft", "updateBriefDraft",
			"commitBriefDraft", "planJob", "showJob", "cancelJob", "appendJobInput",
		} {
			if !methods[want] {
				t.Fatalf("Projects metadata missing method %q", want)
			}
		}
	}
	if !foundProjects {
		t.Fatal("SkillRegistry missing Projects metadata")
	}
}

func TestSkillMetadataUsesTeamSpaceTerminology(t *testing.T) {
	for _, skill := range SkillRegistry {
		if strings.Contains(strings.ToLower(skill.Name), "family account") {
			t.Errorf("SkillRegistry[%s] exposes family-account terminology in name", skill.Name)
		}
		for _, m := range skill.Methods {
			surface := strings.ToLower(m.Name + " " + m.Signature)
			forbidden := []string{"family account", "family-only", "family account only", "family.push", "target is not the family account"}
			for _, term := range forbidden {
				if strings.Contains(surface, term) {
					t.Errorf("SkillRegistry[%s].%s exposes %q in signature %q", skill.Name, m.Name, term, m.Signature)
				}
			}
		}
	}
}
