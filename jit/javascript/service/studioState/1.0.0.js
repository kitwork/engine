;(function (global, kit) {
"use strict";

// KitJS staged service: studioState@1.0.0
// The desktop host owns the private KitDB file. This service exposes only
// bounded application metadata; credential references are reduced to a
// boolean and passwords never enter this contract.
var MAX_ID = 128;
var MAX_NAME = 256;
var MAX_HOST = 255;
var MAX_PATH = 4096;
var MAX_SQL = 64 * 1024;
var MAX_ERROR = 128;
var MAX_CONNECTIONS = 256;
var MAX_SAVED_QUERIES = 1000;
var MAX_HISTORY = 10000;
var MAX_PREFERENCES = 128;
var MAX_PREFERENCE_JSON = 64 * 1024;
var objectPrototype = Object.prototype;
var arrayPrototype = Array.prototype;
var objectFreeze = Object.freeze;
var objectKeys = Object.keys;
var objectGetOwnPropertyDescriptors = Object.getOwnPropertyDescriptors;
var objectGetOwnPropertySymbols = Object.getOwnPropertySymbols;
var objectGetPrototypeOf = Object.getPrototypeOf;
var hasOwn = Function.prototype.call.bind(objectPrototype.hasOwnProperty);
var arrayIsArray = Array.isArray;
var numberIsFinite = Number.isFinite;
var numberIsSafeInteger = Number.isSafeInteger;
var StringType = global.String;
var ErrorType = global.Error;
var JSONType = global.JSON;
var PromiseType = global.Promise;
var promiseResolve = Function.prototype.call.bind(PromiseType.resolve, PromiseType);
var promiseReject = Function.prototype.call.bind(PromiseType.reject, PromiseType);
var assembly = global.document && global.document[Symbol.for("kitjs:assembly")];
var nativeHost = assembly && assembly.nativeHost;
var privateErrors = new WeakSet();

function errorCode(value) {
  var code = "";
  try { code = value && typeof value.code === "string" ? value.code : ""; }
  catch (_) { return "FAILED"; }
  if (code === "BRIDGE_BUSY" || code === "CONFLICT" || code === "OVERLOADED") return "BUSY";
  if (code === "BRIDGE_TIMEOUT") return "TIMEOUT";
  if (code === "BRIDGE_UNAVAILABLE" || code === "STORE_CLOSED") return "UNAVAILABLE";
  if (code === "INVALID_ARGUMENT") return "INVALID_REQUEST";
  if (code === "LIMIT_REACHED" || code === "TOO_LARGE") return "LIMIT";
  if (["DENIED", "CANCELLED", "UNAVAILABLE", "TIMEOUT", "BUSY", "NOT_FOUND", "INVALID_REQUEST", "LIMIT", "FAILED"].indexOf(code) !== -1) return code;
  return "FAILED";
}

function stateError(code, operation) {
  code = errorCode({ code: code });
  var messages = {
    DENIED: "Studio storage access was denied",
    CANCELLED: "Studio storage operation was cancelled",
    UNAVAILABLE: "Persistent Studio storage is unavailable",
    TIMEOUT: "Studio storage operation timed out",
    BUSY: "Studio storage is busy",
    NOT_FOUND: "Stored Studio item was not found",
    INVALID_REQUEST: "Studio storage request is invalid",
    LIMIT: "Studio storage limit was reached",
    FAILED: "Studio storage operation failed"
  };
  var error = new ErrorType(messages[code] || messages.FAILED);
  Object.defineProperties(error, {
    name: { value: "KitStudioStateError" },
    code: { value: code, enumerable: true },
    operation: { value: operation, enumerable: true }
  });
  objectFreeze(error);
  privateErrors.add(error);
  return error;
}

function nativeCall(action, params, operation, validator) {
  if (!nativeHost) return promiseReject(stateError("UNAVAILABLE", operation));
  try {
    return promiseResolve(nativeHost.call("studioState." + action, objectFreeze(params))).then(function (value) {
      try { return validator(value); }
      catch (error) {
        if (privateErrors.has(error)) throw error;
        throw stateError("FAILED", operation);
      }
    }, function (error) {
      throw stateError(errorCode(error), operation);
    });
  } catch (error) {
    return promiseReject(privateErrors.has(error) ? error : stateError(errorCode(error), operation));
  }
}

function exactObject(value, keys) {
  var prototype;
  var symbols;
  var descriptors;
  try {
    prototype = value && objectGetPrototypeOf(value);
    symbols = value && objectGetOwnPropertySymbols(value);
    descriptors = value && objectGetOwnPropertyDescriptors(value);
  } catch (_) { return null; }
  if (!value || prototype !== objectPrototype && prototype !== null || symbols.length || objectKeys(descriptors).length !== keys.length) return null;
  for (var index = 0; index < keys.length; index++) {
    var descriptor = descriptors[keys[index]];
    if (!descriptor || !descriptor.enumerable || !hasOwn(descriptor, "value") || descriptor.get || descriptor.set) return null;
  }
  return descriptors;
}

function exactArray(value, maximum) {
  var prototype;
  var descriptors;
  var symbols;
  try {
    if (!arrayIsArray(value)) return null;
    prototype = objectGetPrototypeOf(value);
    descriptors = objectGetOwnPropertyDescriptors(value);
    symbols = objectGetOwnPropertySymbols(value);
  } catch (_) { return null; }
  var length = descriptors && descriptors.length && descriptors.length.value;
  if (prototype !== arrayPrototype || symbols.length || !numberIsSafeInteger(length) || length < 0 || length > maximum || objectKeys(descriptors).length !== length + 1) return null;
  var output = new Array(length);
  for (var index = 0; index < length; index++) {
    var descriptor = descriptors[StringType(index)];
    if (!descriptor || !descriptor.enumerable || !hasOwn(descriptor, "value") || descriptor.get || descriptor.set) return null;
    output[index] = descriptor.value;
  }
  return output;
}

function safeText(value, maximum, allowEmpty, controls) {
  if (typeof value !== "string" || !allowEmpty && value === "" || value.length > maximum) return null;
  for (var index = 0; index < value.length; index++) {
    var unit = value.charCodeAt(index);
    if (unit === 0 || controls && (unit <= 31 || unit >= 127 && unit <= 159)) return null;
  }
  return value;
}

function safeID(value) {
  var text = safeText(value, MAX_ID, false, true);
  if (!text || text === "__proto__" || text === "constructor" || text === "prototype") return null;
  return text;
}

function safeInteger(value, minimum, maximum) {
  return numberIsSafeInteger(value) && value >= minimum && value <= maximum ? value : null;
}

function trueResult(value) {
  if (value !== true) throw new TypeError("expected true");
  return true;
}

function connectionInput(value) {
  var fields = ["id", "name", "driver", "host", "port", "defaultDatabase", "path", "user", "tls", "rememberPassword"];
  var d = exactObject(value, fields);
  if (!d) throw new TypeError("studioState.upsertConnection expects one exact connection record");
  var driver = safeText(d.driver.value, 32, false, true);
  if (["PostgreSQL", "MySQL", "MariaDB", "SQL Server", "Redis", "SQLite"].indexOf(driver) === -1) throw new TypeError("connection driver is invalid");
  var record = {
    id: safeID(d.id.value),
    name: safeText(d.name.value, MAX_NAME, false, true),
    driver: driver,
    host: safeText(d.host.value, MAX_HOST, true, true),
    port: safeText(d.port.value, 5, true, true),
    database: safeText(d.defaultDatabase.value, MAX_NAME, false, true),
    path: safeText(d.path.value, MAX_PATH, true, true),
    user: safeText(d.user.value, MAX_NAME, true, true),
    tls: d.tls.value,
    rememberPassword: d.rememberPassword.value
  };
  if (!record.id || !record.name || record.host === null || record.port === null || !record.database || record.path === null || record.user === null || typeof record.tls !== "boolean" || typeof record.rememberPassword !== "boolean") throw new TypeError("connection record is invalid");
  if (driver === "SQLite" ? record.path === "" || record.host !== "" || record.port !== "" || record.user !== "" : record.host === "" || !/^\d+$/.test(record.port) || Number(record.port) < 1 || Number(record.port) > 65535 || record.path !== "") throw new TypeError("connection endpoint is invalid");
  record.port = driver === "SQLite" ? 0 : Number(record.port);
  return objectFreeze(record);
}

function connectionResult(value) {
  var fields = ["id", "name", "driver", "host", "port", "defaultDatabase", "path", "user", "tls", "credentialAvailable", "createdAt", "updatedAt", "lastOpenedAt", "favorite", "sortOrder"];
  var d = exactObject(value, fields);
  if (!d || typeof d.credentialAvailable.value !== "boolean") throw new TypeError("invalid stored connection");
  var base = connectionInput({
    id: d.id.value, name: d.name.value, driver: d.driver.value, host: d.host.value,
    port: d.port.value, defaultDatabase: d.defaultDatabase.value, path: d.path.value,
    user: d.user.value, tls: d.tls.value, rememberPassword: d.credentialAvailable.value
  });
  var createdAt = safeInteger(d.createdAt.value, 0, Number.MAX_SAFE_INTEGER);
  var updatedAt = safeInteger(d.updatedAt.value, 0, Number.MAX_SAFE_INTEGER);
  var lastOpenedAt = safeInteger(d.lastOpenedAt.value, 0, Number.MAX_SAFE_INTEGER);
  var sortOrder = safeInteger(d.sortOrder.value, 0, 1000000);
  if (createdAt === null || updatedAt === null || lastOpenedAt === null || sortOrder === null || typeof d.favorite.value !== "boolean") throw new TypeError("invalid stored connection metadata");
  return objectFreeze({
    id: base.id, name: base.name, driver: base.driver, host: base.host, port: d.port.value,
    defaultDatabase: base.database, path: base.path, user: base.user, tls: base.tls,
    credentialAvailable: d.credentialAvailable.value, rememberPassword: d.credentialAvailable.value,
    createdAt: createdAt, updatedAt: updatedAt,
    lastOpenedAt: lastOpenedAt, favorite: d.favorite.value, sortOrder: sortOrder
  });
}

function savedQueryInput(value) {
  var fields = ["id", "connectionId", "database", "schema", "name", "sql", "createdAt", "updatedAt"];
  var d = exactObject(value, fields);
  if (!d) throw new TypeError("studioState.upsertSavedQuery expects one exact saved query");
  var record = {
    id: safeID(d.id.value),
    connectionId: safeText(d.connectionId.value, MAX_ID, true, true),
    database: safeText(d.database.value, MAX_NAME, true, true),
    schema: safeText(d.schema.value, MAX_NAME, true, true),
    name: safeText(d.name.value, MAX_NAME, false, true),
    sql: safeText(d.sql.value, MAX_SQL, false, false),
    createdAt: safeInteger(d.createdAt.value, 0, Number.MAX_SAFE_INTEGER),
    updatedAt: safeInteger(d.updatedAt.value, 0, Number.MAX_SAFE_INTEGER)
  };
  for (var key in record) if (record[key] === null) throw new TypeError("saved query is invalid");
  return objectFreeze(record);
}

function historyInput(value) {
  var fields = ["id", "connectionId", "database", "schema", "sql", "durationMs", "rowCount", "status", "errorCode", "executedAt"];
  var d = exactObject(value, fields);
  if (!d) throw new TypeError("studioState.appendHistory expects one exact history record");
  var status = d.status.value;
  var record = {
    id: safeID(d.id.value),
    connectionId: safeText(d.connectionId.value, MAX_ID, true, true),
    database: safeText(d.database.value, MAX_NAME, true, true),
    schema: safeText(d.schema.value, MAX_NAME, true, true),
    sql: safeText(d.sql.value, MAX_SQL, false, false),
    durationMs: safeInteger(d.durationMs.value, 0, 600000),
    rowCount: safeInteger(d.rowCount.value, -1, 1000000000),
    status: status,
    errorCode: safeText(d.errorCode.value, MAX_ERROR, true, true),
    executedAt: safeInteger(d.executedAt.value, 1, Number.MAX_SAFE_INTEGER)
  };
  if (status !== "success" && status !== "error" && status !== "cancelled") throw new TypeError("history status is invalid");
  for (var key in record) if (record[key] === null) throw new TypeError("history record is invalid");
  return objectFreeze(record);
}

function preferenceResult(value) {
  var d = exactObject(value, ["key", "value", "updatedAt"]);
  if (!d) throw new TypeError("invalid stored preference");
  var key = safeID(d.key.value);
  var updatedAt = safeInteger(d.updatedAt.value, 0, Number.MAX_SAFE_INTEGER);
  var clone;
  try {
    var json = JSONType.stringify(d.value.value);
    if (typeof json !== "string" || json.length > MAX_PREFERENCE_JSON) return null;
    clone = JSONType.parse(json);
  } catch (_) { return null; }
  if (!key || updatedAt === null) return null;
  return objectFreeze({ key: key, value: clone, updatedAt: updatedAt });
}

function preferencesResult(value) {
  var prototype;
  var descriptors;
  var symbols;
  try {
    prototype = value && objectGetPrototypeOf(value);
    descriptors = value && objectGetOwnPropertyDescriptors(value);
    symbols = value && objectGetOwnPropertySymbols(value);
  } catch (_) { return null; }
  if (!value || prototype !== objectPrototype && prototype !== null || symbols.length || objectKeys(descriptors).length > MAX_PREFERENCES) return null;
  var output = Object.create(null);
  var keys = objectKeys(descriptors);
  for (var index = 0; index < keys.length; index++) {
    var key = keys[index];
    var descriptor = descriptors[key];
    var clone;
    if (!safeID(key) || !descriptor || !descriptor.enumerable || !hasOwn(descriptor, "value") || descriptor.get || descriptor.set) return null;
    try {
      var json = JSONType.stringify(descriptor.value);
      if (typeof json !== "string" || json.length > MAX_PREFERENCE_JSON) return null;
      clone = JSONType.parse(json);
    } catch (_) { return null; }
    output[key] = clone;
  }
  return objectFreeze(output);
}

function mapRecords(value, maximum, mapper) {
  var input = exactArray(value, maximum);
  if (!input) throw new TypeError("invalid Studio state list");
  var output = new Array(input.length);
  for (var index = 0; index < input.length; index++) {
    output[index] = mapper(input[index]);
    if (!output[index]) throw new TypeError("invalid Studio state record");
  }
  return objectFreeze(output);
}

function snapshotResult(value) {
  var d = exactObject(value, ["connections", "savedQueries", "history", "preferences"]);
  if (!d) throw new TypeError("invalid Studio state snapshot");
  var preferences = preferencesResult(d.preferences.value);
  if (!preferences) throw new TypeError("invalid Studio preferences");
  return objectFreeze({
    connections: mapRecords(d.connections.value, MAX_CONNECTIONS, connectionResult),
    savedQueries: mapRecords(d.savedQueries.value, MAX_SAVED_QUERIES, savedQueryInput),
    history: mapRecords(d.history.value, MAX_HISTORY, historyInput),
    preferences: preferences
  });
}

function loadSnapshot() {
  if (arguments.length !== 0) throw new TypeError("studioState.loadSnapshot does not accept arguments");
  return nativeCall("loadSnapshot", {}, "loadSnapshot", snapshotResult);
}

function upsertConnection(value) {
  if (arguments.length !== 1) throw new TypeError("studioState.upsertConnection expects one record");
  var checked = connectionInput(value);
  return nativeCall("upsertConnection", {
    connection: objectFreeze({
      id: checked.id, name: checked.name, driver: checked.driver, host: checked.host,
      port: checked.port, defaultDatabase: checked.database, path: checked.path,
      user: checked.user, tls: checked.tls
    }),
    rememberPassword: checked.rememberPassword
  }, "upsertConnection", connectionResult);
}

function deleteConnection(id) {
  if (arguments.length !== 1 || !safeID(id)) throw new TypeError("studioState.deleteConnection expects one ID");
  return nativeCall("deleteConnection", { id: id }, "deleteConnection", trueResult);
}

function upsertSavedQuery(value) {
  if (arguments.length !== 1) throw new TypeError("studioState.upsertSavedQuery expects one record");
  return nativeCall("upsertSavedQuery", { query: savedQueryInput(value) }, "upsertSavedQuery", savedQueryInput);
}

function deleteSavedQuery(id) {
  if (arguments.length !== 1 || !safeID(id)) throw new TypeError("studioState.deleteSavedQuery expects one ID");
  return nativeCall("deleteSavedQuery", { id: id }, "deleteSavedQuery", trueResult);
}

function appendHistory(value) {
  if (arguments.length !== 1) throw new TypeError("studioState.appendHistory expects one record");
  return nativeCall("appendHistory", { history: historyInput(value) }, "appendHistory", historyInput);
}

function clearHistory() {
  if (arguments.length !== 0) throw new TypeError("studioState.clearHistory does not accept arguments");
  return nativeCall("clearHistory", {}, "clearHistory", trueResult);
}

function updatePreference(key, value) {
  if (arguments.length !== 2 || !safeID(key)) throw new TypeError("studioState.updatePreference expects a key and JSON value");
  var clone;
  try {
    var json = JSONType.stringify(value);
    if (typeof json !== "string" || json.length > MAX_PREFERENCE_JSON) throw new TypeError("preference value is invalid");
    clone = JSONType.parse(json);
  } catch (_) { throw new TypeError("preference value is invalid"); }
  return nativeCall("updatePreference", { key: key, value: clone }, "updatePreference", preferenceResult);
}

kit.service("studioState", {
  loadSnapshot: loadSnapshot,
  upsertConnection: upsertConnection,
  deleteConnection: deleteConnection,
  upsertSavedQuery: upsertSavedQuery,
  deleteSavedQuery: deleteSavedQuery,
  appendHistory: appendHistory,
  clearHistory: clearHistory,
  updatePreference: updatePreference
});
})(globalThis, kit);
