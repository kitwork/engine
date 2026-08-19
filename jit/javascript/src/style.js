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
