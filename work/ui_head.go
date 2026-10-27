package work

import (
	"strings"

	"github.com/kitwork/engine/jit/hydrate"
	"github.com/kitwork/engine/jit/icons"
	jitjs "github.com/kitwork/engine/jit/js"
	"github.com/kitwork/engine/jit/logo"
	jitmanifest "github.com/kitwork/engine/jit/manifest"
	"github.com/kitwork/engine/jit/theme"
	"github.com/kitwork/engine/ui/stylesheet"
	"github.com/kitwork/engine/utilities/minifier"
	"github.com/kitwork/engine/version"
)

// A Kitwork UI page knows, when it is compiled, every class, icon, component and runtime attribute it
// can put in its HTML (ui.Page.Skeleton). The passes its head needs therefore run over that skeleton
// once, and every response reuses what they put in <head>: the manifest link, the generator meta, the
// icons and logos the skeleton names, the scripts it needs (the kernel, Drive, components, services)
// and the appearance pre-paint. Its CSS is engine/ui/stylesheet's (reboot, colors, page) and the
// engine's fonts file; what the passes make as CSS, the icons and the logos, is taken out of <head>
// and served as a file of its own, /_kitwork/icons.<hash>.css and logo.<hash>.css, inside the
// elements layer. Until 27/10/2026 the template pages' renderer (render.PresentHead) built this head.

// uiPresentation is what a route's .ui pages take from the site to build their head: Drive, the
// router.manifest path, whether the route has a dark look (its pages then pre-paint the appearance
// before anything shows; otherwise only a page with something that switches it does) and whether the
// site minifies.
type uiPresentation struct {
	drive        bool
	manifestPath string
	dark         bool
	minify       bool
}

// uiHeadStylesheet is one file of a page's CSS the passes make, under its role.
type uiHeadStylesheet struct {
	// Name is the file's role, the first part of its name under /_kitwork/: "icons" or "logo".
	Name string
	// Generated is the data-kitwork-generated value of its link: the <style> blocks it holds were
	// marked so.
	Generated string
	// CSS is its blocks' CSS, in the order they stood, inside the elements layer of a .ui page
	// (stylesheet.Elements): unlayered, an icon would beat show="false" medium:show="true".
	CSS string
}

// The roles of the stylesheets the passes make for a .ui page, in the order they write their blocks.
var uiHeadStylesheetRoles = []struct{ name, generated string }{
	{"icons", "icons"},
	{"logo", "logo"},
}

// uiHeadStylesheetRole is the role of a <style> block by its data-kitwork-generated value, -1 for a
// block no role names.
func uiHeadStylesheetRole(generated string) int {
	for i, role := range uiHeadStylesheetRoles {
		if role.generated == generated {
			return i
		}
	}
	return -1
}

// uiStylesheetMarker holds the place of the page's stylesheets while the passes run: at the top of
// <head>, so the pre-paint goes before it and the scripts after. A <meta>, which the minifier keeps
// (an empty <style> it would drop).
const (
	uiStylesheetMarkerName = "data-kitwork-stylesheets"
	uiStylesheetMarker     = "<meta " + uiStylesheetMarkerName + ">"
)

// uiHead is what the passes build once for a Kitwork UI page.
type uiHead struct {
	// Head is what goes into <head> as it is, in the passes' order: the appearance pre-paint, the
	// generator meta, the manifest link and the scripts the page needs. It holds no stylesheet.
	Head string
	// Stylesheet is where in Head the page's stylesheets belong: after the pre-paint (which must run
	// before anything paints) and before the scripts.
	Stylesheet int
	// Stylesheets are the CSS files the passes made, icons then logos, each only when the skeleton
	// names one.
	Stylesheets []uiHeadStylesheet
	// Scripts are the JavaScript files Head links, in its order (jitjs.DeliverFiles): the kernel, Drive,
	// the directives, services and components the page uses; nil when it links none.
	Scripts []*jitjs.Asset
}

// Insert is Head with links (the page's stylesheets, in the cascade's order) where they belong.
func (h uiHead) Insert(links string) string {
	at := h.Stylesheet
	if at < 0 || at > len(h.Head) {
		at = len(h.Head)
	}
	return h.Head[:at] + links + h.Head[at:]
}

