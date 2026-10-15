package ui

import "testing"

// width="fit" is as wide as what it holds, also in a column that stretches its children; width="full"
// is the whole width of its parent (KITWORK_UI §7, "width").

func TestWidthFitAndFull(t *testing.T) {
	html, css := renderCSS(t, `<content><card width="fit" padding="4"><text>Only as wide as these words</text></card>`+
		`<box flex="row"><box width="full"><text>All of the row</text></box></box>`+
		`<image value="/hero.svg" alt="" width="full"></image></content>`, nil)
	mustContain(t, html,
		`<div class="card flex-column width-fit padding-4">`,
		`<div class="flex-column width-full">`,
		`<img class="width-full"`)
	mustContain(t, css, `.width-fit{inline-size:fit-content}`, `.width-full{inline-size:100%}`)
	// control: unwritten, nothing is added and a card in a column stretches as before
	html, css = renderCSS(t, `<content><card padding="4"><text>a</text></card></content>`, nil)
	mustNotContain(t, html, "width-")
	mustNotContain(t, css, "inline-size")
}

func TestWidthTakesAWidthPrefix(t *testing.T) {
	html, css := renderCSS(t, `<content><box width="full" medium:width="fit"><text>a</text></box></content>`, nil)
	mustContain(t, html, `class="flex-column width-full medium:width-fit"`)
	mustContain(t, css, "@media (min-width:768px){\n"+`.medium\:width-fit{inline-size:fit-content}`)
}

func TestWidthWordsAreChecked(t *testing.T) {
	mustFail(t, map[string]string{"/site/content.ui": `<content><box width="giant"><text>a</text></box></content>`},
		"content.ui:1:15", "width nhận fit (ôm nội dung) hoặc full")
}
