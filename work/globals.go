package work

import (
	"math"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/kitwork/engine/value"
)

// injectJSCompat bổ sung các global chuẩn JavaScript (Math, Date) cho VM,
// giúp script của tenant chạy giống hệt môi trường JS quen thuộc:
//
//	Math.floor(7 / 2)        // 3
//	Date.now()               // epoch milliseconds
//	const d = new Date();    // `new` được parser chấp nhận như tiền tố JS
//	d.getFullYear()          // 2026
func injectJSCompat(globals map[string]value.Value) {
	globals["Math"] = buildMathObject()
	globals["Date"] = buildDateConstructor()
	globals["Object"] = buildObjectGlobal()
	globals["Number"] = buildNumberGlobal()
	globals["String"] = buildStringGlobal()
	globals["Boolean"] = buildBooleanGlobal()
}

/* =============================================================================
   Math
   ============================================================================= */

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

func buildMathObject() value.Value {
	m := map[string]value.Value{
		// Hằng số
		"PI":      value.New(math.Pi),
		"E":       value.New(math.E),
		"LN2":     value.New(math.Ln2),
		"LN10":    value.New(math.Ln10),
		"LOG2E":   value.New(math.Log2E),
		"LOG10E":  value.New(math.Log10E),
		"SQRT2":   value.New(math.Sqrt2),
		"SQRT1_2": value.New(1 / math.Sqrt2),

		// Một đối số
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

		// JS Math.round làm tròn .5 lên trên (khác math.Round của Go với số âm)
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

		// Hai đối số
		"pow":   mathFn2(math.Pow),
		"atan2": mathFn2(math.Atan2),
		"hypot": mathFn2(math.Hypot),

		// Variadic
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

/* =============================================================================
   Date
   ============================================================================= */

var dateLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02",
	"2006/01/02",
	"01/02/2006",
	time.RFC1123,
	time.RFC1123Z,
}

func parseDateString(s string) (time.Time, bool) {
	for _, layout := range dateLayouts {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// timeFromParts dựng time.Time từ các đối số kiểu JS:
// (year, monthIndex, day, hours, minutes, seconds, ms) — month tính từ 0.
func timeFromParts(args []value.Value, loc *time.Location) time.Time {
	part := func(idx int, def float64) int {
		if idx < len(args) && args[idx].K == value.Number {
			return int(args[idx].N)
		}
		return int(def)
	}
	year := part(0, 1970)
	month := part(1, 0)
	day := part(2, 1)
	hour := part(3, 0)
	minute := part(4, 0)
	sec := part(5, 0)
	ms := part(6, 0)
	return time.Date(year, time.Month(month+1), day, hour, minute, sec, ms*1e6, loc)
}

func timeFromArgs(args []value.Value) time.Time {
	if len(args) == 0 {
		return time.Now()
	}
	if len(args) == 1 {
		switch args[0].K {
		case value.Number:
			ms := int64(args[0].N)
			return time.UnixMilli(ms)
		case value.String:
			if t, ok := parseDateString(args[0].Text()); ok {
				return t
			}
			return time.UnixMilli(0)
		}
		return time.Now()
	}
	return timeFromParts(args, time.Local)
}

// newDateObject trả về object mô phỏng instance Date của JS,
// với các getter quen thuộc đóng trên một time.Time bất biến.
func newDateObject(t time.Time) value.Value {
	num := func(f func() float64) value.Value {
		return value.NewFunc(func(args ...value.Value) value.Value {
			return value.New(f())
		})
	}
	str := func(f func() string) value.Value {
		return value.NewFunc(func(args ...value.Value) value.Value {
			return value.NewString(f())
		})
	}

	isoString := func() string {
		return t.UTC().Format("2006-01-02T15:04:05.000Z")
	}

	obj := map[string]value.Value{
		"getTime": num(func() float64 { return float64(t.UnixMilli()) }),
		"valueOf": num(func() float64 { return float64(t.UnixMilli()) }),

		"getFullYear":     num(func() float64 { return float64(t.Year()) }),
		"getMonth":        num(func() float64 { return float64(int(t.Month()) - 1) }),
		"getDate":         num(func() float64 { return float64(t.Day()) }),
		"getDay":          num(func() float64 { return float64(int(t.Weekday())) }),
		"getHours":        num(func() float64 { return float64(t.Hour()) }),
		"getMinutes":      num(func() float64 { return float64(t.Minute()) }),
		"getSeconds":      num(func() float64 { return float64(t.Second()) }),
		"getMilliseconds": num(func() float64 { return float64(t.Nanosecond() / 1e6) }),

		"getUTCFullYear": num(func() float64 { return float64(t.UTC().Year()) }),
		"getUTCMonth":    num(func() float64 { return float64(int(t.UTC().Month()) - 1) }),
		"getUTCDate":     num(func() float64 { return float64(t.UTC().Day()) }),
		"getUTCHours":    num(func() float64 { return float64(t.UTC().Hour()) }),

		"getTimezoneOffset": num(func() float64 {
			_, offsetSec := t.Zone()
			return float64(-offsetSec / 60)
		}),

		"toISOString":        str(isoString),
		"toJSON":             str(isoString),
		"toString":           str(func() string { return t.Format("Mon Jan 02 2006 15:04:05 GMT-0700 (MST)") }),
		"toDateString":       str(func() string { return t.Format("Mon Jan 02 2006") }),
		"toTimeString":       str(func() string { return t.Format("15:04:05 GMT-0700 (MST)") }),
		"toLocaleDateString": str(func() string { return t.Format("02/01/2006") }),
		"toLocaleTimeString": str(func() string { return t.Format("15:04:05") }),
		"toLocaleString":     str(func() string { return t.Format("02/01/2006 15:04:05") }),
	}
	return value.New(obj)
}

/* =============================================================================
   Object / Number / String / Boolean globals
   ============================================================================= */

// buildObjectGlobal cung cấp các static method quen thuộc của Object.
// LƯU Ý: thứ tự key của keys/values/entries KHÔNG đảm bảo (Go map) —
// khác JS giữ thứ tự chèn; cần thứ tự ổn định hãy .sort() kết quả.
func buildObjectGlobal() value.Value {
	ctor := func(args ...value.Value) value.Value {
		if len(args) > 0 {
			return args[0]
		}
		return value.New(map[string]value.Value{})
	}

	props := map[string]value.Value{
		"keys": value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) == 0 || args[0].K != value.Map {
				return value.New([]value.Value{})
			}
			m := args[0].Map()
			out := make([]value.Value, 0, len(m))
			for k := range m {
				out = append(out, value.NewString(k))
			}
			return value.New(out)
		}),
		"values": value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) == 0 || args[0].K != value.Map {
				return value.New([]value.Value{})
			}
			m := args[0].Map()
			out := make([]value.Value, 0, len(m))
			for _, v := range m {
				out = append(out, v)
			}
			return value.New(out)
		}),
		"entries": value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) == 0 || args[0].K != value.Map {
				return value.New([]value.Value{})
			}
			m := args[0].Map()
			out := make([]value.Value, 0, len(m))
			for k, v := range m {
				out = append(out, value.New([]value.Value{value.NewString(k), v}))
			}
			return value.New(out)
		}),
		// assign(target, ...sources) — mutate target và trả về target, chuẩn JS.
		"assign": value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) == 0 {
				return value.New(map[string]value.Value{})
			}
			target := args[0]
			if target.K != value.Map {
				return target
			}
			tm := target.Map()
			for _, src := range args[1:] {
				if src.K == value.Map {
					for k, v := range src.Map() {
						tm[k] = v
					}
				}
			}
			return target
		}),
		"fromEntries": value.NewFunc(func(args ...value.Value) value.Value {
			out := map[string]value.Value{}
			if len(args) == 0 || args[0].K != value.Array {
				return value.New(out)
			}
			for _, pair := range args[0].Array() {
				if pair.K == value.Array && pair.Len() >= 2 {
					out[pair.Index(0).Text()] = pair.Index(1)
				}
			}
			return value.New(out)
		}),
	}

	return value.NewFuncObject(ctor, props)
}

