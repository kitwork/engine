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
