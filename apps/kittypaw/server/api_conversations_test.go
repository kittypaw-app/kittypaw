package server

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/store"
)

func TestChatHistoryCanBeScopedToConversation(t *testing.T) {
	srv := newProjectsAPITestServer(t)

	if err := srv.store.SetConversationScope("project:alpha", "project", "alpha"); err != nil {
		t.Fatalf("set project scope: %v", err)
	}
	if err := srv.store.AddConversationTurn(&core.ConversationTurn{
		ConversationID: store.DefaultConversationID,
		Role:           core.RoleUser,
		Content:        "general-only",
		Timestamp:      "1",
	}); err != nil {
		t.Fatalf("add general turn: %v", err)
	}
	if err := srv.store.AddConversationTurn(&core.ConversationTurn{
		ConversationID: "project:alpha",
		Role:           core.RoleUser,
		Content:        "project-only",
		Timestamp:      "2",
	}); err != nil {
		t.Fatalf("add project turn: %v", err)
	}

	var body struct {
		Turns []struct {
			Content        string `json:"content"`
			ConversationID string `json:"conversation_id"`
		} `json:"turns"`
	}
	projectsAPIRequest(t, srv, http.MethodGet, "/api/v1/chat/history?conversation_id=project:alpha", nil, http.StatusOK, &body)
	if len(body.Turns) != 1 {
		t.Fatalf("turns = %+v, want one project turn", body.Turns)
	}
	if body.Turns[0].Content != "project-only" || body.Turns[0].ConversationID != "project:alpha" {
		t.Fatalf("turn = %+v, want project-only scoped turn", body.Turns[0])
	}
}

func TestConversationsAPIListsInfoAndMessages(t *testing.T) {
	srv := newProjectsAPITestServer(t)

	if err := srv.store.SetConversationScope("project:alpha", "project", "alpha"); err != nil {
		t.Fatalf("set project scope: %v", err)
	}
	if err := srv.store.AddConversationTurn(&core.ConversationTurn{
		ConversationID: "project:alpha",
		Role:           core.RoleUser,
		Content:        "project message",
		Timestamp:      "1",
	}); err != nil {
		t.Fatalf("add project turn: %v", err)
	}

	var list struct {
		Conversations []struct {
			ID           string `json:"id"`
			ScopeType    string `json:"scope_type"`
			MessageCount int    `json:"message_count"`
			LastMessage  string `json:"last_message"`
		} `json:"conversations"`
	}
	projectsAPIRequest(t, srv, http.MethodGet, "/api/v1/conversations", nil, http.StatusOK, &list)
	found := false
	for _, conv := range list.Conversations {
		if conv.ID == "project:alpha" {
			found = true
			if conv.ScopeType != "project" || conv.MessageCount != 1 || conv.LastMessage != "project message" {
				t.Fatalf("project conversation = %+v, want scoped metadata", conv)
			}
		}
	}
	if !found {
		t.Fatalf("conversations = %+v, want project:alpha", list.Conversations)
	}

	escapedID := url.PathEscape("project:alpha")
	var info struct {
		Conversation struct {
			ID        string `json:"id"`
			ScopeType string `json:"scope_type"`
		} `json:"conversation"`
	}
	projectsAPIRequest(t, srv, http.MethodGet, "/api/v1/conversations/"+escapedID, nil, http.StatusOK, &info)
	if info.Conversation.ID != "project:alpha" || info.Conversation.ScopeType != "project" {
		t.Fatalf("conversation info = %+v, want project:alpha", info.Conversation)
	}

	var messages struct {
		Turns []struct {
			Content string `json:"content"`
		} `json:"turns"`
	}
	projectsAPIRequest(t, srv, http.MethodGet, "/api/v1/conversations/"+escapedID+"/messages", nil, http.StatusOK, &messages)
	if len(messages.Turns) != 1 || messages.Turns[0].Content != "project message" {
		t.Fatalf("messages = %+v, want project message", messages.Turns)
	}
}

