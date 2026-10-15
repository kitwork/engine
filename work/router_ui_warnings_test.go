package work

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// lowContrastContent is the shared site's home with two pairs short of WCAG 2.2 AA on Kit Design's
// canvas: ink 70% lighter written inside a for (one pair, however many rows render) and danger.
const lowContrastContent = "<content>\n<list><item for=\"item of items\"><text color=\"ink-70\">{{ item.name }}</text></item></list>\n<text color=\"danger\">{{ count }} món</text>\n</content>"

// lowContrastTold is what the contrast check says of lowContrastContent, a line a pair: the same
// words whether a warning (each line then said after "cảnh báo: ") or, under "strict", the error.
var lowContrastTold = []string{
	`content.ui:2:39: color="ink-70" trên nền canvas chỉ 1.99:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="ink-40" (4.61:1 sáng).`,
	`content.ui:3:7: color="danger" trên nền canvas chỉ 3.70:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="danger+15" (4.70:1 sáng).`,
}

// warningsScriptRe finds the warnings file a .ui page links while developing.
var warningsScriptRe = regexp.MustCompile(`<script data-kitwork-generated="?warnings"? src="?(/_kitwork/warnings\.[0-9a-f]{16}\.js)"? defer>`)

// warningLines are the lines of the process's output that are warnings, in the order printed.
func warningLines(printed string) []string {
	var out []string
	for _, line := range strings.Split(printed, "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "cảnh báo:") {
			out = append(out, line)
		}
	}
	return out
}

// By default a pair short of contrast is a warning (decision D): the page renders 200 as written.
// While developing, the terminal hears each pair once a compile — not once a row, not once a request
// — and the page links /_kitwork/warnings.<hash>.js, which says each in the browser's console on every
// load: a file, never an inline script. A page with nothing to tell links none. CONTROL: production
// prints nothing and links nothing, and the file's name is the engine's 404 there.
func TestUISiteLowContrastWarnsWhileDeveloping(t *testing.T) {
	files := withFiles(map[string]string{"localhost/content.ui": lowContrastContent})
	var tenant *Tenant
	var page string
	printed := captureBootOutput(t, func() {
		tenant = buildUISite(t, files)
		for i := 0; i < 3; i++ {
			rec := serveUI(t, tenant, "http://localhost/")
			if rec.Code != http.StatusOK {
				t.Fatalf("GET / = %d, want the page as written:\n%s", rec.Code, rec.Body.String())
			}
			page = rec.Body.String()
		}
		serveUI(t, tenant, "http://localhost/tinh")
	})
	want := []string{"cảnh báo: " + lowContrastTold[0], "cảnh báo: " + lowContrastTold[1]}
	if got := warningLines(printed); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the terminal hears each pair once a compile:\n got  %q\n want %q", got, want)
	}
	// rendered as written: both rows in the colour the author chose, and the count in danger
	if strings.Count(page, `class="color-ink-70"`) != 2 || strings.Count(page, `class="color-danger"`) != 1 {
		t.Errorf("the text keeps the colours written:\n%s", linesWith(page, "color-"))
	}
	mustContainAll(t, page, "Cà phê", "Trà &lt;đào&gt;", "2 món")

	link := warningsScriptRe.FindStringSubmatch(page)
	if link == nil {
		t.Fatalf("a page with warnings links its warnings file while developing:\n%s", linesWith(page, "<script"))
	}
	if strings.Contains(page, "console.warn") {
		t.Errorf("the warnings are a file the page links, never inline:\n%s", linesWith(page, "console.warn"))
	}
	rec := serveUI(t, tenant, "http://localhost"+link[1])
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript") {
		t.Fatalf("%s = %d %q, want the warnings file", link[1], rec.Code, rec.Header().Get("Content-Type"))
	}
	script := rec.Body.String()
	if strings.Count(script, "console.warn(") != 2 {
		t.Errorf("one console.warn a warning:\n%s", script)
	}
	for _, told := range lowContrastTold {
		quoted := `console.warn("cảnh báo: ` + strings.ReplaceAll(told, `"`, `\"`) + `");`
		if !strings.Contains(script, quoted) {
			t.Errorf("the console says %s\ngot:\n%s", quoted, script)
		}
	}
	// a page with nothing to tell links no warnings file
	if quiet := serveUI(t, tenant, "http://localhost/tinh").Body.String(); strings.Contains(quiet, "warnings") {
		t.Errorf("/tinh has no warning and links no warnings file:\n%s", linesWith(quiet, "warnings"))
	}

	// a pair in the page.ui every route shares — the home, a branch, a static page, the not-found page
	// — is one compile's warning of each of those pages, and the terminal hears it once, not once a route
	shared := withFiles(map[string]string{"localhost/page.ui": "<page>\n<text color=\"ink-70\">Quán</text><content /></page>"})
	printed = captureBootOutput(t, func() {
		tenant = buildUISite(t, shared)
		for _, target := range []string{"/", "/tinh", "/mon/ca-phe", "/khong-co"} {
			page := serveUI(t, tenant, "http://localhost"+target).Body.String()
			if !warningsScriptRe.MatchString(page) {
				t.Errorf("%s shares page.ui's warning and links a warnings file", target)
			}
		}
	})
	sharedTold := `cảnh báo: page.ui:2:7: color="ink-70" trên nền canvas chỉ 1.99:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="ink-40" (4.61:1 sáng).`
	if got := warningLines(printed); len(got) != 1 || got[0] != sharedTold {
		t.Errorf("page.ui's pair is said once for every route that shares it:\n got  %q\n want %q", got, sharedTold)
	}

	// CONTROL: production — the same page renders, nothing printed, nothing linked, no file
	printed = captureBootOutput(t, func() {
		tenant = buildUISiteMode(t, files, false)
		rec = serveUI(t, tenant, "http://localhost/")
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("production GET / = %d", rec.Code)
	}
	if lines := warningLines(printed); len(lines) != 0 {
		t.Errorf("production prints no warning, got %q", lines)
	}
	if strings.Contains(rec.Body.String(), "warnings") {
		t.Errorf("production links no warnings file:\n%s", linesWith(rec.Body.String(), "warnings"))
	}
	if missing := serveUI(t, tenant, "http://localhost"+link[1]); missing.Code != http.StatusNotFound {
		t.Errorf("production %s = %d, want 404", link[1], missing.Code)
	}
}

