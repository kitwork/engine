/* clipboard component @v2.0.0 (latest) — copy text to clipboard with 2s feedback.
 * Usage:
 *   - <div data-kit-component="clipboard">
 *   - <div data-kit-component="clipboard@v2.0.0">
 */
var clipboardDef = {
  copied: false,
  copy: function (text) {
    if (!navigator.clipboard || !navigator.clipboard.writeText) return;
    var self = this;
    navigator.clipboard.writeText(text).then(function () {
      self.copied = true;
      setTimeout(function () { self.copied = false; }, 2000);
    }).catch(function () {});
  }
};

window.kit.component("clipboard", clipboardDef);
window.kit.component("clipboard@v2.0.0", clipboardDef);
window.kit.component("copy", clipboardDef);
window.kit.component("copy@v2.0.0", clipboardDef);
