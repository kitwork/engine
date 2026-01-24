package tests

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/kitwork/engine/core"
	"github.com/kitwork/engine/security"
	"github.com/kitwork/engine/value"
	"github.com/kitwork/engine/work"
)

func setupBenchmarkServer() (*core.Engine, error) {
	configPath := "config"

	cfg, err := security.LoadConfigFromDir(configPath)
	if err != nil {
		cfg = &security.Config{}
	}

	if err := work.InitDB(cfg.Database); err != nil {
		fmt.Printf("Benchmark DB Warning: %v\n", err)
	}

	e := core.New()

	scriptPath := "demo/api/shorthand.js"
	if _, err := os.Stat(scriptPath); err == nil {
		content, _ := os.ReadFile(scriptPath)
		w, _ := e.Build(string(content))
		e.Trigger(context.Background(), w)

		work.GlobalRouter.Mu.Lock()
		for i := range work.GlobalRouter.Routes {
			if work.GlobalRouter.Routes[i].Work == nil {
				work.GlobalRouter.Routes[i].Work = w
			}
		}
		work.GlobalRouter.Mu.Unlock()
	}

	return e, nil
}

func benchmarkHandler(e *core.Engine) http.HandlerFunc {
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
			dummyWork = targetRoute.Work
		}
		result := e.ExecuteLambda(dummyWork, targetRoute.Fn, reqParams)

		if result.Error != "" {
			rw.WriteHeader(500)
			return
		}

		rw.WriteHeader(200)
	}
}

func BenchmarkAPIUsers(b *testing.B) {
	e, _ := setupBenchmarkServer()
	handler := benchmarkHandler(e)
	req, _ := http.NewRequest("GET", "/api/users", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
	}
}

func BenchmarkAPIDynamicUsers(b *testing.B) {
	e, _ := setupBenchmarkServer()

	scriptPath := "demo/api/gateway.js"
	if _, err := os.Stat(scriptPath); err == nil {
		content, _ := os.ReadFile(scriptPath)
		w, _ := e.Build(string(content))
		e.Trigger(context.Background(), w)

		work.GlobalRouter.Mu.Lock()
		for i := range work.GlobalRouter.Routes {
			if work.GlobalRouter.Routes[i].Work == nil {
				work.GlobalRouter.Routes[i].Work = w
			}
		}
		work.GlobalRouter.Mu.Unlock()
	}

	handler := benchmarkHandler(e)
	req, _ := http.NewRequest("GET", "/api/dynamic/users?name=bob", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
	}
}

func BenchmarkAPIRaw(b *testing.B) {
	e, _ := setupBenchmarkServer()

	scriptPath := "demo/api/raw.js"
	if _, err := os.Stat(scriptPath); err == nil {
		content, _ := os.ReadFile(scriptPath)
		w, _ := e.Build(string(content))
		e.Trigger(context.Background(), w)

		work.GlobalRouter.Mu.Lock()
		for i := range work.GlobalRouter.Routes {
			if work.GlobalRouter.Routes[i].Work == nil {
				work.GlobalRouter.Routes[i].Work = w
			}
		}
		work.GlobalRouter.Mu.Unlock()
	}

	handler := benchmarkHandler(e)
	req, _ := http.NewRequest("GET", "/api/raw", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
	}
}
