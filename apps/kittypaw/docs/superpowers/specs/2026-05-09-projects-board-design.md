# Projects / Board Design

## Purpose

KittyPaw Projects replaces the current Workspace and Kanban concepts with a
project-centered work system. A project is a local folder plus its project chat,
tickets, board view, jobs, drivers, staff assignments, and file index.

This is not a traditional Kanban board with comments attached. It is a
conversation-first project system that renders tickets as a board.

## Product Principles

- `Kanban` is removed as a product concept.
- `Workspace` is removed as a product concept.
- A `Project` is the folder KittyPaw manages.
- Project state lives in the account database, not in a `.paw` folder.
- Project and ticket chats use the normal conversation model.
- A board is a view over tickets, actions, and messages. It is not the source
  of truth.
- Every file/search operation in project or ticket chat is scoped to that
  project folder.
- Every `Job` belongs to a `Ticket`; every `Ticket` belongs to a `Project`.
- `Driver` is the pluggable backend used by the Job Runtime to perform a job.
- Dangerous or broad changes require user approval before being committed or
  executed.

## Terminology

`Project` is a managed folder. It has a name, immutable key once tickets exist,
root path, default driver settings, autonomy policy, and staff assignments.

`Board` is the project view that groups tickets by status.

`Ticket` is an actionable work item. User-facing ticket keys use a project
prefix and sequence such as `KITTY-001`.

`Job` is one attempt to process a ticket. A job can be planned, approved,
running, succeeded, failed, or canceled. A job stores the driver, mode,
worktree, branch, prompt summary, prompt text, result summary, bounded logs,
and events for that attempt.

`Driver` is a global account-level execution adapter such as Codex, Claude, or
shell. A project chooses default driver settings. A job stores a resolved driver
snapshot so historical records stay stable when driver config changes later.

`Project Kickoff` is the project-start conversation led by the project PM.

`Project Brief Draft` is the editable draft produced during kickoff. It
contains a short project briefing plus proposed tickets, priorities,
dependencies, and staff assignments. It is committed only after user approval.

`Staff Assignment` connects staff to a project or ticket with roles such as
`pm`, `developer`, `reviewer`, or `owner`.

## Removed Concepts

`Workspace` is removed. Project folders replace workspace registration,
workspace-project binding, file allowlists, and indexing scope.

`Kanban` is removed. Existing Kanban APIs, UI, engine tool surface, and schema
are replaced by Projects, Tickets, Jobs, and Board.

`Milestone` is excluded from the MVP and the first schema. The initial planning
model uses tickets, priority, dependencies, labels, and staff assignments. A
future phase may add optional grouping if real usage shows a need.

`Onboarding` is avoided for project flow naming because it conflicts with
KittyPaw product onboarding. Use `Project Kickoff` and `Project Brief Draft`.

## Project Creation Flow

The user starts from the Projects home and selects `New Project`.

1. The user chooses a project folder through a local folder picker.
2. KittyPaw analyzes the folder name and lightweight metadata.
3. KittyPaw shows a confirmation screen:
   - project folder,
   - project name,
   - project key,
   - PM staff candidate,
   - default driver,
   - default job mode,
   - autonomy policy.
4. The user approves or edits the proposed values.
5. KittyPaw creates the project.
6. KittyPaw creates the project chat.
7. KittyPaw stores project staff assignments.
8. KittyPaw starts background indexing for the project folder.
9. The PM starts Project Kickoff in project chat.

The project key is suggested before creation. The key may be edited until the
first ticket is created. After the first ticket exists, the project key is
locked. Project name remains editable.

## Empty And Non-Empty Folders

KittyPaw classifies project folders as empty-ish or non-empty using both file
count and project indicator files.

Empty-ish folders may contain only incidental files such as `.git`,
`.DS_Store`, `.gitignore`, `.env.example`, or a simple README.

Non-empty projects include folders with project indicator files such as
`package.json`, `go.mod`, `Cargo.toml`, `Package.swift`, `pyproject.toml`,
`requirements.txt`, `Gemfile`, `pom.xml`, `build.gradle`, or `Makefile`, or
folders with enough meaningful files/directories to indicate real content.

For empty-ish folders, the PM starts with:

```text
이 프로젝트에서 무엇을 만들까요?
```

For non-empty folders, the PM starts with:

```text
내용을 파악해서 티켓 초안을 만들까요?
```

## Project Kickoff And Brief Draft

Project Kickoff is chat-led by the project PM.

When the user approves analysis, KittyPaw performs a structure-focused scan:

