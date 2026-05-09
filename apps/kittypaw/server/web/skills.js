// KittyPaw Skills Gallery

function skillsT(key, params, fallback) {
  const runtime = window.KittyPawI18n;
  const value = runtime && typeof runtime.t === 'function' ? runtime.t(key, params) : key;
  return value === key && fallback ? fallback : value;
}

const Skills = {
  _container: null,
  _installed: [],   // packages from /api/v1/packages
  _skills: [],      // skills from /api/v1/skills
  _available: [],   // registry results from /api/v1/search
  _view: 'gallery', // 'gallery' | 'detail'
  _debounce: null,
  _query: '',

  mount(container) {
    this._container = container;
    this._view = 'gallery';
    this._query = '';
    this._renderShell();
    this._loadAll('');
  },

  // Render the stable outer shell (search bar stays, results area is dynamic).
  _renderShell() {
    let html = '<div class="gallery-view">';
    html += '<h1>' + esc(skillsT('skills.title', null, 'Skill Gallery')) + '</h1>';
    html += '<p class="sub">' + esc(skillsT('skills.subtitle', null, 'Browse, install, and configure automation skills.')) + '</p>';
    html += '<div class="gallery-search">';
    html += '<input type="text" id="gallery-q" placeholder="' + escHTMLAttr(skillsT('skills.search', null, 'Search skills...')) + '" value="">';
    html += '</div>';
    html += '<div id="gallery-results"><div class="gallery-loading">' + esc(skillsT('skills.loading', null, 'Loading skills...')) + '</div></div>';
    html += '</div>';
    this._container.innerHTML = html;

    const input = document.getElementById('gallery-q');
    input.addEventListener('input', () => {
      clearTimeout(this._debounce);
      this._debounce = setTimeout(() => this._loadAll(input.value), 300);
    });
  },

  async _loadAll(query) {
    this._query = query;
    try {
      const [installed, search, skillsRes] = await Promise.all([
        api('/api/v1/packages'),
        api('/api/v1/search?q=' + encodeURIComponent(query)),
        api('/api/v1/skills'),
      ]);
      this._installed = (installed && installed.packages) || [];
      this._available = (search && search.results) || [];
      this._skills = (skillsRes && skillsRes.skills) || [];
      this._renderResults(query);
    } catch (e) {
      const el = document.getElementById('gallery-results');
      if (el) el.innerHTML = '<div class="gallery-error">' + esc(skillsT('skills.failedToLoadGallery', { error: e.message }, 'Failed to load gallery: ' + e.message)) + '</div>';
    }
  },

  // Only update the results area — search input stays intact.
  _renderResults(query) {
    const el = document.getElementById('gallery-results');
    if (!el) return;

    const installedIDs = new Set(this._installed.map(p => p.meta.id));
    const q = (query || '').toLowerCase();

    // Filter skills by query
    const filteredSkills = this._skills.filter(s => {
      if (!q) return true;
      return (s.name || '').toLowerCase().includes(q) ||
             (s.description || '').toLowerCase().includes(q);
    });

    // Filter packages by query (installed)
    const filteredPkgs = this._installed.filter(p => {
      if (!q) return true;
      const m = p.meta || {};
      return (m.name || '').toLowerCase().includes(q) ||
             (m.id || '').toLowerCase().includes(q) ||
             (m.description || '').toLowerCase().includes(q);
    });

    let html = '';

    // Installed section (skills + packages)
    const hasInstalled = filteredSkills.length > 0 || filteredPkgs.length > 0;
    if (hasInstalled) {
      const total = filteredSkills.length + filteredPkgs.length;
      html += '<div class="gallery-section">';
      html += '<div class="gallery-section-title">' + esc(skillsT('skills.installedCount', { count: total }, 'Installed (' + total + ')')) + '</div>';
      html += '<div class="gallery-grid">';
      for (const s of filteredSkills) {
        html += this._skillCardHTML(s);
      }
      for (const pkg of filteredPkgs) {
        html += this._cardHTML(pkg.meta, true);
      }
      html += '</div></div>';
    }

    // Available section (exclude already installed)
    const avail = this._available.filter(e => !installedIDs.has(e.id));
    html += '<div class="gallery-section">';
    html += '<div class="gallery-section-title">' + esc(avail.length ? skillsT('skills.availableCount', { count: avail.length }, 'Available (' + avail.length + ')') : skillsT('skills.available', null, 'Available')) + '</div>';
    if (avail.length === 0) {
      html += '<div class="gallery-empty">' + esc(skillsT('skills.noAdditionalPackages', null, 'No additional packages found.')) + '</div>';
    } else {
      html += '<div class="gallery-grid">';
      for (const entry of avail) {
        html += this._cardHTML(entry, false);
      }
      html += '</div>';
    }
    html += '</div>';

    el.innerHTML = html;

    // Wire card clicks
    el.querySelectorAll('[data-pkg-id]').forEach(card => {
      card.addEventListener('click', () => {
        this._showDetail(card.dataset.pkgId, card.dataset.installed === 'true');
      });
    });
    el.querySelectorAll('[data-skill-name]').forEach(card => {
      card.addEventListener('click', () => {
        this._showSkillDetail(card.dataset.skillName);
      });
    });
  },

  _skillCardHTML(skill) {
    const name = skill.name || '';
    const desc = skill.description || '';
    const enabled = skill.enabled;
    const trigger = skill.trigger || '';

    let html = '<div class="gallery-card" data-skill-name="' + escHTMLAttr(name) + '">';
    html += '<div class="gallery-card-header">';
    html += '<div class="gallery-card-title">' + esc(name) + '</div>';
    html += '<span class="gallery-card-badge' + (enabled ? '' : ' badge-disabled') + '">' +
            esc(enabled ? skillsT('skills.enabled', null, 'Enabled') : skillsT('skills.disabled', null, 'Disabled')) + '</span>';
    html += '</div>';
    html += '<div class="gallery-card-desc">' + esc(desc) + '</div>';
    html += '<div class="gallery-card-meta">';
    html += '<span>' + esc(skillsT('skills.skillType', null, 'skill')) + '</span>';
    if (trigger) html += '<span>' + esc(trigger) + '</span>';
    html += '</div></div>';
    return html;
  },

  _cardHTML(entry, installed) {
    const id = entry.id;
    const name = entry.name || id;
    const desc = entry.description || '';
    const version = entry.version || '';
    const author = entry.author || '';

    let html = '<div class="gallery-card" data-pkg-id="' + escHTMLAttr(id) + '" data-installed="' + escHTMLAttr(installed) + '">';
    html += '<div class="gallery-card-header">';
    html += '<div class="gallery-card-title">' + esc(name) + '</div>';
    if (installed) html += '<span class="gallery-card-badge">' + esc(skillsT('skills.installed', null, 'Installed')) + '</span>';
    html += '</div>';
    html += '<div class="gallery-card-desc">' + esc(desc) + '</div>';
    html += '<div class="gallery-card-meta">';
    if (installed) html += '<span>' + esc(skillsT('skills.packageType', null, 'package')) + '</span>';
    if (version) html += '<span>v' + esc(version) + '</span>';
    if (author) html += '<span>' + esc(author) + '</span>';
    html += '</div></div>';
    return html;
  },

  // --- Skill detail (teach-created skills) ---

  _showSkillDetail(name) {
    this._view = 'detail';
    const skill = this._skills.find(s => s.name === name);
    if (!skill) return;

    let html = '<div class="gallery-detail">';
    html += '<button class="gallery-back" id="gallery-back-btn">&larr; ' + esc(skillsT('skills.backToGallery', null, 'Back to gallery')) + '</button>';

    html += '<div class="gallery-detail-header">';
    html += '<div class="gallery-detail-title">' + esc(skill.name) + '</div>';
    html += '<div class="gallery-detail-meta">';
    html += '<span class="gallery-type-badge">' + esc(skillsT('skills.skillType', null, 'skill')) + '</span>';
    if (skill.trigger) html += '<span>' + esc(skillsT('skills.trigger', null, 'Trigger')) + ': ' + esc(skill.trigger) + '</span>';
    html += '<span>v' + esc(String(skill.version || 1)) + '</span>';
    html += '</div>';
    if (skill.description) {
      html += '<div class="gallery-detail-desc">' + esc(skill.description) + '</div>';
    }
    html += '</div>';

    // Actions
    html += '<div class="gallery-actions">';
    if (skill.enabled) {
      html += '<button class="gallery-btn gallery-btn-secondary" id="gallery-toggle-btn">' + esc(skillsT('skills.disable', null, 'Disable')) + '</button>';
    } else {
      html += '<button class="gallery-btn gallery-btn-primary" id="gallery-toggle-btn">' + esc(skillsT('skills.enable', null, 'Enable')) + '</button>';
    }
    html += '<button class="gallery-btn gallery-btn-danger" id="gallery-delete-btn">' + esc(skillsT('common.delete', null, 'Delete')) + '</button>';
    html += '</div>';
    html += '<div id="gallery-msg"></div>';
    html += '</div>';

    this._container.innerHTML = html;

    document.getElementById('gallery-back-btn').addEventListener('click', () => this.mount(this._container));

    const toggleBtn = document.getElementById('gallery-toggle-btn');
    toggleBtn.addEventListener('click', async () => {
      toggleBtn.disabled = true;
      const action = skill.enabled ? 'disable' : 'enable';
      try {
        await api('/api/v1/skills/' + encodeURIComponent(name) + '/' + action, { method: 'POST' });
        this.mount(this._container); // reload
      } catch (e) {
        document.getElementById('gallery-msg').innerHTML =
          '<div class="gallery-error">' + esc(e.message) + '</div>';
        toggleBtn.disabled = false;
      }
    });

    const deleteBtn = document.getElementById('gallery-delete-btn');
    deleteBtn.addEventListener('click', async () => {
      if (!confirm(skillsT('skills.deleteSkillConfirm', { name }, 'Delete skill "' + name + '"?'))) return;
      deleteBtn.disabled = true;
      try {
        await api('/api/v1/skills/' + encodeURIComponent(name), { method: 'DELETE' });
        this.mount(this._container);
      } catch (e) {
        document.getElementById('gallery-msg').innerHTML =
          '<div class="gallery-error">' + esc(e.message) + '</div>';
        deleteBtn.disabled = false;
      }
    });
  },

  // --- Package detail (registry packages) ---

  async _showDetail(id, installed) {
    this._view = 'detail';
    this._container.innerHTML = '<div class="gallery-view"><div class="gallery-loading">' + esc(skillsT('common.loading', null, 'Loading...')) + '</div></div>';

    try {
      if (installed) {
        const data = await api('/api/v1/packages/' + encodeURIComponent(id));
        this._renderDetail(data, true);
      } else {
        const entry = this._available.find(e => e.id === id);
        if (!entry) { this.mount(this._container); return; }
        this._renderRegistryDetail(entry);
      }
    } catch (e) {
      this._container.innerHTML = '<div class="gallery-error">' + esc(skillsT('skills.failedToLoad', { error: e.message }, 'Failed to load: ' + e.message)) + '</div>';
    }
  },

  _renderDetail(data, installed) {
    const meta = data.meta || {};
    const schema = data.config_schema || [];
    const values = data.config_values || {};
    const readme = data.readme || '';

    let html = '<div class="gallery-detail">';
    html += '<button class="gallery-back" id="gallery-back-btn">&larr; ' + esc(skillsT('skills.backToGallery', null, 'Back to gallery')) + '</button>';

    // Header
    html += '<div class="gallery-detail-header">';
    html += '<div class="gallery-detail-title">' + esc(meta.name || meta.id) + '</div>';
    html += '<div class="gallery-detail-meta">';
    html += '<span class="gallery-type-badge">' + esc(skillsT('skills.packageType', null, 'package')) + '</span>';
    html += '<span>v' + esc(meta.version || '') + '</span>';
    if (meta.author) html += '<span>' + esc(meta.author) + '</span>';
    if (meta.cron) html += '<span>' + esc(skillsT('skills.cron', null, 'Cron')) + ': ' + esc(meta.cron) + '</span>';
    html += '</div>';
    html += '<div class="gallery-detail-desc">' + esc(meta.description || '') + '</div>';
    html += '</div>';

    // README
    if (readme) {
      html += '<div class="gallery-readme">' + renderSkillsMarkdown(readme) + '</div>';
    }

    // Config form
    if (schema.length > 0) {
      html += '<div class="gallery-config">';
      html += '<div class="gallery-config-title">' + esc(skillsT('skills.configuration', null, 'Configuration')) + '</div>';
      html += '<form id="gallery-config-form">';
      for (const f of schema) {
        html += this._fieldHTML(f, values[f.key] || '');
      }
      html += '</form>';
      html += '<div class="gallery-actions">';
      html += '<button class="gallery-btn gallery-btn-primary" id="gallery-save-btn">' + esc(skillsT('skills.saveConfiguration', null, 'Save Configuration')) + '</button>';
      html += '</div>';
      html += '<div id="gallery-msg"></div>';
      html += '</div>';
    }

    // Uninstall action
    if (installed) {
      html += '<div class="gallery-actions gallery-actions-danger">';
      html += '<button class="gallery-btn gallery-btn-danger" id="gallery-uninstall-btn">' + esc(skillsT('skills.uninstall', null, 'Uninstall')) + '</button>';
      html += '</div>';
    }

    html += '</div>';
    this._container.innerHTML = html;
    this._wireDetailEvents(meta.id, installed);
  },

  _renderRegistryDetail(entry) {
    let html = '<div class="gallery-detail">';
    html += '<button class="gallery-back" id="gallery-back-btn">&larr; ' + esc(skillsT('skills.backToGallery', null, 'Back to gallery')) + '</button>';

    html += '<div class="gallery-detail-header">';
    html += '<div class="gallery-detail-title">' + esc(entry.name || entry.id) + '</div>';
    html += '<div class="gallery-detail-meta">';
    html += '<span>v' + esc(entry.version || '') + '</span>';
    if (entry.author) html += '<span>' + esc(entry.author) + '</span>';
    html += '</div>';
    html += '<div class="gallery-detail-desc">' + esc(entry.description || '') + '</div>';
    html += '</div>';

    html += '<div class="gallery-actions">';
    html += '<button class="gallery-btn gallery-btn-primary" id="gallery-install-btn" data-id="' + escHTMLAttr(entry.id) + '">' + esc(skillsT('skills.install', null, 'Install')) + '</button>';
    html += '</div>';
    html += '<div id="gallery-msg"></div>';
    html += '</div>';

    this._container.innerHTML = html;

    document.getElementById('gallery-back-btn').addEventListener('click', () => this.mount(this._container));

    const installBtn = document.getElementById('gallery-install-btn');
    installBtn.addEventListener('click', async () => {
      installBtn.disabled = true;
      installBtn.textContent = skillsT('skills.installing', null, 'Installing...');
      try {
        await api('/api/v1/packages/install-from-registry', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ id: entry.id }),
        });
        const data = await api('/api/v1/packages/' + encodeURIComponent(entry.id));
        this._renderDetail(data, true);
      } catch (e) {
        document.getElementById('gallery-msg').innerHTML =
          '<div class="gallery-error">' + esc(skillsT('skills.installFailed', { error: e.message }, 'Install failed: ' + e.message)) + '</div>';
        installBtn.disabled = false;
        installBtn.textContent = skillsT('skills.install', null, 'Install');
      }
    });
  },

  _fieldHTML(field, value) {
    const req = field.required ? '<span class="required">*</span>' : '';
    const masked = value === '****';
    let html = '<div class="gallery-config-field">';

    switch (field.type) {
      case 'boolean': {
        const checked = value === 'true' || (!value && field.default === 'true');
        html += '<div class="gallery-config-check">';
        html += '<input type="checkbox" id="cfg-' + escHTMLAttr(field.key) + '" data-key="' + escHTMLAttr(field.key) + '"' + (checked ? ' checked' : '') + '>';
        html += '<label for="cfg-' + escHTMLAttr(field.key) + '">' + esc(field.label || field.key) + req + '</label>';
        html += '</div>';
        break;
      }
      case 'select': {
        html += '<label for="cfg-' + escHTMLAttr(field.key) + '">' + esc(field.label || field.key) + req + '</label>';
        html += '<select id="cfg-' + escHTMLAttr(field.key) + '" data-key="' + escHTMLAttr(field.key) + '">';
        for (const opt of (field.options || [])) {
          const sel = (value || field.default) === opt ? ' selected' : '';
          html += '<option value="' + escHTMLAttr(opt) + '"' + sel + '>' + esc(opt) + '</option>';
        }
        html += '</select>';
        break;
      }
      case 'secret': {
        html += '<label for="cfg-' + escHTMLAttr(field.key) + '">' + esc(field.label || field.key) + req + '</label>';
        html += '<input type="password" id="cfg-' + escHTMLAttr(field.key) + '" data-key="' + escHTMLAttr(field.key) + '"';
        html += ' placeholder="' + escHTMLAttr(masked ? skillsT('skills.setHidden', null, 'Set (hidden)') : (field.default || '')) + '">';
        break;
      }
      case 'number': {
        html += '<label for="cfg-' + escHTMLAttr(field.key) + '">' + esc(field.label || field.key) + req + '</label>';
        html += '<input type="number" id="cfg-' + escHTMLAttr(field.key) + '" data-key="' + escHTMLAttr(field.key) + '"';
        html += ' value="' + escHTMLAttr(value || field.default || '') + '">';
        break;
      }
      default: { // string
        html += '<label for="cfg-' + escHTMLAttr(field.key) + '">' + esc(field.label || field.key) + req + '</label>';
        html += '<input type="text" id="cfg-' + escHTMLAttr(field.key) + '" data-key="' + escHTMLAttr(field.key) + '"';
        html += ' value="' + escHTMLAttr(value || field.default || '') + '">';
      }
    }
    html += '</div>';
    return html;
  },

  _wireDetailEvents(pkgId, installed) {
    document.getElementById('gallery-back-btn').addEventListener('click', () => this.mount(this._container));

    const saveBtn = document.getElementById('gallery-save-btn');
    if (saveBtn) {
      saveBtn.addEventListener('click', async () => {
        saveBtn.disabled = true;
        saveBtn.textContent = skillsT('skills.saving', null, 'Saving...');
        const values = {};
        this._container.querySelectorAll('[data-key]').forEach(el => {
          const key = el.dataset.key;
          if (el.type === 'checkbox') {
            values[key] = el.checked ? 'true' : 'false';
          } else if (el.type === 'password' && el.value === '') {
            return;
          } else {
            values[key] = el.value;
          }
        });

        try {
          await api('/api/v1/packages/' + encodeURIComponent(pkgId) + '/config', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ values }),
          });
          document.getElementById('gallery-msg').innerHTML =
            '<div class="gallery-success"><p>' + esc(skillsT('skills.configurationSaved', null, 'Configuration saved.')) + '</p></div>';
          saveBtn.textContent = skillsT('skills.saveConfiguration', null, 'Save Configuration');
          saveBtn.disabled = false;
        } catch (e) {
          document.getElementById('gallery-msg').innerHTML =
            '<div class="gallery-error">' + esc(skillsT('skills.saveFailed', { error: e.message }, 'Save failed: ' + e.message)) + '</div>';
          saveBtn.textContent = skillsT('skills.saveConfiguration', null, 'Save Configuration');
          saveBtn.disabled = false;
        }
      });
    }

    const uninstallBtn = document.getElementById('gallery-uninstall-btn');
    if (uninstallBtn) {
      uninstallBtn.addEventListener('click', async () => {
        if (!confirm(skillsT('skills.uninstallPackageConfirm', { id: pkgId }, 'Uninstall package "' + pkgId + '"?'))) return;
        uninstallBtn.disabled = true;
        try {
          await api('/api/v1/packages/' + encodeURIComponent(pkgId), { method: 'DELETE' });
          this.mount(this._container);
        } catch (e) {
          document.getElementById('gallery-msg').innerHTML =
            '<div class="gallery-error">' + esc(skillsT('skills.uninstallFailed', { error: e.message }, 'Uninstall failed: ' + e.message)) + '</div>';
          uninstallBtn.disabled = false;
        }
      });
    }
  },
};

