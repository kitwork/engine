package value

import (
	"math"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// What a number is as text and what text is as a number, as JavaScript says (15/10/2026: the owner's
// L2 and L3). A division by zero gives Infinity or NaN since then, so they reach every place a number is
// written, and ++ and -- make a number of what they step first.

// AppendNumber writes n as JavaScript's Number::toString does (String(n), `${n}`, "" + n, a key of an
// object): the fewest digits that read back as n, written plain from 1e-7 up to 1e21 ("1234567.5",
// "0.000001", "100000000000000000000") and with an exponent outside ("1e+21", "1e-7", "1.5e-10"); -0 is
// "0"; NaN, Infinity and -Infinity as words. Go's 'g' wrote an exponent from 1e21 down to 1e6 for a
// number with a fraction (String(1234567.5) was "1.2345675e+06", where JSON.stringify gave "1234567.5")
// and wrote whole numbers past 2^53 with digits JavaScript does not (15/10/2026, the review of L2).
func AppendNumber(b []byte, n float64) []byte {
	if text, nonFinite := NonFiniteText(n); nonFinite {
		return append(b, text...)
	}
	if n == 0 {
		return append(b, '0')
	}
	if n == math.Trunc(n) && math.Abs(n) < 1<<53 {
		return strconv.AppendInt(b, int64(n), 10)
	}
	if n < 0 {
		b = append(b, '-')
		n = -n
	}
	// the shortest digits that read back as n, d.ddde±x; JavaScript's k digits and point n - k
	var scratch [32]byte
	written := strconv.AppendFloat(scratch[:0], n, 'e', -1, 64)
	mark := 0
	for written[mark] != 'e' {
		mark++
	}
	exponent, _ := strconv.Atoi(string(written[mark+1:]))
	digits := append([]byte{written[0]}, written[min(2, mark):mark]...)
	k, point := len(digits), exponent+1
	switch {
	case k <= point && point <= 21:
		b = append(b, digits...)
		for i := k; i < point; i++ {
			b = append(b, '0')
		}
	case 0 < point && point <= 21:
		b = append(append(append(b, digits[:point]...), '.'), digits[point:]...)
	case -6 < point && point <= 0:
		b = append(b, "0."...)
		for i := point; i < 0; i++ {
			b = append(b, '0')
		}
		b = append(b, digits...)
	default:
		b = append(b, digits[0])
		if k > 1 {
			b = append(append(b, '.'), digits[1:]...)
		}
		b = append(b, 'e')
		if point > 0 {
			b = append(b, '+')
		}
		b = strconv.AppendInt(b, int64(point-1), 10)
	}
	return b
}

// NumberText is AppendNumber as a string: the text JavaScript gives a number.
func NumberText(n float64) string { return string(AppendNumber(nil, n)) }

// FixedText is JavaScript's n.toFixed(digits) for digits 0 to 100: the number nearest n with that many
// digits after the point, and of two as near the one further from zero, as JavaScript rounds the exact
// value n holds ((1.25).toFixed(1) is "1.3", (2.5).toFixed(0) "3", (1.005).toFixed(2) "1.00", for
// 1.005 holds a little less); -0 is "0.00"; from 1e21 up, n as String(n) writes it; NaN and the
// infinities as words. Go rounds a tie to the even digit ((1.25).toFixed(1) was "1.2"; 15/10/2026).
func FixedText(n float64, digits int) string {
	if text, nonFinite := NonFiniteText(n); nonFinite {
		return text
	}
	if math.Abs(n) >= 1e21 {
		return NumberText(n)
	}
	sign := ""
	if n < 0 {
		sign = "-"
	}
	scaled := new(big.Rat).SetFloat64(math.Abs(n))
	scaled.Mul(scaled, new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil)))
	scaled.Add(scaled, big.NewRat(1, 2))
	text := new(big.Int).Quo(scaled.Num(), scaled.Denom()).String()
	if digits == 0 {
		return sign + text
	}
	for len(text) <= digits {
		text = "0" + text
	}
	return sign + text[:len(text)-digits] + "." + text[len(text)-digits:]
}

// ToIntegerOrInfinity is JavaScript's ToIntegerOrInfinity of a number: NaN is 0, an infinity itself, any
// other number without its fraction (-0 is 0).
func ToIntegerOrInfinity(n float64) float64 {
	if n != n {
		return 0
	}
	return math.Trunc(n) + 0
}

// saturatedInt64 is ToIntegerOrInfinity(n) as an int64, clamped to the int64 range: Go's int64(n) of NaN,
// an infinity or a number past the range is -9223372036854775808 on amd64.
func saturatedInt64(n float64) int64 {
	n = ToIntegerOrInfinity(n)
	switch {
	case n >= 1<<63:
		return math.MaxInt64
	case n <= -(1 << 63):
		return math.MinInt64
	}
	return int64(n)
}

