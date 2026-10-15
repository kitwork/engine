package render

import (
	"strings"
	"testing"
)

// A template page of a site that moves without a reload gets the progressbar component at the top of
// <body>, unless it placed one; a page with no <body> is left alone.
func TestTemplatePageGetsTheProgressbar(t *testing.T) {
	page := `<!doctype html><html lang="en"><head><title>x</title></head><body class="a"><main>x</main></body></html>`
	out := withProgressbar(page)
	host := strings.Index(out, `data-kitwork-component="progressbar"`)
	if host < 0 {
		t.Fatalf("no progressbar on a Drive page:\n%s", out)
	}
	if !(strings.Index(out, `<body class="a">`) < host && host < strings.Index(out, "<main>")) {
		t.Errorf("the progressbar goes first in <body>:\n%s", out)
	}
	for _, want := range []string{`data-kitwork-style="top: top"`, `data-kitwork-bind="show: active; aria-valuenow: value"`,
		`data-kitwork-style="width: value + '%'"`, `aria-label="Loading the next page"`,
		`class="h-[1px] [background-color:var(--kitwork-progress,var(--kitwork-brand))] transition-all"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in\n%s", want, out)
		}
	}
	if withProgressbar(out) != out {
		t.Error("a page that has a progressbar must not get a second")
	}
	if vi := withProgressbar(strings.Replace(page, `lang="en"`, `lang="vi"`, 1)); !strings.Contains(vi, `aria-label="Đang chuyển trang"`) {
		t.Errorf("a Vietnamese page names the bar in Vietnamese:\n%s", vi)
	}
	// a > inside {{ }} in the start tag does not end it
	templated := `<html><head></head><body {{ if count > 1 }}data-many="yes"{{ end }}><main>x</main></body></html>`
	if got := withProgressbar(templated); !strings.Contains(got, `{{ end }}><div class="fixed`) {
		t.Errorf("the progressbar must follow the whole start tag:\n%s", got)
	}
	// a > inside a quoted value does not end it either, and characters whose lower case has another byte
	// length (İ, the Kelvin sign) or an invalid byte before <body> do not shift where it goes
	for _, start := range []string{
		`<body class="[&>*]:m-0">`,
		`<body data-kit-class="{ wide: width > 600 }">`,
		`<body title='a > b'>`,
	} {
		for _, before := range []string{"", "<title>İİİ K\xff</title>"} {
			doc := `<html><head>` + before + `</head>` + start + `<main>x</main></body></html>`
			got := withProgressbar(doc)
			if !strings.Contains(got, start+`<div class="fixed`) {
				t.Errorf("the progressbar must follow the whole start tag %s (before: %q):\n%s", start, before, got)
			}
			if strings.Count(got, `data-kitwork-component="progressbar"`) != 1 {
				t.Errorf("one progressbar:\n%s", got)
			}
		}
	}
	// control: the author's own progressbar, plain or pinned, in either form — the hand-written
	// data-kit-component, or the grouped data-kitwork-component a Kitwork UI page carries, with or without
	// an alias — and a fragment without <body>, stay as they are
	for _, keep := range []string{
		`<html><body><div data-kit-component="progressbar"></div><main>x</main></body></html>`,
		`<html><body><div data-kit-component="progressbar@1.0.0"></div></body></html>`,
		`<html><body><div data-kitwork-component="progressbar"></div><main>x</main></body></html>`,
		`<html><body><div data-kitwork-component="progressbar@1.0.0: $bar"></div></body></html>`,
		`<html><body><div data-kitwork-component="progressbar: $bar"></div></body></html>`,
		`<main>a fragment</main>`,
		`<html><body`, // never closed
	} {
		if got := withProgressbar(keep); got != keep {
			t.Errorf("must be left alone:\n%s\ngot\n%s", keep, got)
		}
	}
}

// A template page of a site that moves without a reload gets the reader too: Drive says the new title
// through it. A page that placed one, in either form, is left alone.
func TestTemplatePageGetsTheReader(t *testing.T) {
	page := `<!doctype html><html><head><title>x</title></head><body class="a"><main>x</main></body></html>`
	out := withReader(page)
	if !strings.Contains(out, `<body class="a"><div data-kitwork-component="reader" data-kitwork-bind="text: text" aria-live="polite"`) {
		t.Fatalf("no reader at the top of <body>:\n%s", out)
	}
	if withReader(out) != out {
		t.Error("a page that has a reader must not get a second")
	}
	own := strings.Replace(page, "<main>", `<div data-kit-component="reader"></div><main>`, 1)
	if withReader(own) != own {
		t.Error("a template page's own reader host counts")
	}
	if withReader("<p>no body</p>") != "<p>no body</p>" {
		t.Error("a page without <body> is left alone")
	}
}
