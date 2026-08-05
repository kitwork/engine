/* get component @v1.0.0 — fetch content.
 * Usage: <div data-kit-component="get">
 */
var getDef = {
  loading: false,
  data: null,
  fetch: function (url, targetId) {
    var self = this;
    self.loading = true;
    fetch(url, { headers: { "X-Kitwork-Hydrate": "1" } })
      .then(function (r) { return r.text(); })
      .then(function (html) {
        self.loading = false;
        self.data = html;
        if (targetId) {
          var el = document.querySelector(targetId);
          if (el) el.innerHTML = html;
        }
      }).catch(function () { self.loading = false; });
  }
};

window.kit.component("get", getDef);
window.kit.component("get@v1.0.0", getDef);
