package ui

import (
	"strings"
	"testing"
)

// The skeleton lists what the page CAN use, every branch at once, so the presentation passes run over
// it once instead of over every response. A render shows one branch; the skeleton shows them all.
func TestSkeletonListsEveryBranch(t *testing.T) {
	files := map[string]string{"/site/content.ui": `<content>
  <text if="open" color="brand+15">mở</text>
  <text if="!open" color="danger+15">đóng</text>
  <list><item for="x of xs" padding="2">{{ x }}</item><empty color="ink-30">trống</empty></list>
  <icon value="{{ dark ? 'sun' : 'moon' }}" alt=""></icon>
  <badge color="{{ order.tone }}"><text>{{ order.label }}</text></badge>
</content>`}
	s := memorySite("", files)
	s.Reload = true
	page, err := s.Compile("/site")
	if err != nil {
		t.Fatal(err)
	}
	skeleton := page.Skeleton()
	for _, want := range []string{"color-brand+15", "color-danger+15", "padding-2", "color-ink-30", "icon-sun", "icon-moon"} {
		if !strings.Contains(skeleton, want) {
			t.Errorf("the skeleton misses %q:\n%s", want, skeleton)
		}
	}
	if got := strings.Join(page.Icons(), " "); got != "moon sun" {
		t.Errorf("the page's icons are every value the expression spells out, got %q", got)
	}
	// a colour only the data knows is not in the skeleton: every class it can make is in the page's
	// stylesheet already, the palette's whole domain
	if strings.Contains(skeleton, "color-warning") {
		t.Errorf("the skeleton cannot know a colour from data:\n%s", skeleton)
	}
	if css := string(page.Stylesheet().CSS); !strings.Contains(css, `.color-warning:where(.badge){--kitwork-tint:color-mix(in oklab,var(--warning) 15%,var(--canvas));color:var(--kitwork-badge-warning)}`) ||
		!strings.Contains(css, `--kitwork-badge-warning:#`) {
		t.Errorf("the badge's colour from data has every declared token in the page's stylesheet:\n%s", css)
	}

	// control: one render shows one branch only, so a head built from a render would miss the other
	body := page.Render(Request{Path: "/", Scope: map[string]any{"open": true, "dark": false, "xs": []any{}}})
	if strings.Contains(body, "color-danger") || strings.Contains(body, "icon-sun") || strings.Contains(body, "padding-2") {
		t.Errorf("a render should show one branch only; this test proves nothing otherwise:\n%s", body)
	}
}

