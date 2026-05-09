// KittyPaw Chat Panel — WS turn-based chat + permission modal + reconnect

function chatT(key, params, fallback) {
  const runtime = window.KittyPawI18n;
  const value = runtime && typeof runtime.t === 'function' ? runtime.t(key, params) : key;
  return value === key && fallback ? fallback : value;
}

const Chat = {
  ws: null,
  container: null,
  messagesEl: null,
  inputEl: null,
  sessionId: null,
  currentBubble: null,
  reconnectAttempts: 0,
  maxReconnectAttempts: 10,
  reconnectTimer: null,
  busy: false,
  permissionQueue: [],
  permissionActive: false,

  mount(container) {
    this.container = container;
    container.innerHTML = `
      <div class="chat-container">
        <div class="chat-status" id="chat-status"></div>
        <div class="chat-messages" id="chat-messages"></div>
        <div class="chat-input-area">
          <textarea class="chat-input" id="chat-input"
            placeholder="${escHTMLAttr(chatT('chat.placeholder', null, 'Type a message...'))}" rows="1"></textarea>
          <button class="btn btn--primary btn--sm chat-send" id="chat-send">${esc(chatT('chat.send', null, 'Send'))}</button>
        </div>
      </div>
      <div class="permission-overlay" id="perm-overlay" style="display:none">
        <div class="permission-modal">
          <h3>${esc(chatT('chat.permissionRequest', null, 'Permission Request'))}</h3>
          <p id="perm-desc"></p>
          <p class="hint" id="perm-resource"></p>
          <div class="flex gap-8 mt-16">
            <button class="btn btn--primary btn--sm" id="perm-allow">${esc(chatT('chat.allow', null, 'Allow'))}</button>
            <button class="btn btn--ghost btn--sm" id="perm-deny">${esc(chatT('chat.deny', null, 'Deny'))}</button>
          </div>
        </div>
      </div>`;

    this.messagesEl = document.getElementById('chat-messages');
    this.inputEl = document.getElementById('chat-input');

    document.getElementById('chat-send').addEventListener('click', () => this.send());
    this.inputEl.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        this.send();
      }
    });
    this.inputEl.addEventListener('input', () => this._autoResize());

    document.getElementById('perm-allow').addEventListener('click', () => this._respondPermit(true));
    document.getElementById('perm-deny').addEventListener('click', () => this._respondPermit(false));

    this.connect();
  },

  connect() {
    if (!App.wsUrl) {
      // Construct from current location if no wsUrl from bootstrap.
      const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
      const path = App.chatOnly ? '/chat/ws' : '/ws';
      App.wsUrl = `${proto}//${location.host}${path}`;
    }

    // Tear down any existing connection.
    if (this.ws) {
      this.ws.onclose = null;
      this.ws.close();
      this.ws = null;
    }
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    this.currentBubble = null;
    this.busy = false;
    this.permissionQueue = [];
    this.permissionActive = false;

    this._setStatus('connecting', chatT('chat.connecting', null, 'Connecting...'));

    let url = App.wsUrl;
    if (App.apiKey && !App.authRequired) {
      url += `?token=${encodeURIComponent(App.apiKey)}`;
    }
    this.ws = new WebSocket(url);
    this._bindWs();
  },

  _bindWs() {
    this.ws.onopen = () => {
      this.reconnectAttempts = 0;
      this._setStatus('connected', chatT('chat.connected', null, 'Connected'));
      this.inputEl.disabled = false;
    };

    this.ws.onmessage = (evt) => {
      let msg;
      try { msg = JSON.parse(evt.data); } catch { return; }
      this._handleMessage(msg);
    };

    this.ws.onclose = () => {
      this.inputEl.disabled = true;
      this.sessionId = null;
      this._setStatus('disconnected', chatT('chat.disconnected', null, 'Disconnected'));
      this._scheduleReconnect();
    };

    this.ws.onerror = () => {
      this._setStatus('error', chatT('chat.connectionError', null, 'Connection error'));
    };
  },

  _handleMessage(msg) {
    switch (msg.type) {
      case 'session':
        this.sessionId = msg.id;
        break;

      case 'done':
        if (this.currentBubble) {
          this._renderAssistantDone(this.currentBubble, msg);
          this.currentBubble.classList.remove('streaming');
          this.currentBubble = null;
          this._scrollToBottom();
        }
        this.busy = false;
        this.inputEl.disabled = false;
        this.inputEl.focus();
        break;

      case 'error':
        this._addSystemBubble(msg.message, 'error');
        this.busy = false;
        this.inputEl.disabled = false;
        break;

      case 'permission':
        this.permissionQueue.push(msg);
        if (!this.permissionActive) this._showNextPermission();
        break;
    }
  },

  send() {
    const text = this.inputEl.value.trim();
    if (!text || !this.ws || this.ws.readyState !== WebSocket.OPEN || this.busy) return;

    this.busy = true;
    this._addUserBubble(text);
    this.currentBubble = this._addAssistantBubble();
    this.inputEl.value = '';
    this._autoResize();
    this.inputEl.disabled = true;

    this.ws.send(JSON.stringify({ type: 'chat', text }));
  },

  // ── UI helpers ────────────────────────────────────────

  _addUserBubble(text) {
    const el = document.createElement('div');
    el.className = 'chat-bubble chat-bubble--user';
    el.textContent = text;
    this.messagesEl.appendChild(el);
    this._scrollToBottom();
  },

  _addAssistantBubble() {
    const el = document.createElement('div');
    el.className = 'chat-bubble chat-bubble--assistant streaming';
    this.messagesEl.appendChild(el);
    this._scrollToBottom();
    return el;
  },

  _addSystemBubble(text, type) {
    const el = document.createElement('div');
    el.className = `chat-bubble chat-bubble--system ${type || ''}`;
    el.textContent = text;
    this.messagesEl.appendChild(el);
    this._scrollToBottom();
  },

  _renderAssistantDone(el, msg) {
    const result = (msg.full_text || '').trim();
    const image = msg.image;
    if (!image || !image.url) {
      el.innerHTML = renderChatMarkdown(result);
      return;
    }

    const caption = (image.caption || '').trim();
    el.innerHTML = caption ? renderChatMarkdown(caption) : '';
    const img = document.createElement('img');
    img.className = 'chat-image';
    img.src = image.url;
    img.alt = image.alt || '';
    img.loading = 'lazy';
    el.appendChild(img);
  },

  _scrollToBottom() {
    this.messagesEl.scrollTop = this.messagesEl.scrollHeight;
  },

  _autoResize() {
    this.inputEl.style.height = 'auto';
    this.inputEl.style.height = Math.min(this.inputEl.scrollHeight, 120) + 'px';
  },

  _setStatus(state, text) {
    const el = document.getElementById('chat-status');
    if (!el) return;
    el.className = `chat-status chat-status--${state}`;
    el.textContent = text;
  },

  // ── Permission modal ──────────────────────────────────

  _showNextPermission() {
    if (!this.permissionQueue.length) {
      this.permissionActive = false;
      return;
    }
    this.permissionActive = true;
    const perm = this.permissionQueue[0];
    document.getElementById('perm-desc').textContent = perm.description;
    document.getElementById('perm-resource').textContent = perm.resource;
    document.getElementById('perm-overlay').style.display = 'flex';
  },

  _respondPermit(ok) {
    this.permissionQueue.shift();
    document.getElementById('perm-overlay').style.display = 'none';

    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify({ type: 'permit', ok }));
    }

    this._showNextPermission();
  },

  // ── Reconnect ─────────────────────────────────────────

  _scheduleReconnect() {
    if (this.reconnectAttempts >= this.maxReconnectAttempts) {
      this._setStatus('error', chatT('chat.connectionLost', null, 'Connection lost'));
      this._addSystemBubble(chatT('chat.connectionLostPrompt', null, 'Connection lost. Click to reconnect.'), 'error');
      const reconnectBtn = document.createElement('button');
      reconnectBtn.className = 'btn btn--ghost btn--sm mt-12';
      reconnectBtn.textContent = chatT('chat.reconnect', null, 'Reconnect');
      reconnectBtn.addEventListener('click', () => {
        this.reconnectAttempts = 0;
        this.connect();
      });
      this.messagesEl.appendChild(reconnectBtn);
      return;
    }

    const delay = Math.min(1000 * Math.pow(2, this.reconnectAttempts), 30000);
    this.reconnectAttempts++;
    this._setStatus('connecting', chatT('chat.reconnectingIn', { seconds: Math.round(delay / 1000) }, `Reconnecting in ${Math.round(delay / 1000)}s...`));

    this.reconnectTimer = setTimeout(async () => {
      if (App.chatOnly) {
        await App.bootstrapChat();
      } else {
        await App.bootstrap();
      }
      this.connect();
    }, delay);
  },
};

