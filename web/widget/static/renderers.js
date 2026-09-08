/* miodesk widget renderers — one shared set of primitives, one renderer per
   tool result kind. Data contracts come from the tools' structuredContent;
   every payload carries a "kind" discriminator. Rendering uses DOM APIs with
   textContent everywhere: tool output is data, never markup. */

"use strict";

function mEl(tag, cls, text) {
  const node = document.createElement(tag);
  if (cls) node.className = cls;
  if (text != null) node.textContent = text;
  return node;
}

function mIcon(name) { return icon(name, "ic"); }

/* ---------- shared primitives ---------- */

function rhead(icName, action, target, meta) {
  const head = mEl("div", "rhead");
  head.append(mIcon(icName), mEl("span", "action", action));
  if (target) head.append(mEl("span", "target", target));
  if (meta) head.append(mEl("span", "meta", meta));
  return head;
}

function stateLine(kind, iconName, text) {
  const line = mEl("p", "state state--" + kind);
  line.setAttribute("role", "status");
  line.append(icon(iconName, "ic"), mEl("span", null, text));
  return line;
}

function note(text) { return mEl("p", "note", text); }

function emptyState(text) {
  return stateLine("info", "dot", text);
}

// codeSurface renders text with preserved whitespace, bounded height, and an
// optional copy button.
function codeSurface(text, label) {
  const box = mEl("div", "code");
  if (label) {
    const bar = mEl("div", "code__bar");
    bar.append(mEl("span", "label", label));
    bar.append(copyButton(text));
    box.append(bar);
  }
  const pre = mEl("pre");
  pre.textContent = text;
  box.append(pre);
  return box;
}

function copyButton(text) {
  const btn = mEl("button", null, "Copy");
  btn.type = "button";
  btn.setAttribute("aria-label", "Copy to clipboard");
  btn.addEventListener("click", () => {
    const done = () => {
      btn.textContent = "Copied";
      setTimeout(() => { btn.textContent = "Copy"; }, 1500);
    };
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(done, done);
    } else {
      const ta = mEl("textarea");
      ta.value = text;
      document.body.append(ta);
      ta.select();
      document.execCommand("copy");
      ta.remove();
      done();
    }
  });
  return btn;
}

function kvList(pairs) {
  const dl = mEl("dl", "kv");
  for (const [k, v] of pairs) {
    dl.append(mEl("dt", null, k), mEl("dd", null, v == null || v === "" ? "—" : String(v)));
  }
  return dl;
}

function fmtBytes(n) {
  if (n == null) return "";
  if (n < 1024) return n + " B";
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + " KB";
  return (n / 1024 / 1024).toFixed(1) + " MB";
}

// showMore wraps a container and reveals rows progressively.
function showMore(container, batchSize) {
  const children = Array.from(container.children);
  if (children.length <= batchSize) return container;
  const hidden = children.slice(batchSize);
  hidden.forEach((c) => { c.hidden = true; });
  const btn = mEl("button", null, `Show ${hidden.length} more`);
  btn.type = "button";
  btn.addEventListener("click", () => {
    hidden.slice(0, batchSize).forEach((c) => { c.hidden = false; });
    hidden.splice(0, batchSize);
    if (!hidden.length) btn.remove();
    else btn.textContent = `Show ${hidden.length} more`;
  });
  container.append(btn);
  return container;
}

/* ---------- per-kind renderers: data -> DOM ---------- */

