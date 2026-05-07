package engine

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/store"
)

const staffDraftTTL = 24 * time.Hour

type StaffDraft struct {
	ID          string   `json:"id"`
	DisplayName string   `json:"display_name"`
	Description string   `json:"description"`
	Aliases     []string `json:"aliases"`
	Soul        string   `json:"soul"`
	Source      string   `json:"source"`
	CreatedAt   string   `json:"created_at"`
	ExpiresAt   string   `json:"expires_at"`
}

var staffIDUnsafe = regexp.MustCompile(`[^a-z0-9_-]+`)

func pendingStaffDraftKey(conversationID string) string {
	if strings.TrimSpace(conversationID) == "" {
		conversationID = "default"
	}
	return "pending_staff_draft:" + conversationID
}

func pendingStaffOfferKey(conversationID string) string {
	if strings.TrimSpace(conversationID) == "" {
		conversationID = "default"
	}
	return "pending_staff_offer:" + conversationID
}

func pendingStaffSwitchKey(conversationID string) string {
	if strings.TrimSpace(conversationID) == "" {
		conversationID = "default"
	}
	return "pending_staff_switch:" + conversationID
}

func buildStaffDraft(role, source string) StaffDraft {
	role = strings.TrimSpace(role)
	displayName := staffDisplayName(role)
	id := staffIDFromRole(role)
	description := staffDescription(displayName, role)
	now := time.Now().UTC()
	draft := StaffDraft{
		ID:          id,
		DisplayName: displayName,
		Description: description,
		Aliases:     staffAliases(role, displayName, id),
		Source:      strings.TrimSpace(source),
		CreatedAt:   now.Format(time.RFC3339),
		ExpiresAt:   now.Add(staffDraftTTL).Format(time.RFC3339),
	}
	if draft.Source == "" {
		draft.Source = "chat"
	}
	draft.Soul = staffSoulDraft(draft)
	return draft
}

func staffDisplayName(role string) string {
	compact := strings.ReplaceAll(strings.ToLower(role), " ", "")
	if strings.Contains(compact, "개발") && (strings.Contains(compact, "pm") || strings.Contains(compact, "피엠")) {
		return "개발 PM"
	}
	if strings.TrimSpace(role) == "" {
		return "새 Staff"
	}
	return strings.TrimSpace(role)
}

func staffIDFromRole(role string) string {
	lower := strings.ToLower(strings.TrimSpace(role))
	compact := strings.ReplaceAll(lower, " ", "")
	if strings.Contains(compact, "개발") && (strings.Contains(compact, "pm") || strings.Contains(compact, "피엠")) {
		return "dev-pm"
	}

	var parts []string
	for _, mapping := range []struct {
		needles []string
		part    string
	}{
		{[]string{"개발", "dev", "developer"}, "dev"},
		{[]string{"pm", "피엠", "프로젝트", "project"}, "pm"},
		{[]string{"디자인", "디자이너", "design"}, "designer"},
		{[]string{"재무", "finance"}, "finance"},
		{[]string{"법무", "legal"}, "legal"},
		{[]string{"마케팅", "marketing"}, "marketing"},
		{[]string{"운영", "ops"}, "ops"},
		{[]string{"데이터", "data"}, "data"},
		{[]string{"qa", "테스트", "test"}, "qa"},
	} {
		for _, needle := range mapping.needles {
			if strings.Contains(lower, needle) {
				parts = append(parts, mapping.part)
				break
			}
		}
	}
	id := strings.Join(uniqueStrings(parts), "-")
	if id == "" {
		id = staffIDUnsafe.ReplaceAllString(lower, "-")
		id = strings.Trim(id, "-_")
	}
	if id == "" {
		sum := sha1.Sum([]byte(role))
		id = "staff-" + hex.EncodeToString(sum[:])[:8]
	}
	if err := core.ValidateStaffID(id); err != nil {
		sum := sha1.Sum([]byte(role))
		id = "staff-" + hex.EncodeToString(sum[:])[:8]
	}
	return id
}

func staffDescription(displayName, role string) string {
	if displayName == "개발 PM" {
		return "요구사항 정리, 일정 관리, 우선순위 조율, 진행상황 추적, blocker 관리"
	}
	role = strings.TrimSpace(role)
	if role == "" {
		return "사용자가 지정한 역할을 담당하는 staff"
	}
	return role + " 역할을 담당하는 staff"
}

func staffAliases(role, displayName, id string) []string {
	candidates := []string{role, displayName, strings.ReplaceAll(role, " ", ""), strings.ReplaceAll(displayName, " ", "")}
	if displayName == "개발 PM" {
		candidates = append(candidates, "PM", "pm")
	}
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || candidate == id || staffContainsString(out, candidate) {
			continue
		}
		out = append(out, candidate)
	}
	return out
}

