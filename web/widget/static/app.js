/* miodesk standalone page — a compact diagnostics view, not a dashboard.
   It answers one question: is miodesk working, and where is it? The same
   renderer set is used; results render through /widget and inside hosts. */

"use strict";

function refreshStatus() {
  fetch("/api/status", { headers: { Accept: "application/json" } })
    .then((res) => {
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      return res.json();
    })
    .then(renderResult)
    .catch((err) => {
      const host = document.getElementById("result");
      host.replaceChildren();
      host.append(stateLine("fail", "alert", `miodesk unreachable: ${err.message}`));
    });
}

applySystemTheme();
refreshStatus();
setInterval(refreshStatus, 10000);

function applySystemTheme() {
  // Standalone page follows the system; hosted pages get the host's theme.
  delete document.documentElement.dataset.theme;
}
