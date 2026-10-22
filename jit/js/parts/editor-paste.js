/* editor-paste — what the editor (components/editor/v2.0.0.js) takes in from outside the page: a
 * paste and a drop, read onto richtext's grammar and built with createElement. It is a file of its
 * own, delivered as /_kitwork/parts/editor-paste.<hash>.js and loaded by the editor at the first paste
 * or drop (never with the page): most visits paste nothing, and the editor stays under 11 KiB gzip.
 *
 * The file defines one function, under a symbol the editor knows, that the editor calls once with the
 * parts of itself a paste uses (its reading, its writing, its editing primitives), and that answers
 * the two things a paste and a drop need:
 *
 *   paste(mine, r, transfer)  a command (the editor runs it as it runs its own): what transfer holds
 *             (text/html read as richtext reads HTML, with what Google Docs and Word say in style;
 *             else text/plain as richtext reads text) put at r
 *   pointed(mine, x, y)  the place in the writing area under a point of the screen, null outside it
 *
 * A paste and a drop also say what they left out (the editor's dropped, leftOut below). The formats'
 * part (parts/editor-format.js, 21/10/2026) reads Markdown and HTML with the same hands: build (blocks
 * into elements), plain (text with no tags, as richtext reads it) and leftOut.
 */
window[Symbol.for("kitwork:editor-paste")] = function (editor) {
  var read = editor.read, run = editor.run, tidy = editor.tidy, words = editor.words, make = editor.make, MARKS = editor.MARKS;
  var styleOf = editor.styleOf, safeLink = editor.safeLink, verbatim = editor.verbatim, linkTo = editor.linkTo, around = editor.around;
  var serialize = editor.serialize, removeSelection = editor.removeSelection, collapse = editor.collapse, lineOf = editor.lineOf;
  var list = editor.list, blank = editor.blank, alignable = editor.alignable, attribute = editor.attribute, splitLine = editor.splitLine;
  var tail = editor.tail, join = editor.join, caretAfter = editor.caretAfter, under = editor.under;

  // mapping is what a paste from Google Docs or Word says in style, read where the editor reads an
  // element of it (read's map), into the marks the element's words get and the role it is: bold and
  // italic in style; its wrapper (b, font-weight normal, id docs-internal-guid-…) no bold (richtext's
  // rule for b and strong); Word's list markers not words ("drop"); underline, line-through and a
  // monospace face on what is not a block or a link u, s and code (the editor's alone: richtext reads no
  // span). The area's own elements carry no style nor id: the normaliser takes them off before it is
  // read, so the area is read without it.
  function mapping(el, at, inner, role, block) {
    var weight = styleOf(el, "font-weight"), slant = styleOf(el, "font-style");
    var decoration = styleOf(el, "text-decoration-line") || styleOf(el, "text-decoration");
    if (/mso-list\s*:\s*ignore/i.test(el.getAttribute("style"))) return "drop";
    if (/^docs-internal-guid/.test(el.id)) role = "";
    if (weight != null) {
      if (role !== "strong") inner.strong = /^bold/.test(weight) || +weight >= 600;
      else if (/^(normal|lighter)$/.test(weight) || /^[+-]?\d+$/.test(weight) && +weight < 600) role = "";
    }
    if (slant != null) inner.em = /^(italic|oblique)$/.test(slant);
    if (!block && role !== "a") {
      if (decoration != null) {
        if (!at.href) inner.u = /underline/.test(decoration); // a link's underline is the link's
        inner.s = /line-through/.test(decoration);
      }
      if (/mono|courier|consolas|menlo|monaco/.test(styleOf(el, "font-family"))) inner.code = true;
    }
    return role;
  }

  // plain is text read as richtext reads a source with no tags: a blank line starts a paragraph, a
  // newline is a line break
  function plain(text) {
    var blocks = [], runs = null, at = {};
    text.replace(/\r\n?/g, "\n").split("\n").forEach(function (piece) {
      piece = piece.replace(/[\0​﻿]/g, "").replace(/ /g, " ");
      if (!words(piece)) { runs = null; return; }
      if (runs) runs.push(run({ br: 1 }, at));
      else blocks.push({ tag: "p", runs: runs = [] });
      runs.push(run({ text: piece }, at));
    });
    return blocks.filter(function (block) { return (block.runs = tidy(block.runs)); });
  }

  // build makes blocks into elements, one by one: never HTML parsed into the page
  function build(blocks) {
    var out = document.createDocumentFragment();
    function into(el, runs, side) {
      if (/^(center|right)$/.test(side)) el.setAttribute("class", "align-" + side);
      runs.forEach(function (r) {
        var node = r.br ? make("br") : document.createTextNode(r.text);
        for (var i = MARKS.length; i--;) {
          if (!r[MARKS[i]]) continue;
          var mark = make(i ? MARKS[i] : "a");
          if (!i) mark.setAttribute("href", r.href);
          mark.appendChild(node);
          node = mark;
        }
        el.appendChild(node);
      });
      return el;
    }
    blocks.forEach(function (block) {
      var el = make(block.tag);
      if (block.tag === "pre") el.appendChild(make("code")).textContent = block.code;
      else if (block.parts) block.parts.forEach(function (part) { el.appendChild(into(make(block.tag === "blockquote" ? "p" : "li"), part, part.align)); });
      else if (block.runs) into(el, block.runs, block.align);
      out.appendChild(el);
    });
    return out;
  }

  // leftOut is what a tree read onto the grammar leaves out (the editor's state dropped): the names of
  // its elements, in the order met, each once, that the grammar has no place for and that take
  // something the writer sees with them: one gone with all it holds (the editor's drop role, richtext's:
  // script, style, form, button, iframe, svg, math…), one drawn of its own that holds no words (img,
  // picture, video, audio, canvas, embed, input), a table (its cells' words stay, as paragraphs). One
  // only unwrapped (span, font) or renamed (b, div, h1) keeps its words where they were: not one.
  var GONE = /^(form|object|select|button|script|style|textarea|title|xmp|iframe|noembed|noframes|noscript|template|plaintext|img|picture|video|audio|canvas|embed|input|table)$/;
  function leftOut(root) {
    var out = [];
    (function walk(node) {
      for (var el = node.firstElementChild; el; el = el.nextElementSibling) {
        var name = el.localName, foreign = el.namespaceURI !== "http://www.w3.org/1999/xhtml";
        if (foreign || GONE.test(name)) {
          if (out.indexOf(name) < 0) out.push(name);
          if (foreign || name !== "table") continue; // what a table holds stays, and is looked at
        }
        walk(el);
      }
    })(root);
    return out;
  }

  function paste(mine, r, transfer) {
    var html = transfer.getData("text/html"), text = transfer.getData("text/plain");
    var address = /^https?:\/\/\S+$/i.test(text.trim()) && safeLink(text.trim());
    var body = html && new DOMParser().parseFromString(html, "text/html").body;
    mine.scope.dropped = body ? leftOut(body) : [];
    if (address && !r.collapsed && !verbatim(mine, r)) return linkTo(mine, r, address); // an address pasted over words links them
    var blocks = body ? read(body, mapping) : [];
    if (around(mine, r.startContainer, "pre")) {
      // into a code block: the words alone, as written (white space too)
      var node = document.createTextNode((text || serialize(blocks).text).replace(/\r\n?/g, "\n"));
      if (!node.length) return;
      removeSelection(mine, r).insertNode(node);
      collapse(node, node.length);
      return true;
    }
    if (!blocks.length) blocks = plain(text);
    if (!blocks.length) return; // files and images alone: nothing to take
    var runs = blocks.length === 1 && blocks[0].runs, said = body && body.textContent || text;
    if (runs) {
      // words into a line keep the space they start or end with: the line around them is tidied whole
      if (/^\s/.test(said) && !runs[0].br) runs[0].text = " " + runs[0].text;
      if (/\s$/.test(said) && !runs[runs.length - 1].br) runs[runs.length - 1].text += " ";
    }
    r = removeSelection(mine, r);
    var line = lineOf(mine, r.startContainer), made = build(blocks), placed = list(made.childNodes), last;
    if (!line) return;
    if (blocks.length === 1 && blocks[0].tag === "p") {
      // words: into the line, where the caret is; an empty line takes the alignment the paragraph says,
      // as it would pasted with another
      var nodes = document.createDocumentFragment(), p = made.firstChild, side = blocks[0].align;
      if (side && blank(line) && alignable(line)) attribute(line, "class", side !== "left" && "align-" + side);
      last = p.lastChild;
      while (p.firstChild) nodes.appendChild(p.firstChild);
      r.insertNode(nodes);
    } else {
      // blocks: the line is split at the caret (and its quote or list after it), the blocks between
      var rest = splitLine(line, r.startContainer, r.startOffset), at = line;
      if (line.parentNode !== mine.area) {
        at = line.parentNode;
        tail(line);
      }
      last = placed[placed.length - 1];
      at.parentNode.insertBefore(made, at.nextSibling);
      [line, rest].forEach(function (end) {
        if (!blank(end)) return;
        var holder = end.parentNode;
        end.remove();
        if (holder !== mine.area && !holder.firstElementChild) holder.remove();
      });
      placed.forEach(function (el) { if (el.isConnected && /^(UL|OL|BLOCKQUOTE)$/.test(el.tagName)) join(el); });
    }
    // blocks that end in a rule (kept there since 21/10/2026): the caret at the start of the line after
    // it, as rule() leaves it (the editor's under)
    if (last.tagName === "HR") return under(mine, last);
    while (last.lastChild) last = last.lastChild;
    if (last.nodeType === 3) collapse(last, last.length);
    else caretAfter(last);
    return true;
  }

  function pointed(mine, x, y) {
    var r = document.caretRangeFromPoint && document.caretRangeFromPoint(x, y), p = !r && document.caretPositionFromPoint && document.caretPositionFromPoint(x, y);
    if (p) (r = document.createRange()).setStart(p.offsetNode, p.offset);
    return r && mine.area.contains(r.startContainer) ? r : null;
  }

  // (build, plain and leftOut are the formats' too: parts/editor-format.js reads Markdown and HTML onto
  // the grammar as a paste is read)
  return { paste: paste, pointed: pointed, build: build, plain: plain, leftOut: leftOut };
};
