package js

import "testing"

// progressbar is the page's view of kit.progress: markup reads value/active from the component, the
// component's getters read the service, so the bar shows whatever any source reported — JavaScript
// calling kit.progress, or the page driving it through the alias — and nothing is copied.
func TestBrowserProgressbarShowsKitProgress(t *testing.T) {
	runBrowserProof(t, "progressbar", nil, `
<div id="bar" data-kit-component="progressbar" data-kit-alias="$progress" data-kit-show="active" hidden
     role="progressbar" aria-label="Loading" aria-valuemin="0" aria-valuemax="100" data-kit-bind:aria-valuenow="value">
  <div id="fill" data-kit-style:width="value + '%'"></div>
</div>
<button id="upload" data-kit-click="$progress.update(40)">40%</button>
<button id="done" data-kit-click="$progress.finish()">done</button>
<script>
(async function () {
  await wait(150);
  var byId = function (id) { return document.getElementById(id); };
  var width = function () { return parseFloat(byId("fill").style.width || "0"); };
  assert(byId("bar").hidden, "hidden while nothing is in flight");

  kit.progress.start(); await wait(700);
  assert(!byId("bar").hidden && width() > 10 && width() < 90, "a source in JavaScript starts it; it creeps toward 90: " + width());
  assert(byId("bar").getAttribute("aria-valuenow") === String(kit.progress.value), "aria-valuenow follows the service: " + byId("bar").getAttribute("aria-valuenow"));

  byId("upload").click(); await wait(300);
  assert(width() === 40, "the page drives it through the alias, and a known amount stays put: " + width());

  byId("done").click(); await wait(40);
  assert(width() === 100, "finish goes to 100: " + width());
  await wait(400);
  assert(byId("bar").hidden && !kit.progress.active, "then the bar leaves with the work");

  kit.progress.finish(); await wait(40);
  assert(byId("bar").hidden, "finishing nothing shows nothing");
  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}
