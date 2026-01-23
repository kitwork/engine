package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/kitwork/engine"
	"github.com/kitwork/engine/internal/security"
	"github.com/kitwork/engine/value"
	"github.com/kitwork/engine/work"
)

func main() {
	// 1. Load Config from Directory
	cfg, err := security.LoadConfigFromDir("config")
	if err != nil {
		fmt.Printf("⚠️ Config Warning: %v. Defaults may apply.\n", err)

		// Fallback empty config if load fails to allow server start
		cfg = &security.Config{}
	}

	if err := work.InitDB(cfg.Database); err != nil {
		fmt.Printf("⚠️ DB Connect Error: %v\n", err)
	} else {
		fmt.Println("✅ Database Connected")
	}

	e := engine.New()

	recoveryMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					log.Printf("🔥 CRITICAL HTTP PANIC: %v", err)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					json.NewEncoder(w).Encode(map[string]string{"error": "Internal Server Error", "details": fmt.Sprintf("%v", err)})
				}
			}()
			next.ServeHTTP(w, r)
		})
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/deploy", func(rw http.ResponseWriter, r *http.Request) {
		scriptName := r.URL.Query().Get("script")
		if scriptName == "" {
			scriptName = "api_gateway"
		}
		if !strings.HasSuffix(scriptName, ".js") {
			scriptName += ".js"
		}

		scriptPath := "demo/" + scriptName
		content, err := os.ReadFile(scriptPath)
		if err != nil {
			rw.WriteHeader(404)
			fmt.Fprintf(rw, "Script not found: %s", scriptPath)
			return
		}

		work.GlobalRouter.Mu.Lock()
		work.GlobalRouter.Routes = nil
		startIdx := 0
		work.GlobalRouter.Mu.Unlock()

		w, err := e.Build(string(content))
		if err != nil {
			rw.WriteHeader(500)
			fmt.Fprintf(rw, "Build failed: %v", err)
			return
		}

		e.Trigger(context.Background(), w)

		// Patch Work into new routes (Dynamic Registration Support)
		work.GlobalRouter.Mu.Lock()
		endIdx := len(work.GlobalRouter.Routes)
		for i := startIdx; i < endIdx; i++ {
			if work.GlobalRouter.Routes[i].Work == nil {
				work.GlobalRouter.Routes[i].Work = w
			}
		}
		work.GlobalRouter.Mu.Unlock()

		rw.Header().Set("Content-Type", "application/json")

		json.NewEncoder(rw).Encode(map[string]any{
			"status":       "deployed",
			"routes_count": len(work.GlobalRouter.Routes),
		})
	})

	mux.HandleFunc("/", handleDynamicRoute(e))

	// Auto-deploy demo APIs
	demoScripts := []string{"demo/api/shorthand.js", "demo/api/gateway.js"}
	for _, scriptPath := range demoScripts {
		content, err := os.ReadFile(scriptPath)
		if err == nil {
			w, err := e.Build(string(content))
			if err == nil {
				// Capture start index to patch only new routes
				work.GlobalRouter.Mu.Lock()
				startIdx := len(work.GlobalRouter.Routes)
				work.GlobalRouter.Mu.Unlock()

				e.Trigger(context.Background(), w)

				// Patch Work
				work.GlobalRouter.Mu.Lock()
				endIdx := len(work.GlobalRouter.Routes)
				for i := startIdx; i < endIdx; i++ {
					if work.GlobalRouter.Routes[i].Work == nil {
						work.GlobalRouter.Routes[i].Work = w
					}
				}
				work.GlobalRouter.Mu.Unlock()
				fmt.Printf("✅ Auto-deployed: %s\n", scriptPath)
			}
		}
	}

	fmt.Println("🚀 Kitwork Dynamic Server running on http://localhost:8081")
	log.Fatal(http.ListenAndServe(":8081", recoveryMiddleware(mux)))
}

func handleDynamicRoute(e *engine.Engine) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		method := r.Method

		var targetRoute *work.Route
		work.GlobalRouter.Mu.RLock()
		for _, rt := range work.GlobalRouter.Routes {
			if rt.Method == method && rt.Path == path {
				targetRoute = &rt
				break
			}
		}
		work.GlobalRouter.Mu.RUnlock()

		if targetRoute == nil {
			rw.WriteHeader(404)
			fmt.Fprintf(rw, "Route %s %s not found", method, path)
			return
		}

		query := make(map[string]value.Value)
		for k, v := range r.URL.Query() {
			if len(v) > 0 {
				query[k] = value.New(v[0])
			}
		}

		reqParams := map[string]value.Value{
			"query": value.New(query),
			"path":  value.New(path),
		}

		dummyWork := work.NewWork("api_call")
		if targetRoute.Work != nil {
			dummyWork = targetRoute.Work // Use the work context containing bytecode!
		}
		result := e.ExecuteLambda(dummyWork, targetRoute.Fn, reqParams)

		if result.Error != "" {
			rw.WriteHeader(500)
			json.NewEncoder(rw).Encode(map[string]string{"error": "Lambda execution failed", "details": result.Error})
			return
		}

		rw.Header().Set("Content-Type", "application/json")
		json.NewEncoder(rw).Encode(result.Response.Interface())
	}
}
