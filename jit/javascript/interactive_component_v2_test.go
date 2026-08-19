package javascript

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

var interactiveComponentV2Names = []string{"dialog", "dropdown", "tabs"}

func TestInteractiveComponentV2SourcesAndExactGraph(t *testing.T) {
	catalog, err := loadDeliveryCatalog()
	if err != nil {
		t.Fatal(err)
	}
	composer := &Composer{catalog: catalog}
	for _, name := range interactiveComponentV2Names {
		source := readVanillaFile(t, "component", name, "2.0.0.js")
		if len(source) == 0 || source[0] != ';' || source[len(source)-1] != '\n' {
			t.Fatalf("%s@2 is not a sealable classic script", name)
		}
		if got := bytes.Count(source, []byte(`kit.component("`+name+`"`)); got != 1 {
			t.Fatalf("%s@2 registration count = %d", name, got)
		}
		for _, forbidden := range [][]byte{
			[]byte("document.querySelector"), []byte("window."), []byte("kit.service("),
			[]byte("createElement("), []byte("innerHTML"), []byte("<dialog"),
		} {
			if bytes.Contains(source, forbidden) {
				t.Fatalf("%s@2 contains forbidden ownership %q", name, forbidden)
			}
		}
		component, err := catalog.component(name, "2.0.0")
		if err != nil {
			t.Fatal(err)
		}
		if component.identity != (ComponentVersion{Name: name, Version: "2.0.0"}) || len(component.requires) != 0 {
			t.Fatalf("%s enhanced = %#v requires %#v", name, component.identity, component.requires)
		}
		simple, err := catalog.component(name, "")
		if err != nil {
			t.Fatal(err)
		}
		if simple.identity != (ComponentVersion{Name: name, Version: "1.0.0"}) || len(simple.requires) != 0 {
			t.Fatalf("%s simple default = %#v requires %#v", name, simple.identity, simple.requires)
		}
		authoredV1, err := composer.ComposeHTML([]byte(`<main data-kit-component="` + name + `@1.0.0"></main>`))
		if err != nil {
			t.Fatal(err)
		}
		explicitV1, err := composer.ComposeStandalone([]ComponentRef{{Name: name, Version: "1.0.0"}}, false)
		if err != nil {
			t.Fatal(err)
		}
		if authoredV1.ContentHash != explicitV1.ContentHash ||
			!bytes.Equal(authoredV1.JavaScript, explicitV1.JavaScript) {
			t.Fatalf("%s canonical v1 pin did not select the simple v1 package", name)
		}
		if !bytes.Contains(authoredV1.JavaScript,
			[]byte(`components["`+name+`"] = "1.0.0"`)) {
			t.Fatalf("%s simple artifact lost its exact v1 manifest", name)
		}
		fromHTML, err := composer.ComposeHTML([]byte(`<main data-kit-component="` + name + `" data-kit-version="2.0.0"></main>`))
		if err != nil {
			t.Fatal(err)
		}
		explicit, err := composer.ComposeStandalone([]ComponentRef{{Name: name, Version: "2.0.0"}}, false)
		if err != nil {
			t.Fatal(err)
		}
		if fromHTML.ContentHash != explicit.ContentHash || !bytes.Equal(fromHTML.JavaScript, explicit.JavaScript) {
			t.Fatalf("%s exact authored pin did not select v2", name)
		}
		if fromHTML.ContentHash == authoredV1.ContentHash {
			t.Fatalf("%s pinned v1 and enhanced v2 unexpectedly share an artifact", name)
		}
		if !bytes.Contains(fromHTML.JavaScript,
			[]byte(`components["`+name+`"] = "2.0.0"`)) {
			t.Fatalf("%s enhanced artifact lost its exact v2 manifest", name)
		}
		for _, candidate := range interactiveComponentV2Names {
			want := 0
			if candidate == name {
				want = 1
			}
			if got := bytes.Count(fromHTML.JavaScript, []byte(`kit.component("`+candidate+`"`)); got != want {
				t.Fatalf("%s artifact contains %d %s registrations, want %d", name, got, candidate, want)
			}
		}
	}
}

