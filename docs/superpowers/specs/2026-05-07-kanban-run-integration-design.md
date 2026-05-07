# Kanban Run Integration Design

Date: 2026-05-07
Status: Approved for implementation by continuation request

## Goal

Connect durable Kanban tasks to a concrete local execution path without adding a
full runner dispatcher. A user should be able to claim a task, run a command in
that task's Run work dir, and have the run outcome recorded durably.

## Research Notes

The current Kanban kernel already records Projects, Tasks, and Runs. Claiming a
task creates a running Run and chooses the Run work dir from either an explicit
CLI/API value or the Project root. Completing a task closes the running Run and
marks the Task done.

The missing kernel capability is a failed Run outcome. The status model already
has `failed`, but no store/API operation writes it. The missing user workflow is
a single command that executes work and updates the Kanban run record based on
the command result.

## Scope

This phase adds:

- a store operation to fail the latest running Run for a task
- a server API endpoint for the same failed-run transition
- a CLI command that claims a task, executes a local command in the Run work dir,
  and records completed or failed outcome

This phase does not add a scheduler, background dispatcher, LLM-driven task
worker, WebSocket progress stream, task editing, automatic Run work dir
providers, or live Web UI controls for failure.

## User Workflow

CLI shape:

```bash
kittypaw kanban exec <task> [--actor <name>] [--work-dir <path>] [--summary <text>] -- <command> [args...]
```

Behavior:

1. Resolve the local account and Kanban task.
2. Claim the task, creating a running Run.
3. Use the Run's recorded `work_dir` as the command working directory.
4. Stream command stdin/stdout/stderr through the current terminal.
5. If the command exits zero, complete the task with a summary and metadata.
6. If the command cannot start or exits non-zero, mark the Run failed, return a
   non-zero CLI error, and move the task back to `todo`.

Default success summary:

```text
command completed: <command line>
```

Default failure summary:

```text
command failed: <command line>
```

## Store Transition

Add:

```go
type FailKanbanTaskRequest struct {
    Actor        string
    Summary      string
    Error        string
    MetadataJSON string
}

func (s *Store) FailKanbanTask(taskID string, req FailKanbanTaskRequest) error
```

Rules:

- `error` is required after trimming.
- The latest running Run for the task is updated to outcome `failed`.
- The Run records summary, error text, metadata JSON, `finished_at`, and
  refreshed `heartbeat_at`.
- The Task status becomes `todo`.
- A `failed` event is recorded.
- If no running Run exists, the operation returns an error and leaves the Task
  unchanged.

## API

Add:

```text
POST /api/v1/kanban/tasks/{task}/fail
```

Request body:

```json
{"actor":"alice","summary":"tests failed","error":"exit status 1","metadata":{"command":["go","test","./..."],"exit_code":1}}
```

The handler validates the task exists, requires non-empty `error`, accepts
metadata with the same JSON validation helper used by complete, and returns the
updated task envelope:

```json
{"task":{}}
```

## CLI Execution Metadata

`kanban exec` stores compact JSON metadata:

```json
{
  "command": ["go", "test", "./..."],
  "exit_code": 0,
  "duration_ms": 1234,
  "run_id": "run_..."
}
```

Non-process startup failures use `exit_code: -1`.

## Error Handling

If recording the final Kanban outcome fails after the command has already run,
the CLI returns an error that includes both the command result and the store
recording failure. It does not retry the command.

If the Run work dir does not exist, the command start fails after claim. The CLI
records a failed Run with `exit_code: -1`.

## Testing

Use TDD with focused tests:

- Store tests for successful failed-run recording and no-running-run rejection.
- API tests for `/fail`, including task status and run outcome.
- CLI tests for command exposure and flags.
- CLI integration tests using a temporary account config and project:
  - a successful command writes a file in the Run work dir and completes the task
  - a failing command records `failed` outcome, `exit_code`, and returns a CLI
    error

Final verification:

```bash
cd apps/kittypaw
go test ./store -run 'TestKanban.*Fail' -count=1
go test ./server -run 'TestKanbanAPI.*Fail' -count=1
go test ./cli -run 'TestKanbanCommand|TestKanbanExec' -count=1
go test ./... -short -count=1
```
