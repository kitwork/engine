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
	"github.com/kitwork/engine/gateway"
	"github.com/kitwork/engine/host"
	hydrate "github.com/kitwork/engine/jit/hydrate"
	jitjs "github.com/kitwork/engine/jit/js"
	"github.com/kitwork/engine/logger"
	"github.com/kitwork/engine/proxies"
	"github.com/kitwork/engine/utilities/compress"
	"github.com/kitwork/engine/work"
)

func Run(configFile ...string) (err error) {
	// Lưu ý: KHÔNG nạp .env vào môi trường tiến trình toàn cục (sẽ làm mọi tenant
	// chung env, rò secret). env là SCOPED: host đọc root .env trong evalConfigJS;
	// mỗi tenant đọc .env riêng của nó (work.Tenant.Run → kitwork().env).

	// Manifest DUY NHẤT là một file chạy được: application.work (22/10/2026), hay app.kitwork.js,
	// server.kitwork.js (tên cũ, vẫn đọc khi đứng một mình). YAML/JSON KHÔNG nạp trực tiếp ở đây —
	// muốn dùng chúng thì trỏ từ manifest: server.run("config.kitwork.yaml").
	file := ""
	if len(configFile) > 0 && configFile[0] != "" {
		file = configFile[0]
	} else {
		found, findErr := FindManifest(".")
		if findErr != nil {
			return findErr
		}
		file = found
	}

	if !isManifestFile(file) {
		return fmt.Errorf("engine.Run chỉ nhận manifest application.work (hay .kitwork.js cũ), nhận %q — "+
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
	printBootNotices(file, builder)
	if builder.err != "" {
		return fmt.Errorf("failed to evaluate config %s: config validation error: %s", file, builder.err)
	}

	// DISPATCH THEO MANIFEST: khai báo là DỮ LIỆU, lệnh mới quyết định chạy gì. Không có web
	// surface thì cloud host không có gì để phục vụ — nếu app khai desktop/mobile thì đó là
	// hợp lệ (chạy shell tương ứng), không phải lỗi.
	if !builder.hasWeb && !builder.hasDatabase && !builder.hasGateway && !builder.hasProxy {
		if _, hasDesktop := builder.config["desktop"]; hasDesktop {
			fmt.Printf("%s khai báo app.desktop() nhưng không có website server — không có gì để phục vụ.\n"+
				"→ Chạy `kitwork-desktop` cho app desktop, hoặc thêm `app.website().run(env.PORT || 8080)` để phục vụ HTTP.\n", file)
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
	surfaces := make([]string, 0, 4)
	if builder.hasWeb {
		switch {
		case builder.hasWebsite:
			surfaces = append(surfaces, "app.website")
		case builder.ran:
			surfaces = append(surfaces, "app.run")
		default:
			surfaces = append(surfaces, "app.web (compatibility)")
		}
	}
	if builder.hasDatabase {
		surfaces = append(surfaces, "app.database")
	}
	if builder.hasGateway {
		surfaces = append(surfaces, "app.gateway")
	}
	if builder.hasProxy {
		surfaces = append(surfaces, "app.proxy")
	}
	surface := strings.Join(surfaces, " + ")
	fmt.Printf("Loaded configuration from %s (%s)\n", file, surface)

	cfg, err := ParseConfig(raw)
	if err != nil {
		return fmt.Errorf("failed to process configuration: %w", err)
	}
	cfg.Web = builder.hasWeb
	cfg.Minify = builder.minify
	cfg.SharedEnvironment = builder.sharedEnvironment
	if builder.hasWeb || builder.hasDatabase {
		if err := resolveRootConfig(cfg); err != nil {
			return fmt.Errorf("resolve app root: %w", err)
		}
		if err := validateSharedApps(cfg.SharedEnvironment, cfg.Root, cfg.RootLayout); err != nil {
			return err
		}
	} else {
		cfg.Root = "."
	}
	if err := validateGatewaySurface(builder.hasWeb, cfg.Gateway); err != nil {
		return err
	}
	if err := validatePrewarmSurface(builder.hasWeb, cfg.Prewarm); err != nil {
		return err
	}
	// The host's .database/ of app.database is declared relative to the manifest.
	manifestDirectory, err := filepath.Abs(filepath.Dir(file))
	if err != nil {
		return fmt.Errorf("resolve manifest directory: %w", err)
	}
	for index := range cfg.AppDatabases {
		if !filepath.IsAbs(cfg.AppDatabases[index].Path) {
			cfg.AppDatabases[index].Path = filepath.Join(manifestDirectory, cfg.AppDatabases[index].Path)
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

	applyDatabaseManifest(cfg)

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
	appDatabaseRuntimes, appDatabaseErrors, err := startAppDatabaseRuntimes(appDatabaseContext, cfg.AppDatabases,
		appDatabaseApps{root: cfg.Root, layout: cfg.RootLayout})
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

	var proxyManager *proxies.Manager
	var proxyErrors <-chan error
	if len(cfg.Proxies) > 0 {
		proxyManager, err = proxies.New(cfg.Proxies)
		if err != nil {
			return fmt.Errorf("configure app.proxy: %w", err)
		}
		proxyErrors = proxyManager.Errors()
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = proxyManager.Close(shutdownCtx)
		}()
	}

	if !builder.hasWeb && !builder.hasGateway {
		printBanner(cfg, host.IsLocalhost(), false)
		signalCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stopSignals()
		select {
		case <-signalCtx.Done():
			slog.Info("Shutdown signal received")
		case runErr := <-appDatabaseErrors:
			slog.Error("KitDB server stopped", "error", runErr)
			return runErr
		case runErr := <-proxyErrors:
			slog.Error("Forward proxy stopped", "error", runErr)
			return runErr
		}
		return closeAppDatabases()
	}

	// Pass global settings to the work package
	work.AllowLocal = cfg.AllowLocal
	// Authoring diagnostics are for a person at a keyboard, not for a production log.
	hydrate.Diagnostics = cfg.AllowLocal
	// The bridge and its capability proxies only reach anything inside a desktop or mobile shell.
	// A cloud host serving the same app over HTTP has no WebView handle, so it sends neither.
	_, hasDesktop := builder.config["desktop"]
	_, hasMobile := builder.config["mobile"]
	work.NativeShell = hasDesktop || hasMobile
	// The page JavaScript files are named by their bytes, so these two decide every name: minified
	// in production (or with MINIFY=true while developing: minifies), and the native bridge in the
	// kernel inside a shell. Templates and the heads of .ui pages minify by the same rule (work.Minify).
	jitjs.Minify = minifies(cfg)
	work.Minify = cfg.Minify
	jitjs.Native = work.NativeShell
	work.ServerPort = cfg.Port

	// Domain whitelist (for AutoSSL HostPolicy) + redirect rules (engine + :80 fallback).
	domain.Allows = append([]string(nil), cfg.Domains...)
	for _, target := range cfg.Gateway {
		if target.Domain != "" {
			domain.Allows = append(domain.Allows, target.Domain)
		}
	}
	domain.SitesDir = ""
	if builder.hasWeb {
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
	}
	domain.Configure(cfg.Canonical, cfg.Redirects)

	// Initialize and run the engine
	var handler *core.Engine
	if builder.hasWeb {
		handler = core.New(cfg.Root, cfg.MaxEnergy, cfg.HotReload, cfg.Hostname)
	} else {
		handler = core.NewGatewayHost(cfg.Hostname)
	}
	defer handler.Close()
	handler.SetWebEnabled(builder.hasWeb)
	handler.SetSharedEnvironment(cfg.SharedEnvironment)
	if builder.hasWeb {
		if err := handler.SetRootLayout(cfg.RootLayout); err != nil {
			return fmt.Errorf("configure app root layout: %w", err)
		}
	}
	var gatewayManager *gateway.Manager
	if len(cfg.Gateway) > 0 {
		gatewayManager, err = gateway.NewManager(cfg.Gateway)
		if err != nil {
			return fmt.Errorf("configure app.gateway: %w", err)
		}
		defer gatewayManager.Close()
		handler.SetGateway(gatewayManager)
	}
	if builder.hasWeb {
		if directory := bytecodeCacheDirectory(cfg); directory != "" {
			handler.SetBytecodeCache(directory)
		}
	}
	// Client-IP source: as the edge server Kitwork ignores X-Forwarded-For by default (spoofable);
	// trust_proxy: true opts in when running behind your own reverse proxy.
	work.TrustProxyHeaders = cfg.TrustProxy
	// The host's ceiling on a request body (app.web({ maxBody: "32mb" }), max_body): the server stops
	// reading past it, and no request.json({ limit }) asks for more (work/request_limits.go).
	if cfg.MaxBody > 0 {
		work.MaxBodyBytes = cfg.MaxBody
	}

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

	if builder.hasWeb {
		// Selected sites publish complete generations before readiness. Sites not
		// selected here retain request-driven cold loading.
		if err := prewarmConfiguredSites(handler, cfg); err != nil {
			return fmt.Errorf("prepare web runtime: %w", err)
		}

		// A schedule cannot wait for a request, so every app identity with .schedule/
		// or .shared/queue/ starts its owner eagerly after selected sites are ready.
		handler.StartAppSchedulers()
		for _, site := range work.DiscoverLegacySites(cfg.Root) {
			slog.Warn("Legacy site entry is ignored; migrate to router.kitwork.js", "site", site)
		}
	}

	// Transport compression wraps the whole handler once. A Kitwork page is markup-heavy — a real
	// site measured 174 KB uncompressed — and while the render costs microseconds, shipping those
	// bytes costs hundreds of milliseconds. The middleware leaves live streams, already-compressed
	// formats and tiny bodies alone; see utilities/compress.
	var baseHandler http.Handler = http.MaxBytesHandler(handler, work.MaxBodyBytes)
	srvHandler := compress.Middleware(baseHandler)
	var servers []*http.Server
	serverErrors := make(chan error, 2)
	if !host.IsLocalhost() && !cfg.AllowLocal {
		tlsConfig := domain.AutoSSL(cfg.Domains)

		httpsServer := &http.Server{
			Addr:              ":443",
			Handler:           srvHandler,
			TLSConfig:         tlsConfig,
			ReadHeaderTimeout: readHeaderTimeout,
		}
		servers = append(servers, httpsServer)
		go func() {
			serverErrors <- httpsServer.ListenAndServeTLS("", "")
		}()
	}

	printBanner(cfg, host.IsLocalhost(), true)

	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           srvHandler,
		ReadHeaderTimeout: readHeaderTimeout,
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
	case runErr = <-proxyErrors:
		slog.Error("Forward proxy stopped", "error", runErr)
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
// without opening a listener, publishing a generation, or starting a schedule.
func Check(configFile ...string) (core.CheckReport, error) {
	cfg, err := commandConfig(configFile...)
	if err != nil {
		return core.CheckReport{}, err
	}
	if err := validateAppDatabaseConfigs(cfg.AppDatabases); err != nil {
		return core.CheckReport{}, err
	}
	if !cfg.Web {
		return core.CheckReport{}, nil
	}
	for i := range cfg.Databases {
		dbConfig := cfg.Databases[i]
		alias := dbConfig.Alias
		if alias == "" {
			alias = "default"
		}
		database.Configs[alias] = dbConfig
	}
	applyDatabaseManifest(cfg)
	work.AllowLocal = cfg.AllowLocal
	return core.CheckWithEnvironment(
		cfg.Root,
		cfg.MaxEnergy,
		cfg.RootLayout,
		cfg.SharedEnvironment,
		bytecodeCacheDirectory(cfg),
	), nil
}

// applyDatabaseManifest hands the engine what application.work says an app's .shared/schema/ may reach
// (KITWORK_DATABASE.md mục 1): the app.databases catalogue, which connection("tên") picks from, and
// app.policy({ tenantConnections }), which turns connect() off, and app.history({ days }), how long the history
// of the apps' databases keeps. Run and Check both call it, so a check sees the databases the server would.
func applyDatabaseManifest(cfg *Config) {
	database.SetCatalog(cfg.DatabaseCatalog)
	work.SetTenantConnections(cfg.TenantConnections)
	work.SetHistoryDays(cfg.HistoryDays)
}

// ProfileReport is the static bytecode report returned by Profile.
type ProfileReport = core.ProfileReport

// Profile compiles every executable router, schedule, and queue entrypoint and
// returns immutable bytecode metrics without executing tenant code.
func Profile(configFile ...string) (core.ProfileReport, error) {
	cfg, err := commandConfig(configFile...)
	if err != nil {
		return core.ProfileReport{}, err
	}
	if !cfg.Web {
		return core.ProfileReport{}, nil
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

// manifestNames are the manifests a folder may hold, the new one first. Only one may be there.
var manifestNames = []string{"application.work", "app.kitwork.js", "server.kitwork.js"}

// ErrNoManifest is FindManifest's answer for a folder holding none of manifestNames.
var ErrNoManifest = errors.New("không tìm thấy manifest: cần application.work")

// FindManifest returns the one manifest in dir. application.work beside an old app.kitwork.js or
// server.kitwork.js stops the boot naming both: which one the author means is not the engine's guess.
// The native shells find theirs by the same rule (kitwork-desktop beside its executable, an app folder).
func FindManifest(dir string) (string, error) {
	var found []string
	for _, name := range manifestNames {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil && !info.IsDir() {
			found = append(found, name)
		}
	}
	switch len(found) {
	case 0:
		return "", ErrNoManifest
	case 1:
		return filepath.Join(dir, found[0]), nil
	}
	return "", fmt.Errorf("có %s cùng một thư mục: giữ một manifest, application.work là tên mới", strings.Join(found, " và "))
}

// isManifestFile reports whether file has a manifest's extension: .work, or .js for the old names.
func isManifestFile(file string) bool {
	lower := strings.ToLower(file)
	return strings.HasSuffix(lower, ".work") || strings.HasSuffix(lower, ".js")
}

func commandConfig(configFile ...string) (*Config, error) {
	file := ""
	if len(configFile) > 0 && configFile[0] != "" {
		file = configFile[0]
	} else {
		found, err := FindManifest(".")
		if err != nil {
			return nil, err
		}
		file = found
	}
	if !isManifestFile(file) {
		return nil, fmt.Errorf(
			"engine command chỉ nhận manifest application.work (hay .kitwork.js cũ), nhận %q",
			file,
		)
	}

	builder, err := evalServerBuilder(file)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate config %s: %w", file, err)
	}
	printBootNotices(file, builder)
	if builder.err != "" {
		return nil, fmt.Errorf(
			"failed to evaluate config %s: config validation error: %s",
			file,
			builder.err,
		)
	}
	if !builder.hasWeb && !builder.hasDatabase && !builder.hasGateway && !builder.hasProxy {
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
	cfg.Web = builder.hasWeb
	cfg.SharedEnvironment = builder.sharedEnvironment
	if err := validateGatewaySurface(builder.hasWeb, cfg.Gateway); err != nil {
		return nil, err
	}
	if err := validatePrewarmSurface(builder.hasWeb, cfg.Prewarm); err != nil {
		return nil, err
	}
	if builder.hasWeb || builder.hasDatabase {
		if err := resolveRootConfig(cfg); err != nil {
			return nil, fmt.Errorf("resolve app root: %w", err)
		}
		if err := validateSharedApps(cfg.SharedEnvironment, cfg.Root, cfg.RootLayout); err != nil {
			return nil, err
		}
	} else {
		cfg.Root = "."
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
		fmt.Printf("%sdatabase · %s %s(postgresql %s:%d, SQLite, read-only)%s\n", label("db"), db.Path, dim, db.Host, db.Port, reset)
	}
	for _, proxy := range cfg.Proxies {
		hostname := proxy.Host
		if hostname == "" {
			hostname = "127.0.0.1"
		}
		protocol := proxy.Protocol
		if protocol == "" {
			protocol = "http"
		}
		fmt.Printf("%s%s · %s:%d\n", label("proxy"), protocol, hostname, proxy.Port)
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

// minifies reports whether the engine minifies what it serves (the page JavaScript files, the HTML of
// template pages, the head of a .ui page): always in production, and while developing (AllowLocal)
// when MINIFY=true asks for production's bytes (14/10/2026). ALLOW_LOCAL must stay true on a local
// machine (no ACME, no TLS), so before MINIFY a local run could never show what production serves.
// Only minification follows it: a .ui page's warnings and every other part of developing stay.
func minifies(cfg *Config) bool {
	return cfg != nil && (!cfg.AllowLocal || cfg.Minify)
}

// readHeaderTimeout is how long the server waits for a request's headers: a client that sends them a
// byte at a time does not hold a connection open forever (Slowloris).
const readHeaderTimeout = 10 * time.Second
