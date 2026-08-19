package javascript

import (
	"bytes"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type driveReentrantAsset struct {
	source   []byte
	requests atomic.Int64
}

// TestStagedDriveDefersReentrantNavigation proves that navigation event and
// component cleanup callbacks cannot interleave a newer visit with the
// synchronous setup or Morph commit of the visit that invoked them.
func TestStagedDriveDefersReentrantNavigation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping reentrant staged Drive browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	base := ComponentPackage{
		Name: "drive-atomic-base", Version: "1.0.0",
		Source: []byte(`; kit.component("drive-atomic-base", { ready: "base-ready" });
`),
	}
	cleanup := ComponentPackage{
		Name: "drive-atomic-cleanup", Version: "1.0.0",
		Source: []byte(`; kit.component("drive-atomic-cleanup", {
  init: function () {
    return function () {
      globalThis.__driveAtomicCleanupRuns++;
      document.getElementById("to-fragment").click();
      document.getElementById("to-c").click();
      document.getElementById("to-d").click();
    };
  }
});
`),
	}
	initial, err := BuildStaged(StagedBuildOptions{
		Profile: ProfileHydrate, Components: []ComponentPackage{base, cleanup},
	})
	if err != nil {
		t.Fatal(err)
	}
	target, err := BuildStaged(StagedBuildOptions{
		Profile: ProfileHydrate, Components: []ComponentPackage{base},
	})
	if err != nil {
		t.Fatal(err)
	}
	if initial.Runtime.SHA256() != target.Runtime.SHA256() || initial.Hydrate == nil ||
		target.Hydrate == nil || initial.Hydrate.SHA256() != target.Hydrate.SHA256() {
		t.Fatal("reentrant fixture changed the stable runtime or Hydrate asset")
	}

	assets := make(map[string]*driveReentrantAsset)
	for _, assembly := range []StagedAssembly{initial, target} {
		for _, artifact := range assembly.Artifacts() {
			path := "/jit/" + artifact.Name()
			if prior, exists := assets[path]; exists {
				if !bytes.Equal(prior.source, artifact.Bytes()) {
					t.Fatalf("content-addressed fixture collision at %s", path)
				}
				continue
			}
			assets[path] = &driveReentrantAsset{source: artifact.Bytes()}
		}
	}

	contractSource := []byte(driveReentrantContractSource)
	contractIntegrity := driveScriptIntegrity(contractSource)
	var aFull atomic.Int64
	var bDrive atomic.Int64
	var bFull atomic.Int64
	var cDrive atomic.Int64
	var cFull atomic.Int64
	var dDrive atomic.Int64
	var dFull atomic.Int64
	var slowDrive atomic.Int64
	var slowFull atomic.Int64
	var outerDrive atomic.Int64
	var outerFull atomic.Int64
	var winnerDrive atomic.Int64
	var winnerFull atomic.Int64
	var pagehideDrive atomic.Int64
	var pagehideFull atomic.Int64
	var fallbackDrive atomic.Int64
	var fallbackFull atomic.Int64
	var forbiddenDrive atomic.Int64
	var forbiddenFull atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/drive-reentrant-contract.js" {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(contractSource)
			return
		}
		if asset, exists := assets[request.URL.Path]; exists {
			asset.requests.Add(1)
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			response.Header().Set("X-Content-Type-Options", "nosniff")
			_, _ = response.Write(asset.source)
			return
		}

		drive := request.Header.Get("X-KitJS-Drive") == "1"
		serve := func(counter *atomic.Int64, assembly StagedAssembly, route string) {
			counter.Add(1)
			writeStagedDriveHTML(response, driveReentrantDocument(
				assembly, route, contractIntegrity, false))
		}
		switch request.URL.Path {
		case "/a":
			if drive {
				http.Error(response, "unexpected Drive request", http.StatusInternalServerError)
				return
			}
			serve(&aFull, initial, "a")
		case "/b":
			if drive {
				serve(&bDrive, target, "b")
				return
			}
			bFull.Add(1)
			response.WriteHeader(http.StatusNoContent)
		case "/c":
			if drive {
				serve(&cDrive, target, "c")
				return
			}
			cFull.Add(1)
			response.WriteHeader(http.StatusNoContent)
		case "/d":
			if drive {
				serve(&dDrive, target, "d")
				return
			}
			dFull.Add(1)
			response.WriteHeader(http.StatusNoContent)
		case "/slow":
			if drive {
				serve(&slowDrive, target, "slow")
				return
			}
			slowFull.Add(1)
			response.WriteHeader(http.StatusNoContent)
		case "/outer":
			if drive {
				serve(&outerDrive, target, "outer")
				return
			}
			outerFull.Add(1)
			response.WriteHeader(http.StatusNoContent)
		case "/winner":
			if drive {
				serve(&winnerDrive, target, "winner")
				return
			}
			winnerFull.Add(1)
			response.WriteHeader(http.StatusNoContent)
		case "/pagehide-slow":
			if drive {
				serve(&pagehideDrive, target, "pagehide-slow")
				return
			}
			pagehideFull.Add(1)
			response.WriteHeader(http.StatusNoContent)
		case "/fallback":
			if drive {
				fallbackDrive.Add(1)
				writeStagedDriveHTML(response, driveReentrantDocument(
					target, "fallback", contractIntegrity, true))
				return
			}
			fallbackFull.Add(1)
			response.Header().Set("Set-Cookie", "drive_atomic_fallback=1; Path=/; SameSite=Lax")
			response.WriteHeader(http.StatusNoContent)
		case "/forbidden":
			if drive {
				forbiddenDrive.Add(1)
			} else {
				forbiddenFull.Add(1)
			}
			response.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/a")

	for _, expectation := range []struct {
		name string
		got  int64
		want int64
	}{
		{name: "initial full", got: aFull.Load(), want: 1},
		{name: "B Drive", got: bDrive.Load(), want: 1},
		{name: "B full", got: bFull.Load(), want: 0},
		{name: "superseded C Drive", got: cDrive.Load(), want: 0},
		{name: "superseded C full", got: cFull.Load(), want: 0},
		{name: "latest D Drive", got: dDrive.Load(), want: 1},
		{name: "D full", got: dFull.Load(), want: 0},
		{name: "slow full", got: slowFull.Load(), want: 0},
		{name: "outer Drive", got: outerDrive.Load(), want: 0},
		{name: "outer full", got: outerFull.Load(), want: 0},
		{name: "winner Drive", got: winnerDrive.Load(), want: 1},
		{name: "winner full", got: winnerFull.Load(), want: 0},
		{name: "pagehide full", got: pagehideFull.Load(), want: 0},
		{name: "fallback Drive", got: fallbackDrive.Load(), want: 1},
		{name: "fallback full", got: fallbackFull.Load(), want: 1},
		{name: "forbidden Drive", got: forbiddenDrive.Load(), want: 0},
		{name: "forbidden full", got: forbiddenFull.Load(), want: 0},
	} {
		if expectation.got != expectation.want {
			t.Errorf("%s requests = %d, want %d", expectation.name, expectation.got, expectation.want)
		}
	}
	if got := slowDrive.Load(); got > 1 {
		t.Errorf("slow Drive requests = %d, want at most the aborted request", got)
	}
	if got := pagehideDrive.Load(); got > 1 {
		t.Errorf("pagehide Drive requests = %d, want at most the aborted request", got)
	}
}

