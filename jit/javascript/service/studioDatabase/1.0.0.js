;(function (global, kit) {
"use strict";

// KitJS staged service: studioDatabase@1.0.0
// The desktop host owns PostgreSQL/MySQL sockets and every connection. This
// boundary exposes only immutable metadata references and bounded read APIs;
// connection handles and credentials stay private to trusted code.
var TOKEN = /^[A-Za-z0-9_-]{32}$/;
var OBJECT_ID = /^(?:table|view)-[A-Za-z0-9_-]{1,1368}-[A-Za-z0-9_-]{1,1368}$/;
var BASE64_VALUE = /^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/;
var BASE64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
var CATALOG_FORMAT = "kitwork-studio-native-database-catalog";
var TABLE_FORMAT = "kitwork-studio-native-database-table";
var QUERY_FORMAT = "kitwork-studio-native-database-query";
var MAX_NAME_BYTES = 1024;
var MAX_HOST_BYTES = 255;
var MAX_IDENTITY_BYTES = 128;
var MAX_PASSWORD_BYTES = 4096;
var MAX_PROFILE_ID_BYTES = 128;
var MAX_SQL_BYTES = 32 * 1024;
var MAX_OBJECTS = 256;
var MAX_DATABASES = 64;
var MAX_SCHEMAS = 64;
var MAX_COLUMNS = 128;
var MAX_INDEXES = 128;
var MAX_FOREIGN_KEYS = 128;
var MAX_ROWS = 120;
var MAX_PAGE = 1000000;
var MAX_OFFSET = 1000000;
var MAX_CELL_BYTES = 256 * 1024;
var MAX_RESULT_BYTES = 2 * 1024 * 1024;
var MAX_DURATION_MS = 60000;
var objectPrototype = Object.prototype;
var arrayPrototype = Array.prototype;
var objectCreate = Object.create;
var objectDefineProperties = Object.defineProperties;
var objectFreeze = Object.freeze;
var objectGetOwnPropertyDescriptors = Object.getOwnPropertyDescriptors;
var objectGetOwnPropertySymbols = Object.getOwnPropertySymbols;
var objectGetPrototypeOf = Object.getPrototypeOf;
var objectKeys = Object.keys;
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
var arrayIndexOf = call.bind(arrayPrototype.indexOf);
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
var assembly = global.document && global.document[Symbol.for("kitjs:assembly")];
var nativeHost = assembly && assembly.nativeHost;
var references = new WeakMap();
var privateErrors = new WeakSet();

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
    code === "DATABASE_UNAVAILABLE") return "UNAVAILABLE";
  if (code === "OPERATION_NOT_FOUND" || code === "HANDLE_NOT_FOUND" ||
    code === "OBJECT_NOT_FOUND") return "NOT_FOUND";
  if (code === "LIMIT_REACHED" || code === "CATALOG_TOO_LARGE" ||
    code === "RESULT_TOO_LARGE" || code === "QUERY_TOO_LARGE") return "LIMIT";
  if (code === "UNSTABLE_ORDER") return "UNSUPPORTED";
  if (code === "INVALID_QUERY") return "INVALID_REQUEST";
  if (code === "DENIED" || code === "CANCELLED" || code === "UNAVAILABLE" ||
    code === "TIMEOUT" || code === "BUSY" || code === "NOT_FOUND" || code === "INVALID_REQUEST" ||
    code === "INVALID_DATABASE" || code === "DATABASE_CORRUPT" ||
    code === "AUTHENTICATION_FAILED" || code === "TLS_ERROR" || code === "CONNECTION_FAILED" ||
    code === "READ_ONLY_REQUIRED" || code === "SQL_ERROR" || code === "SCHEMA_CHANGED" ||
    code === "LIMIT" || code === "UNSUPPORTED" || code === "FAILED") return code;
  return "FAILED";
}

