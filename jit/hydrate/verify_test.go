package hydrate

import (
	"strings"
	"testing"
)

// Five of the six bugs found on 25/09 were silent: the grammar refuses what it cannot vouch for and
// says nothing, on purpose, so a mistyped handler never breaks a page at runtime. The place to be
// loud is the build, where the server already holds the whole document, the parser, and the list of
// everything a service grants.
//
// These are the three the day actually cost me. Each one is a real mistake made in this repo.

func verifyOf(t *testing.T, html string) string {
	t.Helper()
	return strings.Join(authoringMistakes(html), "\n")
}

// kit.theme.system() existed on the service and was missing from its expression grant. In markup it
// read as undefined, the call returned undefined, the button did nothing, the console stayed clean.
func TestVerifyNamesAnUngrantedServiceMember(t *testing.T) {
	report := verifyOf(t, `<button data-kit-click="kit.theme.systen()">x</button>`)

	if !strings.Contains(report, "kit.theme") || !strings.Contains(report, "systen") {
		t.Errorf("an ungranted service member was not reported:\n%s", report)
	}
	// The report has to say what IS available, or the author is left guessing at a spelling.
	if !strings.Contains(report, "toggle") {
		t.Errorf("the report should name the members that do exist:\n%s", report)
	}

	// CONTROL: the members that ARE granted must stay silent, or the check is just noise.
	//
	// kit.window is here on purpose. The first grant reader ran one regex across all three modules
	// and swallowed window's grant into the span of an earlier, ungranted service — so a real
	// titlebar (huynhnhanquoc.com) got three false alarms for calls that work. native.js declares
	// window AFTER several services that grant nothing, which is exactly the shape that broke it.
	for _, fine := range []string{
		`<button data-kit-click="kit.theme.toggle()">x</button>`,
		`<button data-kit-click="kit.theme.set('dark')">x</button>`,
		`<b data-kit-text="kit.theme.mode"></b>`,
		`<button data-kit-click="kit.clipboard.copy('x')">x</button>`,
		`<button data-kit-click="kit.window.minimize()">x</button>`,
		`<button data-kit-click="kit.window.close()">x</button>`,
		`<button data-kit-click="kit.storage.set('k', 1)">x</button>`,
	} {
		if report := verifyOf(t, fine); report != "" {
			t.Errorf("a granted member was reported as a mistake: %s\n%s", fine, report)
		}
	}
}

// A service the kernel does not expose to markup at all — permissions, files, the bridge — must be
// named too, and named differently: the member is not the problem, the whole service is.
func TestVerifyNamesAServiceMarkupCannotReach(t *testing.T) {
	report := verifyOf(t, `<button data-kit-click="kit.permissions.request('camera')">x</button>`)

	if !strings.Contains(report, "kit.permissions") {
		t.Errorf("an ungranted service was not reported:\n%s", report)
	}
	if !strings.Contains(report, "not available") && !strings.Contains(report, "cannot") {
		t.Errorf("the report should say the service is out of reach, not blame the member:\n%s", report)
	}
}

// $element.nosuch.textContent is how copy(undefined) reached the clipboard: the name resolved to
// nothing and the expression carried on. The server can see every data-kit-element in the document.
func TestVerifyNamesAnElementNothingDeclares(t *testing.T) {
	html := `<body><figure data-kit-component="clipboard">
  <pre data-kit-element="source">x</pre>
  <button data-kit-click="copy($element.sorce.textContent)">c</button>
</figure></body>`
	report := verifyOf(t, html)

	if !strings.Contains(report, "sorce") {
		t.Errorf("an element name nothing declares was not reported:\n%s", report)
	}
	if !strings.Contains(report, "source") {
		t.Errorf("the report should name the element that IS declared:\n%s", report)
	}

	// CONTROL: the correct spelling is silent.
	correct := strings.Replace(html, "$element.sorce", "$element.source", 1)
	if report := verifyOf(t, correct); report != "" {
		t.Errorf("a declared element was reported as a mistake:\n%s", report)
	}
}

// A FRAGMENT is not judged. huynhnhanquoc.com calls $element.search.showModal() in its header and
// declares data-kit-element="search" in its subbar — two slots of one page. Read file by file, that
// looked like a typo five times over; assembled, it is correct. Render always sees the assembled
// page, so the pass only speaks about names when it can see everything a name could refer to.
func TestVerifyDoesNotGuessAboutAFragment(t *testing.T) {
	header := `<button data-kit-click="$element.search.showModal()">Search</button>`
	if report := verifyOf(t, header); report != "" {
		t.Errorf("a fragment was judged as if it were the whole page:\n%s", report)
	}

	// The same markup, assembled with the slot that declares the name: silent, and rightly so.
	page := `<html><body>` + header + `<dialog data-kit-element="search"></dialog></body></html>`
	if report := verifyOf(t, page); report != "" {
		t.Errorf("an element declared in another part of the assembled page was reported:\n%s", report)
	}

	// CONTROL: assembled WITHOUT the declaring slot, it is a real finding and must be said.
	if report := verifyOf(t, `<html><body>`+header+`</body></html>`); !strings.Contains(report, "search") {
		t.Errorf("a whole page that really declares nothing should be reported:\n%s", report)
	}
}

