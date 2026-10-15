/* terminal component — what a terminal the author draws does (KITWORK_UI §11; ui/terminal.go
 * compiles the markup). The author writes the whole look; the engine writes only a prompt before each
 * command and an empty polite status region at the end of the host. Inside the host every <code> is a
 * command, wherever it sits (a <code> in a bar the author draws is one too), except inside a component
 * nested in it, whose code is its own. A command's prompt is outside its <code>:
 *
 *   <div class="flex-column …" data-kitwork-component="terminal: $install" data-kitwork-scope="typing: 3000">
 *     <div class="flex-row …">
 *       <p class="size-small font-code">bash</p>
 *       <button type="button" data-kitwork-bind="text: copied ? 'Copied' : 'Copy'"
 *               data-kitwork-event="click: copy()">Copy</button>
 *     </div>
 *     <div class="flex-column padding-4">
 *       <pre><span class="code-prompt">$ </span><code class="language-bash">git clone …</code></pre>
 *       <p class="color-ink-30">Cloning into 'engine'...</p>
 *       <pre><span class="code-prompt">$ </span><code class="language-bash">cd engine</code></pre>
 *     </div>
 *     <span class="reader" role="status"></span>
 *   </div>
 *
 *   copy()   put the commands on the clipboard (kit.clipboard: the system pasteboard in a desktop or
 *            mobile shell), one a line, in page order: the text of each <code> the host owns and shows
 *            (shown: one a show hides is neither copied nor typed) — never a prompt (outside the
 *            <code>), a <text>, nor the code of a nested component. It says what
 *            it did, politely, in the status region (in the page's language: Vietnamese for vi or a
 *            page that names none, English otherwise); the author writes no status markup. A copy
 *            while the last one is still said empties the region first, so it is heard again.
 *            Typing that has not finished finishes first.
 *   copied   true for two seconds after the commands reached the clipboard, then false
 *   typing   the total time typing every command takes, in milliseconds (typing="3s" → 3000); 0 types
 *            nothing
 *
 * A bare copy() inside the host is the nearest terminal's; from anywhere else, its alias: as="$install"
 * on the host, then $install.copy() and $install.copied.
 *
 * Typing: every character of the HTML is there from the first byte and nothing here moves, removes or
 * delays it. Typing only names CSS Custom Highlight ranges: what is not typed yet — from the next
 * character of the command being typed to the end of the host — is painted transparent
 * (::highlight(kitwork-terminal-untyped)) and the next character sits under a block caret
 * (::highlight(kitwork-terminal-caret)), so a screen reader, find in page, a selection and a reader
 * without JavaScript have the whole text at once. What lies before the first command (a bar the author
 * draws) shows from the start; what lies between two commands (what one printed, the next prompt)
 * shows when the first is entered. The whole of typing takes the time written: a share (WAITS) waits
 * before each command and after it, the rest types the characters, each a little longer or shorter
 * than the mean, so a longer command types faster. Its clock runs only while someone can see the
 * terminal — at least half of it on screen (or half the window, when it is taller), in a tab that is
 * not hidden — from the first frame it is seen in, and is read once a frame, painting the furthest
 * character due on the frame nearest its time: nothing late adds up, and typing ends within a frame of
 * the time written (a timer would be late by the system's tick, 15.6 ms on Windows). Everything shows
 * at once under prefers-reduced-motion, in forced colours (where a
 * highlight's transparent text would show as a selection), in a browser without the Highlight API,
 * before printing and after a copy. A command wider than its <pre> scrolls after the caret, and back to
 * its prompt once entered.
 */
var TERMINAL = Symbol("kit:terminal");
var UNTYPED = "kitwork-terminal-untyped";
var CARET = "kitwork-terminal-caret";
// how long copied is true, and the status region keeps its sentence, after a copy
var SAID = 2000;
// the status region is emptied, then given its sentence this much later: a polite region speaks a
// change, so a copy made while the last one is still said is heard too
var BEAT = 100;
// the share of typing spent waiting, before the first character of each command and after its last;
// the rest types the characters, each between CHARACTER_LEAST and CHARACTER_MOST of the mean
var WAITS = 0.3;
var CHARACTER_LEAST = 0.5;
var CHARACTER_MOST = 1.5;

