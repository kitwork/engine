package stylesheet

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"testing"
)

// scrolled: is a state like hover: or active: (KITWORK_UI §8): true while the page is scrolled away
// from its top. The class is the attribute as written; its rule is in the page layer, the state inside
// :where() so it weighs one class like every page rule, read from the flag scrolled.<hash>.js keeps on
// <html>. It combines with widths the way states do (medium+scrolled:), never with (hover:hover).
// CONTROL: a page with only other states needs no flag.
func TestScrolledClassAndRule(t *testing.T) {
	table := NewTable(examplePalette(t))
	var r Rules
	got := classes(t, table, &r,
		Intent{State: "scrolled", Name: "shadow", Value: "small"},
		Intent{Width: "medium", State: "scrolled", Name: "background", Value: "paper"},
		Intent{State: "scrolled,hover", Name: "color", Value: "ink"})
	if got != "scrolled:shadow-small medium+scrolled:background-paper scrolled,hover:color-ink" {
		t.Fatalf("classes %q", got)
	}
	css := string(r.CSS())
	page := css[strings.Index(css, "@layer page{"):]
	for _, rule := range []string{
		`.scrolled\:shadow-small:where(:root[data-kitwork-scrolled] *){box-shadow:0 1px 2px 0 rgb(0 0 0/5%)}`,
		"@media (min-width:768px){\n" + `.medium\+scrolled\:background-paper:where(:root[data-kitwork-scrolled] *){background-color:var(--paper)}`,
		// a state group holds under either: under scrolled with no media, under hover where one hovers
		`.scrolled\,hover\:color-ink:where(:root[data-kitwork-scrolled] *){color:var(--ink)}`,
		"@media (hover:hover){\n" + `.scrolled\,hover\:color-ink:where(:hover){color:var(--ink)}`,
	} {
		if !strings.Contains(page, rule) {
			t.Errorf("the page layer lacks %s\n%s", rule, css)
		}
	}
	if strings.Contains(css, "(hover:hover){\n"+`.scrolled\:shadow-small`) {
		t.Errorf("scrolled is not a pointer state; it holds without (hover:hover):\n%s", css)
	}
	if !r.Scrolled() {
		t.Error("a page with a scrolled: class must say it needs the flag")
	}
	// a scrolled: class that animates nothing (align sets only variables) reads the flag all the same
	var still Rules
	classes(t, table, &still, Intent{State: "scrolled", Name: "align", Value: "center"})
	if !still.Scrolled() {
		t.Errorf("scrolled:align-center animates nothing and still needs the flag:\n%s", still.CSS())
	}
	if strings.Contains(string(still.CSS()), "transition") {
		t.Errorf("a variable does not animate: no transition:\n%s", still.CSS())
	}
	var others Rules
	classes(t, table, &others,
		Intent{State: "hover", Name: "shadow", Value: "small"},
		Intent{State: "pressed", Name: "scale", Value: "95"},
		Intent{Name: "shadow", Value: "small"})
	if others.Scrolled() {
		t.Error("control: a page whose classes hold under hover and pressed needs no scrolled flag")
	}
	if strings.Contains(string(others.CSS()), "data-kitwork-scrolled") {
		t.Errorf("control: no scrolled rule without scrolled:\n%s", others.CSS())
	}
}

