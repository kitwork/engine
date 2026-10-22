package ui

import (
	"fmt"
	"html"
	"strconv"
	"strings"
	"sync"

	"github.com/kitwork/engine/jit/hydrate"
	"github.com/kitwork/engine/ui/stylesheet"
	"github.com/kitwork/engine/utilities/richtext"
)

// Kitwork's editor (decisions of 20/10/2026): rich text a person writes into a form. It has no UI of its
// own: the author draws the toolbars, the link's popover and the writing area inside it, and the engine
// recognises each part and writes its wiring:
//
//	<form method="post" gap="4">
//	  <field>
//	    <label>Nội dung</label>
//	    <editor as="$body" gap="2">
//	      <box alt="Định dạng" flex="row" wrap="true" gap="1">
//	        <button click="toggle('bold')" active:background="brand/15"><icon value="bold" alt="Đậm" /></button>
//	        <button click="toggle('heading', 2)" active:background="brand/15"><icon value="h-2" alt="Tiêu đề mục" /></button>
//	        <button click="link()"><icon value="link" alt="Liên kết" /></button>
//	        <button click="undo()"><icon value="arrow-back-up" alt="Hoàn tác" /></button>
//	      </box>
//	      <popover place="below-left" gap="2" padding="2" background="paper">
//	        <input value="{{ address }}" alt="Địa chỉ liên kết" placeholder="https://… hay /trang" />
//	        <button click="link(address)">Gắn</button>
//	        <button click="unlink()">Bỏ liên kết</button>
//	        <button click="close()">Huỷ</button>
//	        <error>Chỉ nhận https://, http://, mailto:, tel: hay đường dẫn bắt đầu bằng /.</error>
//	      </popover>
//	      <content name="body" value="{{ post.body }}" required="true" placeholder="Viết bài ở đây…" rows="12" padding="4" border="line" />
//	    </editor>
//	    <hint>{{ $body.words }} từ</hint>
//	  </field>
//	  <button type="submit">Lưu bài</button>
//	</form>
//
//   - The host is a box (class editor), inside a <form method="post">: the post goes with the form.
//     data-kitwork-scope holds its first state, empty, words, characters (the value cleaned), unsaved
//     (false; true when the page answers a request that is no GET, a refused save shown again), open
//     (false) and address ('').
//   - The one <content> inside it is the writing area (everywhere else <content> is still the page's
//     content region). Its words are the field's: name (required), value (the saved post, the page's
//     data), placeholder, required, readonly, disabled, rows (its least height in lines), alt (its name
//     when no <label> of a <field> names it); the rest are presentation, the area's look. It becomes two
//     elements: the area, <div class="richtext editor-area" role="textbox" …> holding the value cleaned
//     by richtext.Clean (writing looks like reading), without contenteditable, which the component turns
//     on as it starts; and right after it the field the form posts, <textarea class="editor-field"
//     name="…">, holding the same HTML escaped: a real control (required holds, and without JavaScript
//     it is the one shown), its id the <field>'s, so <label for> names it.
//   - A toolbar is the nearest container of the buttons whose click is one bare call of toggle(…),
//     align(…), rule(), link(), unlink(), undo(), redo() or close() that does not hold the area:
//     role="toolbar", its alt its name, aria-controls the area, one stop for Tab across all of it (the
//     first button tabindex="0", every other -1; the arrows move along). An editor may have several,
//     each with alt. A format button carries data-kitwork-format (bold, italic, underline,
//     strikethrough, code, code-block, quote, heading-2 to heading-6, list, list-ordered),
//     aria-pressed="false" and, for bold, italic and underline, aria-keyshortcuts; a command button
//     data-kitwork-command (align-left, align-center, align-right, rule, link, unlink, undo, redo,
//     close), an align button aria-pressed="false" too. link() opens the popover: aria-haspopup="dialog",
//     aria-controls it, aria-expanded bound to open. undo, redo and link start aria-disabled="true"
//     (nothing to undo, no text chosen); every command button stays so in an editor written readonly or
//     disabled.
//   - A toolbar may hold <group>s (toolbar v2, 20/10/2026): each a group of its buttons, role="group",
//     named by its alt when written, class editor-group (the scope draws a line between two groups);
//     the stop for Tab still runs across every group, one toolbar. A <dropdown> in a toolbar (or a group)
//     holds more commands: one button outside its popover opens it, <button click="toggle()"> (the
//     dropdown's own toggle, one stop of the toolbar, aria-expanded bound to the dropdown's open and
//     aria-controls its popover), and the buttons in its popover call the editor through its alias,
//     <button click="$body.toggle('heading', 4)"> (a bare toggle() there is the dropdown's): they are
//     the editor's commands, wired as the toolbar's (editorMenuOf, editorMenuElement).
//   - The one <popover> inside it is the link's: role="dialog", named by its address input's alt, the
//     class editor-popover. Its one <input value="{{ address }}"> is the address (editor-address,
//     type="text" inputmode="url" autocomplete="off" autocapitalize="none" spellcheck="false": a url
//     type would block the whole form on /trang, a name would post it), its buttons call link(address),
//     unlink() and close(), and one <error> may say why an address was refused: hidden, with an id the
//     component points the input's aria-describedby at when it refuses. Its place (place="below-left",
//     the class place-below-left this file leaves to the author) is measured by the component from the
//     words it links, not from the host (21/10/2026, jit/js/parts/editor-place.js): nothing more on the
//     wire. Neither for the tooltips of the toolbars' buttons that show no words: the part makes their
//     one element and reads each button's name (its icon's aria-label) and aria-keyshortcuts.
//   - Its names are read bare inside the host, through its alias outside ($body.words); its commands are
//     its buttons' alone: called from anywhere else (an alias, another event, a button in a for) they
//     would take the visitor's selection away first, and are told so. It sends no event of its own yet:
//     input and change on <editor> or <content> are told so.
//   - Its format (21/10/2026) is how the post is seen and typed: 'rich' (the area, written as it reads),
//     'markdown' or 'html' (a text surface the component shows in the area's place). Whatever the format,
//     one value goes with the form: the field holds the cleaned HTML. format="markdown" on the host is
//     the one it starts in ('rich' when not written; a word written out, it leaves the host as the
//     countdown's duration does). A button whose click is one assignment of a format written out,
//     <button click="format = 'markdown'">, switches it: data-kitwork-command="format",
//     data-kitwork-format-value="markdown", aria-pressed bound to format == 'markdown' (active: draws the
//     one chosen; the component writes none of its own), and in a toolbar it is one of its stops; it
//     finds a toolbar as a command button does.
//     A <select value="{{ format }}" alt="…"> whose options are formats switches it too:
//     data-kitwork-command="format" and the kernel's two-way binding, value: format (selectBind), its
//     option of the format it starts in painted selected; the field around the editor is the area's,
//     so the select is never its control (holdsField). An editor written readonly or disabled keeps the
//     format it starts in (the component refuses a switch there): its switches are aria-disabled like
//     every button, its select disabled. dropped
//     holds the tags the last switch from html, a paste or markdown's raw HTML left out: read, never
//     written. Any other write of format is told where to put it (editorEvents, editorFormatField).
//
// What does not fit is told where it is written, each with what to write (editorWords, editorFormatWord,
// editorParts, editorButtonOf, editorFormat, editorAlign, editorFormatProblem, editorSelectOf,
// editorMenuOf, editorToolbars, editorGroupPlace, editorEvents, checkEditors).

// editorMethods are what an editor does, called by its buttons; editorNames what it holds, which
// markup reads.
var (
	editorMethods = []string{"toggle", "align", "rule", "link", "unlink", "undo", "redo", "close"}
	editorNames   = []string{"empty", "words", "characters", "unsaved", "open", "address", "format", "dropped"}
)

// editorFormats are how a post is seen and typed, the values of format: written as it reads (rich),
// Markdown, the HTML the field posts. editorFormatChoices is how they are named in a message.
var editorFormats = []string{"rich", "markdown", "html"}

const editorFormatChoices = "rich, markdown hay html"

// editorFormatProblem tells a format written out that is none of editorFormats, "" for one that is;
// written writes a format as the author wrote it there (format = 'code' in a click, format="code" on the
// host, <option value="code"> in a select), for the message and the fix in it. code is the editor's mark
// and block, md an abbreviation, source and raw the names others give html, visual, wysiwyg and text
// the names others give rich.
func editorFormatProblem(value string, written func(string) string) string {
	at := written(value)
	switch value {
	case "rich", "markdown", "html":
		return ""
	case "code":
		return at + ": code không phải một cách viết: mã trong bài là toggle('code') trong dòng, toggle('code', 'block') một khối; xem và sửa bài bằng HTML là " + written("html")
	case "source", "source-code", "raw", "html-source":
		return at + ": cách viết bằng mã HTML tên là html, viết " + written("html")
	case "md":
		return at + ": md là chữ viết tắt; .ui viết đủ chữ: " + written("markdown")
	case "visual", "wysiwyg", "text", "richtext", "rich-text", "formatted":
		return at + ": cách viết thấy như đọc tên là rich, viết " + written("rich")
	}
	return at + ": editor không có cách viết " + value + "; có " + editorFormatChoices + nearestWords(value, editorFormats)
}

// editorInstead is what other ways to let a person write rich text are told: contenteditable on a tag,
// a <wysiwyg>.
const editorInstead = `vùng soạn chữ là <editor> có một <content name="body" /> bên trong, đặt trong <form method="post">`

// editorFormatList is how the formats are named in a message.
const editorFormatList = "bold, italic, underline, strikethrough, code, quote, heading 2 tới 6, list"

// editorSides is how align is called, for a message.
const editorSides = "align('left'), align('center') hay align('right')"

// editorKey is where a node is written: what the compile's copies of it (a popover without its show, a
// host) share with it, and every instance of a component's .ui used twice.
type editorKey struct {
	file         string
	line, column int
}

func editorKeyOf(n *Node) editorKey { return editorKey{n.File, n.Line, n.Column} }

// editorScope is one editor host: what ctx.editor points to inside it, and its frame's editor.
type editorScope struct {
	node      *Node // the host as written
	component *Attr
	number    int
	alias     string
	aliases   *aliasFrame
	// inside are the elements written inside the host, nested components too
	inside map[editorKey]bool

	// the writing area and the words of its field
	area        *Node
	areaID      string
	name        string
	value       *expr // nil: it starts empty
	placeholder *Attr
	alt         *Attr
	required    bool
	readonly    bool
	disabled    bool
	rows        int // its least height in lines, 0 when not written

	toolbars map[editorKey]*editorToolbar
	groups   map[editorKey]bool // the <group>s its toolbars hold
	buttons  map[editorKey]*editorButton
	roving   map[editorKey]string // the tabindex of each button of a toolbar, a dropdown's trigger too

	// menus are the <dropdown>s of its toolbars that hold more of its commands, by the dropdown;
	// menuParts the trigger and the popover of each, menuButtons the commands in them (called through
	// the alias), each by where it is written: they compile inside the dropdown, where no editor is
	// around (editorMenuElement)
	menus       map[editorKey]*editorMenu
	menuParts   map[editorKey]*editorMenu
	menuButtons map[editorKey]*editorButton
	// strays are the buttons in the popover of a dropdown written in the host that call a command of an
	// editor through another alias than this one's (editorStrayCall), by where each is written: the
	// dropdown it stands in
	strays map[editorKey]*Node

	popover      *Node
	popoverID    string
	address      *Node
	addressAlt   *Attr
	addressError *Node
	errorID      string

	// format is the format it starts in (rich when not written), formatWritten its word on the host as
	// written (nil for none); selects are the <select value="{{ format }}"> written in the host, by where
	// each is written; field is the field around it, the writing area's (nil outside a field)
	format        string
	formatWritten *Attr
	selects       map[editorKey]*Node
	field         *fieldIDs
}

// holdsField reports whether f is the field around an editor, its writing area's: no other control
// inside the editor takes its id (a format select). False on no editor (outside every editor).
func (s *editorScope) holdsField(f *fieldIDs) bool { return s != nil && f != nil && f == s.field }

// selectBind is the kernel's binding of a select of the editor's format, "value: format": two-way, as
// .ui binds a field on what the browser keeps (KITWORK_UI "Một chiều và hai chiều"), the kernel writing
// format on each choice and the choice on each switch; written as a binding alone, since a <select> has
// no value attribute for the server to paint (its option of the format is painted selected). "" for any
// other element, and outside every editor.
func (s *editorScope) selectBind(n *Node) string {
	if s == nil || s.selects[editorKeyOf(n)] == nil {
		return ""
	}
	return "value: format"
}

// editorToolbar is a container of command buttons.
type editorToolbar struct {
	node    *Node
	buttons []*Node // the buttons the toolbar holds, in order, for its roving tabindex
}

// editorButton is a button whose click is one of the editor's commands.
type editorButton struct {
	node    *Node
	click   *Attr
	command string // toggle, align, rule, link, unlink, undo, redo, close; format for a switch of format
	format  string // the format a toggle names (bold, heading-4, code-block…); "" for every other command
	value   string // the format a switch writes (rich, markdown, html); "" for every command
	wire    string // data-kitwork-command: the command, an align's with its side (align-center); "" for a format
	popover bool   // it stands in the link's popover
	apply   bool   // link(address): it links the selection to the address
	// menu is the toolbar's dropdown whose popover holds it, the editor called through its alias; nil
	// for a button written in the host
	menu *editorMenu
	path []*Node // the elements around it inside the host (for a menu's, around its dropdown)
}

// pressed reports whether the button says it is on: a format, at the caret or over the selection, an
// align, the side of the caret's block, and a switch of format, the format chosen.
func (b *editorButton) pressed() bool {
	return b.format != "" || b.command == "align" || b.command == "format"
}

// editorMenu is a <dropdown> in a toolbar that holds more of the editor's commands: its trigger opens
// it, its popover holds the commands.
type editorMenu struct {
	node      *Node   // the <dropdown>
	path      []*Node // the elements around it inside the host, outermost first
	trigger   *Node   // its <button click="toggle()">, one stop of its toolbar
	popover   *Node
	popoverID string
	buttons   []*editorButton
}

