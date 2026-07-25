/* get component @v1.0.0 — fetch content and place in target element.
 * Usage: <button data-kit-component="get" data-kit-url="/api/data" data-kit-target="#result">Fetch</button>
 */
window.kitwork.components.action("get", function (el) {
  var url = el.getAttribute("data-kit-url") || el.getAttribute("data-kitwork-url");
  if (!url) return;
  var target = window.kitwork.components.target(el);
  if (!target) return;
  fetch(url, { headers: { "X-Kitwork-Hydrate": "1" } })
    .then(function (r) { return r.text(); })
    .then(function (html) { target.innerHTML = html; })
    .catch(function () {});
});
