package hydrate

import (
	"math"
	"strings"
	"unicode/utf8"

	"github.com/kitwork/engine/value"
)

// The expression globals — the fourteen the design named (ideaship-extension §4) and the spec
// then listed as fact while the code had none of them: Math.ceil(7 / 4) read as undefined, on both
// engines, and said nothing. Added 29/09.
//
//	Math · Date · JSON · Number · String · Boolean · Array · Object.keys
//	parseInt · parseFloat · encodeURIComponent · decodeURIComponent · isNaN · isFinite
//
// A global sits at the END of name resolution: a name the page declares wins, as a local does in
// JavaScript. There is no `new` in the grammar, so Date offers now/parse/UTC and nothing that
// makes a Date object; `Object` is only { keys } — not the real Object, whose defineProperty and
// friends stay out of reach.
//
// Each answers as JavaScript's does in the browser's kernel (16/10/2026): every member node has (the
// whole of Math, String.fromCharCode, Array.of, Date.UTC …), with the same functions, lengths and names.
// What belongs to the moment and the place the page is open — Date.now(), Date(), Math.random(), a
// local time Date.parse reads — the server leaves undefined, and the kernel answers in the browser.
var expressionGlobals = map[string]bool{
	"Math": true, "Date": true, "JSON": true, "Number": true, "String": true, "Boolean": true,
	"Array": true, "Object": true, "parseInt": true, "parseFloat": true,
	"encodeURIComponent": true, "decodeURIComponent": true, "isNaN": true, "isFinite": true,
}

// globalRef is what a global name evaluates to on the server: the name, answered when it is called
// or read through.
type globalRef string

func lookupGlobal(name string) (globalRef, bool) {
	if expressionGlobals[name] {
		return globalRef(name), true
	}
	return "", false
}

// callable reports a global that is a function: all but Math, JSON and the kernel's Object, which is
// a plain { keys }.
func (g globalRef) callable() bool {
	switch g {
	case "Math", "JSON", "Object":
		return false
	}
	return true
}

// text is the global as String writes it: Math and JSON by their tag, the kernel's Object as the plain
// object it is, a function as V8 writes a built-in one.
func (g globalRef) text() string {
	switch g {
	case "Math", "JSON":
		return "[object " + string(g) + "]"
	case "Object":
		return "[object Object]"
	}
	return "function " + string(g) + "() { [native code] }"
}

// functionLengths are the lengths of the global functions that have no table of their own.
var functionLengths = map[globalRef]int{
	"parseInt": 2, "parseFloat": 1, "isNaN": 1, "isFinite": 1, "encodeURIComponent": 1, "decodeURIComponent": 1,
}

// own is a global's own member: Math.PI, String.fromCharCode, Object.keys, String.length …
func (g globalRef) own(key string) (any, bool) {
	switch g {
	case "Object":
		if key == "keys" {
			return nativeFunc{"Object", "keys"}, true
		}
		return nil, false
	case "parseInt", "parseFloat", "isNaN", "isFinite", "encodeURIComponent", "decodeURIComponent":
		switch key {
		case "length":
			return float64(functionLengths[g]), true
		case "name":
			return string(g), true
		}
		return nil, false
	}
	if member, ok := nativeMembers[string(g)][key]; ok && member.kind != memberAccessor {
		v, _ := fromPrototype(string(g), key)
		return v, true
	}
	return nil, false
}

// callGlobal is a bare call of a global: Number(x), parseInt(s), isNaN(x), Array(3) …
func (m *machine) callGlobal(f *frame, g globalRef, args []any) (any, error) {
	arg := func(i int) any {
		if i < len(args) {
			return args[i]
		}
		return Undefined
	}
	switch g {
	// Number, parseInt, parseFloat, isNaN and isFinite read as JavaScript's (16/10/2026, the owner's E2),
	// through the VM's own readers in value, so the server, the browser and the VM give one answer:
	// Number("0x1f") 31, Number(null) 0, Number([5]) 5, parseInt("0x1f") 31, parseInt("7", 37) NaN,
	// parseFloat("3.5px") 3.5, parseFloat("1e400") Infinity, isNaN(null) false
	case "Number":
		if len(args) == 0 {
			return float64(0), nil
		}
		return m.toNumber(f, arg(0))
	case "String":
		if len(args) == 0 {
			return "", nil
		}
		return m.toString(f, arg(0))
	case "Boolean":
		return truthy(arg(0)), nil
	case "parseInt":
		text, err := m.toString(f, arg(0))
		if err != nil {
			return nil, err
		}
		radix, err := m.toNumber(f, arg(1))
		if err != nil {
			return nil, err
		}
		return parseIntV8(text, value.ToInt32(radix)), nil
	case "parseFloat":
		text, err := m.toString(f, arg(0))
		if err != nil {
			return nil, err
		}
		return value.ParseFloatText(text), nil
	case "isNaN", "isFinite":
		n, err := m.toNumber(f, arg(0))
		if err != nil {
			return nil, err
		}
		if g == "isNaN" {
			return n != n, nil
		}
		return !math.IsNaN(n) && !math.IsInf(n, 0), nil
	case "encodeURIComponent":
		text, err := m.toString(f, arg(0))
		if err != nil {
			return nil, err
		}
		return encodeURIComponentJS(text)
	case "decodeURIComponent":
		text, err := m.toString(f, arg(0))
		if err != nil {
			return nil, err
		}
		return decodeURIComponentJS(text)
	case "Array":
		return m.arrayConstruct(f, args)
	case "Date":
		// Date() is the time now as text: the browser's, never the server's
		return Undefined, nil
	}
	return nil, typeError(string(g) + " is not a function")
}

