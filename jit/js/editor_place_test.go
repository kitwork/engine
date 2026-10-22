package js

import (
	"regexp"
	"strings"
	"testing"

	"github.com/kitwork/engine/utilities/richtext"
)

// What floats over the editor, put in its place by its part parts/editor-place.js (21/10/2026, the
// owner: "thêm tooltip vào từng button editor và chỗ link nó phải hiện dưới chỗ nút ta cần điền liên
// kết"): a tooltip on each button of a toolbar that shows no words, and the link's popover right under
// the words it links. The proofs run in a real browser on the wire of toolbar v2 (editorWireV2) with the
// icons the engine writes for <icon alt> in its buttons, the part loaded with the page (as on a page that
// was focused before), except TestBrowserEditorLoadsItsPlacePart.

// editorIconNames are the names the canonical toolbar's icons say (docs/components/editor.md, "Cách
// dùng"), by the id of their button in editorWireV2. Undo keeps its words ("undo"): a button that shows
// words has no tooltip.
var editorIconNames = map[string]string{
	"bold": "Đậm", "italic": "Nghiêng", "underline": "Gạch chân", "strike": "Gạch ngang", "code": "Mã",
	"h2": "Tiêu đề mục", "h3": "Tiêu đề nhỏ", "h4": "Tiêu đề mức 4", "h5": "Tiêu đề mức 5", "h6": "Tiêu đề mức 6",
	"quote": "Trích dẫn", "codeblock": "Khối mã", "list": "Danh sách", "ordered": "Danh sách số",
	"left": "Căn trái", "center": "Căn giữa", "right": "Căn phải", "link": "Liên kết", "rule": "Đường kẻ ngang", "redo": "Làm lại",
}

// editorButtonWords is a button of editorWireV2 holding its id as its words.
var editorButtonWords = regexp.MustCompile(`(<button [^>]*id="([\w-]+)"[^>]*>)([\w-]+)</button>`)

// editorWireIcons is editorWireV2 holding value, its toolbar's buttons holding an icon each as the engine
// writes <icon value="…" alt="…" /> (<i class="icon-…" role="img" aria-label="…">), the dropdown's
// trigger too (its alt on its icon, as the canonical markup writes it); undo and the popover's buttons
// keep their words.
func editorWireIcons(value string) string {
	c := richtext.Clean(value)
	wire := editorWireV2(value, startScope(c.Empty(), c.Words, c.Characters), "")
	wire = strings.Replace(wire, ` aria-label="Tiêu đề nhỏ hơn"`, "", 1)
	wire = strings.Replace(wire, `>v</button>`, `><i class="icon-chevron-down" role="img" aria-label="Tiêu đề nhỏ hơn"></i></button>`, 1)
	return editorButtonWords.ReplaceAllStringFunc(wire, func(button string) string {
		m := editorButtonWords.FindStringSubmatch(button)
		if name, ok := editorIconNames[m[2]]; ok && m[3] == m[2] {
			return m[1] + `<i class="icon-` + m[2] + `" role="img" aria-label="` + name + `"></i></button>`
		}
		return button
	})
}

// editorPlaceStyle is what the place proofs need of a page's CSS beyond editorStyle, copied from where it
// is written: reboot.css's popover and anchor, the place classes the engine writes for place="…"
// (ui/stylesheet/values.go), richtext's centred paragraph, the tooltip's look (ui/stylesheet/editor.go,
// whose test holds its rule); and so that where things stand is known, the page's ink and paper,
// a toolbar of 32px buttons in rows, words of 16px on lines of 24px, the writing area 600px wide, a
// popover 240px wide.
const editorPlaceStyle = `<style>
:root{--ink:#18181b;--paper:#fff;--line:#e4e4e7}
body{margin:8px;font:16px/24px sans-serif}
#host{inline-size:600px}
#toolbar,.editor-group{display:flex;flex-wrap:wrap;gap:4px}
#toolbar button{inline-size:32px;block-size:32px;padding:0;overflow:hidden}
.editor-area p{margin:0}
.align-center{text-align:center}
.place-anchor{position:relative}
.popover{position:absolute;inset-block:100% auto;inset-inline:0 auto;margin-block-start:.5rem;z-index:50;min-inline-size:12rem;background:#fff}
.editor-popover{inline-size:240px;box-sizing:border-box;padding:8px}
.place-below-left{position:absolute;inset-block:100% auto;inset-inline:0 auto;margin:.5rem 0 0;translate:none;z-index:50}
.place-below{position:absolute;inset:100% auto auto 50%;margin:.5rem 0 0;translate:-50% 0;z-index:50}
.place-below-right{position:absolute;inset-block:100% auto;inset-inline:auto 0;margin:.5rem 0 0;translate:none;z-index:50}
.place-above-left{position:absolute;inset-block:auto 100%;inset-inline:0 auto;margin:0 0 .5rem;translate:none;z-index:50}
.place-above{position:absolute;inset:auto auto 100% 50%;margin:0 0 .5rem;translate:-50% 0;z-index:50}
.place-above-right{position:absolute;inset-block:auto 100%;inset-inline:auto 0;margin:0 0 .5rem;translate:none;z-index:50}
` + editorTipRules + `</style>`

// editorTipRules is the tooltip's rule as ui/stylesheet/editor.go writes it (TestEditorTipLook holds it
// there).
const editorTipRules = `.editor-tip{position:absolute;z-index:60;padding:.25rem .5rem;border:1px solid transparent;border-radius:.375rem;background:var(--ink);color:var(--paper);font-size:.75rem;line-height:1.5;white-space:nowrap;pointer-events:none}
`

