package ui

import (
	"strings"
	"testing"
)

// A terminal is reached from outside its host through its alias (decision T2, 12/10/2026): written
// outside every component, what reads $install and nothing else of the page goes to the browser as it
// does inside a component — an event as data-kitwork-event, a value as data-kitwork-bind — wherever it
// stands, before the host or after it, or inside another component. Its server paint is the value the
// component starts with. CONTROLS: an alias read with the page's data stays the server's, as it was.
func TestTerminalIsReachedThroughItsAlias(t *testing.T) {
	terminal := `<terminal as="$install"><code language="bash">npm i kitwork</code></terminal>`
	outside := `<button id="outside" click="$install.copy()">Chép</button>` +
		`<text id="state">{{ $install.copied ? 'yes' : 'no' }}</text>` +
		`<text id="shown" show="$install.copied">Đã chép</text>`
	for name, content := range map[string]string{
		"after the host":  `<content>` + terminal + outside + `</content>`,
		"before the host": `<content>` + outside + terminal + `</content>`,
	} {
		html := mustCompile(t, map[string]string{"/site/content.ui": content})
		mustContain(t, html,
			`id="outside" type="button" data-kitwork-event="click: $install.copy()">Chép</button>`,
			`<p id="state" data-kitwork-bind="text: $install.copied ? &#39;yes&#39; : &#39;no&#39;">no</p>`,
			`<p id="shown" data-kitwork-bind="show: $install.copied" hidden>`,
			`data-kitwork-component="terminal: $install"`)
		if t.Failed() {
			t.Fatalf("%s:\n%s", name, html)
		}
	}
	// inside another component the kernel runs, the alias reaches the terminal too
	menu := `<content>` + terminal + `<dropdown><button click="toggle()">Mở</button><popover><button click="$install.copy()">Chép lệnh</button></popover></dropdown></content>`
	mustContain(t, mustCompile(t, map[string]string{"/site/content.ui": menu}), `data-kitwork-event="click: $install.copy()">Chép lệnh</button>`)

	// CONTROL: an alias read with the page's data is not the browser's yet ($cart.add(product, 1)):
	// every argument is a name read, the first one too
	mixed := `<content>` + terminal + `<button click="$install.copy(note)">Chép</button><text>{{ $install.copied ? note : 'no' }}</text></content>`
	html := mustCompile(t, map[string]string{"/site/content.ui": mixed})
	if strings.Count(html, "data-kitwork-event") != 0 || strings.Count(html, "data-kitwork-bind") != 0 {
		t.Errorf("an alias read with the page's data went to the browser:\n%s", html)
	}
}

