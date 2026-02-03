package value

// Kind represents the underlying data type discriminator.
type Kind uint8

const (
	Invalid Kind = iota // Internal error or uninitialized
	Nil                 // Null / undefined

	// --- Scalar Types (Fast-path, data stored in N) ---
	Number
	Bool
	Time
	Duration

	// --- Reference Types (Slow-path, data stored in V) ---
	String
	Bytes
	Map
	Array

	// --- Complex Types ---
	Struct
	Func
	Any

	Return
	Proxy
)

func (k Kind) String() string {
	switch k {
	case Invalid:
		return "invalid"
	case Nil:
		return "nil"
	case Number:
		return "number"
	case Bool:
		return "bool"
	case Time:
		return "time"
	case Duration:
		return "duration"
	case String:
		return "string"
	case Bytes:
		return "bytes"
	case Map:
		return "map"
	case Array:
		return "array"
	case Struct:
		return "struct"
	case Func:
		return "func"
	case Any:
		return "any"
	case Proxy:
		return "proxy"

	default:
		return "unknown"
	}
}

// Method đại diện cho hàm thực thi: target.method(args...)
type Method func(target Value, args ...Value) Value

// MaxKinds = 24 (Tối ưu cho CPU Cache và BCE)
const MaxKinds = 24

// Methods lưu trữ phương thức động (cho phép người dùng mở rộng sau này)
var Methods = func() [MaxKinds]map[string]Method {
	var m [MaxKinds]map[string]Method
	for i := 0; i < MaxKinds; i++ {
		m[i] = make(map[string]Method)
	}
	return m
}()

// Prototype cho phép đăng ký phương thức động từ bên ngoài
func (k Kind) Prototype(name string, fn Method) {
	if k < MaxKinds {
		Methods[k][name] = fn
	}
}

// Method tìm kiếm phương thức: Sử dụng Method Expressions để đạt hiệu năng tối đa
func (k Kind) Method(name string) (Method, bool) {
	// 1. GLOBAL / ANY METHODS
	switch name {
	case "string", "text", "toString":
		return Value.ToString, true
	case "int", "integer":
		return Value.Integer, true
	case "float", "toFloat":
		return Value.ToFloat, true
	case "json", "toJson":
		return Value.ToJson, true
	case "len", "length":
		return Value.Length, true
	case "html":
		return Value.HTML, true
	case "render":
		return Value.Render, true
	}

	// 2. TYPE-SPECIFIC METHODS
	switch k {
	case String:
		switch name {
		case "upper", "toUpperCase":
			return Value.Upper, true
		case "lower", "toLowerCase":
			return Value.Lower, true
		case "trim":
			return Value.Trim, true
		case "includes":
			return Value.Includes, true
		case "startsWith":
			return Value.StartsWith, true
		case "endsWith":
			return Value.EndsWith, true
		case "split":
			return Value.Split, true
		case "replace":
			return Value.Replace, true
		case "capitalize":
			return Value.Capitalize, true
		case "safe":
			return Value.Safe, true
		}
	case Array:
		switch name {
		case "push":
			return Value.Push, true
		case "pop":
			return Value.Pop, true
		case "shift":
			return Value.Shift, true
		case "unshift":
			return Value.Unshift, true
		case "join":
			return Value.Join, true
		case "reverse":
			return Value.Reverse, true
		case "shuffle":
			return Value.Shuffle, true
		case "random":
			return Value.Random, true
		case "at", "index":
			return Value.ItemAt, true
		case "compact":
			return Value.Compact, true
		case "unique":
			return Value.Unique, true
		case "map", "filter", "find":
			// Functional methods handled directly by Runtime for callback execution power
			return func(target Value, args ...Value) Value { return Value{K: Nil} }, true
		}
	case Map:
		switch name {
		case "keys":
			return Value.Keys, true
		case "delete":
			return Value.Delete, true
		case "has":
			return Value.Has, true
		case "merge":
			return Value.Merge, true
		}
	}

	// 3. DYNAMIC FALLBACK
	if k < MaxKinds {
		if fn, ok := Methods[k][name]; ok {
			return fn, true
		}
	}

	return nil, false
}
