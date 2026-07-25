/* more component @v1.0.0 — load more items into list.
 * Usage: <button data-kit-component="more" data-kit-url="/items?page=2" data-kit-target="#list">Load More</button>
 */
window.kitwork.components.action("more", function (el) {
  var url = el.getAttribute("data-kit-url") || el.getAttribute("data-kitwork-url");
  if (!url) return;
  var target = window.kitwork.components.target(el);
  if (!target) return;
  fetch(url, { headers: { "X-Kitwork-Hydrate": "1" } })
    .then(function (r) { return r.text(); })
    .then(function (html) { target.insertAdjacentHTML("beforeend", html); })
    .catch(function () {});
});
