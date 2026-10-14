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
	case *Lambda:
		return Value{K: Func, V: v}
	default:
		return Parse(i)
	}

}

// NewString - Đã có hàm New(any) trong gói value xử lý cực tốt
func NewString(s string) Value {
	return New(s)
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

// FuncObject là một hàm có kèm thuộc tính tĩnh (static properties),
// mô phỏng hành vi "function là object" của JavaScript.
// Ví dụ: Date vừa gọi được `Date()` vừa truy cập được `Date.now`.
type FuncObject struct {
	Fn    func(args ...Value) Value
	Props map[string]Value
}

func NewFuncObject(fn func(args ...Value) Value, props map[string]Value) Value {
	return Value{K: Func, V: &FuncObject{Fn: fn, Props: props}}
}

func NewNil() Value {
	return Value{K: Nil}
}

// ParseNumber - Chuyển chuỗi thành số thực, nạp vào trường N
func ParseNumber(s string) Value {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return InvalidFailure(InvalidInputCode, "không đọc được số: "+strconv.Quote(s))
	}
	return Value{K: Number, N: f}
}

func ToBool(b bool) Value {
	if b {
		return Value{K: Bool, N: 1}
	}
	return Value{K: Bool, N: 0}
}

// Parse turns Go data into a Value, walking into its slices, arrays and maps. Go data can hold itself
// too: a .ui page's {{ x = $ }} puts its scope, a map[string]any, into itself, and a helper the page
// calls with it hands it here. A map, slice or array met again on its own path, or a nesting past
// MaxDepth, makes the whole value the failure (TYPE, INVALID_ARGUMENT), not a Go stack overflow
// (14/10/2026).
func Parse(i any) Value {
	var w walk
	if id, kind, ok := goWalkID(i); ok {
		w.enter(id, kind, "", rootStep)
	}
	parsed, err := w.parse(i)
	if err != nil {
		return err.Failure("")
	}
	return parsed
}

func (w *walk) parse(i any) (Value, *WalkError) {
	rv := reflect.ValueOf(i)
	for rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return Value{K: Nil}, nil
		}
		rv = rv.Elem()
	}

	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			return Value{K: Bytes, V: rv.Bytes()}, nil
		}
		n := rv.Len()
		out := make([]Value, n)
		for i := 0; i < n; i++ {
			item, err := w.parseItem(rv.Index(i).Interface(), "", i)
			if err != nil {
				return Value{}, err
			}
			out[i] = item
		}
		return Value{K: Array, V: &out}, nil

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
			item, err := w.parseItem(iter.Value().Interface(), key, keyStep)
			if err != nil {
				return Value{}, err
			}
			out[key] = item
		}
		return Value{K: Map, V: out}, nil

	case reflect.Struct:
		return Value{K: Struct, V: i}, nil

	default:
		if rv.CanFloat() {
			return Value{K: Number, N: rv.Float()}, nil
		}
		if rv.CanInt() {
			return Value{K: Number, N: float64(rv.Int())}, nil
		}
		return Value{K: Any, V: i}, nil
	}
}

// parseItem is New of one item a Go slice, array or map holds, held under key or at index: a slice,
// array or map New would parse is walked into on the path w.
func (w *walk) parseItem(item any, key string, index int) (Value, *WalkError) {
	switch item.(type) {
	case nil, string, float64, bool, int, int64, Value:
		return New(item), nil // what JSON and a database row hold most: nothing to walk into
	case []Value, *[]Value, map[string]Value, []byte:
		return New(item), nil // a Value's own storage, or bytes: New takes it as it is
	}
	id, kind, ok := goWalkID(item)
	if !ok {
		return New(item), nil
	}
	if err := w.enter(id, kind, key, index); err != nil {
		return Value{}, err
	}
	parsed, err := w.parse(item)
	w.leave()
	return parsed, err
}

// goWalkID is what the Go map, slice or array item is, for a walk; ok is false for anything else.
// An array is the pointer it is reached through: by itself it is a copy, and cannot hold itself.
func goWalkID(item any) (id walkID, kind Kind, ok bool) {
	rv := reflect.ValueOf(item)
	var through uintptr
	for rv.Kind() == reflect.Ptr && !rv.IsNil() {
		through = rv.Pointer()
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Map:
		if !rv.IsNil() {
			id = walkID{pointer: rv.Pointer(), length: -1}
		}
		return id, Map, true
	case reflect.Slice:
		if rv.Len() > 0 {
			id = walkID{pointer: rv.Pointer(), length: rv.Len()}
		}
		return id, Array, true
	case reflect.Array:
		if through != 0 {
			id = walkID{pointer: through, length: -1}
		}
		return id, Array, true
	}
	return walkID{}, Invalid, false
}
