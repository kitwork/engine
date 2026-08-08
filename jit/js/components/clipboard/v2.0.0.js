/* clipboard component @v2.0.0 (latest) — copy text to clipboard with 2s feedback.
 * Usage:
 *   - <div data-kit-component="clipboard">
 *   - <div data-kit-component="clipboard@v2.0.0">
 */
var clipboardDef = {
  copied: false,
  copy: function (text) {
    var self = this;
    var copied = function () {
      self.copied = true;
      clearTimeout(self.copyResetTimer);
      self.copyResetTimer = setTimeout(function () { self.copied = false; }, 2000);
    };
    var fallback = function () {
      var active = document.activeElement;
      var area = document.createElement("textarea");
      area.value = text == null ? "" : String(text);
      area.setAttribute("readonly", "");
      area.setAttribute("aria-hidden", "true");
      area.tabIndex = -1;
      area.style.position = "fixed";
      area.style.opacity = "0";
      document.body.appendChild(area);
      area.focus();
      area.select();
      var ok = false;
      try { ok = document.execCommand("copy"); } catch (_) { ok = false; }
      document.body.removeChild(area);
      if (active && active.focus) {
        try { active.focus({ preventScroll: true }); } catch (_) { active.focus(); }
      }
      if (ok) copied();
    };
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(copied).catch(fallback);
    } else {
      fallback();
    }
  }
};

window.kit.component("clipboard", clipboardDef);
window.kit.component("clipboard@v2.0.0", clipboardDef);
window.kit.component("copy", clipboardDef);
window.kit.component("copy@v2.0.0", clipboardDef);
