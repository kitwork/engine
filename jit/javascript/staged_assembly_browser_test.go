package javascript

import (
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestStagedAssemblyBrowserContract(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping staged JIT browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	options := StagedBuildOptions{
		Profile: ProfileKit,
		Services: []Service{{
			Name: "storage", Version: "1.0.0", Actions: []string{"get"},
			Source: []byte("; kit.service(\"storage\", { get: function () { return \"stored\"; } });\n"),
		}},
		Components: []ComponentPackage{
			{
				Name: "counter", Version: "1.0.0",
				Source: []byte("; globalThis.__stagedDelivery = document[Symbol.for(\"kitjs:assembly\")].delivery;\n" +
					"kit.component(\"counter\", { count: 0 });\n"),
			},
			{
				Name: "spare", Version: "1.0.0",
				Source: []byte("; kit.component(\"spare\", { ready: true });\n"),
			},
		},
		SharedComponentNames: []string{"counter", "spare"},
	}
	assembly, err := BuildStaged(options)
	if err != nil {
		t.Fatal(err)
	}
	options.Profile = ProfileHydrate
	hydrateAssembly, err := BuildStaged(options)
	if err != nil {
		t.Fatal(err)
	}
	poisonOptions := options
	poisonOptions.Profile = ProfileKit
	poisonOptions.Components = append([]ComponentPackage(nil), options.Components...)
	poisonOptions.Components[0] = ComponentPackage{
		Name: "counter", Version: "1.0.0", Source: []byte("; throw new Error(\"poison package\");\n"),
	}
	poisonAssembly, err := BuildStaged(poisonOptions)
	if err != nil {
		t.Fatal(err)
	}
	zeroAssembly, err := BuildStaged(StagedBuildOptions{Profile: ProfileKit})
	if err != nil {
		t.Fatal(err)
	}

	assets := make(map[string][]byte)
	for _, candidate := range []StagedAssembly{assembly, hydrateAssembly, poisonAssembly, zeroAssembly} {
		for _, artifact := range candidate.Artifacts() {
			assets["/jit/"+artifact.Name()] = artifact.Bytes()
		}
	}
	var foreignJITRequests atomic.Int64
	foreignServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Access-Control-Allow-Origin", "*")
		if source, exists := assets[request.URL.Path]; exists {
			foreignJITRequests.Add(1)
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(source)
			return
		}
		if request.URL.Path == "/legacy-theme.js" {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte("; globalThis.__legacyThemeJIT = true;\n"))
			return
		}
		http.NotFound(response, request)
	}))
	defer foreignServer.Close()
	pages := map[string]string{
		"/valid.html":               stagedBrowserDocument(assembly, "valid", true, ""),
		"/valid-hydrate.html":       stagedBrowserDocument(hydrateAssembly, "valid", true, ""),
		"/tampered.html":            stagedBrowserDocument(assembly, "tampered", true, ""),
		"/missing.html":             stagedBrowserDocument(assembly, "missing", true, ""),
		"/unavailable.html":         stagedBrowserDocument(assembly, "unavailable", true, ""),
		"/graph-unavailable.html":   stagedBrowserDocument(assembly, "graph-unavailable", true, ""),
		"/hydrate-unavailable.html": stagedBrowserDocument(hydrateAssembly, "hydrate-unavailable", true, ""),
		"/reversed.html":            stagedBrowserDocument(assembly, "reversed", true, ""),
		"/poison.html":              stagedBrowserDocument(poisonAssembly, "poison", true, ""),
		"/zero.html":                stagedBrowserDocument(zeroAssembly, "valid", false, ""),
		"/cross-base.html":          stagedBrowserDocument(assembly, "valid", true, foreignServer.URL+"/"),
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if source, exists := assets[request.URL.Path]; exists {
			if strings.Contains(request.Referer(), "/unavailable.html") && assembly.ComponentsBundle != nil &&
				request.URL.Path == "/jit/"+assembly.ComponentsBundle.Name() {
				http.NotFound(response, request)
				return
			}
			if strings.Contains(request.Referer(), "/graph-unavailable.html") &&
				request.URL.Path == "/jit/"+assembly.Graph.Name() {
				http.NotFound(response, request)
				return
			}
			if strings.Contains(request.Referer(), "/hydrate-unavailable.html") && hydrateAssembly.Hydrate != nil &&
				request.URL.Path == "/jit/"+hydrateAssembly.Hydrate.Name() {
				http.NotFound(response, request)
				return
			}
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(source)
			return
		}
		if request.URL.Path == "/legacy-theme.js" {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write([]byte("; globalThis.__legacyThemeJIT = true;\n"))
			return
		}
		if page, exists := pages[request.URL.Path]; exists {
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(page))
			return
		}
		http.NotFound(response, request)
	}))
	defer server.Close()

	t.Run("publishes before DOMContentLoaded", func(t *testing.T) {
		runVanillaBrowser(t, browser, server.URL+"/valid.html")
	})
	t.Run("optional Hydrate addon", func(t *testing.T) {
		runVanillaBrowser(t, browser, server.URL+"/valid-hydrate.html")
	})
	t.Run("tampered package remains inert", func(t *testing.T) {
		runVanillaBrowser(t, browser, server.URL+"/tampered.html")
	})
	t.Run("missing package remains inert", func(t *testing.T) {
		runVanillaBrowser(t, browser, server.URL+"/missing.html")
	})
	t.Run("unavailable package remains inert", func(t *testing.T) {
		runVanillaBrowser(t, browser, server.URL+"/unavailable.html")
	})
	t.Run("unavailable graph cleans assembly", func(t *testing.T) {
		runVanillaBrowser(t, browser, server.URL+"/graph-unavailable.html")
	})
	t.Run("unavailable Hydrate cleans assembly", func(t *testing.T) {
		runVanillaBrowser(t, browser, server.URL+"/hydrate-unavailable.html")
	})
	t.Run("reversed tags remain inert", func(t *testing.T) {
		runVanillaBrowser(t, browser, server.URL+"/reversed.html")
	})
	t.Run("throwing package remains inert", func(t *testing.T) {
		runVanillaBrowser(t, browser, server.URL+"/poison.html")
	})
	t.Run("zero package graph", func(t *testing.T) {
		runVanillaBrowser(t, browser, server.URL+"/zero.html")
	})
	t.Run("authored cross-origin base", func(t *testing.T) {
		before := foreignJITRequests.Load()
		runVanillaBrowser(t, browser, server.URL+"/cross-base.html")
		if got := foreignJITRequests.Load() - before; got != 0 {
			t.Fatalf("cross-origin base received %d staged JIT requests", got)
		}
	})
}

