# KittyPaw Architecture Map

This document is a product-level map. It intentionally stays above package
internals and focuses on where user-visible concepts flow.

## App Boundaries

```text
apps/kittypaw
  Local CLI, daemon, local web UI, engine, store, local channels, scheduler.

apps/portal
  Hosted OAuth, users, refresh tokens, devices, discovery, JWKS, admin.

apps/space
  Hosted web surface for remote access to local daemon through relay.

apps/chat
  Hosted relay protocol and OpenAI-compatible relay endpoints.

apps/kakao
  Kakao OpenBuilder webhook, callback dispatch, Kakao-specific pairing.

apps/kittyapi
  Hosted public data API and proxy endpoints.
```

Rule: app boundaries are ownership boundaries. Do not let one app read another
app's database directly.

## Local Runtime

```text
kittypaw CLI
    |
    | starts/configures
    v
local daemon/server
    |
    +-- local HTTP API
    |     +-- /api/v1/*
    |     +-- /api/settings/*
    |     +-- /api/chat/bootstrap
    |
    +-- local web UI
    |     +-- /_settings
    |     +-- /chat
    |     +-- /kanban
    |
    +-- account router
    |     +-- account: jinto
    |     +-- account: other
    |
    +-- channel connectors
    |     +-- telegram
    |     +-- kakao_talk
    |     +-- slack/discord when configured
    |
    +-- scheduler
    |
    +-- hosted relay connector
```

## Account Runtime

```text
account directory
    |
    +-- config.toml
    +-- secrets.json
    +-- data/kittypaw.db
    +-- skills/
    +-- staff/<id>/SOUL.md
    +-- packages/
    +-- workspace roots
```

The account is the default state boundary. A chat request, channel event, skill
execution, Kanban operation, or workspace read must resolve to an account
before touching local state.

## Chat Execution Flow

```text
User message
  |
  +-- Web /chat websocket
  +-- Telegram event
  +-- Kakao callback event
  +-- Hosted Space relay request
        |
        v
AccountRouter selects account/session
        |
        v
Session.Run
        |
        +-- deterministic slash command?
        |     |
        |     +-- yes: handle registry command or deterministic unknown-command error
        |     +-- no: continue only when the message is not a slash command
        |
        +-- deterministic pipeline intent?
        |     |
        |     +-- staff draft/approval/cancel/switch
        |     +-- other product intents
        |
        +-- LLM runner loop
              |
              +-- build prompt from SOUL.md + policy + tools + memory
              +-- generate JS
              +-- execute sandbox
              +-- resolve tool calls
              +-- return output through source channel
```

Important distinction:

- Slash commands and pipeline intents are product control paths.
- Unknown slash commands never fall through to the LLM; they return a `/help`
  hint from the deterministic command path.
- LLM tool calls are reasoning/execution paths.
- A user-visible durable mutation should not depend only on a free-form LLM
  claim.

## Staff Flow

```text
/staff list
  -> store.ListActiveStaff()
  -> display real staff rows

/staff use <id-or-alias>
  -> ResolveStaffID()
  -> GetStaffMeta()
  -> require active real staff
  -> set active_staff:<conversation>

"개발PM 만들어줘"
  -> staff creation intent
  -> ask whether to use KittyPaw Staff
  -> build pending draft
  -> show canonical ID + SOUL.md draft
  -> user approves
  -> write staff_meta + staff/<id>/SOUL.md + aliases
  -> ask whether to switch current conversation
```

Do not treat `LoadStaff(id)` fallback as proof that a staff exists. It is a
runtime fallback for normal assistant execution, not a creation primitive.

## Skill Flow

```text
CLI:
  kittypaw skill search/install/run/config
        |
        v
  account skill/package files + metadata

Chat:
  /skills, /run <name>, /teach <description>
        |
        v
  deterministic chat command path

Natural language:
  "환율 스킬 설치해줘"
        |
        v
  Skill.search -> ask consent -> Skill.installFromRegistry -> Skill.run
```

The registry install path must include explicit consent before installation.

## Workspace And File Flow

```text
setup or /_settings
  -> choose workspace directory
  -> update account workspace config/store
  -> refresh index/cache

chat tool call
  -> File.search/read/write/summary
  -> resolve path inside configured workspace unless user gave an absolute path
  -> enforce sandbox/path policy
```

Web UI should prefer directory browsing/pickers. Chat should prefer
workspace-relative paths.

## Kanban Flow

```text
CLI project commands
  -> project rows, boards, milestones

CLI kanban commands
  -> task rows, runs, comments, links

Local /kanban
  -> /api/v1/projects
  -> /api/v1/kanban/tasks
  -> direct visual task surface

Natural-language chat
  -> Kanban.create/show/claim/complete/block/comment/link/heartbeat
  -> same account store

Slash:
  /projects, /project current|show|use
  -> project rows + current_project:<conversation_id>

  /tickets, /ticket show|chat|job|move|block|done
  -> ticket rows, job plans, or ticket conversation_id advisory
```

Kanban is a work surface. It should not be embedded in Settings as a tab.

## Hosted Space Flow

```text
Browser at space.kittypaw.app
        |
        v
Hosted Space app
        |
        v
Portal auth/session
        |
        v
Hosted relay
        |
        v
local daemon relay connector
        |
        v
local account/session/API
```

Hosted Space is a remote access layer. It does not own account state. If a
local route is added, hosted Space needs its own route, auth, relay/API support,
and smoke coverage before the feature is considered hosted.

## Channel Delivery Flow

```text
Inbound message
  -> channel adapter
  -> account router
  -> Session.Run
  -> response through same channel when supported

Scheduled/proactive message
  -> scheduler or explicit send tool
  -> channel must support proactive outbound
```

Delivery capabilities differ:

- Telegram has stable `chat_id`, so proactive outbound is supported.
- Kakao OpenBuilder callback has an action/callback ID for the current request,
  not a stable proactive send target.
- Kakao proactive sending requires separate Kakao business/channel APIs.

## External Connection Flow

```text
kittypaw connect gmail/x
  -> hosted portal/connect OAuth flow
  -> token stored server-side
  -> local account can call broker/client tool
  -> usage/quota recorded server-side where applicable
```

The local daemon should not assume direct third-party API access when the
product policy says usage must be controlled through KittyPaw servers.
