"use strict";

const { createError } = require("./errors.js");

function skipQuotedSource(source, index, quote) {
  index++;
  while (index < source.length) {
    const character = source[index++];
    if (character === "\\") {
      if (index < source.length) index++;
      continue;
    }
    if (character === quote) return index;
  }
  throw createError("KIT_PARSE_UNTERMINATED_STRING", "Unterminated string literal", {
    source,
    position: index
  });
}

function skipTemplateSource(source, index) {
  index++;
  while (index < source.length) {
    const character = source[index];
    if (character === "\\") {
      index += 2;
      continue;
    }
    if (character === "`") return index + 1;
    if (character === "$" && source[index + 1] === "{") {
      index = skipBracedExpressionSource(source, index + 2);
      continue;
    }
    index++;
  }
  throw createError("KIT_PARSE_UNTERMINATED_TEMPLATE", "Unterminated template literal", {
    source,
    position: index
  });
}

function skipBracedExpressionSource(source, index) {
  let depth = 1;
  while (index < source.length) {
    const character = source[index];
    if (character === "'" || character === '"') {
      index = skipQuotedSource(source, index, character);
      continue;
    }
    if (character === "`") {
      index = skipTemplateSource(source, index);
      continue;
    }
    if (character === "{") {
      depth++;
      index++;
      continue;
    }
    if (character === "}") {
      depth--;
      index++;
      if (depth === 0) return index;
      continue;
    }
    index++;
  }
  throw createError(
    "KIT_PARSE_UNTERMINATED_TEMPLATE_EXPRESSION",
    "Unterminated template interpolation",
    { source, position: index }
  );
}

function scanTopLevel(source, visitor) {
  let round = 0;
  let square = 0;
  let curly = 0;
  let index = 0;

  while (index < source.length) {
    const character = source[index];
    if (character === "'" || character === '"') {
      index = skipQuotedSource(source, index, character);
      continue;
    }
    if (character === "`") {
      index = skipTemplateSource(source, index);
      continue;
    }

    if (character === "(") round++;
    else if (character === ")") round--;
    else if (character === "[") square++;
    else if (character === "]") square--;
    else if (character === "{") curly++;
    else if (character === "}") curly--;
    else if (round === 0 && square === 0 && curly === 0) visitor(character, index);

    if (round < 0 || square < 0 || curly < 0) {
      throw createError("KIT_PARSE_UNBALANCED_DELIMITER", "Unbalanced delimiter", {
        source,
        position: index
      });
    }
    index++;
  }

  if (round !== 0 || square !== 0 || curly !== 0) {
    throw createError("KIT_PARSE_UNBALANCED_DELIMITER", "Unbalanced delimiter", {
      source,
      position: source.length
    });
  }
}

module.exports = {
  skipQuotedSource,
  skipTemplateSource,
  skipBracedExpressionSource,
  scanTopLevel
};
