package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/manifest"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
	"github.com/kitwork/engine/work"
	"gopkg.in/yaml.v3"
)

// loadReferencedConfig nạp file config mà server.run("path") trỏ tới: .js (đệ quy
// qua evalConfigJS), .json hoặc .yaml/.yml → map[string]interface{} cho ParseConfig.
func loadReferencedConfig(path string) (map[string]interface{}, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".js" {
		return evalConfigJS(path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("server.run: %w", err)
	}
	expanded := []byte(os.ExpandEnv(string(data)))
	raw := map[string]interface{}{}
	switch ext {
	case ".json":
		err = json.Unmarshal(expanded, &raw)
	case ".yaml", ".yml":
		err = yaml.Unmarshal(expanded, &raw)
	default:
		return nil, fmt.Errorf("server.run: unsupported config file %q (cần .js/.json/.yaml)", path)
	}
	if err != nil {
		return nil, fmt.Errorf("server.run: parse %s: %w", path, err)
	}
	return raw, nil
}

// evalConfigJS chạy một file bootstrap (vd server.kitwork.js) trong một VM SETUP
// tối giản — chỉ có builtin `server` + `env`, KHÔNG có router/database/runtime của
// tenant — rồi trả về object config được truyền vào server.run({...}) dưới dạng
// map[string]interface{}, sẵn sàng cho ParseConfig (giống hệt nguồn .json/.yaml).
//
// `env` ở đây là env CẤP HOST (root .env + biến môi trường thật). Mỗi TENANT có
// `env` riêng, cô lập theo path, dựng trong work.Tenant.Run() (kitwork().env).
//
// An toàn để thực thi vì subset Kitwork cấm `while` + có gas → không thể treo.
// ServerBuilder captures the app manifest: what it owns, which surfaces it
// exposes, and the server started by app.run(port). Legacy app.web(...) and
// server.run(...) declarations remain compatibility paths.
type ServerBuilder struct {
	config     map[string]value.Value
	ran        bool // app.run() or its legacy server.run() alias was called
	hasWeb     bool // an HTTP server is declared by app.run() or legacy app.web()
	hasWebsite bool // the canonical app.website() surface was declared
	hasGateway bool // a GATEWAY surface is declared with app.gateway(target, ...)
	hasProxy   bool // forward-proxy listeners declared with app.proxy(config, ...)
	path       string
	err        string
	unknown    []string // manifest methods the chain called that this engine does not have, each once
	// connections are the external connections the chain declared, as written (printBootNotices).
	connections []manifestConnection
	// hasDatabase: an owned KitDB root is declared with app.database(path, options) — the DATABASE
	// surface (the app offers a database, as app.website offers HTTP). This build has no KitDB runtime
	// to serve it; the declaration is data the tool reads, exactly like app.desktop on the cloud host.
	hasDatabase bool
	// minify is MINIFY=true of the host env (root .env or the real environment): the engine minifies
	// what it serves as production does while ALLOW_LOCAL stays true (Config.Minify, minifies).
	minify bool
}

// MissingMember is called for a manifest method this engine does not have — app.postgresql() on a
// build that predates it, a typo, a method from a newer engine. The chain must not break: the name
// is recorded and the builder itself is returned, so app.website() after it still lands and the boot
// prints one line naming the method instead of "declares no surface", which pointed at the wrong
// line entirely.
func (b *ServerBuilder) MissingMember(name string) value.Value {
	return value.NewFunc(func(args ...value.Value) value.Value {
		for _, known := range b.unknown {
			if known == name {
				return value.New(b) // named once however many times the chain calls it
			}
		}
		b.unknown = append(b.unknown, name)
		return value.New(b)
	})
}

// unknownMethodWarning names the manifest methods the chain called that this engine does not have.
func (b *ServerBuilder) unknownMethodWarning() string {
	if len(b.unknown) == 0 {
		return ""
	}
	return "app." + strings.Join(b.unknown, "(), app.") + "() is not a method of this engine's manifest — ignored; everything else on the chain still applies"
}

func NewServerBuilder() *ServerBuilder {
	return &ServerBuilder{
		config: make(map[string]value.Value),
	}
}

func coerceToNumeric(v value.Value) value.Value {
	if v.K == value.String {
		if f, err := strconv.ParseFloat(v.Text(), 64); err == nil {
			return value.New(f)
		}
	}
	return v
}

// Port is the legacy way to declare the website server port. New manifests use app.run(port).
func (b *ServerBuilder) Port(v value.Value) *ServerBuilder {
	b.config["port"] = coerceToNumeric(v)
	b.hasWeb = true
	return b
}

// Web declares the WEB surface — app.web({ port, hostname, rateLimit, allowLocal, trustProxy,
// canonical, redirects, domains }). It flattens into the same flat keys ParseConfig already reads, so
// the whole host pipeline is untouched. Omitting it is NOT an error: it simply means this app has no
// web surface (e.g. a desktop-only app). app.web(8080) is shorthand for just the port.
func (b *ServerBuilder) Web(args ...value.Value) *ServerBuilder {
	b.hasWeb = true
	if len(args) == 0 {
		return b
	}
	v := args[0]
	if v.IsNumeric() || v.K == value.String {
		b.config["port"] = coerceToNumeric(v)
		return b
	}
	if !v.IsMap() {
		return b
	}
	for k, val := range v.Map() {
		switch k {
		case "port":
			b.config["port"] = coerceToNumeric(val)
		case "rateLimit":
			b.config["rate_limit"] = val
		case "allowLocal":
			b.config["allow_local"] = val
		case "trustProxy":
			b.config["trust_proxy"] = val
		case "hotReload":
			b.config["hot_reload"] = val
		case "bytecodeCache":
			b.config["bytecode_cache"] = val
		case "bytecodeCacheDir":
			b.config["bytecode_cache_dir"] = val
		case "domain":
			b.config["domains"] = val
		default:
			// hostname / canonical / redirects / domains / rate_limit / allow_local … pass straight
			// through — ParseConfig already knows these keys.
			b.config[k] = val
		}
	}
	return b
}

// Website declares the canonical website surface. A string is the primary
// hostname; port belongs to the terminal app.run(port) operation. The object
// form accepts the existing host policies while deliberately rejecting port so
// the new API has one unambiguous place for server startup.
func (b *ServerBuilder) Website(args ...value.Value) *ServerBuilder {
	b.hasWebsite = true
	if len(args) > 1 {
		b.err = "app.website expects no arguments, a hostname string, or one options object"
		return b
	}
	if len(args) == 0 {
		return b
	}

	v := args[0]
	if v.K == value.String {
		hostname, err := normalizeWebsiteHostname(v.Text())
		if err != nil {
			b.err = err.Error()
			return b
		}
		b.config["hostname"] = value.New(hostname)
		return b
	}
	if !v.IsMap() {
		b.err = "app.website expects a hostname string or options object; pass the port to app.run(port)"
		return b
	}
	if _, configured := v.Map()["port"]; configured {
		b.err = "app.website does not accept port; pass it to app.run(port)"
		return b
	}
	for key, item := range v.Map() {
		switch key {
		case "hostname":
			if item.K != value.String {
				b.err = "app.website hostname must be a string"
				return b
			}
			hostname, err := normalizeWebsiteHostname(item.Text())
			if err != nil {
				b.err = err.Error()
				return b
			}
			b.config["hostname"] = value.New(hostname)
		case "rateLimit":
			b.config["rate_limit"] = item
		case "allowLocal":
			b.config["allow_local"] = item
		case "trustProxy":
			b.config["trust_proxy"] = item
		case "hotReload":
			b.config["hot_reload"] = item
		case "bytecodeCache":
			b.config["bytecode_cache"] = item
		case "bytecodeCacheDir":
			b.config["bytecode_cache_dir"] = item
		case "domain":
			b.config["domains"] = item
		default:
			b.config[key] = item
		}
	}
	return b
}

func normalizeWebsiteHostname(source string) (string, error) {
	hostname := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(source, ".")))
	if hostname == "" {
		return "", fmt.Errorf("app.website hostname cannot be empty")
	}
	if strings.ContainsAny(hostname, "/\\:@ ") || strings.Contains(hostname, "*") {
		return "", fmt.Errorf("app.website hostname %q is invalid", source)
	}
	return hostname, nil
}

