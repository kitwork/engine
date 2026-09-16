package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kitwork/engine/core"
	"github.com/kitwork/engine/database"
	"github.com/kitwork/engine/domain"
	"github.com/kitwork/engine/host"
	"github.com/kitwork/engine/logger"
	"github.com/kitwork/engine/utilities/compress"
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
	if !builder.hasWeb && !builder.hasDatabase {
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
	surface := "app.database"
	if builder.hasWeb && builder.hasDatabase {
		surface = "app.web + app.database"
	} else if builder.hasWeb {
		surface = "app.web"
	}
	fmt.Printf("Loaded configuration from %s (%s)\n", file, surface)

	cfg, err := ParseConfig(raw)
	if err != nil {
		return fmt.Errorf("failed to process configuration: %w", err)
	}
	if err := resolveRootConfig(cfg); err != nil {
		return fmt.Errorf("resolve app root: %w", err)
	}
	// An owned KitDB root is declared relative to the manifest, and kitsql needs the web surface.
	manifestDirectory, err := filepath.Abs(filepath.Dir(file))
	if err != nil {
		return fmt.Errorf("resolve manifest directory: %w", err)
	}
	for index := range cfg.AppDatabases {
		if !filepath.IsAbs(cfg.AppDatabases[index].Path) {
			cfg.AppDatabases[index].Path = filepath.Join(manifestDirectory, cfg.AppDatabases[index].Path)
		}
	}
	if !builder.hasWeb {
		for _, configured := range cfg.AppDatabases {
			if configured.KitSQL {
				return fmt.Errorf("app.database %q: kitsql requires an app.web surface", configured.Path)
			}
		}
	}

	// Initialize structured logger
	logger.InitLogger(cfg.Logger)

	slog.Info(
		"Kitwork Engine starting...",
		"port", cfg.Port,
		"root", cfg.Root,
		"layout", cfg.RootLayout.String(),
	)

	var systemConnected bool
	for i := range cfg.Databases {
		dbCfg := cfg.Databases[i]
		alias := dbCfg.Alias
		if alias == "" {
			alias = "default"
		}
		database.Configs[alias] = dbCfg

		if strings.EqualFold(strings.TrimSpace(dbCfg.Alias), "system") {
			dbConn, err := dbCfg.Connect()
			if err != nil {
				return fmt.Errorf("failed to connect to system database: %w", err)
			}
			defer dbConn.Close()

			database.System = dbConn
			// Record the DIALECT alongside the handle: a *sql.DB cannot be asked what SQL it
			// speaks, and the background stores need to know rather than assume.
			database.SystemDriver = strings.ToLower(strings.TrimSpace(dbCfg.Type))
			systemConnected = true
		}
	}

	if !systemConnected {
		fmt.Println("System Database is not provided")
	}

	appDatabaseContext, cancelAppDatabases := context.WithCancel(context.Background())
	appDatabaseRuntimes, appDatabaseErrors, err := startAppDatabaseRuntimes(appDatabaseContext, cfg.AppDatabases)
	if err != nil {
		cancelAppDatabases()
		return err
	}
	closeAppDatabases := func() error {
		cancelAppDatabases()
		var closeErr error
		for index := len(appDatabaseRuntimes) - 1; index >= 0; index-- {
			closeErr = errors.Join(closeErr, appDatabaseRuntimes[index].Close())
		}
		return closeErr
	}
	defer closeAppDatabases()

	if !builder.hasWeb {
		printBanner(cfg, host.IsLocalhost(), false)
		signalCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stopSignals()
		select {
		case <-signalCtx.Done():
			slog.Info("Shutdown signal received")
		case runErr := <-appDatabaseErrors:
			slog.Error("KitDB server stopped", "error", runErr)
			return runErr
		}
		return closeAppDatabases()
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
	domain.Allows = append([]string(nil), cfg.Domains...)
	domain.SitesDir = ""
	switch cfg.RootLayout {
	case work.RootLayoutSingle:
		if cfg.Hostname != "" {
			domain.Allows = append(domain.Allows, cfg.Hostname)
		}
	case work.RootLayoutMultiDomain:
		domain.SitesDir = cfg.Root
		if sites := work.DiscoverFlatSites(cfg.Root); len(sites) > 0 {
			domain.Allows = append(domain.Allows, sites...)
			slog.Info("Multi-site domains discovered", "count", len(sites), "dir", domain.SitesDir)
		}
	case work.RootLayoutMultiTenant:
		if sites := work.DiscoverTenantDomains(cfg.Root); len(sites) > 0 {
			domain.Allows = append(domain.Allows, sites...)
			slog.Info("Multi-tenant domains discovered", "count", len(sites), "root", cfg.Root)
		}
	case work.RootLayoutAuto:
		// Compatibility mode for explicitly configured legacy roots.
		domain.SitesDir = filepath.Join(cfg.Root, work.SitesDirName)
		if sites := work.DiscoverSites(cfg.Root); len(sites) > 0 {
			domain.Allows = append(domain.Allows, sites...)
			slog.Info("Legacy sites discovered", "count", len(sites), "dir", domain.SitesDir)
		}
	}
	domain.Configure(cfg.Canonical, cfg.Redirects)

	// Initialize and run the engine
	handler := core.New(cfg.Root, cfg.MaxEnergy, cfg.HotReload, cfg.Hostname)
	defer handler.Close()
	if err := handler.SetRootLayout(cfg.RootLayout); err != nil {
		return fmt.Errorf("configure app root layout: %w", err)
	}
	if directory := bytecodeCacheDirectory(cfg); directory != "" {
		handler.SetBytecodeCache(directory)
	}
	if cfg.Search.CollectionCanary {
		if err := handler.SetCollectionSearchCanary(true); err != nil {
			return fmt.Errorf("configure collection search canary: %w", err)
		}
		slog.Info("Collection search segment canary enabled")
	}

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
	for _, site := range work.DiscoverLegacySites(cfg.Root) {
		slog.Warn("Legacy site entry is ignored; migrate to router.kitwork.js", "site", site)
	}

	// Transport compression wraps the whole handler once. A Kitwork page is markup-heavy — a real
	// site measured 174 KB uncompressed — and while the render costs microseconds, shipping those
	// bytes costs hundreds of milliseconds. The middleware leaves live streams, already-compressed
	// formats and tiny bodies alone; see utilities/compress.
	var baseHandler http.Handler = handler
	baseHandler, err = newAppDatabaseKitSQLHandler(
		appDatabaseRuntimes,
		baseHandler,
		cfg.AllowLocal || host.IsLocalhost(),
	)
	if err != nil {
		return fmt.Errorf("configure KitSQL endpoint: %w", err)
	}
	srvHandler := compress.Middleware(baseHandler)
	var servers []*http.Server
	serverErrors := make(chan error, 2)
	if !host.IsLocalhost() && !cfg.AllowLocal {
		tlsConfig := domain.AutoSSL(cfg.Domains)

		httpsServer := &http.Server{
			Addr:      ":443",
			Handler:   srvHandler,
			TLSConfig: tlsConfig,
		}
		servers = append(servers, httpsServer)
		go func() {
			serverErrors <- httpsServer.ListenAndServeTLS("", "")
		}()
	}

	printBanner(cfg, host.IsLocalhost(), true)

	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: srvHandler,
	}
	servers = append(servers, httpServer)
	go func() {
		serverErrors <- httpServer.ListenAndServe()
	}()

	signalCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	var runErr error
	select {
	case <-signalCtx.Done():
		slog.Info("Shutdown signal received")
	case runErr = <-serverErrors:
		if !errors.Is(runErr, http.ErrServerClosed) {
			slog.Error("HTTP server stopped", "error", runErr)
		}
	case runErr = <-appDatabaseErrors:
		slog.Error("KitDB server stopped", "error", runErr)
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	for _, server := range servers {
		if err := server.Shutdown(shutdownCtx); err != nil && runErr == nil {
			runErr = err
		}
	}
	if err := closeAppDatabases(); err != nil && runErr == nil {
		runErr = err
	}

	if errors.Is(runErr, http.ErrServerClosed) {
		return nil
	}
	return runErr
}

// Check validates the executable manifest and prepares every discovered site
// without opening a listener, publishing a generation, or starting cron.
func Check(configFile ...string) (core.CheckReport, error) {
	cfg, err := commandConfig(configFile...)
	if err != nil {
		return core.CheckReport{}, err
	}
	for i := range cfg.Databases {
		dbConfig := cfg.Databases[i]
		alias := dbConfig.Alias
		if alias == "" {
			alias = "default"
		}
		database.Configs[alias] = dbConfig
	}
	work.AllowLocal = cfg.AllowLocal
	return core.CheckWithLayout(
		cfg.Root,
		cfg.MaxEnergy,
		cfg.RootLayout,
		bytecodeCacheDirectory(cfg),
	), nil
}

// ProfileReport is the static bytecode report returned by Profile.
type ProfileReport = core.ProfileReport

// Profile compiles every executable router, cron, and queue entrypoint and
// returns immutable bytecode metrics without executing tenant code.
func Profile(configFile ...string) (core.ProfileReport, error) {
	cfg, err := commandConfig(configFile...)
	if err != nil {
		return core.ProfileReport{}, err
	}
	return core.ProfileWithLayout(cfg.Root, cfg.RootLayout), nil
}

func bytecodeCacheDirectory(cfg *Config) string {
	if cfg == nil || !cfg.BytecodeCache {
		return ""
	}
	if cfg.BytecodeCacheDir == "" {
		return filepath.Join(cfg.Root, ".kitwork", "cache", "bytecode")
	}
	if filepath.IsAbs(cfg.BytecodeCacheDir) {
		return cfg.BytecodeCacheDir
	}
	return filepath.Join(cfg.Root, cfg.BytecodeCacheDir)
}

func commandConfig(configFile ...string) (*Config, error) {
	file := ""
	if len(configFile) > 0 && configFile[0] != "" {
		file = configFile[0]
	} else {
		for _, candidate := range []string{"app.kitwork.js", "server.kitwork.js"} {
			if _, err := os.Stat(candidate); err == nil {
				file = candidate
				break
			}
		}
	}
	if file == "" {
		return nil, fmt.Errorf(
			"không tìm thấy manifest: cần app.kitwork.js (hoặc server.kitwork.js)",
		)
	}
	if !strings.HasSuffix(strings.ToLower(file), ".js") {
		return nil, fmt.Errorf(
			"engine command chỉ nhận manifest .kitwork.js, nhận %q",
			file,
		)
	}

	builder, err := evalServerBuilder(file)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate config %s: %w", file, err)
	}
	if builder.err != "" {
		return nil, fmt.Errorf(
			"failed to evaluate config %s: config validation error: %s",
			file,
			builder.err,
		)
	}
	if !builder.hasWeb && !builder.hasDatabase {
		return nil, fmt.Errorf(
			"failed to evaluate config %s: %w",
			file,
			noWebSurfaceErr(builder, file),
		)
	}
	raw, err := builderToMap(builder, file)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate config %s: %w", file, err)
	}
	cfg, err := ParseConfig(raw)
	if err != nil {
		return nil, fmt.Errorf("failed to process configuration: %w", err)
	}
	if err := resolveRootConfig(cfg); err != nil {
		return nil, fmt.Errorf("resolve app root: %w", err)
	}
	return cfg, nil
}