// Document puts the head built once into <head>. It asks Request.IconsHead for another head only when
// this request's data named an icon the skeleton could not list, with those icons' names; a colour from
// data never asks (its class is in the page's stylesheet). A value from data that is no value of the
// attribute never becomes a class (data must not write CSS that loads or runs something), and while
// developing the author hears of it, once.
func TestDocumentHeadAndDataIcons(t *testing.T) {
	files := map[string]string{"/site/content.ui": `<content>
  <badge color="{{ order.tone }}"><text>x</text></badge>
  <icon value="{{ dark ? 'sun' : 'moon' }}" alt=""></icon>
  <icon value="{{ order.icon }}" alt=""></icon>
</content>`}
	s := memorySite("", files)
	s.Reload = true
	page, err := s.Compile("/site")
	if err != nil {
		t.Fatal(err)
	}
	var asked [][]string
	var warned []string
	request := func(tone, icon string, dark bool) Request {
		return Request{
			Path: "/",
			Head: `<link rel="stylesheet" href="/_kitwork/icons.HEAD.css" data-kitwork-generated="icons">`,
			IconsHead: func(icons []string) string {
				asked = append(asked, icons)
				return "<!-- ICONS " + strings.Join(icons, " ") + " -->"
			},
			Warn:  func(message string) { warned = append(warned, message) },
			Scope: map[string]any{"order": map[string]any{"tone": tone, "icon": icon}, "dark": dark},
		}
	}

	doc := page.Document(request("warning", "sun", true))
	if !strings.Contains(doc, `icons.HEAD.css`) || strings.Index(doc, "icons.HEAD.css") > strings.Index(doc, "</head>") {
		t.Errorf("the head built once must sit in <head>:\n%s", doc)
	}
	if len(asked) != 0 {
		t.Fatalf("a colour from data and an icon the page spells out ask for nothing, asked %v", asked)
	}
	mustContain(t, doc, `<span class="badge flex-row color-warning">`)
	mustNotContain(t, doc, "<style")

	doc = page.Document(request("warning", "coffee", false))
	if len(asked) != 1 || strings.Join(asked[0], " ") != "coffee" {
		t.Fatalf("IconsHead must be asked for exactly the data's own icons, got %v", asked)
	}
	if !strings.Contains(doc, "<!-- ICONS coffee -->") || strings.Contains(doc, "icons.HEAD.css") || strings.Index(doc, "ICONS") > strings.Index(doc, "</head>") {
		t.Errorf("the head IconsHead gives replaces the one built once, in <head>:\n%s", doc)
	}

	// a value from data that is no token is dropped, never handed on as a class, and named once
	asked = nil
	for i := 0; i < 2; i++ {
		doc = page.Document(request("[url(https://x.test/a.png)]", "bad name", false))
	}
	if strings.Contains(doc, "url(") || strings.Contains(doc, "bad name\"") || len(asked) != 0 {
		t.Errorf("an arbitrary value from data became a class (asked %v):\n%s", asked, doc)
	}
	mustContain(t, doc, `<span class="badge flex-row">`)
	if len(warned) != 2 || !strings.Contains(warned[0], `content.ui:2:10: color="{{ order.tone }}" cho "[url(https://x.test/a.png)]": không phải giá trị color nhận; không có class`) ||
		!strings.Contains(warned[1], `content.ui:4:9: value="{{ order.icon }}" cho "bad name": tên icon chỉ gồm chữ thường`) {
		t.Errorf("each value from data that makes no class is named once, at its attribute: %q", warned)
	}
	// control: a token from data is a class, and nothing is asked for it
	asked = nil
	doc = page.Document(request("success", "", false))
	mustContain(t, doc, `<span class="badge flex-row color-success">`)
	if len(asked) != 0 {
		t.Errorf("a token from data needs no CSS of its own, asked %v", asked)
	}
}

// An icon the page shows anywhere is in its icons stylesheet already: data that names it asks for no
// head of its own. Control: an icon the page never shows is asked for.
func TestDataIconThePageShowsIsCovered(t *testing.T) {
	files := map[string]string{"/site/content.ui": `<content>
  <text color="brand+15">tĩnh</text>
  <text color="{{ tone }}">động</text>
  <icon value="coffee" alt=""></icon>
  <icon value="{{ glyph }}" alt=""></icon>
</content>`}
	s := memorySite("", files)
	s.Reload = true
	page, err := s.Compile("/site")
	if err != nil {
		t.Fatal(err)
	}
	var asked []string
	render := func(tone, glyph string) {
		asked = nil
		page.Document(Request{Path: "/", Head: "<!-- head -->",
			IconsHead: func(icons []string) string { asked = append(asked, icons...); return "" },
			Scope:     map[string]any{"tone": tone, "glyph": glyph}})
	}
	render("brand", "coffee")
	if len(asked) != 0 {
		t.Errorf("an icon the page shows itself must not be asked for again, asked %v", asked)
	}
	render("danger", "sun")
	if strings.Join(asked, " ") != "sun" {
		t.Errorf("control: an icon the page never shows is the data's own, asked %v", asked)
	}
}

// resultLiterals reads only what an expression spells out.
func TestResultLiterals(t *testing.T) {
	for source, want := range map[string]string{
		"a ? 'x' : b ? 'y' : 'z'": "x y z",
		"a ?? 'x'":                "",
		"a && 'on'":               "on",
		"'fixed'":                 "fixed",
		"x":                       "",
		"'a' + b":                 "",
	} {
		e, err := compileExpr(source, &Node{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		got, ok := resultLiterals(e.ir)
		if want == "" {
			if ok {
				t.Errorf("%s: want unknown, got %v", source, got)
			}
			continue
		}
		if !ok || strings.Join(got, " ") != want {
			t.Errorf("%s: got %v (%v), want %s", source, got, ok, want)
		}
	}
}
