package javascript

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestStudioSQLiteServiceStaticContract(t *testing.T) {
	source := readVanillaFile(t, "service", "studioSqlite", "1.0.0.js")
	if len(source) == 0 || source[0] != ';' || source[len(source)-1] != '\n' || bytes.Contains(source, []byte{'\r'}) {
		t.Fatal("studioSqlite@1.0.0 is not a sealable LF-only classic script")
	}
	if got := bytes.Count(source, []byte(`kit.service("studioSqlite"`)); got != 1 {
		t.Fatalf("studioSqlite registration count = %d, want one", got)
	}
	for _, contract := range [][]byte{
		[]byte(`open: open`),
		[]byte(`catalog: catalog`),
		[]byte(`tablePage: tablePage`),
		[]byte(`query: query`),
		[]byte(`close: close`),
		[]byte(`nativeHost.call("studioSqlite." + action`),
		[]byte(`var references = new WeakMap()`),
		[]byte(`var operations = new WeakMap()`),
		[]byte(`var OPEN_TIMEOUT_MS = 10 * 60 * 1000`),
		[]byte(`var OPEN_MAX_POLLS = 2400`),
		[]byte(`name: { value: "KitStudioSQLiteError" }`),
	} {
		if !bytes.Contains(source, contract) {
			t.Errorf("studioSqlite@1.0.0 lost contract %q", contract)
		}
	}
	for _, forbidden := range [][]byte{
		[]byte(`globalThis.kit`),
		[]byte(`window.kit`),
		[]byte(`kit.component(`),
		[]byte(`path:`),
		[]byte(`dsn`),
		[]byte(`beginOpen: beginOpen`),
		[]byte(`pollOpen: pollOpen`),
		[]byte(`releaseOpen: releaseOpen`),
	} {
		if bytes.Contains(bytes.ToLower(source), bytes.ToLower(forbidden)) {
			t.Errorf("studioSqlite@1.0.0 exposes forbidden coupling %q", forbidden)
		}
	}
}

