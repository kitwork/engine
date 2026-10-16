package hydrate

import (
	"math"
	"strconv"
	"strings"
)

// Date in an expression, which has no `new`: Date.UTC and Date.parse of a time the text fixes answer
// as V8 does; the time now (Date.now(), Date()) and a time the visitor's zone decides (Date.parse of a
// date and time with no zone, or of a form JavaScript leaves to each engine) belong to the browser,
// and the server leaves them undefined.

const msPerDay = 86400000

func (m *machine) dateStatic(f *frame, name string, args []any) (any, error) {
	switch name {
	case "now":
		return Undefined, nil
	case "UTC":
		fields := []float64{math.NaN(), 0, 1, 0, 0, 0, 0}
		for i := 0; i < len(args) && i < 7; i++ {
			n, err := m.toNumber(f, args[i])
			if err != nil {
				return nil, err
			}
			fields[i] = n
		}
		year := fields[0]
		if year == year {
			if y := integerPart(year); y >= 0 && y <= 99 {
				year = 1900 + y
			}
		}
		day := makeDay(year, fields[1], fields[2])
		time := makeTime(fields[3], fields[4], fields[5], fields[6])
		return timeClip(makeDate(day, time)), nil
	case "parse":
		text, err := m.toString(f, arg(args, 0))
		if err != nil {
			return nil, err
		}
		return parseDate(text), nil
	}
	return nil, typeError("Date." + name + " is not a function")
}

// integerPart is V8's DoubleToInteger: n without its fraction, NaN as 0.
func integerPart(n float64) float64 {
	if n != n {
		return 0
	}
	return math.Trunc(n) + 0
}

// makeDay is V8's MakeDay: the day number of year, month (any integer, carried into the year) and
// date, NaN past a million years.
func makeDay(year, month, date float64) float64 {
	if !(year >= -1000000 && year <= 1000000) || !(month >= -10000000 && month <= 10000000) || math.IsInf(date, 0) || date != date {
		return math.NaN()
	}
	y := int64(year)
	mo := int64(month)
	y += mo / 12
	mo %= 12
	if mo < 0 {
		mo += 12
		y--
	}
	const yearDelta = 399999
	base := int64(365*(1970+yearDelta) + (1970+yearDelta)/4 - (1970+yearDelta)/100 + (1970+yearDelta)/400)
	days := 365*(y+yearDelta) + (y+yearDelta)/4 - (y+yearDelta)/100 + (y+yearDelta)/400 - base
	common := [12]int64{0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334}
	leap := [12]int64{0, 31, 60, 91, 121, 152, 182, 213, 244, 274, 305, 335}
	if y%4 != 0 || (y%100 == 0 && y%400 != 0) {
		days += common[mo]
	} else {
		days += leap[mo]
	}
	return float64(days-1) + integerPart(date)
}

// makeTime is V8's MakeTime: hours, minutes, seconds and milliseconds as one count of milliseconds.
func makeTime(h, m, s, ms float64) float64 {
	for _, v := range []float64{h, m, s, ms} {
		if v != v || math.IsInf(v, 0) {
			return math.NaN()
		}
	}
	return integerPart(h)*3600000 + integerPart(m)*60000 + integerPart(s)*1000 + integerPart(ms)
}

func makeDate(day, time float64) float64 {
	if day != day || time != time || math.IsInf(day, 0) || math.IsInf(time, 0) {
		return math.NaN()
	}
	date := day*msPerDay + time
	if math.IsInf(date, 0) {
		return math.NaN()
	}
	return date
}

// timeClip is TimeClip: a time past 8.64e15 milliseconds either way is NaN.
func timeClip(t float64) float64 {
	if !(math.Abs(t) <= 8.64e15) {
		return math.NaN()
	}
	return integerPart(t)
}

// parseDate is Date.parse of text, as V8 reads it: the date-time format of ECMAScript — a date alone
// (UTC), or a date and a time with Z or an offset — as a number, NaN when a field of that format is out
// of range (2024-13-01, T25:00Z), text with no digit NaN; anything else, which V8 reads in the visitor's
// zone (a date and time with no zone, " 2024-01-02", "Jan 2 2024") or by rules JavaScript leaves to
// each engine, undefined for the browser to answer.
func parseDate(text string) any {
	if t, decided := isoDate(text); decided {
		return t
	}
	if !strings.ContainsAny(text, "0123456789") {
		return math.NaN()
	}
	return Undefined
}

