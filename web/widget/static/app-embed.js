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
        section.append(MIODESK_RENDERERS[data.kind] ? MIODESK_RENDERERS[data.kind](data) : MIODESK_RENDERERS.unknown(data));
        stack.append(section);
      }
      host.replaceChildren(stack);
    } else if (mocks[kind]) {
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

  window.addEventListener("message", (event) => {
    const msg = event.data;
    if (msg && typeof msg === "object" && msg.method === "ui/notifications/tool-result") {
      renderResult(msg.params && msg.params.structuredContent);
    }
  });

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
