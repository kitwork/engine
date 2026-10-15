package ui

import (
	"html"
	"strconv"
	"strings"
	"sync"

	"github.com/kitwork/engine/jit/highlight"
	"github.com/kitwork/engine/ui/stylesheet"
)

// <code language="…"> is coloured on the server, with no JavaScript in the browser (decision of
// 12/10/2026). The text is read by jit/highlight into tokens; each token that is not plain text becomes
// <span class="code-<kind>">, plain text stays bare, every text is escaped, so the text content of the
// element is the source byte for byte and code never becomes markup. A block is
// <pre><code class="language-go">, code in a line <code class="language-go">.
//
//   - Static code is coloured once, when the page compiles; code from {{ }} when the page renders, its
//     HTML kept by language and text (highlighted), so a page rendered again tokenizes nothing.
//   - language= names one of highlight.Languages in full. A name it does not have is a compile error
//     that says what to write (js → javascript); "text" is plain. From {{ }}, each name the expression
//     spells out is checked when the page compiles, and a value from data when the page renders: one it
//     does not have shows the code plain and tells the author while developing, once a page.
//   - The colour of a kind is a role of the palette (stylesheet.CodeLook), so code follows router.colors
//     and the dark look, unless the site's router.highlight sets it (stylesheet/highlight.go): then the
//     author's colour, the class unchanged. The rule of a kind is in page.<hash>.css only when the page
//     can show it: the kinds static code holds, every kind for code from {{ }}. Each colour is measured
//     on what lies behind the code like any text (contrast.go): a warning by default, an error under
//     "strict". An author's colour on the page's canvas, paper or panel (or an accent kind's own fill)
//     is told once, at the call in router.work (tellHighlight); elsewhere at language=, as the author's.
//
// For Kitwork's own components that show code (the terminal, each of whose <code> is a command), the
// pieces <code> is made of:
//   - code (above) writes a command as any code, coloured in its own language=;
//   - codeKinds(language, f, []highlight.Kind{highlight.Prompt}) records the prompt's rule (the text
//     around it at 70%, never selected: stylesheet.CodeLook) and measures it where it stands; the
//     prompt itself is <span class="code-prompt">$ </span> (stylesheet.CodeClass), which terminal.go
//     writes before the command, outside its <code>;
//   - highlightHTML(language, source) is a text coloured, with the kinds it holds, outside any page.

// codeLanguage is the language= of a code element: a name written, checked when the page compiles, or a
// value from {{ }} (in), whose names spelled out are checked then and whose value from data is checked
// when the page renders. node and attr are where it is written: where a problem with it is told.
type codeLanguage struct {
	name string
	in   *interpolation
	node *Node
	attr *Attr
}

// place is file:line:col of the attribute that names the language, or of the element without one.
func (l codeLanguage) place() string {
	line, column := l.node.Line, l.node.Column
	if l.attr != nil && l.attr.Line > 0 {
		line, column = l.attr.Line, l.attr.Column
	}
	return l.node.File + ":" + strconv.Itoa(line) + ":" + strconv.Itoa(column)
}

// written is the attribute as the source writes it: language="{{ snippet.language }}".
func (l codeLanguage) written() string {
	if l.attr == nil || l.attr.Name == "" {
		return "language=\"" + l.name + "\""
	}
	return l.attr.Name + "=\"" + l.attr.Value + "\""
}

// languageClass is the class that names a code element's language, HTML's own way to say it
// (language-go). It has no rule: nothing about the element's look depends on it.
func languageClass(name string) string { return "language-" + name }

// languageProblem is the error of a language= value that is not one of highlight's languages, naming
// what to write instead (`ngôn ngữ "js" chưa có; viết javascript`); "" for a language it has.
func languageProblem(value string) string {
	if highlight.Has(value) {
		return ""
	}
	return (&highlight.UnknownLanguageError{Name: value, Suggestions: highlight.Suggest(value)}).Error()
}

