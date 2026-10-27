package work

import (
	"strings"
	"testing"

	"github.com/kitwork/engine/ui/stylesheet"
	"github.com/kitwork/engine/utilities/minifier"
)

// headStylesheetNames lists a head's stylesheets by role, in order.
func headStylesheetNames(built uiHead) string {
	var out []string
	for _, sheet := range built.Stylesheets {
		out = append(out, sheet.Name+"="+sheet.Generated)
	}
	return strings.Join(out, " ")
}

// headStylesheetOf is the CSS of one role, "" when the head has none.
func headStylesheetOf(built uiHead, name string) string {
	for _, sheet := range built.Stylesheets {
		if sheet.Name == name {
			return sheet.CSS
		}
	}
	return ""
}

// The head of a .ui page runs its passes once over the page's skeleton and keeps what they put in
// <head>; nothing of the skeleton's body comes back. A .ui page's CSS is engine/ui/stylesheet's, so no
// template CSS pass runs: no utilities for a class, no material, no @font-face. What the passes make as
// CSS, the icons, is taken out of the head, a stylesheet of its own, inside the elements layer (the
// brand logos had one too until jit/logo went, 27/10/2026). Insert puts the page's links where the stylesheets belong: after the pre-paint,
// before the scripts.
func TestUIHeadTakesTheStylesheetsOut(t *testing.T) {
	p := &uiPresentation{dark: true, drive: true}
	skeleton := `<body><div class="p-2 text-brand font-outfit card" data-kitwork-component="toast"><i class="icon-coffee" aria-hidden="true"></i></div></body>`
	built := p.head(skeleton)

	if got := headStylesheetNames(built); got != "icons=icons" {
		t.Fatalf("a .ui page's passes make its icons stylesheet only: got %q", got)
	}
	icons := headStylesheetOf(built, "icons")
	if !strings.HasPrefix(icons, stylesheet.LayerStatement+"\n@layer elements{\n") || !strings.Contains(icons, ".icon-coffee") || !strings.HasSuffix(icons, "}\n") {
		t.Errorf("the icons are inside the elements layer, after the layer statement:\n%.300s", icons)
	}
	for _, unwanted := range []string{".p-2", ".text-brand", "--color-", "@font-face", ".card"} {
		if strings.Contains(icons, unwanted) {
			t.Errorf("no template CSS pass runs for a .ui page, yet %q is there:\n%.300s", unwanted, icons)
		}
	}

	for _, unwanted := range []string{"<style", "</style>", "<body", "<div", "</head>", "<head>", "data-kitwork-stylesheets", "@font-face"} {
		if strings.Contains(built.Head, unwanted) {
			t.Errorf("the head must not carry %q:\n%s", unwanted, built.Head)
		}
	}
	for _, want := range []string{`name="generator"`, `data-kitwork-generated="appearance"`, `data-kitwork-generated="runtime"`} {
		if !strings.Contains(built.Head, want) {
			t.Errorf("the head misses %q:\n%s", want, built.Head)
		}
	}

	links := stylesheetLink("/_kitwork/reboot.0123456789abcdef.css", "reboot") + stylesheetLink("/_kitwork/icons.1123456789abcdef.css", "icons")
	if links != `<link rel="stylesheet" href="/_kitwork/reboot.0123456789abcdef.css" data-kitwork-generated="reboot">`+
		`<link rel="stylesheet" href="/_kitwork/icons.1123456789abcdef.css" data-kitwork-generated="icons">` {
		t.Fatalf("stylesheetLink marks each stylesheet as the engine's with its role: %s", links)
	}
	head := built.Insert(links)
	prepaint, at, script := strings.Index(head, `data-kitwork-generated="appearance"`), strings.Index(head, links), strings.Index(head, "<script data-kitwork-generated=\"runtime\"")
	if strings.Count(head, links) != 1 || !(prepaint < at && at < script) {
		t.Errorf("the stylesheets go once after the pre-paint (%d) and before the scripts (%d), at %d:\n%s", prepaint, script, at, head)
	}

	// control: a skeleton with no icon makes no stylesheet
	other := p.head(`<div class="p-4"></div>`)
	if got := headStylesheetNames(other); got != "" {
		t.Errorf("a page with no icon has no stylesheet from the passes, got %q", got)
	}
	if empty := (uiHead{Head: "<meta>", Stylesheet: -1}); empty.Insert("<link>") != "<meta><link>" {
		t.Error("a head with no place for the stylesheets puts them at its end")
	}
}

// A route with no dark look pre-paints only a page with something that switches the appearance; a
// route with one pre-paints every page. CONTROL: the same skeleton with dark gets the pre-paint.
func TestUIHeadPrepaintsADarkRoute(t *testing.T) {
	skeleton := `<p>x</p>`
	if built := (&uiPresentation{}).head(skeleton); strings.Contains(built.Head, `data-kitwork-generated="appearance"`) {
		t.Errorf("a page of a route with no dark look and nothing that switches it has no pre-paint:\n%s", built.Head)
	}
	if built := (&uiPresentation{dark: true}).head(skeleton); !strings.Contains(built.Head, `data-kitwork-generated="appearance"`) {
		t.Errorf("CONTROL: every page of a route with a dark look pre-paints:\n%s", built.Head)
	}
}

