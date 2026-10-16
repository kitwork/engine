package hydrate

import (
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/kitwork/engine/value"
)

// numberMethod is a method of Number.prototype, as V8 runs it.
func (m *machine) numberMethod(f *frame, name string, this any, args []any) (any, error) {
	x, ok := this.(float64)
	if !ok {
		return nil, typeError("Number.prototype." + name + " requires that 'this' be a Number")
	}
	switch name {
	case "valueOf":
		return x, nil
	case "toLocaleString":
		// how the visitor's language writes a number: the browser answers it
		return Undefined, nil
	case "toString":
		radix := 10.0
		if !isUndefined(arg(args, 0)) {
			r, err := m.toIntegerOrInfinity(f, arg(args, 0))
			if err != nil {
				return nil, err
			}
			radix = r
		}
		if radix < 2 || radix > 36 {
			return nil, rangeError("toString() radix must be between 2 and 36")
		}
		if radix == 10 {
			return value.NumberText(x), nil
		}
		return radixText(x, int(radix)), nil
	case "toFixed":
		digits, err := m.toIntegerOrInfinity(f, arg(args, 0))
		if err != nil {
			return nil, err
		}
		if math.IsInf(digits, 0) || digits < 0 || digits > 100 {
			return nil, rangeError("toFixed() digits argument must be between 0 and 100")
		}
		return value.FixedText(x, int(digits)), nil
	case "toExponential":
		digits, err := m.toIntegerOrInfinity(f, arg(args, 0))
		if err != nil {
			return nil, err
		}
		if text, nonFinite := value.NonFiniteText(x); nonFinite {
			return text, nil
		}
		if digits < 0 || digits > 100 {
			return nil, rangeError("toExponential() argument must be between 0 and 100")
		}
		return exponentialText(x, int(digits), isUndefined(arg(args, 0))), nil
	case "toPrecision":
		if isUndefined(arg(args, 0)) {
			return value.NumberText(x), nil
		}
		p, err := m.toIntegerOrInfinity(f, arg(args, 0))
		if err != nil {
			return nil, err
		}
		if text, nonFinite := value.NonFiniteText(x); nonFinite {
			return text, nil
		}
		if p < 1 || p > 100 {
			return nil, rangeError("toPrecision() argument must be between 1 and 100")
		}
		return precisionText(x, int(p)), nil
	}
	return nil, typeError("Number.prototype." + name + " is not a function")
}

// decimalDigits is x (> 0) as the integer n with exactly digits digits and the exponent e, such that
// n × 10^(e - digits + 1) is nearest x, and of two as near the larger n — as toExponential and
// toPrecision round the exact value x holds. e is found on the exact value, 10^e <= x < 10^(e+1), and
// moves up only when the rounding carries (99.96 to three digits is 1.00e+2). 16/10/2026: e came from
// Log10, which rounds a double just below a power of ten up to it, and is off for a subnormal, so
// (999.9999999999998).toPrecision(16) was "1000.000000000000" and (5e-324).toPrecision(1) "0e-308".
func decimalDigits(x float64, digits int) (string, int) {
	exact := new(big.Rat).SetFloat64(x)
	e := int(math.Floor(math.Log10(x))) // a guess, made exact below
	for exact.Cmp(powerOfTen(e)) < 0 {
		e--
	}
	for exact.Cmp(powerOfTen(e+1)) >= 0 {
		e++
	}
	scaled := new(big.Rat).Quo(exact, powerOfTen(e-digits+1))
	scaled.Add(scaled, big.NewRat(1, 2))
	text := new(big.Int).Quo(scaled.Num(), scaled.Denom()).String()
	if len(text) > digits {
		// the rounding carried to 10^digits: one digit more, all zeros after the 1
		return text[:digits], e + 1
	}
	return text, e
}

// powerOfTen is 10^k exactly, k of either sign.
func powerOfTen(k int) *big.Rat {
	p := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(absInt(k))), nil)
	if k < 0 {
		return new(big.Rat).SetFrac(big.NewInt(1), p)
	}
	return new(big.Rat).SetInt(p)
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// shortestDigits are the fewest digits that read back as x (> 0), and the exponent of the first.
func shortestDigits(x float64) (string, int) {
	text := strconv.FormatFloat(x, 'e', -1, 64)
	mark := strings.IndexByte(text, 'e')
	e, _ := strconv.Atoi(text[mark+1:])
	return strings.Replace(text[:mark], ".", "", 1), e
}

