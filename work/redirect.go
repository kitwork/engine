package work

type Redirect struct {
	router *Router
	method string
	path   string
	next   string
	code   int
}

func (r *Redirect) Status(code int) *Redirect {
	r.code = code
	return r
}
