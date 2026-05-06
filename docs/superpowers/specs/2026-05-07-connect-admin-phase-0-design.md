# KittyPaw Connect Admin Phase 0 Design

## Summary

KittyPaw Connect now links external accounts through `apps/portal`, exposed as
`connect.kittypaw.app`. Gmail and X are the first real providers. The next
missing layer is not another OAuth provider; it is an operator control plane for
who may use Connect, which providers are enabled, and how paid provider risk is
contained.

Phase 0 adds a small admin foundation to `apps/portal`:

- staff-only access control for admin routes;
- provider status and configuration visibility;
- per-user Connect entitlement records;
- provider-level enable/disable gates;
- X-specific allowlist and quota metadata;
- audit records for admin changes.

Phase 0 deliberately does not let admins read Gmail mail, read X posts through a
user token, or inspect third-party OAuth tokens.

## Problem

Today `portal` has `users`, `devices`, and first-party refresh tokens. It does
not have roles, plans, provider entitlements, or provider usage controls.

The current Connect flow is intentionally local-token oriented:

1. The user runs `kittypaw connect gmail` or `kittypaw connect x`.
2. `connect.kittypaw.app` performs the external OAuth flow.
3. Portal creates a short-lived Connect code.
4. The local CLI exchanges that code and stores the external refresh/access
   tokens in the local KittyPaw account secrets file.

That is a good privacy default for Gmail, because portal does not persist Gmail
tokens or mail content. It is not enough for X, because X API usage is billed to
the KittyPaw developer app. If every connected local daemon can call X directly,
KittyPaw cannot centrally enforce per-user quotas, cost controls, or abuse
blocks.

## Official Constraints

### Gmail

Google's Gmail API scope table classifies `gmail.readonly` as a restricted
scope. Google says apps requesting sensitive or restricted scopes must complete
OAuth verification unless an exception applies. Restricted scopes can also
trigger security assessment if restricted data is stored or transmitted through
a server.

Implication for Phase 0:

- Admin must show Gmail verification state and requested scopes.
- Admin must not create an expectation that portal staff can inspect mail.
- The current local-token model should remain the default for Gmail.

Sources:

- https://developers.google.com/workspace/gmail/api/auth/scopes
- https://support.google.com/cloud/answer/13463073
- https://developers.google.com/identity/protocols/oauth2/production-readiness/restricted-scope-verification

### X

X API v2 is pay-per-usage. Usage is tracked at the developer app level, credits
are deducted as API requests are made, and successful post-returning endpoints
such as search, timelines, post lookup, liked posts, bookmarks, lists, and
spaces count toward usage. X also exposes usage reporting with daily app/project
consumption.

Implication for Phase 0:

- X Connect must default to restricted access, not public access.
- Admin must model X as a paid provider even if a user's own X account is used.
- Per-user entitlement, quota metadata, and audit history are required before
  opening X beyond staff/internal users.
- A later phase should broker X API calls through a server-side control path if
  KittyPaw wants enforceable central quotas.

Sources:

- https://docs.x.com/x-api/fundamentals/post-cap
- https://docs.x.com/x-api/usage/introduction
- https://docs.x.com/fundamentals/authentication/oauth-2-0/authorization-code
- https://docs.x.com/fundamentals/developer-apps

## Non-Goals

Phase 0 does not:

- store Gmail or X OAuth tokens in portal;
- proxy X API calls through portal;
- implement billing or paid subscriptions;
- expose an end-user self-service account page;
- add Gmail send/modify scope;
- add X write, like, follow, or DM scopes;
- add a general RBAC framework for every KittyPaw app;
- let an admin view external user data such as mail bodies, X posts fetched by a
  user, DMs, bookmarks, or private account data.

## Design Principles

1. Admin is control-plane only. It governs access and operational state, not
   user data.
2. X is cost-bearing. Its default state must be safer than Gmail's.
3. Provider scopes are product decisions, not incidental code constants.
4. Every admin mutation needs audit history.
5. Start with narrow, explicit tables. Avoid a generic permission framework
   until more than Connect needs it.
6. Keep app boundaries: `apps/portal` owns Connect admin; `apps/kittypaw` keeps
   local runtime behavior.

## Recommended Approach

Use an operator-only portal admin surface first:

```text
https://portal.kittypaw.app/admin/connect
```

This belongs on the portal host, not the connect host. `connect.kittypaw.app` is
for user-facing OAuth flows. `portal.kittypaw.app` is the identity, discovery,
device, and operator authority.

Phase 0 should be server-rendered HTML or small JSON-backed admin pages inside
the existing `apps/portal` binary. It should not add a separate frontend app or
new service.

## Alternative Approaches Considered

### A. Read-only dashboard only

Show provider status, configured scopes, and env health but do not gate users.

Pros:

- very low risk;
- useful for operations;
- no product policy decisions.

Cons:

- does not solve the actual X cost exposure;
- still lets any authenticated/local user connect if the route is public.