func TestStagedComponentTransactionBrowserContract(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping staged component transaction browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	storage := Service{
		Name: "storage", Version: "1.0.0", Actions: []string{"get"},
		Source: []byte("; kit.service(\"storage\", { get: function () { return \"stored\"; } });\n"),
	}
	appSource := []byte("; globalThis.__handoffCore = document[Symbol.for(\"kitjs:assembly\")];\n" +
		"kit.component(\"app\", { ready: true });\n")
	initialOptions := StagedBuildOptions{
		Profile:  ProfileHydrate,
		Services: []Service{storage},
		Components: []ComponentPackage{
			{Name: "app", Version: "1.0.0", Source: appSource},
			{Name: "alpha", Version: "1.0.0", Source: []byte("; kit.component(\"alpha\", { value: \"alpha\" });\n")},
		},
		ComponentRequires: []ComponentServiceRequirement{{
			Component: "app", Service: ServiceVersion{Name: "storage", Version: "1.0.0"},
		}},
	}
	targetOptions := StagedBuildOptions{
		Profile:  ProfileHydrate,
		Services: []Service{storage},
		Components: []ComponentPackage{
			{Name: "app", Version: "1.0.0", Source: appSource},
			{Name: "beta", Version: "1.0.0", Source: []byte("; globalThis.__betaInstallerRuns = (globalThis.__betaInstallerRuns || 0) + 1;\nglobalThis.__betaCapturedKit = kit;\nglobalThis.__betaGraphAtInstall = kit[Symbol.for(\"kitjs:graph\")].artifact;\nkit.component(\"beta\", { value: \"beta\" });\n")},
		},
		ComponentRequires: []ComponentServiceRequirement{
			{Component: "app", Service: ServiceVersion{Name: "storage", Version: "1.0.0"}},
			{Component: "beta", Service: ServiceVersion{Name: "storage", Version: "1.0.0"}},
		},
	}
	initial, err := BuildStaged(initialOptions)
	if err != nil {
		t.Fatal(err)
	}
	target, err := BuildStaged(targetOptions)
	if err != nil {
		t.Fatal(err)
	}
	conflictOptions := targetOptions
	conflictOptions.ComponentRequires = []ComponentServiceRequirement{{
		Component: "beta", Service: ServiceVersion{Name: "storage", Version: "1.0.0"},
	}}
	conflict, err := BuildStaged(conflictOptions)
	if err != nil {
		t.Fatal(err)
	}
	targetOptions.SharedComponentNames = []string{"app", "beta"}
	bundled, err := BuildStaged(targetOptions)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Runtime.SHA256() != target.Runtime.SHA256() ||
		initial.Hydrate.SHA256() != target.Hydrate.SHA256() ||
		stagedArtifactByPackage(t, initial.Services, "storage").SHA256() !=
			stagedArtifactByPackage(t, target.Services, "storage").SHA256() {
		t.Fatal("component-only transaction fixture changed runtime or service identity")
	}

	assets := make(map[string][]byte)
	for _, assembly := range []StagedAssembly{initial, target, bundled, conflict} {
		for _, artifact := range assembly.Artifacts() {
			assets["/jit/"+artifact.Name()] = artifact.Bytes()
		}
	}
	page := stagedComponentHandoffDocument(initial, target, bundled, conflict)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if source, exists := assets[request.URL.Path]; exists {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(source)
			return
		}
		if request.URL.Path == "/" {
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(page))
			return
		}
		http.NotFound(response, request)
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/")
}

