// Package js is Kitwork's JIT JavaScript runtime — "jitjs", a sibling of jit/css and jit/icons.
// The author writes `data-kit-action="<verb>"` (canonical; `data-kitwork-action` is a deprecated
// alias — see the prefix convention: data-kit-* = author-written, data-kitwork-* = engine-emitted).
// Render scans the page and injects ONE
// `<script data-kitwork-jit="js">` holding the core delegated dispatcher plus ONLY the verb modules
// the page actually uses. No framework, no full-library payload — the jitcss model applied to JS.
// Verbs are delegated (one listener on document), so the runtime is inherently safe under Kitwork
// Drive: swapped-in markup just works, swapped-out markup leaks nothing.
//
// Each verb is one file in ./lib (copy.js, toggle.js, dismiss.js, tab.js, theme.js, dialog.js, …).
// Drop a `lib/<name>.js` that calls `window.kitwork.components.action("<name>", fn)` and it is
// emitted only on pages that use `data-kit-action="<name>"`. Heavy widgets use the platform,
// not JS: dropdown → popover, accordion → <details>, modal → <dialog> (the dialog verb only
// opens/closes it).
//
// THE CORE IS THE UNIFIED KERNEL (engine/jit/hydrate/runtime.js): one window.kitwork root, one
// behavior registry, one set of delegated listeners shared with expressions/model/validate/live.
// Verb modules register into it through the kitwork.components compat surface; the kernel is
// boot-guarded, so double inclusion (inline bundle + /kit.js) is harmless.
package js

import (
	"embed"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	hydrate "github.com/kitwork/engine/jit/hydrate"
)

//go:embed lib components
var jsFS embed.FS

// moduleCache memoizes parsed module files (and misses, stored as "") so each is read at most once.
var moduleCache sync.Map

// coreName stays reserved so no verb module can ever shadow the kernel slot.
const coreName = "core"

// runtimeMarker tags the injected <script> within the shared data-kitwork-jit namespace (value
// "js"), so Kitwork Drive's mergeHead re-runs it on navigation alongside the css/icons blocks.
const runtimeMarker = `data-kitwork-jit="js"`

var (
	// verbRe validates a verb slug; anchored, so a name can never escape the embedded lib dir.
	verbRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	// componentNameRe validates a component slug name.
	componentNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	// componentVersionRe validates a component version suffix.
	componentVersionRe = regexp.MustCompile(`^v[0-9]+(\.[0-9]+)*$`)

	// actionAttrRe extracts the verb from every data-kit-action="…" (or deprecated
	// data-kitwork-action) attribute.
	actionAttrRe = regexp.MustCompile(`data-kit(?:work)?-action="([a-z][a-z0-9-]*)"`)
	// componentAttrRe extracts the component name and optional version suffix. The optional `=$alias`
	// tail (the client-side global handle, e.g. data-kit-component="sidebar@v1.0.0=$sidebar") is
	// matched but NOT captured: the alias is purely a runtime concern (kernel registers it), while the
	// server only needs (name, version) to pick which module to emit.
	componentAttrRe = regexp.MustCompile(`data-kit(?:work)?-component="([a-z][a-z0-9-]*)(?:@([v0-9.]+))?(?:=\$[A-Za-z][A-Za-z0-9_-]*)?"`)
)

// parseVersion converts a string like "v1.2.3.js" or "v1.2.3" into major, minor, patch ints.
func parseVersion(s string) (major, minor, patch int) {
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimSuffix(s, ".js")
	parts := strings.Split(s, ".")
	if len(parts) > 0 {
		major, _ = strconv.Atoi(parts[0])
	}
	if len(parts) > 1 {
		minor, _ = strconv.Atoi(parts[1])
	}
	if len(parts) > 2 {
		patch, _ = strconv.Atoi(parts[2])
	}
	return
}

// versionLess returns true if v1 is less than v2 semver-wise.
func versionLess(v1, v2 string) bool {
	maj1, min1, pat1 := parseVersion(v1)
	maj2, min2, pat2 := parseVersion(v2)
	if maj1 != maj2 {
		return maj1 < maj2
	}
	if min1 != min2 {
		return min1 < min2
	}
	return pat1 < pat2
}