// stylesheetLink is the <link> of one stylesheet a page links, marked as the engine's with its role, so
// Drive trades it for the next page's of the same role on a move.
func stylesheetLink(href, generated string) string {
	return `<link rel="stylesheet" href="` + href + `" data-kitwork-generated="` + generated + `">`
}

// uiGeneratorTag names the engine in the page, the way a page built by WordPress or Hugo says so.
const uiGeneratorTag = `<meta name="generator" content="Kitwork ` + version.Number + `">`

// head runs the passes over a page's skeleton and returns what they put in <head> (uiHead): the icons
// and logos apart, one CSS text each, the rest as it goes into <head>, minified once when the site
// minifies. What the passes do to the body of the skeleton is dropped: the skeleton only lists what the
// page can use, the page itself renders per request.
func (p *uiPresentation) head(skeleton string) uiHead {
	if p == nil {
		return uiHead{}
	}
	doc := "<!doctype html><html><head>" + uiStylesheetMarker + "</head><body>" + skeleton + "</body></html>"
	out, scripts := p.passes(doc)
	start := strings.Index(out, "<head>")
	end := strings.Index(out, "</head>")
	if start < 0 || end < start {
		return uiHead{}
	}
	inner := out[start+len("<head>") : end]
	separator := "\n"
	if p.minify {
		inner = theme.Canonicalize(minifier.HTML(inner))
		separator = ""
	}
	at := -1
	if i := strings.Index(inner, uiStylesheetMarkerName); i >= 0 {
		open := strings.LastIndexByte(inner[:i], '<')
		if end := strings.IndexByte(inner[i:], '>'); open >= 0 && end >= 0 {
			// every block the passes add goes after it, so taking them out does not move this place
			inner, at = inner[:open]+inner[i+end+1:], open
		}
	}
	rest, first, sheets := takeHeadStylesheets(inner, separator)
	if at < 0 {
		at = first
	}
	for i := range sheets {
		sheets[i].CSS = string(stylesheet.Elements([]byte(sheets[i].CSS)))
	}
	return uiHead{Head: rest, Stylesheet: at, Stylesheets: sheets, Scripts: scripts}
}

// passes are the head's passes over a document, in order: the manifest link, the generator meta, the
// icons and logos, the scripts (it returns the JavaScript files it linked), the check of the grouped
// attributes while developing, the appearance pre-paint. A .ui page writes its own progressbar and
// reader.
func (p *uiPresentation) passes(out string) (string, []*jitjs.Asset) {
	out = jitmanifest.Render(out, p.manifestPath)
	if end := strings.Index(out, "</head>"); end >= 0 {
		out = out[:end] + uiGeneratorTag + out[end:]
	}
	out = icons.Render(out)
	out = logo.Render(out)
	out, scripts := jitjs.DeliverFiles(out, jitjs.Options{Drive: p.drive})
	hydrate.Check(out, jitjs.ComponentNames())
	if p.dark {
		out = theme.Force(out)
	} else {
		out = theme.Render(out)
	}
	return out, scripts
}

// iconsStylesheet is the icons file of one response of a Kitwork UI page whose data named icons its
// skeleton could not list (<icon value="{{ … }}" />): the rules of every icon named, the page's own and
// the data's, one set, minified when the site minifies, inside the elements layer like the page's own
// icons file, for the caller to serve as /_kitwork/icons.<hash>.css. nil when none of the names is a
// known icon.
func (p *uiPresentation) iconsStylesheet(names []string) []byte {
	if p == nil || len(names) == 0 {
		return nil
	}
	var known []string
	for _, name := range names {
		if icons.Has(name) {
			known = append(known, name)
		}
	}
	css := icons.CSS(known)
	if css == "" {
		return nil
	}
	if p.minify {
		css = minifier.CSS(css)
	}
	return stylesheet.Elements([]byte(css))
}