func TestInteractiveComponentV1BytesRemainFrozen(t *testing.T) {
	want := map[string]string{
		"dialog":   "e21ade945e8a74794cabd557c308b31d4763f4848bbbcdb38847ccd705154711",
		"dropdown": "c889e8aa5e37a2efc4308249293dc4ce820a8e27353e431c75ccdd25165a07e4",
		"tabs":     "8b32a4f9830eb3e5cdb843950d1621e5b683f980fee83f7672f14a4af1ee6763",
	}
	for name, digest := range want {
		if got := ContentHash(readVanillaFile(t, "component", name, "1.0.0.js")); got != digest {
			t.Fatalf("%s@1 bytes changed: %s", name, got)
		}
	}
}

func TestBrowserInteractiveComponentV2APGAndCleanup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping interactive component v2 browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeStandalone([]ComponentRef{
		{Name: "dialog", Version: "2.0.0"},
		{Name: "dropdown", Version: "2.0.0"},
		{Name: "tabs", Version: "2.0.0"},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/interactive-v2.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(bundle.JavaScript)
		case "/interactive-v2.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(interactiveComponentV2Document))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/interactive-v2.html")
}

func TestBrowserInteractiveComponentV2HydrateMorphCleanup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping interactive component v2 Hydrate cleanup in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeStandalone([]ComponentRef{{Name: "dialog", Version: "2.0.0"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	contractSource := []byte(interactiveComponentV2HydrateContractSource)
	scriptTags := interactiveComponentV2HydrateScriptTags(
		driveScriptIntegrity(bundle.JavaScript),
		driveScriptIntegrity(contractSource),
	)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/hydrate-v2.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(bundle.JavaScript)
		case "/hydrate-v2-contract.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(contractSource)
		case "/hydrate-v2.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(interactiveComponentV2HydrateDocument(scriptTags)))
		case "/next":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(interactiveComponentV2HydrateNextDocument(scriptTags)))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/hydrate-v2.html")
}

func interactiveComponentV2HydrateScriptTags(bundleIntegrity, contractIntegrity string) string {
	return fmt.Sprintf(`<script defer src="/hydrate-v2.js" integrity="%s" crossorigin="anonymous"></script>
<script defer src="/hydrate-v2-contract.js" integrity="%s" crossorigin="anonymous"></script>`,
		bundleIntegrity, contractIntegrity)
}

func interactiveComponentV2HydrateDocument(scriptTags string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>hydrate dialog</title>%s</head><body>
  <main id="route">
    <section id="morph-dialog" data-kit-component="dialog" data-kit-version="2.0.0">
      <button id="morph-open" type="button" data-dialog-trigger>Open</button>
      <div id="morph-panel" data-dialog-panel role="dialog" aria-modal="true" hidden><button data-dialog-close>Close</button></div>
    </section>
    <a id="next" href="/next">Next</a>
  </main>
  <aside id="morph-sibling">Sibling</aside>
</body></html>`, scriptTags)
}

func interactiveComponentV2HydrateNextDocument(scriptTags string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>next</title>%s</head><body>
  <main id="next-content">Next</main><aside id="morph-sibling">Sibling</aside>
</body></html>`, scriptTags)
}

const interactiveComponentV2HydrateContractSource = browserHarness + `
__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  document.getElementById("morph-open").click();
  await waitFor(function () { return !document.getElementById("morph-panel").hidden && document.getElementById("morph-sibling").hasAttribute("inert"); }, "dialog did not lock before Drive morph");
  document.getElementById("next").click();
  await waitFor(function () { return location.pathname === "/next" && document.getElementById("next-content"); }, "Drive did not commit next page");
  assert(!document.documentElement.style.overflow, "Drive disposal leaked dialog scroll lock");
  assert(!document.getElementById("morph-sibling").hasAttribute("inert") && !document.getElementById("morph-sibling").hasAttribute("aria-hidden"), "Drive disposal leaked dialog background state");
});`

