package javascript

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
)

type completedServiceContract struct {
	service    Service
	members    []string
	stagedOnly bool
}

func TestExpressionNamespacePolicyCoversDeliveryCatalog(t *testing.T) {
	catalog, err := loadDeliveryCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for name, versions := range catalog.services {
		_, authored := authoredServiceActions[name]
		_, sealed := sealedExpressionServiceNames[name]
		if authored == sealed {
			t.Fatalf("service namespace %q must be classified exactly once: authored=%v sealed=%v", name, authored, sealed)
		}
		service, exists := versions["1.0.0"]
		if !exists {
			t.Fatalf("service namespace %q has no 1.0.0 policy anchor", name)
		}
		if authored != (len(service.actions) > 0) {
			t.Fatalf("service namespace %q policy/actions drifted: authored=%v actions=%#v", name, authored, service.actions)
		}
	}
}

func TestCompletedServiceCatalogPackagesAndDependencies(t *testing.T) {
	catalog := completedServiceCatalog(t)
	wantIdentities := make([]string, len(catalog))
	for index, contract := range catalog {
		wantIdentities[index] = contract.service.Name + "@" + contract.service.Version
	}
	wantIdentities = append(
		wantIdentities,
		"deepLinks@1.1.0",
		"files@1.0.0", "files@1.1.0", "files@1.2.0",
		"notifications@1.1.0", "notifications@1.2.0",
		"studioDatabase@1.1.0",
		"studioSqlite@1.1.0",
		"network@1.1.0", "files@1.4.0", "camera@1.1.0", "media@1.1.0",
	)
	sort.Strings(wantIdentities)

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve Vanilla service catalog directory")
	}
	root := filepath.Join(filepath.Dir(filename), "service")
	directories, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	gotIdentities := make([]string, 0, len(directories))
	known := make(map[string]bool, len(catalog))
	for _, contract := range catalog {
		known[contract.service.Name] = true
	}
	for _, directory := range directories {
		if !directory.IsDir() {
			t.Fatalf("service catalog root contains non-package entry %q", directory.Name())
		}
		if !known[directory.Name()] {
			t.Fatalf("service catalog contains undeclared package %q", directory.Name())
		}
		entries, err := os.ReadDir(filepath.Join(root, directory.Name()))
		if err != nil {
			t.Fatal(err)
		}
		versions := make([]string, 0, 1)
		for _, entry := range entries {
			if entry.IsDir() {
				t.Fatalf("service package %q contains nested directory %q", directory.Name(), entry.Name())
			}
			if filepath.Ext(entry.Name()) == ".js" {
				versions = append(versions, strings.TrimSuffix(entry.Name(), ".js"))
			}
		}
		wantVersions := []string{"1.0.0"}
		if directory.Name() == "files" {
			wantVersions = append(wantVersions, "1.1.0", "1.2.0", "1.3.0", "1.4.0")
		}
		if directory.Name() == "network" || directory.Name() == "camera" || directory.Name() == "media" {
			wantVersions = append(wantVersions, "1.1.0")
		}
		if directory.Name() == "notifications" {
			wantVersions = append(wantVersions, "1.1.0", "1.2.0")
		}
		if directory.Name() == "deepLinks" {
			wantVersions = append(wantVersions, "1.1.0")
		}
		if directory.Name() == "studioDatabase" || directory.Name() == "studioSqlite" {
			wantVersions = append(wantVersions, "1.1.0")
		}
		sort.Strings(versions)
		if strings.Join(versions, ",") != strings.Join(wantVersions, ",") {
			t.Fatalf("service package %q JS versions = %v, want %v", directory.Name(), versions, wantVersions)
		}
		for _, version := range versions {
			gotIdentities = append(gotIdentities, directory.Name()+"@"+version)
		}
	}
	sort.Strings(gotIdentities)
	if strings.Join(gotIdentities, ",") != strings.Join(wantIdentities, ",") {
		t.Fatalf("completed service catalog = %v, want %v", gotIdentities, wantIdentities)
	}

	wantRequires := map[string]string{
		"biometric":   "capabilities@1.0.0",
		"geolocation": "capabilities@1.0.0",
		"nfc":         "capabilities@1.0.0",
		"camera":      "capabilities@1.0.0,files@1.3.0",
		"media":       "files@1.3.0",
		"qr":          "capabilities@1.0.0",
		"request":     "progress@1.0.0",
		"share":       "clipboard@1.0.0",
	}
	for _, contract := range catalog {
		got := make([]string, len(contract.service.Requires))
		for index, dependency := range contract.service.Requires {
			got[index] = dependency.Name + "@" + dependency.Version
		}
		sort.Strings(got)
		if want := wantRequires[contract.service.Name]; strings.Join(got, ",") != want {
			t.Fatalf("%s@%s dependencies = %v, want %q",
				contract.service.Name, contract.service.Version, got, want)
		}
	}
}

