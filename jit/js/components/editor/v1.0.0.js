/* editor component — rich text on a contenteditable, driven from the markup's own buttons.
 *
 * Usage — every button is the grammar; the component does the editing:
 *
 *   <div data-kit-component="editor" data-kit-alias="$post">
 *     <div role="toolbar" aria-label="Formatting">
 *       <button data-kit-click="run('bold')" aria-label="Bold"
 *               data-kit-bind:aria-pressed="isActive('bold') ? 'true' : 'false'">B</button>
 *       <button data-kit-click="run('heading')" …>H</button>
 *       <button data-kit-click="beginLink()" …>Link</button>
 *     </div>
 *     <div data-kit-element="area" contenteditable="true" role="textbox" aria-multiline="true"
 *          aria-label="Post body" data-kit-keydown:mod+k:prevent="beginLink()"></div>
 *     <form data-kit-show="linking" hidden data-kit-submit:prevent="applyLink()">
 *       <input data-kit-model="href" data-kit-keydown:escape="cancelLink()" aria-label="Link address">
 *     </form>
 *     <span data-kit-text="words + ' words'"></span>
 *   </div>
 *
 * Commands for run(name): bold italic underline strike · heading subheading paragraph quote code ·
 * bullets numbers rule · undo redo. isActive(name) answers the same names, plus link.
 *
 * WHY THIS IS A COMPONENT. Editing a contenteditable is nothing markup can say. What it does:
 *
 *   · runs the commands, and keeps the selection while a toolbar button is pressed — a press would
 *     otherwise take focus, and the command would act on nothing;
 *   · answers isActive() for the aria-pressed bindings, repainting when the selection moves;
 *   · makes a link: beginLink() remembers the selection while the address is typed elsewhere,
 *     applyLink() puts it back and links it; only http(s), mailto, tel, relative and #anchors are
 *     accepted — a javascript: address never becomes a link;
 *   · pastes as plain text, so a page copied from elsewhere brings no styles, spans or scripts;
 *   · turns a line started with "# ", "## ", "- ", "1. " or "> " into that block as you type.
 *
 * Deliberately left out, from the KitJS editor: the bubble toolbar that floated over a selection
 * (positioning only — the toolbar above has every command) and markdown() export (a converter, not
 * an editor). The editing itself uses execCommand, the one API that keeps the browser's own undo
 * history for a contenteditable; it is marked deprecated with nothing yet to replace it.
 *
 * The public surface: html, empty, words, characters, linking, href, run, isActive, beginLink,
 * applyLink, cancelLink, unlink, set(html), clear().
 */
var EDITOR = Symbol("kit:editor");

