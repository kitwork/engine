package javascript

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The first KitJS UI suite is deliberately state-only. These are its complete
// public contracts; semantics live in authored bind/show/class/action markup.
//
// accordion@1.0.0
//
//	fields:  multiple, openItems
//	methods: toggle, expand, collapse, collapseAll, isOpen
//
// dialog@1.0.0
//
//	fields:  open, returnValue
//	methods: show, close, toggle
//
// tabs@1.0.0
//
//	fields:  tabs, active
//	methods: select, next, previous, first, last, isActive
//
// dropdown@1.0.0
//
//	fields:  open, items, activeIndex, selected
//	methods: show, hide, toggle, next, previous, choose, isActive
var componentSuiteContracts = []struct {
	name    string
	fields  []string
	methods []string
}{
	{name: "accordion", fields: []string{"multiple", "openItems"}, methods: []string{"toggle", "expand", "collapse", "collapseAll", "isOpen"}},
	{name: "dialog", fields: []string{"open", "returnValue"}, methods: []string{"show", "close", "toggle"}},
	{name: "tabs", fields: []string{"tabs", "active"}, methods: []string{"select", "next", "previous", "first", "last", "isActive"}},
	{name: "dropdown", fields: []string{"open", "items", "activeIndex", "selected"}, methods: []string{"show", "hide", "toggle", "next", "previous", "choose", "isActive"}},
}

func TestComponentSuiteSourcesAreClosedStatePackages(t *testing.T) {
	for _, contract := range componentSuiteContracts {
		contract := contract
		t.Run(contract.name, func(t *testing.T) {
			source := readVanillaFile(t, "component", contract.name, "1.0.0.js")
			registration := []byte(`kit.component("` + contract.name + `"`)
			if len(source) == 0 || source[0] != ';' || source[len(source)-1] != '\n' {
				t.Fatal("component is not a sealable classic script")
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
				[]byte("kit.service("), []byte("document."), []byte("addEventListener("),
				[]byte("removeEventListener("), []byte("WeakMap"), []byte("$host"),
				[]byte("$refs"), []byte("__kitwork_core__"), []byte("init:"),
			} {
				if bytes.Contains(source, forbidden) {
					t.Fatalf("state package contains forbidden runtime ownership %q", forbidden)
				}
			}
		})
	}
}

func TestComponentSuiteCatalogKeepsSimpleV1Defaults(t *testing.T) {
	catalog, err := loadDeliveryCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, contract := range componentSuiteContracts {
		legacy, err := catalog.component(contract.name, "1.0.0")
		if err != nil {
			t.Fatal(err)
		}
		if legacy.identity != (ComponentVersion{Name: contract.name, Version: "1.0.0"}) {
			t.Fatalf("%s v1 identity = %#v", contract.name, legacy.identity)
		}
		if len(legacy.requires) != 0 {
			t.Fatalf("%s v1 dependencies = %#v, want none", contract.name, legacy.requires)
		}
	}
	for _, name := range []string{"dialog", "dropdown", "tabs"} {
		component, err := catalog.component(name, "")
		if err != nil {
			t.Fatal(err)
		}
		if component.identity != (ComponentVersion{Name: name, Version: "1.0.0"}) {
			t.Fatalf("%s default identity = %#v", name, component.identity)
		}
		if len(component.requires) != 0 {
			t.Fatalf("%s default dependencies = %#v, want none", name, component.requires)
		}
		enhanced, err := catalog.component(name, "2.0.0")
		if err != nil {
			t.Fatal(err)
		}
		if enhanced.identity != (ComponentVersion{Name: name, Version: "2.0.0"}) {
			t.Fatalf("%s enhanced identity = %#v", name, enhanced.identity)
		}
	}
	accordion, err := catalog.component("accordion", "")
	if err != nil || accordion.identity.Version != "1.0.0" {
		t.Fatalf("accordion default = %#v, %v", accordion.identity, err)
	}
	if _, err := catalog.component("dialog", "3.0.0"); !errors.Is(err, ErrModuleNotFound) {
		t.Fatalf("unknown suite version error = %v", err)
	}
	composer := &Composer{catalog: catalog}
	_, err = composer.ComposeHTML([]byte(
		`<main data-kit-component="dialog" data-kit-version="3.0.0"></main>`,
	))
	if !errors.Is(err, ErrModuleNotFound) {
		t.Fatalf("authored unknown suite version error = %v", err)
	}
}

