package core

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kitwork/engine/app"
	collectioncap "github.com/kitwork/engine/capabilities/collection"
	"github.com/kitwork/engine/compiler"
	dom "github.com/kitwork/engine/domain"
	kitdbnode "github.com/kitwork/engine/kitdb/node"
	requestscope "github.com/kitwork/engine/request"
	kitruntime "github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/search"
	"github.com/kitwork/engine/site"
	"github.com/kitwork/engine/work"
)

// Authorizer resolves trusted identity after the site is known and before its
// request scope is created. Returning an error rejects the request with 401.
type Authorizer func(
	request *http.Request,
	appID string,
	domain string,
) (requestscope.Authorization, error)

type cachedTenant struct {
	tenant      *work.Tenant
	lastAccess  time.Time
	lastChecked time.Time
	mu          sync.Mutex
	reloadMu    sync.Mutex
}

func (c *cachedTenant) touch() {
	c.mu.Lock()
	c.lastAccess = time.Now()
	c.mu.Unlock()
}

func (c *cachedTenant) current() *work.Tenant {
	c.mu.Lock()
	tenant := c.tenant
	c.mu.Unlock()
	return tenant
}

func (c *cachedTenant) isExpired(now time.Time, timeout time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return now.Sub(c.lastAccess) > timeout
}

type Engine struct {
	startedAt        time.Time
	root             string
	rootLayout       work.RootLayout
	maxEnergy        uint64
	hotReload        bool
	Hostname         string
	cache            map[string]*cachedTenant
	localHosts       map[string]string
	appRuntimes      map[string]*app.Runtime
	appTenants       map[string]*work.Tenant // identity → app runtime that owns that identity's _cron scheduler
	appStarting      map[string]struct{}
	idleTimeout      time.Duration // bao lâu idle thì evict khỏi cache; 0 = không bao giờ evict
	rateLimiter      *RateLimiter  // host-level limits (nil = off); set qua SetRateLimit trước khi serve
	authorizer       Authorizer
	bytecodeCacheMu  sync.RWMutex
	bytecodeCacheDir string
	runtimeHealth    *work.RuntimeHealth
	searchManager    *search.Manager
	searchManagerErr error
	kitDBNodeManager *kitdbnode.Manager
	kitDBNodeErr     error
	collectionCanary atomic.Bool
	collectionStats  *collectioncap.SegmentSearchCanaryTelemetry
	mu               sync.RWMutex
	stopCleanup      chan struct{}
	closeOnce        sync.Once
	closed           bool
}

func New(root string, maxEnergy uint64, hotReload bool, hostname string) *Engine {
	if maxEnergy == 0 {
		maxEnergy = kitruntime.Limits().DefaultMaxEnergy
	}
	searchManager, searchManagerErr := search.NewManager(
		filepath.Join(root, ".data", "search"),
		search.ManagerOptions{
			ReplacementTimeout: 24 * time.Hour,
			Writer:             search.WriterOptions{Segment: search.BuildOptions{SkipLongTerms: true}},
		},
	)
	if searchManagerErr != nil {
		slog.Warn("Search manager failed to initialize", "error", searchManagerErr)
	}
	kitDBNodeManager, kitDBNodeErr := kitdbnode.NewManager(kitdbnode.Limits{})
	if kitDBNodeErr != nil {
		slog.Warn("KitDB node manager failed to initialize", "error", kitDBNodeErr)
	}
	e := &Engine{
		startedAt:        time.Now(),
		root:             root,
		maxEnergy:        maxEnergy,
		hotReload:        hotReload,
		Hostname:         hostname,
		cache:            make(map[string]*cachedTenant),
		localHosts:       make(map[string]string),
		appRuntimes:      make(map[string]*app.Runtime),
		appTenants:       make(map[string]*work.Tenant),
		appStarting:      make(map[string]struct{}),
		runtimeHealth:    work.NewRuntimeHealth(),
		searchManager:    searchManager,
		searchManagerErr: searchManagerErr,
		kitDBNodeManager: kitDBNodeManager,
		kitDBNodeErr:     kitDBNodeErr,
		collectionStats:  collectioncap.NewSegmentSearchCanaryTelemetry(),
		idleTimeout:      10 * time.Minute, // mặc định; chỉnh bằng SetIdleTimeout (0 = không evict)
		stopCleanup:      make(chan struct{}),
	}
	// Vòng dọn cache chạy nền mỗi 1 phút; timeout đọc động từ e.idleTimeout.
	go e.cleanupLoop(1 * time.Minute)
	return e
}

func appRuntimeKey(identity, domain string) string {
	if identity != "" {
		return "app:" + identity
	}
	return "site:" + domain
}

func (e *Engine) appRuntimeKey(identity, domain string) string {
	if e.rootLayout.IsSingleApp() {
		return "root-app"
	}
	return appRuntimeKey(identity, domain)
}

