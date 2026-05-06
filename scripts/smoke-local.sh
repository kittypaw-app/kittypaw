#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export GOCACHE="${GOCACHE:-/private/tmp/kitty-go-build}"
export PYTHONPYCACHEPREFIX="${PYTHONPYCACHEPREFIX:-/private/tmp/kitty-pycache}"

section() {
    printf '\n==> %s\n' "$1"
}

run() {
    printf '+ %s\n' "$*"
    "$@"
}

section "contracts"
run make -C "$ROOT" contracts-check

section "deploy script syntax"
run bash -n "$ROOT/apps/kittyapi/deploy/smoke.sh"
run bash -n "$ROOT/apps/portal/deploy/smoke.sh"
run bash -n "$ROOT/apps/portal/deploy/e2e_devices.sh"
run bash -n "$ROOT/apps/portal/deploy/check-token-shape.sh"
run bash -n "$ROOT/apps/chat/deploy/smoke.sh"
run bash -n "$ROOT/apps/space/deploy/smoke.sh"
run bash -n "$ROOT/apps/kakao/deploy/smoke.sh"

section "deploy python syntax"
run python3 -m py_compile "$ROOT/apps/kittyapi/fabfile.py" "$ROOT/apps/portal/fabfile.py" "$ROOT/apps/chat/fabfile.py" "$ROOT/apps/space/fabfile.py" "$ROOT/apps/kakao/fabfile.py"

section "kittypaw agent/channel critical flows"
run go test ./apps/kittypaw/engine -run 'Test(InstallConsent|InstalledExchangeRate|SlashPersona|RunAtMention|RunCanCreatePersona|RunReflectionCycle|TriggerEvolution)' -count=1
run go test ./apps/kittypaw/channel -run 'Test(TelegramTextUpdateFixtureBuildsEvent|KakaoIncomingFixtureBuildsEvent)' -count=1

section "go tests"
run go test ./apps/kittyapi/... -count=1
run go test ./apps/portal/... -count=1
run go test ./apps/chat/... -count=1
run go test ./apps/space/... -count=1
run go test ./apps/kakao/... -count=1
run go test ./apps/kittypaw/... -count=1

section "hosted relay in-process e2e"
run make -C "$ROOT/apps/chat" smoke-local
run make -C "$ROOT/apps/space" smoke-local

section "done"