var WORDS = {
  vi: { copied: "Đã chép lệnh vào bộ nhớ tạm", failed: "Không chép được lệnh" },
  en: { copied: "Copied the commands to the clipboard", failed: "Could not copy the commands" }
};

// ownOf is what an instance keeps for itself, unseen by markup (a Symbol is never a name)
function ownOf(scope) {
  return scope[TERMINAL] || (scope[TERMINAL] = {});
}

var terminalDef = {
  typing: 0,
  copied: false,

  copy: function () {
    var self = this;
    var context = window.kit.context(this);
    var own = ownOf(this);
    if (own.typist) finish(own.typist);
    var text = shown(context).map(function (code) { return code.textContent; }).join("\n");
    // where the focus is (the button pressed): a clipboard without navigator.clipboard copies through
    // a textarea it selects and takes out, which leaves the focus nowhere, so it is given back
    var before = document.activeElement;
    var written;
    try {
      written = Promise.resolve(window.kit.clipboard.writeText(text));
    } catch (problem) {
      written = Promise.reject(problem);
    }
    var settle = function (worked) {
      var now = document.activeElement;
      if (before && before !== now && before.isConnected && (!now || now === document.body) && typeof before.focus === "function") {
        before.focus({ preventScroll: true });
      }
      said(self, own, context.host, worked);
    };
    return written.then(function () { settle(true); }, function () { settle(false); });
  },

  init: function (context) {
    var own = ownOf(this);
    context.cleanup(function () {
      clearTimeout(own.reset);
      clearTimeout(own.saying);
      if (own.typist) finish(own.typist);
    });
    var total = Number(this.typing);
    if (!(total > 0) || typeof Highlight !== "function" || !window.CSS || !CSS.highlights) return;
    var motion = window.matchMedia("(prefers-reduced-motion: reduce)");
    var forced = window.matchMedia("(forced-colors: active)");
    if (motion.matches || forced.matches) return;
    var plan = planOf(shown(context), total);
    if (!plan) return;
    var typist = own.typist = {
      host: context.host, stops: plan.stops, index: 0, elapsed: 0, since: -1, last: 0, period: 1000 / 60,
      running: false, frame: 0,
      done: false, scrolled: [], untyped: new Range(), caret: new Range(), observer: null
    };
    context.listen(motion, "change", function () { if (motion.matches) finish(typist); });
    context.listen(forced, "change", function () { if (forced.matches) finish(typist); });
    context.listen(window, "beforeprint", function () { finish(typist); });
    // the clock runs only while someone can see the terminal; on screen now or not is read at once,
    // so typing seen from the first paint starts with it
    var onScreen = seen(context.host);
    var watch = function () {
      if (onScreen && !document.hidden) run(typist);
      else hold(typist);
    };
    context.listen(document, "visibilitychange", watch);
    typist.observer = new IntersectionObserver(function (entries) {
      var entry = entries[entries.length - 1];
      onScreen = entry.isIntersecting &&
        (entry.intersectionRatio >= 0.5 || entry.intersectionRect.height >= window.innerHeight / 2);
      watch();
    }, { threshold: [0, 0.25, 0.5, 0.75, 1] });
    typist.observer.observe(context.host);
    // last: anything above that throws leaves the text as the HTML has it, shown
    paint(typist, plan.start, plan.stops[0]);
    watch();
  }
};

// said tells what a copy did: copied for SAID, and the sentence in the status region a beat after it
// is emptied.
function said(scope, own, host, worked) {
  clearTimeout(own.reset);
  clearTimeout(own.saying);
  scope.copied = worked;
  var region = regionOf(host);
  if (region) {
    region.textContent = "";
    own.saying = setTimeout(function () {
      region.textContent = wordsOf(host)[worked ? "copied" : "failed"];
    }, BEAT);
  }
  own.reset = setTimeout(function () {
    scope.copied = false;
    if (region) region.textContent = "";
  }, SAID);
}

