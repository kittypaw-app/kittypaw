# Project Job Runtime Design

## Purpose

Project Job Runtime turns the Projects MVP job records into real local
execution. It runs an approved ticket job through a selected Driver, records
bounded logs and lifecycle events, and moves the ticket to a useful next state.

This phase implements the first runtime slice: one-shot execution only. It does
not introduce interactive PTY sessions, tmux sessions, or mid-run user input.

## Scope

Included:

- start an `approved` Job with `mode = one_shot`,
- create an account-managed git worktree per Job,
- invoke Codex, Claude, or shell Drivers in that worktree,
- record lifecycle events and bounded log output,
- support best-effort cancellation for running child processes,
- move successful tickets to `review`,
- move failed tickets to `blocked`,
- expose job status, start, cancel, and logs through API and web UI.

Excluded:

- PTY sessions,
- tmux sessions,
- remote or cloud execution,
- full log retention,
- automatic worktree deletion,
- automatic commit, push, or PR creation,
- direct execution in the project root as the default path,
- automatic git initialization or first commit without explicit user approval.

## Product Rules

- A Job belongs to exactly one Ticket, and a Ticket belongs to exactly one
  Project.
- A Job is one attempt. Retrying means creating a new Job, not restarting a
  completed Job.
- Only `approved` Jobs can start.
- Starting a Job moves the Ticket to `in_progress`.
- A succeeded Job moves the Ticket to `review`.
- A failed Job moves the Ticket to `blocked`.
- A canceled Job moves the Ticket back to `backlog`.
- A Job never moves a Ticket directly to `done`.
- At most one Job may be `running` for the same Ticket.
- Worktrees are preserved after completion until the user explicitly cleans
  them up.

## Worktree Policy

The default execution directory is an account-managed git worktree:

```text
~/.kittypaw/accounts/<account>/worktrees/<project_id>/<ticket_id>/<job_id>
```

The branch name is deterministic and human-readable:

```text
kittypaw/<ticket-key>/<job-short-id>
```

The runtime creates the worktree with the project root as the source git
repository. The implementation uses `git worktree add -b <branch> <path>
HEAD`.

The project root must be a git repository with a valid `HEAD`. A repository
with no commit is not enough for Phase 1.5 because there is no stable source
revision for the worktree.

If the project root has uncommitted changes, Job start fails with a clear
message asking the user to commit or stash first. The runtime must not silently
copy uncommitted changes into the worktree.

## Non-Git Projects

When a user tries to start a Job for a non-git Project, KittyPaw does not fall
back to direct project-root execution.

Instead, the start attempt returns an actionable blocked result:

```text
This project is not a git repository. Initialize git for this project?
```

If the user approves, KittyPaw runs `git init` in the project root. It must not
stage files or create an initial commit automatically in Phase 1.5. If the
repository still has no `HEAD`, the next start attempt returns:

```text
Create an initial commit before starting a job.
```

This keeps the default runtime safe while still guiding new folders into the
git-backed workflow.

## Driver Invocation

Drivers are account-level execution adapters. The runtime resolves the Driver
from the Job's stored `driver_snapshot_json`, not from the current mutable
driver definition.

The built-in defaults are:

### Codex

Command shape:

```text
codex exec -C <worktree> --json --sandbox workspace-write --ask-for-approval never <prompt>
```

Rules:

- do not use `--dangerously-bypass-approvals-and-sandbox`,
- capture JSONL stdout as log events,
- treat process exit code `0` as success,
- treat non-zero exit as failure.

### Claude

Command shape:

```text
claude -p --output-format stream-json --permission-mode acceptEdits <prompt>
```

Rules:

- run with the worktree as the process working directory,
- do not use `--dangerously-skip-permissions` by default,
- capture stream JSON output as log events,
- treat process exit code `0` as success,
- treat non-zero exit as failure.

### Shell

Shell Drivers execute the configured command in the worktree. The prompt text
is provided on stdin and also through `KITTYPAW_JOB_PROMPT` for simple scripts.

The shell Driver is intended for explicit, user-configured automation, not for
arbitrary LLM-generated shell execution.

## Prompt Contract

The Job prompt sent to a Driver contains:

- project key and name,
- project root path,
- ticket key, title, body, status, and priority,
- job id,
- selected driver and mode,
- user-approved prompt text,
- explicit instruction to leave changes in the worktree,
- explicit instruction not to commit, push, or open a PR unless a later phase
  adds that capability.

The Driver should produce a concise final summary. The runtime stores that
summary in `jobs.result_summary` when it can extract one. If no final summary
is available, the runtime stores a short status-derived summary.

## Runtime State

The existing `jobs` table already contains the core fields:

```text
status
worktree_path
branch_name
prompt_summary
prompt_text
result_summary
log_tail
error_excerpt
log_truncated
driver_snapshot_json
approved_by
started_at
finished_at
```

Phase 1.5 adds a migration with:

```text
exit_code INTEGER
```

The runtime does not persist process IDs as authority. PIDs can be reused after
daemon restart. Live processes are tracked in memory by `job_id`; persisted
records are the source of truth for durable status.

