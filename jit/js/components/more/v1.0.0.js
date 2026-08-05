/* more component @v1.0.0 — load more items into list.
 * Usage: <div data-kit-component="more">
 */
var moreDef = {
  loading: false,
  load: function (url, targetId) {
    var self = this;
    self.loading = true;
    fetch(url, { headers: { "X-Kitwork-Hydrate": "1" } })
      .then(function (r) { return r.text(); })
      .then(function (html) {
        self.loading = false;
        if (targetId) {
          var el = document.querySelector(targetId);
          if (el) el.insertAdjacentHTML("beforeend", html);
        }
      }).catch(function () { self.loading = false; });
  }
};

window.kit.component("more", moreDef);
window.kit.component("more@v1.0.0", moreDef);
