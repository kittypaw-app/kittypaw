# KittyPaw Surfaces

This document says where each concept is exposed. It is the checklist for
whether a concept belongs in CLI, onboarding, web control, web chat, web Kanban,
in-chat commands, natural-language chat, or hosted Space.

## Surface Types

| Surface | User Shape | Best For | Avoid |
| --- | --- | --- | --- |
| CLI | Exact commands such as `kittypaw skill install`. | Precise setup, scripting, diagnostics, destructive operations with flags. | Ambiguous natural language. |
| Setup/onboarding | Guided first-run or reconfiguration flow. | Initial account, model, channel, workspace, auth setup. | Advanced daily operations. |
| Web control | Local `/_settings` control UI. | Account configuration, workspace browsing, setup repair, admin-like local controls. | Chat conversation or Kanban task work. |
| Web chat | Local `/chat` UI. | Interactive assistant conversation against the local daemon. | Settings-heavy flows. |
| Web Kanban | Local `/kanban` UI. | Project/task/board/milestone work. | Generic app settings. |
| In-chat slash | Deterministic commands inside chat, such as `/staff list`. | Small state reads/writes that should not depend on LLM interpretation. | Long forms and high-risk hidden mutations. |
| Natural-language chat | User asks normally; assistant may call tools. | High-level intents, discovery, creation drafts, "install/run this" flows. | Silent irreversible state changes. |
| Hosted Space | Remote web app, currently through authenticated hosted relay where supported. | Remote access to local chat/control surfaces. | Assuming every local API is hosted automatically. |

Slash command help is generated from the command registry. Unknown slash
commands return a deterministic error with `/help` guidance and never fall
through to the LLM. Read-only diagnostics such as `/help`, `/status`,
`/model` without an ID, `/session`, and `/context` should not write
conversation history; auditable command results such as `/run`, `/model <id>`,
`/project use`, and ticket state changes may be recorded in the active
conversation.

## Exposure Matrix

Legend:

- **Primary**: preferred user path.
- **Supported**: available but not the main path.
- **Indirect**: available through another concept or tool.
- **No**: should not be exposed there yet.

| Concept | CLI | Setup | Web Control | Web Chat | Web Kanban | Slash | Natural Chat | Hosted Space |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Account | Primary | Primary | Supported | Indirect | Indirect | No | Limited | Indirect |
| Login/User auth | Primary | Primary | Supported | Indirect | Indirect | No | No | Primary |
| Device/relay | Primary | Supported | Supported | Indirect | Indirect | No | No | Primary |
| Server/daemon | Primary | Supported | Supported | Indirect | Indirect | No | Limited | Indirect |
| Model | Primary | Primary | Supported | Supported | No | Primary (`/model`) | Limited | Supported via relay |
| Workspace | Supported | Primary | Primary | Indirect | Supported picker | No | Indirect via `File` | Supported when routed |
| Skill/package | Primary | Supported | Supported | Indirect | No | Primary (`/skills`, `/run`, `/teach`) | Primary with consent | Supported via relay |
| Tool global | No direct | No | No | Indirect | Indirect | No | Primary | Supported via relay |
| Staff | Future CLI/admin | No | Future | Indirect | No | Primary (`/staff`) | Primary draft flow | Supported via relay |
| Channel | Primary | Primary | Supported | Indirect | No | No | Limited | Indirect |
| Conversation/history | Primary | No | No | Primary | No | Supported (`/status`, `/session`, `/context`) | Primary | Supported via relay |
| Memory | Primary search | No | Future | Indirect | No | No | Primary through `Memory` | Supported via relay |
| Project | Primary | No | No | Indirect | Primary | Supported (`/projects`, `/project`) | Supported via `Kanban` | Local unless hosted route exists |
| Board | Supported | No | No | Indirect | Primary | No | Supported via `Kanban` | Local unless hosted route exists |
| Milestone | Primary | No | No | Indirect | Primary | No | Supported via `Kanban` | Local unless hosted route exists |
| Kanban task | Primary | No | No | Indirect | Primary | Supported (`/tickets`, `/ticket`) | Primary through `Kanban` | Local unless hosted route exists |
| Run | Primary | No | No | Indirect | Primary | Supported (`/ticket job`) | Supported through `Kanban` | Local unless hosted route exists |
| Team Space | Config/admin | No | Future | Indirect | No | No | Supported through `Share`/`Fanout` | Future |
| Gmail connection | Primary | Supported | Future | Indirect | No | No | Primary read-only tool | Supported via relay |
| X connection | Primary | Supported | Future | Indirect | No | No | Primary read-only tool | Supported via relay |
| Browser/CDP | Config/admin | No | Future | Indirect | No | No | Primary when enabled | Local daemon only unless routed |

## Concept-Specific Surface Policy

### Account

CLI is the primary account management surface:

