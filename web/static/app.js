function csrfToken() {
  const m = document.cookie.match(/(?:^|;\s*)sgu_csrf=([^;]+)/);
  return m ? decodeURIComponent(m[1]) : "";
}

async function api(path, opts = {}) {
  const headers = { ...(opts.headers || {}) };
  if (opts.method && opts.method !== "GET") {
    headers["Content-Type"] = "application/json";
    headers["X-CSRF-Token"] = csrfToken();
  }
  const res = await fetch(path, { ...opts, headers });
  if (res.status === 401) {
    window.location.href = "/login";
    throw new Error("unauthorized");
  }
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error(data.error || `HTTP ${res.status}`);
  }
  return data;
}

function setText(id, v) {
  document.getElementById(id).textContent = v == null || v === "" ? "—" : v;
}

function fmtTime(s) {
  if (!s) return "—";
  try {
    const d = new Date(s);
    if (isNaN(d.getTime())) return s;
    return d.toLocaleString();
  } catch {
    return s;
  }
}

async function refreshStatus() {
  const data = await api("/api/status");
  document.getElementById("who").textContent = data.username ? `Signed in as ${data.username}` : "";
  setText("s-path", data.repo_path);
  setText("s-branch", data.branch);
  setText("s-remote", data.remote);
  if (data.git_error) {
    setText("s-head", `(error) ${data.git_error}`);
    setText("s-subject", "—");
    setText("s-time", "—");
    setText("s-dirty", "—");
    setText("s-ba", "—");
  } else {
    const g = data.git || {};
    setText("s-head", g.commit);
    setText("s-subject", g.commit_subject);
    setText("s-time", fmtTime(g.commit_time));
    setText("s-dirty", g.dirty ? "dirty (uncommitted changes)" : "clean");
    setText("s-ba", g.behind_ahead);
  }
  setText(
    "s-auto",
    data.auto_update_enabled
      ? `enabled — every ${data.auto_update_minutes} min`
      : "disabled"
  );
}

async function loadConfig() {
  const cfg = await api("/api/config");
  const f = document.getElementById("config-form");
  f.repo_path.value = cfg.repo_path || "";
  f.branch.value = cfg.branch || "main";
  f.remote.value = cfg.remote || "origin";
  f.post_update_command.value = cfg.post_update_command || "";
  f.post_update_args.value = (cfg.post_update_args || []).join("\n");
  f.auto_update_enabled.checked = !!cfg.auto_update_enabled;
  f.auto_update_minutes.value = cfg.auto_update_minutes || 15;
}

async function saveConfig(ev) {
  ev.preventDefault();
  const f = ev.currentTarget;
  const status = document.getElementById("config-status");
  status.textContent = "Saving…";
  const args = f.post_update_args.value
    .split("\n")
    .map((s) => s.trim())
    .filter(Boolean);
  try {
    await api("/api/config", {
      method: "POST",
      body: JSON.stringify({
        repo_path: f.repo_path.value.trim(),
        branch: f.branch.value.trim(),
        remote: f.remote.value.trim(),
        post_update_command: f.post_update_command.value.trim(),
        post_update_args: args,
        auto_update_enabled: f.auto_update_enabled.checked,
        auto_update_minutes: parseInt(f.auto_update_minutes.value, 10),
      }),
    });
    status.textContent = "Saved.";
    await refreshStatus();
  } catch (e) {
    status.textContent = "Error: " + e.message;
  }
}

async function changePassword(ev) {
  ev.preventDefault();
  const f = ev.currentTarget;
  const status = document.getElementById("password-status");
  status.textContent = "Updating…";
  try {
    await api("/api/password", {
      method: "POST",
      body: JSON.stringify({
        current: f.current.value,
        new: f.new.value,
      }),
    });
    f.reset();
    status.textContent = "Password updated.";
  } catch (e) {
    status.textContent = "Error: " + e.message;
  }
}

async function runUpdate() {
  const btn = document.getElementById("update-btn");
  const statusEl = document.getElementById("update-status");
  btn.disabled = true;
  statusEl.textContent = "Running…";
  try {
    const data = await api("/api/update", { method: "POST" });
    const e = data.entry || {};
    statusEl.textContent = data.ok
      ? `OK — ${e.duration || ""} ${e.old_head ? "(" + e.old_head.slice(0, 7) + " → " + (e.new_head || "").slice(0, 7) + ")" : ""}`
      : "Failed: see history below.";
    document.getElementById("last-output").textContent = e.output || "";
    await Promise.all([refreshStatus(), loadLogs()]);
  } catch (e) {
    statusEl.textContent = "Error: " + e.message;
  } finally {
    btn.disabled = false;
  }
}

async function loadLogs() {
  const data = await api("/api/logs");
  const tbody = document.querySelector("#logs tbody");
  tbody.innerHTML = "";
  const entries = data.entries || [];
  for (const e of entries) {
    const tr = document.createElement("tr");
    const cells = [
      fmtTime(e.time),
      e.source || "",
      e.success ? "ok" : "fail",
      e.old_head || e.new_head
        ? `${(e.old_head || "").slice(0, 7)} → ${(e.new_head || "").slice(0, 7)}`
        : "",
      e.duration || "",
    ];
    for (const c of cells) {
      const td = document.createElement("td");
      td.textContent = c;
      tr.appendChild(td);
    }
    tr.classList.add(e.success ? "row-ok" : "row-fail");
    tbody.appendChild(tr);
  }
  if (entries.length > 0) {
    document.getElementById("last-output").textContent = entries[0].output || "";
  }
}

document.getElementById("logout").addEventListener("click", async () => {
  await fetch("/logout", { method: "POST", headers: { "X-CSRF-Token": csrfToken() } });
  window.location.href = "/login";
});

document.getElementById("update-btn").addEventListener("click", runUpdate);
document.getElementById("refresh-btn").addEventListener("click", refreshStatus);
document.getElementById("config-form").addEventListener("submit", saveConfig);
document.getElementById("password-form").addEventListener("submit", changePassword);

(async () => {
  try {
    await Promise.all([refreshStatus(), loadConfig(), loadLogs()]);
  } catch (e) {
    console.error(e);
  }
})();
