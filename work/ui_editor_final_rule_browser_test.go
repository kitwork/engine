package work

import (
	"strings"
	"testing"

	"github.com/kitwork/engine/utilities/richtext"
)

// A rule that ends the post (decided 21/10/2026: it is kept, so what the writer sees is what is saved),
// by the keys and the mouse as a person does it. These read the editor through editorFinalRuleLook:
// the field (held to the identity by editorStep: the area cleaned by Go is the field), whether it is
// unsaved, and where the caret is ("under the rule": in the line right after the area's last rule).

// editorFinalRulePage opens the page of editorV2Files holding saved, the probe installed, with its site
// (to wait for a post).
func editorFinalRulePage(t *testing.T, saved string) (*devtools, *editorSite) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping the editor browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	files := map[string]string{}
	for name, source := range editorV2Files {
		files[name] = strings.Replace(source, editorV2Saved, saved, 1)
	}
	site := newEditorSite(buildUISite(t, files), "localhost")
	t.Cleanup(site.Close)
	d := openDevtools(t, browser, site.URL+"/", 1000, 1400, "")
	d.loaded(exampleSettle)
	d.call("Page.enable", map[string]any{})
	d.call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": editorBrowserHeard})
	networkReload(d)
	d.evaluate(`window.__before = true`)
	d.evaluate(editorBrowserProbe)
	return d, site
}

// editorFinalRuleCaret says where the caret is: "under the rule" in the line right after the area's
// last rule, "above the rule" in a line before it, "in the rule" in the hr itself, "beside the rule" in
// the area itself, "no rule" when the area has none, "none" outside the area.
const editorFinalRuleCaret = `(function () {
  var area = document.querySelector(".editor-area"), s = getSelection(), rules = area.querySelectorAll("hr"), rule = rules[rules.length - 1];
  if (!s.rangeCount || !area.contains(s.anchorNode)) return "none";
  if (s.anchorNode === area) return "beside the rule";
  var line = s.anchorNode.nodeType === 1 ? s.anchorNode : s.anchorNode.parentNode;
  while (line.parentNode !== area) line = line.parentNode;
  return !rule ? "no rule" : line === rule ? "in the rule" : line.previousElementSibling === rule ? "under the rule" : rule.compareDocumentPosition(line) & Node.DOCUMENT_POSITION_PRECEDING ? "above the rule" : "further down";
})()`

// editorFinalRuleLook is the field, unsaved, and the caret, in one line; the area's last two elements
// when asked.
func editorFinalRuleLook(t *testing.T, d *devtools, name string) string {
	t.Helper()
	got := editorStep(t, d, name)
	unsaved := "saved"
	if strings.Contains(got.State, "unsaved true") {
		unsaved = "unsaved"
	}
	return got.Field + " | " + unsaved + " | caret " + d.evaluate(editorFinalRuleCaret)
}

