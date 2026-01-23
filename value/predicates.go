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
