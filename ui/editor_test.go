package ui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/kitwork/engine/utilities/richtext"
)

// The editor (20/10/2026, editor.go) is written on a box the author fills: the toolbars, the link's
// popover and one <content> that is the writing area. These tests read what the compiler writes for
// each part (the wire the editor's component reads), the first paint of the value cleaned, the
// field's wiring, and what it refuses and warns of, each at its place with its exact words.

// editorBar is a toolbar of a format and the link; editorLink the link's popover; editorArea a writing
// area named by its alt.
const (
	editorBar  = `<box alt="Định dạng"><button click="toggle('bold')"><icon value="bold" alt="Đậm" /></button><button click="link()"><icon value="link" alt="Liên kết" /></button></box>`
	editorLink = `<popover><input value="{{ address }}" alt="Địa chỉ liên kết" /><button click="link(address)">Gắn</button><button click="close()">Huỷ</button></popover>`
	editorArea = `<content name="body" alt="Nội dung" />`
)

// editorIn is a content.ui with an editor written with words and what is inside it, in a post form.
func editorIn(words, inside string) string {
	return `<content><form method="post"><editor` + words + `>` + inside + `</editor></form></content>`
}

// editorMenuIn is a content.ui with an editor as $body whose toolbar holds bold and a dropdown written
// with what is inside it.
func editorMenuIn(inside string) string {
	return `<content><form method="post"><editor as="$body"><box alt="F"><button click="toggle('bold')">B</button><dropdown>` + inside +
		`</dropdown></box>` + editorArea + `</editor></form></content>`
}

// editorPage compiles content.ui (no progressbar) and renders "/" with data, by method; it returns
// the page and the warnings, one line each.
func editorPage(t *testing.T, source, method string, data map[string]any) (string, []string) {
	t.Helper()
	s := memorySite("", content(source))
	s.Reload = true
	page, warned := compileDiagnose(t, s)
	return page.Render(Request{Path: "/", Method: method, Scope: data}), warned
}

// editorQuiet is editorPage for a page that must compile with no warning at all.
func editorQuiet(t *testing.T, source, method string, data map[string]any) string {
	t.Helper()
	html, warned := editorPage(t, source, method, data)
	if len(warned) > 0 {
		t.Errorf("want no warning, got:\n%s", strings.Join(warned, "\n"))
	}
	return html
}

// editorCanonical is the editor of the decision, as an author writes it: the toolbar of v2 (20/10/2026,
// evening), its buttons in groups, the smaller headings in a dropdown.
const editorCanonical = `<content>
<form method="post" gap="4">
  <field>
    <label>Nội dung</label>
    <editor as="$body" gap="2">
      <box alt="Định dạng" flex="row" wrap="true" align="left" gap="3">
        <group alt="Kiểu chữ" flex="row" gap="1">
          <button click="toggle('bold')" active:background="brand/15"><icon value="bold" alt="Đậm" /></button>
          <button click="toggle('italic')" active:background="brand/15"><icon value="italic" alt="Nghiêng" /></button>
          <button click="toggle('underline')" active:background="brand/15"><icon value="underline" alt="Gạch chân" /></button>
          <button click="toggle('strikethrough')" active:background="brand/15"><icon value="strikethrough" alt="Gạch ngang" /></button>
          <button click="toggle('code')" active:background="brand/15"><icon value="code" alt="Mã" /></button>
        </group>
        <group alt="Tiêu đề" flex="row" gap="1">
          <button click="toggle('heading', 2)" active:background="brand/15"><icon value="h-2" alt="Tiêu đề mục" /></button>
          <button click="toggle('heading', 3)" active:background="brand/15"><icon value="h-3" alt="Tiêu đề nhỏ" /></button>
          <dropdown>
            <button click="toggle()"><icon value="chevron-down" alt="Tiêu đề nhỏ hơn" /></button>
            <popover flex="row" gap="1" padding="1">
              <button click="$body.toggle('heading', 4)" active:background="brand/15"><icon value="h-4" alt="Tiêu đề mức 4" /></button>
              <button click="$body.toggle('heading', 5)" active:background="brand/15"><icon value="h-5" alt="Tiêu đề mức 5" /></button>
              <button click="$body.toggle('heading', 6)" active:background="brand/15"><icon value="h-6" alt="Tiêu đề mức 6" /></button>
            </popover>
          </dropdown>
        </group>
        <group alt="Khối" flex="row" gap="1">
          <button click="toggle('quote')" active:background="brand/15"><icon value="blockquote" alt="Trích dẫn" /></button>
          <button click="toggle('code', 'block')" active:background="brand/15"><icon value="source-code" alt="Khối mã" /></button>
        </group>
        <group alt="Danh sách" flex="row" gap="1">
          <button click="toggle('list')" active:background="brand/15"><icon value="list" alt="Danh sách" /></button>
          <button click="toggle('list', 'ordered')" active:background="brand/15"><icon value="list-numbers" alt="Danh sách số" /></button>
        </group>
        <group alt="Căn lề" flex="row" gap="1">
          <button click="align('left')" active:background="brand/15"><icon value="align-left" alt="Căn trái" /></button>
          <button click="align('center')" active:background="brand/15"><icon value="align-center" alt="Căn giữa" /></button>
          <button click="align('right')" active:background="brand/15"><icon value="align-right" alt="Căn phải" /></button>
        </group>
        <group alt="Chèn" flex="row" gap="1">
          <button click="link()"><icon value="link" alt="Liên kết" /></button>
          <button click="rule()"><icon value="separator-horizontal" alt="Đường kẻ ngang" /></button>
        </group>
        <group alt="Lịch sử" flex="row" gap="1">
          <button click="undo()"><icon value="arrow-back-up" alt="Hoàn tác" /></button>
          <button click="redo()"><icon value="arrow-forward-up" alt="Làm lại" /></button>
        </group>
      </box>
      <popover place="below-left" flex="row" wrap="true" gap="2" padding="2" radius="medium" background="paper">
        <input value="{{ address }}" alt="Địa chỉ liên kết" placeholder="https://… hay /trang" />
        <button click="link(address)">Gắn</button>
        <button click="unlink()">Bỏ liên kết</button>
        <button click="close()">Huỷ</button>
        <error>Chỉ nhận https://, http://, mailto:, tel: hay đường dẫn bắt đầu bằng /.</error>
      </popover>
      <content name="body" value="{{ post.body }}" required="true" placeholder="Viết bài ở đây…" rows="12"
               padding="4" radius="medium" border="line" />
    </editor>
    <hint>{{ $body.words }} từ</hint>
  </field>
  <button type="submit">Lưu bài</button>
  <text show="$body.unsaved">Chưa lưu</text>
</form>
</content>`

// editorPost is a saved post as an editor's value: a script and an h1 the cleaning takes out or makes
// an h2, a b it makes strong.
var editorPost = map[string]any{"post": map[string]any{"body": `<p>Xin chào <b>thế giới</b></p><script>alert(1)</script><h1>Tựa</h1>`}}

// editorButtonClass is a format button's classes: its own active: background, no neutral fill.
const editorButtonClass = `<button class="flex-row active:background-brand/15" type="button"`

// editorNeutral is a button with no fill of its own: the neutral fill the engine gives it.
const editorNeutral = `<button class="flex-row background-ink/6 hover:background-ink/10 pressed:background-ink/14 disabled:background-ink/4" type="button"`

