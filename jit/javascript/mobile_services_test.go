package javascript

import (
	"bytes"
	"testing"
)

func TestSealedMobileServicePackagesStaticContract(t *testing.T) {
	for _, contract := range []struct {
		name    string
		members []string
	}{
		{name: "capabilities", members: []string{"supports"}},
		{name: "device", members: []string{"info", "vibrate"}},
		{name: "files", members: []string{"importBlob", "stat", "toBlob", "share", "release"}},
		{name: "secureStorage", members: []string{"get", "set", "remove"}},
		{name: "shell", members: []string{"open"}},
	} {
		source := readVanillaFile(t, "service", contract.name, "1.0.0.js")
		if len(source) == 0 || source[0] != ';' || source[len(source)-1] != '\n' || bytes.Contains(source, []byte{'\r'}) {
			t.Fatalf("%s@1.0.0 is not a sealable LF-only classic script", contract.name)
		}
		if got := bytes.Count(source, []byte(`kit.service("`+contract.name+`"`)); got != 1 {
			t.Fatalf("%s@1.0.0 registration count = %d, want one", contract.name, got)
		}
		for _, member := range contract.members {
			if !bytes.Contains(source, []byte(member+": "+member)) {
				t.Fatalf("%s@1.0.0 omitted sealed member %s", contract.name, member)
			}
		}
		for _, forbidden := range [][]byte{
			[]byte(`globalThis.kit`), []byte(`global.kit`), []byte(`window.kit`),
			[]byte(`kit.component(`), []byte(`fetch(`),
			[]byte(`.bridge`), []byte(`dispatch:`),
		} {
			if bytes.Contains(source, forbidden) {
				t.Fatalf("%s@1.0.0 contains forbidden public coupling %q", contract.name, forbidden)
			}
		}
		if contract.name != "files" && bytes.Contains(source, []byte(`XMLHttpRequest`)) {
			t.Fatalf("%s@1.0.0 contains forbidden eager browser transport", contract.name)
		}
	}
	files110 := readVanillaFile(t, "service", "files", "1.1.0.js")
	if !bytes.Contains(files110, []byte("pick: pick")) {
		t.Fatal("files@1.1.0 omitted sealed member pick")
	}
	files120 := readVanillaFile(t, "service", "files", "1.2.0.js")
	if !bytes.Contains(files120, []byte("export: exportFile")) {
		t.Fatal("files@1.2.0 omitted sealed member export")
	}
	if bytes.Contains(files110, []byte("export: exportFile")) {
		t.Fatal("files@1.1.0 changed after files@1.2.0 introduced export")
	}

	catalog, err := loadDeliveryCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range sealedApp110Services {
		versions, exists := catalog.services[name]
		if !exists {
			t.Fatalf("sealed app service %s is absent from the catalog", name)
		}
		service, exists := versions["1.0.0"]
		if !exists || service.identity.Version != "1.0.0" {
			t.Fatalf("sealed app service %s@1.0.0 is absent from the catalog", name)
		}
		if len(service.actions) != 0 || appGrantsAuthoredService("1.1.0", name) {
			t.Fatalf("sealed app service %s escaped into authored actions", name)
		}
	}
	filesVersion, exists := catalog.services["files"]["1.1.0"]
	if !exists || filesVersion.identity != (ServiceVersion{Name: "files", Version: "1.1.0"}) || len(filesVersion.actions) != 0 {
		t.Fatal("files@1.1.0 is absent from the sealed exact-version catalog")
	}
	filesVersion, exists = catalog.services["files"]["1.2.0"]
	if !exists || filesVersion.identity != (ServiceVersion{Name: "files", Version: "1.2.0"}) || len(filesVersion.actions) != 0 {
		t.Fatal("files@1.2.0 is absent from the sealed exact-version catalog")
	}
}

