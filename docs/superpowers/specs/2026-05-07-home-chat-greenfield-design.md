# Home Chat Greenfield Design

Date: 2026-05-07
Status: Approved for implementation planning

## Purpose

Build the hosted chat surface as if `apps/chat` did not already exist, using
`home.kittypaw.app/chat` as the product-facing entry point. The first slice is
chat only. Kanban remains out of scope until its local implementation and
contracts settle.

The target result is a new `apps/home` service that can eventually replace
`apps/chat`. Existing `apps/chat` code and deployment stay intact during the
transition. Cutover happens only after portal discovery, token audiences, daemon
connectors, production smoke, and client fallback paths are all verified.

## Product Model

`home.kittypaw.app` is the remote Home for a user's local KittyPaw daemon. It is
not a generic localhost tunnel. It exposes first-party surfaces backed by narrow
capability operations over an outbound daemon WebSocket.

Initial surface:

```text
https://home.kittypaw.app/chat
```

Future surface, not in this slice:

```text
https://home.kittypaw.app/kanban
```

## Service Boundaries

Add a new deployable app:

```text
apps/home
```

`apps/home` owns:

- Home web shell and hosted chat UI at `/chat`.
- Browser BFF session handling for Home.
- Daemon outbound WebSocket endpoint at `/daemon/connect`.
- Authenticated route discovery at `/v1/routes`.
- OpenAI-compatible chat relay endpoints.
- Verification of Portal-issued Home resource tokens.

`apps/home` must not import `apps/chat/internal/*`. During migration it may copy
or adapt proven patterns, but app boundaries stay explicit. Shared wire shapes
belong in `contracts/`; shared runtime packages should not be introduced just
because code looks similar.

Existing ownership remains:

- `apps/portal`: OAuth, users, refresh tokens, devices, discovery, JWKS, token
  issuance.
- `apps/kittypaw`: local daemon, local `/chat`, local store, engine, and daemon
  outbound connector.
- `apps/chat`: legacy hosted chat service until cutover is complete.

## Routes

Home service routes:

```text
GET  /health
GET  /
GET  /chat
GET  /chat/

GET  /auth/login/google
GET  /auth/callback
POST /auth/logout

GET  /chat/api/session
GET  /chat/api/routes
GET  /chat/api/nodes/*
POST /chat/api/nodes/*

GET  /daemon/connect
GET  /v1/routes
GET  /nodes/{device_id}/accounts/{account_id}/v1/models
POST /nodes/{device_id}/accounts/{account_id}/v1/chat/completions
```

`/chat` redirects to `/chat/`. The browser app calls only `/chat/api/*`; the BFF
injects server-held access tokens when proxying to the internal relay handler.

The raw relay/API routes remain available for OpenAI-compatible clients and for
daemon route discovery. They are authenticated with bearer credentials and are
not browser-session endpoints.

## Auth And Audience

Greenfield canonical resource audience:

```text
https://home.kittypaw.app
```

Chat capability scopes remain capability-oriented:

```text
chat:relay
models:read
daemon:connect
```

Migration policy:

- Portal adds `AudienceHome = "https://home.kittypaw.app"`.
- During migration, user/API tokens include `api`, `chat`, and `home`
  audiences.
- During migration, daemon device tokens include both `chat` and `home`
  audiences.
- `apps/home` verifies the Home audience.
- `apps/chat` continues verifying the Chat audience.
- After `apps/chat` is decommissioned, Portal removes the Chat audience from new
  credentials in a separate compatibility cleanup.

This lets existing daemons refresh credentials after a Home connection receives
401, while avoiding a permanent Home verifier that accepts Chat-only tokens.

Portal web OAuth must allow:

```text
https://home.kittypaw.app/auth/callback
```

The existing server-to-server `/auth/web/exchange` flow remains the right model:
the browser never receives refresh tokens.

## Discovery Contract

Add an optional discovery key:

```json
{
  "home_base_url": "https://home.kittypaw.app"
}
```

During migration Portal returns both:

```json
{
  "home_base_url": "https://home.kittypaw.app",
  "chat_relay_url": "https://chat.kittypaw.app"
}
```

`apps/kittypaw` consumes `home_base_url` first for the hosted relay base URL. If
it is absent, it falls back to `chat_relay_url`. Empty optional URLs continue to
delete stale persisted service URLs.

Changing `contracts/discovery/` requires the contract checklist:

1. Update schema or enum source.
2. Update at least one example fixture.
3. Add or update Portal producer tests.
4. Add or update `apps/kittypaw` consumer tests.
5. Run `make contracts-check` from the repository root.

## Daemon Relay Protocol

The first Home protocol version keeps the existing chat operation shape:

```text
openai.models
openai.chat_completions
```

The WebSocket remains operation-based:

```text
hello -> request -> response_headers -> response_chunk* -> response_end
```

The daemon advertises capabilities in `hello.capabilities`. Home routes only to
online device/account pairs whose advertised capabilities satisfy the requested
operation.

Kanban will add distinct `kanban.*` operations later. Home must not proxy
arbitrary local HTTP paths.

