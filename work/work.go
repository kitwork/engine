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

	// 1.5 Kiểm tra static cache (disk-based)
	if matched.staticTTL > 0 {
		ctxRouter := *matched
		ctxRouter.request = r
		if ctxRouter.serveStaticCache(w, r) {
			return
		}
	}

	if matched.response != nil && matched.response.IsSend() {
		ctxRouter := *matched
		ctxRouter.request = r
		ctxRouter.responder(w)
		return
	}

	vm := vmPool.Get().(*runtime.VM)
	defer vmPool.Put(vm)

	// KHỞI TẠO CONTEXT (Chính là Router copy)
	ctxRouter := *matched
	ctxRouter.request = r
	ctxRouter.params = params
	ctxRouter.response = &Response{} // Response riêng cho lượt chạy này

	ctxRouter.run(vm, w, matched)
}

func (r *Router) run(vm *runtime.VM, w http.ResponseWriter, original *Router) {
	vm.FastReset(r.tenant.bytecode.Instructions, r.tenant.bytecode.Constants, r.tenant.vm.Globals, r.tenant.bytecode.SourceMap)
	vm.MaxEnergy = r.tenant.MaxEnergy

	reqObj := &Request{router: r}
	ctxObj := &Context{
		request: reqObj,
	}

	// 1. Chạy Guards (Middleware style)
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

	// 2. Chạy Handle chính (Chỉ khi chưa có lỗi và chưa gửi response)
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
					"iterations":  r.benchmarkCount,
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

	// 3. HẬU XỬ LÝ: Catch (Có lỗi) vs Then (Hoàn tất sạch sẽ)
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

	// 4. FINALLY: Luôn luôn chạy cuối cùng cho mọi request
	if r.final != nil {
		fArgs := ctxObj.arguments(r.final)
		vm.ExecuteLambda(r.final, fArgs)
	}

	// Gửi phản hồi cuối cùng
	r.responder(w)

	// 4. Lưu cache nếu thành công
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
