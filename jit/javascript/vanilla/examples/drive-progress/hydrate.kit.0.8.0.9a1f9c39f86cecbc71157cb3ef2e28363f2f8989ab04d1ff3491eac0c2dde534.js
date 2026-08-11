; (function (global, document) {
  "use strict";

  var VERSION = "0.8.0";
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
      document.querySelectorAll("[data-kit-component]").forEach(function (element) {
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
      if (["?.", "??", "&&", "||", "<=", ">=", "=>", "==", "!="].indexOf(operator) >= 0) {
        token("operator", operator, start);
        index += 2;
        continue;
      }
      if (["++", "--", "+=", "-=", "*=", "/=", "%=", "**", "<<", ">>"].indexOf(operator) >= 0) {
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
    function unary() {
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
      while (true) {
        if (take(".")) {
          if (current().type !== "identifier") core.syntax("expected a member name", source, current().position);
          value = make("member", {
            object: value,
            property: safeName(current().value, current().position),
            computed: false,
            chain: value.chain === true
          });
          position++;
        } else if (take("?.")) {
          if (is("(")) core.syntax("optional calls are not supported", source, current().position);
          if (is("[")) core.syntax("optional computed members are not supported", source, current().position);
          if (current().type !== "identifier") core.syntax("expected a member name", source, current().position);
          value = make("member", {
            object: value,
            property: safeName(current().value, current().position),
            computed: false,
            chain: true
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
            chain: value.chain === true
          });
        } else if (take("(")) {
          value = make("call", {
            callee: value,
            args: nested(argumentsList),
            chain: value.chain === true
          });
        } else break;
      }
      return value;
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
        if (!action && name.charAt(0) === "$") {
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
  var activeOwnership = null;
  var activeInvocation = null;

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
      resolveAlias: function (name) {
        if (aliases.has(name)) return aliases.get(name);
        var current = core.resolveAlias(name);
        aliases.set(name, current.scope);
        return current.scope;
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

  function member(owner, key, resolver) {
    var inheritedOwner = resultOwner(owner);
    var alias = owner && aliasRefs.get(owner);
    owner = resolveAliasValue(owner, resolver);
    var resolvedOwner = alias ? core.scopeRecords.get(owner) : inheritedOwner;
    if (rootOf(resolver).mode !== "action") resolvedOwner = null;
    key = core.memberKey(key);
    if (key === core.INVALID_MEMBER) throw new TypeError("KitJS: invalid or blocked member key");
    if (owner === null || owner === undefined) return undefined;
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

  function evaluate(ast, resolver, budget) {
    if (++budget.nodes > NODE_LIMIT) throw new Error("KitJS: expression budget exceeded");
    var type = ast.type;
    if (type === "literal") return ast.value;
    if (type === "identifier") return resolver.get(ast.name);
    if (type === "group") return evaluate(ast.value, resolver, budget);
    if (type === "array") {
      return ast.items.map(function (item) { return evaluate(item, resolver, budget); });
    }
    if (type === "object") {
      var object = Object.create(null);
      ast.entries.forEach(function (entry) {
        if (core.blocked(entry.key)) throw new TypeError("KitJS: blocked object key");
        object[entry.key] = evaluate(entry.value, resolver, budget);
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
      return resolver.set(ast.name, evaluate(ast.value, resolver, budget));
    }
    if (type === "unary") {
      var unary = evaluate(ast.value, resolver, budget);
      return ast.operator === "!" ? !unary : ast.operator === "-" ? -unary : +unary;
    }
    if (type === "logical") {
      var logical = evaluate(ast.left, resolver, budget);
      return ast.operator === "&&" ?
        (logical ? evaluate(ast.right, resolver, budget) : logical) :
        (logical ? logical : evaluate(ast.right, resolver, budget));
    }
    if (type === "coalesce") {
      var nullable = evaluate(ast.left, resolver, budget);
      return nullable === null || nullable === undefined ? evaluate(ast.right, resolver, budget) : nullable;
    }
    if (type === "conditional") {
      return evaluate(ast.condition, resolver, budget) ?
        evaluate(ast.yes, resolver, budget) : evaluate(ast.no, resolver, budget);
    }
    if (type === "binary") {
      var left = evaluate(ast.left, resolver, budget);
      var right = evaluate(ast.right, resolver, budget);
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
      var owner = evaluate(ast.object, resolver, budget);
      if (owner === null || owner === undefined) return undefined;
      var key = ast.computed ? evaluate(ast.property, resolver, budget) : ast.property;
      return member(owner, key, resolver);
    }
    if (type === "call") {
      var receiver;
      var callable;
      var methodName;
      var callOwner;
      var startedAt = activeOwnership ? activeOwnership.stamp : 0;
      if (ast.callee.type === "member") {
        receiver = evaluate(ast.callee.object, resolver, budget);
        receiver = resolveAliasValue(receiver, resolver);
        if (receiver === null || receiver === undefined) return undefined;
        methodName = ast.callee.computed ?
          evaluate(ast.callee.property, resolver, budget) : ast.callee.property;
        methodName = core.memberKey(methodName);
        if (methodName === core.INVALID_MEMBER) throw new TypeError("KitJS: invalid or blocked member key");
        if (!hasMethod(receiver, methodName)) return undefined;
        callOwner = resultOwner(receiver) || core.scopeRecords.get(receiver) || rootOf(resolver).owner;
      } else {
        callable = evaluate(ast.callee, resolver, budget);
        if (typeof callable !== "function") return undefined;
        callOwner = lambdas.has(callable) ? lambdas.get(callable).owner : rootOf(resolver).owner;
      }
      var args = ast.args.map(function (argument) { return evaluate(argument, resolver, budget); });
      if (++budget.depth > CALL_LIMIT) {
        budget.depth--;
        throw new Error("KitJS: expression call depth exceeded");
      }
      var previousInvocation = activeInvocation;
      activeInvocation = { budget: budget, resolver: resolver };
      try {
        var result = ast.callee.type === "member" ?
          method(receiver, methodName, args) : callable.apply(resolver.thisValue, args);
        return rootOf(resolver).mode === "action" ? markCallResult(result, callOwner, startedAt) : result;
      } finally {
        activeInvocation = previousInvocation;
        budget.depth--;
      }
    }
    throw new Error("KitJS: invalid private expression node");
  }

  function compile(source, mode) {
    source = typeof source === "string" ? source.trim() : "";
    mode = mode === "action" ? "action" : "binding";
    var key = mode + "\u0000" + source;
    if (core.compiled.has(key)) return core.compiled.get(key);
    if (!source) core.syntax("empty expression", source, 0);
    var ast = core.parse(core.lex(source), source, mode);
    var read = function (scope, locals, observeResult) {
      var root = mode === "action" ? actionResolver(scope) : directResolver(scope);
      var resolver = locals ? contextResolver(root, locals) : root;
      var results = [];
      var previousOwnership = activeOwnership;
      if (mode === "action") activeOwnership = { owners: new WeakMap(), stamp: 0 };
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
; (function (document) {
  "use strict";

  var core = document[Symbol.for("kitjs:assembly")];
  if (!core || core.phase !== "evaluator") throw new Error("KitJS: component loaded out of order");
  if (core.reuse) { core.phase = "component"; return; }

  var OWN = core.OWN;
  var COMPONENTS = "[data-kit-component]";
  var VERSIONED = "[data-kit-component],[data-kit-version]";
  var ALIASES = "[data-kit-as]";
  var aliases = new WeakMap();
  var metadata = new WeakMap();
  var cleanupObserver = null;
  var cleanupOwners = 0;
  var removedRoots = new Set();
  var removalQueued = false;
  var retainValidity = new WeakMap();
  var retainStructureValidity = new WeakMap();
  var retainReports = new WeakMap();
  var COMPONENT_NAME = /^[A-Za-z_$][A-Za-z0-9_$.-]*$/;
  var SERVICE_NAME = /^[A-Za-z][A-Za-z0-9_.-]*$/;
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

  function ownCleanup(current, cleanup) {
    current.cleanup = cleanup;
    if (!connectedHere(current.host)) {
      queueRemovedRoot(current.host);
      return;
    }
    current.ownsCleanup = true;
    cleanupOwners++;
    startCleanupObserver();
  }

  function copyValue(value, seen) {
    if (value === null || typeof value !== "object") return value;
    var prototype = Object.getPrototypeOf(value);
    if (!Array.isArray(value) && prototype !== Object.prototype && prototype !== null) {
      throw new TypeError("KitJS: component state must contain only plain objects and arrays");
    }
    if (seen.has(value)) throw new TypeError("KitJS: circular component state is not supported");
    seen.add(value);
    var output = Array.isArray(value) ? [] : Object.create(prototype);
    Object.keys(value).forEach(function (name) {
      if (core.blocked(name)) throw new TypeError("KitJS: blocked component state key \"" + name + "\"");
      output[name] = copyValue(value[name], seen);
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

  function installComponentGraph(source) {
    if (core.graph) throw new Error("KitJS: component graph is already installed");
    if (core.booted || core.phase !== "events" && core.phase !== "drive") {
      throw new Error("KitJS: component graph must be installed immediately before boot");
    }
    var prototype = source && Object.getPrototypeOf(source);
    if (!source || prototype !== Object.prototype && prototype !== null ||
      Object.getOwnPropertySymbols(source).length) graphError("expected a plain object");
    var id = source.id;
    var profile = source.profile;
    var services = source.services;
    var components = source.components;
    if (typeof id !== "string" || !GRAPH_ID.test(id)) graphError("invalid id");
    if (profile !== "kit" && profile !== "hydrate") graphError("invalid profile");
    if (profile !== (core.phase === "drive" ? "hydrate" : "kit")) {
      graphError("profile does not match the assembled runtime");
    }
    prototype = services && Object.getPrototypeOf(services);
    if (!services || prototype !== Object.prototype && prototype !== null ||
      Object.getOwnPropertySymbols(services).length) graphError("services must be a plain object");
    prototype = components && Object.getPrototypeOf(components);
    if (!components || prototype !== Object.prototype && prototype !== null ||
      Object.getOwnPropertySymbols(components).length) graphError("components must be a plain object");

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
    if (core.serviceRegistry) {
      core.serviceRegistry.forEach(function (_, name) {
        if (!OWN.call(serviceManifest, name)) {
          graphError("registered service \"" + name + "\" is not declared");
        }
      });
    }

    var componentManifest = Object.create(null);
    Object.keys(components).forEach(function (name) {
      var version = components[name];
      if (!validComponentName(name)) graphError("invalid component name \"" + name + "\"");
      if (!validVersion(version)) graphError("invalid version for component \"" + name + "\"");
      componentManifest[name] = version;
    });
    core.registry.forEach(function (_, name) {
      if (!OWN.call(componentManifest, name)) {
        graphError("registered component \"" + name + "\" is not declared");
      }
    });
    Object.freeze(serviceManifest);
    Object.freeze(componentManifest);
    var graph = Object.freeze({
      id: id,
      profile: profile,
      services: serviceManifest,
      components: componentManifest
    });
    Object.defineProperty(core.kit, Symbol.for("kitjs:graph"), { value: graph });
    core.graph = graph;
  }

  function metadataError(element, entry, message, shouldReport) {
    entry.value = null;
    entry.error = message;
    if (shouldReport && !entry.reported) {
      entry.reported = true;
      core.report(new TypeError("KitJS: " + message));
    }
    metadata.set(element, entry);
    return null;
  }

  // Undefined means this element carries no component metadata. Null means its
  // metadata is invalid. A record means it is a valid component host.
  function componentMetadata(element, shouldReport) {
    if (!element || element.nodeType !== 1 || !element.hasAttribute) return undefined;
    var hasComponent = element.hasAttribute("data-kit-component");
    var hasVersion = element.hasAttribute("data-kit-version");
    if (!hasComponent && !hasVersion) return undefined;
    var componentSource = hasComponent ? element.getAttribute("data-kit-component") : null;
    var versionSource = hasVersion ? element.getAttribute("data-kit-version") : null;
    var entry = metadata.get(element);
    if (entry && entry.componentSource === componentSource && entry.versionSource === versionSource) {
      if (shouldReport && entry.error && !entry.reported) {
        entry.reported = true;
        core.report(new TypeError("KitJS: " + entry.error));
      }
      return entry.value;
    }
    entry = {
      componentSource: componentSource,
      versionSource: versionSource,
      value: undefined,
      error: "",
      reported: false
    };
    if (!hasComponent) {
      return metadataError(element, entry, "data-kit-version requires a component host", shouldReport);
    }
    var name = String(componentSource || "").trim();
    if (name.indexOf("@") >= 0) {
      return metadataError(element, entry,
        "inline component versions are not supported; use data-kit-version", shouldReport);
    }
    if (!validComponentName(name)) {
      return metadataError(element, entry, "invalid component name \"" + name + "\"", shouldReport);
    }
    var version = null;
    if (hasVersion) {
      version = String(versionSource || "").trim();
      if (!validVersion(version)) {
        return metadataError(element, entry,
          "data-kit-version must be an exact semantic version", shouldReport);
      }
      if (!core.graph || !OWN.call(core.graph.components, name)) {
        return metadataError(element, entry,
          "component \"" + name + "\" is not present in the installed graph", shouldReport);
      }
      if (core.graph.components[name] !== version) {
        return metadataError(element, entry,
          "component \"" + name + "\" requires " + version +
          " but the installed graph provides " + core.graph.components[name], shouldReport);
      }
    }
    entry.value = { name: name, version: version };
    metadata.set(element, entry);
    return entry.value;
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
    if (root.nodeType === 1 && root.matches(VERSIONED)) componentMetadata(root, true);
    root.querySelectorAll(VERSIONED).forEach(function (element) {
      componentMetadata(element, true);
    });
    root.querySelectorAll("template").forEach(function (template) {
      if (template.content) prepareComponentTree(template.content, true);
    });
    if (!nested) inspectRetains(root, true, false);
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
    Object.keys(core.graph.components).forEach(function (name) {
      if (!core.registry.has(name)) {
        throw new Error("KitJS: component graph is missing definition \"" + name + "\"");
      }
    });
  }

  function component(name, definition) {
    if (arguments.length !== 2) throw new TypeError("KitJS: component(name, definition) expects two arguments");
    if (typeof name !== "string" || !COMPONENT_NAME.test(name)) {
      throw new TypeError("KitJS: invalid component name");
    }
    if (core.blocked(name)) throw new TypeError("KitJS: blocked component name");
    if (core.graph && !OWN.call(core.graph.components, name)) {
      throw new Error("KitJS: component \"" + name + "\" is not declared by the installed graph");
    }
    var prototype = definition && Object.getPrototypeOf(definition);
    if (!definition || prototype !== Object.prototype && prototype !== null) {
      throw new TypeError("KitJS: component definition must be a plain object");
    }
    if (core.registry.has(name)) throw new Error("KitJS: component \"" + name + "\" already exists");
    core.registry.set(name, snapshot(definition));
    if (core.booted) core.invalidate();
  }

  function createInstance(descriptors, host, request) {
    var own = {};
    Object.keys(descriptors).forEach(function (name) {
      own[name] = Object.assign({}, descriptors[name]);
      if (name !== "init" && OWN.call(own[name], "value")) {
        own[name].value = copyValue(own[name].value, new WeakSet());
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
      cleanup: null,
      ownsCleanup: false,
      initialized: false,
      rendered: false,
      disposed: false,
      structures: undefined,
      observations: null,
      captures: new WeakMap(),
      componentIdentity: Object.freeze({
        name: request.name,
        version: request.version,
        alias: host.hasAttribute("data-kit-as") ? host.getAttribute("data-kit-as") : null
      })
    };
    var scope = new Proxy(target, {
      set: function (object, name, value, receiver) {
        if (core.blocked(String(name))) return false;
        var before = Reflect.get(object, name, receiver);
        var success = Reflect.set(object, name, value, receiver);
        if (success && !core.equal(before, Reflect.get(object, name, receiver))) core.invalidate(current);
        return success;
      },
      deleteProperty: function (object, name) {
        if (core.blocked(String(name))) return false;
        var success = Reflect.deleteProperty(object, name);
        if (success) core.invalidate(current);
        return success;
      }
    });
    current.scope = scope;
    core.scopeRecords.set(scope, current);
    return current;
  }

  function nearest(element) {
    while (element) {
      if (element.nodeType === 1 && element.hasAttribute("data-kit-component")) return element;
      element = element.parentElement;
    }
    return null;
  }
  function invalidRetainHost(element) {
    if (!element || !element.hasAttribute("data-kit-retain")) return false;
    if (!retainValidity.has(element)) {
      inspectRetains(connectedHere(element) ? document : element, true, false);
    }
    return retainValidity.get(element) === true;
  }
  function ensureComponent(element) {
    var current = core.scopes.get(element);
    if (current) return current.failed ? null : current;
    if (invalidRetainHost(element)) {
      core.scopes.set(element, { host: element, failed: true, disposed: false });
      return null;
    }
    var request = componentMetadata(element, true);
    if (!request) {
      core.scopes.set(element, { host: element, failed: true, disposed: false });
      return null;
    }
    var descriptors = core.registry.get(request.name);
    if (!descriptors) return null;
    try {
      current = createInstance(descriptors, element, request);
      core.scopes.set(element, current);
      return current;
    } catch (error) {
      core.report(error);
      core.scopes.set(element, { host: element, failed: true, disposed: false });
      return null;
    }
  }
  function scopeRecordFor(element) {
    var boundary = nearest(element);
    return boundary ? ensureComponent(boundary) : null;
  }
  function ownedElements(current, selector) {
    var output = [];
    var host = current && current.host;
    if (!host || current.disposed || !host.isConnected) return output;
    if (host.matches(selector)) output.push(host);
    var walker = document.createTreeWalker(host, 1, {
      acceptNode: function (element) {
        if (element.hasAttribute("data-kit-component")) return 2;
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
      var initialized = current.init.call(current.scope);
      if (typeof initialized === "function") ownCleanup(current, initialized);
      else core.observe(initialized, current);
    }
    catch (error) { core.report(error); }
  }
  function componentElements(root) {
    var output = [];
    root = root && root.querySelectorAll ? root : document;
    if (root.nodeType === 1 && root.matches(COMPONENTS)) output.push(root);
    root.querySelectorAll(COMPONENTS).forEach(function (element) { output.push(element); });
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
    var cleanup = current.cleanup;
    var scope = current.scope;
    current.cleanup = null;
    if (current.ownsCleanup) {
      current.ownsCleanup = false;
      cleanupOwners--;
      stopCleanupObserver();
    }
    if (typeof cleanup === "function") {
      try { cleanup.call(scope); }
      catch (error) { core.report(error); }
    }
    if (current.observations) current.observations.clear();
    if (current.scope) core.scopeRecords.delete(current.scope);
    current.host = null;
    current.scope = null;
    current.init = null;
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
  core.componentMetadata = componentMetadata;
  core.inspectRetains = function (root) { return inspectRetains(root, false, true); };
  core.invalidRetainStructure = function (element) {
    if (!retainStructureValidity.has(element)) {
      inspectRetains(connectedHere(element) ? document : element, true, false);
    }
    return retainStructureValidity.get(element) === true;
  };
  core.prepareComponentTree = prepareComponentTree;
  core.assertComponentGraph = assertComponentGraph;
  core.ensureComponent = ensureComponent;
  core.ownerFor = nearest;
  core.scopeRecordFor = scopeRecordFor;
  core.ownedElements = ownedElements;
  core.initialize = initialize;
  core.liveComponents = liveComponents;
  core.disposeComponent = disposeComponent;
  core.validAlias = validAlias;
  core.validServiceName = validServiceName;
  core.resolveAlias = resolveAlias;
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
  "component version as retain ignore text show bind class model if for key".split(" ").forEach(function (name) {
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
    if (!current || current.disposed || !current.host || !current.host.isConnected) return [];
    core.initialize(current);
    var structuresChanged = core.reconcileStructures && core.reconcileStructures(current);
    core.prepareHooks.forEach(function (prepare) { prepare(current); });
    if (!structuresChanged) return [];
    return core.liveComponents(current.host).filter(function (candidate) {
      return candidate !== current && !candidate.rendered;
    });
  }
  function renderElement(element) {
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
          if (ancestor.hasAttribute("data-kit-component")) depth++;
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
      if (!current || current.disposed || !current.host || !current.host.isConnected || pending.has(current)) return;
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
        children.forEach(enqueue);
      }
    } finally {
      core.renderPending = null;
    }
  }
  function executeAttribute(element, name, locals) {
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
  if (!core || core.phase !== "class") throw new Error("KitJS: model loaded out of order");
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
    if (!/^[A-Za-z_$][A-Za-z0-9_$]*$/.test(source) || core.blocked(source)) {
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
    if (element && element.hasAttribute && element.hasAttribute("data-kit-model")) composing.add(element);
  };
  core.modelCompositionEnd = function (element) {
    if (!element) return;
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
    return !core.componentMetadata || core.componentMetadata(element, true) !== null;
  }

  function eventElement(event) {
    var target = event.target;
    return target && target.nodeType === 1 ? target : target && target.parentElement;
  }

  function safeEvent(element, name) {
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
    var record = core.records.get(element);
    if (!validMetadata(element)) {
      if (!record) record = core.elementRecord(element);
      if (element.hasAttribute("data-kit-component")) record.invalid["data-kit-component"] = true;
      if (element.hasAttribute("data-kit-version")) record.invalid["data-kit-version"] = true;
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
    if (!root) return;
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
      if (!target) return;
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

  function directiveIdentity(element) {
    var attributes = [];
    element.getAttributeNames().forEach(function (name) {
      if (name.toLowerCase().indexOf("data-kit-") !== 0) return;
      attributes.push(name.toLowerCase() + "\u0000" + element.getAttribute(name));
    });
    attributes.sort();
    return attributes.join("\u0001");
  }

  function retainKey(element) {
    if (!element || element.nodeType !== ELEMENT || !element.hasAttribute(RETAIN)) return "";
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
        mounted.version === currentEntry.request.version && mounted.alias === canonicalAlias(current)) &&
      current.namespaceURI === incoming.namespaceURI && current.localName === incoming.localName &&
      currentEntry.request.name === incomingEntry.request.name &&
      currentEntry.request.version === incomingEntry.request.version &&
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
      while (ancestor && ancestor !== incomingRoot) {
        context.incomingAncestors.add(ancestor);
        ancestor = parentElement(ancestor);
      }
    });
    return context;
  }

  function componentCompatible(current, incoming) {
    var currentName = current.getAttribute("data-kit-component");
    var incomingName = incoming.getAttribute("data-kit-component");
    if (currentName === null && incomingName === null) return true;
    return currentName !== null && incomingName !== null &&
      currentName === incomingName &&
      current.getAttribute("data-kit-as") === incoming.getAttribute("data-kit-as") &&
      directiveIdentity(current) === directiveIdentity(incoming);
  }

  function compatible(current, incoming, context) {
    if (!current || !incoming || current.nodeType !== incoming.nodeType) return false;
    if (current.nodeType === TEXT || current.nodeType === COMMENT) return true;
    if (current.nodeType !== ELEMENT) return false;
    if (current.namespaceURI !== incoming.namespaceURI || current.localName !== incoming.localName) return false;
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
      var replacement = incoming.cloneNode(true);
      disposeNode(current);
      current.parentNode.replaceChild(replacement, current);
      return replacement;
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
  var profileURL = profileScript && profileScript.src ? absoluteURL(profileScript.src, document.baseURI) : null;
  var profilePlan = profileScript && profileScript.hasAttribute("data-kitwork-plan")
    ? profileScript.getAttribute("data-kitwork-plan")
    : null;
  var profileIntegrity = profileScript && profileScript.hasAttribute("integrity")
    ? profileScript.getAttribute("integrity")
    : null;
  var activeVisit = null;
  var visitSequence = 0;
  var started = false;
  var scrollFrame = 0;
  var NAVIGATION_EVENT = "kit:navigation";
  var ACTIVE_ELEMENTS = {
    applet: true,
    embed: true,
    fencedframe: true,
    frame: true,
    iframe: true,
    object: true,
    portal: true
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
    try { visit.controller.abort(); } catch (_) { /* AbortController is best effort. */ }
    return finishVisit(visit, "cancelled", visit.url);
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
    if (!profileURL || !incoming || !incoming.body) return false;
    var base = incomingBase(incoming, responseURL);
    var matches = array(incoming.querySelectorAll("script[src]")).filter(function (script) {
      if (absoluteURL(script.getAttribute("src"), base) !== profileURL) return false;
      var type = String(script.getAttribute("type") || "").trim().toLowerCase();
      if (script.hasAttribute("nomodule") || type && type !== "text/javascript" && type !== "application/javascript") {
        return false;
      }
      if (profileIntegrity !== null && (!script.hasAttribute("integrity") ||
        script.getAttribute("integrity") !== profileIntegrity)) return false;
      return profilePlan === null || script.hasAttribute("data-kitwork-plan") &&
        script.getAttribute("data-kitwork-plan") === profilePlan;
    });
    return matches.length === 1;
  }

  function collectComponents(root, output) {
    if (!root) return;
    if (root.nodeType === 1 && (root.hasAttribute("data-kit-component") ||
      root.hasAttribute("data-kit-version"))) output.push(root);
    if (!root.querySelectorAll) return;
    array(root.querySelectorAll("[data-kit-component],[data-kit-version]")).forEach(function (element) {
      output.push(element);
    });
    array(root.querySelectorAll("template")).forEach(function (template) {
      if (template.content) collectComponents(template.content, output);
    });
  }

  function knownComponents(incoming) {
    if (!incoming || !incoming.body || !core.registry || typeof core.registry.has !== "function" ||
      typeof core.componentMetadata !== "function") return false;
    var components = [];
    collectComponents(incoming.body, components);
    return components.every(function (element) {
      var request = core.componentMetadata(element, false);
      return !!request && core.registry.has(request.name);
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
    if (url.pathname === global.location.pathname && url.search === global.location.search && url.hash) {
      return null;
    }
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

  function saveScroll() {
    if (!global.history || typeof global.history.replaceState !== "function") return;
    try {
      global.history.replaceState(historyState(global.history.state, scrollPosition()), "", global.location.href);
    } catch (_) { /* History is unavailable for some opaque documents. */ }
  }

  function scheduleScrollSave() {
    if (scrollFrame || activeVisit) return;
    var frame = global.requestAnimationFrame || function (callback) { return global.setTimeout(callback, 0); };
    scrollFrame = frame(function () {
      scrollFrame = 0;
      saveScroll();
    });
  }

  function cancelScrollSave() {
    if (!scrollFrame) return;
    if (typeof global.cancelAnimationFrame === "function") global.cancelAnimationFrame(scrollFrame);
    else global.clearTimeout(scrollFrame);
    scrollFrame = 0;
  }

  function savedScroll(state) {
    var value = state && state.__kitjs_drive__ && state.__kitjs_drive__.scroll;
    if (!value || !Number.isFinite(Number(value.x)) || !Number.isFinite(Number(value.y))) return null;
    return { x: Number(value.x), y: Number(value.y) };
  }

  function hashTarget(hash) {
    if (!hash) return null;
    var id;
    try { id = decodeURIComponent(hash.slice(1)); }
    catch (_) { id = hash.slice(1); }
    if (!id) return document.documentElement;
    return document.getElementById(id) || (document.getElementsByName(id)[0] || null);
  }

  function focusRoute(url) {
    var target = hashTarget(url.hash) || document.querySelector("[autofocus],main,[role='main'],h1") || document.body;
    if (!target || typeof target.focus !== "function") return;
    var temporary = !target.hasAttribute("tabindex") && !/^(a|button|input|select|textarea)$/i.test(target.localName || "");
    if (temporary) target.setAttribute("tabindex", "-1");
    try { target.focus({ preventScroll: true }); }
    catch (_) { try { target.focus(); } catch (_) { /* Non-focusable browser host. */ } }
  }

  function restoreScroll(url, position) {
    var frame = global.requestAnimationFrame || function (callback) { return global.setTimeout(callback, 0); };
    frame(function () {
      if (position) {
        try { global.scrollTo(position.x, position.y); } catch (_) { /* Non-visual browser. */ }
        return;
      }
      var target = hashTarget(url.hash);
      if (target && typeof target.scrollIntoView === "function") target.scrollIntoView();
      else {
        try { global.scrollTo(0, 0); } catch (_) { /* Non-visual browser. */ }
      }
    });
  }

  function hardNavigate(source) {
    global.location.assign(String(source));
  }

  function fallbackVisit(visit, source, outcome, error) {
    source = String(source || visit.url);
    if (error) core.report(error);
    finishVisit(visit, outcome, source);
    try { hardNavigate(source); }
    catch (navigationError) { core.report(navigationError); }
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
    if (!sameProfile(incoming, url.href) || !knownComponents(incoming) || !compatibleRetains(incoming) ||
      !compatibleBase(incoming, url.href) || hasActiveDocumentContent(incoming.documentElement)) {
      return false;
    }

    if (options.leavingScroll) {
      global.history.replaceState(
        historyState(global.history.state, options.leavingScroll),
        "",
        global.location.href
      );
    }
    // Advance the document URL only after every compatibility check, but before
    // inserting relative body resources so they resolve against the new route.
    if (options.history === "push") {
      global.history.pushState(historyState(null, { x: 0, y: 0 }), "", url.href);
    } else if (options.history === "replace") {
      global.history.replaceState(historyState(global.history.state, { x: 0, y: 0 }), "", url.href);
    }
    reconcileHead(incoming, url.href);
    reconcileDocumentAttributes(incoming);
    core.morph(document.body, incoming.body);
    if (document.title !== incoming.title) document.title = incoming.title;
    focusRoute(url);
    restoreScroll(url, options.scroll || null);
    return true;
  }

  function visit(source, options) {
    options = options || {};
    var url = source instanceof URL ? source : sameOriginURL(source);
    if (!url) {
      hardNavigate(source);
      return Promise.resolve(false);
    }
    cancelScrollSave();
    var leavingScroll = options.history !== "none" ? scrollPosition() : null;
    if (activeVisit) cancelVisit(activeVisit);
    var controller = new AbortController();
    var sequence = ++visitSequence;
    var visitRecord = {
      controller: controller,
      sequence: sequence,
      url: url.href,
      finished: false
    };
    activeVisit = visitRecord;
    try { emitNavigation(visitRecord, "start"); }
    catch (error) {
      return Promise.resolve(fallbackVisit(visitRecord, url.href, "error", error));
    }
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
      var finalURL = sameOriginURL(response.url || url.href);
      if (!response.ok || !isHTML(response) || !finalURL) {
        fallbackVisit(visitRecord, response.url || url.href, "fallback");
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
      if (!loaded || !currentVisit(visitRecord)) return false;
      var committed = commit(loaded.document, loaded.url, {
        history: options.history || "push",
        scroll: options.scroll || null,
        leavingScroll: leavingScroll
      });
      if (!committed) return fallbackVisit(visitRecord, loaded.url.href, "fallback");
      finishVisit(visitRecord, "loaded", loaded.url.href);
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
    if (url) visit(url, { history: "none", scroll: savedScroll(event.state) });
  }

  function onPageHide() {
    saveScroll();
    if (activeVisit) cancelVisit(activeVisit);
  }

  function start() {
    if (started || !profileURL || typeof global.fetch !== "function" ||
      typeof global.DOMParser !== "function" || typeof global.AbortController !== "function" ||
      !global.history || typeof global.history.pushState !== "function") return false;
    started = true;
    if ("scrollRestoration" in global.history) global.history.scrollRestoration = "manual";
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
    });
    sealed = true;
    if (!delete kit.service) throw new Error("KitJS: service registrar could not be removed");
    core.servicesSealed = true;
    return core.sealKit();
  };
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
  var graph = { id: "e1048241708a83972a34414d877fb1f80c645c998889fc20314ff60fdc833b79", profile: "hydrate", services: services, components: components };
  if (core.reuse) {
    var installed = global.kit && global.kit[GRAPH];
    if (!installed || installed.id !== graph.id || installed.profile !== graph.profile) {
      delete document[ASSEMBLY];
      throw new Error("KitJS: installed component graph does not match this artifact");
    }
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
  var core = document[ASSEMBLY];
  if (!core || ["events", "drive"].indexOf(core.phase) < 0) {
    throw new Error("KitJS: boot loaded out of order");
  }
  if (core.reuse) {
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
