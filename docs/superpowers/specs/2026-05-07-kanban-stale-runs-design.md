# Kanban Stale Runs Design

Date: 2026-05-07
Status: Approved for implementation by continuation request

## Goal

Add a durable stale-run detection surface for Kanban so operators and future
dispatchers can identify running tasks whose latest Run heartbeat is older than
a configured threshold.

## Research Notes

The Kanban kernel already records Runs with `started_at`, `heartbeat_at`,
`finished_at`, and `outcome`. Manual lifecycle controls can heartbeat, cancel,
and reclaim a running task. The next dispatcher-oriented gap is not another
state transition; it is a query that answers which running Runs are stale.

The existing design documents list heartbeat timeout and retry/reclaim as
dispatcher follow-ups. This phase provides the read side of that workflow
without launching background workers or automatically changing task state.

## Scope

This phase adds:

- a store query for stale running Runs
- an authenticated API endpoint for the same query
- a CLI command for manual inspection
- focused tests for cutoff, project filtering, ordering, validation, and command
  wiring

This phase does not add a dispatcher, automatic reclaim, retries,
staff-assigned runner launch, Web UI controls, WebSocket updates, schema
migrations, or a new Run
work-dir provider.

## Stale Definition

A Run is stale when all of these are true:

- `kanban_task_runs.outcome = "running"`
- the owning task still has `status = "running"`
- the owning project is not archived
- `heartbeat_at` is strictly older than a caller-provided cutoff timestamp

The cutoff is computed outside the store. The store receives an RFC3339 UTC
timestamp string in the same `2006-01-02T15:04:05Z` format used by existing
Kanban timestamps. This keeps tests deterministic and avoids adding injectable
clock state to the store.

The query returns the stalest Runs first by `heartbeat_at ASC, started_at ASC`.
It supports optional project filtering and a bounded limit.

## Store API

Add:

```go
type KanbanStaleRun struct {
    Run         KanbanRun  `json:"run"`
    Task        KanbanTask `json:"task"`
    ProjectSlug string     `json:"project_slug"`
    ProjectName string     `json:"project_name"`
}

type KanbanStaleRunFilter struct {
    ProjectID   string
    StaleBefore string
    Limit       int
}

func (s *Store) ListStaleKanbanRuns(filter KanbanStaleRunFilter) ([]KanbanStaleRun, error)
```

Validation rules:

- `StaleBefore` is required.
- `Limit <= 0` uses a conservative default of `50`.
- `Limit > 200` is clamped to `200`.

The method does not mutate Runs or Tasks and does not record task events.

## API

Add:

```text
GET /api/v1/kanban/runs/stale?stale_after=<duration>[&project=<project>][&limit=<n>]
```

Request rules:

- `stale_after` is required and parsed with Go duration syntax, for example
  `10m`, `1h`, or `90s`.
- `stale_after` must be positive.
- `project` is optional and accepts project ID or slug.
- `limit` is optional, defaults to `50`, and must be positive when supplied.
- limits above `200` are clamped by the store.

Response:

```json
{
  "stale_runs": [
    {
      "run": {"id": "run_...", "task_id": "tsk_...", "outcome": "running"},
      "task": {"id": "tsk_...", "title": "Investigate"},
      "project_slug": "kitty",
      "project_name": "KittyPaw"
    }
  ],
  "stale_before": "2026-05-07T03:00:00Z"
}
```

The endpoint is read-only and uses the existing `/api/v1` authentication
boundary. The current route table has no conflicting `/kanban/runs/{id}` route.

## CLI

Add:

```bash
kittypaw kanban stale --stale-after <duration> [--project <project>] [--limit <n>] [--account <account>]
```

Behavior:

- open the account-scoped local store using existing Kanban command helpers
- resolve `--project` when supplied
- compute the cutoff with `time.Now().UTC().Add(-duration)`
- print `No stale runs.` when the result is empty
- otherwise print one compact line per stale Run with task ID, run ID, project
  slug, actor, heartbeat timestamp, and task title

The CLI does not reclaim, cancel, or heartbeat anything. It only reports.

## Error Handling

Store errors remain ordinary Go errors. Server validation errors return HTTP
400. Missing project filters return 404 through the existing Kanban store error
mapping. Unexpected store query failures return through `kanbanWriteStoreError`.

CLI validation returns errors before opening the store when `--stale-after` is
missing or invalid. Project resolution errors are returned directly, matching
existing Kanban commands.

## Testing

Use TDD at each layer:

- store tests create multiple running Runs, manually age `heartbeat_at`, and
  verify stale-only results, project filtering, ordering, and limit behavior
- server tests cover valid response shape, required `stale_after`, invalid
  duration, missing project filter, and route registration
- CLI tests cover command exposure, flags, empty output, filtered output, and
  invalid duration handling

Focused commands:

```bash
cd apps/kittypaw
go test ./store -run 'TestKanban.*Stale' -count=1
go test ./server -run 'TestKanbanAPI.*Stale' -count=1
go test ./cli -run 'TestKanban.*Stale|TestKanbanCommand' -count=1
go test ./... -short -count=1
```

## Review Checklist

- The stale query is read-only.
- The store cutoff is deterministic and testable.
- API and CLI use duration thresholds; the store uses a timestamp cutoff.
- The result carries enough task/project context for a human or dispatcher to
  decide whether to reclaim.
- No automatic dispatcher behavior is introduced in this phase.