// appRuntimeLocked returns the host-owned application runtime. The caller must
// hold e.mu for writing so runtime creation remains a singleton per key.
func (e *Engine) appRuntimeLocked(identity, domain string) *app.Runtime {
	key := e.appRuntimeKey(identity, domain)
	if current := e.appRuntimes[key]; current != nil {
		return current
	}
	current := app.NewRuntime(identity)
	e.appRuntimes[key] = current
	return current
}

// StartAppSchedulers eagerly boots one scheduler owner for each app that has
// _cron/ or _queue/ sources. A root app loads those sources from app/ once;
// multi-tenant mode loads them once per apps/<identity>. Routing remains lazy,
// but background clocks cannot wait for a domain request. Call after the system
// DB is wired so a configured shared backend is selected.
func (e *Engine) StartAppSchedulers() (started int) {
	identities := work.DiscoverAppIdentities(e.root)
	switch e.rootLayout {
	case work.RootLayoutSingle:
		if !work.HasAppBackgroundSources(e.root, "") {
			return 0
		}
		identities = []string{work.RootAppIdentity}
	case work.RootLayoutMultiDomain:
		if !work.HasAppBackgroundSources(e.root, "") {
			return 0
		}
		identities = []string{work.RootAppIdentity}
	}
	for _, identity := range identities {
		e.mu.Lock()
		if e.closed {
			e.mu.Unlock()
			return started
		}
		_, exists := e.appTenants[identity]
		_, starting := e.appStarting[identity]
		if exists || starting {
			e.mu.Unlock()
			continue
		}
		e.appStarting[identity] = struct{}{}
		appRuntime := e.appRuntimeLocked(identity, "")
		e.mu.Unlock()

		appTenant := work.NewAppTenantWithRuntimeLayout(
			e.root,
			identity,
			e.rootLayout,
			appRuntime,
		)
		appTenant.SetSearchManager(e.searchManager, e.searchManagerErr)
		appTenant.SetKitDBNodeManager(e.kitDBNodeManager, e.kitDBNodeErr)
		appTenant.SetCollectionSearchCanary(e.collectionCanary.Load())
		appTenant.SetCollectionSearchCanaryTelemetry(e.collectionStats)
		appTenant.SetRuntimeHealth(e.runtimeHealth)
		appTenant.MaxEnergy = e.maxEnergy
		appTenant.HotReload = e.hotReload
		if err := appTenant.Run(); err != nil {
			appTenant.Close()
			e.mu.Lock()
			delete(e.appStarting, identity)
			e.mu.Unlock()
			slog.Warn("App scheduler failed to start", "identity", identity, "error", err)
			continue
		}
		e.mu.Lock()
		delete(e.appStarting, identity)
		if e.closed {
			e.mu.Unlock()
			appTenant.Close()
			return started
		}
		if _, exists := e.appTenants[identity]; exists {
			e.mu.Unlock()
			appTenant.Close()
			continue
		}
		e.appTenants[identity] = appTenant
		e.mu.Unlock()
		started++
	}
	if started > 0 {
		slog.Info("App schedulers started", "count", started)
	}
	return started
}

// SetRateLimit bật rate limit tầng host (global/IP/browser/user — xem RateLimiter). Gọi MỘT lần
// lúc boot, trước khi phục vụ request; nil = tắt.
func (e *Engine) SetRateLimit(rl *RateLimiter) {
	e.rateLimiter = rl
}

