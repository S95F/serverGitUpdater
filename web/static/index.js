import { api, fmtRelative, el, renderTopbar, systemPathWarning, resolveUnderReposDir } from "/static/nav.js";

await renderTopbar("apps");

const tbody = document.querySelector("#apps-table tbody");
const empty = document.getElementById("empty");
const dlg = document.getElementById("new-app-dialog");
const newForm = document.getElementById("new-app-form");
const newError = document.getElementById("new-error");

let reposDir = "";
try {
  const s = await api("/api/server");
  reposDir = s.repos_dir || "";
} catch { /* ignore */ }

const newRepoInput = newForm.querySelector('input[name="repo_path"]');
const newRepoHint = document.getElementById("new-repo-hint");
function updateNewRepoHint() {
  const p = (newRepoInput.value || "").trim();
  if (!p) {
    newRepoHint.innerHTML = "You can fill in the rest after the app is created.";
    return;
  }
  if (reposDir) {
    newRepoHint.innerHTML = `Resolves to: <code>${resolveUnderReposDir(reposDir, p)}</code>`;
    return;
  }
  // No repos_dir set: fall back to old behavior + system-path warning.
  if (p.startsWith("/")) {
    const warn = systemPathWarning(p);
    newRepoHint.innerHTML = `Absolute path: <code>${p}</code>` + (warn ? `<br>${warn}` : "");
    return;
  }
  newRepoHint.innerHTML = `Relative path with no <strong>repos_dir</strong> set — will resolve against the binary's working directory. <a href="/settings#server">Set one in Settings</a>.`;
}
newRepoInput.addEventListener("input", updateNewRepoHint);

document.getElementById("new-app-btn").addEventListener("click", () => {
  newForm.reset();
  newError.hidden = true;
  updateNewRepoHint();
  dlg.showModal();
});
document.getElementById("new-cancel").addEventListener("click", () => dlg.close());

const cloneOutWrap = document.getElementById("new-clone-output-wrap");
const cloneOut = document.getElementById("new-clone-output");

newForm.addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const fd = new FormData(newForm);
  newError.hidden = true;
  cloneOutWrap.hidden = true;
  try {
    const r = await api("/api/apps", {
      method: "POST",
      body: JSON.stringify({
        name: fd.get("name"),
        repo_path: fd.get("repo_path"),
        clone_url: fd.get("clone_url"),
      }),
    });
    if (r.clone) {
      cloneOut.textContent = r.clone.output || "(no output)";
      cloneOutWrap.open = true;
      cloneOutWrap.hidden = false;
      if (!r.clone.ok) {
        newError.textContent = "App created but clone failed: " + (r.clone.error || "see output");
        newError.hidden = false;
        return; // stay on dialog so the user can read the output
      }
    }
    dlg.close();
    if (r.app && r.app.id) {
      window.location.href = `/apps/${r.app.id}`;
    } else {
      await load();
    }
  } catch (e) {
    newError.textContent = e.message;
    newError.hidden = false;
  }
});

function badge(on, label) {
  return el("span", { class: "badge " + (on ? "on" : "off") }, label);
}

async function runUpdate(id, btn) {
  const original = btn.textContent;
  btn.disabled = true;
  btn.textContent = "Running…";
  try {
    const r = await api(`/api/apps/${id}/update`, { method: "POST" });
    btn.textContent = r.ok ? "OK" : "Failed";
    setTimeout(() => { btn.textContent = original; btn.disabled = false; }, 1500);
    await load();
  } catch (e) {
    btn.textContent = "Error";
    setTimeout(() => { btn.textContent = original; btn.disabled = false; }, 1500);
    alert("Update error: " + e.message);
  }
}

async function deleteApp(id, name) {
  if (!window.confirm(`Delete "${name}"? This only removes it from the updater; the repo, service unit and Caddy snippet remain on disk unless you uninstall them first.`)) return;
  await api(`/api/apps/${id}`, { method: "DELETE" });
  await load();
}

async function load() {
  const r = await api("/api/apps");
  const apps = r.apps || [];
  tbody.innerHTML = "";
  empty.hidden = apps.length > 0;

  for (const a of apps) {
    const updateBtn = el("button", { class: "ghost small", onclick: () => runUpdate(a.id, updateBtn) }, "Update");
    const editBtn = el("a", { class: "ghost small", href: `/apps/${a.id}` }, "Edit");
    const deleteBtn = el("button", { class: "ghost small danger", onclick: () => deleteApp(a.id, a.name) }, "Delete");

    const tr = el("tr", {}, [
      el("td", { class: "name-cell" },
        el("a", { href: `/apps/${a.id}`, class: "name-link" }, a.name)),
      el("td", {
        class: "mono",
        "data-label": "Repo",
        title: a.resolved_repo_path ? `repo_path: ${a.repo_path}` : "",
      }, a.resolved_repo_path || a.repo_path || "—"),
      el("td", { class: "mono", "data-label": "Branch" }, a.branch || "—"),
      el("td", { class: "mono", "data-label": "Port" }, a.app_port ? String(a.app_port) : "—"),
      el("td", { "data-label": "Auto-update" },
        badge(a.auto_update_enabled, a.auto_update_enabled ? `every ${a.auto_update_minutes}m` : "off")),
      el("td", { "data-label": "Auto-build" },
        badge(a.auto_build_enabled, a.auto_build_enabled ? "on" : "off")),
      el("td", { "data-label": "Service" },
        a.service_enabled ? badge(true, a.service_name || "on") : badge(false, "off")),
      el("td", { "data-label": "Caddy" },
        a.caddy_enabled ? badge(true, a.caddy_domain || "on") : badge(false, "off")),
      el("td", { class: "muted", "data-label": "Last run" }, fmtRelative(a.last_run)),
      el("td", { class: "actions" }, [updateBtn, editBtn, deleteBtn]),
    ]);
    tbody.appendChild(tr);
  }
}

await load();
