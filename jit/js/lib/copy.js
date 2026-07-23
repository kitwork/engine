/* copy verb — copy data-kitwork-copy (or the target's text) to the clipboard.
 * Flags `.is-copied` for 2s so the author renders the "Copied!" state purely in CSS.
 * Supports: <button data-kitwork-action="copy" data-kitwork-copy="npm i kitwork">Copy</button>
 */
window.kitwork.components.action("copy", function (el) {
  var target = null;
  var text = (el.getAttribute("data-kit-copy") || el.getAttribute("data-kitwork-copy"));
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
  var selected = function () {
    if (!target || !window.getSelection || !document.createRange) return;
    var range = document.createRange();
    var selection = window.getSelection();
    range.selectNodeContents(target);
    selection.removeAllRanges();
    selection.addRange(range);
    el.classList.add("is-copy-selected");
    var store = window.kitwork.components.state(el);
    clearTimeout(store.copySelectedResetTimer);
    store.copySelectedResetTimer = setTimeout(function () { el.classList.remove("is-copy-selected"); }, 4000);
  };
  var fallback = function () {
    var area = document.createElement("textarea");
    area.value = text;
    area.setAttribute("readonly", "");
    area.style.position = "fixed";
    area.style.opacity = "0";
    document.body.appendChild(area);
    area.select();
    var ok = false;
    try { ok = document.execCommand("copy"); } catch (error) { ok = false; }
    document.body.removeChild(area);
    if (ok) copied();
    else selected();
  };
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(text).then(copied).catch(fallback);
  } else {
    fallback();
  }
});