// editorPlaceScript is the hands and eyes the place proofs add. The keyboard's focus is said by the
// proof: a background headless page has no focus, so :focus-visible (and :focus) never matches there,
// and matches(":focus-visible") answers window.keyboard for the focused element; a touch screen is said
// by window.coarse, through matchMedia("(pointer: coarse)"). hover() is the pointer over an element at a
// point out of the page (the tooltip reads where the pointer is only for the pointer on it); pointAt()
// is the pointer moved to a point, over what the browser finds there (the tooltip takes no pointer:
// what it covers); leave() is the pointer out of the window from an element.
const editorPlaceScript = `
function tip() { return document.querySelector(".editor-tip"); }
function shows() { var t = tip(); return t && !t.hidden ? t.textContent : ""; }
function hover(el, kind) { el.dispatchEvent(new PointerEvent("pointerover", { bubbles: true, pointerType: kind || "mouse", clientX: -100, clientY: -100 })); }
function pointAt(x, y) {
  var el = document.elementFromPoint(x, y), init = { bubbles: true, pointerType: "mouse", clientX: x, clientY: y };
  el.dispatchEvent(new PointerEvent("pointerover", init));
  el.dispatchEvent(new PointerEvent("pointermove", init));
  return el;
}
function leave(el) { el.dispatchEvent(new PointerEvent("pointerout", { bubbles: true, pointerType: "mouse", relatedTarget: null, clientX: -100, clientY: -100 })); }
function hits(x, y) { var el = document.elementFromPoint(x, y); return el && (el.closest("button") || el); }
function freeIn(r) { for (var y = r.top + 2; y < r.bottom - 1; y += 3) for (var x = r.left + 2; x < r.right - 1; x += 4) if (hits(x, y).tagName !== "BUTTON") return [x, y]; return null; }
function icon(id) { return $(id).querySelector("i") || $(id); }
function box(el) { return el.getBoundingClientRect(); }
function near(got, want, what) { assert(Math.abs(got - want) <= 1, what + ": " + got + " for " + want); }
function wordBox(word) { var at = find(word), r = document.createRange(); r.setStart(at[0], at[1]); r.setEnd(at[0], at[1] + word.length); var all = r.getClientRects(); return all[all.length - 1]; }
window.keyboard = false;
window.coarse = false;
var matching = Element.prototype.matches;
Element.prototype.matches = function (selector) { return selector === ":focus-visible" ? window.keyboard && document.activeElement === this : matching.call(this, selector); };
var media = window.matchMedia;
window.matchMedia = function (query) { return query === "(pointer: coarse)" ? { matches: window.coarse, media: query, addEventListener: function () {}, removeEventListener: function () {} } : media.call(window, query); };
`

// editorPlacePart is the tag of the part that places the tooltips and the popover
// (parts/editor-place.js), which the editor itself loads at the first focus in it or hover on its
// toolbar (TestBrowserEditorLoadsItsPlacePart).
func editorPlacePart() string {
	return `<script src="` + PartAsset("editor-place").Path + `"></script>`
}

// runEditorPlaceProof is runEditorProof with the v2 hands, the place proofs' style and hands, the parts
// loaded with the page, and the 20 s of virtual time of runEditorPartProof: a tooltip's proof waits out
// its 500 ms many times.
func runEditorPlaceProof(t *testing.T, label, body string) {
	t.Helper()
	body = strings.Replace(body, "<script>", "<script>"+editorPlaceScript, 1)
	runEditorPartProof(t, label, false, editorPlaceStyle+editorPastePart()+editorPlacePart()+body)
}

// The part is a file of its own, by its exact current name, as the paste part is
// (TestEditorPastePartIsDeliveredByName): the editor's source names it without its hash, the file a page
// gets with it; no page links it; nothing else is it.
// CONTROL (run 21/10/2026, a copy of v2.0.0.js through -overlay naming the part in single quotes, which
// withParts does not rewrite): "the editor's source does not name its place part" failed.
func TestEditorPlacePartIsDeliveredByName(t *testing.T) {
	part := PartAsset("editor-place")
	if part == nil || part.Role != "part" || !regexp.MustCompile(`^/_kitwork/parts/editor-place\.[0-9a-f]{16}\.js$`).MatchString(part.Path) {
		t.Fatalf("the place part is not a part by its name with its hash: %+v", part)
	}
	if !strings.Contains(ComponentSource("editor", ""), `"/_kitwork/parts/editor-place.js"`) {
		t.Fatalf("the editor's source does not name its place part")
	}
	editor := componentAsset("editor", nil)
	if body := string(editor.Body); !strings.Contains(body, `"`+part.Path+`"`) || strings.Contains(body, `"/_kitwork/parts/editor-place.js"`) {
		t.Fatalf("the editor a page gets names the part with its hash (%s): %v", part.Path, strings.Contains(body, part.Path))
	}
	page := Deliver(`<html><head></head><body><div data-kitwork-component="editor"></div></body></html>`, nil, Options{})
	if !strings.Contains(page, editor.Path) || strings.Contains(page, "/parts/") {
		t.Fatalf("a page links the editor and never its parts:\n%s", page)
	}
}

