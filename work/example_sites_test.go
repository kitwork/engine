package work

import (
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/kitwork/engine/ui/stylesheet"
)

// examplesRoot is the Kitwork UI examples beside the engine (docs/examples of the repository the
// engine lives in): a checkout of the engine alone has none, and the test then has nothing to read.
const examplesRoot = "../../docs/examples"

// tailwindClass matches the words of Tailwind's dialect a .ui page never carries: its width
// prefixes, its utilities, its arbitrary values and its bare display words. A .ui class is the
// attribute as written (padding-4, background-brand, medium:padding-6).
var tailwindClass = regexp.MustCompile(`^(?:(?:sm|md|lg|xl|2xl):.*|bg-.*|text-.*|justify-.*|items-.*|rounded.*|p[xytblrse]?-[0-9].*|m[xytblr]?-.*|` +
	`(?:w|h|min-w|min-h|max-w|max-h)-.*|.*\[.*|flex|inline-flex|flex-col|flex-1|hidden|block|relative|absolute|fixed|sticky|group|uppercase|` +
	`font-(?:thin|light|normal|medium|semibold|bold|mono|sans)|grid-cols-.*|col-span-.*|tracking-.*|leading-.*|z-[0-9]+|line-clamp-.*|` +
	`transition.*|shadow-(?:sm|md|lg|xl|2xl)|border-0|list-none|cursor-.*|overflow-.*|inset-.*)$`)

var classAttributeRe = regexp.MustCompile(`\sclass="([^"]*)"`)
var localLinkRe = regexp.MustCompile(`href="(/[^"#?]*)"`)

// tailwindWordsIn lists the Tailwind words among a page's classes.
func tailwindWordsIn(page string) []string {
	var found []string
	for _, m := range classAttributeRe.FindAllStringSubmatch(page, -1) {
		for _, c := range strings.Fields(m[1]) {
			if tailwindClass.MatchString(c) {
				found = append(found, c)
			}
		}
	}
	return found
}

