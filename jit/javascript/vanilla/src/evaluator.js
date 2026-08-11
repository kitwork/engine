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