func TestSealedMobileServicesNativeNodeContract(t *testing.T) {
	nativeSource, err := sources.ReadFile(nativeHostRuntimeFragment)
	if err != nil {
		t.Fatal(err)
	}
	capabilities := readVanillaFile(t, "service", "capabilities", "1.0.0.js")
	clipboard := readVanillaFile(t, "service", "clipboard", "1.0.0.js")
	device := readVanillaFile(t, "service", "device", "1.0.0.js")
	network := readVanillaFile(t, "service", "network", "1.0.0.js")
	secureStorage := readVanillaFile(t, "service", "secureStorage", "1.0.0.js")
	share := readVanillaFile(t, "service", "share", "1.0.0.js")
	shell := readVanillaFile(t, "service", "shell", "1.0.0.js")

	script := `
"use strict";
var ASSEMBLY = Symbol.for("kitjs:assembly");
var HOST = Symbol.for("kitwork:native:host:v1");
var NO_HOST = {};
function assert(value, message) { if (!value) throw new Error(message); }
async function rejected(promise) {
  try { await promise; } catch (error) { return error; }
  throw new Error("promise unexpectedly resolved");
}
function installTransport(seed) {
  var document = {};
  var core = {
    phase: "events",
    reuse: false,
    blocked: function (name) { return name === "window" || name === "globalThis"; },
    validServiceName: function (name) {
      return ["capabilities", "clipboard", "device", "network", "secureStorage", "share", "shell"].indexOf(name) >= 0;
    },
    installComponentGraph: function (source) { return source; },
    installStagedDelivery: function (source) { return source; },
    beginComponentHandoff: function () { return false; }
  };
  Object.defineProperty(document, ASSEMBLY, { value: core, configurable: true });
  if (seed !== NO_HOST) Object.defineProperty(document, HOST, { value: seed, configurable: true });
` + string(nativeSource) + `
  return { core: core, document: document };
}
function installServices(installed) {
  globalThis.document = installed.document;
  var namespaces = Object.create(null);
  var kit = { service: function (name, namespace) { namespaces[name] = Object.freeze(namespace); } };
` + string(capabilities) + string(clipboard) + string(device) + string(network) +
		string(secureStorage) + string(share) + string(shell) + `
  return namespaces;
}
(async function () {
  var browserClipboardCalls = 0;
  var browserShareCalls = 0;
  Object.defineProperty(globalThis, "navigator", {
    configurable: true,
    value: {
      onLine: true,
      clipboard: {
        writeText: function () { browserClipboardCalls++; return Promise.resolve(); },
        readText: function () { browserClipboardCalls++; return Promise.resolve("browser"); }
      },
      share: function () { browserShareCalls++; return Promise.resolve(); },
      canShare: function () { return true; }
    }
  });
  globalThis.location = { href: "https://app.test/home" };
  globalThis.addEventListener = function () {};
  globalThis.removeEventListener = function () {};
  globalThis.File = function File(parts, name, options) {
    this.name = name;
    this.type = options && options.type || "";
    this.size = String(parts && parts[0] || "").length;
  };

  var calls = [];
  var values = new Map();
  var failureAction = "";
  var invalidAction = "";
  var seeded = {
    version: "1.0.0",
    call: function (action, params) {
      calls.push({ action: action, params: params, receiver: this === seeded });
      if (action === failureAction) return Promise.reject({ code: "DENIED", message: "raw native secret" });
      if (action === invalidAction) {
        if (action === "device.info") return { platform: "android", osVersion: "16", model: "Pixel", secret: true };
        if (action === "network.status") return { online: "yes" };
        if (action === "clipboard.readText") return "\uD800";
        return { invalid: true };
      }
      if (action === "capabilities.supports") return true;
      if (action === "clipboard.writeText") return true;
      if (action === "clipboard.readText") return "native clipboard";
      if (action === "device.info") return { platform: "android", osVersion: "16", model: "Pixel" };
      if (action === "device.vibrate") return true;
      if (action === "network.status") return { online: false };
      if (action === "secureStorage.get") return values.has(params.key) ? values.get(params.key) : null;
      if (action === "secureStorage.set") { values.set(params.key, params.value); return true; }
      if (action === "secureStorage.remove") { var existed = values.delete(params.key); return existed; }
      if (action === "share.open" || action === "shell.open") return true;
      throw new Error("unexpected native action " + action);
    }
  };
  var installed = installTransport(seeded);
  var services = installServices(installed);
  assert(!Object.prototype.hasOwnProperty.call(installed.document, HOST), "raw native host slot survived capture");
  assert(await services.capabilities.supports("capabilities.supports") === true,
    "capabilities.supports did not reach the native host");
  assert(await services.device.info().then(function (value) {
    return Object.isFrozen(value) && value.platform === "android" && value.osVersion === "16" && value.model === "Pixel";
  }), "device.info did not return an exact frozen snapshot");
  assert(await services.device.vibrate([10, 20]) === true, "device.vibrate changed its native result");
  assert((await services.network.status()).online === false, "network.status did not use the native result");
  assert(await services.secureStorage.get("notes.primary") === null, "missing secure value did not stay null");
  assert(await services.secureStorage.set("notes.primary", "private") === true,
    "secure storage set failed");
  assert(await services.secureStorage.get("notes.primary") === "private",
    "secure storage get failed");
  assert(await services.secureStorage.remove("notes.primary") === true,
    "secure storage remove failed");
  assert(await services.clipboard.writeText("native") === undefined &&
    await services.clipboard.readText() === "native clipboard", "clipboard did not stay on the native path");
  assert(services.share.canShare({ text: "native" }) === true &&
    await services.share.open({ title: "Kitwork", text: "native" }) === true,
    "share did not stay on the native path");
  assert(await services.shell.open("https://example.test/open") === true, "shell.open failed");
  assert(browserClipboardCalls === 0 && browserShareCalls === 0,
    "a present native host fell back to browser clipboard/share");
  assert(calls.every(function (call) { return call.receiver && Object.isFrozen(call.params); }),
    "native service receiver or frozen payload boundary drifted");
  assert(calls.some(function (call) {
    return call.action === "device.vibrate" && Object.isFrozen(call.params.pattern) &&
      call.params.pattern.join(",") === "10,20";
  }), "device vibration pattern was not cloned and frozen");
  assert(calls.some(function (call) {
    return call.action === "shell.open" && call.params.url === "https://example.test/open";
  }), "shell URL was not canonical and exact");

  var before = calls.length;
  var typeFailures = [];
  try { services.capabilities.supports("bad"); } catch (error) { typeFailures.push(error instanceof TypeError); }
  try { services.device.vibrate([10001]); } catch (error) { typeFailures.push(error instanceof TypeError); }
  try { services.device.vibrate(new Array(32).fill(2000)); } catch (error) { typeFailures.push(error instanceof TypeError); }
  try { services.secureStorage.get("../escape"); } catch (error) { typeFailures.push(error instanceof TypeError); }
  try { services.secureStorage.set("safe", "x".repeat(1024 * 1024 + 1)); } catch (error) { typeFailures.push(error instanceof TypeError); }
  try { services.secureStorage.set("safe", "\uD800"); } catch (error) { typeFailures.push(error instanceof TypeError); }
  try { services.shell.open("javascript:alert(1)"); } catch (error) { typeFailures.push(error instanceof TypeError); }
  try { services.shell.open("https://example.test/\uD800"); } catch (error) { typeFailures.push(error instanceof TypeError); }
  try { services.clipboard.writeText("\uD800"); } catch (error) { typeFailures.push(error instanceof TypeError); }
  try { services.clipboard.writeText("\uDC00"); } catch (error) { typeFailures.push(error instanceof TypeError); }
  try { services.clipboard.writeText("before\0after"); } catch (error) { typeFailures.push(error instanceof TypeError); }
  try { services.share.open({ title: "\uD800" }); } catch (error) { typeFailures.push(error instanceof TypeError); }
  try { services.share.open({ text: "\uDC00" }); } catch (error) { typeFailures.push(error instanceof TypeError); }
  try { services.share.open({ title: "before\0after" }); } catch (error) { typeFailures.push(error instanceof TypeError); }
  try { services.share.open({ text: "before\0after" }); } catch (error) { typeFailures.push(error instanceof TypeError); }
  try { services.share.open({ url: "https://example.test/\uD800" }); } catch (error) { typeFailures.push(error instanceof TypeError); }
  try { services.share.open({ url: "https://example.test/before\0after" }); } catch (error) { typeFailures.push(error instanceof TypeError); }
  assert(typeFailures.length === 17 && typeFailures.every(Boolean) && calls.length === before,
    "invalid service parameters reached the native host");

  failureAction = "secureStorage.get";
  var failure = await rejected(services.secureStorage.get("safe"));
  assert(failure.name === "KitSecureStorageError" && failure.code === "DENIED" &&
    failure.operation === "get" && failure.message.indexOf("raw native secret") < 0 && Object.isFrozen(failure),
    "secure storage denial was not stably normalized");
  failureAction = "share.open";
  failure = await rejected(services.share.open({ text: "denied" }));
  assert(failure.name === "KitShareError" && failure.code === "DENIED" && browserShareCalls === 0,
    "native share denial fell back or escaped normalization");
  failureAction = "clipboard.readText";
  failure = await rejected(services.clipboard.readText());
  assert(failure.name === "KitClipboardError" && failure.code === "DENIED" && browserClipboardCalls === 0,
    "native clipboard denial fell back or escaped normalization");
  failureAction = "";

  invalidAction = "device.info";
  failure = await rejected(services.device.info());
  assert(failure.name === "KitDeviceError" && failure.code === "FAILED", "invalid device result escaped validation");
  invalidAction = "network.status";
  failure = await rejected(services.network.status());
  assert(failure.name === "KitNetworkError" && failure.code === "FAILED", "invalid network result escaped validation");
  invalidAction = "clipboard.readText";
  failure = await rejected(services.clipboard.readText());
  assert(failure.name === "KitClipboardError" && failure.code === "FAILED",
    "malformed UTF-16 clipboard result escaped validation");
  invalidAction = "shell.open";
  failure = await rejected(services.shell.open("https://example.test/"));
  assert(failure.name === "KitShellError" && failure.code === "FAILED", "invalid shell result escaped validation");
  invalidAction = "";

  var file = new File(["content"], "note.txt", { type: "text/plain" });
  before = calls.length;
  failure = await rejected(services.share.open({ text: "no native file bypass", files: [file] }));
  assert(failure.name === "KitShareError" && failure.code === "UNAVAILABLE" && calls.length === before &&
    browserShareCalls === 0, "native file sharing bypassed the manifest host boundary");
  ["capabilities", "clipboard", "device", "network", "secureStorage", "share", "shell"].forEach(function (name) {
    assert(services[name].bridge === undefined && services[name].nativeHost === undefined &&
      services[name].dispatch === undefined, name + " leaked native transport controls");
  });

  var noHostServices = installServices(installTransport(NO_HOST));
  assert(await noHostServices.capabilities.supports("network.status") === false,
    "capability negotiation without a host did not fail closed");
  assert((await noHostServices.network.status()).online === true,
    "browser network.status did not preserve the browser snapshot");
  failure = await rejected(noHostServices.device.info());
  assert(failure.name === "KitDeviceError" && failure.code === "UNAVAILABLE",
    "device.info without a host did not report stable unavailability");
  failure = await rejected(noHostServices.secureStorage.get("safe"));
  assert(failure.name === "KitSecureStorageError" && failure.code === "UNAVAILABLE",
    "secure storage without a host did not report stable unavailability");
  failure = await rejected(noHostServices.shell.open("https://example.test/"));
  assert(failure.name === "KitShellError" && failure.code === "UNAVAILABLE",
    "shell without a host did not report stable unavailability");
})().catch(function (error) {
  console.error(error && error.stack || error);
  process.exitCode = 1;
});
`
	runNativeHostNode(t, script)
}
