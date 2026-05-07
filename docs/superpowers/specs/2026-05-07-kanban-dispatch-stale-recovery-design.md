# Kanban Dispatch Stale Recovery Design

Date: 2026-05-07
Status: Approved for implementation by user design approval

## Goal

Extend `kanban dispatch` so a long-running dispatcher can recover stale running
work before claiming new ready tasks. The recovery path should reclaim stale
Runs, execute the same worker command, and block tasks that have already failed
too many times.

## Scope

This phase adds:

- `--reclaim-stale-after <duration>` to `kittypaw kanban dispatch`
- `--max-attempts <n>` to stop repeatedly running a failing task
- stale Run scanning at the start of each dispatch cycle
- automatic reclaim and worker execution for stale running tasks
- automatic block for tasks whose failed Run count has reached `--max-attempts`
- focused CLI tests for stale reclaim execution, max-attempt blocking, and
  validation

This phase does not add schema columns, hosted dispatcher workers, staff/runner
worker selection, backoff scheduling, Web UI controls, or a new work-dir
provider.

## CLI

Extend:

```bash
kittypaw kanban dispatch --project <project> \
  [--reclaim-stale-after <duration>] [--max-attempts <n>] \
  -- <command> [args...]
```

Rules:

- `--reclaim-stale-after` is optional. When omitted, dispatch behavior remains
  ready-task-only.
- The duration must be positive when supplied.
- `--max-attempts` defaults to `0`, meaning unlimited attempts.
- `--max-attempts` must not be negative.
- Stale recovery requires a reclaim actor. If `--actor` is empty, dispatch uses
  `dispatcher` for automatic reclaim, block, and recovered worker attribution.

## Dispatch Cycle

Each cycle processes at most `--limit` tasks total.

1. If stale recovery is enabled, compute `stale_before = now - duration`.
2. Query project-scoped stale running Runs with the existing
   `ListStaleKanbanRuns`.
3. For each stale task up to the remaining cycle limit:
   - if `--max-attempts` is reached, block the task with a deterministic reason
   - otherwise reclaim the task with `ReclaimKanbanTask`
   - execute the worker command against the new Run
4. If cycle capacity remains, list ready tasks as before.
5. For each ready task up to the remaining cycle limit:
   - if `--max-attempts` is reached, block the task
   - otherwise claim and execute it as today

Failed attempt count is the number of prior Runs for the task whose outcome is
`failed`. Reclaimed, canceled, blocked, and completed Runs do not count toward
the failure limit.

Blocking uses the reason:

```text
max attempts reached
```

## Execution Refactor

The existing dispatch helper claims a task and immediately executes the command.
Stale recovery needs to execute an already-created reclaimed Run. Refactor the
helper into:

- claim ready task then execute Run
- reclaim stale task then execute Run
- common execute Run helper

The common execution helper keeps the existing environment variables and command
metadata.

## Testing

Use TDD in `apps/kittypaw/cli`:

- command flag test pins `reclaim-stale-after` and `max-attempts`
- stale recovery test creates a stale running Run, dispatches with
  `--reclaim-stale-after`, and verifies:
  - old Run is `reclaimed`
  - new Run is `completed`
  - task is `done`
  - worker output file is written in the project root
- max attempts test creates a ready task with a prior failed Run, dispatches
  with `--max-attempts 1`, and verifies:
  - command is not executed
  - task is `blocked`
  - existing failed Run remains recorded
- validation test covers invalid stale duration and negative max attempts

Focused commands:

```bash
cd apps/kittypaw
go test ./cli -run 'TestKanbanCommand|TestKanbanDispatch' -count=1
go test ./cli -count=1
go test ./... -short -count=1
```