// findLatestComponentVersion reads the components/<name> directory and returns the filename
// (e.g. "v2.0.0.js") of the latest version semver-wise. Returns "" if empty/absent.
func findLatestComponentVersion(name string) string {
	entries, err := jsFS.ReadDir("components/" + name)
	if err != nil || len(entries) == 0 {
		return ""
	}
	var latest string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		fname := entry.Name()
		if !strings.HasSuffix(fname, ".js") {
			continue
		}
		if latest == "" || versionLess(latest, fname) {
			latest = fname
		}
	}
	return latest
}

// readAction returns the (trimmed) contents of lib/<name>.js, or "" if absent. Cached.
func readAction(name string) string {
	key := "action:" + name
	if v, ok := moduleCache.Load(key); ok {
		return v.(string)
	}
	s := ""
	if b, err := jsFS.ReadFile("lib/" + name + ".js"); err == nil {
		s = strings.TrimSpace(string(b))
	}
	moduleCache.Store(key, s)
	return s
}

// readComponent returns the (trimmed) contents of components/<name>/<version>.js, or "" if absent. Cached.
func readComponent(nameWithVersion string) string {
	key := "component:" + nameWithVersion
	if v, ok := moduleCache.Load(key); ok {
		return v.(string)
	}

	s := ""
	parts := strings.SplitN(nameWithVersion, "@", 2)
	name := parts[0]

	var versionFile string
	if len(parts) == 2 {
		versionFile = parts[1] + ".js"
	} else {
		versionFile = findLatestComponentVersion(name)
	}

	if versionFile != "" {
		if b, err := jsFS.ReadFile("components/" + name + "/" + versionFile); err == nil {
			s = strings.TrimSpace(string(b))
		}
	}

	moduleCache.Store(key, s)
	return s
}

// HasAction reports whether an action has a module (and is not the reserved core).
func HasAction(name string) bool { return name != coreName && readAction(name) != "" }

// HasComponent reports whether a component has a module (and is not the reserved core).
func HasComponent(name string) bool { return name != coreName && readComponent(name) != "" }

// HasVerb reports whether a verb/component has a module (and is not the reserved core).
func HasVerb(name string) bool {
	return name != coreName && (readAction(name) != "" || readComponent(name) != "")
}

// scanVerbs collects the distinct actions and components used that resolve to a module.
// It returns their cache keys (e.g. "action:more", "component:copy@v1.0.0").
func scanVerbs(html string) []string {
	seen := make(map[string]bool)
	var out []string

	// Scan action verbs
	for _, m := range actionAttrRe.FindAllStringSubmatch(html, -1) {
		n := m[1]
		key := "action:" + n
		if seen[key] || !verbRe.MatchString(n) || !HasAction(n) {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}

	// Scan components
	for _, m := range componentAttrRe.FindAllStringSubmatch(html, -1) {
		name := m[1]
		version := m[2]

		var nameWithVersion string
		if version != "" {
			nameWithVersion = name + "@" + version
			if !componentVersionRe.MatchString(version) {
				continue
			}
		} else {
			nameWithVersion = name
		}

		if !componentNameRe.MatchString(name) {
			continue
		}

		key := "component:" + nameWithVersion
		if seen[key] || !HasComponent(nameWithVersion) {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}

	sort.Strings(out)
	return out
}

// RuntimeJS concatenates the unified kernel + each named module (deduped, sorted). "" if none.
func RuntimeJS(names []string) string {
	seen := make(map[string]bool)
	var keys []string
	for _, n := range names {
		var key string
		if strings.Contains(n, ":") {
			key = n
		} else {
			if HasAction(n) {
				key = "action:" + n
			} else if HasComponent(n) {
				key = "component:" + n
			}
		}

		if key != "" && !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}

	if len(keys) == 0 {
		return ""
	}

	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(strings.TrimSpace(hydrate.Runtime()))
	for _, k := range keys {
		parts := strings.SplitN(k, ":", 2)
		typ, name := parts[0], parts[1]
		b.WriteByte('\n')
		if typ == "action" {
			b.WriteString(readAction(name))
		} else if typ == "component" {
			b.WriteString(readComponent(name))
		}
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
// A cheap no-op when the page uses no verbs/components. Both prefixes are checked: the canonical
// authored form is data-kit-*, and a page that uses ONLY the short form must still get the runtime.
func Render(html string) string {
	if !strings.Contains(html, "data-kit-action=") &&
		!strings.Contains(html, "data-kitwork-action=") &&
		!strings.Contains(html, "data-kit-component=") &&
		!strings.Contains(html, "data-kitwork-component=") {
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
