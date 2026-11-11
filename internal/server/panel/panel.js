"use strict";

const $ = (id) => document.getElementById(id);
const state = {
  view: "overview",
  status: null,
  savedURL: "",
  saving: false,
  secret: "",
  secretEpoch: 0,
  secretTimer: null,
  tokenBusy: false,
  statusGeneration: 0,
  logsGeneration: 0,
  statusController: null,
  logsController: null,
  tokenController: null,
  queryTimer: null,
  toolsSignature: "",
  logsSignature: "",
  logsFilter: "",
};

function text(id, value) {
  $(id).textContent = value ?? "—";
}

function message(id, value, tone = "") {
  text(id, value);
  $(id).hidden = !value;
  $(id).dataset.tone = tone;
}

function element(tag, className, value) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (value != null) node.textContent = value;
  return node;
}

function pill(id, value, tone) {
  text(id, value);
  $(id).dataset.tone = tone;
}

let toastTimer;
function toast(value) {
  clearTimeout(toastTimer);
  message("toast", value);
  toastTimer = setTimeout(() => message("toast", ""), 2400);
}

async function copy(value) {
  if (!value) return;
  try {
    await navigator.clipboard.writeText(value);
    toast("已复制");
  } catch {
    throw new Error("无法访问剪贴板，请手动选择并复制。");
  }
}

async function request(path, { body, signal } = {}) {
  const controller = new AbortController();
  const abort = () => controller.abort();
  if (signal?.aborted) controller.abort();
  else signal?.addEventListener("abort", abort, { once: true });
  let timedOut = false;
  const timer = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, 8000);
  try {
    const options = { cache: "no-store", signal: controller.signal, credentials: "same-origin" };
    if (body !== undefined) {
      options.method = "POST";
      options.headers = { "Content-Type": "application/json", "X-Miodesk-Panel": "1" };
      options.body = JSON.stringify(body);
    }
    const response = await fetch(path, options);
    const data = await response.json();
    if (!response.ok) throw new Error(data.error || `HTTP ${response.status}`);
    return data;
  } catch (error) {
    if (timedOut) throw new Error("请求超时，请重试。");
    throw error;
  } finally {
    clearTimeout(timer);
    signal?.removeEventListener("abort", abort);
  }
}

function errorText(error) {
  const value = String(error.message || error);
  const translations = [
    [/direct loopback|same-origin/, "请通过本机地址直接打开面板后重试。"],
    [/Bridge uses an environment token/, "Bridge 正在使用环境变量 Token。请改用 Token 文件并重启 Bridge，再在此更新。"],
    [/single value of at most 4096 bytes/, "Token 必须为单行，且不超过 4096 字节。"],
    [/Cannot read the Bridge token/, "无法读取 Token 文件，请检查文件路径与权限，或导入已有 Token。"],
    [/private XDG credentials directory/, "Token 只能保存在 miodesk 的私有凭据目录。"],
    [/Cannot prepare private miodesk credentials/, "无法写入私有凭据目录。请检查权限，并确保目录和文件不是符号链接。"],
    [/configuration rollback failed/, "Token 更新及配置回滚均失败，请在终端修复 config.toml。"],
    [/previous configuration restored/, "Token 更新失败，已恢复原配置。"],
    [/Cannot save miodesk configuration/, "配置保存失败，Token 未更改。请检查配置文件权限。"],
    [/Cannot read miodesk configuration/, "无法读取配置，请在终端检查 config.toml。"],
    [/loopback HTTP|loopback URL|Bridge URL|mioweave URL/, "Bridge 地址需为本机 HTTP 地址，且不包含路径、查询或凭据。"],
    [/Bridge port must be/, "Bridge 端口需在 1–65535 之间。"],
    [/Invalid log source, level, time range, or query/, "筛选条件无效，请缩短搜索关键字后重试。"],
    [/Failed to fetch|NetworkError|Load failed/, "无法连接本地服务，请检查服务后重试。"],
  ];
  for (const [pattern, translated] of translations) {
    if (pattern.test(value)) return translated;
  }
  let safe = value;
  for (const secret of [state.secret, $("new-token").value.trim()]) {
    if (secret) safe = safe.split(secret).join("[已隐藏]");
  }
  return safe;
}

