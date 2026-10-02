;(function (global, kit) {
"use strict";

// KitJS staged service: studioSqlite@1.1.0
// The desktop host owns file selection and every SQLite connection. This
// boundary exposes immutable metadata references, bounded reads, and structured
// atomic edits of tables with a verified row identity. SQL stays host-generated;
// native operation tokens, connection handles, and file locations stay here.
var TOKEN = /^[A-Za-z0-9_-]{32}$/;
var BASE64_VALUE = /^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/;
var BASE64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
var CATALOG_FORMAT = "kitwork-studio-native-sqlite-catalog";
var TABLE_FORMAT = "kitwork-studio-native-sqlite-table";
var QUERY_FORMAT = "kitwork-studio-native-sqlite-query";
var COMMIT_FORMAT = "kitwork-studio-native-sqlite-commit";
var MAX_CHANGES = 100;
var MAX_STATEMENTS = 512;
var MAX_NAME_BYTES = 1024;
var MAX_FILE_NAME_BYTES = 255;
var MAX_SQL_BYTES = 32 * 1024;
var MAX_OBJECTS = 256;
var MAX_COLUMNS = 128;
var MAX_INDEXES = 128;
var MAX_FOREIGN_KEYS = 128;
var MAX_ROWS = 120;
var MAX_PAGE = 1000000;
var MAX_OFFSET = 1000000;
var MAX_CELL_BYTES = 256 * 1024;
var MAX_RESULT_BYTES = 2 * 1024 * 1024;
var MAX_DURATION_MS = 60000;
var POLL_DELAY_MS = 250;
var OPEN_TIMEOUT_MS = 10 * 60 * 1000;
var OPEN_MAX_POLLS = 2400;
var objectPrototype = Object.prototype;
var arrayPrototype = Array.prototype;
var objectCreate = Object.create;
var objectDefineProperties = Object.defineProperties;
var objectFreeze = Object.freeze;
var objectGetOwnPropertyDescriptors = Object.getOwnPropertyDescriptors;
var objectGetOwnPropertyDescriptor = Object.getOwnPropertyDescriptor;
var objectGetOwnPropertySymbols = Object.getOwnPropertySymbols;
var objectGetPrototypeOf = Object.getPrototypeOf;
var objectKeys = Object.keys;
var jsonStringify = JSON.stringify;
var ArrayType = global.Array;
var ErrorType = global.Error;
var StringType = global.String;
var arrayIsArray = Array.isArray;
var numberIsFinite = Number.isFinite;
var numberIsSafeInteger = Number.isSafeInteger;
var MAX_SAFE_INTEGER = Number.MAX_SAFE_INTEGER;
var mathCeil = Math.ceil;
var mathFloor = Math.floor;
var call = Function.prototype.call;
var arrayConcat = call.bind(arrayPrototype.concat);
var hasOwn = call.bind(objectPrototype.hasOwnProperty);
var regExpTest = call.bind(RegExp.prototype.test);
var stringCharCodeAt = call.bind(String.prototype.charCodeAt);
var stringIndexOf = call.bind(String.prototype.indexOf);
var stringSlice = call.bind(String.prototype.slice);
var stringToLowerCase = call.bind(String.prototype.toLowerCase);
var stringTrim = call.bind(String.prototype.trim);
var stringFrom = StringType;
var PromiseType = global.Promise;
var promiseReject = call.bind(PromiseType.reject, PromiseType);
var promiseResolve = call.bind(PromiseType.resolve, PromiseType);
var setTimer = typeof global.setTimeout === "function" && global.setTimeout.bind(global);
var clearTimer = typeof global.clearTimeout === "function" && global.clearTimeout.bind(global);
var assembly = global.document && global.document[Symbol.for("kitjs:assembly")];
var nativeHost = assembly && assembly.nativeHost;
var references = new WeakMap();
var operations = new WeakMap();
var privateErrors = new WeakSet();
var activeOpen = null;

function report(error) {
  try {
    if (typeof global.reportError === "function") {
      global.reportError(error);
      return;
    }
    if (global.console && typeof global.console.error === "function") global.console.error(error);
  } catch (_) { /* Cleanup diagnostics never change public settlement. */ }
}

function canonicalErrorCode(code) {
  if (code === "BRIDGE_BUSY" || code === "CONFLICT" ||
    code === "OVERLOADED" || code === "PICKER_BUSY" || code === "HANDLE_LIMIT" ||
    code === "OPERATION_LIMIT") return "BUSY";
  if (code === "BRIDGE_TIMEOUT") return "TIMEOUT";
  if (code === "BRIDGE_UNAVAILABLE" || code === "MANAGER_CLOSED" ||
    code === "PICKER_UNAVAILABLE" || code === "DATABASE_UNAVAILABLE") return "UNAVAILABLE";
  if (code === "OPERATION_NOT_FOUND" || code === "HANDLE_NOT_FOUND" ||
    code === "OBJECT_NOT_FOUND") return "NOT_FOUND";
  if (code === "LIMIT_REACHED" || code === "CATALOG_TOO_LARGE" ||
    code === "RESULT_TOO_LARGE" || code === "QUERY_TOO_LARGE") return "LIMIT";
  if (code === "UNSTABLE_ORDER") return "UNSUPPORTED";
  if (code === "INVALID_QUERY") return "INVALID_REQUEST";
  if (code === "DENIED" || code === "CANCELLED" || code === "UNAVAILABLE" ||
    code === "TIMEOUT" || code === "BUSY" || code === "NOT_FOUND" || code === "INVALID_REQUEST" ||
    code === "INVALID_DATABASE" || code === "DATABASE_CORRUPT" ||
    code === "READ_ONLY_REQUIRED" || code === "SQL_ERROR" || code === "SCHEMA_CHANGED" ||
    code === "WRITE_UNAVAILABLE" || code === "READ_ONLY_TABLE" || code === "INVALID_CHANGE" ||
    code === "ROW_CONFLICT" || code === "CONSTRAINT_FAILED" ||
    code === "LIMIT" || code === "UNSUPPORTED" || code === "FAILED") return code;
  return "FAILED";
}

function errorCode(value) {
  var code = "";
  try { code = value && typeof value.code === "string" ? value.code : ""; }
  catch (_) { /* Raw host failures never cross this service boundary. */ }
  return canonicalErrorCode(code);
}

function studioError(code, operation, details) {
  code = canonicalErrorCode(code);
  var messages = {
    DENIED: "SQLite file access was denied",
    CANCELLED: "SQLite operation was cancelled",
    UNAVAILABLE: "SQLite file access is unavailable",
    TIMEOUT: "SQLite operation timed out",
    BUSY: "SQLite service is busy",
    NOT_FOUND: "SQLite operation or connection was not found",
    INVALID_REQUEST: "SQLite request is invalid",
    INVALID_DATABASE: "Selected file is not a valid SQLite database",
    READ_ONLY_REQUIRED: "Only a read-only SQLite query is allowed",
    DATABASE_CORRUPT: "Selected SQLite database is corrupt",
    SQL_ERROR: "SQLite could not execute the query",
    LIMIT: "SQLite operation exceeds the supported limit",
    SCHEMA_CHANGED: "SQLite schema changed",
    WRITE_UNAVAILABLE: "SQLite writes are unavailable for this connection",
    READ_ONLY_TABLE: "This SQLite table has no safe editable row identity",
    INVALID_CHANGE: "SQLite pending changes are invalid",
    ROW_CONFLICT: "A row changed or was removed since it was loaded; refresh before committing",
    CONSTRAINT_FAILED: "SQLite rejected a pending change because of a constraint",
    UNSUPPORTED: "SQLite operation is not supported",
    FAILED: "SQLite operation failed"
  };
  var error = new ErrorType(messages[code] || messages.FAILED);
  objectDefineProperties(error, {
    name: { value: "KitStudioSQLiteError" },
    code: { value: code, enumerable: true },
    operation: { value: operation, enumerable: true },
    details: { value: details || null, enumerable: !!details }
  });
  objectFreeze(error);
  privateErrors.add(error);
  return error;
}

function isPrivateError(error) {
  try { return privateErrors.has(error); }
  catch (_) { return false; }
}

function nativeCall(action, params, operation, validator) {
  if (!nativeHost) return promiseReject(studioError("UNAVAILABLE", operation));
  try {
    return promiseResolve(nativeHost.call("studioSqlite." + action, objectFreeze(params))).then(
      function (value) {
        try { return validator(value); }
        catch (error) {
          if (isPrivateError(error)) throw error;
          throw studioError("FAILED", operation);
        }
      },
      function (error) { throw studioError(errorCode(error), operation); }
    );
  } catch (error) {
    return promiseReject(isPrivateError(error) ? error : studioError(errorCode(error), operation));
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
  if (!value || prototype !== objectPrototype && prototype !== null || symbols.length ||
    objectKeys(descriptors).length !== keys.length) return null;
  for (var index = 0; index < keys.length; index++) {
    var descriptor = descriptors[keys[index]];
    if (!descriptor || !descriptor.enumerable || !hasOwn(descriptor, "value") ||
      descriptor.get || descriptor.set) return null;
  }
  return descriptors;
}

function arrayDescriptors(value, minimum, maximum) {
  var prototype;
  var symbols;
  var descriptors;
  try {
    if (!arrayIsArray(value)) return null;
    prototype = objectGetPrototypeOf(value);
    symbols = objectGetOwnPropertySymbols(value);
    descriptors = objectGetOwnPropertyDescriptors(value);
  } catch (_) { return null; }
  var lengthDescriptor = descriptors.length;
  var length = lengthDescriptor && lengthDescriptor.value;
  if (prototype !== arrayPrototype || symbols.length || !lengthDescriptor ||
    hasOwn(lengthDescriptor, "get") || !numberIsSafeInteger(length) ||
    length < minimum || length > maximum || objectKeys(descriptors).length !== length + 1) return null;
  var output = new ArrayType(length);
  for (var index = 0; index < length; index++) {
    var descriptor = descriptors[stringFrom(index)];
    if (!descriptor || !descriptor.enumerable || !hasOwn(descriptor, "value") ||
      descriptor.get || descriptor.set) return null;
    output[index] = descriptor.value;
  }
  return output;
}

function utf8Length(value, maximum) {
  var bytes = 0;
  for (var index = 0; index < value.length; index++) {
    var unit = stringCharCodeAt(value, index);
    if (unit < 0x80) bytes++;
    else if (unit < 0x800) bytes += 2;
    else if (unit >= 0xD800 && unit <= 0xDBFF) {
      if (index + 1 >= value.length) return -1;
      var next = stringCharCodeAt(value, index + 1);
      if (next < 0xDC00 || next > 0xDFFF) return -1;
      bytes += 4;
      index++;
    } else if (unit >= 0xDC00 && unit <= 0xDFFF) return -1;
    else bytes += 3;
    if (bytes > maximum) return bytes;
  }
  return bytes;
}

function safeText(value, maximum, allowEmpty, rejectControls) {
  if (typeof value !== "string" || !allowEmpty && value.length === 0) return null;
  var bytes = utf8Length(value, maximum);
  if (bytes < 0 || bytes > maximum) return null;
  for (var index = 0; index < value.length; index++) {
    var unit = stringCharCodeAt(value, index);
    if (unit === 0 || rejectControls && (unit <= 0x1F || unit >= 0x7F && unit <= 0x9F)) return null;
    if (unit >= 0xD800 && unit <= 0xDBFF) index++;
  }
  return { value: value, bytes: bytes };
}

function safeName(value) {
  var checked = safeText(value, MAX_NAME_BYTES, false, true);
  if (!checked) return null;
  var lower = stringToLowerCase(value);
  if (lower === "__proto__" || lower === "constructor" || lower === "prototype") return null;
  return checked;
}

function safeFileName(value) {
  var checked = safeText(value, MAX_FILE_NAME_BYTES, false, true);
  if (!checked || value === "." || value === ".." ||
    stringIndexOf(value, "/") >= 0 || stringIndexOf(value, "\\") >= 0) return null;
  return checked;
}

function budgetText(budget, value, maximum, allowEmpty, rejectControls) {
  var checked = safeText(value, maximum, allowEmpty, rejectControls);
  if (!checked) return null;
  budget.bytes += checked.bytes;
  return budget.bytes <= (budget.limit || MAX_RESULT_BYTES) ? checked.value : null;
}

function safeInteger(value, minimum, maximum) {
  return numberIsSafeInteger(value) && value >= minimum && value <= maximum;
}

function safeToken(value) {
  return typeof value === "string" && regExpTest(TOKEN, value) ? value : null;
}

function resultEnvelope(value, fields, format, state) {
  var keys = arrayConcat(["format", "version", "ok", "source", "database", "schema", "readOnly"], fields);
  var descriptors = exactObject(value, keys);
  if (!descriptors || descriptors.format.value !== format || descriptors.version.value !== 1 ||
    descriptors.ok.value !== true || descriptors.source.value !== "sqlite" ||
    descriptors.database.value !== state.name || descriptors.schema.value !== "main" ||
    descriptors.readOnly.value !== true) return null;
  return descriptors;
}

function beginResult(value) {
  var descriptors = exactObject(value, ["operation"]);
  var operation = descriptors && safeToken(descriptors.operation.value);
  if (!operation) throw new TypeError("invalid SQLite open operation");
  return operation;
}

function pollResult(value) {
  var descriptors = exactObject(value, ["status"]);
  if (descriptors) {
    var status = descriptors.status.value;
    if (status === "pending" || status === "cancelled") return objectFreeze({ status: status });
    throw new TypeError("invalid SQLite open status");
  }
  descriptors = exactObject(value, ["status", "error"]);
  if (descriptors && descriptors.status.value === "failed") {
    var failure = exactObject(descriptors.error.value, ["code", "message"]);
    if (!failure || typeof failure.code.value !== "string" ||
      !safeText(failure.message.value, 512, false, true)) throw new TypeError("invalid SQLite open failure");
    return objectFreeze({ status: "failed", code: canonicalErrorCode(failure.code.value) });
  }
  descriptors = exactObject(value, ["status", "handle", "name", "database", "schema", "readOnly"]);
  if (!descriptors || descriptors.status.value !== "connected") {
    throw new TypeError("invalid SQLite open result");
  }
  var handle = safeToken(descriptors.handle.value);
  var name = safeFileName(descriptors.name.value);
  var database = safeFileName(descriptors.database.value);
  if (!handle || !name || !database || name.value !== database.value ||
    descriptors.schema.value !== "main" || descriptors.readOnly.value !== true) {
    throw new TypeError("invalid SQLite connection metadata");
  }
  return objectFreeze({ status: "connected", handle: handle, name: name.value });
}

function trueResult(value) {
  if (value !== true) throw new TypeError("invalid SQLite release result");
  return true;
}

function makeReference(connection) {
  var reference = objectFreeze({ name: connection.name, schema: "main", readOnly: true });
  references.set(reference, { handle: connection.handle, name: connection.name, closed: false, closing: null,
    editEpoch: 0, editing: objectCreate(null), committing: false });
  return reference;
}

function referenceState(reference, allowClosing) {
  var state = reference && typeof reference === "object" ? references.get(reference) : null;
  if (!state || state.closed || state.closing && !allowClosing) {
    throw new TypeError("SQLite connection reference is invalid or closed");
  }
  return state;
}

function columnSnapshot(value, position, budget) {
  var descriptors = exactObject(value, [
    "key", "position", "name", "type", "nullable", "keyLabel", "defaultValue", "primaryOrder", "generated"
  ]);
  if (!descriptors || descriptors.position.value !== position ||
    descriptors.key.value !== descriptors.name.value ||
    !safeInteger(descriptors.primaryOrder.value, 0, MAX_COLUMNS) ||
    typeof descriptors.generated.value !== "boolean") return null;
  var name = safeName(descriptors.name.value);
  var type = budgetText(budget, descriptors.type.value, MAX_NAME_BYTES, false, true);
  var nullable = descriptors.nullable.value;
  var keyLabel = descriptors.keyLabel.value;
  var defaultValue = budgetText(budget, descriptors.defaultValue.value, MAX_CELL_BYTES, true, false);
  if (!name || type === null || (nullable !== "Yes" && nullable !== "No") ||
    (keyLabel !== "" && keyLabel !== "PRIMARY KEY") || defaultValue === null) return null;
  budget.bytes += name.bytes + utf8Length(nullable, 3) + utf8Length(keyLabel, 11);
  if (budget.bytes > (budget.limit || MAX_RESULT_BYTES)) return null;
  return objectFreeze({
    key: name.value,
    position: position,
    name: name.value,
    type: type,
    nullable: nullable,
    keyLabel: keyLabel,
    defaultValue: defaultValue,
    primaryOrder: descriptors.primaryOrder.value,
    generated: descriptors.generated.value
  });
}

function columnsSnapshot(value, budget) {
  var values = arrayDescriptors(value, 1, MAX_COLUMNS);
  if (!values) return null;
  var output = new ArrayType(values.length);
  var names = objectCreate(null);
  for (var index = 0; index < values.length; index++) {
    var column = columnSnapshot(values[index], index + 1, budget);
    if (!column || hasOwn(names, column.name)) return null;
    names[column.name] = true;
    output[index] = column;
  }
  return objectFreeze(output);
}

function indexSnapshot(value, budget) {
  var descriptors = exactObject(value, ["name", "columns", "kind", "method", "unique", "partial"]);
  if (!descriptors || typeof descriptors.unique.value !== "boolean" ||
    typeof descriptors.partial.value !== "boolean") return null;
  var name = budgetText(budget, descriptors.name.value, MAX_NAME_BYTES, false, true);
  var columns = budgetText(budget, descriptors.columns.value, MAX_RESULT_BYTES, false, false);
  var kind = budgetText(budget, descriptors.kind.value, 64, false, true);
  var method = budgetText(budget, descriptors.method.value, 64, false, true);
  if (name === null || columns === null ||
    (kind !== "Index" && kind !== "Primary key" && kind !== "Unique") || method !== "btree") return null;
  return objectFreeze({
    name: name,
    columns: columns,
    kind: kind,
    method: method,
    unique: descriptors.unique.value,
    partial: descriptors.partial.value
  });
}

function indexesSnapshot(value, budget) {
  var values = arrayDescriptors(value, 0, MAX_INDEXES);
  if (!values) return null;
  var output = new ArrayType(values.length);
  var names = objectCreate(null);
  for (var index = 0; index < values.length; index++) {
    var item = indexSnapshot(values[index], budget);
    if (!item || hasOwn(names, item.name)) return null;
    names[item.name] = true;
    output[index] = item;
  }
  return objectFreeze(output);
}

function foreignKeySnapshot(value, budget) {
  var descriptors = exactObject(value, [
    "name", "columns", "target", "onUpdate", "onDelete", "targetTable", "targetColumns"
  ]);
  if (!descriptors) return null;
  var name = budgetText(budget, descriptors.name.value, 2048, false, true);
  var columns = budgetText(budget, descriptors.columns.value, MAX_RESULT_BYTES, false, false);
  var target = budgetText(budget, descriptors.target.value, MAX_RESULT_BYTES, false, false);
  var onUpdate = budgetText(budget, descriptors.onUpdate.value, 64, false, true);
  var onDelete = budgetText(budget, descriptors.onDelete.value, 64, false, true);
  var targetTable = budgetText(budget, descriptors.targetTable.value, MAX_NAME_BYTES, false, true);
  var targetColumns = budgetText(budget, descriptors.targetColumns.value, MAX_RESULT_BYTES, true, false);
  if (name === null || columns === null || target === null || onUpdate === null || onDelete === null ||
    targetTable === null || targetColumns === null) return null;
  return objectFreeze({
    name: name,
    columns: columns,
    target: target,
    onUpdate: onUpdate,
    onDelete: onDelete,
    targetTable: targetTable,
    targetColumns: targetColumns
  });
}

function foreignKeysSnapshot(value, budget) {
  var values = arrayDescriptors(value, 0, MAX_FOREIGN_KEYS);
  if (!values) return null;
  var output = new ArrayType(values.length);
  var names = objectCreate(null);
  for (var index = 0; index < values.length; index++) {
    var item = foreignKeySnapshot(values[index], budget);
    if (!item || hasOwn(names, item.name)) return null;
    names[item.name] = true;
    output[index] = item;
  }
  return objectFreeze(output);
}

function objectSnapshot(value, budget) {
  var descriptors = exactObject(value, [
    "id", "name", "type", "schema", "columns", "indexes", "foreignKeys", "withoutRowid", "strict"
  ]);
  if (!descriptors || typeof descriptors.withoutRowid.value !== "boolean" ||
    typeof descriptors.strict.value !== "boolean" || descriptors.schema.value !== "main") return null;
  var name = safeName(descriptors.name.value);
  var type = descriptors.type.value;
  var id = safeText(descriptors.id.value, MAX_NAME_BYTES + 6, false, true);
  if (!name || !id || (type !== "table" && type !== "view") || id.value !== type + "-" + name.value) return null;
  budget.bytes += name.bytes + id.bytes + type.length;
  if (budget.bytes > MAX_RESULT_BYTES) return null;
  var columns = columnsSnapshot(descriptors.columns.value, budget);
  var indexes = indexesSnapshot(descriptors.indexes.value, budget);
  var foreignKeys = foreignKeysSnapshot(descriptors.foreignKeys.value, budget);
  if (!columns || !indexes || !foreignKeys) return null;
  return objectFreeze({
    id: id.value,
    name: name.value,
    type: type,
    schema: "main",
    columns: columns,
    indexes: indexes,
    foreignKeys: foreignKeys
  });
}

function catalogResult(value, state) {
  var descriptors = resultEnvelope(value, ["catalogVersion", "objects"], CATALOG_FORMAT, state);
  if (!descriptors || !safeInteger(descriptors.catalogVersion.value, 0, 2147483647)) {
    throw new TypeError("invalid SQLite catalog result");
  }
  var values = arrayDescriptors(descriptors.objects.value, 0, MAX_OBJECTS);
  if (!values) throw new TypeError("invalid SQLite catalog objects");
  var budget = { bytes: 0 };
  var output = new ArrayType(values.length);
  var ids = objectCreate(null);
  var names = objectCreate(null);
  for (var index = 0; index < values.length; index++) {
    var item = objectSnapshot(values[index], budget);
    if (!item || hasOwn(ids, item.id) || hasOwn(names, item.name)) {
      throw new TypeError("invalid SQLite catalog object");
    }
    ids[item.id] = true;
    names[item.name] = true;
    output[index] = item;
  }
  return objectFreeze({
    catalogVersion: descriptors.catalogVersion.value,
    objects: objectFreeze(output)
  });
}

function plainPageOptions(value) {
  var descriptors = exactObject(value, ["type", "name", "catalogVersion", "page", "pageSize"]);
  if (!descriptors) throw new TypeError("SQLite table page options must contain exactly type, name, catalogVersion, page, and pageSize");
  var type = descriptors.type.value;
  var name = safeName(descriptors.name.value);
  var catalogVersion = descriptors.catalogVersion.value;
  var page = descriptors.page.value;
  var pageSize = descriptors.pageSize.value;
  if ((type !== "table" && type !== "view") || !name ||
    !safeInteger(catalogVersion, 0, 2147483647) || !safeInteger(page, 1, MAX_PAGE) ||
    !safeInteger(pageSize, 1, MAX_ROWS) || (page - 1) * pageSize > MAX_OFFSET) {
    throw new TypeError("SQLite table page options are invalid");
  }
  return objectFreeze({
    type: type,
    name: name.value,
    catalogVersion: catalogVersion,
    page: page,
    pageSize: pageSize
  });
}

function queryColumnSnapshot(value, budget) {
  var descriptors = exactObject(value, ["name", "type"]);
  if (!descriptors) return null;
  var name = budgetText(budget, descriptors.name.value, MAX_NAME_BYTES, true, false);
  var type = budgetText(budget, descriptors.type.value, MAX_NAME_BYTES, false, true);
  return name === null || type === null ? null : objectFreeze({ name: name, type: type });
}

function queryColumnsSnapshot(value, budget) {
  var values = arrayDescriptors(value, 1, MAX_COLUMNS);
  if (!values) return null;
  var output = new ArrayType(values.length);
  for (var index = 0; index < values.length; index++) {
    var column = queryColumnSnapshot(values[index], budget);
    if (!column) return null;
    output[index] = column;
  }
  return objectFreeze(output);
}

function canonicalBlob(value, budget) {
  var descriptors = exactObject(value, ["type", "base64", "bytes"]);
  if (!descriptors || descriptors.type.value !== "blob" ||
    typeof descriptors.base64.value !== "string" ||
    !safeInteger(descriptors.bytes.value, 0, MAX_CELL_BYTES)) return null;
  var encoded = descriptors.base64.value;
  var bytes = descriptors.bytes.value;
  if (encoded.length !== mathCeil(bytes / 3) * 4 || !regExpTest(BASE64_VALUE, encoded)) return null;
  var remainder = bytes % 3;
  if (remainder === 0 && stringIndexOf(encoded, "=") >= 0) return null;
  if (remainder === 1) {
    if (stringSlice(encoded, -2) !== "==" || stringIndexOf(BASE64, encoded[encoded.length - 3]) < 0 ||
      (stringIndexOf(BASE64, encoded[encoded.length - 3]) & 15) !== 0) return null;
  }
  if (remainder === 2) {
    if (stringSlice(encoded, -1) !== "=" || stringIndexOf(BASE64, encoded[encoded.length - 2]) < 0 ||
      (stringIndexOf(BASE64, encoded[encoded.length - 2]) & 3) !== 0) return null;
  }
  budget.bytes += encoded.length + 32;
  if (budget.bytes > (budget.limit || MAX_RESULT_BYTES)) return null;
  return objectFreeze({ type: "blob", base64: encoded, bytes: bytes });
}

function cellSnapshot(value, budget) {
  if (value === null) {
    budget.bytes += 4;
    return budget.bytes <= (budget.limit || MAX_RESULT_BYTES) ? null : undefined;
  }
  if (typeof value === "boolean") {
    budget.bytes++;
    return budget.bytes <= (budget.limit || MAX_RESULT_BYTES) ? value : undefined;
  }
  if (typeof value === "number") {
    if (!numberIsFinite(value) || mathFloor(value) === value && !numberIsSafeInteger(value)) return undefined;
    budget.bytes += 8;
    return budget.bytes <= (budget.limit || MAX_RESULT_BYTES) ? value : undefined;
  }
  if (typeof value === "string") {
    var checked = safeText(value, MAX_CELL_BYTES, true, false);
    if (!checked) return undefined;
    budget.bytes += checked.bytes;
    return budget.bytes <= (budget.limit || MAX_RESULT_BYTES) ? value : undefined;
  }
  if (!value || typeof value !== "object") return undefined;
  return canonicalBlob(value, budget) || undefined;
}

function rowsSnapshot(value, columns, maximum, budget) {
  var rows = arrayDescriptors(value, 0, maximum);
  if (!rows) return null;
  var output = new ArrayType(rows.length);
  for (var rowIndex = 0; rowIndex < rows.length; rowIndex++) {
    var cells = arrayDescriptors(rows[rowIndex], columns.length, columns.length);
    if (!cells) return null;
    var row = new ArrayType(cells.length);
    for (var cellIndex = 0; cellIndex < cells.length; cellIndex++) {
      var cell = cellSnapshot(cells[cellIndex], budget);
      if (cell === undefined) return null;
      row[cellIndex] = cell;
    }
    output[rowIndex] = objectFreeze(row);
  }
  return objectFreeze(output);
}

function editingSnapshot(value, columns, type, budget) {
  var descriptors = exactObject(value, ["supported", "keyColumns", "reason"]);
  if (!descriptors || typeof descriptors.supported.value !== "boolean") return null;
  var reason = budgetText(budget, descriptors.reason.value, MAX_NAME_BYTES, true, true);
  var keys = arrayDescriptors(descriptors.keyColumns.value, 0, MAX_COLUMNS);
  if (reason === null || !keys || descriptors.supported.value && (type !== "table" || !keys.length)) return null;
  var names = objectCreate(null);
  for (var index = 0; index < columns.length; index++) names[columns[index].name] = true;
  var seen = objectCreate(null);
  for (var keyIndex = 0; keyIndex < keys.length; keyIndex++) {
    var key = safeName(keys[keyIndex]);
    if (!key || !hasOwn(names, key.value) || hasOwn(seen, key.value)) return null;
    seen[key.value] = true;
    keys[keyIndex] = key.value;
    budget.bytes += key.bytes;
  }
  if (budget.bytes > MAX_RESULT_BYTES) return null;
  return objectFreeze({ supported: descriptors.supported.value, keyColumns: objectFreeze(keys), reason: reason });
}

function changeRecord(value, columns, minimum, original, budget) {
  var names;
  try { names = objectKeys(value); } catch (_) { return null; }
  if (names.length < minimum || names.length > MAX_COLUMNS || original && names.length !== columns.length) return null;
  var descriptors = exactObject(value, names);
  if (!descriptors) return null;
  var allowed = objectCreate(null);
  for (var index = 0; index < columns.length; index++) allowed[columns[index].name] = columns[index];
  var output = objectCreate(null);
  for (var nameIndex = 0; nameIndex < names.length; nameIndex++) {
    var name = safeName(names[nameIndex]);
    if (!name || name.value === "toJSON" || !hasOwn(allowed, name.value) || !original && allowed[name.value].generated) return null;
    budget.bytes += name.bytes;
    var cell = cellSnapshot(descriptors[name.value].value, budget);
    if (cell === undefined) return null;
    output[name.value] = cell;
  }
  return objectFreeze(output);
}

function commitOptions(value, state) {
  var descriptors = exactObject(value, ["name", "catalogVersion", "changes"]);
  if (!descriptors || !safeName(descriptors.name.value) || !safeInteger(descriptors.catalogVersion.value, 0, 2147483647)) {
    throw new TypeError("SQLite commit expects name, catalogVersion, and changes");
  }
  var name = descriptors.name.value;
  var metadata = state.editing[name];
  if (!metadata || metadata.catalogVersion !== descriptors.catalogVersion.value) throw studioError("SCHEMA_CHANGED", "commit");
  if (!metadata.editing.supported) throw studioError("READ_ONLY_TABLE", "commit");
  var changes = arrayDescriptors(descriptors.changes.value, 1, MAX_CHANGES);
  if (!changes) throw new TypeError("SQLite commit requires 1 to 100 pending changes");
  var budget = { bytes: 0 };
  for (var index = 0; index < changes.length; index++) {
    var change = exactObject(changes[index], ["kind", "original", "values"]);
    if (!change) change = exactObject(changes[index], ["kind", "values"]);
    if (!change) change = exactObject(changes[index], ["kind", "original"]);
    if (!change) throw new TypeError("SQLite change has invalid fields");
    var kind = change.kind.value;
    if (kind !== "insert" && kind !== "update" && kind !== "delete" ||
      kind !== "insert" && !change.original || kind !== "delete" && !change.values) {
      throw new TypeError("SQLite change requires a kind, original row, and changed values");
    }
    var output = { kind: kind };
    if (change.original) {
      output.original = changeRecord(change.original.value, metadata.columns, kind === "insert" ? 0 : 1, kind !== "insert", budget);
      if (!output.original || kind === "insert" && objectKeys(output.original).length) throw new TypeError("SQLite original row is invalid");
    }
    if (change.values) {
      output.values = changeRecord(change.values.value, metadata.columns, kind === "update" ? 1 : 0, false, budget);
      if (!output.values || kind === "delete" && objectKeys(output.values).length) throw new TypeError("SQLite changed values are invalid");
    }
    changes[index] = objectFreeze(output);
  }
  var outputOptions = objectFreeze({ name: name, catalogVersion: descriptors.catalogVersion.value, changes: objectFreeze(changes) });
  if (objectGetOwnPropertyDescriptor(objectPrototype, "toJSON") || objectGetOwnPropertyDescriptor(arrayPrototype, "toJSON")) {
    throw new TypeError("SQLite changes require unmodified JSON serialization");
  }
  var encoded = jsonStringify(outputOptions);
  if (utf8Length(encoded, MAX_RESULT_BYTES) > MAX_RESULT_BYTES) throw new TypeError("SQLite changes exceed the 2 MiB batch limit");
  return outputOptions;
}

function statementsSnapshot(value, budget) {
  var statements = arrayDescriptors(value, 0, MAX_STATEMENTS);
  if (!statements) return null;
  for (var index = 0; index < statements.length; index++) {
    var descriptors = exactObject(statements[index], ["sql", "parameters", "durationMs", "affectedRows"]);
    if (!descriptors) descriptors = exactObject(statements[index], ["sql", "parameters", "durationMs", "affectedRows", "rowCount"]);
    if (!descriptors) descriptors = exactObject(statements[index], ["sql", "parameters", "durationMs", "affectedRows", "failed"]);
    if (!descriptors) descriptors = exactObject(statements[index], ["sql", "parameters", "durationMs", "affectedRows", "rowCount", "failed"]);
    if (!descriptors || !safeInteger(descriptors.durationMs.value, 0, MAX_DURATION_MS) ||
      !safeInteger(descriptors.affectedRows.value, 0, MAX_SAFE_INTEGER) ||
      descriptors.rowCount && !safeInteger(descriptors.rowCount.value, 0, MAX_SAFE_INTEGER) ||
      descriptors.failed && typeof descriptors.failed.value !== "boolean") return null;
    var sql = budgetText(budget, descriptors.sql.value, 512 * 1024, false, false);
    var parameters = arrayDescriptors(descriptors.parameters.value, 0, MAX_COLUMNS * 2);
    if (sql === null || !parameters) return null;
    for (var parameterIndex = 0; parameterIndex < parameters.length; parameterIndex++) {
      var cell = cellSnapshot(parameters[parameterIndex], budget);
      if (cell === undefined) return null;
      parameters[parameterIndex] = cell;
    }
    var statement = { sql: sql, parameters: objectFreeze(parameters), durationMs: descriptors.durationMs.value,
      affectedRows: descriptors.affectedRows.value };
    if (descriptors.rowCount) statement.rowCount = descriptors.rowCount.value;
    if (descriptors.failed) statement.failed = descriptors.failed.value;
    statements[index] = objectFreeze(statement);
  }
  return objectFreeze(statements);
}

function commitResult(value, expected, state) {
  var failed = exactObject(value, ["format", "version", "ok", "source", "code", "message", "details"]);
  var budget = { bytes: 0, limit: 8 * 1024 * 1024 };
  if (failed && failed.format.value === COMMIT_FORMAT && failed.version.value === 1 &&
    failed.ok.value === false && failed.source.value === "sqlite") {
    var details = exactObject(failed.details.value, ["name", "catalogVersion", "statements", "failedChange", "rolledBack"]);
    if (!details || details.name.value !== expected.name || details.catalogVersion.value !== expected.catalogVersion ||
      !safeInteger(details.failedChange.value, -1, expected.changes.length - 1) || typeof details.rolledBack.value !== "boolean" ||
      !safeText(failed.message.value, MAX_NAME_BYTES, false, false)) throw new TypeError("invalid SQLite commit failure");
    var attempts = statementsSnapshot(details.statements.value, budget);
    if (!attempts) throw new TypeError("invalid SQLite attempted statements");
    throw studioError(failed.code.value, "commit", objectFreeze({ name: expected.name, catalogVersion: expected.catalogVersion,
      statements: attempts, failedChange: details.failedChange.value, rolledBack: details.rolledBack.value }));
  }
  var descriptors = exactObject(value, ["format", "version", "ok", "source", "database", "schema", "readOnly",
    "name", "catalogVersion", "affectedRows", "durationMs", "statements"]);
  if (!descriptors || descriptors.format.value !== COMMIT_FORMAT || descriptors.version.value !== 1 ||
    descriptors.ok.value !== true || descriptors.source.value !== "sqlite" || descriptors.database.value !== state.name ||
    descriptors.schema.value !== "main" || descriptors.readOnly.value !== false || descriptors.name.value !== expected.name ||
    descriptors.catalogVersion.value !== expected.catalogVersion ||
    descriptors.affectedRows.value !== expected.changes.length ||
    !safeInteger(descriptors.durationMs.value, 0, MAX_DURATION_MS)) throw new TypeError("invalid SQLite commit result");
  var statements = statementsSnapshot(descriptors.statements.value, budget);
  if (!statements || statements.length === 0) throw new TypeError("invalid SQLite commit statements");
  return objectFreeze({ name: expected.name, catalogVersion: expected.catalogVersion,
    affectedRows: descriptors.affectedRows.value, durationMs: descriptors.durationMs.value, statements: statements });
}

function tablePageResult(value, expected, state) {
  var descriptors = resultEnvelope(value, [
    "catalogVersion", "name", "type", "columns", "rows", "totalRows", "page", "pageSize",
    "pageCount", "pageStart", "pageEnd", "hasPreviousPage", "hasNextPage", "durationMs", "editing"
  ], TABLE_FORMAT, state);
  if (!descriptors || descriptors.catalogVersion.value !== expected.catalogVersion || descriptors.name.value !== expected.name ||
    descriptors.type.value !== expected.type || descriptors.page.value !== expected.page ||
    descriptors.pageSize.value !== expected.pageSize ||
    !safeInteger(descriptors.totalRows.value, 0, MAX_SAFE_INTEGER) ||
    !safeInteger(descriptors.pageCount.value, 0, MAX_SAFE_INTEGER) ||
    !safeInteger(descriptors.pageStart.value, 0, MAX_SAFE_INTEGER) ||
    !safeInteger(descriptors.pageEnd.value, 0, MAX_SAFE_INTEGER) ||
    typeof descriptors.hasPreviousPage.value !== "boolean" || typeof descriptors.hasNextPage.value !== "boolean" ||
    !safeInteger(descriptors.durationMs.value, 0, MAX_DURATION_MS)) {
    throw new TypeError("invalid SQLite table page result");
  }
  var budget = { bytes: 0 };
  var columns = columnsSnapshot(descriptors.columns.value, budget);
  var rows = columns && rowsSnapshot(descriptors.rows.value, columns, expected.pageSize, budget);
  if (!columns || !rows) throw new TypeError("invalid SQLite table page data");
  var editing = editingSnapshot(descriptors.editing.value, columns, expected.type, budget);
  if (!editing) throw new TypeError("invalid SQLite table editing metadata");
  var totalRows = descriptors.totalRows.value;
  var pageCount = descriptors.pageCount.value;
  var expectedPageCount = totalRows === 0 ? 0 : mathCeil(totalRows / expected.pageSize);
  var offset = (expected.page - 1) * expected.pageSize;
  var expectedStart = rows.length ? offset + 1 : 0;
  var expectedEnd = rows.length ? offset + rows.length : 0;
  if (pageCount !== expectedPageCount || descriptors.pageStart.value !== expectedStart ||
    descriptors.pageEnd.value !== expectedEnd || expectedEnd > totalRows ||
    descriptors.hasPreviousPage.value !== (pageCount > 0 && expected.page > 1) ||
    descriptors.hasNextPage.value !== (expected.page < pageCount)) {
    throw new TypeError("inconsistent SQLite table page result");
  }
  return objectFreeze({
    catalogVersion: expected.catalogVersion,
    name: expected.name,
    type: expected.type,
    columns: columns,
    editing: editing,
    rows: rows,
    totalRows: totalRows,
    page: expected.page,
    pageSize: expected.pageSize,
    pageCount: pageCount,
    pageStart: expectedStart,
    pageEnd: expectedEnd,
    hasPreviousPage: descriptors.hasPreviousPage.value,
    hasNextPage: descriptors.hasNextPage.value,
    durationMs: descriptors.durationMs.value
  });
}

function queryResult(value, state) {
  var descriptors = resultEnvelope(value, [
    "columns", "rows", "rowCount", "returnedRows", "truncated", "durationMs"
  ], QUERY_FORMAT, state);
  if (!descriptors || typeof descriptors.truncated.value !== "boolean" ||
    !safeInteger(descriptors.durationMs.value, 0, MAX_DURATION_MS)) {
    throw new TypeError("invalid SQLite query result");
  }
  var budget = { bytes: 0 };
  var columns = queryColumnsSnapshot(descriptors.columns.value, budget);
  var rows = columns && rowsSnapshot(descriptors.rows.value, columns, MAX_ROWS, budget);
  if (!columns || !rows || descriptors.rowCount.value !== rows.length ||
    descriptors.returnedRows.value !== rows.length) throw new TypeError("invalid SQLite query data");
  return objectFreeze({
    columns: columns,
    rows: rows,
    rowCount: rows.length,
    returnedRows: rows.length,
    truncated: descriptors.truncated.value,
    durationMs: descriptors.durationMs.value
  });
}

function bestEffortCloseConnection(connection) {
  if (!connection || connection.status !== "connected") return;
  nativeCall("close", { handle: connection.handle }, "close", trueResult).then(
    function () {},
    function (error) { report(error); }
  );
}

function open() {
  if (arguments.length !== 0) throw new TypeError("studioSqlite.open does not accept parameters");
  if (!nativeHost || !setTimer || !clearTimer) {
    return promiseReject(studioError("UNAVAILABLE", "open"));
  }
  if (activeOpen) return promiseReject(studioError("BUSY", "open"));

  var state = {
    polls: 0,
    pollTimer: null,
    deadlineTimer: null,
    terminal: false,
    publicSettled: false,
    beginPending: false,
    releasePending: null,
    resolve: null,
    reject: null
  };
  activeOpen = state;
  var result = new PromiseType(function (resolve, reject) {
    state.resolve = resolve;
    state.reject = reject;
  });

  function publish(ok, value) {
    if (state.publicSettled) return;
    state.publicSettled = true;
    if (ok) state.resolve(value);
    else state.reject(value);
  }

  function clearClocks() {
    if (state.pollTimer !== null) {
      clearTimer(state.pollTimer);
      state.pollTimer = null;
    }
    if (state.deadlineTimer !== null) {
      clearTimer(state.deadlineTimer);
      state.deadlineTimer = null;
    }
  }

  function complete() {
    clearClocks();
    operations.delete(state);
    if (activeOpen === state) activeOpen = null;
  }

  function releaseOperation() {
    var operation = operations.get(state);
    if (!operation) return promiseResolve(undefined);
    if (state.releasePending) return state.releasePending;
    state.releasePending = nativeCall("releaseOpen", { operation: operation }, "open", trueResult).then(
      function () { return undefined; },
      function (error) { report(error); return undefined; }
    );
    return state.releasePending;
  }

  function finish(ok, value, prompt) {
    if (state.terminal) return;
    state.terminal = true;
    clearClocks();
    if (prompt) publish(ok, value);
    if (state.beginPending && !operations.has(state)) return;
    releaseOperation().then(function () {
      if (!prompt) publish(ok, value);
      complete();
    });
  }

  function schedulePoll() {
    if (state.terminal) return;
    if (state.polls >= OPEN_MAX_POLLS) {
      finish(false, studioError("TIMEOUT", "open"), true);
      return;
    }
    try {
      state.pollTimer = setTimer(function () {
        state.pollTimer = null;
        poll();
      }, POLL_DELAY_MS);
    } catch (_) {
      finish(false, studioError("UNAVAILABLE", "open"), true);
    }
  }

  function poll() {
    if (state.terminal) return;
    state.polls++;
    nativeCall("pollOpen", { operation: operations.get(state) }, "open", pollResult).then(function (value) {
      if (state.terminal) {
        bestEffortCloseConnection(value);
        return;
      }
      if (value.status === "pending") {
        schedulePoll();
        return;
      }
      if (value.status === "cancelled") {
        finish(true, null, false);
        return;
      }
      if (value.status === "failed") {
        finish(false, studioError(value.code, "open"), false);
        return;
      }
      finish(true, makeReference(value), false);
    }, function (error) {
      if (!state.terminal) finish(false, error, false);
    });
  }

  try {
    state.deadlineTimer = setTimer(function () {
      state.deadlineTimer = null;
      finish(false, studioError("TIMEOUT", "open"), true);
    }, OPEN_TIMEOUT_MS);
  } catch (_) {
    finish(false, studioError("UNAVAILABLE", "open"), true);
    return result;
  }

  state.beginPending = true;
  nativeCall("beginOpen", {}, "open", beginResult).then(function (operation) {
    state.beginPending = false;
    operations.set(state, operation);
    if (state.terminal) {
      releaseOperation().then(complete);
      return;
    }
    poll();
  }, function (error) {
    state.beginPending = false;
    if (!state.terminal) publish(false, error);
    complete();
  });

  return result;
}

function catalog(reference) {
  if (arguments.length !== 1) throw new TypeError("studioSqlite.catalog expects one ConnectionRef");
  var state = referenceState(reference, false);
  return nativeCall("catalog", { handle: state.handle }, "catalog", function (value) {
    return catalogResult(value, state);
  });
}

function tablePage(reference, input) {
  if (arguments.length !== 2) throw new TypeError("studioSqlite.tablePage expects a ConnectionRef and options");
  var state = referenceState(reference, false);
  var options = plainPageOptions(input);
  var epoch = state.editEpoch;
  return nativeCall("tablePage", {
    handle: state.handle,
    type: options.type,
    name: options.name,
    catalogVersion: options.catalogVersion,
    page: options.page,
    pageSize: options.pageSize,
    includeEditing: true
  }, "tablePage", function (value) {
    var result = tablePageResult(value, options, state);
    if (!state.closed && !state.closing && !state.committing && state.editEpoch === epoch) {
      if (objectKeys(state.editing).length >= MAX_OBJECTS && !hasOwn(state.editing, options.name)) state.editing = objectCreate(null);
      state.editing[options.name] = objectFreeze({ catalogVersion: result.catalogVersion,
        columns: result.columns, editing: result.editing });
    }
    return result;
  });
}

function commit(reference, input) {
  if (arguments.length !== 2) throw new TypeError("studioSqlite.commit expects a ConnectionRef and options");
  var state = referenceState(reference, false);
  if (state.committing) return promiseReject(studioError("BUSY", "commit"));
  var options = commitOptions(input, state);
  state.committing = true;
  state.editEpoch++;
  return nativeCall("commit", { handle: state.handle, name: options.name, catalogVersion: options.catalogVersion,
    changes: options.changes }, "commit", function (value) { return commitResult(value, options, state); }).then(function (result) {
      state.committing = false;
      state.editEpoch++;
      delete state.editing[options.name];
      return result;
    }, function (error) {
      state.committing = false;
      state.editEpoch++;
      if (error.code === "SCHEMA_CHANGED") state.editing = objectCreate(null);
      throw error;
    });
}

function query(reference, sql) {
  if (arguments.length !== 2) throw new TypeError("studioSqlite.query expects a ConnectionRef and SQL text");
  var state = referenceState(reference, false);
  var checked = safeText(sql, MAX_SQL_BYTES, false, false);
  if (!checked || stringTrim(sql) === "") throw new TypeError("SQLite query must be non-empty text up to 32 KiB");
  return nativeCall("query", { handle: state.handle, sql: sql }, "query", function (value) {
    return queryResult(value, state);
  });
}

function close(reference) {
  if (arguments.length !== 1) throw new TypeError("studioSqlite.close expects one ConnectionRef");
  var state = reference && typeof reference === "object" ? references.get(reference) : null;
  if (!state) throw new TypeError("SQLite connection reference is invalid");
  if (state.committing) return promiseReject(studioError("BUSY", "close"));
  if (state.closed) return promiseResolve(true);
  if (state.closing) return state.closing;
  var handle = state.handle;
  state.closing = nativeCall("close", { handle: handle }, "close", trueResult).then(function () {
    state.closed = true;
    state.closing = null;
    state.handle = null;
    state.editEpoch++;
    state.editing = objectCreate(null);
    return true;
  }, function (error) {
    state.closing = null;
    throw error;
  });
  return state.closing;
}

kit.service("studioSqlite", {
  open: open,
  catalog: catalog,
  tablePage: tablePage,
  query: query,
  commit: commit,
  close: close
});
})(globalThis, kit);
