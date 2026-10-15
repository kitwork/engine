// Package js is Kitwork's JIT JavaScript runtime — "jitjs", a sibling of jit/css and jit/icons.
// A page that uses a kernel component (`data-kit-component="dialog"`) gets the kernel plus ONLY the
// components it names. No framework, no full-library payload — the jitcss model applied to JS.
//
// Each component is a file in ./components/<name>/<version>.js registering a `kit.component`
// blueprint under its bare name — never a versioned one (11/10/2026): which version's source ships
// is the engine's business, the latest unless a template page's markup names one. The verb system
// that used to live beside them (`data-kit-action="copy"` + `data-kit-target`, ./lib) is gone
// (22/09): behaviour is a component or an expression, transport is Drive. The optional capability
// modules (data-kit-api, -live, -remember) went on 02/10: nothing used them, and a site that keeps
// something in the browser does it in a component of its own.
//
// THE CORE IS the kernel behind the canonical window.kit root (window.kitwork remains a
// compatibility alias) and one delegated event system. Deliver (delivery.go) gives a page the
// kernel and its components as one file per part; there is no composed runtime any more.
package js

import (
	"embed"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	hydrate "github.com/kitwork/engine/jit/hydrate"
	"github.com/kitwork/engine/jit/internal/htmlattr"
)

//go:embed components services directives
var jsFS embed.FS

// moduleCache memoizes parsed module files (and misses, stored as "") so each is read at most once.
var moduleCache sync.Map

// coreName stays reserved so no module can ever shadow the kernel slot.
const coreName = "core"

// runtimeMarker tags the injected <script> within the shared data-kitwork-generated namespace (value
// "js"), so Kitwork Drive's mergeHead re-runs it on navigation alongside the css/icons blocks.
const runtimeMarker = `data-kitwork-generated="runtime"`

// legacyRuntimeMarker is the same tag under its name before 10/10/2026 (data-kitwork-jit): a page
// rendered then, and still in a cache, already carries the kernel and must not get a second one.
const legacyRuntimeMarker = `data-kitwork-jit="runtime"`

var (
	// slugRe validates a module slug; anchored, so a name can never escape the embedded dirs.
	slugRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	// componentNameRe validates a component slug name.
	componentNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	// componentVersionRe validates a component version suffix.
	componentVersionRe = regexp.MustCompile(`^v[0-9]+(\.[0-9]+)*$`)

	// componentAttrRe extracts the component name and optional version suffix. The alias is its own
	// attribute (data-kit-alias, a runtime concern the kernel registers); the server only needs
	// (name, version) to pick which module to emit.
	componentAttrRe = regexp.MustCompile(`data-kit-component="([a-z][a-z0-9-]*)(?:@([v0-9.]+))?"`)

	// groupedComponentAttrRe finds the engine's form, data-kitwork-component="name[: $alias]"
	// (KITWORK_UI.md §11). Its value is read by hydrate.ComponentValue, the reader the kernel's twin
	// is held to; the alias goes. The .ui engine never writes a version there, but the reader takes
	// "name@version" (with or without its "v") from a template page that writes one.
	groupedComponentAttrRe = regexp.MustCompile(`data-kitwork-component="([^"]*)"`)
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

// HasComponent reports whether a component has a module (and is not the reserved core).
func HasComponent(name string) bool { return name != coreName && readComponent(name) != "" }

// ComponentSource is the authored module of one catalogue component, as a page would read it to
// show what it runs: "" when there is no such component. The version may be written with or
// without its "v" ("1.0.0" or "v1.0.0"), and empty means the latest. Names and versions are checked
// against the same patterns markup is, so the receiver can only ever address one embedded file.
func ComponentSource(name, version string) string {
	if !componentNameRe.MatchString(name) || name == coreName {
		return ""
	}
	if version == "" {
		return readComponent(name)
	}
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	if !componentVersionRe.MatchString(version) {
		return ""
	}
	return readComponent(name + "@" + version)
}

// scanComponents collects the distinct components used that resolve to a module — embedded, or
// the site's own (site.go). It returns their cache keys (e.g. "component:dialog",
// "component:copy@v1.0.0", "component:bid-stepper"). What a <pre>/<code> block prints is an
// example, not a host: a docs page must not download the component it documents.
func scanComponents(html string, site Site) []string {
	html = hydrate.WithoutExamples(html)
	seen := make(map[string]bool)
	var out []string

	found := componentAttrRe.FindAllStringSubmatch(html, -1)
	for _, m := range found {
		// A template page may write the version with or without its "v" (dialog@2.0.0), as the
		// grouped form may: both mean the catalogue's v2.0.0.
		if m[2] != "" {
			m[2] = "v" + strings.TrimPrefix(m[2], "v")
		}
	}
	var grouped [][]string
	if strings.Contains(html, "data-kitwork-component=") {
		grouped = groupedComponentAttrRe.FindAllStringSubmatch(html, -1)
	}
	for _, m := range grouped {
		named, _, err := hydrate.ComponentValue(htmlattr.Decode(m[1]))
		if err != nil {
			continue // Check names it
		}
		name, version, _ := strings.Cut(named, "@")
		if version != "" {
			version = "v" + strings.TrimPrefix(version, "v")
		}
		found = append(found, []string{m[0], name, version})
	}
	for _, m := range found {
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
		if seen[key] || !hasComponent(nameWithVersion, site) {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}

	sort.Strings(out)
	return out
}

func moduleKeys(names []string, site Site) []string {
	seen := make(map[string]bool)
	var keys []string
	for _, n := range names {
		var key string
		if strings.Contains(n, ":") {
			parts := strings.SplitN(n, ":", 2)
			if parts[0] == "component" {
				name := strings.SplitN(parts[1], "@", 2)[0]
				if componentNameRe.MatchString(name) && hasComponent(parts[1], site) {
					key = n
				}
			}
		} else if hasComponent(n, site) {
			key = "component:" + n
		}

		if key != "" && !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}

	if len(keys) == 0 {
		return nil
	}

	sort.Strings(keys)
	return keys
}

// ModuleKeys returns canonical component:<name> keys for the runtime route.
// Unknown names are omitted, so arbitrary query strings cannot create arbitrary asset variants.
func ModuleKeys(names []string) []string { return moduleKeys(names, nil) }

// ModuleKeysFor is ModuleKeys for one tenant: its own _components count as known names.
func ModuleKeysFor(names []string, site Site) []string { return moduleKeys(names, site) }
