// Package js is Kitwork's JIT JavaScript runtime — "jitjs", a sibling of jit/css and jit/icons.
// A page that uses a kernel component (`data-kitwork-component="tabs: $files"`, what the engine
// writes for <tabs as="$files">) gets the kernel plus ONLY the components it names. No framework, no
// full-library payload — the jitcss model applied to JS.
//
// Each component is a file in ./components/<name>/<version>.js registering a `kitwork.component`
// blueprint under its bare name — never a versioned one (11/10/2026): the latest version ships. The
// catalogue is what Kitwork UI's tags deliver; the components only template pages used, their older
// versions and a site's own _components/<name>.js went with template pages' client on 26/10/2026.
//
// THE CORE IS the kernel behind the canonical window.kitwork root (window.kitwork remains a
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

//go:embed components services directives parts
var jsFS embed.FS

// moduleCache memoizes parsed module files (and misses, stored as "") so each is read at most once.
var moduleCache sync.Map

// coreName stays reserved so no module can ever shadow the kernel slot.
const coreName = "core"

// runtimeMarker tags the injected <script> within the shared data-kitwork-generated namespace (value
// "js"), so Kitwork Drive's mergeHead re-runs it on navigation alongside the css/icons blocks.
const runtimeMarker = `data-kitwork-generated="runtime"`

var (
	// slugRe validates a module slug; anchored, so a name can never escape the embedded dirs.
	slugRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	// componentNameRe validates a component slug name.
	componentNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	// componentVersionRe validates a component version suffix.
	componentVersionRe = regexp.MustCompile(`^v[0-9]+(\.[0-9]+)*$`)

	// groupedComponentAttrRe finds the engine's form, data-kitwork-component="name[: $alias]"
	// (KITWORK_UI.md §11). Its value is read by hydrate.ComponentValue, the reader the kernel's twin
	// is held to; the alias goes. The engine never writes a version there.
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

// scanComponents collects the distinct catalogue components a page's hosts name, as cache keys
// ("component:tabs"). What a <pre>/<code> block prints is an example, not a host: a docs page must
// not download the component it documents. A name the catalogue does not have is left out (the
// kernel reports the host).
func scanComponents(html string) []string {
	html = hydrate.WithoutExamples(html)
	if !strings.Contains(html, "data-kitwork-component=") {
		return nil
	}
	seen := make(map[string]bool)
	var out []string
	for _, m := range groupedComponentAttrRe.FindAllStringSubmatch(html, -1) {
		name, _, err := hydrate.ComponentValue(htmlattr.Decode(m[1]))
		if err != nil || !componentNameRe.MatchString(name) || !HasComponent(name) {
			continue // Check names a value that does not read
		}
		if key := "component:" + name; !seen[key] {
			seen[key] = true
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// ModuleKeys returns canonical component:<name> keys for the names given (bare, or already keys).
// Unknown names are omitted, so arbitrary input cannot create arbitrary asset variants.
func ModuleKeys(names []string) []string {
	seen := make(map[string]bool)
	var keys []string
	for _, n := range names {
		name := strings.TrimPrefix(n, "component:")
		if !componentNameRe.MatchString(name) || !HasComponent(name) || seen[name] {
			continue
		}
		seen[name] = true
		keys = append(keys, "component:"+name)
	}
	sort.Strings(keys)
	return keys
}