var interactiveComponentV2Document = fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>interactive v2</title></head><body>
  <main id="background">
    <section id="outer" data-kit-component="dialog" data-kit-version="2.0.0" data-kit-as="$outer">
      <button id="outer-trigger" type="button" data-dialog-trigger>Open outer</button>
      <div id="outer-panel" data-dialog-panel role="dialog" aria-modal="true" hidden>
        <input id="outer-hidden-input" type="hidden" data-dialog-initial-focus>
        <div hidden><button id="outer-hidden-button" type="button" data-dialog-initial-focus>Hidden</button></div>
        <button id="outer-css-hidden" type="button" style="display:none" data-dialog-initial-focus>CSS hidden</button>
        <button id="outer-first" type="button" data-dialog-initial-focus>First</button>
        <button id="outer-second" type="button">Second</button>
        <button id="outer-close" type="button" data-dialog-close data-dialog-value="accepted">Close</button>
        <button id="reactive-close-outer" type="button" data-kit-click="open = false">Close parent state</button>
        <section id="inner" data-kit-component="dialog" data-kit-version="2.0.0">
          <button id="inner-trigger" type="button" data-dialog-trigger>Open inner</button>
          <div id="inner-panel" data-dialog-panel role="dialog" aria-modal="true" hidden>
            <button id="inner-cancel" type="button" data-dialog-cancel data-dialog-value="button-cancel" data-dialog-initial-focus>Cancel</button>
            <a id="inner-link" href="#kept">Kept link</a>
          </div>
          <output id="inner-result" data-kit-text="cancelled ? returnValue : ''"></output>
        </section>
      </div>
    </section>
  </main>

  <section id="swap-dialog" data-kit-component="dialog" data-kit-version="2.0.0">
    <button id="swap-trigger" type="button" data-dialog-trigger>Open swap dialog</button>
    <button id="swap-refresh" type="button" hidden data-kit-click="returnValue = returnValue === 'refresh-a' ? 'refresh-b' : 'refresh-a'">Refresh</button>
    <div id="swap-path-a">
      <div id="swap-panel" data-dialog-panel role="dialog" aria-modal="true" hidden>
        <button id="swap-old-focus" type="button" data-dialog-initial-focus>Old panel</button>
      </div>
    </div>
    <div id="swap-path-b"></div>
    <template id="swap-template">
      <div id="swap-panel-next" data-dialog-panel role="dialog" aria-modal="true" hidden>
        <button id="swap-new-focus" type="button" data-dialog-initial-focus>New panel</button>
        <button id="swap-close" type="button" data-dialog-close>Close</button>
      </div>
    </template>
  </section>

  <section id="dropdown" data-kit-component="dropdown" data-kit-version="2.0.0">
    <button id="dropdown-trigger" type="button" data-dropdown-trigger>Actions</button>
    <div id="dropdown-menu" role="menu" data-dropdown-menu hidden>
      <button id="dropdown-alpha" role="menuitem" data-dropdown-item data-dropdown-value="alpha">Alpha</button>
      <button id="dropdown-disabled" role="menuitem" data-dropdown-item disabled>Beta</button>
      <button id="dropdown-aria-disabled" role="menuitem" data-dropdown-item data-dropdown-value="blocked" aria-disabled="true">Blocked</button>
      <span hidden><button id="dropdown-hidden" role="menuitem" data-dropdown-item>Charlie</button></span>
      <a id="dropdown-docs" role="menuitem" data-dropdown-item data-dropdown-value="docs" href="#docs">Documentation</a>
      <button id="dropdown-zulu" role="menuitem" data-dropdown-item>Zulu</button>
    </div>
    <output id="dropdown-selected" data-kit-text="selected"></output>
    <output id="dropdown-index" data-kit-text="activeIndex"></output>
    <button id="dropdown-refresh" type="button" hidden data-kit-click="selected = selected === 'refresh-a' ? 'refresh-b' : 'refresh-a'">Refresh</button>
  </section>
  <button id="outside" type="button">Outside</button>

  <section id="tabs-auto" data-kit-component="tabs" data-kit-version="2.0.0">
    <div data-tabs-list role="tablist">
      <button id="auto-one" role="tab" data-tab="one">One</button>
      <button id="auto-disabled" role="tab" data-tab="disabled" disabled>Disabled</button>
      <span hidden><button id="auto-hidden" role="tab" data-tab="hidden">Hidden</button></span>
      <button id="auto-two" role="tab" data-tab="two">Two</button>
      <button id="auto-two-duplicate" role="tab" data-tab="two">Two duplicate</button>
    </div>
    <div id="auto-panel-one" role="tabpanel" data-panel="one">Panel one</div>
    <div id="auto-panel-two" role="tabpanel" data-panel="two">Panel two</div>
    <output id="auto-active" data-kit-text="active"></output>
    <button id="auto-refresh" type="button" hidden data-kit-click="tabs = tabs.length ? [] : ['refresh']">Refresh</button>
  </section>
  <section id="tabs-manual" data-kit-component="tabs" data-kit-version="2.0.0" data-tabs-activation="manual">
    <div data-tabs-list role="tablist" aria-orientation="vertical">
      <button id="manual-one" role="tab" data-tab="one">One</button>
      <button id="manual-two" role="tab" data-tab="two">Two</button>
    </div>
    <div id="manual-panel-one" role="tabpanel" data-panel="one">Manual one</div>
    <div id="manual-panel-two" role="tabpanel" data-panel="two">Manual two</div>
    <output id="manual-active" data-kit-text="active"></output>
    <button id="manual-refresh" type="button" hidden data-kit-click="tabs = tabs.length ? [] : ['refresh']">Refresh</button>
  </section>

  <script src="/interactive-v2.js"></script><script>
