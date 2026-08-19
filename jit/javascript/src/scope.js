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
