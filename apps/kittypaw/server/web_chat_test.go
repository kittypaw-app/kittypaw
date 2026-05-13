package server

import (
	"os"
	"strings"
	"testing"
)

func TestWebChatUsesCookieForAuthenticatedBrowserSession(t *testing.T) {
	src, err := os.ReadFile("web/chat.js")
	if err != nil {
		t.Fatalf("read web chat: %v", err)
	}
	body := string(src)
	if !strings.Contains(body, "App.apiKey && !App.authRequired") {
		t.Fatalf("chat websocket must not append bootstrap token for authenticated browser sessions")
	}
}

func TestWebChatUsesChatScopedSocketOnChatSurface(t *testing.T) {
	src, err := os.ReadFile("web/chat.js")
	if err != nil {
		t.Fatalf("read web chat: %v", err)
	}
	body := string(src)
	if !strings.Contains(body, "App.chatOnly ? '/chat/ws' : '/ws'") {
		t.Fatalf("chat surface must fall back to /chat/ws, got:\n%s", body)
	}
}

func TestChatWebModuleSendsConversationIDWhenMountedWithScope(t *testing.T) {
	src, err := os.ReadFile("web/chat.js")
	if err != nil {
		t.Fatalf("read web chat: %v", err)
	}
	body := string(src)
	for _, token := range []string{
		"mount(container, options = {})",
		"this.conversationID = options.conversationID || ''",
		"conversationID: this.conversationID",
		"conversation_id: this.pendingTurn.conversationID",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("chat module missing scoped conversation token %q", token)
		}
	}
}

func TestChatWebModuleSendsTurnIDAndReplaysPendingTurn(t *testing.T) {
	src, err := os.ReadFile("web/chat.js")
	if err != nil {
		t.Fatalf("read web chat: %v", err)
	}
	body := string(src)
	for _, token := range []string{
		"pendingTurn: null",
		"_newTurnID()",
		"turn_id: this.pendingTurn.id",
		"this.pendingTurn = {",
		"this._sendPendingTurn();",
		"if (this.pendingTurn && msg.turn_id && msg.turn_id !== this.pendingTurn.id) return;",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("chat module missing browser turn replay token %q", token)
		}
	}
}

func TestWebSettingsDoesNotLaunchSetupWizard(t *testing.T) {
	src, err := os.ReadFile("web/settings.js")
	if err != nil {
		t.Fatalf("read web settings: %v", err)
	}
	body := string(src)
	if strings.Contains(body, "launchWizard") || strings.Contains(body, "Setup Wizard") || strings.Contains(body, "setup wizard") {
		t.Fatalf("settings must not route users into browser onboarding, got:\n%s", body)
	}
	if !strings.Contains(body, "/api/settings/llm") || !strings.Contains(body, "/api/settings/telegram") {
		t.Fatalf("settings must use post-setup settings APIs, got:\n%s", body)
	}
}

func TestWebSettingsDoesNotExposeWorkspaceManagement(t *testing.T) {
	src, err := os.ReadFile("web/settings.js")
	if err != nil {
		t.Fatalf("read web settings: %v", err)
	}
	body := string(src)
	for _, token := range []string{"/api/settings/workspaces", "Workspace", "Add Workspace", "_showWorkspaceForm", "_selectedWorkspacePath"} {
		if strings.Contains(body, token) {
			t.Fatalf("settings must not expose workspace management token %s, got:\n%s", token, body)
		}
	}
}

func TestProjectsFolderPickerHasFinderStyleLayout(t *testing.T) {
	src, err := os.ReadFile("web/style.css")
	if err != nil {
		t.Fatalf("read web style: %v", err)
	}
	body := string(src)
	for _, token := range []string{
		".projects-dir-body",
		".projects-dir-sidebar",
		".projects-dir-breadcrumb",
		".projects-dir-crumb",
		".projects-dir-main",
		".projects-dir-footer",
		".projects-dir-selected-path",
		"grid-template-columns",
		"overflow-y: auto",
		"max-height",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("projects folder picker CSS missing token %s, got:\n%s", token, body)
		}
	}
}

func TestWebShellDoesNotExposeProjectsInSidebar(t *testing.T) {
	src, err := os.ReadFile("web/app.js")
	if err != nil {
		t.Fatalf("read web app: %v", err)
	}
	body := string(src)
	if strings.Contains(body, `href="/projects">Projects`) || strings.Contains(body, "projectsNav") {
		t.Fatalf("main settings shell must not expose Projects in the sidebar, got:\n%s", body)
	}
}
