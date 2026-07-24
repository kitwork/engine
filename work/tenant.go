package work

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kitwork/engine/capabilities"
	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/database"
	jitcss "github.com/kitwork/engine/jit/css"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/utilities/cache"
	collectionhelper "github.com/kitwork/engine/utilities/collection"
	httphelper "github.com/kitwork/engine/utilities/http"
	"github.com/kitwork/engine/utilities/persist"
	"github.com/kitwork/engine/utilities/ratelimit"
	"github.com/kitwork/engine/value"
)

const kitwork = "kitwork"
const extension = "." + kitwork

const (
	ScopeTenant = 0
	ScopeServer = 1
	ScopeMax    = 2
)

// RouterFileName is the tenant marker: the root router of the filesystem-routed tree
// (router.kitwork.js). A folder holding one IS a tenant — discovery, hot reload and the layout
// conventions all key off it. (The old flat app.kitwork.js entry is gone with the tree-only
// cutover and plays no part anywhere.)
const RouterFileName = "router" + extension + ".js"

type Tenant struct {
	config *Config
	entity *Entity

	bytecode  *compiler.Bytecode
	vm        *runtime.VM
	MaxEnergy uint64
	// HotReload enables the per-folder hot check: an edited router.kitwork.js (or an imported
	// module) recompiles just its folder; created/removed folders re-enter the tree. Set by the
	// engine from config; off = every compile is exactly once (production).
	HotReload bool

	// tree, when non-nil, marks this tenant as FILESYSTEM-ROUTED: requests are resolved by
	// walking the folder tree (each folder = a runtime node) instead of the flat routes table.
	// Activated by a `filesystem.kitwork` marker at the tenant root. See tree*.go.
	tree *RouteTree

	env value.Value // env scoped của tenant này (đọc từ <path>/.env), lộ qua kitwork().env

	jitcssConfig *jitcss.Config // JIT-CSS config passed to the render engine

	// Declared by the root router during ensureFolder (same publish pattern as jitcssConfig):
	faviconFile string       // .favicon(): file served at /favicon.ico ("" = none declared)
	assetMounts []assetMount // .assets(): allowlisted static roots, each URL prefix → disk dir (empty = serve any safe file)
	themeMode   string       // .jittheme(): "" = auto-scan, "force" = always inject, "off" = never

	respCache    *cache.Store       // .cache(): RAM response cache
	persistStore *persist.Store     // .persist(): disk response cache (<tenant>/.persist)
	limiter      *ratelimit.Limiter // .limit()/.ratelimit(): rate limiter

	collectionMu    sync.Mutex
	collectionStore *collectionhelper.Store
	collectionErr   error
	collectionFTS   map[string]string // collection path → dir signature at last FTS sync (RAM, per process)

	cacheLock sync.RWMutex
	cache     map[string]*Responser

	databases map[string]*sql.DB
	dbMu      sync.Mutex

	// Rate Limiting fields
	limiters []*LimiterStore // index 0 = ScopeTenant, index 1 = ScopeServer

	crons       []*CronJob
	cronMu      sync.Mutex
	cronCancels []chan struct{}
	cronDB      *sql.DB             // underlying durable store handle (.data/scheduler.db, or shared PG)
	cronStore   cronStore           // dialect-abstracted coordination store (sqlite Phase 2 / pg Phase 3)
	cronByName  map[string]*CronJob // cron name → job, so a claimed DB slot finds its code to run
	cronNode    string              // lease owner override (multi-node demo); "" → process cronNodeID

	lruCache     map[string]*CacheItem
	lruCacheLock sync.RWMutex

	rateLimitRules []rateRule

	// Global App level configs
	meta              value.Value
	capabilitiesCache *capabilities.InstanceCache
}

func (t *Tenant) CapabilitiesCache() *capabilities.InstanceCache {
	if t == nil {
		return nil
	}
	t.dbMu.Lock()
	defer t.dbMu.Unlock()
	if t.capabilitiesCache == nil {
		t.capabilitiesCache = capabilities.NewInstanceCache()
	}
	return t.capabilitiesCache
}

