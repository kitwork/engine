package javascript

import (
	"bytes"
	"strings"
	"testing"
)

func TestStudioDatabaseServiceStaticContract(t *testing.T) {
	source := readVanillaFile(t, "service", "studioDatabase", "1.0.0.js")
	if len(source) == 0 || source[0] != ';' || source[len(source)-1] != '\n' || bytes.Contains(source, []byte{'\r'}) {
		t.Fatal("studioDatabase@1.0.0 is not a sealable LF-only classic script")
	}
	for _, contract := range []string{
		`kit.service("studioDatabase"`,
		`nativeHost.call("studioDatabase." + action`,
		`var references = new WeakMap()`,
		`name: { value: "KitStudioDatabaseError" }`,
		`["driver", "host", "port", "database", "user", "password", "tls"]`,
		`["driver", "host", "port", "database", "user", "password", "tls", "profileId", "rememberPassword"]`,
		`credentialAvailable`,
		`["schema", "type", "name", "catalogVersion", "page", "pageSize"]`,
		`["catalogVersion", "databases", "schemas", "objects"]`,
		`"kitwork-studio-native-database-catalog"`,
		`"kitwork-studio-native-database-table"`,
		`"kitwork-studio-native-database-query"`,
	} {
		if !strings.Contains(string(source), contract) {
			t.Errorf("studioDatabase@1.0.0 lost contract %q", contract)
		}
	}
	for _, forbidden := range []string{
		`globalThis.kit`, `window.kit`, `kit.component(`, `beginOpen`, `pollOpen`, `releaseOpen`, `withoutRowid`, `"strict"`, `method !== "btree"`,
	} {
		if bytes.Contains(bytes.ToLower(source), bytes.ToLower([]byte(forbidden))) {
			t.Errorf("studioDatabase@1.0.0 exposes forbidden coupling %q", forbidden)
		}
	}
}

func TestStudioDatabaseNativeNodeContract(t *testing.T) {
	serviceSource := readVanillaFile(t, "service", "studioDatabase", "1.0.0.js")
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
  var core = { graph: { services: { studioDatabase: "1.0.0" } }, nativeHost: host };
  Object.defineProperty(document, ASSEMBLY, { value: core, configurable: true });
  globalThis.document = document;
  var namespaces = Object.create(null);
  var kit = {
    service: function (name, namespace) {
      assert(!Object.prototype.hasOwnProperty.call(namespaces, name), "duplicate service " + name);
      namespaces[name] = Object.freeze(namespace);
      Object.defineProperty(kit, name, { value: namespaces[name], enumerable: true });
    }
  };
` + string(serviceSource) + `
  return { services: namespaces };
}

