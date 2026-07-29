package runtime

import (
	"sort"

	"github.com/kitwork/engine/value"
)

func (vm *VM) Defer(fn *value.Lambda) {
	if vm.FrameIdx >= 0 {
		f := &vm.Frames[vm.FrameIdx]
		f.Defers = append(f.Defers, fn)
	}
}

// commit completes an effect after its entire expression has been configured. The effect may return
// a continuation for the current VM, but the runtime has no knowledge of the effect's domain.
func (vm *VM) commit(v value.Value) value.Value {
	effect, ok := v.V.(value.Committer)
	if !ok {
		return v
	}

	var committed value.CommitResult
	if failure := vm.nativeAction("COMMIT", func() {
		committed = effect.Commit()
	}); failure.K == value.Invalid {
		return failure
	}
	if committed.Handler != nil {
		result := vm.ExecuteLambda(
			committed.Handler,
			[]value.Value{committed.Argument},
		)
		if result.K == value.Invalid {
			return result
		}
	}
	return v
}

// lookupScopeChain tìm biến dọc theo chuỗi closure bao ngoài (lexical scoping).
// Cho phép lambda lồng nhiều cấp đọc biến của mọi hàm bao ngoài, đúng ngữ nghĩa JS.
func lookupScopeChain(fn *value.Lambda, name string) (value.Value, bool) {
	for ; fn != nil; fn = fn.Parent {
		if fn.Scope != nil {
			if v, ok := fn.Scope[name]; ok {
				return v, true
			}
		}
	}
	return value.Value{}, false
}

// storeScopeChain ghi đè biến ĐÃ TỒN TẠI ở scope bao ngoài gần nhất (nếu có).
// Trả về true nếu đã ghi — false nghĩa là biến mới, lưu cục bộ tại frame hiện hành.
func storeScopeChain(fn *value.Lambda, name string, val value.Value) bool {
	for ; fn != nil; fn = fn.Parent {
		if fn.Scope != nil {
			if _, ok := fn.Scope[name]; ok {
				fn.Scope[name] = val
				return true
			}
		}
	}
	return false
}

