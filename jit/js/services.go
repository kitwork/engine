package js

// Native services: the capabilities a desktop or mobile shell offers a page — studioState,
// studioSqlite, studioDatabase, and the mobile ones that follow — as kit.<name>. They were KitJS's
// (removed 01/10) and are kept as they were written: one file per service, services/<name>/v<x>.js,
// each registering itself with kit.service and reaching the shell through the adapter the shell
// seeds at document-start (native/bridge). They went with KitJS by mistake: the Studio desktop app
// lost its state, its SQLite browser and its database connections, silently, because each call
// checks for its service first (found 02/10).
//
// A page gets a service the way it gets a component — one file, only when something on the page
// names it: kit.studioSqlite in the markup, or in the source of a component the page uses (Studio
// calls its services from its own component, never from markup). Before the first service comes
// one small file, the assembly (services/assembly.js), which hands every service what KitJS used to:
// the shell's host adapter, its file channel, and the list of service versions this engine carries.
// On the wire a service is /_kitwork/services/<name>.<hash>.js and the assembly
// /_kitwork/services/assembly.<hash>.js: no version in either name.

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

// serviceUseRe finds a name read off kit — kit.studioState, kitwork.nfc — in markup or in source.
var serviceUseRe = regexp.MustCompile(`\bkit(?:work)?\.([a-z][A-Za-z0-9]*)\b`)

// serviceRequiresRe reads a service's own note of what it needs loaded first:
// "// Requires capabilities@1.0.0 and files@1.3.0."
var serviceRequiresRe = regexp.MustCompile(`(?m)^// Requires (.*)$`)
var serviceRequiredNameRe = regexp.MustCompile(`([a-z][A-Za-z0-9]*)@`)

// ServiceNames lists the embedded services, sorted.
func ServiceNames() []string {
	entries, err := jsFS.ReadDir("services")
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() && slugCamelRe.MatchString(entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names
}

// slugCamelRe is a service name: lower camel case, as kit.<name> spells it.
var slugCamelRe = regexp.MustCompile(`^[a-z][A-Za-z0-9]*$`)

// serviceVersion is the version of the service this engine carries ("1.1.0"), or "" for none.
func serviceVersion(name string) string {
	if !slugCamelRe.MatchString(name) {
		return ""
	}
	entries, err := jsFS.ReadDir("services/" + name)
	if err != nil {
		return ""
	}
	latest := ""
	for _, entry := range entries {
		file := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(file, ".js") {
			continue
		}
		if latest == "" || versionLess(latest, file) {
			latest = file
		}
	}
	return strings.TrimSuffix(strings.TrimPrefix(latest, "v"), ".js")
}

// HasService reports whether the engine carries a native service by that name.
func HasService(name string) bool { return serviceVersion(name) != "" }

func serviceSource(name string) string {
	version := serviceVersion(name)
	if version == "" {
		return ""
	}
	key := "service:" + name
	if v, ok := moduleCache.Load(key); ok {
		return v.(string)
	}
	b, err := jsFS.ReadFile("services/" + name + "/v" + version + ".js")
	s := ""
	if err == nil {
		s = strings.TrimSpace(string(b))
	}
	moduleCache.Store(key, s)
	return s
}

// serviceLoadsAfter is an order a service needs but its file does not write down: files takes the
// upload boundary network leaves on the assembly as it loads, and refuses to load without it.
var serviceLoadsAfter = map[string][]string{"files": {"network"}}

// serviceRequires is what a service says it needs loaded before it, limited to the services this
// engine carries (capabilities, say, is the kernel's own and always there).
func serviceRequires(name string) []string {
	out := append([]string(nil), serviceLoadsAfter[name]...)
	for _, line := range serviceRequiresRe.FindAllStringSubmatch(serviceSource(name), -1) {
		for _, m := range serviceRequiredNameRe.FindAllStringSubmatch(line[1], -1) {
			if m[1] != name && HasService(m[1]) {
				out = append(out, m[1])
			}
		}
	}
	return out
}

// scanServices is the services the given sources name, each after the services it requires.
func scanServices(sources ...string) []string {
	var named []string
	seen := map[string]bool{}
	for _, source := range sources {
		for _, m := range serviceUseRe.FindAllStringSubmatch(source, -1) {
			if !seen[m[1]] && HasService(m[1]) {
				seen[m[1]] = true
				named = append(named, m[1])
			}
		}
	}
	sort.Strings(named)
	var ordered []string
	placed := map[string]bool{}
	var place func(name string, depth int)
	place = func(name string, depth int) {
		if placed[name] || depth > 16 {
			return
		}
		placed[name] = true
		for _, required := range serviceRequires(name) {
			place(required, depth+1)
		}
		ordered = append(ordered, name)
	}
	for _, name := range named {
		place(name, 0)
	}
	return ordered
}

// assemblySource is the file that comes before the first service. The graph lists every service
// this engine carries, not only the page's: a service checks a sibling's version by name, and the
// list being the same on every page is what lets this file be one file.
func assemblySource() string {
	graph := map[string]string{}
	for _, name := range ServiceNames() {
		graph[name] = serviceVersion(name)
	}
	encoded, _ := json.Marshal(graph)
	return strings.Replace(readAssemblyTemplate(), "__SERVICES__", string(encoded), 1)
}

func readAssemblyTemplate() string {
	b, err := jsFS.ReadFile("services/assembly.js")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// AssemblyAsset is the file every page with a service loads first.
func AssemblyAsset() *Asset {
	return cachedAsset("services:assembly", func() *Asset { return buildAsset("service", "assembly", assemblySource()) })
}

// serviceAsset is one service's file, named /_kitwork/services/<name>.<hash>.js. The version is the
// engine's own business, like a component's (11/10/2026): it stays in the source file's name and in
// the assembly's list, where a service checks a sibling by it, and never reaches the wire — the hash
// already tells two versions apart.
func serviceAsset(name string) *Asset {
	source := serviceSource(name)
	if source == "" {
		return nil
	}
	return cachedAsset("service:"+name+"@"+serviceVersion(name), func() *Asset { return buildAsset("service", name, source) })
}
