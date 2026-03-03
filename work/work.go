package work

import (
	"fmt"
	"net/http"
	"strings"
	"time"

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

// Router struct is defined in router.go
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

	// 1. Kiểm tra cache
	if matched.cacheTTL > 0 {
		cacheKey := matched.Method + ":" + matched.Path
		t.cacheLock.RLock()
		if cached, ok := t.cache[cacheKey]; ok && time.Now().Before(cached.ExpireAt) {
			t.cacheLock.RUnlock()
			ctxRouter := *matched
			ctxRouter.request = r
			ctxRouter.response = cached.Response
			ctxRouter.responder(w)
			return
		}
		t.cacheLock.RUnlock()
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

	ctxRouter.run(vm, w, matched)
}

func (r *Router) run(vm *runtime.Runtime, w http.ResponseWriter, original *Router) {
	vm.FastReset(r.tenant.bytecode.Instructions, r.tenant.bytecode.Constants, r.tenant.vm.Globals)

	reqObj := &Request{router: r}
	ctxObj := &Context{
		request: reqObj,
	}

	// 1. Chạy Guards (Middleware style)
	for _, guard := range r.guards {
		gArgs := ctxObj.arguments(guard)
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
			hArgs := ctxObj.arguments(r.handle)
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
			fArgs := ctxObj.arguments(r.catch)
			vm.ExecuteLambda(r.catch, fArgs)
		}
	} else {
		if r.then != nil {
			dArgs := ctxObj.arguments(r.then)
			vm.ExecuteLambda(r.then, dArgs)
		}
	}

	// 4. FINALLY: Luôn luôn chạy cuối cùng cho mọi request
	if r.final != nil {
		fArgs := ctxObj.arguments(r.final)
		vm.ExecuteLambda(r.final, fArgs)
	}

	// Gửi phản hồi cuối cùng
	r.responder(w)

	// 4. Lưu cache nếu thành công
	if original != nil && original.cacheTTL > 0 && r.err == nil && r.response.IsSend() {
		cacheKey := original.Method + ":" + original.Path
		r.tenant.cacheLock.Lock()
		r.tenant.cache[cacheKey] = &CachedResult{
			Response: r.response,
			ExpireAt: time.Now().Add(original.cacheTTL),
		}
		r.tenant.cacheLock.Unlock()
	}
}

func matchRoute(path, routePath string) (map[string]string, bool) {
	if path == routePath {
		return nil, true
	}
	pS := strings.Split(strings.Trim(path, "/"), "/")
	rS := strings.Split(strings.Trim(routePath, "/"), "/")

	// Xử lý trường hợp chuỗi rỗng sau khi trim
	if len(pS) == 1 && pS[0] == "" {
		pS = []string{}
	}
	if len(rS) == 1 && rS[0] == "" {
		rS = []string{}
	}

	params := make(map[string]string)
	for i := 0; i < len(rS); i++ {
		// Nếu gặp wildcard *, khớp toàn bộ phần còn lại
		if rS[i] == "*" {
			return params, true
		}

		if i >= len(pS) {
			return nil, false
		}

		if strings.HasPrefix(rS[i], ":") {
			params[rS[i][1:]] = pS[i]
		} else if rS[i] != pS[i] {
			return nil, false
		}
	}

	// Nếu không có wildcard, độ dài phải khớp chính xác
	if len(pS) > len(rS) {
		return nil, false
	}

	return params, true
}