func staffSoulDraft(draft StaffDraft) string {
	return fmt.Sprintf(`You are %s, a KittyPaw staff member.

## Role
%s

## Working Style
- Clarify goals, constraints, owners, and blockers before proposing work.
- Keep plans practical and ordered by priority.
- Track decisions and open questions explicitly.
- Respond in the same language the user uses.
- Be concise unless the user asks for detail.
`, draft.DisplayName, draft.Description)
}

func savePendingStaffDraft(st *store.Store, conversationID string, draft StaffDraft) error {
	if st == nil {
		return fmt.Errorf("store is required")
	}
	data, err := json.Marshal(draft)
	if err != nil {
		return err
	}
	return st.SetUserContext(pendingStaffDraftKey(conversationID), string(data), "staff_draft")
}

func loadPendingStaffDraft(st *store.Store, conversationID string) (StaffDraft, bool, error) {
	if st == nil {
		return StaffDraft{}, false, nil
	}
	raw, ok, err := st.GetUserContext(pendingStaffDraftKey(conversationID))
	if err != nil || !ok {
		return StaffDraft{}, false, err
	}
	var draft StaffDraft
	if err := json.Unmarshal([]byte(raw), &draft); err != nil {
		return StaffDraft{}, false, err
	}
	expiresAt, err := time.Parse(time.RFC3339, draft.ExpiresAt)
	if err == nil && time.Now().UTC().After(expiresAt) {
		_, _ = st.DeleteUserContext(pendingStaffDraftKey(conversationID))
		return StaffDraft{}, false, nil
	}
	return draft, true, nil
}

func clearPendingStaffDraft(st *store.Store, conversationID string) error {
	if st == nil {
		return nil
	}
	_, err := st.DeleteUserContext(pendingStaffDraftKey(conversationID))
	return err
}

func savePendingStaffOffer(st *store.Store, conversationID, role string) error {
	if st == nil {
		return fmt.Errorf("store is required")
	}
	return st.SetUserContext(pendingStaffOfferKey(conversationID), strings.TrimSpace(role), "staff_draft")
}

func loadPendingStaffOffer(st *store.Store, conversationID string) (string, bool, error) {
	if st == nil {
		return "", false, nil
	}
	return st.GetUserContext(pendingStaffOfferKey(conversationID))
}

func clearPendingStaffOffer(st *store.Store, conversationID string) error {
	if st == nil {
		return nil
	}
	_, err := st.DeleteUserContext(pendingStaffOfferKey(conversationID))
	return err
}

func savePendingStaffSwitch(st *store.Store, conversationID, staffID string) error {
	if st == nil {
		return fmt.Errorf("store is required")
	}
	return st.SetUserContext(pendingStaffSwitchKey(conversationID), staffID, "staff_draft")
}

func loadPendingStaffSwitch(st *store.Store, conversationID string) (string, bool, error) {
	if st == nil {
		return "", false, nil
	}
	return st.GetUserContext(pendingStaffSwitchKey(conversationID))
}

func clearPendingStaffSwitch(st *store.Store, conversationID string) error {
	if st == nil {
		return nil
	}
	_, err := st.DeleteUserContext(pendingStaffSwitchKey(conversationID))
	return err
}

func commitStaffDraft(baseDir string, st *store.Store, draft StaffDraft) error {
	if st == nil {
		return fmt.Errorf("store is required")
	}
	if err := core.ValidateStaffID(draft.ID); err != nil {
		return err
	}
	if _, ok, err := st.GetStaffMeta(draft.ID); err != nil {
		return err
	} else if ok {
		return fmt.Errorf("staff %q already exists", draft.ID)
	}
	for _, alias := range draft.Aliases {
		resolved, ok, err := st.ResolveStaffID(alias)
		if err != nil {
			return err
		}
		if ok && resolved != draft.ID {
			return fmt.Errorf("staff alias %q already belongs to %q", alias, resolved)
		}
	}
	base, err := core.ResolveBaseDir(baseDir)
	if err != nil {
		return err
	}
	staffDir := filepath.Join(base, "staff", draft.ID)
	if err := os.MkdirAll(staffDir, 0o755); err != nil {
		return fmt.Errorf("create staff dir: %w", err)
	}
	soulPath := filepath.Join(staffDir, "SOUL.md")
	if err := os.WriteFile(soulPath, []byte(draft.Soul), 0o644); err != nil {
		return fmt.Errorf("write SOUL.md: %w", err)
	}
	if err := st.UpsertStaffMetaWithDisplayName(draft.ID, draft.DisplayName, draft.Description, "[]", "chat"); err != nil {
		_ = os.Remove(soulPath)
		return err
	}
	if err := st.ReplaceStaffAliases(draft.ID, draft.Aliases); err != nil {
		return err
	}
	return nil
}

func uniqueStrings(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item == "" || staffContainsString(out, item) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func staffContainsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
