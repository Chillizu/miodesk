(() => {
  'use strict';
  const $ = (id) => document.getElementById(id);
  const state = { status: null, timer: 0, toast: 0, secretTimer: 0, secret: '', secretEpoch: 0, statusController: null, logsController: null, statusBusy: false, logsBusy: false };
  const text = (id, value) => { $(id).textContent = value == null || value === '' ? '—' : String(value); };
  const safe = (value) => value == null ? '' : String(value);
  const safeError = (error) => { let message = safe(error && error.message) || '请求失败'; for (const secret of [state.secret, $('new-token')?.value]) if (secret) message = message.split(secret).join('[已隐藏]'); return message; };
  const number = (value) => Number.isFinite(Number(value)) ? Number(value).toLocaleString('zh-CN') : '0';
  const setPill = (id, labelId, label, kind) => { const pill = $(id); pill.className = `status-pill ${kind}`; text(labelId, label); };
  const toast = (message) => { const box = $('toast'); box.textContent = message; box.classList.add('show'); clearTimeout(state.toast); state.toast = setTimeout(() => box.classList.remove('show'), 1800); };
  async function fetchJSON(url, options = {}, slot) {
    if (slot === 'status' && state.statusController) state.statusController.abort();
    if (slot === 'logs' && state.logsController) state.logsController.abort();
    const controller = new AbortController();
    if (slot === 'status') state.statusController = controller;
    if (slot === 'logs') state.logsController = controller;
    const timeout = setTimeout(() => controller.abort(), 9000);
    try {
      const response = await fetch(url, { ...options, signal: controller.signal, cache: 'no-store', credentials: 'same-origin', redirect: 'error' });
      let data;
      try { data = await response.json(); } catch (_) { throw new Error(`服务器返回无效响应 (${response.status})`); }
      if (!response.ok) throw new Error(safe(data && data.error) || `请求失败 (${response.status})`);
      return data;
    } finally { clearTimeout(timeout); }
  }
  function renderStatus(data) {
    state.status = data;
    const core = data.core || {};
    const connected = !!(core.endpoint || core.kind || core.name);
    setPill('core-status', 'core-status-text', connected ? '运行中' : '不可用', connected ? 'ok' : 'bad');
    text('core-kind', core.platform || 'Core');
    text('core-endpoint', core.endpoint);
    text('core-workspace', core.workspace);
    const accessModes = { local: '本机（local）', token: '令牌访问（token）', unsafe: '不安全开放（unsafe）' };
    text('core-access', accessModes[core.remote] || safe(core.remote) || '未知');
    const up = Number(core.uptime_seconds);
    const uptime = Number.isFinite(up) && up >= 0 ? (up >= 86400 ? `${Math.floor(up / 86400)} 天 ${Math.floor(up % 86400 / 3600)} 小时` : up >= 3600 ? `${Math.floor(up / 3600)} 小时 ${Math.floor(up % 3600 / 60)} 分` : `${Math.floor(up / 60)} 分钟`) : '—';
    text('core-version-uptime', [core.version, uptime].filter(Boolean).join(' · ') || '—');
    const tools = Array.isArray(core.tools) ? core.tools : [];
    text('core-tool-count', tools.length);
    const calls = core.stats && core.stats.calls || {};
    const total = core.stats && core.stats.total;
    text('core-call-count', number(total));
    text('core-call-stats', Object.entries(calls).length ? Object.entries(calls).map(([name, count]) => `${name}: ${number(count)}`).join(' · ') : '暂无');
    const list = $('tools-list'); list.replaceChildren();
    if (!tools.length) { const p = document.createElement('p'); p.className = 'muted'; p.textContent = '当前没有可用工具信息'; list.append(p); }
    tools.forEach((tool) => { const article = document.createElement('article'); article.className = 'tool-item'; const name = document.createElement('strong'); name.textContent = safe(tool.name) || '未命名工具'; const desc = document.createElement('p'); desc.textContent = safe(tool.description) || '无描述'; article.append(name, desc); list.append(article); });
    text('tools-total', `${tools.length} 项`);
    const bridge = data.mioweave || {};
    const isConfigured = !!bridge.token_configured || !!bridge.url;
    setPill('bridge-status', 'bridge-status-text', bridge.authorized ? '已授权' : bridge.connected ? '已连接' : isConfigured ? '未连接' : '未配置（可选）', bridge.authorized ? 'ok' : bridge.connected ? 'warn' : 'pending');
    text('bridge-connected', bridge.connected ? '可连接' : '未连接'); text('bridge-authorized', bridge.authorized ? '已授权' : bridge.connected ? '未授权' : '—'); text('bridge-tools', Number.isFinite(Number(bridge.tools)) ? `${number(bridge.tools)} 项` : '—');
    text('bridge-url', bridge.url); text('bridge-token-file', bridge.token_file || '未配置'); text('bridge-token-source', bridge.token_source || '—');
    $('reveal-token').disabled = !bridge.token_configured;
    const bridgeError = $('bridge-error'); bridgeError.textContent = safe(bridge.error); bridgeError.hidden = !bridge.error;
    if (!$('bridge-url-input').value) $('bridge-url-input').value = safe(bridge.url);
    $('token-file-input').value = safe(bridge.token_file);
    const tunnel = data.tunnel || {};
    const tunnelKind = tunnel.ready ? 'ok' : tunnel.connected ? 'warn' : tunnel.configured ? 'bad' : 'pending';
    const tunnelLabel = !tunnel.configured ? '未配置（可选）' : tunnel.ready ? '已就绪' : tunnel.connected ? '已连接，未就绪' : '不可用';
    setPill('tunnel-status', 'tunnel-status-text', tunnelLabel, tunnelKind);
    text('tunnel-configured', tunnel.configured ? '已配置' : '未配置'); text('tunnel-health', tunnel.connected ? `健康 · ${tunnel.ready ? '就绪' : '未就绪'}` : '未连接'); text('tunnel-transport', tunnel.admin_transport === 'unix' ? `Unix socket · ${tunnel.socket_path || '已配置'}` : '未配置'); text('tunnel-version', tunnel.version || '—'); text('tunnel-notice', [tunnel.notice, tunnel.error].filter(Boolean).map(safe).join(' · '));
    const channels = $('tunnel-channels'); channels.replaceChildren();
    (Array.isArray(tunnel.channels) ? tunnel.channels : []).forEach((channel) => { const item = document.createElement('span'); item.className = `channel ${channel.enabled ? 'enabled' : 'disabled'}`; item.textContent = `${safe(channel.name) || '通道'} · ${channel.enabled ? '启用' : '停用'}${channel.transport_kind ? ` · ${safe(channel.transport_kind)}` : ''}${channel.reason ? ` · ${safe(channel.reason)}` : ''}`; channels.append(item); });
    text('config-path', data.config_path ? `配置：${data.config_path}` : '');
    const logging = data.logging || {}; if (logging.level && $('log-level').dataset.initialized !== 'yes') { const available = [...$('log-level').options].some((o) => o.value === logging.level); if (available) $('log-level').value = logging.level; $('log-level').dataset.initialized = 'yes'; }
    $('status-error').hidden = true; text('last-updated', `状态更新：${new Date().toLocaleTimeString('zh-CN')}`);
  }
  async function loadStatus() {
    if (state.statusBusy || document.hidden) return;
    state.statusBusy = true;
    try { renderStatus(await fetchJSON('/api/panel/status', {}, 'status')); }
    catch (error) { if (error.name !== 'AbortError') { const box = $('status-error'); box.textContent = '无法读取状态，请检查 miodesk Core 是否运行后重试。'; box.hidden = false; setPill('core-status', 'core-status-text', '状态读取失败', 'bad'); } }
    finally { state.statusBusy = false; }
  }
  function clearSecret() { state.secretEpoch++; clearTimeout(state.secretTimer); state.secretTimer = 0; state.secret = ''; $('token-display').value = ''; $('token-display').type = 'password'; $('copy-token').disabled = true; $('reveal-token').textContent = '显示'; $('reveal-token').disabled = !state.status?.mioweave?.token_configured; }
  async function revealToken() {
    if (document.hidden || !state.status?.mioweave?.token_configured) return;
    if (state.secret) { clearSecret(); return; }
    const epoch = ++state.secretEpoch;
    $('reveal-token').disabled = true;
    try {
      const result = await fetchJSON('/api/panel/mioweave/token', { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Miodesk-Panel': '1' }, body: '{}' });
      if (epoch !== state.secretEpoch || document.hidden) return;
      state.secret = safe(result.token); $('token-display').value = state.secret; $('token-display').type = 'text'; $('copy-token').disabled = !state.secret; $('reveal-token').textContent = '隐藏'; state.secretTimer = setTimeout(clearSecret, 30000);
    } catch (_) {
      if (epoch === state.secretEpoch && !document.hidden) toast('无法读取 Token，请检查本机桥接设置。');
    } finally {
      if (epoch === state.secretEpoch) $('reveal-token').disabled = !state.status?.mioweave?.token_configured;
    }
  }
  async function copyText(value) { if (!value) return; try { await navigator.clipboard.writeText(value); toast('已复制'); } catch (_) { toast('复制失败，请检查浏览器权限'); } }
  async function saveBridge(event) {
    event.preventDefault(); const button = $('save-bridge'); button.disabled = true; clearSecret();
    const body = { url: $('bridge-url-input').value.trim(), token_file: $('token-file-input').value };
    const token = $('new-token').value; if (token) body.token = token;
    const message = $('bridge-form-message'); message.textContent = '正在保存…';
    try { const result = await fetchJSON('/api/panel/mioweave', { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Miodesk-Panel': '1' }, body: JSON.stringify(body) }); $('new-token').value = ''; message.textContent = safe(result.notice) || '已保存'; await loadStatus(); }
    catch (error) { message.textContent = `保存失败：${safeError(error)}`; }
    finally { button.disabled = false; }
  }
  function renderLogs(records) {
    const list = $('logs-list'); list.replaceChildren();
    if (!records.length) { const p = document.createElement('p'); p.className = 'muted'; p.textContent = '此筛选条件下没有日志记录。'; list.append(p); }
    records.forEach((record) => {
      const article = document.createElement('article'); article.className = 'log-entry'; const line = document.createElement('div'); line.className = 'log-line';
      const time = document.createElement('time'); time.className = 'log-time'; const originalTime = safe(record.time); const parsedTime = new Date(originalTime); time.dateTime = originalTime; time.title = originalTime; time.textContent = originalTime && !Number.isNaN(parsedTime.getTime()) ? parsedTime.toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' }) : originalTime || '时间未知';
      const source = document.createElement('span'); source.className = 'log-source'; source.textContent = safe(record.source) || 'unknown';
      const level = document.createElement('span'); level.className = `log-level ${safe(record.level).toLowerCase()}`; level.textContent = safe(record.level) || 'info';
      const message = document.createElement('div'); message.className = 'log-message'; message.textContent = safe(record.message);
      line.append(time, source, level, message); article.append(line);
      if (record.fields && typeof record.fields === 'object' && Object.keys(record.fields).length) { const details = document.createElement('details'); const summary = document.createElement('summary'); summary.textContent = '展开字段'; const pre = document.createElement('pre'); try { pre.textContent = JSON.stringify(record.fields, null, 2); } catch (_) { pre.textContent = '[字段无法显示]'; } details.append(summary, pre); article.append(details); }
      list.append(article);
    });
  }
  async function loadLogs() {
    if (state.logsBusy || document.hidden || $('logs-view').hidden) return;
    state.logsBusy = true; let query = $('log-query').value; for (const secret of [state.secret, $('new-token').value]) if (secret) query = query.split(secret).join(''); const params = new URLSearchParams({ source: $('log-source').value, level: $('log-level').value, since: $('log-since').value, lines: $('log-lines').value, query });
    try { const result = await fetchJSON(`/api/panel/logs?${params.toString()}`, {}, 'logs'); const records = Array.isArray(result.records) ? result.records : []; renderLogs(records); text('log-count', `${records.length} 条记录`); const msg = $('logs-message'); msg.textContent = safe(result.notice); msg.hidden = !result.notice; text('log-updated', `更新于 ${new Date().toLocaleTimeString('zh-CN')}`); }
    catch (error) { if (error.name !== 'AbortError') { const msg = $('logs-message'); msg.textContent = '日志读取失败，请确认本机日志服务可用后重试。'; msg.hidden = false; text('log-count', '读取失败'); } }
    finally { state.logsBusy = false; }
  }
  function restartPolling() { clearInterval(state.timer); state.timer = setInterval(() => { if (!document.hidden && !$('pause-logs').checked && !$('logs-view').hidden) loadLogs(); if (!document.hidden) loadStatus(); }, 15000); }
  const themeSelect = $('theme-select'); try { const theme = localStorage.getItem('miodesk-panel-theme'); if (['light', 'dark', 'system'].includes(theme)) themeSelect.value = theme; } catch (_) {}
  const applyTheme = () => { document.documentElement.dataset.theme = themeSelect.value === 'system' ? (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light') : themeSelect.value; try { localStorage.setItem('miodesk-panel-theme', themeSelect.value); } catch (_) {} };
  themeSelect.addEventListener('change', applyTheme); matchMedia('(prefers-color-scheme: dark)').addEventListener?.('change', () => { if (themeSelect.value === 'system') applyTheme(); }); applyTheme();
  const selectView = (view) => {
    clearSecret();
    $('new-token').value = '';
    const logs = view === 'logs';
    document.querySelector('.overview').hidden = logs;
    $('bridge-settings').hidden = logs;
    if (logs) $('bridge-settings').open = false;
    else state.logsController?.abort();
    $('tools-view').hidden = logs;
    $('logs-view').hidden = !logs;
    $('view-tools').classList.toggle('active', !logs);
    $('view-logs').classList.toggle('active', logs);
    $('view-tools').setAttribute('aria-selected', String(!logs));
    $('view-logs').setAttribute('aria-selected', String(logs));
  };
  $('view-tools').addEventListener('click', () => selectView('tools')); $('view-logs').addEventListener('click', () => { selectView('logs'); if (!$('pause-logs').checked) loadLogs(); });
  $('copy-endpoint').addEventListener('click', () => copyText(state.status?.core?.endpoint)); $('copy-workspace').addEventListener('click', () => copyText(state.status?.core?.workspace));
  $('reveal-token').addEventListener('click', revealToken); $('copy-token').addEventListener('click', () => copyText(state.secret)); $('bridge-form').addEventListener('submit', saveBridge);
  $('bridge-settings').addEventListener('toggle', () => { if (!$('bridge-settings').open) { clearSecret(); $('new-token').value = ''; } });
  $('refresh-logs').addEventListener('click', loadLogs); $('pause-logs').addEventListener('change', restartPolling);
  $('log-filters').addEventListener('submit', (event) => { event.preventDefault(); loadLogs(); });
  ['log-source', 'log-level', 'log-since', 'log-lines'].forEach((id) => $(id).addEventListener('change', loadLogs));
  let queryTimer = 0; $('log-query').addEventListener('input', () => { clearTimeout(queryTimer); queryTimer = setTimeout(loadLogs, 300); });
  document.addEventListener('visibilitychange', () => { if (document.hidden) { state.statusController?.abort(); state.logsController?.abort(); clearSecret(); $('new-token').value = ''; } else { loadStatus(); if (!$('pause-logs').checked && !$('logs-view').hidden) loadLogs(); } });
  const clearPageSecrets = () => { clearSecret(); $('new-token').value = ''; };
  window.addEventListener('pagehide', clearPageSecrets); window.addEventListener('beforeunload', clearPageSecrets);
  applyTheme(); loadStatus(); restartPolling();
})();
