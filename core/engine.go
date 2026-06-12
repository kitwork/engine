package core

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kitwork/engine/work"
)

type cachedTenant struct {
	tenant       *work.Tenant
	lastAccess   time.Time
	lastCompiled time.Time // Thời điểm compile file app.kitwork.js gần nhất
	lastChecked  time.Time // Thời điểm thực hiện check ModTime gần nhất (để throttle)
	mu           sync.Mutex
}

func (c *cachedTenant) touch() {
	c.mu.Lock()
	c.lastAccess = time.Now()
	c.mu.Unlock()
}

func (c *cachedTenant) isExpired(now time.Time, timeout time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return now.Sub(c.lastAccess) > timeout
}

type Engine struct {
	root      string
	maxEnergy uint64
	hotReload bool
	Hostname  string
	cache     map[string]*cachedTenant
	mu        sync.RWMutex

	RateLimit *RateLimiter
}

func New(root string, maxEnergy uint64, hotReload bool, hostname string) *Engine {
	if maxEnergy == 0 {
		maxEnergy = 10000000 // Default 10M
	}
	e := &Engine{
		root:      root,
		maxEnergy: maxEnergy,
		hotReload: hotReload,
		Hostname:  hostname,
		cache:     make(map[string]*cachedTenant),
		RateLimit: &RateLimiter{
			Enabled:          true,
			Rate:             2000,
			IpRate:           200,
			Period:           time.Second,
			currentLimiters:  make(map[string]*work.RateLimiter),
			previousLimiters: make(map[string]*work.RateLimiter),
			lastRotation:     time.Now(),
		},
	}
	// Start background cleanup loop every 1 minute, with 10 minutes idle timeout
	go e.cleanupLoop(1*time.Minute, 10*time.Minute)
	return e
}

func (e *Engine) cleanupLoop(interval time.Duration, idleTimeout time.Duration) {
	ticker := time.NewTicker(interval)
	for range ticker.C {
		e.mu.Lock()
		now := time.Now()
		for domain, cached := range e.cache {
			if cached.isExpired(now, idleTimeout) {
				slog.Info("Evicting idle tenant from cache", "domain", domain)
				delete(e.cache, domain)
			}
		}
		e.mu.Unlock()
	}
}

func (e *Engine) run(hostname string) (*work.Tenant, error) {
	// 1. Read lock check to see if the tenant is already cached
	e.mu.RLock()
	cached, ok := e.cache[hostname]
	e.mu.RUnlock()

	if ok {
		cached.touch()

		// Hot reload checks
		if e.hotReload {
			now := time.Now()
			cached.mu.Lock()
			shouldCheck := now.Sub(cached.lastChecked) > 1*time.Second
			if shouldCheck {
				cached.lastChecked = now
			}
			cached.mu.Unlock()

			if shouldCheck {
				// Lấy đường dẫn file app.kitwork.js của tenant
				tempTenant := work.NewTenant(e.root, hostname)
				appFile := tempTenant.AppFile()
				info, err := os.Stat(appFile)

				if err != nil {
					if os.IsNotExist(err) {
						// File đã bị xóa/đổi tên -> Loại bỏ khỏi cache và trả về lỗi
						slog.Warn("Tenant directory or file removed. Evicting from cache", "hostname", hostname)
						e.mu.Lock()
						delete(e.cache, hostname)
						e.mu.Unlock()
						return nil, fmt.Errorf("tenant not found: %s", hostname)
					}
					// Lỗi đọc đĩa khác -> Tiếp tục dùng bản cũ
					slog.Error("os.Stat error during hot reload", "error", err)
				} else {
					// Nếu file được sửa đổi sau lần compile cuối cùng
					if info.ModTime().After(cached.lastCompiled) {
						slog.Info("Detecting change. Recompiling...", "file", appFile)
						newTenant := work.NewTenant(e.root, hostname)
						newTenant.MaxEnergy = e.maxEnergy
						if err := newTenant.Run(); err != nil {
							// Lỗi cú pháp hoặc file dở dang -> Graceful Compile Fallback
							slog.Error("Compile error during hot reload. Fallback to cached version", "error", err)
						} else {
							// Thành công -> cập nhật cache
							e.mu.Lock()
							cached.tenant = newTenant
							cached.lastCompiled = info.ModTime()
							e.mu.Unlock()
							slog.Info("Successfully reloaded tenant", "hostname", hostname)
						}
					}
				}
			}
		}
		return cached.tenant, nil
	}

	// 2. Write lock block for initialization
	e.mu.Lock()
	defer e.mu.Unlock()

	// 3. Double-check to see if another goroutine initialized it while we were waiting for the lock
	if cached, ok = e.cache[hostname]; ok {
		cached.touch()
		return cached.tenant, nil
	}

	tenant := work.NewTenant(e.root, hostname)
	tenant.MaxEnergy = e.maxEnergy
	if err := tenant.Run(); err != nil {
		return nil, err
	}

	// Lấy ModTime để lưu làm lastCompiled
	lastCompiled := time.Now()
	if info, err := os.Stat(tenant.AppFile()); err == nil {
		lastCompiled = info.ModTime()
	}

	e.cache[hostname] = &cachedTenant{
		tenant:       tenant,
		lastAccess:   time.Now(),
		lastCompiled: lastCompiled,
		lastChecked:  time.Now(),
	}
	return tenant, nil
}

func (e *Engine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("Critical panic recovered", "panic", rec)
			http.Error(w, "Service Unavailable", 503)
		}
	}()

	// 1. Layer 1 Rate Limiting Check (Global & Per-IP Server Protection)
	if !e.RateLimit.check(r, w) {
		return
	}

	domain := strings.Split(r.Host, ":")[0]
	if (domain == "localhost" || domain == "127.0.0.1") && e.Hostname != "" {
		domain = e.Hostname
	}
	tenant, err := e.run(domain)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}

	// Bàn giao toàn bộ quyền xử lý cho Tenant
	tenant.Serve(w, r)
}
