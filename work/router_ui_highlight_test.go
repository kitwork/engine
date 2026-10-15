package work

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// highlightCode is a .ui page with a block of Go: keyword, function, punctuation, string and comment.
const highlightCode = "<content><code language=\"go\">package main\nfunc main() { println(\"x\") } // chạy</code></content>"

// highlightDark is the shared site's colours with a dark canvas: the palette has a dark look.
const highlightDark = `router.colors({ brand: "#c8742c", canvas: { light: "#f6f9fc", dark: "#16161e" } });`

// bodyOf is a page's <body>…</body>: its HTML without the head, whose links name the stylesheets.
func bodyOf(t *testing.T, page string) string {
	t.Helper()
	start, end := strings.Index(page, "<body"), strings.LastIndex(page, "</body>")
	if start < 0 || end < start {
		t.Fatalf("no body in\n%.800s", page)
	}
	return page[start:end]
}

// router.highlight in router.work reaches the page's own stylesheet: the kinds it sets are the author's
// colours (a named colour's hex, a pair's custom property set per mode), the kinds it leaves keep their
// engine colour, and the HTML is the same byte for byte — the class of a kind never depends on the
// palette. CONTROL: the same site without the call writes the engine's colours.
func TestRouterHighlightReachesThePageStylesheet(t *testing.T) {
	files := withRootRouter(t, highlightDark+"\n"+`router.highlight({ keyword: "violet", "function": { light: "#0550ae", dark: "#79c0ff" } });`,
		map[string]string{"localhost/content.ui": highlightCode})
	page := pageWithStylesheet(t, buildUISite(t, files), "http://localhost/")
	for _, want := range []string{
		`<span class="code-keyword">func</span>`,
		".code-keyword{color:#ee82ee}",
		".code-function{color:var(--kitwork-highlight-function)}",
		"--kitwork-highlight-function:#0550ae",
		"--kitwork-highlight-function:#79c0ff",
		".code-string{color:var(--kitwork-code-success)}",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %s in\n%s", want, linesWith(page, "code-"))
		}
	}
	if strings.Contains(page, "--kitwork-code-info") {
		t.Errorf("function is the author's: the engine's info is not set\n%s", linesWith(page, "--kitwork-"))
	}
	plain := pageWithStylesheet(t, buildUISite(t, withRootRouter(t, highlightDark, map[string]string{"localhost/content.ui": highlightCode})), "http://localhost/")
	if !strings.Contains(plain, ".code-keyword{color:var(--kitwork-code-brand)}") || !strings.Contains(plain, ".code-function{color:var(--kitwork-code-info)}") { // CONTROL
		t.Errorf("CONTROL: without router.highlight the engine's colours:\n%s", linesWith(plain, "code-"))
	}
	if bodyOf(t, page) != bodyOf(t, plain) {
		t.Errorf("router.highlight changes the CSS only, never the HTML:\n%s\n----\n%s", bodyOf(t, page), bodyOf(t, plain))
	}
}

