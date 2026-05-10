import { api, fmtTime, fmtRelative, renderTopbar, systemPathWarning, resolveUnderReposDir } from "/static/nav.js";

await renderTopbar("apps");

const id = window.location.pathname.split("/").pop();
const f = document.getElementById("config-form");

function setText(elId, v) {
  document.getElementById(elId).textContent = v == null || v === "" ? "—" : v;
}
function lines(value) {
  return value.split("\n").map((s) => s.trim()).filter(Boolean);
}

// ---------- tabs ----------
const tabs = document.querySelectorAll(".tab");
const panels = document.querySelectorAll(".tab-panel");
function showTab(name) {
  for (const t of tabs) t.classList.toggle("active", t.dataset.tab === name);
  for (const p of panels) p.hidden = p.dataset.panel !== name;
  history.replaceState(null, "", `#${name}`);
}
for (const t of tabs) {
  t.addEventListener("click", () => showTab(t.dataset.tab));
}
const initialTab = (window.location.hash || "#build").slice(1);
showTab([...tabs].some((t) => t.dataset.tab === initialTab) ? initialTab : "build");

// ---------- caddy mode toggle ----------
const modeSelect = document.getElementById("caddy-mode");
function applyCaddyMode() {
  const mode = modeSelect.value;
  for (const s of document.querySelectorAll(".mode-section")) {
    s.hidden = s.dataset.mode !== mode;
  }
}
modeSelect.addEventListener("change", applyCaddyMode);

// ---------- live "Resolves to" hint for the repo-path input ----------
let reposDir = "";
async function loadServerHint() {
  try {
    const s = await api("/api/server");
    reposDir = s.repos_dir || "";
  } catch { /* ignore */ }
  updateRepoResolution();
}
function updateRepoResolution() {
  const el = document.getElementById("repo-resolution");
  if (!el) return;
  const p = (f.repo_path.value || "").trim();
  if (!p) {
    el.innerHTML = `When <strong>repos_dir</strong> is set, this path is always joined under it (leading slashes ignored). When repos_dir is empty, an absolute path is used as-is and a relative one is resolved against the binary's working directory.`;
    return;
  }
  if (reposDir) {
    const full = resolveUnderReposDir(reposDir, p);
    el.innerHTML = `Resolves to: <code>${full}</code>`;
    return;
  }
  // No repos_dir: keep the old per-mode hints + system-path warning.
  if (p.startsWith("/")) {
    const warn = systemPathWarning(p);
    el.innerHTML = `Absolute path: <code>${p}</code>` + (warn ? `<br>${warn}` : "");
    return;
  }
  el.innerHTML = `Relative path with no <strong>repos_dir</strong> set — will resolve against the binary's working directory. Set one in <a href="/settings#server">Settings → Server configuration</a>.`;
}

// ---------- load / save ----------
async function loadApp() {
  const r = await api(`/api/apps/${id}`);
  const a = r.app || {};
  document.getElementById("app-title").textContent = r.name || "(unnamed)";

  f.name.value = r.name || "";
  f.repo_path.value = a.repo_path || "";
  f.clone_url.value = a.clone_url || "";
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
  f.caddy_mode.value = a.caddy_mode || "proxy";
  f.caddy_domain.value = a.caddy_domain || "";
  f.caddy_upstream.value = a.caddy_upstream || "127.0.0.1:{port}";
  f.caddy_root.value = a.caddy_root || "";
  f.caddy_browse.checked = !!a.caddy_browse;
  f.caddy_try_files.value = a.caddy_try_files || "";
  f.caddy_extra.value = a.caddy_extra || "";
  f.caddy_snippet_dir.value = a.caddy_snippet_dir || "/etc/caddy/sites.d";
  f.caddy_snippet_name.value = a.caddy_snippet_name || "";
  f.caddy_reload_command.value = (a.caddy_reload_command || ["caddy", "reload", "--config", "/etc/caddy/Caddyfile"]).join("\n");
  f.caddy_files_user.value = a.caddy_files_user || "";
  f.caddy_files_group.value = a.caddy_files_group || "";
  f.caddy_files_dir_mode.value = a.caddy_files_dir_mode || "0755";
  f.caddy_files_file_mode.value = a.caddy_files_file_mode || "0644";

  f.import_from_repo_file.checked = !!a.import_from_repo_file;
  f.repo_file_path.value = a.repo_file_path || ".servergitupdater.json";

  f.webhook_enabled.checked = !!a.webhook_enabled;
  f.webhook_branch_only.checked = !!a.webhook_branch_only;
  f.webhook_auto_register.checked = !!a.webhook_auto_register;
  f.webhook_secret.value = ""; // never echo persisted secret in the input
  document.getElementById("webhook-secret").placeholder = a.webhook_secret
    ? "(secret on file — leave blank to keep, type or generate to replace)"
    : "(no secret yet — type one or click Generate new secret)";
  document.getElementById("webhook-url").value = `${window.location.origin}/api/apps/${id}/webhook`;
  const remoteEl = document.getElementById("webhook-remote");
  remoteEl.textContent = a.webhook_remote_id
    ? `Registered on GitHub as hook ID ${a.webhook_remote_id}.`
    : "Not registered on GitHub yet (toggle Auto-register and save, or click Sync now).";

  applyCaddyMode();
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

  // Toggle clone affordances based on whether the dir exists / is a repo.
  const missing = !data.repo_path_exists || !data.is_git_repo;
  document.getElementById("missing-dir-notice").hidden = !missing;
  const cloneBtn = document.getElementById("clone-btn");
  cloneBtn.hidden = !(missing && (data.clone_url || ""));
}

