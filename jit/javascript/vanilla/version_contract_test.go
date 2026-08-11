package vanilla

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestVersionedArtifactIsDeterministicImmutableAndExact(t *testing.T) {
	alpha := Script{
		Name: "alpha",
		Source: []byte(`;kit.component("alpha", { value: "alpha" });
`),
	}
	zeta := Script{
		Name: "zeta",
		Source: []byte(`;kit.component("zeta", { value: "zeta" });
`),
	}
	leftOptions := BuildOptions{
		Profile: ProfileHydrate,
		Components: []ComponentVersion{
			{Name: "zeta", Version: "2.0.0"},
			{Name: "alpha", Version: "1.2.3-rc.1+build.7"},
		},
		Scripts: []Script{zeta, alpha},
	}
	rightOptions := BuildOptions{
		Profile: ProfileHydrate,
		Components: []ComponentVersion{
			{Name: "alpha", Version: "1.2.3-rc.1+build.7"},
			{Name: "zeta", Version: "2.0.0"},
		},
		Scripts: []Script{alpha, zeta},
	}

	left, err := Build(leftOptions)
	if err != nil {
		t.Fatal(err)
	}
	right, err := Build(rightOptions)
	if err != nil {
		t.Fatal(err)
	}
	leftBytes := left.Bytes()
	if !bytes.Equal(leftBytes, right.Bytes()) || left.Name() != right.Name() || left.SHA256() != right.SHA256() {
		t.Fatal("equivalent component graphs produced different artifacts")
	}
	if left.Profile() != ProfileHydrate || left.Release() != ReleaseVersion || left.Size() != len(leftBytes) {
		t.Fatalf("artifact metadata = profile %q release %q size %d", left.Profile(), left.Release(), left.Size())
	}
	if left.SHA256() != ContentHash(leftBytes) {
		t.Fatal("artifact SHA-256 does not identify its exact bytes")
	}
	namePattern := regexp.MustCompile(`^hydrate\.kit\.` + regexp.QuoteMeta(ReleaseVersion) + `\.[0-9a-f]{64}\.js$`)
	if !namePattern.MatchString(left.Name()) || !strings.Contains(left.Name(), left.SHA256()) {
		t.Fatalf("immutable artifact name = %q", left.Name())
	}

	oldBytes := append([]byte(nil), leftBytes...)
	changed, err := Build(BuildOptions{
		Profile: ProfileHydrate,
		Components: []ComponentVersion{
			{Name: "alpha", Version: "1.2.4"},
			{Name: "zeta", Version: "2.0.0"},
		},
		Scripts: []Script{alpha, zeta},
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed.Name() == left.Name() || changed.SHA256() == left.SHA256() || bytes.Equal(changed.Bytes(), oldBytes) {
		t.Fatal("changing only a component pin did not create a new immutable artifact")
	}
	if !bytes.Equal(left.Bytes(), oldBytes) {
		t.Fatal("building a new release changed the retained old artifact")
	}

	// Neither a returned byte slice nor caller-owned source may rewrite an
	// already identified artifact.
	leftBytes[0] ^= 0xff
	leftOptions.Scripts[0].Source[0] ^= 0xff
	if !bytes.Equal(left.Bytes(), oldBytes) {
		t.Fatal("artifact bytes changed through a caller-owned slice")
	}
}

func TestVersionedArtifactRejectsAmbiguousGraphs(t *testing.T) {
	validScript := Script{Name: "counter", Source: []byte(";kit.component(\"counter\", {});\n")}
	tests := []struct {
		name    string
		options BuildOptions
	}{
		{
			name: "inline component version",
			options: BuildOptions{Profile: ProfileKit,
				Components: []ComponentVersion{{Name: "counter@1.0.0", Version: "1.0.0"}}},
		},
		{
			name: "missing graph version",
			options: BuildOptions{Profile: ProfileKit,
				Components: []ComponentVersion{{Name: "counter", Version: ""}}},
		},
		{
			name: "version range",
			options: BuildOptions{Profile: ProfileKit,
				Components: []ComponentVersion{{Name: "counter", Version: "^1.0.0"}}},
		},
		{
			name: "v prefix",
			options: BuildOptions{Profile: ProfileKit,
				Components: []ComponentVersion{{Name: "counter", Version: "v1.0.0"}}},
		},
		{
			name: "duplicate same component",
			options: BuildOptions{Profile: ProfileKit, Components: []ComponentVersion{
				{Name: "counter", Version: "1.0.0"},
				{Name: "counter", Version: "1.0.0"},
			}},
		},
		{
			name: "two versions of component",
			options: BuildOptions{Profile: ProfileKit, Components: []ComponentVersion{
				{Name: "counter", Version: "1.0.0"},
				{Name: "counter", Version: "2.0.0"},
			}},
		},
		{
			name: "duplicate script identity",
			options: BuildOptions{Profile: ProfileKit, Scripts: []Script{
				validScript,
				validScript,
			}},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			if _, err := Build(test.options); err == nil {
				t.Fatal("ambiguous graph was accepted")
			}
		})
	}
	if _, err := Build(BuildOptions{Profile: Profile("unknown")}); err == nil {
		t.Fatal("unknown runtime profile was accepted")
	}
}

func TestBrowserVersionedComponentHandshake(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping component-version browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	oldArtifact := buildVersionFixtureArtifact(t, ProfileKit, "version-counter", "1.0.0", "old")
	newArtifact := buildVersionFixtureArtifact(t, ProfileKit, "version-counter", "2.0.0", "new")
	guardArtifact := buildVersionFixtureArtifact(t, ProfileKit, "graph-guard-counter", "1.0.0", "guard")
	differentArtifact := buildVersionFixtureArtifact(t, ProfileKit, "different-graph-counter", "1.0.0", "different")
	kitJS, err := Source()
	if err != nil {
		t.Fatal(err)
	}

	assets := map[string][]byte{
		"/assets/" + oldArtifact.Name():       oldArtifact.Bytes(),
		"/assets/" + newArtifact.Name():       newArtifact.Bytes(),
		"/assets/" + guardArtifact.Name():     guardArtifact.Bytes(),
		"/assets/" + differentArtifact.Name(): differentArtifact.Bytes(),
		"/kit.js":                             kitJS,
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if source, exists := assets[request.URL.Path]; exists {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(source)
			return
		}
		switch request.URL.Path {
		case "/old.html":
			writeVersionContractHTML(response, versionHandshakeDocument(oldArtifact, true, "old", "1.0.0"))
		case "/new.html":
			writeVersionContractHTML(response, versionHandshakeDocument(newArtifact, false, "new", "2.0.0"))
		case "/local.html":
			writeVersionContractHTML(response, versionLocalDocument)
		case "/guard.html":
			writeVersionContractHTML(response, versionGraphGuardDocument(guardArtifact, differentArtifact))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	for _, route := range []string{"old.html", "new.html", "local.html", "guard.html"} {
		route := route
		t.Run(strings.TrimSuffix(route, ".html"), func(t *testing.T) {
			runVanillaBrowser(t, browser, server.URL+"/"+route)
		})
	}
}

func versionGraphGuardDocument(installed, different Artifact) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>Component graph guard</title></head>
<body>
  <section data-kit-component="graph-guard-counter" data-kit-version="1.0.0">
    <output id="graph-guard-output" data-kit-text="label">guard-server</output>
  </section>
  <section data-kit-component="different-graph-counter" data-kit-version="1.0.0">
    <output id="different-graph-output" data-kit-text="label">different-server</output>
  </section>
  <script>
    (function () {
      var original = EventTarget.prototype.addEventListener;
      globalThis.__graphGuardListeners = { click: 0, keydown: 0 };
      EventTarget.prototype.addEventListener = function (type, listener, options) {
        if (this === document && (type === "click" || type === "keydown")) {
          globalThis.__graphGuardListeners[type]++;
        }
        return original.call(this, type, listener, options);
      };
      globalThis.__restoreGraphGuardListenerSpy = function () {
        EventTarget.prototype.addEventListener = original;
        delete globalThis.__restoreGraphGuardListenerSpy;
      };
      globalThis.__graphGuardErrors = [];
      window.addEventListener("error", function (event) {
        var message = String(event.error && event.error.message || event.message || "");
        if (message.indexOf("installed component graph does not match this artifact") >= 0) {
          globalThis.__graphGuardErrors.push(message);
          event.preventDefault();
          return;
        }
        globalThis.__graphGuardUnexpectedError = message;
      });
    })();
  </script>
  <script src="/assets/%s"></script>
  <script>globalThis.__graphGuardFirstKit = globalThis.kit;</script>
  <script src="/assets/%s"></script>
  <script>globalThis.__graphGuardSameKit = globalThis.kit === globalThis.__graphGuardFirstKit;</script>
  <script src="/assets/%s"></script>
  <script>
    globalThis.__restoreGraphGuardListenerSpy();
%s
%s
  </script>
</body>
</html>`, installed.Name(), installed.Name(), different.Name(), browserHarness, versionGraphGuardAssertions)
}

const versionGraphGuardAssertions = `__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  await waitFor(function () {
    return document.getElementById("graph-guard-output").textContent.trim() === "guard";
  }, "sealed component graph did not mount");
  assert(globalThis.__graphGuardUnexpectedError === undefined,
    "unexpected graph error: " + globalThis.__graphGuardUnexpectedError);
  assert(globalThis.__graphGuardErrors.length === 1, "different graph did not throw exactly once");
  assert(globalThis.__graphGuardSameKit, "same sealed artifact replaced the public kit object");
  assert(globalThis.kit === globalThis.__graphGuardFirstKit, "different graph replaced the public kit object");
  assert(globalThis.__versionPackageRuns === 1, "same artifact reran component package source");
  assert(globalThis.__versionInitCount === 1, "same artifact initialized the component twice");
  assert(globalThis.__graphGuardListeners.click === 1 && globalThis.__graphGuardListeners.keydown === 1,
    "same artifact installed duplicate delegated listeners");
  assert(document.getElementById("different-graph-output").textContent.trim() === "different-server",
    "different graph partially registered or mounted its component");
  var graphDescriptor = Object.getOwnPropertyDescriptor(globalThis.kit, Symbol.for("kitjs:graph"));
  assert(graphDescriptor && graphDescriptor.enumerable === false, "private graph is missing or enumerable");
  assert(Object.isFrozen(graphDescriptor.value) && Object.isFrozen(graphDescriptor.value.components),
    "installed graph metadata is mutable");
  assert(Object.keys(globalThis.kit).join(",") === "version,component", "private graph expanded public kit API");
});`

func TestBrowserDriveRejectsVersionMismatchBeforeMutation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping component-version Drive contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	artifact := buildVersionFixtureArtifact(t, ProfileHydrate, "drive-version-counter", "1.0.0", "drive")
	assetPath := "/assets/" + artifact.Name()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case assetPath:
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(artifact.Bytes())
		case "/drive-version.html":
			writeVersionContractHTML(response, versionDriveInitialDocument(assetPath))
		case "/drive-version-mismatch":
			if request.Header.Get("X-KitJS-Drive") == "1" {
				writeVersionContractHTML(response, versionDriveRejectedDocument(
					assetPath, "2.0.0", "drive-version-counter", "Mismatch poison"))
				return
			}
			response.Header().Set("Set-Cookie", "kit_version_mismatch_fallback=1; Path=/; SameSite=Lax")
			response.WriteHeader(http.StatusNoContent)
		case "/drive-version-unknown":
			if request.Header.Get("X-KitJS-Drive") == "1" {
				writeVersionContractHTML(response, versionDriveRejectedDocument(
					assetPath, "1.0.0", "unknown-version-component", "Unknown poison"))
				return
			}
			response.Header().Set("Set-Cookie", "kit_version_unknown_fallback=1; Path=/; SameSite=Lax")
			response.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/drive-version.html")
}

func buildVersionFixtureArtifact(t *testing.T, profile Profile, name, version, label string) Artifact {
	t.Helper()
	source := []byte(fmt.Sprintf(`;kit.component(%q, {
  count: 0,
  label: %q,
  init: function () {
    globalThis.__versionInitCount = (globalThis.__versionInitCount || 0) + 1;
  }
});
globalThis.__versionPackageRuns = (globalThis.__versionPackageRuns || 0) + 1;
`, name, label))
	artifact, err := Build(BuildOptions{
		Profile:    profile,
		Components: []ComponentVersion{{Name: name, Version: version}},
		Scripts:    []Script{{Name: name, Source: source}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func versionHandshakeDocument(artifact Artifact, invalidCases bool, label, version string) string {
	extra := ""
	if invalidCases {
		extra = `<section data-kit-component="version-counter" data-kit-version="2.0.0">
    <output id="version-mismatch" data-kit-text="label">mismatch-server</output>
  </section>
  <section data-kit-component="version-counter" data-kit-version="v1.0.0">
    <output id="version-invalid" data-kit-text="label">invalid-server</output>
  </section>
  <section data-kit-component="version-counter@1.0.0">
    <output id="version-inline" data-kit-text="label">inline-server</output>
  </section>
  <section data-kit-component="unknown-version-component" data-kit-version="1.0.0">
    <output id="version-unknown" data-kit-text="label">unknown-server</output>
  </section>
  <div data-kit-version="1.0.0">
    <output id="version-orphan" data-kit-text="label">orphan-server</output>
  </div>`
	}
	assertions := fmt.Sprintf(`__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  await waitFor(function () {
    return document.getElementById("version-exact").textContent.trim() === %q &&
      document.getElementById("version-unversioned").textContent.trim() === %q;
  }, "valid component hosts did not mount");
  assert(Object.keys(globalThis.kit).join(",") === "version,component", "component graph expanded the public kit API");
  assert(globalThis.__versionFetchCalls === 0, "component version verification fetched a package");
  assert(globalThis.__versionPackageRuns === 1, "component package did not execute exactly once");
  assert(globalThis.__versionInitCount === 2, "invalid component metadata ran init");
  %s
});`, label, label, versionInvalidAssertions(invalidCases))
	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Version handshake</title>
  <script>
    globalThis.__versionFetchCalls = 0;
    var __versionRealFetch = globalThis.fetch;
    globalThis.fetch = function () {
      globalThis.__versionFetchCalls++;
      return __versionRealFetch.apply(this, arguments);
    };
  </script>
</head>
<body>
  <section data-kit-component="version-counter" data-kit-version="%s">
    <output id="version-exact" data-kit-text="label">exact-server</output>
  </section>
  <section data-kit-component="version-counter">
    <output id="version-unversioned" data-kit-text="label">unversioned-server</output>
  </section>
  %s
  <script src="/assets/%s"></script>
  <script>
%s
%s
  </script>
</body>
</html>`, version, extra, artifact.Name(), browserHarness, assertions)
}

