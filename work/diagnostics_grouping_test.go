package work

import (
	"net/http"
	"strings"
	"testing"
)

// kitworkColors is the palette of docs/examples/kitwork's router.work, the surfaces and ink: a palette
// with a dark look, where background="white" stays white.
const kitworkColors = `router.colors({
  brand: { light: "#f5225f", dark: "#ff6b6b" },
  canvas: { light: "#ffeaec", dark: "#16161e" },
  paper: { light: "#ffffff", dark: "#1e2030" },
  ink: { light: "#242442", dark: "#c0caf5" }
});`

// whiteBox is a home whose box is written background="white" under the page's ink and two texts in
// ink-30: three places that fall short in the dark because of the one white.
const whiteBox = "<content>\n<box background=\"white\" color=\"ink\">\n<text>Cà phê</text>\n<text color=\"ink-30\">Trà</text>\n<text color=\"ink-30\">Bánh</text>\n</box>\n</content>"

// The pairs that fall short because of one background reach the author as one line, while developing:
// the terminal prints it once, and the page's warnings file says it in one console.warn. The page
// renders as written (200, the white kept). Under router.contrast("strict") the page answers every
// pair, each its own line, as before.
func TestUISiteGroupsContrastByBackground(t *testing.T) {
	files := withRootRouter(t, kitworkColors, map[string]string{"localhost/content.ui": whiteBox})
	var tenant *Tenant
	var page string
	printed := captureBootOutput(t, func() {
		tenant = buildUISite(t, files)
		for i := 0; i < 2; i++ {
			rec := serveUI(t, tenant, "http://localhost/")
			if rec.Code != http.StatusOK {
				t.Fatalf("GET / = %d, want the page as written:\n%s", rec.Code, rec.Body.String())
			}
			page = rec.Body.String()
		}
	})
	want := []string{"cảnh báo: " + groupedWhiteBox + " (số chỗ là của trang /; console của mỗi trang nêu chỗ của trang đó)"}
	if got := warningLines(printed); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the terminal hears the white once:\n got  %q\n want %q", got, want)
	}
	if !strings.Contains(page, "background-white") {
		t.Errorf("the box keeps the white written:\n%s", linesWith(page, "background-"))
	}
	link := warningsScriptRe.FindStringSubmatch(page)
	if link == nil {
		t.Fatalf("a page with warnings links its warnings file:\n%s", linesWith(page, "<script"))
	}
	script := serveUI(t, tenant, "http://localhost"+link[1]).Body.String()
	quoted := `console.warn("cảnh báo: ` + strings.ReplaceAll(groupedWhiteBox, `"`, `\"`) + `");`
	if strings.Count(script, "console.warn(") != 1 || !strings.Contains(script, quoted) {
		t.Errorf("one console.warn for the white:\n got  %s\n want %s", script, quoted)
	}

	// strict: every pair, each its own line, as before
	files = withRootRouter(t, kitworkColors+"\nrouter.contrast(\"strict\");", map[string]string{"localhost/content.ui": whiteBox})
	rec := serveUI(t, buildUISite(t, files), "http://localhost/")
	body := htmlUnescape(rec.Body.String())
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("strict: GET / = %d, want the compile error", rec.Code)
	}
	for _, told := range strictWhiteBox {
		if !strings.Contains(body, told) {
			t.Errorf("strict: the error says\n%s\ngot:\n%s", told, body)
		}
	}
}

// groupedWhiteBox is what the terminal and the console hear of whiteBox: one line at the white.
const groupedWhiteBox = `content.ui:2:6: background="white" giữ màu trắng ở chế độ tối; chữ ở 3 chỗ trên nó khó đọc (ink 1.61:1, ink-30 3.22:1; chữ thường cần 4.5:1, chữ lớn 3:1): ` +
	`content.ui:2:25, 4:7, 5:7. Viết background="paper" (6.60:1 sáng, 5.00:1 tối), hoặc giữ nền trắng và viết color="black" cho chữ trên nó (21.00:1 sáng, 21.00:1 tối). ` +
	`router.contrast("strict") nêu từng chỗ với gợi ý riêng.`

