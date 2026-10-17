package conformance

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/kitwork/engine/jit/hydrate"
	"github.com/kitwork/engine/ui"
	"github.com/kitwork/engine/ui/stylesheet"
)

// Kitwork UI conformance: a site written in .ui, the data each page receives, and the HTML the web
// target must produce — or, for a case that writes its own sources ("files"), the compile error the
// author must get, at the file, line and column to look at. The fixture is language-neutral (sources,
// JSON, HTML) so an engine written in another language can run the same cases.
//
//	go test ./conformance -run TestUIConformance -count=1
//	go test ./conformance -run TestUIConformance -update   (rewrite expected HTML, then review the diff)

var updateUI = flag.Bool("update", false, "rewrite the expected HTML of the UI conformance cases")

type uiManifest struct {
	SchemaVersion int    `json:"schema_version"`
	Site          string `json:"site"`
	Target        string `json:"target"`
	Reload        bool   `json:"reload"` // router.reload; false (the default) moves between pages without one
	Origin        string `json:"origin"`
	// Theme is the site's router.colors: each token the interface may name and its colour, or its
	// { light, dark } pair, over Kit Design's default palette (secondary text is ink-30, ink made lighter
	// in the markup, not a token). The contrast check measures every mode the palette has.
	Theme map[string]uiColor `json:"theme"`
	// Branches are the router.colors of folders below the site root, by their path from it: each merges
	// over the theme for the routes at or below that folder, the nearest last, as the engine merges a
	// branch's colours (quan-ly writes its own brand).
	Branches map[string]map[string]uiColor `json:"branches"`
	Shared   map[string]any                `json:"shared"`
	Cases    []uiCase                      `json:"cases"`
}

// uiColor is one token of router.colors: "#hex", or { "light": "#hex", "dark": "#hex" }.
type uiColor stylesheet.Color

func (c *uiColor) UnmarshalJSON(data []byte) error {
	var hex string
	if err := json.Unmarshal(data, &hex); err == nil {
		*c = uiColor{Light: hex}
		return nil
	}
	var pair struct {
		Light string `json:"light"`
		Dark  string `json:"dark"`
	}
	if err := json.Unmarshal(data, &pair); err != nil || pair.Light == "" {
		return fmt.Errorf("a colour is \"#hex\" or { \"light\": \"#hex\", \"dark\": \"#hex\" }, got %s", data)
	}
	*c = uiColor{Light: pair.Light, Dark: pair.Dark}
	return nil
}

type uiCase struct {
	Name      string            `json:"name"`
	Route     string            `json:"route"`
	Path      string            `json:"path"`
	NotFound  bool              `json:"notfound"` // the page a missing path gets (notfound.ui), answered with 404
	Scope     map[string]any    `json:"scope"`
	Functions map[string]string `json:"functions"`
	Expect    string            `json:"expect"`
	// Files are the case's own sources, by path from the site root ("../.components/cart.work" for the
	// identity's): the case compiles them instead of the fixture's site.
	Files map[string]string `json:"files"`
	// Error is the compile error the case must give instead of a page.
	Error *uiError `json:"error"`
	// Contrast is the site's router.contrast for the case: "strict" makes a pair of colours short of
	// contrast a compile error; "" or "warning" (the default) a warning of the page.
	Contrast string `json:"contrast"`
	// Warnings are the warnings the page compiles with, in order, each where it points and words its
	// message holds: a case that lists none must compile with none. A case with its own files and no
	// expect stops at them.
	Warnings []uiError `json:"warnings"`
}

// uiError is a compile error: where it points (file:line:col, the file from the site root) and words
// its message must hold.
type uiError struct {
	At   string `json:"at"`
	Says string `json:"says"`
}

