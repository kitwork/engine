/* editor component — rich text written in place, in the one grammar a saved post is shown in
 * (Kitwork UI's <editor>; the grammar is utilities/richtext's, v2). (A template page's editor,
 * v1.0.0, went on 26/10/2026.)
 *
 * The author draws everything; the engine writes the parts this file finds:
 *
 *   <editor as="$body">
 *     <box alt="Định dạng"> <group alt="Kiểu chữ"> <button click="toggle('bold')">…</button> … </group>
 *       <group alt="Tiêu đề"> … <dropdown> <button click="toggle()">…</button>
 *         <popover> <button click="$body.toggle('heading', 4)">H4</button> … </popover> </dropdown> </group>
 *       … <button click="align('center')">…</button> … <button click="rule()">…</button> … </box>
 *     <popover> <input value="{{ address }}" alt="Địa chỉ liên kết" /> <button click="link(address)">Gắn</button>
 *       <button click="unlink()">Bỏ liên kết</button> <button click="close()">Huỷ</button> <error>…</error> </popover>
 *     <content name="body" value="{{ post.body }}" required="true" />
 *   </editor>
 *
 * as data-kitwork-component="editor: $body" on the box the tag builds, with its first state in
 * data-kitwork-scope; the writing area a div.richtext.editor-area (role textbox, no contenteditable
 * yet) holding the cleaned value, then textarea.editor-field with the field's name holding it too;
 * each toolbar role="toolbar" (.editor-toolbar), its groups role="group"; anywhere in the host (a
 * dropdown's popover in a toolbar too) the format buttons data-kitwork-format="bold|italic|underline|
 * strikethrough|code|code-block|quote|heading-2…heading-6|list|list-ordered" with aria-pressed, the
 * other buttons data-kitwork-command="align-left|align-center|align-right" (aria-pressed too) or
 * "rule|link|unlink|undo|redo|close"; the popover .editor-popover (role dialog), its input
 * .editor-address and its error .editor-address-error (hidden, an id of its own).
 *
 *   empty, words, characters  the value's, counted as richtext counts them; read-only
 *   unsaved   the value differs from the one at ready (always, on a page that answers a refused save:
 *             the server's unsaved: true); read-only
 *   open      the link popover shows; read-only. address: what its input holds (two-way there)
 *   format    'rich', 'markdown' or 'html' (21/10/2026): how the post is seen and typed, one value on
 *             the wire either way (the field's canonical HTML). Written by a format button's one
 *             assignment (format = 'markdown': data-kitwork-command="format", data-kitwork-format-value)
 *             or a <select> bound to it; the start is the scope's (format="markdown" on <editor>).
 *             Markdown and HTML are a part's (parts/editor-format.js, loaded at the first switch away
 *             from rich or at such a start; Markdown's reading and writing parts/editor-markdown.js)
 *   dropped   the tag names the last switch from HTML or Markdown, or a paste, left out, in the order met,
 *             each once ([] when none); read-only
 *   toggle(format[, variant])  'bold', 'italic', 'underline', 'strikethrough', 'code' (in a line),
 *             'code' with 'block', 'quote', 'heading' with 2 to 6, 'list'[, 'ordered']: on the
 *             selection, off when all of it has it already (a block off is a paragraph); with a
 *             caret, a mark waits there for what is typed next, in no element until then (a caret
 *             moved, it no longer does). During an IME composition it waits for the end.
 *   align(side)  'left', 'center' or 'right': the paragraphs and headings of the selection (an item
 *             or a code block takes none; left is no class); nothing before the area's first caret
 *   rule()    a rule after the caret's block (its list or quote split there), an empty paragraph
 *             after it when it is the last (for the caret: the value keeps the rule, not the empty
 *             paragraph); on an empty line after a block, in its place; the caret goes after it
 *   link()    keeps the selection, opens the popover with the address of the link at the caret, the
 *             input focused (nothing with a caret outside a link: the button is aria-disabled); the
 *             popover stands right under the words it links, as its place reads from them (the part)
 *   link(address)  checked by richtext.Link's rule (a bare host, kitwork.io/x, is https://): the kept
 *             selection becomes a link, or the link at the caret takes it; refused, the popover stays,
 *             the input aria-invalid and the error shown
 *   unlink()  the links at the caret or across the selection go; close() closes, the selection back
 *   undo(), redo()  the editor's own history: 100 steps, typing one step until a 500ms pause or a
 *             caret that moved
 *
 * In a code block Enter and Shift+Enter write a newline (Enter on an empty last line leaves it, a
 * paragraph after), a paste is its words as written, and marks, links and alignment are off;
 * Backspace at the start of a paragraph after one joins the paragraph's words to its last line. A
 * command pressed in a dropdown's popover closes the dropdown once it is done, and so do Escape and the
 * focus leaving it. A caret of the area itself (beside a rule) is the end of the line before it for
 * every command (within).
 *
 * No execCommand. The browser types and deletes (insertText, insertReplacementText, delete*, every
 * composition input); everything else beforeinput would do (a new paragraph, a line break, a paste, a
 * drop, formats, links, history, lists) is cancelled and done here, during a composition once it ends,
 * and after each input (never during a composition) the area is put back in the grammar by renaming
 * and unwrapping elements: a text node is moved, never changed, and the caret stays where it was. A
 * paste is read (DOMParser, inert) onto the grammar and built with createElement; so is a drop from
 * elsewhere, taken at its drop event, before the browser builds anything of it. That reading is a file
 * of its own, parts/editor-paste.js, loaded at the first paste or drop (bring); so is what puts what
 * floats over the editor in its place, the toolbars' tooltips and the link's popover right under the
 * words it links, parts/editor-place.js, loaded at the first focus in it or hover on a toolbar
 * (placing, mine.place). The value — written
 * into the field on every input, at a composition's end and on the form's formdata — is the area read
 * the way richtext.Clean reads HTML and written the way it writes it, so Clean(value) === value.
 */
var EDITOR = Symbol("kit:editor");

// toggle()'s formats and the element each is (a Go test holds this table, and ROLES, to richtext)
var FORMATS = { bold: "strong", italic: "em", underline: "u", strikethrough: "s", code: "code", "code-block": "pre", quote: "blockquote",
  "heading-2": "h2", "heading-3": "h3", "heading-4": "h4", "heading-5": "h5", "heading-6": "h6", list: "ul", "list-ordered": "ol" };
// what an element read in is, as richtext reads it: a line of its kind (center a centred paragraph,
// pre a code block), a container, a mark, a break, a rule, or gone with what it holds. Anything else
// is unwrapped, its text kept.
var ROLES = {
  p: "p div section article aside header footer main nav td th dd address details dialog dl dt fieldset figcaption figure hgroup legend summary table caption tr search",
  center: "center", pre: "pre listing", h2: "h1 h2", h3: "h3", h4: "h4", h5: "h5", h6: "h6", blockquote: "blockquote", ul: "ul menu", ol: "ol", li: "li",
  strong: "strong b", em: "em i", u: "u ins", s: "s del strike", code: "code kbd samp tt", a: "a", br: "br", hr: "hr",
  drop: "form object select button script style textarea title xmp iframe noembed noframes noscript template plaintext svg math"
};
var ROLE = {};
Object.keys(ROLES).forEach(function (role) {
  ROLES[role].split(" ").forEach(function (name) { ROLE[name.toUpperCase()] = role; });
});
// roleOf is what an element is; one of SVG or MathML (an <svg>, a <math>, its tag in lower case) goes
// with all it holds, as richtext skips a foreign element whole
function roleOf(el) { return el.namespaceURI === "http://www.w3.org/1999/xhtml" ? known(ROLE, el.tagName) : "drop"; }
var MARKS = ["href", "strong", "em", "u", "s", "code"]; // a run's marks, nested a > strong > em > u > s > code
var MARK = /^(strong|em|u|s|code)$/;
var BLOCK = /^(p|center|pre|h\d|blockquote|ul|ol|li)$/;
var LINES = "p,h2,h3,h4,h5,h6,li,pre";
var SPACE = /[\t\n\f\r ]+/g; // HTML's white space, the only space that collapses
var renamed = new WeakMap(); // an element renamed → the one that took its place (a kept caret follows)
var changed = false; // the normaliser moved something: the caret is put back

function own(scope) { return scope[EDITOR] || null; }
function known(map, key) { return Object.prototype.hasOwnProperty.call(map, key) ? map[key] : ""; }
function make(tag) { return document.createElement(tag); }
function list(nodes) { return Array.from(nodes); }
function index(node) { return list(node.parentNode.childNodes).indexOf(node); }
function selection() { return document.getSelection(); }
function collapse(node, offset) { selection().collapse(node, offset); }
function caretAfter(node) { collapse(node.parentNode, index(node) + 1); }
function words(text) { return /[^\t\n\f\r ]/.test(text); }
function blank(node) { return !/[^\s\u200b]/.test(node.textContent); }
function around(mine, node, selector) {
  var el = node && (node.nodeType === 1 ? node : node.parentNode);
  el = el && el.closest(selector);
  return el && el !== mine.area && mine.area.contains(el) ? el : null;
}
function lineOf(mine, node) { return around(mine, node, LINES); }
function isCode(line) { return line.tagName === "PRE"; }
function codeOf(pre) { return pre.querySelector("code") || pre; }
// alignable: a paragraph (of a quote too) or a heading; sideOf is its alignment as the area writes it
function alignable(line) { return /^(P|H\d)$/.test(line.tagName); }
function sideOf(line) { return (line.getAttribute("class") || "align-left").slice(6); }