func driveReentrantDocument(
	assembly StagedAssembly,
	route string,
	contractIntegrity string,
	incompatibleInline bool,
) string {
	var output strings.Builder
	output.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8">`)
	output.WriteString(`<meta http-equiv="Content-Security-Policy" content="default-src 'self'; script-src 'self'">`)
	output.WriteString(`<title>Atomic ` + html.EscapeString(route) + `</title>`)
	output.WriteString(`<script defer src="/drive-reentrant-contract.js" integrity="` +
		html.EscapeString(contractIntegrity) + `" crossorigin="anonymous"></script>`)
	for _, artifact := range assembly.Artifacts() {
		output.WriteString(`<script data-kitwork-jit="` + html.EscapeString(string(artifact.Role())) +
			`" data-kitwork-hash="` + artifact.SHA256() + `" src="/jit/` + html.EscapeString(artifact.Name()) +
			`" integrity="` + html.EscapeString(artifact.Integrity()) +
			`" crossorigin="anonymous" defer></script>` + "\n")
	}
	output.WriteString(`</head><body><nav>
<a id="to-fragment" href="/b#atomic-target">Fragment</a>
<a id="to-b" href="/b">B</a>
<a id="to-c" href="/c">C</a>
<a id="to-d" href="/d">D</a>
<a id="to-slow" href="/slow">Slow</a>
<a id="to-outer" href="/outer">Outer</a>
<a id="to-winner" href="/winner">Winner</a>
<a id="to-pagehide" href="/pagehide-slow">Pagehide</a>
<a id="to-fallback" href="/fallback">Fallback</a>
<a id="to-forbidden" href="/forbidden">Forbidden</a>
</nav><main id="atomic-route">` + html.EscapeString(route) + `</main>
<div id="atomic-target">Target</div>
<section data-kit-component="drive-atomic-base" data-kit-version="1.0.0">
  <output id="atomic-base" data-kit-text="ready">server-base</output>
</section>`)
	if route == "a" {
		output.WriteString(`<section id="atomic-cleanup" data-kit-component="drive-atomic-cleanup" data-kit-version="1.0.0"></section>`)
	}
	if incompatibleInline {
		output.WriteString(`<script>globalThis.__incomingFallbackScriptMustNotRun = true;</script>`)
	}
	output.WriteString(`</body></html>`)
	return output.String()
}

