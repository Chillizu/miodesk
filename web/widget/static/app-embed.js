/* miodesk embedded diagnostics widget.
   Background tools never call back into MCP from this UI. */

"use strict";

function applyPreviewTheme() {
  const params = new URLSearchParams(window.location.search);
  const theme = params.get("theme");
  if (theme === "light" || theme === "dark") {
    document.documentElement.dataset.theme = theme;
  }
}

const MIODESK_MCP_APPS_VERSION = "2026-01-26";

function postToHost(message) {
  if (!window.parent || window.parent === window || !window.parent.postMessage) return false;
  window.parent.postMessage(message, "*");
  return true;
}

function structuredPayload(result) {
  if (!result || typeof result !== "object") return result;
  if (result.structuredContent !== undefined) return result.structuredContent;
  if (result.result && result.result.structuredContent !== undefined) return result.result.structuredContent;
  if (result.mcp_tool_result && result.mcp_tool_result.structuredContent !== undefined) {
    return result.mcp_tool_result.structuredContent;
  }
  return result;
}

function handleToolPayload(payload) {
  renderResult(structuredPayload(payload));
}

function applyHostContext(context) {
  if (!context || typeof context !== "object") return;

  const root = document.documentElement;
  if (context.theme === "light" || context.theme === "dark") {
    root.dataset.theme = context.theme;
  }

  const variables = context.styles && context.styles.variables;
  if (variables && typeof variables === "object") {
    for (const [name, value] of Object.entries(variables)) {
      if (/^--[a-z0-9-]+$/i.test(name) && typeof value === "string") {
        root.style.setProperty(name, value);
      }
    }
  }

  const insets = context.safeAreaInsets;
  if (insets && typeof insets === "object") {
    for (const side of ["top", "right", "bottom", "left"]) {
      const value = Number(insets[side]);
      if (Number.isFinite(value) && value >= 0) {
        root.style.setProperty("--miodesk-safe-" + side, value + "px");
      }
    }
  }
}

function startMCPAppsBridge() {
  if (!window.parent || window.parent === window || !window.parent.postMessage) return false;

  const initializeID = "miodesk-ui-initialize";
  window.addEventListener("message", (event) => {
    if (event.source !== window.parent) return;
    const msg = event.data;
    if (!msg || typeof msg !== "object") return;

    if (msg.id === initializeID && msg.result && typeof msg.result === "object") {
      applyHostContext(msg.result.hostContext);
      postToHost({
        jsonrpc: "2.0",
        method: "ui/notifications/initialized",
        params: {},
      });
      return;
    }

    if (msg.method === "ui/notifications/host-context-changed") {
      applyHostContext(msg.params);
      return;
    }

    if (msg.method === "ui/notifications/tool-result") {
      const params = msg.params;
      const payload = params && typeof params === "object" &&
        params.structuredContent !== undefined ? params.structuredContent : params;
      handleToolPayload(payload);
      return;
    }

    if (msg.method === "ui/resource-teardown" && msg.id) {
      postToHost({ jsonrpc: "2.0", id: msg.id, result: {} });
    }
  });

  postToHost({
    jsonrpc: "2.0",
    id: initializeID,
    method: "ui/initialize",
    params: {
      protocolVersion: MIODESK_MCP_APPS_VERSION,
      appInfo: { name: "miodesk", version: /*__MIODESK_APP_VERSION__*/ },
      appCapabilities: { availableDisplayModes: ["inline"] },
    },
  });
  return true;
}

function bootWidget() {
  const host = document.getElementById("result");
  if (!host) return;

  if (window.__MIODESK_PREVIEW) {
    applyPreviewTheme();
    const mocks = window.MIODESK_MOCKS || {};
    renderResult(mocks.status);
    return;
  }

  startMCPAppsBridge();

  try {
    const out = window.openai && window.openai.toolOutput;
    if (out && typeof out === "object") {
      handleToolPayload(out);
      return;
    }
  } catch (_) { /* host not present */ }

  if (window.parent === window) {
    setTimeout(() => {
      if (host.childElementCount > 0) return;
      fetch("/api/status", { headers: { Accept: "application/json" } })
        .then((res) => {
          if (!res.ok) throw new Error("HTTP " + res.status);
          return res.json();
        })
        .then(handleToolPayload)
        .catch((err) => {
          host.replaceChildren();
          host.append(stateLine("fail", "alert", "miodesk unreachable: " + err.message));
        });
    }, 300);
  }
}

bootWidget();
