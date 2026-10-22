package work

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// editorPlaceLook is what the place proof reads, once drawn: the tooltip (its words, shown or not, its
// box), a box of the page by its selector, the popover's box and its style, the box of the first words
// of the writing area that read so, and the focus (a toolbar's button by its format or command).
const editorPlaceLook = `(function (button, words) {
  return new Promise(function (done) { requestAnimationFrame(function () { requestAnimationFrame(function () {
    var tip = document.querySelector(".editor-tip"), pop = document.querySelector(".editor-popover"), a = document.activeElement;
    var rect = function (el) { if (!el) return null; var r = el.getBoundingClientRect(); return { top: r.top, right: r.right, bottom: r.bottom, left: r.left }; };
    var word = null;
    if (words) {
      var walker = document.createTreeWalker(document.querySelector(".editor-area"), NodeFilter.SHOW_TEXT);
      while (walker.nextNode()) {
        var at = walker.currentNode.data.indexOf(words);
        if (at >= 0) { var r = document.createRange(); r.setStart(walker.currentNode, at); r.setEnd(walker.currentNode, at + words.length); word = rect(r); break; }
      }
    }
    done(JSON.stringify({
      tip: tip && !tip.hidden ? tip.textContent : "", tipBox: tip && !tip.hidden ? rect(tip) : null, button: button ? rect(document.querySelector(button)) : null,
      popover: pop.hidden ? null : rect(pop), style: pop.getAttribute("style"), word: word,
      focus: a === document.querySelector(".editor-area") ? "area" : a === document.querySelector(".editor-address") ? "address" :
        a && a.closest(".editor-toolbar") ? (a.getAttribute("data-kitwork-format") || a.getAttribute("data-kitwork-command") || "trigger") : a ? a.tagName.toLowerCase() : "none"
    }));
  }); }); });
})`

type editorBox struct{ Top, Right, Bottom, Left float64 }

type editorPlaceRead struct {
	Tip     string
	TipBox  *editorBox
	Button  *editorBox
	Popover *editorBox
	Style   *string
	Word    *editorBox
	Focus   string
}

// editorPlaceAt reads the page (editorPlaceLook), button a selector ("" for none), words in the area.
func editorPlaceAt(d *devtools, button, words string) editorPlaceRead {
	d.t.Helper()
	var r editorPlaceRead
	raw := d.evaluate(editorPlaceLook + `(` + jsString(button) + `, ` + jsString(words) + `)`)
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		d.t.Fatalf("the page reads %s: %v", raw, err)
	}
	return r
}

// editorMouseTo moves the mouse to a point of the screen, as a person's hand does (no button down).
func editorMouseTo(d *devtools, x, y float64) {
	d.t.Helper()
	d.call("Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": x, "y": y})
}