// codeLanguageOf reads the language= of a code element.
func (c *compiler) codeLanguageOf(n *Node, a *Attr) (codeLanguage, error) {
	language := codeLanguage{node: n, attr: a}
	value := strings.TrimSpace(a.Value)
	if !strings.Contains(value, "{{") {
		if problem := languageProblem(value); problem != "" {
			return language, errAttr(n, a, "%s", problem)
		}
		language.name = value
		return language, nil
	}
	in, err := parseInterpolation(a.Value, n, a)
	if err != nil {
		return language, err
	}
	// every name the expression spells out ({{ shell ? 'bash' : 'powershell' }}) is checked now, even
	// beside a value from data, which waits for the render
	values, _ := interpolationLiterals(in)
	for _, v := range values {
		if v = strings.TrimSpace(v); v == "" {
			continue
		}
		if problem := languageProblem(v); problem != "" {
			return language, errAttr(n, a, "%s", problem)
		}
	}
	language.in = in
	return language, nil
}

// code: <code> inside a line, <pre><code> as a block (keeps line breaks and spaces). With language= its
// text is coloured (above).
func (c *compiler) code(n *Node, cx ctx, site *callSite) (op, error) {
	if len(n.Elements()) > 0 {
		return nil, errAt(n, "code chỉ chứa chữ, không có thẻ con")
	}
	el := &element{tag: "code", html: "code"}
	p, err := c.attributes(n, el)
	if err != nil {
		return nil, err
	}
	var language *codeLanguage
	for _, name := range p.order {
		a := p.semantic[name]
		if name == "language" {
			read, err := c.codeLanguageOf(n, a)
			if err != nil {
				return nil, err
			}
			language = &read
			continue
		}
		if done, err := c.notHere(n, a, "<code>", "code chỉ chứa chữ"); err != nil {
			return nil, err
		} else if !done {
			return nil, errAttr(n, a, "thuộc tính %s không có trên <code>", name)
		}
	}
	anchor, err := c.offAnchor(n, p, cx, "<code>")
	if err != nil {
		return nil, err
	}
	raw := ""
	for _, ch := range n.Children {
		raw += ch.Text
	}
	in, err := parseInterpolation(raw, n, nil)
	if err != nil {
		return nil, err
	}
	if !cx.inline {
		in, _ = parseInterpolation(blockText(raw), n, nil)
	}
	// the code face of router.fonts, and a block that scrolls sideways, are reboot.css's (code, pre)
	c.uses["code"] = true
	f := c.paintOf(n, el, p, cx, site)
	if strings.TrimSpace(raw) != "" {
		c.measure(f, false)
	}
	classes := append(append([]string{}, p.classes...), p.show...)
	if site != nil {
		classes = append(classes, site.classes...)
	}
	inner := &elemOp{tag: "code", children: []op{&textOp{in: in}}}
	if language != nil {
		content, err := c.codeContent(in, *language, f)
		if err != nil {
			return nil, err
		}
		inner.children = []op{content}
		if language.in == nil {
			inner.staticClass = []string{languageClass(language.name)}
		} else {
			inner.dynClasses = []dynClass{{name: "language", in: language.in, language: true,
				where: language.place(), written: language.written()}}
		}
	}
	outer := inner
	if !cx.inline {
		outer = &elemOp{tag: "pre", children: []op{fold(inner)}}
	}
	outer.staticClass = dedupe(append(outer.staticClass, classes...))
	outer.staticAttrs = anchor
	outer.dynClasses = append(outer.dynClasses, p.dynClasses...)
	outer.forEach, outer.ifExpr, outer.showExpr = p.forEach, p.ifExpr, p.showExpr
	if site != nil {
		mergeCallSite(outer, site)
	}
	return outer, nil
}

// blockText is the text of a code block as it shows: its lines as written, without the blank lines
// around them (the line break after <code>, the indent before </code>) and without the indent the source
// gives every one of them.
func blockText(raw string) string {
	lines := strings.Split(raw, "\n")
	for len(lines) > 1 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 1 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return dedent(strings.Join(lines, "\n"))
}

