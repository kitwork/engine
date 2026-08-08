"use strict";

const { createError } = require("./errors.js");
const { isBlockedMember } = require("./constants.js");
const { lex } = require("./lexer.js");
const { node, isWritableAst, parseBinding } = require("./parser.js");
const { scanTopLevel } = require("./source-scanner.js");

function decodeQuotedKey(raw) {
  const tokens = lex(raw);
  if (tokens.length !== 2 || tokens[0].type !== "literal" ||
      typeof tokens[0].value !== "string") {
    throw createError("KIT_MAP_KEY", "Invalid quoted map key '" + raw + "'", { source: raw });
  }
  return tokens[0].value;
}

function validateMapKey(rawKey, key, options) {
  options = options || {};
  if (isBlockedMember(key)) {
    throw createError("KIT_BLOCKED_MEMBER", "Map key '" + key + "' is blocked", { key });
  }

  if (rawKey[0] === "'" || rawKey[0] === '"') return;

  if (options.classMap) {
    if (!/^-?[A-Za-z0-9_./-]+$/.test(key)) {
      throw createError(
        "KIT_CLASS_KEY_QUOTE_REQUIRED",
        "Class key '" + key + "' must be quoted because it contains reserved characters",
        { key }
      );
    }
    return;
  }

  if (!/^-{0,2}[A-Za-z_][A-Za-z0-9_.-]*$/.test(key)) {
    throw createError(
      "KIT_MAP_KEY_QUOTE_REQUIRED",
      "Map key '" + key + "' must be a static bare key or quoted string",
      { key }
    );
  }
}

function parseNamedMap(source, options) {
  source = String(source == null ? "" : source);
  options = options || {};

  const entries = [];
  let segmentStart = 0;
  let colon = -1;
  const seen = Object.create(null);

  function pushEntry(end) {
    const segment = source.slice(segmentStart, end).trim();
    if (!segment) {
      segmentStart = end + 1;
      colon = -1;
      return;
    }

    if (colon < segmentStart) {
      throw createError("KIT_MAP_MISSING_COLON", "Map entry is missing ':'", {
        source,
        segment,
        position: segmentStart
      });
    }

    const rawKey = source.slice(segmentStart, colon).trim();
    const expressionSource = source.slice(colon + 1, end).trim();
    if (!rawKey || !expressionSource) {
      throw createError("KIT_MAP_ENTRY", "Map key and expression are required", {
        source,
        segment,
        position: segmentStart
      });
    }

    const key = rawKey[0] === "'" || rawKey[0] === '"'
      ? decodeQuotedKey(rawKey)
      : rawKey;

    validateMapKey(rawKey, key, options);

    if (seen[key]) {
      throw createError("KIT_MAP_DUPLICATE_KEY", "Duplicate map key '" + key + "'", {
        source,
        key,
        position: segmentStart
      });
    }
    seen[key] = true;

    entries.push({
      key,
      source: expressionSource,
      ast: parseBinding(expressionSource)
    });

    segmentStart = end + 1;
    colon = -1;
  }

  scanTopLevel(source, (character, position) => {
    if (character === ":" && colon < segmentStart) colon = position;
    else if (character === ";") pushEntry(position);
  });

  if (source.slice(segmentStart).trim()) pushEntry(source.length);
  return node("NamedMap", 0, source.length, { entries });
}

function looksLikeClassMap(source) {
  source = String(source == null ? "" : source).trim();
  if (!source) return false;
  if (source[0] === "{" || source[0] === "[" || source[0] === "`") return false;

  let firstColon = -1;
  let firstQuestion = -1;
  scanTopLevel(source, (character, position) => {
    if (character === ":" && firstColon < 0) firstColon = position;
    else if (character === "?" && source[position + 1] !== "." &&
             source[position + 1] !== "?" && firstQuestion < 0) {
      firstQuestion = position;
    }
  });

  if (firstColon < 0 || (firstQuestion >= 0 && firstQuestion < firstColon)) return false;

  const rawKey = source.slice(0, firstColon).trim();
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
  const ast = parseBinding(source);
  if (!isWritableAst(ast)) {
    throw createError("KIT_MODEL_PATH", "Writable path must be an identifier/member path", {
      source
    });
  }
  return node("WritablePath", 0, source.length, { ast });
}

function parseIdentity(source) {
  source = String(source == null ? "" : source).trim();
  if (!source) {
    throw createError("KIT_IDENTITY_EMPTY", "Identity literal cannot be empty", { source });
  }
  return node("IdentityLiteral", 0, source.length, { value: source });
}

function parseIterator(source) {
  source = String(source == null ? "" : source).trim();
  const match = /^(\$[A-Za-z_][A-Za-z0-9_]*)\s*(?:,\s*(\$[A-Za-z_][A-Za-z0-9_]*)\s*)?\s+of\s+([\s\S]+)$/.exec(source);
  if (!match) {
    throw createError(
      "KIT_ITERATOR_PARSE",
      "Iterator must use '$item, $index of collection' syntax",
      { source }
    );
  }

  return node("IteratorExpression", 0, source.length, {
    itemName: match[1],
    indexName: match[2] || "",
    collectionSource: match[3].trim(),
    collectionAst: parseBinding(match[3].trim())
  });
}

module.exports = {
  decodeQuotedKey,
  parseNamedMap,
  looksLikeClassMap,
  parseClassValue,
  parseWritablePath,
  parseIdentity,
  parseIterator
};