// The tooltips of the editor's toolbar and its link popover under the words, by a person's mouse and
// keys in a real browser, on the example site's /components/editor (the demo: its names are English,
// "Bold"), the page as the engine compiles and serves it with its stylesheet, the editor's part loaded
// by the editor itself:
//   - the mouse onto Bold: after 500 ms (none at 250) "Bold · Ctrl+B" right under the button (4px), centred
//     on it; Escape hides it, the focus where it was;
//   - the mouse onto Italic: its tooltip; down through the gap, onto the tooltip: it stays (hoverable);
//     into the writing area: it goes;
//   - the keyboard (the page given focus, so :focus-visible is the browser's own): a click in the area,
//     Alt+F10, the focus on Rich, the toolbar's stop (first of the formats since 21/10/2026: a button
//     with words, no tooltip); three arrows on, the focus on Bold and its tooltip after 500 ms; the arrow
//     to Italic, Italic's; Escape hides it and leaves the focus on Italic, the next Escape is the
//     toolbar's, back to the area;
//   - a word in the middle of the post selected, the link button clicked with the mouse: the popover's top
//     8px under the word, its left edge at the word's, the address focused; Escape shuts it, its style gone.
//
// CONTROL (run 21/10/2026, copies through -overlay, controls.py in the scratchpad's
// editor-tips/fix/controls): with the editor never loading its place part (the loader in v2.0.0.js returning at once), "the mouse
// onto Bold" failed (no tooltip); with the part not reading the pointer on the tooltip (onTip() false:
// the tooltip takes no pointer, so the pointer on it is read from where it is), "down through the gap"
// failed (the tooltip gone), and "onto the tooltip" with it. With the demo without its segment of
// formats (run 21/10/2026, c23 in the scratchpad's editor-formats/integrate: a copy of the examples tree,
// examplesRoot pointed at it through an overlaid example_sites_test.go), "Alt+F10" failed (focus bold)
// and the arrows after it.
func TestBrowserEditorTooltipsAndPopoverByTheMouse(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the editor place browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant, _ := examplePage(t, "/components/editor")
	site := newEditorSite(tenant, exampleHost)
	defer site.Close()
	d := openDevtools(t, browser, site.URL+"/components/editor", 1280, 1000, "")
	d.loaded(exampleSettle)
	d.call("Page.enable", map[string]any{})
	d.call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": editorBrowserHeard})
	networkReload(d)
	// the page has the focus, as a person's window has it: :focus-visible is the browser's own
	d.call("Emulation.setFocusEmulationEnabled", map[string]any{"enabled": true})
	d.evaluate(editorBrowserProbe)
	near := func(what string, got, want, within float64) {
		t.Helper()
		if math.Abs(got-want) > within {
			t.Errorf("%s: %.1f, want %.1f (±%.1f)", what, got, want, within)
		}
	}
	const bold, italic = `.editor-toolbar [data-kitwork-format="bold"]`, `.editor-toolbar [data-kitwork-format="italic"]`

	// the mouse onto Bold, then Escape
	x, y := editorCenter(d, bold)
	editorMouseTo(d, x, y)
	time.Sleep(250 * time.Millisecond)
	if r := editorPlaceAt(d, bold, ""); r.Tip != "" {
		t.Errorf("the mouse onto Bold: a tooltip at 250 ms already, %q", r.Tip)
	}
	time.Sleep(550 * time.Millisecond)
	r := editorPlaceAt(d, bold, "")
	if r.Tip != "Bold · Ctrl+B" || r.TipBox == nil {
		t.Fatalf("the mouse onto Bold: tooltip %q, want %q", r.Tip, "Bold · Ctrl+B")
	}
	near("its top under the button", r.TipBox.Top, r.Button.Bottom+4, 1.5)
	near("centred on it", (r.TipBox.Left+r.TipBox.Right)/2, (r.Button.Left+r.Button.Right)/2, 1.5)
	focus := r.Focus
	editorReviewKey(d, "Escape", "Escape", 27, 0)
	if r = editorPlaceAt(d, bold, ""); r.Tip != "" || r.Focus != focus {
		t.Errorf("Escape: tooltip %q, focus %s, want none and the focus on %s", r.Tip, r.Focus, focus)
	}

	// the mouse onto Italic, down through the gap onto the tooltip, then into the area
	x, y = editorCenter(d, italic)
	editorMouseTo(d, x, y)
	time.Sleep(800 * time.Millisecond)
	r = editorPlaceAt(d, italic, "")
	if r.Tip != "Italic · Ctrl+I" || r.TipBox == nil {
		t.Fatalf("the mouse onto Italic: tooltip %q", r.Tip)
	}
	editorMouseTo(d, x, r.Button.Bottom+2)
	time.Sleep(300 * time.Millisecond)
	if got := editorPlaceAt(d, italic, "").Tip; got != "Italic · Ctrl+I" {
		t.Errorf("down through the gap: tooltip %q", got)
	}
	editorMouseTo(d, x, (r.TipBox.Top+r.TipBox.Bottom)/2)
	time.Sleep(300 * time.Millisecond)
	if got := editorPlaceAt(d, italic, "").Tip; got != "Italic · Ctrl+I" {
		t.Errorf("onto the tooltip: tooltip %q", got)
	}
	ax, ay := editorCenter(d, ".editor-area")
	editorMouseTo(d, ax, ay)
	time.Sleep(100 * time.Millisecond)
	if got := editorPlaceAt(d, "", "").Tip; got != "" {
		t.Errorf("into the area: tooltip %q, want none", got)
	}

	// the keyboard: Alt+F10 to Bold, the arrow to Italic, Escape twice
	for _, kind := range []string{"mousePressed", "mouseReleased"} {
		d.call("Input.dispatchMouseEvent", map[string]any{"type": kind, "x": ax, "y": ay, "button": "left", "clickCount": 1})
	}
	editorReviewKey(d, "F10", "F10", 121, 1)
	time.Sleep(800 * time.Millisecond)
	// (the toolbar's stop is Rich, first of the formats: a button with words, no tooltip; the arrows on
	// to Bold)
	if r = editorPlaceAt(d, `[data-kitwork-format-value="rich"]`, ""); r.Focus != "format" || r.Tip != "" {
		t.Errorf("Alt+F10: focus %s, tooltip %q, want the switch to rich and none", r.Focus, r.Tip)
	}
	for i := 0; i < 3; i++ {
		editorReviewKey(d, "ArrowRight", "ArrowRight", 39, 0)
	}
	time.Sleep(800 * time.Millisecond)
	if r = editorPlaceAt(d, bold, ""); r.Focus != "bold" || r.Tip != "Bold · Ctrl+B" {
		t.Errorf("the arrows to Bold: focus %s, tooltip %q, want bold and %q", r.Focus, r.Tip, "Bold · Ctrl+B")
	}
	editorReviewKey(d, "ArrowRight", "ArrowRight", 39, 0)
	time.Sleep(800 * time.Millisecond)
	if r = editorPlaceAt(d, italic, ""); r.Focus != "italic" || r.Tip != "Italic · Ctrl+I" {
		t.Errorf("the arrow to Italic: focus %s, tooltip %q", r.Focus, r.Tip)
	}
	editorReviewKey(d, "Escape", "Escape", 27, 0)
	if r = editorPlaceAt(d, italic, ""); r.Focus != "italic" || r.Tip != "" {
		t.Errorf("Escape on Italic: focus %s, tooltip %q, want italic and none", r.Focus, r.Tip)
	}
	editorReviewKey(d, "Escape", "Escape", 27, 0)
	if r = editorPlaceAt(d, "", ""); r.Focus != "area" {
		t.Errorf("the next Escape: focus %s, want area (the toolbar's Escape)", r.Focus)
	}

	// a word in the middle of the post, the link button clicked: the popover under the word
	d.evaluate(`window.__editor.select("board")`)
	editorClick(d, `.editor-toolbar [data-kitwork-command="link"]`)
	r = editorPlaceAt(d, "", "board")
	if r.Popover == nil || r.Word == nil {
		t.Fatalf("the link button: popover %v, word %v", r.Popover, r.Word)
	}
	near("the popover under the word: its top", r.Popover.Top, r.Word.Bottom+8, 2)
	near("its left edge at the word's", r.Popover.Left, r.Word.Left, 2)
	if r.Focus != "address" {
		t.Errorf("the popover open: focus %s, want address", r.Focus)
	}
	editorReviewKey(d, "Escape", "Escape", 27, 0)
	if r = editorPlaceAt(d, "", ""); r.Popover != nil || r.Style != nil || r.Focus != "area" {
		t.Errorf("Escape: popover %v, style %v, focus %s, want shut, no style, the area", r.Popover, r.Style, r.Focus)
	}
	if got := d.evaluate(`JSON.stringify(window.__complaints)`); got != "[]" {
		t.Errorf("the page complains: %s", got)
	}
}

