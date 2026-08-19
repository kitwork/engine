package javascript

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The second KitJS UI suite remains state-only. Authored HTML owns semantic
// elements, ARIA, focus movement, dismissal policies, and visual transitions.
var componentSuiteV2Contracts = []struct {
	name    string
	fields  []string
	methods []string
}{
	{name: "alert", fields: []string{"visible", "message", "tone"}, methods: []string{"show", "dismiss", "isTone"}},
	{name: "switch", fields: []string{"checked", "disabled"}, methods: []string{"toggle", "on", "off", "set"}},
	{name: "pagination", fields: []string{"page", "pages"}, methods: []string{"select", "next", "previous", "first", "last", "canPrevious", "canNext"}},
	{name: "carousel", fields: []string{"slides", "active"}, methods: []string{"select", "next", "previous", "first", "last", "isActive"}},
	{name: "popover", fields: []string{"open", "placement"}, methods: []string{"show", "hide", "toggle", "place"}},
	{name: "tooltip", fields: []string{"open", "content"}, methods: []string{"show", "hide", "toggle"}},
	{name: "toast", fields: []string{"visible", "message", "tone"}, methods: []string{"show", "dismiss", "isTone"}},
	{name: "drawer", fields: []string{"open", "side"}, methods: []string{"show", "hide", "toggle", "place"}},
}

func TestComponentSuiteV2SourcesAreClosedStatePackages(t *testing.T) {
	for _, contract := range componentSuiteV2Contracts {
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
				[]byte("kit.service("), []byte("document."), []byte("window."),
				[]byte("addEventListener("), []byte("removeEventListener("),
				[]byte("setTimeout("), []byte("setInterval("), []byte("WeakMap"),
				[]byte("$host"), []byte("$refs"), []byte("__kitwork_core__"), []byte("init:"),
			} {
				if bytes.Contains(source, forbidden) {
					t.Fatalf("state package contains forbidden runtime ownership %q", forbidden)
				}
			}
		})
	}
}

