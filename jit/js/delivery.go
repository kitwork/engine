package js

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"sync"

	hydrate "github.com/kitwork/engine/jit/hydrate"
	"github.com/kitwork/engine/utilities/minifier"
)

// How a page gets its JavaScript: one file per part, each named by its own content.
//
//	<script data-kitwork-generated="runtime"    src="/_kitwork/kernel.9f2c41d0a7b3e815.js" defer></script>
//	<script data-kitwork-generated="drive"      src="/_kitwork/drive.51e0c2….js" defer></script>
//	<script data-kitwork-generated="directive"  src="/_kitwork/directives/intersect.0b7d….js" defer></script>
//	<script data-kitwork-generated="service"    src="/_kitwork/services/studioState.e2a9….js" defer></script>
//	<script data-kitwork-generated="component"  src="/_kitwork/components/menu.c47a….js" defer></script>
//
// Each name is <role>.<hash>.js, grouped by what the file IS, never by its type (the extension says
// that): one-of-a-kind files at the top, kinds with many files in a folder of their own.
//
// The kernel is the same file on every page of every site, Drive the same on every site that asked
// for it, and each component the same wherever it is used — so each is downloaded once and kept.
// The hash in the name is what makes keeping it forever safe: change one byte and the name changes,
// so a fixed component can never be served from a cache under its old name. That matters because a
// catalogue component is fixed in place without a new version (tree@1.0.0 was, on 30/09); a name
// that carried only the version would have kept the broken one in every visitor's cache.
//
// A name on the wire is the part's name and nothing else (11/10/2026) — components, services and
// directives alike. The catalogue keeps versioned SOURCE files (components/<name>/vX.Y.Z.js,
// services/<name>/vX.Y.Z.js) as the engine's own business: the latest ships, unless a template
// page's markup names a component's version (data-kit-component="dialog@2.0.0"), and either way the
// file is components/<name>.<hash>.js and registers the bare name — the kernel drops a version
// written in a host before it looks the blueprint up.
//
// Everything the engine serves lives under one prefix, /_kitwork/ (AssetPrefix): the files here,
// the vendored fonts (/_kitwork/fonts/…), a Kitwork UI page's stylesheet. A tenant's folders
// starting with "_" are private in the route tree, and this prefix is answered before the tree is
// walked, so nothing under it is ever a tenant's file.
//
// Order is the document order of `defer`: the kernel first, then Drive, then the modules. The kernel
// starts on DOMContentLoaded, which every deferred file precedes, so each component has registered
// before the first paint (hydrate boot.js). Drive skips a file it has already loaded when it swaps a
// page, so moving between pages loads only the components the new page adds.
//
// An OLD name still works. HTML outlives the engine that wrote it — a cached page, a proxy, a tab
// left open across a deploy — and the name it carries may hash bytes this build no longer makes, or
// sit under the prefix the engine used before (/kit/, until 11/10/2026), or carry the version a
// component's or a service's name had then (/kit/components/menu@1.0.0.<hash>.js,
// /kit/services/studioState@1.0.0.<hash>.js). A name that still means something is answered with
// today's file, marked not to be kept, so the page keeps its JavaScript; only the exact current
// name is cached for good. That is also why the tags carry no integrity attribute: it would turn a
// stale page into a page with no JavaScript at all, to protect a same-origin file that the hash in
// its name already identifies.
//
// (/kit.js?components=… — everything in one response — went on 03/10: no page asked for it, and it
// carried none of the directive files.)

// Minify and Native are set once at boot by the engine, like hydrate.Diagnostics: minified files
// in production, and the native bridge in the kernel when the app runs inside a desktop or mobile
// shell. A file's name follows its bytes, so changing either changes every name.
var (
	Minify bool
	Native bool
)

// AssetPrefix is where the files are served: the one prefix everything the engine serves lives
// under.
const AssetPrefix = "/_kitwork/"

// LegacyAssetPrefix is where they were served until 11/10/2026. A name under it is still answered —
// with today's file, never as the exact name (AssetAt) — for the pages written before.
const LegacyAssetPrefix = "/kit/"

// Options are a site's choices about its JavaScript.
type Options struct {
	// Drive loads the navigator and the morph it drives. It is the site's one switch for moving
	// between pages without a reload (router.reload(false)), so EVERY page gets it — a page that uses
	// no grammar too: a static page without Drive is where the next click would reload and drop what
	// the swap keeps.
	Drive bool
	// (Versions — a site-wide pin, router.javascript({ components: { dialog: "2.0.0" } }) — went with
	// router.javascript() on 11/10/2026: a component is found by its name, and the latest ships.)
}

// Asset is one served file.
type Asset struct {
	Path   string // the URL, /_kitwork/…
	Role   string // runtime | drive | directive | service | component — the data-kitwork-generated value
	Body   []byte
	Digest string // the whole SHA-256, hex — the ETag
}

// assetCache holds every file built so far, by the inputs that decide its bytes. The bytes of an
// embedded part depend only on the engine and the two settings; a site's own component on its source.
var assetCache sync.Map