// editorPlacePage opens the demo /components/editor as TestBrowserEditorTooltipsAndPopoverByTheMouse
// does, width by height, the editor's place part served late by delay (0: at once), the toolbar 120px
// under the top of the viewport.
func editorPlacePage(t *testing.T, width, height int, delay time.Duration) *devtools {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping the editor place browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant, _ := examplePage(t, "/components/editor")
	site := newEditorSite(tenant, exampleHost)
	if delay > 0 {
		served := site.Config.Handler
		site.Config.Handler = http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			if strings.Contains(request.URL.Path, "/parts/editor-place.") {
				time.Sleep(delay)
			}
			served.ServeHTTP(response, request)
		})
	}
	t.Cleanup(site.Close)
	d := openDevtools(t, browser, site.URL+"/components/editor", width, height, "")
	d.loaded(exampleSettle)
	d.call("Page.enable", map[string]any{})
	d.call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": editorBrowserHeard})
	networkReload(d)
	d.call("Emulation.setFocusEmulationEnabled", map[string]any{"enabled": true})
	d.evaluate(editorBrowserProbe)
	d.evaluate(`scrollTo(0, scrollY + document.querySelector(".editor-toolbar").getBoundingClientRect().top - 120)`)
	return d
}

// editorTipWords are the words of the tooltip shown, "" for none.
const editorTipWords = `(function () { var t = document.querySelector(".editor-tip"); return t && !t.hidden ? t.textContent : ""; })()`

