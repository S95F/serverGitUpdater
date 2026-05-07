import { api, fmtTime, fmtRelative, el, renderTopbar } from "/static/nav.js";

await renderTopbar("administration");

function setText(id, v) {
  document.getElementById(id).textContent = v;
}

function fmtMs(ms) {
  if (!ms) return "—";
  if (ms < 1000) return `${ms} ms`;
  return `${(ms / 1000).toFixed(2)} s`;
}

async function loadSummary() {
  const r = await api("/api/admin/summary");
  setText("o-apps", r.app_count ?? 0);
  setText("o-total", r.total_runs ?? 0);
  setText("o-ok", r.total_success ?? 0);
  setText("o-fail", r.total_failure ?? 0);
  setText("o-24h", r.runs_24h ?? 0);
  const rate = r.runs_24h > 0 ? Math.round((r.success_24h / r.runs_24h) * 100) : null;
  setText("o-24h-rate", rate == null ? "—" : `${rate}%`);

  const tbody = document.querySelector("#per-app tbody");
  tbody.innerHTML = "";
  for (const a of r.per_app || []) {
    tbody.appendChild(el("tr", {}, [
      el("td", {}, el("a", { href: `/apps/${a.id}` }, a.name)),
      el("td", { class: "mono" }, String(a.runs)),
      el("td", { class: "mono good" }, String(a.successes)),
      el("td", { class: "mono " + (a.failures > 0 ? "bad" : "") }, String(a.failures)),
      el("td", { class: "mono" }, fmtMs(a.avg_duration_ms)),
      el("td", { class: "muted" }, fmtRelative(a.last_run)),
      el("td", { class: "muted" }, fmtRelative(a.last_success)),
      el("td", { class: "muted" }, fmtRelative(a.last_failure)),
    ]));
  }
}

async function loadFilterOptions() {
  const r = await api("/api/apps");
  const sel = document.getElementById("filter");
  for (const a of r.apps || []) {
    sel.appendChild(el("option", { value: a.id }, a.name));
  }
}

async function loadLogs() {
  const filter = document.getElementById("filter").value;
  const url = "/api/logs?limit=500" + (filter ? `&app_id=${encodeURIComponent(filter)}` : "");
  const r = await api(url);
  const tbody = document.querySelector("#logs tbody");
  tbody.innerHTML = "";
  for (const e of r.entries || []) {
    const old7 = (e.old_head || "").slice(0, 7);
    const new7 = (e.new_head || "").slice(0, 7);
    const heads = old7 || new7 ? `${old7} → ${new7}` : "";
    const tr = el("tr", {
      class: e.success ? "row-ok" : "row-fail",
      onclick: () => { document.getElementById("entry-output").textContent = e.output || "(no output)"; },
    }, [
      el("td", { class: "muted" }, fmtTime(e.time)),
      el("td", {}, e.app_name || e.app_id || ""),
      el("td", {}, e.source || ""),
      el("td", {}, e.success ? "ok" : "fail"),
      el("td", { class: "mono" }, heads),
      el("td", { class: "mono" }, e.duration || ""),
    ]);
    tbody.appendChild(tr);
  }
}

document.getElementById("filter").addEventListener("change", loadLogs);

await loadFilterOptions();
await Promise.all([loadSummary(), loadLogs()]);
