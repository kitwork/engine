/* submit component @v1.0.0 — submit form via fetch.
 * Usage: <form data-kit-component="submit" action="/api/save">...</form>
 */
window.kitwork.components.action("submit", function (el) {
  var form = el.tagName === "FORM" ? el : el.closest("form");
  if (!form) return;
  var action = form.action || location.href;
  var method = (form.method || "POST").toUpperCase();
  var body = new FormData(form);
  fetch(action, { method: method, body: body, headers: { "X-Kitwork-Hydrate": "1" } })
    .catch(function () {});
});