// Prewarm selects web generations that must be prepared before the host is
// ready. No arguments means the primary hostname, "*" means every discovered
// site, and false explicitly keeps request-driven cold loading.
func (b *ServerBuilder) Prewarm(domains ...value.Value) *ServerBuilder {
	if _, declared := b.config["prewarm"]; declared {
		b.err = "app.prewarm may be declared only once"
		return b
	}
	if len(domains) == 0 {
		b.config["prewarm"] = value.TRUE
		return b
	}
	if len(domains) == 1 && domains[0].K == value.Bool {
		b.config["prewarm"] = domains[0]
		return b
	}

	configured := make([]value.Value, 0, len(domains))
	for index, domain := range domains {
		if domain.K != value.String {
			b.err = fmt.Sprintf("app.prewarm argument %d must be a domain string", index+1)
			return b
		}
		configured = append(configured, domain)
	}
	b.config["prewarm"] = value.Value{K: value.Array, V: &configured}
	return b
}

// Gateway declares operator-controlled reverse-proxy targets. Each argument is
// one target: { domain?, target, weight?, health? }. Repeated calls append to
// the same manifest collection.
func (b *ServerBuilder) Gateway(targets ...value.Value) *ServerBuilder {
	b.hasGateway = true
	if len(targets) == 0 {
		b.err = "app.gateway expects at least one target object"
		return b
	}

	var current []value.Value
	if configured, ok := b.config["gateway"]; ok && configured.K == value.Array {
		current = append(current, (*configured.V.(*[]value.Value))...)
	}
	for index, target := range targets {
		if !target.IsMap() {
			b.err = fmt.Sprintf("app.gateway target %d must be an object", index+1)
			return b
		}
		current = append(current, target)
	}
	b.config["gateway"] = value.Value{K: value.Array, V: &current}
	return b
}