func versionInvalidAssertions(enabled bool) string {
	if !enabled {
		return ""
	}
	return `assert(document.getElementById("version-mismatch").textContent.trim() === "mismatch-server", "mismatched pin mounted");
  assert(document.getElementById("version-invalid").textContent.trim() === "invalid-server", "invalid version mounted");
  assert(document.getElementById("version-inline").textContent.trim() === "inline-server", "inline @ version mounted");
  assert(document.getElementById("version-unknown").textContent.trim() === "unknown-server", "unknown component mounted");
  assert(document.getElementById("version-orphan").textContent.trim() === "orphan-server", "orphan version activated directives");`
}

const versionLocalDocument = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>Local component without graph</title></head>
<body>
  <section data-kit-component="local-version-counter">
    <output id="local-unversioned" data-kit-text="label">unversioned-server</output>
  </section>
  <section data-kit-component="local-version-counter" data-kit-version="1.0.0">
    <output id="local-pinned" data-kit-text="label">pinned-server</output>
  </section>
  <script>
    globalThis.__versionFetchCalls = 0;
    var __versionRealFetch = globalThis.fetch;
    globalThis.fetch = function () {
      globalThis.__versionFetchCalls++;
      return __versionRealFetch.apply(this, arguments);
    };
  </script>
  <script src="/kit.js"></script>
  <script>
    kit.component("local-version-counter", {
      label: "local",
      init: function () { globalThis.__versionInitCount = (globalThis.__versionInitCount || 0) + 1; }
    });
  </script>
  <script>
