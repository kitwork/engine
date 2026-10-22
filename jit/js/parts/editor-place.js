/* editor-place — what floats over the editor (components/editor/v2.0.0.js), put in its place: the
 * tooltips of its toolbars, and the link's popover right under the words it links. It is a file of its
 * own, delivered as /_kitwork/parts/editor-place.<hash>.js and loaded by the editor once a page at the
 * first focus in an editor or hover on one of its toolbars (never with the page): the editor stays
 * under 11 KiB gzip, and a page only read never fetches it.
 *
 * The file defines one function, under a symbol the editor knows, that the editor calls with itself
 * (mine) until the function has set itself up (mine.hears); from then on it hears the pointer and the
 * focus itself, on the document. While the file loads the editor keeps each focusin in it and each
 * pointerover and pointerout (not a touch's) of its toolbars (mine.heard): the file takes them in the
 * order they came, so what shows is what the pointer and the focus are on once it is here, not what they
 * crossed on the way. The editor also keeps what came last before a focus, a press (mine.pressed true)
 * or a key (false), from the page's start. It gives the editor mine.place, which the editor calls each
 * time its popover opens or shuts.
 *
 * Tooltips:
 *   - a button of one of the editor's toolbars that shows no words (an icon's, named by its alt, the
 *     icon's aria-label), not in a dropdown's popover, shows its tooltip after 500 ms of a hover by a
 *     mouse or a pen, or of the keyboard's focus: a focus that follows a key (Tab, Alt+F10, an arrow),
 *     never one that follows a press (a click on the dropdown's trigger); before any press or key the
 *     browser's :focus-visible says. Of the two, the one that moved last says which button (the pointer
 *     going elsewhere gives the tooltip back to the focused button, and the focus to the hovered one).
 *     At once when a tooltip shows, or hid less than 300 ms ago: along the toolbar, across the gap
 *     between two buttons or two groups, an arrow from one button to the next. A button with words of
 *     its own has none;
 *   - its words are the button's name and, when it has aria-keyshortcuts, its keys as the platform
 *     writes them: "Đậm · Ctrl+B", "Làm lại · Ctrl+Shift+Z", on an Apple device (the editor wrote Meta+
 *     there) "Đậm · ⌘B", "Làm lại · ⇧⌘Z";
 *   - WCAG 1.4.13: Escape hides it, and that Escape is the tooltip's alone (no listener of the editor,
 *     the dropdown or the page hears it: the toolbar's Escape back to the area is the next one); the
 *     pointer may move onto it, through the 4px gap to it, and it stays (the tooltip takes no press, so
 *     the pointer on it is read from where it is, not what it is on: a button of a toolbar it covers,
 *     the next row of a toolbar that wraps, is the pointer's, hovered and pressed as if it were not
 *     there); it stays until the hover or the focus that showed it goes elsewhere (the pointer out of
 *     the window too), an Escape, a press, or a key that is no modifier (a command, an arrow), and the
 *     same button shows it again only once the hover or the focus has left;
 *   - none on a touch screen (pointer: coarse) nor for a touch, none while the link's popover or a
 *     dropdown of a toolbar is open;
 *   - one element per editor, made here at the first, the host's last child: <div class="editor-tip"
 *     role="tooltip" id="kitwork-editor-N-tip" hidden> (the area's id, -tip for -area; its look is
 *     ui/stylesheet/editor.go's), under the button, centred on it, inside the viewport: above it when the
 *     viewport has no room under it, along at the edges. It repeats the button's name, so no button is
 *     aria-describedby it (a reader would say the name twice); its keys are aria-keyshortcuts already.
 *
 * The link's popover (place): it stands by the words of the selection the editor kept, or of the link at
 * a caret (their text alone: a block wholly in the selection has a box of its own, which is no line), as
 * its place reads from them: below-left (or no place) under their last line, its left edge at their
 * start on that line, below centred on that line, below-right its right edge at their end; above-left,
 * above, above-right the same over their first line, held by its bottom edge (an address refused shows
 * its error: it grows away from the words), so it never covers a line of them. Left and right mirror on a
 * page written right to left (below-left: at their start, on the right). Without room in the viewport on
 * its side, it goes to the other; it stays inside the box it is placed in (the editor's) and the
 * viewport, measured to the fraction of a pixel, so a popover moved in from the right edge never wraps.
 * Its style is the editor's alone (.ui writes none): set as it opens, on a resize and on the writing
 * area's own scroll while it is open (the page's scroll moves it with the host, which it is placed in),
 * taken off as it shuts.
 */
