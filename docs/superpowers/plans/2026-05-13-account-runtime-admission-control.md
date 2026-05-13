# Account Runtime Admission Control Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add bounded admission control for account runtime execution so WebSocket, relay/API, channel, scheduler, delegation, and MoA cannot overload the same account or conversation.

**Architecture:** Introduce a small `engine.RuntimeAdmission` limiter owned by each `AccountRuntime`. It enforces account-level and conversation/source-level concurrency with bounded wait queues, returns typed busy errors for callers to map to user-visible responses, and exposes snapshot counters for status. Scheduler gets its own account-local worker cap so due jobs cannot create unbounded goroutines.

**Tech Stack:** Go, existing `core.Config`, `engine.AccountRuntime`, `server` WebSocket/API/channel dispatch, existing Go unit tests.

---

## File Structure

- Modify `apps/kittypaw/core/config.go`: add `[runtime]` config for admission defaults.
- Create `apps/kittypaw/engine/admission.go`: keyed limiter, typed errors, snapshots.
- Create `apps/kittypaw/engine/admission_test.go`: limiter behavior tests.
- Modify `apps/kittypaw/engine/account_runtime.go`: acquire/release admission around `Run` and `RunTurn`.
- Modify `apps/kittypaw/engine/schedule.go`: scheduler worker cap and queue policy.
- Modify `apps/kittypaw/engine/schedule_test.go`: scheduler cap regression tests.
- Modify `apps/kittypaw/server/account_deps.go` and `apps/kittypaw/server/account_config.go`: wire admission into new/rebuilt runtimes.
- Modify `apps/kittypaw/server/api.go`, `apps/kittypaw/server/ws.go`, `apps/kittypaw/server/channel_dispatch.go`, `apps/kittypaw/server/chat_relay_dispatcher.go`: map admission busy to 429/503/error frame/busy channel response.
- Modify `apps/kittypaw/server/api_test.go`, `apps/kittypaw/server/ws_validate_test.go`, `apps/kittypaw/server/channel_dispatch_test.go`, `apps/kittypaw/server/chat_relay_dispatcher_test.go`: caller behavior tests.

---

### Task 1: Runtime Config Surface

**Files:**
- Modify: `apps/kittypaw/core/config.go`
- Test: `apps/kittypaw/core/config_test.go`

- [ ] **Step 1: Add failing config test**

Add a test that verifies default values and TOML loading:

```go
func TestRuntimeConfigDefaultsAndParsing(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Runtime.MaxConcurrentTurnsPerAccount != 1 {
		t.Fatalf("MaxConcurrentTurnsPerAccount = %d, want 1", cfg.Runtime.MaxConcurrentTurnsPerAccount)
	}
	if cfg.Runtime.MaxQueuedTurnsPerAccount != 32 {
		t.Fatalf("MaxQueuedTurnsPerAccount = %d, want 32", cfg.Runtime.MaxQueuedTurnsPerAccount)
	}
	if cfg.Runtime.MaxConcurrentTurnsPerConversation != 1 {
		t.Fatalf("MaxConcurrentTurnsPerConversation = %d, want 1", cfg.Runtime.MaxConcurrentTurnsPerConversation)
	}
	if cfg.Runtime.MaxConcurrentScheduledJobs != 2 {
		t.Fatalf("MaxConcurrentScheduledJobs = %d, want 2", cfg.Runtime.MaxConcurrentScheduledJobs)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./apps/kittypaw/core -run TestRuntimeConfigDefaultsAndParsing -count=1
```

Expected: FAIL because `Config.Runtime` does not exist.

- [ ] **Step 3: Add config type and defaults**

Add to `core/config.go`:

```go
type RuntimeConfig struct {
	MaxConcurrentTurnsPerAccount      uint32 `toml:"max_concurrent_turns_per_account"`
	MaxQueuedTurnsPerAccount          uint32 `toml:"max_queued_turns_per_account"`
	MaxConcurrentTurnsPerConversation uint32 `toml:"max_concurrent_turns_per_conversation"`
	MaxConcurrentScheduledJobs        uint32 `toml:"max_concurrent_scheduled_jobs"`
}
```

Add to `Config`:

```go
Runtime RuntimeConfig `toml:"runtime"`
```

Add to `DefaultConfig()`:

```go
Runtime: RuntimeConfig{
	MaxConcurrentTurnsPerAccount:      1,
	MaxQueuedTurnsPerAccount:          32,
	MaxConcurrentTurnsPerConversation: 1,
	MaxConcurrentScheduledJobs:        2,
},
```

