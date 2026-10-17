package ui

import (
	"crypto/sha256"
	"encoding/base64"
	"regexp"
	"strings"
	"testing"
)

// tabsWindow is a file-tabbed terminal: the tabs in the terminal's own bar, the Copy button beside
// them, a panel of code for each tab, the second panel written BEFORE the bar so a panel is wired
// wherever it stands.
const tabsWindow = `<content>
<box component="terminal" as="$code" typing="800ms" background="panel">
  <box show="$files.selected == 'page'"><code language="ui">page</code></box>
  <box flex="row" gap="2">
    <box component="tabs" as="$files" persist="true" alt="Files" flex="row" gap="1"TABS_INPUT>
      <button click="select('router')">router.work</button>
      <button click="select('page')">page.ui</button>
    </box>
    <button click="copy()">Copy</button>
  </box>
  <box show="$files.selected == 'router'"><code language="work">router.get()</code></box>
  <text>{{ $files.selected }}</text>
</box>
</content>`

func tabsCompile(t *testing.T, source string) *Page {
	t.Helper()
	s := memorySite("", map[string]string{"/site/content.ui": source})
	s.Reload = true
	page, err := s.Compile("/site")
	if err != nil {
		t.Fatalf("compile: %v\n%s", err, source)
	}
	return page
}

// The engine writes the tabs' accessibility itself, from the markup the author writes anyway: the host
// is the tablist; a button inside it whose click is select('<name>') is a tab — role, its name as value,
// aria-selected and tabindex bound to the choice; an element whose show compares the alias's selected
// with a tab's name is that tab's panel — role, tabindex 0, and the two ids that tie them — wherever it
// stands (the page panel is written before the bar). The server paints the first tab chosen and its
// panel shown, the others hidden: the instance starts from selected: 'router' (data-kitwork-scope), and
// $files.selected reads that on the server, outside the host too. persist="true" hands the instance the
// name its choice is kept under: the set of its tab names, sorted, page+router. A file's code in a
// terminal has no prompt.
// CONTROLS: the Copy button beside the host is no tab; selected="page" on the call chooses the second
// tab instead.
func TestTabsWireTheTablistAndItsPanels(t *testing.T) {
	page := tabsCompile(t, strings.Replace(tabsWindow, "TABS_INPUT", "", 1))
	html := page.Render(Request{Path: "/"})
	mustContain(t, html,
		`data-kitwork-component="tabs: $files" role="tablist" aria-label="Files" data-kitwork-scope="persist: &#39;page+router&#39;; selected: &#39;router&#39;"`,
		`id="kitwork-tab-1-1" type="button" role="tab" aria-controls="kitwork-tabpanel-1-1" value="router" data-kitwork-bind="aria-selected: selected == &#39;router&#39;; tabindex: selected == &#39;router&#39; ? 0 : -1" data-kitwork-event="click: select(&#39;router&#39;)" aria-selected="true" tabindex="0">router.work</button>`,
		`id="kitwork-tab-1-2" type="button" role="tab" aria-controls="kitwork-tabpanel-1-2" value="page" data-kitwork-bind="aria-selected: selected == &#39;page&#39;; tabindex: selected == &#39;page&#39; ? 0 : -1" data-kitwork-event="click: select(&#39;page&#39;)" aria-selected="false" tabindex="-1">page.ui</button>`,
		`<div class="flex-column" id="kitwork-tabpanel-1-2" role="tabpanel" aria-labelledby="kitwork-tab-1-2" tabindex="0" data-kitwork-bind="show: $files.selected == &#39;page&#39;" hidden><pre><code class="language-ui">`,
		`<div class="flex-column" id="kitwork-tabpanel-1-1" role="tabpanel" aria-labelledby="kitwork-tab-1-1" tabindex="0" data-kitwork-bind="show: $files.selected == &#39;router&#39;"><pre><code class="language-work">`,
		`<p data-kitwork-bind="text: $files.selected">router</p>`)
	// CONTROL: copy() is a button beside the host, no tab
	mustContain(t, html, `</div><button class="flex-row background-ink/6 hover:background-ink/10 pressed:background-ink/14 disabled:background-ink/4" type="button" data-kitwork-event="click: copy()">Copy</button>`)
	if got := strings.Count(html, `role="tab"`); got != 2 {
		t.Errorf("two tabs, got %d:\n%s", got, html)
	}
	mustNotContain(t, html, "code-prompt")
	if w := page.Warnings(); len(w) != 0 {
		t.Errorf("no warning, got %q", w)
	}

	// CONTROL: the call chooses the second tab
	chosen := tabsCompile(t, strings.Replace(tabsWindow, "TABS_INPUT", ` selected="page"`, 1)).Render(Request{Path: "/"})
	mustContain(t, chosen,
		`data-kitwork-scope="selected: &#39;page&#39;; persist: &#39;page+router&#39;"`,
		`value="page" data-kitwork-bind="aria-selected: selected == &#39;page&#39;; tabindex: selected == &#39;page&#39; ? 0 : -1" data-kitwork-event="click: select(&#39;page&#39;)" aria-selected="true" tabindex="0">`,
		`aria-labelledby="kitwork-tab-1-1" tabindex="0" data-kitwork-bind="show: $files.selected == &#39;router&#39;" hidden>`,
		`aria-labelledby="kitwork-tab-1-2" tabindex="0" data-kitwork-bind="show: $files.selected == &#39;page&#39;"><pre>`,
		`<p data-kitwork-bind="text: $files.selected">page</p>`)
}