// A tooltip on each button of a toolbar that shows no words: after 500 ms of a mouse's hover or of the
// keyboard's focus (one that follows a key), under the button, centred on it, its name and its keys as Windows and Linux write
// them, one element per editor (role="tooltip", an id, in the host), no button described by it; at once
// for another button while one shows; the pointer may go onto it, through the gap (hoverable), the foot
// of its button still the button's (it takes no pointer); Escape hides it and is its alone, the toolbar's Escape back to the area the next one; a press hides it, and
// a key that is no modifier; the same button shows it again only once the hover or the focus has left;
// none for a press's focus, a touch, a touch screen, a button with words, a button in the dropdown's
// popover, while the dropdown or the link's popover is open; above the button when the viewport has no
// room under it, inside the viewport at its edges.
// CONTROL (run 21/10/2026, again after the review's fixes, copies of parts/editor-place.js through
// -overlay, in the scratchpad's editor-tips/fix/controls): with focusIn() not reading the press before
// a focus, "a press's focus shows none" failed; without stopImmediatePropagation on Escape, "Escape
// hides it, heard by nothing else" failed; with no wait (0 ms in place of 500), "nothing yet at 300 ms"
// failed; with open() not reading busy(), "none while the dropdown is open" failed; with the part not
// reading the pointer on the tooltip (onTip() false), "the pointer in the gap under the button keeps it"
// failed.
func TestBrowserEditorTooltips(t *testing.T) {
	runEditorPlaceProof(t, "editor tooltips", editorWireIcons("<p>một hai ba bốn năm</p>")+editorProof(`
  hover(icon("h2"));
  await wait(300);
  assert(!shows(), "nothing yet at 300 ms, got " + JSON.stringify(shows()));
  await wait(300);
  assert(shows() === "Tiêu đề mục", "a hover shows the button's name after 500 ms, got " + JSON.stringify(shows()));
  var t = box(tip()), b = box($("h2"));
  near(t.top, b.bottom + 4, "under the button");
  near((t.left + t.right) / 2, (b.left + b.right) / 2, "centred on it");
  assert(tip().getAttribute("role") === "tooltip" && tip().id === "area-tip" && tip().parentNode === $("host"), "one element, role tooltip, an id, in the host");
  assert(!document.querySelector("[aria-describedby]"), "no button is described by it");
  assert(hits((b.left + b.right) / 2, b.bottom - 0.5) === $("h2"), "the foot of the button is the button's, got " + hits((b.left + b.right) / 2, b.bottom - 0.5));
  pointAt((b.left + b.right) / 2, b.bottom + 2);
  assert(shows() === "Tiêu đề mục", "the pointer in the gap under the button keeps it, got " + JSON.stringify(shows()));
  var free = freeIn(t);
  assert(free && pointAt(free[0], free[1]) !== tip(), "a point of the tooltip over no button, which the pointer finds under it");
  await wait(600);
  assert(shows() === "Tiêu đề mục", "the pointer on it keeps it, got " + JSON.stringify(shows()));
  pointAt(free[0], t.bottom + 30);
  assert(!shows(), "the pointer off it, over what was under it: gone");
  hover(icon("h2"));
  await wait(20);
  assert(shows() === "Tiêu đề mục", "back on its button within 300 ms: at once, got " + JSON.stringify(shows()));

  hover(icon("bold"));
  await wait(20);
  assert(shows() === "Đậm · Ctrl+B", "another button while one shows: at once, with its keys, got " + JSON.stringify(shows()));
  assert(box(tip()).left >= 3.5, "inside the viewport at its left edge, got " + box(tip()).left);
  hover(icon("redo"));
  await wait(20);
  assert(shows() === "Làm lại · Ctrl+Shift+Z", "keys of three, got " + JSON.stringify(shows()));
  hover($("toolbar"));
  assert(!shows(), "the pointer off the buttons hides it");
  hover($("undo"));
  await wait(600);
  assert(!shows(), "a button with words has none, got " + JSON.stringify(shows()));
  hover(icon("more-button"));
  await wait(600);
  assert(shows() === "Tiêu đề nhỏ hơn", "the dropdown's trigger has one, got " + JSON.stringify(shows()));

  hover(icon("bold"));
  await wait(20);
  $("bold").dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, cancelable: true }));
  assert(!shows(), "a press hides it");
  hover(icon("bold"));
  await wait(600);
  assert(!shows(), "the same button shows it again only once the hover has left");
  hover($("toolbar"));
  hover(icon("bold"));
  await wait(600);
  assert(shows() === "Đậm · Ctrl+B", "and does once it has");
  var heard = 0;
  document.addEventListener("keydown", function (event) { if (event.key === "Escape") heard++; });
  var escape = key(document.body, "Escape");
  assert(escape.defaultPrevented && !shows() && heard === 0, "Escape hides it, heard by nothing else, heard " + heard);
  hover(icon("bold"));
  await wait(600);
  assert(!shows(), "the hover that stayed does not show it again");
  assert(!key(document.body, "Escape").defaultPrevented && heard === 1, "with none shown Escape is the page's");
  hover($("toolbar"));
  hover(icon("italic"));
  await wait(600);
  key(area, "Shift");
  assert(shows() === "Nghiêng · Ctrl+I", "a modifier keeps it, got " + JSON.stringify(shows()));
  selectWord("hai");
  key(area, "i", { ctrlKey: true });
  assert(!shows() && field.value === "<p>một <em>hai</em> ba bốn năm</p>", "a key that runs a command hides it, got " + JSON.stringify(shows()) + " " + field.value);

  hover($("toolbar"));
  await wait(400); // past the 300 ms after the last tooltip hid
  window.keyboard = true;
  focusIn($("bold"));
  await wait(300);
  assert(!shows(), "the keyboard's focus: nothing yet at 300 ms");
  await wait(300);
  assert(shows() === "Đậm · Ctrl+B", "the keyboard's focus shows it after 500 ms, got " + JSON.stringify(shows()));
  hover(area);
  assert(shows() === "Đậm · Ctrl+B", "the pointer going elsewhere keeps what the focus showed");
  escape = key($("bold"), "Escape");
  assert(escape.defaultPrevented && !shows() && active() === "bold", "Escape hides it, the focus where it was, got " + active());
  key($("bold"), "Escape");
  assert(active() === "area", "the next Escape is the toolbar's: back to the area, got " + active());
  window.keyboard = false;
  $("more-button").dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, cancelable: true }));
  focusIn($("more-button"));
  await wait(600);
  assert(!shows(), "a press's focus shows none, got " + JSON.stringify(shows()));

  hover(icon("italic"), "touch");
  await wait(600);
  assert(!shows(), "a touch shows none");
  window.coarse = true;
  hover(icon("underline"));
  await wait(600);
  assert(!shows(), "a touch screen shows none");
  window.coarse = false;
  hover($("toolbar"));
  openMore();
  assert(dropdownOpen(), "the dropdown opens");
  hover(icon("h3"));
  await wait(600);
  assert(!shows(), "none while the dropdown is open");
  hover(icon("h4"));
  await wait(600);
  assert(!shows(), "none for a button in its popover");
  openMore();
  hover($("toolbar"));
  selectWord("ba");
  press($("link"));
  assert(!$("pop").hidden, "the link's popover opens");
  hover(icon("code"));
  await wait(600);
  assert(!shows(), "none while the link's popover is open");
  press($("cancel"));

  // the toolbar at the foot of the viewport: the tooltip above its button; at the viewport's right edge
  hover($("toolbar"));
  $("host").style.marginTop = (document.documentElement.clientHeight - 10 - box($("toolbar")).bottom) + "px";
  hover(icon("rule"));
  await wait(600);
  t = box(tip()); b = box($("rule"));
  assert(b.bottom > document.documentElement.clientHeight - 20, "the button stands at the foot of the viewport, got " + b.bottom);
  near(t.bottom, b.top - 4, "no room under the button: above it");
  pointAt((b.left + b.right) / 2, b.top - 2);
  assert(shows() === "Đường kẻ ngang", "the gap above keeps it then, got " + JSON.stringify(shows()));
  hover($("toolbar"));
  $("host").style.marginTop = "0";
  $("host").style.inlineSize = "auto";
  $("toolbar").style.justifyContent = "flex-end";
  hover(icon("redo"));
  await wait(600);
  t = box(tip());
  assert(shows() && t.right <= document.documentElement.clientWidth - 3.5 && t.left < box($("redo")).left, "at the viewport's right edge it stays inside, got " + t.left + " to " + t.right);
  assert(document.querySelectorAll(".editor-tip").length === 1, "one tooltip element all along");
`))
}