// shown is the commands a reader has, in page order: a command hidden inside the host (show="false", a
// medium:show="false" below that width, a show from data that is false) is neither copied nor typed —
// a terminal holding PowerShell for a wide screen and bash for a narrow one copies the one in sight.
// When the host itself is not drawn (a closed panel around it), every command is.
function shown(context) {
  var commands = context.owned("code");
  if (!context.host.getClientRects().length) return commands;
  return commands.filter(function (code) { return code.getClientRects().length > 0; });
}

// regionOf is the polite status region the engine writes last in the host
function regionOf(host) {
  for (var child = host.lastElementChild; child; child = child.previousElementSibling) {
    if (child.matches('.reader[role="status"]')) return child;
  }
  return null;
}

// wordsOf is the sentences in the language of the page around host: Vietnamese for vi (vi-VN…) or a
// page that names none, English for any other
function wordsOf(host) {
  var marked = host.closest("[lang]");
  var language = marked ? String(marked.getAttribute("lang")).toLowerCase() : "";
  return language === "" || language.indexOf("vi") === 0 ? WORDS.vi : WORDS.en;
}

// seen reports whether host is on screen as the IntersectionObserver above counts it
function seen(host) {
  var box = host.getBoundingClientRect();
  var height = Math.min(box.bottom, window.innerHeight) - Math.max(box.top, 0);
  var width = Math.min(box.right, window.innerWidth) - Math.max(box.left, 0);
  if (height <= 0 || width <= 0) return false;
  return height >= box.height * 0.5 || height >= window.innerHeight / 2;
}

// highlightNamed is the page's highlight of a name, made on first use: the name belongs to the page,
// so every terminal typing at once keeps its own ranges in the same one
function highlightNamed(name, priority) {
  var found = CSS.highlights.get(name);
  if (!found) {
    found = new Highlight();
    found.priority = priority;
    CSS.highlights.set(name, found);
  }
  return found;
}

// planOf reads the commands into where typing stops, each at the time it is due (ms of typing):
// after each character of a command, then where it is entered — the first character of the next
// command, everything between shown at once as a terminal prints it, or the end (node null) at total.
// Typing starts at the first character of the first command. null when no command has a character.
function planOf(commands, total) {
  var typed = [];
  var count = 0;
  commands.forEach(function (code) {
    var characters = [];
    var walker = document.createTreeWalker(code, NodeFilter.SHOW_TEXT);
    for (var node = walker.nextNode(); node; node = walker.nextNode()) {
      var text = node.data;
      for (var at = 0; at < text.length;) {
        var unit = text.charCodeAt(at);
        var width = unit >= 0xd800 && unit <= 0xdbff && at + 1 < text.length ? 2 : 1;
        characters.push({ node: node, from: at, offset: at + width });
        at += width;
      }
    }
    if (characters.length) {
      typed.push({ scroller: scrollerOf(code), characters: characters });
      count += characters.length;
    }
  });
  if (!typed.length) return null;
  var wait = total * WAITS / (2 * typed.length);
  var weights = [], sum = 0;
  for (var i = 0; i < count; i++) {
    var weight = CHARACTER_LEAST + Math.random() * (CHARACTER_MOST - CHARACTER_LEAST);
    weights.push(weight);
    sum += weight;
  }
  var share = total * (1 - WAITS) / sum;
  var stops = [];
  var due = 0, k = 0;
  typed.forEach(function (command, j) {
    command.characters.forEach(function (character, c) {
      due += (c === 0 ? wait : 0) + weights[k++] * share;
      stops.push({ node: character.node, from: character.from, offset: character.offset, due: due, character: true, scroller: command.scroller });
    });
    due += wait;
    var next = typed[j + 1];
    stops.push(next ? { node: next.characters[0].node, offset: 0, due: due } : { node: null, due: total });
  });
  return { start: { node: typed[0].characters[0].node, offset: 0 }, stops: stops };
}

