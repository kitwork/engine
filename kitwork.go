package engine

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/kitwork/engine/core"
	"github.com/kitwork/engine/database"
	"github.com/kitwork/engine/domain"
	"github.com/kitwork/engine/host"
	"github.com/kitwork/engine/logger"
	"github.com/kitwork/engine/work"
)

func Run(configFile ...string) (err error) {
	// Lưu ý: KHÔNG nạp .env vào môi trường tiến trình toàn cục (sẽ làm mọi tenant
	// chung env, rò secret). env là SCOPED: host đọc root .env trong evalConfigJS;
	// mỗi tenant đọc .env riêng của nó (work.Tenant.Run → kitwork().env).

	// Manifest DUY NHẤT là một file .kitwork.js chạy được: app.kitwork.js (mặc định mới),
	// hoặc server.kitwork.js (tên cũ, vẫn đọc). YAML/JSON KHÔNG nạp trực tiếp ở đây — muốn
	// dùng chúng thì trỏ từ manifest: server.run("config.kitwork.yaml").
	file := ""
	if len(configFile) > 0 && configFile[0] != "" {
		file = configFile[0]
	} else {
		for _, candidate := range []string{"app.kitwork.js", "server.kitwork.js"} {
			if _, statErr := os.Stat(candidate); statErr == nil {
				file = candidate
				break
			}
		}
		if file == "" {
			return fmt.Errorf("không tìm thấy manifest: cần app.kitwork.js (hoặc server.kitwork.js)")
		}
	}

	if !strings.HasSuffix(strings.ToLower(file), ".js") {
		return fmt.Errorf("engine.Run chỉ nhận manifest .kitwork.js, nhận %q — "+
			"muốn dùng YAML/JSON thì trỏ từ manifest: server.run(\"config.kitwork.yaml\")", file)
	}

	if _, statErr := os.Stat(file); statErr != nil {
		return fmt.Errorf("không tìm thấy manifest %s: %w", file, statErr)
	}

	// Chạy manifest trong VM setup tối giản để BẮT các khai báo (surfaces + config chung).
	// Engine tự sở hữu stack → config cũng là chính ngôn ngữ Kitwork, không parser ngoài.
	builder, err := evalServerBuilder(file)
	if err != nil {
		return fmt.Errorf("failed to evaluate config %s: %w", file, err)
	}
	if builder.err != "" {
		return fmt.Errorf("failed to evaluate config %s: config validation error: %s", file, builder.err)
	}

	// DISPATCH THEO MANIFEST: khai báo là DỮ LIỆU, lệnh mới quyết định chạy gì. Không có web
	// surface thì cloud host không có gì để phục vụ — nếu app khai desktop/mobile thì đó là
	// hợp lệ (chạy shell tương ứng), không phải lỗi.
	if !builder.hasWeb {
		if _, hasDesktop := builder.config["desktop"]; hasDesktop {
			fmt.Printf("%s khai báo app.desktop() nhưng không có web surface — không có gì để phục vụ.\n"+
				"→ Chạy `kitwork-desktop` cho app desktop, hoặc thêm `app.web({ port: env.PORT || 8080 })` để phục vụ HTTP.\n", file)
			return nil
		}
		if _, hasMobile := builder.config["mobile"]; hasMobile {
			fmt.Printf("%s chỉ khai báo app.mobile() — cloud host không có gì để phục vụ.\n", file)
			return nil
		}
		return fmt.Errorf("failed to evaluate config %s: %w", file, noWebSurfaceErr(builder, file))
	}

	raw, err := builderToMap(builder, file)
	if err != nil {
		return fmt.Errorf("failed to evaluate config %s: %w", file, err)
	}
	fmt.Printf("Loaded configuration from %s (app.web)\n", file)

	cfg, err := ParseConfig(raw)
	if err != nil {
		return fmt.Errorf("failed to process configuration: %w", err)
	}

	// apps/ is the modern root name (an app = a folder); deployments created before the rename still
	// have tenants/. When the configured apps/ is missing but the legacy folder exists, follow it —
	// an old server keeps booting untouched, no config edit required.
	if cfg.Root == "apps" {
		if _, err := os.Stat(cfg.Root); os.IsNotExist(err) {
			if _, err := os.Stat("tenants"); err == nil {
				fmt.Println("Root apps/ not found — using legacy tenants/ folder")
				cfg.Root = "tenants"
			}
		}
	}

	// Initialize structured logger
	logger.InitLogger(cfg.Logger)

	slog.Info("Kitwork Engine starting...", "port", cfg.Port, "root", cfg.Root)

	var systemConnected bool
	for i := range cfg.Databases {
		dbCfg := cfg.Databases[i]
		alias := dbCfg.Alias
		if alias == "" {
			alias = "default"
		}
		database.Configs[alias] = dbCfg

		if dbCfg.Alias == "system" {
			dbConn, err := dbCfg.Connect()
			if err != nil {
				return fmt.Errorf("failed to connect to system database: %w", err)
			}
			defer dbConn.Close()

			database.System = dbConn
			systemConnected = true
		}
	}

	if !systemConnected {
		fmt.Println("System Database is not provided")
	}

	// Pass global settings to the work package
	work.AllowLocal = cfg.AllowLocal
	work.ServerPort = cfg.Port

	// Scheduler backend is chosen automatically: a connected system Postgres → the SHARED cluster store
	// (crons + cron_runs tables, SKIP LOCKED claim, lease/heartbeat, cross-node reclaim); no system DB →
	// per-tenant SQLite. No flag — the presence of database.System is the switch (see startPersistedScheduler).
	if database.System != nil {
		slog.Info("Scheduler: shared Postgres backend (system DB connected)")
	}

	// Domain whitelist (for AutoSSL HostPolicy) + redirect rules (engine + :80 fallback).
	domain.Allows = cfg.Domains
	// Single-tenant sites/ convention: every folder under <root>/sites/ is a domain AutoSSL should
	// serve, with no identity and no DB. Enable the live HostPolicy folder check, and seed the
	// whitelist from the sites present at boot (the live check also covers ones added later).
	switch cfg.Root {
	case "", "./", "../", "/", ".", "..":
		// standalone: no sites/ root
	default:
		domain.SitesDir = filepath.Join(cfg.Root, work.SitesDirName)
		if sites := work.DiscoverSites(cfg.Root); len(sites) > 0 {
			domain.Allows = append(domain.Allows, sites...)
			slog.Info("Single-tenant sites discovered", "count", len(sites), "dir", domain.SitesDir)
		}
	}
	domain.Configure(cfg.Canonical, cfg.Redirects)

	// Initialize and run the engine
	handler := core.New(cfg.Root, cfg.MaxEnergy, cfg.HotReload, cfg.Hostname)

	// Client-IP source: as the edge server Kitwork ignores X-Forwarded-For by default (spoofable);
	// trust_proxy: true opts in when running behind your own reverse proxy.
	work.TrustProxyHeaders = cfg.TrustProxy

	// Host-level rate limits (first gate in ServeHTTP, before tenant resolution). Configured via
	// server.kitwork.js .rateLimit({...}) or the YAML rate_limit: block; absent = off.
	if cfg.RateLimit != nil {
		handler.SetRateLimit(&core.RateLimiter{
			Rate:        cfg.RateLimit.Rate,
			IPRate:      cfg.RateLimit.IP,
			BrowserRate: cfg.RateLimit.Browser,
			UserRate:    cfg.RateLimit.User,
			Period:      cfg.RateLimit.Period,
		})
	}

	// FILESYSTEM-ROUTED is lazy BY DESIGN: nothing is scanned or compiled at startup — the engine is
	// idle until the first request, and each folder's router.kitwork.js compiles on first hit. So
	// there is NO route prewarm; the old eager route-registration is gone with the flat model.
	//
	// The ONE deliberate exception is the scheduler: a cron cannot wait for a request. So every app
	// (identity) with a _cron/ boots an app runtime NOW that starts its scheduler eagerly.
	handler.StartAppSchedulers()

	if !host.IsLocalhost() && !cfg.AllowLocal {
		tlsConfig := domain.AutoSSL(cfg.Domains)

		go func() {
			server := &http.Server{
				Addr:      ":443",
				Handler:   handler,
				TLSConfig: tlsConfig,
			}
			if err := server.ListenAndServeTLS("", ""); err != nil {
				slog.Error("HTTPS Server error", "error", err)
			}
		}()
	}

	printBanner(cfg, host.IsLocalhost())

	return http.ListenAndServe(fmt.Sprintf(":%d", cfg.Port), handler)
}

