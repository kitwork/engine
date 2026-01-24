package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/kitwork/engine/core"
	"github.com/kitwork/engine/security"
	"github.com/kitwork/engine/value"
	"github.com/kitwork/engine/work"
	"gopkg.in/yaml.v3"
)

// Run bắt đầu môi trường Kitwork Engine với config đầy đủ
func Run(cfg *security.Config) {
	e := core.New()

	// Khởi tạo DB nếu có config
	if cfg.Database.Type != "" {
		if err := work.InitDB(cfg.Database); err != nil {
			fmt.Printf("❌ Database connection failed: %v\n", err)
		} else {
			fmt.Println("✅ Database Connected")
		}
	}

	sourceDir := "./"

	// 1. Quét Config (JSON/YAML)
	loadConfigs(e, sourceDir)

	// 2. Quét Logic (JS)
	loadLogic(e, sourceDir)

	// 3. Đồng bộ Router
	e.SyncRegistry()

	// 4. Khởi động Server
	bootServer(e, cfg.Server.Port)
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
			if s, ok := data["source"].(string); ok {
				e.Config.Source = s
			}

			if e.Config.Debug {
				fmt.Printf("📦 Config loaded: %s [%s]\n", w.Name, f)
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
				if e.Config.Debug {
					fmt.Printf("📜 Logic loaded: %s\n", path)
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
				e.Trigger(context.TODO(), w)
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
	port := "8080"
	if serverPort != 0 {
		port = fmt.Sprintf("%d", serverPort)
	}

	fmt.Printf("🚀 Kitwork Engine online at http://localhost:%s\n", port)

	work.GlobalRouter.Mu.RLock()
	fmt.Printf("🔍 Routes registered: %d\n", len(work.GlobalRouter.Routes))
	for _, r := range work.GlobalRouter.Routes {
		fmt.Printf(" - %s %s (Fn Address: %d)\n", r.Method, r.Path, r.Fn.Address)
	}
	work.GlobalRouter.Mu.RUnlock()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		method := r.Method

		var matchedRoute *work.Route
		work.GlobalRouter.Mu.RLock()
		for _, rt := range work.GlobalRouter.Routes {
			if rt.Method == method && rt.Path == path {
				matched := rt
				matchedRoute = &matched
				break
			}
		}
		work.GlobalRouter.Mu.RUnlock()

		if matchedRoute == nil {
			http.NotFound(w, r)
			return
		}

		params := make(map[string]value.Value)
		for k, v := range r.URL.Query() {
			if len(v) > 0 {
				params[k] = value.New(v[0])
			}
		}

		res := e.ExecuteLambda(matchedRoute.Work, matchedRoute.Fn, params)
		if res.Error != "" {
			http.Error(w, res.Error, 500)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		responseVal := res.Response
		if responseVal.K == value.Nil {
			responseVal = res.Value
		}
		json.NewEncoder(w).Encode(responseVal.Interface())
	})

	http.ListenAndServe(":"+port, nil)
}