const driveReentrantContractSource = `(function (global, document) {
  "use strict";
  global.__driveAtomicCleanupRuns = 0;
  global.__driveAtomicEvents = [];
  global.__driveAtomicArmCancelled = false;
  global.__driveAtomicArmPagehide = false;
  global.__driveAtomicArmFallback = false;
  document.addEventListener("kit:navigation", function (event) {
    var detail = event.detail;
    var path = new URL(detail.url, location.href).pathname;
    var route = document.getElementById("atomic-route");
    global.__driveAtomicEvents.push({
      phase: detail.phase,
      path: path,
      outcome: detail.outcome || "",
      livePath: location.pathname,
      title: document.title,
      route: route ? route.textContent : ""
    });
    if (global.__driveAtomicArmCancelled && detail.phase === "finish" &&
      path === "/slow" && detail.outcome === "cancelled") {
      global.__driveAtomicArmCancelled = false;
      document.getElementById("to-winner").click();
    }
    if (global.__driveAtomicArmFallback && detail.phase === "finish" &&
      path === "/fallback" && detail.outcome === "fallback") {
      global.__driveAtomicArmFallback = false;
      document.getElementById("to-forbidden").click();
    }
    if (global.__driveAtomicArmPagehide && detail.phase === "finish" &&
      path === "/pagehide-slow" && detail.outcome === "cancelled") {
      global.__driveAtomicArmPagehide = false;
      document.getElementById("to-forbidden").click();
    }
  });
})(globalThis, document);
` + browserHarness + `
__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var nextTurn = __kitTestNextTurn;
  function signature(event) {
    return event.phase + ":" + event.path + (event.outcome ? ":" + event.outcome : "");
  }
  function signatures() {
    return globalThis.__driveAtomicEvents.map(signature).join(",");
  }

  await waitFor(function () {
    return globalThis.kit && document.getElementById("atomic-base").textContent === "base-ready";
  }, "initial reentrant fixture did not boot");
  var root = document.documentElement;
  var body = document.body;

  document.getElementById("to-b").click();
  await waitFor(function () {
    return location.pathname === "/d" && document.getElementById("atomic-route").textContent === "d";
  }, "latest cleanup navigation did not load after B committed");
  assert(globalThis.__driveAtomicCleanupRuns === 1, "cleanup did not run exactly once");
  assert(signatures() === "start:/b,finish:/b:loaded,start:/d,finish:/d:loaded",
    "cleanup navigation events were " + signatures());
  assert(globalThis.__driveAtomicEvents[1].livePath === "/b" &&
    globalThis.__driveAtomicEvents[1].title === "Atomic b" &&
    globalThis.__driveAtomicEvents[1].route === "b",
    "B finish was emitted before its DOM, title, and history commit");
  assert(globalThis.__driveAtomicEvents[2].livePath === "/b" &&
    globalThis.__driveAtomicEvents[2].title === "Atomic b" &&
    globalThis.__driveAtomicEvents[2].route === "b",
    "latest cleanup visit started against a transient pre-B document");
  assert(document.documentElement === root && document.body === body,
    "cleanup reentrancy replaced the document root");

  globalThis.__driveAtomicEvents = [];
  globalThis.__driveAtomicArmCancelled = true;
  document.getElementById("to-slow").click();
  document.getElementById("to-outer").click();
  await waitFor(function () {
    return location.pathname === "/winner" && document.getElementById("atomic-route").textContent === "winner";
  }, "finish(cancelled) listener did not win without a ghost visit");
  assert(signatures() === "start:/slow,finish:/slow:cancelled,start:/outer,finish:/outer:cancelled,start:/winner,finish:/winner:loaded",
    "cancel/setup navigation events were " + signatures());
  assert(globalThis.__driveAtomicEvents.every(function (event) {
    return event.outcome !== "error" && event.outcome !== "fallback";
  }), "cancel/setup race emitted a false terminal outcome");

  globalThis.__driveAtomicEvents = [];
  globalThis.__driveAtomicArmPagehide = true;
  document.getElementById("to-pagehide").click();
  globalThis.dispatchEvent(new Event("pagehide"));
  await nextTurn();
  assert(signatures() === "start:/pagehide-slow,finish:/pagehide-slow:cancelled",
    "pagehide leaked a queued finish-listener visit: " + signatures());

  globalThis.__driveAtomicEvents = [];
  globalThis.__driveAtomicArmFallback = true;
  document.getElementById("to-fallback").click();
  await waitFor(function () {
    return document.cookie.indexOf("drive_atomic_fallback=1") >= 0;
  }, "incompatible page did not hard-navigate");
  await nextTurn();
  assert(signatures() === "start:/fallback,finish:/fallback:fallback",
    "terminal fallback leaked a queued visit: " + signatures());
  assert(globalThis.__incomingFallbackScriptMustNotRun === undefined,
    "fetched fallback script executed before native navigation");
  assert(location.pathname === "/winner" && document.getElementById("atomic-route").textContent === "winner",
    "fallback mutated the live URL or body before the native load");
});`