type Cache struct {
	sync.RWMutex
	data map[string]*Responser
}

type Responser struct {
	Response *Response
	ExpireAt time.Time
}

func (t *Tenant) resolve(paths ...string) string {
	if t.config.base == "" {
		switch t.config.root {
		case "", "./", "../", "/", ".", "..":
			t.config.base = "."
		default:
			if t.entity.Identity != "" {
				t.config.base = filepath.Join(t.config.root, t.entity.Identity, t.entity.Domain)
			} else {
				// No identity (single-tenant). Resolve in priority order: the sites/ convention
				// (root/sites/<domain>), the test layout (root/test/<domain>), then a flat
				// root/<domain>. Default to flat when none has a root router yet (preserves the
				// pre-existing behaviour for brand-new tenants).
				flatPath := filepath.Join(t.config.root, t.entity.Domain)
				t.config.base = flatPath
				for _, cand := range []string{
					filepath.Join(t.config.root, SitesDirName, t.entity.Domain),
					filepath.Join(t.config.root, "test", t.entity.Domain),
				} {
					if _, err := os.Stat(filepath.Join(cand, RouterFileName)); err == nil {
						t.config.base = cand
						break
					}
				}
			}
		}
	}
	if len(paths) == 0 {
		return t.config.base
	}
	return filepath.Join(append([]string{t.config.base}, paths...)...)
}

// capabilities.Scope interface implementation:
func (t *Tenant) AppID() string                      { return t.appID() }
func (t *Tenant) Domain() string                     { return t.entity.Domain }
func (t *Tenant) ResolvePath(paths ...string) string { return t.resolve(paths...) }
func (t *Tenant) DB(name string) *sql.DB             { return sqliteFor(t, name).db() }

// resolveApp resolves a path at the IDENTITY (app) level — apps/<identity>/… — which every domain of
// the app shares. This is where app-wide infrastructure lives: `_cron` (one schedule set per app),
// `.data` (the app's scheduler DB), `_core` (shared services). Single-tenant (flat/sites) layouts have
// no identity layer, so it falls back to the domain level.
func (t *Tenant) resolveApp(paths ...string) string {
	if t.entity != nil && t.entity.Identity != "" && t.config.root != "" {
		return filepath.Join(append([]string{t.config.root, t.entity.Identity}, paths...)...)
	}
	return t.resolve(paths...)
}

// serveViewStatic auto-serves a plain .txt file that lives in the tenant's views/ folder with NO
// explicit route. Dropping `views/robots.txt` makes GET /robots.txt serve it as text/plain — "add
// a .txt to views and it just opens". This is a Zero-VM disk read (http.ServeFile handles
// Content-Type, Last-Modified, ETag and Range); an explicit route always wins because Serve only
// reaches here when nothing matched. Scoped to .txt so .kitwork.* sources are never exposed, and
// guarded against path traversal. Returns true if it served the request.
func (t *Tenant) serveViewStatic(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if !strings.HasSuffix(strings.ToLower(r.URL.Path), ".txt") {
		return false
	}

	// Clean the request path and refuse anything that still looks like traversal.
	clean := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/"))
	if strings.Contains(clean, "..") {
		return false
	}

	viewsDir := t.resolve("views")
	full := filepath.Join(viewsDir, filepath.FromSlash(clean))

	// Defense in depth: the resolved path must stay inside views/.
	rel, err := filepath.Rel(viewsDir, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}

	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		return false
	}

	http.ServeFile(w, r, full)
	return true
}

// RouterFile returns the path of the tenant's root router (the tenant marker) — what hot reload
// watches for changes/removal. With an argument it resolves that filename inside the tenant instead.
func (t *Tenant) RouterFile(filenames ...string) string {
	if len(filenames) > 0 {
		return t.resolve(filenames[0])
	}
	return t.resolve(RouterFileName)
}

