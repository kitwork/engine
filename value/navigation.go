package value

import (
	"reflect"
	"strings"
)

/* =============================================================================
   5. NAVIGATION & REFLECTION
   ============================================================================= */

func (v Value) Len() int {
	if v.V == nil {
		return 0
	}
	switch v.K {
	case String:
		return len(v.V.(string))
	case Bytes:
		return len(v.V.([]byte))
	case Array:
		if ptr, ok := v.V.(*[]Value); ok {
			return len(*ptr)
		}
		if arr, ok := v.V.([]Value); ok {
			return len(arr)
		}
	case Map:
		if m, ok := v.V.(map[string]Value); ok {
			return len(m)
		}
	}

	// Reflection fallback for robustness
	rv := reflect.ValueOf(v.V)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array || rv.Kind() == reflect.Map {
		return rv.Len()
	}

	return 0
}

func (v Value) Index(i int) Value {
	if !v.IsObject() {
		return Value{K: Nil}
	}
	switch v.K {
	case Array:
		var a []Value
		if ptr, ok := v.V.(*[]Value); ok {
			a = *ptr
		} else {
			a = v.V.([]Value)
		}
		if i >= 0 && i < len(a) {
			return a[i]
		}
	case Bytes:
		b := v.V.([]byte)
		if i >= 0 && i < len(b) {
			return Value{K: Number, N: float64(b[i])}
		}
	case String:
		s := v.V.(string)
		if i >= 0 && i < len(s) {
			return Value{K: String, V: string(s[i])}
		}
	}
	return Value{K: Nil}
}

func (v Value) Map() map[string]Value {
	if m, ok := v.V.(map[string]Value); ok {
		return m
	}
	return nil
}

func (v Value) Array() []Value {
	if ptr, ok := v.V.(*[]Value); ok {
		return *ptr
	}
	if a, ok := v.V.([]Value); ok {
		return a
	}
	return nil
}

func (v Value) Set(key string, val Value) {
	if v.K == Map {
		v.Map()[key] = val
	}
}

func (v Value) Get(key string) Value {
	// JS-Compatibility: .length property
	if key == "length" {
		return New(v.Len())
	}
	if key == "type" {
		return New(v.K.String())
	}

	// ƯU TIÊN 1: Tra cứu Prototype Table (Fix lỗi upper, type, len)
	// Kind.GetMethod sẽ quét map tĩnh đã đăng ký trong InitStandardLibrary
	if fn, ok := v.K.Method(key); ok {
		return Value{K: Func, V: fn}
	}

	// Nếu không phải Prototype, kiểm tra xem có phải Object/Struct không
	if !v.IsObject() {
		return Value{K: Nil} // Trả về Nil để tránh gãy chuỗi (Safety)
	}

	// ƯU TIÊN 2: Tra cứu Dynamic (Reflection hoặc Map)
	switch v.K {
	case Struct:

		return v.reflect(key)
	case Map:
		if m := v.Map(); m != nil {
			if val, ok := m[key]; ok {
				return val
			}
		}
	case Proxy:
		// Tracking keys through a generic proxy
		if d, ok := v.V.(*ProxyData); ok && d.Handler != nil {
			return d.Handler.OnGet(key)
		}
	}

	return Value{K: Nil}
}

// At allows deep path traversal
func (v Value) At(path ...any) Value {
	cur := v
	for _, p := range path {
		switch x := p.(type) {
		case string:
			cur = cur.Get(x)
		case int:
			cur = cur.Index(x)
		default:
			return Value{K: Nil}
		}
		if cur.IsBlank() {
			return cur
		}
	}
	return cur
}

func (v Value) reflect(key string) Value {
	if v.V == nil {
		return Value{K: Nil}
	}

	ptrRv := reflect.ValueOf(v.V)
	ptrRt := ptrRv.Type()

	// Tìm Method (hỗ trợ gọi 'from' cho hàm 'From')
	for i := 0; i < ptrRt.NumMethod(); i++ {
		m := ptrRt.Method(i)
		if strings.EqualFold(m.Name, key) {
			return Value{K: Func, V: ptrRv.Method(i)}
		}
	}

	// Tìm Field (hỗ trợ 'table' cho 'Table')
	rv := ptrRv
	for rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	if rv.Kind() == reflect.Struct {
		rt := rv.Type()
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			// f.PkgPath is empty for exported fields
			if f.PkgPath == "" && strings.EqualFold(f.Name, key) {
				return New(rv.Field(i).Interface())
			}
		}
	}

	return Value{K: Nil}
}