async function runClone() {
  const btn = document.getElementById("clone-btn");
  const statusEl = document.getElementById("update-status");
  btn.disabled = true;
  statusEl.textContent = "Cloning…";
  try {
    const r = await api(`/api/apps/${id}/clone`, { method: "POST" });
    document.getElementById("last-output").textContent = r.output || "(no output)";
    statusEl.textContent = r.ok ? "Clone OK." : `Clone failed: ${r.error || "see output"}`;
    await refreshStatus();
  } catch (e) {
    statusEl.textContent = "Error: " + e.message;
  } finally {
    btn.disabled = false;
  }
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
        clone_url: f.clone_url.value.trim(),
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
        caddy_mode: f.caddy_mode.value,
        caddy_domain: f.caddy_domain.value.trim(),
        caddy_upstream: f.caddy_upstream.value.trim(),
        caddy_root: f.caddy_root.value.trim(),
        caddy_browse: f.caddy_browse.checked,
        caddy_try_files: f.caddy_try_files.value.trim(),
        caddy_extra: f.caddy_extra.value,
        caddy_snippet_dir: f.caddy_snippet_dir.value.trim(),
        caddy_snippet_name: f.caddy_snippet_name.value.trim(),
        caddy_reload_command: lines(f.caddy_reload_command.value),
        caddy_files_user: f.caddy_files_user.value.trim(),
        caddy_files_group: f.caddy_files_group.value.trim(),
        caddy_files_dir_mode: f.caddy_files_dir_mode.value.trim(),
        caddy_files_file_mode: f.caddy_files_file_mode.value.trim(),

        import_from_repo_file: f.import_from_repo_file.checked,
        repo_file_path: f.repo_file_path.value.trim(),

        webhook_enabled: f.webhook_enabled.checked,
        webhook_secret: f.webhook_secret.value.trim(),
        webhook_branch_only: f.webhook_branch_only.checked,
        webhook_auto_register: f.webhook_auto_register.checked,
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

document.getElementById("suggest-port-btn").addEventListener("click", async () => {
  const status = document.getElementById("suggest-port-status");
  const btn = document.getElementById("suggest-port-btn");
  btn.disabled = true;
  status.textContent = "Looking for a free port…";
  try {
    const r = await api(`/api/suggest-port?exclude_app=${encodeURIComponent(id)}`);
    if (!r.ok) {
      status.textContent = "Couldn't find a free port: " + (r.error || "no candidates");
      return;
    }
    f.app_port.value = r.port;
    status.textContent = `Picked ${r.port}. Click Save settings to apply.`;
  } catch (e) {
    status.textContent = "Error: " + e.message;
  } finally {
    btn.disabled = false;
  }
});

document.getElementById("update-btn").addEventListener("click", runUpdate);
document.getElementById("clone-btn").addEventListener("click", runClone);
document.getElementById("refresh-btn").addEventListener("click", refreshStatus);
f.repo_path.addEventListener("input", updateRepoResolution);
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
document.getElementById("caddy-fix-perms").addEventListener("click", async () => {
  const dir = (f.caddy_root.value.trim() || "the resolved repo path");
  if (!window.confirm(`Walk ${dir} and apply chmod (and chown if a user/group is set)?`)) return;
  await caddyAction(`/api/apps/${id}/caddy/fix-permissions`, "Fix permissions");
});

// ---------- repo-file import ----------
// Pass the field's current value as ?file= so the user doesn't have to
// click Save settings before previewing/importing a renamed config file.
function repoFileQuery() {
  const v = (f.repo_file_path.value || "").trim();
  return v ? `?file=${encodeURIComponent(v)}` : "";
}
document.getElementById("repofile-preview").addEventListener("click", async () => {
  const status = document.getElementById("repofile-status");
  const out = document.getElementById("repofile-output");
  status.textContent = "Reading…";
  try {
    const r = await api(`/api/apps/${id}/repo-file${repoFileQuery()}`);
    if (!r.exists) {
      status.textContent = `Not found: ${r.path} (${r.error || "no file"})`;
      out.textContent = "—";
      return;
    }
    status.textContent = `Found ${r.path}.`;
    out.textContent = JSON.stringify(r.imported, null, 2);
  } catch (e) {
    status.textContent = "Error: " + e.message;
  }
});
document.getElementById("repofile-import").addEventListener("click", async () => {
  const status = document.getElementById("repofile-status");
  const out = document.getElementById("repofile-output");
  status.textContent = "Importing…";
  try {
    const r = await api(`/api/apps/${id}/repo-file/import${repoFileQuery()}`, { method: "POST" });
    if (!r.ok) {
      status.textContent = `Import failed: ${r.error || "see output"}`;
      out.textContent = r.error || "";
      return;
    }
    status.textContent = `Imported from ${r.path}.`;
    await loadApp();
  } catch (e) {
    status.textContent = "Error: " + e.message;
  }
});
document.getElementById("show-repo-file-schema").addEventListener("click", (ev) => {
  ev.preventDefault();
  const out = document.getElementById("repofile-output");
  out.textContent = SAMPLE_REPO_FILE;
});

const SAMPLE_REPO_FILE = `// Drop this at the repo root as .servergitupdater.json. Every key is
// optional; missing keys leave the app's existing value alone. Strings
// support {port}, {repo_path} and {name} placeholders where the
// matching server-side field does.
{
  "auto_build_enabled": true,
  "build_command": "go",
  "build_args": ["build", "-o", "{repo_path}/{name}", "./..."],

  "app_port": 8081,
  "port_flag": "-port",

  "service_manage_enabled": true,
  "service_name": "myapp",
  "service_scope": "user",
  "service_exec_args": ["--config", "{repo_path}/config.toml"],
  "service_restart": "on-failure",

  "caddy_enabled": true,
  "caddy_auto_apply": true,
  "caddy_mode": "proxy",
  "caddy_domain": "myapp.example.com",
  "caddy_upstream": "127.0.0.1:{port}"
}`;

// ---------- webhook ----------
document.getElementById("webhook-regen").addEventListener("click", async () => {
  const status = document.getElementById("webhook-status");
  if (!window.confirm("Generate a new secret? Any existing GitHub webhook configured with the old secret will stop working until you update it.")) return;
  status.textContent = "Generating…";
  try {
    const r = await api(`/api/apps/${id}/webhook/regenerate`, { method: "POST" });
    document.getElementById("webhook-secret").value = r.webhook_secret;
    status.textContent = "New secret generated and saved. Update your GitHub webhook to match.";
  } catch (e) {
    status.textContent = "Error: " + e.message;
  }
});
document.getElementById("webhook-copy").addEventListener("click", async () => {
  const status = document.getElementById("webhook-status");
  const url = document.getElementById("webhook-url").value;
  const secret = document.getElementById("webhook-secret").value || "(secret already saved on the server; click Generate new secret if you've lost it)";
  const text = `URL:    ${url}\nSecret: ${secret}\nEvents: push\nFormat: application/json`;
  try {
    await navigator.clipboard.writeText(text);
    status.textContent = "Copied to clipboard.";
  } catch (e) {
    status.textContent = "Couldn't access clipboard: " + e.message;
  }
});
document.getElementById("webhook-sync").addEventListener("click", async () => {
  const status = document.getElementById("webhook-status");
  status.textContent = "Syncing with GitHub…";
  try {
    const r = await api(`/api/apps/${id}/webhook/sync`, { method: "POST" });
    status.textContent = r.ok ? `OK — ${r.message || "synced"}` : `Failed: ${r.error || "see server logs"}`;
    await loadApp(); // pick up the new webhook_remote_id
  } catch (e) {
    status.textContent = "Error: " + e.message;
  }
});

await Promise.all([loadApp(), loadServerHint()]);
updateRepoResolution();
await Promise.all([refreshStatus(), refreshDetect(), refreshServiceStatus(), refreshCaddyStatus()]);
