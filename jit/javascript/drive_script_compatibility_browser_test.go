package javascript

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestBrowserHydrateDrivePreservesNativeScriptSemantics(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Hydrate Drive script compatibility browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	hydrateJS, err := SourceForProfile(ProfileHydrate)
	if err != nil {
		t.Fatal(err)
	}
	helperIntegrity := driveScriptIntegrity([]byte(driveScriptHelperSource))
	contractIntegrity := driveScriptIntegrity([]byte(driveScriptContractSource))
	hydrateIntegrity := driveScriptIntegrity(hydrateJS)
	profileTag := fmt.Sprintf(`<script defer src="/hydrate.kit.js?v=script-contract" integrity="%s" crossorigin="anonymous"></script>`, hydrateIntegrity)
	var driveOnlyScriptRequests atomic.Int64

	document := func(title, route, headScripts, bodyScripts string) string {
		return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="description" content="%s description">
  <title>%s</title>
  <script defer src="/script-helper.js" integrity="%s" crossorigin="anonymous"></script>
  %s
  %s
  <script defer src="/script-contract.js" integrity="%s" crossorigin="anonymous"></script>
</head>
<body>
  <nav>
    <a id="shared-link" href="/script/shared">Shared</a>
    <a id="inline-link" href="/script/inline">Inline</a>
    <a id="external-link" href="/script/external">External</a>
    <a id="removed-link" href="/script/removed">Removed</a>
    <a id="order-link" href="/script/order">Order</a>
    <a id="attribute-link" href="/script/attribute">Attribute</a>
    <a id="unsigned-link" href="/script/unsigned">Unsigned</a>
    <a id="invalid-integrity-link" href="/script/invalid-integrity">Invalid integrity</a>
    <a id="module-link" href="/script/module">Module</a>
    <a id="importmap-link" href="/script/importmap">Import map</a>
    <a id="profile-async-link" href="/script/profile-async">Profile async</a>
    <a id="profile-integrity-link" href="/script/profile-integrity">Profile integrity</a>
    <a id="profile-unsigned-link" href="/script/profile-unsigned">Profile unsigned</a>
    <a id="profile-body-link" href="/script/profile-body">Profile body</a>
    <a id="profile-attribute-link" href="/script/profile-attribute">Profile attribute</a>
    <a id="cross-slot-link" href="/script/cross-slot">Cross slot</a>
    <a id="unsigned-current-link" href="/script/unsigned-current-next">Unsigned current profile</a>
    <a id="changed-link" href="/script/changed">Changed</a>
  </nav>
  <main id="script-route">%s</main>
  <script id="route-data" type="application/json">{"route":%q}</script>
  %s
</body>
</html>`, title, title, helperIntegrity, profileTag, headScripts, contractIntegrity, route, route, bodyScripts)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		writeFallback := func(cookie string) {
			response.Header().Set("Set-Cookie", cookie+"=1; Path=/; SameSite=Lax")
			response.WriteHeader(http.StatusNoContent)
		}
		switch request.URL.Path {
		case "/hydrate.kit.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(hydrateJS)
		case "/script-helper.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(driveScriptHelperSource))
		case "/script-contract.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(driveScriptContractSource))
		case "/script-drive-only.js":
			driveOnlyScriptRequests.Add(1)
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(`sessionStorage.setItem("driveOnlyExternalRuns", String(Number(sessionStorage.getItem("driveOnlyExternalRuns") || 0) + 1));`))
		case "/script-changed.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(driveScriptChangedSource))
		case "/script/host":
			writeHydrateHTML(response, `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Drive script host</title></head><body><iframe id="script-frame" src="/script/start" title="Drive script contract"></iframe></body></html>`)
		case "/script/start":
			writeHydrateHTML(response, document("Script start", "Start", "", ""))
		case "/script/shared":
			writeHydrateHTML(response, document("Script shared", "Shared", "", ""))
		case "/script/inline":
			if request.Header.Get("X-KitJS-Drive") != "1" {
				writeFallback("drive_inline_fallback")
				return
			}
			writeHydrateHTML(response, document("Inline changed", "Inline changed", "",
				`<script>sessionStorage.setItem("driveOnlyInlineRuns", String(Number(sessionStorage.getItem("driveOnlyInlineRuns") || 0) + 1));</script>`))
		case "/script/external":
			if request.Header.Get("X-KitJS-Drive") != "1" {
				writeFallback("drive_external_fallback")
				return
			}
			writeHydrateHTML(response, document("External changed", "External changed",
				`<script defer src="/script-drive-only.js"></script>`, ""))
		case "/script/removed":
			if request.Header.Get("X-KitJS-Drive") != "1" {
				writeFallback("drive_removed_fallback")
				return
			}
			removed := document("Removed script", "Removed script", "", "")
			removed = fmt.Sprintf("%s", removed)
			removed = replaceOnce(removed,
				fmt.Sprintf(`  <script defer src="/script-helper.js" integrity="%s" crossorigin="anonymous"></script>`+"\n", helperIntegrity), "")
			writeHydrateHTML(response, removed)
		case "/script/order":
			if request.Header.Get("X-KitJS-Drive") != "1" {
				writeFallback("drive_order_fallback")
				return
			}
			ordered := document("Reordered script", "Reordered script", "", "")
			helperTag := fmt.Sprintf(`  <script defer src="/script-helper.js" integrity="%s" crossorigin="anonymous"></script>`, helperIntegrity)
			contractTag := fmt.Sprintf(`  <script defer src="/script-contract.js" integrity="%s" crossorigin="anonymous"></script>`, contractIntegrity)
			ordered = replaceOnce(ordered, helperTag, "")
			ordered = replaceOnce(ordered, contractTag, helperTag+"\n"+contractTag)
			ordered = replaceOnce(ordered, helperTag+"\n"+contractTag, contractTag+"\n"+helperTag)
			writeHydrateHTML(response, ordered)
		case "/script/attribute":
			if request.Header.Get("X-KitJS-Drive") != "1" {
				writeFallback("drive_attribute_fallback")
				return
			}
			changed := document("Attribute changed", "Attribute changed", "", "")
			changed = replaceOnce(changed, `src="/script-helper.js"`, `src="/script-helper.js" data-revision="2"`)
			writeHydrateHTML(response, changed)
		case "/script/unsigned":
			if request.Header.Get("X-KitJS-Drive") != "1" {
				writeFallback("drive_unsigned_fallback")
				return
			}
			unsigned := document("Unsigned script", "Unsigned script", "", "")
			unsigned = replaceOnce(unsigned, ` integrity="`+helperIntegrity+`"`, "")
			writeHydrateHTML(response, unsigned)
		case "/script/invalid-integrity":
			if request.Header.Get("X-KitJS-Drive") != "1" {
				writeFallback("drive_invalid_integrity_fallback")
				return
			}
			invalid := document("Invalid integrity", "Invalid integrity", "", "")
			invalid = replaceOnce(invalid, helperIntegrity, "sha256-A")
			writeHydrateHTML(response, invalid)
		case "/script/module":
			if request.Header.Get("X-KitJS-Drive") != "1" {
				writeFallback("drive_module_fallback")
				return
			}
			writeHydrateHTML(response, document("Module changed", "Module changed",
				`<script type="module">sessionStorage.setItem("driveOnlyModuleRuns", "1");</script>`, ""))
		case "/script/importmap":
			if request.Header.Get("X-KitJS-Drive") != "1" {
				writeFallback("drive_importmap_fallback")
				return
			}
			writeHydrateHTML(response, document("Import map changed", "Import map changed",
				`<script type="importmap">{"imports":{"drive-probe":"/script-drive-only.js"}}</script>`, ""))
		case "/script/profile-async":
			if request.Header.Get("X-KitJS-Drive") != "1" {
				writeFallback("drive_profile_async_fallback")
				return
			}
			changed := document("Profile async", "Profile async", "", "")
			changed = replaceOnce(changed, profileTag, replaceOnce(profileTag, `<script defer`, `<script defer async`))
			writeHydrateHTML(response, changed)
		case "/script/profile-integrity":
			if request.Header.Get("X-KitJS-Drive") != "1" {
				writeFallback("drive_profile_integrity_fallback")
				return
			}
			changed := document("Profile integrity", "Profile integrity", "", "")
			changed = replaceOnce(changed, profileTag,
				replaceOnce(profileTag, hydrateIntegrity, helperIntegrity))
			writeHydrateHTML(response, changed)
		case "/script/profile-unsigned":
			if request.Header.Get("X-KitJS-Drive") != "1" {
				writeFallback("drive_profile_unsigned_fallback")
				return
			}
			changed := document("Profile unsigned", "Profile unsigned", "", "")
			changed = replaceOnce(changed, profileTag,
				replaceOnce(profileTag, ` integrity="`+hydrateIntegrity+`"`, ""))
			writeHydrateHTML(response, changed)
		case "/script/profile-body":
			if request.Header.Get("X-KitJS-Drive") != "1" {
				writeFallback("drive_profile_body_fallback")
				return
			}
			changed := document("Profile body", "Profile body", "", "")
			changed = replaceOnce(changed, "  "+profileTag+"\n", "")
			changed = replaceOnce(changed, "</body>", "  "+profileTag+"\n</body>")
			writeHydrateHTML(response, changed)
		case "/script/profile-attribute":
			if request.Header.Get("X-KitJS-Drive") != "1" {
				writeFallback("drive_profile_attribute_fallback")
				return
			}
			changed := document("Profile attribute", "Profile attribute", "", "")
			changed = replaceOnce(changed, profileTag,
				replaceOnce(profileTag, ` crossorigin="anonymous"`, ` crossorigin="anonymous" data-profile-revision="2"`))
			writeHydrateHTML(response, changed)
		case "/script/cross-slot":
			if request.Header.Get("X-KitJS-Drive") != "1" {
				writeFallback("drive_cross_slot_fallback")
				return
			}
			changed := document("Cross slot", "Cross slot", "", "")
			helperTag := fmt.Sprintf(`  <script defer src="/script-helper.js" integrity="%s" crossorigin="anonymous"></script>`, helperIntegrity)
			changed = replaceOnce(changed, helperTag+"\n  "+profileTag, "  "+profileTag+"\n"+helperTag)
			writeHydrateHTML(response, changed)
		case "/script/changed":
			if request.Header.Get("X-KitJS-Drive") != "1" {
				response.Header().Set("Set-Cookie", "drive_changed_full=1; Path=/; SameSite=Lax")
			}
			changed := document("Script changed full", "Changed full",
				`<script defer src="/script-changed.js"></script>`,
				`<script>sessionStorage.setItem("driveChangedInlineRuns", String(Number(sessionStorage.getItem("driveChangedInlineRuns") || 0) + 1));</script>`)
			changed = replaceOnce(changed, profileTag,
				replaceOnce(profileTag, ` integrity="`+hydrateIntegrity+`"`, ""))
			writeHydrateHTML(response, changed)
		case "/script/unsigned-current-next":
			if request.Header.Get("X-KitJS-Drive") != "1" {
				writeFallback("drive_unsigned_current_fallback")
				return
			}
			changed := document("Unsigned current next", "Unsigned current next", "", "")
			changed = replaceOnce(changed, profileTag,
				replaceOnce(profileTag, ` integrity="`+hydrateIntegrity+`"`, ""))
			writeHydrateHTML(response, changed)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/script/host")
	if got := driveOnlyScriptRequests.Load(); got != 0 {
		t.Fatalf("fetched page-specific external script requests = %d, want zero before 204 fallback", got)
	}
}

func driveScriptIntegrity(source []byte) string {
	sum := sha256.Sum256(source)
	return "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
}

func replaceOnce(source, old, replacement string) string {
	index := -1
	for offset := 0; offset+len(old) <= len(source); offset++ {
		if source[offset:offset+len(old)] == old {
			index = offset
			break
		}
	}
	if index < 0 {
		return source
	}
	return source[:index] + replacement + source[index+len(old):]
}

const driveScriptHelperSource = `globalThis.__driveScriptHelperRuns = (globalThis.__driveScriptHelperRuns || 0) + 1;`

const driveScriptChangedSource = `sessionStorage.setItem("driveChangedExternalRuns", String(Number(sessionStorage.getItem("driveChangedExternalRuns") || 0) + 1));
globalThis.__driveScriptChangedRuns = (globalThis.__driveScriptChangedRuns || 0) + 1;`

const driveScriptContractSource = `(function (global, document) {
  "use strict";
  global.__driveScriptContractRuns = (global.__driveScriptContractRuns || 0) + 1;

  function finish(status, error) {
    var root = global.parent && global.parent !== global ? global.parent.document.documentElement : document.documentElement;
    root.setAttribute("data-kit-test", status);
    if (error) root.setAttribute("data-kit-test-error", String(error && error.message || error));
  }
  function assert(condition, message) {
    if (!condition) throw new Error(message);
  }
  function waitFor(predicate, message) {
    return new Promise(function (resolve, reject) {
      var deadline = performance.now() + 4000;
      function poll() {
        try {
          if (predicate()) { resolve(); return; }
          if (performance.now() >= deadline) { reject(new Error(message)); return; }
          setTimeout(poll, 8);
        } catch (error) { reject(error); }
      }
      poll();
    });
  }
  function ready(run) {
    if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", run, { once: true });
    else run();
  }

  if (location.pathname === "/script/changed") {
    ready(function () {
      setTimeout(function () {
        try {
          var before = JSON.parse(sessionStorage.getItem("driveBeforeNative") || "null");
          assert(document.cookie.indexOf("drive_changed_full=1") >= 0, "changed script did not use native full navigation");
          assert(global.__driveScriptContractRuns === 1, "shared contract script did not run exactly once after full navigation");
          assert(global.__driveScriptHelperRuns === 1, "shared helper did not run exactly once after full navigation");
          assert(global.__driveScriptChangedRuns === 1 && sessionStorage.getItem("driveChangedExternalRuns") === "1",
            "changed external script did not run exactly once on the full document");
          assert(sessionStorage.getItem("driveChangedInlineRuns") === "1",
            "changed inline script did not run exactly once on the full document");
          assert(before && before.path === "/script/shared" && before.title === "Script shared" && before.route === "Shared",
            "Drive mutated title, history URL, or body before native fallback");
          assert(before.inlineRuns === null && before.externalRuns === null,
            "a fetched script executed before native fallback");
          assert(document.title === "Script changed full" && document.getElementById("script-route").textContent.trim() === "Changed full",
            "native full document did not commit normally");
          var unsignedTitle = document.title;
          var unsignedRoute = document.getElementById("script-route").textContent.trim();
          var unsignedPath = location.pathname;
          var unsignedHistoryLength = history.length;
          document.getElementById("unsigned-current-link").click();
          waitFor(function () {
            return document.cookie.indexOf("drive_unsigned_current_fallback=1") >= 0;
          }, "unsigned current Hydrate profile did not hard-navigate").then(function () {
            assert(document.title === unsignedTitle &&
              document.getElementById("script-route").textContent.trim() === unsignedRoute &&
              location.pathname === unsignedPath && history.length === unsignedHistoryLength,
              "unsigned current Hydrate profile mutated the page before fallback");
            assert(global.__driveScriptContractRuns === 1 && global.__driveScriptHelperRuns === 1,
              "unsigned current Hydrate profile reran a shared script");
            finish("passed");
          }).catch(function (error) { finish("failed", error); });
        } catch (error) { finish("failed", error); }
      }, 0);
    });
    return;
  }

  ready(function () {
    setTimeout(function () {
      Promise.resolve().then(async function () {
        assert(location.pathname === "/script/start", "script test did not start on its initial document");
        assert(global.__driveScriptContractRuns === 1 && global.__driveScriptHelperRuns === 1,
          "initial shared scripts did not run exactly once");
        ["driveOnlyInlineRuns", "driveOnlyExternalRuns", "driveOnlyModuleRuns",
          "driveChangedInlineRuns", "driveChangedExternalRuns", "driveBeforeNative"].forEach(function (name) {
          sessionStorage.removeItem(name);
        });

        document.getElementById("shared-link").click();
        await waitFor(function () {
          return location.pathname === "/script/shared" &&
            document.getElementById("script-route").textContent.trim() === "Shared";
        }, "identical stable shared scripts did not allow Drive Morph");
        assert(global.__driveScriptContractRuns === 1 && global.__driveScriptHelperRuns === 1,
          "identical shared external head scripts were executed again");
        assert(document.title === "Script shared", "shared Morph did not reconcile title");

        async function expectFallback(link, cookie, label) {
          var title = document.title;
          var route = document.getElementById("script-route").textContent.trim();
          var path = location.pathname + location.search + location.hash;
          var historyLength = history.length;
          document.getElementById(link).click();
          await waitFor(function () { return document.cookie.indexOf(cookie + "=1") >= 0; }, label + " did not hard navigate");
          assert(document.title === title && document.getElementById("script-route").textContent.trim() === route,
            label + " mutated title or body before fallback");
          assert(location.pathname + location.search + location.hash === path && history.length === historyLength,
            label + " mutated history before fallback");
          assert(global.__driveScriptContractRuns === 1 && global.__driveScriptHelperRuns === 1,
            label + " reran a current shared script");
        }

        await expectFallback("inline-link", "drive_inline_fallback", "inline script");
        assert(sessionStorage.getItem("driveOnlyInlineRuns") === null, "fetched inline script executed");
        await expectFallback("external-link", "drive_external_fallback", "external script addition");
        assert(sessionStorage.getItem("driveOnlyExternalRuns") === null, "fetched external script executed");
        await expectFallback("removed-link", "drive_removed_fallback", "external script removal");
        await expectFallback("order-link", "drive_order_fallback", "external script reorder");
        await expectFallback("attribute-link", "drive_attribute_fallback", "external script attribute change");
        await expectFallback("unsigned-link", "drive_unsigned_fallback", "unsigned external script");
        await expectFallback("invalid-integrity-link", "drive_invalid_integrity_fallback", "invalid script integrity");
        await expectFallback("module-link", "drive_module_fallback", "module script");
        assert(sessionStorage.getItem("driveOnlyModuleRuns") === null, "fetched module script executed");
        await expectFallback("importmap-link", "drive_importmap_fallback", "import map");
        await expectFallback("profile-async-link", "drive_profile_async_fallback", "async profile script");
        await expectFallback("profile-integrity-link", "drive_profile_integrity_fallback", "changed profile integrity");
        await expectFallback("profile-unsigned-link", "drive_profile_unsigned_fallback", "unsigned profile script");
        await expectFallback("profile-body-link", "drive_profile_body_fallback", "body profile script");
        await expectFallback("profile-attribute-link", "drive_profile_attribute_fallback", "profile attribute change");
        await expectFallback("cross-slot-link", "drive_cross_slot_fallback", "authored script cross-slot move");

        global.addEventListener("pagehide", function () {
          sessionStorage.setItem("driveBeforeNative", JSON.stringify({
            path: location.pathname,
            title: document.title,
            route: document.getElementById("script-route").textContent.trim(),
            inlineRuns: sessionStorage.getItem("driveChangedInlineRuns"),
            externalRuns: sessionStorage.getItem("driveChangedExternalRuns")
          }));
        }, { once: true });
        document.getElementById("changed-link").click();
      }).catch(function (error) { finish("failed", error); });
    }, 0);
  });
})(globalThis, document);`