// The canonical editor, whole, is the wire the editor's component reads: the host with its first state;
// the toolbar (role, name, the area it controls, one roving tabindex across all of it), each group
// (role="group", named by its alt, class editor-group), each format button with its format, aria-pressed
// and its keys (Control+U for underline), the dropdown of smaller headings (its trigger one stop of the
// toolbar, aria-controls its popover and aria-expanded bound to the dropdown's open; the popover's id;
// each command in it with its format and aria-pressed, called through the alias), each align button
// with its command and aria-pressed, rule and the other commands with theirs, the link button with the
// popover it opens; the popover (dialog, named by its address input), the address input with the
// engine's words, the error hidden with its id; the area, a richtext block named by the field's label,
// described by its hint, holding the value cleaned, not editable until the component starts; the field
// the form posts right after it, the same HTML escaped, the field's id and the label's for. The <content>
// of content.ui around it is still the page's content region (<main>), and page.ui's <content /> too.
// And it compiles with no warning: the trigger's bare toggle() is the dropdown's, by the engine's reading.
// CONTROL: the editor's value written raw (editorValueOp without richtext.Clean), the area left a region
// (element() without its editorContent), the popover keeping the field's id (generic without the field
// cleared there) each turned this red (run 20/10/2026 on overlaid copies of editor.go and compile.go).
// Toolbar v2 (run 21/10/2026 on overlaid copies, controls-toolbar in the scratchpad): editorMenuElement
// writing nothing, a group without role="group", underline without its keys, checkEditors telling the
// dropdown's calls through the alias, and the shadow warning of checkNames not told the trigger's
// toggle() is the dropdown's (host.go) each turned this red.
func TestEditorCanonicalWire(t *testing.T) {
	s := memorySite("", map[string]string{"/site/content.ui": editorCanonical, "/site/page.ui": `<page><content /></page>`})
	s.Reload = true
	page, warned := compileDiagnose(t, s)
	if len(warned) > 0 {
		t.Errorf("want no warning, got:\n%s", strings.Join(warned, "\n"))
	}
	html := page.Render(Request{Path: "/", Method: "GET", Scope: editorPost})
	// a button with its own active: background, its wire, its click and its icon
	button := func(tab, wire, call, icon, alt string) string {
		if tab != "" {
			tab = ` tabindex="` + tab + `"`
		}
		return editorButtonClass + tab + wire + ` data-kitwork-event="click: ` + call + `"><i class="icon-` + icon + `" role="img" aria-label="` + alt + `"></i></button>`
	}
	format := func(name, keys, call, icon, alt, tab string) string {
		wire := ` data-kitwork-format="` + name + `" aria-pressed="false"`
		if keys != "" {
			wire += ` aria-keyshortcuts="` + keys + `"`
		}
		return button(tab, wire, call, icon, alt)
	}
	align := func(side, alt, tab string) string {
		return button(tab, ` data-kitwork-command="align-`+side+`" aria-pressed="false"`, "align(&#39;"+side+"&#39;)", "align-"+side, alt)
	}
	group := func(alt string) string {
		return `<div class="flex-row gap-1 editor-group" aria-label="` + alt + `" role="group">`
	}
	want := strings.Join([]string{
		`<main class="flex-column">`,
		`<form class="flex-column gap-4" method="post">`,
		`<div class="field flex-column">`,
		`<label for="kitwork-field-1" id="kitwork-field-1-label">Nội dung</label>`,
		`<div class="flex-column place-anchor gap-2 editor" data-kitwork-component="editor: $body" data-kitwork-scope="empty: false; words: 5; characters: 20; unsaved: false; open: false; address: &#39;&#39;; format: &#39;rich&#39;; dropped: []">`,
		`<div class="flex-row wrap-true align-left gap-3 editor-toolbar" aria-label="Định dạng" role="toolbar" aria-controls="kitwork-editor-1-area">`,
		group("Kiểu chữ"),
		format("bold", "Control+B", "toggle(&#39;bold&#39;)", "bold", "Đậm", "0"),
		format("italic", "Control+I", "toggle(&#39;italic&#39;)", "italic", "Nghiêng", "-1"),
		format("underline", "Control+U", "toggle(&#39;underline&#39;)", "underline", "Gạch chân", "-1"),
		format("strikethrough", "", "toggle(&#39;strikethrough&#39;)", "strikethrough", "Gạch ngang", "-1"),
		format("code", "", "toggle(&#39;code&#39;)", "code", "Mã", "-1"),
		`</div>`,
		group("Tiêu đề"),
		format("heading-2", "", "toggle(&#39;heading&#39;, 2)", "h-2", "Tiêu đề mục", "-1"),
		format("heading-3", "", "toggle(&#39;heading&#39;, 3)", "h-3", "Tiêu đề nhỏ", "-1"),
		`<div class="flex-column place-anchor" data-kitwork-component="dropdown">`,
		editorNeutral + ` tabindex="-1" aria-controls="kitwork-editor-1-menu-1" data-kitwork-bind="aria-expanded: open" data-kitwork-event="click: toggle()" aria-expanded="false"><i class="icon-chevron-down" role="img" aria-label="Tiêu đề nhỏ hơn"></i></button>`,
		`<div class="popover flex-row gap-1 padding-1" hidden id="kitwork-editor-1-menu-1" data-kitwork-bind="show: open">`,
		format("heading-4", "", "$body.toggle(&#39;heading&#39;, 4)", "h-4", "Tiêu đề mức 4", ""),
		format("heading-5", "", "$body.toggle(&#39;heading&#39;, 5)", "h-5", "Tiêu đề mức 5", ""),
		format("heading-6", "", "$body.toggle(&#39;heading&#39;, 6)", "h-6", "Tiêu đề mức 6", ""),
		`</div>`,
		`</div>`,
		`</div>`,
		group("Khối"),
		format("quote", "", "toggle(&#39;quote&#39;)", "blockquote", "Trích dẫn", "-1"),
		format("code-block", "", "toggle(&#39;code&#39;, &#39;block&#39;)", "source-code", "Khối mã", "-1"),
		`</div>`,
		group("Danh sách"),
		format("list", "", "toggle(&#39;list&#39;)", "list", "Danh sách", "-1"),
		format("list-ordered", "", "toggle(&#39;list&#39;, &#39;ordered&#39;)", "list-numbers", "Danh sách số", "-1"),
		`</div>`,
		group("Căn lề"),
		align("left", "Căn trái", "-1"),
		align("center", "Căn giữa", "-1"),
		align("right", "Căn phải", "-1"),
		`</div>`,
		group("Chèn"),
		editorNeutral + ` tabindex="-1" data-kitwork-command="link" aria-keyshortcuts="Control+K" aria-controls="kitwork-editor-1-popover" aria-haspopup="dialog" aria-disabled="true" data-kitwork-bind="aria-expanded: open" data-kitwork-event="click: link()" aria-expanded="false"><i class="icon-link" role="img" aria-label="Liên kết"></i></button>`,
		editorNeutral + ` tabindex="-1" data-kitwork-command="rule" data-kitwork-event="click: rule()"><i class="icon-separator-horizontal" role="img" aria-label="Đường kẻ ngang"></i></button>`,
		`</div>`,
		group("Lịch sử"),
		editorNeutral + ` tabindex="-1" data-kitwork-command="undo" aria-keyshortcuts="Control+Z" aria-disabled="true" data-kitwork-event="click: undo()"><i class="icon-arrow-back-up" role="img" aria-label="Hoàn tác"></i></button>`,
		editorNeutral + ` tabindex="-1" data-kitwork-command="redo" aria-keyshortcuts="Control+Shift+Z" aria-disabled="true" data-kitwork-event="click: redo()"><i class="icon-arrow-forward-up" role="img" aria-label="Làm lại"></i></button>`,
		`</div>`,
		`</div>`,
		`<div class="popover place-below-left flex-row wrap-true gap-2 padding-2 radius-medium background-paper editor-popover" hidden id="kitwork-editor-1-popover" role="dialog" aria-label="Địa chỉ liên kết" data-kitwork-bind="show: open">`,
		`<input class="editor-address" aria-label="Địa chỉ liên kết" placeholder="https://… hay /trang" type="text" inputmode="url" autocomplete="off" autocapitalize="none" spellcheck="false" data-kitwork-bind="value: address" value="">`,
		editorNeutral + ` data-kitwork-command="link" data-kitwork-event="click: link(address)">Gắn</button>`,
		editorNeutral + ` data-kitwork-command="unlink" data-kitwork-event="click: unlink()">Bỏ liên kết</button>`,
		editorNeutral + ` data-kitwork-command="close" data-kitwork-event="click: close()">Huỷ</button>`,
		`<p class="error editor-address-error" id="kitwork-editor-1-address-error" hidden>Chỉ nhận https://, http://, mailto:, tel: hay đường dẫn bắt đầu bằng /.</p>`,
		`</div>`,
		`<div class="richtext editor-area padding-4 radius-medium border-line editor-rows-12" id="kitwork-editor-1-area" role="textbox" aria-multiline="true" aria-labelledby="kitwork-field-1-label" aria-describedby="kitwork-field-1-hint" aria-placeholder="Viết bài ở đây…" aria-required="true" aria-readonly="true" translate="no" spellcheck="true" data-kitwork-name="body">`,
		"<p>Xin chào <strong>thế giới</strong></p>\n<h2>Tựa</h2>",
		`</div>`,
		`<textarea class="editor-field padding-4 radius-medium border-line" id="kitwork-field-1" name="body" required rows="12" placeholder="Viết bài ở đây…" aria-describedby="kitwork-field-1-hint">`,
		"&lt;p&gt;Xin chào &lt;strong&gt;thế giới&lt;/strong&gt;&lt;/p&gt;\n&lt;h2&gt;Tựa&lt;/h2&gt;",
		`</textarea>`,
		`</div>`,
		`<p class="hint" id="kitwork-field-1-hint" data-kitwork-bind="text: [($body.words), &#39; từ&#39;].join(&#39;&#39;)">5 từ</p>`,
		`</div>`,
		`<button class="flex-row background-ink/6 hover:background-ink/10 pressed:background-ink/14 disabled:background-ink/4" type="submit">Lưu bài</button>`,
		`<p data-kitwork-bind="show: $body.unsaved" hidden>Chưa lưu</p>`,
		`</form>`,
		`</main>`,
	}, "")
	if !strings.Contains(html, want) {
		t.Errorf("the canonical editor:\n got  %s\n want %s", html, want)
	}
	for _, part := range []string{"contenteditable", "<script", "alert(1)", "<h1", "<b>"} {
		if strings.Contains(html, part) {
			t.Errorf("the first paint holds %q:\n%s", part, html)
		}
	}
	if n := strings.Count(html, `id="kitwork-field-1"`); n != 1 {
		t.Errorf("the field's id written %d times, want once (the field the form posts):\n%s", n, html)
	}
	// the area holds what richtext.Clean makes of the value, the field the same, escaped
	cleaned := richtext.Clean(editorPost["post"].(map[string]any)["body"].(string))
	if !strings.Contains(html, cleaned.HTML+`</div><textarea`) || cleaned.Words != 5 || cleaned.Characters != 20 {
		t.Errorf("the area is not richtext.Clean of the value (%q, %d words, %d characters)", cleaned.HTML, cleaned.Words, cleaned.Characters)
	}
}

// The first paint reads the value as the server has it: cleaned (a script and its words gone, a link the
// rule refuses unwrapped, its words kept), plain text a paragraph per blank line and a line break per
// newline, nothing or no value an empty area (empty: true, no words); the counts are the cleaned text's,
// in data-kitwork-scope and through the alias outside the host ($body.words), and two editors on one
// page count each its own value. unsaved is false for a GET (and for a request with no method) and true
// for a request that is no GET: a POST answered with the page, a refused save shown again.
// CONTROL: the counts read off the value as written (bindings without richtext.Clean) and unsaved
// always false (its function returning false) each turned rows of this red (run 20/10/2026 on overlaid
// copies of editor.go).
func TestEditorFirstPaintIsTheValueCleaned(t *testing.T) {
	page := func(value string, after ...string) string {
		return `<content><form method="post"><editor as="$body">` + editorBar + editorLink + `<content name="body" alt="Nội dung" ` + value + `/></editor>` +
			`<text>{{ $body.words }}/{{ $body.characters }}/{{ $body.empty }}</text>` + strings.Join(after, "") + `</form></content>`
	}
	for _, c := range []struct {
		name, written, value, area, field, scope, outside string
	}{
		{"html cleaned", `value="{{ body }}" `, `<p>Một <a href="javascript:alert(1)">hai</a> ba</p><script>bốn()</script>`,
			`<p>Một hai ba</p>`, `&lt;p&gt;Một hai ba&lt;/p&gt;`, `empty: false; words: 3; characters: 10`, `3/10/false`},
		{"plain text", `value="{{ body }}" `, "Dòng một\nDòng hai\n\nĐoạn hai",
			"<p>Dòng một<br>Dòng hai</p>\n<p>Đoạn hai</p>", "&lt;p&gt;Dòng một&lt;br&gt;Dòng hai&lt;/p&gt;\n&lt;p&gt;Đoạn hai&lt;/p&gt;", `empty: false; words: 6; characters: 24`, `6/24/false`},
		{"nothing", `value="{{ body }}" `, "", ``, ``, `empty: true; words: 0; characters: 0`, `0/0/true`},
		{"no value written", ``, "ignored", ``, ``, `empty: true; words: 0; characters: 0`, `0/0/true`},
	} {
		t.Run(c.name, func(t *testing.T) {
			html := editorQuiet(t, page(c.written), "GET", map[string]any{"body": c.value})
			mustContain(t, html, `data-kitwork-name="body">`+c.area+`</div>`, `aria-label="Nội dung">`+c.field+`</textarea>`,
				`data-kitwork-scope="`+c.scope+`; unsaved: false; open: false; address: &#39;&#39;; format: &#39;rich&#39;; dropped: []"`, `>`+c.outside+`</p>`)
		})
	}
	// unsaved: false for a GET or no method, true for any other
	for _, c := range []struct{ method, unsaved string }{{"GET", "false"}, {"", "false"}, {"HEAD", "false"}, {"POST", "true"}, {"post", "true"}, {"PUT", "true"}} {
		html := editorQuiet(t, page(``, `<text show="$body.unsaved">Chưa lưu</text>`), c.method, nil)
		mustContain(t, html, `unsaved: `+c.unsaved+`;`)
		if shown := !strings.Contains(html, `hidden>Chưa lưu</p>`); shown != (c.unsaved == "true") {
			t.Errorf("method %q: $body.unsaved outside the host painted %v, want %s:\n%s", c.method, shown, c.unsaved, html)
		}
	}
	// two editors, each its own value
	two := `<content><form method="post"><editor as="$title">` + editorBar + editorLink + `<content name="title" alt="Tựa" value="{{ title }}" /></editor>` +
		`<editor as="$body">` + `<box alt="Định dạng"><button click="toggle('italic')">I</button></box>` + `<content name="body" alt="Nội dung" value="{{ body }}" /></editor>` +
		`<text>{{ $title.words }} và {{ $body.words }}</text></form></content>`
	html := editorQuiet(t, two, "GET", map[string]any{"title": "Một hai", "body": "<p>Ba bốn năm</p>"})
	mustContain(t, html, `id="kitwork-editor-1-area"`, `id="kitwork-editor-2-area"`, `data-kitwork-name="title"><p>Một hai</p></div>`,
		`data-kitwork-name="body"><p>Ba bốn năm</p></div>`, `>2 và 3</p>`)
}

