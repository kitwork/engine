/* copy component @v1.0.0 — copy text to clipboard with 1s feedback.
 * Supports: <div data-kit-component="copy@v1.0.0">
 */
window.kitwork.component("copy@v1.0.0", {
  copied: false,
  copy: function (text) {
    if (!navigator.clipboard || !navigator.clipboard.writeText) return;
    var self = this;
    navigator.clipboard.writeText(text).then(function () {
      self.copied = true;
      setTimeout(function () { self.copied = false; }, 1000);
    }).catch(function () {});
  }
});
