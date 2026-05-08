package server

import (
	"os/exec"
	"testing"
)

func runSettingsJSTest(t *testing.T, script string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available")
	}
	wrapped := settingsJSTestHarness + `
(async () => {
` + script + `
})().catch((err) => {
  console.error(err && err.stack ? err.stack : err);
  process.exit(1);
});
`
	cmd := exec.Command(node, "--input-type=commonjs", "-e", wrapped)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("settings js test failed: %v\n%s", err, out)
	}
}

const settingsJSTestHarness = `
const fs = require('fs');
const vm = require('vm');
const assert = require('assert');

const source = fs.readFileSync('web/settings.js', 'utf8');
const sandbox = {
  console,
  esc: (value) => String(value),
  apiRaw: async () => { throw new Error('apiRaw not stubbed'); },
};
vm.createContext(sandbox);
vm.runInContext(source + '\nglobalThis.SettingsForTest = Settings;', sandbox);
const Settings = sandbox.SettingsForTest;

function sameJSON(actual, expected) {
  assert.deepStrictEqual(JSON.parse(JSON.stringify(actual)), expected);
}

function makeElement(tagName) {
  const classSet = new Set();
  return {
    tagName,
    className: '',
    dataset: {},
    children: [],
    disabled: false,
    hidden: false,
    textContent: '',
    type: '',
    value: '',
    listeners: {},
    classList: {
      add: (name) => classSet.add(name),
      remove: (name) => classSet.delete(name),
      contains: (name) => classSet.has(name),
    },
    append(...children) { this.children.push(...children); },
    appendChild(child) { this.children.push(child); return child; },
    replaceChildren(...children) { this.children = children; },
    addEventListener(type, listener) { this.listeners[type] = listener; },
  };
}

function mountDirectoryPickerElements() {
  const elements = {};
  for (const id of [
    'settings-directory-list',
    'settings-workspace-path',
    'settings-workspace-selected',
    'settings-directory-parent',
    'settings-directory-breadcrumb',
    'settings-form-error',
    'settings-workspace-alias',
  ]) {
    elements[id] = makeElement('div');
  }
  sandbox.document = {
    createElement: makeElement,
    createDocumentFragment: () => makeElement('#fragment'),
    getElementById: (id) => elements[id] || null,
  };
  return elements;
}
`

func TestWebSettingsWorkspaceBreadcrumbsSupportUNCPaths(t *testing.T) {
	runSettingsJSTest(t, `
const unc = Settings._workspaceBreadcrumbs('\\\\server\\share\\repo');
sameJSON(unc, [
  { label: '\\\\server\\share', path: '\\\\server\\share' },
  { label: 'repo', path: '\\\\server\\share\\repo' },
]);

const slashUNC = Settings._workspaceBreadcrumbs('//server/share/repo');
sameJSON(slashUNC, [
  { label: '//server/share', path: '//server/share' },
  { label: 'repo', path: '//server/share/repo' },
]);

const drive = Settings._workspaceBreadcrumbs('C:\\Users\\jinto');
sameJSON(drive, [
  { label: 'C:', path: 'C:\\' },
  { label: 'Users', path: 'C:\\Users' },
  { label: 'jinto', path: 'C:\\Users\\jinto' },
]);
`)
}

func TestWebSettingsWorkspacePickerPreservesStateAndManualAlias(t *testing.T) {
	runSettingsJSTest(t, `
const elements = mountDirectoryPickerElements();
const list = elements['settings-directory-list'];
const pathInput = elements['settings-workspace-path'];
const selected = elements['settings-workspace-selected'];
const error = elements['settings-form-error'];
const alias = elements['settings-workspace-alias'];

const responses = [
  {
    path: '/Users/jinto/projects',
    parent: '/Users/jinto',
    entries: [{ name: 'kittypaw', path: '/Users/jinto/projects/kittypaw' }],
  },
  new Error('path does not exist or is not a directory'),
  {
    path: '/Users/jinto/projects/kittypaw',
    parent: '/Users/jinto/projects',
    entries: [{ name: 'kitty<script>', path: '/Users/jinto/projects/kittypaw/kitty<script>' }],
  },
];
sandbox.apiRaw = async () => {
  const response = responses.shift();
  if (response instanceof Error) throw response;
  return response;
};

Settings._selectedWorkspacePath = '';
Settings._directoryPickerRequestID = 0;
Settings._workspaceAliasAuto = true;

await Settings._loadDirectoryPicker('/Users/jinto/projects');
assert.strictEqual(pathInput.value, '/Users/jinto/projects');
assert.strictEqual(selected.textContent, '/Users/jinto/projects');
assert.strictEqual(alias.value, 'projects');
const previousList = list.children[0];

await Settings._loadDirectoryPicker('/no/such/path');
assert.strictEqual(pathInput.value, '/Users/jinto/projects');
assert.strictEqual(selected.textContent, '/Users/jinto/projects');
assert.strictEqual(list.children[0], previousList);
assert.strictEqual(error.hidden, false);

alias.value = 'custom-alias';
Settings._workspaceAliasAuto = false;
await Settings._loadDirectoryPicker('/Users/jinto/projects/kittypaw');
assert.strictEqual(alias.value, 'custom-alias');
const fragment = list.children[0];
const item = fragment.children[0];
assert.strictEqual(item.children[0].textContent, 'kitty<script>');
assert.strictEqual(item.children[1].textContent, '/Users/jinto/projects/kittypaw/kitty<script>');
`)
}