- directory structure,
- README, AGENTS, CLAUDE, CODEX, and similar project instruction files,
- common language and build configuration files,
- git status and recent commits,
- likely build and test commands,
- TODO/FIXME markers.

The default output is a concise project briefing plus 5-8 proposed tickets.
Each proposed ticket includes title, purpose, priority, suggested staff,
dependencies if any, and evidence from the scan.

The user may edit the whole draft through chat:

- split or merge tickets,
- remove items from MVP,
- reprioritize work,
- change assignees,
- add dependencies,
- ask for task-first or larger-plan structure.

Approval commits the Project Brief Draft as a plan commit. Committing creates:

- tickets,
- ticket dependencies,
- project staff assignments,
- ticket staff assignments,
- project chat messages,
- ticket chats,
- initial ticket chat PM briefing messages,
- ticket action/event records.

## Staff Model

Project PM selection uses staff metadata first and name/alias heuristics second.

Candidate order:

1. active staff with role/tag `project-manager` or `pm`,
2. active staff with matching display name, alias, or ID such as `pm`,
   `project manager`, `개발PM`, or `프로젝트 매니저`,
3. default assistant.

If the user wants a new PM, KittyPaw creates the project first with the current
candidate, then runs the existing staff draft and approval flow in project
chat. After approval, the new PM becomes the primary project PM and the project
chat's staff is switched to that PM.

Project staff assignments are separate from ticket staff assignments.

```text
project_staff_assignments
- project_id
- staff_id
- role
- is_primary
- created_at

ticket_staff_assignments
- ticket_id
- staff_id
- role
- is_primary
- created_at
```

## Conversation Model

Project chat and ticket chat are normal conversations with scope metadata.

```text
conversation_scope
- conversation_id
- scope_type: general | project | ticket
- scope_id
```

Project chat defaults to the primary PM staff. Ticket chat defaults to the
ticket's primary staff assignment. If no ticket staff exists, ticket chat falls
back to the project PM.

Resolution rules:

- Project chat commands and natural language default to that project.
- Ticket chat commands and natural language default to that ticket, and use the
  same project for related ticket queries.
- General chat has no project scope. Ambiguous project references must ask the
  user to choose.
- Explicit ticket keys such as `KITTY-001` may resolve from any conversation.
- Do not use recent-project fallback for ambiguous actions.

## Ticket Model

Tickets are actionable work units. Each ticket has an internal stable ID and a
user-facing key.

```text
tickets
- id
- project_id
- key
- title
- body
- status
- priority
- labels_json
- created_by
- archived_at
- created_at
- updated_at
```

Default statuses:

```text
draft
backlog
ready
in_progress
blocked
review
done
archived
```

Project Brief Drafts may contain draft tickets that are not committed as real
tickets yet. Committed tickets normally start in `backlog` or `ready`.

Single-ticket chat requests can create tickets directly when the request is
clear. Multi-ticket planning requests, project scan results, and broad roadmap
requests use draft and approval first.

Ticket deletion defaults to archive. Hard delete is an advanced operation with
strong confirmation and clear scope display.

## Ticket Dependencies

Ticket dependencies are explicit records.

```text
ticket_dependencies
- id
- project_id
- blocker_ticket_id
- blocked_ticket_id
- type: blocks | relates_to | duplicates
- created_by
- created_at
```

Blocked tickets should not be silently promoted when blockers finish. The PM or
system can propose promotion in chat.

## Action And Event Model

State changes are action/message based.

- Dragging a card creates a status-change action and event.
- Chat commands such as "move this to ready" create the same action and event.
- Job lifecycle updates create action/event records.
- The board renders the resulting ticket state.

Clear single-ticket status changes may be performed directly by the LLM tool.
Ambiguous or multi-ticket changes require clarification or a draft.

## Board UI

Projects home shows a list of projects with:

- project name and key,
- root path,
- PM staff,
- open ticket count,
- blocked count,
- review count,
- active job count,
- last activity,
- open board action,
- open project chat action.

Project page tabs:

- `Board`
- `Project Chat`
- `Jobs`
- `Settings`

Ticket click opens a ticket-chat-centered detail view. Ticket metadata appears
as a compact side or header panel. The metadata panel shows status, staff,
driver default, dependencies, labels, latest job, key, and title.

## Job Model

Every job belongs to a ticket. Jobs are created from ticket chat or explicit
commands when the user asks KittyPaw to process a ticket.

Job planning flow:

1. User asks to process a ticket.
2. Project PM writes a job plan and driver prompt.
3. KittyPaw shows a concise plan for approval.
4. User approves.
5. KittyPaw creates or prepares a git worktree.
6. Job Runtime invokes the selected driver.
7. Job events, bounded logs, and summaries are recorded.
8. Ticket status is updated.

Job success moves the ticket to `review`. Job failure moves the ticket to
`blocked`. A job never moves a ticket directly to `done`.

```text
jobs
- id
- project_id
- ticket_id
- driver_id
- mode: one_shot | pty | tmux
- status: planned | approved | running | succeeded | failed | canceled
- worktree_path
- branch_name
- prompt_summary
- prompt_text
- result_summary
- log_tail
- error_excerpt
- log_truncated
- created_by
- approved_by
- started_at
- finished_at
- created_at
- updated_at
```

The default worktree location is account-managed, not inside the project
folder:

```text
~/.kittypaw/accounts/<account>/worktrees/<project_id>/<ticket_id>/
```

Worktrees are preserved by default after jobs finish. Cleanup is an explicit
user action or later policy.

## Driver Model

Drivers are global account-level execution backends. They are not normal
skills. Skills are function calls inside a turn. Drivers are invoked by the Job
Runtime and can manage long-running processes, PTY/tmux sessions, logs,
interrupts, cancellation, and lifecycle state.

```text
driver_definitions
- id
- display_name
- command
- supported_modes_json
- default_args_json
- enabled
- created_at
- updated_at
```

Project-level driver settings choose defaults:

```text
project_driver_settings
- project_id
- default_driver_id
- default_mode
- default_worktree_policy
- autonomy_policy
- created_at
- updated_at
```

A job stores the resolved driver snapshot needed to explain historical jobs
even after account driver settings change.

User-facing copy should say "driver" only where appropriate for power users.
Korean UI can use "구동 방식" or "실행 도구" while the internal term remains
Driver.

## Autonomy Policy

Autonomy is project-level. The default policy is `edit_and_test`.

Default `edit_and_test` policy:

- file reading allowed,
- file editing allowed,
- tests allowed,
- normal commands limited to the approved job plan,
- dependency installation requires approval,
- network requires approval,
- destructive commands are denied.

Every job plan shows the effective autonomy policy before approval.

## Job Logs

Job logs are bounded by default. KittyPaw does not store unlimited raw
stdout/stderr in the database.

Always store:

- job metadata,
- start, finish, error, and cancellation events,
- PM/driver summary,
- bounded log tail,
- error excerpt,
- user input sent to interactive sessions.

Driver output stores bounded tail and excerpts. Full raw logs are opt-in per
project or per job and still require a hard cap. Truncation must be explicit
through `log_truncated`.

Potential storage:

```text
job_events
- id
- job_id
- type
- actor_id
- message
- metadata_json
- created_at

job_log_chunks
- id
- job_id
- stream: stdout | stderr | system
- seq
- content
- created_at
```

Interactive mode stores user input as durable job events. Driver output remains
bounded unless full log retention is explicitly enabled.

## API Surface

The new API namespace keeps `/api/v1`:

```text
/api/v1/projects
/api/v1/tickets
/api/v1/jobs
/api/v1/drivers
```

Representative endpoints:

```text
GET  /api/v1/projects
POST /api/v1/projects
GET  /api/v1/projects/{project}
GET  /api/v1/projects/{project}/board
GET  /api/v1/projects/{project}/brief-drafts
POST /api/v1/projects/{project}/brief-drafts
POST /api/v1/projects/{project}/brief-drafts/{draft}/messages
POST /api/v1/projects/{project}/brief-drafts/{draft}/commit

GET  /api/v1/tickets?project={project}
POST /api/v1/tickets
GET  /api/v1/tickets/{ticket}
PATCH /api/v1/tickets/{ticket}
POST /api/v1/tickets/{ticket}/actions
POST /api/v1/tickets/{ticket}/archive
GET  /api/v1/tickets/{ticket}/jobs
POST /api/v1/tickets/{ticket}/jobs/plan

GET  /api/v1/jobs/{job}
POST /api/v1/jobs/{job}/approve
POST /api/v1/jobs/{job}/start
POST /api/v1/jobs/{job}/input
POST /api/v1/jobs/{job}/cancel
GET  /api/v1/jobs/{job}/logs

GET  /api/v1/drivers
POST /api/v1/drivers
PATCH /api/v1/drivers/{driver}
```

Exact endpoint count can be reduced during implementation, but API naming must
stay Projects/Tickets/Jobs/Drivers rather than Kanban/Workspace.