// On an Apple device the keys are the platform's: Command (the editor wrote Meta+ in aria-keyshortcuts)
// and Shift as ⌘ and ⇧, the key after them.
// CONTROL (run 21/10/2026, a copy of parts/editor-place.js through -overlay writing the keys as Windows
// writes them everywhere): "⌘ on an Apple device" failed (Đậm · Meta+B).
func TestBrowserEditorTooltipsOnAnAppleDevice(t *testing.T) {
	runEditorPlaceProof(t, "editor tooltips apple", editorWireIcons("<p>một hai</p>")+
		`<script>Object.defineProperty(navigator, "platform", { get: function () { return "MacIntel"; } });</script><script src="/kit.js"></script>`+editorProof(`
  hover(icon("bold"));
  await wait(600);
  assert(shows() === "Đậm · ⌘B", "⌘ on an Apple device, got " + JSON.stringify(shows()));
  hover(icon("redo"));
  await wait(20);
  assert(shows() === "Làm lại · ⇧⌘Z", "and ⇧, got " + JSON.stringify(shows()));
  hover(icon("rule"));
  await wait(20);
  assert(shows() === "Đường kẻ ngang", "a button with no keys says its name alone, got " + JSON.stringify(shows()));
`))
}

// The link's popover stands right under the words it links, as its place reads from them: below-left
// (or no place) its left edge at theirs, below centred, below-right its right edge at theirs, 8px under
// their last line; above-left, above, above-right 8px over them, held by its bottom edge (a refused
// address shows the error: it grows away from the words); a selection of several lines under its last
// one, from that line's start; a caret in a link under the link. Without room on its side in the
// viewport it goes to the other; past the editor's right edge, or the viewport's, it moves left; on a
// page written right to left, left and right mirror. It follows a resize and the area's own scroll; the
// page's scroll moves it with the words. The address takes the focus as it opens; as it shuts its
// style goes, the focus and the selection back in the area.
// CONTROL (run 21/10/2026, copies of parts/editor-place.js through -overlay): with place() setting no
// style, "no place: its top 8px under the words" failed (it stood under the editor); without the resize
// listener, "a resize: under the words again" failed; without the RTL mirror, "right to left, below-left:
// at their start, on the right" failed; with the above held by its top, "a refused address: it grows
// away from the words" failed.
func TestBrowserEditorPopoverUnderTheWords(t *testing.T) {
	const value = `<p class="align-center">giữa</p><p>Họp nhóm lúc chín giờ sáng nay ở phòng lớn tầng hai, mang theo máy tính và bản ghi của tuần trước để cả nhóm đọc lại cùng nhau trước khi bàn việc mới cho tháng sau</p><p>Hai <a href="/x">liên kết dài</a> ở đây</p>`
	runEditorPlaceProof(t, "editor popover place", `<style>body{padding-block:200px 2000px}</style>`+editorWireIcons(value)+editorProof(`
  var pop = $("pop"), p, w, view = document.documentElement;
  function opened(place, choose) {
    pop.className = "popover editor-popover" + (place ? " place-" + place : "");
    choose();
    press($("link"));
    assert(!pop.hidden && active() === "address", (place || "no place") + ": open, the address focused, got " + active());
    return box(pop);
  }
  function shut(word) {
    press($("cancel"));
    assert(pop.hidden && !pop.hasAttribute("style"), "shut, its style gone, got " + pop.getAttribute("style"));
    assert(active() === "area" && getSelection().toString() === word, "the focus and the selection back, got " + active() + " " + JSON.stringify(getSelection().toString()));
  }
  var cases = [["", "below", "left"], ["below-left", "below", "left"], ["below", "below", "centre"], ["below-right", "below", "right"],
    ["above-left", "above", "left"], ["above", "above", "centre"], ["above-right", "above", "right"]];
  for (var i = 0; i < cases.length; i++) {
    var c = cases[i], name = c[0] || "no place";
    p = opened(c[0], function () { selectWord("giữa"); });
    w = wordBox("giữa");
    if (c[1] === "below") near(p.top, w.bottom + 8, name + ": its top 8px under the words");
    else near(p.bottom, w.top - 8, name + ": its bottom 8px over the words");
    if (c[2] === "left") near(p.left, w.left, name + ": its left edge at theirs");
    else if (c[2] === "right") near(p.right, w.right, name + ": its right edge at theirs");
    else near((p.left + p.right) / 2, (w.left + w.right) / 2, name + ": centred on them");
    shut("giữa");
  }

  // a refused address shows the error: over the words it grows upward
  p = opened("above-left", function () { selectWord("giữa"); });
  $("address").value = "javascript:alert(1)";
  key($("address"), "Enter");
  w = wordBox("giữa");
  assert(!$("error").hidden && box(pop).height > p.height, "the error shows, the popover taller");
  near(box(pop).bottom, w.top - 8, "a refused address: it grows away from the words");
  shut("giữa");

  // several lines: under the last, from its start; a caret in a link: under the link
  var a = find("chín"), z = find("sau");
  p = opened("below-left", function () { select(a[0], a[1], z[0], z[1] + 3); });
  w = wordBox("sau");
  near(p.top, w.bottom + 8, "several lines: under the last");
  near(p.left, box(area).left, "from that line's start");
  assert(w.top > wordBox("chín").top + 30, "the selection is several lines");
  press($("cancel"));
  p = opened("below-left", function () { caretAt("liên", true); });
  var link = area.querySelector("a").getClientRects()[0];
  near(p.top, link.bottom + 8, "a caret in a link: under the link");
  near(p.left, link.left, "at its start");
  press($("cancel"));

  // no room in the viewport on its side: the other
  w = wordBox("giữa");
  document.body.style.paddingTop = (200 + view.clientHeight - 20 - w.bottom) + "px";
  p = opened("below-left", function () { selectWord("giữa"); });
  w = wordBox("giữa");
  near(p.bottom, w.top - 8, "no room under the words: over them");
  shut("giữa");
  document.body.style.paddingTop = "";
  scrollTo(0, scrollY + wordBox("giữa").top - 20);
  p = opened("above-left", function () { selectWord("giữa"); });
  w = wordBox("giữa");
  near(p.top, w.bottom + 8, "no room over the words: under them");
  shut("giữa");
  scrollTo(0, 0);

  // past the editor's right edge it moves left, past the viewport's too: the last word on the first line
  // of the second paragraph, [its offset, its length] in the paragraph's text
  var text = find("Họp")[0];
  function lastOnTheFirstLine() {
    var line = [], first;
    text.data.split(" ").reduce(function (at, word) {
      var r = document.createRange(); r.setStart(text, at); r.setEnd(text, at + word.length);
      var rect = r.getClientRects()[0]; first = first || rect;
      if (rect && Math.abs(rect.top - first.top) < 2) line.push([at, word.replace(/,$/, "").length]);
      return at + word.length + 1;
    }, 0);
    var end = line[line.length - 1], r = document.createRange();
    r.setStart(text, end[0]); r.setEnd(text, end[0] + end[1]);
    return { at: end[0], to: end[0] + end[1], box: r.getClientRects()[0] };
  }
  var end = lastOnTheFirstLine(), host = box($("host"));
  p = opened("below-left", function () { select(text, end.at, text, end.to); });
  assert(end.box.left + p.width > host.right && end.box.right < view.clientWidth, "the word stands where the popover would pass the editor's edge");
  assert(p.right <= host.right + 0.5 && p.left < end.box.left, "past the editor's right edge it moves left, got " + p.left + " to " + p.right + " for " + end.box.left);
  near(p.top, end.box.bottom + 8, "still under the words");
  press($("cancel"));
  $("host").style.inlineSize = "1400px";
  end = lastOnTheFirstLine();
  p = opened("below-left", function () { select(text, end.at, text, end.to); });
  assert(end.box.left + p.width > view.clientWidth && box($("host")).right > view.clientWidth + p.width, "the word stands where the popover would pass the viewport's edge, not the editor's");
  assert(p.right <= view.clientWidth + 0.5 && p.right > view.clientWidth - 1.5, "past the viewport's right edge it moves left, got " + p.left + " to " + p.right + " in " + view.clientWidth);
  press($("cancel"));
  $("host").style.inlineSize = "";

  // right to left: left and right mirror
  $("host").dir = "rtl";
  p = opened("below-left", function () { selectWord("giữa"); });
  w = wordBox("giữa");
  near(p.right, w.right, "right to left, below-left: at their start, on the right");
  shut("giữa");
  p = opened("below-right", function () { selectWord("giữa"); });
  w = wordBox("giữa");
  near(p.left, w.left, "right to left, below-right: at their end, on the left");
  shut("giữa");
  $("host").dir = "";

  // a resize, the area's scroll, the page's scroll
  p = opened("below-left", function () { selectWord("giữa"); });
  $("host").style.inlineSize = "500px";
  dispatchEvent(new Event("resize"));
  w = wordBox("giữa");
  near(box(pop).left, w.left, "a resize: under the words again");
  near(box(pop).top, w.bottom + 8, "a resize: 8px under them");
  shut("giữa");
  $("host").style.inlineSize = "";
  area.style.maxBlockSize = "48px";
  area.style.overflow = "auto";
  p = opened("below-left", function () { selectWord("Họp"); });
  area.scrollTop = 24;
  area.dispatchEvent(new Event("scroll"));
  w = wordBox("Họp");
  near(box(pop).top, w.bottom + 8, "the area's scroll: under the words again");
  near(box(pop).top, p.top - 24, "moved with them");
  shut("Họp");
  area.style.maxBlockSize = area.style.overflow = "";
  area.scrollTop = 0;
  p = opened("below-left", function () { selectWord("giữa"); });
  scrollTo(0, 50);
  w = wordBox("giữa");
  near(box(pop).top, w.bottom + 8, "the page's scroll moves it with the words");
  key($("address"), "Escape");
  assert(pop.hidden && !pop.hasAttribute("style") && active() === "area" && getSelection().toString() === "giữa", "Escape shuts it as before, its style gone");
`))
}