// A tabs that persists has its pre-paint right after the element of its name that starts last — here
// the router panel, written after the bar — carrying the name and its tabs by id; its body is one
// constant whose hash is TabsPrepaintHash, what a policy allows.
// CONTROL: a tabs that keeps nothing has none.
func TestTabsPrepaintFollowsTheGroupsLastPanel(t *testing.T) {
	html := tabsCompile(t, strings.Replace(tabsWindow, "TABS_INPUT", "", 1)).Render(Request{Path: "/"})
	at := strings.Index(html, `id="kitwork-tabpanel-1-1"`)
	if at < 0 {
		t.Fatalf("no router panel:\n%s", html)
	}
	end := at + strings.Index(html[at:], "</div>") + len("</div>")
	script := regexp.MustCompile(`^<script data-kitwork-generated="tabs" data-kitwork-persist="page\+router" data-kitwork-tabs="kitwork-tab-1-1 kitwork-tab-1-2">([\s\S]*?)</script>`).FindStringSubmatch(html[end:])
	if script == nil {
		t.Fatalf("no pre-paint right after the router panel:\n%.400s", html[end:])
	}
	if got := strings.Count(html, `data-kitwork-generated="tabs"`); got != 1 {
		t.Errorf("one pre-paint for the group, got %d", got)
	}
	sum := sha256.Sum256([]byte(script[1]))
	if hash := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"; hash != TabsPrepaintHash() || !CarriesTabsPrepaint(html) {
		t.Errorf("the pre-paint's hash is %s, TabsPrepaintHash says %s", hash, TabsPrepaintHash())
	}
	// CONTROL: nothing kept, nothing to paint before the kernel
	plain := tabsCompile(t, strings.Replace(strings.Replace(tabsWindow, "TABS_INPUT", "", 1), ` persist="true"`, "", 1)).Render(Request{Path: "/"})
	mustContain(t, plain, `id="kitwork-tabpanel-1-1"`)
	mustNotContain(t, plain, "<script")
}

