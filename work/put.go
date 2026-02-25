package work

import "github.com/kitwork/engine/value"

type Put struct {
	router *Router
	method string
	path   string
}

func (r *Put) Forward(target value.Value) *Forward {
	return &Forward{
		router: r.router,
		method: r.method,
		path:   r.path,
		target: target,
	}
}
