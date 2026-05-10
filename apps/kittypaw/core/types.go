package core

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const MaxHistoryTurns = 100

// Role represents who is speaking in a conversation.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
)

// EventType identifies the channel source of an incoming event.
type EventType string

const (
	EventWebChat   EventType = "web_chat"
	EventTelegram  EventType = "telegram"
	EventDesktop   EventType = "desktop"
	EventKakaoTalk EventType = "kakao_talk"
	EventSlack     EventType = "slack"
	EventDiscord   EventType = "discord"
	// EventTeamSpacePush is emitted by ChannelFanout when a team-space account
	// pushes a message to a member account. AccountRouter dispatches to the
	// target Session the same way it dispatches inbound chat events, so the
	// member runner can treat it as a normal observation.
	EventTeamSpacePush EventType = "team_space.push"

	// EventFamilyPush is retained as a compile-time compatibility alias while
	// tests and callers migrate to team-space terminology.
	EventFamilyPush EventType = EventTeamSpacePush
)

const legacyTeamSpacePushEventType EventType = "family.push"

// IsTeamSpacePushEvent recognizes current team-space pushes and the pre-rename
// wire literal that may still exist in queued or persisted events.
func IsTeamSpacePushEvent(t EventType) bool {
	return t == EventTeamSpacePush || t == legacyTeamSpacePushEventType
}

// LoopPhase tracks the runner loop state machine position.
type LoopPhase string

const (
	PhaseInit     LoopPhase = "init"
	PhasePrompt   LoopPhase = "prompt"
	PhaseGenerate LoopPhase = "generate"
	PhaseRetry    LoopPhase = "retry"
	PhaseFinish   LoopPhase = "finish"
)

// ConversationState holds the mutable runtime state for the account conversation.
type ConversationState struct {
	ConversationID      string             `json:"conversation_id,omitempty"`
	SystemPrompt        string             `json:"system_prompt"`
	ConversationStaffID string             `json:"conversation_staff_id,omitempty"`
	Turns               []ConversationTurn `json:"turns"`
}

// ConversationTurn is a single message in a conversation.
type ConversationTurn struct {
	ConversationID string      `json:"conversation_id,omitempty"`
	Role           Role        `json:"role"`
	Content        string      `json:"content"`
	Code           string      `json:"code,omitempty"`
	Result         string      `json:"result,omitempty"`
	ToolTraces     []ToolTrace `json:"tool_traces,omitempty"`
	Channel        string      `json:"channel,omitempty"`
	ChannelUserID  string      `json:"channel_user_id,omitempty"`
	ChatID         string      `json:"chat_id,omitempty"`
	MessageID      string      `json:"message_id,omitempty"`
	Timestamp      string      `json:"timestamp"`
}

// Event is an inbound message from any channel.
// AccountID identifies which account the event belongs to. Empty AccountID is
// rejected by the AccountRouter (no default fallback) to prevent cross-account
// leaks in multi-account deployments.
type Event struct {
	Type      EventType       `json:"type"`
	AccountID string          `json:"account_id,omitempty"`
	Payload   json.RawMessage `json:"payload"`
}

// ChatPayload is the common structure inside Event.Payload.
//
// SessionID is transport/runtime metadata. ConversationID optionally selects
// a first-class conversation/thread.
type ChatPayload struct {
	ChatID         string           `json:"chat_id"`
	Text           string           `json:"text"`
	FromName       string           `json:"from_name,omitempty"`
	WorkspaceID    string           `json:"workspace_id,omitempty"`
	SessionID      string           `json:"session_id,omitempty"`
	ConversationID string           `json:"conversation_id,omitempty"`
	Attachments    []ChatAttachment `json:"attachments,omitempty"`
	// ReplyToMessageID is the platform-specific message ID of the inbound
	// message. When set, channels that support reply-quoting (Telegram) will
	// quote the original message in the response. Empty = plain send.
	ReplyToMessageID string `json:"reply_to_message_id,omitempty"`
}