// SetRootLayout freezes the root-to-site mapping selected during host boot.
// Direct embedders that do not call it retain the historical Auto resolver.
func (e *Engine) SetRootLayout(layout work.RootLayout) error {
	if !layout.Valid() {
		return fmt.Errorf("invalid root layout %d", layout)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || len(e.cache) != 0 || len(e.appTenants) != 0 || len(e.appRuntimes) != 0 {
		return fmt.Errorf("root layout must be set before loading apps or sites")
	}
	e.rootLayout = layout
	return nil
}

func (e *Engine) SetAuthorizer(authorizer Authorizer) {
	e.mu.Lock()
	e.authorizer = authorizer
	e.mu.Unlock()
}

// SetBytecodeCache enables generation-scoped bytecode artifacts. An empty
// directory disables the cache. Call it during host boot before serving.
func (e *Engine) SetBytecodeCache(directory string) {
	e.bytecodeCacheMu.Lock()
	e.bytecodeCacheDir = directory
	e.bytecodeCacheMu.Unlock()
}

// SetSearchManagerOptions replaces the default bounded search owner during
// host boot. It must run before schedulers, prewarm, or request-driven tenant
// loading so no tenant can retain the previous owner.
func (e *Engine) SetSearchManagerOptions(options search.ManagerOptions) error {
	if e == nil {
		return fmt.Errorf("engine is nil")
	}
	replacement, err := search.NewManager(filepath.Join(e.root, ".data", "search"), options)
	if err != nil {
		return err
	}
	e.mu.Lock()
	if e.closed || len(e.cache) != 0 || len(e.appTenants) != 0 || len(e.appRuntimes) != 0 {
		e.mu.Unlock()
		_ = replacement.Close()
		return fmt.Errorf("search manager options must be set before loading apps or sites")
	}
	previous := e.searchManager
	e.searchManager = replacement
	e.searchManagerErr = nil
	e.mu.Unlock()
	if previous != nil {
		return previous.Close()
	}
	return nil
}

// SetKitDBNodeLimits replaces the default host-wide KitDB fleet owner during
// boot. Configuration freezes once an app or site starts loading.
func (e *Engine) SetKitDBNodeLimits(limits kitdbnode.Limits) error {
	if e == nil {
		return fmt.Errorf("engine is nil")
	}
	replacement, err := kitdbnode.NewManager(limits)
	if err != nil {
		return err
	}
	e.mu.Lock()
	if e.closed || len(e.cache) != 0 || len(e.appTenants) != 0 ||
		len(e.appRuntimes) != 0 || len(e.appStarting) != 0 {
		e.mu.Unlock()
		_ = replacement.Close()
		return fmt.Errorf("KitDB node limits must be set before loading apps or sites")
	}
	previous := e.kitDBNodeManager
	e.kitDBNodeManager = replacement
	e.kitDBNodeErr = nil
	e.mu.Unlock()
	if previous != nil {
		return previous.Close()
	}
	return nil
}

// SetCollectionSearchCanary enables bounded shadow comparison during host
// boot. The legacy collection engine remains the serving path. Configuration
// is frozen once an app or site starts loading.
func (e *Engine) SetCollectionSearchCanary(enabled bool) error {
	if e == nil {
		return fmt.Errorf("engine is nil")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || len(e.cache) != 0 || len(e.appTenants) != 0 ||
		len(e.appRuntimes) != 0 || len(e.appStarting) != 0 {
		return fmt.Errorf("collection search canary must be set before loading apps or sites")
	}
	e.collectionCanary.Store(enabled)
	return nil
}

// CollectionSearchCanaryStats returns fixed-cardinality host counters that
// survive tenant generation replacement.
func (e *Engine) CollectionSearchCanaryStats() collectioncap.SegmentSearchCanaryStats {
	if e == nil || e.collectionStats == nil {
		return collectioncap.SegmentSearchCanaryStats{}
	}
	stats := e.collectionStats.Snapshot()
	stats.Enabled = e.collectionCanary.Load()
	return stats
}

// SearchStats returns bounded process-local manager counters without tenant or
// query labels and without opening an index.
func (e *Engine) SearchStats() search.ManagerStats {
	if e == nil {
		return search.ManagerStats{}
	}
	e.mu.RLock()
	manager := e.searchManager
	e.mu.RUnlock()
	return manager.Stats()
}

// KitDBNodeStats returns bounded host-wide handle and page-cache ownership
// counters without opening a database or inspecting tenant data.
func (e *Engine) KitDBNodeStats() kitdbnode.Stats {
	if e == nil {
		return kitdbnode.Stats{}
	}
	e.mu.RLock()
	manager := e.kitDBNodeManager
	e.mu.RUnlock()
	return manager.Stats()
}

// ScheduleKitDBBackup submits one host-trusted verified backup to the shared
// KitDB node governor. Tenant code cannot call this filesystem-level API.
func (e *Engine) ScheduleKitDBBackup(
	ctx context.Context,
	request kitdbnode.BackupRequest,
) (*kitdbnode.MaintenanceTicket, error) {
	if e == nil {
		return nil, fmt.Errorf("engine is nil")
	}
	e.mu.RLock()
	manager := e.kitDBNodeManager
	e.mu.RUnlock()
	if manager == nil {
		return nil, fmt.Errorf("KitDB node manager is unavailable")
	}
	return manager.ScheduleBackup(ctx, request)
}

// RegisterKitDBProductionAnchorPublisher installs one host-trusted off-node or
// transfer-spool adapter. Tenant code cannot receive this authority.
func (e *Engine) RegisterKitDBProductionAnchorPublisher(
	name string,
	publisher kitdbnode.ProductionAnchorPublisher,
) error {
	manager, err := e.kitDBProductionManager()
	if err != nil {
		return err
	}
	return manager.RegisterProductionAnchorPublisher(name, publisher)
}

// UnregisterKitDBProductionAnchorPublisher releases one unused host adapter.
// Published anchors remain untouched.
func (e *Engine) UnregisterKitDBProductionAnchorPublisher(name string) error {
	manager, err := e.kitDBProductionManager()
	if err != nil {
		return err
	}
	return manager.UnregisterProductionAnchorPublisher(name)
}

// KitDBProductionAnchorPublishers returns registered path-free labels.
func (e *Engine) KitDBProductionAnchorPublishers() []string {
	manager, err := e.kitDBProductionManager()
	if err != nil {
		return nil
	}
	return manager.ProductionAnchorPublishers()
}

// RegisterKitDBProductionPolicy registers one host-trusted recurring backup,
// publication, and restore policy on the shared node governor. Tenant code
// cannot supply source, backup, or publisher authority.
func (e *Engine) RegisterKitDBProductionPolicy(
	config kitdbnode.ProductionPolicyConfig,
) (kitdbnode.ProductionPolicyHealth, error) {
	manager, err := e.kitDBProductionManager()
	if err != nil {
		return kitdbnode.ProductionPolicyHealth{}, err
	}
	return manager.RegisterProductionPolicy(config)
}

// RunKitDBProductionPolicyOnce schedules or joins one host-trusted protection
// cycle. Canceling ctx stops only this waiter.
func (e *Engine) RunKitDBProductionPolicyOnce(
	ctx context.Context,
	name string,
) (kitdbnode.ProductionPolicyCycle, error) {
	manager, err := e.kitDBProductionManager()
	if err != nil {
		return kitdbnode.ProductionPolicyCycle{}, err
	}
	return manager.RunProductionPolicyOnce(ctx, name)
}

// PauseKitDBProductionPolicy stops future automatic cycles without
// interrupting a running durable stage.
func (e *Engine) PauseKitDBProductionPolicy(
	name string,
) (kitdbnode.ProductionPolicyHealth, error) {
	manager, err := e.kitDBProductionManager()
	if err != nil {
		return kitdbnode.ProductionPolicyHealth{}, err
	}
	return manager.PauseProductionPolicy(name)
}

// ResumeKitDBProductionPolicy enables automatic reconciliation immediately.
func (e *Engine) ResumeKitDBProductionPolicy(
	name string,
) (kitdbnode.ProductionPolicyHealth, error) {
	manager, err := e.kitDBProductionManager()
	if err != nil {
		return kitdbnode.ProductionPolicyHealth{}, err
	}
	return manager.ResumeProductionPolicy(name)
}

// WakeKitDBProductionPolicy makes one unpaused host policy eligible now.
func (e *Engine) WakeKitDBProductionPolicy(name string) error {
	manager, err := e.kitDBProductionManager()
	if err != nil {
		return err
	}
	return manager.WakeProductionPolicy(name)
}

// UnregisterKitDBProductionPolicy releases one idle process-local policy.
// Verified anchors and the durable source pin remain intact.
func (e *Engine) UnregisterKitDBProductionPolicy(name string) error {
	manager, err := e.kitDBProductionManager()
	if err != nil {
		return err
	}
	return manager.UnregisterProductionPolicy(name)
}

// KitDBProductionPolicyHealth returns one named path-free health snapshot.
func (e *Engine) KitDBProductionPolicyHealth(
	name string,
) (kitdbnode.ProductionPolicyHealth, error) {
	manager, err := e.kitDBProductionManager()
	if err != nil {
		return kitdbnode.ProductionPolicyHealth{}, err
	}
	return manager.ProductionPolicyHealth(name)
}

// KitDBProductionPolicies returns path-free health for registered host policies.
func (e *Engine) KitDBProductionPolicies() []kitdbnode.ProductionPolicyHealth {
	manager, err := e.kitDBProductionManager()
	if err != nil {
		return nil
	}
	return manager.ProductionPolicies()
}

func (e *Engine) kitDBProductionManager() (*kitdbnode.Manager, error) {
	if e == nil {
		return nil, fmt.Errorf("engine is nil")
	}
	e.mu.RLock()
	manager := e.kitDBNodeManager
	managerErr := e.kitDBNodeErr
	closed := e.closed
	e.mu.RUnlock()
	if managerErr != nil {
		return nil, managerErr
	}
	if closed || manager == nil {
		return nil, kitdbnode.ErrClosed
	}
	return manager, nil
}

// ScheduleKitDBHistoryPrune submits one host-trusted retained-history advance
// to the shared KitDB node governor. Tenant code cannot call this destructive
// filesystem-level API.
func (e *Engine) ScheduleKitDBHistoryPrune(
	ctx context.Context,
	request kitdbnode.HistoryPruneRequest,
) (*kitdbnode.MaintenanceTicket, error) {
	if e == nil {
		return nil, fmt.Errorf("engine is nil")
	}
	e.mu.RLock()
	manager := e.kitDBNodeManager
	e.mu.RUnlock()
	if manager == nil {
		return nil, fmt.Errorf("KitDB node manager is unavailable")
	}
	return manager.ScheduleHistoryPrune(ctx, request)
}

// ScheduleKitDBReplicaCatchUp submits one host-trusted source-to-target replay
// to the shared KitDB node governor. Both database paths remain reserved for the
// operation and tenant code cannot call this topology-level API.
func (e *Engine) ScheduleKitDBReplicaCatchUp(
	ctx context.Context,
	request kitdbnode.ReplicaCatchUpRequest,
) (*kitdbnode.MaintenanceTicket, error) {
	if e == nil {
		return nil, fmt.Errorf("engine is nil")
	}
	e.mu.RLock()
	manager := e.kitDBNodeManager
	e.mu.RUnlock()
	if manager == nil {
		return nil, fmt.Errorf("KitDB node manager is unavailable")
	}
	return manager.ScheduleReplicaCatchUp(ctx, request)
}

// Health returns bounded process-local runtime and lifecycle aggregates without
// exposing request, URL, tenant, or argument data. The returned snapshot is
// detached and safe to serialize while the engine is live. Ownership fields
// are best effort: a busy engine ownership lock leaves them zero and marks
// OwnershipSnapshotAvailable false without delaying lifecycle gauges.
func (e *Engine) Health() work.RuntimeHealthSnapshot {
	if e == nil {
		return work.RuntimeHealthSnapshot{}
	}
	if !e.mu.TryRLock() {
		snapshot := e.runtimeHealth.Snapshot()
		snapshot.VMPool = work.VMPoolHealth()
		return snapshot
	}
	defer e.mu.RUnlock()
	// Cold preparation and every activation begin while owning e.mu. Capture
	// lifecycle only after the ownership read lock is secured so an available
	// snapshot cannot pair stale lifecycle gauges with post-transition ownership.
	snapshot := e.runtimeHealth.Snapshot()
	snapshot.VMPool = work.VMPoolHealth()
	snapshot.OwnershipSnapshotAvailable = true
	snapshot.LoadedApps = len(e.appRuntimes)
	snapshot.LoadedSites = len(e.cache)
	maxInt := int(^uint(0) >> 1)
	// Every cached.tenant mutation requires e.mu for writing. Reading it directly
	// under this read lock avoids waiting for cachedTenant.mu and makes the
	// ownership portion one consistent best-effort snapshot.
	for _, cached := range e.cache {
		tenant := cached.tenant
		if tenant == nil {
			continue
		}
		generation := tenant.SiteGeneration()
		if generation == nil || generation.Retired() {
			continue
		}
		snapshot.ActiveGenerations++
		active := generation.Active()
		if active > maxInt-snapshot.ActiveGenerationLeases {
			snapshot.ActiveGenerationLeases = maxInt
		} else {
			snapshot.ActiveGenerationLeases += active
		}
	}
	drainingLeases := snapshot.Generations.DrainingLeases
	if drainingLeases > uint64(maxInt-snapshot.ActiveGenerationLeases) {
		snapshot.ActiveGenerationLeases = maxInt
	} else {
		snapshot.ActiveGenerationLeases += int(drainingLeases)
	}
	return snapshot
}

func (e *Engine) prepareTenantCandidate(
	hostname string,
	appRuntime *app.Runtime,
	siteRuntime *site.Runtime,
) (_ *work.Tenant, err error) {
	finish := e.runtimeHealth.BeginGenerationPrepare()
	succeeded := false
	var generation *site.Generation
	var candidate *work.Tenant
	defer func() {
		finish(succeeded)
	}()
	defer func() {
		if !succeeded {
			if candidate != nil {
				candidate.Close()
			} else if generation != nil {
				generation.Retire()
			}
		}
	}()
	if siteRuntime == nil {
		return nil, fmt.Errorf("site runtime is nil")
	}
	generation, err = siteRuntime.PrepareGeneration()
	if err != nil {
		return nil, err
	}
	e.bytecodeCacheMu.RLock()
	directory := e.bytecodeCacheDir
	e.bytecodeCacheMu.RUnlock()
	if directory != "" {
		if setError := generation.SetBytecodeCache(compiler.NewFileCache(directory)); setError != nil {
			return nil, setError
		}
	}
	candidate = work.NewTenantWithRuntimeLayout(
		e.root,
		hostname,
		e.rootLayout,
		appRuntime,
		siteRuntime,
		generation,
	)
	candidate.SetSearchManager(e.searchManager, e.searchManagerErr)
	candidate.SetKitDBNodeManager(e.kitDBNodeManager, e.kitDBNodeErr)
	candidate.SetCollectionSearchCanary(e.collectionCanary.Load())
	candidate.SetCollectionSearchCanaryTelemetry(e.collectionStats)
	candidate.SetRuntimeHealth(e.runtimeHealth)
	candidate.MaxEnergy = e.maxEnergy
	candidate.HotReload = e.hotReload
	if err = candidate.Run(); err != nil {
		return nil, err
	}
	succeeded = true
	return candidate, nil
}

func (e *Engine) activateGeneration(tenant *work.Tenant) (err error) {
	finish := e.runtimeHealth.BeginGenerationActivate()
	succeeded := false
	defer func() {
		finish(succeeded)
	}()
	err = tenant.ActivateGeneration()
	succeeded = err == nil
	return err
}

func (e *Engine) drainTenant(tenant *work.Tenant) {
	if tenant == nil {
		return
	}
	generation := tenant.SiteGeneration()
	if generation == nil {
		tenant.Close()
		return
	}
	finish := e.runtimeHealth.BeginGenerationDrain(generation)
	succeeded := false
	defer func() {
		finish(succeeded)
	}()
	tenant.Close()
	succeeded = true
}

// SetIdleTimeout chỉnh thời gian một tenant idle được giữ trong RAM cache.
// Đặt 0 để KHÔNG BAO GIỜ evict — hợp khi số tenant ít & cố định (giữ ấm mãi).
func (e *Engine) SetIdleTimeout(d time.Duration) {
	e.mu.Lock()
	e.idleTimeout = d
	e.mu.Unlock()
}

func (e *Engine) cleanupLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			var evicted []*work.Tenant
			e.mu.Lock()
			timeout := e.idleTimeout
			if timeout > 0 { // 0 = không evict (giữ ấm mọi tenant)
				now := time.Now()
				for domain, cached := range e.cache {
					if cached.isExpired(now, timeout) {
						slog.Info("Evicting idle tenant from cache", "domain", domain)
						evicted = append(evicted, cached.tenant)
						delete(e.cache, domain)
					}
				}
			}
			e.mu.Unlock()
			for _, tenant := range evicted {
				e.drainTenant(tenant)
				e.closeTenantSearch(tenant)
				if owner := tenant.AppRuntime(); owner != nil {
					owner.RemoveSite(tenant.Domain())
				}
			}
		case <-e.stopCleanup:
			return
		}
	}
}

