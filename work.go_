package engine

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/kitwork/engine/core"
	"github.com/kitwork/engine/render"
	"github.com/kitwork/engine/value"
	"github.com/kitwork/engine/work"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Port    int      `yaml:"port"`
	Debug   bool     `yaml:"debug"`
	Sources []string `yaml:"source"`
	Master  []string `yaml:"master"`

	Identity string `yaml:"identity"`
	Domain   string `yaml:"domain"`
}

// Run starts the Kitwork Engine using the provided Config.
// It will automatically discover modular configs and database settings within cfg.Sources.
func Run(cfg *Config) {
	if cfg == nil {
		cfg = &Config{}
	}

	e := core.New()

	// 1. Map Base Config
	e.Config.Port = cfg.Port
	if e.Config.Port == 0 {
		e.Config.Port = 8080 // Default port
	}
	e.Config.Debug = cfg.Debug
	e.Config.Sources = cfg.Sources
	if len(e.Config.Sources) == 0 {
		e.Config.Sources = []string{"./"}
	}

	// 3. Automated Discovery & Initialization
	// This will scan each source for work.yaml, logic, and DATABASE configs.
	for _, dir := range e.Config.Sources {
		if e.Config.Debug {
			fmt.Printf("📦 Loading source: %s\n", dir)
		}

		loadLogic(e, dir)
	}

	// 3. Finalize & Fire
	e.SyncRegistry()
	bootServer(e, e.Config.Port)
}

// LoadConfig loads the root configuration file (config.yaml).
func LoadConfig(dir string) (*Config, error) {
	cfg := &Config{}

	// Root config
	path := filepath.Join(dir, "config.yaml")
	if data, err := os.ReadFile(path); err == nil {
		yaml.Unmarshal(data, cfg)
	} else if os.IsNotExist(err) {
		fmt.Printf("ℹ️  config.yaml not found at %s. Using default settings.\n", path)
	}

	// Set defaults
	if cfg.Port == 0 {
		cfg.Port = 8080
	}
	if len(cfg.Sources) == 0 {
		cfg.Sources = []string{"./"}
	}

	return cfg, nil
}

func loadLogic(e *core.Engine, dir string) {
	// PHA 1: Tìm và nạp file gốc (work.js) để khởi tạo Context / App trước
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && info.Name() == "work.js" {
			compileAndRun(e, dir, path)
		}
		return nil
	})
	if err != nil && e.Config.Debug {
		fmt.Printf("⚠️  Warning: Error walking directory %s (Phase 1): %v\n", dir, err)
	}

	// PHA 2: Chạy các file JS khác (routes, api, jobs, etc.)
	err = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		// Bỏ qua work.js vì đã nạp ở Pha 1
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".js") && info.Name() != "work.js" {
			compileAndRun(e, dir, path)
		}
		return nil
	})
	if err != nil && e.Config.Debug {
		fmt.Printf("⚠️  Warning: Error walking directory %s (Phase 2): %v\n", dir, err)
	}
}

// Hàm helper để tránh lặp code khi Build & Trigger
func compileAndRun(e *core.Engine, dir, path string) {
	content, _ := os.ReadFile(path)

	// Extract tenant and domain from path: public/[tenant]/[domain]/...
	tenantID := ""
	domain := ""
	relPath, _ := filepath.Rel(dir, path)
	parts := strings.Split(filepath.ToSlash(relPath), "/")
	if len(parts) >= 2 {
		if strings.Contains(dir, "public") {
			tenantID = parts[0]
			if len(parts) >= 2 {
				domain = parts[1]
			}
		}
	}

	w, err := e.Build(string(content), tenantID, domain, path)
	if err == nil {
		w.SourcePath, _ = filepath.Abs(path) // Track Source Path
		if e.Config.Debug {
			fmt.Printf("📜 Logic loaded: %s (Tenant: %s, Domain: %s)\n", path, tenantID, domain)
		}

		// GLOBAL BYTECODE PROPAGATION
		if w.GetBytecode() != nil {
			e.RegistryMu.Lock()
			for _, rt := range e.Routers {
				if rt.Work.Entity == tenantID && rt.Work.Domain == domain && rt.Work.GetBytecode() == nil {
					rt.Work.SetBytecode(w.GetBytecode())
				}
			}
			for _, c := range e.Crons {
				if c.Work.Entity == tenantID && c.Work.Domain == domain && c.Work.GetBytecode() == nil {
					c.Work.SetBytecode(w.GetBytecode())
				}
			}
			e.RegistryMu.Unlock()
		}

		e.Trigger(context.TODO(), w, nil, nil)
	} else {
		fmt.Printf("❌ Code Error in %s: %v\n", path, err)
	}
}

