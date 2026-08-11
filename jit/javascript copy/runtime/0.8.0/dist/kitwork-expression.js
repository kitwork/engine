/*
 * Kitwork Client Runtime — Expression Engine M1
 *
 * Milestone 1 extracted and improved from the current reference kernel.
 * This module intentionally contains no DOM, component, Drive, or scheduler code.
 * Runtime integration supplies an Environment adapter for lexical ownership.
 *
 * Author expressions are always:
 *   source -> lexer -> parser(mode) -> private cached AST -> evaluator
 *
 * No eval. No new Function. No serialized/public IR contract.
 */
(function (root, factory) {
  "use strict";

  var api = factory();

  if (typeof module === "object" && module && module.exports) {
    module.exports = api;
    return;
  }

  // Development/reference attachment only. Production composition should keep
  // the engine in a closure and expose it through kit.internal, not public kit.*.
  try {
    Object.defineProperty(root, "KitworkExpression", {
      value: api,
      configurable: true,
      enumerable: false,
      writable: false
    });
  } catch (_) {
    root.KitworkExpression = api;
  }
})(typeof globalThis !== "undefined" ? globalThis : this, function () {
  "use strict";

  var DEFAULT_EVALUATION_BUDGET = 10000;
  var DEFAULT_CALL_DEPTH = 64;
  var DEFAULT_CACHE_ENTRIES = 2048;

  var MODES = Object.freeze({
    NAMED_MAP: "named-map",
    BINDING: "binding",
    CLASS_VALUE: "class-value",
    ACTION: "action",
    WRITABLE_PATH: "writable-path",
    IDENTITY: "identity",
    ITERATOR: "iterator"
  });

  var MODE_ALIASES = Object.freeze({
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

  var RESERVED_ASSIGNMENT_ROOTS = createWordSet(
    "$element $host $event $refs $parent $index kit"
  );

  var BLOCKED_MEMBERS = createWordSet(
    "constructor prototype __proto__ __defineGetter__ __defineSetter__ " +
    "__lookupGetter__ __lookupSetter__ ownerDocument defaultView contentWindow " +
    "window globalThis top parent self"
  );

  var FORBIDDEN_KEYWORDS = createWordSet(
    "var let const function class return if else for while do switch case new " +
    "delete void typeof instanceof in await yield throw try catch finally import export"
  );

  function createWordSet(source) {
    var output = Object.create(null);
    String(source || "").split(/\s+/).forEach(function (word) {
      if (word) output[word] = true;
    });
    return output;
  }

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
    var current = ast;
    while (current && current.type === "MemberExpression") current = current.object;
    return current && current.type === "Identifier" ? current.name : "";
  }

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

  function error(code, message, details, cause) {
    return new KitworkExpressionError(code, message, details, cause);
  }

  // -------------------------------------------------------------------------
  // Source scanners shared by templates and named-map/class-map parsing.
  // -------------------------------------------------------------------------

  function skipQuotedSource(source, index, quote) {
    index++; // opening quote
    while (index < source.length) {
      var character = source[index++];
      if (character === "\\") {
        if (index < source.length) index++;
        continue;
      }
      if (character === quote) return index;
    }
    throw error("KIT_PARSE_UNTERMINATED_STRING", "Unterminated string literal", {
      source: source,
      position: index
    });
  }

  function skipTemplateSource(source, index) {
    index++; // opening backtick
    while (index < source.length) {
      var character = source[index];
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
    throw error("KIT_PARSE_UNTERMINATED_TEMPLATE", "Unterminated template literal", {
      source: source,
      position: index
    });
  }

  function skipBracedExpressionSource(source, index) {
    var depth = 1;
    while (index < source.length) {
      var character = source[index];
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
    throw error("KIT_PARSE_UNTERMINATED_TEMPLATE_EXPRESSION", "Unterminated template interpolation", {
      source: source,
      position: index
    });
  }

  function scanTopLevel(source, visitor) {
    var round = 0;
    var square = 0;
    var curly = 0;
    var index = 0;

    while (index < source.length) {
      var character = source[index];
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
        throw error("KIT_PARSE_UNBALANCED_DELIMITER", "Unbalanced delimiter", {
          source: source,
          position: index
        });
      }
      index++;
    }

    if (round !== 0 || square !== 0 || curly !== 0) {
      throw error("KIT_PARSE_UNBALANCED_DELIMITER", "Unbalanced delimiter", {
        source: source,
        position: source.length
      });
    }
  }

  // -------------------------------------------------------------------------
  // Lexer
  // -------------------------------------------------------------------------

  function Lexer(source) {
    this.source = String(source == null ? "" : source);
    this.length = this.source.length;
    this.index = 0;
    this.tokens = [];
  }

  Lexer.prototype.raise = function (code, message, position) {
    throw error(code, message, {
      source: this.source,
      position: position == null ? this.index : position
    });
  };

  Lexer.prototype.push = function (type, value, start, end) {
    this.tokens.push({
      type: type,
      value: value,
      start: start,
      end: end == null ? this.index : end
    });
  };

  Lexer.prototype.scanString = function (quote) {
    var start = this.index++;
    var value = "";

    while (this.index < this.length) {
      var character = this.source[this.index++];
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

      var escaped = this.source[this.index++];
      if (escaped === "n") value += "\n";
      else if (escaped === "r") value += "\r";
      else if (escaped === "t") value += "\t";
      else if (escaped === "b") value += "\b";
      else if (escaped === "f") value += "\f";
      else if (escaped === "v") value += "\v";
      else if (escaped === "0") value += "\0";
      else if (escaped === "u") {
        var hex = this.source.slice(this.index, this.index + 4);
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
    var start = this.index;
    var source = this.source;
    var index = this.index;

    if (source[index] === ".") index++;
    while (index < this.length && /[0-9]/.test(source[index])) index++;

    if (source[index] === ".") {
      index++;
      while (index < this.length && /[0-9]/.test(source[index])) index++;
    }

    if (source[index] === "e" || source[index] === "E") {
      index++;
      if (source[index] === "+" || source[index] === "-") index++;
      var exponentStart = index;
      while (index < this.length && /[0-9]/.test(source[index])) index++;
      if (index === exponentStart) {
        this.raise("KIT_PARSE_INVALID_NUMBER", "Exponent requires at least one digit", start);
      }
    }

    var raw = source.slice(start, index);
    var value = Number(raw);
    if (!raw || !Number.isFinite(value)) {
      this.raise("KIT_PARSE_INVALID_NUMBER", "Invalid number literal", start);
    }

    this.index = index;
    this.push("literal", value, start, index);
  };

  Lexer.prototype.scanIdentifier = function () {
    var start = this.index;
    var value = "";

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
    var start = this.index;
    var end = skipBracedExpressionSource(this.source, this.index);
    // skipBracedExpressionSource starts after `${` with depth=1 and returns after `}`.
    var result = this.source.slice(start, end - 1);
    this.index = end;
    return result;
  };

  Lexer.prototype.scanTemplate = function () {
    var start = this.index++;
    var quasis = [];
    var expressions = [];
    var current = "";

    while (this.index < this.length) {
      var character = this.source[this.index++];
      if (character === "`") {
        quasis.push(current);
        this.push("template", {
          quasis: quasis,
          expressions: expressions
        }, start, this.index);
        return;
      }

      if (character === "\\") {
        if (this.index >= this.length) {
          this.raise("KIT_PARSE_UNTERMINATED_TEMPLATE", "Unterminated template escape", start);
        }
        var escaped = this.source[this.index++];
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
      var character = this.source[this.index];

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

      var start = this.index;
      var three = this.source.slice(this.index, this.index + 3);
      var two = this.source.slice(this.index, this.index + 2);

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

  // -------------------------------------------------------------------------
  // Parser
  // -------------------------------------------------------------------------

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
    var token = this.current();
    return token && token.type === "operator" && token.value === value;
  };

  Parser.prototype.match = function (value) {
    if (!this.is(value)) return false;
    this.index++;
    return true;
  };

  Parser.prototype.expect = function (value) {
    var token = this.current();
    if (!this.is(value)) {
      throw error(
        "KIT_PARSE_EXPECTED_TOKEN",
        "Expected '" + value + "' but found '" + (token ? token.value : "<end>") + "'",
        { source: this.source, position: token ? token.start : this.source.length }
      );
    }
    this.index++;
    return token;
  };

  Parser.prototype.expectIdentifier = function () {
    var token = this.current();
    if (!token || token.type !== "identifier") {
      throw error(
        "KIT_PARSE_EXPECTED_IDENTIFIER",
        "Expected identifier",
        { source: this.source, position: token ? token.start : this.source.length }
      );
    }
    this.index++;
    return token;
  };

  Parser.prototype.parseProgram = function () {
    var body = [];

    while (this.current().type !== "eof") {
      body.push(this.parseAssignment());
      if (this.match(";")) {
        while (this.match(";")) { /* Empty statements are ignored. */ }
      } else if (this.current().type !== "eof") {
        throw error(
          "KIT_PARSE_ACTION_SEPARATOR",
          "Expected ';' between action expressions",
          { source: this.source, position: this.current().start }
        );
      }
    }

    return node("Program", 0, this.source.length, { body: body });
  };

  Parser.prototype.parseSingleExpression = function () {
    var expression = this.parseAssignment();
    if (this.current().type !== "eof") {
      throw error(
        "KIT_PARSE_TRAILING_INPUT",
        "Unexpected trailing input near '" + this.current().value + "'",
        { source: this.source, position: this.current().start }
      );
    }
    return expression;
  };

  Parser.prototype.parseAssignment = function () {
    var left = this.parseConditional();
    if (!this.match("=")) return left;

    if (!this.options.allowAssignment) {
      throw error(
        "KIT_BINDING_ASSIGNMENT",
        "Assignment is not allowed in this parser mode",
        { source: this.source, position: this.previous().start }
      );
    }

    if (!isWritableAst(left)) {
      throw error(
        "KIT_INVALID_ASSIGNMENT_TARGET",
        "Assignment target must be an identifier or writable member path",
        { source: this.source, position: left.start }
      );
    }

    var right = this.parseAssignment();
    return node("AssignmentExpression", left.start, right.end, {
      operator: "=",
      left: left,
      right: right
    });
  };

  Parser.prototype.parseConditional = function () {
    var test = this.parseNullish();
    if (!this.match("?")) return test;

    var consequent = this.parseAssignment();
    this.expect(":");
    var alternate = this.parseAssignment();

    return node("ConditionalExpression", test.start, alternate.end, {
      test: test,
      consequent: consequent,
      alternate: alternate
    });
  };

  Parser.prototype.parseNullish = function () {
    var expression = this.parseOr();
    while (this.match("??")) {
      var right = this.parseOr();
      expression = node("LogicalExpression", expression.start, right.end, {
        operator: "??",
        left: expression,
        right: right
      });
    }
    return expression;
  };

  Parser.prototype.parseOr = function () {
    var expression = this.parseAnd();
    while (this.match("||")) {
      var right = this.parseAnd();
      expression = node("LogicalExpression", expression.start, right.end, {
        operator: "||",
        left: expression,
        right: right
      });
    }
    return expression;
  };

  Parser.prototype.parseAnd = function () {
    var expression = this.parseEquality();
    while (this.match("&&")) {
      var right = this.parseEquality();
      expression = node("LogicalExpression", expression.start, right.end, {
        operator: "&&",
        left: expression,
        right: right
      });
    }
    return expression;
  };

  Parser.prototype.parseEquality = function () {
    var expression = this.parseRelational();
    while (this.is("===") || this.is("!==")) {
      var operator = this.next();
      var right = this.parseRelational();
      expression = node("BinaryExpression", expression.start, right.end, {
        operator: operator.value,
        left: expression,
        right: right
      });
    }
    return expression;
  };

  Parser.prototype.parseRelational = function () {
    var expression = this.parseAdditive();
    while (this.is("<") || this.is(">") || this.is("<=") || this.is(">=")) {
      var operator = this.next();
      var right = this.parseAdditive();
      expression = node("BinaryExpression", expression.start, right.end, {
        operator: operator.value,
        left: expression,
        right: right
      });
    }
    return expression;
  };

  Parser.prototype.parseAdditive = function () {
    var expression = this.parseMultiplicative();
    while (this.is("+") || this.is("-")) {
      var operator = this.next();
      var right = this.parseMultiplicative();
      expression = node("BinaryExpression", expression.start, right.end, {
        operator: operator.value,
        left: expression,
        right: right
      });
    }
    return expression;
  };

  Parser.prototype.parseMultiplicative = function () {
    var expression = this.parseUnary();
    while (this.is("*") || this.is("/") || this.is("%")) {
      var operator = this.next();
      var right = this.parseUnary();
      expression = node("BinaryExpression", expression.start, right.end, {
        operator: operator.value,
        left: expression,
        right: right
      });
    }
    return expression;
  };

  Parser.prototype.parseUnary = function () {
    if (this.is("!") || this.is("-") || this.is("+")) {
      var operator = this.next();
      var argument = this.parseUnary();
      return node("UnaryExpression", operator.start, argument.end, {
        operator: operator.value,
        argument: argument
      });
    }
    return this.parsePostfix();
  };

  Parser.prototype.parsePostfix = function () {
    var expression = this.parsePrimary();

    while (true) {
      if (this.match(".")) {
        var property = this.expectIdentifier();
        expression = node("MemberExpression", expression.start, property.end, {
          object: expression,
          property: node("Literal", property.start, property.end, { value: property.value }),
          computed: false,
          optional: false
        });
        continue;
      }

      if (this.match("?.")) {
        var optionalStart = this.previous().start;
        if (this.match("[")) {
          var optionalProperty = this.parseAssignment();
          var optionalClose = this.expect("]");
          expression = node("MemberExpression", expression.start, optionalClose.end, {
            object: expression,
            property: optionalProperty,
            computed: true,
            optional: true
          });
          continue;
        }
        if (this.match("(")) {
          var optionalArgs = this.parseArgumentsAfterOpen();
          expression = node("CallExpression", expression.start, optionalArgs.end, {
            callee: expression,
            arguments: optionalArgs.arguments,
            optional: true
          });
          continue;
        }
        var optionalName = this.expectIdentifier();
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
        var computedProperty = this.parseAssignment();
        var computedClose = this.expect("]");
        expression = node("MemberExpression", expression.start, computedClose.end, {
          object: expression,
          property: computedProperty,
          computed: true,
          optional: false
        });
        continue;
      }

      if (this.match("(")) {
        var args = this.parseArgumentsAfterOpen();
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
    var args = [];
    if (!this.match(")")) {
      do {
        args.push(this.parseAssignment());
      } while (this.match(","));
      var close = this.expect(")");
      return { arguments: args, end: close.end };
    }
    return { arguments: args, end: this.previous().end };
  };

  Parser.prototype.parsePrimary = function () {
    var token = this.current();

    if (token.type === "literal") {
      this.next();
      return node("Literal", token.start, token.end, { value: token.value });
    }

    if (token.type === "template") {
      this.next();
      var expressions = [];
      for (var i = 0; i < token.value.expressions.length; i++) {
        expressions.push(parseBinding(token.value.expressions[i]));
      }
      return node("TemplateLiteral", token.start, token.end, {
        quasis: token.value.quasis.slice(),
        expressions: expressions
      });
    }

    if (token.type === "identifier") {
      this.next();
      return node("Identifier", token.start, token.end, { name: token.value });
    }

    if (this.match("(")) {
      var grouped = this.parseAssignment();
      var close = this.expect(")");
      grouped.end = close.end;
      return grouped;
    }

    if (this.match("[")) {
      var arrayStart = this.previous().start;
      var elements = [];
      if (!this.match("]")) {
        do {
          if (this.is("]")) {
            throw error("KIT_PARSE_TRAILING_COMMA", "Array trailing comma is not supported", {
              source: this.source,
              position: this.current().start
            });
          }
          elements.push(this.parseAssignment());
        } while (this.match(","));
        var arrayClose = this.expect("]");
        return node("ArrayExpression", arrayStart, arrayClose.end, { elements: elements });
      }
      return node("ArrayExpression", arrayStart, this.previous().end, { elements: elements });
    }

    if (this.match("{")) {
      var objectStart = this.previous().start;
      var properties = [];
      var seenKeys = Object.create(null);
      if (!this.match("}")) {
        do {
          if (this.is("}")) {
            throw error("KIT_PARSE_TRAILING_COMMA", "Object trailing comma is not supported", {
              source: this.source,
              position: this.current().start
            });
          }

          var keyToken = this.current();
          if (keyToken.type !== "identifier" && keyToken.type !== "literal") {
            throw error("KIT_PARSE_OBJECT_KEY", "Object key must be an identifier or literal", {
              source: this.source,
              position: keyToken.start
            });
          }
          this.next();
          var key = String(keyToken.value);
          if (isBlockedMember(key)) {
            throw error("KIT_BLOCKED_MEMBER", "Object key '" + key + "' is blocked", {
              source: this.source,
              position: keyToken.start
            });
          }
          if (seenKeys[key]) {
            throw error("KIT_PARSE_OBJECT_DUPLICATE_KEY", "Duplicate object key '" + key + "'", {
              source: this.source,
              position: keyToken.start
            });
          }
          seenKeys[key] = true;
          this.expect(":");
          properties.push({ key: key, value: this.parseAssignment() });
        } while (this.match(","));
        var objectClose = this.expect("}");
        return node("ObjectExpression", objectStart, objectClose.end, { properties: properties });
      }
      return node("ObjectExpression", objectStart, this.previous().end, { properties: properties });
    }

    throw error(
      "KIT_PARSE_UNEXPECTED_TOKEN",
      "Unexpected token near '" + (token ? token.value : "<end>") + "'",
      { source: this.source, position: token ? token.start : this.source.length }
    );
  };

  function node(type, start, end, properties) {
    var output = { type: type, start: start, end: end };
    if (properties) {
      Object.keys(properties).forEach(function (key) {
        output[key] = properties[key];
      });
    }
    return output;
  }

  function isWritableAst(ast) {
    if (!ast) return false;
    if (ast.type === "Identifier") return true;
    return ast.type === "MemberExpression" && !ast.optional && isWritableAst(ast.object);
  }

  function lex(source) {
    return new Lexer(source).tokenize();
  }

  function parseBinding(source) {
    source = String(source == null ? "" : source);
    return new Parser(lex(source), source, { allowAssignment: false }).parseSingleExpression();
  }

  function parseAction(source) {
    source = String(source == null ? "" : source);
    return new Parser(lex(source), source, { allowAssignment: true }).parseProgram();
  }

  // -------------------------------------------------------------------------
  // Specialized parser modes
  // -------------------------------------------------------------------------

  function decodeQuotedKey(raw) {
    var tokens = lex(raw);
    if (tokens.length !== 2 || tokens[0].type !== "literal" ||
        typeof tokens[0].value !== "string") {
      throw error("KIT_MAP_KEY", "Invalid quoted map key '" + raw + "'", { source: raw });
    }
    return tokens[0].value;
  }

  function validateMapKey(rawKey, key, options) {
    options = options || {};
    if (isBlockedMember(key)) {
      throw error("KIT_BLOCKED_MEMBER", "Map key '" + key + "' is blocked", { key: key });
    }

    if (rawKey[0] === "'" || rawKey[0] === '"') return;

    if (options.classMap) {
      if (!/^-?[A-Za-z0-9_./-]+$/.test(key)) {
        throw error(
          "KIT_CLASS_KEY_QUOTE_REQUIRED",
          "Class key '" + key + "' must be quoted because it contains reserved characters",
          { key: key }
        );
      }
      return;
    }

    if (!/^-{0,2}[A-Za-z_][A-Za-z0-9_.-]*$/.test(key)) {
      throw error(
        "KIT_MAP_KEY_QUOTE_REQUIRED",
        "Map key '" + key + "' must be a static bare key or quoted string",
        { key: key }
      );
    }
  }

  function parseNamedMap(source, options) {
    source = String(source == null ? "" : source);
    options = options || {};

    var entries = [];
    var segmentStart = 0;
    var colon = -1;
    var seen = Object.create(null);

    function pushEntry(end) {
      var segment = source.slice(segmentStart, end).trim();
      if (!segment) {
        segmentStart = end + 1;
        colon = -1;
        return;
      }

      if (colon < segmentStart) {
        throw error("KIT_MAP_MISSING_COLON", "Map entry is missing ':'", {
          source: source,
          segment: segment,
          position: segmentStart
        });
      }

      var rawKey = source.slice(segmentStart, colon).trim();
      var expressionSource = source.slice(colon + 1, end).trim();
      if (!rawKey || !expressionSource) {
        throw error("KIT_MAP_ENTRY", "Map key and expression are required", {
          source: source,
          segment: segment,
          position: segmentStart
        });
      }

      var key = (rawKey[0] === "'" || rawKey[0] === '"')
        ? decodeQuotedKey(rawKey)
        : rawKey;

      validateMapKey(rawKey, key, options);

      if (seen[key]) {
        throw error("KIT_MAP_DUPLICATE_KEY", "Duplicate map key '" + key + "'", {
          source: source,
          key: key,
          position: segmentStart
        });
      }
      seen[key] = true;

      entries.push({
        key: key,
        source: expressionSource,
        ast: parseBinding(expressionSource)
      });

      segmentStart = end + 1;
      colon = -1;
    }

    scanTopLevel(source, function (character, position) {
      if (character === ":" && colon < segmentStart) colon = position;
      else if (character === ";") pushEntry(position);
    });

    if (source.slice(segmentStart).trim()) pushEntry(source.length);
    return node("NamedMap", 0, source.length, { entries: entries });
  }

  function looksLikeClassMap(source) {
    source = String(source == null ? "" : source).trim();
    if (!source) return false;
    if (source[0] === "{" || source[0] === "[" || source[0] === "`") return false;

    var firstColon = -1;
    var firstQuestion = -1;
    scanTopLevel(source, function (character, position) {
      if (character === ":" && firstColon < 0) firstColon = position;
      else if (character === "?" && source[position + 1] !== "." &&
               source[position + 1] !== "?" && firstQuestion < 0) {
        firstQuestion = position;
      }
    });

    if (firstColon < 0 || (firstQuestion >= 0 && firstQuestion < firstColon)) return false;

    var rawKey = source.slice(0, firstColon).trim();
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
    var ast = parseBinding(source);
    if (!isWritableAst(ast)) {
      throw error("KIT_MODEL_PATH", "Writable path must be an identifier/member path", {
        source: source
      });
    }
    return node("WritablePath", 0, source.length, { ast: ast });
  }

  function parseIdentity(source) {
    source = String(source == null ? "" : source).trim();
    if (!source) {
      throw error("KIT_IDENTITY_EMPTY", "Identity literal cannot be empty", { source: source });
    }
    return node("IdentityLiteral", 0, source.length, { value: source });
  }

  function parseIterator(source) {
    source = String(source == null ? "" : source).trim();
    var match = /^(\$[A-Za-z_][A-Za-z0-9_]*)\s*(?:,\s*(\$[A-Za-z_][A-Za-z0-9_]*)\s*)?\s+of\s+([\s\S]+)$/.exec(source);
    if (!match) {
      throw error(
        "KIT_ITERATOR_PARSE",
        "Iterator must use '$item, $index of collection' syntax",
        { source: source }
      );
    }

    return node("IteratorExpression", 0, source.length, {
      itemName: match[1],
      indexName: match[2] || "",
      collectionSource: match[3].trim(),
      collectionAst: parseBinding(match[3].trim())
    });
  }

  // -------------------------------------------------------------------------
  // Environment adapter and evaluator
  // -------------------------------------------------------------------------

  function normalizeResolution(value, name) {
    if (value && typeof value === "object" && hasOwn(value, "value") &&
        (hasOwn(value, "found") || hasOwn(value, "owner") || hasOwn(value, "readonly"))) {
      if (!hasOwn(value, "found")) value.found = true;
      return value;
    }
    return {
      found: value !== undefined,
      value: value,
      owner: null,
      readonly: false,
      name: name
    };
  }

  function createObjectEnvironment(stateObject, options) {
    options = options || {};
    var state = stateObject || Object.create(null);
    var contexts = options.contexts || Object.create(null);
    var aliases = options.aliases || Object.create(null);
    var services = options.services || Object.create(null);
    var readonlyRoots = options.readonlyRoots || RESERVED_ASSIGNMENT_ROOTS;

    return {
      resolve: function (name) {
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

      assign: function (name, value) {
        if (!name || readonlyRoots[name] || name[0] === "$" || name === "kit") {
          throw error("KIT_READONLY_CONTEXT", "Cannot assign to runtime context '" + name + "'", {
            name: name
          });
        }
        state[name] = value;
        if (typeof options.onMutation === "function") {
          options.onMutation({ type: "identifier", name: name, owner: state, value: value });
        }
        return value;
      },

      canWriteMember: function (reference) {
        if (!reference || !reference.owner || reference.key == null) return false;
        if (isBlockedMember(reference.key)) return false;
        if (readonlyRoots[reference.root]) return false;
        if (reference.owner === services || reference.owner === contexts) return false;
        if (typeof globalThis !== "undefined" && reference.owner === globalThis) return false;
        if (reference.owner && reference.owner.nodeType) return false;
        if (typeof Map !== "undefined" && reference.owner instanceof Map) return false;
        if (typeof Set !== "undefined" && reference.owner instanceof Set) return false;

        var descriptor = Object.getOwnPropertyDescriptor(reference.owner, reference.key);
        if (descriptor && (descriptor.writable === false || typeof descriptor.value === "function" ||
            descriptor.get || descriptor.set)) return false;

        return true;
      },

      onMutation: options.onMutation || null,
      onEffect: options.onEffect || null,
      defaultThis: options.defaultThis || state,
      state: state
    };
  }

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

  function resolveIdentifier(name, context) {
    var environment = context.environment;
    var resolution;

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
    var key = ast.computed
      ? evaluateAst(ast.property, context)
      : ast.property.value;

    key = typeof key === "symbol" ? key : String(key);
    if (typeof key === "string" && isBlockedMember(key)) {
      throw error("KIT_BLOCKED_MEMBER", "Access to member '" + key + "' is blocked", {
        key: key
      });
    }
    return key;
  }

  function evaluateReference(ast, context) {
    if (ast.type === "Identifier") {
      var resolution = resolveIdentifier(ast.name, context);
      return {
        value: resolution.value,
        owner: resolution.owner,
        key: ast.name,
        readonly: !!resolution.readonly,
        kind: resolution.kind || "identifier",
        root: ast.name,
        found: !!resolution.found
      };
    }

    if (ast.type === "MemberExpression") {
      var object = evaluateAst(ast.object, context);
      if (object == null) {
        if (ast.optional) {
          return {
            value: undefined,
            owner: null,
            key: null,
            readonly: true,
            kind: "optional",
            root: rootIdentifier(ast),
            found: false
          };
        }
        throw error("KIT_NULL_MEMBER_ACCESS", "Cannot read a member from null or undefined", {
          root: rootIdentifier(ast),
          position: ast.start
        });
      }

      var key = evaluatePropertyKey(ast, context);
      return {
        value: object[key],
        owner: object,
        key: key,
        readonly: false,
        kind: "member",
        root: rootIdentifier(ast),
        found: key in Object(object)
      };
    }

    return {
      value: evaluateAst(ast, context),
      owner: null,
      key: null,
      readonly: true,
      kind: "value",
      root: rootIdentifier(ast),
      found: true
    };
  }

  function writeReference(ast, value, context) {
    var environment = context.environment;

    if (ast.type === "Identifier") {
      if (!environment || typeof environment.assign !== "function") {
        throw error("KIT_ENVIRONMENT_ASSIGN", "Environment does not implement assign(name, value)", {
          name: ast.name
        });
      }
      var assigned = environment.assign(ast.name, value);
      context.mutations.push({ type: "identifier", name: ast.name, value: value });
      return assigned;
    }

    if (ast.type !== "MemberExpression" || ast.optional) {
      throw error("KIT_INVALID_ASSIGNMENT_TARGET", "Invalid assignment target", {
        position: ast.start
      });
    }

    var reference = evaluateReference(ast, context);
    if (!reference.owner || reference.key == null) {
      throw error("KIT_READONLY_PATH", "Cannot assign to unresolved path", {
        root: reference.root
      });
    }

    if (RESERVED_ASSIGNMENT_ROOTS[reference.root]) {
      throw error("KIT_READONLY_PATH", "Cannot assign through read-only root '" + reference.root + "'", {
        root: reference.root
      });
    }

    if (environment && typeof environment.canWriteMember === "function" &&
        environment.canWriteMember(reference) !== true) {
      throw error("KIT_READONLY_PATH", "Cannot assign to path rooted at '" + reference.root + "'", {
        root: reference.root,
        key: reference.key
      });
    }

    reference.owner[reference.key] = value;
    var mutation = {
      type: "member",
      root: reference.root,
      owner: reference.owner,
      key: reference.key,
      value: value
    };
    context.mutations.push(mutation);
    if (environment && typeof environment.onMutation === "function") {
      environment.onMutation(mutation);
    }
    return value;
  }

  function registerEffect(result, context) {
    if (!isThenable(result)) return;
    if (context.effects.indexOf(result) < 0) context.effects.push(result);
    if (context.environment && typeof context.environment.onEffect === "function") {
      context.environment.onEffect(result);
    }
  }

  function evaluateAst(ast, context) {
    context.evaluationCount++;
    if (context.evaluationCount > context.evaluationBudget) {
      throw error("KIT_EVALUATION_BUDGET", "Expression evaluation budget exceeded", {
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
        var text = ast.quasis[0] || "";
        for (var ti = 0; ti < ast.expressions.length; ti++) {
          var interpolation = evaluateAst(ast.expressions[ti], context);
          if (isThenable(interpolation)) {
            throw error("KIT_ASYNC_BINDING", "Template interpolation cannot resolve a Promise");
          }
          text += interpolation == null ? "" : String(interpolation);
          text += ast.quasis[ti + 1] || "";
        }
        return text;
      }

      case "ArrayExpression": {
        var array = [];
        for (var ai = 0; ai < ast.elements.length; ai++) {
          array.push(evaluateAst(ast.elements[ai], context));
        }
        return array;
      }

      case "ObjectExpression": {
        var object = Object.create(null);
        for (var oi = 0; oi < ast.properties.length; oi++) {
          var property = ast.properties[oi];
          if (isBlockedMember(property.key)) {
            throw error("KIT_BLOCKED_MEMBER", "Object key '" + property.key + "' is blocked");
          }
          object[property.key] = evaluateAst(property.value, context);
        }
        return object;
      }

      case "UnaryExpression": {
        var unaryValue = evaluateAst(ast.argument, context);
        if (ast.operator === "!") return !unaryValue;
        if (ast.operator === "-") return -unaryValue;
        if (ast.operator === "+") return +unaryValue;
        throw error("KIT_UNKNOWN_OPERATOR", "Unknown unary operator '" + ast.operator + "'");
      }

      case "LogicalExpression": {
        var logicalLeft = evaluateAst(ast.left, context);
        if (ast.operator === "&&") {
          return logicalLeft ? evaluateAst(ast.right, context) : logicalLeft;
        }
        if (ast.operator === "||") {
          return logicalLeft ? logicalLeft : evaluateAst(ast.right, context);
        }
        if (ast.operator === "??") {
          return logicalLeft == null ? evaluateAst(ast.right, context) : logicalLeft;
        }
        throw error("KIT_UNKNOWN_OPERATOR", "Unknown logical operator '" + ast.operator + "'");
      }

      case "BinaryExpression": {
        var left = evaluateAst(ast.left, context);
        var right = evaluateAst(ast.right, context);
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
        throw error("KIT_UNKNOWN_OPERATOR", "Unknown binary operator '" + ast.operator + "'");
      }

      case "ConditionalExpression":
        return evaluateAst(ast.test, context)
          ? evaluateAst(ast.consequent, context)
          : evaluateAst(ast.alternate, context);

      case "MemberExpression":
        return evaluateReference(ast, context).value;

      case "AssignmentExpression": {
        var assignedValue = evaluateAst(ast.right, context);
        return writeReference(ast.left, assignedValue, context);
      }

      case "CallExpression": {
        var reference = evaluateReference(ast.callee, context);
        if (reference.value == null && ast.optional) return undefined;
        if (typeof reference.value !== "function") {
          throw error("KIT_NOT_CALLABLE", "Expression target is not callable", {
            root: reference.root,
            key: reference.key
          });
        }

        if (context.callDepth >= context.callDepthLimit) {
          throw error("KIT_CALL_DEPTH", "Expression call-depth limit exceeded", {
            limit: context.callDepthLimit
          });
        }

        var args = [];
        for (var ci = 0; ci < ast.arguments.length; ci++) {
          args.push(evaluateAst(ast.arguments[ci], context));
        }

        context.callDepth++;
        try {
          var thisArg = reference.owner ||
            (context.environment && context.environment.defaultThis) ||
            undefined;
          var result = reference.value.apply(thisArg, args);
          if (context.mode === MODES.ACTION) registerEffect(result, context);
          return result;
        } finally {
          context.callDepth--;
        }
      }

      case "Program": {
        var programValue;
        for (var pi = 0; pi < ast.body.length; pi++) {
          programValue = evaluateAst(ast.body[pi], context);
          if (context.mode === MODES.ACTION) registerEffect(programValue, context);
        }
        return programValue;
      }

      default:
        throw error("KIT_UNKNOWN_AST", "Unknown AST node '" + ast.type + "'");
    }
  }

  function normalizeMode(mode) {
    mode = mode || MODES.BINDING;
    return MODE_ALIASES[mode] || mode;
  }

  function createExpressionEngine(options) {
    options = options || {};
    var cache = new Map();
    var maxCacheEntries = options.maxCacheEntries || DEFAULT_CACHE_ENTRIES;

    function cacheSet(key, value) {
      if (cache.size >= maxCacheEntries) {
        var first = cache.keys().next();
        if (!first.done) cache.delete(first.value);
      }
      cache.set(key, value);
      return value;
    }

    function compile(mode, source) {
      mode = normalizeMode(mode);
      source = String(source == null ? "" : source).trim();
      var key = mode + "\u0000" + source;
      if (cache.has(key)) return cache.get(key);

      if (!source && mode !== MODES.NAMED_MAP) {
        throw error("KIT_EMPTY_EXPRESSION", "Directive expression cannot be empty", {
          mode: mode,
          source: source
        });
      }

      var compiled;
      if (mode === MODES.BINDING) {
        compiled = { mode: mode, source: source, ast: parseBinding(source) };
      } else if (mode === MODES.ACTION) {
        compiled = { mode: mode, source: source, ast: parseAction(source) };
      } else if (mode === MODES.NAMED_MAP) {
        compiled = { mode: mode, source: source, ast: parseNamedMap(source) };
      } else if (mode === MODES.CLASS_VALUE) {
        compiled = { mode: mode, source: source, ast: parseClassValue(source) };
      } else if (mode === MODES.WRITABLE_PATH) {
        compiled = { mode: mode, source: source, ast: parseWritablePath(source) };
      } else if (mode === MODES.IDENTITY) {
        compiled = { mode: mode, source: source, ast: parseIdentity(source) };
      } else if (mode === MODES.ITERATOR) {
        compiled = { mode: mode, source: source, ast: parseIterator(source) };
      } else {
        throw error("KIT_PARSE_MODE", "Unknown parser mode '" + mode + "'", {
          mode: mode
        });
      }

      return cacheSet(key, compiled);
    }

    function execute(compiled, environment, executeOptions) {
      if (!compiled || !compiled.mode || !compiled.ast) {
        throw error("KIT_COMPILED_EXPRESSION", "Invalid compiled expression");
      }

      executeOptions = executeOptions || {};
      var context = new EvaluationContext(environment, {
        mode: compiled.mode,
        evaluationBudget: executeOptions.evaluationBudget || options.evaluationBudget,
        callDepthLimit: executeOptions.callDepthLimit || options.callDepthLimit
      });

      var value;
      if (compiled.mode === MODES.BINDING) {
        value = evaluateAst(compiled.ast, context);
        if (isThenable(value)) {
          throw error("KIT_ASYNC_BINDING", "Binding expression cannot resolve a Promise", {
            source: compiled.source
          });
        }
      } else if (compiled.mode === MODES.ACTION) {
        value = evaluateAst(compiled.ast, context);
      } else if (compiled.mode === MODES.NAMED_MAP) {
        value = compiled.ast.entries.map(function (entry) {
          var entryValue = evaluateAst(entry.ast, context);
          if (isThenable(entryValue)) {
            throw error("KIT_ASYNC_BINDING", "Named map value cannot resolve a Promise", {
              source: entry.source,
              key: entry.key
            });
          }
          return { key: entry.key, value: entryValue };
        });
      } else if (compiled.mode === MODES.CLASS_VALUE) {
        if (compiled.ast.type === "ClassMap") {
          value = compiled.ast.map.entries.map(function (entry) {
            var entryValue = evaluateAst(entry.ast, context);
            if (isThenable(entryValue)) {
              throw error("KIT_ASYNC_BINDING", "Class map value cannot resolve a Promise", {
                source: entry.source,
                key: entry.key
              });
            }
            return { key: entry.key, value: entryValue };
          });
        } else {
          value = evaluateAst(compiled.ast.ast, context);
          if (isThenable(value)) {
            throw error("KIT_ASYNC_BINDING", "Class expression cannot resolve a Promise", {
              source: compiled.source
            });
          }
        }
      } else if (compiled.mode === MODES.WRITABLE_PATH) {
        value = evaluateAst(compiled.ast.ast, context);
      } else if (compiled.mode === MODES.IDENTITY) {
        value = compiled.ast.value;
      } else if (compiled.mode === MODES.ITERATOR) {
        value = {
          itemName: compiled.ast.itemName,
          indexName: compiled.ast.indexName,
          collection: evaluateAst(compiled.ast.collectionAst, context)
        };
        if (isThenable(value.collection)) {
          throw error("KIT_ASYNC_BINDING", "Iterator collection cannot resolve a Promise", {
            source: compiled.ast.collectionSource
          });
        }
      }

      return {
        value: value,
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
        throw error("KIT_MODEL_PATH", "assign() requires a writable-path compiled expression");
      }
      executeOptions = executeOptions || {};
      var context = new EvaluationContext(environment, {
        mode: MODES.ACTION,
        evaluationBudget: executeOptions.evaluationBudget || options.evaluationBudget,
        callDepthLimit: executeOptions.callDepthLimit || options.callDepthLimit
      });
      var assigned = writeReference(compiledWritablePath.ast.ast, value, context);
      return {
        value: assigned,
        effects: context.effects.slice(),
        mutations: context.mutations.slice(),
        evaluationCount: context.evaluationCount
      };
    }

    return Object.freeze({
      modes: MODES,
      compile: compile,
      execute: execute,
      evaluate: evaluate,
      assign: assign,
      clearCache: function () { cache.clear(); },
      cacheSize: function () { return cache.size; },
      createObjectEnvironment: createObjectEnvironment
    });
  }

  return Object.freeze({
    MODES: MODES,
    KitworkExpressionError: KitworkExpressionError,
    createExpressionEngine: createExpressionEngine,
    createObjectEnvironment: createObjectEnvironment,

    // Exposed only for unit/conformance tests in this milestone package.
    // Production runtime composition must keep AST shapes private.
    testing: Object.freeze({
      lex: lex,
      parseBinding: parseBinding,
      parseAction: parseAction,
      parseNamedMap: parseNamedMap,
      parseClassValue: parseClassValue,
      parseWritablePath: parseWritablePath,
      parseIterator: parseIterator,
      isBlockedMember: isBlockedMember
    })
  });
});