func dedent(s string) string {
	lines := strings.Split(s, "\n")
	indent := -1
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		n := len(l) - len(strings.TrimLeft(l, " \t"))
		if indent < 0 || n < indent {
			indent = n
		}
	}
	if indent <= 0 {
		return s
	}
	for i, l := range lines {
		if len(l) >= indent {
			lines[i] = l[indent:]
		}
	}
	return strings.Join(lines, "\n")
}

// codeContent is the op that writes the text of a code element (in) coloured in its language: static
// text coloured now, text or a language from {{ }} coloured at render (codeOp). It records the rule of
// every kind the op can write and measures each on what lies behind the code (f, the code element as
// the contrast check reads it): the kinds static text holds, every kind a tokenizer writes for text from
// {{ }}, which no one knows before it comes.
func (c *compiler) codeContent(in *interpolation, language codeLanguage, f *paint) (op, error) {
	if language.in == nil && in.static() {
		markup, kinds, err := highlightHTML(language.name, in.literal())
		if err != nil {
			// a source over highlight.MaximumSourceSize, or a tokenizer's failure: shown plain, as written
			c.warn(language.node.File, language.node.Line, language.node.Column, "<code "+language.written()+">: "+err.Error())
		}
		if err := c.codeKinds(language, f, kinds); err != nil {
			return nil, err
		}
		return &staticOp{s: markup}, nil
	}
	if err := c.codeKinds(language, f, writtenKinds()); err != nil {
		return nil, err
	}
	return &codeOp{text: in, language: language}, nil
}

// writtenKinds are the kinds a tokenizer can write a span for: every kind but plain (bare text) and the
// prompt (the terminal's own).
func writtenKinds() []highlight.Kind {
	var out []highlight.Kind
	for _, kind := range highlight.Kinds() {
		if kind != highlight.Plain && kind != highlight.Prompt {
			out = append(out, kind)
		}
	}
	return out
}

// codeKinds records the rule of each kind in the page's stylesheet and measures its colour on what lies
// behind the code (f) like any text, the kinds that look alike (stylesheet.CodeLook: colour, fill,
// weight and the call of router.highlight that set them) as one. A pair short of contrast is told at the
// attribute that names the language (place). A kind router.highlight colours names its call for the
// line the page tells there (tellHighlight). f nil measures nothing.
func (c *compiler) codeKinds(language codeLanguage, f *paint, kinds []highlight.Kind) error {
	var order []string
	alike := map[string]*codeToken{}
	for _, kind := range kinds {
		name := kind.String()
		if _, err := c.table.Code(c.rules, name); err != nil {
			return &Error{File: language.node.File, Line: language.node.Line, Column: language.node.Column, Message: err.Error()}
		}
		look, _ := c.table.TokenLook(name)
		key := look.Color + "|" + look.Background + "|" + strconv.Itoa(look.Weight) + "|" + strconv.Itoa(look.Share)
		if look.Source != nil {
			key += "|" + look.Source.String()
			if c.highlightShown == nil {
				c.highlightShown = map[stylesheet.Source]bool{}
			}
			c.highlightShown[*look.Source] = true
		}
		if alike[key] == nil {
			alike[key] = &codeToken{look: look, language: language}
			order = append(order, key)
		}
		alike[key].kinds = append(alike[key].kinds, name)
	}
	if f == nil {
		return nil
	}
	for _, key := range order {
		c.measure(&paint{parent: f, node: f.node, tag: f.tag, html: "span", token: alike[key]}, false)
	}
	return nil
}

// codeToken is one or more kinds of token of a code element that look alike, as the contrast check
// reads them: a span inside the code whose colour, fill and weight are the engine's, chosen through the
// palette's roles (stylesheet.CodeLook), or a share of the text around it (the terminal's prompt), or
// whose colour the site's router.highlight sets (look.Source).
type codeToken struct {
	kinds    []string
	look     stylesheet.CodeLook
	language codeLanguage
}