func TestCompletedServiceCatalogFullBuildIsDeterministic(t *testing.T) {
	catalog := completedServiceCatalog(t)
	forward := catalogServices(standaloneServiceCatalog(catalog))
	reverse := append([]Service(nil), forward...)
	for left, right := 0, len(reverse)-1; left < right; left, right = left+1, right-1 {
		reverse[left], reverse[right] = reverse[right], reverse[left]
	}

	for _, profile := range []Profile{ProfileKit, ProfileHydrate} {
		profile := profile
		t.Run(string(profile), func(t *testing.T) {
			left, err := Build(BuildOptions{Profile: profile, Services: forward})
			if err != nil {
				t.Fatal(err)
			}
			right, err := Build(BuildOptions{Profile: profile, Services: reverse})
			if err != nil {
				t.Fatal(err)
			}
			if left.Name() != right.Name() || left.SHA256() != right.SHA256() ||
				!bytes.Equal(left.Bytes(), right.Bytes()) {
				t.Fatalf("%s full service catalog changed identity with discovery order", profile)
			}
			for _, contract := range catalog {
				if contract.stagedOnly {
					if bytes.Contains(left.Bytes(), contract.service.Source) {
						t.Fatalf("%s monolithic catalog unexpectedly contains staged-only %s@%s",
							profile, contract.service.Name, contract.service.Version)
					}
					continue
				}
				if got := bytes.Count(left.Bytes(), contract.service.Source); got != 1 {
					t.Fatalf("%s full catalog contains %s@%s source %d times, want 1",
						profile, contract.service.Name, contract.service.Version, got)
				}
			}
		})
	}
}

func TestCompletedServiceCatalogOnlySelectedPackagesAreSealed(t *testing.T) {
	catalog := completedServiceCatalog(t)
	byName := make(map[string]Service, len(catalog))
	for _, contract := range catalog {
		byName[contract.service.Name] = contract.service
	}

	for _, selected := range catalog {
		selected := selected
		t.Run(selected.service.Name, func(t *testing.T) {
			included := map[string]bool{selected.service.Name: true}
			services := []Service{selected.service}
			for _, dependency := range selected.service.Requires {
				included[dependency.Name] = true
				services = append(services, byName[dependency.Name])
			}
			var artifactSource []byte
			if selected.stagedOnly {
				assembly, err := BuildStaged(StagedBuildOptions{Profile: ProfileKit, Services: services})
				if err != nil {
					t.Fatal(err)
				}
				for _, artifact := range assembly.Artifacts() {
					artifactSource = append(artifactSource, artifact.Bytes()...)
				}
			} else {
				artifact, err := Build(BuildOptions{Profile: ProfileKit, Services: services})
				if err != nil {
					t.Fatal(err)
				}
				artifactSource = artifact.Bytes()
			}
			for _, contract := range catalog {
				got := bytes.Count(artifactSource, contract.service.Source)
				if included[contract.service.Name] && got != 1 {
					t.Fatalf("%s selection contains required %s source %d times, want 1",
						selected.service.Name, contract.service.Name, got)
				}
				if !included[contract.service.Name] && got != 0 {
					t.Fatalf("%s selection unexpectedly contains %s@%s",
						selected.service.Name, contract.service.Name, contract.service.Version)
				}
			}
		})
	}
}