` + browserHarness + `
__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  await waitFor(function () {
    return document.getElementById("local-unversioned").textContent.trim() === "local";
  }, "unversioned local component did not mount");
  assert(document.getElementById("local-pinned").textContent.trim() === "pinned-server",
    "pinned component mounted without an installed graph");
  assert(globalThis.__versionInitCount === 1, "pinned component without a graph ran init");
  assert(globalThis.__versionFetchCalls === 0, "local version verification fetched a package");
  assert(Object.keys(globalThis.kit).join(",") === "version,component", "local runtime public API changed");
});
  </script>
</body>
</html>`

func versionDriveInitialDocument(assetPath string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="description" content="Version initial">
  <title>Version initial</title>
  <script src="%s"></script>
</head>
<body>
  <header id="drive-version-shell" data-kit-component="drive-version-counter" data-kit-version="1.0.0">
    <button id="drive-version-add" type="button" data-kit-click="count = count + 1">Add</button>
    <output id="drive-version-output" data-kit-text="count">server</output>
  </header>
  <nav>
    <a id="drive-version-mismatch-link" href="/drive-version-mismatch">Mismatch</a>
    <a id="drive-version-unknown-link" href="/drive-version-unknown">Unknown</a>
  </nav>
  <main id="drive-version-main">Initial body</main>
  <script>
    globalThis.__versionIncomingScript = 0;
%s
%s
  </script>
</body>
</html>`, assetPath, browserHarness, versionDriveAssertions)
}

