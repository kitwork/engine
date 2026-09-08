package javascript

import (
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNativeHTTPStagedBrowserContract(t *testing.T) {
	if testing.Short() {
		t.Skip("staged HTTPS browser contract skipped in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	options, err := composer.stagedBuildOptions(ScanResult{Components: []ComponentRef{{Name: "app", Version: "1.15.0"}}, NeedsRuntime: true}, ProfileKit, nil)
	if err != nil {
		t.Fatal(err)
	}
	assembly, err := BuildStaged(options)
	if err != nil {
		t.Fatal(err)
	}
	assets := make(map[string][]byte)
	var tags strings.Builder
	for _, artifact := range assembly.Artifacts() {
		assets["/jit/"+artifact.Name()] = artifact.Bytes()
		tags.WriteString(`<script data-kitwork-jit="` + html.EscapeString(string(artifact.Role())) + `" data-kitwork-hash="` + artifact.SHA256() + `" src="/jit/` + html.EscapeString(artifact.Name()) + `" integrity="` + html.EscapeString(artifact.Integrity()) + `" crossorigin="anonymous" defer></script>` + "\n")
	}
	page := `<!doctype html><html><head><meta charset="utf-8"><title>Native HTTPS contract</title>
<script>
(function(){
  var calls = globalThis.__httpCalls = [];
  var host = Object.freeze({version:"1.0.0",call:function(action,params){
    calls.push({action:action,params:params,receiver:this===host});
    if(action==="network.beginRequest" || action==="network.beginUpload")return {operation:"O".repeat(32)};
    if(action==="network.pollRequest")return {state:"completed",response:{status:200,headers:{"content-type":"application/json"},body:'{"ok":true}'}};
    if(action==="network.releaseRequest")return true;
    if(action==="camera.beginCapture")return {operation:"C".repeat(32)};
    if(action==="camera.pollCapture")return {status:"ready",file:{handle:"H".repeat(32),name:"photo.jpg",type:"image/jpeg",size:3,sha256:"a".repeat(64)}};
    if(action==="camera.releaseCapture" || action==="files.release")return true;
    return Promise.reject({code:"UNAVAILABLE"});
  }});
  Object.defineProperty(document,Symbol.for("kitwork:native:host:v1"),{value:host,configurable:true});
})();
</script>` + tags.String() + `
<script>
document.addEventListener("DOMContentLoaded",function(){
  var root=document.documentElement;
  function assert(value,message){if(!value)throw new Error(message);}
  Promise.resolve().then(async function(){
    var kit=globalThis.kit, graph=kit && kit[Symbol.for("kitjs:graph")];
    assert(kit && Object.isFrozen(kit) && graph.services.network==="1.1.0" && graph.services.files==="1.4.0","new graph not published");
    assert(document[Symbol.for("kitjs:assembly")]===undefined && document[Symbol.for("kitwork:native:host:v1")]===undefined,"private assembly leaked");
    assert(graph.actions.network.request===undefined && graph.actions.files.upload===undefined,"authored HTTPS authority leaked");
    var response=await kit.network.request({url:"https://api.example.com/v1",headers:{Authorization:"Bearer secret"}});
    assert(response.status===200 && response.body==='{"ok":true}' && Object.isFrozen(response.headers),"HTTPS response lost");
    var ref=await kit.camera.capture();
    response=await kit.files.upload(ref,{url:"https://api.example.com/upload"});
    assert(response.status===200 && ref.handle===undefined,"upload lost opaque file boundary");
    var upload=__httpCalls.find(function(c){return c.action==="network.beginUpload";});
    assert(upload && upload.receiver && Object.isFrozen(upload.params) && upload.params.handle==="H".repeat(32),"native transport changed upload identity");
    assert(__httpCalls.filter(function(c){return c.action==="network.releaseRequest";}).length===2,"native requests not released");
    await kit.files.release(ref);
    root.setAttribute("data-kit-test","passed");
  }).catch(function(error){root.setAttribute("data-kit-test","failed");root.setAttribute("data-kit-test-error",String(error && error.message || error));});
},{once:true});
</script></head><body><main data-kit-component="app@1.15.0" data-kit-as="$app"></main></body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if source, ok := assets[r.URL.Path]; ok {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = w.Write(source)
			return
		}
		if r.URL.Path == "/network.html" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(page))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	runVanillaBrowser(t, browser, server.URL+"/network.html")
}
