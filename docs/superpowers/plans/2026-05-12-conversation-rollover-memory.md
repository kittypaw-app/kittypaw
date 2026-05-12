# Conversation Rollover Memory Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Automatically roll over overlong general conversations into fresh child conversations, conservatively distill durable memory, and show a clear rollover notice.

**Architecture:** Add first-class route and parent metadata in the store, then resolve inbound general chat through a route before prompt construction. Length-based rollover creates a child conversation and updates the route before recording the current user turn. Distillation writes only allowlisted `memory:*` keys to `user_context`; topic-shift detection is advisory only.

**Tech Stack:** Go, SQLite migrations, existing `store.Store`, `engine.Session`, `llm.Provider`, and current conversation/checkpoint/compaction APIs.

---

### Task 1: Store Schema And Route APIs

**Files:**
- Create: `apps/kittypaw/store/migrations/031_conversation_rollover.sql`
- Modify: `apps/kittypaw/store/store.go`
- Test: `apps/kittypaw/store/store_test.go`

- [ ] **Step 1: Write failing store tests**

Add tests covering:

```go
func TestConversationRolloverMetadata(t *testing.T) {
	st := openTestStore(t)
	parent, err := st.CreateConversation(store.CreateConversationRequest{
		ScopeType: "general",
		ScopeID:   "web:old",
		Title:     "Old",
	})
	if err != nil {
		t.Fatalf("CreateConversation(parent): %v", err)
	}
	child, err := st.CreateConversation(store.CreateConversationRequest{
		ScopeType:              "general",
		ScopeID:                "web:new",
		Title:                  "New",
		ParentConversationID:   parent.ID,
		RolloverReason:         "length_turns",
		RolloverFromTurnID:     42,
		SourceChannel:          "web_chat",
		SourceSessionID:        "sess-1",
		ChatID:                 "sess-1",
	})
	if err != nil {
		t.Fatalf("CreateConversation(child): %v", err)
	}
	got, ok, err := st.Conversation(child.ID)
	if err != nil || !ok {
		t.Fatalf("Conversation(child) ok=%v err=%v", ok, err)
	}
	if got.ParentConversationID != parent.ID || got.RolloverReason != "length_turns" || got.RolloverFromTurnID != 42 {
		t.Fatalf("child metadata = %+v", got)
	}
}

func TestConversationRouteUpsertAndLookup(t *testing.T) {
	st := openTestStore(t)
	first, err := st.CreateConversation(store.CreateConversationRequest{ScopeType: "general", ScopeID: "first"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.CreateConversation(store.CreateConversationRequest{ScopeType: "general", ScopeID: "second"})
	if err != nil {
		t.Fatal(err)
	}
	route := store.ConversationRoute{
		RouteKey:        "web_chat:sess-1",
		ConversationID:  first.ID,
		SourceChannel:   "web_chat",
		SourceSessionID: "sess-1",
		ChatID:          "sess-1",
	}
	if err := st.UpsertConversationRoute(route); err != nil {
		t.Fatalf("UpsertConversationRoute(first): %v", err)
	}
	route.ConversationID = second.ID
	if err := st.UpsertConversationRoute(route); err != nil {
		t.Fatalf("UpsertConversationRoute(second): %v", err)
	}
	got, ok, err := st.ConversationRoute("web_chat:sess-1")
	if err != nil || !ok {
		t.Fatalf("ConversationRoute ok=%v err=%v", ok, err)
	}
	if got.ConversationID != second.ID || got.SourceSessionID != "sess-1" {
		t.Fatalf("route = %+v", got)
	}
}

func TestCreateRolloverConversationUpdatesOneRoute(t *testing.T) {
	st := openTestStore(t)
	parent, err := st.CreateConversation(store.CreateConversationRequest{ScopeType: "general", ScopeID: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateConversation(store.CreateConversationRequest{ScopeType: "general", ScopeID: "other"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertConversationRoute(store.ConversationRoute{RouteKey: "web_chat:sess-1", ConversationID: parent.ID}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertConversationRoute(store.ConversationRoute{RouteKey: "web_chat:sess-2", ConversationID: other.ID}); err != nil {
		t.Fatal(err)
	}
	child, err := st.CreateRolloverConversation(store.CreateRolloverConversationRequest{
		ParentConversationID: parent.ID,
		RolloverReason:       "length_turns",
		RolloverFromTurnID:   7,
		Route: store.ConversationRoute{
			RouteKey:        "web_chat:sess-1",
			SourceChannel:   "web_chat",
			SourceSessionID: "sess-1",
			ChatID:          "sess-1",
		},
	})
	if err != nil {
		t.Fatalf("CreateRolloverConversation: %v", err)
	}
	got, _, _ := st.ConversationRoute("web_chat:sess-1")
	if got.ConversationID != child.ID {
		t.Fatalf("route sess-1 = %+v, want child %s", got, child.ID)
	}
	otherRoute, _, _ := st.ConversationRoute("web_chat:sess-2")
	if otherRoute.ConversationID != other.ID {
		t.Fatalf("route sess-2 changed: %+v", otherRoute)
	}
}
```