func TestComponentSuiteV2CatalogDefaultsAreDependencyFree(t *testing.T) {
	catalog, err := loadDeliveryCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, contract := range componentSuiteV2Contracts {
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
	if _, err := catalog.component("toast", "2.0.0"); !errors.Is(err, ErrModuleNotFound) {
		t.Fatalf("unknown suite version error = %v", err)
	}
}

func TestComponentSuiteV2ArtifactsContainOnlySelectedPackage(t *testing.T) {
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	for _, selected := range componentSuiteV2Contracts {
		bundle, err := composer.ComposeStandalone([]ComponentRef{{Name: selected.name}}, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, candidate := range componentSuiteV2Contracts {
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

func TestComponentSuiteV2ExactHTMLScanMatchesExplicitSelection(t *testing.T) {
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	for _, contract := range componentSuiteV2Contracts {
		html := []byte(`<main data-kit-component="` + contract.name + `" data-kit-version="1.0.0"></main>`)
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

func TestBrowserComponentSuiteV2StateAndDirectiveContract(t *testing.T) {
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
	references := make([]ComponentRef, 0, len(componentSuiteV2Contracts))
	for _, contract := range componentSuiteV2Contracts {
		references = append(references, ComponentRef{Name: contract.name, Version: "1.0.0"})
	}
	bundle, err := composer.ComposeStandalone(references, false)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/suite-v2.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(bundle.JavaScript)
		case "/suite-v2.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(componentSuiteV2Document))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/suite-v2.html")
}

var componentSuiteV2Document = fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>KitJS component suite v2</title></head><body>
  <section data-kit-component="alert" data-kit-version="1.0.0">
    <button id="alert-show" data-kit-click="show('Saved', 'success')">Show alert</button>
    <button id="alert-invalid" data-kit-click="show('Still saved', 'loud')">Invalid tone</button>
    <button id="alert-dismiss" data-kit-click="dismiss()">Dismiss</button>
    <div id="alert-panel" role="status" data-kit-show="visible" hidden><span id="alert-message" data-kit-text="message"></span></div>
    <output id="alert-tone" data-kit-text="tone"></output>
  </section>

  <section data-kit-component="switch" data-kit-version="1.0.0">
    <button id="switch-toggle" role="switch" data-kit-click="toggle()" data-kit-bind="'aria-checked': checked;">Switch</button>
    <button id="switch-off" data-kit-click="off()">Off</button>
    <button id="switch-disable" data-kit-click="disabled = true">Disable</button>
    <output id="switch-state" data-kit-text="checked ? 'on' : 'off'"></output>
  </section>

  <section data-kit-component="pagination" data-kit-version="1.0.0" data-kit-scope="page: 1; pages: 3">
    <button id="page-next" data-kit-click="next()">Next</button>
    <button id="page-last" data-kit-click="last()">Last</button>
    <button id="page-bad" data-kit-click="select('bad')">Bad</button>
    <button id="page-first" data-kit-click="first()">First</button>
    <output id="page-state" data-kit-text="page"></output>
    <output id="page-can-next" data-kit-text="canNext() ? 'yes' : 'no'"></output>
  </section>

  <section data-kit-component="carousel" data-kit-version="1.0.0" data-kit-scope="slides: ['one', 'two', 'three']; active: 0">
    <button id="carousel-next" data-kit-click="next()">Next</button>
    <button id="carousel-previous" data-kit-click="previous()">Previous</button>
    <button id="carousel-last" data-kit-click="last()">Last</button>
    <button id="carousel-bad" data-kit-click="select(9)">Bad</button>
    <div id="carousel-three" data-kit-show="isActive(2)" hidden>Three</div>
    <output id="carousel-state" data-kit-text="active"></output>
  </section>

  <section data-kit-component="popover" data-kit-version="1.0.0">
    <button id="popover-toggle" data-kit-click="toggle()" data-kit-bind="'aria-expanded': open;">Popover</button>
    <button id="popover-top" data-kit-click="place('top')">Top</button>
    <button id="popover-bad" data-kit-click="place('center')">Bad placement</button>
    <div id="popover-panel" data-kit-show="open" hidden>Popover panel</div>
    <output id="popover-placement" data-kit-text="placement"></output>
  </section>

  <section data-kit-component="tooltip" data-kit-version="1.0.0">
    <button id="tooltip-show" data-kit-click="show('Copied')">Show tooltip</button>
    <button id="tooltip-toggle" data-kit-click="toggle()">Toggle tooltip</button>
    <div id="tooltip-panel" role="tooltip" data-kit-show="open" data-kit-text="content" hidden></div>
  </section>

  <section data-kit-component="toast" data-kit-version="1.0.0">
    <button id="toast-show" data-kit-click="show('Uploaded', 'warning')">Show toast</button>
    <button id="toast-dismiss" data-kit-click="dismiss()">Dismiss toast</button>
    <div id="toast-panel" role="status" data-kit-show="visible" hidden><span id="toast-message" data-kit-text="message"></span></div>
    <output id="toast-tone" data-kit-text="tone"></output>
  </section>

  <section data-kit-component="drawer" data-kit-version="1.0.0">
    <button id="drawer-show" data-kit-click="show()">Show drawer</button>
    <button id="drawer-left" data-kit-click="place('left')">Left</button>
    <button id="drawer-bad" data-kit-click="place('middle')">Bad side</button>
    <button id="drawer-hide" data-kit-click="hide()">Hide drawer</button>
    <aside id="drawer-panel" data-kit-show="open" hidden>Drawer</aside>
    <output id="drawer-side" data-kit-text="side"></output>
  </section>

  <script src="/suite-v2.js"></script><script>
%s
__runStandaloneKitTest(async function () {
  var waitFor = __kitTestWaitFor;
  function click(id) { document.getElementById(id).click(); }

  await waitFor(function () {
    return document.getElementById("switch-state").textContent === "off" &&
      document.getElementById("page-state").textContent === "1" &&
      document.getElementById("carousel-state").textContent === "0";
  }, "suite v2 did not mount seeded state");

  click("alert-show");
  await waitFor(function () { return document.getElementById("alert-message").textContent === "Saved" && document.getElementById("alert-tone").textContent === "success" && !document.getElementById("alert-panel").hidden; }, "alert show failed");
  click("alert-invalid");
  await waitFor(function () { return document.getElementById("alert-message").textContent === "Still saved" && document.getElementById("alert-tone").textContent === "success"; }, "alert invalid tone did not preserve state");
  click("alert-dismiss");
  await waitFor(function () { return document.getElementById("alert-panel").hidden; }, "alert dismiss failed");

  click("switch-toggle");
  await waitFor(function () { return document.getElementById("switch-state").textContent === "on" && document.getElementById("switch-toggle").getAttribute("aria-checked") === "true"; }, "switch toggle failed");
  click("switch-disable"); click("switch-off");
  await waitFor(function () { return document.getElementById("switch-state").textContent === "on"; }, "disabled switch mutated");

  click("page-next");
  await waitFor(function () { return document.getElementById("page-state").textContent === "2"; }, "pagination next failed");
  click("page-last"); click("page-bad");
  await waitFor(function () { return document.getElementById("page-state").textContent === "3" && document.getElementById("page-can-next").textContent === "no"; }, "pagination last/invalid selection failed");
  click("page-first");
  await waitFor(function () { return document.getElementById("page-state").textContent === "1"; }, "pagination first failed");

  click("carousel-previous");
  await waitFor(function () { return document.getElementById("carousel-state").textContent === "2" && !document.getElementById("carousel-three").hidden; }, "carousel previous did not wrap");
  click("carousel-next"); click("carousel-last"); click("carousel-bad");
  await waitFor(function () { return document.getElementById("carousel-state").textContent === "2"; }, "carousel navigation/invalid selection failed");

  click("popover-toggle"); click("popover-top"); click("popover-bad");
  await waitFor(function () { return !document.getElementById("popover-panel").hidden && document.getElementById("popover-placement").textContent === "top"; }, "popover state/placement failed");

  click("tooltip-show");
  await waitFor(function () { return !document.getElementById("tooltip-panel").hidden && document.getElementById("tooltip-panel").textContent === "Copied"; }, "tooltip show failed");
  click("tooltip-toggle");
  await waitFor(function () { return document.getElementById("tooltip-panel").hidden; }, "tooltip toggle failed");

  click("toast-show");
  await waitFor(function () { return !document.getElementById("toast-panel").hidden && document.getElementById("toast-message").textContent === "Uploaded" && document.getElementById("toast-tone").textContent === "warning"; }, "toast show failed");
  click("toast-dismiss");
  await waitFor(function () { return document.getElementById("toast-panel").hidden; }, "toast dismiss failed");

  click("drawer-show"); click("drawer-left"); click("drawer-bad");
  await waitFor(function () { return !document.getElementById("drawer-panel").hidden && document.getElementById("drawer-side").textContent === "left"; }, "drawer state/side failed");
  click("drawer-hide");
  await waitFor(function () { return document.getElementById("drawer-panel").hidden; }, "drawer hide failed");
});
  </script>
</body></html>`, browserHarness)
