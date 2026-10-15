package hydrate

import (
	"io"
	"os"
	"strings"
	"testing"
)

// The server half of the grouped web output (KITWORK_UI.md §11): recognising a page written only in
// data-kitwork-*, and saying, while someone develops, what in it the kernel would quietly skip.

// A page whose directives are all grouped uses the grammar — each of the eight alone is enough —
// and the transport markers the engine hangs on nearly every page are not grammar at all.
func TestGroupedDirectivesAreTheGrammarAndTransportIsNot(t *testing.T) {
	page := func(body string) string { return `<html><head></head><body>` + body + `</body></html>` }
	for _, body := range []string{
		`<div data-kitwork-component="progressbar"></div>`,
		`<div data-kitwork-scope="open: false"></div>`,
		`<b data-kitwork-bind="text: total">0</b>`,
		`<i data-kitwork-class="icon-sun: dark"></i>`,
		`<div data-kitwork-style="width: value + '%'"></div>`,
		`<button data-kitwork-event="click: toggle()">x</button>`,
		`<p data-kitwork-if="open">x</p>`,
		`<li data-kitwork-for="row of rows; key: row.id">x</li>`,
	} {
		if !UsesGrammar(page(body)) {
			t.Errorf("a page with only %s uses the grammar", body)
		}
	}
	for _, body := range []string{
		`<style data-kitwork-generated="css"></style>`,
		`<main data-kitwork-layout="page.ui"></main>`,
		`<a href="/" data-kitwork-current="">x</a>`,
		`<div data-kitwork-delay="0"></div>`,
		`<main data-kitwork-transition="fade"></main>`,
		`<div data-kitwork-shell="app"></div>`,
		`<code data-kitwork-highlight="go">x</code>`,
		`<link data-kitwork-jit="manifest">`,
		`<div data-kitwork-bindings="text: x" data-kitwork-format="x"></div>`, // a longer name is not one of the eight
		`<i data-kitwork-classes="icon-sun: dark"></i>`,                       // nor is one a letter longer than class
	} {
		if UsesGrammar(page(body)) {
			t.Errorf("transport is not grammar: %s", body)
		}
		// and it costs a substring test, not a regex: the cheap door stays shut
		if hasDirectivePrefix(page(body)) {
			t.Errorf("the cheap door must stay shut for transport: %s", body)
		}
		// past the cheap door too: a Kitwork UI link's data-kit-reload (Drive's opt-out, no directive)
		// lets the substring test through, so the regex itself must not take transport for grammar
		if reload := page(`<a data-kit-reload="true" href="/x">x</a>` + body); UsesGrammar(reload) {
			t.Errorf("transport beside data-kit-reload is not grammar: %s", body)
		}
	}
	// a grouped directive shown in an example block is text, as data-kit-* is
	if UsesGrammar(page(`<pre><code>&lt;b data-kitwork-bind="text: x"&gt;</code></pre>`)) {
		t.Error("a grouped directive printed in <pre><code> is an example, not grammar")
	}
	// control: the hand-written form is still the grammar, and an example of it is still not
	if !UsesGrammar(page(`<b data-kit-text="total">0</b>`)) {
		t.Error("data-kit-* is still the grammar")
	}
}

// Check is gated by the same door: a grouped-only page is read and its mistakes are printed; a
// transport-only page costs a substring test and prints nothing.
func TestCheckReadsAGroupedOnlyPage(t *testing.T) {
	report := capturedHydrateCheck(t, `<html><body><b data-kitwork-bind="text: a +">0</b></body></html>`)
	if !strings.Contains(report, "data-kitwork-bind text:") || !strings.Contains(report, `in data-kitwork-bind="text: a +"`) {
		t.Errorf("a mistake on a grouped-only page must be printed, got:\n%s", report)
	}
	// control: the same page written correctly prints nothing
	if report := capturedHydrateCheck(t, `<html><body><div data-kitwork-scope="a: 1"><b data-kitwork-bind="text: a + 1">0</b></div></body></html>`); report != "" {
		t.Errorf("nothing to say about a correct page, got:\n%s", report)
	}
}