// what names the token's text in a message, its colour as written in the mode measured: chữ keyword,
// type (brand+35) trong khối mã in light, (brand) in dark on Kit Design; chữ prompt (paper 70%) trong
// khối mã; a pair of router.highlight as its side of the mode, chữ keyword (#7c3aed) trong khối mã.
func (t *codeToken) what(value string) string {
	return "chữ " + strings.Join(t.kinds, ", ") + " (" + value + ") trong khối mã"
}

// advice is what a pair of the token short of contrast is told to do: a colour router.highlight sets is
// changed there, at its call, or the code put on another ground; the engine's colours are read from the
// palette's roles, so what changes them is router.colors, or the ground under the code. A share of the
// text around it (the prompt) follows what the author writes: a colour for that text, or another ground.
// Text and ground that are rungs of one token are never told to declare it anew: a new value would move
// both.
func (t *codeToken) advice(text, ground string) string {
	if t.look.Source != nil {
		return " Màu này là của router.highlight (" + t.look.Source.String() + "): đổi " + strings.Join(t.kinds, ", ") +
			" ở đó (một cặp { light, dark } khi mỗi chế độ cần một màu), hoặc đặt khối mã trên nền khác (background)."
	}
	base := func(v string) string {
		if cv, ok := stylesheet.SplitColour(v); ok {
			return cv.Base
		}
		return v
	}
	rung, under := base(text), base(ground)
	pair := " trong router.colors bằng một cặp { light: \"#…\", dark: \"#…\" } đủ tương phản"
	if t.look.Share > 0 {
		return " Dấu nhắc là màu chữ quanh lệnh ở " + strconv.Itoa(t.look.Share) + "%: viết color trên thẻ chủ (hay phần tử bao lệnh) một màu tương phản mạnh hơn với nền " + under + ", hoặc đổi background."
	}
	if t.look.Background != "" && ground == t.look.Background {
		return " Chữ và nền này engine tô theo router.colors: khai lại " + rung + " hoặc " + under + pair + "."
	}
	if rung == under {
		return " Chữ này engine tô theo router.colors từ " + rung + ", mà nền cũng là " + under + ": đặt khối mã trên nền khác (background)."
	}
	return " Chữ này engine tô theo router.colors: khai lại " + rung + pair + " trên nền " + under +
		", hoặc đặt khối mã trên nền khác (background)."
}

// highlightHTML writes source in a language as what goes inside a <code>: each token that is not plain
// text in <span class="code-<kind>">, plain text bare, every text escaped (a token is source, never
// markup: </code><script> stays text), so the text content of the result is source byte for byte. kinds
// are the kinds it wrote a span for, in the order of highlight.Kinds. err is the tokenizer's — an
// unknown language, a source over highlight.MaximumSourceSize, a failure of its own —, with the source
// written plain.
func highlightHTML(language, source string) (string, []highlight.Kind, error) {
	tokens, err := highlight.Tokenize(language, source)
	var b strings.Builder
	b.Grow(len(source) + len(source)/2)
	used := map[highlight.Kind]bool{}
	for _, token := range tokens {
		if token.Kind == highlight.Plain {
			b.WriteString(html.EscapeString(token.Text))
			continue
		}
		used[token.Kind] = true
		b.WriteString(`<span class="`)
		b.WriteString(stylesheet.CodeClass(token.Kind.String()))
		b.WriteString(`">`)
		b.WriteString(html.EscapeString(token.Text))
		b.WriteString(`</span>`)
	}
	var kinds []highlight.Kind
	for _, kind := range highlight.Kinds() {
		if used[kind] {
			kinds = append(kinds, kind)
		}
	}
	return b.String(), kinds, err
}

// codeOp writes the text of a code element whose text or language comes from {{ }}, coloured at render.
type codeOp struct {
	text     *interpolation
	language codeLanguage
}

