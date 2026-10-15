package value

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

func (v Value) IsNumber() bool    { return v.K == Number }
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

// Truthy is JavaScript's truthiness: a number is false only at 0 (-0 too) and NaN, so -1 is true
// (14/10/2026; until then N > 0 decided, and -1 || 5 was 5). A time and a duration, numbers underneath,
// follow the same rule: one before 1970 or a negative duration is true, the exact moment 1970-01-01T00:00:00Z
// and a zero duration are false (a Date of JavaScript is always true; JavaScript has no duration).
// false, null and a failure are false; a string is false only when empty; any other value is true.
func (v Value) Truthy() bool {
	switch v.K {
	case Number, Time, Duration:
		return v.N != 0 && v.N == v.N
	}
	if v.IsImmediate() {
		return v.N > 0
	}
	if v.K == String {
		return v.V != nil && v.V.(string) != ""
	}
	return v.IsObject()
}

// TypeOf is JavaScript's typeof of v (14/10/2026): "number", "string", "boolean", "function", "object",
// and "undefined" for null. Kitwork has one value where JavaScript has two, null and undefined (a name
// nothing declares, a missing key, let y;, a function that returns nothing all give it), and typeof
// gives it "undefined", so typeof x === "undefined" still finds a name or a key that is not there;
// JavaScript gives "object" for null, which is why its object test reads typeof x === "object" && x !== null.
// A time and a duration are "object", as a Date is; so are an array, bytes and every value of the host.
func (v Value) TypeOf() string {
	switch v.K {
	case Number:
		return "number"
	case String:
		return "string"
	case Bool:
		return "boolean"
	case Func:
		return "function"
	case Invalid, Nil:
		return "undefined"
	}
	return "object"
}
