package javascript

import (
	"bytes"
	"reflect"
	"testing"
)

const capabilityLab100SHA256 = "5d210fea8fea133d65a5cbf07d0f1f61515f7a5312ffa06fe1abdc90b7467578"

func TestCapabilityLab110PinsRoutedNotificationsWithoutChangingDefault(t *testing.T) {
	legacySource := readVanillaFile(t, "component", "capability-lab", "1.0.0.js")
	if got := ContentHash(legacySource); got != capabilityLab100SHA256 {
		t.Fatalf("capability-lab@1.0.0 bytes changed: %s", got)
	}
	if bytes.Contains(legacySource, []byte(`notifications.tap`)) || bytes.Contains(legacySource, []byte(`payload.path`)) {
		t.Fatal("capability-lab@1.0.0 gained notification-tap behavior")
	}
	currentSource := readVanillaFile(t, "component", "capability-lab", "1.1.0.js")
	if len(currentSource) == 0 || currentSource[0] != ';' ||
		currentSource[len(currentSource)-1] != '\n' || bytes.Contains(currentSource, []byte{'\r'}) {
		t.Fatal("capability-lab@1.1.0 is not a sealable LF-only classic script")
	}
	for _, marker := range [][]byte{
		[]byte(`capability-lab@1.1.0`),
		[]byte(`capability: "notifications.tap"`),
		[]byte(`supports.call(kit.capabilities, "notifications.tap")`),
		[]byte(`var NOTIFICATION_TAP_PATH = "/capability-lab?source=notification#tap"`),
		[]byte(`payload.path = NOTIFICATION_TAP_PATH`),
		[]byte(`scope.record("notification-tap", "running", "granted", "awaiting-tap")`),
		[]byte(`snapshot.path === NOTIFICATION_TAP_PATH`),
		[]byte(`value.path === NOTIFICATION_TAP_PATH`),
	} {
		if !bytes.Contains(currentSource, marker) {
			t.Fatalf("capability-lab@1.1.0 lost %q", marker)
		}
	}
	for _, forbidden := range [][]byte{
		[]byte(`addEventListener(`), []byte(`location.assign`), []byte(`location.href`),
		[]byte(`kit.service(`), []byte(`kitwork:deep-link`),
		[]byte(`scope.record("notification-tap", "pass", "granted", "tap-route-attached")`),
	} {
		if bytes.Contains(currentSource, forbidden) {
			t.Fatalf("capability-lab@1.1.0 owns forbidden service/tap behavior %q", forbidden)
		}
	}

	catalog, err := loadDeliveryCatalog()
	if err != nil {
		t.Fatal(err)
	}
	defaultComponent, err := catalog.component("capability-lab", "")
	if err != nil {
		t.Fatal(err)
	}
	if defaultComponent.identity.Version != "1.0.0" || !bytes.Equal(defaultComponent.source, legacySource) {
		t.Fatal("capability-lab default or immutable 1.0.0 source changed")
	}
	legacy, err := catalog.component("capability-lab", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	current, err := catalog.component("capability-lab", "1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	legacyVersions := serviceVersionMap(legacy.requires)
	currentVersions := serviceVersionMap(current.requires)
	if legacyVersions["notifications"] != "1.0.0" || currentVersions["notifications"] != "1.1.0" {
		t.Fatalf("capability-lab notification pins: 1.0=%q 1.1=%q",
			legacyVersions["notifications"], currentVersions["notifications"])
	}
	delete(legacyVersions, "notifications")
	delete(currentVersions, "notifications")
	if !reflect.DeepEqual(currentVersions, legacyVersions) {
		t.Fatalf("capability-lab@1.1.0 changed unrelated service pins: 1.0=%#v 1.1=%#v",
			legacyVersions, currentVersions)
	}

	composer := &Composer{catalog: catalog}
	legacyArtifact, err := composer.ComposeHTML([]byte(`<main data-kit-component="capability-lab@1.0.0"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	currentArtifact, err := composer.ComposeHTML([]byte(`<main data-kit-component="capability-lab@1.1.0"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(currentArtifact.JavaScript, []byte(`services["notifications"] = "1.1.0"`)) ||
		bytes.Contains(currentArtifact.JavaScript, []byte(`services["notifications"] = "1.0.0"`)) {
		t.Fatal("capability-lab@1.1.0 did not close over notifications@1.1.0 exactly")
	}
	if currentArtifact.ContentHash == legacyArtifact.ContentHash ||
		bytes.Equal(currentArtifact.JavaScript, legacyArtifact.JavaScript) {
		t.Fatal("capability-lab versions shared an artifact identity")
	}
}

func TestCapabilityLab110PassesTapOnlyAfterMatchingDeepLink(t *testing.T) {
	source := readVanillaFile(t, "component", "capability-lab", "1.1.0.js")
	script := `
"use strict";
function assert(value, message) { if (!value) throw new Error(message); }
var definition = null;
var deepLinkListener = null;
var snapshotValue = null;
var shownPayload = null;
var kit = {
  component: function (name, value) {
    assert(name === "capability-lab" && definition === null, "unexpected component registration");
    definition = value;
  },
  capabilities: {
    supports: function (name) { return Promise.resolve(name === "notifications.tap"); }
  },
  deepLinks: {
    snapshot: function () { return Promise.resolve(snapshotValue); },
    subscribe: function (listener) {
      deepLinkListener = listener;
      return function () { deepLinkListener = null; };
    }
  },
  notifications: {
    permission: function () { return Promise.resolve("granted"); },
    show: function (payload) { shownPayload = payload; return Promise.resolve(true); }
  }
};
globalThis.kit = kit;
globalThis.document = { visibilityState: "visible" };
` + string(source) + `

function row(instance, id) {
  return instance.items.filter(function (item) { return item.id === id; })[0];
}

(async function () {
  var instance = {};
  Object.keys(definition).forEach(function (key) { instance[key] = definition[key]; });
  var cleanup = null;
  await instance.init({
    listen: function () {},
    cleanup: function (value) { cleanup = value; }
  });
  assert(typeof cleanup === "function" && typeof deepLinkListener === "function",
    "deep-link subscription was not lifecycle owned");

  assert(await instance.showNotification() === true, "routed notification was not shown");
  assert(shownPayload && shownPayload.path === "/capability-lab?source=notification#tap",
    "notification did not carry the exact probe route");
  assert(row(instance, "notification-tap").result === "running" &&
    row(instance, "notification-tap").detail === "awaiting-tap",
    "show() incorrectly proved the tap");

  deepLinkListener(Object.freeze({ id: "unrelated", path: "/orders/42" }));
  assert(row(instance, "notification-tap").result === "running",
    "an unrelated subscribed link proved the notification tap");
  deepLinkListener(Object.freeze({
    id: "matching",
    path: "/capability-lab?source=notification#tap"
  }));
  assert(row(instance, "notification-tap").result === "pass" &&
    row(instance, "notification-tap").detail === "received",
    "the matching subscribed route did not prove the notification tap");

  assert(await instance.showNotification() === true, "second routed notification was not shown");
  snapshotValue = Object.freeze({ id: "manual-unrelated", path: "/settings" });
  assert(await instance.checkDeepLink() === true, "unrelated manual snapshot was not readable");
  assert(row(instance, "notification-tap").result === "running",
    "an unrelated manual snapshot proved the notification tap");
  snapshotValue = Object.freeze({
    id: "manual-matching",
    path: "/capability-lab?source=notification#tap"
  });
  assert(await instance.checkDeepLink() === true, "matching manual snapshot was not readable");
  assert(row(instance, "notification-tap").result === "pass",
    "the matching manual snapshot did not prove the notification tap");

  cleanup();
  assert(deepLinkListener === null, "cleanup retained the deep-link subscription");
})().catch(function (error) {
  console.error(error && error.stack || error);
  process.exitCode = 1;
});
`
	runNativeHostNode(t, script)
}
