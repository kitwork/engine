"use strict";

const { KitworkExpressionError, createError } = require("./errors.js");
const { MODES, normalizeMode, isThenable } = require("./constants.js");
const { lex } = require("./lexer.js");
const { parseBinding, parseAction } = require("./parser.js");
const {
  parseNamedMap,
  parseClassValue,
  parseWritablePath,
  parseIdentity,
  parseIterator
} = require("./modes.js");
const { createObjectEnvironment } = require("./object-environment.js");
const { EvaluationContext, evaluateAst, writeReference } = require("./evaluator.js");

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