// The editor loads its place part at the first hover (not a touch's) on a toolbar or focus in it, never
// with the page; that first hover's tooltip shows once the part is here; before the part has heard a key
// or a press, the browser's :focus-visible says which focus shows a tooltip; once loaded it places the
// popover; a part that cannot load is asked for once, and the popover then stands where its place puts
// it, by the host.
// CONTROL (run 21/10/2026, copies of v2.0.0.js and parts/editor-place.js through -overlay): with the
// editor loading the part at ready, "nothing of the part with the page" failed; with no pointerover
// listener on the toolbars, "a hover on a toolbar loads it" failed; with the part asked for at each
// hover, "asked for once" failed; with the part not reading :focus-visible while it has heard nothing,
// "a focus the browser does not show, before any key or press: none" failed.
func TestBrowserEditorLoadsItsPlacePart(t *testing.T) {
	part := PartAsset("editor-place").Path
	const fetched = `function placeTags() { return document.querySelectorAll('script[src*="/_kitwork/parts/editor-place."]'); }
async function until(test) { for (var i = 0; i < 150 && !test(); i++) await wait(20); return test(); }
` + editorPlaceScript
	runEditorPartProof(t, "editor place part", false, editorPlaceStyle+editorWireIcons("<p>hello world</p>")+editorProof(fetched+`
  assert(!placeTags().length && !window[Symbol.for("kitwork:editor-place")], "nothing of the part with the page");
  hover(icon("italic"), "touch");
  await wait(100);
  assert(!placeTags().length, "a touch loads nothing");
  hover(icon("italic"));
  assert(await until(function () { return window[Symbol.for("kitwork:editor-place")]; }), "a hover on a toolbar loads it");
  assert(placeTags().length === 1 && placeTags()[0].getAttribute("src") === `+jsString(part)+`, "by its name with its hash, got " + (placeTags()[0] && placeTags()[0].getAttribute("src")));
  assert(await until(function () { return shows(); }) && shows() === "Nghiêng · Ctrl+I", "that hover's tooltip shows once it is here, got " + JSON.stringify(shows()));
  selectWord("world");
  press($("link"));
  var w = wordBox("world"), p = box($("pop"));
  near(p.top, w.bottom + 8, "it places the popover");
  press($("cancel"));
`))
	runEditorPartProof(t, "editor place part, a focus first", false, editorPlaceStyle+editorWireIcons("<p>hello world</p>")+editorProof(fetched+`
  assert(!placeTags().length, "nothing of the part with the page");
  focusIn(area);
  assert(await until(function () { return window[Symbol.for("kitwork:editor-place")]; }), "a focus in the editor loads it");
  // no key nor press heard yet: the browser's :focus-visible says which focus is the keyboard's
  focusIn($("more-button"));
  await wait(600);
  assert(!shows(), "a focus the browser does not show, before any key or press: none, got " + JSON.stringify(shows()));
  window.keyboard = true;
  focusIn($("bold"));
  await wait(600);
  assert(shows() === "Đậm · Ctrl+B", "a focus it shows: the tooltip, got " + JSON.stringify(shows()));
  window.keyboard = false;
  selectWord("hello");
  press($("link"));
  var w = wordBox("hello"), p = box($("pop"));
  near(p.top, w.bottom + 8, "the popover under the words");
  near(p.left, w.left, "at their left");
`))
	runEditorPartProof(t, "editor place part refused", true, editorPlaceStyle+editorWireIcons("<p>hello world</p>")+editorProof(fetched+`
  hover(icon("bold"));
  await wait(400);
  hover($("toolbar"));
  hover(icon("italic"));
  await wait(700);
  assert(placeTags().length === 1 && !shows(), "a part that cannot load: asked for once, no tooltip, got " + placeTags().length);
  selectWord("world");
  press($("link"));
  assert(!$("pop").hidden && !$("pop").hasAttribute("style") && active() === "address", "the popover opens where its place puts it");
`))
}

