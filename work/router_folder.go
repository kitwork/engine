package work

// Per-folder router: the collector that a folder's router.kitwork.js writes into, plus the
// tree-mode kitwork() binding and the generation preparation of that script.
//
// In a tree tenant, `const { router } = kitwork()` inside a folder's router.kitwork.js must
// yield a collector bound to THAT folder — not the flat, path-registering Router. We get that
// by handing the folder-compile VM a kitwork() that returns a TreeKitWork, whose Router()
// shadows the flat one and returns the folder's *FolderRouter. Every other capability
// (env, db, http, render…) is promoted from the embedded *KitWork unchanged.

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/site"
	"github.com/kitwork/engine/ui/stylesheet"
	"github.com/kitwork/engine/value"
)

func lambdaOf(v value.Value) *value.Lambda {
	lb, _ := v.V.(*value.Lambda)
	return lb
}

// appendLambdas flattens guard arguments — a single fn, a variadic list, or an array [fn, fn] —
// into dst in order. So guard(auth), guard(a, b) and guard([a, b]) all work and run sequentially.
func appendLambdas(dst []*value.Lambda, args ...value.Value) []*value.Lambda {
	for _, a := range args {
		if a.K == value.Array {
			for _, el := range a.Array() {
				if lb := lambdaOf(el); lb != nil {
					dst = append(dst, lb)
				}
			}
		} else if lb := lambdaOf(a); lb != nil {
			dst = append(dst, lb)
		}
	}
	return dst
}

// ── a single HTTP method on a folder ────────────────────────────────────────

type FolderMethod struct {
	method     string
	handle     *value.Lambda
	view       bool            // declared by router.view: the page's jobs are called through it (view_call.go)
	folder     *FolderRouter   // the folder whose router file declared it
	declaredBy string          // the call that declared it: router.get, router.proxy…
	declaredAt int             // its line in the router file, 0 when not known (declared outside the boot run)
	guards     []*value.Lambda // the before-chain: runs in order; each may block / respond / prepare
	success    *value.Lambda   // runs after a clean handler (canonical name for the old `then`)
	errorH     *value.Lambda   // runs when the method errored (canonical name for the old `catch`)
	final      *value.Lambda
	outputKind string
	outputData value.Value
	outputPath string

	// .proxy(): answer from an upstream instead of a page. proxyTarget is a fixed URL or a handler
	// computing one per request. See serveProxy in tree_proxy.go.
	isProxy     bool
	proxyTarget value.Value

	// Response caching + rate limiting (see cache/persist/ratelimit helper packages). The expiry
	// resolvers accept a rolling duration OR a wall-clock boundary ("nextday 03:00", "weekly", …),
	// evaluated at save time; nil = that tier is off.
	cacheExpiry    func(time.Time) time.Duration // .cache(): RAM
	persistExpiry  func(time.Time) time.Duration // .persist(): disk <tenant>/.persist
	cacheKeyPolicy *responseCacheKeyPolicy       // nil keeps the historical full-query key
	cacheKeyErr    error                         // surfaced while the route generation is built
	limits         []methodLimit                 // .limit(...): rate-limit rules
}

// methodLimit is one rate-limit rule: at most Rate hits per Per window, keyed by Dim ("ip"|"user").
type methodLimit struct {
	Rate int
	Per  time.Duration
	Dim  string
}

// parseTTL reads a duration from a number (milliseconds) or a string ("5m", "1h").
func parseTTL(v value.Value) time.Duration {
	if v.IsNumeric() {
		return time.Duration(v.N) * time.Millisecond
	}
	d, _ := ParseDuration(v.Text())
	return d
}

func (m *FolderMethod) Handle(l value.Value) *FolderMethod { m.handle = lambdaOf(l); return m }

// Guard registers before-hooks that run IN ORDER ahead of the handler — each may block (return
// false), respond (return data), or just prepare the context (return nothing). Guard subsumes the
// old `middleware`: it is one ordered chain. Accepts a single fn, a variadic list, or an array:
// guard(auth) / guard(a, b) / guard([a, b]).
func (m *FolderMethod) Guard(args ...value.Value) *FolderMethod {
	m.guards = appendLambdas(m.guards, args...)
	return m
}

func (m *FolderMethod) Success(l value.Value) *FolderMethod { m.success = lambdaOf(l); return m }
func (m *FolderMethod) Error(l value.Value) *FolderMethod   { m.errorH = lambdaOf(l); return m }
func (m *FolderMethod) Finally(l value.Value) *FolderMethod { m.final = lambdaOf(l); return m }

// Deprecated aliases — success/error/guard are canonical; these keep old habits working.
func (m *FolderMethod) Then(l value.Value) *FolderMethod             { return m.Success(l) }
func (m *FolderMethod) Catch(l value.Value) *FolderMethod            { return m.Error(l) }
func (m *FolderMethod) Middleware(args ...value.Value) *FolderMethod { return m.Guard(args...) }

// Cache keeps the rendered response in RAM (fast, lost on restart). The argument is a rolling
// duration OR a wall-clock boundary; no arg = forever:
//
//	router.get(...).cache("5m")             // rolling 5 minutes
//	router.get(...).cache("nextday 03:00")  // until 03:00 tomorrow (aligns to a data refresh)
//	router.get(...).cache("weekly")         // until next Monday 00:00
//	router.get(...).cache("5m", "page")     // key only by the page query parameter
//	router.get(...).cache("5m", { page: true, category: ["new", "click"] })
func (m *FolderMethod) Cache(args ...value.Value) *FolderMethod {
	m.cacheExpiry = expiryOf(args...)
	m.checkExpiry("cache", m.cacheExpiry, args)
	m.configureResponseCacheKey(args...)
	return m
}