On daemon startup, any `running` Job without a live process is marked `failed`
with an error excerpt explaining that the daemon stopped while the Job was
running.

## Job Events

The runtime records these event types in `job_events`:

```text
started
log
succeeded
failed
canceled
cleanup
```

Event metadata is JSON. For process completion events it includes:

```json
{
  "exit_code": 0,
  "duration_ms": 1234,
  "driver_id": "codex",
  "mode": "one_shot",
  "worktree_path": "...",
  "branch_name": "..."
}
```

Log events store bounded chunks. They are not full transcript retention.

## Log Bounds

Phase 1.5 keeps logs intentionally small:

- `jobs.log_tail` stores the last 64 KiB of combined stdout/stderr text,
- a single `job_events` log message is capped at 8 KiB,
- `jobs.error_excerpt` stores at most 4 KiB,
- `jobs.log_truncated` is set when any output was dropped.

Full logs are a later opt-in feature.

## API

Existing endpoints are extended:

```text
POST /api/v1/projects/{project}/git/init
POST /api/v1/jobs/{job}/start
POST /api/v1/jobs/{job}/cancel
GET  /api/v1/jobs/{job}/logs
```

`POST /api/v1/projects/{project}/git/init`:

- requires explicit user approval from the UI or chat flow,
- runs `git init` in the project root,
- does not stage files,
- does not create a commit,
- returns project git readiness after initialization.

`POST /api/v1/jobs/{job}/start`:

- requires `approved`,
- validates project git readiness,
- validates no running Job for the same Ticket,
- creates the worktree,
- updates Job to `running`,
- starts the child process asynchronously,
- returns HTTP `202` with the updated Job.

`POST /api/v1/jobs/{job}/cancel`:

- cancels the live child process when present,
- marks the Job `canceled`,
- records a `canceled` event,
- returns the Ticket to `backlog`.

`GET /api/v1/jobs/{job}/logs` returns:

- current Job,
- log tail,
- lifecycle events.

If start cannot proceed because git initialization or an initial commit is
needed, the API returns HTTP `409` with a structured error code.

## Engine Tool

The `Projects` tool gains:

```text
initProjectGit(projectID)
startJob(jobID, options?)
jobLogs(jobID)
```

`initProjectGit` follows the same approval rule as the API and never stages or
commits files.

`startJob` follows the same approval and readiness checks as the API.

Natural language may propose a Job plan, approve an existing Job, or start an
approved Job, but broad or dangerous changes still require explicit approval.

## Web UI

The Projects UI adds a Job detail surface reachable from Ticket detail and the
Project `Jobs` tab.

The surface shows:

- Job status,
- driver and mode,
- branch and worktree path,
- prompt summary,
- latest result summary,
- log tail,
- lifecycle events,
- `Start`, `Cancel`, and `Open Worktree` actions when applicable.

For non-git Projects, start shows the git initialization prompt instead of
silently falling back to direct execution.

## Error Handling

Start errors are explicit:

- `job_not_approved`,
- `job_already_started`,
- `ticket_has_running_job`,
- `project_not_git_repository`,
- `project_git_head_missing`,
- `project_git_dirty`,
- `worktree_create_failed`,
- `driver_not_found`,
- `driver_mode_unsupported`,
- `driver_process_failed`.

User-facing copy should include the ticket key, driver name, and next action.

## Safety

- No direct project-root execution by default.
- No automatic `git add`, commit, push, or PR.
- No dangerous Codex or Claude bypass flags by default.
- No use of uncommitted project-root changes.
- No hidden deletion of worktrees.
- Cancellation is best-effort and only authoritative for live processes owned
  by the current daemon.

## Testing

Required deterministic tests:

- store migration adds `exit_code`,
- approved Job can transition to `running`, `succeeded`, `failed`, and
  `canceled`,
- start rejects non-approved Jobs,
- start rejects a second running Job for the same Ticket,
- non-git Project returns a git initialization requirement,
- git repo with no `HEAD` returns an initial commit requirement,
- dirty project root blocks start,
- fake shell Driver records logs and exit code,
- success moves Ticket to `review`,
- failure moves Ticket to `blocked`,
- cancellation moves Ticket to `backlog`,
- `/api/v1/jobs/{job}/start` returns `202`,
- `/api/v1/jobs/{job}/logs` returns Job, events, and bounded tail,
- `/api/v1/projects/{project}/git/init` initializes git without staging files,
- `Projects.startJob` and `Projects.jobLogs` call the runtime path.

Optional live tests are gated behind environment variables and may exercise
real `codex exec` and `claude -p` on a temporary repository.

## Later Phases

Phase 2 adds managed PTY sessions, user input forwarding, and interactive
transcript handling.

Phase 3 adds tmux-backed sessions, cleanup policy, full log retention opt-in,
driver-specific advanced settings, and optional commit/PR workflows.

## Self-Review

- No placeholder sections remain.
- The scope is limited to one-shot execution and does not smuggle PTY/tmux into
  Phase 1.5.
- The non-git policy is explicit and does not imply unsafe auto-commits.
- The data-model delta is minimal: only `exit_code` is required.
- Error, API, UI, and test expectations all map to the runtime rules above.