// NonFiniteText is the text JavaScript gives NaN, Infinity and -Infinity, and false for a finite number:
// a writer with its own format for numbers (toFixed) asks it first.
func NonFiniteText(n float64) (string, bool) {
	switch {
	case n != n:
		return "NaN", true
	case math.IsInf(n, 1):
		return "Infinity", true
	case math.IsInf(n, -1):
		return "-Infinity", true
	}
	return "", false
}

// ToNumber is JavaScript's ToNumber for what Kitwork holds, which ++, --, isNaN and isFinite apply
// (15/10/2026), and Number(), unary + and Math (16/10/2026):
//
//   - a number is itself; null (Kitwork's one value for null and undefined) is 0, as JavaScript's null;
//   - true is 1, false is 0;
//   - a string is read as JavaScript reads it (StringToNumber): "5" is 5, "" and "  " are 0, "0x1f" is
//     31, "a" and "5a" are NaN;
//   - an engine time is its time value in milliseconds, as a JavaScript date's; a duration its length in
//     milliseconds;
//   - a date made by new Date() is its time value in milliseconds, NaN for an invalid date, as in
//     JavaScript (DateTimeValue);
//   - an array is read through the text JavaScript gives it, its items joined by commas (ParseText):
//     [] is 0, [5], ["5"] and [[" 5 "]] are 5, [null] is 0, and two items or more are NaN, for a comma
//     is in their text (16/10/2026, the owner's E2: every array was NaN, since Kitwork's own text of
//     [5] is "[5]");
//   - any other object, a function and a value of the host are NaN. No valueOf of the program is
//     called;
//   - a failure stays the failure it is.
func ToNumber(v Value) Value {
	switch v.K {
	case Number, Invalid:
		return v
	case Map:
		if ms, ok := DateTimeValue(v); ok {
			return Value{K: Number, N: ms}
		}
	case Array:
		return Value{K: Number, N: StringToNumber(arrayParseText(v))}
	case Nil:
		return Value{K: Number, N: 0}
	case Bool:
		if v.N > 0 {
			return Value{K: Number, N: 1}
		}
		return Value{K: Number, N: 0}
	case String:
		text, _ := v.V.(string)
		return Value{K: Number, N: StringToNumber(text)}
	case Time:
		return Value{K: Number, N: float64(time.Unix(0, int64(v.N)).UnixMilli())}
	case Duration:
		return Value{K: Number, N: v.N / 1e6}
	}
	return Value{K: Number, N: math.NaN()}
}

// dateMark is what the valueOf of a date made by new Date() carries (NewDateValueOf): the date object it
// belongs to and the date's time value.
type dateMark struct {
	owner map[string]Value
	ms    float64
}

// NewDateValueOf is the valueOf of owner, a date object made by new Date() (builtins.Date), whose time
// value is ms (NaN for an invalid date): a function the program calls, and the mark by which ToNumber,
// isNaN, isFinite, Number(), Math and ++ know owner for a date and read its time value as JavaScript's
// ToNumber does (15/10/2026). Copied into another object ({ valueOf: d.valueOf }, { ...d }) it is not
// that object's own, and ToNumber of that object is NaN, as of any object.
func NewDateValueOf(owner map[string]Value, ms float64) Value {
	return Value{K: Func, V: &FuncObject{
		Fn:   func(args ...Value) Value { return Value{K: Number, N: ms} },
		date: &dateMark{owner: owner, ms: ms},
	}}
}

// DateTimeValue is the time value of v when v is a date made by new Date(): its own valueOf carries the
// mark (NewDateValueOf).
func DateTimeValue(v Value) (float64, bool) {
	m, ok := v.V.(map[string]Value)
	if !ok {
		return 0, false
	}
	function, ok := m["valueOf"].V.(*FuncObject)
	if !ok || function.date == nil || reflect.ValueOf(function.date.owner).Pointer() != reflect.ValueOf(m).Pointer() {
		return 0, false
	}
	return function.date.ms, true
}

// ParseFloatText is JavaScript's parseFloat of text: white space and line terminators before it dropped,
// then the longest beginning that is a decimal number, with an optional sign ("3.5px" is 3.5, ".5" 0.5,
// "-.5e1x" -5, "1e" 1, "1.2.3" 1.2, "1e400" Infinity, "-0" -0), or "Infinity" with an optional sign
// ("Infinity!" is Infinity). A text that begins with neither is NaN: "", "abc", ".", "inf". "0x10" is 0
// and "1_000" 1, the numbers they begin with. Until 16/10/2026 (the owner's E2) parseFloat read the
// whole text, and what was no number was 0 ("3.5px" was 0).
func ParseFloatText(text string) float64 {
	text = trimJavaScriptSpace(text)
	unsigned := text
	if unsigned != "" && (unsigned[0] == '+' || unsigned[0] == '-') {
		unsigned = unsigned[1:]
	}
	if strings.HasPrefix(unsigned, "Infinity") {
		if text[0] == '-' {
			return math.Inf(-1)
		}
		return math.Inf(1)
	}
	end := decimalPrefix(text)
	if end == 0 {
		return math.NaN()
	}
	// ParseFloat reads every decimal number decimalPrefix lets through, the sign with it; past the
	// largest number it gives ±Infinity with ErrRange, as JavaScript does
	n, _ := strconv.ParseFloat(text[:end], 64)
	return n
}

