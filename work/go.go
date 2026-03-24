package work

import (
	"fmt"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)


func (k *KitWork) Go(fn value.Value, args ...value.Value) *KitWork {
	if fn.IsCallable() {
		// Tạo một VM riêng cho chạy background để tránh xung đột với luồng chính
		// Lấy VM từ pool hoặc tạo mới
		vm := vmPool.Get().(*runtime.Runtime)

		go func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Printf("[Background Task] Panic: %v\n", r)
				}
				vmPool.Put(vm)
			}()

			// Khởi tạo VM với state của tenant hiện tại (Bytecode & Globals)
			vm.FastReset(k.tenant.bytecode.Instructions, k.tenant.bytecode.Constants, k.tenant.vm.Globals)

			// TỐI ƯU: Copy các biến top-level (như log, router) từ tenant vào VM background
			// Điều này giúp các closure lồng nhau có thể truy cập được các biến môi trường
			for key, val := range k.tenant.vm.Vars {
				vm.Vars[key] = val
			}

			if lambda, ok := fn.V.(*value.Lambda); ok {
				// Thực thi lambda JS
				vm.ExecuteLambda(lambda, args)
			} else {
				// Thực thi Go Function hoặc Method
				fn.Call("go", args...)
			}
		}()
	}
	return k
}