function setTheme(value) {
  const dark = value === "dark" || (value === "system" && matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.dataset.theme = dark ? "dark" : "light";
}
let theme = "system";
try {
  const saved = localStorage.getItem("miodesk-panel-theme");
  if (["system", "light", "dark"].includes(saved)) theme = saved;
} catch { /* Theme persistence is optional. */ }
$("theme-select").value = theme;
setTheme(theme);
$("theme-select").addEventListener("change", () => {
  theme = $("theme-select").value;
  setTheme(theme);
  try { localStorage.setItem("miodesk-panel-theme", theme); } catch { /* Use this tab only. */ }
});
matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => setTheme(theme));

function selectView(view) {
  if (view === state.view) return;
  clearSensitive();
  state.view = view;
  for (const name of ["overview", "logs"]) {
    const selected = name === view;
    $("view-" + name).setAttribute("aria-selected", String(selected));
    $("view-" + name).tabIndex = selected ? 0 : -1;
    $(name + "-view").hidden = !selected;
  }
  clearTimeout(state.queryTimer);
  state.queryTimer = null;
  state.logsGeneration++;
  state.logsController?.abort();
  if (view === "logs") loadLogs();
}
for (const view of ["overview", "logs"]) {
  $("view-" + view).addEventListener("click", () => selectView(view));
  $("view-" + view).addEventListener("keydown", (event) => {
    let next;
    if (event.key === "Home") next = "overview";
    else if (event.key === "End") next = "logs";
    else if (["ArrowLeft", "ArrowRight"].includes(event.key)) next = view === "logs" ? "overview" : "logs";
    if (!next) return;
    event.preventDefault();
    selectView(next);
    $("view-" + next).focus();
  });
}

function clock(date = new Date()) {
  return date.toLocaleTimeString("zh-CN", { hour12: false });
}

function duration(seconds) {
  if (seconds < 60) return "不足 1 分钟";
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分钟`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)} 小时 ${Math.floor(seconds % 3600 / 60)} 分钟`;
  return `${Math.floor(seconds / 86400)} 天 ${Math.floor(seconds % 86400 / 3600)} 小时`;
}

const toolDescriptions = {
  read: "读取文件", search: "搜索文件内容", list: "浏览目录", write: "创建或覆盖文件",
  edit: "批量原子编辑", delete: "删除文件或目录", exec_command: "执行命令",
  write_stdin: "继续、输入或终止命令", context: "跨会话交接与检查点", status: "查看服务状态",
  code: "组合只读工具 · 实验性",
};

function renderTools(core) {
  const tools = core.tools || [];
  const calls = core.stats?.calls || {};
  const signature = JSON.stringify([tools, calls]);
  text("tools-total", `${tools.length} 个`);
  if (signature === state.toolsSignature) return;
  state.toolsSignature = signature;
  const rows = tools.map((tool) => {
    const row = element("div", "tool-row");
    const name = element("code", "", tool.name);
    const description = element("p", "", toolDescriptions[tool.name] || tool.description);
    row.append(name, description, element("span", "tool-calls", `${calls[tool.name] || 0} 次调用`));
    return row;
  });
  $("tools-list").replaceChildren(...(rows.length ? rows : [element("p", "empty-state", "暂无可用工具")]));
}

