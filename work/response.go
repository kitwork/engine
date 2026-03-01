package work

import (
	"net/http"

	"github.com/kitwork/engine/value"
)

type Response struct {
	data    value.Value
	kind    string
	code    int
	headers map[string]string
}

func (r *Response) JSON(data value.Value) *Response {
	r.data = data
	r.kind = "json"
	return r
}

func (r *Response) Redirect(url value.Value) *Response {
	r.data = url
	r.kind = "redirect"
	return r
}

func (r *Response) HTML(data value.Value) *Response {
	r.data = data
	r.kind = "html"
	return r
}

func (r *Response) HelloWorld() *Response {
	r.data = value.New("Hello World")
	r.kind = "text"
	return r
}

func (r *Response) NotFound() *Response {
	r.data = value.New("404 Not Found")
	r.kind = "error"
	r.code = 404
	return r
}

func (r *Response) Status(code int) *Response {
	r.code = code
	return r
}

func (r *Response) Code() int {
	if r.code == 0 {
		return 200
	}
	return r.code
}

func (r *Response) Type() string {
	return r.kind
}

func (r *Response) Data() value.Value {
	return r.data
}

func (r *Response) String() string {
	return r.data.String()
}

func (r *Response) Bytes() []byte {
	return []byte(r.data.String())
}

func (r *Response) Write(w http.ResponseWriter, req *http.Request) {
	switch r.kind {
	case "redirect":
		http.Redirect(w, req, r.String(), http.StatusSeeOther)
	case "text":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(r.Code())
		w.Write(r.Bytes())
	case "json":
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(r.Code())
		w.Write(r.Bytes())
	case "html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(r.Code())
		w.Write(r.Bytes())
	case "file":
		http.ServeFile(w, req, r.String())
	case "folder":
		http.FileServer(http.Dir(r.String())).ServeHTTP(w, req)
	case "empty":
		w.WriteHeader(r.Code())
	case "error":
		w.WriteHeader(r.Code())
		w.Write(r.Bytes())
	default:
		http.NotFound(w, req)
	}
}

// func (r *Response) Header(key, val string) {
// 	if r.Headers == nil {
// 		r.Headers = make(map[string]string)
// 	}
// 	r.Headers[key] = val
// }
