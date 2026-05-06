# Kanban Web UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a default-account Kanban tab to the local KittyPaw Web UI.

**Architecture:** Keep the server API unchanged and add a vanilla JavaScript
`Kanban` module mounted by the existing `App` shell. The module calls
authenticated `/api/v1` endpoints through `api()`, groups tasks into fixed
status columns, and uses a drawer for task details, actions, comments, and runs.

**Tech Stack:** Go static-source tests, embedded web assets, vanilla JavaScript,
existing CSS variables and shell layout.

---

## File Structure

- Create `apps/kittypaw/server/web/kanban.js`: Kanban tab state, rendering, API
  calls, and event wiring.
- Create `apps/kittypaw/server/web_kanban_test.go`: static-source tests for
  shell wiring, authenticated API usage, endpoints, actions, and CSS hooks.
- Modify `apps/kittypaw/server/web/index.html`: load `/kanban.js`.
- Modify `apps/kittypaw/server/web/app.js`: add `Kanban` nav item and mount
  `Kanban.mount(content)`.
- Modify `apps/kittypaw/server/web/style.css`: Kanban layout and responsive
  rules.
- Modify `apps/kittypaw/server/web_app_test.go`: add shell-level Kanban guard if
  that assertion is clearer beside the existing shell tests.

## Task 1: Shell Wiring

**Files:**
- Test: `apps/kittypaw/server/web_kanban_test.go`
- Modify: `apps/kittypaw/server/web/index.html`
- Modify: `apps/kittypaw/server/web/app.js`

- [ ] **Step 1: Write failing static tests**

Add `apps/kittypaw/server/web_kanban_test.go`:

```go
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
	if !strings.Contains(app, `data-tab="kanban"`) {
		t.Fatal("default shell must expose a Kanban nav item")
	}
	if !strings.Contains(app, "tab === 'kanban'") || !strings.Contains(app, "Kanban.mount(content)") {
		t.Fatal("switchTab must mount the Kanban module")
	}
}
```

- [ ] **Step 2: Run the focused test and verify it fails**

Run: `cd apps/kittypaw && go test ./server -run TestKanbanWebAssetsAreLoadedAndMounted -count=1`

Expected: FAIL because `kanban.js` is not loaded and `Kanban.mount(content)` is
not wired.

- [ ] **Step 3: Add the shell wiring**

In `web/index.html`, load the script after `skills.js` and before
`settings.js`:

```html
  <script src="/skills.js"></script>
  <script src="/kanban.js"></script>
  <script src="/settings.js"></script>
```

In `web/app.js`, include Kanban in default-account admin nav:

```js
const adminNav = this.isDefault
  ? '<button class="nav-item" data-tab="dashboard">Dashboard</button><button class="nav-item" data-tab="kanban">Kanban</button><button class="nav-item" data-tab="skills">Skills</button>'
  : '';
```

Add the `switchTab` branch before Skills:

```js
if (tab === 'dashboard') {
  this._showDashboard(content);
} else if (tab === 'kanban') {
  Kanban.mount(content);
} else if (tab === 'skills') {
  Skills.mount(content);
} else {
  Settings.mount(content);
}
```

- [ ] **Step 4: Run the focused test and verify it passes**

Run: `cd apps/kittypaw && go test ./server -run TestKanbanWebAssetsAreLoadedAndMounted -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/kittypaw/server/web_kanban_test.go apps/kittypaw/server/web/index.html apps/kittypaw/server/web/app.js
git commit -m "feat: wire kanban web tab"
```

## Task 2: Kanban Module API Surface

**Files:**
- Test: `apps/kittypaw/server/web_kanban_test.go`
- Create: `apps/kittypaw/server/web/kanban.js`

- [ ] **Step 1: Add failing API/static tests**

Append to `web_kanban_test.go`:

```go
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
```

- [ ] **Step 2: Run the focused test and verify it fails**

Run: `cd apps/kittypaw && go test ./server -run TestKanbanWebModuleUsesAuthenticatedAPIAndExpectedEndpoints -count=1`

Expected: FAIL because `web/kanban.js` does not exist.

- [ ] **Step 3: Implement `Kanban` shell, load flow, and board rendering**

Create `web/kanban.js` with:

