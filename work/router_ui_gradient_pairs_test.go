package work

import (
	"strings"
	"testing"
)

// router.gradients takes a stop whose colour is a { light, dark } pair (decision D2 of 13/10/2026), as
// router.colors takes a token: the page's stylesheet sets the pair's custom property in light and again
// for the dark look, and the image names it; a page writes the site's gradient lighter too (sky-70,
// decision D3), and the page backdrops (dusk) need no declaring. A pair that cannot be read stops the
// boot naming the gradient, the stop and what to write: a side missing, a key other than light and dark
// (drak would leave the stop without its dark), a side that is no string, a side that is a token, an
// empty pair; and a site's gradient is never named like a backdrop. CONTROL: the colour stop beside the
// pair is still the token's var().
func TestRouterGradientsTakePairs(t *testing.T) {
	colours := `router.colors({ brand: "#c8742c", canvas: { light: "#f6f3ee", dark: "#141110" } });`
	sky := func(first string) string {
		return colours + "\n" + `router.gradients({ sky: { type: "linear", direction: "to-bottom", stops: [ { color: ` + first + `, at: 0 }, { color: "canvas", at: 100 } ] } });`
	}
	files := withRootRouter(t, sky(`{ light: "#849cdd", dark: "#202f5e" }`), map[string]string{
		"localhost/content.ui": `<content><box background="sky" padding="4"><text>Quán</text></box><box background="sky-70" padding="4"><text>Trà</text></box><box background="dusk"><text>Tối</text></box></content>`,
	})
	tenant := buildUISite(t, files)
	page := pageWithStylesheet(t, tenant, "http://localhost/")
	for _, want := range []string{
		":root{--kitwork-gradient-dusk-1:#ca92b9;", "--kitwork-gradient-sky-1:#849cdd}",
		"@media screen{:root[data-theme=dark]{--kitwork-gradient-dusk-1:#4c2041;", "--kitwork-gradient-sky-1:#202f5e}}",
		".background-sky{background-image:linear-gradient(to bottom,var(--kitwork-gradient-sky-1) 0%,var(--canvas) 100%)}",
		`.background-sky-70{background-image:linear-gradient(to bottom,color-mix(in oklab,var(--kitwork-gradient-sky-1) 30%,var(--canvas)) 0%,color-mix(in oklab,var(--canvas) 30%,var(--canvas)) 100%);`,
		".background-dusk{background-image:linear-gradient(to bottom right,var(--kitwork-gradient-dusk-1) 0%,",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("/ is missing %q:\n%s", want, linesWith(page, "gradient"))
		}
	}

	for first, want := range map[string]string{
		`{ light: "#849cdd" }`:                    `router.gradients: "sky" điểm dừng 1: cặp màu thiếu dark: color { light, dark } viết cả hai`,
		`{ light: "#849cdd", drak: "#202f5e" }`:   `router.gradients({ sky: … }): điểm dừng 1: một cặp màu chỉ nhận light và dark, không có "drak"`,
		`{ light: "#849cdd", dark: 5 }`:           `router.gradients({ sky: … }): điểm dừng 1: dark của cặp màu là một chữ, "#rrggbb" hay một màu có tên`,
		`{ light: "#849cdd", dark: "brand" }`:     `router.gradients: "sky" điểm dừng 1: dark là "brand": một màu của cặp là "#rgb", "#rrggbb" hay một màu có tên`,
		`{ light: "#849cdd", dark: "navy-blue" }`: `dark là "navy-blue": navy-blue không phải tên màu: đó là tên Figma đặt cho navy`,
		`{}`: `router.gradients({ sky: … }): điểm dừng 1: cặp màu trống`,
		`{ light: "#849cdd", dark: "#202f5e" }, x`: `router.gradients({ sky: … }): điểm dừng 1 nhận color và at, không có "x"`,
	} {
		if strings.HasSuffix(first, ", x") {
			first = strings.TrimSuffix(first, ", x") + `, x: 1`
		}
		files := withRootRouter(t, sky(first), nil)
		if _, err := bootUISite(t, files, true); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s:\n want a boot error naming %q\n got %v", first, want, err)
		}
	}
	files = withRootRouter(t, colours+"\n"+`router.gradients({ dusk: { type: "linear", direction: "to-end", stops: [ { color: "brand", at: 0 }, { color: "ink", at: 100 } ] } });`, nil)
	if _, err := bootUISite(t, files, true); err == nil || !strings.Contains(err.Error(), `router.gradients: "dusk" là tên một nền trang của Kitwork, có sẵn`) {
		t.Errorf("a site's gradient named like a backdrop: %v", err)
	}
}
