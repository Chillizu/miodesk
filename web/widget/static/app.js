/* miodesk standalone diagnostics page. */

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

delete document.documentElement.dataset.theme;
refreshStatus();
setInterval(refreshStatus, 10000);