// A tab's look written with selected: follows the tab chosen with no expression: the class is the
// attribute as written, its CSS holds while aria-selected is true (what the engine binds and the
// pre-paint sets), and its colour is measured in that state like any other.
// CONTROL: the same colour without the prefix is not under aria-selected.
func TestSelectedStateFollowsTheChosenTab(t *testing.T) {
	source := `<content>
<box component="tabs" as="$files" alt="Files" flex="row">
  <button click="select('a')" color="ink-30" selected:color="ink" selected:underline="true">a</button>
  <button click="select('b')" color="ink-30" selected:color="SELECTED">b</button>
</box>
<box show="$files.selected == 'a'"><text>a</text></box>
<box show="$files.selected == 'b'"><text>b</text></box>
</content>`
	page := tabsCompile(t, strings.Replace(source, "SELECTED", "ink", 1))
	html, css := page.Render(Request{Path: "/"}), string(page.Stylesheet().CSS)
	mustContain(t, html, `color-ink-30 selected:color-ink selected:underline-true" id="kitwork-tab-1-1"`)
	mustContain(t, css, `.selected\:color-ink:where([aria-selected=true]){color:var(--ink)}`,
		`.selected\:underline-true:where([aria-selected=true]){text-decoration-line:underline}`)
	mustNotContain(t, css, `.color-ink-30:where([aria-selected`)
	if w := page.Warnings(); len(w) != 0 {
		t.Errorf("no warning, got %q", w)
	}
	short := tabsCompile(t, strings.Replace(source, "SELECTED", "ink-80", 1))
	at := where("content.ui", source, `selected:color="SELECTED"`)
	if w := short.Warnings(); len(w) != 1 || !strings.HasPrefix(w[0].String(), at) || !strings.Contains(w[0].String(), `selected:color="ink-80"`) {
		t.Errorf("want the selected colour short of contrast told at %s, got %q", at, w)
	}
}

// A tabs in a component's own .ui is painted by the server as one on the page is: its alias reads, in
// the instance, the instance as it starts, so the first tab's panel is shown and the other hidden.
// CONTROLS: the same window in content.ui, and in a layout called by its tag.
func TestTabsInAComponentsOwnUIPaintTheFirstTab(t *testing.T) {
	window := `<box component="terminal" typing="2s">
  <box flex="row">
    <box component="tabs" as="$files" alt="Files" flex="row">
      <button click="select('router')">router.work</button>
      <button click="select('page')">page.ui</button>
    </box>
    <button click="copy()">Copy</button>
  </box>
  <box show="$files.selected == 'router'"><code language="work">router</code></box>
  <box show="$files.selected == 'page'"><code language="ui">page</code></box>
</box>`
	panels := regexp.MustCompile(`<div[^>]*role="tabpanel"[^>]*>`)
	for name, files := range map[string]map[string]string{
		"content.ui": {"/site/content.ui": `<content>` + window + `</content>`},
		"layout":     {"/site/content.ui": `<content><files-window /></content>`, "/.components/files-window.ui": "<box>" + window + "</box>"},
		"component": {"/site/content.ui": `<content><box component="code-window" /></content>`, "/.components/code-window.ui": "<box>" + window + "</box>",
			"/.components/code-window.work": `import { component } from "kitwork"; component.define("code-window", () => { var note = 1; });`},
	} {
		s := memorySite("", files)
		s.Reload = true
		page, err := s.Compile("/site")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := panels.FindAllString(page.Render(Request{Path: "/"}), -1)
		if len(got) != 2 || strings.HasSuffix(got[0], " hidden>") || !strings.HasSuffix(got[1], " hidden>") {
			t.Errorf("%s: the router panel shown and the page panel hidden, got %q", name, got)
		}
		if w := page.Warnings(); len(w) != 0 {
			t.Errorf("%s: no warning, got %q", name, w)
		}
	}
}

// An alias of a component the browser runs reads, on the server, the instance as it starts — its
// declared state, then what its call passes in — wherever the expression stands, before the host or
// after it: what the kernel leaves there once it runs. CONTROL: the page's own $-name is the page's.
func TestAliasReadsTheInstanceAsItStarts(t *testing.T) {
	source := `<content>
<text>{{ $install.copied }} {{ $menu.open }}</text>
<box component="terminal" as="$install"><code>ls</code></box>
<box component="dropdown" as="$menu"><button click="toggle()">Mở</button></box>
<text>{{ $install.typing }}</text>
</content>`
	html := tabsCompile(t, source).Render(Request{Path: "/"})
	mustContain(t, html, `data-kitwork-bind="text: [($install.copied), &#39; &#39;, ($menu.open)].join(&#39;&#39;)">false false</p>`, `>0</p>`)
	// CONTROL: a $-name the page declares itself is the page's
	page := tabsCompile(t, `<content><text>{{ $install.copied }}</text><box component="terminal" as="$install"><code>ls</code></box></content>`).
		Render(Request{Path: "/", Scope: map[string]any{"$install": map[string]any{"copied": "của trang"}}})
	mustContain(t, page, ">của trang</p>")
}

