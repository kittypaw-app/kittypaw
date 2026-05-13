package engine

import (
	"fmt"
	"strings"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/store"
)

func setConversationStaff(baseDir string, st *store.Store, conversationID, staffRef string) (string, error) {
	if st == nil {
		return "", fmt.Errorf("store is required")
	}
	base, err := core.ResolveBaseDir(baseDir)
	if err != nil {
		return "", err
	}
	id, ok, err := core.ResolveStaffReference(base, staffRef)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("staff %q를 찾지 못했습니다", staffRef)
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		if err := st.SetConversationStaff(id); err != nil {
			return "", err
		}
		return id, nil
	}
	if _, ok, err := st.Conversation(conversationID); err != nil {
		return "", err
	} else if !ok {
		scopeID := strings.TrimPrefix(conversationID, "general:")
		if scopeID == conversationID {
			scopeID = conversationID
		}
		if err := st.EnsureConversation(conversationID, "general", scopeID); err != nil {
			return "", err
		}
	}
	if _, err := st.SetConversationDefaultStaff(conversationID, id); err != nil {
		return "", err
	}
	return id, nil
}

func conversationDefaultStaff(base string, st *store.Store, conversationID string) (string, bool) {
	if st == nil {
		return "", false
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return "", false
	}
	conversation, ok, err := st.Conversation(conversationID)
	if err != nil || !ok {
		return "", false
	}
	if conversation.DefaultStaffID == "" || !core.StaffHasSoul(base, conversation.DefaultStaffID) {
		return "", false
	}
	return conversation.DefaultStaffID, true
}