Verdict: too weak for X.

### B. Provider entitlement admin, no API proxy

Add staff-only admin, per-provider enablement, per-user entitlements, and audit
logs. Keep local token storage and local Gmail/X execution for now.

Pros:

- smallest change that closes the public-X access gap;
- preserves Gmail local-token privacy;
- gives operators a real list of who may connect paid providers;
- does not require a new usage billing system yet.

Cons:

- X quotas are advisory until X calls are server-brokered;
- local users who already have tokens may keep using them unless local runtime
  also consults entitlement state or tokens are revoked/rotated.

Verdict: recommended Phase 0.

### C. Full server-brokered Connect from the start

Store provider tokens server-side or broker every paid-provider API request
through portal. Enforce quotas centrally.

Pros:

- strongest X cost control;
- enables usage accounting and future billing;
- clean place for provider-side caching and dedupe.

Cons:

- changes the privacy model;
- expands security burden;
- requires new token encryption, revocation, usage accounting, and local
  protocol changes;
- too large for the immediate admin gap.

Verdict: likely Phase 1/2 for X, not Phase 0.

## Phase 0 Scope

### Staff Identity

Add a minimal staff authorization layer to portal.

Recommended v0 source:

```text
PORTAL_ADMIN_EMAILS=alice@example.com,bob@example.com
```

The existing portal auth creates users through Google/GitHub identity login.
Admin routes should require an authenticated portal user whose email is in the
allowlist. This is simpler and safer than adding a full roles table before the
first admin page exists.

Follow-up trigger for DB-backed roles:

- more than 3 admins;
- need support-only vs owner roles;
- need non-email subject identities;
- need self-service team administration.

### Provider Registry

Add a typed registry for Connect providers:

```text
provider_id: gmail | x
display_name
configured: bool
enabled_default: bool
requested_scopes
write_capable: bool
cost_bearing: bool
docs_url
```

The registry can start in code/config, derived from existing env and scope
constants. It should power both admin status pages and connect gating.

### Provider Policy Table

Add a small DB table for mutable operator policy:

```sql
connect_provider_policies (
  provider_id text primary key,
  enabled boolean not null default false,
  default_entitlement text not null default 'deny',
  requested_scopes text[] not null default '{}',
  verification_status text not null default 'unknown',
  cost_mode text not null default 'none',
  notes text not null default '',
  updated_by uuid,
  updated_at timestamptz not null default now()
)
```

Allowed values:

- `default_entitlement`: `allow`, `deny`
- `verification_status`: `unknown`, `not_applicable`, `testing`,
  `submitted`, `verified`, `blocked`
- `cost_mode`: `none`, `external_policy`, `kitty_paid`

Initial policy:

- Gmail: `enabled=true`, `default_entitlement=allow`,
  `verification_status=testing`, `cost_mode=none`.
- X: `enabled=true`, `default_entitlement=deny`,
  `verification_status=not_applicable`, `cost_mode=kitty_paid`.

### User Entitlements

Add a table that says which user can use which provider:

```sql
connect_user_entitlements (
  id uuid primary key default gen_random_uuid(),
  user_id uuid not null references users(id) on delete cascade,
  provider_id text not null,
  status text not null,
  quota_json jsonb not null default '{}'::jsonb,
  reason text not null default '',
  granted_by uuid,
  granted_at timestamptz not null default now(),
  revoked_at timestamptz,
  unique (user_id, provider_id)
)
```

Allowed `status` values:

- `allowed`
- `blocked`
- `revoked`

Example X `quota_json`:

```json
{
  "monthly_post_reads": 100,
  "daily_searches": 10,
  "note": "internal beta credit"
}
```

For Phase 0 this quota is policy metadata and UI guidance. It is not yet strong
enforcement for local direct X API calls.

### Audit Log

Add a small append-only table:

```sql
connect_admin_audit_events (
  id uuid primary key default gen_random_uuid(),
  actor_user_id uuid references users(id),
  action text not null,
  provider_id text,
  target_user_id uuid references users(id),
  before_json jsonb,
  after_json jsonb,
  created_at timestamptz not null default now()
)
```

Every mutation in Phase 0 writes one audit row. Do not log secrets or tokens.

## Route Shape

Admin routes live only on the portal host and require portal user auth plus the
admin email allowlist:

```text
GET  /admin/connect
GET  /admin/connect/providers
POST /admin/connect/providers/{provider_id}
GET  /admin/connect/users
POST /admin/connect/users/{user_id}/providers/{provider_id}
GET  /admin/connect/audit
```

Connect flow routes remain on the connect host:

```text
GET  https://connect.kittypaw.app/connect/gmail/login
GET  https://connect.kittypaw.app/connect/x/login
```

The login handlers should check provider policy and user entitlement before
redirecting to Google/X. For CLI-originated X Connect flows, Phase 0 requires a
first-party KittyPaw access token from `kittypaw login`; without an
authenticated portal user, X fails closed before redirecting to X OAuth. Gmail
keeps the existing unauthenticated broker flow in Phase 0 unless explicitly
disabled by provider policy.