func TestUIConformance(t *testing.T) {
	fixtures, err := filepath.Glob(filepath.Join("testdata", "ui", "*", "cases.json"))
	if err != nil || len(fixtures) == 0 {
		t.Fatalf("no UI fixtures found (%v)", err)
	}
	for _, manifestPath := range fixtures {
		dir := filepath.Dir(manifestPath)
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			t.Fatal(err)
		}
		var m uiManifest
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("%s: %v", manifestPath, err)
		}
		for _, c := range m.Cases {
			c := c
			t.Run(filepath.Base(dir)+"/"+c.Name, func(t *testing.T) {
				palette, err := themePalette(m.Theme, m.Branches, c.Route)
				if err != nil {
					t.Fatalf("%s: theme: %v", manifestPath, err)
				}
				site := &ui.Site{Root: filepath.Join(dir, m.Site), Target: m.Target, Reload: m.Reload, Palette: palette, Contrast: c.Contrast}
				if c.Files != nil {
					site = sourcesSite(site, c.Files)
				}
				compile := site.Compile
				if c.NotFound {
					compile = site.CompileNotFound
				}
				page, err := compile(filepath.Join(site.Root, filepath.FromSlash(c.Route)))
				if c.Error != nil {
					if err == nil {
						t.Fatalf("want the compile error %s: %s, the page compiled", c.Error.At, c.Error.Says)
					}
					if got := err.Error(); !strings.HasPrefix(got, c.Error.At+": ") || !strings.Contains(got, c.Error.Says) {
						t.Fatalf("want the compile error at %s saying %q,\n got %s", c.Error.At, c.Error.Says, got)
					}
					return
				}
				if err != nil {
					t.Fatalf("compile: %v", err)
				}
				warned := page.Warnings()
				if len(warned) != len(c.Warnings) {
					t.Fatalf("want %d warnings, the page compiled with %d: %v", len(c.Warnings), len(warned), warned)
				}
				for i, w := range c.Warnings {
					if got := warned[i].String(); !strings.HasPrefix(got, w.At+": ") || !strings.Contains(got, w.Says) {
						t.Errorf("want warning %d at %s saying %q, got %s", i+1, w.At, w.Says, got)
					}
				}
				if c.Files != nil && c.Expect == "" {
					return
				}
				scope := map[string]any{}
				for name, v := range c.Scope {
					scope[name] = resolveShared(t, v, m.Shared)
				}
				for name, source := range c.Functions {
					ir, err := hydrate.Compile(source)
					if err != nil {
						t.Fatalf("function %s: %v", name, err)
					}
					scope[name] = hydrate.Func(func([]any) any {
						v, _ := hydrate.Eval(ir, scope)
						return v
					})
				}
				got := page.Render(ui.Request{Path: c.Path, Origin: m.Origin, Scope: scope})
				if strings.Contains(got, "\n") {
					t.Fatalf("rendered body holds a line break; the golden format assumes none:\n%s", got)
				}
				// every class of every branch, not only this render's: the skeleton has them all
				css := string(page.Stylesheet().CSS)
				if missing := unstyledClasses(page.Skeleton()+got, css); len(missing) > 0 {
					t.Errorf("classes with no CSS (the page would be silently unstyled): %s", strings.Join(missing, " "))
				}
				if words := tailwindWords(page.Skeleton() + got); len(words) > 0 {
					t.Errorf("a .ui page carries no Tailwind word, found: %s", strings.Join(words, " "))
				}
				expectPath := filepath.Join(dir, filepath.FromSlash(c.Expect))
				if *updateUI {
					if err := os.MkdirAll(filepath.Dir(expectPath), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(expectPath, []byte(indentHTML(got)), 0o644); err != nil {
						t.Fatal(err)
					}
					return
				}
				want, err := os.ReadFile(expectPath)
				if err != nil {
					t.Fatalf("expected HTML missing (run with -update and review it): %v", err)
				}
				if flat := flattenHTML(string(want)); flat != got {
					t.Fatalf("HTML differs from %s\n--- want\n%s\n--- got\n%s", c.Expect, indentHTML(flat), indentHTML(got))
				}
			})
		}
	}
}

// sourcesSite is the fixture's site reading a case's own sources, by path from the site root.
func sourcesSite(site *ui.Site, files map[string]string) *ui.Site {
	s := *site
	s.Read = func(path string) ([]byte, error) {
		rel, err := filepath.Rel(site.Root, path)
		if err != nil {
			return nil, os.ErrNotExist
		}
		if source, ok := files[filepath.ToSlash(rel)]; ok {
			return []byte(source), nil
		}
		return nil, os.ErrNotExist
	}
	return &s
}

// resolveShared replaces "@shared.name" and "@shared.name[i]" with the shared value.
func resolveShared(t *testing.T, v any, shared map[string]any) any {
	s, ok := v.(string)
	if !ok || !strings.HasPrefix(s, "@shared.") {
		return v
	}
	ref := strings.TrimPrefix(s, "@shared.")
	index := -1
	if i := strings.Index(ref, "["); i >= 0 && strings.HasSuffix(ref, "]") {
		n, err := strconv.Atoi(ref[i+1 : len(ref)-1])
		if err != nil {
			t.Fatalf("bad shared reference %s", s)
		}
		ref, index = ref[:i], n
	}
	value, ok := shared[ref]
	if !ok {
		t.Fatalf("unknown shared reference %s", s)
	}
	if index < 0 {
		return value
	}
	list, ok := value.([]any)
	if !ok || index >= len(list) {
		t.Fatalf("shared reference %s is not a list entry", s)
	}
	return list[index]
}