const MIODESK_RENDERERS = {

  read(data) {
    const root = mEl("div", "result");
    root.append(rhead("file", "Read", data.path,
      [fmtBytes(data.size), `lines ${data.offset}–${data.offset + data.lines - 1}`].filter(Boolean).join(" · ")));
    const content = data.content || "";
    if (content) {
      root.append(lineNumbers(content, data.offset || 1));
    } else {
      root.append(emptyState("empty file"));
    }
    if (data.truncated) {
      root.append(note("Truncated — the file continues beyond this window; read again with offset/limit for more."));
    }
    return root;
  },

  search(data) {
    const root = mEl("div", "result");
    const matches = data.matches || [];
    const files = new Set(matches.map((m) => m.file)).size;
    root.append(rhead("search", "Search", data.query,
      `${matches.length} match${matches.length === 1 ? "" : "es"} · ${files} file${files === 1 ? "" : "s"}` +
      (data.engine ? ` · ${data.engine}` : "")));
    if (!matches.length) {
      root.append(emptyState("no matches"));
      return root;
    }
    if (data.truncated) root.append(note(`Result capped — showing the first ${matches.length} matches; narrow the query or raise max_results.`));

    const byFile = new Map();
    for (const m of matches) {
      if (!byFile.has(m.file)) byFile.set(m.file, []);
      byFile.get(m.file).push(m);
    }
    const groups = mEl("div", "rowlist");
    for (const [file, ms] of byFile) {
      const group = mEl("div", "search-group");
      const row = mEl("div", "row");
      row.append(mIcon("file"), mEl("span", "name", file),
        mEl("span", "fill"), mEl("span", "meta", `${ms.length} match${ms.length === 1 ? "" : "es"}`));
      const detail = mEl("div", null);
      for (const m of ms) {
        const line = mEl("div", "match-line");
        line.append(mEl("span", "no", String(m.line)), mEl("span", "txt", m.text));
        detail.append(line);
      }
      group.append(row, detail);
      groups.append(group);
    }
    root.append(showMore(groups, 12));
    return root;
  },

  list(data) {
    const root = mEl("div", "result");
    const entries = data.entries || [];
    root.append(rhead("folder", "List", data.path || ".",
      `${entries.length} entr${entries.length === 1 ? "y" : "ies"}` + (data.truncated ? " · truncated" : "")));
    if (!entries.length) {
      root.append(emptyState("empty directory"));
      return root;
    }
    const rows = mEl("div", "rowlist");
    for (const e of entries) {
      const row = mEl("div", "row");
      const depth = e.path.split("/").length - 1;
      row.style.paddingLeft = depth * 14 + "px";
      row.append(mIcon(e.kind === "dir" ? "folder" : e.kind === "symlink" ? "link" : "file"),
        mEl("span", "name", e.name), mEl("span", "fill"));
      if (e.size) row.append(mEl("span", "meta", fmtBytes(e.size)));
      rows.append(row);
    }
    root.append(showMore(rows, 60));
    if (data.truncated) root.append(note("Entry cap reached — raise the limit or list a subdirectory."));
    return root;
  },

  write(data) {
    const root = mEl("div", "result");
    root.append(rhead("pencil", data.created ? "Created" : "Updated", data.path, fmtBytes(data.bytes)));
    root.append(stateLine("ok", "check", data.created ? "File created." : "File updated."));
    return root;
  },

  delete(data) {
    const root = mEl("div", "result");
    const what = data.target === "dir" ? "Directory" : data.target === "symlink" ? "Symlink" : "File";
    root.append(rhead("trash", "Deleted", data.path, what.toLowerCase()));
    root.append(stateLine("ok", "check", `${what} removed.`));
    return root;
  },

  edit(data) {
    const root = mEl("div", "result");
    root.append(rhead("pencil", "Edited", `${(data.files || []).length} file(s)`, `${data.applied ?? 0} operations`));
    if (!data.files || !data.files.length) {
      root.append(emptyState("nothing changed"));
      return root;
    }
    const stack = mEl("div", "stack");
    for (const f of data.files) {
      const block = mEl("div", "subresult");
      block.append(rhead("file", "Edit", f.path, `${f.bytes ? fmtBytes(f.bytes) : ""}`));
      for (const group of f.diff || []) block.append(hunkBlock(f, group));
      stack.append(block);
    }
    root.append(stack);
    return root;
  },

  command(data) {
    const root = mEl("div", "result");
    const st = commandState(data);
    root.append(rhead("terminal", "Command", data.command || data.id, st.meta));
    root.append(stateLine(st.cls, st.icon, st.text));
    if (data.stdout) root.append(codeSurface(data.stdout, "stdout" + (data.stdout_truncated ? " (truncated)" : "")));
    if (data.stderr) root.append(codeSurface(data.stderr, "stderr" + (data.stderr_truncated ? " (truncated)" : "")));
    if (!data.stdout && !data.stderr) root.append(emptyState("no output"));
    return root;
  },

  status(data) {
    const root = mEl("div", "result");
    root.append(stateLine("ok", "check", "miodesk is running"));
    root.append(kvList([
      ["workspace", data.workspace],
      ["endpoint", data.endpoint],
      ["remote access", data.remote],
      ["tunnel", data.tunnel],
      ["tool calls", data.stats ? data.stats.total : 0],
      ["uptime", formatUptime(data.uptime_seconds)],
      ["version", data.version ? `${data.version} (${data.platform})` : null],
    ]));
    return root;
  },

  unknown(data) {
    const root = mEl("div", "result");
    root.append(emptyState("no renderer for this result kind"));
    root.append(codeSurface(JSON.stringify(data, null, 2)));
    return root;
  },
};

/* ---------- command lifecycle states ---------- */

function commandState(data) {
  const meta = [];
  if (data.elapsed_ms != null) meta.push((data.elapsed_ms / 1000).toFixed(data.elapsed_ms < 1000 ? 1 : 0) + "s");
  if (data.exit_code !== null && data.exit_code !== undefined && data.exit_code >= 0) meta.push(`exit ${data.exit_code}`);
  if (data.id) meta.push(data.id);

  if (data.status === "running") {
    return { cls: "info", icon: "dot", text: "Running…", meta: meta.join(" · ") };
  }
  if (data.exit_code === null || data.exit_code === undefined) {
    // A task that has not reported an exit code yet (command_start).
    return { cls: "info", icon: "play", text: "Started.", meta: meta.join(" · ") };
  }
  if (data.timed_out) {
    return { cls: "fail", icon: "clock", text: "Timed out — the process was killed.", meta: meta.join(" · ") };
  }
  if (data.exit_code !== null && data.exit_code !== undefined && data.exit_code < 0) {
    return { cls: "warn", icon: "x", text: "Cancelled.", meta: meta.join(" · ") };
  }
  if (data.exit_code === 0) {
    return { cls: "ok", icon: "check", text: "Passed.", meta: meta.join(" · ") };
  }
  return { cls: "fail", icon: "alert", text: `Failed with exit code ${data.exit_code}.`, meta: meta.join(" · ") };
}

