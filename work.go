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
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kitwork/engine/core"
	"github.com/kitwork/engine/jit/css"
	"github.com/kitwork/engine/render"
	"github.com/kitwork/engine/security"
	"github.com/kitwork/engine/value"
	"github.com/kitwork/engine/work"
	"gopkg.in/yaml.v3"
)

type Asset struct {
	Dir  string `yaml:"dir"`
	Path string `yaml:"path"`
}

type Config struct {
	Port      int      `yaml:"port"`
	Debug     bool     `yaml:"debug"`
	Sources   []string `yaml:"source"`
	Assets    []Asset  `yaml:"assets"`
	Databases []string `yaml:"databases"`
	SMTPS     []string `yaml:"smtp"`
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

	// Map Assets
	for _, a := range cfg.Assets {
		e.Config.Assets = append(e.Config.Assets, core.Asset{
			Dir:  a.Dir,
			Path: a.Path,
		})
	}

	// 2. Automated Discovery & Initialization
	// Load explicitly defined databases from files
	for _, dbPath := range cfg.Databases {
		if data, err := os.ReadFile(dbPath); err == nil {
			var dbCfg security.DBConfig
			if err := yaml.Unmarshal(data, &dbCfg); err == nil {
				if err := work.InitDB(dbCfg); err == nil {
					fmt.Printf("✅ Database Connected from file: %s\n", dbPath)
				} else {
					fmt.Printf("❌ Database Connection Failed (%s): %v\n", dbPath, err)
				}
			}
		} else {
			fmt.Printf("⚠️  Database config file not found: %s\n", dbPath)
		}
	}

	// Load explicitly defined SMTP from files
	for _, smtpPath := range cfg.SMTPS {
		if data, err := os.ReadFile(smtpPath); err == nil {
			var smtpCfg security.SMTPConfig
			if err := yaml.Unmarshal(data, &smtpCfg); err == nil {
				fmt.Printf("📧 SMTP Config Loaded from file: %s (Host: %s)\n", smtpPath, smtpCfg.Host)
			}
		} else {
			fmt.Printf("⚠️  SMTP config file not found: %s\n", smtpPath)
		}
	}

	// 3. Automated Discovery & Initialization
	// This will scan each source for work.yaml, logic, and DATABASE configs.
	for _, dir := range e.Config.Sources {
		if e.Config.Debug {
			fmt.Printf("📦 Loading source: %s\n", dir)
		}
		loadConfigs(e, dir)
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

func loadConfigs(e *core.Engine, dir string) {
	patterns := []string{"work.json", "work.yaml", "work.yml"}
	for _, p := range patterns {
		files, _ := filepath.Glob(filepath.Join(dir, p))
		for _, f := range files {
			content, _ := os.ReadFile(f)
			data := make(map[string]any)
			var err error
			if strings.HasSuffix(f, ".json") {
				err = json.Unmarshal(content, &data)
			} else {
				err = yaml.Unmarshal(content, &data)
			}
			if err != nil {
				fmt.Printf("❌ Config error [%s]: %v\n", f, err)
				continue
			}

			w := work.NewWork("generic")
			w.LoadFromConfig(data)
			e.RegisterWork(w)

			// Update global config if present in file
			if p, ok := data["port"].(int); ok {
				e.Config.Port = p
			}
			if p, ok := data["port"].(float64); ok {
				e.Config.Port = int(p)
			}
			if d, ok := data["debug"].(bool); ok {
				e.Config.Debug = d
			}
			if str, ok := data["source"].(string); ok {
				e.Config.Sources = append(e.Config.Sources, str)
			}
			if ss, ok := data["source"].([]any); ok {
				for _, s := range ss {
					if str, ok := s.(string); ok {
						e.Config.Sources = append(e.Config.Sources, str)
					}
				}
			}

			if e.Config.Debug {
				fmt.Printf("📦 Config loaded: %s [%s]\n", w.Name, f)
			}

			// SUPPORT MODULAR DB INITIALIZATION
			if dbRaw, ok := data["database"]; ok {
				var dbCfg security.DBConfig
				// Convert map to struct via JSON (easiest way in Go for generic maps)
				jsonData, _ := json.Marshal(dbRaw)
				json.Unmarshal(jsonData, &dbCfg)

				if dbCfg.Type != "" {
					if err := work.InitDB(dbCfg); err == nil {
						fmt.Printf("✅ Database Connected (from %s)\n", f)
					}
				}
			}
		}
	}
}

func loadLogic(e *core.Engine, dir string) {
	// Recursive walk to find all .js files
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		} // Skip read errors
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".js") {
			content, _ := os.ReadFile(path)
			w, err := e.Build(string(content))
			if err == nil {
				w.SourcePath, _ = filepath.Abs(path) // Track Source Path
				if e.Config.Debug {
					fmt.Printf("📜 Logic loaded: %s (Registry size: %d)\n", path, len(e.Registry))
				}

				// GLOBAL BYTECODE PROPAGATION
				if w.Bytecode != nil {
					for _, other := range e.Registry {
						if other.Bytecode == nil {
							other.Bytecode = w.Bytecode
						}
					}
				}

				fmt.Printf("[loadLogic] Calling Trigger for Work: %s (bytecode: %v)\n", w.Name, w.Bytecode != nil)
				e.Trigger(context.TODO(), w, nil, nil)
			} else {
				fmt.Printf("❌ Code Error in %s: %v\n", path, err)
			}
		}
		return nil
	})
	if err != nil && e.Config.Debug {
		fmt.Printf("⚠️  Warning: Error walking directory %s: %v\n", dir, err)
	}
}

