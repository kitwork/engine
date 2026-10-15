/* clipboard component — copy text to clipboard with 1s feedback.
 * Usage: <div data-kit-component="clipboard@v1.0.0"> — only a template page that names this version
 * gets this file; data-kit-component="clipboard" gets the latest.
 */
var clipboardDef = {
  copied: false,
  copy: function (text) {
    if (!navigator.clipboard || !navigator.clipboard.writeText) return;
    var self = this;
    navigator.clipboard.writeText(text).then(function () {
      self.copied = true;
      setTimeout(function () { self.copied = false; }, 1000);
    }).catch(function () {});
  }
};

window.kit.component("clipboard", clipboardDef);