// What does not fit the tabs is told where it is written: errors for what the engine cannot wire, a
// warning for what renders but probably is not what the author meant.
func TestTabsSayWhatDoesNotFit(t *testing.T) {
	panels := `<box show="$files.selected == 'a'"><text>a</text></box><box show="$files.selected == 'b'"><text>b</text></box>`
	for _, c := range []struct{ source, needle, says string }{
		{`<content><box component="tabs" as="$files"><button click="toggle()">a</button></box></content>`, `<box`,
			`tabs cần ít nhất một tab bên trong thẻ chủ: <button click="select('tên')">…</button>`},
		{`<content><box component="tabs" as="$files"><button click="select('a')">a</button><button click="select('a')">b</button></box>` + panels + `</content>`, `click="select('a')">b`,
			`tab a đã có ở dòng 1: mỗi tab một tên`},
		{`<content><box component="tabs" as="$files"><box for="name of names"><button click="select('a')">a</button></box></box>` + panels + `</content>`, `click="select('a')"`,
			`tab a nằm trong for`},
		{`<content><box component="tabs" as="$files"><button click="select('a')" value="x">a</button></box>` + panels + `</content>`, `value="x"`,
			`value của một tab là tên của nó, engine viết từ select('a'): bỏ value`},
		{`<content><box component="tabs" as="$files" selected="c"><button click="select('a')">a</button><button click="select('b')">b</button></box>` + panels + `</content>`, `selected="c"`,
			`selected="c" không phải tên của tab nào (a, b)`},
		{`<content><box component="tabs" as="$files"><button click="select('a')">a</button></box><code show="$files.selected == 'a'">a</code></content>`, `show=`,
			`<code> không làm panel của tab a được`},
		{`<content><box component="tabs" as="$files"><button click="select('a')">a</button></box><box for="x of xs" show="$files.selected == 'a'"><text>a</text></box></content>`, `show=`,
			`panel của tab a nằm trong for`},
	} {
		mustFail(t, map[string]string{"/site/content.ui": c.source}, where("content.ui", c.source, c.needle), c.says)
	}
	for _, c := range []struct{ source, needle, says string }{
		{`<content><box component="tabs" as="$files"><button click="select('a')">a</button><button click="select('b')">b</button></box><box show="$files.selected == 'a'"><text>a</text></box></content>`,
			`click="select('b')"`, `tab b của $files không có panel: viết show="$files.selected == 'b'" trên phần tử nó cho hiện`},
		{`<content><box component="tabs" as="$files"><button click="select('a')">a</button></box><box show="$files.selected == 'a'"><text>a</text></box><box show="'c' === $files.selected"><text>c</text></box></content>`,
			`show="'c'`, `show so $files.selected với 'c', mà $files không có tab c (a): phần tử này không bao giờ hiện`},
		{`<content><box component="tabs"><button click="select('a')">a</button></box></content>`,
			`component="tabs"`, `tabs không có as: ngoài thẻ chủ không gì đọc được tab đang chọn`},
		{`<content><box component="tabs" as="$files" selected="a"><button click="select('a')">a</button><button click="select(selected == 'a' ? 'b' : 'a')">b</button></box><box show="$files.selected == 'a'"><text>a</text></box></content>`,
			`click="select(selected`, `engine nhận một nút là tab khi click là select('tên') với tên viết sẵn`},
		// a tablist holds tabs only: what a person can press inside it that is no tab is told where it is
		{`<content><box component="terminal"><box component="tabs" as="$files"><button click="select('a')">a</button><button click="copy()">Copy</button></box><box show="$files.selected == 'a'"><code language="work">a</code></box></box></content>`,
			`<button click="copy()"`, `<button> trong thẻ chủ của tabs không phải tab: dải tab chỉ chứa tab (role="tablist")`},
		{`<content><box component="tabs" as="$files"><button click="select('a')">a</button><box><link value="/x">x</link></box></box><box show="$files.selected == 'a'"><text>a</text></box></content>`,
			`<link`, `<link> trong thẻ chủ của tabs không phải tab: dải tab chỉ chứa tab (role="tablist")`},
	} {
		page := tabsCompile(t, c.source)
		at := where("content.ui", c.source, c.needle)
		found := false
		for _, w := range page.Warnings() {
			found = found || strings.HasPrefix(w.String(), at) && strings.Contains(w.String(), c.says)
		}
		if !found || len(page.Warnings()) != 1 {
			t.Errorf("want one warning at %q saying %q, got %q", at, c.says, page.Warnings())
		}
	}
}

