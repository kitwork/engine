/* editor-markdown — the post as Markdown, both ways, in the browser: a part of the editor
 * (components/editor/v2.0.0.js), delivered as /_kitwork/parts/editor-markdown.<hash>.js and loaded by
 * its formats' part (parts/editor-format.js) when Markdown is first chosen, or when an editor starts in
 * Markdown; never with the page. The value never changes kind: what goes on the wire is the cleaned
 * HTML of richtext's grammar v2, and Markdown is a way to see and type the same post.
 *
 * The file defines one function, under a symbol the formats' part knows, called once with the editor's
 * hands (its reading and its writing: read, serialize), that answers:
 *
 *   markdown(blocks)  the blocks as the editor reads them (read) written as Markdown: a paragraph a
 *             paragraph, a blank line between two blocks; h2…h6 "## "…"###### "; strong **x**, em
 *             *x*, s ~~x~~, code in a line `x` (a run of backticks longer than any inside), u <u>x</u>,
 *             a link [words](address) (the address in <…> when it holds a space, a parenthesis, < or >);
 *             a line break a backslash at the end of the line; a quote "> " lines; a list "- " items
 *             (an ordered one "1. ", "2. "), the second of two lists of a kind side by side "* " ("1) "),
 *             or Markdown reads them as one; a code block fenced by backticks, the fence longer than any
 *             run inside; a rule ---; an aligned paragraph or heading one line of its HTML. Text is
 *             escaped so it reads back as itself. Each block is read back before it is given: a block
 *             whose Markdown would not read back as itself (bold that ends with a space, which Markdown
 *             cannot close) is written with its marks as HTML tags, and failing that as its HTML.
 *             So html(markdown(blocks)), read as the editor reads HTML, is the blocks again.
 *   html(text)  Markdown read into HTML, for the editor to read as it reads a paste (DOMParser, inert,
 *             onto the grammar): a CommonMark subset with GFM strikethrough (below), raw HTML passed
 *             through for the grammar to map (u, an aligned block, anything else in the grammar stays;
 *             the rest is dropped and named in dropped), an image its words and an empty <img> (dropped
 *             says img), a table its cells (dropped says table). A code block's text has no last newline
 *             (CommonMark gives one; the grammar keeps a code block's text as written).
 *   url(address)  an address written as a link's destination
 *
 * What the reading knows: ATX headings (# and ## are h2: h1 is the page's title, so the grammar has
 * none), setext headings (=== under a paragraph an h2, --- an h3), paragraphs, hard breaks (a backslash
 * or two spaces at a line's end), quotes and lists (- * + and 1. 1)) with their lazy lines (only where
 * a paragraph goes on: after a fence, a code block or a heading the quote or the list ends), fenced
 * code (``` or ~~~, an info string ignored), indented code (4 spaces), rules (--- *** ___), emphasis
 * and strong by CommonMark's delimiter rules (* and _, the rule of three), ~~strike~~ (two tildes),
 * code spans, links [words](address "title") (the title goes; parentheses nested 32 deep at most, as
 * cmark: so a line of "[a](" reads in linear time), autolinks <https://…> <a@b.c>, backslash escapes,
 * character references (a name HTML reads whole: the HTML reading decodes it), raw HTML inline and in
 * blocks (CommonMark's seven kinds; a script, style, textarea, title, xmp, iframe, noembed, noframes or
 * plaintext with no end tag in its block is words, GFM's tag filter: read as HTML it would take every
 * word after it), GFM tables. Not: reference links and their definitions (their words stay as written),
 * footnotes, task lists. Nesting deeper than the grammar holds is the grammar's: a list in a list, a
 * quote in a quote are flattened where the editor reads them.
 */
