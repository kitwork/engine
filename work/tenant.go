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

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/capabilities"
	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/database"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/site"
	"github.com/kitwork/engine/utilities/cache"
	collectionhelper "github.com/kitwork/engine/utilities/collection"
	httphelper "github.com/kitwork/engine/utilities/http"
	"github.com/kitwork/engine/utilities/persist"
	"github.com/kitwork/engine/utilities/ratelimit"
	"github.com/kitwork/engine/utilities/safepath"
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
// conventions all key off it.
const RouterFileName = "router" + extension + ".js"

// AppScope retains only compatibility metadata used by Tenant adapters.
// Identity-wide resources live on app.Runtime.
type AppScope struct {
	config *Config
	entity *Entity
}

// SiteScope remains as a source-compatible embedding point while concrete
// site state moves to site.Runtime and site.Generation.
type SiteScope struct{}

// Tenant represents an active tenant, composing AppScope and SiteScope with runtime VM state.
type Tenant struct {
	AppScope
	SiteScope

	appRuntime  *app.Runtime
	siteRuntime *site.Runtime
	generation  *site.Generation
	ownsApp     bool

	bytecode      *compiler.Bytecode
	vm            *runtime.VM
	runtimeHealth *RuntimeHealth
	MaxEnergy     uint64
	appBoundary   *safepath.Boundary
	siteBoundary  *safepath.Boundary

	requestMu      sync.Mutex
	requestWG      sync.WaitGroup
	requestClosing bool
	closeOnce      sync.Once

	// App-only compatibility tenants have no site generation.
	capabilitiesMu    sync.Mutex
	capabilitiesCache *capabilities.InstanceCache

	// HotReload is retained for compatibility. Reload ownership now belongs to
	// core.Engine, which replaces the complete generation instead of mutating
	// this Tenant in place.
	HotReload bool

	// Compatibility aliases. A web tenant borrows these from its generation
	// and site runtime; an app-only tenant still owns local fallbacks.
	env          value.Value
	respCache    *cache.Store
	persistStore *persist.Store
	limiter      *ratelimit.Limiter

	collectionMu    sync.Mutex
	collectionStore *collectionhelper.Store
	collectionErr   error
	collectionFTS   map[string]string // collection path → dir signature at last FTS sync

	cacheLock sync.RWMutex
	cache     map[string]*Responser

	limiters []*LimiterStore // index 0 = ScopeTenant, index 1 = ScopeServer

	lruCache     map[string]*CacheItem
	lruCacheLock sync.RWMutex

	rateLimitRules []rateRule

	meta value.Value
}

// CapabilitiesCache owns LifetimeSite instances for this tenant generation.
func (t *Tenant) CapabilitiesCache() *capabilities.InstanceCache {
	if t == nil {
		return nil
	}
	if t.generation != nil {
		return t.generation.CapabilitiesCache()
	}
	t.capabilitiesMu.Lock()
	defer t.capabilitiesMu.Unlock()
	if t.capabilitiesCache == nil {
		t.capabilitiesCache = capabilities.NewInstanceCache()
	}
	return t.capabilitiesCache
}

// AppCapabilitiesCache delegates LifetimeApp ownership to the shared app runtime.
func (t *Tenant) AppCapabilitiesCache() *capabilities.InstanceCache {
	if t == nil {
		return nil
	}
	if t.appRuntime != nil {
		return t.appRuntime.CapabilitiesCache()
	}
	return t.CapabilitiesCache()
}

func (t *Tenant) AppRuntime() *app.Runtime {
	if t == nil {
		return nil
	}
	return t.appRuntime
}

func (t *Tenant) SiteRuntime() *site.Runtime {
	if t == nil {
		return nil
	}
	return t.siteRuntime
}

func (t *Tenant) SiteGeneration() *site.Generation {
	if t == nil {
		return nil
	}
	return t.generation
}