func bootServer(e *core.Engine, serverPort int) {
	port := strconv.Itoa(serverPort)
	fmt.Printf("🚀 Kitwork Engine online at http://localhost:%s\n", port)

	work.GlobalRouter.Mu.RLock()
	fmt.Printf("🔍 Routes registered: %d\n", len(work.GlobalRouter.Routes))
	for _, r := range work.GlobalRouter.Routes {
		fmt.Printf(" - %s %s (Fn Address: %d)\n", r.Method, r.Path, r.Fn.Address)
	}
	work.GlobalRouter.Mu.RUnlock()

	// Register Static Assets
	for _, asset := range e.Config.Assets {
		prefix := "/" + strings.Trim(asset.Path, "/") + "/"
		if prefix == "//" { // Root asset
			prefix = "/"
		}

		if e.Config.Debug {
			fmt.Printf("📂 Asset Registered: Path=%s -> Dir=%s\n", prefix, asset.Dir)
		}

		handler := http.StripPrefix(strings.TrimSuffix(prefix, "/"), http.FileServer(http.Dir(asset.Dir)))
		// Wrap handler with Cache-Control
		cacheHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "public, max-age=31536000") // 1 year
			handler.ServeHTTP(w, r)
		})
		http.Handle(prefix, cacheHandler)
	}

	// Internal API: System Introspection (Routes)
	http.HandleFunc("/_kitwork/routes", func(w http.ResponseWriter, r *http.Request) {
		work.GlobalRouter.Mu.RLock()
		defer work.GlobalRouter.Mu.RUnlock()

		var routes []map[string]string
		for _, r := range work.GlobalRouter.Routes {
			wn := "global"
			if r.Work != nil {
				wn = r.Work.Name
			}
			routes = append(routes, map[string]string{
				"method": r.Method,
				"path":   r.Path,
				"work":   wn,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(routes)
	})

	// Internal API: View Source Code (Safe Read based on Work Name + Tenant)
	http.HandleFunc("/_kitwork/source", func(w http.ResponseWriter, r *http.Request) {
		workName := r.URL.Query().Get("work")
		tenantID := r.URL.Query().Get("tenant") // Optional Tenant Filter

		if workName == "" {
			http.Error(w, "Missing work name", 400)
			return
		}

		e.RegistryMu.RLock()
		workUnit, ok := e.Registry[workName]

		if !ok {
			e.RegistryMu.RUnlock()
			http.Error(w, "Work unit not found", 404)
			return
		}

		// Grab data before unlocking to be safe (though Work is mostly immutable pointer in Registry)
		unitPath := workUnit.SourcePath
		unitTenant := workUnit.TenantID
		e.RegistryMu.RUnlock()

		// Tenant Isolation Check
		if tenantID != "" && unitTenant != tenantID {
			http.Error(w, "Work unit does not belong to this tenant", 403)
			return
		}

		if unitPath == "" {
			http.Error(w, "Source not available (compiled from memory?)", 404)
			return
		}

		// SECURITY: Verify file is within allowed tenant directories if necessary
		// For now we rely on SourcePath being set by the trusted engine

		content, err := os.ReadFile(unitPath)
		if err != nil {
			http.Error(w, "Failed to read source file", 500)
			return
		}

		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write(content)
	})

	// Internal API: Hot Deploy Work Unit (Serverless Deployment)
	http.HandleFunc("/_kitwork/deploy", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", 405)
			return
		}

		// TODO: Add Master Key Authentication here

		var req struct {
			Content string `json:"content"`
			Path    string `json:"path"` // Relative path to save file (canonical)
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", 400)
			return
		}

		if req.Content == "" {
			http.Error(w, "Content required", 400)
			return
		}

		// 1. Hot Compile (Virtual Machine check)
		newWork, err := e.Build(req.Content)
		if err != nil {
			fmt.Printf("❌ Hot Deploy Failed: %v\n", err)
			http.Error(w, fmt.Sprintf("Compilation Failed: %v", err), 400)
			return
		}

		// 2. Persistence (Write to Disk)
		// If path is provided, we save it to become permanent
		if req.Path != "" {
			// Validate path to prevent directory traversal
			if strings.Contains(req.Path, "..") {
				http.Error(w, "Invalid path", 400)
				return
			}

			// Use first source directory as default root
			rootDir := "./"
			if len(e.Config.Sources) > 0 {
				rootDir = e.Config.Sources[0]
			}
			fullPath := filepath.Join(rootDir, req.Path)

			er := os.MkdirAll(filepath.Dir(fullPath), 0755)
			if er == nil {
				os.WriteFile(fullPath, []byte(req.Content), 0644)
				newWork.SourcePath, _ = filepath.Abs(fullPath)
			}
		}

		// 3. Hot Swap (Thread-Safe Registry Update)
		e.RegistryMu.Lock()
		e.Registry[newWork.Name] = newWork
		e.RegistryMu.Unlock()

		// 4. Trigger Initialization (Run top-level logic)
		e.Trigger(context.TODO(), newWork, nil, nil)

		fmt.Printf("🔥 Hot Deployed: %s (v%s)\n", newWork.Name, newWork.Ver)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "deployed",
			"work":    newWork.Name,
			"version": newWork.Ver,
		})
	})

	// Internal API: JIT CSS Generator
	http.HandleFunc("/_kitwork/jit", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		var req struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", 400)
			return
		}

		cssOutput := css.GenerateJIT(req.Content)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"css": cssOutput,
		})
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		method := r.Method

		fmt.Printf("[HTTP] Incoming %s %s\n", method, path)

		var matchedRoute *work.Route
		pathParams := make(map[string]value.Value)

		work.GlobalRouter.Mu.RLock()
		for i := range work.GlobalRouter.Routes {
			rt := &work.GlobalRouter.Routes[i]
			if rt.Method == method {
				if params, ok := matchRoute(path, rt.Path); ok {
					matchedRoute = rt
					for k, v := range params {
						pathParams[k] = value.New(v)
					}
					fmt.Printf("[HTTP] Matched route: %s %s\n", rt.Method, rt.Path)
					break
				}
			}
		}
		work.GlobalRouter.Mu.RUnlock()

		if matchedRoute == nil {
			fmt.Printf("[HTTP] No match found for %s %s\n", method, path)
			http.NotFound(w, r)
			return
		}

		// 1. FAST-PATH: Redirect Support
		if matchedRoute.Redirect != nil {
			fmt.Printf("[HTTP] Redirecting %s -> %s (%d)\n", path, matchedRoute.Redirect.URL, matchedRoute.Redirect.Code)
			http.Redirect(w, r, matchedRoute.Redirect.URL, matchedRoute.Redirect.Code)
			return
		}

		// 2. FAST-PATH: Resource Serving (File or Assets)
		if matchedRoute.Work.ResourcePath != "" {
			info, err := os.Stat(matchedRoute.Work.ResourcePath)
			if err == nil {
				if info.IsDir() {
					// Directory mode: handle wildcard/prefix
					prefix := strings.TrimSuffix(matchedRoute.Path, "*")
					subPath := strings.TrimPrefix(path, prefix)
					target := filepath.Join(matchedRoute.Work.ResourcePath, subPath)
					fmt.Printf("[HTTP] Serving Asset: %s\n", target)
					http.ServeFile(w, r, target)
				} else {
					// File mode: serve directly
					fmt.Printf("[HTTP] Serving File: %s\n", matchedRoute.Work.ResourcePath)
					http.ServeFile(w, r, matchedRoute.Work.ResourcePath)
				}
				return
			}
		}

		// 2. URL Query Params
		queryParams := make(map[string]value.Value)
		for k, v := range r.URL.Query() {
			if len(v) > 0 {
				queryParams[k] = value.New(v[0])
			}
		}

		// 2. JSON Body Params
		bodyParams := make(map[string]value.Value)
		if r.Method != "GET" && r.Body != nil {
			var bodyData map[string]any
			if err := json.NewDecoder(r.Body).Decode(&bodyData); err == nil {
				for k, v := range bodyData {
					bodyParams[k] = value.New(v)
				}
			}
		}

		// 3. Cache Check (RAM)
		cacheKey := ""
		if matchedRoute.Work.CacheDuration > 0 {
			cacheKey = "work:" + matchedRoute.Work.Name + ":" + r.URL.String()
			if cached, ok := work.GetCache(cacheKey); ok {
				fmt.Printf("[HTTP] Cache Hit: %s\n", cacheKey)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Cache", "HIT")
				outputData, _ := json.Marshal(cached.Interface())
				w.Write(outputData)
				return
			}
		}

		// 4. Static Stack Check (DISK)
		stackPath := ""
		if matchedRoute.Work.StaticDuration > 0 {
			hashedName := fmt.Sprintf("%x", sha256.Sum256([]byte(r.URL.String())))
			stackPath = filepath.Join(".stack", matchedRoute.Work.Name, hashedName)

			if info, err := os.Stat(stackPath); err == nil {
				if time.Since(info.ModTime()) < matchedRoute.Work.StaticDuration {
					// Potentially verify checksum if enabled
					valid := true
					if matchedRoute.Work.StaticCheck {
						// Logic for checksum verification
						valid = verifyChecksum(stackPath)
					}

					if valid {
						fmt.Printf("[HTTP] Stack Hit: %s\n", stackPath)
						w.Header().Set("X-Stack", "HIT")
						http.ServeFile(w, r, stackPath)
						return
					}
				}
			}
		}

		// 4. SPECIAL: Benchmark Mode
		if matchedRoute.BenchmarkIters > 0 {
			iters := matchedRoute.BenchmarkIters
			workerCount := runtime.GOMAXPROCS(0) // Auto-detect optimal workers
			if workerCount < 1 {
				workerCount = 1
			}
			// Ensure workerCount doesn't exceed iterations
			if iters < workerCount {
				workerCount = iters
			}

			// Prepare Memory Stats
			var msBefore, msAfter runtime.MemStats
			runtime.GC() // Force GC before starting to get clean state
			runtime.ReadMemStats(&msBefore)

			start := time.Now()
			var wg sync.WaitGroup
			wg.Add(workerCount)

			// Dummy Writer for benchmark
			dummyWriter := &DummyWriter{HeaderMap: make(http.Header)}

			itersPerWorker := iters / workerCount

			for i := 0; i < workerCount; i++ {
				// Handle remainder iterations for the last worker
				count := itersPerWorker
				if i == workerCount-1 {
					count = iters - (itersPerWorker * (workerCount - 1))
				}

				go func(c int) {
					defer wg.Done()
					for j := 0; j < c; j++ {
						e.ExecuteLambda(matchedRoute.Work, matchedRoute.Fn, r, dummyWriter, queryParams, bodyParams, pathParams)
					}
				}(count)
			}
			wg.Wait()
			duration := time.Since(start)

			// Collect Memory Stats
			runtime.ReadMemStats(&msAfter)

			// Metrics
			throughput := float64(iters) / duration.Seconds()
			latency := duration / time.Duration(iters)
			totalAlloc := msAfter.TotalAlloc - msBefore.TotalAlloc
			gcCycles := msAfter.NumGC - msBefore.NumGC

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"benchmark":    true,
				"iterations":   iters,
				"workers":      workerCount,
				"duration":     duration.String(),
				"throughput":   fmt.Sprintf("%.0f req/s", throughput),
				"latency":      latency.String(),
				"latency_ns":   latency.Nanoseconds(),
				"alloc_bytes":  totalAlloc,
				"alloc_per_op": totalAlloc / uint64(iters),
				"gc_cycles":    gcCycles,
			})
			return
		}

		res := e.ExecuteLambda(matchedRoute.Work, matchedRoute.Fn, r, w, queryParams, bodyParams, pathParams)
		if res.Error != "" {
			http.Error(w, res.Error, 500)
			return
		}

		responseVal := res.Response
		if responseVal.K == value.Nil {
			responseVal = res.Value
		}

		// 5. NEW: Auto-Render Logic (Low-latency rendering)
		var t *work.Template = matchedRoute.Template

		// Fallback to Work unit routes if global router is stale
		if t == nil || t.Page == "" {
			for _, r := range matchedRoute.Work.Routes {
				if r.Method == matchedRoute.Method && r.Path == matchedRoute.Path {
					t = r.Template
					break
				}
			}
		}

		if t != nil && t.Page != "" && res.ResType != "json" {
			tmpl, err := os.ReadFile(t.Page)
			if err != nil {
				fmt.Printf("❌ Template Error: %v (Path: %s)\n", err, t.Page)
				http.Error(w, "Template not found", 404)
				return
			}
			dataMap := make(map[string]value.Value)
			if responseVal.K == value.Map {
				for k, v := range responseVal.Map() {
					dataMap[k] = v
				}
			} else {
				dataMap["value"] = responseVal
			}

			// Composite Rendering: Pre-render Layout partials
			for key, partPath := range t.Layout {
				partTmpl, err := os.ReadFile(partPath)
				if err == nil {
					// Render partial with SAME data context
					// Must wrap dataMap in Value to pass to Render
					renderedPart := render.Render(string(partTmpl), value.New(dataMap))
					// Mark as SafeHTML to avoid double escaping in layout
					dataMap[key] = value.Value{K: value.String, V: renderedPart, S: value.SafeHTML}
				}
			}

			htmlContent := render.Render(string(tmpl), value.New(dataMap))
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(htmlContent))
			return
		}

		if res.ResType == "html" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			htmlContent := ""
			if responseVal.K == value.Map {
				templateVal := responseVal.Get("template")
				dataVal := responseVal.Get("data")
				htmlContent = render.Render(templateVal.Text(), dataVal)
			} else {
				htmlContent = responseVal.Text()
			}
			w.Write([]byte(htmlContent))
			return
		}

		// AUTO-DETECT: SafeHTML Result -> Render as HTML
		if res.ResType == "" && responseVal.S == value.SafeHTML {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(responseVal.Text()))
			return
		}

		// Fallback to JSON if ResType is empty or explicitly "json"
		w.Header().Set("Content-Type", "application/json")
		outputData, _ := json.Marshal(responseVal.Interface())
		fmt.Printf("[HTTP] Response: %s\n", string(outputData))

		// Save to cache (RAM)
		if cacheKey != "" && res.Error == "" {
			work.SetCache(cacheKey, responseVal, matchedRoute.Work.CacheDuration)
		}

		// Save to stack (DISK)
		if stackPath != "" && res.Error == "" {
			os.MkdirAll(filepath.Dir(stackPath), 0755)
			data, _ := json.Marshal(responseVal.Interface())
			os.WriteFile(stackPath, data, 0644)
			if matchedRoute.Work.StaticCheck {
				writeChecksum(stackPath)
			}
		}

		w.Write(outputData)
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
