package runtime

import (
	"math"
	"reflect"
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
		result := vm.executeLambda1(committed.Handler, committed.Argument)
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

// arrayCallbackMethod executes every Array method that accepts a script lambda.
// Keeping callback execution here gives all methods the same failure, energy,
// cancellation, and frame-restoration semantics.
// Trả về (kết quả, true) nếu method được xử lý tại đây; ngược lại (zero, false)
// để rơi xuống prototype table (vd: sort() không comparator).
func (vm *VM) arrayCallbackMethod(
	target value.Value,
	m string,
	cb *value.Lambda,
	initial value.Value,
	hasInitial bool,
) (value.Value, bool) {
	var arr []value.Value
	if ptr, ok := target.V.(*[]value.Value); ok {
		arr = *ptr
	} else if a, ok := target.V.([]value.Value); ok {
		arr = a
	} else {
		return value.Value{}, false
	}

	if cb == nil {
		return value.Value{}, false
	}

	switch m {
	case "map":
		result := make([]value.Value, len(arr))
		for i, item := range arr {
			result[i] = vm.executeLambda2(cb, item, arrayIndex(i))
			if result[i].K == value.Invalid {
				return result[i], true
			}
		}
		return value.New(result), true

	case "filter":
		result := make([]value.Value, 0, len(arr))
		for i, item := range arr {
			matched := vm.executeLambda2(cb, item, arrayIndex(i))
			if matched.K == value.Invalid {
				return matched, true
			}
			if matched.Truthy() {
				result = append(result, item)
			}
		}
		return value.New(result), true

	case "find":
		for i, item := range arr {
			matched := vm.executeLambda2(cb, item, arrayIndex(i))
			if matched.K == value.Invalid {
				return matched, true
			}
			if matched.Truthy() {
				return item, true
			}
		}
		return value.Value{K: value.Nil}, true

	case "forEach":
		for i, item := range arr {
			result := vm.executeLambda2(cb, item, arrayIndex(i))
			if result.K == value.Invalid {
				return result, true
			}
		}
		// JS forEach trả về undefined
		return value.Value{K: value.Nil}, true

	case "some":
		for i, item := range arr {
			result := vm.executeLambda2(cb, item, arrayIndex(i))
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
			result := vm.executeLambda2(cb, item, arrayIndex(i))
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
			result := vm.executeLambda2(cb, item, arrayIndex(i))
			if result.K == value.Invalid {
				return result, true
			}
			if result.Truthy() {
				return arrayIndex(i), true
			}
		}
		return value.Value{K: value.Number, N: -1}, true

	case "reduce":
		start := 0
		var acc value.Value
		if hasInitial {
			acc = initial
		} else {
			if len(arr) == 0 {
				return value.Value{K: value.Invalid, V: "reduce: empty array with no initial value"}, true
			}
			acc = arr[0]
			start = 1
		}
		for i := start; i < len(arr); i++ {
			acc = vm.executeLambda3(cb, acc, arr[i], arrayIndex(i))
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
			result := vm.executeLambda2(cb, a, b)
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
			keyVal := vm.executeLambda2(cb, item, arrayIndex(i))
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
			key := vm.executeLambda2(cb, item, arrayIndex(i))
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
		seen := make(map[arrayUniqueKey]struct{}, len(arr))
		resArr := make([]value.Value, 0, len(arr))
		for i, item := range arr {
			keyVal := vm.executeLambda2(cb, item, arrayIndex(i))
			if keyVal.K == value.Invalid {
				return keyVal, true
			}
			key := makeArrayUniqueKey(keyVal)
			if _, exists := seen[key]; !exists {
				seen[key] = struct{}{}
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

func isArrayCallbackMethod(method string) bool {
	switch method {
	case "map",
		"filter",
		"find",
		"forEach",
		"some",
		"every",
		"findIndex",
		"reduce",
		"sort",
		"group",
		"groupBy",
		"sortBy",
		"unique":
		return true
	default:
		return false
	}
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

func arrayIndex(index int) value.Value {
	return value.Value{K: value.Number, N: float64(index)}
}

type arrayUniqueKey struct {
	kind     value.Kind
	number   float64
	identity any
}

func makeArrayUniqueKey(item value.Value) arrayUniqueKey {
	key := arrayUniqueKey{kind: item.K, number: item.N}
	switch item.K {
	case value.Number:
		if math.IsNaN(item.N) {
			key.identity = "NaN"
			key.number = 0
		}
	case value.String:
		key.identity = item.Text()
	case value.Bytes:
		key.identity = string(item.Bytes())
	case value.Array, value.Map, value.Func, value.Proxy:
		key.identity = valueIdentity(item.V)
	default:
		if item.V != nil {
			itemType := reflect.TypeOf(item.V)
			if itemType.Comparable() {
				key.identity = item.V
			} else {
				key.identity = valueIdentity(item.V)
			}
		}
	}
	return key
}

func valueIdentity(item any) uintptr {
	if item == nil {
		return 0
	}
	reflected := reflect.ValueOf(item)
	switch reflected.Kind() {
	case reflect.Chan,
		reflect.Func,
		reflect.Map,
		reflect.Pointer,
		reflect.Slice,
		reflect.UnsafePointer:
		return reflected.Pointer()
	default:
		return 0
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