func TestBrowserCompletedServiceCatalogPublicSurface(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping completed service catalog browser gate in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	catalog := completedServiceCatalog(t)
	standaloneCatalog := standaloneServiceCatalog(catalog)
	artifact, err := Build(BuildOptions{
		Profile:  ProfileKit,
		Services: catalogServices(standaloneCatalog),
	})
	if err != nil {
		t.Fatal(err)
	}
	page := completedServiceCatalogDocument(t, "/assets/"+artifact.Name(), standaloneCatalog)
	packagePaths := make(map[string]bool, len(catalog)*3)
	for _, contract := range catalog {
		name := contract.service.Name
		packagePaths["/service/"+name+"/"+contract.service.Version+".js"] = true
		packagePaths["/"+name+".js"] = true
		packagePaths["/"+name+".service.js"] = true
	}
	var packageRequests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/assets/"+artifact.Name():
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(artifact.Bytes())
		case request.URL.Path == "/catalog.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(page))
		case packagePaths[request.URL.Path] || strings.HasPrefix(request.URL.Path, "/service/"):
			packageRequests.Add(1)
			http.Error(response, "service packages must already be sealed", http.StatusGone)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/catalog.html")
	if got := packageRequests.Load(); got != 0 {
		t.Fatalf("completed catalog fetched service packages at runtime %d times", got)
	}
}

func completedServiceCatalog(t *testing.T) []completedServiceContract {
	t.Helper()
	return []completedServiceContract{
		{service: platformServicePackage(t, "announce"), members: []string{"assertive", "clear", "polite", "say"}},
		{service: appearanceServicePackage(t), members: []string{"mode", "resolved", "set", "snapshot", "subscribe", "system", "toggle"}},
		{service: commonNativeServicePackage(t, "biometric"), members: []string{"authenticate", "available", "status"}, stagedOnly: true},
		{service: commonNativeServicePackage(t, "geolocation"), members: []string{"available", "getCurrentPosition", "permission"}, stagedOnly: true},
		{service: commonNativeServicePackage(t, "nfc"), members: []string{"available", "scan", "status"}, stagedOnly: true},
		{service: Service{
			Name: "camera", Version: "1.0.0",
			Requires: []ServiceVersion{{Name: "capabilities", Version: "1.0.0"}, {Name: "files", Version: "1.3.0"}},
			Source:   readVanillaFile(t, "service", "camera", "1.0.0.js"),
		}, members: []string{"available", "capture"}, stagedOnly: true},
		{service: mobileServicePackage(t, "capabilities"), members: []string{"supports"}, stagedOnly: true},
		{service: clipboardServicePackage(t), members: []string{"readText", "writeText"}},
		{service: cookieServicePackage(t), members: []string{"get", "has", "remove", "set"}},
		{service: deepLinksServicePackage(t), members: []string{"snapshot", "subscribe"}},
		{service: mobileServicePackage(t, "device"), members: []string{"info", "vibrate"}, stagedOnly: true},
		{service: mobileServiceVersionPackage(t, "files", "1.3.0"), members: []string{"export", "importBlob", "pick", "release", "share", "stat", "toBlob"}, stagedOnly: true},
		{service: platformServicePackage(t, "fullscreen"), members: []string{"active", "exit", "request"}},
		{service: lifecycleServicePackage(t), members: []string{"snapshot", "subscribe"}},
		{service: Service{
			Name: "media", Version: "1.0.0",
			Requires: []ServiceVersion{{Name: "files", Version: "1.3.0"}},
			Source:   readVanillaFile(t, "service", "media", "1.0.0.js"),
		}, members: []string{"pickImage"}, stagedOnly: true},
		{service: platformServicePackage(t, "navigation"), members: []string{"back", "forward", "reload"}},
		{service: networkServicePackage(t), members: []string{"online", "snapshot", "status", "subscribe"}},
		{service: notificationServicePackage(t), members: []string{"permission", "requestPermission", "show"}},
		{service: progressServicePackage(t), members: []string{"finish", "snapshot", "start", "subscribe", "update"}},
		{service: Service{
			Name: "qr", Version: "1.0.0",
			Requires: []ServiceVersion{{Name: "capabilities", Version: "1.0.0"}},
			Source:   readVanillaFile(t, "service", "qr", "1.0.0.js"),
		}, members: []string{"available", "scan"}, stagedOnly: true},
		{service: requestServicePackage(t), members: []string{"abort", "get", "post", "send"}},
		{service: mobileServicePackage(t, "secureStorage"), members: []string{"get", "remove", "set"}, stagedOnly: true},
		{service: shareServicePackage(t), members: []string{"canShare", "open"}},
		{service: mobileServicePackage(t, "shell"), members: []string{"open"}, stagedOnly: true},
		{service: storageServicePackage(t), members: []string{"clear", "get", "has", "remove", "set"}},
		{service: mobileServicePackage(t, "studioDatabase"), members: []string{"catalog", "close", "open", "query", "tablePage"}, stagedOnly: true},
		{service: mobileServicePackage(t, "studioSqlite"), members: []string{"catalog", "close", "open", "query", "tablePage"}, stagedOnly: true},
		{service: mobileServicePackage(t, "studioState"), members: []string{"appendHistory", "clearHistory", "deleteConnection", "deleteSavedQuery", "loadSnapshot", "updatePreference", "upsertConnection", "upsertSavedQuery"}, stagedOnly: true},
		{service: mobileServicePackage(t, "wakeLock"), members: []string{"request"}},
		{service: platformServicePackage(t, "window"), members: []string{"close", "drag", "maximize", "minimize", "restore"}, stagedOnly: true},
	}
}