// Documentation prints the grammar inside <pre><code>, and an example is text. kitjs.org's
// installation page shows a whole standalone page there — including data-kit-click="count++" —
// and it must not be reported as the page's own markup.
func TestVerifyDoesNotJudgeAPrintedExample(t *testing.T) {
	example := `<html><body><pre><code>&lt;button data-kit-click="count++"&gt;+&lt;/button&gt;
&lt;b data-kit-text="kit.theme.systen"&gt;&lt;/b&gt;</code></pre></body></html>`
	if report := verifyOf(t, example); report != "" {
		t.Errorf("a printed example was judged as live markup:\n%s", report)
	}

	// CONTROL: a directive ON the <pre> itself is live, and a mistake in it must still be named.
	live := `<html><body><pre data-kit-text="kit.theme.systen"></pre></body></html>`
	if report := verifyOf(t, live); !strings.Contains(report, "systen") {
		t.Errorf("a live directive on a <pre> element was skipped along with its content:\n%s", report)
	}
}

// The existing checks must keep working, since this pass is where they live.
func TestVerifyStillReportsWhatItAlreadyDid(t *testing.T) {
	if report := verifyOf(t, `<b data-kit-text="count *">x</b>`); !strings.Contains(report, "hydrate:") {
		t.Errorf("a malformed expression was not reported:\n%s", report)
	}
	if report := verifyOf(t, `<b data-kit-class="'text-' + colour">x</b>`); !strings.Contains(report, "written out in full") {
		t.Errorf("a constructed class name was not reported:\n%s", report)
	}
	// …but joining two complete names with a space is not construction. This is studio's own
	// completion popup, reduced; it was reported until the separator was told apart from a fragment.
	joined := `<b data-kit-class="(wide ? 'left-96' : 'left-14') + ' ' + (tall ? 'top-8' : 'top-2')">x</b>`
	if report := verifyOf(t, joined); report != "" {
		t.Errorf("two complete names joined by a space were reported as constructed:\n%s", report)
	}
}

// And a page with nothing wrong reports nothing at all — otherwise every site would learn to ignore
// this output, which is the failure mode the whole pass exists to avoid.
func TestVerifyIsSilentOnCorrectMarkup(t *testing.T) {
	html := `<div data-kit-scope="{ open: false, count: 0 }" data-kit-click:outside="open = false">
  <button data-kit-click="open = !open" data-kit-bind:aria-expanded="open ? 'true' : 'false'">m</button>
  <ul data-kit-show="open" hidden>
    <li data-kit-for="item, i of items" data-kit-key="item.id" data-kit-text="item.name + ' ' + i"></li>
  </ul>
  <b data-kit-text="kit.theme.resolved"></b>
  <input data-kit-model="count" data-kit-debounce="200">
</div>`
	if report := verifyOf(t, html); report != "" {
		t.Errorf("correct markup produced a report:\n%s", report)
	}
}

// A binding runs on every paint, so one that writes changes the state each time the page is drawn.
// KitJS 1.0.0 made it a rule — bindings read, actions write — and it arrived with ++/-- (29/09).
// It covers every write, not just the new operators: data-kit-text="count = 5" is the same mistake.
func TestVerifyNamesAWriteInsideABinding(t *testing.T) {
	for _, bad := range []string{
		`<b data-kit-text="count++"></b>`,
		`<b data-kit-text="--count"></b>`,
		`<b data-kit-show="open = true"></b>`,
		`<b data-kit-class="$.seen++ ? 'a' : 'b'"></b>`,
		`<b data-kit-bind:title="label = 'x'"></b>`,
		`<b data-kit-style:width="n++"></b>`,
		// A lambda in a binding is there to be CALLED: map runs this write once per item, per paint.
		`<b data-kit-text="items.map((x) => seen++).length"></b>`,
	} {
		if report := verifyOf(t, bad); !strings.Contains(report, "binding runs on every paint") {
			t.Errorf("a write inside a binding was not reported: %s\n%s", bad, report)
		}
	}

	// CONTROL: the same writes in an ACTION are the point of the grammar and must stay silent —
	// including the exact demo counter kitjs.org ships. A lambda that only READS is fine anywhere.
	for _, fine := range []string{
		`<button data-kit-click="count++">+</button>`,
		`<button data-kit-click="--count">-</button>`,
		`<button data-kit-click="open = !open">t</button>`,
		`<form data-kit-submit:prevent="$.sent++"></form>`,
		`<b data-kit-text="count + 1"></b>`,
		`<b data-kit-text="items.filter((x) => x.done).length"></b>`,
	} {
		if report := verifyOf(t, fine); report != "" {
			t.Errorf("a write in an action, or a pure read, was reported: %s\n%s", fine, report)
		}
	}
}