// editorAt is the middle of the first element selector finds, on the screen as it stands (no scroll).
func editorAt(d *devtools, selector string) (float64, float64) {
	d.t.Helper()
	var at struct{ X, Y float64 }
	raw := d.evaluate(`(function () { var r = document.querySelector(` + jsString(selector) + `).getBoundingClientRect(); return JSON.stringify({ x: r.left + r.width / 2, y: r.top + r.height / 2 }); })()`)
	if err := json.Unmarshal([]byte(raw), &at); err != nil {
		d.t.Fatalf("nothing on the page is %s (%s)", selector, raw)
	}
	return at.X, at.Y
}

// editorPressAt presses and lets go the mouse's button at a point of the screen.
func editorPressAt(d *devtools, x, y float64) {
	d.t.Helper()
	for _, kind := range []string{"mouseMoved", "mousePressed", "mouseReleased"} {
		d.call("Input.dispatchMouseEvent", map[string]any{"type": kind, "x": x, "y": y, "button": "left", "clickCount": 1})
	}
}

// The review of 21/10/2026, by a person's mouse on the demo: the tooltip takes no press.
//   - 640px wide, the toolbar in rows: Bold's tooltip covers a format button of the next row; the hand
//     straight down onto it, its own tooltip; a press there runs its command on "board";
//   - Bold's tooltip shown, a press on the foot of Bold (0.5px over its bottom, beside the tooltip) bolds
//     "board";
//   - the hand over Bold (the point Bold's, not the site's header) and out of the window at its top within
//     100 ms: no tooltip after it has gone.
//
// CONTROL (run 21/10/2026, copies through -overlay, controls.py in the scratchpad's
// editor-tips/fix/controls): with the tooltip taking the pointer (pointer-events:none gone from ui/stylesheet/editor.go, the ::before
// of before the review back), "the hand onto the button under Bold's tooltip", "a press there runs its
// command" and "a press on the foot of Bold" failed; with the part not reading a pointerout, "the hand out
// of the window" failed (Bold · Ctrl+B after it had gone).
func TestBrowserEditorTooltipsTakeNoPress(t *testing.T) {
	const bold = `.editor-toolbar [data-kitwork-format="bold"]`
	d := editorPlacePage(t, 640, 900, 0)
	tip := func() string { return d.evaluate(editorTipWords) }
	board := func() string {
		return d.evaluate(`(function () { var html = document.querySelector(".editor-area").innerHTML, i = html.indexOf("board"); return html.slice(Math.max(0, i - 12), i + 5); })()`)
	}
	bx, by := editorAt(d, bold)
	editorMouseTo(d, bx, by)
	time.Sleep(800 * time.Millisecond)
	if got := tip(); got != "Bold · Ctrl+B" {
		t.Fatalf("the mouse onto Bold: tooltip %q", got)
	}
	var under struct {
		Name string
		X, Y float64
	}
	raw := d.evaluate(`(function () {
  var tip = document.querySelector(".editor-tip").getBoundingClientRect(), b = document.querySelector(` + jsString(bold) + `).getBoundingClientRect();
  var all = Array.prototype.slice.call(document.querySelectorAll(".editor-toolbar [data-kitwork-format]")).filter(function (button) { return !button.closest(".popover"); });
  for (var i = 0; i < all.length; i++) {
    var r = all[i].getBoundingClientRect(), x = (r.left + r.right) / 2;
    if (r.top > b.bottom && x > tip.left && x < tip.right && r.top < tip.bottom - 4) return JSON.stringify({ name: all[i].getAttribute("data-kitwork-format"), x: x, y: (r.top + Math.min(r.bottom, tip.bottom)) / 2 });
  }
  return "none";
})()`)
	if err := json.Unmarshal([]byte(raw), &under); err != nil {
		t.Fatalf("640px wide, the toolbar in rows: no format button under Bold's tooltip (%s)", raw)
	}
	for i := 1; i <= 5; i++ {
		editorMouseTo(d, bx+(under.X-bx)*float64(i)/5, by+(under.Y-by)*float64(i)/5)
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	if got := tip(); got == "" || got == "Bold · Ctrl+B" {
		t.Errorf("the hand onto the button under Bold's tooltip (%s): tooltip %q, want its own", under.Name, got)
	}
	d.evaluate(`window.__editor.select("board")`)
	before := board()
	editorPressAt(d, under.X, under.Y)
	time.Sleep(200 * time.Millisecond)
	if after := board(); after == before {
		t.Errorf("a press there runs its command (%s) on \"board\": still %q", under.Name, after)
	}

	editorMouseTo(d, bx, by)
	time.Sleep(800 * time.Millisecond)
	d.evaluate(`window.__editor.select("board")`)
	var foot float64
	_ = json.Unmarshal([]byte(d.evaluate(`document.querySelector(`+jsString(bold)+`).getBoundingClientRect().bottom`)), &foot)
	editorPressAt(d, bx, foot-0.5)
	time.Sleep(200 * time.Millisecond)
	if got := board(); !strings.Contains(got, "<strong>board") {
		t.Errorf("Bold's tooltip shown, a press on the foot of Bold bolds \"board\": got %q", got)
	}

	ax, ay := editorAt(d, ".editor-area")
	editorMouseTo(d, ax, ay)
	time.Sleep(400 * time.Millisecond)
	if hit := d.evaluate(`(function () { var e = document.elementFromPoint(` + strconv.FormatFloat(bx, 'f', 2, 64) + `, ` + strconv.FormatFloat(by, 'f', 2, 64) + `); return e && e.closest("button") === document.querySelector(` + jsString(bold) + `) ? "bold" : String(e && e.tagName); })()`); hit != "bold" {
		t.Fatalf("the hand over Bold: the point is %s's", hit)
	}
	editorMouseTo(d, bx, by)
	time.Sleep(100 * time.Millisecond)
	editorMouseTo(d, bx, -30)
	time.Sleep(900 * time.Millisecond)
	if got := tip(); got != "" {
		t.Errorf("the hand over Bold and out of the window: tooltip %q after it had gone", got)
	}
	if got := d.evaluate(`JSON.stringify(window.__complaints)`); got != "[]" {
		t.Errorf("the page complains: %s", got)
	}
}

// The review of 21/10/2026, by a person's mouse on the demo, its place part 400 ms late (the first hover
// loads it): the hand over Bold and resting on Italic, Italic's tooltip once the part is here; on a page
// of its own, the hand over Bold and into the writing area, none.
//
// CONTROL (run 21/10/2026, copies of v2.0.0.js through -overlay, controls.py in the scratchpad's
// editor-tips/fix/controls): with the editor keeping the first pointerover and no pointerout, "the hand
// resting on Italic" failed (Bold · Ctrl+B) and "the hand into the area" failed (Bold · Ctrl+B over the
// words).
func TestBrowserEditorTooltipWhileItsPartComes(t *testing.T) {
	const bold, italic = `.editor-toolbar [data-kitwork-format="bold"]`, `.editor-toolbar [data-kitwork-format="italic"]`
	d := editorPlacePage(t, 1280, 1000, 400*time.Millisecond)
	bx, by := editorAt(d, bold)
	ix, iy := editorAt(d, italic)
	editorMouseTo(d, bx, by)
	time.Sleep(100 * time.Millisecond)
	editorMouseTo(d, ix, iy)
	time.Sleep(1300 * time.Millisecond)
	if got := d.evaluate(editorTipWords); got != "Italic · Ctrl+I" {
		t.Errorf("the hand over Bold, resting on Italic while the part came: tooltip %q, want %q", got, "Italic · Ctrl+I")
	}

	d = editorPlacePage(t, 1280, 1000, 400*time.Millisecond)
	bx, by = editorAt(d, bold)
	editorMouseTo(d, bx, by)
	time.Sleep(60 * time.Millisecond)
	editorMouseTo(d, bx+300, by+200)
	time.Sleep(1300 * time.Millisecond)
	if got := d.evaluate(editorTipWords); got != "" {
		t.Errorf("the hand over Bold and into the area while the part came: tooltip %q, want none", got)
	}
}