// What router.highlight cannot read stops the boot at the call, router.work:<line>:<column> (the place
// the VM gives a native call), saying what to write: one object; a string that names the template's
// colour set or not; keys that are kinds (plain, another spelling, a short name, a typo — a hyphenated
// key in quotes, the author's and the nearest kind's —, the template's keys, the three window dots in one
// sentence); a value that is a string or a pair of light and dark, both strings, not empty, no lone #hex
// (a # that is no colour said so), a side that is a colour, no pair and no #hex for inserted. A call made
// while a page is served (in router.view) is refused: the colours are the router's, set once. CONTROL: a
// call that reads boots.
func TestRouterHighlightRefusesWhatItDoesNotRead(t *testing.T) {
	for written, want := range map[string]string{
		`router.highlight();`:  `router.highlight nhận một object màu theo loại chữ: router.highlight({ keyword: "brand", comment: "ink-30" })`,
		`router.highlight(5);`: `router.highlight nhận một object màu theo loại chữ`,
		`router.highlight({ keyword: "brand" }, { comment: "ink-30" });`:             `router.highlight nhận một object màu theo loại chữ`,
		`router.highlight("tokyonight");`:                                            `router.highlight("tokyonight"): tokyonight là bộ màu của trang template, mã màu cố định cho một nền tối; khối mã của trang .ui tô theo bảng màu của site, đọc trên nền của trang ở cả sáng và tối. Đổi màu loại nào thì viết loại ấy: router.highlight({ keyword: "brand", string: "success", comment: "ink-30" })`,
		`router.highlight("tokyo-night");`:                                           `router.highlight("tokyo-night"): tokyo-night là bộ màu của trang template`,
		`router.highlight("abc");`:                                                   `router.highlight("abc"): router.highlight nhận một object màu theo loại chữ, không nhận một chữ`,
		`router.highlight({ keywrod: "brand" });`:                                    `router.highlight({ keywrod: … }): không có loại chữ "keywrod"; gần nhất: keyword`,
		`router.highlight({ "regular-expresion": "brand" });`:                        `router.highlight({ "regular-expresion": … }): không có loại chữ "regular-expresion"; gần nhất: "regular-expression"`,
		`router.highlight({ red: "#f7768e", yellow: "#e0af68", green: "#9ece6a" });`: `router.highlight({ green, red, yellow }): trang .ui không có khoá green, red, yellow. red, yellow, green là ba chấm cửa sổ ở trang template; ở trang .ui ba chấm là markup của người viết: background="danger", "warning", "success" trên từng chấm.`,
		`router.highlight({ regex: "success" });`:                                    `router.highlight({ regex: … }): tên loại chữ viết đủ chữ: router.highlight({ "regular-expression": "success" })`,
		`router.highlight({ regularExpression: "success" });`:                        `router.highlight({ regularExpression: … }): tên loại chữ là regular-expression, chữ thường nối bằng gạch, viết trong dấu nháy`,
		`router.highlight({ banana: "brand" });`:                                     `router.highlight({ banana: … }): không có loại chữ "banana". Loại chữ: keyword, type, string, regular-expression`,
		`router.highlight({ plain: "ink" });`:                                        `router.highlight({ plain: … }): plain là chữ thường của khối mã, không có màu riêng`,
		`router.highlight({ theme: "tokyonight", command: "brand-dark" });`:          `router.highlight({ command, theme }): trang .ui không có khoá command, theme. command là tên lệnh của dòng shell ở trang template; ở trang .ui đó là function (tên được gọi hay tên lệnh): "function": "brand" (brand-dark là màu của brand ở chế độ tối của trang template; ở trang .ui brand tự đổi theo chế độ). theme là bộ màu có tên của trang template (tokyonight, classic, mono)`,
		`router.highlight({ command: "#ff0000" });`:                                  `router.highlight({ command }): trang .ui không có khoá command. command là tên lệnh của dòng shell ở trang template; ở trang .ui đó là function (tên được gọi hay tên lệnh): "function": { light: "#ff0000", dark: "#…" }.`,
		`router.highlight({ flag: "warning", keywrod: "brand" });`:                   `router.highlight({ flag, keywrod }): trang .ui không có khoá flag, keywrod. flag là cờ dòng lệnh (-x, --long) ở trang template; ở trang .ui đó là attribute (tên thuộc tính trong markup hay cờ dòng lệnh): attribute: "warning". keywrod: không có loại chữ "keywrod"; gần nhất: keyword.`,
		`router.highlight({ keyword: 5 });`:                                          `router.highlight({ keyword: … }): màu của keyword là một chữ (brand, ink-30, violet) hay một cặp { light: "#7c3aed", dark: "#bb9af7" }`,
		`router.highlight({ keyword: "" });`:                                         `router.highlight({ keyword: "" }): màu trống`,
		`router.highlight({ keyword: "#7c3aed" });`:                                  `router.highlight({ keyword: "#7c3aed" }): màu #hex là màu của một chế độ: viết một cặp cho sáng và tối, keyword: { light: "#7c3aed", dark: "#…" }`,
		`router.highlight({ keyword: "#zzz" });`:                                     `router.highlight({ keyword: "#zzz" }): "#zzz" không phải màu: màu #hex là "#rgb" hay "#rrggbb"`,
		`router.highlight({ inserted: "#1a7f37" });`:                                 `router.highlight({ inserted: "#1a7f37" }): inserted nhận một màu nhấn, không nhận #hex`,
		`router.highlight({ "function": { light: "#0550ae", drak: "#79c0ff" } });`:   `router.highlight({ "function": { drak: … } }): một cặp màu chỉ nhận light và dark, không có "drak"`,
		`router.highlight({ "function": { light: "#0550ae" } });`:                    `router.highlight({ "function": … }): cặp màu thiếu dark: viết cả hai, { light: "#0550ae", dark: "#…" }`,
		`router.highlight({ "function": { light: "#0550ae", dark: 5 } });`:           `router.highlight({ "function": … }): dark của cặp màu là một chữ, "#rrggbb" hay một màu có tên`,
		`router.highlight({ "function": { light: "#0550ae", dark: "info" } });`:      `router.highlight({ "function": … }): dark là "info": một bên của cặp là "#rgb", "#rrggbb" hay một màu có tên`,
		`router.highlight({ "function": { light: "#0550ae", dark: "navy-blue" } });`: `router.highlight({ "function": … }): dark là "navy-blue": navy-blue không phải tên màu: đó là tên Figma đặt cho navy (#000080) của CSS; viết navy`,
		`router.highlight({ "function": {} });`:                                      `router.highlight({ "function": … }): cặp màu trống, viết { light: "#0550ae", dark: "#79c0ff" }`,
		`router.highlight({ inserted: { light: "#1a7f37", dark: "#3fb950" } });`:     `router.highlight({ inserted: … }): inserted nhận một màu nhấn, không nhận cặp`,
	} {
		files := withRootRouter(t, sitePalette+"\n"+written, nil)
		_, err := bootUISite(t, files, true)
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "router.work:6:") {
			t.Errorf("%s:\n want the boot to stop at router.work:6: saying %s\n got %v", written, want, err)
		}
	}
	// the three window dots share one sentence, said once
	dots := withRootRouter(t, sitePalette+"\n"+`router.highlight({ red: "#f7768e", yellow: "#e0af68", green: "#9ece6a" });`, nil)
	if _, err := bootUISite(t, dots, true); err == nil || strings.Count(err.Error(), "là ba chấm cửa sổ") != 1 {
		t.Errorf("red, yellow, green: one sentence, got %v", err)
	}
	// at request time, in a view: the page answers it, and the colours stay as the router set them
	view := withFiles(map[string]string{"localhost/content.ui": highlightCode})
	view["localhost/router.work"] = strings.Replace(view["localhost/router.work"], `ctx.title("Thực đơn");`, `ctx.title("Thực đơn");
  router.highlight({ keyword: "violet" });`, 1)
	tenant := buildUISite(t, view)
	if rec := serveUI(t, tenant, "http://localhost/"); rec.Code != http.StatusInternalServerError ||
		!strings.Contains(rec.Body.String(), "router.highlight viết ở cấp đầu của router.work, chạy một lần khi site khởi động") {
		t.Errorf("router.highlight in a view: %d %.600s", rec.Code, rec.Body.String())
	}
	if css := pageWithStylesheet(t, tenant, "http://localhost/tinh"); strings.Contains(css, "#ee82ee") {
		t.Errorf("a call refused at request time changes nothing:\n%s", linesWith(css, "code-"))
	}
	// CONTROL
	files := withRootRouter(t, sitePalette+"\n"+`router.highlight({ keyword: "brand+40", "regular-expression": "success+30", "function": { light: "#0550ae", dark: "navy" } });`, nil)
	if _, err := bootUISite(t, files, true); err != nil {
		t.Errorf("CONTROL: a call that reads boots: %v", err)
	}
}

