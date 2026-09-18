;(function () {
"use strict";

// The code editor: a textarea that behaves like one. Tab indents the line or
// the selected lines, Shift+Tab outdents them, Enter keeps the indentation of
// the line above (one deeper after an opening bracket), and a typed bracket or
// quote closes itself around a selection. The textarea is authored —
// data-code-input — and so is the gutter, data-code-gutter, which the
// component keeps scrolled with it. Line and column follow the caret; lines()
// is what the markup draws the gutter from.
//
//   <div data-kit-component="code-editor" data-kit-scope="value: 'export const x = 1;'">
//     <div data-code-gutter><template data-kit-for="n of lines()"><span data-kit-text="n"></span></template></div>
//     <textarea data-code-input data-kit-model="value"></textarea>
//
// Highlighting is the server's job — the JIT highlighter prints code — so this
// component draws none: what you type is what is there.

var INSTANCE = Symbol("kit:code-editor");
var PAIRS = { "(": ")", "[": "]", "{": "}", '"': '"', "'": "'", "`": "`" };
var OPENERS = /[([{]\s*$/;

function instance(scope) {
  return scope ? scope[INSTANCE] : undefined;
}

function input(data) {
  return data ? data.context.owned("[data-code-input]")[0] || null : null;
}

function gutter(data) {
  return data ? data.context.owned("[data-code-gutter]")[0] || null : null;
}

function text(value) {
  return typeof value === "string" ? value : "";
}

function unit(scope) {
  var size = Number(scope.tabSize);
  size = Number.isInteger(size) && size > 0 && size <= 8 ? size : 2;
  return scope.tabs === true ? "\t" : new Array(size + 1).join(" ");
}

function indentOf(line) {
  var m = /^[ \t]*/.exec(line);
  return m ? m[0] : "";
}

// Replace [start, end) with insert and put the caret at caret (or select
// [caret, caretEnd)); fire input so data-kit-model and the scope follow.
function edit(field, start, end, insert, caret, caretEnd) {
  var value = field.value;
  field.value = value.slice(0, start) + insert + value.slice(end);
  field.setSelectionRange(caret, caretEnd === undefined ? caret : caretEnd);
  field.dispatchEvent(new Event("input", { bubbles: true }));
}

function lineBounds(value, start, end) {
  var from = value.lastIndexOf("\n", start - 1) + 1;
  var to = value.indexOf("\n", end);
  if (to < 0) to = value.length;
  return { from: from, to: to };
}

function indentLines(scope, field, outdent) {
  var value = field.value;
  var start = field.selectionStart;
  var end = field.selectionEnd;
  var tab = unit(scope);
  var bounds = lineBounds(value, start, end);
  var block = value.slice(bounds.from, bounds.to);
  var lines = block.split("\n");
  var firstDelta = 0;
  var total = 0;
  var changed = lines.map(function (line, index) {
    var delta;
    if (outdent) {
      var lead = line.indexOf(tab) === 0 ? tab.length : (line.charAt(0) === "\t" || line.charAt(0) === " " ? 1 : 0);
      delta = -lead;
      line = line.slice(lead);
    } else {
      delta = tab.length;
      line = tab + line;
    }
    if (index === 0) firstDelta = delta;
    total += delta;
    return line;
  }).join("\n");
  var caret = Math.max(bounds.from, start + firstDelta);
  var caretEnd = Math.max(caret, end + total);
  edit(field, bounds.from, bounds.to, changed, caret, start === end ? caret : caretEnd);
}

function insertTab(scope, field) {
  var start = field.selectionStart;
  var tab = unit(scope);
  edit(field, start, field.selectionEnd, tab, start + tab.length);
}

function newline(scope, field) {
  var value = field.value;
  var start = field.selectionStart;
  var bounds = lineBounds(value, start, start);
  var line = value.slice(bounds.from, start);
  var indent = indentOf(line);
  if (OPENERS.test(line)) indent += unit(scope);
  var insert = "\n" + indent;
  var after = value.charAt(field.selectionEnd);
  var before = line.charAt(line.length - 1);
  // Enter between a bracket pair opens a block: the closer moves to its own line.
  if (before && PAIRS[before] === after && before !== after) {
    insert += "\n" + indentOf(line);
    edit(field, start, field.selectionEnd, insert, start + 1 + indent.length);
    return;
  }
  edit(field, start, field.selectionEnd, insert, start + insert.length);
}

function pair(field, opener) {
  var closer = PAIRS[opener];
  var start = field.selectionStart;
  var end = field.selectionEnd;
  var selected = field.value.slice(start, end);
  if (start !== end) {
    edit(field, start, end, opener + selected + closer, start + 1, end + 1);
    return true;
  }
  var next = field.value.charAt(start);
  // Typing the closer over an auto-inserted closer just steps past it.
  if (opener === closer && next === closer) {
    field.setSelectionRange(start + 1, start + 1);
    return true;
  }
  if (next && !/[\s)\]};,]/.test(next)) return false;
  edit(field, start, end, opener + closer, start + 1);
  return true;
}

function caret(scope, data) {
  var field = input(data);
  if (!field || data.disposed) return;
  var value = field.value;
  var position = field.selectionStart;
  var before = value.slice(0, position);
  var line = before.split("\n").length;
  var column = position - before.lastIndexOf("\n");
  if (scope.line !== line) scope.line = line;
  if (scope.column !== column) scope.column = column;
}

function follow(data) {
  var field = input(data);
  var rail = gutter(data);
  if (field && rail) rail.style.transform = "translateY(" + (-field.scrollTop) + "px)";
}

kit.component("code-editor", {
  value: "",
  tabSize: 2,
  tabs: false,
  pairs: true,
  line: 1,
  column: 1,

  init: function (context) {
    var scope = this;
    var data = { context: context, disposed: false };
    Object.defineProperty(scope, INSTANCE, { value: data, configurable: true });
    var field = input(data);
    if (field && typeof scope.value === "string" && field.value === "" && scope.value !== "") field.value = scope.value;
    caret(scope, data);

    context.listen(context.host, "keydown", function (event) {
      var target = input(data);
      if (!target || event.target !== target || event.ctrlKey || event.metaKey || event.altKey) return;
      var key = event.key;
      if (key === "Tab") {
        var multi = target.value.slice(target.selectionStart, target.selectionEnd).indexOf("\n") >= 0;
        if (event.shiftKey || multi) indentLines(scope, target, event.shiftKey);
        else insertTab(scope, target);
      } else if (key === "Enter") {
        newline(scope, target);
      } else if (PAIRS[key] && scope.pairs !== false) {
        if (!pair(target, key)) return;
      } else if (key === "Backspace" && target.selectionStart === target.selectionEnd) {
        var at = target.selectionStart;
        var open = target.value.charAt(at - 1);
        if (!PAIRS[open] || target.value.charAt(at) !== PAIRS[open]) return;
        edit(target, at - 1, at + 1, "", at - 1);
      } else return;
      event.preventDefault();
      caret(scope, data);
    });

    var track = function (event) {
      if (event.target === input(data)) caret(scope, data);
    };
    context.listen(context.host, "input", track);
    context.listen(context.host, "click", track);
    context.listen(context.host, "keyup", track);
    // scroll does not bubble: listen on the field itself.
    if (field) context.listen(field, "scroll", function () { follow(data); });

    function afterRender() {
      if (data.disposed) return;
      follow(data);
      context.afterRender(afterRender);
    }
    context.afterRender(afterRender);
    context.cleanup(function () { data.disposed = true; });
  },

  // 1 … n, one per line, for the gutter.
  lines: function () {
    var count = text(this.value).split("\n").length;
    var out = [];
    for (var i = 1; i <= count; i++) out.push(i);
    return out;
  },
  count: function () { return text(this.value).split("\n").length; },
  length: function () { return text(this.value).length; },

  set: function (value) {
    var field = input(instance(this));
    value = text(value);
    this.value = value;
    if (field) {
      field.value = value;
      field.dispatchEvent(new Event("input", { bubbles: true }));
    }
    return this.value;
  },
  clear: function () { return this.set(""); },
  focus: function () {
    var field = input(instance(this));
    if (field && typeof field.focus === "function") field.focus();
    return !!field;
  },
  // Move the caret to a line (one-based) and scroll it into view.
  goTo: function (line) {
    var field = input(instance(this));
    line = Number(line);
    if (!field || !Number.isInteger(line) || line < 1) return false;
    var lines = field.value.split("\n");
    if (line > lines.length) line = lines.length;
    var position = 0;
    for (var i = 0; i < line - 1; i++) position += lines[i].length + 1;
    field.focus();
    field.setSelectionRange(position, position);
    caret(this, instance(this));
    return line;
  }
});

})();