// hashLength is how much of the SHA-256 the name carries: 64 bits tell two versions of one file
// apart.
const hashLength = 16

func buildAsset(role, name, source string) *Asset {
	if Minify {
		// The minifier returns the readable input unchanged on a parse error.
		source = minifier.JS(source)
	}
	body := []byte(source)
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	dir := ""
	switch role {
	case "component":
		dir = "components/"
	case "service":
		dir = "services/"
	case "directive":
		dir = "directives/"
	}
	return &Asset{
		Path:   AssetPrefix + dir + name + "." + digest[:hashLength] + ".js",
		Role:   role,
		Body:   body,
		Digest: digest,
	}
}

func cachedAsset(key string, build func() *Asset) *Asset {
	key += "|" + strconvBool(Minify) + strconvBool(Native)
	if cached, ok := assetCache.Load(key); ok {
		return cached.(*Asset)
	}
	asset := build()
	if asset == nil {
		return nil
	}
	actual, _ := assetCache.LoadOrStore(key, asset)
	return actual.(*Asset)
}

func strconvBool(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// KernelAsset is the kernel file: bridge (in a shell), kernel, platform modules, boot.
func KernelAsset() *Asset {
	return cachedAsset("kernel", func() *Asset { return buildAsset("runtime", "kernel", hydrate.KernelFile(Native)) })
}

// DriveAsset is Drive and morph.
func DriveAsset() *Asset {
	return cachedAsset("drive", func() *Asset { return buildAsset("drive", "drive", hydrate.DriveFile()) })
}

// wrapComponent gives a component a scope of its own. A component file is written as a plain script
// — top-level `var` and `function` — and joined bare, every one of those became a global, so two
// components with a helper of the same name overwrote each other: tree's nameOf replaced tabs',
// menu's walk replaced context-menu's, and on a page with both the one loaded first quietly broke
// (30/09). A component — including a site's own — cannot be trusted to wrap itself.
func wrapComponent(source string) string {
	return ";(function () {\n" + source + "\n})();"
}

// componentAsset is one component file. key is the component's name part: "menu", "dialog@v2.0.0"
// or a site's "bid-stepper". A catalogue component's source is the version it resolved to, its
// name only the component's: components/dialog.<hash>.js, whichever version the bytes are.
func componentAsset(key string, site Site) *Asset {
	name, version := splitComponentKey(key)
	if version == "" && HasComponent(name) {
		version = strings.TrimSuffix(findLatestComponentVersion(name), ".js")
	}
	if version != "" {
		source := readComponent(name + "@" + version)
		if source == "" {
			return nil
		}
		return cachedAsset("component:"+name+"@"+version, func() *Asset {
			return buildAsset("component", name, wrapComponent(source))
		})
	}
	source := siteComponent(site, name)
	if source == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(source))
	return cachedAsset("site:"+name+"|"+hex.EncodeToString(sum[:]), func() *Asset {
		return buildAsset("component", name, wrapComponent(source))
	})
}

