package ui

import (
	"strings"
	"testing"

	"github.com/kitwork/engine/ui/stylesheet"
)

// wrap on a text says how its words break into lines (KITWORK_UI §7, "wrap trên chữ"): true breaks as
// usual, false keeps a phrase together, balance evens the lines, pretty avoids one lonely last word.

func TestTextWrapValues(t *testing.T) {
	html, css := renderCSS(t, `<content><text heading="1" size="huge">Build with one codebase. <text wrap="false" color="brand">Ship and grow everywhere.</text></text>`+
		`<text wrap="balance">a</text><text wrap="pretty">b</text><text wrap="true">c</text></content>`, nil)
	mustContain(t, html,
		`<span class="wrap-false color-brand">Ship and grow everywhere.</span></h1>`,
		`<p class="wrap-balance">a</p>`,
		`<p class="wrap-pretty">b</p>`,
		`<p class="wrap-true">c</p>`)
	// on a text that lays out no children: false keeps the phrase whole, balance and pretty are how
	// its lines break, true breaks as usual
	mustContain(t, css,
		`.wrap-false:where(:not(.flex-row,.flex-column,.grid)){display:inline-block}`,
		`.wrap-balance{text-wrap:balance}`,
		`.wrap-pretty{text-wrap:pretty}`,
		`.wrap-true:where(:not(.flex-row,.flex-column,.grid)){text-wrap:wrap}`)
}

// On a frame, wrap keeps its meaning: the children go to the next line. The text's meaning is scoped
// to an element that lays out no children, so it never reaches the frame.
func TestFrameWrapStaysFlexWrap(t *testing.T) {
	html, css := renderCSS(t, `<content><box flex="row" wrap="true"><text>a</text></box></content>`, nil)
	mustContain(t, html, `<div class="flex-row wrap-true">`)
	mustContain(t, css, `.wrap-true{flex-wrap:wrap}`)
	mustNotContain(t, css, "text-wrap")
}

func TestTextWrapErrors(t *testing.T) {
	cases := []struct {
		name, content, at, says string
	}{
		{"unknown word on a text", `<content><text wrap="tight">a</text></content>`,
			"content.ui:1:16", "true, false, balance hoặc pretty"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mustFail(t, map[string]string{"/site/content.ui": c.content}, c.at, c.says)
		})
	}
}

// Unwritten, reboot.css balances headings and keeps paragraphs from ending on one word, weightless
// (in the elements layer) so that wrap written on a text wins.
func TestRebootBreaksLinesWell(t *testing.T) {
	reboot := rebootCSS()
	for _, rule := range []string{":where(h1,h2,h3,h4,h5,h6){font-size:1rem;font-weight:600;line-height:1.25;text-wrap:balance}", ":where(p){text-wrap:pretty}"} {
		if !strings.Contains(reboot, rule) {
			t.Errorf("reboot.css lacks %q", rule)
		}
	}
}

// rebootCSS is reboot.<hash>.css as served: compact, the layer statement first.
func rebootCSS() string { return string(stylesheet.Reboot().CSS) }
