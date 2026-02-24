package value

import (
	"fmt"
	"reflect"
	"strconv"
	"time"
)

/* =============================================================================
   6. CONSTRUCTORS & NORMALIZATION
   ============================================================================= */

func New(i any) Value {
	if i == nil {
		return Value{K: Nil}
	}

	switch v := i.(type) {
	case Value:
		return v
	case string:
		return Value{K: String, V: v}
	case []byte:
		return Value{K: Bytes, V: v}
	case bool:
		if v {
			return Value{K: Bool, N: 1}
		}
		return Value{K: Bool}
	case int:
		return Value{K: Number, N: float64(v)}
	case int8:
		return Value{K: Number, N: float64(v)}
	case int16:
		return Value{K: Number, N: float64(v)}
	case int32:
		return Value{K: Number, N: float64(v)}
	case int64:
		return Value{K: Number, N: float64(v)}
	case uint:
		return Value{K: Number, N: float64(v)}
	case uint8:
		return Value{K: Number, N: float64(v)}
	case uint16:
		return Value{K: Number, N: float64(v)}
	case uint32:
		return Value{K: Number, N: float64(v)}
	case uint64:
		return Value{K: Number, N: float64(v)}
	case float32:
		return Value{K: Number, N: float64(v)}
	case float64:
		return Value{K: Number, N: v}
	case time.Time:
		return Value{K: Time, N: float64(v.UnixNano())}
	case time.Duration:
		return Value{K: Duration, N: float64(v.Nanoseconds())}
	case []Value:
		return Value{K: Array, V: &v}
	case *[]Value:
		return Value{K: Array, V: v}
	case map[string]Value:
		return Value{K: Map, V: v}
	case *Script:
		return Value{K: Func, V: v}
	default:
		return Parse(i)
	}

}

// NewString - Đã có hàm New(any) trong gói value xử lý cực tốt
func NewString(s string) Value {
	return New(s)
}

func NewSafeHTML(s string) Value {
	return Value{K: String, V: s, S: SafeHTML}
}

// NewBool - Tận dụng hàm ToBool hoặc New(bool) bạn đã viết
func NewBool(b bool) Value {
	return ToBool(b)
}

// NewNull - Khớp với giá trị Nil trong gói của bạn
func NewNull() Value {
	return NewNil()
}

func NewFunc(fn func(args ...Value) Value) Value {
	return Value{K: Func, V: fn}
}

func NewNil() Value {
	return Value{K: Nil}
}

// ParseNumber - Chuyển chuỗi thành số thực, nạp vào trường N
func ParseNumber(s string) Value {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return Value{K: Invalid}
	}
	return Value{K: Number, N: f}
}

func ToBool(b bool) Value {
	if b {
		return Value{K: Bool, N: 1}
	}
	return Value{K: Bool, N: 0}
}

func Parse(i any) Value {
	rv := reflect.ValueOf(i)
	for rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return Value{K: Nil}
		}
		rv = rv.Elem()
	}

	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			return Value{K: Bytes, V: rv.Bytes()}
		}
		n := rv.Len()
		out := make([]Value, n)
		for i := 0; i < n; i++ {
			out[i] = New(rv.Index(i).Interface())
		}
		return Value{K: Array, V: &out}

	case reflect.Map:
		out := make(map[string]Value)
		iter := rv.MapRange()
		for iter.Next() {
			var key string
			rk := iter.Key()
			// PERFORMANCE OPTIMIZATION: Avoid fmt.Sprint for string keys
			if rk.Kind() == reflect.String {
				key = rk.String()
			} else {
				key = fmt.Sprint(rk.Interface())
			}
			out[key] = New(iter.Value().Interface())
		}
		return Value{K: Map, V: out}

	case reflect.Struct:
		return Value{K: Struct, V: i}

	default:
		if rv.CanFloat() {
			return Value{K: Number, N: rv.Float()}
		}
		if rv.CanInt() {
			return Value{K: Number, N: float64(rv.Int())}
		}
		return Value{K: Any, V: i}
	}
}
