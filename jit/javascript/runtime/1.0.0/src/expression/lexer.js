"use strict";

const { createError } = require("./errors.js");
const {
  FORBIDDEN_KEYWORDS,
  isIdentifierStart,
  isIdentifierPart
} = require("./constants.js");
const { skipBracedExpressionSource } = require("./source-scanner.js");

function Lexer(source) {
  this.source = String(source == null ? "" : source);
  this.length = this.source.length;
  this.index = 0;
  this.tokens = [];
}

Lexer.prototype.raise = function (code, message, position) {
  throw createError(code, message, {
    source: this.source,
    position: position == null ? this.index : position
  });
};

Lexer.prototype.push = function (type, value, start, end) {
  this.tokens.push({
    type,
    value,
    start,
    end: end == null ? this.index : end
  });
};

Lexer.prototype.scanString = function (quote) {
  const start = this.index++;
  let value = "";

  while (this.index < this.length) {
    const character = this.source[this.index++];
    if (character === quote) {
      this.push("literal", value, start, this.index);
      return;
    }
    if (character !== "\\") {
      value += character;
      continue;
    }

    if (this.index >= this.length) {
      this.raise("KIT_PARSE_UNTERMINATED_ESCAPE", "Unterminated escape sequence", start);
    }

    const escaped = this.source[this.index++];
    if (escaped === "n") value += "\n";
    else if (escaped === "r") value += "\r";
    else if (escaped === "t") value += "\t";
    else if (escaped === "b") value += "\b";
    else if (escaped === "f") value += "\f";
    else if (escaped === "v") value += "\v";
    else if (escaped === "0") value += "\0";
    else if (escaped === "u") {
      const hex = this.source.slice(this.index, this.index + 4);
      if (!/^[0-9A-Fa-f]{4}$/.test(hex)) {
        this.raise("KIT_PARSE_INVALID_UNICODE_ESCAPE", "Invalid unicode escape", this.index - 2);
      }
      value += String.fromCharCode(parseInt(hex, 16));
      this.index += 4;
    } else {
      value += escaped;
    }
  }

  this.raise("KIT_PARSE_UNTERMINATED_STRING", "Unterminated string literal", start);
};

Lexer.prototype.scanNumber = function () {
  const start = this.index;
  const source = this.source;
  let index = this.index;

  if (source[index] === ".") index++;
  while (index < this.length && /[0-9]/.test(source[index])) index++;

  if (source[index] === ".") {
    index++;
    while (index < this.length && /[0-9]/.test(source[index])) index++;
  }

  if (source[index] === "e" || source[index] === "E") {
    index++;
    if (source[index] === "+" || source[index] === "-") index++;
    const exponentStart = index;
    while (index < this.length && /[0-9]/.test(source[index])) index++;
    if (index === exponentStart) {
      this.raise("KIT_PARSE_INVALID_NUMBER", "Exponent requires at least one digit", start);
    }
  }

  const raw = source.slice(start, index);
  const value = Number(raw);
  if (!raw || !Number.isFinite(value)) {
    this.raise("KIT_PARSE_INVALID_NUMBER", "Invalid number literal", start);
  }

  this.index = index;
  this.push("literal", value, start, index);
};

Lexer.prototype.scanIdentifier = function () {
  const start = this.index;
  let value = "";

  while (this.index < this.length && isIdentifierPart(this.source[this.index])) {
    value += this.source[this.index++];
  }

  if (FORBIDDEN_KEYWORDS[value]) {
    this.raise("KIT_PARSE_FORBIDDEN_KEYWORD", "Forbidden keyword '" + value + "'", start);
  }

  if (value === "true") this.push("literal", true, start);
  else if (value === "false") this.push("literal", false, start);
  else if (value === "null") this.push("literal", null, start);
  else if (value === "undefined") this.push("literal", undefined, start);
  else this.push("identifier", value, start);
};

Lexer.prototype.readTemplateInterpolation = function () {
  const start = this.index;
  const end = skipBracedExpressionSource(this.source, this.index);
  const result = this.source.slice(start, end - 1);
  this.index = end;
  return result;
};

Lexer.prototype.scanTemplate = function () {
  const start = this.index++;
  const quasis = [];
  const expressions = [];
  let current = "";

  while (this.index < this.length) {
    const character = this.source[this.index++];
    if (character === "`") {
      quasis.push(current);
      this.push("template", { quasis, expressions }, start, this.index);
      return;
    }

    if (character === "\\") {
      if (this.index >= this.length) {
        this.raise("KIT_PARSE_UNTERMINATED_TEMPLATE", "Unterminated template escape", start);
      }
      const escaped = this.source[this.index++];
      if (escaped === "n") current += "\n";
      else if (escaped === "r") current += "\r";
      else if (escaped === "t") current += "\t";
      else current += escaped;
      continue;
    }

    if (character === "$" && this.source[this.index] === "{") {
      this.index++;
      quasis.push(current);
      current = "";
      expressions.push(this.readTemplateInterpolation());
      continue;
    }

    current += character;
  }

  this.raise("KIT_PARSE_UNTERMINATED_TEMPLATE", "Unterminated template literal", start);
};

Lexer.prototype.tokenize = function () {
  while (this.index < this.length) {
    const character = this.source[this.index];

    if (/\s/.test(character)) {
      this.index++;
      continue;
    }

    if (character === "'" || character === '"') {
      this.scanString(character);
      continue;
    }

    if (character === "`") {
      this.scanTemplate();
      continue;
    }

    if (/[0-9]/.test(character) ||
        (character === "." && /[0-9]/.test(this.source[this.index + 1]))) {
      this.scanNumber();
      continue;
    }

    if (isIdentifierStart(character)) {
      this.scanIdentifier();
      continue;
    }

    const start = this.index;
    const three = this.source.slice(this.index, this.index + 3);
    const two = this.source.slice(this.index, this.index + 2);

    if (three === "===" || three === "!==") {
      this.index += 3;
      this.push("operator", three, start);
      continue;
    }

    if (two === "?." || two === "??" || two === "&&" || two === "||" ||
        two === "<=" || two === ">=") {
      this.index += 2;
      this.push("operator", two, start);
      continue;
    }

    if (two === "=>") {
      this.raise("KIT_PARSE_ARROW_FUNCTION", "Arrow functions are not allowed in markup", start);
    }
    if (two === "++" || two === "--") {
      this.raise("KIT_PARSE_INCREMENT", "Increment/decrement are not allowed", start);
    }
    if (two === "==" || two === "!=") {
      this.raise("KIT_PARSE_LOOSE_EQUALITY", "Loose equality is not supported; use === or !==", start);
    }
    if (two === "+=" || two === "-=" || two === "*=" || two === "/=" || two === "%=") {
      this.raise("KIT_PARSE_COMPOUND_ASSIGNMENT", "Compound assignment is not supported", start);
    }

    if ("+-*/%<>!?:.,()[]{}=;".indexOf(character) >= 0) {
      this.index++;
      this.push("operator", character, start);
      continue;
    }

    this.raise("KIT_PARSE_UNEXPECTED_CHARACTER", "Unexpected character '" + character + "'", start);
  }

  this.push("eof", "", this.length, this.length);
  return this.tokens;
};

function lex(source) {
  return new Lexer(source).tokenize();
}

module.exports = {
  Lexer,
  lex
};
