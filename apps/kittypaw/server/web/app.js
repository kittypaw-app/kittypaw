// KittyPaw Web App — Router + Auth Bootstrap + Tab Navigation

const I18n = window.KittyPawI18n;
const t = (key, params) => I18n ? I18n.t(key, params) : key;

const App = {
  root: null,
  apiKey: null,
  wsUrl: null,
  authRequired: false,
  accountID: null,
  isDefault: true,
  chatOnly: false,
  kanbanOnly: false,
  settingsSurface: false,
  activeTab: null,
  _dashboardInterval: null,
  _languagePicker: null,

  async init() {
    this.root = document.getElementById('app');
    this.chatOnly = this.isChatSurface();
    this.kanbanOnly = this.isKanbanSurface();
    this.settingsSurface = this.isSettingsSurface();
    const auth = await this.checkAuth();
    this.authRequired = !!auth.auth_required;
    this.accountID = auth.account_id || null;
    this.isDefault = auth.is_default !== false;
    if (auth.auth_required && !auth.authenticated) {
      this.showLogin();
      return;
    }
    if (!this.chatOnly && !this.kanbanOnly && !this.settingsSurface) {
      this.redirectToSettingsSurface();
      return;
    }
    await this.startCurrentSurface();
  },

  isChatSurface() {
    return location.pathname === '/chat' || location.pathname.startsWith('/chat/');
  },

  isKanbanSurface() {
    return location.pathname === '/kanban' || location.pathname.startsWith('/kanban/');
  },

  isSettingsSurface() {
    return location.pathname === '/_settings' || location.pathname.startsWith('/_settings/');
  },

  redirectToSettingsSurface() {
    location.replace('/_settings');
  },

  async startCurrentSurface() {
    if (this.chatOnly) {
      await this.startChatFlow();
      return;
    }
    if (this.kanbanOnly) {
      await this.startKanbanFlow();
      return;
    }
    await this.startMainFlow();
  },

  async startMainFlow() {
    const status = await apiRaw('/api/setup/status');
    if (!status.completed) {
      this.showCliSetupRequired(status);
    } else {
      if (!this.authRequired || this.isDefault) {
        await this.bootstrap();
      } else {
        this.apiKey = null;
        this.wsUrl = null;
      }
      this.showShell();
    }
  },

  async bootstrap() {
    const data = await apiRaw('/api/bootstrap');
    this.apiKey = data.api_key;
    this.wsUrl = data.ws_url;
  },

  async bootstrapChat() {
    const data = await apiRaw('/api/chat/bootstrap');
    this.apiKey = null;
    this.wsUrl = data.ws_url;
    this.accountID = data.account_id || this.accountID;
    this.isDefault = data.is_default !== false;
    return data;
  },

  async startChatFlow() {
    const data = await this.bootstrapChat();
    if (data.setup_completed === false) {
      this.showChatSetupRequired();
      return;
    }
    this.showChatSurface();
  },

  async startKanbanFlow() {
    const status = await apiRaw('/api/setup/status');
    if (!status.completed) {
      this.showCliSetupRequired(status);
      return;
    }
    if (!this.authRequired || this.isDefault) {
      await this.bootstrap();
    } else {
      this.apiKey = null;
      this.wsUrl = null;
    }
    this.showKanbanSurface();
  },

  async checkAuth() {
    try {
      const res = await fetch('/api/auth/me', { credentials: 'same-origin' });
      if (res.status === 401) {
        return { auth_required: true, authenticated: false };
      }
      if (!res.ok) {
        throw new Error(`auth check failed: ${res.status}`);
      }
      return res.json();
    } catch (e) {
      console.error('Auth check failed:', e);
      return { auth_required: true, authenticated: false };
    }
  },

  showLogin(errorMessage = '') {
    this._teardown();
    this.apiKey = null;
    this.wsUrl = null;
    this.accountID = null;
    this.isDefault = true;
    this.root.style.cssText = '';
    this.root.innerHTML = `
      <form class="card card--center login-card" id="login-form">
        <h1 class="login-title">Kitty<span class="accent">Paw</span></h1>
        <div class="login-fields">
          <div class="text-left">
            <label for="login-account" data-i18n="app.accountId">${esc(t('app.accountId'))}</label>
            <input class="input" id="login-account" name="account_id" autocomplete="username" required>
          </div>
          <div class="text-left">
            <label for="login-password" data-i18n="app.password">${esc(t('app.password'))}</label>
            <input class="input" id="login-password" name="password" type="password" autocomplete="current-password" required>
          </div>
        </div>
        <div class="error-box login-error" id="login-error" ${errorMessage ? '' : 'hidden'}>${esc(errorMessage)}</div>
        <button class="btn btn--primary login-submit" type="submit" data-i18n="app.signIn">${esc(t('app.signIn'))}</button>
      </form>`;

    const form = document.getElementById('login-form');
    form.addEventListener('submit', async (event) => {
      event.preventDefault();
      const button = form.querySelector('button[type="submit"]');
      const error = document.getElementById('login-error');
      button.disabled = true;
      error.hidden = true;
      try {
        const res = await fetch('/api/auth/login', {
          method: 'POST',
          credentials: 'same-origin',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            account_id: form.account_id.value.trim(),
            password: form.password.value,
          }),
        });
        if (!res.ok) throw new Error('login failed');
        const auth = await res.json();
        this.authRequired = true;
        this.accountID = auth.account_id || null;
        this.isDefault = auth.is_default !== false;
        if (this.chatOnly || this.kanbanOnly) {
          await this.startCurrentSurface();
        } else {
          location.assign('/_settings');
        }
      } catch (e) {
        error.textContent = t('app.invalidLogin');
        error.hidden = false;
      } finally {
        button.disabled = false;
      }
    });
  },

  _chatMounted: false,

  _teardown() {
    this.destroyLanguagePicker();
    if (this._chatMounted) {
      if (Chat.ws) { Chat.ws.onclose = null; Chat.ws.close(); Chat.ws = null; }
      if (Chat.reconnectTimer) { clearTimeout(Chat.reconnectTimer); Chat.reconnectTimer = null; }
    }
    if (this._dashboardInterval) { clearInterval(this._dashboardInterval); this._dashboardInterval = null; }
    this._chatMounted = false;
    this.activeTab = null;
  },

  showChatSurface() {
    this._teardown();
    this.root.style.display = 'block';
    this.root.style.alignItems = '';
    this.root.style.justifyContent = '';
    this.root.innerHTML = `
      <main class="chat-surface">
        <div id="chat-panel"></div>
      </main>`;
    const chatPanel = document.getElementById('chat-panel');
    chatPanel.style.display = 'flex';
    Chat.mount(chatPanel);
    this._chatMounted = true;
    this.activeTab = 'chat';
  },

  showKanbanSurface() {
    this._teardown();
    this.root.style.display = 'block';
    this.root.style.alignItems = '';
    this.root.style.justifyContent = '';
    this.root.innerHTML = '<main class="kanban-surface"><div id="kanban-panel"></div></main>';
    Kanban.mount(document.getElementById('kanban-panel'));
    this.activeTab = 'kanban';
  },

  showChatSetupRequired() {
    this._teardown();
    this.root.style.cssText = '';
    this.root.innerHTML = `
      <div class="card card--center">
        <h1>Kitty<span class="accent">Paw</span></h1>
        <p class="sub mt-16">${esc(t('app.runSetupChat'))}</p>
      </div>`;
  },

  showCliSetupRequired() {
    this._teardown();
    this.root.style.cssText = '';
    this.root.innerHTML = `
      <div class="card card--center">
        <h1>Kitty<span class="accent">Paw</span></h1>
        <p class="sub mt-16">${esc(t('app.runSetupLocal'))}</p>
      </div>`;
  },

  showShell() {
    this._teardown();
    const defaultNav = this.isDefault
      ? '<button class="nav-item" data-tab="dashboard" data-i18n="nav.dashboard">' + esc(t('nav.dashboard')) + '</button><button class="nav-item" data-tab="skills" data-i18n="nav.skills">' + esc(t('nav.skills')) + '</button>'
      : '';

    // Override #app centering from stylesheet
    this.root.style.display = 'block';
    this.root.style.alignItems = '';
    this.root.style.justifyContent = '';
    this.root.innerHTML = `
      <div class="shell">
        <aside class="sidebar">
          <div class="sidebar-logo">Kitty<span class="accent">Paw</span></div>
          <nav class="sidebar-nav">
            ${defaultNav}
            <button class="nav-item" data-tab="settings" data-i18n="nav.settings">${esc(t('nav.settings'))}</button>
          </nav>
          <div class="sidebar-footer">
            <span class="sidebar-version">v0.1.0</span>
          </div>
        </aside>
        <main class="main-content">
          <header class="app-header">
            <div class="app-language" id="app-language"></div>
          </header>
          <div id="tab-content"></div>
        </main>
      </div>`;

    this.root.querySelectorAll('[data-tab]').forEach(btn => {
      btn.addEventListener('click', () => this.switchTab(btn.dataset.tab));
    });

    this.mountLanguagePicker(document.getElementById('app-language'));
    this.switchTab('settings');
  },

  mountLanguagePicker(target) {
    this.destroyLanguagePicker();
    if (!I18n || !target || typeof I18n.mountLanguagePicker !== 'function') {
      return;
    }
    this._languagePicker = I18n.mountLanguagePicker(target, {
      className: 'i18n-language',
      onChange: async (locale) => {
        try {
          await apiRaw('/api/settings/locale', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ locale }),
          });
        } catch (e) {
          console.warn('Locale preference save failed:', e);
        }
        await this.refreshCurrentSurface();
      },
    });
    if (this._languagePicker && this._languagePicker.element) {
      const globe = this._languagePicker.element.querySelector('.kp-language-picker__globe');
      if (globe) {
        globe.classList.add('i18n-language-button');
      }
    }
    if (this._languagePicker && this._languagePicker.select) {
      this._languagePicker.select.classList.add('i18n-language-select');
    }
  },

  destroyLanguagePicker() {
    if (this._languagePicker && typeof this._languagePicker.destroy === 'function') {
      this._languagePicker.destroy();
    }
    this._languagePicker = null;
  },

  async refreshCurrentSurface() {
    const tab = this.activeTab;
    if (tab && document.getElementById('tab-content')) {
      this.activeTab = null;
      this.switchTab(tab);
      return;
    }
    await this.startCurrentSurface();
  },

  switchTab(tab) {
    if (tab === this.activeTab) return;
    const prev = this.activeTab;
    this.activeTab = tab;

    this.root.querySelectorAll('[data-tab]').forEach(btn => {
      btn.classList.toggle('active', btn.dataset.tab === tab);
    });

    if (this._dashboardInterval) {
      clearInterval(this._dashboardInterval);
      this._dashboardInterval = null;
    }

    const content = document.getElementById('tab-content');

    // Hide/destroy previous
    if (prev) {
      content.innerHTML = '';
    }

    // Show/mount new
    content.style.display = '';
    if (tab === 'dashboard') {
      this._showDashboard(content);
    } else if (tab === 'skills') {
      Skills.mount(content);
    } else {
      Settings.mount(content);
    }
  },

  _showDashboard(container) {
    container.innerHTML = `
      <div class="dashboard">
        <h1>\u{1F43E} ${esc(t('dashboard.title'))}</h1>
        <p class="hint">${esc(t('dashboard.autoRefresh'))}</p>
        <div class="stats-grid" id="stats"></div>
        <h2>${esc(t('dashboard.conversation'))}</h2>
        <table><thead><tr><th>${esc(t('dashboard.time'))}</th><th>${esc(t('dashboard.role'))}</th><th>${esc(t('dashboard.channel'))}</th><th>${esc(t('dashboard.content'))}</th></tr></thead>
        <tbody id="conversation"></tbody></table>
        <h2 class="mt-20">${esc(t('dashboard.llmUsage'))}</h2>
        <table><thead><tr><th>${esc(t('dashboard.model'))}</th><th>${esc(t('dashboard.calls'))}</th><th>${esc(t('dashboard.input'))}</th><th>${esc(t('dashboard.output'))}</th><th>${esc(t('dashboard.cache'))}</th><th>${esc(t('dashboard.cost'))}</th></tr></thead>
        <tbody id="llm-usage"></tbody></table>
        <h2 class="mt-20">${esc(t('dashboard.recentExecutions'))}</h2>
        <table><thead><tr><th>${esc(t('dashboard.time'))}</th><th>${esc(t('dashboard.skill'))}</th><th>${esc(t('dashboard.status'))}</th><th>${esc(t('dashboard.duration'))}</th><th>${esc(t('dashboard.summary'))}</th></tr></thead>
        <tbody id="exec"></tbody></table>
      </div>`;
    this._refreshDashboard();
    this._dashboardInterval = setInterval(() => this._refreshDashboard(), 30000);
  },

  async _refreshDashboard() {
    try {
      const s = await api('/api/v1/status');
      const statsEl = document.getElementById('stats');
      if (statsEl) {
        statsEl.innerHTML =
          statCard(s.total_runs || 0, t('dashboard.todayRuns')) +
          statCard(s.successful || 0, t('dashboard.successful'), 'ok') +
          statCard(s.failed || 0, t('dashboard.failed'), 'fail') +
          statCard(s.total_tokens || 0, t('dashboard.tokens')) +
          statCard(formatUSD(s.estimated_cost_usd || 0), t('dashboard.estimatedCost'));
      }

      const usageRows = s.llm_usage_by_model || [];
      const usageEl = document.getElementById('llm-usage');
      if (usageEl) {
        usageEl.innerHTML = usageRows.length
          ? usageRows.map(r => {
            const cacheTokens = (r.cache_creation_input_tokens || 0) + (r.cache_read_input_tokens || 0);
            const model = r.provider ? `${r.provider}/${r.model || ''}` : (r.model || '');
            return `<tr><td>${esc(model)}</td>` +
              `<td>${esc(String(r.calls || 0))}</td>` +
              `<td>${esc(String(r.input_tokens || 0))}</td>` +
              `<td>${esc(String(r.output_tokens || 0))}</td>` +
              `<td>${esc(String(cacheTokens))}</td>` +
              `<td>${esc(formatUSD(r.estimated_cost_usd || 0))}</td></tr>`;
          }).join('')
          : '<tr><td colspan="6">' + esc(t('dashboard.noLLMUsage')) + '</td></tr>';
      }

      const historyData = await api('/api/v1/chat/history?limit=10');
      const turns = historyData.turns || [];
      const conversationEl = document.getElementById('conversation');
      if (conversationEl) {
        conversationEl.innerHTML = turns.length
          ? turns.map(t =>
            `<tr><td>${esc(((t.Timestamp || t.timestamp) || '').slice(0,19))}</td>` +
            `<td>${esc(t.Role || t.role || '')}</td>` +
            `<td>${esc(t.Channel || t.channel || '')}</td>` +
            `<td>${esc(t.Content || t.content || '')}</td></tr>`
          ).join('')
          : '<tr><td colspan="4">' + esc(t('dashboard.noConversation')) + '</td></tr>';
      }

      const execData = await api('/api/v1/executions');
      const execs = execData.executions || [];
      const execEl = document.getElementById('exec');
      if (execEl) {
        execEl.innerHTML = execs.length
          ? execs.map(r =>
            `<tr><td>${esc(((r.StartedAt || r.started_at) || '').slice(0,19))}</td>` +
            `<td>${esc(r.SkillName || r.skill_name || '')}</td>` +
            `<td class="${escHTMLAttr((r.Success || r.success) ? 'ok' : 'fail')}">${esc((r.Success || r.success) ? t('dashboard.executionOK') : t('dashboard.executionFail'))}</td>` +
            `<td>${esc(String(r.DurationMs || r.duration_ms || 0))}ms</td>` +
            `<td>${esc(((r.ResultSummary || r.result_summary) || '').slice(0,60))}</td></tr>`
          ).join('')
          : '<tr><td colspan="5">' + esc(t('dashboard.noExecutions')) + '</td></tr>';
      }
    } catch (e) { console.error('Dashboard refresh failed:', e); }
  },
};