// Proxy declares host-owned HTTP or SOCKS5 forward-proxy listeners. Each
// argument is one listener object; repeated calls append to the collection.
func (b *ServerBuilder) Proxy(listeners ...value.Value) *ServerBuilder {
	b.hasProxy = true
	if len(listeners) == 0 {
		b.err = "app.proxy expects at least one listener object"
		return b
	}

	var current []value.Value
	if configured, ok := b.config["proxy"]; ok && configured.K == value.Array {
		current = append(current, (*configured.V.(*[]value.Value))...)
	}
	for index, listener := range listeners {
		if !listener.IsMap() {
			b.err = fmt.Sprintf("app.proxy listener %d must be an object", index+1)
			return b
		}
		current = append(current, listener)
	}
	b.config["proxy"] = value.Value{K: value.Array, V: &current}
	return b
}

// Mobile declares the MOBILE surface (iOS/Android shell). The raw value stays untouched for legacy
// compatibility; native build tools opt into versioned validation through ReadMobileManifest. The
// cloud host and desktop shell ignore it.
func (b *ServerBuilder) Mobile(v value.Value) *ServerBuilder {
	b.config["mobile"] = v
	return b
}

// Title remains the current mobile identity field and a compatibility fallback
// for older desktop tooling. Canonical desktop identity lives in app.desktop.
// A web page's <title> is per-page metadata and unrelated to this value.
func (b *ServerBuilder) Title(v value.Value) *ServerBuilder {
	b.config["title"] = v
	return b
}

// Icon remains the current mobile icon and a compatibility fallback for older
// desktop tooling. Canonical desktop identity lives in app.desktop.
func (b *ServerBuilder) Icon(v value.Value) *ServerBuilder {
	b.config["icon"] = v
	return b
}

func (b *ServerBuilder) Root(v value.Value) *ServerBuilder {
	b.config["root"] = v
	return b
}

func (b *ServerBuilder) Hostname(v value.Value) *ServerBuilder {
	b.config["hostname"] = v
	return b
}

func (b *ServerBuilder) HotReload(v value.Value) *ServerBuilder {
	b.config["hot_reload"] = v
	return b
}

func (b *ServerBuilder) AllowLocal(v value.Value) *ServerBuilder {
	b.config["allow_local"] = v
	return b
}

func (b *ServerBuilder) Redirects(v value.Value) *ServerBuilder {
	b.config["redirects"] = v
	return b
}

func (b *ServerBuilder) Canonical(v value.Value) *ServerBuilder {
	b.config["canonical"] = v
	return b
}

func (b *ServerBuilder) RateLimit(v value.Value) *ServerBuilder {
	b.config["rate_limit"] = v
	return b
}

// TrustProxy: believe X-Forwarded-For/X-Real-IP for the client IP. Enable ONLY when Kitwork runs
// behind a reverse proxy you control — as the edge server those headers are client-spoofable.
func (b *ServerBuilder) TrustProxy(v value.Value) *ServerBuilder {
	b.config["trust_proxy"] = v
	return b
}

func (b *ServerBuilder) Databases(v value.Value) *ServerBuilder {
	b.config["databases"] = v
	return b
}

func (b *ServerBuilder) Database(values ...value.Value) *ServerBuilder {
	if len(values) == 0 || len(values) > 2 {
		b.err = "app.database expects an owned database object, a legacy connection object, or (path, options)"
		return b
	}
	if len(values) == 1 && values[0].IsMap() {
		if path, owned := values[0].Map()["path"]; owned {
			if path.K != value.String || strings.TrimSpace(path.Text()) == "" {
				b.err = "app.database path must be a non-empty string"
				return b
			}
			var current []value.Value
			if configured, ok := b.config["owned_databases"]; ok && configured.K == value.Array {
				current = append(current, (*configured.V.(*[]value.Value))...)
			}
			current = append(current, values[0])
			b.config["owned_databases"] = value.Value{K: value.Array, V: &current}
			b.hasDatabase = true
			return b
		}
	}
	if values[0].K == value.String {
		if strings.TrimSpace(values[0].Text()) == "" {
			b.err = "app.database path cannot be empty"
			return b
		}
		options := map[string]value.Value{"path": values[0]}
		if len(values) == 2 {
			if !values[1].IsMap() {
				b.err = "app.database options must be an object"
				return b
			}
			for key, item := range values[1].Map() {
				options[key] = item
			}
		}
		var current []value.Value
		if configured, ok := b.config["owned_databases"]; ok && configured.K == value.Array {
			current = *configured.V.(*[]value.Value)
		}
		current = append(current, value.New(options))
		b.config["owned_databases"] = value.Value{K: value.Array, V: &current}
		b.hasDatabase = true
		return b
	}
	if len(values) != 1 {
		b.err = "app.database(path, options) requires a string path"
		return b
	}
	v := values[0]
	var current []value.Value
	if d, ok := b.config["databases"]; ok && d.K == value.Array {
		current = *d.V.(*[]value.Value)
	}
	current = append(current, v)
	b.config["databases"] = value.Value{K: value.Array, V: &current}
	return b
}

