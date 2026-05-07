# KittyPaw X Broker Quota Design

## Summary

X/Twitter usage must become centrally controllable because KittyPaw owns the X
developer app, API plan, and resulting cost exposure. The current X Connect flow
gates who may connect, but after OAuth it gives the local KittyPaw runtime the X
access and refresh tokens. That lets local tools call `api.x.com` directly, so
server-side monthly quotas and future billing cannot be enforced.

This design changes X only:

- Gmail remains local-token/direct because Gmail data is sensitive and should not
  pass through KittyPaw servers without a separate product/security decision.
- X OAuth tokens are stored server-side by `apps/portal`.
- Local KittyPaw stores only a connected marker and continues using the normal
  KittyPaw login token to call `connect.kittypaw.app`.
- `apps/portal` brokers X read endpoints, enforces entitlement and monthly post
  quotas, records usage, and calls `api.x.com`.

## Goals

- Make X usage enforceable before public or paid usage grows.
- Preserve the existing `kittypaw connect x` UX.
- Prevent local direct X API calls by not returning X provider tokens to the
  local machine for the brokered flow.
- Enforce `connect_user_entitlements.quota_json.monthly_post_reads`.
- Record usage in portal so admins can audit and later bill by user/provider.
- Keep the implementation scoped to X read-only operations already exposed in
  KittyPaw: recent search, user lookup, and user posts.

## Non-Goals

- Broker Gmail.
- Add payment plans or invoicing.
- Add X write scopes.
- Meter every possible X API endpoint.
- Build an end-user billing dashboard.
- Backfill usage for prior direct local X calls.

## Architecture

```text
kittypaw connect x
  -> POST connect.kittypaw.app/connect/x/sessions
  -> x.com OAuth
  -> connect.kittypaw.app/connect/x/callback
  -> portal stores encrypted X tokens for user_id/provider=x
  -> local receives a broker-connected marker, not X tokens

paw uses X.searchRecent / X.user / X.userPosts
  -> local engine calls connect.kittypaw.app/connect/x/broker/*
  -> portal verifies KittyPaw user JWT
  -> portal checks provider policy and user entitlement
  -> portal checks monthly_post_reads quota
  -> portal refreshes server-side X token when needed
  -> portal calls api.x.com
  -> portal records usage
  -> local engine receives X JSON and asks LLM to answer
```

## Token Model

Add a portal-owned token table for external provider tokens:

```sql
connect_provider_tokens (
  user_id uuid not null references users(id) on delete cascade,
  provider_id text not null,
  access_token_ciphertext bytea not null,
  refresh_token_ciphertext bytea,
  token_type text not null default 'Bearer',
  scope text not null default '',
  username text not null default '',
  expires_at timestamptz,
  updated_at timestamptz not null default now(),
  primary key (user_id, provider_id)
)
```

X tokens are encrypted before storing. The first implementation uses a required
`CONNECT_TOKEN_ENCRYPTION_KEY` environment variable carrying a base64-encoded
32-byte key and AES-GCM. The server must fail startup if X broker storage is
enabled without a valid key.

For backward-compatible local state, `connect/cli/exchange` still returns a
`TokenSet`, but for X it returns:

```json
{
  "provider": "x",
  "token_type": "broker",
  "scope": "tweet.read users.read offline.access",
  "username": "jaypark",
  "issued_at": "..."
}
```

No X access token or refresh token is returned for X. Gmail remains unchanged.

## Broker API

Add authenticated Connect-host endpoints under `connect.kittypaw.app`:

```text
GET /connect/x/broker/search/recent?query=...&limit=10
GET /connect/x/broker/users/by/username/{username}
GET /connect/x/broker/users/by/username/{username}/tweets?limit=10
GET /connect/x/broker/tweets/{id}
```

The `tweets/{id}` endpoint is included in this MVP because users naturally paste
X status URLs, and direct tweet lookup is cheaper and more accurate than asking
the model to infer from a timeline.

