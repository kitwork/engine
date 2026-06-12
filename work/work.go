package work

import (
	"fmt"
	"net/http"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

var vmPool = sync.Pool{
	New: func() interface{} {
		// Tạo một VM trắng, sẽ được cấu hình lại bằng FastReset
		return runtime.New(nil, nil)
	},
}

// Router struct is defined in router.go
type Config struct {
	root     string
	base     string
	multiple bool
}

func (t *Tenant) Kitwork(vals ...value.Value) *KitWork { return &KitWork{tenant: t} }

type KitWork struct {
	tenant *Tenant
}

func (t *Tenant) Serve(w http.ResponseWriter, r *http.Request) {
	var matched *Router
	var params map[string]string

	path := r.URL.Path
	if t.routes != nil {
		matched, params = t.routes.Match(r.Method, path)
	}

	if matched == nil {
		var notFoundMatched *Router
		var notFoundParams map[string]string
		if t.routes != nil {
			notFoundMatched, notFoundParams = t.routes.Match("NOTFOUND", path)
		}

		if notFoundMatched != nil {
			matched = notFoundMatched
			params = notFoundParams
		} else {
			http.NotFound(w, r)
			return
		}
	}

	// Rate Limiting Check
	if !t.checkRateLimit(matched, r, w) {
		return
	}

	// KHỞI TẠO CONTEXT (Chính là Router copy cho lượt chạy này để tránh race conditions)
	ctxRouter := *matched
	ctxRouter.request = r
	ctxRouter.params = params
	ctxRouter.response = &Response{} // Response riêng cho lượt chạy này
	if matched.response != nil && matched.response.IsSend() {
		ctxRouter.response.kind = matched.response.kind
		ctxRouter.response.data = matched.response.data
		ctxRouter.response.code = matched.response.code
		ctxRouter.response.page = matched.response.page
	}

	// 0. Chạy Start Hook đầu tiên nếu có (Trước khi kiểm tra Cache!)
	if ctxRouter.start != nil {
		vm := vmPool.Get().(*runtime.VM)
		vm.FastReset(t.bytecode.Instructions, t.bytecode.Constants, t.vm.Globals, t.bytecode.SourceMap)
		vm.MaxEnergy = t.MaxEnergy

		reqObj := &Request{router: &ctxRouter}
		ctxObj := &Context{request: reqObj}
		gArgs := ctxObj.arguments(ctxRouter.start)

		result := vm.ExecuteLambda(ctxRouter.start, gArgs)
		vmPool.Put(vm) // Giải phóng VM sớm

		if result.IsInvalid() {
			ctxRouter.err = fmt.Errorf("start hook error: %v", result.V)
			ctxRouter.responder(w)
			return
		}

		// A. Nếu Start tự gửi phản hồi (ctx.json, ...)
		if ctxRouter.response.IsSend() {
			ctxRouter.responder(w)
			return
		}

		// B. Nếu Start trả về false (bị từ chối)
		if result.IsBool() && !result.Truthy() {
			ctxRouter.response.Status(http.StatusForbidden)
			ctxRouter.response.Send(value.New("Request rejected by start hook"))
			ctxRouter.responder(w)
			return
		}
	}

	// 1. Kiểm tra Cold Cache (Bypass VM & Guards) - Chỉ chạy khi route KHÔNG CÓ guards bảo vệ
	if len(ctxRouter.guards) == 0 {
		if ctxRouter.cacheTTL > 0 {
			cacheKey := ctxRouter.Method + ":" + ctxRouter.Path
			t.cacheLock.RLock()
			if cached, ok := t.cache[cacheKey]; ok && time.Now().Before(cached.ExpireAt) {
				t.cacheLock.RUnlock()
				ctxRouter.response = cached.Response
				ctxRouter.responder(w)
				return
			}
			t.cacheLock.RUnlock()
		}

		if ctxRouter.staticTTL > 0 {
			if ctxRouter.serveStaticCache(w, r) {
				return
			}
		}
	}

	if ctxRouter.response != nil && ctxRouter.response.IsSend() {
		ctxRouter.responder(w)
		return
	}

	vm := vmPool.Get().(*runtime.VM)
	defer vmPool.Put(vm)

	ctxRouter.run(vm, w, matched)
}

func (r *Router) run(vm *runtime.VM, w http.ResponseWriter, original *Router) {
	vm.FastReset(r.tenant.bytecode.Instructions, r.tenant.bytecode.Constants, r.tenant.vm.Globals, r.tenant.bytecode.SourceMap)
	vm.MaxEnergy = r.tenant.MaxEnergy

	reqObj := &Request{router: r}
	ctxObj := &Context{
		request: reqObj,
	}

	// 1. Chạy Middlewares (nếu có)
	for _, middleware := range r.middlewares {
		mArgs := ctxObj.arguments(middleware)
		result := vm.ExecuteLambda(middleware, mArgs)

		if result.IsInvalid() {
			r.err = fmt.Errorf("middleware error: %v", result.V)
			break
		}

		// A. Nếu Middleware tự gửi phản hồi (ctx.json, ...)
		if r.response.IsSend() {
			break
		}

		// B. Nếu Middleware chặn bằng cách trả về FALSE
		if result.IsBool() && !result.Truthy() {
			r.err = fmt.Errorf("middleware rejected request")
			break
		}

		// C. Nếu có lỗi từ ctx.error()
		if r.err != nil {
			break
		}
	}

	// 2. Chạy Guards (nếu có) - Chỉ khi chưa có lỗi từ Middleware
	if r.err == nil && !r.response.IsSend() {
		for _, guard := range r.guards {
			gArgs := ctxObj.arguments(guard)
			result := vm.ExecuteLambda(guard, gArgs)

			if result.IsInvalid() {
				r.err = fmt.Errorf("guard error: %v", result.V)
				break
			}

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
	}

	// 3. Kiểm tra Hot Cache (Sau khi đã pass qua Guards thành công) - Chỉ chạy khi có Guards bảo vệ
	if r.err == nil && !r.response.IsSend() && len(r.guards) > 0 {
		if original != nil && original.cacheTTL > 0 {
			cacheKey := original.Method + ":" + original.Path
			r.tenant.cacheLock.RLock()
			if cached, ok := r.tenant.cache[cacheKey]; ok && time.Now().Before(cached.ExpireAt) {
				r.tenant.cacheLock.RUnlock()
				r.response = cached.Response
				r.runFinally(vm, ctxObj)
				r.responder(w)
				return
			}
			r.tenant.cacheLock.RUnlock()
		}

		if original != nil && original.staticTTL > 0 {
			if r.serveStaticCache(w, r.request) {
				r.runFinally(vm, ctxObj)
				return
			}
		}
	}

	// 4. Chạy Handle chính (Chỉ khi chưa có lỗi và chưa gửi response)
	if r.err == nil && !r.response.IsSend() {
		if r.handle != nil {
			hArgs := ctxObj.arguments(r.handle)

			// KIỂM TRA BENCHMARK
			if r.benchmarkCount > 0 {
				var m1, m2 goruntime.MemStats
				goruntime.GC()
				goruntime.ReadMemStats(&m1)

				start := time.Now()
				for i := 0; i < r.benchmarkCount; i++ {
					vm.ExecuteLambda(r.handle, hArgs)
				}
				duration := time.Since(start)
				goruntime.ReadMemStats(&m2)

				// Tính toán chỉ số chi tiết
				allocBytes := m2.TotalAlloc - m1.TotalAlloc
				gcCycles := m2.NumGC - m1.NumGC
				ops := float64(r.benchmarkCount) / duration.Seconds()

				report := map[string]interface{}{
					"iterations1": r.benchmarkCount,
					"duration":    duration.String(),
					"ops_per_sec": fmt.Sprintf("%.0f", ops),
					"avg_latency": (duration / time.Duration(r.benchmarkCount)).String(),
					"memory": map[string]interface{}{
						"total_alloc_mb": fmt.Sprintf("%.2f MB", float64(allocBytes)/1024/1024),
						"alloc_per_op":   fmt.Sprintf("%d bytes", allocBytes/uint64(r.benchmarkCount)),
						"gc_cycles":      gcCycles,
					},
				}

				// Ghi đè Response bằng báo cáo JSON
				r.response.JSON(value.New(report))
				r.responder(w)
				return
			}

			result := vm.ExecuteLambda(r.handle, hArgs)
			if result.IsInvalid() {
				r.err = fmt.Errorf("%v", result.V)
			} else if !r.response.IsSend() && result.Truthy() {
				if result.K == value.Map || result.K == value.Array {
					r.response.JSON(result)
				} else {
					r.response.HTML(result)
				}
			}
		}
	}

	// 5. HẬU XỬ LÝ: Catch (Có lỗi) vs Then (Hoàn tất sạch sẽ)
	if r.err != nil {
		if r.catch != nil {
			fArgs := ctxObj.arguments(r.catch)
			result := vm.ExecuteLambda(r.catch, fArgs)
			if !result.IsInvalid() && !r.response.IsSend() && result.Truthy() {
				if r.response.Code() == 0 {
					r.response.Status(500)
				}
				if result.K == value.Map || result.K == value.Array {
					r.response.JSON(result)
				} else {
					r.response.HTML(result)
				}
			}
		}
	} else {
		if r.then != nil {
			dArgs := ctxObj.arguments(r.then)
			vm.ExecuteLambda(r.then, dArgs)
		}
	}

	// 6. FINALLY: Luôn luôn chạy cuối cùng cho mọi request
	r.runFinally(vm, ctxObj)

	// Gửi phản hồi cuối cùng
	r.responder(w)

	// 7. Lưu cache nếu thành công
	if original != nil && r.err == nil && r.response.IsSend() {
		if original.cacheTTL > 0 {
			cacheKey := original.Method + ":" + original.Path
			r.tenant.cacheLock.Lock()
			r.tenant.cache[cacheKey] = &Responser{
				Response: r.response,
				ExpireAt: time.Now().Add(original.cacheTTL),
			}
			r.tenant.cacheLock.Unlock()
		}

		if original.staticTTL > 0 {
			r.saveStaticCache()
		}
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
		// 1. Xử lý Wildcard *
		if rS[i] == "*" {
			return params, true
		}

		// 2. Kiểm tra nếu là tham số động (:name hoặc :name?)
		if strings.HasPrefix(rS[i], ":") {
			isOptional := strings.HasSuffix(rS[i], "?")
			paramName := rS[i][1:]
			if isOptional {
				paramName = paramName[:len(paramName)-1]
			}

			if i < len(pS) {
				// Nếu có dữ liệu trong path thực tế, gán vào params
				params[paramName] = pS[i]
			} else if !isOptional {
				// Nếu là tham số bắt buộc nhưng không có dữ liệu -> FAIL
				return nil, false
			}
			continue
		}

		// 3. Nếu là đường dẫn tĩnh
		if i >= len(pS) || rS[i] != pS[i] {
			return nil, false
		}
	}

	// Nếu path thực tế dài hơn route định nghĩa và không có wildcard -> FAIL
	if len(pS) > len(rS) {
		return nil, false
	}

	return params, true
}

func (r *Router) runFinally(vm *runtime.VM, ctxObj *Context) {
	if r.final != nil {
		fArgs := ctxObj.arguments(r.final)
		vm.ExecuteLambda(r.final, fArgs)
	}
}
