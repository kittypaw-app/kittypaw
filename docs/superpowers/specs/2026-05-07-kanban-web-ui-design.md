# Kanban Web UI Design

Date: 2026-05-07
Status: Approved for implementation by continuation request

## Goal

Add a usable local Kanban view to the existing `apps/kittypaw` Web UI so a user
can manage Projects, Milestones, Boards, Tasks, comments, and run history through
the durable Kanban API already mounted under `/api/v1`.

This phase is a browser client over the existing API. It does not add a
dispatcher, live WebSocket updates, drag-and-drop transitions, bulk actions,
agent tools, or new server routes.

## Research Notes

The Hermes Kanban docs use a browser dashboard as the human control surface:
six lifecycle columns, a toolbar, task cards, and a side drawer for details,
comments, actions, and run history. That shape fits KittyPaw's current MVP
because the server API already exposes list/detail/action endpoints and the
existing Web UI is a small vanilla JavaScript shell.

KittyPaw's current Web UI constraints:

- `web/app.js` owns auth bootstrap, shell navigation, and tab switching.
- `web/skills.js` and `web/settings.js` are plain global modules mounted by the
  shell, so Kanban should follow the same pattern.
- `/api/v1` routes must use `api()`, not `apiRaw()` or `apiPost()`, so requests
  include the browser session-bound bearer token.
- Default-account API tabs are gated behind `this.isDefault`, so Kanban belongs
  with Dashboard and Skills, not with account-scoped Settings.

## Information Architecture

The left navigation adds a default-account `Kanban` tab between Dashboard and
Skills.

The Kanban screen has three primary regions:

- Toolbar: project selector, milestone selector, refresh, and compact create
  actions.
- Board: six fixed columns in store status order: `triage`, `todo`, `ready`,
  `running`, `blocked`, `done`.
- Drawer: selected task details, action buttons, comments, and run history.

Board and Milestone are project-level concepts. The UI treats Project as the
required top-level selection. Board is loaded for context and future expansion,
but task filtering uses the default board unless the API returns additional
boards. Milestone is an optional filter and create target.

## User Workflows

Empty project state:

- Show a compact create-project form.
- Require `slug` and absolute `root_path`.
- After create, select the new project and load its default board.

Existing project state:

- Load projects from `GET /api/v1/projects`.
- Select the first project by default.
- Load boards, milestones, and tasks for the selected project.
- Render tasks grouped by status.

Task creation:

- Create a task with title, optional body, status, priority, assignee, and
  optional milestone.
- Use `POST /api/v1/kanban/tasks`.
- Reload the board after a successful create.

Task detail:

- Clicking a task opens the drawer.
- Load detail from `GET /api/v1/kanban/tasks/{task}`.
- Show fields returned by the API, comments, and runs.

Task actions:

- Claim sends `POST /api/v1/kanban/tasks/{task}/claim`.
- Complete prompts for a summary and sends
  `POST /api/v1/kanban/tasks/{task}/complete`.
- Block prompts for a reason and sends
  `POST /api/v1/kanban/tasks/{task}/block`.
- Unblock sends `POST /api/v1/kanban/tasks/{task}/unblock`.
- Comment sends `POST /api/v1/kanban/tasks/{task}/comments`.

## UI Behavior

The view should be dense and operational. It should not become a landing page or
marketing screen. Cards are task items only, not nested layout containers. The
board scrolls horizontally on narrow screens while preserving stable column
widths and readable card content.

Status colors should make the board scannable without changing the global design
system. Use small status dots and subtle column accents instead of a single-hue
surface.

Errors from API calls appear in a single inline error region near the toolbar or
drawer. Loading states should preserve the shell and not resize the board
dramatically.

## Files

Create:

- `apps/kittypaw/server/web/kanban.js`: plain global `Kanban` module. Owns
  client state, API calls, rendering, and event wiring for this tab only.
- `apps/kittypaw/server/web_kanban_test.go`: static source tests that pin
  script loading, shell wiring, authenticated API use, expected endpoints,
  status columns, and CSS hooks.

Modify:

- `apps/kittypaw/server/web/index.html`: load `/kanban.js`.
- `apps/kittypaw/server/web/app.js`: add default-account `Kanban` nav item and
  mount `Kanban.mount(content)` in `switchTab`.
- `apps/kittypaw/server/web/style.css`: add Kanban layout, board, drawer, form,
  status, and responsive rules.
- `apps/kittypaw/server/web_app_test.go`: pin shell wiring where it naturally
  belongs with the existing app tests.

## Data Flow

`Kanban.mount(container)` renders a stable shell and calls `_loadProjects()`.
The selected project slug is the client state key. Changing project or milestone
reloads boards, milestones, and tasks through authenticated `api()` calls.

Writes call the API, then reload the project board. If a selected task is still
present, the drawer reloads its detail. The UI does not try to apply domain
state transitions locally.

## Security

The Kanban tab is shown only for default-account sessions, matching the existing
Dashboard and Skills pattern. All `/api/v1` calls use `api()` so the browser
session-bound bearer token is sent. The new module must not call `apiRaw()` or
`apiPost()` for `/api/v1` Kanban routes.

## Error Handling

Validation remains server-side. The UI trims obvious text inputs and surfaces the
server error message as returned by `api()`.

Client-side prompts are used only for required action payloads that would be
awkward to keep visible at all times: completion summary and block reason.
Canceled prompts do not call the API.

## Testing

Use the current static-source Go test style for Web UI assets:

- `index.html` loads `kanban.js`.
- `app.js` gates the Kanban tab with default-account admin nav and mounts the
  module from `switchTab`.
- `kanban.js` defines the expected statuses and uses authenticated `api()` calls
  for all `/api/v1` endpoints.
- `kanban.js` includes project, milestone, task create, detail, action, comment,
  and run-history flows.
- `style.css` contains the Kanban selectors and responsive board rules.

Final verification:

```bash
cd apps/kittypaw
go test ./server -run 'TestWeb.*Kanban|TestKanbanWeb' -count=1
go test ./... -short -count=1
```

## Out Of Scope

- Live event streaming.
- Drag-and-drop status changes.
- Bulk card selection and batch actions.
- Dependency editor.
- Markdown rendering.
- Non-default-account Kanban views.
- Automatic run work-dir creation or Git worktree management.
