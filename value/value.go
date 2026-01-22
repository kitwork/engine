package value

import (
	"bytes"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unsafe"
)

var (
	NULL  = NewNull()
	TRUE  = NewBool(true)
	FALSE = NewBool(false)
)

/* =============================================================================
   1. CORE TYPE DEFINITIONS
   ============================================================================= */

// Kind represents the underlying data type discriminator.
type Kind uint8

const (
	Invalid Kind = iota // Internal error or uninitialized
	Nil                 // Null / undefined

	// --- Scalar Types (Fast-path, data stored in N) ---
	Number   // float64
	Bool     // 0/1 in N
	Time     // UnixNano stored in N
	Duration // Nanoseconds stored in N

	// --- Reference Types (Slow-path, data stored in V) ---
	String // string
	Bytes  // []byte
	Map    // map[string]Value
	Array  // []Value

	// --- Complex Types ---
	Struct // Go struct or pointer
	Func   // Callable function / pipe
	Any    // Opaque Go interface{}

	Return
)

func (k Kind) String() string {
	switch k {
	case Invalid:
		return "invalid"
	case Nil:
		return "nil"
	// Scalar Types
	case Number:
		return "number"
	case Bool:
		return "bool"
	case Time:
		return "time"
	case Duration:
		return "duration"
	// Reference Types
	case String:
		return "string"
	case Bytes:
		return "bytes"
	case Map:
		return "map"
	case Array:
		return "array"
	// Complex Types
	case Struct:
		return "struct"
	case Func:
		return "func"
	case Any:
		return "any"
	default:
		return "unknown"
	}
}

// Trong package value

// Method đại diện cho hàm thực thi: target.method(args...)
type Method func(target Value, args ...Value) Value

// MaxKinds = 24 (Tối ưu cho CPU Cache và BCE)
const MaxKinds = 24

// Methods lưu trữ phương thức tập trung theo Kind
var Methods [MaxKinds]map[string]Method

func init() {
	for i := 0; i < MaxKinds; i++ {
		Methods[i] = make(map[string]Method)
	}
}

// RegisterMethod giúp Kind tự đăng ký phương thức cho chính nó
func (k Kind) Prototype(name string, fn Method) {
	if k < MaxKinds {
		Methods[k][name] = fn
	}
}

// GetMethod lấy phương thức của Kind đó
func (k Kind) Method(name string) (Method, bool) {
	if k >= MaxKinds {
		return nil, false
	}

	// 1. Thử tìm phương thức riêng của Kind (ví dụ: String.upper)
	if fn, ok := Methods[k][name]; ok {
		return fn, true
	}

	// 2. Nếu không có, tự động tìm ở Any (ví dụ: Any.len)
	// Giả sử Any là một hằng số trong danh sách Kind của bạn
	if k != Any {
		return Any.Method(name)
	}

	return nil, false
}

// Value is the atomic runtime unit of the
// 24 bytes on 64-bit for cache & stack efficiency.
type Value struct {
	N float64 // Scalar storage
	V any     // Reference storage
	K Kind    // Type discriminator
}

// Prototype cho phép đăng ký phương thức mới dựa trên thực thể Value hiện có.
// Ví dụ: myStrValue.Prototype("upper", myFunc) sẽ đăng ký cho TẤT CẢ các String.
func (v Value) Prototype(name string, fn Method) {
	v.K.Prototype(name, fn)
}

// Method tìm kiếm phương thức phù hợp cho thực thể Value này.
func (v Value) Method(name string) (Method, bool) {
	return v.K.Method(name)
}

// Invoke tìm phương thức theo tên rồi mới thực thi (Dùng cho target.method())
func (v Value) Invoke(name string, args ...Value) Value {
	if v.K == Nil {
		return v // Nil-safety: nil.any() -> nil
	}

	// Lấy hàm/thuộc tính từ hàm Get đã sửa ở trên
	attr := v.Get(name)

	if attr.K == Func {
		// TRƯỜNG HỢP A: Hàm Prototype (Native Go func)
		// Cần truyền 'v' vào làm tham số đầu tiên (target)
		if fn, ok := attr.V.(func(Value, ...Value) Value); ok {
			return fn(v, args...)
		}

		// TRƯỜNG HỢP B: Hàm Reflection (reflect.Value)
		return attr.Call(args...)
	}

	return attr
}