func TestConversationsAPICreatesGeneralConversation(t *testing.T) {
	srv := newProjectsAPITestServer(t)

	var created struct {
		Conversation struct {
			ID        string `json:"id"`
			ScopeType string `json:"scope_type"`
			Title     string `json:"title"`
		} `json:"conversation"`
	}
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/conversations", map[string]string{
		"title": "Research thread",
	}, http.StatusCreated, &created)

	if created.Conversation.ID == "" || created.Conversation.ScopeType != "general" || created.Conversation.Title != "Research thread" {
		t.Fatalf("created conversation = %+v, want titled general conversation", created.Conversation)
	}

	var info struct {
		Conversation struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"conversation"`
	}
	projectsAPIRequest(t, srv, http.MethodGet, "/api/v1/conversations/"+url.PathEscape(created.Conversation.ID), nil, http.StatusOK, &info)
	if info.Conversation.ID != created.Conversation.ID || info.Conversation.Title != "Research thread" {
		t.Fatalf("conversation info = %+v, want created conversation", info.Conversation)
	}
}

func TestConversationsAPIUpdatesDefaultStaffID(t *testing.T) {
	srv := newProjectsAPITestServer(t)
	seedServerActiveStaff(t, srv.runtime.BaseDir, "dev-pm", "development pm")
	conv, err := srv.store.CreateConversation(store.CreateConversationRequest{
		ScopeType: "general",
		ScopeID:   "staff-route",
	})
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}

	var updated struct {
		Conversation struct {
			ID             string `json:"id"`
			DefaultStaffID string `json:"default_staff_id"`
		} `json:"conversation"`
	}
	projectsAPIRequest(t, srv, http.MethodPatch, "/api/v1/conversations/"+url.PathEscape(conv.ID), map[string]string{
		"default_staff_id": "dev-pm",
	}, http.StatusOK, &updated)
	if updated.Conversation.ID != conv.ID || updated.Conversation.DefaultStaffID != "dev-pm" {
		t.Fatalf("updated conversation = %+v, want dev-pm", updated.Conversation)
	}
}

func seedServerActiveStaff(t *testing.T, base, id, soul string) {
	t.Helper()
	if err := core.WriteStaffDraft(base, core.StaffMetaFile{ID: id}, soul); err != nil {
		t.Fatalf("WriteStaffDraft(%s): %v", id, err)
	}
	if err := core.ActivateStaffDraft(base, id); err != nil {
		t.Fatalf("ActivateStaffDraft(%s): %v", id, err)
	}
}

func TestConversationInfoIncludesRolloverMetadata(t *testing.T) {
	srv := newProjectsAPITestServer(t)
	parent, err := srv.store.CreateConversation(store.CreateConversationRequest{ScopeType: "general", ScopeID: "parent"})
	if err != nil {
		t.Fatalf("CreateConversation(parent): %v", err)
	}
	child, err := srv.store.CreateConversation(store.CreateConversationRequest{
		ScopeType:            "general",
		ScopeID:              "child",
		ParentConversationID: parent.ID,
		RolloverReason:       "length_turns",
		RolloverFromTurnID:   42,
		SourceChannel:        "web_chat",
		SourceSessionID:      "sess-1",
		ChatID:               "chat-1",
	})
	if err != nil {
		t.Fatalf("CreateConversation(child): %v", err)
	}

	var info struct {
		Conversation struct {
			ID                   string `json:"id"`
			ParentConversationID string `json:"parent_conversation_id"`
			RolloverReason       string `json:"rollover_reason"`
			RolloverFromTurnID   int64  `json:"rollover_from_turn_id"`
		} `json:"conversation"`
	}
	projectsAPIRequest(t, srv, http.MethodGet, "/api/v1/conversations/"+url.PathEscape(child.ID), nil, http.StatusOK, &info)
	if info.Conversation.ParentConversationID != parent.ID ||
		info.Conversation.RolloverReason != "length_turns" ||
		info.Conversation.RolloverFromTurnID != 42 {
		t.Fatalf("conversation info = %+v", info.Conversation)
	}
}

func TestChatForgetCanBeScopedToConversation(t *testing.T) {
	srv := newProjectsAPITestServer(t)

	if err := srv.store.SetConversationScope("project:alpha", "project", "alpha"); err != nil {
		t.Fatalf("set project scope: %v", err)
	}
	for _, turn := range []core.ConversationTurn{
		{ConversationID: store.DefaultConversationID, Role: core.RoleUser, Content: "general", Timestamp: "1"},
		{ConversationID: "project:alpha", Role: core.RoleUser, Content: "project", Timestamp: "2"},
	} {
		if err := srv.store.AddConversationTurn(&turn); err != nil {
			t.Fatalf("add turn %+v: %v", turn, err)
		}
	}

	var body struct {
		TurnsDeleted int `json:"turns_deleted"`
	}
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/chat/forget", map[string]string{
		"conversation_id": "project:alpha",
	}, http.StatusOK, &body)
	if body.TurnsDeleted != 1 {
		t.Fatalf("turns_deleted = %d, want one project turn", body.TurnsDeleted)
	}

	general, err := srv.store.ListConversationTurnsForConversation(store.DefaultConversationID, 10)
	if err != nil {
		t.Fatalf("list general: %v", err)
	}
	project, err := srv.store.ListConversationTurnsForConversation("project:alpha", 10)
	if err != nil {
		t.Fatalf("list project: %v", err)
	}
	if len(general) != 1 || general[0].Content != "general" {
		t.Fatalf("general turns = %+v, want preserved general turn", general)
	}
	if len(project) != 0 {
		t.Fatalf("project turns = %+v, want forgotten project conversation", project)
	}
}

func TestCheckpointsListDefaultsToDefaultConversationAndCanBeScoped(t *testing.T) {
	srv := newProjectsAPITestServer(t)

	if err := srv.store.SetConversationScope("project:alpha", "project", "alpha"); err != nil {
		t.Fatalf("set project scope: %v", err)
	}
	if err := srv.store.AddConversationTurn(&core.ConversationTurn{
		ConversationID: store.DefaultConversationID,
		Role:           core.RoleUser,
		Content:        "general",
		Timestamp:      "1",
	}); err != nil {
		t.Fatalf("add general turn: %v", err)
	}
	if _, err := srv.store.CreateCheckpoint("general checkpoint"); err != nil {
		t.Fatalf("create general checkpoint: %v", err)
	}
	if err := srv.store.AddConversationTurn(&core.ConversationTurn{
		ConversationID: "project:alpha",
		Role:           core.RoleUser,
		Content:        "project",
		Timestamp:      "2",
	}); err != nil {
		t.Fatalf("add project turn: %v", err)
	}
	if _, err := srv.store.CreateCheckpointForConversation("project checkpoint", "project:alpha"); err != nil {
		t.Fatalf("create project checkpoint: %v", err)
	}

	var defaults struct {
		Checkpoints []struct {
			Label          string `json:"label"`
			ConversationID string `json:"conversation_id"`
		} `json:"checkpoints"`
	}
	projectsAPIRequest(t, srv, http.MethodGet, "/api/v1/chat/checkpoints", nil, http.StatusOK, &defaults)
	if len(defaults.Checkpoints) != 1 || defaults.Checkpoints[0].ConversationID != store.DefaultConversationID {
		t.Fatalf("default checkpoints = %+v, want only default conversation", defaults.Checkpoints)
	}

	var scoped struct {
		Checkpoints []struct {
			Label          string `json:"label"`
			ConversationID string `json:"conversation_id"`
		} `json:"checkpoints"`
	}
	projectsAPIRequest(t, srv, http.MethodGet, "/api/v1/chat/checkpoints?conversation_id=project:alpha", nil, http.StatusOK, &scoped)
	if len(scoped.Checkpoints) != 1 || scoped.Checkpoints[0].ConversationID != "project:alpha" {
		t.Fatalf("scoped checkpoints = %+v, want only project conversation", scoped.Checkpoints)
	}
}
