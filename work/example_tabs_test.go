package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// exampleCodeRe finds a block of code of a language on a page the engine wrote.
var exampleCodeRe = regexp.MustCompile(`<code class="language-(work|ui)">([\s\S]*?)</code>`)

// examplePage boots the kitwork example while developing and returns its tenant and the HTML of path,
// which must be 200 and link no warnings.
func examplePage(t *testing.T, path string) (*Tenant, string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(examplesRoot, "kitwork", "kitwork.localhost", "components", "content.ui")); err != nil {
		t.Skipf("no kitwork example beside the engine (%v)", err)
	}
	root := copyExamples(t)
	saved := AllowLocal
	AllowLocal = true // developing: a page with something to tell links its warnings
	t.Cleanup(func() { AllowLocal = saved })
	tenant := NewTenant(root, "kitwork.localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	tenant.Serve(rec, httptest.NewRequest(http.MethodGet, "http://kitwork.localhost"+path, nil))
	page := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("%s = %d:\n%.1500s", path, rec.Code, page)
	}
	if link := warningsScriptRe.FindStringSubmatch(page); link != nil {
		r := httptest.NewRecorder()
		tenant.Serve(r, httptest.NewRequest(http.MethodGet, "http://kitwork.localhost"+link[1], nil))
		t.Errorf("%s links warnings:\n%.2000s", path, r.Body.String())
	}
	return tenant, page
}

// The kitwork example's /components shows the tabs at work: one terminal window of two files, the
// router.work and the page.ui of a tiny site. The page is served with no warning. The terminal types
// (typing: 2000); its bar holds the window's dots (from small), a tablist of two tabs, router first,
// each tab's look written with selected:, and one Copy beside the tablist, not in it; then the two files,
// each a panel its tab shows (page.ui's painted hidden), with no prompt (a file's code), and the
// terminal's status region after them: both panels are the one terminal's, so its Copy copies the one
// shown. One terminal, one copy(), no width show and no grid in the section: nothing stands side by
// side. No persist, so the scope hands the instance no name to keep its choice under and the page
// carries no pre-paint. And the two files it shows are real: written as a site of their own (with the
// content.ui the route needs) they compile with no warning and serve the page they describe. CONTROL:
// the same page.ui with a colour short of contrast is told, so "no warning" means something.
func TestExampleTabsWindowShowsTwoFilesThatCompile(t *testing.T) {
	_, page := examplePage(t, "/components")
	start := strings.Index(page, `id="tabs"`)
	end := strings.Index(page[max(start, 0):], "</section>")
	if start < 0 || end < 0 {
		t.Fatalf("/components has no section id=\"tabs\":\n%.2000s", page)
	}
	section := page[start : start+end]
	for _, want := range []string{
		`<div class="flex-column max-width-large radius-medium" data-kitwork-component="terminal" data-kitwork-scope="typing: 2000"><div class="flex-row wrap-true align-center space-between gap-3 padding-x-4 padding-y-2 background-ink-80 radius-top-medium"><div class="flex-row align-center gap-4"><div class="flex-row gap-1 show-false small:show-true">`,
		`<div class="flex-row gap-1" data-kitwork-component="tabs: $files" role="tablist" aria-label="Files" data-kitwork-scope="selected: &#39;router&#39;">`,
		`role="tab" aria-controls="kitwork-tabpanel-1-1" value="router"`,
		`role="tab" aria-controls="kitwork-tabpanel-1-2" value="page"`,
		`background-ink-80 color-ink-20 selected:background-panel selected:color-ink selected:underline-true" id="kitwork-tab-1-1"`,
		`background-ink-80 color-ink-20 selected:background-panel selected:color-ink selected:underline-true" id="kitwork-tab-1-2"`,
		`>page.ui</button></div></div><button class="flex-row background-ink/6 hover:background-ink/10 pressed:background-ink/14 disabled:background-ink/4 size-small padding-x-2 padding-y-1" type="button" data-kitwork-bind="text: copied ? &#39;Copied&#39; : &#39;Copy&#39;" data-kitwork-event="click: copy()">Copy</button></div>` +
			`<div class="flex-column padding-4 background-panel radius-bottom-medium size-small" id="kitwork-tabpanel-1-1" role="tabpanel" aria-labelledby="kitwork-tab-1-1" tabindex="0" data-kitwork-bind="show: $files.selected == &#39;router&#39;"><pre><code class="language-work">`,
		`</code></pre></div><div class="flex-column padding-4 background-panel radius-bottom-medium size-small" id="kitwork-tabpanel-1-2" role="tabpanel" aria-labelledby="kitwork-tab-1-2" tabindex="0" data-kitwork-bind="show: $files.selected == &#39;page&#39;" hidden><pre><code class="language-ui">`,
		`</code></pre></div><span class="reader" role="status"></span></div>`,
	} {
		if !strings.Contains(section, want) {
			t.Errorf("/components has no %s", want)
		}
	}
	for what, want := range map[string]int{
		`data-kitwork-component="terminal`: 1,
		`click: copy()`:                    1,
		`role="tablist"`:                   1,
		`role="tabpanel"`:                  2,
		`medium:show`:                      0,
		`class="grid`:                      0,
		`code-prompt`:                      0,
	} {
		if got := strings.Count(section, what); got != want {
			t.Errorf("the tabs section of /components has %s %d times, want %d", what, got, want)
		}
	}
	if strings.Contains(page, `data-kitwork-generated="tabs"`) || strings.Contains(page, `persist: &#39;`) {
		t.Error("/components keeps no choice: its tabs have no persist, and the page no pre-paint")
	}
	files := map[string]string{}
	for _, m := range exampleCodeRe.FindAllStringSubmatch(page, -1) {
		files[m[1]] = textContent(m[2])
	}
	if files["work"] == "" || files["ui"] == "" {
		t.Fatalf("/components shows no router.work and page.ui: %q", files)
	}
	site := func(page string) (string, *httptest.ResponseRecorder) {
		var rec *httptest.ResponseRecorder
		printed := captureBootOutput(t, func() {
			tenant := buildUISite(t, map[string]string{
				"localhost/router.work": files["work"],
				"localhost/page.ui":     page,
				"localhost/content.ui":  "<content></content>",
			})
			rec = serveUI(t, tenant, "http://localhost/")
		})
		return strings.Join(warningLines(printed), "\n"), rec
	}
	warned, rec := site(files["ui"])
	if warned != "" || rec.Code != http.StatusOK {
		t.Fatalf("the files the example shows, as a site: %d, warnings %q\nrouter.work:\n%s\npage.ui:\n%s\n%.1500s", rec.Code, warned, files["work"], files["ui"], rec.Body.String())
	}
	served := rec.Body.String()
	for _, want := range []string{"<title>Today's menu</title>", `>Today&#39;s menu</h1>`, ">Espresso</p>", ">4.50</p>"} {
		if !strings.Contains(served, want) {
			t.Errorf("the tiny site has no %s:\n%.2000s", want, served)
		}
	}
	// CONTROL: the same page.ui with its price in a colour short of contrast is told
	if warned, _ := site(strings.Replace(files["ui"], `color="ink-30"`, `color="ink-70"`, 1)); !strings.Contains(warned, `color="ink-70"`) {
		t.Errorf("control: a colour short of contrast in the page.ui shown is not told: %q", warned)
	}
}

