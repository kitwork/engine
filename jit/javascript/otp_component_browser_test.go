package javascript

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The otp component browser proof drives its owned [data-otp-slot] inputs:
// digit entry assembles the value, non-digits are rejected, clear() empties it,
// and a paste distributes digits across the remaining slots.
func TestBrowserOTPComponentAssemblesValue(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping otp component browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeStandalone([]ComponentRef{{Name: "otp", Version: "1.0.0"}}, false)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/otp.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(bundle.JavaScript)
		case "/otp.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(otpComponentDocument))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/otp.html")
}

var otpComponentDocument = fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>KitJS otp component</title></head><body>
  <section data-kit-component="otp@1.0.0">
    <input id="otp-0" data-otp-slot inputmode="numeric">
    <input id="otp-1" data-otp-slot inputmode="numeric">
    <input id="otp-2" data-otp-slot inputmode="numeric">
    <input id="otp-3" data-otp-slot inputmode="numeric">
    <button id="otp-clear" data-kit-click="clear()">clear</button>
    <output id="otp-value" data-kit-text="value"></output>
    <output id="otp-complete" data-kit-text="isComplete() ? 'yes' : 'no'"></output>
  </section>
  <script src="/otp.js"></script><script>
%s
__runStandaloneKitTest(async function () {
  var waitFor = __kitTestWaitFor;
  var assert = __kitTestAssert;
  function type(id, value) { var el = document.getElementById(id); el.value = value; el.dispatchEvent(new Event("input", { bubbles: true })); }
  function text(id) { return document.getElementById(id).textContent.trim(); }

  await waitFor(function () { return document.getElementById("otp-0").getAttribute("maxlength") === "1"; }, "otp did not mount and observe its slots");

  type("otp-0", "5");
  await waitFor(function () { return text("otp-value") === "5"; }, "otp did not accept the first digit");

  type("otp-1", "x");
  await waitFor(function () { return text("otp-value") === "5"; }, "otp accepted a non-digit");

  type("otp-1", "6"); type("otp-2", "7"); type("otp-3", "8");
  await waitFor(function () { return text("otp-value") === "5678" && text("otp-complete") === "yes"; }, "otp did not assemble the full code");

  document.getElementById("otp-clear").click();
  await waitFor(function () { return text("otp-value") === "" && text("otp-complete") === "no"; }, "otp clear did not empty the code");

  assert(typeof DataTransfer === "function", "browser lacks DataTransfer for the paste proof");
  var transfer = new DataTransfer();
  transfer.setData("text", "ab12-34cd");
  document.getElementById("otp-0").dispatchEvent(new ClipboardEvent("paste", { clipboardData: transfer, bubbles: true, cancelable: true }));
  await waitFor(function () { return text("otp-value") === "1234" && text("otp-complete") === "yes"; }, "otp paste did not distribute digits");
});
  </script>
</body></html>`, browserHarness)
