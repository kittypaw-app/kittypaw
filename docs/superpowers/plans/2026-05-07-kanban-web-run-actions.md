# Kanban Web Run Actions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Web drawer controls for durable Kanban Run heartbeat, cancel, and reclaim actions.

**Architecture:** Reuse the existing Web Kanban module and `_taskAction` request flow. Add static tests first, then render buttons, bind handlers, post to existing API routes, and enrich Run history timestamps.

**Tech Stack:** Go static-source tests, vanilla JavaScript, existing KittyPaw Web CSS.

---

## File Map

- Modify `apps/kittypaw/server/web_kanban_test.go`
  - Pin the new button IDs, lifecycle endpoints, prompt methods, and timestamp fields.
- Modify `apps/kittypaw/server/web/kanban.js`
  - Render `Heartbeat`, `Cancel`, and `Reclaim` buttons.
  - Bind click handlers.
  - Add `_heartbeatTask`, `_cancelTask`, and `_reclaimTask`.
  - Render Run timestamps.
- Modify `apps/kittypaw/server/web/style.css`
  - Add a small `.kanban-run-time` style if timestamp markup needs it.

---

### Task 1: Web Static Tests

**Files:**
- Modify: `apps/kittypaw/server/web_kanban_test.go`

- [ ] **Step 1: Add failing Web lifecycle test**

Add this test after `TestKanbanWebModuleSupportsCreateDetailActionsAndRuns`:

```go
func TestKanbanWebModuleSupportsRunLifecycleActions(t *testing.T) {
	src := readWebAssetForKanbanTest(t, "web/kanban.js")
	for _, token := range []string{
		"id=\"kanban-heartbeat-task\"",
		"id=\"kanban-cancel-task\"",
		"id=\"kanban-reclaim-task\"",
		"/heartbeat'",
		"/cancel'",
		"/reclaim'",
		"_heartbeatTask",
		"_cancelTask",
		"_reclaimTask",
		"prompt('Cancel reason')",
		"prompt('Reclaim reason')",
		"metadata: { source: 'web' }",
		"run.started_at",
		"run.heartbeat_at",
		"run.finished_at",
	} {
		if !strings.Contains(src, token) {
			t.Fatalf("kanban module missing run lifecycle token %s", token)
		}
	}
}
```

- [ ] **Step 2: Add CSS hook assertion**

Extend `TestKanbanWebStylesProvideBoardDrawerAndResponsiveRules` with:

```go
".kanban-run-time",
```

- [ ] **Step 3: Run tests to verify RED**

Run:

```bash
cd apps/kittypaw
go test ./server -run 'TestKanbanWeb' -count=1
```

Expected: fail because the new button IDs, methods, endpoint tokens, timestamp tokens, and CSS hook are not present.

---

### Task 2: Web Implementation

**Files:**
- Modify: `apps/kittypaw/server/web/kanban.js`
- Modify: `apps/kittypaw/server/web/style.css`

- [ ] **Step 1: Render action buttons**

Update `_actionRowHTML()` to include the new buttons after `Claim`:

```js
'<button class="btn btn--secondary btn--sm" id="kanban-heartbeat-task" type="button">Heartbeat</button>' +
'<button class="btn btn--ghost btn--sm" id="kanban-cancel-task" type="button">Cancel</button>' +
'<button class="btn btn--ghost btn--sm" id="kanban-reclaim-task" type="button">Reclaim</button>' +
```

- [ ] **Step 2: Bind button handlers**

Add these bindings in `_bindEvents()` after the claim binding:

```js
const heartbeat = document.getElementById('kanban-heartbeat-task');
if (heartbeat) heartbeat.addEventListener('click', () => this._heartbeatTask());

const cancel = document.getElementById('kanban-cancel-task');
if (cancel) cancel.addEventListener('click', () => this._cancelTask());

const reclaim = document.getElementById('kanban-reclaim-task');
if (reclaim) reclaim.addEventListener('click', () => this._reclaimTask());
```

- [ ] **Step 3: Add task action methods**

Add these methods near `_claimTask()`:

```js
async _heartbeatTask() {
  await this._taskAction('/heartbeat', { actor: 'web' });
},

async _cancelTask() {
  const reason = prompt('Cancel reason');
  if (!reason) return;
  await this._taskAction('/cancel', {
    actor: 'web',
    reason: reason,
    metadata: { source: 'web' },
  });
},

async _reclaimTask() {
  const reason = prompt('Reclaim reason');
  if (!reason) return;
  await this._taskAction('/reclaim', {
    actor: 'web',
    reason: reason,
    metadata: { source: 'web' },
  });
},
```

- [ ] **Step 4: Render Run timestamps**

Inside `_runsHTML()`, after work dir rendering and before summary rendering, add:

```js
const runTimes = [];
if (run.started_at) runTimes.push('started ' + run.started_at);
if (run.heartbeat_at) runTimes.push('heartbeat ' + run.heartbeat_at);
if (run.finished_at) runTimes.push('finished ' + run.finished_at);
if (runTimes.length) html += '<span class="kanban-run-time">' + esc(runTimes.join(' | ')) + '</span>';
```

- [ ] **Step 5: Add CSS**

Add this near the other Kanban list styles:

```css
.kanban-run-time {
  font-size: 11px;
  color: #475569;
}
```

- [ ] **Step 6: Run syntax and focused tests**

Run:

```bash
cd apps/kittypaw
node --check server/web/kanban.js
go test ./server -run 'TestKanbanWeb' -count=1
```

Expected: both pass.

- [ ] **Step 7: Commit Web implementation**

Run:

```bash
git add apps/kittypaw/server/web_kanban_test.go apps/kittypaw/server/web/kanban.js apps/kittypaw/server/web/style.css
git commit -m "feat(web): add kanban run actions"
```

---

### Task 3: Review And Verification

**Files:**
- Review all changed files.

- [ ] **Step 1: Run focused verification**

Run:

```bash
cd apps/kittypaw
node --check server/web/kanban.js
go test ./server -run 'TestWeb.*Kanban|TestKanbanWeb' -count=1
```

Expected: pass.

- [ ] **Step 2: Review diff locally**

Run:

```bash
git diff --stat main...HEAD
git diff main...HEAD -- apps/kittypaw/server/web_kanban_test.go apps/kittypaw/server/web/kanban.js apps/kittypaw/server/web/style.css
```

Check:

- no product-facing use of the reserved Git working-directory term;
- the Web module calls existing API routes through `_taskAction`;
- cancel and reclaim require a prompt reason;
- reclaim does not supply `work_dir`;
- Run history renders heartbeat and finished timestamps.

- [ ] **Step 3: Run full verification**

Run:

```bash
cd apps/kittypaw
go test ./... -short -count=1
```

Expected: pass.

- [ ] **Step 4: Commit any review fixes**

If review finds issues, fix them with a focused test first, then commit:

```bash
git add <changed-files>
git commit -m "fix: tighten kanban web run actions"
```

- [ ] **Step 5: Final status**

Run:

```bash
git status --short --branch
git log --oneline --max-count=8 main..HEAD
```

Expected: clean branch with design, plan, and Web implementation commits.
