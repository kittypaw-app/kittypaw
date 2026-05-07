package server

import (
	"os"
	"strings"
	"testing"
)

func readWebAssetForKanbanTest(t *testing.T, path string) string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(src)
}

func TestKanbanWebAssetsAreLoadedAndMounted(t *testing.T) {
	index := readWebAssetForKanbanTest(t, "web/index.html")
	if !strings.Contains(index, `<script src="/kanban.js"></script>`) {
		t.Fatal("web index must load kanban.js")
	}

	app := readWebAssetForKanbanTest(t, "web/app.js")
	if strings.Contains(app, `data-tab="kanban"`) || strings.Contains(app, "tab === 'kanban'") {
		t.Fatal("control shell must not mount Kanban as a settings tab")
	}
	if !strings.Contains(app, "showKanbanSurface()") || !strings.Contains(app, "Kanban.mount(document.getElementById('kanban-panel'))") {
		t.Fatal("standalone /kanban surface must mount the Kanban module directly")
	}
}

func TestKanbanWebModuleUsesAuthenticatedAPIAndExpectedEndpoints(t *testing.T) {
	src := readWebAssetForKanbanTest(t, "web/kanban.js")
	for _, status := range []string{"triage", "todo", "ready", "running", "blocked", "done"} {
		if !strings.Contains(src, `key: '`+status+`'`) {
			t.Fatalf("kanban module missing status column %q", status)
		}
	}
	for _, endpoint := range []string{
		"/api/v1/projects",
		"/api/v1/kanban/tasks?project=",
		"/api/v1/projects/' + encodeURIComponent(project) + '/boards",
		"/api/v1/projects/' + encodeURIComponent(project) + '/milestones",
		"/api/v1/kanban/tasks/' + encodeURIComponent(taskID)",
	} {
		if !strings.Contains(src, endpoint) {
			t.Fatalf("kanban module missing endpoint %s", endpoint)
		}
	}
	if strings.Contains(src, "apiRaw('/api/v1") || strings.Contains(src, "apiPost('/api/v1") {
		t.Fatal("kanban module must use api(), not apiRaw/apiPost, for /api/v1 routes")
	}
}

func TestKanbanWebModuleSupportsCreateDetailActionsAndRuns(t *testing.T) {
	src := readWebAssetForKanbanTest(t, "web/kanban.js")
	for _, token := range []string{
		"id=\"kanban-project-form\"",
		"id=\"kanban-task-form\"",
		"_requestJSON(url, 'POST', body)",
		"/api/settings/workspaces",
		"id=\"kanban-workspace-select\"",
		"name=\"workspace_id\"",
		"_workspaceByID",
		"for (const candidate of [workspace.alias, workspace.name, workspace.id, 'project'])",
		"/api/v1/kanban/tasks'",
		"/claim'",
		"/complete'",
		"/block'",
		"/unblock'",
		"/comments'",
		"kanban-runs",
		"kanban-comments",
		"id=\"kanban-edit-form\"",
		"id=\"kanban-archive-task\"",
		"task.status === 'running'",
		"method: method",
		"'/api/v1/kanban/tasks/' + encodeURIComponent(this._selectedTaskID)",
		"'/archive'",
		"_updateTask",
		"_archiveTask",
	} {
		if !strings.Contains(src, token) {
			t.Fatalf("kanban module missing token %s", token)
		}
	}
	for _, bad := range []string{
		`name="root_path"`,
		`placeholder="/absolute/path"`,
		"_field(form, 'root_path')",
	} {
		if strings.Contains(src, bad) {
			t.Fatalf("kanban project form must not ask for absolute paths directly, found %s", bad)
		}
	}
}

func TestKanbanWebModuleDirectsEmptyWorkspaceUsersToSettings(t *testing.T) {
	src := readWebAssetForKanbanTest(t, "web/kanban.js")
	for _, token := range []string{
		"Add a workspace in Settings",
		`href="/_settings"`,
		"_workspaces.length === 0",
	} {
		if !strings.Contains(src, token) {
			t.Fatalf("kanban module missing empty-workspace guidance %s", token)
		}
	}
}

func TestKanbanWebModuleSupportsRunLifecycleActions(t *testing.T) {
	src := readWebAssetForKanbanTest(t, "web/kanban.js")
	for _, token := range []string{
		"id=\"kanban-heartbeat-task\"",
		"id=\"kanban-cancel-task\"",
		"id=\"kanban-reclaim-task\"",
		"/heartbeat'",
		"/cancel'",
		"/reclaim'",
		"_heartbeatTask",
		"_cancelTask",
		"_reclaimTask",
		"prompt('Cancel reason')",
		"prompt('Reclaim reason')",
		"metadata: { source: 'web' }",
		"run.started_at",
		"run.heartbeat_at",
		"run.finished_at",
	} {
		if !strings.Contains(src, token) {
			t.Fatalf("kanban module missing run lifecycle token %s", token)
		}
	}
}

func TestKanbanWebModuleRendersCleanFormLabels(t *testing.T) {
	src := readWebAssetForKanbanTest(t, "web/kanban.js")
	for _, bad := range []string{"Status><select", "Milestone><select"} {
		if strings.Contains(src, bad) {
			t.Fatalf("kanban form label contains stray marker %q", bad)
		}
	}
	for _, good := range []string{"<label>Status<select", "<label>Milestone<select"} {
		if !strings.Contains(src, good) {
			t.Fatalf("kanban form label missing %q", good)
		}
	}
}

func TestKanbanWebStylesProvideBoardDrawerAndResponsiveRules(t *testing.T) {
	src := readWebAssetForKanbanTest(t, "web/style.css")
	for _, token := range []string{
		".kanban-view",
		".kanban-toolbar",
		".kanban-board",
		".kanban-column",
		".kanban-task",
		".kanban-drawer",
		".kanban-status-dot",
		".kanban-column--triage .kanban-status-dot",
		".kanban-form",
		".kanban-run-time",
		"@media (max-width: 900px)",
	} {
		if !strings.Contains(src, token) {
			t.Fatalf("kanban styles missing token %s", token)
		}
	}
}