func TestComponentSuiteArtifactsContainOnlySelectedPackage(t *testing.T) {
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	for _, selected := range componentSuiteContracts {
		bundle, err := composer.ComposeStandalone([]ComponentRef{{Name: selected.name}}, false)
		if err != nil {
			t.Fatal(err)
		}
		if bundle.Empty() || bundle.Profile != ProfileKit {
			t.Fatalf("%s artifact = empty:%v profile:%q", selected.name, bundle.Empty(), bundle.Profile)
		}
		for _, candidate := range componentSuiteContracts {
			count := bytes.Count(bundle.JavaScript, []byte(`kit.component("`+candidate.name+`"`))
			want := 0
			if candidate.name == selected.name {
				want = 1
			}
			if count != want {
				t.Fatalf("%s-only artifact registration count for %s = %d, want %d", selected.name, candidate.name, count, want)
			}
		}
		if bytes.Contains(bundle.JavaScript, []byte(`kit.service("`)) {
			t.Fatalf("%s-only artifact unexpectedly sealed a service", selected.name)
		}
	}
}

func TestComponentSuiteHTMLScanMatchesExplicitSelection(t *testing.T) {
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	for _, contract := range componentSuiteContracts {
		html := []byte(`<main data-kit-component="` + contract.name +
			`" data-kit-version="1.0.0"></main>`)
		use, err := ScanHTML(html)
		if err != nil {
			t.Fatal(err)
		}
		if !use.NeedsRuntime || len(use.Components) != 1 ||
			use.Components[0].Name != contract.name || use.Components[0].Version != "1.0.0" {
			t.Fatalf("%s scan = %#v", contract.name, use)
		}
		fromHTML, err := composer.ComposeHTML(html)
		if err != nil {
			t.Fatal(err)
		}
		explicit, err := composer.ComposeStandalone([]ComponentRef{{
			Name: contract.name, Version: "1.0.0",
		}}, false)
		if err != nil {
			t.Fatal(err)
		}
		if fromHTML.ContentHash != explicit.ContentHash || !bytes.Equal(fromHTML.JavaScript, explicit.JavaScript) {
			t.Fatalf("%s scanned and explicit selections produced different artifacts", contract.name)
		}
	}
}

func TestBrowserComponentSuiteStateAndDirectiveContract(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping component-suite browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	references := make([]ComponentRef, 0, len(componentSuiteContracts))
	for _, contract := range componentSuiteContracts {
		references = append(references, ComponentRef{Name: contract.name, Version: "1.0.0"})
	}
	bundle, err := composer.ComposeStandalone(references, false)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/suite.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(bundle.JavaScript)
		case "/suite.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(componentSuiteDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/suite.html")
}

var componentSuiteDocument = fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>KitJS component suite</title></head><body>
  <section id="accordion" data-kit-component="accordion" data-kit-version="1.0.0"
    data-kit-scope="multiple: true; openItems: ['first']">
    <button id="accordion-first" type="button" data-kit-click="toggle('first')"
      data-kit-bind="'aria-expanded': isOpen('first');">First</button>
    <div id="accordion-panel" data-kit-show="isOpen('first')">First panel</div>
    <button id="accordion-second" type="button" data-kit-click="toggle('second')">Second</button>
    <button id="accordion-collapse" type="button" data-kit-click="collapseAll()">Collapse</button>
    <output id="accordion-state" data-kit-text="(isOpen('first') ? 'first' : '') + (isOpen('second') ? 'second' : '')">server</output>
  </section>

  <section id="dialog" data-kit-component="dialog" data-kit-version="1.0.0">
    <button id="dialog-show" type="button" data-kit-click="show()">Show</button>
    <div id="dialog-panel" role="dialog" aria-modal="true" aria-labelledby="dialog-title"
      data-kit-show="open" data-kit-keydown:escape="close('escape')" hidden>
      <h2 id="dialog-title">Dialog</h2>
      <button id="dialog-close" type="button" data-kit-click="close('accepted')">Close</button>
    </div>
    <output id="dialog-open" data-kit-text="open ? 'open' : 'closed'">server</output>
    <output id="dialog-result" data-kit-text="returnValue">server</output>
  </section>

  <section id="tabs" data-kit-component="tabs" data-kit-version="1.0.0"
    data-kit-scope="tabs: ['overview', 'api', 'examples']; active: 'overview'">
    <div role="tablist">
      <button id="tab-overview" type="button" role="tab" data-kit-click="select('overview')"
        data-kit-keydown="$event.key === 'ArrowRight' ? next() : $event.key === 'ArrowLeft' ? previous() : active"
        data-kit-bind="'aria-selected': isActive('overview'); tabindex: isActive('overview') ? 0 : -1;">Overview</button>
      <button id="tab-api" type="button" role="tab" data-kit-click="select('api')"
        data-kit-bind="'aria-selected': isActive('api'); tabindex: isActive('api') ? 0 : -1;">API</button>
    </div>
    <div id="tab-overview-panel" role="tabpanel" data-kit-show="isActive('overview')">Overview panel</div>
    <div id="tab-api-panel" role="tabpanel" data-kit-show="isActive('api')">API panel</div>
    <button id="tabs-first" type="button" data-kit-click="first()">First</button>
    <button id="tabs-last" type="button" data-kit-click="last()">Last</button>
    <output id="tabs-active" data-kit-text="active">server</output>
  </section>

  <section id="dropdown" data-kit-component="dropdown" data-kit-version="1.0.0"
    data-kit-scope="items: ['profile', 'settings', 'sign-out']" data-kit-click:outside="hide()">
    <button id="dropdown-trigger" type="button" data-kit-click="toggle()"
      data-kit-keydown="$event.key === 'ArrowDown' ? next() : $event.key === 'ArrowUp' ? previous() : activeIndex"
      data-kit-keydown:escape="hide()" data-kit-bind="'aria-expanded': open;">Menu</button>
    <div id="dropdown-menu" role="menu" data-kit-show="open" data-kit-class="open ? 'visible' : 'invisible'" hidden>
      <button id="dropdown-settings" type="button" role="menuitem" data-kit-click="choose('settings')">Settings</button>
    </div>
    <output id="dropdown-index" data-kit-text="activeIndex">server</output>
    <output id="dropdown-selected" data-kit-text="selected">server</output>
  </section>

  <script src="/suite.js"></script>
  <script>