func capturedHydrateCheck(t *testing.T, html string) string {
	t.Helper()
	previous := Diagnostics
	Diagnostics = true
	defer func() { Diagnostics = previous }()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = write
	Check(html, nil)
	os.Stdout = stdout
	_ = write.Close()
	printed, _ := io.ReadAll(read)
	return strings.TrimSpace(string(printed))
}

// Each grouped mistake is named with its attribute, its pair and where to look; the same markup one
// edit away from correct says nothing.
func TestGroupedValuesAreVerified(t *testing.T) {
	cases := []struct {
		name, markup string
		want         []string // every one must be in the report
	}{
		{"a value that does not compile", `<b data-kitwork-bind="show: open; text: a +"></b>`,
			[]string{"data-kitwork-bind text: hydrate:", "the value starts at character 19", `in data-kitwork-bind="show: open; text: a +"`}},
		{"a value that does not split", `<b data-kitwork-bind="text: f(a; show: b"></b>`,
			[]string{`data-kitwork-bind: "(" is never closed (at character 8)`}},
		{"an event handler as a bind", `<b data-kitwork-bind="onclick: go()"></b>`, []string{"data-kitwork-bind onclick: is refused", "data-kitwork-event"}},
		{"style as a bind", `<b data-kitwork-bind="style: s"></b>`, []string{"data-kitwork-bind style:", "data-kitwork-style"}},
		{"srcdoc", `<iframe data-kitwork-bind="srcdoc: page"></iframe>`, []string{"data-kitwork-bind srcdoc: is refused"}},
		{"a directive as a bind", `<b data-kitwork-bind="data-kit-text: x"></b>`, []string{"data-kitwork-bind data-kit-text: is refused"}},
		{"class as a bind", `<b data-kitwork-bind="class: on"></b>`, []string{"data-kitwork-bind class: classes are toggled one by one in data-kitwork-class", "(at character 1)"}},
		// class.<name> left data-kitwork-bind for data-kitwork-class (11/10/2026): the kernel no longer
		// toggles anything for it, so the pair is reported, with where it went
		{"a class toggle as a bind", `<b data-kitwork-bind="text: x; class.done: value == 100"></b>`,
			[]string{"data-kitwork-bind class.done: a class toggle is no longer a bind pair, so nothing toggles done", `write it in data-kitwork-class="done: value == 100"`, "(at character 10)"}},
		{"a class toggle as a bind naming nothing", `<b data-kitwork-bind="class.: on"></b>`, []string{"data-kitwork-bind class.: toggles no class", "data-kitwork-class"}},
		{"a class condition that does not compile", `<b data-kitwork-class="on: a; done: value =="></b>`,
			[]string{"data-kitwork-class done: hydrate:", "the value starts at character 14", `in data-kitwork-class="on: a; done: value =="`}},
		{"a class value that does not split", `<b data-kitwork-class="done: f(a"></b>`, []string{`data-kitwork-class: "(" is never closed (at character 8)`}},
		{"an arbitrary value", `<b data-kitwork-class="on: a; w-[3px]: wide"></b>`, []string{`"w-[3px]" is not a name`, "(at character 8)", "an arbitrary value"}},
		{"a variant class", `<b data-kitwork-class="on: a; hover:underline: on"></b>`,
			[]string{`data-kitwork-class hover: "hover:underline" is a variant class, and a toggled class holds no ':'`, "(at character 8)"}},
		{"a write in a class condition", `<b data-kitwork-class="done: count++"></b>`, []string{`data-kitwork-class done writes to "count"`, "Move the write to data-kitwork-event"}},
		{"a class named twice", `<b data-kitwork-class="on: a; on: b"></b>`, []string{"data-kitwork-class names on twice (the second at character 8)"}},
		{"the bind form copied into class", `<b data-kitwork-class="class.done: x"></b>`, []string{`data-kitwork-class class.done: toggles a class named "class.done"`, "write done: x"}},
		{"the bind form copied into class is a warning, so its value is still compiled", `<b data-kitwork-class="class.done: x +"></b>`,
			[]string{`toggles a class named "class.done"`, "data-kitwork-class class.done: hydrate:"}},
		{"kit in a class condition", `<b data-kitwork-class="icon-sun: kit.theme.dark"></b>`, []string{"markup does not call kit", `in data-kitwork-class="icon-sun: kit.theme.dark"`}},
		{"not an attribute name", `<b data-kitwork-bind="text: x; Title: t"></b>`, []string{"data-kitwork-bind Title: is not text, show", "(at character 10)"}},
		{"a CSS property in capitals", `<b data-kitwork-style="Width: w"></b>`, []string{"data-kitwork-style Width is not a CSS property name"}},
		{"the engine's custom properties", `<b data-kitwork-style="--kitwork-brand: c"></b>`, []string{"data-kitwork-style --kitwork-brand writes the engine's own namespace"}},
		{"a blocked property", `<b data-kitwork-style="behavior: b"></b>`, []string{"data-kitwork-style behavior is refused"}},
		{"an unknown modifier", `<b data-kitwork-event="click.prevnt: go()"></b>`, []string{"data-kitwork-event click.prevnt: unknown modifier .prevnt", "the pipeline is .window"}},
		{"a key filter on a click", `<b data-kitwork-event="click.escape: go()"></b>`, []string{".escape and .enter filter a key", "not click"}},
		{"outside said twice", `<b data-kitwork-event="click-outside.outside: close()"></b>`, []string{"click-outside already listens outside"}},
		{"outside on an event it cannot watch", `<b data-kitwork-event="context-menu.outside: x()"></b>`, []string{".outside applies to click", "not contextmenu"}},
		{"a modifier on ready", `<b data-kitwork-event="ready.once: x()"></b>`, []string{"ready runs once", "takes no modifier"}},
		{"leave on intersect-leave", `<b data-kitwork-event="intersect-leave.leave: x()"></b>`, []string{"unknown modifier .leave for intersect-leave"}},
		// the example must be one the grouped form can write: a pair's name has no '%'
		{"a margin with nothing in it", `<b data-kitwork-event="intersect-enter.margin(): x()"></b>`, []string{".margin() needs a CSS margin, like .margin(-2rem)"}},
		{"a timing out of range", `<b data-kitwork-event="input.debounce(0): search()"></b>`, []string{"debounce(0): the delay is 1–60000 milliseconds"}},
		{"an empty modifier", `<b data-kitwork-event="click..once: go()"></b>`, []string{"has an empty modifier"}},
		{"an unknown event name", `<b data-kitwork-event="hover: peek()"></b>`,
			[]string{`data-kitwork-event hover: "hover" is not an event the kernel knows`, "custom event", "(at character 1)"}},
		{"an unknown event name is a warning, so its value is still compiled", `<b data-kitwork-event="hover: a +"></b>`,
			[]string{`"hover" is not an event the kernel knows`, "data-kitwork-event hover: hydrate:"}},
		{"a write in a bind", `<b data-kitwork-bind="text: count++"></b>`, []string{`data-kitwork-bind text writes to "count"`, "Move the write to data-kitwork-event"}},
		{"a write in a style", `<b data-kitwork-style="width: w = 3"></b>`, []string{`data-kitwork-style width writes to "w"`}},
		{"a write in an if", `<b data-kitwork-if="open = true"></b>`, []string{`data-kitwork-if writes to "open"`}},
		{"a write in a for", `<b data-kitwork-for="x of rows = []"></b>`, []string{`data-kitwork-for list writes to "rows"`}},
		{"two expressions in an if", `<b data-kitwork-if="a; b"></b>`, []string{"data-kitwork-if holds one expression; a second one starts at character 4"}},
		{"a for that is not a for", `<b data-kitwork-for="line in items"></b>`, []string{"data-kitwork-for:", `is not "item of list"`}},
		{"a for pair that is not key", `<b data-kitwork-for="line of items; track: line.id"></b>`, []string{"a for takes only key after the list, not track (at character 16)"}},
		{"a key that does not compile", `<b data-kitwork-for="line of items; key: line.id +"></b>`, []string{"data-kitwork-for key: hydrate:", "the value starts at character 21"}},
		{"a scope key that is not a name", `<b data-kitwork-scope="open-now: false"></b>`, []string{"data-kitwork-scope open-now: a scope key is a name"}},
		{"a name given twice", `<b data-kitwork-bind="text: a; text: b"></b>`, []string{"data-kitwork-bind names text twice (the second at character 10)"}},
		{"a component name with a space", `<b data-kitwork-component="drop down"></b>`, []string{"data-kitwork-component:", "is not a component name"}},
		{"an alias without $", `<b data-kitwork-component="dropdown: account"></b>`, []string{"data-kitwork-component:", `"account" is not an alias`}},
		{"a version that is not one", `<b data-kitwork-component="theme@one"></b>`, []string{`"one" is not a version`}},
		{"a capital in a component name", `<b data-kitwork-component="Dialog"></b>`, []string{`"Dialog" is not a component name`}},
		{"a reserved alias", `<b data-kitwork-component="menu: $this"></b>`, []string{"$this is the kernel's own handle"}},
		{"kit in markup", `<b data-kitwork-event="click: kit.theme.toggle()"></b>`, []string{"markup does not call kit", "the theme component", `in data-kitwork-event="click: kit.theme.toggle()"`}},
	}
	for _, c := range cases {
		report := verifyOf(t, c.markup)
		for _, want := range c.want {
			if !strings.Contains(report, want) {
				t.Errorf("%s: %s\n  want %q in the report, got:\n%s", c.name, c.markup, want, report)
			}
		}
	}

	// control: everything the vocabulary allows, written right, says nothing
	for _, fine := range []string{
		`<b data-kitwork-bind="text: money(total()); show: open; value: query; checked: done; aria-expanded: open; title: name; disabled: !ready"></b>`,
		// every name the splitter allows is a class name, and a ternary's ':' is not a variant's
		`<b data-kitwork-class="done: value == 100; icon-sun: resolved == 'dark'; is_open: open; text-2.5: big; a+b: x; w-(--size): wide; on: a ? b : c; off: x ? 'a:b' : 'c'"></b>`,
		// an alpha and a glass are names too (13/10/2026: glass/60 is the thin glass)
		`<b data-kitwork-class="bg-red-500/50: on; material-glass/60: dark; background-brand+15/40: hot"></b>`,
		`<b data-kitwork-style="width: value + '%'; top: offset(); --progress: value; flex-basis: size + '%'"></b>`,
		`<b data-kitwork-event="click: toggle(); double-click.outside: x(); click-outside: close(); keydown.escape: close(); keydown.ctrl+k: open(); input.debounce(300): search(); submit.prevent: save(); focus-in: f(); pointer-enter.once: peek(); scroll.throttle(100): s(); intersect-enter.once: seen(); intersect-leave.half: gone(); ready: start(); change: c(); toggle: t(); load: l(); error: e()"></b>`,
		`<b data-kitwork-event="click: count++"></b>`,                                                                 // an event may write: it is an action, not a binding
		`<b data-kitwork-event="intersect-enter.margin(-1.5rem): seen(); intersect-leave.margin(0.5px): gone()"></b>`, // a dot inside the (…) is the margin's
		`<b data-kitwork-scope="open: false; count: 0; label: 'a; b'"></b>`,
		`<b data-kitwork-if="a && b"></b>`,
		`<b data-kitwork-for="row, i of rows.slice(0, 3); key: row.id"></b>`,
		`<b data-kitwork-for="line of items"></b>`,
		`<b data-kitwork-component="progressbar"></b><b data-kitwork-component="theme@1.0.0: $theme"></b><b data-kitwork-component="dialog@v2.0.0"></b>`,
		`<b data-kitwork-bind="text: a &amp;&amp; b; title: &#39;x&#39;"></b>`, // decoded as the browser decodes it
		`<pre><code>&lt;b data-kitwork-bind="text: a +"&gt;</code></pre>`,      // an example is not checked
	} {
		if report := verifyOf(t, fine); report != "" {
			t.Errorf("%s: nothing should be reported, got:\n%s", fine, report)
		}
	}
}