```js
// KittyPaw Kanban Board

const Kanban = {
  _container: null,
  _projects: [],
  _boards: [],
  _milestones: [],
  _tasks: [],
  _selectedProject: '',
  _selectedMilestone: '',
  _selectedTaskID: '',
  _detail: null,
  _loading: false,
  _error: '',

  _statuses: [
    { key: 'triage', label: 'Triage' },
    { key: 'todo', label: 'Todo' },
    { key: 'ready', label: 'Ready' },
    { key: 'running', label: 'Running' },
    { key: 'blocked', label: 'Blocked' },
    { key: 'done', label: 'Done' },
  ],

  mount(container) {
    this._container = container;
    this._projects = [];
    this._boards = [];
    this._milestones = [];
    this._tasks = [];
    this._selectedProject = '';
    this._selectedMilestone = '';
    this._selectedTaskID = '';
    this._detail = null;
    this._error = '';
    this._render();
    this._loadProjects();
  },

  async _loadProjects() {
    this._loading = true;
    this._error = '';
    this._render();
    try {
      const data = await api('/api/v1/projects');
      this._projects = data.projects || [];
      if (!this._selectedProject && this._projects.length) {
        this._selectedProject = this._projectKey(this._projects[0]);
      }
      await this._loadProjectData();
    } catch (e) {
      this._error = e.message || String(e);
    } finally {
      this._loading = false;
      this._render();
    }
  },

  async _loadProjectData() {
    if (!this._selectedProject) return;
    const project = this._selectedProject;
    const query = this._taskQuery(project);
    const [boards, milestones, tasks] = await Promise.all([
      api('/api/v1/projects/' + encodeURIComponent(project) + '/boards'),
      api('/api/v1/projects/' + encodeURIComponent(project) + '/milestones'),
      api('/api/v1/kanban/tasks?project=' + query),
    ]);
    this._boards = boards.boards || [];
    this._milestones = milestones.milestones || [];
    this._tasks = tasks.tasks || [];
  },
}
```

Then complete the rendering helpers used by this task: `_render`,
`_toolbarHTML`, `_emptyProjectHTML`, `_boardHTML`, `_taskCardHTML`,
`_bindEvents`, `_projectKey`, `_selectedProjectObject`, `_taskQuery`,
`_tasksByStatus`, and `_setError`. These helpers render the toolbar, six
columns, and task cards only.

- [ ] **Step 4: Run the focused test and verify it passes**

Run: `cd apps/kittypaw && go test ./server -run TestKanbanWebModuleUsesAuthenticatedAPIAndExpectedEndpoints -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/kittypaw/server/web_kanban_test.go apps/kittypaw/server/web/kanban.js
git commit -m "feat: add kanban web board"
```

## Task 3: Create Forms And Task Actions

**Files:**
- Test: `apps/kittypaw/server/web_kanban_test.go`
- Modify: `apps/kittypaw/server/web/kanban.js`

- [ ] **Step 1: Add failing action tests**

Append to `web_kanban_test.go`:

```go
func TestKanbanWebModuleSupportsCreateDetailActionsAndRuns(t *testing.T) {
	src := readWebAssetForKanbanTest(t, "web/kanban.js")
	for _, token := range []string{
		"id=\"kanban-project-form\"",
		"id=\"kanban-task-form\"",
		"method: 'POST'",
		"root_path",
		"/api/v1/kanban/tasks'",
		"/claim'",
		"/complete'",
		"/block'",
		"/unblock'",
		"/comments'",
		"kanban-runs",
		"kanban-comments",
	} {
		if !strings.Contains(src, token) {
			t.Fatalf("kanban module missing token %s", token)
		}
	}
}
```

- [ ] **Step 2: Run the focused test and verify it fails**

Run: `cd apps/kittypaw && go test ./server -run TestKanbanWebModuleSupportsCreateDetailActionsAndRuns -count=1`

Expected: FAIL until project form, task form, detail drawer, actions, comments,
and run history are implemented.

- [ ] **Step 3: Implement forms and actions**

Extend `Kanban` with:

- `_createProject(form)`: posts `{slug, name, root_path}` to
  `/api/v1/projects`, selects the created project, then reloads.
- `_createTask(form)`: posts `{project, milestone, title, body, status,
  priority, assignee}` to `/api/v1/kanban/tasks`, then reloads.