// exponentialText is x.toExponential(digits): one digit, the point, digits more, e and the exponent
// with its sign; as many digits as x needs when none were asked for.
func exponentialText(x float64, digits int, shortest bool) string {
	sign := ""
	if x < 0 {
		sign, x = "-", -x
	}
	var text string
	var e int
	switch {
	case x == 0:
		text, e = strings.Repeat("0", digits+1), 0
	case shortest:
		text, e = shortestDigits(x)
	default:
		text, e = decimalDigits(x, digits+1)
	}
	mantissa := text[:1]
	if len(text) > 1 {
		mantissa += "." + text[1:]
	}
	exponent := "+" + strconv.Itoa(e)
	if e < 0 {
		exponent = strconv.Itoa(e)
	}
	return sign + mantissa + "e" + exponent
}

// precisionText is x.toPrecision(p): p significant digits, with an exponent when it would be below
// -6 or at least p.
func precisionText(x float64, p int) string {
	sign := ""
	if x < 0 {
		sign, x = "-", -x
	}
	var text string
	var e int
	if x == 0 {
		text, e = strings.Repeat("0", p), 0
	} else {
		text, e = decimalDigits(x, p)
	}
	if x != 0 && (e < -6 || e >= p) {
		mantissa := text[:1]
		if p != 1 {
			mantissa += "." + text[1:]
		}
		exponent := "+" + strconv.Itoa(e)
		if e < 0 {
			exponent = strconv.Itoa(e)
		}
		return sign + mantissa + "e" + exponent
	}
	switch {
	case e == p-1:
		return sign + text
	case e >= 0:
		return sign + text[:e+1] + "." + text[e+1:]
	}
	return sign + "0." + strings.Repeat("0", -(e+1)) + text
}

// radixText is x.toString(radix) for a radix other than 10, as V8's DoubleToRadixCString writes it:
// the integer part exactly, the fraction to as many digits as x holds, its last digit rounded.
func radixText(x float64, radix int) string {
	if text, nonFinite := value.NonFiniteText(x); nonFinite {
		return text
	}
	if x == 0 {
		return "0"
	}
	const chars = "0123456789abcdefghijklmnopqrstuvwxyz"
	negative := x < 0
	if negative {
		x = -x
	}
	integer := math.Floor(x)
	fraction := x - integer
	delta := 0.5 * (math.Nextafter(x, math.Inf(1)) - x)
	delta = math.Max(math.Nextafter(0, 1), delta)
	var frac []byte
	if fraction >= delta {
		frac = append(frac, '.')
		for {
			fraction *= float64(radix)
			delta *= float64(radix)
			digit := int(fraction)
			frac = append(frac, chars[digit])
			fraction -= float64(digit)
			if fraction > 0.5 || (fraction == 0.5 && digit&1 == 1) {
				if fraction+delta > 1 {
					// round up, carrying over the digits already written
					for {
						last := len(frac) - 1
						if last == 0 {
							integer++
							frac = frac[:1]
							break
						}
						c := frac[last]
						d := strings.IndexByte(chars, c)
						if d+1 < radix {
							frac[last] = chars[d+1]
							break
						}
						frac = frac[:last]
					}
					break
				}
			}
			if !(fraction >= delta) {
				break
			}
		}
		if len(frac) == 1 {
			frac = nil
		}
	}
	var whole []byte
	for exponentOf(integer/float64(radix)) > 0 {
		integer /= float64(radix)
		whole = append(whole, '0')
	}
	for {
		remainder := math.Mod(integer, float64(radix))
		whole = append(whole, chars[int(remainder)])
		integer = (integer - remainder) / float64(radix)
		if !(integer > 0) {
			break
		}
	}
	for i, j := 0, len(whole)-1; i < j; i, j = i+1, j-1 {
		whole[i], whole[j] = whole[j], whole[i]
	}
	out := string(whole) + string(frac)
	if negative {
		return "-" + out
	}
	return out
}

// exponentOf is V8's Double::Exponent: e such that x is a 53-bit integer times 2^e.
func exponentOf(x float64) int {
	bits := math.Float64bits(x)
	biased := int(bits>>52) & 0x7FF
	if biased == 0 {
		return -1074
	}
	return biased - 1075
}
