;(function () {
"use strict";

// The editor: a contenteditable region and a toolbar that formats what is
// selected. The region is authored — data-editor-area, with the starting
// content inside it — and so are the buttons, data-editor-command="bold".
// The component owns what markup cannot: running a command without losing
// the selection, keeping html / empty / words true to the region, and telling
// each button whether its format is on at the caret so aria-pressed can follow.
//
//   <div data-kit-component="editor">
//     <button data-editor-command="bold" data-kit-bind="aria-pressed: isActive('bold');">B</button>
//     <div data-editor-area contenteditable="true"><p>Hello</p></div>
//
// The engine underneath is document.execCommand — deprecated, universal, and
// the only one that edits the selection in place without a model of its own.
// The command names are the component's contract, not execCommand's.

var INSTANCE = Symbol("kit:editor");

var INLINE = { bold: "bold", italic: "italic", underline: "underline", strike: "strikeThrough" };
var BLOCK = { paragraph: "p", heading: "h2", subheading: "h3", quote: "blockquote", code: "pre" };
var LIST = { bullets: "insertUnorderedList", numbers: "insertOrderedList" };

function instance(scope) {
  return scope ? scope[INSTANCE] : undefined;
}

function area(data) {
  return data ? data.context.owned("[data-editor-area]")[0] || null : null;
}

function text(value) {
  return typeof value === "string" ? value : "";
}

function words(value) {
  var plain = text(value).replace(/<[^>]*>/g, " ").replace(/&nbsp;/g, " ").trim();
  return plain ? plain.split(/\s+/).length : 0;
}

// Read the region back into the scope after every edit, so bindings follow.
function sync(scope, data) {
  var region = area(data);
  if (!region || data.disposed) return;
  var html = region.innerHTML;
  var plain = (region.textContent || "").replace(/\u00a0/g, " ").trim();
  if (scope.html !== html) scope.html = html;
  var empty = plain === "" && !region.querySelector("img, hr, table");
  if (scope.empty !== empty) scope.empty = empty;
  var count = words(html);
  if (scope.words !== count) scope.words = count;
}

function focusRegion(data) {
  var region = area(data);
  if (region && typeof region.focus === "function" && region.ownerDocument.activeElement !== region) region.focus();
}

function execute(data, command, value) {
  var doc = data.context.host.ownerDocument;
  try {
    return doc.execCommand(command, false, value === undefined ? null : value) === true;
  } catch (_) {
    return false;
  }
}

function run(scope, data, command, value) {
  if (!data || data.disposed) return false;
  focusRegion(data);
  var done = false;
  if (INLINE[command]) done = execute(data, INLINE[command]);
  else if (BLOCK[command]) done = execute(data, "formatBlock", "<" + BLOCK[command] + ">");
  else if (LIST[command]) done = execute(data, LIST[command]);
  else if (command === "link") done = typeof value === "string" && value !== "" ? execute(data, "createLink", value) : false;
  else if (command === "unlink") done = execute(data, "unlink");
  else if (command === "clear") done = execute(data, "removeFormat") && (execute(data, "formatBlock", "<p>") || true);
  else if (command === "undo" || command === "redo") done = execute(data, command);
  else return false;
  sync(scope, data);
  scope.revision = (Number(scope.revision) || 0) + 1;
  return done;
}

function active(data, command) {
  if (!data || data.disposed) return false;
  var doc = data.context.host.ownerDocument;
  var region = area(data);
  var selection = doc.getSelection ? doc.getSelection() : null;
  var node = selection && selection.anchorNode;
  if (!region || !node || !region.contains(node)) return false;
  try {
    if (INLINE[command]) return doc.queryCommandState(INLINE[command]) === true;
    if (LIST[command]) return doc.queryCommandState(LIST[command]) === true;
    if (BLOCK[command]) return String(doc.queryCommandValue("formatBlock")).toLowerCase() === BLOCK[command];
  } catch (_) { /* an engine without the query answers false */ }
  return false;
}

kit.component("editor", {
  html: "",
  empty: true,
  words: 0,
  revision: 0,

  init: function (context) {
    var scope = this;
    var data = { context: context, disposed: false };
    Object.defineProperty(scope, INSTANCE, { value: data, configurable: true });
    var region = area(data);
    if (region && typeof scope.html === "string" && scope.html !== "" && region.innerHTML.trim() === "") {
      region.innerHTML = scope.html;
    }
    sync(scope, data);

    context.listen(context.host, "input", function (event) {
      if (area(data) && area(data).contains(event.target)) sync(scope, data);
    });

    // A toolbar press must not take the selection away from the region.
    context.listen(context.host, "mousedown", function (event) {
      var button = event.target && event.target.closest ? event.target.closest("[data-editor-command]") : null;
      if (button && context.host.contains(button)) event.preventDefault();
    });
    context.listen(context.host, "click", function (event) {
      var button = event.target && event.target.closest ? event.target.closest("[data-editor-command]") : null;
      if (!button || !context.host.contains(button)) return;
      event.preventDefault();
      run(scope, data, button.getAttribute("data-editor-command"), button.getAttribute("data-editor-value") || undefined);
    });

    // The caret moved: the buttons' pressed state may have changed.
    context.listen(context.host.ownerDocument, "selectionchange", function () {
      if (data.disposed) return;
      var region = area(data);
      var selection = context.host.ownerDocument.getSelection ? context.host.ownerDocument.getSelection() : null;
      if (!region || !selection || !selection.anchorNode || !region.contains(selection.anchorNode)) return;
      scope.revision = (Number(scope.revision) || 0) + 1;
    });

    context.cleanup(function () { data.disposed = true; });
  },

  run: function (command, value) { return run(this, instance(this), text(command), value); },
  // Reading revision ties the binding to the caret: each selectionchange bumps it.
  isActive: function (command) { return this.revision >= 0 && active(instance(this), text(command)); },

  // Replace the content; "" empties it.
  set: function (html) {
    var data = instance(this);
    var region = area(data);
    if (!region) return this.html;
    region.innerHTML = text(html);
    sync(this, data);
    return this.html;
  },
  clear: function () { return this.set(""); },
  focus: function () { focusRegion(instance(this)); return true; },
  plain: function () {
    var region = area(instance(this));
    return region ? (region.textContent || "").replace(/\u00a0/g, " ") : "";
  }
});

})();
