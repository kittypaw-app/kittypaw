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

func TestWebSettingsManagesAccountWorkspaces(t *testing.T) {
	src, err := os.ReadFile("web/settings.js")
	if err != nil {
		t.Fatalf("read web settings: %v", err)
	}
	body := string(src)
	if !strings.Contains(body, "/api/settings/workspaces") {
		t.Fatalf("settings must use account-scoped workspace settings APIs, got:\n%s", body)
	}
	for _, token := range []string{
		"/api/settings/directories",
		`id="settings-workspace-path"`,
		`id="settings-directory-breadcrumb"`,
		`id="settings-workspace-selected"`,
		"settings-dir-body",
		"settings-dir-sidebar",
		"settings-dir-list",
		"Add Workspace",
		"keydown",
		"_workspaceBreadcrumbs",
		"_suggestWorkspaceAlias",
		"pathInput.value = previousPath;",
		"selected.textContent = previousPath || settingsT('settings.noFolderSelected');",
		"_directoryPickerRequestID",
		"const requestID = ++this._directoryPickerRequestID;",
		"if (requestID !== this._directoryPickerRequestID) return false;",
		"_resolveWorkspacePathForSave",
		"_renderDirectoryEntries",
		"document.createElement('button')",
		"button.dataset.path =",
		"name.textContent =",
		"sub.textContent =",
		"_workspaceAliasAuto",
		"aliasInput.addEventListener('input'",
		"pathInput.classList.add('is-loading')",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("settings workspace picker missing token %s, got:\n%s", token, body)
		}
	}
	for _, token := range []string{
		`data-path="${esc`,
		"entries.map(entry =>",
		"list.innerHTML =",
	} {
		if strings.Contains(body, token) {
			t.Fatalf("settings workspace picker must not build dynamic path HTML with %s, got:\n%s", token, body)
		}
	}
	if !strings.Contains(body, "Workspace") || !strings.Contains(body, "Alias") {
		t.Fatalf("settings must expose workspace alias controls, got:\n%s", body)
	}
}

func TestWebSettingsWorkspacePickerHasFinderStyleLayout(t *testing.T) {
	src, err := os.ReadFile("web/style.css")
	if err != nil {
		t.Fatalf("read web style: %v", err)
	}
	body := string(src)
	for _, token := range []string{
		".settings-dir-body",
		".settings-dir-sidebar",
		".settings-dir-breadcrumb",
		".settings-dir-crumb",
		".settings-dir-main",
		".settings-dir-footer",
		".settings-dir-selected-path",
		"grid-template-columns",
		"overflow-y: auto",
		"max-height",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("settings workspace picker CSS missing token %s, got:\n%s", token, body)
		}
	}
}

func TestWebShellDoesNotExposeKanbanInSidebar(t *testing.T) {
	src, err := os.ReadFile("web/app.js")
	if err != nil {
		t.Fatalf("read web app: %v", err)
	}
	body := string(src)
	if strings.Contains(body, `href="/kanban">Kanban`) || strings.Contains(body, "kanbanNav") {
		t.Fatalf("main settings shell must not expose Kanban in the sidebar, got:\n%s", body)
	}
}
