package ui

import (
	"strings"
	"testing"
)

// On a heading, size is a step of the heading scale (KITWORK_UI §7): the same five words as body text,
// sized for a heading, so an h1 can be larger than its default without changing what it is. The class
// is the attribute as written (size-huge); its body rule is the body step, and the heading's steps are
// scoped to h1–h6 in the same class, growing with the screen.

func TestHeadingSizeUsesTheHeadingScale(t *testing.T) {
	html, css := renderCSS(t, `<content><text heading="1" size="huge">Ship everywhere.</text></content>`, nil)
	mustContain(t, html, `<h1 class="size-huge">`)
	mustContain(t, css,
		`.size-huge:where(h1,h2,h3,h4,h5,h6){font-size:3rem;letter-spacing:-.05em}`,
		"@media (min-width:640px){\n"+`.size-huge:where(h1,h2,h3,h4,h5,h6){font-size:3.75rem}`,
		"@media (min-width:1024px){\n"+`.size-huge:where(h1,h2,h3,h4,h5,h6){font-size:4.5rem}`)

	_, css = renderCSS(t, `<content><text heading="1" size="large">Build</text></content>`, nil)
	mustContain(t, css, `.size-large:where(h1,h2,h3,h4,h5,h6){font-size:2.25rem;letter-spacing:-.045em}`,
		"@media (min-width:640px){\n"+`.size-large:where(h1,h2,h3,h4,h5,h6){font-size:3rem}`)

	html, css = renderCSS(t, `<content><text heading="2" size="mini">The principle</text></content>`, nil)
	mustContain(t, html, `<h2 class="size-mini">`)
	mustContain(t, css, `.size-mini:where(h1,h2,h3,h4,h5,h6){font-size:1.125rem;letter-spacing:-.02em}`)
}

// Unwritten, a level's look is reboot.css's: h1 the medium step, h2 small, h3 mini, h4–h6 the body
// size set apart by weight; every heading semibold at 1.25. No class.
func TestHeadingLevelsKeepTheirDefaultSize(t *testing.T) {
	html := render(t, `<content><text heading="1">a</text><text heading="2">b</text><text heading="3">c</text><text heading="4">d</text></content>`, nil)
	mustContain(t, html, `<h1>a</h1>`, `<h2>b</h2>`, `<h3>c</h3>`, `<h4>d</h4>`)
	reboot := rebootCSS()
	mustContain(t, reboot,
		`:where(h1,h2,h3,h4,h5,h6){font-size:1rem;font-weight:600;line-height:1.25;text-wrap:balance}`,
		`:where(h1){font-size:1.875rem;letter-spacing:-0.04em}`,
		`:where(h2){font-size:1.5rem;letter-spacing:-0.03em}`,
		`:where(h3){font-size:1.125rem;letter-spacing:-0.02em}`,
		`@media (min-width:640px){:where(h1){font-size:2.25rem}}`)
}

// Body text keeps the body scale: huge is 24px there, and the heading steps never reach a <p>.
func TestBodyTextKeepsTheBodyScale(t *testing.T) {
	html, css := renderCSS(t, `<content><text size="huge">Big words</text></content>`, nil)
	mustContain(t, html, `<p class="size-huge">Big words</p>`)
	mustContain(t, css, `.size-huge{font-size:1.5rem}`)
	mustNotContain(t, css, "4.5rem", ":where(h1")
}

// A width-prefixed size on a heading takes the step's full size from that width on.
func TestHeadingSizeWithAWidthPrefix(t *testing.T) {
	html, css := renderCSS(t, `<content><text heading="1" large:size="huge">Hero</text></content>`, nil)
	mustContain(t, html, `<h1 class="large:size-huge">`)
	mustContain(t, css, "@media (min-width:1024px){\n"+`.large\:size-huge{font-size:1.5rem}`+"\n"+
		`.large\:size-huge:where(h1,h2,h3,h4,h5,h6){font-size:4.5rem;letter-spacing:-.05em}`)
	if strings.Contains(css, "3rem") || strings.Contains(css, "3.75rem") {
		t.Errorf("from large the step's largest size, never its smaller ones:\n%s", css)
	}
}