// router.contrast("strict") on the site root turns the same pairs back into the compile error, word for
// word, every pair at once; it is inherited down the folders, and a branch may say "warning" for itself.
// CONTROL: a page with no pair short of contrast renders under strict, and router.contrast("warning")
// written out is the default.
func TestUISiteContrastStrict(t *testing.T) {
	files := withRootRouter(t, `router.colors({ brand: "#c8742c" });
router.contrast("strict");`, map[string]string{
		"localhost/content.ui":            lowContrastContent,
		"localhost/mon/{slug}/content.ui": `<content><text color="danger">{{ name }}</text></content>`,
	})
	tenant := buildUISite(t, files)
	rec := serveUI(t, tenant, "http://localhost/")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("strict: GET / = %d, want the compile error", rec.Code)
	}
	for _, told := range lowContrastTold {
		if !strings.Contains(htmlUnescape(rec.Body.String()), told) {
			t.Errorf("strict: the error says\n%s\ngot:\n%s", told, rec.Body.String())
		}
	}
	// inherited: the branch writes no router.contrast
	if rec := serveUI(t, tenant, "http://localhost/mon/ca-phe"); rec.Code != http.StatusInternalServerError || !strings.Contains(htmlUnescape(rec.Body.String()), `color="danger" trên nền canvas chỉ 3.70:1`) {
		t.Errorf("strict is inherited down the folders: GET /mon/ca-phe = %d\n%s", rec.Code, rec.Body.String())
	}
	// CONTROL: nothing short of contrast renders under strict
	if rec := serveUI(t, tenant, "http://localhost/tinh"); rec.Code != http.StatusOK {
		t.Errorf("strict: GET /tinh = %d, want 200", rec.Code)
	}

	// a branch says "warning" for itself
	files["localhost/mon/{slug}/router.work"] = strings.Replace(files["localhost/mon/{slug}/router.work"], `import { router } from "kitwork";`, `import { router } from "kitwork";
router.contrast("warning");`, 1)
	tenant = buildUISite(t, files)
	if rec := serveUI(t, tenant, "http://localhost/mon/ca-phe"); rec.Code != http.StatusOK {
		t.Errorf("a branch's router.contrast(\"warning\") wins over the root's strict: GET /mon/ca-phe = %d\n%s", rec.Code, rec.Body.String())
	}
	if rec := serveUI(t, tenant, "http://localhost/"); rec.Code != http.StatusInternalServerError {
		t.Errorf("the root keeps strict: GET / = %d", rec.Code)
	}

	// CONTROL: "warning" written out is the default
	files = withRootRouter(t, `router.colors({ brand: "#c8742c" });
router.contrast("warning");`, map[string]string{"localhost/content.ui": lowContrastContent})
	if rec := serveUI(t, buildUISite(t, files), "http://localhost/"); rec.Code != http.StatusOK {
		t.Errorf("router.contrast(\"warning\"): GET / = %d, want 200", rec.Code)
	}
}

// router.contrast takes one of two words; anything else stops the boot naming both. CONTROL: each of
// the two boots.
func TestRouterContrastIsValidated(t *testing.T) {
	for written, want := range map[string]string{
		`router.contrast("off");`:    `router.contrast("off"): chỉ nhận "warning" (mặc định`,
		`router.contrast("Strict");`: `router.contrast("Strict"): chỉ nhận "warning"`,
		`router.contrast();`:         `router.contrast nhận một chữ, "warning" (mặc định) hoặc "strict"`,
		`router.contrast(true);`:     `router.contrast nhận một chữ, "warning" (mặc định) hoặc "strict"`,
	} {
		files := withRootRouter(t, sitePalette+"\n"+written, nil)
		if _, err := bootUISite(t, files, true); err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), `"strict"`) {
			t.Errorf("%s: want a boot error naming %q and both words, got %v", written, want, err)
		}
	}
	for _, mode := range []string{"warning", "strict"} {
		files := withRootRouter(t, sitePalette+"\nrouter.contrast(\""+mode+"\");", nil)
		if _, err := bootUISite(t, files, true); err != nil {
			t.Errorf("router.contrast(%q) must boot, got %v", mode, err)
		}
	}
}

func mustContainAll(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("missing %q", w)
		}
	}
}

// htmlUnescape reads an error page's text the way the author does: its quotes and angle brackets.
func htmlUnescape(s string) string {
	return strings.NewReplacer("&#34;", `"`, "&quot;", `"`, "&lt;", "<", "&gt;", ">", "&#39;", "'", "&amp;", "&").Replace(s)
}