func bootServer(e *core.Engine, serverPort int) {
	port := strconv.Itoa(serverPort)
	fmt.Printf("🚀 Kitwork Engine online at http://localhost:%s\n", port)

	e.RegistryMu.RLock()
	fmt.Printf("🔍 Routes registered: %d\n", len(e.Routers))
	e.RegistryMu.RUnlock()

	// --- HANDLER CHÍNH (ROUTING + ASSETS) ---
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Recovery from Panics
		defer func() {
			if rec := recover(); rec != nil {
				fmt.Printf("[CRITICAL] Panic Recovered: %v\n", rec)
				debug.PrintStack()
				http.Error(w, "Internal Server Error (Panic)", http.StatusInternalServerError)
			}
		}()

		path := r.URL.Path
		method := r.Method

		e.RegistryMu.RLock()

		// 1. TÌM ROUTE
		var matchedRoute *work.Router
		pathParams := make(map[string]value.Value)
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}

		if e.Config.Debug {
			fmt.Printf("[DEBUG] Request Host: %s, Total Routers: %d\n", host, len(e.Routers))
		}
		for _, rt := range e.Routers {
			if e.Config.Debug {
				fmt.Printf("[DEBUG] Checking Route: Domain=%s, Method=%s, Path=%s (Request: Host=%s, Path=%s)\n", rt.Work.Domain, rt.Method, rt.Path, host, path)
			}
			if rt.Work.Domain != "" && rt.Work.Domain != host && rt.Work.Domain != "localhost" && host != "127.0.0.1" {
				continue
			}
			if rt.Method == method || rt.Method == "ANY" {
				if params, ok := matchRoute(path, rt.Path); ok {
					matchedRoute = rt
					for k, v := range params {
						pathParams[k] = value.New(v)
					}
					break
				}
			}
		}

		if matchedRoute == nil && e.Config.Debug {
			fmt.Printf("[DEBUG] No Route Matched for: %s %s (Host: %s)\n", method, path, host)
		}

		if matchedRoute != nil && e.Config.Debug {
			fmt.Printf("[DEBUG] Matched Route TemplatePath: '%s'\n", matchedRoute.Work.TemplatePath)
		}

		// 2. TÌM ASSET TĨNH (Nếu không có route)
		if matchedRoute == nil && (method == "GET" || method == "HEAD") {
			if e.Config.Debug && path == "/favicon.ico" {
				fmt.Printf("[DEBUG] Checking assets for %s. Asset count: %d\n", path, len(e.Config.Assets))
			}
			for _, asset := range e.Config.Assets {
				cleanAssetPath := "/" + strings.Trim(asset.Path, "/")
				info, err := os.Stat(asset.Dir)
				if err != nil {
					if e.Config.Debug {
						fmt.Printf("[ASSET] Skip %s: %v\n", asset.Dir, err)
					}
					continue
				}

				if !info.IsDir() {
					// Khớp chính xác file đơn lẻ (Ví dụ: /favicon.ico)
					if path == cleanAssetPath {
						if e.Config.Debug {
							fmt.Printf("[ASSET] Serving File: %s -> %s\n", path, asset.Dir)
						}
						e.RegistryMu.RUnlock()
						w.Header().Set("Cache-Control", "public, max-age=31536000")
						http.ServeFile(w, r, asset.Dir)
						return
					}
				} else {
					// Khớp thư mục (Ví dụ: /public/*)
					prefix := cleanAssetPath
					if !strings.HasSuffix(prefix, "/") {
						prefix += "/"
					}
					if path == cleanAssetPath || strings.HasPrefix(path, prefix) {
						rel := ""
						if path != cleanAssetPath {
							rel = strings.TrimPrefix(path, prefix)
						}
						full := filepath.Join(asset.Dir, rel)

						if fi, err := os.Stat(full); err == nil && !fi.IsDir() {
							if e.Config.Debug {
								fmt.Printf("[ASSET] Serving Prefix Match: %s -> %s\n", path, full)
							}
							e.RegistryMu.RUnlock()
							w.Header().Set("Cache-Control", "public, max-age=31536000")
							http.ServeFile(w, r, full)
							return
						}
					}
				}
			}
		}
		e.RegistryMu.RUnlock()

		if matchedRoute == nil {
			fmt.Printf("[HTTP] 404: %s\n", path)
			http.NotFound(w, r)
			return
		}

		fmt.Printf("[HTTP] Matched: %s %s\n", method, path)

		// 3. THỰC THI LOGIC
		queryParams := make(map[string]value.Value)
		for k, v := range r.URL.Query() {
			if len(v) > 0 {
				queryParams[k] = value.New(v[0])
			}
		}

		bodyParams := make(map[string]value.Value)
		if method != "GET" && r.Body != nil {
			var bodyData map[string]any
			if err := json.NewDecoder(r.Body).Decode(&bodyData); err == nil {
				for k, v := range bodyData {
					bodyParams[k] = value.New(v)
				}
			}
		}

		res := e.ExecuteLambda(&matchedRoute.Work, matchedRoute.GetDone(), r, w, queryParams, bodyParams, pathParams)
		if res.Error != "" {
			http.Error(w, res.Error, 500)
			return
		}

		responseVal := res.Response.Data
		if responseVal.K == value.Nil {
			responseVal = res.Value
		}

		// Apply buffered headers
		if res.Response.Headers != nil {
			for k, v := range res.Response.Headers {
				w.Header().Set(k, v)
			}
		}

		// Apply status code
		if res.Response.Code != 0 {
			w.WriteHeader(res.Response.Code)
		}

		renderData := responseVal.Interface()
		if renderData == nil {
			renderData = make(map[string]any)
		}

		// If script called html(template, data), override TemplatePath
		if res.Response.Type == "html" && responseVal.K == value.Map {
			tVal := responseVal.Get("template")
			if tVal.K == value.String {
				tmplPath = tVal.Text()
			}
			dVal := responseVal.Get("data")
			if dVal.K != value.Nil {
				renderData = dVal.Interface()
			}
		}

		if tmplPath != "" {
			finalPath := tmplPath
			if !filepath.IsAbs(finalPath) {
				// 1. NGĂN XẾP ƯU TIÊN 1: Tìm ngay bên cạnh file JS (Angular Style / Co-location)
				// Sử dụng SourcePath của Work (ví dụ index.js) để làm gốc
				if matchedRoute.Work.SourcePath != "" {
					dir := filepath.Dir(matchedRoute.Work.SourcePath)
					possible := filepath.Join(dir, tmplPath)
					if _, err := os.Stat(possible); err == nil {
						finalPath = possible
						goto found
					}
				}

				// 2. NGĂN XẾP ƯU TIÊN 2: Tìm trong folder 'pages' chuẩn
				globalViewDir := ""
				if matchedRoute.Work.Entity != "" && matchedRoute.Work.Domain != "" {
					globalViewDir = filepath.Join("public", matchedRoute.Work.Entity, matchedRoute.Work.Domain, "pages")
				}

				if globalViewDir != "" {
					p := filepath.Join(globalViewDir, tmplPath)
					if _, err := os.Stat(p); err == nil {
						finalPath = p
						goto found
					}
				}
			}

			// Nếu không tìm thấy tệp tin
			if _, err := os.Stat(finalPath); err != nil {
				fmt.Printf("[HTTP] Template Not Found: %s (Check your path in index.js)\n", tmplPath)
			}

		found:
			content, err := os.ReadFile(finalPath)
			if err == nil {
				// Re-resolve globalViewDir for shell discovery
				globalViewDir := ""
				if matchedRoute.Work.Entity != "" && matchedRoute.Work.Domain != "" {
					globalViewDir = filepath.Join("public", matchedRoute.Work.Entity, matchedRoute.Work.Domain, "pages")
				}

				// 1. Render the Page Fragment
				rendered := render.RenderWithDir(string(content), renderData, filepath.Dir(finalPath), globalViewDir)

				if w.Header().Get("Content-Type") == "" {
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
				}
				w.Write([]byte(rendered))
				return
			} else {
				fmt.Printf("[HTTP] Template Error: %v (Final Path: %s)\n", err, finalPath)
			}
		}

		if res.Response.Type == "html" {
			if w.Header().Get("Content-Type") == "" {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
			}
			w.Write([]byte(responseVal.Text()))
			return
		}

		// AUTO-DETECT: SafeHTML Result -> Render as HTML
		if res.Response.Type == "" && responseVal.S == value.SafeHTML {
			if w.Header().Get("Content-Type") == "" {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
			}
			w.Write([]byte(responseVal.Text()))
			return
		}

		// Fallback to JSON if ResType is empty or explicitly "json"
		if res.Response.Type == "json" || res.Response.Type == "" {
			if w.Header().Get("Content-Type") == "" {
				w.Header().Set("Content-Type", "application/json")
			}
			outputData, _ := json.Marshal(responseVal.Interface())
			fmt.Printf("[HTTP] Response: %s\n", string(outputData))
			w.Write(outputData)
		}
	})

	p, _ := strconv.Atoi(port)
	for {
		addr := fmt.Sprintf(":%s", strconv.Itoa(p))
		l, err := net.Listen("tcp", addr)
		if err == nil {
			l.Close()
			fmt.Printf("🚀 Kitwork Engine online at http://localhost:%d\n", p)
			err = http.ListenAndServe(addr, nil)
			if err != nil {
				fmt.Printf("❌ Server Failed: %v\n", err)
			}
			break
		}
		p++
		if p > 9000 {
			break
		}
	}
}

