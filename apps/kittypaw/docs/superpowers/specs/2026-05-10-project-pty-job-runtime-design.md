# Project PTY Job Runtime Design

## Purpose

Project PTY Job Runtime extends the existing Projects Job Runtime from
approved one-shot execution to managed interactive sessions.

The goal is to let a user start a `pty` Job in a per-job git worktree, watch
the transcript, and send input while the Driver is still running. This gives
KittyPaw the first practical version of "intervene while Codex, Claude, or a
shell driver is working" without introducing tmux or long-lived session
recovery yet.

## Baseline

Already released in `kittypaw/v0.5.21`:

- Project, Ticket, Job, Driver, and scoped chat records.
- Approved `one_shot` Job execution.
- Account-managed git worktree creation per Job.
- Codex, Claude, and shell Driver process invocation.
- Bounded job logs and job lifecycle events.
- Start, cancel, logs API, web controls, and Projects tool wiring.

This spec is the next slice after Phase 1.5. It keeps the worktree and approval
rules from the one-shot runtime.

## Scope

Included:

- make `jobs.mode = "pty"` executable,
- start a PTY-backed child process in the existing per-job worktree,
- stream PTY output into bounded job log tail and `job_events`,
- accept user input while a PTY Job is running,
- expose input through API, web UI, and Projects engine tool,
- record user input as an event without storing unbounded raw logs,
- cancel the live PTY child process,
- keep daemon restart behavior simple by failing orphaned running Jobs.

Excluded:

- tmux-backed sessions,
- terminal attach or full-screen terminal emulation in the web UI,
- remote/cloud execution,
- automatic worktree cleanup,
- automatic commit, push, branch publish, or PR creation,
- full raw log archive,
- durable live-session recovery after daemon restart,
- direct execution in the project root.

## Product Rules

- `pty` Jobs still require explicit approval before start.
- A `pty` Job belongs to exactly one Ticket and one Project.
- A `pty` Job uses the same account-managed git worktree policy as one-shot
  Jobs.
- At most one Job may be `running` for the same Ticket.
- Starting a `pty` Job moves the Ticket to `in_progress`.
- A succeeded `pty` Job moves the Ticket to `review`.
- A failed `pty` Job moves the Ticket to `blocked`.
- A canceled `pty` Job moves the Ticket back to `backlog`.
- Input is accepted only for live running `pty` Jobs owned by the current
  daemon.
- Input sent to non-running, non-PTY, or orphaned Jobs returns a structured
  error.
- A Job never moves a Ticket directly to `done`.

## Driver Invocation

The runtime keeps `driver_snapshot_json` as the source of truth. The current
mutable Driver definition is not used after a Job has been planned.

Default Driver support changes:

- `codex`: supported modes become `["one_shot", "pty"]`.
- `claude`: supported modes become `["one_shot", "pty"]`.
- `shell`: supported modes become `["one_shot", "pty"]`.

PTY command shapes are based on the local CLI help available on
2026-05-10.

### Codex PTY

Command shape:

```text
codex -C <worktree> --sandbox workspace-write --ask-for-approval on-request --no-alt-screen <prompt>
```

Rules:

- do not use `--dangerously-bypass-approvals-and-sandbox`,
- start in the job worktree,
- pass the approved prompt as the initial prompt,
- use `--no-alt-screen` so transcript capture stays line-oriented enough for
  the current UI,
- allow Codex to request approval inside the interactive session.

### Claude PTY

Command shape:

```text
claude --permission-mode default <prompt>
```

Rules:

- run with the job worktree as the process working directory,
- do not use `--dangerously-skip-permissions`,
- pass the approved prompt as the initial prompt,
- keep Claude in interactive mode by avoiding `-p`/`--print`,
- allow Claude to ask for permission inside the interactive session.

### Shell PTY

Shell Drivers execute the configured command in the PTY. The approved prompt is
sent after process start as an initial input frame with a trailing newline.

Rules:

- run with the job worktree as the process working directory,
- set `KITTYPAW_JOB_PROMPT` and `KITTYPAW_JOB_CONTEXT`,
- record the initial prompt as an `input` event with actor `runtime`,
- rely on the configured shell command to decide how to consume input.

### Custom Drivers

Custom Drivers can support `pty` if their `supported_modes_json` includes
`"pty"`. The runtime invokes the configured command and default args in the
job worktree, then writes the approved prompt as the initial input frame.

## Runtime Architecture

The current `ProjectJobRuntime` keeps the lifecycle owner. It should gain a
separate PTY process adapter rather than overloading the one-shot
`JobCommandRunner`.

Recommended internal shape:

```text
ProjectJobRuntime
  - one-shot path: existing JobCommandRunner
  - pty path: JobPTYRunner
  - running[job_id]: runningProjectJob

runningProjectJob
  - cancel()
  - input(text)
  - mode
```

`JobPTYRunner` is responsible for:

- starting the command through `github.com/creack/pty`,
- returning a handle that accepts writes,
- streaming PTY bytes to the runtime callback,
- waiting for process exit,
- closing the PTY on cancellation.

The runtime remains responsible for:

- worktree readiness checks,
- job status transitions,
- ticket status transitions,
- bounded log tail updates,
- job event creation,
- input authorization checks.

## Input API

Add:

```text
POST /api/v1/jobs/{job}/input
```

Request:

```json
{
  "actor_id": "web",
  "text": "continue\n"
}
```

Response:

```json
{
  "accepted": true,
  "job": { "...": "..." }
}
```

Rules:

- `text` must be non-empty after trimming only NUL bytes; whitespace and
  newline input are valid terminal input.
