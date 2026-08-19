package javascript

import (
	"bytes"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type stagedDriveRaceAsset struct {
	source   []byte
	requests atomic.Int64
}

// TestStagedDriveSupersededComponentHandoff proves that a marked component
// script from a cancelled visit cannot register into, abort, or otherwise
// poison the next visit's transaction. The fixture deterministically holds the
// old node outside the document, starts a newer visit, then evaluates the old
// node while the newer graph node is the only accepted handoff script.
func TestStagedDriveSupersededComponentHandoff(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping superseded staged component-handoff browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	base := ComponentPackage{
		Name: "race-base", Version: "1.0.0",
		Source: []byte(`; globalThis.__handoffRaceRuns.base++;
kit.component("race-base", { ready: "base-ready" });
`),
	}
	slow := ComponentPackage{
		Name: "race-slow", Version: "1.0.0",
		Source: []byte(`; globalThis.__handoffRaceRuns.slow++;
kit.component("race-slow", { ready: "slow-ready" });
`),
	}
	fast := ComponentPackage{
		Name: "race-fast", Version: "1.0.0",
		Source: []byte(`; globalThis.__handoffRaceRuns.fast++;
kit.component("race-fast", { ready: "fast-ready" });
`),
	}

	initial, err := BuildStaged(StagedBuildOptions{
		Profile: ProfileHydrate, Components: []ComponentPackage{base},
	})
	if err != nil {
		t.Fatal(err)
	}
	slowTarget, err := BuildStaged(StagedBuildOptions{
		Profile: ProfileHydrate, Components: []ComponentPackage{base, slow},
	})
	if err != nil {
		t.Fatal(err)
	}
	fastTarget, err := BuildStaged(StagedBuildOptions{
		Profile: ProfileHydrate, Components: []ComponentPackage{base, fast},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, assembly := range []StagedAssembly{slowTarget, fastTarget} {
		if initial.Runtime.SHA256() != assembly.Runtime.SHA256() || initial.Hydrate == nil ||
			assembly.Hydrate == nil || initial.Hydrate.SHA256() != assembly.Hydrate.SHA256() {
			t.Fatal("race fixture changed the stable runtime or Hydrate asset")
		}
	}

	assets := make(map[string]*stagedDriveRaceAsset)
	for _, assembly := range []StagedAssembly{initial, slowTarget, fastTarget} {
		for _, artifact := range assembly.Artifacts() {
			path := "/jit/" + artifact.Name()
			if prior, exists := assets[path]; exists {
				if !bytes.Equal(prior.source, artifact.Bytes()) {
					t.Fatalf("content-addressed fixture collision at %s", path)
				}
				continue
			}
			assets[path] = &stagedDriveRaceAsset{source: artifact.Bytes()}
		}
	}
	slowComponent := stagedDriveArtifact(t, slowTarget, JITRoleComponent, "race-slow")
	fastComponent := stagedDriveArtifact(t, fastTarget, JITRoleComponent, "race-fast")
	contractSource := []byte(stagedDriveRaceContractSource(slowComponent.SHA256(), fastTarget.Graph.SHA256()))
	contractIntegrity := driveScriptIntegrity(contractSource)

	var slowDrive atomic.Int64
	var fastDrive atomic.Int64
	var slowFull atomic.Int64
	var fastFull atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/staged-drive-race-contract.js" {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(contractSource)
			return
		}
		if asset, exists := assets[request.URL.Path]; exists {
			asset.requests.Add(1)
			// The stale component must evaluate while the newer graph node is the
			// active bridge expectation, not after the newer transaction closes.
			if request.URL.Path == "/jit/"+fastTarget.Graph.Name() {
				time.Sleep(180 * time.Millisecond)
			}
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			response.Header().Set("X-Content-Type-Options", "nosniff")
			_, _ = response.Write(asset.source)
			return
		}

		drive := request.Header.Get("X-KitJS-Drive") == "1"
		switch request.URL.Path {
		case "/":
			writeStagedDriveHTML(response, stagedDriveRaceDocument(
				initial, "initial", contractIntegrity))
		case "/slow":
			if drive {
				slowDrive.Add(1)
				writeStagedDriveHTML(response, stagedDriveRaceDocument(
					slowTarget, "slow", contractIntegrity))
				return
			}
			slowFull.Add(1)
			response.Header().Set("Set-Cookie", "staged_handoff_race_slow_full=1; Path=/; SameSite=Lax")
			response.WriteHeader(http.StatusNoContent)
		case "/fast":
			if drive {
				fastDrive.Add(1)
				writeStagedDriveHTML(response, stagedDriveRaceDocument(
					fastTarget, "fast", contractIntegrity))
				return
			}
			fastFull.Add(1)
			response.Header().Set("Set-Cookie", "staged_handoff_race_fast_full=1; Path=/; SameSite=Lax")
			response.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/")

	if got := slowDrive.Load(); got != 1 {
		t.Fatalf("slow Drive requests = %d, want 1", got)
	}
	if got := fastDrive.Load(); got != 1 {
		t.Fatalf("fast Drive requests = %d, want 1", got)
	}
	if got := slowFull.Load(); got != 0 {
		t.Fatalf("slow hard navigations = %d, want 0", got)
	}
	if got := fastFull.Load(); got != 0 {
		t.Fatalf("fast hard navigations = %d, want 0", got)
	}
	if got := assets["/jit/"+slowTarget.Graph.Name()].requests.Load(); got != 1 {
		t.Fatalf("slow graph requests = %d, want 1", got)
	}
	if got := assets["/jit/"+slowComponent.Name()].requests.Load(); got != 1 {
		t.Fatalf("late slow component requests = %d, want 1", got)
	}
	if got := assets["/jit/"+fastTarget.Graph.Name()].requests.Load(); got != 1 {
		t.Fatalf("fast graph requests = %d, want 1", got)
	}
	if got := assets["/jit/"+fastComponent.Name()].requests.Load(); got != 1 {
		t.Fatalf("fast component requests = %d, want 1", got)
	}
}

func stagedDriveRaceDocument(
	assembly StagedAssembly,
	route string,
	contractIntegrity string,
) string {
	var output strings.Builder
	output.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8">`)
	output.WriteString(`<meta http-equiv="Content-Security-Policy" content="default-src 'self'; script-src 'self' 'unsafe-inline'">`)
	output.WriteString(`<title>Handoff race ` + html.EscapeString(route) + `</title>`)
	output.WriteString(`<script defer src="/staged-drive-race-contract.js" integrity="` +
		html.EscapeString(contractIntegrity) + `" crossorigin="anonymous"></script>`)
	for _, artifact := range assembly.Artifacts() {
		output.WriteString(`<script data-kitwork-jit="` + html.EscapeString(string(artifact.Role())) +
			`" data-kitwork-hash="` + artifact.SHA256() + `" src="/jit/` + html.EscapeString(artifact.Name()) +
			`" integrity="` + html.EscapeString(artifact.Integrity()) +
			`" crossorigin="anonymous" defer></script>` + "\n")
	}
	output.WriteString(`</head><body><nav>
<a id="to-slow" href="/slow">Slow</a>
<a id="to-fast" href="/fast">Fast</a>
</nav><main id="race-route">` + html.EscapeString(route) + `</main>
<section data-kit-component="race-base" data-kit-version="1.0.0">
  <output id="race-base" data-kit-text="ready">server-base</output>
</section>`)
	if route == "slow" {
		output.WriteString(`<section data-kit-component="race-slow" data-kit-version="1.0.0">
  <output id="race-slow" data-kit-text="ready">server-slow</output>
</section>`)
	}
	if route == "fast" {
		output.WriteString(`<section data-kit-component="race-fast" data-kit-version="1.0.0">
  <output id="race-fast" data-kit-text="ready">server-fast</output>
</section>`)
	}
	output.WriteString(`</body></html>`)
	return output.String()
}

func stagedDriveRaceContractSource(slowComponentHash, fastGraphHash string) string {
	return `(function (global, document) {
  "use strict";
  global.__handoffRaceRuns = { base: 0, slow: 0, fast: 0 };
  global.__handoffRaceError = "";
  global.__handoffRaceHeldNode = null;
  global.addEventListener("error", function (event) {
    if (!global.__handoffRaceError) {
      global.__handoffRaceError = String(event.error && event.error.message || event.message || "script error");
    }
  });
  var append = document.head.appendChild;
  document.head.appendChild = function (node) {
    if (node && node.localName === "script" && node.hasAttribute("data-kitwork-handoff") &&
      node.getAttribute("data-kitwork-hash") === "` + slowComponentHash + `" &&
      !global.__handoffRaceHeldNode) {
      global.__handoffRaceHeldNode = node;
      global.setTimeout(function () { document.getElementById("to-fast").click(); }, 0);
      return node;
    }
    if (node && node.localName === "script" && node.hasAttribute("data-kitwork-handoff") &&
      node.getAttribute("data-kitwork-hash") === "` + fastGraphHash + `" &&
      global.__handoffRaceHeldNode) {
      var stale = global.__handoffRaceHeldNode;
      global.__handoffRaceHeldNode = null;
      append.call(this, stale);
    }
    return append.call(this, node);
  };
})(globalThis, document);
` + browserHarness + `
__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  await waitFor(function () {
    return globalThis.kit && document.getElementById("race-base").textContent === "base-ready";
  }, "initial race component did not boot");
  var kitObject = globalThis.kit;
  var root = document.documentElement;
  var body = document.body;
  document.getElementById("to-slow").click();
  await waitFor(function () {
    return location.pathname === "/fast" && document.getElementById("race-route").textContent === "fast" &&
      document.getElementById("race-fast") && document.getElementById("race-fast").textContent === "fast-ready";
  }, "newer component handoff did not survive a stale marked script");
  assert(globalThis.__handoffRaceError === "", "stale marked script raised " + globalThis.__handoffRaceError);
  assert(globalThis.__handoffRaceRuns.base === 1 && globalThis.__handoffRaceRuns.slow === 0 &&
    globalThis.__handoffRaceRuns.fast === 1, "stale component registered or stable component reran");
  assert(globalThis.kit === kitObject && document.documentElement === root && document.body === body,
    "superseded handoff replaced runtime or document roots");
  assert(document.cookie.indexOf("staged_handoff_race_") < 0,
    "superseded handoff fell back to a hard navigation");
});`
}
