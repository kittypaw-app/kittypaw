# Kanban Dispatch Cancellation Design

Date: 2026-05-07
Status: Approved for implementation by user design approval

## Goal

Make `kittypaw kanban dispatch --loop` respond to process interrupt signals
through the Cobra command context, and ensure an interrupted worker command does
not leave the task in `running` forever.

## Scope

This phase adds:

- root CLI execution with a signal-aware context
- dispatch worker execution that treats context cancellation as a recorded
  failed Run
- focused tests for root context wiring and dispatch cancellation persistence

This phase does not add automatic stale reclaim, retry limits, a new Run outcome,
server background dispatching, or graceful cancellation to every existing CLI
command.

## Behavior

`main` creates a `signal.NotifyContext` for `SIGINT` and `SIGTERM` and executes
the root Cobra command with `ExecuteContext`. Commands using `cmd.Context()` can
then observe cancellation.

When a dispatched worker exits because the dispatch context was canceled,
`kanban dispatch` records the Run through `FailKanbanTask`, leaving the task in
`todo`. The default summary uses `command canceled: <command>`. Existing command
metadata remains unchanged and still carries command, Run ID, exit code, and
duration.

`kanban exec` remains unchanged in this phase because it does not use
`cmd.Context()` today. Extending one-shot `exec` cancellation can be a separate
follow-up if needed.

## Testing

- CLI root test verifies `ExecuteContext` propagates a pre-canceled context to a
  command.
- Dispatch test starts a long-running worker, cancels the context, and verifies:
  - `runKanbanDispatch` returns a cancellation-related error
  - the task returns to `todo`
  - the Run outcome is `failed`
  - the Run summary contains `command canceled`

Focused commands:

```bash
cd apps/kittypaw
go test ./cli -run 'TestRootCommandPropagatesContext|TestKanbanDispatchRecordsCanceledWorker' -count=1
go test ./cli -run 'TestKanbanDispatch' -count=1
go test ./cli -count=1
go test ./... -short -count=1
```
