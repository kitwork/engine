/* toast verb — flash a transient message.
 * Supports: <button data-kitwork-action="toast" data-kitwork-toast="Success">
 */
window.kitwork.components.action("toast", function (el) {
  var text = el.getAttribute("data-kit-toast") || el.getAttribute("data-kitwork-toast");
  if (text == null) {
    var t = window.kitwork.components.target(el);
    text = t ? (t.innerText || t.textContent || "") : "";
  }
  var ms = parseInt(el.getAttribute("data-kit-toast-ms") || el.getAttribute("data-kitwork-toast-ms"), 10) || 3000;
  
  // Use the global kitwork.toast utility if available, else fall back to inline creation
  if (typeof window.kitwork.toast === "function") {
    window.kitwork.toast(text, ms);
    return;
  }

  var host = document.getElementById("kitwork-toasts");
  if (!host) {
    host = document.createElement("div");
    host.id = "kitwork-toasts";
    host.setAttribute("data-kitwork-ui", "toasts"); // kernel overlay: morph/Drive swaps keep it
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

  var store = window.kitwork.components.state(el);
  clearTimeout(store.toastTimer);
  store.toastTimer = setTimeout(function () {
    msg.setAttribute("data-state", "leave");
    msg.style.opacity = "0";
    msg.style.transform = "translateY(.5rem)";
    setTimeout(function () { msg.remove(); if (!host.children.length) host.remove(); }, 200);
  }, ms);
});