// SourcesChanged compares the active generation's executable source manifest
// with disk. Templates are deliberately excluded because render reads them
// directly for each request.
func (t *Tenant) SourcesChanged() (bool, error) {
	if t == nil || t.generation == nil {
		return false, nil
	}
	return t.generation.Sources().Changed()
}

func (t *Tenant) presentation() *site.Presentation {
	if t == nil || t.generation == nil {
		return nil
	}
	return t.generation.Presentation()
}

func (t *Tenant) routeTree() *RouteTree {
	if t == nil || t.generation == nil {
		return nil
	}
	tree, _ := t.generation.RouteGraph().(*RouteTree)
	return tree
}

func (t *Tenant) renderPlan() *RenderPlan {
	if t == nil || t.generation == nil {
		return nil
	}
	plan, _ := t.generation.RenderPlan().(*RenderPlan)
	return plan
}

// ActivateGeneration publishes this tenant's prepared site revision. It does
// not retire the previous generation; the previous Tenant owns that drain.
func (t *Tenant) ActivateGeneration() error {
	if t == nil || t.siteRuntime == nil || t.generation == nil {
		return nil
	}
	_, err := t.siteRuntime.ActivateGeneration(t.generation)
	return err
}

func (t *Tenant) generationLease() (*site.Lease, error) {
	if t == nil || t.generation == nil {
		return nil, nil
	}
	current := t.siteRuntime.CurrentGeneration()
	if current == nil {
		if err := t.ActivateGeneration(); err != nil {
			return nil, err
		}
	} else if current != t.generation {
		return nil, fmt.Errorf(
			"site generation %d is no longer current",
			t.generation.Version(),
		)
	}
	lease, ok := t.generation.Acquire()
	if !ok {
		return nil, fmt.Errorf("site generation %d is retired", t.generation.Version())
	}
	return lease, nil
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
func (t *Tenant) RAMStore() httphelper.ResponseStore { return t.fetchRAM() }

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

	// Defense in depth: canonical resolution must remain in this site. This
	// rejects symlinks that point into a sibling site or outside the app.
	if !t.insideSiteRoot(full) {
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
	// folder tree IS the router. Build the finite route declaration tree before activation so
	// requests never publish a partially prepared generation.
	t.bytecode = &compiler.Bytecode{Program: runtime.EmptyProgram()}
	t.vm = runtime.New(t.bytecode.Program)
	t.vm.MaxEnergy = t.MaxEnergy
	if err := t.preparePathBoundaries(); err != nil {
		return fmt.Errorf("prepare filesystem boundaries: %w", err)
	}

	envFile := t.resolve(".env")
	environment := NewEnv(ParseDotEnv(envFile))
	if t.generation != nil {
		if err := t.generation.SetEnvironment(environment); err != nil {
			return err
		}
		if err := t.generation.Sources().WatchFile(envFile); err != nil {
			return err
		}
		t.env = t.generation.Environment()
	} else {
		t.env = environment
	}

	kitworkFunc := value.NewFunc(func(args ...value.Value) value.Value {
		return value.New(&KitWork{tenant: t, vm: t.vm})
	})
	t.vm.Builtins = []value.Value{kitworkFunc}
	t.vm.Globals = make(map[string]value.Value)
	t.vm.Globals[kitwork] = kitworkFunc

	injectJSCompat(t.vm.Globals)

	if t.generation != nil && t.siteRuntime != nil {
		if err := t.siteRuntime.ConfigureResources(t.resolve()); err != nil {
			return err
		}
		t.respCache = t.generation.ResponseCache()
		t.persistStore = t.siteRuntime.PersistStore()
		t.limiter = t.siteRuntime.Limiter()
	} else {
		t.respCache = cache.NewStore(1000)
		t.persistStore = persist.New(t.resolve(".persist"))
		t.limiter = ratelimit.New()
	}

	t.vm.Globals["fetch"] = value.NewFunc(func(args ...value.Value) value.Value {
		return httphelper.FetchWith(httphelper.NewClient(t.fetchRAM(), t.fetchDisk()), args...)
	})

	if t.entity.Domain != "" {
		tree := NewRouteTree(t)
		if err := tree.Prepare(); err != nil {
			return err
		}
		if err := t.generation.SetRouteGraph(tree); err != nil {
			tree.Close()
			return err
		}
		plan, err := newRenderPlan(t, tree)
		if err != nil {
			return err
		}
		if err := t.generation.SetRenderPlan(plan); err != nil {
			plan.Close()
			return err
		}
	}

	// App-level background work, loaded once per identity (Domain == "" is the app tenant, not one of
	// its domains). Both must register before any request arrives: they run on their own clock, so
	// the lazy compile a route can afford would be too late.
	if t.entity.Domain == "" {
		t.LoadCronFiles()
		t.LoadQueueFiles()
	}

	return nil
}

func NewAppTenant(root, identity string) *Tenant {
	appRuntime := app.NewRuntime(identity)
	tenant := NewAppTenantWithRuntime(root, identity, appRuntime)
	tenant.ownsApp = true
	return tenant
}

func NewAppTenantWithRuntime(root, identity string, appRuntime *app.Runtime) *Tenant {
	return &Tenant{
		AppScope: AppScope{
			config: &Config{root: root},
			entity: &Entity{Identity: identity, Domain: ""},
		},
		SiteScope:  SiteScope{},
		appRuntime: appRuntime,
		cache:      make(map[string]*Responser),
		lruCache:   make(map[string]*CacheItem),
	}
}

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

// DiscoverLegacySites reports domain folders that still have the removed flat
// app.kitwork.js entry but no filesystem router marker. The host never executes
// these files; surfacing them at boot prevents a silent migration failure.
func DiscoverLegacySites(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}

	var legacy []string
	check := func(dir, label string) {
		if _, err := os.Stat(filepath.Join(dir, RouterFileName)); err == nil {
			return
		}
		if info, err := os.Stat(filepath.Join(dir, "app.kitwork.js")); err == nil && !info.IsDir() {
			legacy = append(legacy, label)
		}
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		first := filepath.Join(root, entry.Name())
		check(first, entry.Name())

		children, readErr := os.ReadDir(first)
		if readErr != nil {
			continue
		}
		for _, child := range children {
			if child.IsDir() {
				check(filepath.Join(first, child.Name()), child.Name())
			}
		}
	}
	return legacy
}