// Minified once for production: the CSS and the head alike, and the pre-paint kept byte for byte (its
// CSP hash is fixed). The minifier drops the quotes of data-kitwork-generated="icons"; the role is
// still read, so the stylesheets stay apart (CONTROL: it does drop them).
func TestUIHeadMinifiesOnce(t *testing.T) {
	skeleton := `<p class="p-2 text-brand"><i class="icon-coffee"></i>x</p>`
	built := (&uiPresentation{dark: true, minify: true}).head(skeleton)
	icons := headStylesheetOf(built, "icons")
	written := headStylesheetOf((&uiPresentation{dark: true}).head(skeleton), "icons")
	if !strings.Contains(icons, ".icon-coffee") || !strings.Contains(icons, "var(--i)center") || len(icons) >= len(written) ||
		!strings.Contains(written, "var(--i) center") {
		t.Errorf("the CSS must be minified (the development file keeps the space):\n%.300s\n%.300s", icons, written)
	}
	if got := headStylesheetNames(built); got != "icons=icons" {
		t.Errorf("minified, the icons are still a stylesheet of their own: got %q", got)
	}
	for _, sheet := range built.Stylesheets {
		if strings.Contains(sheet.CSS, "<style") {
			t.Errorf("no <style> may remain in %s:\n%.200s", sheet.Name, sheet.CSS)
		}
	}
	if strings.Contains(built.Head, "<style") {
		t.Errorf("no <style> may remain, minified or not:\nhead %s", built.Head)
	}
	if unquoted := minifier.HTML(`<style data-kitwork-generated="icons">a{b:c}</style>`); !strings.Contains(unquoted, `data-kitwork-generated=icons>`) {
		t.Errorf("CONTROL: the minifier was expected to drop the quotes, which is what this test reads through: %s", unquoted)
	}
	control := (&uiPresentation{dark: true}).head(`<p class="p-2 text-brand">x</p>`)
	prepaint := control.Head[:strings.Index(control.Head, "</script>")+len("</script>")]
	if !strings.HasPrefix(prepaint, `<script data-kitwork-generated="appearance">`) || !strings.Contains(built.Head, prepaint) {
		t.Errorf("the minified head must keep the canonical pre-paint:\n%s", built.Head)
	}
}

// tagAttribute reads a start tag's attribute quoted, single-quoted or bare, whole-name and without
// regard to case; a name that only ends or starts like it is another attribute.
func TestTagAttribute(t *testing.T) {
	for attributes, want := range map[string]string{
		` data-kitwork-generated="icons"`:                                "icons",
		` data-kitwork-generated='reboot'`:                               "reboot",
		` data-kitwork-generated=fonts`:                                  "fonts",
		` id=x data-kitwork-generated = fonts media=all`:                 "fonts",
		` DATA-KITWORK-GENERATED="icons"`:                                "icons",
		` data-x-data-kitwork-generated="icons"`:                         "",
		` data-kitwork-generated-by="icons" data-kitwork-generated=page`: "page",
		` data-kitwork-generated`:                                        "",
		``:                                                               "",
		` media="all"`:                                                   "",
	} {
		if got := tagAttribute(attributes, "data-kitwork-generated"); got != want {
			t.Errorf("tagAttribute(%q) = %q, want %q", attributes, got, want)
		}
	}
}

// iconsStylesheet is the icons file of a response whose data named icons: every known icon of the set
// (the page's and the data's), layered like the page's own icons file, nothing else; an unknown name
// adds nothing and a set with no known icon is no file.
func TestUIIconsStylesheet(t *testing.T) {
	p := &uiPresentation{}
	css := string(p.iconsStylesheet([]string{"coffee", "sun", "not-an-icon"}))
	for _, want := range []string{stylesheet.LayerStatement + "\n@layer elements{\n", ".icon-coffee", ".icon-sun"} {
		if !strings.Contains(css, want) {
			t.Errorf("missing %q:\n%s", want, css)
		}
	}
	for _, unwanted := range []string{":root", "box-sizing", "not-an-icon", "<style"} {
		if strings.Contains(css, unwanted) {
			t.Errorf("the icons file holds icons only, not %q:\n%s", unwanted, css)
		}
	}
	if p.iconsStylesheet([]string{"not-an-icon"}) != nil || p.iconsStylesheet(nil) != nil {
		t.Error("no known icon, no file")
	}
	// the same set in another order is the same bytes: the file is named by them
	if string(p.iconsStylesheet([]string{"sun", "coffee"})) != string(p.iconsStylesheet([]string{"coffee", "sun"})) {
		t.Error("one set, one file, whatever the order")
	}
}

// A <style> no role names stays in the head where it stood: CSS a .ui page does not expect is seen by
// the tests that refuse a <style> in the head, never dropped in silence. CONTROL: the icons block next
// to it is taken out as its file.
func TestTakeHeadStylesheetsKeepsAnUnknownBlock(t *testing.T) {
	rest, at, sheets := takeHeadStylesheets(`<meta x><style data-kitwork-generated="css">.p-2{}</style><style data-kitwork-generated="icons">.icon-a{}</style><script></script>`, "")
	if rest != `<meta x><style data-kitwork-generated="css">.p-2{}</style><script></script>` || at != len(`<meta x><style data-kitwork-generated="css">.p-2{}</style>`) {
		t.Errorf("the unknown block stays where it stood, the place is the icons': %q at %d", rest, at)
	}
	if len(sheets) != 1 || sheets[0].Name != "icons" || sheets[0].CSS != ".icon-a{}" {
		t.Errorf("CONTROL: the icons block is its file: %+v", sheets)
	}
}
