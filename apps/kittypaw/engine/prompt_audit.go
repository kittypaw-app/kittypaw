package engine

import (
	"encoding/json"

	"github.com/jinto/kittypaw/core"
)

var promptLayerManifest = []string{
	"soul",
	"identity",
	"execution",
	"quality",
	"channel_hint",
	"channel_delivery",
	"runtime_context",
	"workspace_guide",
	"staff_dispatch",
	"skills",
	"skill_creation",
	"memory",
	"mcp_tools",
	"staff_user_notes",
	"memory_context",
	"observations",
	"history",
}

type PromptAuditSource struct {
	ChatID        string `json:"chat_id,omitempty"`
	ChannelUserID string `json:"channel_user_id,omitempty"`
	MessageID     string `json:"message_id,omitempty"`
}

type PromptAudit struct {
	PromptHash     string                 `json:"prompt_hash"`
	Layers         []string               `json:"layers"`
	StaffID        string                 `json:"staff_id,omitempty"`
	StaffRoute     StaffRouteDecision     `json:"staff_route"`
	ConversationID string                 `json:"conversation_id,omitempty"`
	Channel        string                 `json:"channel,omitempty"`
	Source         PromptAuditSource      `json:"source,omitempty"`
	Attempt        int                    `json:"attempt"`
	ObserveRound   int                    `json:"observe_round"`
	RecentWindow   int                    `json:"recent_window"`
	ModelOverride  string                 `json:"model_override,omitempty"`
	Mode           string                 `json:"mode"`
	Delegation     *PromptAuditDelegation `json:"delegation,omitempty"`
}

type PromptAuditDelegation struct {
	ParentConversationID   string `json:"parent_conversation_id,omitempty"`
	DelegateConversationID string `json:"delegate_conversation_id,omitempty"`
	Task                   string `json:"task,omitempty"`
}

func BuildPromptAudit(messages []core.LlmMessage, runtime PromptRuntimeContext, compaction CompactionConfig, attempt, observeRound int, modelOverride string) PromptAudit {
	mode := "interactive"
	if runtime.Background {
		mode = "background"
	}
	var delegation *PromptAuditDelegation
	if runtime.Delegated {
		mode = "delegated"
		delegation = &PromptAuditDelegation{
			ParentConversationID:   runtime.ParentConversationID,
			DelegateConversationID: runtime.DelegateConversationID,
			Task:                   runtime.DelegationTask,
		}
	}
	return PromptAudit{
		PromptHash:     promptMessagesHash(messages),
		Layers:         append([]string(nil), promptLayerManifest...),
		StaffID:        runtime.StaffID,
		StaffRoute:     runtime.StaffRoute,
		ConversationID: runtime.ConversationID,
		Channel:        runtime.ChannelName,
		Source: PromptAuditSource{
			ChatID:        runtime.ChatID,
			ChannelUserID: runtime.ChannelUserID,
			MessageID:     runtime.MessageID,
		},
		Attempt:       attempt,
		ObserveRound:  observeRound,
		RecentWindow:  compaction.RecentWindow,
		ModelOverride: modelOverride,
		Mode:          mode,
		Delegation:    delegation,
	}
}

func (a PromptAudit) MetadataJSON() string {
	data, err := json.Marshal(a)
	if err != nil {
		return ""
	}
	return string(data)
}

func promptMessagesHash(messages []core.LlmMessage) string {
	data, err := json.Marshal(messages)
	if err != nil {
		return ""
	}
	return first16Hex(data)
}