// The expected HTML is stored indented so a person can review it; the indentation is only line
// breaks plus spaces between tags, which the renderer never writes, so it is removed before comparing.
var indentation = regexp.MustCompile(`\n *`)

func flattenHTML(s string) string { return strings.TrimSpace(indentation.ReplaceAllString(s, "")) }

var blockTags = map[string]bool{
	"body": true, "header": true, "footer": true, "nav": true, "main": true, "aside": true,
	"section": true, "div": true, "ul": true, "ol": true, "li": true, "form": true, "details": true,
	"summary": true, "p": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"blockquote": true, "select": true, "option": true, "script": true, "img": true, "pre": true,
	"button": true, "label": true, "input": true, "textarea": true,
}

var tagPattern = regexp.MustCompile(`<(/?)([a-z0-9]+)[^>]*>`)

// indentHTML puts each block element on its own line, indented by depth.
func indentHTML(s string) string {
	var b strings.Builder
	depth := 0
	last := 0
	void := map[string]bool{"img": true, "input": true}
	opened := "" // the block element whose opening tag was written last, with nothing after it
	for _, m := range tagPattern.FindAllStringSubmatchIndex(s, -1) {
		closing := s[m[2]:m[3]] == "/"
		name := s[m[4]:m[5]]
		if m[0] > last {
			opened = ""
		}
		b.WriteString(s[last:m[0]])
		last = m[1]
		if !blockTags[name] {
			b.WriteString(s[m[0]:m[1]])
			opened = ""
			continue
		}
		if closing {
			depth--
			if opened != name && strings.HasSuffix(b.String(), ">") && endsWithBlock(b.String()) {
				b.WriteString("\n" + strings.Repeat("  ", depth))
			}
			b.WriteString(s[m[0]:m[1]])
			opened = ""
			continue
		}
		opened = name
		if void[name] {
			opened = ""
		}
		if b.Len() > 0 {
			b.WriteString("\n" + strings.Repeat("  ", depth))
		}
		b.WriteString(s[m[0]:m[1]])
		if !void[name] {
			depth++
		}
	}
	b.WriteString(s[last:])
	return b.String() + "\n"
}

// endsWithBlock reports whether the text written so far ends with a block tag, so the closing tag
// goes on its own line; after inline content it stays on the same line.
func endsWithBlock(s string) bool {
	i := strings.LastIndex(s, "<")
	if i < 0 {
		return false
	}
	m := tagPattern.FindStringSubmatch(s[i:])
	return m != nil && blockTags[m[2]]
}

// themePalette is a route's colour chain as the engine builds it (work/router_ui.go, routeStyleOf):
// the fixture's router.colors, then those of each branch folder the route is at or below, the nearest
// last, over Kit Design's default palette.
func themePalette(colors map[string]uiColor, branches map[string]map[string]uiColor, route string) (*stylesheet.Palette, error) {
	chain := []map[string]stylesheet.Color{routerColors(colors)}
	var folders []string
	for folder := range branches {
		if r, f := "/"+path.Clean(route)+"/", "/"+path.Clean(folder)+"/"; strings.HasPrefix(r, f) {
			folders = append(folders, folder)
		}
	}
	sort.Slice(folders, func(i, j int) bool { return len(path.Clean(folders[i])) < len(path.Clean(folders[j])) })
	for _, folder := range folders {
		chain = append(chain, routerColors(branches[folder]))
	}
	return stylesheet.NewPalette(false, chain...)
}

// routerColors is one folder's router.colors as the palette reads it.
func routerColors(colors map[string]uiColor) map[string]stylesheet.Color {
	out := make(map[string]stylesheet.Color, len(colors))
	for name, c := range colors {
		out[name] = stylesheet.Color(c)
	}
	return out
}

// classAttribute is the class attribute alone: the whitespace before it keeps data-kitwork-class (whose
// value is pairs of a class and a condition, not a list) from being read as one.
var classAttribute = regexp.MustCompile(`\sclass="([^"]*)"`)

// elementWord reports a class with no page rule of its own: a word the engine writes that reboot.css
// styles (the direction, the tag word of a shared element, the parent of a placed child), or an icon
// or a logo (jit/icons and jit/logo make their CSS).
func elementWord(c string) bool {
	if strings.HasPrefix(c, "icon-") || strings.HasPrefix(c, "logo-") {
		return true
	}
	for _, w := range stylesheet.ElementWords() {
		if c == w {
			return true
		}
	}
	return false
}

