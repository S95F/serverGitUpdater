const form = document.getElementById("login-form");
const errEl = document.getElementById("login-error");

form.addEventListener("submit", async (ev) => {
  ev.preventDefault();
  errEl.hidden = true;
  const fd = new FormData(form);
  const body = JSON.stringify({
    username: fd.get("username"),
    password: fd.get("password"),
  });
  try {
    const res = await fetch("/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body,
    });
    if (res.ok) {
      window.location.href = "/";
      return;
    }
    const data = await res.json().catch(() => ({}));
    errEl.textContent = data.error || `Login failed (${res.status})`;
    errEl.hidden = false;
  } catch (e) {
    errEl.textContent = "Network error: " + e.message;
    errEl.hidden = false;
  }
});