func TestStudioSQLiteCatalogClosesExactSealedGraph(t *testing.T) {
	composer, err := NewDefaultComposer()
	if err != nil {
		t.Fatal(err)
	}
	service, err := composer.catalog.service(ServiceVersion{Name: "studioSqlite", Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(service.actions) != 0 || len(service.requires) != 0 {
		t.Fatalf("studioSqlite catalog definition = %#v", service)
	}
	if _, err := composer.catalog.service(ServiceVersion{Name: "studioSqlite", Version: "1.0.1"}); !errors.Is(err, ErrModuleNotFound) {
		t.Fatalf("unknown studioSqlite version error = %v", err)
	}

	bundle, err := composer.ComposeHTML([]byte(`<main data-kit-component="app@1.11.0" data-kit-alias="$app"></main>`))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{
		`services["studioSqlite"] = "1.0.0"`,
		`grants["app"]["studioSqlite"] = "1.0.0"`,
		`KitJS staged service: studioSqlite@1.0.0`,
	} {
		if !strings.Contains(string(bundle.JavaScript), marker) {
			t.Fatalf("app@1.11.0 artifact omitted %q", marker)
		}
	}
	if bytes.Contains(bundle.JavaScript, []byte(`actions["studioSqlite"]["`)) ||
		appGrantsAuthoredService("1.11.0", "studioSqlite") ||
		validAuthoredServiceAction("studioSqlite", "query") {
		t.Fatal("studioSqlite gained authored-expression authority")
	}
}

func TestStudioSQLiteNativeNodeContract(t *testing.T) {
	serviceSource := readVanillaFile(t, "service", "studioSqlite", "1.0.0.js")
	script := `
"use strict";
var ASSEMBLY = Symbol.for("kitjs:assembly");
function assert(value, message) { if (!value) throw new Error(message); }
async function rejected(value) {
  try { await value; } catch (error) { return error; }
  throw new Error("promise unexpectedly resolved");
}
async function turns(count) { while (count-- > 0) await Promise.resolve(); }

var nextTimer = 0;
var deadlines = new Map();
globalThis.setTimeout = function (callback, delay) {
  var id = ++nextTimer;
  if (delay === 250) queueMicrotask(callback);
  else if (delay === 10 * 60 * 1000) deadlines.set(id, callback);
  else throw new Error("unexpected timer delay " + delay);
  return id;
};
globalThis.clearTimeout = function (id) { deadlines.delete(id); };
function fireDeadlines() {
  var callbacks = Array.from(deadlines.values());
  deadlines.clear();
  callbacks.forEach(function (callback) { callback(); });
}

function install(host) {
  var document = {};
  var core = { graph: { services: { studioSqlite: "1.0.0" } }, nativeHost: host };
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
  return { document: document, core: core, kit: kit, services: namespaces };
}

(async function () {
  var calls = [];
  var released = [];
  var closed = [];
  var reported = 0;
  var mode = "success";
  var releaseReject = false;
  var queryFailureCode = "SQL_ERROR";
  var pollCount = 0;
  var delayedPollResolve = null;
  var operation = "O".repeat(32);
  var handle = "H".repeat(32);
  var secondHandle = "J".repeat(32);
  var columns = [
    { key: "id", position: 1, name: "id", type: "INTEGER", nullable: "No", keyLabel: "PRIMARY KEY", defaultValue: "—", primaryOrder: 1, generated: false },
    { key: "payload", position: 2, name: "payload", type: "BLOB", nullable: "Yes", keyLabel: "", defaultValue: "—", primaryOrder: 0, generated: false }
  ];
  var catalogPayload = {
    format: "kitwork-studio-native-sqlite-catalog", version: 1, ok: true, source: "sqlite",
    database: "demo.sqlite", schema: "main", readOnly: true,
    catalogVersion: 7,
    objects: [{
      id: "table-orders archive", name: "orders archive", type: "table", schema: "main",
      columns: columns,
      indexes: [{ name: "orders archive_pkey", columns: "id", kind: "Primary key", method: "btree", unique: true, partial: false }],
      foreignKeys: [{ name: "fk_orders archive_0", columns: "id", target: "main.users (id)", onUpdate: "NO ACTION", onDelete: "CASCADE", targetTable: "users", targetColumns: "id" }],
      withoutRowid: false, strict: false
    }]
  };
  var tablePayload = {
    format: "kitwork-studio-native-sqlite-table", version: 1, ok: true, source: "sqlite",
    database: "demo.sqlite", schema: "main", readOnly: true,
    catalogVersion: 7, name: "orders archive", type: "table", columns: columns,
    rows: [[3, { type: "blob", base64: "AQ==", bytes: 1 }]], totalRows: 3,
    page: 2, pageSize: 2, pageCount: 2, pageStart: 3, pageEnd: 3,
    hasPreviousPage: true, hasNextPage: false, durationMs: 4
  };
  var queryPayload = {
    format: "kitwork-studio-native-sqlite-query", version: 1, ok: true, source: "sqlite",
    database: "demo.sqlite", schema: "main", readOnly: true,
    columns: [{ name: "value", type: "INTEGER" }, { name: "value", type: "BLOB" }],
    rows: [[1, { type: "blob", base64: "AQ==", bytes: 1 }]],
    rowCount: 1, returnedRows: 1, truncated: false, durationMs: 2
  };
  globalThis.reportError = function () { reported++; };
  var host = {
    call: function (action, params) {
      calls.push({ action: action, params: params, frozen: Object.isFrozen(params) });
      if (action === "studioSqlite.beginOpen") {
        pollCount = 0;
        if (mode === "beginDenied") return Promise.reject({ code: "DENIED", message: "private location" });
        if (mode === "badOperation") return { operation: "short" };
        return { operation: operation };
      }
      if (action === "studioSqlite.pollOpen") {
        assert(params.operation === operation, "poll operation changed");
        pollCount++;
        if (mode === "hold") return new Promise(function (resolve) { delayedPollResolve = resolve; });
        if (mode === "cancelled") return { status: "cancelled" };
        if (mode === "failed") return { status: "failed", error: { code: "DATABASE_UNAVAILABLE", message: "C:\\private\\secret.db" } };
        if (mode === "malformed") return { status: "connected", handle: handle, name: "demo.sqlite", database: "demo.sqlite", schema: "main", readOnly: true, secret: true };
        if (pollCount === 1) return { status: "pending" };
        return { status: "connected", handle: handle, name: "demo.sqlite", database: "demo.sqlite", schema: "main", readOnly: true };
      }
      if (action === "studioSqlite.releaseOpen") {
        released.push(params.operation);
        if (releaseReject) return Promise.reject({ code: "FAILED", message: "private operation" });
        return true;
      }
      if (action === "studioSqlite.catalog") {
        if (mode === "badCatalog") return Object.assign({ extra: true }, catalogPayload);
        if (mode === "badDatabase") return Object.assign({}, catalogPayload, { database: "other.sqlite" });
        if (mode === "catalogFailure") return Promise.reject({ code: "CATALOG_TOO_LARGE", message: "private catalog detail" });
        return catalogPayload;
      }
      if (action === "studioSqlite.tablePage") {
        if (mode === "badPage") return Object.assign({}, tablePayload, { pageStart: 2 });
        if (params.catalogVersion === 0) return Object.assign({}, tablePayload, {
          catalogVersion: 0, page: 1, rows: [[1, null]], totalRows: 1,
          pageCount: 1, pageStart: 1, pageEnd: 1, hasPreviousPage: false, hasNextPage: false
        });
        return tablePayload;
      }
      if (action === "studioSqlite.query") {
        if (mode === "queryFailure") return Promise.reject({ code: queryFailureCode, message: "C:\\private\\secret.db: syntax" });
        if (mode === "badQuery") return Object.assign({}, queryPayload, { returnedRows: 2 });
        return queryPayload;
      }
      if (action === "studioSqlite.close") {
        closed.push(params.handle);
        return true;
      }
      throw new Error("unexpected native action " + action);
    }
  };

  var installed = install(host);
  var service = installed.services.studioSqlite;
  assert(Object.isFrozen(service) && Object.keys(service).sort().join(",") === "catalog,close,open,query,tablePage" &&
    service.beginOpen === undefined && service.pollOpen === undefined && service.releaseOpen === undefined,
    "studioSqlite public surface widened");

  var reference = await service.open();
  assert(Object.isFrozen(reference) && Object.keys(reference).sort().join(",") === "name,readOnly,schema" &&
    reference.name === "demo.sqlite" && reference.schema === "main" && reference.readOnly === true &&
    reference.handle === undefined && reference.operation === undefined && reference.database === undefined,
    "open leaked native connection authority");
  assert(released.length === 1 && released[0] === operation && calls.every(function (entry) { return entry.frozen; }),
    "open did not release its operation or freeze native parameters");

  var catalog = await service.catalog(reference);
  assert(Object.isFrozen(catalog) && Object.isFrozen(catalog.objects) && Object.isFrozen(catalog.objects[0]) &&
    Object.isFrozen(catalog.objects[0].columns) && Object.isFrozen(catalog.objects[0].columns[0]) &&
    Object.isFrozen(catalog.objects[0].indexes[0]) && Object.isFrozen(catalog.objects[0].foreignKeys[0]),
    "catalog was not deeply frozen");
  assert(Object.keys(catalog).sort().join(",") === "catalogVersion,objects" && catalog.ok === undefined &&
    Object.keys(catalog.objects[0]).sort().join(",") === "columns,foreignKeys,id,indexes,name,schema,type" &&
    catalog.objects[0].schema === "main" && catalog.objects[0].withoutRowid === undefined &&
    catalog.objects[0].columns[0].primaryOrder === 1 && catalog.objects[0].indexes[0].unique === true &&
    catalog.objects[0].foreignKeys[0].targetTable === "users",
    "catalog did not strip transport fields while preserving safe schema metadata");
  catalogPayload.objects[0].columns[0].name = "mutated";
  assert(catalog.objects[0].columns[0].name === "id", "catalog retained a mutable host object");
  catalogPayload.objects[0].columns[0].name = "id";

  var page = await service.tablePage(reference, {
    type: "table", name: "orders archive", catalogVersion: 7, page: 2, pageSize: 2
  });
  assert(Object.isFrozen(page) && Object.isFrozen(page.rows) && Object.isFrozen(page.rows[0]) &&
    page.pageStart === 3 && page.pageEnd === 3 && page.pageCount === 2 && page.rows[0][1].bytes === 1 &&
    page.ok === undefined && page.handle === undefined,
    "tablePage did not return one canonical immutable page");
  var pageCall = calls.filter(function (entry) { return entry.action === "studioSqlite.tablePage"; }).slice(-1)[0];
  assert(Object.keys(pageCall.params).sort().join(",") === "catalogVersion,handle,name,page,pageSize,type" &&
    pageCall.params.handle === handle && pageCall.params.page === 2,
    "tablePage did not route through the private handle and exact bounded options");
  var zeroVersionPage = await service.tablePage(reference, {
    type: "table", name: "orders archive", catalogVersion: 0, page: 1, pageSize: 2
  });
  assert(zeroVersionPage.catalogVersion === 0 && zeroVersionPage.pageStart === 1,
    "tablePage rejected SQLite schema version zero");

  var query = await service.query(reference, "SELECT 1, x'01'");
  assert(Object.isFrozen(query) && Object.isFrozen(query.columns) && Object.isFrozen(query.rows) &&
    query.columns[0].name === query.columns[1].name && query.rows[0][1].base64 === "AQ==" &&
    query.ok === undefined && query.handle === undefined,
    "query did not preserve duplicate aliases/BLOBs in a private immutable snapshot");
  var queryCall = calls.filter(function (entry) { return entry.action === "studioSqlite.query"; }).slice(-1)[0];
  assert(Object.keys(queryCall.params).sort().join(",") === "handle,sql" && queryCall.params.handle === handle,
    "query did not route through the private handle");

  assert(await service.close(reference) === true && await service.close(reference) === true &&
    closed.filter(function (value) { return value === handle; }).length === 1,
    "close was not idempotent");
  var beforeCalls = calls.length;
  var typeFailures = [];
  try { service.catalog(reference); } catch (error) { typeFailures.push(error instanceof TypeError); }
  try { service.catalog(Object.freeze({ name: "demo.sqlite", schema: "main", readOnly: true })); } catch (error) { typeFailures.push(error instanceof TypeError); }
  try { service.tablePage(reference, { type: "table", name: "orders archive", catalogVersion: 7, page: 1, pageSize: 2 }); } catch (error) { typeFailures.push(error instanceof TypeError); }
  assert(typeFailures.length === 3 && typeFailures.every(Boolean) && calls.length === beforeCalls,
    "closed or forged references reached the native host");

  mode = "cancelled";
  assert(await service.open() === null && released.length === 2, "picker cancellation did not resolve null and release");

  mode = "failed";
  var failure = await rejected(service.open());
  assert(failure.name === "KitStudioSQLiteError" && failure.code === "UNAVAILABLE" &&
    failure.operation === "open" && Object.isFrozen(failure) && failure.message.indexOf("private") < 0 &&
    failure.message.indexOf("secret.db") < 0 && released.length === 3,
    "open failure leaked host detail or skipped cleanup");

  mode = "beginDenied";
  failure = await rejected(service.open());
  assert(failure.code === "DENIED" && failure.operation === "open" && failure.message.indexOf("private") < 0 &&
    released.length === 3, "begin rejection was not safely normalized");

  mode = "badOperation";
  failure = await rejected(service.open());
  assert(failure.code === "FAILED" && released.length === 3, "malformed operation escaped validation");

  mode = "malformed";
  failure = await rejected(service.open());
  assert(failure.code === "FAILED" && released.length === 4, "non-exact connected result escaped validation");

  mode = "success";
  releaseReject = true;
  var beforeReported = reported;
  reference = await service.open();
  assert(reference.name === "demo.sqlite" && reported === beforeReported + 1,
    "best-effort operation release changed a valid connection");
  releaseReject = false;

  mode = "catalogFailure";
  failure = await rejected(service.catalog(reference));
  assert(failure.code === "LIMIT" && failure.operation === "catalog" &&
    failure.message.indexOf("private") < 0, "catalog failure leaked native detail");
  mode = "badCatalog";
  failure = await rejected(service.catalog(reference));
  assert(failure.code === "FAILED", "non-exact catalog escaped validation");
  mode = "badDatabase";
  failure = await rejected(service.catalog(reference));
  assert(failure.code === "FAILED", "catalog for a different private connection escaped validation");
  mode = "badPage";
  failure = await rejected(service.tablePage(reference, {
    type: "table", name: "orders archive", catalogVersion: 7, page: 2, pageSize: 2
  }));
  assert(failure.code === "FAILED" && failure.operation === "tablePage", "inconsistent table page escaped validation");
  mode = "queryFailure";
  failure = await rejected(service.query(reference, "SELECT broken"));
  assert(failure.code === "SQL_ERROR" && failure.operation === "query" &&
    failure.message.indexOf("secret.db") < 0, "query failure leaked native detail");
  var mappedCodes = [
    ["OBJECT_NOT_FOUND", "NOT_FOUND"],
    ["UNSTABLE_ORDER", "UNSUPPORTED"],
    ["INVALID_QUERY", "INVALID_REQUEST"],
    ["QUERY_TOO_LARGE", "LIMIT"],
    ["DATABASE_CORRUPT", "DATABASE_CORRUPT"]
  ];
  for (var mappedIndex = 0; mappedIndex < mappedCodes.length; mappedIndex++) {
    queryFailureCode = mappedCodes[mappedIndex][0];
    failure = await rejected(service.query(reference, "SELECT broken"));
    assert(failure.code === mappedCodes[mappedIndex][1] && failure.message.indexOf("secret.db") < 0,
      queryFailureCode + " was not normalized to " + mappedCodes[mappedIndex][1]);
  }
  queryFailureCode = "SQL_ERROR";
  mode = "badQuery";
  failure = await rejected(service.query(reference, "SELECT 1"));
  assert(failure.code === "FAILED", "inconsistent query result escaped validation");
  mode = "success";

  beforeCalls = calls.length;
  typeFailures = [];
  try { service.open({}); } catch (error) { typeFailures.push(error instanceof TypeError); }
  try { service.query(reference, " "); } catch (error) { typeFailures.push(error instanceof TypeError); }
  try { service.query(reference, "x".repeat(32 * 1024 + 1)); } catch (error) { typeFailures.push(error instanceof TypeError); }
  try { service.tablePage(reference, { type: "table", name: "orders archive", catalogVersion: 7, page: 1, pageSize: 121 }); } catch (error) { typeFailures.push(error instanceof TypeError); }
  try { service.tablePage(reference, { type: "table", name: "orders archive", catalogVersion: 7, page: 1, pageSize: 2, extra: true }); } catch (error) { typeFailures.push(error instanceof TypeError); }
  var accessor = { type: "table", name: "orders archive", catalogVersion: 7, page: 1 };
  var getterCalls = 0;
  Object.defineProperty(accessor, "pageSize", { enumerable: true, get: function () { getterCalls++; return 2; } });
  try { service.tablePage(reference, accessor); } catch (error) { typeFailures.push(error instanceof TypeError); }
  assert(typeFailures.length === 6 && typeFailures.every(Boolean) && getterCalls === 0 && calls.length === beforeCalls,
    "invalid caller data reached native SQLite");

  mode = "hold";
  var pending = service.open();
  await turns(8);
  failure = await rejected(service.open());
  assert(failure.code === "BUSY", "two simultaneous file pickers were admitted");
  assert(typeof delayedPollResolve === "function", "held poll was not installed");
  fireDeadlines();
  failure = await rejected(pending);
  assert(failure.code === "TIMEOUT" && failure.operation === "open", "open deadline was not enforced");
  await turns(12);
  var lateCloseBefore = closed.length;
  delayedPollResolve({ status: "connected", handle: secondHandle, name: "late.sqlite", database: "late.sqlite", schema: "main", readOnly: true });
  await turns(12);
  assert(closed.length === lateCloseBefore + 1 && closed[closed.length - 1] === secondHandle,
    "a connection that arrived after timeout leaked its handle");

  await service.close(reference);
  var noHost = install(null);
  failure = await rejected(noHost.services.studioSqlite.open());
  assert(failure.name === "KitStudioSQLiteError" && failure.code === "UNAVAILABLE" &&
    failure.operation === "open", "missing native host did not fail closed");
})().catch(function (error) {
  console.error(error && error.stack || error);
  process.exitCode = 1;
});
`
	runNativeHostNode(t, script)
}
