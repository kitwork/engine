package javascript

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBrowserDrivePreservesCompatibleDocumentRootAndRejectsBoundaryDrift(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Drive document-root boundary contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	artifact := buildDriveRootBoundaryArtifact(t)
	assetPath := "/assets/" + artifact.Name()
	assetIntegrity := driveScriptIntegrity(artifact.Bytes())
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/drive-root-contract.js" {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte(driveRootContractSource))
			return
		}
		if request.URL.Path == assetPath {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(artifact.Bytes())
			return
		}
		if request.URL.Path == "/drive-root.html" {
			writeDriveRootBoundaryHTML(response, driveRootInitialDocument(assetPath, assetIntegrity))
			return
		}
		if request.URL.Path == "/drive-root-same" {
			writeDriveRootBoundaryHTML(response, driveRootSameDocument(assetPath, assetIntegrity))
			return
		}

		var source string
		switch request.URL.Path {
		case "/drive-root-component":
			source = driveRootRejectedDocument(assetPath, assetIntegrity,
				`data-kit-component="shell" data-kit-version="1.0.0" data-kit-as="$app" data-kit-scope="{ count: 0 }"`)
		case "/drive-root-version":
			source = driveRootRejectedDocument(assetPath, assetIntegrity,
				`data-kit-component="app" data-kit-version="2.0.0" data-kit-as="$app" data-kit-scope="{ count: 0 }"`)
		case "/drive-root-alias":
			source = driveRootRejectedDocument(assetPath, assetIntegrity,
				`data-kit-component="app" data-kit-version="1.0.0" data-kit-as="$other" data-kit-scope="{ count: 0 }"`)
		case "/drive-root-scope":
			source = driveRootRejectedDocument(assetPath, assetIntegrity,
				`data-kit-component="app" data-kit-version="1.0.0" data-kit-as="$app" data-kit-scope="{ count: 99 }"`)
		case "/drive-root-removed":
			source = driveRootRejectedDocument(assetPath, assetIntegrity, "")
		case "/drive-root-unknown":
			source = driveRootRejectedDocument(assetPath, assetIntegrity,
				`data-kit-component="unknown-root" data-kit-as="$app" data-kit-scope="{ count: 0 }"`)
		default:
			http.NotFound(response, request)
			return
		}
		if request.Header.Get("X-KitJS-Drive") == "1" {
			writeDriveRootBoundaryHTML(response, source)
			return
		}
		response.Header().Set("Set-Cookie", "kit_root_"+request.URL.Path[len("/drive-root-"):]+"_fallback=1; Path=/; SameSite=Lax")
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/drive-root.html")
}

