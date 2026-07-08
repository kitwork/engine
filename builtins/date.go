package builtins

import (
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

func Date() value.Value {
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
