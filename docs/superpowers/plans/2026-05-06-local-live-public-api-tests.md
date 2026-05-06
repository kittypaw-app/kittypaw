# Local Live Public API Tests Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a local full-test target that reuses production public-data API keys for AirKorea, KMA, and KASI integration tests without copying sensitive service credentials.

**Architecture:** Keep Gmail/OAuth E2E hermetic with fake Google. Add a small shell helper that fetches only an allowlist of public-data keys from the server `.env`, exports them for one child command, and never writes or prints secret values. Wire it into the root Makefile after existing `smoke-local` and `e2e-local`, keeping DB-backed KittyAPI integration separate from public-data live tests.

**Tech Stack:** Bash, GNU/POSIX shell tools available on macOS, SSH, Make, existing Go integration tests.

---

### Task 1: Add Allowlisted Env Fetch Helper

**Files:**
- Create: `scripts/with-kittyapi-public-env.sh`

- [ ] **Step 1: Create helper script**

Create `scripts/with-kittyapi-public-env.sh` with these behaviors:
- Default SSH host: `second`
- Default remote env file: `/home/jinto/kittyapi/.env`
- Export only `AIRKOREA_API_KEY`, `WEATHER_API_KEY`, and `HOLIDAY_API_KEY`
- Do not print values
- Execute the command passed after `--`

- [ ] **Step 2: Syntax check**

Run:

```bash
bash -n scripts/with-kittyapi-public-env.sh
```

Expected: exit 0.

### Task 2: Add Root Full Local Live Target

**Files:**
- Modify: `Makefile`
- Modify: `apps/kittyapi/Makefile`
- Modify: `apps/kittyapi/internal/config/config_test.go`
- Modify: `apps/kittyapi/internal/proxy/places_integration_test.go`
- Create: `apps/kittyapi/internal/model/integration_helpers_test.go`

- [ ] **Step 1: Add target**

Add `full-local-live` to `.PHONY`, help output, and implementation:

```make
full-local-live:
	@scripts/smoke-local.sh
	@scripts/e2e-local.sh
	@set -e; \
		trap 'make -C apps/kittyapi test-integration-down >/dev/null' EXIT; \
		make -C apps/kittyapi test-integration; \
		scripts/with-kittyapi-public-env.sh -- make -C apps/kittyapi test-integration-public
```

- [ ] **Step 2: Dry list check**

Run:

```bash
make help
```

Expected: help includes `full-local-live`.

### Task 3: Verify

**Files:**
- No code edits.

- [ ] **Step 1: Run smoke**

Run:

```bash
make smoke-local
```

Expected: exit 0.

- [ ] **Step 2: Run Docker E2E**

Run:

```bash
make e2e-local
```

Expected: exit 0 and disposable Postgres is removed.

- [ ] **Step 3: Run live public-data integration**

Run:

```bash
scripts/with-kittyapi-public-env.sh -- make -C apps/kittyapi test-integration-public
```

Expected: exit 0. Tests may skip only if the remote env lacks a specific public-data key.

- [ ] **Step 4: Run full target**

Run:

```bash
make full-local-live
```

Expected: exit 0. This runs local smoke, Docker-backed E2E, DB-backed KittyAPI integration tests, allowlisted public-data live tests, and cleans up the KittyAPI test database container.