- [ ] **Step 2: Verify RED**

Run:

```bash
go test ./apps/kittypaw/store -run 'TestConversationRolloverMetadata|TestConversationRouteUpsertAndLookup|TestCreateRolloverConversationUpdatesOneRoute' -count=1
```

Expected: compile failures for missing fields/types/methods.

- [ ] **Step 3: Add migration and store methods**

Implement:

```go
type ConversationRoute struct {
	RouteKey        string `json:"route_key"`
	ConversationID  string `json:"conversation_id"`
	SourceChannel   string `json:"source_channel,omitempty"`
	SourceSessionID string `json:"source_session_id,omitempty"`
	ChatID          string `json:"chat_id,omitempty"`
	CreatedAt       string `json:"created_at,omitempty"`
	UpdatedAt       string `json:"updated_at,omitempty"`
}

type CreateRolloverConversationRequest struct {
	ParentConversationID string
	RolloverReason       string
	RolloverFromTurnID   int64
	Route                ConversationRoute
}
```

Add `ParentConversationID`, `RolloverReason`, and `RolloverFromTurnID` to
`ConversationRecord` and `CreateConversationRequest`. Update
`CreateConversation`, `Conversation`, `ListConversations`, and
`scanConversationRecord`.

Add:

```go
func (s *Store) UpsertConversationRoute(route ConversationRoute) error
func (s *Store) ConversationRoute(routeKey string) (*ConversationRoute, bool, error)
func (s *Store) CreateRolloverConversation(req CreateRolloverConversationRequest) (*ConversationRecord, error)
func (s *Store) LatestConversationTurnID(conversationID string) (int64, error)
```

`CreateRolloverConversation` must create the child conversation and update only
the supplied route in one transaction.

- [ ] **Step 4: Verify GREEN**

Run the same store test command. Expected: pass.

### Task 2: Memory Filtering And Distillation Primitives

**Files:**
- Create: `apps/kittypaw/engine/rollover.go`
- Modify: `apps/kittypaw/store/store.go`
- Test: `apps/kittypaw/store/store_test.go`
- Test: `apps/kittypaw/engine/rollover_test.go`

- [ ] **Step 1: Write failing memory filter test**

Add a store test that writes `memory:*`, `current_project:*`,
`conversation_route:*`, `rollover_pending:*`, `pending_staff_*`, and
`active_staff:*` rows. Assert `MemoryContextLines()` includes `memory:*` rows
and excludes route/control rows.

- [ ] **Step 2: Write failing distiller tests**

Add engine tests:

```go
func TestRolloverDistillerStoresAllowedMemoryOnly(t *testing.T)
func TestRolloverDistillerIgnoresInvalidJSON(t *testing.T)
func TestRolloverMemoryKeyIsStableAndCapped(t *testing.T)
```

