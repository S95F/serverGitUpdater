import { api, renderTopbar } from "/static/nav.js";

await renderTopbar("settings");

const form = document.getElementById("password-form");
const status = document.getElementById("password-status");

form.addEventListener("submit", async (ev) => {
  ev.preventDefault();
  status.textContent = "Updating…";
  try {
    await api("/api/password", {
      method: "POST",
      body: JSON.stringify({
        current: form.current.value,
        new: form.new.value,
      }),
    });
    form.reset();
    status.textContent = "Password updated.";
  } catch (e) {
    status.textContent = "Error: " + e.message;
  }
});
