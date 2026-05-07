# Kanban Runner Toolset Design

Date: 2026-05-07
Status: Approved for implementation by user design approval

## Goal

Expose durable Kanban operations to the JavaScript skill sandbox so runners and
future Kanban staff runners can inspect and update tasks without shelling out to the
CLI.

## Scope

This phase adds the `Kanban` sandbox global with:

- `Kanban.show(taskId)`
- `Kanban.create(options)`
- `Kanban.claim(taskId, options?)`
- `Kanban.complete(taskId, options?)`
- `Kanban.block(taskId, reasonOrOptions)`
- `Kanban.comment(taskId, bodyOrOptions)`
- `Kanban.link(parentTaskId, childTaskId)`
- `Kanban.heartbeat(taskId, options?)`

This phase does not add dispatcher staff execution, hosted API contracts,
bulk task operations, automatic retries, task listing, Web UI controls, or a new
work-dir provider.

## JS API

`Kanban.create(options)` accepts:

```js
{
  project: "project-id-or-slug",
  title: "Task title",
  body: "...",
  status: "ready",
  priority: 10,
  assignee: "staff-id",
  milestone: "milestone-id-or-slug",
  created_by: "runner"
}
```

Other mutating calls accept object options when they need more than one value:

```js
Kanban.claim(taskId, {actor: "runner", work_dir: "/repo"})
Kanban.complete(taskId, {actor: "runner", summary: "done", metadata: {source: "runner"}})
Kanban.block(taskId, {actor: "runner", reason: "waiting on review"})
Kanban.comment(taskId, {author: "runner", body: "note"})
Kanban.heartbeat(taskId, {actor: "runner"})
```

For convenience, `block` may receive a plain reason string and `comment` may
receive a plain body string.

## Engine Behavior

The sandbox already creates globals from `core.SkillRegistry`, so adding
`Kanban` there exposes stubs and prompt metadata together. The engine resolver
adds `executeKanban`, which calls only existing `store` Kanban methods.

Project, board, and milestone references are resolved by ID or slug using the
store helpers. Task IDs are direct task IDs. Metadata options can be either an
object or a JSON string; objects are normalized to JSON before store calls.

Responses are JSON objects:

- `show`: `{task, comments, runs, events}`
- `create`: `{task}`
- `claim`: `{run}`
- `complete`: `{success: true}`
- `block`: `{success: true}`
- `comment`: `{comment}`
- `link`: `{success: true}`
- `heartbeat`: `{run}`

Errors are returned as `{error: "..."}` matching existing skill handlers.

## Testing

- sandbox test confirms `Kanban.*` calls are exposed and captured as skill calls
- engine tests cover create/show/comment/link
- engine tests cover claim/heartbeat/complete and claim/block
- validation tests cover missing required arguments and invalid status

Focused commands:

```bash
cd apps/kittypaw
go test ./sandbox -run 'TestExecuteKanbanSkillCall' -count=1
go test ./engine -run 'TestExecuteKanban' -count=1
go test ./engine ./sandbox -count=1
go test ./... -short -count=1
```