// printBanner renders the Kitwork startup banner: a brand-red "KITWORK" wordmark
// plus honest runtime facts (mode, listen address, TLS, databases). No fake metrics.
func printBanner(cfg *Config, isLocalhost bool) {
	const (
		red   = "\033[38;2;248;34;68m" // brand red #f82244
		dim   = "\033[2m"
		reset = "\033[0m"
	)
	label := func(name string) string {
		return fmt.Sprintf("  %s▸%s %s%-7s%s ", red, reset, dim, name, reset)
	}

	mode, root := "Multi-Tenant", cfg.Root
	switch cfg.Root {
	case "", "./", "../", "/", ".", "..":
		mode, root = "Standalone", "."
	}

	fmt.Println("\n" + red + `█   █ █████ █████ █   █  ███  ████  █   █
█  █    █     █   █   █ █   █ █   █ █  █
███     █     █   █ █ █ █   █ ████  ███
█  █    █     █   ██ ██ █   █ █  █  █  █
█   █ █████   █   █   █  ███  █   █ █   █` + reset)
	fmt.Println(dim + "  sovereign logic engine\n" + reset)

	fmt.Printf("%s%s  %sroot:%s %s\n", label("mode"), mode, dim, reset, root)
	fmt.Printf("%shttp://localhost:%d\n", label("listen"), cfg.Port)
	if cfg.AllowLocal || isLocalhost {
		fmt.Printf("%s%sdisabled (local dev)%s\n", label("tls"), dim, reset)
	} else {
		fmt.Printf("%sAutoSSL · :443\n", label("tls"))
	}
	for _, db := range cfg.Databases {
		alias := db.Alias
		if alias == "" {
			alias = "default"
		}
		if db.Type == "sqlite" || db.Type == "sqlite3" {
			name := db.Name
			if name == "" {
				name = db.Host
			}
			fmt.Printf("%ssqlite · %s %s(%s)%s\n", label("db"), name, dim, alias, reset)
		} else {
			fmt.Printf("%s%s · %s:%d %s(%s)%s\n", label("db"), db.Type, db.Host, db.Port, dim, alias, reset)
		}
	}
	fmt.Println()
}
