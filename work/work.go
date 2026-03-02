package work

import (
	"fmt"
	"net/http"
	"strings"

	"sync"

	"github.com/kitwork/engine/runtime"
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
}

func NewEntity(identity string, domain string) *Entity {
	return &Entity{
		Identity: identity,
		Domain:   domain,
	}
}

type Config struct {
	source string
}

func (t *Tenant) Config(vals ...value.Value) *KitWork { return &KitWork{tenant: t} }

type KitWork struct {
	tenant *Tenant
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
		ctxRouter := *matched
		ctxRouter.request = r
		ctxRouter.responder(w)
		return
	}

	vm := vmPool.Get().(*runtime.Runtime)
	defer vmPool.Put(vm)

	// KHỞI TẠO CONTEXT (Chính là Router copy)
	ctxRouter := *matched
	ctxRouter.request = r
	ctxRouter.params = params
	ctxRouter.response = &Response{} // Response riêng cho lượt chạy này

	ctxRouter.run(vm, w)
}

func (r *Router) run(vm *runtime.Runtime, w http.ResponseWriter) {
	vm.FastReset(r.tenant.bytecode.Instructions, r.tenant.bytecode.Constants, r.tenant.vm.Globals)

	reqObj := &Request{router: r}
	ctxObj := &Context{
		request: reqObj,
	}

	// 1. Chạy Guards (Middleware style)
	for _, guard := range r.guards {
		gArgs := ctxObj.argsLambda(guard)
		result := vm.ExecuteLambda(guard, gArgs)

		// A. Nếu Guard tự gửi phản hồi (ctx.json, ...)
		if r.response.IsSend() {
			break
		}

		// B. Nếu Guard CỐ Ý chặn bằng cách trả về FALSE
		if result.IsBool() && !result.Truthy() {
			r.err = fmt.Errorf("guard rejected request")
			break
		}

		// C. Nếu Guard Trả về DỮ LIỆU (Object/String) -> Auto Response & Break
		if !result.IsBlank() && !result.IsBool() {
			r.response.Send(result) // Tự động nhận diện JSON/Text
			break
		}

		// D. Nếu có lỗi từ ctx.error()
		if r.err != nil {
			break
		}
	}

	// 2. Chạy Handle chính (Chỉ khi chưa có lỗi và chưa gửi response)
	if r.err == nil && !r.response.IsSend() {
		if r.handle != nil {
			hArgs := ctxObj.argsLambda(r.handle)
			result := vm.ExecuteLambda(r.handle, hArgs)
			// Tự động nhận diện kết quả trả về của Handle
			if !r.response.IsSend() && result.Truthy() {
				if result.K == value.Map || result.K == value.Array {
					r.response.JSON(result)
				} else {
					r.response.HTML(result)
				}
			}
		}
	}

	// 3. HẬU XỬ LÝ: Catch (Có lỗi) vs Then (Hoàn tất sạch sẽ)
	if r.err != nil {
		if r.catch != nil {
			fArgs := ctxObj.argsLambda(r.catch)
			vm.ExecuteLambda(r.catch, fArgs)
		}
	} else {
		if r.then != nil {
			dArgs := ctxObj.argsLambda(r.then)
			vm.ExecuteLambda(r.then, dArgs)
		}
	}

	// 4. FINALLY: Luôn luôn chạy cuối cùng cho mọi request
	if r.final != nil {
		fArgs := ctxObj.argsLambda(r.final)
		vm.ExecuteLambda(r.final, fArgs)
	}

	// Gửi phản hồi cuối cùng
	r.responder(w)
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
