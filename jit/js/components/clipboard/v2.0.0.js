/* clipboard component — copy text, and say so for two seconds. The file a page gets for
 * data-kit-component="clipboard" (the latest).
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
    // A page copies what it reads out of its own markup — copy($element.snippet.textContent) — and
    // an element name that does not resolve gives undefined. Without this the reader gets the word
    // "undefined" on their clipboard, with the confirmation saying it worked. Numbers are allowed:
    // a price or a count is a reasonable thing to copy.
    if (typeof text === "number" && isFinite(text)) text = String(text);
    if (typeof text !== "string" || text === "") return;
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
