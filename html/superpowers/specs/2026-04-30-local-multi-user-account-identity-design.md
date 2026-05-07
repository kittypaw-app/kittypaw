# Local Multi-User Account Identity Design

> Historical plan snapshot. This document records the implementation plan or design state at the time it was written; use repository README, ARCHITECTURE.md, and app README/DEPLOY docs for the current live shape.

## Goal

Make `~/.kittypaw/accounts/<id>/` a first-class human account from the first setup, instead of creating `accounts/default/` and treating account selection as a later concern.

This is a prerequisite for hosted relay access. `chat.kittypaw.app` can only route safely if the local server already knows which local account is being accessed and if the Web UI/API surface is scoped to that account.

## Current State

The repository has already renamed tenant concepts to account. The current `account` model is mostly a runtime/data isolation boundary:

- `core.Account` maps to `~/.kittypaw/accounts/<id>/`.
- `AccountRouter` owns one `engine.Session` per account.
- Channels are keyed by account ID.
- `kittypaw account add <name>` creates additional account directories.

The remaining weakness is that several user-facing flows still assume `default`:

- Fresh `kittypaw setup` writes `~/.kittypaw/accounts/default/config.toml`.
- `core.ConfigPath()` always points at `accounts/default/config.toml`.
- CLI login/setup/Kakao secret flows use `core.DefaultAccountID`.
- Web bootstrap returns the account API key after localhost bootstrap, not a user session.
- HTTP handlers are still bound to the default account session in `server.New`.

So `account` exists, but local login/user identity does not.

## Decision

Fresh installs must create a named local account:

```text
~/.kittypaw/
  auth.json
  server.toml
  accounts/
    alice/
      config.toml
      secrets.json
      data/kittypaw.db
      skills/
      staff/
      packages/
```

`default` remains only as a legacy migration output and test fixture. New setup must not create it unless the user explicitly enters `default`.

## Local User Credentials

Local Web UI credentials are server-wide metadata stored outside account directories:

```text
~/.kittypaw/auth.json
```

This avoids a chicken-and-egg problem: the server must authenticate a user before selecting an account, so credentials cannot live only inside an account-scoped DB.

`auth.json` stores:

- user/account ID
- password hash
- password algorithm metadata
- created/updated timestamps
- disabled flag

It does not store plaintext passwords, LLM API keys, channel tokens, or cloud relay tokens.

Password hashing should use Argon2id via `golang.org/x/crypto/argon2` with per-user random salt. If dependency friction appears during implementation, bcrypt is an acceptable fallback, but plaintext, SHA-only, and unsalted hashes are not acceptable.

## Account ID Rules

The existing `core.ValidateAccountID` rule remains the canonical ID rule:

```text
^[a-z0-9_][a-z0-9_-]{0,31}$
```

This is intentionally stricter than display names. The account ID is a filesystem path component, route key, audit key, and future relay routing key.

If a richer display name is needed later, add a separate field. Do not loosen account IDs.

## Setup Flow

`kittypaw setup` asks for local account credentials before LLM/channel setup:

1. Account ID
2. Password
3. Password confirmation
4. Existing LLM/channel/workspace setup

Non-interactive setup adds:

```text
kittypaw setup --account alice --password-stdin ...
```

Avoid `--password` because it leaks through process listings and shell history.

If `~/.kittypaw/accounts/` already contains accounts, bare `kittypaw setup` must not silently modify an arbitrary account. It should require either:

```text
kittypaw setup --account alice
```

or an interactive account selection.

## Account Add Flow

`kittypaw account add <id>` must also create a local login credential:

```text
kittypaw account add bob --password-stdin ...
```

In interactive mode, prompt for password and confirmation. Account directory creation and auth-store creation must be transactional:

- If account directory creation fails, do not write auth.
- If auth write fails, remove the staged account directory before commit.
- If hot activation fails after the account is committed, keep the account and auth entry, and print the existing restart guidance.

## Web UI Login

The local Web UI becomes session-based:

- `POST /api/auth/login`
- `POST /api/auth/logout`
- `GET /api/auth/me`

Authenticated browser sessions use `HttpOnly`, `Secure` when TLS is present, and `SameSite=Lax` cookies. The session contains the local account ID and expires.

After login, every Web UI API request is routed to the logged-in account. This is the important behavioral change: two local users opening the same server Web UI must not share the default session, default DB, or default config.

## HTTP And WebSocket Routing

The server already has `AccountRouter`. HTTP and WebSocket handlers need to stop assuming `s.session`.

Add a request context value:

```go
type requestAccount struct {
    ID      string
    Account *core.Account
    Session *engine.Session
    Deps    *AccountDeps
}
```

Middleware resolves it from the authenticated browser session or API key. Handlers that operate on account state use the request account. Server-wide admin routes stay separate.

For compatibility, CLI/API requests with the legacy per-account API key may keep working, but if more than one account exists and no account is specified, the server must reject the request instead of falling back to `default`.

## CLI Account Selection

CLI commands need a consistent active-account rule:

1. `--account <id>` flag
2. `KITTYPAW_ACCOUNT`
3. If exactly one account exists, use it
4. If multiple accounts exist, fail with a list and ask for `--account`

Do not introduce a new implicit `default` preference.

## Relay Implications

Hosted relay pairing must pair a cloud user to a specific local account on a specific device:

```text
cloud_user_id -> device_id -> local_account_id
```

The local password is not sent to the cloud. Pairing proves local control through a local authenticated session or CLI credential prompt.

OpenAI-compatible API routing later becomes:

```text
cloud API key -> cloud user -> device -> local account -> local OpenAI-compatible handler
```

## Migration

Existing installs keep working:

- Legacy root layout still migrates to `accounts/default/`.
- Existing `accounts/default/` stays valid.
- On first run after this change, if `auth.json` is missing and exactly one account exists, the user is prompted to create credentials for that account.
- If multiple accounts exist and `auth.json` is missing, the user must create credentials account-by-account.

Fresh installs do not create `default`.

## Non-Goals

- Do not implement cloud relay in this phase.
- Do not expose generic localhost proxying.
- Do not support account ID rename in this phase.
- Do not merge local account credentials with hosted `chat.kittypaw.app` credentials.

## Open Risk

The biggest implementation risk is HTTP handler scope. Many handlers currently touch `s.store`, `s.session`, `s.pkgManager`, and `s.config`. Those are default-account fields. The implementation must either convert handlers incrementally behind request-scoped accessors or it will accidentally preserve cross-user leakage.
