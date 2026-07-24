package work

import (
	"context"
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
// conventions all key off it.
const RouterFileName = "router" + extension + ".js"

// AppScope encapsulates app-level state shared across all domains/sites of an application identity:
// Identity config, DB connections, Capabilities Cache, Background Tasks, and Cron Scheduler.
type AppScope struct {
	config            *Config
	entity            *Entity
	databases         map[string]*sql.DB
	dbMu              sync.Mutex
	crons             []*CronJob
	cronMu            sync.Mutex
	cronCancels       []chan struct{}
	cronDB            *sql.DB             // underlying durable store handle (.data/scheduler.db, or shared PG)
	cronStore         cronStore           // dialect-abstracted coordination store
	cronByName        map[string]*CronJob // cron name → job, so a claimed DB slot finds its code to run
	cronNode          string              // lease owner override
	capabilitiesCache *capabilities.InstanceCache

	backgroundMu      sync.Mutex
	backgroundCtx     context.Context
	backgroundCancel  context.CancelFunc
	backgroundWG      sync.WaitGroup
	backgroundClosing bool
}

// SiteScope encapsulates domain/site-level web presentation state:
// Dynamic RouteTree, JIT CSS config, asset mounts, favicon, and theme mode.
type SiteScope struct {
	tree         *RouteTree
	jitcssConfig *jitcss.Config
	faviconFile  string       // .favicon(): file served at /favicon.ico ("" = none declared)
	assetMounts  []assetMount // .assets(): allowlisted static roots, each URL prefix → disk dir
	themeMode    string       // .jittheme(): "" = auto-scan, "force" = always inject, "off" = never
}

// Tenant represents an active tenant, composing AppScope and SiteScope with runtime VM state.
type Tenant struct {
	AppScope
	SiteScope

	bytecode  *compiler.Bytecode
	vm        *runtime.VM
	MaxEnergy uint64
	HotReload bool

	env value.Value // env scoped của tenant này (đọc từ <path>/.env), lộ qua kitwork().env

	respCache    *cache.Store       // .cache(): RAM response cache
	persistStore *persist.Store     // .persist(): disk response cache (<tenant>/.persist)
	limiter      *ratelimit.Limiter // .limit()/.ratelimit(): rate limiter

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
// the app shares.
func (t *Tenant) resolveApp(paths ...string) string {
	if t.entity != nil && t.entity.Identity != "" && t.config.root != "" {
		return filepath.Join(append([]string{t.config.root, t.entity.Identity}, paths...)...)
	}
	return t.resolve(paths...)
}

func (t *Tenant) serveViewStatic(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if !strings.HasSuffix(strings.ToLower(r.URL.Path), ".txt") {
		return false
	}

	clean := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/"))
	if strings.Contains(clean, "..") {
		return false
	}

	viewsDir := t.resolve("views")
	full := filepath.Join(viewsDir, filepath.FromSlash(clean))

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

func (t *Tenant) RouterFile(filenames ...string) string {
	if len(filenames) > 0 {
		return t.resolve(filenames[0])
	}
	return t.resolve(RouterFileName)
}

func (t *Tenant) Run() error {
	t.bytecode = &compiler.Bytecode{}
	t.vm = runtime.New(t.bytecode.Instructions, t.bytecode.Constants)
	t.vm.MaxEnergy = t.MaxEnergy
	t.vm.SourceMap = t.bytecode.SourceMap

	t.env = NewEnv(ParseDotEnv(t.resolve(".env")))

	kitworkFunc := value.NewFunc(func(args ...value.Value) value.Value {
		return value.New(t.Kitwork(args...))
	})
	t.vm.Builtins = []value.Value{kitworkFunc}
	t.vm.Globals = make(map[string]value.Value)
	t.vm.Globals[kitwork] = kitworkFunc

	injectJSCompat(t.vm.Globals)

	t.respCache = cache.NewStore(1000)
	t.persistStore = persist.New(t.resolve(".persist"))
	t.limiter = ratelimit.New()

	t.vm.Globals["fetch"] = value.NewFunc(func(args ...value.Value) value.Value {
		return httphelper.FetchWith(httphelper.NewClient(t.fetchRAM(), t.fetchDisk()), args...)
	})

	t.tree = NewRouteTree(t)

	if t.entity.Domain == "" {
		t.LoadCronFiles()
	}

	return nil
}

func NewAppTenant(root, identity string) *Tenant {
	return &Tenant{
		AppScope: AppScope{
			config:    &Config{root: root},
			entity:    &Entity{Identity: identity, Domain: ""},
			databases: make(map[string]*sql.DB),
		},
		SiteScope: SiteScope{},
		cache:     make(map[string]*Responser),
		lruCache:  make(map[string]*CacheItem),
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

func NewTenant(root string, domain string) *Tenant {
	var identity string
	if domain != "" {
		if dbIdentity, err := database.IdentitySystem(domain); err == nil && dbIdentity != "" {
			identity = dbIdentity
		}
		if identity == "" {
			identity = findIdentity(root, domain)
		}
	}

	tenant := &Tenant{
		AppScope: AppScope{
			config:    &Config{root: root},
			entity:    &Entity{Identity: identity, Domain: domain},
			databases: make(map[string]*sql.DB),
		},
		SiteScope: SiteScope{},
		cache:     make(map[string]*Responser),
		lruCache:  make(map[string]*CacheItem),
	}

	return tenant
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
	return sseBrokerFor(t.brokerKey())
}

func (t *Tenant) brokerKey() string {
	if t.entity != nil && (t.entity.Identity != "" || t.entity.Domain != "") {
		return t.entity.Identity + "/" + t.entity.Domain
	}
	return "default"
}

func (t *Tenant) Close() {
	t.stopBackgroundTasks()
	t.StopCronJobs()

	if t.capabilitiesCache != nil {
		t.capabilitiesCache.Close()
	}

	t.dbMu.Lock()
	defer t.dbMu.Unlock()
	for alias, db := range t.databases {
		db.Close()
		delete(t.databases, alias)
	}
}

func (t *Tenant) SetHostLimiters(s *LimiterStore) {
	if len(t.limiters) < ScopeMax {
		newLimiters := make([]*LimiterStore, ScopeMax)
		copy(newLimiters, t.limiters)
		t.limiters = newLimiters
	}
	t.limiters[ScopeServer] = s
}

func (t *Tenant) CompileDynamicRoute(filePath string) error {
	bytecode, err := compiler.CompileFile(filePath)
	if err != nil {
		return err
	}

	vm := enginePool.Acquire()
	defer enginePool.Release(vm)

	vm.Builtins = t.vm.Builtins
	vm.FastReset(bytecode.Instructions, bytecode.Constants, t.vm.Globals, bytecode.SourceMap)
	vm.MaxEnergy = t.MaxEnergy

	res := vm.Run()
	if res.K == value.Invalid {
		return fmt.Errorf("dynamic runtime error: %v", res.V)
	}
	return nil
}
