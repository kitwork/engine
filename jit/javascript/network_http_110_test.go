package javascript

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestNativeHTTPVersionedSourcesRemainImmutable(t *testing.T) {
	for _, item := range []struct{ name, version, hash string }{
		{"network", "1.0.0", "f16eb04267a4a97e4ea6220503ab887a87f80e42b524538c997ce6d92436683d"},
		{"files", "1.3.0", "199f42793e708f226ade7e376f071a82cf0d95a751a2c25086e8c25ba14d5821"},
		{"camera", "1.0.0", "b5be26f5bd5e4ae4e534328557007530cd639c0e7e88f09d15b021d8e1c4da33"},
		{"media", "1.0.0", "946981aadef58660027d433758f22cd67b51f39dc5f452f9147eb44d637b32aa"},
		{"network", "1.1.0", "5ffd817e56ffd2f08d8474c73d919ff6f96d2fefc87c2d2666abab679dbf0622"},
		{"files", "1.4.0", "3c69a1e8f38292ebcf7cc791542ce4901e30c47847b2a2d899d6bf11456dc1c3"},
		{"camera", "1.1.0", "835f1abacae7192753a301d8046410eb7c53d85c4cd156c4a9895c568ad2881f"},
		{"media", "1.1.0", "df60fe617b519f40fb42c99431d18331859c2dff8ff91a9c97e1e15b5bf9e515"},
	} {
		source := readVanillaFile(t, "service", item.name, item.version+".js")
		if got := fmt.Sprintf("%x", sha256.Sum256(source)); got != item.hash {
			t.Fatalf("%s@%s hash changed: %s", item.name, item.version, got)
		}
	}
}

