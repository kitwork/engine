package work

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// showWidthFiles is a page of elements whose show reads a tab, some with a width that shows them:
// #a and #b a tab's panel each, shown from medium whatever the tab (side by side on a wide screen); #c
// shown from medium and hidden again from large; #d a text with the same width, which it cannot use
// (the author is told); #e a panel with no width, the data alone.
var showWidthFiles = map[string]string{
	"localhost/router.work": `import { router } from "kitwork";

router.colors({ brand: "#c8742c" });
`,
	"localhost/page.ui": `<page><content /></page>`,
	"localhost/content.ui": `<content padding="4" gap="4">
  <box component="tabs" as="$f" alt="F" flex="row" gap="1">
    <button id="tab-a" click="select('a')">a</button>
    <button id="tab-b" click="select('b')">b</button>
  </box>
  <box id="a" show="$f.selected == 'a'" medium:show="true"><text>a</text></box>
  <box id="b" show="$f.selected == 'b'" medium:show="true"><text>b</text></box>
  <box id="c" show="$f.selected == 'b'" medium:show="true" large:show="false"><text>c</text></box>
  <text id="d" show="$f.selected == 'b'" medium:show="true">d</text>
  <box id="e" show="$f.selected == 'b'"><text>e</text></box>
</content>`,
}

// TestBrowserWidthShowsWhatALiveShowHides draws, in a real browser, what reboot.css does with a live
// show beside a width that shows the element: below the width the data decides; from it the element is
// shown whatever the data says, with its own display (a frame's flex); a later width that hides it
// (large:show="false") hides it again; a text, with no display of its own, stays hidden at every width
// while the data hides it; an element with no width is the data's alone. Read at a phone (375px), a
// tablet (800px) and a desktop (1100px), with tab a chosen (the server's first), then with b.
func TestBrowserWidthShowsWhatALiveShowHides(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the width show browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, showWidthFiles)
	var serving sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		forward := request.Clone(request.Context())
		forward.Host = "localhost"
		serving.Lock()
		defer serving.Unlock()
		tenant.Serve(response, forward)
	}))
	defer server.Close()
	d := openDevtools(t, browser, server.URL+"/", 1100, 900, "")
	d.loaded(exampleSettle)
	shown := func() string {
		return d.evaluate(`new Promise(function (done) { requestAnimationFrame(function () { requestAnimationFrame(function () {
  done(["a", "b", "c", "d", "e"].filter(function (id) { var el = document.getElementById(id); return getComputedStyle(el).display !== "none" && el.getClientRects().length > 0; })
    .map(function (id) { return id + (id === "d" ? "" : ":" + getComputedStyle(document.getElementById(id)).display); }).join(" "));
}); }); })`)
	}
	got := map[string]string{}
	for _, tab := range []string{"a", "b"} {
		if tab == "b" {
			d.evaluate(`document.getElementById("tab-b").click()`)
		}
		for _, width := range []int{375, 800, 1100} {
			emulate(d, width, 900)
			got[tab+" "+strconv.Itoa(width)] = shown()
		}
	}
	want := map[string]string{
		"a 375":  "a:flex",
		"a 800":  "a:flex b:flex c:flex",
		"a 1100": "a:flex b:flex",
		"b 375":  "b:flex c:flex d e:flex",
		"b 800":  "a:flex b:flex c:flex d e:flex",
		"b 1100": "a:flex b:flex d e:flex",
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("tab %s px: shown %q, want %q", key, got[key], value)
		}
	}
}

// widthKinds is one element of each kind, each on a line of its own, with a live show the data hides
// while tab a is chosen and a width that shows it from medium (SHOW).
var widthKinds = []struct{ id, markup string }{
	{"box", `<box id="box" SHOW><text>box</text></box>`},
	{"grid", `<grid id="grid" columns="2" SHOW><text>grid</text></grid>`},
	{"card", `<card id="card" SHOW><text>card</text></card>`},
	{"list", `<list id="list" SHOW><item>list</item></list>`},
	{"image", `<image id="image" value="data:image/png;base64,iVBORw0KGgo=" alt="image" SHOW />`},
	{"icon", `<icon id="icon" value="coffee" SHOW />`},
	{"badge", `<badge id="badge" SHOW><text>badge</text></badge>`},
	{"button", `<button id="button" click="$f.select('a')" SHOW>button</button>`},
	{"link", `<link id="link" value="/x" SHOW>link</link>`},
	{"link-row", `<link id="link-row" value="/x" flex="row" SHOW>link</link>`},
	{"link-icon", `<link id="link-icon" value="/x" SHOW><icon value="coffee" /><text>link</text></link>`},
	{"input", `<input id="input" name="q" alt="Tìm" SHOW />`},
	{"select", `<select id="select" name="s" alt="Chọn" SHOW><option value="1">1</option></select>`},
	{"textarea", `<textarea id="textarea" name="t" alt="Ghi chú" SHOW />`},
	{"label", `<label id="label" SHOW><text>label</text></label>`},
	{"time", `<time id="time" value="2026-10-16" SHOW>time</time>`},
	{"text", `<text id="text" SHOW>text</text>`},
	{"text-lines", `<text id="text-lines" lines="2" SHOW>text</text>`},
	{"heading", `<text id="heading" heading="2" SHOW>heading</text>`},
}