- request body text is capped at 16 KiB,
- input is written exactly as provided,
- if the text does not end with `\n`, the API does not append one,
- rejected input does not create an event,
- accepted input creates a `job_events` row with type `input`.

Structured errors:

- `job_not_running`,
- `job_input_not_supported`,
- `job_session_unavailable`,
- `job_input_too_large`,
- `driver_process_failed`.

## Engine Tool

The `Projects` tool gains:

```text
appendJobInput(jobID, text)
```

Rules:

- the tool calls the same runtime path as the API,
- natural-language chat can send input to a running Job only when the user
  identifies the Job or the current ticket has exactly one running Job,
- ambiguous input requests should ask which Job to target,
- broad instructions that imply changing ticket scope should plan a new Job
  rather than writing surprise input to a running session.

Examples:

```text
@pm tell KITTY-012 job to continue
@pm send "y" to the running job
@pm stop that job
```

## Web UI

The existing Job detail surface adds a compact input area when the selected Job
is `running` and `mode = "pty"`:

- transcript/log event list,
- single-line input field with a send button,
- `Shift+Enter` for newline in the input field if a textarea is used,
- disabled input state when the Job is not a live PTY session,
- visible latest status after each send.

The UI does not attempt to render a full terminal emulator in this phase. It
uses the existing log/event display so the implementation stays bounded.

The input field is not a secret entry field. User-facing copy should make clear
that terminal input is recorded in Job events. Password-style hidden input is
out of scope for this phase.

## Events and Logs

Existing `job_events` remains the event source. No new table is required for
this phase.

New event types:

```text
pty_started
input
transcript
pty_closed
```

The existing `log` event may still be used for compatibility. The PTY runtime
should use `transcript` for PTY output so UI and tests can distinguish terminal
output from one-shot stdout/stderr chunks.

Transcript storage is display-oriented, not a raw terminal capture:

- decode PTY bytes as UTF-8 and replace invalid byte sequences,
- remove ANSI escape and OSC control sequences before storing event messages,
- keep printable text plus `\n`, `\r`, and `\t`,
- HTML escaping remains the UI layer's responsibility,
- do not store a separate raw PTY byte archive in this phase.

Bounds:

- `jobs.log_tail` remains capped at 64 KiB,
- a single transcript event message is capped at 8 KiB,
- a single input event message is capped at 16 KiB,
- `jobs.log_truncated` is set when output is dropped.

Input event metadata includes:

```json
{
  "bytes": 9,
  "mode": "pty",
  "driver_id": "codex"
}
```

Transcript event metadata includes:

```json
{
  "bytes": 1024,
  "duration_ms": 1200,
  "mode": "pty",
  "driver_id": "codex"
}
```

## Cancellation and Restart

Cancellation remains best-effort:

- cancel invokes the live PTY session cancel function,
- the PTY file is closed,
- the child process is terminated by context cancellation,
- the Job is marked `canceled`,
- the Ticket returns to `backlog`.

On daemon startup, any persisted `running` Job without a live process is marked
`failed` with an error excerpt explaining that the daemon stopped while the Job
was running. This matches Phase 1.5 and avoids pretending an interactive
session can be restored.

## Safety

- No direct project-root execution.
- No automatic commit, push, or PR.
- No dangerous Codex or Claude bypass flags by default.
- No hidden worktree deletion.
- No unbounded transcript retention.
- No input is accepted for Jobs that are not live in this daemon process.
- Input is recorded so later review can distinguish user-entered terminal
  content from driver output.
- Secret input is not supported; recorded input should be treated as project
  history.

## Testing

Required deterministic tests:

- default Drivers include `pty` support,
- planning a `pty` Job stores the selected mode and driver snapshot,
- starting a `pty` Job uses the existing worktree readiness checks,
- PTY start records `pty_started` and moves the Ticket to `in_progress`,
- PTY output updates `jobs.log_tail` and records `transcript` events,
- PTY transcript storage strips ANSI/OSC control sequences before persisting
  event messages,
- accepted input writes to the fake PTY session and records an `input` event,
- input to one-shot Jobs returns `job_input_not_supported`,
- input to completed Jobs returns `job_not_running`,
- input to a running PTY Job missing a live session returns
  `job_session_unavailable`,
- oversized input returns `job_input_too_large`,
- PTY success moves the Ticket to `review`,
- PTY failure moves the Ticket to `blocked`,
- PTY cancel moves the Ticket to `backlog`,
- `POST /api/v1/jobs/{job}/input` returns accepted JSON for live PTY Jobs,
- `Projects.appendJobInput` calls the runtime input path,
- web Projects module includes the Job input endpoint and running PTY input
  controls.

Optional live smoke:

- start a shell PTY Job in a disposable git repository,
- send one input line,
- assert the command echoes it into the transcript,
- cancel or complete the job,
- verify the worktree remains preserved.

Live Codex and Claude PTY smoke tests are useful but must remain opt-in because
they require local auth, network availability, and tool-specific behavior.

## Later Phases

Phase 3 adds tmux-backed sessions, cleanup policy, full log retention opt-in,
driver-specific advanced settings, and optional commit/PR workflows.

Terminal emulation can be considered later if the compact transcript plus input
surface is not enough for real use.

## Self-Review

- The scope is focused on PTY execution and does not include tmux.
- The design reuses the existing Project, Ticket, Job, Driver, worktree, and
  event model.
- No schema table is required beyond possible enum/error additions in code.
- Input, transcript, cancellation, restart, API, UI, engine tool, and tests map
  to explicit requirements.
- The spec avoids automatic commit/push/PR and keeps worktree preservation
  unchanged.
