import { api, fmtTime, fmtRelative, renderTopbar } from "/static/nav.js";

await renderTopbar("apps");

const id = window.location.pathname.split("/").pop();
const f = document.getElementById("config-form");

function setText(elId, v) {
  document.getElementById(elId).textContent = v == null || v === "" ? "—" : v;
}
function lines(value) {
  return value.split("\n").map((s) => s.trim()).filter(Boolean);
}

async function loadApp() {
  const r = await api(`/api/apps/${id}`);
  const a = r.app || {};
  document.getElementById("app-title").textContent = r.name || "(unnamed)";

  f.name.value = r.name || "";
  f.repo_path.value = a.repo_path || "";
  f.branch.value = a.branch || "main";
  f.remote.value = a.remote || "origin";
  f.post_update_command.value = a.post_update_command || "";
  f.post_update_args.value = (a.post_update_args || []).join("\n");
  f.auto_update_enabled.checked = !!a.auto_update_enabled;
  f.auto_update_minutes.value = a.auto_update_minutes || 15;

  f.auto_build_enabled.checked = !!a.auto_build_enabled;
  f.build_command.value = a.build_command || "";
  f.build_args.value = (a.build_args || []).join("\n");
  f.app_port.value = a.app_port || "";
  f.port_flag.value = a.port_flag || "-port";

  f.service_manage_enabled.checked = !!a.service_manage_enabled;
  f.service_name.value = a.service_name || "";
  f.service_scope.value = a.service_scope || "system";
  f.service_description.value = a.service_description || "";
  f.service_exec_start.value = a.service_exec_start || "";
  f.service_exec_args.value = (a.service_exec_args || []).join("\n");
  f.service_working_dir.value = a.service_working_dir || "";
  f.service_user.value = a.service_user || "";
  f.service_env.value = (a.service_env || []).join("\n");
  f.service_restart.value = a.service_restart || "on-failure";

  f.caddy_enabled.checked = !!a.caddy_enabled;
  f.caddy_auto_apply.checked = !!a.caddy_auto_apply;
  f.caddy_domain.value = a.caddy_domain || "";
  f.caddy_upstream.value = a.caddy_upstream || "127.0.0.1:{port}";
  f.caddy_extra.value = a.caddy_extra || "";
  f.caddy_snippet_dir.value = a.caddy_snippet_dir || "/etc/caddy/sites.d";
  f.caddy_snippet_name.value = a.caddy_snippet_name || "";
  f.caddy_reload_command.value = (a.caddy_reload_command || ["systemctl", "reload", "caddy"]).join("\n");
}

async function refreshStatus() {
  const data = await api(`/api/apps/${id}/status`);
  setText("s-path", data.repo_path);
  setText("s-branch", data.branch);
  setText("s-remote", data.remote);
  if (data.git_error) {
    setText("s-head", `(error) ${data.git_error}`);
    setText("s-subject", "—"); setText("s-time", "—"); setText("s-dirty", "—"); setText("s-ba", "—");
  } else {
    const g = data.git || {};
    setText("s-head", g.commit);
    setText("s-subject", g.commit_subject);
    setText("s-time", fmtTime(g.commit_time));
    setText("s-dirty", g.dirty ? "dirty" : "clean");
    setText("s-ba", g.behind_ahead);
  }
  setText("s-auto", data.auto_update_enabled ? `enabled — every ${data.auto_update_minutes} min` : "disabled");
  setText("s-last", fmtRelative(data.last_run));
}

async function saveConfig(ev) {
  ev.preventDefault();
  const status = document.getElementById("config-status");
  status.textContent = "Saving…";
  try {
    await api(`/api/apps/${id}`, {
      method: "POST",
      body: JSON.stringify({
        name: f.name.value.trim(),
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
        app_port: parseInt(f.app_port.value, 10) || 0,
        port_flag: f.port_flag.value.trim() || "-port",

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

        caddy_enabled: f.caddy_enabled.checked,
        caddy_auto_apply: f.caddy_auto_apply.checked,
        caddy_domain: f.caddy_domain.value.trim(),
        caddy_upstream: f.caddy_upstream.value.trim(),
        caddy_extra: f.caddy_extra.value,
        caddy_snippet_dir: f.caddy_snippet_dir.value.trim(),
        caddy_snippet_name: f.caddy_snippet_name.value.trim(),
        caddy_reload_command: lines(f.caddy_reload_command.value),
      }),
    });
    status.textContent = "Saved.";
    await Promise.all([refreshStatus(), refreshDetect(), refreshServiceStatus(), refreshCaddyStatus()]);
    document.getElementById("app-title").textContent = f.name.value;
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
    const data = await api(`/api/apps/${id}/update`, { method: "POST" });
    const e = data.entry || {};
    statusEl.textContent = data.ok
      ? `OK — ${e.duration || ""}${e.old_head ? ` (${e.old_head.slice(0, 7)} → ${(e.new_head || "").slice(0, 7)})` : ""}`
      : "Failed: see Last run output below.";
    document.getElementById("last-output").textContent = e.output || "(no output)";
    await refreshStatus();
  } catch (e) {
    statusEl.textContent = "Error: " + e.message;
  } finally {
    btn.disabled = false;
  }
}

