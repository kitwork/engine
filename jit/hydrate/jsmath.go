package hydrate

import (
	"math"
	"math/bits"
)

// mathMethod is a function of Math, as V8 computes it. Math.random belongs to the moment the page is
// open: the browser answers it.
func (m *machine) mathMethod(f *frame, name string, args []any) (any, error) {
	n := make([]float64, len(args))
	for i, a := range args {
		v, err := m.toNumber(f, a)
		if err != nil {
			return nil, err
		}
		n[i] = v
	}
	x := func(i int) float64 {
		if i < len(n) {
			return n[i]
		}
		return math.NaN()
	}
	switch name {
	case "random":
		return Undefined, nil
	case "abs":
		return math.Abs(x(0)), nil
	case "acos":
		return jsAcos(x(0)), nil
	case "acosh":
		return jsAcosh(x(0)), nil
	case "asin":
		return jsAsin(x(0)), nil
	case "asinh":
		return jsAsinh(x(0)), nil
	case "atan":
		return jsAtan(x(0)), nil
	case "atanh":
		return jsAtanh(x(0)), nil
	case "atan2":
		return jsAtan2(x(0), x(1)), nil
	case "cbrt":
		return jsCbrt(x(0)), nil
	case "ceil":
		return math.Ceil(x(0)), nil
	case "clz32":
		return float64(bits.LeadingZeros32(toUint32(x(0)))), nil
	case "cos":
		return jsCos(x(0)), nil
	case "cosh":
		return jsCosh(x(0)), nil
	case "exp":
		return jsExp(x(0)), nil
	case "expm1":
		return jsExpm1(x(0)), nil
	case "floor":
		return math.Floor(x(0)), nil
	case "fround":
		return float64(float32(x(0))), nil
	case "hypot":
		return hypot(n), nil
	case "imul":
		return float64(int32(toUint32(x(0)) * toUint32(x(1)))), nil
	case "log":
		return jsLog(x(0)), nil
	case "log10":
		return jsLog10(x(0)), nil
	case "log1p":
		return jsLog1p(x(0)), nil
	case "log2":
		return jsLog2(x(0)), nil
	case "max", "min":
		out := math.Inf(-1)
		if name == "min" {
			out = math.Inf(1)
		}
		for _, v := range n {
			if math.IsNaN(v) {
				return math.NaN(), nil
			}
			// 0 is greater than -0 here, as in JavaScript and the VM (16/10/2026: Math.max(-0, 0) was -0)
			zeros := v == 0 && out == 0
			if (name == "max" && (v > out || zeros && !math.Signbit(v))) ||
				(name == "min" && (v < out || zeros && math.Signbit(v))) {
				out = v
			}
		}
		return out, nil
	case "pow":
		return jsPow(x(0), x(1)), nil
	case "round":
		return mathRound(x(0)), nil
	case "sign":
		v := x(0)
		switch {
		case v != v || v == 0:
			return v, nil
		case v > 0:
			return float64(1), nil
		}
		return float64(-1), nil
	case "sin":
		return jsSin(x(0)), nil
	case "sinh":
		return jsSinh(x(0)), nil
	case "sqrt":
		return math.Sqrt(x(0)), nil
	case "tan":
		return jsTan(x(0)), nil
	case "tanh":
		return jsTanh(x(0)), nil
	case "trunc":
		return math.Trunc(x(0)), nil
	}
	return nil, typeError("Math." + name + " is not a function")
}

// hypot is V8's Math.hypot: an infinity wins over NaN, the squares summed with Kahan's compensation
// after scaling by the largest.
func hypot(n []float64) float64 {
	largest := 0.0
	sawNaN := false
	abs := make([]float64, len(n))
	for i, v := range n {
		abs[i] = math.Abs(v)
		if abs[i] != abs[i] {
			sawNaN = true
		} else if abs[i] > largest {
			largest = abs[i]
		}
	}
	if math.IsInf(largest, 1) {
		return math.Inf(1)
	}
	if sawNaN {
		return math.NaN()
	}
	if largest == 0 {
		return 0
	}
	sum, compensation := 0.0, 0.0
	for _, a := range abs {
		r := a / largest
		summand := r*r - compensation
		preliminary := sum + summand
		compensation = (preliminary - sum) - summand
		sum = preliminary
	}
	return math.Sqrt(sum) * largest
}

// jsPow is JavaScript's Number::exponentiate: as IEEE's pow, except that a NaN exponent is NaN and 1
// or -1 to an infinite power NaN (Go and C give 1).
func jsPow(x, y float64) float64 {
	switch {
	case y != y:
		return math.NaN()
	case y == 0:
		return 1
	case (x == 1 || x == -1) && math.IsInf(y, 0):
		return math.NaN()
	}
	return ieeePow(x, y)
}
