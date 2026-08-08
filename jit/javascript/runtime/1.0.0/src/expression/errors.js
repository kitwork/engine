"use strict";

function KitworkExpressionError(code, message, details, cause) {
  this.name = "KitworkExpressionError";
  this.code = code || "KIT_EXPRESSION_ERROR";
  this.message = message || this.code;
  this.details = details || null;
  this.cause = cause || null;
  if (Error.captureStackTrace) Error.captureStackTrace(this, KitworkExpressionError);
}

KitworkExpressionError.prototype = Object.create(Error.prototype);
KitworkExpressionError.prototype.constructor = KitworkExpressionError;

function createError(code, message, details, cause) {
  return new KitworkExpressionError(code, message, details, cause);
}

module.exports = {
  KitworkExpressionError,
  createError
};
