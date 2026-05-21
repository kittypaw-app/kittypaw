# Background Delegation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `Runner.delegate(staffId, task, true)` create and run a durable background delegation job that can be tracked by job id.

**Architecture:** Add a delegation-specific store table and runtime instead of reusing project shell jobs. The runtime claims queued jobs with leases, reuses the existing `executeDelegateTask` path, stores the result, and exposes status through `Runner.delegateStatus`, REST, and CLI.

**Tech Stack:** Go, SQLite migrations, Kittypaw `store`, `engine`, `server`, `client`, and `cli` packages.

---

### Task 1: Store Lifecycle

**Files:**
- Create: `apps/kittypaw/store/migrations/042_delegation_jobs.sql`
- Create: `apps/kittypaw/store/delegation_jobs.go`
- Modify: `apps/kittypaw/store/store_test.go`

- [ ] Write a failing store test for create, claim lease, finish, list, and cancel.
- [ ] Add the migration and store methods.
- [ ] Run `go test ./store -run 'TestDelegationJob'`.

### Task 2: Engine Runtime

**Files:**
- Create: `apps/kittypaw/engine/delegation_jobs.go`
- Modify: `apps/kittypaw/engine/orchestration.go`
- Modify: `apps/kittypaw/engine/account_runtime.go`
- Modify: `apps/kittypaw/engine/orchestration_test.go`

- [ ] Write a failing engine test proving `Runner.delegate(..., true)` returns a queued job immediately.
- [ ] Write a failing engine test proving the runtime processes the job through existing delegate execution.
- [ ] Implement `DelegationJobRuntime` and wire it to `AccountRuntime`.
- [ ] Run `go test ./engine -run 'TestRunnerDelegateBackground|TestDelegationJobRuntime'`.

### Task 3: Status Surfaces

**Files:**
- Modify: `apps/kittypaw/core/skillmeta.go`
- Modify: `apps/kittypaw/server/api.go`
- Modify: `apps/kittypaw/server/server.go`
- Modify: `apps/kittypaw/client/client.go`
- Modify: `apps/kittypaw/cli/main.go`

- [ ] Add `Runner.delegateStatus(jobId)` schema and executor handling.
- [ ] Add account-scoped REST list/get/cancel endpoints.
- [ ] Add CLI commands for list/show/cancel.
- [ ] Run targeted `engine`, `server`, and `cli` tests.

### Task 4: Verification And Candidate State

**Files:**
- Modify: `../candidates.md`

- [ ] Mark background delegation semantics as reflected.
- [ ] Run `git diff --check`.
- [ ] Run `go test ./...` from `apps/kittypaw`.