// Each part an author writes wrong is told at its place in its words: where the editor stands (outside a
// post form, inside another, inside a line, in a for), the words its host does not take, its writing
// area (none, two, in a for or an if, inside a nested component, no name, words written as a value, a
// value of the browser's, a word another text area takes), its buttons (a format not written out,
// another library's name for one, a format it does not have, a heading level it does not have, a list
// variant, a link with no popover to open, a link of the address outside the popover, a command
// that takes nothing given something, a button repeated by for, beside the area), its toolbars (no alt,
// not a box), its popover (two, no address input, two, the words the engine writes on it, a value that
// is not the address, no alt, two errors), what is written outside it (a command through its alias, a
// name it has not, a command in another event, its state written) and the other ways to write rich text.
// Toolbar v2 (21/10/2026): the formats of v2 (another library's name, a level past 6, code's variant,
// a rule or a side asked of toggle), align (no side, start and end, justify, a side it has not, two),
// rule given something, align in the link's popover or on the host, a <group> anywhere but a toolbar or
// in another, and a dropdown of commands (a bare call in it, no trigger, a second button beside the
// trigger, a command beside it, link or close in it, expanded on the trigger, a component in it, a
// format it has not, a dropdown of commands beside the area).
// CONTROL: each row compiled with its check taken out of editor.go (the toolbar's alt, link() with no
// popover, the state written on the host, the button beside the area, a call through the alias): this
// test failed on each (run 20/10/2026 on overlaid copies of editor.go). Toolbar v2 (run 21/10/2026 on
// overlaid copies, controls-toolbar in the scratchpad): editorAlign taking every side, editorMenuButton
// taking every bare call as the dropdown's, editorGroupPlace taking every group and the editor's format
// table of v1 (editorFormat as it was) each turned their rows red.
// A dropdown's button calling through an alias that is not its editor's (the editor without as, or
// another editor's alias) is told at the button which alias its editor has, or that it needs one (review
// of 21/10/2026).
// CONTROL (run 21/10/2026 on overlaid copies, fix in the scratchpad): with the toolbar's dropdown refused
// whole again (editorFirstStray's branch off), "a dropdown calling through an alias, the editor without
// as" and "a dropdown calling only another editor" got the dropdown's own refusal at <dropdown>, which
// tells the alias call they wrote; with checkEditors no longer looking for another editor's dropdown,
// "a dropdown calling another editor" got "$b.toggle('heading', 5) ở ngoài <editor>".
func TestEditorRefusalsAtTheirPlace(t *testing.T) {
	area := editorArea
	for _, c := range []struct {
		name, source, needle, says string
		refs                       []string
	}{
		{"outside a form", `<content><editor>` + editorBar + editorLink + area + `</editor></content>`, "<editor",
			`editor phải nằm trong <form method="post">: bài viết đi theo form khi gửi, router.post của trang nhận nó`, nil},
		{"in a get form", `<content><form><editor>` + editorBar + editorLink + area + `</editor></form></content>`, "<editor",
			`editor phải nằm trong <form method="post">: form GET đưa cả bài viết lên địa chỉ trang (<form> ở %s)`, []string{"<form"}},
		{"inside another editor", editorIn(``, editorBar+editorLink+area+`<editor>`+area+`</editor>`), "<editor><content",
			`editor không lồng trong editor (editor ngoài ở %s)`, []string{"<editor"}},
		{"inside another editor, through a box", editorIn(``, editorBar+editorLink+area+`<box><editor>`+area+`</editor></box>`), "<editor><content",
			`editor không lồng trong editor (editor ngoài ở %s)`, []string{"<editor"}},
		{"inside a line", `<content><form method="post"><text><editor>` + area + `</editor></text></form></content>`, "<editor",
			`editor là một khối: không đặt trong <text>, <link>, <button>, <label> (nó đang nằm trong <text> ở %s)`, []string{"<text"}},
		{"in a for", `<content><form method="post"><box for="post of posts"><editor>` + area + `</editor></box></form></content>`, "for=",
			`editor không lặp bằng for: vùng viết, thanh công cụ và khung liên kết của nó mang id riêng, mỗi trang sửa một bài (editor ở %s)`, []string{"<editor"}},
		{"empty", editorIn(``, ``), "<editor", `editor rỗng: vẽ vùng viết bên trong, <editor><content name="body" alt="Nội dung" /></editor>`, nil},
		{"no area", editorIn(``, editorBar+editorLink), "<editor",
			`editor cần một vùng viết bên trong, engine viết role="textbox" lên nó: <content name="body" value="{{ post.body }}" />`, nil},
		{"two areas", editorIn(``, editorBar+editorLink+area+`<content name="other" alt="Khác" />`), `<content name="other"`,
			`editor có một vùng viết: <content> ở %s đã là của nó; vùng viết thứ hai là một <editor> khác`, []string{`<content name="body"`}},
		{"area in a for", editorIn(``, editorBar+editorLink+`<box for="x of xs">`+area+`</box>`), "for=",
			`vùng viết của editor không lặp bằng for: for này lặp <content> ở %s; đặt <content> ngoài for`, []string{`<content name="body"`}},
		{"area under an if", editorIn(``, editorBar+editorLink+`<box if="ok">`+area+`</box>`), "if=",
			`vùng viết của editor luôn có mặt: if này có thể bỏ <content> ở %s; đặt <content> ngoài if`, []string{`<content name="body"`}},
		{"area in a nested component", editorIn(``, editorBar+editorLink+`<dropdown>`+area+`</dropdown>`), "<content name",
			`vùng viết của editor viết thẳng trong <editor>, không trong <dropdown> lồng bên trong (ở %s): tên trần bên trong đó là của nó`, []string{"<dropdown"}},
		{"area with words inside", editorIn(``, editorBar+editorLink+`<content name="body" alt="Nội dung"><text>x</text></content>`), "<content name",
			`vùng viết của editor không có nội dung bên trong: bài lúc đầu là value="{{ post.body }}"`, nil},
		{"no name", editorIn(``, editorBar+editorLink+`<content alt="Nội dung" />`), "<content alt",
			`<content> của editor cần name: không có name thì form không gửi bài lên server (name="body")`, nil},
		{"name from data", editorIn(``, editorBar+editorLink+`<content name="{{ field }}" alt="x" />`), "name=",
			`name của vùng viết là chữ cố định, tên ô form gửi lên (name="body"), không lấy từ dữ liệu`, nil},
		{"value written", editorIn(``, editorBar+editorLink+`<content name="body" alt="x" value="Xin chào" />`), `value="Xin`,
			`value của vùng viết là bài đã lưu, một {{ }} đọc dữ liệu của trang: value="{{ post.body }}"`, nil},
		{"value of its own state", editorIn(``, editorBar+editorLink+`<content name="body" alt="x" value="{{ words }}" />`), `value="{{ words`,
			`value của vùng viết dựng ở server: đọc dữ liệu của trang, không đọc words (trạng thái ở trình duyệt)`, nil},
		{"rows not a number", editorIn(``, editorBar+editorLink+`<content name="body" alt="x" rows="many" />`), "rows=",
			`rows là số dòng thấp nhất của vùng viết, một số nguyên từ 1 tới 100 (rows="12"), không phải "many"`, nil},
		{"required from data", editorIn(``, editorBar+editorLink+`<content name="body" alt="x" required="{{ must }}" />`), "required=",
			`required của vùng viết là "true" hay "false" viết sẵn ở bản này, không lấy từ dữ liệu`, nil},
		{"flex on the area", editorIn(``, editorBar+editorLink+`<content name="body" alt="x" flex="row" />`), "flex=",
			`vùng viết là một khối chữ, engine đặt cách các đoạn nằm: bỏ flex`, nil},
		{"maxlength on the area", editorIn(``, editorBar+editorLink+`<content name="body" alt="x" maxlength="500" />`), "maxlength=",
			`maxlength không có trên vùng viết: độ dài bài kiểm ở server (richtext(…).characters)`, nil},
		{"change on the area", editorIn(``, editorBar+editorLink+`<content name="body" alt="x" change="save()" />`), "change=",
			`change viết trên <content> của editor chưa có ở bản này: bài đi theo form khi gửi, số chữ là {{ words }}`, nil},
		{"an area nothing names", editorIn(``, editorBar+editorLink+`<content name="body" />`), "<content name",
			`vùng viết chưa có tên đọc được: đặt editor trong <field> có <label>, hay viết alt trên <content>`, nil},
		{"name on the host", editorIn(` name="body"`, editorBar+editorLink+area), "name=",
			`name không có trên <editor>: name, value, placeholder, required, readonly, disabled, rows viết trên <content> bên trong`, nil},
		{"state on the host", editorIn(` as="$body" words="0"`, editorBar+editorLink+area), "words=",
			`words là trạng thái của editor, không phải đầu vào: đọc {{ $body.words }}`, nil},
		{"a method on the host", editorIn(` link="x"`, editorBar+editorLink+area), "link=",
			`link là hàm của editor, không phải một chữ trên thẻ: gọi nó trong click của một nút bên trong (<button click="link()">)`, nil},
		{"input on the host", editorIn(` input="count()"`, editorBar+editorLink+area), "input=",
			`input trên <editor> chưa có ở bản này: bài đi theo form khi gửi, số chữ là {{ words }}`, nil},
		{"a toolbar attribute", editorIn(` toolbar="bold italic"`, editorBar+editorLink+area), "toolbar=",
			`editor không có toolbar: editor không có giao diện dựng sẵn, vẽ nút bên trong (<button click="toggle('bold')">)`, nil},
		{"a role", editorIn(` role="textbox"`, editorBar+editorLink+area), "role=",
			`vai trò của editor là việc của engine (role="textbox" trên vùng viết, role="toolbar" trên thanh công cụ): bỏ role`, nil},
		{"a format not written out", editorIn(``, `<box alt="F"><button click="toggle(name)">B</button></box>`+area), "click=",
			`toggle cần tên định dạng viết sẵn, như toggle('bold'): engine đọc nó để viết aria-pressed và phím tắt`, nil},
		{"strong", editorIn(``, `<box alt="F"><button click="toggle('strong')">B</button></box>`+area), "click=",
			`toggle('strong'): định dạng này tên là bold, viết toggle('bold')`, nil},
		{"numbered-list", editorIn(``, `<box alt="F"><button click="toggle('numbered-list')">B</button></box>`+area), "click=",
			`toggle('numbered-list'): định dạng này tên là list 'ordered', viết toggle('list', 'ordered')`, nil},
		{"an image", editorIn(``, `<box alt="F"><button click="toggle('image')">U</button></box>`+area), "click=",
			`toggle('image'): editor chưa có định dạng này ở bản này; có bold, italic, underline, strikethrough, code, quote, heading 2 tới 6, list`, nil},
		{"strike", editorIn(``, `<box alt="F"><button click="toggle('strike')">S</button></box>`+area), "click=",
			`toggle('strike'): định dạng này tên là strikethrough, viết toggle('strikethrough')`, nil},
		{"a format it has not", editorIn(``, `<box alt="F"><button click="toggle('bols')">B</button></box>`+area), "click=",
			`toggle('bols'): editor không có định dạng bols; có bold, italic, underline, strikethrough, code, quote, heading 2 tới 6, list; gần nhất: bold`, nil},
		{"a paragraph", editorIn(``, `<box alt="F"><button click="toggle('paragraph')">P</button></box>`+area), "click=",
			`toggle('paragraph'): editor không có định dạng paragraph: bấm lại nút đang bật là về đoạn văn`, nil},
		{"heading without a level", editorIn(``, `<box alt="F"><button click="toggle('heading')">H</button></box>`+area), "click=",
			`toggle('heading') cần mức: toggle('heading', 2) tới toggle('heading', 6)`, nil},
		{"heading 1", editorIn(``, `<box alt="F"><button click="toggle('heading', 1)">H</button></box>`+area), "click=",
			`toggle('heading', 1): heading 1 là tiêu đề của trang (<text heading="1">); trong bài viết dùng toggle('heading', 2) tới toggle('heading', 6)`, nil},
		{"h1 by another name", editorIn(``, `<box alt="F"><button click="toggle('h1')">H</button></box>`+area), "click=",
			`toggle('h1'): heading 1 là tiêu đề của trang (<text heading="1">); trong bài viết dùng toggle('heading', 2) tới toggle('heading', 6)`, nil},
		{"heading 7", editorIn(``, `<box alt="F"><button click="toggle('heading', 7)">H</button></box>`+area), "click=",
			`toggle('heading', 7): bài viết có tiêu đề mục từ mức 2 tới mức 6; viết toggle('heading', 2) tới toggle('heading', 6)`, nil},
		{"h4 by another name", editorIn(``, `<box alt="F"><button click="toggle('h4')">H</button></box>`+area), "click=",
			`toggle('h4'): định dạng này tên là heading mức 4, viết toggle('heading', 4)`, nil},
		{"code of another kind", editorIn(``, `<box alt="F"><button click="toggle('code', 'inline')">C</button></box>`+area), "click=",
			`toggle('code', 'inline'): code nhận 'block' hay không gì: toggle('code') là mã trong dòng, toggle('code', 'block') là khối mã`, nil},
		{"pre", editorIn(``, `<box alt="F"><button click="toggle('pre')">C</button></box>`+area), "click=",
			`toggle('pre'): định dạng này tên là code 'block', viết toggle('code', 'block')`, nil},
		{"a rule toggled", editorIn(``, `<box alt="F"><button click="toggle('hr')">—</button></box>`+area), "click=",
			`toggle('hr'): đường kẻ ngang là rule(), nó chèn một <hr> sau khối ở con trỏ (<button click="rule()">)`, nil},
		{"a side toggled", editorIn(``, `<box alt="F"><button click="toggle('align', 'center')">C</button></box>`+area), "click=",
			`toggle('align', 'center'): căn lề không phải một định dạng của toggle: viết align('center')`, nil},
		{"align without a side", editorIn(``, `<box alt="F"><button click="align()">C</button></box>`+area), "click=",
			`align cần một phía viết sẵn, align('left'), align('center') hay align('right'): engine đọc nó để viết aria-pressed`, nil},
		{"align start", editorIn(``, `<box alt="F"><button click="align('start')">C</button></box>`+area), "click=",
			`align('start'): .ui nói trái, phải (trang viết từ phải sang trái lật như gương): viết align('left')`, nil},
		{"align end", editorIn(``, `<box alt="F"><button click="align('end')">C</button></box>`+area), "click=",
			`align('end'): .ui nói trái, phải (trang viết từ phải sang trái lật như gương): viết align('right')`, nil},
		{"justify", editorIn(``, `<box alt="F"><button click="align('justify')">J</button></box>`+area), "click=",
			`align('justify'): editor chưa có căn đều ở bản này: tiếng Việt căn đều dễ hở chữ giữa các từ; có left, center, right`, nil},
		{"a side it has not", editorIn(``, `<box alt="F"><button click="align('rigth')">R</button></box>`+area), "click=",
			`align('rigth'): editor không căn rigth; có left, center, right; gần nhất: right`, nil},
		{"two sides", editorIn(``, `<box alt="F"><button click="align('left', 'right')">R</button></box>`+area), "click=",
			`align('left', 'right'): align nhận một phía, align('left'), align('center') hay align('right')`, nil},
		{"rule given something", editorIn(``, `<box alt="F"><button click="rule(2)">—</button></box>`+area), "click=",
			`rule() không nhận gì: viết rule()`, nil},
		{"align in the link's popover", editorIn(``, editorBar+`<popover><input value="{{ address }}" alt="Địa chỉ" /><button click="align('left')">T</button></popover>`+area), `click="align`,
			`align('left') là nút của thanh công cụ, không đặt trong <popover> của liên kết`, nil},
		{"align on the host", editorIn(` align="left"`, editorBar+editorLink+area), "align=",
			`align không viết trên <editor>: align() là lệnh căn lề của editor (<button click="align('center')">); căn các phần bên trong bằng align trên khung bọc chúng (<box align="left">)`, nil},
		{"a group outside an editor", `<content><group><text>x</text></group></content>`, "<group",
			`<group> là một nhóm nút trong thanh công cụ của editor (<box alt="Định dạng"><group alt="Kiểu chữ">…</group></box>); ở chỗ khác gom các phần bằng <box>`, nil},
		{"a group in an editor, in no toolbar", editorIn(``, editorBar+editorLink+`<group><button>x</button></group>`+area), "<group",
			`<group> là một nhóm nút trong thanh công cụ của editor (<box alt="Định dạng"><group alt="Kiểu chữ">…</group></box>); ở chỗ khác gom các phần bằng <box>`, nil},
		{"a group as the toolbar", editorIn(``, `<group alt="F"><button click="toggle('bold')">B</button></group>`+area), "<group",
			`<group> là một nhóm nút trong thanh công cụ của editor: đặt các <group> trong một khung có alt, <box alt="Định dạng"><group alt="Kiểu chữ">…</group></box>, engine viết role="toolbar" lên khung đó`, nil},
		{"a group in a group", editorIn(``, `<box alt="F"><group><group><button click="toggle('bold')">B</button></group></group></box>`+area), "<group><button",
			`<group> không lồng trong <group> (nhóm ngoài ở %s): mỗi nhóm là một hàng nút của thanh công cụ`, []string{"<group"}},
		{"a bare toggle in a dropdown", `<content><form method="post"><editor as="$body"><box alt="F"><dropdown><button click="toggle()">v</button><popover><button click="toggle('heading', 4)">H4</button></popover></dropdown></box>` + area + `</editor></form></content>`, `click="toggle('heading'`,
			`toggle('heading', 4) trong <dropdown>: tên trần bên trong <dropdown> là của nó (toggle() mở và đóng nó); lệnh của editor ở đây gọi qua alias, $body.toggle('heading', 4)`, nil},
		{"a bare toggle in a dropdown, no alias", editorIn(``, `<box alt="F"><dropdown><button click="toggle()">v</button><popover><button click="toggle('heading', 4)">H4</button></popover></dropdown></box>`+area), `click="toggle('heading'`,
			`toggle('heading', 4) trong <dropdown>: tên trần bên trong <dropdown> là của nó (toggle() mở và đóng nó); lệnh của editor ở đây gọi qua alias, đặt as trên <editor> (<editor as="$body">) rồi gọi $body.toggle('heading', 4)`, nil},
		{"a dropdown calling through an alias, the editor without as", editorIn(``, `<box alt="F"><button click="toggle('bold')">B</button><dropdown><button click="toggle()">v</button><popover><button click="$body.toggle('heading', 4)">H4</button></popover></dropdown></box>`+area), `click="$body`,
			`$body.toggle('heading', 4) trong <dropdown> của <editor> chưa có as: nút trong dropdown gọi editor bao quanh nó qua alias của nó; đặt as trên <editor> (<editor as="$body">) rồi gọi $body.toggle('heading', 4)`, nil},
		{"a dropdown calling another editor", `<content><form method="post"><editor as="$a"><box alt="F"><button click="toggle('bold')">B</button><dropdown><button click="toggle()">v</button><popover><button click="$a.toggle('heading', 4)">H4</button><button click="$b.toggle('heading', 5)">H5</button></popover></dropdown></box><content name="a" alt="A" /></editor>` +
			`<editor as="$b"><box alt="G"><button click="toggle('bold')">B</button></box><content name="b" alt="B" /></editor></form></content>`, `click="$b`,
			`$b.toggle('heading', 5) trong <dropdown> của editor $a: nút trong dropdown gọi editor bao quanh nó, qua alias của nó ($a.toggle('heading', 5))`, nil},
		{"a dropdown calling only another editor", `<content><form method="post"><editor as="$a"><box alt="F"><button click="toggle('bold')">B</button><dropdown><button click="toggle()">v</button><popover><button click="$b.toggle('heading', 5)">H5</button></popover></dropdown></box><content name="a" alt="A" /></editor>` +
			`<editor as="$b"><box alt="G"><button click="toggle('bold')">B</button></box><content name="b" alt="B" /></editor></form></content>`, `click="$b`,
			`$b.toggle('heading', 5) trong <dropdown> của editor $a: nút trong dropdown gọi editor bao quanh nó, qua alias của nó ($a.toggle('heading', 5))`, nil},
		{"a bare align in a dropdown", `<content><form method="post"><editor as="$body"><box alt="F"><dropdown><button click="toggle()">v</button><popover><button click="align('center')">C</button></popover></dropdown></box>` + area + `</editor></form></content>`, `click="align`,
			`align('center') trong <dropdown>: tên trần bên trong <dropdown> là của nó (toggle() mở và đóng nó); lệnh của editor ở đây gọi qua alias, $body.align('center')`, nil},
		{"a dropdown without its trigger", editorMenuIn(`<popover><button click="$body.toggle('heading', 4)">H4</button></popover>`), "<dropdown",
			`<dropdown> trong thanh công cụ của editor cần nút mở nó, <button click="toggle()">, ngoài <popover>: nút đó là một điểm dừng của Tab trong thanh`, nil},
		{"a second button beside the trigger", editorMenuIn(`<button click="toggle()">v</button><button>khác</button><popover><button click="$body.toggle('heading', 4)">H4</button></popover>`), "<button>khác",
			`<dropdown> trong thanh công cụ của editor có một nút ngoài <popover>, nút mở nó (<button click="toggle()"> ở %s): đặt nút này trong <popover>`, []string{`<button click="toggle()"`}},
		{"a command beside the trigger", editorMenuIn(`<button click="toggle()">v</button><button click="$body.toggle('heading', 5)">H5</button><popover><button click="$body.toggle('heading', 4)">H4</button></popover>`), `click="$body.toggle('heading', 5)`,
			`$body.toggle('heading', 5) ở ngoài <popover> của <dropdown>: nút lệnh của dropdown trong thanh công cụ nằm trong <popover> của nó; ngoài <popover> chỉ có nút mở nó, <button click="toggle()">`, nil},
		{"a link in a dropdown", editorMenuIn(`<button click="toggle()">v</button><popover><button click="$body.link()">L</button></popover>`), `click="$body.link`,
			`$body.link() không đặt trong <dropdown>: nút liên kết mở khung liên kết của editor, viết nó thẳng trong thanh công cụ (<button click="link()">)`, nil},
		{"close in a dropdown", editorMenuIn(`<button click="toggle()">v</button><popover><button click="$body.close()">x</button></popover>`), `click="$body.close`,
			`$body.close() đóng khung liên kết của editor: nút này là của <popover> của liên kết (<button click="close()">); dropdown của thanh tự đóng sau mỗi lệnh`, nil},
		{"expanded on the trigger", editorMenuIn(`<button click="toggle()" expanded="{{ open }}">v</button><popover><button click="$body.toggle('heading', 4)">H4</button></popover>`), "expanded=",
			`expanded của nút mở dropdown trong thanh công cụ là việc của engine (aria-expanded theo open): bỏ expanded`, nil},
		{"a component in a toolbar's dropdown", editorMenuIn(`<button click="toggle()">v</button><popover><button click="$body.toggle('heading', 4)">H4</button><dropdown><button click="toggle()">x</button></dropdown></popover>`), "<dropdown><button click=\"toggle()\">x",
			`<dropdown> không đặt trong <dropdown> của thanh công cụ của editor (dropdown ở %s): dropdown của thanh giữ nút mở nó, và trong <popover> các nút lệnh của editor`, []string{"<dropdown"}},
		{"a heading the dropdown has not", editorMenuIn(`<button click="toggle()">v</button><popover><button click="$body.toggle('heading', 7)">H7</button></popover>`), `click="$body`,
			`toggle('heading', 7): bài viết có tiêu đề mục từ mức 2 tới mức 6; viết toggle('heading', 2) tới toggle('heading', 6)`, nil},
		{"a dropdown of commands beside the area", `<content><form method="post"><editor as="$body">` + editorBar + editorLink + `<dropdown><button click="toggle()">v</button><popover><button click="$body.toggle('heading', 4)">H4</button></popover></dropdown>` + area + `</editor></form></content>`, `click="$body`,
			`nút của editor (click="$body.toggle('heading', 4)") nằm chung khung với vùng viết: đặt các nút trong một khung riêng có alt, <box alt="Định dạng">…</box>, engine viết role="toolbar" lên khung đó`, nil},
		{"a list numbered", editorIn(``, `<box alt="F"><button click="toggle('list', 'numbered')">L</button></box>`+area), "click=",
			`toggle('list', 'numbered'): list nhận 'ordered' hay không gì, như <list ordered="true">`, nil},
		{"bold with a variant", editorIn(``, `<box alt="F"><button click="toggle('bold', 2)">B</button></box>`+area), "click=",
			`toggle('bold', 2): bold không nhận thêm gì, viết toggle('bold')`, nil},
		{"a link with no popover", editorIn(``, `<box alt="F"><button click="toggle('bold')">B</button><button click="link()">L</button></box>`+area), `click="link`,
			`link() mở ô địa chỉ mà editor chưa có: vẽ <popover> chứa <input value="{{ address }}" alt="Địa chỉ liên kết" />`, nil},
		{"a link of something else", editorIn(``, `<box alt="F"><button click="link('/x')">L</button></box>`+editorLink+area), `click="link('`,
			`link nhận address, chữ trong ô địa chỉ: link(address); mở ô địa chỉ là link()`, nil},
		{"the address linked from the toolbar", editorIn(``, `<box alt="F"><button click="link(address)">L</button></box>`+editorLink+area), `click="link(address)">L`,
			`link(address) gắn chữ trong ô địa chỉ: đặt nút này trong <popover> của editor, cạnh <input value="{{ address }}">`, nil},
		{"link() in the popover", editorIn(``, editorBar+`<popover><input value="{{ address }}" alt="Địa chỉ" /><button click="link()">Mở</button></popover>`+area), `click="link()">Mở`,
			`link() mở ô địa chỉ, mà nút này đã ở trong ô: nút gắn liên kết là link(address)`, nil},
		{"undo given something", editorIn(``, `<box alt="F"><button click="undo(2)">U</button></box>`+area), "click=",
			`undo() không nhận gì: viết undo()`, nil},
		{"a button in a for", editorIn(``, `<box alt="F"><button for="f of formats" click="toggle('bold')">B</button></box>`+area), "click=",
			`nút của editor không lặp bằng for (for ở %s): mỗi nút viết một lần, định dạng viết sẵn`, []string{"for="}},
		{"a button beside the area", editorIn(``, `<button click="toggle('bold')">B</button>`+area), "click=",
			`nút của editor (click="toggle('bold')") nằm chung khung với vùng viết: đặt các nút trong một khung riêng có alt, <box alt="Định dạng">…</box>, engine viết role="toolbar" lên khung đó`, nil},
		{"a toolbar without alt", editorIn(``, `<box><button click="toggle('bold')">B</button></box>`+area), "<box",
			`thanh công cụ của editor cần alt: engine viết role="toolbar", alt là tên của thanh (<box alt="Định dạng">)`, nil},
		{"a toolbar that is a list", editorIn(``, `<list alt="F"><item><button click="toggle('bold')">B</button></item><item><button click="toggle('italic')">I</button></item></list>`+area), "<list",
			`thanh công cụ của editor là một <box>, <card> hay <section> có alt: các nút như nút ở %s nằm chung trong <list>`, []string{"<button"}},
		{"two popovers", editorIn(``, editorBar+editorLink+editorLink+area), "<popover><input value=\"{{ address }}\" alt=\"Địa chỉ liên kết\" /><button click=\"link(address)\">Gắn</button><button click=\"close()\">Huỷ</button></popover><content",
			`editor có một popover, khung của liên kết: <popover> ở %s đã là của nó`, []string{"<popover"}},
		{"a popover without an input", editorIn(``, editorBar+`<popover><button click="close()">Huỷ</button></popover>`+area), "<popover",
			`popover của editor cần đúng một <input> làm ô địa chỉ: <input value="{{ address }}" alt="Địa chỉ liên kết" />`, nil},
		{"two inputs", editorIn(``, editorBar+`<popover><input value="{{ address }}" alt="Địa chỉ" /><input value="{{ address }}" alt="Lại" /></popover>`+area), `<input value="{{ address }}" alt="Lại"`,
			`popover của editor có một ô địa chỉ: <input> ở %s đã là ô của nó`, []string{"<input"}},
		{"an address of type url", editorIn(``, editorBar+`<popover><input type="url" value="{{ address }}" alt="Địa chỉ" /></popover>`+area), "type=",
			`ô địa chỉ không viết type: engine viết inputmode="url"; type="url" chặn cả form khi ô giữ /trang`, nil},
		{"an address with a name", editorIn(``, editorBar+`<popover><input name="href" value="{{ address }}" alt="Địa chỉ" /></popover>`+area), `name="href"`,
			`ô địa chỉ không có name: nó không gửi lên server cùng bài viết`, nil},
		{"an address with autocomplete", editorIn(``, editorBar+`<popover><input autocomplete="url" value="{{ address }}" alt="Địa chỉ" /></popover>`+area), "autocomplete=",
			`autocomplete của ô địa chỉ là việc của engine (inputmode="url" autocomplete="off" autocapitalize="none" spellcheck="false"): bỏ autocomplete`, nil},
		{"an address of another name", editorIn(``, editorBar+`<popover><input value="{{ href }}" alt="Địa chỉ" /></popover>`+area), "value=\"{{ href",
			`ô địa chỉ của editor là <input value="{{ address }}" alt="Địa chỉ liên kết" />: nó ghi chữ người dùng gõ vào address, link(address) đọc nó`, nil},
		{"an address without alt", editorIn(``, editorBar+`<popover><input value="{{ address }}" /></popover>`+area), "<input",
			`ô địa chỉ của editor cần alt: tên của ô, cũng là tên của khung liên kết (alt="Địa chỉ liên kết")`, nil},
		{"two errors", editorIn(``, editorBar+`<popover><input value="{{ address }}" alt="Địa chỉ" /><error>a</error><error>b</error></popover>`+area), "<error>b",
			`popover của editor có một lời báo địa chỉ sai: <error> ở %s đã là của nó`, []string{"<error>a"}},
		{"a form inside", editorIn(``, editorBar+editorLink+area+`<form><button type="submit">x</button></form>`), "<form><button",
			`form không đặt trong <editor>: editor là một ô của form bao ngoài nó (<form method="post">…<editor>…</editor>…</form>)`, nil},
		{"a command in another event", editorIn(``, editorBar+editorLink+area+`<box click="toggle('bold')"><text>x</text></box>`), `click="toggle('bold')"><text`,
			`toggle('bold') là lệnh của editor: gọi bằng click của một <button> viết trong <editor>, cả click là lời gọi đó (<button click="toggle('bold')">), để chỗ chọn chữ còn nguyên khi bấm`, nil},
		{"a command on the host's ready", editorIn(` ready="undo()"`, editorBar+editorLink+area), "ready=",
			`undo() là lệnh của editor: gọi bằng click của một <button> viết trong <editor>, cả click là lời gọi đó (<button click="undo()">), để chỗ chọn chữ còn nguyên khi bấm`, nil},
		{"its state written", editorIn(``, editorBar+editorLink+area+`<button click="open = true">x</button>`), "click=\"open",
			`open của editor chỉ đọc: mở ô địa chỉ là link(), đóng là close()`, nil},
		{"its state called", editorIn(``, editorBar+editorLink+area+`<button click="words()">x</button>`), "click=\"words",
			`words là trạng thái của editor, không gọi được: đọc words; editor có: toggle, align, rule, link, unlink, undo, redo, close`, nil},
		{"another editor's method", editorIn(``, editorBar+editorLink+area+`<button click="run('bold')">x</button>`), "click=\"run",
			`editor không có run: định dạng là toggle với tên viết sẵn: toggle('bold'), toggle('heading', 2)`, nil},
		{"a field two-way on its state", editorIn(``, editorBar+editorLink+area+`<input value="{{ words }}" alt="Số chữ" />`), "value=\"{{ words",
			`words của editor chỉ đọc: ô này ghi ngược chữ gõ vào words; chỉ để hiện thì thêm readonly="true"`, nil},
		{"a command through the alias", `<content><form method="post"><editor as="$body">` + editorBar + editorLink + area + `</editor><button click="$body.toggle('bold')">x</button></form></content>`, "click=\"$body",
			`$body.toggle('bold') ở ngoài <editor>: lệnh của editor là nút viết trong <editor> gọi tên trần (<button click="toggle('bold')">), để giữ chỗ chọn chữ khi bấm`, nil},
		{"a name it has not, through the alias", `<content><form method="post"><editor as="$body">` + editorBar + editorLink + area + `</editor><text>{{ $body.dirty }}</text></form></content>`, "{{ $body.dirty",
			`editor không có dirty: chưa lưu là unsaved ($body.unsaved)`, nil},
		{"contenteditable", `<content><box contenteditable="true"><text>x</text></box></content>`, "contenteditable=",
			`contenteditable là chữ của web; vùng soạn chữ là <editor> có một <content name="body" /> bên trong, đặt trong <form method="post">`, nil},
		{"a wysiwyg", `<content><wysiwyg /></content>`, "<wysiwyg",
			`không có thẻ <wysiwyg>: vùng soạn chữ là <editor> có một <content name="body" /> bên trong, đặt trong <form method="post">`, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			// told where it is written: a second part at the last place its needle is written
			line, column := editorAt(c.source, c.needle, strings.HasPrefix(c.name, "two "))
			args := make([]any, len(c.refs))
			for i, ref := range c.refs {
				args[i] = strings.TrimSuffix(where("content.ui", c.source, ref), ": ")
			}
			says := c.says
			if len(args) > 0 {
				says = fmt.Sprintf(says, args...)
			}
			mustFailExactly(t, content(c.source), "content.ui:"+itoa(line)+":"+itoa(column)+": "+says)
		})
	}
}