Use a fake `llm.Provider` returning JSON with allowed and disallowed categories.
Assert only `memory:preference:*`, `memory:decision:*`,
`memory:ongoing_task:*`, `memory:open_question:*`, and `memory:state:*` are
written with source `conversation_rollover`.

- [ ] **Step 3: Verify RED**

Run:

```bash
go test ./apps/kittypaw/store -run TestMemoryContextLines -count=1
go test ./apps/kittypaw/engine -run 'TestRolloverDistillerStoresAllowedMemoryOnly|TestRolloverDistillerIgnoresInvalidJSON|TestRolloverMemoryKeyIsStableAndCapped' -count=1
```

Expected: failures for missing distiller and current memory leakage.

- [ ] **Step 4: Implement memory filtering and distiller**

In `MemoryContextLines()`, exclude prefixes:

```text
current_project:
conversation_route:
rollover_pending:
pending_staff_
active_staff:
```

In `engine/rollover.go`, implement:

```go
type rolloverMemory struct {
	Category   string  `json:"category"`
	Key        string  `json:"key"`
	Value      string  `json:"value"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}

func distillRolloverMemory(ctx context.Context, st *store.Store, provider llm.Provider, conversationID string) error
func storeRolloverMemories(st *store.Store, memories []rolloverMemory) (int, error)
func rolloverMemoryContextKey(category, key, value string) string
```

Use an allowlist for categories, confidence threshold `0.75`, key cap `80`, and
value cap `500`. Invalid JSON returns an error but writes nothing.

- [ ] **Step 5: Verify GREEN**

Run the same memory/distiller commands. Expected: pass.

### Task 3: Route Resolution And Automatic Length Rollover

**Files:**
- Modify: `apps/kittypaw/engine/session.go`
- Modify: `apps/kittypaw/engine/rollover.go`
- Test: `apps/kittypaw/engine/rollover_test.go`

- [ ] **Step 1: Write failing engine tests**

Add tests:

```go
func TestLengthRolloverCreatesChildAndRecordsCurrentTurnThere(t *testing.T)
func TestLengthRolloverIsIdempotentForNextRun(t *testing.T)
func TestProjectConversationDoesNotAutoRollover(t *testing.T)
func TestRolloverNoticeAppearsOnce(t *testing.T)
```

Seed a general conversation with more than the test policy max turns, create a
web chat event with stable `SessionID`, run `Session.Run`, and assert:

- a child conversation has `ParentConversationID` set;
- the route points to the child;
- the new user turn is in the child, not parent;
- the assistant response contains `Conversation rolled over`;
- a second run on the same route does not create another child immediately.

For project/ticket scope, pass explicit project/ticket `ConversationID` and
assert no child is created.

- [ ] **Step 2: Verify RED**

Run:

```bash
go test ./apps/kittypaw/engine -run 'TestLengthRolloverCreatesChildAndRecordsCurrentTurnThere|TestLengthRolloverIsIdempotentForNextRun|TestProjectConversationDoesNotAutoRollover|TestRolloverNoticeAppearsOnce' -count=1
```

Expected: failures because route resolution and rollover do not exist.

- [ ] **Step 3: Implement route resolution**

Add:

```go
type rolloverPolicy struct {
	Enabled                 bool
	MaxTurns                int
	MaxEstimatedTokensRatio float64
	MinTurnsBeforeRollover  int
}

type conversationResolution struct {
	ConversationID string
	Route          *store.ConversationRoute
	RolledOver     bool
	Notice         string
}