// scrollerOf is the <pre> a command scrolls sideways in (a block), null for one in a line
function scrollerOf(code) {
  var parent = code.parentElement;
  return parent && parent.tagName === "PRE" ? parent : null;
}

// paint shows the host typed up to position: what follows it transparent, and the caret over the
// character the next stop types (none while a command waits to be entered). The ranges are taken out
// and put back, so the page repaints them.
function paint(typist, position, next) {
  var untyped = highlightNamed(UNTYPED, 0), caret = highlightNamed(CARET, 1);
  untyped.delete(typist.untyped);
  caret.delete(typist.caret);
  if (!position || !position.node) return;
  typist.untyped.setStart(position.node, position.offset);
  typist.untyped.setEnd(typist.host, typist.host.childNodes.length);
  untyped.add(typist.untyped);
  if (next && next.character) {
    typist.caret.setStart(next.node, next.from);
    typist.caret.setEnd(next.node, next.offset);
    caret.add(typist.caret);
    follow(typist, next.scroller);
  }
}

// follow keeps the caret in sight in a <pre> narrower than its command (a phone): past its right edge
// the <pre> scrolls after it
function follow(typist, scroller) {
  if (!scroller || scroller.scrollWidth <= scroller.clientWidth) return;
  var box = scroller.getBoundingClientRect(), at = typist.caret.getBoundingClientRect();
  var right = box.left + scroller.clientLeft + scroller.clientWidth;
  if (at.right > right) {
    scroller.scrollLeft += Math.ceil(at.right - right + (parseFloat(getComputedStyle(scroller).paddingRight) || 0));
    if (typist.scrolled.indexOf(scroller) < 0) typist.scrolled.push(scroller);
  }
}

// rewind scrolls every <pre> that followed the caret back to its start, so an entered command reads
// from its prompt
function rewind(typist) {
  typist.scrolled.forEach(function (scroller) { scroller.scrollLeft = 0; });
  typist.scrolled = [];
}

// run starts the clock, or starts it again where it stopped, from the next frame: the first one the
// reader can see the terminal typing in (a page still loading paints none)
function run(typist) {
  if (typist.done || typist.running) return;
  typist.running = true;
  typist.since = -1;
  typist.last = 0;
  nextFrame(typist);
}

// hold stops the clock, keeping the time typing has run
function hold(typist) {
  if (!typist.running) return;
  typist.running = false;
  if (typist.since >= 0) typist.elapsed += Math.max(0, performance.now() - typist.since);
  cancelAnimationFrame(typist.frame);
  typist.frame = 0;
}

// nextFrame reads the clock again on the next frame
function nextFrame(typist) {
  typist.frame = requestAnimationFrame(function (time) { tick(typist, time); });
}

// tick paints the furthest stop that is due at the frame's time; at the end typing finishes
function tick(typist, time) {
  typist.frame = 0;
  if (typist.done || !typist.running) return;
  if (typist.since < 0) typist.since = time;
  // a stop is painted on the frame nearest its time: due within half a frame from now is due now. A
  // frame lasts the shortest time seen between two (a dropped frame is longer, never shorter)
  if (typist.last > 0 && time - typist.last > 0 && time - typist.last < typist.period) typist.period = time - typist.last;
  typist.last = time;
  var now = typist.elapsed + Math.max(0, time - typist.since) + typist.period / 2;
  var stops = typist.stops, index = typist.index, entered = false;
  while (index < stops.length && stops[index].due <= now) {
    if (!stops[index].character) entered = true;
    index++;
  }
  if (index >= stops.length) {
    finish(typist);
    return;
  }
  if (index !== typist.index) {
    typist.index = index;
    if (entered) rewind(typist);
    paint(typist, stops[index - 1], stops[index]);
  }
  nextFrame(typist);
}

// finish shows everything at once, for good
function finish(typist) {
  if (typist.done) return;
  typist.done = true;
  typist.running = false;
  cancelAnimationFrame(typist.frame);
  typist.frame = 0;
  if (typist.observer) typist.observer.disconnect();
  paint(typist, null, null);
  rewind(typist);
}

window.kit.component("terminal", terminalDef);
