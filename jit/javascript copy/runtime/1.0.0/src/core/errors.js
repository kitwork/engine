"use strict";

function KitworkRuntimeError(code, message, context, cause) {
  this.name = "KitworkRuntimeError";
  this.code = code || "KIT_RUNTIME_ERROR";
  this.message = message || this.code;
  this.context = context || null;
  this.cause = cause || null;
  if (Error.captureStackTrace) Error.captureStackTrace(this, KitworkRuntimeError);
}
KitworkRuntimeError.prototype = Object.create(Error.prototype);
KitworkRuntimeError.prototype.constructor = KitworkRuntimeError;

function createRuntimeError(code, message, context, cause) {
  return new KitworkRuntimeError(code, message, context || null, cause || null);
}

function normalizeRuntimeError(error, code, message, context) {
  if (error instanceof KitworkRuntimeError) {
    if (!error.context && context) error.context = context;
    return error;
  }
  if (error && error.code && typeof error.message === "string") {
    var wrapped = createRuntimeError(error.code, error.message, context || error.context, error);
    return wrapped;
  }
  return createRuntimeError(
    code || "KIT_RUNTIME_ERROR",
    message || (error && error.message ? error.message : String(error)),
    context || null,
    error || null
  );
}

module.exports = {
  KitworkRuntimeError: KitworkRuntimeError,
  createRuntimeError: createRuntimeError,
  normalizeRuntimeError: normalizeRuntimeError
};