function statCard(value, label, cls) {
  return `<div class="stat-card"><div class="value ${escHTMLAttr(cls || '')}">${esc(String(value))}</div><div class="label">${esc(label)}</div></div>`;
}

function formatUSD(value) {
  return `$${Number(value || 0).toFixed(6)}`;
}

// ── Helpers ──────────────────────────────────────────────

function esc(s) {
  const d = document.createElement('div');
  d.textContent = s == null ? '' : String(s);
  return d.innerHTML;
}

function escHTMLAttr(value) {
  return String(value == null ? '' : value)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

/** Fetch without auth (for setup/bootstrap endpoints). */
async function apiRaw(url, opts) {
  const res = await fetch(url, Object.assign({ credentials: 'same-origin' }, opts || {}));
  if (res.status === 401) {
    App.showLogin(t('app.sessionExpired'));
    throw new Error('unauthorized');
  }
  if (res.status === 403) {
    throw new Error(await responseErrorMessage(res, 'forbidden'));
  }
  if (!res.ok) {
    let message = `Request failed: ${res.status}`;
    try {
      const body = await res.json();
      if (body && body.error) message = body.error;
    } catch (_) {}
    throw new Error(message);
  }
  return res.json();
}

/** Fetch with Bearer auth header. */
async function api(url, opts = {}) {
  opts.credentials = opts.credentials || 'same-origin';
  if (App.apiKey) {
    opts.headers = Object.assign({}, opts.headers || {}, { Authorization: `Bearer ${App.apiKey}` });
  }
  const res = await fetch(url, opts);
  if (res.status === 401) {
    App.showLogin(t('app.sessionExpired'));
    throw new Error('unauthorized');
  }
  if (res.status === 403) {
    throw new Error(await responseErrorMessage(res, 'forbidden'));
  }
  if (!res.ok) {
    let message = `Request failed: ${res.status}`;
    try {
      const body = await res.json();
      if (body && body.error) message = body.error;
    } catch (_) {}
    throw new Error(message);
  }
  return res.json();
}

async function responseErrorMessage(res, fallback) {
  try {
    const body = await res.json();
    if (body && body.error) return body.error;
  } catch (_) {}
  return fallback;
}

async function apiPost(url, body) {
  return apiRaw(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
}

// ── Boot ─────────────────────────────────────────────────

document.addEventListener('DOMContentLoaded', () => App.init());