// Persist writes the rendered response to <tenant>/.persist on disk — any content type
// (html/json/image) — served with no VM and surviving restarts. Same expiry grammar as Cache;
// no arg = forever (until the file is removed):
//
//	router.get(...).persist()               // forever
//	router.get(...).persist("nextday 03:00")
//	router.get(...).persist("1d", ["page", "category"])
func (m *FolderMethod) Persist(args ...value.Value) *FolderMethod {
	m.persistExpiry = expiryOf(args...)
	m.checkExpiry("persist", m.persistExpiry, args)
	m.configureResponseCacheKey(args...)
	return m
}

// Limit adds a rate-limit rule. Accepts a config map or (rate, per[, type]):
//
//	router.get(...).limit({ rate: 100, per: "1m", type: "ip" })
//	router.get(...).limit(100, "1m")
func (m *FolderMethod) Limit(args ...value.Value) *FolderMethod {
	lim := methodLimit{Dim: "ip"}
	if len(args) == 1 && args[0].IsMap() {
		mp := args[0].Map()
		if r, ok := mp["rate"]; ok {
			lim.Rate = int(r.N)
		}
		if p, ok := mp["per"]; ok {
			lim.Per = parseTTL(p)
		}
		if d, ok := mp["type"]; ok && d.Text() != "" {
			lim.Dim = d.Text()
		}
		if d, ok := mp["by"]; ok && d.Text() != "" {
			lim.Dim = d.Text()
		}
	} else {
		if len(args) > 0 {
			lim.Rate = int(args[0].N)
		}
		if len(args) > 1 {
			lim.Per = parseTTL(args[1])
		}
		if len(args) > 2 && args[2].Text() != "" {
			lim.Dim = args[2].Text()
		}
	}
	if lim.Rate > 0 && lim.Per > 0 {
		m.folder.checkLimitBy(lim.Dim)
		m.limits = append(m.limits, lim)
	}
	return m
}

// ratelimitRules parses the .ratelimit() config shape into rules. One map per call; each DIMENSION
// key (ip/browser/user/global) carries its own rate, sharing the map's window — and calls STACK, so
// layered windows read naturally:
//
//	router.ratelimit({ ip: 30, second: 1 })      // 30 per second (unit key = the window)
//	      .ratelimit({ ip: 600, minute: 1 })     // 600 per minute
//	      .ratelimit({ ip: 100, minute: 5 })     // 100 per 5 minutes
//	      .ratelimit({ global: 5000, period: "1m" }) // explicit period works too (and wins)
//
// The window: `period`/`per` (a duration string) when given, else a UNIT key
// (second/minute/hour/day, singular or plural, value = how many), else 1s. The legacy
// { rate, per, type } shape is accepted too. Rules missing a positive rate or window are dropped
// (never a silent zero-limit).
func ratelimitRules(args ...value.Value) []methodLimit {
	var rules []methodLimit
	for _, a := range args {
		if !a.IsMap() {
			continue
		}
		mp := a.Map()
		per := time.Duration(0)
		if p, ok := mp["period"]; ok {
			per = parseTTL(p)
		} else if p, ok := mp["per"]; ok {
			per = parseTTL(p)
		}
		if per <= 0 {
			per = unitWindow(mp)
		}
		if per <= 0 {
			per = time.Second
		}
		for _, dim := range []string{"ip", "browser", "user", "global"} {
			if rv, ok := mp[dim]; ok && int(rv.N) > 0 {
				rules = append(rules, methodLimit{Dim: dim, Rate: int(rv.N), Per: per})
			}
		}
		if rv, ok := mp["rate"]; ok && int(rv.N) > 0 { // legacy shape, and { rate, per, by }
			dim := "ip"
			if d, ok := mp["type"]; ok && d.Text() != "" {
				dim = d.Text()
			}
			if d, ok := mp["by"]; ok && d.Text() != "" {
				dim = d.Text()
			}
			rules = append(rules, methodLimit{Dim: dim, Rate: int(rv.N), Per: per})
		}
	}
	return rules
}

// unitWindow reads the window from a time-UNIT key: { second: 1 } = 1s, { minute: 5 } = 5m.
// Full words only (no-abbreviation), singular or plural. Returns 0 when no unit key is present.
func unitWindow(mp map[string]value.Value) time.Duration {
	units := []struct {
		key string
		d   time.Duration
	}{
		{"second", time.Second}, {"seconds", time.Second},
		{"minute", time.Minute}, {"minutes", time.Minute},
		{"hour", time.Hour}, {"hours", time.Hour},
		{"day", 24 * time.Hour}, {"days", 24 * time.Hour},
	}
	for _, u := range units {
		if v, ok := mp[u.key]; ok && int(v.N) > 0 {
			return time.Duration(int(v.N)) * u.d
		}
	}
	return 0
}

// Ratelimit adds rate-limit rules to THIS method (same stacking shape as the router-level form).
func (m *FolderMethod) Ratelimit(args ...value.Value) *FolderMethod {
	m.limits = append(m.limits, ratelimitRules(args...)...)
	return m
}

// RateLimit is the camelCase alias (.rateLimit).
func (m *FolderMethod) RateLimit(args ...value.Value) *FolderMethod { return m.Ratelimit(args...) }

// ── the folder's router (runtime behaviour for this node) ───────────────────