async function refreshDetect() {
  const el = document.getElementById("detect-summary");
  try {
    const d = await api(`/api/apps/${id}/build/detect`);
    if (!d.kind) { el.textContent = "Detection: no buildable project found"; return; }
    const tool = d.tool_available ? `${d.tool_path || d.kind} (${d.tool_version || "ok"})` : `tool missing — ${d.notes || ""}`;
    el.textContent = `Detection: ${d.kind} → ${tool}; default: ${d.suggested_command} ${(d.suggested_args || []).join(" ")}`.trim();
  } catch (e) {
    el.textContent = "Detection error: " + e.message;
  }
}

async function buildNow() {
  const el = document.getElementById("svc-output");
  const status = document.getElementById("config-status");
  status.textContent = "Building…";
  try {
    const r = await api(`/api/apps/${id}/build/run`, { method: "POST" });
    el.textContent = r.output || "(no output)";
    status.textContent = r.ok ? "Build OK." : `Build failed: ${r.error || "see output"}`;
  } catch (e) { status.textContent = "Build error: " + e.message; }
}

async function refreshServiceStatus() {
  const el = document.getElementById("svc-summary");
  try {
    const r = await api(`/api/apps/${id}/service/status`);
    if (!r.configured) { el.textContent = "Status: no service_name configured"; return; }
    const st = r.status || {};
    if (!st.available) { el.textContent = `Status: ${st.notes || "systemctl unavailable"}`; return; }
    el.textContent = `Status: ${st.installed ? "installed" : "not installed"} | active=${st.active_state || "?"} | enable=${st.enable_state || "?"}${st.unit_path ? ` | ${st.unit_path}` : ""}`;
  } catch (e) { el.textContent = "Status error: " + e.message; }
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
  } catch (e) { status.textContent = `${label} error: ` + e.message; }
}

async function svcPreview() {
  const el = document.getElementById("svc-output");
  try {
    const r = await api(`/api/apps/${id}/service/preview`);
    el.textContent = `# would be written to ${r.unit_path}\n\n${r.unit}`;
  } catch (e) { el.textContent = "Preview error: " + e.message; }
}

async function refreshCaddyStatus() {
  const el = document.getElementById("caddy-summary");
  try {
    const r = await api(`/api/apps/${id}/caddy/status`);
    if (!r.configured) { el.textContent = "Status: caddy not enabled or domain not set"; return; }
    const st = r.status || {};
    const reloader = st.available ? "reload command available" : st.notes || "reload command unavailable";
    const onDisk = st.snippet_exists
      ? (st.on_disk_matches ? "snippet on disk matches config" : "snippet on disk DIFFERS from config")
      : "no snippet on disk";
    el.textContent = `Status: ${onDisk} | ${reloader}${st.snippet_path ? ` | ${st.snippet_path}` : ""}`;
  } catch (e) { el.textContent = "Status error: " + e.message; }
}

async function caddyAction(path, label) {
  const el = document.getElementById("caddy-output");
  const status = document.getElementById("config-status");
  status.textContent = label + "…";
  try {
    const r = await api(path, { method: "POST" });
    el.textContent = r.output || "(no output)";
    status.textContent = r.ok ? `${label} OK.` : `${label} failed: ${r.error || "see output"}`;
    await refreshCaddyStatus();
  } catch (e) { status.textContent = `${label} error: ` + e.message; }
}

async function caddyPreview() {
  const el = document.getElementById("caddy-output");
  try {
    const r = await api(`/api/apps/${id}/caddy/preview`);
    el.textContent = `# would be written to ${r.snippet_path}\n\n${r.snippet}`;
  } catch (e) { el.textContent = "Preview error: " + e.message; }
}

document.getElementById("update-btn").addEventListener("click", runUpdate);
document.getElementById("refresh-btn").addEventListener("click", refreshStatus);
f.addEventListener("submit", saveConfig);
document.getElementById("detect-btn").addEventListener("click", refreshDetect);
document.getElementById("build-now").addEventListener("click", buildNow);
document.getElementById("svc-install").addEventListener("click", () => svcAction(`/api/apps/${id}/service/install`, "Install"));
document.getElementById("svc-restart").addEventListener("click", () => svcAction(`/api/apps/${id}/service/restart`, "Restart"));
document.getElementById("svc-uninstall").addEventListener("click", async () => {
  if (!window.confirm("Stop, disable and remove the unit file?")) return;
  await svcAction(`/api/apps/${id}/service/uninstall`, "Uninstall");
});
document.getElementById("svc-preview").addEventListener("click", svcPreview);
document.getElementById("caddy-apply").addEventListener("click", () => caddyAction(`/api/apps/${id}/caddy/apply`, "Apply"));
document.getElementById("caddy-reload").addEventListener("click", () => caddyAction(`/api/apps/${id}/caddy/reload`, "Reload"));
document.getElementById("caddy-remove").addEventListener("click", async () => {
  if (!window.confirm("Remove the Caddy snippet and reload?")) return;
  await caddyAction(`/api/apps/${id}/caddy/remove`, "Remove");
});
document.getElementById("caddy-preview").addEventListener("click", caddyPreview);

await loadApp();
await Promise.all([refreshStatus(), refreshDetect(), refreshServiceStatus(), refreshCaddyStatus()]);
