/* copy component @v2.0.0 (latest) — copy text to clipboard with 2s feedback.
 * Supports:
 *   - <div data-kit-component="copy">
 *   - <div data-kit-component="copy@v2.0.0">
 */
var copyDef = {
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

window.kitwork.component("copy", copyDef);
window.kitwork.component("copy@v2.0.0", copyDef);
