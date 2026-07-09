package work

// Per-folder router: the collector that a folder's router.kitwork.js writes into, plus the
// tree-mode kitwork() binding and the lazy one-time compile of that script.
//
// In a tree tenant, `const { router } = kitwork()` inside a folder's router.kitwork.js must
// yield a collector bound to THAT folder — not the flat, path-registering Router. We get that
// by handing the folder-compile VM a kitwork() that returns a TreeKitWork, whose Router()
// shadows the flat one and returns the folder's *FolderRouter. Every other capability
// (env, db, http, render…) is promoted from the embedded *KitWork unchanged.

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
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
	method   string
	handle   *value.Lambda
	guards   []*value.Lambda // the before-chain: runs in order; each may block / respond / prepare
	success  *value.Lambda   // runs after a clean handler (canonical name for the old `then`)
	errorH   *value.Lambda   // runs when the method errored (canonical name for the old `catch`)
	final    *value.Lambda
	isView   bool
	viewArgs []value.Value

	// Response caching + rate limiting (see cache/persist/ratelimit helper packages). The expiry
	// resolvers accept a rolling duration OR a wall-clock boundary ("nextday 03:00", "weekly", …),
	// evaluated at save time; nil = that tier is off.
	cacheExpiry   func(time.Time) time.Duration // .cache(): RAM
	persistExpiry func(time.Time) time.Duration // .persist(): disk <tenant>/.persist
	limits        []methodLimit                 // .limit(...): rate-limit rules
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

// View ends a method by rendering this folder's page — no JS handler:
//
//	router.get().view()          // GET → this folder's page.kitwork.html
func (m *FolderMethod) View(args ...value.Value) *FolderMethod {
	m.isView = true
	m.viewArgs = args
	return m
}

// Cache keeps the rendered response in RAM (fast, lost on restart). The argument is a rolling
// duration OR a wall-clock boundary; no arg = forever:
//
//	router.get(...).cache("5m")             // rolling 5 minutes
//	router.get(...).cache("nextday 03:00")  // until 03:00 tomorrow (aligns to a data refresh)
//	router.get(...).cache("weekly")         // until next Monday 00:00
func (m *FolderMethod) Cache(args ...value.Value) *FolderMethod {
	m.cacheExpiry = expiryOf(args...)
	return m
}

// Persist writes the rendered response to <tenant>/.persist on disk — any content type
// (html/json/image) — served with no VM and surviving restarts. Same expiry grammar as Cache;
// no arg = forever (until the file is removed):
//
//	router.get(...).persist()               // forever
//	router.get(...).persist("nextday 03:00")
func (m *FolderMethod) Persist(args ...value.Value) *FolderMethod {
	m.persistExpiry = expiryOf(args...)
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
		m.limits = append(m.limits, lim)
	}
	return m
}

// ratelimitRules parses the .ratelimit() config shape into rules. One map per call; each DIMENSION
// key (ip/browser/user/global) carries its own rate, sharing the map's period — and calls STACK, so
// layered windows read naturally:
//
//	router.ratelimit({ ip: 30, period: "1s" })   // burst ceiling
//	      .ratelimit({ ip: 600, period: "1m" })  // sustained ceiling
//	      .ratelimit({ global: 5000, period: "1m" })
//
// The legacy { rate, per, type } shape is accepted too. Rules missing a positive rate or period
// are dropped (never a silent zero-limit).
func ratelimitRules(args ...value.Value) []methodLimit {
	var rules []methodLimit
	for _, a := range args {
		if !a.IsMap() {
			continue
		}
		mp := a.Map()
		per := time.Second
		if p, ok := mp["period"]; ok {
			per = parseTTL(p)
		} else if p, ok := mp["per"]; ok {
			per = parseTTL(p)
		}
		if per <= 0 {
			continue
		}
		for _, dim := range []string{"ip", "browser", "user", "global"} {
			if rv, ok := mp[dim]; ok && int(rv.N) > 0 {
				rules = append(rules, methodLimit{Dim: dim, Rate: int(rv.N), Per: per})
			}
		}
		if rv, ok := mp["rate"]; ok && int(rv.N) > 0 { // legacy shape
			dim := "ip"
			if d, ok := mp["type"]; ok && d.Text() != "" {
				dim = d.Text()
			}
			rules = append(rules, methodLimit{Dim: dim, Rate: int(rv.N), Per: per})
		}
	}
	return rules
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
	notFound *value.Lambda
	errorH   *value.Lambda
	meta     map[string]value.Value // declared via router.meta()/.favicon(); inherited down the chain
	limits   []methodLimit          // router.ratelimit() rules — this folder AND every descendant
}