type FolderRouter struct {
	tenant   *Tenant
	node     *RouteNode
	bytecode *compiler.Bytecode // the folder's own compiled router.kitwork.js (nil if none)

	guards   []*value.Lambda // folder before-chain — applied to this folder AND every descendant
	methods  map[string]*FolderMethod
	outputs  map[string]*FolderMethod // semantic generated endpoints such as /rss.xml and /sitemap.xml
	notFound *value.Lambda
	errorH   *value.Lambda
	meta     map[string]value.Value // declared via router.meta()/.favicon(); inherited down the chain
	jsonld   []value.Value          // router.jsonld() nodes — inherited down the chain, accumulated
	limits   []methodLimit          // router.ratelimit() rules — this folder AND every descendant
	assetErr error                  // first fatal .assets() conflict, surfaced as a generation error
	// redeclared is the first method the router file declared twice (declare), a boot error
	redeclared error
	// limitErr is the first rate limit of router.work counting what it may not (checkLimitBy), a boot
	// error; limitWarning the line a template site's file prints for one (router_limit_by.go)
	limitErr     error
	limitWarning string
	// expiryWarning is the line a template site's file prints for a cache expiry it cannot read
	// (checkExpiry, router_limit_by.go)
	expiryWarning string
	unknown       []string // methods the chain called that this engine does not have

	// router.transition(): how <content /> changes on a move between pages; inherited down the chain
	transition string
	// router.colors() and router.fonts() of this folder, every call of the file added up. The site
	// root's become the site's CSS configuration (publishSitePalette); a branch's are merged over the
	// site's for its routes and the ones below (routeCSS).
	colors map[string]value.Value
	fonts  map[string]value.Value
	// router.gradients() of this folder, every call of the file added up, as written: each route's .ui
	// pages read the gradients of the folders down to it, the nearest winning per name (routeStyleOf,
	// router_ui_gradients.go)
	gradients map[string]value.Value
	// ui says the folder's router file is router.work: router.highlight reads Kitwork UI's grammar there
	// (router_ui_highlight.go) and the template's in router.kitwork.js
	ui bool
	// router.highlight() of this folder in router.work, every call of the file added up, as written, and
	// where the call that set each kind is (highlightAt): each route's .ui pages read the kinds of the
	// folders down to it, the nearest winning per kind (routeStyleOf, router_ui_highlight.go)
	highlight   map[string]value.Value
	highlightAt map[string]stylesheet.Source
	// callSite is where the call being made is in the router file, the place the VM gives it; set only
	// while the router runs (runFolderRouter), nil once it has: a call at request time has no place
	callSite func() runtime.SourceLocation
	// router.appearance(): "system", "light" or "dark", the mode a page shows before the visitor
	// chooses; "" when this folder does not say (routeAppearance: inherited down the chain)
	appearance string
	// router.contrast(): "warning" (the default) or "strict", how a .ui page's pair of colours short of
	// contrast is told; "" when this folder does not say (routeContrast: inherited down the chain)
	contrast string
	// the old router methods the file called, each once in the order first called: router.theme()
	// (applied as router.colors() and router.fonts()), router.drive(), router.javascript() and
	// router.jitjs() (gone, applying nothing). compileFolder names each once at boot
	// (removedMethodWarnings).
	oldMethods []oldMethod
	// router.favicon() as the URL a page links (<link rel="icon">): the nearest folder's wins
	favicon string
	// router.bundle(): how this folder's .ui pages deliver their CSS and their JavaScript, per part; ""
	// for a part it does not name (routeBundle: the nearest folder that names it wins, router_ui_bundle.go)
	bundle bundleChoice

	// The rest is a Kitwork UI folder's (router_vocabulary.go). configBytecode is its config.work, compiled
	// (nil when it has none): a function config.work hands the router (router.guard) runs with it at request
	// time (code). running is the file being run as the folder is built, config.work or router.work, for
	// the rules of the vocabulary; "" otherwise.
	configBytecode *compiler.Bytecode
	running        string
	// app says the folder is the app's, the collector of the config.work at the app's root, parent of every
	// site's root (appRouterConfig). appRoot says the folder is a site's root that is the app's folder too
	// (one site in the app's folder): its config.work is the app's as well, and takes the words of both.
	app, appRoot bool
	// nearest are the calls of the folder whose nearest folder wins, by word, each with the file and line
	// it is written at: once per folder, config.work and router.work together
	nearest map[string]routerCallPlace
	// seeds are the calls of the app's config.work, as the app's collector heard them, that a site's root
	// starts from (seed): every one but router.timezone, in written order
	seeds []routerSeed
	// timezone is router.timezone(…), the app's zone (nil: UTC): set on the app's collector, and on a site's
	// root from it, or from its own config.work when that is the app's (appRoot)
	timezone *time.Location
}

// MissingMember is called for a member the folder router does not have. The chain must not break
// — every declaration after the unknown call still lands — and the boot must not stay quiet: the
// name is recorded and compileFolder prints it, so a `.themes()` written against an engine that
// has not shipped it, or a typo, is one visible line naming the method instead of a site that
// silently lost whatever came after. A warning, not a refusal: with the chain intact the only
// thing ignored is that one declaration, and a router written ahead of its engine should still
// serve. A template's router only: a Kitwork UI folder's answers the words of kitwork/router and
// refuses any other (uiRouter, router_vocabulary.go).
func (f *FolderRouter) MissingMember(name string) value.Value {
	return value.NewFunc(func(args ...value.Value) value.Value {
		f.unknown = append(f.unknown, name)
		return value.New(f)
	})
}

// unknownMethodWarning names the methods the chain called that this engine does not have.
func (f *FolderRouter) unknownMethodWarning() string {
	if len(f.unknown) == 0 {
		return ""
	}
	return "router." + strings.Join(f.unknown, "(), router.") + "() is not a method of this engine's router — ignored; everything else on the chain still applies"
}

// oldMethod is a router method a site may still call that this engine renamed or removed: the call
// keeps the chain, and the boot says what to write instead, once per file.
type oldMethod string

const (
	renamedTheme      oldMethod = "router.theme() is now router.colors() and router.fonts() — applied"
	removedDrive      oldMethod = "router.drive() is gone — write router.reload(false)"
	removedJavascript oldMethod = "router.javascript() is gone — moving between pages is router.reload(false), components are found by name"
)

// removed records that the file called an old method; a second call (or router.jitjs() after
// router.javascript(), which share their line) records nothing more.
func (f *FolderRouter) removed(method oldMethod) {
	if f == nil {
		return
	}
	for _, seen := range f.oldMethods {
		if seen == method {
			return
		}
	}
	f.oldMethods = append(f.oldMethods, method)
}

// removedMethodWarnings are the boot lines of the old methods the file called, one each, in the
// order the file first called them.
func (f *FolderRouter) removedMethodWarnings() []string {
	if f == nil {
		return nil
	}
	lines := make([]string, len(f.oldMethods))
	for i, method := range f.oldMethods {
		lines[i] = string(method)
	}
	return lines
}

