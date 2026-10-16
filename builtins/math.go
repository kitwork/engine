package builtins

import (
	"math"
	"math/rand"

	"github.com/kitwork/engine/value"
)

// number is an argument of Math made a number as JavaScript's Math does (value.ToNumber, 15/10/2026):
// "2.5" is 2.5, a date its time value, "x" and an object NaN, an array the number its text is ([-3] is
// -3, [1, 2] NaN; every array was NaN until 16/10/2026). Math read the number a value held, 0 for any of
// them (Math.round("2.5") was 0).
func number(v value.Value) float64 {
	if v.K == value.Number {
		return v.N
	}
	if n := value.ToNumber(v); n.K == value.Number {
		return n.N
	}
	return math.NaN()
}

func mathFn1(f func(float64) float64) value.Value {
	return value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) == 0 {
			return value.Value{K: value.Nil}
		}
		return value.New(f(number(args[0])))
	})
}

func mathFn2(f func(float64, float64) float64) value.Value {
	return value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) < 2 {
			return value.Value{K: value.Nil}
		}
		return value.New(f(number(args[0]), number(args[1])))
	})
}

// round is JavaScript's Math.round: the nearest whole number, a half up; 0 and -0, NaN and the
// infinities themselves, -0 for -0.5 to 0, and a number past 2^52 (already whole) itself. floor(x + 0.5)
// made 0.49999999999999994 1 and 4503599627370497 4503599627370498 (15/10/2026).
func round(x float64) float64 {
	switch {
	case x != x, math.IsInf(x, 0), x == 0, math.Abs(x) >= 1<<52:
		return x
	case x > 0 && x < 0.5:
		return 0
	case x < 0 && x >= -0.5:
		return math.Copysign(0, -1)
	}
	return math.Floor(x + 0.5)
}

// pow is JavaScript's Math.pow, which is NaN for an exponent NaN, and for a base 1 or -1 to an infinite
// exponent; Go's math.Pow gives 1 for both (15/10/2026).
func pow(x, y float64) float64 {
	if y != y || math.Abs(x) == 1 && math.IsInf(y, 0) {
		return math.NaN()
	}
	return math.Pow(x, y)
}

func Math() value.Value {
	m := map[string]value.Value{
		"PI":      value.New(math.Pi),
		"E":       value.New(math.E),
		"LN2":     value.New(math.Ln2),
		"LN10":    value.New(math.Ln10),
		"LOG2E":   value.New(math.Log2E),
		"LOG10E":  value.New(math.Log10E),
		"SQRT2":   value.New(math.Sqrt2),
		"SQRT1_2": value.New(1 / math.Sqrt2),

		"abs":   mathFn1(math.Abs),
		"floor": mathFn1(math.Floor),
		"ceil":  mathFn1(math.Ceil),
		"trunc": mathFn1(math.Trunc),
		"sqrt":  mathFn1(math.Sqrt),
		"cbrt":  mathFn1(math.Cbrt),
		"exp":   mathFn1(math.Exp),
		"log":   mathFn1(math.Log),
		"log2":  mathFn1(math.Log2),
		"log10": mathFn1(math.Log10),
		"sin":   mathFn1(math.Sin),
		"cos":   mathFn1(math.Cos),
		"tan":   mathFn1(math.Tan),
		"asin":  mathFn1(math.Asin),
		"acos":  mathFn1(math.Acos),
		"atan":  mathFn1(math.Atan),

		"round": mathFn1(round),
		"sign": mathFn1(func(x float64) float64 {
			if x > 0 {
				return 1
			}
			if x < 0 {
				return -1
			}
			// 0, -0 and NaN are themselves, as in JavaScript (15/10/2026: NaN was 0)
			return x
		}),

		"pow":   mathFn2(pow),
		"atan2": mathFn2(math.Atan2),
		"hypot": mathFn2(math.Hypot),

		"min": value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) == 0 {
				return value.New(math.Inf(1))
			}
			result := number(args[0])
			for _, a := range args[1:] {
				// -0 is less than 0 here, as in JavaScript (15/10/2026)
				if n := number(a); n < result || n != n || n == 0 && result == 0 && math.Signbit(n) {
					result = n
				}
			}
			if result != result {
				// one NaN makes the whole NaN, as in JavaScript, wherever it stands (15/10/2026: only
				// a first NaN did, Math.min(1, 0 / 0) was 1)
				return value.New(math.NaN())
			}
			return value.New(result)
		}),
		"max": value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) == 0 {
				return value.New(math.Inf(-1))
			}
			result := number(args[0])
			for _, a := range args[1:] {
				// 0 is greater than -0 here, as in JavaScript (15/10/2026)
				if n := number(a); n > result || n != n || n == 0 && result == 0 && !math.Signbit(n) {
					result = n
				}
			}
			if result != result {
				// one NaN makes the whole NaN, as in JavaScript, wherever it stands (15/10/2026: only
				// a first NaN did, Math.max(1, 0 / 0) was 1)
				return value.New(math.NaN())
			}
			return value.New(result)
		}),
		"random": value.NewFunc(func(args ...value.Value) value.Value {
			return value.New(rand.Float64())
		}),
	}
	return value.New(m)
}
