"use strict";

const { createError } = require("./errors.js");
const {
  MODES,
  RESERVED_ASSIGNMENT_ROOTS,
  hasOwn,
  isThenable,
  isBlockedMember,
  rootIdentifier
} = require("./constants.js");

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