// The review of 21/10/2026, the tooltips: the tooltip takes no pointer, so where it covers a button
// (the next row of a toolbar that wraps), the button is the pointer's: found there, hovered (its own
// tooltip, at once) and pressed (its command runs); the pointer out of the window ends the hover, before
// the tooltip shows or while it does.
// CONTROL (run 21/10/2026, copies through -overlay, in the scratchpad's editor-tips/fix/controls): with
// the tooltip taking the pointer again (pointer-events:none gone from editorTipRules), "the button under
// the tooltip is the pointer's" failed (the tooltip found there); with pointed() not reading a pointerout,
// "the pointer out of the window before 500 ms: none" failed.
func TestBrowserEditorTooltipsTakeNoPointer(t *testing.T) {
	runEditorPlaceProof(t, "editor tooltips take no pointer", editorWireIcons("<p>một hai ba bốn năm</p>")+editorProof(`
  hover(icon("bold"));
  await wait(600);
  assert(shows() === "Đậm · Ctrl+B", "Bold's tooltip, got " + JSON.stringify(shows()));
  var t = box(tip()), b = box($("bold")), under = null;
  Array.prototype.forEach.call(document.querySelectorAll("#toolbar button"), function (button) {
    var r = box(button), x = (r.left + r.right) / 2;
    if (!under && !button.closest(".popover") && button.getAttribute("aria-disabled") !== "true" && r.top > b.bottom && x > t.left && x < t.right && r.top < t.bottom - 4) under = { button: button, x: x, y: (r.top + Math.min(r.bottom, t.bottom)) / 2 };
  });
  assert(under, "the toolbar wraps: a button of its next row under Bold's tooltip");
  assert(hits(under.x, under.y) === under.button, "the button under the tooltip is the pointer's, got " + hits(under.x, under.y));
  pointAt(under.x, under.y);
  await wait(20);
  assert(shows() && shows() !== "Đậm · Ctrl+B", "the pointer onto it: its tooltip, at once, got " + JSON.stringify(shows()));
  caretAt("hai", true);
  var before = area.innerHTML;
  press(hits(under.x, under.y));
  assert(area.innerHTML !== before, "pressed there, its command runs, got " + under.button.id + " " + area.innerHTML);

  hover($("toolbar"));
  await wait(400);
  hover(icon("italic"));
  await wait(100);
  leave(icon("italic"));
  await wait(600);
  assert(!shows(), "the pointer out of the window before 500 ms: none, got " + JSON.stringify(shows()));
  hover(icon("italic"));
  await wait(600);
  assert(shows() === "Nghiêng · Ctrl+I", "back on it: its tooltip, got " + JSON.stringify(shows()));
  leave(icon("italic"));
  assert(!shows(), "out of the window while it shows: gone");
`))
}

