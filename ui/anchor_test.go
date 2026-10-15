package ui

import (
	"strings"
	"testing"
)

// An id is where a link lands (KITWORK_UI §7, "Neo: id"): a fixed word, once per page, on the element
// that carries it.

func TestAnchorIsWrittenOnTheElement(t *testing.T) {
	html := render(t, `<content><section id="quickstart" gap="2"><text heading="2" id="check-before-serving">Check</text>`+
		`<link value="#quickstart">Back to the top of the section</link></section></content>`, nil)
	mustContain(t, html,
		`<section class="flex-column gap-2" id="quickstart">`,
		`id="check-before-serving">Check</h2>`,
		`href="#quickstart"`)
	// control: without the attribute there is no id at all
	html = render(t, `<content><section gap="2"><text heading="2">Check</text></section></content>`, nil)
	mustNotContain(t, html, ` id="`)
}

func TestAnchorOnBoxQuoteAndAccordion(t *testing.T) {
	html := render(t, `<content><box id="surfaces"><text>a</text></box><quote id="motto"><text>One process.</text></quote>`+
		`<accordion id="faq"><text click="toggle()">Question</text><text>Answer</text></accordion></content>`, nil)
	mustContain(t, html, `<div class="flex-column" id="surfaces">`, `<blockquote class="flex-column" id="motto">`, `<details id="faq">`)
}

func TestAnchorErrors(t *testing.T) {
	cases := []struct {
		name, content, at, says string
	}{
		{"from data", `<content><section id="{{ slug }}"><text>a</text></section></content>`,
			"content.ui:1:19", "id là chữ cố định"},
		{"starts with a digit", `<content><section id="2-steps"><text>a</text></section></content>`,
			"content.ui:1:19", "bắt đầu bằng chữ"},
		{"double hyphen", `<content><section id="get--started"><text>a</text></section></content>`,
			"content.ui:1:19", "chỉ gồm chữ thường"},
		{"engine prefix", `<content><section id="kitwork-field-1"><text>a</text></section></content>`,
			"content.ui:1:19", "kitwork- là của engine"},
		{"on its own for", `<content><list><item for="step of steps; key: step.id" id="step"><text>a</text></item></list></content>`,
			"content.ui:1:56", "nằm trong for"},
		{"inside a for", `<content><list><item for="step of steps; key: step.id"><text id="title">a</text></item></list></content>`,
			"content.ui:1:62", "nằm trong for"},
		{"twice", `<content><section id="faq"><text>a</text></section><box id="faq"><text>b</text></box></content>`,
			"content.ui:1:57", "đã dùng ở content.ui:1:19"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mustFail(t, map[string]string{"/site/content.ui": c.content}, c.at, c.says)
		})
	}
}

// The page is one place: an id in a region and the same id in the content collide.
func TestAnchorIsUniqueAcrossThePage(t *testing.T) {
	files := map[string]string{
		"/site/page.ui":    `<page><header /><content /></page>`,
		"/site/header.ui":  `<header id="top"><text>Kitwork</text></header>`,
		"/site/content.ui": `<content><section id="top"><text>a</text></section></content>`,
	}
	mustFail(t, files, "content.ui:1:19", "đã dùng ở header.ui:1:9")
	// control: two different ids compile
	files["/site/content.ui"] = `<content><section id="intro"><text>a</text></section></content>`
	html := mustCompile(t, files)
	mustContain(t, html, `id="top"`, `id="intro"`)
}

// A layout called with for renders its file once per row: an id in it would repeat.
func TestAnchorInsideALayoutCalledWithFor(t *testing.T) {
	files := map[string]string{
		"/site/content.ui":   `<content><grid><step-card for="step of steps; key: step.id" /></grid></content>`,
		"/site/step-card.ui": `<card id="step"><text>{{ step.title }}</text></card>`,
	}
	got := compileError(t, files)
	if !strings.Contains(got, "nằm trong for") {
		t.Fatalf("want the for error, got %q", got)
	}
	// control: called once, the same file may carry its id
	files["/site/content.ui"] = `<content><step-card /></content>`
	files["/site/step-card.ui"] = `<card id="step"><text>One</text></card>`
	mustContain(t, mustCompile(t, files), `id="step"`)
}
