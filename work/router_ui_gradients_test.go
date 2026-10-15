package work

import (
	"net/http"
	"strings"
	"testing"
)

// router.gradients through a whole site, wired like router.colors: the site root's gradients reach every
// page, a branch's are its folder's and the ones below, over the site's, the nearest folder winning per
// name, and the standard set needs no declaring. A page writes background="hero" and color="headline";
// page.<hash>.css has their images, the stops the colours the table writes (a token's var(), a named
// colour's hex). A branch whose gradient names a colour the route's palette does not have answers the
// error while developing, the place and the stop named; its sibling, which leaves the site's hero
// alone, is untouched. CONTROL: the site's own page keeps the site's hero, and a page that writes no
// gradient has no gradient rule.
func TestUISiteGradients(t *testing.T) {
	files := withRootRouter(t,
		`router.colors({ brand: "#c8742c" });
router.gradients({ hero: { type: "linear", direction: "to-bottom-end", stops: [ { color: "brand", at: 0 }, { color: "violet", at: 100 } ] } });`,
		map[string]string{
			"localhost/content.ui": `<content><box background="hero" padding="4"><text heading="1" color="headline">Quán</text></box></content>`,
			"localhost/quan-ly/router.work": `import { router } from "kitwork";
router.gradients({ hero: { type: "radial", position: "top-end", stops: [ { color: "brand-85", at: 0 }, { color: "brand-85/0", at: 70 } ] } });`,
			"localhost/quan-ly/content.ui":          `<content><box background="hero" padding="4"><text>Quản lý</text></box></content>`,
			"localhost/quan-ly/don-hang/content.ui": `<content><box background="hero" padding="4"><text>Đơn hàng</text></box></content>`,
			"localhost/kho/router.work": `import { router } from "kitwork";
router.gradients({ shelf: { type: "linear", direction: "to-end", stops: [ { color: "brnad", at: 0 }, { color: "paper", at: 100 } ] } });`,
			"localhost/kho/content.ui":  `<content><text>Kho</text></content>`,
			"localhost/tinh/content.ui": `<content><box background="hero"><text>Tĩnh</text></box></content>`,
		})
	tenant := buildUISite(t, files)

	home := pageWithStylesheet(t, tenant, "http://localhost/")
	for _, want := range []string{
		`class="flex-column background-hero padding-4"`, `color-headline`,
		`.background-hero{background-image:linear-gradient(to bottom right,var(--brand) 0%,#ee82ee 100%)}`,
		`.background-hero:where(:dir(rtl)){background-image:linear-gradient(to bottom left,var(--brand) 0%,#ee82ee 100%)}`,
		`-webkit-background-clip:text;background-clip:text;-webkit-text-fill-color:transparent`,
	} {
		if !strings.Contains(home, want) {
			t.Errorf("/ is missing %q", want)
		}
	}
	for _, path := range []string{"/quan-ly", "/quan-ly/don-hang"} {
		branch := pageWithStylesheet(t, tenant, "http://localhost"+path)
		if !strings.Contains(branch, `.background-hero{background-image:radial-gradient(at top right,color-mix(in oklab,var(--brand) 15%,var(--canvas)) 0%,color-mix(in srgb,color-mix(in oklab,var(--brand) 15%,var(--canvas)) 0%,transparent) 70%);`) ||
			strings.Contains(branch, "#ee82ee") {
			t.Errorf("%s takes the nearest folder's hero, the branch's radial one:\n%s", path, linesWith(branch, "background-hero"))
		}
	}
	if static := pageWithStylesheet(t, tenant, "http://localhost/tinh"); !strings.Contains(static, `.background-hero{background-image:linear-gradient(to bottom right,var(--brand) 0%,#ee82ee 100%)}`) {
		t.Errorf("/tinh keeps the site's hero:\n%s", linesWith(static, "background-hero"))
	}
	kho := serveUI(t, tenant, "http://localhost/kho")
	if body := htmlUnescape(kho.Body.String()); kho.Code != http.StatusInternalServerError ||
		!strings.Contains(body, `router.gradients: "shelf", điểm dừng 1: color="brnad": "brnad" không có trong bảng màu của trang; gần nhất: brand.`) {
		t.Errorf("/kho = %d %.600q, want the error naming the stop", kho.Code, body)
	}
}