// declare adds the HTTP method name to the folder, by the call the router file wrote (router.get,
// router.proxy…). A method is declared once in a router file: a second declaration is a
// boot error naming the line of the first, not a silent replacement of it (19/10/2026). The second
// gets a method of its own, kept nowhere, so the rest of its chain still reads.
func (f *FolderRouter) declare(call, name string, args ...value.Value) *FolderMethod {
	m := &FolderMethod{method: name, declaredBy: call, folder: f}
	if f.callSite != nil {
		m.declaredAt = int(f.callSite().Line)
	}
	if len(args) > 0 && args[0].K == value.Func { // router.get(handler) shorthand
		m.handle = lambdaOf(args[0])
	}
	if first := f.methods[name]; first != nil {
		if f.redeclared == nil {
			f.redeclared = redeclaredMethod(first, m)
		}
		return m
	}
	f.methods[name] = m
	return m
}

// redeclaredMethod is the boot error of a method declared a second time in one router file.
func redeclaredMethod(first, second *FolderMethod) error {
	where := func(line int) string {
		if line <= 0 {
			return "ở trên"
		}
		return fmt.Sprintf("ở dòng %d", line)
	}
	if first.declaredBy == second.declaredBy {
		return fmt.Errorf("%s đã khai %s; khai lại %s: một router file khai mỗi method một lần, gộp hai lần vào một %s(…)",
			first.declaredBy, where(first.declaredAt), where(second.declaredAt), first.declaredBy)
	}
	return fmt.Errorf("%s %s khai lại %s mà %s đã khai %s: một router file khai mỗi method một lần, giữ một trong hai",
		second.declaredBy, where(second.declaredAt), first.method, first.declaredBy, where(first.declaredAt))
}

func (f *FolderRouter) Get(args ...value.Value) *FolderMethod {
	return f.declare("router.get", "GET", args...)
}
func (f *FolderRouter) Post(args ...value.Value) *FolderMethod {
	return f.declare("router.post", "POST", args...)
}

// Proxy answers this folder from an UPSTREAM url instead of a page — a reverse proxy mounted at the
// folder. The target is a fixed URL, or a handler that computes one per request (params/path):
//
//	router.proxy("https://cdn.example.com/logo.png").persist("30d")
//	router.proxy((ctx) => "https://cdn.example.com/" + ctx.params("id") + ".png").persist("30d")
//
// Compose with .persist()/.cache(): a hit replays the stored bytes with NO VM and NO refetch — the
// "cached mount" (declare once, first hit fetches, the rest serve from disk). The upstream's
// Content-Type is preserved, so images come back as images.
//
// SSRF: the TENANT owns the target (it computes it) — the engine still blocks private/loopback space
// at the transport as a backstop. Never build the target straight from untrusted input.
func (f *FolderRouter) Proxy(args ...value.Value) *FolderMethod {
	m := f.declare("router.proxy", "GET") // declare WITHOUT args so a handler-target never becomes the page handler
	m.isProxy = true
	if len(args) > 0 {
		m.proxyTarget = args[0]
	}
	return m
}
func (f *FolderRouter) Put(args ...value.Value) *FolderMethod {
	return f.declare("router.put", "PUT", args...)
}
func (f *FolderRouter) Patch(args ...value.Value) *FolderMethod {
	return f.declare("router.patch", "PATCH", args...)
}
func (f *FolderRouter) Delete(args ...value.Value) *FolderMethod {
	return f.declare("router.delete", "DELETE", args...)
}

func (f *FolderRouter) output(kind, defaultPath string, args ...value.Value) *FolderMethod {
	m := &FolderMethod{method: "GET", outputKind: kind, outputData: value.New(map[string]value.Value{})}
	if f != nil && f.ui {
		// a Kitwork UI site's built-in door is a route of its folder like any other: its rules are read as
		// the folder's (checkExpiry, checkLimitBy) and its cache counts in the app's zone (routeNow). A
		// template's keeps no folder, as before.
		m.folder = f
	}
	outputPath := defaultPath
	if len(args) > 0 {
		// BOOLEAN mode: the short way to say "yes, the default". true generates
		// with defaults; false declares nothing at all, so the path stays a 404
		// rather than serving an empty document.
		if args[0].K == value.Bool {
			if args[0].N == 0 {
				return m
			}
			outputPath = "/" + strings.TrimPrefix(path.Clean("/"+outputPath), "/")
			m.outputPath = outputPath
			f.outputs[outputPath] = m
			return m
		}
		if args[0].IsString() { // FILE mode: serve the disk file at the canonical URL (Zero-VM static)
			m.outputPath = "/" + strings.TrimPrefix(path.Clean("/"+defaultPath), "/")
			if disk := cleanAssetPrefix(args[0].Text()); disk != "" {
				if err := f.tenant.presentation().AddAssetMount(site.AssetMount{
					URL:  path.Join(f.node.relPath(), cleanAssetPrefix(defaultPath)),
					Disk: path.Join(f.node.relPath(), disk),
				}); err != nil && f.assetErr == nil {
					f.assetErr = err
				}
			}
			return m // served statically; not registered as a generated output
		}
		if args[0].K == value.Func {
			m.handle = lambdaOf(args[0])
		} else {
			m.outputData = args[0]
			if args[0].IsMap() {
				if configured, ok := args[0].Map()["path"]; ok && configured.Text() != "" {
					outputPath = configured.Text()
				}
			}
		}
	}
	outputPath = "/" + strings.TrimPrefix(path.Clean("/"+outputPath), "/")
	m.outputPath = outputPath
	f.outputs[outputPath] = m
	return m
}

func (f *FolderRouter) generatedOutput(requestPath string) *FolderMethod {
	requestPath = path.Clean("/" + requestPath)
	if method := f.outputs[requestPath]; method != nil {
		return method
	}
	for _, method := range f.outputs {
		if method.outputKind != "sitemap" {
			continue
		}
		extension := path.Ext(method.outputPath)
		base := strings.TrimSuffix(method.outputPath, extension)
		if !strings.HasPrefix(requestPath, base+"-") || !strings.HasSuffix(requestPath, extension) {
			continue
		}
		pageText := strings.TrimSuffix(strings.TrimPrefix(requestPath, base+"-"), extension)
		if page, err := strconv.Atoi(pageText); err == nil && page > 0 {
			return method
		}
	}
	return nil
}