## Authentication Decision

Phase 0 should not rely on `?secret=` admin URLs. The Kakao app has secret-based
admin endpoints for a different app boundary, but portal already has real user
identity and JWT middleware. Use portal auth.

For CLI Connect gating there were two viable options:

1. Require `kittypaw login` before `kittypaw connect x`, then attach the portal
   access token to the Connect login initiation.
2. Allow unauthenticated `connect gmail` during beta, but require authenticated
   `connect x`.

Phase 0 decision:

- Gmail can stay as-is until verification/user rollout policy is decided.
- X should require first-party portal auth before OAuth redirect.

This preserves current Gmail workflow while treating X as cost-bearing.

## X Cost Control Semantics

Phase 0 gates who can connect X. It does not yet meter all X usage.

Operationally:

- X provider default is deny.
- Staff explicitly grants X to a user.
- The grant should contain a quota note.
- The admin page displays that X usage still happens locally in v0.5.2-style
  clients and is not centrally enforceable yet.
- Docs and UI should state that public X rollout requires server-brokered usage
  enforcement.

Phase 1 trigger:

- more than staff/internal users have X enabled;
- X credits exceed a small operator-defined monthly threshold;
- user asks for public/free X access;
- X write scopes are considered.

Phase 1 direction:

- route X tool calls through a KittyPaw-owned broker endpoint;
- record `provider_usage_events`;
- cache and dedupe returned post IDs per UTC day;
- enforce per-user quota before the upstream call;
- surface usage in admin.

## Gmail Semantics

Phase 0 tracks Gmail as a provider and shows verification state, but should not
block current internal testing unless policy says so.

Admin should expose:

- requested scopes;
- Google publishing status as operator-entered metadata;
- test user notes;
- verification docs links;
- whether `CONNECT_GOOGLE_CLIENT_ID` is configured, masked.

It should not expose:

- Gmail tokens;
- message count;
- mailbox contents;
- user email metadata fetched from Gmail.

## Data Flow

X allowed-user flow:

1. User signs into KittyPaw portal.
2. Staff opens `/admin/connect/users`.
3. Staff grants `x` to the user with a quota note.
4. User runs `kittypaw connect x`.
5. Portal verifies provider is enabled and user has X entitlement.
6. Portal redirects to X OAuth.
7. Local KittyPaw stores returned X tokens in local account secrets.

X denied-user flow:

1. User runs `kittypaw connect x`.
2. Portal sees no entitlement.
3. Portal returns a clear denial page or CLI-friendly error.
4. No X OAuth redirect occurs.

## UI Shape

Keep the UI utilitarian:

- provider table: provider, configured, enabled, default policy, scopes,
  verification/cost state, last updated;
- user table: email, name, devices count, Gmail entitlement, X entitlement;
- provider detail: scopes, redirect URI, masked env status, docs links, notes;
- audit table: timestamp, actor, action, provider, target user.

No marketing page. No token display.

## Testing

Unit tests:

- admin allowlist accepts configured emails and rejects others;
- provider policy defaults match expected Gmail/X behavior;
- user entitlement lookup allows, denies, and fails closed;
- audit events are written for provider and entitlement mutations.

Router tests:

- `/admin/connect` 401 without auth;
- `/admin/connect` 403 for non-admin authenticated user;
- `/admin/connect` 200 for allowlisted admin;
- connect host cannot serve admin routes;
- portal host cannot serve connect OAuth routes.

Connect gating tests:

- X login without entitlement fails before upstream redirect;
- X login with entitlement redirects to X OAuth;
- Gmail login behavior remains unchanged unless policy explicitly disables it.

Migration tests:

- new tables apply cleanly;
- foreign keys delete entitlement rows when users are deleted;
- audit rows do not contain token-looking fields.

## Rollout

1. Add admin auth allowlist and read-only admin shell.
2. Add provider policy and entitlement tables.
3. Seed Gmail/X default policies.
4. Gate X Connect with fail-closed entitlement checks.
5. Add admin forms to grant/revoke X.
6. Add docs explaining that X is staff/invite-only until usage is brokered.

Do not open X to all users in Phase 0.

## Open Decisions For Implementation Plan

1. Whether `kittypaw connect x` should force `kittypaw login` immediately, or
   allow a browser login bounce from the Connect page.
2. Whether Gmail should also require first-party portal auth in Phase 0, or keep
   the current unauthenticated OAuth broker flow for beta continuity.
3. Whether admin UI should be server-rendered HTML only, or JSON API plus simple
   HTML. Recommended: server-rendered HTML first.

## Approved Phase 0 Boundary

The first implementation plan should stop at:

- admin-only Connect status and entitlement management;
- X connect allowlist;
- audit log;
- docs/tests.

It should not implement:

- X server-side API proxy;
- billing;
- public self-service access requests;
- write scopes;
- token storage in portal.
