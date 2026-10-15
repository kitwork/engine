package ui

import "testing"

// An icon is decoration unless it has an alt (KITWORK_UI §7, "Tên đọc được"); what must have a name is
// the button, link or badge an icon may be the only content of.

func TestIconWithoutAltIsDecoration(t *testing.T) {
	html := render(t, `<content><button><icon value="plus"></icon><text>Add</text></button></content>`, nil)
	mustContain(t, html, `<i class="icon-plus" aria-hidden="true">`)
	// control: an alt makes it an image with a name
	html = render(t, `<content><button><icon value="x" alt="Close"></icon></button></content>`, nil)
	mustContain(t, html, `role="img" aria-label="Close"`)
	mustNotContain(t, html, `aria-hidden="true"`)
}

func TestElementsThatMustHaveAName(t *testing.T) {
	cases := []struct {
		name, content, at, says string
	}{
		{"button with only an icon", `<content><button><icon value="x"></icon></button></content>`,
			"content.ui:1:10", "<button> chỉ có icon nên chưa có tên"},
		{"an empty alt is not a name", `<content><button><icon value="x" alt=""></icon></button></content>`,
			"content.ui:1:10", "viết alt trên icon"},
		{"link with only an image", `<content><link value="/"><image value="/logo.svg" alt=""></image></link></content>`,
			"content.ui:1:10", "<link> chỉ có ảnh nên chưa có tên"},
		{"empty button", `<content><button click="go()"></button></content>`,
			"content.ui:1:10", "<button> cần tên đọc được"},
		{"badge with only an icon", `<content><badge color="success"><icon value="point-fill"></icon></badge></content>`,
			"content.ui:1:10", "<badge> chỉ có icon cần alt"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			compileWarns(t, map[string]string{"/site/content.ui": c.content}, c.at, c.says) // accessibility: a warning (decision D)
		})
	}
}

func TestNamesThatCount(t *testing.T) {
	html := render(t, `<content>`+
		`<button><icon value="x" alt="Close"></icon></button>`+ // the icon's alt is the name
		`<link value="/"><image value="/logo.svg" alt=""></image><text>Kitwork</text></link>`+ // words beside it
		`<button><text>{{ label }}</text></button>`+ // words from data
		`<badge color="success" alt="Online"><icon value="point-fill"></icon></badge>`+ // the badge's own alt
		`</content>`, map[string]any{"label": "Save"})
	mustContain(t, html, `aria-label="Close"`, `Kitwork`, `Save`, `aria-label="Online"`)
}