// editorAt is the line and column (in characters) of the first place needle is written in source, or
// of the last.
func editorAt(source, needle string, last bool) (int, int) {
	i := strings.Index(source, needle)
	if last {
		i = strings.LastIndex(source, needle)
	}
	line, column := 1, 1
	for _, r := range source[:i] {
		if r == '\n' {
			line, column = line+1, 1
			continue
		}
		column++
	}
	return line, column
}

// What works but is probably not what the author wants is a warning at its place, said once, and the
// page renders: an editor with no button (a phone has no keys to bold with), a popover nothing opens,
// a format button drawn with pressed: (the moment it is pressed, not the format being on), a submit kept
// disabled while the editor is empty (without JavaScript nothing is ever sent), an alt beside the
// field's label (the label names the area; the alt is left out). Toolbar v2 (21/10/2026): pressed: on an
// align button and on a format in a toolbar's dropdown too.
// CONTROL: the canonical editor warns of nothing (TestEditorCanonicalWire); each warning's call taken out
// of editor.go turned its row red (run 20/10/2026 on overlaid copies of editor.go). Toolbar v2 (run
// 21/10/2026 on an overlaid copy): with editorPressedWarning reading formats alone, the align row failed,
// and with the dropdown's commands not warned of (editorMenuButton without it), the dropdown's row.
func TestEditorWarnings(t *testing.T) {
	label := `<content><form method="post"><field><label>Nội dung</label><editor>` + editorBar + editorLink + editorArea + `</editor></field></form></content>`
	for _, c := range []struct{ name, source, needle, says string }{
		{"no button", editorIn(``, editorArea), "<editor",
			`editor không có nút nào: điện thoại không có phím tắt; vẽ ít nhất <button click="toggle('bold')"> và <button click="link()"> trong một <box alt="Định dạng">`},
		{"a popover nothing opens", editorIn(``, `<box alt="F"><button click="toggle('bold')">B</button></box>`+editorLink+editorArea), "<popover",
			`popover của editor chỉ mở bằng link(): chưa có nút <button click="link()">, nên khung liên kết không bao giờ hiện`},
		{"pressed: on a format", editorIn(``, `<box alt="F"><button click="toggle('bold')" pressed:color="brand">B</button><button click="link()">L</button></box>`+editorLink+editorArea), "pressed:",
			`pressed: là lúc đang nhấn; nút đang bật định dạng tô bằng active: (active:color)`},
		{"pressed: on an align", editorIn(``, `<box alt="F"><button click="align('center')" pressed:background="brand/15">C</button><button click="link()">L</button></box>`+editorLink+editorArea), "pressed:",
			`pressed: là lúc đang nhấn; nút đang bật định dạng tô bằng active: (active:background)`},
		{"pressed: in a toolbar's dropdown", `<content><form method="post"><editor as="$body"><box alt="F"><button click="link()">L</button><dropdown><button click="toggle()">v</button><popover>` +
			`<button click="$body.toggle('heading', 4)" pressed:color="brand">H4</button></popover></dropdown></box>` + editorLink + editorArea + `</editor></form></content>`, "pressed:",
			`pressed: là lúc đang nhấn; nút đang bật định dạng tô bằng active: (active:color)`},
		{"a submit kept while empty", `<content><form method="post"><editor as="$body">` + editorBar + editorLink + editorArea + `</editor><button type="submit" disabled="{{ $body.empty }}">Lưu</button></form></content>`, "disabled=",
			`nút gửi khoá theo $body.empty: không có JavaScript thì không gửi được; required="true" trên <content> đã chặn bài rỗng`},
		{"alt beside the label", label, `alt="Nội dung"`,
			`alt trên <content> không có tác dụng: <label> của <field> bao ngoài đã là tên của vùng viết (trang bỏ alt này)`},
	} {
		t.Run(c.name, func(t *testing.T) {
			compileWarns(t, content(c.source), where("content.ui", c.source, c.needle), c.says)
		})
	}
	// the alt left out: the label names the area, once
	html, _ := editorPage(t, label, "GET", nil)
	mustContain(t, html, `aria-labelledby="kitwork-field-1-label"`)
	mustNotContain(t, html, `aria-label="Nội dung"`)
}

