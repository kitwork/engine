package work

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"sync"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/script"
	"github.com/kitwork/engine/value"
)

var vmPool = sync.Pool{
	New: func() interface{} {
		// Tạo một VM trắng, sẽ được cấu hình lại bằng FastReset
		return runtime.New(nil, nil)
	},
}

type Entity struct {
	Identity string
	Domain   string
	public   string
}

func (t *Entity) path(path string) string {
	return filepath.Join(t.public, t.Identity, t.Domain, strings.TrimPrefix(path, "/"))
}

func (t *Entity) appfile(filenames ...string) string {
	file := "app.js"
	if len(filenames) > 0 {
		file = strings.TrimPrefix(filenames[0], "/")
	}
	return filepath.Join(t.public, t.Identity, t.Domain, file)
}

func NewEntity(identity string, domain string) *Entity {
	return &Entity{
		Identity: identity,
		Domain:   domain,
	}
}

type Config struct {
	public string
}

// Tenant đại diện cho một instance ứng dụng (khách hàng) hoàn chỉnh
type Tenant struct {
	source   string
	entity   *Entity
	bytecode *compiler.Bytecode
	vm       *runtime.Runtime
	routes   []*Router
}

func NewTenant(source string, domain string) (*Tenant, error) {
	identity := "test"
	entity := &Entity{
		public:   source,
		Identity: identity,
		Domain:   domain,
	}
	bytecode, err := script.Bytecode(entity.appfile())
	if err != nil {
		return nil, err
	}

	tenant := &Tenant{
		entity:   entity,
		bytecode: bytecode,
		vm:       runtime.New(bytecode.Instructions, bytecode.Constants),
		routes:   make([]*Router, 0),
	}

	// TỐI ƯU SIÊU CẤP: Đăng ký kitwork vào Builtin Index 0
	kitworkFunc := value.NewFunc(func(args ...value.Value) value.Value {
		return value.New(tenant.Config(args...))
	})
	tenant.vm.Builtins = []value.Value{kitworkFunc}

	// Giữ lại trong Globals cho các trường hợp đặc biệt
	tenant.vm.Globals["kitwork"] = kitworkFunc

	tenant.vm.Run()
	return tenant, nil
}

func (t *Tenant) Config(vals ...value.Value) *KitWork { return &KitWork{tenant: t} }

type KitWork struct {
	tenant *Tenant
}

type Log struct {
	tenant *Tenant
}

func (l *Log) Print(v value.Value) { fmt.Println(v) }

func (w *KitWork) Log() *Log { return &Log{tenant: w.tenant} }

type Router struct {
	tenant   *Tenant
	Method   string
	Path     string
	basePath string

	guards []*value.Lambda
	handle *value.Lambda
	done   *value.Lambda
	fail   *value.Lambda

	response *Response
	request  *http.Request
	params   map[string]string
}

// --- WRAPPERS CHO JAVASCRIPT ---

type Request struct {
	router *Router
}

func (r *Request) Query(key string) value.Value {
	if r.router.request == nil {
		return value.Value{K: value.Nil}
	}
	return value.New(r.router.request.URL.Query().Get(key))
}

func (r *Request) Param(key string) value.Value {
	if v, ok := r.router.params[key]; ok {
		return value.New(v)
	}
	return value.Value{K: value.Nil}
}

// Context HUB
type Context struct {
	Request  *Request
	Response *Response
}

func (c *Context) JSON(v value.Value) *Context  { c.Response.JSON(v); return c }
func (c *Context) HTML(v value.Value) *Context  { c.Response.HTML(v); return c }
func (c *Context) Status(v int) *Context        { c.Response.Status(v); return c }
func (c *Context) Param(key string) value.Value { return c.Request.Param(key) }
func (c *Context) Query(key string) value.Value { return c.Request.Query(key) }
func (c *Context) Context() *Context            { return c }

// --- ENGINE LOGIC ---

func (w *KitWork) Router() *Router { return &Router{tenant: w.tenant} }

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
func (r *Router) Done(l value.Value) *Router { r.done, _ = l.V.(*value.Lambda); return r }
func (r *Router) Fail(l value.Value) *Router { r.fail, _ = l.V.(*value.Lambda); return r }

