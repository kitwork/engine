/* clipboard component @v2.0.0 (latest) — copy text, and say so for two seconds.
 *
 * Usage (the host is the shared ancestor of the button and whatever it copies):
 *   <article data-kit-component="clipboard">
 *     <pre data-kit-element="snippet">npm i kitwork</pre>
 *     <button data-kit-click="copy($element.snippet.textContent)"
 *             data-kit-class="copied ? 'is-copied' : ''">Copy</button>
 *   </article>
 *
 * Accessibility: `copied` is state, not an announcement — put it where a reader will hear it
 * (aria-live="polite") if the confirmation matters.
 *
 * The platform call needs a user gesture and a secure context; where it is refused, the component
 * falls back to a hidden textarea and execCommand, restoring focus afterwards. The reset timer is
 * released with the host, so a copy confirmed just before Drive swaps the page leaves nothing.
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

clipboardDef.init = function (context) {
  var self = this;
  context.cleanup(function () { clearTimeout(self.copyResetTimer); });
};

window.kit.component("clipboard", clipboardDef);
window.kit.component("clipboard@v2.0.0", clipboardDef);
