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
//	<script data-kitwork-jit="runtime"    src="/kit/kernel.9f2c41d0a7b3e815.js" defer></script>
//	<script data-kitwork-jit="drive"      src="/kit/drive.51e0c2….js" defer></script>
//	<script data-kitwork-jit="component"  src="/kit/components/menu@1.0.0.c47a….js" defer></script>
//
// The kernel is the same file on every page of every site, Drive the same on every site that asked
// for it, and each component the same wherever it is used — so each is downloaded once and kept.
// The hash in the name is what makes keeping it forever safe: change one byte and the name changes,
// so a fixed component can never be served from a cache under its old name. That matters because a
// catalogue component is fixed in place without a new version (tree@1.0.0 was, on 30/09); a name
// that carried only the version would have kept the broken one in every visitor's cache.
//
// Order is the document order of `defer`: the kernel first, then Drive, then the modules. The kernel
// starts on DOMContentLoaded, which every deferred file precedes, so each component has registered
// before the first paint (hydrate boot.js). Drive skips a file it has already loaded when it swaps a
// page, so moving between pages loads only the components the new page adds.
//
// An OLD name still works. HTML outlives the engine that wrote it — a cached page, a proxy, a tab
// left open across a deploy — and the name it carries may hash bytes this build no longer makes. A
// name that still means something (the kernel, menu@1.0.0) is answered with today's file, marked
// not to be kept, so the page keeps its JavaScript; only the exact name is cached for good. That is
// also why the tags carry no integrity attribute: it would turn a stale page into a page with no
// JavaScript at all, to protect a same-origin file that the hash in its name already identifies.
//
// /kit.js?components=… — everything in one response — is still served, for tooling and for a page
// assembled by hand (Render); the engine's own pages use Deliver.

// Minify and Native are set once at boot by the engine, like hydrate.Diagnostics: minified files
// in production, and the native bridge in the kernel when the app runs inside a desktop or mobile
// shell. A file's name follows its bytes, so changing either changes every name.
var (
	Minify bool
	Native bool
)

// AssetPrefix is where the files are served — beside /kit.js, which serves the whole runtime in one
// response for tooling.
const AssetPrefix = "/kit/"

// Options are a site's choices about its JavaScript — router.javascript() and router.drive().
type Options struct {
	// Drive loads the navigator and the morph it drives, and flags the kernel's tag so it starts.
	Drive bool
	// Versions pins a catalogue component for the whole site: {"dialog": "2.0.0"} makes
	// data-kit-component="dialog" mean dialog@v2.0.0 on every page. A version written in the markup
	// still wins; a name with no pin is the latest.
	Versions map[string]string
}

// Asset is one served file.
type Asset struct {
	Path   string // the URL, /kit/…
	Role   string // runtime | drive | service | component — the data-kitwork-jit value
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

// wrapComponent gives a component a scope of its own (see ModulesJSFor for why).
func wrapComponent(source string) string {
	return ";(function () {\n" + source + "\n})();"
}

// componentAsset is one component file. key is the component's name part: "menu", "dialog@v2.0.0"
// or a site's "bid-stepper". A catalogue component is named with the version it resolved to.
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
		label := name + "@" + strings.TrimPrefix(version, "v")
		return cachedAsset("component:"+name+"@"+version, func() *Asset {
			return buildAsset("component", label, wrapComponent(source))
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
	if len(keys) == 0 && !hydrate.UsesGrammar(html) {
		return nil
	}
	assets := []*Asset{KernelAsset()}
	if options.Drive {
		assets = append(assets, DriveAsset())
	}
	var components []*Asset
	sources := []string{html}
	seen := make(map[string]bool)
	for _, key := range keys {
		_, name, _ := strings.Cut(key, ":")
		if pinned, ok := options.Versions[name]; ok && !strings.Contains(name, "@") && HasComponent(name+"@v"+strings.TrimPrefix(pinned, "v")) {
			name += "@v" + strings.TrimPrefix(pinned, "v")
		}
		asset := componentAsset(name, site)
		if asset != nil && !seen[asset.Path] {
			seen[asset.Path] = true
			components = append(components, asset)
			sources = append(sources, string(asset.Body))
		}
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
	if strings.Contains(html, `<script `+runtimeMarker) {
		return html
	}
	assets := PageAssets(html, site, options)
	if len(assets) == 0 {
		return html
	}
	var tags strings.Builder
	for _, asset := range assets {
		tags.WriteString(`<script data-kitwork-jit="`)
		tags.WriteString(asset.Role)
		tags.WriteString(`"`)
		if asset.Role == "runtime" && options.Drive {
			tags.WriteString(` data-kitwork-drive`)
		}
		tags.WriteString(` src="`)
		tags.WriteString(asset.Path)
		tags.WriteString(`" defer></script>`)
	}
	tag := tags.String()
	if i := strings.LastIndex(html, "</head>"); i >= 0 {
		return html[:i] + tag + html[i:]
	}
	if i := strings.LastIndex(html, "</body>"); i >= 0 {
		return html[:i] + tag + html[i:]
	}
	return html + tag
}

// AssetAt is the file a /kit/… path names, built afresh when this process has not built it yet — a
// server restarted under a page still open in a browser can still answer it. exact says whether the
// path is that file's current name; when it is not (an old hash), the file is still today's, and the
// caller must not let it be cached under the old name. nil when the path names nothing.
func AssetAt(path string, site Site) (asset *Asset, exact bool) {
	rest, ok := strings.CutPrefix(path, AssetPrefix)
	if !ok || !strings.HasSuffix(rest, ".js") || len(rest) > 160 {
		return nil, false
	}
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
	case label == "services/assembly":
		asset = AssemblyAsset()
	case strings.HasPrefix(label, "services/"):
		name, version, _ := strings.Cut(strings.TrimPrefix(label, "services/"), "@")
		if version != "" && serviceVersion(name) == version {
			asset = serviceAsset(name)
		}
	case strings.HasPrefix(label, "components/"):
		name := strings.TrimPrefix(label, "components/")
		if base, version, versioned := strings.Cut(name, "@"); versioned {
			if !componentNameRe.MatchString(base) || !componentVersionRe.MatchString("v"+version) {
				return nil, false
			}
			asset = componentAsset(base+"@v"+version, nil)
		} else if componentNameRe.MatchString(name) && !HasComponent(name) {
			asset = componentAsset(name, site)
		}
	}
	if asset == nil {
		return nil, false
	}
	return asset, asset.Path == path
}

// HasComponentVersion reports whether the catalogue has name at version ("2.0.0", with or without
// its "v") — what a site-wide pin must name.
func HasComponentVersion(name, version string) bool {
	version = "v" + strings.TrimPrefix(version, "v")
	return componentNameRe.MatchString(name) && componentVersionRe.MatchString(version) && HasComponent(name+"@"+version)
}

// ComponentVersionsOf lists the catalogue's versions of name, oldest first, without their "v" — for
// a message that says what a pin could have named.
func ComponentVersionsOf(name string) []string {
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
