package builtins

import (
	"math"
	"time"

	"github.com/kitwork/engine/value"
)

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

// maxTimeValue is the largest time value a JavaScript date holds, in milliseconds either side of 1970:
// past it, or NaN or an infinity, the date is invalid.
const maxTimeValue = 8.64e15

// validTime reports whether ms is a time value a date holds (maxTimeValue).
func validTime(ms float64) bool { return math.Abs(ms) <= maxTimeValue }

// partsValid reports whether every part new Date(y, m, …) or Date.UTC is given is a finite number: one
// NaN or infinity makes the date invalid, as in JavaScript (int() made -9223372036854775808 of it).
func partsValid(args []value.Value) bool {
	for _, a := range args {
		if a.K == value.Number && (a.N != a.N || math.IsInf(a.N, 0)) {
			return false
		}
	}
	return true
}

func timeFromParts(args []value.Value, loc *time.Location) time.Time {
	part := func(idx int, def float64) int {
		if idx < len(args) && args[idx].K == value.Number {
			return args[idx].Int()
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

// timeFromArgs is the time new Date(args…) holds, and false for an invalid date (15/10/2026, as in
// JavaScript): a number that is NaN, an infinity or past maxTimeValue, text no layout reads (it was 1970,
// so isNaN(new Date(text).getTime()) never told a bad date), a part that is NaN or an infinity.
func timeFromArgs(args []value.Value) (time.Time, bool) {
	if len(args) == 0 {
		return time.Now(), true
	}
	if len(args) == 1 {
		switch args[0].K {
		case value.Number:
			if !validTime(args[0].N) {
				return time.Time{}, false
			}
			return time.UnixMilli(args[0].Int64()), true
		case value.String:
			return parseDateString(args[0].Text())
		}
		return time.Now(), true
	}
	if !partsValid(args) {
		return time.Time{}, false
	}
	return timeFromParts(args, time.Local), true
}

// newDateObject is the object new Date() makes, holding t; an invalid date (valid false) holds NaN as
// in JavaScript: every get… is NaN, every to…String "Invalid Date", toJSON null, and toISOString the
// INVALID_ARGUMENT failure (JavaScript's RangeError).
func newDateObject(t time.Time, valid bool) value.Value {
	num := func(f func() float64) value.Value {
		return value.NewFunc(func(args ...value.Value) value.Value {
			if !valid {
				return value.New(math.NaN())
			}
			return value.New(f())
		})
	}
	str := func(f func() string) value.Value {
		return value.NewFunc(func(args ...value.Value) value.Value {
			if !valid {
				return value.NewString("Invalid Date")
			}
			return value.NewString(f())
		})
	}

	isoString := func() string {
		return t.UTC().Format("2006-01-02T15:04:05.000Z")
	}

	ms := float64(t.UnixMilli())
	if !valid {
		ms = math.NaN()
	}
	obj := map[string]value.Value{
		"getTime": num(func() float64 { return ms }),

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

		"toISOString": value.NewFunc(func(args ...value.Value) value.Value {
			if !valid {
				return value.InvalidFailure(value.InvalidArgumentCode, "toISOString: ngày không hợp lệ (Invalid Date) không có dạng ISO")
			}
			return value.NewString(isoString())
		}),
		"toJSON": value.NewFunc(func(args ...value.Value) value.Value {
			if !valid {
				return value.Value{K: value.Nil}
			}
			return value.NewString(isoString())
		}),
		"toString":           str(func() string { return t.Format("Mon Jan 02 2006 15:04:05 GMT-0700 (MST)") }),
		"toDateString":       str(func() string { return t.Format("Mon Jan 02 2006") }),
		"toTimeString":       str(func() string { return t.Format("15:04:05 GMT-0700 (MST)") }),
		"toLocaleDateString": str(func() string { return t.Format("02/01/2006") }),
		"toLocaleTimeString": str(func() string { return t.Format("15:04:05") }),
		"toLocaleString":     str(func() string { return t.Format("02/01/2006 15:04:05") }),
	}
	// valueOf is the mark by which ToNumber (isNaN, Number(), Math, ++) knows this object for a date
	obj["valueOf"] = value.NewDateValueOf(obj, ms)
	return value.New(obj)
}

func Date() value.Value {
	ctor := func(args ...value.Value) value.Value {
		return newDateObject(timeFromArgs(args))
	}

	props := map[string]value.Value{
		"now": value.NewFunc(func(args ...value.Value) value.Value {
			return value.New(float64(time.Now().UnixMilli()))
		}),
		// Date.parse of text no layout reads is NaN, as in JavaScript (it was 0, 1970; 15/10/2026)
		"parse": value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) == 0 {
				return value.New(math.NaN())
			}
			if t, ok := parseDateString(args[0].Text()); ok {
				return value.New(float64(t.UnixMilli()))
			}
			return value.New(math.NaN())
		}),
		"UTC": value.NewFunc(func(args ...value.Value) value.Value {
			if !partsValid(args) {
				return value.New(math.NaN())
			}
			return value.New(float64(timeFromParts(args, time.UTC).UnixMilli()))
		}),
	}

	return value.NewFuncObject(ctor, props)
}