// rule() on the last line puts the rule and an empty line after it for the caret: the value holds the
// rule at once (unsaved), never the empty line. The browser's Backspace in that line takes the rule (as
// it does in the middle of a post); undo brings it back with the caret under it, where words typed go;
// undo and redo take and bring back what was typed there. Saved with the rule at its end, the post is
// posted with it, shown with it, and the answer's editor holds it with a line under it again.
// CONTROL (21/10/2026, copies of v2.0.0.js through -overlay, controls.py in the scratchpad's
// final-rule): with read() dropping a rule at the end again (its kept.pop() put back), "rule() on the
// last line" failed (<p>một</p>\n<p>hai</p>, saved: the rule no part of the value; the area cleaned by
// Go not the field), and so did every row after it that ends in a rule, "what was sent" among them;
// with normalise() leaving no line under a rule that ends the post, "the answer's area: a line under the
// rule" failed (rule() itself puts one there).
func TestBrowserEditorFinalRuleByTheKeys(t *testing.T) {
	d, site := editorFinalRulePage(t, `<p>một</p><p>hai</p>`)
	look := func(name string) string { t.Helper(); return editorFinalRuleLook(t, d, name) }
	check := func(what, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s:\n got  %s\n want %s", what, got, want)
		}
	}
	const post, ruled = "<p>một</p>\n<p>hai</p>", "<p>một</p>\n<p>hai</p>\n<hr>"
	editorClick(d, ".editor-area p")
	editorCaretAt(d, "hai", -1)
	editorClick(d, `[data-kitwork-command="rule"]`)
	check("rule() on the last line", look("rule"), ruled+" | unsaved | caret under the rule")
	check("the line under it", d.evaluate(`document.querySelector(".editor-area").innerHTML`), "<p>một</p><p>hai</p><hr><p><br></p>")
	editorReviewKey(d, "Backspace", "Backspace", 8, 0)
	check("Backspace in the line under the rule takes it", look("Backspace"), post+" | saved | caret no rule")
	editorPress(d, "Control+Z")
	check("undo brings the rule back, the caret under it", look("undo"), ruled+" | unsaved | caret under the rule")
	editorType(d, "ba")
	check("words typed there go under it", look("typed"), ruled+"\n<p>ba</p> | unsaved | caret under the rule")
	editorPress(d, "Control+Z")
	check("undo the words", look("undo the words"), ruled+" | unsaved | caret under the rule")
	editorPress(d, "Control+Y")
	check("redo them", look("redo"), ruled+"\n<p>ba</p> | unsaved | caret under the rule")
	editorPress(d, "Control+Z")
	check("undo them again", look("undo again"), ruled+" | unsaved | caret under the rule")

	// saved with the rule at its end
	from := site.heard()
	editorClickAway(d, `button[type="submit"]`)
	check("Save clicked", editorDialog(d), "no question")
	site.waitFor(t, "the post", func() bool { return strings.Contains(site.pages(from), "POST") })
	d.loaded(`(window.__before ? "wait of 0" : ` + exampleSettle + `)`)
	check("what the browser sent", site.pages(from), "POST / 200 site=same-origin application/x-www-form-urlencoded")
	sent := d.evaluate(`document.querySelector("pre code.language-html").textContent`)
	check("what was sent", sent, ruled)
	check("what richtext() keeps of it", richtext.Clean(sent).HTML, ruled)
	check("the post shown", d.evaluate(`document.querySelector("#shown .richtext").innerHTML`), richtext.Display(sent, "http://localhost"))
	d.evaluate(editorBrowserProbe)
	check("the answer's editor", strings.SplitN(editorFinalRuleLook(t, d, "the answer"), " | ", 2)[0], ruled)
	check("the answer's area: a line under the rule", d.evaluate(`document.querySelector(".editor-area").innerHTML`), "<p>một</p><p>hai</p><hr><p><br></p>")
	check("complaints", d.evaluate(`JSON.stringify(window.__complaints)`), "[]")
}

// A post saved with a rule at its end, opened again: the value is the post (saved), and the area holds
// an empty line under the rule (normalise), so a click under the rule, Control+End and undo put the
// caret there and words typed go under the rule (in Chrome a caret with no line under the rule stands
// beside it, and a click under it types above it). Backspace in that line takes the rule; undo brings it
// back with the caret under it, and words typed go there.
// CONTROL (21/10/2026, copies of v2.0.0.js through -overlay, controls.py in the scratchpad's
// final-rule): with normalise() leaving no line under a rule that ends the post, "as it opens" failed
// (<p>một</p><hr>), and "a click under the rule, then x" (<p>một</p>\n<p>x</p>\n<hr>: the caret above
// the rule, the words typed above it) and every row after it; with unpath() leaving a path that ends
// on a rule in it (no move to the line after it), "undo after Backspace" failed (the caret in the
// rule) and "undo after Backspace, then u" (<p>một</p>\n<p>u</p>\n<hr>: the words above the rule); with
// read() dropping a rule at the end again, "as it opens" failed, every row after it whose value ends in
// a rule, and "Backspace in the line under the rule takes it" (saved: the value it opened with had no
// rule).
func TestBrowserEditorFinalRuleOpenedAgain(t *testing.T) {
	d, _ := editorFinalRulePage(t, `<p>một</p><hr>`)
	look := func(name string) string { t.Helper(); return editorFinalRuleLook(t, d, name) }
	check := func(what, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s:\n got  %s\n want %s", what, got, want)
		}
	}
	const ruled = "<p>một</p>\n<hr>"
	check("as it opens", look("opened")+" | "+d.evaluate(`document.querySelector(".editor-area").innerHTML`), ruled+" | saved | caret none | <p>một</p><hr><p><br></p>")
	x, y := editorCenter(d, ".editor-area hr")
	for _, kind := range []string{"mouseMoved", "mousePressed", "mouseReleased"} {
		d.call("Input.dispatchMouseEvent", map[string]any{"type": kind, "x": x, "y": y + 30, "button": "left", "clickCount": 1})
	}
	editorType(d, "x")
	check("a click under the rule, then x", look("click under"), ruled+"\n<p>x</p> | unsaved | caret under the rule")
	editorPress(d, "Control+Z")
	check("undo", look("undo"), ruled+" | saved | caret under the rule")
	editorType(d, "w")
	check("undo, then w", look("w"), ruled+"\n<p>w</p> | unsaved | caret under the rule")
	editorPress(d, "Control+Z")
	editorCaretAt(d, "một", 0)
	editorPress(d, "Control+End")
	check("Control+End", look("Control+End"), ruled+" | saved | caret under the rule")
	editorReviewKey(d, "Backspace", "Backspace", 8, 0)
	check("Backspace in the line under the rule takes it", look("Backspace"), "<p>một</p> | unsaved | caret no rule")
	editorPress(d, "Control+Z")
	check("undo after Backspace", look("undo after Backspace"), ruled+" | saved | caret under the rule")
	editorType(d, "u")
	check("undo after Backspace, then u", look("u"), ruled+"\n<p>u</p> | unsaved | caret under the rule")
	check("complaints", d.evaluate(`JSON.stringify(window.__complaints)`), "[]")
}

