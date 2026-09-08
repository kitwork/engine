package javascript

import (
	"bytes"
	"strings"
	"testing"
)

func lifecycleServicePackage(t *testing.T) Service {
	t.Helper()
	return Service{Name: "lifecycle", Version: "1.0.0", Source: readVanillaFile(t, "service", "lifecycle", "1.0.0.js")}
}

func deepLinksServicePackage(t *testing.T) Service {
	t.Helper()
	return Service{Name: "deepLinks", Version: "1.0.0", Source: readVanillaFile(t, "service", "deepLinks", "1.0.0.js")}
}

func TestLifecycleAndDeepLinksSourcesAreClosed(t *testing.T) {
	for _, service := range []Service{lifecycleServicePackage(t), deepLinksServicePackage(t)} {
		if !bytes.HasPrefix(bytes.TrimSpace(service.Source), []byte(";")) || bytes.Contains(service.Source, []byte{'\r'}) {
			t.Fatalf("%s@1.0.0 is not a sealable LF-only classic script", service.Name)
		}
		if got := bytes.Count(service.Source, []byte(`kit.service("`+service.Name+`"`)); got != 1 {
			t.Fatalf("%s registration count=%d, want one", service.Name, got)
		}
		for _, forbidden := range [][]byte{
			[]byte("fetch("), []byte("XMLHttpRequest"), []byte("localStorage"),
			[]byte("sessionStorage"), []byte("kit.component("), []byte("data-kit"),
		} {
			if bytes.Contains(service.Source, forbidden) {
				t.Fatalf("%s widened into forbidden dependency %q", service.Name, forbidden)
			}
		}
	}
	deepSource := deepLinksServicePackage(t).Source
	for _, marker := range [][]byte{
		[]byte(`nativeHost.call("deepLinks.snapshot", {})`),
		[]byte(`"kitwork:deep-link"`),
		[]byte(`MAX_PATH_BYTES = 2048`),
		[]byte(`kit.service("deepLinks", { snapshot: snapshot, subscribe: subscribe })`),
	} {
		if !bytes.Contains(deepSource, marker) {
			t.Fatalf("deepLinks@1.0.0 lost contract %q", marker)
		}
	}
	for _, forbidden := range [][]byte{
		[]byte(`location.href`), []byte(`location.assign`), []byte(`history.pushState`),
		[]byte(`setInterval`), []byte(`setTimeout`), []byte(`rawURL`),
	} {
		if bytes.Contains(deepSource, forbidden) {
			t.Fatalf("deepLinks@1.0.0 gained forbidden behavior %q", forbidden)
		}
	}
}

func TestLifecycleAndDeepLinksBuildAsSealedServices(t *testing.T) {
	services := []Service{lifecycleServicePackage(t), deepLinksServicePackage(t)}
	artifact, err := Build(BuildOptions{Profile: ProfileKit, Services: services})
	if err != nil {
		t.Fatal(err)
	}
	for _, service := range services {
		if bytes.Count(artifact.Bytes(), service.Source) != 1 {
			t.Fatalf("artifact did not contain %s exactly once", service.Name)
		}
		if !strings.Contains(string(artifact.Bytes()), `services["`+service.Name+`"] = "1.0.0";`) {
			t.Fatalf("artifact metadata omitted %s", service.Name)
		}
	}
	staged, err := BuildStaged(StagedBuildOptions{Profile: ProfileHydrate, Services: services})
	if err != nil {
		t.Fatal(err)
	}
	if len(staged.Services) != 2 || staged.Services[0].Package() != "deepLinks" || staged.Services[1].Package() != "lifecycle" {
		t.Fatalf("staged service order=%#v", staged.Services)
	}
	for _, name := range []string{"deepLinks", "lifecycle"} {
		if appGrantsAuthoredService("1.8.0", name) {
			t.Fatalf("app@1.8.0 exposed %s to authored HTML", name)
		}
		for _, member := range []string{"snapshot", "subscribe"} {
			if validAuthoredServiceAction(name, member) {
				t.Fatalf("%s.%s escaped into authored actions", name, member)
			}
		}
	}
}

