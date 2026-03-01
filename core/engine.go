package core

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/kitwork/engine/work"
)

type Engine struct {
	Source string
	cache  map[string]*work.Tenant
}

func New(source string) *Engine {
	return &Engine{
		Source: source,
		cache:  make(map[string]*work.Tenant),
	}
}

func (e *Engine) load(hostname string) (*work.Tenant, error) {
	if t, ok := e.cache[hostname]; ok {
		return t, nil
	}

	tenant, err := work.NewTenant(e.Source, hostname)
	if err != nil {
		return nil, err
	}

	e.cache[hostname] = tenant
	return tenant, nil
}

func (e *Engine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			fmt.Printf("[CRITICAL] Panic: %v\n", rec)
			http.Error(w, "Service Unavailable", 503)
		}
	}()

	domain := strings.Split(r.Host, ":")[0]
	tenant, err := e.load(domain)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}

	// Bàn giao toàn bộ quyền xử lý cho Tenant
	tenant.Serve(w, r)
}
