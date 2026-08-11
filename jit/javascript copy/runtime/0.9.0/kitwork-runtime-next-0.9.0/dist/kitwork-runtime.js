/* Kitwork Client Runtime Next — M2 Core
 * Generated from modular source. Do not edit dist directly.
 */
(function(global){
"use strict";
var __modules = {
0: function(module, exports, __require){
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

},
1: function(module, exports, __require){
"use strict";

const MODES = Object.freeze({
  NAMED_MAP: "named-map",
  BINDING: "binding",
  CLASS_VALUE: "class-value",
  ACTION: "action",
  WRITABLE_PATH: "writable-path",
  IDENTITY: "identity",
  ITERATOR: "iterator"
});

const MODE_ALIASES = Object.freeze({
  map: MODES.NAMED_MAP,
  namedMap: MODES.NAMED_MAP,
  binding: MODES.BINDING,
  class: MODES.CLASS_VALUE,
  classValue: MODES.CLASS_VALUE,
  action: MODES.ACTION,
  writable: MODES.WRITABLE_PATH,
  writablePath: MODES.WRITABLE_PATH,
  identity: MODES.IDENTITY,
  iterator: MODES.ITERATOR
});

function createWordSet(source) {
  const output = Object.create(null);
  String(source || "").split(/\s+/).forEach((word) => {
    if (word) output[word] = true;
  });
  return output;
}

const BLOCKED_MEMBERS = createWordSet(
  "constructor prototype __proto__ __defineGetter__ __defineSetter__ " +
  "__lookupGetter__ __lookupSetter__ ownerDocument defaultView contentWindow " +
  "window globalThis top parent self"
);

const FORBIDDEN_KEYWORDS = createWordSet(
  "var let const function class return if else for while do switch case new " +
  "delete void typeof instanceof in await yield throw try catch finally import export"
);

const RESERVED_ASSIGNMENT_ROOTS = createWordSet(
  "$element $host $event $refs $parent $index kit"
);

function hasOwn(object, key) {
  return object != null && Object.prototype.hasOwnProperty.call(object, key);
}

function isThenable(value) {
  return value != null &&
    (typeof value === "object" || typeof value === "function") &&
    typeof value.then === "function";
}

function isIdentifierStart(character) {
  return character === "$" || character === "_" ||
    (character >= "A" && character <= "Z") ||
    (character >= "a" && character <= "z");
}

function isIdentifierPart(character) {
  return isIdentifierStart(character) ||
    (character >= "0" && character <= "9");
}

function isBlockedMember(key) {
  return typeof key === "string" && BLOCKED_MEMBERS[key] === true;
}

function rootIdentifier(ast) {
  let current = ast;
  while (current && current.type === "MemberExpression") current = current.object;
  return current && current.type === "Identifier" ? current.name : "";
}

function normalizeMode(mode) {
  mode = mode || MODES.BINDING;
  return MODE_ALIASES[mode] || mode;
}

module.exports = {
  MODES,
  MODE_ALIASES,
  BLOCKED_MEMBERS,
  FORBIDDEN_KEYWORDS,
  RESERVED_ASSIGNMENT_ROOTS,
  createWordSet,
  hasOwn,
  isThenable,
  isIdentifierStart,
  isIdentifierPart,
  isBlockedMember,
  rootIdentifier,
  normalizeMode
};

},
2: function(module, exports, __require){
"use strict";

const { createError } = __require(0);

function skipQuotedSource(source, index, quote) {
  index++;
  while (index < source.length) {
    const character = source[index++];
    if (character === "\\") {
      if (index < source.length) index++;
      continue;
    }
    if (character === quote) return index;
  }
  throw createError("KIT_PARSE_UNTERMINATED_STRING", "Unterminated string literal", {
    source,
    position: index
  });
}

function skipTemplateSource(source, index) {
  index++;
  while (index < source.length) {
    const character = source[index];
    if (character === "\\") {
      index += 2;
      continue;
    }
    if (character === "`") return index + 1;
    if (character === "$" && source[index + 1] === "{") {
      index = skipBracedExpressionSource(source, index + 2);
      continue;
    }
    index++;
  }
  throw createError("KIT_PARSE_UNTERMINATED_TEMPLATE", "Unterminated template literal", {
    source,
    position: index
  });
}

function skipBracedExpressionSource(source, index) {
  let depth = 1;
  while (index < source.length) {
    const character = source[index];
    if (character === "'" || character === '"') {
      index = skipQuotedSource(source, index, character);
      continue;
    }
    if (character === "`") {
      index = skipTemplateSource(source, index);
      continue;
    }
    if (character === "{") {
      depth++;
      index++;
      continue;
    }
    if (character === "}") {
      depth--;
      index++;
      if (depth === 0) return index;
      continue;
    }
    index++;
  }
  throw createError(
    "KIT_PARSE_UNTERMINATED_TEMPLATE_EXPRESSION",
    "Unterminated template interpolation",
    { source, position: index }
  );
}

function scanTopLevel(source, visitor) {
  let round = 0;
  let square = 0;
  let curly = 0;
  let index = 0;

  while (index < source.length) {
    const character = source[index];
    if (character === "'" || character === '"') {
      index = skipQuotedSource(source, index, character);
      continue;
    }
    if (character === "`") {
      index = skipTemplateSource(source, index);
      continue;
    }

    if (character === "(") round++;
    else if (character === ")") round--;
    else if (character === "[") square++;
    else if (character === "]") square--;
    else if (character === "{") curly++;
    else if (character === "}") curly--;
    else if (round === 0 && square === 0 && curly === 0) visitor(character, index);

    if (round < 0 || square < 0 || curly < 0) {
      throw createError("KIT_PARSE_UNBALANCED_DELIMITER", "Unbalanced delimiter", {
        source,
        position: index
      });
    }
    index++;
  }

  if (round !== 0 || square !== 0 || curly !== 0) {
    throw createError("KIT_PARSE_UNBALANCED_DELIMITER", "Unbalanced delimiter", {
      source,
      position: source.length
    });
  }
}

module.exports = {
  skipQuotedSource,
  skipTemplateSource,
  skipBracedExpressionSource,
  scanTopLevel
};

},
3: function(module, exports, __require){
"use strict";

const { createError } = __require(0);
const {
  FORBIDDEN_KEYWORDS,
  isIdentifierStart,
  isIdentifierPart
} = __require(1);
const { skipBracedExpressionSource } = __require(2);

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

},
4: function(module, exports, __require){
"use strict";

const { createError } = __require(0);
const { isBlockedMember } = __require(1);
const { lex } = __require(3);

function node(type, start, end, properties) {
  const output = { type, start, end };
  if (properties) Object.assign(output, properties);
  return output;
}

function isWritableAst(ast) {
  if (!ast) return false;
  if (ast.type === "Identifier") return true;
  return ast.type === "MemberExpression" && !ast.optional && isWritableAst(ast.object);
}

function Parser(tokens, source, options) {
  this.tokens = tokens;
  this.source = source;
  this.options = options || {};
  this.index = 0;
}

Parser.prototype.current = function () {
  return this.tokens[this.index];
};

Parser.prototype.previous = function () {
  return this.tokens[Math.max(0, this.index - 1)];
};

Parser.prototype.next = function () {
  return this.tokens[this.index++];
};

Parser.prototype.is = function (value) {
  const token = this.current();
  return token && token.type === "operator" && token.value === value;
};

Parser.prototype.match = function (value) {
  if (!this.is(value)) return false;
  this.index++;
  return true;
};

Parser.prototype.expect = function (value) {
  const token = this.current();
  if (!this.is(value)) {
    throw createError(
      "KIT_PARSE_EXPECTED_TOKEN",
      "Expected '" + value + "' but found '" + (token ? token.value : "<end>") + "'",
      { source: this.source, position: token ? token.start : this.source.length }
    );
  }
  this.index++;
  return token;
};

Parser.prototype.expectIdentifier = function () {
  const token = this.current();
  if (!token || token.type !== "identifier") {
    throw createError(
      "KIT_PARSE_EXPECTED_IDENTIFIER",
      "Expected identifier",
      { source: this.source, position: token ? token.start : this.source.length }
    );
  }
  this.index++;
  return token;
};

Parser.prototype.parseProgram = function () {
  const body = [];
  while (this.current().type !== "eof") {
    body.push(this.parseAssignment());
    if (this.match(";")) {
      while (this.match(";")) { /* Ignore empty statements. */ }
    } else if (this.current().type !== "eof") {
      throw createError(
        "KIT_PARSE_ACTION_SEPARATOR",
        "Expected ';' between action expressions",
        { source: this.source, position: this.current().start }
      );
    }
  }
  return node("Program", 0, this.source.length, { body });
};

Parser.prototype.parseSingleExpression = function () {
  const expression = this.parseAssignment();
  if (this.current().type !== "eof") {
    throw createError(
      "KIT_PARSE_TRAILING_INPUT",
      "Unexpected trailing input near '" + this.current().value + "'",
      { source: this.source, position: this.current().start }
    );
  }
  return expression;
};

Parser.prototype.parseAssignment = function () {
  const left = this.parseConditional();
  if (!this.match("=")) return left;

  if (!this.options.allowAssignment) {
    throw createError(
      "KIT_BINDING_ASSIGNMENT",
      "Assignment is not allowed in this parser mode",
      { source: this.source, position: this.previous().start }
    );
  }

  if (!isWritableAst(left)) {
    throw createError(
      "KIT_INVALID_ASSIGNMENT_TARGET",
      "Assignment target must be an identifier or writable member path",
      { source: this.source, position: left.start }
    );
  }

  const right = this.parseAssignment();
  return node("AssignmentExpression", left.start, right.end, {
    operator: "=",
    left,
    right
  });
};

Parser.prototype.parseConditional = function () {
  const test = this.parseNullish();
  if (!this.match("?")) return test;
  const consequent = this.parseAssignment();
  this.expect(":");
  const alternate = this.parseAssignment();
  return node("ConditionalExpression", test.start, alternate.end, {
    test,
    consequent,
    alternate
  });
};

Parser.prototype.parseNullish = function () {
  let expression = this.parseOr();
  while (this.match("??")) {
    const right = this.parseOr();
    expression = node("LogicalExpression", expression.start, right.end, {
      operator: "??",
      left: expression,
      right
    });
  }
  return expression;
};

Parser.prototype.parseOr = function () {
  let expression = this.parseAnd();
  while (this.match("||")) {
    const right = this.parseAnd();
    expression = node("LogicalExpression", expression.start, right.end, {
      operator: "||",
      left: expression,
      right
    });
  }
  return expression;
};

Parser.prototype.parseAnd = function () {
  let expression = this.parseEquality();
  while (this.match("&&")) {
    const right = this.parseEquality();
    expression = node("LogicalExpression", expression.start, right.end, {
      operator: "&&",
      left: expression,
      right
    });
  }
  return expression;
};

Parser.prototype.parseEquality = function () {
  let expression = this.parseRelational();
  while (this.is("===") || this.is("!==")) {
    const operator = this.next();
    const right = this.parseRelational();
    expression = node("BinaryExpression", expression.start, right.end, {
      operator: operator.value,
      left: expression,
      right
    });
  }
  return expression;
};

Parser.prototype.parseRelational = function () {
  let expression = this.parseAdditive();
  while (this.is("<") || this.is(">") || this.is("<=") || this.is(">=")) {
    const operator = this.next();
    const right = this.parseAdditive();
    expression = node("BinaryExpression", expression.start, right.end, {
      operator: operator.value,
      left: expression,
      right
    });
  }
  return expression;
};

Parser.prototype.parseAdditive = function () {
  let expression = this.parseMultiplicative();
  while (this.is("+") || this.is("-")) {
    const operator = this.next();
    const right = this.parseMultiplicative();
    expression = node("BinaryExpression", expression.start, right.end, {
      operator: operator.value,
      left: expression,
      right
    });
  }
  return expression;
};

Parser.prototype.parseMultiplicative = function () {
  let expression = this.parseUnary();
  while (this.is("*") || this.is("/") || this.is("%")) {
    const operator = this.next();
    const right = this.parseUnary();
    expression = node("BinaryExpression", expression.start, right.end, {
      operator: operator.value,
      left: expression,
      right
    });
  }
  return expression;
};

Parser.prototype.parseUnary = function () {
  if (this.is("!") || this.is("-") || this.is("+")) {
    const operator = this.next();
    const argument = this.parseUnary();
    return node("UnaryExpression", operator.start, argument.end, {
      operator: operator.value,
      argument
    });
  }
  return this.parsePostfix();
};

Parser.prototype.parsePostfix = function () {
  let expression = this.parsePrimary();

  while (true) {
    if (this.match(".")) {
      const property = this.expectIdentifier();
      expression = node("MemberExpression", expression.start, property.end, {
        object: expression,
        property: node("Literal", property.start, property.end, { value: property.value }),
        computed: false,
        optional: false
      });
      continue;
    }

    if (this.match("?.")) {
      const optionalStart = this.previous().start;
      if (this.match("[")) {
        const optionalProperty = this.parseAssignment();
        const optionalClose = this.expect("]");
        expression = node("MemberExpression", expression.start, optionalClose.end, {
          object: expression,
          property: optionalProperty,
          computed: true,
          optional: true
        });
        continue;
      }
      if (this.match("(")) {
        const optionalArgs = this.parseArgumentsAfterOpen();
        expression = node("CallExpression", expression.start, optionalArgs.end, {
          callee: expression,
          arguments: optionalArgs.arguments,
          optional: true
        });
        continue;
      }
      const optionalName = this.expectIdentifier();
      expression = node("MemberExpression", expression.start, optionalName.end, {
        object: expression,
        property: node("Literal", optionalName.start, optionalName.end, { value: optionalName.value }),
        computed: false,
        optional: true,
        operatorStart: optionalStart
      });
      continue;
    }

    if (this.match("[")) {
      const computedProperty = this.parseAssignment();
      const computedClose = this.expect("]");
      expression = node("MemberExpression", expression.start, computedClose.end, {
        object: expression,
        property: computedProperty,
        computed: true,
        optional: false
      });
      continue;
    }

    if (this.match("(")) {
      const args = this.parseArgumentsAfterOpen();
      expression = node("CallExpression", expression.start, args.end, {
        callee: expression,
        arguments: args.arguments,
        optional: false
      });
      continue;
    }

    break;
  }

  return expression;
};

Parser.prototype.parseArgumentsAfterOpen = function () {
  const args = [];
  if (!this.match(")")) {
    do {
      args.push(this.parseAssignment());
    } while (this.match(","));
    const close = this.expect(")");
    return { arguments: args, end: close.end };
  }
  return { arguments: args, end: this.previous().end };
};

Parser.prototype.parsePrimary = function () {
  const token = this.current();

  if (token.type === "literal") {
    this.next();
    return node("Literal", token.start, token.end, { value: token.value });
  }

  if (token.type === "template") {
    this.next();
    const expressions = token.value.expressions.map(parseBinding);
    return node("TemplateLiteral", token.start, token.end, {
      quasis: token.value.quasis.slice(),
      expressions
    });
  }

  if (token.type === "identifier") {
    this.next();
    return node("Identifier", token.start, token.end, { name: token.value });
  }

  if (this.match("(")) {
    const grouped = this.parseAssignment();
    const close = this.expect(")");
    grouped.end = close.end;
    return grouped;
  }

  if (this.match("[")) {
    const arrayStart = this.previous().start;
    const elements = [];
    if (!this.match("]")) {
      do {
        if (this.is("]")) {
          throw createError("KIT_PARSE_TRAILING_COMMA", "Array trailing comma is not supported", {
            source: this.source,
            position: this.current().start
          });
        }
        elements.push(this.parseAssignment());
      } while (this.match(","));
      const arrayClose = this.expect("]");
      return node("ArrayExpression", arrayStart, arrayClose.end, { elements });
    }
    return node("ArrayExpression", arrayStart, this.previous().end, { elements });
  }

  if (this.match("{")) {
    const objectStart = this.previous().start;
    const properties = [];
    const seenKeys = Object.create(null);
    if (!this.match("}")) {
      do {
        if (this.is("}")) {
          throw createError("KIT_PARSE_TRAILING_COMMA", "Object trailing comma is not supported", {
            source: this.source,
            position: this.current().start
          });
        }

        const keyToken = this.current();
        if (keyToken.type !== "identifier" && keyToken.type !== "literal") {
          throw createError("KIT_PARSE_OBJECT_KEY", "Object key must be an identifier or literal", {
            source: this.source,
            position: keyToken.start
          });
        }
        this.next();
        const key = String(keyToken.value);
        if (isBlockedMember(key)) {
          throw createError("KIT_BLOCKED_MEMBER", "Object key '" + key + "' is blocked", {
            source: this.source,
            position: keyToken.start
          });
        }
        if (seenKeys[key]) {
          throw createError("KIT_PARSE_OBJECT_DUPLICATE_KEY", "Duplicate object key '" + key + "'", {
            source: this.source,
            position: keyToken.start
          });
        }
        seenKeys[key] = true;
        this.expect(":");
        properties.push({ key, value: this.parseAssignment() });
      } while (this.match(","));
      const objectClose = this.expect("}");
      return node("ObjectExpression", objectStart, objectClose.end, { properties });
    }
    return node("ObjectExpression", objectStart, this.previous().end, { properties });
  }

  throw createError(
    "KIT_PARSE_UNEXPECTED_TOKEN",
    "Unexpected token near '" + (token ? token.value : "<end>") + "'",
    { source: this.source, position: token ? token.start : this.source.length }
  );
};

function parseBinding(source) {
  source = String(source == null ? "" : source);
  return new Parser(lex(source), source, { allowAssignment: false }).parseSingleExpression();
}

function parseAction(source) {
  source = String(source == null ? "" : source);
  return new Parser(lex(source), source, { allowAssignment: true }).parseProgram();
}

module.exports = {
  Parser,
  node,
  isWritableAst,
  parseBinding,
  parseAction
};

},
5: function(module, exports, __require){
"use strict";

const { createError } = __require(0);
const { isBlockedMember } = __require(1);
const { lex } = __require(3);
const { node, isWritableAst, parseBinding } = __require(4);
const { scanTopLevel } = __require(2);

function decodeQuotedKey(raw) {
  const tokens = lex(raw);
  if (tokens.length !== 2 || tokens[0].type !== "literal" ||
      typeof tokens[0].value !== "string") {
    throw createError("KIT_MAP_KEY", "Invalid quoted map key '" + raw + "'", { source: raw });
  }
  return tokens[0].value;
}

function validateMapKey(rawKey, key, options) {
  options = options || {};
  if (isBlockedMember(key)) {
    throw createError("KIT_BLOCKED_MEMBER", "Map key '" + key + "' is blocked", { key });
  }

  if (rawKey[0] === "'" || rawKey[0] === '"') return;

  if (options.classMap) {
    if (!/^-?[A-Za-z0-9_./-]+$/.test(key)) {
      throw createError(
        "KIT_CLASS_KEY_QUOTE_REQUIRED",
        "Class key '" + key + "' must be quoted because it contains reserved characters",
        { key }
      );
    }
    return;
  }

  if (!/^-{0,2}[A-Za-z_][A-Za-z0-9_.-]*$/.test(key)) {
    throw createError(
      "KIT_MAP_KEY_QUOTE_REQUIRED",
      "Map key '" + key + "' must be a static bare key or quoted string",
      { key }
    );
  }
}

function parseNamedMap(source, options) {
  source = String(source == null ? "" : source);
  options = options || {};

  const entries = [];
  let segmentStart = 0;
  let colon = -1;
  const seen = Object.create(null);

  function pushEntry(end) {
    const segment = source.slice(segmentStart, end).trim();
    if (!segment) {
      segmentStart = end + 1;
      colon = -1;
      return;
    }

    if (colon < segmentStart) {
      throw createError("KIT_MAP_MISSING_COLON", "Map entry is missing ':'", {
        source,
        segment,
        position: segmentStart
      });
    }

    const rawKey = source.slice(segmentStart, colon).trim();
    const expressionSource = source.slice(colon + 1, end).trim();
    if (!rawKey || !expressionSource) {
      throw createError("KIT_MAP_ENTRY", "Map key and expression are required", {
        source,
        segment,
        position: segmentStart
      });
    }

    const key = rawKey[0] === "'" || rawKey[0] === '"'
      ? decodeQuotedKey(rawKey)
      : rawKey;

    validateMapKey(rawKey, key, options);

    if (seen[key]) {
      throw createError("KIT_MAP_DUPLICATE_KEY", "Duplicate map key '" + key + "'", {
        source,
        key,
        position: segmentStart
      });
    }
    seen[key] = true;

    entries.push({
      key,
      source: expressionSource,
      ast: parseBinding(expressionSource)
    });

    segmentStart = end + 1;
    colon = -1;
  }

  scanTopLevel(source, (character, position) => {
    if (character === ":" && colon < segmentStart) colon = position;
    else if (character === ";") pushEntry(position);
  });

  if (source.slice(segmentStart).trim()) pushEntry(source.length);
  return node("NamedMap", 0, source.length, { entries });
}

function looksLikeClassMap(source) {
  source = String(source == null ? "" : source).trim();
  if (!source) return false;
  if (source[0] === "{" || source[0] === "[" || source[0] === "`") return false;

  let firstColon = -1;
  let firstQuestion = -1;
  scanTopLevel(source, (character, position) => {
    if (character === ":" && firstColon < 0) firstColon = position;
    else if (character === "?" && source[position + 1] !== "." &&
             source[position + 1] !== "?" && firstQuestion < 0) {
      firstQuestion = position;
    }
  });

  if (firstColon < 0 || (firstQuestion >= 0 && firstQuestion < firstColon)) return false;

  const rawKey = source.slice(0, firstColon).trim();
  if (!rawKey) return false;
  if (rawKey[0] === "'" || rawKey[0] === '"') return true;
  return /^-?[A-Za-z0-9_./-]+$/.test(rawKey);
}

function parseClassValue(source) {
  source = String(source == null ? "" : source).trim();
  if (looksLikeClassMap(source)) {
    return node("ClassMap", 0, source.length, {
      map: parseNamedMap(source, { classMap: true })
    });
  }
  return node("ClassExpression", 0, source.length, {
    ast: parseBinding(source)
  });
}

function parseWritablePath(source) {
  source = String(source == null ? "" : source).trim();
  const ast = parseBinding(source);
  if (!isWritableAst(ast)) {
    throw createError("KIT_MODEL_PATH", "Writable path must be an identifier/member path", {
      source
    });
  }
  return node("WritablePath", 0, source.length, { ast });
}

function parseIdentity(source) {
  source = String(source == null ? "" : source).trim();
  if (!source) {
    throw createError("KIT_IDENTITY_EMPTY", "Identity literal cannot be empty", { source });
  }
  return node("IdentityLiteral", 0, source.length, { value: source });
}

function parseIterator(source) {
  source = String(source == null ? "" : source).trim();
  const match = /^(\$[A-Za-z_][A-Za-z0-9_]*)\s*(?:,\s*(\$[A-Za-z_][A-Za-z0-9_]*)\s*)?\s+of\s+([\s\S]+)$/.exec(source);
  if (!match) {
    throw createError(
      "KIT_ITERATOR_PARSE",
      "Iterator must use '$item, $index of collection' syntax",
      { source }
    );
  }

  return node("IteratorExpression", 0, source.length, {
    itemName: match[1],
    indexName: match[2] || "",
    collectionSource: match[3].trim(),
    collectionAst: parseBinding(match[3].trim())
  });
}

module.exports = {
  decodeQuotedKey,
  parseNamedMap,
  looksLikeClassMap,
  parseClassValue,
  parseWritablePath,
  parseIdentity,
  parseIterator
};

},
6: function(module, exports, __require){
"use strict";

const { createError } = __require(0);
const {
  RESERVED_ASSIGNMENT_ROOTS,
  hasOwn,
  isBlockedMember
} = __require(1);

function createObjectEnvironment(stateObject, options) {
  options = options || {};
  const state = stateObject || Object.create(null);
  const contexts = options.contexts || Object.create(null);
  const aliases = options.aliases || Object.create(null);
  const services = options.services || Object.create(null);
  const readonlyRoots = options.readonlyRoots || RESERVED_ASSIGNMENT_ROOTS;

  return {
    resolve(name) {
      if (hasOwn(contexts, name)) {
        return { found: true, value: contexts[name], owner: null, readonly: true, kind: "context" };
      }
      if (hasOwn(aliases, name)) {
        return { found: true, value: aliases[name], owner: aliases[name], readonly: true, kind: "alias" };
      }
      if (name === "kit") {
        return { found: true, value: services, owner: null, readonly: true, kind: "service" };
      }
      if (hasOwn(state, name)) {
        return { found: true, value: state[name], owner: state, readonly: false, kind: "state" };
      }
      return { found: false, value: undefined, owner: null, readonly: false, kind: "missing" };
    },

    assign(name, value) {
      if (!name || readonlyRoots[name] || name[0] === "$" || name === "kit") {
        throw createError("KIT_READONLY_CONTEXT", "Cannot assign to runtime context '" + name + "'", {
          name
        });
      }
      state[name] = value;
      if (typeof options.onMutation === "function") {
        options.onMutation({ type: "identifier", name, owner: state, value });
      }
      return value;
    },

    canWriteMember(reference) {
      if (!reference || !reference.owner || reference.key == null) return false;
      if (isBlockedMember(reference.key)) return false;
      if (readonlyRoots[reference.root]) return false;
      if (reference.owner === services || reference.owner === contexts) return false;
      if (typeof globalThis !== "undefined" && reference.owner === globalThis) return false;
      if (reference.owner && reference.owner.nodeType) return false;
      if (typeof Map !== "undefined" && reference.owner instanceof Map) return false;
      if (typeof Set !== "undefined" && reference.owner instanceof Set) return false;

      const descriptor = Object.getOwnPropertyDescriptor(reference.owner, reference.key);
      if (descriptor && (
        descriptor.writable === false ||
        typeof descriptor.value === "function" ||
        descriptor.get || descriptor.set
      )) return false;

      return true;
    },

    onMutation: options.onMutation || null,
    onEffect: options.onEffect || null,
    defaultThis: options.defaultThis || state,
    state
  };
}

module.exports = {
  createObjectEnvironment
};

},
7: function(module, exports, __require){
"use strict";

const { createError } = __require(0);
const {
  MODES,
  RESERVED_ASSIGNMENT_ROOTS,
  hasOwn,
  isThenable,
  isBlockedMember,
  rootIdentifier
} = __require(1);

const DEFAULT_EVALUATION_BUDGET = 10000;
const DEFAULT_CALL_DEPTH = 64;

function EvaluationContext(environment, options) {
  options = options || {};
  this.environment = environment;
  this.mode = options.mode || MODES.BINDING;
  this.evaluationBudget = options.evaluationBudget || DEFAULT_EVALUATION_BUDGET;
  this.callDepthLimit = options.callDepthLimit || DEFAULT_CALL_DEPTH;
  this.evaluationCount = 0;
  this.callDepth = 0;
  this.effects = [];
  this.mutations = [];
}

function normalizeResolution(value, name) {
  if (value && typeof value === "object" && hasOwn(value, "value") &&
      (hasOwn(value, "found") || hasOwn(value, "owner") || hasOwn(value, "readonly"))) {
    if (!hasOwn(value, "found")) value.found = true;
    return value;
  }
  return {
    found: value !== undefined,
    value,
    owner: null,
    readonly: false,
    name
  };
}

function resolveIdentifier(name, context) {
  const environment = context.environment;
  let resolution;

  if (environment && typeof environment.resolve === "function") {
    resolution = normalizeResolution(environment.resolve(name), name);
  } else if (environment && typeof environment.get === "function") {
    resolution = normalizeResolution(environment.get(name), name);
  } else if (environment && hasOwn(environment, name)) {
    resolution = { found: true, value: environment[name], owner: environment, readonly: false };
  } else {
    resolution = { found: false, value: undefined, owner: null, readonly: false };
  }

  resolution.name = name;
  return resolution;
}

function evaluatePropertyKey(ast, context) {
  let key = ast.computed
    ? evaluateAst(ast.property, context)
    : ast.property.value;

  key = typeof key === "symbol" ? key : String(key);
  if (typeof key === "string" && isBlockedMember(key)) {
    throw createError("KIT_BLOCKED_MEMBER", "Access to member '" + key + "' is blocked", { key });
  }
  return key;
}

function evaluateReference(ast, context) {
  if (ast.type === "Identifier") {
    const resolution = resolveIdentifier(ast.name, context);
    return {
      value: resolution.value,
      owner: resolution.owner,
      key: ast.name,
      readonly: !!resolution.readonly,
      kind: resolution.kind || "identifier",
      root: ast.name,
      found: !!resolution.found,
      boundary: resolution.boundary || null
    };
  }

  if (ast.type === "MemberExpression") {
    const object = evaluateAst(ast.object, context);
    if (object == null) {
      if (ast.optional) {
        return {
          value: undefined,
          owner: null,
          key: null,
          readonly: true,
          kind: "optional",
          root: rootIdentifier(ast),
          found: false,
          boundary: null
        };
      }
      throw createError("KIT_NULL_MEMBER_ACCESS", "Cannot read a member from null or undefined", {
        root: rootIdentifier(ast),
        position: ast.start
      });
    }

    const key = evaluatePropertyKey(ast, context);
    return {
      value: object[key],
      owner: object,
      key,
      readonly: false,
      kind: "member",
      root: rootIdentifier(ast),
      found: key in Object(object),
      boundary: null
    };
  }

  return {
    value: evaluateAst(ast, context),
    owner: null,
    key: null,
    readonly: true,
    kind: "value",
    root: rootIdentifier(ast),
    found: true,
    boundary: null
  };
}

function writeReference(ast, value, context) {
  const environment = context.environment;

  if (ast.type === "Identifier") {
    if (!environment || typeof environment.assign !== "function") {
      throw createError("KIT_ENVIRONMENT_ASSIGN", "Environment does not implement assign(name, value)", {
        name: ast.name
      });
    }
    const assigned = environment.assign(ast.name, value);
    context.mutations.push({ type: "identifier", name: ast.name, value });
    return assigned;
  }

  if (ast.type !== "MemberExpression" || ast.optional) {
    throw createError("KIT_INVALID_ASSIGNMENT_TARGET", "Invalid assignment target", {
      position: ast.start
    });
  }

  const reference = evaluateReference(ast, context);
  if (!reference.owner || reference.key == null) {
    throw createError("KIT_READONLY_PATH", "Cannot assign to unresolved path", {
      root: reference.root
    });
  }

  if (RESERVED_ASSIGNMENT_ROOTS[reference.root]) {
    throw createError("KIT_READONLY_PATH", "Cannot assign through read-only root '" + reference.root + "'", {
      root: reference.root
    });
  }

  if (environment && typeof environment.canWriteMember === "function" &&
      environment.canWriteMember(reference) !== true) {
    throw createError("KIT_READONLY_PATH", "Cannot assign to path rooted at '" + reference.root + "'", {
      root: reference.root,
      key: reference.key
    });
  }

  reference.owner[reference.key] = value;
  const mutation = {
    type: "member",
    root: reference.root,
    owner: reference.owner,
    key: reference.key,
    value
  };
  context.mutations.push(mutation);
  if (environment && typeof environment.onMutation === "function") {
    environment.onMutation(mutation);
  }
  return value;
}

function registerEffect(result, context) {
  if (!isThenable(result)) return;
  if (!context.effects.includes(result)) context.effects.push(result);
  if (context.environment && typeof context.environment.onEffect === "function") {
    context.environment.onEffect(result);
  }
}

function evaluateAst(ast, context) {
  context.evaluationCount++;
  if (context.evaluationCount > context.evaluationBudget) {
    throw createError("KIT_EVALUATION_BUDGET", "Expression evaluation budget exceeded", {
      limit: context.evaluationBudget
    });
  }

  if (!ast) return undefined;

  switch (ast.type) {
    case "Literal":
      return ast.value;

    case "Identifier":
      return resolveIdentifier(ast.name, context).value;

    case "TemplateLiteral": {
      let text = ast.quasis[0] || "";
      for (let i = 0; i < ast.expressions.length; i++) {
        const interpolation = evaluateAst(ast.expressions[i], context);
        if (isThenable(interpolation)) {
          throw createError("KIT_ASYNC_BINDING", "Template interpolation cannot resolve a Promise");
        }
        text += interpolation == null ? "" : String(interpolation);
        text += ast.quasis[i + 1] || "";
      }
      return text;
    }

    case "ArrayExpression":
      return ast.elements.map((element) => evaluateAst(element, context));

    case "ObjectExpression": {
      const object = Object.create(null);
      for (const property of ast.properties) {
        if (isBlockedMember(property.key)) {
          throw createError("KIT_BLOCKED_MEMBER", "Object key '" + property.key + "' is blocked");
        }
        object[property.key] = evaluateAst(property.value, context);
      }
      return object;
    }

    case "UnaryExpression": {
      const unaryValue = evaluateAst(ast.argument, context);
      if (ast.operator === "!") return !unaryValue;
      if (ast.operator === "-") return -unaryValue;
      if (ast.operator === "+") return +unaryValue;
      throw createError("KIT_UNKNOWN_OPERATOR", "Unknown unary operator '" + ast.operator + "'");
    }

    case "LogicalExpression": {
      const left = evaluateAst(ast.left, context);
      if (ast.operator === "&&") return left ? evaluateAst(ast.right, context) : left;
      if (ast.operator === "||") return left ? left : evaluateAst(ast.right, context);
      if (ast.operator === "??") return left == null ? evaluateAst(ast.right, context) : left;
      throw createError("KIT_UNKNOWN_OPERATOR", "Unknown logical operator '" + ast.operator + "'");
    }

    case "BinaryExpression": {
      const left = evaluateAst(ast.left, context);
      const right = evaluateAst(ast.right, context);
      if (ast.operator === "+") return left + right;
      if (ast.operator === "-") return left - right;
      if (ast.operator === "*") return left * right;
      if (ast.operator === "/") return left / right;
      if (ast.operator === "%") return left % right;
      if (ast.operator === ">") return left > right;
      if (ast.operator === ">=") return left >= right;
      if (ast.operator === "<") return left < right;
      if (ast.operator === "<=") return left <= right;
      if (ast.operator === "===") return left === right;
      if (ast.operator === "!==") return left !== right;
      throw createError("KIT_UNKNOWN_OPERATOR", "Unknown binary operator '" + ast.operator + "'");
    }

    case "ConditionalExpression":
      return evaluateAst(ast.test, context)
        ? evaluateAst(ast.consequent, context)
        : evaluateAst(ast.alternate, context);

    case "MemberExpression":
      return evaluateReference(ast, context).value;

    case "AssignmentExpression":
      return writeReference(ast.left, evaluateAst(ast.right, context), context);

    case "CallExpression": {
      const reference = evaluateReference(ast.callee, context);
      if (reference.value == null && ast.optional) return undefined;
      if (typeof reference.value !== "function") {
        throw createError("KIT_NOT_CALLABLE", "Expression target is not callable", {
          root: reference.root,
          key: reference.key
        });
      }

      if (context.callDepth >= context.callDepthLimit) {
        throw createError("KIT_CALL_DEPTH", "Expression call-depth limit exceeded", {
          limit: context.callDepthLimit
        });
      }

      const args = ast.arguments.map((argument) => evaluateAst(argument, context));
      context.callDepth++;
      try {
        const thisArg = reference.owner ||
          (context.environment && context.environment.defaultThis) ||
          undefined;
        const result = reference.value.apply(thisArg, args);
        if (context.mode === MODES.ACTION) registerEffect(result, context);
        return result;
      } finally {
        context.callDepth--;
      }
    }

    case "Program": {
      let value;
      for (const expression of ast.body) {
        value = evaluateAst(expression, context);
        if (context.mode === MODES.ACTION) registerEffect(value, context);
      }
      return value;
    }

    default:
      throw createError("KIT_UNKNOWN_AST", "Unknown AST node '" + ast.type + "'");
  }
}

module.exports = {
  DEFAULT_EVALUATION_BUDGET,
  DEFAULT_CALL_DEPTH,
  EvaluationContext,
  resolveIdentifier,
  evaluateReference,
  writeReference,
  evaluateAst
};

},
8: function(module, exports, __require){
"use strict";

const { KitworkExpressionError, createError } = __require(0);
const { MODES, normalizeMode, isThenable } = __require(1);
const { lex } = __require(3);
const { parseBinding, parseAction } = __require(4);
const {
  parseNamedMap,
  parseClassValue,
  parseWritablePath,
  parseIdentity,
  parseIterator
} = __require(5);
const { createObjectEnvironment } = __require(6);
const { EvaluationContext, evaluateAst, writeReference } = __require(7);

const DEFAULT_CACHE_ENTRIES = 2048;

function createExpressionEngine(options) {
  options = options || {};
  const cache = new Map();
  const maxCacheEntries = options.maxCacheEntries || DEFAULT_CACHE_ENTRIES;

  function cacheSet(key, value) {
    if (cache.size >= maxCacheEntries) {
      const first = cache.keys().next();
      if (!first.done) cache.delete(first.value);
    }
    cache.set(key, value);
    return value;
  }

  function compile(mode, source) {
    mode = normalizeMode(mode);
    source = String(source == null ? "" : source).trim();
    const key = mode + "\u0000" + source;
    if (cache.has(key)) return cache.get(key);

    if (!source && mode !== MODES.NAMED_MAP) {
      throw createError("KIT_EMPTY_EXPRESSION", "Directive expression cannot be empty", {
        mode,
        source
      });
    }

    let ast;
    if (mode === MODES.BINDING) ast = parseBinding(source);
    else if (mode === MODES.ACTION) ast = parseAction(source);
    else if (mode === MODES.NAMED_MAP) ast = parseNamedMap(source);
    else if (mode === MODES.CLASS_VALUE) ast = parseClassValue(source);
    else if (mode === MODES.WRITABLE_PATH) ast = parseWritablePath(source);
    else if (mode === MODES.IDENTITY) ast = parseIdentity(source);
    else if (mode === MODES.ITERATOR) ast = parseIterator(source);
    else throw createError("KIT_PARSE_MODE", "Unknown parser mode '" + mode + "'", { mode });

    return cacheSet(key, { mode, source, ast });
  }

  function createContext(compiled, environment, executeOptions) {
    executeOptions = executeOptions || {};
    return new EvaluationContext(environment, {
      mode: compiled.mode,
      evaluationBudget: executeOptions.evaluationBudget || options.evaluationBudget,
      callDepthLimit: executeOptions.callDepthLimit || options.callDepthLimit
    });
  }

  function rejectAsync(value, message, details) {
    if (isThenable(value)) throw createError("KIT_ASYNC_BINDING", message, details || null);
    return value;
  }

  function execute(compiled, environment, executeOptions) {
    if (!compiled || !compiled.mode || !compiled.ast) {
      throw createError("KIT_COMPILED_EXPRESSION", "Invalid compiled expression");
    }

    const context = createContext(compiled, environment, executeOptions);
    let value;

    if (compiled.mode === MODES.BINDING) {
      value = rejectAsync(
        evaluateAst(compiled.ast, context),
        "Binding expression cannot resolve a Promise",
        { source: compiled.source }
      );
    } else if (compiled.mode === MODES.ACTION) {
      value = evaluateAst(compiled.ast, context);
    } else if (compiled.mode === MODES.NAMED_MAP) {
      value = compiled.ast.entries.map((entry) => ({
        key: entry.key,
        value: rejectAsync(
          evaluateAst(entry.ast, context),
          "Named map value cannot resolve a Promise",
          { source: entry.source, key: entry.key }
        )
      }));
    } else if (compiled.mode === MODES.CLASS_VALUE) {
      if (compiled.ast.type === "ClassMap") {
        value = compiled.ast.map.entries.map((entry) => ({
          key: entry.key,
          value: rejectAsync(
            evaluateAst(entry.ast, context),
            "Class map value cannot resolve a Promise",
            { source: entry.source, key: entry.key }
          )
        }));
      } else {
        value = rejectAsync(
          evaluateAst(compiled.ast.ast, context),
          "Class expression cannot resolve a Promise",
          { source: compiled.source }
        );
      }
    } else if (compiled.mode === MODES.WRITABLE_PATH) {
      value = evaluateAst(compiled.ast.ast, context);
    } else if (compiled.mode === MODES.IDENTITY) {
      value = compiled.ast.value;
    } else if (compiled.mode === MODES.ITERATOR) {
      value = {
        itemName: compiled.ast.itemName,
        indexName: compiled.ast.indexName,
        collection: rejectAsync(
          evaluateAst(compiled.ast.collectionAst, context),
          "Iterator collection cannot resolve a Promise",
          { source: compiled.ast.collectionSource }
        )
      };
    }

    return {
      value,
      effects: context.effects.slice(),
      mutations: context.mutations.slice(),
      evaluationCount: context.evaluationCount
    };
  }

  function evaluate(compiled, environment, executeOptions) {
    return execute(compiled, environment, executeOptions).value;
  }

  function assign(compiledWritablePath, environment, value, executeOptions) {
    if (!compiledWritablePath || compiledWritablePath.mode !== MODES.WRITABLE_PATH) {
      throw createError("KIT_MODEL_PATH", "assign() requires a writable-path compiled expression");
    }

    const context = new EvaluationContext(environment, {
      mode: MODES.ACTION,
      evaluationBudget: executeOptions && (executeOptions.evaluationBudget || options.evaluationBudget),
      callDepthLimit: executeOptions && (executeOptions.callDepthLimit || options.callDepthLimit)
    });

    const assigned = writeReference(compiledWritablePath.ast.ast, value, context);
    return {
      value: assigned,
      effects: context.effects.slice(),
      mutations: context.mutations.slice(),
      evaluationCount: context.evaluationCount
    };
  }

  return Object.freeze({
    modes: MODES,
    compile,
    execute,
    evaluate,
    assign,
    clearCache() { cache.clear(); },
    cacheSize() { return cache.size; },
    createObjectEnvironment
  });
}

module.exports = Object.freeze({
  MODES,
  KitworkExpressionError,
  createExpressionEngine,
  createObjectEnvironment,

  // Milestone-only test exports. AST remains non-normative.
  testing: Object.freeze({
    lex,
    parseBinding,
    parseAction,
    parseNamedMap,
    parseClassValue,
    parseWritablePath,
    parseIterator
  })
});

},
9: function(module, exports, __require){
/*
 * Kitwork Client Runtime — Lexical Environment Adapter M1
 *
 * Bridges the expression engine to the runtime ownership model without making
 * the expression package depend on DOM traversal, component registries, or the
 * render scheduler.
 */
(function (root, factory) {
  "use strict";

  var expressionApi = null;
  if (typeof module === "object" && module && module.exports) {
    expressionApi = __require(8);
    module.exports = factory(expressionApi);
    return;
  }

  expressionApi = root.KitworkExpression;
  var api = factory(expressionApi);
  try {
    Object.defineProperty(root, "KitworkLexicalEnvironment", {
      value: api,
      configurable: true,
      enumerable: false,
      writable: false
    });
  } catch (_) {
    root.KitworkLexicalEnvironment = api;
  }
})(typeof globalThis !== "undefined" ? globalThis : this, function (expressionApi) {
  "use strict";

  if (!expressionApi) throw new Error("KitworkExpression must be loaded first");

  var KitworkExpressionError = expressionApi.KitworkExpressionError;

  var BLOCKED = Object.create(null);
  (
    "constructor prototype __proto__ __defineGetter__ __defineSetter__ " +
    "__lookupGetter__ __lookupSetter__ ownerDocument defaultView contentWindow " +
    "window globalThis top parent self"
  ).split(/\s+/).forEach(function (key) {
    if (key) BLOCKED[key] = true;
  });

  var READONLY_MEMBER_ROOTS = Object.create(null);
  "$element $host $event $refs $parent $index kit".split(/\s+/).forEach(function (key) {
    READONLY_MEMBER_ROOTS[key] = true;
  });

  var RESERVED_INSTANCE_KEYS = Object.create(null);
  "$host $refs $parent $alias $app".split(/\s+/).forEach(function (key) {
    RESERVED_INSTANCE_KEYS[key] = true;
  });

  function hasOwn(object, key) {
    return object != null && Object.prototype.hasOwnProperty.call(object, key);
  }

  function fail(code, message, details) {
    throw new KitworkExpressionError(code, message, details || null);
  }

  function aliasGet(aliases, name) {
    if (!aliases) return undefined;
    if (typeof aliases.get === "function") return aliases.get(name);
    return hasOwn(aliases, name) ? aliases[name] : undefined;
  }

  function aliasHas(aliases, name) {
    if (!aliases) return false;
    if (typeof aliases.has === "function") return aliases.has(name);
    return hasOwn(aliases, name);
  }

  function propertyDescriptor(object, key) {
    var current = object;
    while (current) {
      var descriptor = Object.getOwnPropertyDescriptor(current, key);
      if (descriptor) return descriptor;
      current = Object.getPrototypeOf(current);
    }
    return null;
  }

  function isBlocked(key) {
    return typeof key === "string" && BLOCKED[key] === true;
  }

  function normalizeScopeEntry(entry, fallbackBoundary) {
    if (!entry) return null;
    if (entry.scope) {
      return {
        scope: entry.scope,
        boundary: entry.boundary || entry.element || fallbackBoundary || null
      };
    }
    return { scope: entry, boundary: fallbackBoundary || null };
  }

  function createLexicalEnvironment(options) {
    options = options || {};

    var contexts = options.contexts || Object.create(null);
    var aliases = options.aliases || null;
    var loopFrames = options.loopFrames || [];
    var component = options.component || null;
    var componentBoundary = options.componentBoundary || options.host || null;
    var appScope = options.appScope || Object.create(null);
    var appBoundary = options.appBoundary || options.appRoot || null;
    var kitSurface = options.kit || Object.create(null);
    var localScopes = [];

    (options.localScopes || []).forEach(function (entry) {
      var normalized = normalizeScopeEntry(entry, null);
      if (normalized) localScopes.push(normalized);
    });

    function dirty(boundary, mutation) {
      if (typeof options.onDirty === "function") {
        options.onDirty(boundary || componentBoundary || appBoundary, mutation || null);
      }
    }

    function resolve(name) {
      if (hasOwn(contexts, name)) {
        return {
          found: true,
          value: contexts[name],
          owner: null,
          readonly: true,
          kind: "context",
          boundary: null
        };
      }

      if (name === "kit") {
        return {
          found: true,
          value: kitSurface,
          owner: null,
          readonly: true,
          kind: "service",
          boundary: null
        };
      }

      if (name && name[0] === "$" && aliasHas(aliases, name)) {
        var alias = aliasGet(aliases, name);
        var instance = alias && alias.instance ? alias.instance : alias;
        return {
          found: true,
          value: instance,
          owner: instance,
          readonly: true,
          kind: "alias",
          boundary: alias && alias.host ? alias.host : null
        };
      }

      for (var i = 0; i < loopFrames.length; i++) {
        if (hasOwn(loopFrames[i], name)) {
          return {
            found: true,
            value: loopFrames[i][name],
            owner: loopFrames[i],
            readonly: name === "$index",
            kind: "loop",
            boundary: null
          };
        }
      }

      for (var j = 0; j < localScopes.length; j++) {
        if (hasOwn(localScopes[j].scope, name)) {
          return {
            found: true,
            value: localScopes[j].scope[name],
            owner: localScopes[j].scope,
            readonly: false,
            kind: "local",
            boundary: localScopes[j].boundary
          };
        }
      }

      if (component && name in component) {
        return {
          found: true,
          value: component[name],
          owner: component,
          readonly: false,
          kind: "component",
          boundary: componentBoundary
        };
      }

      if (hasOwn(appScope, name)) {
        return {
          found: true,
          value: appScope[name],
          owner: appScope,
          readonly: false,
          kind: "app",
          boundary: appBoundary
        };
      }

      return {
        found: false,
        value: undefined,
        owner: null,
        readonly: false,
        kind: "missing",
        boundary: null
      };
    }

    function assertWritableComponentKey(key) {
      if (!component) return;
      if (RESERVED_INSTANCE_KEYS[key]) {
        fail("KIT_COMPONENT_STATE_COLLISION", "Cannot overwrite runtime metadata '" + key + "'", {
          key: key
        });
      }
      var descriptor = propertyDescriptor(component, key);
      if (descriptor && (
        typeof descriptor.value === "function" ||
        descriptor.get || descriptor.set || descriptor.writable === false
      )) {
        fail("KIT_COMPONENT_STATE_COLLISION", "Cannot overwrite component method/accessor '" + key + "'", {
          key: key
        });
      }
    }

    function assign(name, value) {
      if (!name || name === "kit" || name[0] === "$" || hasOwn(contexts, name)) {
        fail("KIT_READONLY_CONTEXT", "Cannot assign to runtime context '" + name + "'", {
          name: name
        });
      }

      for (var i = 0; i < localScopes.length; i++) {
        if (hasOwn(localScopes[i].scope, name)) {
          localScopes[i].scope[name] = value;
          dirty(localScopes[i].boundary, { type: "identifier", name: name, value: value });
          return value;
        }
      }

      if (component && name in component) {
        assertWritableComponentKey(name);
        component[name] = value;
        dirty(componentBoundary, { type: "identifier", name: name, value: value });
        return value;
      }

      if (hasOwn(appScope, name)) {
        appScope[name] = value;
        dirty(appBoundary, { type: "identifier", name: name, value: value });
        return value;
      }

      if (localScopes.length) {
        localScopes[0].scope[name] = value;
        dirty(localScopes[0].boundary, { type: "identifier", name: name, value: value });
        return value;
      }

      if (component) {
        assertWritableComponentKey(name);
        component[name] = value;
        dirty(componentBoundary, { type: "identifier", name: name, value: value });
        return value;
      }

      appScope[name] = value;
      dirty(appBoundary, { type: "identifier", name: name, value: value });
      return value;
    }

    function canWriteMember(reference) {
      if (!reference || !reference.owner || reference.key == null) return false;
      if (isBlocked(reference.key)) return false;
      if (READONLY_MEMBER_ROOTS[reference.root]) return false;
      if (reference.owner === kitSurface || reference.owner === contexts) return false;
      if (typeof globalThis !== "undefined" && reference.owner === globalThis) return false;
      if (reference.owner && reference.owner.nodeType) return false;
      if (typeof Map !== "undefined" && reference.owner instanceof Map) return false;
      if (typeof Set !== "undefined" && reference.owner instanceof Set) return false;

      if (reference.owner === component) {
        assertWritableComponentKey(String(reference.key));
      }

      var descriptor = propertyDescriptor(reference.owner, reference.key);
      if (descriptor && (
        descriptor.writable === false ||
        typeof descriptor.value === "function" ||
        descriptor.get || descriptor.set
      )) return false;

      return true;
    }

    function onMutation(mutation) {
      if (!mutation || mutation.type !== "member") return;
      var rootResolution = mutation.root ? resolve(mutation.root) : null;
      dirty(rootResolution && rootResolution.boundary, mutation);
    }

    return {
      resolve: resolve,
      assign: assign,
      canWriteMember: canWriteMember,
      onMutation: onMutation,
      onEffect: typeof options.onEffect === "function" ? options.onEffect : null,
      defaultThis: component || appScope,

      // Runtime integration/debug metadata, not authored-expression surface.
      internal: {
        contexts: contexts,
        aliases: aliases,
        loopFrames: loopFrames,
        localScopes: localScopes,
        component: component,
        appScope: appScope,
        appBoundary: appBoundary,
        componentBoundary: componentBoundary
      }
    };
  }

  return Object.freeze({
    createLexicalEnvironment: createLexicalEnvironment
  });
});

},
10: function(module, exports, __require){
"use strict";

function hasOwn(object, key) {
  return object != null && Object.prototype.hasOwnProperty.call(object, key);
}

function isThenable(value) {
  return value != null && (typeof value === "object" || typeof value === "function") &&
    typeof value.then === "function";
}

function enqueueMicrotask(callback) {
  if (typeof queueMicrotask === "function") return queueMicrotask(callback);
  Promise.resolve().then(callback);
}

function createNullObject() {
  return Object.create(null);
}

function nodeContains(parent, child) {
  if (!parent || !child) return false;
  if (parent === child) return true;
  if (parent.nodeType === 1 && typeof parent.contains === "function") return parent.contains(child);
  var current = child.parentNode;
  while (current) {
    if (current === parent) return true;
    current = current.parentNode;
  }
  return false;
}

function nodeDepth(node) {
  var depth = 0;
  var current = node;
  while (current) {
    depth++;
    current = current.parentNode;
  }
  return depth;
}

function isElement(value) {
  return !!value && value.nodeType === 1;
}

function isNode(value) {
  return !!value && typeof value.nodeType === "number";
}

function toArray(value) {
  return Array.prototype.slice.call(value || []);
}

function cloneState(value, seen) {
  if (value == null || typeof value !== "object") return value;
  if (typeof structuredClone === "function") {
    try { return structuredClone(value); } catch (_) { /* fall through */ }
  }

  seen = seen || new Map();
  if (seen.has(value)) return seen.get(value);

  if (value instanceof Date) return new Date(value.getTime());
  if (value instanceof RegExp) return new RegExp(value.source, value.flags);
  if (Array.isArray(value)) {
    var array = [];
    seen.set(value, array);
    for (var i = 0; i < value.length; i++) array[i] = cloneState(value[i], seen);
    return array;
  }

  var prototype = Object.getPrototypeOf(value);
  if (prototype !== Object.prototype && prototype !== null) return value;

  var object = Object.create(prototype);
  seen.set(value, object);
  Object.keys(value).forEach(function (key) {
    object[key] = cloneState(value[key], seen);
  });
  return object;
}

function normalizeScalar(value) {
  if (value == null) return "";
  if (typeof value === "string" || typeof value === "number" || typeof value === "boolean" ||
      typeof value === "bigint") return String(value);
  return null;
}

function cssEscape(value) {
  if (typeof CSS !== "undefined" && CSS && typeof CSS.escape === "function") return CSS.escape(value);
  return String(value).replace(/[^A-Za-z0-9_-]/g, function (character) {
    return "\\" + character.charCodeAt(0).toString(16) + " ";
  });
}

function eventPath(event) {
  if (event && typeof event.composedPath === "function") return event.composedPath();
  var path = [];
  var current = event && event.target;
  while (current) {
    path.push(current);
    current = current.parentNode || current.host || null;
  }
  if (typeof window !== "undefined") path.push(window);
  return path;
}

module.exports = {
  hasOwn: hasOwn,
  isThenable: isThenable,
  enqueueMicrotask: enqueueMicrotask,
  createNullObject: createNullObject,
  nodeContains: nodeContains,
  nodeDepth: nodeDepth,
  isElement: isElement,
  isNode: isNode,
  toArray: toArray,
  cloneState: cloneState,
  normalizeScalar: normalizeScalar,
  cssEscape: cssEscape,
  eventPath: eventPath
};

},
11: function(module, exports, __require){
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

},
12: function(module, exports, __require){
"use strict";

/*
 * Runtime ownership records.
 *
 * These records are intentionally plain data. Subsystems keep their private
 * maps, while the records provide one stable contract for ownership and
 * cleanup across hydration, rendering, events and Drive integration.
 */

function createAppRecord(root, name) {
  return {
    root: root,
    name: name || "main",
    scope: Object.create(null),
    scopeInitialized: false,
    aliases: new Map(),
    bindings: new Set(),
    structures: new Set(),
    components: new Set(),
    persisted: new Map(),
    dirtyBoundaries: new Set(),
    pendingComponents: new Set(),
    removedNodes: new Set(),
    reactiveCache: new WeakMap(),
    observer: null,
    scheduled: false,
    rendering: false,
    renderAgain: false,
    initialized: false,
    destroyed: false,
    renderCount: 0,
    errorDepth: 0,
    lastMutation: null,
    cleanups: []
  };
}

function createNodeRecord(node, app) {
  return {
    node: node,
    app: app || null,
    hydrated: false,
    scope: null,
    scopeInitialized: false,
    component: null,
    refOwner: null,
    refName: "",
    bindings: new Map(),
    eventBindings: new Map(),
    structure: null,
    loopFrame: null,
    persistKey: "",
    composing: false,
    fresh: false,
    cleanups: [],
    destroyed: false
  };
}

function createComponentRecord(app, host, name, parent) {
  return {
    app: app,
    host: host,
    name: name,
    parent: parent || null,
    target: Object.create(null),
    instance: null,
    refs: Object.create(null),
    alias: "",
    definition: null,
    hostSeed: Object.create(null),
    activated: false,
    mounted: false,
    mounting: false,
    unmounting: false,
    destroyed: false,
    mountCleanup: null,
    pendingEffects: new Set(),
    cleanups: [],
    tasks: new Set()
  };
}

function createBindingRecord(options, element, attributeName, directiveName, contract, source, compiled) {
  // Support the runtime's positional constructor and an object-form constructor
  // used by tools/tests. Keeping this normalization here prevents every directive
  // subsystem from knowing the record layout.
  if (!options || !options.root) {
    options = options || {};
  } else {
    options = {
      app: options,
      element: element,
      attributeName: attributeName,
      directiveName: directiveName,
      name: directiveName,
      contract: contract,
      source: source,
      compiled: compiled,
      mode: contract && contract.mode,
      phase: contract && contract.phase
    };
  }

  return {
    app: options.app,
    element: options.element,
    attributeName: options.attributeName || "",
    directiveName: options.directiveName || options.name || "",
    name: options.name || options.directiveName || "",
    source: options.source || "",
    mode: options.mode || (options.contract && options.contract.mode) || "binding",
    compiled: options.compiled || null,
    contract: options.contract || null,
    phase: options.phase || (options.contract && options.contract.phase) || "content",
    boundary: options.boundary || null,
    ownerBoundary: options.boundary || null,
    lastValue: undefined,
    initialized: false,
    disabled: false,
    destroyed: false,

    // DOM ownership snapshots.
    initialClasses: null,
    ownedClasses: new Set(),
    ownedStyles: new Set(),
    styleOriginals: new Map(),
    ownedAttributes: new Set(),
    attributeOriginals: new Map(),
    authorHidden: undefined,

    // Model/event state.
    modelSeeded: false,
    eventType: "",
    modifiers: null,
    pendingCount: 0,
    busySnapshot: null,
    consumed: false,
    timer: null,
    throttleAt: 0,
    lastRun: 0,
    directCleanup: null,
    directListenerCleanup: null,
    cleanup: null
  };
}

module.exports = {
  createAppRecord: createAppRecord,
  createNodeRecord: createNodeRecord,
  createComponentRecord: createComponentRecord,
  createBindingRecord: createBindingRecord
};

},
13: function(module, exports, __require){
"use strict";

var utils = __require(10);
var enqueueMicrotask = utils.enqueueMicrotask;
var nodeContains = utils.nodeContains;
var isNode = utils.isNode;

function createScheduler(runtime) {
  function normalizeBoundary(app, boundary) {
    if (!boundary) return app.root;
    if (boundary.host && isNode(boundary.host)) return boundary.host;
    if (boundary.element && isNode(boundary.element)) return boundary.element;
    if (isNode(boundary)) return boundary;
    return app.root;
  }

  function invalidate(app, boundary, mutation) {
    if (!app || app.destroyed) return;
    var target = normalizeBoundary(app, boundary);
    if (!target || !target.isConnected || !nodeContains(app.root, target)) target = app.root;

    var existing = Array.from(app.dirtyBoundaries);
    for (var i = 0; i < existing.length; i++) {
      var current = existing[i];
      if (nodeContains(current, target)) return;
      if (nodeContains(target, current)) app.dirtyBoundaries.delete(current);
    }

    app.dirtyBoundaries.add(target);
    if (runtime.options && runtime.options.development && mutation) {
      app.lastMutation = mutation;
    }
    schedule(app);
  }

  function schedule(app) {
    if (!app || app.destroyed || app.scheduled) return;
    app.scheduled = true;
    enqueueMicrotask(function () {
      app.scheduled = false;
      flush(app);
    });
  }

  function flush(app) {
    if (!app || app.destroyed) return;
    if (app.rendering) {
      app.renderAgain = true;
      return;
    }

    app.rendering = true;
    try {
      var boundaries = Array.from(app.dirtyBoundaries);
      app.dirtyBoundaries.clear();
      if (!boundaries.length) boundaries.push(app.root);
      runtime.renderBoundaries(app, boundaries);
    } finally {
      app.rendering = false;
    }

    if (app.renderAgain || app.dirtyBoundaries.size) {
      app.renderAgain = false;
      schedule(app);
    }
  }

  return {
    invalidate: invalidate,
    schedule: schedule,
    flush: flush
  };
}

module.exports = {
  createScheduler: createScheduler
};

},
14: function(module, exports, __require){
"use strict";

var errors = __require(11);
var createRuntimeError = errors.createRuntimeError;

function createDirectiveRegistry() {
  var directives = new Map();

  function register(name, contract) {
    name = String(name || "").trim();
    if (!/^[a-z][a-z0-9-]*$/.test(name)) {
      throw createRuntimeError("KIT_DIRECTIVE_NAME", "Invalid directive name '" + name + "'", {
        directive: name
      });
    }
    if (!contract || typeof contract !== "object") {
      throw createRuntimeError("KIT_DIRECTIVE_CONTRACT", "Directive '" + name + "' requires a contract");
    }
    directives.set(name, Object.assign({ name: name, phase: "content" }, contract));
    return contract;
  }

  return {
    register: register,
    get: function (name) { return directives.get(name); },
    has: function (name) { return directives.has(name); },
    names: function () { return Array.from(directives.keys()); },
    entries: function () { return Array.from(directives.entries()); }
  };
}

module.exports = {
  createDirectiveRegistry: createDirectiveRegistry
};

},
15: function(module, exports, __require){
"use strict";

var utils = __require(10);
var errors = __require(11);
var hasOwn = utils.hasOwn;
var createNullObject = utils.createNullObject;
var createRuntimeError = errors.createRuntimeError;

var BLOCKED_MEMBERS = createNullObject();
(
  "constructor prototype __proto__ __defineGetter__ __defineSetter__ " +
  "__lookupGetter__ __lookupSetter__ ownerDocument defaultView contentWindow " +
  "window globalThis top parent self"
).split(/\s+/).forEach(function (name) {
  if (name) BLOCKED_MEMBERS[name] = true;
});

var RESERVED_NAMES = createNullObject();
(
  "runtime component service start destroy render onError internal dev " +
  "module modules has get set scope scopeFor compile run"
).split(/\s+/).forEach(function (name) {
  if (name) RESERVED_NAMES[name] = true;
});

function validName(name) {
  return /^[A-Za-z][A-Za-z0-9]*$/.test(name) && !RESERVED_NAMES[name] && !BLOCKED_MEMBERS[name];
}

function createServiceRegistry(kit) {
  var services = new Map();
  var grants = new Map();
  var surfaces = new Map();

  function createSurface(name) {
    if (surfaces.has(name)) return surfaces.get(name);
    var surface = new Proxy(createNullObject(), {
      get: function (_, member) {
        var allowed = grants.get(name);
        if (!allowed || !allowed.has(member)) return undefined;
        var service = services.get(name);
        if (service == null) return undefined;
        var value = service[member];
        return typeof value === "function" ? value.bind(service) : value;
      },
      has: function (_, member) {
        var allowed = grants.get(name);
        return !!allowed && allowed.has(member);
      },
      ownKeys: function () {
        var allowed = grants.get(name);
        return allowed ? Array.from(allowed) : [];
      },
      getOwnPropertyDescriptor: function (_, member) {
        var allowed = grants.get(name);
        if (!allowed || !allowed.has(member)) return undefined;
        return { configurable: true, enumerable: true };
      },
      set: function () { return false; },
      defineProperty: function () { return false; },
      deleteProperty: function () { return false; },
      setPrototypeOf: function () { return false; },
      getPrototypeOf: function () { return null; }
    });
    surfaces.set(name, surface);
    return surface;
  }

  var publicSurface = new Proxy(createNullObject(), {
    get: function (_, name) {
      if (typeof name !== "string" || !grants.has(name)) return undefined;
      return createSurface(name);
    },
    has: function (_, name) { return grants.has(name); },
    ownKeys: function () { return Array.from(grants.keys()); },
    getOwnPropertyDescriptor: function (_, name) {
      if (!grants.has(name)) return undefined;
      return { configurable: true, enumerable: true };
    },
    set: function () { return false; },
    defineProperty: function () { return false; },
    deleteProperty: function () { return false; },
    setPrototypeOf: function () { return false; },
    getPrototypeOf: function () { return null; }
  });

  function register(name, implementation, options) {
    name = String(name || "");
    if (!validName(name)) {
      throw createRuntimeError("KIT_SERVICE_NAME", "Invalid or reserved service name '" + name + "'", {
        service: name
      });
    }

    services.set(name, implementation);
    kit[name] = implementation;

    var allowed = new Set();
    var members = options && options.expression;
    if (Array.isArray(members)) {
      members.forEach(function (member) {
        member = String(member || "");
        if (/^[A-Za-z_$][A-Za-z0-9_$]*$/.test(member) && !BLOCKED_MEMBERS[member]) {
          allowed.add(member);
        }
      });
    }

    if (allowed.size) grants.set(name, allowed);
    else grants.delete(name);
    surfaces.delete(name);
    return implementation;
  }

  function service(name) {
    return services.get(String(name || ""));
  }

  return {
    register: register,
    get: service,
    publicSurface: publicSurface,
    services: services,
    grants: grants
  };
}

module.exports = {
  createServiceRegistry: createServiceRegistry
};

},
16: function(module, exports, __require){
"use strict";

var utils = __require(10);
var errors = __require(11);
var records = __require(12);
var hasOwn = utils.hasOwn;
var cloneState = utils.cloneState;
var nodeDepth = utils.nodeDepth;
var isThenable = utils.isThenable;
var createRuntimeError = errors.createRuntimeError;
var createComponentRecord = records.createComponentRecord;

var RESERVED_ALIAS_NAMES = new Set([
  "$element", "$host", "$event", "$refs", "$component", "$parent", "$item", "$index"
]);
var RESERVED_INSTANCE_KEYS = new Set([
  "$host", "$refs", "$parent", "$alias", "$app", "$invalidate", "$runtime"
]);

function descriptorIn(object, key) {
  var current = object;
  while (current) {
    var descriptor = Object.getOwnPropertyDescriptor(current, key);
    if (descriptor) return descriptor;
    current = Object.getPrototypeOf(current);
  }
  return null;
}

function createComponentManager(runtime) {
  var definitions = new Map();
  var recordsByHost = new WeakMap();

  function validComponentName(name) {
    return /^[A-Za-z][A-Za-z0-9_.-]*$/.test(name);
  }

  function validAlias(alias) {
    return /^\$[A-Za-z][A-Za-z0-9_]*$/.test(alias) && !RESERVED_ALIAS_NAMES.has(alias);
  }

  function assertStateKey(record, key) {
    if (RESERVED_INSTANCE_KEYS.has(key)) {
      throw createRuntimeError("KIT_COMPONENT_STATE_COLLISION", "Cannot overwrite runtime metadata '" + key + "'", {
        component: record.name,
        key: key,
        host: record.host
      });
    }
    var descriptor = descriptorIn(record.target, key);
    if (descriptor && (typeof descriptor.value === "function" || descriptor.get || descriptor.set || descriptor.writable === false)) {
      throw createRuntimeError("KIT_COMPONENT_STATE_COLLISION", "Cannot overwrite component method/accessor '" + key + "'", {
        component: record.name,
        key: key,
        host: record.host
      });
    }
  }

  function createInstance(record) {
    var target = record.target;
    var proxy = new Proxy(target, {
      get: function (object, key, receiver) {
        if (key === "$host") return record.host;
        if (key === "$refs") return record.refs;
        if (key === "$parent") return record.parent ? record.parent.instance : undefined;
        if (key === "$alias") return record.alias || "";
        if (key === "$app") return record.app.root;
        if (key === "$runtime") return runtime.publicApi;
        if (key === "$invalidate") return function () {
          runtime.scheduler.invalidate(record.app, record.host, { type: "component-manual", component: record.name });
        };
        return Reflect.get(object, key, receiver);
      },
      set: function (object, key, value, receiver) {
        key = String(key);
        assertStateKey(record, key);
        var previous = object[key];
        var changed = previous !== value;
        var result = Reflect.set(object, key, value, receiver);
        if (changed) {
          runtime.scheduler.invalidate(record.app, record.host, {
            type: "component-state",
            component: record.name,
            key: key,
            value: value
          });
        }
        return result;
      },
      defineProperty: function (object, key, descriptor) {
        key = String(key);
        assertStateKey(record, key);
        var result = Reflect.defineProperty(object, key, descriptor);
        runtime.scheduler.invalidate(record.app, record.host, {
          type: "component-define",
          component: record.name,
          key: key
        });
        return result;
      },
      deleteProperty: function (object, key) {
        key = String(key);
        assertStateKey(record, key);
        var existed = hasOwn(object, key);
        var result = Reflect.deleteProperty(object, key);
        if (existed) runtime.scheduler.invalidate(record.app, record.host, {
          type: "component-delete",
          component: record.name,
          key: key
        });
        return result;
      }
    });
    record.instance = proxy;
    return proxy;
  }

  function installDefinition(record, definition) {
    if (!definition || record.activated) return record.instance;
    record.definition = definition;

    var keys = Reflect.ownKeys(definition);
    for (var i = 0; i < keys.length; i++) {
      var key = keys[i];
      if (typeof key !== "string") continue;
      if (RESERVED_INSTANCE_KEYS.has(key)) {
        throw createRuntimeError("KIT_COMPONENT_STATE_COLLISION", "Component definition uses reserved key '" + key + "'", {
          component: record.name,
          key: key
        });
      }
      var descriptor = Object.getOwnPropertyDescriptor(definition, key);
      if (!descriptor) continue;

      if (hasOwn(record.hostSeed, key) && (typeof descriptor.value === "function" || descriptor.get || descriptor.set)) {
        throw createRuntimeError("KIT_COMPONENT_STATE_COLLISION", "SSR scope cannot override component method/accessor '" + key + "'", {
          component: record.name,
          key: key,
          host: record.host
        });
      }

      if (typeof descriptor.value === "function" || descriptor.get || descriptor.set) {
        Object.defineProperty(record.target, key, descriptor);
      } else if (!hasOwn(record.target, key)) {
        Object.defineProperty(record.target, key, {
          configurable: descriptor.configurable !== false,
          enumerable: descriptor.enumerable !== false,
          writable: descriptor.writable !== false,
          value: cloneState(descriptor.value)
        });
      }
    }

    Object.keys(record.hostSeed).forEach(function (key) {
      assertStateKey(record, key);
      record.target[key] = record.hostSeed[key];
    });

    record.activated = true;
    record.app.pendingComponents.add(record);
    runtime.scheduler.invalidate(record.app, record.host, { type: "component-activated", component: record.name });
    return record.instance;
  }

  function register(name, definition) {
    name = String(name || "").trim();
    if (!validComponentName(name)) {
      throw createRuntimeError("KIT_COMPONENT_NAME", "Invalid component name '" + name + "'", { component: name });
    }
    if (arguments.length === 1) return definitions.get(name);
    if (!definition || typeof definition !== "object") {
      throw createRuntimeError("KIT_COMPONENT_DEFINITION", "Component '" + name + "' requires a plain object definition", {
        component: name
      });
    }
    definitions.set(name, definition);

    runtime.apps.forEach(function (app) {
      app.components.forEach(function (record) {
        if (record.name === name && !record.activated && !record.destroyed) {
          try { installDefinition(record, definition); }
          catch (error) { runtime.reportError(error, runtime.contextFor(record.host, "component", name, "mount")); }
        }
      });
    });
    return definition;
  }

  function setAlias(record, alias) {
    alias = String(alias || "").trim();
    if (record.alias && record.app.aliases.get(record.alias) === record) {
      record.app.aliases.delete(record.alias);
    }
    record.alias = "";
    if (!alias) return;
    if (!validAlias(alias)) {
      throw createRuntimeError("KIT_ALIAS_INVALID", "Invalid or reserved component alias '" + alias + "'", {
        alias: alias,
        host: record.host
      });
    }
    var existing = record.app.aliases.get(alias);
    if (existing && existing !== record) {
      throw createRuntimeError("KIT_DUPLICATE_ALIAS", "Component alias '" + alias + "' is already registered in this app", {
        alias: alias,
        host: record.host,
        existingHost: existing.host
      });
    }
    record.alias = alias;
    record.app.aliases.set(alias, record);
  }

  function ensure(app, host, name, parent, hostSeed) {
    var record = recordsByHost.get(host);
    if (record) {
      record.parent = parent || null;
      if (hostSeed) record.hostSeed = hostSeed;
      var aliasNow = host.getAttribute("data-kit-as") || "";
      if (aliasNow !== record.alias) setAlias(record, aliasNow);
      return record;
    }

    record = createComponentRecord(app, host, name, parent);
    record.hostSeed = hostSeed || Object.create(null);
    createInstance(record);
    recordsByHost.set(host, record);
    app.components.add(record);
    runtime.nodeRecord(host, app).component = record;

    try { setAlias(record, host.getAttribute("data-kit-as") || ""); }
    catch (error) { runtime.reportError(error, runtime.contextFor(host, "as", host.getAttribute("data-kit-as"), "mount")); }

    var definition = definitions.get(name);
    if (definition) {
      try { installDefinition(record, definition); }
      catch (error) { runtime.reportError(error, runtime.contextFor(host, "component", name, "mount")); }
    } else if (runtime.options.development) {
      runtime.warn("KIT_COMPONENT_MISSING", "Component '" + name + "' is not registered yet", {
        component: name,
        host: host
      });
    }

    return record;
  }

  function nearest(node, app, includeSelf) {
    var current = includeSelf === false ? node && node.parentNode : node;
    while (current && current !== app.root.parentNode) {
      var record = recordsByHost.get(current);
      if (record && !record.destroyed && record.app === app) return record;
      if (current === app.root) break;
      current = runtime.logicalParent(current) || current.parentNode;
    }
    return null;
  }

  function registerRef(record, name, element) {
    if (!record) {
      if (runtime.options.development) runtime.warn("KIT_REF_NO_COMPONENT", "data-kit-ref requires an owning component", {
        ref: name,
        element: element
      });
      return;
    }
    name = String(name || "").trim();
    if (!/^[A-Za-z][A-Za-z0-9_]*$/.test(name)) {
      throw createRuntimeError("KIT_REF_INVALID", "Invalid ref name '" + name + "'", { ref: name, element: element });
    }
    var existing = record.refs[name];
    if (existing && existing !== element) {
      throw createRuntimeError("KIT_DUPLICATE_REF", "Ref '" + name + "' is already registered in component '" + record.name + "'", {
        ref: name,
        component: record.name,
        element: element,
        existing: existing
      });
    }
    record.refs[name] = element;
  }

  function removeRef(record, name, element) {
    if (record && name && record.refs[name] === element) delete record.refs[name];
  }

  function runMount(record) {
    if (!record || record.destroyed || record.mounted || record.mounting || !record.activated) return;
    var mount = record.instance && record.instance.mount;
    record.mounted = true;
    if (typeof mount !== "function") return;
    record.mounting = true;
    var result;
    try {
      result = mount.call(record.instance);
    } catch (error) {
      record.mounting = false;
      runtime.reportError(error, runtime.contextFor(record.host, "component", record.name, "mount"));
      return;
    }

    if (isThenable(result)) {
      record.pendingEffects.add(result);
      result.then(function (cleanup) {
        record.pendingEffects.delete(result);
        record.mounting = false;
        if (record.destroyed) {
          if (typeof cleanup === "function") {
            try { cleanup(); } catch (error) { runtime.reportError(error, runtime.contextFor(record.host, "component", record.name, "cleanup")); }
          }
          return;
        }
        if (typeof cleanup === "function") record.mountCleanup = cleanup;
      }, function (error) {
        record.pendingEffects.delete(result);
        record.mounting = false;
        runtime.reportError(error, runtime.contextFor(record.host, "component", record.name, "mount"));
      });
    } else {
      record.mounting = false;
      if (typeof result === "function") record.mountCleanup = result;
    }
  }

  function mountPending(app, boundary) {
    var pending = Array.from(app.pendingComponents).filter(function (record) {
      return !record.destroyed && record.host.isConnected &&
        (!boundary || boundary === app.root || (boundary.contains && boundary.contains(record.host)) || boundary === record.host);
    });
    pending.sort(function (left, right) { return nodeDepth(right.host) - nodeDepth(left.host); });
    pending.forEach(function (record) {
      app.pendingComponents.delete(record);
      runMount(record);
    });
  }

  function unmount(record) {
    if (!record || record.destroyed) return;
    record.destroyed = true;

    // Every task started with this component instance as owner is aborted
    // before lifecycle cleanup, preventing stale async work from mutating a
    // component that has already left its application.
    if (runtime.task && typeof runtime.task.abort === "function") {
      try { runtime.task.abort(record.instance, undefined, "component-unmount"); }
      catch (error) { runtime.reportError(error, runtime.contextFor(record.host, "component", record.name, "task-abort")); }
    }

    var children = Array.from(record.app.components).filter(function (candidate) {
      return candidate.parent === record && !candidate.destroyed;
    }).sort(function (left, right) { return nodeDepth(right.host) - nodeDepth(left.host); });
    children.forEach(unmount);

    // Async work is owned by the component instance, not by its current DOM
    // position. A real unmount aborts it; a DOM move inside the same app never
    // reaches this path.
    if (runtime.task && record.instance) {
      try { runtime.task.abort(record.instance); }
      catch (taskError) { runtime.reportError(taskError, runtime.contextFor(record.host, "component", record.name, "task-abort")); }
    }

    if (record.mounted && record.instance && typeof record.instance.unmount === "function") {
      try { record.instance.unmount.call(record.instance); }
      catch (error) { runtime.reportError(error, runtime.contextFor(record.host, "component", record.name, "unmount")); }
    }

    if (typeof record.mountCleanup === "function") {
      try { record.mountCleanup(); }
      catch (error) { runtime.reportError(error, runtime.contextFor(record.host, "component", record.name, "cleanup")); }
      record.mountCleanup = null;
    }

    while (record.cleanups.length) {
      try { record.cleanups.pop()(); }
      catch (error) { runtime.reportError(error, runtime.contextFor(record.host, "component", record.name, "cleanup")); }
    }

    if (record.alias && record.app.aliases.get(record.alias) === record) record.app.aliases.delete(record.alias);
    Object.keys(record.refs).forEach(function (key) { delete record.refs[key]; });
    record.app.components.delete(record);
    record.app.pendingComponents.delete(record);
    recordsByHost.delete(record.host);
  }

  return {
    register: register,
    get: function (name) { return definitions.get(name); },
    ensure: ensure,
    nearest: nearest,
    byHost: function (host) { return recordsByHost.get(host) || null; },
    setAlias: setAlias,
    registerRef: registerRef,
    removeRef: removeRef,
    mountPending: mountPending,
    unmount: unmount,
    definitions: definitions,
    recordsByHost: recordsByHost
  };
}

module.exports = {
  createComponentManager: createComponentManager
};

},
17: function(module, exports, __require){
"use strict";

var expressionConstants = __require(1);
var errors = __require(11);
var utils = __require(10);
var MODES = expressionConstants.MODES;
var createRuntimeError = errors.createRuntimeError;
var normalizeScalar = utils.normalizeScalar;
var hasOwn = utils.hasOwn;

var HTML_BOOLEAN_ATTRIBUTES = new Set([
  "allowfullscreen", "async", "autofocus", "autoplay", "checked", "controls", "default",
  "defer", "disabled", "formnovalidate", "hidden", "inert", "ismap", "itemscope", "loop",
  "multiple", "muted", "nomodule", "novalidate", "open", "playsinline", "readonly", "required",
  "reversed", "selected"
]);

var BLOCKED_BIND_ATTRIBUTES = new Set([
  "class", "style", "srcdoc", "value", "checked", "selected"
]);

var URL_ATTRIBUTES = new Set([
  "href", "src", "action", "formaction", "poster", "xlink:href"
]);

function flattenClasses(value, output) {
  output = output || [];
  if (value == null || value === false || value === true) return output;
  if (typeof value === "string") {
    value.split(/\s+/).forEach(function (name) { if (name) output.push(name); });
    return output;
  }
  if (Array.isArray(value)) {
    value.forEach(function (item) { flattenClasses(item, output); });
    return output;
  }
  if (typeof value === "object") {
    Object.keys(value).forEach(function (key) {
      if (value[key]) flattenClasses(key, output);
    });
  }
  return output;
}

function classSetFromValue(value) {
  var set = new Set();
  if (Array.isArray(value) && value.length && value[0] && hasOwn(value[0], "key") && hasOwn(value[0], "value")) {
    value.forEach(function (entry) {
      if (entry.value) flattenClasses(entry.key, []).forEach(function (name) { set.add(name); });
    });
  } else {
    flattenClasses(value, []).forEach(function (name) { set.add(name); });
  }
  return set;
}

function installCoreDirectives(runtime) {
  var registry = runtime.directives;

  registry.register("text", {
    mode: MODES.BINDING,
    phase: "content",
    update: function (binding, result) {
      var text = normalizeScalar(result.value);
      if (text == null) {
        throw createRuntimeError("KIT_TEXT_NON_SCALAR", "data-kit-text must resolve to a scalar value", {
          value: result.value,
          element: binding.element
        });
      }
      if (binding.element.textContent !== text) binding.element.textContent = text;
      binding.lastValue = text;
    }
  });

  registry.register("show", {
    mode: MODES.BINDING,
    phase: "content",
    mount: function (binding) {
      binding.authorHidden = binding.element.hasAttribute("hidden");
    },
    update: function (binding, result) {
      var nextHidden = !result.value;
      if (binding.element.hidden !== nextHidden) binding.element.hidden = nextHidden;
      binding.lastValue = !!result.value;
    },
    unmount: function (binding) {
      binding.element.hidden = !!binding.authorHidden;
    }
  });

  registry.register("class", {
    mode: MODES.CLASS_VALUE,
    phase: "content",
    mount: function (binding) {
      binding.initialClasses = new Set(Array.prototype.slice.call(binding.element.classList || []));
    },
    update: function (binding, result) {
      var wanted = classSetFromValue(result.value);
      binding.ownedClasses.forEach(function (name) {
        if (!wanted.has(name)) {
          binding.element.classList.remove(name);
          binding.ownedClasses.delete(name);
        }
      });
      wanted.forEach(function (name) {
        if (!binding.element.classList.contains(name)) binding.element.classList.add(name);
        if (!binding.initialClasses.has(name)) binding.ownedClasses.add(name);
      });
      binding.lastValue = wanted;
    },
    unmount: function (binding) {
      binding.ownedClasses.forEach(function (name) { binding.element.classList.remove(name); });
      binding.ownedClasses.clear();
    }
  });

  registry.register("style", {
    mode: MODES.NAMED_MAP,
    phase: "content",
    mount: function (binding) {
      binding.styleOriginals = new Map();
    },
    update: function (binding, result) {
      var seen = new Set();
      function setAttribute(name, value) {
        var next = String(value);
        if (!binding.element.hasAttribute(name) || binding.element.getAttribute(name) !== next) {
          binding.element.setAttribute(name, next);
        }
      }
      function setBooleanAttribute(name) {
        if (!binding.element.hasAttribute(name) || binding.element.getAttribute(name) !== "") {
          binding.element.setAttribute(name, "");
        }
      }
      result.value.forEach(function (entry) {
        var key = entry.key;
        seen.add(key);
        if (!binding.styleOriginals.has(key)) {
          binding.styleOriginals.set(key, {
            value: binding.element.style.getPropertyValue(key),
            priority: binding.element.style.getPropertyPriority(key)
          });
        }
        if (entry.value == null || entry.value === false) {
          if (binding.element.style.getPropertyValue(key)) binding.element.style.removeProperty(key);
        } else {
          var nextStyle = String(entry.value);
          if (binding.element.style.getPropertyValue(key) !== nextStyle) {
            binding.element.style.setProperty(key, nextStyle);
          }
        }
      });
      binding.ownedStyles.forEach(function (_, key) {
        if (!seen.has(key)) binding.element.style.removeProperty(key);
      });
      binding.ownedStyles = new Map();
      seen.forEach(function (key) { binding.ownedStyles.set(key, true); });
    },
    unmount: function (binding) {
      if (!binding.styleOriginals) return;
      binding.styleOriginals.forEach(function (original, key) {
        if (original.value) binding.element.style.setProperty(key, original.value, original.priority || "");
        else binding.element.style.removeProperty(key);
      });
    }
  });

  registry.register("bind", {
    mode: MODES.NAMED_MAP,
    phase: "content",
    validate: function (binding) {
      var entries = binding.compiled.ast && binding.compiled.ast.entries || [];
      entries.forEach(function (entry) {
        var name = String(entry.key);
        var lower = name.toLowerCase();
        if (BLOCKED_BIND_ATTRIBUTES.has(lower) || lower.indexOf("on") === 0 ||
            lower.indexOf("data-kit-") === 0 || lower.indexOf("data-kitwork-") === 0) {
          throw createRuntimeError("KIT_UNSAFE_ATTRIBUTE", "data-kit-bind cannot own attribute '" + name + "'", {
            attribute: name,
            element: binding.element
          });
        }
      });
    },
    mount: function (binding) {
      binding.attributeOriginals = new Map();
    },
    update: function (binding, result) {
      var seen = new Set();
      function setAttribute(name, value) {
        var next = String(value);
        if (!binding.element.hasAttribute(name) || binding.element.getAttribute(name) !== next) {
          binding.element.setAttribute(name, next);
        }
      }
      function setBooleanAttribute(name) {
        if (!binding.element.hasAttribute(name) || binding.element.getAttribute(name) !== "") {
          binding.element.setAttribute(name, "");
        }
      }
      result.value.forEach(function (entry) {
        var name = entry.key;
        var lower = name.toLowerCase();
        var value = entry.value;
        seen.add(name);
        if (!binding.attributeOriginals.has(name)) {
          binding.attributeOriginals.set(name, {
            present: binding.element.hasAttribute(name),
            value: binding.element.getAttribute(name)
          });
        }

        if (URL_ATTRIBUTES.has(lower) && value != null) {
          var normalized = String(value).trim().toLowerCase();
          if (normalized.indexOf("javascript:") === 0 || normalized.indexOf("vbscript:") === 0) {
            throw createRuntimeError("KIT_UNSAFE_URL", "Unsafe URL scheme in attribute '" + name + "'", {
              attribute: name,
              value: value,
              element: binding.element
            });
          }
        }

        if (lower.indexOf("data-") === 0 || lower.indexOf("aria-") === 0) {
          if (value == null) {
            if (binding.element.hasAttribute(name)) binding.element.removeAttribute(name);
          } else if (value === true) setAttribute(name, "true");
          else if (value === false) setAttribute(name, "false");
          else setAttribute(name, value);
        } else if (HTML_BOOLEAN_ATTRIBUTES.has(lower)) {
          if (value === true) setBooleanAttribute(name);
          else if (value == null || value === false) {
            if (binding.element.hasAttribute(name)) binding.element.removeAttribute(name);
          } else setAttribute(name, value);
        } else {
          if (value == null || value === false) {
            if (binding.element.hasAttribute(name)) binding.element.removeAttribute(name);
          } else if (value === true) setAttribute(name, "true");
          else setAttribute(name, value);
        }
      });

      binding.ownedAttributes.forEach(function (_, name) {
        if (!seen.has(name)) binding.element.removeAttribute(name);
      });
      binding.ownedAttributes = new Map();
      seen.forEach(function (name) { binding.ownedAttributes.set(name, true); });
    },
    unmount: function (binding) {
      if (!binding.attributeOriginals) return;
      binding.attributeOriginals.forEach(function (original, name) {
        if (original.present) binding.element.setAttribute(name, original.value == null ? "" : original.value);
        else binding.element.removeAttribute(name);
      });
    }
  });

  registry.register("model", {
    mode: MODES.WRITABLE_PATH,
    phase: "form",
    mount: function (binding) {
      runtime.model.mount(binding);
    },
    update: function (binding, result) {
      runtime.model.update(binding, result.value);
    },
    unmount: function (binding) {
      runtime.model.unmount(binding);
    }
  });
}

module.exports = {
  installCoreDirectives: installCoreDirectives,
  flattenClasses: flattenClasses,
  classSetFromValue: classSetFromValue,
  HTML_BOOLEAN_ATTRIBUTES: HTML_BOOLEAN_ATTRIBUTES
};

},
18: function(module, exports, __require){
"use strict";

var utils = __require(10);
var hasOwn = utils.hasOwn;

function createModelManager(runtime) {
  function inputType(element) {
    return String(element.type || "").toLowerCase();
  }

  function readValue(binding, event) {
    var element = binding.element;
    var tag = String(element.tagName || "").toLowerCase();
    var type = inputType(element);

    if (type === "file") return element.files;
    if (type === "number" || type === "range") {
      if (element.value === "") return null;
      var number = Number(element.value);
      return Number.isFinite(number) ? number : null;
    }
    if (type === "checkbox") {
      var environment = runtime.environmentFor(element, event || null);
      var current;
      try { current = runtime.expression.evaluate(binding.compiled, environment); }
      catch (_) { current = undefined; }
      if (Array.isArray(current)) {
        var next = current.slice();
        var index = next.indexOf(element.value);
        if (element.checked && index < 0) next.push(element.value);
        if (!element.checked && index >= 0) next.splice(index, 1);
        return next;
      }
      return !!element.checked;
    }
    if (type === "radio") return element.checked ? element.value : undefined;
    if (tag === "select" && element.multiple) {
      return Array.prototype.slice.call(element.options || []).filter(function (option) {
        return option.selected;
      }).map(function (option) { return option.value; });
    }
    return element.value == null ? "" : String(element.value);
  }

  function writeValue(element, value) {
    var tag = String(element.tagName || "").toLowerCase();
    var type = inputType(element);

    if (type === "file") return;
    if (type === "checkbox") {
      if (Array.isArray(value)) element.checked = value.indexOf(element.value) >= 0;
      else element.checked = !!value;
      return;
    }
    if (type === "radio") {
      element.checked = value != null && String(value) === String(element.value);
      return;
    }
    if (tag === "select" && element.multiple) {
      var selected = Array.isArray(value) ? value.map(String) : [];
      Array.prototype.slice.call(element.options || []).forEach(function (option) {
        option.selected = selected.indexOf(String(option.value)) >= 0;
      });
      return;
    }

    var next = value == null ? "" : String(value);
    if (element.value !== next) element.value = next;
  }

  function seedIfMissing(binding) {
    if (binding.modelSeeded) return;
    binding.modelSeeded = true;
    var environment = runtime.environmentFor(binding.element, null);
    var current;
    try { current = runtime.expression.evaluate(binding.compiled, environment); }
    catch (_) { current = undefined; }
    if (current !== undefined) return;
    var value = readValue(binding, null);
    if (value === undefined) return;
    try {
      runtime.expression.assign(binding.compiled, environment, value);
    } catch (error) {
      runtime.reportError(error, runtime.contextFor(binding.element, "model", binding.source, "model-seed"));
    }
  }

  function mount(binding) {
    seedIfMissing(binding);
    runtime.modelBindings.add(binding);
  }

  function update(binding, value) {
    var record = runtime.nodeRecord(binding.element, binding.app);
    if (record.composing) return;
    writeValue(binding.element, value);
  }

  function unmount(binding) {
    runtime.modelBindings.delete(binding);
  }

  function commit(element, event) {
    var record = runtime.peekNodeRecord(element);
    if (!record || record.composing) return;
    var binding = null;
    record.bindings.forEach(function (candidate) {
      if (candidate.directiveName === "model") binding = candidate;
    });
    if (!binding || binding.disabled) return;

    var value = readValue(binding, event);
    if (value === undefined) return;
    try {
      var environment = runtime.environmentFor(element, event || null);
      runtime.expression.assign(binding.compiled, environment, value);
      runtime.scheduler.invalidate(binding.app, runtime.boundaryFor(element), {
        type: "model",
        source: binding.source,
        value: value
      });
    } catch (error) {
      runtime.reportError(error, runtime.contextFor(element, "model", binding.source, "input", event));
    }
  }

  function handleInput(event) {
    var element = event.target;
    if (!element || !element.getAttribute) return;
    var record = runtime.peekNodeRecord(element);
    if (!record) return;
    var hasModel = false;
    record.bindings.forEach(function (binding) {
      if (binding.directiveName === "model") hasModel = true;
    });
    if (!hasModel) return;
    commit(element, event);
  }

  function install(document) {
    runtime.listen(document, "compositionstart", function (event) {
      var record = runtime.peekNodeRecord(event.target);
      if (record) record.composing = true;
    }, true);
    runtime.listen(document, "compositionend", function (event) {
      var record = runtime.peekNodeRecord(event.target);
      if (record) record.composing = false;
      commit(event.target, event);
    }, true);
    runtime.listen(document, "input", handleInput, true);
    runtime.listen(document, "change", handleInput, true);
  }

  return {
    mount: mount,
    update: update,
    unmount: unmount,
    commit: commit,
    install: install,
    readValue: readValue,
    writeValue: writeValue
  };
}

module.exports = {
  createModelManager: createModelManager
};

},
19: function(module, exports, __require){
"use strict";

var expressionConstants = __require(1);
var errors = __require(11);
var utils = __require(10);
var MODES = expressionConstants.MODES;
var createRuntimeError = errors.createRuntimeError;
var eventPath = utils.eventPath;
var isThenable = utils.isThenable;

var EVENT_TYPES = new Set((
  "click dblclick contextmenu mousedown mouseup mousemove mouseover mouseout mouseenter mouseleave " +
  "pointerdown pointerup pointermove pointerover pointerout pointerenter pointerleave pointercancel " +
  "keydown keyup keypress input change submit reset focus blur focusin focusout scroll resize wheel " +
  "drag dragstart dragend dragenter dragleave dragover drop touchstart touchmove touchend touchcancel " +
  "animationstart animationend animationiteration transitionstart transitionend transitioncancel"
).split(/\s+/));

var DELEGATED_EVENTS = new Set((
  "click dblclick contextmenu mousedown mouseup mousemove mouseover mouseout " +
  "pointerdown pointerup pointermove pointerover pointerout pointercancel " +
  "keydown keyup keypress input change submit reset focusin focusout wheel " +
  "drag dragstart dragend dragenter dragleave dragover drop touchstart touchmove touchend touchcancel " +
  "animationstart animationend animationiteration transitionstart transitionend transitioncancel"
).split(/\s+/));

var KEYBOARD_EVENTS = new Set(["keydown", "keyup", "keypress"]);
var OUTSIDE_EVENTS = new Set(["click", "mousedown", "mouseup", "pointerdown", "pointerup", "touchstart"]);

function createEventManager(runtime) {
  var delegatedInstalled = new Set();
  var delegatedBindings = new Map();
  var outsideBindings = new Map();
  var documentBindings = new Map();
  var windowBindings = new Map();

  function parseAttribute(attributeName) {
    if (attributeName.indexOf("data-kit-") !== 0) return null;
    var raw = attributeName.slice(9);
    var pieces = raw.split(":");
    var type = pieces.shift();
    if (!EVENT_TYPES.has(type)) return null;

    var spec = {
      type: type,
      target: "element",
      outside: false,
      enter: false,
      escape: false,
      prevent: false,
      stop: false,
      once: false,
      debounce: 0,
      throttle: 0
    };

    pieces.forEach(function (modifier) {
      if (modifier === "window" || modifier === "document") {
        if (spec.target !== "element") throw createRuntimeError("KIT_INVALID_MODIFIER", "Only one event target modifier is allowed", {
          attribute: attributeName
        });
        spec.target = modifier;
      } else if (modifier === "outside") spec.outside = true;
      else if (modifier === "enter") spec.enter = true;
      else if (modifier === "escape") spec.escape = true;
      else if (modifier === "prevent") spec.prevent = true;
      else if (modifier === "stop") spec.stop = true;
      else if (modifier === "once") spec.once = true;
      else if (/^debounce\(\d+\)$/.test(modifier)) {
        if (spec.throttle) throw createRuntimeError("KIT_INVALID_MODIFIER", "debounce() and throttle() cannot be combined", { attribute: attributeName });
        spec.debounce = parseInt(modifier.slice(9, -1), 10);
      } else if (/^throttle\(\d+\)$/.test(modifier)) {
        if (spec.debounce) throw createRuntimeError("KIT_INVALID_MODIFIER", "debounce() and throttle() cannot be combined", { attribute: attributeName });
        spec.throttle = parseInt(modifier.slice(9, -1), 10);
      } else {
        throw createRuntimeError("KIT_INVALID_MODIFIER", "Unknown event modifier '" + modifier + "'", {
          attribute: attributeName,
          modifier: modifier
        });
      }
    });

    if ((spec.enter || spec.escape) && !KEYBOARD_EVENTS.has(type)) {
      throw createRuntimeError("KIT_INVALID_MODIFIER", "Keyboard filters are only valid on keyboard events", {
        attribute: attributeName
      });
    }
    if (spec.outside && !OUTSIDE_EVENTS.has(type)) {
      throw createRuntimeError("KIT_INVALID_MODIFIER", ":outside is only valid on pointer/click events", {
        attribute: attributeName
      });
    }
    if (spec.outside && spec.target !== "element") {
      throw createRuntimeError("KIT_INVALID_MODIFIER", ":outside cannot be combined with :window or :document", {
        attribute: attributeName
      });
    }
    return spec;
  }

  function setFor(map, type) {
    var set = map.get(type);
    if (!set) { set = new Set(); map.set(type, set); }
    return set;
  }

  function trackEffect(binding, promise, event) {
    if (!isThenable(promise)) return;
    binding.pendingCount++;
    if (binding.pendingCount === 1) {
      binding.busySnapshot = {
        dataBusyPresent: binding.element.hasAttribute("data-busy"),
        dataBusy: binding.element.getAttribute("data-busy"),
        ariaBusyPresent: binding.element.hasAttribute("aria-busy"),
        ariaBusy: binding.element.getAttribute("aria-busy")
      };
      binding.element.setAttribute("data-busy", "true");
      binding.element.setAttribute("aria-busy", "true");
    }

    Promise.resolve(promise).then(function () {
      settle(binding);
    }, function (error) {
      runtime.reportError(error, runtime.contextFor(binding.element, binding.attributeName, binding.source, "async-action", event));
      settle(binding);
    });
  }

  function settle(binding) {
    binding.pendingCount = Math.max(0, binding.pendingCount - 1);
    if (binding.pendingCount === 0 && binding.busySnapshot) {
      var snapshot = binding.busySnapshot;
      if (snapshot.dataBusyPresent) binding.element.setAttribute("data-busy", snapshot.dataBusy == null ? "" : snapshot.dataBusy);
      else binding.element.removeAttribute("data-busy");
      if (snapshot.ariaBusyPresent) binding.element.setAttribute("aria-busy", snapshot.ariaBusy == null ? "" : snapshot.ariaBusy);
      else binding.element.removeAttribute("aria-busy");
      binding.busySnapshot = null;
    }
    runtime.scheduler.invalidate(binding.app, runtime.boundaryFor(binding.element), {
      type: "async-settle",
      directive: binding.attributeName
    });
  }

  function action(binding, event) {
    if (binding.disabled || binding.consumed && binding.modifiers.once) return { stop: false };
    var spec = binding.modifiers;

    if (spec.enter && event.key !== "Enter") return { stop: false };
    if (spec.escape && event.key !== "Escape" && event.key !== "Esc" && event.keyCode !== 27) return { stop: false };
    if (spec.outside && (binding.element === event.target || binding.element.contains(event.target))) return { stop: false };
    if (spec.outside && runtime.isFresh(binding.element)) return { stop: false };

    if (spec.prevent && event && event.preventDefault) event.preventDefault();
    if (spec.stop && event && event.stopPropagation) event.stopPropagation();
    if (spec.once) binding.consumed = true;

    function execute() {
      try {
        var environment = runtime.environmentFor(binding.element, event || null);
        var result = runtime.expression.execute(binding.compiled, environment);
        runtime.scheduler.invalidate(binding.app, runtime.boundaryFor(binding.element), {
          type: "action",
          directive: binding.attributeName,
          mutations: result.mutations
        });
        result.effects.forEach(function (effect) { trackEffect(binding, effect, event); });
      } catch (error) {
        runtime.reportError(error, runtime.contextFor(binding.element, binding.attributeName, binding.source, "action", event));
      }
    }

    if (spec.debounce > 0) {
      if (binding.timer) clearTimeout(binding.timer);
      binding.timer = setTimeout(function () {
        binding.timer = null;
        execute();
      }, spec.debounce);
    } else if (spec.throttle > 0) {
      var now = Date.now();
      if (!binding.throttleAt || now - binding.throttleAt >= spec.throttle) {
        binding.throttleAt = now;
        execute();
      }
    } else execute();

    return { stop: !!spec.stop };
  }

  function dispatchDelegated(type, event) {
    var path = eventPath(event);
    for (var i = 0; i < path.length; i++) {
      var node = path[i];
      if (!node || node.nodeType !== 1) continue;
      var record = runtime.peekNodeRecord(node);
      if (!record) continue;
      var list = record.eventBindings.get(type);
      if (!list) continue;
      for (var j = 0; j < list.length; j++) {
        var result = action(list[j], event);
        if (result.stop) return;
      }
    }
  }

  function installDelegated(type) {
    if (delegatedInstalled.has(type)) return;
    delegatedInstalled.add(type);
    runtime.listen(runtime.document, type, function (event) {
      dispatchDelegated(type, event);
      var outside = outsideBindings.get(type);
      if (outside) Array.from(outside).forEach(function (binding) { action(binding, event); });
      var documentSet = documentBindings.get(type);
      if (documentSet) Array.from(documentSet).forEach(function (binding) { action(binding, event); });
    }, false);
  }

  function installWindow(type) {
    var key = "window:" + type;
    if (delegatedInstalled.has(key)) return;
    delegatedInstalled.add(key);
    runtime.listen(runtime.global, type, function (event) {
      var set = windowBindings.get(type);
      if (set) Array.from(set).forEach(function (binding) { action(binding, event); });
    }, false);
  }

  function register(binding, spec) {
    binding.mode = MODES.ACTION;
    binding.eventType = spec.type;
    binding.modifiers = spec;

    if (spec.target === "window") {
      setFor(windowBindings, spec.type).add(binding);
      installWindow(spec.type);
      return;
    }
    if (spec.target === "document") {
      setFor(documentBindings, spec.type).add(binding);
      installDelegated(spec.type);
      return;
    }
    if (spec.outside) {
      setFor(outsideBindings, spec.type).add(binding);
      installDelegated(spec.type);
      return;
    }
    if (DELEGATED_EVENTS.has(spec.type)) {
      var record = runtime.nodeRecord(binding.element, binding.app);
      var list = record.eventBindings.get(spec.type);
      if (!list) { list = []; record.eventBindings.set(spec.type, list); }
      list.push(binding);
      setFor(delegatedBindings, spec.type).add(binding);
      installDelegated(spec.type);
      return;
    }

    var listener = function (event) { action(binding, event); };
    binding.element.addEventListener(spec.type, listener, false);
    binding.directListenerCleanup = function () {
      binding.element.removeEventListener(spec.type, listener, false);
    };
  }

  function unregister(binding) {
    if (binding.timer) clearTimeout(binding.timer);
    binding.timer = null;
    if (binding.directListenerCleanup) binding.directListenerCleanup();
    binding.directListenerCleanup = null;

    var spec = binding.modifiers;
    if (!spec) return;
    [delegatedBindings, outsideBindings, documentBindings, windowBindings].forEach(function (map) {
      var set = map.get(spec.type);
      if (set) set.delete(binding);
    });
    var record = runtime.peekNodeRecord(binding.element);
    if (record) {
      var list = record.eventBindings.get(spec.type);
      if (list) {
        var index = list.indexOf(binding);
        if (index >= 0) list.splice(index, 1);
        if (!list.length) record.eventBindings.delete(spec.type);
      }
    }
  }

  return {
    parseAttribute: parseAttribute,
    register: register,
    unregister: unregister,
    action: action,
    eventTypes: EVENT_TYPES
  };
}

module.exports = {
  createEventManager: createEventManager,
  EVENT_TYPES: EVENT_TYPES
};

},
20: function(module, exports, __require){
"use strict";

var constants = __require(1);
var errors = __require(11);
var utils = __require(10);
var MODES = constants.MODES;
var createRuntimeError = errors.createRuntimeError;
var nodeContains = utils.nodeContains;

function createStructuralManager(runtime) {
  function collect(element, app) {
    var ifSource = element.getAttribute("data-kit-if");
    var forSource = element.getAttribute("data-kit-for");
    if (ifSource != null && forSource != null) {
      throw createRuntimeError("KIT_STRUCTURE_CONFLICT", "data-kit-if and data-kit-for cannot own the same element", {
        element: element
      });
    }
    if (ifSource == null && forSource == null) return null;

    var parent = element.parentNode;
    if (!parent) return null;
    var type = ifSource != null ? "if" : "for";
    var anchor = runtime.document.createComment("kit-" + type);
    var template = element.cloneNode(true);
    template.removeAttribute(type === "if" ? "data-kit-if" : "data-kit-for");
    if (type === "for") template.removeAttribute("data-kit-key");

    var structure = {
      type: type,
      app: app,
      anchor: anchor,
      template: template,
      source: type === "if" ? ifSource : forSource,
      compiled: runtime.expression.compile(type === "if" ? MODES.BINDING : MODES.ITERATOR, type === "if" ? ifSource : forSource),
      keySource: type === "for" ? (element.getAttribute("data-kit-key") || "") : "",
      keyCompiled: null,
      mounted: null,
      rows: new Map(),
      destroyed: false
    };
    if (type === "for" && structure.keySource) {
      structure.keyCompiled = runtime.expression.compile(MODES.BINDING, structure.keySource);
    }

    parent.replaceChild(anchor, element);
    var record = runtime.nodeRecord(anchor, app);
    record.structure = structure;
    record.hydrated = true;
    app.structures.add(structure);
    return structure;
  }

  function renderIf(structure) {
    var environment = runtime.environmentFor(structure.anchor, null);
    var visible = !!runtime.expression.evaluate(structure.compiled, environment);
    var changed = false;
    if (visible && !structure.mounted) {
      var node = structure.template.cloneNode(true);
      structure.anchor.parentNode.insertBefore(node, structure.anchor.nextSibling);
      structure.mounted = node;
      runtime.markFresh(node);
      runtime.hydrateTree(node, structure.app);
      changed = true;
    } else if (!visible && structure.mounted) {
      runtime.cleanupTree(structure.mounted, structure.app);
      if (structure.mounted.parentNode) structure.mounted.parentNode.removeChild(structure.mounted);
      structure.mounted = null;
      changed = true;
    }
    return changed;
  }

  function normalizeKey(value, index) {
    if (typeof value !== "string" && typeof value !== "number") return "index:" + index;
    return typeof value + ":" + String(value);
  }

  function renderFor(structure) {
    var environment = runtime.environmentFor(structure.anchor, null);
    var iterator = runtime.expression.evaluate(structure.compiled, environment);
    var collection = iterator && iterator.collection;
    if (!Array.isArray(collection)) collection = [];

    var used = new Set();
    var ordered = [];
    var duplicate = false;

    for (var i = 0; i < collection.length; i++) {
      var frame = Object.create(null);
      frame[iterator.itemName] = collection[i];
      if (iterator.indexName) frame[iterator.indexName] = i;

      var rawKey = i;
      if (structure.keyCompiled) {
        var keyEnvironment = runtime.environmentFor(structure.anchor, null, { loopFrames: [frame] });
        rawKey = runtime.expression.evaluate(structure.keyCompiled, keyEnvironment);
        if (typeof rawKey !== "string" && typeof rawKey !== "number") {
          if (runtime.options.development) runtime.warn("KIT_LIST_KEY_TYPE", "data-kit-key must resolve to a string or number; index fallback used", {
            value: rawKey,
            index: i,
            anchor: structure.anchor
          });
          rawKey = i;
        }
      } else if (runtime.options.development && i === 0) {
        runtime.warn("KIT_LIST_INDEX_KEY", "data-kit-for has no data-kit-key; index fallback does not preserve identity across reorders", {
          anchor: structure.anchor
        });
      }

      var key = normalizeKey(rawKey, i);
      if (used.has(key)) {
        duplicate = true;
        runtime.reportError(createRuntimeError("KIT_DUPLICATE_LIST_KEY", "Duplicate list key '" + rawKey + "'", {
          key: rawKey,
          index: i,
          anchor: structure.anchor
        }), runtime.contextFor(structure.anchor, "for", structure.source, "structure"));
        continue;
      }
      used.add(key);

      var row = structure.rows.get(key);
      if (!row) {
        var node = structure.template.cloneNode(true);
        var record = runtime.nodeRecord(node, structure.app);
        record.loopFrame = frame;
        row = { key: key, rawKey: rawKey, node: node, frame: frame };
        structure.rows.set(key, row);
      } else {
        row.frame[iterator.itemName] = collection[i];
        if (iterator.indexName) row.frame[iterator.indexName] = i;
        row.rawKey = rawKey;
      }
      ordered.push(row);
    }

    var changed = false;
    var parent = structure.anchor.parentNode;
    if (!parent) return false;
    var cursor = structure.anchor.nextSibling;
    ordered.forEach(function (row) {
      if (!row.node.isConnected) {
        parent.insertBefore(row.node, cursor);
        runtime.markFresh(row.node);
        runtime.hydrateTree(row.node, structure.app);
        changed = true;
      } else if (row.node !== cursor) {
        parent.insertBefore(row.node, cursor);
        changed = true;
      }
      cursor = row.node.nextSibling;
    });

    Array.from(structure.rows.entries()).forEach(function (entry) {
      if (used.has(entry[0])) return;
      var row = entry[1];
      runtime.cleanupTree(row.node, structure.app);
      if (row.node.parentNode) row.node.parentNode.removeChild(row.node);
      structure.rows.delete(entry[0]);
      changed = true;
    });
    return changed || duplicate;
  }

  function render(app, boundary) {
    var changed = false;
    Array.from(app.structures).forEach(function (structure) {
      if (structure.destroyed || !structure.anchor.isConnected) return;
      if (boundary !== app.root && !nodeContains(boundary, structure.anchor)) return;
      try {
        if (structure.type === "if") changed = renderIf(structure) || changed;
        else changed = renderFor(structure) || changed;
      } catch (error) {
        runtime.reportError(error, runtime.contextFor(structure.anchor, structure.type, structure.source, "structure"));
      }
    });
    return changed;
  }

  function cleanup(structure) {
    if (!structure || structure.destroyed) return;
    structure.destroyed = true;
    if (structure.mounted) {
      runtime.cleanupTree(structure.mounted, structure.app);
      structure.mounted = null;
    }
    structure.rows.forEach(function (row) { runtime.cleanupTree(row.node, structure.app); });
    structure.rows.clear();
    structure.app.structures.delete(structure);
  }

  return {
    collect: collect,
    render: render,
    cleanup: cleanup
  };
}

module.exports = {
  createStructuralManager: createStructuralManager
};

},
21: function(module, exports, __require){
"use strict";

function createTaskService(options) {
  options = options || {};
  var globalObject = options.globalObject || (typeof globalThis !== "undefined" ? globalThis : {});
  var AbortControllerCtor = globalObject.AbortController || (typeof AbortController !== "undefined" ? AbortController : null);
  var DOMExceptionCtor = globalObject.DOMException || (typeof DOMException !== "undefined" ? DOMException : null);
  var owners = new WeakMap();

  function abortError(reason) {
    if (DOMExceptionCtor) return new DOMExceptionCtor(reason || "Aborted", "AbortError");
    var error = new Error(reason || "Aborted");
    error.name = "AbortError";
    return error;
  }

  function ownerMap(owner) {
    if (!owner || (typeof owner !== "object" && typeof owner !== "function")) {
      throw new TypeError("kit.task owner must be an object");
    }
    var map = owners.get(owner);
    if (!map) {
      map = new Map();
      owners.set(owner, map);
    }
    return map;
  }

  function abortRecord(record, reason) {
    if (!record || record.done) return false;
    record.aborted = true;
    try { record.controller.abort(abortError(reason || "aborted")); } catch (_) { /* noop */ }
    return true;
  }

  function run(owner, task, taskOptions) {
    taskOptions = taskOptions || {};
    if (!AbortControllerCtor) throw new Error("AbortController is required by kit.task");

    var key = taskOptions.key !== undefined ? taskOptions.key : Symbol("task");
    var map = ownerMap(owner);
    if (taskOptions.latest && map.has(key)) abortRecord(map.get(key), "superseded");

    var controller = new AbortControllerCtor();
    var record = {
      owner: owner,
      key: key,
      controller: controller,
      done: false,
      aborted: false,
      promise: null
    };
    map.set(key, record);

    var result;
    try {
      result = typeof task === "function"
        ? task({ signal: controller.signal, key: key, owner: owner })
        : task;
    } catch (error) {
      map.delete(key);
      record.done = true;
      throw error;
    }

    var promise = Promise.resolve(result).finally(function () {
      record.done = true;
      if (map.get(key) === record) map.delete(key);
    });
    record.promise = promise;
    if (typeof options.onTask === "function") options.onTask(record);
    return promise;
  }

  function latest(owner, key, task) {
    return run(owner, task, { key: key, latest: true });
  }

  function abort(owner, key, reason) {
    var map = owners.get(owner);
    if (!map) return false;

    // Passing `undefined` as the key intentionally aborts every task owned by
    // the component/application. This keeps lifecycle code explicit without
    // requiring a second public method.
    if (key !== undefined) {
      var record = map.get(key);
      if (!record) return false;
      var changed = abortRecord(record, reason || "aborted");
      map.delete(key);
      return changed;
    }

    var aborted = false;
    map.forEach(function (record) {
      aborted = abortRecord(record, reason || "aborted") || aborted;
    });
    map.clear();
    return aborted;
  }

  function delay(ms, delayOptions) {
    delayOptions = delayOptions || {};
    return new Promise(function (resolve, reject) {
      var timer = setTimeout(resolve, Math.max(0, Number(ms) || 0));
      var signal = delayOptions.signal;
      if (!signal) return;
      if (signal.aborted) {
        clearTimeout(timer);
        reject(signal.reason instanceof Error ? signal.reason : abortError(signal.reason || "Aborted"));
        return;
      }
      signal.addEventListener("abort", function () {
        clearTimeout(timer);
        reject(signal.reason instanceof Error ? signal.reason : abortError(signal.reason || "Aborted"));
      }, { once: true });
    });
  }

  function pending(owner) {
    var map = owners.get(owner);
    return map ? map.size : 0;
  }

  return Object.freeze({
    run: run,
    latest: latest,
    abort: abort,
    delay: delay,
    pending: pending
  });
}

module.exports = { createTaskService: createTaskService };

},
22: function(module, exports, __require){
"use strict";

function createRequestService(globalObject) {
  const active = new Map();
  const globalFetch = globalObject && globalObject.fetch
    ? globalObject.fetch.bind(globalObject)
    : null;

  function csrfToken() {
    const doc = globalObject && globalObject.document;
    if (!doc) return "";
    const meta = doc.querySelector('meta[name="csrf-token"],meta[name="csrf"]');
    return meta ? String(meta.content || "") : "";
  }

  function composeSignal(signal, timeout, key) {
    const Controller = globalObject.AbortController || AbortController;
    const controller = new Controller();
    let timeoutId = null;
    const abort = (reason) => {
      try { controller.abort(reason); } catch (_) { /* noop */ }
    };
    if (signal) {
      if (signal.aborted) abort(signal.reason);
      else signal.addEventListener("abort", () => abort(signal.reason), { once: true });
    }
    if (timeout > 0) timeoutId = setTimeout(() => abort("timeout"), timeout);
    if (key != null) {
      const previous = active.get(key);
      if (previous) previous.abort("superseded");
      active.set(key, controller);
    }
    return {
      controller,
      cleanup() {
        if (timeoutId) clearTimeout(timeoutId);
        if (key != null && active.get(key) === controller) active.delete(key);
      }
    };
  }

  async function parseResponse(response) {
    const type = String(response.headers.get("content-type") || "").toLowerCase();
    let data;
    if (type.indexOf("application/json") >= 0) data = await response.json();
    else data = await response.text();
    return {
      ok: response.ok,
      status: response.status,
      statusText: response.statusText,
      headers: response.headers,
      data,
      response
    };
  }

  async function request(url, requestOptions) {
    if (!globalFetch) throw new Error("Fetch API is not available");
    requestOptions = Object.assign({}, requestOptions || {});
    const key = requestOptions.key;
    const timeout = Math.max(0, Number(requestOptions.timeout) || 0);
    const signalRecord = composeSignal(requestOptions.signal, timeout, key);
    delete requestOptions.key;
    delete requestOptions.timeout;
    requestOptions.signal = signalRecord.controller.signal;
    const HeadersCtor = globalObject.Headers || Headers;
    requestOptions.headers = new HeadersCtor(requestOptions.headers || {});

    const method = String(requestOptions.method || "GET").toUpperCase();
    const token = csrfToken();
    if (token && method !== "GET" && method !== "HEAD" && !requestOptions.headers.has("X-CSRF-Token")) {
      requestOptions.headers.set("X-CSRF-Token", token);
    }

    if (requestOptions.json !== undefined) {
      requestOptions.body = JSON.stringify(requestOptions.json);
      requestOptions.headers.set("Content-Type", "application/json");
      delete requestOptions.json;
    }

    try {
      return await parseResponse(await globalFetch(url, requestOptions));
    } finally {
      signalRecord.cleanup();
    }
  }

  function get(url, options) {
    return request(url, Object.assign({}, options || {}, { method: "GET" }));
  }

  function post(url, body, options) {
    options = Object.assign({}, options || {}, { method: "POST" });
    const FormDataCtor = globalObject.FormData;
    const BlobCtor = globalObject.Blob;
    if ((FormDataCtor && body instanceof FormDataCtor) || typeof body === "string" || (BlobCtor && body instanceof BlobCtor)) {
      options.body = body;
    } else {
      options.json = body;
    }
    return request(url, options);
  }

  function submit(form, options) {
    if (!form || !form.tagName || String(form.tagName).toLowerCase() !== "form") {
      throw new TypeError("kit.request.submit() requires a form element");
    }
    options = Object.assign({}, options || {});
    const method = String(options.method || form.method || "GET").toUpperCase();
    const action = options.url || form.action || (globalObject.location && globalObject.location.href) || "";
    const data = new globalObject.FormData(form);
    if (method === "GET") {
      const url = new globalObject.URL(action, globalObject.location && globalObject.location.href);
      for (const pair of data.entries()) url.searchParams.append(pair[0], String(pair[1]));
      return request(url.toString(), Object.assign(options, { method: "GET" }));
    }
    return request(action, Object.assign(options, { method, body: data }));
  }

  function abort(key, reason) {
    const controller = active.get(key);
    if (!controller) return false;
    controller.abort(reason || "aborted");
    active.delete(key);
    return true;
  }

  return Object.freeze({ request, get, post, submit, abort });
}

module.exports = { createRequestService };

},
23: function(module, exports, __require){
"use strict";

var expressionApi = __require(8);
var lexicalApi = __require(9);
var utils = __require(10);
var errors = __require(11);
var records = __require(12);
var schedulerModule = __require(13);
var directiveRegistryModule = __require(14);
var serviceRegistryModule = __require(15);
var componentModule = __require(16);
var coreDirectivesModule = __require(17);
var modelModule = __require(18);
var eventModule = __require(19);
var structuralModule = __require(20);
var taskServiceModule = __require(21);
var requestServiceModule = __require(22);
var taskServiceModule = __require(21);
var requestServiceModule = __require(22);

var MODES = expressionApi.MODES;
var createExpressionEngine = expressionApi.createExpressionEngine;
var createLexicalEnvironment = lexicalApi.createLexicalEnvironment;
var hasOwn = utils.hasOwn;
var enqueueMicrotask = utils.enqueueMicrotask;
var nodeContains = utils.nodeContains;
var nodeDepth = utils.nodeDepth;
var isElement = utils.isElement;
var createNullObject = utils.createNullObject;
var createAppRecord = records.createAppRecord;
var createNodeRecord = records.createNodeRecord;
var createBindingRecord = records.createBindingRecord;
var createRuntimeError = errors.createRuntimeError;
var normalizeRuntimeError = errors.normalizeRuntimeError;

var RESERVED_ATTRIBUTES = new Set([
  "data-kit-app", "data-kit-component", "data-kit-as", "data-kit-scope", "data-kit-ref",
  "data-kit-if", "data-kit-for", "data-kit-key", "data-kit-persist"
]);

var PHASE_ORDER = { content: 10, form: 20 };

function createRuntime(globalObject, options) {
  options = Object.assign({
    development: false,
    autoStart: true,
    evaluationBudget: 10000,
    callDepthLimit: 64
  }, options || {});

  var global = globalObject || (typeof window !== "undefined" ? window : globalThis);
  var document = global.document || null;
  var kit = global.kit = global.kit || {};

  var runtime = {
    global: global,
    document: document,
    kit: kit,
    options: options,
    apps: new Set(),
    appsByRoot: new WeakMap(),
    nodeRecords: new WeakMap(),
    logicalOwners: new WeakMap(),
    modelBindings: new Set(),
    globalCleanups: [],
    warningKeys: new Set(),
    started: false,
    destroyed: false,
    publicApi: null
  };

  runtime.expression = createExpressionEngine({
    evaluationBudget: options.evaluationBudget,
    callDepthLimit: options.callDepthLimit
  });
  runtime.directives = directiveRegistryModule.createDirectiveRegistry();
  runtime.services = serviceRegistryModule.createServiceRegistry(kit);
  runtime.scheduler = schedulerModule.createScheduler(runtime);
  runtime.components = componentModule.createComponentManager(runtime);
  runtime.model = modelModule.createModelManager(runtime);
  runtime.events = eventModule.createEventManager(runtime);
  runtime.structural = structuralModule.createStructuralManager(runtime);

  // Core async services are ordinary service façades. Trusted component code sees
  // the concrete objects on `kit`; authored expressions see only explicitly
  // granted members through the curated service surface.
  runtime.task = taskServiceModule.createTaskService({
    onTask: function (taskRecord) {
      var owner = taskRecord && taskRecord.owner;
      if (!owner) return;
      runtime.apps.forEach(function (app) {
        app.components.forEach(function (componentRecord) {
          if (componentRecord.instance === owner) componentRecord.tasks.add(taskRecord);
        });
      });
      function releaseTask() {
        runtime.apps.forEach(function (app) {
          app.components.forEach(function (componentRecord) {
            componentRecord.tasks.delete(taskRecord);
          });
        });
      }
      Promise.resolve(taskRecord.promise).then(releaseTask, releaseTask);
    }
  });
  runtime.request = requestServiceModule.createRequestService(global);

  runtime.nodeRecord = function (node, app) {
    var record = runtime.nodeRecords.get(node);
    if (!record) {
      record = createNodeRecord(node, app || runtime.appForNode(node));
      runtime.nodeRecords.set(node, record);
    } else if (app && record.app !== app) {
      record.app = app;
    }
    return record;
  };
  runtime.peekNodeRecord = function (node) { return runtime.nodeRecords.get(node) || null; };
  runtime.logicalParent = function (node) { return runtime.logicalOwners.get(node) || null; };

  runtime.listen = function (target, type, handler, listenerOptions) {
    if (!target || !target.addEventListener) return function () {};
    target.addEventListener(type, handler, listenerOptions);
    var cleanup = function () { target.removeEventListener(type, handler, listenerOptions); };
    runtime.globalCleanups.push(cleanup);
    return cleanup;
  };

  runtime.warn = function (code, message, context) {
    if (!options.development) return;
    var key = code + "\u0000" + message;
    if (runtime.warningKeys.has(key)) return;
    runtime.warningKeys.add(key);
    if (global.console && typeof global.console.warn === "function") {
      global.console.warn("kitwork [" + code + "]: " + message, context || "");
    }
  };

  runtime.contextFor = function (node, directive, source, phase, event) {
    var app = runtime.appForNode(node);
    var component = app ? runtime.components.nearest(node && node.nodeType === 1 ? node : node && node.parentNode, app, true) : null;
    return {
      app: app,
      phase: phase || "runtime",
      directive: directive || "",
      source: source || "",
      element: node && node.nodeType === 1 ? node : null,
      node: node || null,
      host: component ? component.host : null,
      component: component ? component.instance : null,
      componentRecord: component || null,
      event: event || null
    };
  };

  runtime.reportError = function (error, context) {
    var normalized = normalizeRuntimeError(error, null, null, context || null);
    var componentRecord = context && context.componentRecord;
    var handled = false;
    var app = context && context.app;

    if (app && app.errorDepth > 4) return normalized;
    if (app) app.errorDepth++;
    try {
      if (componentRecord && componentRecord.instance && typeof componentRecord.instance.error === "function") {
        try { handled = componentRecord.instance.error(normalized, context || {}) === true; }
        catch (hookError) {
          normalized = normalizeRuntimeError(hookError, "KIT_ERROR_HOOK", "Component error hook failed", context);
        }
      }
      if (!handled && typeof kit.onError === "function") {
        try { handled = kit.onError(normalized, context || {}) === true; }
        catch (hookError2) {
          normalized = normalizeRuntimeError(hookError2, "KIT_ERROR_HOOK", "Global error hook failed", context);
        }
      }
    } finally {
      if (app) app.errorDepth--;
    }

    var root = app ? app.root : document;
    if (!handled && root && root.dispatchEvent && typeof global.CustomEvent === "function") {
      try {
        root.dispatchEvent(new global.CustomEvent("kitwork:error", {
          bubbles: true,
          detail: { error: normalized, context: context || {} }
        }));
      } catch (_) { /* older DOM */ }
    }
    if (!handled && options.development && global.console && typeof global.console.error === "function") {
      global.console.error("kitwork [" + normalized.code + "]: " + normalized.message, context || {}, normalized.cause || "");
    }
    return normalized;
  };

  runtime.appForNode = function (node) {
    if (!node) return runtime.apps.size === 1 ? Array.from(runtime.apps)[0] : null;
    var current = node.nodeType === 1 ? node : node.parentNode;
    while (current) {
      var app = runtime.appsByRoot.get(current);
      if (app && !app.destroyed) return app;
      current = runtime.logicalParent(current) || current.parentNode;
    }
    return null;
  };

  runtime.boundaryFor = function (node) {
    var app = runtime.appForNode(node);
    if (!app) return null;
    var component = runtime.components.nearest(node && node.nodeType === 1 ? node : node && node.parentNode, app, true);
    if (component) return component.host;
    var current = node && node.nodeType === 1 ? node : node && node.parentNode;
    while (current && current !== app.root.parentNode) {
      var record = runtime.peekNodeRecord(current);
      if (record && record.scope) return current;
      if (current === app.root) break;
      current = runtime.logicalParent(current) || current.parentNode;
    }
    return app.root;
  };

  function collectLoopFrames(node, app, extra) {
    var frames = [];
    (extra || []).forEach(function (frame) { frames.push(frame); });
    var current = node && node.nodeType === 1 ? node : node && node.parentNode;
    var seen = new Set(frames);
    while (current && current !== app.root.parentNode) {
      var record = runtime.peekNodeRecord(current);
      if (record && record.loopFrame && !seen.has(record.loopFrame)) {
        frames.push(record.loopFrame);
        seen.add(record.loopFrame);
      }
      if (current === app.root) break;
      current = runtime.logicalParent(current) || current.parentNode;
    }
    return frames;
  }

  function collectLocalScopes(node, app, componentRecord, prepended) {
    var scopes = [];
    (prepended || []).forEach(function (entry) {
      scopes.push(entry.scope ? entry : { scope: entry, boundary: node && node.nodeType === 1 ? node : null });
    });
    var current = node && node.nodeType === 1 ? node : node && node.parentNode;
    var stop = componentRecord ? componentRecord.host : app.root.parentNode;
    while (current && current !== stop) {
      var record = runtime.peekNodeRecord(current);
      if (record && record.scope) scopes.push({ scope: record.scope, boundary: current });
      if (current === app.root) break;
      current = runtime.logicalParent(current) || current.parentNode;
    }
    return scopes;
  }

  runtime.environmentFor = function (node, event, extra) {
    extra = extra || {};
    var app = extra.app || runtime.appForNode(node);
    if (!app) throw createRuntimeError("KIT_APP_MISSING", "No Kitwork application owns this node", { node: node });
    var element = extra.element || (node && node.nodeType === 1 ? node : node && node.parentNode);
    var componentRecord;
    if (hasOwn(extra, "componentRecord")) componentRecord = extra.componentRecord;
    else componentRecord = runtime.components.nearest(element, app, true);

    var contexts = createNullObject();
    contexts.$element = element || undefined;
    contexts.$host = componentRecord ? componentRecord.host : undefined;
    contexts.$event = event || undefined;
    contexts.$refs = componentRecord ? componentRecord.refs : undefined;
    contexts.$component = componentRecord ? componentRecord.instance : undefined;
    contexts.$parent = componentRecord && componentRecord.parent ? componentRecord.parent.instance : undefined;

    return createLexicalEnvironment({
      contexts: contexts,
      aliases: app.aliases,
      loopFrames: collectLoopFrames(node, app, extra.loopFrames),
      localScopes: collectLocalScopes(node, app, componentRecord, extra.prependScopes),
      component: componentRecord ? componentRecord.instance : null,
      componentBoundary: componentRecord ? componentRecord.host : null,
      appScope: app.scope,
      appBoundary: app.root,
      kit: runtime.services.publicSurface,
      onDirty: function (boundary, mutation) {
        runtime.scheduler.invalidate(app, boundary || runtime.boundaryFor(node), mutation);
      }
    });
  };

  runtime.evaluateNamedMapInto = function (compiled, node, app, target, extra) {
    if (!compiled || compiled.mode !== MODES.NAMED_MAP) {
      throw createRuntimeError("KIT_MAP_COMPILED", "Expected a named-map compiled expression");
    }
    var staged = target || createNullObject();
    var entries = compiled.ast.entries || [];
    for (var i = 0; i < entries.length; i++) {
      var entry = entries[i];
      var environment = runtime.environmentFor(node, null, Object.assign({}, extra || {}, {
        app: app,
        prependScopes: [{ scope: staged, boundary: node && node.nodeType === 1 ? node : app.root }].concat(extra && extra.prependScopes || [])
      }));
      var value = runtime.expression.evaluate({ mode: MODES.BINDING, source: entry.source, ast: entry.ast }, environment);
      staged[entry.key] = value;
    }
    return staged;
  };

  runtime.markFresh = function (node) {
    var app = runtime.appForNode(node);
    if (!app) return;
    var record = runtime.nodeRecord(node, app);
    record.fresh = true;
    enqueueMicrotask(function () { if (record) record.fresh = false; });
  };
  runtime.isFresh = function (node) {
    var record = runtime.peekNodeRecord(node);
    return !!(record && record.fresh);
  };

  runtime.registerPersist = function (element, app) {
    var record = runtime.nodeRecord(element, app);
    var next = String(element.getAttribute("data-kit-persist") || "").trim();
    if (record.persistKey && record.persistKey !== next && app.persisted.get(record.persistKey) === element) {
      app.persisted.delete(record.persistKey);
    }
    record.persistKey = "";
    if (!next) return;
    var existing = app.persisted.get(next);
    if (existing && existing !== element) {
      throw createRuntimeError("KIT_DUPLICATE_PERSIST_KEY", "Duplicate data-kit-persist key '" + next + "'", {
        key: next,
        element: element,
        existing: existing
      });
    }
    record.persistKey = next;
    app.persisted.set(next, element);
  };

  function initializeAppScope(app) {
    if (app.scopeInitialized) return;
    app.scopeInitialized = true;
    var source = app.root.getAttribute && app.root.getAttribute("data-kit-scope");
    if (!source) return;
    try {
      var compiled = runtime.expression.compile(MODES.NAMED_MAP, source);
      runtime.evaluateNamedMapInto(compiled, app.root, app, app.scope, { componentRecord: null });
    } catch (error) {
      runtime.reportError(error, runtime.contextFor(app.root, "scope", source, "app-scope"));
    }
  }

  function initializeLocalScope(element, app, record) {
    if (record.scopeInitialized) return;
    record.scopeInitialized = true;
    var source = element.getAttribute("data-kit-scope");
    if (!source) return;
    try {
      var target = createNullObject();
      var compiled = runtime.expression.compile(MODES.NAMED_MAP, source);
      runtime.evaluateNamedMapInto(compiled, element, app, target);
      record.scope = target;
    } catch (error) {
      runtime.reportError(error, runtime.contextFor(element, "scope", source, "scope"));
    }
  }

  function evaluateHostSeed(element, app, parentComponent) {
    var source = element.getAttribute("data-kit-scope");
    if (!source) return createNullObject();
    var target = createNullObject();
    var compiled = runtime.expression.compile(MODES.NAMED_MAP, source);
    runtime.evaluateNamedMapInto(compiled, element, app, target, {
      componentRecord: parentComponent || null
    });
    return target;
  }

  function reconcileRef(element, app, componentRecord, isComponentHost) {
    var record = runtime.nodeRecord(element, app);
    var nextName = String(element.getAttribute("data-kit-ref") || "").trim();
    var owner = isComponentHost ? (componentRecord && componentRecord.parent) : componentRecord;
    if (record.refName && (record.refName !== nextName || record.refOwner !== owner)) {
      runtime.components.removeRef(record.refOwner, record.refName, element);
      record.refName = "";
      record.refOwner = null;
    }
    if (!nextName) return;
    try {
      runtime.components.registerRef(owner, nextName, element);
      record.refName = nextName;
      record.refOwner = owner;
    } catch (error) {
      runtime.reportError(error, runtime.contextFor(element, "ref", nextName, "reconcile"));
    }
  }

  function cleanupBinding(binding) {
    if (!binding || binding.destroyed) return;
    binding.destroyed = true;
    if (binding.contract && typeof binding.contract.unmount === "function") {
      try { binding.contract.unmount(binding, runtime); }
      catch (error) { runtime.reportError(error, runtime.contextFor(binding.element, binding.attributeName, binding.source, "binding-unmount")); }
    }
    runtime.events.unregister(binding);
    if (typeof binding.cleanup === "function") {
      try { binding.cleanup(); } catch (error2) { runtime.reportError(error2, runtime.contextFor(binding.element, binding.attributeName, binding.source, "binding-cleanup")); }
    }
    binding.app.bindings.delete(binding);
    var nodeRecord = runtime.peekNodeRecord(binding.element);
    if (nodeRecord) nodeRecord.bindings.delete(binding.attributeName);
  }
  runtime.cleanupBinding = cleanupBinding;

  function createDirectiveBinding(element, app, attribute, directiveName, contract) {
    var source = attribute.value;
    var compiled = runtime.expression.compile(contract.mode, source);
    var binding = createBindingRecord(app, element, attribute.name, directiveName, contract, source, compiled);
    if (typeof contract.validate === "function") contract.validate(binding, runtime);
    if (typeof contract.mount === "function") contract.mount(binding, runtime);
    runtime.nodeRecord(element, app).bindings.set(attribute.name, binding);
    app.bindings.add(binding);
    return binding;
  }

  function createEventBinding(element, app, attribute, eventSpec) {
    var contract = { name: eventSpec.type, mode: MODES.ACTION, phase: "event" };
    var compiled = runtime.expression.compile(MODES.ACTION, attribute.value);
    var binding = createBindingRecord(app, element, attribute.name, eventSpec.type, contract, attribute.value, compiled);
    runtime.nodeRecord(element, app).bindings.set(attribute.name, binding);
    app.bindings.add(binding);
    runtime.events.register(binding, eventSpec);
    return binding;
  }

  runtime.reconcileBindings = function (element, app) {
    var record = runtime.nodeRecord(element, app);
    var active = new Set();
    var attributes = Array.prototype.slice.call(element.attributes || []);

    attributes.forEach(function (attribute) {
      if (attribute.name.indexOf("data-kit-") !== 0 || RESERVED_ATTRIBUTES.has(attribute.name)) return;
      var directiveName = attribute.name.slice(9);
      var contract = runtime.directives.get(directiveName);
      var eventSpec = null;
      if (!contract) {
        try { eventSpec = runtime.events.parseAttribute(attribute.name); }
        catch (error) {
          runtime.reportError(error, runtime.contextFor(element, attribute.name, attribute.value, "event-parse"));
          return;
        }
      }
      if (!contract && !eventSpec) {
        if (directiveName === "teleport" || directiveName === "transition") {
          runtime.warn("KIT_CAPABILITY_DEFERRED", "Directive '" + directiveName + "' requires an optional capability", { element: element });
        } else {
          runtime.warn("KIT_UNKNOWN_DIRECTIVE", "Unknown Kitwork directive '" + attribute.name + "'", { element: element });
        }
        return;
      }

      active.add(attribute.name);
      var existing = record.bindings.get(attribute.name);
      if (existing && existing.source === attribute.value) return;
      if (existing) cleanupBinding(existing);
      try {
        if (contract) createDirectiveBinding(element, app, attribute, directiveName, contract);
        else createEventBinding(element, app, attribute, eventSpec);
      } catch (error2) {
        runtime.reportError(error2, runtime.contextFor(element, attribute.name, attribute.value, "binding-create"));
      }
    });

    Array.from(record.bindings.entries()).forEach(function (entry) {
      if (!active.has(entry[0])) cleanupBinding(entry[1]);
    });
  };

  runtime.hydrateTree = function hydrateTree(node, app) {
    if (!node || !app || app.destroyed) return;
    if (node.nodeType !== 1) {
      runtime.nodeRecord(node, app).hydrated = true;
      return;
    }

    if (node !== app.root && node.hasAttribute("data-kit-app")) return;

    if (node.hasAttribute("data-kit-if") || node.hasAttribute("data-kit-for")) {
      try { runtime.structural.collect(node, app); }
      catch (error) { runtime.reportError(error, runtime.contextFor(node, "structure", "", "hydrate")); }
      return;
    }

    var record = runtime.nodeRecord(node, app);
    if (record.destroyed) return;

    var componentName = String(node.getAttribute("data-kit-component") || "").trim();
    var componentRecord = null;
    if (componentName) {
      if (node === app.root) {
        runtime.reportError(createRuntimeError("KIT_APP_COMPONENT_CONFLICT", "data-kit-app and data-kit-component cannot share one element in Runtime 1.0", {
          element: node
        }), runtime.contextFor(node, "component", componentName, "hydrate"));
      }
      var parentComponent = runtime.components.nearest(node, app, false);
      var seed = createNullObject();
      try { seed = evaluateHostSeed(node, app, parentComponent); }
      catch (error2) { runtime.reportError(error2, runtime.contextFor(node, "scope", node.getAttribute("data-kit-scope") || "", "component-seed")); }
      componentRecord = runtime.components.ensure(app, node, componentName, parentComponent, seed);
      record.component = componentRecord;
      record.scopeInitialized = true;
    } else {
      record.component = runtime.components.nearest(node, app, true);
      if (node !== app.root && node.hasAttribute("data-kit-scope")) initializeLocalScope(node, app, record);
    }

    reconcileRef(node, app, componentRecord || record.component, !!componentName);
    if (node.hasAttribute("data-kit-persist")) {
      try { runtime.registerPersist(node, app); }
      catch (error3) { runtime.reportError(error3, runtime.contextFor(node, "persist", node.getAttribute("data-kit-persist"), "hydrate")); }
    }
    runtime.reconcileBindings(node, app);
    record.hydrated = true;

    var children = Array.prototype.slice.call(node.childNodes || []);
    for (var i = 0; i < children.length; i++) runtime.hydrateTree(children[i], app);
  };

  runtime.cleanupTree = function cleanupTree(node, expectedApp) {
    if (!node) return;
    var children = Array.prototype.slice.call(node.childNodes || []);
    for (var i = children.length - 1; i >= 0; i--) cleanupTree(children[i], expectedApp);

    var record = runtime.peekNodeRecord(node);
    if (!record || record.destroyed || expectedApp && record.app !== expectedApp) return;
    record.destroyed = true;

    Array.from(record.bindings.values()).forEach(cleanupBinding);
    if (record.refName) runtime.components.removeRef(record.refOwner, record.refName, node);
    if (record.persistKey && record.app.persisted.get(record.persistKey) === node) record.app.persisted.delete(record.persistKey);
    if (record.structure) runtime.structural.cleanup(record.structure);
    // Descendant node records point at their owning component for context
    // resolution. Only the actual component host owns the instance lifecycle.
    if (record.component && record.component.host === node) {
      runtime.components.unmount(record.component);
    }
    while (record.cleanups.length) {
      try { record.cleanups.pop()(); }
      catch (error) { runtime.reportError(error, runtime.contextFor(node, "cleanup", "", "cleanup")); }
    }
    runtime.nodeRecords.delete(node);
  };

  function renderBinding(binding) {
    if (!binding || binding.disabled || !binding.element.isConnected) return;
    try {
      var environment = runtime.environmentFor(binding.element, null);
      var result = runtime.expression.execute(binding.compiled, environment);
      binding.ownerBoundary = runtime.boundaryFor(binding.element);
      if (binding.contract && typeof binding.contract.update === "function") binding.contract.update(binding, result, runtime);
      binding.initialized = true;
    } catch (error) {
      runtime.reportError(error, runtime.contextFor(binding.element, binding.attributeName, binding.source, "render"));
    }
  }

  runtime.renderBoundaries = function (app, boundaries) {
    app.renderCount++;
    boundaries.forEach(function (boundary) {
      var structurePass = 0;
      while (structurePass++ < 8 && runtime.structural.render(app, boundary)) { /* settle nested structure */ }

      var bindings = Array.from(app.bindings).filter(function (binding) {
        return !binding.destroyed && binding.phase !== "event" && binding.element.isConnected &&
          (boundary === app.root || nodeContains(boundary, binding.element));
      });
      bindings.sort(function (left, right) {
        var phase = (PHASE_ORDER[left.phase] || 50) - (PHASE_ORDER[right.phase] || 50);
        if (phase) return phase;
        return nodeDepth(left.element) - nodeDepth(right.element);
      });
      bindings.forEach(renderBinding);
      runtime.components.mountPending(app, boundary);
    });
  };

  function setupObserver(app) {
    if (!global.MutationObserver || !app.root) return;
    app.observer = new global.MutationObserver(function (mutations) {
      mutations.forEach(function (mutation) {
        if (mutation.type === "childList") {
          Array.prototype.slice.call(mutation.addedNodes || []).forEach(function (node) {
            if (!node.isConnected) return;
            var owner = runtime.appForNode(node) || app;
            if (owner !== app) return;

            // Moving a hydrated subtree across application roots changes every
            // ownership contract (scope, aliases, tasks, scheduler and errors).
            // Tear down the old app record before hydrating it into the new app.
            var previousRecord = runtime.peekNodeRecord(node);
            if (previousRecord && previousRecord.app && previousRecord.app !== app) {
              runtime.cleanupTree(node, previousRecord.app);
            }
            runtime.hydrateTree(node, app);
            var boundaryNode = node.nodeType === 1 ? node : node.parentNode;
            runtime.scheduler.invalidate(app, runtime.boundaryFor(boundaryNode || app.root), {
              type: "mutation-add"
            });
          });
          Array.prototype.slice.call(mutation.removedNodes || []).forEach(function (node) {
            app.removedNodes.add(node);
          });
          return;
        }

        // The runtime owns class/style/aria/data/hidden output mutations. Re-rendering
        // because of those mutations creates a feedback loop. Only authored Kitwork
        // directive changes are reconciled through the observer.
        if (mutation.type === "attributes" && mutation.attributeName &&
            mutation.attributeName.indexOf("data-kit-") === 0) {
          var element = mutation.target;
          if (element.isConnected && runtime.appForNode(element) === app) {
            runtime.hydrateTree(element, app);
            runtime.scheduler.invalidate(app, runtime.boundaryFor(element), {
              type: "directive-attribute",
              name: mutation.attributeName
            });
          }
        }
      });

      if (app.removedNodes.size) enqueueMicrotask(function () {
        Array.from(app.removedNodes).forEach(function (node) {
          app.removedNodes.delete(node);
          var currentApp = node.isConnected ? runtime.appForNode(node) : null;
          // A DOM move inside the same application keeps logical ownership and must
          // not unmount. A real removal, or a move across app roots, is cleaned up.
          if (!node.isConnected || currentApp !== app) runtime.cleanupTree(node, app);
        });
      });
    });
    app.observer.observe(app.root, { childList: true, subtree: true, attributes: true });
  }

  function createApp(root, id) {
    if (runtime.appsByRoot.has(root)) return runtime.appsByRoot.get(root);
    var app = createAppRecord(root, id || root.getAttribute && root.getAttribute("data-kit-app") || "main");
    runtime.apps.add(app);
    runtime.appsByRoot.set(root, app);
    initializeAppScope(app);
    setupObserver(app);
    runtime.hydrateTree(root, app);
    app.initialized = true;
    runtime.scheduler.invalidate(app, root, { type: "app-start" });
    runtime.scheduler.flush(app);
    return app;
  }

  function discoverRoots() {
    var roots = Array.prototype.slice.call(document.querySelectorAll("[data-kit-app]"));
    if (!roots.length) return [document.documentElement];
    roots.forEach(function (root) {
      var parent = root.parentElement && root.parentElement.closest("[data-kit-app]");
      if (parent) throw createRuntimeError("KIT_APP_NESTED", "Nested data-kit-app roots are not allowed", {
        root: root,
        parent: parent
      });
    });
    return roots;
  }

  runtime.start = function (root) {
    if (!document || runtime.destroyed) return runtime.publicApi;
    if (root) createApp(root, root.getAttribute && root.getAttribute("data-kit-app") || "main");
    else discoverRoots().forEach(function (candidate) { createApp(candidate); });
    runtime.started = true;
    if (document && document.dispatchEvent && typeof global.CustomEvent === "function") {
      document.dispatchEvent(new global.CustomEvent("kitwork:ready", {
        detail: { runtime: kit.runtime, apps: runtime.apps.size }
      }));
    }
    return runtime.publicApi;
  };

  runtime.destroyApp = function (app) {
    if (!app || app.destroyed) return;
    app.destroyed = true;
    if (runtime.task && typeof runtime.task.abort === "function") {
      try {
        runtime.task.abort(app, undefined, "app-destroy");
        runtime.task.abort(app.scope, undefined, "app-destroy");
      } catch (error) {
        runtime.reportError(error, { app: app, phase: "task-abort", directive: "data-kit-app", source: app.name });
      }
    }
    if (app.observer) app.observer.disconnect();
    runtime.cleanupTree(app.root, app);
    app.aliases.clear();
    app.bindings.clear();
    app.structures.clear();
    app.persisted.clear();
    app.dirtyBoundaries.clear();
    runtime.apps.delete(app);
    runtime.appsByRoot.delete(app.root);
  };

  runtime.destroy = function (root) {
    if (root) runtime.destroyApp(runtime.appsByRoot.get(root));
    else Array.from(runtime.apps).forEach(runtime.destroyApp);
    if (!root) {
      while (runtime.globalCleanups.length) {
        try { runtime.globalCleanups.pop()(); } catch (_) { /* cleanup */ }
      }
      runtime.destroyed = true;
      runtime.started = false;
    }
  };

  runtime.render = function (target) {
    if (!target) {
      runtime.apps.forEach(function (app) { runtime.scheduler.invalidate(app, app.root, { type: "manual" }); });
      return;
    }
    if (target.nodeType) {
      var app = runtime.appForNode(target);
      if (app) runtime.scheduler.invalidate(app, runtime.boundaryFor(target), { type: "manual" });
      return;
    }
    runtime.apps.forEach(function (app) {
      app.components.forEach(function (record) {
        if (record.instance === target) runtime.scheduler.invalidate(app, record.host, { type: "manual" });
      });
    });
  };

  runtime.inspect = function (element) {
    var app = runtime.appForNode(element);
    var record = runtime.peekNodeRecord(element);
    var component = app ? runtime.components.nearest(element, app, true) : null;
    return {
      app: app,
      node: record,
      component: component,
      scope: record && record.scope,
      refs: component && component.refs,
      aliases: app ? Array.from(app.aliases.keys()) : [],
      bindings: record ? Array.from(record.bindings.keys()) : [],
      dirtyBoundaries: app ? Array.from(app.dirtyBoundaries) : []
    };
  };

  coreDirectivesModule.installCoreDirectives(runtime);
  runtime.model.install(document);

  // Built-in runtime services are installed through the same public service
  // registry as application capabilities. Trusted JavaScript always receives
  // the concrete service. Authored expressions see only the explicitly granted
  // request members; task orchestration remains a component-method concern.
  runtime.services.register("task", runtime.task, { expression: [] });
  runtime.services.register("request", runtime.request, {
    expression: ["get", "post", "submit", "abort"]
  });

  kit.runtime = Object.assign(kit.runtime && typeof kit.runtime === "object" ? kit.runtime : {}, {
    name: "kitwork",
    version: "3.0.0-draft.m2",
    specification: "0.7.0-draft",
    development: !!options.development,
    loaded: true,
    booted: false
  });

  kit.component = function (name, definition) {
    if (arguments.length === 1) return runtime.components.get(name);
    return runtime.components.register(name, definition);
  };
  kit.service = function (name, implementation, serviceOptions) {
    if (arguments.length === 1) return runtime.services.get(name);
    return runtime.services.register(name, implementation, serviceOptions);
  };
  kit.start = function (root) {
    var result = runtime.start(root);
    kit.runtime.booted = true;
    return result;
  };
  kit.destroy = function (root) {
    runtime.destroy(root);
    if (!root) kit.runtime.booted = false;
  };
  kit.render = runtime.render;
  kit.onError = typeof kit.onError === "function" ? kit.onError : function () { return false; };
  kit.dev = kit.dev || {};
  kit.dev.inspect = runtime.inspect;

  runtime.publicApi = kit;
  kit.internal = Object.freeze({
    runtime: runtime,
    persist: Object.freeze({
      find: function (root, key) {
        var app = root && root.root ? root : runtime.appsByRoot.get(root);
        return app ? app.persisted.get(String(key)) || null : null;
      }
    }),
    hydrate: runtime.hydrateTree,
    cleanupTree: runtime.cleanupTree,
    invalidate: function (node) {
      var app = runtime.appForNode(node);
      if (app) runtime.scheduler.invalidate(app, runtime.boundaryFor(node), { type: "internal" });
    }
  });

  if (document && options.autoStart !== false) {
    if (document.readyState === "loading") {
      runtime.listen(document, "DOMContentLoaded", function () { if (!runtime.started) kit.start(); }, { once: true });
    } else enqueueMicrotask(function () { if (!runtime.started) kit.start(); });
  }

  return runtime;
}

module.exports = {
  createRuntime: createRuntime
};

},
24: function(module, exports, __require){
"use strict";

var core = __require(23);

function install(globalObject, options) {
  var global = globalObject || (typeof window !== "undefined" ? window : globalThis);
  var kit = global.kit = global.kit || {};
  if (kit.internal && kit.internal.runtime && kit.internal.runtime.options) return kit.internal.runtime;
  return core.createRuntime(global, options || global.KITWORK_RUNTIME_OPTIONS || {});
}

module.exports = {
  createRuntime: core.createRuntime,
  install: install
};

if (typeof window !== "undefined" && window.document) install(window);

}
};
var __cache = {};
function __require(id){
  if(__cache[id]) return __cache[id].exports;
  var module = __cache[id] = { exports: {} };
  __modules[id](module, module.exports, __require);
  return module.exports;
}
var api = __require(24);
if (global) global.KitworkRuntimeNext = api;
})(typeof window !== "undefined" ? window : globalThis);
