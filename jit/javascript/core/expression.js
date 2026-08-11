// KitJS closed expression language: source -> private AST -> bounded walker.
(function (window) {
  "use strict";

  var kit = window.kit;
  var core = kit && kit.__kitwork_core__;
  if (!core) throw new Error("KitJS core/global.js must be loaded before core/expression.js");
  if (core.reuse) return;
  if (core.phase !== "global") throw new Error("KitJS core fragment order error before core/expression.js");

  var OWN = core.OWN;
  var FORBIDDEN = core.FORBIDDEN;
  var INVALID_MEMBER = core.INVALID_MEMBER;
  var blocked = core.blocked;
  var memberKey = core.memberKey;
  var warn = core.warn;
  var astCache = Object.create(null);
  var astOrder = [];
  var AST_CACHE_LIMIT = 2048;

  function ParseError(message) {
    this.name = "KitParseError";
    this.message = message;
  }

  function lex(source) {
    var tokens = [];
    var index = 0;

    function identifierStart(character) {
      return (character >= "A" && character <= "Z") ||
        (character >= "a" && character <= "z") || character === "_" || character === "$";
    }
    function identifierPart(character) {
      return identifierStart(character) || (character >= "0" && character <= "9");
    }
    function digit(character) { return character >= "0" && character <= "9"; }

    while (index < source.length) {
      var character = source.charAt(index);
      if (/\s/.test(character)) { index++; continue; }

      if (digit(character) || (character === "." && digit(source.charAt(index + 1)))) {
        var numberStart = index;
        if (character === ".") index++;
        while (index < source.length && digit(source.charAt(index))) index++;
        if (source.charAt(index) === ".") {
          index++;
          while (index < source.length && digit(source.charAt(index))) index++;
        }
        if (source.charAt(index) === "e" || source.charAt(index) === "E") {
          var exponent = index++;
          if (source.charAt(index) === "+" || source.charAt(index) === "-") index++;
          var exponentDigits = index;
          while (index < source.length && digit(source.charAt(index))) index++;
          if (exponentDigits === index) throw new ParseError("Invalid number exponent at " + exponent);
        }
        var numeric = Number(source.substring(numberStart, index));
        if (!Number.isFinite(numeric)) throw new ParseError("Number is outside the supported range");
        tokens.push({ type: "literal", value: numeric });
        continue;
      }

      if (character === "'" || character === "\"") {
        var quote = character;
        var text = "";
        index++;
        while (index < source.length && source.charAt(index) !== quote) {
          if (source.charAt(index) === "\\") {
            index++;
            if (index >= source.length) break;
            var escaped = source.charAt(index++);
            text += escaped === "n" ? "\n" : escaped === "r" ? "\r" : escaped === "t" ? "\t" : escaped;
          } else text += source.charAt(index++);
        }
        if (source.charAt(index) !== quote) throw new ParseError("Unterminated string");
        index++;
        tokens.push({ type: "literal", value: text });
        continue;
      }

      if (identifierStart(character)) {
        var identifier = "";
        while (index < source.length && identifierPart(source.charAt(index))) identifier += source.charAt(index++);
        if (FORBIDDEN[identifier]) throw new ParseError("Forbidden keyword: " + identifier);
        if (identifier === "true") tokens.push({ type: "literal", value: true });
        else if (identifier === "false") tokens.push({ type: "literal", value: false });
        else if (identifier === "null") tokens.push({ type: "literal", value: null });
        else if (identifier === "undefined") tokens.push({ type: "literal", value: undefined });
        else tokens.push({ type: "identifier", value: identifier });
        continue;
      }

      var three = source.substr(index, 3);
      var two = source.substr(index, 2);
      if (three === "===" || three === "!==") {
        tokens.push({ type: "operator", value: three });
        index += 3;
        continue;
      }
      if (two === "?." || two === "??" || two === "&&" || two === "||" || two === "<=" || two === ">=") {
        tokens.push({ type: "operator", value: two });
        index += 2;
        continue;
      }
      if (two === "=>" || two === "++" || two === "--" || two === "==" || two === "!=" ||
          two === "+=" || two === "-=" || two === "*=" || two === "/=") {
        throw new ParseError("Unsupported operator: " + two);
      }
      if ("+-*/%<>!?:.,()[]{}=".indexOf(character) !== -1) {
        tokens.push({ type: "operator", value: character });
        index++;
        continue;
      }
      throw new ParseError("Unexpected character: " + character);
    }
    tokens.push({ type: "eof", value: "" });
    return tokens;
  }

  function parse(tokens, allowAssignment) {
    var position = 0;
    function current() { return tokens[position]; }
    function is(value) { return current().type === "operator" && current().value === value; }
    function take(value) { if (is(value)) { position++; return true; } return false; }
    function expect(value) { if (!take(value)) throw new ParseError("Expected '" + value + "'"); }

    function expression() { return assignment(); }
    function assignment() {
      var left = coalesce();
      if (take("=")) {
        if (!allowAssignment) throw new ParseError("Assignment is not allowed here");
        if (left.type !== "identifier" && left.type !== "member") throw new ParseError("Invalid assignment target");
        return { type: "assign", target: left, value: assignment() };
      }
      return left;
    }
    function coalesce() {
      var node = conditional();
      while (take("??")) node = { type: "coalesce", left: node, right: conditional() };
      return node;
    }
    function conditional() {
      var node = logicalOr();
      if (take("?")) {
        var yes = assignment();
        expect(":");
        node = { type: "conditional", condition: node, yes: yes, no: assignment() };
      }
      return node;
    }
    function logicalOr() {
      var node = logicalAnd();
      while (take("||")) node = { type: "logical", operator: "||", left: node, right: logicalAnd() };
      return node;
    }
    function logicalAnd() {
      var node = equality();
      while (take("&&")) node = { type: "logical", operator: "&&", left: node, right: equality() };
      return node;
    }
    function equality() {
      var node = relation();
      while (is("===") || is("!==")) {
        var operator = current().value;
        position++;
        node = { type: "binary", operator: operator, left: node, right: relation() };
      }
      return node;
    }
    function relation() {
      var node = addition();
      while (is("<") || is(">") || is("<=") || is(">=")) {
        var operator = current().value;
        position++;
        node = { type: "binary", operator: operator, left: node, right: addition() };
      }
      return node;
    }
    function addition() {
      var node = multiplication();
      while (is("+") || is("-")) {
        var operator = current().value;
        position++;
        node = { type: "binary", operator: operator, left: node, right: multiplication() };
      }
      return node;
    }
    function multiplication() {
      var node = unary();
      while (is("*") || is("/") || is("%")) {
        var operator = current().value;
        position++;
        node = { type: "binary", operator: operator, left: node, right: unary() };
      }
      return node;
    }
    function unary() {
      if (is("!") || is("-") || is("+")) {
        var operator = current().value;
        position++;
        return { type: "unary", operator: operator, value: unary() };
      }
      return postfix();
    }
    function postfix() {
      var node = primary();
      while (true) {
        if (take(".") || take("?.")) {
          var property = current();
          if (property.type !== "identifier") throw new ParseError("Expected a member name");
          position++;
          node = { type: "member", object: node, property: property.value, computed: false };
        } else if (take("[")) {
          var key = expression();
          expect("]");
          node = { type: "member", object: node, property: key, computed: true };
        } else if (take("(")) {
          var args = [];
          if (!is(")")) {
            args.push(assignment());
            while (take(",")) args.push(assignment());
          }
          expect(")");
          node = { type: "call", callee: node, args: args };
        } else break;
      }
      return node;
    }
    function primary() {
      var token = current();
      if (token.type === "literal") { position++; return { type: "literal", value: token.value }; }
      if (token.type === "identifier") { position++; return { type: "identifier", name: token.value }; }
      if (take("(")) { var grouped = expression(); expect(")"); return grouped; }
      if (take("[")) {
        var items = [];
        if (!is("]")) {
          items.push(assignment());
          while (take(",")) items.push(assignment());
        }
        expect("]");
        return { type: "array", items: items };
      }
      if (take("{")) {
        var entries = [];
        if (!is("}")) {
          do {
            var key = current();
            if (key.type !== "identifier" && key.type !== "literal") throw new ParseError("Expected an object key");
            position++;
            expect(":");
            if (blocked(String(key.value))) throw new ParseError("Blocked object key: " + key.value);
            entries.push({ key: String(key.value), value: assignment() });
          } while (take(","));
        }
        expect("}");
        return { type: "object", entries: entries };
      }
      throw new ParseError("Unexpected token: " + token.value);
    }

    var output = expression();
    if (current().type !== "eof") throw new ParseError("Unexpected trailing input");
    return output;
  }

  function compile(source, mode) {
    source = typeof source === "string" ? source.trim() : "";
    var cacheKey = (mode || "binding") + "\u0000" + source;
    if (OWN.call(astCache, cacheKey)) return astCache[cacheKey];
    var compiled;
    try {
      compiled = parse(lex(source), mode === "action");
    } catch (error) {
      warn("KIT_PARSE_INVALID", error.message + " in: " + source);
      compiled = { type: "error" };
    }
    astCache[cacheKey] = compiled;
    astOrder.push(cacheKey);
    if (astOrder.length > AST_CACHE_LIMIT) delete astCache[astOrder.shift()];
    return compiled;
  }

  function evaluate(ast, resolver, budget) {
    if (!ast || ast.type === "error") return undefined;
    budget = budget || { nodes: 0, calls: 0 };
    if (++budget.nodes > 20000) throw new Error("Expression budget exceeded");

    if (ast.type === "literal") return ast.value;
    if (ast.type === "identifier") return resolver.get(ast.name);
    if (ast.type === "array") return ast.items.map(function (item) { return evaluate(item, resolver, budget); });
    if (ast.type === "object") {
      var object = Object.create(null);
      ast.entries.forEach(function (entry) { object[entry.key] = evaluate(entry.value, resolver, budget); });
      return object;
    }
    if (ast.type === "unary") {
      var unary = evaluate(ast.value, resolver, budget);
      return ast.operator === "!" ? !unary : ast.operator === "-" ? -unary : +unary;
    }
    if (ast.type === "logical") {
      var logical = evaluate(ast.left, resolver, budget);
      return ast.operator === "&&" ? (logical ? evaluate(ast.right, resolver, budget) : logical) :
        (logical ? logical : evaluate(ast.right, resolver, budget));
    }
    if (ast.type === "coalesce") {
      var nullable = evaluate(ast.left, resolver, budget);
      return nullable === null || nullable === undefined ? evaluate(ast.right, resolver, budget) : nullable;
    }
    if (ast.type === "conditional") {
      return evaluate(ast.condition, resolver, budget) ? evaluate(ast.yes, resolver, budget) : evaluate(ast.no, resolver, budget);
    }
    if (ast.type === "binary") {
      var left = evaluate(ast.left, resolver, budget);
      var right = evaluate(ast.right, resolver, budget);
      if (ast.operator === "+") return left + right;
      if (ast.operator === "-") return left - right;
      if (ast.operator === "*") return left * right;
      if (ast.operator === "/") return left / right;
      if (ast.operator === "%") return left % right;
      if (ast.operator === "<") return left < right;
      if (ast.operator === ">") return left > right;
      if (ast.operator === "<=") return left <= right;
      if (ast.operator === ">=") return left >= right;
      if (ast.operator === "===") return left === right;
      if (ast.operator === "!==") return left !== right;
      return undefined;
    }
    if (ast.type === "member") {
      var owner = evaluate(ast.object, resolver, budget);
      var member = memberKey(ast.computed ? evaluate(ast.property, resolver, budget) : ast.property);
      if (owner === null || owner === undefined || member === INVALID_MEMBER) return undefined;
      return owner[member];
    }
    if (ast.type === "call") {
      if (++budget.calls > 64) throw new Error("Expression call depth exceeded");
      var result;
      var args = ast.args.map(function (arg) { return evaluate(arg, resolver, budget); });
      if (ast.callee.type === "member") {
        var receiver = evaluate(ast.callee.object, resolver, budget);
        var method = memberKey(ast.callee.computed ? evaluate(ast.callee.property, resolver, budget) : ast.callee.property);
        if (receiver !== null && receiver !== undefined && method !== INVALID_MEMBER && typeof receiver[method] === "function") {
          result = receiver[method].apply(receiver, args);
        }
      } else {
        var callable = evaluate(ast.callee, resolver, budget);
        if (typeof callable === "function") result = callable.apply(undefined, args);
      }
      budget.calls--;
      return result;
    }
    if (ast.type === "assign") {
      var value = evaluate(ast.value, resolver, budget);
      if (ast.target.type === "identifier") resolver.set(ast.target.name, value);
      else {
        var target = evaluate(ast.target.object, resolver, budget);
        var targetKey = memberKey(ast.target.computed ? evaluate(ast.target.property, resolver, budget) : ast.target.property);
        if (target !== null && target !== undefined && targetKey !== INVALID_MEMBER) target[targetKey] = value;
      }
      return value;
    }
    return undefined;
  }

  function splitTop(source, separator) {
    var output = [];
    var current = "";
    var depth = 0;
    var quote = "";
    for (var index = 0; index < source.length; index++) {
      var character = source.charAt(index);
      if (quote) {
        current += character;
        if (character === quote && source.charAt(index - 1) !== "\\") quote = "";
      } else if (character === "'" || character === "\"") {
        quote = character;
        current += character;
      } else if (character === "(" || character === "[" || character === "{") {
        depth++;
        current += character;
      } else if (character === ")" || character === "]" || character === "}") {
        depth--;
        current += character;
      } else if (character === separator && depth === 0) {
        output.push(current);
        current = "";
      } else current += character;
    }
    if (current.trim()) output.push(current);
    return output;
  }

  function parseMap(source) {
    var output = [];
    splitTop(source, ";").forEach(function (statement) {
      var parts = splitTop(statement, ":");
      if (parts.length < 2) { warn("KIT_PARSE_INVALID_MAP", statement); return; }
      var key = parts.shift().trim();
      if ((key.charAt(0) === "'" && key.charAt(key.length - 1) === "'") ||
          (key.charAt(0) === "\"" && key.charAt(key.length - 1) === "\"")) {
        key = key.substring(1, key.length - 1);
      }
      output.push({ key: key, expression: parts.join(":").trim() });
    });
    return output;
  }

  function classMap(source) {
    var depth = 0;
    var quote = "";
    for (var index = 0; index < source.length; index++) {
      var character = source.charAt(index);
      if (quote) {
        if (character === quote && source.charAt(index - 1) !== "\\") quote = "";
      } else if (character === "'" || character === "\"") quote = character;
      else if (character === "(" || character === "[" || character === "{") depth++;
      else if (character === ")" || character === "]" || character === "}") depth--;
      else if (depth === 0 && character === "?") return false;
      else if (depth === 0 && character === ":") return true;
    }
    return false;
  }

  core.ParseError = ParseError;
  core.lex = lex;
  core.parse = parse;
  core.compile = compile;
  core.evaluate = evaluate;
  core.splitTop = splitTop;
  core.parseMap = parseMap;
  core.classMap = classMap;
  core.phase = "expression";

})(window);
