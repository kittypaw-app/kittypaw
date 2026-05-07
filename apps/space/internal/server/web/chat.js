(function () {
  const web = window.KittySpaceWeb;
  const appStorageKey = "kittyspace-chat-state-v1";

  const state = {
    routes: [],
    deviceID: "",
    accountID: "",
    model: "main",
    messages: [],
  };

  const els = {
    status: document.getElementById("appStatus"),
    device: document.getElementById("deviceSelect"),
    account: document.getElementById("accountSelect"),
    model: document.getElementById("modelSelect"),
    reloadRoutes: document.getElementById("reloadRoutesButton"),
    loadModels: document.getElementById("loadModelsButton"),
    clearChat: document.getElementById("clearChatButton"),
    messages: document.getElementById("messages"),
    input: document.getElementById("messageInput"),
    composer: document.getElementById("composer"),
    logout: document.getElementById("logoutButton"),
  };

  function loadState() {
    try {
      const saved = JSON.parse(window.localStorage.getItem(appStorageKey) || "{}");
      state.deviceID = saved.deviceID || "";
      state.accountID = saved.accountID || "";
      state.model = saved.model || "main";
      state.messages = Array.isArray(saved.messages) ? saved.messages : [];
    } catch {
      window.localStorage.removeItem(appStorageKey);
    }
  }

  function saveState() {
    window.localStorage.setItem(appStorageKey, JSON.stringify({
      deviceID: state.deviceID,
      accountID: state.accountID,
      model: state.model,
      messages: state.messages,
    }));
  }

  function setStatus(text, error = false) {
    els.status.textContent = text;
    els.status.classList.toggle("error", error);
  }

  async function requestJSON(path, options = {}) {
    const resp = await window.fetch(path, {
      ...options,
      headers: {
        Accept: "application/json",
        ...(options.headers || {}),
      },
    });
    const text = await resp.text();
    let body = null;
    if (text) {
      try {
        body = JSON.parse(text);
      } catch {
        body = null;
      }
    }
    if (!resp.ok) {
      if (resp.status === 401) {
        window.location.replace("/auth/login/google");
        return null;
      }
      throw new Error(web.formatHTTPError(resp, body, text));
    }
    return body;
  }

  function renderSelect(select, values, selected) {
    select.textContent = "";
    for (const value of values) {
      const option = document.createElement("option");
      option.value = value;
      option.textContent = value;
      option.selected = value === selected;
      select.appendChild(option);
    }
  }

  function renderRoutes() {
    const deviceIDs = [...new Set(state.routes.map((r) => r.device_id).filter(Boolean))];
    renderSelect(els.device, deviceIDs, state.deviceID);
    const route = state.routes.find((r) => r.device_id === state.deviceID);
    const accounts = route && Array.isArray(route.local_accounts) ? route.local_accounts : [];
    renderSelect(els.account, accounts, state.accountID);
  }

  function renderModels(models = []) {
    const ids = models.map((m) => m.id).filter(Boolean);
    if (state.model && !ids.includes(state.model)) {
      ids.unshift(state.model);
    }
    renderSelect(els.model, ids, state.model);
  }

  function renderMessages() {
    els.messages.textContent = "";
    if (state.messages.length === 0) {
      const empty = document.createElement("div");
      empty.className = "empty";
      empty.textContent = "No messages";
      els.messages.appendChild(empty);
      return;
    }
    for (const message of state.messages) {
      const node = document.createElement("article");
      node.className = `message ${message.role}`;
      node.textContent = message.content;
      els.messages.appendChild(node);
    }
    els.messages.scrollTop = els.messages.scrollHeight;
  }

  function routePath(suffix) {
    if (!state.deviceID || !state.accountID) {
      throw new Error("No daemon route is available.");
    }
    return `/chat/api/nodes/${encodeURIComponent(state.deviceID)}/accounts/${encodeURIComponent(state.accountID)}${suffix}`;
  }

  async function loadRoutes() {
    setStatus("Loading routes");
    const body = await requestJSON("/chat/api/routes");
    if (!body) {
      return;
    }
    state.routes = Array.isArray(body.data) ? body.data : [];
    const selected = web.selectFirstAvailableRoute(state, state.routes);
    state.deviceID = selected.deviceID;
    state.accountID = selected.accountID;
    renderRoutes();
    saveState();
    setStatus(state.routes.length ? "Routes loaded" : "No daemon online", state.routes.length === 0);
  }

  async function loadModels() {
    setStatus("Loading models");
    const body = await requestJSON(routePath("/v1/models"));
    if (!body) {
      return;
    }
    renderModels(Array.isArray(body.data) ? body.data : []);
    setStatus("Models loaded");
  }

  async function sendMessage(event) {
    event.preventDefault();
    const text = els.input.value.trim();
    if (!text) {
      return;
    }
    state.messages.push({ role: "user", content: text });
    els.input.value = "";
    renderMessages();
    saveState();

    try {
      setStatus("Sending");
      const body = await requestJSON(routePath("/v1/chat/completions"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          model: state.model || "main",
          messages: state.messages.map(({ role, content }) => ({ role, content })),
        }),
      });
      if (!body) {
        return;
      }
      const content = body && body.choices && body.choices[0] && body.choices[0].message
        ? body.choices[0].message.content || ""
        : "";
      state.messages.push({ role: "assistant", content: content || "(empty)" });
      renderMessages();
      saveState();
      setStatus("Ready");
    } catch (err) {
      state.messages.push({ role: "system", content: err.message });
      renderMessages();
      saveState();
      setStatus(err.message, true);
    }
  }

  async function logout() {
    try {
      await window.fetch("/auth/logout", { method: "POST" });
    } catch {
      // Local state is still cleared; the server cookie may already be gone.
    }
    window.localStorage.removeItem(appStorageKey);
    window.location.replace("/chat/");
  }

  function clearChat() {
    state.messages = [];
    renderMessages();
    saveState();
    setStatus("Chat cleared");
  }

  function syncSelection() {
    state.deviceID = els.device.value;
    state.accountID = els.account.value;
    state.model = els.model.value || "main";
    saveState();
  }

  async function init() {
    loadState();
    renderModels([]);
    renderMessages();
    els.reloadRoutes.addEventListener("click", () => loadRoutes().catch((err) => setStatus(err.message, true)));
    els.loadModels.addEventListener("click", () => loadModels().catch((err) => setStatus(err.message, true)));
    els.device.addEventListener("change", () => {
      state.deviceID = els.device.value;
      const selected = web.selectFirstAvailableRoute(state, state.routes);
      state.accountID = selected.accountID;
      renderRoutes();
      saveState();
    });
    els.account.addEventListener("change", syncSelection);
    els.model.addEventListener("change", syncSelection);
    els.composer.addEventListener("submit", sendMessage);
    els.clearChat.addEventListener("click", clearChat);
    els.logout.addEventListener("click", logout);
    try {
      await loadRoutes();
    } catch (err) {
      setStatus(err.message, true);
    }
  }

  init();
})();