window[Symbol.for("kitwork:editor-markdown")] = function (editor) {
  var read = editor.read, serialize = editor.serialize;
  var ENTITY = { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" };
  function escape(text) { return text.replace(/[&<>"]/g, function (c) { return ENTITY[c]; }); }
  function repeat(text, n) { return new Array(n + 1).join(text); }
  function longest(text) { return Math.max.apply(null, [0].concat((text.match(/`+/g) || []).map(function (run) { return run.length; }))); }
  // canonical is Markdown read back as the editor reads it: the HTML of the grammar
  function canonical(text) { return serialize(read(new DOMParser().parseFromString(html(text), "text/html").body)).html; }

  // ---- writing: blocks as Markdown ----
  var WORD = /[\p{L}\p{N}]/u;
  // plain is text written so that it reads back as itself: what would start a mark, a code span, a
  // link, a tag or a character reference escaped; an underscore only where it does not stand inside a
  // word (snake_case stays), a tilde only next to a tilde or at an edge (two of them strike)
  function plain(text) {
    var out = "";
    for (var i = 0; i < text.length; i++) {
      var c = text.charAt(i), before = text.charAt(i - 1), after = text.charAt(i + 1);
      if (/[\\`*[\]]/.test(c) || c === "_" && !(WORD.test(before) && WORD.test(after)) || c === "~" && (before === "~" || after === "~" || !before || !after) ||
        c === "<" && /[A-Za-z\/!?]/.test(after) || c === "&" && /^&(#\d+|#x[\da-f]+|[a-z][a-z\d]*);/i.test(text.slice(i))) out += "\\";
      out += c;
    }
    return out;
  }
  // starts escapes what would start a block at the start of a line (each line of a paragraph, an item,
  // a quote's paragraph: a line break starts one): a heading, a quote, a list item, a rule or the
  // underline of a heading
  function starts(text) {
    return text.split("\n").map(function (line) {
      var number = /^(\d{1,9})([.)])([ \t]|$)/.exec(line);
      if (number) return number[1] + "\\" + line.slice(number[1].length);
      return /^([-=])\1*[ \t]*$|^(#{1,6}|[-+])([ \t]|$)|^>/.test(line) ? "\\" + line : line;
    }).join("\n");
  }
  // destination is an address as a link writes it: a backslash and what would be a character reference
  // escaped; in <…> when it holds a space, a parenthesis, < or >
  function destination(href) {
    href = href.replace(/\\|&(?=#?\w+;)/g, "\\$&");
    return /[\s<>()]/.test(href) ? "<" + href.replace(/[<>]/g, "\\$&") + ">" : href;
  }
  // span is a code span: backticks one more than the longest run inside, a space inside each end when
  // the code starts or ends with a backtick or a space (Markdown takes one space off each end)
  function span(code) {
    var fence = repeat("`", longest(code) + 1), pad = /^[ `]|[ `]$/.test(code) && /[^ ]/.test(code) ? " " : "";
    return fence + pad + code + pad + fence;
  }

  // phrase writes a line's runs. A mark is opened where it starts, the one that goes on longest first
  // (outermost), and closed where it ends, what was opened inside it closed first and opened again;
  // code is innermost, its runs with the same other marks one span. strong, em and s are written with
  // delimiters where Markdown reads them back (their opening one can open, their closing one close, each
  // read with what stands beside it: a mark's own is punctuation either way), else as HTML tags; tags:
  // every one as tags (for a block whose delimiters do not read back even so).
  var DELIMITER = { strong: "**", em: "*", s: "~~" }, ORDER = ["href", "strong", "em", "u", "s"];
  function phrase(runs, tags) {
    var tokens = [], open = [];
    function has(r, mark) { return mark.name === "href" ? !!r.href && r.href === mark.href : r[mark.name]; }
    function reach(i, mark) { for (var j = i; j < runs.length && has(runs[j], mark); j++); return j; }
    function shut() { tokens.push({ mark: open.pop(), end: true }); }
    for (var i = 0; i < runs.length; i++) {
      var r = runs[i], keep = 0, adding = [];
      while (keep < open.length && has(r, open[keep])) keep++;
      while (open.length > keep) shut();
      ORDER.forEach(function (name) {
        var mark = { name: name, href: r.href };
        if (has(r, mark) && !open.some(function (other) { return other.name === name; })) adding.push(mark);
      });
      adding.sort(function (a, b) { return reach(i, b) - reach(i, a); }).forEach(function (mark) {
        tokens.push({ mark: mark });
        open.push(mark);
      });
      if (r.code) {
        // the runs of the code span: code, the same other marks
        for (var j = i + 1; j < runs.length && runs[j].code && ORDER.every(function (name) { return runs[j][name] === r[name]; }); j++);
        var held = runs.slice(i, j), words = held.map(function (each) { return each.br ? "\n" : each.text; }).join("");
        // a line break in code: Markdown's code spans hold none, so the code is its tags
        tokens.push({ s: /\n/.test(words) ? "<code>" + held.map(function (each) { return each.br ? "<br>" : plain(each.text); }).join("") + "</code>" : span(words) });
        i = j - 1;
      } else tokens.push({ s: r.br ? "\\\n" : plain(r.text) });
    }
    while (open.length) shut();
    // beside is the character next to token k (step -1 before it, 1 after it): a line's start or end is
    // white space
    function beside(k, step) {
      for (var j = k + step; j >= 0 && j < tokens.length; j += step) {
        var s = tokens[j].s;
        if (s == null) return "*";
        if (s) return s.charAt(step > 0 ? 0 : s.length - 1);
      }
      return "\n";
    }
    tokens.forEach(function (token, k) {
      var before = beside(k, -1), after = beside(k, 1);
      if (token.mark && !(token.end ? !WHITE.test(before) && (!PUNCT.test(before) || WHITE.test(after) || PUNCT.test(after)) :
        !WHITE.test(after) && (!PUNCT.test(after) || WHITE.test(before) || PUNCT.test(before)))) token.mark.tags = true;
    });
    return starts(tokens.map(function (token) {
      var mark = token.mark, name = mark && mark.name;
      if (!mark) return token.s;
      if (name === "href") return token.end ? "](" + destination(mark.href) + ")" : "[";
      return name === "u" || tags || mark.tags ? "<" + (token.end ? "/" : "") + name + ">" : DELIMITER[name];
    }).join(""));
  }

  function aligned(block) { return /^(center|right)$/.test(block.align); }
  // block writes one block; alternate: the second of two lists of a kind side by side
  function block(b, alternate, tags) {
    var tag = b.tag, text;
    if (tag === "hr") return "---";
    if (tag === "pre") {
      var fence = repeat("`", Math.max(3, longest(b.code) + 1));
      return fence + "\n" + b.code + "\n" + fence;
    }
    if (b.runs) {
      if (aligned(b)) return serialize([b]).html;
      text = phrase(b.runs, tags);
      if (tag === "p") return text;
      // a heading is one line; a last # would close it
      return /\n/.test(text) ? serialize([b]).html : repeat("#", +tag.charAt(1)) + " " + text.replace(/#$/, "\\#");
    }
    if (tag === "blockquote") {
      return b.parts.map(function (part) {
        return aligned(part) ? serialize([{ tag: "p", align: part.align, runs: part }]).html : phrase(part, tags);
      }).join("\n\n").split("\n").map(function (line) { return line ? "> " + line : ">"; }).join("\n");
    }
    return b.parts.map(function (part, i) {
      var marker = tag === "ol" ? i + 1 + (alternate ? ")" : ".") : alternate ? "*" : "-";
      return marker + " " + phrase(part, tags).split("\n").join("\n" + repeat(" ", marker.length + 1));
    }).join("\n");
  }

  function markdown(blocks) {
    var alternate = [], written = blocks.map(function (b, i) {
      var before = blocks[i - 1];
      alternate[i] = !!(before && before.tag === b.tag && /^[uo]l$/.test(b.tag) && !alternate[i - 1]);
      return block(b, alternate[i]);
    }), text = written.join("\n\n"), want = serialize(blocks).html;
    if (canonical(text) === want) return text;
    // block by block: as written, else its marks as tags, else its HTML; and should the whole still not
    // read back, every block its HTML (each a block of HTML of its own, which reads as itself). A rule
    // is --- after any block (read alone it is nothing: a rule first goes), the last one too
    text = blocks.map(function (b, i) {
      var own = serialize([b]).html, tagged;
      return b.tag === "hr" || canonical(written[i]) === own ? written[i] : canonical(tagged = block(b, alternate[i], true)) === own ? tagged : own;
    }).join("\n\n");
    return canonical(text) === want ? text : blocks.map(function (b) { return serialize([b]).html; }).join("\n\n");
  }

  // ---- reading: Markdown as HTML ----
  // indent is the width of a line's leading white space, a tab to the next stop of 4
  function indent(line) {
    for (var width = 0, i = 0; i < line.length; i++) {
      var c = line.charAt(i);
      if (c === " ") width++;
      else if (c === "\t") width += 4 - width % 4;
      else break;
    }
    return width;
  }
  // strip takes n columns of white space off a line's start (a tab partly taken leaves its spaces)
  function strip(line, n) {
    for (var width = 0, i = 0; i < line.length && width < n; i++) {
      var c = line.charAt(i);
      if (c === " ") width++;
      else if (c === "\t") {
        var next = width + 4 - width % 4;
        if (next > n) return repeat(" ", next - n) + line.slice(i + 1);
        width = next;
      } else break;
    }
    return line.slice(i);
  }
  function blank(line) { return /^[ \t]*$/.test(line); }
  var ATX = /^ {0,3}(#{1,6})(?=[ \t]|$)(.*)$/, RULE = /^ {0,3}([-*_])(?:[ \t]*\1){2,}[ \t]*$/, SETEXT = /^ {0,3}(=+|-+)[ \t]*$/, QUOTE = /^ {0,3}>/;
  var FENCE = /^( {0,3})(`{3,}(?=[^`]*$)|~{3,})/, ITEM = /^( {0,3})([-+*]|(\d{1,9})([.)]))([ \t]+|$)/;
  var BLOCKS = "address|article|aside|base|basefont|blockquote|body|caption|center|col|colgroup|dd|details|dialog|dir|div|dl|dt|fieldset|figcaption|figure|footer|form|frame|frameset|h[1-6]|head|header|hr|html|iframe|legend|li|link|main|menu|menuitem|nav|noframes|ol|optgroup|option|p|param|search|section|summary|table|tbody|td|tfoot|th|thead|title|tr|track|ul";
  var NAME = "[A-Za-z][A-Za-z0-9-]*", ATTRIBUTE = "(?:\\s+[A-Za-z_:][\\w.:-]*(?:\\s*=\\s*(?:[^\\s\"'=<>`]+|'[^']*'|\"[^\"]*\"))?)";
  var OPEN = "<" + NAME + ATTRIBUTE + "*\\s*\\/?>", CLOSE = "<\\/" + NAME + "\\s*>";
  // the seven starts of a block of HTML, each with what ends it (a blank line for the last two)
  var HTML = [
    [/^ {0,3}<(script|pre|style|textarea)(\s|>|$)/i, /<\/(script|pre|style|textarea)>/i], [/^ {0,3}<!--/, /-->/], [/^ {0,3}<\?/, /\?>/],
    [/^ {0,3}<![A-Za-z]/, />/], [/^ {0,3}<!\[CDATA\[/, /\]\]>/], [new RegExp("^ {0,3}</?(" + BLOCKS + ")(\\s|/?>|$)", "i")],
    [new RegExp("^ {0,3}(" + OPEN + "|" + CLOSE + ")[ \\t]*$")]
  ];
  function htmlStart(line) {
    for (var kind = 0; kind < 7; kind++) if (HTML[kind][0].test(line) && !(kind === 6 && /^ {0,3}<\/?(script|style|pre|textarea)\b/i.test(line))) return kind + 1;
    return 0;
  }
  // inert keeps HTML's elements whose words are not read as markup (script, style, textarea, title, xmp,
  // iframe, noembed, noframes, plaintext) from taking every word after them, to their end tag or the
  // post's end: one closed in the same block is HTML (left out with what it holds, dropped names it);
  // one with no end tag there (a tag named in prose, "the <textarea> tag") is words, its < written &lt;
  // (GFM's tag filter). html is a block's HTML (a paragraph's words, a block of HTML).
  function inert(html) {
    var last = {}; // where each one's last end tag stands (plaintext has none)
    html.replace(/<\/(script|style|textarea|title|xmp|iframe|noembed|noframes)[\s\/>]/gi, function (tag, name, at) { last[name.toLowerCase()] = at; });
    return html.replace(/<(script|style|textarea|title|xmp|iframe|noembed|noframes|plaintext)(?=[\s\/>]|$)/gi, function (tag, name, at) {
      return last[name.toLowerCase()] > at ? tag : "&lt;" + name;
    });
  }
  // item is a list item's start: its kind (the bullet, or the delimiter of a number), its number, the
  // column its words start at, and its first line's words
  function item(line) {
    var m = ITEM.exec(line);
    if (!m) return null;
    var head = indent(m[1]) + m[2].length, rest = line.slice(m[0].length), spaces = indent(repeat(" ", head) + m[5]) - head;
    return { kind: m[4] || m[2], number: m[3], offset: head + (!rest || spaces > 4 ? 1 : spaces), first: !rest ? "" : spaces > 4 ? repeat(" ", spaces - 1) + rest : rest };
  }
  // interrupts: a line that ends a paragraph and starts a block of its own (an empty item, or a number
  // other than 1, does not)
  function interrupts(line) {
    var start = item(line), kind = htmlStart(line);
    return ATX.test(line) || RULE.test(line) || FENCE.test(line) || QUOTE.test(line) || kind > 0 && kind < 7 ||
      !!start && !blank(start.first) && (!start.number || +start.number === 1);
  }
  // cells is a table's row split at its pipes (an escaped one is a word), the pipes at its ends gone
  function cells(line) {
    var out = [""];
    line.trim().replace(/^\|/, "").replace(/(^|[^\\])\|$/, "$1").replace(/\\\||\||[^\\|]+|\\/g, function (piece) {
      if (piece === "|") out.push("");
      else out[out.length - 1] += piece === "\\|" ? "|" : piece;
    });
    return out;
  }
  var ROW = /^ {0,3}\|?[ \t]*:?-+:?[ \t]*(\|[ \t]*:?-+:?[ \t]*)*\|?[ \t]*$/;

  // closer is what closes a fence (m its FENCE match): as many of its marks or more, alone on a line
  function closer(m) { return new RegExp("^ {0,3}" + m[2].charAt(0) + "{" + m[2].length + ",}[ \\t]*$"); }
  // follow keeps, line by line, what the lines of a quote or an item end in (state): a paragraph still
  // open (para), the one block a lazy line goes on (a line without the quote's > or the item's indent:
  // after a fence, a code block, a heading or a rule the quote or the list ends there); a fence still
  // open (fence: what closes it)
  function follow(state, line) {
    var m = !state.fence && FENCE.exec(line), kind = htmlStart(line);
    if (state.fence || m) {
      state.fence = m ? closer(m) : !state.fence.test(line) && state.fence;
      state.para = false;
    } else state.para = !blank(line) && !ATX.test(line) && !RULE.test(line) && !(state.para ? SETEXT.test(line) || kind && kind < 7 : indent(line) >= 4 || kind);
    return line;
  }
  // lazy is a lazy line as the words of the paragraph it goes on: an underline of = is words there
  function lazy(line) { return SETEXT.test(line) ? "\\" + line.trim() : line; }

  // blocks reads lines (a document's, a quote's, an item's) into HTML
  function blocks(lines) {
    var out = "", i = 0, n = lines.length, line, m, kind, body, start, state, ended = -1;
    // (the last line that ends a block of HTML of the first kind: <script>, <style> or <textarea> with no
    // end ahead is no such block, its words read as words)
    for (var e = 0; e < n; e++) if (HTML[0][1].test(lines[e])) ended = e;
    while (i < n) {
      line = lines[i];
      if (blank(line)) { i++; continue; }
      if (indent(line) >= 4) {
        // indented code: to the last line indented as much, the blank lines between kept
        var last = i;
        for (var j = i; j < n && (blank(lines[j]) || indent(lines[j]) >= 4); j++) if (!blank(lines[j])) last = j;
        for (body = []; i <= last; i++) body.push(strip(lines[i], 4));
        out += "<pre><code>" + escape(body.join("\n")) + "</code></pre>\n";
        continue;
      }
      if ((m = FENCE.exec(line))) {
        var closing = closer(m), skip = m[1].length;
        for (body = [], i++; i < n && !closing.test(lines[i]); i++) body.push(strip(lines[i], skip));
        i++;
        out += "<pre><code>" + escape(body.join("\n")) + "</code></pre>\n";
        continue;
      }
      if ((m = ATX.exec(line))) {
        out += "<h" + m[1].length + ">" + inline(m[2].replace(/(^|[ \t]+)#+[ \t]*$/, "").trim()) + "</h" + m[1].length + ">\n";
        i++;
        continue;
      }
      if (RULE.test(line)) { out += "<hr>\n"; i++; continue; }
      if ((kind = htmlStart(line)) && (kind > 1 || ended >= i || /^ {0,3}<pre/i.test(line))) {
        // a block of HTML, as written, to the line that ends it (a blank line for the last two kinds)
        for (body = []; i < n; i++) {
          if (kind > 5 && blank(lines[i])) break;
          body.push(lines[i]);
          if (kind < 6 && HTML[kind - 1][1].test(lines[i])) { i++; break; }
        }
        out += inert(body.join("\n")) + "\n";
        continue;
      }
      if (QUOTE.test(line)) {
        // a quote: its lines without their >, and the lazy lines of a paragraph in it
        for (body = [], state = {}; i < n; i++) {
          line = lines[i];
          if (QUOTE.test(line)) body.push(follow(state, line.replace(/^ {0,3}> ?/, "")));
          else if (state.para && !blank(line) && !interrupts(line)) body.push(lazy(line));
          else break;
        }
        out += "<blockquote>\n" + blocks(body) + "</blockquote>\n";
        continue;
      }
      if ((start = item(line)) && !RULE.test(line)) {
        // a list: its items of one kind, each item's lines (indented as far as its words, the blank ones
        // between, the lazy ones of a paragraph) read as a document of their own
        var tag = start.number ? "ol" : "ul", items = [], held = null, offset = 0;
        for (; i < n; i++) {
          line = lines[i];
          var next = item(line);
          if (next && (!held || indent(line) < offset)) {
            if (next.kind !== start.kind || RULE.test(line)) break;
            items.push(held = [follow(state = {}, next.first)]);
            offset = next.offset;
            continue;
          }
          if (blank(line)) {
            // a blank line goes on with the item when what follows it is indented as far, or is the next item
            for (var k = i + 1; k < n && blank(lines[k]); k++);
            var after = k < n && item(lines[k]);
            if (k < n && (indent(lines[k]) >= offset || after && after.kind === start.kind)) { held.push(follow(state, "")); continue; }
            break;
          }
          if (indent(line) >= offset) { held.push(follow(state, strip(line, offset))); continue; }
          if (state.para && !interrupts(line)) { held.push(lazy(line)); continue; } // a lazy line
          break;
        }
        out += "<" + tag + ">\n" + items.map(function (lines) { return "<li>" + blocks(lines) + "</li>\n"; }).join("") + "</" + tag + ">\n";
        continue;
      }
      if (/\|/.test(line) && i + 1 < n && ROW.test(lines[i + 1]) && /\|/.test(lines[i + 1]) && cells(line).length === cells(lines[i + 1]).length) {
        // a table: its cells' words, each a paragraph where the editor reads it (dropped says table)
        for (body = [line], i += 2; i < n && !blank(lines[i]) && !interrupts(lines[i]); i++) body.push(lines[i]);
        out += "<table>" + body.map(function (row) {
          return "<tr>" + cells(row).map(function (cell) { return "<td>" + inline(cell.trim()) + "</td>"; }).join("") + "</tr>";
        }).join("") + "</table>\n";
        continue;
      }
      // a paragraph: to a blank line or a block that interrupts it; an underline makes it a heading
      var heading = 0;
      for (body = [line.replace(/^[ \t]+/, "")], i++; i < n; i++) {
        line = lines[i];
        if ((m = SETEXT.exec(line))) { heading = m[1].charAt(0) === "=" ? 2 : 3; i++; break; }
        if (blank(line) || interrupts(line)) break;
        body.push(line.replace(/^[ \t]+/, ""));
      }
      var words = inline(body.join("\n").replace(/[ \t]+$/, ""));
      out += heading ? "<h" + heading + ">" + words + "</h" + heading + ">\n" : "<p>" + words + "</p>\n";
    }
    return out;
  }

  // ---- reading a line's words: CommonMark's inlines ----
  var WHITE = /\s/, PUNCT = /[\p{P}\p{S}]/u, ESCAPABLE = /[!-\/:-@[-`{-~]/;
  var TEXT = /[^\\`<&[\]!*_~\n]+/y, AUTOLINK = /<([A-Za-z][A-Za-z\d+.-]{1,31}:[^\s<>]*)>/y;
  var EMAIL = /<([\w.!#$%&'*+\/=?^`{|}~-]+@[A-Za-z\d](?:[A-Za-z\d-]{0,61}[A-Za-z\d])?(?:\.[A-Za-z\d](?:[A-Za-z\d-]{0,61}[A-Za-z\d])?)*)>/y;
  var RAW = new RegExp(OPEN + "|" + CLOSE + "|<!--[\\s\\S]*?-->|<\\?[\\s\\S]*?\\?>|<![A-Za-z][^>]*>|<!\\[CDATA\\[[\\s\\S]*?\\]\\]>", "y");
  var REFERENCE = /&(?:#\d{1,7}|#[xX][\da-fA-F]{1,6}|[A-Za-z][A-Za-z\d]{1,31});/y;
  // NAMED: each name of a character reference the text holds, true when HTML reads it whole (html)
  var NAMED = {};
  // attribute is a destination's text as HTML: its escapes taken, its character references kept for the
  // HTML reading to decode
  function attribute(text) {
    return text.replace(/\\([!-\/:-@[-`{-~])|&(?:#\d{1,7}|#[xX][\da-fA-F]{1,6}|[A-Za-z][A-Za-z\d]{1,31});|[&<>"]/g, function (match, escaped) {
      return escaped ? ENTITY[escaped] || escaped : match.length > 1 ? match : ENTITY[match];
    });
  }
  // skip is the white space at src[i]: spaces and tabs, one line ending at most
  function skip(src, i) {
    var space = /[ \t]*(?:\n[ \t]*)?/y;
    space.lastIndex = i;
    space.exec(src);
    return space.lastIndex;
  }
  // link reads (destination "title") at src[at]: the address as HTML and where it ends, or null
  function link(src, at) {
    if (src.charAt(at) !== "(") return null;
    var i = skip(src, at + 1), from = i, depth = 0, href, m;
    if (src.charAt(i) === "<") {
      var angle = /<((?:[^<>\n\\]|\\[\s\S])*)>/y;
      angle.lastIndex = i;
      if (!(m = angle.exec(src))) return null;
      href = m[1];
      i = angle.lastIndex;
    } else {
      for (; i < src.length; i++) {
        var c = src.charAt(i);
        if (c === "\\" && ESCAPABLE.test(src.charAt(i + 1))) i++;
        else if (c === "(") { if (++depth > 32) return null; } // (deeper: no link, as cmark, so a line of "[a](" reads in linear time)
        else if (c === ")" && !depth--) break;
        else if (/[\s\x00-\x1f]/.test(c)) break;
      }
      if (depth > 0) return null;
      href = src.slice(from, i);
    }
    var j = skip(src, i), title = /"(?:[^"\\]|\\[\s\S])*"|'(?:[^'\\]|\\[\s\S])*'|\((?:[^()\\]|\\[\s\S])*\)/y;
    if (j > i && /["'(]/.test(src.charAt(j))) {
      title.lastIndex = j;
      if (!title.exec(src)) return null;
      j = skip(src, title.lastIndex);
    }
    return src.charAt(j) === ")" ? { href: attribute(href), end: j + 1 } : null;
  }

  // inline reads a paragraph's (a heading's, a cell's) words into HTML: text escaped, the rest by
  // CommonMark's algorithm (delimiter runs, brackets, process emphasis)
  function inline(src) {
    var nodes = [], brackets = [], i = 0, n = src.length, m;
    function push(h) { nodes.push({ h: h }); }
    function at(re) { re.lastIndex = i; return re.exec(src); }
    while (i < n) {
      var c = src.charAt(i);
      if ((m = at(TEXT))) { push(escape(m[0])); i += m[0].length; continue; }
      if (c === "\\") {
        var next = src.charAt(i + 1);
        if (next === "\n") for (push("<br>"), i += 2; /[ \t]/.test(src.charAt(i)); i++);
        else if (ESCAPABLE.test(next)) { push(escape(next)); i += 2; }
        else { push("\\"); i++; }
        continue;
      }
      if (c === "\n") {
        // two spaces or more before it: a line break; else a soft break. The spaces go either way.
        var last = nodes[nodes.length - 1], spaces = last && last.h ? /( *)$/.exec(last.h)[1].length : 0;
        if (spaces) last.h = last.h.slice(0, -spaces);
        for (push(spaces > 1 ? "<br>" : "\n"), i++; /[ \t]/.test(src.charAt(i)); i++);
        continue;
      }
      if (c === "`") {
        // a code span: to the next run of as many backticks; none, and the run is words
        var ticks = at(/`+/y)[0], scan = /`+/g, end = -1;
        scan.lastIndex = i + ticks.length;
        for (var probe; (probe = scan.exec(src));) if (probe[0].length === ticks.length) { end = probe.index; break; }
        if (end < 0) { push(ticks); i += ticks.length; continue; }
        var code = src.slice(i + ticks.length, end).replace(/\n/g, " ");
        if (/^ [\s\S]* $/.test(code) && /[^ ]/.test(code)) code = code.slice(1, -1);
        push("<code>" + escape(code) + "</code>");
        i = end + ticks.length;
        continue;
      }
      if (c === "<") {
        if ((m = at(AUTOLINK) || at(EMAIL))) push('<a href="' + escape((m[1].indexOf(":") < 0 ? "mailto:" : "") + m[1]) + '">' + escape(m[1]) + "</a>");
        else if ((m = at(RAW))) push(m[0]);
        else push("&lt;");
        i += m ? m[0].length : 1;
        continue;
      }
      if (c === "&") {
        // a character reference HTML reads whole (a name it does not have is words: &copyx; is not ©x;)
        if ((m = at(REFERENCE)) && NAMED[m[0]] === false) m = null;
        push(m ? m[0] : "&amp;");
        i += m ? m[0].length : 1;
        continue;
      }
      if (c === "*" || c === "_" || c === "~") {
        var run = at(new RegExp("\\" + c + "+", "y"))[0], k = run.length;
        var before = src.charAt(i - 1) || "\n", after = src.charAt(i + k) || "\n";
        var left = !WHITE.test(after) && (!PUNCT.test(after) || WHITE.test(before) || PUNCT.test(before));
        var right = !WHITE.test(before) && (!PUNCT.test(before) || WHITE.test(after) || PUNCT.test(after));
        if (c === "~" && k !== 2) push(run);
        else nodes.push({ d: c, n: k, size: k, open: left && (c !== "_" || !right || PUNCT.test(before)), close: right && (c !== "_" || !left || PUNCT.test(after)), before: "", after: "" });
        i += k;
        continue;
      }
      if (c === "[" || c === "!" && src.charAt(i + 1) === "[") {
        brackets.push(nodes.length);
        nodes.push({ h: c === "!" ? "![" : "[", image: c === "!", active: true });
        i += c === "!" ? 2 : 1;
        continue;
      }
      if (c === "]") {
        var opener = brackets.length ? nodes[brackets.pop()] : null, target = opener && opener.active && link(src, i + 1);
        if (!target) { push("]"); i++; continue; }
        var from = nodes.indexOf(opener);
        emphasis(nodes, from + 1);
        var words = nodes.splice(from).slice(1).map(output).join("");
        if (opener.image) push(words.replace(/<[^>]*>/g, "") + "<img>"); // its words, plain (an alt is text)
        else {
          push('<a href="' + target.href + '">' + words + "</a>");
          brackets.forEach(function (index) { if (!nodes[index].image) nodes[index].active = false; }); // no link in a link
        }
        i = target.end;
        continue;
      }
      push(c); // a ! not before a [
      i++;
    }
    emphasis(nodes, 0);
    return inert(nodes.map(output).join(""));
  }
  function output(node) { return node.d ? node.before + repeat(node.d, node.n) + node.after : node.h; }
  // emphasis is CommonMark's "process emphasis" over nodes from bottom on: each closer, left to right,
  // with the nearest opener of its kind (the rule of three for * and _; ~ two and two), strong when both
  // have two left; the delimiters between them are words from then on
  function emphasis(nodes, bottom) {
    var floors = {};
    for (var k = bottom; k < nodes.length; k++) {
      var closer = nodes[k], opener, found = -1;
      if (!closer.d || !closer.close || !closer.n) continue;
      var key = closer.d + closer.open + closer.size % 3;
      for (var j = k - 1; j >= Math.max(bottom, floors[key] || 0); j--) {
        opener = nodes[j];
        if (opener.d !== closer.d || !opener.open || !opener.n) continue;
        if (closer.d === "~" ? opener.n === 2 && closer.n === 2 : !((opener.close || closer.open) && closer.size % 3 && (opener.size + closer.size) % 3 === 0)) { found = j; break; }
      }
      if (found < 0) {
        floors[key] = k;
        if (!closer.open) closer.close = false;
        continue;
      }
      opener = nodes[found];
      var use = closer.d === "~" || opener.n > 1 && closer.n > 1 ? 2 : 1, tag = closer.d === "~" ? "s" : use > 1 ? "strong" : "em";
      opener.n -= use;
      closer.n -= use;
      opener.after = "<" + tag + ">" + opener.after;
      closer.before += "</" + tag + ">";
      for (j = found + 1; j < k; j++) if (nodes[j].d) nodes[j].open = nodes[j].close = false;
      if (closer.n) k--;
    }
  }

  // html reads Markdown: its lines (CR LF and CR a newline, NUL the replacement character). The names of
  // the character references it holds are read by HTML first, all in one reading (NAMED): a name HTML
  // reads only in part (&notit; is ¬ then it;, &copyx; is © then x;) is words, as CommonMark has it.
  function html(text) {
    text = String(text).replace(/\r\n?/g, "\n").replace(/\0/g, "�");
    var names = (text.match(/&[A-Za-z][A-Za-z\d]{1,31};/g) || []).filter(function (name) { return !(name in NAMED); });
    if (names.length) {
      var read = new DOMParser().parseFromString("<b>" + names.join("</b><b>") + "</b>", "text/html").body.children;
      names.forEach(function (name, k) { var words = read[k].textContent; NAMED[name] = words !== name && (words === ";" || !/;$/.test(words)); });
    }
    return blocks(text.split("\n"));
  }

  return { markdown: markdown, html: html, url: destination };
};
