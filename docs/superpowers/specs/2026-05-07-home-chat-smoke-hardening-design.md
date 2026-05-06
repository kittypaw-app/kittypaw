# Home Chat Smoke Hardening Design

## Goal

Strengthen `apps/home` smoke coverage so the Home chat cutover cannot pass while
the browser-facing `/chat` BFF path is broken. The first Home slice already
validates the daemon relay core; this follow-up verifies the hosted browser
session path that users will actually exercise at `home.kittypaw.app/chat`.

## Scope

In scope:

- Local smoke covers the full BFF path: login callback, session cookie,
  `/chat/api/routes`, and `/chat/api/nodes/.../v1/chat/completions`.
- Local smoke still covers direct OpenAI-compatible relay routes.
- Production smoke checks unauthenticated browser-facing invariants that do not
  require credentials: health, chat HTML, JS assets, `/chat/api/session` 401,
  and Google login redirect shape.
- Docs state exactly what local and production smoke prove.

Out of scope:

- Persistent browser session storage.
- Real production OAuth completion.
- Kanban routes or UI.
- Removing `apps/chat`.

## Design

`internal/smoke` keeps using an in-process Home router and fake daemon. It adds a
fake Portal auth server and configures a Home `webapp.Handler` against it. The
smoke flow creates a pending PKCE login, follows the Home callback with a fake
OAuth code, captures the `kittyhome_session` cookie, then calls the BFF routes
with that cookie. The fake Portal exchange returns a static Home-audience API
token that the existing memory verifier accepts.

The fake daemon should answer both direct streaming requests and BFF JSON
requests. Streaming requests return SSE chunks as they do now. Non-streaming
requests return an OpenAI-compatible JSON body so the browser app path is
validated without depending on streaming parsing in the static JS.

Production smoke remains credential-free. It should fail when the deployed Home
surface lacks `/chat/`, the browser JS no longer points at `/chat/api`, the BFF
session endpoint does not reject anonymous callers, or Portal login redirect
wiring is missing.

## Testing

- Unit/local smoke tests assert BFF login and chat completion round-trip.
- `make smoke-local` exercises direct and BFF relay paths.
- `bash apps/home/deploy/smoke.sh` checks production-safe browser invariants.
- Full verification includes Home, Portal, Kittypaw targeted tests, contracts,
  build, local smoke, shell syntax, and `git diff --check`.
