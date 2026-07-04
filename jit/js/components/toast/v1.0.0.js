/* toast component utility @v1.0.0 — flash transient messages.
 * Supports:
 *   - window.kitwork.toast("Message")
 */
window.kitwork.toast = function (text, ms) {
  if (!text) return;
  var host = document.getElementById("kitwork-toasts");
  if (!host) {
    host = document.createElement("div");
    host.id = "kitwork-toasts";
    host.setAttribute("role", "status");
    host.setAttribute("aria-live", "polite");
    host.style.cssText = "position:fixed;bottom:1rem;right:1rem;z-index:2147483647;" +
      "display:flex;flex-direction:column;gap:.5rem;pointer-events:none";
    document.body.appendChild(host);
  }

  var msg = document.createElement("div");
  msg.className = "kitwork-toast";
  msg.setAttribute("data-state", "enter");
  msg.textContent = text;
  msg.style.cssText = "pointer-events:auto;max-width:22rem;padding:.6rem .9rem;border-radius:.5rem;" +
    "background:#18181b;color:#fafafa;font:500 14px system-ui,sans-serif;box-shadow:0 4px 16px rgba(0,0,0,.25);" +
    "opacity:0;transform:translateY(.5rem);transition:opacity .18s ease,transform .18s ease";
  host.appendChild(msg);
  requestAnimationFrame(function () {
    msg.setAttribute("data-state", "shown");
    msg.style.opacity = "1";
    msg.style.transform = "translateY(0)";
  });

  var duration = ms || 3000;
  setTimeout(function () {
    msg.setAttribute("data-state", "leave");
    msg.style.opacity = "0";
    msg.style.transform = "translateY(.5rem)";
    setTimeout(function () { msg.remove(); if (!host.children.length) host.remove(); }, 200);
  }, duration);
};
