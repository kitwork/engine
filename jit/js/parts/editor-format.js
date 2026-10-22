/* editor-format — the editor's formats (components/editor/v2.0.0.js, 21/10/2026): the post seen and
 * typed as rich text (the writing area), as Markdown or as HTML (a plain-text surface in the area's
 * place). It is a file of its own, delivered as /_kitwork/parts/editor-format.<hash>.js and loaded by the
 * editor at the first switch away from rich, or at the start of an editor that starts in another format
 * (<editor format="markdown">), never with the page: most visits write rich.
 *
 * Whatever the format, one value goes on the wire: the cleaned HTML of richtext's grammar v2, in the
 * field the form posts. The area stays the value: while a text format shows, what is typed is read onto
 * the grammar (Markdown by parts/editor-markdown.js into HTML, then HTML as a paste is read: DOMParser,
 * inert, the editor's read with richtext's one reading of style, built with createElement by
 * parts/editor-paste.js, never innerHTML), two quotes or two lists of a kind side by side one as the area
 * holds them, and put in the hidden area: at each input while the text is short (under SHORT characters:
 * a reading costs less than a frame there, and the field, the browser's required check and a form a
 * page's own script sends read the post as it is), at the first change since the post was saved (unsaved
 * and the question on leaving at once, as in rich) and when the post turns empty or not; else 300 ms
 * after the typing stops; when the surface loses the focus, on the form's formdata and on a switch. The
 * editor's own sync then writes the field and the counts (words, characters, empty, unsaved), and each
 * reading of HTML or Markdown names in dropped what the grammar left out (the paste part's leftOut:
 * script, iframe, img, table…, in the order met, each once).
 *
 * The file defines one function, under a symbol the editor knows, which the editor calls with itself
 * (mine) and its hands each time its format is asked for (format = '…', or the start): the format asked
 * is what scope.format reads. The first call makes the editor's surface and takes its commands (below);
 * each call then shows the format asked once the files it needs are here (the paste part, and the
 * Markdown part for Markdown), in the order asked, and never during a composition (an input method
 * writing a word: the switch waits for its end, in the area as in the surface). A format it cannot show
 * (a file that did not load, asked again at the next switch; a word that is none; a switch of a readonly
 * editor away from the one it starts in) is refused: format reads the one shown again.
 *
 *   rich      the area, as ever
 *   markdown  the surface holds the post as Markdown, spellchecked; the toolbar writes Markdown around
 *             the selection (bold **…**, italic *…*, underline <u>…</u>, strikethrough ~~…~~, on each
 *             line it holds, after the line's own start, inside the white space at its ends; code a pair
 *             of backticks, a code block a fence above and below the lines, a heading as many # at the
 *             start of each line, a quote "> ", a list "- ", an ordered list "1. ", "2. "…: each again
 *             takes it off; a rule a "---" line after the caret's; the link's popover inserts
 *             [selection](address), or changes the link at the caret); no button is pressed (plain text
 *             has no state at the caret) and the alignments are aria-disabled
 *   html      the surface holds the canonical HTML, one block a line, as richtext writes it, not
 *             spellchecked; every formatting button, the link and the rule are aria-disabled, and so is
 *             the button of a toolbar's dropdown that holds only those (its click opens nothing). HTML with
 *             no tag is read as richtext reads such a source: a blank line starts a paragraph, a newline
 *             is a line break.
 *
 * In a text surface what is typed is the browser's, and so is its history: undo() and redo() (the
 * buttons, Control+Z) are the surface's own, and a toolbar's insertion is made the way typing is
 * (execCommand("insertText") on the focused surface, which keeps that history; setRangeText where it
 * fails). A switch back to rich is one step of the editor's history (after: the area as the text left
 * it). A switch from rich leaves nothing out (dropped []). A command asked during a composition in the
 * surface (a click on the toolbar, Control+B) is done at its end, at the caret it ends with, as in rich.
 *
 * The surface is a <textarea class="editor-text"> right after the area, with the area's presentation
 * classes (padding, border, radius…), at least as high as the area's own least height (rows), named as
 * the area is (aria-labelledby, aria-label, aria-describedby, aria-required, its placeholder the
 * area's). The area is hidden while it shows, the toolbars' aria-controls name it, and the focus goes to
 * the one shown on a switch, wherever it stood (a format button keeps it from itself, and who presses
 * it writes next), but on a select's change (it stays there: a change of a select moves nothing) and at
 * the start; the caret near the same words where they are found, else at the start. Alt+F10 goes to the
 * toolbar from it, Escape on the toolbar back to it; in Markdown, Control+B, I, U, K are bold, italic,
 * underline and link. aria-pressed of the format buttons (data-kitwork-format-value) is not this
 * file's: the server binds it to format (aria-pressed: format == 'markdown'), so it says what format and
 * $body.format read, at once on a click, and the one shown again when a switch is refused (format is
 * written back here).
 */