// usedNames reads every name of an argument list, an array and an object: the first item of each was
// once taken for an operator and lost, so $cart.add(product, 1) read as $cart alone.
func TestUsedNamesReadEveryItem(t *testing.T) {
	for source, want := range map[string]string{
		"$cart.add(product, 1)":   "$cart product",
		"f(a, b)":                 "f a b",
		"[a, b]":                  "a b",
		"({ x: a, y: b })":        "a b",
		"items.map((x) => x + y)": "items y",
	} {
		e, err := compileExpr(source, &Node{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(usedNames(e.ir), " "); got != want {
			t.Errorf("usedNames(%s) = %q, want %q", source, got, want)
		}
	}
}

// Outside every component, a name a component on the page declares names nothing (decision T2: copy()
// and copied outside a component that gives them are a compile error): said where it is written, with
// the two ways to reach it. The route's view may give the same name, and then it is the view's: a key of
// the object its context.view({ … }) is given (23/10/2026: a const of router.work's top level is no name
// of the page). CONTROLS: inside the host it is the terminal's; given by the view it is the view's; a name
// no component has stays the page's. CONTROL (23/10/2026, go test -overlay): with viewNames reading the
// file's top-level names as when router.view handed them, copied of the object was told as the terminal's
// ("content.ui:1:79: copied là tên của terminal …"), and the warnings of TestNameReadInsideTheHostIsNotShadowed
// and TestPartsShadowedNameIsAWarning, whose view hands open in its object, were not given ("got []").
// When the view's object is not known here — .view(data), .view({ ...base }), a router.work handing its
// context or its handler to code it imports — copied may be the view's and compiles. CONTROLS (23/10/2026,
// go test -overlay): with viewNames returning the names it found even when not known, the four rows failed
// "content.ui:1:79: copied là tên của terminal …"; with viewNames not looking at what a router.work hands an
// imported name, the two imported rows alone failed so; with any import making the names unknown, the row whose router.work calls
// listNavigation() with no argument compiled ("expected a compile error").
func TestTerminalNamesOutsideItsHost(t *testing.T) {
	terminal := `<terminal><code language="bash">npm i kitwork</code></terminal>`
	view := map[string]string{"/site/router.work": `const title = "Kitwork";`}
	site := func(content string, extra map[string]string) map[string]string {
		files := map[string]string{"/site/content.ui": content}
		for name, source := range extra {
			files[name] = source
		}
		return files
	}
	for _, c := range []struct{ content, needle, says string }{
		{`<content><button click="copy()">Chép</button>` + terminal + `</content>`, `click=`,
			`copy là tên của terminal, mà ở đây nằm ngoài <terminal> nên không đọc hay gọi được gì: đặt chỗ này bên trong <terminal>, hoặc viết <terminal as="$tên"> rồi viết $tên.copy`},
		{`<content>` + terminal + `<text>{{ copied ? 'Copied' : 'Copy' }}</text></content>`, `{{ copied`, `copied là tên của terminal`},
		{`<content>` + terminal + `<button show="copied">x</button></content>`, `show=`, `copied là tên của terminal`},
	} {
		mustFail(t, site(c.content, view), where("content.ui", c.content, c.needle), c.says)
	}
	// controls
	mustCompile(t, site(`<content><terminal><code>ls</code><button click="copy()">{{ copied ? 'Copied' : 'Copy' }}</button></terminal></content>`, view))
	mustCompile(t, site(`<content>`+terminal+`<text>{{ copied ? 'Copied' : 'Copy' }}</text></content>`,
		map[string]string{"/site/router.work": `router.get((context) => context.view({ copied: false }));`}))
	// a const of the file is not the view's: the page cannot read it, so copied there is still the terminal's
	mustFail(t, site(`<content>`+terminal+`<text>{{ copied ? 'Copied' : 'Copy' }}</text></content>`,
		map[string]string{"/site/router.work": `const copied = false;`}), "content.ui:1:", `copied là tên của terminal`)
	// what the view hands is not known here, so copied may be the view's: a .view(…) of a name or of a spread,
	// and a router.work that hands its context or its handler to code of another file, which may call .view
	stray := `<content>` + terminal + `<text>{{ copied ? 'Copied' : 'Copy' }}</text></content>`
	helper := map[string]string{"/site/page.work": "export const page = (context) => context.view({ copied: false });"}
	for name, router := range map[string]string{
		"a name":               "router.get((context) => {\n  const data = { copied: false };\n  return context.view(data);\n});",
		"a spread":             "const base = { copied: false };\nrouter.get((context) => context.view({ ...base }));",
		"the context imported": "import { page } from \"./page.work\";\nrouter.get((context) => page(context));",
		"the handler imported": "import { page as answer } from \"./page.work\";\nrouter.get(answer);",
	} {
		t.Run(name, func(t *testing.T) {
			files := map[string]string{"/site/router.work": router}
			for file, source := range helper {
				files[file] = source
			}
			mustCompile(t, site(stray, files))
		})
	}
	// CONTROL: a name of another file called with no argument hands it nothing (navigation: listNavigation()),
	// so the names stay the object's and copied is still the terminal's
	mustFail(t, site(stray, map[string]string{
		"/site/router.work": "import { listNavigation } from \"./page.work\";\nrouter.get((context) => context.view({ navigation: listNavigation() }));",
		"/site/page.work":   "export const listNavigation = () => [];",
	}), "content.ui:1:", `copied là tên của terminal`)
	mustCompile(t, site(`<content>`+terminal+`<text>{{ title }}</text><button click="track()">x</button></content>`, view))
	// an accordion's header writes click="toggle()", the accordion's own word (the browser opens
	// <details>), beside a dropdown that declares toggle: no name of the dropdown's
	mustCompile(t, site(`<content><accordion><box click="toggle()"><text>Câu hỏi</text></box><text>Trả lời</text></accordion>`+
		`<dropdown><button click="toggle()">Menu</button></dropdown></content>`, view))
}