/* ---------- diff viewer (edit renderer) ---------- */

function countDiffType(groups, type) {
  let n = 0;
  for (const g of groups || []) for (const l of g.lines) if (l.type === type) n++;
  return n;
}

function hunkBlock(file, group) {
  const box = mEl("details", "diff");
  box.open = true;

  const adds = countDiffType([group], "add");
  const dels = countDiffType([group], "del");
  const summary = mEl("summary");
  summary.append(mEl("span", "diff__path", group.header || ""), statSpan(adds, dels));

  const body = mEl("div", "diff__body");
  const pos = mEl("span", "diff__pos");
  const bar = mEl("div", "diff__bar");
  const prev = mEl("button", null, "↑");
  const next = mEl("button", null, "↓");
  const close = mEl("button", null, "×");
  prev.type = next.type = close.type = "button";
  prev.title = "previous change";
  next.title = "next change";
  prev.setAttribute("aria-label", "previous change");
  next.setAttribute("aria-label", "next change");
  close.title = "hide navigation";
  close.setAttribute("aria-label", "hide navigation");
  bar.append(pos, prev, next, close);

  const scroll = mEl("div", "diff__scroll");
  const lines = mEl("div", "dlines");
  const changeRuns = [];
  let run = [];
  const flushRun = () => { if (run.length) changeRuns.push(run); run = []; };
  for (const line of group.lines || []) {
    const row = lineRow(line);
    if (line.type === "add" || line.type === "del") run.push(row);
    else flushRun();
    lines.append(row);
  }
  flushRun();
  scroll.append(lines);
  body.append(bar, scroll);
  box.append(summary, body);

  let current = -1;
  function show(i) {
    if (!changeRuns.length) return;
    if (current >= 0) for (const r of changeRuns[current]) r.classList.remove("dl--target");
    current = (i + changeRuns.length) % changeRuns.length;
    const rows = changeRuns[current];
    for (const r of rows) r.classList.add("dl--target");
    const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    scroll.scrollTo({ top: rows[0].offsetTop - scroll.clientHeight / 3, behavior: reduceMotion ? "auto" : "smooth" });
    pos.textContent = `${current + 1}/${changeRuns.length}`;
    prev.disabled = next.disabled = changeRuns.length < 2;
  }
  prev.addEventListener("click", () => show(current - 1 < 0 ? changeRuns.length - 1 : current - 1));
  next.addEventListener("click", () => show(current + 1 >= changeRuns.length ? 0 : current + 1));
  close.addEventListener("click", () => { bar.hidden = true; });
  if (!changeRuns.length) prev.disabled = next.disabled = true;
  return box;
}

function statSpan(adds, dels) {
  const stat = mEl("span", "diff__stat");
  stat.append(mEl("span", "plus", `+${adds}`), document.createTextNode(" "), mEl("span", "minus", `−${dels}`));
  return stat;
}

function lineRow(line) {
  const cls = line.type === "add" ? "dl--add" : line.type === "del" ? "dl--del" : "";
  const row = mEl("div", "dl" + (cls ? " " + cls : ""));
  row.append(
    mEl("span", "dl__no", line.old ? String(line.old) : ""),
    mEl("span", "dl__no", line.new ? String(line.new) : ""),
    mEl("span", "dl__text", line.text),
  );
  return row;
}

// lineNumbers renders content with a gutter; startLine is the payload's
// 1-based offset so numbers stay true to the source file.
function lineNumbers(content, startLine) {
  const box = mEl("div", "code code--lines");
  const wrap = mEl("div", "dlines");
  const lines = content.replace(/\n$/, "").split("\n");
  lines.forEach((text, i) => {
    const row = mEl("div", "dl");
    row.append(
      mEl("span", "dl__no", String((startLine || 1) + i)),
      mEl("span", "dl__no", ""),
      mEl("span", "dl__text", text),
    );
    wrap.append(row);
  });
  box.append(wrap);
  return box;
}

function formatUptime(seconds) {
  if (seconds == null) return "—";
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  return m > 0 ? `${m}m ${s}s` : `${s}s`;
}

/* ---------- dispatch ---------- */

function resultRenderer(kind) {
  if (typeof kind === "string" && Object.prototype.hasOwnProperty.call(MIODESK_RENDERERS, kind)) {
    return MIODESK_RENDERERS[kind];
  }
  return MIODESK_RENDERERS.unknown;
}

function renderResult(data) {
  const host = document.getElementById("result");
  if (!host) return;
  host.replaceChildren();
  if (!data || typeof data !== "object") {
    host.append(emptyState("no data"));
    return;
  }
  host.append(resultRenderer(data.kind)(data));
}
