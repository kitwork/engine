;(function (global, kit) {
"use strict";

// KitJS staged service: notifications@1.2.0
// Adds one optional canonical same-app path for a native notification tap.
// It installs no tap listener and performs no navigation; taps enter the
// existing permission-gated deepLinks latest-wins inbox.
var KEYS = Object.freeze({ title: true, body: true, tag: true, path: true });
var TAG = /^[A-Za-z0-9][A-Za-z0-9._:-]*$/;
var MAX_TITLE_UNITS = 256;
var MAX_BODY_UNITS = 4096;
var MAX_TAG_BYTES = 128;
var MAX_PATH_BYTES = 2048;
var decode = global.decodeURIComponent;
var call = Function.prototype.call;
var functionCall = call.bind(call);
var objectPrototype = Object.prototype;
var hasOwn = call.bind(objectPrototype.hasOwnProperty);
var objectDefineProperties = Object.defineProperties;
var objectFreeze = Object.freeze;
var objectGetOwnPropertyDescriptors = Object.getOwnPropertyDescriptors;
var objectGetOwnPropertySymbols = Object.getOwnPropertySymbols;
var objectGetPrototypeOf = Object.getPrototypeOf;
var objectKeys = Object.keys;
var numberIsSafeInteger = Number.isSafeInteger;
var dateNow = Date.now;
var arrayIsArray = Array.isArray;
var arrayPrototype = Array.prototype;
var regExpTest = call.bind(RegExp.prototype.test);
var stringCharCodeAt = call.bind(String.prototype.charCodeAt);
var PromiseType = global.Promise;
var promiseReject = call.bind(PromiseType.reject, PromiseType);
var promiseResolve = call.bind(PromiseType.resolve, PromiseType);
var assembly = global.document && global.document[Symbol.for("kitjs:assembly")];
var nativeHost = assembly && assembly.nativeHost;

function errorCode(value) {
  var code = "";
  var name = "";
  try {
    code = value && typeof value.code === "string" ? value.code : "";
    name = value && typeof value.name === "string" ? value.name : "";
  } catch (_) { /* Raw platform failures never escape this namespace. */ }
  if (code === "DENIED" || code === "CANCELLED" || code === "UNAVAILABLE" ||
    code === "TIMEOUT" || code === "OVERLOADED") return code;
  if (name === "NotAllowedError" || name === "SecurityError") return "DENIED";
  if (name === "AbortError") return "CANCELLED";
  if (name === "NotFoundError" || name === "NotSupportedError") return "UNAVAILABLE";
  return "FAILED";
}

function notificationError(code, operation) {
  var messages = {
    DENIED: "Notification permission was denied",
    CANCELLED: "Notification operation was cancelled",
    UNAVAILABLE: "Notifications are unavailable",
    TIMEOUT: "Notification operation timed out",
    OVERLOADED: "Notifications are busy",
    FAILED: "Notification operation failed"
  };
  var error = new Error(messages[code] || messages.FAILED);
  objectDefineProperties(error, {
    name: { value: "KitNotificationError" },
    code: { value: code || "FAILED", enumerable: true },
    operation: { value: operation, enumerable: true }
  });
  return objectFreeze(error);
}

function wellFormedWithoutControls(value) {
  for (var index = 0; index < value.length; index++) {
    var unit = stringCharCodeAt(value, index);
    if (unit <= 0x1F || unit >= 0x7F && unit <= 0x9F) return false;
    if (unit >= 0xD800 && unit <= 0xDBFF) {
      if (index + 1 >= value.length) return false;
      var next = stringCharCodeAt(value, index + 1);
      if (next < 0xDC00 || next > 0xDFFF) return false;
      index++;
    } else if (unit >= 0xDC00 && unit <= 0xDFFF) return false;
  }
  return true;
}

function upperHex(unit) {
  return unit >= 48 && unit <= 57 || unit >= 65 && unit <= 70;
}

function hexValue(unit) {
  return unit <= 57 ? unit - 48 : unit - 65 + 10;
}

function unreserved(unit) {
  return unit >= 48 && unit <= 57 || unit >= 65 && unit <= 90 || unit >= 97 && unit <= 122 ||
    unit === 45 || unit === 46 || unit === 95 || unit === 126;
}

function canonicalEscapes(value) {
  for (var index = 0; index < value.length; index++) {
    if (stringCharCodeAt(value, index) !== 37) continue;
    if (index + 2 >= value.length) return false;
    var high = stringCharCodeAt(value, index + 1);
    var low = stringCharCodeAt(value, index + 2);
    if (!upperHex(high) || !upperHex(low) || unreserved(hexValue(high) * 16 + hexValue(low))) return false;
    index += 2;
  }
  return true;
}

function controlOrBackslash(value) {
  for (var index = 0; index < value.length; index++) {
    var unit = stringCharCodeAt(value, index);
    if (unit === 92 || unit <= 31 || unit >= 127 && unit <= 159) return true;
  }
  return false;
}

function dotSegments(path) {
  var segments = path.split("/");
  for (var index = 0; index < segments.length; index++) {
    if (segments[index] === "." || segments[index] === "..") return true;
  }
  return false;
}

function canonicalPath(value) {
  if (typeof value !== "string" || !value || value.length > MAX_PATH_BYTES ||
    value.charAt(0) !== "/" || value.slice(0, 2) === "//" ||
    value.charAt(value.length - 1) === "?" || value.charAt(value.length - 1) === "#" ||
    value.indexOf("?#") >= 0) return null;
  var fragments = 0;
  for (var index = 0; index < value.length; index++) {
    var unit = stringCharCodeAt(value, index);
    if (unit <= 32 || unit >= 127 || unit === 92 ||
      unit === 34 || unit === 60 || unit === 62 || unit === 91 || unit === 93 ||
      unit === 94 || unit === 96 || unit === 123 || unit === 124 || unit === 125) return null;
    if (unit === 35 && ++fragments > 1) return null;
    if (unit === 37) {
      if (index + 2 >= value.length) return null;
      var high = stringCharCodeAt(value, index + 1);
      var low = stringCharCodeAt(value, index + 2);
      if (!upperHex(high) || !upperHex(low) || unreserved(hexValue(high) * 16 + hexValue(low))) return null;
      index += 2;
    }
  }
  var delimiter = value.search(/[?#]/);
  var path = delimiter < 0 ? value : value.slice(0, delimiter);
  var decodedPath = path;
  for (var depth = 0; depth < 8; depth++) {
    if (!canonicalEscapes(decodedPath) || /%2F|%5C/i.test(decodedPath)) return null;
    var nextPath;
    try { nextPath = decode(decodedPath); }
    catch (_) { return null; }
    if (nextPath.charAt(0) !== "/" || nextPath.slice(0, 2) === "//" ||
      controlOrBackslash(nextPath) || dotSegments(nextPath)) return null;
    if (nextPath === decodedPath) break;
    decodedPath = nextPath;
    if (depth === 7) {
      if (decodedPath.indexOf("%") >= 0) return null;
      break;
    }
  }
  var decoded = value;
  for (var round = 0; round < 8; round++) {
    if (!canonicalEscapes(decoded)) return null;
    var next;
    try { next = decode(decoded); }
    catch (_) { return null; }
    if (controlOrBackslash(next)) return null;
    if (next === decoded) return value;
    decoded = next;
    if (round === 7) return decoded.indexOf("%") < 0 ? value : null;
  }
  return null;
}

function plainPayload(value) {
  var prototype;
  var symbols;
  var descriptors;
  try {
    prototype = value && objectGetPrototypeOf(value);
    symbols = value && objectGetOwnPropertySymbols(value);
    descriptors = value && objectGetOwnPropertyDescriptors(value);
  } catch (_) {
    throw new TypeError("Notification data must be a plain object");
  }
  if (!value || prototype !== objectPrototype && prototype !== null || symbols.length) {
    throw new TypeError("Notification data must be a plain object");
  }
  var names = objectKeys(descriptors);
  if (names.length < 2 || names.length > 4) {
    throw new TypeError("Notification data requires title, body, optional tag, and optional path");
  }
  var output = {};
  for (var index = 0; index < names.length; index++) {
    var name = names[index];
    if (!hasOwn(KEYS, name)) throw new TypeError("Unknown notification field: " + name);
    var descriptor = descriptors[name];
    if (!descriptor.enumerable || !hasOwn(descriptor, "value") || descriptor.get || descriptor.set) {
      throw new TypeError("Notification data must not contain accessors");
    }
    output[name] = descriptor.value;
  }
  if (!hasOwn(output, "title") || !hasOwn(output, "body") ||
    typeof output.title !== "string" || !output.title || output.title.length > MAX_TITLE_UNITS ||
    !wellFormedWithoutControls(output.title)) {
    throw new TypeError("Notification title must be non-empty control-free text up to 256 UTF-16 units");
  }
  if (typeof output.body !== "string" || output.body.length > MAX_BODY_UNITS ||
    !wellFormedWithoutControls(output.body)) {
    throw new TypeError("Notification body must be control-free text up to 4096 UTF-16 units");
  }
  if (hasOwn(output, "tag")) {
    if (typeof output.tag !== "string" || !output.tag || output.tag.length > MAX_TAG_BYTES ||
      !regExpTest(TAG, output.tag)) {
      throw new TypeError("Notification tag must be a safe ASCII identifier up to 128 bytes");
    }
  }
  if (hasOwn(output, "path")) {
    output.path = canonicalPath(output.path);
    if (output.path === null) {
      throw new TypeError("Notification path must be one canonical same-app route up to 2048 ASCII bytes");
    }
  }
  return objectFreeze(output);
}

function permissionResult(value, operation, browserDefault) {
  if (browserDefault && value === "default") value = "prompt";
  if (value !== "granted" && value !== "denied" && value !== "prompt") {
    throw notificationError("FAILED", operation);
  }
  return value;
}

function platformNotification(operation) {
  var NotificationType;
  try { NotificationType = global.Notification; }
  catch (error) { throw notificationError(errorCode(error), operation); }
  if (typeof NotificationType !== "function") {
    throw notificationError("UNAVAILABLE", operation);
  }
  return NotificationType;
}

function nativeCall(action, params, validator) {
  try {
    return promiseResolve(nativeHost.call("notifications." + action, params)).then(
      function (value) {
        try { return validator(value); }
        catch (error) {
          if (error && error.name === "KitNotificationError") throw error;
          throw notificationError("FAILED", action);
        }
      },
      function (error) { throw notificationError(errorCode(error), action); }
    );
  } catch (error) {
    return promiseReject(notificationError(errorCode(error), action));
  }
}

function permission() {
  if (arguments.length !== 0) throw new TypeError("notifications.permission does not accept parameters");
  if (nativeHost) {
    return nativeCall("permission", {}, function (value) {
      return permissionResult(value, "permission", false);
    });
  }
  try {
    var NotificationType = platformNotification("permission");
    return promiseResolve(permissionResult(NotificationType.permission, "permission", true));
  } catch (error) {
    return promiseReject(error && error.name === "KitNotificationError" ? error :
      notificationError(errorCode(error), "permission"));
  }
}

function requestPermission() {
  if (arguments.length !== 0) throw new TypeError("notifications.requestPermission does not accept parameters");
  if (nativeHost) {
    return nativeCall("requestPermission", {}, function (value) {
      return permissionResult(value, "requestPermission", false);
    });
  }
  var NotificationType;
  try { NotificationType = platformNotification("requestPermission"); }
  catch (error) { return promiseReject(error); }
  var request;
  try { request = NotificationType.requestPermission; }
  catch (error) { return promiseReject(notificationError(errorCode(error), "requestPermission")); }
  if (typeof request !== "function") {
    return promiseReject(notificationError("UNAVAILABLE", "requestPermission"));
  }
  try {
    return promiseResolve(functionCall(request, NotificationType)).then(
      function (value) { return permissionResult(value, "requestPermission", true); },
      function (error) { throw notificationError(errorCode(error), "requestPermission"); }
    );
  } catch (error) {
    return promiseReject(notificationError(errorCode(error), "requestPermission"));
  }
}

function show(input) {
  if (arguments.length !== 1) throw new TypeError("notifications.show requires one data object");
  var data = plainPayload(input);
  var params = { title: data.title, body: data.body };
  if (data.tag !== undefined) params.tag = data.tag;
  if (data.path !== undefined) params.path = data.path;
  if (nativeHost) {
    return nativeCall("show", params, function (value) {
      if (value !== true) throw notificationError("FAILED", "show");
      return true;
    });
  }
  if (data.path !== undefined) {
    return promiseReject(notificationError("UNAVAILABLE", "show"));
  }
  try {
    var NotificationType = platformNotification("show");
    if (permissionResult(NotificationType.permission, "show", true) !== "granted") {
      return promiseReject(notificationError("DENIED", "show"));
    }
    var options = { body: data.body };
    if (data.tag !== undefined) options.tag = data.tag;
    new NotificationType(data.title, options);
    return promiseResolve(true);
  } catch (error) {
    return promiseReject(error && error.name === "KitNotificationError" ? error :
      notificationError(errorCode(error), "show"));
  }
}


function scheduledPayload(input) {
  var prototype, descriptors, symbols;
  try {
    prototype = objectGetPrototypeOf(input);
    descriptors = objectGetOwnPropertyDescriptors(input);
    symbols = objectGetOwnPropertySymbols(input);
  } catch (_) { throw new TypeError("Invalid reminder payload"); }
  if (!input || prototype !== objectPrototype && prototype !== null || symbols.length) throw new TypeError("Invalid reminder payload");
  var names = objectKeys(descriptors), data = {}, at;
  if (names.length < 4 || names.length > 5) throw new TypeError("Reminder requires title, body, tag and at");
  names.forEach(function (name) {
    var descriptor = descriptors[name];
    if (!descriptor.enumerable || !hasOwn(descriptor, "value") || descriptor.get || descriptor.set) throw new TypeError("Reminder data cannot contain accessors");
    if (name === "at") at = descriptor.value;
    else {
      if (!hasOwn(KEYS, name)) throw new TypeError("Unknown reminder field");
      data[name] = descriptor.value;
    }
  });
  if (!hasOwn(data, "tag")) throw new TypeError("Reminder tag is required");
  var now = dateNow();
  if (!numberIsSafeInteger(at) || at < now + 1000 || at > now + 30 * 24 * 60 * 60 * 1000) throw new TypeError("Reminder time must be 1 second to 30 days in the future");
  var checked = plainPayload(data);
  var result = {title: checked.title, body: checked.body, tag: checked.tag, at: at};
  if (checked.path !== undefined) result.path = checked.path;
  return result;
}
function schedule(input) {
  if (arguments.length !== 1) throw new TypeError("notifications.schedule requires one payload");
  var params = scheduledPayload(input);
  if (!nativeHost) return promiseReject(notificationError("UNAVAILABLE", "schedule"));
  return permission().then(function (state) {
    if (state !== "granted") throw notificationError("DENIED", "schedule");
    return nativeCall("schedule", params, function (value) {
      if (value !== true) throw new TypeError("Invalid reminder acknowledgement");
      return true;
    });
  });
}
function pending() {
  if (arguments.length) throw new TypeError("notifications.pending accepts no arguments");
  if (!nativeHost) return promiseReject(notificationError("UNAVAILABLE", "pending"));
  return nativeCall("pending", {}, function (value) {
    if (!arrayIsArray(value) || objectGetPrototypeOf(value) !== arrayPrototype || value.length > 32 || objectGetOwnPropertySymbols(value).length) throw new TypeError("Invalid reminders");
    var arrayDescriptors = objectGetOwnPropertyDescriptors(value), output = [], seen = Object.create(null);
    if (objectKeys(arrayDescriptors).length !== value.length + 1) throw new TypeError("Invalid reminder array");
    for (var index = 0; index < value.length; index++) {
      var entry = arrayDescriptors[String(index)];
      if (!entry || !entry.enumerable || !hasOwn(entry, "value")) throw new TypeError("Invalid reminder entry");
      var item = entry.value, prototype = item && objectGetPrototypeOf(item);
      if (!item || prototype !== objectPrototype && prototype !== null || objectGetOwnPropertySymbols(item).length) throw new TypeError("Invalid reminder entry");
      var d = objectGetOwnPropertyDescriptors(item);
      if (objectKeys(d).length !== 2 || !d.tag || !d.at || !d.tag.enumerable || !d.at.enumerable ||
          !hasOwn(d.tag, "value") || !hasOwn(d.at, "value")) throw new TypeError("Invalid reminder entry");
      var tag = d.tag.value, at = d.at.value;
      if (typeof tag !== "string" || tag.length > MAX_TAG_BYTES || !regExpTest(TAG, tag) || seen[tag] || !numberIsSafeInteger(at) || at <= 0) throw new TypeError("Invalid reminder fields");
      seen[tag] = true;
      output.push(objectFreeze({tag: tag, at: at}));
    }
    return objectFreeze(output);
  });
}
function cancel(tag) {
  if (arguments.length !== 1 || typeof tag !== "string" || tag.length > MAX_TAG_BYTES || !regExpTest(TAG, tag)) throw new TypeError("Invalid reminder tag");
  if (!nativeHost) return promiseReject(notificationError("UNAVAILABLE", "cancel"));
  return nativeCall("cancel", {tag: tag}, function (value) {
    if (value !== true) throw new TypeError("Invalid reminder cancellation");
    return true;
  });
}

kit.service("notifications", {
  schedule: schedule,
  pending: pending,
  cancel: cancel,
  permission: permission,
  requestPermission: requestPermission,
  show: show
});
})(globalThis, kit);
