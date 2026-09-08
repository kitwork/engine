package javascript

import (
	"bytes"
	"strings"
	"testing"
)

func TestStudioStateServiceStaticContract(t *testing.T) {
	source := readVanillaFile(t, "service", "studioState", "1.0.0.js")
	if len(source) == 0 || source[0] != ';' || source[len(source)-1] != '\n' || bytes.Contains(source, []byte{'\r'}) {
		t.Fatal("studioState@1.0.0 is not a sealable LF-only classic script")
	}
	for _, marker := range []string{
		`kit.service("studioState"`,
		`nativeHost.call("studioState." + action`,
		`name: { value: "KitStudioStateError" }`,
		`credentialAvailable: d.credentialAvailable.value`,
		`rememberPassword: checked.rememberPassword`,
		`connections: mapRecords`,
		`savedQueries: mapRecords`,
		`history: mapRecords`,
	} {
		if !strings.Contains(string(source), marker) {
			t.Errorf("studioState@1.0.0 lost contract %q", marker)
		}
	}
	for _, forbidden := range []string{`globalThis.kit`, `window.kit`, `kit.component(`, `"password"`, `"secret"`} {
		if bytes.Contains(bytes.ToLower(source), bytes.ToLower([]byte(forbidden))) {
			t.Errorf("studioState@1.0.0 exposes forbidden coupling %q", forbidden)
		}
	}
}

func TestStudioStateNativeNodeContract(t *testing.T) {
	serviceSource := readVanillaFile(t, "service", "studioState", "1.0.0.js")
	script := `
"use strict";
var ASSEMBLY = Symbol.for("kitjs:assembly");
function assert(value, message) { if (!value) throw new Error(message); }
async function rejected(value) {
  try { await value; } catch (error) { return error; }
  throw new Error("promise unexpectedly resolved");
}
function install(host) {
  var document = {};
  Object.defineProperty(document, ASSEMBLY, { value: { nativeHost: host }, configurable: true });
  globalThis.document = document;
  var services = Object.create(null);
  var kit = { service: function (name, namespace) { services[name] = Object.freeze(namespace); } };
` + string(serviceSource) + `
  return services.studioState;
}

(async function () {
  var calls = [];
  var connection = {
    id: "local-postgres", name: "Local PostgreSQL", driver: "PostgreSQL", host: "127.0.0.1",
    port: "5432", defaultDatabase: "app", path: "", user: "postgres", tls: false,
    credentialAvailable: true, createdAt: 10, updatedAt: 20,
    lastOpenedAt: 30, favorite: false, sortOrder: 1
  };
  var saved = { id: "saved-1", connectionId: "local-postgres", database: "app", schema: "public", name: "Users", sql: "SELECT * FROM users", createdAt: 10, updatedAt: 20 };
  var history = { id: "history-1", connectionId: "local-postgres", database: "app", schema: "public", sql: "SELECT 1", durationMs: 2, rowCount: 1, status: "success", errorCode: "", executedAt: 30 };
  var host = { call: function (action, params) {
    calls.push({ action: action, params: params });
    if (action === "studioState.loadSnapshot") return { connections: [connection], savedQueries: [saved], history: [history], preferences: { sqlTheme: "kitwork" } };
    if (action === "studioState.upsertConnection") {
      assert(params.connection.port === 5432 && params.connection.defaultDatabase === "app" && params.rememberPassword === true && !Object.prototype.hasOwnProperty.call(params.connection, "rememberPassword"), "connection payload leaked or changed");
      return connection;
    }
    if (action === "studioState.upsertSavedQuery") return params.query;
    if (action === "studioState.appendHistory") return params.history;
    if (action === "studioState.updatePreference") return { key: params.key, value: params.value, updatedAt: 40 };
    if (action === "studioState.deleteConnection" || action === "studioState.deleteSavedQuery" || action === "studioState.clearHistory") return true;
    throw new Error("unexpected action " + action);
  }};
  var service = install(host);
  assert(Object.isFrozen(service) && Object.keys(service).sort().join(",") === "appendHistory,clearHistory,deleteConnection,deleteSavedQuery,loadSnapshot,updatePreference,upsertConnection,upsertSavedQuery", "public surface widened");
  var snapshot = await service.loadSnapshot();
  assert(Object.isFrozen(snapshot) && Object.isFrozen(snapshot.connections) && snapshot.connections[0].defaultDatabase === "app", "snapshot changed");
  assert(snapshot.connections[0].rememberPassword === true && !Object.prototype.hasOwnProperty.call(snapshot.connections[0], "credentialRef"), "credential reference crossed the service");
  assert(snapshot.preferences.sqlTheme === "kitwork", "preference map changed");

  var stored = await service.upsertConnection({ id: "local-postgres", name: "Local PostgreSQL", driver: "PostgreSQL", host: "127.0.0.1", port: "5432", defaultDatabase: "app", path: "", user: "postgres", tls: false, rememberPassword: true });
  assert(stored.rememberPassword && stored.port === "5432", "connection normalization changed");
  assert((await service.upsertSavedQuery(saved)).id === "saved-1", "saved query changed");
  assert((await service.appendHistory(history)).rowCount === 1, "history changed");
  assert((await service.updatePreference("sqlTheme", "midnight")).value === "midnight", "preference changed");
  assert(await service.deleteConnection("local-postgres") && await service.deleteSavedQuery("saved-1") && await service.clearHistory(), "delete contract changed");

  var before = calls.length;
  try { service.upsertConnection({ id: "bad", name: "Bad", driver: "PostgreSQL", host: "localhost", port: "5432", defaultDatabase: "app", path: "", user: "u", tls: false, rememberPassword: false, extra: true }); }
  catch (error) { assert(error instanceof TypeError, "invalid connection error changed"); }
  assert(calls.length === before, "invalid connection reached native host");

  var noHost = install(null);
  var failure = await rejected(noHost.loadSnapshot());
  assert(failure.name === "KitStudioStateError" && failure.code === "UNAVAILABLE", "web fallback did not fail closed");
})().catch(function (error) {
  console.error(error && error.stack || error);
  process.exitCode = 1;
});
`
	runNativeHostNode(t, script)
}