function renderStatus(data) {
  const { core, mioweave: bridge, tunnel } = data;
  if (!state.status || $("bridge-url-input").value === state.savedURL) $("bridge-url-input").value = bridge.url || "";
  state.savedURL = bridge.url || "";
  state.status = data;
  pill("core-status", "运行中", "success");
  text("core-endpoint", core.endpoint);
  text("core-workspace", core.workspace);
  $("copy-endpoint").disabled = !core.endpoint;
  $("copy-workspace").disabled = !core.workspace;
  text("core-access", { local: "仅本机", token: "Token 验证", unsafe: "开放访问" }[core.remote] || core.remote);
  text("core-tool-count", core.tools?.length ?? 0);
  text("core-call-count", core.stats?.total ?? 0);
  renderTools(core);

  const authorized = bridge.connected && bridge.authorized;
  pill("bridge-status", authorized ? "已连接" : bridge.connected ? "待授权" : "未连接", authorized ? "success" : bridge.connected ? "warning" : "neutral");
  text("bridge-summary", authorized ? `${bridge.tools} 个上游工具` : bridge.connected ? "Bridge 可用，尚未授权" : "可选的浏览器工具连接");
  text("bridge-url", bridge.url || "未配置");
  text("bridge-token-state", bridge.token_configured ? "已配置" : "未配置");
  const bridgeHint = authorized ? "" : !bridge.connected ? "检查 Bridge 是否启动，或在下方设置连接。" : !bridge.token_configured ? "导入 Bridge 已有 Token 以完成授权。" : "授权未完成，请确认 Bridge 与扩展使用同一 Token。";
  message("bridge-hint", bridgeHint);
  text("bridge-diagnostic", bridge.error || "无异常");

  const unchecked = tunnel.configured && tunnel.admin_transport !== "unix";
  pill("tunnel-status", !tunnel.configured ? "未启用" : unchecked ? "未检查" : !tunnel.connected ? "离线" : tunnel.ready ? "就绪" : "未就绪", !tunnel.configured || unchecked ? "neutral" : !tunnel.connected ? "error" : tunnel.ready ? "success" : "warning");
  text("tunnel-summary", !tunnel.configured ? "用于远程 MCP 客户端" : { openai: "OpenAI Secure MCP Tunnel", custom: "自有 HTTPS 端点" }[core.tunnel] || "远程 MCP 隧道");
  $("tunnel-details").hidden = !tunnel.configured;
  message("tunnel-hint", !tunnel.configured ? "本机工具不受影响。" : unchecked ? "配置私有 Socket 后可查看隧道状态。" : !tunnel.connected ? "检查隧道服务与 Socket 配置。" : tunnel.ready ? "请在 ChatGPT 中调用工具确认连接。" : "在诊断详情中查看通道状态。");
  text("tunnel-transport", tunnel.admin_transport === "unix" ? "私有 Unix Socket" : "未配置");
  text("tunnel-socket", tunnel.socket_path || "未配置");
  text("tunnel-version", tunnel.version || "未知");
  message("tunnel-error", tunnel.error || "");
  $("tunnel-channels").replaceChildren(...(tunnel.channels || []).map((channel) => {
    const row = element("div", "channel-row");
    row.append(element("code", "", channel.name), element("span", "", channel.enabled ? "已启用" : "未启用"));
    row.append(element("p", "", [channel.transport_kind, channel.reason].filter(Boolean).join(" · ")));
    return row;
  }));

  $("token-file-input").value = bridge.token_file || "";
  $("copy-token-path").disabled = !bridge.token_file;
  $("token-display").placeholder = bridge.token_configured ? "点击显示后查看" : "尚未配置";
  message("token-source-warning", bridge.token_source === "environment" ? "Bridge 正在使用环境变量；此处显示的是文件值，不代表正在使用的凭据。" : bridge.token_source === "unknown" && bridge.token_configured ? "尚未确认 Bridge 的凭据来源，此处仅显示文件值。" : "");
  tokenControls();
  updateSave();
  text("core-version", `${core.version} · ${core.platform}`);
  text("core-uptime", duration(core.uptime_seconds));
  text("config-path", data.config_path);
  text("logging-config", `${data.logging.level} · ${data.logging.format}`);
  text("last-updated", `更新于 ${clock()}`);
  $("status-error").hidden = true;
  $("connection-cards").dataset.stale = "false";
}

