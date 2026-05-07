(function () {
  const state = {
    routes: [],
    deviceID: "",
    accountID: "",
  };

  const els = {
    status: document.getElementById("kanbanRouteStatus"),
    device: document.getElementById("kanbanDeviceSelect"),
    account: document.getElementById("kanbanAccountSelect"),
    reload: document.getElementById("reloadKanbanRoutesButton"),
    logout: document.getElementById("logoutButton"),
    app: document.getElementById("kanbanApp"),
  };

  function setStatus(message, error) {
    if (!els.status) return;
    els.status.textContent = message;
    els.status.classList.toggle("error", Boolean(error));
  }

  function esc(value) {
    return String(value ?? "")
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;")
      .replaceAll('"', "&quot;")
      .replaceAll("'", "&#039;");
  }

  function routeSupportsKanban(route) {
    const caps = Array.isArray(route.capabilities) ? route.capabilities : [];
    return caps.includes("kittypaw.api");
  }

  function fillRouteControls() {
    const routes = state.routes.filter(routeSupportsKanban);
    els.device.innerHTML = "";
    for (const route of routes) {
      const option = document.createElement("option");
      option.value = route.device_id || "";
      option.textContent = route.device_id || "Device";
      option.selected = option.value === state.deviceID;
      els.device.appendChild(option);
    }
    const route = routes.find((item) => item.device_id === state.deviceID) || routes[0];
    state.deviceID = route ? route.device_id || "" : "";
    els.device.value = state.deviceID;

    const accounts = route && Array.isArray(route.local_accounts) ? route.local_accounts : [];
    els.account.innerHTML = "";
    for (const account of accounts) {
      const option = document.createElement("option");
      option.value = account;
      option.textContent = account;
      option.selected = option.value === state.accountID;
      els.account.appendChild(option);
    }
    if (!accounts.includes(state.accountID)) {
      state.accountID = accounts[0] || "";
    }
    els.account.value = state.accountID;
  }

  function routeAPIPath(path) {
    if (!state.deviceID || !state.accountID) {
      throw new Error("No Kanban relay route is online");
    }
    return "/kanban/api/nodes/" + encodeURIComponent(state.deviceID) +
      "/accounts/" + encodeURIComponent(state.accountID) + path;
  }

  async function apiRaw(path, opts) {
    const resp = await fetch(routeAPIPath(path), {
      ...opts,
      credentials: "same-origin",
      headers: {
        Accept: "application/json",
        ...(opts && opts.body ? { "Content-Type": "application/json" } : {}),
        ...((opts && opts.headers) || {}),
      },
    });
    const rawText = await resp.text();
    let body = null;
    if (rawText) {
      try {
        body = JSON.parse(rawText);
      } catch (_) {
        body = null;
      }
    }
    if (!resp.ok) {
      const formatter = window.KittySpaceWeb && window.KittySpaceWeb.formatHTTPError;
      throw new Error(formatter ? formatter(resp, body, rawText) : "HTTP " + resp.status);
    }
    return body || {};
  }

  async function api(path, opts) {
    return apiRaw(path, opts || {});
  }

  async function apiPost(path, body) {
    return apiRaw(path, { method: "POST", body: JSON.stringify(body || {}) });
  }

  async function logout() {
    await fetch("/auth/logout", { method: "POST", credentials: "same-origin" });
    window.location.href = "/chat/";
  }

  async function loadRoutes() {
    setStatus("Loading routes");
    const resp = await fetch("/kanban/api/routes", {
      credentials: "same-origin",
      headers: { Accept: "application/json" },
    });
    if (resp.status === 401) {
      window.location.href = "/auth/login/google";
      return;
    }
    const rawText = await resp.text();
    let body = {};
    try {
      body = rawText ? JSON.parse(rawText) : {};
    } catch (_) {
      body = {};
    }
    if (!resp.ok) {
      const formatter = window.KittySpaceWeb && window.KittySpaceWeb.formatHTTPError;
      throw new Error(formatter ? formatter(resp, body, rawText) : "HTTP " + resp.status);
    }
    state.routes = Array.isArray(body.data) ? body.data : [];
    if (!state.deviceID || !state.routes.some((route) => route.device_id === state.deviceID && routeSupportsKanban(route))) {
      const selected = state.routes.find(routeSupportsKanban);
      state.deviceID = selected ? selected.device_id || "" : "";
      state.accountID = selected && Array.isArray(selected.local_accounts) ? selected.local_accounts[0] || "" : "";
    }
    fillRouteControls();
    if (!state.deviceID || !state.accountID) {
      setStatus("No Kanban-capable daemon online", true);
      els.app.innerHTML = '<div class="kanban-empty"><h2>No daemon online</h2><p class="kanban-muted">Install the latest kittypaw and start the local server.</p></div>';
      return;
    }
    setStatus("Ready");
    window.api = api;
    window.apiPost = apiPost;
    window.esc = esc;
    window.KittyPawKanbanAPI = { api, apiPost };
    Kanban.mount(els.app);
  }

  els.device.addEventListener("change", () => {
    state.deviceID = els.device.value;
    state.accountID = "";
    fillRouteControls();
    if (state.deviceID && state.accountID) Kanban.mount(els.app);
  });
  els.account.addEventListener("change", () => {
    state.accountID = els.account.value;
    if (state.deviceID && state.accountID) Kanban.mount(els.app);
  });
  els.reload.addEventListener("click", () => loadRoutes().catch((err) => setStatus(err.message, true)));
  els.logout.addEventListener("click", () => logout().catch((err) => setStatus(err.message, true)));

  loadRoutes().catch((err) => setStatus(err.message, true));
})();
