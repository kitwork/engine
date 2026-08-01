package core

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/compiler"
	dom "github.com/kitwork/engine/domain"
	requestscope "github.com/kitwork/engine/request"
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
	root             string
	maxEnergy        uint64
	hotReload        bool
	Hostname         string
	cache            map[string]*cachedTenant
	appRuntimes      map[string]*app.Runtime
	appTenants       map[string]*work.Tenant // identity → app runtime that owns that identity's _cron scheduler
	appStarting      map[string]struct{}
	idleTimeout      time.Duration // bao lâu idle thì evict khỏi cache; 0 = không bao giờ evict
	rateLimiter      *RateLimiter  // host-level limits (nil = off); set qua SetRateLimit trước khi serve
	authorizer       Authorizer
	bytecodeCacheMu  sync.RWMutex
	bytecodeCacheDir string
	runtimeHealth    *work.RuntimeHealth
	mu               sync.RWMutex
	stopCleanup      chan struct{}
	closeOnce        sync.Once
	closed           bool
}

func New(root string, maxEnergy uint64, hotReload bool, hostname string) *Engine {
	if maxEnergy == 0 {
		maxEnergy = 10000000 // Default 10M
	}
	e := &Engine{
		root:          root,
		maxEnergy:     maxEnergy,
		hotReload:     hotReload,
		Hostname:      hostname,
		cache:         make(map[string]*cachedTenant),
		appRuntimes:   make(map[string]*app.Runtime),
		appTenants:    make(map[string]*work.Tenant),
		appStarting:   make(map[string]struct{}),
		runtimeHealth: work.NewRuntimeHealth(),
		idleTimeout:   10 * time.Minute, // mặc định; chỉnh bằng SetIdleTimeout (0 = không evict)
		stopCleanup:   make(chan struct{}),
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

// appRuntimeLocked returns the host-owned application runtime. The caller must
// hold e.mu for writing so runtime creation remains a singleton per key.
func (e *Engine) appRuntimeLocked(identity, domain string) *app.Runtime {
	key := appRuntimeKey(identity, domain)
	if current := e.appRuntimes[key]; current != nil {
		return current
	}
	current := app.NewRuntime(identity)
	e.appRuntimes[key] = current
	return current
}

// StartAppSchedulers boots one app runtime per identity that has a non-empty _cron/, EAGERLY at startup.
// Each app-tenant loads apps/<identity>/_cron and starts a single scheduler for the app — so crons run
// without waiting for a domain to be hit (routing is lazy) and without one dispatcher per domain. This
// is the deliberate exception to "no prewarm": a scheduler cannot wait for a request. Call once after
// the system DB is wired (so the shared-Postgres backend is chosen). Idempotent per identity.
func (e *Engine) StartAppSchedulers() (started int) {
	for _, identity := range work.DiscoverAppIdentities(e.root) {
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

		appTenant := work.NewAppTenantWithRuntime(e.root, identity, appRuntime)
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

// Health returns a process-local VM report without exposing request or tenant
// data. The returned snapshot is safe to serialize while the engine is live.
func (e *Engine) Health() work.RuntimeHealthSnapshot {
	if e == nil {
		return work.RuntimeHealthSnapshot{}
	}
	return e.runtimeHealth.Snapshot()
}

func (e *Engine) prepareGeneration(siteRuntime *site.Runtime) (*site.Generation, error) {
	generation, err := siteRuntime.PrepareGeneration()
	if err != nil {
		return nil, err
	}
	e.bytecodeCacheMu.RLock()
	directory := e.bytecodeCacheDir
	e.bytecodeCacheMu.RUnlock()
	if directory != "" {
		if err := generation.SetBytecodeCache(compiler.NewFileCache(directory)); err != nil {
			generation.Retire()
			return nil, err
		}
	}
	return generation, nil
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
				tenant.Close()
				if owner := tenant.AppRuntime(); owner != nil {
					owner.RemoveSite(tenant.Domain())
				}
			}
		case <-e.stopCleanup:
			return
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
		e.appTenants = make(map[string]*work.Tenant)
		e.appRuntimes = make(map[string]*app.Runtime)
		e.appStarting = make(map[string]struct{})
		e.mu.Unlock()

		for _, tenant := range tenants {
			tenant.Close()
		}
		for _, appRuntime := range apps {
			appRuntime.Close()
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
						current.Close()
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
						generation, prepareErr := e.prepareGeneration(current.SiteRuntime())
						if prepareErr != nil {
							slog.Error(
								"Generation preparation failed during hot reload. Fallback to cached version",
								"error",
								prepareErr,
							)
							return current, nil
						}
						newTenant := work.NewTenantWithRuntime(
							e.root,
							hostname,
							current.AppRuntime(),
							current.SiteRuntime(),
							generation,
						)
						newTenant.SetRuntimeHealth(e.runtimeHealth)
						newTenant.MaxEnergy = e.maxEnergy
						newTenant.HotReload = e.hotReload

						if err := newTenant.Run(); err != nil {
							// Lỗi cú pháp hoặc file dở dang -> Graceful Compile Fallback
							slog.Error("Compile error during hot reload. Fallback to cached version", "error", err)
							newTenant.Close()
						} else {
							// Thành công -> cập nhật cache
							e.mu.Lock()
							if e.closed {
								e.mu.Unlock()
								newTenant.Close()
								return nil, fmt.Errorf("engine is closed")
							}
							if err := newTenant.ActivateGeneration(); err != nil {
								e.mu.Unlock()
								newTenant.Close()
								return nil, fmt.Errorf("activate site generation: %w", err)
							}
							cached.mu.Lock()
							oldTenant := cached.tenant
							cached.tenant = newTenant
							cached.mu.Unlock()
							e.mu.Unlock()
							oldTenant.Close()
							slog.Info("Successfully reloaded tenant", "hostname", hostname)
						}
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

	identity := work.ResolveIdentity(e.root, hostname)
	appRuntime := e.appRuntimeLocked(identity, hostname)
	siteRuntime, err := appRuntime.Site(e.root, hostname)
	if err != nil {
		return nil, err
	}
	generation, err := e.prepareGeneration(siteRuntime)
	if err != nil {
		appRuntime.RemoveSite(hostname)
		return nil, err
	}
	tenant := work.NewTenantWithRuntime(e.root, hostname, appRuntime, siteRuntime, generation)
	tenant.SetRuntimeHealth(e.runtimeHealth)
	tenant.MaxEnergy = e.maxEnergy
	tenant.HotReload = e.hotReload

	if err := tenant.Run(); err != nil {
		tenant.Close()
		appRuntime.RemoveSite(hostname)
		return nil, err
	}
	if err := tenant.ActivateGeneration(); err != nil {
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

	domain := strings.Split(r.Host, ":")[0]
	if (domain == "localhost" || domain == "127.0.0.1") && e.Hostname != "" {
		domain = e.Hostname
	}

	// 2. Domain redirects on the EFFECTIVE domain (after localhost→Hostname mapping),
	// and BEFORE tenant resolution — a redirect-only domain has no tenant folder.
	// Order: static config (canonical www↔apex + map) then the system-DB `redirect_to`
	// column (cached). http→https itself is forced by the :80 ACME fallback.
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if target, ok := dom.Target(scheme, domain, r.URL.Path, r.URL.RawQuery, false); ok {
		http.Redirect(w, r, target, http.StatusMovedPermanently)
		return
	}
	if !work.AllowLocal {
		if to := dom.DBRedirectTarget(domain); to != "" && to != domain {
			http.Redirect(w, r, dom.RedirectURL(scheme, to, r.URL.Path, r.URL.RawQuery), http.StatusMovedPermanently)
			return
		}
	}

	tenant, err := e.run(domain)

	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}

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
