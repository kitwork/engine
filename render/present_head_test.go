package render

import (
	"strings"
	"testing"

	"github.com/kitwork/engine/ui/stylesheet"
	"github.com/kitwork/engine/utilities/minifier"
)

// names lists a head's stylesheets by role, in order.
func names(built PageHead) string {
	var out []string
	for _, sheet := range built.Stylesheets {
		out = append(out, sheet.Name+"="+sheet.Generated)
	}
	return strings.Join(out, " ")
}

// sheetOf is the CSS of one role, "" when the head has none.
func sheetOf(built PageHead, name string) string {
	for _, sheet := range built.Stylesheets {
		if sheet.Name == name {
			return sheet.CSS
		}
	}
	return ""
}

// PresentHead runs the presentation passes a Kitwork UI page needs once over its skeleton and keeps
// what they put in <head>; nothing of the skeleton's body comes back. A .ui page's CSS is
// engine/ui/stylesheet's, so no template CSS pass runs: no utilities for a class, no material, no
// @font-face. What the passes still make as CSS — the icons and the brand logos — is taken out of the
// head, one stylesheet each, inside the elements layer. Insert puts the page's links where the
// stylesheets belong: after the pre-paint, before the scripts.
func TestPresentHeadTakesTheStylesheetsOut(t *testing.T) {
	r := New(Config{Base: t.TempDir(), JitCSS: true, ThemeMode: "force", Drive: true})
	skeleton := `<body><div class="p-2 text-brand font-outfit card" data-kitwork-component="toast"><i class="icon-coffee" aria-hidden="true"></i></div></body>`
	built := r.PresentHead(skeleton)

	if got := names(built); got != "icons=icons" {
		t.Fatalf("a .ui page's passes make its icons stylesheet only: got %q", got)
	}
	icons := sheetOf(built, "icons")
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

	links := Link("/_kitwork/reboot.0123456789abcdef.css", "reboot") + Link("/_kitwork/icons.1123456789abcdef.css", "icons")
	if links != `<link rel="stylesheet" href="/_kitwork/reboot.0123456789abcdef.css" data-kitwork-generated="reboot">`+
		`<link rel="stylesheet" href="/_kitwork/icons.1123456789abcdef.css" data-kitwork-generated="icons">` {
		t.Fatalf("Link marks each stylesheet as the engine's with its role: %s", links)
	}
	head := built.Insert(links)
	prepaint, at, script := strings.Index(head, `data-kitwork-generated="appearance"`), strings.Index(head, links), strings.Index(head, "<script data-kitwork-generated=\"runtime\"")
	if strings.Count(head, links) != 1 || !(prepaint < at && at < script) {
		t.Errorf("the stylesheets go once after the pre-paint (%d) and before the scripts (%d), at %d:\n%s", prepaint, script, at, head)
	}

	// control: a skeleton with no icon makes no stylesheet, and the template pages' passes over the
	// same skeleton do make the utilities, the material and the @font-face
	other := r.PresentHead(`<div class="p-4"></div>`)
	if got := names(other); got != "" {
		t.Errorf("a page with no icon and no logo has no stylesheet from the passes, got %q", got)
	}
	if empty := (PageHead{Head: "<meta>", Stylesheet: -1}); empty.Insert("<link>") != "<meta><link>" {
		t.Error("a head with no place for the stylesheets puts them at its end")
	}
	template := r.Present("<!doctype html><html><head></head>" + skeleton + "</html>")
	for _, want := range []string{".p-2", `data-kitwork-generated="material"`, "@font-face"} {
		if !strings.Contains(template, want) {
			t.Errorf("CONTROL: a template page's passes make %q; without it this test proves nothing", want)
		}
	}
}

// A brand logo is a generator of its own: its stylesheet comes after the icons, as its block did,
// inside the elements layer too.
func TestPresentHeadLinksTheLogoAfterTheIcons(t *testing.T) {
	r := New(Config{Base: t.TempDir(), JitCSS: true})
	built := r.PresentHead(`<p class="p-2 font-outfit"><i class="icon-coffee"></i><i class="logo-github"></i></p>`)
	if got := names(built); got != "icons=icons logo=logo" {
		t.Fatalf("a page with a logo has two stylesheets from the passes, the logo second: got %q", got)
	}
	logo := sheetOf(built, "logo")
	if !strings.Contains(logo, ".logo-github") || strings.Contains(sheetOf(built, "icons"), ".logo-github") || !strings.HasPrefix(logo, stylesheet.LayerStatement) {
		t.Errorf("the logo's rule is in the logo file alone, layered:\n%.300s", logo)
	}
}