%s
__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var nextTurn = __kitTestNextTurn;
  function key(target, value, shift) {
    target.dispatchEvent(new KeyboardEvent("keydown", { key: value, shiftKey: !!shift, bubbles: true, cancelable: true }));
  }
  await waitFor(function () {
    return document.getElementById("auto-active").textContent === "one" &&
      document.getElementById("manual-active").textContent === "one";
  }, "tabs did not derive authored IDs");

  var outerHost = document.getElementById("outer");
  document.getElementById("outer-trigger").click();
  await waitFor(function () { return !document.getElementById("outer-panel").hidden; }, "outer dialog did not open");
  assert(document.activeElement === document.getElementById("outer-first"), "dialog initial focus failed");
  assert(document.getElementById("dropdown").hasAttribute("inert") && document.getElementById("outside").hasAttribute("inert"), "dialog did not inert background siblings");
  key(document.activeElement, "Tab");
  assert(document.activeElement === document.getElementById("outer-second"), "dialog Tab did not move to the next focusable part");
  key(document.activeElement, "Tab", true);
  assert(document.activeElement === document.getElementById("outer-first"), "dialog Shift+Tab did not return to the prior focusable part");
  for (var nestedStep = 0; nestedStep < 8 && document.activeElement !== document.getElementById("inner-trigger"); nestedStep++) {
    key(document.activeElement, "Tab");
  }
  assert(document.activeElement === document.getElementById("inner-trigger"), "dialog Tab trap pruned a nested component control");

  document.getElementById("inner-trigger").click();
  await waitFor(function () { return !document.getElementById("inner-panel").hidden; }, "inner dialog did not open");
  assert(document.activeElement === document.getElementById("inner-cancel"), "inner dialog did not take focus");
  document.getElementById("outer-close").click();
  await waitFor(function () { return document.getElementById("inner-panel").hidden && document.getElementById("outer-panel").hidden; }, "closing an ancestor did not unwind its nested dialog");
  assert(!document.getElementById("dropdown").hasAttribute("inert"), "ancestor close leaked modal background state");
  document.getElementById("outer-trigger").click();
  await waitFor(function () { return !document.getElementById("outer-panel").hidden; }, "outer dialog did not reopen after cascade close");
  document.getElementById("inner-trigger").click();
  await waitFor(function () { return !document.getElementById("inner-panel").hidden; }, "inner dialog did not reopen after cascade close");
  document.getElementById("reactive-close-outer").click();
  await waitFor(function () {
    return document.getElementById("outer-panel").hidden && document.getElementById("inner-panel").hidden &&
      document.getElementById("inner-result").textContent === "ancestor";
  }, "reactive outer.open=false did not cancel the nested dialog");
  assert(!document.documentElement.style.overflow && !document.getElementById("dropdown").hasAttribute("inert") &&
    document.activeElement === document.getElementById("outer-trigger"), "reactive ancestor close leaked modal ownership or focus");
  document.getElementById("outer-trigger").click();
  await waitFor(function () { return !document.getElementById("outer-panel").hidden; }, "outer dialog did not reopen after reactive close");
  document.getElementById("inner-trigger").click();
  await waitFor(function () { return !document.getElementById("inner-panel").hidden; }, "inner dialog did not reopen after reactive close");
  key(document.activeElement, "Escape");
  await waitFor(function () { return document.getElementById("inner-panel").hidden; }, "one Escape did not close top dialog");
  assert(!document.getElementById("outer-panel").hidden, "one Escape cascaded into outer dialog");
  assert(document.activeElement === document.getElementById("inner-trigger"), "top dialog did not restore focus");
  key(document.activeElement, "Escape");
  await waitFor(function () { return document.getElementById("outer-panel").hidden; }, "second Escape did not close outer dialog");
  assert(document.activeElement === document.getElementById("outer-trigger"), "outer dialog did not restore focus");
  assert(!document.getElementById("dropdown").hasAttribute("inert") && !document.getElementById("outside").hasAttribute("inert"), "dialog did not restore background");

  var trigger = document.getElementById("dropdown-trigger");
  key(trigger, "ArrowDown");
  await waitFor(function () { return document.activeElement === document.getElementById("dropdown-alpha"); }, "dropdown ArrowDown did not focus first item");
  key(document.activeElement, "ArrowDown");
  assert(document.activeElement === document.getElementById("dropdown-docs"), "dropdown did not skip disabled item");
  key(document.activeElement, "z");
  assert(document.activeElement === document.getElementById("dropdown-zulu"), "dropdown typeahead failed");
  key(document.activeElement, "Home");
  assert(document.activeElement === document.getElementById("dropdown-alpha"), "dropdown Home failed");
  key(document.activeElement, "Escape");
  await waitFor(function () { return document.getElementById("dropdown-menu").hidden; }, "dropdown Escape failed");
  assert(document.activeElement === trigger, "dropdown Escape did not restore trigger focus");
  trigger.click();
  document.getElementById("dropdown-docs").click();
  await waitFor(function () { return location.hash === "#docs" && document.getElementById("dropdown-selected").textContent === "docs"; }, "dropdown item lost selection or link default action");

  document.getElementById("auto-one").focus();
  key(document.activeElement, "ArrowRight");
  await waitFor(function () { return document.activeElement === document.getElementById("auto-two") && document.getElementById("auto-active").textContent === "two"; }, "automatic tabs failed or did not skip disabled tab");
  assert(document.getElementById("auto-panel-one").hidden && !document.getElementById("auto-panel-two").hidden, "automatic panel sync failed");
  assert(document.querySelectorAll("#tabs-auto [aria-selected='true']").length === 1 && document.querySelectorAll("#tabs-auto [tabindex='0']").length === 1, "duplicate tab IDs gained duplicate APG state");
  document.getElementById("auto-two").disabled = true;
  document.getElementById("auto-two-duplicate").disabled = true;
  document.getElementById("auto-two").remove();
  document.getElementById("auto-two-duplicate").remove();
  document.getElementById("auto-refresh").click();
  await waitFor(function () {
    return document.activeElement === document.getElementById("auto-one") &&
      document.getElementById("auto-one").getAttribute("tabindex") === "0" &&
      document.querySelectorAll("#tabs-auto [data-tab][tabindex='0']").length === 1 &&
      document.getElementById("auto-active").textContent === "one";
  }, "tabs did not restore focus when active markers disappeared");

  document.getElementById("manual-one").focus();
  key(document.activeElement, "ArrowDown");
  assert(document.activeElement === document.getElementById("manual-two"), "manual vertical tabs did not move focus");
  assert(document.getElementById("manual-active").textContent === "one", "manual tabs activated on focus move");
  key(document.activeElement, "Enter");
  await waitFor(function () { return document.getElementById("manual-active").textContent === "two"; }, "manual tabs Enter activation failed");
  document.getElementById("manual-two").setAttribute("data-tab", "renamed");
  document.getElementById("manual-refresh").click();
  await waitFor(function () {
    return document.activeElement === document.getElementById("manual-one") &&
      document.getElementById("manual-one").getAttribute("tabindex") === "0" &&
      document.getElementById("manual-two").getAttribute("tabindex") === "-1" &&
      document.querySelectorAll("#tabs-manual [data-tab][tabindex='0']").length === 1 &&
      document.getElementById("manual-active").textContent === "one";
  }, "tabs did not restore focus when the focused tab identity changed");

  trigger.click();
  await waitFor(function () { return document.activeElement === document.getElementById("dropdown-alpha"); }, "dropdown did not reopen for keyboard activation");
  history.replaceState(null, "", location.pathname + location.search);
  key(document.activeElement, "ArrowDown");
  key(document.activeElement, "Enter");
  await waitFor(function () {
    return document.getElementById("dropdown-selected").textContent === "docs" &&
      document.getElementById("dropdown-menu").hidden && location.hash === "#docs";
  }, "dropdown Enter did not activate focused link and preserve navigation");
  trigger.click();
  await waitFor(function () { return document.activeElement === document.getElementById("dropdown-alpha"); }, "dropdown did not reopen for Space activation");
  key(document.activeElement, " ");
  await waitFor(function () {
    return document.getElementById("dropdown-selected").textContent === "alpha" && document.getElementById("dropdown-menu").hidden;
  }, "dropdown Space did not activate focused button");
  trigger.click();
  await waitFor(function () { return !document.getElementById("dropdown-menu").hidden; }, "dropdown did not reopen for disabled activation guard");
  document.getElementById("dropdown-aria-disabled").focus();
  key(document.getElementById("dropdown-aria-disabled"), "Enter");
  key(document.getElementById("dropdown-aria-disabled"), " ");
  await nextTurn();
  assert(document.getElementById("dropdown-selected").textContent === "alpha" && !document.getElementById("dropdown-menu").hidden,
    "dropdown keyboard activated an aria-disabled item");
  document.getElementById("dropdown-alpha").focus();
  key(document.getElementById("dropdown-alpha"), "ArrowDown");
  assert(document.activeElement === document.getElementById("dropdown-docs"),
    "dropdown did not establish a focused active item before reorder");
  var insertedItem = document.createElement("button");
  insertedItem.id = "dropdown-inserted";
  insertedItem.type = "button";
  insertedItem.setAttribute("role", "menuitem");
  insertedItem.setAttribute("data-dropdown-item", "");
  insertedItem.textContent = "Inserted";
  document.getElementById("dropdown-menu").insertBefore(insertedItem, document.getElementById("dropdown-docs"));
  document.getElementById("dropdown-docs").hidden = true;
  document.getElementById("dropdown-refresh").click();
  await waitFor(function () {
    return document.activeElement === insertedItem && insertedItem.getAttribute("tabindex") === "0" &&
      document.getElementById("dropdown-docs").getAttribute("tabindex") === "-1";
  }, "dropdown reorder did not recover focus from a connected unavailable active item");
  document.getElementById("dropdown-docs").hidden = false;
  insertedItem.focus();
  insertedItem.remove();
  document.getElementById("dropdown-refresh").click();
  await waitFor(function () {
    return document.getElementById("dropdown-index").textContent === "4" &&
      document.activeElement === document.getElementById("dropdown-docs") &&
      document.querySelectorAll("#dropdown-menu [data-dropdown-item][tabindex='0']").length === 1;
  }, "dropdown did not preserve numeric ownership after removing the reordered item");
  document.getElementById("dropdown-docs").setAttribute("aria-disabled", "true");
  document.getElementById("dropdown-refresh").click();
  await waitFor(function () {
    return document.getElementById("dropdown-index").textContent === "0" &&
      document.activeElement === document.getElementById("dropdown-alpha") &&
      document.querySelectorAll("#dropdown-menu [data-dropdown-item][tabindex='0']").length === 1;
  }, "dropdown did not normalize disabled active item");
  document.getElementById("dropdown-alpha").setAttribute("aria-disabled", "true");
  document.getElementById("dropdown-refresh").click();
  await waitFor(function () {
    return document.getElementById("dropdown-index").textContent === "5" &&
      document.activeElement === document.getElementById("dropdown-zulu") &&
      document.querySelectorAll("#dropdown-menu [data-dropdown-item][tabindex='0']").length === 1;
  }, "dropdown did not normalize a second disabled active item");
  document.querySelectorAll("#dropdown-menu [data-dropdown-item]").forEach(function (item) { item.remove(); });
  document.getElementById("dropdown-refresh").click();
  await waitFor(function () {
    return document.getElementById("dropdown-index").textContent === "-1" &&
      document.querySelectorAll("#dropdown-menu [data-dropdown-item][tabindex='0']").length === 0;
  }, "dropdown empty menu did not normalize to -1");
  key(trigger, "Escape");

  var swapTrigger = document.getElementById("swap-trigger");
  swapTrigger.click();
  await waitFor(function () {
    return !document.getElementById("swap-panel").hidden && document.activeElement === document.getElementById("swap-old-focus");
  }, "swap dialog did not open its first panel");
  var nextPanel = document.getElementById("swap-template").content.firstElementChild.cloneNode(true);
  document.getElementById("swap-path-b").appendChild(nextPanel);
  document.getElementById("swap-panel").remove();
  document.getElementById("swap-refresh").dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
  await waitFor(function () {
    return !nextPanel.hidden && document.activeElement === document.getElementById("swap-new-focus") &&
      document.getElementById("swap-path-a").hasAttribute("inert") &&
      !document.getElementById("swap-path-b").hasAttribute("inert");
  }, "open dialog did not transition to its replacement panel path");
  assert(document.querySelectorAll("#swap-dialog [data-dialog-panel]").length === 1,
    "dialog panel transition retained duplicate panel ownership");
  document.getElementById("swap-close").click();
  await waitFor(function () {
    return nextPanel.hidden && document.activeElement === swapTrigger && !document.documentElement.style.overflow &&
      !document.getElementById("swap-path-a").hasAttribute("inert") && !document.getElementById("swap-path-b").hasAttribute("inert");
  }, "replacement dialog panel did not restore trigger, scroll, and background");

  // Direct removal must dispose document listeners and restore global modal state.
  document.getElementById("outer-trigger").click();
  await waitFor(function () { return !document.getElementById("outer-panel").hidden; }, "dialog did not reopen before disposal");
  outerHost.remove();
  await nextTurn();
  await nextTurn();
  assert(!document.documentElement.style.overflow, "dialog disposal leaked scroll lock");
  assert(!document.getElementById("dropdown").hasAttribute("inert") && !document.getElementById("outside").hasAttribute("inert"), "dialog disposal leaked inert background");
  key(document.body, "Escape");
  await nextTurn();
});
  </script>
</body></html>`, browserHarness)
