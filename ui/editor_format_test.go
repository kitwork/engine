package ui

import (
	"fmt"
	"strings"
	"testing"
)

// The editor's formats (21/10/2026, editor.go): how a post is seen and typed, 'rich', 'markdown' or
// 'html', one value on the wire whatever the format (the cleaned HTML). These tests read what the
// compiler writes for the switches of format the author draws (a segment of buttons, a select), the
// state it seeds (format, dropped), and what it refuses and warns of, each at its place with its words.

// editorSegment is the canonical segment of the decision, as an author writes it: its own group, first
// in the toolbar.
const editorSegment = `<group alt="Viết bằng" flex="row" gap="1">
          <button click="format = 'rich'" active:background="brand/15">Rich</button>
          <button click="format = 'markdown'" active:background="brand/15">Markdown</button>
          <button click="format = 'html'" active:background="brand/15">HTML</button>
        </group>
        `

// editorWithSegment is the canonical editor with the segment first in its toolbar, the host written
// with words.
func editorWithSegment(words string) string {
	source := strings.Replace(editorCanonical, `<group alt="Kiểu chữ"`, editorSegment+`<group alt="Kiểu chữ"`, 1)
	return strings.Replace(source, `<editor as="$body" gap="2">`, `<editor as="$body" gap="2"`+words+`>`, 1)
}

// editorSwitch is the wire of a switch of format: its place in the toolbar's tab order, its command and
// the format it writes, aria-pressed bound to that format (painted as the editor starts), its click.
func editorSwitch(tab, value, label string, pressed bool) string {
	return `<button class="flex-row active:background-brand/15" type="button" tabindex="` + tab + `" data-kitwork-command="format" data-kitwork-format-value="` + value +
		`" data-kitwork-bind="aria-pressed: format == &#39;` + value + `&#39;" data-kitwork-event="click: format = &#39;` + value + `&#39;" aria-pressed="` +
		fmt.Sprint(pressed) + `">` + label + `</button>`
}

