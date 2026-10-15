package ui

import (
	"path"
	"sort"
	"strings"

	"github.com/kitwork/engine/ui/stylesheet"
)

// Request is what one render needs from the request and from router.work.
type Request struct {
	// Path is the URL path being viewed: links to it get aria-current.
	Path string
	// Origin ("https://quancaphe.vn") turns relative links into the absolute URLs that structured
	// data (BreadcrumbList) requires.
	Origin string
	// Scope holds the names the page reads: the locals of router.work's view and its imports.
	Scope map[string]any

	Title       string
	Description string
	Language    string // lang of <html>; "vi" when empty. dir="rtl" is the compile's (Site.Language), not this
	// Transition is router.transition of this page's folder: how <content /> changes when a link
	// moves here without a reload. "" and "none" write nothing; the page's own value is the one the
	// move uses, so each branch of a site can move its own way.
	Transition string
	// Appearance is router.appearance of this page's folder (the nearest that sets one): the light/dark
	// mode the page shows before the visitor chooses. "light" and "dark" are written on <html> as
	// data-kitwork-appearance, for the pre-paint and kit.appearance to fall back on while the visitor
	// has stored no choice; "" and "system" (follow the device) write nothing.
	Appearance string
	// Favicon is the URL of the page's icon (router.favicon of the nearest folder): <link rel="icon">.
	Favicon string
	// Head is what the engine built for this page once (KITWORK_UI §18): the appearance pre-paint, the
	// links to its stylesheets in the cascade's order — reboot, colors, fonts, icons, page, each named
	// by its role and its bytes —, the scripts it needs and the font preloads. It goes into <head> as it
	// is; nothing is scanned per request. Empty, the document carries the stylesheets of the page itself
	// inline instead (reboot, the palette's colours, the page's own rules), and the scrolled script when
	// it can carry a scrolled: class: a page shown on its own, with no engine to serve the files.
	Head string
	// IconsHead is the head of a response whose data named icons the page's icons stylesheet could not
	// list (<icon value="{{ … }}" />), given their names sorted: the head built once with its icons
	// link naming ONE file for the page's own icons and these (the engine keeps one per distinct set).
	// nil writes Head as it is, and those icons have no CSS. Classes from data never need it: every
	// class a value from data can make is in the page's stylesheet.
	IconsHead func(icons []string) string
	// Warn hears, while developing, of a value from data that made no class (a colour that is no token
	// of the palette): file:line:col of the attribute, once per page. nil says nothing.
	Warn func(message string)
}

// Render writes the page body (<body>…</body>) for one request.
func (p *Page) Render(req Request) string {
	body, _ := p.render(req)
	return body
}

// render writes the body and returns the names of the icons only this request's data named, sorted.
func (p *Page) render(req Request) (string, []string) {
	scope := make(map[string]any, len(req.Scope)+4)
	for name, fn := range formatHelpers() {
		scope[name] = fn
	}
	for name, v := range req.Scope { // the author's names win over the built-in helpers
		scope[name] = v
	}
	r := &run{path: req.Path, origin: req.Origin, page: p, language: req.Language, warn: req.Warn}
	p.root.render(r, scope)
	extra := make([]string, 0, len(r.extra))
	for c := range r.extra {
		extra = append(extra, strings.TrimPrefix(c, "icon-"))
	}
	sort.Strings(extra)
	return r.b.String(), extra
}