func TestLifecycleAndDeepLinksNodeContract(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping lifecycle/deep-link node contract in short mode")
	}
	script := `
"use strict";
const assert = (condition, message) => { if (!condition) throw new Error(message); };
function target() {
  const listeners = Object.create(null);
  return {
    adds: Object.create(null), removes: Object.create(null),
    addEventListener(type, listener) {
      this.adds[type] = (this.adds[type] || 0) + 1;
      (listeners[type] || (listeners[type] = new Set())).add(listener);
    },
    removeEventListener(type, listener) {
      this.removes[type] = (this.removes[type] || 0) + 1;
      if (listeners[type]) listeners[type].delete(listener);
    },
    emit(type) { if (listeners[type]) Array.from(listeners[type]).forEach(listener => listener({ type })); }
  };
}
const windowTarget = target();
globalThis.addEventListener = windowTarget.addEventListener.bind(windowTarget);
globalThis.removeEventListener = windowTarget.removeEventListener.bind(windowTarget);
globalThis.emitWindow = windowTarget.emit.bind(windowTarget);
const documentTarget = target();
let focused = true;
globalThis.document = {
  visibilityState: "visible",
  hasFocus() { return focused; },
  addEventListener: documentTarget.addEventListener.bind(documentTarget),
  removeEventListener: documentTarget.removeEventListener.bind(documentTarget),
  emit: documentTarget.emit.bind(documentTarget)
};
const reports = [];
globalThis.reportError = error => reports.push(error);
const services = Object.create(null);
globalThis.kit = { service(name, value) { services[name] = value; } };
let nativeValue = null;
let nativeFailure = null;
let nativeDeferred = null;
let nativeCalls = 0;
const nativeHost = {
  call(action, params) {
    nativeCalls++;
    assert(action === "deepLinks.snapshot", "unexpected native action " + action);
    assert(Object.keys(params).length === 0, "snapshot params were not empty");
    if (nativeFailure) return Promise.reject(nativeFailure);
    if (nativeDeferred) {
      const pending = nativeDeferred;
      nativeDeferred = null;
      return new Promise((resolve, reject) => { pending.resolve = resolve; pending.reject = reject; });
    }
    return Promise.resolve(nativeValue);
  }
};
document[Symbol.for("kitjs:assembly")] = { nativeHost };
` + string(lifecycleServicePackage(t).Source) + `
` + string(deepLinksServicePackage(t).Source) + `
(async () => {
  const lifecycle = services.lifecycle;
  const deepLinks = services.deepLinks;
  assert(Object.isFrozen(lifecycle.snapshot()), "lifecycle snapshot was mutable");
  assert(JSON.stringify(lifecycle.snapshot()) === '{"state":"active"}', "initial lifecycle state");
  const states = [];
  const stopLifecycle = lifecycle.subscribe(value => states.push(value.state));
  assert(states.join(",") === "active", "lifecycle did not deliver immediately");
  focused = false;
  emitWindow("blur");
  assert(states.join(",") === "active,inactive", "blur did not publish inactive");
  document.visibilityState = "hidden";
  document.emit("visibilitychange");
  assert(states.join(",") === "active,inactive,background", "hidden did not publish background");
  emitWindow("pagehide");
  emitWindow("focus");
  assert(states.join(",") === "active,inactive,background", "pagehide background was not sticky");
  document.visibilityState = "visible";
  focused = true;
  emitWindow("pageshow");
  assert(states.join(",") === "active,inactive,background,active", "pageshow did not restore active");
  let lifecycleArgsThrew = false;
  try { lifecycle.snapshot(1); } catch (_) { lifecycleArgsThrew = true; }
  assert(lifecycleArgsThrew, "snapshot accepted args");
  stopLifecycle(); stopLifecycle();
  assert(windowTarget.removes.focus === 1 && documentTarget.removes.visibilitychange === 1,
    "lifecycle listeners were not released exactly once");
  focused = false;
  const remountStates = [];
  const stopRemount = lifecycle.subscribe(value => remountStates.push(value.state));
  assert(remountStates.join(",") === "inactive", "detached state was not reconciled exactly once");
  emitWindow("pagehide");
  stopRemount();
  document.visibilityState = "visible";
  focused = true;
  const finalStates = [];
  const stopFinal = lifecycle.subscribe(value => finalStates.push(value.state));
  assert(finalStates.join(",") === "active", "pagehide latch survived teardown");
  stopFinal();

  const received = [];
  const stop = deepLinks.subscribe(value => received.push(value));
  await Promise.resolve(); await Promise.resolve();
  assert(received.length === 0 && nativeCalls === 1, "empty cold snapshot contract");
  nativeValue = { id: "0123456789abcdef0123456789abcdef", path: "/orders/42?tab=history#receipt" };
  emitWindow("kitwork:deep-link");
  await Promise.resolve(); await Promise.resolve();
  assert(received.length === 1 && Object.isFrozen(received[0]), "warm link was not delivered frozen");
  emitWindow("kitwork:deep-link");
  await Promise.resolve(); await Promise.resolve();
  assert(received.length === 1, "same native snapshot was delivered twice");
  const second = [];
  const stopSecond = deepLinks.subscribe(value => second.push(value.path));
  await Promise.resolve(); await Promise.resolve();
  assert(second.length === 1 && second[0] === "/orders/42?tab=history#receipt",
    "new subscriber did not receive current cold snapshot");
  nativeValue = { id: "abcdef0123456789abcdef0123456789", path: "/search?q=%E2%82%AC" };
  emitWindow("focus");
  await Promise.resolve(); await Promise.resolve();
  assert(received.length === 2 && second.length === 2, "new link was not delivered to each subscriber");
  const direct = await deepLinks.snapshot();
  assert(Object.isFrozen(direct) && direct.path === "/search?q=%E2%82%AC", "direct snapshot contract");

  nativeDeferred = {};
  const staleWake = nativeDeferred;
  const staleValue = { id: "55555555555555555555555555555555", path: "/stale" };
  nativeValue = staleValue;
  emitWindow("kitwork:deep-link");
  await Promise.resolve();
  nativeValue = { id: "66666666666666666666666666666666", path: "/newest" };
  emitWindow("kitwork:deep-link");
  staleWake.resolve(staleValue);
  await Promise.resolve(); await Promise.resolve(); await Promise.resolve(); await Promise.resolve();
  assert(received.length === 3 && received[2].path === "/newest" && second.length === 3 && second[2] === "/newest",
    "event during an in-flight snapshot leaked stale data");

  nativeDeferred = {};
  const abandoned = nativeDeferred;
  const abandonedValue = { id: "77777777777777777777777777777777", path: "/old-cohort" };
  nativeValue = abandonedValue;
  emitWindow("kitwork:deep-link");
  await Promise.resolve();
  stop(); stopSecond();
  nativeValue = { id: "88888888888888888888888888888888", path: "/remounted" };
  const remounted = [];
  const stopRemounted = deepLinks.subscribe(value => remounted.push(value.path));
  await Promise.resolve(); await Promise.resolve();
  abandoned.resolve(abandonedValue);
  await Promise.resolve(); await Promise.resolve();
  assert(remounted.join(",") === "/remounted", "abandoned request leaked into a new subscriber cohort");

  nativeValue = { id: "11111111111111111111111111111111", path: "/x%41" };
  emitWindow("kitwork:deep-link");
  await Promise.resolve(); await Promise.resolve(); await Promise.resolve(); await Promise.resolve();
  assert(remounted.length === 1 && reports.some(error => error && error.name === "KitDeepLinkError"),
    "hostile native path was not contained and reported");
  for (const path of ["/x%2541", "/x?q=%2541", "/x?q=%252f", "/x#x=%252f"]) {
    nativeValue = { id: "22222222222222222222222222222222", path };
    let rejected = false;
    try { await deepLinks.snapshot(); } catch (error) { rejected = error && error.name === "KitDeepLinkError"; }
    assert(rejected, "nested non-canonical escape was accepted: " + path);
  }
  const nested = (value, layers) => {
    for (let layer = 0; layer < layers; layer++) value = value.replaceAll("%", "%25");
    return value;
  };
  nativeValue = { id: "33333333333333333333333333333333", path: "/currency/" + nested("%E2%82%AC", 7) };
  assert((await deepLinks.snapshot()).id === nativeValue.id, "exact eight-pass route was rejected");
  nativeValue = { id: "44444444444444444444444444444444", path: "/currency/" + nested("%E2%82%AC", 8) };
  let budgetRejected = false;
  try { await deepLinks.snapshot(); } catch (_) { budgetRejected = true; }
  assert(budgetRejected, "nine-pass route crossed the decode budget");
  nativeFailure = { code: "DENIED", secret: "do-not-leak" };
  let failure = null;
  try { await deepLinks.snapshot(); } catch (error) { failure = error; }
  assert(failure && Object.isFrozen(failure) && failure.name === "KitDeepLinkError" && failure.code === "DENIED",
    "native rejection was not normalized");
  assert(!String(failure.message).includes("secret"), "native failure data leaked");
  stop(); stop(); stopSecond(); stopSecond(); stopRemounted(); stopRemounted();
  assert(windowTarget.removes["kitwork:deep-link"] === 2,
    "deep-link wake listener was not released once per subscriber cohort");
  let threw = false;
  try { deepLinks.subscribe(null); } catch (_) { threw = true; }
  assert(threw, "deepLinks.subscribe accepted a non-function");
})().catch(error => { console.error(error.stack || error); process.exitCode = 1; });
`
	runNativeHostNode(t, script)
}

func TestDeepLinksNodeBrowserFallbackIsInert(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping deep-link browser fallback node contract in short mode")
	}
	script := `
"use strict";
const assert = (condition, message) => { if (!condition) throw new Error(message); };
let adds = 0;
globalThis.addEventListener = () => { adds++; };
globalThis.removeEventListener = () => {};
globalThis.document = { addEventListener() { adds++; }, removeEventListener() {} };
let service = null;
globalThis.kit = { service(name, value) { if (name === "deepLinks") service = value; } };
` + string(deepLinksServicePackage(t).Source) + `
(async () => {
  assert(service && await service.snapshot() === null, "browser fallback snapshot was not null");
  let calls = 0;
  const stop = service.subscribe(() => { calls++; });
  await Promise.resolve();
  assert(calls === 0 && adds === 0, "browser fallback attached listeners or fabricated a link");
  stop(); stop();
})().catch(error => { console.error(error.stack || error); process.exitCode = 1; });
`
	runNativeHostNode(t, script)
}