// ── Markdown renderer (lightweight, XSS-safe) ───────────

function renderChatMarkdown(text) {
  if (!text) return '';

  const lines = text.split('\n');
  let html = '';
  let inCodeBlock = false;
  let codeContent = '';
  let codeLang = '';

  for (const line of lines) {
    if (line.startsWith('```')) {
      if (inCodeBlock) {
        html += `<pre><code class="lang-${escHTMLAttr(escAttr(codeLang))}">${esc(codeContent)}</code></pre>`;
        codeContent = '';
        codeLang = '';
        inCodeBlock = false;
      } else {
        codeLang = line.slice(3).trim();
        inCodeBlock = true;
      }
      continue;
    }

    if (inCodeBlock) {
      codeContent += (codeContent ? '\n' : '') + line;
      continue;
    }

    html += renderChatInline(line) + '\n';
  }

  if (inCodeBlock) {
    html += `<pre><code>${esc(codeContent)}</code></pre>`;
  }

  return html;
}

function renderChatInline(line) {
  const headingMatch = line.match(/^(#{1,3})\s+(.+)/);
  if (headingMatch) {
    return `<strong>${esc(headingMatch[2])}</strong>`;
  }

  if (line.match(/^[-*]\s+/)) {
    const content = line.replace(/^[-*]\s+/, '');
    return `<span class="md-li">${formatChatInline(content)}</span>`;
  }

  return formatChatInline(line);
}

function formatChatInline(text) {
  let result = esc(text);
  result = result.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');
  result = result.replace(/`([^`]+)`/g, '<code class="inline-code">$1</code>');
  return result;
}

function escAttr(s) {
  return s.replace(/[^a-zA-Z0-9_-]/g, '');
}