// isoDate reads YYYY[-MM[-DD]] or ±YYYYYY[…], then Z, or T or t with HH:mm[:ss[.s…]] and Z, ±HH:mm or
// ±HHMM. decided is false for what V8 hands to its other reader instead.
func isoDate(s string) (float64, bool) {
	nan := math.NaN()
	at := 0
	digits := func(n int) (int, bool) {
		if at+n > len(s) {
			return 0, false
		}
		v := 0
		for i := 0; i < n; i++ {
			c := s[at+i]
			if c < '0' || c > '9' {
				return 0, false
			}
			v = v*10 + int(c-'0')
		}
		at += n
		return v, true
	}
	isDigitAt := func(i int) bool { return i < len(s) && s[i] >= '0' && s[i] <= '9' }
	var year int
	if at < len(s) && (s[at] == '+' || s[at] == '-') {
		sign := s[at]
		at++
		y, ok := digits(6)
		if !ok || sign == '-' && y == 0 {
			return 0, false
		}
		year = y
		if sign == '-' {
			year = -y
		}
	} else {
		y, ok := digits(4)
		if !ok {
			return 0, false
		}
		year = y
	}
	month, day := 1, 1
	if at < len(s) && s[at] == '-' {
		at++
		v, ok := digits(2)
		if !ok || isDigitAt(at) {
			return 0, false
		}
		month = v
		if at < len(s) && s[at] == '-' {
			at++
			v, ok := digits(2)
			if !ok || isDigitAt(at) {
				return 0, false
			}
			day = v
		}
	}
	if month < 1 || month > 12 || day < 1 || day > 31 {
		return nan, true
	}
	date := makeDay(float64(year), float64(month-1), float64(day))
	switch {
	case at == len(s):
		return timeClip(makeDate(date, 0)), true
	case (s[at] == 'Z' || s[at] == 'z') && at+1 == len(s):
		return timeClip(makeDate(date, 0)), true
	case s[at] == 'T' || s[at] == 't':
		at++
	case s[at] >= '0' && s[at] <= '9' || s[at] == ' ':
		return 0, false
	default:
		return nan, true
	}
	hour, ok := digits(2)
	if !ok || at >= len(s) || s[at] != ':' {
		return nan, true
	}
	at++
	minute, ok := digits(2)
	if !ok {
		return nan, true
	}
	second, milli := 0, 0
	if at < len(s) && s[at] == ':' {
		at++
		if second, ok = digits(2); !ok {
			return nan, true
		}
		if at < len(s) && s[at] == '.' {
			at++
			start := at
			for isDigitAt(at) {
				at++
			}
			if at == start {
				return nan, true
			}
			fraction := s[start:at]
			for len(fraction) < 3 {
				fraction += "0"
			}
			milli, _ = strconv.Atoi(fraction[:3])
		}
	}
	if hour > 24 || minute > 59 || second > 59 || hour == 24 && (minute != 0 || second != 0 || milli != 0) {
		return nan, true
	}
	zone := 0
	switch {
	case at == len(s):
		return 0, false // a date and a time with no zone: the visitor's local time
	case s[at] == 'Z' || s[at] == 'z':
		at++
	case s[at] == '+' || s[at] == '-':
		sign := 1
		if s[at] == '-' {
			sign = -1
		}
		at++
		zh, ok := digits(2)
		if !ok {
			return nan, true
		}
		if at < len(s) && s[at] == ':' {
			at++
		}
		zm, ok := digits(2)
		if !ok || zh > 23 || zm > 59 {
			return nan, true
		}
		zone = sign * (zh*60 + zm)
	default:
		return nan, true
	}
	if at != len(s) {
		return nan, true
	}
	t := makeDate(date, makeTime(float64(hour), float64(minute), float64(second), float64(milli)))
	return timeClip(t - float64(zone)*60000), true
}
