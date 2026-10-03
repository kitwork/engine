package js

import "testing"

// theme is the page's view of kit.theme: a toggle host reads resolved/mode from its getters, another
// part of the page reads and sets it through the alias, and two hosts never disagree, because neither
// holds the choice — the service does.
func TestBrowserThemeComponentShowsKitTheme(t *testing.T) {
	runBrowserProof(t, "theme", nil, `
<button id="toggle" data-kit-component="theme" data-kit-alias="$theme" data-kit-click="toggle()">
  <span id="icon" data-kit-text="resolved === 'dark' ? 'moon' : 'sun'"></span>
</button>
<button id="mobile" data-kit-component="theme" data-kit-click="toggle()"><span id="mobile-icon" data-kit-text="resolved"></span></button>
<span id="mode" data-kit-text="$theme.mode"></span>
<button id="dark" data-kit-click="$theme.set('dark')">dark</button>
<button id="system" data-kit-click="$theme.system()">system</button>
<script>
(async function () {
  try { localStorage.removeItem("theme"); } catch (_) {}
  await wait(150);
  var byId = function (id) { return document.getElementById(id); };
  var html = document.documentElement;
  byId("dark").click(); await wait(30);
  assert(html.classList.contains("dark") && byId("icon").textContent === "moon" && byId("mode").textContent === "dark", "set('dark') through the alias: " + byId("icon").textContent + " " + byId("mode").textContent);
  byId("toggle").click(); await wait(30);
  assert(!html.classList.contains("dark") && byId("icon").textContent === "sun" && byId("mobile-icon").textContent === "light", "toggle() in its host, and the second host agrees: " + byId("icon").textContent + " " + byId("mobile-icon").textContent);
  byId("mobile").click(); await wait(30);
  assert(html.classList.contains("dark") && byId("icon").textContent === "moon", "the other host toggles the same choice: " + byId("icon").textContent);
  byId("system").click(); await wait(30);
  assert(byId("mode").textContent === "system", "system(): " + byId("mode").textContent);
  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}