func (f *FolderRouter) declare(name string, args ...value.Value) *FolderMethod {
	m := &FolderMethod{method: name}
	if len(args) > 0 && args[0].K == value.Func { // router.get(handler) shorthand
		m.handle = lambdaOf(args[0])
	}
	f.methods[name] = m
	return m
}

func (f *FolderRouter) Get(args ...value.Value) *FolderMethod    { return f.declare("GET", args...) }
func (f *FolderRouter) Post(args ...value.Value) *FolderMethod   { return f.declare("POST", args...) }
func (f *FolderRouter) Put(args ...value.Value) *FolderMethod    { return f.declare("PUT", args...) }
func (f *FolderRouter) Patch(args ...value.Value) *FolderMethod  { return f.declare("PATCH", args...) }
func (f *FolderRouter) Delete(args ...value.Value) *FolderMethod { return f.declare("DELETE", args...) }

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
	f.limits = append(f.limits, ratelimitRules(args...)...)
	return f
}

// RateLimit is the camelCase alias (.rateLimit).
func (f *FolderRouter) RateLimit(args ...value.Value) *FolderRouter { return f.Ratelimit(args...) }

// Favicon declares the favicon: sugar for meta({ favicon }) — templates read $.meta.favicon — AND
// registers the file (resolved inside this folder) to be served at /favicon.ico, which browsers
// request unprompted.
func (f *FolderRouter) Favicon(args ...value.Value) *FolderRouter {
	if len(args) == 0 {
		return f
	}
	f.meta["favicon"] = args[0]
	rel := strings.TrimPrefix(strings.TrimPrefix(args[0].Text(), "./"), "/")
	if rel == "" {
		return f
	}
	full := filepath.Join(f.node.diskPath(), filepath.FromSlash(path.Clean(rel)))
	base := f.tenant.resolve()
	if r, err := filepath.Rel(base, full); err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		f.tenant.faviconFile = full
	}
	return f
}

