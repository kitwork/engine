package runtime

import (
	"math"

	"github.com/kitwork/engine/value"
)

// MaxArrayGap is the most nulls one a[i] = v adds between an array's last item and item i (about 24 MB
// of items). Past it the write is the INVALID_ARGUMENT failure, as "x".repeat(n) is past 8 MB: a sparse
// key belongs in an object.
const MaxArrayGap = 1 << 20

// TypeofMethod is the method name typeof x compiles to (x; PUSH "#typeof"; INVOKE 0). The VM answers it
// before any key or method of x, with JavaScript's typeof (value.TypeOf). A source cannot write # in a
// name after a dot, so nothing but typeof reaches it; the instruction set did not change (14/10/2026).
const TypeofMethod = "#typeof"

// setArrayItem is a[key] = item on an array, as JavaScript sets an array's item (14/10/2026; until then a
// key that was not an index from 0 to the last appended item, whatever it was):
//
//   - a whole number from 0 to the last index replaces that item, and the length appends one (an array
//     literal fills itself this way);
//   - a whole number past the length pads the gap with null, as JavaScript leaves holes that read
//     undefined (Kitwork's null): [1, 2] with a[4] = 5 is [1, 2, null, null, 5]. Each null costs one unit
//     of energy, and more than MaxArrayGap of them is the INVALID_ARGUMENT failure;
//   - any other key (a negative or fractional number, NaN, a string, "2" included, a boolean, null) is the
//     UNSUPPORTED failure: JavaScript keeps such a value as a property of the array object, and an array
//     of Kitwork has none (a read of a["2"] or a.x gives null), so the value would be lost.
//
// It returns a failure (an Invalid value) or null. The message never shows the key or the value: either
// may be a secret a request carried.
func (vm *VM) setArrayItem(target value.Value, key, item value.Value, ip int) value.Value {
	items := target.V.(*[]value.Value)
	length := float64(len(*items))
	index := key.N
	switch {
	case key.K != value.Number || index != index || index < 0:
		return notAnArrayIndex(key)
	case index-length > MaxArrayGap:
		return value.InvalidFailure(value.InvalidArgumentCode,
			"chỉ số cách cuối mảng hơn 1048576 phần tử, mảng sẽ phải đệm ngần ấy null: cần khoá thưa như vậy thì dùng object { }")
	case math.Trunc(index) != index:
		return notAnArrayIndex(key)
	case index < length:
		(*items)[int(index)] = item
	case index == length:
		*items = append(*items, item)
	default:
		gap := int(index) - len(*items)
		if !vm.consumeEnergy(Cost(gap)) {
			return vm.diagnosticValue(DiagnosticEnergyLimit, "Energy Limit Exceeded: Execution halted", ip)
		}
		for ; gap > 0; gap-- {
			*items = append(*items, value.Value{K: value.Nil})
		}
		*items = append(*items, item)
	}
	return value.Value{K: value.Nil}
}

// notAnArrayIndex is the failure of a key an array has no item for, naming its kind, never its value.
func notAnArrayIndex(key value.Value) value.Value {
	what := value.KindName(key)
	switch {
	case key.K != value.Number:
	case key.N != key.N:
		what = "NaN"
	case key.N < 0:
		what = "số âm"
	default:
		what = "số không nguyên"
	}
	return value.InvalidFailure(value.UnsupportedCode,
		"mảng chỉ nhận chỉ số là số nguyên từ 0 trở lên, ở đây là "+what+
			": mảng không có thuộc tính như object của JavaScript; cần khoá như vậy thì dùng object { }")
}
