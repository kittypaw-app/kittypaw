// KittyPaw Settings Panel — Channel & LLM status

function settingsT(key, params, fallback) {
  const runtime = typeof window !== 'undefined' ? window.KittyPawI18n : null;
  const value = runtime && typeof runtime.t === 'function' ? runtime.t(key, params) : key;
  return value === key && fallback ? fallback : value;
}

const Settings = {
  mount(container) {
    container.innerHTML = `
      <div class="settings-view">
        <h1>${esc(settingsT('settings.title', null, 'Settings'))}</h1>
        <div id="settings-content" class="settings-content">
          <p class="hint">${esc(settingsT('common.loading', null, 'Loading...'))}</p>
        </div>
      </div>`;
    this._load(document.getElementById('settings-content'));
  },

  async _load(container) {
    try {
      const s = await apiRaw('/api/setup/status');
      container.innerHTML = '';

      // --- Channels section ---
      const chSection = document.createElement('section');
      chSection.className = 'settings-section';
      chSection.innerHTML = '<h2>' + esc(settingsT('settings.channels', null, 'Channels')) + '</h2>';

      const telegramRow = this._channelRow(
        'Telegram',
        s.has_telegram,
        s.has_telegram ? `${settingsT('settings.chatID', null, 'Chat ID')}: ${s.telegram_chat_id || ''}` : null,
      );
      telegramRow.appendChild(this._actionButton(s.has_telegram ? settingsT('settings.change', null, 'Change') : settingsT('settings.connect', null, 'Connect'), () => this._showTelegramForm(container, s)));
      chSection.appendChild(telegramRow);

      chSection.appendChild(this._channelRow(
        'KakaoTalk',
        s.has_kakao,
        s.kakao_available ? null : settingsT('settings.relayNotAvailable', null, 'Relay not available'),
      ));

      container.appendChild(chSection);

      // --- LLM section ---
      const llmSection = document.createElement('section');
      llmSection.className = 'settings-section';
      llmSection.innerHTML = '<h2>' + esc(settingsT('settings.llmProvider', null, 'LLM Provider')) + '</h2>';
      const llmRow = document.createElement('div');
      llmRow.className = 'settings-row';
      if (s.existing_provider) {
        llmRow.innerHTML = `
          <div class="settings-row-icon connected"></div>
          <div class="settings-row-body">
            <div class="settings-row-title">${esc(s.existing_provider)}</div>
            <div class="settings-row-sub">${esc(settingsT('common.connected', null, 'Connected'))}</div>
          </div>`;
      } else {
        llmRow.innerHTML = `
          <div class="settings-row-icon"></div>
          <div class="settings-row-body">
            <div class="settings-row-title">${esc(settingsT('settings.notConfigured', null, 'Not configured'))}</div>
            <div class="settings-row-sub">${esc(settingsT('common.notConnected', null, 'Not connected'))}</div>
          </div>`;
      }
      llmSection.appendChild(llmRow);
      llmRow.appendChild(this._actionButton(s.existing_provider ? settingsT('settings.change', null, 'Change') : settingsT('settings.connect', null, 'Connect'), () => this._showLLMForm(container, s)));
      container.appendChild(llmSection);
    } catch (e) {
      container.innerHTML = `<div class="error-box">${esc(settingsT('settings.failedToLoad', { error: String(e) }, `Failed to load settings: ${String(e)}`))}</div>`;
    }
  },

  _channelRow(name, connected, detail) {
    const row = document.createElement('div');
    row.className = 'settings-row';
    const statusClass = connected ? 'connected' : '';
    const statusText = connected ? settingsT('common.connected', null, 'Connected') : settingsT('common.notConnected', null, 'Not connected');
    const detailHtml = detail ? `<span class="settings-row-detail">${esc(detail)}</span>` : '';
    row.innerHTML = `
      <div class="settings-row-icon ${statusClass}"></div>
      <div class="settings-row-body">
        <div class="settings-row-title">${esc(name)} ${detailHtml}</div>
        <div class="settings-row-sub">${esc(statusText)}</div>
      </div>`;
    return row;
  },

  _actionButton(label, onClick) {
    const btn = document.createElement('button');
    btn.className = 'btn btn--ghost btn--sm';
    btn.textContent = label;
    btn.onclick = onClick;
    return btn;
  },

  _showLLMForm(container, status) {
    const provider = status.existing_provider || 'anthropic';
    container.innerHTML = `
      <section class="settings-section">
        <h2>${esc(settingsT('settings.llmProvider', null, 'LLM Provider'))}</h2>
        <div class="settings-form">
          <label>${esc(settingsT('settings.provider', null, 'Provider'))}</label>
          <select class="input" id="settings-llm-provider">
            <option value="anthropic">Anthropic</option>
            <option value="openai">OpenAI</option>
            <option value="gemini">Gemini</option>
            <option value="openrouter">OpenRouter</option>
            <option value="local">Local</option>
          </select>
          <label>${esc(settingsT('settings.apiKey', null, 'API Key'))}</label>
          <input class="input input--mono" id="settings-llm-api-key" type="password" autocomplete="off">
          <label>${esc(settingsT('settings.model', null, 'Model'))}</label>
          <input class="input input--mono" id="settings-llm-model">
          <label>${esc(settingsT('settings.localURL', null, 'Local URL'))}</label>
          <input class="input input--mono" id="settings-llm-local-url" value="http://localhost:11434/v1">
          <div class="settings-actions">
            <button class="btn btn--primary btn--sm" id="settings-llm-save">${esc(settingsT('common.save', null, 'Save'))}</button>
            <button class="btn btn--ghost btn--sm" id="settings-back">${esc(settingsT('common.cancel', null, 'Cancel'))}</button>
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
          <label>${esc(settingsT('settings.botToken', null, 'Bot Token'))}</label>
          <input class="input input--mono" id="settings-telegram-token" type="password" autocomplete="off">
          <label>${esc(settingsT('settings.chatID', null, 'Chat ID'))}</label>
          <input class="input input--mono" id="settings-telegram-chat-id" value="${escHTMLAttr(chatID)}">
          <div class="settings-actions">
            <button class="btn btn--secondary btn--sm" id="settings-telegram-detect">${esc(settingsT('settings.detectChatID', null, 'Detect Chat ID'))}</button>
            <button class="btn btn--primary btn--sm" id="settings-telegram-save">${esc(settingsT('common.save', null, 'Save'))}</button>
            <button class="btn btn--ghost btn--sm" id="settings-back">${esc(settingsT('common.cancel', null, 'Cancel'))}</button>
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
