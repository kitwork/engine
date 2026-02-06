package value

import (
	"bytes"
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
		return Value{K: Number, N: float64(int64(a.N) % int64(b.N))}
	}
	return Value{K: Invalid}
}

// Deep equality
func (a Value) Equal(b Value) bool {
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
		x, y := a.V.([]Value), b.V.([]Value)
		if len(x) != len(y) {
			return false
		}
		for i := range x {
			if !x[i].Equal(y[i]) {
				return false
			}
		}
		return true
	case Map:
		x, y := a.V.(map[string]Value), b.V.(map[string]Value)
		if len(x) != len(y) {
			return false
		}
		for k, xv := range x {
			yv, ok := y[k]
			if !ok || !xv.Equal(yv) {
				return false
			}
		}
		return true
	default:
		return a.V == b.V
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
