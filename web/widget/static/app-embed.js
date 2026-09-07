/* miodesk embedded widget — MCP App / ChatGPT bootstrap.
   Picks the data source in documented priority order and hands the payload
   to the kind-dispatched renderer:
   1. preview mode (/preview) — renders bundled mocks;
   2. window.openai.toolOutput — ChatGPT's documented alias;
   3. postMessage ui/notifications/tool-result — MCP Apps notification;
   4. fetch /api/status — standalone fallback (status view).
   No external requests are required in host mode. */

"use strict";

function applyPreviewTheme() {
  const params = new URLSearchParams(window.location.search);
  const theme = params.get("theme");
  if (theme === "light" || theme === "dark") {
    document.documentElement.dataset.theme = theme;
  }
}

const MIODESK_MCP_APPS_VERSION = "2026-01-26";

// Raw postMessage bridge for hosts that implement the MCP Apps lifecycle but
// do not inject the JavaScript SDK. The widget remains dependency-free and
// keeps ChatGPT's window.openai alias as a compatibility path.
function startMCPAppsBridge() {
  if (window.parent === window || !window.parent || !window.parent.postMessage) return;

  const initializeID = "miodesk-ui-initialize";
  window.addEventListener("message", (event) => {
    if (event.source !== window.parent) return;
    const msg = event.data;
    if (!msg || typeof msg !== "object") return;

    if (msg.id === initializeID && msg.result && typeof msg.result === "object") {
      applyHostContext(msg.result.hostContext);
      window.parent.postMessage({
        jsonrpc: "2.0",
        method: "ui/notifications/initialized",
        params: {},
      }, "*");
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
      renderResult(payload);
    }
  });

  window.parent.postMessage({
    jsonrpc: "2.0",
    id: initializeID,
    method: "ui/initialize",
    params: {
      protocolVersion: MIODESK_MCP_APPS_VERSION,
      appInfo: { name: "miodesk", version: "0.1.0" },
      appCapabilities: { availableDisplayModes: ["inline"] },
    },
  }, "*");
}

function applyHostContext(context) {
  if (!context || typeof context !== "object") return;
  if (context.theme === "light" || context.theme === "dark") {
    document.documentElement.dataset.theme = context.theme;
  }
}

function bootWidget() {
  const host = document.getElementById("result");
  if (!host) return;

  if (window.__MIODESK_PREVIEW) {
    applyPreviewTheme();
    const kind = window.__MIODESK_PREVIEW;
    const mocks = window.MIODESK_MOCKS || {};
    if (kind === "all") {
      const stack = mEl("div", "stack");
      for (const [key, data] of Object.entries(mocks)) {
        const section = mEl("section");
        section.append(mEl("h2", "section-h", key));
        section.append(resultRenderer(data.kind)(data));
        stack.append(section);
      }
      host.replaceChildren(stack);
    } else if (Object.prototype.hasOwnProperty.call(mocks, kind)) {
      renderResult(mocks[kind]);
    } else {
      host.replaceChildren();
      host.append(stateLine("info", "dot", `no mock for ${kind}`));
    }
    return;
  }

  try {
    const out = window.openai && window.openai.toolOutput;
    if (out && typeof out === "object") {
      renderResult(out);
      return;
    }
  } catch (_) { /* host not present */ }

  startMCPAppsBridge();

  // Standalone fallback: the local status API. Give a possibly-in-flight
  // postMessage a beat first so host mode never flashes fetched data.
  setTimeout(() => {
    if (document.getElementById("result").childElementCount > 0) return;
    fetch("/api/status", { headers: { Accept: "application/json" } })
      .then((res) => {
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        return res.json();
      })
      .then(renderResult)
      .catch((err) => {
        const host = document.getElementById("result");
        host.replaceChildren();
        host.append(stateLine("fail", "alert", `unreachable: ${err.message}`));
      });
  }, 300);
}

bootWidget();
