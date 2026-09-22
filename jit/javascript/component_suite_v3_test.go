package javascript

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The third KitJS UI suite adds the common input/disclosure families. It stays
// state-only: authored HTML owns the semantic controls, ARIA, focus movement,
// and visual transitions, while these packages own only reactive state and its
// bounded transitions.
var componentSuiteV3Contracts = []struct {
	name    string
	fields  []string
	methods []string
}{
	{name: "stepper", fields: []string{"value", "min", "max", "step", "disabled"}, methods: []string{"set", "increment", "decrement", "canIncrement", "canDecrement"}},
	{name: "slider", fields: []string{"value", "min", "max", "step", "page", "disabled"}, methods: []string{"set", "increment", "decrement", "pageUp", "pageDown", "toStart", "toEnd", "percent"}},
	{name: "rating", fields: []string{"value", "max", "readonly", "hovered"}, methods: []string{"rate", "hover", "clear", "current", "isFilled"}},
	{name: "tags", fields: []string{"tags", "draft", "max"}, methods: []string{"add", "remove", "removeLast", "clear", "has", "canAdd"}},
	{name: "collapse", fields: []string{"open", "disabled"}, methods: []string{"show", "hide", "toggle"}},
	{name: "combobox", fields: []string{"open", "query", "options", "activeIndex", "selected"}, methods: []string{"matches", "filtered", "search", "show", "hide", "toggle", "next", "previous", "choose", "chooseActive", "isActive"}},
}

func TestComponentSuiteV3SourcesAreClosedStatePackages(t *testing.T) {
	for _, contract := range componentSuiteV3Contracts {
		contract := contract
		t.Run(contract.name, func(t *testing.T) {
			source := readVanillaFile(t, "component", contract.name, "1.0.0.js")
			registration := []byte(`kit.component("` + contract.name + `"`)
			if len(source) == 0 || source[0] != ';' || source[len(source)-1] != '\n' || bytes.Contains(source, []byte{'\r'}) {
				t.Fatal("component is not a sealable LF-only classic script")
			}
			if got := bytes.Count(source, registration); got != 1 {
				t.Fatalf("registration count = %d, want 1", got)
			}
			for _, field := range contract.fields {
				if !bytes.Contains(source, []byte(field+":")) {
					t.Fatalf("public field %q is absent", field)
				}
			}
			for _, method := range contract.methods {
				if !bytes.Contains(source, []byte(method+": function")) {
					t.Fatalf("public method %q is absent", method)
				}
			}
			for _, forbidden := range [][]byte{
				[]byte("kit.service("), []byte("document."), []byte("window."),
				[]byte("addEventListener("), []byte("removeEventListener("),
				[]byte("setTimeout("), []byte("setInterval("), []byte("WeakMap"),
				[]byte("$host"), []byte("$element"), []byte("__kitwork_core__"), []byte("init:"),
			} {
				if bytes.Contains(source, forbidden) {
					t.Fatalf("state package contains forbidden runtime ownership %q", forbidden)
				}
			}
		})
	}
}

func TestComponentSuiteV3CatalogDefaultsAreDependencyFree(t *testing.T) {
	catalog, err := loadDeliveryCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, contract := range componentSuiteV3Contracts {
		component, err := catalog.component(contract.name, "")
		if err != nil {
			t.Fatal(err)
		}
		if component.identity != (ComponentVersion{Name: contract.name, Version: "1.0.0"}) {
			t.Fatalf("%s default identity = %#v", contract.name, component.identity)
		}
		if len(component.requires) != 0 {
			t.Fatalf("%s dependencies = %#v, want none", contract.name, component.requires)
		}
	}
	if _, err := catalog.component("stepper", "2.0.0"); !errors.Is(err, ErrModuleNotFound) {
		t.Fatalf("unknown suite version error = %v", err)
	}
}

