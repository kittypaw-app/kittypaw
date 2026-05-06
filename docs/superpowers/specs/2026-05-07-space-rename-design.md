# Space Rename Design

Date: 2026-05-07
Status: Approved for implementation planning

## Purpose

Rename the hosted Home service to Space before production cutover. The external
canonical host becomes `space.kittypaw.app`, and the codebase should no longer
publish Home naming for this service.

This is a pre-production breaking rename. Compatibility with `home_base_url`,
`HOME_BASE_URL`, `KITTYHOME_*`, and `https://home.kittypaw.app` is intentionally
not preserved.

## Scope

Rename:

- `apps/home` to `apps/space`.
- Go module `github.com/kittypaw-app/kittyhome` to
  `github.com/kittypaw-app/kittyspace`.
- Binary and commands from `kittyhome*` to `kittyspace*`.
- Public host and JWT audience from `https://home.kittypaw.app` to
  `https://space.kittypaw.app`.
- Service env names from `KITTYHOME_*` to `KITTYSPACE_*`.
- Discovery/env keys from `home_base_url` / `HOME_BASE_URL` to
  `space_base_url` / `SPACE_BASE_URL`.
- Deployment paths from `kittyhome` to `kittyspace`.

Route paths stay the same:

```text
/chat
/chat/api/*
/daemon/connect
/v1/routes
/nodes/{device_id}/accounts/{account_id}/v1/chat/completions
```

`apps/chat` remains the legacy service until Space cutover checks pass.

## Architecture

The service remains a standalone Go app. Only naming, package import paths,
configuration keys, contracts, and docs change. Internal package boundaries are
unchanged.

Portal emits `space_base_url` in discovery and issues tokens with Space audience
during the migration. Kittypaw clients store `space_base_url`, prefer it for
hosted relay, and fall back to `chat_relay_url` when it is absent.

The local app directory is `apps/space`, while the product host is
`space.kittypaw.app`. Release tags use `space/vX.Y.Z`.

## Error Handling

Existing runtime behavior is unchanged. Config errors now reference
`KITTYSPACE_*` or `SPACE_*` names. Space cutover smoke fails fast when
`SPACE_BASE_URL`, `SPACE_USER_TOKEN`, `SPACE_DEVICE_TOKEN`, `SPACE_DEVICE_ID`,
or `SPACE_LOCAL_ACCOUNT_ID` are missing.

## Testing

- Space app tests pass under `apps/space`.
- Portal producer tests assert `space_base_url` and Space audience.
- Kittypaw discovery/client tests assert `space_base_url` preference and
  chat fallback.
- Contract schema/examples use `space_base_url`.
- Contract checks pass.
- Local smoke and credentialed cutover smoke command names work.
- Repository smoke script checks Space deploy syntax and fabfile syntax.

## Out Of Scope

- Production deploy execution.
- Backward compatibility for Home discovery/env/audience names.
- `apps/chat` decommission.
- Kanban implementation changes.