## Slash Commands

Commands provide deterministic control alongside natural language:

```text
/projects
/project current
/project use <key>
/project show <key>
/project new
/project settings

/tickets
/ticket show <key>
/ticket chat <key>
/ticket job <key>
/ticket move <key> <status>
/ticket block <key> <reason>
/ticket done <key>
```

`/project new` opens the UI folder picker. Direct path creation is out of MVP
scope.

## Engine Tool Surface

The LLM-facing tool is `Projects`, not `Kanban`.

Representative methods:

```text
Projects.list()
Projects.current()
Projects.show(project)
Projects.listTickets(project)
Projects.createTicket(...)
Projects.showTicket(ticket)
Projects.updateTicket(...)
Projects.moveTicket(...)
Projects.commentTicket(...)
Projects.createBriefDraft(...)
Projects.updateBriefDraft(...)
Projects.commitBriefDraft(...)
Projects.planJob(ticket)
Projects.showJob(job)
Projects.cancelJob(job)
Projects.appendJobInput(job, text)
```

Allowed direct writes:

- single clear ticket creation,
- ticket/project lookup,
- message/comment addition,
- clear single-ticket status changes,
- ticket metadata changes,
- archive.

Approval required:

- Project Brief Draft commit,
- bulk ticket creation,
- job approval/start,
- driver execution,
- hard delete,
- project archive/delete,
- autonomy policy change,
- driver settings change.

## File And Search Scope

File and search tools use the current conversation's project scope.

- Project chat root is the project folder.
- Ticket chat root is the ticket's project folder.
- General chat has no project root. File actions must ask the user to choose a
  project when ambiguous.
- Explicit absolute paths outside the project root are denied by default or
  require explicit approval.

Indexing starts automatically after project creation. Existing indexer exclude
rules are reused and project-level include/exclude overrides are supported by
schema. UI for overrides can come later.

## MVP 1 Scope

MVP 1 establishes the new product model without completing full driver
execution.

Included:

- remove Workspace from user-facing Projects flow,
- create Projects home,
- create `New Project` folder picker and confirmation screen,
- create project, project key, project chat, and PM assignment,
- start background project indexing,
- implement Project Kickoff messages for empty-ish and non-empty folders,
- implement Project Brief Draft create/update/commit,
- commit brief drafts into tickets, dependencies, staff assignments, and ticket
  chats,
- implement Board view with ticket-chat-centered detail,
- implement ticket create/show/update/status/archive flows,
- implement project/ticket conversation scope,
- implement `Projects` engine tool for non-execution operations,
- implement job plan records and approval UI state,
- remove old Kanban and Workspace UI/API surfaces.

Excluded from MVP 1:

- actual Codex/Claude/shell driver execution,
- PTY or tmux interactive sessions,
- full log chunk retention,
- hard delete UI,
- project include/exclude editor UI,
- direct path creation through `/project new <path>`,
- optional milestone/grouping model.

## Later Phases

Phase 1.5 adds one-shot driver execution for approved jobs, account-managed
git worktrees, bounded job logs, success-to-review, failure-to-blocked, and job
detail UI.

Phase 2 adds managed PTY sessions, user input forwarding, cancellation, and
interactive transcript/event handling.

Phase 3 adds tmux-backed sessions, cleanup policy, fuller driver settings UI,
full log retention opt-in, and advanced project/ticket reporting.

## Migration And Removal

Because the current Kanban and Workspace surfaces are not treated as stable
public contracts, Projects can replace them directly.

Remove or rewrite:

- `/api/v1/kanban` endpoints,
- old project/kanban task APIs that encode Kanban vocabulary,
- old Kanban web view,
- `Kanban` engine tool,
- workspace management UI as a primary concept,
- workspace-to-project binding code.

Store migrations may add new Projects schema and leave old tables unused or
drop/recreate local databases during development. Since the product is still
single-user/local in practice, compatibility is less important than a coherent
Projects model.

## Testing

Tests should cover:

- project creation from a selected folder,
- project key suggestion and lock after first ticket,
- empty-ish versus non-empty folder classification,
- project chat creation and scope,
- ticket chat creation and scope,
- staff assignment selection and fallback,
- Project Brief Draft edit and commit,
- direct single-ticket creation,
- bulk ticket request requiring draft/approval,
- status action/event creation,
- Board rendering from ticket state,
- job plan creation requiring approval before start,
- file/search scope using project root,
- old Kanban routes removed,
- old Workspace UI removed as a primary product surface.
