package js

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
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
//	<script data-kitwork-generated="component"  src="/_kitwork/components/tabs.c47a….js" defer></script>
//
// (and /_kitwork/parts/editor-paste.<hash>.js, which no page links: the component it is part of loads
// it itself when it needs it, PartAsset.)
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
// services/<name>/vX.Y.Z.js) as the engine's own business: the latest ships, as
// components/<name>.<hash>.js, and registers the bare name.
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
// An OLD hash still works. HTML outlives the engine that wrote it — a cached page, a proxy, a tab
// left open across a deploy — and the name it carries may hash bytes this build no longer makes. A
// name that still means something is answered with today's file, marked not to be kept, so the page
// keeps its JavaScript; only the exact current name is cached for good. That is also why the tags
// carry no integrity attribute: it would turn a stale page into a page with no JavaScript at all, to
// protect a same-origin file that the hash in its name already identifies. (The /kit/ prefix and the
// names that carried a version, answered until 26/10/2026, belonged to template pages.)
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
	Role   string // runtime | drive | directive | service | component — the data-kitwork-generated value; part (PartAsset)
	Body   []byte
	Digest string // the whole SHA-256, hex — the ETag
}

// assetCache holds every file built so far, by the inputs that decide its bytes: the bytes of an
// embedded part depend only on the engine and the two settings.
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
	case "part":
		dir = "parts/"
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

// CallAsset is router.view's bridge in the browser, call.<hash>.js (28/10/2026): a page that runs in the
// kernel because its view binds a job or a var loads it.
func CallAsset() *Asset {
	return cachedAsset("call", func() *Asset { return buildAsset("call", "call", hydrate.CallFile()) })
}

// callsJobs reports whether a page runs in the kernel as router.view's page, with jobs or vars: it carries
// its data in data-kitwork-page (ui/page_live.go).
func callsJobs(html string) bool {
	return strings.Contains(html, "data-kitwork-page")
}

// wrapComponent gives a component a scope of its own. A component file is written as a plain script
// — top-level `var` and `function` — and joined bare, every one of those became a global, so two
// components with a helper of the same name overwrote each other: tree's nameOf replaced tabs',
// menu's walk replaced context-menu's, and on a page with both the one loaded first quietly broke
// (30/09). A component — including a site's own — cannot be trusted to wrap itself.
func wrapComponent(source string) string {
	return ";(function () {\n" + source + "\n})();"
}

// componentAsset is one catalogue component's file, components/<name>.<hash>.js: the source of its
// latest version. nil when the catalogue has no such component.
func componentAsset(name string) *Asset {
	if !componentNameRe.MatchString(name) || name == coreName {
		return nil
	}
	version := strings.TrimSuffix(findLatestComponentVersion(name), ".js")
	source := readComponent(name + "@" + version)
	if version == "" || source == "" {
		return nil
	}
	return cachedAsset("component:"+name+"@"+version, func() *Asset {
		return buildAsset("component", name, wrapComponent(withParts(source)))
	})
}

// A part is a file a catalogue component loads itself when it needs it, never with the page: the
// editor's reading of a paste or a drop (parts/editor-paste.js, 21/10/2026), which most visits never
// use. Its name on the wire is parts/<name>.<hash>.js. The component's source names it without its
// hash, "/_kitwork/parts/editor-paste.js" in quotes, and the file the page gets has the name with its
// hash in that place (withParts), so the component's own hash follows the part's bytes too. Only the
// exact current name is answered: a component of another build would hand a part of this one what it
// no longer expects, so it gets nothing and does without (the editor pastes plain words).
//
// A part may name another the same way (parts/editor-format.js, 21/10/2026, loads
// parts/editor-markdown.js when Markdown is first chosen): its file has the other's name with its hash
// in that place too. A part naming itself keeps the name as written (its hash would be its own bytes).

// partNameRe is a part's name, as its file in parts/ is named.
var partNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// partInSourceRe finds a part named in a component's source, without its hash.
var partInSourceRe = regexp.MustCompile(`"/_kitwork/parts/([a-z][a-z0-9-]*)\.js"`)

