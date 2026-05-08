# Workspace Picker Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Settings workspace directory picker with a Finder-style picker that also accepts direct path input.

**Architecture:** Keep the existing server API and account-scoped workspace flow. Update only the browser Settings module and its CSS, with static web tests documenting the new UI contract. The picker remains a directory-only browser backed by `GET /api/settings/directories`.

**Tech Stack:** Go tests for static web asset contracts, vanilla JavaScript in `server/web/settings.js`, CSS in `server/web/style.css`, existing REST endpoints in `server/api_settings.go`.

---

## File Structure

- Modify `apps/kittypaw/server/web_chat_test.go`
  - Update the Settings workspace test so direct path input is now expected.
  - Add contract checks for breadcrumb, selected-path footer, and `Add Workspace`.
- Modify `apps/kittypaw/server/web/settings.js`
  - Replace the current `Up` + list picker markup.
  - Add Enter navigation for typed/pasted paths.
  - Add breadcrumb rendering and alias auto-suggestion.
  - Keep the existing save payload `{ alias, path }`.
- Modify `apps/kittypaw/server/web/style.css`
  - Add Finder-style picker layout classes.
  - Keep controls compact, stable, and scroll-contained.

No server API or database changes are planned.

---

### Task 1: Update Static UI Contract Tests

**Files:**
- Modify: `apps/kittypaw/server/web_chat_test.go`

- [ ] **Step 1: Write the failing test**

Replace `TestWebSettingsManagesAccountWorkspaces` with this version:

```go
func TestWebSettingsManagesAccountWorkspaces(t *testing.T) {
	src, err := os.ReadFile("web/settings.js")
	if err != nil {
		t.Fatalf("read web settings: %v", err)
	}
	body := string(src)
	if !strings.Contains(body, "/api/settings/workspaces") {
		t.Fatalf("settings must use account-scoped workspace settings APIs, got:\n%s", body)
	}
	for _, token := range []string{
		"/api/settings/directories",
		`id="settings-workspace-path"`,
		`id="settings-directory-breadcrumb"`,
		`id="settings-workspace-selected"`,
		"settings-dir-body",
		"settings-dir-sidebar",
		"settings-dir-list",
		"Add Workspace",
		"keydown",
		"_workspaceBreadcrumbs",
		"_suggestWorkspaceAlias",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("settings workspace picker missing token %s, got:\n%s", token, body)
		}
	}
	if !strings.Contains(body, "Workspace") || !strings.Contains(body, "Alias") {
		t.Fatalf("settings must expose workspace alias controls, got:\n%s", body)
	}
}
```

Add a CSS contract test below it:

```go
func TestWebSettingsWorkspacePickerHasFinderStyleLayout(t *testing.T) {
	src, err := os.ReadFile("web/style.css")
	if err != nil {
		t.Fatalf("read web style: %v", err)
	}
	body := string(src)
	for _, token := range []string{
		".settings-dir-body",
		".settings-dir-sidebar",
		".settings-dir-breadcrumb",
		".settings-dir-crumb",
		".settings-dir-main",
		".settings-dir-footer",
		".settings-dir-selected-path",
		"grid-template-columns",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("settings workspace picker CSS missing token %s, got:\n%s", token, body)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./server -run 'TestWebSettings(ManagesAccountWorkspaces|WorkspacePickerHasFinderStyleLayout)' -count=1
```

Expected: FAIL because `settings.js` does not yet expose the direct path input, breadcrumb, selected-path footer, and Finder-style CSS classes.

- [ ] **Step 3: Commit failing-test checkpoint**

Do not commit failing tests alone. Continue to Task 2 before committing.

---

### Task 2: Implement Finder-Style Workspace Picker Behavior

**Files:**
- Modify: `apps/kittypaw/server/web/settings.js`

- [ ] **Step 1: Replace workspace form markup**

In `_showWorkspaceForm(container)`, replace the current `Alias`/`Path` block with this markup:

```js
container.innerHTML = `
  <section class="settings-section">
    <h2>Workspace</h2>
    <div class="settings-form settings-form--wide">
      <label>Alias</label>
      <input class="input" id="settings-workspace-alias" autocomplete="off">
      <label>Path</label>
      <input class="input input--mono" id="settings-workspace-path" autocomplete="off" spellcheck="false">
      <div class="settings-dir-picker">
        <div class="settings-dir-body">
          <div class="settings-dir-sidebar">
            <button class="btn btn--ghost btn--sm settings-dir-up" id="settings-directory-parent" type="button">Up</button>
            <div class="settings-dir-breadcrumb" id="settings-directory-breadcrumb"></div>
          </div>
          <div class="settings-dir-main">
            <div class="settings-dir-list" id="settings-directory-list"></div>
          </div>
        </div>
        <div class="settings-dir-footer">
          <span class="settings-dir-footer-label">Selected</span>
          <span class="settings-dir-selected-path" id="settings-workspace-selected"></span>
        </div>
      </div>
      <div class="settings-actions">
        <button class="btn btn--primary btn--sm" id="settings-workspace-save">Add Workspace</button>
        <button class="btn btn--ghost btn--sm" id="settings-back">Cancel</button>
      </div>
      <div class="error-box mt-12" id="settings-form-error" hidden></div>
    </div>
  </section>`;
```

- [ ] **Step 2: Add path input Enter navigation**

Still inside `_showWorkspaceForm(container)`, after `document.getElementById('settings-back').onclick = ...`, add:

```js
const pathInput = document.getElementById('settings-workspace-path');
pathInput.addEventListener('keydown', (event) => {
  if (event.key !== 'Enter') return;
  event.preventDefault();
  this._loadDirectoryPicker(pathInput.value.trim());
});
```

- [ ] **Step 3: Keep save behavior but use the selected path**

Keep the existing save handler, but make sure it still posts the selected path:

```js
document.getElementById('settings-workspace-save').onclick = async () => {
  const button = document.getElementById('settings-workspace-save');
  const error = document.getElementById('settings-form-error');
  button.disabled = true;
  error.hidden = true;
  try {
    if (!this._selectedWorkspacePath) throw new Error('Select a workspace path.');
    await this._postJSON('/api/settings/workspaces', {
      alias: document.getElementById('settings-workspace-alias').value.trim(),
      path: this._selectedWorkspacePath,
    });
    await this._load(container);
  } catch (e) {
    error.textContent = String(e.message || e);
    error.hidden = false;
  } finally {
    button.disabled = false;
  }
};
```

- [ ] **Step 4: Replace `_loadDirectoryPicker(path)`**

Replace the existing `_loadDirectoryPicker(path)` with:

```js
async _loadDirectoryPicker(path) {
  const list = document.getElementById('settings-directory-list');
  const pathInput = document.getElementById('settings-workspace-path');
  const selected = document.getElementById('settings-workspace-selected');
  const parentButton = document.getElementById('settings-directory-parent');
  const breadcrumb = document.getElementById('settings-directory-breadcrumb');
  const error = document.getElementById('settings-form-error');
  if (!list || !pathInput || !selected || !parentButton || !breadcrumb) return;

  const previousPath = this._selectedWorkspacePath;
  list.innerHTML = '<div class="settings-dir-empty">Loading...</div>';
  parentButton.disabled = true;
  if (error) error.hidden = true;
  try {
    const suffix = path ? `?path=${encodeURIComponent(path)}` : '';
    const data = await apiRaw(`/api/settings/directories${suffix}`);
    this._selectedWorkspacePath = data.path || '';
    pathInput.value = this._selectedWorkspacePath;
    selected.textContent = this._selectedWorkspacePath || 'No folder selected';
    this._renderDirectoryBreadcrumb(breadcrumb, this._selectedWorkspacePath);
    this._suggestWorkspaceAlias(this._selectedWorkspacePath);

    parentButton.disabled = !data.parent;
    parentButton.onclick = () => {
      if (data.parent) this._loadDirectoryPicker(data.parent);
    };

    const entries = Array.isArray(data.entries) ? data.entries : [];
    if (!entries.length) {
      list.innerHTML = '<div class="settings-dir-empty">No folders</div>';
      return;
    }
    list.innerHTML = entries.map(entry => `
      <button class="settings-dir-item" type="button" data-path="${esc(entry.path || '')}">
        <span class="settings-dir-name">${esc(entry.name || '')}</span>
        <span class="settings-dir-sub">${esc(entry.path || '')}</span>
      </button>`).join('');
    list.querySelectorAll('.settings-dir-item').forEach(button => {
      button.addEventListener('click', () => this._loadDirectoryPicker(button.dataset.path || ''));
    });
  } catch (e) {
    this._selectedWorkspacePath = previousPath;
    if (previousPath) {
      pathInput.value = previousPath;
      selected.textContent = previousPath;
    }
    list.innerHTML = '';
    if (error) {
      error.textContent = String(e.message || e);
      error.hidden = false;
    }
  }
},
```

