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

	dom "github.com/kitwork/engine/domain"
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
	root        string
	maxEnergy   uint64
	hotReload   bool
	Hostname    string
	cache       map[string]*cachedTenant
	idleTimeout time.Duration // bao lâu idle thì evict khỏi cache; 0 = không bao giờ evict
	mu          sync.RWMutex
}

func New(root string, maxEnergy uint64, hotReload bool, hostname string) *Engine {
	if maxEnergy == 0 {
		maxEnergy = 10000000 // Default 10M
	}
	e := &Engine{
		root:        root,
		maxEnergy:   maxEnergy,
		hotReload:   hotReload,
		Hostname:    hostname,
		cache:       make(map[string]*cachedTenant),
		idleTimeout: 10 * time.Minute, // mặc định; chỉnh bằng SetIdleTimeout (0 = không evict)

	}
	// Vòng dọn cache chạy nền mỗi 1 phút; timeout đọc động từ e.idleTimeout.
	go e.cleanupLoop(1 * time.Minute)
	return e
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
	for range ticker.C {
		e.mu.Lock()
		timeout := e.idleTimeout
		if timeout > 0 { // 0 = không evict (giữ ấm mọi tenant)
			now := time.Now()
			for domain, cached := range e.cache {
				if cached.isExpired(now, timeout) {
					slog.Info("Evicting idle tenant from cache", "domain", domain)
					cached.tenant.Close()
					delete(e.cache, domain)
				}
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

// Prewarm compile sẵn mọi tenant tìm thấy dưới root vào cache, để request ĐẦU
// TIÊN tới mỗi tenant không phải chịu cold compile. Best-effort: tenant nào lỗi
// compile sẽ được log và bỏ qua (vẫn compile lazy ở request đầu). Gọi sau New(),
// trước khi phục vụ. Trả về (số warm, số lỗi). Caller có thể chạy nền: go e.Prewarm().
func (e *Engine) Prewarm() (warmed int, failed int) {
	domains := e.discoverTenants()
	for _, d := range domains {
		if _, err := e.run(d); err != nil {
			slog.Warn("Prewarm: tenant failed to compile, will compile lazily", "domain", d, "error", err)
			failed++
			continue
		}
		warmed++
	}
	slog.Info("Prewarm complete", "warmed", warmed, "failed", failed, "total", len(domains))
	return warmed, failed
}

// discoverTenants liệt kê domain tenant bằng cách duyệt root/<domain>/ hoặc root/<identity>/<domain>/
// và lấy thư mục nào chứa file app của tenant.
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
		// 0. Single-tenant convention: root/sites/<domain>/app.kitwork.js (no identity layer).
		// Handled explicitly so it is not mistaken for an identity folder.
		if entry.Name() == work.SitesDirName {
			domains = append(domains, work.DiscoverSites(e.root)...)
			continue
		}
		// 1. Kiểm tra cấu trúc phẳng: root/<domain>/app.kitwork.js
		if _, err := os.Stat(filepath.Join(e.root, entry.Name(), work.AppFileName)); err == nil {
			domains = append(domains, entry.Name())
			continue
		}
		// 2. Kiểm tra cấu trúc lồng: root/<identity>/<domain>/app.kitwork.js
		idPath := filepath.Join(e.root, entry.Name())
		subEntries, err := os.ReadDir(idPath)
		if err != nil {
			continue
		}
		for _, sub := range subEntries {
			if !sub.IsDir() {
				continue
			}
			if _, err := os.Stat(filepath.Join(idPath, sub.Name(), work.AppFileName)); err == nil {
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

	// tenant := work.NewTenant(e.root, domain)

	// tenant.MaxEnergy = e.maxEnergy

	// if err := tenant.Run(); err != nil {

	// 	// Lỗi cú pháp hoặc file dở dang -> Graceful Compile Fallback
	// 	slog.Error("Compile error during hot reload. Fallback to cached version", "error", err)
	// }

	// Bàn giao toàn bộ quyền xử lý cho Tenant
	tenant.Serve(w, r)
}
