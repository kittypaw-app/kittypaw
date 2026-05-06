# Kitty Monorepo

Kitty is the monorepo for the KittyPaw product family.

This repository holds multiple independently deployed or released apps while
centralizing the contracts and cross-app verification that make them safe to
evolve together.

## Shape

```text
apps/
  kittypaw/          Local CLI, daemon, and local web UI
  kittyapi/          api.kittypaw.app hosted Kitty API surface
  chat/              chat.kittypaw.app hosted chat UI and daemon relay
  kakao/             kakao.kittypaw.app Kakao webhook and bridge
  portal/            portal.kittypaw.app identity/bootstrap service
contracts/           Wire-level schemas, examples, and version policies
testkit/             Cross-app verification helpers and fakes
deploy/              Repository-level deployment notes and shared assets
docs/                Architecture notes, decisions, operations, plans
scripts/             Repository-level helper scripts
```

## Core Rule

The repo boundary is shared. Runtime ownership is not.

- Apps are deployed or released separately.
- App-owned databases stay private to their app.
- Apps do not import another app's internal implementation.
- Shared code is added only when it removes real cross-service duplication.
- Wire contracts, schemas, examples, and E2E fixtures are centralized first.
- Contract changes must run producer and consumer tests together.

## Release Model

Product releases use namespaced tags:

```text
kittypaw/v0.1.0
kittyapi/v0.1.0
chat/v0.1.0
kakao/v0.1.0
portal/v0.1.0
```

`apps/kittypaw` keeps its own release workflow. The install script must resolve
the latest `kittypaw/v*` release, not the repository-wide latest release.

## Current Status

The historical repositories have been imported with path-level history. New
deployable product work should live under `apps/<name>`.

## Local Smoke

```bash
make help
make contracts-check
make smoke-local
```

`make contracts-check` validates JSON contract fixtures. `make smoke-local`
runs contracts, deploy-script syntax checks, Go/Rust package tests, and the
Chat and Space in-process e2e smokes without touching production hosts.
`make e2e-local` runs the Docker-backed local auth/space E2E.