function errorCode(value) {
  var code = "";
  try { code = value && typeof value.code === "string" ? value.code : ""; }
  catch (_) { /* Raw host failures never cross this service boundary. */ }
  return canonicalErrorCode(code);
}

function studioError(code, operation) {
  code = canonicalErrorCode(code);
  var messages = {
    DENIED: "Database access was denied",
    CANCELLED: "Database operation was cancelled",
    UNAVAILABLE: "Desktop database access is unavailable",
    TIMEOUT: "Database operation timed out",
    BUSY: "Database service is busy",
    NOT_FOUND: "Database operation or connection was not found",
    INVALID_REQUEST: "Database request is invalid",
    INVALID_DATABASE: "Database name is invalid",
    READ_ONLY_REQUIRED: "Only a read-only query is allowed",
    DATABASE_CORRUPT: "Database response is invalid",
    AUTHENTICATION_FAILED: "Database authentication failed",
    TLS_ERROR: "Database TLS connection failed",
    CONNECTION_FAILED: "Database connection failed",
    SQL_ERROR: "Database could not execute the query",
    LIMIT: "Database operation exceeds the supported limit",
    SCHEMA_CHANGED: "Database schema changed",
    UNSUPPORTED: "Database operation is not supported",
    FAILED: "Database operation failed"
  };
  var error = new ErrorType(messages[code] || messages.FAILED);
  objectDefineProperties(error, {
    name: { value: "KitStudioDatabaseError" },
    code: { value: code, enumerable: true },
    operation: { value: operation, enumerable: true }
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
    return promiseResolve(nativeHost.call("studioDatabase." + action, objectFreeze(params))).then(
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

function budgetText(budget, value, maximum, allowEmpty, rejectControls) {
  var checked = safeText(value, maximum, allowEmpty, rejectControls);
  if (!checked) return null;
  budget.bytes += checked.bytes;
  return budget.bytes <= MAX_RESULT_BYTES ? checked.value : null;
}

function safeInteger(value, minimum, maximum) {
  return numberIsSafeInteger(value) && value >= minimum && value <= maximum;
}

function safeToken(value) {
  return typeof value === "string" && regExpTest(TOKEN, value) ? value : null;
}

function resultEnvelope(value, fields, format, state, expectedSchema) {
  var keys = arrayConcat(["format", "version", "ok", "source", "database", "schema", "readOnly"], fields);
  var descriptors = exactObject(value, keys);
  if (!descriptors || descriptors.format.value !== format || descriptors.version.value !== 1 ||
    descriptors.ok.value !== true || descriptors.source.value !== state.driver ||
    descriptors.database.value !== state.database || descriptors.schema.value !== (expectedSchema || state.schema) ||
    descriptors.readOnly.value !== true) return null;
  return descriptors;
}

function trueResult(value) {
  if (value !== true) throw new TypeError("invalid database close result");
  return true;
}

function makeReference(connection) {
  var publicState = { driver: connection.driver, database: connection.database, schema: connection.schema, readOnly: true };
  if (connection.credentialAvailable !== null) {
    publicState.credentialAvailable = connection.credentialAvailable;
    publicState.remembered = connection.remembered;
  }
  var reference = objectFreeze(publicState);
  references.set(reference, {
    handle: connection.handle,
    driver: connection.driver,
    database: connection.database,
    schema: connection.schema,
    closed: false,
    closing: null
  });
  return reference;
}

function referenceState(reference, allowClosing) {
  var state = reference && typeof reference === "object" ? references.get(reference) : null;
  if (!state || state.closed || state.closing && !allowClosing) {
    throw new TypeError("Database connection reference is invalid or closed");
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
  if (budget.bytes > MAX_RESULT_BYTES) return null;
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
    (kind !== "Index" && kind !== "Primary key" && kind !== "Unique") || method === null) return null;
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

function objectSnapshot(value, budget, schemas) {
  var descriptors = exactObject(value, [
    "id", "name", "type", "schema", "columns", "indexes", "foreignKeys"
  ]);
  if (!descriptors) return null;
  var name = safeName(descriptors.name.value);
  var schema = safeName(descriptors.schema.value);
  var type = descriptors.type.value;
  var id = safeText(descriptors.id.value, 2750, false, true);
  if (!name || !schema || !id || (type !== "table" && type !== "view") ||
    !regExpTest(OBJECT_ID, id.value) || arrayIndexOf(schemas, schema.value) < 0) return null;
  budget.bytes += name.bytes + schema.bytes + id.bytes + type.length;
  if (budget.bytes > MAX_RESULT_BYTES) return null;
  var columns = columnsSnapshot(descriptors.columns.value, budget);
  var indexes = indexesSnapshot(descriptors.indexes.value, budget);
  var foreignKeys = foreignKeysSnapshot(descriptors.foreignKeys.value, budget);
  if (!columns || !indexes || !foreignKeys) return null;
  return objectFreeze({
    id: id.value,
    name: name.value,
    type: type,
    schema: schema.value,
    columns: columns,
    indexes: indexes,
    foreignKeys: foreignKeys
  });
}

function catalogResult(value, state) {
  var descriptors = resultEnvelope(value, ["catalogVersion", "databases", "schemas", "objects"], CATALOG_FORMAT, state);
  if (!descriptors || !safeInteger(descriptors.catalogVersion.value, 1, 2147483647)) {
    throw new TypeError("invalid database catalog result");
  }
  var databaseValues = arrayDescriptors(descriptors.databases.value, 1, MAX_DATABASES);
  var schemaValues = arrayDescriptors(descriptors.schemas.value, 1, MAX_SCHEMAS);
  var values = arrayDescriptors(descriptors.objects.value, 0, MAX_OBJECTS);
  if (!databaseValues || !schemaValues || !values) throw new TypeError("invalid database catalog objects");
  var budget = { bytes: 0 };
  var databases = new ArrayType(databaseValues.length);
  var seenDatabases = objectCreate(null);
  for (var databaseIndex = 0; databaseIndex < databaseValues.length; databaseIndex++) {
    var checkedDatabase = safeText(databaseValues[databaseIndex], MAX_IDENTITY_BYTES, false, true);
    if (!checkedDatabase || hasOwn(seenDatabases, checkedDatabase.value)) throw new TypeError("invalid database catalog database");
    seenDatabases[checkedDatabase.value] = true;
    budget.bytes += checkedDatabase.bytes;
    if (budget.bytes > MAX_RESULT_BYTES) throw new TypeError("invalid database catalog database");
    databases[databaseIndex] = checkedDatabase.value;
  }
  if (!hasOwn(seenDatabases, state.database)) throw new TypeError("database catalog omitted the connected database");
  var schemas = new ArrayType(schemaValues.length);
  var seenSchemas = objectCreate(null);
  for (var schemaIndex = 0; schemaIndex < schemaValues.length; schemaIndex++) {
    var checkedSchema = safeName(schemaValues[schemaIndex]);
    if (!checkedSchema || hasOwn(seenSchemas, checkedSchema.value)) throw new TypeError("invalid database catalog schema");
    seenSchemas[checkedSchema.value] = true;
    budget.bytes += checkedSchema.bytes;
    if (budget.bytes > MAX_RESULT_BYTES) throw new TypeError("invalid database catalog schema");
    schemas[schemaIndex] = checkedSchema.value;
  }
  if (!hasOwn(seenSchemas, state.schema)) throw new TypeError("database catalog omitted the default schema");
  var output = new ArrayType(values.length);
  var ids = objectCreate(null);
  var names = objectCreate(null);
  for (var index = 0; index < values.length; index++) {
    var item = objectSnapshot(values[index], budget, schemas);
    var qualifiedName = item && item.schema + "\u0000" + item.name;
    if (!item || hasOwn(ids, item.id) || hasOwn(names, qualifiedName)) {
      throw new TypeError("invalid database catalog object");
    }
    ids[item.id] = true;
    names[qualifiedName] = true;
    output[index] = item;
  }
  return objectFreeze({
    catalogVersion: descriptors.catalogVersion.value,
    databases: objectFreeze(databases),
    schemas: objectFreeze(schemas),
    objects: objectFreeze(output)
  });
}

function plainPageOptions(value) {
  var descriptors = exactObject(value, ["schema", "type", "name", "catalogVersion", "page", "pageSize"]);
  if (!descriptors) throw new TypeError("Database table page options must contain exactly schema, type, name, catalogVersion, page, and pageSize");
  var schema = safeName(descriptors.schema.value);
  var type = descriptors.type.value;
  var name = safeName(descriptors.name.value);
  var catalogVersion = descriptors.catalogVersion.value;
  var page = descriptors.page.value;
  var pageSize = descriptors.pageSize.value;
  if (!schema || (type !== "table" && type !== "view") || !name ||
    !safeInteger(catalogVersion, 1, 2147483647) || !safeInteger(page, 1, MAX_PAGE) ||
    !safeInteger(pageSize, 1, MAX_ROWS) || (page - 1) * pageSize > MAX_OFFSET) {
    throw new TypeError("Database table page options are invalid");
  }
  return objectFreeze({
    schema: schema.value,
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
  if (budget.bytes > MAX_RESULT_BYTES) return null;
  return objectFreeze({ type: "blob", base64: encoded, bytes: bytes });
}

function cellSnapshot(value, budget) {
  if (value === null) {
    budget.bytes += 4;
    return budget.bytes <= MAX_RESULT_BYTES ? null : undefined;
  }
  if (typeof value === "boolean") {
    budget.bytes++;
    return budget.bytes <= MAX_RESULT_BYTES ? value : undefined;
  }
  if (typeof value === "number") {
    if (!numberIsFinite(value) || mathFloor(value) === value && !numberIsSafeInteger(value)) return undefined;
    budget.bytes += 8;
    return budget.bytes <= MAX_RESULT_BYTES ? value : undefined;
  }
  if (typeof value === "string") {
    var checked = safeText(value, MAX_CELL_BYTES, true, false);
    if (!checked) return undefined;
    budget.bytes += checked.bytes;
    return budget.bytes <= MAX_RESULT_BYTES ? value : undefined;
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

function tablePageResult(value, expected, state) {
  var descriptors = resultEnvelope(value, [
    "catalogVersion", "name", "type", "columns", "rows", "totalRows", "page", "pageSize",
    "pageCount", "pageStart", "pageEnd", "hasPreviousPage", "hasNextPage", "durationMs"
  ], TABLE_FORMAT, state, expected.schema);
  if (!descriptors || descriptors.catalogVersion.value !== expected.catalogVersion || descriptors.name.value !== expected.name ||
    descriptors.type.value !== expected.type || descriptors.page.value !== expected.page ||
    descriptors.pageSize.value !== expected.pageSize ||
    !safeInteger(descriptors.totalRows.value, 0, MAX_SAFE_INTEGER) ||
    !safeInteger(descriptors.pageCount.value, 0, MAX_SAFE_INTEGER) ||
    !safeInteger(descriptors.pageStart.value, 0, MAX_SAFE_INTEGER) ||
    !safeInteger(descriptors.pageEnd.value, 0, MAX_SAFE_INTEGER) ||
    typeof descriptors.hasPreviousPage.value !== "boolean" || typeof descriptors.hasNextPage.value !== "boolean" ||
    !safeInteger(descriptors.durationMs.value, 0, MAX_DURATION_MS)) {
    throw new TypeError("invalid database table page result");
  }
  var budget = { bytes: 0 };
  var columns = columnsSnapshot(descriptors.columns.value, budget);
  var rows = columns && rowsSnapshot(descriptors.rows.value, columns, expected.pageSize, budget);
  if (!columns || !rows) throw new TypeError("invalid database table page data");
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
    throw new TypeError("inconsistent database table page result");
  }
  return objectFreeze({
    catalogVersion: expected.catalogVersion,
    schema: expected.schema,
    name: expected.name,
    type: expected.type,
    columns: columns,
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
    throw new TypeError("invalid database query result");
  }
  var budget = { bytes: 0 };
  var columns = queryColumnsSnapshot(descriptors.columns.value, budget);
  var rows = columns && rowsSnapshot(descriptors.rows.value, columns, MAX_ROWS, budget);
  if (!columns || !rows || descriptors.rowCount.value !== rows.length ||
    descriptors.returnedRows.value !== rows.length) throw new TypeError("invalid database query data");
  return objectFreeze({
    columns: columns,
    rows: rows,
    rowCount: rows.length,
    returnedRows: rows.length,
    truncated: descriptors.truncated.value,
    durationMs: descriptors.durationMs.value
  });
}

function plainOpenOptions(value) {
  var profileFields = ["driver", "host", "port", "database", "user", "password", "tls", "profileId", "rememberPassword"];
  var descriptors = exactObject(value, profileFields);
  var usesProfile = !!descriptors;
  if (!descriptors) descriptors = exactObject(value, ["driver", "host", "port", "database", "user", "password", "tls"]);
  if (!descriptors) throw new TypeError("studioDatabase.open expects exact connection options");
  var driver = descriptors.driver.value;
  var host = safeText(descriptors.host.value, MAX_HOST_BYTES, false, true);
  var database = safeText(descriptors.database.value, MAX_IDENTITY_BYTES, false, true);
  var user = safeText(descriptors.user.value, MAX_IDENTITY_BYTES, false, true);
  var password = safeText(descriptors.password.value, MAX_PASSWORD_BYTES, true, false);
  var port = descriptors.port.value;
  if ((driver !== "postgresql" && driver !== "mysql") || !host || !database || !user || !password ||
    !safeInteger(port, 1, 65535) || typeof descriptors.tls.value !== "boolean") {
    throw new TypeError("Database connection options are invalid");
  }
  var output = {
    driver: driver,
    host: host.value,
    port: port,
    database: database.value,
    user: user.value,
    password: password.value,
    tls: descriptors.tls.value
  };
  if (usesProfile) {
    var profileId = safeText(descriptors.profileId.value, MAX_PROFILE_ID_BYTES, false, true);
    if (!profileId || stringTrim(profileId.value) !== profileId.value ||
      typeof descriptors.rememberPassword.value !== "boolean") {
      throw new TypeError("Database credential options are invalid");
    }
    output.profileId = profileId.value;
    output.rememberPassword = descriptors.rememberPassword.value;
  }
  return output;
}

function bestEffortCloseRawHandle(handle) {
  if (!handle) return;
  nativeCall("close", { handle: handle }, "close", trueResult).then(
    function () {},
    function (error) { report(error); }
  );
}

function ownRawHandle(value) {
  var descriptors;
  try { descriptors = value && typeof value === "object" ? objectGetOwnPropertyDescriptors(value) : null; }
  catch (_) { return null; }
  var descriptor = descriptors && descriptors.handle;
  if (!descriptor || !hasOwn(descriptor, "value") || descriptor.get || descriptor.set) return null;
  return safeToken(descriptor.value);
}

function openResult(value, expected) {
  var cleanupHandle = ownRawHandle(value);
  var fields = expected.usesProfile ?
    ["handle", "driver", "database", "schema", "readOnly", "credentialAvailable", "remembered"] :
    ["handle", "driver", "database", "schema", "readOnly"];
  var descriptors = exactObject(value, fields);
  var handle = descriptors && safeToken(descriptors.handle.value);
  var schema = descriptors && safeName(descriptors.schema.value);
  if (!descriptors || !handle || !schema || descriptors.driver.value !== expected.driver ||
    descriptors.database.value !== expected.database || descriptors.readOnly.value !== true ||
    expected.usesProfile && (typeof descriptors.credentialAvailable.value !== "boolean" ||
      typeof descriptors.remembered.value !== "boolean" || descriptors.remembered.value && !descriptors.credentialAvailable.value)) {
    // Cleanup reads only an own data descriptor and never invokes a getter.
    // Exact public validation remains separate and rejects missing/extra fields.
    if (cleanupHandle) bestEffortCloseRawHandle(cleanupHandle);
    throw new TypeError("invalid database connection metadata");
  }
  return {
    handle: handle,
    driver: expected.driver,
    database: expected.database,
    schema: schema.value,
    credentialAvailable: expected.usesProfile ? descriptors.credentialAvailable.value : null,
    remembered: expected.usesProfile ? descriptors.remembered.value : false
  };
}

function open(input) {
  if (arguments.length !== 1) throw new TypeError("studioDatabase.open expects one connection options object");
  var options = plainOpenOptions(input);
  if (!nativeHost) return promiseReject(studioError("UNAVAILABLE", "open"));
  var expected = { driver: options.driver, database: options.database, usesProfile: !!options.profileId };
  var params = {
    driver: options.driver,
    host: options.host,
    port: options.port,
    database: options.database,
    user: options.user,
    password: options.password,
    tls: options.tls
  };
  if (options.profileId) {
    params.profileId = options.profileId;
    params.rememberPassword = options.rememberPassword;
  }
  return nativeCall("open", params, "open", function (value) {
    return makeReference(openResult(value, expected));
  });
}
function catalog(reference) {
  if (arguments.length !== 1) throw new TypeError("studioDatabase.catalog expects one ConnectionRef");
  var state = referenceState(reference, false);
  return nativeCall("catalog", { handle: state.handle }, "catalog", function (value) {
    return catalogResult(value, state);
  });
}

function tablePage(reference, input) {
  if (arguments.length !== 2) throw new TypeError("studioDatabase.tablePage expects a ConnectionRef and options");
  var state = referenceState(reference, false);
  var options = plainPageOptions(input);
  return nativeCall("tablePage", {
    handle: state.handle,
    schema: options.schema,
    type: options.type,
    name: options.name,
    catalogVersion: options.catalogVersion,
    page: options.page,
    pageSize: options.pageSize
  }, "tablePage", function (value) { return tablePageResult(value, options, state); });
}

function query(reference, sql) {
  if (arguments.length !== 2) throw new TypeError("studioDatabase.query expects a ConnectionRef and SQL text");
  var state = referenceState(reference, false);
  var checked = safeText(sql, MAX_SQL_BYTES, false, false);
  if (!checked || stringTrim(sql) === "") throw new TypeError("Database query must be non-empty text up to 32 KiB");
  return nativeCall("query", { handle: state.handle, sql: sql }, "query", function (value) {
    return queryResult(value, state);
  });
}

function close(reference) {
  if (arguments.length !== 1) throw new TypeError("studioDatabase.close expects one ConnectionRef");
  var state = reference && typeof reference === "object" ? references.get(reference) : null;
  if (!state) throw new TypeError("Database connection reference is invalid");
  if (state.closed) return promiseResolve(true);
  if (state.closing) return state.closing;
  var handle = state.handle;
  state.closing = nativeCall("close", { handle: handle }, "close", trueResult).then(function () {
    state.closed = true;
    state.closing = null;
    state.handle = null;
    return true;
  }, function (error) {
    state.closing = null;
    throw error;
  });
  return state.closing;
}

kit.service("studioDatabase", {
  open: open,
  catalog: catalog,
  tablePage: tablePage,
  query: query,
  close: close
});
})(globalThis, kit);