// Connect declares an external connector. A URL supplies its driver through
// the scheme; an options object must declare type or driver explicitly.
func (b *ServerBuilder) Connect(values ...value.Value) *ServerBuilder {
	return b.addExternalConnection("", "app.connect", values...)
}

// Postgresql declares an external PostgreSQL connection.
func (b *ServerBuilder) Postgresql(values ...value.Value) *ServerBuilder {
	return b.addExternalConnection("postgres", "app.postgresql", values...)
}

// Mysql declares an external MySQL connection.
func (b *ServerBuilder) Mysql(values ...value.Value) *ServerBuilder {
	return b.addExternalConnection("mysql", "app.mysql", values...)
}

// Sqlite declares a host-owned SQLite compatibility connection.
func (b *ServerBuilder) Sqlite(values ...value.Value) *ServerBuilder {
	return b.addExternalConnection("sqlite", "app.sqlite", values...)
}

// Redis declares an external Redis connection. Runtime availability depends
// on the distribution registering a Redis connector.
func (b *ServerBuilder) Redis(values ...value.Value) *ServerBuilder {
	return b.addExternalConnection("redis", "app.redis", values...)
}

func (b *ServerBuilder) addExternalConnection(provider, method string, values ...value.Value) *ServerBuilder {
	if len(values) == 1 && values[0].IsMap() {
		alias, ok := values[0].Map()["alias"]
		if !ok || alias.K != value.String || strings.TrimSpace(alias.Text()) == "" {
			b.err = method + " object requires a non-empty alias"
			return b
		}
		values = []value.Value{alias, values[0]}
	}
	if len(values) != 2 || values[0].K != value.String {
		b.err = method + " expects ({ alias, ...options }) or (alias, urlOrOptions)"
		return b
	}
	alias := strings.TrimSpace(values[0].Text())
	if alias == "" {
		b.err = method + " alias cannot be empty"
		return b
	}
	connection := map[string]value.Value{"alias": value.New(alias)}
	source := values[1]
	switch {
	case source.K == value.String:
		text := strings.TrimSpace(source.Text())
		if text == "" {
			b.err = method + " URL or path cannot be empty"
			return b
		}
		if provider == "sqlite" {
			connection["name"] = value.New(text)
		} else {
			connection["url"] = value.New(text)
			detected, err := connectionTypeFromURL(text)
			if err != nil {
				b.err = method + ": " + err.Error()
				return b
			}
			if provider == "" {
				provider = detected
			} else if detected != provider {
				b.err = method + " URL scheme does not match its provider"
				return b
			}
		}
	case source.IsMap():
		for key, item := range source.Map() {
			connection[key] = item
		}
		if configured, found := connection["alias"]; found && (configured.K != value.String || strings.TrimSpace(configured.Text()) != alias) {
			b.err = method + " options cannot replace its alias"
			return b
		}
		connection["alias"] = value.New(alias)
		configuredProvider := ""
		for _, key := range []string{"type", "driver"} {
			if item, found := connection[key]; found {
				if item.K != value.String || strings.TrimSpace(item.Text()) == "" {
					b.err = method + " " + key + " must be a non-empty string"
					return b
				}
				configuredProvider = canonicalConnectionType(item.Text())
				break
			}
		}
		urlProvider := ""
		if item, found := connection["url"]; found {
			if item.K != value.String || strings.TrimSpace(item.Text()) == "" {
				b.err = method + " url must be a non-empty string"
				return b
			}
			var err error
			urlProvider, err = connectionTypeFromURL(item.Text())
			if err != nil {
				b.err = method + ": " + err.Error()
				return b
			}
		}
		if provider == "" {
			provider = configuredProvider
			if provider == "" {
				provider = urlProvider
			}
			if provider == "" {
				b.err = method + " options require type, driver, or url"
				return b
			}
		} else if configuredProvider != "" && configuredProvider != provider {
			b.err = method + " options cannot replace its provider"
			return b
		}
		if urlProvider != "" && urlProvider != provider {
			b.err = method + " URL scheme does not match its provider"
			return b
		}
	default:
		b.err = method + " URL or options must be a string or object"
		return b
	}
	provider = canonicalConnectionType(provider)
	connection["type"] = value.New(provider)
	delete(connection, "driver")
	var current []value.Value
	if configured, ok := b.config["databases"]; ok && configured.K == value.Array {
		current = *configured.V.(*[]value.Value)
	}
	current = append(current, value.New(connection))
	b.config["databases"] = value.Value{K: value.Array, V: &current}
	b.connections = append(b.connections, manifestConnection{method: method, alias: alias, provider: provider})
	return b
}

