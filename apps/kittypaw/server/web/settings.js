// KittyPaw Settings Panel — Channel & LLM status

const Settings = {
  _selectedWorkspacePath: '',

  mount(container) {
    container.innerHTML = `
      <div class="settings-view">
        <h1>Settings</h1>
        <div id="settings-content" class="settings-content">
          <p class="hint">Loading...</p>
        </div>
      </div>`;
    this._load(document.getElementById('settings-content'));
  },

  async _load(container) {
    try {
      const [s, workspaces] = await Promise.all([
        apiRaw('/api/setup/status'),
        apiRaw('/api/settings/workspaces'),
      ]);
      container.innerHTML = '';

      // --- Workspaces section ---
      const wsSection = document.createElement('section');
      wsSection.className = 'settings-section';
      wsSection.innerHTML = '<h2>Workspaces</h2>';
      if (workspaces.length) {
        workspaces.forEach(ws => wsSection.appendChild(this._workspaceRow(ws, container)));
      } else {
        const emptyRow = document.createElement('div');
        emptyRow.className = 'settings-row';
        emptyRow.innerHTML = `
          <div class="settings-row-icon"></div>
          <div class="settings-row-body">
            <div class="settings-row-title">No workspaces</div>
            <div class="settings-row-sub">Not connected</div>
          </div>`;
        wsSection.appendChild(emptyRow);
      }
      wsSection.appendChild(this._actionButton('Add Workspace', () => this._showWorkspaceForm(container)));
      container.appendChild(wsSection);

      // --- Channels section ---
      const chSection = document.createElement('section');
      chSection.className = 'settings-section';
      chSection.innerHTML = '<h2>Channels</h2>';

      const telegramRow = this._channelRow(
        'Telegram',
        s.has_telegram,
        s.has_telegram ? `Chat ID: ${esc(s.telegram_chat_id || '')}` : null,
      );
      telegramRow.appendChild(this._actionButton(s.has_telegram ? 'Change' : 'Connect', () => this._showTelegramForm(container, s)));
      chSection.appendChild(telegramRow);

      chSection.appendChild(this._channelRow(
        'KakaoTalk',
        s.has_kakao,
        s.kakao_available ? null : 'Relay not available',
      ));

      container.appendChild(chSection);

      // --- LLM section ---
      const llmSection = document.createElement('section');
      llmSection.className = 'settings-section';
      llmSection.innerHTML = '<h2>LLM Provider</h2>';
      const llmRow = document.createElement('div');
      llmRow.className = 'settings-row';
      if (s.existing_provider) {
        llmRow.innerHTML = `
          <div class="settings-row-icon connected"></div>
          <div class="settings-row-body">
            <div class="settings-row-title">${esc(s.existing_provider)}</div>
            <div class="settings-row-sub">Connected</div>
          </div>`;
      } else {
        llmRow.innerHTML = `
          <div class="settings-row-icon"></div>
          <div class="settings-row-body">
            <div class="settings-row-title">Not configured</div>
            <div class="settings-row-sub">Not connected</div>
          </div>`;
      }
      llmSection.appendChild(llmRow);
      llmRow.appendChild(this._actionButton(s.existing_provider ? 'Change' : 'Connect', () => this._showLLMForm(container, s)));
      container.appendChild(llmSection);
    } catch (e) {
      container.innerHTML = `<div class="error-box">Failed to load settings: ${esc(String(e))}</div>`;
    }
  },

  _channelRow(name, connected, detail) {
    const row = document.createElement('div');
    row.className = 'settings-row';
    const statusClass = connected ? 'connected' : '';
    const statusText = connected ? 'Connected' : 'Not connected';
    const detailHtml = detail ? `<span class="settings-row-detail">${esc(detail)}</span>` : '';
    row.innerHTML = `
      <div class="settings-row-icon ${statusClass}"></div>
      <div class="settings-row-body">
        <div class="settings-row-title">${esc(name)} ${detailHtml}</div>
        <div class="settings-row-sub">${statusText}</div>
      </div>`;
    return row;
  },

  _workspaceRow(ws, container) {
    const row = document.createElement('div');
    row.className = 'settings-row';
    row.innerHTML = `
      <div class="settings-row-icon connected"></div>
      <div class="settings-row-body">
        <div class="settings-row-title">${esc(ws.alias || ws.name || 'Workspace')}</div>
        <div class="settings-row-sub settings-row-path">${esc(ws.root_path || '')}</div>
      </div>`;
    row.appendChild(this._actionButton('Remove', async () => {
      if (!window.confirm(`Remove workspace "${ws.alias || ws.name || ws.root_path}"?`)) return;
      try {
        await this._deleteJSON(`/api/settings/workspaces/${encodeURIComponent(ws.id)}`);
        await this._load(container);
      } catch (e) {
        window.alert(String(e.message || e));
      }
    }));
    return row;
  },

  _actionButton(label, onClick) {
    const btn = document.createElement('button');
    btn.className = 'btn btn--ghost btn--sm';
    btn.textContent = label;
    btn.onclick = onClick;
    return btn;
  },

  _showWorkspaceForm(container) {
    this._selectedWorkspacePath = '';
    container.innerHTML = `
      <section class="settings-section">
        <h2>Workspace</h2>
        <div class="settings-form">
          <label>Alias</label>
          <input class="input" id="settings-workspace-alias" autocomplete="off">
          <label>Path</label>
          <div class="settings-dir-picker">
            <div class="settings-dir-toolbar">
              <button class="btn btn--ghost btn--sm" id="settings-directory-parent" type="button">Up</button>
              <div class="settings-dir-path" id="settings-workspace-path"></div>
            </div>
            <div class="settings-dir-list" id="settings-directory-list"></div>
          </div>
          <div class="settings-actions">
            <button class="btn btn--primary btn--sm" id="settings-workspace-save">Save</button>
            <button class="btn btn--ghost btn--sm" id="settings-back">Cancel</button>
          </div>
          <div class="error-box mt-12" id="settings-form-error" hidden></div>
        </div>
      </section>`;
    document.getElementById('settings-back').onclick = () => this._load(container);
    this._loadDirectoryPicker('');
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
  },

  async _loadDirectoryPicker(path) {
    const list = document.getElementById('settings-directory-list');
    const current = document.getElementById('settings-workspace-path');
    const parentButton = document.getElementById('settings-directory-parent');
    const error = document.getElementById('settings-form-error');
    if (!list || !current || !parentButton) return;
    list.innerHTML = '<div class="settings-dir-empty">Loading...</div>';
    parentButton.disabled = true;
    if (error) error.hidden = true;
    try {
      const suffix = path ? `?path=${encodeURIComponent(path)}` : '';
      const data = await apiRaw(`/api/settings/directories${suffix}`);
      this._selectedWorkspacePath = data.path || '';
      current.textContent = this._selectedWorkspacePath;
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
      list.innerHTML = '';
      if (error) {
        error.textContent = String(e.message || e);
        error.hidden = false;
      }
    }
  },

  _showLLMForm(container, status) {
    const provider = status.existing_provider || 'anthropic';
    container.innerHTML = `
      <section class="settings-section">
        <h2>LLM Provider</h2>
        <div class="settings-form">
          <label>Provider</label>
          <select class="input" id="settings-llm-provider">
            <option value="anthropic">Anthropic</option>
            <option value="openai">OpenAI</option>
            <option value="gemini">Gemini</option>
            <option value="openrouter">OpenRouter</option>
            <option value="local">Local</option>
          </select>
          <label>API Key</label>
          <input class="input input--mono" id="settings-llm-api-key" type="password" autocomplete="off">
          <label>Model</label>
          <input class="input input--mono" id="settings-llm-model">
          <label>Local URL</label>
          <input class="input input--mono" id="settings-llm-local-url" value="http://localhost:11434/v1">
          <div class="settings-actions">
            <button class="btn btn--primary btn--sm" id="settings-llm-save">Save</button>
            <button class="btn btn--ghost btn--sm" id="settings-back">Cancel</button>
          </div>
          <div class="error-box mt-12" id="settings-form-error" hidden></div>
        </div>
      </section>`;
    document.getElementById('settings-llm-provider').value = this._providerValue(provider);
    document.getElementById('settings-back').onclick = () => this._load(container);
    document.getElementById('settings-llm-save').onclick = async () => {
      const button = document.getElementById('settings-llm-save');
      const error = document.getElementById('settings-form-error');
      button.disabled = true;
      error.hidden = true;
      const selected = document.getElementById('settings-llm-provider').value;
      const model = document.getElementById('settings-llm-model').value.trim();
      try {
        await this._postJSON('/api/settings/llm', {
          provider: selected,
          api_key: document.getElementById('settings-llm-api-key').value,
          model,
          local_model: model,
          local_url: document.getElementById('settings-llm-local-url').value.trim(),
        });
        await this._load(container);
      } catch (e) {
        error.textContent = String(e.message || e);
        error.hidden = false;
      } finally {
        button.disabled = false;
      }
    };
  },

  _showTelegramForm(container, status) {
    const chatID = (status.telegram_chat_id || '').includes('*') ? '' : (status.telegram_chat_id || '');
    container.innerHTML = `
      <section class="settings-section">
        <h2>Telegram</h2>
        <div class="settings-form">
          <label>Bot Token</label>
          <input class="input input--mono" id="settings-telegram-token" type="password" autocomplete="off">
          <label>Chat ID</label>
          <input class="input input--mono" id="settings-telegram-chat-id" value="${esc(chatID)}">
          <div class="settings-actions">
            <button class="btn btn--secondary btn--sm" id="settings-telegram-detect">Detect Chat ID</button>
            <button class="btn btn--primary btn--sm" id="settings-telegram-save">Save</button>
            <button class="btn btn--ghost btn--sm" id="settings-back">Cancel</button>
          </div>
          <div class="error-box mt-12" id="settings-form-error" hidden></div>
        </div>
      </section>`;
    document.getElementById('settings-back').onclick = () => this._load(container);
    document.getElementById('settings-telegram-detect').onclick = async () => {
      const error = document.getElementById('settings-form-error');
      error.hidden = true;
      try {
        const res = await this._postJSON('/api/settings/telegram/chat-id', {
          token: document.getElementById('settings-telegram-token').value,
        });
        document.getElementById('settings-telegram-chat-id').value = res.chat_id || '';
      } catch (e) {
        error.textContent = String(e.message || e);
        error.hidden = false;
      }
    };
    document.getElementById('settings-telegram-save').onclick = async () => {
      const button = document.getElementById('settings-telegram-save');
      const error = document.getElementById('settings-form-error');
      button.disabled = true;
      error.hidden = true;
      try {
        await this._postJSON('/api/settings/telegram', {
          bot_token: document.getElementById('settings-telegram-token').value,
          chat_id: document.getElementById('settings-telegram-chat-id').value,
        });
        await this._load(container);
      } catch (e) {
        error.textContent = String(e.message || e);
        error.hidden = false;
      } finally {
        button.disabled = false;
      }
    };
  },

  _providerValue(provider) {
    if (provider === 'gemini') return 'gemini';
    if (provider === 'openrouter') return 'openrouter';
    if (provider === 'openai') return 'openai';
    if (provider === 'local') return 'local';
    return 'anthropic';
  },

  async _postJSON(url, body) {
    return apiRaw(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
  },

  async _deleteJSON(url) {
    return apiRaw(url, { method: 'DELETE' });
  },
};
