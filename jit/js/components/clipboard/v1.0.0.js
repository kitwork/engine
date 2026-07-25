/* clipboard component @v1.0.0 — copy text to clipboard with feedback state.
 * Usage: <button data-kit-component="clipboard" data-kit-clipboard="npm i kitwork">Copy</button>
 */
window.kitwork.components.register("clipboard", function (el) {
  var target = null;
  var text = (el.getAttribute("data-kit-clipboard") || el.getAttribute("data-kit-copy") || el.getAttribute("data-kitwork-copy"));
  if (text == null) {
    target = window.kitwork.components.target(el);
    text = target ? (target.innerText || target.textContent || "") : "";
  }
  var copied = function () {
    el.classList.add("is-copied");
    var store = window.kitwork.components.state(el);
    clearTimeout(store.copyResetTimer);
    store.copyResetTimer = setTimeout(function () { el.classList.remove("is-copied"); }, 2000);
  };

  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(text).then(copied).catch(function () {});
  } else {
    var area = document.createElement("textarea");
    area.value = text;
    area.style.position = "fixed";
    area.style.opacity = "0";
    document.body.appendChild(area);
    area.select();
    try { document.execCommand("copy"); copied(); } catch (e) {}
    document.body.removeChild(area);
  }
});

// Alias for backward compatibility
window.kitwork.components.register("copy", function (el) {
  var fn = window.kitwork.components.get ? window.kitwork.components.get("clipboard") : null;
  if (fn) fn(el);
});
