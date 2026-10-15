/* code-editor component — a textarea that indents like an editor and knows where the caret is.
 *
 * Usage — the grammar counts the lines; the component handles the keys and the caret:
 *
 *   <div data-kit-scope="{ value: 'import { router } from \'kitwork\';' }">
 *     <div data-kit-component="code-editor">
 *       <div data-kit-element="gutter" aria-hidden="true">
 *         <div data-kit-for="n of value.split('\n').map((line, i) => i + 1)" data-kit-text="n"></div>
 *       </div>
 *       <textarea data-kit-element="field" data-kit-model="value" data-tab-size="2" wrap="off"
 *                 spellcheck="false" aria-label="Source" aria-describedby="editor-help"></textarea>
 *       <span data-kit-text="'Ln ' + line + ', Col ' + column"></span>
 *       <p id="editor-help" class="sr-only">Tab indents. Press Escape, then Tab, to leave the editor.</p>
 *     </div>
 *   </div>
 *
 * WHY THIS IS A COMPONENT. The text is data-kit-model, and the line numbers and counts are the
 * grammar over it. What the grammar cannot do:
 *
 *   · the KEYS an editor owes its author — Tab indents (every line of a selection, when it spans
 *     several), Shift+Tab outdents, Enter carries the current line's indentation onto the next;
 *   · the CARET — which line and column it is on is a fact the field owns and only reports by
 *     event; `line` and `column` turn it into numbers the markup can print;
 *   · the GUTTER following the field when it scrolls.
 *
 * AND A WAY OUT. An editor that takes Tab traps a keyboard user inside it, which WCAG 2.1.2
 * forbids. Escape releases the next Tab, so Escape then Tab leaves the field as Tab always does —
 * say so to the reader, as the aria-describedby above does. The KitJS editor took Tab for good.
 *
 * Edits go through execCommand("insertText") where the browser allows it, so Ctrl+Z still undoes
 * them; otherwise setRangeText, and an input event so data-kit-model hears the change either way.
 *
 * The public surface: line, column.
 */
var CODE_EDITOR = Symbol("kit:code-editor");

function editorField(scope) {
  var own = scope[CODE_EDITOR];
  return own ? own.context.element("field") : null;
}

function tabSize(field) {
  var n = field && field.hasAttribute("data-tab-size") ? Number(field.getAttribute("data-tab-size")) : 2;
  return isFinite(n) && n > 0 && n <= 8 ? n : 2;
}

function caret(scope) {
  var field = editorField(scope);
  if (!field) return;
  var before = field.value.slice(0, field.selectionStart);
  var lines = before.split("\n");
  var line = lines.length, column = lines[lines.length - 1].length + 1;
  if (scope.line !== line) scope.line = line;
  if (scope.column !== column) scope.column = column;
}

// Replace field.value[start:end] with text and leave the selection at [selStart, selEnd].
function replace(field, start, end, text, selStart, selEnd) {
  field.setSelectionRange(start, end);
  var done = false;
  try { done = field.ownerDocument.execCommand("insertText", false, text); } catch (error) { done = false; }
  if (!done || field.value.slice(start, start + text.length) !== text) {
    field.setRangeText(text, start, end, "end");
    field.dispatchEvent(new Event("input", { bubbles: true }));
  }
  field.setSelectionRange(selStart, selEnd);
}

var codeEditorDef = {
  line: 1,
  column: 1,

  init: function (context) {
    var scope = this;
    var released = false; // Escape hands the next Tab back to the browser
    scope[CODE_EDITOR] = { context: context };
    context.require("field");
    context.cleanup(function () { delete scope[CODE_EDITOR]; });

    ["keyup", "click", "input", "select", "focusin"].forEach(function (type) {
      context.listen(context.host, type, function (event) { if (event.target === editorField(scope)) caret(scope); });
    });

    var gutter = context.element("gutter"), field = context.element("field");
    if (gutter && field) context.listen(field, "scroll", function () { gutter.scrollTop = field.scrollTop; }, { passive: true });

    context.listen(context.host, "keydown", function (event) {
      var field = editorField(scope);
      if (!field || event.target !== field || event.altKey || event.ctrlKey || event.metaKey) return;
      var value = field.value, start = field.selectionStart, end = field.selectionEnd;
      var indent = new Array(tabSize(field) + 1).join(" ");

      if (event.key === "Escape") { released = true; return; }
      if (event.key === "Tab" && released) { released = false; return; } // the way out
      released = false;

      if (event.key === "Tab") {
        event.preventDefault();
        var lineStart = value.lastIndexOf("\n", start - 1) + 1;
        var multiline = value.slice(start, end).indexOf("\n") >= 0;
        if (!event.shiftKey && !multiline) {
          replace(field, start, end, indent, start + indent.length, start + indent.length);
        } else {
          // Every line the selection touches, indented or outdented as a block.
          var blockEnd = value.indexOf("\n", end - (end > start && value.charAt(end - 1) === "\n" ? 1 : 0));
          if (blockEnd < 0) blockEnd = value.length;
          var block = value.slice(lineStart, blockEnd).split("\n");
          var firstShift = 0, total = 0;
          block = block.map(function (text, i) {
            if (!event.shiftKey) { if (i === 0) firstShift = indent.length; total += indent.length; return indent + text; }
            var remove = Math.min(indent.length, text.length - text.replace(/^ +/, "").length);
            if (i === 0) firstShift = -remove;
            total -= remove;
            return text.slice(remove);
          });
          var joined = block.join("\n");
          replace(field, lineStart, blockEnd, joined, Math.max(lineStart, start + firstShift), end + total);
        }
        caret(scope);
        return;
      }

      if (event.key === "Enter" && start === end) {
        var from = value.lastIndexOf("\n", start - 1) + 1;
        var carried = /^[ \t]*/.exec(value.slice(from, start))[0];
        if (!carried) return; // nothing to carry; let Enter be Enter
        event.preventDefault();
        replace(field, start, end, "\n" + carried, start + 1 + carried.length, start + 1 + carried.length);
        caret(scope);
      }
    });
  }
};

window.kit.component("code-editor", codeEditorDef);
