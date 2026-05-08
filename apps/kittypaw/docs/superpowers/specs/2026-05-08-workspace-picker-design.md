# Workspace Picker Design

## Context

The Settings workspace form currently shows a plain directory list with an `Up`
button and a long current path. It works, but it is slow to navigate and does
not make the selected folder obvious. Users who already know a path also cannot
paste it directly.

## Goals

- Make folder selection feel closer to a Finder-style picker.
- Keep the selected workspace path visible and unambiguous.
- Allow direct path paste/type plus Enter navigation.
- Preserve the existing Settings workspace API and account isolation behavior.
- Keep the first implementation focused on workspace creation, not a general
  file manager.

## Non-Goals

- No full-text filesystem search in this pass.
- No file selection; only directories are shown and selectable.
- No server-side changes unless the existing directory browse response proves
  insufficient during implementation.
- No native OS file picker dependency.

## UX Design

The workspace form keeps the alias field, but the picker becomes the main
control:

1. `Alias` remains editable. When the user navigates to a folder and the alias
   is blank, the UI suggests the selected folder name.
2. `Path` becomes an editable path input. Pressing Enter loads that path through
   `/api/settings/directories?path=...`.
3. Below the input, the picker shows a Finder-style layout:
   - breadcrumb/current path row,
   - a list of nearby directories,
   - a clear `Selected: /absolute/path` row.
4. The primary action reads `Add Workspace`, not `Save`, because the action is
   choosing a directory as a workspace root.
5. `Cancel` returns to the workspace list without changing state.

The first implementation can use a two-pane layout rather than true macOS
column view:

- Left/toolbar area: breadcrumb and current path controls.
- Main list: child folders for the current path.
- Footer: selected path and `Add Workspace` action.

This is enough to remove the current ambiguity without overbuilding a complete
Finder clone.

## Data Flow

- Initial form load calls `_loadDirectoryPicker('')`, preserving the current
  behavior of starting from the server-selected default directory.
- Directory navigation continues to call the existing
  `GET /api/settings/directories?path=<absolute-path>`.
- Successful responses update:
  - `_selectedWorkspacePath`,
  - path input value,
  - breadcrumb display,
  - child folder list,
  - selected-path footer.
- Saving posts to `POST /api/settings/workspaces` with the same payload shape:
  `{ alias, path }`.

## Error Handling

- Invalid or relative path input shows an inline form error and leaves the
  previous valid selection intact.
- Unreadable directories show an inline error from the existing API response.
- Loading state appears inside the folder list while a request is in flight.
- Save remains disabled while a save request is in flight.

## Accessibility and Layout

- Directory rows are buttons with stable height and keyboard focus styling.
- Path input supports Enter navigation.
- Breadcrumb segments are buttons.
- Text uses current Settings typography and avoids oversized controls.
- The picker has a fixed responsive height so long folder lists scroll inside
  the picker rather than pushing the action buttons off-screen.

## Testing

- Update web static tests so Settings still contains the directory browse API,
  path input, selected-path display, and workspace save action.
- Add JavaScript-level coverage if the existing test harness supports it; at
  minimum, keep server tests for `/api/settings/directories` unchanged and add
  targeted static assertions for the new UI contract.
- Manually verify in browser:
  - initial default path loads,
  - clicking a child folder navigates,
  - breadcrumb/up navigation works,
  - direct path paste plus Enter works,
  - invalid path shows an inline error,
  - workspace save returns to the workspace list.
