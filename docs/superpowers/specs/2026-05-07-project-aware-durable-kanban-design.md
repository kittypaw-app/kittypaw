# Project-Aware Durable Kanban Design

Date: 2026-05-07
Status: Draft approved for staged implementation

## Goal

Add a local, durable Kanban system to `apps/kittypaw` that can track real
project work before any automatic runner dispatcher exists. The first MVP should
be useful from the CLI and stable enough for later Web UI and runner tool
layers.

The MVP introduces Project, Board, Milestone, Task, and Run as durable local
objects owned by a single Kittypaw account store.

## Terminology

- Project: a workstream, product, repository, or folder-backed effort. A
  project has a `root_path`.
- Project root: the project 기준 경로. Usually a repository root, but it is
  just a filesystem path.
- Board: a project-level flow view. Boards own task status columns.
- Milestone: a project-level goal, release, or delivery scope.
- Task: the actual work item. A task belongs to one project and one board, and
  may belong to one milestone.
- Run: one concrete attempt to work on a task. A run records `work_dir`, actor,
  result, metadata, and outcome.
- Work dir: the filesystem path used by one run.

Do not use "worktree" as a product metaphor. Git worktree support is reserved
as a future run work-dir provider named `git_worktree`.

## Domain Model

```text
Project
  root_path
  Boards
  Milestones
  Tasks

Board
  project_id
  columns/status policy

Milestone
  project_id
  target_date
  status

Task
  project_id
  board_id
  milestone_id?
  status
  priority
  assignee?

Run
  task_id
  work_dir
  work_dir_provider
  actor
  outcome
```

Board and Milestone are sibling axes under Project. Task references both.

## Status Model

Initial task statuses:

- `triage`: captured but not ready.
- `todo`: accepted backlog.
- `ready`: unblocked and claimable.
- `running`: active run in progress.
- `blocked`: waiting on a person or dependency.
- `done`: completed.
- `archived`: hidden from normal boards.

Initial run outcomes:

- `running`
- `completed`
- `blocked`
- `failed`
- `canceled`
- `reclaimed`

The kernel should centralize state transitions so CLI, server API, Web UI, and
future runner tools do not each invent their own rules.

## Storage

Persistence lives in the existing `apps/kittypaw/store` SQLite database and
migration system. No shared runtime package is introduced.

Tables:

- `kanban_projects`
- `kanban_boards`
- `kanban_milestones`
- `kanban_tasks`
- `kanban_task_links`
- `kanban_task_comments`
- `kanban_task_events`
- `kanban_task_runs`

IDs are string IDs with stable prefixes:

- `prj_`
- `brd_`
- `ms_`
- `tsk_`
- `run_`
- `evt_`
- `cmt_`

Default objects:

- Creating a project also creates a default board.
- A task created without an explicit board uses the project's default board.
- A task may omit milestone.
- A run without an explicit work dir uses the project's `root_path`.
- Initial `work_dir_provider` values are `project_root`, `manual`, and
  `scratch`. The first implementation records `scratch` only if the directory
  already exists or is explicitly supplied; automatic scratch directory
  lifecycle can follow later.

## Kernel Operations

Project operations:

- create, get, list, update, archive

Board operations:

- create, get, list by project

Milestone operations:

- create, get, list by project, update status

Task operations:

- create
- list by project, board, milestone, status
- show detail with comments, events, runs, and links
- assign
- move status
- link parent/child
- comment
- claim
- complete
- block
- unblock
- archive

Run operations:

- start on claim
- heartbeat timestamp update
- complete with summary and metadata JSON
- block with reason
- fail/cancel/reclaim
- list by task

## Dependency Rules

`kanban_task_links` supports parent/child dependency edges. The first MVP
implements only `blocks` edges:

- A child task with incomplete blocking parents cannot be `ready`.
- When the last blocking parent becomes `done`, a `todo` child can be promoted
  to `ready`.
- Promotion records an event.

Cycles are rejected at link creation.

## CLI Surface

Initial commands:

```bash
kittypaw project create <slug> --root <path> [--name <name>]
kittypaw project list
kittypaw project show <project>
kittypaw project board list <project>

kittypaw project milestone create <project> <title> [--target-date YYYY-MM-DD]
kittypaw project milestone list <project>

kittypaw kanban create <title> --project <project> [--board <board>] [--milestone <milestone>] [--body <text>] [--assignee <staff>]
kittypaw kanban list --project <project> [--board <board>] [--milestone <milestone>] [--status <status>]
kittypaw kanban show <task>
kittypaw kanban claim <task> [--work-dir <path>] [--actor <name>]
kittypaw kanban complete <task> --summary <text> [--metadata <json>]
kittypaw kanban block <task> <reason>
kittypaw kanban unblock <task> [--comment <text>]
kittypaw kanban comment <task> <body>
kittypaw kanban link <parent> <child>
kittypaw kanban runs <task>
```

The CLI uses local account resolution like other account-scoped commands.

## Server API

The Web UI and future runner tools should use the same kernel through server
handlers. The first API pass mirrors the CLI operations under `/api/v1`:

- `/api/v1/projects`
- `/api/v1/projects/{id}/boards`
- `/api/v1/projects/{id}/milestones`
- `/api/v1/kanban/tasks`
- `/api/v1/kanban/tasks/{id}`
- `/api/v1/kanban/tasks/{id}/comments`
- `/api/v1/kanban/tasks/{id}/runs`
- `/api/v1/kanban/tasks/{id}/claim`
- `/api/v1/kanban/tasks/{id}/complete`
- `/api/v1/kanban/tasks/{id}/block`
- `/api/v1/kanban/tasks/{id}/unblock`

All routes remain account-scoped through the existing local server auth model.

## Web UI

The Web UI is a follow-up after CLI/kernel stability:

- project selector
- board columns
- milestone filter
- task drawer
- comments
- run history

Polling is acceptable for the first Web UI. Dedicated live updates can follow
after the dispatcher exists.

## Runner Toolset And Dispatcher

Runner tools are a follow-up after the CLI/API kernel is stable:

- `Kanban.show`
- `Kanban.create`
- `Kanban.claim`
- `Kanban.complete`
- `Kanban.block`
- `Kanban.comment`
- `Kanban.link`
- `Kanban.heartbeat`

The automatic dispatcher is a separate follow-up:

- ready-task claim loop
- staff-assigned runner launch
- heartbeat timeout
- failure limit
- retry/reclaim
- future `git_worktree` work-dir provider

## Implementation Stages

1. Kernel schema and store methods.
2. Project CLI, including board and milestone subcommands.
3. Task, link, comment, run CLI.
4. State-transition tests and dependency promotion.
5. Server API.
6. Web UI.
7. Runner tools.
8. Dispatcher and richer work-dir providers.

The first implementation target is stages 1-4.

## Testing

Minimum coverage for stages 1-4:

- migrations apply on empty `:memory:` store.
- project creation creates a default board.
- task creation resolves project/default board.
- milestone assignment is optional.
- claim starts a run, records `work_dir`, and moves task to `running`.
- complete closes the latest run, records summary/metadata, and moves task to
  `done`.
- block/unblock produce events and status changes.
- dependency cycles are rejected.
- completing the last blocking parent promotes child to `ready`.
- CLI command tests cover JSON and text output enough to protect command
  contracts.

## Out Of Scope For First Target

- automatic runner dispatcher
- Git worktree creation
- background run process management
- WebSocket live board updates
- hosted cross-service contracts
- direct cross-account task sharing
