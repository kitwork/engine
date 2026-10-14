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

// HasOwn reports whether v is an object carrying key as its own key. Get returns such a key before
// any method of the kind; a call reaches it only when it holds a function (CallsOwn).
func (v Value) HasOwn(key string) bool {
	if v.K != Map {
		return false
	}
	_, ok := v.Map()[key]
	return ok
}

// CallsOwn reports whether a call v.name(…) goes to v's own key rather than to the builtin of the
// kind. An own key wins a call only when it holds a function: a lambda the author wrote
// ({ greet: () => … }), or a host function such as the toString of new Date(). An own key holding
// data (a string, a number, a bool, null, an object, an array: anything request.json(), JSON.parse
// or a database row can carry) never answers a call to a builtin method of the kind: the call goes
// to the builtin exactly as on an object without that key, so data from outside cannot replace
// body.pick(…), has, keys, text, json… A data key named otherwise (error, isError and type
// included) is not called either: calling it after a dot is the NOT_A_FUNCTION runtime error
// (14/10/2026; until then { x: 3 }.x() gave 3).
func (v Value) CallsOwn(name string) bool {
	if v.K != Map {
		return false
	}
	own, ok := v.Map()[name]
	return ok && own.K == Func
}

// MissingMember lets a native object answer for a member it does not have — to record an error
// naming it, to keep a fluent chain alive, or both. Get consults it after reflection finds nothing.
type MissingMember interface {
	MissingMember(name string) Value
}

func (v Value) Get(key string) Value {
	if v.K == Invalid {
		// A failure (a failed db query, fail("…")) is K==Invalid carrying its message in .V. A program
		// never holds one: it ends the run the instruction that made it (safe(() => …) is how a program
		// reads a failure). A host reading one finds .message and .isError; every OTHER access stays
		// the failure, so it keeps bubbling.
		switch key {
		case "message":
			if s, ok := v.V.(string); ok {
				return New(s)
			}
		case "isError":
			return New(true)
		}
		return v
	}
	// An object's own key wins over every method of its kind, as an own property shadows a
	// prototype method in JavaScript: { delete: 3 }.delete is 3, { text: "hi" }.text is "hi", and
	// an object's own `type` is data, not the kind helper (`$.meta.type`). It wins over length too:
	// { ok: false, error: "x" }.error is "x", { length: 5 }.length is 5, always (14/10/2026: no value
	// carries an error the engine attached to data any more, so no object keeps the engine's
	// length, isError and error over its own). An object without the key reaches the methods
	// ({ a: 1 }.keys() still lists its keys). A call is another matter: see CallsOwn.
	if v.K == Map {
		if item, ok := v.Map()[key]; ok {
			return item
		}
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
		// A native object may answer for members it does not have. Without this a missing method
		// reads as nil, the call on nil yields nil, and everything chained after it is dropped
		// without a word — a router declaring `.themes()` on an engine that has not shipped it
		// lost its client runtime and design tokens silently.
		if m, ok := v.V.(MissingMember); ok {
			return m.MissingMember(key)
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

	// ƯU TIÊN 3: Proxy (an object's own keys were read above)
	switch v.K {
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
			// a method read as a field still has its error channel: a Go method of no argument that
			// returns only an error (napas.validate()) fails with it, as a call of it does
			return reflectedCallResult(key, method.Call(nil))
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