async function loadStatus() {
  if (document.hidden || state.saving) return;
  state.statusController?.abort();
  const controller = new AbortController();
  state.statusController = controller;
  const generation = ++state.statusGeneration;
  try {
    const data = await request("/api/panel/status", { signal: controller.signal });
    if (generation === state.statusGeneration && !document.hidden) renderStatus(data);
  } catch (error) {
    if (error.name === "AbortError" || generation !== state.statusGeneration) return;
    text("status-error-text", state.status ? "无法更新状态，以下为上次结果。" : errorText(error));
    $("status-error").hidden = false;
    $("connection-cards").dataset.stale = "true";
    if (!state.status) {
      for (const id of ["core-status", "bridge-status", "tunnel-status"]) pill(id, "未读取", "neutral");
    }
  } finally {
    if (generation === state.statusGeneration) state.statusController = null;
  }
}
$("retry-status").addEventListener("click", loadStatus);
$("copy-endpoint").addEventListener("click", () => copy(state.status?.core.endpoint).catch((e) => toast(errorText(e))));
$("copy-workspace").addEventListener("click", () => copy(state.status?.core.workspace).catch((e) => toast(errorText(e))));
$("copy-token-path").addEventListener("click", () => copy($("token-file-input").value).catch((e) => toast(errorText(e))));

function tokenControls() {
  const disabled = !state.status?.mioweave.token_configured || state.tokenBusy || state.saving;
  $("reveal-token").disabled = disabled;
  $("copy-token").disabled = disabled;
  text("reveal-token", state.secret ? "隐藏" : "显示");
}

function hideToken() {
  state.secretEpoch++;
  state.tokenController?.abort();
  state.tokenController = null;
  state.tokenBusy = false;
  clearTimeout(state.secretTimer);
  state.secret = "";
  $("token-display").value = "";
  $("token-display").type = "password";
  tokenControls();
}

function clearSensitive() {
  // Clear known secrets from a pending search before discarding the secret itself.
  const query = $("log-query");
  for (const secret of [state.secret, $("new-token").value.trim()]) {
    if (secret && query.value.includes(secret)) query.value = "";
  }
  hideToken();
  $("new-token").value = "";
  message("token-message", "");
  updateSave();
}

async function useToken(action) {
  if (action === "reveal" && state.secret) {
    hideToken();
    return;
  }
  if (state.tokenBusy || state.saving || !$("bridge-settings").open) return;
  const epoch = ++state.secretEpoch;
  const controller = new AbortController();
  state.tokenController = controller;
  state.tokenBusy = true;
  tokenControls();
  message("token-message", "");
  try {
    const data = state.secret ? { token: state.secret } : await request("/api/panel/mioweave/token", { body: {}, signal: controller.signal });
    if (epoch !== state.secretEpoch || document.hidden || state.view !== "overview" || !$("bridge-settings").open) return;
    if (action === "copy") await copy(data.token);
    else {
      state.secret = data.token;
      $("token-display").type = "text";
      $("token-display").value = data.token;
      clearTimeout(state.secretTimer);
      state.secretTimer = setTimeout(hideToken, 30000);
    }
  } catch (error) {
    if (epoch === state.secretEpoch && error.name !== "AbortError") message("token-message", errorText(error), "error");
  } finally {
    if (epoch === state.secretEpoch) {
      state.tokenBusy = false;
      state.tokenController = null;
      tokenControls();
    }
  }
}
$("reveal-token").addEventListener("click", () => useToken("reveal"));
$("copy-token").addEventListener("click", () => useToken("copy"));
$("bridge-settings").addEventListener("toggle", () => { if (!$("bridge-settings").open) clearSensitive(); });
$("manage-bridge").addEventListener("click", () => {
  $("bridge-settings").open = true;
  $("bridge-url-input").focus();
});