// editorFinalRuleCaretLine says where the caret is by its block, for a post with more than one rule:
// the block it is in, the element before that block, and how many characters of the block come before
// the caret; "the area, at n" when it is in the area itself (beside a rule).
const editorFinalRuleCaretLine = `(function () {
  var area = document.querySelector(".editor-area"), s = getSelection(), block, r = document.createRange();
  if (!s.rangeCount || !area.contains(s.anchorNode)) return "none";
  if (s.anchorNode === area) return "the area, at " + s.anchorOffset;
  for (block = s.anchorNode; block.parentNode !== area; block = block.parentNode);
  r.setStart(block, 0);
  r.setEnd(s.anchorNode, s.anchorOffset);
  return "in " + block.outerHTML + " after " + (block.previousElementSibling ? block.previousElementSibling.outerHTML : "nothing") + ", " + r.toString().length + " in";
})()`

// A paste or a drop whose blocks end in a rule keeps the rule (the value may end in one since
// 21/10/2026), and leaves the caret at the start of the line after it, as rule() does: an empty
// paragraph made there when no block follows (or a rule does), else the line the paste split off, the
// block that followed, a list's first item. From there every key and button acts under the rule (in the
// area itself, beside the rule, the editor's commands act on the line above it: Control+B, Enter, the
// heading button, another paste). Backspace in the line under the pasted rule takes it (the line stays,
// empty, as after rule()), undo brings it back with the caret under it, and undo takes the paste away
// whole. A line split off keeps the space it starts with until a word is typed before it: Chrome then
// drops the space it never showed, as it does after Enter in the middle of a line ("whai").
// CONTROL (21/10/2026, a copy of parts/editor-paste.js through -overlay that puts the caret after the
// last block it placed, a rule too, as before: p1-paste-caret-beside-rule in the scratchpad's
// final-rule/controls.py): every row but Backspace's failed. Every "pasted" and "dropped" row had the
// caret in the area, at 3 (beside the rule); Control+B then w wrote <p>b<strong>w</strong></p>\n<hr>,
// above the rule; the heading button made <h2>bt</h2>; both undos left the caret beside the rule;
// before a rule the two rules stood side by side with no line between them.
func TestBrowserEditorFinalRulePasted(t *testing.T) {
	check := func(t *testing.T, what, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s:\n got  %s\n want %s", what, got, want)
		}
	}
	look := func(t *testing.T, d *devtools, name string) string {
		t.Helper()
		return editorFinalRuleLook(t, d, name) + " | " + d.evaluate(editorFinalRuleCaretLine)
	}
	area := func(d *devtools) string { return d.evaluate(`document.querySelector(".editor-area").innerHTML`) }
	const blocks, pasted = `<p>b</p><hr>`, "<p>một</p>\n<p>b</p>\n<hr>"

	t.Run("at the end of the post", func(t *testing.T) {
		d, _ := editorFinalRulePage(t, `<p>một</p>`)
		editorClick(d, ".editor-area p")
		editorCaretAt(d, "một", -1)
		d.evaluate(`window.__editor.paste(` + jsString(blocks) + `, "b")`)
		check(t, "pasted", look(t, d, "pasted"), pasted+" | unsaved | caret under the rule | in <p><br></p> after <hr>, 0 in")
		check(t, "the line under it", area(d), "<p>một</p><p>b</p><hr><p><br></p>")
		editorPress(d, "Control+B")
		editorType(d, "w")
		check(t, "Control+B, then w", look(t, d, "bold w"), pasted+"\n<p><strong>w</strong></p> | unsaved | caret under the rule | in <p><strong>w</strong></p> after <hr>, 1 in")
		editorPress(d, "Control+Z")
		check(t, "undo the w", look(t, d, "undo w"), pasted+" | unsaved | caret under the rule | in <p><br></p> after <hr>, 0 in")
		editorReviewKey(d, "Backspace", "Backspace", 8, 0)
		check(t, "Backspace in the line under the rule takes it", look(t, d, "Backspace"), "<p>một</p>\n<p>b</p> | unsaved | caret no rule | in <p><br></p> after <p>b</p>, 0 in")
		editorPress(d, "Control+Z")
		check(t, "undo brings the rule back, the caret under it", look(t, d, "undo Backspace"), pasted+" | unsaved | caret under the rule | in <p><br></p> after <hr>, 0 in")
		editorClick(d, `[data-kitwork-format="heading-2"]`)
		editorType(d, "t")
		check(t, "the heading button, then t", look(t, d, "heading"), pasted+"\n<h2>t</h2> | unsaved | caret under the rule | in <h2>t</h2> after <hr>, 1 in")
		editorPress(d, "Control+Z")
		editorPress(d, "Control+Z")
		editorPress(d, "Control+Z")
		check(t, "undo takes the paste away whole", editorFinalRuleLook(t, d, "undo the paste"), "<p>một</p> | saved | caret no rule")
		check(t, "complaints", d.evaluate(`JSON.stringify(window.__complaints)`), "[]")
	})

	t.Run("in the middle of a line", func(t *testing.T) {
		d, _ := editorFinalRulePage(t, `<p>một hai</p>`)
		editorClick(d, ".editor-area p")
		editorCaretAt(d, "một", -1)
		d.evaluate(`window.__editor.paste(` + jsString(blocks) + `, "b")`)
		check(t, "pasted", look(t, d, "pasted"), pasted+"\n<p>hai</p> | unsaved | caret under the rule | in <p> hai</p> after <hr>, 0 in")
		editorPress(d, "Control+B")
		editorType(d, "w")
		check(t, "Control+B, then w", look(t, d, "bold w"), pasted+"\n<p><strong>w</strong>hai</p> | unsaved | caret under the rule | in <p><strong>w</strong>hai</p> after <hr>, 1 in")
		check(t, "complaints", d.evaluate(`JSON.stringify(window.__complaints)`), "[]")
	})

	t.Run("before a rule", func(t *testing.T) {
		d, _ := editorFinalRulePage(t, `<p>một</p><hr><p>hai</p>`)
		editorClick(d, ".editor-area p")
		editorCaretAt(d, "một", -1)
		d.evaluate(`window.__editor.paste(` + jsString(blocks) + `, "b")`)
		check(t, "pasted", look(t, d, "pasted"), pasted+"\n<p>hai</p> | unsaved | caret above the rule | in <p><br></p> after <hr>, 0 in")
		check(t, "an empty line between the two rules", area(d), "<p>một</p><p>b</p><hr><p><br></p><hr><p>hai</p>")
		editorPress(d, "Control+B")
		editorType(d, "w")
		check(t, "Control+B, then w", look(t, d, "bold w"), pasted+"\n<p><strong>w</strong></p>\n<hr>\n<p>hai</p> | unsaved | caret above the rule | in <p><strong>w</strong></p> after <hr>, 1 in")
		check(t, "complaints", d.evaluate(`JSON.stringify(window.__complaints)`), "[]")
	})

	t.Run("in a list", func(t *testing.T) {
		d, _ := editorFinalRulePage(t, `<ul><li>một hai</li></ul>`)
		editorClick(d, ".editor-area li")
		editorCaretAt(d, "một", -1)
		d.evaluate(`window.__editor.paste(` + jsString(blocks) + `, "b")`)
		check(t, "pasted", look(t, d, "pasted"), "<ul>\n<li>một</li>\n</ul>\n<p>b</p>\n<hr>\n<ul>\n<li>hai</li>\n</ul> | unsaved | caret under the rule | in <ul><li> hai</li></ul> after <hr>, 0 in")
		editorPress(d, "Control+B")
		editorType(d, "w")
		check(t, "Control+B, then w", look(t, d, "bold w"), "<ul>\n<li>một</li>\n</ul>\n<p>b</p>\n<hr>\n<ul>\n<li><strong>w</strong>hai</li>\n</ul> | unsaved | caret under the rule | in <ul><li><strong>w</strong>hai</li></ul> after <hr>, 1 in")
		check(t, "complaints", d.evaluate(`JSON.stringify(window.__complaints)`), "[]")
	})

	t.Run("a drop", func(t *testing.T) {
		d, _ := editorFinalRulePage(t, `<p>một</p>`)
		editorDrop(d, ".editor-area p", blocks)
		check(t, "dropped", look(t, d, "dropped"), pasted+" | unsaved | caret under the rule | in <p><br></p> after <hr>, 0 in")
		editorPress(d, "Control+B")
		editorType(d, "w")
		check(t, "Control+B, then w", look(t, d, "bold w"), pasted+"\n<p><strong>w</strong></p> | unsaved | caret under the rule | in <p><strong>w</strong></p> after <hr>, 1 in")
		check(t, "complaints", d.evaluate(`JSON.stringify(window.__complaints)`), "[]")
	})
}