// selectorEscape writes a class as the page's stylesheet names it: the five characters a .ui class
// keeps from its source are escaped, and % (width-50%).
var selectorEscape = strings.NewReplacer(":", `\:`, "+", `\+`, ",", `\,`, ".", `\.`, "/", `\/`, "%", `\%`)

// unstyledClasses lists the classes of an HTML body for which the page's stylesheet has no rule: a
// class whose rule starts ".class{" or ".class:where(" (a state, or a rule scoped to an element). A word
// reboot.css styles must be styled there. The language of a code element (language-go) is HTML's own
// way to name it and nothing styles it: it needs no rule.
func unstyledClasses(body, css string) []string {
	reboot := string(stylesheet.Reboot().CSS)
	seen := map[string]bool{}
	var missing []string
	for _, m := range classAttribute.FindAllStringSubmatch(body, -1) {
		for _, c := range strings.Fields(m[1]) {
			if seen[c] || strings.HasPrefix(c, "language-") {
				continue
			}
			seen[c] = true
			if elementWord(c) {
				if !strings.HasPrefix(c, "icon-") && !strings.HasPrefix(c, "logo-") && !strings.Contains(reboot, "."+c) {
					missing = append(missing, c+" (reboot.css)")
				}
				continue
			}
			selector := "." + selectorEscape.Replace(c)
			if !strings.Contains(css, selector+"{") && !strings.Contains(css, selector+":where(") {
				missing = append(missing, c)
			}
		}
	}
	return missing
}

// tailwindPatterns are the words of Tailwind's dialect a .ui page must never carry: its width prefixes,
// its utilities (p-4, bg-, text-, justify-, items-, rounded-…), its arbitrary values ([…]) and the
// bare display words. A .ui class is the attribute as written (padding-4, background-brand, gap-4).
var tailwindPatterns = regexp.MustCompile(`^(?:(?:sm|md|lg|xl|2xl):.*|bg-.*|text-.*|justify-.*|items-.*|rounded.*|p[xytblrse]?-[0-9].*|m[xytblr]?-.*|` +
	`(?:w|h|min-w|min-h|max-w|max-h)-.*|.*\[.*|flex|inline-flex|flex-col|flex-1|hidden|block|relative|absolute|fixed|sticky|group|uppercase|underline|` +
	`font-(?:thin|light|normal|medium|semibold|bold|mono|sans)|grid-cols-.*|col-span-.*|tracking-.*|leading-.*|z-[0-9]+|line-clamp-.*|` +
	`transition.*|shadow-(?:sm|md|lg|xl|2xl)|border-0|list-none|cursor-.*|overflow-.*|inset-.*|top-.*|right-.*|bottom-.*|left-.*)$`)

// tailwindWords lists the Tailwind words among the classes of an HTML body.
func tailwindWords(body string) []string {
	seen := map[string]bool{}
	var found []string
	for _, m := range classAttribute.FindAllStringSubmatch(body, -1) {
		for _, c := range strings.Fields(m[1]) {
			// the engine's own words (transition-fade, transition-none) are reboot.css's, never Tailwind's
			if !seen[c] && !elementWord(c) && tailwindPatterns.MatchString(c) {
				seen[c] = true
				found = append(found, c)
			}
		}
	}
	return found
}

// The Tailwind check catches what the compiler wrote before Kitwork UI had its own CSS (CONTROL: the
// old output of the menu's header and a card), and passes what it writes now.
func TestTailwindWordsAreFound(t *testing.T) {
	old := `<header class="flex flex-row justify-between gap-4 p-4 md:p-6 items-center w-full max-w-[1280px] [margin-inline:auto]">` +
		`<div class="bg-paper rounded-xl flex flex-col gap-3 col-span-2"><p class="text-xs uppercase tracking-[0.14em]">x</p></div></header>`
	if got := strings.Join(tailwindWords(old), " "); got != "flex justify-between p-4 md:p-6 items-center w-full max-w-[1280px] [margin-inline:auto] bg-paper rounded-xl flex-col col-span-2 text-xs uppercase tracking-[0.14em]" {
		t.Errorf("CONTROL: the old output's Tailwind words, got %q", got)
	}
	now := `<header class="flex-row align-center space-between gap-4 padding-4 medium:padding-6 container-wide">` +
		`<div class="card flex-column gap-3 span-2 width-50% max-width-68ch aspect-ratio-16/9 pin-top"><p class="size-mini case-upper color-ink-30 hover:background-brand+15 color-brand+15/40">x</p></div></header>`
	if got := tailwindWords(now); len(got) != 0 {
		t.Errorf("the words .ui writes are not Tailwind's, found %v", got)
	}
}
