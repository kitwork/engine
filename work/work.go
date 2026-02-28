package work

import (
	"net/http"

	"github.com/kitwork/engine/value"
)

type KitWork struct {
	routes map[string]*Response
}

func New(args ...value.Value) *KitWork {
	return &KitWork{
		routes: make(map[string]*Response),
	}
}

func (w *KitWork) Router() *Router {
	return &Router{work: w}
}

func (w *KitWork) Routes() map[string]*Response {
	return w.routes
}

type Router struct {
	work *KitWork
}

func (r *Router) Get(path string) *Get {
	return &Get{
		work:   r.work,
		path:   path,
		method: "GET",
	}
}

type Get struct {
	work   *KitWork
	path   string
	method string
}

func (g *Get) Redirect(redirect value.Value) *Response {
	resp := &Response{kind: "redirect", data: redirect}
	g.work.routes[g.path] = resp
	return resp
}

func (g *Get) Folder(path string) *Response {
	resp := &Response{kind: "folder", data: value.New(path)}
	g.work.routes[g.path] = resp
	return resp
}

func (g *Get) File(path string) *Response {
	resp := &Response{kind: "file", data: value.New(path)}
	g.work.routes[g.path] = resp
	return resp
}

func (g *Get) Handle(callback value.Value) *Response {
	// Lưu callback (Lambda) vào data để thực thi sau
	resp := &Response{kind: "handle", data: callback}
	g.work.routes[g.path] = resp
	return resp
}

func (kit *KitWork) Server(w http.ResponseWriter, r *http.Request) error {
	response := kit.routes[r.URL.Path]

	switch response.Type() {
	case "redirect":
		http.Redirect(w, r, response.String(), http.StatusSeeOther)
	case "handle":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(response.Code())
		w.Write([]byte("Halle"))
	case "text":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(response.Code())
		w.Write(response.Bytes())
	case "json":
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(response.Code())
		w.Write(response.Bytes())
	case "html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(response.Code())
		w.Write(response.Bytes())
	case "file":
		http.ServeFile(w, r, response.String())
	case "folder":
		http.FileServer(http.Dir(response.String())).ServeHTTP(w, r)
	case "empty":
		// No action needed for empty response
	case "error":
		w.WriteHeader(http.StatusInternalServerError)
	default:
		http.NotFound(w, r)
	}
	return nil
}
