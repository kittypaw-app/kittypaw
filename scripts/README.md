# Scripts

Repository-level helper scripts live here.

Service-specific scripts should stay inside their service directory unless they
coordinate multiple services.

## `smoke-local.sh`

Runs the repeatable local cross-service smoke used after monorepo contract or
boundary changes:

```bash
make smoke-local
```

It validates contracts, checks deploy script syntax, runs deterministic
Kittypaw runner/channel critical flows early, runs Go/Rust package tests, and
runs the Chat in-process e2e smoke. It intentionally does not run production
endpoint smoke scripts, DB-backed integration tests, or LLM-judged evals.

The early Kittypaw flow checks are deterministic engine/channel regressions,
not CLI workflow tests. They cover chat-driven skill install/run, staff
mention routing, in-chat `/staff`, reflection over `conversation_turns`,
staff evolution pending proposals, and captured-shape Telegram/Kakao channel
fixtures.

## `e2e-local.sh`

Runs the Docker-backed local auth/chat E2E:

```bash
make e2e-local
```

It starts a disposable PostgreSQL container, migrates Portal's schema from the
Go harness, starts real Portal and Chat binaries, uses a fake Google OAuth
server, connects Kittypaw chat relay connectors, and verifies the Chat BFF
session can reach `/app/api/*` without a browser `Authorization` header.

The harness also runs a heavier local runner flow: browser chat -> Chat relay ->
real Kittypaw dispatcher -> fake skill registry. It verifies that a user can ask
for exchange rates, receive an install offer, approve it, and get the installed
skill result back through the Chat BFF. It also verifies an installed
exchange-rate follow-up with KRW conversion, a Gangnam Station weather install
using fake KittyAPI geo resolution, and installed weather skill reuse.

Set `KITTY_E2E_KEEP_DB=1` to keep the database container after the run. Set
`KITTY_E2E_SKIP_COMPOSE=1` and provide `DATABASE_URL` to use an already-running
test database.