// The canonical segment, first in the canonical toolbar, is the wire the editor's component reads: its
// group (role="group", named by its alt), each switch with data-kitwork-command="format", the format it
// writes (data-kitwork-format-value) and aria-pressed bound to format == '<it>', which the server paints
// as the editor starts (rich pressed), so active: draws the one chosen: the binding is the one writer of
// it (the component writes none, since the integration of 21/10/2026), so the switch says what format
// and $body.format read; the first switch is the toolbar's one stop for Tab (tabindex 0) and Bold, first
// before, is now -1 like every other button. The host's first state holds format: 'rich' and dropped:
// [] after the counts. Written format="markdown" on the host, the editor starts there: the scope says
// so, Markdown is the one pressed, the word leaves the host (no format= on its element), $body.format
// outside paints it, and dropped inside reads an empty list (its show hidden, its join empty). It all
// compiles with no warning.
// CONTROL (run 21/10/2026 with go test -overlay on disabled copies of editor.go, the scratchpad's
// editor-formats/controls c1, c2, c3): editorButtonWire without the aria-pressed binding (no bind, no
// aria-pressed), bindings without format and dropped (the scope as before), and editorFormatWord
// leaving format on the host (editorWords then refuses it as the editor's state, so the page does not
// compile) each turned this red.
func TestEditorFormatSegmentWire(t *testing.T) {
	s := memorySite("", map[string]string{"/site/content.ui": editorWithSegment(""), "/site/view.ui": `<view><content /></view>`})
	s.Reload = true
	page, warned := compileDiagnose(t, s)
	if len(warned) > 0 {
		t.Errorf("want no warning, got:\n%s", strings.Join(warned, "\n"))
	}
	html := page.Render(Request{Path: "/", Method: "GET", Scope: editorPost})
	want := strings.Join([]string{
		`<div class="flex-column place-anchor gap-2 editor" data-kitwork-component="editor: $body" data-kitwork-scope="empty: false; words: 5; characters: 20; unsaved: false; open: false; address: &#39;&#39;; format: &#39;rich&#39;; dropped: []">`,
		`<div class="flex-row wrap-true align-left gap-3 editor-toolbar" aria-label="Định dạng" role="toolbar" aria-controls="kitwork-editor-1-area">`,
		`<div class="flex-row gap-1 editor-group" aria-label="Viết bằng" role="group">`,
		editorSwitch("0", "rich", "Rich", true),
		editorSwitch("-1", "markdown", "Markdown", false),
		editorSwitch("-1", "html", "HTML", false),
		`</div>`,
		`<div class="flex-row gap-1 editor-group" aria-label="Kiểu chữ" role="group">`,
		editorButtonClass + ` tabindex="-1" data-kitwork-format="bold" aria-pressed="false" aria-keyshortcuts="Control+B" data-kitwork-event="click: toggle(&#39;bold&#39;)">`,
	}, "")
	if !strings.Contains(html, want) {
		t.Errorf("the canonical segment:\n got  %s\n want %s", html, want)
	}
	if n := strings.Count(html, `tabindex="0"`); n != 1 {
		t.Errorf("tabindex=\"0\" written %d times, want once (the toolbar's one stop, Rich):\n%s", n, html)
	}

	// it starts in markdown: the scope, the switch pressed, the alias outside, dropped inside
	source := strings.Replace(editorWithSegment(` format="markdown"`), `<textarea name="body"`, `<text show="dropped.length">Đã bỏ {{ dropped.join(', ') }}</text>
      <textarea name="body"`, 1)
	source = strings.Replace(source, `<text show="$body.unsaved">Chưa lưu</text>`, `<text show="$body.unsaved">Chưa lưu</text><text>Viết bằng {{ $body.format }}</text>`, 1)
	html = editorQuiet(t, source, "GET", editorPost)
	mustContain(t, html,
		`data-kitwork-scope="empty: false; words: 5; characters: 20; unsaved: false; open: false; address: &#39;&#39;; format: &#39;markdown&#39;; dropped: []">`,
		editorSwitch("0", "rich", "Rich", false), editorSwitch("-1", "markdown", "Markdown", true), editorSwitch("-1", "html", "HTML", false),
		`<p data-kitwork-bind="show: dropped.length; text: [&#39;Đã bỏ &#39;, (dropped.join(&#39;, &#39;))].join(&#39;&#39;)" hidden>Đã bỏ </p>`,
		`>Viết bằng markdown</p>`)
	mustNotContain(t, html, `format="markdown"`, `aria-pressed="true">Rich`)
}

// A <select value="{{ format }}" alt="…"> in the editor switches the format too: the engine writes
// data-kitwork-command="format" on it (the component's mark) and the kernel's two-way binding
// (data-kitwork-bind="value: format", with no value attribute: a <select> has none), names it by its
// alt, and the server paints the option of the format the editor starts in selected. The field around
// the editor is the writing area's: the select never takes its id nor its hint, so the field's label
// still names the field the form posts (its id written once). A select in a <field> of its own inside
// the editor is that field's control, named by its label, alt not needed.
// CONTROL (run 21/10/2026 with go test -overlay, controls c4, c5, c13, c2, c3): compile.go's select
// taking the field around whatever (without holdsField) wrote the field's id twice, the label naming
// the select; editorElement without the select's command left data-kitwork-command off; compile.go
// without the select's binding (selectBind) left it one way; bindings without format left no option
// selected; format left on the host did not compile; each turned this red.
func TestEditorFormatSelect(t *testing.T) {
	options := `<option value="rich">Rich</option><option value="markdown">Markdown</option><option value="html">HTML</option>`
	source := `<content><form method="post"><field><label>Nội dung</label><editor as="$body" format="markdown">` +
		`<box alt="Định dạng" flex="row"><button click="toggle('bold')">B</button><select value="{{ format }}" alt="Viết bằng">` + options + `</select></box>` +
		`<textarea name="body" /></editor><hint>{{ $body.words }} từ</hint></field></form></content>`
	html := editorQuiet(t, source, "GET", nil)
	mustContain(t, html,
		`<select aria-label="Viết bằng" data-kitwork-command="format" data-kitwork-bind="value: format"><option value="rich">Rich</option><option value="markdown" selected>Markdown</option><option value="html">HTML</option></select>`,
		`<label for="kitwork-field-1" id="kitwork-field-1-label">Nội dung</label>`,
		`<textarea class="editor-field" id="kitwork-field-1" name="body" aria-describedby="kitwork-field-1-hint"></textarea>`)
	if n := strings.Count(html, `id="kitwork-field-1"`); n != 1 {
		t.Errorf("the field's id written %d times, want once (the field the form posts):\n%s", n, html)
	}
	if n := strings.Count(html, `aria-describedby="kitwork-field-1-hint"`); n != 2 {
		t.Errorf("the hint describes %d elements, want 2 (the area and the field):\n%s", n, html)
	}

	// in a field of its own inside the editor: that field's control, named by its label
	own := `<content><form method="post"><field><label>Nội dung</label><editor>` +
		`<box alt="Định dạng" flex="row"><button click="toggle('bold')">B</button><field><label>Viết bằng</label><select value="{{ format }}">` + options + `</select></field></box>` +
		`<textarea name="body" /></editor></field></form></content>`
	html = editorQuiet(t, own, "GET", nil)
	mustContain(t, html, `<label for="kitwork-field-2">Viết bằng</label><select id="kitwork-field-2" data-kitwork-command="format" data-kitwork-bind="value: format"><option value="rich" selected>Rich</option>`,
		`<textarea class="editor-field" id="kitwork-field-1" name="body"></textarea>`)
}

