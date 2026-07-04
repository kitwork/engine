/* submit — submit a form over fetch and swap the server's HTML response into a target, no full
 * reload. Method + action come from the <form>; with JS off it's a normal form submit (PE).
 *
 *   <form action="/subscribe" method="post" data-kitwork-action="submit"
 *         data-kitwork-target="#result" data-kitwork-swap="replace"> … </form>
 *
 * data-kitwork-swap: "replace" (target's innerHTML, default) | "append" | "prepend" | "outer".
 * Adds `.is-loading` to the form while in flight; ignores re-submits during a request. */
window.kitwork.components.action("submit", function (form, e) {
  if (e && e.preventDefault) e.preventDefault();
  var store = window.kitwork.components.state(form);
  if (store.isLoading) return;
  var target = window.kitwork.components.target(form);
  var swap = form.getAttribute("data-kitwork-swap") || "replace";
  var method = (form.getAttribute("method") || "get").toUpperCase();
  var action = form.getAttribute("action") || location.href;
  var formData = new FormData(form);
  var options = { method: method, credentials: "same-origin", headers: { "X-Kitwork-Fragment": "1" } };
  if (method === "GET") {
    var query = new URLSearchParams(formData).toString();
    action += (action.indexOf("?") >= 0 ? "&" : "?") + query;
  } else {
    options.body = formData;
  }

  store.isLoading = true;
  form.classList.add("is-loading");
  form.setAttribute("data-state", "loading");
  var subButtons = form.querySelectorAll('button[type="submit"], input[type="submit"]');
  subButtons.forEach(function (btn) { btn.disabled = true; });

  var fetchFn = window.kitwork.fetchWithRetry || function (u, o) { return fetch(u, o); };

  fetchFn(action, options)
    .then(function (r) { return r.text(); })
    .then(function (html) {
      store.isLoading = false;
      form.classList.remove("is-loading");
      form.setAttribute("data-state", "ready");
      subButtons.forEach(function (btn) { btn.disabled = false; });

      if (!target) return;
      if (swap === "append") target.insertAdjacentHTML("beforeend", html);
      else if (swap === "prepend") target.insertAdjacentHTML("afterbegin", html);
      else if (swap === "outer") target.outerHTML = html;
      else target.innerHTML = html;
      document.dispatchEvent(new CustomEvent("kitwork:load", { detail: { url: action, target: target } }));
    })
    .catch(function () {
      store.isLoading = false;
      form.classList.remove("is-loading");
      form.setAttribute("data-state", "error");
      subButtons.forEach(function (btn) { btn.disabled = false; });
    });
});