// strictWhiteBox is what router.contrast("strict") answers for whiteBox: each pair its own line.
var strictWhiteBox = []string{
	`content.ui:2:25: color="ink" trên nền white chỉ 1.61:1 ở chế độ tối; chữ thường cần 4.5:1 (chữ lớn 3:1). Nền white là màu cố định, không đổi theo chế độ như chữ: viết background="paper" ở content.ui:2:6 (14.94:1 sáng, 9.97:1 tối), hoặc giữ nền trắng và viết color="black" (21.00:1 sáng, 21.00:1 tối).`,
	`content.ui:4:7: color="ink-30" trên nền white chỉ 3.22:1 ở chế độ tối; chữ thường cần 4.5:1 (chữ lớn 3:1). Nền white là màu cố định, không đổi theo chế độ như chữ: viết background="paper" ở content.ui:2:6 (6.60:1 sáng, 5.00:1 tối), hoặc giữ nền trắng và viết color="black" (21.00:1 sáng, 21.00:1 tối).`,
	`content.ui:5:7: color="ink-30" trên nền white chỉ 3.22:1 ở chế độ tối; chữ thường cần 4.5:1 (chữ lớn 3:1). Nền white là màu cố định, không đổi theo chế độ như chữ: viết background="paper" ở content.ui:2:6 (6.60:1 sáng, 5.00:1 tối), hoặc giữ nền trắng và viết color="black" (21.00:1 sáng, 21.00:1 tối).`,
}

// whitePageHead is what every page of a site whose page.ui is written background="white" says of it:
// the head of its line (ui.Warning.Head), the places after it being each page's own.
const whitePageHead = `page.ui:1:7: background="white" giữ màu trắng ở chế độ tối; `

// A page.ui every route shares, written background="white": each page's line lists that page's own
// places, so the lines differ, but the terminal says the white once a generation (by the line's head),
// for the first page that has it; each page's console still says its own line, its places included.
func TestUISiteSaysASharedBackgroundOnce(t *testing.T) {
	files := withRootRouter(t, kitworkColors, map[string]string{
		"localhost/page.ui":         `<page background="white" color="ink"><content /></page>`,
		"localhost/content.ui":      "<content>\n<text color=\"ink-30\">Cà phê</text>\n<text color=\"ink-30\">Trà</text>\n</content>",
		"localhost/tinh/content.ui": "<content>\n<text color=\"ink-30\">Bánh</text>\n<text color=\"ink-30\">Kem</text>\n</content>",
	})
	var tenant *Tenant
	pages := map[string]string{}
	printed := captureBootOutput(t, func() {
		tenant = buildUISite(t, files)
		for _, target := range []string{"/", "/tinh"} {
			rec := serveUI(t, tenant, "http://localhost"+target)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d, want the page as written", target, rec.Code)
			}
			pages[target] = rec.Body.String()
		}
	})
	var heard []string
	for _, line := range warningLines(printed) {
		if strings.HasPrefix(line, "cảnh báo: "+whitePageHead) {
			heard = append(heard, line)
		}
	}
	// the count and the places are the first page's, and the line says whose: a shared background has
	// places on other pages the terminal does not list (review 12/10/2026)
	if len(heard) != 1 || !strings.Contains(heard[0], "): content.ui:2:7, 3:7. ") || !strings.HasSuffix(heard[0], "(số chỗ là của trang /; console của mỗi trang nêu chỗ của trang đó)") {
		t.Errorf("the terminal says the shared white once, with the first page's places and that page:\n%s", strings.Join(heard, "\n"))
	}
	for target, places := range map[string]string{"/": "): content.ui:2:7, 3:7. ", "/tinh": "): tinh/content.ui:2:7, 3:7. "} {
		link := warningsScriptRe.FindStringSubmatch(pages[target])
		if link == nil {
			t.Fatalf("%s links its warnings file", target)
		}
		script := serveUI(t, tenant, "http://localhost"+link[1]).Body.String()
		if !strings.Contains(script, `console.warn("cảnh báo: `+strings.ReplaceAll(whitePageHead, `"`, `\"`)) || !strings.Contains(script, places) {
			t.Errorf("%s: its console says its own line, places %q:\n%s", target, places, script)
		}
	}
}