// In the fixed order scrolled sits after open and active and before hover, focus, pressed and
// disabled: a header the page has scrolled under still lights up under the pointer, and a disabled
// button stays disabled-looking. Written in no particular order, the rules come out in that order.
// CONTROL: sorted by name, scrolled would come after pressed, so the order is the writer's.
func TestScrolledSitsBetweenActiveAndHover(t *testing.T) {
	table := NewTable(examplePalette(t))
	var r Rules
	written := []string{"disabled", "hover", "scrolled", "pressed", "open", "focus", "active"}
	for _, state := range written {
		if _, err := table.Class(&r, Intent{State: state, Name: "background", Value: "panel"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := table.Class(&r, Intent{Width: "large", State: "scrolled", Name: "background", Value: "paper"}); err != nil {
		t.Fatal(err)
	}
	css := string(r.CSS())
	at := func(selector string) int {
		i := strings.Index(css, selector)
		if i < 0 {
			t.Fatalf("no %s in\n%s", selector, css)
		}
		return i
	}
	order := []string{
		`.open\:background-panel:where(`,
		`.active\:background-panel:where(`,
		`.scrolled\:background-panel:where(:root[data-kitwork-scrolled] *)`,
		`.large\+scrolled\:background-paper:where(:root[data-kitwork-scrolled] *)`, // widths ascend inside the state
		`.hover\:background-panel:where(:hover)`,
		`.focus\:background-panel:where(`,
		`.pressed\:background-panel:where(`,
		`.disabled\:background-panel:where(`,
	}
	for i := 1; i < len(order); i++ {
		if at(order[i-1]) > at(order[i]) {
			t.Errorf("%s comes after %s; the order is open < active < scrolled < hover < focus < pressed < disabled:\n%s",
				order[i-1], order[i], css)
		}
	}
	names := []string{"scrolled:background-panel", "pressed:background-panel", "hover:background-panel", "active:background-panel"}
	sort.Strings(names)
	if indexOf(names, "scrolled:background-panel") < indexOf(names, "pressed:background-panel") {
		t.Fatalf("control: sorted by name scrolled no longer comes after pressed: %v", names)
	}
}

// An element that carries a scrolled: class gets a short transition for exactly the properties its
// scrolled: classes change — never all, never a property it does not change, never a CSS variable —
// so the shadow comes in and goes out softly. Each class has its own transition rule, outside the
// state so it animates both ways; an element with several such classes gets one more rule naming its
// own classes with every property they change, after the single ones (CSS keeps one transition per
// element: alone, the later single rule would drop the other's property). Classes that change the
// same properties need no merge. CONTROL: classes without scrolled: get no transition, and an element
// without a scrolled: class gets no merge.
func TestScrolledTransitionListsOnlyItsProperties(t *testing.T) {
	table := NewTable(examplePalette(t))
	var r Rules
	header := strings.Fields(classes(t, table, &r,
		Intent{Name: "pin", Value: "top"},
		Intent{Name: "padding", Value: "4"},
		Intent{State: "scrolled", Name: "shadow", Value: "small"},
		Intent{State: "scrolled", Name: "background", Value: "paper"},
		Intent{Width: "medium", State: "scrolled", Name: "padding-y", Value: "2"},
		Intent{State: "hover", Name: "color", Value: "brand"}))
	badge := classes(t, table, &r, Intent{Tag: "badge", State: "scrolled", Name: "color", Value: "success"})
	pressed := classes(t, table, &r, Intent{State: "pressed", Name: "scale", Value: "95"})
	twoWidths := strings.Fields(classes(t, table, &r,
		Intent{State: "scrolled", Name: "shadow", Value: "medium"},
		Intent{Width: "large", State: "scrolled", Name: "shadow", Value: "large"}))
	r.Transition(header)
	r.Transition([]string{badge})
	r.Transition(append([]string{pressed}, "scrolled:shadow-small"))
	r.Transition(twoWidths)
	r.Transition([]string{pressed, "hover:color-brand"})
	css := string(r.CSS())

	singles := []string{
		`.scrolled\:shadow-small{transition:box-shadow .15s}`,
		`.scrolled\:background-paper{transition:background-color .15s}`,
		`.medium\+scrolled\:padding-y-2{transition:padding-block .15s}`,
		// a badge's tint goes through --kitwork-tint, which does not animate: only color is listed
		`.scrolled\:color-success{transition:color .15s}`,
		`.pressed\:scale-95{transition:scale .15s,rotate .15s}`,
	}
	merged := []string{
		`.medium\+scrolled\:padding-y-2:where(.scrolled\:background-paper.scrolled\:shadow-small){transition:padding-block .15s,background-color .15s,box-shadow .15s}`,
		`.pressed\:scale-95:where(.scrolled\:shadow-small){transition:scale .15s,rotate .15s,box-shadow .15s}`,
	}
	for _, rule := range append(append([]string(nil), singles...), merged...) {
		if !strings.Contains(css, "\n"+rule+"\n") {
			t.Errorf("no rule %s in\n%s", rule, css)
		}
	}
	// the merged rules come after every single one, the larger set last
	last := 0
	for _, rule := range singles {
		if i := strings.Index(css, rule); i > last {
			last = i
		}
	}
	if two, three := strings.Index(css, merged[1]), strings.Index(css, merged[0]); two < last || three < two {
		t.Errorf("the merged transitions come after the single ones, a larger set after a smaller:\n%s", css)
	}
	// nothing the scrolled: classes do not change: not all, not the tint variable, not what the classes
	// without a state set (pin's position, padding)
	for _, never := range []string{"transition:all", "--kitwork-tint .15s", "position .15s", "z-index .15s", "padding .15s", "padding-inline .15s"} {
		if strings.Contains(css, never) {
			t.Errorf("a transition lists %q:\n%s", never, css)
		}
	}
	// classes that change the same property need no merge
	if strings.Contains(css, `:where(.large\+scrolled\:shadow-large)`) || strings.Contains(css, `.large\+scrolled\:shadow-large:where(.scrolled`) {
		t.Errorf("two shadows for two widths are one property, no merged rule:\n%s", css)
	}
	if n := strings.Count(css, "transition:"); n != len(singles)+len(merged)+2 { // + the two shadows' own
		t.Errorf("%d transitions, want %d:\n%s", n, len(singles)+len(merged)+2, css)
	}
	// control: hover changes no transition, and an element with no scrolled: class has no merge
	if strings.Contains(css, `.hover\:color-brand{`) || strings.Contains(css, `:where(.hover\:color-brand)`) {
		t.Errorf("control: a hover class has no transition, and no merge without scrolled:\n%s", css)
	}
}

// scrolled.<hash>.js is named by its role and its bytes, the bytes compacted (no comment line, no
// indentation) the same whichever way the checkout ends its lines. It keeps the flag the rules read,
// past the same 8px, without reading layout itself (14/10/2026): an IntersectionObserver reports when
// its mark, a hidden strip 1px wide over the page's top 8px that it puts beside <body>, leaves or
// comes back into view. The observer's root is the document, this page's own viewport (a page in a
// frame is not scrolled when the page around the frame is), the implicit root only where a browser
// refuses a document; either root reaches 100000px past both sides of the viewport (15/10/2026), so a
// page scrolled to the side is not scrolled from its top. The observer's branch reads no scroll
// position and no box and listens to no scroll; only the branch for a browser without
// IntersectionObserver does, its first read in an animation frame, never in the script's own run.
// (Its behaviour in a browser: TestBrowserScrolledFollowsThePage.)
func TestScrolledScript(t *testing.T) {
	script := ScrolledScript()
	sum := sha256.Sum256(script.JavaScript)
	if script.Role != "scrolled" || script.Name != "scrolled."+hex.EncodeToString(sum[:8]) {
		t.Fatalf("role %q name %q", script.Role, script.Name)
	}
	javascript := string(script.JavaScript)
	for _, part := range []string{
		`"data-kitwork-scrolled"`,
		// the observer and its mark: one hidden strip at the top of the page, a child of <html>
		"if (window.IntersectionObserver) {\n",
		`mark.setAttribute("data-kitwork-generated", "scrolled");`, `mark.setAttribute("aria-hidden", "true");`,
		`mark.style.cssText = "position:absolute;top:0;left:0;width:1px;height:8px;visibility:hidden";`,
		"root.appendChild(mark);",
		"flag(!entries[entries.length - 1].isIntersecting);\n",
		// measured against this page's own viewport, the implicit root only where a document is refused;
		// either way reaching far past both sides, so a scroll to the side is not a scroll from the top
		"try {\nobserver = new IntersectionObserver(report, { root: document, rootMargin: \"0px 100000px\" });\n} catch (error) {\n" +
			"observer = new IntersectionObserver(report, { rootMargin: \"0px 100000px\" });\n}\nobserver.observe(mark);\nreturn;\n}\n",
		// the browser without it: a passive listener and a first read in an animation frame
		`(window.scrollY || root.scrollTop || 0) > 8`,
		`window.addEventListener("scroll", update, { passive: true });`, "\nwindow.requestAnimationFrame(update);\n",
	} {
		if !strings.Contains(javascript, part) {
			t.Errorf("the script lacks %s:\n%s", part, javascript)
		}
	}
	observer := javascript[strings.Index(javascript, "if (window.IntersectionObserver) {"):]
	observer = observer[:strings.Index(observer, "return;\n}")]
	for _, layout := range []string{"scrollY", "scrollTop", "pageYOffset", "getBoundingClientRect", "offsetTop", "addEventListener", "update"} {
		if strings.Contains(observer, layout) {
			t.Errorf("the observer's branch reads %s; it must leave the layout to the browser:\n%s", layout, observer)
		}
	}
	if strings.Contains(javascript, "\nupdate();\n") {
		t.Errorf("the scroll position is read in the script's own run, which lays the page out there:\n%s", javascript)
	}
	for _, line := range strings.Split(javascript, "\n") {
		if strings.HasPrefix(line, "//") || strings.HasPrefix(line, " ") || strings.Contains(line, "\r") {
			t.Fatalf("a line is not compacted: %q", line)
		}
	}
	if crlf := compactScript(strings.ReplaceAll(scrolledSource, "\n", "\r\n")); string(crlf) != javascript {
		t.Errorf("a checkout with CRLF serves other bytes:\n%q", crlf)
	}
	if len(script.JavaScript) > 1152 {
		t.Errorf("the script is %d bytes; it is a few lines and an observer", len(script.JavaScript))
	}
	// what the rules read is what the script writes
	if !strings.Contains(stateSelector["scrolled"], "[data-kitwork-scrolled]") {
		t.Errorf("the scrolled rules read %s", stateSelector["scrolled"])
	}
}
