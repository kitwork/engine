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