func connectionTypeFromURL(source string) (string, error) {
	separator := strings.Index(source, "://")
	if separator < 1 {
		return "", fmt.Errorf("URL must include a connector scheme")
	}
	provider := canonicalConnectionType(source[:separator])
	if provider == "" {
		return "", fmt.Errorf("URL connector scheme cannot be empty")
	}
	return provider, nil
}

func canonicalConnectionType(source string) string {
	switch provider := strings.ToLower(strings.TrimSpace(source)); provider {
	case "postgresql", "postgres":
		return "postgres"
	case "sqlite3", "sqlite":
		return "sqlite"
	case "rediss", "redis":
		return "redis"
	default:
		return provider
	}
}

func (b *ServerBuilder) Logger(v value.Value) *ServerBuilder {
	b.config["logger"] = v
	return b
}

// Desktop declares one native desktop surface. The canonical API is flat:
// app.desktop({ name, icon, width, height, native, ... }). Compatibility
// mirrors keep the previous top-level title/icon and nested chrome/window
// representation available to existing desktop tools.
func (b *ServerBuilder) Desktop(v value.Value) *ServerBuilder {
	if !v.IsMap() {
		b.config["desktop"] = v // app.desktop(true) — surface declared, all defaults
		return b
	}
	options := v.Map()
	if _, position := options["position"]; position {
		if _, center := options["center"]; center {
			b.err = "app.desktop cannot combine position with center"
			return b
		}
	}
	if _, native := options["native"]; native {
		if _, chrome := options["chrome"]; chrome {
			b.err = "app.desktop cannot combine native with legacy chrome"
			return b
		}
	}
	if _, window := options["window"]; window {
		for _, key := range []string{"width", "height", "minWidth", "minHeight", "resizable", "center", "maximized"} {
			if _, flat := options[key]; flat {
				b.err = "app.desktop cannot combine flat window options with legacy window"
				return b
			}
		}
	}
	for key, item := range options {
		if !b.applyDesktopOption(key, item) {
			return b
		}
	}
	if desktop, ok := b.config["desktop"]; ok && desktop.IsMap() {
		if name, named := desktop.Map()["name"]; named {
			if _, titled := desktop.Map()["title"]; !titled {
				b.setDesktop("title", name)
			}
		}
		// Normalize compatibility identity after all map entries are read. Go map
		// iteration is intentionally unordered, so an explicit title must win over
		// the name-derived default regardless of source-object traversal order.
		desktop = b.config["desktop"]
		if title, titled := desktop.Map()["title"]; titled {
			b.config["title"] = title
		}
		if icon, configured := desktop.Map()["icon"]; configured {
			b.config["icon"] = icon
		}
	}
	return b
}