func buildDriveRootBoundaryArtifact(t *testing.T) Artifact {
	t.Helper()
	artifact, err := Build(BuildOptions{
		Profile: ProfileHydrate,
		Components: []ComponentVersion{
			{Name: "app", Version: "1.0.0"},
			{Name: "shell", Version: "1.0.0"},
		},
		Scripts: []Script{
			{Name: "app", Source: []byte(`;kit.component("app", {
  count: 0,
  init: function () {
    globalThis.__driveRootInitCount = (globalThis.__driveRootInitCount || 0) + 1;
  }
});
`)},
			{Name: "shell", Source: []byte(`;kit.component("shell", { count: 0 });
`)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func driveRootInitialDocument(assetPath, assetIntegrity string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="en" data-kit-component=" app " data-kit-version=" 1.0.0 " data-kit-as=" $app " data-kit-scope=" { count: 0 } ">
<head>
  <meta charset="utf-8">
  <meta name="description" content="Root initial">
  <title>Root initial</title>
  <script defer src="%s" integrity="%s" crossorigin="anonymous"></script>
  <script defer src="/drive-root-contract.js" integrity="%s" crossorigin="anonymous"></script>
</head>
<body>
  <button id="drive-root-add" type="button" data-kit-click="count = count + 1">Add</button>
  <output id="drive-root-count" data-kit-text="count">server</output>
  <a id="drive-root-same-link" href="/drive-root-same">Same root</a>
  <main id="drive-root-main">Initial body</main>
</body>
</html>`, assetPath, assetIntegrity, driveRootContractIntegrity)
}

func driveRootSameDocument(assetPath, assetIntegrity string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="vi" data-kit-component="app" data-kit-version="1.0.0" data-kit-as="$app" data-kit-scope="{ count: 0 }">
<head>
  <meta charset="utf-8">
  <meta name="description" content="Root accepted">
  <title>Root accepted</title>
  <script defer src="%s" integrity="%s" crossorigin="anonymous"></script>
  <script defer src="/drive-root-contract.js" integrity="%s" crossorigin="anonymous"></script>
</head>
<body>
  <output id="drive-root-count" data-kit-text="count">server</output>
  <nav>
    <a id="drive-root-component-link" href="/drive-root-component">Changed component</a>
    <a id="drive-root-version-link" href="/drive-root-version">Changed version</a>
    <a id="drive-root-alias-link" href="/drive-root-alias">Changed alias</a>
    <a id="drive-root-scope-link" href="/drive-root-scope">Changed scope</a>
    <a id="drive-root-removed-link" href="/drive-root-removed">Removed boundary</a>
    <a id="drive-root-unknown-link" href="/drive-root-unknown">Unknown component</a>
  </nav>
  <main id="drive-root-main">Accepted body</main>
</body>
</html>`, assetPath, assetIntegrity, driveRootContractIntegrity)
}

func driveRootRejectedDocument(assetPath, assetIntegrity, metadata string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="poisoned" %s>
<head>
  <meta charset="utf-8">
  <meta name="description" content="Root poison">
  <title>Root poison</title>
  <script defer src="%s" integrity="%s" crossorigin="anonymous"></script>
  <script defer src="/drive-root-contract.js" integrity="%s" crossorigin="anonymous"></script>
</head>
<body>
  <output id="drive-root-count" data-kit-text="count">poison</output>
  <main id="drive-root-main">Poisoned body</main>
</body>
</html>`, metadata, assetPath, assetIntegrity, driveRootContractIntegrity)
}

var driveRootContractIntegrity = driveScriptIntegrity([]byte(driveRootContractSource))

const driveRootContractSource = browserHarness + `
` + driveRootBoundaryAssertions

const driveRootBoundaryAssertions = `__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  await waitFor(function () {
    return document.getElementById("drive-root-count").textContent.trim() === "0";
  }, "document-root app did not mount");
  document.getElementById("drive-root-add").click();
  await waitFor(function () {
    return document.getElementById("drive-root-count").textContent.trim() === "1";
  }, "document-root app state did not update");

  var root = document.documentElement;
  document.getElementById("drive-root-same-link").click();
  await waitFor(function () {
    return location.pathname === "/drive-root-same" &&
      document.getElementById("drive-root-main").textContent.trim() === "Accepted body" &&
      document.getElementById("drive-root-count").textContent.trim() === "1";
  }, "compatible document-root boundary did not commit with live state");
  assert(document.documentElement === root, "Drive replaced a compatible document root");
  assert(globalThis.__driveRootInitCount === 1, "Drive reinitialized a compatible document-root component");
  assert(document.documentElement.getAttribute("data-kit-component") === " app ",
    "Drive reconciled document-root component metadata");
  assert(document.title === "Root accepted" &&
    document.querySelector('meta[name="description"]').content === "Root accepted" &&
    document.documentElement.lang === "vi", "compatible root did not reconcile ordinary document state");

  async function rejects(id, cookie) {
    document.getElementById(id).click();
    await waitFor(function () { return document.cookie.indexOf(cookie + "=1") >= 0; },
      id + " did not hard-navigate");
    assert(document.documentElement === root, id + " replaced the root before fallback");
    assert(location.pathname === "/drive-root-same", id + " committed a rejected URL");
    assert(document.title === "Root accepted" &&
      document.querySelector('meta[name="description"]').content === "Root accepted" &&
      document.documentElement.lang === "vi", id + " changed document metadata before fallback");
    assert(document.getElementById("drive-root-main").textContent.trim() === "Accepted body" &&
      document.getElementById("drive-root-count").textContent.trim() === "1",
      id + " changed body or root state before fallback");
    assert(globalThis.__driveRootInitCount === 1, id + " reinitialized the root component");
  }

  await rejects("drive-root-component-link", "kit_root_component_fallback");
  await rejects("drive-root-version-link", "kit_root_version_fallback");
  await rejects("drive-root-alias-link", "kit_root_alias_fallback");
  await rejects("drive-root-scope-link", "kit_root_scope_fallback");
  await rejects("drive-root-removed-link", "kit_root_removed_fallback");
  await rejects("drive-root-unknown-link", "kit_root_unknown_fallback");
});`

func writeDriveRootBoundaryHTML(response http.ResponseWriter, source string) {
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = response.Write([]byte(source))
}
