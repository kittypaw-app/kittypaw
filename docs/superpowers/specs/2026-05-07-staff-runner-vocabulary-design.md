# Staff And Runner Vocabulary Design

## Goal

Replace the overloaded LegacyStaff, LegacyIdentity, and LegacyRunner vocabulary with three
separate domain concepts:

- Account: the user, workspace, configuration, and data isolation boundary.
- Staff: a named assistant owned by an Account. Staff owns SOUL.md and optional
  USER.md identity files.
- Runner: the execution subject that runs Staff through the engine loop,
  sandbox, observation flow, and delegation flow.

This is a breaking rename. Public code, JavaScript tool names, HTTP routes,
CLI slash commands, config fields, docs, tests, and user-visible errors should
use Staff and Runner after this change.

## Non Goals

- Do not keep LegacyStaff, LegacyIdentity, or LegacyRunner as compatibility aliases in the public
  skill surface, API surface, or CLI command surface.
- Do not rename unrelated platform terms such as HTTP User-Agent, macOS
  LaunchAgent, or upstream .agents skill registry paths.
- Do not change SOUL.md as a file name.
- Do not expand Kanban behavior beyond naming updates required by this
  vocabulary change.

## Domain Model

Account remains the durable data boundary. Each account has isolated config,
data, skills, packages, and staff.

Staff is the durable identity. A StaffID identifies one named assistant inside
an account. The staff directory contains SOUL.md and may contain USER.md and
preset metadata. Staff metadata records description, active state, equipped
skills, creator, and timestamps.

Runner is runtime behavior. A runner assembles prompts, executes JavaScript,
handles Runner.observe interrupts, and delegates work to another staff through
Runner.delegate. Runner is not the owner of SOUL.md and should not be used as a
synonym for the person-like assistant.

Conversation history is neither Staff nor Runner. Existing LegacyRunnerState and
LegacyRunnerID names should become ConversationState and ConversationID because that
data stores the account conversation timeline.

## Breaking Surface Changes

Core code:

- core.LegacyStaff becomes core.Staff.
- core.LegacyStaffConfig becomes core.StaffConfig.
- core.ValidateLegacyStaffID becomes core.ValidateStaffID.
- core.LoadLegacyStaff, EnsureDefaultLegacyStaff, ApplyPreset, DetectDirty, and
  PresetStatus become Staff-named functions.
- Account.LegacyStaffDir becomes Account.StaffDir or Account.StaffRootDir.

Store:

- store.LegacyStaffMeta becomes store.StaffMeta.
- profile_meta becomes staff_meta through a new SQLite migration.
- Store methods are renamed to UpsertStaffMeta, GetStaffMeta,
  ListActiveStaff, SetStaffActive, and UpdateEquippedStaffSkills.
- user_context keys change from legacy_active_staff:<conversation> to
  active_staff:<conversation>.

Config:

- Config.LegacyStaffs becomes Config.Staff.
- TOML key legacy staff dirs becomes staff.
- LegacyRunnerConfig, LegacyRunners, FindLegacyRunner, and DefaultLegacyRunner become RunnerConfig,
  Runners, FindRunner, and DefaultRunner if the config is still active.
  If those fields are unused, remove or narrow them instead of carrying stale
  vocabulary forward.

Engine and sandbox:

- JavaScript LegacyStaff.list, LegacyStaff.switch, LegacyStaff.create, and LegacyStaff.update
  become Staff.list, Staff.switch, Staff.create, and Staff.update.
- JavaScript LegacyRunner.observe becomes Runner.observe.
- JavaScript LegacyRunner.delegate becomes Runner.delegate(staffId, task, background?).
- Prompt text, tool metadata, code normalization, tests, and errors use
  Runner.observe and Staff terms only.
- Delegation structs and comments should use StaffID when naming the target
  assistant and Runner when naming execution.

CLI and chat commands:

- /legacy_identity becomes /staff.
- Help text and errors use staff ID.
- Root CLI tests that assert no legacy_identity or legacy_runner management should be updated
  to the new public policy.

Server and client:

- /api/v1/legacy staff dirs becomes /api/v1/staff.
- Client methods LegacyStaffList and LegacyStaffActivate become StaffList and
  StaffActivate.
- JSON response keys use staff unless a more specific object name is better.

Filesystem:

- Account staff identity directories move from legacy staff dirs/<id>/ to staff/<id>/.
- New account setup creates staff/default/SOUL.md.
- One-way local migration renames legacy staff dirs to staff when staff does not already
  exist. After migration, code only reads staff.

Docs and Kanban:

- Kanban docs and tests should use runner tools or staff assignment wording
  instead of legacy_runner toolset/legacy_staff worker wording.
- Kanban assignee help text should say staff or assignee staff ID.

## Data Migration

The rename is breaking at the API and code level, but existing local data should
not be discarded.

SQLite should receive a new migration that renames profile_meta to staff_meta
or creates staff_meta from profile_meta and then drops the old table. Runtime
code must reference only staff_meta after the migration.

Filesystem migration should run through the existing account migration path. If
an account has legacy staff dirs/ and does not have staff/, rename profiles/ to staff/.
If both exist, leave both untouched and prefer staff/ so the operator can
resolve the conflict manually. The implementation should surface a clear log or
error for this conflict where the existing migration pattern supports it.

user_context active selection keys are not migrated in place unless the store
already has a narrow migration helper. If omitted, active staff selection falls
back to account default staff and users can select again with /staff. No
runtime path should read legacy_active_staff after this change.

## Error Handling

All user-visible errors should use the new words:

- staff ID is empty
- staff ID contains invalid characters
- staff "x" not found
- Runner.observe requires an argument
- unknown Staff method: x
- unknown Runner method: x

Internal logs should use "staff" for identity and "runner" for execution.

## Testing

Focused tests should cover:

- staff directory loading, default staff creation, presets, dirty detection, and
  invalid StaffID validation.
- account setup and account migration from profiles/ to staff/.
- staff_meta migration and store CRUD methods.
- Staff.switch and active_staff resolution.
- Staff.create creates metadata and returns staff terminology.
- Runner.observe sandbox interruption and prompt follow-up text.
- Runner.delegate passes a StaffID target.
- /staff command behavior and help output.
- /api/v1/staff routes and client methods.
- Kanban tool docs/tests after vocabulary updates.

Final verification should include:

```sh
go test ./core ./store ./sandbox ./engine ./server ./client ./cli -count=1
go test ./... -short -count=1
git diff --check
```