// ParseIntText is JavaScript's parseInt(text, radix), radix already made an integer by ToInt32: white
// space and line terminators before the text dropped, an optional sign, then 0x or 0X, which makes it
// base 16, when radix is 0 or 16; radix 0 is 10, and a radix outside 2 to 36 is NaN. Then as many
// digits of the base as follow, of any case: "12px" is 12, "0x1f" 31, "-0x1f" -31, "1e3" 1, "08" 8,
// "-0" -0. No digit is NaN: "", "abc", "0x", ".5". Digits past 2^53 are rounded to the nearest number
// ("99999999999999999999" is 1e20), which JavaScript asks for in the bases 10, 2, 4, 8, 16 and 32; in
// any other base it lets an engine approximate, and V8 (node, Chrome) differs in the last bit at times:
// parseInt("ffffffffffffffff", 36) is 3.4108547614056005e+24 here and 3.410854761405601e+24 in node
// (the parity row parse-int-past-2-53-in-another-base). Until 16/10/2026 (the owner's E2)
// parseInt read 0x only with the radix 16, made a radix outside 2 to 36 ten, gave 0 for no digit and
// stopped at the int64 range.
func ParseIntText(text string, radix int32) float64 {
	text = trimJavaScriptSpace(text)
	negative := false
	if text != "" && (text[0] == '+' || text[0] == '-') {
		negative = text[0] == '-'
		text = text[1:]
	}
	base := int(radix)
	prefixed := true // whether 0x may lead the digits
	switch {
	case base == 0:
		base = 10
	case base < 2 || base > 36:
		return math.NaN()
	default:
		prefixed = base == 16
	}
	if prefixed && len(text) >= 2 && text[0] == '0' && (text[1] == 'x' || text[1] == 'X') {
		text, base = text[2:], 16
	}
	end := 0
	for end < len(text) && digitValue(text[end]) < base {
		end++
	}
	if end == 0 {
		return math.NaN()
	}
	var n float64
	if base == 10 {
		// decimal digits alone: ParseFloat rounds them as JavaScript does, however many there are
		n, _ = strconv.ParseFloat(text[:end], 64)
	} else {
		n = integerText(text[:end], base)
	}
	if negative {
		return -n
	}
	return n
}

// ToInt32 is JavaScript's ToInt32 of a number, which parseInt makes of its radix: NaN and the infinities
// are 0, any other number loses its fraction and wraps into -2^31 to 2^31 - 1 (16.9 is 16, 2^32 + 16
// is 16, -1 is -1).
func ToInt32(n float64) int32 {
	if n != n || math.IsInf(n, 0) {
		return 0
	}
	n = math.Mod(math.Trunc(n), 1<<32)
	if n < 0 {
		n += 1 << 32
	}
	return int32(uint32(n))
}

// ParseText is the text parseInt and parseFloat read of v, which JavaScript's ToString gives it: a
// string itself, a number as String(n) writes it ("1e+21"), null "null", true "true", an array its
// items joined by commas as far as a number reads it (arrayParseText: [" 15 ", 3] reads as " 15 ,"),
// an object or a function a text that holds no number, as JavaScript's "[object Object]" and a
// function's source hold none; an engine time or duration its Kitwork text.
func ParseText(v Value) string {
	switch v.K {
	case String:
		text, _ := v.V.(string)
		return text
	case Array:
		return arrayParseText(v)
	case Map, Func:
		return objectText
	}
	return v.Text()
}

// objectText is what JavaScript's String gives a plain object, and what ParseText gives every object and
// function: a text no number begins.
const objectText = "[object Object]"