// buildNumberGlobal — Number(x) chuyển đổi kiểu + các static quen thuộc.
func buildNumberGlobal() value.Value {
	toNumber := func(v value.Value) value.Value {
		switch v.K {
		case value.Number:
			return v
		case value.Bool:
			return value.Value{K: value.Number, N: v.N}
		case value.String:
			s := strings.TrimSpace(v.Text())
			if s == "" {
				return value.Value{K: value.Number, N: 0}
			}
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				return value.New(f)
			}
			// JS trả NaN — VM không có NaN, trả Nil để thể hiện "không phải số"
			return value.Value{K: value.Nil}
		case value.Nil:
			return value.Value{K: value.Number, N: 0}
		}
		return value.Value{K: value.Nil}
	}

	ctor := func(args ...value.Value) value.Value {
		if len(args) == 0 {
			return value.Value{K: value.Number, N: 0}
		}
		return toNumber(args[0])
	}

	props := map[string]value.Value{
		"MAX_SAFE_INTEGER": value.New(9007199254740991.0),
		"MIN_SAFE_INTEGER": value.New(-9007199254740991.0),
		"EPSILON":          value.New(2.220446049250313e-16),

		"isInteger": value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) == 0 || args[0].K != value.Number {
				return value.FALSE
			}
			return value.ToBool(args[0].N == float64(int64(args[0].N)))
		}),
		"isFinite": value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) == 0 || args[0].K != value.Number {
				return value.FALSE
			}
			return value.ToBool(!math.IsInf(args[0].N, 0) && !math.IsNaN(args[0].N))
		}),
		"parseFloat": value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) == 0 {
				return value.Value{K: value.Nil}
			}
			return toNumber(args[0])
		}),
		"parseInt": value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) == 0 {
				return value.Value{K: value.Nil}
			}
			n := toNumber(args[0])
			if n.K != value.Number {
				return n
			}
			return value.Value{K: value.Number, N: float64(int64(n.N))}
		}),
	}

	return value.NewFuncObject(ctor, props)
}