// Hàm Call của bạn (giữ nguyên logic reflect)
func (v Value) Call(args ...Value) Value {
	if v.K != Func || v.V == nil {
		return Value{K: Invalid}
	}

	// Trường hợp 1: V là hàm Prototype (Native)
	// Ép kiểu về func(Value, ...Value) Value
	if fn, ok := v.V.(func(Value, ...Value) Value); ok {
		return fn(Value{K: Nil}, args...)
	}

	// Trường hợp 2: V là reflect.Value (Struct Method)
	if fn, ok := v.V.(reflect.Value); ok {
		fnType := fn.Type()
		numIn := fnType.NumIn()
		isVariadic := fnType.IsVariadic()

		// Go reflect expects all arguments separately for Call
		// even if it's variadic.
		goArgs := make([]reflect.Value, len(args))

		for i := 0; i < len(args); i++ {
			var targetType reflect.Type
			if isVariadic && i >= numIn-1 {
				// Đối với variadic, các đối số cuối cùng có kiểu là element của slice
				targetType = fnType.In(numIn - 1).Elem()
			} else if i < numIn {
				targetType = fnType.In(i)
			} else {
				// Quá nhiều đối số và không phải variadic
				return Value{K: Invalid}
			}
			goArgs[i] = transformArg(args[i], targetType)
		}

		// Kiểm tra thiếu đối số (nếu không phải variadic)
		if !isVariadic && len(args) < numIn {
			return Value{K: Invalid}
		}
		// Nếu là variadic, phải có ít nhất numIn-1 đối số
		if isVariadic && len(args) < numIn-1 {
			return Value{K: Invalid}
		}

		results := fn.Call(goArgs)
		if len(results) > 0 {
			return New(results[0].Interface())
		}
		return Value{K: Nil}
	}
	return Value{K: Invalid}
}

// Hàm trợ giúp chuyển đổi kiểu dữ liệu an toàn
func transformArg(val Value, targetType reflect.Type) reflect.Value {
	// Ưu tiên 1: Nếu đích đến là kiểu Value (cho các hàm như Print(...Value))
	if targetType == reflect.TypeOf(Value{}) {
		return reflect.ValueOf(val)
	}

	// Ưu tiên 2: Giải nén interface
	v := val.Interface()
	if v == nil {
		return reflect.Zero(targetType)
	}

	rv := reflect.ValueOf(v)
	if rv.Type().AssignableTo(targetType) {
		return rv
	}
	if rv.Type().ConvertibleTo(targetType) {
		return rv.Convert(targetType)
	}

	// Ưu tiên 3: Xử lý số thực từ JS sang các kiểu số nguyên của Go
	if val.K == Number {
		switch targetType.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return reflect.ValueOf(int64(val.N)).Convert(targetType)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return reflect.ValueOf(uint64(val.N)).Convert(targetType)
		}
	}

	return reflect.Zero(targetType)
}

/* =============================================================================
   2. TYPE PREDICATES
   ============================================================================= */

func (v Value) IsInvalid() bool   { return v.K == Invalid }
func (v Value) IsNil() bool       { return v.K == Nil }
func (v Value) IsBlank() bool     { return v.K <= Nil }
func (v Value) IsValid() bool     { return v.K >= Number }
func (v Value) IsImmediate() bool { return v.K <= Duration }

func (v Value) IsScalar() bool { return v.K >= Number && v.K <= Duration }
func (v Value) IsNumeric() bool {
	switch v.K {
	case Number, Time, Duration:
		return true
	default:
		return false
	}
}

func (v Value) IsBool() bool      { return v.K == Bool }
func (v Value) IsTrue() bool      { return v.K == Bool && v.N > 0 }
func (v Value) IsString() bool    { return v.K == String }
func (v Value) IsBytes() bool     { return v.K == Bytes }
func (v Value) IsArray() bool     { return v.K == Array }
func (v Value) IsMap() bool       { return v.K == Map }
func (v Value) IsCallable() bool  { return v.K == Func }
func (v Value) IsReference() bool { return v.K >= String }
func (v Value) IsObject() bool    { return v.K >= String && v.V != nil }
func (v Value) IsReturn() bool    { return v.K == Return }

func (v Value) IsIterable() bool {
	switch v.K {
	case Array, Map, Bytes:
		return true
	default:
		return false
	}
}