// editorHost reads the editor's host before anything inside it compiles: where it stands, its own word
// (format, which leaves the host: editorFormatWord), the words it refuses, and its parts (editorParts).
// The host then goes on as the host of a component without a UI.
func (c *compiler) editorHost(n *Node, component *Attr, cx ctx) (*Node, *editorScope, error) {
	s := &editorScope{node: n, component: component, number: c.next("editor"), inside: map[editorKey]bool{},
		toolbars: map[editorKey]*editorToolbar{}, groups: map[editorKey]bool{}, buttons: map[editorKey]*editorButton{},
		roving: map[editorKey]string{}, menus: map[editorKey]*editorMenu{}, menuParts: map[editorKey]*editorMenu{},
		menuButtons: map[editorKey]*editorButton{}, strays: map[editorKey]*Node{},
		format: "rich", selects: map[editorKey]*Node{}, field: cx.field}
	if err := c.editorPlace(n, component, cx); err != nil {
		return nil, nil, err
	}
	host, err := editorFormatWord(n, s)
	if err != nil {
		return nil, nil, err
	}
	if err := editorWords(host); err != nil {
		return nil, nil, err
	}
	if len(n.SignificantChildren()) == 0 {
		return nil, nil, errAttr(n, component, `editor rỗng: vẽ vùng viết bên trong, <editor><content name="body" alt="Nội dung" /></editor>`)
	}
	if err := c.editorParts(n, s, cx); err != nil {
		return nil, nil, err
	}
	c.editors = append(c.editors, s)
	var rows []int
	for _, e := range c.editors {
		rows = append(rows, e.rows)
	}
	c.rules.Editor(rows) // the scope, with the height of every rows the page's editors write
	return host, s, nil
}

// editorFormatWord reads the editor's own word, format: the format the post is seen and typed in as the
// page opens, rich, markdown or html, written out (rich when not written). It leaves the host, as a
// countdown's duration does: the state starts there (bindings), and the host goes on without it. The
// parsed tree is shared by every instance, so the host is copied, never changed.
func editorFormatWord(n *Node, s *editorScope) (*Node, error) {
	host := *n
	host.Attrs = nil
	for i := range n.Attrs {
		a := &n.Attrs[i]
		chain, base := "", a.Name
		if j := strings.LastIndex(a.Name, ":"); j >= 0 && !isEvent(a.Name) {
			chain, base = a.Name[:j], a.Name[j+1:]
		}
		if base != "format" || isEvent(a.Name) {
			host.Attrs = append(host.Attrs, *a)
			continue
		}
		value := strings.TrimSpace(a.Value)
		switch {
		case chain != "":
			return nil, errAttr(n, a, `format không nhận tiền tố: cách viết lúc trang mở là một ở mọi độ rộng (format="markdown")`)
		case strings.Contains(a.Value, "{{"):
			return nil, errAttr(n, a, `format trên <editor> là cách viết lúc trang mở, chữ viết sẵn (format="markdown"), không lấy từ dữ liệu`)
		case value == "":
			return nil, errAttr(n, a, `format cần một cách viết: format="rich", format="markdown" hay format="html"`)
		}
		if problem := editorFormatProblem(value, func(v string) string { return `format="` + v + `"` }); problem != "" {
			return nil, errAttr(n, a, "%s", problem)
		}
		s.format, s.formatWritten = value, a
	}
	return &host, nil
}

// editorPlace refuses an editor where it cannot work: inside another editor, inside a line (it is a
// block), and outside a <form method="post"> (its value goes with the form; a GET would put the whole
// post in the page's address).
func (c *compiler) editorPlace(n *Node, component *Attr, cx ctx) error {
	repeat := cx.forAt
	if a := attrNamed(n, "for"); a.Name == "for" {
		repeat = &forSite{node: n, attr: a}
	}
	if repeat != nil {
		return errAttr(repeat.node, repeat.attr, "editor không lặp bằng for: vùng viết, thanh công cụ và khung liên kết của nó mang id riêng, mỗi trang sửa một bài (editor ở %s:%d:%d)", n.File, component.Line, component.Column)
	}
	for f := cx.paint; f != nil; f = f.parent {
		if f.node != nil && c.hostTags[f.node] == "editor" {
			return errAttr(n, component, "editor không lồng trong editor (editor ngoài ở %s:%d:%d)", f.node.File, f.node.Line, f.node.Column)
		}
	}
	var line *paint
	for f := cx.paint; f != nil; f = f.parent {
		if lineElements[f.html] {
			line = f
		}
	}
	if line != nil || cx.inline {
		message := "editor là một khối: không đặt trong <text>, <link>, <button>, <label>"
		if line != nil && line.node != nil {
			message += fmt.Sprintf(" (nó đang nằm trong <%s> ở %s:%d:%d)", c.paintTag(line), line.node.File, line.node.Line, line.node.Column)
		}
		return errAttr(n, component, "%s", message)
	}
	var form *paint
	for f := cx.paint; f != nil && form == nil; f = f.parent {
		if f.html == "form" && f.node != nil {
			form = f
		}
	}
	if form == nil {
		return errAttr(n, component, `editor phải nằm trong <form method="post">: bài viết đi theo form khi gửi, router.post của trang nhận nó`)
	}
	if method := strings.TrimSpace(attrNamed(form.node, "method").Value); method != "post" {
		return errAttr(n, component, `editor phải nằm trong <form method="post">: form GET đưa cả bài viết lên địa chỉ trang (<form> ở %s:%d:%d)`,
			form.node.File, form.node.Line, form.node.Column)
	}
	return nil
}

// editorStrangers are the words other editors take, written on <editor>, each with what to write
// here; %s is the word as written.
var editorStrangers = func() map[string]string {
	out := map[string]string{}
	add := func(message string, words ...string) {
		for _, word := range words {
			out[word] = message
		}
	}
	add(`%s không có trên <editor>: name, value, placeholder, required, readonly, disabled, rows viết trên <content> bên trong`,
		"name", "value", "placeholder", "required", "readonly", "disabled", "rows")
	add(`alt không có trên <editor>: tên của vùng viết là <label> của <field> bao ngoài, hay alt trên <content>`, "alt")
	add(`editor không có %s: editor không có giao diện dựng sẵn, vẽ nút bên trong (<button click="toggle('bold')">)`,
		"toolbar", "buttons", "formats", "plugins", "extensions", "menu")
	add(`editor không có %s: độ dài bài kiểm ở server (richtext(…).characters)`, "maxlength", "minlength", "limit", "max")
	add(`contenteditable không có trên <editor>: vùng viết là <content> bên trong, engine bật soạn khi trang chạy`, "contenteditable")
	add(`editor không có %s: bài lúc đầu là value của <content>, server vẽ sẵn`, "html", "content", "text", "initial")
	add(`editor không có %s: viết bằng Markdown lúc trang mở là format="markdown"; nút đổi cách viết là <button click="format = 'markdown'">`, "markdown")
	add(`editor không có %s: cách viết là format, format="rich", format="markdown" hay format="html"`, "mode", "view", "syntax", "source")
	add(`editor không có %s: lưu là việc của form, <button type="submit">, router.post nhận`, "save", "submit", "autosave", "draft", "persist")
	add(`editor không có %s: chưa lưu là unsaved, trạng thái của editor`, "dirty", "changed", "modified")
	add(`editor không có linking: ô liên kết đang mở là open, trạng thái của editor`, "linking")
	add(`editor không có %s: chữ trong ô địa chỉ là address, trạng thái của editor`, "href", "url")
	add(`editor không có sự kiện %s ở bản này: bài đi theo form khi gửi`, "update", "edit", "text-change")
	add(`editor không có %s: định dạng là nút bên trong, <button click="toggle('bold')">`,
		"command", "run", "exec", "bold", "italic", "underline", "strikethrough", "code", "heading", "quote", "list")
	return out
}()

// editorCallExample is how each method is called by its button, for a message.
var editorCallExample = map[string]string{
	"toggle": "toggle('bold')", "align": "align('center')", "rule": "rule()", "link": "link()", "unlink": "unlink()",
	"undo": "undo()", "redo": "redo()", "close": "close()",
}

// editorWords refuses, on the host, the words an editor does not take: the field's words (they are
// <content>'s), the state and the methods written as attributes, the words of other editors, a role,
// and input or change, which it does not send yet.
func editorWords(n *Node) error {
	alias := strings.TrimSpace(attrNamed(n, "as").Value)
	for i := range n.Attrs {
		a := &n.Attrs[i]
		if i == 0 && a.Name == "component" {
			continue // the tag as written (asHost)
		}
		if isEvent(a.Name) {
			head, _, _ := strings.Cut(a.Name, ":")
			for _, event := range strings.Split(head, ",") {
				switch event = strings.TrimSpace(event); event {
				case "input", "change":
					return errAttr(n, a, "%s trên <editor> chưa có ở bản này: bài đi theo form khi gửi, số chữ là {{ words }}", event)
				case "toggle":
					return errAttr(n, a, `toggle trên <editor> không phải sự kiện nào của nó: toggle là lệnh của editor, gọi bằng nút bên trong (<button click="toggle('bold')">)`)
				}
			}
			continue
		}
		base := a.Name[strings.LastIndex(a.Name, ":")+1:]
		switch {
		case base == "align":
			// a word of the box the host builds, and the editor's command too: one word, one meaning on a tag
			return errAttr(n, a, `%s không viết trên <editor>: align() là lệnh căn lề của editor (<button click="align('center')">); căn các phần bên trong bằng align trên khung bọc chúng (<box align="left">)`, a.Name)
		case editorStrangers[base] != "":
			says := editorStrangers[base]
			if strings.Contains(says, "%s") {
				says = fmt.Sprintf(says, base)
			}
			return errAttr(n, a, "%s", says)
		case containsName(editorNames, base):
			read := "{{ " + base + " }} bên trong <editor>"
			if alias != "" {
				read = "{{ " + alias + "." + base + " }}"
			}
			return errAttr(n, a, "%s là trạng thái của editor, không phải đầu vào: đọc %s", base, read)
		case containsName(editorMethods, base):
			return errAttr(n, a, `%s là hàm của editor, không phải một chữ trên thẻ: gọi nó trong click của một nút bên trong (<button click="%s">)`, base, editorCallExample[base])
		case base == "role" || strings.HasPrefix(base, "aria-"):
			return errAttr(n, a, `vai trò của editor là việc của engine (role="textbox" trên vùng viết, role="toolbar" trên thanh công cụ): bỏ %s`, base)
		}
	}
	return nil
}

// editorFound is an element the walk of a host found, with what stands around it there.
type editorFound struct {
	node    *Node
	path    []*Node // the elements around it inside the host, outermost first (a fragment builds none)
	repeat  *Node   // the nearest element around it inside the host that writes for or if
	nested  *Node   // the component call around it inside the host, nil for none
	popover *Node   // the popover around it, nil for none
}

