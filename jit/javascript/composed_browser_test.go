package javascript

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"
	"time"
)

func TestBrowserStandaloneAutoBootHydratesWithoutRootMarker(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping headless composed-bundle contract in short mode")
	}
	browser := findContractBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	selection := []byte(`<section data-kit-component="counter" data-kit-version="1.0.0"></section>`)
	bundle, err := composer.ComposeHTML(selection)
	if err != nil {
		t.Fatal(err)
	}
	for _, module := range bundle.Modules {
		if module.Kind == CoreModule && (module.Name == "morph" || module.Name == "drive") {
			t.Fatalf("component-only bundle unexpectedly contains %s", module)
		}
	}

	pageSource := `<!doctype html>
<html data-test-status="pending">
<head><meta charset="utf-8"><title>Composed KitJS contract</title></head>
<body>
  <section data-kit-component="counter" data-kit-version="1.0.0">
    <button id="increment" type="button" data-kit-click="count = count + 1">Increment</button>
    <output id="count" data-kit-text="count">server fallback</output>
  </section>
  <script>
    (function () {
      var root = document.documentElement;
      function fail(error) {
        root.setAttribute("data-test-status", "failed");
        root.setAttribute("data-test-error", String(error && error.message || error));
      }
      window.addEventListener("error", function (event) { fail(event.error || event.message); });
      window.addEventListener("unhandledrejection", function (event) { fail(event.reason); });
      document.addEventListener("DOMContentLoaded", function () {
        setTimeout(function () {
          try {
            if (!window.kit || typeof kit.component !== "function") throw new Error("composed public API missing");
            var publicKeys = Object.keys(kit);
            if (publicKeys.length !== 2 || publicKeys[0] !== "version" || publicKeys[1] !== "component") {
              throw new Error("unexpected core public keys: " + publicKeys.join(","));
            }
            ["start", "destroy", "use", "mount", "unmount"].forEach(function (name) {
              if (kit[name] !== undefined) throw new Error("private runtime control leaked: kit." + name);
            });
            var lookupRejected = false;
            try { kit.component("counter"); }
            catch (error) { lookupRejected = error instanceof TypeError; }
            if (!lookupRejected) throw new Error("kit.component retained its one-argument getter");
            var nonPlainRejected = false;
            try { kit.component("invalid-array", []); }
            catch (error) { nonPlainRejected = error instanceof TypeError; }
            if (!nonPlainRejected) throw new Error("kit.component accepted a non-plain definition");
            if (Object.prototype.hasOwnProperty.call(kit, "__kitwork_core__")) throw new Error("assembly capsule leaked");
            if (kit.morph !== undefined || kit.drive !== undefined) throw new Error("optional navigation leaked into base bundle");
            if (document.querySelector("[data-kit-app],[data-kit-hydrate]")) throw new Error("root activation marker leaked into standalone contract");
            if (document.getElementById("count").textContent !== "0") throw new Error("initial binding did not hydrate");
            document.getElementById("increment").click();
            setTimeout(function () {
              try {
                if (document.getElementById("count").textContent !== "1") throw new Error("composed event did not update state");

                var added = document.createElement("section");
                added.id = "added-counter";
                added.setAttribute("data-kit-component", "counter");
                added.innerHTML = '<button id="added-increment" type="button" data-kit-click="count = count + 1">Add</button>' +
                  '<output id="added-count" data-kit-text="count">server fallback</output>';
                document.body.appendChild(added);
                setTimeout(function () {
                  try {
                    if (document.getElementById("added-count").textContent !== "0") throw new Error("added subtree did not hydrate");
                    document.getElementById("added-increment").click();
                    setTimeout(function () {
                      try {
                        if (document.getElementById("added-count").textContent !== "1") throw new Error("added subtree event did not update state");
                        root.setAttribute("data-test-status", "passed");
                      } catch (error) { fail(error); }
                    }, 0);
                  } catch (error) { fail(error); }
                }, 0);
              } catch (error) { fail(error); }
            }, 0);
          } catch (error) { fail(error); }
        }, 0);
      }, { once: true });
    })();
  </script>
</body>
</html>`
	page, err := InjectRuntime([]byte(pageSource), bundle)
	if err != nil {
		t.Fatal(err)
	}
	assetPath := "/kit.js/" + bundle.ContentHash + ".js"

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case assetPath:
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(bundle.JavaScript)
		default:
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write(page)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	args := []string{
		"--headless=new",
		"--disable-gpu",
		"--no-sandbox",
		"--disable-dev-shm-usage",
		"--disable-background-networking",
		"--disable-default-apps",
		"--disable-extensions",
		"--disable-sync",
		"--metrics-recording-only",
		"--no-first-run",
		"--run-all-compositor-stages-before-draw",
		"--user-data-dir=" + t.TempDir(),
		"--virtual-time-budget=4000",
		"--dump-dom",
		server.URL,
	}
	output, runErr := exec.CommandContext(ctx, browser, args...).CombinedOutput()
	if bytes.Contains(output, []byte(`data-test-status="passed"`)) {
		return
	}
	if ctx.Err() != nil {
		t.Fatalf("headless composed-bundle contract timed out: %v\n%s", ctx.Err(), boundedBrowserOutput(output))
	}
	if runErr != nil {
		t.Fatalf("headless composed-bundle contract failed to run: %v\n%s", runErr, boundedBrowserOutput(output))
	}
	t.Fatalf("headless composed-bundle contract did not pass\n%s", boundedBrowserOutput(output))
}
