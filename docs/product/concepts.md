# KittyPaw Product Concepts

This document is the product vocabulary for KittyPaw. When a feature adds,
removes, or changes a user-visible concept, update this file and
`docs/product/surfaces.md` in the same change.

## Reading Rules

- **Concept** means a durable product idea users can talk about, configure, or
  see in a UI.
- **Surface** means the way a user reaches a concept: CLI, onboarding, web UI,
  chat slash command, natural-language chat, or hosted Space.
- **Source of truth** is the state that decides reality. Assistant wording must
  not imply a concept exists until its source of truth exists.

## Concept Map

```text
KittyPaw install
  |
  +-- local daemon/server
        |
        +-- account
              |
              +-- config + secrets
              +-- local store
              +-- workspaces
              +-- staff
              +-- skills/packages
              +-- channels
              +-- models
              +-- memory/history
              +-- projects
                    |
                    +-- boards
                    +-- milestones
                    +-- kanban tasks
                          |
                          +-- runs
                          +-- comments
                          +-- links
```

## Core Concepts

| Concept | What It Means | Source Of Truth | Primary Owner |
| --- | --- | --- | --- |
| Install | One local KittyPaw installation on a machine. | `$KITTYPAW_CONFIG_DIR` or `~/.kittypaw`; installed binary/service files. | `apps/kittypaw` |
| Local daemon/server | Long-running local process that serves web UI, APIs, channels, scheduler, and hosted relay connector. | Running process plus `server.toml` and per-account config. | `apps/kittypaw` |
| Account | An isolated personal or team-space profile. Owns config, secrets, DB, skills, staff, channels, and workspaces. | `~/.kittypaw/accounts/<id>/` and that account's DB. | `apps/kittypaw` |
| User | Human identity authenticated through hosted portal for cloud/relay services. | Portal OAuth user and local login token. | `apps/portal`, `apps/kittypaw` |
| Device | A local daemon identity registered for hosted relay access. | Portal device credential and relay pairing state. | `apps/portal`, `apps/chat`, `apps/space` |
| Model | A named LLM backend configuration usable by chat and runner execution. | Account `config.toml` model list and active runtime override. | `apps/kittypaw` |
| Workspace | A local filesystem root indexed and exposed to file tools. | Account `[workspace]` config and workspace DB/cache. | `apps/kittypaw` |
| Skill | Installed or generated automation callable by the runner. | Skill files/packages under the account directory plus package metadata. | `apps/kittypaw` |
| Tool global | Built-in JS API exposed to the runner, such as `Gmail`, `X`, `Kanban`, `Staff`, `Browser`. | `core.SkillRegistry` and executor dispatch. | `apps/kittypaw` |
| Channel | Messaging adapter for inbound/outbound user interaction. | Account channel config plus runtime channel connector. | `apps/kittypaw`; Kakao gateway in `apps/kakao` |
| Conversation | The account runner history and per-conversation context such as active staff overrides. | Account store conversation/user-context tables. | `apps/kittypaw` |
| Staff | A named assistant identity or role with metadata and `SOUL.md`. | `staff_meta` row plus `staff/<id>/SOUL.md`; aliases in store. | `apps/kittypaw` |
| Memory | Stored user preferences, execution memory, and searchable conversation/file context. | Account store and workspace index. | `apps/kittypaw` |
| Project | A local Kanban workstream with a slug, name, and root workspace/path. | Kanban project rows in account store. | `apps/kittypaw` |
| Board | A project flow view for tasks. | Kanban board rows in account store. | `apps/kittypaw` |
| Milestone | A project goal, release, or delivery scope. | Kanban milestone rows in account store. | `apps/kittypaw` |
| Kanban task | A durable task with status, assignee, priority, comments, and runs. | Kanban task rows in account store. | `apps/kittypaw` |
| Run | A concrete execution attempt against a Kanban task. | Kanban run rows and heartbeat/outcome fields. | `apps/kittypaw` |
| Team Space | A shared/coordinator account that can read allowed member data and fan out messages. | Account `is_shared` and `team_space.members` config. | `apps/kittypaw` |
| Hosted Space | Remote web surface under `space.kittypaw.app` that talks to a local daemon through hosted relay. | Hosted app deployment plus daemon relay connection. | `apps/space`, `apps/kittypaw` |
| Portal | Hosted auth, users, devices, OAuth connections, discovery, and admin. | Portal database and environment. | `apps/portal` |
| External connection | OAuth/service account connection such as Gmail or X. | Portal/connect token store plus local credential bootstrap. | `apps/portal`, `apps/kittypaw` |