(function () {
  var MARKDOWN = "/_kitwork/parts/editor-markdown.js", CONVERTER = Symbol.for("kitwork:editor-markdown"), loaded = null, converter = null;
  var FORMATS = /^(rich|markdown|html)$/, PREFIX = /^ {0,3}(#{1,6}[ \t]+|>[ \t]?|[-*+][ \t]+|\d{1,9}[.)][ \t]+)/, SHORT = 2000;
  // a fence's line, and a line a mark does not go around: a rule, the underline of a heading
  var FENCED = /^ {0,3}(```|~~~)/, BARE = /^ {0,3}([-*_=])(?:[ \t]*\1){2,}[ \t]*$/;
  var WRAPS = { bold: ["**", "**"], italic: ["*", "*"], underline: ["<u>", "</u>"], strikethrough: ["~~", "~~"] };
  function repeat(text, n) { return new Array(n + 1).join(text); }
  function longest(text, re) { return Math.max.apply(null, [0].concat((text.match(re) || []).map(function (run) { return run.length; }))); }
  function parse(html) { return new DOMParser().parseFromString(html, "text/html"); }
  // markdownFile loads the Markdown part once a page: settled once it has run, or failed to (and asked
  // again at the next call: offline a moment, a deploy)
  function markdownFile() {
    return loaded = loaded || new Promise(function (done) {
      if (window[CONVERTER]) return done();
      var tag = document.head.appendChild(document.createElement("script"));
      tag.onload = tag.onerror = function () {
        if (!window[CONVERTER]) loaded = null;
        done();
      };
      tag.src = MARKDOWN;
    });
  }

  function views(mine, editor) {
    var area = mine.area, field = mine.field, scope = mine.scope, host = mine.context.host, listen = mine.context.listen, attribute = editor.attribute;
    var surface = editor.make("textarea"), rich = {};
    var start = /(?:^|;)\s*format:\s*'([a-z]+)'/.exec(host.getAttribute("data-kitwork-scope") || "");
    var view = { shown: "rich", start: start ? start[1] : "rich", first: true, queue: Promise.resolve(), timer: 0, paste: null, link: null, formats: mine.formats, commands: mine.commands,
      composing: false, waiting: [] };
    // the buttons of the toolbars' dropdowns (each a component of its own): none of the editor's commands
    var triggers = [];
    mine.toolbars.forEach(function (bar) {
      editor.list(bar.querySelectorAll("button")).forEach(function (button) {
        if (!button.matches("[data-kitwork-format],[data-kitwork-command]") && button.closest("[data-kitwork-component]") !== host) triggers.push(button);
      });
    });

    surface.className = ("editor-text " + area.className.replace(/(^|\s)(richtext|editor-area|editor-rows-\d+)(?=\s|$)/g, " ")).replace(/\s+/g, " ").trim();
    if (area.id) surface.id = area.id.replace(/area$/, "") + "text";
    ["aria-labelledby", "aria-label", "aria-describedby", "aria-required", "translate"].forEach(function (name) {
      if (area.hasAttribute(name)) surface.setAttribute(name, area.getAttribute(name));
    });
    surface.placeholder = area.getAttribute("aria-placeholder") || "";
    surface.autocomplete = "off";
    surface.readOnly = mine.blocked;
    surface.disabled = field.disabled;
    surface.hidden = true;
    area.parentNode.insertBefore(surface, area.nextSibling);

    // ---- the text into the area: what is typed read onto the grammar ----
    // weight is what richtext.Clean reads of style in a source, and nothing more (a paste reads more:
    // parts/editor-paste.js): b and strong of a normal weight are not bold (its normalWeight)
    function weight(el, at, inner, role) {
      var value = role === "strong" && editor.styleOf(el, "font-weight");
      return value && (/^(normal|lighter)$/.test(value) || /^[+-]?\d+$/.test(value) && +value < 600) ? "" : role;
    }
    function flush() {
      clearTimeout(view.timer);
      view.timer = 0;
      var text = surface.value, doc = view.shown === "markdown" ? parse(converter.html(text)) : /<[a-z\/!?]/i.test(text) ? parse(text) : null;
      area.replaceChildren(view.paste.build(doc ? editor.read(doc.body, weight) : view.paste.plain(text)));
      // two quotes or two lists of a kind side by side are one, as the area holds them (the editor's
      // normalise): the post reads the same whether the writer goes back to rich or not
      editor.list(area.children).forEach(function (el) { if (el.isConnected && /^(UL|OL|BLOCKQUOTE)$/.test(el.tagName)) editor.join(el); });
      editor.sync(mine);
      if (field.value) surface.removeAttribute("aria-invalid");
      var left = doc ? view.paste.leftOut(doc.documentElement) : [];
      if (left.join() !== String(scope.dropped)) scope.dropped = left;
    }
    function changed() {
      clearTimeout(view.timer);
      // at once: a short text (a reading costs less than a frame), the first change since the post was
      // saved, the post turning empty or not (by its letters); else when the typing stops
      if (surface.value.length < SHORT || !scope.unsaved || !field.value === /\S/.test(surface.value)) flush();
      else view.timer = setTimeout(flush, 300);
    }
    listen(surface, "input", function (event) { if (!view.composing && !event.isComposing) changed(); });
    // a composition (an input method writing a word) is read at its end, and what waited for it done then
    listen(surface, "compositionstart", function () { view.composing = true; });
    listen(surface, "compositionend", function () {
      view.composing = false;
      changed();
      view.waiting.splice(0).forEach(function (fn) { fn(); });
    });
    // later is now, or at the end of the composition under way, in the area (the editor's own wait) or in
    // the surface
    function later(fn) {
      if (mine.composing) mine.queue.push(fn);
      else if (view.composing) view.waiting.push(fn);
      else fn();
    }
    listen(surface, "focusout", function () { if (view.timer) flush(); });
    mine.context.cleanup(function () { clearTimeout(view.timer); });
    if (mine.form) listen(mine.form, "formdata", function (event) {
      // after the editor's own listener (added before this one): the text as it is now
      if (view.shown === "rich") return;
      flush();
      if (field.name && !field.disabled) event.formData.set(field.name, field.value);
    });
    listen(field, "invalid", function () {
      if (view.shown === "rich") return;
      surface.setAttribute("aria-invalid", "true");
      surface.focus();
    });
    listen(field, "focus", function () { if (view.shown !== "rich") surface.focus(); });

    // ---- the caret across a switch: near the same words, where they are found ----
    function textCaret() {
      var v = surface.value, at = surface.selectionEnd, from = v.lastIndexOf("\n", at - 1) + 1, to = v.indexOf("\n", at);
      return { before: v.slice(from, at), after: v.slice(at, to < 0 ? v.length : to) };
    }
    function richCaret() {
      var s = getSelection(), r = s.rangeCount && area.contains(s.focusNode) ? s.getRangeAt(0) : mine.kept, line = r && area.contains(r.endContainer) && editor.lineOf(mine, r.endContainer);
      if (!line) return null;
      var probe = document.createRange();
      probe.setStart(line, 0);
      probe.setEnd(r.endContainer, r.endOffset);
      var before = probe.toString();
      probe.setStart(r.endContainer, r.endOffset);
      probe.setEnd(line, line.childNodes.length);
      return { before: before, after: probe.toString() };
    }
    // find is where the caret goes in words: after the longest end of what stood before it on its line
    // that is found (16, 8, 4 or 2 characters), else before the longest start of what stood after it;
    // -1 when neither is
    function find(words, caret) {
      for (var side = 0; caret && side < 2; side++) {
        for (var size = 16; size > 1; size >>= 1) {
          var piece = side ? caret.after.slice(0, size).replace(/\s+$/, "") : caret.before.slice(-size).replace(/^\s+/, ""), at = piece ? words.indexOf(piece) : -1;
          if (at >= 0) return side ? at : at + piece.length;
        }
      }
      return -1;
    }
    function caretInArea(caret) {
      var texts = [], all = "", walker = document.createTreeWalker(area, NodeFilter.SHOW_TEXT), text, at;
      while ((text = walker.nextNode())) { texts.push([text, all.length]); all += text.data; }
      at = find(all, caret);
      area.focus({ preventScroll: true });
      // (at the end of the words before it, where two texts meet: the marks it stands in are theirs; at the
      // start of the words, in the first text)
      for (var i = texts.length; i--;) if (at >= 0 && (texts[i][1] < at || !i)) return editor.collapse(texts[i][0], at - texts[i][1]);
      var line = area.querySelector("p,h2,h3,h4,h5,h6,li,pre");
      if (line) editor.collapse(line, 0);
    }

    // ---- showing a format ----
    function press() {
      var shown = view.shown, html = shown === "html";
      // (in rich the editor's update writes the buttons; a format button's aria-pressed is its binding's)
      if (shown !== "rich") {
        view.formats.forEach(function (button) {
          button.setAttribute("aria-pressed", "false");
          attribute(button, "aria-disabled", (mine.blocked || html) && "true");
        });
        view.commands.forEach(function (button) {
          var name = button.getAttribute("data-kitwork-command"), align = /^align/.test(name);
          if (align) button.setAttribute("aria-pressed", "false");
          attribute(button, "aria-disabled", (mine.blocked || name !== "format" && (align || html && /^(link|unlink|rule)$/.test(name))) && "true");
        });
      }
      // the button of a dropdown whose every button is off here is off too: it opens nothing (below)
      triggers.forEach(function (button) {
        var held = editor.list(button.closest("[data-kitwork-component]").querySelectorAll("[data-kitwork-format],[data-kitwork-command]"));
        attribute(button, "aria-disabled", shown !== "rich" && held.length && held.every(function (b) { return b.getAttribute("aria-disabled") === "true"; }) && "true");
      });
    }
    function apply() {
      var want = scope.format, from = view.shown, active = document.activeElement || document.body, starting = view.first && view.start !== "rich";
      view.first = false;
      if (mine.blocked && want !== view.start || !FORMATS.test(want) || !view.paste || (want === "markdown" || from === "markdown") && !converter) {
        if (want !== from) scope.format = from; // refused: format reads the one shown
        return press();
      }
      if (want === from) return press();
      // the focus follows the switch: a format button keeps it from itself as every button of the editor
      // does, and who presses it writes next (wherever the focus stood: the page's body too); not a
      // change of a select (it stays there: a select's change moves nothing), nor the start
      var moving = !starting && active.tagName !== "SELECT", caret = from === "rich" ? richCaret() : textCaret();
      if (from !== "rich") flush();
      else {
        scope.dropped = []; // a switch from rich leaves nothing out
        // the editor's buttons are the format's while it shows: its update writes none of them
        view.formats = mine.formats;
        view.commands = mine.commands;
        mine.formats = mine.commands = [];
        surface.style.minBlockSize = getComputedStyle(area).minBlockSize;
      }
      view.shown = want;
      mine.toolbars.forEach(function (bar) { bar.setAttribute("aria-controls", (want === "rich" ? area : surface).id); });
      if (want === "rich") {
        surface.hidden = true;
        area.hidden = false;
        mine.formats = view.formats;
        mine.commands = view.commands;
        mine.pending = null;
        if (moving) caretInArea(caret);
        editor.after(mine, false); // one step of the history: the area as the text left it
      } else {
        var blocks = editor.read(area);
        surface.value = want === "html" ? editor.serialize(blocks).html : converter.markdown(blocks);
        surface.spellcheck = want === "markdown";
        area.hidden = true;
        surface.hidden = false;
        if (moving) {
          var at = find(surface.value, caret);
          surface.focus({ preventScroll: true });
          surface.setSelectionRange(Math.max(at, 0), Math.max(at, 0));
        }
      }
      press();
    }
    // ask shows the format asked once the files it needs are here, in the order asked, once no
    // composition is under way (its word is written where it is being written, then the switch)
    view.ask = function () {
      view.queue = view.queue.then(function () {
        var want = scope.format;
        return new Promise(function (done) {
          editor.bring(function (taken) {
            view.paste = taken.build ? taken : null;
            if ((want === "markdown" || view.shown === "markdown") && !converter) {
              markdownFile().then(function () {
                converter = converter || window[CONVERTER] && window[CONVERTER](editor);
                done();
              });
            } else done();
          });
        });
      }).then(function () { return new Promise(later); }).then(apply).catch(function (error) { setTimeout(function () { throw error; }); });
    };

    // ---- the commands in a text surface ----
    function markdown() { return view.shown === "markdown" && !mine.blocked; }
    // put replaces from…to of the text with words the way typing does, then selects a…b
    function put(from, to, words, a, b) {
      surface.focus();
      surface.setSelectionRange(from, to);
      if (from < to || words) {
        if (!document.execCommand(words ? "insertText" : "delete", false, words)) {
          surface.setRangeText(words, from, to, "end");
          surface.dispatchEvent(new Event("input", { bubbles: true }));
        }
      }
      surface.setSelectionRange(a, b);
    }
    // stars counts the * right before at (step -1) or from it on (step 1)
    function stars(v, at, step) {
      for (var n = 0; v.charAt(step < 0 ? at - 1 - n : at + n) === "*"; n++);
      return n;
    }
    // marked: words with the mark's delimiters at their ends (a star by its count: one or three is
    // italic, two or three bold)
    function marked(words, open, close) {
      var size = open.length;
      return words.length >= size + close.length && words.slice(0, size) === open && words.slice(-close.length) === close &&
        (open.charAt(0) !== "*" || (size === 1 ? stars(words, 0, 1) % 2 : stars(words, 0, 1) > 1));
    }
    // wrap puts a mark around the words of each line the selection holds: after the line's own start
    // (# , > , - , 1. ) when it holds the line from there, inside the white space at their ends (Markdown
    // closes no ** after a space: a word double-clicked with the space after it), never on a fence, the
    // code between two fences or a rule; across lines each line its own (Markdown's marks do not cross a
    // block); again where all have it, it comes off
    function wrap(open, close) {
      var v = surface.value, a = surface.selectionStart, b = surface.selectionEnd, size = open.length, spans = [], at = a, code = false;
      v.slice(a, b).split("\n").forEach(function (piece, i) {
        var head = (i || !a || v.charAt(a - 1) === "\n") && PREFIX.exec(piece), from = at + (head ? head[0].length : 0), to = at + piece.length, fence = FENCED.test(piece);
        for (; from < to && /\s/.test(v.charAt(from)); from++);
        for (; to > from && /\s/.test(v.charAt(to - 1)); to--);
        if (from < to && !code && !fence && !BARE.test(piece)) spans.push([from, to]);
        code = code !== fence;
        at += piece.length + 1;
      });
      if (spans.length > 1) {
        var on = spans.every(function (span) { return marked(v.slice(span[0], span[1]), open, close); }), text = "", end = spans[0][0];
        spans.forEach(function (span) {
          var words = v.slice(span[0], span[1]);
          text += v.slice(end, span[0]) + (on ? words.slice(size, -close.length) : open + words + close);
          end = span[1];
        });
        return put(spans[0][0], end, text, spans[0][0], spans[0][0] + text.length);
      }
      if (spans.length) {
        a = spans[0][0];
        b = spans[0][1];
      } else if (a < b) return surface.focus(); // a selection of no words (white space, a rule, a fence): nothing to mark
      var inner = v.slice(a, b);
      // is it on already: marks right outside the selection, or at its ends
      var outside = open.charAt(0) === "*" ? (size === 1 ? stars(v, a, -1) % 2 && stars(v, b, 1) % 2 : stars(v, a, -1) > 1 && stars(v, b, 1) > 1) :
        v.slice(a - size, a) === open && v.slice(b, b + close.length) === close;
      if (outside) return put(a - size, b + close.length, inner, a - size, b - size);
      if (marked(inner, open, close)) return put(a, b, inner.slice(size, -close.length), a, b - size - close.length);
      put(a, b, open + inner + close, a + size, b + size);
    }
    function code() {
      var v = surface.value, a = surface.selectionStart, b = surface.selectionEnd, inner = v.slice(a, b);
      var before = /`+$/.exec(v.slice(0, a)), after = /^`+/.exec(v.slice(b));
      if (before && after && before[0].length === after[0].length) return put(a - before[0].length, b + after[0].length, inner, a - before[0].length, b - before[0].length);
      var fence = repeat("`", longest(inner, /`+/g) + 1), pad = /^`|`$/.test(inner) ? " " : "";
      put(a, b, fence + pad + inner + pad + fence, a + fence.length + pad.length, b + fence.length + pad.length);
    }
    // lines changes the whole lines the selection holds (one it only touches at its start is not one)
    function lines(change) {
      var v = surface.value, a = surface.selectionStart, b = surface.selectionEnd;
      if (b > a && v.charAt(b - 1) === "\n") b--;
      var from = v.lastIndexOf("\n", a - 1) + 1, to = v.indexOf("\n", b);
      if (to < 0) to = v.length;
      change(v, from, to, v.slice(from, to).split("\n"));
    }
    function kindOf(line) {
      var m = PREFIX.exec(line), head = m && m[1].trim();
      return !m ? "" : /^\d/.test(head) ? "1." : /^[-*+]$/.test(head) ? "-" : head.charAt(0) === ">" ? ">" : head;
    }
    function block(format) {
      lines(function (v, from, to, held) {
        if (format === "code-block") {
          var above = /(^|\n)(`{3,})[^`\n]*\n$/.exec(v.slice(0, from)), below = /^\n(`{3,})[ \t]*(?=\n|$)/.exec(v.slice(to));
          if (above && below && below[1].length >= above[2].length) {
            var top = from - above[0].length + above[1].length;
            return put(top, to + below[0].length, held.join("\n"), top, top + to - from);
          }
          var fence = repeat("`", Math.max(3, longest(held.join("\n"), /`+/g) + 1));
          return put(from, to, fence + "\n" + held.join("\n") + "\n" + fence, from + fence.length + 1, to + fence.length + 1);
        }
        var want = format === "quote" ? ">" : format === "list" ? "-" : format === "list-ordered" ? "1." : repeat("#", +format.slice(8)), number = 0;
        var off = held.every(function (line) { return !/\S/.test(line) || kindOf(line) === want; });
        var text = held.map(function (line) {
          if (!/\S/.test(line)) return off || want !== ">" ? line : ">";
          var words = line.replace(PREFIX, "");
          return off ? words : (want === "1." ? ++number + ". " : want + " ") + words;
        }).join("\n");
        put(from, to, text, from, from + text.length);
      });
    }
    function rule() {
      var v = surface.value, end = v.indexOf("\n", surface.selectionEnd);
      if (end < 0) end = v.length;
      var before = v.slice(0, end), text = (/\S/.test(before) ? /\n[ \t]*\n[ \t]*$/.test(before) ? "" : /\n[ \t]*$/.test(before) ? "\n" : "\n\n" : "") + "---\n";
      put(end, end, text, end + text.length, end + text.length);
    }
    // linkAt is the Markdown link around a place of the text, on its line
    function linkAt(at) {
      var v = surface.value, from = v.lastIndexOf("\n", at - 1) + 1, to = v.indexOf("\n", at), line = v.slice(from, to < 0 ? v.length : to), found = null, m;
      var re = /\[((?:[^[\]\\\n]|\\.)*)\]\((<(?:[^<>\\\n]|\\.)*>|(?:[^()\s\\]|\\.|\([^()\s]*\))*)\)/g;
      while ((m = re.exec(line))) if (from + m.index <= at && at <= from + m.index + m[0].length) found = m;
      return found && { from: from + found.index, to: from + found.index + found[0].length, words: found[1], href: found[2].replace(/^<([\s\S]*)>$/, "$1").replace(/\\([\s\S])/g, "$1") };
    }
    var typed = {
      toggle: function (format, variant) {
        if (!markdown()) return;
        var name = format === "heading" ? "heading-" + variant : format === "list" && variant === "ordered" ? "list-ordered" : format === "code" && variant === "block" ? "code-block" : format;
        if (WRAPS[name]) wrap(WRAPS[name][0], WRAPS[name][1]);
        else if (name === "code") code();
        else if (/^(heading-[2-6]|quote|list|list-ordered|code-block)$/.test(name)) block(name);
      },
      align: function () {},
      rule: function () { if (markdown()) rule(); },
      link: function (address) {
        if (!markdown()) return;
        var a = surface.selectionStart, b = surface.selectionEnd;
        if (!arguments.length) {
          // the link at the caret, else the selection; the popover opens with its address
          view.link = linkAt(a) || { from: a, to: b, words: surface.value.slice(a, b), href: "" };
          mine.saved = null; // the popover stands where its place puts it: no words of the area to stand by
          scope.address = view.link.href;
          editor.show(mine, true);
          if (mine.input) {
            mine.input.value = view.link.href;
            mine.input.focus();
            mine.input.select();
          }
          return;
        }
        var href = editor.safeLink(address, true);
        if (!href) return rich.link.call(this, address); // refused: the editor says so in the popover
        editor.show(mine, false);
        var at = view.link || { from: a, to: b, words: surface.value.slice(a, b) };
        var text = "[" + (at.words || href.replace(/[\\[\]*_`<]/g, "\\$&")) + "](" + converter.url(href) + ")";
        view.link = null;
        put(at.from, at.to, text, at.from + text.length, at.from + text.length);
        return true;
      },
      unlink: function () {
        if (!markdown()) return;
        var at = view.link && view.link.href ? view.link : linkAt(surface.selectionStart);
        editor.show(mine, false);
        view.link = null;
        if (at && at.href) put(at.from, at.to, at.words, at.from, at.from + at.words.length);
        else surface.focus();
      },
      close: function () {
        editor.show(mine, false);
        view.link = null;
        surface.focus();
      },
      undo: function () { surface.focus(); document.execCommand("undo"); },
      redo: function () { surface.focus(); document.execCommand("redo"); }
    };
    // the editor's commands are the format's while a text format shows; one asked during a composition
    // in the surface is done at its end (in the area the editor's own command() waits)
    Object.keys(typed).forEach(function (name) {
      rich[name] = scope[name];
      scope[name] = function () {
        var self = this, args = arguments, run = function () { return (view.shown === "rich" ? rich[name] : typed[name]).apply(self, args); };
        if (view.shown === "rich" || !view.composing) return run();
        view.waiting.push(run);
      };
    });

    // ---- the keyboard ----
    var KEYS = { b: ["toggle", "bold"], i: ["toggle", "italic"], u: ["toggle", "underline"], k: ["link"] };
    listen(surface, "keydown", function (event) {
      if (event.altKey && event.key === "F10") {
        var first = mine.toolbars[0] && mine.toolbars[0].querySelector('button[tabindex="0"]');
        if (first) { event.preventDefault(); first.focus(); }
        return;
      }
      var key = KEYS[(event.key || "").toLowerCase()];
      if (!key || event.altKey || event.shiftKey || !(mine.apple ? event.metaKey : event.ctrlKey) || view.shown !== "markdown") return;
      event.preventDefault();
      scope[key[0]].apply(scope, key.slice(1));
    });
    // Escape on a toolbar goes back to the surface (the editor sends it to the area, hidden): not from a
    // dropdown's popover, whose Escape goes to its button
    mine.toolbars.forEach(function (bar) {
      listen(bar, "keydown", function (event) {
        var pop = event.target.parentNode && event.target.parentNode.closest(".popover");
        if (event.key === "Escape" && event.defaultPrevented && view.shown !== "rich" && !(pop && bar.contains(pop))) surface.focus();
      });
      // a dropdown's button that is off opens nothing: its click stops here, before the kernel's (on the
      // document) runs its toggle()
      listen(bar, "click", function (event) {
        var button = event.target.closest && event.target.closest("button");
        if (triggers.indexOf(button) >= 0 && button.getAttribute("aria-disabled") === "true") event.stopPropagation();
      });
    });
    return view;
  }

  window[Symbol.for("kitwork:editor-format")] = function (mine, editor) {
    (mine.view = mine.view || views(mine, editor)).ask();
  };
})();