func (t *Tenant) Run() error {
	// FILESYSTEM-ROUTED, always. There is no flat app.kitwork.js entry and no route table — the
	// folder tree IS the router. We only set up the shared VM + globals + env here; the tree
	// compiles each folder's router.kitwork.js LAZILY on the first request that reaches it.
	t.bytecode = &compiler.Bytecode{}
	t.vm = runtime.New(t.bytecode.Instructions, t.bytecode.Constants)
	t.vm.MaxEnergy = t.MaxEnergy
	t.vm.SourceMap = t.bytecode.SourceMap

	// env scoped THEO PATH của tenant: chỉ đọc <root>/<identity>/<domain>/.env →
	// tenant không bao giờ thấy env của host hay tenant khác. Lộ qua kitwork().env.
	t.env = NewEnv(ParseDotEnv(t.resolve(".env")))

	// Đăng ký kitwork vào Builtin Index 0 + Globals, trả về Struct KitWork.
	kitworkFunc := value.NewFunc(func(args ...value.Value) value.Value {
		return value.New(t.Kitwork(args...))
	})
	t.vm.Builtins = []value.Value{kitworkFunc}
	t.vm.Globals = make(map[string]value.Value)
	t.vm.Globals[kitwork] = kitworkFunc

	// Inject JS-compatible globals: Math, Date, JSON, console, fetch, parseInt, parseFloat...
	injectJSCompat(t.vm.Globals)

	// Response caching + rate limiting stores (used by .cache()/.persist()/.limit()).
	t.respCache = cache.NewStore(1000)
	t.persistStore = persist.New(t.resolve(".persist"))
	t.limiter = ratelimit.New()

	// Override the tenant-agnostic fetch builtin with one wired to THIS tenant's cache tiers, so
	// fetch(url, { cache: "5m", persist: "1d" }) stores per-tenant (same tiers as kitwork().http).
	t.vm.Globals["fetch"] = value.NewFunc(func(args ...value.Value) value.Value {
		return httphelper.FetchWith(httphelper.NewClient(t.fetchRAM(), t.fetchDisk()), args...)
	})

	// Build the (lazy) resolution tree — folders compile on first hit.
	t.tree = NewRouteTree(t)

	// Scheduler: the APP-TENANT (no domain, base = apps/<identity>) owns the identity's _cron scheduler
	// and boots it eagerly here. Domain-tenants serve HTTP ONLY — they must not each start a scheduler,
	// or one identity's _cron would run N dispatchers (one per domain). See NewAppTenant + StartAppSchedulers.
	if t.entity.Domain == "" {
		t.LoadCronFiles()
	}

	return nil
}

// NewAppTenant builds the APP-level runtime for one identity: a tenant with NO domain, whose base is
// apps/<identity>. It does not serve HTTP — it exists to run the app's _cron scheduler (from
// apps/<identity>/_cron) eagerly at server boot, independent of which domain gets traffic. Exactly one
// per identity per process, so an identity's crons run through a single dispatcher.
func NewAppTenant(root, identity string) *Tenant {
	return &Tenant{
		config:    &Config{root: root},
		entity:    &Entity{Identity: identity, Domain: ""},
		cache:     make(map[string]*Responser),
		databases: make(map[string]*sql.DB),
		lruCache:  make(map[string]*CacheItem),
	}
}

// DiscoverAppIdentities lists identity folders under root that hold a non-empty _cron/ — i.e. apps that
// have scheduled work. Convention folders (sites/, test/) are layouts, not identities, and are skipped.
func DiscoverAppIdentities(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var ids []string
	for _, e := range entries {
		if !e.IsDir() || e.Name() == SitesDirName || e.Name() == "test" {
			continue
		}
		cronDir := filepath.Join(root, e.Name(), "_cron")
		files, err := os.ReadDir(cronDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if !f.IsDir() && strings.HasSuffix(f.Name(), ".kitwork.js") {
				ids = append(ids, e.Name())
				break
			}
		}
	}
	return ids
}

