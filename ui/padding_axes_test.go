package ui

import "testing"

// padding-x and padding-y pad one axis (KITWORK_UI §7): x and y are .ui's axis words, as in
// scroll="x|y", so a pill can be wider than it is tall without a raw value. The class is the
// attribute as written; its rule pads one logical axis.
func TestPaddingAxes(t *testing.T) {
	files := map[string]string{
		"/site/content.ui": `<content><link value="/docs" padding-x="4" padding-y="2" medium:padding-x="6">Get started</link></content>`,
	}
	html, css := renderWithCSS(t, memorySite("", files), ".", "/", nil)
	mustContain(t, html, `<a class="padding-x-4 padding-y-2 medium:padding-x-6" href="/docs"`)
	mustContain(t, css, `.padding-x-4{padding-inline:1rem}`, `.padding-y-2{padding-block:.5rem}`,
		"@media (min-width:768px){\n"+`.medium\:padding-x-6{padding-inline:1.5rem}`)
	// control: plain padding is still both axes at once
	files["/site/content.ui"] = `<content><link value="/docs" padding="4">Get started</link></content>`
	html, css = renderWithCSS(t, memorySite("", files), ".", "/", nil)
	mustContain(t, html, `<a class="padding-4" href="/docs"`)
	mustContain(t, css, `.padding-4{padding:1rem}`)
	mustNotContain(t, html, "padding-x-")
}

// A button pads itself when the author writes nothing: reboot.css's padding, in the elements layer.
// An axis the author writes is a page-layer rule for that axis only, so the other axis keeps the
// default and the two never fight over the same side.
func TestButtonDefaultPaddingYieldsPerAxis(t *testing.T) {
	files := map[string]string{
		"/site/content.ui": `<content><button padding-x="6">Add</button></content>`,
	}
	html, css := renderWithCSS(t, memorySite("", files), ".", "/", nil)
	mustContain(t, html, `<button class="flex-row `+neutralClasses+` padding-x-6" type="button">Add</button>`)
	mustContain(t, css, `.padding-x-6{padding-inline:1.5rem}`)
	mustNotContain(t, css, "padding-block", "padding:")
	// the default both axes start from, under every page rule
	mustContain(t, rebootCSS(), `@layer elements{`, `padding:0.5rem 0.75rem;border:1px solid transparent;border-radius:0.375rem}`)
	// control: nothing written is no padding class, the default alone
	files["/site/content.ui"] = `<content><button>Add</button></content>`
	html, css = renderWithCSS(t, memorySite("", files), ".", "/", nil)
	mustContain(t, html, `<button class="flex-row `+neutralClasses+`" type="button">Add</button>`)
	mustNotContain(t, css, "padding")
}

// Widths are min-width: small,large:padding="1" holds from small, so large says nothing. The class
// names the narrowest, and while developing the author hears what to write instead, at the attribute.
// CONTROL: one width alone is no warning.
func TestWidthsJoinedByACommaWarn(t *testing.T) {
	s := memorySite("", map[string]string{"/site/content.ui": "<content>\n  <box small,large:padding=\"1\" medium:gap=\"2\"><text>x</text></box>\n</content>"})
	s.Reload = true
	page, err := s.Compile("/site")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	mustContain(t, page.Render(Request{Path: "/"}), `class="flex-column small:padding-1 medium:gap-2"`)
	var warned []string
	for _, w := range page.Warnings() {
		warned = append(warned, w.String())
	}
	if len(warned) != 1 || warned[0] != "content.ui:2:8: small,large:padding — độ rộng là từ đó trở lên, nên small đã gồm cả large: viết small:padding" {
		t.Errorf("one warning at the attribute naming what to write, got %q", warned)
	}
	// the sentence names only the wider widths, in the order written, never the narrowest again
	s.Read = memorySite("", map[string]string{"/site/content.ui": "<content>\n  <box large,small,medium:padding=\"1\"><text>x</text></box>\n</content>"}).Read
	if page, err = s.Compile("/site"); err != nil {
		t.Fatalf("compile: %v", err)
	}
	mustContain(t, page.Render(Request{Path: "/"}), `small:padding-1`)
	warned = nil
	for _, w := range page.Warnings() {
		warned = append(warned, w.String())
	}
	if len(warned) != 1 || warned[0] != "content.ui:2:8: large,small,medium:padding — độ rộng là từ đó trở lên, nên small đã gồm cả large, medium: viết small:padding" {
		t.Errorf("three widths: the narrowest is not in its own list, got %q", warned)
	}
	// the same width twice has no wider width to name: the sentence says it is repeated
	s.Read = memorySite("", map[string]string{"/site/content.ui": "<content>\n  <box small,small:padding=\"1\"><text>x</text></box>\n</content>"}).Read
	if page, err = s.Compile("/site"); err != nil {
		t.Fatalf("compile: %v", err)
	}
	warned = nil
	for _, w := range page.Warnings() {
		warned = append(warned, w.String())
	}
	if len(warned) != 1 || warned[0] != "content.ui:2:8: small,small:padding — độ rộng small viết lặp lại: viết small:padding" {
		t.Errorf("a repeated width, got %q", warned)
	}
	// with a state the width words are still compared as widths, and what to write keeps the state
	s.Read = memorySite("", map[string]string{"/site/content.ui": "<content>\n  <box small,large+hover:padding=\"1\"><text>x</text></box>\n</content>"}).Read
	if page, err = s.Compile("/site"); err != nil {
		t.Fatalf("compile: %v", err)
	}
	warned = nil
	for _, w := range page.Warnings() {
		warned = append(warned, w.String())
	}
	if len(warned) != 1 || warned[0] != "content.ui:2:8: small,large+hover:padding — độ rộng là từ đó trở lên, nên small đã gồm cả large: viết small+hover:padding" {
		t.Errorf("widths with a state, got %q", warned)
	}
	// CONTROL: one width alone is no warning
	s.Read = memorySite("", map[string]string{"/site/content.ui": "<content>\n  <box small:padding=\"1\"><text>x</text></box>\n</content>"}).Read
	if page, err = s.Compile("/site"); err != nil {
		t.Fatalf("compile: %v", err)
	}
	if list := page.Warnings(); len(list) != 0 {
		t.Errorf("one width: no warning, got %v", list)
	}
}