## Concept Boundaries

### Account

An account is the strongest local boundary. Features that touch secrets,
channels, staff, skills, workspaces, memory, or Kanban data must be explicit
about which account they operate on.

Rules:

- One account must not read another account's DB directly.
- Cross-account reads go through explicit sharing APIs, such as `Share.read`.
- Cross-account push goes through explicit fanout APIs, such as `Fanout.send`.
- CLI commands should accept or resolve an account before mutating account
  state.

### Workspace

A workspace is not the same thing as a Kanban project. A workspace is a local
filesystem root used by file search, file read/write, indexing, and summary
tools. A Kanban project may point at a workspace/root, but it also has boards,
milestones, tasks, runs, and comments.

Rules:

- Users should not be asked to type arbitrary absolute paths in primary web UI
  flows when a safe workspace picker is available.
- Chat tools should treat relative file paths as workspace-relative.
- A new workspace-changing feature must document whether it updates config,
  store state, index cache, or all three.

### Staff

A staff is real only when both durable metadata and identity material exist.

Required state:

- `staff_meta` row for the canonical ID.
- `staff/<id>/SOUL.md` for the actual identity.
- Optional display name and aliases for user-facing resolution.

Rules:

- Natural-language staff creation uses a draft and approval flow.
- `/staff use <id>` should resolve only real active staff.
- Missing `SOUL.md` fallback is allowed for normal default execution, but it
  must not make an arbitrary ID appear to be a real staff.
- `paw` may be a user-facing alias, but should be explicit, not an accidental
  fallback.

### Skill

A skill is user-installable or user-generated automation. It differs from a
tool global:

- A **tool global** is built into KittyPaw, such as `Gmail.search` or
  `Kanban.create`.
- A **skill/package** is installed, searched, configured, enabled, disabled, and
  run by ID.

Rules:

- CLI can expose precise lifecycle commands.
- Chat can expose a natural-language flow, but install from registry requires
  explicit user consent.
- Scheduled skills need a delivery channel that actually supports proactive
  outbound messages.

### Channel

Channels are not equivalent. Each channel has different delivery capabilities.

| Channel | Inbound | Reply To Current Message | Proactive Outbound |
| --- | --- | --- | --- |
| Web chat | Yes | Yes | Connection-bound only |
| Telegram | Yes | Yes | Yes, with bot token and chat ID |
| KakaoTalk OpenBuilder | Yes | Yes, through callback action ID | No through current callback relay |
| Slack | Configured support | Configured support | Depends on configured token/channel |
| Discord | Configured support | Configured support | Depends on configured token/channel |

KakaoTalk proactive sending is a separate product question. It should be
evaluated through Kakao BizMessage, FriendTalk, Brand Message, or channel
message APIs, not by stretching the current OpenBuilder callback relay.

### Project And Kanban

A project is the durable workstream. Kanban tasks are the actionable units
inside a project.

Rules:

- `project` commands manage project-level lifecycle.
- `/project use <key>` selects the current project for the active conversation;
  `/tickets` follows that selection unless a project key is supplied.
- `/ticket chat <key>` is advisory and returns the ticket conversation ID. It
  does not switch the current chat implicitly.
- `kanban` commands manage task/run lifecycle.
- Web Kanban should be a direct Kanban surface, not a tab inside Settings.
- Chat can create/update Kanban tasks through the `Kanban` tool global when the
  user asks for work tracking.

### Hosted Space

Hosted Space is a remote surface, not a second source of truth. It should route
to the local daemon through authenticated relay or local API calls. Do not mark
a local web surface as available in hosted Space until the hosted route and
relay/API path are implemented and documented.
