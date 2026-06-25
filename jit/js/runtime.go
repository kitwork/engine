// Package js is Kitwork's JIT JavaScript runtime — "jitjs", a sibling of jit/css and jit/icons.
// The author writes `data-kitwork-action="<verb>"`; Render scans the page and injects ONE
// `<script data-kitwork-jit="js">` holding the core delegated dispatcher plus ONLY the verb modules
// the page actually uses. No framework, no full-library payload — the jitcss model applied to JS.
// Verbs are delegated (one listener on document), so the runtime is inherently safe under Kitwork
// Drive: swapped-in markup just works, swapped-out markup leaks nothing.
//
// Each verb is one file in ./lib (core.js + copy.js, toggle.js, dismiss.js, tab.js, theme.js,
// dialog.js, …). Drop a `lib/<name>.js` that calls `window.kitwork.components.action("<name>", fn)`
// and it is emitted only on pages that use `data-kitwork-action="<name>"`. Heavy widgets use the
// platform, not JS: dropdown → popover, accordion → <details>, modal → <dialog> (the dialog verb
// only opens/closes it).
package js

import (
	"embed"
	"regexp"
	"sort"
	"strings"
	"sync"
)

//go:embed lib
var libFS embed.FS

// moduleCache memoizes parsed module files (and misses, stored as "") so each is read at most once.
var moduleCache sync.Map

// coreName is the always-included dispatcher module; it is reserved — never a verb.
const coreName = "core"

// runtimeMarker tags the injected <script> within the shared data-kitwork-jit namespace (value
// "js"), so Kitwork Drive's mergeHead re-runs it on navigation alongside the css/icons blocks.
const runtimeMarker = `data-kitwork-jit="js"`

var (
	// verbRe validates a verb slug; anchored, so a name can never escape the embedded lib dir.
	verbRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	// actionAttrRe extracts the verb from every data-kitwork-action="…" attribute.
	actionAttrRe = regexp.MustCompile(`data-kitwork-action="([a-z][a-z0-9-]*)"`)
)

// readModule returns the (trimmed) contents of lib/<name>.js, or "" if absent. Cached.
func readModule(name string) string {
	if v, ok := moduleCache.Load(name); ok {
		return v.(string)
	}
	s := ""
	if b, err := libFS.ReadFile("lib/" + name + ".js"); err == nil {
		s = strings.TrimSpace(string(b))
	}
	moduleCache.Store(name, s)
	return s
}

// HasVerb reports whether a verb has a module (and is not the reserved core).
func HasVerb(name string) bool { return name != coreName && readModule(name) != "" }

// scanVerbs collects the distinct verbs used in data-kitwork-action="…" that resolve to a module.
func scanVerbs(html string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, m := range actionAttrRe.FindAllStringSubmatch(html, -1) {
		n := m[1]
		if seen[n] || !verbRe.MatchString(n) || !HasVerb(n) {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// RuntimeJS concatenates the core dispatcher + each named verb module (deduped, sorted). "" if none.
func RuntimeJS(names []string) string {
	seen := make(map[string]bool)
	var verbs []string
	for _, n := range names {
		if !seen[n] && HasVerb(n) {
			seen[n] = true
			verbs = append(verbs, n)
		}
	}
	if len(verbs) == 0 {
		return ""
	}
	sort.Strings(verbs)
	var b strings.Builder
	b.WriteString(readModule(coreName))
	for _, n := range verbs {
		b.WriteByte('\n')
		b.WriteString(readModule(n))
	}
	return b.String()
}

// SiteRuntimeJS is the whole-tenant form: the union of verbs used across many templates (for a
// future /jitjs service route, mirroring jit/icons SiteCSS).
func SiteRuntimeJS(htmls ...string) string {
	seen := make(map[string]bool)
	var all []string
	for _, h := range htmls {
		for _, n := range scanVerbs(h) {
			if !seen[n] {
				seen[n] = true
				all = append(all, n)
			}
		}
	}
	return RuntimeJS(all)
}

// Render injects the per-page runtime as ONE `<script data-kitwork-jit="js">` before </head>.
// A cheap no-op when the page uses no verbs.
func Render(html string) string {
	if !strings.Contains(html, "data-kitwork-action=") {
		return html
	}
	js := RuntimeJS(scanVerbs(html))
	if js == "" {
		return html
	}
	tag := "<script " + runtimeMarker + ">" + js + "</script>"
	if i := strings.LastIndex(html, "</head>"); i >= 0 {
		return html[:i] + tag + html[i:]
	}
	return tag + html
}
