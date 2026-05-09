package server

import (
	"os/exec"
	"testing"
)

func runProjectsJSTest(t *testing.T, script string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available")
	}
	wrapped := projectsJSTestHarness + `
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
		t.Fatalf("projects js test failed: %v\n%s", err, out)
	}
}

const projectsJSTestHarness = `
const fs = require('fs');
const vm = require('vm');
const assert = require('assert');

const source = fs.readFileSync('web/projects.js', 'utf8');
const sandbox = {
  console,
  esc: (value) => String(value),
  escHTMLAttr: (value) => String(value),
  api: async () => { throw new Error('api not stubbed'); },
  apiRaw: async () => { throw new Error('apiRaw not stubbed'); },
};
vm.createContext(sandbox);
vm.runInContext(source + '\nglobalThis.ProjectsForTest = Projects;', sandbox);
const Projects = sandbox.ProjectsForTest;

function sameJSON(actual, expected) {
  assert.deepStrictEqual(JSON.parse(JSON.stringify(actual)), expected);
}

function makeElement(tagName) {
  const classSet = new Set();
  const el = {
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
  Object.defineProperty(el, 'innerHTML', {
    get() { return this._innerHTML || ''; },
    set(value) {
      this._innerHTML = String(value);
      if (!this._documentElements) return;
      const idPattern = /id="([^"]+)"/g;
      let match;
      while ((match = idPattern.exec(this._innerHTML)) !== null) {
        if (!this._documentElements[match[1]]) {
          this._documentElements[match[1]] = makeElement('div');
        }
      }
    },
  });
  return el;
}

function mountProjectsDocument() {
  const elements = {};
  sandbox.document = {
    createElement: makeElement,
    createDocumentFragment: () => makeElement('#fragment'),
    getElementById: (id) => elements[id] || null,
  };
  const container = makeElement('div');
  container._documentElements = elements;
  return { container, elements };
}

function mountDirectoryPickerElements() {
  const elements = {};
  for (const id of [
    'projects-directory-list',
    'projects-folder-path',
    'projects-folder-selected',
    'projects-directory-parent',
    'projects-directory-breadcrumb',
    'projects-form-error',
    'projects-project-key',
    'projects-project-name',
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

function flushPromises() {
  return new Promise((resolve) => setImmediate(resolve));
}
`

func TestProjectsFolderBreadcrumbsSupportUNCPaths(t *testing.T) {
	runProjectsJSTest(t, `
const unc = Projects._projectBreadcrumbs('\\\\server\\share\\repo');
sameJSON(unc, [
  { label: '\\\\server\\share', path: '\\\\server\\share' },
  { label: 'repo', path: '\\\\server\\share\\repo' },
]);

const slashUNC = Projects._projectBreadcrumbs('//server/share/repo');
sameJSON(slashUNC, [
  { label: '//server/share', path: '//server/share' },
  { label: 'repo', path: '//server/share/repo' },
]);

const drive = Projects._projectBreadcrumbs('C:\\Users\\jinto');
sameJSON(drive, [
  { label: 'C:', path: 'C:\\' },
  { label: 'Users', path: 'C:\\Users' },
  { label: 'jinto', path: 'C:\\Users\\jinto' },
]);
`)
}

func TestProjectsFolderPickerPreservesStateAndManualFields(t *testing.T) {
	runProjectsJSTest(t, `
const elements = mountDirectoryPickerElements();
const list = elements['projects-directory-list'];
const pathInput = elements['projects-folder-path'];
const selected = elements['projects-folder-selected'];
const error = elements['projects-form-error'];
const key = elements['projects-project-key'];
const name = elements['projects-project-name'];

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

Projects._selectedProjectPath = '';
Projects._directoryPickerRequestID = 0;
Projects._projectFieldsAuto = true;

await Projects._loadDirectoryPicker('/Users/jinto/projects');
assert.strictEqual(pathInput.value, '/Users/jinto/projects');
assert.strictEqual(selected.textContent, '/Users/jinto/projects');
assert.strictEqual(key.value, 'PROJECTS');
assert.strictEqual(name.value, 'projects');
const previousList = list.children[0];

await Projects._loadDirectoryPicker('/no/such/path');
assert.strictEqual(pathInput.value, '/Users/jinto/projects');
assert.strictEqual(selected.textContent, '/Users/jinto/projects');
assert.strictEqual(list.children[0], previousList);
assert.strictEqual(error.hidden, false);

key.value = 'CUSTOM';
name.value = 'custom name';
Projects._projectFieldsAuto = false;
await Projects._loadDirectoryPicker('/Users/jinto/projects/kittypaw');
assert.strictEqual(key.value, 'CUSTOM');
assert.strictEqual(name.value, 'custom name');
const fragment = list.children[0];
const item = fragment.children[0];
assert.strictEqual(item.children[0].textContent, 'kitty<script>');
assert.strictEqual(item.children[1].textContent, '/Users/jinto/projects/kittypaw/kitty<script>');
`)
}

func TestProjectsCreateResolvesEditedPath(t *testing.T) {
	runProjectsJSTest(t, `
const { container, elements } = mountProjectsDocument();
let savedBody = null;
const calls = [];
sandbox.apiRaw = async (url, options = {}) => {
  calls.push(url);
  if (url === '/api/settings/directories') {
    return { path: '/Users/jinto/projects', parent: '/Users/jinto', entries: [] };
  }
  if (url === '/api/settings/directories?path=%2FUsers%2Fjinto%2Fprojects%2Fkittypaw') {
    return { path: '/Users/jinto/projects/kittypaw', parent: '/Users/jinto/projects', entries: [] };
  }
  throw new Error('unexpected raw api call: ' + url);
};
sandbox.api = async (url, options = {}) => {
  calls.push(url);
  if (url === '/api/v1/projects' && options.method === 'POST') {
    savedBody = JSON.parse(options.body);
    return { project: { id: 'prj_1', key: 'KITTY', name: 'KittyPaw', root_path: savedBody.root_path } };
  }
  if (url === '/api/v1/projects') return { projects: [] };
  if (url === '/api/v1/drivers') return { drivers: [] };
  throw new Error('unexpected api call: ' + url);
};

Projects._container = container;
Projects._selectedProjectPath = '';
Projects._directoryPickerRequestID = 0;
Projects._projectFieldsAuto = true;
Projects._renderProjectForm();
await flushPromises();

elements['projects-folder-path'].value = '/Users/jinto/projects/kittypaw';
elements['projects-project-key'].value = 'KITTY';
elements['projects-project-name'].value = 'KittyPaw';
await elements['projects-project-save'].onclick();

assert.strictEqual(savedBody.root_path, '/Users/jinto/projects/kittypaw');
assert.strictEqual(savedBody.key, 'KITTY');
assert.strictEqual(savedBody.name, 'KittyPaw');
assert(calls.includes('/api/settings/directories?path=%2FUsers%2Fjinto%2Fprojects%2Fkittypaw'));
`)
}
