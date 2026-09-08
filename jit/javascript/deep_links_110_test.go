package javascript

import (
	"bytes"
	"testing"
)

const deepLinksService100SHA256 = "9dd6aa83cbf6d889228de950f281f15b9fce9d04b2d65e935af209bfddb1c5a1"
const deepLinksService110SHA256 = "a3f1709350b3bf1de45148a62b91dd0e6cf60c6fe6e671068779609a0d7335b5"

func deepLinksService110Package(t *testing.T) Service {
	t.Helper()
	return Service{
		Name:    "deepLinks",
		Version: "1.1.0",
		Source:  readVanillaFile(t, "service", "deepLinks", "1.1.0.js"),
	}
}

func TestDeepLinks110TightensCanonicalPathsWithoutChanging100(t *testing.T) {
	legacy := deepLinksServicePackage(t).Source
	if got := ContentHash(legacy); got != deepLinksService100SHA256 {
		t.Fatalf("deepLinks@1.0.0 bytes changed: %s", got)
	}
	current := deepLinksService110Package(t)
	if got := ContentHash(current.Source); got != deepLinksService110SHA256 {
		t.Fatalf("deepLinks@1.1.0 bytes changed: %s", got)
	}
	if bytes.Equal(legacy, current.Source) ||
		!bytes.Contains(current.Source, []byte(`deepLinks@1.1.0`)) ||
		!bytes.Contains(current.Source, []byte(`if (unit === 35 && ++fragments > 1)`)) {
		t.Fatal("deepLinks@1.1.0 did not add its exact canonical-path fence")
	}

	catalog, err := loadDeliveryCatalog()
	if err != nil {
		t.Fatal(err)
	}
	legacyCatalog, err := catalog.service(ServiceVersion{Name: "deepLinks", Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	currentCatalog, err := catalog.service(ServiceVersion{Name: "deepLinks", Version: "1.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(legacyCatalog.source, legacy) || !bytes.Equal(currentCatalog.source, current.Source) {
		t.Fatal("deep-link catalog versions do not preserve their exact source packages")
	}
}

func TestDeepLinks110RejectsReservedAndAmbiguousRoutes(t *testing.T) {
	source := deepLinksService110Package(t).Source
	script := `
"use strict";
var ASSEMBLY = Symbol.for("kitjs:assembly");
function assert(value, message) { if (!value) throw new Error(message); }
async function rejected(promise) {
  try { await promise; } catch (error) { return error; }
  throw new Error("promise unexpectedly resolved");
}
var nativeValue = null;
var document = {
  addEventListener: function () {},
  removeEventListener: function () {}
};
Object.defineProperty(document, ASSEMBLY, {
  configurable: true,
  value: {
    nativeHost: {
      call: function (action, params) {
        assert(action === "deepLinks.snapshot", "unexpected native action");
        assert(Object.keys(params).length === 0, "snapshot params changed");
        return Promise.resolve(nativeValue);
      }
    }
  }
});
globalThis.document = document;
var namespace = null;
var kit = {
  service: function (name, value) {
    assert(name === "deepLinks" && namespace === null, "unexpected service registration");
    namespace = Object.freeze(value);
  }
};
` + string(source) + `
(async function () {
  var id = "0123456789abcdef0123456789abcdef";
  var valid = [
    "/", "/orders/42?tab=history#receipt", "/search?q=%E2%82%AC",
    "/query?next=%252Forders"
  ];
  for (var index = 0; index < valid.length; index++) {
    nativeValue = { id: id, path: valid[index] };
    var snapshot = await namespace.snapshot();
    assert(Object.isFrozen(snapshot) && snapshot.path === valid[index],
      "valid route was rejected: " + valid[index]);
  }

  var invalid = [
    "", "relative", "//authority", "/space here", "/unicode/€", "/bad\\path",
    "/empty?", "/empty#", "/bad?#fragment", "/two#fragments#here",
    "/quote\"route", "/angle<route", "/angle>route", "/bracket[route",
    "/bracket]route", "/caret^route", "/tick` + "`" + `route", "/brace{route",
    "/pipe|route", "/brace}route", "/lower/%e2%82%ac", "/encoded/%41",
    "/dot/../route", "/dot/%252E%252E/route", "/slash%2Froute",
    "/slash%252Froute", "/invalid/%FF", "/" + "x".repeat(2048)
  ];
  for (var invalidIndex = 0; invalidIndex < invalid.length; invalidIndex++) {
    nativeValue = { id: id, path: invalid[invalidIndex] };
    var failure = await rejected(namespace.snapshot());
    assert(failure && failure.name === "KitDeepLinkError" && failure.code === "FAILED" &&
      Object.isFrozen(failure), "invalid route escaped: " + invalid[invalidIndex]);
  }
})().catch(function (error) {
  console.error(error && error.stack || error);
  process.exitCode = 1;
});
`
	runNativeHostNode(t, script)
}
