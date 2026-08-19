package javascript

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBrowserClassAndModelConformance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping standalone KitJS class/model conformance in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	kitJS, err := SourceForProfile(ProfileKit)
	if err != nil {
		t.Fatal(err)
	}
	source := readVanillaFile(t, "examples", "form.html")
	modelMarker := []byte(`<input id="form-missing" data-kit-model="missingField" value="server-missing">`)
	if bytes.Count(source, modelMarker) != 1 {
		t.Fatal("form example lost the invalid-model test seam")
	}
	source = bytes.Replace(source, modelMarker, []byte(`<input id="form-reserved" data-kit-model="$field" value="server-reserved">
        <input id="form-dollar" data-kit-model="field$value" value="server-dollar">
        <input id="form-missing" data-kit-model="missingField" value="server-missing">`), 1)
	runtimeMarker := []byte(`<script src="../kit.js"></script>`)
	if bytes.Count(source, runtimeMarker) != 1 {
		t.Fatal("form example lost the runtime test seam")
	}
	source = bytes.Replace(source, runtimeMarker, []byte(`<script>
    globalThis.__kitFormErrors = [];
    (function (originalError) {
      console.error = function (error) {
        globalThis.__kitFormErrors.push(String(error && error.message || error));
        return originalError.apply(this, arguments);
      };
    })(console.error);
  </script>
  <script src="../kit.js"></script>`), 1)
	fixture := injectBrowserAssertions(t, source, classModelAssertions)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/kit.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(kitJS)
		case "/examples/form.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write(fixture)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/examples/form.html")
}