// The undeclared-name pass reads the grouped boundaries and pairs: every key of a data-kitwork-scope
// (not only the first — the comma reader would see one), a for's item, a two-way value, and the
// reads of every bind, style and event pair and the if.
func TestGroupedNamesAreChecked(t *testing.T) {
	cases := []struct {
		name string
		html string
		want []string // names warned about, in order
	}{
		{"a typo against a grouped scope", `<body><div data-kitwork-scope="darkMode: false"><span data-kitwork-bind="show: dark">…</span></div></body>`, []string{"dark"}},
		{"control: spelled right", `<body><div data-kitwork-scope="darkMode: false"><span data-kitwork-bind="show: darkMode">…</span></div></body>`, nil},
		{"the second key of a scope", `<body><div data-kitwork-scope="open: false; count: 0"><b data-kitwork-bind="text: count"></b></div></body>`, nil},
		{"control: a name neither key is", `<body><div data-kitwork-scope="open: false; count: 0"><b data-kitwork-bind="text: cnt"></b></div></body>`, []string{"cnt"}},
		{"a for's item", `<body><ul data-kitwork-scope="rows: []"><li data-kitwork-for="row of rows; key: row.id" data-kitwork-bind="text: row.name"></li></ul></body>`, nil},
		{"control: a name the for does not bind", `<body><ul data-kitwork-scope="rows: []"><li data-kitwork-for="row of rows" data-kitwork-bind="text: item.name"></li></ul></body>`, []string{"item"}},
		{"a two-way value declares its name", `<body><input data-kitwork-bind="value: query"><div data-kitwork-scope="x: 1"><b data-kitwork-bind="text: query"></b></div></body>`, nil},
		{"control: a one-way value declares nothing", `<body><input data-kitwork-bind="value: query + ''"><div data-kitwork-scope="x: 1"><b data-kitwork-bind="text: query"></b></div></body>`, []string{"query", "query"}},
		{"an assignment in a handler is not a read", `<body><div data-kitwork-scope="open: false"><button data-kitwork-event="click: opne = true">x</button></div></body>`, nil},
		{"an event handler calling nothing", `<body><div data-kitwork-scope="open: false"><button data-kitwork-event="click: tggle()">x</button></div></body>`, []string{"tggle"}},
		{"a style value", `<body><div data-kitwork-scope="size: 1"><b data-kitwork-style="width: sise + '%'"></b></div></body>`, []string{"sise"}},
		{"a class condition", `<body><div data-kitwork-scope="open: false"><b data-kitwork-class="active: opne; icon-sun: open"></b></div></body>`, []string{"opne"}},
		{"control: a class condition that reads the scope", `<body><div data-kitwork-scope="open: false"><b data-kitwork-class="active: open"></b></div></body>`, nil},
		{"control: a class name is not a read", `<body><div data-kitwork-scope="open: false"><b data-kitwork-class="dark: open; text-red-500: !open"></b></div></body>`, nil},
		{"an if", `<body><div data-kitwork-scope="open: false"><b data-kitwork-if="shown"></b></div></body>`, []string{"shown"}},
		{"control: an if that reads the scope", `<body><div data-kitwork-scope="open: false"><b data-kitwork-if="open"></b></div></body>`, nil},
		{"a component host is opaque when its names are unknown", `<body><div data-kitwork-component="workbench: $bench"><b data-kitwork-bind="text: anything"></b></div></body>`, nil},
		{"a scope that does not split declares nothing certain", `<body><div data-kitwork-scope="open: f("><b data-kitwork-bind="text: anything"></b></div></body>`, nil},
	}
	for _, c := range cases {
		lines := undeclaredWarnings(c.html)
		var got []string
		for _, line := range lines {
			if start := strings.Index(line, ` reads "`); start >= 0 {
				rest := line[start+len(` reads "`):]
				got = append(got, rest[:strings.IndexByte(rest, '"')])
			}
		}
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: got %v, want %v\n%s", c.name, got, c.want, strings.Join(lines, "\n"))
		}
	}
	// the report names the pair it came from
	lines := undeclaredWarnings(`<body><div data-kitwork-scope="darkMode: false"><span data-kitwork-bind="show: dark">…</span></div></body>`)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], `data-kitwork-bind show reads "dark"`) {
		t.Errorf("the report names the attribute and the pair, got %q", lines)
	}
	lines = undeclaredWarnings(`<body><div data-kitwork-scope="open: false"><b data-kitwork-class="active: opne"></b></div></body>`)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], `data-kitwork-class active reads "opne"`) {
		t.Errorf("a class condition's report names the attribute and the class, got %q", lines)
	}
}