// Truthy evaluates logical truthiness:
// - Scalars: N > 0
// - Objects: non-nil
func (v Value) Truthy() bool {
	if v.IsImmediate() {
		return v.N > 0
	}
	return v.IsObject()
}

/* =============================================================================
   3. STRINGIFY & CONVERSION
   ============================================================================= */

func (v Value) Text() string {
	buf := make([]byte, 0, 64)
	return string(v.Append(buf))
}

func (v Value) Append(b []byte) []byte {
	switch v.K {
	case String:
		return append(b, v.String()...)
	case Number:
		i := int64(v.N)
		if v.N == float64(i) {
			return strconv.AppendInt(b, i, 10)
		}
		return strconv.AppendFloat(b, v.N, 'g', -1, 64)
	case Bool:
		if v.N > 0 {
			return append(b, "true"...)
		}
		return append(b, "false"...)
	case Nil:
		return append(b, "null"...)
	case Time:
		return time.Unix(0, int64(v.N)).AppendFormat(b, time.RFC3339)
	case Duration:
		return append(b, time.Duration(int64(v.N)).String()...)
	case Bytes:
		return append(b, v.Bytes()...)
	case Array:
		b = append(b, '[')
		arr := v.V.([]Value)
		for i, item := range arr {
			if i > 0 {
				b = append(b, ", "...)
			}
			b = item.Append(b)
		}
		return append(b, ']')
	case Map:
		b = append(b, '{')
		m := v.V.(map[string]Value)
		first := true
		for k, val := range m {
			if !first {
				b = append(b, ", "...)
			}
			b = append(b, k...)
			b = append(b, ": "...)
			b = val.Append(b)
			first = false
		}
		return append(b, '}')
	default:
		return b
	}
}

func (v Value) Interface() any {
	switch v.K {
	case Nil:
		return nil
	case Bool:
		return v.N > 0
	case Number:
		return v.N
	case String:
		return v.V.(string)
	case Time:
		return time.Unix(0, int64(v.N))
	case Duration:
		return time.Duration(int64(v.N))
	case Bytes:
		return v.V.([]byte)
	case Array:
		arr := v.V.([]Value)
		res := make([]any, len(arr))
		for i, val := range arr {
			res[i] = val.Interface()
		}
		return res
	case Map:
		m := v.V.(map[string]Value)
		res := make(map[string]any)
		for k, val := range m {
			res[k] = val.Interface()
		}
		return res
	default:
		return v.V
	}
}

func (v Value) String() string {
	if s, ok := v.V.(string); ok {
		return s
	}
	return ""
}

func (v Value) Int() int64     { return int64(v.N) }
func (v Value) Float() float64 { return v.N }

func (v Value) Bytes() []byte {
	if b, ok := v.V.([]byte); ok {
		return b
	}
	return nil
}

// AsBytes provides a zero-copy read-only view into string data.
func (v Value) AsBytes() []byte {
	if v.K == Bytes {
		return v.Bytes()
	}
	s := v.String()
	if s == "" {
		return nil
	}
	return unsafe.Slice(unsafe.StringData(s), len(s))
}

func (v Value) ByteSlice() []byte {
	switch v.K {
	case Bytes:
		return v.Bytes()
	case String:
		return v.AsBytes()
	default:
		return nil
	}
}

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

/* =============================================================================
   5. NAVIGATION & REFLECTION
   ============================================================================= */

func (v Value) Len() int {
	if !v.IsObject() {
		return 0
	}
	switch v.K {
	case String:
		return len(v.V.(string))
	case Bytes:
		return len(v.V.([]byte))
	case Array:
		return len(v.V.([]Value))
	case Map:
		return len(v.V.(map[string]Value))
	}
	return 0
}

func (v Value) Index(i int) Value {
	if !v.IsObject() {
		return Value{K: Nil}
	}
	switch v.K {
	case Array:
		a := v.V.([]Value)
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

func (v Value) Get(key string) Value {
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
		if m, ok := v.V.(map[string]Value); ok {
			if val, ok := m[key]; ok {
				return val
			}
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
			if strings.EqualFold(f.Name, key) {
				return New(rv.Field(i).Interface())
			}
		}
	}
	return Value{K: Nil}
}

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
		return Value{K: Array, V: v}
	case map[string]Value:
		return Value{K: Map, V: v}
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
		return Value{K: Array, V: out}

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