%s
__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;

  await waitFor(function () {
    return document.getElementById("accordion-state").textContent === "first" &&
      document.getElementById("dialog-open").textContent === "closed" &&
      document.getElementById("tabs-active").textContent === "overview" &&
      document.getElementById("dropdown-index").textContent === "-1";
  }, "component suite did not mount seeded state");

  document.getElementById("accordion-second").click();
  await waitFor(function () { return document.getElementById("accordion-state").textContent === "firstsecond"; },
    "multiple accordion did not expand independently");
  document.getElementById("accordion-first").click();
  await waitFor(function () {
    return document.getElementById("accordion-state").textContent === "second" &&
      document.getElementById("accordion-first").getAttribute("aria-expanded") === "false" &&
      document.getElementById("accordion-panel").hidden;
  }, "accordion bindings did not follow state");
  document.getElementById("accordion-collapse").click();
  await waitFor(function () { return document.getElementById("accordion-state").textContent === ""; },
    "accordion collapseAll did not close every item");

  document.getElementById("dialog-show").click();
  await waitFor(function () { return document.getElementById("dialog-open").textContent === "open" && !document.getElementById("dialog-panel").hidden; },
    "dialog did not open");
  document.getElementById("dialog-panel").dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
  await waitFor(function () { return document.getElementById("dialog-result").textContent === "escape" && document.getElementById("dialog-panel").hidden; },
    "dialog Escape action did not close with a return value");
  document.getElementById("dialog-show").click();
  document.getElementById("dialog-close").click();
  await waitFor(function () { return document.getElementById("dialog-result").textContent === "accepted"; },
    "dialog close action did not retain its result");

  // This proves authored key events can drive state/ARIA/visibility. Component
  // packages intentionally own no DOM references, so it makes no focus-move claim.
  document.getElementById("tab-overview").dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true, cancelable: true }));
  await waitFor(function () {
    return document.getElementById("tabs-active").textContent === "api" &&
      document.getElementById("tab-api").getAttribute("aria-selected") === "true" &&
      document.getElementById("tab-overview-panel").hidden && !document.getElementById("tab-api-panel").hidden;
  }, "tabs event-driven state/ARIA/panel bindings did not agree");
  document.getElementById("tabs-last").click();
  await waitFor(function () { return document.getElementById("tabs-active").textContent === "examples"; }, "tabs last failed");
  document.getElementById("tabs-first").click();
  await waitFor(function () { return document.getElementById("tabs-active").textContent === "overview"; }, "tabs first failed");

  var trigger = document.getElementById("dropdown-trigger");
  trigger.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true, cancelable: true }));
  await waitFor(function () {
    return trigger.getAttribute("aria-expanded") === "true" &&
      document.getElementById("dropdown-index").textContent === "0" &&
      document.getElementById("dropdown-menu").classList.contains("visible");
  }, "dropdown next did not open and activate the first item");
  document.getElementById("dropdown-settings").click();
  await waitFor(function () {
    return document.getElementById("dropdown-selected").textContent === "settings" &&
      document.getElementById("dropdown-index").textContent === "-1" && document.getElementById("dropdown-menu").hidden;
  }, "dropdown choose did not select and close");
  trigger.click();
  await waitFor(function () { return trigger.getAttribute("aria-expanded") === "true"; }, "dropdown did not reopen");
  document.body.click();
  await waitFor(function () { return trigger.getAttribute("aria-expanded") === "false"; }, "dropdown outside action did not close");
});
  </script>
</body></html>`, browserHarness)