const classModelAssertions = `__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var nextTurn = __kitTestNextTurn;
  __kitTestPublicContract();

  function byId(id) { return document.getElementById(id); }
  function dispatch(element, type) {
    element.dispatchEvent(new Event(type, { bubbles: true, cancelable: true }));
  }
  function delay(milliseconds) {
    return new Promise(function (resolve) { setTimeout(resolve, milliseconds); });
  }
  async function assertRenderSettles(message) {
    await nextTurn();
    var reads = globalThis.__kitFormRenderReads;
    await delay(80);
    assert(globalThis.__kitFormRenderReads === reads, message + ": render reads changed from " + reads + " to " + globalThis.__kitFormRenderReads);
  }

  var summary = byId("form-summary");
  var name = byId("form-name");
  var biography = byId("form-biography");
  var country = byId("form-country");
  var subscribed = byId("form-subscribed");
  var planBasic = byId("form-plan-basic");
  var planPro = byId("form-plan-pro");
  var age = byId("form-age");
  var volume = byId("form-volume");

  assert(summary && name && biography && country && subscribed && planBasic && planPro && age && volume, "form demo controls missing");
  await waitFor(function () {
    return name.value === "Ada Lovelace" &&
      biography.value === "First computer programmer" &&
      country.value === "vn" &&
      subscribed.checked === true &&
      planBasic.checked === false && planPro.checked === true &&
      age.value === "36" && volume.value === "60" &&
      summary.classList.contains("bg-slate-100");
  }, "class/model directives did not perform their initial state-to-DOM render");

  assert(summary.classList.contains("form-summary"), "class binding removed an authored static class");
  assert(summary.classList.contains("shared-token"), "class binding removed a static token shared with a dynamic branch");
  assert(summary.classList.contains("text-slate-900") && summary.classList.contains("md:grid-cols-1"), "class binding did not apply complete inactive tokens");
  assert(!summary.classList.contains("bg-emerald-600") && !summary.classList.contains("md:grid-cols-2"), "class binding mixed active and inactive tokens");

  summary.classList.add("external-owned-by-app");
  byId("form-highlight").click();
  await waitFor(function () {
    return summary.classList.contains("bg-emerald-600") &&
      summary.classList.contains("text-white") &&
      summary.classList.contains("md:grid-cols-2");
  }, "class binding did not switch to its active full-token set");
  assert(!summary.classList.contains("bg-slate-100") && !summary.classList.contains("text-slate-900") && !summary.classList.contains("md:grid-cols-1"), "class binding retained stale owned tokens");
  assert(summary.classList.contains("form-summary") && summary.classList.contains("shared-token") && summary.classList.contains("external-owned-by-app"), "class binding removed a class it did not own");

  byId("form-highlight").click();
  await waitFor(function () { return summary.classList.contains("bg-slate-100"); }, "class binding did not return to its inactive tokens");
  assert(!summary.classList.contains("bg-emerald-600") && summary.classList.contains("external-owned-by-app") && summary.classList.contains("shared-token"), "class binding ownership drifted after a second update");

  name.dispatchEvent(new CompositionEvent("compositionstart", { bubbles: true }));
  name.value = "Linh";
  name.dispatchEvent(new InputEvent("input", {
    bubbles: true,
    cancelable: true,
    data: "Linh",
    inputType: "insertCompositionText",
    isComposing: true
  }));
  await nextTurn();
  assert(byId("form-name-output").textContent === "Ada Lovelace", "model committed an intermediate IME composition value");
  name.dispatchEvent(new CompositionEvent("compositionend", { bubbles: true, data: "Linh" }));
  name.dispatchEvent(new InputEvent("input", {
    bubbles: true,
    cancelable: true,
    data: "Linh",
    inputType: "insertText",
    isComposing: false
  }));

  biography.value = "Runtime biography";
  dispatch(biography, "input");
  country.value = "jp";
  dispatch(country, "change");
  subscribed.checked = false;
  dispatch(subscribed, "change");
  planBasic.checked = true;
  dispatch(planBasic, "change");
  age.value = "43";
  dispatch(age, "input");
  volume.value = "75";
  dispatch(volume, "input");

  await waitFor(function () {
    return byId("form-name-output").textContent === "Linh" &&
      byId("form-biography-output").textContent === "Runtime biography" &&
      byId("form-country-output").textContent === "jp" &&
      byId("form-subscribed-output").textContent === "false" &&
      byId("form-plan-output").textContent === "basic" &&
      byId("form-age-output").textContent === "43" &&
      byId("form-volume-output").textContent === "75";
  }, "model did not write every supported control back to component state");
  assert(byId("form-age-kind").textContent === "number", "number model wrote a string");
  assert(byId("form-volume-kind").textContent === "number", "range model wrote a string");
  await assertRenderSettles("DOM-to-state model update caused a render loop");

  byId("form-load-example").click();
  await waitFor(function () {
    return name.value === "Grace Hopper" &&
      biography.value === "Compiler pioneer" &&
      country.value === "us" &&
      subscribed.checked === true &&
      planBasic.checked === false && planPro.checked === true &&
      age.value === "85" && volume.value === "25";
  }, "model did not reflect component state back to every supported control");
  await assertRenderSettles("state-to-DOM model update caused a render loop");

  var missing = byId("form-missing");
  var readonly = byId("form-readonly");
  var nested = byId("form-nested");
  var reserved = byId("form-reserved");
  var dollar = byId("form-dollar");
  assert(reserved.value === "server-reserved", "reserved-$ model changed server DOM during preparation");
  assert(dollar.value === "server-dollar", "model containing $ changed server DOM during preparation");
  assert(missing.value === "server-missing", "missing-field model changed server DOM during preparation");
  assert(readonly.value === "server-readonly", "read-only model changed server DOM during preparation");
  assert(nested.value === "server-nested", "member-path model changed server DOM during preparation");
  missing.value = "created";
  dispatch(missing, "input");
  readonly.value = "changed";
  dispatch(readonly, "input");
  nested.value = "changed";
  dispatch(nested, "input");
  reserved.value = "changed";
  dispatch(reserved, "input");
  dollar.value = "changed";
  dispatch(dollar, "input");
  byId("form-highlight").click();
  await nextTurn();
  assert(byId("form-missing-output").textContent === "false", "model created an unknown component field");
  assert(byId("form-readonly-output").textContent === "locked", "model wrote a read-only component field");
  assert(byId("form-nested-output").textContent === "Nested Ada", "model accepted a member path instead of a direct field");
  assert(globalThis.__kitFormErrors.some(function (message) {
    return message === "KitJS: model cannot use the reserved $ namespace";
  }), "model did not reject the reserved $ namespace during directive preparation");
  assert(globalThis.__kitFormErrors.some(function (message) {
    return message === "KitJS: model must name one component field";
  }), "model did not enforce the exact field-name grammar during directive preparation");
  await assertRenderSettles("invalid model handling caused a render loop");
});`