func mobileServicePackage(t *testing.T, name string) Service {
	t.Helper()
	return mobileServiceVersionPackage(t, name, "1.0.0")
}

func mobileServiceVersionPackage(t *testing.T, name, version string) Service {
	t.Helper()
	return Service{
		Name: name, Version: version,
		Source: readVanillaFile(t, "service", name, version+".js"),
	}
}

func catalogServices(catalog []completedServiceContract) []Service {
	services := make([]Service, len(catalog))
	for index, contract := range catalog {
		services[index] = contract.service
	}
	return services
}

func standaloneServiceCatalog(catalog []completedServiceContract) []completedServiceContract {
	output := make([]completedServiceContract, 0, len(catalog))
	for _, contract := range catalog {
		if !contract.stagedOnly {
			output = append(output, contract)
		}
	}
	return output
}

func completedServiceCatalogDocument(t *testing.T, assetPath string, catalog []completedServiceContract) string {
	t.Helper()
	expected := make(map[string]map[string]any, len(catalog))
	for _, contract := range catalog {
		expected[contract.service.Name] = map[string]any{
			"version": contract.service.Version,
			"members": contract.members,
		}
	}
	encoded, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf(`<!doctype html><html lang="en"><head><meta charset="utf-8">
<title>Completed service catalog contract</title><script>
globalThis.__catalogFetches = 0;
var __catalogFetch = globalThis.fetch;
globalThis.fetch = function () {
  globalThis.__catalogFetches++;
  return __catalogFetch.apply(this, arguments);
};
</script><script src=%q></script></head><body><script>
%s
__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var expected = %s;
  var names = Object.keys(expected).sort();
  var publicKeys = ["version", "component"].concat(names).join(",");
  assert(Object.keys(globalThis.kit).join(",") === publicKeys,
    "completed catalog keys were " + Object.keys(globalThis.kit).join(","));
  assert(Object.isFrozen(globalThis.kit), "completed catalog kit facade was mutable");
  assert(globalThis.kit.service === undefined && globalThis.kit.bridge === undefined &&
    !Object.prototype.hasOwnProperty.call(globalThis.kit, "service"),
    "completed catalog leaked service registrar or bridge");
  assert(document.querySelectorAll("script[src]").length === 1,
    "completed catalog loaded more than its one sealed artifact");
  names.forEach(function (name) {
    var namespace = globalThis.kit[name];
    var contract = expected[name];
    assert(namespace && Object.isFrozen(namespace), name + " namespace was missing or mutable");
    assert(namespace.version === contract.version, name + " version was " + namespace.version);
    assert(Object.keys(namespace).slice().sort().join(",") === contract.members.join(","),
      name + " members were " + Object.keys(namespace).slice().sort().join(","));
    assert(namespace.bridge === undefined && namespace.register === undefined,
      name + " leaked bridge or registration controls");
  });
  var graph = globalThis.kit[Symbol.for("kitjs:graph")];
  assert(graph && Object.isFrozen(graph) && Object.isFrozen(graph.services),
    "completed catalog graph metadata was missing or mutable");
  assert(Object.keys(graph.services).sort().join(",") === names.join(","),
    "completed graph services were " + Object.keys(graph.services).sort().join(","));
  names.forEach(function (name) {
    assert(graph.services[name] === expected[name].version, name + " graph version was wrong");
  });
  assert(globalThis.__catalogFetches === 0,
    "completed service catalog performed " + globalThis.__catalogFetches + " runtime fetches");
});
</script></body></html>`, assetPath, browserHarness, encoded)
}
