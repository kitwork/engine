package work

import "github.com/kitwork/engine/value"

type Router struct {
	Work
	Method string
	Path   string
}

func (r *Router) Done(fn value.Value) *Router {
	r.Work.Done(fn)
	return r
}

func (r *Router) Fail(fn value.Value) *Router {
	r.Work.Fail(fn)
	return r
}