// Like router.colors, router.highlight is inherited down the folders, the nearest folder winning per
// kind: a branch's keyword over the site's, the site's comment kept; a folder without a router.work takes
// the site's. A colour of a branch's call is told at that call, its file named from the site root
// (mon/{slug}/router.work). CONTROL: the site's page keeps the site's keyword.
func TestRouterHighlightBranchesPerKind(t *testing.T) {
	files := withRootRouter(t, sitePalette+"\n"+`router.highlight({ keyword: "violet", comment: "ink-20" });`, map[string]string{
		"localhost/content.ui": highlightCode,
		"localhost/mon/{slug}/router.work": `import { router } from "kitwork";
router.highlight({ keyword: "brand" });
router.view((ctx) => {
  if (ctx.params("slug") != "ca-phe") return ctx.notFound();
  const name = "Cà phê sữa";
});
`,
		"localhost/mon/{slug}/content.ui": highlightCode,
		"localhost/tinh/content.ui":       highlightCode,
	})
	var tenant *Tenant
	printed := captureBootOutput(t, func() { tenant = buildUISite(t, files) })
	comment := ".code-comment{color:color-mix(in oklab,var(--ink) 80%,var(--canvas));font-style:italic}"
	for path, keyword := range map[string]string{
		"/":           ".code-keyword{color:#ee82ee}", // CONTROL
		"/tinh":       ".code-keyword{color:#ee82ee}",
		"/mon/ca-phe": ".code-keyword{color:var(--brand)}",
	} {
		page := pageWithStylesheet(t, tenant, "http://localhost"+path)
		if !strings.Contains(page, keyword) || !strings.Contains(page, comment) {
			t.Errorf("%s: want %s and the site's %s:\n%s", path, keyword, comment, linesWith(page, "code-"))
		}
	}
	branch := regexp.MustCompile(`cảnh báo: mon/\{slug\}/router\.work:2:\d+: router\.highlight: màu chưa đủ tương phản trên nền của trang \(chữ thường cần 4\.5:1\): keyword "brand" ở chế độ sáng`)
	if !branch.MatchString(printed) {
		t.Errorf("the branch's call is told at mon/{slug}/router.work:2:\n%s", strings.Join(warningLines(printed), "\n"))
	}
}

