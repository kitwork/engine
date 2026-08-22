package value

import (
	"bytes"
	"math"
	"reflect"
)

/* =============================================================================
   4. ARITHMETIC & COMPARISON
   ============================================================================= */

func (a Value) Add(others ...Value) Value {
	res := a
	for _, b := range others {
		// Nếu res đã bị Invalid từ bước trước, trả về Invalid luôn
		if res.K == Invalid {
			return res
		}

		if res.K == Number && b.K == Number {
			res = Value{K: Number, N: res.N + b.N}
		} else {
			res = res.Extend(b)
		}
	}
	return res
}

func (a Value) Extend(b Value) Value {
	// Nếu một trong hai là Invalid, kết quả phải là Invalid để báo lỗi mạch toán
	if a.IsInvalid() || b.IsInvalid() {
		return Value{K: Invalid}
	}

	switch {
	// Ưu tiên cộng chuỗi (String Concatenation)
	case a.K == String || b.K == String:
		return Value{K: String, V: a.Text() + b.Text()}

	// Xử lý khi cộng với Nil (Coi Nil như giá trị trung hòa)
	case a.IsNil():
		return b
	case b.IsNil():
		return a

	case a.K == Time && b.K == Duration:
		return Value{K: Time, N: a.N + b.N}
	case a.K == Time && b.K == Number:
		return Value{K: Time, N: a.N + b.N*1e9}

	default:
		// Nếu không khớp kiểu dữ liệu nào (ví dụ: cộng Number với Array)
		return Value{K: Invalid}
	}
}

func (a Value) Sub(b Value) Value {
	if a.K == Number && b.K == Number {
		return Value{K: Number, N: a.N - b.N}
	}
	if a.K == Time && b.K == Duration {
		return Value{K: Time, N: a.N - b.N}
	}
	return Value{K: Invalid}
}

func (a Value) Mul(b Value) Value {
	if a.K == Number && b.K == Number {
		return Value{K: Number, N: a.N * b.N}
	}
	return Value{K: Invalid}
}

func (a Value) Div(b Value) Value {
	if a.K == Number && b.K == Number {
		if b.N == 0 {
			return Value{K: Nil}
		}
		return Value{K: Number, N: a.N / b.N}
	}
	return Value{K: Invalid}
}

func (a Value) Mod(b Value) Value {
	if a.K == Number && b.K == Number {
		if b.N == 0 {
			return Value{K: Nil}
		}
		return Value{K: Number, N: math.Mod(a.N, b.N)}
	}
	return Value{K: Invalid}
}

// Deep equality
func (a Value) Equal(b Value) bool {
	return equalValue(a, b, nil)
}

type equalityVisit struct {
	kind        Kind
	left, right uintptr
}

func equalValue(a, b Value, seen map[equalityVisit]struct{}) bool {
	if a.K != b.K {
		return false
	}
	switch a.K {
	case Number:
		diff := a.N - b.N
		if diff < 0 {
			diff = -diff
		}
		return diff < 1e-12
	case Bool, Time, Duration:
		return a.N == b.N
	case String:
		return a.String() == b.String()
	case Nil:
		return true
	case Bytes:
		return bytes.Equal(a.Bytes(), b.Bytes())
	case Array:
		x, y := a.Array(), b.Array()
		if len(x) != len(y) {
			return false
		}
		if same, visited, nextSeen := equalityCollectionVisit(a, b, seen); same || visited {
			return true
		} else {
			seen = nextSeen
		}
		for i := range x {
			if !equalValue(x[i], y[i], seen) {
				return false
			}
		}
		return true
	case Map:
		x, y := a.Map(), b.Map()
		if len(x) != len(y) {
			return false
		}
		if same, visited, nextSeen := equalityCollectionVisit(a, b, seen); same || visited {
			return true
		} else {
			seen = nextSeen
		}
		for k, xv := range x {
			yv, ok := y[k]
			if !ok || !equalValue(xv, yv, seen) {
				return false
			}
		}
		return true
	default:
		if a.V == nil || b.V == nil {
			return a.V == nil && b.V == nil
		}
		leftType := reflect.TypeOf(a.V)
		if leftType != reflect.TypeOf(b.V) {
			return false
		}
		if leftType.Comparable() {
			return a.V == b.V
		}
		return reflect.DeepEqual(a.V, b.V)
	}
}

func equalityCollectionVisit(
	a, b Value,
	seen map[equalityVisit]struct{},
) (same, visited bool, next map[equalityVisit]struct{}) {
	left := equalityCollectionReference(a)
	right := equalityCollectionReference(b)
	if left == 0 || right == 0 {
		return false, false, seen
	}
	if left == right {
		return true, false, seen
	}
	visit := equalityVisit{kind: a.K, left: left, right: right}
	if _, ok := seen[visit]; ok {
		return false, true, seen
	}
	if seen == nil {
		seen = make(map[equalityVisit]struct{})
	}
	seen[visit] = struct{}{}
	return false, false, seen
}

func equalityCollectionReference(item Value) uintptr {
	switch data := item.V.(type) {
	case *[]Value:
		return reflect.ValueOf(data).Pointer()
	case []Value:
		return reflect.ValueOf(data).Pointer()
	case map[string]Value:
		return reflect.ValueOf(data).Pointer()
	default:
		return 0
	}
}

func (a Value) Less(b Value) bool {
	if a.K <= Duration && b.K <= Duration {
		return a.N < b.N
	}
	if a.K == String && b.K == String {
		return a.String() < b.String()
	}
	return false
}

func (a Value) NotEqual(b Value) bool     { return !a.Equal(b) }
func (a Value) Greater(b Value) bool      { return b.Less(a) }
func (a Value) LessEqual(b Value) bool    { return !b.Less(a) }
func (a Value) GreaterEqual(b Value) bool { return !a.Less(b) }
