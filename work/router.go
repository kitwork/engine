package work

import (
	"sync"

	"github.com/kitwork/engine/value"
)

type Route struct {
	Method         string
	Path           string
	Fn             *value.Script
	Work           *Work // Exec context
	Redirect       *Redirect
	Template       *Template
	BenchmarkIters int
	IsJIT          bool
}

type Router struct {
	Mu     sync.RWMutex
	Routes []Route
}

var GlobalRouter = &Router{}

func (r *Router) Get(path string, fn value.Value) *Router {
	return r.register("GET", path, fn)
}

func (r *Router) Post(path string, fn value.Value) *Router {
	return r.register("POST", path, fn)
}

// JIT registers a route that serves the JIT CSS framework
func (r *Router) JIT(path string) *Router {
	r.Mu.Lock()
	defer r.Mu.Unlock()
	r.Routes = append(r.Routes, Route{Method: "GET", Path: path, IsJIT: true})
	return r
}

func (r *Router) register(method, path string, fn value.Value) *Router {
	if sFn, ok := fn.V.(*value.Script); ok {
		r.Mu.Lock()
		defer r.Mu.Unlock()
		r.Routes = append(r.Routes, Route{Method: method, Path: path, Fn: sFn})
	}
	return r
}

func (r *Router) Clear() {
	r.Mu.Lock()
	defer r.Mu.Unlock()
	r.Routes = r.Routes[:0]
}
