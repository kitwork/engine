package ui

import (
	"fmt"
	"strings"
	"sync"

	"github.com/kitwork/engine/ui/stylesheet"
	"github.com/kitwork/engine/utilities/richtext"
)

// <richtext value="{{ post.body }}" /> (decision of 20/10/2026) shows a post the site saved — an
// editor's value, a paste from Google Docs or Word, plain text from a form — as it was written. The
// value is cleaned on the server into one closed grammar (utilities/richtext, v2 of 20/10/2026: p and
// h2–h6 with an alignment class or none, blockquote, ul, ol, li, pre holding one code, hr, strong, em,
// u, s, code, a href, br, rebuilt from what was read, never filtered in place) and written
// inside <div class="richtext"> as the request reads it, for the page's own origin: a link to another
// host carries rel="external noopener" (richtext.Display; a value cleaned once is kept, displayCache).
// No JavaScript runs it. It is the only way .ui writes HTML that came as data: there is no option to
// skip the cleaning, and no {{{ }}}, raw(), html=, <html>, <raw>, <prose> or <markdown> (each refused
// with a hint that names richtext).
//
//   - value is one {{ }} that reads the page's data. The server builds the richtext, so a value read
//     from what lives in the browser ($alias, the names of a component the kernel runs, a toast's
//     notice) is an error, and so is a value written in the markup: words written for the page are
//     .ui's own tags (<text>, <list>, <quote>).
//   - It is a block: never inside a line (<text>, <link>, <button>, <label>…), and nothing is written
//     inside it.
//   - Its look is the text scope of the page's stylesheet (stylesheet/richtext.go), in page.<hash>.css
//     only on a page that holds one, with the code face. Its words are measured where it stands like
//     any text, whatever the value will hold, its links in the engine's link colour, and code on its
//     fills (ink/8 in a line, the panel for a block), in each mode (contrast.go), told at the richtext.

// richtextCall is how .ui shows a saved post: what every refusal of another way to write HTML names.
const richtextCall = `<richtext value="{{ post.body }}" />`

// htmlFromData is why every other way to write HTML from data is refused.
const htmlFromData = ".ui không ghi HTML từ dữ liệu; bài đã lưu hiện bằng " + richtextCall + ", làm sạch ở server"

// refusedTags are the tags other systems show a saved post with, each told what .ui writes.
var refusedTags = map[string]string{
	"html":     "không có thẻ <html> (trang đã là <view>): HTML đã soạn, một bài viết hay một mô tả, hiện bằng " + richtextCall + ", làm sạch ở server",
	"raw":      "không có thẻ <raw>: .ui không ghi HTML chưa làm sạch; bài đã lưu hiện bằng " + richtextCall,
	"prose":    "không có thẻ <prose>: khối chữ của một bài đã lưu là " + richtextCall + ", khoảng cách, danh sách, link và trích dẫn là của engine",
	"markdown": "không có thẻ <markdown>: bài đã lưu hiện bằng " + richtextCall + " (richtext đọc HTML hoặc chữ thường, không đọc markdown)",
}

// htmlAttributes are the attribute names other systems write HTML from data with (html=, v-html,
// x-html, innerHTML, dangerouslySetInnerHTML), in lower case: refused on every tag (webWord).
var htmlAttributes = map[string]bool{
	"html": true, "inner-html": true, "innerhtml": true, "v-html": true, "x-html": true, "dangerouslysetinnerhtml": true,
}

// lineElements are the HTML elements whose content is a line: what <text> becomes (p, span, s, a
// heading), <link> (a), <button>, <label>, <hint> and <error> (p), <badge> (span), <time>, <option>, a
// <quote> inside a line (q) and an accordion's summary.
var lineElements = map[string]bool{
	"p": true, "span": true, "s": true, "a": true, "button": true, "label": true, "time": true, "option": true,
	"q": true, "summary": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
}

