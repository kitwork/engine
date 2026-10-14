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

// typeFailure is an operator used on kinds it does not take ("a" * 2): TypeCode, its message naming
// the operator and both kinds, never their values (what a program computes with can be a secret).
// Until 14/10/2026 the failure had no code and its whole message was "invalid".
func typeFailure(operator string, a, b Value) Value {
	if a.K == Invalid {
		return a
	}
	if b.K == Invalid {
		return b
	}
	return InvalidFailure(TypeCode, "phép "+operator+" không dùng được giữa "+KindName(a)+" và "+KindName(b))
}

func (a Value) Extend(b Value) Value {
	// a failure stays the failure it is
	if a.IsInvalid() || b.IsInvalid() {
		return typeFailure("+", a, b)
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
		// no kind pair adds up (a number and an array)
		return typeFailure("+", a, b)
	}
}

func (a Value) Sub(b Value) Value {
	if a.K == Number && b.K == Number {
		return Value{K: Number, N: a.N - b.N}
	}
	if a.K == Time && b.K == Duration {
		return Value{K: Time, N: a.N - b.N}
	}
	return typeFailure("-", a, b)
}

func (a Value) Mul(b Value) Value {
	if a.K == Number && b.K == Number {
		return Value{K: Number, N: a.N * b.N}
	}
	return typeFailure("*", a, b)
}

func (a Value) Div(b Value) Value {
	if a.K == Number && b.K == Number {
		if b.N == 0 {
			return Value{K: Nil}
		}
		return Value{K: Number, N: a.N / b.N}
	}
	return typeFailure("/", a, b)
}

func (a Value) Mod(b Value) Value {
	if a.K == Number && b.K == Number {
		if b.N == 0 {
			return Value{K: Nil}
		}
		return Value{K: Number, N: math.Mod(a.N, b.N)}
	}
	return typeFailure("%", a, b)
}

// Deep equality. Two objects or arrays are equal when what they hold is, pair by pair. The pairs wait
// on a list rather than on Go's stack, so two values nested a million deep compare without a stack
// overflow, which no recover() catches (14/10/2026); a pair met again is equal so far, so values that
// hold themselves compare too.
func (a Value) Equal(b Value) bool {
	var waiting [8]equalityPair
	pending := append(waiting[:0], equalityPair{a, b})
	var seen map[equalityVisit]struct{}
	for len(pending) > 0 {
		last := len(pending) - 1
		left, right := pending[last].left, pending[last].right
		pending = pending[:last]
		if left.K != right.K {
			return false
		}
		switch left.K {
		case Array:
			x, y := left.Array(), right.Array()
			if len(x) != len(y) {
				return false
			}
			if same, visited, nextSeen := equalityCollectionVisit(left, right, seen); same || visited {
				continue
			} else {
				seen = nextSeen
			}
			for i := len(x) - 1; i >= 0; i-- {
				pending = append(pending, equalityPair{x[i], y[i]})
			}
		case Map:
			x, y := left.Map(), right.Map()
			if len(x) != len(y) {
				return false
			}
			if same, visited, nextSeen := equalityCollectionVisit(left, right, seen); same || visited {
				continue
			} else {
				seen = nextSeen
			}
			for k, xv := range x {
				yv, ok := y[k]
				if !ok {
					return false
				}
				pending = append(pending, equalityPair{xv, yv})
			}
		default:
			if !equalScalar(left, right) {
				return false
			}
		}
	}
	return true
}

type equalityVisit struct {
	kind        Kind
	left, right uintptr
}

type equalityPair struct {
	left, right Value
}

// equalScalar compares two values of one kind that hold nothing to walk into.
func equalScalar(a, b Value) bool {
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