func (e *Engine) closeTenantSearch(tenant *work.Tenant) {
	if e == nil || tenant == nil || e.searchManager == nil {
		return
	}
	for _, key := range tenant.SearchIndexKeys() {
		if err := e.searchManager.CloseIndex(key); err != nil {
			slog.Warn("Search index failed to close during site eviction", "error", err)
		}
	}
}

// Close stops host-owned background work and drains every loaded app/site runtime.
// It is idempotent and closes tenants outside the engine lock because Tenant.Close
// may wait for in-flight requests and background jobs.
func (e *Engine) Close() {
	if e == nil {
		return
	}
	e.closeOnce.Do(func() {
		close(e.stopCleanup)

		e.mu.Lock()
		e.closed = true
		tenants := make([]*work.Tenant, 0, len(e.cache)+len(e.appTenants))
		seen := make(map[*work.Tenant]struct{}, len(e.cache)+len(e.appTenants))
		for _, cached := range e.cache {
			if _, ok := seen[cached.tenant]; !ok {
				seen[cached.tenant] = struct{}{}
				tenants = append(tenants, cached.tenant)
			}
		}
		for _, tenant := range e.appTenants {
			if _, ok := seen[tenant]; !ok {
				seen[tenant] = struct{}{}
				tenants = append(tenants, tenant)
			}
		}
		apps := make([]*app.Runtime, 0, len(e.appRuntimes))
		for _, appRuntime := range e.appRuntimes {
			apps = append(apps, appRuntime)
		}
		e.cache = make(map[string]*cachedTenant)
		e.localHosts = make(map[string]string)
		e.appTenants = make(map[string]*work.Tenant)
		e.appRuntimes = make(map[string]*app.Runtime)
		e.appStarting = make(map[string]struct{})
		e.mu.Unlock()

		for _, tenant := range tenants {
			e.drainTenant(tenant)
		}
		for _, appRuntime := range apps {
			appRuntime.Close()
		}
		if e.searchManager != nil {
			if err := e.searchManager.Close(); err != nil {
				slog.Warn("Search manager failed to close cleanly", "error", err)
			}
		}
		if e.kitDBNodeManager != nil {
			if err := e.kitDBNodeManager.Close(); err != nil {
				slog.Warn("KitDB node manager failed to close cleanly", "error", err)
			}
		}
	})
}

