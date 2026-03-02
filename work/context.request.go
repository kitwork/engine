package work

import "github.com/kitwork/engine/value"

// --- WRAPPERS CHO JAVASCRIPT ---

type Request struct {
	router *Router
}

func (r *Request) Query(key string) value.Value {
	if r.router.request == nil {
		return value.Value{K: value.Nil}
	}
	return value.New(r.router.request.URL.Query().Get(key))
}

func (r *Request) Param(key string) value.Value {
	if v, ok := r.router.params[key]; ok {
		return value.New(v)
	}
	return value.Value{K: value.Nil}
}
