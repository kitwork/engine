package javascript

import (
	"bytes"
	"strings"
	"testing"
)

func TestStudioDatabase110ServiceStaticContract(t *testing.T) {
	source := readVanillaFile(t, "service", "studioDatabase", "1.1.0.js")
	if len(source) == 0 || source[0] != ';' || source[len(source)-1] != '\n' || bytes.Contains(source, []byte{'\r'}) {
		t.Fatal("studioDatabase@1.1.0 is not a sealable LF-only classic script")
	}
	for _, contract := range []string{
		`studioDatabase@1.1.0`,
		`"kitwork-studio-native-database-structure"`,
		`"kitwork-studio-native-database-query-count"`,
		`["schema", "type", "name", "catalogVersion"]`,
		`nativeCall("catalogList"`,
		`nativeCall("structure"`,
		`nativeCall("queryCount", { handle: state.handle, query: sql }`,
		`structure: structure`,
		`queryCount: queryCount`,
	} {
		if !strings.Contains(string(source), contract) {
			t.Errorf("studioDatabase@1.1.0 lost contract %q", contract)
		}
	}
}

func TestStudioDatabase110NativeNodeContract(t *testing.T) {
	serviceSource := readVanillaFile(t, "service", "studioDatabase", "1.1.0.js")
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
  Object.defineProperty(document, ASSEMBLY, { value: { graph: { services: { studioDatabase: "1.1.0" } }, nativeHost: host } });
  globalThis.document = document;
  var namespaces = Object.create(null);
  var kit = { service: function (name, namespace) { namespaces[name] = Object.freeze(namespace); } };
` + string(serviceSource) + `
  return namespaces.studioDatabase;
}

(async function () {
  var calls = [];
  var handle = "H".repeat(32);
  var columns = [
    { key: "id", position: 1, name: "id", type: "bigint", nullable: "No", keyLabel: "PRIMARY KEY", defaultValue: "", primaryOrder: 1, generated: false },
    { key: "email", position: 2, name: "email", type: "text", nullable: "Yes", keyLabel: "", defaultValue: "", primaryOrder: 0, generated: false }
  ];
  var structurePayload = {
    format: "kitwork-studio-native-database-structure", version: 1, ok: true, source: "postgresql",
    database: "app", schema: "public", readOnly: true, catalogVersion: 7, name: "users", type: "table",
    columns: columns,
    indexes: [{ name: "users_pkey", columns: "id", kind: "Primary key", method: "btree", unique: true, partial: false }],
    foreignKeys: [], durationMs: 3, cached: false
  };
  var catalogPayload = {
    format: "kitwork-studio-native-database-catalog", version: 1, ok: true, source: "postgresql",
    database: "app", schema: "public", readOnly: true, catalogVersion: 7,
    databases: ["app"], schemas: ["public"], objects: [{
      id: "table-cHVibGlj-dXNlcnM", name: "users", type: "table", schema: "public",
      columns: [], indexes: [], foreignKeys: []
    }]
  };
  var countPayload = {
    format: "kitwork-studio-native-database-query-count", version: 1, ok: true, source: "postgresql",
    database: "app", schema: "public", readOnly: true, totalRows: 1041, durationMs: 4
  };
  var structureOverride = null;
  var countOverride = null;
  var host = { call: function (action, params) {
    calls.push({ action: action, params: params, frozen: Object.isFrozen(params) });
    if (action === "studioDatabase.open") {
      return { handle: handle, driver: "postgresql", database: "app", schema: "public", readOnly: true };
    }
	if (action === "studioDatabase.catalogList") return catalogPayload;
    if (action === "studioDatabase.structure") return structureOverride || structurePayload;
    if (action === "studioDatabase.queryCount") return countOverride || countPayload;
    if (action === "studioDatabase.close") return true;
    throw new Error("unexpected action " + action);
  }};
  var service = install(host);
  assert(Object.isFrozen(service) && Object.keys(service).sort().join(",") === "catalog,close,open,query,queryCount,structure,tablePage", "public surface drifted");
  var reference = await service.open({ driver: "postgresql", host: "127.0.0.1", port: 5432, database: "app", user: "studio", password: "secret", tls: false });
	var catalog = await service.catalog(reference);
	assert(Object.isFrozen(catalog.objects[0].columns) && catalog.objects[0].columns.length === 0, "shallow catalog object was rejected");
	var catalogCall = calls.find(function (entry) { return entry.action === "studioDatabase.catalogList"; });
	assert(catalogCall && Object.keys(catalogCall.params).join(",") === "handle", "shallow catalog used the legacy action or widened its payload");
  var structure = await service.structure(reference, { schema: "public", type: "table", name: "users", catalogVersion: 7 });
  assert(Object.isFrozen(structure) && Object.isFrozen(structure.columns) && structure.schema === "public" && structure.cached === false, "structure snapshot is mutable or incomplete");
  var count = await service.queryCount(reference, "SELECT * FROM public.users");
  assert(Object.isFrozen(count) && count.totalRows === 1041 && count.durationMs === 4, "count result changed");
  var structureCall = calls.find(function (entry) { return entry.action === "studioDatabase.structure"; });
  var countCall = calls.find(function (entry) { return entry.action === "studioDatabase.queryCount"; });
  assert(structureCall.frozen && Object.keys(structureCall.params).sort().join(",") === "catalogVersion,handle,name,schema,type" && structureCall.params.catalogVersion === 7, "structure bridge payload widened");
  assert(countCall.frozen && Object.keys(countCall.params).sort().join(",") === "handle,query" && countCall.params.query === "SELECT * FROM public.users", "queryCount bridge payload widened");

  var before = calls.length;
  var callerFailures = [];
  try { service.structure(reference, { schema: "public", type: "table", name: "users", catalogVersion: 7, extra: true }); } catch (error) { callerFailures.push(error instanceof TypeError); }
  try { service.structure(reference, { schema: "public", type: "sequence", name: "users", catalogVersion: 7 }); } catch (error) { callerFailures.push(error instanceof TypeError); }
  try { service.queryCount(reference, "   "); } catch (error) { callerFailures.push(error instanceof TypeError); }
  assert(callerFailures.length === 3 && callerFailures.every(Boolean) && calls.length === before, "invalid caller input reached native host");

  structureOverride = Object.assign({}, structurePayload, { cached: "false" });
  var failure = await rejected(service.structure(reference, { schema: "public", type: "table", name: "users", catalogVersion: 7 }));
  assert(failure.name === "KitStudioDatabaseError" && failure.code === "FAILED" && failure.operation === "structure", "malformed structure escaped validation");
  structureOverride = Object.assign({}, structurePayload, { extra: true });
  failure = await rejected(service.structure(reference, { schema: "public", type: "table", name: "users", catalogVersion: 7 }));
  assert(failure.code === "FAILED", "extra structure field escaped validation");
  structureOverride = null;

  countOverride = Object.assign({}, countPayload, { totalRows: Number.MAX_SAFE_INTEGER + 1 });
  failure = await rejected(service.queryCount(reference, "SELECT * FROM public.users"));
  assert(failure.name === "KitStudioDatabaseError" && failure.code === "FAILED" && failure.operation === "queryCount", "unsafe count escaped validation");
  countOverride = Object.assign({}, countPayload, { schema: "other" });
  failure = await rejected(service.queryCount(reference, "SELECT * FROM public.users"));
  assert(failure.code === "FAILED", "wrong count envelope escaped validation");
  assert(await service.close(reference) === true, "connection did not close");
})().catch(function (error) {
  console.error(error && error.stack || error);
  process.exitCode = 1;
});
`
	runNativeHostNode(t, script)
}