// Icons are the names of the icons the page can show whatever its data (<icon value="coffee" />,
// every value an expression spells out), sorted: what its icons stylesheet holds. An icon only a
// request's data names is that request's (Request.IconsHead).
func (p *Page) Icons() []string {
	p.prepareSkeleton()
	var out []string
	for class := range p.skeleton.classes {
		if name, ok := strings.CutPrefix(class, "icon-"); ok && name != "" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// Stylesheet is the page's own stylesheet, page.<hash>.css: the rule of every class any branch of its
// HTML can carry, recorded as it compiled, inside the page and show layers. Its Name is "" when the
// page carries no class: no file, no link.
func (p *Page) Stylesheet() stylesheet.File {
	if p.rules == nil {
		return stylesheet.File{Role: "page"}
	}
	return p.rules.File()
}

// Weights are the font weights the page's text can take, ascending: running text's 400, those of the
// elements it holds before anything is written (a badge's 500, a heading's 600) and those its classes
// set (weight="bold" is 700): the weights its fonts file ships.
func (p *Page) Weights() []int {
	seen := map[int]bool{}
	elements := stylesheet.ElementWeights()
	seen[elements["text"]] = true
	for _, kind := range []string{"badge", "heading"} {
		if p.uses[kind] {
			seen[elements[kind]] = true
		}
	}
	if p.rules != nil {
		for _, w := range p.rules.Weights() {
			seen[w] = true
		}
	}
	out := make([]int, 0, len(seen))
	for w := range seen {
		out = append(out, w)
	}
	sort.Ints(out)
	return out
}

// UsesCode reports whether anything on the page is set in the code face: a code block, code in a
// line, or font="code" (any prefix).
func (p *Page) UsesCode() bool {
	if p.uses["code"] {
		return true
	}
	if p.rules != nil {
		for _, class := range p.rules.Classes() {
			if class == "font-code" || strings.HasSuffix(class, ":font-code") {
				return true
			}
		}
	}
	return false
}

// UsesScrolled reports whether the page's HTML can carry a scrolled: class (scrolled:shadow="small",
// in any branch, in any piece the page is made of): the page needs the script that keeps the flag
// those classes read, scrolled.<hash>.js. A page without one gets no script.
func (p *Page) UsesScrolled() bool {
	return p.rules != nil && p.rules.Scrolled()
}

// iconType is the media type of an icon by its extension, so the browser skips one it cannot show.
func iconType(url string) string {
	if i := strings.IndexAny(url, "?#"); i >= 0 {
		url = url[:i]
	}
	switch strings.ToLower(path.Ext(url)) {
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".ico":
		return "image/x-icon"
	case ".webp":
		return "image/webp"
	}
	return ""
}

// Document writes the whole HTML document for one request: head (a page backdrop's theme-color per
// mode, title, description, then the head built once — the pre-paint, the links to the page's
// stylesheets, the scripts — or, with none, the page's stylesheets and the scrolled script inline) and
// body.
func (p *Page) Document(req Request) string {
	body, extra := p.render(req)
	language := req.Language
	if language == "" {
		language = "vi"
	}
	var b strings.Builder
	b.Grow(len(body) + 4096)
	b.WriteString("<!doctype html>\n<html")
	writeAttr(&b, "lang", language)
	if p.rightToLeft {
		// the browser takes the direction from dir, never from lang: the compile's language, so start and
		// end flip where its warnings of left and right said (Request.Language changes lang only)
		writeAttr(&b, "dir", "rtl")
	}
	if req.Appearance == "light" || req.Appearance == "dark" {
		writeAttr(&b, "data-kitwork-appearance", req.Appearance)
	}
	if req.Transition != "" && req.Transition != "none" {
		writeAttr(&b, "data-kitwork-transition", req.Transition)
	}
	b.WriteString(">\n<head>\n<meta charset=\"utf-8\">\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	b.WriteString(p.themeColor(req.Appearance)) // a page backdrop's first stop, per mode (backdrop.go)
	if req.Title != "" {
		b.WriteString("<title>")
		b.WriteString(escapeText(req.Title))
		b.WriteString("</title>\n")
	}
	if req.Description != "" {
		b.WriteString("<meta name=\"description\"")
		writeAttr(&b, "content", req.Description)
		b.WriteString(">\n")
	}
	if req.Favicon != "" {
		b.WriteString("<link rel=\"icon\"")
		writeAttr(&b, "href", req.Favicon)
		if kind := iconType(req.Favicon); kind != "" {
			writeAttr(&b, "type", kind)
		}
		b.WriteString(">\n")
	}
	if req.Head == "" {
		b.WriteString("<style>\n")
		b.Write(stylesheet.Reboot().CSS)
		b.Write(stylesheet.Colors(p.palette).CSS)
		b.Write(p.Stylesheet().CSS)
		b.WriteString("</style>\n")
		if p.UsesScrolled() {
			b.WriteString("<script>\n")
			b.Write(stylesheet.ScrolledScript().JavaScript)
			b.WriteString("</script>\n")
		}
	}
	if len(extra) > 0 && req.IconsHead != nil {
		b.WriteString(req.IconsHead(extra))
	} else {
		b.WriteString(req.Head)
	}
	b.WriteString("</head>\n")
	b.WriteString(body)
	b.WriteString("\n</html>\n")
	return b.String()
}

func escapeText(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
