package work

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// Host đại diện cho một instance ứng dụng hoàn chỉnh
type Host struct {
	Bytecode *compiler.Bytecode
	VM       *runtime.Runtime
	Routes   []*Router
}

// Context đóng gói dữ liệu yêu cầu để truyền vào JS
type Context struct {
	Request   *http.Request
	paramsMap map[string]value.Value
	queryMap  map[string]value.Value
}

// Helper cho JS: ctx.param("id")
func (c *Context) Param(key string) value.Value { return c.paramsMap[key] }
func (c *Context) Query(key string) value.Value { return c.queryMap[key] }

type KitWork struct {
	host *Host
}

type Router struct {
	host   *Host
	Method string
	Path   string
	Prefix string // Lưu tiền tố (Prefix) ví dụ "/api"
	Kind   string // Loại phản hồi: "file", "redirect", "html", "json"
	Value  value.Value

	guard  *value.Lambda
	handle *value.Lambda
	done   *value.Lambda
	fail   *value.Lambda

	staticRes *Response // Dùng cho redirect/file tĩnh
}

func NewHost(bc *compiler.Bytecode) *Host {
	h := &Host{
		Bytecode: bc,
		VM:       runtime.New(bc.Instructions, bc.Constants),
		Routes:   make([]*Router, 0),
	}
	return h
}

// Export đối tượng này sang JS
func (h *Host) Provider() *KitWork {
	return &KitWork{host: h}
}

// Thêm phương thức Router() để khớp với lệnh JS 'const { router } = kitwork;'
func (w *KitWork) Router() *Router {
	return &Router{host: w.host}
}

func (r *Router) New(method, path string) *Router {
	// CLONE đối tượng router hiện tại
	newRoute := *r

	// GHÉP TIỀN TỐ: Prefix + path
	fullPath := "/" + strings.Trim(r.Prefix, "/") + "/" + strings.Trim(path, "/")
	fullPath = strings.ReplaceAll(fullPath, "//", "/") // Làm sạch chuỗi // thành /

	newRoute.Method = method
	newRoute.Path = fullPath

	// Reset dữ liệu riêng lẻ
	newRoute.handle = nil
	newRoute.staticRes = nil
	newRoute.Kind = ""
	newRoute.Value = value.Value{}

	r.host.Routes = append(r.host.Routes, &newRoute)
	return &newRoute
}

// JS: kitwork.router.get("/path")
func (r *Router) Get(path string) *Router {
	return r.New("GET", path)
}

// Fluent API cho Router
func (r *Router) Handle(l value.Value) *Router { r.handle, _ = l.V.(*value.Lambda); return r }
func (r *Router) Guard(l value.Value) *Router  { r.guard, _ = l.V.(*value.Lambda); return r }
func (r *Router) Done(l value.Value) *Router   { r.done, _ = l.V.(*value.Lambda); return r }
func (r *Router) Fail(l value.Value) *Router   { r.fail, _ = l.V.(*value.Lambda); return r }
func (r *Router) File(path string) *Router     { r.Kind = "file"; r.Value = value.New(path); return r }

// MỚI: Thiết lập tiền tố cho cả một nhóm
func (r *Router) Base(prefix string) *Router {
	newGroup := *r
	// Đảm bảo có dấu / ở đầu và không có ở cuối
	prefix = "/" + strings.Trim(prefix, "/")
	newGroup.Prefix = prefix
	return &newGroup
}

func (r *Router) Redirect(url string) *Router {
	r.staticRes = &Response{kind: "redirect", data: value.New(url)}
	return r
}

// Heart of the host: Xử lý Request
func (h *Host) Serve(w http.ResponseWriter, r *http.Request) {
	// 1. Match Route
	var matched *Router
	var params map[string]string

	path := r.URL.Path
	if path == "" {
		path = "/"
	}

	for _, rt := range h.Routes {
		fmt.Printf("[DEBUG] Checking route: %s %s vs Request: %s %s\n", rt.Method, rt.Path, r.Method, path)
		if rt.Method == r.Method || rt.Method == "ANY" {
			if p, ok := matchRoute(path, rt.Path); ok {
				matched = rt
				params = p
				break
			}
		}
	}

	if matched == nil {
		http.NotFound(w, r)
		return
	}

	// 2. Nếu là route tĩnh (redirect), xử lý ngay
	if matched.staticRes != nil {
		matched.staticRes.Write(w, r)
		return
	}

	// 3. Nếu là Handle (Lambda), thực thi VM
	h.execute(w, r, matched, params)
}

func (h *Host) execute(w http.ResponseWriter, r *http.Request, rt *Router, p map[string]string) {
	// Tạo VM con sạch sẽ để tránh xung đột
	vm := runtime.New(h.Bytecode.Instructions, h.Bytecode.Constants)

	// Chuẩn bị Context dữ liệu
	ctxParams := make(map[string]value.Value)
	for k, v := range p {
		ctxParams[k] = value.New(v)
	}

	ctxQuery := make(map[string]value.Value)
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			ctxQuery[k] = value.New(v[0])
		}
	}

	ctx := &Context{Request: r, paramsMap: ctxParams, queryMap: ctxQuery}
	res := &Response{}

	args := []value.Value{value.New(ctx), value.New(res)}

	// Pipeline: Guard -> Handle -> Done
	if rt.guard != nil {
		if !vm.ExecuteLambda(rt.guard, args).Truthy() {
			if rt.fail != nil {
				vm.ExecuteLambda(rt.fail, args)
			}
			res.Write(w, r)
			return
		}
	}

	if rt.handle != nil {
		result := vm.ExecuteLambda(rt.handle, args)
		// Auto-convert kết quả return từ JS
		if result.Truthy() && res.Type() == "" {
			if result.K == value.Map || result.K == value.Array {
				res.JSON(result)
			} else {
				res.HTML(result)
			}
		}
	}

	if rt.done != nil {
		vm.ExecuteLambda(rt.done, args)
	}

	res.Write(w, r)
}

func matchRoute(path, routePath string) (map[string]string, bool) {
	if path == routePath {
		return nil, true
	}
	pS, rS := strings.Split(strings.Trim(path, "/"), "/"), strings.Split(strings.Trim(routePath, "/"), "/")
	if len(pS) != len(rS) {
		return nil, false
	}
	params := make(map[string]string)
	for i := range rS {
		if strings.HasPrefix(rS[i], ":") {
			params[rS[i][1:]] = pS[i]
		} else if rS[i] != pS[i] {
			return nil, false
		}
	}
	return params, true
}