function updateSave() {
  const dirty = $("bridge-url-input").value.trim() !== state.savedURL || !!$("new-token").value.trim();
  $("save-bridge").disabled = !state.status || !dirty || state.saving;
}
for (const id of ["bridge-url-input", "new-token"]) {
  $(id).addEventListener("input", () => {
    updateSave();
    message("bridge-form-message", "");
  });
}
$("bridge-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  if ($("save-bridge").disabled) return;
  const body = { url: $("bridge-url-input").value.trim(), token_file: state.status.mioweave.token_file };
  const token = $("new-token").value.trim();
  if (token) body.token = token;
  const source = state.status.mioweave.token_source;
  state.saving = true;
  state.statusGeneration++;
  state.statusController?.abort();
  hideToken();
  $("bridge-fields").disabled = true;
  updateSave();
  text("save-bridge", "保存中…");
  message("bridge-form-message", "");
  try {
    const result = await request("/api/panel/mioweave", { body });
    $("new-token").value = "";
    state.savedURL = body.url;
    const notice = result.restart_required ? `已保存。将 Bridge 指向此 Token 文件并重启。${token ? "请同步扩展 Token。" : ""}` : token ? source === "file" ? "Token 已更新，请同步到扩展。" : "Token 已保存。请确认 Bridge 使用此文件，并同步扩展。" : "已保存";
    message("bridge-form-message", notice, result.restart_required ? "warning" : "success");
  } catch (error) {
    // The request's token may already have been cleared by a view/visibility change.
    const safe = token ? errorText(error).split(token).join("[已隐藏]") : errorText(error);
    message("bridge-form-message", safe, "error");
  } finally {
    state.saving = false;
    $("bridge-fields").disabled = false;
    text("save-bridge", "保存更改");
    tokenControls();
    updateSave();
    loadStatus();
  }
});

function filterSummary() {
  const select = $("log-since");
  text("filter-summary", `${select.options[select.selectedIndex].text} · ${$("log-lines").value} 条`);
}

function resetFilters() {
  $("log-source").value = "all";
  $("log-level").value = "info";
  $("log-since").value = "10m";
  $("log-lines").value = "100";
  $("log-query").value = "";
  filterSummary();
  $("log-query").focus({ preventScroll: true });
  loadLogs();
}

function renderLogs(records, filtered, unavailable) {
  const signature = JSON.stringify([records, filtered, unavailable]);
  if (signature === state.logsSignature) return;
  state.logsSignature = signature;
  const open = new Set([...$("logs-list").querySelectorAll("details[open]")].map((node) => node.dataset.key));
  const focused = document.activeElement?.closest(".log-entry")?.dataset.key;
  const occurrences = new Map();
  let focusTarget;
  const rows = [...records].reverse().map((record) => {
    const fields = record.fields && Object.keys(record.fields).length > 0;
    const entry = element(fields ? "details" : "div", "log-entry");
    const identity = JSON.stringify(record);
    const occurrence = (occurrences.get(identity) || 0) + 1;
    occurrences.set(identity, occurrence);
    entry.dataset.key = identity + "#" + occurrence;
    if (fields) entry.open = open.has(entry.dataset.key);
    const line = element(fields ? "summary" : "div", "log-line");
    const date = new Date(record.time);
    const validDate = !Number.isNaN(date.getTime());
    const time = element("time", "", validDate ? date.toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false }) : record.time || "时间未知");
    if (validDate) time.dateTime = record.time;
    time.title = record.time || "";
    const level = element("span", "log-level", (record.level || "info").toUpperCase());
    level.dataset.level = record.level;
    line.append(time, element("span", "log-source", { core: "miodesk", tunnel: "Tunnel", mioweave: "mioweave" }[record.source] || record.source), level, element("span", "log-message", record.message));
    entry.append(line);
    if (fields) entry.append(element("pre", "log-fields", JSON.stringify(record.fields, null, 2)));
    if (fields && entry.dataset.key === focused) focusTarget = line;
    return entry;
  });
  if (!rows.length) {
    const empty = element("div", "empty-state", unavailable ? "无法读取日志，请检查系统日志服务。" : filtered ? "没有符合筛选条件的日志。" : "此时间范围内没有日志。");
    if (filtered && !unavailable) {
      const reset = element("button", "text-button", "重置筛选");
      reset.type = "button";
      reset.addEventListener("click", resetFilters);
      empty.append(element("br"), reset);
    }
    rows.push(empty);
  }
  $("logs-list").replaceChildren(...rows);
  focusTarget?.focus({ preventScroll: true });
}

