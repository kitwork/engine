"use strict";

const { createError } = require("./errors.js");
const { isBlockedMember } = require("./constants.js");
const { lex } = require("./lexer.js");

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