- [ ] **Step 5: Add breadcrumb and alias helpers**

Add these methods immediately after `_loadDirectoryPicker(path)`:

```js
_renderDirectoryBreadcrumb(container, path) {
  const parts = this._workspaceBreadcrumbs(path);
  if (!parts.length) {
    container.innerHTML = '<span class="settings-dir-empty-inline">No path</span>';
    return;
  }
  container.innerHTML = parts.map(part => `
    <button class="settings-dir-crumb" type="button" data-path="${esc(part.path)}">${esc(part.label)}</button>
  `).join('');
  container.querySelectorAll('.settings-dir-crumb').forEach(button => {
    button.addEventListener('click', () => this._loadDirectoryPicker(button.dataset.path || ''));
  });
},

_workspaceBreadcrumbs(path) {
  const raw = String(path || '').trim();
  if (!raw) return [];
  const windowsDrive = /^[A-Za-z]:[\\/]/.test(raw);
  const separator = raw.includes('\\') && !raw.includes('/') ? '\\' : '/';
  const tokens = raw.split(/[\\/]+/).filter(Boolean);
  if (!tokens.length) return [{ label: separator, path: separator }];

  if (windowsDrive) {
    let current = tokens[0] + separator;
    const out = [{ label: tokens[0], path: current }];
    tokens.slice(1).forEach(token => {
      current = current.endsWith(separator) ? current + token : current + separator + token;
      out.push({ label: token, path: current });
    });
    return out;
  }

  let current = separator;
  const out = [{ label: separator, path: separator }];
  tokens.forEach(token => {
    current = current === separator ? separator + token : current + separator + token;
    out.push({ label: token, path: current });
  });
  return out;
},

_suggestWorkspaceAlias(path) {
  const input = document.getElementById('settings-workspace-alias');
  if (!input || input.value.trim()) return;
  const parts = String(path || '').split(/[\\/]+/).filter(Boolean);
  const last = parts[parts.length - 1] || '';
  if (last) input.value = last;
},
```

- [ ] **Step 6: Run test to verify JS contract now passes or only CSS remains**

Run:

```bash
go test ./server -run TestWebSettingsManagesAccountWorkspaces -count=1
```

Expected: PASS if all required JS tokens are present.

---

### Task 3: Add Finder-Style Picker CSS

**Files:**
- Modify: `apps/kittypaw/server/web/style.css`

- [ ] **Step 1: Add wide form modifier and picker layout CSS**

Add this after the existing `.settings-form` rule:

```css
.settings-form--wide {
  max-width: 640px;
}
```

Replace the existing directory picker CSS block from `.settings-dir-picker` through `.settings-dir-empty` with:

```css
.settings-dir-picker {
  border: 1px solid var(--card-border);
  border-radius: var(--radius-md);
  background: var(--card-bg);
  overflow: hidden;
}

.settings-dir-body {
  display: grid;
  grid-template-columns: minmax(150px, 200px) minmax(0, 1fr);
  min-height: 280px;
}

.settings-dir-sidebar {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 10px;
  border-right: 1px solid var(--card-border);
  background: var(--surface);
  min-width: 0;
}

.settings-dir-up {
  width: 100%;
}

.settings-dir-breadcrumb {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.settings-dir-crumb {
  width: 100%;
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
  font-family: 'SF Mono', 'Fira Code', monospace;
  font-size: 12px;
  padding: 7px 8px;
  text-align: left;
  overflow-wrap: anywhere;
}

.settings-dir-crumb:hover,
.settings-dir-crumb:focus {
  background: var(--card-bg);
  color: var(--text);
  outline: none;
}

.settings-dir-main {
  min-width: 0;
}

.settings-dir-list {
  max-height: 320px;
  overflow-y: auto;
}

.settings-dir-item {
  width: 100%;
  min-height: 56px;
  padding: 10px 12px;
  border: 0;
  border-bottom: 1px solid var(--card-border);
  background: transparent;
  color: var(--text);
  text-align: left;
  cursor: pointer;
}

.settings-dir-item:hover,
.settings-dir-item:focus {
  background: var(--surface);
  outline: none;
}

.settings-dir-name {
  display: block;
  font-weight: 600;
}

.settings-dir-sub {
  display: block;
  margin-top: 2px;
  font-family: 'SF Mono', 'Fira Code', monospace;
  font-size: 11px;
  color: var(--text-muted);
  overflow-wrap: anywhere;
}

.settings-dir-footer {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
  border-top: 1px solid var(--card-border);
  min-width: 0;
}

.settings-dir-footer-label {
  flex-shrink: 0;
  font-size: 11px;
  font-weight: 700;
  color: var(--text-muted);
  text-transform: uppercase;
}

.settings-dir-selected-path {
  min-width: 0;
  font-family: 'SF Mono', 'Fira Code', monospace;
  font-size: 12px;
  color: var(--text-muted);
  overflow-wrap: anywhere;
}

.settings-dir-empty {
  padding: 14px 12px;
  color: var(--text-muted);
}

.settings-dir-empty-inline {
  padding: 7px 8px;
  color: var(--text-muted);
  font-size: 12px;
}

@media (max-width: 700px) {
  .settings-dir-body {
    grid-template-columns: 1fr;
  }

  .settings-dir-sidebar {
    border-right: 0;
    border-bottom: 1px solid var(--card-border);
  }

  .settings-dir-breadcrumb {
    flex-direction: row;
    flex-wrap: wrap;
  }

  .settings-dir-crumb {
    width: auto;
  }
}
```

