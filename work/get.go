package work

import "github.com/kitwork/engine/value"

type Get struct {
	router *Router
	method string
	path   string
}

func (r *Get) Redirect(next string) *Redirect {
	return &Redirect{
		router: r.router,
		method: r.method,
		path:   r.path,
		next:   next,
	}
}

func (r *Get) Folder(path string) *Folder {
	return &Folder{
		router: r.router,
		method: r.method,
		path:   r.path,
		folder: path,
	}
}

func (r *Get) File(path string) *File {
	return &File{
		router: r.router,
		method: r.method,
		path:   r.path,
		file:   path,
	}
}

func (r *Get) Forward(target value.Value) *Forward {
	return &Forward{
		router: r.router,
		method: r.method,
		path:   r.path,
		target: target,
	}
}