func stagedComponentHandoffDocument(initial, target, bundled, conflict StagedAssembly) string {
	var tags strings.Builder
	for _, artifact := range initial.Artifacts() {
		tags.WriteString(`<script data-kitwork-jit="` + html.EscapeString(string(artifact.Role())) +
			`" data-kitwork-hash="` + artifact.SHA256() + `" src="/jit/` + html.EscapeString(artifact.Name()) +
			`" integrity="` + html.EscapeString(artifact.Integrity()) + `" crossorigin="anonymous" defer></script>` + "\n")
	}
	return `<!doctype html><html><head><meta charset="utf-8"><title>Staged component transaction</title>
` + tags.String() + `<script>
document.addEventListener("DOMContentLoaded", function () {
  var root = document.documentElement;
  var HANDOFF = Symbol.for("kitjs:handoff");
  var GRAPH = Symbol.for("kitjs:graph");
  var core = globalThis.__handoffCore;
  var originalKit = globalThis.kit;
  var originalGraph = originalKit && originalKit[GRAPH];
  var originalDelivery = core && core.delivery;
  var tx = null;
  var targetGraphSource = null;
  var targetDeliverySource = null;
  var expectedGraph = ` + stagedArtifactLiteral(target.Graph) + `;
  var staleComponent = ` + stagedArtifactLiteral(stagedArtifactForDocument(target.Components, "beta")) + `;
  var partialError = null;
  var grantConflictError = null;
  var expectGraphFailure = false;

  function assert(value, message) { if (!value) throw new Error(message); }
  function assertScript(script, asset) {
    assert(script && script.getAttribute("data-kitwork-jit") === asset.role &&
      script.getAttribute("data-kitwork-hash") === asset.hash &&
      script.getAttribute("data-kitwork-handoff") === "" &&
      script.getAttribute("src") === "/jit/" + asset.name &&
      script.getAttribute("integrity") === asset.integrity &&
      script.getAttribute("crossorigin") === "anonymous", "handoff script identity mismatch");
  }
  function load(asset) {
    return new Promise(function (resolve, reject) {
      var script = document.createElement("script");
      script.setAttribute("data-kitwork-jit", asset.role);
      script.setAttribute("data-kitwork-hash", asset.hash);
      script.setAttribute("data-kitwork-handoff", "");
      script.setAttribute("integrity", asset.integrity);
      script.setAttribute("crossorigin", "anonymous");
      script.defer = true;
      script.async = false;
      script.onload = function () { resolve(); };
      script.onerror = function () { reject(new Error("failed to load " + asset.name)); };
      script.setAttribute("src", "/jit/" + asset.name);
      document.head.appendChild(script);
    });
  }
  function componentAsset(identity) {
    return targetDeliverySource.assets.filter(function (asset) {
      return asset.components && asset.components.some(function (component) {
        return component.name === identity.name && component.version === identity.version &&
          component.sourceHash === identity.sourceHash;
      });
    })[0];
  }
  var bridge = Object.freeze({
    graph: function (script, graph, delivery) {
      assertScript(script, expectedGraph);
      if (expectGraphFailure) {
        try { core.beginComponentHandoff(graph, delivery); }
        catch (error) { grantConflictError = error; }
        return;
      }
      tx = core.beginComponentHandoff(graph, delivery);
      targetGraphSource = tx.graph;
      targetDeliverySource = tx.delivery;
    },
    component: function (script, componentPackage) {
      var asset = componentAsset(componentPackage);
      assert(asset && asset.role === "component", "individual component asset mapping is missing");
      assertScript(script, asset);
      tx.register(componentPackage.name, componentPackage.version,
        componentPackage.sourceHash, componentPackage.install);
    },
    components: function (script, componentPackages) {
      var asset = targetDeliverySource.assets.filter(function (candidate) {
        return candidate.role === "components";
      })[0];
      assert(asset, "component bundle asset mapping is missing");
      assertScript(script, asset);
      try {
        componentPackages.forEach(function (componentPackage) {
          tx.register(componentPackage.name, componentPackage.version,
            componentPackage.sourceHash, componentPackage.install);
        });
      } catch (error) {
        partialError = error;
      }
    }
  });
  Object.defineProperty(document, HANDOFF, { value: bridge, configurable: true });

  Promise.resolve().then(function () {
    assert(core && typeof core.beginComponentHandoff === "function", "private component handoff API is missing");
    core.compiled.set("before-handoff", function () {});
    return load(expectedGraph);
  }).then(function () {
    var missing = tx.missing();
    assert(Object.isFrozen(missing) && missing.length === 1 && missing[0].name === "beta" &&
      Object.isFrozen(missing[0]), "exact missing component set is wrong");
    assert(Object.isFrozen(tx.graph) && Object.isFrozen(tx.graph.componentHashes) &&
      Object.isFrozen(tx.delivery) && Object.isFrozen(tx.delivery.assets),
      "transaction did not expose normalized frozen graph and delivery");
    return load(componentAsset(missing[0]));
  }).then(function () {
    assert(tx.ready() === true, "complete transaction is not ready");
    assert(globalThis.__betaInstallerRuns === undefined,
      "component source executed before transaction commit");
    var rollback = tx.commit();
    assert(globalThis.kit === originalKit && Object.isFrozen(globalThis.kit), "handoff replaced public kit");
    assert(globalThis.kit[GRAPH].artifact === expectedGraph.hash && core.delivery.graphHash === expectedGraph.hash,
      "handoff did not activate exact graph and delivery");
    assert(core.registry.has("app") && core.registry.has("beta") && !core.registry.has("alpha"),
      "handoff did not replace the active component registry");
    assert(globalThis.kit[GRAPH].grants.app.storage === "1.0.0" &&
      globalThis.kit[GRAPH].grants.beta.storage === "1.0.0",
      "handoff did not activate exact target route grants");
    assert(core.compiled.size === 0, "handoff did not clear graph-sensitive compiled metadata");
    assert(globalThis.__betaInstallerRuns === 1, "missing component installer did not run exactly once");
    assert(globalThis.__betaCapturedKit === globalThis.kit &&
      globalThis.__betaGraphAtInstall === expectedGraph.hash,
      "component closure did not receive canonical kit with the target graph");
    assert(rollback() === true && rollback() === false, "rollback is not idempotent");
    assert(globalThis.kit[GRAPH] === originalGraph && core.delivery === originalDelivery &&
      core.registry.has("alpha") && !core.registry.has("beta") && core.compiled.has("before-handoff"),
      "rollback did not restore the complete active transaction");
    assert(globalThis.__betaCapturedKit[GRAPH] === originalGraph,
      "captured canonical kit did not resolve the rolled-back active graph");
    var retry = core.beginComponentHandoff(targetGraphSource, targetDeliverySource);
    assert(retry.missing().length === 1 && retry.missing()[0].name === "beta",
      "rollback retained a newly installed component cache entry");
    assert(retry.abort() === true && retry.abort() === false, "abort is not idempotent");

    expectedGraph = ` + stagedArtifactLiteral(bundled.Graph) + `;
    return load(expectedGraph);
  }).then(function () {
    var missing = tx.missing();
    assert(missing.length === 1 && missing[0].name === "beta", "bundle fixture has wrong delta");
    var bundle = targetDeliverySource.assets.filter(function (asset) { return asset.role === "components"; })[0];
    return load(bundle);
  }).then(function () {
    assert(partialError && /duplicate or already cached/.test(String(partialError.message || partialError)),
      "partial-overlap component bundle was not rejected");
    var closed = false;
    try { tx.missing(); } catch (_) { closed = true; }
    assert(closed && globalThis.kit[GRAPH] === originalGraph && core.delivery === originalDelivery,
      "rejected bundle changed active graph state");
    assert(globalThis.__betaInstallerRuns === 1, "rejected bundle executed an uncached member");
    expectedGraph = ` + stagedArtifactLiteral(conflict.Graph) + `;
    expectGraphFailure = true;
    return load(expectedGraph);
  }).then(function () {
    assert(grantConflictError && /cannot replace overlapping component/.test(
      String(grantConflictError.message || grantConflictError)),
      "overlapping component grant change was not rejected");
    assert(globalThis.kit[GRAPH] === originalGraph && core.delivery === originalDelivery,
      "rejected grant change altered active graph state");
    delete document[HANDOFF];
    return load(staleComponent);
  }).then(function () {
    assert(globalThis.__betaInstallerRuns === 1 && globalThis.kit[GRAPH] === originalGraph &&
      core.delivery === originalDelivery,
      "late marked component script was not inert after bridge removal");
    root.setAttribute("data-kit-test", "passed");
  }).catch(function (error) {
    delete document[HANDOFF];
    root.setAttribute("data-kit-test", "failed");
    root.setAttribute("data-kit-test-error", String(error && error.message || error));
  });
}, { once: true });
</script></head><body><main data-kit-component="app" data-kit-version="1.0.0"></main></body></html>`
}

