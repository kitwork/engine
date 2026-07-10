/* more — progressive "load more" with NO fragment endpoint and NO server-side template-in-JS.
 * Fetches the NEXT PAGE (a normal, server-rendered, paginated page), finds the SAME list container
 * in it, skips items already present (deduped by their data-key), and appends the rest. The trigger
 * then advances to that page's own "next" link and retires itself on the last page. With JS off it
 * degrades to real pagination (the trigger is a normal <a href>).
 *
 *   <div id="list"> … <article data-key="123">…</article> … </div>
 *   <a href="/posts?page=2" data-kitwork-action="more" data-kitwork-target="#list">Load more</a>
 */
window.kitwork.components.action("more", function (el, e) {
  if (e && e.preventDefault) e.preventDefault();
  var store = window.kitwork.components.state(el);
  if (store.isLoading) return;
  var href = (el.getAttribute("data-kit-href") || el.getAttribute("data-kitwork-href")) || el.getAttribute("href");
  var selector = (el.getAttribute("data-kit-target") || el.getAttribute("data-kitwork-target"));
  var dest = selector ? document.querySelector(selector) : null;
  if (!href || !dest) return;
  store.isLoading = true;

  el.classList.add("is-loading");
  el.setAttribute("data-state", "loading");
  if (el.tagName === "BUTTON" || el.tagName === "INPUT" || el.tagName === "A") {
    el.style.pointerEvents = "none";
    if (el.tagName !== "A") el.disabled = true;
  }

  var fetchFn = window.kitwork.fetchWithRetry || function (u, o) { return fetch(u, o); };

  fetchFn(href, { credentials: "same-origin" })
    .then(function (r) { return r.text(); })
    .then(function (html) {
      var doc = new DOMParser().parseFromString(html, "text/html");
      var source = doc.querySelector(selector);
      if (source) {
        // Keys already in the live container → skip duplicates.
        var seen = {};
        var keySelector = "[data-kitwork-key],[data-kit-key],[data-key]";
        function getKey(n) {
          return (n.getAttribute("data-kit-key") || n.getAttribute("data-kitwork-key")) || n.getAttribute("data-kit-key") || n.getAttribute("data-key");
        }
        dest.querySelectorAll(keySelector).forEach(function (n) {
          var k = getKey(n);
          if (k) seen[k] = true;
        });
        source.querySelectorAll(keySelector).forEach(function (n) {
          var k = getKey(n);
          if (!k || seen[k]) return;
          seen[k] = true;
          dest.appendChild(document.importNode(n, true));
        });
      }
      // Advance the trigger to the fetched page's own "next" link; retire it on the last page.
      var next = doc.querySelector('[data-kit-action="more"],[data-kitwork-action="more"]');
      var nextHref = next && ((next.getAttribute("data-kit-href") || next.getAttribute("data-kitwork-href")) || next.getAttribute("href"));
      store.isLoading = false;
      el.classList.remove("is-loading");
      el.setAttribute("data-state", "ready");
      if (el.tagName === "BUTTON" || el.tagName === "INPUT" || el.tagName === "A") {
        el.style.pointerEvents = "";
        if (el.tagName !== "A") el.disabled = false;
      }

      if (nextHref) {
        el.setAttribute(el.hasAttribute("data-kitwork-href") ? "data-kitwork-href" : "href", nextHref);
      } else {
        el.remove();
      }
      document.dispatchEvent(new CustomEvent("kitwork:load", { detail: { url: href, target: dest } }));
    })
    .catch(function () {
      store.isLoading = false;
      el.classList.remove("is-loading");
      el.setAttribute("data-state", "error");
      if (el.tagName === "BUTTON" || el.tagName === "INPUT" || el.tagName === "A") {
        el.style.pointerEvents = "";
        if (el.tagName !== "A") el.disabled = false;
      }
    });
});