// arrayCallbackMethod xử lý các method Array nhận callback — forEach, some,
// every, findIndex, reduce, sort(comparator) — chỉ VM mới thực thi được Lambda.
// Trả về (kết quả, true) nếu method được xử lý tại đây; ngược lại (zero, false)
// để rơi xuống prototype table (vd: sort() không comparator).
func (vm *VM) arrayCallbackMethod(target value.Value, m string, ivArgs []value.Value) (value.Value, bool) {
	var arr []value.Value
	if ptr, ok := target.V.(*[]value.Value); ok {
		arr = *ptr
	} else if a, ok := target.V.([]value.Value); ok {
		arr = a
	} else {
		return value.Value{}, false
	}

	var cb *value.Lambda
	if len(ivArgs) > 0 && ivArgs[0].K == value.Func {
		cb, _ = ivArgs[0].V.(*value.Lambda)
	}
	if cb == nil {
		return value.Value{}, false
	}

	switch m {
	case "forEach":
		for i, item := range arr {
			result := vm.ExecuteLambda(cb, []value.Value{item, value.New(float64(i))})
			if result.K == value.Invalid {
				return result, true
			}
		}
		// JS forEach trả về undefined
		return value.Value{K: value.Nil}, true

	case "some":
		for i, item := range arr {
			result := vm.ExecuteLambda(cb, []value.Value{item, value.New(float64(i))})
			if result.K == value.Invalid {
				return result, true
			}
			if result.Truthy() {
				return value.TRUE, true
			}
		}
		return value.FALSE, true

	case "every":
		for i, item := range arr {
			result := vm.ExecuteLambda(cb, []value.Value{item, value.New(float64(i))})
			if result.K == value.Invalid {
				return result, true
			}
			if !result.Truthy() {
				return value.FALSE, true
			}
		}
		return value.TRUE, true

	case "findIndex":
		for i, item := range arr {
			result := vm.ExecuteLambda(cb, []value.Value{item, value.New(float64(i))})
			if result.K == value.Invalid {
				return result, true
			}
			if result.Truthy() {
				return value.New(float64(i)), true
			}
		}
		return value.Value{K: value.Number, N: -1}, true

	case "reduce":
		start := 0
		var acc value.Value
		if len(ivArgs) > 1 {
			acc = ivArgs[1]
		} else {
			if len(arr) == 0 {
				return value.Value{K: value.Invalid, V: "reduce: empty array with no initial value"}, true
			}
			acc = arr[0]
			start = 1
		}
		for i := start; i < len(arr); i++ {
			acc = vm.ExecuteLambda(cb, []value.Value{acc, arr[i], value.New(float64(i))})
			if acc.K == value.Invalid {
				return acc, true
			}
		}
		return acc, true

	case "sort":
		// sort(comparator) — sắp xếp tại chỗ, trả về chính mảng (chuẩn JS)
		var failure value.Value
		failed := false
		sortByComparator(arr, func(a, b value.Value) bool {
			if failed {
				return false
			}
			result := vm.ExecuteLambda(cb, []value.Value{a, b})
			if result.K == value.Invalid {
				failure = result
				failed = true
				return false
			}
			return result.N < 0
		})
		if failed {
			return failure, true
		}
		return target, true

	case "group", "groupBy":
		groups := make(map[string]value.Value)
		for i, item := range arr {
			keyVal := vm.ExecuteLambda(cb, []value.Value{item, value.New(float64(i))})
			if keyVal.K == value.Invalid {
				return keyVal, true
			}
			keyStr := keyVal.Text()

			var groupArr []value.Value
			if existing, ok := groups[keyStr]; ok {
				groupArr = *existing.V.(*[]value.Value)
			}
			groupArr = append(groupArr, item)
			groups[keyStr] = value.Value{K: value.Array, V: &groupArr}
		}
		return value.New(groups), true

	case "sortBy":
		type pair struct {
			item value.Value
			key  value.Value
		}
		pairs := make([]pair, len(arr))
		for i, item := range arr {
			key := vm.ExecuteLambda(cb, []value.Value{item, value.New(float64(i))})
			if key.K == value.Invalid {
				return key, true
			}
			pairs[i] = pair{item: item, key: key}
		}
		sort.SliceStable(pairs, func(i, j int) bool {
			ki := pairs[i].key
			kj := pairs[j].key
			if ki.IsNumeric() && kj.IsNumeric() {
				return ki.N < kj.N
			}
			return ki.Text() < kj.Text()
		})
		for i, p := range pairs {
			arr[i] = p.item
		}
		return target, true

	case "unique":
		seen := make(map[any]bool)
		resArr := []value.Value{}
		for i, item := range arr {
			keyVal := vm.ExecuteLambda(cb, []value.Value{item, value.New(float64(i))})
			if keyVal.K == value.Invalid {
				return keyVal, true
			}
			key := keyVal.Interface()
			if !seen[key] {
				seen[key] = true
				resArr = append(resArr, item)
			}
		}
		if ptr, ok := target.V.(*[]value.Value); ok {
			*ptr = resArr
		}
		return target, true
	}

	return value.Value{}, false
}

// sortByComparator — insertion sort ổn định, tránh import sort để giữ vm.go gọn.
// Mảng tenant thường nhỏ; comparator do user cung cấp chạy qua VM.
func sortByComparator(a []value.Value, less func(x, y value.Value) bool) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && less(a[j], a[j-1]); j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

func (vm *VM) currentLine(ip int) int32 {
	return vm.currentLocation(ip).Line
}

func (vm *VM) currentLocation(ip int) SourceLocation {
	if vm == nil || vm.program == nil {
		return SourceLocation{}
	}
	return vm.program.SourceAt(ip)
}