// persist="true" keeps the choice under the SET of the tab names — sorted, joined by "+" — so two tabs
// with the same tabs written in another order, anywhere on the site, keep it under one name, and share
// one pre-paint on a page, and start on one tab ($two, written bash first, starts on powershell as $one
// does); another set is another name. persist="shell" is that name, whatever the tabs. persist="false"
// is the same as not writing it: the same HTML to the byte, nothing kept.
func TestTabsPersistUnderTheSetOfTheirNames(t *testing.T) {
	tabs := func(alias, persist string, names ...string) string {
		s := `<box component="tabs" as="` + alias + `"` + persist + ` alt="Shell" flex="row">`
		for _, name := range names {
			s += `<button click="select('` + name + `')">` + name + `</button>`
		}
		s += `</box>`
		for _, name := range names {
			s += `<box show="` + alias + `.selected == '` + name + `'"><text>` + name + `</text></box>`
		}
		return s
	}
	page := tabsCompile(t, `<content>`+
		tabs("$one", ` persist="true"`, "powershell", "bash")+
		tabs("$two", ` persist="true"`, "bash", "powershell")+
		tabs("$three", ` persist="true"`, "bash", "powershell", "cmd")+
		tabs("$four", ` persist="shell"`, "bash", "cmd")+`</content>`)
	html := page.Render(Request{Path: "/"})
	mustContain(t, html,
		`data-kitwork-component="tabs: $one" role="tablist" aria-label="Shell" data-kitwork-scope="persist: &#39;bash+powershell&#39;; selected: &#39;powershell&#39;"`,
		`data-kitwork-component="tabs: $two" role="tablist" aria-label="Shell" data-kitwork-scope="persist: &#39;bash+powershell&#39;; selected: &#39;powershell&#39;"`,
		`data-kitwork-component="tabs: $three" role="tablist" aria-label="Shell" data-kitwork-scope="persist: &#39;bash+cmd+powershell&#39;; selected: &#39;bash&#39;"`,
		`data-kitwork-component="tabs: $four" role="tablist" aria-label="Shell" data-kitwork-scope="persist: &#39;shell&#39;; selected: &#39;bash&#39;"`,
		`<script data-kitwork-generated="tabs" data-kitwork-persist="bash+powershell" data-kitwork-tabs="kitwork-tab-1-1 kitwork-tab-1-2 kitwork-tab-2-1 kitwork-tab-2-2">`,
		`<script data-kitwork-generated="tabs" data-kitwork-persist="bash+cmd+powershell" data-kitwork-tabs="kitwork-tab-3-1 kitwork-tab-3-2 kitwork-tab-3-3">`,
		`<script data-kitwork-generated="tabs" data-kitwork-persist="shell" data-kitwork-tabs="kitwork-tab-4-1 kitwork-tab-4-2">`)
	if got := strings.Count(html, `data-kitwork-generated="tabs"`); got != 3 {
		t.Errorf("one pre-paint for each name, three, got %d", got)
	}
	// the pre-paint of the two that share a name follows the last of them, $two's last panel
	at := strings.Index(html, `data-kitwork-persist="bash+powershell"`)
	if before := html[:at]; !strings.HasSuffix(before, `<p>powershell</p></div><script data-kitwork-generated="tabs" `) || strings.Count(before, `role="tablist"`) != 2 {
		t.Errorf("the shared pre-paint is not right after $two's last panel:\n%.300s", html[at-300:])
	}
	if w := page.Warnings(); len(w) != 0 {
		t.Errorf("no warning, got %q", w)
	}

	// CONTROL: persist="false" writes exactly what no persist writes: nothing kept, no pre-paint
	none := tabsCompile(t, `<content>`+tabs("$one", "", "bash", "powershell")+`</content>`).Render(Request{Path: "/"})
	off := tabsCompile(t, `<content>`+tabs("$one", ` persist="false"`, "bash", "powershell")+`</content>`).Render(Request{Path: "/"})
	if off != none || strings.Contains(none, "persist") || strings.Contains(none, "<script") {
		t.Errorf("persist=\"false\" must be the same as none, with nothing kept:\n false %s\n none  %s", off, none)
	}
}

