package builtins

import (
	"math"

	"github.com/kitwork/engine/value"
)

func Number() value.Value {
	// Number(x) is JavaScript's ToNumber (value.ToNumber, the one ++, --, unary +, isNaN and Math use):
	// text as JavaScript reads it, a date its time value, [5] 5, and what is no number NaN, as in
	// JavaScript: Number("abc"), Number({}) and Number([1, 2]) are NaN, Number("") and Number(null) 0
	// (16/10/2026, the owner's E2; what was no number was null, and every array was).
	ctor := func(args ...value.Value) value.Value {
		if len(args) == 0 {
			return value.Value{K: value.Number, N: 0}
		}
		return value.ToNumber(args[0])
	}

	props := map[string]value.Value{
		"MAX_SAFE_INTEGER":  value.New(9007199254740991.0),
		"MIN_SAFE_INTEGER":  value.New(-9007199254740991.0),
		"EPSILON":           value.New(2.220446049250313e-16),
		"MAX_VALUE":         value.New(math.MaxFloat64),
		"MIN_VALUE":         value.New(5e-324),
		"POSITIVE_INFINITY": value.New(math.Inf(1)),
		"NEGATIVE_INFINITY": value.New(math.Inf(-1)),
		"NaN":               value.New(math.NaN()),

		// Number.isNaN, unlike isNaN, makes no number of its argument: only the number NaN is NaN
		"isNaN": value.NewFunc(func(args ...value.Value) value.Value {
			return value.ToBool(len(args) > 0 && args[0].K == value.Number && args[0].N != args[0].N)
		}),

		"isInteger": value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) == 0 || args[0].K != value.Number {
				return value.FALSE
			}
			// a number without a fraction, however large (an int64 round trip refused 1e300)
			return value.ToBool(!math.IsInf(args[0].N, 0) && math.Trunc(args[0].N) == args[0].N)
		}),
		"isSafeInteger": value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) == 0 || args[0].K != value.Number {
				return value.FALSE
			}
			n := args[0].N
			return value.ToBool(math.Trunc(n) == n && math.Abs(n) <= 9007199254740991)
		}),
		"isFinite": value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) == 0 || args[0].K != value.Number {
				return value.FALSE
			}
			return value.ToBool(!math.IsInf(args[0].N, 0) && !math.IsNaN(args[0].N))
		}),
		// Number.parseFloat and Number.parseInt read as the global parseFloat and parseInt, as in
		// JavaScript (16/10/2026: they made a number of the whole argument, and what was no number was null)
		"parseFloat": ParseFloat(),
		"parseInt":   ParseInt(),
	}

	return value.NewFuncObject(ctor, props)
}

// ParseFloat is the global parseFloat: JavaScript's, the longest decimal number the text of its argument
// begins with (value.ParseFloatText: "3.5px" is 3.5, ".5" 0.5, "1e400" Infinity; "abc", "" and no
// argument are NaN). Until 16/10/2026 (the owner's E2) it read the whole text, and what was no number
// was 0.
func ParseFloat() value.Value {
	return value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) == 0 {
			return value.New(math.NaN())
		}
		if args[0].K == value.Invalid {
			return args[0]
		}
		return value.New(value.ParseFloatText(value.ParseText(args[0])))
	})
}

// ParseInt is the global parseInt: JavaScript's, the digits the text of its argument begins with, in the
// base its radix names (value.ParseIntText): "12px" is 12, "0x1f" 31 without a radix, ("ff", 16) 255,
// ("7", 37) NaN, "abc", "" and no argument NaN, "99999999999999999999" 1e20. The radix is made a number
// and then an integer as JavaScript's ToInt32 makes it; null, NaN and 0 are no radix. Until 16/10/2026
// (the owner's E2) it read 0x only with the radix 16, took a radix outside 2 to 36 for 10, gave 0 for no
// digit and stopped at the int64 range.
func ParseInt() value.Value {
	return value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) == 0 {
			return value.New(math.NaN())
		}
		if args[0].K == value.Invalid {
			return args[0]
		}
		var radix int32
		if len(args) > 1 {
			n := value.ToNumber(args[1])
			if n.K != value.Number {
				return n
			}
			radix = value.ToInt32(n.N)
		}
		return value.New(value.ParseIntText(value.ParseText(args[0]), radix))
	})
}

// IsNaN is the global isNaN: JavaScript's, which makes a number of its argument first (value.ToNumber),
// so isNaN("abc") and isNaN({}) are true, isNaN("") and isNaN(null) false (15/10/2026). Kitwork has one
// value for null and undefined, and it is 0 here, where JavaScript's undefined is NaN. Number("abc") is
// NaN since 16/10/2026 (the owner's E2), so isNaN(Number(x)) tells text that is no number, as in
// JavaScript.
func IsNaN() value.Value {
	return value.NewFunc(func(args ...value.Value) value.Value {
		n := toNumberArgument(args)
		if n.K != value.Number {
			return n
		}
		return value.ToBool(n.N != n.N)
	})
}

// IsFinite is the global isFinite: its argument made a number, neither NaN nor an infinity
// (isFinite("12") is true; Number.isFinite("12") is false, as in JavaScript).
func IsFinite() value.Value {
	return value.NewFunc(func(args ...value.Value) value.Value {
		n := toNumberArgument(args)
		if n.K != value.Number {
			return n
		}
		return value.ToBool(!math.IsInf(n.N, 0) && n.N == n.N)
	})
}

// toNumberArgument is the first argument made a number; no argument is JavaScript's undefined, NaN.
func toNumberArgument(args []value.Value) value.Value {
	if len(args) == 0 {
		return value.New(math.NaN())
	}
	return value.ToNumber(args[0])
}