- [ ] **Step 2: Run CSS contract test**

Run:

```bash
go test ./server -run TestWebSettingsWorkspacePickerHasFinderStyleLayout -count=1
```

Expected: PASS.

- [ ] **Step 3: Run combined server web tests**

Run:

```bash
go test ./server -run 'TestWeb(Settings|Shell|Kanban)' -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit implementation**

Run:

```bash
git add apps/kittypaw/server/web_chat_test.go apps/kittypaw/server/web/settings.js apps/kittypaw/server/web/style.css
git commit -m "feat(web): improve workspace picker"
```

---

### Task 4: Browser Verification

**Files:**
- No source changes are part of this task. Defects found here must be fixed in
  the affected JS/CSS file and committed before final verification.

- [ ] **Step 1: Start local dev server**

Run:

```bash
make build
KITTYPAW_CONFIG_DIR="$(mktemp -d)" ./bin/kittypaw server --bind 127.0.0.1:3010
```

Expected: server listens on `127.0.0.1:3010`. If port `3010` is in use, use `3011`.

- [ ] **Step 2: Open Settings**

Open:

```text
http://127.0.0.1:3010/_settings
```

Expected: Settings loads. If the temporary config requires setup/login, use an existing local dev config instead of the temp config and do not mutate production workspaces beyond the test entry.

- [ ] **Step 3: Verify picker interactions manually**

Check:

- `Add Workspace` opens the workspace form.
- The `Path` input is editable.
- Pressing Enter in the `Path` input loads that path.
- Child folder click navigates into that folder.
- Breadcrumb segment click navigates to that segment.
- `Up` moves to the parent when available.
- `Selected` always shows the path that will be saved.
- Invalid path shows an inline error and leaves the previous selected path intact.

- [ ] **Step 4: Capture any visual regression**

If text overflows, buttons shift, or the picker pushes actions off-screen, fix CSS in `server/web/style.css`, then rerun:

```bash
go test ./server -run 'TestWebSettings' -count=1
```

- [ ] **Step 5: Commit verification fixes when Step 4 changed source**

If changes were needed:

```bash
git add apps/kittypaw/server/web/style.css apps/kittypaw/server/web/settings.js apps/kittypaw/server/web_chat_test.go
git commit -m "fix(web): polish workspace picker layout"
```

---

### Task 5: Final Verification

**Files:**
- No source changes expected.

- [ ] **Step 1: Run targeted tests**

Run:

```bash
go test ./server -run 'TestWebSettings|TestSettingsDirectories|TestSettingsWorkspaces' -count=1
```

Expected: PASS.

- [ ] **Step 2: Run app tests**

Run:

```bash
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 3: Run lint**

Run:

```bash
golangci-lint run ./...
```

Expected: `0 issues.`

- [ ] **Step 4: Build**

Run:

```bash
make build
```

Expected: `bin/kittypaw` builds successfully.

- [ ] **Step 5: Final status**

Run:

```bash
git status --short
```

Expected: clean worktree after commits.

---

## Self-Review

- Spec coverage: direct path input, Finder-style layout, selected-path display, alias suggestion, existing API reuse, error handling, responsive scroll containment, and testing are covered.
- Placeholder scan: no placeholder red flags or vague “add tests later” steps
  remain.
- Type consistency: all DOM IDs and helper names introduced in tests match the implementation steps.