// A toolbar is the nearest container of the command buttons that does not hold the area: two boxes with
// alt are two toolbars, each with its own first tab stop (tabindex 0, the rest -1, a button that is no
// command too), its name and the area it controls; a box around the toolbar and the popover makes no
// toolbar of its own; a toolbar may sit inside a box that holds the area as well. In an editor written
// readonly or disabled every command button is aria-disabled, the field carries the word and the area
// says it; a format button is never pressed at first paint.
// CONTROL: with the roving tabindex left out (editorToolbars writing no s.roving) and with a readonly
// editor's buttons left enabled, rows of this failed (run 20/10/2026 on overlaid copies of editor.go).
func TestEditorToolbarsAndTheirButtons(t *testing.T) {
	two := editorIn(``, `<box alt="Chữ"><button click="toggle('bold')">B</button><button click="toggle('italic')">I</button></box>`+
		`<box alt="Lịch sử"><button click="undo()">U</button><button click="redo()">R</button><button>Khác</button></box>`+editorArea)
	html := editorQuiet(t, two, "GET", nil)
	toolbars := regexp.MustCompile(`<div class="flex-column editor-toolbar" aria-label="([^"]*)" role="toolbar" aria-controls="kitwork-editor-1-area">`).FindAllStringSubmatch(html, -1)
	if len(toolbars) != 2 || toolbars[0][1] != "Chữ" || toolbars[1][1] != "Lịch sử" {
		t.Errorf("two toolbars named Chữ and Lịch sử, got %v:\n%s", toolbars, html)
	}
	tabs := regexp.MustCompile(`tabindex="(-?[01])"`).FindAllStringSubmatch(html, -1)
	var order []string
	for _, m := range tabs {
		order = append(order, m[1])
	}
	if got := strings.Join(order, " "); got != "0 -1 0 -1 -1" {
		t.Errorf("roving tabindex %q, want each toolbar's first button 0 and the rest -1 (0 -1 0 -1 -1):\n%s", got, html)
	}
	mustContain(t, html, editorNeutral+` tabindex="-1">Khác</button>`)
	if n := strings.Count(html, `aria-pressed="false"`); n != 2 {
		t.Errorf("aria-pressed written %d times, want twice (the two formats):\n%s", n, html)
	}

	// a box around the toolbar and the popover is no toolbar; a toolbar inside a box beside the area
	wrapped := editorIn(``, `<box gap="2"><box alt="Định dạng"><button click="toggle('bold')">B</button><button click="link()">L</button></box>`+editorLink+`</box>`+editorArea)
	html = editorQuiet(t, wrapped, "GET", nil)
	if n := strings.Count(html, `role="toolbar"`); n != 1 {
		t.Errorf("role=\"toolbar\" written %d times, want once:\n%s", n, html)
	}
	mustContain(t, html, `<div class="flex-column editor-toolbar" aria-label="Định dạng" role="toolbar"`)
	beside := editorIn(``, `<box gap="2"><box alt="Định dạng"><button click="toggle('bold')">B</button><button click="link()">L</button></box>`+editorLink+editorArea+`</box>`)
	html = editorQuiet(t, beside, "GET", nil)
	mustContain(t, html, `<div class="flex-column editor-toolbar" aria-label="Định dạng" role="toolbar" aria-controls="kitwork-editor-1-area">`)

	// readonly and disabled: every command button aria-disabled, the field and the area say it
	for _, c := range []struct{ word, field, area string }{
		{"readonly", ` readonly aria-label`, ` aria-readonly="true" translate`},
		{"disabled", ` disabled aria-label`, ` aria-readonly="true" aria-disabled="true" translate`},
	} {
		source := editorIn(``, editorBar+editorLink+`<content name="body" alt="Nội dung" `+c.word+`="true" />`)
		html := editorQuiet(t, source, "GET", nil)
		if commands, disabled := strings.Count(html, `data-kitwork-format=`)+strings.Count(html, `data-kitwork-command=`), strings.Count(html, `aria-disabled="true" data-kitwork-`); commands != 4 || disabled != commands {
			t.Errorf("%s: %d command buttons, %d of them aria-disabled; want all 4:\n%s", c.word, commands, disabled, html)
		}
		mustContain(t, html, `name="body"`+c.field, c.area)
	}
	// written false, the editor takes writing: only link, undo and redo start disabled
	html = editorQuiet(t, editorIn(``, editorBar+editorLink+`<content name="body" alt="Nội dung" readonly="false" disabled="false" />`), "GET", nil)
	mustContain(t, html, `data-kitwork-format="bold" aria-pressed="false" aria-keyshortcuts="Control+B" data-kitwork-event`)
	mustNotContain(t, html, ` readonly`, ` disabled `, `aria-disabled="true" translate`)
}

