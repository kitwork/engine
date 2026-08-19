package javascript

import (
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrowserOrderedFragmentsMatchStandaloneContract(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping standalone KitJS fragment browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	fixture := fragmentFixture()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/fragments.html" {
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(fixture))
			return
		}
		name := strings.TrimPrefix(request.URL.Path, "/")
		if !strings.HasPrefix(name, "src/") {
			http.NotFound(response, request)
			return
		}
		source, err := sources.ReadFile(name)
		if err != nil {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = response.Write(source)
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/fragments.html")
}

func fragmentFixture() string {
	var page strings.Builder
	page.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8"><title>KitJS fragments</title></head><body>
<main data-kit-component="counter">
  <button id="fragment-add" data-kit-click="count = count + 1">+</button>
  <output id="fragment-count" data-kit-text="count">server</output>
</main>
`)
	for _, name := range FragmentNames()[:len(FragmentNames())-1] {
		page.WriteString(`<script src="/` + html.EscapeString(name) + `"></script>` + "\n")
	}
	page.WriteString(`<script>
globalThis.__kitPublishedBeforeBoot = Object.prototype.hasOwnProperty.call(globalThis, "kit") || globalThis.kit !== undefined;
</script>
<script src="/src/boot.js"></script>
<script>kit.component("counter", { count: 0 });</script>
<script>
`)
	page.WriteString(browserHarness)
	page.WriteString(`
__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  assert(globalThis.__kitPublishedBeforeBoot === false, "kit was published before boot");
  assert(document[Symbol.for("kitjs:assembly")] === undefined, "assembly capsule survived boot");
  assert(Object.keys(kit).join(",") === "version,component", "fragment public API drifted");
  await waitFor(function () { return document.getElementById("fragment-count").textContent === "0"; }, "fragment runtime did not boot");
  document.getElementById("fragment-add").click();
  await waitFor(function () { return document.getElementById("fragment-count").textContent === "1"; }, "fragment action did not render");
});
</script></body></html>`)
	return page.String()
}