// RSS declares a generated RSS 2.0 feed at /rss.xml. It accepts either a provider callback or a
// channel map whose items field may itself be a callback. The returned method supports cache,
// persist, limit, and guards just like router.get().
func (f *FolderRouter) RSS(args ...value.Value) *FolderMethod {
	return f.output("rss", "/rss.xml", args...)
}

// Sitemap declares a generated sitemap at /sitemap.xml from a provider callback or page map.
func (f *FolderRouter) Sitemap(args ...value.Value) *FolderMethod {
	return f.output("sitemap", "/sitemap.xml", args...)
}

// Robots declares the site's /robots.txt. Like sitemap/rss it is polymorphic by argument shape:
// a STRING serves that disk file verbatim (Zero-VM static — e.g. router.robots("./robots.txt"));
// a MAP / no-arg GENERATES the file — router.robots() is allow-all plus an auto Sitemap line for
// the serving domain, router.robots({ disallow: ["/dashboard"] }) blocks paths. Per-page indexing
// is a different concern: use meta({ robots: "noindex" }) for a <meta name="robots"> tag.
func (f *FolderRouter) Robots(args ...value.Value) *FolderMethod {
	return f.output("robots", "/robots.txt", args...)
}

// Llms declares the site's /llms.txt — the plain-Markdown map of a site that
// language models read instead of crawling HTML. Same argument shapes as
// robots/sitemap: a STRING serves that disk file, a MAP generates one, true
// generates the defaults.
//
//	router.llms("./llms.txt")
//	router.llms({ title: "Kitwork", summary: "…", sections: [...] })
//
// The longer companion document is the same output at another path, so it needs
// no second method:
//
//	router.llms({ path: "/llms-full.txt", title: "Kitwork", sections: [...] })
func (f *FolderRouter) Llms(args ...value.Value) *FolderMethod {
	return f.output("llms", "/llms.txt", args...)
}

// LlmsFull declares the companion /llms-full.txt — the expanded document, for a
// reader that wants the whole map rather than the index. Same argument shapes.
// It is a separate method rather than a path option because the two documents
// have different canonical paths, and a convention is only discoverable if it
// has a name.
//
//	router.llmsFull(true)
//	router.llmsFull("./llms-full.txt")
//	router.llmsFull({ title: "Kitwork", sections: [...] })
func (f *FolderRouter) LlmsFull(args ...value.Value) *FolderMethod {
	return f.output("llms", "/llms-full.txt", args...)
}

// Manifest declares the site's /manifest.json — the web app manifest a browser
// reads to install the site. Same argument shapes as robots/sitemap: a STRING
// serves that disk file, a MAP generates one, true generates defaults.
//
//	router.manifest(true)
//	router.manifest({ name: "Kitwork", theme: "#f7f7f8", icon: "/assets/logo.png" })
//	router.manifest("./manifest.json")
//
// Unlike the others this one must also be LINKED from <head> to have any
// effect; jit/manifest fills a <link data-kitwork-generated="manifest"> marker where
// the author placed one, and injects the link when none is present.
func (f *FolderRouter) Manifest(args ...value.Value) *FolderMethod {
	method := f.output("manifest", "/manifest.json", args...)
	if method != nil && method.outputPath != "" {
		f.tenant.presentation().SetManifestPath(method.outputPath)
	}
	return method
}

// Guard registers folder-level before-hooks (auth/prepare) that run IN ORDER for this folder and
// every descendant — the outside-in cascade. Accepts a fn, a variadic list, or an array.
func (f *FolderRouter) Guard(args ...value.Value) *FolderRouter {
	f.guards = appendLambdas(f.guards, args...)
	return f
}

// Middleware is a deprecated alias for Guard — the two collapsed into one before-chain.
func (f *FolderRouter) Middleware(args ...value.Value) *FolderRouter { return f.Guard(args...) }

func (f *FolderRouter) Notfound(l value.Value) *FolderRouter { f.notFound = lambdaOf(l); return f }
func (f *FolderRouter) Error(l value.Value) *FolderRouter    { f.errorH = lambdaOf(l); return f }

// Meta merges the given fields into this folder's meta; every field becomes $.meta.<key> in the
// view, inherited by descendant folders. Declare site-wide defaults once at the root.
func (f *FolderRouter) Meta(v value.Value) *FolderRouter {
	if v.IsMap() {
		for k, val := range v.Map() {
			f.meta[k] = val
		}
	}
	return f
}

// Ratelimit adds rate-limit rules for this folder AND every descendant (enforced on the resolve
// chain, outside-in — declare site-wide ceilings once at the root). Calls stack; see ratelimitRules
// for the shape. One bucket per client per rule for the whole subtree.
func (f *FolderRouter) Ratelimit(args ...value.Value) *FolderRouter {
	rules := ratelimitRules(args...)
	for _, rule := range rules {
		f.checkLimitBy(rule.Dim)
	}
	f.limits = append(f.limits, rules...)
	return f
}

// RateLimit is the camelCase alias (.rateLimit).
func (f *FolderRouter) RateLimit(args ...value.Value) *FolderRouter { return f.Ratelimit(args...) }