// A switch of format finds its toolbar as a command button does: a box holding only switches, with alt,
// is a toolbar of its own (role, name, one stop for Tab); in a readonly or disabled editor, which keeps
// the format it starts in (the component refuses a switch there), the switches are aria-disabled like
// every command, still bound and painted pressed for the format it starts in, and a select of format is
// disabled and aria-disabled (once, written disabled by its author too), as the component writes them
// once it starts; in an editor that takes writing, none of them. An editor whose only buttons are
// switches still hears that it has no command (a phone has no keys to bold with).
// CONTROL (run 21/10/2026 with go test -overlay, controls c6, c7, c1): editorParts leaving the switches
// out of the toolbars' buttons made the box no toolbar (this test red, and the switch beside the area
// of the refusals compiled), the warning counting every button (switches too) left the editor of
// switches unwarned, and no aria-pressed binding left the readonly row without its pressed switch.
// CONTROL (run 21/10/2026, the integration's c14 and c15 in the scratchpad's editor-formats/integrate):
// editorButtonWire leaving a switch enabled in such an editor (the wire before the JS and Go sides were
// made to agree) turned the readonly and disabled rows red; editorElement writing the select without
// its disabled turned the select's rows red.
func TestEditorFormatSwitchesFindAToolbar(t *testing.T) {
	switches := `<box alt="Viết bằng" flex="row"><button click="format = 'rich'">Rich</button><button click="format = 'markdown'">Markdown</button></box>`
	html := editorQuiet(t, editorIn(``, switches+editorBar+editorLink+editorArea), "GET", nil)
	mustContain(t, html,
		`<div class="flex-row editor-toolbar" aria-label="Viết bằng" role="toolbar" aria-controls="kitwork-editor-1-area">`,
		`tabindex="0" data-kitwork-command="format" data-kitwork-format-value="rich"`,
		`tabindex="-1" data-kitwork-command="format" data-kitwork-format-value="markdown"`,
		`<div class="flex-column editor-toolbar" aria-label="Định dạng" role="toolbar" aria-controls="kitwork-editor-1-area">`)
	if n := strings.Count(html, `tabindex="0"`); n != 2 {
		t.Errorf("tabindex=\"0\" written %d times, want twice (each toolbar's stop):\n%s", n, html)
	}
	options := `<option value="rich">Rich</option><option value="markdown">Markdown</option>`
	for _, word := range []string{"readonly", "disabled"} {
		html := editorQuiet(t, editorIn(``, switches+editorBar+editorLink+`<textarea name="body" alt="Nội dung" `+word+`="true" />`), "GET", nil)
		mustContain(t, html, `aria-disabled="true" data-kitwork-event="click: toggle(&#39;bold&#39;)"`,
			`data-kitwork-format-value="rich" aria-disabled="true" data-kitwork-bind="aria-pressed: format == &#39;rich&#39;" data-kitwork-event="click: format = &#39;rich&#39;" aria-pressed="true">`,
			`data-kitwork-format-value="markdown" aria-disabled="true" data-kitwork-bind="aria-pressed: format == &#39;markdown&#39;" data-kitwork-event="click: format = &#39;markdown&#39;" aria-pressed="false">`)
		// a select of format: disabled, aria-disabled, its option of the start still selected
		chosen := editorQuiet(t, editorIn(``, `<box alt="V"><button click="toggle('italic')">I</button><select value="{{ format }}" alt="Viết bằng">`+options+`</select></box>`+
			editorBar+editorLink+`<textarea name="body" alt="Nội dung" `+word+`="true" />`), "GET", nil)
		mustContain(t, chosen, `<select aria-label="Viết bằng" data-kitwork-command="format" disabled aria-disabled="true" data-kitwork-bind="value: format"><option value="rich" selected>Rich</option>`)
		// written disabled by its author too: disabled once
		twice := editorQuiet(t, editorIn(``, `<box alt="V"><button click="toggle('italic')">I</button><select value="{{ format }}" alt="Viết bằng" disabled="true">`+options+`</select></box>`+
			editorBar+editorLink+`<textarea name="body" alt="Nội dung" `+word+`="true" />`), "GET", nil)
		tag := twice[strings.Index(twice, "<select"):]
		tag = tag[:strings.Index(tag, ">")+1]
		if n := strings.Count(tag, " disabled"); n != 1 || !strings.Contains(tag, `aria-disabled="true"`) {
			t.Errorf("%s, the select written disabled: disabled written %d times, want once, and aria-disabled:\n%s", word, n, tag)
		}
	}
	// an editor that takes writing: nothing of it disabled
	html = editorQuiet(t, editorIn(``, `<box alt="V"><button click="format = 'rich'">Rich</button><select value="{{ format }}" alt="Viết bằng">`+options+`</select></box>`+editorBar+editorLink+editorArea), "GET", nil)
	mustContain(t, html, `<select aria-label="Viết bằng" data-kitwork-command="format" data-kitwork-bind="value: format">`,
		`data-kitwork-format-value="rich" data-kitwork-bind="aria-pressed: format == &#39;rich&#39;" data-kitwork-event="click: format = &#39;rich&#39;" aria-pressed="true">`)
	only := editorIn(``, switches+editorArea)
	compileWarns(t, content(only), where("content.ui", only, "<editor"),
		`editor không có nút nào: điện thoại không có phím tắt; vẽ ít nhất <button click="toggle('bold')"> và <button click="link()"> trong một <box alt="Định dạng">`)
	pressed := editorIn(``, `<box alt="V"><button click="format = 'html'" pressed:background="brand/15">HTML</button></box>`+editorBar+editorLink+editorArea)
	compileWarns(t, content(pressed), where("content.ui", pressed, "pressed:"), `pressed: là lúc đang nhấn; nút của cách viết đang chọn tô bằng active: (active:background)`)
}