// router.gradients is checked when it is declared, and a boot that meets a gradient that cannot be read
// stops naming it and what to write: the argument is one object of gradients; each an object of type,
// direction or position and stops; the stops an array of { color, at }; the words and the order of the
// stops (stylesheet.CheckGradient); the name, never a colour's or a standard gradient's. A name that is
// a token of the route's palette is answered by its pages, which know the palette. CONTROL: the owner's
// two examples boot, and several calls of one file add up.
func TestRouterGradientsIsValidated(t *testing.T) {
	hero := `stops: [ { color: "brand", at: 0 }, { color: "brand+15", at: 100 } ]`
	for written, want := range map[string]string{
		`router.gradients();`:                                                                  `router.gradients nhận một object các gradient`,
		`router.gradients("glow");`:                                                            `router.gradients nhận một object các gradient`,
		`router.gradients({ hero: "brand" });`:                                                 `router.gradients({ hero: … }): một gradient là một object { type, direction hay position, stops }`,
		`router.gradients({ hero: { direction: "to-end", ` + hero + ` } });`:                   `router.gradients: "hero" thiếu type: linear, radial hoặc conic`,
		`router.gradients({ hero: { type: "linear", ` + hero + ` } });`:                        `router.gradients: "hero" là linear nên cần direction: to-top, to-bottom`,
		`router.gradients({ hero: { type: "linear", direction: "to-right", ` + hero + ` } });`: `trái phải viết start và end, tự lật khi viết từ phải sang trái: to-end`,
		`router.gradients({ hero: { type: "linear", direction: "to-end", stops: [ { color: "brand", at: 60 }, { color: "ink", at: 20 } ] } });`:    `router.gradients: "hero" điểm dừng 2 có at 20, nhỏ hơn at 60 của điểm dừng trước`,
		`router.gradients({ hero: { type: "linear", direction: "to-end", stops: [ { color: "brand", at: 0 }, { color: "ink", at: 140 } ] } });`:    `"hero" điểm dừng 2 có at 140: at là vị trí trên gradient, từ 0 đến 100`,
		`router.gradients({ hero: { type: "linear", direction: "to-end", stops: [ { color: "brand", at: 0 }, { color: "ink" } ] } });`:             `router.gradients({ hero: … }): điểm dừng 2 thiếu at, một số từ 0 đến 100`,
		`router.gradients({ hero: { type: "linear", direction: "to-end", stops: [ { color: "brand", at: "0%" }, { color: "ink", at: 100 } ] } });`: `điểm dừng 1 thiếu at, một số từ 0 đến 100`,
		`router.gradients({ hero: { type: "linear", direction: "to-end", stops: [ { colour: "brand", at: 0 } ] } });`:                              `điểm dừng 1 nhận color và at, không có "colour"`,
		`router.gradients({ hero: { type: "linear", direction: "to-end", stops: "brand" } });`:                                                     `stops là một mảng các điểm dừng`,
		`router.gradients({ hero: { type: "linear", angle: 45, ` + hero + ` } });`:                                                                 `một gradient nhận type, direction, position và stops, không có "angle"`,
		`router.gradients({ glow: { type: "radial", ` + hero + ` } });`:                                                                            `router.gradients: "glow" là tên một gradient chuẩn của Kitwork, có sẵn`,
		`router.gradients({ violet: { type: "radial", ` + hero + ` } });`:                                                                          `router.gradients: "violet" đã là tên một màu (#ee82ee)`,
		// a name never ends in -<digits>: hero-2 would read as hero 2% lighter (13/10/2026)
		`router.gradients({ "hero-2": { type: "radial", ` + hero + ` } });`: `router.gradients: "hero-2" kết thúc bằng -2`,
	} {
		files := withRootRouter(t, sitePalette+"\n"+written, nil)
		if _, err := bootUISite(t, files, true); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s:\n want a boot error naming %q\n got %v", written, want, err)
		}
	}
	controls := sitePalette + `
router.gradients({ hero: { type: "linear", direction: "to-bottom-end", stops: [ { color: "brand", at: 0 }, { color: "violet", at: 100 } ] } });
router.gradients({ halo: { type: "radial", position: "top", stops: [ { color: "brand/40", at: 0 }, { color: "brand/0", at: 70 } ] } });`
	files := withRootRouter(t, controls, map[string]string{
		"localhost/content.ui": `<content><box background="hero"><text>Quán</text></box><box background="halo"><text>Trà</text></box></content>`,
	})
	tenant, err := bootUISite(t, files, true)
	if err != nil {
		t.Fatalf("control: the owner's two gradients boot: %v", err)
	}
	if page := pageWithStylesheet(t, tenant, "http://localhost/"); !strings.Contains(page, ".background-hero{") || !strings.Contains(page, ".background-halo{") {
		t.Errorf("control: two calls of one file add up:\n%s", linesWith(page, "background-h"))
	}
	// a gradient named like a token of the palette: the page answers it
	files = withRootRouter(t, sitePalette+"\n"+`router.gradients({ panel: { type: "linear", direction: "to-end", `+hero+` } });`, nil)
	tenant = buildUISite(t, files)
	if rec := serveUI(t, tenant, "http://localhost/"); rec.Code != http.StatusInternalServerError ||
		!strings.Contains(htmlUnescape(rec.Body.String()), `router.gradients: "panel" đã là token màu của bảng màu`) {
		t.Errorf("a gradient named panel: %d %.500q", rec.Code, rec.Body.String())
	}
}
