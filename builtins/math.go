package builtins

import (
	"math"
	"math/rand"

	"github.com/kitwork/engine/value"
)

func mathFn1(f func(float64) float64) value.Value {
	return value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) == 0 {
			return value.Value{K: value.Nil}
		}
		return value.New(f(args[0].N))
	})
}

func mathFn2(f func(float64, float64) float64) value.Value {
	return value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) < 2 {
			return value.Value{K: value.Nil}
		}
		return value.New(f(args[0].N, args[1].N))
	})
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

		"round": mathFn1(func(x float64) float64 { return math.Floor(x + 0.5) }),
		"sign": mathFn1(func(x float64) float64 {
			if x > 0 {
				return 1
			}
			if x < 0 {
				return -1
			}
			return 0
		}),

		"pow":   mathFn2(math.Pow),
		"atan2": mathFn2(math.Atan2),
		"hypot": mathFn2(math.Hypot),

		"min": value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) == 0 {
				return value.New(math.Inf(1))
			}
			result := args[0].N
			for _, a := range args[1:] {
				if a.N < result {
					result = a.N
				}
			}
			return value.New(result)
		}),
		"max": value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) == 0 {
				return value.New(math.Inf(-1))
			}
			result := args[0].N
			for _, a := range args[1:] {
				if a.N > result {
					result = a.N
				}
			}
			return value.New(result)
		}),
		"random": value.NewFunc(func(args ...value.Value) value.Value {
			return value.New(rand.Float64())
		}),
	}
	return value.New(m)
}