func NewTenant(root string, domain string) *Tenant {
	identity := ResolveIdentity(root, domain)
	appRuntime := app.NewRuntime(identity)
	siteRuntime, _ := appRuntime.Site(root, domain)
	generation, _ := siteRuntime.PrepareGeneration()
	tenant := NewTenantWithRuntime(root, domain, appRuntime, siteRuntime, generation)
	tenant.ownsApp = true
	return tenant
}

func NewTenantWithRuntime(
	root,
	domain string,
	appRuntime *app.Runtime,
	siteRuntime *site.Runtime,
	generations ...*site.Generation,
) *Tenant {
	identity := ""
	if appRuntime != nil {
		identity = appRuntime.ID()
	}
	var generation *site.Generation
	if len(generations) > 0 {
		generation = generations[0]
	} else if siteRuntime != nil {
		generation, _ = siteRuntime.PrepareGeneration()
	}
	return &Tenant{
		AppScope: AppScope{
			config: &Config{root: root},
			entity: &Entity{Identity: identity, Domain: domain},
		},
		SiteScope:   SiteScope{},
		appRuntime:  appRuntime,
		siteRuntime: siteRuntime,
		generation:  generation,
		cache:       make(map[string]*Responser),
		lruCache:    make(map[string]*CacheItem),
	}
}

