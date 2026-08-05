/* submit component @v1.0.0 — submit form via fetch.
 * Usage: <div data-kit-component="submit">
 */
var submitDef = {
  submitting: false,
  send: function (formEl) {
    var self = this;
    if (!formEl) return;
    self.submitting = true;
    var action = formEl.action || location.href;
    var method = (formEl.method || "POST").toUpperCase();
    var body = new FormData(formEl);
    fetch(action, { method: method, body: body, headers: { "X-Kitwork-Hydrate": "1" } })
      .then(function () { self.submitting = false; })
      .catch(function () { self.submitting = false; });
  }
};

window.kit.component("submit", submitDef);
window.kit.component("submit@v1.0.0", submitDef);
