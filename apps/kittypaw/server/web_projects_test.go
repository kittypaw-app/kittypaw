package server

import (
	"os"
	"strings"
	"testing"
)

func readWebAssetForProjectsTest(t *testing.T, path string) string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(src)
}

func TestProjectsWebAssetsAreLoadedAndMounted(t *testing.T) {
	index := readWebAssetForProjectsTest(t, "web/index.html")
	if !strings.Contains(index, `<script src="/projects.js"></script>`) {
		t.Fatal("web index must load projects.js")
	}
	if strings.Contains(index, `<script src="/kanban.js"></script>`) {
		t.Fatal("web index must not load kanban.js")
	}

	app := readWebAssetForProjectsTest(t, "web/app.js")
	for _, token := range []string{
		"projectsOnly: false",
		"isProjectsSurface()",
		"async startProjectsFlow()",
		"showProjectsSurface()",
		"Projects.mount(document.getElementById('projects-panel'))",
		"this.chatOnly || this.projectsOnly",
	} {
		if !strings.Contains(app, token) {
			t.Fatalf("web app missing Projects surface token %s", token)
		}
	}
	for _, legacy := range []string{"kanbanOnly", "isKanbanSurface", "showKanbanSurface", "Kanban.mount"} {
		if strings.Contains(app, legacy) {
			t.Fatalf("web app must not keep legacy Kanban surface token %s", legacy)
		}
	}
}

func TestProjectsWebModuleUsesProjectsTicketsJobsAndDriversAPIs(t *testing.T) {
	src := readWebAssetForProjectsTest(t, "web/projects.js")
	for _, status := range []string{"draft", "backlog", "ready", "in_progress", "blocked", "review", "done"} {
		if !strings.Contains(src, `key: '`+status+`'`) {
			t.Fatalf("projects module missing status column %q", status)
		}
	}
	for _, endpoint := range []string{
		"/api/v1/projects",
		"/api/v1/projects/' + encodeURIComponent(projectKey) + '/board",
		"/api/v1/tickets",
		"/api/v1/tickets/' + encodeURIComponent(ticketID)",
		"/api/v1/tickets/' + encodeURIComponent(this._selectedTicketID) + '/actions",
		"/api/v1/tickets/' + encodeURIComponent(this._selectedTicketID) + '/archive",
		"/api/v1/tickets/' + encodeURIComponent(this._selectedTicketID) + '/jobs/plan",
		"/api/v1/drivers",
	} {
		if !strings.Contains(src, endpoint) {
			t.Fatalf("projects module missing endpoint %s", endpoint)
		}
	}
	for _, legacy := range []string{
		"/api/v1/kanban",
		"/api/settings/workspaces",
		"workspace_id",
		"Kanban",
		"kanbanT(",
	} {
		if strings.Contains(src, legacy) {
			t.Fatalf("projects module must not use legacy token %s", legacy)
		}
	}
}

func TestProjectsWebIncludesJobRuntimeControls(t *testing.T) {
	src := readWebAssetForProjectsTest(t, "web/projects.js")
	for _, token := range []string{
		"_selectedJob",
		"_startJob",
		"_cancelJob",
		"_loadJobLogs",
		"_promptGitInit",
		"/api/v1/projects/' + encodeURIComponent(project.id || project.key) + '/git/init",
		"/api/v1/jobs/' + encodeURIComponent(this._selectedJob) + '/start",
		"/api/v1/jobs/' + encodeURIComponent(this._selectedJob) + '/cancel",
		"/api/v1/jobs/' + encodeURIComponent(jobID) + '/logs",
		"Open Worktree",
	} {
		if !strings.Contains(src, token) {
			t.Fatalf("projects module missing job runtime token %s", token)
		}
	}
}

func TestProjectsWebModuleMountsProjectAndTicketChats(t *testing.T) {
	src := readWebAssetForProjectsTest(t, "web/projects.js")
	for _, token := range []string{
		"_projectChatHTML",
		"_ticketChatHTML",
		"data-project-tab=\"' + escHTMLAttr(tab.key)",
		"projects-project-chat-panel",
		"projects-ticket-chat-panel",
		"project.project_conversation_id",
		"ticket.ticket_conversation_id",
		"Chat.mount(projectChatPanel, { conversationID: project.project_conversation_id || '' })",
		"Chat.mount(ticketChatPanel, { conversationID: ticket.ticket_conversation_id || '' })",
	} {
		if !strings.Contains(src, token) {
			t.Fatalf("projects module missing chat token %s", token)
		}
	}
}

func TestProjectsWebModuleCreatesProjectsFromFoldersDirectly(t *testing.T) {
	src := readWebAssetForProjectsTest(t, "web/projects.js")
	for _, token := range []string{
		"id=\"projects-project-form\"",
		"id=\"projects-folder-path\"",
		"Project Folder",
		"_resolveProjectPathForSave",
		"/api/settings/directories",
		"root_path: projectPath",
		"key: document.getElementById('projects-project-key').value.trim()",
		"name: document.getElementById('projects-project-name').value.trim()",
	} {
		if !strings.Contains(src, token) {
			t.Fatalf("projects module missing project-folder token %s", token)
		}
	}
	for _, bad := range []string{"Workspace", "Add Workspace", "_workspaceAliasAuto", "_selectedWorkspacePath"} {
		if strings.Contains(src, bad) {
			t.Fatalf("projects module must not expose workspace UI token %s", bad)
		}
	}
}

func TestProjectsWebStylesProvideBoardDrawerAndResponsiveRules(t *testing.T) {
	src := readWebAssetForProjectsTest(t, "web/style.css")
	for _, token := range []string{
		".projects-surface",
		".projects-view",
		".projects-toolbar",
		".projects-board",
		".projects-column",
		".projects-ticket",
		".projects-drawer",
		".projects-status-dot",
		".projects-column--in_progress .projects-status-dot",
		".projects-form",
		"@media (max-width: 900px)",
	} {
		if !strings.Contains(src, token) {
			t.Fatalf("projects styles missing token %s", token)
		}
	}
}