// takeHeadStylesheets takes every <style> block out of a head: what is left, where the first one stood
// (-1 when there was none), and their CSS by role (uiHeadStylesheetRole of the block's
// data-kitwork-generated value), each role's blocks joined by separator in the order they stood. A
// block no role names stays in the head where it stood: a pass that makes CSS a .ui page does not
// expect is seen, never dropped.
func takeHeadStylesheets(head, separator string) (rest string, at int, sheets []uiHeadStylesheet) {
	var kept strings.Builder
	css := make([]strings.Builder, len(uiHeadStylesheetRoles))
	at = -1
	for from := 0; ; {
		open := indexFold(head, from, "<style")
		if open < 0 {
			kept.WriteString(head[from:])
			break
		}
		next := open + len("<style")
		if next < len(head) && head[next] != '>' && head[next] != ' ' && head[next] != '\t' && head[next] != '\n' && head[next] != '\r' && head[next] != '/' {
			kept.WriteString(head[from:next]) // <styles…>: not a style element
			from = next
			continue
		}
		contentStart := startTagClose(head, next)
		if contentStart < 0 {
			kept.WriteString(head[from:])
			break
		}
		closing := indexFold(head, contentStart, "</style")
		if closing < 0 {
			kept.WriteString(head[from:])
			break
		}
		closeEnd := strings.IndexByte(head[closing:], '>')
		if closeEnd < 0 {
			kept.WriteString(head[from:])
			break
		}
		role := uiHeadStylesheetRole(tagAttribute(head[next:contentStart-1], "data-kitwork-generated"))
		if role < 0 {
			kept.WriteString(head[from : closing+closeEnd+1])
			from = closing + closeEnd + 1
			continue
		}
		kept.WriteString(head[from:open])
		if at < 0 {
			at = kept.Len()
		}
		if css[role].Len() > 0 {
			css[role].WriteString(separator)
		}
		css[role].WriteString(head[contentStart:closing])
		from = closing + closeEnd + 1
	}
	for role := range uiHeadStylesheetRoles {
		if css[role].Len() == 0 {
			continue // only empty blocks: nothing to link
		}
		sheets = append(sheets, uiHeadStylesheet{
			Name:      uiHeadStylesheetRoles[role].name,
			Generated: uiHeadStylesheetRoles[role].generated,
			CSS:       css[role].String(),
		})
	}
	return kept.String(), at, sheets
}

// indexFold is the index of needle in s from from on, without regard to ASCII case; -1 when it is not
// there.
func indexFold(s string, from int, needle string) int {
	for i := from; i+len(needle) <= len(s); i++ {
		if strings.EqualFold(s[i:i+len(needle)], needle) {
			return i
		}
	}
	return -1
}

// startTagClose is the index just after the > that ends a start tag whose attributes begin at from, a
// quoted > aside, or -1 when the tag never closes.
func startTagClose(s string, from int) int {
	var quote byte
	for j := from; j < len(s); j++ {
		c := s[j]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '>':
			return j + 1
		}
	}
	return -1
}

// tagAttribute is the value of one attribute in the attributes of a start tag (what stands between its
// name and its >), quoted or not (the minifier drops the quotes it can), or "" when the tag does not
// carry it. The name is matched whole and without regard to ASCII case.
func tagAttribute(attributes, name string) string {
	for from := 0; ; {
		i := indexFold(attributes, from, name)
		if i < 0 {
			return ""
		}
		from = i + len(name)
		if i > 0 && !isTagSpace(attributes[i-1]) {
			continue // the end of another name: data-x-data-kitwork-generated
		}
		j := from
		for j < len(attributes) && isTagSpace(attributes[j]) {
			j++
		}
		if j >= len(attributes) || attributes[j] != '=' {
			if j < len(attributes) && attributes[j] != '>' && attributes[j] != '/' && j == from {
				continue // a longer name: data-kitwork-generated-by
			}
			return "" // present with no value
		}
		j++
		for j < len(attributes) && isTagSpace(attributes[j]) {
			j++
		}
		if j < len(attributes) && (attributes[j] == '"' || attributes[j] == '\'') {
			end := strings.IndexByte(attributes[j+1:], attributes[j])
			if end < 0 {
				return attributes[j+1:]
			}
			return attributes[j+1 : j+1+end]
		}
		end := j
		for end < len(attributes) && !isTagSpace(attributes[end]) && attributes[end] != '>' {
			end++
		}
		return attributes[j:end]
	}
}

func isTagSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}
