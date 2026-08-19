; (function (global, document) {
  "use strict";

  var VERSION = "0.9.0-next.10";
  var ASSEMBLY = Symbol.for("kitjs:assembly");
  var INSTALL = Symbol.for("kitjs:runtime");
  var ownKit = Object.prototype.hasOwnProperty.call(global, "kit");
  var currentKit = global.kit;
  var nextObservation = 0;

  if (Object.prototype.hasOwnProperty.call(document, ASSEMBLY)) {
    throw new Error("KitJS: another assembly is already in progress");
  }
  if (currentKit && currentKit[INSTALL] === VERSION &&
    currentKit.version === VERSION && typeof currentKit.component === "function") {
    Object.defineProperty(document, ASSEMBLY, {
      value: { phase: "core", reuse: true },
      configurable: true
    });
    return;
  }
  if (ownKit || currentKit !== undefined) {
    throw new Error("KitJS: globalThis.kit is already owned by another script");
  }

  var OWN = Object.prototype.hasOwnProperty;
  function words(source) {
    var output = Object.create(null);
    source.split(" ").forEach(function (word) { if (word) output[word] = true; });
    return output;
  }
  var BLOCKED = words(
    "constructor prototype __proto__ __defineGetter__ __defineSetter__ " +
    "__lookupGetter__ __lookupSetter__ ownerDocument defaultView contentWindow " +
    "window globalThis top parent self caller callee arguments"
  );
  var FORBIDDEN = words(
    "var let const function class return if else for while do switch case new " +
    "delete void typeof instanceof in await yield throw try catch finally import export " +
    "this super with debugger of async document location navigator Function eval " +
    "undefined NaN Infinity"
  );
  var INVALID_MEMBER = {};

  function syntax(message, source, position) {
    throw new SyntaxError("KitJS: " + message + " in \"" + source + "\" at " + position);
  }
  function report(error) {
    if (global.console && typeof global.console.error === "function") global.console.error(error);
  }
  function equal(left, right) {
    return left === right || left !== left && right !== right;
  }
  function blocked(value) {
    return typeof value === "string" && BLOCKED[value] === true;
  }
  function ignoredForRuntime(element) {
    while (element && element !== document) {
      if (element.nodeType === 1 && element.hasAttribute && element.hasAttribute("data-kit-ignore")) {
        return true;
      }
      element = element.parentElement;
    }
    return false;
  }
  function memberKey(value) {
    if (typeof value === "number") {
      if (!Number.isFinite(value)) return INVALID_MEMBER;
      value = String(value);
    } else if (typeof value !== "string") return INVALID_MEMBER;
    return blocked(value) ? INVALID_MEMBER : value;
  }

  var core = {
    phase: "core",
    reuse: false,
    version: VERSION,
    install: INSTALL,
    assembly: ASSEMBLY,
    OWN: OWN,
    FORBIDDEN: FORBIDDEN,
    INVALID_MEMBER: INVALID_MEMBER,
    syntax: syntax,
    report: report,
    equal: equal,
    blocked: blocked,
    ignoredForRuntime: ignoredForRuntime,
    memberKey: memberKey,
    registry: new Map(),
    compiled: new Map(),
    scopes: new WeakMap(),
    scopeRecords: new WeakMap(),
    records: new WeakMap(),
    cacheLimit: 256,
    booted: false,
    dirtyAll: false,
    dirtyRecords: new Set(),
    queued: false,
    render: null,
    renderPending: null,
    startHooks: []
  };

  core.invalidate = function (record) {
    if (record && typeof record === "object") {
      if (record.disposed || core.renderPending && core.renderPending.has(record)) return;
      core.dirtyRecords.add(record);
    }
    else core.dirtyAll = true;
    if (core.queued) return;
    core.queued = true;
    queueMicrotask(function () {
      core.queued = false;
      if (!core.render || !core.dirtyAll && !core.dirtyRecords.size) return;
      var all = core.dirtyAll;
      var records = all ? null : Array.from(core.dirtyRecords);
      core.dirtyAll = false;
      core.dirtyRecords.clear();
      core.render(records);
    });
  };
  core.resetDirty = function () {
    core.dirtyAll = false;
    core.dirtyRecords.clear();
  };
  function attachObservation(value, then, tickets) {
    var settled = false;
    function settle(error, rejected) {
      if (settled) return;
      settled = true;
      if (rejected) report(error);
      var waiting = new Set(tickets);
      document.querySelectorAll("[data-kit-component],[data-kit-scope]").forEach(function (element) {
        if (ignoredForRuntime(element)) return;
        var record = core.scopes.get(element);
        if (!record || record.disposed || !record.observations) return;
        tickets.forEach(function (ticket) {
          if (!waiting.has(ticket) || !record.observations.delete(ticket)) return;
          waiting.delete(ticket);
          core.invalidate(record);
        });
      });
      tickets.length = 0;
    }
    try {
      then.call(value, function () { settle(null, false); }, function (error) { settle(error, true); });
    } catch (error) {
      settle(error, true);
    }
  }
  core.observe = function (value, owners) {
    if (!value) return;
    var then;
    try { then = value.then; }
    catch (error) { report(error); return; }
    if (typeof then !== "function") return;
    if (!Array.isArray(owners)) owners = owners ? [owners] : [];
    var seen = new Set();
    var tickets = [];
    owners.forEach(function (record) {
      if (!record || record.disposed || seen.has(record)) return;
      seen.add(record);
      var ticket = ++nextObservation;
      if (!record.observations) record.observations = new Set();
      record.observations.add(ticket);
      tickets.push(ticket);
    });
    attachObservation(value, then, tickets);
  };

  Object.defineProperty(document, ASSEMBLY, {
    value: core,
    configurable: true
  });
})(globalThis, document);
; (function (document) {
  "use strict";

  var core = document[Symbol.for("kitjs:assembly")];
  if (!core || core.phase !== "core") throw new Error("KitJS: lexer loaded out of order");
  if (core.reuse) { core.phase = "lexer"; return; }

  function space(character) {
    return character === " " || character === "\t" || character === "\n" ||
      character === "\r" || character === "\f";
  }
  function digit(character) { return character >= "0" && character <= "9"; }
  function identifierStart(character) {
    return character === "$" || character === "_" ||
      character >= "a" && character <= "z" || character >= "A" && character <= "Z";
  }
  function identifierPart(character) { return identifierStart(character) || digit(character); }

  function lex(source) {
    var tokens = [];
    var index = 0;
    function token(type, value, position) {
      tokens.push({ type: type, value: value, position: position });
    }

    while (index < source.length) {
      var character = source.charAt(index);
      if (space(character)) { index++; continue; }
      var start = index;

      if (digit(character) || character === "." && digit(source.charAt(index + 1))) {
        if (character === ".") index++;
        while (digit(source.charAt(index))) index++;
        if (source.charAt(index) === ".") {
          index++;
          while (digit(source.charAt(index))) index++;
        }
        if (source.charAt(index) === "e" || source.charAt(index) === "E") {
          var exponent = index++;
          if (source.charAt(index) === "+" || source.charAt(index) === "-") index++;
          var digits = index;
          while (digit(source.charAt(index))) index++;
          if (digits === index) core.syntax("invalid number exponent", source, exponent);
        }
        var number = Number(source.slice(start, index));
        if (!Number.isFinite(number)) core.syntax("number is outside the supported range", source, start);
        token("literal", number, start);
        continue;
      }

      if (character === "'" || character === '"') {
        var quote = character;
        var value = "";
        index++;
        while (index < source.length) {
          character = source.charAt(index++);
          if (character === quote) break;
          if (character !== "\\") { value += character; continue; }
          if (index >= source.length) core.syntax("unfinished string", source, start);
          character = source.charAt(index++);
          if ("nrtbf\\'\"".indexOf(character) < 0) {
            core.syntax("unsupported string escape \\" + character, source, index - 2);
          }
          value += character === "n" ? "\n" : character === "r" ? "\r" :
            character === "t" ? "\t" : character === "b" ? "\b" :
              character === "f" ? "\f" : character;
        }
        if (character !== quote) core.syntax("unfinished string", source, start);
        token("literal", value, start);
        continue;
      }

      if (identifierStart(character)) {
        index++;
        while (identifierPart(source.charAt(index))) index++;
        var identifier = source.slice(start, index);
        if (identifier === "true") token("literal", true, start);
        else if (identifier === "false") token("literal", false, start);
        else if (identifier === "null") token("literal", null, start);
        else {
          if (core.FORBIDDEN[identifier]) core.syntax("forbidden keyword \"" + identifier + "\"", source, start);
          token("identifier", identifier, start);
        }
        continue;
      }

      var operator = source.slice(index, index + 3);
      if (operator === "===" || operator === "!==") {
        token("operator", operator, start);
        index += 3;
        continue;
      }
      operator = source.slice(index, index + 2);
      if (["??", "&&", "||", "<=", ">=", "=>", "==", "!=", "?.", "++", "--"].indexOf(operator) >= 0) {
        token("operator", operator, start);
        index += 2;
        continue;
      }
      if (["+=", "-=", "*=", "/=", "%=", "**", "<<", ">>"].indexOf(operator) >= 0) {
        core.syntax("unsupported operator \"" + operator + "\"", source, start);
      }
      if ("+-*/%!?:.,()[]{}=;<>".indexOf(character) >= 0) {
        token("operator", character, start);
        index++;
        continue;
      }
      core.syntax("unexpected character \"" + character + "\"", source, start);
    }
    token("end", "", source.length);
    return tokens;
  }

  core.lex = lex;
  core.phase = "lexer";
})(document);
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
; (function (document) {
  "use strict";

  var core = document[Symbol.for("kitjs:assembly")];
  if (!core || core.phase !== "parser") throw new Error("KitJS: evaluator loaded out of order");
  if (core.reuse) { core.phase = "evaluator"; return; }

  var NODE_LIMIT = 10000;
  var CALL_LIMIT = 64;
  var OWN = core.OWN;
  var lambdas = new WeakMap();
  var aliasRefs = new WeakMap();
  var CHAIN_SKIP = Object.freeze(Object.create(null));
  var activeOwnership = null;
  var activeInvocation = null;

  function serviceCapability(value) {
    var services = activeOwnership && activeOwnership.services;
    return services && value && (typeof value === "object" || typeof value === "function") ?
      services.get(value) || null : null;
  }
  function markServiceCapability(value, name, owner) {
    if (!activeOwnership || !activeOwnership.services || !value) {
      throw new TypeError("KitJS: app services are action-only");
    }
    activeOwnership.services.set(value, { name: name, owner: owner });
    return value;
  }
  function serviceName(value) {
    return typeof core.serviceName === "function" ? core.serviceName(value) : null;
  }
  function rejectServiceValue(value) {
    if (serviceCapability(value) || serviceName(value)) {
      throw new TypeError("KitJS: app service namespaces cannot be used as expression values");
    }
    return value;
  }

  function aliasReference(name) {
    var reference = Object.freeze(Object.create(null));
    aliasRefs.set(reference, name);
    return reference;
  }
  function markResultOwner(value, owner, replace) {
    var owners = activeOwnership && activeOwnership.owners;
    if (owners && owner && value && (typeof value === "object" || typeof value === "function") &&
      (replace || !owners.has(value))) {
      owners.set(value, { owner: owner, stamp: ++activeOwnership.stamp });
    }
    return value;
  }
  function resultOwner(value) {
    var owners = activeOwnership && activeOwnership.owners;
    return owners && value && (typeof value === "object" || typeof value === "function") ?
      (owners.get(value) || {}).owner || null : null;
  }
  function markCallResult(value, owner, startedAt) {
    var owners = activeOwnership && activeOwnership.owners;
    var current = owners && value && (typeof value === "object" || typeof value === "function") ?
      owners.get(value) : null;
    return current && current.stamp > startedAt ? value : markResultOwner(value, owner, true);
  }
  function takeResultOwner(value, fallback) {
    var owners = activeOwnership && activeOwnership.owners;
    if (!value || typeof value !== "object" && typeof value !== "function" ||
      !owners || !owners.has(value)) return fallback || null;
    var owner = owners.get(value).owner;
    owners.delete(value);
    return owner && !owner.disposed ? owner : null;
  }

  function requireField(scope, name) {
    if (core.blocked(name) || !OWN.call(scope, name)) {
      throw new ReferenceError("KitJS: unknown component field \"" + name + "\"");
    }
  }
  function requireWritableData(scope, name) {
    requireField(scope, name);
    var descriptor = Object.getOwnPropertyDescriptor(scope, name);
    if (!descriptor || !OWN.call(descriptor, "value") || !descriptor.writable) {
      throw new TypeError("KitJS: component field \"" + name + "\" is read-only");
    }
  }
  function directResolver(scope) {
    return {
      parent: null,
      scope: scope,
      owner: core.scopeRecords.get(scope) || null,
      mode: "binding",
      thisValue: scope,
      get: function (name) {
        requireField(scope, name);
        return Reflect.get(scope, name, scope);
      },
      set: function (name, value) {
        requireWritableData(scope, name);
        if (!Reflect.set(scope, name, value, scope)) {
          throw new TypeError("KitJS: component field \"" + name + "\" is read-only");
        }
        return value;
      }
    };
  }
  function actionResolver(scope) {
    var writes = Object.create(null);
    var order = [];
    var aliases = new Map();
    var status = "active";
    var proxy;

    function read(name) {
      if (!OWN.call(scope, name)) {
        if (name.charAt(0) === "$" && core.validAlias && core.validAlias(name)) {
          return aliasReference(name);
        }
        requireField(scope, name);
      }
      if (status === "active" && OWN.call(writes, name)) return writes[name];
      return Reflect.get(scope, name, proxy);
    }
    function write(name, value) {
      rejectServiceValue(value);
      requireWritableData(scope, name);
      if (status === "aborted") throw new Error("KitJS: action transaction is no longer active");
      if (status !== "active") {
        if (!Reflect.set(scope, name, value, scope)) throw new TypeError("KitJS: component field is read-only");
        return value;
      }
      if (!OWN.call(writes, name)) order.push(name);
      writes[name] = value;
      return value;
    }
    function writeFromMethod(name, value) {
      rejectServiceValue(value);
      requireField(scope, name);
      var descriptor = Object.getOwnPropertyDescriptor(scope, name);
      if (descriptor && !OWN.call(descriptor, "value") && descriptor.set) {
        if (!Reflect.set(scope, name, value, scope)) {
          throw new TypeError("KitJS: component field \"" + name + "\" is read-only");
        }
        return value;
      }
      return write(name, value);
    }
    proxy = new Proxy(scope, {
      get: function (_, name) {
        if (typeof name === "symbol") return Reflect.get(scope, name, scope);
        return read(String(name));
      },
      set: function (_, name, value) {
        if (typeof name === "symbol") return false;
        writeFromMethod(String(name), value);
        return true;
      },
      deleteProperty: function () { return false; }
    });
    return {
      parent: null,
      scope: scope,
      owner: core.scopeRecords.get(scope) || null,
      mode: "action",
      thisValue: proxy,
      get: read,
      set: write,
      resolveAliasRecord: function (name) {
        if (aliases.has(name)) return aliases.get(name);
        var current = core.resolveAlias(name);
        aliases.set(name, current);
        return current;
      },
      resolveAlias: function (name) {
        return this.resolveAliasRecord(name).scope;
      },
      resolveAppService: function (alias, service) {
        var current = this.resolveAliasRecord(alias);
        var namespace = core.resolveAppService && core.resolveAppService(alias, current, service);
        return namespace === undefined ? undefined : markServiceCapability(namespace, service, current);
      },
      commit: function () {
        if (status !== "active") return;
        status = "committed";
        try {
          order.forEach(function (name) {
            if (!Reflect.set(scope, name, writes[name], scope)) {
              throw new TypeError("KitJS: component field \"" + name + "\" is read-only");
            }
          });
        } finally {
          writes = Object.create(null);
          order.length = 0;
          aliases.clear();
        }
      },
      abort: function () {
        if (status !== "active") return;
        status = "aborted";
        writes = Object.create(null);
        order.length = 0;
        aliases.clear();
      }
    };
  }

  function localResolver(parent, locals) {
    return {
      kind: "local",
      parent: parent,
      locals: locals,
      thisValue: parent.thisValue,
      get: function (name) {
        return OWN.call(locals, name) ? locals[name] : parent.get(name);
      },
      set: function (name, value) {
        if (OWN.call(locals, name)) { locals[name] = value; return value; }
        return parent.set(name, value);
      }
    };
  }
  function contextResolver(parent, locals) {
    return {
      kind: "context",
      parent: parent,
      locals: locals,
      thisValue: parent.thisValue,
      get: function (name) {
        return OWN.call(locals, name) ? locals[name] : parent.get(name);
      },
      set: function (name, value) {
        if (OWN.call(locals, name)) {
          throw new TypeError("KitJS: event context \"" + name + "\" is read-only");
        }
        return parent.set(name, value);
      }
    };
  }
  function childResolver(parent, params, args) {
    var locals = Object.create(null);
    params.forEach(function (name, index) { locals[name] = args[index]; });
    return localResolver(parent, locals);
  }
  function rootOf(resolver) {
    while (resolver.parent) resolver = resolver.parent;
    return resolver;
  }
  function rebaseResolver(captured, root) {
    if (!captured.parent) return root;
    var parent = rebaseResolver(captured.parent, root);
    return captured.kind === "context" ? contextResolver(parent, captured.locals) :
      localResolver(parent, captured.locals);
  }
  function captureLayers(resolver, owner) {
    if (!owner) return null;
    var layers = [];
    while (resolver.parent) {
      var token = {};
      owner.captures.set(token, resolver.locals);
      layers.push({ kind: resolver.kind, token: token });
      resolver = resolver.parent;
    }
    return layers;
  }
  function restoreLayers(record, root) {
    var resolver = root;
    for (var index = record.layers.length - 1; index >= 0; index--) {
      var layer = record.layers[index];
      var locals = record.owner.captures && record.owner.captures.get(layer.token);
      if (!locals) return null;
      resolver = layer.kind === "context" ? contextResolver(resolver, locals) :
        localResolver(resolver, locals);
    }
    return resolver;
  }

  function resolveAliasValue(value, resolver) {
    var name = value && aliasRefs.get(value);
    if (!name) return value;
    var root = rootOf(resolver);
    if (!root.resolveAlias) throw new ReferenceError("KitJS: aliases are action-only");
    return root.resolveAlias(name);
  }

  function member(owner, key, resolver, computed, optional, allowAppProjection) {
    var inheritedOwner = resultOwner(owner);
    var alias = owner && aliasRefs.get(owner);
    key = core.memberKey(key);
    if (key === core.INVALID_MEMBER) throw new TypeError("KitJS: invalid or blocked member key");
    var root = rootOf(resolver);
    if (alias && allowAppProjection && !computed && !optional && root.resolveAppService) {
      var projected = root.resolveAppService(alias, key);
      if (projected !== undefined) return projected;
      var appGrants = core.graph && core.graph.grants && core.graph.grants.app;
      if (alias === "$app" && appGrants && OWN.call(appGrants, key)) {
        throw new TypeError("KitJS: app service namespace is unavailable for this component");
      }
    }
    owner = resolveAliasValue(owner, resolver);
    var resolvedOwner = alias ? core.scopeRecords.get(owner) : inheritedOwner;
    if (root.mode !== "action") resolvedOwner = null;
    if (owner === null || owner === undefined) {
      throw new TypeError("KitJS: cannot read a member of a nullish value");
    }
    if (serviceCapability(owner) || serviceName(owner)) {
      throw new TypeError("KitJS: app service methods must be called directly");
    }
    if (typeof owner === "string") {
      if (key === "length") return owner.length;
      if (/^(?:0|[1-9][0-9]*)$/.test(key)) return owner[Number(key)];
      return undefined;
    }
    if (Array.isArray(owner)) {
      if (key === "length") return owner.length;
      return OWN.call(owner, key) ? owner[key] : undefined;
    }
    if ((typeof owner === "object" || typeof owner === "function") && OWN.call(owner, key)) {
      return markResultOwner(owner[key], resolvedOwner, true);
    }
    return undefined;
  }

  function hasMethod(receiver, name) {
    if (typeof receiver === "string") {
      return ["includes", "startsWith", "endsWith", "trim", "toLowerCase", "toUpperCase"].indexOf(name) >= 0;
    }
    if (typeof receiver === "number") return name === "toFixed";
    if (Array.isArray(receiver)) {
      return ["join", "includes", "indexOf", "slice", "map", "filter", "find", "some", "every"].indexOf(name) >= 0;
    }
    return (typeof receiver === "object" || typeof receiver === "function") &&
      OWN.call(receiver, name) && typeof receiver[name] === "function";
  }
  function method(receiver, name, args) {
    if (typeof receiver === "string") {
      if (name === "includes" && args.length === 1) return receiver.includes(String(args[0]));
      if (name === "startsWith" && args.length === 1) return receiver.startsWith(String(args[0]));
      if (name === "endsWith" && args.length === 1) return receiver.endsWith(String(args[0]));
      if (name === "trim" && args.length === 0) return receiver.trim();
      if (name === "toLowerCase" && args.length === 0) return receiver.toLowerCase();
      if (name === "toUpperCase" && args.length === 0) return receiver.toUpperCase();
      return undefined;
    }
    if (typeof receiver === "number") {
      return name === "toFixed" && args.length <= 1 ?
        receiver.toFixed(args.length ? Number(args[0]) : 0) : undefined;
    }
    if (Array.isArray(receiver)) {
      if (name === "join" && args.length <= 1) return receiver.join(args[0]);
      if (name === "includes" && args.length === 1) return receiver.includes(args[0]);
      if (name === "indexOf" && args.length === 1) return receiver.indexOf(args[0]);
      if (name === "slice" && args.length <= 2) return receiver.slice.apply(receiver, args);
      if (["map", "filter", "find", "some", "every"].indexOf(name) >= 0 &&
        args.length === 1 && typeof args[0] === "function") {
        return Array.prototype[name].call(receiver, args[0]);
      }
      return undefined;
    }
    return receiver[name].apply(receiver, args);
  }

  function makeLambda(record) {
    return function () {
      if (record.owner && (record.owner.disposed || !record.owner.host || !record.owner.host.isConnected)) {
        return undefined;
      }
      var context = activeInvocation;
      var startedAt = activeOwnership ? activeOwnership.stamp : 0;
      var activeBudget = context ? context.budget : { nodes: 0, depth: 0 };
      var activeRoot = context && rootOf(context.resolver);
      var sameOwner = activeRoot && record.owner && activeRoot.owner === record.owner;
      var scope = record.owner ? record.owner.scope : record.scope;
      if (!scope) return undefined;
      var root = sameOwner ? activeRoot : record.mode === "action" ?
        actionResolver(scope) : directResolver(scope);
      var parent = record.owner ? restoreLayers(record, root) : rebaseResolver(record.resolver, root);
      if (!parent) return undefined;
      try {
        var result = evaluate(record.ast.body,
          childResolver(parent, record.ast.params, arguments), activeBudget);
        if (!sameOwner && root.commit) root.commit();
        return record.mode === "action" ?
          markCallResult(result, record.owner || root.owner, startedAt) : result;
      } catch (error) {
        if (!sameOwner && root.abort) root.abort();
        throw error;
      }
    };
  }

  function callTarget(ast) {
    var grouped = false;
    while (ast.type === "group") { grouped = true; ast = ast.value; }
    if (grouped && ast.type === "chain" && ast.value.type === "member") {
      return { value: ast.value, closedChain: true };
    }
    return { value: ast, closedChain: false };
  }

  function appLoaderLeaf(ast) {
    if (!ast || ast.type !== "member" || ast.computed || ast.optional ||
      ast.property !== "visible" && ast.property !== "value") return null;
    var loader = ast.object;
    if (!loader || loader.type !== "member" || loader.computed || loader.optional ||
      loader.property !== "loader" || !loader.object || loader.object.type !== "identifier" ||
      loader.object.name !== "$app") return null;
    return ast.property;
  }

  function evaluate(ast, resolver, budget) {
    if (++budget.nodes > NODE_LIMIT) throw new Error("KitJS: expression budget exceeded");
    var type = ast.type;
    if (type === "literal") return ast.value;
    if (type === "identifier") return resolver.get(ast.name);
    if (type === "group") return evaluate(ast.value, resolver, budget);
    if (type === "array") {
      return ast.items.map(function (item) {
        return rejectServiceValue(evaluate(item, resolver, budget));
      });
    }
    if (type === "object") {
      var object = Object.create(null);
      ast.entries.forEach(function (entry) {
        if (core.blocked(entry.key)) throw new TypeError("KitJS: blocked object key");
        object[entry.key] = rejectServiceValue(evaluate(entry.value, resolver, budget));
      });
      return object;
    }
    if (type === "lambda") {
      var capturedRoot = rootOf(resolver);
      var record = {
        ast: ast,
        owner: capturedRoot.owner,
        mode: capturedRoot.mode,
        layers: captureLayers(resolver, capturedRoot.owner),
        resolver: capturedRoot.owner ? null : resolver,
        scope: capturedRoot.owner ? null : capturedRoot.scope
      };
      var lambda = makeLambda(record);
      lambdas.set(lambda, record);
      return lambda;
    }
    if (type === "sequence") {
      var sequence;
      ast.expressions.forEach(function (expression) {
        sequence = evaluate(expression, resolver, budget);
      });
      return sequence;
    }
    if (type === "assign") {
      return resolver.set(ast.name, rejectServiceValue(evaluate(ast.value, resolver, budget)));
    }
    if (type === "update") {
      var previous = resolver.get(ast.name);
      var returned = ast.operator === "++" ? previous++ : previous--;
      resolver.set(ast.name, previous);
      return ast.prefix ? previous : returned;
    }
    if (type === "unary") {
      var unary = rejectServiceValue(evaluate(ast.value, resolver, budget));
      return ast.operator === "!" ? !unary : ast.operator === "-" ? -unary : +unary;
    }
    if (type === "logical") {
      var logical = rejectServiceValue(evaluate(ast.left, resolver, budget));
      return ast.operator === "&&" ?
        (logical ? evaluate(ast.right, resolver, budget) : logical) :
        (logical ? logical : evaluate(ast.right, resolver, budget));
    }
    if (type === "coalesce") {
      var nullable = rejectServiceValue(evaluate(ast.left, resolver, budget));
      return nullable === null || nullable === undefined ? evaluate(ast.right, resolver, budget) : nullable;
    }
    if (type === "conditional") {
      return rejectServiceValue(evaluate(ast.condition, resolver, budget)) ?
        evaluate(ast.yes, resolver, budget) : evaluate(ast.no, resolver, budget);
    }
    if (type === "binary") {
      var left = rejectServiceValue(evaluate(ast.left, resolver, budget));
      var right = rejectServiceValue(evaluate(ast.right, resolver, budget));
      if (ast.operator === "+") return left + right;
      if (ast.operator === "-") return left - right;
      if (ast.operator === "*") return left * right;
      if (ast.operator === "/") return left / right;
      if (ast.operator === "%") return left % right;
      if (ast.operator === "<") return left < right;
      if (ast.operator === "<=") return left <= right;
      if (ast.operator === ">") return left > right;
      if (ast.operator === ">=") return left >= right;
      if (ast.operator === "==") return left == right;
      if (ast.operator === "!=") return left != right;
      if (ast.operator === "===") return left === right;
      return left !== right;
    }
    if (type === "member") {
      var bindingRoot = rootOf(resolver);
      var loaderLeaf = bindingRoot.mode === "binding" ? appLoaderLeaf(ast) : null;
      if (loaderLeaf) {
        if (typeof core.resolveAppLoader !== "function") {
          throw new ReferenceError("KitJS: $app.loader is unavailable");
        }
        return core.resolveAppLoader(bindingRoot.owner, loaderLeaf);
      }
      var owner = evaluate(ast.object, resolver, budget);
      if (owner === CHAIN_SKIP) return CHAIN_SKIP;
      if (owner === null || owner === undefined) {
        if (ast.optional) return CHAIN_SKIP;
        throw new TypeError("KitJS: cannot read a member of a nullish value");
      }
      var key = ast.computed ? evaluate(ast.property, resolver, budget) : ast.property;
      return member(owner, key, resolver, ast.computed, ast.optional,
        ast.object.type === "identifier" && ast.object.name === "$app");
    }
    if (type === "chain") {
      var chain = evaluate(ast.value, resolver, budget);
      return chain === CHAIN_SKIP ? undefined : chain;
    }
    if (type === "call") {
      var receiver;
      var callable;
      var methodName;
      var callOwner;
      var callReference = callTarget(ast.callee);
      var target = callReference.value;
      var memberCall = target.type === "member";
      var startedAt = activeOwnership ? activeOwnership.stamp : 0;
      if (memberCall) {
        receiver = evaluate(target.object, resolver, budget);
        if (receiver === CHAIN_SKIP) {
          if (!callReference.closedChain || ast.optional) return CHAIN_SKIP;
          throw new TypeError("KitJS: value is not callable");
        }
        receiver = resolveAliasValue(receiver, resolver);
        if (receiver === null || receiver === undefined) {
          if (target.optional) {
            if (!callReference.closedChain || ast.optional) return CHAIN_SKIP;
            throw new TypeError("KitJS: value is not callable");
          }
          throw new TypeError("KitJS: cannot call a member of a nullish value");
        }
        methodName = target.computed ?
          evaluate(target.property, resolver, budget) : target.property;
        methodName = core.memberKey(methodName);
        if (methodName === core.INVALID_MEMBER) throw new TypeError("KitJS: invalid or blocked member key");
        var capability = serviceCapability(receiver);
        var registeredService = serviceName(receiver);
        if (registeredService && !capability) {
          throw new TypeError("KitJS: service namespace is unavailable to authored HTML");
        }
        if (capability) {
          if (target.computed || target.optional || callReference.closedChain || ast.optional ||
            !core.graph.actions[capability.name][methodName]) {
            throw new TypeError("KitJS: authored action \"" + capability.name + "." + methodName + "\" is not granted");
          }
        }
        if (!hasMethod(receiver, methodName)) {
          var methodValue = member(receiver, methodName, resolver);
          if (ast.optional && (methodValue === null || methodValue === undefined)) return CHAIN_SKIP;
          throw new TypeError("KitJS: member \"" + methodName + "\" is not callable");
        }
        callOwner = capability ? capability.owner :
          resultOwner(receiver) || core.scopeRecords.get(receiver) || rootOf(resolver).owner;
      } else {
        callable = evaluate(ast.callee, resolver, budget);
        if (callable === CHAIN_SKIP) return CHAIN_SKIP;
        if (callable === null || callable === undefined) {
          if (ast.optional) return CHAIN_SKIP;
          throw new TypeError("KitJS: value is not callable");
        }
        if (typeof callable !== "function") throw new TypeError("KitJS: value is not callable");
        callOwner = lambdas.has(callable) ? lambdas.get(callable).owner : rootOf(resolver).owner;
      }
      var args = ast.args.map(function (argument) {
        return rejectServiceValue(evaluate(argument, resolver, budget));
      });
      if (++budget.depth > CALL_LIMIT) {
        budget.depth--;
        throw new Error("KitJS: expression call depth exceeded");
      }
      var previousInvocation = activeInvocation;
      activeInvocation = { budget: budget, resolver: resolver };
      try {
        var result = memberCall ?
          method(receiver, methodName, args) : callable.apply(resolver.thisValue, args);
        return rootOf(resolver).mode === "action" ? markCallResult(result, callOwner, startedAt) : result;
      } finally {
        activeInvocation = previousInvocation;
        budget.depth--;
      }
    }
    throw new Error("KitJS: invalid private expression node");
  }

  function appServiceRoot(ast) {
    if (!ast || ast.type !== "member" || !ast.object || ast.object.type !== "identifier" ||
      ast.object.name !== "$app") return null;
    var grants = core.graph && core.graph.grants && core.graph.grants.app;
    if (!grants) return null;
    if (!ast.computed) {
      return OWN.call(grants, ast.property) ? { name: ast.property, direct: !ast.optional } : null;
    }
    if (!ast.property || ast.property.type !== "literal") return { name: "", direct: false };
    var name = typeof ast.property.value === "string" ? ast.property.value : "";
    return name && OWN.call(grants, name) ? { name: name, direct: false } : null;
  }

  function appServiceCommand(ast) {
    if (!ast || ast.type !== "call" || ast.optional || !ast.callee || ast.callee.type !== "member" ||
      ast.callee.computed || ast.callee.optional) return null;
    var root = appServiceRoot(ast.callee.object);
    if (!root || !root.direct) return null;
    return { service: root.name, method: ast.callee.property, args: ast.args };
  }

  function ordinaryDirectAppMember(ast) {
    if (!ast || ast.type !== "member" || ast.computed || ast.optional || !ast.object ||
      ast.object.type !== "identifier" || ast.object.name !== "$app") return false;
    var grants = core.graph && core.graph.grants && core.graph.grants.app;
    return !grants || !OWN.call(grants, ast.property);
  }

  function validateAppLoaderBinding(ast, source, mode) {
    function containsLoaderRoot(node) {
      if (!node) return false;
      if (node.type === "member") {
        if (node.object && node.object.type === "identifier" && node.object.name === "$app" &&
          (!node.computed && node.property === "loader" || node.computed && node.property &&
            node.property.type === "literal" && node.property.value === "loader")) return true;
        return containsLoaderRoot(node.object) || node.computed && containsLoaderRoot(node.property);
      }
      if (node.type === "group" || node.type === "unary" || node.type === "chain") {
        return containsLoaderRoot(node.value);
      }
      if (node.type === "logical" || node.type === "coalesce" || node.type === "binary") {
        return containsLoaderRoot(node.left) || containsLoaderRoot(node.right);
      }
      if (node.type === "conditional") {
        return containsLoaderRoot(node.condition) || containsLoaderRoot(node.yes) ||
          containsLoaderRoot(node.no);
      }
      if (node.type === "array") return node.items.some(containsLoaderRoot);
      if (node.type === "object") {
        return node.entries.some(function (entry) { return containsLoaderRoot(entry.value); });
      }
      if (node.type === "lambda") return containsLoaderRoot(node.body);
      if (node.type === "assign") return containsLoaderRoot(node.value);
      if (node.type === "sequence") return node.expressions.some(containsLoaderRoot);
      if (node.type === "call") {
        return containsLoaderRoot(node.callee) || node.args.some(containsLoaderRoot);
      }
      return false;
    }
    function invalid() {
      core.syntax("$app bindings may only read loader.visible or loader.value", source, 0);
    }
    var loaderExpression = containsLoaderRoot(ast);
    if (mode === "action") {
      if (loaderExpression) invalid();
      return;
    }
    function walk(node, allowLeaf) {
      if (!node) return;
      if (appLoaderLeaf(node)) {
        if (!allowLeaf) invalid();
        return;
      }
      if (node.type === "identifier") {
        if (node.name === "$app") invalid();
        return;
      }
      if (node.type === "literal") return;
      if (node.type === "group" || node.type === "chain" || node.type === "call" ||
        node.type === "array" || node.type === "object" || node.type === "lambda" ||
        node.type === "assign" || node.type === "sequence" || node.type === "update") {
        if (loaderExpression) invalid();
      }
      if (node.type === "unary") {
        walk(node.value, true);
        return;
      }
      if (node.type === "logical" || node.type === "coalesce" || node.type === "binary") {
        walk(node.left, true);
        walk(node.right, true);
        return;
      }
      if (node.type === "conditional") {
        walk(node.condition, true);
        walk(node.yes, true);
        walk(node.no, true);
        return;
      }
      if (node.type === "group" || node.type === "chain") {
        walk(node.value, false);
        return;
      }
      if (node.type === "array") {
        node.items.forEach(function (item) { walk(item, false); });
        return;
      }
      if (node.type === "object") {
        node.entries.forEach(function (entry) { walk(entry.value, false); });
        return;
      }
      if (node.type === "lambda") {
        walk(node.body, false);
        return;
      }
      if (node.type === "assign") {
        walk(node.value, false);
        return;
      }
      if (node.type === "sequence") {
        node.expressions.forEach(function (expression) { walk(expression, false); });
        return;
      }
      if (node.type === "member") {
        if (loaderExpression && node.computed) invalid();
        if (node.object && node.object.type === "identifier" && node.object.name === "$app" &&
          !node.computed && node.property === "loader") invalid();
        walk(node.object, false);
        if (node.computed) walk(node.property, false);
        return;
      }
      if (node.type === "call") {
        walk(node.callee, false);
        node.args.forEach(function (argument) { walk(argument, false); });
      }
    }
    walk(ast, true);
  }

  function validateAppServiceStructure(ast, source, mode) {
    var grants = core.graph && core.graph.grants && core.graph.grants.app;
    if (!grants || !Object.keys(grants).length) return;

    function invalid(message) { core.syntax(message, source, 0); }
    function walk(node, commandPosition) {
      if (!node) return;
      var command = appServiceCommand(node);
      if (command) {
        if (mode !== "action" || !commandPosition) {
          invalid("app service commands must be top-level action statements");
        }
        if (!core.graph.actions[command.service][command.method]) {
          invalid("app service method \"" + command.service + "." + command.method + "\" is not granted");
        }
        command.args.forEach(function (argument) { walk(argument, false); });
        return;
      }
      if (appServiceRoot(node)) {
        invalid("app service namespaces may only be used in static command calls");
      }
      if (node.type === "identifier") {
        if (node.name === "$app") invalid("the $app alias may only name a static service command");
        return;
      }
      if (node.type === "literal" || node.type === "update") return;
      if (node.type === "group" || node.type === "unary" || node.type === "chain") {
        walk(node.value, false);
        return;
      }
      if (node.type === "array") {
        node.items.forEach(function (item) { walk(item, false); });
        return;
      }
      if (node.type === "object") {
        node.entries.forEach(function (entry) { walk(entry.value, false); });
        return;
      }
      if (node.type === "lambda") {
        walk(node.body, false);
        return;
      }
      if (node.type === "assign") {
        walk(node.value, false);
        return;
      }
      if (node.type === "logical" || node.type === "coalesce" || node.type === "binary") {
        walk(node.left, false);
        walk(node.right, false);
        return;
      }
      if (node.type === "conditional") {
        walk(node.condition, false);
        walk(node.yes, false);
        walk(node.no, false);
        return;
      }
      if (node.type === "member") {
        if (ordinaryDirectAppMember(node)) return;
        walk(node.object, false);
        if (node.computed) walk(node.property, false);
        return;
      }
      if (node.type === "call") {
        if (!ordinaryDirectAppMember(node.callee)) walk(node.callee, false);
        node.args.forEach(function (argument) { walk(argument, false); });
      }
    }

    if (ast.type === "sequence") {
      ast.expressions.forEach(function (expression) { walk(expression, true); });
    } else walk(ast, true);
  }

  function compile(source, mode) {
    source = typeof source === "string" ? source.trim() : "";
    mode = mode === "action" ? "action" : "binding";
    var key = mode + "\u0000" + source;
    if (core.compiled.has(key)) return core.compiled.get(key);
    if (!source) core.syntax("empty expression", source, 0);
    var ast = core.parse(core.lex(source), source, mode);
    validateAppLoaderBinding(ast, source, mode);
    validateAppServiceStructure(ast, source, mode);
    var read = function (scope, locals, observeResult) {
      var root = mode === "action" ? actionResolver(scope) : directResolver(scope);
      var resolver = locals ? contextResolver(root, locals) : root;
      var results = [];
      var previousOwnership = activeOwnership;
      if (mode === "action") activeOwnership = { owners: new WeakMap(), services: new WeakMap(), stamp: 0 };
      function publish() {
        if (mode !== "action" || typeof observeResult !== "function") return;
        var grouped = new Map();
        results.forEach(function (entry) {
          var owners = grouped.get(entry.value);
          if (!owners) {
            owners = [];
            grouped.set(entry.value, owners);
          }
          if (entry.owner && owners.indexOf(entry.owner) < 0) owners.push(entry.owner);
        });
        grouped.forEach(function (owners, value) { observeResult(value, owners); });
        results.length = 0;
      }
      function capture(value) {
        rejectServiceValue(value);
        results.push({ value: value, owner: takeResultOwner(value, root.owner) });
      }
      try {
        var budget = { nodes: 0, depth: 0 };
        var result;
        if (mode === "action" && ast.type === "sequence") {
          ast.expressions.forEach(function (expression) {
            result = evaluate(expression, resolver, budget);
            capture(result);
          });
        } else {
          result = evaluate(ast, resolver, budget);
          if (mode === "action") capture(result);
        }
        if (root.commit) root.commit();
        publish();
        return result;
      } catch (error) {
        if (root.abort) root.abort();
        publish();
        throw error;
      } finally {
        activeOwnership = previousOwnership;
      }
    };
    if (core.compiled.size >= core.cacheLimit) {
      core.compiled.delete(core.compiled.keys().next().value);
    }
    core.compiled.set(key, read);
    return read;
  }

  core.compile = compile;
  core.phase = "evaluator";
})(document);
;(function (document) {
  "use strict";

  var core = document[Symbol.for("kitjs:assembly")];
  if (!core || core.phase !== "evaluator") throw new Error("KitJS: scope loaded out of order");
  if (core.reuse) { core.phase = "scope"; return; }

  var SOURCE_LIMIT = 16384;
  var DEPTH_LIMIT = 32;
  var NODE_LIMIT = 1024;
  var IDENTIFIER = /^[A-Za-z_][A-Za-z0-9_]*$/;
  var VALUE_WORDS = Object.create(null);
  VALUE_WORDS.true = VALUE_WORDS.false = VALUE_WORDS.null = true;
  var PROTOTYPE_KEYS = Object.create(null);
  ("constructor prototype __proto__ __defineGetter__ __defineSetter__ " +
    "__lookupGetter__ __lookupSetter__").split(" ").forEach(function (name) {
      PROTOTYPE_KEYS[name] = true;
    });
  var metadata = new WeakMap();
  var MOUNTED = Object.freeze(Object.create(null));

  function parseScope(source) {
    source = typeof source === "string" ? source : "";
    if (source.length > SOURCE_LIMIT) {
      throw new RangeError("KitJS: data-kit-scope exceeds " + SOURCE_LIMIT + " UTF-16 code units");
    }

    var index = 0;
    var nodes = 0;

    function syntax(message, at) { core.syntax(message, source, at === undefined ? index : at); }
    function space(character) {
      return character === " " || character === "\t" || character === "\n" ||
        character === "\r" || character === "\f";
    }
    function skip() { while (space(source.charAt(index))) index++; }
    function count(value) {
      if (++nodes > NODE_LIMIT) {
        throw new RangeError("KitJS: data-kit-scope exceeds " + NODE_LIMIT + " data nodes");
      }
      return value;
    }
    function depth(level) {
      if (level > DEPTH_LIMIT) {
        throw new RangeError("KitJS: data-kit-scope exceeds " + DEPTH_LIMIT + " data levels");
      }
    }
    function hexadecimal(character) {
      return character >= "0" && character <= "9" || character >= "a" && character <= "f" ||
        character >= "A" && character <= "F";
    }

    function stringValue() {
      var start = index;
      var quote = source.charAt(index++);
      var output = "";
      function appendCodeUnit(unit, at) {
        if (unit >= 0xDC00 && unit <= 0xDFFF) syntax("lone UTF-16 low surrogate", at);
        if (unit < 0xD800 || unit > 0xDBFF) {
          output += String.fromCharCode(unit);
          return;
        }
        var low;
        if (source.charAt(index) === "\\") {
          if (source.charAt(index + 1) !== "u") syntax("invalid UTF-16 surrogate pair", at);
          var lowHex = source.slice(index + 2, index + 6);
          if (lowHex.length !== 4 || !Array.prototype.every.call(lowHex, hexadecimal)) {
            syntax("invalid UTF-16 surrogate pair", at);
          }
          low = parseInt(lowHex, 16);
          if (low < 0xDC00 || low > 0xDFFF) syntax("invalid UTF-16 surrogate pair", at);
          index += 6;
        } else {
          low = source.charCodeAt(index);
          if (low < 0xDC00 || low > 0xDFFF) syntax("invalid UTF-16 surrogate pair", at);
          index++;
        }
        output += String.fromCharCode(unit, low);
      }
      while (index < source.length) {
        var character = source.charAt(index++);
        if (character === quote) return output;
        if (character === "\\") {
          if (index >= source.length) syntax("unfinished string", start);
          var escaped = source.charAt(index++);
          if (escaped === "u") {
            var unicodeAt = index - 2;
            var hexadecimalValue = source.slice(index, index + 4);
            if (hexadecimalValue.length !== 4 || !Array.prototype.every.call(hexadecimalValue, hexadecimal)) {
              syntax("invalid unicode string escape", unicodeAt);
            }
            index += 4;
            appendCodeUnit(parseInt(hexadecimalValue, 16), unicodeAt);
          } else if (escaped === "n") output += "\n";
          else if (escaped === "r") output += "\r";
          else if (escaped === "t") output += "\t";
          else if (escaped === "b") output += "\b";
          else if (escaped === "f") output += "\f";
          else if (escaped === "\\" || escaped === "/" || escaped === '"' || escaped === "'") {
            output += escaped;
          } else syntax("unsupported string escape \\" + escaped, index - 2);
          continue;
        }
        var unit = character.charCodeAt(0);
        if (unit < 32) syntax("unescaped control character in string", index - 1);
        appendCodeUnit(unit, index - 1);
      }
      syntax("unfinished string", start);
    }

    function name() {
      skip();
      var start = index;
      var character = source.charAt(index);
      var output;
      if (character === '"' || character === "'") output = stringValue();
      else {
        if (!/[A-Za-z_$]/.test(character)) syntax("expected a scope field name", start);
        index++;
        while (/[A-Za-z0-9_$]/.test(source.charAt(index))) index++;
        output = source.slice(start, index);
      }
      if (!IDENTIFIER.test(output) || VALUE_WORDS[output] || core.FORBIDDEN[output] || core.blocked(output)) {
        syntax("invalid scope field \"" + output + "\"", start);
      }
      return output;
    }

    function objectKey(topLevel) {
      skip();
      var start = index;
      var character = source.charAt(index);
      if (topLevel || character !== '"' && character !== "'") return name();
      var output = stringValue();
      if (PROTOTYPE_KEYS[output]) syntax("blocked object key \"" + output + "\"", start);
      return output;
    }

    function numberValue() {
      var start = index;
      var match = /^[+-]?(?:(?:0|[1-9][0-9]*)(?:\.[0-9]+)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?/.exec(source.slice(index));
      if (!match) syntax("invalid number", start);
      index += match[0].length;
      var output = Number(match[0]);
      if (!Number.isFinite(output)) syntax("number is outside the supported range", start);
      return output;
    }

    function objectValue(level, requireEntry, topLevel) {
      depth(level);
      var output = count(Object.create(null));
      var seen = Object.create(null);
      index++;
      skip();
      if (source.charAt(index) === "}") {
        if (requireEntry) syntax("scope objects cannot be empty", index);
        index++;
        return output;
      }
      while (index < source.length) {
        var keyAt = index;
        var key = objectKey(topLevel);
        if (seen[key]) syntax("duplicate scope field \"" + key + "\"", keyAt);
        seen[key] = true;
        skip();
        if (source.charAt(index) !== ":") syntax("expected \":\"", index);
        index++;
        output[key] = value(level);
        skip();
        var separator = source.charAt(index);
        if (separator === "}") { index++; return output; }
        if (separator !== ",") syntax("expected \",\" or \"}\"", index);
        index++;
        skip();
        if (source.charAt(index) === "}") { index++; return output; }
      }
      syntax("expected \"}\"", index);
    }

    function arrayValue(level) {
      depth(level);
      var output = count([]);
      index++;
      skip();
      if (source.charAt(index) === "]") { index++; return output; }
      while (index < source.length) {
        output.push(value(level));
        skip();
        var separator = source.charAt(index);
        if (separator === "]") { index++; return output; }
        if (separator !== ",") syntax("expected \",\" or \"]\"", index);
        index++;
        skip();
        if (source.charAt(index) === "]") syntax("arrays reject a trailing comma", index);
      }
      syntax("expected \"]\"", index);
    }

    function value(parentLevel) {
      skip();
      var character = source.charAt(index);
      if (character === "{") return objectValue(parentLevel + 1, false, false);
      if (character === "[") return arrayValue(parentLevel + 1);
      if (character === '"' || character === "'") return count(stringValue());
      if (character === "+" || character === "-" || character === "." ||
        character >= "0" && character <= "9") return count(numberValue());
      var start = index;
      if (/[A-Za-z_$]/.test(character)) {
        index++;
        while (/[A-Za-z0-9_$]/.test(source.charAt(index))) index++;
        var word = source.slice(start, index);
        if (word === "true") return count(true);
        if (word === "false") return count(false);
        if (word === "null") return count(null);
        syntax("scope values must be pure data; found identifier \"" + word + "\"", start);
      }
      syntax("expected a scope value", start);
    }

    function shorthand() {
      depth(1);
      var output = count(Object.create(null));
      var seen = Object.create(null);
      while (index < source.length) {
        var keyAt = index;
        var key = name();
        if (seen[key]) syntax("duplicate scope field \"" + key + "\"", keyAt);
        seen[key] = true;
        skip();
        if (source.charAt(index) !== ":") syntax("expected \":\"", index);
        index++;
        output[key] = value(1);
        skip();
        if (index >= source.length) return output;
        if (source.charAt(index) !== ";") syntax("expected \";\"", index);
        index++;
        skip();
        if (index >= source.length) return output;
      }
      return output;
    }

    skip();
    if (index >= source.length) syntax("empty data-kit-scope", index);
    var output = source.charAt(index) === "{" ? objectValue(1, true, true) : shorthand();
    skip();
    if (index !== source.length) syntax("unexpected token \"" + source.charAt(index) + "\"", index);
    return output;
  }

  function scopeElementValue(element) {
    if (String(element.localName || "").toLowerCase() === "template") {
      throw new TypeError(
        "KitJS: data-kit-scope cannot be used on a template; place the boundary inside template.content"
      );
    }
    return parseScope(element.getAttribute("data-kit-scope"));
  }

  function scopeSeed(element, shouldReport) {
    if (!element || element.nodeType !== 1 || !element.hasAttribute("data-kit-scope")) return undefined;
    if (core.ignoredForRuntime(element)) return undefined;
    var mounted = core.scopes && core.scopes.get(element);
    if (mounted) return mounted.failed || mounted.disposed ? null : MOUNTED;
    var source = element.getAttribute("data-kit-scope");
    var entry = metadata.get(element);
    if (!entry || entry.source !== source) {
      entry = { source: source, value: null, error: null, reported: false };
      try { entry.value = scopeElementValue(element); }
      catch (error) { entry.error = error; }
      metadata.set(element, entry);
    }
    if (entry.error && shouldReport && !entry.reported) {
      entry.reported = true;
      core.report(entry.error);
    }
    return entry.error ? null : entry.value;
  }

  function validateScopeTree(root) {
    if (!root || root.nodeType !== 1 && root.nodeType !== 9 && root.nodeType !== 11) return true;
    if (root.nodeType === 1 && core.ignoredForRuntime(root)) return true;
    if (root.nodeType === 1 && root.hasAttribute("data-kit-scope")) {
      scopeElementValue(root);
    }
    if (!root.querySelectorAll) return true;
    root.querySelectorAll("[data-kit-scope]").forEach(function (element) {
      if (core.ignoredForRuntime(element)) return;
      scopeElementValue(element);
    });
    root.querySelectorAll("template").forEach(function (template) {
      if (!core.ignoredForRuntime(template) && template.content) validateScopeTree(template.content);
    });
    return true;
  }

  core.parseScope = parseScope;
  core.blockedScopeKey = function (name) { return PROTOTYPE_KEYS[name] === true; };
  core.scopeSeed = scopeSeed;
  core.releaseScopeSeed = function (element) { metadata.delete(element); };
  core.validateScopeTree = validateScopeTree;
  core.phase = "scope";
})(document);
; (function (document) {
  "use strict";

  var core = document[Symbol.for("kitjs:assembly")];
  if (!core || core.phase !== "scope") throw new Error("KitJS: component loaded out of order");
  if (core.reuse) { core.phase = "component"; return; }

  var OWN = core.OWN;
  var BOUNDARIES = "[data-kit-component],[data-kit-scope]";
  var METADATA = "[data-kit-component],[data-kit-version],[data-kit-local],[data-kit-scope]";
  var ALIASES = "[data-kit-as]";
  var aliases = new WeakMap();
  var metadata = new WeakMap();
  var localRegistry = new Map();
  var missingLocalReports = new WeakSet();
  var localAuditComplete = false;
  var componentCache = new Map();
  var componentHandoff = null;
  var componentRegistration = null;
  var componentRegistrationGraph = null;
  var cleanupObserver = null;
  var cleanupOwners = 0;
  var removedRoots = new Set();
  var removalQueued = false;
  var retainValidity = new WeakMap();
  var retainStructureValidity = new WeakMap();
  var retainReports = new WeakMap();
  var COMPONENT_NAME = /^[A-Za-z_$][A-Za-z0-9_$.-]*$/;
  var SERVICE_NAME = /^[A-Za-z][A-Za-z0-9_.-]*$/;
  var SERVICE_ACTION = /^[A-Za-z_$][A-Za-z0-9_$]*$/;
  var GRAPH_ID = /^[0-9a-f]{64}$/;
  var RETAIN_KEY = /^[A-Za-z][A-Za-z0-9._:-]{0,127}$/;
  var EXACT_SEMVER = /^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-((?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$/;
  var RESERVED_ALIASES = {
    $element: true, $host: true, $event: true, $refs: true, $component: true,
    $parent: true, $error: true, $alias: true, $invalidate: true
  };

  function enqueue(callback) {
    var schedule = document.defaultView && document.defaultView.queueMicrotask;
    if (typeof schedule === "function") schedule.call(document.defaultView, callback);
    else Promise.resolve().then(callback);
  }

  function connectedHere(element) {
    return !!element && element.isConnected === true && element.ownerDocument === document;
  }

  function flushRemovedRoots() {
    removalQueued = false;
    var roots = Array.from(removedRoots);
    removedRoots.clear();
    roots.forEach(function (root) {
      if (connectedHere(root) || typeof core.disposeTree !== "function") return;
      try { core.disposeTree(root); }
      catch (error) { core.report(error); }
    });
  }

  function queueRemovedRoot(root) {
    if (!root || root.nodeType !== 1) return;
    removedRoots.add(root);
    if (removalQueued) return;
    removalQueued = true;
    enqueue(flushRemovedRoots);
  }

  function startCleanupObserver() {
    if (cleanupObserver || cleanupOwners < 1) return;
    var Observer = document.defaultView && document.defaultView.MutationObserver;
    if (typeof Observer !== "function") return;
    cleanupObserver = new Observer(function (mutations) {
      mutations.forEach(function (mutation) {
        Array.prototype.forEach.call(mutation.removedNodes || [], queueRemovedRoot);
      });
    });
    cleanupObserver.observe(document, { childList: true, subtree: true });
  }

  function stopCleanupObserver() {
    if (cleanupOwners > 0 || !cleanupObserver) return;
    cleanupObserver.disconnect();
    cleanupObserver = null;
  }

  var NOOP_CANCEL = Object.freeze(function () { });

  function ensureCleanupOwner(current) {
    if (current.ownsCleanup) return;
    if (!connectedHere(current.host)) {
      queueRemovedRoot(current.host);
      return;
    }
    current.ownsCleanup = true;
    cleanupOwners++;
    startCleanupObserver();
  }

  function releaseCleanupOwner(current) {
    if (!current.ownsCleanup || current.cleanups.length || current.afterRenders.length) return;
    current.ownsCleanup = false;
    cleanupOwners--;
    stopCleanupObserver();
  }

  function removeLifecycleEntry(entries, entry) {
    var index = entries.indexOf(entry);
    if (index >= 0) entries.splice(index, 1);
  }

  function invokeLifecycle(current, callback) {
    try { callback.call(current.scope); }
    catch (error) { core.report(error); }
  }

  function ownCleanup(current, cleanup) {
    if (!current || current.disposed) return NOOP_CANCEL;
    var entry = { callback: cleanup, active: true };
    current.cleanups.push(entry);
    ensureCleanupOwner(current);
    return Object.freeze(function () {
      if (!entry.active) return;
      entry.active = false;
      removeLifecycleEntry(current.cleanups, entry);
      invokeLifecycle(current, cleanup);
      releaseCleanupOwner(current);
    });
  }

  function lifecycleContext(current) {
    function owned(selector) {
      if (typeof selector !== "string") throw new TypeError("KitJS: context.owned(selector) expects a string");
      if (current.disposed) return Object.freeze([]);
      return Object.freeze(ownedElements(current, selector));
    }
    function cleanup(callback) {
      if (typeof callback !== "function") throw new TypeError("KitJS: context.cleanup(fn) expects a function");
      return ownCleanup(current, callback);
    }
    function listen(target, type, callback, options) {
      if (!target || typeof target.addEventListener !== "function" ||
        typeof target.removeEventListener !== "function") {
        throw new TypeError("KitJS: context.listen(target, type, fn, options) expects an EventTarget");
      }
      if (typeof type !== "string" || typeof callback !== "function") {
        throw new TypeError("KitJS: context.listen(target, type, fn, options) expects a string and function");
      }
      if (current.disposed) return NOOP_CANCEL;
      var capture = typeof options === "boolean" ? options : !!(options && options.capture);
      target.addEventListener(type, callback, options);
      return ownCleanup(current, function () {
        target.removeEventListener(type, callback, capture);
      });
    }
    function afterRender(callback) {
      if (typeof callback !== "function") {
        throw new TypeError("KitJS: context.afterRender(fn) expects a function");
      }
      if (current.disposed) return NOOP_CANCEL;
      var entry = { callback: callback, active: true };
      current.afterRenders.push(entry);
      ensureCleanupOwner(current);
      return Object.freeze(function () {
        if (!entry.active) return;
        entry.active = false;
        removeLifecycleEntry(current.afterRenders, entry);
        releaseCleanupOwner(current);
      });
    }
    var context = {};
    Object.defineProperties(context, {
      host: { get: function () { return current.host; }, enumerable: true },
      owned: { value: Object.freeze(owned), enumerable: true },
      listen: { value: Object.freeze(listen), enumerable: true },
      cleanup: { value: Object.freeze(cleanup), enumerable: true },
      afterRender: { value: Object.freeze(afterRender), enumerable: true }
    });
    return Object.freeze(context);
  }

  function flushAfterRender(current) {
    if (!current || current.disposed || !current.afterRenders.length) return;
    if (!connectedHere(current.host)) {
      queueRemovedRoot(current.host);
      return;
    }
    var entries = current.afterRenders.slice();
    current.afterRenders.length = 0;
    entries.forEach(function (entry) {
      if (!entry.active || current.disposed) return;
      entry.active = false;
      invokeLifecycle(current, entry.callback);
    });
    releaseCleanupOwner(current);
  }

  function copyValue(value, seen, scopeData) {
    if (value === null || typeof value !== "object") return value;
    var prototype = Object.getPrototypeOf(value);
    if (!Array.isArray(value) && prototype !== Object.prototype && prototype !== null) {
      throw new TypeError("KitJS: component state must contain only plain objects and arrays");
    }
    if (seen.has(value)) throw new TypeError("KitJS: circular component state is not supported");
    seen.add(value);
    var output = Array.isArray(value) ? [] : Object.create(prototype);
    Object.keys(value).forEach(function (name) {
      if (scopeData ? core.blockedScopeKey(name) : core.blocked(name)) {
        throw new TypeError("KitJS: blocked component state key \"" + name + "\"");
      }
      output[name] = copyValue(value[name], seen, scopeData);
    });
    seen.delete(value);
    return output;
  }

  function snapshot(definition) {
    var descriptors = Object.getOwnPropertyDescriptors(definition);
    if (Object.getOwnPropertySymbols(definition).length) {
      throw new TypeError("KitJS: component definitions cannot contain symbol fields");
    }
    Object.keys(descriptors).forEach(function (name) {
      if (name.charAt(0) === "$") {
        throw new TypeError("KitJS: component fields cannot use the reserved $ namespace");
      }
      if (core.blocked(name)) throw new TypeError("KitJS: blocked component field \"" + name + "\"");
      var descriptor = descriptors[name];
      if (OWN.call(descriptor, "value") && typeof descriptor.value === "object" && descriptor.value !== null) {
        descriptor.value = copyValue(descriptor.value, new WeakSet());
      }
    });
    return descriptors;
  }

  function validComponentName(name) {
    return typeof name === "string" && COMPONENT_NAME.test(name) && !core.blocked(name);
  }

  function validServiceName(name) {
    return typeof name === "string" && SERVICE_NAME.test(name) &&
      name !== "version" && name !== "component" && name !== "service" &&
      !core.blocked(name);
  }

  function validVersion(version) {
    return typeof version === "string" && EXACT_SEMVER.test(version);
  }

  function graphError(message) {
    throw new TypeError("KitJS: invalid component graph: " + message);
  }

  function plainObject(value) {
    var prototype = value && Object.getPrototypeOf(value);
    return !!value && (prototype === Object.prototype || prototype === null) &&
      Object.getOwnPropertySymbols(value).length === 0;
  }

  function normalizeComponentGraph(source) {
    var prototype = source && Object.getPrototypeOf(source);
    if (!source || prototype !== Object.prototype && prototype !== null ||
      Object.getOwnPropertySymbols(source).length) graphError("expected a plain object");
    var id = source.id;
    var profile = source.profile;
    var artifact = source.artifact;
    var services = source.services;
    var components = source.components;
    var componentHashes = source.componentHashes;
    var actions = source.actions;
    var grants = source.grants;
    if (typeof id !== "string" || !GRAPH_ID.test(id)) graphError("invalid id");
    if (profile !== "kit" && profile !== "hydrate") graphError("invalid profile");
    if (artifact !== undefined && (typeof artifact !== "string" || !GRAPH_ID.test(artifact))) {
      graphError("invalid artifact");
    }
    if (profile !== core.profile) {
      graphError("profile does not match the assembled runtime");
    }
    if (!plainObject(services)) graphError("services must be a plain object");
    if (!plainObject(components)) graphError("components must be a plain object");
    if (!plainObject(actions)) graphError("actions must be a plain object");
    if (!plainObject(grants)) graphError("grants must be a plain object");

    var serviceManifest = Object.create(null);
    Object.keys(services).forEach(function (name) {
      var version = services[name];
      if (!validServiceName(name)) graphError("invalid service name \"" + name + "\"");
      if (!validVersion(version)) graphError("invalid version for service \"" + name + "\"");
      serviceManifest[name] = version;
    });
    if (Object.keys(serviceManifest).length && !(core.serviceRegistry instanceof Map)) {
      graphError("services require the service registrar");
    }

    var componentManifest = Object.create(null);
    Object.keys(components).forEach(function (name) {
      var version = components[name];
      if (!validComponentName(name)) graphError("invalid component name \"" + name + "\"");
      if (!validVersion(version)) graphError("invalid version for component \"" + name + "\"");
      componentManifest[name] = version;
    });
    var hashManifest;
    if (componentHashes !== undefined) {
      if (artifact === undefined) graphError("staged graph requires an artifact");
      if (!plainObject(componentHashes)) graphError("componentHashes must be a plain object");
      hashManifest = Object.create(null);
      Object.keys(componentHashes).forEach(function (name) {
        if (!OWN.call(componentManifest, name)) {
          graphError("componentHashes names undeclared component \"" + name + "\"");
        }
        if (typeof componentHashes[name] !== "string" || !GRAPH_ID.test(componentHashes[name])) {
          graphError("invalid source hash for component \"" + name + "\"");
        }
        hashManifest[name] = componentHashes[name];
      });
      Object.keys(componentManifest).forEach(function (name) {
        if (!OWN.call(hashManifest, name)) {
          graphError("missing source hash for component \"" + name + "\"");
        }
      });
      Object.freeze(hashManifest);
    }

    var actionManifest = Object.create(null);
    Object.keys(actions).forEach(function (serviceName) {
      if (!OWN.call(serviceManifest, serviceName)) {
        graphError("actions name undeclared service \"" + serviceName + "\"");
      }
      var members = actions[serviceName];
      var memberPrototype = members && Object.getPrototypeOf(members);
      if (!members || memberPrototype !== Object.prototype && memberPrototype !== null ||
        Object.getOwnPropertySymbols(members).length) {
        graphError("actions for service \"" + serviceName + "\" must be a plain object");
      }
      var normalized = Object.create(null);
      Object.keys(members).forEach(function (memberName) {
        if (!SERVICE_ACTION.test(memberName) || core.blocked(memberName) || members[memberName] !== true) {
          graphError("invalid authored action \"" + serviceName + "." + memberName + "\"");
        }
        normalized[memberName] = true;
      });
      actionManifest[serviceName] = Object.freeze(normalized);
    });
    Object.keys(serviceManifest).forEach(function (serviceName) {
      if (!OWN.call(actionManifest, serviceName)) {
        graphError("missing actions for service \"" + serviceName + "\"");
      }
    });

    var grantManifest = Object.create(null);
    Object.keys(grants).forEach(function (componentName) {
      if (!OWN.call(componentManifest, componentName)) {
        graphError("grants name undeclared component \"" + componentName + "\"");
      }
      var dependencies = grants[componentName];
      var dependencyPrototype = dependencies && Object.getPrototypeOf(dependencies);
      if (!dependencies || dependencyPrototype !== Object.prototype && dependencyPrototype !== null ||
        Object.getOwnPropertySymbols(dependencies).length) {
        graphError("grants for component \"" + componentName + "\" must be a plain object");
      }
      var normalized = Object.create(null);
      Object.keys(dependencies).forEach(function (serviceName) {
        if (!OWN.call(serviceManifest, serviceName) || dependencies[serviceName] !== serviceManifest[serviceName]) {
          graphError("invalid grant \"" + componentName + " -> " + serviceName + "\"");
        }
        normalized[serviceName] = dependencies[serviceName];
      });
      grantManifest[componentName] = Object.freeze(normalized);
    });
    Object.keys(componentManifest).forEach(function (componentName) {
      if (!OWN.call(grantManifest, componentName)) {
        graphError("missing grants for component \"" + componentName + "\"");
      }
    });
    Object.freeze(serviceManifest);
    Object.freeze(componentManifest);
    Object.freeze(actionManifest);
    Object.freeze(grantManifest);
    var graphSource = {
      id: id,
      profile: profile,
      services: serviceManifest,
      components: componentManifest,
      actions: actionManifest,
      grants: grantManifest
    };
    if (artifact !== undefined) graphSource.artifact = artifact;
    if (hashManifest) graphSource.componentHashes = hashManifest;
    return Object.freeze(graphSource);
  }

  function stagedGraph(graph) {
    return !!graph && OWN.call(graph, "componentHashes");
  }

  function installComponentGraph(source) {
    if (core.graph) throw new Error("KitJS: component graph is already installed");
    if (core.booted || core.phase !== "events" && core.phase !== "drive") {
      throw new Error("KitJS: component graph must be installed immediately before boot");
    }
    var graph = normalizeComponentGraph(source);
    if (core.serviceRegistry) {
      core.serviceRegistry.forEach(function (_, name) {
        if (!OWN.call(graph.services, name)) {
          graphError("registered service \"" + name + "\" is not declared");
        }
      });
    }
    core.registry.forEach(function (_, name) {
      if (!OWN.call(graph.components, name)) {
        graphError("registered component \"" + name + "\" is not declared");
      }
    });
    localRegistry.forEach(function (_, name) {
      if (OWN.call(graph.components, name)) {
        graphError("client component \"" + name + "\" conflicts with a managed component");
      }
    });
    core.graph = graph;
    if (stagedGraph(graph)) {
      Object.defineProperty(core.kit, Symbol.for("kitjs:graph"), {
        get: function () { return componentRegistrationGraph || core.graph; }
      });
    } else {
      Object.defineProperty(core.kit, Symbol.for("kitjs:graph"), { value: graph });
    }
  }

  function deliveryError(message) {
    throw new TypeError("KitJS: invalid staged delivery: " + message);
  }

  function normalizeDeliveryComponent(source) {
    if (!plainObject(source) || !validComponentName(source.name) || !validVersion(source.version) ||
      typeof source.sourceHash !== "string" || !GRAPH_ID.test(source.sourceHash)) {
      deliveryError("invalid component source identity");
    }
    return Object.freeze({
      name: source.name,
      version: source.version,
      sourceHash: source.sourceHash
    });
  }

  function normalizeStagedDelivery(source, graph) {
    if (!plainObject(source) || !stagedGraph(graph)) deliveryError("expected a staged graph contract");
    if (source.profile !== graph.profile || source.graphKey !== graph.id ||
      source.graphHash !== graph.artifact || typeof source.runtimeHash !== "string" ||
      !GRAPH_ID.test(source.runtimeHash) || source.hydrateHash !== null &&
      (typeof source.hydrateHash !== "string" || !GRAPH_ID.test(source.hydrateHash)) ||
      !Array.isArray(source.assets)) {
      deliveryError("graph identity does not match");
    }
    if (graph.profile === "hydrate" && source.hydrateHash === null ||
      graph.profile === "kit" && source.hydrateHash !== null) {
      deliveryError("profile assets do not match");
    }

    var assets = [];
    var roleCounts = Object.create(null);
    var serviceAssets = Object.create(null);
    var componentSources = Object.create(null);
    var graphAsset = null;
    var runtimeAsset = null;
    var hydrateAsset = null;
    source.assets.forEach(function (assetSource) {
      if (!plainObject(assetSource)) deliveryError("asset must be a plain object");
      var role = assetSource.role;
      var packageName = assetSource.package;
      var version = assetSource.version;
      var hash = assetSource.hash;
      var integrity = assetSource.integrity;
      var name = assetSource.name;
      var url = assetSource.url;
      if (["runtime", "hydrate", "graph", "service", "component", "components"].indexOf(role) < 0 ||
        typeof packageName !== "string" || typeof version !== "string" ||
        typeof hash !== "string" || !GRAPH_ID.test(hash) || typeof integrity !== "string" || !integrity ||
        typeof name !== "string" || name.indexOf(hash + ".") !== 0 || name.slice(-3) !== ".js" ||
        typeof url !== "string" || !url) {
        deliveryError("invalid asset identity");
      }

      var packages = null;
      if (assetSource.packages !== null && assetSource.packages !== undefined) {
        if (!Array.isArray(assetSource.packages)) deliveryError("asset packages must be an array");
        packages = assetSource.packages.map(function (value) {
          if (typeof value !== "string" || !value) deliveryError("invalid asset package member");
          return value;
        });
        Object.freeze(packages);
      }
      var components = null;
      if (assetSource.components !== null && assetSource.components !== undefined) {
        if (!Array.isArray(assetSource.components) || !assetSource.components.length) {
          deliveryError("asset components must be a non-empty array");
        }
        components = assetSource.components.map(normalizeDeliveryComponent);
        Object.freeze(components);
      }
      var sourceHash = assetSource.sourceHash === null || assetSource.sourceHash === undefined
        ? null : assetSource.sourceHash;
      if (sourceHash !== null && (typeof sourceHash !== "string" || !GRAPH_ID.test(sourceHash))) {
        deliveryError("invalid component asset source hash");
      }

      if (role === "component") {
        if (!validComponentName(packageName) || !validVersion(version) || !components ||
          components.length !== 1 || components[0].name !== packageName ||
          components[0].version !== version || components[0].sourceHash !== sourceHash) {
          deliveryError("individual component mapping does not match its asset");
        }
      } else if (role === "components") {
        if (packageName || version || !components || components.length < 2 || sourceHash !== null) {
          deliveryError("component bundle mapping is incomplete");
        }
      } else if (components || sourceHash !== null) {
        deliveryError("non-component asset contains a component mapping");
      }

      var asset = Object.freeze({
        role: role,
        package: packageName,
        version: version,
        hash: hash,
        integrity: integrity,
        name: name,
        url: url,
        packages: packages,
        components: components,
        sourceHash: sourceHash
      });
      assets.push(asset);
      roleCounts[role] = (roleCounts[role] || 0) + 1;
      if (role === "runtime") runtimeAsset = asset;
      else if (role === "hydrate") hydrateAsset = asset;
      else if (role === "graph") graphAsset = asset;
      else if (role === "service") {
        if (!validServiceName(packageName) || !validVersion(version) || serviceAssets[packageName]) {
          deliveryError("invalid or duplicate service asset");
        }
        serviceAssets[packageName] = asset;
      }
      if (components) components.forEach(function (identity) {
        if (componentSources[identity.name]) deliveryError("duplicate component source mapping");
        componentSources[identity.name] = identity;
      });
    });
    Object.freeze(assets);

    if (roleCounts.runtime !== 1 || roleCounts.graph !== 1 ||
      graph.profile === "hydrate" && roleCounts.hydrate !== 1 ||
      graph.profile === "kit" && roleCounts.hydrate || !runtimeAsset || !graphAsset ||
      runtimeAsset.hash !== source.runtimeHash || graphAsset.hash !== source.graphHash ||
      graphAsset.integrity !== source.graphIntegrity || graphAsset.name !== source.graphName ||
      graphAsset.url !== source.graphURL || hydrateAsset && hydrateAsset.hash !== source.hydrateHash) {
      deliveryError("required asset identities do not match");
    }
    Object.keys(graph.services).forEach(function (name) {
      var asset = serviceAssets[name];
      if (!asset || asset.version !== graph.services[name]) {
        deliveryError("missing service asset \"" + name + "\"");
      }
    });
    Object.keys(serviceAssets).forEach(function (name) {
      if (!OWN.call(graph.services, name)) deliveryError("undeclared service asset \"" + name + "\"");
    });
    Object.keys(graph.components).forEach(function (name) {
      var identity = componentSources[name];
      if (!identity || identity.version !== graph.components[name] ||
        identity.sourceHash !== graph.componentHashes[name]) {
        deliveryError("missing component source mapping \"" + name + "\"");
      }
    });
    Object.keys(componentSources).forEach(function (name) {
      if (!OWN.call(graph.components, name)) deliveryError("undeclared component source mapping \"" + name + "\"");
    });

    return Object.freeze({
      profile: source.profile,
      graphKey: source.graphKey,
      runtimeHash: source.runtimeHash,
      hydrateHash: source.hydrateHash,
      graphHash: source.graphHash,
      graphIntegrity: source.graphIntegrity,
      graphName: source.graphName,
      graphURL: source.graphURL,
      assets: assets
    });
  }

  function installStagedDelivery(source) {
    if (!core.graph || !stagedGraph(core.graph) || core.activeDelivery) {
      throw new Error("KitJS: staged delivery cannot be installed");
    }
    core.activeDelivery = normalizeStagedDelivery(source, core.graph);
    return core.activeDelivery;
  }

  function metadataError(element, entry, message, shouldReport) {
    entry.value = null;
    entry.error = message;
    if (shouldReport && !entry.reported) {
      entry.reported = true;
      core.report(new TypeError("KitJS: " + message));
    }
    if (entry.cacheable) metadata.set(element, entry);
    return null;
  }

  function storeMetadata(element, entry, value) {
    entry.value = value;
    if (entry.cacheable) metadata.set(element, entry);
    return value;
  }

  // Undefined means this element carries no component metadata. Null means its
  // metadata is invalid. A record means it is a valid component host.
  function componentMetadata(element, shouldReport, requestedGraph) {
    if (!element || element.nodeType !== 1 || !element.hasAttribute) return undefined;
    if (core.ignoredForRuntime(element)) return undefined;
    var hasComponent = element.hasAttribute("data-kit-component");
    var hasVersion = element.hasAttribute("data-kit-version");
    var hasLocal = element.hasAttribute("data-kit-local");
    if (!hasComponent && !hasVersion && !hasLocal) return undefined;
    var componentSource = hasComponent ? element.getAttribute("data-kit-component") : null;
    var versionSource = hasVersion ? element.getAttribute("data-kit-version") : null;
    var localSource = hasLocal ? element.getAttribute("data-kit-local") : null;
    var retainSource = element.hasAttribute("data-kit-retain") ? element.getAttribute("data-kit-retain") : null;
    var cacheable = arguments.length < 3;
    var graphIdentity = cacheable ? core.graph || null : requestedGraph || null;
    var entry = cacheable ? metadata.get(element) : null;
    if (entry && entry.componentSource === componentSource && entry.versionSource === versionSource &&
      entry.localSource === localSource && entry.retainSource === retainSource &&
      entry.graphIdentity === graphIdentity) {
      if (shouldReport && entry.error && !entry.reported) {
        entry.reported = true;
        core.report(new TypeError("KitJS: " + entry.error));
      }
      return entry.value;
    }
    entry = {
      componentSource: componentSource,
      versionSource: versionSource,
      localSource: localSource,
      retainSource: retainSource,
      graphIdentity: graphIdentity,
      value: undefined,
      error: "",
      reported: false,
      cacheable: cacheable
    };
    if (!hasComponent) {
      return metadataError(element, entry,
        hasLocal ? "data-kit-local requires a component host" :
          "data-kit-version requires a component host", shouldReport);
    }
    if (String(element.localName || "").toLowerCase() === "template") {
      return metadataError(element, entry,
        "data-kit-component cannot be used on a template; place the boundary inside template.content",
        shouldReport);
    }
    var componentSpec = String(componentSource || "");
    var spec = componentSpec.trim();
    var inlineSpec = componentSpec.trimStart();
    var separator = spec.indexOf("@");
    var name = separator < 0 ? spec : inlineSpec.slice(0, inlineSpec.indexOf("@"));
    var inlineVersion = separator < 0 ? null : inlineSpec.slice(inlineSpec.indexOf("@") + 1);
    if (inlineVersion !== null && !validVersion(inlineVersion)) {
      return metadataError(element, entry,
        "inline component version must be an exact semantic version", shouldReport);
    }
    if (!validComponentName(name)) {
      return metadataError(element, entry, "invalid component name \"" + name + "\"", shouldReport);
    }
    if (hasLocal && localSource !== "") {
      return metadataError(element, entry,
        "data-kit-local must be an empty presence marker", shouldReport);
    }
    if (inlineVersion !== null && hasVersion) {
      return metadataError(element, entry,
        "inline component versions cannot be combined with data-kit-version", shouldReport);
    }
    var version = inlineVersion;
    if (hasVersion) {
      version = String(versionSource || "").trim();
      if (!validVersion(version)) {
        return metadataError(element, entry,
          "data-kit-version must be an exact semantic version", shouldReport);
      }
    }
    var managed = version !== null;
    if (hasLocal && managed) {
      return metadataError(element, entry,
        hasVersion ? "data-kit-local components cannot use data-kit-version" :
          "data-kit-local cannot mark a versioned component", shouldReport);
    }
    if (!managed) {
      if (element.hasAttribute("data-kit-retain") && (hasLocal || graphIdentity)) {
        return metadataError(element, entry,
          "unversioned client components cannot use data-kit-retain", shouldReport);
      }
      if (graphIdentity && OWN.call(graphIdentity.components, name)) {
        return metadataError(element, entry,
          "client component \"" + name + "\" conflicts with the installed graph", shouldReport);
      }
      return storeMetadata(element, entry, {
        name: name, version: null, lane: "client"
      });
    }
    if (!graphIdentity || !OWN.call(graphIdentity.components, name)) {
      return metadataError(element, entry,
        "component \"" + name + "\" is not present in the installed graph", shouldReport);
    }
    if (graphIdentity.components[name] !== version) {
      return metadataError(element, entry,
        "component \"" + name + "\" requires " + version +
        " but the installed graph provides " + graphIdentity.components[name], shouldReport);
    }
    return storeMetadata(element, entry, {
      name: name, version: version, lane: "managed"
    });
  }

  function componentMetadataForGraph(element, graph) {
    return componentMetadata(element, false, graph);
  }

  function retainIssue(entry, message) {
    if (entry.errors.indexOf(message) < 0) entry.errors.push(message);
  }

  function inspectRetains(root, shouldReport, strict) {
    root = root && (root.nodeType === 1 || root.nodeType === 9 || root.nodeType === 11) ? root : document;
    var entries = [];

    function rememberStructural(element) {
      retainStructureValidity.set(element, false);
    }

    function visit(node, templates, structures, retainedAncestor) {
      if (!node) return;
      if (node.nodeType === 1) {
        if (node.hasAttribute("data-kit-ignore")) return;
        var name = String(node.localName || "").toLowerCase();
        var nextTemplates = templates;
        var nextStructures = structures;
        if (name === "template") {
          rememberStructural(node);
          nextTemplates = templates.concat(node);
        }
        if (node.hasAttribute("data-kit-if") || node.hasAttribute("data-kit-for")) {
          rememberStructural(node);
          nextStructures = structures.concat(node);
        }

        var nextRetained = retainedAncestor;
        if (node.hasAttribute("data-kit-retain")) {
          var key = node.getAttribute("data-kit-retain");
          var entry = {
            key: key,
            element: node,
            request: null,
            mounted: null,
            blocked: false,
            errors: []
          };
          retainValidity.set(node, false);
          if (!RETAIN_KEY.test(key)) retainIssue(entry, "invalid key \"" + key + "\"");
          if (!node.hasAttribute("data-kit-component")) {
            retainIssue(entry, "key \"" + key + "\" requires a component host");
          } else {
            entry.request = componentMetadata(node, false);
            if (!entry.request) retainIssue(entry, "key \"" + key + "\" has invalid component metadata");
          }
          var mounted = core.scopes.get(node);
          if (core.scopes.has(node)) {
            if (mounted && !mounted.failed && !mounted.disposed && mounted.componentIdentity) {
              entry.mounted = mounted.componentIdentity;
            } else entry.blocked = true;
          }
          if (nextTemplates.length) {
            retainIssue(entry, "key \"" + key + "\" cannot be used inside a template");
            nextTemplates.forEach(function (template) { retainStructureValidity.set(template, true); });
          }
          if (nextStructures.length) {
            retainIssue(entry, "key \"" + key + "\" cannot be used in a structural region");
            nextStructures.forEach(function (structure) { retainStructureValidity.set(structure, true); });
          }
          if (retainedAncestor) {
            retainIssue(entry, "key \"" + key + "\" cannot be nested below retained key \"" +
              retainedAncestor.key + "\"");
            retainIssue(retainedAncestor, "retained key \"" + retainedAncestor.key +
              "\" cannot contain retained key \"" + key + "\"");
          }
          entries.push(entry);
          nextRetained = entry;
        }

        var child = node.firstChild;
        while (child) {
          visit(child, nextTemplates, nextStructures, nextRetained);
          child = child.nextSibling;
        }
        if (name === "template" && node.content) {
          child = node.content.firstChild;
          while (child) {
            visit(child, nextTemplates, nextStructures, nextRetained);
            child = child.nextSibling;
          }
        }
        return;
      }
      var descendant = node.firstChild;
      while (descendant) {
        visit(descendant, templates, structures, retainedAncestor);
        descendant = descendant.nextSibling;
      }
    }

    visit(root, [], [], null);
    var duplicates = new Map();
    entries.forEach(function (entry) {
      if (!duplicates.has(entry.key)) duplicates.set(entry.key, []);
      duplicates.get(entry.key).push(entry);
    });
    duplicates.forEach(function (matches, key) {
      if (matches.length < 2) return;
      matches.forEach(function (entry) { retainIssue(entry, "duplicate key \"" + key + "\""); });
    });

    var firstError = "";
    var byKey = new Map();
    entries.forEach(function (entry) {
      var invalid = entry.errors.length > 0;
      retainValidity.set(entry.element, invalid);
      if (!invalid) {
        retainReports.delete(entry.element);
        byKey.set(entry.key, entry);
        return;
      }
      var message = entry.errors[0];
      if (!firstError) firstError = message;
      if (shouldReport && retainReports.get(entry.element) !== message) {
        retainReports.set(entry.element, message);
        core.report(new TypeError("KitJS: invalid data-kit-retain: " + message));
      }
    });
    if (strict && firstError) throw new TypeError("KitJS: invalid data-kit-retain: " + firstError);
    return firstError ? null : byKey;
  }

  function prepareComponentTree(root, nested) {
    root = root && root.querySelectorAll ? root : document;
    if (root.nodeType === 1 && core.ignoredForRuntime(root)) return;
    if (root.nodeType === 1 && root.matches(METADATA)) {
      componentMetadata(root, true);
      core.scopeSeed(root, true);
    }
    root.querySelectorAll(METADATA).forEach(function (element) {
      if (core.ignoredForRuntime(element)) return;
      componentMetadata(element, true);
      core.scopeSeed(element, true);
    });
    root.querySelectorAll("template").forEach(function (template) {
      if (!core.ignoredForRuntime(template) && template.content) prepareComponentTree(template.content, true);
    });
    if (!nested) inspectRetains(root, true, false);
  }

  function validateComponentTree(root) {
    if (!root || root.nodeType !== 1 && root.nodeType !== 9 && root.nodeType !== 11) return true;
    function visit(node) {
      if (!node) return;
      if (node.nodeType === 1 && core.ignoredForRuntime(node)) return;
      if (node.nodeType === 1 && String(node.localName || "").toLowerCase() === "template") {
        if (node.hasAttribute("data-kit-component")) {
          throw new TypeError(
            "KitJS: data-kit-component cannot be used on a template; place the boundary inside template.content"
          );
        }
        if (node.content) visit(node.content);
      }
      var child = node.firstChild;
      while (child) {
        visit(child);
        child = child.nextSibling;
      }
    }
    visit(root);
    return true;
  }

  function assertComponentGraph() {
    if (!core.graph) return;
    if (core.serviceRegistry) {
      core.serviceRegistry.forEach(function (_, name) {
        if (!OWN.call(core.graph.services, name)) {
          throw new Error("KitJS: service \"" + name + "\" is not declared by the installed graph");
        }
      });
    }
    Object.keys(core.graph.services).forEach(function (name) {
      if (!core.serviceRegistry || !core.serviceRegistry.has(name)) {
        throw new Error("KitJS: service graph is missing definition \"" + name + "\"");
      }
    });
    core.registry.forEach(function (_, name) {
      if (!OWN.call(core.graph.components, name)) {
        throw new Error("KitJS: component \"" + name + "\" is not declared by the installed graph");
      }
    });
    localRegistry.forEach(function (_, name) {
      if (OWN.call(core.graph.components, name)) {
        throw new Error("KitJS: client component \"" + name + "\" conflicts with the installed graph");
      }
    });
    Object.keys(core.graph.components).forEach(function (name) {
      if (!core.registry.has(name)) {
        throw new Error("KitJS: component graph is missing definition \"" + name + "\"");
      }
      if (stagedGraph(core.graph)) {
        var key = componentCacheKey(name, core.graph.components[name], core.graph.componentHashes[name]);
        var cached = componentCache.get(key);
        if (!cached || cached.descriptors !== core.registry.get(name)) {
          throw new Error("KitJS: component graph is missing exact package \"" + name + "\"");
        }
      }
    });
  }

  function componentDescriptors(name, definition, graph) {
    if (typeof name !== "string" || !COMPONENT_NAME.test(name)) {
      throw new TypeError("KitJS: invalid component name");
    }
    if (core.blocked(name)) throw new TypeError("KitJS: blocked component name");
    if (graph && !OWN.call(graph.components, name)) {
      throw new Error("KitJS: component \"" + name + "\" is not declared by the installed graph");
    }
    var prototype = definition && Object.getPrototypeOf(definition);
    if (!definition || prototype !== Object.prototype && prototype !== null) {
      throw new TypeError("KitJS: component definition must be a plain object");
    }
    // Only the canonical app boundary projects service names into its authored
    // action alias. Other trusted components may use a same-named reactive
    // field even when their JavaScript package depends on that service.
    var granted = name === "app" && graph && graph.grants[name];
    if (granted) Object.keys(granted).forEach(function (serviceName) {
      if (OWN.call(definition, serviceName)) {
        throw new TypeError("KitJS: component field \"" + serviceName +
          "\" conflicts with a granted service");
      }
    });
    return snapshot(definition);
  }

  function assertDescriptorsForGraph(name, descriptors, graph) {
    var granted = name === "app" && graph && graph.grants[name];
    if (!granted) return true;
    Object.keys(granted).forEach(function (serviceName) {
      if (OWN.call(descriptors, serviceName)) {
        throw new TypeError("KitJS: cached component field \"" + serviceName +
          "\" conflicts with a granted service");
      }
    });
    return true;
  }

  function component(name, definition) {
    if (arguments.length !== 2) throw new TypeError("KitJS: component(name, definition) expects two arguments");
    if (componentRegistration) return componentRegistration(name, definition);
    if (typeof name !== "string" || !COMPONENT_NAME.test(name)) {
      throw new TypeError("KitJS: invalid component name");
    }
    if (core.blocked(name)) throw new TypeError("KitJS: blocked component name");
    var managed = !core.graph || OWN.call(core.graph.components, name);
    if (!managed && componentHandoff && OWN.call(componentHandoff.graph.components, name)) {
      throw new Error("KitJS: client component \"" + name + "\" conflicts with the pending graph");
    }
    var registry = managed ? core.registry : localRegistry;
    if (core.registry.has(name) || localRegistry.has(name)) {
      throw new Error("KitJS: component \"" + name + "\" already exists");
    }
    if (!managed && localRegistry.size >= core.cacheLimit) {
      throw new Error("KitJS: client component registry limit exceeded");
    }
    registry.set(name, componentDescriptors(name, definition, managed ? core.graph : null));
    if (core.booted) core.invalidate();
  }

  function componentCacheKey(name, version, sourceHash) {
    return name + "\u0000" + version + "\u0000" + sourceHash;
  }

  function recordStagedComponentPackage(name, version, sourceHash) {
    if (!stagedGraph(core.graph) || !validComponentName(name) || !validVersion(version) ||
      typeof sourceHash !== "string" || !GRAPH_ID.test(sourceHash) ||
      core.graph.components[name] !== version || core.graph.componentHashes[name] !== sourceHash ||
      !core.registry.has(name)) {
      throw new Error("KitJS: staged component package does not match the active graph");
    }
    var key = componentCacheKey(name, version, sourceHash);
    if (componentCache.has(key)) throw new Error("KitJS: staged component package is already cached");
    componentCache.forEach(function (entry) {
      if (entry.name === name && entry.version === version && entry.sourceHash !== sourceHash) {
        throw new Error("KitJS: staged component version has conflicting source bytes");
      }
    });
    if (componentCache.size >= core.cacheLimit) {
      throw new Error("KitJS: staged component cache limit exceeded");
    }
    componentCache.set(key, Object.freeze({
      name: name,
      version: version,
      sourceHash: sourceHash,
      descriptors: core.registry.get(name)
    }));
  }

  function sameStringManifest(left, right) {
    var leftNames = Object.keys(left);
    var rightNames = Object.keys(right);
    return leftNames.length === rightNames.length && leftNames.every(function (name) {
      return OWN.call(right, name) && left[name] === right[name];
    });
  }

  function sameActionManifest(left, right) {
    var leftNames = Object.keys(left);
    var rightNames = Object.keys(right);
    return leftNames.length === rightNames.length && leftNames.every(function (name) {
      return OWN.call(right, name) && sameStringManifest(left[name], right[name]);
    });
  }

  function serviceAssetMap(delivery) {
    var output = Object.create(null);
    delivery.assets.forEach(function (asset) {
      if (asset.role !== "service") return;
      output[asset.package] = [asset.version, asset.hash, asset.integrity, asset.name].join("\u0000");
    });
    return output;
  }

  function beginComponentHandoff(graphSource, deliverySource) {
    if (!core.booted || !stagedGraph(core.graph) || !core.activeDelivery) {
      throw new Error("KitJS: component handoff requires an active staged runtime");
    }
    if (componentHandoff) throw new Error("KitJS: another component handoff is already active");
    var targetGraph = normalizeComponentGraph(graphSource);
    var targetDelivery = normalizeStagedDelivery(deliverySource, targetGraph);
    var currentDelivery = core.activeDelivery;
    if (!stagedGraph(targetGraph) || targetGraph.profile !== core.graph.profile ||
      targetDelivery.profile !== currentDelivery.profile ||
      targetDelivery.runtimeHash !== currentDelivery.runtimeHash ||
      targetDelivery.hydrateHash !== currentDelivery.hydrateHash ||
      !sameStringManifest(targetGraph.services, core.graph.services) ||
      !sameActionManifest(targetGraph.actions, core.graph.actions) ||
      !sameStringManifest(serviceAssetMap(targetDelivery), serviceAssetMap(currentDelivery))) {
      throw new Error("KitJS: component handoff requires the exact active runtime and services");
    }
    Object.keys(targetGraph.components).forEach(function (name) {
      if (localRegistry.has(name)) {
        throw new Error("KitJS: component handoff conflicts with client component \"" + name + "\"");
      }
      if (OWN.call(core.graph.components, name) &&
        (targetGraph.components[name] !== core.graph.components[name] ||
          targetGraph.componentHashes[name] !== core.graph.componentHashes[name] ||
          !sameStringManifest(targetGraph.grants[name], core.graph.grants[name]))) {
        throw new Error("KitJS: component handoff cannot replace overlapping component \"" + name + "\"");
      }
    });

    var pending = new Map();
    var missingEntries = [];
    Object.keys(targetGraph.components).forEach(function (name) {
      var version = targetGraph.components[name];
      var sourceHash = targetGraph.componentHashes[name];
      var key = componentCacheKey(name, version, sourceHash);
      var cached = componentCache.get(key);
      componentCache.forEach(function (entry) {
        if (entry.name === name && entry.version === version && entry.sourceHash !== sourceHash) {
          throw new Error("KitJS: component handoff found conflicting source bytes for \"" + name + "\"");
        }
      });
      if (cached) {
        assertDescriptorsForGraph(name, cached.descriptors, targetGraph);
      } else {
        missingEntries.push(Object.freeze({ name: name, version: version, sourceHash: sourceHash }));
      }
    });
    Object.freeze(missingEntries);
    if (componentCache.size + missingEntries.length > core.cacheLimit) {
      throw new Error("KitJS: component handoff exceeds the component cache limit");
    }
    var closed = false;
    var committed = false;
    var rollbackUsed = false;
    var tx;

    function assertOpen() {
      if (closed || componentHandoff !== tx) throw new Error("KitJS: component handoff is closed");
    }

    function abort() {
      if (closed) return false;
      closed = true;
      pending.clear();
      if (componentHandoff === tx) componentHandoff = null;
      return true;
    }

    function missing() {
      assertOpen();
      return missingEntries;
    }

    function register(name, version, sourceHash, installer) {
      assertOpen();
      try {
        if (!validComponentName(name) || !validVersion(version) ||
          typeof sourceHash !== "string" || !GRAPH_ID.test(sourceHash) ||
          typeof installer !== "function" || !Object.isFrozen(installer) ||
          targetGraph.components[name] !== version ||
          targetGraph.componentHashes[name] !== sourceHash) {
          throw new Error("KitJS: component handoff package does not match the target graph");
        }
        var key = componentCacheKey(name, version, sourceHash);
        if (componentCache.has(key) || pending.has(key)) {
          throw new Error("KitJS: component handoff package is duplicate or already cached");
        }
        pending.set(key, Object.freeze({
          name: name,
          version: version,
          sourceHash: sourceHash,
          installer: installer
        }));
        return true;
      } catch (error) {
        abort();
        throw error;
      }
    }

    function ready() {
      assertOpen();
      try {
        if (pending.size !== missingEntries.length || !missingEntries.every(function (identity) {
          return pending.has(componentCacheKey(identity.name, identity.version, identity.sourceHash));
        })) {
          throw new Error("KitJS: component handoff has missing or partial registration");
        }
        return true;
      } catch (error) {
        abort();
        throw error;
      }
    }

    function commit() {
      ready();
      Object.keys(targetGraph.components).forEach(function (name) {
        if (localRegistry.has(name)) {
          abort();
          throw new Error("KitJS: component handoff conflicts with client component \"" + name + "\"");
        }
      });
      var previousGraph = core.graph;
      var previousDelivery = core.activeDelivery;
      var previousRegistry = core.registry;
      var previousCompiled = core.compiled;
      var previousMetadata = metadata;
      var previousRetainValidity = retainValidity;
      var previousRetainStructureValidity = retainStructureValidity;
      var previousRetainReports = retainReports;
      var nextRegistry = new Map();
      var materialized = new Map();
      var added = [];
      try {
        missingEntries.forEach(function (identity) {
          var key = componentCacheKey(identity.name, identity.version, identity.sourceHash);
          var staged = pending.get(key);
          if (!staged) throw new Error("KitJS: component handoff package disappeared before commit");
          var registered = 0;
          var descriptors = null;
          function registrar(registeredName, definition) {
            registered++;
            if (registered !== 1 || registeredName !== identity.name) {
              throw new Error("KitJS: component handoff package registered an undeclared component");
            }
            descriptors = componentDescriptors(registeredName, definition, targetGraph);
          }
          if (componentRegistration || componentRegistrationGraph) {
            throw new Error("KitJS: component registration transaction is already active");
          }
          componentRegistration = registrar;
          componentRegistrationGraph = targetGraph;
          try {
            staged.installer(core.kit);
          } finally {
            componentRegistration = null;
            componentRegistrationGraph = null;
          }
          if (registered !== 1 || !descriptors) {
            throw new Error("KitJS: component handoff package must register exactly once");
          }
          materialized.set(key, Object.freeze({
            name: identity.name,
            version: identity.version,
            sourceHash: identity.sourceHash,
            descriptors: descriptors
          }));
        });
        Object.keys(targetGraph.components).forEach(function (name) {
          var key = componentCacheKey(name, targetGraph.components[name], targetGraph.componentHashes[name]);
          var entry = materialized.get(key) || componentCache.get(key);
          if (!entry) throw new Error("KitJS: component handoff cache is incomplete");
          assertDescriptorsForGraph(name, entry.descriptors, targetGraph);
          nextRegistry.set(name, entry.descriptors);
        });
        materialized.forEach(function (entry, key) {
          if (componentCache.has(key)) throw new Error("KitJS: component handoff cache changed before commit");
          componentCache.set(key, entry);
          added.push(Object.freeze({ key: key, entry: entry }));
        });
        core.registry = nextRegistry;
        core.graph = targetGraph;
        core.activeDelivery = targetDelivery;
        core.compiled = new Map();
        metadata = new WeakMap();
        retainValidity = new WeakMap();
        retainStructureValidity = new WeakMap();
        retainReports = new WeakMap();
        closed = true;
        committed = true;
        componentHandoff = null;
        pending.clear();
      } catch (error) {
        added.forEach(function (item) {
          if (componentCache.get(item.key) === item.entry) componentCache.delete(item.key);
        });
        core.registry = previousRegistry;
        core.graph = previousGraph;
        core.activeDelivery = previousDelivery;
        core.compiled = previousCompiled;
        metadata = previousMetadata;
        retainValidity = previousRetainValidity;
        retainStructureValidity = previousRetainStructureValidity;
        retainReports = previousRetainReports;
        abort();
        throw error;
      }

      function rollback() {
        if (rollbackUsed) return false;
        rollbackUsed = true;
        if (!committed || core.graph !== targetGraph || core.activeDelivery !== targetDelivery ||
          core.registry !== nextRegistry) {
          throw new Error("KitJS: component handoff can no longer be rolled back");
        }
        added.forEach(function (item) {
          if (componentCache.get(item.key) === item.entry) componentCache.delete(item.key);
        });
        core.registry = previousRegistry;
        core.graph = previousGraph;
        core.activeDelivery = previousDelivery;
        core.compiled = previousCompiled;
        metadata = previousMetadata;
        retainValidity = previousRetainValidity;
        retainStructureValidity = previousRetainStructureValidity;
        retainReports = previousRetainReports;
        return true;
      }
      return Object.freeze(rollback);
    }

    tx = Object.freeze({
      graph: targetGraph,
      delivery: targetDelivery,
      missing: Object.freeze(missing),
      register: Object.freeze(register),
      ready: Object.freeze(ready),
      commit: Object.freeze(commit),
      abort: Object.freeze(abort)
    });
    componentHandoff = tx;
    return tx;
  }

  function supportsAppLoader(version) {
    return version === "1.1.0";
  }

  function releaseAppLoaderLinks(current) {
    if (current.loaderSources) {
      current.loaderSources.forEach(function (source) {
        if (source.loaderDependents) source.loaderDependents.delete(current);
      });
      current.loaderSources.clear();
      current.loaderSources = null;
    }
    if (current.loaderDependents) {
      current.loaderDependents.forEach(function (dependent) {
        if (dependent.loaderSources) dependent.loaderSources.delete(current);
      });
      current.loaderDependents.clear();
      current.loaderDependents = null;
    }
  }

  function trackAppLoaderDependency(source, dependent) {
    if (!source || !dependent || source === dependent || source.disposed || dependent.disposed) return;
    if (!source.loaderDependents) source.loaderDependents = new Set();
    if (!dependent.loaderSources) dependent.loaderSources = new Set();
    source.loaderDependents.add(dependent);
    dependent.loaderSources.add(source);
  }

  function invalidateAppLoaderDependents(source) {
    if (!source.loaderDependents) return;
    source.loaderDependents.forEach(function (dependent) {
      if (!dependent || dependent.disposed || !connectedHere(dependent.host)) {
        source.loaderDependents.delete(dependent);
        if (dependent && dependent.loaderSources) dependent.loaderSources.delete(source);
        return;
      }
      core.invalidate(dependent);
    });
  }

  function createInstance(descriptors, host, request, seed) {
    var own = {};
    Object.keys(descriptors).forEach(function (name) {
      own[name] = Object.assign({}, descriptors[name]);
      if (name !== "init" && OWN.call(own[name], "value")) {
        own[name].value = copyValue(own[name].value, new WeakSet());
      }
    });
    if (seed !== undefined) Object.keys(seed).forEach(function (name) {
      if (request) {
        var seeded = own[name];
        if (!seeded) {
          throw new TypeError("KitJS: data-kit-scope field \"" + name +
            "\" is not declared by component \"" + request.name + "\"");
        }
        if (!OWN.call(seeded, "value") || typeof seeded.value === "function" || name === "init") {
          throw new TypeError("KitJS: data-kit-scope cannot seed non-data component field \"" + name + "\"");
        }
        if (!seeded.writable) {
          throw new TypeError("KitJS: data-kit-scope cannot seed read-only component field \"" + name + "\"");
        }
        seeded.value = copyValue(seed[name], new WeakSet(), true);
      } else {
        own[name] = {
          value: copyValue(seed[name], new WeakSet(), true),
          writable: true,
          enumerable: true,
          configurable: true
        };
      }
    });
    var init = own.init && own.init.value;
    if (own.init && (typeof init !== "function" || own.init.get || own.init.set)) {
      throw new TypeError("KitJS: init must be a method");
    }
    delete own.init;
    var target = Object.defineProperties(Object.create(null), own);
    var current = {
      host: host,
      scope: null,
      init: init,
      cleanups: [],
      afterRenders: [],
      context: null,
      ownsCleanup: false,
      initialized: false,
      rendered: false,
      disposed: false,
      structures: undefined,
      observations: null,
      captures: new WeakMap(),
      loaderSources: null,
      loaderDependents: null,
      componentIdentity: request ? Object.freeze({
        name: request.name,
        version: request.version,
        lane: request.lane,
        alias: host.hasAttribute("data-kit-as") ? aliasName(host) : null
      }) : null
    };
    var scope = new Proxy(target, {
      set: function (object, name, value, receiver) {
        if (core.blocked(String(name))) return false;
        var before = Reflect.get(object, name, receiver);
        var success = Reflect.set(object, name, value, receiver);
        if (success && !core.equal(before, Reflect.get(object, name, receiver))) {
          if (String(name) === "loader") invalidateAppLoaderDependents(current);
          core.invalidate(current);
        }
        return success;
      },
      deleteProperty: function (object, name) {
        if (core.blocked(String(name))) return false;
        var had = OWN.call(object, name);
        var success = Reflect.deleteProperty(object, name);
        if (success && had) {
          if (String(name) === "loader") invalidateAppLoaderDependents(current);
          core.invalidate(current);
        }
        return success;
      }
    });
    current.scope = scope;
    current.context = lifecycleContext(current);
    core.scopeRecords.set(scope, current);
    return current;
  }

  function nearest(element) {
    while (element) {
      if (element.nodeType === 1) {
        if (element.hasAttribute("data-kit-ignore")) return null;
        if (element.hasAttribute("data-kit-component") || element.hasAttribute("data-kit-scope")) return element;
      }
      element = element.parentElement;
    }
    return null;
  }
  function reportMissingLocal(element, name) {
    if (!element || missingLocalReports.has(element)) return;
    missingLocalReports.add(element);
    core.report(new ReferenceError("KitJS: client component \"" + name +
      "\" has no registered definition"));
  }
  function auditLocalComponents() {
    localAuditComplete = true;
    var definitions = core.graph ? localRegistry : core.registry;
    document.querySelectorAll("[data-kit-component]").forEach(function (element) {
      if (core.ignoredForRuntime(element)) return;
      var request = componentMetadata(element, false);
      if (request && request.lane === "client" && !definitions.has(request.name)) {
        reportMissingLocal(element, request.name);
      }
    });
  }

  function registryForRequest(request) {
    return request && request.lane === "client" && core.graph ? localRegistry : core.registry;
  }

  function hasComponentDefinition(request) {
    var registry = registryForRequest(request);
    return !!request && !!registry && registry.has(request.name);
  }
  function invalidRetainHost(element) {
    if (!element || !element.hasAttribute("data-kit-retain")) return false;
    if (!retainValidity.has(element)) {
      inspectRetains(connectedHere(element) ? document : element, true, false);
    }
    return retainValidity.get(element) === true;
  }
  function ensureComponent(element) {
    if (!element || core.ignoredForRuntime(element)) return null;
    var current = core.scopes.get(element);
    if (current) return current.failed ? null : current;
    if (invalidRetainHost(element)) {
      core.scopes.set(element, { host: element, failed: true, disposed: false });
      if (element.hasAttribute("data-kit-scope") && core.releaseScopeSeed) core.releaseScopeSeed(element);
      return null;
    }
    var hasScope = element.hasAttribute("data-kit-scope");
    var seed = core.scopeSeed(element, true);
    var request = componentMetadata(element, true);
    var invalidAlias = request === undefined && hasScope && element.hasAttribute("data-kit-as");
    if (invalidAlias) aliasName(element);
    if (request === null || hasScope && seed === null || request === undefined && !hasScope || invalidAlias) {
      core.scopes.set(element, { host: element, failed: true, disposed: false });
      if (hasScope && core.releaseScopeSeed) core.releaseScopeSeed(element);
      return null;
    }
    var descriptors = request ? registryForRequest(request).get(request.name) : Object.create(null);
    if (request && !descriptors) {
      if (request.lane === "client" && localAuditComplete) reportMissingLocal(element, request.name);
      return null;
    }
    try {
      current = createInstance(descriptors, element, request, seed);
      core.scopes.set(element, current);
      return current;
    } catch (error) {
      core.report(error);
      core.scopes.set(element, { host: element, failed: true, disposed: false });
      return null;
    } finally {
      if (hasScope && core.releaseScopeSeed) core.releaseScopeSeed(element);
    }
  }
  function scopeRecordFor(element) {
    var boundary = nearest(element);
    return boundary ? ensureComponent(boundary) : null;
  }
  function ownedElements(current, selector) {
    var output = [];
    var host = current && current.host;
    if (!host || current.disposed || !host.isConnected || core.ignoredForRuntime(host)) return output;
    if (host.matches(selector)) output.push(host);
    var walker = document.createTreeWalker(host, 1, {
      acceptNode: function (element) {
        if (element.hasAttribute("data-kit-ignore")) return 2;
        if (element.hasAttribute("data-kit-component") || element.hasAttribute("data-kit-scope")) return 2;
        return element.matches(selector) ? 1 : 3;
      }
    });
    var element;
    while ((element = walker.nextNode())) output.push(element);
    return output;
  }
  function initialize(current) {
    if (!current || current.initialized || current.disposed) return;
    current.initialized = true;
    if (!current.init) return;
    try {
      var initialized = current.init.call(current.scope, current.context);
      if (typeof initialized === "function") ownCleanup(current, initialized);
      else core.observe(initialized, current);
    }
    catch (error) { core.report(error); }
  }
  function componentElements(root) {
    var output = [];
    root = root && root.querySelectorAll ? root : document;
    if (root.nodeType === 1 && core.ignoredForRuntime(root)) return output;
    if (root.nodeType === 1 && root.matches(BOUNDARIES)) output.push(root);
    root.querySelectorAll(BOUNDARIES).forEach(function (element) {
      if (!core.ignoredForRuntime(element)) output.push(element);
    });
    return output;
  }
  function liveComponents(root) {
    var output = [];
    componentElements(root).forEach(function (element) {
      if (element.hasAttribute("data-kit-as")) aliasName(element);
      var current = ensureComponent(element);
      if (current) output.push(current);
    });
    return output;
  }
  function disposeComponent(element) {
    var current = core.scopes.get(element);
    if (!current || current.disposed) return;
    current.disposed = true;
    if (current.failed) {
      current.host = null;
      core.dirtyRecords.delete(current);
      if (core.renderPending) core.renderPending.delete(current);
      core.scopes.delete(element);
      aliases.delete(element);
      metadata.delete(element);
      retainValidity.delete(element);
      retainStructureValidity.delete(element);
      retainReports.delete(element);
      return;
    }
    var scope = current.scope;
    var cleanups = current.cleanups.slice().reverse();
    current.cleanups.length = 0;
    current.afterRenders.forEach(function (entry) { entry.active = false; });
    current.afterRenders.length = 0;
    if (current.ownsCleanup) {
      current.ownsCleanup = false;
      cleanupOwners--;
      stopCleanupObserver();
    }
    cleanups.forEach(function (entry) {
      if (!entry.active) return;
      entry.active = false;
      try { entry.callback.call(scope); }
      catch (error) { core.report(error); }
    });
    releaseAppLoaderLinks(current);
    if (current.observations) current.observations.clear();
    if (current.scope) core.scopeRecords.delete(current.scope);
    current.host = null;
    current.scope = null;
    current.init = null;
    current.context = null;
    current.cleanups = null;
    current.afterRenders = null;
    current.captures = null;
    current.componentIdentity = null;
    core.dirtyRecords.delete(current);
    if (core.renderPending) core.renderPending.delete(current);
    core.scopes.delete(element);
    aliases.delete(element);
  }
  function validAlias(name) {
    return /^\$[A-Za-z][A-Za-z0-9_]*$/.test(name) && !RESERVED_ALIASES[name];
  }
  function aliasName(element) {
    if (aliases.has(element)) return aliases.get(element);
    var name = (element.getAttribute("data-kit-as") || "").trim();
    if (!element.hasAttribute("data-kit-component") || !validAlias(name)) {
      core.report(new TypeError("KitJS: data-kit-as requires a component host and a valid $alias"));
      name = null;
    }
    aliases.set(element, name);
    return name;
  }
  function resolveAlias(name) {
    if (!validAlias(name)) throw new ReferenceError("KitJS: unknown component alias \"" + name + "\"");
    var matches = [];
    document.querySelectorAll(ALIASES).forEach(function (element) {
      if (core.ignoredForRuntime(element)) return;
      if (aliasName(element) === name) matches.push(element);
    });
    if (matches.length > 1) throw new TypeError("KitJS: duplicate component alias \"" + name + "\"");
    if (!matches.length) throw new ReferenceError("KitJS: unknown component alias \"" + name + "\"");
    var current = ensureComponent(matches[0]);
    if (!current || current.disposed || !current.host || !current.host.isConnected) {
      throw new ReferenceError("KitJS: unavailable component alias \"" + name + "\"");
    }
    initialize(current);
    return current;
  }

  function resolveAppService(alias, current, serviceName) {
    if (alias !== "$app" || !current || !current.componentIdentity ||
      current.componentIdentity.lane !== "managed" || current.componentIdentity.name !== "app" ||
      current.componentIdentity.alias !== "$app" ||
      !core.graph || !core.graph.grants || !core.serviceRegistry) return undefined;
    var granted = core.graph.grants.app;
    if (!granted || !OWN.call(granted, serviceName) ||
      granted[serviceName] !== core.graph.services[serviceName]) return undefined;
    return core.serviceRegistry.get(serviceName);
  }

  function resolveAppLoader(dependent, leaf) {
    if (leaf !== "visible" && leaf !== "value" || !dependent || dependent.disposed ||
      !connectedHere(dependent.host)) {
      throw new ReferenceError("KitJS: $app.loader is unavailable");
    }
    var current = resolveAlias("$app");
    var identity = current.componentIdentity;
    if (!identity || identity.lane !== "managed" || identity.name !== "app" || identity.alias !== "$app" ||
      !supportsAppLoader(identity.version) || !core.graph ||
      core.graph.components.app !== identity.version || !current.host.contains(dependent.host)) {
      throw new ReferenceError("KitJS: $app.loader requires the canonical app@1.1.0 ancestor");
    }
    var granted = core.graph.grants && core.graph.grants.app;
    if (granted && OWN.call(granted, "loader")) {
      throw new TypeError("KitJS: $app.loader cannot expose a granted service");
    }
    var loader = Reflect.get(current.scope, "loader", current.scope);
    if (!loader || typeof loader !== "object" || !Object.isFrozen(loader) || !OWN.call(loader, leaf) ||
      typeof core.serviceName === "function" && core.serviceName(loader)) {
      throw new TypeError("KitJS: invalid $app.loader snapshot");
    }
    var value = loader[leaf];
    var invalidValue = leaf === "visible" ? typeof value !== "boolean" :
      value !== null && (typeof value !== "number" || !Number.isFinite(value) || value < 0 || value > 100);
    if (invalidValue ||
      typeof core.serviceName === "function" && core.serviceName(value)) {
      throw new TypeError("KitJS: invalid $app.loader snapshot");
    }
    trackAppLoaderDependency(current, dependent);
    return value;
  }

  core.startHooks.push(function () {
    function scheduleAudit() {
      var schedule = document.defaultView && document.defaultView.setTimeout;
      if (typeof schedule === "function") schedule.call(document.defaultView, auditLocalComponents, 0);
      else enqueue(auditLocalComponents);
    }
    if (document.readyState === "loading") {
      document.addEventListener("DOMContentLoaded", scheduleAudit, { once: true });
    } else scheduleAudit();
  });

  core.activeDelivery = null;
  Object.defineProperty(core, "delivery", {
    get: function () { return core.activeDelivery; }
  });
  core.component = component;
  var kit = {};
  Object.defineProperties(kit, {
    version: { value: core.version, enumerable: true },
    component: { value: component, enumerable: true },
    [core.install]: { value: core.version }
  });
  core.kit = kit;
  core.sealKit = function () {
    if (OWN.call(kit, "service")) {
      throw new Error("KitJS: service registrar must be removed before publication");
    }
    if (!Object.isFrozen(kit)) Object.freeze(kit);
    return kit;
  };
  core.installComponentGraph = installComponentGraph;
  core.installStagedDelivery = installStagedDelivery;
  core.recordStagedComponentPackage = recordStagedComponentPackage;
  core.beginComponentHandoff = beginComponentHandoff;
  core.componentMetadata = componentMetadata;
  core.componentMetadataForGraph = componentMetadataForGraph;
  core.hasComponentDefinition = hasComponentDefinition;
  core.inspectRetains = function (root) { return inspectRetains(root, false, true); };
  core.invalidRetainStructure = function (element) {
    if (!retainStructureValidity.has(element)) {
      inspectRetains(connectedHere(element) ? document : element, true, false);
    }
    return retainStructureValidity.get(element) === true;
  };
  core.prepareComponentTree = prepareComponentTree;
  core.validateComponentTree = validateComponentTree;
  core.assertComponentGraph = assertComponentGraph;
  core.ensureComponent = ensureComponent;
  core.ownerFor = nearest;
  core.scopeRecordFor = scopeRecordFor;
  core.ownedElements = ownedElements;
  core.initialize = initialize;
  core.flushAfterRender = flushAfterRender;
  core.liveComponents = liveComponents;
  core.disposeComponent = disposeComponent;
  core.validAlias = validAlias;
  core.validServiceName = validServiceName;
  core.resolveAlias = resolveAlias;
  core.resolveAppLoader = resolveAppLoader;
  core.resolveAppService = resolveAppService;
  core.phase = "component";
})(document);
;(function (document) {
  "use strict";

  var core = document[Symbol.for("kitjs:assembly")];
  if (!core || core.phase !== "component") throw new Error("KitJS: directives loaded out of order");
  if (core.reuse) { core.phase = "directives"; return; }

  var EVENTS = Object.create(null);
  var MODIFIERS = Object.create(null);
  var RESERVED = Object.create(null);
  var OUTSIDE = Object.create(null);
  var EVENT_NAMES = (
    "click dblclick submit input change keydown keyup pointerdown pointerup focusin focusout"
  ).split(" ");

  EVENT_NAMES.forEach(function (name) { EVENTS[name] = true; });
  "self prevent stop once outside enter escape".split(" ").forEach(function (name) {
    MODIFIERS[name] = true;
  });
  "component scope version local as retain drive ignore text show bind class style model if for key".split(" ").forEach(function (name) {
    RESERVED[name] = true;
  });
  "click dblclick pointerdown pointerup focusin".split(" ").forEach(function (name) {
    OUTSIDE[name] = true;
  });

  function directiveError(message, name) {
    throw new SyntaxError("KitJS: " + message + " in attribute \"" + name + "\"");
  }

  function parseEventAttribute(name) {
    if (typeof name !== "string" || name.indexOf("data-kit-") !== 0) return null;
    var source = name.slice(9);
    var parts = source.split(":");
    var type = parts.shift();
    if (!EVENTS[type]) {
      if (RESERVED[type]) {
        if (parts.length) directiveError("directive does not accept modifiers", name);
        return null;
      }
      directiveError("unsupported directive", name);
    }

    var seen = Object.create(null);
    var descriptor = {
      name: name,
      type: type,
      self: false,
      prevent: false,
      stop: false,
      once: false,
      outside: false,
      key: "",
      delay: 0
    };

    parts.forEach(function (modifier) {
      if (!modifier) directiveError("empty event modifier", name);
      var canonical = modifier;
      var debounce = /^debounce\(([0-9]+)\)$/.exec(modifier);
      if (debounce) canonical = "debounce";
      else if (!MODIFIERS[modifier]) directiveError("unsupported event modifier \"" + modifier + "\"", name);
      if (seen[canonical]) directiveError("duplicate event modifier \"" + canonical + "\"", name);
      seen[canonical] = true;

      if (canonical === "debounce") {
        var delay = Number(debounce[1]);
        if (!Number.isInteger(delay) || delay < 1 || delay > 60000) {
          directiveError("debounce delay must be between 1 and 60000", name);
        }
        descriptor.delay = delay;
      } else if (canonical === "enter" || canonical === "escape") {
        if (type !== "keydown" && type !== "keyup") {
          directiveError("keyboard modifier requires keydown or keyup", name);
        }
        if (descriptor.key) directiveError("event cannot use both enter and escape", name);
        descriptor.key = canonical === "enter" ? "Enter" : "Escape";
      } else descriptor[canonical] = true;
    });

    if (descriptor.outside && !OUTSIDE[type]) {
      directiveError("outside is not supported for this event", name);
    }
    if (descriptor.outside && descriptor.self) {
      directiveError("outside and self cannot be combined", name);
    }
    return descriptor;
  }

  core.eventTypes = EVENT_NAMES;
  core.outsideEventTypes = OUTSIDE;
  core.parseEventAttribute = parseEventAttribute;
  core.prepareHooks = [];
  core.renderHooks = [];
  core.phase = "directives";
})(document);
; (function (global, document) {
  "use strict";

  var core = document[Symbol.for("kitjs:assembly")];
  if (!core || core.phase !== "directives") throw new Error("KitJS: DOM fragment loaded out of order");
  if (core.reuse) { core.phase = "dom"; return; }

  var OWN = core.OWN;
  var EMPTY = {};
  var EMPTY_SCOPE = Object.freeze(Object.create(null));
  var BINDINGS = "[data-kit-text],[data-kit-show],[data-kit-bind]";
  var SAFE_PROPERTIES = {
    value: "value",
    checked: "checked",
    selected: "selected",
    disabled: "disabled",
    hidden: "hidden",
    readonly: "readOnly",
    required: "required",
    multiple: "multiple",
    indeterminate: "indeterminate"
  };
  var BOOLEAN_PROPERTIES = {
    checked: true, selected: true, disabled: true, hidden: true,
    readonly: true, required: true, multiple: true, indeterminate: true
  };

  function elementRecord(element) {
    var record = core.records.get(element);
    if (record) return record;
    record = {
      programs: Object.create(null),
      events: Object.create(null),
      modules: Object.create(null),
      invalid: Object.create(null)
    };
    core.records.set(element, record);
    return record;
  }
  function safeProgram(element, name, mode) {
    var programs = elementRecord(element).programs;
    if (OWN.call(programs, name)) return programs[name];
    try {
      programs[name] = {
        read: core.compile(element.getAttribute(name), mode),
        last: EMPTY
      };
    } catch (error) {
      core.report(error);
      programs[name] = null;
    }
    return programs[name];
  }

  function splitTop(source, separators) {
    var output = [];
    var start = 0;
    var depth = 0;
    var quote = "";
    for (var index = 0; index < source.length; index++) {
      var character = source.charAt(index);
      if (quote) {
        if (character === "\\") index++;
        else if (character === quote) quote = "";
      } else if (character === "'" || character === '"') quote = character;
      else if (character === "(" || character === "[" || character === "{") depth++;
      else if (character === ")" || character === "]" || character === "}") depth--;
      else if (depth === 0 && separators.indexOf(character) >= 0) {
        output.push(source.slice(start, index));
        start = index + 1;
      }
    }
    output.push(source.slice(start));
    return output;
  }
  function bindEntries(source) {
    source = source.trim();
    if (source.charAt(0) === "{" && source.charAt(source.length - 1) === "}") {
      source = source.slice(1, -1);
    }
    var entries = [];
    splitTop(source, ",;").forEach(function (part) {
      if (!part.trim()) return;
      var pieces = splitTop(part, ":");
      if (pieces.length < 2) core.syntax("invalid bind entry", source, 0);
      var key = pieces.shift().trim();
      var quoted = /^(['"])([A-Za-z_][A-Za-z0-9_.:-]*)\1$/.exec(key);
      if (quoted) key = quoted[2];
      else if (!/^[A-Za-z_][A-Za-z0-9_.:-]*$/.test(key)) core.syntax("invalid bind name", source, 0);
      var lowerKey = key.toLowerCase();
      if (/^on/i.test(key) || /^data-kit-/i.test(key) ||
        ["srcdoc", "style", "innerhtml", "outerhtml", "insertadjacenthtml",
          "textcontent", "innertext", "outertext"].indexOf(lowerKey) >= 0) {
        core.syntax("unsafe bind name \"" + key + "\"", source, 0);
      }
      entries.push({
        name: key,
        read: core.compile(pieces.join(":").trim(), "binding"),
        last: EMPTY
      });
    });
    if (!entries.length) core.syntax("empty bind map", source, 0);
    return entries;
  }
  function safeBind(element) {
    var programs = elementRecord(element).programs;
    if (OWN.call(programs, "data-kit-bind")) return programs["data-kit-bind"];
    try { programs["data-kit-bind"] = bindEntries(element.getAttribute("data-kit-bind")); }
    catch (error) { core.report(error); programs["data-kit-bind"] = null; }
    return programs["data-kit-bind"];
  }

  function safeURL(name, value) {
    if (["href", "src", "action", "formaction", "poster", "xlink:href"].indexOf(name.toLowerCase()) < 0) {
      return true;
    }
    var text = String(value).replace(/[\u0000-\u0020]+/g, "").toLowerCase();
    return text.indexOf("javascript:") !== 0 && text.indexOf("vbscript:") !== 0 &&
      text.indexOf("data:text/html") !== 0;
  }
  function writeBound(element, name, value) {
    if (!safeURL(name, value)) throw new TypeError("KitJS: unsafe URL binding");
    var lowerName = name.toLowerCase();
    var property = SAFE_PROPERTIES[lowerName];
    if (property) {
      value = BOOLEAN_PROPERTIES[lowerName] ? !!value :
        value === null || value === undefined ? "" : value;
      if (!core.equal(element[property], value)) element[property] = value;
    } else if (value === null || value === undefined || value === false && name.indexOf("aria-") !== 0) {
      if (element.hasAttribute(name)) element.removeAttribute(name);
    } else {
      var text = value === true && name.indexOf("aria-") !== 0 ? "" : String(value);
      if (element.getAttribute(name) !== text) element.setAttribute(name, text);
    }
  }
  function asyncBinding(value) {
    if (!value || typeof value.then !== "function") return false;
    value.then(function () { }, core.report);
    core.report(new TypeError("KitJS: bindings must return synchronously"));
    return true;
  }

  function prepareBoundary(current) {
    if (!current || current.disposed || !current.host || !current.host.isConnected ||
      core.ignoredForRuntime(current.host)) return [];
    core.initialize(current);
    var structuresChanged = core.reconcileStructures && core.reconcileStructures(current);
    core.prepareHooks.forEach(function (prepare) { prepare(current); });
    if (!structuresChanged) return [];
    return core.liveComponents(current.host).filter(function (candidate) {
      return candidate !== current && !candidate.rendered;
    });
  }
  function renderElement(element) {
    if (core.ignoredForRuntime(element)) return;
    var current = core.scopeRecordFor(element);
    if (!current) return;
    var scope = current.scope;
    var program;
    if (element.hasAttribute("data-kit-text")) {
      program = safeProgram(element, "data-kit-text", "binding");
      if (program) {
        var value = program.read(scope, core.localsFor ? core.localsFor(element) : null);
        if (!asyncBinding(value)) {
          var text = value === null || value === undefined ? "" : String(value);
          if (!core.equal(program.last, text)) {
            program.last = text;
            if (element.textContent !== text) element.textContent = text;
          }
        }
      }
    }
    if (element.hasAttribute("data-kit-show")) {
      program = safeProgram(element, "data-kit-show", "binding");
      if (program) {
        var shown = program.read(scope, core.localsFor ? core.localsFor(element) : null);
        if (!asyncBinding(shown)) {
          var hidden = !shown;
          if (!core.equal(program.last, hidden)) {
            program.last = hidden;
            if (element.hidden !== hidden) element.hidden = hidden;
          }
        }
      }
    }
    if (element.hasAttribute("data-kit-bind")) {
      var entries = safeBind(element);
      if (entries) entries.forEach(function (entry) {
        var bound = entry.read(scope, core.localsFor ? core.localsFor(element) : null);
        if (asyncBinding(bound) || core.equal(entry.last, bound)) return;
        entry.last = bound;
        writeBound(element, entry.name, bound);
      });
    }
  }
  function render(records) {
    var initial = Array.isArray(records) ? records : core.liveComponents();
    if (Array.isArray(records)) {
      var order = new Map();
      var depths = new Map();
      initial.forEach(function (current, index) {
        order.set(current, index);
        var depth = 0;
        var ancestor = current.host && current.host.parentElement;
        while (ancestor) {
          if (ancestor.hasAttribute("data-kit-component") || ancestor.hasAttribute("data-kit-scope")) depth++;
          ancestor = ancestor.parentElement;
        }
        depths.set(current, depth);
      });
      initial.sort(function (left, right) {
        return depths.get(left) - depths.get(right) || order.get(left) - order.get(right);
      });
    }
    var queue = [];
    var pending = new Set();
    function enqueue(current) {
      if (!current || current.disposed || !current.host || !current.host.isConnected ||
        core.ignoredForRuntime(current.host) || pending.has(current)) return;
      pending.add(current);
      queue.push(current);
    }
    initial.forEach(enqueue);
    core.renderPending = pending;
    try {
      for (var index = 0; index < queue.length; index++) {
        var current = queue[index];
        pending.delete(current);
        if (current.disposed || !current.host || !current.host.isConnected) continue;
        var children;
        try { children = prepareBoundary(current); }
        catch (error) { core.report(error); children = []; }
        core.ownedElements(current, BINDINGS).forEach(function (element) {
          try { renderElement(element); } catch (error) { core.report(error); }
        });
        core.renderHooks.forEach(function (renderHook) {
          try { renderHook(current); } catch (error) { core.report(error); }
        });
        current.rendered = true;
        core.flushAfterRender(current);
        children.forEach(enqueue);
      }
    } finally {
      core.renderPending = null;
    }
  }
  function executeAttribute(element, name, locals) {
    if (core.ignoredForRuntime(element)) return false;
    var boundary = core.ownerFor(element);
    var current = core.scopeRecordFor(element);
    if (boundary && !current) return false;
    var program = safeProgram(element, name, "action");
    if (!program) return false;
    if (current) core.initialize(current);
    try {
      if (core.localsFor) locals = core.localsFor(element, locals);
      program.read(current ? current.scope : EMPTY_SCOPE, locals, function (value, owner) {
        core.observe(value, owner);
      });
      return true;
    } catch (error) {
      core.report(error);
      return false;
    }
  }

  core.BINDINGS = BINDINGS;
  core.elementRecord = elementRecord;
  core.safeProgram = safeProgram;
  core.asyncBinding = asyncBinding;
  core.executeAttribute = executeAttribute;
  core.render = render;
  core.phase = "dom";
})(globalThis, document);
;(function (document) {
  "use strict";

  var core = document[Symbol.for("kitjs:assembly")];
  if (!core || core.phase !== "dom") throw new Error("KitJS: structure loaded out of order");
  if (core.reuse) { core.phase = "structure"; return; }

  var OWN = core.OWN;
  var SELECTOR = "[data-kit-if],[data-kit-for],[data-kit-key]";
  var MAX_DEPTH = 64;
  var UNSET = {};
  var contexts = new WeakMap();
  var levels = new WeakMap();

  function copyOwn(target, source) {
    if (!source) return target;
    Object.keys(source).forEach(function (name) { target[name] = source[name]; });
    return target;
  }

  function replaceOwn(target, source) {
    source = source || Object.create(null);
    var targetKeys = Object.keys(target);
    var sourceKeys = Object.keys(source);
    var changed = targetKeys.length !== sourceKeys.length || sourceKeys.some(function (name) {
      return !OWN.call(target, name) || !core.equal(target[name], source[name]);
    });
    Object.keys(target).forEach(function (name) { delete target[name]; });
    copyOwn(target, source);
    return changed;
  }

  function localsFor(element, extra) {
    var chain = [];
    var node = element;
    while (node && node !== document) {
      var local = contexts.get(node);
      if (local) chain.push(local);
      node = node.parentElement;
    }
    if (!chain.length) return extra || null;
    if (chain.length === 1 && !extra) return chain[0];
    var output = Object.create(null);
    for (var index = chain.length - 1; index >= 0; index--) copyOwn(output, chain[index]);
    return copyOwn(output, extra);
  }

  function validLocal(name) {
    return /^[A-Za-z_][A-Za-z0-9_]*$/.test(name) &&
      !core.blocked(name) && !core.FORBIDDEN[name];
  }

  function parseFor(source) {
    var match = /^\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*(?:,\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*)?\s+of\s+([\s\S]+?)\s*$/.exec(source || "");
    if (!match || !validLocal(match[1]) || match[2] && !validLocal(match[2]) ||
        match[2] && match[1] === match[2]) {
      throw new SyntaxError("KitJS: invalid for specification");
    }
    return { item: match[1], index: match[2] || "", source: match[3] };
  }

  function fail(state, error) {
    var message = String(error && error.message || error);
    if (state.error !== message) {
      state.error = message;
      core.report(error);
    }
    return false;
  }

  function clearFailure(state) { state.error = ""; }

  function structureState(element) {
    if (core.ignoredForRuntime(element)) return null;
    var modules = core.elementRecord(element).modules;
    if (OWN.call(modules, "structure")) return modules.structure;
    if (core.invalidRetainStructure && core.invalidRetainStructure(element)) {
      modules.structure = null;
      return null;
    }
    try {
      if (element.tagName !== "TEMPLATE") {
        throw new TypeError("KitJS: if, for, and key require a template element");
      }
      var hasIf = element.hasAttribute("data-kit-if");
      var hasFor = element.hasAttribute("data-kit-for");
      var hasKey = element.hasAttribute("data-kit-key");
      if (hasIf && hasFor) throw new TypeError("KitJS: one template cannot combine if and for");
      if (hasKey && !hasFor) throw new TypeError("KitJS: key requires for on the same template");
      if (!hasIf && !hasFor) throw new TypeError("KitJS: orphan structural template");
      if (element.content.querySelector("script")) {
        throw new TypeError("KitJS: structural templates cannot contain script elements");
      }
      if (hasIf) {
        var condition = core.safeProgram(element, "data-kit-if", "binding");
        if (!condition) throw new SyntaxError("KitJS: invalid if expression");
        modules.structure = {
          kind: "if",
          condition: condition,
          branch: null,
          error: ""
        };
      } else {
        var spec = parseFor(element.getAttribute("data-kit-for"));
        modules.structure = {
          kind: "for",
          item: spec.item,
          index: spec.index,
          list: core.compile(spec.source, "binding"),
          key: hasKey ? core.compile(element.getAttribute("data-kit-key"), "binding") : null,
          keyInvalid: false,
          lastList: UNSET,
          rows: new Map(),
          order: [],
          error: ""
        };
      }
    } catch (error) {
      core.report(error);
      modules.structure = null;
    }
    return modules.structure;
  }

  function rangeNodes(range) {
    if (range.nodes) return range.nodes.slice();
    var output = [];
    var node = range.start;
    while (node) {
      output.push(node);
      if (node === range.end) break;
      node = node.nextSibling;
    }
    return output;
  }

  function bindRange(range) {
    rangeNodes(range).forEach(function (node) {
      if (node.nodeType !== 1) return;
      contexts.set(node, range.locals);
      levels.set(node, range.level);
    });
  }

  function createRange(template, locals) {
    var fragment = template.content.cloneNode(true);
    var start = document.createComment("kit-structure-start");
    var end = document.createComment("kit-structure-end");
    var nodes = [start].concat(Array.prototype.slice.call(fragment.childNodes), [end]);
    var range = {
      start: start,
      end: end,
      nodes: nodes,
      locals: locals,
      level: contextLevel(template) + 1
    };
    bindRange(range);
    return range;
  }

  function insertRange(range, before, fresh) {
    var nodes = rangeNodes(range);
    var parent = before.parentNode;
    nodes.forEach(function (node) { parent.insertBefore(node, before); });
    range.nodes = null;
    if (fresh && core.prepareEventTree) {
      nodes.forEach(function (node) {
        if (node.nodeType === 1) core.prepareEventTree(node);
      });
    }
  }

  function disposeElement(element) {
    if (core.disposeElementEvents) core.disposeElementEvents(element);
    if (core.disposeComponent) core.disposeComponent(element);
    core.records.delete(element);
    core.scopes.delete(element);
    contexts.delete(element);
    levels.delete(element);
  }

  function disposeTree(root) {
    if (!root || root.nodeType !== 1) return;
    var descendants = Array.prototype.slice.call(root.querySelectorAll("*")).reverse();
    descendants.forEach(disposeElement);
    disposeElement(root);
  }

  function removeRange(range) {
    var nodes = rangeNodes(range);
    nodes.forEach(function (node) { if (node.nodeType === 1) disposeTree(node); });
    nodes.forEach(function (node) { if (node.parentNode) node.parentNode.removeChild(node); });
    range.nodes = [];
  }

  function makeLocals(outer, itemName, item, indexName, index) {
    var output = Object.create(null);
    copyOwn(output, outer);
    output[itemName] = item;
    if (indexName) output[indexName] = index;
    return output;
  }

  function dirtyRangeComponents(range, owner) {
    rangeNodes(range).forEach(function (node) {
      if (node.nodeType !== 1) return;
      core.liveComponents(node).forEach(function (current) {
        if (current !== owner) core.invalidate(current);
      });
    });
  }

  function keyValue(value) {
    if (typeof value === "string") return value;
    if (typeof value === "number" && Number.isFinite(value)) return value;
    throw new TypeError("KitJS: key must return a string or finite number");
  }

  function processIf(template, state) {
    var current = core.scopeRecordFor(template);
    if (!current) return false;
    core.initialize(current);
    var outer = localsFor(template);
    var visible;
    try {
      visible = state.condition.read(current.scope, outer);
      if (core.asyncBinding(visible)) return false;
      visible = !!visible;
      clearFailure(state);
    } catch (error) { return fail(state, error); }

    if (!visible) {
      if (!state.branch) return false;
      removeRange(state.branch);
      state.branch = null;
      return true;
    }
    if (state.branch) {
      if (replaceOwn(state.branch.locals, outer)) dirtyRangeComponents(state.branch, current);
      return false;
    }
    var locals = copyOwn(Object.create(null), outer);
    state.branch = createRange(template, locals);
    insertRange(state.branch, template, true);
    return true;
  }

  function sameOrder(left, right) {
    if (left.length !== right.length) return false;
    for (var index = 0; index < left.length; index++) {
      if (left[index] !== right[index]) return false;
    }
    return true;
  }

  function contextLevel(element) {
    var node = element;
    while (node && node !== document) {
      if (levels.has(node)) return levels.get(node);
      node = node.parentElement;
    }
    return 0;
  }

  function elementDepth(element) {
    var depth = 0;
    while (element && element !== document) {
      depth++;
      element = element.parentElement;
    }
    return depth;
  }

  function processFor(template, state) {
    if (state.keyInvalid) return false;
    var current = core.scopeRecordFor(template);
    if (!current) return false;
    core.initialize(current);
    var outer = localsFor(template);
    var items;
    var plan = [];
    var keys = [];
    var seen = new Map();
    var listChanged = false;
    try {
      items = state.list(current.scope, outer);
      if (core.asyncBinding(items)) return false;
      if (!Array.isArray(items)) throw new TypeError("KitJS: for expression must return an array");
      for (var index = 0; index < items.length; index++) {
        var locals = makeLocals(outer, state.item, items[index], state.index, index);
        var key = index;
        if (state.key) {
          var rawKey = state.key(current.scope, locals);
          if (core.asyncBinding(rawKey)) {
            state.keyInvalid = true;
            return false;
          }
          key = keyValue(rawKey);
        }
        if (seen.has(key)) throw new TypeError("KitJS: duplicate for key \"" + key + "\"");
        seen.set(key, true);
        keys.push(key);
        plan.push({ key: key, locals: locals, row: state.rows.get(key) || null, fresh: false });
      }
      for (var createIndex = 0; createIndex < plan.length; createIndex++) {
        if (!plan[createIndex].row) {
          plan[createIndex].row = createRange(template, plan[createIndex].locals);
          plan[createIndex].fresh = true;
        }
      }
      listChanged = !core.equal(state.lastList, items);
      clearFailure(state);
    } catch (error) { return fail(state, error); }

    var nextRows = new Map();
    plan.forEach(function (entry) {
      if (!entry.fresh) {
        var localsChanged = replaceOwn(entry.row.locals, entry.locals);
        if (listChanged || localsChanged) dirtyRangeComponents(entry.row, current);
      }
      nextRows.set(entry.key, entry.row);
    });

    var removed = false;
    state.rows.forEach(function (row, key) {
      if (nextRows.has(key)) return;
      removeRange(row);
      removed = true;
    });

    var orderChanged = !sameOrder(state.order, keys);
    if (orderChanged) {
      var before = template;
      for (var moveIndex = plan.length - 1; moveIndex >= 0; moveIndex--) {
        var entry = plan[moveIndex];
        if (!entry.row.start.parentNode || entry.row.end.nextSibling !== before) {
          insertRange(entry.row, before, entry.fresh);
        }
        before = entry.row.start;
      }
    }
    state.rows = nextRows;
    state.order = keys;
    state.lastList = items;
    return removed || orderChanged;
  }

  function reconcile(current) {
    if (current.structures === false) return false;
    var changedAny = false;
    for (var pass = 0; pass < 64; pass++) {
      var changed = false;
      var elements = core.ownedElements(current, SELECTOR);
      if (current.structures === undefined) current.structures = elements.length > 0;
      if (!current.structures) return false;
      elements.sort(function (left, right) {
        return contextLevel(left) - contextLevel(right) || elementDepth(left) - elementDepth(right);
      });
      elements.forEach(function (element) {
        if (!element.isConnected) return;
        var state = structureState(element);
        if (!state) return;
        if (contextLevel(element) >= MAX_DEPTH) {
          fail(state, new RangeError("KitJS: structural nesting exceeds " + MAX_DEPTH + " levels"));
          return;
        }
        if (state.kind === "if" ? processIf(element, state) : processFor(element, state)) changed = true;
      });
      if (!changed) return changedAny;
      changedAny = true;
    }
    core.report(new RangeError("KitJS: structural reconciliation exceeds " + MAX_DEPTH + " passes"));
    return changedAny;
  }

  function resetStructures(root) {
    if (!root || root.nodeType !== 1) return;
    var templates = [];
    if (root.matches && root.matches(SELECTOR)) templates.push(root);
    Array.prototype.push.apply(templates, root.querySelectorAll(SELECTOR));
    templates.reverse().forEach(function (template) {
      if (core.ignoredForRuntime(template)) return;
      var record = core.records.get(template);
      var state = record && record.modules.structure;
      if (!state) return;
      if (state.kind === "if") {
        if (state.branch) removeRange(state.branch);
        state.branch = null;
      } else {
        state.rows.forEach(removeRange);
        state.rows.clear();
        state.order = [];
        state.lastList = UNSET;
      }
    });
    core.liveComponents(root).forEach(function (current) {
      current.structures = undefined;
    });
  }

  core.localsFor = localsFor;
  core.disposeTree = disposeTree;
  core.resetStructures = resetStructures;
  core.reconcileStructures = reconcile;
  core.phase = "structure";
})(document);
;(function (document) {
  "use strict";

  var core = document[Symbol.for("kitjs:assembly")];
  if (!core || core.phase !== "structure") throw new Error("KitJS: class loaded out of order");
  if (core.reuse) { core.phase = "class"; return; }

  var SELECTOR = "[data-kit-class]";

  function addTokens(output, source) {
    String(source).trim().split(/\s+/).forEach(function (token) {
      if (token) output[token] = true;
    });
  }

  function classTokens(value) {
    var output = Object.create(null);
    if (value === null || value === undefined || value === false) return output;
    if (typeof value === "string") {
      addTokens(output, value);
      return output;
    }
    if (typeof value !== "object" || Array.isArray(value)) {
      throw new TypeError("KitJS: class binding must return a string or object");
    }
    Object.keys(value).forEach(function (name) {
      if (value[name]) addTokens(output, name);
    });
    return output;
  }

  function classState(element) {
    var modules = core.elementRecord(element).modules;
    if (modules.classes) return modules.classes;
    var fixed = Object.create(null);
    element.classList.forEach(function (name) { fixed[name] = true; });
    modules.classes = { fixed: fixed, owned: Object.create(null) };
    return modules.classes;
  }

  function render(current) {
    core.ownedElements(current, SELECTOR).forEach(function (element) {
      try {
        var program = core.safeProgram(element, "data-kit-class", "binding");
        if (!program) return;
        var value = program.read(current.scope, core.localsFor ? core.localsFor(element) : null);
        if (core.asyncBinding(value)) return;
        var next = classTokens(value);
        var state = classState(element);
        Object.keys(state.owned).forEach(function (name) {
          if (next[name]) return;
          element.classList.remove(name);
          delete state.owned[name];
        });
        Object.keys(next).forEach(function (name) {
          if (state.fixed[name]) return;
          if (state.owned[name]) return;
          if (!element.classList.contains(name)) {
            element.classList.add(name);
            state.owned[name] = true;
          }
        });
      } catch (error) { core.report(error); }
    });
  }

  core.renderHooks.push(render);
  core.phase = "class";
})(document);
;(function (document) {
  "use strict";

  var core = document[Symbol.for("kitjs:assembly")];
  if (!core || core.phase !== "class") throw new Error("KitJS: style loaded out of order");
  if (core.reuse) { core.phase = "style"; return; }

  var OWN = core.OWN;
  var SELECTOR = "[data-kit-style]";
  var SOURCE_LIMIT = 16384;
  var ENTRY_LIMIT = 128;
  var UNSET = {};
  var RESET = {};
  var BLOCKED_NAMES = Object.create(null);
  "css-text csstext behavior -moz-binding".split(" ").forEach(function (name) {
    BLOCKED_NAMES[name] = true;
  });
  var SHORTHANDS = Object.create(null);
  (
    "-webkit-animation -webkit-border-after -webkit-border-before -webkit-border-end " +
    "-webkit-border-radius -webkit-border-start -webkit-column-rule -webkit-columns " +
    "-webkit-flex -webkit-flex-flow -webkit-mask -webkit-mask-box-image " +
    "-webkit-mask-position -webkit-text-emphasis -webkit-text-stroke -webkit-transition " +
    "all animation animation-range background background-position border border-block " +
    "border-block-color border-block-end border-block-start border-block-style border-block-width " +
    "border-bottom border-color border-image border-inline border-inline-color border-inline-end " +
    "border-inline-start border-inline-style border-inline-width border-left border-radius " +
    "border-right border-spacing border-style border-top border-width column-rule " +
    "column-rule-inset column-rule-inset-cap column-rule-inset-end column-rule-inset-junction " +
    "column-rule-inset-start columns contain-intrinsic-size container corner-block-end-shape " +
    "corner-block-start-shape corner-bottom-shape corner-inline-end-shape " +
    "corner-inline-start-shape corner-left-shape corner-right-shape corner-shape " +
    "corner-top-shape flex flex-flow font font-synthesis font-variant gap grid grid-area " +
    "grid-column grid-gap grid-row grid-template inset inset-block inset-inline interest-delay " +
    "list-style margin margin-block margin-inline marker mask mask-position offset outline " +
    "overflow overscroll-behavior padding padding-block padding-inline place-content place-items " +
    "place-self position-try row-rule row-rule-inset row-rule-inset-cap row-rule-inset-end " +
    "row-rule-inset-junction row-rule-inset-start rule rule-break rule-color rule-inset " +
    "rule-inset-cap rule-inset-end rule-inset-junction rule-inset-start rule-style " +
    "rule-visibility-items rule-width scroll-margin scroll-margin-block scroll-margin-inline " +
    "scroll-padding scroll-padding-block scroll-padding-inline scroll-timeline text-box " +
    "text-decoration text-emphasis text-wrap timeline-trigger timeline-trigger-activation-range " +
    "timeline-trigger-active-range transition view-timeline white-space"
  ).split(" ").forEach(function (name) { SHORTHANDS[name] = true; });

  function styleSyntax(message, source, position) {
    core.syntax(message, source, position < 0 ? 0 : position);
  }

  function propertyName(source, raw, position, seen) {
    var name = raw.trim();
    if (!name) styleSyntax("empty style property name", source, position);

    var custom = name.indexOf("--") === 0;
    if (custom) {
      if (!/^--[A-Za-z_][A-Za-z0-9_-]*$/.test(name)) {
        styleSyntax("invalid style property name \"" + name + "\"", source, position);
      }
      if (/^--(?:kit|kitwork)-/i.test(name)) {
        styleSyntax("unsafe style property name \"" + name + "\"", source, position);
      }
    } else {
      if (!/^-?[a-z][a-z0-9]*(?:-[a-z0-9]+)*$/.test(name)) {
        styleSyntax("invalid style property name \"" + name + "\"", source, position);
      }
      if (BLOCKED_NAMES[name]) {
        styleSyntax("unsafe style property name \"" + name + "\"", source, position);
      }
      if (SHORTHANDS[name]) {
        styleSyntax("shorthand style property \"" + name + "\" is not supported", source, position);
      }
    }

    var duplicateKey = custom ? name : name.toLowerCase();
    if (seen[duplicateKey]) {
      styleSyntax("duplicate style property \"" + name + "\"", source, position);
    }
    seen[duplicateKey] = true;
    return name;
  }

  function styleEntries(rawSource) {
    var source = rawSource === null ? "" : String(rawSource);
    if (source.length > SOURCE_LIMIT) {
      styleSyntax("style source exceeds 16384 UTF-16 code units", source, SOURCE_LIMIT);
    }

    var trimmed = source.trim();
    if (!trimmed) styleSyntax("empty style map", source, 0);
    if (trimmed.charAt(0) === "{") {
      styleSyntax("style map cannot use outer braces", source, source.indexOf("{"));
    }

    var parts = [];
    var stack = [];
    var quote = "";
    var partStart = 0;
    var colon = -1;

    function finish(end) {
      parts.push({ start: partStart, end: end, colon: colon });
    }

    for (var index = 0; index < trimmed.length; index++) {
      var character = trimmed.charAt(index);
      if (quote) {
        if (character === "\\") index++;
        else if (character === quote) quote = "";
        continue;
      }
      if (character === "'" || character === '"') {
        quote = character;
        continue;
      }
      if (character === "(" || character === "[" || character === "{") {
        stack.push(character);
        continue;
      }
      if (character === ")" || character === "]" || character === "}") {
        var expected = character === ")" ? "(" : character === "]" ? "[" : "{";
        if (stack.pop() !== expected) styleSyntax("unbalanced style map", source, index);
        continue;
      }
      if (!stack.length && character === ":" && colon < partStart) {
        colon = index;
        continue;
      }
      if (!stack.length && character === ";") {
        finish(index);
        partStart = index + 1;
        colon = -1;
      }
    }

    if (quote) styleSyntax("unterminated string in style map", source, trimmed.length);
    if (stack.length) styleSyntax("unbalanced style map", source, trimmed.length);
    if (partStart < trimmed.length) finish(trimmed.length);
    if (!parts.length) styleSyntax("empty style map", source, 0);
    if (parts.length > ENTRY_LIMIT) styleSyntax("style map exceeds 128 entries", source, 0);

    var entries = [];
    var seen = Object.create(null);
    parts.forEach(function (part) {
      var rawPart = trimmed.slice(part.start, part.end);
      if (!rawPart.trim()) styleSyntax("empty style entry", source, part.start);
      if (part.colon < part.start) styleSyntax("invalid style entry", source, part.start);
      var name = propertyName(source, trimmed.slice(part.start, part.colon), part.start, seen);
      var expression = trimmed.slice(part.colon + 1, part.end).trim();
      if (!expression) {
        styleSyntax("empty style expression for \"" + name + "\"", source, part.colon + 1);
      }
      entries.push({ name: name, read: core.compile(expression, "binding"), last: UNSET });
    });
    return entries;
  }

  function safeStyle(element) {
    var programs = core.elementRecord(element).programs;
    if (OWN.call(programs, "data-kit-style")) return programs["data-kit-style"];
    try { programs["data-kit-style"] = styleEntries(element.getAttribute("data-kit-style")); }
    catch (error) { core.report(error); programs["data-kit-style"] = null; }
    return programs["data-kit-style"];
  }

  function unsafeValue(text) {
    if (/[\u0000-\u001f\u007f-\u009f]/.test(text) || /[;{}\\@]/.test(text) ||
      text.indexOf("/*") >= 0 || text.indexOf("*/") >= 0 || /!\s*important\b/i.test(text) ||
      /(^|[^A-Za-z0-9_-])(url|image-set|-webkit-image-set|src|expression|var|attr)\s*\(/i.test(text)) {
      return true;
    }
    var compact = text.replace(/\s+/g, "").toLowerCase();
    return compact.indexOf("javascript:") >= 0 || compact.indexOf("vbscript:") >= 0 ||
      compact.indexOf("data:text/html") >= 0;
  }

  function styleValue(name, value) {
    if (value === null || value === undefined || value === false || value === "") return RESET;
    var text;
    if (typeof value === "number") {
      if (!Number.isFinite(value)) {
        throw new TypeError("KitJS: invalid style value for \"" + name + "\"");
      }
      text = String(value);
    } else if (typeof value === "string") text = value;
    else throw new TypeError("KitJS: invalid style value for \"" + name + "\"");
    if (unsafeValue(text)) throw new TypeError("KitJS: unsafe style value for \"" + name + "\"");
    return text;
  }

  function styleState(element, entries) {
    var modules = core.elementRecord(element).modules;
    if (OWN.call(modules, "style")) return modules.style;
    var baseline = Object.create(null);
    entries.forEach(function (entry) {
      baseline[entry.name] = {
        value: element.style.getPropertyValue(entry.name),
        priority: element.style.getPropertyPriority(entry.name)
      };
    });
    modules.style = { baseline: baseline };
    return modules.style;
  }

  function writeStyle(element, state, name, value) {
    if (value !== RESET) {
      element.style.setProperty(name, value, "");
      return;
    }
    var baseline = state.baseline[name];
    if (baseline.value !== "") element.style.setProperty(name, baseline.value, baseline.priority);
    else element.style.removeProperty(name);
  }

  function render(current) {
    core.ownedElements(current, SELECTOR).forEach(function (element) {
      try {
        var entries = safeStyle(element);
        if (!entries) return;
        var locals = core.localsFor ? core.localsFor(element) : null;
        var next = [];
        for (var index = 0; index < entries.length; index++) {
          var value = entries[index].read(current.scope, locals);
          if (core.asyncBinding(value)) return;
          next.push(styleValue(entries[index].name, value));
        }

        var state = styleState(element, entries);
        entries.forEach(function (entry, entryIndex) {
          var value = next[entryIndex];
          if (entry.last === value) return;
          writeStyle(element, state, entry.name, value);
          entry.last = value;
        });
      } catch (error) { core.report(error); }
    });
  }

  core.renderHooks.push(render);
  core.phase = "style";
})(document);
;(function (document) {
  "use strict";

  var core = document[Symbol.for("kitjs:assembly")];
  if (!core || core.phase !== "style") throw new Error("KitJS: model loaded out of order");
  if (core.reuse) { core.phase = "model"; return; }

  var OWN = core.OWN;
  var SELECTOR = "[data-kit-model]";
  var composing = new WeakSet();

  function controlKind(element) {
    var tag = element.tagName && element.tagName.toLowerCase();
    if (tag === "textarea") return "text";
    if (tag === "select") return element.multiple ? "select-multiple" : "select";
    if (tag !== "input") return "";
    var type = (element.type || "text").toLowerCase();
    if (type === "checkbox" || type === "radio" || type === "number" || type === "range") return type;
    if (["button", "submit", "reset", "image", "file", "hidden"].indexOf(type) >= 0) return "";
    return "text";
  }

  function modelState(element) {
    var modules = core.elementRecord(element).modules;
    if (OWN.call(modules, "model")) return modules.model;
    var source = (element.getAttribute("data-kit-model") || "").trim();
    if (source.charAt(0) === "$") {
      core.report(new SyntaxError("KitJS: model cannot use the reserved $ namespace"));
      modules.model = null;
      return null;
    }
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(source) || core.blocked(source)) {
      core.report(new SyntaxError("KitJS: model must name one component field"));
      modules.model = null;
      return null;
    }
    if (!controlKind(element)) {
      core.report(new TypeError("KitJS: model requires a supported form control"));
      modules.model = null;
      return null;
    }
    modules.model = { name: source, failed: false };
    return modules.model;
  }

  function writable(scope, state) {
    if (state.failed) return null;
    var descriptor = Object.getOwnPropertyDescriptor(scope, state.name);
    if (!descriptor || !OWN.call(descriptor, "value") || !descriptor.writable) {
      state.failed = true;
      core.report(new TypeError("KitJS: model field \"" + state.name + "\" is not writable"));
      return null;
    }
    return descriptor;
  }

  function setValue(element, kind, value) {
    if (kind === "checkbox") {
      var checked = Array.isArray(value) ? value.some(function (item) {
        return String(item) === element.value;
      }) : !!value;
      if (element.checked !== checked) element.checked = checked;
      return;
    }
    if (kind === "radio") {
      var selected = value !== null && value !== undefined && String(value) === element.value;
      if (element.checked !== selected) element.checked = selected;
      return;
    }
    if (kind === "select-multiple") {
      var selectedValues = Array.isArray(value) ? value.map(String) : [];
      Array.prototype.forEach.call(element.options, function (option) {
        var selected = selectedValues.indexOf(option.value) >= 0;
        if (option.selected !== selected) option.selected = selected;
      });
      return;
    }
    var text = value === null || value === undefined ? "" : String(value);
    if (element.value !== text) element.value = text;
  }

  function eventValue(element, kind, current) {
    if (kind === "checkbox") {
      if (!Array.isArray(current)) return !!element.checked;
      var next = current.slice();
      var found = -1;
      next.some(function (item, index) {
        if (String(item) !== element.value) return false;
        found = index;
        return true;
      });
      if (element.checked && found < 0) next.push(element.value);
      else if (!element.checked && found >= 0) next.splice(found, 1);
      return next;
    }
    if (kind === "radio") return element.checked ? element.value : current;
    if (kind === "select-multiple") {
      return Array.prototype.filter.call(element.options, function (option) {
        return option.selected;
      }).map(function (option) { return option.value; });
    }
    if (kind === "number" || kind === "range") {
      if (element.value === "") return null;
      var number = Number(element.value);
      return Number.isFinite(number) ? number : null;
    }
    return element.value;
  }

  function expectedEvent(kind) {
    return kind === "checkbox" || kind === "radio" || kind === "select" || kind === "select-multiple" ?
      "change" : "input";
  }

  function update(element, eventType, force) {
    if (!element || !element.hasAttribute || !element.hasAttribute("data-kit-model")) return false;
    if (core.ignoredForRuntime(element)) return false;
    var state = modelState(element);
    if (!state || state.failed) return false;
    var kind = controlKind(element);
    if (!force && expectedEvent(kind) !== eventType) return false;
    if (kind === "text" && composing.has(element)) return false;
    var current = core.scopeRecordFor(element);
    if (!current || !writable(current.scope, state)) return false;
    core.initialize(current);
    var before = Reflect.get(current.scope, state.name, current.scope);
    var value = eventValue(element, kind, before);
    if (kind === "radio" && !element.checked) return false;
    if (!Reflect.set(current.scope, state.name, value, current.scope)) {
      core.report(new TypeError("KitJS: model field \"" + state.name + "\" rejected a write"));
      return false;
    }
    return true;
  }

  function render(current) {
    core.ownedElements(current, SELECTOR).forEach(function (element) {
      try {
        var state = modelState(element);
        if (!state || !writable(current.scope, state)) return;
        setValue(element, controlKind(element), Reflect.get(current.scope, state.name, current.scope));
      } catch (error) { core.report(error); }
    });
  }

  core.renderHooks.push(render);
  core.updateModel = update;
  core.modelCompositionStart = function (element) {
    if (element && element.hasAttribute && element.hasAttribute("data-kit-model") &&
      !core.ignoredForRuntime(element)) composing.add(element);
  };
  core.modelCompositionEnd = function (element) {
    if (!element || core.ignoredForRuntime(element)) return;
    composing.delete(element);
    update(element, "input", true);
  };
  core.phase = "model";
})(document);
;(function (document) {
  "use strict";

  var core = document[Symbol.for("kitjs:assembly")];
  if (!core || core.phase !== "model") throw new Error("KitJS: events fragment loaded out of order");
  if (core.reuse) { core.phase = "events"; return; }

  var OWN = core.OWN;
  var outsideActive = Object.create(null);
  var prepared = false;

  function validMetadata(element) {
    if (core.ignoredForRuntime(element)) return false;
    function valid(candidate) {
      return (!core.componentMetadata || core.componentMetadata(candidate, true) !== null) &&
        (!core.scopeSeed || core.scopeSeed(candidate, true) !== null);
    }
    if (!valid(element)) return false;
    var boundary = core.ownerFor && core.ownerFor(element);
    return !boundary || boundary === element || valid(boundary);
  }

  function eventElement(event) {
    var target = event.target;
    return target && target.nodeType === 1 ? target : target && target.parentElement;
  }

  function safeEvent(element, name) {
    if (core.ignoredForRuntime(element)) return null;
    if (!validMetadata(element)) return null;
    var events = core.elementRecord(element).events;
    if (OWN.call(events, name)) return events[name];
    try {
      var descriptor = core.parseEventAttribute(name);
      if (!descriptor) return null;
      var program = core.safeProgram(element, name, "action");
      events[name] = program ? {
        descriptor: descriptor,
        program: program,
        onceDone: false,
        timer: 0,
        generation: 0
      } : null;
      if (events[name] && descriptor.outside) {
        outsideActive[descriptor.type] = (outsideActive[descriptor.type] || 0) + 1;
      }
    } catch (error) {
      core.report(error);
      events[name] = null;
    }
    return events[name];
  }

  function eventStates(element, type) {
    if (core.ignoredForRuntime(element)) return [];
    var prefix = "data-kit-" + type;
    var output = [];
    element.getAttributeNames().forEach(function (name) {
      if (name !== prefix && name.indexOf(prefix + ":") !== 0) return;
      var state = safeEvent(element, name);
      if (state) output.push(state);
    });
    return output;
  }

  function validateElement(element) {
    if (core.ignoredForRuntime(element)) return;
    var record = core.records.get(element);
    if (!validMetadata(element)) {
      if (!record) record = core.elementRecord(element);
      if (element.hasAttribute("data-kit-component")) record.invalid["data-kit-component"] = true;
      if (element.hasAttribute("data-kit-version")) record.invalid["data-kit-version"] = true;
      if (element.hasAttribute("data-kit-scope")) record.invalid["data-kit-scope"] = true;
      return;
    }
    element.getAttributeNames().forEach(function (name) {
      if (name.indexOf("data-kit-") !== 0 || record && OWN.call(record.invalid, name)) return;
      try {
        var descriptor = core.parseEventAttribute(name);
        if (descriptor) safeEvent(element, name);
      } catch (error) {
        core.report(error);
        if (!record) record = core.elementRecord(element);
        record.invalid[name] = true;
      }
    });
  }

  function prepare() {
    if (prepared) return;
    prepared = true;
    if (core.prepareComponentTree) core.prepareComponentTree(document);
    document.querySelectorAll("*").forEach(validateElement);
  }

  function prepareTree(root) {
    if (!root || root.nodeType === 1 && core.ignoredForRuntime(root)) return;
    if (core.prepareComponentTree) core.prepareComponentTree(root);
    if (root.nodeType === 1) validateElement(root);
    if (root.querySelectorAll) root.querySelectorAll("*").forEach(validateElement);
  }

  function disposeElement(element) {
    var record = core.records.get(element);
    if (!record) return;
    Object.keys(record.events).forEach(function (name) {
      var state = record.events[name];
      if (!state) return;
      if (state.timer) clearTimeout(state.timer);
      state.timer = 0;
      state.generation++;
      if (state.descriptor.outside && outsideActive[state.descriptor.type]) {
        outsideActive[state.descriptor.type]--;
      }
    });
  }

  function snapshot(event, target) {
    var value = null;
    if (target && "value" in target) {
      var candidate = target.value;
      if (candidate === null || typeof candidate === "string" || typeof candidate === "boolean" ||
          typeof candidate === "number" && Number.isFinite(candidate)) value = candidate;
    }
    var checked = target && "checked" in target ? !!target.checked : false;
    var output = Object.create(null);
    Object.assign(output, {
      type: String(event.type || ""),
      key: typeof event.key === "string" ? event.key : "",
      code: typeof event.code === "string" ? event.code : "",
      button: typeof event.button === "number" ? event.button : 0,
      buttons: typeof event.buttons === "number" ? event.buttons : 0,
      clientX: typeof event.clientX === "number" ? event.clientX : 0,
      clientY: typeof event.clientY === "number" ? event.clientY : 0,
      detail: typeof event.detail === "number" ? event.detail : 0,
      ctrlKey: !!event.ctrlKey,
      shiftKey: !!event.shiftKey,
      altKey: !!event.altKey,
      metaKey: !!event.metaKey,
      repeat: !!event.repeat,
      isComposing: !!event.isComposing,
      value: value,
      checked: checked
    });
    return Object.freeze(output);
  }

  function locals(eventSnapshot) {
    var output = Object.create(null);
    output.$event = eventSnapshot;
    return output;
  }

  function matches(state, element, target, event) {
    var descriptor = state.descriptor;
    if (state.onceDone) return false;
    if (descriptor.self && target !== element) return false;
    if (descriptor.key && (event.isComposing || event.keyCode === 229 || event.key !== descriptor.key)) {
      return false;
    }
    return true;
  }

  function connectedOwner(state) {
    var owner = null;
    Array.prototype.some.call(document.querySelectorAll("*"), function (element) {
      if (core.ignoredForRuntime(element)) return false;
      var record = core.records.get(element);
      if (!record || record.events[state.descriptor.name] !== state ||
          !element.hasAttribute(state.descriptor.name)) return false;
      owner = element;
      return true;
    });
    return owner;
  }

  function scheduleDebounce(state, eventSnapshot) {
    if (state.timer) clearTimeout(state.timer);
    var generation = ++state.generation;
    state.timer = setTimeout(function () {
      state.timer = 0;
      if (generation !== state.generation || state.onceDone) return;
      var owner = connectedOwner(state);
      if (!owner) return;
      if (core.executeAttribute(owner, state.descriptor.name, locals(eventSnapshot)) &&
          state.descriptor.once) state.onceDone = true;
    }, state.descriptor.delay);
  }

  function execute(state, element, event, eventSnapshot) {
    var descriptor = state.descriptor;
    if (descriptor.prevent && event.cancelable) event.preventDefault();
    if (descriptor.stop) event.stopPropagation();

    if (descriptor.delay) {
      scheduleDebounce(state, eventSnapshot);
      return true;
    }

    var success = core.executeAttribute(element, descriptor.name, locals(eventSnapshot));
    if (success && descriptor.once) state.onceDone = true;
    return success;
  }

  function direct(event, target, eventSnapshot) {
    var element = target;
    while (element && element !== document) {
      var states = eventStates(element, event.type);
      var stopped = false;
      for (var index = 0; index < states.length; index++) {
        var state = states[index];
        if (state.descriptor.outside || !matches(state, element, target, event)) continue;
        execute(state, element, event, eventSnapshot);
        if (state.descriptor.stop) stopped = true;
      }
      if (stopped) return true;
      element = element.parentElement;
    }
    return false;
  }

  function outside(event, target, eventSnapshot) {
    return Array.prototype.some.call(document.querySelectorAll("*"), function (element) {
      if (core.ignoredForRuntime(element)) return false;
      if (element.contains(target)) return false;
      var states = eventStates(element, event.type);
      var stopped = false;
      for (var index = 0; index < states.length; index++) {
        var state = states[index];
        if (!state.descriptor.outside || !matches(state, element, target, event)) continue;
        execute(state, element, event, eventSnapshot);
        if (state.descriptor.stop) stopped = true;
      }
      return stopped;
    });
  }

  function dispatch(event) {
    try {
      var target = eventElement(event);
      if (!target || core.ignoredForRuntime(target)) return;
      if (event.type === "input" || event.type === "change") core.updateModel(target, event.type, false);
      var eventSnapshot = snapshot(event, target);
      if (!direct(event, target, eventSnapshot) && outsideActive[event.type]) {
        outside(event, target, eventSnapshot);
      }
    } catch (error) { core.report(error); }
  }

  core.prepareHooks.push(prepare);
  core.prepareEventTree = prepareTree;
  core.disposeElementEvents = disposeElement;
  core.installEvents = function () {
    core.eventTypes.forEach(function (type) { document.addEventListener(type, dispatch); });
    document.addEventListener("compositionstart", function (event) {
      core.modelCompositionStart(eventElement(event));
    });
    document.addEventListener("compositionend", function (event) {
      core.modelCompositionEnd(eventElement(event));
    });
  };
  core.phase = "events";
})(document);
;(function (document) {
  "use strict";

  var core = document[Symbol.for("kitjs:assembly")];
  if (!core || core.phase !== "events") throw new Error("KitJS: Morph loaded out of order");
  if (core.reuse) { core.phase = "morph"; return; }

  var ELEMENT = 1;
  var TEXT = 3;
  var COMMENT = 8;
  var RETAIN = "data-kit-retain";
  var IGNORE = "data-kit-ignore";
  var COMPONENT_METADATA = {
    "data-kit-component": true,
    "data-kit-version": true,
    "data-kit-local": true
  };
  var URL_ATTRIBUTES = {
    action: true,
    formaction: true,
    href: true,
    poster: true,
    src: true,
    "xlink:href": true
  };
  var ACTIVE_ELEMENTS = {
    applet: true,
    embed: true,
    fencedframe: true,
    frame: true,
    iframe: true,
    object: true,
    portal: true
  };

  function unsafeURL(value) {
    var text = String(value || "").replace(/[\u0000-\u0020]+/g, "").toLowerCase();
    return text.indexOf("javascript:") === 0 || text.indexOf("vbscript:") === 0 ||
      text.indexOf("data:text/html") === 0;
  }

  function sanitizeAttributes(element) {
    element.getAttributeNames().forEach(function (name) {
      var lower = name.toLowerCase();
      if (lower.indexOf("on") === 0 || lower === "srcdoc" ||
        URL_ATTRIBUTES[lower] && unsafeURL(element.getAttribute(name))) {
        element.removeAttribute(name);
      }
    });
  }

  function activeContent(element) {
    var name = String(element.localName || "").toLowerCase();
    if (ACTIVE_ELEMENTS[name]) return true;
    return name === "meta" &&
      String(element.getAttribute("http-equiv") || "").trim().toLowerCase() === "refresh";
  }

  function sanitizeContainer(container) {
    Array.prototype.slice.call(container.childNodes).forEach(function (node) {
      if (node.nodeType !== ELEMENT) return;
      if ((node.localName && node.localName.toLowerCase() === "script") || activeContent(node)) {
        container.removeChild(node);
        return;
      }
      sanitizeAttributes(node);
      if (node.localName && node.localName.toLowerCase() === "template" && node.content) {
        sanitizeContainer(node.content);
      }
      sanitizeContainer(node);
    });
  }

  function sanitizedClone(element) {
    var clone = element.cloneNode(true);
    if ((clone.localName && clone.localName.toLowerCase() === "script") || activeContent(clone)) {
      throw new TypeError("KitJS: Morph root cannot contain active document content");
    }
    sanitizeAttributes(clone);
    if (clone.localName && clone.localName.toLowerCase() === "template" && clone.content) {
      sanitizeContainer(clone.content);
    }
    sanitizeContainer(clone);
    return clone;
  }

  function directiveIdentity(element, normalizeComponent) {
    var attributes = [];
    element.getAttributeNames().forEach(function (name) {
      var lower = name.toLowerCase();
      if (lower.indexOf("data-kit-") !== 0 || normalizeComponent && COMPONENT_METADATA[lower]) return;
      attributes.push(lower + "\u0000" + element.getAttribute(name));
    });
    attributes.sort();
    return attributes.join("\u0001");
  }

  function retainKey(element) {
    if (!element || element.nodeType !== ELEMENT || element.hasAttribute(IGNORE) ||
      !element.hasAttribute(RETAIN)) return "";
    return element.getAttribute(RETAIN);
  }

  function parentElement(node) {
    var parent = node && node.parentNode;
    return parent && parent.nodeType === ELEMENT ? parent : null;
  }

  function canonicalAlias(element) {
    return element.hasAttribute("data-kit-as") ? element.getAttribute("data-kit-as") : null;
  }

  function retainCompatible(currentEntry, incomingEntry) {
    var current = currentEntry.element;
    var incoming = incomingEntry.element;
    var mounted = currentEntry.mounted;
    return !currentEntry.blocked && (!mounted || mounted.name === currentEntry.request.name &&
        mounted.version === currentEntry.request.version && mounted.lane === currentEntry.request.lane &&
        mounted.alias === canonicalAlias(current)) &&
      current.namespaceURI === incoming.namespaceURI && current.localName === incoming.localName &&
      currentEntry.request.name === incomingEntry.request.name &&
      currentEntry.request.version === incomingEntry.request.version &&
      currentEntry.request.lane === incomingEntry.request.lane &&
      canonicalAlias(current) === canonicalAlias(incoming);
  }

  function retainContext(currentRoot, incomingRoot) {
    if (typeof core.inspectRetains !== "function") {
      throw new Error("KitJS: incomplete retain metadata assembly");
    }
    var current = core.inspectRetains(currentRoot);
    var incoming = core.inspectRetains(incomingRoot);
    var context = {
      current: current,
      incoming: incoming,
      pairs: new Map(),
      incomingPairs: new Map(),
      protected: new Set(),
      used: new Set(),
      incomingAncestors: new WeakSet(),
      parking: document.createDocumentFragment()
    };
    incoming.forEach(function (incomingEntry, key) {
      var currentEntry = current.get(key);
      if (!currentEntry || !retainCompatible(currentEntry, incomingEntry)) return;
      context.pairs.set(currentEntry.element, incomingEntry.element);
      context.incomingPairs.set(incomingEntry.element, currentEntry.element);
      context.protected.add(currentEntry.element);
      var ancestor = parentElement(incomingEntry.element);
      while (ancestor) {
        context.incomingAncestors.add(ancestor);
        if (ancestor === incomingRoot) break;
        ancestor = parentElement(ancestor);
      }
    });
    return context;
  }

  function componentCompatible(current, incoming) {
    var currentHasComponent = current.hasAttribute("data-kit-component");
    var incomingHasComponent = incoming.hasAttribute("data-kit-component");
    var currentScope = current.getAttribute("data-kit-scope");
    var incomingScope = incoming.getAttribute("data-kit-scope");
    if (!currentHasComponent && !incomingHasComponent) {
      if (currentScope === null && incomingScope === null) return true;
      return currentScope !== null && incomingScope !== null && currentScope === incomingScope &&
        current.getAttribute("data-kit-as") === incoming.getAttribute("data-kit-as") &&
        current.getAttribute("data-kit-version") === incoming.getAttribute("data-kit-version");
    }
    if (!currentHasComponent || !incomingHasComponent || typeof core.componentMetadata !== "function") return false;
    var currentRequest = core.componentMetadata(current, false);
    var incomingRequest = core.componentMetadata(incoming, false);
    return !!currentRequest && !!incomingRequest &&
      currentRequest.name === incomingRequest.name &&
      currentRequest.version === incomingRequest.version &&
      currentRequest.lane === incomingRequest.lane &&
      current.getAttribute("data-kit-as") === incoming.getAttribute("data-kit-as") &&
      directiveIdentity(current, true) === directiveIdentity(incoming, true);
  }

  function compatible(current, incoming, context) {
    if (!current || !incoming || current.nodeType !== incoming.nodeType) return false;
    if (current.nodeType === TEXT || current.nodeType === COMMENT) return true;
    if (current.nodeType !== ELEMENT) return false;
    if (current.namespaceURI !== incoming.namespaceURI || current.localName !== incoming.localName) return false;
    var currentIgnored = current.hasAttribute(IGNORE);
    var incomingIgnored = incoming.hasAttribute(IGNORE);
    if (currentIgnored || incomingIgnored) return currentIgnored && incomingIgnored;
    if (context && context.pairs.get(current) === incoming) return true;
    if (context && (retainKey(current) || retainKey(incoming))) return false;
    if (!componentCompatible(current, incoming)) return false;
    if (current.localName === "input" &&
      (current.getAttribute("type") || "text").toLowerCase() !==
      (incoming.getAttribute("type") || "text").toLowerCase()) return false;
    if (current.localName === "input" &&
      (current.getAttribute("type") || "text").toLowerCase() === "file") return false;
    if ((current.localName === "input" || current.localName === "textarea" || current.localName === "select") &&
      !stableFormIdentity(current, incoming)) return false;
    return true;
  }

  function identity(node) {
    if (!node || node.nodeType !== ELEMENT) return "";
    var retained = retainKey(node);
    if (retained) return "retain\u0000" + retained;
    var id = node.getAttribute("id");
    return id ? "id\u0000" + id : "";
  }

  function resetElement(element) {
    if (core.disposeElementEvents) core.disposeElementEvents(element);
    core.records.delete(element);
  }

  function disposeNode(node) {
    if (!node || node.nodeType !== ELEMENT) return;
    if (core.disposeTree) core.disposeTree(node);
  }

  function stableFormIdentity(current, incoming) {
    var name = current.localName;
    if (name !== "input" && name !== "textarea" && name !== "select") return false;
    var id = current.getAttribute("id");
    return !!id && id === incoming.getAttribute("id");
  }

  function formState(element) {
    var name = element.localName;
    if (name === "input") {
      var type = (element.type || "text").toLowerCase();
      var checked = type === "checkbox" || type === "radio";
      if (checked && element.checked !== element.defaultChecked) {
        return { kind: "checked", checked: element.checked };
      }
      if (type !== "file" && element.value !== element.defaultValue) {
        return { kind: "value", value: element.value };
      }
      return null;
    }
    if (name === "textarea") {
      return element.value !== element.defaultValue ? { kind: "value", value: element.value } : null;
    }
    if (name !== "select") return null;
    var options = Array.prototype.slice.call(element.options);
    if (element.multiple) {
      var dirty = options.some(function (option) { return option.selected !== option.defaultSelected; });
      var values = options.filter(function (option) { return option.selected; }).map(function (option) {
        return option.value;
      });
      return dirty ? { kind: "selectedValues", values: values } : null;
    }
    var defaultIndex = -1;
    for (var index = 0; index < options.length; index++) {
      if (options[index].defaultSelected) { defaultIndex = index; break; }
    }
    if (defaultIndex < 0 && options.length) defaultIndex = 0;
    return element.selectedIndex !== defaultIndex ? {
      kind: "selectedValue",
      value: element.selectedIndex < 0 ? null : options[element.selectedIndex].value
    } : null;
  }

  function applyIncomingFormState(element, incoming) {
    var name = element.localName;
    if (name === "input") {
      var type = (element.type || "text").toLowerCase();
      if (type === "checkbox" || type === "radio") {
        element.checked = incoming.checked;
        element.indeterminate = incoming.indeterminate;
      }
      if (type !== "file") element.value = incoming.value;
      return;
    }
    if (name === "textarea") {
      element.value = incoming.value;
      return;
    }
    if (name !== "select") return;
    if (!element.multiple) {
      element.selectedIndex = incoming.selectedIndex;
      return;
    }
    Array.prototype.forEach.call(element.options, function (option, index) {
      option.selected = !!incoming.options[index] && incoming.options[index].selected;
    });
  }

  function restoreFormState(element, state) {
    if (!state) return;
    if (state.kind === "checked") element.checked = state.checked;
    else if (state.kind === "value") element.value = state.value;
    else if (state.kind === "selectedValue") {
      if (state.value === null) element.selectedIndex = -1;
      else Array.prototype.some.call(element.options, function (option, index) {
        if (option.value !== state.value) return false;
        element.selectedIndex = index;
        return true;
      });
    } else if (state.kind === "selectedValues") {
      var remaining = state.values.slice();
      Array.prototype.forEach.call(element.options, function (option) {
        var index = remaining.indexOf(option.value);
        option.selected = index >= 0;
        if (index >= 0) remaining.splice(index, 1);
      });
    }
  }

  function patchAttributes(current, incoming) {
    current.getAttributeNames().forEach(function (name) {
      if (!incoming.hasAttribute(name)) current.removeAttribute(name);
    });
    incoming.getAttributeNames().forEach(function (name) {
      var value = incoming.getAttribute(name);
      if (current.getAttribute(name) !== value) current.setAttribute(name, value);
    });
  }

  function uniqueIdentities(nodes) {
    var output = new Map();
    nodes.forEach(function (node) {
      var key = identity(node);
      if (!key) return;
      output.set(key, output.has(key) ? null : node);
    });
    return output;
  }

  function positionalMatch(cursor) {
    return cursor && !identity(cursor) ? cursor : null;
  }

  function parkRetainedDescendants(node, context) {
    if (!context || !node || node.nodeType !== ELEMENT || !node.querySelectorAll) return;
    Array.prototype.forEach.call(node.querySelectorAll("[data-kit-retain]"), function (element) {
      if (!context.protected.has(element) || context.used.has(element) || !node.contains(element)) return;
      context.parking.appendChild(element);
    });
  }

  function morphChildren(current, incoming, context) {
    var original = Array.prototype.slice.call(current.childNodes);
    var keyed = uniqueIdentities(original);
    var used = new Set();
    var cursor = current.firstChild;
    Array.prototype.slice.call(incoming.childNodes).forEach(function (incomingChild) {
      while (cursor && used.has(cursor)) cursor = cursor.nextSibling;
      var retained = retainKey(incomingChild);
      var key = identity(incomingChild);
      var match = retained && context ? context.incomingPairs.get(incomingChild) :
        key ? keyed.get(key) : positionalMatch(cursor);
      if (!match || used.has(match) || context && context.used.has(match) ||
        !compatible(match, incomingChild, context)) match = null;
      if (!match) {
        var shallow = incomingChild.nodeType === ELEMENT && context &&
          context.incomingAncestors.has(incomingChild);
        var inserted = incomingChild.cloneNode(!shallow);
        current.insertBefore(inserted, cursor);
        if (shallow) morphElement(inserted, incomingChild, context);
        return;
      }
      if (match !== cursor) current.insertBefore(match, cursor);
      used.add(match);
      if (context) context.used.add(match);
      morphNode(match, incomingChild, context);
      cursor = match.nextSibling;
    });
    original.forEach(function (node) {
      if (used.has(node) || node.parentNode !== current) return;
      if (context && context.protected.has(node) && !context.used.has(node)) return;
      parkRetainedDescendants(node, context);
      disposeNode(node);
      current.removeChild(node);
    });
  }

  function morphElement(current, incoming, context) {
    var state = stableFormIdentity(current, incoming) ? formState(current) : null;
    resetElement(current);
    patchAttributes(current, incoming);
    if (current.localName === "template" && current.content && incoming.content) {
      morphChildren(current.content, incoming.content, context);
    } else morphChildren(current, incoming, context);
    applyIncomingFormState(current, incoming);
    restoreFormState(current, state);
    return current;
  }

  function morphNode(current, incoming, context) {
    if (!compatible(current, incoming, context)) {
      var shallow = incoming.nodeType === ELEMENT && context &&
        context.incomingAncestors.has(incoming);
      var replacement = incoming.cloneNode(!shallow);
      if (shallow) parkRetainedDescendants(current, context);
      disposeNode(current);
      current.parentNode.replaceChild(replacement, current);
      if (shallow) morphElement(replacement, incoming, context);
      return replacement;
    }
    if (current.nodeType === ELEMENT && current.hasAttribute(IGNORE) && incoming.hasAttribute(IGNORE)) {
      return current;
    }
    if (current.nodeType === TEXT || current.nodeType === COMMENT) {
      if (current.data !== incoming.data) current.data = incoming.data;
      return current;
    }
    if (context && context.pairs.get(current) === incoming) context.used.add(current);
    return morphElement(current, incoming, context);
  }

  function focusState(root) {
    var active = document.activeElement;
    if (!active || active === document.body || active !== root && !root.contains(active)) return null;
    var selection = null;
    try {
      if (typeof active.selectionStart === "number") {
        selection = [active.selectionStart, active.selectionEnd, active.selectionDirection];
      }
    } catch (_) { selection = null; }
    return { element: active, id: active.id || "", selection: selection };
  }

  function findIdentity(root, state) {
    var output = null;
    if (state.id && root.querySelectorAll) {
      Array.prototype.some.call(root.querySelectorAll("[id]"), function (element) {
        if (element.id !== state.id) return false;
        output = element;
        return true;
      });
    }
    return output;
  }

  function invalidateBoundaries(root) {
    var records = core.liveComponents(root);
    var owner = core.scopeRecordFor(root);
    if (owner && records.indexOf(owner) < 0) records.unshift(owner);
    records.forEach(function (record) { core.invalidate(record); });
  }

  function restoreFocus(root, state) {
    if (!state) return;
    var element = state.element && state.element.isConnected ? state.element :
      findIdentity(root, state);
    if (!element || typeof element.focus !== "function") return;
    if (document.activeElement !== element) {
      try { element.focus({ preventScroll: true }); }
      catch (_) { element.focus(); }
    }
    if (state.selection && typeof element.setSelectionRange === "function") {
      try { element.setSelectionRange(state.selection[0], state.selection[1], state.selection[2]); }
      catch (_) { /* The replacement input type may reject selection. */ }
    }
  }

  function morph(currentRoot, incomingRoot) {
    if (!currentRoot || currentRoot.nodeType !== ELEMENT ||
      !incomingRoot || incomingRoot.nodeType !== ELEMENT) {
      throw new TypeError("KitJS: Morph expects two element roots");
    }
    var focus = focusState(currentRoot);
    var incoming = sanitizedClone(incomingRoot);
    if (core.validateScopeTree) core.validateScopeTree(incoming);
    if (core.validateComponentTree) core.validateComponentTree(incoming);
    var context = retainContext(currentRoot, incoming);
    if (core.resetStructures) core.resetStructures(currentRoot);
    var result = morphNode(currentRoot, incoming, context);
    if (core.prepareEventTree) core.prepareEventTree(result);
    restoreFocus(result, focus);
    invalidateBoundaries(result);
    return result;
  }

  core.validateMorphRetains = function (currentRoot, incomingRoot) {
    if (!currentRoot || currentRoot.nodeType !== ELEMENT ||
      !incomingRoot || incomingRoot.nodeType !== ELEMENT) {
      throw new TypeError("KitJS: Morph retain validation expects two element roots");
    }
    if (core.validateScopeTree) core.validateScopeTree(incomingRoot);
    if (core.validateComponentTree) core.validateComponentTree(incomingRoot);
    retainContext(currentRoot, incomingRoot);
    return true;
  };
  core.morph = morph;
  core.phase = "morph";
})(document);
; (function (global, document) {
  "use strict";

  var core = document[Symbol.for("kitjs:assembly")];
  if (!core || core.phase !== "morph") throw new Error("KitJS: Drive fragment loaded out of order");
  if (core.reuse) { core.phase = "drive"; return; }
  if (typeof core.morph !== "function" || !Array.isArray(core.startHooks)) {
    throw new Error("KitJS: incomplete Hydrate runtime assembly");
  }

  var profileScript = document.currentScript;
  var stagedProfile = profileScript && profileScript.getAttribute("data-kitwork-jit") === "hydrate";
  var profileURL = profileScript && profileScript.src ? absoluteURL(profileScript.src, document.baseURI) : null;
  var profilePlan = profileScript && profileScript.hasAttribute("data-kitwork-plan")
    ? profileScript.getAttribute("data-kitwork-plan")
    : null;
  var HANDOFF = Symbol.for("kitjs:handoff");
  var activeVisit = null;
  var navigationCritical = false;
  var navigationTerminal = false;
  var deferredNavigation = null;
  var visitSequence = 0;
  var started = false;
  var scrollTimer = 0;
  var lastSavedScroll = null;
  var lastSavedURL = "";
  var documentPath = global.location.pathname;
  var documentSearch = global.location.search;
  var SCROLL_SAVE_DELAY = 250;
  var HANDOFF_LOAD_TIMEOUT = 10000;
  var HANDOFF_GRAPH_CACHE_LIMIT = 32;
  var NAVIGATION_EVENT = "kit:navigation";
  var handoffGraphs = new Map();
  var engineHandoffScripts = new WeakSet();
  var liveStagedScripts = null;
  var liveStagedSignatures = null;
  var liveExecutableTopology = null;
  var ACTIVE_ELEMENTS = {
    applet: true,
    embed: true,
    fencedframe: true,
    frame: true,
    iframe: true,
    object: true,
    portal: true
  };
  var STAGED_ROLES = {
    runtime: true,
    hydrate: true,
    graph: true,
    service: true,
    component: true,
    components: true
  };
  var STAGED_SCRIPT_ATTRIBUTES = {
    "data-kitwork-jit": true,
    "data-kitwork-hash": true,
    src: true,
    integrity: true,
    crossorigin: true,
    defer: true
  };

  function array(value) {
    return Array.prototype.slice.call(value || []);
  }

  function absoluteURL(source, base) {
    try { return new URL(source, base).href; }
    catch (_) { return null; }
  }

  function sameOriginURL(source) {
    var href = absoluteURL(source, document.baseURI);
    if (!href) return null;
    var url = new URL(href);
    if ((url.protocol !== "http:" && url.protocol !== "https:") || url.origin !== global.location.origin) {
      return null;
    }
    return url;
  }

  function emitNavigation(visit, phase, values) {
    values = values || {};
    var detail = {
      id: visit.sequence,
      phase: phase,
      url: String(values.url || visit.url)
    };
    if (phase === "progress") {
      detail.loaded = values.loaded;
      detail.total = values.total;
    } else if (phase === "finish") detail.outcome = values.outcome;
    Object.freeze(detail);

    var event;
    if (typeof global.CustomEvent === "function") {
      event = new global.CustomEvent(NAVIGATION_EVENT, {
        detail: detail,
        bubbles: false,
        cancelable: false,
        composed: false
      });
    } else {
      event = document.createEvent("CustomEvent");
      event.initCustomEvent(NAVIGATION_EVENT, false, false, detail);
    }
    document.dispatchEvent(event);
  }

  function currentVisit(visit) {
    return !!visit && !visit.finished && activeVisit === visit;
  }

  function finishVisit(visit, outcome, url) {
    if (!visit || visit.finished) return false;
    visit.finished = true;
    if (activeVisit === visit) activeVisit = null;
    emitNavigation(visit, "finish", { url: url || visit.url, outcome: outcome });
    return true;
  }

  function cancelVisit(visit) {
    if (!visit || visit.finished) return false;
    if (visit.handoff && typeof visit.handoff.cancel === "function") {
      try { visit.handoff.cancel(); } catch (_) { /* Handoff cancellation is best effort. */ }
    }
    try { visit.controller.abort(); } catch (_) { /* AbortController is best effort. */ }
    return finishVisit(visit, "cancelled", visit.url);
  }

  function copiedVisitOptions(options) {
    options = options || {};
    var copied = {};
    if (Object.prototype.hasOwnProperty.call(options, "history")) copied.history = options.history;
    if (options.scroll) copied.scroll = { x: options.scroll.x, y: options.scroll.y };
    return copied;
  }

  function settleNavigation(intent, result, error) {
    if (!intent) return;
    if (error && typeof intent.reject === "function") intent.reject(error);
    else if (typeof intent.resolve === "function") intent.resolve(result);
  }

  function discardDeferredNavigation() {
    var intent = deferredNavigation;
    deferredNavigation = null;
    settleNavigation(intent, false);
  }

  function queueNavigation(intent) {
    var previous = deferredNavigation;
    deferredNavigation = intent;
    settleNavigation(previous, false);
  }

  function deferVisit(url, options) {
    return new Promise(function (resolve, reject) {
      queueNavigation({
        kind: "visit",
        source: url.href,
        options: copiedVisitOptions(options),
        resolve: resolve,
        reject: reject
      });
    });
  }

  function queueNativeAssign(source, resolve, reject) {
    queueNavigation({
      kind: "assign",
      source: String(source),
      resolve: resolve || null,
      reject: reject || null
    });
  }

  function queueScrollRestore(url, position) {
    queueNavigation({
      kind: "restore",
      source: url.href,
      position: position ? { x: position.x, y: position.y } : null
    });
  }

  function beginNavigationCritical() {
    if (navigationCritical) return false;
    navigationCritical = true;
    navigationTerminal = false;
    return true;
  }

  function terminateNavigationCritical() {
    navigationTerminal = true;
    discardDeferredNavigation();
  }

  function executeNavigationIntent(intent) {
    if (!intent) return;
    if (intent.kind === "visit") {
      var result;
      try { result = visit(intent.source, intent.options); }
      catch (error) {
        settleNavigation(intent, false, error);
        return;
      }
      Promise.resolve(result).then(function (loaded) {
        settleNavigation(intent, loaded);
      }, function (error) {
        settleNavigation(intent, false, error);
      });
      return;
    }

    if (activeVisit) {
      // Seed the native intent before cancellation. A synchronous finish
      // listener may replace it, and the latest authored intent must win.
      beginNavigationCritical();
      queueNavigation(intent);
      cancelVisit(activeVisit);
      endNavigationCritical(true);
      return;
    }

    if (intent.kind === "restore") {
      var url = sameOriginURL(intent.source);
      if (url) {
        if (intent.position) rememberScroll(intent.position);
        restoreScroll(url, intent.position);
      }
      settleNavigation(intent, true);
      return;
    }

    try {
      flushScrollSave();
      hardNavigate(intent.source);
      settleNavigation(intent, false);
    } catch (error) {
      core.report(error);
      settleNavigation(intent, false, error);
    }
  }

  function endNavigationCritical(drain) {
    if (!navigationCritical) return;
    var intent = drain && !navigationTerminal ? deferredNavigation : null;
    if (!intent) discardDeferredNavigation();
    else deferredNavigation = null;
    navigationCritical = false;
    navigationTerminal = false;
    if (intent) executeNavigationIntent(intent);
  }

  function exactBodyLength(response) {
    if (!response || !response.headers || typeof response.headers.get !== "function") return 0;
    var encoding = String(response.headers.get("content-encoding") || "").trim().toLowerCase();
    if (encoding && encoding !== "identity") return 0;
    var source = String(response.headers.get("content-length") || "").trim();
    if (!/^[1-9][0-9]*$/.test(source)) return 0;
    var total = Number(source);
    return Number.isSafeInteger(total) && total > 0 ? total : 0;
  }

  function responseText(response, visit) {
    var total = exactBodyLength(response);
    var body = response && response.body;
    if (!total || !body || typeof body.getReader !== "function" ||
      typeof global.TextDecoder !== "function") return response.text();

    var decoder;
    var reader;
    try {
      decoder = new global.TextDecoder();
      reader = body.getReader();
    } catch (_) {
      if (reader && typeof reader.releaseLock === "function") reader.releaseLock();
      return response.text();
    }

    var chunks = [];
    var loaded = 0;
    var lastPercent = 0;
    function cancelReader() {
      try {
        var cancelled = reader.cancel();
        if (cancelled && typeof cancelled.catch === "function") cancelled.catch(function () {});
      } catch (_) { /* The visit controller also aborts the stream. */ }
    }
    function read() {
      if (!currentVisit(visit)) {
        cancelReader();
        return Promise.resolve(null);
      }
      return reader.read().then(function (result) {
        if (!currentVisit(visit)) {
          cancelReader();
          return null;
        }
        if (result.done) {
          var tail = decoder.decode();
          if (tail) chunks.push(tail);
          if (typeof reader.releaseLock === "function") {
            try { reader.releaseLock(); } catch (_) { /* Completed readers may already be unlocked. */ }
          }
          return chunks.join("");
        }
        var value = result.value;
        var size = value && Number(value.byteLength);
        if (!Number.isSafeInteger(size) || size < 0) {
          throw new TypeError("KitJS: invalid navigation response chunk");
        }
        loaded += size;
        if (!Number.isSafeInteger(loaded)) {
          throw new TypeError("KitJS: navigation response is too large");
        }
        var text = decoder.decode(value, { stream: true });
        if (text) chunks.push(text);
        if (loaded < total) {
          var percent = Math.floor(loaded / total * 100);
          if (percent > lastPercent) {
            lastPercent = percent;
            emitNavigation(visit, "progress", {
              url: visit.url,
              loaded: loaded,
              total: total
            });
          }
        }
        return read();
      });
    }
    return read();
  }

  function incomingBase(incoming, responseURL) {
    var base = incoming.head && incoming.head.querySelector("base[href]");
    return base ? absoluteURL(base.getAttribute("href"), responseURL) || responseURL : responseURL;
  }

  function compatibleBase(incoming, responseURL) {
    if (!document.head || !incoming.head) return false;
    var currentHref = document.head.querySelector("base[href]");
    var nextHref = incoming.head.querySelector("base[href]");
    if (!!currentHref !== !!nextHref) return false;
    if (currentHref) {
      var currentURL = absoluteURL(currentHref.getAttribute("href"), global.location.href);
      var nextURL = absoluteURL(nextHref.getAttribute("href"), responseURL);
      if (!currentURL || !nextURL || currentURL !== nextURL) return false;
    }

    var currentTarget = document.head.querySelector("base[target]");
    var nextTarget = incoming.head.querySelector("base[target]");
    if (!!currentTarget !== !!nextTarget) return false;
    return !currentTarget || currentTarget.getAttribute("target") === nextTarget.getAttribute("target");
  }

  function hasActiveDocumentContent(root) {
    if (!root) return false;
    if (root.nodeType === 1) {
      var name = String(root.localName || "").toLowerCase();
      if (ACTIVE_ELEMENTS[name] || name === "meta" &&
        String(root.getAttribute("http-equiv") || "").trim().toLowerCase() === "refresh") {
        return true;
      }
      if (name === "template" && root.content && hasActiveDocumentContent(root.content)) return true;
    }
    var child = root.firstChild;
    while (child) {
      if (hasActiveDocumentContent(child)) return true;
      child = child.nextSibling;
    }
    return false;
  }

  function sameProfile(incoming, responseURL) {
    if (stagedProfile) return sameStagedDelivery(incoming, responseURL);
    if (!profileURL || !incoming || !incoming.body) return false;
    var current = standaloneProfileScript(document, document.baseURI, true);
    var next = standaloneProfileScript(incoming, incomingBase(incoming, responseURL), false);
    return !!current && !!next && current.signature === next.signature;
  }

  function metaContentSecurityPolicies(head) {
    if (!head || !head.querySelectorAll) return [];
    return array(head.querySelectorAll("meta[http-equiv]")).filter(function (meta) {
      return meta.parentNode === head &&
        String(meta.getAttribute("http-equiv") || "").trim().toLowerCase() === "content-security-policy";
    }).map(function (meta) {
      return String(meta.getAttribute("content") || "");
    });
  }

  function compatibleContentSecurityPolicy(incoming) {
    if (!incoming || !incoming.head || !document.head) return false;
    var current = metaContentSecurityPolicies(document.head);
    var next = metaContentSecurityPolicies(incoming.head);
    if (current.length !== next.length) return false;
    for (var index = 0; index < current.length; index++) {
      if (current[index] !== next[index]) return false;
    }
    return true;
  }

  function hasContentSecurityPolicyHeader(response) {
    if (!response || !response.headers || typeof response.headers.get !== "function") return false;
    return !!(String(response.headers.get("content-security-policy") || "").trim() ||
      String(response.headers.get("content-security-policy-report-only") || "").trim());
  }

  function executableScriptKind(script) {
    var type = String(script.getAttribute("type") || "").trim().toLowerCase();
    var essence = type.split(";", 1)[0].trim();
    if (essence === "module" || essence === "importmap" || essence === "speculationrules") return essence;
    if (!essence || essence.indexOf("javascript") >= 0 || essence.indexOf("ecmascript") >= 0 ||
      essence === "text/jscript" || essence === "text/livescript") return "classic";
    return null;
  }

  function validIntegrityMetadata(source) {
    if (typeof global.atob !== "function" || typeof global.btoa !== "function") return false;
    var tokens = String(source || "").trim().split(/\s+/).filter(Boolean);
    if (!tokens.length) return false;
    return tokens.every(function (token) {
      var match = /^(sha256|sha384|sha512)-([A-Za-z0-9+/]+={0,2})$/.exec(token);
      if (!match) return false;
      try {
        var bytes = global.atob(match[2]);
        var length = match[1] === "sha256" ? 32 : match[1] === "sha384" ? 48 : 64;
        return bytes.length === length && global.btoa(bytes) === match[2];
      } catch (_) { return false; }
    });
  }

  function scriptAttributeSignature(script) {
    var attributes = [];
    var valid = true;
    array(script && script.attributes).forEach(function (attribute) {
      var name = String(attribute.name || "").toLowerCase();
      if (/^on/.test(name)) valid = false;
      attributes.push(name + "=" + String(attribute.value || ""));
    });
    if (!valid) return null;
    attributes.sort();
    return attributes.join("\n");
  }

  function standaloneProfileScript(root, base, current) {
    if (!root || !root.querySelectorAll || !profileURL) return null;
    var matches = array(root.querySelectorAll("script[src]")).filter(function (script) {
      return absoluteURL(script.getAttribute("src"), base) === profileURL;
    });
    if (matches.length !== 1 || current && matches[0] !== profileScript) return null;
    var script = matches[0];
    if (!script.parentNode || script.parentNode !== root.head ||
      executableScriptKind(script) !== "classic" || !script.hasAttribute("defer") ||
      script.hasAttribute("async") || script.hasAttribute("nomodule") ||
      String(script.textContent || "")) return null;
    if (!validIntegrityMetadata(script.getAttribute("integrity"))) return null;
    var attributes = scriptAttributeSignature(script);
    if (attributes === null) return null;
    return {
      node: script,
      signature: "profile\nurl=" + profileURL + "\n" + attributes
    };
  }

  function stableHeadScriptSignature(script, base) {
    if (!script || !script.parentNode || script.parentNode !== script.ownerDocument.head ||
      executableScriptKind(script) !== "classic" || !script.hasAttribute("src") ||
      !script.hasAttribute("defer") || script.hasAttribute("async") ||
      script.hasAttribute("nomodule") || String(script.textContent || "")) return null;
    var source = absoluteURL(script.getAttribute("src"), base);
    if (!source) return null;
    var integrity = String(script.getAttribute("integrity") || "").trim();
    if (!validIntegrityMetadata(integrity)) return null;
    var attributes = scriptAttributeSignature(script);
    if (attributes === null) return null;
    return "url=" + source + "\n" + attributes;
  }

  function stagedScriptSignature(script) {
    var attributes = scriptAttributeSignature(script);
    return attributes === null ? null : String(script.localName || "").toLowerCase() + "\n" +
      attributes + "\ntext=" + String(script.textContent || "");
  }

  function stagedRole(source) {
    var role = String(source || "").trim().toLowerCase();
    return STAGED_ROLES[role] ? role : "";
  }

  function reservedStagedNode(node) {
    if (!node || node.nodeType !== 1) return false;
    if (engineHandoffScripts.has(node)) return false;
    if (node.hasAttribute("data-kitwork-hash") || node.hasAttribute("data-kitwork-runtime") ||
      node.hasAttribute("data-kitwork-handoff")) return true;
    return String(node.localName || "").toLowerCase() === "script" &&
      !!stagedRole(node.getAttribute("data-kitwork-jit"));
  }

  function stagedReservedNodes(root) {
    if (!root || !root.querySelectorAll) return [];
    return array(root.querySelectorAll(
      "[data-kitwork-hash],[data-kitwork-runtime],[data-kitwork-handoff],[data-kitwork-jit]"
    )).filter(reservedStagedNode);
  }

  function exactStagedScriptAttributes(script) {
    var attributes = array(script && script.attributes);
    if (attributes.length !== 6) return false;
    for (var index = 0; index < attributes.length; index++) {
      if (!STAGED_SCRIPT_ATTRIBUTES[String(attributes[index].name || "").toLowerCase()]) return false;
    }
    return Object.keys(STAGED_SCRIPT_ATTRIBUTES).every(function (name) {
      return script.hasAttribute(name);
    });
  }

  function captureLiveStagedDelivery() {
    var candidate = stagedCandidate(document, global.location.href);
    if (!candidate || !sameCandidateDelivery(candidate, core.delivery)) return false;
    var signatures = candidate.scripts.map(stagedScriptSignature);
    if (signatures.some(function (signature) { return signature === null; })) return false;
    liveStagedScripts = candidate.scripts.slice();
    liveStagedSignatures = signatures.slice();
    return true;
  }

  function stagedScriptsForTopology(root, base) {
    var candidate = stagedCandidate(root, base);
    if (!candidate) return null;
    if (root !== document) return candidate.scripts;
    if (!liveStagedScripts || !liveStagedSignatures) return null;
    var signatures = candidate.scripts.map(stagedScriptSignature);
    if (signatures.some(function (signature) { return signature === null; })) return null;
    if (candidate.scripts.length !== liveStagedScripts.length ||
      candidate.scripts.some(function (script, index) {
        return script !== liveStagedScripts[index] || signatures[index] !== liveStagedSignatures[index];
      })) return null;
    return candidate.scripts;
  }

  function executableScriptTopology(root, base, current) {
    if (!root || !root.querySelectorAll) return null;
    var managed;
    var marker;
    if (stagedProfile) {
      managed = stagedScriptsForTopology(root, base);
      marker = "managed=staged";
    } else {
      var profile = standaloneProfileScript(root, base, current);
      managed = profile ? [profile.node] : null;
      marker = profile && profile.signature;
    }
    if (!managed || !marker) return null;
    var managedSet = new Set(managed);
    var managedSeen = 0;
    var markerWritten = false;
    var managedBlockClosed = false;
    var signatures = [];
    var scripts = array(root.querySelectorAll("script"));
    for (var index = 0; index < scripts.length; index++) {
      var script = scripts[index];
      if (engineHandoffScripts.has(script)) continue;
      if (managedSet.has(script)) {
        if (managedBlockClosed) return null;
        managedSeen++;
        if (!markerWritten) {
          signatures.push(marker);
          markerWritten = true;
        }
        continue;
      }
      if (!executableScriptKind(script)) continue;
      if (markerWritten) managedBlockClosed = true;
      var signature = stableHeadScriptSignature(script, base);
      if (!signature) return null;
      signatures.push("authored\n" + signature);
    }
    return managedSeen === managed.length ? signatures : null;
  }

  function compatibleExecutableScripts(incoming, responseURL) {
    if (!incoming || !incoming.head || !document.head) return false;
    var current = executableScriptTopology(document, document.baseURI, true);
    var next = executableScriptTopology(incoming, incomingBase(incoming, responseURL), false);
    if (!liveExecutableTopology || !current || !next || current.length !== liveExecutableTopology.length ||
      current.length !== next.length) return false;
    for (var index = 0; index < current.length; index++) {
      if (current[index] !== liveExecutableTopology[index] || current[index] !== next[index]) return false;
    }
    return true;
  }

  function sameStagedDelivery(incoming, responseURL) {
    var delivery = core.delivery;
    if (!incoming || !incoming.body || !delivery || delivery.profile !== "hydrate" ||
      !Array.isArray(delivery.assets) || !delivery.graphHash) return false;
    var scripts = array(incoming.querySelectorAll(
      "script[data-kitwork-hash],script[data-kitwork-jit=\"runtime\"]," +
      "script[data-kitwork-jit=\"hydrate\"],script[data-kitwork-jit=\"graph\"]," +
      "script[data-kitwork-jit=\"service\"],script[data-kitwork-jit=\"component\"]," +
      "script[data-kitwork-jit=\"components\"]"
    ));
    if (scripts.length !== delivery.assets.length) return false;
    for (var index = 0; index < delivery.assets.length; index++) {
      var script = scripts[index];
      var asset = delivery.assets[index];
      var expectedSource = "/jit/" + asset.name;
      var rawSource = script.getAttribute("src");
      var type = String(script.getAttribute("type") || "").trim().toLowerCase();
      if (script.getAttribute("data-kitwork-jit") !== asset.role ||
        script.getAttribute("data-kitwork-hash") !== asset.hash ||
        rawSource !== expectedSource || absoluteURL(rawSource, responseURL) !== asset.url ||
        script.getAttribute("integrity") !== asset.integrity ||
        script.getAttribute("crossorigin") !== "anonymous" ||
        !script.hasAttribute("defer") || script.hasAttribute("async") ||
        script.hasAttribute("data-kitwork-handoff") || script.hasAttribute("nomodule") ||
        type && type !== "text/javascript" &&
        type !== "application/javascript") return false;
    }
    return true;
  }

  function stagedIntegrity(hash) {
    if (!/^[0-9a-f]{64}$/.test(String(hash || "")) || typeof global.btoa !== "function") return null;
    var binary = "";
    for (var index = 0; index < hash.length; index += 2) {
      binary += String.fromCharCode(parseInt(hash.slice(index, index + 2), 16));
    }
    return "sha256-" + global.btoa(binary);
  }

  function stagedCandidate(incoming, responseURL) {
    if (!stagedProfile || !incoming || !incoming.body || !incoming.querySelectorAll) return null;
    var scripts = stagedReservedNodes(incoming);
    if (scripts.length < 3) return null;
    var assets = [];
    for (var index = 0; index < scripts.length; index++) {
      var script = scripts[index];
      var role = String(script.getAttribute("data-kitwork-jit") || "");
      var hash = String(script.getAttribute("data-kitwork-hash") || "");
      var rawSource = script.getAttribute("src");
      var match = /^\/jit\/([0-9a-f]{64})\.([A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?)\.js$/.exec(String(rawSource || ""));
      var type = String(script.getAttribute("type") || "").trim().toLowerCase();
      var url = match ? absoluteURL(rawSource, responseURL) : null;
      if (String(script.localName || "").toLowerCase() !== "script" ||
        !incoming.head || script.parentNode !== incoming.head || !exactStagedScriptAttributes(script) ||
        index > 0 && scripts[index - 1].nextElementSibling !== script ||
        String(script.textContent || "") || !match || match[1] !== hash || !url ||
        new URL(url).origin !== global.location.origin ||
        script.getAttribute("integrity") !== stagedIntegrity(hash) ||
        script.getAttribute("crossorigin") !== "anonymous" || !script.hasAttribute("defer") ||
        script.hasAttribute("async") || script.hasAttribute("data-kitwork-handoff") ||
        script.hasAttribute("nomodule") || type &&
        type !== "text/javascript" && type !== "application/javascript") return null;
      assets.push({
        node: script,
        role: role,
        hash: hash,
        integrity: script.getAttribute("integrity"),
        name: match[1] + "." + match[2] + ".js",
        rawSource: rawSource,
        url: url
      });
    }
    if (assets[0].role !== "runtime" || assets[1].role !== "hydrate" || assets[2].role !== "graph") {
      return null;
    }
    var phase = "service";
    var bundleSeen = false;
    for (var offset = 3; offset < assets.length; offset++) {
      var nextRole = assets[offset].role;
      if (nextRole === "service" && phase === "service") continue;
      if (nextRole === "components" && phase !== "component" && !bundleSeen) {
        phase = "components";
        bundleSeen = true;
        continue;
      }
      if (nextRole === "component") {
        phase = "component";
        continue;
      }
      return null;
    }
    return { scripts: scripts, assets: assets, graph: assets[2] };
  }

  function sameStagedAsset(left, right) {
    return !!left && !!right && left.role === right.role && left.hash === right.hash &&
      left.integrity === right.integrity && left.name === right.name && left.url === right.url;
  }

  function sameCandidateDelivery(candidate, delivery) {
    if (!candidate || !delivery || delivery.profile !== "hydrate" ||
      !Array.isArray(delivery.assets) || delivery.assets.length !== candidate.assets.length ||
      delivery.graphHash !== candidate.graph.hash) return false;
    for (var index = 0; index < candidate.assets.length; index++) {
      if (!sameStagedAsset(candidate.assets[index], delivery.assets[index])) return false;
    }
    return true;
  }

  function stableHandoffRuntime(candidate) {
    var delivery = core.delivery;
    return !!delivery && delivery.profile === "hydrate" && Array.isArray(delivery.assets) &&
      delivery.assets.length >= 3 && sameStagedAsset(candidate.assets[0], delivery.assets[0]) &&
      sameStagedAsset(candidate.assets[1], delivery.assets[1]);
  }

  function sameHandoffServices(target) {
    var current = core.delivery;
    if (!current || !target || !Array.isArray(current.assets) || !Array.isArray(target.assets)) return false;
    var currentServices = current.assets.filter(function (asset) { return asset.role === "service"; });
    var targetServices = target.assets.filter(function (asset) { return asset.role === "service"; });
    if (currentServices.length !== targetServices.length) return false;
    for (var index = 0; index < currentServices.length; index++) {
      var left = currentServices[index];
      var right = targetServices[index];
      if (!sameStagedAsset(left, right) || left.package !== right.package || left.version !== right.version) {
        return false;
      }
    }
    return true;
  }

  function sameCandidateServiceAssets(candidate) {
    var delivery = core.delivery;
    if (!delivery || !Array.isArray(delivery.assets)) return false;
    var current = delivery.assets.filter(function (asset) { return asset.role === "service"; });
    var incoming = candidate.assets.filter(function (asset) { return asset.role === "service"; });
    if (current.length !== incoming.length) return false;
    for (var index = 0; index < current.length; index++) {
      if (!sameStagedAsset(current[index], incoming[index])) return false;
    }
    return true;
  }

  function rememberHandoffGraph(hash, value) {
    if (!/^[0-9a-f]{64}$/.test(String(hash || "")) || !value) return false;
    if (handoffGraphs.has(hash)) handoffGraphs.delete(hash);
    while (handoffGraphs.size >= HANDOFF_GRAPH_CACHE_LIMIT) {
      var oldest = handoffGraphs.keys().next();
      if (oldest.done) break;
      handoffGraphs.delete(oldest.value);
    }
    handoffGraphs.set(hash, value);
    return true;
  }

  function handoffPackageKey(value) {
    return value.name + "\u0000" + value.version + "\u0000" + value.sourceHash;
  }

  function componentPackagesForAsset(graph, asset) {
    if (!graph || !graph.components || !graph.componentHashes || !asset) return null;
    var packages = [];
    if ((asset.role !== "component" && asset.role !== "components") ||
      !Array.isArray(asset.components) || !asset.components.length) return null;
    asset.components.forEach(function (source) {
      packages.push({ name: source.name, version: source.version, sourceHash: source.sourceHash });
    });
    if (asset.role === "component" && (packages.length !== 1 ||
      asset.package !== packages[0].name || asset.version !== packages[0].version ||
      asset.sourceHash !== packages[0].sourceHash)) return null;
    if (asset.role === "components" && packages.length < 2) return null;
    var seen = Object.create(null);
    for (var offset = 0; offset < packages.length; offset++) {
      var entry = packages[offset];
      if (!entry || typeof entry.name !== "string" || typeof entry.version !== "string" ||
        !/^[0-9a-f]{64}$/.test(String(entry.sourceHash || "")) ||
        graph.components[entry.name] !== entry.version ||
        graph.componentHashes[entry.name] !== entry.sourceHash) return null;
      var key = handoffPackageKey(entry);
      if (seen[key]) return null;
      seen[key] = true;
    }
    return packages;
  }

  function missingHandoffAssets(graph, delivery, missing) {
    if (!Array.isArray(missing) || !delivery || !Array.isArray(delivery.assets)) return null;
    var needed = Object.create(null);
    for (var index = 0; index < missing.length; index++) {
      var requirement = missing[index];
      if (!requirement || !/^[0-9a-f]{64}$/.test(String(requirement.sourceHash || ""))) return null;
      var key = handoffPackageKey(requirement);
      if (needed[key]) return null;
      needed[key] = true;
    }
    var selected = [];
    for (var assetIndex = 0; assetIndex < delivery.assets.length; assetIndex++) {
      var asset = delivery.assets[assetIndex];
      if (asset.role !== "component" && asset.role !== "components") continue;
      var packages = componentPackagesForAsset(graph, asset);
      if (!packages || !packages.length) return null;
      var count = 0;
      for (var packageIndex = 0; packageIndex < packages.length; packageIndex++) {
        if (needed[handoffPackageKey(packages[packageIndex])]) count++;
      }
      // A shared chunk is atomic. Re-executing only part of it would duplicate
      // a cached component registration, so partial overlap hard-falls back.
      if (asset.role === "components" && count > 0 && count !== packages.length) return null;
      if (count > 0) {
        selected.push(asset);
        packages.forEach(function (entry) { delete needed[handoffPackageKey(entry)]; });
      }
    }
    return Object.keys(needed).length === 0 ? selected : null;
  }

  function handoffAbortError() {
    var error = new Error("KitJS: component handoff was cancelled");
    error.name = "AbortError";
    return error;
  }

  function assertHandoffScript(script, expected) {
    if (!script || !expected || String(script.localName || "").toLowerCase() !== "script" ||
      script.getAttribute("data-kitwork-jit") !== expected.role ||
      script.getAttribute("data-kitwork-hash") !== expected.hash ||
      script.getAttribute("integrity") !== expected.integrity ||
      script.getAttribute("crossorigin") !== "anonymous" || script.crossOrigin !== "anonymous" ||
      script.getAttribute("data-kitwork-handoff") !== "" ||
      script.getAttribute("src") !== expected.url || script.src !== expected.url ||
      !script.hasAttribute("defer") || script.defer !== true || script.async === true ||
      script.hasAttribute("nomodule") || script.noModule === true) {
      throw new Error("KitJS: component handoff script does not match the sealed asset");
    }
    var type = String(script.getAttribute("type") || "").trim().toLowerCase();
    if (type && type !== "text/javascript" && type !== "application/javascript") {
      throw new Error("KitJS: component handoff requires classic JavaScript");
    }
    return true;
  }

  function createHandoffState(visit, candidate) {
    if (document[HANDOFF] !== undefined || typeof core.beginComponentHandoff !== "function") return null;
    var state = {
      visit: visit,
      candidate: candidate,
      expected: null,
      expectedNode: null,
      accepted: false,
      currentCancel: null,
      target: null,
      transaction: null,
      error: null,
      cancelled: false,
      closed: false
    };

    function fail(error) {
      state.error = error instanceof Error ? error : new Error(String(error || "KitJS: component handoff failed"));
      throw state.error;
    }

    function acceptGraph(script, graph, delivery, dynamic) {
      // A cancelled dynamic script can still finish evaluating after a newer
      // visit has installed its own bridge. It must be inert rather than
      // poisoning the newer transaction.
      if (dynamic && script !== state.expectedNode) return false;
      try {
        if (state.cancelled || !currentVisit(visit) || state.target) throw handoffAbortError();
        if (dynamic) {
          if (state.expected !== candidate.graph || state.accepted) {
            throw new Error("KitJS: unexpected component graph handoff");
          }
          assertHandoffScript(script, state.expected);
        }
        if (!sameCandidateDelivery(candidate, delivery) || !sameHandoffServices(delivery)) {
          throw new Error("KitJS: component graph changes its sealed runtime or services");
        }
        var transaction = core.beginComponentHandoff(graph, delivery);
        if (!transaction || !transaction.graph || !transaction.delivery ||
          typeof transaction.missing !== "function" ||
          typeof transaction.register !== "function" || typeof transaction.ready !== "function" ||
          typeof transaction.commit !== "function" || typeof transaction.abort !== "function") {
          throw new Error("KitJS: component handoff transaction is unavailable");
        }
        state.transaction = transaction;
        state.target = { graph: transaction.graph, delivery: transaction.delivery };
        state.accepted = true;
        return state.target;
      } catch (error) { return fail(error); }
    }

    function expectedPackages() {
      var entries = state.target && componentPackagesForAsset(state.target.graph, state.expected);
      if (!entries || !entries.length) fail(new Error("KitJS: component handoff asset has no sealed packages"));
      return entries;
    }

    function registerPackage(source, expected) {
      if (!source || source.name !== expected.name || source.version !== expected.version ||
        source.sourceHash !== expected.sourceHash || typeof source.install !== "function") {
        fail(new Error("KitJS: component handoff package identity does not match its asset"));
      }
      state.transaction.register(source.name, source.version, source.sourceHash, source.install);
    }

    var bridge = Object.freeze({
      graph: function (script, graph, delivery) {
        return acceptGraph(script, graph, delivery, true);
      },
      component: function (script, componentPackage) {
        if (script !== state.expectedNode) return false;
        try {
          if (state.cancelled || !currentVisit(visit) || !state.target || state.accepted ||
            !state.expected || state.expected.role !== "component") throw handoffAbortError();
          assertHandoffScript(script, state.expected);
          var entries = expectedPackages();
          if (entries.length !== 1) throw new Error("KitJS: individual component asset is ambiguous");
          registerPackage(componentPackage, entries[0]);
          state.accepted = true;
        } catch (error) { return fail(error); }
      },
      components: function (script, componentPackages) {
        if (script !== state.expectedNode) return false;
        try {
          if (state.cancelled || !currentVisit(visit) || !state.target || state.accepted ||
            !state.expected || state.expected.role !== "components" || !Array.isArray(componentPackages)) {
            throw handoffAbortError();
          }
          assertHandoffScript(script, state.expected);
          var entries = expectedPackages();
          if (entries.length !== componentPackages.length) {
            throw new Error("KitJS: component bundle registration is incomplete");
          }
          for (var index = 0; index < entries.length; index++) {
            registerPackage(componentPackages[index], entries[index]);
          }
          state.accepted = true;
        } catch (error) { return fail(error); }
      }
    });

    try {
      Object.defineProperty(document, HANDOFF, { value: bridge, configurable: true });
    } catch (_) { return null; }

    state.acceptCached = function (value) {
      return acceptGraph(null, value.graph, value.delivery, false);
    };
    state.cancel = function () {
      if (state.cancelled || state.closed) return;
      state.cancelled = true;
      if (state.currentCancel) state.currentCancel(handoffAbortError());
      if (state.transaction) {
        try { state.transaction.abort(); } catch (_) { /* The old active graph remains authoritative. */ }
      }
      if (document[HANDOFF] === bridge) delete document[HANDOFF];
    };
    state.close = function () {
      if (state.closed) return;
      state.closed = true;
      state.currentCancel = null;
      if (document[HANDOFF] === bridge) delete document[HANDOFF];
    };
    visit.handoff = state;
    return state;
  }

  function loadHandoffScript(visit, state, asset) {
    return new Promise(function (resolve, reject) {
      if (!currentVisit(visit) || state.cancelled || !document.head) {
        reject(handoffAbortError());
        return;
      }
      var script = document.createElement("script");
      engineHandoffScripts.add(script);
      var settled = false;
      var timer = 0;
      state.expected = asset;
      state.expectedNode = script;
      state.accepted = false;
      state.error = null;

      function finish(error) {
        if (settled) return;
        settled = true;
        if (timer) global.clearTimeout(timer);
        script.onload = null;
        script.onerror = null;
        if (script.parentNode) script.parentNode.removeChild(script);
        if (state.expectedNode === script) state.expectedNode = null;
        state.currentCancel = null;
        if (error) reject(error);
        else resolve(true);
      }

      state.currentCancel = finish;
      script.setAttribute("data-kitwork-jit", asset.role);
      script.setAttribute("data-kitwork-hash", asset.hash);
      script.setAttribute("integrity", asset.integrity);
      script.setAttribute("crossorigin", "anonymous");
      script.setAttribute("data-kitwork-handoff", "");
      script.setAttribute("defer", "");
      script.defer = true;
      script.async = false;
      script.setAttribute("src", asset.url);
      script.onload = function () {
        if (!currentVisit(visit) || state.cancelled) finish(handoffAbortError());
        else if (state.error) finish(state.error);
        else if (!state.accepted) finish(new Error("KitJS: component handoff script did not register its sealed payload"));
        else finish(null);
      };
      script.onerror = function () {
        finish(new Error("KitJS: component handoff asset could not be loaded"));
      };
      timer = global.setTimeout(function () {
        finish(new Error("KitJS: component handoff asset timed out"));
      }, HANDOFF_LOAD_TIMEOUT);
      try { document.head.appendChild(script); }
      catch (error) { finish(error); }
    });
  }

  function loadHandoffAssets(visit, state, assets, index) {
    if (index >= assets.length) return Promise.resolve(true);
    return loadHandoffScript(visit, state, assets[index]).then(function () {
      return loadHandoffAssets(visit, state, assets, index + 1);
    });
  }

  function prepareComponentHandoff(incoming, responseURL, visit) {
    var candidate = stagedCandidate(incoming, responseURL);
    if (!candidate || !stableHandoffRuntime(candidate) || !sameCandidateServiceAssets(candidate)) {
      return Promise.resolve(null);
    }
    var state = createHandoffState(visit, candidate);
    if (!state) return Promise.resolve(null);
    var cached = handoffGraphs.get(candidate.graph.hash) || null;
    var graphReady;
    try {
      if (cached) {
        state.acceptCached(cached);
        graphReady = Promise.resolve(true);
      } else graphReady = loadHandoffScript(visit, state, candidate.graph);
    } catch (error) {
      state.cancel();
      return Promise.reject(error);
    }
    return graphReady.then(function () {
      if (!state.target || !currentVisit(visit)) throw handoffAbortError();
      if (!cached) rememberHandoffGraph(candidate.graph.hash, state.target);
      var missing = state.transaction.missing();
      var assets = missingHandoffAssets(state.target.graph, state.target.delivery, missing);
      if (!assets) throw new Error("KitJS: component handoff cannot load the sealed package delta");
      return loadHandoffAssets(visit, state, assets, 0);
    }).then(function () {
      if (!currentVisit(visit) || state.cancelled || !state.transaction.ready()) throw handoffAbortError();
      var transaction = state.transaction;
      var settled = false;
      var activating = false;
      state.close();
      visit.handoff = null;
      return Object.freeze({
        graph: state.target.graph,
        activate: Object.freeze(function () {
          if (settled || activating) throw new Error("KitJS: component handoff transition is already settled");
          activating = true;
          try {
            var rollback = transaction.commit();
            if (typeof rollback !== "function") {
              throw new Error("KitJS: component handoff did not provide rollback");
            }
            settled = true;
            return rollback;
          } catch (error) {
            try { transaction.abort(); } catch (_) { /* The active graph was not changed. */ }
            settled = true;
            throw error;
          } finally {
            activating = false;
          }
        }),
        abort: Object.freeze(function () {
          if (settled) return false;
          settled = true;
          return transaction.abort();
        })
      });
    }).catch(function (error) {
      state.cancel();
      visit.handoff = null;
      throw error;
    });
  }

  function collectComponents(root, output) {
    if (!root || root.nodeType === 1 && core.ignoredForRuntime(root)) return;
    if (root.nodeType === 1 && (root.hasAttribute("data-kit-component") ||
      root.hasAttribute("data-kit-version") || root.hasAttribute("data-kit-local"))) output.push(root);
    if (!root.querySelectorAll) return;
    array(root.querySelectorAll("[data-kit-component],[data-kit-version],[data-kit-local]")).forEach(function (element) {
      if (!core.ignoredForRuntime(element)) output.push(element);
    });
    array(root.querySelectorAll("template")).forEach(function (template) {
      if (!core.ignoredForRuntime(template) && template.content) collectComponents(template.content, output);
    });
  }

  function knownComponents(incoming) {
    if (!incoming || !incoming.documentElement || !incoming.body ||
      typeof core.componentMetadata !== "function" ||
      typeof core.hasComponentDefinition !== "function") return false;
    var components = [];
    var root = incoming.documentElement;
    if (root.hasAttribute("data-kit-component") || root.hasAttribute("data-kit-version") ||
      root.hasAttribute("data-kit-local")) {
      // The document root is outside body morphing, so data-kit-ignore cannot
      // exempt its component identity from the incoming graph check.
      components.push(root);
    }
    collectComponents(incoming.body, components);
    return components.every(function (element) {
      var request = core.componentMetadata(element, false);
      return !!request && core.hasComponentDefinition(request);
    });
  }

  function knownComponentsForGraph(incoming, graph) {
    if (!incoming || !incoming.documentElement || !incoming.body || !graph || !graph.components ||
      typeof core.componentMetadataForGraph !== "function" ||
      typeof core.hasComponentDefinition !== "function") return false;
    var components = [];
    var root = incoming.documentElement;
    if (root.hasAttribute("data-kit-component") || root.hasAttribute("data-kit-version") ||
      root.hasAttribute("data-kit-local")) {
      components.push(root);
    }
    collectComponents(incoming.body, components);
    return components.every(function (element) {
      var request = core.componentMetadataForGraph(element, graph);
      return !!request && (request.lane === "managed" || core.hasComponentDefinition(request));
    });
  }

  function rootMetadata(element, name) {
    if (!element || !element.hasAttribute(name)) return null;
    return String(element.getAttribute(name) || "").trim();
  }

  function compatibleDocumentBoundary(incoming) {
    if (!incoming || !incoming.documentElement || !document.documentElement) return false;
    var current = document.documentElement;
    var next = incoming.documentElement;
    if (current.hasAttribute("data-kit-component") !== next.hasAttribute("data-kit-component")) return false;
    if (current.hasAttribute("data-kit-component")) {
      var currentRequest = core.componentMetadata(current, false);
      var nextRequest = core.componentMetadata(next, false);
      if (!currentRequest || !nextRequest || currentRequest.name !== nextRequest.name ||
        currentRequest.version !== nextRequest.version || currentRequest.lane !== nextRequest.lane) return false;
    }
    return ["data-kit-as", "data-kit-scope"].every(function (name) {
      return current.hasAttribute(name) === next.hasAttribute(name) &&
        rootMetadata(current, name) === rootMetadata(next, name);
    });
  }

  function compatibleRetains(incoming) {
    if (!incoming || !incoming.body || !document.body ||
      typeof core.validateMorphRetains !== "function") return false;
    try {
      core.validateMorphRetains(document.body, incoming.body);
      return true;
    } catch (error) {
      core.report(error);
      return false;
    }
  }

  function compatibleHandoffDocument(incoming, responseURL) {
    return compatibleContentSecurityPolicy(incoming) &&
      compatibleDocumentBoundary(incoming) && compatibleBase(incoming, responseURL) &&
      !hasActiveDocumentContent(incoming.documentElement);
  }

  function disabled(element) {
    var node = element;
    while (node && node.nodeType === 1) {
      if (node.hasAttribute("data-kit-drive") &&
        String(node.getAttribute("data-kit-drive") || "").trim().toLowerCase() === "false") return true;
      if (node === document.body) break;
      node = node.parentElement;
    }
    return false;
  }

  function eventElement(event) {
    var target = event.target;
    return target && target.nodeType === 1 ? target : target && target.parentElement;
  }

  function eligibleLink(event) {
    if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey ||
      event.shiftKey || event.altKey || !document.body) return null;
    var origin = eventElement(event);
    var link = origin && origin.closest ? origin.closest("a[href],area[href]") : null;
    if (!link || !document.body.contains(link) || disabled(link) || link.hasAttribute("download")) return null;
    var target = String(link.getAttribute("target") || "").toLowerCase();
    if (target && target !== "_self") return null;
    if ((" " + String(link.getAttribute("rel") || "").toLowerCase() + " ").indexOf(" external ") >= 0) {
      return null;
    }
    var url = sameOriginURL(link.getAttribute("href"));
    if (!url) return null;
    return url;
  }

  function formURL(form, submitter) {
    var method = submitter && submitter.getAttribute("formmethod") || form.getAttribute("method") || "get";
    if (String(method).toLowerCase() !== "get") return null;
    var target = submitter && submitter.getAttribute("formtarget") || form.getAttribute("target") || "";
    target = String(target).toLowerCase();
    if (target && target !== "_self") return null;
    var action = submitter && submitter.getAttribute("formaction") || form.getAttribute("action") || global.location.href;
    var url = sameOriginURL(action);
    if (!url) return null;

    var values;
    try { values = submitter ? new FormData(form, submitter) : new FormData(form); }
    catch (_) {
      values = new FormData(form);
      if (submitter && submitter.name && !submitter.disabled) values.append(submitter.name, submitter.value);
    }
    url.search = "";
    values.forEach(function (value, name) {
      if (typeof value === "string") url.searchParams.append(name, value);
    });
    return url;
  }

  function eligibleForm(event) {
    if (event.defaultPrevented || !document.body) return null;
    var form = event.target;
    if (!form || String(form.localName || "").toLowerCase() !== "form" ||
      !document.body.contains(form) || disabled(form)) return null;
    var submitter = event.submitter || null;
    if (submitter && disabled(submitter)) return null;
    return formURL(form, submitter);
  }

  function scrollPosition() {
    return {
      x: Number(global.scrollX || global.pageXOffset || 0),
      y: Number(global.scrollY || global.pageYOffset || 0)
    };
  }

  function historyState(state, position) {
    var next = Object.create(null);
    if (state && typeof state === "object") {
      Object.keys(state).forEach(function (key) { next[key] = state[key]; });
    }
    next.__kitjs_drive__ = { scroll: position };
    return next;
  }

  function sameScroll(left, right) {
    return !!left && !!right && left.x === right.x && left.y === right.y;
  }

  function rememberScroll(position) {
    lastSavedScroll = { x: position.x, y: position.y };
    lastSavedURL = String(global.location.href);
  }

  function writeHistory(method, state, url) {
    if (!global.history || typeof global.history[method] !== "function") return false;
    try {
      global.history[method](state, "", url);
      return true;
    } catch (_) {
      // History is unavailable for some opaque or constrained documents.
      return false;
    }
  }

  function saveScroll(position) {
    if (!global.history || typeof global.history.replaceState !== "function") return;
    position = position || scrollPosition();
    try {
      var href = String(global.location.href);
      var state = global.history.state;
      var stored = savedScroll(state);
      if (lastSavedURL === href && sameScroll(lastSavedScroll, position) && sameScroll(stored, position)) return;
      if (writeHistory("replaceState", historyState(state, position), href)) rememberScroll(position);
    } catch (_) { /* History is unavailable for some opaque documents. */ }
  }

  function scheduleScrollSave() {
    if (scrollTimer || activeVisit) return;
    scrollTimer = global.setTimeout(function () {
      scrollTimer = 0;
      saveScroll();
    }, SCROLL_SAVE_DELAY);
  }

  function cancelScrollSave() {
    if (!scrollTimer) return;
    global.clearTimeout(scrollTimer);
    scrollTimer = 0;
  }

  function flushScrollSave() {
    cancelScrollSave();
    saveScroll();
  }

  function savedScroll(state) {
    var value = state && state.__kitjs_drive__ && state.__kitjs_drive__.scroll;
    if (!value || !Number.isFinite(Number(value.x)) || !Number.isFinite(Number(value.y))) return null;
    return { x: Number(value.x), y: Number(value.y) };
  }

  function hashTarget(hash, explicit) {
    // URL.hash is an empty string for both "no fragment" and an explicit
    // trailing "#". Keep the latter mapped to the document root.
    if (!hash) return explicit ? document.documentElement : null;
    var raw = hash.slice(1);
    if (!raw) return document.documentElement;
    var target = fragmentIdentifierTarget(raw);
    if (target) return target;
    var decoded = decodeFragmentIdentifier(raw);
    if (decoded !== raw) target = fragmentIdentifierTarget(decoded);
    if (target) return target;
    return decoded.toLowerCase() === "top" ? document.documentElement : null;
  }

  function decodeFragmentIdentifier(raw) {
    try { return decodeURIComponent(raw); }
    catch (_) { /* Invalid UTF-8 uses the platform's replacement semantics below. */ }
    if (typeof global.TextDecoder !== "function" || typeof global.TextEncoder !== "function" ||
      typeof global.Uint8Array !== "function") return raw;
    try {
      var bytes = [];
      var encoder = new global.TextEncoder();
      for (var index = 0; index < raw.length;) {
        if (raw.charAt(index) === "%" && /^[0-9a-f]{2}$/i.test(raw.slice(index + 1, index + 3))) {
          bytes.push(parseInt(raw.slice(index + 1, index + 3), 16));
          index += 3;
          continue;
        }
        var nextPercent = raw.indexOf("%", index);
        var end = nextPercent < 0 ? raw.length : nextPercent;
        if (end === index) end++;
        var encoded = encoder.encode(raw.slice(index, end));
        for (var offset = 0; offset < encoded.length; offset++) bytes.push(encoded[offset]);
        index = end;
      }
      return new global.TextDecoder("utf-8").decode(new global.Uint8Array(bytes));
    } catch (_) {
      return raw;
    }
  }

  function fragmentIdentifierTarget(id) {
    var target = document.getElementById(id);
    if (target) return target;
    var named = document.getElementsByName(id);
    for (var index = 0; index < named.length; index++) {
      if (String(named[index].localName || "").toLowerCase() === "a") return named[index];
    }
    return null;
  }

  function hasFragment(url) {
    return url && String(url.href).indexOf("#") >= 0;
  }

  function preserveRequestedFragment(source, requested) {
    var href = absoluteURL(source, document.baseURI);
    if (!href) return String(source || requested && requested.href || "");
    if (hasFragment(requested) && href.indexOf("#") < 0) {
      href += requested.href.slice(requested.href.indexOf("#"));
    }
    return href;
  }

  function sameDocument(url) {
    return !!url && url.pathname === documentPath && url.search === documentSearch;
  }

  function focusRoute(url) {
    var target = hashTarget(url.hash, hasFragment(url)) ||
      document.querySelector("[autofocus],main,[role='main'],h1") || document.body;
    if (!target || typeof target.focus !== "function") return;
    var name = String(target.localName || "").toLowerCase();
    var intrinsic = /^(button|input|select|textarea)$/.test(name) ||
      (name === "a" && target.hasAttribute("href"));
    var temporary = !target.hasAttribute("tabindex") && !intrinsic;
    if (temporary) target.setAttribute("tabindex", "-1");
    try { target.focus({ preventScroll: true }); }
    catch (_) { try { target.focus(); } catch (_) { /* Non-focusable browser host. */ } }
  }

  function restoreScroll(url, position) {
    if (position) {
      try { global.scrollTo(position.x, position.y); } catch (_) { /* Non-visual browser. */ }
      return;
    }
    scrollToFragment(url);
  }

  function scrollToFragment(url) {
    var target = hashTarget(url.hash, hasFragment(url));
    if (target && typeof target.scrollIntoView === "function") target.scrollIntoView();
    else {
      try { global.scrollTo(0, 0); } catch (_) { /* Non-visual browser. */ }
    }
  }

  function hardNavigate(source) {
    global.location.assign(String(source));
  }

  function fallbackVisit(visit, source, outcome, error) {
    source = String(source || visit.url);
    var ownsCritical = beginNavigationCritical();
    if (!ownsCritical) terminateNavigationCritical();
    if (error) {
      try { core.report(error); }
      catch (_) { /* Reporting must not interrupt a terminal fallback. */ }
    }
    finishVisit(visit, outcome, source);
    // A fallback is terminal for the current document. A synchronous finish
    // listener must not start work that the pending native navigation stomps.
    terminateNavigationCritical();
    try { hardNavigate(source); }
    catch (navigationError) {
      try { core.report(navigationError); }
      catch (_) { /* Reporting must not leave the critical section armed. */ }
    }
    if (ownsCritical) endNavigationCritical(false);
    return false;
  }

  function isHTML(response) {
    var type = response.headers && response.headers.get
      ? String(response.headers.get("content-type") || "").toLowerCase()
      : "";
    return type.indexOf("text/html") >= 0 || type.indexOf("application/xhtml+xml") >= 0;
  }

  function safeHeadNode(node) {
    var name = String(node.localName || "").toLowerCase();
    if (name === "meta") return !node.hasAttribute("http-equiv") &&
      (node.hasAttribute("name") || node.hasAttribute("property"));
    if (name === "style") return node.hasAttribute("data-kitwork-jit") || node.hasAttribute("data-kit-head");
    if (name !== "link") return false;
    var allowed = {
      alternate: true, canonical: true, icon: true, manifest: true,
      stylesheet: true, "apple-touch-icon": true, "mask-icon": true
    };
    var relations = String(node.getAttribute("rel") || "").toLowerCase().split(/\s+/).filter(Boolean);
    return relations.length > 0 && relations.every(function (relation) { return allowed[relation] === true; });
  }

  function safeHeadClone(source, base, nonce) {
    var clone = document.importNode ? document.importNode(source, true) : source.cloneNode(true);
    array(clone.attributes).forEach(function (attribute) {
      if (/^on/i.test(attribute.name) || String(attribute.name).toLowerCase() === "srcdoc") {
        clone.removeAttribute(attribute.name);
      }
    });
    if (String(clone.localName || "").toLowerCase() === "link" && clone.hasAttribute("href")) {
      var href = absoluteURL(clone.getAttribute("href"), base);
      if (!href || /^(javascript|vbscript):/i.test(href)) return null;
      clone.setAttribute("href", href);
    }
    if (String(clone.localName || "").toLowerCase() === "style" && nonce !== null) clone.nonce = nonce;
    return clone;
  }

  function headSignature(node) {
    return String(node.outerHTML || "");
  }

  function reconcileHead(incoming, responseURL) {
    if (!document.head || !incoming.head) return;
    var base = incomingBase(incoming, responseURL);
    var nonceNode = document.head.querySelector("script[nonce],style[nonce]");
    var nonce = nonceNode ? String(nonceNode.nonce || nonceNode.getAttribute("nonce") || "") : null;
    var current = array(document.head.children).filter(safeHeadNode);
    var bySignature = new Map();
    current.forEach(function (node) {
      var signature = headSignature(node);
      if (!bySignature.has(signature)) bySignature.set(signature, []);
      bySignature.get(signature).push(node);
    });
    var used = new Set();
    var anchor = document.head.querySelector("script") || null;
    array(incoming.head.children).filter(safeHeadNode).forEach(function (source) {
      var clone = safeHeadClone(source, base, nonce);
      if (!clone) return;
      var signature = headSignature(clone);
      var candidates = bySignature.get(signature);
      var node = candidates && candidates.length ? candidates.shift() : clone;
      used.add(node);
      document.head.insertBefore(node, anchor);
    });
    current.forEach(function (node) {
      if (!used.has(node) && node.parentNode === document.head) document.head.removeChild(node);
    });
  }

  function reconcileDocumentAttributes(incoming) {
    ["lang", "dir"].forEach(function (name) {
      if (incoming.documentElement.hasAttribute(name)) {
        document.documentElement.setAttribute(name, incoming.documentElement.getAttribute(name));
      } else document.documentElement.removeAttribute(name);
    });
  }

  function commit(incoming, url, options) {
    // Artifact/plan compatibility is complete before title, head, history, or
    // the live body can be changed.
    if (!sameProfile(incoming, url.href) || !compatibleContentSecurityPolicy(incoming) ||
      !compatibleExecutableScripts(incoming, url.href) ||
      !knownComponents(incoming) ||
      !compatibleDocumentBoundary(incoming) || !compatibleRetains(incoming) ||
      !compatibleBase(incoming, url.href) || hasActiveDocumentContent(incoming.documentElement)) {
      return false;
    }

    if (options.leavingScroll) saveScroll(options.leavingScroll);
    // Advance the document URL only after every compatibility check, but before
    // inserting relative body resources so they resolve against the new route.
    if (options.history === "push") {
      if (!writeHistory("pushState", historyState(null, { x: 0, y: 0 }), url.href)) return false;
      rememberScroll({ x: 0, y: 0 });
    } else if (options.history === "replace") {
      var state;
      try { state = global.history.state; }
      catch (_) { return false; }
      if (!writeHistory("replaceState", historyState(state, { x: 0, y: 0 }), url.href)) return false;
      rememberScroll({ x: 0, y: 0 });
    }
    documentPath = url.pathname;
    documentSearch = url.search;
    reconcileHead(incoming, url.href);
    reconcileDocumentAttributes(incoming);
    core.morph(document.body, incoming.body);
    if (document.title !== incoming.title) document.title = incoming.title;
    focusRoute(url);
    if (options.scroll) restoreScroll(url, options.scroll);
    else {
      scrollToFragment(url);
      saveScroll();
    }
    return true;
  }

  function discardTransition(rollback, transition) {
    try {
      if (rollback) rollback();
      else if (transition && transition.abort) transition.abort();
    } catch (error) {
      try { core.report(error); }
      catch (_) { /* Transaction cleanup remains best effort. */ }
    }
  }

  function visit(source, options) {
    options = options || {};
    var url = source instanceof URL ? source : sameOriginURL(source);
    if (!url) {
      if (navigationCritical) {
        return new Promise(function (resolve, reject) {
          queueNativeAssign(source, resolve, reject);
        });
      }
      hardNavigate(source);
      return Promise.resolve(false);
    }
    if (navigationCritical) return deferVisit(url, options);

    var leavingScroll = null;
    var controller = null;
    var visitRecord = null;
    beginNavigationCritical();
    try {
      // A popstate has already activated the destination history entry.
      // Writing the still-rendered page's viewport now would corrupt it.
      if (options.history === "none") cancelScrollSave();
      else flushScrollSave();
      leavingScroll = options.history !== "none" ? scrollPosition() : null;
      if (activeVisit) cancelVisit(activeVisit);
      // pagehide may have run from a synchronous cancellation listener. Never
      // create a new visit in a document whose navigation is now terminal.
      if (navigationTerminal) {
        endNavigationCritical(false);
        return Promise.resolve(false);
      }
      controller = new AbortController();
      var sequence = ++visitSequence;
      visitRecord = {
        controller: controller,
        sequence: sequence,
        url: url.href,
        history: options.history || "push",
        finished: false
      };
      activeVisit = visitRecord;
      emitNavigation(visitRecord, "start");
    }
    catch (error) {
      endNavigationCritical(false);
      if (!visitRecord) {
        var ownsFailureCritical = beginNavigationCritical();
        terminateNavigationCritical();
        try { core.report(error); }
        catch (_) { /* Reporting must not interrupt a terminal navigation. */ }
        terminateNavigationCritical();
        try { hardNavigate(url.href); }
        catch (navigationError) {
          try { core.report(navigationError); }
          catch (_) { /* Reporting must not leave the critical section armed. */ }
        }
        if (ownsFailureCritical) endNavigationCritical(false);
        return Promise.resolve(false);
      }
      return Promise.resolve(fallbackVisit(visitRecord, url.href, "error", error));
    }
    endNavigationCritical(true);
    if (!currentVisit(visitRecord)) {
      if (!visitRecord.finished) finishVisit(visitRecord, "cancelled", visitRecord.url);
      return Promise.resolve(false);
    }

    var request;
    try {
      request = global.fetch(url.href, {
        method: "GET",
        credentials: "same-origin",
        redirect: "follow",
        signal: controller.signal,
        headers: {
          "Accept": "text/html, application/xhtml+xml",
          "X-KitJS-Drive": "1"
        }
      });
    } catch (error) {
      return Promise.resolve(fallbackVisit(visitRecord, url.href, "error", error));
    }

    return Promise.resolve(request).then(function (response) {
      if (!currentVisit(visitRecord)) return null;
      var responseURL = preserveRequestedFragment(response.url || url.href, url);
      var finalURL = sameOriginURL(responseURL);
      if (!response.ok || !isHTML(response) || !finalURL || hasContentSecurityPolicyHeader(response)) {
        fallbackVisit(visitRecord, responseURL, "fallback");
        return null;
      }
      visitRecord.url = finalURL.href;
      return responseText(response, visitRecord).then(function (sourceText) {
        if (sourceText === null || !currentVisit(visitRecord)) return null;
        return {
          document: new DOMParser().parseFromString(sourceText, "text/html"),
          url: finalURL
        };
      });
    }).then(function (loaded) {
      if (!loaded || !currentVisit(visitRecord)) return null;
      if (!compatibleExecutableScripts(loaded.document, loaded.url.href)) {
        return { loaded: loaded, transition: false };
      }
      if (sameProfile(loaded.document, loaded.url.href)) {
        return { loaded: loaded, transition: null };
      }
      if (!stagedProfile || !compatibleHandoffDocument(loaded.document, loaded.url.href)) {
        return { loaded: loaded, transition: false };
      }
      return prepareComponentHandoff(loaded.document, loaded.url.href, visitRecord).then(function (transition) {
        return { loaded: loaded, transition: transition || false };
      });
    }).then(function (prepared) {
      if (!prepared) return false;
      var loaded = prepared.loaded;
      var transition = prepared.transition;
      var rollback = null;
      if (!currentVisit(visitRecord)) {
        if (transition && transition.abort) transition.abort();
        return false;
      }
      if (transition === false) return fallbackVisit(visitRecord, loaded.url.href, "fallback");
      if (transition && (!knownComponentsForGraph(loaded.document, transition.graph) ||
        !compatibleRetains(loaded.document))) {
        transition.abort();
        return fallbackVisit(visitRecord, loaded.url.href, "fallback");
      }
      var ownsCritical = beginNavigationCritical();
      try {
        // Activate the exact graph in the same synchronous turn as Morph. No
        // observer, authored microtask, or newer navigation can see target
        // authority paired with the old document.
        if (transition) {
          rollback = transition.activate();
          // Direct terminal navigation can still invalidate the visit. Drive
          // visits triggered here are deferred until the commit is complete.
          if (!currentVisit(visitRecord)) {
            discardTransition(rollback, transition);
            endNavigationCritical(false);
            ownsCritical = false;
            return false;
          }
        }
        var committed = commit(loaded.document, loaded.url, {
          history: options.history || "push",
          scroll: options.scroll || null,
          leavingScroll: leavingScroll
        });
        if (!committed) {
          discardTransition(rollback, transition);
          endNavigationCritical(false);
          ownsCritical = false;
          return fallbackVisit(visitRecord, loaded.url.href, "fallback");
        }
        if (!currentVisit(visitRecord)) {
          endNavigationCritical(false);
          ownsCritical = false;
          return false;
        }
        finishVisit(visitRecord, "loaded", loaded.url.href);
        endNavigationCritical(true);
        ownsCritical = false;
      } catch (error) {
        discardTransition(rollback, transition);
        if (ownsCritical) endNavigationCritical(false);
        if (visitRecord.finished) return false;
        // Complete the terminal fallback in this turn. Deferred-intent promise
        // reactions must not run between a failed commit and its native load.
        return fallbackVisit(visitRecord, loaded.url.href, "error", error);
      }
      return true;
    }).catch(function (error) {
      if (visitRecord.finished) return false;
      if (controller.signal.aborted || error && error.name === "AbortError") {
        finishVisit(visitRecord, "cancelled", visitRecord.url);
        return false;
      }
      return fallbackVisit(visitRecord, visitRecord.url, "error", error);
    }).finally(function () {
      if (!visitRecord.finished) {
        finishVisit(visitRecord, controller.signal.aborted ? "cancelled" : "error", visitRecord.url);
      }
    });
  }

  function onClick(event) {
    var url = eligibleLink(event);
    if (!url) return;
    if (hasFragment(url) && sameDocument(url)) {
      // Save the leaving entry before the browser creates its native fragment
      // entry, then preserve the platform's default :target/hashchange behavior.
      if (navigationCritical) {
        event.preventDefault();
        queueNativeAssign(url.href);
        return;
      }
      if (activeVisit) {
        event.preventDefault();
        beginNavigationCritical();
        queueNativeAssign(url.href);
        cancelVisit(activeVisit);
        endNavigationCritical(true);
        return;
      }
      flushScrollSave();
      return;
    }
    event.preventDefault();
    visit(url, { history: "push" });
  }

  function onSubmit(event) {
    var url = eligibleForm(event);
    if (!url) return;
    event.preventDefault();
    visit(url, { history: "push" });
  }

  function onPopState(event) {
    cancelScrollSave();
    var url = sameOriginURL(global.location.href);
    if (!url) return;
    var position = savedScroll(event.state);
    if (sameDocument(url)) {
      if (navigationCritical) {
        queueScrollRestore(url, position);
        return;
      }
      if (activeVisit) {
        beginNavigationCritical();
        queueScrollRestore(url, position);
        cancelVisit(activeVisit);
        endNavigationCritical(true);
        return;
      }
      if (position) rememberScroll(position);
      restoreScroll(url, position);
      return;
    }
    visit(url, { history: "none", scroll: position });
  }

  function onPageHide() {
    // A cross-document popstate has already activated its destination entry,
    // while the old document can remain rendered until Drive commits. Never
    // write that old viewport into the destination during pagehide. Comparing
    // rendered and address-bar routes also covers a failed popstate fetch after
    // its active visit has already been cleared for a hard-navigation fallback.
    var url = sameOriginURL(global.location.href);
    if (url && !sameDocument(url)) cancelScrollSave();
    else flushScrollSave();
    var ownsCritical = beginNavigationCritical();
    terminateNavigationCritical();
    if (activeVisit) cancelVisit(activeVisit);
    terminateNavigationCritical();
    if (ownsCritical) endNavigationCritical(false);
  }

  function start() {
    if (started || !profileURL || typeof global.fetch !== "function" ||
      typeof global.DOMParser !== "function" || typeof global.AbortController !== "function" ||
      !global.history || typeof global.history.pushState !== "function") return false;
    if (stagedProfile && !captureLiveStagedDelivery()) return false;
    liveExecutableTopology = executableScriptTopology(document, document.baseURI, true);
    started = true;
    if (stagedProfile && core.graph && core.delivery && core.delivery.graphHash) {
      rememberHandoffGraph(core.delivery.graphHash, { graph: core.graph, delivery: core.delivery });
    }
    try {
      if ("scrollRestoration" in global.history) global.history.scrollRestoration = "manual";
    } catch (_) { /* History is unavailable for some opaque documents. */ }
    document.addEventListener("click", onClick);
    document.addEventListener("submit", onSubmit);
    global.addEventListener("popstate", onPopState);
    global.addEventListener("scroll", scheduleScrollSave, { passive: true });
    global.addEventListener("pagehide", onPageHide);
    saveScroll();
    return true;
  }

  core.startHooks.push(start);
  core.phase = "drive";
})(globalThis, document);
; (function (global, document) {
  "use strict";

  var ASSEMBLY = Symbol.for("kitjs:assembly");
  var PROFILE = Symbol.for("kitjs:profile");
  var expected = "hydrate";
  var core = document[ASSEMBLY];
  if (!core || core.phase !== "drive") {
    delete document[ASSEMBLY];
    throw new Error("KitJS: hydrate profile marker loaded out of order");
  }

  try {
    if (core.reuse) {
      var active = global.kit && global.kit[PROFILE];
      if (active !== expected) {
        if (active === "kit") {
          throw new Error("KitJS: cannot install hydrate profile over active kit profile");
        }
        throw new Error("KitJS: active runtime has no compatible hydrate profile marker");
      }
      core.profile = expected;
      return;
    }
    if (!core.kit || core.OWN.call(core.kit, PROFILE)) {
      throw new Error("KitJS: hydrate profile marker cannot be installed");
    }
    Object.defineProperty(core.kit, PROFILE, { value: expected });
    core.profile = expected;
  } catch (error) {
    delete document[ASSEMBLY];
    throw error;
  }
})(globalThis, document);
; (function (document) {
  "use strict";

  var core = document[Symbol.for("kitjs:assembly")];
  if (!core || ["events", "drive"].indexOf(core.phase) < 0) {
    throw new Error("KitJS: service registrar loaded out of order");
  }
  if (core.reuse) return;
  if (!core.kit || typeof core.validServiceName !== "function" ||
    typeof core.sealKit !== "function" || core.serviceRegistry) {
    throw new Error("KitJS: service registrar cannot be installed");
  }

  var OWN = core.OWN;
  var registry = new Map();
  var identities = new WeakMap();
  var kit = core.kit;
  var sealed = false;

  function snapshot(name, namespace) {
    var prototype = namespace && Object.getPrototypeOf(namespace);
    if (!namespace || prototype !== Object.prototype && prototype !== null ||
      Object.getOwnPropertySymbols(namespace).length) {
      throw new TypeError("KitJS: service namespace must be a plain object");
    }
    var descriptors = Object.getOwnPropertyDescriptors(namespace);
    var output = Object.create(null);
    Object.keys(descriptors).forEach(function (member) {
      if (member === "version" || core.blocked(member)) {
        throw new TypeError("KitJS: invalid service member \"" + member + "\"");
      }
      var descriptor = descriptors[member];
      if (descriptor.set || !OWN.call(descriptor, "value") && typeof descriptor.get !== "function") {
        throw new TypeError("KitJS: service members must be values or readonly getters");
      }
      if (OWN.call(descriptor, "value")) {
        Object.defineProperty(output, member, {
          value: descriptor.value,
          enumerable: descriptor.enumerable !== false
        });
      } else {
        Object.defineProperty(output, member, {
          get: descriptor.get,
          enumerable: descriptor.enumerable !== false
        });
      }
    });
    Object.defineProperty(output, "version", {
      value: core.graph.services[name]
    });
    return Object.freeze(output);
  }

  function service(name, namespace) {
    if (arguments.length !== 2) {
      throw new TypeError("KitJS: service(name, namespace) expects two arguments");
    }
    if (sealed) throw new Error("KitJS: service registrar is sealed");
    if (!core.graph) throw new Error("KitJS: services must register after the graph is installed");
    if (!core.validServiceName(name)) throw new TypeError("KitJS: invalid service name");
    if (!OWN.call(core.graph.services, name)) {
      throw new Error("KitJS: service \"" + name + "\" is not declared by the installed graph");
    }
    if (registry.has(name)) throw new Error("KitJS: service \"" + name + "\" already exists");
    var value = snapshot(name, namespace);
    Object.defineProperty(kit, name, {
      value: value,
      enumerable: true
    });
    registry.set(name, value);
    identities.set(value, name);
  }

  Object.defineProperty(kit, "service", {
    value: service,
    configurable: true
  });

  core.serviceRegistry = registry;
  core.sealServices = function () {
    if (sealed) throw new Error("KitJS: services are already sealed");
    if (!core.graph) throw new Error("KitJS: service graph is not installed");
    Object.keys(core.graph.services).forEach(function (name) {
      if (!registry.has(name)) {
        throw new Error("KitJS: service graph is missing definition \"" + name + "\"");
      }
      Object.keys(core.graph.actions[name]).forEach(function (member) {
        if (typeof registry.get(name)[member] !== "function") {
          throw new Error("KitJS: authored action \"" + name + "." + member + "\" is not callable");
        }
      });
    });
    sealed = true;
    if (!delete kit.service) throw new Error("KitJS: service registrar could not be removed");
    core.servicesSealed = true;
    return core.sealKit();
  };
  core.serviceName = function (value) { return identities.get(value) || null; };
})(document);
; (function (global, document) {
  "use strict";

  var ASSEMBLY = Symbol.for("kitjs:assembly");
  var GRAPH = Symbol.for("kitjs:graph");
  var core = document[ASSEMBLY];
  if (!core || core.phase !== "drive") throw new Error("KitJS: component graph loaded out of order");
  var services = Object.create(null);
  services["progress"] = "1.0.0";
  var components = Object.create(null);
  components["progress-bar"] = "2.0.0";
  var actions = Object.create(null);
  actions["progress"] = Object.create(null);
  var grants = Object.create(null);
  grants["progress-bar"] = Object.create(null);
  grants["progress-bar"]["progress"] = "1.0.0";
  var graph = { id: "3e62dbb117f79f702f0f674253237f271bc5a02964b3ca620c276035e339cd60", profile: "hydrate", services: services, components: components, actions: actions, grants: grants };
  if (core.reuse) {
    var installed = global.kit && global.kit[GRAPH];
    if (!installed || installed.id !== graph.id || installed.profile !== graph.profile) {
      delete document[ASSEMBLY];
      throw new Error("KitJS: installed component graph does not match this artifact");
    }
    core.graphValidated = true;
    return;
  }
  try {
    if (typeof core.installComponentGraph !== "function") throw new Error("KitJS: component graph installer is unavailable");
    core.installComponentGraph(graph);
    var kit = core.kit;
    if (!kit || kit.version !== core.version || kit.component !== core.component) throw new Error("KitJS: package facade is unavailable");
    ; (function (kit) {
;(function (global, document, kit) {
"use strict";

// KitJS service: progress@1.0.0
var listeners = new Set();
var deliveries = [];
var delivering = false;
var current = freeze({
  id: "",
  phase: "idle",
  source: "",
  url: "",
  loaded: 0,
  total: null,
  outcome: null
});

function freeze(value) {
  return Object.freeze({
    id: value.id,
    phase: value.phase,
    source: value.source,
    url: value.url,
    loaded: value.loaded,
    total: value.total,
    outcome: value.outcome
  });
}

function report(error) {
  try {
    if (typeof global.reportError === "function") {
      global.reportError(error);
      return;
    }
    if (global.console && typeof global.console.error === "function") {
      global.console.error(error);
    }
  } catch (_) { /* Reporting must not break another subscriber. */ }
}

function deliver(listener, value) {
  try { listener(value); }
  catch (error) { report(error); }
}

function publish(value) {
  var published = current = freeze(value);
  deliveries.push({
    value: published,
    subscriptions: Array.from(listeners)
  });
  if (delivering) return published;

  delivering = true;
  try {
    var index = 0;
    while (index < deliveries.length) {
      var delivery = deliveries[index];
      deliveries[index] = null;
      index++;
      delivery.subscriptions.forEach(function (subscription) {
        if (subscription.listener) deliver(subscription.listener, delivery.value);
      });
    }
  } finally {
    deliveries.length = 0;
    delivering = false;
  }
  return published;
}

function progressID(value) {
  if (typeof value === "number") {
    if (!Number.isFinite(value)) throw new TypeError("Progress id must be a non-empty string or finite number");
    return String(value);
  }
  if (typeof value !== "string" || !value) {
    throw new TypeError("Progress id must be a non-empty string or finite number");
  }
  return value;
}

function optionsOf(value) {
  if (value === undefined || value === null) value = {};
  var prototype = value && Object.getPrototypeOf(value);
  if (!value || prototype !== Object.prototype && prototype !== null) {
    throw new TypeError("Progress options must be a plain object");
  }
  if (value.source !== undefined && (typeof value.source !== "string" || !value.source)) {
    throw new TypeError("Progress source must be a non-empty string");
  }
  if (value.url !== undefined && typeof value.url !== "string") {
    throw new TypeError("Progress url must be a string");
  }
  if (value.total !== undefined && value.total !== null &&
    (typeof value.total !== "number" || !Number.isFinite(value.total) || value.total <= 0)) {
    throw new TypeError("Progress total must be a positive finite number or null");
  }
  return {
    source: value.source === undefined ? "manual" : value.source,
    url: value.url === undefined ? "" : value.url,
    total: value.total === undefined || value.total === null ? null : value.total
  };
}

function snapshot() {
  return current;
}

function subscribe(listener) {
  if (typeof listener !== "function") throw new TypeError("Progress subscriber must be a function");
  var subscription = { listener: listener };
  listeners.add(subscription);
  deliver(listener, current);
  var subscribed = true;
  return function () {
    if (!subscribed) return;
    subscribed = false;
    listeners.delete(subscription);
    subscription.listener = null;
    listener = null;
  };
}

function start(id, options) {
  id = progressID(id);
  options = optionsOf(options);
  return publish({
    id: id,
    phase: "start",
    source: options.source,
    url: options.url,
    loaded: 0,
    total: options.total,
    outcome: null
  });
}

function update(id, loaded, total) {
  id = progressID(id);
  if (current.id !== id || current.phase === "idle" || current.phase === "finish") return false;
  if (typeof loaded !== "number" || !Number.isFinite(loaded) || loaded < 0 ||
    typeof total !== "number" || !Number.isFinite(total) || total <= 0 || loaded > total) {
    throw new TypeError("Progress update expects finite values where 0 <= loaded <= total and total > 0");
  }
  return publish({
    id: current.id,
    phase: "progress",
    source: current.source,
    url: current.url,
    loaded: loaded,
    total: total,
    outcome: null
  });
}

function finish(id, outcome) {
  id = progressID(id);
  if (current.id !== id || current.phase === "idle" || current.phase === "finish") return false;
  if (outcome !== "loaded" && outcome !== "cancelled" && outcome !== "error" && outcome !== "fallback") {
    throw new TypeError("Progress outcome must be loaded, cancelled, error, or fallback");
  }
  return publish({
    id: current.id,
    phase: "finish",
    source: current.source,
    url: current.url,
    loaded: outcome === "loaded" && current.total !== null ? current.total : current.loaded,
    total: current.total,
    outcome: outcome
  });
}

function navigation(event) {
  try {
    var detail = event && event.detail;
    if (!detail || typeof detail !== "object" || typeof detail.url !== "string") return;
    if (detail.phase === "start") {
      start(detail.id, {
        source: "navigation",
        url: detail.url
      });
      return;
    }
    var id = progressID(detail.id);
    if (current.source !== "navigation" || current.id !== id) return;
    if (detail.phase === "progress") update(id, detail.loaded, detail.total);
    else if (detail.phase === "finish") finish(id, detail.outcome);
  } catch (_) { /* Untrusted document events never enter the trusted API. */ }
}

kit.service("progress", {
  snapshot: snapshot,
  subscribe: subscribe,
  start: start,
  update: update,
  finish: finish
});

document.addEventListener("kit:navigation", navigation);
})(globalThis, document, kit);
    })(kit);
    if (typeof core.sealServices !== "function") throw new Error("KitJS: service graph sealer is unavailable");
    core.sealServices();
    ; (function (kit) {
;(function () {
"use strict";

kit.component("progress-bar", {
  visible: false,
  value: null,

  init: function () {
    var scope = this;
    var hideTimer = null;

    function clearHide() {
      if (hideTimer === null) return;
      clearTimeout(hideTimer);
      hideTimer = null;
    }

    function hide() {
      scope.visible = false;
      scope.value = null;
    }

    var unsubscribe = kit.progress.subscribe(function (progress) {
      clearHide();

      if (progress.phase === "start") {
        scope.visible = true;
        scope.value = null;
        return;
      }

      if (progress.phase === "progress") {
        scope.visible = true;
        scope.value = Math.min(99, Math.floor(progress.loaded / progress.total * 100));
        return;
      }

      if (progress.phase === "finish" && progress.outcome === "loaded") {
        scope.visible = true;
        scope.value = 100;
        hideTimer = setTimeout(function () {
          hideTimer = null;
          hide();
        }, 300);
        return;
      }

      hide();
    });

    return function () {
      clearHide();
      unsubscribe();
    };
  }
});

})();
    })(kit);
  } catch (error) {
    delete document[ASSEMBLY];
    throw error;
  }
})(globalThis, document);
; (function (global, document) {
  "use strict";

  var ASSEMBLY = Symbol.for("kitjs:assembly");
  var GRAPH = Symbol.for("kitjs:graph");
  var core = document[ASSEMBLY];
  if (!core || ["events", "drive"].indexOf(core.phase) < 0) {
    throw new Error("KitJS: boot loaded out of order");
  }
  var expectedProfile = core.phase === "drive" ? "hydrate" : "kit";
  if (core.profile !== expectedProfile) {
    delete document[ASSEMBLY];
    throw new Error("KitJS: runtime profile marker is unavailable");
  }
  if (core.reuse) {
    if (global.kit && Object.prototype.hasOwnProperty.call(global.kit, GRAPH) &&
      core.graphValidated !== true) {
      delete document[ASSEMBLY];
      throw new Error("KitJS: installed component graph does not match this artifact");
    }
    delete document[ASSEMBLY];
    return;
  }
  if (typeof core.component !== "function" || typeof core.render !== "function" ||
    typeof core.installEvents !== "function" || typeof core.sealKit !== "function" || !core.kit) {
    delete document[ASSEMBLY];
    throw new Error("KitJS: incomplete runtime assembly");
  }
  try {
    if (typeof core.assertComponentGraph === "function") core.assertComponentGraph();
    if (core.serviceRegistry && core.servicesSealed !== true) {
      throw new Error("KitJS: services must be sealed before publication");
    }
    core.sealKit();
  } catch (error) {
    delete document[ASSEMBLY];
    throw error;
  }

  var kit = core.kit;

  function boot() {
    if (core.booted) return;
    core.booted = true;
    if (typeof core.prepareComponentTree === "function") core.prepareComponentTree(document);
    core.render();
    core.resetDirty();
  }

  delete document[ASSEMBLY];
  core.installEvents();
  global.kit = kit;
  core.startHooks.forEach(function (start) {
    try { start(); } catch (error) { core.report(error); }
  });
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", boot, { once: true });
  } else queueMicrotask(boot);
})(globalThis, document);