func TestApp1150UpgradesSealedNativeHTTPGraph(t *testing.T) {
	if !bytes.Equal(readVanillaFile(t, "component", "app", "1.14.0.js"), readVanillaFile(t, "component", "app", "1.15.0.js")) {
		t.Fatal("app component behavior changed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	current, err := composer.ComposeHTML([]byte(`<html data-kit-component="app@1.15.0" data-kit-as="$app"></html>`))
	if err != nil {
		t.Fatal(err)
	}
	oldPins := serviceVersionMap(appComponentServices("1.14.0"))
	newPins := serviceVersionMap(appComponentServices("1.15.0"))
	for name, version := range map[string]string{"network": "1.1.0", "files": "1.4.0", "camera": "1.1.0", "media": "1.1.0"} {
		if newPins[name] != version {
			t.Fatalf("wrong %s pin: %s", name, newPins[name])
		}
		if !bytes.Contains(current.JavaScript, []byte(`services["`+name+`"] = "`+version+`"`)) {
			t.Fatalf("missing service pin %s", name)
		}
		if appGrantsAuthoredService("1.15.0", name) || bytes.Contains(current.JavaScript, []byte(`actions["`+name+`"]["`)) {
			t.Fatalf("%s gained authored actions", name)
		}
		newPins[name] = oldPins[name]
	}
	if !reflect.DeepEqual(oldPins, newPins) {
		t.Fatal("unrelated service versions changed")
	}
	networkAt := bytes.Index(current.JavaScript, []byte("KitJS service: network@1.1.0"))
	filesAt := bytes.Index(current.JavaScript, []byte("KitJS staged service: files@1.4.0"))
	cameraAt := bytes.Index(current.JavaScript, []byte("KitJS staged service: camera@1.1.0"))
	if networkAt < 0 || filesAt < networkAt || cameraAt < filesAt {
		t.Fatal("private dependency installation order changed")
	}
	legacy, err := composer.ComposeHTML([]byte(`<html data-kit-component="app@1.14.0"></html>`))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(legacy.JavaScript, []byte("network@1.1.0")) || bytes.Contains(legacy.JavaScript, []byte("files@1.4.0")) {
		t.Fatal("legacy graph silently upgraded")
	}
	for _, expression := range []string{"$app.network.request({})", "$app.files.upload({})"} {
		if _, err := composer.ComposeHTML([]byte(`<html data-kit-component="app@1.15.0" data-kit-as="$app"><button data-kit-click="` + expression + `"></button></html>`)); err == nil {
			t.Fatalf("authored expression gained HTTPS authority: %s", expression)
		}
	}
}

func TestCapabilityLab130ComposesWithApp1150(t *testing.T) {
	legacy := readVanillaFile(t, "component", "capability-lab", "1.2.0.js")
	current := readVanillaFile(t, "component", "capability-lab", "1.3.0.js")
	if ContentHash(legacy) != capabilityLab120SHA256 {
		t.Fatal("legacy capability lab source changed")
	}
	if strings.ReplaceAll(string(legacy), "capability-lab@1.2.0", "capability-lab@1.3.0") != string(current) {
		t.Fatal("capability lab behavior changed")
	}
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := composer.ComposeHTML([]byte(`<html data-kit-component="app@1.15.0"><section data-kit-component="capability-lab@1.3.0"></section></html>`))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{`components["capability-lab"] = "1.3.0"`, `services["network"] = "1.1.0"`, `services["files"] = "1.4.0"`, `services["camera"] = "1.1.0"`, `services["media"] = "1.1.0"`} {
		if !bytes.Contains(bundle.JavaScript, []byte(marker)) {
			t.Fatalf("current lab missing %s", marker)
		}
	}
	if _, err := composer.ComposeHTML([]byte(`<html data-kit-component="app@1.15.0"><section data-kit-component="capability-lab@1.2.0"></section></html>`)); err == nil {
		t.Fatal("mixed old and new native service graphs accepted")
	}
}

func TestNativeHTTPNodeContract(t *testing.T) {
	network := readVanillaFile(t, "service", "network", "1.1.0.js")
	files := readVanillaFile(t, "service", "files", "1.4.0.js")
	camera := readVanillaFile(t, "service", "camera", "1.1.0.js")
	media := readVanillaFile(t, "service", "media", "1.1.0.js")
	script := `
"use strict";
function assert(value, message) { if (!value) throw new Error(message); }
async function rejected(value) { try { await value; } catch (error) { return error; } throw new Error("unexpected success"); }
async function turns() { for (var i=0;i<10;i++) await Promise.resolve(); }
var nativeCalls = [], mode = "success", polls = 0, nextOperation = 0, beginResolve, pollResolve;
var metadata = {handle:"H".repeat(32),name:"photo.jpg",type:"image/jpeg",size:5,sha256:"a".repeat(64)};
var deadlines = new Map(), nextTimer = 0;
globalThis.setTimeout = function(callback, delay) {
  var token = ++nextTimer;
  if (delay === 150) queueMicrotask(callback);
  else if (delay === 35000 || delay === 600000) deadlines.set(token, callback);
  else throw new Error("unexpected timer delay " + delay);
  return token;
};
globalThis.clearTimeout = function(token) { deadlines.delete(token); };
var host = {call:function(action, params) {
  nativeCalls.push({action:action, params:params});
  if (action === "network.status") return {online:true};
  if (action === "network.beginRequest" || action === "network.beginUpload") {
    polls=0;
    if (mode === "late") return new Promise(function(resolve){beginResolve=resolve;});
    if (mode === "begin-failure") return Promise.reject({code:"ORIGIN_DENIED",message:"SECRET"});
    return {operation:String(++nextOperation).padStart(32,"O")};
  }
  if (action === "network.pollRequest") {
    if (mode === "waiting") return new Promise(function(resolve){pollResolve=resolve;});
    if (mode === "bad-response") return {state:"completed",response:{status:200,headers:{},body:42}};
    if (mode === "failed") return {state:"failed",code:"ADDRESS_DENIED"};
    if (++polls===1) return {state:"pending"};
    return {state:"completed",response:{status:201,headers:{"x-result":"ok"},body:"xin chào"}};
  }
  if (action === "network.releaseRequest") return true;
  if (action === "camera.beginCapture") return {operation:"C".repeat(32)};
  if (action === "camera.pollCapture") return {status:"ready",file:metadata};
  if (action === "camera.releaseCapture") return true;
  if (action === "files.release") return true;
  throw new Error("unexpected native method "+action);
}};
function install(nativeHost) {
  var graph={services:{network:"1.1.0",files:"1.4.0",camera:"1.1.0",media:"1.1.0",capabilities:"1.0.0"}};
  var assembly={graph:graph,nativeHost:nativeHost,nativeFiles:null};
  globalThis.document={};
  Object.defineProperty(document,Symbol.for("kitjs:assembly"),{value:assembly});
  var kit={capabilities:{supports:function(){return Promise.resolve(true);}},service:function(name,value){this[name]=Object.freeze(value);}};
` + string(network) + string(files) + string(camera) + string(media) + `
  assert(!Object.prototype.hasOwnProperty.call(assembly,Symbol.for("kitjs:network:upload:v1")),"upload boundary leaked");
  assert(!Object.prototype.hasOwnProperty.call(assembly,Symbol.for("kitjs:files:adopt:v1")),"file adopter leaked");
  return kit;
}
(async function(){
  var kit=install(host);
  var result=await kit.network.request({url:"https://api.example.com/notes",method:"POST",body:"hello",headers:{Authorization:"Bearer secret"}});
  assert(result.status===201 && result.body==="xin chào" && Object.isFrozen(result) && Object.isFrozen(result.headers),"response contract");
  var begin=nativeCalls.find(function(c){return c.action==="network.beginRequest";});
  assert(begin.params.timeoutMS===15000 && begin.params.headers.authorization==="Bearer secret" && begin.params.body==="hello","wire options");
  assert(nativeCalls.filter(function(c){return c.action==="network.releaseRequest";}).length===1,"completion did not release");
  var before=nativeCalls.length;
  for (var options of [
    {url:"http://api.example.com"},{url:"https://api.example.com",body:"x"},
    {url:"https://api.example.com",method:"TRACE"},{url:"https://api.example.com",timeoutMS:30001},
    {url:"https://api.example.com",headers:{Cookie:"secret"}},
    {url:"https://api.example.com",headers:{"X-Test":"a\r\nInjected: yes"}},
    {url:"https://api.example.com",method:"POST",body:"x".repeat(1048577)},
    {url:"https://api.example.com",method:"POST",body:"\ud800"},
    {get url(){throw new Error("accessor ran");}}
  ]) { var didThrow=false; try{kit.network.request(options);}catch(error){didThrow=error instanceof TypeError;} assert(didThrow,"invalid option accepted"); }
  assert(nativeCalls.length===before,"invalid request reached host");
  mode="failed";
  var error=await rejected(kit.network.request({url:"https://api.example.com"}));
  assert(error.code==="ADDRESS_DENIED" && error.name==="KitNetworkError","native error code lost");
  mode="begin-failure";
  error=await rejected(kit.network.request({url:"https://api.example.com"}));
  assert(error.code==="ORIGIN_DENIED" && !error.message.includes("SECRET"),"raw host error escaped");
  mode="bad-response";
  error=await rejected(kit.network.request({url:"https://api.example.com"}));
  assert(error.code==="INVALID_RESPONSE","malformed response accepted");
  var controller=new AbortController(); controller.abort(); before=nativeCalls.length;
  error=await rejected(kit.network.request({url:"https://api.example.com",signal:controller.signal}));
  assert(error.code==="CANCELLED" && nativeCalls.length===before,"preabort reached host");
  mode="late"; controller=new AbortController();
  var late=kit.network.request({url:"https://api.example.com",signal:controller.signal});
  controller.abort(); error=await rejected(late); assert(error.code==="CANCELLED","late begin cancellation");
  beginResolve({operation:"L".repeat(32)}); await turns();
  assert(nativeCalls.some(function(c){return c.action==="network.releaseRequest" && c.params.operation==="L".repeat(32);}),"late operation leaked");
  mode="waiting";
  var pending=kit.network.request({url:"https://api.example.com"}); await turns();
  var timers=Array.from(deadlines.values()); deadlines.clear(); timers.forEach(function(f){f();});
  error=await rejected(pending); assert(error.code==="TIMEOUT","deadline failed");
  pollResolve({state:"pending"}); await turns();
  var controllers=[], concurrent=[];
  for(var i=0;i<4;i++){var ctl=new AbortController();controllers.push(ctl);concurrent.push(rejected(kit.network.request({url:"https://api.example.com",signal:ctl.signal})));}
  error=await rejected(kit.network.request({url:"https://api.example.com"}));assert(error.code==="BUSY","concurrency unbounded");
  controllers.forEach(function(ctl){ctl.abort();});await Promise.all(concurrent);
  mode="success";
  var reference=await kit.camera.capture();
  result=await kit.files.upload(reference,{url:"https://api.example.com/upload"});
  assert(result.status===201,"FileRef upload failed");
  begin=nativeCalls.find(function(c){return c.action==="network.beginUpload";});
  assert(begin.params.handle===metadata.handle && begin.params.method==="POST" && !Object.prototype.hasOwnProperty.call(reference,"handle"),"opaque upload identity escaped");
  var forged=false;try{kit.files.upload({name:reference.name,type:reference.type,size:reference.size,sha256:reference.sha256},{url:"https://api.example.com/upload"});}catch(error){forged=error instanceof TypeError;}assert(forged,"forged FileRef uploaded");
  await kit.files.release(reference);
  var released=false;try{kit.files.upload(reference,{url:"https://api.example.com/upload"});}catch(error){released=error instanceof TypeError;}assert(released,"released FileRef uploaded");
  var noHost=install(null);
  error=await rejected(noHost.network.request({url:"https://api.example.com"}));assert(error.code==="UNAVAILABLE","browser fallback widened network authority");
  assert(deadlines.size===0,"deadline timers leaked");
})().catch(function(error){console.error(error && error.stack || error);process.exitCode=1;});
`
	runNativeHostNode(t, script)
}