Each endpoint returns the same JSON shapes as `core.XClient` already exposes:
`XUser`, `XPost`, and `XPostsResult`.

## Quota Semantics

The entitlement field `quota_json.monthly_post_reads` becomes enforceable for X.

- `monthly_post_reads` counts returned posts, not HTTP requests.
- Search and user posts count the number of posts returned.
- Tweet lookup counts 1 when a post is returned.
- User lookup counts 0 because it does not return posts.
- Empty result sets count 0.
- Quota window is UTC calendar month for the first version.
- Missing `monthly_post_reads` means no monthly post quota for that entitlement.
- Blocked, revoked, disabled provider, or missing entitlement returns 403.
- Quota exhausted returns 429 with a machine-readable JSON error.

Add a usage table:

```sql
connect_provider_usage_events (
  id uuid primary key default gen_random_uuid(),
  user_id uuid not null references users(id) on delete cascade,
  provider_id text not null,
  operation text not null,
  quantity integer not null check (quantity >= 0),
  quota_key text not null default 'post_reads',
  window_start timestamptz not null,
  metadata_json jsonb not null default '{}'::jsonb,
  created_at timestamptz not null default now()
)
```

Quota checks run in a transaction after the X response is known, so the server
can count actual returned posts. If recording the usage would exceed quota, the
broker returns 429 and does not return the X result body to the local client.

## Local Runtime Changes

`apps/kittypaw/engine/x.go` stops using `ServiceTokenMgr.LoadAccessToken("x")`
for X API calls. Instead it builds a broker client using:

- `APITokenMgr.LoadAccessToken(core.DefaultAPIServerURL)` for the KittyPaw user
  JWT;
- `APITokenMgr.ResolveConnectBaseURL(core.DefaultAPIServerURL)` for the broker
  base URL.

If the user is not logged in, the tool returns guidance to run
`kittypaw login --account <account>`. If X is not connected server-side, the
broker returns 403 and the tool returns guidance to run
`kittypaw connect x --account <account>`.

Keep `oauth-x/*` support for MCP env injection only as legacy compatibility.
New `kittypaw connect x` results should not write X provider tokens locally.

## Error Handling

- 401 from broker: local login missing or expired; tell user to run
  `kittypaw login`.
- 403 from broker: X not connected, provider disabled, entitlement missing, or
  blocked; tell user to run `kittypaw connect x` or contact admin.
- 429 from broker: quota exhausted; include quota reset month/window in JSON.
- 502 from broker: X API failed; include a safe error string without tokens.
- Token refresh failure: delete no tokens automatically; tell user to reconnect.

## Migration

Existing users may already have local `oauth-x/access_token` and
`oauth-x/refresh_token`. The first broker release should prefer broker calls and
ignore local X provider tokens. It should not silently delete local tokens; a
later cleanup command can remove them after the broker path is stable.

After deploy, existing users must run `kittypaw connect x` once so portal can
store server-side X tokens.

## Tests

- Portal unit tests:
  - X callback stores server-side token and one-time exchange does not expose X
    access/refresh tokens.
  - Broker endpoints require authenticated portal user.
  - Broker enforces entitlement and provider enabled state.
  - Broker records usage and blocks when monthly quota would be exceeded.
  - Broker refreshes expired X tokens before calling X.
- Portal store tests:
  - token encryption round trip;
  - usage sum by month/provider/user;
  - quota transaction blocks over-limit usage.
- KittyPaw unit tests:
  - X engine calls broker with KittyPaw JWT instead of `api.x.com`;
  - broker 401/403/429 map to actionable errors;
  - connect x exchange stores broker marker without provider tokens.
- Smoke:
  - existing `make smoke-local`;
  - manual production X connect after deploy.

## Rollout

1. Add token storage, usage storage, and broker endpoints behind existing X
   provider configuration.
2. Change local X engine to broker-only.
3. Deploy portal and new Kittypaw CLI.
4. Re-run `kittypaw connect x` for initial users.
5. Monitor usage events and X API rate-limit responses.