func splitComponentKey(key string) (name, version string) {
	parts := strings.SplitN(key, "@", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return parts[0], ""
}

// PageAssets is the ordered set of files a page needs, or nil when it needs no JavaScript at all —
// no directive and no component.
func PageAssets(html string, site Site, options Options) []*Asset {
	keys := ModuleKeysFor(scanComponents(html, site), site)
	if len(keys) == 0 && !hydrate.UsesGrammar(html) && !options.Drive {
		return nil
	}
	assets := []*Asset{KernelAsset()}
	if options.Drive {
		assets = append(assets, DriveAsset())
	}
	var components []*Asset
	sources := []string{hydrate.WithoutExamples(html)} // a service a docs page prints is not one it calls
	seen := make(map[string]bool)
	for _, key := range keys {
		_, name, _ := strings.Cut(key, ":")
		asset := componentAsset(name, site)
		if asset != nil && !seen[asset.Path] {
			seen[asset.Path] = true
			components = append(components, asset)
			sources = append(sources, string(asset.Body))
		}
	}
	// A directive whose machinery is not in the kernel (data-kit-intersect: an IntersectionObserver)
	// is a file of its own, for the page that writes it.
	for _, asset := range directiveAssets(hydrate.WithoutExamples(html)) {
		assets = append(assets, asset)
	}
	// A native service a page names — in its markup or in a component it uses — comes after the
	// kernel and before the components that call it (services.go).
	if services := scanServices(sources...); len(services) > 0 {
		assets = append(assets, AssemblyAsset())
		for _, name := range services {
			if asset := serviceAsset(name); asset != nil {
				assets = append(assets, asset)
			}
		}
	}
	return append(assets, components...)
}

// Deliver puts the page's files into it, before </head> (else </body>, else at the end). A page that
// already carries the engine's runtime tag is left alone; one that needs nothing is returned as is.
func Deliver(html string, site Site, options Options) string {
	html, _ = DeliverFiles(html, site, options)
	return html
}

// DeliverFiles is Deliver, also returning the files it linked, in their order (nil when it linked
// none): a Kitwork UI page's head rewrites them by router.bundle (work/router_ui_bundle.go).
func DeliverFiles(html string, site Site, options Options) (string, []*Asset) {
	if strings.Contains(html, `<script `+runtimeMarker) || strings.Contains(html, `<script `+legacyRuntimeMarker) {
		return html, nil
	}
	assets := PageAssets(html, site, options)
	if len(assets) == 0 {
		return html, nil
	}
	var tags strings.Builder
	for _, asset := range assets {
		tags.WriteString(`<script data-kitwork-generated="`)
		tags.WriteString(asset.Role)
		tags.WriteString(`" src="`)
		tags.WriteString(asset.Path)
		tags.WriteString(`" defer></script>`)
	}
	tag := tags.String()
	if i := strings.LastIndex(html, "</head>"); i >= 0 {
		return html[:i] + tag + html[i:], assets
	}
	if i := strings.LastIndex(html, "</body>"); i >= 0 {
		return html[:i] + tag + html[i:], assets
	}
	return html + tag, assets
}

// AssetAt is the file a /_kitwork/… path names, built afresh when this process has not built it yet
// — a server restarted under a page still open in a browser can still answer it. exact says whether
// the path is that file's current name; when it is not (an old hash, the old /kit/ prefix, a
// component or service name that still carried its version), the file is still today's, and the
// caller must not let it be cached under the old name. nil when the path names nothing.
func AssetAt(path string, site Site) (asset *Asset, exact bool) {
	rest, ok := strings.CutPrefix(path, AssetPrefix)
	if !ok {
		rest, ok = strings.CutPrefix(path, LegacyAssetPrefix)
	}
	if !ok || !strings.HasSuffix(rest, ".js") || len(rest) > 160 {
		return nil, false
	}
	// current is the name as this build writes it, whichever prefix the path came under: what a
	// hash is compared against when the name alone does not say which bytes.
	current := AssetPrefix + rest
	rest = strings.TrimSuffix(rest, ".js")
	dot := strings.LastIndexByte(rest, '.')
	if dot < 0 || len(rest)-dot-1 != hashLength {
		return nil, false
	}
	label := rest[:dot]
	switch {
	case label == "kernel":
		asset = KernelAsset()
	case label == "drive":
		asset = DriveAsset()
	case strings.HasPrefix(label, "directives/"):
		asset = directiveAsset(strings.TrimPrefix(label, "directives/"))
	case label == "services/assembly":
		asset = AssemblyAsset()
	case strings.HasPrefix(label, "services/"):
		name, version, versioned := strings.Cut(strings.TrimPrefix(label, "services/"), "@")
		// A name written before 11/10/2026 carried the version the engine had then. The engine
		// carries one version of each service, so today's file is the only answer, whichever
		// version the old name spelled — as an old hash gets today's file.
		if versioned && !componentVersionRe.MatchString("v"+version) {
			return nil, false
		}
		asset = serviceAsset(name)
	case strings.HasPrefix(label, "components/"):
		name := strings.TrimPrefix(label, "components/")
		if base, version, versioned := strings.Cut(name, "@"); versioned {
			// A name written before 11/10/2026, when a catalogue component's name carried its
			// version: that version's file, as it is today.
			if !componentNameRe.MatchString(base) || !componentVersionRe.MatchString("v"+version) {
				return nil, false
			}
			asset = componentAsset(base+"@v"+version, nil)
		} else if componentNameRe.MatchString(name) && HasComponent(name) {
			asset = catalogueComponentAt(name, current)
		} else if componentNameRe.MatchString(name) {
			asset = componentAsset(name, site)
		}
	}
	if asset == nil {
		return nil, false
	}
	return asset, asset.Path == path
}

// catalogueComponentAt is the catalogue file components/<name>.<hash>.js names (current: the path
// under today's prefix). The name no longer says which version, so the hash does: a template page
// that names an older version gets that version's file under the same name, and so does the same
// name under the old /kit/ prefix. A hash this build makes for none of them is answered with the
// latest — today's file, not the exact name.
func catalogueComponentAt(name, current string) *Asset {
	for _, version := range componentVersionsOf(name) {
		if asset := componentAsset(name+"@v"+version, nil); asset != nil && asset.Path == current {
			return asset
		}
	}
	return componentAsset(name, nil)
}

// componentVersionsOf lists the catalogue's versions of name, oldest first, without their "v".
func componentVersionsOf(name string) []string {
	if !componentNameRe.MatchString(name) {
		return nil
	}
	entries, err := jsFS.ReadDir("components/" + name)
	if err != nil {
		return nil
	}
	var versions []string
	for _, entry := range entries {
		if file := entry.Name(); !entry.IsDir() && strings.HasSuffix(file, ".js") {
			versions = append(versions, strings.TrimPrefix(strings.TrimSuffix(file, ".js"), "v"))
		}
	}
	sort.Slice(versions, func(i, j int) bool { return versionLess(versions[i], versions[j]) })
	return versions
}