- `kittypaw account add <name>`
- `kittypaw account remove <name>`
- account selection flags where commands support them

Web control may show account-local configuration. Natural-language chat should
not silently create, delete, or switch accounts.

### Model

CLI owns durable model configuration:

- `kittypaw model add`
- `kittypaw model list`
- `kittypaw model remove`

In-chat `/model` changes the active model for the current chat session only.
The assistant may explain model state, but should not mutate durable model
config through natural-language chat unless a deterministic flow exists.

### Workspace

Setup/onboarding and Web control own workspace configuration. Chat accesses
workspace content through `File.*`, `Memory.search`, and summary/index tools.

Required UX:

- Web workspace selection should browse directories instead of asking users to
  type absolute paths.
- Kanban project creation should select from registered workspaces/root choices
  when possible.

### Skill

CLI owns precise lifecycle:

- `kittypaw skill list`
- `kittypaw skill search`
- `kittypaw skill install`
- `kittypaw skill uninstall`
- `kittypaw skill create`
- `kittypaw skill run`
- `kittypaw skill config`

Chat owns intent-driven usage:

- `/skills` lists installed skills.
- `/run <name>` runs an installed skill.
- `/teach <description>` creates a safe skill from chat.
- Natural-language requests may search the registry, suggest a skill, ask for
  explicit install consent, then install and run it.

Rule: registry installation must not happen before the user explicitly agrees
in chat.

### Staff

Slash commands are the deterministic in-chat control surface:

```text
/staff
/staff current
/staff list
/staff show <id>
/staff use <id>
/staff hire <role>
/staff cancel
```

Natural-language staff creation is a draft flow:

```text
User: 개발PM 한 명 만들어줘
Assistant: KittyPaw Staff 기능으로 새 역할을 만들까요?
User: 응
Assistant: shows draft with canonical ID, display name, aliases, SOUL.md
User: 좋아
System: persists staff_meta + staff/<id>/SOUL.md
Assistant: asks whether to use this staff in current conversation
```

Rule: the assistant may only say a staff was created after durable metadata and
`SOUL.md` exist.

### Project And Kanban

Web Kanban remains the primary visual surface. Slash commands provide compact
chat controls:

```text
/projects
/project current
/project show <key>
/project use <key>
/tickets [project-key]
/ticket show <key>
/ticket chat <key>
/ticket job <key>
/ticket move <key> <status>
/ticket block <key> <reason>
/ticket done <key>
```

`/project use <key>` stores the selected project for the current conversation,
so subsequent `/tickets` uses that project unless a project key is supplied.
`/ticket chat <key>` is advisory: it returns the ticket conversation ID but does
not switch the current chat by itself.

### Channel

CLI and setup own channel configuration. Chat may use channels according to
their delivery capability.

Policy:

- Telegram can support proactive scheduled delivery.
- KakaoTalk OpenBuilder can answer the current user-triggered callback, but the
  current relay is not a proactive outbound channel.
- Scheduled skills must pick only channels with proactive outbound support.
- If a user asks for Kakao proactive sending, treat it as a separate
  BizMessage/FriendTalk/Brand Message review item.

### Project And Kanban

CLI owns scriptable project and Kanban operations:

- `kittypaw project create/list/show`
- `kittypaw project board list`
- `kittypaw project milestone create/list`
- `kittypaw kanban create/list/show/edit/archive`
- `kittypaw kanban claim/heartbeat/complete/cancel/reclaim`
- `kittypaw kanban block/unblock/comment/link/runs/dispatch`

Web Kanban owns visual task work. Chat may call `Kanban.*` for user intents
such as "이걸 태스크로 만들어줘" or "blocked 처리해줘".

Rule: Kanban should not be presented as a Settings tab. It is a direct work
surface.

### Hosted Space

Hosted Space should mirror local concepts only after these are true:

1. The hosted route exists.
2. Auth/session behavior is documented.
3. The relay or API path supports the required operation.
4. Smoke tests cover the route.

Local `/kanban` being available does not by itself mean
`https://space.kittypaw.app/kanban` is available.

## Adding A New In-Chat Feature

Before adding a new in-chat feature, decide which path it belongs to:

| Path | Use When | Example |
| --- | --- | --- |
| Slash command | Deterministic, short, stateful, should not rely on LLM. | `/staff list`, `/model main` |
| Natural-language intent | User phrasing matters and a guided confirmation is useful. | "개발PM 만들어줘" |
| Tool global | The LLM needs a programmatic primitive during reasoning. | `Kanban.create`, `Gmail.search` |
| Skill/package | User-installable reusable behavior. | `weather-now` |
| Web UI | User needs browsing, comparison, forms, or visual state. | Kanban board, workspace picker |
| CLI | Needs scripting, diagnostics, flags, or admin precision. | `kittypaw connect x` |

If a feature creates durable state, it must have at least one deterministic
path, a documented confirmation policy, and tests covering false success.
