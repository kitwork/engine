package javascript

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBrowserStyleDirectiveConformance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping standalone KitJS style conformance in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	kitJS, err := SourceForProfile(ProfileKit)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/kit.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(kitJS)
		case "/style.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(styleDirectiveDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/style.html")
}

const styleDirectiveDocument = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>KitJS style directive contract</title>
  <script>
    globalThis.__kitStyleErrors = [];
    (function (original) {
      console.error = function (error) {
        globalThis.__kitStyleErrors.push(String(error && error.message || error));
        return original.apply(this, arguments);
      };
    })(console.error);
  </script>
  <script src="/kit.js"></script>
  <script>
    kit.component("invalid-style-values", {
      promised: function () { return Promise.resolve("40px"); },
      object: function () { return { width: "40px" }; },
      infinite: function () { return 1 / 0; }
    });
  </script>
</head>
<body>
  <main data-kit-scope="progress: 25; visible: true; left: 10; danger: 'none'; vector: 'translateX(1px)'">
    <div id="styled" style="height: 7px; opacity: 0.25 !important; --Authored: keep"
      data-kit-style="width: progress + '%'; opacity: visible ? 1 : null; --Meter: progress + '%'; transform: visible ? 'translateX(2px)' : 'translateX(0px)'"></div>
    <div id="transaction" style="left: 3px; background-image: none"
      data-kit-style="left: left + 'px'; background-image: danger;"></div>
    <div id="adversarial" style="transform: translateX(4px); color: rgb(1, 2, 3)"
      data-kit-style="transform: vector; color: 'rgb(4, 5, 6)';"></div>
    <div id="longhand" style="margin-left: 11px !important"
      data-kit-style="margin-left: visible ? '2px' : null;"></div>
    <div id="shorthand" style="margin-left: 13px"
      data-kit-style="margin: '2px';"></div>
    <div id="braces" style="width: 3px" data-kit-style="{ width: progress + '%' }"></div>
    <div id="duplicate" style="width: 4px" data-kit-style="width: progress + '%'; width: '9px';"></div>
    <div id="blocked-name" style="width: 5px" data-kit-style="css-text: 'width: 9px';"></div>
    <div data-kit-ignore><div id="ignored" style="width: 6px" data-kit-style="width: progress + '%';"></div></div>
    <button id="update" type="button" data-kit-click="progress = 75; visible = false">update</button>
    <button id="unsafe" type="button" data-kit-click="left = 20; danger = 'url(https://example.test/track)'">unsafe</button>
    <button id="unsafe-var" type="button" data-kit-click="vector = 'var(--external)'; progress = 90">unsafe var</button>
    <button id="unsafe-comment" type="button" data-kit-click="vector = 'translateX(2px)/*x*/'; progress = 91">unsafe comment</button>
    <button id="unsafe-important" type="button" data-kit-click="vector = 'translateX(2px) !important'; progress = 92">unsafe important</button>
    <button id="unsafe-attr" type="button" data-kit-click="vector = 'translateX(attr(data-x px))'; progress = 93">unsafe attr</button>
  </main>
  <section data-kit-component="invalid-style-values">
    <div id="async-style" style="width: 7px" data-kit-style="width: promised();"></div>
    <div id="object-style" style="width: 8px" data-kit-style="width: object();"></div>
    <div id="infinite-style" style="width: 9px" data-kit-style="width: infinite();"></div>
  </section>
  <script>
` + browserHarness + `
__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var nextTurn = __kitTestNextTurn;
  __kitTestPublicContract();

  function byId(id) { return document.getElementById(id); }
  await waitFor(function () {
    return byId("styled").style.width === "25%" &&
      byId("styled").style.opacity === "1" &&
      byId("styled").style.getPropertyValue("--Meter") === "25%" &&
      byId("transaction").style.left === "10px" &&
      byId("longhand").style.marginLeft === "2px";
  }, "initial style render did not settle");

  assert(byId("styled").style.height === "7px" &&
    byId("styled").style.getPropertyValue("--Authored") === "keep",
    "style directive changed unowned authored styles");
  assert(byId("braces").style.width === "3px", "outer braces changed server DOM");
  assert(byId("duplicate").style.width === "4px", "duplicate property changed server DOM");
  assert(byId("blocked-name").style.width === "5px", "blocked property changed server DOM");
  assert(byId("shorthand").style.marginLeft === "13px", "shorthand property changed server DOM");
  assert(globalThis.__kitStyleErrors.some(function (message) {
    return message.indexOf('shorthand style property "margin" is not supported') >= 0;
  }), "shorthand property was not rejected during preparation");
  assert(byId("ignored").style.width === "6px", "ignored style directive activated");
  assert(byId("async-style").style.width === "7px" && byId("object-style").style.width === "8px" &&
    byId("infinite-style").style.width === "9px", "invalid runtime style values changed server DOM");

  byId("update").click();
  await waitFor(function () {
    return byId("styled").style.width === "75%" && byId("styled").style.opacity === "0.25" &&
      byId("styled").style.getPropertyPriority("opacity") === "important" &&
      byId("styled").style.getPropertyValue("--Meter") === "75%" &&
      byId("styled").style.transform === "translateX(0px)" &&
      byId("longhand").style.marginLeft === "11px" &&
      byId("longhand").style.getPropertyPriority("margin-left") === "important";
  }, "reactive style update or baseline restoration failed");

  var errors = globalThis.__kitStyleErrors.length;
  byId("unsafe").click();
  await waitFor(function () { return globalThis.__kitStyleErrors.length > errors; },
    "unsafe CSS value was not reported");
  await nextTurn();
  assert(byId("transaction").style.left === "10px" && byId("transaction").style.backgroundImage === "none",
    "invalid style render partially mutated the DOM");

  for (var unsafe of ["unsafe-var", "unsafe-comment", "unsafe-important", "unsafe-attr"]) {
    var previousErrors = globalThis.__kitStyleErrors.length;
    byId(unsafe).click();
    await waitFor(function () { return globalThis.__kitStyleErrors.length > previousErrors; },
      unsafe + " style value was not reported");
    await nextTurn();
    assert(byId("adversarial").style.transform === "translateX(1px)" &&
      byId("adversarial").style.color === "rgb(4, 5, 6)",
      unsafe + " invalid map partially mutated the DOM");
  }
});
  </script>
</body>
</html>`
