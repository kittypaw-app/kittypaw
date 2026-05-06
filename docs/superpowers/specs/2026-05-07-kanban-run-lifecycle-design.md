# Kanban Run Lifecycle Design

Date: 2026-05-07
Status: Approved for implementation by continuation request

## Goal

Complete the manual durable Run lifecycle before adding agent tools or an
automatic dispatcher. A running Kanban task should support a heartbeat, manual
cancel, and manual reclaim by another actor.

## Research Notes

The current Kanban kernel can claim a task, create a running Run, complete it,
fail it, and block it. The schema and constants already include `heartbeat_at`,
`canceled`, and `reclaimed`, but no store/API/CLI operation writes heartbeat,
cancel, or reclaim transitions.

The future dispatcher needs these transitions before it can safely decide
whether work is alive, stopped, or taken over. This phase does not introduce the
dispatcher itself.

## Scope

This phase adds:

- store operations for heartbeat, cancel, and reclaim
- server API endpoints for the same transitions
- CLI commands for manual heartbeat, cancel, and reclaim
- focused tests for state transitions and route contracts

This phase does not add timeout scanning, automatic retry, background workers,
WebSocket progress updates, Web UI action buttons, or any new Run work-dir
provider.

## Store Transitions

Add:

```go
type HeartbeatKanbanTaskRequest struct {
    Actor string
}

type CancelKanbanTaskRequest struct {
    Actor        string
    Reason       string
    MetadataJSON string
}

type ReclaimKanbanTaskRequest struct {
    Actor           string
    Reason          string
    WorkDir         string
    WorkDirProvider string
    MetadataJSON    string
}

func (s *Store) HeartbeatKanbanTask(taskID string, req HeartbeatKanbanTaskRequest) (*KanbanRun, error)
func (s *Store) CancelKanbanTask(taskID string, req CancelKanbanTaskRequest) (*KanbanTask, error)
func (s *Store) ReclaimKanbanTask(taskID string, req ReclaimKanbanTaskRequest) (*KanbanRun, error)
```

Rules:

- Heartbeat updates `heartbeat_at` on the latest running Run for the task and
  returns that Run.
- Heartbeat does not write a task event. Heartbeats may be frequent, and the Run
  row already carries the durable liveness signal.
- Cancel requires a non-empty reason, closes the latest running Run with outcome
  `canceled`, records summary/metadata/finished time, moves the task back to
  `todo`, and writes a `canceled` event.
- Reclaim requires a non-empty actor and reason. It closes the latest running
  Run with outcome `reclaimed`, records summary/metadata/finished time, then
  creates a fresh running Run for the same task.
- Reclaim keeps the task status `running`.
- Reclaim uses the supplied Run work dir when present. Otherwise it reuses the
  reclaimed Run's `work_dir` and `work_dir_provider`.
- All three operations error if there is no currently running Run.

## API

Add:

```text
POST /api/v1/kanban/tasks/{task}/heartbeat
POST /api/v1/kanban/tasks/{task}/cancel
POST /api/v1/kanban/tasks/{task}/reclaim
```

Request examples:

```json
{"actor":"alice"}
{"actor":"alice","reason":"stopping local command","metadata":{"source":"cli"}}
{"actor":"bob","reason":"stale runner","work_dir":"/repo/kitty","metadata":{"stale_after_ms":600000}}
```

Responses:

- heartbeat returns `{"run": {}}`
- cancel returns `{"task": {}}`
- reclaim returns `{"run": {}}`

Metadata accepts either structured `metadata` or raw `metadata_json`, matching
the existing complete/fail handlers.

## CLI

Add:

```bash
kittypaw kanban heartbeat <task> [--actor <name>]
kittypaw kanban cancel <task> <reason> [--actor <name>] [--metadata <json>]
kittypaw kanban reclaim <task> <reason> [--actor <name>] [--work-dir <path>] [--metadata <json>]
```

`reclaim` uses the same `--work-dir` normalization as `claim` and `exec`. If no
Run work dir is supplied, it reuses the previous running Run's recorded work
dir.

## Error Handling

- Missing running Run returns a store error and maps to HTTP 400.
- Missing task maps to HTTP 404 in the API.
- Empty cancel/reclaim reason returns a validation error.
- Empty reclaim actor returns a validation error because reclaim attribution is
  the point of the transition.
- Invalid metadata JSON is rejected by CLI and API before store mutation.

## Testing

Use TDD with focused tests:

- Store tests:
  - heartbeat updates `heartbeat_at` and requires a running Run
  - cancel closes the Run, moves the task to `todo`, and records a `canceled`
    event
  - reclaim closes the old Run as `reclaimed`, starts a new running Run, and
    keeps the task running
- API tests:
  - heartbeat/cancel/reclaim happy paths
  - validation and missing-task routes
- CLI tests:
  - command exposure and flags
  - heartbeat/cancel/reclaim behavior against a temporary account store

Final verification:

```bash
cd apps/kittypaw
go test ./store -run 'TestKanban.*(Heartbeat|Cancel|Reclaim)' -count=1
go test ./server -run 'TestKanbanAPI.*(Heartbeat|Cancel|Reclaim|Validation|Missing)' -count=1
go test ./cli -run 'TestKanbanCommand|TestKanbanHeartbeat|TestKanbanCancel|TestKanbanReclaim' -count=1
go test ./... -short -count=1
```