const versionDriveAssertions = `__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  await waitFor(function () { return document.getElementById("drive-version-output").textContent.trim() === "0"; },
    "Drive version component did not boot");
  document.getElementById("drive-version-add").click();
  await waitFor(function () { return document.getElementById("drive-version-output").textContent.trim() === "1"; },
    "Drive version component did not update");

  var root = document.documentElement;
  var realFetch = globalThis.fetch.bind(globalThis);
  var fetches = [];
  globalThis.fetch = function (source, options) {
    fetches.push(String(source));
    return realFetch(source, options);
  };

  document.getElementById("drive-version-mismatch-link").click();
  await waitFor(function () { return document.cookie.indexOf("kit_version_mismatch_fallback=1") >= 0; },
    "mismatched pin did not hard-navigate");
  assert(document.documentElement === root, "Drive replaced the document before mismatched-pin fallback");
  assert(location.pathname === "/drive-version.html", "Drive committed a mismatched-pin URL");
  assert(document.title === "Version initial", "Drive changed title before mismatched-pin fallback");
  assert(document.querySelector('meta[name="description"]').content === "Version initial",
    "Drive changed head before mismatched-pin fallback");
  assert(document.getElementById("drive-version-main").textContent.trim() === "Initial body",
    "Drive changed body before mismatched-pin fallback");
  assert(document.getElementById("drive-version-output").textContent.trim() === "1",
    "Drive reset component state before mismatched-pin fallback");
  assert(globalThis.__versionIncomingScript === 0, "Drive executed an incoming script before fallback");

  document.getElementById("drive-version-unknown-link").click();
  await waitFor(function () { return document.cookie.indexOf("kit_version_unknown_fallback=1") >= 0; },
    "unknown manifest component did not hard-navigate");
  assert(document.documentElement === root, "Drive replaced the document before unknown-component fallback");
  assert(location.pathname === "/drive-version.html", "Drive committed an unknown-component URL");
  assert(document.title === "Version initial" &&
    document.getElementById("drive-version-main").textContent.trim() === "Initial body" &&
    document.getElementById("drive-version-output").textContent.trim() === "1",
    "unknown component mutated the live document before fallback");
  assert(fetches.filter(function (url) { return new URL(url).pathname === "/drive-version-mismatch"; }).length === 1,
    "mismatched route was fetched more than once by Drive");
  assert(fetches.filter(function (url) { return new URL(url).pathname === "/drive-version-unknown"; }).length === 1,
    "unknown route was fetched more than once by Drive");
  assert(Object.keys(globalThis.kit).join(",") === "version,component", "Drive graph expanded public kit API");
});`

func versionDriveRejectedDocument(assetPath, version, component, title string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="poisoned">
<head>
  <meta charset="utf-8">
  <meta name="description" content="Poisoned metadata">
  <title>%s</title>
  <script src="%s"></script>
</head>
<body>
  <section data-kit-component="%s" data-kit-version="%s">
    <output data-kit-text="count">poisoned component</output>
  </section>
  <main id="drive-version-main">Poisoned body</main>
  <script>globalThis.__versionIncomingScript = 99;</script>
</body>
</html>`, title, assetPath, component, version)
}

func writeVersionContractHTML(response http.ResponseWriter, source string) {
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = response.Write([]byte(source))
}
