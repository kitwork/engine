package work

import "github.com/kitwork/engine/value"

var (
	GET    = "GET"
	POST   = "POST"
	PUT    = "PUT"
	DELETE = "DELETE"
	PATCH  = "PATCH"
)

func (k *Kitwork) Router(config ...value.Value) *Router {
	return &Router{
		kitwork: k,
	}
}

// Global router hooks mapped onto Kitwork root object
func (k *Kitwork) Get(path string) *Get {
	return k.Router().Get(path)
}

func (k *Kitwork) Post(path string) *Post {
	return k.Router().Post(path)
}

func (k *Kitwork) Put(path string) *Put {
	return k.Router().Put(path)
}

func (k *Kitwork) Delete(path string) *Delete {
	return k.Router().Delete(path)
}

func (k *Kitwork) Patch(path string) *Patch {
	return k.Router().Patch(path)
}

type Router struct {
	kitwork    *Kitwork
	prefix     string
	guardRef   value.Value
	rateLimitN int
	bodySize   int
}

func NewRouter(config ...value.Value) *Router {
	return &Router{}
}

func (r *Router) RateLimit(limit int) *Router {
	r.rateLimitN = limit
	return r
}

func (r *Router) BodyLimit(limit int) *Router {
	r.bodySize = limit
	return r
}

func (r *Router) Base(path string) *Router {
	r.prefix = path
	return r
}

func (r *Router) Group(path string) *Router {
	return &Router{
		kitwork:    r.kitwork,
		prefix:     r.prefix + path,
		guardRef:   r.guardRef,
		rateLimitN: r.rateLimitN,
		bodySize:   r.bodySize,
	}
}

func (r *Router) Guard(callback value.Value) *Router {
	r.guardRef = callback
	return r
}

func (r *Router) Get(path string) *Get {
	return &Get{
		router: r,
		method: GET,
		path:   r.prefix + path,
	}
}

func (r *Router) Post(path string) *Post {
	return &Post{
		router: r,
		method: POST,
		path:   r.prefix + path,
	}
}

func (r *Router) Put(path string) *Put {
	return &Put{
		router: r,
		method: PUT,
		path:   r.prefix + path,
	}
}

func (r *Router) Delete(path string) *Delete {
	return &Delete{
		router: r,
		method: DELETE,
		path:   r.prefix + path,
	}
}

func (r *Router) Patch(path string) *Patch {
	return &Patch{
		router: r,
		method: PATCH,
		path:   r.prefix + path,
	}
}