// The field around an editor wires its writing area: the label gets an id the area is labelled by and
// still names the field the form posts (for), the hint and the error describe both (the field too:
// without JavaScript it is the control shown, its error read with it), and the popover's address input
// is the editor's, never the field's control (no id of the field, no description). An editor outside a
// field is named by the alt of its <content>, on the area and on the field. A field without an editor
// is wired as it was: no id on its label or its error.
// CONTROL: with the field's label and error ids not made (generic without holdsEditor) and with the
// popover keeping the field (generic without clearing it there), this test failed (run 20/10/2026 on
// overlaid copies of compile.go); with editorContent writing the hint alone on the field (editor.go as
// it was before the review of 20/10/2026), the textarea's row failed.
func TestEditorFieldWiring(t *testing.T) {
	source := `<content><form method="post"><field><label>Nội dung</label><editor>` + editorBar + editorLink +
		`<content name="body" />` + `</editor><hint>Viết ngắn gọn.</hint><error>Bài cần nội dung.</error></field></form></content>`
	html := editorQuiet(t, source, "GET", nil)
	mustContain(t, html,
		`<label for="kitwork-field-1" id="kitwork-field-1-label">Nội dung</label>`,
		`role="textbox" aria-multiline="true" aria-labelledby="kitwork-field-1-label" aria-describedby="kitwork-field-1-hint kitwork-field-1-error" aria-readonly="true"`,
		`<textarea class="editor-field" id="kitwork-field-1" name="body" aria-describedby="kitwork-field-1-hint kitwork-field-1-error"></textarea>`,
		`<p class="hint" id="kitwork-field-1-hint">Viết ngắn gọn.</p>`,
		`<p class="error" id="kitwork-field-1-error">Bài cần nội dung.</p>`,
		`<input class="editor-address" aria-label="Địa chỉ liên kết" type="text"`)
	if n := strings.Count(html, `id="kitwork-field-1"`); n != 1 {
		t.Errorf("the field's id written %d times, want once, on the field the form posts:\n%s", n, html)
	}
	// a label deeper in the field names the area too
	html = editorQuiet(t, `<content><form method="post"><field><box flex="row"><label>Nội dung</label></box><editor>`+editorBar+editorLink+
		`<content name="body" /></editor></field></form></content>`, "GET", nil)
	mustContain(t, html, `<label for="kitwork-field-1" id="kitwork-field-1-label">Nội dung</label>`, `aria-labelledby="kitwork-field-1-label"`)
	// outside a field: the alt names the area and the field
	html = editorQuiet(t, editorIn(``, editorBar+editorLink+editorArea), "GET", nil)
	mustContain(t, html, `role="textbox" aria-multiline="true" aria-label="Nội dung" aria-readonly="true"`,
		`<textarea class="editor-field" name="body" aria-label="Nội dung"></textarea>`)
	// a field without an editor: as before
	html = editorQuiet(t, `<content><form method="post"><field><label>Tên</label><input name="name" /><error>Cần tên.</error></field></form></content>`, "GET", nil)
	mustContain(t, html, `<label for="kitwork-field-1">Tên</label>`, `<p class="error">Cần tên.</p>`)
}

