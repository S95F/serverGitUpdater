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

  f.auto_build_enabled.checked = !!cfg.auto_build_enabled;
  f.build_command.value = cfg.build_command || "";
  f.build_args.value = (cfg.build_args || []).join("\n");

  f.service_manage_enabled.checked = !!cfg.service_manage_enabled;
  f.service_name.value = cfg.service_name || "";
  f.service_scope.value = cfg.service_scope || "system";
  f.service_description.value = cfg.service_description || "";
  f.service_exec_start.value = cfg.service_exec_start || "";
  f.service_exec_args.value = (cfg.service_exec_args || []).join("\n");
  f.service_working_dir.value = cfg.service_working_dir || "";
  f.service_user.value = cfg.service_user || "";
  f.service_env.value = (cfg.service_env || []).join("\n");
  f.service_restart.value = cfg.service_restart || "on-failure";
}

function lines(value) {
  return value
    .split("\n")
    .map((s) => s.trim())
    .filter(Boolean);
}

async function saveConfig(ev) {
  ev.preventDefault();
  const f = ev.currentTarget;
  const status = document.getElementById("config-status");
  status.textContent = "Saving…";
  try {
    await api("/api/config", {
      method: "POST",
      body: JSON.stringify({
        repo_path: f.repo_path.value.trim(),
        branch: f.branch.value.trim(),
        remote: f.remote.value.trim(),
        post_update_command: f.post_update_command.value.trim(),
        post_update_args: lines(f.post_update_args.value),
        auto_update_enabled: f.auto_update_enabled.checked,
        auto_update_minutes: parseInt(f.auto_update_minutes.value, 10),

        auto_build_enabled: f.auto_build_enabled.checked,
        build_command: f.build_command.value.trim(),
        build_args: lines(f.build_args.value),

        service_manage_enabled: f.service_manage_enabled.checked,
        service_name: f.service_name.value.trim(),
        service_scope: f.service_scope.value,
        service_description: f.service_description.value.trim(),
        service_exec_start: f.service_exec_start.value.trim(),
        service_exec_args: lines(f.service_exec_args.value),
        service_working_dir: f.service_working_dir.value.trim(),
        service_user: f.service_user.value.trim(),
        service_env: lines(f.service_env.value),
        service_restart: f.service_restart.value,
      }),
    });
    status.textContent = "Saved.";
    await Promise.all([refreshStatus(), refreshDetect(), refreshServiceStatus()]);
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

async function refreshDetect() {
  const el = document.getElementById("detect-summary");
  try {
    const d = await api("/api/build/detect");
    if (!d.kind) {
      el.textContent = "Detection: no buildable project found in repo_path";
      return;
    }
    const tool = d.tool_available
      ? `${d.tool_path || d.kind} (${d.tool_version || "ok"})`
      : `tool missing — ${d.notes || ""}`;
    const cmd = `${d.suggested_command} ${(d.suggested_args || []).join(" ")}`.trim();
    el.textContent = `Detection: ${d.kind} → ${tool}; default: ${cmd}`;
  } catch (e) {
    el.textContent = "Detection error: " + e.message;
  }
}

async function refreshServiceStatus() {
  const el = document.getElementById("svc-summary");
  try {
    const r = await api("/api/service/status");
    if (!r.configured) {
      el.textContent = "Status: no service_name configured";
      return;
    }
    const st = r.status || {};
    if (!st.available) {
      el.textContent = `Status: ${st.notes || "systemctl unavailable"}`;
      return;
    }
    const installed = st.installed ? "installed" : "not installed";
    el.textContent = `Status: ${installed} | active=${st.active_state || "?"} | enable=${st.enable_state || "?"}${st.unit_path ? ` | ${st.unit_path}` : ""}`;
  } catch (e) {
    el.textContent = "Status error: " + e.message;
  }
}

async function buildNow() {
  const el = document.getElementById("svc-output");
  const status = document.getElementById("config-status");
  status.textContent = "Building…";
  try {
    const r = await api("/api/build/run", { method: "POST" });
    el.textContent = r.output || "(no output)";
    status.textContent = r.ok ? "Build OK." : `Build failed: ${r.error || "see output"}`;
  } catch (e) {
    status.textContent = "Build error: " + e.message;
  }
}

async function svcAction(path, label) {
  const el = document.getElementById("svc-output");
  const status = document.getElementById("config-status");
  status.textContent = label + "…";
  try {
    const r = await api(path, { method: "POST" });
    el.textContent = r.output || "(no output)";
    status.textContent = r.ok ? `${label} OK.` : `${label} failed: ${r.error || "see output"}`;
    await refreshServiceStatus();
  } catch (e) {
    status.textContent = `${label} error: ` + e.message;
  }
}

async function svcPreview() {
  const el = document.getElementById("svc-output");
  try {
    const r = await api("/api/service/preview");
    el.textContent = `# would be written to ${r.unit_path}\n\n${r.unit}`;
  } catch (e) {
    el.textContent = "Preview error: " + e.message;
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

document.getElementById("detect-btn").addEventListener("click", refreshDetect);
document.getElementById("build-now").addEventListener("click", buildNow);
document.getElementById("svc-install").addEventListener("click", () =>
  svcAction("/api/service/install", "Install"));
document.getElementById("svc-restart").addEventListener("click", () =>
  svcAction("/api/service/restart", "Restart"));
document.getElementById("svc-uninstall").addEventListener("click", async () => {
  if (!window.confirm("Stop, disable and remove the unit file?")) return;
  await svcAction("/api/service/uninstall", "Uninstall");
});
document.getElementById("svc-preview").addEventListener("click", svcPreview);

(async () => {
  try {
    await Promise.all([
      refreshStatus(),
      loadConfig(),
      loadLogs(),
      refreshDetect(),
      refreshServiceStatus(),
    ]);
  } catch (e) {
    console.error(e);
  }
})();