// richtextPlace refuses a richtext with something written inside it, and one inside a line however
// deep: a block (<div>) inside <p>, <a>, <button> or <label> is no HTML a browser keeps where it is
// written (a <p> ends before it, the links of the post split an <a> around it), with a box, a card, a
// list or a layout piece between them or not. The line named is the outermost around it, read along
// the elements around (cx.paint, which a layout's or a component's root takes from its call).
func (c *compiler) richtextPlace(n *Node, cx ctx) error {
	if len(n.SignificantChildren()) > 0 {
		return errAt(n, "richtext không có nội dung bên trong: bài đã lưu đến từ value")
	}
	var line *paint
	for f := cx.paint; f != nil; f = f.parent {
		if lineElements[f.html] {
			line = f
		}
	}
	if line == nil && !cx.inline {
		return nil
	}
	message := "richtext là một khối: không đặt trong <text>, <link>, <button>, <label>"
	around := line
	if around == nil {
		around = cx.paint // a breadcrumb's item, whose text is a line
	}
	if around != nil && around.node != nil {
		message += fmt.Sprintf(" (nó đang nằm trong <%s> ở %s:%d:%d)", c.paintTag(around), around.node.File, around.node.Line, around.node.Column)
	}
	return errAt(n, "%s", message)
}

// richtextValue reads the value of a richtext (a, nil when none is written): one {{ }} that reads the
// page's data, kept on o for the render.
func (c *compiler) richtextValue(n *Node, a *Attr, p *attrPlan, o *elemOp, cx ctx) error {
	if a == nil {
		for _, name := range p.order {
			if htmlAttributes[strings.ToLower(name)] { // <richtext html="…">: the word of another system
				return errAttr(n, p.semantic[name], "%s không có: %s", name, htmlFromData)
			}
		}
		return errAt(n, "richtext cần value: %s", richtextCall)
	}
	in, err := parseInterpolation(a.Value, n, a)
	if err != nil {
		return err
	}
	written := "richtext hiện bài đã lưu, value là {{ }}; chữ viết sẵn thì viết bằng thẻ .ui (<text>, <list>, <quote>)"
	e := in.single()
	if e == nil {
		return errAttr(n, a, "%s", written) // words, or words around a {{ }}
	}
	if _, every := knownLiterals(e.ir); every {
		return errAttr(n, a, "%s", written) // {{ '<p>…</p>' }}, {{ a ? '…' : '…' }}: words of the markup still
	}
	for _, name := range usedNames(e.ir) {
		if live := cx.scope.declaring(name); strings.HasPrefix(name, "$") || live != nil && live.client {
			return errAttr(n, a, "richtext dựng ở server: value đọc dữ liệu của trang, không đọc %s (trạng thái ở trình duyệt)", name)
		}
	}
	o.richtext = e
	return nil
}

// richtextScope records the text scope in the page's stylesheet and measures what the richtext shows
// where it stands (f), whatever its value will hold: its words in the colour around it, its links in
// the engine's link colour, then, once those read, code on its fills: code in a line, the words around
// it on ink/8 over the ground around, and a code block, the words around it on the panel (code inside
// a link has no fill: it reads as the link, measured already). A pair already short at the words or the
// links is told once, there: the author fixes those first. The page ships the code face, which code in
// the value takes.
func (c *compiler) richtextScope(n *Node, f *paint) error {
	if err := c.table.Richtext(c.rules); err != nil {
		return errAt(n, "%s", err.Error())
	}
	c.uses["code"] = true
	told := len(c.out.warnings) + len(c.contrast)
	c.measure(f, false)
	c.measure(&paint{parent: f, node: n, tag: n.Tag, html: "a", part: richtextLink}, false)
	if len(c.out.warnings)+len(c.contrast) == told {
		c.measure(&paint{parent: f, node: n, tag: n.Tag, html: "code", part: richtextCode}, false)
		c.measure(&paint{parent: f, node: n, tag: n.Tag, html: "pre", part: richtextCodeBlock}, false)
	}
	return nil
}

// richtextOp writes the value of a richtext, cleaned, for the page's origin (Request.Origin): a link to
// another host carries rel="external noopener". A value that is no text is written as the page writes
// any value (stringify); none is no content.
type richtextOp struct{ value *expr }

func (o *richtextOp) render(r *run, scope map[string]any) {
	r.b.WriteString(displayed.html(stringify(o.value.eval(scope)), r.origin))
}

// displayCache holds the HTML of the values richtexts wrote, by value and origin, for every page: a post
// read on every request is cleaned once (a post of 48 KB takes about 5 ms to clean). Bounded as the
// cache of coloured code is (codeCache): by the bytes it holds, an entry larger than an eighth of the
// bound never kept (cleaned again each time, in linear time), an entry that would pass the bound
// emptying it first. Safe for concurrent use.
type displayCache struct {
	mu      sync.Mutex
	entries map[displayKey]string
	size    int
	limit   int
}

