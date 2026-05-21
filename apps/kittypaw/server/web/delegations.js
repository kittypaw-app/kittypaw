// KittyPaw Delegations Panel — background delegation tree operations

function delegationsT(key, params, fallback) {
  const runtime = window.KittyPawI18n;
  const value = runtime && typeof runtime.t === 'function' ? runtime.t(key, params) : key;
  return value === key && fallback ? fallback : value;
}

const Delegations = {
  container: null,
  conversations: [],
  recentJobs: [],
  selectedConversationID: '',
  treeRequestSeq: 0,
  loading: false,

  mount(container) {
    this.container = container;
    this.conversations = [];
    this.recentJobs = [];
    this.selectedConversationID = '';
    this.treeRequestSeq = 0;
    container.innerHTML = `
      <div class="delegations-view">
        <div class="delegations-header">
          <div>
            <h1>${esc(delegationsT('delegations.title', null, 'Delegations'))}</h1>
          </div>
          <button class="btn btn--ghost btn--sm" id="delegations-refresh" type="button">${esc(delegationsT('common.refresh', null, 'Refresh'))}</button>
        </div>
        <div class="delegations-toolbar">
          <label for="delegations-conversation">${esc(delegationsT('delegations.conversation', null, 'Conversation'))}</label>
          <select class="input input--mono" id="delegations-conversation"></select>
        </div>
        <div class="delegations-summary" id="delegations-summary"></div>
        <div class="delegations-tree" id="delegations-tree"></div>
        <div class="error-box mt-12" id="delegations-error" hidden></div>
      </div>`;

    document.getElementById('delegations-refresh').onclick = () => this.refresh();
    document.getElementById('delegations-conversation').onchange = (event) => {
      this.selectedConversationID = event.target.value;
      this._loadTree();
    };
    this.refresh();
  },

  async refresh() {
    if (!this.container || this.loading) return;
    this.loading = true;
    this._setError('');
    try {
      const [conversationsData, delegationsData] = await Promise.all([
        api('/api/v1/conversations?limit=50').catch(() => ({ conversations: [] })),
        api('/api/v1/delegations?limit=50').catch(() => ({ delegations: [] })),
      ]);
      this.conversations = Array.isArray(conversationsData.conversations) ? conversationsData.conversations : [];
      this.recentJobs = Array.isArray(delegationsData.delegations) ? delegationsData.delegations : [];
      this._renderConversationOptions();
      if (this.selectedConversationID) {
        await this._loadTree();
      } else {
        this._renderEmpty(delegationsT('delegations.noConversation', null, 'No delegation conversations'));
      }
    } catch (e) {
      this._setError(e && e.message ? e.message : String(e));
    } finally {
      this.loading = false;
    }
  },

  _renderConversationOptions() {
    const select = document.getElementById('delegations-conversation');
    if (!select) return;

    const labels = new Map();
    for (const conv of this.conversations) {
      const id = conv.id || conv.ID || '';
      if (!id) continue;
      labels.set(id, this._conversationLabel(conv));
    }

    const ids = [];
    const seen = new Set();
    for (const job of this.recentJobs) {
      const id = job.parent_conversation_id || '';
      if (!id || seen.has(id)) continue;
      seen.add(id);
      ids.push(id);
      if (!labels.has(id)) labels.set(id, id);
    }
    for (const conv of this.conversations) {
      const id = conv.id || conv.ID || '';
      if (!id || seen.has(id)) continue;
      seen.add(id);
      ids.push(id);
    }

    if (ids.length === 0) {
      select.innerHTML = `<option value="">${esc(delegationsT('delegations.none', null, 'None'))}</option>`;
      this.selectedConversationID = '';
      select.disabled = true;
      return;
    }

    if (!this.selectedConversationID || !ids.includes(this.selectedConversationID)) {
      this.selectedConversationID = ids[0];
    }
    select.disabled = false;
    select.innerHTML = ids.map(id =>
      `<option value="${escHTMLAttr(id)}"${id === this.selectedConversationID ? ' selected' : ''}>${esc(labels.get(id) || id)}</option>`
    ).join('');
  },

  _conversationLabel(conv) {
    const id = conv.id || conv.ID || '';
    const title = conv.title || conv.Title || '';
    if (title && title !== 'General') return `${title} · ${id}`;
    return id;
  },

  async _loadTree() {
    const summaryEl = document.getElementById('delegations-summary');
    const treeEl = document.getElementById('delegations-tree');
    const requestedConversationID = this.selectedConversationID;
    const requestSeq = ++this.treeRequestSeq;
    if (!requestedConversationID || !summaryEl || !treeEl) return;
    summaryEl.innerHTML = '';
    treeEl.innerHTML = `<div class="delegations-empty">${esc(delegationsT('common.loading', null, 'Loading...'))}</div>`;
    this._setError('');

    try {
      const data = await api('/api/v1/delegations/tree?conversation_id=' + encodeURIComponent(requestedConversationID) + '&limit=200');
      if (requestSeq !== this.treeRequestSeq || requestedConversationID !== this.selectedConversationID) return;
      this._renderTree(data.tree || {});
    } catch (e) {
      if (requestSeq !== this.treeRequestSeq || requestedConversationID !== this.selectedConversationID) return;
      treeEl.innerHTML = '';
      this._setError(e && e.message ? e.message : String(e));
    }
  },

  _renderTree(tree) {
    const summaryEl = document.getElementById('delegations-summary');
    const treeEl = document.getElementById('delegations-tree');
    const summary = tree.summary || {};
    summaryEl.innerHTML = this._summaryHTML(summary);

    const jobs = Array.isArray(tree.jobs) ? tree.jobs : [];
    if (jobs.length === 0) {
      this._renderEmpty(delegationsT('delegations.noJobs', null, 'No delegation jobs'));
      return;
    }
    treeEl.innerHTML = jobs.map(node => this._treeNodeHTML(node, 0)).join('');
    treeEl.querySelectorAll('[data-cancel-job]').forEach(button => {
      button.addEventListener('click', () => this._cancelJob(button.dataset.cancelJob));
    });
  },

  _summaryHTML(summary) {
    const items = [
      ['total', delegationsT('delegations.total', null, 'Total')],
      ['queued', delegationsT('delegations.queued', null, 'Queued')],
      ['running', delegationsT('delegations.running', null, 'Running')],
      ['succeeded', delegationsT('delegations.succeeded', null, 'Succeeded')],
      ['failed', delegationsT('delegations.failed', null, 'Failed')],
      ['canceled', delegationsT('delegations.canceled', null, 'Canceled')],
    ];
    return items.map(([key, label]) =>
      `<span class="delegations-summary-chip"><span>${esc(label)}</span><strong>${esc(String(summary[key] || 0))}</strong></span>`
    ).join('');
  },

  _treeNodeHTML(node, depth) {
    const job = node && node.job ? node.job : {};
    const children = Array.isArray(node.children) ? node.children : [];
    const status = job.status || '';
    const canCancel = status === 'queued' || status === 'running';
    const meta = [
      job.parent_staff_id ? `from ${job.parent_staff_id}` : '',
      job.duration_ms ? `${job.duration_ms}ms` : '',
      job.token_usage ? `${job.token_usage} tokens` : '',
      job.updated_at ? String(job.updated_at).slice(0, 19) : '',
    ].filter(Boolean).join(' · ');
    const result = job.error_message || job.result || '';
    return `
      <div class="delegations-node" style="--delegation-depth:${depth}">
        <div class="delegations-node-main">
          <span class="delegations-status delegations-status--${escHTMLAttr(status)}"></span>
          <div class="delegations-node-body">
            <div class="delegations-node-title">
              <span class="delegations-staff">${esc(job.staff_id || '')}</span>
              <span class="delegations-job-id">${esc(job.id || '')}</span>
            </div>
            <div class="delegations-task">${esc(job.task || '')}</div>
            ${meta ? `<div class="delegations-meta">${esc(meta)}</div>` : ''}
            ${result ? `<div class="delegations-result ${job.error_message ? 'is-error' : ''}">${esc(result)}</div>` : ''}
          </div>
          ${canCancel ? `<button class="btn btn--ghost btn--sm delegations-cancel" type="button" data-cancel-job="${escHTMLAttr(job.id || '')}">${esc(delegationsT('common.cancel', null, 'Cancel'))}</button>` : ''}
        </div>
        ${children.length ? `<div class="delegations-node-children">${children.map(child => this._treeNodeHTML(child, depth + 1)).join('')}</div>` : ''}
      </div>`;
  },

  async _cancelJob(jobID) {
    jobID = String(jobID || '').trim();
    if (!jobID) return;
    this._setError('');
    try {
      await api('/api/v1/delegations/' + encodeURIComponent(jobID) + '/cancel', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ reason: 'canceled from web UI' }),
      });
      await this._loadTree();
    } catch (e) {
      this._setError(e && e.message ? e.message : String(e));
    }
  },

  _renderEmpty(message) {
    const summaryEl = document.getElementById('delegations-summary');
    const treeEl = document.getElementById('delegations-tree');
    if (summaryEl) summaryEl.innerHTML = '';
    if (treeEl) treeEl.innerHTML = `<div class="delegations-empty">${esc(message)}</div>`;
  },

  _setError(message) {
    const el = document.getElementById('delegations-error');
    if (!el) return;
    el.textContent = message || '';
    el.hidden = !message;
  },
};