func (e *Engine) run(hostname string) (*work.Tenant, error) {
	// 1. Read lock check to see if the tenant is already cached
	e.mu.RLock()
	if e.closed {
		e.mu.RUnlock()
		return nil, fmt.Errorf("engine is closed")
	}
	cached, ok := e.cache[hostname]
	e.mu.RUnlock()

	if ok {
		cached.touch()

		// Hot reload checks
		if e.hotReload {
			// Requests arriving while a candidate is being prepared wait for
			// the publication decision, so none can receive an old Tenant in
			// the narrow window before it is retired.
			cached.reloadMu.Lock()
			defer cached.reloadMu.Unlock()
			current := cached.current()
			now := time.Now()
			cached.mu.Lock()
			shouldCheck := now.Sub(cached.lastChecked) > 1*time.Second
			if shouldCheck {
				cached.lastChecked = now
			}
			cached.mu.Unlock()

			if shouldCheck {
				// Lấy đường dẫn root router (marker) của tenant
				routerFile := current.RouterFile()
				_, err := os.Stat(routerFile)

				if err != nil {
					if os.IsNotExist(err) {
						// File đã bị xóa/đổi tên -> Loại bỏ khỏi cache và trả về lỗi
						slog.Warn("Tenant directory or file removed. Evicting from cache", "hostname", hostname)
						e.mu.Lock()
						delete(e.cache, hostname)
						e.mu.Unlock()
						e.drainTenant(current)
						e.closeTenantSearch(current)
						if owner := current.AppRuntime(); owner != nil {
							owner.RemoveSite(hostname)
						}
						return nil, fmt.Errorf("tenant not found: %s", hostname)
					}
					// Lỗi đọc đĩa khác -> Tiếp tục dùng bản cũ
					slog.Error("os.Stat error during hot reload", "error", err)
				} else {
					changed, changeErr := current.SourcesChanged()
					if changeErr != nil {
						slog.Error("Source manifest check failed; keeping current generation", "error", changeErr)
					} else if changed {
						slog.Info("Detecting source change. Preparing generation...", "site", hostname)
						newTenant, prepareErr := e.prepareTenantCandidate(
							hostname,
							current.AppRuntime(),
							current.SiteRuntime(),
						)
						if prepareErr != nil {
							slog.Error(
								"Generation preparation failed during hot reload. Fallback to cached version",
								"error",
								prepareErr,
							)
							return current, nil
						}
						newTenant.InheritSearchState(current)
						// Thành công -> cập nhật cache
						e.mu.Lock()
						if e.closed {
							e.mu.Unlock()
							newTenant.Close()
							return nil, fmt.Errorf("engine is closed")
						}
						if err := e.activateGeneration(newTenant); err != nil {
							e.mu.Unlock()
							newTenant.Close()
							return nil, fmt.Errorf("activate site generation: %w", err)
						}
						cached.mu.Lock()
						oldTenant := cached.tenant
						cached.tenant = newTenant
						cached.mu.Unlock()
						e.mu.Unlock()
						e.drainTenant(oldTenant)
						newTenant.InheritSearchState(oldTenant)
						slog.Info("Successfully reloaded tenant", "hostname", hostname)
					}
				}
			}
		}
		return cached.current(), nil
	}

	// 2. Write lock block for initialization
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, fmt.Errorf("engine is closed")
	}

	// 3. Double-check to see if another goroutine initialized it while we were waiting for the lock
	if cached, ok = e.cache[hostname]; ok {
		cached.touch()
		return cached.current(), nil
	}

	identity, err := work.ResolveIdentityForLayout(e.root, hostname, e.rootLayout)
	if err != nil {
		return nil, err
	}
	appRuntime := e.appRuntimeLocked(identity, hostname)
	siteRuntime, err := appRuntime.Site(e.root, hostname)
	if err != nil {
		return nil, err
	}
	tenant, err := e.prepareTenantCandidate(hostname, appRuntime, siteRuntime)
	if err != nil {
		appRuntime.RemoveSite(hostname)
		return nil, err
	}
	if err := e.activateGeneration(tenant); err != nil {
		tenant.Close()
		appRuntime.RemoveSite(hostname)
		return nil, err
	}

	e.cache[hostname] = &cachedTenant{
		tenant:      tenant,
		lastAccess:  time.Now(),
		lastChecked: time.Now(),
	}
	return tenant, nil
}