// What persist does not take is told at the attribute, with what to write: remember (the word before),
// a value that is neither true, false nor a name, one from {{ }}, a prefix, no value. A name with
// letters that carry marks (lựa chọn, über, Đường) is suggested without them, not cut where they stood.
func TestTabsPersistSaysWhatToWrite(t *testing.T) {
	host := func(attr string) string {
		return `<content><box component="tabs" as="$files"` + attr + `><button click="select('a')">a</button></box><box show="$files.selected == 'a'"><text>a</text></box></content>`
	}
	for _, c := range []struct{ attr, needle, says string }{
		{` remember="files"`, `remember=`, `remember đã đổi thành persist (16/10/2026): viết persist="true" để mọi tabs có cùng các tab trên site đi theo một lựa chọn, hay persist="files" để giữ lựa chọn dưới tên này`},
		{` remember="{{ group }}"`, `remember=`, `hay persist="tên" để giữ lựa chọn dưới tên này`},
		{` persist="yes"`, `persist=`, `persist="yes" không phải một giá trị của persist: muốn giữ lựa chọn thì viết persist="true"`},
		{` persist="off"`, `persist=`, `persist="off" không phải một giá trị của persist: không muốn giữ thì bỏ persist (hay persist="false")`},
		{` persist="True"`, `persist=`, `persist="True": viết chữ thường, persist="true"`},
		{` persist="my files"`, `persist=`, `persist="my files": tên giữ lựa chọn bắt đầu bằng một chữ cái không dấu (a–z), rồi chữ không dấu, số, '-', '_', '.'; viết persist="my-files" (hay persist="true")`},
		{` persist="+"`, `persist=`, `persist="+": tên giữ lựa chọn bắt đầu bằng một chữ cái không dấu (a–z), rồi chữ không dấu, số, '-', '_', '.'; viết persist="true"`},
		// a letter with marks loses them, written as one letter or as a letter and its mark
		{` persist="lựa chọn"`, `persist=`, `persist="lựa chọn": tên giữ lựa chọn bắt đầu bằng một chữ cái không dấu (a–z), rồi chữ không dấu, số, '-', '_', '.'; viết persist="lua-chon" (hay persist="true")`},
		{` persist="Đường über"`, `persist=`, `viết persist="Duong-uber" (hay persist="true")`},
		{" persist=\"chọn 2\"", `persist=`, `viết persist="chon-2" (hay persist="true")`},
		{` persist="{{ keep }}"`, `persist=`, `persist viết sẵn, không đọc từ {{ }}`},
		{` medium:persist="true"`, `medium:persist=`, `persist của tabs không nhận tiền tố`},
		{` persist`, `persist`, `persist cần một giá trị`},
	} {
		source := host(c.attr)
		mustFail(t, map[string]string{"/site/content.ui": source}, where("content.ui", source, c.needle), c.says)
	}
	// CONTROL: a name of letters, digits, '-', '_' and '.' is one
	if html := tabsCompile(t, host(` persist="shell-2.lang_x"`)).Render(Request{Path: "/"}); !strings.Contains(html, `data-kitwork-persist="shell-2.lang_x"`) {
		t.Errorf("a name is kept under itself:\n%s", html)
	}
}