// Minified once for production: the CSS and the head alike, and the pre-paint kept byte for byte (its
// CSP hash is fixed). The minifier drops the quotes of data-kitwork-generated="icons"; the role is
// still read, so the stylesheets stay apart (CONTROL: it does drop them).
func TestPresentHeadMinifiesOnce(t *testing.T) {
	r := New(Config{Base: t.TempDir(), JitCSS: true, ThemeMode: "force", DefaultMinify: true})
	built := r.PresentHead(`<p class="p-2 text-brand"><i class="icon-coffee"></i><i class="logo-github"></i>x</p>`)
	icons := sheetOf(built, "icons")
	written := sheetOf(New(Config{Base: t.TempDir(), JitCSS: true, ThemeMode: "force"}).PresentHead(`<p class="p-2 text-brand"><i class="icon-coffee"></i><i class="logo-github"></i>x</p>`), "icons")
	if !strings.Contains(icons, ".icon-coffee") || !strings.Contains(icons, "var(--i)center") || len(icons) >= len(written) ||
		!strings.Contains(written, "var(--i) center") {
		t.Errorf("the CSS must be minified (the development file keeps the space):\n%.300s\n%.300s", icons, written)
	}
	if got := names(built); got != "icons=icons logo=logo" {
		t.Errorf("minified, the stylesheets are still one per generator: got %q", got)
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
	control := New(Config{Base: t.TempDir(), JitCSS: true, ThemeMode: "force"}).PresentHead(`<p class="p-2 text-brand">x</p>`)
	prepaint := control.Head[:strings.Index(control.Head, "</script>")+len("</script>")]
	if !strings.HasPrefix(prepaint, `<script data-kitwork-generated="appearance">`) || !strings.Contains(built.Head, prepaint) {
		t.Errorf("the minified head must keep the canonical pre-paint:\n%s", built.Head)
	}
}

// attributeValue reads a start tag's attribute quoted, single-quoted or bare, whole-name and without
// regard to case; a name that only ends or starts like it is another attribute.
func TestAttributeValue(t *testing.T) {
	for attributes, want := range map[string]string{
		` data-kitwork-generated="icons"`:                                "icons",
		` data-kitwork-generated='logo'`:                                 "logo",
		` data-kitwork-generated=fonts`:                                  "fonts",
		` id=x data-kitwork-generated = fonts media=all`:                 "fonts",
		` DATA-KITWORK-GENERATED="icons"`:                                "icons",
		` data-x-data-kitwork-generated="icons"`:                         "",
		` data-kitwork-generated-by="icons" data-kitwork-generated=logo`: "logo",
		` data-kitwork-generated`:                                        "",
		``:                                                               "",
		` media="all"`:                                                   "",
	} {
		if got := attributeValue(attributes, "data-kitwork-generated"); got != want {
			t.Errorf("attributeValue(%q) = %q, want %q", attributes, got, want)
		}
	}
}

// IconsStylesheet is the icons file of a response whose data named icons: every known icon of the
// set (the page's and the data's), layered like the page's own icons file, nothing else; an unknown
// name adds nothing and a set with no known icon is no file.
func TestIconsStylesheet(t *testing.T) {
	r := New(Config{Base: t.TempDir(), JitCSS: true})
	css := string(r.IconsStylesheet([]string{"coffee", "sun", "not-an-icon"}))
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
	if r.IconsStylesheet([]string{"not-an-icon"}) != nil || r.IconsStylesheet(nil) != nil {
		t.Error("no known icon, no file")
	}
	// the same set in another order is the same bytes: the file is named by them
	if string(r.IconsStylesheet([]string{"sun", "coffee"})) != string(r.IconsStylesheet([]string{"coffee", "sun"})) {
		t.Error("one set, one file, whatever the order")
	}
}

// A <style> no role names stays in the head where it stood: CSS a .ui page does not expect (a template
// pass run by mistake) is seen by the tests that refuse a <style> in the head, never dropped in
// silence. CONTROL: the icons block next to it is taken out as its file.
func TestTakeStylesheetsKeepsAnUnknownBlock(t *testing.T) {
	rest, at, sheets := takeStylesheets(`<meta x><style data-kitwork-generated="css">.p-2{}</style><style data-kitwork-generated="icons">.icon-a{}</style><script></script>`, "")
	if rest != `<meta x><style data-kitwork-generated="css">.p-2{}</style><script></script>` || at != len(`<meta x><style data-kitwork-generated="css">.p-2{}</style>`) {
		t.Errorf("the unknown block stays where it stood, the place is the icons': %q at %d", rest, at)
	}
	if len(sheets) != 1 || sheets[0].Name != "icons" || sheets[0].CSS != ".icon-a{}" {
		t.Errorf("CONTROL: the icons block is its file: %+v", sheets)
	}
}
