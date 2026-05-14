// KittyPaw Memory Management

function memoryT(key, params, fallback) {
  const runtime = typeof window !== 'undefined' ? window.KittyPawI18n : null;
  const value = runtime && typeof runtime.t === 'function' ? runtime.t(key, params) : key;
  return value === key && fallback ? fallback : value;
}

const Memory = {
  _container: null,
  _query: '',
  _memory: [],
  _pending: [],
  _suggestions: [],
  _error: '',
  _notice: '',
  _loading: false,
  _debounce: null,

  mount(container) {
    this._container = container;
    this._query = '';
    this._memory = [];
    this._pending = [];
    this._suggestions = [];
    this._error = '';
    this._notice = '';
    this._loading = false;
    this._render();
    this._load();
  },

  async _load() {
    this._loading = true;
    this._error = '';
    this._render();
    try {
      const memoryURL = this._query
        ? '/api/v1/memory/search?q=' + encodeURIComponent(this._query) + '&limit=100'
        : '/api/v1/memory?limit=100';
      const [memoryData, pendingData, curationData] = await Promise.all([
        api(memoryURL),
        api('/api/v1/memory/pending?limit=100'),
        api('/api/v1/memory/curate?limit=50'),
      ]);
      this._memory = this._query ? (memoryData.results || []) : (memoryData.memory || []);
      this._pending = pendingData.pending || [];
      this._suggestions = curationData.candidates || [];
    } catch (e) {
      this._error = e.message || String(e);
    } finally {
      this._loading = false;
      this._render();
    }
  },

  _render() {
    if (!this._container) return;
    this._container.innerHTML = `
      <div class="memory-view">
        <div class="memory-header">
          <h1>${esc(memoryT('memory.title', null, 'Memory'))}</h1>
          <div class="memory-header-actions">
            <button class="btn btn--secondary btn--sm" id="memory-export">${esc(memoryT('memory.export', null, 'Export JSON'))}</button>
            <button class="btn btn--ghost btn--sm" id="memory-forget-all">${esc(memoryT('memory.forgetAll', null, 'Forget All'))}</button>
          </div>
        </div>
        <div class="memory-toolbar">
          <input class="input" id="memory-search" value="${escHTMLAttr(this._query)}" placeholder="${escHTMLAttr(memoryT('memory.search', null, 'Search memory'))}">
          <button class="btn btn--primary btn--sm" id="memory-refresh">${esc(memoryT('common.refresh', null, 'Refresh'))}</button>
        </div>
        ${this._notice ? `<div class="info-box memory-message">${esc(this._notice)}</div>` : ''}
        ${this._error ? `<div class="error-box memory-message">${esc(this._error)}</div>` : ''}
        <section class="memory-section">
          <div class="memory-section-heading">
            <h2>${esc(memoryT('memory.suggestions', null, 'Suggestions'))}</h2>
            <span class="memory-count">${esc(String(this._suggestions.length))}</span>
          </div>
          ${this._suggestionsTable()}
        </section>
        <section class="memory-section">
          <div class="memory-section-heading">
            <h2>${esc(memoryT('memory.pending', null, 'Pending'))}</h2>
            <span class="memory-count">${esc(String(this._pending.length))}</span>
          </div>
          ${this._pendingTable()}
        </section>
        <section class="memory-section">
          <div class="memory-section-heading">
            <h2>${esc(memoryT('memory.saved', null, 'Saved'))}</h2>
            <span class="memory-count">${esc(String(this._memory.length))}</span>
          </div>
          ${this._memoryTable()}
        </section>
      </div>`;
    this._bind();
  },

  _suggestionsTable() {
    if (this._loading && this._suggestions.length === 0) {
      return `<div class="memory-empty">${esc(memoryT('common.loading', null, 'Loading...'))}</div>`;
    }
    if (this._suggestions.length === 0) {
      return `<div class="memory-empty">${esc(memoryT('memory.noSuggestions', null, 'No cleanup suggestions.'))}</div>`;
    }
    return `
      <div class="memory-table-wrap">
        <table class="memory-table">
          <thead><tr><th>${esc(memoryT('memory.type', null, 'Type'))}</th><th>${esc(memoryT('memory.action', null, 'Action'))}</th><th>${esc(memoryT('memory.scope', null, 'Scope'))}</th><th>${esc(memoryT('memory.summary', null, 'Summary'))}</th><th></th></tr></thead>
          <tbody>
            ${this._suggestions.map(row => `
              <tr>
                <td>${esc(row.type || '')}</td>
                <td>${esc(row.applyable ? (row.action || '') : memoryT('memory.reviewOnly', null, 'Review'))}</td>
                <td>${esc(this._scopeLabel(row))}</td>
                <td>${esc(row.summary || '')}</td>
                <td class="memory-row-actions">
                  ${row.applyable ? `<button class="btn btn--primary btn--sm" data-memory-curate-apply="${escHTMLAttr(row.id || '')}">${esc(memoryT('memory.apply', null, 'Apply'))}</button>` : ''}
                </td>
              </tr>
            `).join('')}
          </tbody>
        </table>
      </div>`;
  },

  _pendingTable() {
    if (this._loading && this._pending.length === 0) {
      return `<div class="memory-empty">${esc(memoryT('common.loading', null, 'Loading...'))}</div>`;
    }
    if (this._pending.length === 0) {
      return `<div class="memory-empty">${esc(memoryT('memory.noPending', null, 'No pending memory.'))}</div>`;
    }
    return `
      <div class="memory-table-wrap">
        <table class="memory-table">
          <thead><tr><th>${esc(memoryT('memory.key', null, 'Key'))}</th><th>${esc(memoryT('memory.reason', null, 'Reason'))}</th><th>${esc(memoryT('memory.scope', null, 'Scope'))}</th><th>${esc(memoryT('memory.value', null, 'Value'))}</th><th></th></tr></thead>
          <tbody>
            ${this._pending.map(row => `
              <tr>
                <td class="memory-key">${esc(row.key || '')}</td>
                <td>${esc(row.reason || '')}</td>
                <td>${esc(this._scopeLabel(row))}</td>
                <td>${esc(row.value || '')}</td>
                <td class="memory-row-actions">
                  <button class="btn btn--primary btn--sm" data-memory-confirm="${escHTMLAttr(row.id)}">${esc(memoryT('memory.confirm', null, 'Confirm'))}</button>
                  <button class="btn btn--ghost btn--sm" data-memory-reject="${escHTMLAttr(row.id)}">${esc(memoryT('memory.reject', null, 'Reject'))}</button>
                </td>
              </tr>
            `).join('')}
          </tbody>
        </table>
      </div>`;
  },

  _memoryTable() {
    if (this._loading && this._memory.length === 0) {
      return `<div class="memory-empty">${esc(memoryT('common.loading', null, 'Loading...'))}</div>`;
    }
    if (this._memory.length === 0) {
      return `<div class="memory-empty">${esc(memoryT('memory.noSaved', null, 'No saved memory.'))}</div>`;
    }
    return `
      <div class="memory-table-wrap">
        <table class="memory-table">
          <thead><tr><th>${esc(memoryT('memory.key', null, 'Key'))}</th><th>${esc(memoryT('memory.kind', null, 'Kind'))}</th><th>${esc(memoryT('memory.scope', null, 'Scope'))}</th><th>${esc(memoryT('memory.updated', null, 'Updated'))}</th><th>${esc(memoryT('memory.value', null, 'Value'))}</th><th></th></tr></thead>
          <tbody>
            ${this._memory.map(row => `
              <tr>
                <td class="memory-key">${esc(row.key || '')}</td>
                <td>${esc(row.kind || '')}</td>
                <td>${esc(this._scopeLabel(row))}</td>
                <td>${esc((row.updated_at || row.created_at || '').slice(0, 19))}</td>
                <td>${esc(row.value || '')}</td>
                <td class="memory-row-actions">
                  <button class="btn btn--ghost btn--sm" data-memory-delete="${escHTMLAttr(row.key || '')}" data-memory-scope-type="${escHTMLAttr(row.scope_type || '')}" data-memory-scope-id="${escHTMLAttr(row.scope_id || '')}">${esc(memoryT('common.delete', null, 'Delete'))}</button>
                </td>
              </tr>
            `).join('')}
          </tbody>
        </table>
      </div>`;
  },

  _bind() {
    const search = document.getElementById('memory-search');
    if (search) {
      search.addEventListener('input', () => {
        clearTimeout(this._debounce);
        this._debounce = setTimeout(() => {
          this._query = search.value.trim();
          this._load();
        }, 250);
      });
    }
    const refresh = document.getElementById('memory-refresh');
    if (refresh) refresh.onclick = () => this._load();
    const exportButton = document.getElementById('memory-export');
    if (exportButton) exportButton.onclick = () => this._export();
    const forgetAll = document.getElementById('memory-forget-all');
    if (forgetAll) forgetAll.onclick = () => this._forgetAll();

    this._container.querySelectorAll('[data-memory-delete]').forEach(button => {
      button.addEventListener('click', () => this._deleteMemory(
        button.dataset.memoryDelete || '',
        button.dataset.memoryScopeType || '',
        button.dataset.memoryScopeId || '',
      ));
    });
    this._container.querySelectorAll('[data-memory-confirm]').forEach(button => {
      button.addEventListener('click', () => this._confirmPending(button.dataset.memoryConfirm || ''));
    });
    this._container.querySelectorAll('[data-memory-reject]').forEach(button => {
      button.addEventListener('click', () => this._rejectPending(button.dataset.memoryReject || ''));
    });
    this._container.querySelectorAll('[data-memory-curate-apply]').forEach(button => {
      button.addEventListener('click', () => this._applySuggestion(button.dataset.memoryCurateApply || ''));
    });
  },

  async _deleteMemory(key, scopeType = '', scopeID = '') {
    if (!key) return;
    this._notice = '';
    this._error = '';
    try {
      const params = new URLSearchParams();
      if (scopeType) params.set('scope_type', scopeType);
      if (scopeID) params.set('scope_id', scopeID);
      const query = params.toString();
      await api('/api/v1/memory/' + encodeURIComponent(key) + (query ? '?' + query : ''), { method: 'DELETE' });
      this._notice = memoryT('memory.deleted', null, 'Deleted.');
      await this._load();
    } catch (e) {
      this._error = e.message || String(e);
      this._render();
    }
  },

  async _confirmPending(id) {
    const n = Number(id);
    if (!Number.isFinite(n) || n <= 0) return;
    this._notice = '';
    this._error = '';
    try {
      await api('/api/v1/memory/pending/' + encodeURIComponent(String(n)) + '/confirm', { method: 'POST' });
      this._notice = memoryT('memory.confirmed', null, 'Confirmed.');
      await this._load();
    } catch (e) {
      this._error = e.message || String(e);
      this._render();
    }
  },

  async _rejectPending(id) {
    const n = Number(id);
    if (!Number.isFinite(n) || n <= 0) return;
    this._notice = '';
    this._error = '';
    try {
      await api('/api/v1/memory/pending/' + encodeURIComponent(String(n)) + '/reject', { method: 'POST' });
      this._notice = memoryT('memory.rejected', null, 'Rejected.');
      await this._load();
    } catch (e) {
      this._error = e.message || String(e);
      this._render();
    }
  },

  async _applySuggestion(id) {
    id = String(id || '').trim();
    if (!id) return;
    this._notice = '';
    this._error = '';
    try {
      await api('/api/v1/memory/curate/' + encodeURIComponent(id) + '/apply', { method: 'POST' });
      this._notice = memoryT('memory.suggestionApplied', null, 'Suggestion applied.');
      await this._load();
    } catch (e) {
      this._error = e.message || String(e);
      this._render();
    }
  },

  async _forgetAll() {
    if (!window.confirm(memoryT('memory.confirmForgetAll', null, 'Delete all prompt-safe memory?'))) return;
    this._notice = '';
    this._error = '';
    try {
      const res = await api('/api/v1/memory/forget-all', { method: 'POST' });
      this._notice = memoryT('memory.forgotAll', { count: res.deleted || 0 }, 'Deleted ' + String(res.deleted || 0) + ' memories.');
      await this._load();
    } catch (e) {
      this._error = e.message || String(e);
      this._render();
    }
  },

  async _export() {
    this._notice = '';
    this._error = '';
    try {
      const res = await api('/api/v1/memory/export?limit=500');
      const blob = new Blob([JSON.stringify(res.memory || [], null, 2)], { type: 'application/json' });
      const url = URL.createObjectURL(blob);
      const link = document.createElement('a');
      link.href = url;
      link.download = 'kittypaw-memory.json';
      document.body.appendChild(link);
      link.click();
      link.remove();
      URL.revokeObjectURL(url);
    } catch (e) {
      this._error = e.message || String(e);
      this._render();
    }
  },

  _scopeLabel(row) {
    const type = row.scope_type || 'global';
    const id = row.scope_id || '';
    return id ? type + ':' + id : type;
  },
};