type displayKey struct{ source, origin string }

// displayed is the cache of every page's richtexts.
var displayed = &displayCache{limit: 8 << 20}

// html is source cleaned for a page of origin (richtext.Display), from the cache when it is there.
func (cache *displayCache) html(source, origin string) string {
	key := displayKey{source: source, origin: origin}
	cache.mu.Lock()
	markup, found := cache.entries[key]
	cache.mu.Unlock()
	if found {
		return markup
	}
	markup = richtext.Display(source, origin)
	size := len(source) + len(origin) + len(markup)
	if size > cache.limit/8 {
		return markup
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.size+size > cache.limit {
		cache.entries, cache.size = nil, 0
	}
	if cache.entries == nil {
		cache.entries = map[displayKey]string{}
	}
	if _, kept := cache.entries[key]; !kept {
		cache.entries[key] = markup
		cache.size += size
	}
	return markup
}

// elementPart is a part of a Kitwork element the engine writes and gives its look, never the author (a
// link inside a richtext, code and its fills), as the contrast check reads it: its look, what it is
// called, and what a pair of it short of contrast is told to do.
type elementPart struct {
	look stylesheet.Look
	name string
	// tell is the advice: what changes the pair, given the text's and the top ground's colour values
	// and the tag, since no attribute written on the part does
	tell func(p *elementPart, text, ground, tag string) string
}

// richtextLink is a link inside a richtext: its words in the engine colour kitwork-link.
var richtextLink = &elementPart{look: stylesheet.RichtextLinkLook(), name: "link", tell: linkAdvice}

// richtextCode is code in a line of a richtext: the words around it on ink/8, a chip of their own ink.
var richtextCode = &elementPart{look: stylesheet.RichtextCodeLook(), name: "mã", tell: func(_ *elementPart, _, _, tag string) string {
	return " Mã trong dòng của <" + tag + "> nằm trên nền " + stylesheet.RichtextCodeFill + " engine tô dưới nó: đổi màu chữ (color trên <" + tag +
		">) hoặc đặt <" + tag + "> trên nền khác (background)."
}}

// richtextCodeBlock is a code block of a richtext: the words around it on the panel.
var richtextCodeBlock = &elementPart{look: stylesheet.RichtextCodeBlockLook(), name: "khối mã", tell: func(_ *elementPart, _, _, tag string) string {
	return " Khối mã trong <" + tag + "> nằm trên nền " + stylesheet.RichtextCodeBlockFill + " engine tô: đổi màu chữ (color trên <" + tag +
		">) hoặc khai lại " + stylesheet.RichtextCodeBlockFill + " trong router.colors bằng một cặp { light: \"#…\", dark: \"#…\" } đủ tương phản."
}}

// what names the part's text in a message, its colour as written in the mode measured: chữ link
// (brand+35) trong <richtext>. (A part whose words are the colour around it, code, is named by that.)
func (p *elementPart) what(value, tag string) string {
	return "chữ " + p.name + " (" + value + ") trong <" + tag + ">"
}

// advice is what a pair of the part short of contrast is told to do (tell).
func (p *elementPart) advice(text, ground, tag string) string { return p.tell(p, text, ground, tag) }

// colourBase is the token of a colour value: brand of brand+35.
func colourBase(v string) string {
	if cv, ok := stylesheet.SplitColour(v); ok {
		return cv.Base
	}
	return v
}

// linkAdvice is a link's: its colour is the engine's, read from the palette's roles, so what changes it
// is router.colors, or the ground under the element.
func linkAdvice(p *elementPart, text, ground, tag string) string {
	rung, under := colourBase(text), colourBase(ground)
	if rung == under {
		return " Chữ " + p.name + " engine tô theo router.colors từ " + rung + ", mà nền cũng là " + under + ": đặt <" + tag + "> trên nền khác (background)."
	}
	return " Chữ " + p.name + " engine tô theo router.colors: khai lại " + rung + " trong router.colors bằng một cặp { light: \"#…\", dark: \"#…\" } đủ tương phản trên nền " +
		under + ", hoặc đặt <" + tag + "> trên nền khác (background)."
}
