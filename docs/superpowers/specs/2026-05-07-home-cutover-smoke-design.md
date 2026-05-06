# Home Cutover Smoke Design

Date: 2026-05-07
Status: Approved for implementation planning

## Purpose

Add a credentialed Home smoke command that verifies the production cutover path
which credential-free smoke cannot cover: a daemon connects to Home with a
Portal-issued device token, a user token discovers the route, and a chat
completion is relayed through that daemon.

This is the next smallest Home cutover gap after service implementation, BFF
local smoke, and deploy automation. It does not run automatically without
operator-provided credentials and it does not stop or modify `apps/chat`.

## Scope

Create an operator-invoked smoke command:

```text
kittyhome-cutover-smoke
```

The command reads:

```text
HOME_BASE_URL
HOME_USER_TOKEN
HOME_DEVICE_TOKEN
HOME_DEVICE_ID
HOME_LOCAL_ACCOUNT_ID
```

Optional:

```text
HOME_SMOKE_TIMEOUT
HOME_SMOKE_USER_ID
```

`HOME_BASE_URL` is an HTTP(S) Home origin such as
`https://home.kittypaw.app`. The command derives
`ws://.../daemon/connect` or `wss://.../daemon/connect` from it.

## Behavior

1. Start a fake daemon WebSocket client using `HOME_DEVICE_TOKEN`.
2. Send a Home relay `hello` frame with the configured device id, local account
   id, protocol version, and `openai.chat_completions` capability.
3. Poll `GET /v1/routes` with `HOME_USER_TOKEN` until the configured
   device/account route appears.
4. Send `POST /nodes/{device}/accounts/{account}/v1/chat/completions` with
   `HOME_USER_TOKEN`.
5. The fake daemon validates the relayed request and returns an SSE chat
   response containing `hello from cutover smoke`.
6. The smoke command verifies the caller receives the SSE content and prints
   progress lines.

Expected progress:

```text
ok daemon connected
ok route discovery <device>/<account>
ok chat completion relayed
```

## Architecture

`apps/home/internal/smoke/remote.go` owns the reusable remote smoke runner. It
has a small config loader for environment variables and a `RunRemote` function
for tests and the CLI. It reuses the existing Home protocol package and mirrors
the local smoke fake daemon behavior, but accepts real Home base URL and bearer
tokens.

`apps/home/cmd/kittyhome-cutover-smoke/main.go` is only a thin CLI wrapper. It
loads env config, applies timeout, calls `RunRemote`, prints progress to stdout,
and reports failures to stderr.

The existing `deploy/smoke.sh` remains credential-free. The new command is
documented as a manual post-deploy cutover check because it requires production
tokens.

## Error Handling

- Missing required env returns a clear config error before network activity.
- Invalid Home URL returns a config error.
- Daemon WebSocket connect failure includes the derived WebSocket URL.
- Route polling times out with the last route error when available.
- Relay request validation failures close the smoke with an explicit error.
- Chat completion failure includes HTTP status and response body.

## Testing

- Unit tests cover env config validation and URL-to-WebSocket derivation.
- An in-process Home router test seeds static user/device credentials, runs
  `RunRemote`, and verifies the full daemon route discovery and chat relay path.
- CLI package compiles as part of `go test ./...`.
- `make smoke-cutover` points at the new command; without env it should fail
  fast with the missing env message.

## Out Of Scope

- Browser OAuth automation.
- Creating Portal tokens.
- Production deployment execution.
- `apps/chat` decommission or redirect behavior.
- Kanban Home smoke.