// A component whose own .ui holds a tabs, used twice on a page, is two tabs: each instance's alias is
// its own, so each instance's panels are wired to its own tabs — ids of their own (kitwork-tab-2-… and
// kitwork-tab-3-…), each panel labelled by its own instance's tab, each tab controlling its own panels —
// with no warning; and, persisted, the one pre-paint of the name lists every instance's tabs, so it
// paints both before the kernel runs. CONTROL: the page's own $files, the same alias as the instances',
// is the page's: its panels are wired to the page's tabs.
func TestTabsInAComponentUsedTwiceWireEachInstance(t *testing.T) {
	window := `<box component="tabs" as="$files"PERSIST alt="Files" flex="row">
  <button click="select('router')">router.work</button>
  <button click="select('page')">page.ui</button>
</box>
<box show="$files.selected == 'router'"><text>router</text></box>
<box show="$files.selected == 'page'"><text>page</text></box>`
	page := `<content>
<box component="tabs" as="$files" alt="Page" flex="row"><button click="select('a')">a</button><button click="select('b')">b</button></box>
<box show="$files.selected == 'b'"><text>b</text></box>
<box component="code-window" />
<box component="code-window" />
<box show="$files.selected == 'a'"><text>a</text></box>
</content>`
	tab := regexp.MustCompile(`id="(kitwork-tab-[0-9-]+)" type="button" role="tab" aria-controls="([^"]*)" value="([a-z]+)"`)
	panel := regexp.MustCompile(`id="(kitwork-tabpanel-[0-9-]+)" role="tabpanel" aria-labelledby="([^"]*)" tabindex="0" data-kitwork-bind="show: \$files.selected == &#39;([a-z]+)&#39;"( hidden)?>`)
	for _, persist := range []string{"", ` persist="true"`} {
		s := memorySite("", map[string]string{
			"/site/content.ui":              page,
			"/.components/code-window.ui":   "<box>" + strings.Replace(window, "PERSIST", persist, 1) + "</box>",
			"/.components/code-window.work": `import { component } from "kitwork"; component.define("code-window", () => { var note = 1; });`,
		})
		s.Reload = true
		compiled, err := s.Compile("/site")
		if err != nil {
			t.Fatalf("persist %q: %v", persist, err)
		}
		html := compiled.Render(Request{Path: "/"})
		var tabs, panels []string
		for _, m := range tab.FindAllStringSubmatch(html, -1) {
			tabs = append(tabs, m[1]+">"+m[2]+" "+m[3])
		}
		for _, m := range panel.FindAllStringSubmatch(html, -1) {
			panels = append(panels, m[1]+"<"+m[2]+" "+m[3]+m[4])
		}
		wantTabs := "kitwork-tab-1-1>kitwork-tabpanel-1-1 a | kitwork-tab-1-2>kitwork-tabpanel-1-2 b | " +
			"kitwork-tab-2-1>kitwork-tabpanel-2-1 router | kitwork-tab-2-2>kitwork-tabpanel-2-2 page | " +
			"kitwork-tab-3-1>kitwork-tabpanel-3-1 router | kitwork-tab-3-2>kitwork-tabpanel-3-2 page"
		wantPanels := "kitwork-tabpanel-1-2<kitwork-tab-1-2 b hidden | " +
			"kitwork-tabpanel-2-1<kitwork-tab-2-1 router | kitwork-tabpanel-2-2<kitwork-tab-2-2 page hidden | " +
			"kitwork-tabpanel-3-1<kitwork-tab-3-1 router | kitwork-tabpanel-3-2<kitwork-tab-3-2 page hidden | " +
			"kitwork-tabpanel-1-1<kitwork-tab-1-1 a"
		if got := strings.Join(tabs, " | "); got != wantTabs {
			t.Errorf("persist %q: tabs\n got %s\nwant %s", persist, got, wantTabs)
		}
		if got := strings.Join(panels, " | "); got != wantPanels {
			t.Errorf("persist %q: panels\n got %s\nwant %s", persist, got, wantPanels)
		}
		if w := compiled.Warnings(); len(w) != 0 {
			t.Errorf("persist %q: no warning, got %q", persist, w)
		}
		scripts := regexp.MustCompile(`<script data-kitwork-generated="tabs" data-kitwork-persist="([^"]*)" data-kitwork-tabs="([^"]*)">`).FindAllStringSubmatch(html, -1)
		switch {
		case persist == "" && len(scripts) != 0:
			t.Errorf("no persist, no pre-paint, got %q", scripts)
		case persist != "" && (len(scripts) != 1 || scripts[0][1] != "page+router" || scripts[0][2] != "kitwork-tab-2-1 kitwork-tab-2-2 kitwork-tab-3-1 kitwork-tab-3-2"):
			t.Errorf("one pre-paint of page+router listing both instances' tabs, got %q", scripts)
		}
	}
}

