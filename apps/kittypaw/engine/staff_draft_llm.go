package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/store"
)

const staffDraftContextTurns = 12

type staffDraftLLMResponse struct {
	ID                 string   `json:"id"`
	DisplayName        string   `json:"display_name"`
	Description        string   `json:"description"`
	Aliases            []string `json:"aliases"`
	Soul               string   `json:"soul"`
	NeedsClarification string   `json:"needs_clarification"`
}

func buildStaffDraftFromRequest(ctx context.Context, sess *Session, request string) (StaffDraft, string, error) {
	request = strings.TrimSpace(request)
	if request == "" {
		return StaffDraft{}, "", fmt.Errorf("staff request is empty")
	}
	if sess == nil || sess.Provider == nil || sess.Store == nil {
		role := request
		if parsed, ok := staffCreateRoleFromText(request); ok {
			role = parsed
		}
		return buildStaffDraft(role, "natural_language"), "", nil
	}

	turns, err := sess.Store.ListConversationTurns(staffDraftContextTurns)
	if err != nil {
		return StaffDraft{}, "", err
	}
	messages := buildStaffDraftMessages(request, turns)
	resp, err := sess.Provider.Generate(WithLLMCallKind(ctx, "staff.draft"), messages)
	if err != nil {
		return StaffDraft{}, "", err
	}
	draft, clarification, err := parseStaffDraftLLMResponse(resp.Content, request)
	if err != nil {
		return StaffDraft{}, "", err
	}
	return draft, clarification, nil
}

func buildStaffDraftMessages(request string, turns []store.ConversationTurnRecord) []core.LlmMessage {
	var sb strings.Builder
	sb.WriteString(`You create KittyPaw staff drafts from the user's request and recent conversation.

Return ONLY a JSON object with this shape:
{
  "id": "lowercase-ascii-id",
  "display_name": "short human label",
  "description": "one practical role summary",
  "aliases": ["short alias"],
  "soul": "SOUL.md content"
}

Rules:
- Use the recent conversation when the request asks to base the staff on the conversation.
- Do not copy request preambles such as "look at our conversation" into the staff name, description, or SOUL.
- The id must be ASCII, concise, and safe for a directory name. Prefer role nouns like pm, designer, qa, finance.
- The SOUL must define the role and working style in the user's language.
- If the role cannot be inferred, return {"needs_clarification":"short question"}.

Recent conversation:
`)
	if len(turns) == 0 {
		sb.WriteString("(none)\n")
	}
	for _, rec := range turns {
		content := strings.TrimSpace(rec.Content)
		if content == "" {
			continue
		}
		if runeCount(content) > 800 {
			content = string([]rune(content)[:800]) + "..."
		}
		sb.WriteString("- ")
		sb.WriteString(string(rec.Role))
		sb.WriteString(": ")
		sb.WriteString(content)
		sb.WriteString("\n")
	}
	sb.WriteString("\nUser staff request:\n")
	sb.WriteString(request)
	sb.WriteString("\n")
	return []core.LlmMessage{{Role: core.RoleUser, Content: sb.String()}}
}

func parseStaffDraftLLMResponse(raw, request string) (StaffDraft, string, error) {
	raw = strings.TrimSpace(stripFences(raw))
	var parsed staffDraftLLMResponse
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return StaffDraft{}, "", fmt.Errorf("parse staff draft JSON: %w", err)
	}
	if strings.TrimSpace(parsed.NeedsClarification) != "" {
		return StaffDraft{}, strings.TrimSpace(parsed.NeedsClarification), nil
	}
	parsed.ID = strings.TrimSpace(strings.ToLower(parsed.ID))
	parsed.DisplayName = strings.TrimSpace(parsed.DisplayName)
	parsed.Description = strings.TrimSpace(parsed.Description)
	parsed.Soul = strings.TrimSpace(parsed.Soul)
	if err := core.ValidateStaffID(parsed.ID); err != nil {
		return StaffDraft{}, "", err
	}
	if parsed.DisplayName == "" {
		parsed.DisplayName = parsed.ID
	}
	if parsed.Description == "" {
		parsed.Description = staffDescription(parsed.DisplayName, request)
	}
	if len(parsed.Aliases) == 0 {
		parsed.Aliases = staffAliases(parsed.DisplayName, parsed.DisplayName, parsed.ID)
	}
	if parsed.Soul == "" {
		parsed.Soul = staffSoulDraft(StaffDraft{
			ID:          parsed.ID,
			DisplayName: parsed.DisplayName,
			Description: parsed.Description,
			Aliases:     parsed.Aliases,
		})
	}
	now := time.Now().UTC()
	return StaffDraft{
		ID:          parsed.ID,
		DisplayName: parsed.DisplayName,
		Description: parsed.Description,
		Aliases:     uniqueStrings(parsed.Aliases),
		Soul:        parsed.Soul,
		Source:      "natural_language_llm",
		CreatedAt:   now.Format(time.RFC3339),
		ExpiresAt:   now.Add(staffDraftTTL).Format(time.RFC3339),
	}, "", nil
}
