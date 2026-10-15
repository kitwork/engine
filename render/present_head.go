package render

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
)

// A Kitwork UI page knows, when it is compiled, every class, icon, component and runtime attribute it
// can put in its HTML (ui.Page.Skeleton). The presentation passes it needs therefore run over that
// skeleton once, and every response reuses what they put in <head>. Its CSS is not theirs: the page's
// classes are engine/ui/stylesheet's (reboot, colors, page), its typefaces the engine's fonts file, so
// jit/css, the material pass, the highlight pass and the fonts pass — the template pages' CSS
// machinery — never run for it. What the passes still make as CSS, the icons and the brand logos the
// skeleton names, is taken out of <head> and served as a file of its own under a name made of its role
// and its bytes (/_kitwork/icons.<hash>.css, logo.<hash>.css), inside the elements layer.

// PageStylesheet is one file of a page's CSS: what one generator made, under its role.
type PageStylesheet struct {
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
var pageStylesheetRoles = []struct{ name, generated string }{
	{"icons", "icons"},
	{"logo", "logo"},
}

// stylesheetRole is the role of a <style> block by its data-kitwork-generated value, -1 for a block no
// role names (the empty marker of where the stylesheets go).
func stylesheetRole(generated string) int {
	for i, role := range pageStylesheetRoles {
		if role.generated == generated {
			return i
		}
	}
	return -1
}

// stylesheetMarker holds the place of the page's stylesheets while the passes run: at the top of
// <head>, so the pre-paint goes before it and the scripts after. A <meta>, which the minifier keeps
// (an empty <style> it would drop).
const (
	stylesheetMarkerName = "data-kitwork-stylesheets"
	stylesheetMarker     = "<meta " + stylesheetMarkerName + ">"
)

// PageHead is what the presentation passes build once for a Kitwork UI page.
type PageHead struct {
	// Head is what goes into <head> as it is, in the passes' order: the appearance pre-paint, the
	// generator meta, the manifest link and the scripts the page needs. It holds no stylesheet.
	Head string
	// Stylesheet is where in Head the page's stylesheets belong: after the pre-paint (which must run
	// before anything paints) and before the scripts.
	Stylesheet int
	// Stylesheets are the CSS files the passes made, icons then logos, each only when the skeleton
	// names one.
	Stylesheets []PageStylesheet
	// Scripts are the JavaScript files Head links, in its order (jitjs.DeliverFiles): the kernel, Drive,
	// the directives, services and components the page uses; nil when it links none.
	Scripts []*jitjs.Asset
}

// Insert is Head with links (the page's stylesheets, in the cascade's order) where they belong.
func (h PageHead) Insert(links string) string {
	at := h.Stylesheet
	if at < 0 || at > len(h.Head) {
		at = len(h.Head)
	}
	return h.Head[:at] + links + h.Head[at:]
}

// Link is the <link> of one stylesheet the page links, marked as the engine's with its role, so Drive
// trades it for the next page's of the same role on a move.
func Link(href, generated string) string {
	return `<link rel="stylesheet" href="` + href + `" data-kitwork-generated="` + generated + `">`
}

// PresentHead runs the presentation passes a Kitwork UI page needs once over its skeleton and returns
// what they put in <head> (PageHead): the icons and logos apart, one CSS text each, the rest as it
// goes into <head>. Minified once when the site minifies. Anything the passes do to the body of the
// skeleton is dropped: the skeleton only lists what the page can use, the page itself renders per
// request.
func (r *Render) PresentHead(skeleton string) PageHead {
	if r == nil {
		return PageHead{}
	}
	doc := "<!doctype html><html><head>" + stylesheetMarker + "</head><body>" + skeleton + "</body></html>"
	out, scripts := r.applyUIPresentation(doc)
	start := strings.Index(out, "<head>")
	end := strings.Index(out, "</head>")
	if start < 0 || end < start {
		return PageHead{}
	}
	inner := out[start+len("<head>") : end]
	minify := r.shouldMinify()
	if minify {
		inner = minifier.HTML(inner)
		if r.themeMode != "off" {
			inner = theme.Canonicalize(inner)
		}
	}
	separator := "\n"
	if minify {
		separator = ""
	}
	at := -1
	if i := strings.Index(inner, stylesheetMarkerName); i >= 0 {
		open := strings.LastIndexByte(inner[:i], '<')
		if end := strings.IndexByte(inner[i:], '>'); open >= 0 && end >= 0 {
			// every block the passes add goes after it, so taking them out does not move this place
			inner, at = inner[:open]+inner[i+end+1:], open
		}
	}
	rest, first, sheets := takeStylesheets(inner, separator)
	if at < 0 {
		at = first
	}
	for i := range sheets {
		sheets[i].CSS = string(stylesheet.Elements([]byte(sheets[i].CSS)))
	}
	return PageHead{Head: rest, Stylesheet: at, Stylesheets: sheets, Scripts: scripts}
}

// applyUIPresentation is applyStaticPresentation for a Kitwork UI page, in the same order, without
// the CSS passes of the template pages (highlight, jit/css, material, fonts): a .ui page's CSS is
// engine/ui/stylesheet's and the engine's fonts file. It also returns the JavaScript files it linked.
func (r *Render) applyUIPresentation(out string) (string, []*jitjs.Asset) {
	out = jitmanifest.Render(out, r.manifestPath)
	out = withGenerator(out)
	if r.drive {
		// a .ui page always writes its own progressbar and reader: these find them and add nothing
		out = withProgressbar(out)
		out = withReader(out)
	}
	out = icons.Render(out)
	out = logo.Render(out)
	out, scripts := jitjs.DeliverFiles(out, r.siteComponents, jitjs.Options{Drive: r.drive})
	hydrate.Check(out, jitjs.ComponentNames(r.siteComponents))
	switch r.themeMode {
	case "off":
	case "force":
		out = theme.Force(out)
	default:
		out = theme.Render(out)
	}
	return out, scripts
}

// IconsStylesheet is the icons file of one response of a Kitwork UI page whose data named icons its
// skeleton could not list (<icon value="{{ … }}" />): the rules of every icon named — the page's own
// and the data's, one set — minified when the site minifies, inside the elements layer like the
// page's own icons file, for the caller to serve as /_kitwork/icons.<hash>.css. nil when none of the
// names is a known icon.
func (r *Render) IconsStylesheet(names []string) []byte {
	if r == nil || len(names) == 0 {
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
	if r.shouldMinify() {
		css = minifier.CSS(css)
	}
	return stylesheet.Elements([]byte(css))
}

// takeStylesheets takes every <style> block out of a head: what is left, where the first one stood
// (-1 when there was none), and their CSS by role (stylesheetRole of the block's data-kitwork-generated
// value), each role's blocks joined by separator in the order they stood. A block no role names stays
// in the head where it stood: a pass that makes CSS a .ui page does not expect is seen, never dropped.
func takeStylesheets(head, separator string) (rest string, at int, sheets []PageStylesheet) {
	var kept strings.Builder
	css := make([]strings.Builder, len(pageStylesheetRoles))
	at = -1
	for from := 0; ; {
		open := indexFoldASCII(head, from, "<style")
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
		contentStart := startTagEnd(head, next)
		if contentStart < 0 {
			kept.WriteString(head[from:])
			break
		}
		closing := indexFoldASCII(head, contentStart, "</style")
		if closing < 0 {
			kept.WriteString(head[from:])
			break
		}
		closeEnd := strings.IndexByte(head[closing:], '>')
		if closeEnd < 0 {
			kept.WriteString(head[from:])
			break
		}
		role := stylesheetRole(attributeValue(head[next:contentStart-1], "data-kitwork-generated"))
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
	for role := range pageStylesheetRoles {
		if css[role].Len() == 0 {
			continue // only empty blocks: nothing to link
		}
		sheets = append(sheets, PageStylesheet{
			Name:      pageStylesheetRoles[role].name,
			Generated: pageStylesheetRoles[role].generated,
			CSS:       css[role].String(),
		})
	}
	return kept.String(), at, sheets
}

// attributeValue is the value of one attribute in the attributes of a start tag (what stands between
// its name and its >), quoted or not — the minifier drops the quotes it can — or "" when the tag does
// not carry it. The name is matched whole and without regard to ASCII case.
func attributeValue(attributes, name string) string {
	for from := 0; ; {
		i := indexFoldASCII(attributes, from, name)
		if i < 0 {
			return ""
		}
		from = i + len(name)
		if i > 0 && !isAttributeSpace(attributes[i-1]) {
			continue // the end of another name: data-x-data-kitwork-generated
		}
		j := from
		for j < len(attributes) && isAttributeSpace(attributes[j]) {
			j++
		}
		if j >= len(attributes) || attributes[j] != '=' {
			if j < len(attributes) && attributes[j] != '>' && attributes[j] != '/' && j == from {
				continue // a longer name: data-kitwork-generated-by
			}
			return "" // present with no value
		}
		j++
		for j < len(attributes) && isAttributeSpace(attributes[j]) {
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
		for end < len(attributes) && !isAttributeSpace(attributes[end]) && attributes[end] != '>' {
			end++
		}
		return attributes[j:end]
	}
}

func isAttributeSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}