window[Symbol.for("kitwork:editor-place")] = function (mine) {
  mine.hears = mine.hears || start(mine);
};

// pad is the padding box of el in the viewport: where what it places absolutely may stand (its right edge
// to the fraction of a pixel: what stands against it keeps its width)
function pad(el) {
  var box = el.getBoundingClientRect(), style = getComputedStyle(el);
  return { left: box.left + el.clientLeft, top: box.top + el.clientTop, right: box.right - parseFloat(style.borderRightWidth), bottom: box.bottom - parseFloat(style.borderBottomWidth) };
}

function start(mine) {
  var context = mine.context, popover = mine.popover, tip = document.createElement("div"), view = document.documentElement;
  // the buttons the pointer and the keyboard's focus are on, which of the two moved last, the button the
  // tooltip is for (showing, waiting, or hidden by an Escape, a press, a key), until when another shows
  // at once
  var hovered = null, focused = null, byPointer = false, shown = null, timer = 0, warm = 0;
  tip.className = "editor-tip";
  tip.id = mine.area.id.replace(/-area$/, "") + "-tip";
  tip.setAttribute("role", "tooltip");
  tip.hidden = true;
  context.host.appendChild(tip);

  function hide() {
    clearTimeout(timer);
    if (!tip.hidden) warm = Date.now() + 300;
    tip.hidden = true;
  }
  // buttonOf is the button a tooltip is for under target: of one of the editor's toolbars, showing no
  // words, not in a dropdown's popover; null for anything else
  function buttonOf(target) {
    var button = target && target.closest && target.closest("button");
    return button && !button.textContent.trim() && !button.closest(".popover") && mine.toolbars.some(function (bar) { return bar.contains(button); }) ? button : null;
  }
  // busy: no tooltip now, on a touch screen or while a popover of the editor is open
  function busy() {
    return mine.scope.open || matchMedia("(pointer: coarse)").matches || mine.toolbars.some(function (bar) { return bar.querySelector(".popover:not([hidden])"); });
  }
  // open shows the tooltip of button: its name, its keys; under it, centred, inside the viewport
  function open(button) {
    var icon = button.querySelector("[aria-label]"), keys = button.getAttribute("aria-keyshortcuts");
    var name = button.getAttribute("aria-label") || icon && icon.getAttribute("aria-label");
    if (!name || busy() || !button.isConnected) return;
    tip.textContent = name + (keys ? " · " + (mine.apple ? (/Shift/.test(keys) ? "⇧" : "") + "⌘" + keys.slice(-1) : keys.replace("Control", "Ctrl")) : "");
    tip.hidden = false;
    if (!tip.isConnected) context.host.appendChild(tip); // a morph of the page that kept the host took it away
    var b = button.getBoundingClientRect(), t = tip.getBoundingClientRect(), box = pad(tip.offsetParent), h = t.height + 4;
    tip.style.left = Math.max(4, Math.min(b.left + (b.width - t.width) / 2, view.clientWidth - t.width - 4)) - box.left + "px";
    tip.style.top = (b.bottom + h > view.clientHeight && b.top >= h ? b.top - h : b.bottom + 4) - box.top + "px";
  }
  // update gives the tooltip to the button of the one that moved last, the pointer or the focus (else of
  // the other): after 500 ms, at once while one shows or for 300 ms after one hid; nothing when it is
  // for that button already
  function update() {
    var button = byPointer ? hovered || focused : focused || hovered;
    if (button === shown) return;
    var now = !tip.hidden || Date.now() < warm;
    hide();
    shown = button;
    if (button) timer = setTimeout(function () { open(button); }, now ? 0 : 500);
  }
  // pointed is the pointer (not a touch) over, along or out of something: the button it is on now (none
  // out of the window); on the tooltip it shows, or the gap between them, it stays on that button
  function pointed(event) {
    var to = event.type === "pointerout" ? event.relatedTarget : event.target, button = buttonOf(to);
    if (event.pointerType === "touch" || button === hovered || !button && to && hovered === shown && !tip.hidden && onTip(event)) return;
    hovered = button;
    byPointer = true;
    update();
  }
  // onTip: the point of event is on the tooltip, or in the 4px between it and its button
  function onTip(event) {
    var t = tip.getBoundingClientRect(), x = event.clientX, y = event.clientY;
    return x >= t.left && x <= t.right && y >= t.top - 5 && y <= t.bottom + 5;
  }
  // focusIn is a focus: on a button, the keyboard's when a key came before it (a press's shows none;
  // before the first press or key, the browser's :focus-visible says)
  function focusIn(event) {
    var button = buttonOf(event.target), pressed = mine.pressed;
    if (button && (pressed || pressed == null && !button.matches(":focus-visible"))) button = null;
    if (button === focused) return;
    focused = button;
    byPointer = false;
    update();
  }
  ["pointerover", "pointermove", "pointerout"].forEach(function (type) { context.listen(document, type, pointed); });
  context.listen(document, "focusin", focusIn);
  // a press hides it, and so does a key that is no modifier; Escape while it shows is its alone
  context.listen(window, "pointerdown", hide, true);
  context.listen(window, "keydown", function (event) {
    if (event.key === "Escape" && !tip.hidden && !event.isComposing) {
      event.preventDefault();
      event.stopImmediatePropagation();
    }
    if (!/^(Shift|Control|Alt|Meta)$/.test(event.key)) hide();
  }, true);

  // place stands the popover by the words it links (see above), or takes its style off when it is shut
  function place() {
    var r = mine.saved, s = popover && popover.style;
    if (!s) return;
    if (popover.hidden || !r) return popover.removeAttribute("style");
    hide();
    var from = r.startContainer, link = r.collapsed && (from.nodeType === 1 ? from : from.parentNode).closest("a");
    if (link && mine.area.contains(link)) (r = document.createRange()).selectNodeContents(link);
    // the boxes of the words' text, their lowest bottom and highest top
    var root = r.commonAncestorContainer, walk = document.createTreeWalker(root.nodeType === 3 ? root.parentNode : root, 4), part = document.createRange(), text;
    var rects = [], low = -Infinity, high = Infinity, left = Infinity, right = -Infinity;
    while ((text = walk.nextNode())) {
      if (!r.intersectsNode(text)) continue;
      part.selectNodeContents(text);
      if (text === r.startContainer) part.setStart(text, r.startOffset);
      if (text === r.endContainer) part.setEnd(text, r.endOffset);
      Array.prototype.forEach.call(part.getClientRects(), function (b) {
        if (!b.width) return;
        rects.push(b);
        low = Math.max(low, b.bottom);
        high = Math.min(high, b.top);
      });
    }
    if (!rects.length) return;
    var word = /(^| )place-(below|above)(-left|-right)?/.exec(popover.className) || [0, 0, "below", "-left"], side = word[3], up = word[2] === "above";
    if (side && getComputedStyle(popover).direction === "rtl") side = side === "-left" ? "-right" : "-left";
    s.cssText = "inset:0 auto auto 0;margin:0;translate:none";
    var size = popover.getBoundingClientRect(), w = size.width, h = size.height + 8, room = view.clientHeight, box = pad(popover.offsetParent);
    if (up ? high < h && room - low > high : room - low < h && high > room - low) up = !up; // no room on its side: the other
    // the line it stands by: under the words their last, over them their first (what stands less than
    // half its own height from their edge)
    rects.forEach(function (b) {
      if (up ? b.top - b.height / 2 < high : b.bottom + b.height / 2 > low) {
        left = Math.min(left, b.left);
        right = Math.max(right, b.right);
      }
    });
    var x = side === "-left" ? left : side ? right - w : (left + right - w) / 2;
    s.left = Math.max(Math.min(x, box.right - w, view.clientWidth - w), box.left, 0) - box.left + "px";
    if (!up) s.top = low + 8 - box.top + "px";
    else {
      s.top = "auto";
      s.bottom = box.bottom - high + 8 + "px";
    }
  }
  mine.place = place;
  context.listen(window, "resize", place);
  context.listen(mine.area, "scroll", place);
  place(); // a popover opened before this file came
  // what the editor kept while this file loaded, in the order it came
  mine.heard.splice(0).forEach(function (event) { (event.type === "focusin" ? focusIn : pointed)(event); });
  return true;
}