func (b *ServerBuilder) applyDesktopOption(key string, item value.Value) bool {
	switch key {
	case "material":
		if item.K != value.String || (item.Text() != "none" && item.Text() != "glass" && item.Text() != "mica") {
			b.err = "desktop.material must be none, glass or mica"
			return false
		}
		b.setDesktop(key, item)
	case "background", "color":
		if err := validateDesktopColor(item); err != nil {
			b.err = "desktop." + key + ": " + err.Error()
			return false
		}
		b.setDesktop(key, item)
	case "bind":
		if !item.IsMap() {
			b.err = "desktop.bind must be an object"
			return false
		}
		budget := 10000
		if err := validateSplashData(item, 0, &budget); err != nil {
			b.err = "desktop.bind: " + err.Error()
			return false
		}
		b.setDesktop(key, item)
	case "splash", "offline", "launch":
		if _, exists := b.config[key]; exists {
			b.err = "cannot combine desktop." + key + " with app." + key
			return false
		}
		if desktop, ok := b.config["desktop"]; ok && desktop.IsMap() {
			if _, exists := desktop.Map()[key]; exists {
				b.err = "desktop." + key + " may only be declared once"
				return false
			}
		}
		var normalized value.Value
		var err error
		if key != "launch" {
			normalized, err = normalizeSplashOption(item, key)
		}
		if key == "launch" {
			normalized, err = normalizeLaunchOption(item)
		}
		if err != nil {
			b.err = "desktop." + key + ": " + err.Error()
			return false
		}
		b.setDesktop(key, normalized)
	case "name", "title", "icon":
		if item.K != value.String || strings.TrimSpace(item.Text()) == "" {
			b.err = "app.desktop " + key + " must be a non-empty string"
			return false
		}
		b.setDesktop(key, item)
		// Existing native tools read shared identity fields. Keep those fields as
		// fallbacks while the canonical ownership moves into the desktop surface.
		compatibilityKey := key
		if key == "name" {
			compatibilityKey = "title"
		}
		if _, explicit := b.config[compatibilityKey]; !explicit {
			b.config[compatibilityKey] = item
		}
	case "position":
		if err := validateDesktopPosition(item); err != nil {
			b.err = "app.desktop position: " + err.Error()
			return false
		}
		b.setDesktop(key, item)
	case "native":
		if item.K != value.Bool {
			b.err = "app.desktop native must be a boolean"
			return false
		}
		b.setDesktop("native", item)
		chrome := "html"
		if item.IsTrue() {
			chrome = "native"
		}
		b.setDesktop("chrome", value.New(chrome))
	case "width", "height", "minWidth", "minHeight":
		if !item.IsNumeric() || item.N <= 0 {
			b.err = "app.desktop " + key + " must be a positive number"
			return false
		}
		b.setDesktop(key, item)
		b.setDesktopWindow(key, item)
	case "resizable", "center", "maximized":
		if item.K != value.Bool {
			b.err = "app.desktop " + key + " must be a boolean"
			return false
		}
		b.setDesktop(key, item)
		b.setDesktopWindow(key, item)
	case "chrome":
		b.setDesktop(key, item)
		if item.K == value.String {
			switch item.Text() {
			case "native":
				b.setDesktop("native", value.TRUE)
			case "html":
				b.setDesktop("native", value.FALSE)
			}
		}
	case "window":
		if !item.IsMap() {
			b.err = "app.desktop window must be an object"
			return false
		}
		b.setDesktop(key, item)
		for windowKey, windowValue := range item.Map() {
			b.setDesktop(windowKey, windowValue)
		}
	default:
		b.setDesktop(key, item)
	}
	return true
}

// setDesktop merges ONE key into the desktop surface block, creating the block if needed. This is what
// makes both config styles build the exact same manifest.
func (b *ServerBuilder) setDesktop(key string, v value.Value) {
	m := map[string]value.Value{}
	if existing, ok := b.config["desktop"]; ok && existing.IsMap() {
		for k, val := range existing.Map() {
			m[k] = val
		}
	}
	m[key] = v
	b.config["desktop"] = value.New(m)
}

func (b *ServerBuilder) setDesktopWindow(key string, v value.Value) {
	window := map[string]value.Value{}
	if desktop, ok := b.config["desktop"]; ok && desktop.IsMap() {
		if current, ok := desktop.Map()["window"]; ok && current.IsMap() {
			for currentKey, currentValue := range current.Map() {
				window[currentKey] = currentValue
			}
		}
	}
	window[key] = v
	b.setDesktop("window", value.New(window))
}

// Chrome is flat sugar for app.desktop({ chrome }) — the window chrome: "html" | "native" | "auto".
func (b *ServerBuilder) Chrome(v value.Value) *ServerBuilder {
	b.setDesktop("chrome", v)
	return b
}

// Window is flat sugar for app.desktop({ window }) — { width, height, minWidth, minHeight, maximized }.
func (b *ServerBuilder) Window(v value.Value) *ServerBuilder {
	b.setDesktop("window", v)
	return b
}

// Run is the terminal server operation. app.run(8080) is the canonical form;
// the object and referenced-file forms remain for legacy server.run(...)
// manifests. A website hostname is an implicit readiness target unless the
// manifest explicitly selected another prewarm policy.
func (b *ServerBuilder) Run(args ...value.Value) value.Value {
	if b.ran {
		b.err = "app.run may be called only once"
		return value.New(b.err)
	}
	if len(args) > 1 {
		b.err = "app.run expects no arguments or one port"
		return value.New(b.err)
	}
	b.ran = true
	b.hasWeb = true
	if len(args) > 0 {
		arg := args[0]
		if arg.K == value.String {
			text := arg.Text()
			lower := strings.ToLower(text)
			if strings.HasSuffix(lower, ".json") || strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml") || strings.HasSuffix(lower, ".js") {
				b.path = text
			} else if f, err := strconv.ParseFloat(text, 64); err == nil {
				// Shorthand string port: server.run("8080")
				b.config["port"] = value.New(f)
			} else {
				// Fallback to treat other string as path
				b.path = text
			}
		} else if arg.IsNumeric() {
			// Shorthand numeric port: server.run(8080)
			b.config["port"] = arg
		} else {
			// Legacy object config: server.run({ port: 8080 })
			b.config = make(map[string]value.Value)
			if arg.IsMap() {
				for k, val := range arg.Map() {
					b.config[k] = val
				}
			}
		}
	}

	// Validate config fields early
	if portVal, ok := b.config["port"]; ok {
		portNum := coerceToNumeric(portVal)
		if portNum.K == value.Number {
			if portNum.N < 1 || portNum.N > 65535 {
				b.err = fmt.Sprintf("invalid port number: %v (must be 1-65535)", portNum.N)
				return value.New(b.err)
			}
			b.config["port"] = portNum
		} else {
			b.err = fmt.Sprintf("invalid port value type: %s", portVal.K.String())
			return value.New(b.err)
		}
	}
	if rootVal, ok := b.config["root"]; ok {
		if rootVal.K == value.String && rootVal.Text() == "" {
			b.err = "invalid root directory: cannot be empty"
			return value.New(b.err)
		}
	}
	if b.hasWebsite {
		if _, explicit := b.config["prewarm"]; !explicit {
			if hostname, configured := b.config["hostname"]; configured && hostname.K == value.String && strings.TrimSpace(hostname.Text()) != "" {
				b.config["prewarm"] = value.TRUE
			}
		}
	}

	return value.Value{K: value.Nil}
}

