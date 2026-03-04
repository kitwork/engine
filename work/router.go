package work

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/kitwork/engine/value"
)

func (w *KitWork) Router() *Router { return &Router{tenant: w.tenant} }

type Router struct {
	tenant   *Tenant
	Method   string
	Path     string
	basePath string

	guards []*value.Lambda
	handle *value.Lambda
	then   *value.Lambda
	catch  *value.Lambda
	final  *value.Lambda

	response *Response
	request  *http.Request

	params map[string]string
	err    error // Biến lưu lỗi để truyền giữa các công đoạn

	// Cache configuration
	cacheTTL time.Duration
}

// --- ENGINE LOGIC ---

func (r *Router) responder(w http.ResponseWriter) {
	kind := r.response.Kind()
	data := r.response.Data()
	request := r.request

	// 1. Tự động xác định kind nếu trống dựa trên data hoặc lỗi
	if kind == "" {
		if r.err != nil {
			kind = "error"
			data = value.New(r.err.Error())
			if r.response.Code() == 0 {
				r.response.Status(500)
			}
		} else if !data.IsBlank() {
			if data.K == value.Map || data.K == value.Array {
				kind = "json"
			} else {
				kind = "html"
			}
		}
	}

	// 2. Mặc định Status 200 nếu chưa có
	if r.response.Code() == 0 {
		r.response.Status(200)
	}

	// 3. Xử lý phản hồi dựa trên kind
	switch kind {
	case "render":
		result, err := r.render()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(r.response.Code())
		w.Write(result)
	case "redirect":
		http.Redirect(w, request, data.String(), http.StatusSeeOther)
	case "text":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(r.response.Code())
		w.Write(r.response.toBytes())
	case "file":
		http.ServeFile(w, request, data.String())
	case "directory":
		http.FileServer(http.Dir(data.String())).ServeHTTP(w, request)
	case "bytes":
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(r.response.Code())
		w.Write(data.Bytes())
	case "image":
		// TODO: Tự động nhận diện mime-type từ bytes
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(r.response.Code())
		w.Write(data.Bytes())
	case "json":
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(r.response.Code())
		b, err := json.Marshal(r.response.Data())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write(b)
	case "html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(r.response.Code())
		w.Write(r.response.toBytes())
	case "error":
		w.WriteHeader(r.response.Code())
		w.Write(r.response.toBytes())
	default:
		http.NotFound(w, request)
	}
}

func (r *Router) render() ([]byte, error) {

	render := NewRender(r.tenant).
		Page(value.New(r.request.URL.Path)).
		Layout(value.New(r.response.page.layout)).
		Template(value.New(r.response.page.template)).
		Bind(r.response.data)
	return []byte(render.String()), nil
}

func (r *Router) New(method, path string) *Router {
	newRoute := *r
	fullPath := "/" + strings.Trim(r.basePath, "/") + "/" + strings.Trim(path, "/")
	fullPath = strings.ReplaceAll(fullPath, "//", "/")

	newRoute.Method = method
	newRoute.Path = fullPath
	newRoute.handle = nil
	newRoute.response = &Response{}

	r.tenant.routes = append(r.tenant.routes, &newRoute)
	return &newRoute
}

func (r *Router) Get(path string) *Router  { return r.New("GET", path) }
func (r *Router) Post(path string) *Router { return r.New("POST", path) }

func (r *Router) Handle(l value.Value) *Router { r.handle, _ = l.V.(*value.Lambda); return r }
func (r *Router) Guard(l value.Value) *Router {
	if l.K == value.Func {
		if lambda, ok := l.V.(*value.Lambda); ok {
			r.guards = append(r.guards, lambda)
		}
	}
	return r
}
func (r *Router) Then(v value.Value) *Router    { r.then, _ = v.V.(*value.Lambda); return r }
func (r *Router) Catch(v value.Value) *Router   { r.catch, _ = v.V.(*value.Lambda); return r }
func (r *Router) Finally(v value.Value) *Router { r.final, _ = v.V.(*value.Lambda); return r }

func (r *Router) Group(prefix string) *Router {
	newGroup := *r
	newGroup.basePath = "/" + strings.Trim(prefix, "/")
	return &newGroup
}

func (r *Router) File(path string) *Router {
	r.response.File(r.tenant.joinPath(path))
	return r
}

func (r *Router) Response(data value.Value, options ...interface{}) *Router {

	// 2. Nếu không, coi là phản hồi dữ liệu tĩnh
	r.response.Send(data, options...)
	return r
}

func (r *Router) Directory(path string) *Router {
	r.response.Directory(r.tenant.joinPath(path))
	return r
}

func (r *Router) Redirect(url string) *Router {
	r.response.Redirect(value.New(url))
	return r
}

func (r *Router) Cache(v value.Value) *Router {
	d, err := time.ParseDuration(v.String())
	if err == nil {
		r.cacheTTL = d
	}
	return r
}