// Prewarm compile sẵn mọi tenant tìm thấy dưới root vào cache, để request ĐẦU
// TIÊN tới mỗi tenant không phải chịu cold compile. Best-effort: tenant nào lỗi
// compile sẽ được log và bỏ qua (request sau sẽ thử lại). Gọi sau New(),
// trước khi phục vụ. Trả về (số warm, số lỗi). Caller có thể chạy nền: go e.Prewarm().
func (e *Engine) Prewarm() (warmed int, failed int) {
	domains := e.discoverTenants()
	for _, d := range domains {
		if _, err := e.run(d); err != nil {
			slog.Warn("Prewarm: tenant failed to compile; a later request will retry", "domain", d, "error", err)
			failed++
			continue
		}
		warmed++
	}
	slog.Info("Prewarm complete", "warmed", warmed, "failed", failed, "total", len(domains))
	return warmed, failed
}

// discoverTenants liệt kê domain tenant bằng cách duyệt root/<domain>/ hoặc root/<identity>/<domain>/
// và lấy thư mục nào chứa marker của tenant — root router (router.kitwork.js). Không còn gì
// liên quan tới app.kitwork.js: cây filesystem là mô hình duy nhất.
func (e *Engine) discoverTenants() []string {
	switch e.rootLayout {
	case work.RootLayoutSingle:
		domain := e.Hostname
		if domain == "" {
			domain = "localhost"
		}
		if info, err := os.Stat(filepath.Join(e.root, work.RouterFileName)); err == nil && !info.IsDir() {
			return []string{domain}
		}
		return nil
	case work.RootLayoutMultiDomain:
		return work.DiscoverFlatSites(e.root)
	case work.RootLayoutMultiTenant:
		return work.DiscoverTenantDomains(e.root)
	}

	var domains []string
	entries, err := os.ReadDir(e.root)
	if err != nil {
		slog.Error("Prewarm: cannot read root", "root", e.root, "error", err)
		return domains
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// 0. Single-tenant convention: root/sites/<domain>/router.kitwork.js (no identity layer).
		// Handled explicitly so it is not mistaken for an identity folder.
		if entry.Name() == work.SitesDirName {
			domains = append(domains, work.DiscoverSites(e.root)...)
			continue
		}
		// 1. Kiểm tra cấu trúc phẳng: root/<domain>/router.kitwork.js
		if _, err := os.Stat(filepath.Join(e.root, entry.Name(), work.RouterFileName)); err == nil {
			domains = append(domains, entry.Name())
			continue
		}
		// 2. Kiểm tra cấu trúc lồng: root/<identity>/<domain>/router.kitwork.js
		idPath := filepath.Join(e.root, entry.Name())
		subEntries, err := os.ReadDir(idPath)
		if err != nil {
			continue
		}
		for _, sub := range subEntries {
			if !sub.IsDir() {
				continue
			}
			if _, err := os.Stat(filepath.Join(idPath, sub.Name(), work.RouterFileName)); err == nil {
				domains = append(domains, sub.Name())
			}
		}
	}
	return domains
}