// The review of 21/10/2026, the tooltips moving: along the toolbar a tooltip that hid less than 300 ms
// ago gives way at once (the 4px between two buttons of a group, the toolbar and the next group between
// two groups, an arrow from a button to the next); later, the wait again. The one of the pointer and the
// focus that moved last says which button: the pointer gone from a button, the focused one's tooltip
// shows again.
// CONTROL (run 21/10/2026, copies of parts/editor-place.js through -overlay, in the scratchpad's
// editor-tips/fix/controls): with no 300 ms after a tooltip hid (warm never set), "the next button within
// 300 ms: at once" failed; with the focus not taking the tooltip back (update() reading the pointer
// alone), "the pointer gone: the focused button's again" failed.
func TestBrowserEditorTooltipsMoveAtOnce(t *testing.T) {
	runEditorPlaceProof(t, "editor tooltips move at once", editorWireIcons("<p>một hai ba bốn năm</p>")+editorProof(`
  hover(icon("bold"));
  await wait(600);
  assert(shows() === "Đậm · Ctrl+B", "Bold's tooltip, got " + JSON.stringify(shows()));
  hover(document.querySelector(".editor-group"));
  assert(!shows(), "between two buttons: none");
  await wait(60);
  hover(icon("italic"));
  await wait(20);
  assert(shows() === "Nghiêng · Ctrl+I", "the next button within 300 ms: at once, got " + JSON.stringify(shows()));
  hover($("toolbar"));
  hover(document.querySelectorAll(".editor-group")[1]);
  await wait(100);
  hover(icon("h2"));
  await wait(20);
  assert(shows() === "Tiêu đề mục", "across two groups: at once, got " + JSON.stringify(shows()));
  hover($("toolbar"));
  await wait(400);
  hover(icon("h3"));
  await wait(20);
  assert(!shows(), "300 ms after it hid: the wait again, got " + JSON.stringify(shows()));
  await wait(600);
  assert(shows() === "Tiêu đề nhỏ", "then its tooltip, got " + JSON.stringify(shows()));

  hover(area);
  await wait(400);
  window.keyboard = true;
  focusIn($("bold"));
  await wait(600);
  assert(shows() === "Đậm · Ctrl+B", "the keyboard's focus: its tooltip, got " + JSON.stringify(shows()));
  key($("bold"), "ArrowRight");
  assert(active() === "italic", "the arrow: the focus on Italic, got " + active());
  focusIn($("italic"));
  await wait(20);
  assert(shows() === "Nghiêng · Ctrl+I", "an arrow: the next button's at once, got " + JSON.stringify(shows()));
  hover(icon("underline"));
  await wait(20);
  assert(shows() === "Gạch chân · Ctrl+U", "the pointer over another button: its, at once, got " + JSON.stringify(shows()));
  hover(area);
  await wait(20);
  assert(shows() === "Nghiêng · Ctrl+I" && active() === "italic", "the pointer gone: the focused button's again, got " + JSON.stringify(shows()));
  window.keyboard = false;
`))
}

// The review of 21/10/2026, the part slow to come: what the pointer and the focus did while it loaded is
// taken in the order it came, so what shows is what they are on once it is here: the pointer that crossed
// Bold and rests on Italic, Italic's; the pointer that crossed Bold into the area, none; Alt+F10 right
// after a click in the area (Chrome's :focus-visible does not match that focus), Bold's.
// CONTROL (run 21/10/2026, copies of v2.0.0.js through -overlay, in the scratchpad's editor-tips/fix/
// controls): with the editor keeping the first event alone (mine.heard pushed once), "the button the
// pointer rests on" failed (Bold's); with no pointerout kept (its listener on the toolbars gone),
// "crossed the toolbar into the area: none" failed (Bold's); with no press or key kept (mine.pressed
// never set), "a tooltip once the part is here" failed (none for Alt+F10).
func TestBrowserEditorPlacePartTakesWhatCameLast(t *testing.T) {
	const until = `async function until(test) { for (var i = 0; i < 150 && !test(); i++) await wait(20); return test(); }
function part() { return window[Symbol.for("kitwork:editor-place")]; }
` + editorPlaceScript
	runEditorPartProof(t, "editor place part, the pointer rests elsewhere", false, editorPlaceStyle+editorWireIcons("<p>hello world</p>")+editorProof(until+`
  hover(icon("bold"));
  $("bold").dispatchEvent(new PointerEvent("pointerout", { bubbles: true, pointerType: "mouse", relatedTarget: icon("italic") }));
  hover(icon("italic"));
  assert(!part(), "all of it before the part is here");
  assert(await until(shows), "a tooltip once the part is here");
  assert(shows() === "Nghiêng · Ctrl+I", "the button the pointer rests on, not the one it crossed, got " + JSON.stringify(shows()));
`))
	runEditorPartProof(t, "editor place part, the pointer into the area", false, editorPlaceStyle+editorWireIcons("<p>hello world</p>")+editorProof(until+`
  hover(icon("bold"));
  $("bold").dispatchEvent(new PointerEvent("pointerout", { bubbles: true, pointerType: "mouse", relatedTarget: area.firstChild }));
  assert(!part(), "all of it before the part is here");
  assert(await until(part), "the part loads");
  await wait(800);
  assert(!shows(), "the pointer crossed the toolbar into the area: none, got " + JSON.stringify(shows()));
`))
	runEditorPartProof(t, "editor place part, Alt+F10 at once", false, editorPlaceStyle+editorWireIcons("<p>hello world</p>")+editorProof(until+`
  area.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, pointerType: "mouse" }));
  focusIn(area);
  assert(key(area, "F10", { altKey: true }).defaultPrevented && active() === "bold", "Alt+F10: the focus on Bold, got " + active());
  $("bold").dispatchEvent(new FocusEvent("focusin", { bubbles: true }));
  assert(!part(), "all of it before the part is here");
  assert(await until(shows), "a tooltip once the part is here");
  assert(shows() === "Đậm · Ctrl+B", "Alt+F10 before the part came: Bold's, got " + JSON.stringify(shows()));
`))
}

