(function () {
  function errorStatus(resp) {
    const label = resp.statusText ? ` ${resp.statusText}` : "";
    return `HTTP ${resp.status}${label}`;
  }

  function looksLikeHTML(text) {
    return /^\s*(<!doctype\s+html|<html[\s>]|<head[\s>]|<body[\s>])/i.test(text);
  }

  function compactErrorText(text) {
    return String(text || "").replace(/\s+/g, " ").trim().slice(0, 220);
  }

  function errorMessageFromJSON(body) {
    if (!body || typeof body !== "object") {
      return "";
    }
    if (typeof body.error === "string") {
      return body.error;
    }
    if (body.error && typeof body.error.message === "string") {
      return body.error.message;
    }
    if (typeof body.message === "string") {
      return body.message;
    }
    return "";
  }

  function formatHTTPError(resp, body, rawText) {
    const status = errorStatus(resp);
    const jsonMessage = compactErrorText(errorMessageFromJSON(body));
    if (jsonMessage && !looksLikeHTML(jsonMessage)) {
      return `${status}: ${jsonMessage}`;
    }
    const textMessage = compactErrorText(rawText || "");
    if (textMessage && !looksLikeHTML(textMessage)) {
      return `${status}: ${textMessage}`;
    }
    return status;
  }

  function selectFirstAvailableRoute(current, routes) {
    const list = Array.isArray(routes) ? routes : [];
    if (list.length === 0) {
      return { deviceID: "", accountID: "" };
    }
    let route = list.find((r) => r.device_id === current.deviceID);
    if (!route) {
      route = list[0];
    }
    const accounts = Array.isArray(route.local_accounts) ? route.local_accounts : [];
    const accountID = accounts.includes(current.accountID) ? current.accountID : accounts[0] || "";
    return { deviceID: route.device_id || "", accountID };
  }

  const api = {
    formatHTTPError,
    selectFirstAvailableRoute,
  };

  if (typeof window !== "undefined") {
    window.KittySpaceWeb = api;
  }
  if (typeof globalThis !== "undefined") {
    globalThis.KittySpaceWeb = api;
  }
})();