func TestComponentSuiteV3ArtifactsContainOnlySelectedPackage(t *testing.T) {
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	for _, selected := range componentSuiteV3Contracts {
		bundle, err := composer.ComposeStandalone([]ComponentRef{{Name: selected.name}}, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, candidate := range componentSuiteV3Contracts {
			got := bytes.Count(bundle.JavaScript, []byte(`kit.component("`+candidate.name+`"`))
			want := 0
			if candidate.name == selected.name {
				want = 1
			}
			if got != want {
				t.Fatalf("%s-only registration count for %s = %d, want %d", selected.name, candidate.name, got, want)
			}
		}
		if bytes.Contains(bundle.JavaScript, []byte(`kit.service("`)) {
			t.Fatalf("%s-only artifact unexpectedly sealed a service", selected.name)
		}
	}
}

func TestComponentSuiteV3ExactHTMLScanMatchesExplicitSelection(t *testing.T) {
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	for _, contract := range componentSuiteV3Contracts {
		html := []byte(`<main data-kit-component="` + contract.name + `@1.0.0"></main>`)
		use, err := ScanHTML(html)
		if err != nil {
			t.Fatal(err)
		}
		if !use.NeedsRuntime || len(use.Components) != 1 ||
			use.Components[0].Name != contract.name || use.Components[0].Version != "1.0.0" ||
			use.Components[0].Alias != "" {
			t.Fatalf("%s scan = %#v", contract.name, use)
		}
		fromHTML, err := composer.ComposeHTML(html)
		if err != nil {
			t.Fatal(err)
		}
		explicit, err := composer.ComposeStandalone([]ComponentRef{{Name: contract.name, Version: "1.0.0"}}, false)
		if err != nil {
			t.Fatal(err)
		}
		if fromHTML.ContentHash != explicit.ContentHash || !bytes.Equal(fromHTML.JavaScript, explicit.JavaScript) {
			t.Fatalf("%s scanned and explicit selections produced different artifacts", contract.name)
		}
	}
}

func TestBrowserComponentSuiteV3StateAndDirectiveContract(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping component-suite v3 browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	references := make([]ComponentRef, 0, len(componentSuiteV3Contracts))
	for _, contract := range componentSuiteV3Contracts {
		references = append(references, ComponentRef{Name: contract.name, Version: "1.0.0"})
	}
	bundle, err := composer.ComposeStandalone(references, false)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/suite-v3.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(bundle.JavaScript)
		case "/suite-v3.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(componentSuiteV3Document))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/suite-v3.html")
}

var componentSuiteV3Document = fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>KitJS component suite v3</title></head><body>
  <section data-kit-component="stepper@1.0.0" data-kit-scope="value: 5, min: 0, max: 10, step: 2">
    <button id="stepper-inc" data-kit-click="increment()">+</button>
    <button id="stepper-dec" data-kit-click="decrement()">-</button>
    <button id="stepper-disable" data-kit-click="disabled = true">disable</button>
    <output id="stepper-value" data-kit-text="value"></output>
    <output id="stepper-can" data-kit-text="canIncrement() ? 'yes' : 'no'"></output>
  </section>

  <section data-kit-component="slider@1.0.0" data-kit-scope="value: 20, min: 0, max: 100, step: 5, page: 25">
    <button id="slider-inc" data-kit-click="increment()">+</button>
    <button id="slider-page" data-kit-click="pageUp()">page</button>
    <button id="slider-end" data-kit-click="toEnd()">end</button>
    <output id="slider-value" data-kit-text="value"></output>
    <output id="slider-pct" data-kit-text="percent()"></output>
  </section>

  <section data-kit-component="rating@1.0.0" data-kit-scope="max: 5">
    <button id="rating-3" data-kit-click="rate(3)">3</button>
    <button id="rating-9" data-kit-click="rate(9)">9</button>
    <button id="rating-clear" data-kit-click="clear()">clear</button>
    <output id="rating-value" data-kit-text="value"></output>
    <output id="rating-star3" data-kit-text="isFilled(3) ? 'on' : 'off'"></output>
    <output id="rating-star4" data-kit-text="isFilled(4) ? 'on' : 'off'"></output>
  </section>

  <section data-kit-component="tags@1.0.0" data-kit-scope="tags: ['alpha'], draft: '', max: 3">
    <input id="tags-input" data-kit-model="draft">
    <button id="tags-add" data-kit-click="add(draft)">add</button>
    <button id="tags-remove" data-kit-click="remove('alpha')">remove alpha</button>
    <output id="tags-count" data-kit-text="tags.length"></output>
    <output id="tags-has" data-kit-text="has('beta') ? 'yes' : 'no'"></output>
  </section>

  <section data-kit-component="collapse@1.0.0">
    <button id="collapse-toggle" data-kit-click="toggle()" data-kit-bind:aria-expanded="open">toggle</button>
    <button id="collapse-disable" data-kit-click="disabled = true">disable</button>
    <div id="collapse-panel" data-kit-show="open" hidden>panel</div>
  </section>

  <section data-kit-component="combobox@1.0.0" data-kit-scope="options: ['Apple', 'Banana', 'Cherry'], query: '', open: false, activeIndex: -1, selected: ''">
    <input id="combobox-input" data-kit-model="query" data-kit-input="search()">
    <button id="combobox-next" data-kit-click="next()">next</button>
    <button id="combobox-choose" data-kit-click="chooseActive()">choose</button>
    <output id="combobox-count" data-kit-text="filtered().length"></output>
    <output id="combobox-active" data-kit-text="activeIndex"></output>
    <output id="combobox-selected" data-kit-text="selected"></output>
  </section>

  <script src="/suite-v3.js"></script><script>