async function loadLogs(automatic = false) {
  if (state.view !== "logs" || document.hidden) return;
  if (automatic && (state.logsController || state.queryTimer)) return;
  clearTimeout(state.queryTimer);
  state.queryTimer = null;
  state.logsController?.abort();
  const generation = ++state.logsGeneration;
  const query = $("log-query").value.trim();
  for (const secret of [state.secret, $("new-token").value.trim()]) {
    if (secret && query.includes(secret)) {
      message("logs-message", "请移除搜索中的 Token 后重试。", "error");
      return;
    }
  }
  const params = new URLSearchParams({ source: $("log-source").value, level: $("log-level").value, since: $("log-since").value, lines: $("log-lines").value, query });
  const filter = params.toString();
  const changed = filter !== state.logsFilter;
  const filtered = params.get("source") !== "all" || params.get("level") !== "info" || params.get("since") !== "10m" || params.get("lines") !== "100" || !!query;
  const controller = new AbortController();
  state.logsController = controller;
  $("logs-list").setAttribute("aria-busy", "true");
  if (changed) {
    $("logs-list").replaceChildren(element("p", "empty-state", "读取中…"));
    state.logsSignature = "";
    text("log-count", "读取中…");
    text("log-updated", "");
    message("logs-message", "");
  }
  try {
    const data = await request("/api/panel/logs?" + filter, { signal: controller.signal });
    if (generation !== state.logsGeneration || document.hidden || state.view !== "logs") return;
    state.logsFilter = filter;
    const unavailable = !!data.notice && !data.notice.includes("1 MiB limit");
    const notice = unavailable ? "部分日志不可用。请检查 systemd 用户日志；前台服务请查看终端。" : data.notice ? "日志已达响应上限，请缩短时间范围或增加筛选。" : "";
    message("logs-message", notice, unavailable ? "error" : "");
    $("logs-message").classList.toggle("error", unavailable);
    renderLogs(data.records || [], filtered, unavailable);
    text("log-count", `${data.records?.length || 0} 条${unavailable ? " · 部分来源不可用" : ""}`);
    text("log-updated", `更新于 ${clock()}`);
  } catch (error) {
    if (error.name === "AbortError" || generation !== state.logsGeneration) return;
    message("logs-message", errorText(error), "error");
    $("logs-message").classList.add("error");
    if (changed || !state.logsSignature) {
      renderLogs([], filtered, true);
      text("log-count", "读取失败");
    } else text("log-count", "更新失败 · 显示上次结果");
  } finally {
    if (generation === state.logsGeneration) {
      state.logsController = null;
      $("logs-list").setAttribute("aria-busy", "false");
    }
  }
}

function scheduleLogs() {
  clearTimeout(state.queryTimer);
  state.logsGeneration++;
  state.logsController?.abort();
  state.logsController = null;
  state.queryTimer = setTimeout(() => {
    state.queryTimer = null;
    loadLogs();
  }, 250);
}
$("log-query").addEventListener("input", scheduleLogs);
for (const id of ["log-source", "log-level", "log-since", "log-lines"]) {
  $(id).addEventListener("change", () => { filterSummary(); loadLogs(); });
}
$("log-filters").addEventListener("submit", (event) => { event.preventDefault(); loadLogs(); });
$("reset-filters").addEventListener("click", resetFilters);
$("refresh-logs").addEventListener("click", () => loadLogs());
$("auto-logs").addEventListener("change", () => {
  if ($("auto-logs").checked) loadLogs();
  else {
    state.logsGeneration++;
    state.logsController?.abort();
    state.logsController = null;
    $("logs-list").setAttribute("aria-busy", "false");
  }
});

function suspend() {
  clearSensitive();
  clearTimeout(state.queryTimer);
  state.queryTimer = null;
  state.statusGeneration++;
  state.logsGeneration++;
  state.statusController?.abort();
  state.logsController?.abort();
  state.statusController = null;
  state.logsController = null;
}
document.addEventListener("visibilitychange", () => {
  if (document.hidden) suspend();
  else {
    loadStatus();
    if (state.view === "logs" && $("auto-logs").checked) loadLogs();
  }
});
window.addEventListener("pagehide", suspend);
window.addEventListener("pageshow", (event) => {
  if (event.persisted) {
    loadStatus();
    if (state.view === "logs") loadLogs();
  }
});
setInterval(loadStatus, 15000);
setInterval(() => { if ($("auto-logs").checked) loadLogs(true); }, 5000);
loadStatus();