func NewTenant(root string, domain string) *Tenant {
	var identity string
	if domain != "" {
		if dbIdentity, err := database.IdentitySystem(domain); err == nil && dbIdentity != "" {
			identity = dbIdentity
		}
		// THE FILESYSTEM IS THE SOURCE OF TRUTH for tenant layout — the system DB (when connected)
		// is just a faster index of it. Without this fallback, a missing DB row (or Postgres being
		// down) silently resolved the tenant to a flat tenants/<domain> that doesn't exist.
		if identity == "" {
			identity = findIdentity(root, domain)
		}
	}

	tenant := &Tenant{
		config: &Config{
			root: root,
		},
		entity: &Entity{
			Identity: identity,
			Domain:   domain,
		},
		cache:     make(map[string]*Responser),
		databases: make(map[string]*sql.DB),
		lruCache:  make(map[string]*CacheItem),
	}

	return tenant
}

// findIdentity locates the identity folder holding <root>/<identity>/<domain>/ by walking the
// tenants root. Top-level convention folders (sites/, test/) are layouts of their own, not
// identities, and are skipped. os.ReadDir returns sorted entries, so a domain that somehow exists
// under two identities resolves deterministically (first alphabetically). Returns "" when the
// domain lives flat under root (or not at all) — resolve() then falls through as before.
func findIdentity(root, domain string) string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == SitesDirName || entry.Name() == "test" {
			continue
		}
		if info, err := os.Stat(filepath.Join(root, entry.Name(), domain)); err == nil && info.IsDir() {
			return entry.Name()
		}
	}
	return ""
}

// SSEBroker returns the event broker for this tenant identity. The broker is shared across every
// *Tenant instance of the same identity (via sseBrokerRegistry), so live SSE connections survive
// hot-reload recompiles — a publish from a freshly-recompiled instance still reaches clients that
// connected through the previous instance. See engine/backbone.md (Phase 1, Invariant C).
func (t *Tenant) SSEBroker() *SSEBroker {
	return sseBrokerFor(t.brokerKey())
}

// brokerKey identifies a tenant for the shared broker registry. (Identity, Domain) is always
// unique per tenant: single-tenant (flat) layouts have an empty Identity but a distinct Domain;
// multi-tenant layouts carry both.
func (t *Tenant) brokerKey() string {
	if t.entity != nil && (t.entity.Identity != "" || t.entity.Domain != "") {
		return t.entity.Identity + "/" + t.entity.Domain
	}
	return "default"
}

// Close releases per-instance tenant resources (database connections). The SSE broker is NOT
// stopped here: it is shared across instances of this identity via sseBrokerRegistry and must
// outlive any single instance (e.g. an evicted/recompiled one) so open streams keep flowing.
func (t *Tenant) Close() {
	t.dbMu.Lock()
	defer t.dbMu.Unlock()
	for alias, db := range t.databases {
		db.Close()
		delete(t.databases, alias)
	}
	t.StopCronJobs()
}

// SetHostLimiters injects the shared host limiter store so this tenant's scope:"server" route
// rules count against the server-wide buckets (across all tenants). Called by core at boot.
func (t *Tenant) SetHostLimiters(s *LimiterStore) {
	if len(t.limiters) < ScopeMax {
		newLimiters := make([]*LimiterStore, ScopeMax)
		copy(newLimiters, t.limiters)
		t.limiters = newLimiters
	}
	t.limiters[ScopeServer] = s
}

// CompileDynamicRoute compiles a router script (e.g. router.kitwork.js) and executes it
// in a freshly-reset VM using the tenant's base globals and builtins, registering routes dynamically.
func (t *Tenant) CompileDynamicRoute(filePath string) error {
	bytecode, err := compiler.CompileFile(filePath)
	if err != nil {
		return err
	}

	vm := vmPool.Get().(*runtime.VM)
	defer vmPool.Put(vm)

	vm.Builtins = t.vm.Builtins
	vm.FastReset(bytecode.Instructions, bytecode.Constants, t.vm.Globals, bytecode.SourceMap)
	vm.MaxEnergy = t.MaxEnergy

	res := vm.Run()
	if res.K == value.Invalid {
		return fmt.Errorf("dynamic runtime error: %v", res.V)
	}
	return nil
}