func (e *Engine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	observed := &observedResponseWriter{ResponseWriter: w}
	w = observed
	started := time.Now()
	e.runtimeHealth.RequestStarted()
	defer func() {
		e.runtimeHealth.RequestCompleted(observed.Status(), time.Since(started))
	}()
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("Critical panic recovered", "panic", rec)
			http.Error(w, "Service Unavailable", 503)
		}
	}()

	// 1. Host-level rate limit — the FIRST gate, before any tenant work, so a flood is refused at
	// the cheapest possible point. check() writes the 429 itself on rejection.
	if rl := e.rateLimiter; rl != nil && !rl.check(w, r) {
		return
	}

	resolvedHost, err := e.resolveRequestHost(r.Host, work.AllowLocal)
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	domain := resolvedHost.Domain

	// 2. Domain redirects on the EFFECTIVE domain (after local host resolution),
	// and BEFORE tenant resolution — a redirect-only domain has no tenant folder.
	// Order: static config (canonical www↔apex + map) then the system-DB `redirect_to`
	// column (cached). http→https itself is forced by the :80 ACME fallback.
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if !(work.AllowLocal && resolvedHost.Local) {
		if target, ok := dom.Target(scheme, domain, r.URL.Path, r.URL.RawQuery, false); ok {
			http.Redirect(w, r, target, http.StatusMovedPermanently)
			return
		}
	}
	if !work.AllowLocal {
		if to := dom.DBRedirectTarget(domain); to != "" && to != domain {
			http.Redirect(w, r, dom.RedirectURL(scheme, to, r.URL.Path, r.URL.RawQuery), http.StatusMovedPermanently)
			return
		}
	}

	resolveStarted := time.Now()
	tenant, err := e.run(domain)
	e.runtimeHealth.RecordResolve(time.Since(resolveStarted))

	if err != nil {
		e.forgetLocalHostResolution(resolvedHost)
		http.Error(w, err.Error(), 404)
		return
	}
	e.rememberLocalHostResolution(resolvedHost)

	e.mu.RLock()
	authorizer := e.authorizer
	e.mu.RUnlock()
	if authorizer != nil {
		authorization, authErr := authorizer(r, tenant.AppID(), tenant.Domain())
		if authErr != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		r = r.WithContext(requestscope.WithAuthorization(r.Context(), authorization))
	}

	// tenant := work.NewTenant(e.root, domain)

	// tenant.MaxEnergy = e.maxEnergy

	// if err := tenant.Run(); err != nil {

	// 	// Lỗi cú pháp hoặc file dở dang -> Graceful Compile Fallback
	// 	slog.Error("Compile error during hot reload. Fallback to cached version", "error", err)
	// }

	// Bàn giao toàn bộ quyền xử lý cho Tenant
	tenant.Serve(w, r)
}