// The kitwork example's /docs/getting-started gives the one step whose commands differ between shells —
// ALLOW_LOCAL and PORT set for the session — as two tabs, bash and PowerShell, with persist="true": the
// instance is handed the set of their names, bash+powershell, to keep the choice under, the bash panel
// is painted shown and the PowerShell one hidden, each holds its shell's commands, and the pre-paint
// that shows a kept choice before the first frame follows the PowerShell panel, the last of the name.
// The other steps read the same in every shell, so they have no tabs: one tablist on the page.
func TestExampleShellTabsKeepTheReadersShell(t *testing.T) {
	_, page := examplePage(t, "/docs/getting-started")
	for _, want := range []string{
		`data-kitwork-component="tabs: $shell" role="tablist" aria-label="Shell" data-kitwork-scope="persist: &#39;bash+powershell&#39;; selected: &#39;bash&#39;"`,
		`id="kitwork-tabpanel-1-1" role="tabpanel" aria-labelledby="kitwork-tab-1-1" tabindex="0" data-kitwork-bind="show: $shell.selected == &#39;bash&#39;"><pre class="size-small"><code class="language-bash">`,
		`id="kitwork-tabpanel-1-2" role="tabpanel" aria-labelledby="kitwork-tab-1-2" tabindex="0" data-kitwork-bind="show: $shell.selected == &#39;powershell&#39;" hidden><pre class="size-small"><code class="language-powershell">`,
		`</code></pre></div><script data-kitwork-generated="tabs" data-kitwork-persist="bash+powershell" data-kitwork-tabs="kitwork-tab-1-1 kitwork-tab-1-2">`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("/docs/getting-started has no %s", want)
		}
	}
	commands := map[string]string{}
	for _, m := range regexp.MustCompile(`<code class="language-(bash|powershell)">([\s\S]*?)</code>`).FindAllStringSubmatch(page, -1) {
		commands[m[1]] += textContent(m[2]) + "\n"
	}
	for language, want := range map[string]string{
		"bash":       "export ALLOW_LOCAL=true\nexport PORT=8080\n",
		"powershell": "$env:ALLOW_LOCAL = \"true\"\n$env:PORT = \"8080\"\n",
	} {
		if !strings.HasPrefix(commands[language], want) {
			t.Errorf("the %s tab holds %q, want it to start with %q", language, commands[language], want)
		}
	}
	if got := strings.Count(page, `" role="tablist"`); got != 1 {
		t.Errorf("%d tablists on /docs/getting-started, want the shell's alone", got)
	}
}