// PartAsset is the part file parts/<name>.js, or nil when there is none.
func PartAsset(name string) *Asset {
	if !partNameRe.MatchString(name) {
		return nil
	}
	return cachedAsset("part:"+name, func() *Asset {
		source, err := jsFS.ReadFile("parts/" + name + ".js")
		if err != nil {
			return nil
		}
		return buildAsset("part", name, wrapComponent(partsNamed(strings.TrimSpace(string(source)), name)))
	})
}

// withParts writes, in place of each part a component's source names without its hash, the part's
// current name; a name no part has stays as written.
func withParts(source string) string { return partsNamed(source, "") }

// partsNamed is withParts for the source of the part self ("" for a component's), which keeps its own
// name as written.
func partsNamed(source, self string) string {
	if !strings.Contains(source, `"`+AssetPrefix+"parts/") {
		return source
	}
	return partInSourceRe.ReplaceAllStringFunc(source, func(quoted string) string {
		name := partInSourceRe.FindStringSubmatch(quoted)[1]
		if name == self {
			return quoted
		}
		if part := PartAsset(name); part != nil {
			return `"` + part.Path + `"`
		}
		return quoted
	})
}

// PageAssets is the ordered set of files a page needs, or nil when it needs no JavaScript at all —
// no directive and no component.
func PageAssets(html string, options Options) []*Asset {
	keys := scanComponents(html)
	calls := callsJobs(html) // the bridge runs on the kernel: a page with it has the kernel too
	if len(keys) == 0 && !hydrate.UsesGrammar(html) && !options.Drive && !calls {
		return nil
	}
	assets := []*Asset{KernelAsset()}
	if options.Drive {
		assets = append(assets, DriveAsset())
	}
	if calls {
		assets = append(assets, CallAsset())
	}
	var components []*Asset
	sources := []string{hydrate.WithoutExamples(html)} // a service a docs page prints is not one it calls
	seen := make(map[string]bool)
	for _, key := range keys {
		_, name, _ := strings.Cut(key, ":")
		asset := componentAsset(name)
		if asset != nil && !seen[asset.Path] {
			seen[asset.Path] = true
			components = append(components, asset)
			sources = append(sources, string(asset.Body))
		}
	}
	// A directive whose machinery is not in the kernel (intersect-enter: an IntersectionObserver) is a
	// file of its own, for the page that writes it.
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
func Deliver(html string, options Options) string {
	html, _ = DeliverFiles(html, options)
	return html
}

// DeliverFiles is Deliver, also returning the files it linked, in their order (nil when it linked
// none): a Kitwork UI page's head rewrites them by router.bundle (work/router_ui_bundle.go).
func DeliverFiles(html string, options Options) (string, []*Asset) {
	if strings.Contains(html, `<script `+runtimeMarker) {
		return html, nil
	}
	assets := PageAssets(html, options)
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
// the path is that file's current name; when it is not (an old hash), the file is still today's, and
// the caller must not let it be cached under the old name. nil when the path names nothing.
func AssetAt(path string) (asset *Asset, exact bool) {
	rest, ok := strings.CutPrefix(path, AssetPrefix)
	if !ok || !strings.HasSuffix(rest, ".js") || len(rest) > 160 {
		return nil, false
	}
	current := path
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
	case label == "call":
		asset = CallAsset()
	case strings.HasPrefix(label, "directives/"):
		asset = directiveAsset(strings.TrimPrefix(label, "directives/"))
	case label == "services/assembly":
		asset = AssemblyAsset()
	case strings.HasPrefix(label, "parts/"):
		if part := PartAsset(strings.TrimPrefix(label, "parts/")); part != nil && part.Path == current {
			asset = part // its exact current name only (PartAsset)
		}
	case strings.HasPrefix(label, "services/"):
		asset = serviceAsset(strings.TrimPrefix(label, "services/"))
	case strings.HasPrefix(label, "components/"):
		asset = componentAsset(strings.TrimPrefix(label, "components/"))
	}
	if asset == nil {
		return nil, false
	}
	return asset, asset.Path == path
}