func resolveConversationForEvent(ctx context.Context, s *Session, event *core.Event, provider llm.Provider) (conversationResolution, error)
func conversationRouteKey(eventType core.EventType, payload core.ChatPayload) (string, store.ConversationRoute)
func maybeRolloverConversation(ctx context.Context, s *Session, current string, route store.ConversationRoute, provider llm.Provider) (conversationResolution, error)
```

Use route lookup for general channel events. Explicit project/ticket/general
`conversation_id` keeps its explicit target and bypasses automatic rollover.

- [ ] **Step 4: Wire into `Session.Run` and `runAgentLoop`**

Compute the resolved conversation before slash handling and before
`loadConversationStateForRun`. Store the resolved ID in context with
`ContextWithConversationID`. Use the resolved notice to prefix final assistant
responses. Do not prefix deterministic slash command output.

- [ ] **Step 5: Verify GREEN**

Run the same engine rollover command. Expected: pass.

### Task 4: Topic-Shift Advisory And Diagnostics

**Files:**
- Modify: `apps/kittypaw/engine/rollover.go`
- Modify: `apps/kittypaw/engine/commands.go`
- Test: `apps/kittypaw/engine/rollover_test.go`
- Test: `apps/kittypaw/engine/commands_test.go`

- [ ] **Step 1: Write failing tests**

Add:

```go
func TestTopicShiftSuggestsWithoutSwitching(t *testing.T)
func TestSessionShowsRolloverMetadata(t *testing.T)
func TestContextShowsRolloverThreshold(t *testing.T)
```

Topic-shift test should use an explicit phrase such as `다른 얘기인데`.
Assert the response suggests a new conversation but the route still points to
the original conversation.

- [ ] **Step 2: Implement advisory and diagnostics**

Add a conservative detector:

```go
func topicShiftSuggestion(text string, recent []core.ConversationTurn) bool
```

Trigger only on explicit phrases in v1. Add a short suggestion to the assistant
response through prompt context or deterministic prefix without changing route.

Extend `/session` to show parent conversation, route key, and rollover reason
when available. Extend `/context` to show `rollover_max_turns`,
`rollover_min_turns`, and current turn count.

- [ ] **Step 3: Verify GREEN**

Run:

```bash
go test ./apps/kittypaw/engine -run 'TestTopicShiftSuggestsWithoutSwitching|TestSessionShowsRolloverMetadata|TestContextShowsRolloverThreshold' -count=1
```

Expected: pass.

### Task 5: API Metadata, Docs, And Full Verification

**Files:**
- Modify: `apps/kittypaw/server/api_conversations_test.go`
- Modify: `apps/kittypaw/server/api.go` if response structs need explicit fields
- Modify: `apps/kittypaw/CLAUDE.md`
- Modify: `apps/kittypaw/README.md`
- Modify: `docs/product/surfaces.md`

- [ ] **Step 1: Write or update API tests**

Assert `GET /api/v1/conversations/{id}` returns:

```json
{
  "parent_conversation_id": "general:...",
  "rollover_reason": "length_turns",
  "rollover_from_turn_id": 42
}
```

- [ ] **Step 2: Update docs**

Document:

- automatic general-conversation rollover;
- project/ticket rollover exclusion;
- conservative `memory:*` distillation;
- user-visible rollover notice.

- [ ] **Step 3: Run focused verification**

Run:

```bash
go test ./apps/kittypaw/store -run 'TestConversationRolloverMetadata|TestConversationRouteUpsertAndLookup|TestCreateRolloverConversationUpdatesOneRoute|TestMemoryContextLines' -count=1
go test ./apps/kittypaw/engine -run 'Rollover|TopicShift|SessionShowsRollover|ContextShowsRollover' -count=1
go test ./apps/kittypaw/server -run 'Conversation' -count=1
```

- [ ] **Step 4: Run full verification**

Run:

```bash
go test ./apps/kittypaw/... -timeout=180s
git diff --check
```

- [ ] **Step 5: Commit**

Commit implementation:

```bash
git add apps/kittypaw/store apps/kittypaw/engine apps/kittypaw/server apps/kittypaw/README.md apps/kittypaw/CLAUDE.md docs/product/surfaces.md
git commit -m "feat(kittypaw): add conversation rollover memory"
```
