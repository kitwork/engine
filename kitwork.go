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

	// Bootstrap DUY NHẤT là một file .kitwork.js chạy được (mặc định
	// server.kitwork.js). YAML/JSON KHÔNG còn nạp trực tiếp ở đây — muốn dùng chúng
	// thì trỏ tới từ trong server.kitwork.js: server.run("config.kitwork.yaml").
	file := "server.kitwork.js"
	if len(configFile) > 0 && configFile[0] != "" {
		file = configFile[0]
	}

	if !strings.HasSuffix(strings.ToLower(file), ".js") {
		return fmt.Errorf("engine.Run chỉ nhận bootstrap .kitwork.js, nhận %q — "+
			"muốn dùng YAML/JSON thì trỏ từ server.kitwork.js: server.run(\"config.kitwork.yaml\")", file)
	}

	if _, statErr := os.Stat(file); statErr != nil {
		return fmt.Errorf("không tìm thấy bootstrap config %s: %w", file, statErr)
	}

	// Chạy bootstrap trong VM setup tối giản, bắt object/đường-dẫn từ server.run(...).
	// Engine tự sở hữu stack → config cũng là chính ngôn ngữ Kitwork, không parser ngoài.
	raw, err := evalConfigJS(file)
	if err != nil {
		return fmt.Errorf("failed to evaluate config %s: %w", file, err)
	}
	fmt.Printf("Loaded configuration from %s (server.run)\n", file)

	cfg, err := ParseConfig(raw)
	if err != nil {
		return fmt.Errorf("failed to process configuration: %w", err)
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
	work.RateLimitEnabled = cfg.RateLimit.Enabled
	if cfg.RateLimit.Period > 0 {
		work.RateLimitPeriod = cfg.RateLimit.Period
	}
	if cfg.RateLimit.Rate > 0 {
		work.DefaultTenantRate = cfg.RateLimit.Rate
	}
	if cfg.RateLimit.IpRate > 0 {
		work.DefaultTenantIpRate = cfg.RateLimit.IpRate
	}
	if cfg.RateLimit.BrowserRate > 0 {
		work.DefaultTenantBrowserRate = cfg.RateLimit.BrowserRate
	}
	if cfg.RateLimit.UserRate > 0 {
		work.DefaultTenantUserRate = cfg.RateLimit.UserRate
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
	handler.RateLimit.Enabled = cfg.RateLimit.Enabled
	handler.RateLimit.Rate = cfg.RateLimit.Rate
	handler.RateLimit.IpRate = cfg.RateLimit.IpRate
	handler.RateLimit.BrowserRate = cfg.RateLimit.BrowserRate
	if cfg.RateLimit.Period > 0 {
		handler.RateLimit.Period = cfg.RateLimit.Period
	}

	// Pre-warm: compile sẵn các tenant để request ĐẦU TIÊN không bị cold compile.
	// Chạy nền để không chặn khởi động; run() idempotent nên an toàn với request đến
	// sớm. Standalone (1 tenant) bỏ qua — compile lazy ở request đầu là đủ rẻ.
	switch cfg.Root {
	case "", "./", "../", "/", ".", "..":
		// standalone: không prewarm
	default:
		go handler.Prewarm()
	}

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
