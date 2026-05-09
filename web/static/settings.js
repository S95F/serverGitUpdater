import { api, renderTopbar } from "/static/nav.js";

await renderTopbar("settings");

const sections = document.querySelectorAll(".section");
const titles = {
  server: "Server configuration",
  password: "Reset password",
};
const titleEl = document.getElementById("settings-title");
const hintEl = document.getElementById("settings-hint");

function showSection(name) {
  const valid = sections && [...sections].some((s) => s.dataset.section === name) ? name : "server";
  for (const s of sections) {
    s.hidden = s.dataset.section !== valid;
  }
  titleEl.textContent = `Settings — ${titles[valid] || valid}`;
  hintEl.hidden = true;
}

function currentSectionFromHash() {
  return (window.location.hash || "#server").slice(1);
}
showSection(currentSectionFromHash());
window.addEventListener("hashchange", () => showSection(currentSectionFromHash()));

// ---------- password ----------

const pwForm = document.getElementById("password-form");
const pwStatus = document.getElementById("password-status");
pwForm.addEventListener("submit", async (ev) => {
  ev.preventDefault();
  pwStatus.textContent = "Updating…";
  try {
    await api("/api/password", {
      method: "POST",
      body: JSON.stringify({ current: pwForm.current.value, new: pwForm.new.value }),
    });
    pwForm.reset();
    pwStatus.textContent = "Password updated.";
  } catch (e) {
    pwStatus.textContent = "Error: " + e.message;
  }
});

// ---------- server config ----------

const srvForm = document.getElementById("server-form");
const srvStatus = document.getElementById("server-status");

async function loadServer() {
  const c = await api("/api/server");
  srvForm.listen_addr.value = c.listen_addr || "";
  srvForm.session_ttl_hours.value = c.session_ttl_hours || 12;
  srvForm.repos_dir.value = c.repos_dir || "";
  srvForm.public_base_url.value = c.public_base_url || "";
  srvForm.log_path.value = c.log_path || "";
  srvForm.max_log_rows.value = c.max_log_rows ?? 500;
  srvForm.cookie_secure.checked = !!c.cookie_secure;
  srvForm.tls_cert_file.value = c.tls_cert_file || "";
  srvForm.tls_key_file.value = c.tls_key_file || "";
  srvForm.github_token.value = "";
  document.getElementById("github-token-state").textContent =
    c.github_token_is_set ? "A token is on file (leave blank to keep, type a new one to replace)."
                          : "No token set. Required for auto-registering webhooks.";
}

srvForm.addEventListener("submit", async (ev) => {
  ev.preventDefault();
  srvStatus.textContent = "Saving…";
  try {
    const r = await api("/api/server", {
      method: "POST",
      body: JSON.stringify({
        listen_addr: srvForm.listen_addr.value.trim(),
        session_ttl_hours: parseInt(srvForm.session_ttl_hours.value, 10),
        repos_dir: srvForm.repos_dir.value.trim(),
        public_base_url: srvForm.public_base_url.value.trim(),
        log_path: srvForm.log_path.value.trim(),
        max_log_rows: parseInt(srvForm.max_log_rows.value, 10),
        cookie_secure: srvForm.cookie_secure.checked,
        tls_cert_file: srvForm.tls_cert_file.value.trim(),
        tls_key_file: srvForm.tls_key_file.value.trim(),
        github_token: srvForm.github_token.value, // empty = leave existing
      }),
    });
    const restart = (r.requires_restart || []).join(", ");
    srvStatus.textContent = `Saved. Requires restart for: ${restart}.`;
  } catch (e) {
    srvStatus.textContent = "Error: " + e.message;
  }
});

const restartBtn = document.getElementById("server-restart-btn");
const restartStatus = document.getElementById("server-restart-status");

async function pingUntilAlive(deadlineMs) {
  const start = Date.now();
  while (Date.now() - start < deadlineMs) {
    try {
      const res = await fetch("/api/me", { credentials: "same-origin", cache: "no-store" });
      if (res.ok || res.status === 401) return true; // server is answering again
    } catch { /* still down */ }
    await new Promise((r) => setTimeout(r, 500));
  }
  return false;
}

restartBtn.addEventListener("click", async () => {
  if (!window.confirm("Restart the daemon now? The web UI will be unavailable for a couple of seconds.")) return;
  restartBtn.disabled = true;
  restartStatus.textContent = "Asking the daemon to shut itself down…";
  try {
    const r = await api("/api/server/restart", { method: "POST" });
    if (r.warning) {
      restartStatus.textContent = `Sent. Warning: ${r.warning}`;
      restartBtn.disabled = false;
      return;
    }
    restartStatus.textContent = "SIGTERM sent. Waiting for systemd to bring it back…";
    const ok = await pingUntilAlive(20000);
    if (ok) {
      restartStatus.textContent = "Back up.";
    } else {
      restartStatus.textContent = "Timed out waiting for the server to return. Check `journalctl -u servergitupdater -f`.";
    }
  } catch (e) {
    // Connection drops mid-request are expected when the server exits before we hear back.
    restartStatus.textContent = "Server closed the connection (likely already shutting down). Waiting for it to come back…";
    const ok = await pingUntilAlive(20000);
    restartStatus.textContent = ok ? "Back up." : "Timed out waiting for the server to return.";
  } finally {
    restartBtn.disabled = false;
  }
});

await loadServer();
