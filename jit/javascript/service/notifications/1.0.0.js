;(function (global, kit) {
"use strict";

// KitJS staged service: notifications@1.0.0
// Local, immediate notifications only. Push, scheduling, actions, and custom
// sounds deliberately remain outside this versioned contract.
var KEYS = Object.freeze({ title: true, body: true, tag: true });
var TAG = /^[A-Za-z0-9][A-Za-z0-9._:-]*$/;
var MAX_TITLE_UNITS = 256;
var MAX_BODY_UNITS = 4096;
var MAX_TAG_BYTES = 128;
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
  if (names.length < 2 || names.length > 3) {
    throw new TypeError("Notification data requires title, body, and optional tag");
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
  if (nativeHost) {
    return nativeCall("show", params, function (value) {
      if (value !== true) throw notificationError("FAILED", "show");
      return true;
    });
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

kit.service("notifications", {
  permission: permission,
  requestPermission: requestPermission,
  show: show
});
})(globalThis, kit);
