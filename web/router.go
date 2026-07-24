package web

import (
	"net/http"
	"sync"
)

type Route struct {
	Method  string
	Path    string
	Handler http.HandlerFunc
}

type Router struct {
	mu     sync.RWMutex
	routes []*Route
}

func NewRouter() *Router {
	return &Router{
		routes: make([]*Route, 0),
	}
}

func (r *Router) Handle(method, path string, handler http.HandlerFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.routes = append(r.routes, &Route{
		Method:  method,
		Path:    path,
		Handler: handler,
	})
}

func (r *Router) Routes() []*Route {
	r.mu.RLock()
	defer r.mu.RUnlock()

	list := make([]*Route, len(r.routes))
	copy(list, r.routes)
	return list
}