func (o *codeOp) render(r *run, scope map[string]any) {
	source := o.text.render(scope)
	name := o.language.name
	if o.language.in != nil {
		name = strings.TrimSpace(stringify(o.language.in.value(scope)))
		if !highlight.Has(name) {
			// a language from data the engine does not have: the code shows plain, and while developing
			// the author hears of it, once a page; no language at all ("") is plain code, nothing to tell
			r.b.WriteString(html.EscapeString(source))
			if name != "" {
				r.warnOnce(o.language.place() + ": " + o.language.written() + ": " + languageProblem(name) + "; khối mã hiện thành chữ thường")
			}
			return
		}
	}
	markup, err := highlighted.html(name, source)
	r.b.WriteString(markup)
	if err != nil {
		// the text is what is wrong (too long), so it is told at the code element, as when it is written
		node := o.language.node
		r.warnOnce(node.File + ":" + strconv.Itoa(node.Line) + ":" + strconv.Itoa(node.Column) + ": <code " + o.language.written() + ">: " + err.Error())
	}
}

// warnOnce tells the author a message of the page's render while developing (Request.Warn), once a
// page.
func (r *run) warnOnce(message string) {
	if r.warn != nil && r.page != nil && r.page.warned.first(message) {
		r.warn(message)
	}
}

// codeCache holds the HTML of code coloured at render, by language and text, for every page: the same
// code in the same language is the same HTML on every page and every site (a kind's class never
// depends on the palette), so it is coloured once. Bounded by the bytes it holds: an entry larger than
// an eighth of the bound is never kept (coloured again each time, in linear time), and an entry that
// would pass the bound empties it first. Safe for concurrent use.
type codeCache struct {
	mu      sync.Mutex
	entries map[codeKey]string
	size    int
	limit   int
}

type codeKey struct{ language, source string }

// highlighted is the cache of every page's code from {{ }}.
var highlighted = &codeCache{limit: 8 << 20}

// html is source coloured in a language (highlightHTML), from the cache when it is there. A result with
// an error is never kept: it is plain text, and its error is told again.
func (cache *codeCache) html(language, source string) (string, error) {
	key := codeKey{language: language, source: source}
	cache.mu.Lock()
	markup, found := cache.entries[key]
	cache.mu.Unlock()
	if found {
		return markup, nil
	}
	markup, _, err := highlightHTML(language, source)
	if err != nil {
		return markup, err
	}
	size := len(language) + len(source) + len(markup)
	if size > cache.limit/8 {
		return markup, nil
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.size+size > cache.limit {
		cache.entries, cache.size = nil, 0
	}
	if cache.entries == nil {
		cache.entries = map[codeKey]string{}
	}
	if _, kept := cache.entries[key]; !kept {
		cache.entries[key] = markup
		cache.size += size
	}
	return markup, nil
}

// tellHighlight tells, at each call of router.highlight whose colours the page can show (codeKinds), the
// line of that call when a colour it sets falls short on the page's canvas, paper or panel
// (stylesheet.Palette.HighlightWarning): every colour the call writes, whether a nearer folder's call
// colours the kind too or not, so the same line on every page of one palette: the terminal says it once.
// A warning by default; under "strict" a compile error of the page, as a pair short of contrast is. A page
// that shows none of a call's colours says nothing of it.
func (c *compiler) tellHighlight() {
	sources := make([]stylesheet.Source, 0, len(c.highlightShown))
	for s := range c.highlightShown {
		sources = append(sources, s)
	}
	stylesheet.SortSources(sources)
	for _, s := range sources {
		message := c.table.Palette().HighlightWarning(s)
		if message == "" {
			continue
		}
		if !c.site.strictContrast() {
			c.warn(s.File, s.Line, s.Column, message)
			continue
		}
		err := &Error{File: s.File, Line: s.Line, Column: s.Column, Message: message}
		if c.contrastSeen == nil {
			c.contrastSeen = map[string]bool{}
		}
		if key := err.Error(); !c.contrastSeen[key] {
			c.contrastSeen[key] = true
			c.contrast = append(c.contrast, err)
		}
	}
}
