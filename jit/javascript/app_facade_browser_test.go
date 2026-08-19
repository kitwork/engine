package javascript

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCanonicalAppClosesOnlyAuthoredServiceGraph(t *testing.T) {
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeStandalone([]ComponentRef{{Name: "app", Version: "1.0.0"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"announce", "appearance", "clipboard", "cookie", "fullscreen", "navigation", "share", "storage"} {
		if !bytes.Contains(bundle.JavaScript, []byte(`services["`+name+`"] = "1.0.0";`)) {
			t.Errorf("app graph omitted %s@1.0.0", name)
		}
		if !bytes.Contains(bundle.JavaScript, []byte(`grants["app"]["`+name+`"] = "1.0.0";`)) {
			t.Errorf("app graph omitted direct %s grant", name)
		}
	}
	for _, name := range []string{"network", "progress", "request"} {
		if bytes.Contains(bundle.JavaScript, []byte(`services["`+name+`"] =`)) ||
			bytes.Contains(bundle.JavaScript, []byte(`grants["app"]["`+name+`"]`)) {
			t.Errorf("empty app unnecessarily selected blocked workflow service %s", name)
		}
	}
}

func TestAppFacadeGraphAndActionsAffectArtifactIdentity(t *testing.T) {
	storage := storageServicePackage(t)
	storage.Actions = []string{"set"}
	base := BuildOptions{
		Profile:    ProfileKit,
		Services:   []Service{storage},
		Components: []ComponentVersion{{Name: "app", Version: "1.0.0"}},
		ComponentRequires: []ComponentServiceRequirement{{
			Component: "app", Service: ServiceVersion{Name: "storage", Version: "1.0.0"},
		}},
		Scripts: []Script{{Name: "app", Source: []byte(";kit.component(\"app\", {});\n")}},
	}
	left, err := Build(base)
	if err != nil {
		t.Fatal(err)
	}
	base.Services[0].Actions = []string{"remove"}
	right, err := Build(base)
	if err != nil {
		t.Fatal(err)
	}
	if left.SHA256() == right.SHA256() {
		t.Fatal("authored action metadata did not affect artifact identity")
	}
	base.Services[0].Actions[0] = "clear"
	if bytes.Contains(left.Bytes(), []byte(`actions["storage"]["clear"]`)) {
		t.Fatal("authored action input aliases immutable artifact storage")
	}
}

func TestBuildRejectsMalformedAuthoredActionsAndLegacyThemeOwnerConflict(t *testing.T) {
	valid := storageServicePackage(t)
	for _, actions := range [][]string{{"set", "set"}, {"constructor"}, {"bad-name"}} {
		service := valid
		service.Actions = actions
		if _, err := Build(BuildOptions{Profile: ProfileKit, Services: []Service{service}}); err == nil {
			t.Fatalf("Build accepted authored actions %q", actions)
		}
	}
	appearance := Service{Name: "appearance", Version: "1.0.0", Source: readVanillaFile(t, "service", "appearance", "1.0.0.js")}
	if _, err := Build(BuildOptions{
		Profile: ProfileKit, Services: []Service{appearance},
		Components: []ComponentVersion{{Name: "theme", Version: "2.0.0"}},
	}); err == nil || !strings.Contains(err.Error(), "conflicts with document owner") {
		t.Fatalf("legacy theme/appearance conflict error = %v", err)
	}
	if _, err := Build(BuildOptions{
		Profile: ProfileKit, Services: []Service{appearance},
		Components: []ComponentVersion{{Name: "theme", Version: "3.0.0"}},
	}); err != nil {
		t.Fatalf("theme@3 + appearance rejected: %v", err)
	}
}

func TestAppServiceFacadeBrowserContract(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping app service facade browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	storage := storageServicePackage(t)
	storage.Actions = []string{"set", "remove"}
	artifact, err := Build(BuildOptions{
		Profile:    ProfileKit,
		Services:   []Service{storage},
		Components: []ComponentVersion{{Name: "app", Version: "1.0.0"}, {Name: "other", Version: "1.0.0"}},
		ComponentRequires: []ComponentServiceRequirement{
			{Component: "app", Service: ServiceVersion{Name: "storage", Version: "1.0.0"}},
			{Component: "other", Service: ServiceVersion{Name: "storage", Version: "1.0.0"}},
		},
		Scripts: []Script{
			{Name: "app", Source: []byte(";kit.component(\"app\", { value: \"clean\", save: function () { this.value = \"saved\"; } });\n")},
			{Name: "other", Source: []byte(";kit.component(\"other\", { value: \"clean\", storage: \"component-state\" });\n")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	document := `<!doctype html><html><head><meta charset="utf-8"><title>app facade</title></head><body>
<main data-kit-component="app" data-kit-version="1.0.0" data-kit-as=" $app ">
  <button id="allowed" data-kit-click="$app.storage.set('app-facade', 'yes')">allowed</button>
  <button id="allowed-sequence" data-kit-click="$app.storage.set('sequence-a', 'yes'); $app.storage.set('sequence-b', 'yes')">sequence</button>
  <button id="app-method" data-kit-click="$app.save()">app method</button>
  <output id="app-value" data-kit-text="value"></output>
  <button id="blocked-read" data-kit-click="$app.storage.get('app-facade')">read</button>
  <button id="blocked-computed" data-kit-click="$app.storage['set']('computed', 'bad')">computed</button>
  <button id="blocked-value" data-kit-click="value = $app.storage">value</button>
  <button id="blocked-logical" data-kit-click="true &amp;&amp; $app.storage.set('logical', 'bad')">logical</button>
  <button id="blocked-group" data-kit-click="($app.storage.set('grouped', 'bad'))">group</button>
  <button id="blocked-argument" data-kit-click="$app.storage.set('argument', $app.storage.set('nested', 'bad'))">argument</button>
  <button id="blocked-group-alias" data-kit-click="($app).storage.set('group-alias', 'bad')">group alias</button>
  <button id="blocked-assigned-alias" data-kit-click="value = $app; value.storage.set('assigned-alias', 'bad')">assigned alias</button>
  <button id="blocked-conditional-alias" data-kit-click="(true ? $app : $other).storage.set('conditional-alias', 'bad')">conditional alias</button>
  <button id="blocked-unqualified" data-kit-click="storage.set('unqualified', 'bad')">unqualified</button>
  <button id="blocked-kit" data-kit-click="kit.storage.set('kit', 'bad')">kit</button>
  <output id="binding" data-kit-text="$app.storage">server-binding</output>
</main>
<section data-kit-component="other" data-kit-version="1.0.0" data-kit-as="$other">
  <button id="other" data-kit-click="$other.storage.set('other', 'bad')">other</button>
  <output id="other-storage" data-kit-text="storage">server-other-storage</output>
</section>
<section data-kit-component="app" data-kit-version="1.0.0" data-kit-as="$wrong">
  <button id="wrong" data-kit-click="$wrong.storage.set('wrong', 'bad')">wrong</button>
</section>
<script>globalThis.__kitDiagnostics=[];var __oldError=console.error;console.error=function(){__kitDiagnostics.push(Array.prototype.map.call(arguments,String).join(' '));};</script>
<script src="/kit.js"></script><script>
` + browserHarness + `
__runStandaloneKitTest(async function(){
  var assert=__kitTestAssert,next=__kitTestNextTurn;
  var graph=globalThis.kit[Symbol.for("kitjs:graph")];
  assert(Object.isFrozen(graph.grants)&&Object.isFrozen(graph.grants.app)&&Object.isFrozen(graph.actions.storage),"graph grants/actions not frozen");
  assert(graph.grants.app.storage==="1.0.0"&&graph.actions.storage.set===true&&graph.actions.storage.get===undefined,"graph facade policy wrong");
  assert(document.getElementById("binding").textContent==="server-binding","$app service leaked into binding");
  assert(document.getElementById("other-storage").textContent==="component-state","non-app service dependency blocked a same-named component field");
  assert(__kitDiagnostics.length>=10,"invalid service structures were not rejected during preparation");
  document.getElementById("allowed").click(); await next();
  assert(localStorage.getItem("kit:app-facade")==='\"yes\"',"granted app storage action did not execute");
  document.getElementById("allowed-sequence").click(); await next();
  assert(localStorage.getItem("kit:sequence-a")==='\"yes\"'&&localStorage.getItem("kit:sequence-b")==='\"yes\"',"top-level service command sequence did not execute");
  document.getElementById("app-method").click(); await next();
  assert(document.getElementById("app-value").textContent==="saved","ordinary direct $app method stopped working");
  var before=__kitDiagnostics.length;
  document.getElementById("blocked-read").click();
  document.getElementById("blocked-computed").click();
  document.getElementById("blocked-value").click();
  document.getElementById("blocked-logical").click();
  document.getElementById("blocked-group").click();
  document.getElementById("blocked-argument").click();
  document.getElementById("blocked-group-alias").click();
  document.getElementById("blocked-assigned-alias").click();
  document.getElementById("blocked-conditional-alias").click();
  document.getElementById("blocked-unqualified").click();
  document.getElementById("blocked-kit").click();
  document.getElementById("other").click();
  document.getElementById("wrong").click(); await next();
  assert(__kitDiagnostics.length>=before+4,"runtime-only blocked facade paths did not fail closed");
  assert(localStorage.getItem("kit:computed")===null&&localStorage.getItem("kit:logical")===null&&localStorage.getItem("kit:grouped")===null&&localStorage.getItem("kit:argument")===null&&localStorage.getItem("kit:nested")===null&&localStorage.getItem("kit:group-alias")===null&&localStorage.getItem("kit:assigned-alias")===null&&localStorage.getItem("kit:conditional-alias")===null&&localStorage.getItem("kit:unqualified")===null&&localStorage.getItem("kit:kit")===null&&localStorage.getItem("kit:other")===null&&localStorage.getItem("kit:wrong")===null,"blocked facade path executed");
});</script></body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/kit.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(artifact.Bytes())
		case "/app.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(document))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/app.html")
}

func TestAppFacadeProjectsTheExactServiceNamespace(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping app service identity browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	probe := Service{
		Name: "probe", Version: "1.0.0", Actions: []string{"touch"},
		Source: []byte(`;kit.service("probe", {
  touch: function () { globalThis.__exactAppService = this === kit.probe; }
});
`),
	}
	artifact, err := Build(BuildOptions{
		Profile: ProfileKit, Services: []Service{probe},
		Components: []ComponentVersion{{Name: "app", Version: "1.0.0"}},
		ComponentRequires: []ComponentServiceRequirement{{
			Component: "app", Service: ServiceVersion{Name: "probe", Version: "1.0.0"},
		}},
		Scripts: []Script{{Name: "app", Source: []byte(";kit.component(\"app\", {});\n")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	document := `<!doctype html><html><head><meta charset="utf-8"><title>identity</title></head><body>
<main data-kit-component="app" data-kit-version="1.0.0" data-kit-as="$app">
  <button id="touch" data-kit-click="$app.probe.touch()">touch</button>
</main><script src="/kit.js"></script><script>` + browserHarness + `
__runStandaloneKitTest(async function(){
  document.getElementById("touch").click(); await __kitTestNextTurn();
  __kitTestAssert(globalThis.__exactAppService===true,"app facade wrapped or cloned the service namespace");
});</script></body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/kit.js" {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(artifact.Bytes())
			return
		}
		if request.URL.Path == "/identity.html" {
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(document))
			return
		}
		http.NotFound(response, request)
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/identity.html")
}

func TestAppFacadeRejectsConfusedAppAliasBrowser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping confused app alias browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	storage := storageServicePackage(t)
	storage.Actions = []string{"set"}
	artifact, err := Build(BuildOptions{
		Profile:    ProfileKit,
		Services:   []Service{storage},
		Components: []ComponentVersion{{Name: "app", Version: "1.0.0"}, {Name: "confuser", Version: "1.0.0"}},
		ComponentRequires: []ComponentServiceRequirement{{
			Component: "app", Service: ServiceVersion{Name: "storage", Version: "1.0.0"},
		}},
		Scripts: []Script{
			{Name: "app", Source: []byte(";kit.component(\"app\", {});\n")},
			{Name: "confuser", Source: []byte(`;kit.component("confuser", {
  storage: { set: function () { globalThis.__confusedAppAliasExecuted = true; } }
});
`)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	document := `<!doctype html><html><head><meta charset="utf-8"><title>confused app alias</title></head><body>
<main data-kit-component="confuser" data-kit-version="1.0.0" data-kit-as="$app">
  <button id="confused" data-kit-click="$app.storage.set('confused', 'bad')">confused</button>
</main>
<script>globalThis.__kitDiagnostics=[];var __oldError=console.error;console.error=function(){__kitDiagnostics.push(Array.prototype.map.call(arguments,String).join(' '));};</script>
<script src="/kit.js"></script><script>` + browserHarness + `
__runStandaloneKitTest(async function(){
  var before=__kitDiagnostics.length;
  document.getElementById("confused").click(); await __kitTestNextTurn();
  __kitTestAssert(globalThis.__confusedAppAliasExecuted!==true,"non-app component inherited the $app service command");
  __kitTestAssert(__kitDiagnostics.length>before,"confused $app alias did not fail closed");
});</script></body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/kit.js" {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(artifact.Bytes())
			return
		}
		_, _ = response.Write([]byte(document))
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/confused.html")
}

func TestGrantedServiceNameCollisionFailsArtifactLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping app service collision browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	storage := storageServicePackage(t)
	storage.Actions = []string{"set"}
	artifact, err := Build(BuildOptions{
		Profile: ProfileKit, Services: []Service{storage},
		Components: []ComponentVersion{{Name: "app", Version: "1.0.0"}},
		ComponentRequires: []ComponentServiceRequirement{{
			Component: "app", Service: ServiceVersion{Name: "storage", Version: "1.0.0"},
		}},
		Scripts: []Script{{Name: "app", Source: []byte(";kit.component(\"app\", { storage: null });\n")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	document := `<!doctype html><html><head><meta charset="utf-8"><title>collision</title>
<script>window.addEventListener("error",function(event){
  var message=String(event.error&&event.error.message||event.message||"");
  if(message.indexOf("conflicts with a granted service")>=0&&globalThis.kit===undefined){
    document.documentElement.setAttribute("data-kit-test","passed");event.preventDefault();
  }
});</script><script src="/kit.js"></script></head><body></body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/kit.js" {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(artifact.Bytes())
			return
		}
		_, _ = response.Write([]byte(document))
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/collision.html")
}
