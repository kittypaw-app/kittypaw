# Kanban Dispatch Loop Design

Date: 2026-05-07
Status: Approved for implementation by user design approval

## Goal

Add a dispatcher loop for local Kanban automation. The dispatcher should find
ready tasks in a project, claim each task through the durable Kanban store, run a
configured command or worker process in the task Run work directory, and record
the Run as completed or failed.

## Research Notes

The Kanban kernel already has the state transitions required for execution:

- `ListKanbanTasks` can list `ready` tasks ordered by `priority DESC,
  created_at`.
- `ClaimKanbanTask` atomically creates a running Run and moves the task to
  `running`.
- `CompleteKanbanTask` records success, moves the task to `done`, and promotes
  unblocked children.
- `FailKanbanTask` records command failure and moves the task back to `todo`.
- `kanban exec` already validates a command, claims one task, runs the process
  in the Run work directory, and stores command metadata.

The missing feature is a project-level selection loop, not a new state machine.
This phase therefore adds a CLI dispatcher that composes existing store
operations and reuses the single-command execution behavior.

## Scope

This phase adds:

- `kittypaw kanban dispatch --project <project> -- <command> [args...]`
- ready-task selection by project and existing priority ordering
- per-task durable claim, command execution, completion, and failure recording
- one-shot mode for processing up to a configured limit
- loop mode for polling repeatedly until the command context is canceled
- command environment variables carrying task, Run, and project context
- focused CLI tests for command exposure, validation, success, failure, empty
  queues, and env propagation

This phase does not add a server background daemon, Web UI controls, automatic
stale reclaim, retry limits, profile-specific LLM worker orchestration, a new
Run work-dir provider, or schema migrations.

## CLI

Add:

```bash
kittypaw kanban dispatch --project <project> [--limit <n>] [--loop] [--interval <duration>] [--actor <actor>] [--work-dir <path>] [--summary <text>] [--account <account>] -- <command> [args...]
```

Flags:

- `--project` is required and accepts project ID or slug.
- `--limit` defaults to `1` and is the maximum number of ready tasks processed
  in one dispatch cycle.
- `--loop` keeps polling after each cycle.
- `--interval` defaults to `30s` and is used between loop cycles.
- `--actor` defaults to the same empty actor behavior as existing lifecycle
  commands unless supplied.
- `--work-dir` uses the existing manual work-dir behavior from `kanban exec`.
- `--summary` overrides the default completion or failure summary.

One-shot mode exits successfully when no ready tasks exist and prints
`No ready tasks.`. Loop mode keeps polling until the Cobra command context is
canceled; each cycle processes up to `--limit` tasks.

## Execution Flow

For each dispatch cycle:

1. Open the account-scoped local store.
2. Resolve `--project` to a `KanbanProject`.
3. List tasks with `ProjectID: project.ID` and `Status: ready`.
4. For each task up to `--limit`, call `ClaimKanbanTask`.
5. Run the configured command with `cmd.Dir = run.WorkDir`.
6. On command success, call `CompleteKanbanTask`.
7. On command failure, call `FailKanbanTask` and return the command error.
8. Print task, Run, and work-dir information for completed tasks.

The dispatcher does not alter command arguments. It exposes context through
environment variables:

- `KITTYPAW_KANBAN_TASK_ID`
- `KITTYPAW_KANBAN_RUN_ID`
- `KITTYPAW_KANBAN_PROJECT_ID`
- `KITTYPAW_KANBAN_PROJECT_SLUG`
- `KITTYPAW_KANBAN_TASK_TITLE`

## Error Handling

Validation errors are returned before opening the store when possible:

- missing command
- missing `--project`
- non-positive `--limit`
- invalid or non-positive `--interval`
- invalid `--work-dir`

Project resolution and store errors return directly, following existing Kanban
CLI behavior. If the external command fails, the dispatcher records the failed
Run first. If recording the failure also fails, the returned error includes both
the command failure and the persistence failure.

If a ready task becomes unclaimable between listing and claiming, the claim error
is returned. A later retry can dispatch the remaining ready tasks.

## Testing

Use TDD in `apps/kittypaw/cli`:

- command exposure includes `kanban dispatch`
- flags include `project`, `limit`, `loop`, `interval`, `actor`, `work-dir`,
  `summary`, and `account`
- success dispatch picks a ready task, executes in the project root, records a
  completed Run, and marks the task `done`
- env propagation test verifies task, Run, project ID, project slug, and title
  are visible to the command
- failure dispatch records a failed Run and returns the task to `todo`
- empty queue prints `No ready tasks.`
- validation covers missing project, missing command, bad limit, and bad
  interval

Focused commands:

```bash
cd apps/kittypaw
go test ./cli -run 'TestKanbanCommand|TestKanbanDispatch' -count=1
go test ./cli -count=1
go test ./... -short -count=1
```

## Review Checklist

- The dispatcher composes existing durable Kanban transitions.
- Ready task selection remains project-scoped.
- Command argv is not modified by the dispatcher.
- Worker context is available through stable environment variables.
- One-shot mode is deterministic and testable.
- Loop mode can be canceled through command context.
