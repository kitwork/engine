package ui

import (
	"path/filepath"
	"testing"
)

// The bar's name, when page.ui writes no alt, is in the page's language like every word the engine
// writes itself (the new-tab note, the terminal's status): Vietnamese for vi (vi-VN…) and for a page
// that names none, English for any other — an English page said "Đang chuyển trang" until 12/10/2026.
// An alt the author wrote is the name in every language.
func TestProgressbarNameIsInThePageLanguage(t *testing.T) {
	compile := func(files map[string]string) *Page {
		t.Helper()
		s := memorySite("", files)
		page, err := s.Compile(filepath.Join(s.Root, "."))
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		return page
	}
	bare := compile(map[string]string{
		"/site/page.ui":    `<page><header /><content /></page>`,
		"/site/header.ui":  `<header><text>Kitwork</text></header>`,
		"/site/content.ui": `<content><text>x</text></content>`,
	})
	for language, want := range map[string]string{
		"":      `aria-label="Đang chuyển trang"`,
		"vi":    `aria-label="Đang chuyển trang"`,
		"vi-VN": `aria-label="Đang chuyển trang"`,
		"en":    `aria-label="Loading the next page"`,
		"en-US": `aria-label="Loading the next page"`,
		"fr":    `aria-label="Loading the next page"`,
	} {
		html := bare.Render(Request{Path: "/", Language: language})
		mustContain(t, html, want, `data-kitwork-component="progressbar"`, `<div class="thickness-1 background-brand"`)
	}
	// written in page.ui with no alt, the bar is named the same way
	written := compile(map[string]string{
		"/site/page.ui":    `<page><box component="progressbar" color="ink-30" /><content /></page>`,
		"/site/content.ui": `<content><text>x</text></content>`,
	})
	mustContain(t, written.Render(Request{Path: "/", Language: "en"}), `aria-label="Loading the next page"`, `background-ink-30`)
	mustContain(t, written.Render(Request{Path: "/", Language: "vi"}), `aria-label="Đang chuyển trang"`)
	// an alt the author wrote is the name whatever the language
	named := compile(map[string]string{
		"/site/page.ui":    `<page><box component="progressbar" alt="Opening the page" /><content /></page>`,
		"/site/content.ui": `<content><text>x</text></content>`,
	})
	for _, language := range []string{"", "vi", "en"} {
		html := named.Render(Request{Path: "/", Language: language})
		mustContain(t, html, `aria-label="Opening the page"`)
		mustNotContain(t, html, "Đang chuyển trang", "Loading the next page")
	}
}