// evalConfigJS chạy một file bootstrap (vd server.kitwork.js) trong một VM SETUP
// tối giản — chỉ có builtin `server` + `env`, KHÔNG có router/database/runtime của
// tenant — rồi trả về object config được truyền vào server.run({...}) dưới dạng
// map[string]interface{}, sẵn sàng cho ParseConfig (giống hệt nguồn .json/.yaml).
//
// `env` ở đây là env CẤP HOST (root .env + biến môi trường thật). Mỗi TENANT có
// `env` riêng, cô lập theo path, dựng trong work.Tenant.Run() (kitwork().env).
//
// An toàn để thực thi vì subset Kitwork cấm `while` + có gas → không thể treo.
func evalConfigJS(file string) (map[string]interface{}, error) {
	builder, err := evalServerBuilder(file)
	if err != nil {
		return nil, err
	}
	printBootNotices(file, builder)
	if builder.err != "" {
		return nil, fmt.Errorf("config validation error: %s", builder.err)
	}
	if !builder.hasWeb && !builder.hasDatabase && !builder.hasGateway && !builder.hasProxy {
		return nil, noWebSurfaceErr(builder, file)
	}
	return builderToMap(builder, file)
}

// noWebSurfaceErr explains a manifest that declares no WEB surface. A desktop/mobile-only manifest is
// perfectly valid — it just has nothing for the cloud host to serve — so say that instead of implying
// the file is broken.
func noWebSurfaceErr(b *ServerBuilder, file string) error {
	if b.hasWebsite && !b.ran {
		return fmt.Errorf("%s declares app.website() but does not start its server: finish the manifest with `app.run(port)`", filepath.Base(file))
	}
	_, hasDesktop := b.config["desktop"]
	_, hasMobile := b.config["mobile"]
	if hasDesktop || hasMobile {
		surface := "app.desktop()"
		if hasMobile && !hasDesktop {
			surface = "app.mobile()"
		}
		return fmt.Errorf("%s declares %s but no website server: the cloud host has nothing to serve.\n"+
			"Add `app.website().run(env.PORT || 8080)` to also serve HTTP — or run `kitwork-desktop`, which needs no website server.",
			filepath.Base(file), surface)
	}
	return fmt.Errorf("%s declares no surface: add `app.website().run(8080)` to serve HTTP, and/or `app.desktop({...})` for a desktop app.\n"+
		"(Legacy `server.run(8080)` still works.)", filepath.Base(file))
}

// builderToMap turns the captured manifest into the flat map ParseConfig reads — or loads the file a
// legacy server.run("cfg.yaml") pointed at.
func builderToMap(b *ServerBuilder, file string) (map[string]interface{}, error) {
	if b.path != "" {
		// server.run("path"): nạp file config được trỏ tới (tương đối so với file này).
		p := b.path
		if !filepath.IsAbs(p) {
			p = filepath.Join(filepath.Dir(file), p)
		}
		return loadReferencedConfig(p)
	}
	// value.Value → map[string]interface{} qua MarshalJSON có sẵn của value.
	jsonBytes, err := json.Marshal(value.New(b.config))
	if err != nil {
		return nil, fmt.Errorf("config serialize error: %w", err)
	}
	raw := make(map[string]interface{})
	if err := json.Unmarshal(jsonBytes, &raw); err != nil {
		return nil, fmt.Errorf("config decode error: %w", err)
	}
	return raw, nil
}

