// Shared helpers for every authenticated page.
//
// The CSP only allows same-origin scripts, so each HTML page includes:
//   <script src="/static/nav.js"></script>
// before its page-specific script. nav.js renders a consistent topbar into
// the <header id="topbar"> element and exports api()/csrfToken()/fmtTime().

export function csrfToken() {
  const m = document.cookie.match(/(?:^|;\s*)sgu_csrf=([^;]+)/);
  return m ? decodeURIComponent(m[1]) : "";
}

export async function api(path, opts = {}) {
  const headers = { ...(opts.headers || {}) };
  if (opts.method && opts.method !== "GET") {
    if (!(opts.body instanceof FormData)) {
      headers["Content-Type"] = headers["Content-Type"] || "application/json";
    }
    headers["X-CSRF-Token"] = csrfToken();
  }
  const res = await fetch(path, { ...opts, headers });
  if (res.status === 401) {
    window.location.href = "/login";
    throw new Error("unauthorized");
  }
  const text = await res.text();
  let data = {};
  if (text) {
    try { data = JSON.parse(text); } catch { data = { error: text }; }
  }
  if (!res.ok) {
    // Surface useful pipeline output if the server returned it alongside the error.
    const detail = data.entry?.output || data.output || data.error || `HTTP ${res.status}`;
    const err = new Error(detail);
    err.status = res.status;
    err.data = data;
    throw err;
  }
  return data;
}

export function fmtTime(s) {
  if (!s) return "—";
  try {
    const d = new Date(s);
    if (isNaN(d.getTime()) || d.getTime() === 0) return "—";
    return d.toLocaleString();
  } catch {
    return s;
  }
}

// systemPathWarning returns a HTML warning string when a path looks like
// it would land somewhere the updater process is unlikely to own. It is
// only useful when repos_dir is empty: when repos_dir is set, the path is
// always reinterpreted as relative to it so escapes are impossible.
export function systemPathWarning(p) {
  if (!p || !p.startsWith("/")) return "";
  const parts = p.replace(/\/+/g, "/").split("/").filter(Boolean);
  if (parts.length === 0) return "";
  if (parts.length === 1) {
    return `<strong>Warning:</strong> <code>${p}</code> sits at the filesystem root. Most processes can't write here. Set <strong>repos_dir</strong> in Settings and use a relative path like <code>${parts[0]}</code>.`;
  }
  const top = "/" + parts[0];
  const systemRoots = ["/etc", "/var", "/usr", "/sys", "/proc", "/boot", "/root", "/sbin", "/bin", "/lib", "/lib64"];
  if (systemRoots.includes(top)) {
    return `<strong>Warning:</strong> <code>${p}</code> is inside <code>${top}</code>, which is usually owned by root. The clone or update will likely fail with "Permission denied".`;
  }
  return "";
}

// resolveUnderReposDir mirrors config.ResolveRepoPath in JS so the UI
// hint matches what the server will actually do.
export function resolveUnderReposDir(reposDir, repoPath) {
  if (!repoPath) return "";
  if (!reposDir) return repoPath;
  const parts = repoPath.replace(/\/+/g, "/").split("/").filter(p => p && p !== "." && p !== "..");
  if (parts.length === 0) return reposDir;
  const base = reposDir.endsWith("/") ? reposDir.slice(0, -1) : reposDir;
  return base + "/" + parts.join("/");
}

export function fmtRelative(s) {
  if (!s) return "never";
  const d = new Date(s);
  if (isNaN(d.getTime()) || d.getTime() === 0) return "never";
  const diff = Date.now() - d.getTime();
  if (diff < 0) return "in the future";
  const secs = Math.floor(diff / 1000);
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 48) return `${hrs}h ago`;
  return `${Math.floor(hrs / 24)}d ago`;
}

export function el(tag, attrs = {}, children = []) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === "class") e.className = v;
    else if (k === "text") e.textContent = v;
    else if (k.startsWith("on") && typeof v === "function") e.addEventListener(k.slice(2), v);
    else if (v !== undefined && v !== null && v !== false) e.setAttribute(k, v);
  }
  for (const c of [].concat(children)) {
    if (c == null) continue;
    e.appendChild(typeof c === "string" ? document.createTextNode(c) : c);
  }
  return e;
}

export async function renderTopbar(active) {
  const bar = document.getElementById("topbar");
  if (!bar) return;
  let username = "";
  try {
    const me = await api("/api/me");
    username = me.username || "";
  } catch { /* logged out; api() already redirected */ }

  const link = (href, label) => {
    const a = el("a", { href, class: "nav-link" + (active === label.toLowerCase() ? " active" : "") }, label);
    return a;
  };

  bar.innerHTML = "";
  bar.appendChild(el("div", { class: "topbar-left" }, [
    el("a", { href: "/", class: "brand" }, "serverGitUpdater"),
    el("nav", { class: "nav-links" }, [
      link("/", "Apps"),
      link("/admin", "Administration"),
      link("/settings", "Settings"),
    ]),
  ]));

  const signOut = el("button", {
    class: "ghost",
    onclick: async () => {
      try {
        await fetch("/logout", { method: "POST", headers: { "X-CSRF-Token": csrfToken() } });
      } finally {
        window.location.href = "/login";
      }
    },
  }, "Sign out");

  bar.appendChild(el("div", { class: "topbar-right" }, [
    el("span", { class: "muted small" }, username ? `${username}` : ""),
    signOut,
  ]));
}