// The page's stylesheet holds the editor's scope only on a page with an editor, once however many there
// are, with a height for each rows its editors write, ascending, each once; the richtext scope its area
// wears comes with it; and active: matches a button that is on (aria-pressed), as the format buttons
// are when the caret stands in their format. A page without an editor has none of it.
// CONTROL: with Rules.Editor writing no rows (editorScope without its loop) and with active: back to
// aria-current alone (values.go), this test failed (run 20/10/2026 on overlaid copies of
// stylesheet/editor.go and stylesheet/values.go).
func TestEditorStylesheet(t *testing.T) {
	page := `<content><form method="post"><editor>` + editorBar + editorLink + `<content name="a" alt="A" rows="12" /></editor>` +
		`<editor><box alt="F"><button click="toggle('bold')" active:background="brand/15">B</button></box><content name="b" alt="B" rows="6" /></editor>` +
		`<editor><box alt="G"><button click="toggle('italic')">I</button></box><content name="c" alt="C" rows="12" /></editor></form></content>`
	s := memorySite("", content(page))
	s.Reload = true
	html, css := renderWithCSS(t, s, ".", "/", nil)
	mustContain(t, html, `editor-rows-12`, `editor-rows-6`)
	for _, rule := range []string{
		".editor{position:relative;min-inline-size:0}\n",
		"@media (scripting:none){.editor-area,.editor-toolbar,.editor-popover,.editor [data-kitwork-command]{display:none!important}",
		".editor-area.editor-rows-6{min-block-size:calc(6*1.5em);min-block-size:6lh}\n.editor-area.editor-rows-12{min-block-size:calc(12*1.5em);min-block-size:12lh}\n",
		".richtext :where(a){color:var(--kitwork-link)",
		`.active\:background-brand\/15:where([aria-current]:not([aria-current=false]),[aria-pressed=true])`,
	} {
		if strings.Count(css, rule) != 1 {
			t.Errorf("want once in the page's stylesheet: %s\n%s", rule, css)
		}
	}
	if strings.Index(css, "@layer elements{") > strings.Index(css, ".editor{") {
		t.Errorf("the editor's scope is not in the elements layer:\n%s", css)
	}
	_, css = renderCSS(t, `<content><form method="post"><field><label>Tên</label><input name="name" /></field></form></content>`, nil)
	mustNotContain(t, css, ".editor", "editor-area", ".richtext")
}

// A toolbar holds its buttons, each always there: a popover or a component written inside it, and an
// if, a show (at any width) or a for on what it holds, are told at their place. The arrows of the
// toolbar would go into buttons that are not its own, and its one stop for Tab (the first button,
// tabindex="0" from the server) could be a button left out, hidden or repeated. An if on the toolbar
// itself is how it is hidden. Toolbar v2 (21/10/2026): a dropdown is held when it holds commands of the
// editor, and told what it holds when it holds none; the rule reaches into groups and into a dropdown.
// CONTROL: with editorToolbars collecting as it did before the review of 20/10/2026 (a popover and a
// component skipped, no word on a button read; a copy of editor.go through -overlay), every row
// compiled with no error and this test failed on each. Toolbar v2 (run 21/10/2026 on overlaid copies,
// controls-toolbar in the scratchpad): with a dropdown of no command told as any component, its row
// failed; with collect not going into groups and dropdowns, the popover in a group and the if in a
// dropdown compiled and their rows failed.
func TestEditorToolbarHoldsOnlyItsButtons(t *testing.T) {
	bar := func(inside string) string { return `<box alt="Định dạng">` + inside + `</box>` }
	bold, italic, link := `<button click="toggle('bold')">B</button>`, `<button click="toggle('italic')">I</button>`, `<button click="link()">L</button>`
	const at = `<box alt="Định dạng">`
	for _, c := range []struct{ name, source, needle, says string }{
		{"a popover inside", editorIn(``, bar(bold+link+editorLink)+editorArea), "<popover",
			`<popover> không đặt trong thanh công cụ của editor (thanh ở %s): phím mũi tên của thanh đi qua từng nút của nó, còn nút của <popover> là của <popover>; đặt <popover> cạnh thanh, trong <editor>`},
		{"a dropdown of no command", editorIn(``, bar(bold+`<dropdown><button>Thêm</button></dropdown>`)+editorArea), "<dropdown",
			`<dropdown> trong thanh công cụ của editor (thanh ở %s) giữ thêm lệnh của editor: nút mở nó <button click="toggle()">, và trong <popover> các nút gọi editor qua alias (<button click="$body.toggle('heading', 4)">); một dropdown khác đặt cạnh thanh, trong <editor>`},
		{"another component inside", editorIn(``, bar(bold+`<appearance click="toggle()">Sáng tối</appearance>`)+editorArea), "<appearance",
			`<appearance> không đặt trong thanh công cụ của editor (thanh ở %s): phím mũi tên của thanh đi qua từng nút của nó, còn nút của <appearance> là của <appearance>; đặt <appearance> cạnh thanh, trong <editor>`},
		{"a popover in a group", editorIn(``, bar(`<group>`+bold+link+editorLink+`</group>`)+editorArea), "<popover",
			`<popover> không đặt trong thanh công cụ của editor (thanh ở %s): phím mũi tên của thanh đi qua từng nút của nó, còn nút của <popover> là của <popover>; đặt <popover> cạnh thanh, trong <editor>`},
		{"an if on a group", editorIn(``, bar(`<group if="more">`+bold+`</group><group>`+italic+`</group>`)+editorArea), "if=",
			`if không có trong thanh công cụ của editor (thanh ở %s): nút của thanh luôn có mặt, nút đầu là điểm dừng của Tab; viết if trên cả thanh`},
		{"an if in a toolbar's dropdown", `<content><form method="post"><editor as="$body">` + bar(bold+`<dropdown><button click="toggle()">v</button><popover><button if="deep" click="$body.toggle('heading', 4)">H4</button></popover></dropdown>`) + editorArea + `</editor></form></content>`, "if=",
			`if không có trong thanh công cụ của editor (thanh ở %s): nút của thanh luôn có mặt, nút đầu là điểm dừng của Tab; viết if trên cả thanh`},
		{"an if on a button", editorIn(``, bar(`<button if="canBold" click="toggle('bold')">B</button>`+italic)+editorArea), "if=",
			`if không có trong thanh công cụ của editor (thanh ở %s): nút của thanh luôn có mặt, nút đầu là điểm dừng của Tab; viết if trên cả thanh`},
		{"a show on a button", editorIn(``, bar(`<button show="flag" click="toggle('bold')">B</button>`+italic)+editorArea), "show=",
			`show không có trong thanh công cụ của editor (thanh ở %s): nút của thanh luôn có mặt, nút đầu là điểm dừng của Tab; viết show trên cả thanh`},
		{"a show at a width", editorIn(``, bar(bold+`<button medium:show="false" click="toggle('italic')">I</button>`)+editorArea), "medium:show=",
			`medium:show không có trong thanh công cụ của editor (thanh ở %s): nút của thanh luôn có mặt, nút đầu là điểm dừng của Tab; viết medium:show trên cả thanh`},
		{"an if around buttons", editorIn(``, bar(`<box if="more">`+bold+`</box>`+italic)+editorArea), "if=",
			`if không có trong thanh công cụ của editor (thanh ở %s): nút của thanh luôn có mặt, nút đầu là điểm dừng của Tab; viết if trên cả thanh`},
		{"a for on a button", editorIn(``, bar(bold+`<button for="x of xs">{{ x }}</button>`)+editorArea), "for=",
			`for không có trong thanh công cụ của editor (thanh ở %s): nút của thanh viết từng nút một, nút đầu là điểm dừng của Tab`},
	} {
		t.Run(c.name, func(t *testing.T) {
			says := fmt.Sprintf(c.says, strings.TrimSuffix(where("content.ui", c.source, at), ": "))
			mustFailExactly(t, content(c.source), where("content.ui", c.source, c.needle)+says)
		})
	}
	// an if on the toolbar itself hides the whole of it
	html := editorQuiet(t, editorIn(``, `<box alt="Định dạng" if="tools">`+bold+italic+`</box>`+editorArea), "GET", map[string]any{"tools": true})
	mustContain(t, html, `role="toolbar"`, `tabindex="0" data-kitwork-format="bold"`)
}