// A colour router.highlight writes is checked against the palette of each route it reaches, as a
// router.colors value is: a token only a branch declares is unknown at the site's pages, which answer the
// error at the call (router.work:<line>:<column>), and known at the branch's, which render. CONTROL: the
// token declared at the site root, the site's page renders too.
func TestRouterHighlightValueAgainstTheRoutePalette(t *testing.T) {
	branch := `import { router } from "kitwork";
router.colors({ chart: "#0f766e" });
`
	files := withRootRouter(t, sitePalette+"\n"+`router.highlight({ keyword: "chart" });`, map[string]string{
		"localhost/content.ui":      highlightCode,
		"localhost/kho/router.work": branch,
		"localhost/kho/content.ui":  highlightCode,
	})
	tenant := buildUISite(t, files)
	rec := serveUI(t, tenant, "http://localhost/")
	if body := htmlUnescape(rec.Body.String()); rec.Code != http.StatusInternalServerError ||
		!regexp.MustCompile(`router\.work:6:\d+: router\.highlight\(\{ keyword: "chart" \}\): "chart" không có trong bảng màu của trang`).MatchString(body) {
		t.Errorf("/ = %d %.600s, want the error at the call", rec.Code, body)
	}
	if page := pageWithStylesheet(t, tenant, "http://localhost/kho"); !strings.Contains(page, ".code-keyword{color:var(--chart)}") {
		t.Errorf("/kho knows chart:\n%s", linesWith(page, "code-keyword"))
	}
	files = withRootRouter(t, `router.colors({ brand: "#c8742c", chart: "#0f766e" });`+"\n"+`router.highlight({ keyword: "chart" });`, map[string]string{"localhost/content.ui": highlightCode})
	if rec := serveUI(t, buildUISite(t, files), "http://localhost/"); rec.Code != http.StatusOK { // CONTROL
		t.Errorf("CONTROL: chart declared at the root, / = %d %.400s", rec.Code, rec.Body.String())
	}
}