// buildStringGlobal — String(x) chuyển mọi giá trị thành chuỗi.
func buildStringGlobal() value.Value {
	ctor := func(args ...value.Value) value.Value {
		if len(args) == 0 {
			return value.NewString("")
		}
		return value.NewString(args[0].Text())
	}

	props := map[string]value.Value{
		"fromCharCode": value.NewFunc(func(args ...value.Value) value.Value {
			runes := make([]rune, 0, len(args))
			for _, a := range args {
				if a.K == value.Number {
					runes = append(runes, rune(int(a.N)))
				}
			}
			return value.NewString(string(runes))
		}),
	}

	return value.NewFuncObject(ctor, props)
}

// buildBooleanGlobal — Boolean(x) theo truthiness chuẩn JS.
func buildBooleanGlobal() value.Value {
	return value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) == 0 {
			return value.FALSE
		}
		return value.ToBool(args[0].Truthy())
	})
}

// buildDateConstructor tạo global Date: vừa gọi được như hàm/constructor
// (`Date()`, `new Date()`, `new Date(ms)`, `new Date("2026-06-12")`,
// `new Date(2026, 5, 12)`), vừa có static method (`Date.now()`, `Date.parse()`).
func buildDateConstructor() value.Value {
	ctor := func(args ...value.Value) value.Value {
		return newDateObject(timeFromArgs(args))
	}

	props := map[string]value.Value{
		"now": value.NewFunc(func(args ...value.Value) value.Value {
			return value.New(float64(time.Now().UnixMilli()))
		}),
		"parse": value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) == 0 {
				return value.New(0)
			}
			if t, ok := parseDateString(args[0].Text()); ok {
				return value.New(float64(t.UnixMilli()))
			}
			return value.New(0)
		}),
		"UTC": value.NewFunc(func(args ...value.Value) value.Value {
			return value.New(float64(timeFromParts(args, time.UTC).UnixMilli()))
		}),
	}

	return value.NewFuncObject(ctor, props)
}