// The label of a field that holds an editor is the name of its writing area (aria-labelledby points at
// it): an if or a for that could leave it out, on the label or around it, is told at its place, with
// where the label is.
// CONTROL: with the field's label taken as it was before the review of 20/10/2026 (compile.go asking
// only whether the field holds one; a copy through -overlay), both rows compiled with no error, the area
// labelled by an id no element may carry.
func TestEditorFieldLabelIsAlwaysThere(t *testing.T) {
	for _, c := range []struct{ name, label, needle string }{
		{"an if on the label", `<label if="named">Nội dung</label>`, "if="},
		{"a label in an if", `<box if="named"><label>Nội dung</label></box>`, "if="},
	} {
		t.Run(c.name, func(t *testing.T) {
			source := `<content><form method="post"><field>` + c.label + `<editor>` + editorBar + editorLink + `<content name="body" /></editor></field></form></content>`
			says := fmt.Sprintf(`if này có thể bỏ <label> ở %s, tên của vùng viết của editor (aria-labelledby trỏ vào nó): <label> của field có editor luôn có mặt`,
				strings.TrimSuffix(where("content.ui", source, "<label"), ": "))
			mustFailExactly(t, content(source), where("content.ui", source, c.needle)+says)
		})
	}
}

// An editor reached through a layout the field calls (<field><label>…</label><post-editor /></field>,
// the editor written in post-editor.ui) is the field's as one written in it: the field's label names
// its writing area, its error describes it and the field it posts, the label's for is that field, and
// nothing asks for an alt the label already gives.
// CONTROL: with fieldHolds walking the field's own markup alone (editor.go before the review of
// 20/10/2026; a copy through -overlay), the page did not compile: "vùng viết chưa có tên đọc được".
func TestEditorFieldSeesThroughALayout(t *testing.T) {
	s := memorySite("", map[string]string{
		"/site/content.ui":     `<content><form method="post"><field><label>Nội dung</label><post-editor /><error>Bài cần nội dung.</error></field></form></content>`,
		"/site/post-editor.ui": `<box gap="2"><editor>` + editorBar + editorLink + `<content name="body" /></editor></box>`,
	})
	s.Reload = true
	page, err := s.Compile("/site")
	if err != nil {
		t.Fatalf("an editor in a layout the field calls compiles; got:\n%v", err)
	}
	if warned := page.Warnings(); len(warned) > 0 {
		t.Errorf("want no warning, got %v", warned)
	}
	html := page.Render(Request{Path: "/", Method: "GET"})
	mustContain(t, html,
		`<label for="kitwork-field-1" id="kitwork-field-1-label">Nội dung</label>`,
		`role="textbox" aria-multiline="true" aria-labelledby="kitwork-field-1-label" aria-describedby="kitwork-field-1-error" aria-readonly="true"`,
		`<textarea class="editor-field" id="kitwork-field-1" name="body" aria-describedby="kitwork-field-1-error"></textarea>`,
		`<p class="error" id="kitwork-field-1-error">Bài cần nội dung.</p>`)
}

// A toolbar may hold <group>s (toolbar v2, 21/10/2026): each a group of its buttons, role="group", named
// by its alt when one is written (a group without alt is still a group), class editor-group; the stop
// for Tab runs across every group and a button beside them, one toolbar with one tabindex="0"; when
// every command stands in one group the toolbar is still what holds the group. The page's stylesheet
// draws the line between two groups once, on the start edge of the second (mirrored right to left).
// CONTROL: (run 21/10/2026 on overlaid copies, controls-toolbar in the scratchpad) with a group left
// without role="group", with editorToolbars taking the nearest container as the toolbar even when it is
// a group (no step out of it), and with the line taken out of stylesheet/editor.go, this test failed
// each time.
func TestEditorToolbarGroups(t *testing.T) {
	source := editorIn(``, `<box alt="Định dạng" flex="row"><group alt="Chữ" flex="row"><button click="toggle('bold')">B</button><button click="toggle('italic')">I</button></group>`+
		`<group flex="row"><button click="align('center')">C</button></group><button click="undo()">U</button></box>`+editorArea)
	html := editorQuiet(t, source, "GET", nil)
	mustContain(t, html,
		`<div class="flex-row editor-toolbar" aria-label="Định dạng" role="toolbar" aria-controls="kitwork-editor-1-area"><div class="flex-row editor-group" aria-label="Chữ" role="group">`+
			editorNeutral+` tabindex="0" data-kitwork-format="bold"`,
		`</button></div><div class="flex-row editor-group" role="group">`+editorNeutral+` tabindex="-1" data-kitwork-command="align-center" aria-pressed="false"`,
		`</button></div>`+editorNeutral+` tabindex="-1" data-kitwork-command="undo"`)
	var order []string
	for _, m := range regexp.MustCompile(`tabindex="(-?[01])"`).FindAllStringSubmatch(html, -1) {
		order = append(order, m[1])
	}
	if got := strings.Join(order, " "); got != "0 -1 -1 -1" {
		t.Errorf("roving tabindex %q, want one stop across the groups (0 -1 -1 -1):\n%s", got, html)
	}

	// every command in one group: the box around it is the toolbar
	one := editorIn(``, `<box alt="Định dạng"><group alt="Chữ"><button click="toggle('bold')">B</button><button click="toggle('italic')">I</button></group></box>`+editorArea)
	html = editorQuiet(t, one, "GET", nil)
	mustContain(t, html, `<div class="flex-column editor-toolbar" aria-label="Định dạng" role="toolbar" aria-controls="kitwork-editor-1-area"><div class="flex-column editor-group" aria-label="Chữ" role="group">`)
	if n := strings.Count(html, `role="toolbar"`); n != 1 {
		t.Errorf("role=\"toolbar\" written %d times, want once:\n%s", n, html)
	}

	s := memorySite("", content(source))
	s.Reload = true
	_, css := renderWithCSS(t, s, ".", "/", nil)
	if rule := ".editor-group+.editor-group{border-inline-start:1px solid var(--line);padding-inline-start:0.75rem}\n"; strings.Count(css, rule) != 1 {
		t.Errorf("want once in the page's stylesheet: %s\n%s", rule, css)
	}
}

// A <dropdown> in a toolbar holds more commands (toolbar v2, 21/10/2026). Its trigger, the bare toggle()
// outside its popover, is one stop of the toolbar where it stands (after bold, before italic: 0 -1 -1
// -1), aria-controls its popover and has aria-expanded bound to the dropdown's open; its popover gets an
// id, numbered by dropdown, or keeps the author's; each command in it, called through the alias, carries
// the wire of a toolbar's button (a format with aria-pressed, an align's command with aria-pressed, rule's
// command, undo's keys and aria-disabled) and no tabindex: the popover is no stop of the toolbar. The
// page compiles with no warning: neither the trigger's toggle() nor close() on the dropdown is a second
// meaning beside the editor's, and the calls through the alias are not told as a call from elsewhere.
// In an editor written readonly every command in a dropdown is aria-disabled too.
// CONTROL: (run 21/10/2026 on overlaid copies, controls-toolbar in the scratchpad) with editorMenuElement
// writing nothing, with checkEditors telling every call through the alias, and with checkNames warning
// of the dropdown's toggle() and close() (host.go without editorMenuOwn), this test failed each time.
func TestEditorToolbarDropdown(t *testing.T) {
	source := `<content><form method="post"><editor as="$body"><box alt="Định dạng"><button click="toggle('bold')">B</button>` +
		`<dropdown click-outside="close()" keydown:escape="close()"><button click="toggle()">Thêm</button><popover>` +
		`<button click="$body.toggle('heading', 4)">H4</button><button click="$body.align('center')">C</button><button click="$body.rule()">—</button><button click="$body.undo()">U</button>` +
		`</popover></dropdown>` +
		`<dropdown><button click="toggle()">Khác</button><popover><button click="$body.toggle('code', 'block')">Mã</button></popover></dropdown>` +
		`<button click="toggle('italic')">I</button></box>` + editorArea + `</editor></form></content>`
	html := editorQuiet(t, source, "GET", nil)
	mustContain(t, html,
		editorNeutral+` tabindex="-1" aria-controls="kitwork-editor-1-menu-1" data-kitwork-bind="aria-expanded: open" data-kitwork-event="click: toggle()" aria-expanded="false">Thêm</button>`,
		`<div class="popover flex-column" hidden id="kitwork-editor-1-menu-1" data-kitwork-bind="show: open">`+
			editorNeutral+` data-kitwork-format="heading-4" aria-pressed="false" data-kitwork-event="click: $body.toggle(&#39;heading&#39;, 4)">H4</button>`+
			editorNeutral+` data-kitwork-command="align-center" aria-pressed="false" data-kitwork-event="click: $body.align(&#39;center&#39;)">C</button>`+
			editorNeutral+` data-kitwork-command="rule" data-kitwork-event="click: $body.rule()">—</button>`+
			editorNeutral+` data-kitwork-command="undo" aria-keyshortcuts="Control+Z" aria-disabled="true" data-kitwork-event="click: $body.undo()">U</button></div>`,
		editorNeutral+` tabindex="-1" aria-controls="kitwork-editor-1-menu-2" data-kitwork-bind="aria-expanded: open" data-kitwork-event="click: toggle()" aria-expanded="false">Khác</button>`,
		`<div class="popover flex-column" hidden id="kitwork-editor-1-menu-2" data-kitwork-bind="show: open">`+
			editorNeutral+` data-kitwork-format="code-block" aria-pressed="false" data-kitwork-event="click: $body.toggle(&#39;code&#39;, &#39;block&#39;)">Mã</button></div>`)
	var order []string
	for _, m := range regexp.MustCompile(`tabindex="(-?[01])"`).FindAllStringSubmatch(html, -1) {
		order = append(order, m[1])
	}
	if got := strings.Join(order, " "); got != "0 -1 -1 -1" {
		t.Errorf("roving tabindex %q, want bold, the two triggers and italic (0 -1 -1 -1), nothing in a popover:\n%s", got, html)
	}

	// the author's id on the popover is the one the trigger controls
	named := `<content><form method="post"><editor as="$body"><box alt="F"><button click="toggle('bold')">B</button><dropdown><button click="toggle()">v</button>` +
		`<popover id="headings"><button click="$body.toggle('heading', 5)">H5</button></popover></dropdown></box>` + editorArea + `</editor></form></content>`
	html = editorQuiet(t, named, "GET", nil)
	mustContain(t, html, ` aria-controls="headings" data-kitwork-bind="aria-expanded: open"`, `id="headings"`)
	mustNotContain(t, html, "kitwork-editor-1-menu")

	// readonly: every command in a dropdown is disabled as the toolbar's are
	readonly := strings.Replace(source, editorArea, `<content name="body" alt="Nội dung" readonly="true" />`, 1)
	html = editorQuiet(t, readonly, "GET", nil)
	if commands, disabled := strings.Count(html, `data-kitwork-format=`)+strings.Count(html, `data-kitwork-command=`), strings.Count(html, `aria-disabled="true" data-kitwork-event`); commands != 7 || disabled != commands {
		t.Errorf("readonly: %d command buttons, %d of them aria-disabled; want all 7:\n%s", commands, disabled, html)
	}
}
