/* miodesk embedded widget — MCP App / ChatGPT bootstrap.
   Status is a one-shot diagnostics view. Task views are live: command_start
   creates one iframe, then that iframe polls command_poll through the MCP Apps
   tools/call bridge and updates its own DOM until the task finishes. */

"use strict";

function applyPreviewTheme() {
  const params = new URLSearchParams(window.location.search);
  const theme = params.get("theme");
  if (theme === "light" || theme === "dark") {
    document.documentElement.dataset.theme = theme;
  }
}

const MIODESK_MCP_APPS_VERSION = "2026-01-26";
const TASK_POLL_INTERVAL_MS = 1200;

let bridgeAvailable = false;
let bridgeInitialized = false;
let bridgeReadyResolve;
const bridgeReady = new Promise((resolve) => { bridgeReadyResolve = resolve; });
let requestSeq = 0;
const pendingRequests = new Map();
let widgetDisposed = false;
let taskPollTimer = null;
let taskPollBusy = false;
let activeTask = null;

function postToHost(message) {
  if (!window.parent || window.parent === window || !window.parent.postMessage) return false;
  window.parent.postMessage(message, "*");
  return true;
}

function startMCPAppsBridge() {
  if (!window.parent || window.parent === window || !window.parent.postMessage) return false;
  bridgeAvailable = true;

  const initializeID = "miodesk-ui-initialize";
  window.addEventListener("message", (event) => {
    if (event.source !== window.parent) return;
    const msg = event.data;
    if (!msg || typeof msg !== "object") return;

    if (msg.id === initializeID && msg.result && typeof msg.result === "object") {
      applyHostContext(msg.result.hostContext);
      bridgeInitialized = true;
      bridgeReadyResolve();
      postToHost({
        jsonrpc: "2.0",
        method: "ui/notifications/initialized",
        params: {},
      });
      return;
    }

    if (msg.id && pendingRequests.has(msg.id)) {
      const pending = pendingRequests.get(msg.id);
      pendingRequests.delete(msg.id);
      clearTimeout(pending.timer);
      if (msg.error) pending.reject(new Error(msg.error.message || "MCP Apps request failed"));
      else pending.resolve(msg.result);
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

    if (msg.method === "ui/resource-teardown") {
      widgetDisposed = true;
      stopTaskPolling();
      if (msg.id) {
        postToHost({ jsonrpc: "2.0", id: msg.id, result: {} });
      }
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

function bridgeToolCall(name, args) {
  return new Promise((resolve, reject) => {
    const id = "miodesk-tools-" + (++requestSeq);
    const timer = setTimeout(() => {
      pendingRequests.delete(id);
      reject(new Error("tool call timed out"));
    }, 15000);
    pendingRequests.set(id, { resolve, reject, timer });
    postToHost({
      jsonrpc: "2.0",
      id,
      method: "tools/call",
      params: { name, arguments: args || {} },
    });
  });
}

function withTimeout(promise, ms) {
  return Promise.race([
    promise,
    new Promise((_, reject) => setTimeout(() => reject(new Error("bridge not ready")), ms)),
  ]);
}

async function callServerTool(name, args) {
  if (bridgeAvailable && !bridgeInitialized) {
    try {
      await withTimeout(bridgeReady, 1500);
    } catch (_) {
      // Initialization unavailable: try ChatGPT's compatibility bridge below.
    }
  }
  if (bridgeInitialized) {
    return bridgeToolCall(name, args);
  }

  try {
    const openai = window.openai;
    if (openai && typeof openai.callTool === "function") {
      return await openai.callTool(name, args || {});
    }
  } catch (err) {
    throw err;
  }

  throw new Error("host does not expose MCP tool calls");
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

function stopTaskPolling() {
  if (taskPollTimer) clearTimeout(taskPollTimer);
  taskPollTimer = null;
}

function scheduleTaskPoll(delay) {
  stopTaskPolling();
  if (widgetDisposed || !activeTask || activeTask.status === "done") return;
  taskPollTimer = setTimeout(pollActiveTask, delay == null ? TASK_POLL_INTERVAL_MS : delay);
}

async function pollActiveTask() {
  if (widgetDisposed || taskPollBusy || !activeTask || activeTask.status === "done") return;
  taskPollBusy = true;
  const id = activeTask.id;
  try {
    const result = await callServerTool("command_poll", { id });
    const payload = structuredPayload(result);
    if (payload && payload.kind === "task" && payload.id === id) {
      handleToolPayload(payload);
    } else {
      scheduleTaskPoll(TASK_POLL_INTERVAL_MS * 2);
    }
  } catch (err) {
    if (activeTask && activeTask.id === id) {
      renderResult(Object.assign({}, activeTask, { poll_error: err.message || String(err) }));
      scheduleTaskPoll(TASK_POLL_INTERVAL_MS * 2);
    }
  } finally {
    taskPollBusy = false;
  }
}

async function cancelTask(id) {
  if (!id) return;
  stopTaskPolling();
  const result = await callServerTool("command_cancel", { id });
  const payload = structuredPayload(result);
  if (payload && typeof payload === "object") handleToolPayload(payload);
}

function acceptTask(data) {
  activeTask = data;
  if (data.status === "done") {
    stopTaskPolling();
    return;
  }
  scheduleTaskPoll(650);
}

function handleToolPayload(payload) {
  payload = structuredPayload(payload);
  renderResult(payload);
  if (payload && payload.kind === "task" && payload.id) acceptTask(payload);
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
      host.append(stateLine("info", "dot", "no mock for " + kind));
    }
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

  // Only the standalone local widget falls back to /api/status. An embedded
  // Task view must wait for its own tool result and never flash status data.
  if (window.parent === window) {
    setTimeout(() => {
      if (document.getElementById("result").childElementCount > 0) return;
      fetch("/api/status", { headers: { Accept: "application/json" } })
        .then((res) => {
          if (!res.ok) throw new Error("HTTP " + res.status);
          return res.json();
        })
        .then(handleToolPayload)
        .catch((err) => {
          const target = document.getElementById("result");
          target.replaceChildren();
          target.append(stateLine("fail", "alert", "unreachable: " + err.message));
        });
    }, 300);
  }
}

bootWidget();