// widthWarnedRe finds, in a page's warnings file, the line of each width that does nothing beside a
// live show.
var widthWarnedRe = regexp.MustCompile(`content\.ui:(\d+):\d+: medium:show=\\?"true\\?" bên cạnh show từ dữ liệu không hiện lại`)

// TestBrowserWidthShowWarnsWhereItStaysHidden holds the compiler's warning to what a real browser does:
// with every kind of element written with a live show the data hides and medium:show="true", what the
// page's warnings name is exactly what stays hidden at 1100px — a link of words, a field, a label, a
// time, a text, a heading — and every other kind (frames, an image, an icon, a badge, a button, a link
// holding elements or written with flex, a text with lines) is shown there by its own display.
// CONTROLS: at 375px the data decides and every one is hidden; with b chosen every one is shown at
// 1100px, the warned ones too (the data still shows them).
func TestBrowserWidthShowWarnsWhereItStaysHidden(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the width show browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	lines := []string{
		`<content padding="4" gap="4">`,
		`<box component="tabs" as="$f" alt="F" flex="row" gap="1"><button id="tab-a" click="select('a')">a</button><button id="tab-b" click="select('b')">b</button></box>`,
		`<box show="$f.selected == 'a'"><text>a</text></box>`,
	}
	atLine := map[int]string{}
	var ids []string
	for _, kind := range widthKinds {
		lines = append(lines, strings.Replace(kind.markup, "SHOW", `show="$f.selected == 'b'" medium:show="true"`, 1))
		atLine[len(lines)] = kind.id
		ids = append(ids, kind.id)
	}
	lines = append(lines, `</content>`)
	tenant := buildUISite(t, map[string]string{
		"localhost/router.work": showWidthFiles["localhost/router.work"],
		"localhost/page.ui":     showWidthFiles["localhost/page.ui"],
		"localhost/content.ui":  strings.Join(lines, "\n"),
	})
	rec := serveUI(t, tenant, "http://localhost/")
	link := warningsScriptRe.FindStringSubmatch(rec.Body.String())
	if rec.Code != http.StatusOK || link == nil {
		t.Fatalf("GET / = %d with no warnings file:\n%.1500s", rec.Code, rec.Body.String())
	}
	var warned []string
	for _, m := range widthWarnedRe.FindAllStringSubmatch(serveUI(t, tenant, "http://localhost"+link[1]).Body.String(), -1) {
		line, _ := strconv.Atoi(m[1])
		warned = append(warned, atLine[line])
	}
	sort.Strings(warned)

	var serving sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		forward := request.Clone(request.Context())
		forward.Host = "localhost"
		serving.Lock()
		defer serving.Unlock()
		tenant.Serve(response, forward)
	}))
	defer server.Close()
	d := openDevtools(t, browser, server.URL+"/", 1100, 900, "")
	d.loaded(exampleSettle)
	hidden := func() []string {
		got := d.evaluate(`new Promise(function (done) { requestAnimationFrame(function () { requestAnimationFrame(function () {
  done(` + jsString(strings.Join(ids, " ")) + `.split(" ").filter(function (id) { return getComputedStyle(document.getElementById(id)).display === "none"; }).sort().join(" "));
}); }); })`)
		if got == "" {
			return nil
		}
		return strings.Split(got, " ")
	}
	all := append([]string(nil), ids...)
	sort.Strings(all)
	emulate(d, 1100, 900)
	wide := hidden()
	emulate(d, 375, 900)
	narrow := hidden()
	d.evaluate(`document.getElementById("tab-b").click()`)
	emulate(d, 1100, 900)
	chosen := hidden()

	if len(warned) == 0 || strings.Join(warned, " ") != strings.Join(wide, " ") {
		t.Errorf("what the page warns of is what stays hidden from medium:\n warned %q\n hidden %q", warned, wide)
	}
	for _, id := range []string{"link", "input", "text", "heading"} {
		if !containsString(warned, id) {
			t.Errorf("%s stays hidden from medium and is not warned of: warned %q", id, warned)
		}
	}
	for _, id := range []string{"box", "grid", "image", "icon", "badge", "button", "link-row", "link-icon", "text-lines"} {
		if containsString(wide, id) {
			t.Errorf("%s has a display of its own and stays hidden at 1100px: hidden %q", id, wide)
		}
	}
	if strings.Join(narrow, " ") != strings.Join(all, " ") {
		t.Errorf("at 375px the data hides every one, hidden %q", narrow)
	}
	if len(chosen) != 0 {
		t.Errorf("with b chosen every one is shown at 1100px, hidden %q", chosen)
	}
}
