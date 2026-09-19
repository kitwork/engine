;(function () {
"use strict";

// The editor, second edition: a content editor, not just a toolbar.
//
// 1.0.0 ran a command on the selection and kept html / empty / words true.
// This one is what a writer expects of a field they will live in:
//
//   - markdown as you type: "# " opens a heading, "- " a list, "> " a quote,
//     "```" a code block, and **bold**, *italic*, `code` close themselves on
//     the space after them;
//   - shortcuts: Ctrl/Cmd+K for a link, Ctrl/Cmd+Shift+7 and 8 for numbered
//     and bulleted lists, Ctrl/Cmd+Alt+1, 2 and 0 for heading, subheading
//     and paragraph (B, I and U are the browser's own);
//   - a link flow: link() keeps the selection while the URL is typed
//     elsewhere, applyLink(href) puts it on; linkHref() reads the one under
//     the caret; unlink() takes it off;
//   - clean paste: pasted HTML is reduced to the tags the editor writes —
//     no styles, classes, spans or scripts — and plain text stays plain;
//   - a bubble: selecting is true while text is selected in the region and
//     bubbleX / bubbleY say where, host-relative, so a floating toolbar can
//     sit above the selection;
//   - two outputs: html as the region has it, markdown() as a file would.
//
// The region is authored — data-editor-area — and so are the buttons,
// data-editor-command="bold". The engine underneath is document.execCommand:
// deprecated, universal, and the only one that edits the selection in place
// without a model of its own. The command names are the component's contract.

var INSTANCE = Symbol("kit:editor");

var INLINE = { bold: "bold", italic: "italic", underline: "underline", strike: "strikeThrough" };
var BLOCK = { paragraph: "p", heading: "h2", subheading: "h3", quote: "blockquote", code: "pre" };
var LIST = { bullets: "insertUnorderedList", numbers: "insertOrderedList" };
var BLOCK_RULES = [
  { pattern: /^#$/, command: "heading" },
  { pattern: /^##$/, command: "subheading" },
  { pattern: /^###$/, command: "subheading" },
  { pattern: /^[-*]$/, command: "bullets" },
  { pattern: /^1[.)]$/, command: "numbers" },
  { pattern: /^>$/, command: "quote" },
  { pattern: /^```$/, command: "code" }
];
var INLINE_RULES = [
  { pattern: /\*\*([^*\s][^*]*?)\*\*$/, command: "bold" },
  { pattern: /(?:^|[^*])\*([^*\s][^*]*?)\*$/, command: "italic", lead: true },
  { pattern: /`([^`\s][^`]*?)`$/, command: "inlineCode" }
];
var KEEP = {
  P: "p", BR: "br", STRONG: "strong", B: "strong", EM: "em", I: "em", U: "u", S: "s", STRIKE: "s", DEL: "s",
  A: "a", H1: "h2", H2: "h2", H3: "h3", H4: "h3", UL: "ul", OL: "ol", LI: "li", BLOCKQUOTE: "blockquote",
  PRE: "pre", CODE: "code", HR: "hr"
};
var DROP = { SCRIPT: true, STYLE: true, TEMPLATE: true, IFRAME: true, OBJECT: true, EMBED: true, SVG: true, META: true, LINK: true, HEAD: true, TITLE: true };

function instance(scope) {
  return scope ? scope[INSTANCE] : undefined;
}

function area(data) {
  return data ? data.context.owned("[data-editor-area]")[0] || null : null;
}

function text(value) {
  return typeof value === "string" ? value : "";
}

function doc(data) {
  return data.context.host.ownerDocument;
}

function selectionIn(data) {
  var region = area(data);
  var selection = doc(data).getSelection ? doc(data).getSelection() : null;
  if (!region || !selection || !selection.rangeCount || !selection.anchorNode || !region.contains(selection.anchorNode)) return null;
  return selection;
}

function words(plain) {
  plain = plain.replace(/\u00a0/g, " ").trim();
  return plain ? plain.split(/\s+/).length : 0;
}

// Read the region back into the scope after every edit, so bindings follow.
function sync(scope, data) {
  var region = area(data);
  if (!region || data.disposed) return;
  var html = region.innerHTML;
  var plain = (region.textContent || "").replace(/\u00a0/g, " ");
  if (scope.html !== html) scope.html = html;
  var empty = plain.trim() === "" && !region.querySelector("img, hr, table");
  if (scope.empty !== empty) scope.empty = empty;
  var count = words(plain);
  if (scope.words !== count) scope.words = count;
  var characters = plain.length;
  if (scope.characters !== characters) scope.characters = characters;
  var dirty = html !== data.baseline;
  if (scope.dirty !== dirty) scope.dirty = dirty;
}

function focusRegion(data) {
  var region = area(data);
  if (region && typeof region.focus === "function" && doc(data).activeElement !== region) region.focus();
}

function execute(data, command, value) {
  try {
    return doc(data).execCommand(command, false, value === undefined ? null : value) === true;
  } catch (_) {
    return false;
  }
}

function bump(scope) {
  scope.revision = (Number(scope.revision) || 0) + 1;
}

// ---- links ----------------------------------------------------------------

function anchorAt(data) {
  var selection = selectionIn(data);
  if (!selection) return null;
  var node = selection.anchorNode;
  if (node && node.nodeType === 3) node = node.parentNode;
  var link = node && node.closest ? node.closest("a[href]") : null;
  return link && area(data).contains(link) ? link : null;
}

function normalizeHref(value) {
  value = text(value).trim();
  if (!value) return "";
  if (/^(https?:|mailto:|tel:|\/|#|\.\.?\/)/i.test(value)) return value;
  if (/^[\w.-]+\.[a-z]{2,}(\/|$)/i.test(value)) return "https://" + value;
  return value;
}

// unlink works on a selection; a caret inside a link means the whole link.
function unlinkAt(data) {
  var selection = selectionIn(data);
  var link = anchorAt(data);
  if (selection && selection.isCollapsed && link) {
    var range = doc(data).createRange();
    range.selectNodeContents(link);
    selection.removeAllRanges();
    selection.addRange(range);
    var done = execute(data, "unlink");
    selection.collapseToEnd();
    return done;
  }
  return execute(data, "unlink");
}

function saveRange(data) {
  var selection = selectionIn(data);
  data.savedRange = selection ? selection.getRangeAt(0).cloneRange() : null;
  return !!data.savedRange;
}

function restoreRange(data) {
  if (!data.savedRange) return false;
  var selection = doc(data).getSelection();
  if (!selection) return false;
  focusRegion(data);
  selection.removeAllRanges();
  selection.addRange(data.savedRange);
  return true;
}

// ---- paste -----------------------------------------------------------------

// Reduce foreign HTML to the tags this editor writes. Attributes go, except
// a link's href; unknown elements are unwrapped, dangerous ones dropped whole.
function clean(data, html) {
  var template = doc(data).createElement("template");
  template.innerHTML = html;
  var out = doc(data).createDocumentFragment();
  function walk(node, into) {
    for (var child = node.firstChild; child; child = child.nextSibling) {
      if (child.nodeType === 3) {
        into.appendChild(doc(data).createTextNode(child.nodeValue));
        continue;
      }
      if (child.nodeType !== 1) continue;
      var tag = child.tagName.toUpperCase();
      if (DROP[tag]) continue;
      var keep = KEEP[tag];
      if (!keep) { walk(child, into); continue; }
      var element = doc(data).createElement(keep);
      if (keep === "a") {
        var href = normalizeHref(child.getAttribute("href"));
        if (!href || /^javascript:/i.test(href)) { walk(child, into); continue; }
        element.setAttribute("href", href);
      }
      walk(child, element);
      into.appendChild(element);
    }
  }
  walk(template.content, out);
  var holder = doc(data).createElement("div");
  holder.appendChild(out);
  return holder.innerHTML;
}

function escapeHTML(value) {
  return value.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
}

// ---- markdown as you type -------------------------------------------------

function blockOf(data, node) {
  var region = area(data);
  if (node && node.nodeType === 3) node = node.parentNode;
  while (node && node !== region) {
    if (/^(P|H1|H2|H3|H4|LI|BLOCKQUOTE|PRE|DIV)$/.test(node.tagName)) return node;
    node = node.parentNode;
  }
  return region;
}

// Text from the start of the block up to the caret, and the caret's text node.
function textBeforeCaret(data) {
  var selection = selectionIn(data);
  if (!selection || !selection.isCollapsed) return null;
  var range = selection.getRangeAt(0);
  var block = blockOf(data, range.startContainer);
  var probe = range.cloneRange();
  probe.selectNodeContents(block);
  probe.setEnd(range.startContainer, range.startOffset);
  return { block: block, text: probe.toString(), node: range.startContainer, offset: range.startOffset, selection: selection };
}

function applyBlockRule(scope, data, at, rule) {
  var selection = at.selection;
  // Remove the marker: select from the block start to the caret and delete it.
  var range = doc(data).createRange();
  range.selectNodeContents(at.block);
  range.setEnd(at.node, at.offset);
  selection.removeAllRanges();
  selection.addRange(range);
  execute(data, "delete");
  run(scope, data, rule.command);
  return true;
}

function applyInlineRule(scope, data, at, rule, match) {
  if (at.node.nodeType !== 3) return false;
  var value = at.node.nodeValue || "";
  var caret = at.offset;
  var whole = match[0];
  var lead = rule.lead && whole.length > 0 && whole.charAt(0) !== "*" ? 1 : 0;
  var start = caret - (whole.length - lead);
  if (start < 0 || value.slice(start, caret) !== whole.slice(lead)) return false;
  var inner = match[1];
  var range = doc(data).createRange();
  range.setStart(at.node, start);
  range.setEnd(at.node, caret);
  var selection = at.selection;
  selection.removeAllRanges();
  selection.addRange(range);
  if (rule.command === "inlineCode") {
    execute(data, "insertHTML", "<code>" + escapeHTML(inner) + "</code>&nbsp;");
    return true;
  }
  execute(data, "insertText", inner);
  // The inserted text is now before the caret: select it and format it.
  var after = selectionIn(data);
  if (!after || !after.rangeCount) return true;
  var end = after.getRangeAt(0);
  var wrap = doc(data).createRange();
  wrap.setStart(end.startContainer, Math.max(0, end.startOffset - inner.length));
  wrap.setEnd(end.startContainer, end.startOffset);
  selection.removeAllRanges();
  selection.addRange(wrap);
  execute(data, INLINE[rule.command]);
  selection.collapseToEnd();
  execute(data, INLINE[rule.command]); // turn the format back off for what follows
  execute(data, "insertText", " ");
  return true;
}

// Space was pressed: does what precedes the caret spell a rule?
function inputRule(scope, data) {
  var at = textBeforeCaret(data);
  if (!at) return false;
  var head = at.text;
  for (var i = 0; i < BLOCK_RULES.length; i++) {
    if (BLOCK_RULES[i].pattern.test(head) && at.block !== area(data) && at.block.tagName !== "PRE") {
      return applyBlockRule(scope, data, at, BLOCK_RULES[i]);
    }
  }
  if (at.block.tagName === "PRE") return false;
  for (var j = 0; j < INLINE_RULES.length; j++) {
    var match = INLINE_RULES[j].pattern.exec(head);
    if (match) return applyInlineRule(scope, data, at, INLINE_RULES[j], match);
  }
  return false;
}

// ---- commands --------------------------------------------------------------

function run(scope, data, command, value) {
  if (!data || data.disposed) return false;
  focusRegion(data);
  var done = false;
  if (INLINE[command]) done = execute(data, INLINE[command]);
  else if (BLOCK[command]) done = execute(data, "formatBlock", "<" + BLOCK[command] + ">");
  else if (LIST[command]) done = execute(data, LIST[command]);
  else if (command === "link") return scope.link();
  else if (command === "unlink") done = unlinkAt(data);
  else if (command === "rule") done = execute(data, "insertHorizontalRule");
  else if (command === "clear") done = execute(data, "removeFormat") && (execute(data, "formatBlock", "<p>") || true);
  else if (command === "undo" || command === "redo") done = execute(data, command);
  else return false;
  sync(scope, data);
  bump(scope);
  return done;
}

function active(data, command) {
  if (!data || data.disposed) return false;
  var selection = selectionIn(data);
  if (!selection) return false;
  try {
    if (INLINE[command]) return doc(data).queryCommandState(INLINE[command]) === true;
    if (LIST[command]) return doc(data).queryCommandState(LIST[command]) === true;
    if (BLOCK[command]) return String(doc(data).queryCommandValue("formatBlock")).toLowerCase() === BLOCK[command];
    if (command === "link") return !!anchorAt(data);
  } catch (_) { /* an engine without the query answers false */ }
  return false;
}

// Where the selection is, for the bubble: above its box, host-relative.
function bubble(scope, data) {
  var selection = selectionIn(data);
  var selecting = !!selection && !selection.isCollapsed && selection.toString().trim() !== "";
  if (scope.selecting !== selecting) scope.selecting = selecting;
  if (!selecting) return;
  var rect = selection.getRangeAt(0).getBoundingClientRect();
  var host = data.context.host.getBoundingClientRect();
  var x = Math.round(rect.left + rect.width / 2 - host.left);
  var y = Math.round(rect.top - host.top);
  if (scope.bubbleX !== x) scope.bubbleX = x;
  if (scope.bubbleY !== y) scope.bubbleY = y;
}

// ---- markdown out ----------------------------------------------------------

function markdownOf(node, listDepth, ordered) {
  var out = "";
  var index = 0;
  for (var child = node.firstChild; child; child = child.nextSibling) {
    if (child.nodeType === 3) { out += child.nodeValue.replace(/\u00a0/g, " "); continue; }
    if (child.nodeType !== 1) continue;
    var tag = child.tagName;
    var inner;
    switch (tag) {
      case "H1": case "H2": out += "\n\n## " + markdownOf(child, listDepth, false).trim() + "\n\n"; break;
      case "H3": case "H4": out += "\n\n### " + markdownOf(child, listDepth, false).trim() + "\n\n"; break;
      case "P": case "DIV": out += "\n\n" + markdownOf(child, listDepth, false).trim() + "\n\n"; break;
      case "BR": out += "  \n"; break;
      case "HR": out += "\n\n---\n\n"; break;
      case "STRONG": case "B": inner = markdownOf(child, listDepth, false); out += inner.trim() ? "**" + inner + "**" : inner; break;
      case "EM": case "I": inner = markdownOf(child, listDepth, false); out += inner.trim() ? "*" + inner + "*" : inner; break;
      case "S": case "STRIKE": case "DEL": inner = markdownOf(child, listDepth, false); out += inner.trim() ? "~~" + inner + "~~" : inner; break;
      case "U": out += markdownOf(child, listDepth, false); break;
      case "CODE":
        if (node.tagName === "PRE") out += markdownOf(child, listDepth, false);
        else out += "`" + (child.textContent || "") + "`";
        break;
      case "PRE": out += "\n\n```\n" + (child.textContent || "").replace(/\n$/, "") + "\n```\n\n"; break;
      case "A":
        inner = markdownOf(child, listDepth, false);
        out += "[" + inner + "](" + (child.getAttribute("href") || "") + ")";
        break;
      case "BLOCKQUOTE":
        out += "\n\n" + markdownOf(child, listDepth, false).trim().split("\n").map(function (line) { return "> " + line; }).join("\n") + "\n\n";
        break;
      case "UL": case "OL":
        out += (listDepth ? "\n" : "\n\n") + markdownOf(child, listDepth + 1, tag === "OL") + (listDepth ? "" : "\n\n");
        break;
      case "LI":
        index++;
        var pad = new Array(Math.max(0, listDepth - 1) * 2 + 1).join(" ");
        var body = markdownOf(child, listDepth, ordered).replace(/^\n+|\n+$/g, "");
        out += pad + (ordered ? index + ". " : "- ") + body + "\n";
        break;
      default: out += markdownOf(child, listDepth, ordered);
    }
  }
  return out;
}

function markdown(data) {
  var region = area(data);
  if (!region) return "";
  return markdownOf(region, 0, false).replace(/[ \t]+\n/g, "\n").replace(/\n{3,}/g, "\n\n").trim() + "\n";
}

// ---- the component --------------------------------------------------------

kit.component("editor", {
  html: "",
  empty: true,
  words: 0,
  characters: 0,
  dirty: false,
  revision: 0,
  selecting: false,
  bubbleX: 0,
  bubbleY: 0,
  linking: false,
  href: "",

  init: function (context) {
    var scope = this;
    var data = { context: context, disposed: false, baseline: "", savedRange: null };
    Object.defineProperty(scope, INSTANCE, { value: data, configurable: true });
    var region = area(data);
    if (region && typeof scope.html === "string" && scope.html !== "" && region.innerHTML.trim() === "") {
      region.innerHTML = scope.html;
    }
    data.baseline = region ? region.innerHTML : "";
    sync(scope, data);

    context.listen(context.host, "input", function (event) {
      if (area(data) && area(data).contains(event.target)) sync(scope, data);
    });

    context.listen(context.host, "keydown", function (event) {
      var region = area(data);
      if (!region || !region.contains(event.target)) return;
      var meta = event.ctrlKey || event.metaKey;
      if (event.key === " " && !meta && !event.altKey) {
        if (inputRule(scope, data)) { event.preventDefault(); sync(scope, data); bump(scope); }
        return;
      }
      if (!meta) return;
      var key = event.key.toLowerCase();
      var handled = false;
      if (key === "k" && !event.shiftKey && !event.altKey) { scope.link(); handled = true; }
      else if (event.shiftKey && (key === "7" || key === "&")) handled = run(scope, data, "numbers") || true;
      else if (event.shiftKey && (key === "8" || key === "*")) handled = run(scope, data, "bullets") || true;
      else if (event.altKey && key === "1") handled = run(scope, data, "heading") || true;
      else if (event.altKey && key === "2") handled = run(scope, data, "subheading") || true;
      else if (event.altKey && key === "0") handled = run(scope, data, "paragraph") || true;
      if (handled) event.preventDefault();
    });

    // Foreign HTML comes in clean; plain text stays plain.
    context.listen(context.host, "paste", function (event) {
      var region = area(data);
      if (!region || !region.contains(event.target) || !event.clipboardData) return;
      var html = event.clipboardData.getData("text/html");
      var plain = event.clipboardData.getData("text/plain");
      if (!html && !plain) return;
      event.preventDefault();
      if (html) execute(data, "insertHTML", clean(data, html));
      else execute(data, "insertText", plain);
      sync(scope, data);
      bump(scope);
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

    // The caret moved: pressed state and the bubble may have changed.
    context.listen(doc(data), "selectionchange", function () {
      if (data.disposed) return;
      var selection = selectionIn(data);
      if (!selection) {
        if (scope.selecting && !scope.linking) scope.selecting = false;
        return;
      }
      if (!scope.linking) bubble(scope, data);
      bump(scope);
    });

    context.cleanup(function () { data.disposed = true; });
  },

  run: function (command, value) { return run(this, instance(this), text(command), value); },
  // Reading revision ties the binding to the caret: each selectionchange bumps it.
  isActive: function (command) { return this.revision >= 0 && active(instance(this), text(command)); },

  // Start a link: keep the selection, open the URL field with the current href.
  link: function () {
    var data = instance(this);
    if (!data) return false;
    var link = anchorAt(data);
    if (!saveRange(data) && !link) return false;
    this.href = link ? link.getAttribute("href") || "" : "";
    this.linking = true;
    return true;
  },
  applyLink: function (href) {
    var data = instance(this);
    if (!data) return false;
    var value = normalizeHref(arguments.length ? href : this.href);
    this.linking = false;
    if (!restoreRange(data)) return false;
    var done;
    if (!value) done = unlinkAt(data);
    else {
      var selection = selectionIn(data);
      var link = anchorAt(data);
      if (selection && selection.isCollapsed && link) {
        link.setAttribute("href", value);
        done = true;
      } else if (selection && selection.isCollapsed) {
        done = execute(data, "insertHTML", '<a href="' + escapeHTML(value) + '">' + escapeHTML(value) + "</a>");
      } else done = execute(data, "createLink", value);
    }
    data.savedRange = null;
    this.href = "";
    sync(this, data);
    bump(this);
    return done;
  },
  cancelLink: function () {
    var data = instance(this);
    this.linking = false;
    this.href = "";
    if (data) { restoreRange(data); data.savedRange = null; }
    return false;
  },
  unlink: function () {
    var data = instance(this);
    if (!data) return false;
    focusRegion(data);
    var done = unlinkAt(data);
    sync(this, data);
    bump(this);
    return done;
  },
  linkHref: function () {
    var link = this.revision >= 0 ? anchorAt(instance(this)) : null;
    return link ? link.getAttribute("href") || "" : "";
  },

  // Put HTML (cleaned) at the caret.
  insert: function (html) {
    var data = instance(this);
    if (!data) return false;
    focusRegion(data);
    var done = execute(data, "insertHTML", clean(data, text(html)));
    sync(this, data);
    bump(this);
    return done;
  },
  // Replace the content; "" empties it. The new content is the clean baseline.
  set: function (html) {
    var data = instance(this);
    var region = area(data);
    if (!region) return this.html;
    region.innerHTML = clean(data, text(html));
    data.baseline = region.innerHTML;
    sync(this, data);
    return this.html;
  },
  clear: function () { return this.set(""); },
  focus: function () { focusRegion(instance(this)); return true; },
  plain: function () {
    var region = area(instance(this));
    return region ? (region.textContent || "").replace(/\u00a0/g, " ") : "";
  },
  markdown: function () { return this.revision >= 0 && this.html !== undefined ? markdown(instance(this)) : ""; }
});

})();
