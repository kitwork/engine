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