// Favicon declares the favicon: sugar for meta({ favicon }) — templates read $.meta.favicon — AND,
// at the site root, registers the file to be served at /favicon.ico, which browsers request
// unprompted. A path starting with / is the site's URL path (/assets/favicon.svg is the file under
// the site root); any other path is inside this folder. A branch's favicon is the icon its pages link
// (<link rel="icon">), never the site's /favicon.ico.
func (f *FolderRouter) Favicon(args ...value.Value) *FolderRouter {
	if len(args) == 0 {
		return f
	}
	f.meta["favicon"] = args[0]
	written := strings.TrimSpace(args[0].Text())
	dir, url := f.node.diskPath(), "/"+f.node.relPath()
	if strings.HasPrefix(written, "/") {
		dir, url = f.tenant.resolve(), "/"
	}
	rel := strings.TrimPrefix(strings.TrimPrefix(written, "./"), "/")
	if rel == "" {
		return f
	}
	f.favicon = path.Join(url, path.Clean(rel))
	if strings.Contains(written, "://") {
		f.favicon = written // an icon on another host is linked as written
		return f
	}
	if f.node.parent != nil {
		return f
	}
	full := filepath.Join(dir, filepath.FromSlash(path.Clean(rel)))
	base := f.tenant.resolve()
	if r, err := filepath.Rel(base, full); err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		f.tenant.presentation().SetFaviconFile(full)
	}
	return f
}

// assetMount maps a PUBLIC url prefix to a DISK dir prefix (both relative to the tenant root). For a
// plain .assets("assets/*") the two are identical (1:1); the alias form points a clean public URL at
// a different — often private, underscore-prefixed — folder.
// Assets DECLARES the public static roots — an allowlist. Without any declaration every safe disk
// file under the tenant is auto-served (zero-config, see serveTreeStatic); once a folder declares
// .assets(...), ONLY the declared prefixes are public and everything else routes through the tree.
// Forms (relative to this folder, accumulating + idempotent under hot-reload):
//
//	.assets("assets/*")             // serve disk "assets/"  at URL /assets/*   (1:1, no underscore)
//	.assets("_assets")              // serve disk "_assets/" at URL /assets/*   (leading "_" stripped from the URL)
//	.assets("/assets/*", "_assets") // explicit alias: the two args are the URL and the disk, taken as-is
//
// A SINGLE arg derives the public URL from the disk prefix but drops the leading underscore of each
// segment — "_" marks a private, non-routable folder and must never leak into a public URL, so disk
// "_assets" serves at /assets/*. The forms "_assets", "/_assets/", "./_assets/*" all normalise the
// same way. Use the two-arg form when the URL must differ from that default.
func (f *FolderRouter) Assets(args ...value.Value) *FolderRouter {
	add := func(url, disk string) {
		url, disk = cleanAssetPrefix(url), cleanAssetPrefix(disk)
		if url == "" || disk == "" {
			return
		}
		if err := f.tenant.presentation().AddAssetMount(site.AssetMount{
			URL:  path.Join(f.node.relPath(), url),
			Disk: path.Join(f.node.relPath(), disk),
		}); err != nil && f.assetErr == nil {
			f.assetErr = err
		}
	}
	// BOOLEAN mode: assets(true) mounts the conventional folder without naming
	// it. Only a folder that exists on disk is mounted, so the shorthand never
	// leaves a dead mount behind; "_assets" is accepted because publicAssetURL
	// already maps a private folder onto the clean public /assets URL.
	if len(args) == 1 && args[0].K == value.Bool {
		if args[0].N == 0 {
			return f
		}
		for _, candidate := range [...]string{"assets", "_assets"} {
			full := filepath.Join(f.node.diskPath(), candidate)
			if info, err := os.Stat(full); err == nil && info.IsDir() {
				add(publicAssetURL(candidate), candidate)
			}
		}
		return f
	}
	if len(args) == 2 { // alias form: (publicURLGlob, diskDir) — explicit, taken as-is
		add(args[0].Text(), args[1].Text())
		return f
	}
	for _, a := range args { // single form: serve the (possibly private) dir at a clean public URL
		disk := cleanAssetPrefix(a.Text())
		add(publicAssetURL(disk), disk)
	}
	return f
}

// cleanAssetPrefix normalises "./assets/*", "/assets", "assets/" → "assets" (no slashes, no glob).
func cleanAssetPrefix(s string) string {
	s = strings.TrimPrefix(s, "./")
	s = strings.TrimSuffix(strings.TrimSuffix(s, "/*"), "/")
	s = strings.Trim(path.Clean("/"+s), "/")
	if s == "." {
		return ""
	}
	return s
}

// publicAssetURL derives the PUBLIC url prefix from a (cleaned) disk prefix by dropping the leading
// underscore of each path segment. The underscore marks a private, non-routable folder, so it must
// never leak into a public URL: disk "_assets" → url "assets" (served at /assets/*), "_a/_b" →
// "a/b". A segment without an underscore is unchanged, so "assets" stays "assets" — the historical
// single-arg 1:1 case is untouched. Only the single-arg .assets() form uses this; the two-arg alias
// form takes the URL exactly as given.
func publicAssetURL(disk string) string {
	if disk == "" {
		return ""
	}
	segs := strings.Split(disk, "/")
	for i, s := range segs {
		segs[i] = strings.TrimPrefix(s, "_")
	}
	return strings.Join(segs, "/")
}

// Language is sugar for meta({ language }) — $.meta.language in the view, for <html lang="…">.
func (f *FolderRouter) Language(v value.Value) *FolderRouter { f.meta["language"] = v; return f }

// Highlight is Kitwork UI's router.highlight: the colour of each kind of code token of the .ui pages,
// router.highlight({ keyword: "violet", "function": { light, dark } }) (uiHighlight,
// router_ui_highlight.go). The template pages' grammar of the same word (a named theme for
// {{ src.highlight() }}) went with the template pages on 27/10/2026: a router.kitwork.js that still
// writes it is told so.
func (f *FolderRouter) Highlight(args ...value.Value) (*FolderRouter, error) {
	if f != nil && f.ui {
		return f.uiHighlight(args...)
	}
	return f, fmt.Errorf("router.highlight của trang template không còn (27/10/2026): màu khối mã của trang .ui viết trong router.work, router.highlight({ keyword: \"brand\", comment: \"ink-30\" })")
}