// encodeURIComponentJS escapes everything but A–Z a–z 0–9 - _ . ! ~ * ' ( ), as JavaScript does, each
// character as the %XX of its UTF-8 bytes; a lone half of a UTF-16 pair is a URIError there and here.
func encodeURIComponentJS(s string) (string, error) {
	const keep = "-_.!~*'()"
	var b strings.Builder
	for i := 0; i < len(s); {
		if _, lone := loneHalf(s[i:]); lone {
			return "", uriError("URI malformed")
		}
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || strings.IndexByte(keep, c) >= 0 {
			b.WriteByte(c)
			i++
			continue
		}
		b.WriteByte('%')
		b.WriteByte("0123456789ABCDEF"[c>>4])
		b.WriteByte("0123456789ABCDEF"[c&15])
		i++
	}
	return b.String(), nil
}

// decodeURIComponentJS is JavaScript's decodeURIComponent: each %XX run read as UTF-8; a % without two
// hex digits after it, or bytes that are no UTF-8, is a URIError.
func decodeURIComponentJS(s string) (string, error) {
	if !strings.Contains(s, "%") {
		return s, nil
	}
	malformed := uriError("URI malformed")
	var b strings.Builder
	for k := 0; k < len(s); k++ {
		if s[k] != '%' {
			b.WriteByte(s[k])
			continue
		}
		if k+2 >= len(s) {
			return "", malformed
		}
		hi, lo := unhex(s[k+1]), unhex(s[k+2])
		if hi < 0 || lo < 0 {
			return "", malformed
		}
		c := byte(hi<<4 | lo)
		k += 2
		if c < 0x80 {
			b.WriteByte(c)
			continue
		}
		n := utf8Length(c)
		if n < 2 || k+3*(n-1) >= len(s) {
			return "", malformed
		}
		octets := []byte{c}
		for j := 1; j < n; j++ {
			k++
			if s[k] != '%' {
				return "", malformed
			}
			hi, lo := unhex(s[k+1]), unhex(s[k+2])
			if hi < 0 || lo < 0 {
				return "", malformed
			}
			c := byte(hi<<4 | lo)
			if c&0xC0 != 0x80 {
				return "", malformed
			}
			k += 2
			octets = append(octets, c)
		}
		r, size := utf8.DecodeRune(octets)
		if r == utf8.RuneError && size <= 1 || size != len(octets) {
			return "", malformed
		}
		b.WriteRune(r)
	}
	return b.String(), nil
}

func unhex(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// utf8Length is how many bytes a UTF-8 character that starts with c has, 0 for no start byte.
func utf8Length(c byte) int {
	switch {
	case c < 0x80:
		return 1
	case c>>5 == 0x6:
		return 2
	case c>>4 == 0xE:
		return 3
	case c>>3 == 0x1E:
		return 4
	}
	return 0
}

// mathRound is JavaScript's Math.round, as the VM's (builtins round): the nearest whole number, a half
// up; NaN, the infinities, 0 and -0 themselves, -0 for -0.5 to 0, and a number past 2^52 (already
// whole) itself. floor(x + 0.5) made 0.49999999999999994 1, 4503599627370497 4503599627370498 and -0.4
// 0 here (16/10/2026), where the VM and the browser give 0, 4503599627370497 and -0.
func mathRound(x float64) float64 {
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

// parseIntV8 is parseInt as the browser's V8 reads it. In base 10 and in the powers of two JavaScript asks
// for the nearest number, which value.ParseIntText (the VM's reader) gives; in any other base it lets an
// engine approximate past 2^53, and V8 adds the digits up in parts of at most 32 bits, each part added to
// the sum times its multiplier in doubles (StringToIntHelper's generic case, after the leading zeros),
// which can differ from the nearest number in the last bit: parseInt('9007199254740993', 36) is
// 1.9896986116031807e+24 there and the nearest 1.989698611603181e+24 (16/10/2026: the server gave the
// nearest, and the page text changed as it hydrated). Each product is made a double before the sum, so
// no fused multiply-add rounds the two at once.
func parseIntV8(text string, radix int32) float64 {
	n := value.ParseIntText(text, radix)
	base := int(radix)
	if n != n || math.Abs(n) < 1<<53 || base == 0 || base == 10 || base&(base-1) == 0 {
		return n // exact below 2^53 on both, and the nearest number where JavaScript asks for it
	}
	digits := trimJS(text)
	negative := false
	if digits != "" && (digits[0] == '+' || digits[0] == '-') {
		negative = digits[0] == '-'
		digits = digits[1:]
	}
	end := 0
	for end < len(digits) && radixDigit(digits[end]) < base {
		end++
	}
	// V8 skips the leading zeros first (StringToIntHelper::DetectRadixInternal), which moves where the
	// parts of the sum begin
	digits = strings.TrimLeft(digits[:end], "0")
	const maxMultiplier = 0xFFFFFFFF / 36
	sum := 0.0
	for i := 0; i < len(digits); {
		part, multiplier := uint32(0), uint32(1)
		for i < len(digits) {
			next := multiplier * uint32(base)
			if next > maxMultiplier {
				break
			}
			part = part*uint32(base) + uint32(radixDigit(digits[i]))
			multiplier = next
			i++
		}
		sum = float64(sum*float64(multiplier)) + float64(part)
	}
	if negative {
		return -sum
	}
	return sum
}

// radixDigit is the value of a digit in any base up to 36, of either case; 99 for what is no digit.
func radixDigit(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'Z':
		return int(c-'A') + 10
	}
	return 99
}