// A component's names are checked through the grouped host too: the name before ": $alias" is the
// component, a literal in data-kitwork-scope sets its parameters, and the alias reaches it from outside.
func TestGroupedComponentHostIsChecked(t *testing.T) {
	stepper := func(component string) ([]string, bool) {
		if component == "stepper" || component == "stepper@1.0.0" {
			return []string{"add", "amount", "max"}, true
		}
		return nil, false
	}
	check := func(html string) string {
		return strings.Join(undeclaredWarningsWith(html, stepper), "\n")
	}
	page := func(inside, outside string) string {
		return `<body><div data-kitwork-component="stepper@1.0.0: $step" data-kitwork-scope="max: 3">` + inside + `</div>` + outside + `</body>`
	}
	for label, c := range map[string]struct{ html, want string }{
		"inside the host":     {page(`<button data-kitwork-event="click: ad()">+</button>`, ``), `reads "ad", which component stepper@1.0.0 does not have`},
		"through the alias":   {page(``, `<button data-kitwork-event="click: $step.ad()">+</button>`), `reads $step.ad, which component stepper@1.0.0 does not have`},
		"a stray parameter":   {`<body><div data-kitwork-component="stepper" data-kitwork-scope="min: 0"></div></body>`, `data-kitwork-scope sets "min" on component stepper`},
		"the pair is labeled": {page(`<b data-kitwork-bind="text: amont"></b>`, ``), `data-kitwork-bind text reads "amont"`},
	} {
		if got := check(c.html); !strings.Contains(got, c.want) {
			t.Errorf("%s: want %q, got:\n%s", label, c.want, got)
		}
	}
	// control: the names the component has, inside, through the alias, and as a parameter
	if got := check(page(`<button data-kitwork-event="click: add()">+</button><b data-kitwork-bind="text: amount"></b>`, `<button data-kitwork-event="click: $step.add()">+</button>`)); got != "" {
		t.Errorf("nothing to report, got:\n%s", got)
	}
}