func matchRoute(path, routePath string) (map[string]string, bool) {
	if path == routePath {
		return nil, true
	}

	// Handle Wildcard at the end: /assets/*
	if strings.HasSuffix(routePath, "*") {
		prefix := strings.TrimSuffix(routePath, "*")
		if strings.HasPrefix(path, prefix) {
			return nil, true
		}
	}

	pSegments := strings.Split(strings.Trim(path, "/"), "/")
	rSegments := strings.Split(strings.Trim(routePath, "/"), "/")

	if len(pSegments) != len(rSegments) {
		return nil, false
	}

	params := make(map[string]string)
	for i := 0; i < len(rSegments); i++ {
		if strings.HasPrefix(rSegments[i], ":") {
			params[rSegments[i][1:]] = pSegments[i]
		} else if rSegments[i] != pSegments[i] {
			return nil, false
		}
	}
	return params, true
}
func writeChecksum(path string) {
	data, _ := os.ReadFile(path)
	hash := sha256.Sum256(data)
	os.WriteFile(path+".sha256", []byte(fmt.Sprintf("%x", hash)), 0644)
}

func verifyChecksum(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	hash := sha256.Sum256(data)
	expected, _ := os.ReadFile(path + ".sha256")
	return fmt.Sprintf("%x", hash) == string(expected)
}

// DummyWriter discards output for benchmark mode
type DummyWriter struct {
	HeaderMap http.Header
}

func (d *DummyWriter) Header() http.Header         { return d.HeaderMap }
func (d *DummyWriter) Write(b []byte) (int, error) { return len(b), nil }
func (d *DummyWriter) WriteHeader(statusCode int)  {}
