# Kanban Task Controls Design

Date: 2026-05-07
Status: Approved for implementation by continuation request

## Goal

Add manual task-management controls to the durable Kanban MVP. A user should be
able to revise a task after creation, move it between non-running statuses, and
archive it so normal board views stay focused.

This phase keeps the Kanban system manual. It does not add automatic
dispatching, background workers, Git work-dir providers, or live WebSocket board
updates.

## Research Notes

The current kernel can create tasks, claim runs, complete/fail/block/unblock
tasks, comment, link dependencies, and display a Web board. The missing daily
workflow is editing. Once a task is created, title, body, priority, assignee,
milestone, and status are effectively fixed unless a specialized workflow
action happens.

The status model already includes `archived`, and the original design says
archived tasks are hidden from normal boards. The current list path returns all
statuses when no status filter is supplied, which means an archived task can
fall back into a visible board column in the Web UI. This phase makes archived
visibility explicit.

## Scope

This phase adds:

- a store-level task update operation
- a store-level task archive operation
- default task listing that excludes archived tasks unless explicitly requested
- HTTP handlers for updating and archiving a task
- CLI commands for editing and archiving a task
- Web drawer controls for editing visible task fields and archiving a task

This phase does not add delete, restore, drag-and-drop, custom columns, board
creation, milestone status editing, run cancellation, or dispatcher retry logic.

## Store Behavior

Add:

```go
type UpdateKanbanTaskRequest struct {
    Actor              string
    Title              *string
    Body               *string
    Status             *string
    Priority           *int
    Assignee           *string
    MilestoneID        *string
    ClearMilestone     bool
}

func (s *Store) UpdateKanbanTask(taskID string, req UpdateKanbanTaskRequest) (*KanbanTask, error)
func (s *Store) ArchiveKanbanTask(taskID, actor string) (*KanbanTask, error)
```

Rules:

- Empty title is rejected when title is supplied.
- `milestone_id` must belong to the task's project when supplied.
- `clear_milestone` and a supplied `milestone_id` are mutually exclusive.
- Direct update to `running` is rejected. Claiming is the only way to enter
  `running`.
- Direct update from `running` is rejected. Running work must finish through
  complete, fail, block, or a future cancel/reclaim operation.
- Direct update to `ready` is rejected when the task still has incomplete
  blocking parents.
- Direct update to `done` sets `completed_at` when it was empty and promotes
  unblocked children using the existing dependency promotion rule.
- Direct update from `done` to any non-done status clears `completed_at`.
- Archive rejects running tasks and sets status to `archived`.
- Both operations record task events with compact metadata describing changed
  fields.

Task list behavior:

- `ListKanbanTasks` excludes `archived` by default when no explicit status is
  supplied.
- `status=archived` returns archived tasks.

## API

Add:

```text
PATCH /api/v1/kanban/tasks/{task}
POST  /api/v1/kanban/tasks/{task}/archive
```

`PATCH` request body:

```json
{
  "actor": "alice",
  "title": "Updated title",
  "body": "Updated body",
  "status": "ready",
  "priority": 4,
  "assignee": "bob",
  "milestone": "release-1",
  "clear_milestone": false
}
```

Fields are optional. String fields may be supplied as empty strings except
`title`; an empty assignee or body clears that field. `milestone` resolves by
ID or slug within the task's project. `clear_milestone` clears the milestone.
Supplying both `milestone` and `clear_milestone: true` is a bad request.

The response envelope is:

```json
{"task": {}}
```

`archive` request body:

```json
{"actor": "alice"}
```

It returns the same task envelope.

## CLI

Add:

```bash
kittypaw kanban edit <task> [--actor <name>] [--title <title>] [--body <text>] [--status <status>] [--priority <n>] [--assignee <name>] [--milestone <milestone>] [--clear-milestone]
kittypaw kanban archive <task> [--actor <name>]
```

CLI flag presence matters. For example, omitting `--assignee` leaves the
assignee unchanged, while `--assignee ""` clears it.

The edit command prints the updated task ID, status, title, assignee, priority,
and milestone when present.

## Web UI

The task drawer should add a compact edit form using the existing authenticated
`api()` helper:

- title input
- status select
- milestone select
- priority input
- assignee input
- body textarea
- Save button
- Archive button

After saving, the board data and drawer detail are reloaded. After archiving,
the selected task is cleared and the board reloads.

Archived tasks do not appear in the normal board because the API list endpoint
uses the store's default hidden-archive behavior.

## Testing

Use TDD with focused tests:

- Store tests for edit fields, milestone clear/set, status movement, archived
  default filtering, archive rejection while running, and blocked-ready
  rejection.
- API tests for `PATCH`, archive, validation, and missing-task route handling.
- CLI tests for command exposure, flags, edit behavior, and archive behavior.
- Web static tests for edit/archive controls and endpoint usage.

Final verification:

```bash
cd apps/kittypaw
go test ./store -run 'TestKanban.*(Update|Archive|List)' -count=1
go test ./server -run 'TestKanbanAPI.*(Update|Archive|Validation|Missing)|TestKanbanWeb' -count=1
go test ./cli -run 'TestKanbanCommand|TestKanbanEdit|TestKanbanArchive' -count=1
node --check server/web/kanban.js
go test ./... -short -count=1
```
