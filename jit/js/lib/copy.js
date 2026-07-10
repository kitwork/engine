/* copy verb — copy data-kitwork-copy (or the target's text) to the clipboard.
 * Flags `.is-copied` for 2s so the author renders the "Copied!" state purely in CSS.
 * Supports: <button data-kitwork-action="copy" data-kitwork-copy="npm i kitwork">Copy</button>
 */
window.kitwork.components.action("copy", function (el) {
  var text = (el.getAttribute("data-kit-copy") || el.getAttribute("data-kitwork-copy"));
  if (text == null) {
    var t = window.kitwork.components.target(el);
    text = t ? (t.innerText || t.textContent || "") : "";
  }
  if (!navigator.clipboard || !navigator.clipboard.writeText) return;
  navigator.clipboard.writeText(text).then(function () {
    el.classList.add("is-copied");
    var store = window.kitwork.components.state(el);
    clearTimeout(store.copyResetTimer);
    store.copyResetTimer = setTimeout(function () { el.classList.remove("is-copied"); }, 2000);
  }).catch(function () {});
});