// Meta shorthands at the node-declaration level — same set as the ViewBuilder, so a static page
// can set its own title in one line (router.title("...")) without a handler, and it still
// inherits/overrides down the chain like any other meta.
func (f *FolderRouter) Title(v value.Value) *FolderRouter       { f.meta["title"] = v; return f }
func (f *FolderRouter) Description(v value.Value) *FolderRouter { f.meta["description"] = v; return f }
func (f *FolderRouter) Image(v value.Value) *FolderRouter       { f.meta["image"] = v; return f }
func (f *FolderRouter) Url(v value.Value) *FolderRouter         { f.meta["url"] = v; return f }
func (f *FolderRouter) Type(v value.Value) *FolderRouter        { f.meta["type"] = v; return f }

// Jsonld declares a JSON-LD structured-data node for SEO. Each call adds ONE node (a plain object or
// array); the engine serialises them HTML-safe — no </script> breakout — into the JSON string
// $.meta.jsonld, which the head template drops into a <script type="application/ld+json">. Declared
// at any folder it inherits down the chain and ACCUMULATES with page-level ctx.jsonld(...), so a
// site-wide Organization at the root plus a per-page FAQPage combine (several nodes are emitted under
// one @context in an @graph array). A default "@context":"https://schema.org" is added when a node
// has none, so callers pass only the schema body.
func (f *FolderRouter) Jsonld(args ...value.Value) *FolderRouter {
	for _, a := range args {
		if a.K == value.Map || a.K == value.Array {
			f.jsonld = append(f.jsonld, a)
		}
	}
	return f
}

// ── tree-mode kitwork() binding ─────────────────────────────────────────────

// TreeKitWork is the kitwork() a folder's router.kitwork.js sees at compile time. It embeds the
// normal *KitWork (so env/db/http/render/… all work) and shadows Router() to return the folder
// collector for the node being compiled.
type TreeKitWork struct {
	*KitWork
	node *RouteNode
}

// Router is the folder's router. A Kitwork UI folder's answers only the words of kitwork/router
// (uiRouter, router_vocabulary.go); a template's is the folder itself, every old word with it.
func (tw *TreeKitWork) Router() value.Value {
	if folder := tw.node.folder; folder != nil && folder.ui {
		return value.New(&uiRouter{folder: folder})
	}
	return value.New(tw.node.folder)
}

// ── generation folder compile ──────────────────────────────────────────────

// ensureFolder compiles + runs this node's router.kitwork.js exactly once, collecting its
// guards/middleware/methods into node.folder. A folder with no router.kitwork.js still gets an
// empty FolderRouter so its page.kitwork.html can render.
func (n *RouteNode) ensureFolder(t *Tenant) *FolderRouter {
	if n.folderReady.Load() {
		return n.folder
	}
	n.folderMu.Lock()
	defer n.folderMu.Unlock()
	if n.folderReady.Load() {
		return n.folder
	}

	n.folderErr = n.compileFolder(t)
	n.folderReady.Store(true)
	return n.folder
}

func (n *RouteNode) compileError() error {
	n.folderMu.Lock()
	err := n.folderErr
	n.folderMu.Unlock()
	return err
}

func (n *RouteNode) sourceFiles() []string {
	n.folderMu.Lock()
	files := append([]string(nil), n.srcFiles...)
	n.folderMu.Unlock()
	return files
}

// compileFolder builds this generation's FolderRouter and records every
// executable source file used by its native import graph. A Kitwork UI folder (uiFolder) runs its
// config.work before its router.work, its site's root the app's config.work before both
// (compileUIFolder); a template's folder runs its router.kitwork.js alone, as it always has.
func (n *RouteNode) compileFolder(t *Tenant) error {
	fr := &FolderRouter{
		tenant: t, node: n,
		methods: map[string]*FolderMethod{}, outputs: map[string]*FolderMethod{}, meta: map[string]value.Value{},
	}
	n.folder = fr // publish before running so TreeKitWork.Router() can reach it

	routerFile := RouterMarker(n.diskPath()) // router.kitwork.js, or router.work for a Kitwork UI site
	// watched even when absent — a router APPEARING is a change
	n.srcFiles = []string{filepath.Join(n.diskPath(), RouterFileName), filepath.Join(n.diskPath(), UIRouterFileName)}
	if n.uiFolder(routerFile) {
		return n.compileUIFolder(t, fr, routerFile)
	}
	if routerFile == "" {
		if n.parent == nil {
			// a site's root is marked by its router file; a config.work there alone would run nowhere, its
			// guards with it, and neither would the app's (KITWORK_ROUTER.md mục 4)
			configFile := filepath.Join(n.diskPath(), compiler.RouterConfigFile)
			n.srcFiles = append(n.srcFiles, configFile)
			if fileExists(configFile) {
				return fmt.Errorf("%s: %s", compiler.RouterConfigFile, routerConfigWithoutRouter)
			}
		}
		return nil
	}
	bc, err := fr.runRouterFile(t, routerFile)
	if bc != nil && len(bc.Files) > 0 {
		n.srcFiles = append([]string(nil), bc.Files...)
	}
	return err
}

// uiFolder reports whether the folder is a Kitwork UI site's (KITWORK_ROUTER.md mục 4): its router file is
// router.work, or it has none and its site's root's router file is router.work. Only such a folder reads a
// config.work; a template's (router.kitwork.js) never does.
func (n *RouteNode) uiFolder(routerFile string) bool {
	if routerFile != "" {
		return filepath.Base(routerFile) == UIRouterFileName
	}
	if n.parent == nil {
		return false
	}
	root := n
	for root.parent != nil {
		root = root.parent
	}
	return root.folder != nil && root.folder.ui
}

