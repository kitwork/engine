package value

import (
	"reflect"
	"strings"
	"sync"
	"unicode/utf8"
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
	if v.K == Invalid {
		return v
	}
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

func (v Value) First() Value {
	return v.Index(0)
}

func (v Value) Last() Value {
	return v.Index(v.Len() - 1)
}

func (v Value) One() Value {
	if v.K == Invalid {
		return v
	}
	if v.K == Array {
		return v.First()
	}
	if v.K == Map {
		m := v.Map()
		// Return the first value found (maps are unordered anyway)
		for _, val := range m {
			return val
		}
	}
	return v
}

func (v Value) Set(key string, val Value) {
	if v.K == Map {
		v.Map()[key] = val
	}
}

func (v Value) Get(key string) Value {
	if v.K == Invalid {
		// An errored value (a failed db query, fail("…") / new Error("…")) is K==Invalid carrying its
		// message in .V. Expose a small, deliberate surface so it reads like an error object AND can
		// be caught — while every OTHER access stays Invalid so a bare value keeps bubbling:
		//   .safe() rescues it into a capturable shape; .message is the text; .isError true.
		switch key {
		case "safe":
			if fn, ok := v.K.Method(key); ok {
				return Value{K: Func, V: fn}
			}
		case "message":
			if s, ok := v.V.(string); ok {
				return New(s)
			}
		case "isError":
			return New(true)
		}
		return v
	}
	// JS-Compatibility: .length property
	if key == "length" {
		// Với String, đếm theo KÝ TỰ (rune) — "Phường".length == 6,
		// khớp trực giác JS thay vì số byte UTF-8.
		if v.K == String {
			return New(utf8.RuneCountInString(v.V.(string)))
		}
		return New(v.Len())
	}
	if key == "isError" && (v.K == Array || v.K == Map || v.IsError) {
		return New(v.IsError)
	}
	if key == "error" && (v.K == Array || v.K == Map || v.IsError) {
		if v.ErrorVal != nil {
			return New(v.ErrorVal)
		}
		if v.K == Array || v.K == Map {
			return Value{K: Nil}
		}
	}

	// ƯU TIÊN 0: Thuộc tính tĩnh của FuncObject (vd: Date.now, Date.parse)
	if v.K == Func {
		if fo, ok := v.V.(*FuncObject); ok {
			if prop, found := fo.Props[key]; found {
				return prop
			}
		}
	}

	// ƯU TIÊN 1: Tra cứu Dynamic (Struct/Func) - Cho phép gọi field của một Hàm (Object-like Function)
	if v.K == Struct || v.K == Func {
		res := v.reflect(key)
		if res.K != Nil {
			return res
		}
	}

	// An object's own `type` field is data, not the generic kind helper. This is especially
	// important for metadata objects (`$.meta.type`).
	if key == "type" && v.K == Map {
		if item, ok := v.Map()[key]; ok {
			return item
		}
	}

	// Plain values expose `.type` as a kind name. Struct methods and map fields take precedence so
	// APIs such as ctx.type(...) and data objects with a `type` field remain usable.
	if key == "type" {
		return New(v.K.String())
	}

	// ƯU TIÊN 2: Tra cứu Prototype Table (Fix lỗi upper, type, len)
	if fn, ok := v.K.Method(key); ok {
		return Value{K: Func, V: fn}
	}

	if !v.IsObject() {
		return Value{K: Nil}
	}

	// ƯU TIÊN 3: Tra cứu Map/Proxy còn lại
	switch v.K {
	case Map:
		if m := v.Map(); m != nil {
			if val, ok := m[key]; ok {
				return val
			}
		}
	case Proxy:
		// Tracking keys through a generic proxy
		if handler, ok := v.V.(ProxyHandler); ok {
			return handler.OnGet(key)
		}
	}

	return Value{K: Nil}
}

// At allows deep path traversal
func (v Value) At(path ...any) Value {
	cur := v
	for _, p := range path {
		if cur.K == Invalid {
			return cur
		}
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

type reflectedMethod struct {
	index  int
	getter bool
}

type reflectedMembers struct {
	methods map[string]reflectedMethod
	fields  map[string]int
}

var reflectedMemberCache sync.Map // map[reflect.Type]*reflectedMembers

func membersFor(reflectedType reflect.Type) *reflectedMembers {
	if cached, ok := reflectedMemberCache.Load(reflectedType); ok {
		return cached.(*reflectedMembers)
	}

	members := &reflectedMembers{
		methods: make(map[string]reflectedMethod, reflectedType.NumMethod()),
	}
	for index := 0; index < reflectedType.NumMethod(); index++ {
		method := reflectedType.Method(index)
		key := strings.ToLower(method.Name)
		if _, exists := members.methods[key]; !exists {
			members.methods[key] = reflectedMethod{
				index:  index,
				getter: method.Type.NumIn() == 1 && method.Type.NumOut() == 1,
			}
		}
	}

	structType := reflectedType
	for structType.Kind() == reflect.Ptr {
		structType = structType.Elem()
	}
	if structType.Kind() == reflect.Struct {
		members.fields = make(map[string]int, structType.NumField())
		for index := 0; index < structType.NumField(); index++ {
			field := structType.Field(index)
			if field.PkgPath == "" {
				key := strings.ToLower(field.Name)
				if _, exists := members.fields[key]; !exists {
					members.fields[key] = index
				}
			}
		}
	}

	actual, _ := reflectedMemberCache.LoadOrStore(reflectedType, members)
	return actual.(*reflectedMembers)
}

func (v Value) reflect(key string) Value {
	if v.V == nil {
		return Value{K: Nil}
	}

	ptrRv := reflect.ValueOf(v.V)
	ptrRt := ptrRv.Type()
	members := membersFor(ptrRt)
	foldedKey := strings.ToLower(key)

	if descriptor, ok := members.methods[foldedKey]; ok {
		method := ptrRv.Method(descriptor.index)
		if descriptor.getter {
			results := method.Call(nil)
			if len(results) > 0 {
				return New(results[0].Interface())
			}
			return Value{K: Nil}
		}
		return Value{K: Func, V: method}
	}

	rv := ptrRv
	// Nếu là một Method (Func), ta muốn soi vào cái Receiver (Struct cha) của nó
	if rv.Kind() == reflect.Func {
		// Go không cho phép lấy Receiver trực tiếp từ reflect.Value của một Method
		// một cách dễ dàng nếu nó đã được gán.
		// TUY NHIÊN, nếu v.V là bản thân cái Pointer Struct (mà ta đánh dấu là Func),
		// thì ta sẽ soi trực tiếp.
	}

	for rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return Value{K: Nil}
		}
		rv = rv.Elem()
	}
	if rv.Kind() == reflect.Struct {
		if index, ok := members.fields[foldedKey]; ok {
			return New(rv.Field(index).Interface())
		}
	}

	return Value{K: Nil}
}