// Assets DECLARES the public static roots — an allowlist. Without any declaration every safe disk
// file under the tenant is auto-served (zero-config, see serveTreeStatic); once a folder declares
// .assets("./assets/*"), ONLY the declared prefixes are served and everything else routes through
// the tree. Multiple patterns/calls accumulate; the pattern is relative to this folder.
func (f *FolderRouter) Assets(args ...value.Value) *FolderRouter {
	for _, a := range args {
		p := strings.TrimPrefix(a.Text(), "./")
		p = strings.TrimSuffix(strings.TrimSuffix(p, "/*"), "/")
		p = strings.Trim(path.Clean("/"+p), "/")
		if p == "" || p == "." {
			continue
		}
		prefix := path.Join(f.node.relPath(), p)
		if !containsString(f.tenant.assetPrefixes, prefix) { // idempotent under hot-reload recompile
			f.tenant.assetPrefixes = append(f.tenant.assetPrefixes, prefix)
		}
	}
	return f
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Language is sugar for meta({ language }) — $.meta.language in the view, for <html lang="…">.
func (f *FolderRouter) Language(v value.Value) *FolderRouter { f.meta["language"] = v; return f }

// Meta shorthands at the node-declaration level — same set as the ViewBuilder, so a static page
// can set its own title in one line (router.title("...")) without a handler, and it still
// inherits/overrides down the chain like any other meta.
func (f *FolderRouter) Title(v value.Value) *FolderRouter       { f.meta["title"] = v; return f }
func (f *FolderRouter) Description(v value.Value) *FolderRouter { f.meta["description"] = v; return f }
func (f *FolderRouter) Image(v value.Value) *FolderRouter       { f.meta["image"] = v; return f }
func (f *FolderRouter) Url(v value.Value) *FolderRouter         { f.meta["url"] = v; return f }
func (f *FolderRouter) Type(v value.Value) *FolderRouter        { f.meta["type"] = v; return f }

// ── tree-mode kitwork() binding ─────────────────────────────────────────────

// TreeKitWork is the kitwork() a folder's router.kitwork.js sees at compile time. It embeds the
// normal *KitWork (so env/db/http/render/… all work) and shadows Router() to return the folder
// collector for the node being compiled.
type TreeKitWork struct {
	*KitWork
	node *RouteNode
}

func (tw *TreeKitWork) Router() *FolderRouter { return tw.node.folder }

// ── lazy one-time folder compile ────────────────────────────────────────────

// ensureFolder compiles + runs this node's router.kitwork.js exactly once, collecting its
// guards/middleware/methods into node.folder. A folder with no router.kitwork.js still gets an
// empty FolderRouter so its page.kitwork.html can render.
func (n *RouteNode) ensureFolder(t *Tenant) *FolderRouter {
	if n.folderReady.Load() {
		if t.HotReload {
			n.hotCheck(t)
		}
		return n.folder
	}
	n.folderMu.Lock()
	defer n.folderMu.Unlock()
	if n.folderReady.Load() {
		return n.folder
	}

	n.compileFolder(t)
	n.folderReady.Store(true)
	return n.folder
}

// compileFolder (re)builds this node's FolderRouter from disk and records the source snapshot the
// hot-reload check compares against. Caller holds folderMu.
func (n *RouteNode) compileFolder(t *Tenant) {
	fr := &FolderRouter{tenant: t, node: n, methods: map[string]*FolderMethod{}, meta: map[string]value.Value{}}
	n.folder = fr // publish before running so TreeKitWork.Router() can reach it

	routerFile := filepath.Join(n.diskPath(), "router"+extension+".js")
	n.srcFiles = []string{routerFile} // watched even when absent — a router APPEARING is a change
	n.srcMod = 0
	if info, err := os.Stat(routerFile); err == nil && !info.IsDir() {
		n.srcMod = info.ModTime().UnixNano()
		if bc, err := compiler.CompileFile(routerFile); err == nil {
			fr.bytecode = bc
			if len(bc.Files) > 0 {
				n.srcFiles = bc.Files // entry + every natively-bundled import (./_core/…)
				n.srcMod = maxModTime(bc.Files)
			}
			runFolderRouter(t, n, bc)
		}
	}
	if info, err := os.Stat(n.diskPath()); err == nil {
		n.dirMod = info.ModTime().UnixNano()
	}
}

// hotCheck is the per-folder hot reload: at most once per second (per node), stat the folder's
// source files and its directory. An edited router — or an edited MODULE it imports — recompiles
// just this folder in place; a created/removed child folder invalidates the children cache so the
// next resolve rediscovers the structure. Views (*.kitwork.html) need none of this: the render
// reads them from disk every request.
func (n *RouteNode) hotCheck(t *Tenant) {
	now := time.Now().UnixNano()
	last := n.hotCheckAt.Load()
	if now-last < int64(time.Second) || !n.hotCheckAt.CompareAndSwap(last, now) {
		return
	}

	n.folderMu.Lock()
	defer n.folderMu.Unlock()
	if maxModTime(n.srcFiles) != n.srcMod {
		n.compileFolder(t)
	}
	if info, err := os.Stat(n.diskPath()); err == nil && info.ModTime().UnixNano() != n.dirMod {
		n.dirMod = info.ModTime().UnixNano()
		n.built.Store(false) // children rescan on the next resolve (new/removed folders)
	}
}

// maxModTime returns the newest modtime (unix nanos) across files; missing files count as 0, so
// a deletion changes the max and is detected like an edit.
func maxModTime(files []string) int64 {
	var max int64
	for _, f := range files {
		if info, err := os.Stat(f); err == nil {
			if m := info.ModTime().UnixNano(); m > max {
				max = m
			}
		}
	}
	return max
}

// runFolderRouter executes a folder's compiled router in an ISOLATED VM whose kitwork() is the
// tree binding. Isolated (not the request pool) so the tree kitwork never leaks into a pooled
// VM that a later flat-mode request might reuse. The handler lambdas it registers carry Address
// offsets into bc — the same bc is FastReset back in at request time (see tree_serve.go).
func runFolderRouter(t *Tenant, n *RouteNode, bc *compiler.Bytecode) {
	treeKitwork := value.NewFunc(func(args ...value.Value) value.Value {
		return value.New(&TreeKitWork{KitWork: t.Kitwork(args...), node: n})
	})

	globals := make(map[string]value.Value, len(t.vm.Globals)+1)
	for k, v := range t.vm.Globals {
		globals[k] = v
	}
	globals[kitwork] = treeKitwork

	vm := runtime.New(bc.Instructions, bc.Constants)
	vm.Builtins = []value.Value{treeKitwork}
	vm.Globals = globals
	vm.SourceMap = bc.SourceMap
	vm.MaxEnergy = t.MaxEnergy
	vm.Run()
}