// ReadManifest evaluates a manifest (app.kitwork.js) and returns EVERYTHING it declares as a raw map:
// app-level identity (title, icon, root…) plus each surface under its own key ("desktop", "mobile")
// and the flattened web keys. A native shell reads its own surface from this AND the app-level fields
// it shares (a desktop window's title is the APP's title — one app, one name).
//
// Unlike the cloud boot it does NOT require a web surface: a desktop-only manifest is perfectly valid,
// so no app.website()/app.run() is needed here. Errors (missing/invalid file) are returned so the
// caller can fall back to its own defaults.
func ReadManifest(manifestPath string) (map[string]interface{}, error) {
	builder, err := evalServerBuilder(manifestPath)
	if err != nil {
		return nil, err
	}
	if builder.err != "" {
		return nil, fmt.Errorf("config validation error: %s", builder.err)
	}
	jsonBytes, err := json.Marshal(value.New(builder.config)) // value.Value has MarshalJSON
	if err != nil {
		return nil, fmt.Errorf("manifest serialize: %w", err)
	}
	raw := make(map[string]interface{})
	if err := json.Unmarshal(jsonBytes, &raw); err != nil {
		return nil, fmt.Errorf("manifest decode: %w", err)
	}
	return raw, nil
}

// ReadMobileManifest evaluates an executable app manifest and resolves its
// versioned mobile surface. ReadManifest remains the raw compatibility API;
// strict versioned mobile validation does not alter cloud boot or legacy/unversioned
// app.mobile declarations.
func ReadMobileManifest(manifestPath string) (manifest.Mobile, error) {
	raw, err := ReadManifest(manifestPath)
	if err != nil {
		return manifest.Mobile{}, err
	}
	mobile, err := manifest.Parse(raw)
	if err != nil {
		return manifest.Mobile{}, fmt.Errorf("mobile manifest: %w", err)
	}
	return mobile, nil
}

// evalServerBuilder compiles + runs a bootstrap (server.kitwork.js) in the minimal setup VM (only the
// `server` + `env` builtins) and returns the populated ServerBuilder. It stops BEFORE the strict
// server.run()/validation checks, so callers that only need a captured section (ReadDesktopConfig)
// can read it even when the file declares no server.run(). Safe: the Kitwork subset bans `while` and
// meters energy → it cannot hang.
func evalServerBuilder(file string) (*ServerBuilder, error) {
	bytecode, err := compiler.CompileFile(file)
	if err != nil {
		return nil, fmt.Errorf("config compile error: %w", err)
	}

	vm := runtime.New(bytecode.Program)
	vm.MaxEnergy = 100_000_000 // rộng tay: setup chạy đúng 1 lần, không phải hot path

	builder := NewServerBuilder()
	serverObj := value.New(builder)

	// HOST env: root .env (cạnh file config) phủ bởi biến môi trường THẬT (OS thắng).
	envMap := work.ParseDotEnv(filepath.Join(filepath.Dir(file), ".env"))
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 {
			envMap[kv[:i]] = kv[i+1:]
		}
	}
	if text, ok := envMap["MINIFY"]; ok {
		minify, err := parseMinifyEnv(text)
		if err != nil {
			return nil, err
		}
		builder.minify = minify
	}
	var missingEnv []string
	envObj := work.NewEnvWithMissing(envMap, &missingEnv)

	// kitwork() trong context setup trả về {app, server, env} (KHÔNG phải tenant runtime).
	// `app` là tên chính thức (app.website/.desktop/.mobile — manifest khai báo surface);
	// `server` giữ làm ALIAS tương thích ngược cho config cũ (server.run/.port/...).
	setupRoot := value.New(map[string]value.Value{
		"app":    serverObj,
		"server": serverObj,
		"env":    envObj,
	})
	kitworkFunc := value.NewFunc(func(args ...value.Value) value.Value { return setupRoot })
	vm.Builtins = []value.Value{kitworkFunc}
	vm.Globals["kitwork"] = kitworkFunc

	res := vm.Run()
	if res.K == value.Invalid {
		return nil, fmt.Errorf("config runtime error: %v", res.V)
	}
	if len(missingEnv) > 0 {
		return nil, fmt.Errorf("missing required env var(s): %s", strings.Join(missingEnv, ", "))
	}
	return builder, nil
}

// parseMinifyEnv reads MINIFY of the host env: "true" turns production minification on while
// ALLOW_LOCAL stays true (a local run that serves production's bytes: a Lighthouse score, the size of
// a page), "false" or nothing leaves it to ALLOW_LOCAL. Anything else stops the boot naming the two
// words, as env.KEY reads only "true" and "false" as a bool.
func parseMinifyEnv(text string) (bool, error) {
	switch strings.TrimSpace(text) {
	case "true":
		return true, nil
	case "false", "":
		return false, nil
	}
	return false, fmt.Errorf(`MINIFY=%s: chỉ nhận true hoặc false; MINIFY=true nén HTML và JavaScript như production trong khi ALLOW_LOCAL vẫn là true (không ACME, không TLS)`, strings.TrimSpace(text))
}