// compileUIFolder builds a Kitwork UI folder: at a site's root the app's config.work first, as the parent
// of the site (appRouterConfig, seed), then the folder's own config.work, then its router.work, each run
// into the same FolderRouter, so the calls of config.work come first (KITWORK_ROUTER.md mục 4, 12).
// config.work is watched even when absent: one appearing, changing or going builds the generation again.
func (n *RouteNode) compileUIFolder(t *Tenant, fr *FolderRouter, routerFile string) error {
	fr.ui = true // router.highlight reads Kitwork UI's grammar; the router answers its words only
	configFile := filepath.Join(n.diskPath(), compiler.RouterConfigFile)
	n.srcFiles = append(n.srcFiles, configFile)
	if n.parent == nil {
		// A Kitwork UI site swaps only <content /> between pages by default; router.reload(true) in it
		// loads every page whole instead.
		t.presentation().SetDrive(true)
		fr.appRoot = sameFolder(n.diskPath(), t.resolveApp())
		if !fr.appRoot {
			collector, files, err := t.appRouterConfig(false)
			n.srcFiles = append(n.srcFiles, files...)
			if err != nil {
				return err
			}
			if collector != nil {
				fr.timezone = collector.timezone
				if err := fr.seed(collector); err != nil {
					return err
				}
			}
		}
	}
	if fileExists(configFile) {
		bc, err := fr.runRouterFile(t, configFile)
		if bc != nil {
			n.srcFiles = append(n.srcFiles, bc.Files...)
			fr.configBytecode = bc
		}
		if err != nil {
			return err
		}
	}
	if routerFile == "" {
		return nil
	}
	bc, err := fr.runRouterFile(t, routerFile)
	if bc != nil {
		n.srcFiles = append(n.srcFiles, bc.Files...)
	}
	return err
}

// sameFolder reports whether two paths name one folder.
func sameFolder(one, other string) bool {
	first, err := filepath.Abs(one)
	if err != nil {
		first = one
	}
	second, err := filepath.Abs(other)
	if err != nil {
		second = other
	}
	return filepath.Clean(first) == filepath.Clean(second)
}

// runRouterFile compiles and runs one router file of the folder (router.kitwork.js, router.work or a
// config.work) into it, then says what the run left to say: an error stops the generation with the file
// from the site's root, a template's warnings are printed. The bytecode is given whenever the file compiled,
// so its sources are watched even when it then fails.
func (f *FolderRouter) runRouterFile(t *Tenant, file string) (*compiler.Bytecode, error) {
	relative := strings.TrimPrefix(file, t.resolve()+string(filepath.Separator))
	bc, err := t.compileFile(file)
	if err != nil {
		return nil, fmt.Errorf("compile router %s: %w", relative, err)
	}
	if filepath.Base(file) != compiler.RouterConfigFile {
		f.bytecode = bc
	}
	// every environment.require("KEY") of the folder's code against the app's environment, before it
	// runs: a missing key stops the boot, with every other folder's in one report (RouteTree.Prepare)
	if missing := t.missingEnvironment(bc); missing != nil {
		missing.placeEntry(filepath.Base(file), filepath.ToSlash(relative))
		return bc, missing
	}
	if f.ui {
		f.running = filepath.Base(file)
		defer func() { f.running = "" }()
	}
	if err := runFolderRouter(t, f.node, bc); err != nil {
		return bc, fmt.Errorf("initialize router %s: %w", relative, err)
	}
	if f.redeclared != nil {
		return bc, fmt.Errorf("router %s: %w", relative, f.redeclared)
	}
	if f.limitErr != nil {
		return bc, fmt.Errorf("router %s: %w", relative, f.limitErr)
	}
	if err := f.handlerDestructureError(); err != nil {
		// the folder's file as the other errors name it: a guard's parameter in a/config.work is not one of
		// the site's other config.work
		return bc, fmt.Errorf("router %s: %w", relative, err)
	}
	if err := f.responseCacheConfigurationError(); err != nil {
		return bc, fmt.Errorf("router %s: %w", relative, err)
	}
	if f.assetErr != nil {
		return bc, fmt.Errorf("router %s: %w", relative, f.assetErr)
	}
	if warning := f.unknownMethodWarning(); warning != "" {
		fmt.Printf("[router] %s: %s\n", relative, warning)
	}
	if f.expiryWarning != "" {
		fmt.Printf("[router] %s: %s\n", relative, f.expiryWarning)
	}
	if f.limitWarning != "" {
		fmt.Printf("[router] %s: %s\n", relative, f.limitWarning)
	}
	for _, warning := range f.removedMethodWarnings() {
		fmt.Printf("[router] %s: %s\n", relative, warning)
	}
	return bc, nil
}

// runFolderRouter executes a folder's compiled router in an ISOLATED VM whose kitwork() is the
// tree binding. Isolated (not the request pool) so the tree kitwork never leaks into a pooled
// VM that a later flat-mode request might reuse. The handler lambdas it registers carry Address
// offsets into bc — the same bc is FastReset back in at request time (see tree_serve.go).
func runFolderRouter(t *Tenant, n *RouteNode, bc *compiler.Bytecode) error {
	var vm *runtime.VM
	treeKitwork := value.NewFunc(func(args ...value.Value) value.Value {
		return value.New(&TreeKitWork{KitWork: &KitWork{tenant: t, vm: vm}, node: n})
	})

	globals := make(map[string]value.Value, len(t.vm.Globals)+1)
	for k, v := range t.vm.Globals {
		globals[k] = v
	}
	globals[kitwork] = treeKitwork

	vm = runtime.New(bc.Program)
	vm.Builtins = []value.Value{treeKitwork}
	vm.Globals = globals
	vm.MaxEnergy = t.MaxEnergy
	if fr := n.folder; fr != nil {
		// a native call of the router (router.highlight) asks where it is written: the instruction the VM
		// runs, the call, in the current frame. Only while the router runs, so no VM is kept after it.
		fr.callSite = func() runtime.SourceLocation {
			if vm.FrameIdx < 0 || vm.FrameIdx >= len(vm.Frames) {
				return runtime.SourceLocation{}
			}
			return vm.Program().SourceAt(vm.Frames[vm.FrameIdx].LastIP)
		}
		defer func() { fr.callSite = nil }()
	}
	// a router only declares as it loads: routine.go or routine.wait at its top level is refused
	defer loadingFile(vm)()
	if result := vm.Run(); result.K == value.Invalid {
		return fmt.Errorf("%s", result.Text())
	}
	return nil
}