func stagedArtifactForDocument(artifacts []JITArtifact, packageName string) JITArtifact {
	for _, artifact := range artifacts {
		if artifact.Package() == packageName {
			return artifact
		}
	}
	panic("missing staged document artifact " + packageName)
}

func stagedArtifactLiteral(artifact JITArtifact) string {
	return `Object.freeze({ role: ` + jsString(string(artifact.Role())) + `, hash: ` +
		jsString(artifact.SHA256()) + `, integrity: ` + jsString(artifact.Integrity()) +
		`, name: ` + jsString(artifact.Name()) + ` })`
}

func stagedBrowserDocument(assembly StagedAssembly, scenario string, features bool, baseURL string) string {
	inert := scenario != "valid"
	artifacts := assembly.Artifacts()
	if scenario == "reversed" && len(artifacts) >= 4 {
		artifacts[len(artifacts)-1], artifacts[len(artifacts)-2] = artifacts[len(artifacts)-2], artifacts[len(artifacts)-1]
	}
	var tags strings.Builder
	for _, artifact := range artifacts {
		if scenario == "missing" && (artifact.Role() == JITRoleComponent || artifact.Role() == JITRoleComponents) {
			continue
		}
		hash := artifact.SHA256()
		if scenario == "tampered" && artifact.Role() == JITRoleComponents {
			hash = strings.Repeat("0", 64)
		}
		tags.WriteString(`<script data-kitwork-jit="` + html.EscapeString(string(artifact.Role())) +
			`" data-kitwork-hash="` + hash + `" src="/jit/` + html.EscapeString(artifact.Name()) +
			`" integrity="` + html.EscapeString(artifact.Integrity()) + `" crossorigin="anonymous" defer></script>` + "\n")
	}
	var page strings.Builder
	page.WriteString(`<!doctype html><html lang="en"><head>
`)
	page.WriteString(tags.String())
	if baseURL != "" {
		page.WriteString(`<base href="` + html.EscapeString(baseURL) + `">` + "\n")
	}
	page.WriteString(`<meta charset="utf-8"><title>Staged KitJS</title>
<script>
document.addEventListener("DOMContentLoaded", function () {
  var root = document.documentElement;
  try {
	`)
	if inert {
		page.WriteString(`    if (globalThis.kit !== undefined) throw new Error("tampered staged delivery published kit");
    if (document.getElementById("count").textContent.trim() !== "server") throw new Error("tampered staged delivery mutated SSR");
`)
	} else {
		page.WriteString(`    if (!globalThis.kit) throw new Error("kit was not published before DOMContentLoaded");
    var graph = globalThis.kit[Symbol.for("kitjs:graph")];
    if (!graph || graph.artifact !== ` + jsString(assembly.Graph.SHA256()) + `) throw new Error("exact graph artifact identity is missing");
    var graphDescriptor = Object.getOwnPropertyDescriptor(globalThis.kit, Symbol.for("kitjs:graph"));
    if (!Object.isFrozen(graph) || !Object.isFrozen(graph.componentHashes) ||
      !graphDescriptor || typeof graphDescriptor.get !== "function" || "value" in graphDescriptor) {
      throw new Error("installed staged graph is mutable or not active");
    }
`)
		if features {
			page.WriteString(`    var delivery = globalThis.__stagedDelivery;
    if (!delivery || !Object.isFrozen(delivery) || !Object.isFrozen(delivery.assets) ||
      !delivery.assets.every(Object.isFrozen)) throw new Error("private staged delivery is not deeply frozen");
    if (delivery.graphHash !== ` + jsString(assembly.Graph.SHA256()) + ` ||
      delivery.graphIntegrity !== ` + jsString(assembly.Graph.Integrity()) + ` ||
      delivery.graphName !== ` + jsString(assembly.Graph.Name()) + ` ||
      delivery.graphURL.slice(-delivery.graphName.length - 5) !== "/jit/" + delivery.graphName) {
      throw new Error("dynamic graph identity is incomplete");
    }
    var componentBundle = delivery.assets.filter(function (asset) { return asset.role === "components"; })[0];
    if (!componentBundle || !Object.isFrozen(componentBundle.packages) || componentBundle.packages.length !== 2 ||
      !Object.isFrozen(componentBundle.components) || componentBundle.components.length !== 2 ||
      !componentBundle.components.every(function (component) {
        return Object.isFrozen(component) && /^[0-9a-f]{64}$/.test(component.sourceHash);
      })) {
      throw new Error("shared component bundle identity is incomplete");
    }
    if (!globalThis.kit.storage || globalThis.kit.storage.get() !== "stored") throw new Error("staged service is unavailable");
    if (document.getElementById("count").textContent.trim() !== "0") throw new Error("staged component did not boot");
    if (globalThis.__legacyThemeJIT !== true) throw new Error("legacy JIT script did not execute");
`)
		} else {
			page.WriteString(`    if (Object.keys(globalThis.kit).join(",") !== "version,component") throw new Error("zero-package public API drifted");
    if (document.getElementById("count").textContent.trim() !== "server") throw new Error("zero-package graph mutated inert SSR");
`)
		}
	}
	if inert {
		page.WriteString(`    setTimeout(function () {
      try {
        if (document[Symbol.for("kitjs:assembly")] !== undefined) throw new Error("failed staged delivery retained private assembly");
        root.setAttribute("data-kit-test", "passed");
      } catch (error) {
        root.setAttribute("data-kit-test", "failed");
        root.setAttribute("data-kit-test-error", String(error && error.message || error));
      }
    }, 0);
    return;
`)
	}
	page.WriteString(`    root.setAttribute("data-kit-test", "passed");
  } catch (error) {
    root.setAttribute("data-kit-test", "failed");
    root.setAttribute("data-kit-test-error", String(error && error.message || error));
  }
}, { once: true });
</script></head><body>
<main data-kit-component="counter" data-kit-version="1.0.0"><output id="count" data-kit-text="count">server</output></main>
<script data-kitwork-jit="legacy-theme" src="/legacy-theme.js" defer></script>
	`)
	page.WriteString("</body></html>")
	return page.String()
}

func jsString(value string) string {
	return `"` + value + `"`
}
