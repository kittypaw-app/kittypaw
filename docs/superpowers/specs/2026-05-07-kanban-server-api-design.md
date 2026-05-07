# Kanban Server API Design

Date: 2026-05-07
Status: Approved for implementation

## Goal

Expose the durable Kanban store through the local `apps/kittypaw` server so the
Web UI and later runner tool layer can use the same Project/Board/Milestone/Task
kernel that the CLI already uses.

This phase does not build the Web UI, runner toolset, dispatcher, or automatic
run work-dir providers.

## Scope

The API lives under existing authenticated `/api/v1` routes and uses the
default account store, matching the current server HTTP boundary. Multi-account
HTTP routing is already a separate server concern and stays out of this branch.

The first pass covers:

- project create/list/show
- board list by project
- milestone create/list by project
- task create/list/show
- task comments and runs
- task claim/complete/block/unblock
- task dependency link

## Routes

Project routes:

```text
GET  /api/v1/projects
POST /api/v1/projects
GET  /api/v1/projects/{project}
GET  /api/v1/projects/{project}/boards
GET  /api/v1/projects/{project}/milestones
POST /api/v1/projects/{project}/milestones
```

Task routes:

```text
GET  /api/v1/kanban/tasks?project=<project>[&board=<board>][&milestone=<milestone>][&status=<status>]
POST /api/v1/kanban/tasks
GET  /api/v1/kanban/tasks/{task}
POST /api/v1/kanban/tasks/{task}/claim
POST /api/v1/kanban/tasks/{task}/complete
POST /api/v1/kanban/tasks/{task}/block
POST /api/v1/kanban/tasks/{task}/unblock
GET  /api/v1/kanban/tasks/{task}/comments
POST /api/v1/kanban/tasks/{task}/comments
GET  /api/v1/kanban/tasks/{task}/runs
POST /api/v1/kanban/tasks/{task}/links
```

`{project}`, `{board}`, and `{milestone}` accept either ID or slug, resolved
within the default account store. `{task}` is a task ID.

## JSON Shapes

List responses use envelopes so later pagination can be added without breaking
clients:

```json
{"projects":[]}
{"boards":[]}
{"milestones":[]}
{"tasks":[]}
{"comments":[]}
{"runs":[]}
```

Create/show responses use object envelopes:

```json
{"project":{}, "default_board":{}}
{"project":{}}
{"milestone":{}}
{"task":{}}
{"task":{}, "comments":[], "events":[], "runs":[]}
{"run":{}}
{"comment":{}}
```

Request bodies:

```json
{"slug":"kitty","name":"KittyPaw","root_path":"/repo/kitty"}
{"title":"MVP","description":"","target_date":"2026-05-31"}
{"project":"kitty","board":"default","milestone":"mvp","title":"Task","body":"","status":"todo","priority":0,"assignee":"alice","created_by":"bob"}
{"actor":"alice","work_dir":"/repo/kitty"}
{"actor":"alice","summary":"done","metadata":{"tests":["go test ./..."]}}
{"actor":"alice","reason":"needs input"}
{"actor":"alice","comment":"input provided"}
{"author":"alice","body":"note"}
{"child_id":"tsk_child"}
```

`complete.metadata` is accepted as any valid JSON value and stored as compact
JSON. If omitted, the store records `{}`.

## Validation And Errors

- Auth is the existing `/api/v1` API-key/session-bound-token gate.
- `project` is required for task list and task create.
- `root_path` must be an absolute path. The API does not require it to exist in
  this phase, matching the CLI/store's lightweight project registration.
- `target_date` must be empty or `YYYY-MM-DD`.
- `status` must be one of the store's Kanban task statuses.
- Missing rows return `404`.
- Malformed JSON and validation errors return `400`.
- Store conflicts return `409`.
- Unexpected store errors return `500`.

## Implementation Boundary

Add a focused server file, `apps/kittypaw/server/api_kanban.go`, and register
routes in `apps/kittypaw/server/server.go`. Add tests in
`apps/kittypaw/server/api_kanban_test.go`.

Do not add client package wrappers in this phase; the Web UI can call the HTTP
routes directly and a typed client can follow when an external consumer needs
one.

## Testing

Tests should use existing server test helpers and real tempdir SQLite stores:

- route registration and auth through `srv.setupRoutes()`
- project create/list/show lifecycle
- board and milestone endpoints
- task create/list/show lifecycle
- claim/complete records run
- comments and links
- validation and not-found behavior

Final verification:

```bash
cd apps/kittypaw
go test ./server -run 'TestKanbanAPI' -count=1
go test ./... -short -count=1
```
