package work

import "github.com/kitwork/engine/value"

type Forward struct {
	router *Router
	method string
	path   string
	target value.Value // This could be a static string or a context-based callback
}
