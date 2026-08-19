; (function (document) {
  "use strict";

  var core = document[Symbol.for("kitjs:assembly")];
  if (!core || core.phase !== "lexer") throw new Error("KitJS: parser loaded out of order");
  if (core.reuse) { core.phase = "parser"; return; }

  var NODE_LIMIT = 10000;
  var NESTING_LIMIT = 64;

  function parse(tokens, source, mode) {
    var position = 0;
    var nodes = 0;
    var nesting = 0;
    var action = mode === "action";

    function current() { return tokens[position]; }
    function look(offset) { return tokens[position + (offset || 0)]; }
    function is(value) { return current().type === "operator" && current().value === value; }
    function take(value) { if (!is(value)) return false; position++; return true; }
    function expect(value) {
      if (!take(value)) core.syntax("expected \"" + value + "\"", source, current().position);
    }
    function make(type, fields) {
      if (++nodes > NODE_LIMIT) core.syntax("expression is too large", source, current().position);
      fields = fields || {};
      fields.type = type;
      return fields;
    }
    function nested(read) {
      if (++nesting > NESTING_LIMIT) core.syntax("expression nesting is too deep", source, current().position);
      try { return read(); } finally { nesting--; }
    }
    function safeName(name, at) {
      if (core.blocked(name)) core.syntax("blocked name \"" + name + "\"", source, at);
      return name;
    }

    function assignment() {
      var left = conditional();
      if (!take("=")) return left;
      if (!action) core.syntax("assignment is only allowed in actions", source, current().position);
      if (left.type !== "identifier") core.syntax("assignment target must be a component field", source, current().position);
      if (left.name.charAt(0) === "$") core.syntax("the $ namespace is read-only", source, current().position);
      return make("assign", { name: left.name, value: nested(assignment) });
    }
    function conditional() {
      var condition = coalesce();
      if (!take("?")) return condition;
      var yes = nested(assignment);
      expect(":");
      return make("conditional", { condition: condition, yes: yes, no: nested(assignment) });
    }
    function coalesce() {
      var left = logicalOr();
      while (take("??")) {
        if (left.type === "logical") core.syntax("parentheses are required when mixing ?? with && or ||", source, current().position);
        var right = logicalOr();
        if (right.type === "logical") core.syntax("parentheses are required when mixing ?? with && or ||", source, current().position);
        left = make("coalesce", { left: left, right: right });
      }
      return left;
    }
    function logicalOr() {
      var left = logicalAnd();
      while (take("||")) left = make("logical", { operator: "||", left: left, right: logicalAnd() });
      return left;
    }
    function logicalAnd() {
      var left = equality();
      while (take("&&")) left = make("logical", { operator: "&&", left: left, right: equality() });
      return left;
    }
    function equality() {
      var left = relation();
      while (["==", "!=", "===", "!=="].indexOf(current().value) >= 0) {
        var operator = current().value;
        position++;
        left = make("binary", { operator: operator, left: left, right: relation() });
      }
      return left;
    }
    function relation() {
      var left = addition();
      while (["<", "<=", ">", ">="].indexOf(current().value) >= 0) {
        var operator = current().value;
        position++;
        left = make("binary", { operator: operator, left: left, right: addition() });
      }
      return left;
    }
    function addition() {
      var left = multiplication();
      while (is("+") || is("-")) {
        var operator = current().value;
        position++;
        left = make("binary", { operator: operator, left: left, right: multiplication() });
      }
      return left;
    }
    function multiplication() {
      var left = unary();
      while (is("*") || is("/") || is("%")) {
        var operator = current().value;
        position++;
        left = make("binary", { operator: operator, left: left, right: unary() });
      }
      return left;
    }
    function updateTarget(value, at) {
      if (!action) core.syntax("update is only allowed in actions", source, at);
      if (value.type !== "identifier") {
        core.syntax("update target must be a direct writable identifier", source, at);
      }
      if (value.name.charAt(0) === "$") core.syntax("the $ namespace is read-only", source, at);
      return value.name;
    }
    function unary() {
      if (is("++") || is("--")) {
        var update = current();
        position++;
        var target = nested(unary);
        return make("update", {
          name: updateTarget(target, update.position),
          operator: update.value,
          prefix: true
        });
      }
      if (is("!") || is("-") || is("+")) {
        var operator = current().value;
        position++;
        return make("unary", { operator: operator, value: nested(unary) });
      }
      return postfix();
    }
    function argumentsList() {
      var args = [];
      if (!is(")")) {
        do {
          if (is(")")) core.syntax("calls reject a trailing comma", source, current().position);
          args.push(assignment());
        } while (take(","));
      }
      expect(")");
      return args;
    }
    function postfix() {
      var value = primary();
      var chain = false;
      while (true) {
        if (take(".")) {
          if (current().type !== "identifier") core.syntax("expected a member name", source, current().position);
          value = make("member", {
            object: value,
            property: safeName(current().value, current().position),
            computed: false,
            optional: false
          });
          position++;
        } else if (take("[")) {
          var key = nested(assignment);
          expect("]");
          if (key.type === "literal" && core.blocked(String(key.value))) {
            core.syntax("blocked member \"" + key.value + "\"", source, current().position);
          }
          value = make("member", {
            object: value,
            property: key,
            computed: true,
            optional: false
          });
        } else if (take("?.")) {
          chain = true;
          if (current().type === "identifier") {
            value = make("member", {
              object: value,
              property: safeName(current().value, current().position),
              computed: false,
              optional: true
            });
            position++;
          } else if (take("[")) {
            var optionalKey = nested(assignment);
            expect("]");
            if (optionalKey.type === "literal" && core.blocked(String(optionalKey.value))) {
              core.syntax("blocked member \"" + optionalKey.value + "\"", source, current().position);
            }
            value = make("member", {
              object: value,
              property: optionalKey,
              computed: true,
              optional: true
            });
          } else if (take("(")) {
            value = make("call", {
              callee: value,
              args: nested(argumentsList),
              optional: true
            });
          } else {
            core.syntax("expected a member name, computed member, or call after optional chain", source, current().position);
          }
        } else if (take("(")) {
          value = make("call", {
            callee: value,
            args: nested(argumentsList),
            optional: false
          });
        } else if (is("++") || is("--")) {
          var postfixUpdate = current();
          position++;
          value = make("update", {
            name: updateTarget(value, postfixUpdate.position),
            operator: postfixUpdate.value,
            prefix: false
          });
          break;
        } else break;
      }
      return chain ? make("chain", { value: value }) : value;
    }
    function arrowParameters() {
      var saved = position;
      if (!take("(")) return null;
      var params = [];
      var seen = Object.create(null);
      if (!take(")")) {
        while (true) {
          if (current().type !== "identifier") { position = saved; return null; }
          var name = safeName(current().value, current().position);
          if (name.charAt(0) === "$") core.syntax("lambda parameters cannot use the $ namespace", source, current().position);
          if (seen[name]) core.syntax("duplicate lambda parameter \"" + name + "\"", source, current().position);
          seen[name] = true;
          params.push(name);
          position++;
          if (!take(",")) break;
          if (is(")")) core.syntax("lambdas reject a trailing comma", source, current().position);
        }
        if (!take(")")) { position = saved; return null; }
      }
      if (!take("=>")) { position = saved; return null; }
      return params;
    }
    function primary() {
      var token = current();
      if (token.type === "literal") {
        position++;
        return make("literal", { value: token.value });
      }
      if (token.type === "identifier") {
        position++;
        var name = safeName(token.value, token.position);
        if (!action && name.charAt(0) === "$" && name !== "$app") {
          core.syntax("the $ namespace is action-only", source, token.position);
        }
        return make("identifier", { name: name });
      }
      if (is("(")) {
        var params = arrowParameters();
        if (params) return make("lambda", { params: params, body: nested(assignment) });
        expect("(");
        var grouped = nested(assignment);
        expect(")");
        return make("group", { value: grouped });
      }
      if (take("[")) {
        var items = [];
        if (!is("]")) {
          while (true) {
            items.push(nested(assignment));
            if (!take(",")) break;
            if (is("]")) core.syntax("arrays reject a trailing comma", source, current().position);
          }
        }
        expect("]");
        return make("array", { items: items });
      }
      if (take("{")) {
        var entries = [];
        while (!is("}")) {
          var key = current();
          if (key.type !== "identifier" &&
            !(key.type === "literal" && typeof key.value === "string")) {
            core.syntax("expected an object key", source, key.position);
          }
          position++;
          var keyName = safeName(String(key.value), key.position);
          expect(":");
          entries.push({ key: keyName, value: nested(assignment) });
          if (!take(",")) break;
          if (is("}")) break;
        }
        expect("}");
        return make("object", { entries: entries });
      }
      core.syntax("expected an expression", source, token.position);
    }

    var output = assignment();
    if (take(";")) {
      if (!action) core.syntax("sequences are only allowed in actions", source, current().position);
      var expressions = [output];
      while (current().type !== "end") {
        expressions.push(assignment());
        if (!take(";")) break;
      }
      output = make("sequence", { expressions: expressions });
    }
    if (current().type !== "end") core.syntax("unexpected token \"" + current().value + "\"", source, current().position);
    return output;
  }

  core.parse = parse;
  core.phase = "parser";
})(document);
