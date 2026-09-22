package javascript

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The system variables of ideaship-final §3 inside an action of the component runtime: `$this` is
// the element that owns the attribute (`$el`, the old spelling, no longer resolves — B1 22/09),
// `$host` the nearest boundary element,
// `$event.target` / `$event.submitter` the real elements behind the event, read through the same
// closed element table as `$element`. A control button on the page has <html> as its host.
func TestBrowserSystemVariablesInActions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping system variables browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeStandalone([]ComponentRef{{Name: "switch", Version: "1.0.0"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/kit.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(bundle.JavaScript)
		case "/system.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(systemVariablesDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/system.html")
}

var systemVariablesDocument = fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>KitJS system variables</title></head><body>
  <section id="boundary" data-kit-scope="thisId: '', sameEl: 'unset', hostId: '', targetId: '', submitterId: '', eventType: ''">
    <button id="probe" type="button" data-kit-click="thisId = $this.id; sameEl = 'n/a'; hostId = $host.id; targetId = $event.target.id; eventType = $event.type"><span id="inner">probe</span></button>
    <form id="form" data-kit-submit:prevent="submitterId = $event.submitter.id"><button id="send" type="submit">send</button></form>
    <button id="old-el" type="button" data-kit-click="thisId = $el.id">old spelling</button>
    <output id="out" data-kit-text="thisId + ' ' + sameEl + ' ' + hostId + ' ' + targetId + ' ' + eventType + ' ' + submitterId"></output>
  </section>
  <script src="/kit.js"></script><script>
%s
__runStandaloneKitTest(async function () {
  var waitFor = __kitTestWaitFor;
  var byId = function (id) { return document.getElementById(id); };
  await waitFor(function () { return byId("out").textContent.indexOf("unset") >= 0; }, "scope did not render");
  byId("inner").click();
  await waitFor(function () { return byId("out").textContent.indexOf("probe n/a boundary inner click") === 0; }, "system variables in a scoped action, got " + JSON.stringify(byId("out").textContent));
  // $el is gone: the old spelling is an unknown name, the action fails and state is untouched.
  var errorsBefore = (window.__kitErrors || []).length;
  byId("old-el").click();
  await __kitTestNextTurn();
  var assertText = __kitTestAssert;
  assertText(byId("out").textContent.indexOf("probe n/a") === 0, "$el must not resolve any more, out = " + byId("out").textContent);
  byId("send").click();
  await waitFor(function () { return byId("out").textContent.indexOf(" send") > 0; }, "$event.submitter should be the submit button, got " + JSON.stringify(byId("out").textContent));
});
  </script>
</body></html>`, browserHarness)
