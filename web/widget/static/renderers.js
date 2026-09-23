/* miodesk diagnostics renderer.
   The embedded MCP App is intentionally status-only; normal tool and task
   results stay in the host's native UI. */

"use strict";

function mEl(tag, cls, text) {
  const node = document.createElement(tag);
  if (cls) node.className = cls;
  if (text != null) node.textContent = text;
  return node;
}

function mIcon(name) { return icon(name, "ic"); }

function stateLine(kind, iconName, text) {
  const line = mEl("p", "state state--" + kind);
  line.setAttribute("role", "status");
  line.append(mIcon(iconName), mEl("span", null, text));
  return line;
}

function note(text) { return mEl("p", "note", text); }

function kvList(pairs) {
  const dl = mEl("dl", "kv");
  for (const [key, value] of pairs) {
    dl.append(
      mEl("dt", null, key),
      mEl("dd", null, value == null || value === "" ? "—" : String(value)),
    );
  }
  return dl;
}

function formatUptime(seconds) {
  if (seconds == null) return "—";
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  return m > 0 ? `${m}m ${s}s` : `${s}s`;
}

function renderStatus(data) {
  const root = mEl("div", "result");
  const calls = data.tool_calls != null ? data.tool_calls : (data.stats ? data.stats.total : 0);
  const connection = [data.remote, data.tunnel && `${data.tunnel} tunnel`].filter(Boolean).join(" · ");

  root.append(stateLine("ok", "check", "miodesk is running"));
  root.append(kvList([
    ["workspace", data.workspace],
    ["endpoint", data.endpoint],
    ["connection", connection],
  ]));
  root.append(note([
    `${calls || 0} tool call${calls === 1 ? "" : "s"}`,
    `up ${formatUptime(data.uptime_seconds)}`,
    data.version ? `v${data.version}` : "",
    data.platform || "",
  ].filter(Boolean).join(" · ")));
  return root;
}

function renderResult(data) {
  const host = document.getElementById("result");
  if (!host) return;
  host.replaceChildren();

  if (!data || typeof data !== "object" || data.kind !== "status") {
    host.append(stateLine("info", "dot", "diagnostics unavailable"));
    return;
  }
  host.append(renderStatus(data));
}
