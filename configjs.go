package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kitwork/engine/compiler"
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
// ServerBuilder captures the app MANIFEST: what this app IS (root/database/logger) and WHERE it runs
// — its surfaces: app.web({port}) / app.desktop({}) / app.mobile({}). Declaring a surface is data, not
// a command: the TOOL (cloud host, desktop shell, future CLI) reads the manifest and decides what to
// start. Legacy `server.run(port)` still works and counts as declaring the web surface.
type ServerBuilder struct {
	config map[string]value.Value
	ran    bool // legacy server.run() was called
	hasWeb bool // a WEB surface is declared (app.web(...) or legacy server.run/port)
	path   string
	err    string
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

// Port is the legacy way to declare the web surface's port; app.web({ port }) is the current one.
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

// Mobile declares the MOBILE surface (iOS/Android shell). Captured for the future gomobile build; the
// cloud host and desktop shell ignore it.
func (b *ServerBuilder) Mobile(v value.Value) *ServerBuilder {
	b.config["mobile"] = v
	return b
}

// Title is the app's NAME — an APP-level identity, not a surface option. A build only ever targets one
// OS, so the same name becomes the desktop window title or the mobile app name; declaring it per
// surface would just repeat it. (A web page's <title> is per-page: $.meta.title.)
func (b *ServerBuilder) Title(v value.Value) *ServerBuilder {
	b.config["title"] = v
	return b
}

// Icon is the app's icon — APP-level identity, same reasoning as Title. Each surface derives the sizes
// / formats it needs from it.
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

func (b *ServerBuilder) Database(v value.Value) *ServerBuilder {
	var current []value.Value
	if d, ok := b.config["databases"]; ok && d.K == value.Array {
		current = *d.V.(*[]value.Value)
	}
	current = append(current, v)
	b.config["databases"] = value.Value{K: value.Array, V: &current}
	return b
}

func (b *ServerBuilder) Logger(v value.Value) *ServerBuilder {
	b.config["logger"] = v
	return b
}

// Desktop declares the DESKTOP surface — app.desktop({ chrome, window }). The cloud host ignores it;
// the desktop shell reads it (via ReadManifest) to build the window. MERGES rather than replaces, so
// the grouped form and the flat sugar (app.chrome/app.window) compose in any order.
func (b *ServerBuilder) Desktop(v value.Value) *ServerBuilder {
	if !v.IsMap() {
		b.config["desktop"] = v // app.desktop(true) — surface declared, all defaults
		return b
	}
	for k, val := range v.Map() {
		b.setDesktop(k, val)
	}
	return b
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

// Run is the LEGACY imperative form (server.run(8080) / server.run({...}) / server.run("cfg.yaml")).
// It is kept working for existing configs and counts as declaring the web surface. New manifests use
// the declarative app.web({ port }) instead — nothing in a manifest should read like a command.
func (b *ServerBuilder) Run(args ...value.Value) value.Value {
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
	if builder.err != "" {
		return nil, fmt.Errorf("config validation error: %s", builder.err)
	}
	if !builder.hasWeb {
		return nil, noWebSurfaceErr(builder, file)
	}
	return builderToMap(builder, file)
}

// noWebSurfaceErr explains a manifest that declares no WEB surface. A desktop/mobile-only manifest is
// perfectly valid — it just has nothing for the cloud host to serve — so say that instead of implying
// the file is broken.
func noWebSurfaceErr(b *ServerBuilder, file string) error {
	_, hasDesktop := b.config["desktop"]
	_, hasMobile := b.config["mobile"]
	if hasDesktop || hasMobile {
		surface := "app.desktop()"
		if hasMobile && !hasDesktop {
			surface = "app.mobile()"
		}
		return fmt.Errorf("%s declares %s but no web surface: the cloud host has nothing to serve.\n"+
			"Add `app.web({ port: env.PORT || 8080 })` to also serve HTTP — or run `kitwork-desktop`, which needs no web surface.",
			filepath.Base(file), surface)
	}
	return fmt.Errorf("%s declares no surface: add `app.web({ port: 8080 })` to serve HTTP, and/or `app.desktop({...})` for a desktop app.\n"+
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
// so no app.web()/server.run() is needed here. Errors (missing/invalid file) are returned so the
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

	vm := runtime.New(bytecode.Instructions, bytecode.Constants)
	vm.SourceMap = bytecode.SourceMap
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
	var missingEnv []string
	envObj := work.NewEnvWithMissing(envMap, &missingEnv)

	// kitwork() trong context setup trả về {app, server, env} (KHÔNG phải tenant runtime).
	// `app` là tên chính thức (app.web/.desktop/.mobile — manifest khai báo surface);
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