- [ ] **Step 4: Run config tests**

Run:

```bash
go test ./apps/kittypaw/core -run TestRuntimeConfigDefaultsAndParsing -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit config surface**

```bash
git add apps/kittypaw/core/config.go apps/kittypaw/core/config_test.go
git commit -m "feat(kittypaw): add runtime admission config"
```

---

### Task 2: Admission Limiter

**Files:**
- Create: `apps/kittypaw/engine/admission.go`
- Create: `apps/kittypaw/engine/admission_test.go`

- [ ] **Step 1: Write failing limiter tests**

Create tests for immediate acquire, busy error, queued wait, and snapshot:

```go
func TestRuntimeAdmissionAccountLimitBusy(t *testing.T) {
	a := NewRuntimeAdmission(RuntimeAdmissionConfig{
		MaxConcurrentAccount: 1,
		MaxQueuedAccount:     0,
		MaxConcurrentScope:   0,
	})
	lease, err := a.Acquire(context.Background(), RuntimeAdmissionRequest{
		AccountID: "alice",
		ScopeKey:  "general:web_chat:s1",
		Class:     AdmissionForeground,
	})
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer lease.Release()

	_, err = a.Acquire(context.Background(), RuntimeAdmissionRequest{
		AccountID: "alice",
		ScopeKey:  "general:web_chat:s2",
		Class:     AdmissionForeground,
	})
	if !errors.Is(err, ErrRuntimeAdmissionBusy) {
		t.Fatalf("second Acquire err = %v, want ErrRuntimeAdmissionBusy", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./apps/kittypaw/engine -run TestRuntimeAdmission -count=1
```

Expected: FAIL because admission types do not exist.

- [ ] **Step 3: Implement `engine/admission.go`**

Create these public types:

```go
var ErrRuntimeAdmissionBusy = errors.New("runtime admission busy")

type AdmissionClass string

const (
	AdmissionForeground AdmissionClass = "foreground"
	AdmissionBackground AdmissionClass = "background"
	AdmissionRetry      AdmissionClass = "retry"
)

type RuntimeAdmissionConfig struct {
	MaxConcurrentAccount uint32
	MaxQueuedAccount     uint32
	MaxConcurrentScope   uint32
}

type RuntimeAdmissionRequest struct {
	AccountID string
	ScopeKey  string
	Class     AdmissionClass
}

type RuntimeAdmissionSnapshot struct {
	AccountRunning uint32 `json:"account_running"`
	AccountQueued  uint32 `json:"account_queued"`
	ScopeRunning   uint32 `json:"scope_running"`
	ScopeQueued    uint32 `json:"scope_queued"`
}
```

Implement `NewRuntimeAdmission`, `Acquire`, `Snapshot`, and an idempotent `RuntimeAdmissionLease.Release()`. Use mutex-protected counters and buffered channels; avoid private runtime fields.

- [ ] **Step 4: Run limiter tests**

Run:

```bash
go test ./apps/kittypaw/engine -run TestRuntimeAdmission -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit limiter**

```bash
git add apps/kittypaw/engine/admission.go apps/kittypaw/engine/admission_test.go
git commit -m "feat(kittypaw): add account runtime admission limiter"
```

---

### Task 3: Wire Admission Into `AccountRuntime`

**Files:**
- Modify: `apps/kittypaw/engine/account_runtime.go`
- Modify: `apps/kittypaw/engine/run_turn_test.go`
- Modify: `apps/kittypaw/server/account_deps.go`
- Modify: `apps/kittypaw/server/account_config.go`

- [ ] **Step 1: Add failing runtime tests**

Add tests around the private admission wrapper that `Run` and the `RunTurn` owner path will both call. This keeps the test independent from provider setup while still proving account-level rejection semantics:

```go
func TestAccountRuntimeAcquireTurnAdmissionBusy(t *testing.T) {
	rt := &AccountRuntime{
		AccountID: "alice",
		Admission: NewRuntimeAdmission(RuntimeAdmissionConfig{
			MaxConcurrentAccount: 1,
			MaxQueuedAccount:     0,
			MaxConcurrentScope:   0,
		}),
	}

	first, err := rt.acquireTurnAdmission(context.Background(), core.Event{
		Type: core.EventWebChat,
		Payload: core.ChatPayload{
			ConversationID:  "general:web_chat:one",
			SourceSessionID: "one",
		},
	})
	if err != nil {
		t.Fatalf("first acquireTurnAdmission: %v", err)
	}
	defer first.Release()

	_, err = rt.acquireTurnAdmission(context.Background(), core.Event{
		Type: core.EventWebChat,
		Payload: core.ChatPayload{
			ConversationID:  "general:web_chat:two",
			SourceSessionID: "two",
		},
	})
	if !errors.Is(err, ErrRuntimeAdmissionBusy) {
		t.Fatalf("second acquireTurnAdmission err = %v, want ErrRuntimeAdmissionBusy", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./apps/kittypaw/engine -run TestAccountRuntimeAcquireTurnAdmissionBusy -count=1
```

Expected: FAIL because `AccountRuntime.Admission` and `acquireTurnAdmission` do not exist.

- [ ] **Step 3: Add admission field and acquire helper**

Add to `AccountRuntime`:

```go
Admission *RuntimeAdmission
```

Add helper:

```go
func (s *AccountRuntime) acquireTurnAdmission(ctx context.Context, event core.Event) (context.Context, *RuntimeAdmissionLease, error) {
	if s == nil || s.Admission == nil {
		return ctx, nil, nil
	}
	if held, ok := ctx.Value(runtimeAdmissionContextKey{}).(*AccountRuntime); ok && held == s {
		return ctx, nil, nil
	}
	scope := admissionScopeForEvent(s, &event)
	lease, err := s.Admission.Acquire(ctx, RuntimeAdmissionRequest{
		AccountID: s.AccountID,
		ScopeKey:  scope,
		Class:     admissionClassForEvent(event),
	})
	if err != nil {
		return ctx, nil, err
	}
	return context.WithValue(ctx, runtimeAdmissionContextKey{}, s), lease, nil
}
```

Acquire at the top of plain `Run`. `RunTurn` owner execution already calls `Run`, while retries with the same `turn_id` wait on the existing cached state and do not acquire another lease. The context marker prevents internal delegate runs from deadlocking behind the outer turn's account lease.

- [ ] **Step 4: Wire construction**

In `buildAccountRuntime` and `rebuildRuntimeForConfigLocked`, create:

```go
Admission: engine.NewRuntimeAdmission(engine.RuntimeAdmissionConfig{
	MaxConcurrentAccount: td.Account.Config.Runtime.MaxConcurrentTurnsPerAccount,
	MaxQueuedAccount:     td.Account.Config.Runtime.MaxQueuedTurnsPerAccount,
	MaxConcurrentScope:   td.Account.Config.Runtime.MaxConcurrentTurnsPerConversation,
}),
```

When rebuilding an account, replace admission config from the new config. Do not carry over old queued waiters across reload.

- [ ] **Step 5: Run runtime tests**

Run:

```bash
go test ./apps/kittypaw/engine -run 'TestAccountRuntimeAcquireTurnAdmissionBusy|TestRunTurn' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit runtime wiring**

```bash
git add apps/kittypaw/engine/account_runtime.go apps/kittypaw/engine/run_turn_test.go apps/kittypaw/server/account_deps.go apps/kittypaw/server/account_config.go
git commit -m "feat(kittypaw): gate account runtime turns"
```

---

### Task 4: Map Admission Busy Per Surface

**Files:**
- Modify: `apps/kittypaw/server/api.go`
- Modify: `apps/kittypaw/server/ws.go`
- Modify: `apps/kittypaw/server/channel_dispatch.go`
- Modify: `apps/kittypaw/server/chat_relay_dispatcher.go`
- Test: `apps/kittypaw/server/channel_dispatch_test.go`
- Test: `apps/kittypaw/server/ws_validate_test.go`

- [ ] **Step 1: Add failing server behavior tests**

Add tests that force `ErrRuntimeAdmissionBusy` and assert:

- channel response uses `channelQueueOverflowResponse`
- WebSocket sends a turn error
- `/api/v1/chat` returns HTTP 429 with a short error body
- chat relay local API maps the upstream 429 without rewriting it

- [ ] **Step 2: Implement a shared mapper**

Add to server package:

```go
func isRuntimeAdmissionBusy(err error) bool {
	return errors.Is(err, engine.ErrRuntimeAdmissionBusy)
}
```

Use it in channel dispatch:

```go
if isRuntimeAdmissionBusy(runErr) {
	s.sendOrQueueChannelFailure(ctx, job, channelQueueOverflowResponse)
	return
}
```

Use it in `/api/v1/chat`:

```go
if errors.Is(err, engine.ErrRuntimeAdmissionBusy) {
	writeError(w, http.StatusTooManyRequests, "runtime busy")
	return
}
```

Use the same message in WebSocket turn errors.

- [ ] **Step 3: Run server tests**

Run:

```bash
go test ./apps/kittypaw/server -run 'Test.*Admission|TestDispatchLoop|TestWebSocket' -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit surface mapping**

```bash
git add apps/kittypaw/server/api.go apps/kittypaw/server/ws.go apps/kittypaw/server/channel_dispatch.go apps/kittypaw/server/chat_relay_dispatcher.go apps/kittypaw/server/*test.go
git commit -m "feat(kittypaw): map runtime admission busy responses"
```

---

### Task 5: Scheduler Account Cap

**Files:**
- Modify: `apps/kittypaw/engine/schedule.go`
- Modify: `apps/kittypaw/engine/schedule_test.go`

- [ ] **Step 1: Add failing scheduler cap test**

Add a test with three due jobs and `MaxConcurrentScheduledJobs = 1`; assert only one starts before the first completes.

- [ ] **Step 2: Implement scheduler semaphore**

Add to `Scheduler`:

```go
	jobSlots chan struct{}
```

Initialize in `NewScheduler`:

```go
limit := runtime.Config.Runtime.MaxConcurrentScheduledJobs
if limit == 0 {
	limit = 2
}
s.jobSlots = make(chan struct{}, limit)
```

Before starting each scheduled goroutine, acquire a slot with non-blocking semantics. If full, skip this tick and leave `last_run_at` untouched so the job can run on a later tick.

- [ ] **Step 3: Run scheduler tests**

Run:

```bash
go test ./apps/kittypaw/engine -run 'TestScheduler|TestSchedule' -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit scheduler cap**

```bash
git add apps/kittypaw/engine/schedule.go apps/kittypaw/engine/schedule_test.go
git commit -m "feat(kittypaw): cap scheduled job concurrency"
```

---

### Task 6: Observability

**Files:**
- Modify: `apps/kittypaw/engine/admission.go`
- Modify: `apps/kittypaw/server/api.go`
- Modify: `apps/kittypaw/engine/commands.go`
- Test: `apps/kittypaw/server/api_test.go`
- Test: `apps/kittypaw/engine/commands_test.go`

- [ ] **Step 1: Add status fields**

Expose `AccountRuntime.Admission.Snapshot()` in `/api/v1/status` under:

```json
{
  "runtime": {
    "account_running": 0,
    "account_queued": 0,
    "scope_running": 0,
    "scope_queued": 0
  }
}
```

Also include a compact line in `/status` slash command:

```text
runtime: running=0 queued=0 scope_running=0 scope_queued=0
```

- [ ] **Step 2: Run observability tests**

Run:

```bash
go test ./apps/kittypaw/server ./apps/kittypaw/engine -run 'Test.*Status.*Runtime|TestStatus' -count=1
```

Expected: PASS.

- [ ] **Step 3: Commit observability**

```bash
git add apps/kittypaw/engine/admission.go apps/kittypaw/engine/commands.go apps/kittypaw/server/api.go apps/kittypaw/*/*test.go
git commit -m "feat(kittypaw): expose runtime admission status"
```

---

### Task 7: Verification

- [ ] **Step 1: Format**

Run:

```bash
gofmt -w $(rg --files apps/kittypaw/core apps/kittypaw/engine apps/kittypaw/server -g '*.go')
```

- [ ] **Step 2: Full package tests**

Run:

```bash
go test ./apps/kittypaw/...
```

Expected: all packages pass.

- [ ] **Step 3: Whitespace check**

Run:

```bash
git diff --check
```

Expected: no output.

- [ ] **Step 4: Final commit**

If previous tasks were not committed one-by-one, create one final commit:

```bash
git add apps/kittypaw/core apps/kittypaw/engine apps/kittypaw/server
git commit -m "feat(kittypaw): add account runtime admission control"
```

---

## Scope Notes

- Do not implement cross-process admission in this phase. The current product assumption is one local daemon per account DB.
- Do not copy OpenClaw `AgentDef.max_concurrency` directly. Staff-level concurrency can be added later on top of `RuntimeAdmission`.
- Do not make channel dispatch less scoped than it is today. Existing channel source queues stay; account admission sits underneath them.
- Daily token hard reservation is intentionally listed as high priority in `candidates.md`, but it should be a follow-up after this limiter lands because it needs provider usage estimation/reservation semantics.
