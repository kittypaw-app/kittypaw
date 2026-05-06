# Kanban Web Run Actions Design

Date: 2026-05-07
Status: Approved for implementation by explicit continuation request

## Goal

Expose the durable Kanban Run lifecycle from the local Web Kanban drawer. A user
should be able to refresh a running Run's heartbeat, cancel a running task, and
reclaim a stale running task without leaving the browser.

## Research Notes

The current Web Kanban drawer already supports task actions, editing, comments,
and Run history. The previous lifecycle phase added store, API, and CLI support
for:

- `POST /api/v1/kanban/tasks/{task}/heartbeat`
- `POST /api/v1/kanban/tasks/{task}/cancel`
- `POST /api/v1/kanban/tasks/{task}/reclaim`

The missing piece is a browser control surface. This is a Web-only increment:
there is no schema migration, no new server route, no dispatcher, and no live
streaming.

## Scope

This phase adds:

- drawer buttons for `Heartbeat`, `Cancel`, and `Reclaim`
- prompt-based reason capture for cancel and reclaim
- authenticated calls through the existing `api()` helper
- board and drawer reload after each action
- visible Run metadata for heartbeat and finished timestamps
- static Web asset tests that pin the new controls and endpoints

This phase does not add automatic heartbeat timers, stale-run scanning,
background workers, drag-and-drop, custom actors, or a new Run work-dir provider.

## Web Behavior

The task drawer action row remains compact and operational. Existing actions
stay available:

- Claim
- Complete
- Block
- Unblock

New Run lifecycle actions are added to the same row:

- Heartbeat sends `{ "actor": "web" }`.
- Cancel prompts for a reason and sends `{ "actor": "web", "reason": reason,
  "metadata": {"source":"web"} }`.
- Reclaim prompts for a reason and sends `{ "actor": "web", "reason": reason,
  "metadata": {"source":"web"} }`.

The browser does not supply `work_dir` for reclaim. The store will reuse the
reclaimed Run's recorded work dir and provider.

Canceled prompts do not call the API. API errors continue to use the existing
inline Kanban error region.

## Run History

Run history should show enough timestamps for lifecycle actions to be auditable:

- `started_at`
- `heartbeat_at`
- `finished_at`

The existing outcome, actor, work dir, summary, and metadata display remains.

## Files

Modify:

- `apps/kittypaw/server/web/kanban.js`
  - render the new action buttons
  - bind click handlers
  - add `_heartbeatTask`, `_cancelTask`, and `_reclaimTask`
  - extend Run history rendering with timestamps
- `apps/kittypaw/server/web/style.css`
  - add a small timestamp style for Run history if needed
- `apps/kittypaw/server/web_kanban_test.go`
  - pin button IDs, endpoints, methods, prompt behavior, and timestamp fields

## Error Handling

All mutations call `_taskAction`, which already clears the error state, posts
JSON, reloads the board, reloads selected task detail, and renders the drawer.
Cancel and reclaim validate that the prompt returns a non-empty reason before
calling `_taskAction`.

## Testing

Use focused static Web tests and syntax verification:

```bash
cd apps/kittypaw
go test ./server -run 'TestKanbanWeb' -count=1
node --check server/web/kanban.js
go test ./server -run 'TestWeb.*Kanban|TestKanbanWeb' -count=1
go test ./... -short -count=1
```

## Review Checklist

- No product-facing use of the reserved Git working-directory term.
- No duplicated domain logic in the browser.
- New actions use existing authenticated `api()` flow.
- Cancel and reclaim do not fire without a reason.
- Run history displays heartbeat and finish timestamps.