// The review of 21/10/2026, the popover by its lines: over the words it stands over their first line
// (above-left from their start on it), never on a line of them, and so when it goes over them for want
// of room under them; the boxes are the words' text alone (a paragraph wholly in the selection has a box
// of its own, which is no line); words above the viewport still have it under them.
// CONTROL (run 21/10/2026, copies of parts/editor-place.js through -overlay, in the scratchpad's
// editor-tips/fix/controls): with the popover over the last line (high read from it), "above-left,
// several lines: over the first" failed; with the range's own boxes (getClientRects of the selection),
// "a block wholly in the selection is no line" failed; with no style for words above the viewport (low
// from 0), "words above the viewport: open, placed" failed (no style).
func TestBrowserEditorPopoverByItsLines(t *testing.T) {
	value := `<p>một hai ba</p><p>Họp nhóm lúc chín giờ sáng nay ở phòng lớn tầng hai, mang theo máy tính và bản ghi của tuần trước để cả nhóm đọc lại cùng nhau trước khi bàn việc mới cho tháng sau</p><p class="align-center">Hai bốn</p>`
	runEditorPlaceProof(t, "editor popover by its lines", `<style>body{padding-block:200px 2000px}</style>`+editorWireIcons(value)+editorProof(`
  var pop = $("pop"), p, first, last, view = document.documentElement;
  function opened(place, choose) {
    pop.className = "popover editor-popover" + (place ? " place-" + place : "");
    choose();
    press($("link"));
    assert(!pop.hidden && active() === "address", (place || "no place") + ": open, the address focused, got " + active());
    return box(pop);
  }
  var a = find("chín"), z = find("sau");
  function several() { select(a[0], a[1], z[0], z[1] + 3); }
  ["above-left", "above", "above-right"].forEach(function (place) {
    p = opened(place, several);
    first = wordBox("chín"); last = wordBox("sau");
    assert(last.top > first.top + 30, "the selection is several lines");
    near(p.bottom, first.top - 8, place + ", several lines: over the first");
    if (place === "above-left") near(p.left, first.left, place + ": from their start on it");
    press($("cancel"));
  });
  // no room under the last line: over the first
  last = wordBox("sau");
  document.body.style.paddingTop = (200 + view.clientHeight - 20 - last.bottom) + "px";
  p = opened("below-left", several);
  first = wordBox("chín");
  near(p.bottom, first.top - 8, "no room under them: over their first line");
  near(p.left, first.left, "from their start on it");
  press($("cancel"));
  document.body.style.paddingTop = "";

  // a paragraph wholly in the selection
  var h = find("hai ba"), e = find("Hai bốn");
  function across() { select(h[0], h[1], e[0], e[1] + 3); }
  p = opened("below-left", across);
  near(p.left, wordBox("Hai").left, "a block wholly in the selection is no line: at the last line's words");
  near(p.top, wordBox("Hai").bottom + 8, "under them");
  press($("cancel"));
  p = opened("above-left", across);
  near(p.bottom, wordBox("hai ba").top - 8, "over the first line, not over the block");
  near(p.left, wordBox("hai ba").left, "at its start");
  press($("cancel"));

  // words above the viewport
  selectWord("một");
  scrollTo(0, scrollY + wordBox("một").bottom + 50);
  assert(wordBox("một").bottom < 0, "the words above the viewport, got " + wordBox("một").bottom);
  press($("link"));
  assert(!pop.hidden && pop.hasAttribute("style"), "words above the viewport: open, placed");
  near(box(pop).top, wordBox("một").bottom + 8, "words above the viewport: under them still");
  near(box(pop).left, wordBox("một").left, "at their start");
  press($("cancel"));
`))
}

// The review of 21/10/2026, the popover in from the right edge: a popover as wide as its row (no width of
// its own, a width to the fraction of a pixel) moved left from the editor's right edge keeps its one row,
// its right edge at the edge of the box it is placed in (the editor's padding box: within its border).
// CONTROL (run 21/10/2026, copies of parts/editor-place.js through -overlay, in the scratchpad's
// editor-tips/fix/controls): with its width read as offsetWidth (a whole number), "no border: one row"
// failed; with the editor's border box in place of its padding box, "a border of 1px: one row" failed.
func TestBrowserEditorPopoverKeepsItsRow(t *testing.T) {
	const style = `<style>
#pop.editor-popover{inline-size:auto;min-inline-size:0;display:flex;flex-wrap:wrap;gap:4px;padding:8px}
#pop > input{flex:none;inline-size:200.1px;min-inline-size:0;padding:0;border:0;box-sizing:border-box}
#pop > button{flex:none;inline-size:60px;padding:0;box-sizing:border-box}
</style>`
	value := `<p>giữa</p><p>Họp nhóm lúc chín giờ sáng nay ở phòng lớn tầng hai, mang theo máy tính và bản ghi của tuần trước để cả nhóm đọc lại cùng nhau trước khi bàn việc mới cho tháng sau</p>`
	runEditorPlaceProof(t, "editor popover keeps its row", style+editorWireIcons(value)+editorProof(`
  var pop = $("pop"), text = find("Họp")[0];
  function opened(choose) {
    pop.className = "popover editor-popover place-below-left";
    choose();
    press($("link"));
    var p = box(pop);
    press($("cancel"));
    return p;
  }
  function lastOnTheFirstLine() {
    var line = [], first;
    text.data.split(" ").reduce(function (at, word) {
      var r = document.createRange(); r.setStart(text, at); r.setEnd(text, at + word.length);
      var rect = r.getClientRects()[0]; first = first || rect;
      if (rect && Math.abs(rect.top - first.top) < 2) line.push([at, word.replace(/,$/, "").length]);
      return at + word.length + 1;
    }, 0);
    return line[line.length - 1];
  }
  [["no border", ""], ["a border of 1px", "1px solid #888"]].forEach(function (c) {
    $("host").style.border = c[1];
    var mid = opened(function () { selectWord("giữa"); }), end = lastOnTheFirstLine(), at = document.createRange();
    at.setStart(text, end[0]);
    at.setEnd(text, end[0] + end[1]);
    var word = at.getClientRects()[0], edge = opened(function () { select(text, end[0], text, end[0] + end[1]); }), inner = box($("host")).right - (c[1] ? 1 : 0);
    assert(mid.width % 1 !== 0, c[0] + ": the popover's width has a fraction, got " + mid.width);
    assert(edge.left < word.left && edge.right <= inner + 0.01 && edge.right > inner - 1, c[0] + ": moved in from the right edge, to it, got " + edge.left + " to " + edge.right + " in " + inner);
    near(edge.height, mid.height, c[0] + ": one row");
  });
`))
}