// printBanner renders the Kitwork startup banner: a brand-red "KITWORK" wordmark
// plus honest runtime facts (mode, listen address, TLS, databases). No fake metrics.
func printBanner(cfg *Config, isLocalhost, hasWeb bool) {
	const (
		red   = "\033[38;2;248;34;68m" // brand red #f82244
		dim   = "\033[2m"
		reset = "\033[0m"
	)
	label := func(name string) string {
		return fmt.Sprintf("  %s▸%s %s%-7s%s ", red, reset, dim, name, reset)
	}

	mode, root := rootLayoutLabel(cfg.RootLayout), cfg.Root

	fmt.Println("\n" + red + `█   █ █████ █████ █   █  ███  ████  █   █
█  █    █     █   █   █ █   █ █   █ █  █
███     █     █   █ █ █ █   █ ████  ███
█  █    █     █   ██ ██ █   █ █  █  █  █
█   █ █████   █   █   █  ███  █   █ █   █` + reset)
	fmt.Println(dim + "  sovereign logic engine\n" + reset)

	fmt.Printf("%s%s  %sroot:%s %s\n", label("mode"), mode, dim, reset, root)
	if hasWeb {
		fmt.Printf("%shttp://localhost:%d\n", label("listen"), cfg.Port)
		if cfg.AllowLocal || isLocalhost {
			fmt.Printf("%s%sdisabled (local dev)%s\n", label("tls"), dim, reset)
		} else {
			fmt.Printf("%sAutoSSL · :443\n", label("tls"))
		}
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
			fmt.Printf("%s%s · %s %s(%s)%s\n", label("db"), db.Type, db.Endpoint(), dim, alias, reset)
		}
	}
	for _, db := range cfg.AppDatabases {
		modes := []string{"native"}
		if db.KitSQL {
			modes = append(modes, "kitsql")
		}
		if db.Port != 0 {
			modes = append(modes, fmt.Sprintf("postgresql:%d", db.Port))
		}
		fmt.Printf("%skitdb · %s %s(%s)%s\n", label("db"), db.Path, dim, strings.Join(modes, ", "), reset)
	}
	fmt.Println()
}

func rootLayoutLabel(layout work.RootLayout) string {
	switch layout {
	case work.RootLayoutSingle:
		return "Single App"
	case work.RootLayoutMultiDomain:
		return "Multi-Domain App"
	case work.RootLayoutMultiTenant:
		return "Multi-Tenant"
	default:
		return "Legacy Auto"
	}
}