var BLOCKS = { heading: "h2", subheading: "h3", paragraph: "p", quote: "blockquote", code: "pre" };
var INLINE = { bold: "bold", italic: "italic", underline: "underline", strike: "strikeThrough" };
var SAFE_LINK = /^(https?:|mailto:|tel:|\/|#|\.{0,2}\/)/i;

function editorArea(scope) {
  var own = scope[EDITOR];
  return own ? own.context.element("area") : null;
}

function exec(scope, command, value) {
  var area = editorArea(scope);
  if (!area) return false;
  if (area.ownerDocument.activeElement !== area) area.focus();
  var done = false;
  try { done = area.ownerDocument.execCommand(command, false, value); } catch (error) { done = false; }
  touch(scope);
  return done;
}

// The block the caret sits in, as a lower-case tag name, within the editing area.
function currentBlock(scope) {
  var area = editorArea(scope), selection = area && area.ownerDocument.getSelection();
  if (!selection || !selection.rangeCount) return "";
  for (var node = selection.anchorNode; node && node !== area; node = node.parentNode) {
    if (node.nodeType === 1 && /^(H2|H3|P|BLOCKQUOTE|PRE|LI)$/.test(node.nodeName)) return node.nodeName.toLowerCase();
  }
  return "";
}

function inSelection(scope, tag) {
  var area = editorArea(scope), selection = area && area.ownerDocument.getSelection();
  if (!selection || !selection.rangeCount) return null;
  for (var node = selection.anchorNode; node && node !== area; node = node.parentNode) {
    if (node.nodeName === tag) return node;
  }
  return null;
}

// Every read of the content comes from here, so words/characters/html/empty agree.
function touch(scope) {
  var area = editorArea(scope);
  if (!area) return;
  var text = area.textContent || "";
  scope.html = area.innerHTML;
  scope.characters = text.length;
  scope.words = text.trim() ? text.trim().split(/\s+/).length : 0;
  scope.empty = text.trim() === "";
  scope.revision = (scope.revision || 0) + 1; // isActive() bindings read the selection, not state
}

var editorDef = {
  html: "",
  empty: true,
  words: 0,
  characters: 0,
  revision: 0,
  linking: false,
  href: "",

  run: function (name) {
    if (INLINE[name]) return exec(this, INLINE[name]);
    if (BLOCKS[name]) return exec(this, "formatBlock", "<" + BLOCKS[name] + ">");
    if (name === "bullets") return exec(this, "insertUnorderedList");
    if (name === "numbers") return exec(this, "insertOrderedList");
    if (name === "rule") return exec(this, "insertHorizontalRule");
    if (name === "undo" || name === "redo") return exec(this, name);
    return false;
  },

  isActive: function (name) {
    void this.revision; // read so the binding repaints when the selection moves
    var area = editorArea(this);
    if (!area) return false;
    if (INLINE[name]) {
      try { return area.ownerDocument.queryCommandState(INLINE[name]); } catch (error) { return false; }
    }
    if (BLOCKS[name]) return currentBlock(this) === BLOCKS[name];
    if (name === "bullets") return !!inSelection(this, "UL");
    if (name === "numbers") return !!inSelection(this, "OL");
    if (name === "link") return !!inSelection(this, "A");
    return false;
  },

  beginLink: function () {
    var area = editorArea(this), selection = area && area.ownerDocument.getSelection();
    if (!selection || !selection.rangeCount || !area.contains(selection.anchorNode)) return;
    this[EDITOR].saved = selection.getRangeAt(0).cloneRange();
    var existing = inSelection(this, "A");
    this.href = existing ? existing.getAttribute("href") || "" : "";
    this.linking = true;
  },

  applyLink: function () {
    var own = this[EDITOR], href = String(this.href || "").trim();
    this.linking = false;
    if (!own || !own.saved) return false;
    var area = editorArea(this), selection = area.ownerDocument.getSelection();
    area.focus();
    selection.removeAllRanges();
    selection.addRange(own.saved);
    own.saved = null;
    if (!href) return exec(this, "unlink");
    if (!SAFE_LINK.test(href)) return false; // a javascript: address never becomes a link
    return exec(this, "createLink", href);
  },

  cancelLink: function () {
    this.linking = false;
    var own = this[EDITOR];
    if (own && own.saved) {
      var area = editorArea(this), selection = area.ownerDocument.getSelection();
      area.focus();
      selection.removeAllRanges();
      selection.addRange(own.saved);
      own.saved = null;
    }
  },

  unlink: function () { return exec(this, "unlink"); },

  set: function (html) {
    var area = editorArea(this);
    if (!area) return;
    area.innerHTML = String(html == null ? "" : html);
    touch(this);
  },

  clear: function () { this.set(""); },

  init: function (context) {
    var scope = this;
    var document = context.host.ownerDocument;
    scope[EDITOR] = { context: context, saved: null };
    context.require("area");
    context.cleanup(function () { delete scope[EDITOR]; });
    touch(scope);

    // A press on a toolbar button must not take focus from the text, or the command acts on nothing.
    context.listen(context.host, "mousedown", function (event) {
      var bar = event.target && event.target.closest ? event.target.closest('[role="toolbar"]') : null;
      if (bar && context.host.contains(bar)) event.preventDefault();
    });

    context.listen(document, "selectionchange", function () {
      var area = editorArea(scope), selection = document.getSelection();
      if (area && selection && selection.anchorNode && area.contains(selection.anchorNode)) scope.revision = (scope.revision || 0) + 1;
    });

    context.listen(context.host, "input", function (event) {
      var area = editorArea(scope);
      if (event.target !== area) return;
      // A line started with a marker and a space becomes that block.
      var selection = document.getSelection(), node = selection && selection.anchorNode;
      if (node && node.nodeType === 3 && selection.anchorOffset === node.textContent.length) {
        var shortcuts = [["# ", "heading"], ["## ", "subheading"], ["> ", "quote"], ["- ", "bullets"], ["1. ", "numbers"]];
        for (var i = 0; i < shortcuts.length; i++) {
          var marker = shortcuts[i][0];
          var block = node.parentNode;
          if (node.textContent.replace(/ /g, " ") === marker && block && block !== area && block.firstChild === node) {
            node.textContent = "";
            scope.run(shortcuts[i][1]);
            break;
          }
        }
      }
      touch(scope);
    });

    // Paste as plain text: styles, spans and scripts from elsewhere stay there.
    context.listen(context.host, "paste", function (event) {
      if (event.target !== editorArea(scope) && !(editorArea(scope) || { contains: function () { return false; } }).contains(event.target)) return;
      var text = event.clipboardData && event.clipboardData.getData("text/plain");
      if (text == null) return;
      event.preventDefault();
      exec(scope, "insertText", text);
    });
  }
};

window.kit.component("editor", editorDef);