// ChatAttachment carries channel-provided media metadata for the current turn.
// URL is intentionally internal transport data: prompts and conversation
// history should expose only the attachment ID/metadata, never private URLs.
type ChatAttachment struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Source    string `json:"source,omitempty"`
	URL       string `json:"url,omitempty"`
	MimeType  string `json:"mime_type,omitempty"`
	FileName  string `json:"file_name,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	Caption   string `json:"caption,omitempty"`
}

// LlmMessage is a single message sent to/from an LLM.
//
// Two content shapes are supported, populated mutually exclusively at the
// callsite; the chosen LLM provider picks whichever is non-empty:
//   - Content (plain string): the historical default. Goes onto the wire as
//     Anthropic's string-form content.
//   - ContentBlocks ([]ContentBlock): native Anthropic content array. Required
//     when the message carries a tool_use or tool_result block; using it is
//     how we keep the LLM from mis-attributing tool output to the user.
type LlmMessage struct {
	Role          Role           `json:"role"`
	Content       string         `json:"content"`
	ContentBlocks []ContentBlock `json:"content_blocks,omitempty"`
}

// Block type discriminators for ContentBlock.Type.
const (
	BlockTypeText       = "text"
	BlockTypeToolUse    = "tool_use"
	BlockTypeToolResult = "tool_result"
)

// ContentBlock is one element of an Anthropic-native message content array.
//
// A single struct (rather than an interface with concrete types) is used
// because the variant set is closed and small. A custom MarshalJSON
// dispatches per Type so each variant emits exactly the fields the API
// requires — Anthropic returns 400 if a required field is missing (e.g.
// tool_use.input must always be present, even when the input is empty).
//
// Variants and their required fields:
//
//   - BlockTypeText:       Text
//   - BlockTypeToolUse:    ID, Name, Input (input is required even when {})
//   - BlockTypeToolResult: ToolUseID, Content
type ContentBlock struct {
	Type string `json:"type"`

	// Text variant.
	Text string `json:"text,omitempty"`

	// ToolUse variant.
	ID    string         `json:"id,omitempty"`
	Name  string         `json:"name,omitempty"`
	Input map[string]any `json:"input,omitempty"`

	// ToolResult variant.
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
}

// MarshalJSON emits only the fields meaningful for the block's Type, with
// no "omitempty" on required fields. Without this, Go's default encoder
// drops empty maps via the struct tag's omitempty — Anthropic then 400s on
// tool_use because input is a required field even when the call takes no
// arguments. See `messages.<n>.content.<n>.tool_use.input: Field required`.
func (b ContentBlock) MarshalJSON() ([]byte, error) {
	switch b.Type {
	case BlockTypeText:
		return json.Marshal(struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{b.Type, b.Text})

	case BlockTypeToolUse:
		input := b.Input
		if input == nil {
			input = map[string]any{}
		}
		return json.Marshal(struct {
			Type  string         `json:"type"`
			ID    string         `json:"id"`
			Name  string         `json:"name"`
			Input map[string]any `json:"input"`
		}{b.Type, b.ID, b.Name, input})

	case BlockTypeToolResult:
		return json.Marshal(struct {
			Type      string `json:"type"`
			ToolUseID string `json:"tool_use_id"`
			Content   string `json:"content"`
		}{b.Type, b.ToolUseID, b.Content})

	default:
		return nil, fmt.Errorf("ContentBlock: unknown type %q", b.Type)
	}
}

// SkillCall represents a skill invocation captured from sandbox execution.
type SkillCall struct {
	ID        string            `json:"id,omitempty"`
	SkillName string            `json:"skill_name"`
	Method    string            `json:"method"`
	Args      []json.RawMessage `json:"args"`
}

// ToolTrace is the structured transcript entry for one sandbox tool call.
// Result is the raw JSON string returned by the resolver so callers can replay
// or inspect the exact tool output without parsing assistant prose.
type ToolTrace struct {
	ID        string            `json:"id"`
	SkillName string            `json:"skill_name"`
	Method    string            `json:"method"`
	Args      []json.RawMessage `json:"args,omitempty"`
	Result    json.RawMessage   `json:"result,omitempty"`
	Error     string            `json:"error,omitempty"`
	Success   bool              `json:"success"`
}

// Observation holds data from a Runner.observe() call in the sandbox.
type Observation struct {
	Label string `json:"label"`
	Data  string `json:"data"`
}

// ExecutionResult is the output of a sandbox code execution.
type ExecutionResult struct {
	Success      bool          `json:"success"`
	Output       string        `json:"output"`
	SkillCalls   []SkillCall   `json:"skill_calls"`
	ToolTraces   []ToolTrace   `json:"tool_traces,omitempty"`
	Error        string        `json:"error,omitempty"`
	Observe      bool          `json:"observe,omitempty"`
	Observations []Observation `json:"observations,omitempty"`
}

// ToEventType maps a channel configuration type to its corresponding event type.
func (ct ChannelType) ToEventType() EventType {
	switch ct {
	case ChannelTelegram:
		return EventTelegram
	case ChannelSlack:
		return EventSlack
	case ChannelDiscord:
		return EventDiscord
	case ChannelWeb:
		return EventWebChat
	case ChannelDesktop:
		return EventDesktop
	case ChannelKakaoTalk:
		return EventKakaoTalk
	default:
		return EventType(ct)
	}
}

// ChannelName returns the human-readable channel name for an event type.
func (t EventType) ChannelName() string {
	switch t {
	case EventTelegram:
		return "telegram"
	case EventSlack:
		return "slack"
	case EventDiscord:
		return "discord"
	case EventWebChat:
		return "web"
	case EventDesktop:
		return "desktop"
	case EventKakaoTalk:
		return "kakao_talk"
	default:
		return string(t)
	}
}

// SplitChunks breaks text into pieces no longer than maxLen.
// It tries to split on newlines, falling back to hard splits.
func SplitChunks(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}
	var chunks []string
	for len(text) > 0 {
		if len(text) <= maxLen {
			chunks = append(chunks, text)
			break
		}
		cut := maxLen
		if idx := strings.LastIndex(text[:maxLen], "\n"); idx > maxLen/2 {
			cut = idx + 1
		}
		chunks = append(chunks, text[:cut])
		text = text[cut:]
	}
	return chunks
}

// ValidateSkillName checks that a skill name contains only safe characters.
var validSkillName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func ValidateSkillName(name string) error {
	if name == "" {
		return fmt.Errorf("skill name is empty")
	}
	if strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("skill name contains path traversal characters: %q", name)
	}
	if !validSkillName.MatchString(name) {
		return fmt.Errorf("skill name contains invalid characters: %q (allowed: a-z, A-Z, 0-9, _, -)", name)
	}
	return nil
}

// ValidateStaffID checks that a staff ID contains only safe characters.
func ValidateStaffID(id string) error {
	if id == "" {
		return fmt.Errorf("staff ID is empty")
	}
	if strings.Contains(id, "..") || strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("staff ID contains path traversal characters: %q", id)
	}
	if !validSkillName.MatchString(id) {
		return fmt.Errorf("staff ID contains invalid characters: %q (allowed: a-z, A-Z, 0-9, _, -)", id)
	}
	return nil
}

// IsSecretEnvVar returns true if the variable name likely contains a secret.
func IsSecretEnvVar(name string) bool {
	upper := strings.ToUpper(name)
	for _, pattern := range []string{"KEY", "SECRET", "TOKEN", "PASSWORD", "CREDENTIAL", "AUTH"} {
		if strings.Contains(upper, pattern) {
			return true
		}
	}
	return false
}

// IsPrivateIP returns true if the host resolves to a private/loopback/link-local address.
func IsPrivateIP(host string) bool {
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasPrefix(lower, "127.") || lower == "::1" {
		return true
	}
	// Check common private IP prefixes (heuristic, not full CIDR check).
	for _, prefix := range []string{"10.", "172.16.", "172.17.", "172.18.", "172.19.",
		"172.20.", "172.21.", "172.22.", "172.23.", "172.24.", "172.25.",
		"172.26.", "172.27.", "172.28.", "172.29.", "172.30.", "172.31.",
		"192.168.", "169.254.", "0."} {
		if strings.HasPrefix(host, prefix) {
			return true
		}
	}
	return false
}

// NowTimestamp returns the current Unix epoch seconds as a string.
func NowTimestamp() string {
	return strconv.FormatInt(time.Now().Unix(), 10)
}

// ParsePayload decodes the Event payload into a ChatPayload.
func (e *Event) ParsePayload() (ChatPayload, error) {
	var p ChatPayload
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		return p, fmt.Errorf("parse event payload: %w", err)
	}
	return p, nil
}