## User Flow

Hosted browser chat:

1. User opens `https://home.kittypaw.app/chat`.
2. Home redirects unauthenticated users through Portal web OAuth.
3. Portal redirects back to `https://home.kittypaw.app/auth/callback` with a
   one-time code and state.
4. Home exchanges the code server-to-server and stores access/refresh tokens in
   an HttpOnly session.
5. Browser calls `/chat/api/routes`.
6. Home injects the access token and calls its relay handler.
7. The selected online daemon/account receives `openai.chat_completions`.

Daemon connection:

1. Local KittyPaw resolves discovery from Portal.
2. It chooses `home_base_url` when present, otherwise `chat_relay_url`.
3. It opens `wss://home.kittypaw.app/daemon/connect`.
4. It sends `hello` with device id, local accounts, daemon version, protocol
   version, and capabilities.
5. Home registers route entries scoped by verified `user_id`, `device_id`, and
   advertised local account ids.

## Error Handling

- Missing or invalid browser session: `/chat/api/*` returns 401.
- Refresh failure: Home deletes the session cookie and returns 401.
- Missing bearer token on raw relay routes: 401.
- Valid token with insufficient scope: 403.
- Unknown or offline device/account: 404 or 503, matching the relay handler's
  existing semantics.
- Unsupported operation or unadvertised capability: structured relay error.
- WebSocket protocol mismatch: reject connection or close with a clear protocol
  error before registering routes.
- Oversized request frames and proxy bodies are capped.

## Deployment

Add deployment assets parallel to existing hosted services:

```text
apps/home/deploy/env.example
apps/home/deploy/kittyhome.nginx
apps/home/deploy/kittyhome.service
apps/home/deploy/smoke.sh
deploy/home/README.md
```

Primary environment variables:

```text
KITTYHOME_PUBLIC_BASE_URL=https://home.kittypaw.app
KITTYHOME_API_AUTH_BASE_URL=https://portal.kittypaw.app/auth
KITTYHOME_JWKS_URL=https://portal.kittypaw.app/.well-known/jwks.json
KITTYHOME_BIND_ADDR=unix:/run/kittyhome/kittyhome.sock
```

Release tags should use a namespaced prefix:

```text
home/vX.Y.Z
```

The root deployment docs and agent guide should be updated when the service is
introduced.

## Testing

Home unit and integration tests:

- Config defaults and env overrides.
- Health route build identity.
- Router mounts for `/chat`, `/chat/`, `/chat/api/*`, `/daemon/connect`,
  `/v1/routes`, and OpenAI-compatible relay routes.
- Credential verifier accepts Home audience and rejects API-only or Chat-only
  tokens.
- BFF OAuth login, callback, token exchange, refresh, logout, and session
  expiry.
- Route discovery filters by verified user, device, account, and capability.
- Daemon WebSocket hello validation and request/response streaming.
- Local smoke with fake daemon and Home relay.

Cross-app tests:

- Portal discovery includes `home_base_url`.
- Portal web redirect allowlist accepts Home callback and still rejects unknown
  redirect URIs.
- Portal-issued user and device credentials include Home audience during
  migration.
- Kittypaw discovery consumer prefers `home_base_url` and falls back to
  `chat_relay_url`.
- Kittypaw daemon connector builds `wss://home.kittypaw.app/daemon/connect` from
  the Home base URL.

## Cutover And Chat Decommission

`apps/chat` can be stopped only after all of these are true:

- `home.kittypaw.app/health` passes production smoke.
- `home.kittypaw.app/chat` login and chat completion smoke passes.
- A local daemon connects to `home.kittypaw.app/daemon/connect` using a Portal
  device credential with Home audience.
- Portal discovery returns `home_base_url` in production.
- Current Kittypaw clients prefer `home_base_url`.
- Existing clients without `home_base_url` support have either upgraded or are
  explicitly unsupported.
- OpenAI-compatible client documentation points to Home.
- Monitoring/logs show no meaningful `chat.kittypaw.app` relay traffic for the
  agreed compatibility window.

After decommission:

- Stop the `apps/chat` systemd service.
- Remove or freeze `chat.kittypaw.app` relay routes.
- Keep a web redirect from `https://chat.kittypaw.app/` to
  `https://home.kittypaw.app/chat` only if operationally useful.
- Remove Chat audience issuance in a later cleanup release.
- Update docs, deployment notes, release tags, and AGENTS app ownership.

## Implementation Phases

1. Add this design and an implementation plan.
2. Scaffold `apps/home` with health, config, router, Makefile, and deploy files.
3. Add Home identity verifier with Home audience tests.
4. Add daemon broker, WebSocket handler, route discovery, and OpenAI relay.
5. Add `/chat` static app and BFF session flow.
6. Update Portal discovery, redirect allowlist docs, and Home audience issuance.
7. Update Kittypaw discovery and relay connector preference.
8. Update contracts and run contract checks.
9. Deploy Home and run prod smoke.
10. Execute the chat decommission checklist after compatibility criteria pass.