// ToggledClasses is what the CSS, icon, logo, font and material engines read: the keys of
// data-kitwork-class, whole, once each, in document order — and nothing else that looks like a class.
func TestToggledClassesAreTheClassKeys(t *testing.T) {
	html := `<i data-kitwork-class="icon-sun: dark; icon-moon: !dark" data-kitwork-bind="title: t"></i>` +
		`<b data-kitwork-bind="text: x" data-kitwork-class="text-red-500: bad; icon-sun: dark"></b>` +
		`<b data-kitwork-class="broken: f("></b>` + // does not split: skipped, Check names it
		// none of these is a toggle: the retired bind form, the hand-written expression, a style pair,
		// a longer attribute name, and a class written inside a condition
		`<b data-kitwork-bind="class.not-this: on" data-kit-class="{ 'nor-this': on }" data-kitwork-style="class.nor-that: x"></b>` +
		`<b data-kitwork-classes="not-mine: x" data-kitwork-class="flex: mode == 'grid-cols-2'"></b>`
	if got := strings.Join(ToggledClasses(html), " "); got != "icon-sun icon-moon text-red-500 flex" {
		t.Errorf("got %q", got)
	}
	if got := ToggledClasses(`<b data-kitwork-class="on: x == &#39;a; b&#39;; off: !x"></b>`); strings.Join(got, " ") != "on off" {
		t.Errorf("a value is decoded before it is split: got %q", got)
	}
	// control: the retired form alone toggles nothing, so it names nothing — the cheap door shuts
	if got := ToggledClasses(`<b class="static" data-kitwork-bind="class.old: x"></b>`); got != nil {
		t.Errorf("no data-kitwork-class, no classes: got %q", got)
	}
}