- `_loadTask(taskID)`: gets `/api/v1/kanban/tasks/{taskID}` and opens the
  drawer.
- `_taskDrawerHTML()`: renders selected task fields, action buttons,
  `kanban-comments`, and `kanban-runs`.
- `_claimTask()`, `_completeTask()`, `_blockTask()`, `_unblockTask()`: call the
  corresponding action endpoints with `api()` and reload.
- `_addComment(form)`: posts `{author:'web', body}` to comments endpoint.

Use `JSON.stringify` bodies and `Content-Type: application/json` headers for all
POST calls.

- [ ] **Step 4: Run the focused test and verify it passes**

Run: `cd apps/kittypaw && go test ./server -run TestKanbanWebModuleSupportsCreateDetailActionsAndRuns -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/kittypaw/server/web_kanban_test.go apps/kittypaw/server/web/kanban.js
git commit -m "feat: add kanban web actions"
```

## Task 4: Kanban Styling

**Files:**
- Test: `apps/kittypaw/server/web_kanban_test.go`
- Modify: `apps/kittypaw/server/web/style.css`

- [ ] **Step 1: Add failing CSS tests**

Append to `web_kanban_test.go`:

```go
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
		".kanban-form",
		"@media (max-width: 900px)",
	} {
		if !strings.Contains(src, token) {
			t.Fatalf("kanban styles missing token %s", token)
		}
	}
}
```

- [ ] **Step 2: Run the focused test and verify it fails**

Run: `cd apps/kittypaw && go test ./server -run TestKanbanWebStylesProvideBoardDrawerAndResponsiveRules -count=1`

Expected: FAIL because Kanban CSS does not exist.

- [ ] **Step 3: Add CSS**

Append a `Kanban` section before the existing responsive section. Include:

- `.kanban-view`: full-height flex column.
- `.kanban-toolbar`: dense top toolbar with controls.
- `.kanban-workspace`: board plus drawer grid.
- `.kanban-board`: horizontal six-column grid with stable column width.
- `.kanban-column`: status column with count header.
- `.kanban-task`: compact task item.
- `.kanban-drawer`: right-side detail panel.
- `.kanban-form`: compact form controls.
- `@media (max-width: 900px)`: stack drawer below the board.

- [ ] **Step 4: Run the focused test and verify it passes**

Run: `cd apps/kittypaw && go test ./server -run TestKanbanWebStylesProvideBoardDrawerAndResponsiveRules -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/kittypaw/server/web_kanban_test.go apps/kittypaw/server/web/style.css
git commit -m "style: add kanban web layout"
```

## Task 5: Verification And Review

**Files:**
- Review all files changed in this branch.

- [ ] **Step 1: Run focused Web UI tests**

Run: `cd apps/kittypaw && go test ./server -run 'TestWeb.*Kanban|TestKanbanWeb' -count=1`

Expected: PASS.

- [ ] **Step 2: Run full short suite**

Run: `cd apps/kittypaw && go test ./... -short -count=1`

Expected: PASS.

- [ ] **Step 3: Local code review**

Review the diff from `main`:

```bash
git diff main...HEAD -- apps/kittypaw/server/web/index.html apps/kittypaw/server/web/app.js apps/kittypaw/server/web/kanban.js apps/kittypaw/server/web/style.css apps/kittypaw/server/web_kanban_test.go
```

Check for:

- Any `/api/v1` route using `apiRaw()` or `apiPost()`.
- Missing escaping through `esc()` for server/user data.
- Event handlers that can fire without a selected project or task.
- Forms that can submit empty required payloads.
- Layout selectors missing from CSS.

- [ ] **Step 4: Fix review findings with tests first**

For each important finding, add or adjust a static test in
`web_kanban_test.go`, run it red, implement the fix, then run it green.

- [ ] **Step 5: Final verification**

Run: `cd apps/kittypaw && go test ./... -short -count=1`

Expected: PASS.

- [ ] **Step 6: Commit review fixes if any**

```bash
git add apps/kittypaw/server/web_kanban_test.go apps/kittypaw/server/web/index.html apps/kittypaw/server/web/app.js apps/kittypaw/server/web/kanban.js apps/kittypaw/server/web/style.css
git commit -m "fix: address kanban web review findings"
```

Skip this commit only if the review finds no code changes are needed.