// A colour of router.highlight short of contrast on the page's grounds is one line at the call, the same
// on every page that shows it: the terminal says it once a generation, each page's console says it
// (warnings.<hash>.js); router.contrast("strict") makes it the page's error. CONTROL: a colour that
// reads is told nothing.
func TestRouterHighlightWarnsOncePerCall(t *testing.T) {
	line := regexp.MustCompile(`router\.work:6:\d+: router\.highlight: màu chưa đủ tương phản trên nền của trang \(chữ thường cần 4\.5:1\): keyword "violet" ở chế độ sáng \(#ee82ee\): canvas 2\.19:1, paper 2\.32:1, panel 2\.08:1\. Dùng keyword: \{ light: "#c400c5", dark: "violet" \}`)
	files := withRootRouter(t, sitePalette+"\n"+`router.highlight({ keyword: "violet" });`, map[string]string{
		"localhost/content.ui":      highlightCode,
		"localhost/tinh/content.ui": `<content><card><code language="go">func main() {}</code></card></content>`,
	})
	var tenant *Tenant
	printed := captureBootOutput(t, func() { tenant = buildUISite(t, files) })
	told := 0
	for _, l := range warningLines(printed) {
		if line.MatchString(l) {
			told++
		}
	}
	if told != 1 {
		t.Errorf("the terminal says the call's line once, got %d:\n%s", told, strings.Join(warningLines(printed), "\n"))
	}
	// a branch that colours one of the call's kinds too: the site's call is still one line, on the site's
	// pages and the branch's, which show its comment
	atSite := regexp.MustCompile(`(^|\s)router\.work:6:\d+: router\.highlight: `)
	branched := withRootRouter(t, highlightDark+"\n"+`router.highlight({ keyword: "violet", comment: "ink-40" });`, map[string]string{
		"localhost/content.ui":      highlightCode,
		"localhost/kho/router.work": "import { router } from \"kitwork\";\nrouter.highlight({ keyword: \"brand+40\" });\n",
		"localhost/kho/content.ui":  highlightCode,
	})
	printed = captureBootOutput(t, func() { buildUISite(t, branched) })
	var site []string
	for _, l := range warningLines(printed) {
		if atSite.MatchString(l) {
			site = append(site, l)
		}
	}
	if len(site) != 1 || !strings.Contains(site[0], `keyword "violet"`) || !strings.Contains(site[0], `comment "ink-40"`) {
		t.Errorf("the site's call is one line, keyword and comment, on every page, got %d:\n%s", len(site), strings.Join(warningLines(printed), "\n"))
	}
	for _, path := range []string{"/", "/tinh"} {
		page := serveUI(t, tenant, "http://localhost"+path).Body.String()
		link := warningsScriptRe.FindStringSubmatch(page)
		if link == nil {
			t.Fatalf("%s links its warnings file", path)
		}
		if script := serveUI(t, tenant, "http://localhost"+link[1]).Body.String(); !line.MatchString(strings.ReplaceAll(script, `\"`, `"`)) {
			t.Errorf("%s: the console says the call's line:\n%s", path, script)
		}
	}
	strict := withRootRouter(t, sitePalette+"\n"+`router.contrast("strict");`+"\n"+`router.highlight({ keyword: "violet" });`, map[string]string{"localhost/content.ui": highlightCode})
	rec := serveUI(t, buildUISite(t, strict), "http://localhost/")
	if body := htmlUnescape(rec.Body.String()); rec.Code != http.StatusInternalServerError || !regexp.MustCompile(`router\.work:7:\d+: router\.highlight: màu chưa đủ tương phản`).MatchString(body) {
		t.Errorf("strict: the page answers the call's line, got %d %.500s", rec.Code, body)
	}
	quiet := withRootRouter(t, sitePalette+"\n"+`router.highlight({ keyword: "brand+40", "function": { light: "#0550ae", dark: "#79c0ff" } });`, map[string]string{"localhost/content.ui": highlightCode})
	printed = captureBootOutput(t, func() { buildUISite(t, quiet) })
	if strings.Contains(printed, "router.highlight") { // CONTROL
		t.Errorf("CONTROL: colours that read are told nothing:\n%s", printed)
	}
}
