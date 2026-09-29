package hydrate

import (
	"encoding/json"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
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
// The server answers the deterministic part. Date and Math.random belong to the moment the page is
// open, so the server gives nil for them and the kernel answers in the browser — the same split as
// a registered component method.
var expressionGlobals = map[string]bool{
	"Math": true, "Date": true, "JSON": true, "Number": true, "String": true, "Boolean": true,
	"Array": true, "Object": true, "parseInt": true, "parseFloat": true,
	"encodeURIComponent": true, "decodeURIComponent": true, "isNaN": true, "isFinite": true,
}

// globalRef is what a global name evaluates to on the server: the name, answered when it is called
// or read through.
type globalRef string

func globalMember(g globalRef, name string) any {
	if g == "Math" {
		switch name {
		case "PI":
			return math.Pi
		case "E":
			return math.E
		}
	}
	return nil
}

// callGlobal is a bare call of a global: Number(x), parseInt(s), isNaN(x) …
func callGlobal(g globalRef, args []any) any {
	arg := func(i int) any {
		if i < len(args) {
			return args[i]
		}
		return nil
	}
	switch g {
	case "Number":
		if len(args) == 0 {
			return float64(0)
		}
		return num(arg(0))
	case "String":
		return toStr(arg(0))
	case "Boolean":
		return truthy(arg(0))
	case "parseInt":
		radix := 10
		if len(args) > 1 {
			radix = int(num(arg(1)))
		}
		return parseIntJS(toStr(arg(0)), radix)
	case "parseFloat":
		return parseFloatJS(toStr(arg(0)))
	case "isNaN":
		return math.IsNaN(num(arg(0)))
	case "isFinite":
		n := num(arg(0))
		return !math.IsNaN(n) && !math.IsInf(n, 0)
	case "encodeURIComponent":
		return encodeURIComponentJS(toStr(arg(0)))
	case "decodeURIComponent":
		out, err := url.PathUnescape(toStr(arg(0)))
		if err != nil {
			return nil
		}
		return out
	}
	return nil
}

// callGlobalMethod is a method of a global: Math.max(…), JSON.parse(…), Object.keys(…) …
func callGlobalMethod(g globalRef, name string, args []any) any {
	nums := func() []float64 {
		out := make([]float64, len(args))
		for i, a := range args {
			out[i] = num(a)
		}
		return out
	}
	switch g {
	case "Math":
		n := nums()
		one := math.NaN()
		if len(n) > 0 {
			one = n[0]
		}
		switch name {
		case "abs":
			return math.Abs(one)
		case "ceil":
			return math.Ceil(one)
		case "floor":
			return math.Floor(one)
		case "trunc":
			return math.Trunc(one)
		case "round":
			return math.Floor(one + 0.5) // JavaScript rounds half UP: -2.5 → -2, where math.Round gives -3
		case "sign":
			if math.IsNaN(one) || one == 0 {
				return one
			}
			if one > 0 {
				return float64(1)
			}
			return float64(-1)
		case "sqrt":
			return math.Sqrt(one)
		case "pow":
			if len(n) < 2 {
				return math.NaN()
			}
			return math.Pow(n[0], n[1])
		case "max", "min":
			out := math.Inf(-1)
			if name == "min" {
				out = math.Inf(1)
			}
			for _, v := range n {
				if math.IsNaN(v) {
					return math.NaN()
				}
				if (name == "max" && v > out) || (name == "min" && v < out) {
					out = v
				}
			}
			return out
		}
	case "JSON":
		switch name {
		case "stringify":
			if len(args) == 0 {
				return nil
			}
			b, err := json.Marshal(args[0])
			if err != nil {
				return nil
			}
			return string(b)
		case "parse":
			var out any
			if len(args) == 0 || json.Unmarshal([]byte(toStr(args[0])), &out) != nil {
				return nil
			}
			return out
		}
	case "Object":
		if name == "keys" && len(args) > 0 {
			m, ok := args[0].(map[string]any)
			if !ok {
				return []any{}
			}
			// A Go map has no order; JavaScript keeps insertion order. Sorted is the one order both
			// sides can agree on for a server-built scope, and pages that need an order should keep
			// an array.
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			out := make([]any, len(keys))
			for i, k := range keys {
				out[i] = k
			}
			return out
		}
	case "Array":
		switch name {
		case "isArray":
			if len(args) == 0 {
				return false
			}
			_, ok := args[0].([]any)
			return ok
		case "from":
			if len(args) > 0 {
				if list, ok := args[0].([]any); ok {
					return append([]any{}, list...)
				}
				if s, ok := args[0].(string); ok {
					out := []any{}
					for _, r := range s {
						out = append(out, string(r))
					}
					return out
				}
			}
			return []any{}
		}
	}
	return nil // Date and anything unknown: answered in the browser, not here
}

// parseIntJS reads a leading integer the way JavaScript's parseInt does: leading space skipped, a
// sign, then as many digits of the radix as there are — "42px" is 42, "px" is NaN.
func parseIntJS(s string, radix int) any {
	s = strings.TrimSpace(s)
	negative := false
	if s != "" && (s[0] == '-' || s[0] == '+') {
		negative = s[0] == '-'
		s = s[1:]
	}
	if radix == 0 {
		radix = 10
		if len(s) > 1 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X') {
			radix, s = 16, s[2:]
		}
	} else if radix == 16 && len(s) > 1 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X') {
		s = s[2:]
	}
	if radix < 2 || radix > 36 {
		return math.NaN()
	}
	end := 0
	for end < len(s) {
		c := s[end]
		var d int
		switch {
		case c >= '0' && c <= '9':
			d = int(c - '0')
		case c >= 'a' && c <= 'z':
			d = int(c-'a') + 10
		case c >= 'A' && c <= 'Z':
			d = int(c-'A') + 10
		default:
			d = 99
		}
		if d >= radix {
			break
		}
		end++
	}
	if end == 0 {
		return math.NaN()
	}
	v, err := strconv.ParseInt(s[:end], radix, 64)
	if err != nil {
		return math.NaN()
	}
	if negative {
		v = -v
	}
	return float64(v)
}

// parseFloatJS reads the longest leading decimal number: "3.5kg" is 3.5.
func parseFloatJS(s string) any {
	s = strings.TrimSpace(s)
	best := math.NaN()
	for end := 1; end <= len(s); end++ {
		if f, err := strconv.ParseFloat(s[:end], 64); err == nil {
			best = f
		}
	}
	return best
}

// encodeURIComponentJS escapes everything but A–Z a–z 0–9 - _ . ! ~ * ' ( ), as JavaScript does.
func encodeURIComponentJS(s string) string {
	const keep = "-_.!~*'()"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || strings.IndexByte(keep, c) >= 0 {
			b.WriteByte(c)
			continue
		}
		b.WriteString("%" + strings.ToUpper(strconv.FormatInt(int64(c)+256, 16)[1:]))
	}
	return b.String()
}