// Each format written wrong is told at its place in its words: on the host (a format the editor has not,
// by its other names, code, an abbreviation, from data, with a prefix; the words other editors take for
// it), on a switch (the same names, a format not written out, in the link's popover, in a for, in a
// dropdown, beside the area), any other write of format (another event, beside another write, the host's
// ready, ++), dropped written or on the host, a field two-way on format that is no select of the editor
// (an input, a select in a dropdown), a select of format written wrong (an option that is no format or
// from data, no alt, a name, without the format it starts in, in the link's popover, in a for), what is
// read through the alias by another name, and format called.
// CONTROL (run 21/10/2026 with go test -overlay on disabled copies of editor.go, the scratchpad's
// editor-formats/controls c3, c6, c8 to c12): editorFormatProblem taking every value (the eleven rows
// of a format by another name, on the host, a switch or an option), editorFormatWord leaving format on
// the host (the seven rows of the host's format and the select without the format it starts in),
// editorEvents taking every write of format (the four writes), editorFormatField off (the input and
// the select in a dropdown), editorSelectOf's checks off (the eight rows of a select), editorMenuButton
// taking a switch in a dropdown (that row) and the switches left out of the toolbars (the switch
// beside the area) each turned their rows red. The two rows of the review of 21/10/2026 (a field
// two-way on $body.format outside the editor, a select and an input): with checkEditors taking it
// (fix/controls.py in the scratchpad's editor-formats, a copy of editor.go through -overlay), both
// compiled ("expected a compile error").
func TestEditorFormatRefusalsAtTheirPlace(t *testing.T) {
	area := editorArea
	bar := editorBar + editorLink
	switchesIn := func(inside string) string { return editorIn(``, `<box alt="V">`+inside+`</box>`+bar+area) }
	options := `<option value="rich">Rich</option><option value="markdown">Markdown</option>`
	selectIn := func(words, inside string) string {
		return editorIn(words, `<box alt="V"><button click="toggle('italic')">I</button>`+inside+`</box>`+bar+area)
	}
	const (
		write   = `cách viết đổi bằng click của một <button> viết trong <editor>, cả click là phép gán đó (<button click="format = 'markdown'">), hay bằng một <select value="{{ format }}" alt="Viết bằng"> trong <editor>`
		field   = `ô nối hai chiều với format: cách viết đổi bằng nút trong <editor> (<button click="format = 'markdown'">) hay một <select value="{{ format }}" alt="Viết bằng"> viết thẳng trong <editor>`
		outside = `ô nối hai chiều với $body.format ở ngoài <editor>: qua alias format chỉ đọc; cách viết đổi bằng nút trong <editor> (<button click="format = 'markdown'">) hay một <select value="{{ format }}" alt="Viết bằng"> viết thẳng trong <editor>`
	)
	for _, c := range []struct {
		name, source, needle, says string
		refs                       []string
	}{
		{"code on the host", editorIn(` format="code"`, bar+area), "format=",
			`format="code": code không phải một cách viết: mã trong bài là toggle('code') trong dòng, toggle('code', 'block') một khối; xem và sửa bài bằng HTML là format="html"`, nil},
		{"md on the host", editorIn(` format="md"`, bar+area), "format=",
			`format="md": md là chữ viết tắt; .ui viết đủ chữ: format="markdown"`, nil},
		{"wysiwyg on the host", editorIn(` format="wysiwyg"`, bar+area), "format=",
			`format="wysiwyg": cách viết thấy như đọc tên là rich, viết format="rich"`, nil},
		{"a format it has not, on the host", editorIn(` format="markdwon"`, bar+area), "format=",
			`format="markdwon": editor không có cách viết markdwon; có rich, markdown hay html; gần nhất: markdown`, nil},
		{"a format from data", editorIn(` format="{{ chosen }}"`, bar+area), "format=",
			`format trên <editor> là cách viết lúc trang mở, chữ viết sẵn (format="markdown"), không lấy từ dữ liệu`, nil},
		{"a format with a prefix", editorIn(` medium:format="html"`, bar+area), "medium:format=",
			`format không nhận tiền tố: cách viết lúc trang mở là một ở mọi độ rộng (format="markdown")`, nil},
		{"a format with nothing", editorIn(` format=""`, bar+area), "format=",
			`format cần một cách viết: format="rich", format="markdown" hay format="html"`, nil},
		{"markdown on the host", editorIn(` markdown="true"`, bar+area), "markdown=",
			`editor không có markdown: viết bằng Markdown lúc trang mở là format="markdown"; nút đổi cách viết là <button click="format = 'markdown'">`, nil},
		{"mode on the host", editorIn(` mode="markdown"`, bar+area), "mode=",
			`editor không có mode: cách viết là format, format="rich", format="markdown" hay format="html"`, nil},
		{"dropped on the host", editorIn(` as="$body" dropped="x"`, bar+area), "dropped=",
			`dropped là trạng thái của editor, không phải đầu vào: đọc {{ $body.dropped }}`, nil},
		{"a switch to code", switchesIn(`<button click="format = 'code'">C</button>`), "click=",
			`format = 'code': code không phải một cách viết: mã trong bài là toggle('code') trong dòng, toggle('code', 'block') một khối; xem và sửa bài bằng HTML là format = 'html'`, nil},
		{"a switch to md", switchesIn(`<button click="format = 'md'">M</button>`), "click=",
			`format = 'md': md là chữ viết tắt; .ui viết đủ chữ: format = 'markdown'`, nil},
		{"a switch to visual", switchesIn(`<button click="format = 'visual'">V</button>`), "click=",
			`format = 'visual': cách viết thấy như đọc tên là rich, viết format = 'rich'`, nil},
		{"a switch to text", switchesIn(`<button click="format = 'text'">T</button>`), "click=",
			`format = 'text': cách viết thấy như đọc tên là rich, viết format = 'rich'`, nil},
		{"a switch to source", switchesIn(`<button click="format = 'source'">S</button>`), "click=",
			`format = 'source': cách viết bằng mã HTML tên là html, viết format = 'html'`, nil},
		{"a switch to a format it has not", switchesIn(`<button click="format = 'htm'">H</button>`), "click=",
			`format = 'htm': editor không có cách viết htm; có rich, markdown hay html; gần nhất: html`, nil},
		{"a switch not written out", switchesIn(`<button click="format = chosen">H</button>`), "click=",
			`format = chosen: cách viết là chữ viết sẵn, format = 'rich', format = 'markdown' hay format = 'html': engine đọc nó để viết aria-pressed`, nil},
		{"a switch in the link's popover", editorIn(``, editorBar+`<popover><input value="{{ address }}" alt="Địa chỉ" /><button click="format = 'html'">H</button></popover>`+area), `click="format`,
			`format = 'html' đổi cách viết của editor: đặt nút này trong thanh công cụ, không trong <popover> của liên kết`, nil},
		{"a switch in a for", switchesIn(`<button for="f of formats" click="format = 'html'">H</button>`), "click=",
			`nút của editor không lặp bằng for (for ở %s): mỗi nút viết một lần, định dạng viết sẵn`, []string{"for="}},
		{"a switch in a toolbar's dropdown", editorMenuIn(`<button click="toggle()">v</button><popover><button click="$body.toggle('heading', 4)">H4</button><button click="format = 'markdown'">M</button></popover>`), `click="format`,
			`format = 'markdown' trong <dropdown>: nút đổi cách viết đứng trong thanh công cụ, một nhóm của nó (<group alt="Viết bằng"><button click="format = 'markdown'">…</group>), hay là một <select value="{{ format }}" alt="Viết bằng">`, nil},
		{"a switch beside the area", editorIn(``, bar+`<button click="format = 'markdown'">M</button>`+area), `click="format`,
			`nút của editor (click="format = 'markdown'") nằm chung khung với vùng viết: đặt các nút trong một khung riêng có alt, <box alt="Định dạng">…</box>, engine viết role="toolbar" lên khung đó`, nil},
		{"format written in another event", editorIn(``, bar+area+`<box click="format = 'html'"><text>x</text></box>`), `click="format`,
			`format = 'html': ` + write, nil},
		{"format written beside another write", switchesIn(`<button click="format = 'html'; open = false">H</button>`), "click=",
			`format = 'html': ` + write, nil},
		{"format written on the host's ready", editorIn(` ready="format = 'markdown'"`, bar+area), "ready=",
			`format = 'markdown': ` + write, nil},
		{"format counted up", switchesIn(`<button click="format++">+</button>`), "click=",
			`format++: ` + write, nil},
		{"dropped written", switchesIn(`<button click="dropped = []">x</button>`), "click=",
			`dropped của editor chỉ đọc: editor ghi vào đó tên các thẻ mà lần đổi cách viết, lần dán hay HTML trong Markdown vừa bỏ`, nil},
		{"format called", switchesIn(`<button click="format('html')">x</button>`), "click=",
			`format là trạng thái của editor, không gọi được: đọc format; editor có: toggle, align, rule, link, unlink, undo, redo, close`, nil},
		{"an input two-way on format", editorIn(``, bar+area+`<input value="{{ format }}" alt="Cách viết" />`), `value="{{ format`, field, nil},
		{"a select of format in a dropdown", editorIn(``, bar+area+`<dropdown><button click="toggle()">v</button><popover><select value="{{ format }}" alt="x">`+options+`</select></popover></dropdown>`), `value="{{ format`, field, nil},
		{"an option that is no format", selectIn(``, `<select value="{{ format }}" alt="Viết bằng">`+options+`<option value="code">Code</option></select>`), `value="code"`,
			`<option value="code">: code không phải một cách viết: mã trong bài là toggle('code') trong dòng, toggle('code', 'block') một khối; xem và sửa bài bằng HTML là <option value="html">`, nil},
		{"an option from data", selectIn(``, `<select value="{{ format }}" alt="Viết bằng"><option value="{{ chosen }}">x</option></select>`), `value="{{ chosen`,
			`option của <select value="{{ format }}"> là một cách viết viết sẵn: <option value="markdown">Markdown</option>`, nil},
		{"a select without alt", selectIn(``, `<select value="{{ format }}">`+options+`</select>`), `<select`,
			`<select value="{{ format }}"> cần alt: tên của nó (alt="Viết bằng"); <label> của <field> bao ngoài editor là tên của vùng viết`, nil},
		{"a select with a name", selectIn(``, `<select name="format" value="{{ format }}" alt="Viết bằng">`+options+`</select>`), `name="format"`,
			`<select value="{{ format }}"> không có name: cách viết không gửi lên server, form gửi bài bằng HTML`, nil},
		{"a select without the format it starts in", selectIn(` format="html"`, `<select value="{{ format }}" alt="Viết bằng">`+options+`</select>`), `<select`,
			`<select value="{{ format }}"> thiếu <option value="html">: editor bắt đầu ở html (format="html" ở %s), select thiếu nó thì hiện sai cách viết đang dùng`, []string{"format="}},
		{"a select without rich", selectIn(``, `<select value="{{ format }}" alt="Viết bằng"><option value="markdown">Markdown</option></select>`), `<select`,
			`<select value="{{ format }}"> thiếu <option value="rich">: editor bắt đầu ở rich, select thiếu nó thì hiện sai cách viết đang dùng`, nil},
		{"a select in the link's popover", editorIn(``, editorBar+`<popover><input value="{{ address }}" alt="Địa chỉ" /><select value="{{ format }}" alt="x">`+options+`</select></popover>`+area), `value="{{ format`,
			`<select value="{{ format }}"> đổi cách viết của editor: đặt nó cạnh thanh công cụ hay trong thanh, không trong <popover> của liên kết`, nil},
		{"a select in a for", editorIn(``, bar+`<box for="x of xs"><select value="{{ format }}" alt="Viết bằng">`+options+`</select></box>`+area), `value="{{ format`,
			`<select value="{{ format }}"> không lặp bằng for (for ở %s): mỗi editor một cách viết`, []string{"for="}},
		{"markdown read through the alias", `<content><form method="post"><editor as="$body">` + bar + area + `</editor><text>{{ $body.markdown }}</text></form></content>`, "{{ $body.markdown",
			`editor không có markdown: cách viết đang chọn là format ('rich', 'markdown' hay 'html'); bài gửi đi luôn là HTML của richtext ($body.format)`, nil},
		{"a select of format outside the editor", `<content><form method="post"><editor as="$body">` + bar + area + `</editor><select value="{{ $body.format }}" alt="Viết bằng">` + options + `</select></form></content>`, `value="{{ $body.format`,
			outside, nil},
		{"an input two-way on the format outside the editor", `<content><form method="post"><editor as="$body">` + bar + area + `</editor><input value="{{ $body.format }}" alt="Cách viết" /></form></content>`, `value="{{ $body.format`,
			outside, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			line, column := editorAt(c.source, c.needle, false)
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
