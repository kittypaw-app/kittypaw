# Agent Guide

This repo is a Go-first monorepo for KittyPaw apps.

## Operating Principles

- Read local docs before making structural changes.
- Keep app boundaries explicit.
- Do not move existing repositories into this tree without an explicit migration
  plan.
- Do not introduce shared runtime packages just because code looks similar.
- Prefer contract fixtures and cross-app tests over implicit shared types.
- Never let one app read another app's database directly.

## Directory Rules

- `apps/kittypaw` owns the local CLI, daemon, local web UI, engine, local store,
  and local channel adapters.
- `apps/kittyapi` owns hosted `/v1/*` public data API and proxy endpoints.
- `apps/portal` owns OAuth, users, refresh tokens, devices, discovery, JWKS,
  and service bootstrap metadata.
- `apps/space` owns hosted Space surfaces, beginning with `/chat`, plus the
  daemon outbound WebSocket relay and Space capability routing.
- `apps/chat` owns the legacy hosted chat service until Space cutover,
  including route discovery, OpenAI-compatible relay endpoints, and daemon
  outbound WebSocket relay.
- `apps/kakao` owns Kakao OpenBuilder webhook, Kakao callback dispatch, and
  Kakao-specific pairing.
- `contracts` owns wire-level schemas and examples. It is the first place to
  update when a producer/consumer contract changes.
- `testkit` owns reusable fake services and credentials for cross-service tests.

## Contract Change Checklist

When changing anything under `contracts/`:

1. Update the schema or enum source.
2. Update at least one example fixture.
3. Add or update producer tests in the owning service.
4. Add or update consumer tests in every affected service.
5. Run `make contracts-check` from the repository root.

## Release Tags

Use namespaced tags:

- `kittypaw/vX.Y.Z`
- `kittyapi/vX.Y.Z`
- `portal/vX.Y.Z`
- `space/vX.Y.Z`
- `chat/vX.Y.Z`
- `kakao/vX.Y.Z`

Do not use a plain `vX.Y.Z` tag for product releases in this monorepo.