// Tabs that keep their choice under one name follow one choice, and so start on one tab: a tabs that
// persists under a name an earlier tabs of the page has, and chose nothing, starts on the tab that one
// starts on when it has it — the server paints it chosen, its panels shown, the kernel starts from it —
// so the page agrees from its first frame, before anything is kept.
// CONTROLS: a tabs that chose (selected="powershell") keeps its choice; one that persists nothing, or
// under another name, starts on its own first tab; one under the same name without the tab the earlier
// starts on starts on its own first.
func TestTabsThatShareANameStartTogether(t *testing.T) {
	tabs := func(alias, words string, names ...string) string {
		s := `<box component="tabs" as="` + alias + `"` + words + ` alt="Shell" flex="row">`
		for _, name := range names {
			s += `<button click="select('` + name + `')">` + name + `</button>`
		}
		s += `</box>`
		for _, name := range names {
			s += `<box show="` + alias + `.selected == '` + name + `'"><text>` + alias + " " + name + `</text></box>`
		}
		return s
	}
	page := tabsCompile(t, `<content>`+
		tabs("$a", ` persist="true"`, "bash", "powershell")+
		tabs("$b", ` persist="true"`, "powershell", "bash")+
		tabs("$c", ` persist="true" selected="powershell"`, "bash", "powershell")+
		tabs("$d", ``, "powershell", "bash")+
		tabs("$e", ` persist="true"`, "powershell", "bash", "cmd")+
		tabs("$f", ` persist="shell"`, "cmd", "powershell")+
		tabs("$g", ` persist="shell"`, "bash", "cmd")+
		tabs("$h", ` persist="shell"`, "bash", "powershell")+`</content>`)
	html := page.Render(Request{Path: "/"})
	scope := regexp.MustCompile(`data-kitwork-component="tabs: (\$[a-z])"[^>]*data-kitwork-scope="([^"]*)"`)
	var got []string
	for _, m := range scope.FindAllStringSubmatch(html, -1) {
		selected := regexp.MustCompile(`selected: &#39;([a-z]+)&#39;`).FindStringSubmatch(m[2])
		got = append(got, m[1]+":"+selected[1])
	}
	if want := "$a:bash $b:bash $c:powershell $d:powershell $e:powershell $f:cmd $g:cmd $h:bash"; strings.Join(got, " ") != want {
		t.Errorf("tabs start on\n got %s\nwant %s", strings.Join(got, " "), want)
	}
	// $b, written powershell first, is painted on bash like $a: its bash tab chosen, its bash panel shown
	mustContain(t, html,
		`value="bash" data-kitwork-bind="aria-selected: selected == &#39;bash&#39;; tabindex: selected == &#39;bash&#39; ? 0 : -1" data-kitwork-event="click: select(&#39;bash&#39;)" aria-selected="true" tabindex="0">bash</button></div><div class="flex-column" id="kitwork-tabpanel-2-1"`,
		`data-kitwork-bind="show: $b.selected == &#39;powershell&#39;" hidden><p>$b powershell</p>`,
		`data-kitwork-bind="show: $b.selected == &#39;bash&#39;"><p>$b bash</p>`)
	if w := page.Warnings(); len(w) != 0 {
		t.Errorf("no warning, got %q", w)
	}
}