// editorParts finds the parts of an editor in what is written inside its host, never inside a component
// nested there (whose names are its own) but for a dropdown of a toolbar's commands (editorMenuOf): the
// one <content>, its words, the one <popover> and its address input, the command buttons and the
// toolbars that hold them; then checks the area has a name.
func (c *compiler) editorParts(n *Node, s *editorScope, cx ctx) error {
	var contents, popovers, buttons, inputs, errorsFound, dropdowns, selects []editorFound
	var walk func(nodes []*Node, path []*Node, repeat, nested, popover *Node) error
	walk = func(nodes []*Node, path []*Node, repeat, nested, popover *Node) error {
		for _, ch := range nodes {
			if ch.IsText() {
				continue
			}
			s.inside[editorKeyOf(ch)] = true
			found := editorFound{node: ch, path: path, repeat: repeat, nested: nested, popover: popover}
			switch {
			case ch.Tag == "content":
				contents = append(contents, found)
				continue // what is written inside it is told by its words
			case nested != nil:
			case ch.Tag == "editor":
				return errAt(ch, "editor không lồng trong editor (editor ngoài ở %s:%d:%d)", n.File, n.Line, n.Column)
			case ch.Tag == "form":
				return errAt(ch, `form không đặt trong <editor>: editor là một ô của form bao ngoài nó (<form method="post">…<editor>…</editor>…</form>)`)
			case ch.Tag == "dropdown" && popover == nil:
				dropdowns = append(dropdowns, found) // more commands of a toolbar, when it holds some (editorMenuOf)
			case ch.Tag == "popover":
				popovers = append(popovers, found)
			case ch.Tag == "button":
				buttons = append(buttons, found)
			case ch.Tag == "select":
				selects = append(selects, found) // a switch of format, when it is two-way on it (editorSelectOf)
			case ch.Tag == "input" && popover != nil:
				inputs = append(inputs, found)
			case ch.Tag == "error" && popover != nil:
				errorsFound = append(errorsFound, found)
			}
			around := repeat
			if a := attrNamed(ch, "for"); a.Name == "for" {
				around = ch
			} else if a := attrNamed(ch, "if"); a.Name == "if" {
				around = ch
			}
			inner, within := nested, popover
			if inner == nil && c.callsComponent(ch) {
				inner = ch
			}
			if inner == nil && ch.Tag == "popover" && within == nil {
				within = ch
			}
			next := path
			if inner == nil && ch.Tag != "fragment" {
				next = append(append([]*Node(nil), path...), ch)
			}
			if err := walk(ch.Children, next, around, inner, within); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(n.Children, nil, nil, nil, nil); err != nil {
		return err
	}

	// the writing area: one, written in the host itself, always there
	if len(contents) == 0 {
		return errAttr(n, s.component, `editor cần một vùng viết bên trong, engine viết role="textbox" lên nó: <content name="body" value="{{ post.body }}" />`)
	}
	area := contents[0]
	for _, f := range contents {
		if f.nested != nil {
			return errAt(f.node, "vùng viết của editor viết thẳng trong <editor>, không trong <%s> lồng bên trong (ở %s:%d:%d): tên trần bên trong đó là của nó",
				c.tagOf(f.nested), f.nested.File, f.nested.Line, f.nested.Column)
		}
	}
	if len(contents) > 1 {
		second := contents[1].node
		return errAt(second, "editor có một vùng viết: <content> ở %s:%d:%d đã là của nó; vùng viết thứ hai là một <editor> khác", area.node.File, area.node.Line, area.node.Column)
	}
	if area.popover != nil {
		return errAt(area.node, "vùng viết của editor không đặt trong <popover>: popover là khung của liên kết, đóng khi không gắn liên kết")
	}
	if r := area.repeat; r != nil {
		if a := attrNamed(r, "for"); a.Name == "for" {
			return errAttr(r, a, "vùng viết của editor không lặp bằng for: for này lặp <content> ở %s:%d:%d; đặt <content> ngoài for", area.node.File, area.node.Line, area.node.Column)
		}
		a := attrNamed(r, "if")
		return errAttr(r, a, "vùng viết của editor luôn có mặt: if này có thể bỏ <content> ở %s:%d:%d; đặt <content> ngoài if", area.node.File, area.node.Line, area.node.Column)
	}
	s.area = area.node
	if err := c.editorContentWords(area.node, s, cx); err != nil {
		return err
	}
	s.areaID = strings.TrimSpace(attrNamed(area.node, "id").Value)
	if s.areaID == "" {
		s.areaID = fmt.Sprintf("kitwork-editor-%d-area", s.number)
	}

	// the link's popover: one
	if len(popovers) > 1 {
		first := popovers[0].node
		return errAt(popovers[1].node, "editor có một popover, khung của liên kết: <popover> ở %s:%d:%d đã là của nó", first.File, first.Line, first.Column)
	}
	if len(popovers) == 1 {
		s.popover = popovers[0].node
		s.popoverID = strings.TrimSpace(attrNamed(s.popover, "id").Value)
		if s.popoverID == "" {
			s.popoverID = fmt.Sprintf("kitwork-editor-%d-popover", s.number)
		}
	}

	// the command buttons and the switches of format, each read where it is written
	links, commands := false, 0
	var toolbarButtons []*editorButton
	for _, f := range buttons {
		b, err := c.editorButtonOf(f, s)
		if err != nil {
			return err
		}
		if b == nil {
			continue
		}
		if b.command != "format" {
			commands++
		}
		s.buttons[editorKeyOf(f.node)] = b
		if b.command == "link" && !b.apply {
			links = true
			if s.popover == nil {
				return errAttr(f.node, b.click, `link() mở ô địa chỉ mà editor chưa có: vẽ <popover> chứa <input value="{{ address }}" alt="Địa chỉ liên kết" />`)
			}
		}
		if !b.popover {
			toolbarButtons = append(toolbarButtons, b)
		}
	}
	// the dropdowns of more commands, which their toolbars hold as they hold a button
	alias := strings.TrimSpace(attrNamed(n, "as").Value)
	for _, f := range dropdowns {
		menu, err := c.editorMenuOf(f, s, alias)
		if err != nil {
			return err
		}
		if menu == nil {
			continue // no command of the editor: a toolbar refuses it as it refuses any component
		}
		s.menus[editorKeyOf(menu.node)] = menu
		s.menuParts[editorKeyOf(menu.trigger)], s.menuParts[editorKeyOf(menu.popover)] = menu, menu
		for _, b := range menu.buttons {
			s.menuButtons[editorKeyOf(b.node)] = b
			toolbarButtons = append(toolbarButtons, b)
			commands++
		}
	}
	if err := c.editorToolbars(s, toolbarButtons, area.path); err != nil {
		return err
	}
	// the selects of format
	for _, f := range selects {
		if err := c.editorSelectOf(f, s); err != nil {
			return err
		}
	}
	if s.popover != nil {
		if err := c.editorPopover(s, inputs, errorsFound); err != nil {
			return err
		}
		if !links {
			c.warn(s.popover.File, s.popover.Line, s.popover.Column, `popover của editor chỉ mở bằng link(): chưa có nút <button click="link()">, nên khung liên kết không bao giờ hiện`)
		}
	}
	if commands == 0 {
		c.warn(n.File, s.component.Line, s.component.Column, `editor không có nút nào: điện thoại không có phím tắt; vẽ ít nhất <button click="toggle('bold')"> và <button click="link()"> trong một <box alt="Định dạng">`)
	}

	// the area's name: the <label> of the field around, else its alt
	labelled := cx.field != nil && cx.field.label != ""
	switch {
	case !labelled && (s.alt == nil || strings.TrimSpace(s.alt.Value) == ""):
		return errAt(s.area, "vùng viết chưa có tên đọc được: đặt editor trong <field> có <label>, hay viết alt trên <content>")
	case labelled && s.alt != nil:
		c.warn(s.area.File, s.alt.Line, s.alt.Column, "alt trên <content> không có tác dụng: <label> của <field> bao ngoài đã là tên của vùng viết (trang bỏ alt này)")
		s.alt = nil
	}
	return nil
}

// editorContentRefused are the words other text areas take, written on <content> inside an editor,
// each with what to write here; %s is the word as written.
var editorContentRefused = map[string]string{
	"maxlength":       "%s không có trên vùng viết: độ dài bài kiểm ở server (richtext(…).characters)",
	"minlength":       "%s không có trên vùng viết: độ dài bài kiểm ở server (richtext(…).characters)",
	"contenteditable": "contenteditable không có trên vùng viết: engine bật soạn khi trang chạy",
	"type":            "type không có trên vùng viết: bài là richtext, server làm sạch",
	"cols":            "cols không có trên vùng viết: độ rộng là width",
	"autocomplete":    "autocomplete không có trên vùng viết",
	"autofocus":       "autofocus không có trên vùng viết: chỗ con trỏ đứng khi trang mở là của người dùng",
	"spellcheck":      `spellcheck của vùng viết là việc của engine (spellcheck="true"): bỏ spellcheck`,
	"translate":       `translate của vùng viết là việc của engine (translate="no": bản dịch của trang không được lưu thành bài): bỏ translate`,
	"role":            `vai trò của vùng viết là việc của engine (role="textbox"): bỏ role`,
}

// editorContentWords reads the words of the writing area, written on <content>: the field's words
// (name, value, placeholder, required, readonly, disabled, rows, alt), and presentation; anything else
// is told, and so is anything written inside it.
func (c *compiler) editorContentWords(n *Node, s *editorScope, cx ctx) error {
	if len(n.SignificantChildren()) > 0 {
		return errAt(n, `vùng viết của editor không có nội dung bên trong: bài lúc đầu là value="{{ post.body }}"`)
	}
	var name *Attr
	for i := range n.Attrs {
		a := &n.Attrs[i]
		if isEvent(a.Name) && !scrollArea(a.Name, a.Value) {
			head, _, _ := strings.Cut(a.Name, ":")
			for _, event := range strings.Split(head, ",") {
				if event = strings.TrimSpace(event); event == "input" || event == "change" {
					return errAttr(n, a, "%s viết trên <content> của editor chưa có ở bản này: bài đi theo form khi gửi, số chữ là {{ words }}", event)
				}
			}
			return errAttr(n, a, `<content> của editor không nhận sự kiện %s ở bản này: lệnh của editor là nút trong thanh công cụ (<button click="toggle('bold')">)`, a.Name)
		}
		chain, base := "", a.Name
		if j := strings.LastIndex(a.Name, ":"); j >= 0 {
			chain, base = a.Name[:j], a.Name[j+1:]
		}
		value := strings.TrimSpace(a.Value)
		switch base {
		case "for":
			return errAttr(n, a, "vùng viết của editor không lặp bằng for: mỗi editor một vùng viết")
		case "if", "show":
			return errAttr(n, a, "vùng viết của editor luôn có mặt: bỏ %s", base)
		case "flex", "grid", "columns", "gap", "align", "space", "wrap":
			return errAttr(n, a, "vùng viết là một khối chữ, engine đặt cách các đoạn nằm: bỏ %s", a.Name)
		case "id":
			continue // an anchor, checked as the area compiles
		}
		if presentationName(base) {
			continue // the area's look, read as it compiles
		}
		if chain != "" {
			return errAttr(n, a, "%s không nhận tiền tố (chỉ thuộc tính trình bày nhận tiền tố)", base)
		}
		switch base {
		case "name":
			name = a
			switch {
			case strings.Contains(a.Value, "{{"):
				return errAttr(n, a, `name của vùng viết là chữ cố định, tên ô form gửi lên (name="body"), không lấy từ dữ liệu`)
			case value == "":
				return errAttr(n, a, `<content> của editor cần name: không có name thì form không gửi bài lên server (name="body")`)
			}
			s.name = value
		case "value":
			e, err := c.editorValue(n, a, cx)
			if err != nil {
				return err
			}
			s.value = e
		case "placeholder":
			s.placeholder = a
		case "alt":
			s.alt = a
		case "required", "readonly", "disabled":
			on, err := editorBoolean(n, a)
			if err != nil {
				return err
			}
			switch base {
			case "required":
				s.required = on
			case "readonly":
				s.readonly = on
			default:
				s.disabled = on
			}
		case "rows":
			rows, err := strconv.Atoi(value)
			if err != nil || rows < 1 || rows > 100 || !digits(value) {
				return errAttr(n, a, `rows là số dòng thấp nhất của vùng viết, một số nguyên từ 1 tới 100 (rows="12"), không phải %q`, a.Value)
			}
			s.rows = rows
		default:
			if says := editorContentRefused[strings.ToLower(base)]; says != "" {
				if strings.Contains(says, "%s") {
					says = fmt.Sprintf(says, base)
				}
				return errAttr(n, a, "%s", says)
			}
			if message := c.webWord(n, a); message != "" {
				return errAttr(n, a, "%s", message)
			}
			return errAttr(n, a, "thuộc tính %s không có trên vùng viết <content>; vùng viết nhận name, value, placeholder, required, readonly, disabled, rows, alt và chữ trình bày", a.Name)
		}
	}
	if name == nil {
		return errAt(n, `<content> của editor cần name: không có name thì form không gửi bài lên server (name="body")`)
	}
	return nil
}

// editorBoolean reads required, readonly or disabled on the writing area: "true" or "false", fixed when
// the page compiles in this version.
func editorBoolean(n *Node, a *Attr) (bool, error) {
	switch strings.TrimSpace(a.Value) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	if strings.Contains(a.Value, "{{") {
		return false, errAttr(n, a, `%s của vùng viết là "true" hay "false" viết sẵn ở bản này, không lấy từ dữ liệu`, a.Name)
	}
	return false, errAttr(n, a, `%s chỉ nhận "true" hoặc "false"`, a.Name)
}

// editorValue reads the value of the writing area: one {{ }} that reads the page's data, the saved post,
// which the server cleans and paints. Words written in the markup are no post, and what lives in the
// browser (an alias, the names of a component the kernel runs, the editor's own) is not the server's.
func (c *compiler) editorValue(n *Node, a *Attr, cx ctx) (*expr, error) {
	in, err := parseInterpolation(a.Value, n, a)
	if err != nil {
		return nil, err
	}
	const written = `value của vùng viết là bài đã lưu, một {{ }} đọc dữ liệu của trang: value="{{ post.body }}"`
	e := in.single()
	if e == nil {
		return nil, errAttr(n, a, "%s", written)
	}
	if _, every := knownLiterals(e.ir); every {
		return nil, errAttr(n, a, "%s", written)
	}
	for _, name := range usedNames(e.ir) {
		live := cx.scope.declaring(name)
		if strings.HasPrefix(name, "$") || live != nil && live.client || containsName(editorNames, name) || containsName(editorMethods, name) {
			return nil, errAttr(n, a, "value của vùng viết dựng ở server: đọc dữ liệu của trang, không đọc %s (trạng thái ở trình duyệt)", name)
		}
	}
	return e, nil
}

// editorCommand reads an expression that is one bare call of an editor's method into the method and
// its arguments.
func editorCommand(ir any) (string, []any, bool) {
	node, ok := ir.([]any)
	if !ok || len(node) != 3 || node[0] != "call" {
		return "", nil, false
	}
	callee, ok := node[1].([]any)
	if !ok || len(callee) != 2 || callee[0] != "$" {
		return "", nil, false
	}
	name, _ := callee[1].(string)
	if !containsName(editorMethods, name) {
		return "", nil, false
	}
	args, _ := node[2].([]any)
	return name, args, true
}

// editorClick is a button's click as written, the event alone (click, click:prevent): nil for none.
func editorClick(n *Node) *Attr {
	for i := range n.Attrs {
		a := &n.Attrs[i]
		if head, _, _ := strings.Cut(a.Name, ":"); head == "click" {
			return a
		}
	}
	return nil
}

// editorButtonOf reads a button written in the host: one of the editor's when its click is one bare call
// of a method of it, with the arguments that method takes, nil for any other button.
func (c *compiler) editorButtonOf(f editorFound, s *editorScope) (*editorButton, error) {
	n := f.node
	click := editorClick(n)
	if click == nil {
		return nil, nil
	}
	e, err := compileExpr(click.Value, n, click)
	if err != nil {
		return nil, nil // wireEvents tells it
	}
	if value, ok := editorFormatWrite(e.ir); ok {
		return c.editorSwitchOf(f, click, value)
	}
	method, args, ok := editorCommand(e.ir)
	if !ok {
		return nil, nil
	}
	b := &editorButton{node: n, click: click, command: method, popover: f.popover != nil, path: f.path}
	if err := editorNotRepeated(n, click, f.repeat); err != nil {
		return nil, err
	}
	if err := editorReadCommand(n, click, b, args); err != nil {
		return nil, err
	}
	switch {
	case b.popover && method != "link" && method != "unlink" && method != "close":
		return nil, errAttr(n, click, "%s là nút của thanh công cụ, không đặt trong <popover> của liên kết", editorCallSource(method, args))
	case b.popover && method == "link" && !b.apply:
		return nil, errAttr(n, click, "link() mở ô địa chỉ, mà nút này đã ở trong ô: nút gắn liên kết là link(address)")
	case !b.popover && b.apply:
		return nil, errAttr(n, click, `link(address) gắn chữ trong ô địa chỉ: đặt nút này trong <popover> của editor, cạnh <input value="{{ address }}">`)
	}
	if method == "link" && !b.apply {
		if a := attrNamed(n, "expanded"); a.Name == "expanded" {
			return nil, errAttr(n, a, "expanded của nút liên kết là việc của engine (aria-expanded theo open): bỏ expanded")
		}
	}
	c.editorPressedWarning(n, b)
	return b, nil
}

// editorNotRepeated refuses a command button a for repeats, written on it or around it in the host.
func editorNotRepeated(n *Node, click *Attr, repeat *Node) error {
	for _, r := range []*Node{n, repeat} {
		if r == nil {
			continue
		}
		if a := attrNamed(r, "for"); a.Name == "for" {
			return errAttr(n, click, "nút của editor không lặp bằng for (for ở %s:%d:%d): mỗi nút viết một lần, định dạng viết sẵn", r.File, a.Line, a.Column)
		}
	}
	return nil
}

// editorReadCommand reads the arguments of a command as its button calls it into what the button carries
// on the wire: a toggle's format, an align's side, link(address); every other command takes nothing.
func editorReadCommand(n *Node, click *Attr, b *editorButton, args []any) error {
	switch b.command {
	case "toggle":
		format, err := editorFormat(n, click, args)
		if err != nil {
			return err
		}
		b.format = format
		return nil
	case "align":
		side, err := editorAlign(n, click, args)
		if err != nil {
			return err
		}
		b.wire = "align-" + side
		return nil
	case "link":
		switch {
		case len(args) == 0:
		case len(args) == 1 && editorBareName(args[0], "address"):
			b.apply = true
		default:
			return errAttr(n, click, "link nhận address, chữ trong ô địa chỉ: link(address); mở ô địa chỉ là link()")
		}
	default:
		if len(args) > 0 {
			return errAttr(n, click, "%s() không nhận gì: viết %s()", b.command, b.command)
		}
	}
	b.wire = b.command
	return nil
}

// editorPressedWarning tells pressed: on a button that says it is on (a format, an align): the moment it
// is pressed, not the format being on.
func (c *compiler) editorPressedWarning(n *Node, b *editorButton) {
	if !b.pressed() {
		return
	}
	on := "nút đang bật định dạng"
	if b.command == "format" {
		on = "nút của cách viết đang chọn"
	}
	for i := range n.Attrs {
		a := &n.Attrs[i]
		if chain, _, ok := strings.Cut(a.Name, ":"); ok && !isEvent(a.Name) && containsName(strings.Split(strings.ReplaceAll(chain, "+", ","), ","), "pressed") {
			c.warn(n.File, a.Line, a.Column, "pressed: là lúc đang nhấn; "+on+" tô bằng active: ("+strings.Replace(a.Name, "pressed", "active", 1)+")")
		}
	}
}

// editorFormatWrite reads an expression that is one assignment of the editor's format, format = …, into
// what it assigns.
func editorFormatWrite(ir any) (any, bool) {
	node, ok := ir.([]any)
	if !ok || len(node) != 3 || node[0] != "=" || node[1] != "format" {
		return nil, false
	}
	return node[2], true
}

// editorFormatSwitch is how a switch of format is written, for a message.
const editorFormatSwitch = `<button click="format = 'markdown'">`

// editorSwitchOf reads a button written in the host whose click is one assignment of format: a switch of
// format when what it assigns is a format written out (format = 'markdown'), which the engine reads to
// bind aria-pressed; written once (no for), never in the link's popover. In a toolbar it is one of its
// stops, and it finds its toolbar as a command button does (editorToolbars).
func (c *compiler) editorSwitchOf(f editorFound, click *Attr, value any) (*editorButton, error) {
	n := f.node
	written := "format = " + editorArgSource(value)
	if err := editorNotRepeated(n, click, f.repeat); err != nil {
		return nil, err
	}
	literal, ok := stringLiteral(value)
	if !ok {
		return nil, errAttr(n, click, "%s: cách viết là chữ viết sẵn, format = 'rich', format = 'markdown' hay format = 'html': engine đọc nó để viết aria-pressed", written)
	}
	if problem := editorFormatProblem(literal, func(v string) string { return "format = " + quoteString(v) }); problem != "" {
		return nil, errAttr(n, click, "%s", problem)
	}
	if f.popover != nil {
		return nil, errAttr(n, click, "%s đổi cách viết của editor: đặt nút này trong thanh công cụ, không trong <popover> của liên kết", written)
	}
	b := &editorButton{node: n, click: click, command: "format", value: literal, path: f.path}
	c.editorPressedWarning(n, b)
	return b, nil
}

// editorArgSource writes an argument as markup writes it: a string, a number, a bare name; "…" for
// anything else.
func editorArgSource(arg any) string {
	node, _ := arg.([]any)
	switch {
	case len(node) == 2 && node[0] == "#":
		switch v := node[1].(type) {
		case string:
			return quoteString(v)
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		}
	case len(node) == 2 && node[0] == "$":
		name, _ := node[1].(string)
		return name
	}
	return "…"
}

// editorSelectOf reads a <select> written in the host: the editor's switch of format when its value is
// {{ format }} (any other select is none of the editor's). Each option's value is a format written out,
// the format the editor starts in among them; it is named by its alt (the label of the field around the
// editor is the writing area's, so the select is never that field's control), unless a <field> of its
// own inside the editor names it; it has no name (the format is never posted: the field posts the
// post's HTML); written once (no for), never in the link's popover.
func (c *compiler) editorSelectOf(f editorFound, s *editorScope) error {
	n := f.node
	a := attrNamed(n, "value")
	if a.Name != "value" {
		return nil
	}
	read, err := parseInterpolation(a.Value, n, a)
	if err != nil {
		return nil // its own compile tells it
	}
	if e := read.single(); e == nil || !editorBareName(e.ir, "format") {
		return nil
	}
	if f.popover != nil {
		return errAttr(n, a, `<select value="{{ format }}"> đổi cách viết của editor: đặt nó cạnh thanh công cụ hay trong thanh, không trong <popover> của liên kết`)
	}
	for _, r := range []*Node{n, f.repeat} {
		if r == nil {
			continue
		}
		if repeat := attrNamed(r, "for"); repeat.Name == "for" {
			return errAttr(n, a, `<select value="{{ format }}"> không lặp bằng for (for ở %s:%d:%d): mỗi editor một cách viết`, r.File, repeat.Line, repeat.Column)
		}
	}
	if name := attrNamed(n, "name"); name.Name == "name" {
		return errAttr(n, name, `<select value="{{ format }}"> không có name: cách viết không gửi lên server, form gửi bài bằng HTML`)
	}
	labelled := false
	for _, p := range f.path {
		labelled = labelled || p.Tag == "field"
	}
	if alt := attrNamed(n, "alt"); !labelled && (alt.Name != "alt" || strings.TrimSpace(alt.Value) == "") {
		return errAt(n, `<select value="{{ format }}"> cần alt: tên của nó (alt="Viết bằng"); <label> của <field> bao ngoài editor là tên của vùng viết`)
	}
	values := map[string]bool{}
	var options func(nodes []*Node) error
	options = func(nodes []*Node) error {
		for _, ch := range nodes {
			if ch.IsText() {
				continue
			}
			if ch.Tag == "option" {
				v := attrNamed(ch, "value")
				if v.Name != "value" {
					continue // option needs a value: its own compile tells it
				}
				value := strings.TrimSpace(v.Value)
				if strings.Contains(v.Value, "{{") || value == "" {
					return errAttr(ch, v, `option của <select value="{{ format }}"> là một cách viết viết sẵn: <option value="markdown">Markdown</option>`)
				}
				if problem := editorFormatProblem(value, func(v string) string { return `<option value="` + v + `">` }); problem != "" {
					return errAttr(ch, v, "%s", problem)
				}
				values[value] = true
				continue
			}
			if err := options(ch.Children); err != nil {
				return err
			}
		}
		return nil
	}
	if err := options(n.Children); err != nil {
		return err
	}
	if !values[s.format] {
		start := ""
		if s.formatWritten != nil {
			start = fmt.Sprintf(` (format="%s" ở %s:%d:%d)`, s.format, s.node.File, s.formatWritten.Line, s.formatWritten.Column)
		}
		return errAt(n, `<select value="{{ format }}"> thiếu <option value="%s">: editor bắt đầu ở %s%s, select thiếu nó thì hiện sai cách viết đang dùng`, s.format, s.format, start)
	}
	s.selects[editorKeyOf(n)] = n
	return nil
}

// editorAliasCall reads an expression that is one call of an editor's method through its alias
// ($body.toggle('heading', 4)) into the method and its arguments.
func editorAliasCall(ir any, alias string) (string, []any, bool) {
	node, ok := ir.([]any)
	if alias == "" || !ok || len(node) != 4 || node[0] != "()" {
		return "", nil, false
	}
	receiver, _ := node[1].([]any)
	if len(receiver) != 2 || receiver[0] != "$" || receiver[1] != alias {
		return "", nil, false
	}
	method, _ := node[2].(string)
	args, _ := node[3].([]any)
	return method, args, containsName(editorMethods, method)
}

// editorStrayCall reads a button whose click is one call of a command of an editor through an alias
// other than alias (the editor's whose host holds the button) and own (the dropdown's it stands in):
// the alias it calls through and the call after it, toggle('heading', 5); ok false for any other.
func editorStrayCall(n *Node, alias, own string) (string, string, bool) {
	click := editorClick(n)
	if click == nil {
		return "", "", false
	}
	e, err := compileExpr(click.Value, n, click)
	if err != nil {
		return "", "", false
	}
	node, ok := e.ir.([]any)
	if !ok || len(node) != 4 || node[0] != "()" {
		return "", "", false
	}
	receiver, _ := node[1].([]any)
	if len(receiver) != 2 || receiver[0] != "$" {
		return "", "", false
	}
	name, _ := receiver[1].(string)
	method, _ := node[2].(string)
	args, _ := node[3].([]any)
	if name == "" || name == alias || name == own || !containsName(editorMethods, method) {
		return "", "", false
	}
	return name, editorCallSource(method, args), true
}

// editorFirstStray is the first button written in dropdown d that calls an editor through another alias
// (s.strays), nil for none.
func editorFirstStray(s *editorScope, d *Node) *Node {
	var first *Node
	var walk func(nodes []*Node)
	walk = func(nodes []*Node) {
		for _, ch := range nodes {
			if first != nil {
				return
			}
			if !ch.IsText() && s.strays[editorKeyOf(ch)] == d {
				first = ch
				return
			}
			walk(ch.Children)
		}
	}
	walk(d.Children)
	return first
}

// editorStrayMessage tells a button in a dropdown of the editor written as alias ("" without as) that
// calls a command through another alias (through, then call): a dropdown's commands go to the editor
// around it, through that editor's alias, which an editor without as does not have yet.
func editorStrayMessage(alias, through, call string) string {
	if alias == "" {
		return fmt.Sprintf(`%s.%s trong <dropdown> của <editor> chưa có as: nút trong dropdown gọi editor bao quanh nó qua alias của nó; đặt as trên <editor> (<editor as="%s">) rồi gọi %s.%s`,
			through, call, through, through, call)
	}
	return fmt.Sprintf(`%s.%s trong <dropdown> của editor %s: nút trong dropdown gọi editor bao quanh nó, qua alias của nó (%s.%s)`, through, call, alias, alias, call)
}

// editorMenuOf reads a <dropdown> written in the host (not in a component nested there, nor in the link's
// popover): a menu of a toolbar when its popover holds a command of the editor, called through the
// editor's alias, $body.toggle('heading', 4) (a bare toggle() there is the dropdown's own); nil for a
// dropdown that holds none, which a toolbar refuses as it refuses any component (editorToolbars). One
// button outside the popover opens it, <button click="toggle()">, the toolbar's stop for Tab; one popover;
// no component inside. A bare call of a command of the editor anywhere in a dropdown is told the alias.
func (c *compiler) editorMenuOf(f editorFound, s *editorScope, alias string) (*editorMenu, error) {
	d := f.node
	menu := &editorMenu{node: d, path: f.path}
	var triggers, others, popovers, nested []*Node
	var walk func(nodes []*Node, inside *Node, repeat *Node) error
	walk = func(nodes []*Node, inside *Node, repeat *Node) error {
		for _, ch := range nodes {
			if ch.IsText() {
				continue
			}
			if c.callsComponent(ch) {
				nested = append(nested, ch) // its names are its own
				continue
			}
			if ch.Tag == "popover" && inside == nil {
				popovers = append(popovers, ch)
			}
			around := repeat
			if attrNamed(ch, "for").Name == "for" || attrNamed(ch, "if").Name == "if" {
				around = ch
			}
			if ch.Tag == "button" {
				b, trigger, err := c.editorMenuButton(ch, alias, menu, inside, around)
				switch {
				case err != nil:
					return err
				case b != nil:
					menu.buttons = append(menu.buttons, b)
				case trigger:
					triggers = append(triggers, ch)
				case inside == nil:
					others = append(others, ch)
				default:
					if _, _, ok := editorStrayCall(ch, alias, strings.TrimSpace(attrNamed(d, "as").Value)); ok {
						s.strays[editorKeyOf(ch)] = d
					}
				}
			}
			within := inside
			if within == nil && ch.Tag == "popover" {
				within = ch
			}
			if err := walk(ch.Children, within, around); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(d.Children, nil, nil); err != nil {
		return nil, err
	}
	if len(menu.buttons) == 0 {
		return nil, nil
	}
	if len(nested) > 0 {
		return nil, errAt(nested[0], "<%s> không đặt trong <dropdown> của thanh công cụ của editor (dropdown ở %s:%d:%d): dropdown của thanh giữ nút mở nó, và trong <popover> các nút lệnh của editor",
			c.tagOf(nested[0]), d.File, d.Line, d.Column)
	}
	if len(popovers) > 1 {
		first := popovers[0]
		return nil, errAt(popovers[1], "<dropdown> của thanh công cụ có một <popover>: <popover> ở %s:%d:%d đã là của nó", first.File, first.Line, first.Column)
	}
	if len(triggers) == 0 {
		return nil, errAt(d, `<dropdown> trong thanh công cụ của editor cần nút mở nó, <button click="toggle()">, ngoài <popover>: nút đó là một điểm dừng của Tab trong thanh`)
	}
	menu.trigger, menu.popover = triggers[0], popovers[0]
	if extra := append(append([]*Node(nil), triggers[1:]...), others...); len(extra) > 0 {
		second := extra[0]
		for _, other := range extra[1:] {
			if other.Line < second.Line || other.Line == second.Line && other.Column < second.Column {
				second = other
			}
		}
		return nil, errAt(second, `<dropdown> trong thanh công cụ của editor có một nút ngoài <popover>, nút mở nó (<button click="toggle()"> ở %s:%d:%d): đặt nút này trong <popover>`,
			menu.trigger.File, menu.trigger.Line, menu.trigger.Column)
	}
	if a := attrNamed(menu.trigger, "expanded"); a.Name == "expanded" {
		return nil, errAttr(menu.trigger, a, "expanded của nút mở dropdown trong thanh công cụ là việc của engine (aria-expanded theo open): bỏ expanded")
	}
	menu.popoverID = strings.TrimSpace(attrNamed(menu.popover, "id").Value)
	if menu.popoverID == "" {
		menu.popoverID = fmt.Sprintf("kitwork-editor-%d-menu-%d", s.number, len(s.menus)+1)
	}
	return menu, nil
}

// editorMenuButton reads a button written in a dropdown of the host: a command of the editor called
// through its alias, in the popover (b); the dropdown's trigger, a bare toggle() outside the popover
// (trigger); or neither. A bare call of a command of the editor there is told: toggle() is the
// dropdown's, and the rest would not keep the visitor's selection as a button of the toolbar does.
func (c *compiler) editorMenuButton(n *Node, alias string, menu *editorMenu, popover, repeat *Node) (*editorButton, bool, error) {
	click := editorClick(n)
	if click == nil {
		return nil, false, nil
	}
	e, err := compileExpr(click.Value, n, click)
	if err != nil {
		return nil, false, nil // wireEvents tells it
	}
	if value, ok := editorFormatWrite(e.ir); ok {
		// a dropdown closes on each command and is no stop of the toolbar's arrows inside: the formats are
		// a group of the toolbar, or a select
		return nil, false, errAttr(n, click, `format = %s trong <dropdown>: nút đổi cách viết đứng trong thanh công cụ, một nhóm của nó (<group alt="Viết bằng">%s…</group>), hay là một <select value="{{ format }}" alt="Viết bằng">`,
			editorArgSource(value), editorFormatSwitch)
	}
	if method, args, ok := editorCommand(e.ir); ok {
		switch {
		case method == "toggle" && len(args) == 0:
			return nil, popover == nil, nil // the dropdown's own: it opens and closes it
		case method == "close" && len(args) == 0:
			return nil, false, nil // the dropdown's own close
		}
		written := editorCallSource(method, args)
		through := `đặt as trên <editor> (<editor as="$body">) rồi gọi $body.` + written
		if alias != "" {
			through = alias + "." + written
		}
		return nil, false, errAttr(n, click, "%s trong <dropdown>: tên trần bên trong <dropdown> là của nó (toggle() mở và đóng nó); lệnh của editor ở đây gọi qua alias, %s", written, through)
	}
	method, args, ok := editorAliasCall(e.ir, alias)
	if !ok {
		return nil, false, nil
	}
	written := alias + "." + editorCallSource(method, args)
	switch {
	case popover == nil:
		return nil, false, errAttr(n, click, `%s ở ngoài <popover> của <dropdown>: nút lệnh của dropdown trong thanh công cụ nằm trong <popover> của nó; ngoài <popover> chỉ có nút mở nó, <button click="toggle()">`, written)
	case method == "link":
		return nil, false, errAttr(n, click, `%s không đặt trong <dropdown>: nút liên kết mở khung liên kết của editor, viết nó thẳng trong thanh công cụ (<button click="link()">)`, written)
	case method == "close":
		return nil, false, errAttr(n, click, `%s đóng khung liên kết của editor: nút này là của <popover> của liên kết (<button click="close()">); dropdown của thanh tự đóng sau mỗi lệnh`, written)
	}
	b := &editorButton{node: n, click: click, command: method, menu: menu, path: menu.path}
	if err := editorNotRepeated(n, click, repeat); err != nil {
		return nil, false, err
	}
	if err := editorReadCommand(n, click, b, args); err != nil {
		return nil, false, err
	}
	c.editorPressedWarning(n, b)
	return b, false, nil
}

// editorBareName reports whether an argument is the bare name given.
func editorBareName(ir any, name string) bool {
	node, ok := ir.([]any)
	return ok && len(node) == 2 && node[0] == "$" && node[1] == name
}

// editorCallSource writes a call of an editor's method as markup writes it, its arguments when each is
// a string, a number or a bare name (toggle('heading', 2)), "…" otherwise.
func editorCallSource(method string, args []any) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, editorArgSource(arg))
	}
	return method + "(" + strings.Join(parts, ", ") + ")"
}

// editorFormatNames are the names other editors give the formats, each with the format's name here and
// how its button calls it.
var editorFormatNames = func() map[string][2]string {
	out := map[string][2]string{}
	add := func(name, call string, words ...string) {
		for _, word := range words {
			out[word] = [2]string{name, call}
		}
	}
	add("bold", "toggle('bold')", "strong", "b")
	add("italic", "toggle('italic')", "em", "i")
	add("underline", "toggle('underline')", "u", "ins", "underlined")
	add("strikethrough", "toggle('strikethrough')", "strike", "s", "del", "line-through", "strikeout", "strike-through")
	add("code", "toggle('code')", "inline-code", "monospace", "mono", "kbd")
	add("code 'block'", "toggle('code', 'block')", "pre", "codeblock", "code-block", "block-code", "preformatted")
	add("quote", "toggle('quote')", "blockquote")
	add("heading mức 2", "toggle('heading', 2)", "h2", "heading-2", "subheading")
	for level := 3; level <= 6; level++ {
		l := strconv.Itoa(level)
		add("heading mức "+l, "toggle('heading', "+l+")", "h"+l, "heading-"+l)
	}
	add("list", "toggle('list')", "bullets", "bullet", "ul", "unordered")
	add("list 'ordered'", "toggle('list', 'ordered')", "numbers", "ol", "numbered-list", "numbered", "ordered", "ordered-list")
	return out
}()

// editorHeadingLevels is how a heading's levels are called, for a message.
const editorHeadingLevels = "toggle('heading', 2) tới toggle('heading', 6)"

// editorFormat reads what a toggle(…) applies: a format written out — bold, italic, underline,
// strikethrough, code (inline, or with 'block' a code block), quote, heading with its level (2 to 6;
// 1 is the page's title), list with an optional 'ordered' — into the name its button carries on the wire
// (bold, code-block, heading-4, list-ordered).
func editorFormat(n *Node, a *Attr, args []any) (string, error) {
	const named = "toggle cần tên định dạng viết sẵn, như toggle('bold'): engine đọc nó để viết aria-pressed và phím tắt"
	if len(args) == 0 {
		return "", errAttr(n, a, "%s", named)
	}
	format, ok := stringLiteral(args[0])
	if !ok {
		return "", errAttr(n, a, "%s", named)
	}
	written := editorCallSource("toggle", args)
	switch format {
	case "bold", "italic", "underline", "strikethrough", "quote":
		if len(args) > 1 {
			return "", errAttr(n, a, "%s: %s không nhận thêm gì, viết toggle('%s')", written, format, format)
		}
		return format, nil
	case "code":
		switch len(args) {
		case 1:
			return "code", nil
		case 2:
			if variant, ok := stringLiteral(args[1]); ok && variant == "block" {
				return "code-block", nil
			}
		}
		return "", errAttr(n, a, "%s: code nhận 'block' hay không gì: toggle('code') là mã trong dòng, toggle('code', 'block') là khối mã", written)
	case "heading":
		if len(args) == 1 {
			return "", errAttr(n, a, "toggle('heading') cần mức: %s", editorHeadingLevels)
		}
		if len(args) > 2 {
			return "", errAttr(n, a, "%s: heading nhận một mức, %s", written, editorHeadingLevels)
		}
		level, isNumber := editorNumber(args[1])
		switch {
		case !isNumber:
			return "", errAttr(n, a, "%s: mức của heading là một số viết sẵn, %s", written, editorHeadingLevels)
		case level >= 2 && level <= 6 && level == float64(int(level)):
			return "heading-" + strconv.Itoa(int(level)), nil
		case level == 1:
			return "", errAttr(n, a, `toggle('heading', 1): heading 1 là tiêu đề của trang (<text heading="1">); trong bài viết dùng %s`, editorHeadingLevels)
		}
		return "", errAttr(n, a, "%s: bài viết có tiêu đề mục từ mức 2 tới mức 6; viết %s", written, editorHeadingLevels)
	case "h1", "heading-1", "title":
		return "", errAttr(n, a, `%s: heading 1 là tiêu đề của trang (<text heading="1">); trong bài viết dùng %s`, written, editorHeadingLevels)
	case "rule", "hr", "horizontal-rule", "divider", "separator":
		return "", errAttr(n, a, `%s: đường kẻ ngang là rule(), nó chèn một <hr> sau khối ở con trỏ (<button click="rule()">)`, written)
	case "align", "left", "center", "right", "justify", "start", "end", "align-left", "align-center", "align-right", "align-justify":
		side := strings.TrimPrefix(format, "align-")
		if format == "align" && len(args) == 2 {
			side, _ = stringLiteral(args[1])
		}
		call := editorSides
		if side == "left" || side == "center" || side == "right" {
			call = "align('" + side + "')"
		}
		return "", errAttr(n, a, "%s: căn lề không phải một định dạng của toggle: viết %s", written, call)
	case "list":
		switch {
		case len(args) == 1:
			return "list", nil
		case len(args) == 2:
			if variant, ok := stringLiteral(args[1]); ok && variant == "ordered" {
				return "list-ordered", nil
			}
			return "", errAttr(n, a, `%s: list nhận 'ordered' hay không gì, như <list ordered="true">`, written)
		}
		return "", errAttr(n, a, "%s: list nhận 'ordered' hay không gì", written)
	case "paragraph", "p", "normal", "text":
		return "", errAttr(n, a, "%s: editor không có định dạng %s: bấm lại nút đang bật là về đoạn văn", written, format)
	case "link":
		return "", errAttr(n, a, "%s: liên kết là link(), nó mở ô địa chỉ", written)
	case "image", "table":
		return "", errAttr(n, a, "%s: editor chưa có định dạng này ở bản này; có %s", written, editorFormatList)
	}
	if named, ok := editorFormatNames[format]; ok {
		return "", errAttr(n, a, "%s: định dạng này tên là %s, viết %s", written, named[0], named[1])
	}
	return "", errAttr(n, a, "%s: editor không có định dạng %s; có %s%s", written, format, editorFormatList,
		nearestWords(format, []string{"bold", "italic", "underline", "strikethrough", "code", "quote", "heading", "list"}))
}

// editorAlign reads the side an align(…) puts the blocks of the selection on, written out: left (where a
// paragraph stands when nothing is written), center or right. As everywhere in .ui it says left and
// right, which a page written right to left mirrors (the value writes text-align:end for right).
func editorAlign(n *Node, a *Attr, args []any) (string, error) {
	const named = "align cần một phía viết sẵn, " + editorSides + ": engine đọc nó để viết aria-pressed"
	if len(args) == 0 {
		return "", errAttr(n, a, "%s", named)
	}
	side, ok := stringLiteral(args[0])
	if !ok {
		return "", errAttr(n, a, "%s", named)
	}
	written := editorCallSource("align", args)
	if len(args) > 1 {
		return "", errAttr(n, a, "%s: align nhận một phía, %s", written, editorSides)
	}
	switch side {
	case "left", "center", "right":
		return side, nil
	case "start":
		return "", errAttr(n, a, "%s: .ui nói trái, phải (trang viết từ phải sang trái lật như gương): viết align('left')", written)
	case "end":
		return "", errAttr(n, a, "%s: .ui nói trái, phải (trang viết từ phải sang trái lật như gương): viết align('right')", written)
	case "justify":
		return "", errAttr(n, a, "%s: editor chưa có căn đều ở bản này: tiếng Việt căn đều dễ hở chữ giữa các từ; có left, center, right", written)
	case "middle", "centre":
		return "", errAttr(n, a, "%s: giữa là center, viết align('center')", written)
	}
	return "", errAttr(n, a, "%s: editor không căn %s; có left, center, right%s", written, side, nearestWords(side, []string{"left", "center", "right"}))
}

// editorNumber reads a number written out.
func editorNumber(ir any) (float64, bool) {
	node, ok := ir.([]any)
	if !ok || len(node) != 2 || node[0] != "#" {
		return 0, false
	}
	v, ok := node[1].(float64)
	return v, ok
}

// editorToolbars finds the toolbars of the command buttons outside the popover (a dropdown's commands
// stand where their dropdown does): the buttons whose outermost container that does not hold the area is
// the same are one toolbar, their nearest common container that is no <group>. It must be a box, a card
// or a section, named by its alt; each of its buttons, in every group, and the trigger of each of its
// dropdowns, takes part in its one roving tabindex.
func (c *compiler) editorToolbars(s *editorScope, buttons []*editorButton, areaPath []*Node) error {
	holds := map[editorKey]bool{}
	for _, p := range areaPath {
		holds[editorKeyOf(p)] = true // every element around the area holds it
	}
	var order []editorKey
	regions := map[editorKey][]*editorButton{}
	for _, b := range buttons {
		var region *Node
		for _, p := range b.path {
			if !holds[editorKeyOf(p)] {
				region = p
				break
			}
		}
		if region == nil {
			return errAttr(b.node, b.click, `nút của editor (click="%s") nằm chung khung với vùng viết: đặt các nút trong một khung riêng có alt, <box alt="Định dạng">…</box>, engine viết role="toolbar" lên khung đó`, b.click.Value)
		}
		k := editorKeyOf(region)
		if regions[k] == nil {
			order = append(order, k)
		}
		regions[k] = append(regions[k], b)
	}
	for _, k := range order {
		members := regions[k]
		common := members[0].path
		for _, b := range members[1:] {
			i := 0
			for i < len(common) && i < len(b.path) && editorKeyOf(common[i]) == editorKeyOf(b.path[i]) {
				i++
			}
			common = common[:i]
		}
		// the buttons of one group stand in it: the toolbar is what holds the group
		var group *Node
		for len(common) > 0 && common[len(common)-1].Tag == "group" {
			group, common = common[len(common)-1], common[:len(common)-1]
		}
		if group != nil && (len(common) == 0 || holds[editorKeyOf(common[len(common)-1])]) {
			return errAt(group, `<group> là một nhóm nút trong thanh công cụ của editor: đặt các <group> trong một khung có alt, <box alt="Định dạng"><group alt="Kiểu chữ">…</group></box>, engine viết role="toolbar" lên khung đó`)
		}
		toolbar := common[len(common)-1] // the region at least: every path holds it
		switch toolbar.Tag {
		case "box", "card", "section":
		default:
			first := members[0].node
			return errAt(toolbar, "thanh công cụ của editor là một <box>, <card> hay <section> có alt: các nút như nút ở %s:%d:%d nằm chung trong <%s>", first.File, first.Line, first.Column, c.tagOf(toolbar))
		}
		if alt := attrNamed(toolbar, "alt"); alt.Name != "alt" || strings.TrimSpace(alt.Value) == "" {
			return errAt(toolbar, `thanh công cụ của editor cần alt: engine viết role="toolbar", alt là tên của thanh (<box alt="Định dạng">)`)
		}
		// what a toolbar holds is its buttons, each always there: the arrows go along them and the first
		// is the toolbar's one stop for Tab, which a button left out (if), hidden (show) or repeated (for)
		// would take away; a popover or a component inside it brings buttons of its own the arrows would
		// go into, but for a dropdown of more commands, whose trigger is one stop of the toolbar
		t := &editorToolbar{node: toolbar}
		var collect func(nodes []*Node, group *Node, menu *editorMenu) error
		collect = func(nodes []*Node, group *Node, menu *editorMenu) error {
			for _, ch := range nodes {
				if ch.IsText() {
					continue
				}
				inner, at := group, editorKeyOf(ch)
				switch m := s.menus[at]; {
				case m != nil && menu == nil:
				case menu != nil && ch.Tag == "popover" && at == editorKeyOf(menu.popover):
				case ch.Tag == "dropdown" && menu == nil:
					if stray := editorFirstStray(s, ch); stray != nil {
						alias := strings.TrimSpace(attrNamed(s.node, "as").Value)
						through, call, _ := editorStrayCall(stray, alias, strings.TrimSpace(attrNamed(ch, "as").Value))
						return errAttr(stray, editorClick(stray), "%s", editorStrayMessage(alias, through, call))
					}
					return errAt(ch, `<dropdown> trong thanh công cụ của editor (thanh ở %s:%d:%d) giữ thêm lệnh của editor: nút mở nó <button click="toggle()">, và trong <popover> các nút gọi editor qua alias (<button click="$body.toggle('heading', 4)">); một dropdown khác đặt cạnh thanh, trong <editor>`,
						toolbar.File, toolbar.Line, toolbar.Column)
				case ch.Tag == "popover" || c.callsComponent(ch):
					return errAt(ch, "<%s> không đặt trong thanh công cụ của editor (thanh ở %s:%d:%d): phím mũi tên của thanh đi qua từng nút của nó, còn nút của <%s> là của <%s>; đặt <%s> cạnh thanh, trong <editor>",
						c.tagOf(ch), toolbar.File, toolbar.Line, toolbar.Column, c.tagOf(ch), c.tagOf(ch), c.tagOf(ch))
				case ch.Tag == "group" && group != nil:
					return errAt(ch, "<group> không lồng trong <group> (nhóm ngoài ở %s:%d:%d): mỗi nhóm là một hàng nút của thanh công cụ", group.File, group.Line, group.Column)
				case ch.Tag == "group" && menu == nil:
					s.groups[at], inner = true, ch
				}
				for i := range ch.Attrs {
					a := &ch.Attrs[i]
					switch {
					case a.Name == "for":
						return errAttr(ch, a, "for không có trong thanh công cụ của editor (thanh ở %s:%d:%d): nút của thanh viết từng nút một, nút đầu là điểm dừng của Tab", toolbar.File, toolbar.Line, toolbar.Column)
					case a.Name == "if", a.Name == "show", strings.HasSuffix(a.Name, ":show"):
						return errAttr(ch, a, "%s không có trong thanh công cụ của editor (thanh ở %s:%d:%d): nút của thanh luôn có mặt, nút đầu là điểm dừng của Tab; viết %s trên cả thanh", a.Name, toolbar.File, toolbar.Line, toolbar.Column, a.Name)
					}
				}
				if ch.Tag == "button" && (menu == nil || at == editorKeyOf(menu.trigger)) {
					t.buttons = append(t.buttons, ch)
				}
				within := menu
				if within == nil {
					within = s.menus[at]
				}
				if err := collect(ch.Children, inner, within); err != nil {
					return err
				}
			}
			return nil
		}
		if err := collect(toolbar.Children, nil, nil); err != nil {
			return err
		}
		for i, b := range t.buttons {
			tab := "-1"
			if i == 0 {
				tab = "0"
			}
			s.roving[editorKeyOf(b)] = tab
		}
		s.toolbars[editorKeyOf(toolbar)] = t
	}
	return nil
}

// editorPopover reads the link's popover: one <input value="{{ address }}"> named by its alt, written
// with none of the words the engine writes on it, and at most one <error>.
func (c *compiler) editorPopover(s *editorScope, inputs, errorsFound []editorFound) error {
	const address = `<input value="{{ address }}" alt="Địa chỉ liên kết" />`
	if len(inputs) == 0 {
		return errAt(s.popover, "popover của editor cần đúng một <input> làm ô địa chỉ: %s", address)
	}
	if len(inputs) > 1 {
		first := inputs[0].node
		return errAt(inputs[1].node, "popover của editor có một ô địa chỉ: <input> ở %s:%d:%d đã là ô của nó", first.File, first.Line, first.Column)
	}
	in := inputs[0].node
	for i := range in.Attrs {
		a := &in.Attrs[i]
		switch a.Name {
		case "type":
			return errAttr(in, a, `ô địa chỉ không viết type: engine viết inputmode="url"; type="url" chặn cả form khi ô giữ /trang`)
		case "name":
			return errAttr(in, a, "ô địa chỉ không có name: nó không gửi lên server cùng bài viết")
		case "inputmode", "autocomplete", "autocapitalize", "spellcheck":
			return errAttr(in, a, `%s của ô địa chỉ là việc của engine (inputmode="url" autocomplete="off" autocapitalize="none" spellcheck="false"): bỏ %s`, a.Name, a.Name)
		case "value":
			read, err := parseInterpolation(a.Value, in, a)
			if err != nil {
				return err
			}
			if e := read.single(); e == nil || !editorBareName(e.ir, "address") {
				return errAttr(in, a, `ô địa chỉ của editor là %s: nó ghi chữ người dùng gõ vào address, link(address) đọc nó`, address)
			}
		case "alt":
			s.addressAlt = a
		}
	}
	if attrNamed(in, "value").Name != "value" {
		return errAt(in, `ô địa chỉ của editor là %s: nó ghi chữ người dùng gõ vào address, link(address) đọc nó`, address)
	}
	if s.addressAlt == nil || strings.TrimSpace(s.addressAlt.Value) == "" {
		return errAt(in, `ô địa chỉ của editor cần alt: tên của ô, cũng là tên của khung liên kết (alt="Địa chỉ liên kết")`)
	}
	s.address = in
	if len(errorsFound) > 1 {
		first := errorsFound[0].node
		return errAt(errorsFound[1].node, "popover của editor có một lời báo địa chỉ sai: <error> ở %s:%d:%d đã là của nó", first.File, first.Line, first.Column)
	}
	if len(errorsFound) == 1 {
		s.addressError = errorsFound[0].node
		s.errorID = fmt.Sprintf("kitwork-editor-%d-address-error", s.number)
	}
	return nil
}

// editorKeyshortcuts are the keys of the commands that have one, as the server writes them (Control:
// the component writes Meta on Apple devices).
var editorKeyshortcuts = map[string]string{
	"bold": "Control+B", "italic": "Control+I", "underline": "Control+U", "link": "Control+K", "undo": "Control+Z", "redo": "Control+Shift+Z",
}

// editorClasses are the classes the engine adds to an element of an editor's host: a toolbar's, a
// group's (a line is drawn between two), the popover's, the address input's and its error's.
func (s *editorScope) editorClasses(n *Node) []string {
	k := editorKeyOf(n)
	switch {
	case s.toolbars[k] != nil:
		return []string{"editor-toolbar"}
	case s.groups[k]:
		return []string{"editor-group"}
	case s.popover != nil && k == editorKeyOf(s.popover):
		return []string{"editor-popover"}
	case s.address != nil && k == editorKeyOf(s.address):
		return []string{"editor-address"}
	case s.addressError != nil && k == editorKeyOf(s.addressError):
		return []string{"editor-address-error"}
	}
	return nil
}

// inPopover reports whether n is the editor's popover.
func (s *editorScope) inPopover(n *Node) bool {
	return s.popover != nil && editorKeyOf(n) == editorKeyOf(s.popover)
}

// editorElement writes what the engine adds to an element inside an editor's host: a toolbar's role,
// name and control, a group's role, a button's place in its toolbar's tab order and its command, the
// popover's role and name, the address input's words and its error's id. A field two-way on one of the
// editor's names other than the address input's is told: the kernel would write what is typed into a
// name the editor keeps.
func (c *compiler) editorElement(n *Node, p *attrPlan, o *elemOp, w *strings.Builder, cx ctx) error {
	s := cx.editor
	k := editorKeyOf(n)
	if s.toolbars[k] != nil {
		w.WriteString(` role="toolbar"`)
		writeAttr(w, "aria-controls", s.areaID)
	}
	if s.groups[k] {
		w.WriteString(` role="group"`) // named by its alt, written before (tagAttributes)
	}
	if tab, ok := s.roving[k]; ok {
		writeAttr(w, "tabindex", tab)
	}
	if b := s.buttons[k]; b != nil {
		if err := c.editorButtonWire(n, b, s, o, w); err != nil {
			return err
		}
	}
	if s.inPopover(n) {
		if p.id == "" {
			writeAttr(w, "id", s.popoverID)
		}
		w.WriteString(` role="dialog"`)
		if err := c.attrValue(s.address, s.addressAlt, "aria-label", false, o, w); err != nil {
			return err
		}
	}
	if s.address != nil && k == editorKeyOf(s.address) {
		w.WriteString(` type="text" inputmode="url" autocomplete="off" autocapitalize="none" spellcheck="false"`)
		return nil
	}
	if s.addressError != nil && k == editorKeyOf(s.addressError) {
		writeAttr(w, "id", s.errorID)
		w.WriteString(" hidden") // the component shows it when link(address) refuses an address
		return nil
	}
	if s.selects[k] != nil {
		// a select of format, marked for the component: the kernel binds it two-way (selectBind), the
		// server paints the option of the format it starts in selected (editorSelectOf). In an editor that
		// takes no writing it is disabled (it posts nothing either way: it has no name), so no choice is
		// made that the component would refuse, and aria-disabled as the component writes it on every
		// command of such an editor
		writeAttr(w, "data-kitwork-command", "format")
		if s.readonly || s.disabled {
			if attrNamed(n, "disabled").Name != "disabled" {
				w.WriteString(" disabled")
			}
			w.WriteString(` aria-disabled="true"`)
		}
		return nil
	}
	return c.editorField(n, cx.scope)
}

// editorButtonWire writes a command button's wire: its format (and aria-pressed) or its command (an
// align's aria-pressed too), its keys, the popover link() opens, and aria-disabled where nothing can be
// done yet.
func (c *compiler) editorButtonWire(n *Node, b *editorButton, s *editorScope, o *elemOp, w *strings.Builder) error {
	if b.command == "format" {
		// a switch of format: pressed while format is its format, the kernel's binding alone (the server
		// paints the format the editor starts in; the component writes no aria-pressed of its own), so
		// it says what format and $body.format read, at once on a click, and again the one shown when a
		// switch is refused (the component writes format back). An editor that takes no writing keeps
		// the format it starts in (the component refuses the switch), so its switches are aria-disabled
		// as every button is there.
		writeAttr(w, "data-kitwork-command", "format")
		writeAttr(w, "data-kitwork-format-value", b.value)
		in, err := parseInterpolation("{{ format == "+quoteString(b.value)+" }}", n, b.click)
		if err != nil {
			return err
		}
		o.dynAttrs = append(o.dynAttrs, dynAttr{name: "aria-pressed", in: in})
		if s.readonly || s.disabled {
			w.WriteString(` aria-disabled="true"`)
		}
		return nil
	}
	if b.format != "" {
		writeAttr(w, "data-kitwork-format", b.format)
	} else {
		writeAttr(w, "data-kitwork-command", b.wire)
	}
	if b.pressed() {
		w.WriteString(` aria-pressed="false"`)
	}
	shortcut := editorKeyshortcuts[b.format]
	if b.format == "" && !b.popover {
		shortcut = editorKeyshortcuts[b.command]
	}
	if shortcut != "" {
		writeAttr(w, "aria-keyshortcuts", shortcut)
	}
	if b.command == "link" && !b.apply {
		writeAttr(w, "aria-controls", s.popoverID)
		w.WriteString(` aria-haspopup="dialog"`)
		in, err := parseInterpolation("{{ open }}", n, b.click)
		if err != nil {
			return err
		}
		o.dynAttrs = append(o.dynAttrs, dynAttr{name: "aria-expanded", in: in})
	}
	// nothing to undo or redo yet, no text chosen to link; and nothing at all in an editor that does not
	// take writing
	if s.readonly || s.disabled || b.command == "undo" || b.command == "redo" || b.command == "link" && !b.apply {
		w.WriteString(` aria-disabled="true"`)
	}
	return nil
}

// editorMenuElement writes what the engine adds to the parts of a dropdown of an editor's toolbar, which
// compile inside the dropdown, where no editor is around: its trigger's place in the toolbar's tab order
// and the popover it opens (aria-controls, aria-expanded bound to the dropdown's open), the popover's id,
// and each command's wire as a button of the toolbar carries it. Nothing for any other element.
func (c *compiler) editorMenuElement(n *Node, p *attrPlan, o *elemOp, w *strings.Builder) error {
	s, k := c.editorOfMenu(n)
	if s == nil {
		return nil
	}
	if b := s.menuButtons[k]; b != nil {
		return c.editorButtonWire(n, b, s, o, w)
	}
	menu := s.menuParts[k]
	if k == editorKeyOf(menu.popover) {
		if p.id == "" {
			writeAttr(w, "id", menu.popoverID)
		}
		return nil
	}
	if tab, ok := s.roving[k]; ok {
		writeAttr(w, "tabindex", tab)
	}
	writeAttr(w, "aria-controls", menu.popoverID)
	in, err := parseInterpolation("{{ open }}", n, editorClick(n))
	if err != nil {
		return err
	}
	o.dynAttrs = append(o.dynAttrs, dynAttr{name: "aria-expanded", in: in}) // the dropdown's open
	return nil
}

// editorOfMenu is the editor whose toolbar's dropdown n is a part of (its trigger, its popover or a
// command in it), with n's key; nil for none. The latest editor first: the one compiling now.
func (c *compiler) editorOfMenu(n *Node) (*editorScope, editorKey) {
	k := editorKeyOf(n)
	for i := len(c.editors) - 1; i >= 0; i-- {
		if s := c.editors[i]; s.menuButtons[k] != nil || s.menuParts[k] != nil {
			return s, k
		}
	}
	return nil, k
}

// editorMenuOwn reports whether name, read at n, is a toolbar's dropdown's own by the engine's reading of
// it: toggle() on its trigger, close() and toggle() in the events of the dropdown itself. The editor
// around declares both too, which is no second meaning there: the trigger opens the dropdown, and its
// commands call the editor through the alias (checkNames does not warn of it).
func (c *compiler) editorMenuOwn(n *Node, name string) bool {
	if name != "toggle" && name != "close" {
		return false
	}
	k := editorKeyOf(n)
	for i := len(c.editors) - 1; i >= 0; i-- {
		s := c.editors[i]
		if s.menus[k] != nil {
			return true
		}
		if m := s.menuParts[k]; m != nil && k == editorKeyOf(m.trigger) {
			return true
		}
	}
	return false
}

// editorGroupPlace refuses a <group> anywhere but in a toolbar of an editor, where it is a group of the
// toolbar's buttons the engine names (role="group") and draws a line before.
func editorGroupPlace(n *Node, cx ctx) error {
	if cx.editor != nil && cx.editor.groups[editorKeyOf(n)] {
		return nil
	}
	return errAt(n, `<group> là một nhóm nút trong thanh công cụ của editor (<box alt="Định dạng"><group alt="Kiểu chữ">…</group></box>); ở chỗ khác gom các phần bằng <box>`)
}

// editorField refuses a field written inside an editor's host that is two-way on one of the editor's
// names (value="{{ words }}"): the kernel would write what is typed into a name the editor keeps. The
// address input is the one field that writes one (address). A field readonly or disabled for good only
// shows the name.
func (c *compiler) editorField(n *Node, scope *frame) error {
	if !editorEdited(n) {
		return nil
	}
	kind := strings.ToLower(strings.TrimSpace(attrNamed(n, "type").Value))
	edits := "value" // what the person changes: a choice's checked, any other field's value
	if n.Tag == "input" && (kind == "checkbox" || kind == "radio") {
		edits = "checked"
	}
	for i := range n.Attrs {
		a := &n.Attrs[i]
		if a.Name != edits {
			continue
		}
		read, err := parseInterpolation(a.Value, n, a)
		if err != nil {
			return nil // the attribute's own compile tells it
		}
		single := read.single()
		if single == nil {
			continue
		}
		ref, ok := single.ir.([]any)
		if !ok || len(ref) != 2 || ref[0] != "$" {
			continue
		}
		name, _ := ref[1].(string)
		if owner := scope.declaring(name); owner == nil || owner.editor == nil || name == "format" {
			continue // format: wherever the editor's names reach (editorFormatField)
		}
		if name == "address" {
			return errAttr(n, a, `address là chữ trong ô địa chỉ của editor: chỉ <input value="{{ address }}"> trong <popover> của nó ghi address`)
		}
		return errAttr(n, a, `%s của editor chỉ đọc: ô này ghi ngược chữ gõ vào %s; chỉ để hiện thì thêm readonly="true"`, name, name)
	}
	return nil
}

// editorEdited reports whether n is a field a person types or chooses in: an input that holds what is
// typed or chosen, a textarea, a select; not one readonly or disabled for good, which only shows.
func editorEdited(n *Node) bool {
	switch n.Tag {
	case "input":
		switch strings.ToLower(strings.TrimSpace(attrNamed(n, "type").Value)) {
		case "hidden", "button", "submit", "reset", "image", "file":
			return false
		}
	case "textarea", "select":
	default:
		return false
	}
	for _, word := range []string{"readonly", "disabled"} {
		if a := attrNamed(n, word); a.Name == word && (a.Bare || strings.TrimSpace(a.Value) == "true") {
			return false
		}
	}
	return true
}

// editorEdits is the attribute of a field that a person changes: a choice's checked, any other field's
// value.
func editorEdits(n *Node) string {
	if kind := strings.ToLower(strings.TrimSpace(attrNamed(n, "type").Value)); n.Tag == "input" && (kind == "checkbox" || kind == "radio") {
		return "checked"
	}
	return "value"
}

// editorTarget is the bare name an expression node calls or writes, and which; "" for any other node.
func editorTarget(node []any) (name string, writes bool) {
	switch {
	case len(node) == 3 && node[0] == "call":
		if callee, ok := node[1].([]any); ok && len(callee) == 2 && callee[0] == "$" {
			name, _ = callee[1].(string)
		}
		return name, false
	case len(node) >= 2 && (node[0] == "=" || node[0] == "++" || node[0] == "--"):
		name, _ = node[1].(string)
		return name, true
	}
	return "", false
}

// editorCallStrangers are the methods of other editors, each with what does it here.
var editorCallStrangers = func() map[string]string {
	out := map[string]string{}
	add := func(message string, words ...string) {
		for _, word := range words {
			out[word] = message
		}
	}
	add("định dạng là toggle với tên viết sẵn: toggle('bold'), toggle('heading', 2)", "run", "exec", "execCommand", "command", "bold", "italic",
		"underline", "strikethrough", "strike", "code", "heading", "quote", "list")
	add("căn lề là "+editorSides, "justify", "setAlign", "textAlign", "alignment")
	add("đường kẻ ngang là rule(), nó chèn một <hr> sau khối ở con trỏ", "hr", "insertRule", "insertHorizontalRule", "horizontalRule", "divider")
	add("engine viết aria-pressed lên nút định dạng; tô nút đang bật bằng active:", "isActive", "active", "pressed")
	add("mở ô địa chỉ là link(), gắn là link(address)", "beginLink", "setLink", "insertLink")
	add("gắn liên kết là link(address), trong <popover> của editor", "applyLink")
	add("đóng ô địa chỉ là close()", "cancelLink", "cancel")
	add("bỏ liên kết là unlink()", "removeLink")
	add("bài lúc đầu là value của <content>, server vẽ sẵn", "set", "clear", "insertHTML", "setContent")
	add(`lưu là việc của form: <button type="submit">, router.post nhận`, "save", "submit")
	return out
}()

// editorWriteProblem is the error of an editor's name written by markup: its state is its own, the
// address is its input's, its methods are called.
func editorWriteProblem(name string) string {
	switch {
	case name == "address":
		return `address là chữ trong ô địa chỉ của editor: chỉ <input value="{{ address }}"> trong <popover> của nó ghi address`
	case name == "open":
		return "open của editor chỉ đọc: mở ô địa chỉ là link(), đóng là close()"
	case name == "dropped":
		return "dropped của editor chỉ đọc: editor ghi vào đó tên các thẻ mà lần đổi cách viết, lần dán hay HTML trong Markdown vừa bỏ"
	case containsName(editorMethods, name):
		return name + " là hàm của editor, không gán được: gọi nó, " + editorCallExample[name]
	}
	return name + " của editor chỉ đọc, editor tự tính khi người dùng viết"
}

// editorFormatWriteProblem is the error of a write of format anywhere but the click of a switch of
// format (a button written in the host whose click is that one assignment, editorSwitchOf): in another
// event, beside another write, in a component nested in the host, ++ or --.
func editorFormatWriteProblem(node []any) string {
	written := "format++"
	switch {
	case node[0] == "--":
		written = "format--"
	case node[0] == "=" && len(node) == 3:
		written = "format = " + editorArgSource(node[2])
	}
	return written + ": cách viết đổi bằng click của một <button> viết trong <editor>, cả click là phép gán đó (" + editorFormatSwitch +
		`), hay bằng một <select value="{{ format }}" alt="Viết bằng"> trong <editor>`
}

// editorFormatField refuses a field two-way on an editor's format, wherever the editor's names reach it,
// but for the editor's select of format (editorSelectOf): a text typed in it, or a select of another
// place, would write format with words the editor does not check.
func editorFormatField(n *Node, scope *frame) error {
	if !editorEdited(n) {
		return nil
	}
	edits := "value" // what the person changes: a choice's checked, any other field's value
	if kind := strings.ToLower(strings.TrimSpace(attrNamed(n, "type").Value)); n.Tag == "input" && (kind == "checkbox" || kind == "radio") {
		edits = "checked"
	}
	a := attrNamed(n, edits)
	if a.Name != edits {
		return nil
	}
	read, err := parseInterpolation(a.Value, n, a)
	if err != nil {
		return nil // the attribute's own compile tells it
	}
	if e := read.single(); e == nil || !editorBareName(e.ir, "format") {
		return nil
	}
	owner := scope.declaring("format")
	if owner == nil || owner.editor == nil || owner.editor.selects[editorKeyOf(n)] != nil {
		return nil
	}
	return errAttr(n, a, `ô nối hai chiều với format: cách viết đổi bằng nút trong <editor> (%s) hay một <select value="{{ format }}" alt="Viết bằng"> viết thẳng trong <editor>`, editorFormatSwitch)
}

// editorEvents checks the bare names an element's events call and write, wherever an editor's names
// reach them (its host and inside it, a layout or a component nested there that does not declare the
// name): a command is its buttons' alone, the click of a button found inside the host, so a call of one
// anywhere else is told; its state is read, never called or written. A name nothing declares, where the
// editor is the nearest component the browser runs, is told what the editor has when it is a word of
// other editors or close to one of its methods. runtimeAttributes calls it for every element the kernel
// wires.
func (c *compiler) editorEvents(n *Node, events []Attr, scope *frame) error {
	var nearest *frame
	for f := scope; f != nil && nearest == nil; f = f.parent {
		if f.editor != nil {
			nearest = f
		}
	}
	if nearest == nil {
		return nil
	}
	if err := editorFormatField(n, scope); err != nil {
		return err
	}
	for i := range events {
		a := &events[i]
		e, err := compileExpr(a.Value, n, a)
		if err != nil {
			continue // wireEvents reports it
		}
		free := usedNames(e.ir)
		var found error
		walkIR(e.ir, func(node []any) {
			name, writes := editorTarget(node)
			if found != nil || name == "" || !containsName(free, name) {
				return
			}
			owner := scope.declaring(name)
			if owner != nil && owner.editor != nil {
				s := owner.editor
				switch {
				case writes && name == "format":
					// its format, switched: by the click of a switch of format alone, the whole click that
					// one assignment of a format written out (editorSwitchOf)
					if b := s.buttons[editorKeyOf(n)]; b == nil || b.command != "format" || b.click.Name != a.Name || b.click.Value != a.Value {
						found = errAttr(n, a, "%s", editorFormatWriteProblem(node))
					}
				case writes:
					found = errAttr(n, a, "%s", editorWriteProblem(name))
				case !containsName(editorMethods, name):
					found = errAttr(n, a, "%s là trạng thái của editor, không gọi được: đọc %s; editor có: %s", name, name, strings.Join(editorMethods, ", "))
				default:
					b := s.buttons[editorKeyOf(n)]
					if b == nil || b.click.Name != a.Name || b.click.Value != a.Value {
						found = errAttr(n, a, `%s là lệnh của editor: gọi bằng click của một <button> viết trong <editor>, cả click là lời gọi đó (<button click="%s">), để chỗ chọn chữ còn nguyên khi bấm`,
							editorCallSource(name, editorArgs(node)), editorCallExample[name])
					}
				}
				return
			}
			if writes || owner != nil || clientGlobals[name] || scope.live() != nearest {
				return // a name of another level, the kernel's, or one a component nearer tells
			}
			if says := editorCallStrangers[name]; says != "" {
				found = errAttr(n, a, "editor không có %s: %s", name, says)
			} else if near := nearestWords(name, editorMethods); near != "" {
				found = errAttr(n, a, "editor không có %s; editor có: %s%s", name, strings.Join(editorMethods, ", "), near)
			}
		})
		if found != nil {
			return found
		}
	}
	return nil
}

// editorArgs are the arguments of a call node.
func editorArgs(node []any) []any {
	if len(node) == 3 {
		if args, ok := node[2].([]any); ok {
			return args
		}
	}
	return nil
}

// editorAlias reads $alias where an expression reads it, when the alias names an editor: that editor
// (nil when none of the page's is found) and the alias.
func (c *compiler) editorAlias(ir any, frame *aliasFrame) (*editorScope, string, bool) {
	ref, ok := ir.([]any)
	if !ok || len(ref) != 2 || ref[0] != "$" {
		return nil, "", false
	}
	alias, _ := ref[1].(string)
	if !aliasName(alias) || frame == nil {
		return nil, "", false
	}
	if def := frame.defOf(alias); def == nil || def.kitwork == nil || def.name != "editor" {
		return nil, "", false
	}
	owner := frame.setter(alias)
	for _, s := range c.editors {
		if s.alias == alias && s.aliases == owner {
			return s, alias, true
		}
	}
	return nil, alias, true
}

// editorReadStrangers are the names other editors' state has, read off the alias, each with what reads
// it here.
var editorReadStrangers = map[string]string{
	"html": "bài lúc đầu là value của <content>, server vẽ sẵn; số chữ là words, số ký tự characters", "value": "bài là value của <content>, form gửi nó; số chữ là words",
	"content": "bài là value của <content>, form gửi nó; số chữ là words", "text": "bài là value của <content>, form gửi nó; số chữ là words",
	"markdown": "cách viết đang chọn là format ('rich', 'markdown' hay 'html'); bài gửi đi luôn là HTML của richtext",
	"mode":     "cách viết đang chọn là format ('rich', 'markdown' hay 'html')", "view": "cách viết đang chọn là format ('rich', 'markdown' hay 'html')",
	"dirty": "chưa lưu là unsaved", "changed": "chưa lưu là unsaved",
	"modified": "chưa lưu là unsaved", "linking": "ô liên kết đang mở là open", "href": "chữ trong ô địa chỉ là address",
	"url": "chữ trong ô địa chỉ là address", "length": "số chữ là words, số ký tự characters", "count": "số chữ là words, số ký tự characters",
}

// checkEditors checks, once every as of the page is known, what reads an editor through its alias: its
// state is read, its commands are its buttons' (a call through the alias would take the selection away
// first); and tells a submit button kept disabled while the editor is empty, which a page without
// JavaScript can never send.
func (c *compiler) checkEditors() error {
	if len(c.editors) == 0 {
		return nil
	}
	for _, use := range c.aliasExprs {
		var err error
		walkIR(use.e.ir, func(node []any) {
			if err != nil {
				return
			}
			switch {
			case len(node) == 4 && node[0] == "()":
				s, alias, ok := c.editorAlias(node[1], use.frame)
				if !ok {
					return
				}
				method, _ := node[2].(string)
				args, _ := node[3].([]any)
				call := alias + "." + editorCallSource(method, args)
				if s != nil && use.attr != nil {
					if b := s.menuButtons[editorKeyOf(use.node)]; b != nil && b.click.Name == use.attr.Name && b.click.Value == use.attr.Value {
						return // a command in a toolbar's dropdown: the one place the alias calls one (editorMenuOf)
					}
				}
				switch {
				case containsName(editorMethods, method):
					for _, t := range c.editors {
						if t != s && t.strays[editorKeyOf(use.node)] != nil {
							err = toastError(use, "%s", editorStrayMessage(t.alias, alias, editorCallSource(method, args)))
							return // in another editor's dropdown: it calls the editor around it
						}
					}
					where := "ở ngoài <editor>"
					if s != nil && s.inside[editorKeyOf(use.node)] {
						where = "qua alias"
					}
					err = toastError(use, `%s %s: lệnh của editor là nút viết trong <editor> gọi tên trần (<button click="%s">), để giữ chỗ chọn chữ khi bấm`, call, where, editorCallSource(method, args))
				case containsName(editorNames, method):
					err = toastError(use, "%s là trạng thái của editor, không gọi được: đọc %s.%s", method, alias, method)
				case editorCallStrangers[method] != "":
					err = toastError(use, "editor không có %s: %s", method, editorCallStrangers[method])
				default:
					err = toastError(use, "editor không có %s; trạng thái của editor là %s%s", method, strings.Join(editorNames, ", "), nearestWords(method, editorNames))
				}
			case len(node) == 3 && node[0] == ".":
				_, alias, ok := c.editorAlias(node[1], use.frame)
				if !ok {
					return
				}
				name, _ := node[2].(string)
				switch {
				case name == "format" && use.attr != nil && use.attr.Name == editorEdits(use.node) && editorEdited(use.node):
					// a field two-way on the format through the alias, outside the editor: the kernel binds no
					// select of another place (selectBind is the editor's own), so it would show one format and
					// switch none
					err = toastError(use, `ô nối hai chiều với %s.format ở ngoài <editor>: qua alias format chỉ đọc; cách viết đổi bằng nút trong <editor> (%s) hay một <select value="{{ format }}" alt="Viết bằng"> viết thẳng trong <editor>`, alias, editorFormatSwitch)
				case containsName(editorNames, name):
				case containsName(editorMethods, name):
					err = toastError(use, `%s.%s là lệnh của editor: nút viết trong <editor> gọi nó (<button click="%s">)`, alias, name, editorCallExample[name])
				case editorReadStrangers[name] != "":
					err = toastError(use, "editor không có %s: %s (%s.%s)", name, editorReadStrangers[name], alias, editorReadFix(name))
				default:
					err = toastError(use, "editor không có %s; trạng thái của editor là %s%s", name, strings.Join(editorNames, ", "), nearestWords(name, editorNames))
				}
			}
		})
		if err != nil {
			return err
		}
		c.editorSubmitWarning(use)
	}
	return nil
}

// editorReadFix is the name read instead of one of editorReadStrangers.
func editorReadFix(name string) string {
	switch name {
	case "dirty", "changed", "modified":
		return "unsaved"
	case "linking":
		return "open"
	case "href", "url":
		return "address"
	case "markdown", "mode", "view":
		return "format"
	}
	return "words"
}

// editorSubmitWarning tells a submit button whose disabled reads an editor's empty through its alias:
// without JavaScript the button never gives way, and required on <content> already holds an empty post
// back.
func (c *compiler) editorSubmitWarning(use aliasExpr) {
	n, a := use.node, use.attr
	if a == nil || n.Tag != "button" || a.Name != "disabled" || strings.TrimSpace(attrNamed(n, "type").Value) != "submit" {
		return
	}
	walkIR(use.e.ir, func(node []any) {
		if len(node) != 3 || node[0] != "." || node[2] != "empty" {
			return
		}
		if _, alias, ok := c.editorAlias(node[1], use.frame); ok {
			c.warn(n.File, a.Line, a.Column, "nút gửi khoá theo "+alias+".empty: không có JavaScript thì không gửi được; required=\"true\" trên <content> đã chặn bài rỗng")
		}
	})
}

// bindings are the editor's first state, read where its call stands for each request: empty, words and
// characters of its value cleaned, unsaved when the page answers a request that is no GET (a refused save
// shown again: what was posted is not what is saved), open and address as they start, format the one it
// starts in (format="markdown", else rich), dropped none. Each goes into the host's data-kitwork-scope as
// a literal, the server paints the host's inside from it, and $body.words outside reads it (aliasStart).
func (s *editorScope) bindings() []binding {
	var args []any
	source := "''"
	if s.value != nil {
		args, source = []any{s.value.ir}, s.value.source
	}
	of := func(name string, read func(richtext.Document) any) binding {
		fn := hydrate.Func(func(in []any) any { return read(editorCleaned(stringify(arg(in, 0)))) })
		e := &expr{source: "richtext(" + source + ")." + name, ir: []any{"call", []any{"#", fn}, args}}
		return binding{name: name, in: &interpolation{parts: []part{{e: e}}}}
	}
	fixed := func(name string, v any, written string) binding {
		return binding{name: name, in: &interpolation{parts: []part{{e: &expr{source: written, ir: []any{"#", v}}}}}}
	}
	unsaved := hydrate.Func(func(in []any) any {
		method := strings.ToUpper(stringify(arg(in, 0)))
		return method != "" && method != "GET" && method != "HEAD"
	})
	return []binding{
		of("empty", func(d richtext.Document) any { return d.Empty() }),
		of("words", func(d richtext.Document) any { return float64(d.Words) }),
		of("characters", func(d richtext.Document) any { return float64(d.Characters) }),
		{name: "unsaved", in: &interpolation{parts: []part{{e: &expr{source: "request.method != 'GET'",
			ir: []any{"call", []any{"#", unsaved}, []any{[]any{"$", requestMethodName}}}}}}}},
		fixed("open", false, "false"),
		fixed("address", "", "''"),
		fixed("format", s.format, quoteString(s.format)),
		// none left out yet: an empty list made for each read, so no request shares one
		{name: "dropped", in: &interpolation{parts: []part{{e: &expr{source: "[]", ir: []any{"[]", []any{}}}}}}},
	}
}

// requestMethodName is the name the page's scope holds the request's method under while it renders
// (Page.render): no name markup can write, so only an editor's unsaved reads it.
const requestMethodName = "kitwork request method"

// editorContent compiles the writing area, the <content> of the editor around: the area, then the field
// the form posts, each holding the value cleaned.
func (c *compiler) editorContent(n *Node, cx ctx) (op, error) {
	s := cx.editor
	if editorKeyOf(n) != editorKeyOf(s.area) {
		return nil, errAt(n, "editor có một vùng viết: <content> ở %s:%d:%d đã là của nó; vùng viết thứ hai là một <editor> khác", s.area.File, s.area.Line, s.area.Column)
	}
	if err := c.checkReads(n, cx, nil); err != nil {
		return nil, err
	}
	el := &element{tag: "richtext", html: "div"} // it looks as a richtext does, so writing looks like reading
	p, err := c.attributes(n, el)
	if err != nil {
		return nil, err
	}
	frame := c.paintOf(n, el, p, cx, nil)
	frame.classes = []string{stylesheet.RichtextClass}
	if err := c.richtextScope(n, frame); err != nil { // the text scope, its words and its links measured here
		return nil, err
	}
	classes := []string{stylesheet.RichtextClass, "editor-area"}
	classes = append(classes, p.classes...)
	if s.rows > 0 {
		classes = append(classes, stylesheet.EditorRowsClass(s.rows))
	}
	area := &elemOp{tag: "div", staticClass: dedupe(classes), dynClasses: p.dynClasses}
	var w strings.Builder
	anchor, err := c.anchorAttribute(n, p, cx)
	if err != nil {
		return nil, err
	}
	if anchor == "" {
		writeAttr(&w, "id", s.areaID)
	}
	w.WriteString(` role="textbox" aria-multiline="true"`)
	if cx.field != nil && cx.field.label != "" {
		writeAttr(&w, "aria-labelledby", cx.field.label)
	} else if err := c.attrValue(n, s.alt, "aria-label", false, area, &w); err != nil {
		return nil, err
	}
	var described []string
	if cx.field != nil {
		for _, id := range []string{cx.field.hint, cx.field.error} {
			if id != "" {
				described = append(described, id)
			}
		}
	}
	if len(described) > 0 {
		writeAttr(&w, "aria-describedby", strings.Join(described, " "))
	}
	if s.placeholder != nil {
		if err := c.attrValue(n, s.placeholder, "aria-placeholder", false, area, &w); err != nil {
			return nil, err
		}
	}
	if s.required {
		w.WriteString(` aria-required="true"`)
	}
	w.WriteString(` aria-readonly="true"`) // until the component starts, and for good when readonly
	if s.disabled {
		w.WriteString(` aria-disabled="true"`)
	}
	w.WriteString(` translate="no" spellcheck="true"`)
	writeAttr(&w, "data-kitwork-name", s.name)
	area.staticAttrs = anchor + w.String()
	area.children = []op{&editorValueOp{value: s.value}}
	runtime, err := c.runtimeAttributes(n, p, area, cx.scope)
	if err != nil {
		return nil, err
	}
	if err := c.withText(n, area, cx.scope, runtime, cx.aliases); err != nil {
		return nil, err
	}

	// the field the form posts: a real control, so required holds and, without JavaScript, the one shown;
	// the component makes it tabindex="-1" aria-hidden="true" as it turns the area on
	field := &elemOp{tag: "textarea", staticClass: dedupe(append([]string{"editor-field"}, p.classes...)), dynClasses: p.dynClasses}
	var fw strings.Builder
	if cx.field != nil {
		writeAttr(&fw, "id", cx.field.control)
	}
	writeAttr(&fw, "name", s.name)
	for _, word := range []struct {
		on   bool
		name string
	}{{s.required, "required"}, {s.readonly, "readonly"}, {s.disabled, "disabled"}} {
		if word.on {
			fw.WriteString(" " + word.name)
		}
	}
	if s.rows > 0 {
		writeAttr(&fw, "rows", strconv.Itoa(s.rows))
	}
	if s.placeholder != nil {
		if err := c.attrValue(n, s.placeholder, "placeholder", false, field, &fw); err != nil {
			return nil, err
		}
	}
	if s.alt != nil {
		if err := c.attrValue(n, s.alt, "aria-label", false, field, &fw); err != nil {
			return nil, err
		}
	}
	if len(described) > 0 {
		// without JavaScript the field is the control shown: the field's hint and error describe it too
		writeAttr(&fw, "aria-describedby", strings.Join(described, " "))
	}
	field.staticAttrs = fw.String()
	field.children = []op{&editorValueOp{value: s.value, escaped: true}}
	return &elemOp{fragment: true, children: []op{area, field}}, nil
}

// editorValueOp writes the editor's value cleaned (richtext.Clean): as it is in the area, escaped in the
// field. None is no content.
type editorValueOp struct {
	value   *expr
	escaped bool
}

func (o *editorValueOp) render(r *run, scope map[string]any) {
	if o.value == nil {
		return
	}
	markup := editorCleaned(stringify(o.value.eval(scope))).HTML
	if o.escaped {
		markup = html.EscapeString(markup)
	}
	r.b.WriteString(markup)
}

// cleanCache holds the documents the editors' values clean to, by value, for every page: one request
// cleans its editor's value once though five things read it (its area, its field, three counts), and a
// post opened again is not cleaned again. Bounded as displayCache is, by the bytes it holds. Safe for
// concurrent use.
type cleanCache struct {
	mu      sync.Mutex
	entries map[string]richtext.Document
	size    int
	limit   int
}

var cleaned = &cleanCache{limit: 8 << 20}

// editorCleaned is source cleaned (richtext.Clean), from the cache when it is there.
func editorCleaned(source string) richtext.Document {
	return cleaned.document(source)
}

func (cache *cleanCache) document(source string) richtext.Document {
	cache.mu.Lock()
	d, found := cache.entries[source]
	cache.mu.Unlock()
	if found {
		return d
	}
	d = richtext.Clean(source)
	size := len(source) + len(d.HTML) + len(d.Text)
	if size > cache.limit/8 {
		return d
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.size+size > cache.limit {
		cache.entries, cache.size = nil, 0
	}
	if cache.entries == nil {
		cache.entries = map[string]richtext.Document{}
	}
	if _, kept := cache.entries[source]; !kept {
		cache.entries[source] = d
		cache.size += size
	}
	return d
}

// holdsEditor reports whether a field holds an editor, however deep (not inside another field), a
// layout it calls included: its <label> then names the writing area, and its <error> describes it.
func (c *compiler) holdsEditor(n *Node) bool { return c.fieldHolds(n, "editor") }

// fieldHolds reports whether a field holds an element of tag (fieldFind).
func (c *compiler) fieldHolds(n *Node, tag string) bool {
	found, _ := c.fieldFind(n, tag, nil, map[string]bool{})
	return found != nil
}

// fieldFind is the first element of tag a field holds, however deep: not inside another field, nor
// inside a popover (an editor's address input and its error are the editor's, not the field's), and
// seen through the layouts it calls (<field><label>…</label><post-editor /></field>, the editor in
// post-editor.ui). around is the element that writes if or for nearest around it on the way there
// (it, or one between), nil for none; seen the layouts already looked through.
func (c *compiler) fieldFind(n *Node, tag string, around *Node, seen map[string]bool) (found, at *Node) {
	for _, ch := range n.Elements() {
		near := around
		if attrNamed(ch, "if").Name == "if" || attrNamed(ch, "for").Name == "for" {
			near = ch
		}
		switch ch.Tag {
		case tag:
			return ch, near
		case "field", "popover":
			continue
		}
		if found, at := c.fieldFind(ch, tag, near, seen); found != nil {
			return found, at
		}
		if root := c.layoutRoot(ch, seen); root != nil {
			if root.Tag == tag {
				return root, near
			}
			if found, at := c.fieldFind(root, tag, near, seen); found != nil {
				return found, at
			}
		}
	}
	return nil, nil
}

// layoutRoot is the root element of the layout ch calls, nil when it calls none: an element of
// Kitwork, a component (whose names are its own), a layout already looked through.
func (c *compiler) layoutRoot(ch *Node, seen map[string]bool) *Node {
	if !siteName(ch.Tag) || c.callsComponent(ch) {
		return nil
	}
	var def *componentDef
	if a := attrNamed(ch, "source"); a.Name == "source" {
		def, _ = c.layoutSource(ch, a)
	} else {
		def, _ = c.layoutNamed(ch, ch.Tag)
	}
	if def == nil || !def.layout() || def.root == nil || seen[def.ui] {
		return nil
	}
	seen[def.ui] = true
	return def.root
}