func (r *Router) Base(prefix string) *Router {
	newGroup := *r
	newGroup.basePath = "/" + strings.Trim(prefix, "/")
	return &newGroup
}

func (r *Router) File(path string) *Router {
	r.response.File(r.tenant.entity.path(path))
	return r
}

func (r *Router) Directory(path string) *Router {
	r.response.Directory(r.tenant.entity.path(path))
	return r
}

func (r *Router) Redirect(url string) *Router {
	r.response.Redirect(value.New(url))
	return r
}

func (t *Tenant) Serve(w http.ResponseWriter, r *http.Request) {
	var matched *Router
	var params map[string]string

	path := r.URL.Path
	for _, rt := range t.routes {
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

	if matched.response != nil && matched.response.IsSend() {
		matched.response.Write(w, r)
		return
	}

	t.execute(w, r, matched, params)
}

// Hàm trợ giúp tiêm dữ liệu dựa trên tên tham số
func buildArgs(lambda *value.Lambda, reqVal, resVal, ctxVal value.Value) []value.Value {
	if lambda == nil {
		return nil
	}
	args := make([]value.Value, 0, len(lambda.Params))
	for i, name := range lambda.Params {
		lower := strings.ToLower(name)
		switch lower {
		case "ctx", "context":
			args = append(args, ctxVal)
		case "req", "request":
			args = append(args, reqVal)
		case "res", "response":
			args = append(args, resVal)
		default:
			// Fallback theo vị trí nếu tên không khớp
			if i == 0 {
				args = append(args, reqVal)
			} else if i == 1 {
				args = append(args, resVal)
			} else if i == 2 {
				args = append(args, ctxVal)
			} else {
				args = append(args, value.Value{K: value.Nil})
			}
		}
	}
	return args
}

func (t *Tenant) execute(w http.ResponseWriter, r *http.Request, rt *Router, p map[string]string) {
	vm := vmPool.Get().(*runtime.Runtime)
	defer vmPool.Put(vm) // Dùng xong trả lại hồ

	// Reset VM với Bytecode của Tenant và Globals của Tenant
	vm.FastReset(t.bytecode.Instructions, t.bytecode.Constants, t.vm.Globals)

	ctxRouter := *rt
	ctxRouter.request = r
	ctxRouter.params = p
	ctxRouter.response = &Response{}

	reqWrapper := value.New(&Request{router: &ctxRouter})
	resWrapper := value.New(ctxRouter.response)
	ctxWrapper := value.New(&Context{Request: reqWrapper.V.(*Request), Response: resWrapper.V.(*Response)})

	// 1. Chạy Guards
	for _, guard := range rt.guards {
		gArgs := buildArgs(guard, reqWrapper, resWrapper, ctxWrapper)
		if !vm.ExecuteLambda(guard, gArgs).Truthy() || ctxRouter.response.IsSend() {
			if !ctxRouter.response.IsSend() {
				if rt.fail != nil {
					fArgs := buildArgs(rt.fail, reqWrapper, resWrapper, ctxWrapper)
					vm.ExecuteLambda(rt.fail, fArgs)
				}
			}
			ctxRouter.response.Write(w, r)
			return
		}
	}

	if ctxRouter.response.IsSend() {
		ctxRouter.response.Write(w, r)
		return
	}

	// 2. Chạy Handle chính
	if rt.handle != nil {
		hArgs := buildArgs(rt.handle, reqWrapper, resWrapper, ctxWrapper)
		result := vm.ExecuteLambda(rt.handle, hArgs)
		if !ctxRouter.response.IsSend() && result.Truthy() {
			if result.K == value.Map || result.K == value.Array {
				ctxRouter.response.JSON(result)
			} else {
				ctxRouter.response.HTML(result)
			}
		}
	}

	if rt.done != nil {
		dArgs := buildArgs(rt.done, reqWrapper, resWrapper, ctxWrapper)
		vm.ExecuteLambda(rt.done, dArgs)
	}

	ctxRouter.response.Write(w, r)
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