%s
__runStandaloneKitTest(async function () {
  var waitFor = __kitTestWaitFor;
  var nextTurn = __kitTestNextTurn;
  var assert = __kitTestAssert;
  function click(id) { document.getElementById(id).click(); }
  function fill(id, value) { var el = document.getElementById(id); el.value = value; el.dispatchEvent(new Event("input", { bubbles: true })); }
  function text(id) { return document.getElementById(id).textContent.trim(); }

  await waitFor(function () { return text("stepper-value") === "5"; }, "stepper did not seed value");
  click("stepper-inc");
  await waitFor(function () { return text("stepper-value") === "7"; }, "stepper increment failed");
  click("stepper-inc"); click("stepper-inc");
  await waitFor(function () { return text("stepper-value") === "10" && text("stepper-can") === "no"; }, "stepper did not clamp at max");
  click("stepper-dec");
  await waitFor(function () { return text("stepper-value") === "8"; }, "stepper decrement failed");
  click("stepper-disable"); click("stepper-inc");
  await nextTurn();
  assert(text("stepper-value") === "8", "disabled stepper mutated");

  await waitFor(function () { return text("slider-value") === "20" && text("slider-pct") === "20"; }, "slider did not seed value/percent");
  click("slider-inc");
  await waitFor(function () { return text("slider-value") === "25" && text("slider-pct") === "25"; }, "slider increment/percent failed");
  click("slider-page");
  await waitFor(function () { return text("slider-value") === "50"; }, "slider pageUp failed");
  click("slider-end");
  await waitFor(function () { return text("slider-value") === "100" && text("slider-pct") === "100"; }, "slider toEnd/percent failed");

  await waitFor(function () { return text("rating-value") === "0"; }, "rating did not seed zero");
  click("rating-3");
  await waitFor(function () { return text("rating-value") === "3" && text("rating-star3") === "on" && text("rating-star4") === "off"; }, "rating rate/fill failed");
  click("rating-9");
  await waitFor(function () { return text("rating-value") === "5"; }, "rating did not clamp to max");
  click("rating-clear");
  await waitFor(function () { return text("rating-value") === "0" && text("rating-star3") === "off"; }, "rating clear failed");

  await waitFor(function () { return text("tags-count") === "1"; }, "tags did not seed");
  fill("tags-input", "beta"); click("tags-add");
  await waitFor(function () { return text("tags-count") === "2" && text("tags-has") === "yes"; }, "tags add failed");
  fill("tags-input", "beta"); click("tags-add");
  await nextTurn();
  assert(text("tags-count") === "2", "tags accepted a duplicate");
  click("tags-remove");
  await waitFor(function () { return text("tags-count") === "1"; }, "tags remove failed");

  await waitFor(function () { return document.getElementById("collapse-panel").hidden && document.getElementById("collapse-toggle").getAttribute("aria-expanded") === "false"; }, "collapse did not start closed");
  click("collapse-toggle");
  await waitFor(function () { return !document.getElementById("collapse-panel").hidden && document.getElementById("collapse-toggle").getAttribute("aria-expanded") === "true"; }, "collapse open failed");
  click("collapse-disable"); click("collapse-toggle");
  await nextTurn();
  assert(!document.getElementById("collapse-panel").hidden, "disabled collapse hid the panel");

  await waitFor(function () { return text("combobox-count") === "3"; }, "combobox did not seed options");
  fill("combobox-input", "an");
  await waitFor(function () { return text("combobox-count") === "1"; }, "combobox query did not filter");
  click("combobox-next");
  await waitFor(function () { return text("combobox-active") === "0"; }, "combobox next did not activate first match");
  click("combobox-choose");
  await waitFor(function () { return text("combobox-selected") === "Banana"; }, "combobox chooseActive failed");
});
  </script>
</body></html>`, browserHarness)