(async function () {
  var calls = [];
  var handle = "H".repeat(32);
  var mode = "success";
  var handleGetterReads = 0;
  var columns = [
    { key: "id", position: 1, name: "id", type: "bigint", nullable: "No", keyLabel: "PRIMARY KEY", defaultValue: "", primaryOrder: 1, generated: false },
    { key: "email", position: 2, name: "email", type: "text", nullable: "Yes", keyLabel: "", defaultValue: "", primaryOrder: 0, generated: false }
  ];
  var object = {
    id: "table-cHVibGlj-dXNlcnM", name: "users", type: "table", schema: "public",
    columns: columns,
    indexes: [{ name: "users_email_gin", columns: "email", kind: "Index", method: "gin", unique: false, partial: false }],
    foreignKeys: []
  };
  var catalogPayload = {
    format: "kitwork-studio-native-database-catalog", version: 1, ok: true, source: "postgresql",
    database: "app", schema: "public", readOnly: true, catalogVersion: 4,
    databases: ["analytics", "app", "postgres"], schemas: ["public", "audit"], objects: [object]
  };
  var catalogOverride = null;
  var tablePayload = {
    format: "kitwork-studio-native-database-table", version: 1, ok: true, source: "postgresql",
    database: "app", schema: "public", readOnly: true, catalogVersion: 4,
    name: "users", type: "table", columns: columns, rows: [[1, "a@example.com"]], totalRows: 1,
    page: 1, pageSize: 25, pageCount: 1, pageStart: 1, pageEnd: 1,
    hasPreviousPage: false, hasNextPage: false, durationMs: 3
  };
  var queryPayload = {
    format: "kitwork-studio-native-database-query", version: 1, ok: true, source: "postgresql",
    database: "app", schema: "public", readOnly: true,
    columns: [{ name: "count", type: "bigint" }], rows: [["1"]], rowCount: 1,
    returnedRows: 1, truncated: false, durationMs: 2
  };
  var host = { call: function (action, params) {
    calls.push({ action: action, params: params, frozen: Object.isFrozen(params) });
    if (mode === "auth") return Promise.reject({ code: "AUTHENTICATION_FAILED", message: "postgres://admin:secret@private/db" });
    if (action === "studioDatabase.open") {
      var profileOpen = Object.prototype.hasOwnProperty.call(params, "profileId");
      assert(Object.keys(params).sort().join(",") === (profileOpen ? "database,driver,host,password,port,profileId,rememberPassword,tls,user" : "database,driver,host,password,port,tls,user"), "open payload widened");
      assert(params.driver === "postgresql" && params.port === 5432, "open connection changed");
      if (profileOpen) assert(params.profileId === "profile-main" && params.password === "" && params.rememberPassword === true, "profile credential options changed");
      else assert(params.password === "temporary-secret", "open credentials changed");
      var openValue = { handle: handle, driver: "postgresql", database: mode === "malformedOpen" ? "wrong-database" : "app", schema: "public", readOnly: true };
      if (profileOpen) { openValue.credentialAvailable = true; openValue.remembered = true; }
      if (mode === "malformedOpenExtra") openValue.extra = "must-not-cross";
      if (mode === "malformedOpenGetter") {
        delete openValue.handle;
        Object.defineProperty(openValue, "handle", { enumerable: true, get: function () { handleGetterReads++; return handle; } });
      }
      return openValue;
    }
    if (action === "studioDatabase.catalog") return catalogOverride || catalogPayload;
    if (action === "studioDatabase.tablePage") {
      assert(params.schema === "public" && params.handle === handle, "table schema or handle missing");
      return tablePayload;
    }
    if (action === "studioDatabase.query") return queryPayload;
    if (action === "studioDatabase.close") return true;
    throw new Error("unexpected action " + action);
  }};

  var installed = install(host);
  var service = installed.services.studioDatabase;
  assert(Object.isFrozen(service) && Object.keys(service).sort().join(",") === "catalog,close,open,query,tablePage", "public surface widened");
  var reference = await service.open({ driver: "postgresql", host: "127.0.0.1", port: 5432, database: "app", user: "admin", password: "temporary-secret", tls: false });
  assert(Object.isFrozen(reference) && Object.keys(reference).sort().join(",") === "database,driver,readOnly,schema", "reference leaked private state");
  assert(reference.driver === "postgresql" && reference.database === "app" && reference.schema === "public", "reference metadata changed");

  var profileReference = await service.open({ driver: "postgresql", host: "127.0.0.1", port: 5432, database: "app", user: "admin", password: "", tls: false, profileId: "profile-main", rememberPassword: true });
  assert(Object.isFrozen(profileReference) && Object.keys(profileReference).sort().join(",") === "credentialAvailable,database,driver,readOnly,remembered,schema" && profileReference.credentialAvailable === true && profileReference.remembered === true, "profile reference did not expose safe credential state");
  assert(await service.close(profileReference) === true, "profile reference did not close");

  var catalog = await service.catalog(reference);
  assert(Object.isFrozen(catalog) && Object.isFrozen(catalog.databases) && Object.isFrozen(catalog.schemas) && Object.isFrozen(catalog.objects) && catalog.databases[1] === "app" && catalog.schemas[1] === "audit", "catalog is not immutable/database-aware/schema-aware");
  assert(catalog.objects[0].schema === "public" && catalog.objects[0].indexes[0].method === "gin", "real catalog metadata was rejected");
  var page = await service.tablePage(reference, { schema: "public", type: "table", name: "users", catalogVersion: 4, page: 1, pageSize: 25 });
  assert(Object.isFrozen(page) && page.schema === "public" && page.rows[0][1] === "a@example.com", "table page context changed");
  var query = await service.query(reference, "SELECT count(*) AS count FROM users");
  assert(Object.isFrozen(query) && query.rows[0][0] === "1", "query result changed");

  var malformedCatalogs = [];
  var zeroPosition = JSON.parse(JSON.stringify(catalogPayload));
  zeroPosition.objects[0].columns[0].position = 0;
  malformedCatalogs.push(zeroPosition);
  var lowercasePrimaryKey = JSON.parse(JSON.stringify(catalogPayload));
  lowercasePrimaryKey.objects[0].columns[0].keyLabel = "primary key";
  malformedCatalogs.push(lowercasePrimaryKey);
  var nullDefault = JSON.parse(JSON.stringify(catalogPayload));
  nullDefault.objects[0].columns[0].defaultValue = null;
  malformedCatalogs.push(nullDefault);
  var duplicateDatabase = JSON.parse(JSON.stringify(catalogPayload));
  duplicateDatabase.databases[0] = "app";
  malformedCatalogs.push(duplicateDatabase);
  var missingConnectedDatabase = JSON.parse(JSON.stringify(catalogPayload));
  missingConnectedDatabase.databases = ["analytics", "postgres"];
  malformedCatalogs.push(missingConnectedDatabase);
  for (var malformedIndex = 0; malformedIndex < malformedCatalogs.length; malformedIndex++) {
    catalogOverride = malformedCatalogs[malformedIndex];
    var malformedFailure = await rejected(service.catalog(reference));
    assert(malformedFailure.name === "KitStudioDatabaseError" && malformedFailure.code === "FAILED", "misaligned native catalog metadata was accepted");
  }
  catalogOverride = null;
  assert(await service.close(reference) === true && await service.close(reference) === true, "close is not idempotent");

  var before = calls.length;
  var failures = [];
  try { service.open({ driver: "postgresql", host: "localhost", port: 5432, database: "app", user: "u", password: "p", tls: false, extra: true }); } catch (error) { failures.push(error instanceof TypeError); }
  try { service.open({ driver: "postgresql", host: "localhost", port: 5432, database: "app", user: "", password: "p", tls: false }); } catch (error) { failures.push(error instanceof TypeError); }
  try { service.open({ driver: "postgresql", host: "localhost", port: 5432, database: "", user: "u", password: "p", tls: false }); } catch (error) { failures.push(error instanceof TypeError); }
  try { service.open({ driver: "postgresql", host: "localhost", port: 5432, database: "d".repeat(129), user: "u", password: "p", tls: false }); } catch (error) { failures.push(error instanceof TypeError); }
  try { service.open({ driver: "postgresql", host: "localhost", port: 5432, database: "app", user: "u".repeat(129), password: "p", tls: false }); } catch (error) { failures.push(error instanceof TypeError); }
  try { service.open({ driver: "postgresql", host: "localhost", port: 5432, database: "app", user: "é".repeat(65), password: "p", tls: false }); } catch (error) { failures.push(error instanceof TypeError); }
  try { service.tablePage({}, { schema: "public", type: "table", name: "users", catalogVersion: 4, page: 1, pageSize: 25 }); } catch (error) { failures.push(error instanceof TypeError); }
  assert(failures.length === 7 && failures.every(Boolean) && calls.length === before, "invalid caller data reached native host");

  mode = "malformedOpen";
  var closeCallsBeforeMalformed = calls.filter(function (entry) { return entry.action === "studioDatabase.close"; }).length;
  var malformedOpenFailure = await rejected(service.open({ driver: "postgresql", host: "private", port: 5432, database: "app", user: "admin", password: "temporary-secret", tls: false }));
  await Promise.resolve();
  var malformedCloseCalls = calls.filter(function (entry) { return entry.action === "studioDatabase.close" && entry.params.handle === handle; }).length;
  assert(malformedOpenFailure.code === "FAILED" && malformedCloseCalls === closeCallsBeforeMalformed + 1,
    "malformed open metadata stranded its exact syntactically valid raw handle");

  mode = "malformedOpenExtra";
  var closeCallsBeforeExtra = calls.filter(function (entry) { return entry.action === "studioDatabase.close" && entry.params.handle === handle; }).length;
  malformedOpenFailure = await rejected(service.open({ driver: "postgresql", host: "private", port: 5432, database: "app", user: "admin", password: "temporary-secret", tls: false }));
  await Promise.resolve();
  malformedCloseCalls = calls.filter(function (entry) { return entry.action === "studioDatabase.close" && entry.params.handle === handle; }).length;
  assert(malformedOpenFailure.code === "FAILED" && malformedCloseCalls === closeCallsBeforeExtra + 1,
    "extra-field open metadata stranded its own-data syntactically valid raw handle");

  mode = "malformedOpenGetter";
  var closeCallsBeforeGetter = calls.filter(function (entry) { return entry.action === "studioDatabase.close" && entry.params.handle === handle; }).length;
  malformedOpenFailure = await rejected(service.open({ driver: "postgresql", host: "private", port: 5432, database: "app", user: "admin", password: "temporary-secret", tls: false }));
  await Promise.resolve();
  malformedCloseCalls = calls.filter(function (entry) { return entry.action === "studioDatabase.close" && entry.params.handle === handle; }).length;
  assert(malformedOpenFailure.code === "FAILED" && handleGetterReads === 0 && malformedCloseCalls === closeCallsBeforeGetter,
    "malformed open cleanup invoked an accessor or trusted its returned handle");

  mode = "auth";
  var failure = await rejected(service.open({ driver: "postgresql", host: "private", port: 5432, database: "app", user: "admin", password: "temporary-secret", tls: true }));
  assert(failure.name === "KitStudioDatabaseError" && failure.code === "AUTHENTICATION_FAILED" && failure.operation === "open" && String(failure.message).indexOf("secret") === -1, "native credential detail escaped");

  var noHost = install(null);
  failure = await rejected(noHost.services.studioDatabase.open({ driver: "mysql", host: "localhost", port: 3306, database: "shop", user: "root", password: "", tls: false }));
  assert(failure.code === "UNAVAILABLE" && failure.operation === "open", "browser preview did not fail closed");
})().catch(function (error) {
  console.error(error && error.stack || error);
  process.exitCode = 1;
});
`
	runNativeHostNode(t, script)
}