// arrayParseText is the text JavaScript gives the array v (String(v): its items joined by commas, null
// as ""), as far as a number reads it: the text of its first item, walking into the first item while
// it is an array, then a comma when any of those arrays holds more than one item. A number ends at the
// comma, so what follows it never changes what Number, parseInt or parseFloat read, and the text needs
// no more ([[5, 6]] is "5,6" in JavaScript and "5," here; both are NaN to Number and 5 to parseInt). An
// array met again on its own path is "", as JavaScript's join writes an array that holds itself; one
// nested past MaxDepth, where JavaScript runs out of stack, is a text no number begins.
func arrayParseText(v Value) string {
	var w walk
	w.enterValue(v, "", rootStep)
	text, comma := "", false
	for {
		items := v.Array()
		if len(items) > 1 {
			comma = true
		}
		if len(items) == 0 {
			break
		}
		first := items[0]
		if first.K != Array {
			if first.K != Nil {
				text = ParseText(first)
			}
			break
		}
		if err := w.enterValue(first, "", 0); err != nil {
			if !err.Cycle {
				text = deepText
			}
			break
		}
		v = first
	}
	if comma {
		return text + ","
	}
	return text
}

// StringToNumber reads text as JavaScript's Number(text) does (ECMAScript StringToNumber): white space
// and line terminators around it are dropped; nothing left is 0; then "Infinity" with an optional sign,
// a decimal literal with an optional sign ("12", "-1.5", ".5", "5.", "1e3"), or 0x, 0o, 0b and an
// integer in that base without a sign. Anything else is NaN: "5a", "1_000", "inf", "-0x10".
func StringToNumber(text string) float64 {
	text = trimJavaScriptSpace(text)
	if text == "" {
		return 0
	}
	switch text {
	case "Infinity", "+Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}
	if len(text) > 2 && text[0] == '0' {
		base := 0
		switch text[1] {
		case 'x', 'X':
			base = 16
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		}
		if base != 0 {
			return integerText(text[2:], base)
		}
	}
	if !decimalLiteral(text) {
		return math.NaN()
	}
	// ParseFloat reads every decimal literal the check lets through; past the largest number it gives
	// ±Infinity with ErrRange, and JavaScript gives the same
	n, _ := strconv.ParseFloat(text, 64)
	return n
}

// decimalLiteral reports [+-]? (digits [. digits?] | . digits) ([eE] [+-]? digits)?.
func decimalLiteral(text string) bool {
	end := decimalPrefix(text)
	return end > 0 && end == len(text)
}

// decimalPrefix is the length of the longest beginning of text that is a decimal literal, 0 when none
// is: [+-]? (digits [. digits?] | . digits) ([eE] [+-]? digits)?. An e without digits after it ends the
// number before the e ("1e" is "1", "1e+x" is "1").
func decimalPrefix(text string) int {
	at := 0
	if at < len(text) && (text[at] == '+' || text[at] == '-') {
		at++
	}
	digits := func() int {
		start := at
		for at < len(text) && text[at] >= '0' && text[at] <= '9' {
			at++
		}
		return at - start
	}
	whole := digits()
	fraction := 0
	if at < len(text) && text[at] == '.' {
		at++
		fraction = digits()
	}
	if whole == 0 && fraction == 0 {
		return 0
	}
	end := at
	if at < len(text) && (text[at] == 'e' || text[at] == 'E') {
		at++
		if at < len(text) && (text[at] == '+' || text[at] == '-') {
			at++
		}
		if digits() > 0 {
			end = at
		}
	}
	return end
}

// integerText reads digits in a base from 2 to 36 (0x, 0o and 0b of Number, parseInt's radix), of any
// length: the nearest number to the integer they write, as JavaScript rounds it. No digit, or one the
// base does not have, is NaN.
func integerText(digits string, base int) float64 {
	if digits == "" {
		return math.NaN()
	}
	for at := 0; at < len(digits); at++ {
		if digitValue(digits[at]) >= base {
			return math.NaN()
		}
	}
	if n, err := strconv.ParseUint(digits, base, 64); err == nil {
		return float64(n)
	}
	whole, ok := new(big.Int).SetString(digits, base)
	if !ok {
		return math.NaN()
	}
	n, _ := new(big.Float).SetInt(whole).Float64()
	return n
}

func digitValue(char byte) int {
	switch {
	case char >= '0' && char <= '9':
		return int(char - '0')
	case char >= 'a' && char <= 'z':
		return int(char-'a') + 10
	case char >= 'A' && char <= 'Z':
		return int(char-'A') + 10
	}
	return 99
}

// trimJavaScriptSpace drops what JavaScript's StringToNumber drops around a number: its WhiteSpace and
// LineTerminator characters. Go's unicode.IsSpace differs on two: it takes U+0085 and not U+FEFF.
func trimJavaScriptSpace(text string) string {
	start, end := 0, len(text)
	for start < end {
		char, size := utf8.DecodeRuneInString(text[start:end])
		if !javaScriptSpace(char) {
			break
		}
		start += size
	}
	for end > start {
		char, size := utf8.DecodeLastRuneInString(text[start:end])
		if !javaScriptSpace(char) {
			break
		}
		end -= size
	}
	return text[start:end]
}

func javaScriptSpace(char rune) bool {
	switch char {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x00A0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return char >= 0x2000 && char <= 0x200A
}
