package js

import (
	"strings"
	"testing"
)

const counterSource = `kit.component("counter", {
  count: 0, min: 0, max: 10, step: 1, startMax: 0,
  increment: function () { this.set(this.count + this.step); },
  decrement: function () { this.set(this.count - this.step); },
  set: function (value) { this.count = Math.min(this.max, Math.max(this.min, value)); },
  init: function () { this.startMax = this.max; }
});`

// A data-kit-scope literal on a component host sets that instance's parameters over the blueprint's
// defaults (03/10): typed, evaluated once in the scope around the host, in place before init runs.
// A host without one keeps the defaults — the control.
func TestBrowserAHostLiteralSetsTheComponentsParameters(t *testing.T) {
	runBrowserProof(t, "host literal", nil, `
<div data-kit-scope="limit: 5">
  <div id="a" data-kit-component="counter" data-kit-scope="min: 1, max: 3, count: 2">
    <button id="a-plus" data-kit-click="increment()">+</button><button id="a-minus" data-kit-click="decrement()">−</button>
    <span id="a-out" data-kit-text="count"></span>
  </div>
  <div id="b" data-kit-component="counter"><span id="b-out" data-kit-text="count + '/' + max"></span></div>
  <div id="c" data-kit-component="counter" data-kit-scope="max: limit"><span id="c-out" data-kit-text="max"></span></div>
</div>
<script>
`+counterSource+`
(async function () {
  await wait(150);
  var byId = function (id) { return document.getElementById(id); };
  var t = function (id) { return byId(id).textContent; };
  assert(t("a-out") === "2", "the literal's count is the starting value: " + t("a-out"));
  for (var i = 0; i < 4; i++) byId("a-plus").click();
  await wait(30);
  assert(t("a-out") === "3", "its max (3) clamps, not the blueprint's 10: " + t("a-out"));
  for (var j = 0; j < 5; j++) byId("a-minus").click();
  await wait(30);
  assert(t("a-out") === "1", "its min (1) clamps: " + t("a-out"));
  assert(window.kit.scopeFor(byId("a")).startMax === 3, "init already saw the host's values");
  assert(t("b-out") === "0/10", "a host without a literal keeps the defaults: " + t("b-out"));
  assert(t("c-out") === "5", "the literal reads the scope around the host: " + t("c-out"));
  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}

// The server reads the component, so a literal key it does not have is named at render.
func TestAHostLiteralKeyTheComponentLacksIsReported(t *testing.T) {
	site := fakeSite{
		"counter":   counterSource,
		"workbench": `kit.component("workbench", { init: function () { var k = "x"; this[k] = 1; } });`,
	}
	check := func(html string) string { return capturedCheck(t, html, ComponentNames(site, nil)) }

	report := check(`<html><body><div data-kit-component="counter" data-kit-scope="mx: 99"></div></body></html>`)
	if !strings.Contains(report, `data-kit-scope sets "mx" on component counter`) || !strings.Contains(report, "max") {
		t.Fatalf("a typo in a parameter should be reported with the component's names, got:\n%s", report)
	}
	for label, html := range map[string]string{
		"CONTROL: parameters it has":    `<html><body><div data-kit-component="counter" data-kit-scope="min: 1, max: 99, count: 1"></div></body></html>`,
		"a component it cannot read":    `<html><body><div data-kit-component="workbench" data-kit-scope="anything: 1"></div></body></html>`,
		"a plain scope, no component":   `<html><body><div data-kit-scope="mx: 99"><b data-kit-text="mx"></b></div></body></html>`,
		"state inside the host, nested": `<html><body><div data-kit-component="counter"><div data-kit-scope="note: ''"><b data-kit-text="note"></b></div></div></body></html>`,
	} {
		if report := check(html); report != "" {
			t.Errorf("%s: nothing should be reported, got:\n%s", label, report)
		}
	}
}
