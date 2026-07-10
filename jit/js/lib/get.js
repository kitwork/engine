/* get — fetch a server-rendered HTML fragment and swap it into a target, no full page load. The
 * thin, SSR-first data layer: the server renders the rows/results, this just swaps them in.
 *
 *   <a href="/users?page=2" data-kitwork-action="get"
 *      data-kitwork-target="#rows" data-kitwork-swap="append">Load more</a>
 *
 * URL: data-kitwork-href, else the element's own href. data-kitwork-swap: "replace" (innerHTML,
 * default) | "append" | "prepend" | "outer". data-kitwork-push="true" → pushState the URL. Adds
 * `.is-loading` to the target while in flight and cancels a prior request to the same target. */
window.kitwork.components.action("get", function (el, e) {
  if (e && e.preventDefault) e.preventDefault();
  var href = (el.getAttribute("data-kit-href") || el.getAttribute("data-kitwork-href")) || el.getAttribute("href");
  var target = window.kitwork.components.target(el);
  if (!href || !target) return;
  var swap = (el.getAttribute("data-kit-swap") || el.getAttribute("data-kitwork-swap")) || "replace";

  var store = window.kitwork.components.state(target);
  if (store.fetchController) store.fetchController.abort();
  var controller = ("AbortController" in window) ? new AbortController() : null;
  store.fetchController = controller;

  target.classList.add("is-loading");
  el.setAttribute("data-state", "loading");
  if (el.tagName === "BUTTON" || el.tagName === "INPUT") el.disabled = true;

  var fetchFn = window.kitwork.fetchWithRetry || function (u, o) { return fetch(u, o); };

  fetchFn(href, {
    headers: { "X-Kitwork-Fragment": "1" },
    credentials: "same-origin",
    signal: controller && controller.signal
  })
    .then(function (r) { return r.text(); })
    .then(function (html) {
      store.fetchController = null;
      target.classList.remove("is-loading");
      el.setAttribute("data-state", "ready");
      if (el.tagName === "BUTTON" || el.tagName === "INPUT") el.disabled = false;

      var landed = target;
      if (swap === "append") target.insertAdjacentHTML("beforeend", html);
      else if (swap === "prepend") target.insertAdjacentHTML("afterbegin", html);
      else if (swap === "outer") { target.outerHTML = html; landed = document.querySelector((el.getAttribute("data-kit-target") || el.getAttribute("data-kitwork-target"))) || document.body; }
      else target.innerHTML = html;
      if ((el.getAttribute("data-kit-push") || el.getAttribute("data-kitwork-push")) === "true") {
        try { history.pushState({}, "", href); } catch (e2) {}
      }
      document.dispatchEvent(new CustomEvent("kitwork:load", { detail: { url: href, target: landed } }));
    })
    .catch(function (err) {
      if (err && err.name === "AbortError") return;
      store.fetchController = null;
      target.classList.remove("is-loading");
      el.setAttribute("data-state", "error");
      if (el.tagName === "BUTTON" || el.tagName === "INPUT") el.disabled = false;
    });
});
