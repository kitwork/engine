package js

import "testing"

// The KitJS catalogue had a `data-table` component: seventeen methods — sortBy, search, filtered,
// sorted, visible, total, pages, from, to, next, previous … — and not one of them moved focus,
// handled a key or measured anything. It is all data, and since the grammar learned array methods
// (fbd6665e) and the globals (44257a2e) the data can be said where a reader sees it: a scope that
// names each step once, a data-kit-for over the last step, and assignments on the buttons.
//
// So data-table is grammar, not a component. This proof is the evidence; if it ever needs code of
// its own to pass, that code is the component it would need.
func TestBrowserDataTableIsGrammar(t *testing.T) {
	runBrowserProof(t, "data table", nil, `
<div data-kit-scope="{
  rows: [
    { site: 'shop.example.com', requests: 128400, status: 'live' },
    { site: 'blog.example.com', requests: 5120, status: 'live' },
    { site: 'docs.example.com', requests: 40210, status: 'paused' },
    { site: 'api.example.com', requests: 99100, status: 'live' },
    { site: 'mail.example.com', requests: 870, status: 'paused' },
    { site: 'cdn.example.com', requests: 310000, status: 'live' }
  ],
  query: '', key: 'site', desc: false, page: 1, size: 4,
  matching: () => rows.filter((r) => !query || (r.site + ' ' + r.status).includes(query.trim().toLowerCase())),
  ordered: () => matching().slice().sort((a, b) => (a[key] > b[key] ? 1 : a[key] < b[key] ? -1 : 0) * (desc ? -1 : 1)),
  pages: () => Math.max(1, Math.ceil(matching().length / size)),
  shown: () => ordered().slice((page - 1) * size, page * size)
}">
  <input id="filter" type="search" data-kit-model="query" data-kit-input="page = 1">
  <table>
    <thead><tr>
      <th id="th-site" data-kit-bind:aria-sort="key === 'site' ? (desc ? 'descending' : 'ascending') : 'none'">
        <button id="by-site" data-kit-click="desc = key === 'site' ? !desc : false; key = 'site'; page = 1">Site</button></th>
      <th id="th-requests" data-kit-bind:aria-sort="key === 'requests' ? (desc ? 'descending' : 'ascending') : 'none'">
        <button id="by-requests" data-kit-click="desc = key === 'requests' ? !desc : false; key = 'requests'; page = 1">Requests</button></th>
    </tr></thead>
    <tbody id="body">
      <tr data-kit-for="row of shown()" data-kit-key="row.site"><td data-kit-text="row.site"></td><td data-kit-text="row.requests"></td></tr>
    </tbody>
  </table>
  <span id="range" data-kit-text="(matching().length ? (page - 1) * size + 1 : 0) + '–' + Math.min(page * size, matching().length) + ' of ' + matching().length"></span>
  <button id="prev" data-kit-click="page--" data-kit-bind:disabled="page <= 1">prev</button>
  <span id="pages" data-kit-text="page + ' / ' + pages()"></span>
  <button id="next" data-kit-click="page++" data-kit-bind:disabled="page >= pages()">next</button>
</div>
<script>
(async function () {
  await wait(80);
  var sites = function () { return [].slice.call(document.querySelectorAll("#body tr")).map(function (tr) { return tr.firstElementChild.textContent.split(".")[0]; }).join(","); };
  var text = function (id) { return document.getElementById(id).textContent; };

  // Sorted by site, first page of four.
  assert(sites() === "api,blog,cdn,docs", "first page sorted by site, got " + sites());
  assert(text("range") === "1–4 of 6" && text("pages") === "1 / 2", "the range and page count come from Math.ceil, got " + text("range") + " · " + text("pages"));
  assert(document.getElementById("th-site").getAttribute("aria-sort") === "ascending", "aria-sort says ascending");

  // The second page, and the buttons know the ends.
  document.getElementById("next").click(); await wait(40);
  assert(sites() === "mail,shop" && document.getElementById("next").disabled, "page two holds the last two and Next is disabled, got " + sites());

  // Sort by requests: ascending, then the same header again for descending — back on page one.
  document.getElementById("by-requests").click(); await wait(40);
  assert(sites() === "mail,blog,docs,api" && text("pages") === "1 / 2", "requests ascending, back on page one, got " + sites());
  document.getElementById("by-requests").click(); await wait(40);
  assert(sites() === "cdn,shop,api,docs", "the same header again sorts descending, got " + sites());
  assert(document.getElementById("th-requests").getAttribute("aria-sort") === "descending" && document.getElementById("th-site").getAttribute("aria-sort") === "none", "aria-sort moves with the sort");

  // The filter narrows the rows, the count and the pages together.
  var filter = document.getElementById("filter");
  filter.value = "paused"; filter.dispatchEvent(new Event("input", { bubbles: true })); await wait(40);
  var sc = window.kit.scopeFor(filter);
  assert(sites() === "docs,mail" && text("range") === "1–2 of 2" && text("pages") === "1 / 1", "filtering by status leaves two rows on one page, got " + sites() + " · " + text("range") + " (query=" + JSON.stringify(sc.query) + " page=" + sc.page + " matching=" + (typeof sc.matching === "function" ? "fn" : typeof sc.matching) + ")");
  filter.value = "nothing"; filter.dispatchEvent(new Event("input", { bubbles: true })); await wait(40);
  assert(sites() === "" && text("range") === "0–0 of 0" && text("pages") === "1 / 1", "no match, no rows, still one page, got " + text("range"));

  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}
