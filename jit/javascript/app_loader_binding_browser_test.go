package javascript

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAppLoaderCrossBoundaryBindingLifecycleBrowser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping app loader cross-boundary browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeStandalone([]ComponentRef{{Name: "app", Version: "1.1.0"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	document := `<!doctype html><html data-kit-component="app" data-kit-version="1.1.0" data-kit-as="$app"><head><meta charset="utf-8"><title>app loader binding</title></head><body data-kit-scope="loader: 'body-clean'">
<output id="visible" data-kit-text="$app.loader.visible"></output>
<output id="value" data-kit-text="$app.loader.value === null ? 'pending' : $app.loader.value + '%'"></output>
<div id="shown" data-kit-show="!$app.loader.visible"></div>
<section id="removable" data-kit-scope="nestedLocal: 0"><output id="nested" data-kit-text="$app.loader.value"></output></section>
<output id="grouped" data-kit-text="($app.loader.value)">grouped-server</output>
<output id="computed" data-kit-text="$app.loader['value']">computed-server</output>
<output id="object" data-kit-text="$app.loader">object-server</output>
<output id="argument" data-kit-text="String($app.loader.value)">argument-server</output>
<output id="sibling-call" data-kit-text="$app.loader.visible ? loader.trim() : 'idle'">call-server</output>
<output id="sibling-computed" data-kit-text="$app.loader.visible ? loader[0] : 'idle'">computed-sibling-server</output>
<output id="sibling-group" data-kit-text="$app.loader.visible ? (loader) : 'idle'">group-sibling-server</output>
<output id="sibling-array" data-kit-text="$app.loader.visible ? [loader] : 'idle'">array-server</output>
<output id="sibling-object" data-kit-text="$app.loader.visible ? { value: loader } : 'idle'">object-sibling-server</output>
<output id="ordinary-member" data-kit-text="$app.loader.visible ? loader.length : 'idle'"></output>
<button id="action-loader" data-kit-click="loader = $app.loader">action loader</button>
<button id="start" data-kit-click="$app.progress.start('load', { total: 100 })">start</button>
<button id="update" data-kit-click="$app.progress.update('load', 42, 100)">update</button>
<button id="finish" data-kit-click="$app.progress.finish('load', 'loaded')">finish</button>
<button id="cancel-start" data-kit-click="$app.progress.start('cancel', { total: 10 })">cancel start</button>
<button id="cancel-finish" data-kit-click="$app.progress.finish('cancel', 'cancelled')">cancel finish</button>
<script>globalThis.__kitDiagnostics=[];var __oldError=console.error;console.error=function(){__kitDiagnostics.push(Array.prototype.map.call(arguments,String).join(' '));};var __define=Object.defineProperty;Object.defineProperty=function(target,key,descriptor){if(target===document&&key===Symbol.for("kitjs:assembly")&&descriptor.value&&descriptor.value.phase==="core")globalThis.__kitCore=descriptor.value;return __define(target,key,descriptor);};</script>
<script src="/kit.js"></script><script>Object.defineProperty=__define;</script><script>` + browserHarness + `
__runStandaloneKitTest(async function(){
  var assert=__kitTestAssert,next=__kitTestNextTurn;
  var core=globalThis.__kitCore;
  assert(document.getElementById("visible").textContent==="false","loader initial visible binding failed");
  assert(document.getElementById("value").textContent==="pending","loader initial value binding failed");
  assert(document.getElementById("shown").hidden===false,"loader unary binding failed");
  assert(document.getElementById("grouped").textContent==="grouped-server"&&document.getElementById("computed").textContent==="computed-server"&&document.getElementById("object").textContent==="object-server"&&document.getElementById("argument").textContent==="argument-server"&&document.getElementById("sibling-call").textContent==="call-server"&&document.getElementById("sibling-computed").textContent==="computed-sibling-server"&&document.getElementById("sibling-group").textContent==="group-sibling-server"&&document.getElementById("sibling-array").textContent==="array-server"&&document.getElementById("sibling-object").textContent==="object-sibling-server","invalid loader binding did not fail closed");
  assert(document.getElementById("ordinary-member").textContent==="idle","safe ordinary member sibling was rejected");
  document.getElementById("action-loader").click(); await next();
  var app=core.scopeRecordFor(document.documentElement),body=core.scopeRecordFor(document.body),nested=core.scopeRecordFor(document.getElementById("removable"));
  assert(core.elementRecord(document.getElementById("action-loader")).programs["data-kit-click"]===null&&body.scope.loader==="body-clean","action extracted the binding-only loader view model");
  assert(app.loaderDependents.has(body)&&app.loaderDependents.has(nested),"loader binding did not register cross-boundary dependents");
  var initialLoader=app.scope.loader; assert(Object.isFrozen(initialLoader),"initial loader snapshot is mutable");
  document.getElementById("start").click(); await next();
  assert(app.scope.loader!==initialLoader&&Object.isFrozen(app.scope.loader),"loader start did not replace with a frozen snapshot");
  assert(document.getElementById("visible").textContent==="true"&&document.getElementById("value").textContent==="pending"&&document.getElementById("shown").hidden===true,"loader start did not rerender nested body scope");
  assert(document.getElementById("ordinary-member").textContent==="10","safe ordinary member sibling did not rerender");
  var startLoader=app.scope.loader; document.getElementById("update").click(); await next();
  assert(app.scope.loader!==startLoader&&Object.isFrozen(app.scope.loader),"loader update did not replace with a frozen snapshot");
  assert(document.getElementById("value").textContent==="42%"&&document.getElementById("nested").textContent==="42","loader progress did not rerender dependents");
  document.getElementById("finish").click(); await next();
  assert(document.getElementById("value").textContent==="100%","loaded finish did not expose 100 percent");
  await new Promise(function(resolve){setTimeout(resolve,350);}); await next();
  assert(document.getElementById("visible").textContent==="false"&&document.getElementById("value").textContent==="pending","loaded finish did not hide loader");
  document.getElementById("cancel-start").click(); document.getElementById("cancel-finish").click(); await next();
  assert(document.getElementById("visible").textContent==="false","cancelled loader remained visible");
  var removable=document.getElementById("removable"); removable.remove(); await next();
  assert(nested.disposed===true&&!app.loaderDependents.has(nested)&&nested.loaderSources===null,"disposed binding retained loader dependency roots");
});</script></body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/kit.js" {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(bundle.JavaScript)
			return
		}
		if request.URL.Path == "/app.html" {
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = response.Write([]byte(document))
			return
		}
		http.NotFound(response, request)
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/app.html")
}

func TestAppLoaderBindingRequiresCanonicalApp110Browser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping app loader identity browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeStandalone([]ComponentRef{{Name: "app", Version: "1.0.0"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	future, err := Build(BuildOptions{
		Profile:    ProfileKit,
		Components: []ComponentVersion{{Name: "app", Version: "1.2.0"}},
		Scripts: []Script{{Name: "app", Source: []byte(`;kit.component("app", {
  loader: Object.freeze({ visible: true, value: 77 }),
  init: function () { this.loader = Object.freeze({ visible: true, value: 77 }); }
});
`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		version string
		script  []byte
	}{
		{version: "1.0.0", script: bundle.JavaScript},
		{version: "1.2.0", script: future.Bytes()},
	} {
		t.Run(test.version, func(t *testing.T) {
			document := `<!doctype html><html data-kit-component="app" data-kit-version="` + test.version + `" data-kit-as="$app"><head><meta charset="utf-8"><title>noncanonical app</title></head><body data-kit-scope="local: 0"><output id="old" data-kit-text="$app.loader.visible">server-old</output><script>globalThis.__kitDiagnostics=[];console.error=function(){__kitDiagnostics.push(1);};</script><script src="/kit.js"></script><script>` + browserHarness + `__runStandaloneKitTest(async function(){__kitTestAssert(document.getElementById("old").textContent==="server-old","noncanonical app projected loader binding");__kitTestAssert(__kitDiagnostics.length>0,"noncanonical app loader rejection was silent");});</script></body></html>`
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.URL.Path == "/kit.js" {
					response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
					_, _ = response.Write(test.script)
					return
				}
				if request.URL.Path == "/old.html" {
					response.Header().Set("Content-Type", "text/html; charset=utf-8")
					_, _ = response.Write([]byte(document))
					return
				}
				http.NotFound(response, request)
			}))
			defer server.Close()
			runVanillaBrowser(t, browser, server.URL+"/old.html")
		})
	}
}