// ── Minimal Markdown Renderer ──────────────────────────────

function renderSkillsMarkdown(text) {
  if (!text) return '';
  // Escape HTML first
  let html = text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');

  // Code blocks (``` ... ```)
  html = html.replace(/```(\w*)\n([\s\S]*?)```/g, (_, lang, code) => {
    return '<pre><code>' + code.trim() + '</code></pre>';
  });

  // Inline code
  html = html.replace(/`([^`]+)`/g, '<code>$1</code>');

  // Bold
  html = html.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');

  // Italic
  html = html.replace(/\*(.+?)\*/g, '<em>$1</em>');

  // Headings
  html = html.replace(/^### (.+)$/gm, '<h3>$1</h3>');
  html = html.replace(/^## (.+)$/gm, '<h2>$1</h2>');
  html = html.replace(/^# (.+)$/gm, '<h1>$1</h1>');

  // Unordered lists
  html = html.replace(/^- (.+)$/gm, '<li>$1</li>');
  html = html.replace(/(<li>[\s\S]*?<\/li>)/g, '<ul>$1</ul>');
  // Collapse consecutive <ul> tags
  html = html.replace(/<\/ul>\s*<ul>/g, '');

  // Paragraphs (double newline)
  html = html.replace(/\n\n+/g, '</p><p>');
  html = '<p>' + html + '</p>';

  // Clean up empty paragraphs and paragraphs wrapping block elements
  html = html.replace(/<p>\s*<\/p>/g, '');
  html = html.replace(/<p>(<h[123]>)/g, '$1');
  html = html.replace(/(<\/h[123]>)<\/p>/g, '$1');
  html = html.replace(/<p>(<pre>)/g, '$1');
  html = html.replace(/(<\/pre>)<\/p>/g, '$1');
  html = html.replace(/<p>(<ul>)/g, '$1');
  html = html.replace(/(<\/ul>)<\/p>/g, '$1');

  return html;
}