// safeLink is richtext.Link's rule for a link's address (utilities/richtext/link.go): C0 controls out,
// spaces in front dropped, a backslash read as a slash, then a web page, an e-mail, a phone number or a
// path on the site ("/x", never "//host"); "" when refused. Typed in the popover, it is trimmed and a
// bare host (kitwork.io/x) is a web page.
function safeLink(text, typed) {
  var href = String(text == null ? "" : text);
  if (typed) {
    href = href.trim();
    if (/^[\w-]+(\.[\w-]+)+(:\d+)?([\/?#]|$)/.test(href)) href = "https://" + href;
  }
  href = href.replace(/[\0-\x1f]/g, "").replace(/^ +/, "").replace(/\\/g, "/");
  return /^(https?|mailto|tel):|^\/(?!\/)/i.test(href) ? href : "";
}

// styleOf is the last declaration of a property in an element's style, trimmed, without !important,
// in lower case (richtext's styleValue); null when it writes none
function styleOf(el, property) {
  var value = null;
  (el.getAttribute("style") || "").split(";").forEach(function (declaration) {
    var at = declaration.indexOf(":");
    if (at >= 0 && declaration.slice(0, at).trim().toLowerCase() === property) value = declaration.slice(at + 1).trim().replace(/!important$/, "").trim().toLowerCase();
  });
  return value;
}
// alignOf is the alignment a block element writes for itself, read as richtext reads it (tree.go,
// explicitAlignment): text-align in its style, else its last align-center or align-right class, else
// its align attribute (on p, div, h1–h6, tr, td and th), else centre for <center>; "" when it writes
// none, so the one around it holds
var STYLED = { center: "center", right: "right", end: "right", left: "left", start: "left", justify: "left" };
var ATTRIBUTED = { center: "center", middle: "center", right: "right", left: "left", justify: "left" };
function alignOf(el) {
  var found = "";
  (el.getAttribute("class") || "").split(SPACE).forEach(function (name) { if (/^align-(center|right)$/.test(name)) found = name.slice(6); });
  return known(STYLED, styleOf(el, "text-align")) || found ||
    /^(P|DIV|H[1-6]|T[RDH])$/.test(el.tagName) && known(ATTRIBUTED, (el.getAttribute("align") || "").toLowerCase()) || (el.tagName === "CENTER" ? "center" : "");
}

// ---- reading: a tree (the area, or a paste) as richtext reads HTML ----
// blocks are { tag: "p" | "h2"…"h6", align, runs }, { tag: "blockquote" | "ul" | "ol", parts: [runs…] }
// (a quote's parts with their align), { tag: "pre", code } or { tag: "hr" }; a run is { text } or
// { br }, with the marks around it (href, strong, em, u, s, code). map, for a paste, reads what an
// element says in style (parts/editor-paste.js).
function read(root, map) {
  var blocks = [], open = false, pending = false, stray = false, newLine = false;
  function add(block) { blocks.push(block); stray = false; return block; }
  function close() { open = pending = false; }
  // a block ends: in a list item the item goes on, its next words on a line of their own; in a code
  // block its next text does
  function boundary(at) {
    if (at.verbatim) newLine = true;
    else if (at.place === "list") pending = pending || open;
    else close();
  }
  function line(at) {
    var last = blocks[blocks.length - 1], part;
    if (!open && at.link && at.link.line) at.link.line = -1; // a link holds the words of one line
    if (at.place === "doc") {
      if (!open) { last = add({ tag: at.kind, align: at.align, runs: [] }); open = true; }
      return last.runs;
    }
    if (!open) {
      last.parts.push(part = []);
      if (last.tag === "blockquote") part.align = at.align;
      open = true;
      pending = false;
    }
    part = last.parts[last.parts.length - 1];
    if (pending) { part.push(run({ br: 1 }, at)); pending = false; }
    return part;
  }
  // code is text into the open code block as written, on a new line after a block inside it
  function code(text) {
    var last = blocks[blocks.length - 1];
    if (!text) return;
    if (newLine && last.code && last.code.slice(-1) !== "\n") last.code += "\n";
    newLine = false;
    last.code += text;
  }
  function text(data, at) {
    data = data.replace(/[\0\u200b\ufeff]/g, "").replace(/\u00a0/g, " "); // no NFC: richtext keeps the text as written
    if (at.verbatim) return code(data);
    if (!data || (!open || pending) && !words(data)) return;
    line(at).push(run({ text: data }, at));
  }
  function walk(node, at) {
    for (var child = node.firstChild; child; child = child.nextSibling) {
      if (child.nodeType === 3) {
        if (!at.pre) text(child.data, at);
        else child.data.split("\n").forEach(function (piece, i) {
          if (i) line(at).push(run({ br: 1 }, at));
          text(piece, at);
        });
      } else if (child.nodeType === 1) element(child, at);
    }
  }
  function element(el, at) {
    var role = roleOf(el), inner = Object.assign({}, at), block = BLOCK.test(role), side;
    if (role === "drop") {
      if (/^(form|xmp|plaintext)$/.test(el.localName)) boundary(at); // blocks: the line before them ends
      return;
    }
    if (role === "br") return at.verbatim ? code("\n") : line(at).push(run({ br: 1 }, at));
    if (role === "hr") {
      // a rule stands among the blocks of the document; in a list, a quote or a code block a line ends
      if (at.place !== "doc" || at.verbatim) return boundary(at);
      close();
      return add({ tag: "hr" });
    }
    if (at.verbatim) {
      // a code block holds text only: a mark or a link inside it is nothing, a block a new line
      if (block) boundary(at);
      walk(el, inner);
      if (block) boundary(at);
      return;
    }
    if (block && (side = alignOf(el))) inner.align = side; // held by the lines inside, as CSS inherits it
    // a paste reads what Google Docs and Word say in style too (map: parts/editor-paste.js), which may
    // change what the element is, or leave it out
    if (map && (role = map(el, at, inner, role, block)) === "drop") return;
    switch (role) {
      case "strong": case "em": case "u": case "s": case "code": inner[role] = true; break;
      case "a": inner.href = safeLink(el.getAttribute("href")); inner.link = {}; break;
      case "pre":
        if (at.place === "doc") {
          close();
          var pre = add({ tag: "pre", code: "" });
          newLine = false;
          inner.verbatim = true;
          walk(el, inner);
          // CR LF and CR are LF over its whole text, as richtext makes them (a CR and an LF in two
          // texts are one newline)
          pre.code = pre.code.replace(/\r\n?/g, "\n");
          return;
        }
        inner.pre = inner.code = true; // in a list or a quote: code in the line, its line breaks kept
        // falls through
      case "p": case "center": case "h2": case "h3": case "h4": case "h5": case "h6":
        boundary(at);
        inner.kind = /^h/.test(role) ? role : "p";
        walk(el, inner);
        boundary(at);
        return;
      case "blockquote": case "ul": case "ol": case "li":
        if (at.place === "doc") {
          // a quote or a list of the document; a stray item makes a list, or joins the stray list before
          close();
          if (role !== "li" || !stray) add({ tag: role === "li" ? "ul" : role, parts: [] });
          inner.place = role === "blockquote" ? "quote" : "list";
          walk(el, inner);
          close();
          stray = role === "li";
        } else if (at.place === "list" && role !== "blockquote") {
          // a nested list's items, and an item, join the list: what follows is a new item
          close();
          walk(el, inner);
          close();
        } else {
          // in a quote a list is read as paragraphs, and so is a quote in a list
          boundary(at);
          walk(el, inner);
          boundary(at);
        }
        return;
    }
    walk(el, inner);
  }
  walk(root, { place: "doc", kind: "p", align: "" });
  // what holds nothing goes; a rule stands after a block (first or after a rule it goes; one that ends
  // the post stays, so the empty paragraph rule() puts after it for the caret goes and the rule stays)
  var kept = [];
  blocks.forEach(function (block) {
    var last = kept[kept.length - 1];
    if (block.tag === "hr" ? last && last.tag !== "hr" : block.tag === "pre" ? words(block.code) : !block.parts ? (block.runs = tidy(block.runs)) :
      (block.parts = block.parts.map(function (part) { var tidied = tidy(part); if (tidied) tidied.align = part.align; return tidied; }).filter(Boolean)).length) kept.push(block);
  });
  return kept;
}

function run(r, at) {
  var link = at.link;
  if (link && !link.line && !r.br && words(r.text)) link.line = 1; // its first word: the link's line
  r.href = link && link.line < 0 ? "" : at.href || "";
  MARKS.forEach(function (mark, i) { if (i) r[mark] = !!at[mark]; });
  return r;
}
function same(a, b) { return MARKS.every(function (mark) { return a[mark] === b[mark]; }); }

// tidy is richtext's: white space collapsed as a browser lays it out (none at either end of a line nor
// next to a break), breaks at either end dropped, runs with the same marks joined; null without words
function tidy(runs) {
  var out = [], space = true;
  function trim() {
    var last = out[out.length - 1];
    if (last && !last.br && !(last.text = last.text.replace(/ $/, ""))) out.pop();
  }
  runs.forEach(function (r) {
    if (r.br) { trim(); out.push(r); space = true; return; }
    var piece = r.text.replace(SPACE, " ");
    if (space && piece.charAt(0) === " ") piece = piece.slice(1);
    if (!piece) return;
    space = piece.charAt(piece.length - 1) === " ";
    var last = out[out.length - 1];
    if (last && !last.br && same(last, r)) last.text += piece;
    else out.push(run({ text: piece }, r));
  });
  trim();
  while (out.length && out[0].br) out.shift();
  while (out.length && out[out.length - 1].br) { out.pop(); trim(); }
  return out.some(function (r) { return !r.br; }) ? out : null;
}

// ---- writing: richtext's serialization, and the counts ----
var ENTITY = { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "\u00a0": "&nbsp;" };
function escape(text, attribute) {
  return text.replace(attribute ? /[&<>"\u00a0]/g : /[&<>]/g, function (c) { return ENTITY[c]; });
}
function phrase(runs, level) {
  if (level === MARKS.length) return runs.map(function (r) { return r.br ? "<br>" : escape(r.text); }).join("");
  var key = MARKS[level], out = "";
  for (var i = 0, j; i < runs.length; i = j) {
    for (j = i + 1; j < runs.length && runs[j][key] === runs[i][key]; j++);
    var mark = runs[i][key], inner = phrase(runs.slice(i, j), level + 1);
    out += !mark ? inner : level ? "<" + key + ">" + inner + "</" + key + ">" : '<a href="' + escape(mark, true) + '">' + inner + "</a>";
  }
  return out;
}
function head(tag, side) { return "<" + tag + (/^(center|right)$/.test(side) ? ' class="align-' + side + '"' : "") + ">"; }
function textOf(runs) { return runs.map(function (r) { return r.br ? "\n" : r.text; }).join(""); }

// serialize is richtext's write and plainText: every block on a line of its own, a code block's text
// as written, a rule one more blank line in the text (one that ends the post nothing)
function serialize(blocks) {
  var text = "";
  var html = blocks.map(function (block, i) {
    if (block.tag === "hr") { if (blocks[i + 1]) text += "\n"; return "<hr>"; }
    if (i) text += "\n\n";
    if (block.tag === "pre") { text += block.code; return "<pre><code>" + escape(block.code) + "</code></pre>"; }
    if (!block.parts) { text += textOf(block.runs); return head(block.tag, block.align) + phrase(block.runs, 0) + "</" + block.tag + ">"; }
    var inner = block.tag === "blockquote" ? "p" : "li";
    text += block.parts.map(textOf).join(inner === "p" ? "\n\n" : "\n");
    return "<" + block.tag + ">\n" + block.parts.map(function (part) {
      return head(inner, part.align) + phrase(part, 0) + "</" + inner + ">\n";
    }).join("") + "</" + block.tag + ">";
  }).join("\n");
  return { html: html, text: text };
}
// value is the area's value, the canonical HTML, with richtext's counts
function value(area) {
  var now = serialize(read(area)), text = now.text;
  now.words = (text.match(/[^\s\u0085]+/g) || []).filter(function (word) { return /[\p{L}\p{Nd}]/u.test(word); }).length;
  now.characters = Array.from(text.replace(/\n/g, "")).length;
  return now;
}

// ---- the normaliser: the area put back in the grammar after the browser's own edits ----
function unwrap(el) {
  var parent = el.parentNode;
  while (el.firstChild) parent.insertBefore(el.firstChild, el);
  parent.removeChild(el);
  changed = true;
}
// rename gives an element's place and children to one of another tag (its alignment with them: the
// normaliser takes it off where none is held)
function rename(el, tag) {
  if (el.tagName.toLowerCase() === tag) return el;
  var next = make(tag);
  if (el.hasAttribute("class")) next.setAttribute("class", el.getAttribute("class"));
  while (el.firstChild) next.appendChild(el.firstChild);
  el.parentNode.replaceChild(next, el);
  renamed.set(el, next);
  changed = true;
  return next;
}
function wrap(before, tag) {
  var el = make(tag);
  before.parentNode.insertBefore(el, before);
  changed = true;
  return el;
}
// apart puts a block's words, unwrapped where it stands, on a line of their own
function apart(node) {
  if (node.previousSibling) node.parentNode.insertBefore(make("br"), node);
  if (node.nextSibling) node.parentNode.insertBefore(make("br"), node.nextSibling);
}
// shape keeps what parent holds to the grammar at its place: the document (area), a quote (p only),
// a list (li only) or a line (text, the marks, a, br). Phrasing between blocks goes into a paragraph
// (an item in a list), a block in a line is unwrapped on lines of its own, a rule stays among the
// blocks of the document only, and every attribute but a link's href and the one alignment class of a
// paragraph or a heading (not of an item) goes.
var KEEP = { doc: ["p", "h2", "h3", "h4", "h5", "h6", "blockquote", "ul", "ol", "pre", "hr"], quote: ["p"], list: ["li"] };
var MAKE = { doc: "p", quote: "p", list: "li" };
function shape(parent, place) {
  var holder = null; // the paragraph or item phrasing between blocks goes into
  for (var node = parent.firstChild, next; node; node = next) {
    next = node.nextSibling;
    if (node.nodeType !== 1) {
      if (node.nodeType !== 3) { parent.removeChild(node); changed = true; }
      else if (place !== "line" && (holder || words(node.data))) (holder = holder || wrap(node, MAKE[place])).appendChild(node);
      continue;
    }
    var role = roleOf(node), side;
    if (role === "center" || role === "pre" && place !== "doc") role = "p";
    side = /^(p|h\d)$/.test(role) && place !== "list" && alignOf(node);
    side = /^(center|right)$/.test(side) ? "align-" + side : "";
    node.getAttributeNames().forEach(function (name) {
      if (!(role === "a" && name === "href" || name === "class" && side && node.getAttribute(name) === side)) { node.removeAttribute(name); changed = true; }
    });
    if (role === "a") {
      var href = safeLink(node.getAttribute("href"));
      if (!href || node.parentNode.closest("a")) role = "";
      else if (href !== node.getAttribute("href")) node.setAttribute("href", href);
    }
    if (role === "drop" || role === "hr" && place !== "doc") { parent.removeChild(node); changed = true; continue; }
    if (!role) { next = node.firstChild || next; unwrap(node); continue; }
    if (MARK.test(role) || role === "a" || role === "br") {
      if (place !== "line") (holder = holder || wrap(node, MAKE[place])).appendChild(node);
      node = rename(node, role);
      if (role !== "br") shape(node, "line");
      continue;
    }
    if (place === "line") {
      // a block inside a line: its words stay, on a line of their own
      apart(node);
      next = node.firstChild || node.nextSibling;
      unwrap(node);
      continue;
    }
    holder = null;
    if (place !== "doc" && (role === "ul" || role === "ol" || role === "blockquote")) {
      next = node.firstChild || next; // a list in a quote, a quote in a list: what it holds stays here
      unwrap(node);
      continue;
    }
    node = rename(node, KEEP[place].indexOf(role) >= 0 ? role : MAKE[place]);
    if (side && node.getAttribute("class") !== side) { node.setAttribute("class", side); changed = true; }
    if (isCode(node)) shapeCode(node);
    else if (role !== "hr") shape(node, node.tagName === "BLOCKQUOTE" ? "quote" : /^[UO]L$/.test(node.tagName) ? "list" : "line");
  }
}
// shapeCode keeps a code block to one code holding text and line breaks: what else the pre holds goes
// into it, a mark or a link in it is unwrapped (a block's words on a line of their own), a rule goes
function shapeCode(pre) {
  var code = pre.firstChild;
  if (!code || code.nodeName !== "CODE") { code = pre.insertBefore(make("code"), code); changed = true; }
  while (code.nextSibling) { code.appendChild(code.nextSibling); changed = true; }
  [code].concat(list(code.querySelectorAll("*"))).forEach(function (el) {
    if (!code.contains(el)) return;
    el.getAttributeNames().forEach(function (name) { el.removeAttribute(name); changed = true; });
    var role = roleOf(el);
    if (el === code || role === "br") return;
    if (role === "drop" || role === "hr") { el.remove(); changed = true; return; }
    if (BLOCK.test(role)) apart(el);
    unwrap(el);
  });
}
// point is a place kept across moves: a text node and an offset, or the node a place is before
function point(node, offset) {
  return node.nodeType === 3 || !node.childNodes[offset] ? [node, offset] : [node.childNodes[offset], -1];
}
function spot(at) {
  var node = at[0];
  if (at[1] < 0) return node.parentNode ? [node.parentNode, index(node)] : null;
  while (renamed.has(node)) node = renamed.get(node);
  return [node, Math.min(at[1], node.nodeType === 3 ? node.length : node.childNodes.length)];
}
// inside is the selection when it lies in the area, both its ends
function inside(mine) {
  var s = selection();
  return s.rangeCount && mine.area.contains(s.anchorNode) && mine.area.contains(s.focusNode) ? s : null;
}
function save(mine) {
  var s = inside(mine);
  return s && [point(s.anchorNode, s.anchorOffset), point(s.focusNode, s.focusOffset)];
}
function restore(mine, saved) {
  var a = saved && spot(saved[0]), b = saved && spot(saved[1]);
  if (a && b && mine.area.contains(a[0]) && mine.area.contains(b[0])) selection().setBaseAndExtent(a[0], a[1], b[0], b[1]);
}
// normalise runs after every input outside a composition. Two quotes or two lists of a kind side by
// side (the browser's Backspace joins a paragraph to the item before, and leaves the list after it
// beside) are one. An area the browser left with no words and nothing typed into, a lone paragraph
// (<p><br></p> once the last letter went), is emptied: the placeholder is drawn on an empty area
// (:empty), and the browser types into an empty one, a paragraph made after. A lone heading, item,
// quote or code block stays what it is (Unikey's dd is Backspace then đ: the đ goes into the heading).
// A rule that ends the post (a value may since 21/10/2026) has an empty paragraph after it, as rule()
// puts one there: a line for the caret (a click under the rule, an undo, Control+End), which the value
// leaves out while it is empty; without it Chrome's caret stands beside the rule and a click under it
// types above it.
function normalise(mine, typed) {
  var saved = save(mine), area = mine.area, last;
  changed = false;
  shape(area, "doc");
  list(area.children).forEach(function (el) { if (el.isConnected && /^(UL|OL|BLOCKQUOTE)$/.test(el.tagName)) join(el); });
  if (!mine.blocked && (last = area.lastChild) && last.nodeName === "HR") { fill(area.appendChild(make("p"))); changed = true; }
  if (typed && area.childNodes.length === 1 && area.firstChild.nodeName === "P" && !area.textContent) area.textContent = "";
  else if (changed) restore(mine, saved);
}

// ---- editing primitives ----
// cut splits what lies between a point and top, so that the point falls between two children of top,
// and returns the child after it (null at the end): the text is split there, and every element on the
// way is split in two, a copy of it taking what comes after.
function cut(top, node, offset) {
  var parent = node, ref = node.childNodes && node.childNodes[offset] || null;
  if (node.nodeType === 3) {
    parent = node.parentNode;
    ref = offset <= 0 ? node : offset >= node.length ? node.nextSibling : node.splitText(offset);
  }
  while (parent !== top) {
    var up = parent.parentNode;
    if (ref === parent.firstChild) ref = parent;
    else if (!ref) ref = parent.nextSibling;
    else {
      var copy = parent.cloneNode(false);
      while (ref) { var after = ref.nextSibling; copy.appendChild(ref); ref = after; }
      up.insertBefore(copy, parent.nextSibling);
      ref = copy;
    }
    parent = up;
  }
  return ref;
}
function splitLine(line, node, offset) {
  var ref = cut(line, node, offset), next = line.cloneNode(false);
  while (ref) { var after = ref.nextSibling; next.appendChild(ref); ref = after; }
  line.parentNode.insertBefore(next, line.nextSibling);
  return next;
}
function fill(line) { if (blank(line) && !line.querySelector("br")) line.appendChild(make("br")); }
// lift takes a line out of its quote or list, which is split around it
function lift(mine, line) {
  var container = line.parentNode;
  if (container === mine.area) return line;
  var rest = tail(line);
  container.parentNode.insertBefore(line, rest);
  [rest, container].forEach(function (part) { if (!part.firstElementChild) part.remove(); });
  return line;
}
// tail moves what follows node in its container into a copy of the container, put right after it
function tail(node) {
  var container = node.parentNode, rest = container.cloneNode(false);
  while (node.nextSibling) rest.appendChild(node.nextSibling);
  return container.parentNode.insertBefore(rest, container.nextSibling);
}
// join puts a quote or a list together with one of its kind beside it
function join(el) {
  [el.previousElementSibling, el.nextElementSibling].forEach(function (other, i) {
    if (!other || other.tagName !== el.tagName) return;
    var from = i ? other : el, to = i ? el : other;
    while (from.firstChild) to.appendChild(from.firstChild);
    from.remove();
    el = to;
    changed = true;
  });
}
function formatOf(line) {
  var tag = line.tagName, outer = line.parentNode.tagName;
  return tag === "LI" ? (outer === "OL" ? "list-ordered" : "list") : tag === "P" ? (outer === "BLOCKQUOTE" ? "quote" : "p") : isCode(line) ? "code-block" : "heading-" + tag.charAt(1);
}
function blankBefore(line, node, offset) {
  var probe = document.createRange();
  probe.setStart(line, 0);
  probe.setEnd(node, offset);
  return !probe.toString().replace(/\u200b/g, "");
}
// the lines a range holds: a line it only touches at the start, after the line it starts in, is not one
function lines(mine, r) {
  var first = lineOf(mine, r.startContainer);
  return list(mine.area.querySelectorAll(LINES)).filter(function (line) {
    return r.intersectsNode(line) && (line === first || r.collapsed || !line.contains(r.endContainer) || !blankBefore(line, r.endContainer, r.endOffset));
  });
}
// verbatim: every line of the range is a code block, where marks, links and alignment are no part
function verbatim(mine, r) {
  var held = lines(mine, r);
  return held.length > 0 && held.every(isCode);
}
// each cuts every line of a range (but a code block, which takes no mark) so the selected part is
// whole children of the line, and hands them over
function each(mine, r, fn) {
  var sc = r.startContainer, so = r.startOffset, ec = r.endContainer, eo = r.endOffset, leaves = [];
  var gather = function (node) {
    if (!node.firstChild) leaves.push(node);
    for (var child = node.firstChild; child; child = child.nextSibling) gather(child);
  };
  lines(mine, r).forEach(function (line) {
    if (isCode(line)) return;
    var end = line.contains(ec) ? cut(line, ec, eo) : null;
    var start = line.contains(sc) ? cut(line, sc, so) : line.firstChild;
    var nodes = [];
    for (var node = start; node && node !== end; node = node.nextSibling) nodes.push(node);
    nodes.forEach(gather);
    if (nodes.length) fn(line, nodes);
  });
  // what was selected stays selected: its texts and breaks are moved, never removed
  leaves = leaves.filter(function (leaf) { return mine.area.contains(leaf); });
  if (leaves.length) {
    var range = document.createRange();
    range.setStartBefore(leaves[0]);
    range.setEndAfter(leaves[leaves.length - 1]);
    selection().setBaseAndExtent(range.startContainer, range.startOffset, range.endContainer, range.endOffset);
  }
}
function textsIn(root) {
  var out = [], walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  while (walker.nextNode()) out.push(walker.currentNode);
  return out;
}
function wrapNodes(line, nodes, tag) {
  var el = line.insertBefore(make(tag), nodes[0]);
  nodes.forEach(function (node) { el.appendChild(node); });
  list(el.querySelectorAll(tag)).forEach(unwrap);
  return el;
}
// removeSelection deletes what is selected, the line it ends in joined to the line it starts in, the
// caret where the first line's words now end (deleteContents leaves it between the two lines)
function removeSelection(mine, r) {
  if (r.collapsed) return r;
  var from = lineOf(mine, r.startContainer), to = lineOf(mine, r.endContainer);
  r.deleteContents();
  if (from && to && from !== to && to.isConnected) {
    var tail = from.lastChild, container = to.parentNode;
    if (isCode(to) && !isCode(from)) {
      // from a line into a code block: the words of its first line join the line, as text, and its
      // other lines stay the code block (as the browser's own delete leaves them)
      var code = flat(to), at = code.indexOf("\n");
      from.appendChild(document.createTextNode(at < 0 ? code : code.slice(0, at)));
      if (at >= 0 && words(code = code.slice(at + 1))) codeOf(to).textContent = code;
      else to.remove();
    } else {
      while (to.firstChild) from.appendChild(to.firstChild);
      to.remove();
    }
    if (container !== mine.area && !container.firstElementChild) container.remove();
    if (!tail) collapse(from, 0);
    else if (tail.nodeType === 3) collapse(tail, tail.length);
    else caretAfter(tail);
    r = selection().getRangeAt(0);
  }
  return r;
}
// flat is the text a range of a code block holds (or all an element holds), each line break a newline
function flat(part) {
  part = part.nodeType ? part.cloneNode(true) : part.cloneContents();
  list(part.querySelectorAll("br")).forEach(function (br) { br.replaceWith("\n"); });
  return part.textContent;
}

// ---- the commands ----
// marked: does all of a range have the mark (with a caret: the place it is at); a code block's text
// has none
function marked(mine, r, tag) {
  if (r.collapsed) return !!around(mine, r.startContainer, tag);
  var root = r.commonAncestorContainer, texts = root.nodeType === 3 ? [root] : textsIn(root);
  texts = texts.filter(function (text) {
    return r.intersectsNode(text) && /[^\s\u200b]/.test(text.data) && !(text === r.endContainer && !r.endOffset) && !(text === r.startContainer && r.startOffset >= text.length) && !around(mine, text, "pre");
  });
  return texts.length > 0 && texts.every(function (text) { return around(mine, text, tag); });
}
function has(mine, r, format, held) {
  var tag = known(FORMATS, format);
  if (!r || !tag) return false;
  if (MARK.test(tag)) {
    var p = mine.pending;
    return p && r.collapsed && format in p.formats ? p.formats[format] : marked(mine, r, tag); // a mark waiting at the caret
  }
  return held.length > 0 && held.every(function (line) { return formatOf(line) === format; });
}
function mark(mine, r, format) {
  if (verbatim(mine, r)) return;
  var tag = FORMATS[format], off = marked(mine, r, tag);
  if (r.collapsed) {
    // nothing selected: the mark is for what is typed from here (settle), on or off as the button then
    // says, nothing written yet; pressed again, the other way, from where the caret is. A caret just
    // after a text is at its end, where the browser types.
    var p = mine.pending = mine.pending || { formats: {} }, before = r.startContainer.childNodes[r.startOffset - 1];
    if (before && before.nodeType === 3) r = collapse(before, before.length) || selection().getRangeAt(0);
    p.formats[format] = !(format in p.formats ? p.formats[format] : off);
    p.at = r.cloneRange();
    p.end = r.cloneRange();
    return true;
  }
  each(mine, r, function (line, nodes) {
    if (!off) return wrapNodes(line, nodes, tag);
    nodes.forEach(function (node) {
      if (node.nodeType !== 1) return;
      list(node.querySelectorAll(tag)).forEach(unwrap);
      if (node.tagName.toLowerCase() === tag) unwrap(node);
    });
  });
  return true;
}
function block(mine, r, format) {
  var held = lines(mine, r);
  if (!held.length) return;
  var target = held.every(function (line) { return formatOf(line) === format; }) ? "p" : format;
  if (target === "code-block") return codify(mine, held);
  var outer = known(FORMATS, target) || "p", inner = outer === "blockquote" ? "p" : outer === "ul" || outer === "ol" ? "li" : outer;
  var moved = held.map(function (line) { return rename(lift(mine, isCode(line) ? uncode(mine, line) : line), inner); }), made = [];
  // the lines side by side go into one quote or list: a rule between two of them keeps its place
  if (outer !== inner) moved.forEach(function (line) {
    var last = made[made.length - 1];
    if (!last || last.nextSibling !== line) made.push(last = line.parentNode.insertBefore(make(outer), line));
    last.appendChild(line);
  });
  made.forEach(function (container) { if (container.isConnected) join(container); });
}
// codify makes the lines side by side one code block, a newline between two (the normaliser then takes
// their marks and links off: a code block holds text only)
function codify(mine, held) {
  var pre, code;
  held.forEach(function (line) {
    var from = lift(mine, line);
    if (pre && pre.nextSibling === from) code.appendChild(document.createTextNode("\n"));
    else code = (pre = mine.area.insertBefore(make("pre"), from)).appendChild(make("code"));
    if (isCode(from) && from.firstChild) from = from.firstChild;
    while (from.firstChild) code.appendChild(from.firstChild);
    line.remove();
  });
}
// uncode makes a code block a paragraph: its lines are the paragraph's, each newline a line break (a
// place the command keeps, after a newline, follows its words into the next text)
function uncode(mine, pre) {
  var p = rename(pre, "p"), code = p.firstChild;
  textsIn(p).forEach(function (text) {
    for (var at, rest; (at = text.data.indexOf("\n")) >= 0; text = rest) {
      (rest = text.splitText(at)).deleteData(0, 1);
      rest.parentNode.insertBefore(make("br"), rest);
      (mine.points || []).forEach(function (place) { if (place[0] === text && place[1] > at) place.splice(0, 2, rest, place[1] - at - 1); });
    }
  });
  if (code && code.nodeName === "CODE") unwrap(code);
  return p;
}
function aligning(mine, r, side) {
  lines(mine, r).forEach(function (line) { if (alignable(line)) attribute(line, "class", side !== "left" && "align-" + side); });
}
// ruling puts a rule after the caret's block (a list or a quote split after its line), an empty
// paragraph after it when no block follows (or a rule does: a line for the caret, which the value
// leaves out while it is empty, the rule kept), and the caret after it. On an empty line
// after a block the rule takes the line's place: the area shows no blank line the value has not.
function ruling(mine, r) {
  var line = lineOf(mine, r.endContainer), at = line, before = line && line.previousElementSibling;
  if (!line) return;
  if (line.parentNode !== mine.area) {
    at = line.parentNode;
    if (!tail(line).firstChild) at.nextSibling.remove();
  }
  var rule = mine.area.insertBefore(make("hr"), at.nextSibling);
  if (at === line && blank(line) && before && before.tagName !== "HR") line.remove();
  return under(mine, rule);
}
// under puts the caret at the start of the line after a rule, an empty paragraph made there when no
// block follows (or a rule does): ruling's, and a paste's or a drop's that ends in a rule (a rule is
// kept there since 21/10/2026; parts/editor-paste.js). Beside the rule, the area's own place, the
// commands would act on the line above it (inward).
function under(mine, rule) {
  var next = rule.nextElementSibling;
  if (!next || next.tagName === "HR") fill(next = mine.area.insertBefore(make("p"), next));
  collapse(next.matches(LINES) ? next : next.querySelector(LINES) || next, 0);
  return true;
}
function enter(mine, r) {
  r = removeSelection(mine, r);
  var line = lineOf(mine, r.startContainer);
  if (!line) return;
  var format = formatOf(line);
  if (isCode(line)) return newline(r, line, true);
  if (/^(list|quote)/.test(format) && blank(line)) {
    // Enter on an empty item or an empty paragraph of a quote leaves the list or the quote
    fill(line = rename(lift(mine, line), "p"));
    collapse(line, 0);
    return true;
  }
  var next = splitLine(line, r.startContainer, r.startOffset);
  if (/^heading/.test(format) && blank(next)) next = rename(next, "p"); // after a heading, a paragraph
  fill(line);
  fill(next);
  collapse(next, 0);
  return true;
}
// newline writes a newline in a code block, two when nothing follows (the browser shows no last empty
// line, and its typing takes the second away). Enter on an empty last line leaves the block: that line
// goes, a paragraph after it, or in its place when the block then holds nothing but white space.
function newline(r, pre, leaving) {
  var range = document.createRange();
  range.selectNodeContents(pre);
  range.setStart(r.startContainer, r.startOffset);
  var after = flat(range);
  range.setStart(pre, 0);
  range.setEnd(r.startContainer, r.startOffset);
  var before = flat(range), p = make("p");
  if (leaving && /^\n?$/.test(after) && /(^|\n)$/.test(before)) {
    fill(p);
    pre.parentNode.insertBefore(p, pre.nextSibling);
    if (words(before = before.slice(0, -1))) codeOf(pre).textContent = before;
    else pre.remove();
    collapse(p, 0);
    return true;
  }
  var text = document.createTextNode(after ? "\n" : "\n\n");
  r.insertNode(text);
  collapse(text, 1);
  return true;
}
function lineBreak(mine, r) {
  r = removeSelection(mine, r);
  var br = make("br"), line = lineOf(mine, r.startContainer);
  if (line && isCode(line)) return newline(r, line);
  r.insertNode(br);
  // a break with nothing shown after it (white space there collapses) gives no new line: a second one
  // holds the caret's place
  var probe = document.createRange();
  probe.setStartAfter(br);
  probe.setEnd(line, line.childNodes.length);
  if (!words(probe.toString()) && !probe.cloneContents().querySelector("br")) br.parentNode.insertBefore(make("br"), br.nextSibling);
  caretAfter(br);
  return true;
}
// joining is Backspace at the start of a paragraph of words after a code block: its words, marks gone,
// go on the code's last line as it shows (a last newline shows no line, a placeholder br after it none
// either), never where the browser's own join of the two would put them
function joining(mine, r) {
  var line = lineOf(mine, r.startContainer), pre = line.previousElementSibling, code = flat(pre).replace(/\n$/, "");
  codeOf(pre).textContent = code + flat(line);
  line.remove();
  collapse(codeOf(pre).firstChild, code.length);
  return true;
}
function linkTo(mine, r, href) {
  var link = around(mine, r.startContainer, "a");
  if (r.collapsed) {
    if (link) link.setAttribute("href", href);
    return;
  }
  each(mine, r, function (line, nodes) { wrapNodes(line, nodes, "a").setAttribute("href", href); });
  return true;
}
function unlinkAt(mine, r) {
  var link = around(mine, r.startContainer, "a");
  if (r.collapsed) { if (link) unwrap(link); return; }
  list(mine.area.querySelectorAll("a")).forEach(function (a) { if (r.intersectsNode(a)) unwrap(a); });
}
// held is what a paste or a drop carries, read while its event lasts (the browser empties it after)
function held(transfer) {
  var html = transfer && transfer.getData("text/html") || "", text = transfer && transfer.getData("text/plain") || "";
  return { getData: function (kind) { return kind === "text/html" ? html : text; } };
}
// PASTE is the file that reads what a paste or a drop brings in (parts/editor-paste.js), loaded at the
// first one, never with the page: the engine writes its name, with its hash, here (jit/js delivery.go,
// withParts). The file defines one function under PART, called once with the parts of the editor it uses.
var PASTE = "/_kitwork/parts/editor-paste.js", PART = Symbol.for("kitwork:editor-paste"), part = null, loading = null;
// PLACE is the file that puts what floats over the editor in its place (parts/editor-place.js): the
// toolbars' tooltips, and the link's popover under the words it links. It is loaded once a page at the
// first focus in the editor or hover on a toolbar (init, placing), never with the page either, and not
// again when it fails (the popover then stands where its place puts it, by the host, and no tooltip
// shows).
var PLACE = "/_kitwork/parts/editor-place.js", PLACER = Symbol.for("kitwork:editor-place"), placeFile = null;
// script loads a part's file: settled once it has run, or failed to
function script(src) {
  return new Promise(function (done) {
    var tag = document.head.appendChild(make("script"));
    tag.onload = tag.onerror = done;
    tag.src = src;
  });
}
// bring hands fn what a paste and a drop need: at once once the file is here, else when it has loaded.
// Should it not load (offline, a file of another build), a paste is its plain words at the caret.
function bring(fn) {
  var made = function () {
    return part = part || window[PART] && window[PART](hands);
  };
  if (made()) return fn(part);
  (loading = loading || script(PASTE)).then(function () {
    loading = null;
    fn(made() || { pointed: function () {}, paste: function (mine, r, transfer) {
      var node = document.createTextNode(transfer.getData("text/plain"));
      if (!node.length) return;
      removeSelection(mine, r).insertNode(node);
      collapse(node, node.length);
      return true;
    } });
  });
}
// pasteAt pastes at a place (where a drop landed, x and y the point it was let go), or at the caret,
// once the file is here (bring) and never during a composition (later)
function pasteAt(scope, at, kept, x, y) {
  var mine = own(scope);
  bring(function (taken) {
    later(mine, function () {
      if (x != null) at = taken.pointed(mine, x, y); // read when it is done: the words a composition ended with stand where they stay
      if (at && mine.area.contains(at.startContainer)) selection().setBaseAndExtent(at.startContainer, at.startOffset, at.endContainer, at.endOffset);
      command(scope, taken.paste, kept);
    });
  });
}

// hands are the parts of the editor its part files use (a paste's reading, the formats' views)
var hands = { read: read, run: run, tidy: tidy, words: words, make: make, MARKS: MARKS, styleOf: styleOf,
  safeLink: safeLink, verbatim: verbatim, linkTo: linkTo, around: around, serialize: serialize, removeSelection: removeSelection, collapse: collapse,
  lineOf: lineOf, list: list, blank: blank, alignable: alignable, attribute: attribute, splitLine: splitLine, tail: tail, join: join, caretAfter: caretAfter, under: under,
  sync: sync, after: after, show: show, bring: bring };
// VIEWS is the file that shows the post in another format than rich (parts/editor-format.js): Markdown
// or HTML in a plain-text surface where the area stands, the area kept as the value. It is loaded at
// the first switch away from rich, or at the start of an editor that starts in another format, never
// with the page; it reads the format asked (scope[FORMAT], what format = '…' wrote) each time it is
// called. Should it not load, the area stays and the format reads rich again, painted again (a slot of
// a symbol is written without a paint: the format buttons' aria-pressed is a binding of format), and
// the next switch asks for the file again (offline a moment, a deploy).
var VIEWS = "/_kitwork/parts/editor-format.js", VIEWER = Symbol.for("kitwork:editor-format"), viewFile = null;
var FORMAT = Symbol("kit:format"); // (a declaration of its own: the names' reader takes scope[FORMAT] = … for a slot of a symbol so)
function viewing(mine) {
  (viewFile = viewFile || script(VIEWS)).then(function () {
    if (window[VIEWER]) window[VIEWER](mine, hands);
    else {
      viewFile = null;
      mine.scope[FORMAT] = "";
      window.kitwork.render();
    }
  });
}

// ---- running a command ----
// inward moves a place of the area itself (beside a rule, before or after every block: where Chrome's
// arrows and a click on a rule leave its caret) into a line: the end of the nearest line before it, or
// (ahead) the start of the nearest after it, the other way when there is none
function inward(mine, node, offset, ahead) {
  var probe = document.createRange(), before, after, line, text;
  if (node !== mine.area) return [node, offset];
  probe.setStart(node, offset);
  list(node.querySelectorAll(LINES)).forEach(function (each) {
    if (probe.comparePoint(each, 0) < 0) before = each;
    else after = after || each;
  });
  line = ahead ? after || before : before || after;
  // the end of its last text that holds something: an empty one (a paste's split leaves them) is no place
  // the browser types at
  text = line && line === before && textsIn(line).filter(function (each) { return each.length; }).pop();
  return !line ? [node, offset] : text ? [text, text.length] : [line, 0];
}
// within is a range whose ends are in lines (inward): a caret beside a rule is at the end of the line
// before it, a selection from there starts at the line after it
function within(mine, r) {
  if (r.startContainer !== mine.area && r.endContainer !== mine.area) return r;
  var a = inward(mine, r.startContainer, r.startOffset, !r.collapsed), b = r.collapsed ? a : inward(mine, r.endContainer, r.endOffset);
  (r = document.createRange()).setStart(a[0], a[1]);
  r.setEnd(b[0], b[1]);
  return r;
}
// focus gives the area the focus and the selection it had (a press on the toolbar, a tap that moved
// the focus), inside one of its lines
function focus(mine) {
  var area = mine.area, s = selection(), kept = mine.kept, at, r;
  var away = !inside(mine); // read first: focusing puts a caret in
  if (document.activeElement !== area) area.focus({ preventScroll: true });
  if (away && kept && area.contains(kept.startContainer) && area.contains(kept.endContainer)) {
    s.setBaseAndExtent(kept.startContainer, kept.startOffset, kept.endContainer, kept.endOffset);
  }
  at = s.rangeCount && s.getRangeAt(0);
  if (at && (r = within(mine, at)) !== at) s.setBaseAndExtent(r.startContainer, r.startOffset, r.endContainer, r.endOffset);
  if (!s.rangeCount || !lineOf(mine, s.anchorNode)) {
    var line = area.querySelector(LINES);
    if (!line) (line = area.appendChild(make("p"))).appendChild(make("br"));
    collapse(line, 0);
  }
  return s.getRangeAt(0);
}
function command(scope, fn, argument) {
  var mine = own(scope);
  if (!mine || mine.blocked) return;
  if (mine.composing) { mine.queue.push(function () { command(scope, fn, argument); }); return; }
  var r = focus(mine), saved = mine.points = save(mine);
  if (!fn(mine, r, argument)) restore(mine, saved);
  after(mine, false);
}
// settle gives the words typed since a mark was toggled at a caret (what the browser or an input method
// wrote from that place to the caret, on its line) the marks as they were asked for, after every input:
// a letter typed, deleted and typed again (Unikey writes đ as d, Backspace, đ) keeps them. The wait goes
// on while the writing does, and ends with a caret moved elsewhere (selectionchange) or another line.
function settle(mine) {
  var p = mine.pending, s = inside(mine), r = p && p.at.cloneRange();
  if (!r) return;
  if (!s || !s.isCollapsed || lineOf(mine, r.startContainer) !== lineOf(mine, s.focusNode)) return (mine.pending = null);
  r.setEnd(s.focusNode, s.focusOffset);
  if (r.collapsed) p.at = r; // nothing typed, or deleted back past where it began: from the caret on
  else for (var format in p.formats) {
    if (marked(mine, r, FORMATS[format]) !== p.formats[format]) {
      mark(mine, r, format);
      r = selection().getRangeAt(0);
    }
  }
  selection().collapseToEnd();
  p.end = selection().getRangeAt(0).cloneRange();
}
// moved ends the wait of a mark when the caret is no longer where the writing left it: read at each
// input's start (the browser may not have said selectionchange yet) and on selectionchange
function moved(mine) {
  var p = mine.pending, s = inside(mine);
  if (p && !(s && s.isCollapsed && !p.end.comparePoint(s.focusNode, s.focusOffset))) mine.pending = null;
}
// later is now, or at the end of the composition under way: nothing touches the area during one
function later(mine, fn) {
  if (mine.composing) mine.queue.push(fn);
  else fn();
}
function after(mine, typing) {
  normalise(mine, typing);
  sync(mine);
  record(mine, typing);
  update(mine);
}
function sync(mine) {
  var scope = mine.scope, now = value(mine.area), unsaved = mine.baseline !== now.html;
  mine.field.value = now.html;
  scope.empty = !now.html;
  scope.words = now.words;
  scope.characters = now.characters;
  scope.unsaved = unsaved;
  if (now.html) mine.area.removeAttribute("aria-invalid");
  hold(mine, unsaved && !mine.sent);
  return now;
}
// hold keeps a visitor from leaving unsaved words without being asked: the browser's own question on
// unload, and Drive loading the next page whole (so that question is asked) instead of swapping it
function hold(mine, on) {
  if (mine.held === on) return;
  mine.held = on;
  if (on) mine.release = mine.context.listen(window, "beforeunload", function (event) { event.preventDefault(); event.returnValue = ""; });
  else if (mine.release) mine.release();
  var drive = window.kitwork.drive;
  if (drive && drive.hold) drive.hold(mine, on);
}

// ---- history: the area's own, as it is after each step, with the caret ----
function path(mine, node, offset) {
  var out = [offset];
  for (; node && node !== mine.area; node = node.parentNode) out.unshift(index(node));
  return out;
}
// unpath is the place a path names in the area as it is now; a rule holds no caret (the browser types
// above it): a path that ends on one is the start of the line after it (one follows every rule that
// ends the post: normalise), where the writer was when an undo brings back the rule a Backspace took
function unpath(mine, steps) {
  var node = mine.area;
  for (var i = 0; i < steps.length - 1 && node.childNodes.length; i++) node = node.childNodes[Math.min(steps[i], node.childNodes.length - 1)];
  if (node.nodeName === "HR") node = node.nextSibling || node;
  return [node, Math.min(steps[steps.length - 1], node.nodeType === 3 ? node.length : node.childNodes.length)];
}
function caretOf(mine) {
  var s = inside(mine);
  return s && [path(mine, s.anchorNode, s.anchorOffset), path(mine, s.focusNode, s.focusOffset)];
}
function snapshot(mine) { return { html: mine.area.innerHTML, caret: caretOf(mine) }; }
function record(mine, typing) {
  var next = snapshot(mine), past = mine.past, top = past[past.length - 1], now = Date.now();
  if (next.html !== top.html) {
    // typing goes on in one step until a pause of 500ms or a caret that moved in between
    if (typing && mine.typing && now - mine.at < 500 && mine.from === mine.where) past[past.length - 1] = next;
    else if (past.push(next) > 101) past.shift();
    mine.future = [];
    mine.typing = typing;
    mine.at = now;
  }
  mine.where = String(next.caret);
}
function travel(scope, back) {
  var mine = own(scope), past = mine && mine.past, future = mine && mine.future;
  if (!mine || mine.blocked || mine.composing || (back ? past.length < 2 : !future.length)) return;
  if (back) future.push(past.pop());
  else past.push(future.pop());
  // the caret of the step, or (the first, taken at ready, has none) of the step undone, where the
  // writer was
  var snap = past[past.length - 1], caret = snap.caret || back && future[future.length - 1].caret;
  focus(mine);
  mine.area.innerHTML = snap.html; // the area's own HTML, as it was after a step: never a paste's
  if (caret) {
    var a = unpath(mine, caret[0]), b = unpath(mine, caret[1]);
    selection().setBaseAndExtent(a[0], a[1], b[0], b[1]);
  }
  mine.typing = false;
  mine.pending = null;
  mine.where = String(caret);
  sync(mine);
  update(mine);
}

// ---- what the toolbar says ----
function current(mine) {
  var s = inside(mine), r = s ? s.getRangeAt(0) : mine.kept;
  return r && within(mine, r);
}
// update writes each button's state: a format pressed when all the selection has it, an alignment
// (align-left, align-center, align-right, a command of the wire with aria-pressed) when all its
// paragraphs and headings have it; in code blocks only, the marks and the link off, and an alignment
// off where the selection holds no paragraph nor heading
function update(mine) {
  var r = current(mine), held = r ? lines(mine, r) : [], code = held.length > 0 && held.every(isCode), aligned = held.filter(alignable);
  mine.formats.forEach(function (button) {
    var format = button.getAttribute("data-kitwork-format"), off = code && MARK.test(FORMATS[format]);
    button.setAttribute("aria-pressed", String(!off && has(mine, r, format, held)));
    attribute(button, "aria-disabled", (mine.blocked || off) && "true");
  });
  mine.commands.forEach(function (button) {
    var name = button.getAttribute("data-kitwork-command"), side = /^align-(\w+)/.exec(name);
    if (side) button.setAttribute("aria-pressed", String(aligned.length > 0 && aligned.every(function (line) { return sideOf(line) === side[1]; })));
    attribute(button, "aria-disabled", (mine.blocked || (side ? !aligned.length : name === "undo" ? mine.past.length < 2 : name === "redo" ? !mine.future.length :
      name === "link" && !button.closest(".editor-popover") && (code || !(r && (!r.collapsed || around(mine, r.startContainer, "a")))))) && "true");
  });
}
// stops are a toolbar's buttons the arrows go along: a dropdown's own button is one, the buttons its
// popover holds are not
function stops(bar) {
  return list(bar.querySelectorAll("button")).filter(function (button) { var pop = button.parentNode.closest(".popover"); return !pop || !bar.contains(pop); });
}
// shutMenu closes a dropdown of a toolbar (a component of its own, el its host)
function shutMenu(el) {
  var menu = el && window.kitwork.scopeFor(el);
  if (menu && typeof menu.close === "function") menu.close();
}
function attribute(el, name, value) {
  if (value) el.setAttribute(name, value);
  else el.removeAttribute(name);
}
function show(mine, on) {
  mine.scope.open = on;
  if (mine.popover) mine.popover.hidden = !on;
  if (mine.place) mine.place(); // under the words it links (parts/editor-place.js), its style gone as it shuts
  if (!on) refuse(mine, false);
}
// refuse marks the address refused, or no longer: the input aria-invalid, the error (its id the
// server's) shown and named by aria-describedby
function refuse(mine, on) {
  var input = mine.input, error = mine.error;
  if (!input) return;
  attribute(input, "aria-invalid", on && "true");
  if (error) {
    error.hidden = !on;
    attribute(input, "aria-describedby", on && error.id);
  }
}
// shut closes the popover; the selection it kept is the one the next command takes
function shut(mine) {
  if (mine.saved) mine.kept = mine.saved;
  mine.saved = null;
  show(mine, false);
}
// opening is link() with no address: the selection kept, the popover open with the address of the
// link at the caret, its input focused; nothing with a caret outside a link, nor in a code block
function opening(mine, r) {
  var link = around(mine, r.startContainer, "a"), input = mine.input;
  if (r.collapsed && !link || verbatim(mine, r)) return;
  mine.saved = r.cloneRange();
  var address = mine.scope.address = link ? link.getAttribute("href") : "";
  show(mine, true);
  if (input) {
    input.value = address;
    input.focus();
    input.select();
  }
  return true;
}

// what the browser types and deletes itself (composition apart): everything else beforeinput would do
// is the editor's. A shortcut and an input of the browser's are a call: its name and arguments.
var TYPED = /^(insert(Text|ReplacementText)|delete)/;
var KEYS = { b: ["toggle", "bold"], i: ["toggle", "italic"], u: ["toggle", "underline"], k: ["link"], z: ["undo"], y: ["redo"] };
var INPUTS = { formatBold: ["toggle", "bold"], formatItalic: ["toggle", "italic"], formatUnderline: ["toggle", "underline"],
  formatStrikeThrough: ["toggle", "strikethrough"], insertUnorderedList: ["toggle", "list"], insertOrderedList: ["toggle", "list", "ordered"],
  formatJustifyLeft: ["align", "left"], formatJustifyCenter: ["align", "center"], formatJustifyRight: ["align", "right"], insertHorizontalRule: ["rule"] };

var editorDef = {
  empty: true,
  words: 0,
  characters: 0,
  unsaved: false,
  open: false,
  address: "",
  dropped: [],

  // format: 'rich', 'markdown' or 'html', the way the post is seen and typed (one value either way: the
  // field holds the canonical HTML). A write asks for the switch (parts/editor-format.js does it); the
  // start (format: '…' in the scope, written before init) is asked at init. An editor that takes no
  // writing keeps the format it starts in: a write is nothing there (no part loaded for it, and the
  // switches' aria-pressed, a binding of format, never says another)
  get format() { return this[FORMAT] || "rich"; },
  set format(to) {
    var mine = own(this);
    if (mine && mine.blocked) return;
    this[FORMAT] = to;
    if (mine) viewing(mine);
  },

  // the formats' words are checked where the page is compiled; a call that names none does nothing
  toggle: function (format, variant) {
    var name = format === "heading" ? "heading-" + variant : format === "list" && variant === "ordered" ? "list-ordered" : format === "code" && variant === "block" ? "code-block" : format;
    var tag = known(FORMATS, name);
    if (tag) command(this, MARK.test(tag) ? mark : block, name);
  },

  // with no caret yet in the area its buttons say there is no line to align (update), and it aligns none
  align: function (side) {
    var mine = own(this);
    if (mine && current(mine) && /^(left|center|right)$/.test(side)) command(this, aligning, side);
  },

  rule: function () { command(this, ruling); },

  link: function (address) {
    var mine = own(this);
    if (!mine) return;
    if (!arguments.length) return command(this, opening);
    var href = safeLink(address, true);
    if (!href) {
      refuse(mine, true);
      if (mine.input) mine.input.focus();
      return false;
    }
    shut(mine);
    command(this, linkTo, href);
    return true;
  },

  unlink: function () {
    var mine = own(this);
    if (!mine) return;
    shut(mine);
    command(this, unlinkAt);
  },

  close: function () {
    var mine = own(this);
    if (!mine) return;
    shut(mine);
    focus(mine);
  },

  undo: function () { travel(this, true); },
  redo: function () { travel(this, false); },

  init: function (context) {
    var scope = this, host = context.host;
    var area = context.owned(".editor-area")[0], field = context.owned(".editor-field")[0];
    if (!area || !field) return;
    // the buttons are the editor's anywhere in its host, a dropdown's popover in a toolbar too
    var all = function (selector) {
      return list(host.querySelectorAll(selector)).filter(function (el) { return el.closest('[data-kitwork-component^="editor"]') === host; });
    };
    var popover = context.owned(".editor-popover")[0] || null;
    var mine = scope[EDITOR] = {
      context: context, scope: scope, area: area, field: field, form: field.form, popover: popover,
      input: popover && popover.querySelector(".editor-address"), error: popover && popover.querySelector(".editor-address-error"),
      toolbars: context.owned(".editor-toolbar"), formats: all("[data-kitwork-format]"), commands: all("[data-kitwork-command]"),
      blocked: field.readOnly || field.disabled, past: [], future: [], queue: [], heard: [],
      apple: /Mac|iP/.test(navigator.platform || navigator.userAgent)
    };
    // the server's paint, one block after another: the white space between them goes
    [area].concat(list(area.querySelectorAll("blockquote,ul,ol"))).forEach(function (holder) {
      list(holder.childNodes).forEach(function (node) { if (node.nodeType === 3 && !words(node.data)) node.remove(); });
    });
    normalise(mine);
    // the field is the form's, out of the way: still the one the browser checks (required), never shown
    field.tabIndex = -1;
    field.setAttribute("aria-hidden", "true");
    if (!mine.blocked) {
      area.contentEditable = "true";
      area.removeAttribute("aria-readonly");
    } else if (!field.disabled) area.tabIndex = 0;
    mine.baseline = scope.unsaved ? null : value(area).html; // a refused save shown again is unsaved from the start
    mine.past.push(snapshot(mine));
    if (mine.apple) all("[aria-keyshortcuts]").forEach(function (button) {
      button.setAttribute("aria-keyshortcuts", button.getAttribute("aria-keyshortcuts").replace(/Control\+/g, "Meta+"));
    });

    // what the browser types and deletes is its own, and so is every input of a composition; anything
    // else is cancelled and done here, at once or, during a composition, at its end (what a paste or a
    // drop carries read now: the browser empties it after its event)
    context.listen(area, "beforeinput", function (event) {
      var type = event.inputType || "", r = current(mine), composing = mine.composing || event.isComposing;
      if (/Composition/.test(type) || composing && TYPED.test(type)) return;
      if (!composing) {
        mine.from = String(caretOf(mine));
        moved(mine);
      }
      // Backspace at the start of a heading, quote, item or code block makes it a paragraph first; at the
      // start of a paragraph of words after a code block, its words join the code (joining)
      var line = !composing && r && r.collapsed && type === "deleteContentBackward" && lineOf(mine, r.startContainer);
      var start = line && blankBefore(line, r.startContainer, r.startOffset) &&
        (formatOf(line) !== "p" ? block : isCode(line.previousElementSibling || line) && words(line.textContent) && joining);
      if (TYPED.test(type) && !start) return;
      event.preventDefault();
      // (during a composition a paste is done at the caret it ends with: its words are not yet where
      // they will stay, so a place among them would not be either)
      var kept = /^insertFrom/.test(type) && held(event.dataTransfer), target = kept && !composing && event.getTargetRanges && event.getTargetRanges()[0], at = target && document.createRange();
      if (at) {
        at.setStart(target.startContainer, target.startOffset);
        at.setEnd(target.endContainer, target.endOffset);
      }
      later(mine, function () {
        var call = known(INPUTS, type);
        if (start) command(scope, start, formatOf(line));
        else if (type === "insertParagraph") command(scope, enter);
        else if (type === "insertLineBreak") command(scope, lineBreak);
        else if (type === "historyUndo") scope.undo();
        else if (type === "historyRedo") scope.redo();
        else if (type === "insertLink") scope.link();
        else if (call) scope[call[0]].apply(scope, call.slice(1));
        else if (kept) pasteAt(scope, at, kept);
        // anything else (a colour, a font, a full justification…) is no part of the grammar: refused
      });
    });
    context.listen(area, "input", function (event) {
      if (mine.composing || event.isComposing) return;
      settle(mine);
      after(mine, TYPED.test(event.inputType || ""));
    });
    context.listen(area, "compositionstart", function () {
      moved(mine);
      mine.composing = true;
      mine.from = String(caretOf(mine));
    });
    context.listen(area, "compositionend", function () {
      mine.composing = false;
      settle(mine);
      after(mine, true);
      mine.queue.splice(0).forEach(function (wait) { wait(); });
    });
    context.listen(area, "paste", function (event) {
      event.preventDefault();
      pasteAt(scope, null, held(event.clipboardData));
    });
    // a drop from elsewhere is the editor's from its start: the browser never builds what it carries in
    // the page (which would fetch its images), the place it lands read off the point. Words dragged
    // within the area are the browser's to take away (deleteByDrag) and the editor's to put down
    // (beforeinput insertFromDrop).
    context.listen(area, "dragstart", function () { mine.dragging = true; });
    context.listen(area, "dragend", function () { mine.dragging = false; });
    context.listen(area, "drop", function (event) {
      if (mine.dragging || mine.blocked) return;
      event.preventDefault();
      pasteAt(scope, null, held(event.dataTransfer), event.clientX, event.clientY);
    });
    context.listen(area, "keydown", function (event) {
      var composing = mine.composing || event.isComposing;
      if (event.altKey && event.key === "F10") {
        var first = mine.toolbars[0] && stops(mine.toolbars[0]).filter(function (button) { return button.tabIndex === 0; })[0];
        if (first && !composing) { event.preventDefault(); first.focus(); }
        return;
      }
      var key = known(KEYS, (event.key || "").toLowerCase());
      if (!key || event.altKey || !(mine.apple ? event.metaKey : event.ctrlKey)) return;
      event.preventDefault();
      if (event.key.toLowerCase() === "z" && event.shiftKey) key = ["redo"];
      // during a composition a shortcut waits for its end, as a toolbar button does
      later(mine, function () { scope[key[0]].apply(scope, key.slice(1)); });
    });
    context.listen(document, "selectionchange", function () {
      var s = inside(mine);
      if (!mine.composing) moved(mine);
      if (!s) return;
      mine.kept = s.getRangeAt(0).cloneRange();
      update(mine);
    });
    // a press on a toolbar button keeps the focus (and so the selection) in the area; a select (of the
    // format) is pressed to open
    var keep = function (event) {
      if (event.target.closest && event.target.closest("[data-kitwork-format],button[data-kitwork-command]")) event.preventDefault();
    };
    context.listen(host, "pointerdown", keep);
    context.listen(host, "mousedown", keep);
    // a command pressed in a dropdown's popover closes the dropdown once the call is done (on window:
    // the kernel runs the call on document, before)
    context.listen(window, "click", function (event) {
      var button = event.target.closest && event.target.closest("[data-kitwork-format],[data-kitwork-command]");
      var other = button && host.contains(button) && button.closest("[data-kitwork-component]");
      if (other !== host) shutMenu(other);
    });

    // the tooltips and the popover's place are a part (parts/editor-place.js), loaded at the first focus
    // in the editor or hover (not a touch's) on a toolbar: until it hears them itself (mine.hears) each
    // focusin, pointerover and pointerout is kept for it in the order it came (mine.heard), and from the
    // page's start what last came before a focus, a press or a key (mine.pressed: a press's focus shows
    // no tooltip)
    var placing = function (event) {
      if (mine.hears || event.pointerType === "touch") return;
      mine.heard.push(event);
      if (window[PLACER]) return window[PLACER](mine);
      // (a part that could not load is asked for no more, and what was kept for it goes)
      (placeFile = placeFile || script(PLACE)).then(function () { if (window[PLACER]) window[PLACER](mine); else mine.heard.length = 0; });
    };
    var input = function (event) { mine.pressed = event.type === "pointerdown"; };
    context.listen(window, "pointerdown", input, true);
    context.listen(window, "keydown", input, true);
    context.listen(host, "focusin", placing);
    // each toolbar is one stop for Tab (the server wrote tabindex 0 on its first button, -1 on the
    // others), its groups one row: the arrows, Home and End move along it, the stop with them (stops).
    // Escape goes back to the area; in a dropdown of the toolbar it closes the dropdown, the focus on
    // its button when it was in its popover (never on a button hidden), and the dropdown closes too once
    // the focus goes out of it (a Tab past its last button, a press in the area).
    mine.toolbars.forEach(function (bar) {
      var rove = function (to) { stops(bar).forEach(function (button) { button.tabIndex = button === to ? 0 : -1; }); };
      context.listen(bar, "pointerover", placing);
      context.listen(bar, "pointerout", placing);
      context.listen(bar, "focusin", function (event) { if (stops(bar).indexOf(event.target) >= 0) rove(event.target); });
      context.listen(bar, "focusout", function (event) {
        var menu = event.target.closest("[data-kitwork-component]"), to = event.relatedTarget;
        if (menu !== host && to && !menu.contains(to)) shutMenu(menu);
      });
      context.listen(bar, "keydown", function (event) {
        var all = stops(bar), at = all.indexOf(event.target), back = getComputedStyle(bar).direction === "rtl" ? "ArrowRight" : "ArrowLeft";
        var menu = event.target.closest("[data-kitwork-component]");
        if (event.key === "Escape" && (at >= 0 || menu !== host)) {
          event.preventDefault();
          if (menu !== host) {
            shutMenu(menu);
            var trigger = all.filter(function (button) { return menu.contains(button); })[0];
            if (at < 0 && trigger) return trigger.focus();
          }
          return focus(mine);
        }
        if (at < 0) return;
        var to = event.key === "Home" ? 0 : event.key === "End" ? all.length - 1 : event.key === back ? at - 1 : /^Arrow(Left|Right)$/.test(event.key) ? at + 1 : null;
        if (to === null) return;
        event.preventDefault();
        to = all[(to + all.length) % all.length];
        rove(to);
        to.focus();
      });
    });
    // Enter in the address links it and never submits the form; the Enter that ends a composition
    // there only ends it
    if (mine.input) context.listen(mine.input, "keydown", function (event) {
      if (event.key !== "Enter") return;
      event.preventDefault();
      if (event.isComposing || event.keyCode === 229) return;
      scope.address = mine.input.value;
      scope.link(mine.input.value);
    });
    // the popover closes with Escape wherever its focus is, and once the focus or a press goes outside
    // it (its kept selection then the one the next command takes)
    if (popover) {
      context.listen(popover, "keydown", function (event) {
        if (event.key === "Escape" && !event.isComposing) {
          event.preventDefault();
          scope.close();
        }
      });
      context.listen(popover, "focusout", function (event) {
        if (event.relatedTarget && !popover.contains(event.relatedTarget)) shut(mine);
      });
      context.listen(document, "pointerdown", function (event) {
        if (scope.open && !popover.contains(event.target)) shut(mine);
      });
    }
    // required: the browser checks the field; the area takes the focus it would give it
    context.listen(field, "invalid", function () {
      area.setAttribute("aria-invalid", "true");
      area.focus();
    });
    context.listen(field, "focus", function () { area.focus(); });
    if (mine.form) {
      context.listen(mine.form, "formdata", function (event) {
        var html = sync(mine).html;
        if (field.name && !field.disabled) event.formData.set(field.name, html);
      });
      // on window, so the page's own submit listeners have run: a submit one of them stopped keeps it
      context.listen(window, "submit", function (event) {
        if (event.target !== mine.form || event.defaultPrevented) return;
        mine.sent = true; // the words go with the form: leaving is no loss
        hold(mine, false);
      });
    }
    context.cleanup(function () { hold(mine, false); });
    sync(mine);
    update(mine);
    if (scope.format != "rich") viewing(mine); // <editor format="markdown">: shown so once its part is here
  }
};

window.kitwork.component("editor", editorDef);