func ResolveIdentity(root, domain string) string {
	if domain == "" {
		return ""
	}
	if dbIdentity, err := database.IdentitySystem(domain); err == nil && dbIdentity != "" {
		return dbIdentity
	}
	return findIdentity(root, domain)
}

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

func (t *Tenant) SSEBroker() *SSEBroker {
	if t != nil && t.siteRuntime != nil {
		return t.siteRuntime.SSEBroker()
	}
	return sseBrokerFor(t.brokerKey())
}

func (t *Tenant) brokerKey() string {
	if t != nil && t.entity != nil && (t.entity.Identity != "" || t.entity.Domain != "") {
		return t.entity.Identity + "/" + t.entity.Domain
	}
	return "default"
}

func (t *Tenant) Close() {
	if t == nil {
		return
	}
	t.closeOnce.Do(func() {
		t.requestMu.Lock()
		t.requestClosing = true
		t.requestMu.Unlock()
		ownsCurrentSite := t.generation == nil ||
			t.siteRuntime == nil ||
			t.siteRuntime.CurrentGeneration() == t.generation
		if ownsCurrentSite && t.siteRuntime != nil {
			t.siteRuntime.StopStreams()
		} else if ownsCurrentSite {
			releaseSSEBroker(t.brokerKey())
		}
		t.requestWG.Wait()
		if t.generation != nil {
			t.generation.Retire()
		} else if t.respCache != nil {
			t.respCache.Close()
		}

		t.capabilitiesMu.Lock()
		if t.capabilitiesCache != nil {
			t.capabilitiesCache.Close()
		}
		t.capabilitiesMu.Unlock()

		if t.ownsApp && t.appRuntime != nil {
			t.appRuntime.Close()
		}
	})
}

func (t *Tenant) beginRequest() bool {
	t.requestMu.Lock()
	defer t.requestMu.Unlock()
	if t.requestClosing {
		return false
	}
	t.requestWG.Add(1)
	return true
}

func (t *Tenant) endRequest() {
	t.requestWG.Done()
}

func (t *Tenant) SetHostLimiters(s *LimiterStore) {
	if len(t.limiters) < ScopeMax {
		newLimiters := make([]*LimiterStore, ScopeMax)
		copy(newLimiters, t.limiters)
		t.limiters = newLimiters
	}
	t.limiters[ScopeServer] = s
}

func (t *Tenant) SetRuntimeHealth(health *RuntimeHealth) {
	if t != nil {
		t.runtimeHealth = health
	}
}

func (t *Tenant) recordVMExecution(
	program *runtime.Program,
	vm *runtime.VM,
	result value.Value,
	elapsed ...time.Duration,
) {
	if t != nil && t.runtimeHealth != nil && vm != nil {
		t.runtimeHealth.Record(program, vm.Stats(), result)
		if len(elapsed) > 0 {
			t.runtimeHealth.RecordVMLatency(elapsed[0])
		}
	}
}

func (t *Tenant) compileFile(paths ...string) (*compiler.Bytecode, error) {
	if t != nil && t.generation != nil {
		if bytecodeCache := t.generation.BytecodeCache(); bytecodeCache != nil {
			return bytecodeCache.CompileFile(paths...)
		}
	}
	return compiler.CompileFile(paths...)
}

func (t *Tenant) CompileDynamicRoute(filePath string) error {
	bytecode, err := t.compileFile(filePath)
	if err != nil {
		return err
	}

	vm := enginePool.Acquire()
	defer enginePool.Release(vm)

	t.prepareExecutionVM(vm, t.vm.Globals, t.vm.Builtins)
	vm.FastResetPrepared(bytecode.Program)
	vm.MaxEnergy = t.MaxEnergy

	started := time.Now()
	res := vm.Run()
	t.recordVMExecution(bytecode.Program, vm, res, time.Since(started))
	if res.K == value.Invalid {
		return fmt.Errorf("dynamic runtime error: %v", res.V)
	}
	return nil
}