// Every page of the example sites, as the engine serves them, is in Kitwork UI's own words: no class
// of Tailwind's dialect, no <style> but the engine's (router.bundle writes the page's CSS in it), every
// class's rule in the stylesheets it has, linked or written in the page. The pages
// are found from each site's home by its links. CONTROL: the detector finds the words of the page
// the compiler wrote before Kitwork UI had its own CSS.
func TestExampleSitesCarryNoTailwindWord(t *testing.T) {
	old := `<header class="flex flex-row justify-between gap-4 p-4 md:p-6 items-center w-full max-w-[1280px] [margin-inline:auto]"><p class="text-xs uppercase tracking-[0.14em]">x</p></header>`
	if got := strings.Join(tailwindWordsIn(old), " "); got != "flex justify-between p-4 md:p-6 items-center w-full max-w-[1280px] [margin-inline:auto] text-xs uppercase tracking-[0.14em]" {
		t.Fatalf("CONTROL: the old output's Tailwind words, got %q", got)
	}
	if _, err := os.Stat(examplesRoot); err != nil {
		t.Skipf("no examples beside the engine (%v)", err)
	}
	// boot a copy: a tenant writes what it keeps (.data, caches) into its folders
	root := t.TempDir()
	err := filepath.WalkDir(examplesRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(examplesRoot, path)
		if d.IsDir() {
			if d.Name() == ".data" || d.Name() == ".persist" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(root, rel), 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(root, rel), data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	saved := AllowLocal
	AllowLocal = true
	t.Cleanup(func() { AllowLocal = saved })
	// the kitwork site at least (the coffee shop left docs/examples on 13/10/2026 and lives on as the
	// conformance fixture): a missing site fails here instead of passing with nothing read
	hosts, _ := filepath.Glob(filepath.Join(root, "*", "*.localhost"))
	kitwork := false
	for _, dir := range hosts {
		kitwork = kitwork || filepath.Base(dir) == "kitwork.localhost"
	}
	if !kitwork {
		t.Fatalf("thiếu site ví dụ kitwork.localhost, chỉ thấy %v", hosts)
	}
	pages := 0
	for _, dir := range hosts {
		host := filepath.Base(dir)
		tenant := NewTenant(root, host)
		if err := tenant.Run(); err != nil {
			t.Fatalf("%s: %v", host, err)
		}
		seen := map[string]bool{"/": true}
		queue := []string{"/"}
		for len(queue) > 0 && len(seen) < 60 {
			path := queue[0]
			queue = queue[1:]
			rec := httptest.NewRecorder()
			tenant.Serve(rec, httptest.NewRequest(http.MethodGet, "http://"+host+path, nil))
			page := rec.Body.String()
			if !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
				continue
			}
			if rec.Code >= 500 {
				t.Errorf("%s%s: %d\n%.300s", host, path, rec.Code, page)
				continue
			}
			pages++
			if words := tailwindWordsIn(page); len(words) > 0 {
				t.Errorf("%s%s carries Tailwind words: %s", host, path, strings.Join(words, " "))
			}
			// the only <style> a page carries is the engine's, router.bundle writing the CSS in the page
			if styles, generated := strings.Count(page, "<style"), len(inlineStylesheetRe.FindAllString(page, -1)); styles != generated {
				t.Errorf("%s%s carries %d <style>, %d of them the engine's (data-kitwork-generated)", host, path, styles, generated)
			}
			css := ""
			for _, sheet := range regexp.MustCompile(`href="(/_kitwork/[a-z]+\.[0-9a-f]{16}\.css)"`).FindAllStringSubmatch(page, -1) {
				r := httptest.NewRecorder()
				tenant.Serve(r, httptest.NewRequest(http.MethodGet, "http://"+host+sheet[1], nil))
				css += r.Body.String()
			}
			for _, block := range inlineStylesheetRe.FindAllStringSubmatch(page, -1) {
				css += block[2]
			}
			for _, m := range classAttributeRe.FindAllStringSubmatch(page, -1) {
				for _, c := range strings.Fields(m[1]) {
					if strings.HasPrefix(c, "language-") {
						continue // the language of a code element (language-go): HTML's own name for it, no rule styles it
					}
					selector := "." + strings.NewReplacer(":", `\:`, "+", `\+`, ",", `\,`, ".", `\.`, "/", `\/`, "%", `\%`).Replace(c)
					if !strings.Contains(css, selector) {
						t.Errorf("%s%s: class %s has no rule in the stylesheets the page links", host, path, c)
					}
				}
			}
			for _, link := range localLinkRe.FindAllStringSubmatch(page, -1) {
				next := link[1]
				if strings.HasPrefix(next, "/_kitwork/") || strings.HasPrefix(next, "/assets/") || seen[next] {
					continue
				}
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	// the kitwork site alone links six pages at least (home, docs, getting started, components,
	// roadmap, about): fewer means the crawl stopped short
	if pages < 6 {
		t.Errorf("chỉ đọc được %d trang của các site ví dụ", pages)
	}
	t.Logf("%d pages of %d sites read", pages, len(hosts))
}

// oldColourSpelling matches the colour spellings removed on 13/10/2026 in front of a name the engine
// knows (a role, a named colour, a gradient, a backdrop): the shade X.N, the rungs X.soft, X.wash and
// X.deep, and the glass words glass.thin, glass-thick, glass.70. Group 1 is the name, group 2 what
// followed its dot; for a glass word group 3 is the word.
func oldColourSpelling(t *testing.T) *regexp.Regexp {
	t.Helper()
	palette, err := stylesheet.NewPalette(false)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{"white": true, "black": true}
	for _, list := range [][]string{palette.Tokens(), stylesheet.NamedColours(), stylesheet.StandardGradients(), stylesheet.Backdrops(), stylesheet.WebGradients()} {
		for _, name := range list {
			seen[name] = true
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		return len(names[i]) > len(names[j]) || len(names[i]) == len(names[j]) && names[i] < names[j]
	})
	for i, name := range names {
		names[i] = regexp.QuoteMeta(name)
	}
	return regexp.MustCompile(`(?:^|[^A-Za-z0-9_.])(?:(` + strings.Join(names, "|") + `)\.([0-9]+|soft|wash|deep)|glass[.\-](thin|thick|[0-9]+))(?:[^A-Za-z0-9]|$)`)
}

// newColourSpelling is what an old spelling is written as today (the operators of 13/10/2026): for a
// colour or a gradient the fix the engine's own message carries (Problem.Suggestion of the stylesheet
// table reading it), so the guard never tells an author to write a spelling the engine refuses — a
// fixed colour's shade is its alpha (white.50 is white/50, burnt-orange.40 burnt-orange/40), never -N.
// "" when the engine has no single fix; oldSpellingFix then says the engine's reason.
func newColourSpelling(m []string) string {
	name, after, glass := m[1], m[2], m[3]
	switch {
	case glass == "thin":
		return "glass/60"
	case glass == "thick":
		return "glass/88"
	case glass != "":
		return "glass/" + glass
	}
	if p := oldSpellingProblem(name, after); p != nil {
		return p.Suggestion
	}
	return ""
}

// oldSpellingProblem is the stylesheet table's refusal of name.after as a background (which takes a
// colour and a gradient alike), on Kit Design's default palette.
func oldSpellingProblem(name, after string) *stylesheet.Problem {
	_, err := stylesheet.NewTable(nil).Class(nil, stylesheet.Intent{Name: "background", Value: name + "." + after})
	var p *stylesheet.Problem
	if errors.As(err, &p) {
		return p
	}
	return nil
}

// oldSpellingFix is what the guard tells an author: write the new spelling, or the engine's reason
// when it has no single fix (ink.150, white.150).
func oldSpellingFix(m []string) string {
	if fix := newColourSpelling(m); fix != "" {
		return "viết " + fix
	}
	if p := oldSpellingProblem(m[1], m[2]); p != nil {
		return strings.TrimPrefix(p.Message, `background="`+m[1]+"."+m[2]+`": `)
	}
	return "không còn cách viết ấy"
}

// The .ui and .work sources of the examples and of the conformance fixture are written in the colour
// operators of 13/10/2026 (X-N lighter, X+N stronger, X/N alpha, glass/N): no old spelling anywhere,
// not even in a page's prose (<code>ink.70</code>) or in data a page reads, which the compiler never
// reads as a colour. CONTROL: the detector finds every old spelling planted in a line, and none of
// today's.
func TestExampleSourcesUseTheOperators(t *testing.T) {
	old := oldColourSpelling(t)
	for planted, want := range map[string]string{
		`color="ink.70"`:                "ink-30",
		`<code>cirrus.30</code>`:        "cirrus-70",
		`'danger.deep'`:                 "danger+15",
		`background="brand.soft/0"`:     "brand-85",
		`hover:background="info.wash"`:  "info-95",
		`color-ink.100`:                 "ink",
		`material="glass.thin"`:         "glass/60",
		`scrolled:material=glass-thick`: "glass/88",
		`glass.70`:                      "glass/70",
		`color="burnt-orange.40"`:       "burnt-orange/40",
		`color="white.50"`:              "white/50",
		`brand.on.80`:                   "brand.on-20",
		`color="ink.150"`:               "",
	} {
		m := old.FindStringSubmatch(planted)
		if m == nil {
			t.Errorf("CONTROL: không thấy cách viết cũ trong %s", planted)
		} else if got := newColourSpelling(m); got != want {
			t.Errorf("CONTROL: %s gợi ý %q, muốn %q", planted, got, want)
		}
	}
	// CONTROL: what the guard tells compiles, and where there is no single fix it says the engine's reason
	table := stylesheet.NewTable(nil)
	for _, fix := range []string{"ink-30", "cirrus-70", "danger+15", "brand-85", "info-95", "ink", "burnt-orange/40", "white/50", "brand.on-20"} {
		if _, err := table.Class(nil, stylesheet.Intent{Name: "background", Value: fix}); err != nil {
			t.Errorf("CONTROL: cách viết mới %s không biên dịch được: %v", fix, err)
		}
	}
	if got := oldSpellingFix(old.FindStringSubmatch(`color="ink.150"`)); !strings.HasPrefix(got, "cách viết .N đã bỏ (13/10/2026): nhạt hơn là ink-N") {
		t.Errorf("CONTROL: ink.150 được bảo %q, muốn lời của engine", got)
	}
	for _, now := range []string{`color="ink-30"`, `hover:background="brand+15/40"`, `material="glass/60"`, `color="brand.on"`,
		`background="cirrus-70"`, `link.70`, `scale="95"`, `version 1.26`, `go1.26`, `node 20.11`, `#f5225f`} {
		if m := old.FindString(now); m != "" {
			t.Errorf("CONTROL: %s bị coi là cách viết cũ (%q)", now, m)
		}
	}
	roots := []string{filepath.Join("..", "conformance", "testdata", "ui")}
	if _, err := os.Stat(examplesRoot); err == nil {
		roots = append(roots, examplesRoot)
	}
	files := 0
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == ".data" || d.Name() == ".persist" {
					return filepath.SkipDir
				}
				return nil
			}
			if ext := filepath.Ext(path); ext != ".ui" && ext != ".work" {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files++
			for i, line := range strings.Split(string(data), "\n") {
				for _, at := range old.FindAllStringSubmatchIndex(line, -1) {
					m := make([]string, 4)
					for g := 1; g < 4; g++ {
						if at[2*g] >= 0 {
							m[g] = line[at[2*g]:at[2*g+1]]
						}
					}
					start, end := at[2], at[5] // the name to what followed its dot
					if start < 0 {
						start, end = strings.Index(line[at[0]:], "glass")+at[0], at[7]
					}
					t.Errorf("%s:%d:%d: %q là cách viết cũ (bỏ ngày 13/10/2026): %s", filepath.ToSlash(path), i+1, start+1,
						line[start:end], oldSpellingFix(m))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if files < 20 {
		t.Fatalf("chỉ đọc được %d tệp .ui/.work từ %v", files, roots)
	}
	t.Logf("%d tệp .ui/.work đọc từ %v", files, roots)
}
