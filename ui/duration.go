package ui

import (
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A duration (19/10/2026) is one number and one unit written together: 800ms, 3s, 1.5m, 2h. Every
// component that takes a time when the page compiles reads it here — the terminal's typing, the
// carousel's duration, the network's delay and duration, the toast's duration option — and keeps its
// own range and its own words: readDuration says which way a value is no duration, the component says it
// to the author.

// durationUnits are the units a duration is written in, each with its milliseconds.
var durationUnits = map[string]float64{"ms": 1, "s": 1000, "m": 60 * 1000, "h": 60 * 60 * 1000}

// durationForeignUnits are the units written elsewhere (Vietnamese with or without its marks, English),
// each with the one .ui writes; the keys are lower case, so PHÚT and Min are told too, as is a unit of
// .ui's in capitals (5S).
var durationForeignUnits = map[string]string{
	"msec": "ms", "msecs": "ms", "millisecond": "ms", "milliseconds": "ms", "mili": "ms",
	"giây": "s", "giay": "s", "sec": "s", "secs": "s", "second": "s", "seconds": "s",
	"p": "m", "ph": "m", "phút": "m", "phut": "m", "min": "m", "mins": "m", "minute": "m", "minutes": "m",
	"g": "h", "giờ": "h", "gio": "h", "tiếng": "h", "tieng": "h", "hr": "h", "hrs": "h", "hour": "h", "hours": "h",
}

// durationISO is the ISO 8601 form of a time (PT5M, PT1M30S, P1D), which .ui does not write: it is told
// its plain form (5m, 90s, 24h).
var durationISO = regexp.MustCompile(`(?i)^P(?:([0-9]+(?:\.[0-9]+)?)D)?(?:T(?:([0-9]+(?:\.[0-9]+)?)H)?(?:([0-9]+(?:\.[0-9]+)?)M)?(?:([0-9]+(?:\.[0-9]+)?)S)?)?$`)

// durationProblem is what makes a value no duration; durationRead when it is one.
type durationProblem int

const (
	durationRead     durationProblem = iota // one number and one unit: 800ms, 3s, 1.5m, 2h
	durationNegative                        // a minus before what would be a time: -3s, -3, -1m30s
	durationZero                            // zero, with its unit or without: 0, 0.0, 0s, 0ms
	durationNoUnit                          // a number without its unit: 300
	durationTwoUnits                        // more than one number and unit: 1m30s, 1h 30m
	durationForeign                         // a unit or a form not .ui's, whose time is plain: 5p, 5phút, 5min, 5S, 5 s, PT5M
	durationNotOne                          // no time at all: fast, .5s, 5d, 1m30
)

// durationScale is how a number written without its unit is read when the engine says what to write:
// durationMilliseconds for a time that is mostly short, where a number that large is milliseconds
// (300 → 300ms, 3 → 3s); durationSeconds for a time counted in seconds (300 → 300s).
type durationScale int

const (
	durationMilliseconds durationScale = iota
	durationSeconds
)

// duration is a value read by readDuration.
type duration struct {
	problem durationProblem
	// ms is the time in milliseconds: what it reads as, or what a foreign spelling or two units mean;
	// a negative's is its time without the minus. Digits too many to hold read as +Inf, longer than any
	// range. 0 for a number written without its unit.
	ms float64
	// number and unit are the time to write: as written, in .ui's unit for a foreign one, in the smallest
	// of two units (1m30s: 90 and s); unit is "" for a number written without one
	number, unit string
	foreign      string // the unit written that is not .ui's (phút, min, S); "" for none
	iso          bool   // written in the ISO 8601 form (PT5M)
	units        int    // how many numbers and units are written
}

// durationPart is one number and the unit after it, as written ("" for none).
type durationPart struct{ number, unit string }

// milliseconds is the part's time, in a unit of .ui's: the digits read as the decimal they are written
// in, seconds a thousand times that in one rounding ("4.03s" is 4030, where 4.03 * 1000 is
// 4030.0000000000005). Digits too many to hold read as +Inf (strconv's ErrRange).
func (p durationPart) milliseconds() float64 {
	if p.unit == "ms" {
		ms, _ := strconv.ParseFloat(p.number, 64)
		return ms
	}
	ms, err := strconv.ParseFloat(p.number+"e3", 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		// no digits but a number already too large to hold: the hours of an ISO 8601 day count (P…D)
		// are "+Inf"
		ms, _ = strconv.ParseFloat(p.number, 64)
	}
	return ms * (durationUnits[p.unit] / 1000)
}

// readDuration reads a value written as a duration, trimmed: one number and one unit among ms, s, m and
// h, in milliseconds. A value that is not one says which way it is not (problem), with what to write
// (suggestion) and, for two units or a foreign spelling, why (reason); the component words the message.
// The countdown will read its time here too, with durationSeconds (a number written without its unit is
// seconds there: 300 → 300s) and whole seconds only, which it checks on ms itself.
func readDuration(value string) duration {
	if rest, ok := strings.CutPrefix(value, "-"); ok {
		// a time after the minus is a negative one; anything else after it is no time at all
		d := readDuration(rest)
		if d.problem != durationNotOne {
			d.problem = durationNegative
		}
		return d
	}
	if m := durationISO.FindStringSubmatch(value); m != nil {
		var parts []durationPart
		if m[1] != "" {
			days, _ := strconv.ParseFloat(m[1], 64) // .ui has no day: 24 hours each
			parts = append(parts, durationPart{number: strconv.FormatFloat(days*24, 'f', -1, 64), unit: "h"})
		}
		for i, unit := range []string{"h", "m", "s"} {
			if m[i+2] != "" {
				parts = append(parts, durationPart{number: m[i+2], unit: unit})
			}
		}
		if len(parts) == 0 {
			return duration{problem: durationNotOne} // P or PT alone
		}
		d := plainDuration(parts)
		d.problem, d.iso = durationForeign, true
		return d
	}
	parts, spaced, ok := durationParts(value)
	if !ok {
		return duration{problem: durationNotOne}
	}
	foreign := ""
	for i, p := range parts {
		lower := strings.ToLower(p.unit)
		switch {
		case p.unit == "":
			if len(parts) > 1 {
				return duration{problem: durationNotOne} // a number without its unit among others: 1m30
			}
		case durationUnits[p.unit] > 0:
		case durationUnits[lower] > 0, durationForeignUnits[lower] != "":
			if foreign == "" {
				foreign = p.unit
			}
			parts[i].unit = lower
			if durationUnits[lower] == 0 {
				parts[i].unit = durationForeignUnits[lower]
			}
		default:
			return duration{problem: durationNotOne} // a unit of no time .ui knows: 5d, 5px
		}
	}
	if len(parts) > 1 {
		d := plainDuration(parts)
		if math.IsInf(d.ms, 0) {
			return duration{problem: durationNotOne} // digits too many to say again as one number
		}
		d.problem = durationTwoUnits
		return d
	}
	p := parts[0]
	if p.unit == "" {
		if number, _ := strconv.ParseFloat(p.number, 64); number == 0 {
			return duration{problem: durationZero, number: p.number, units: 1}
		}
		return duration{problem: durationNoUnit, number: p.number, units: 1}
	}
	d := plainDuration(parts)
	switch {
	case foreign != "" || spaced:
		d.problem, d.foreign = durationForeign, foreign
	case d.ms == 0:
		d.problem = durationZero
	}
	return d
}

// durationParts splits a value into its numbers and the unit after each, as written: "1m 30s" is two,
// "5 phút" one with a space before its unit (spaced). ok is false for anything else: a word, a number
// that does not start with a digit (.5s), a sign or a comma between them.
func durationParts(value string) (parts []durationPart, spaced, ok bool) {
	digits := func(i int) int {
		for i < len(value) && '0' <= value[i] && value[i] <= '9' {
			i++
		}
		return i
	}
	spaces := func(i int) int {
		for i < len(value) && (value[i] == ' ' || value[i] == '\t') {
			i++
		}
		return i
	}
	for i := 0; i < len(value); i = spaces(i) {
		start := i
		if i = digits(i); i == start {
			return nil, false, false
		}
		if i < len(value) && value[i] == '.' {
			fraction := i + 1
			if i = digits(fraction); i == fraction {
				return nil, false, false
			}
		}
		number, unit := value[start:i], spaces(i)
		end := unit
		for end < len(value) {
			r, size := utf8.DecodeRuneInString(value[end:])
			if !unicode.IsLetter(r) {
				break
			}
			end += size
		}
		if end > unit && unit > i {
			spaced = true
		}
		parts = append(parts, durationPart{number: number, unit: value[unit:end]})
		i = end
	}
	return parts, spaced, len(parts) > 0
}

// plainDuration is the one number and unit the parts mean together, in the smallest of their units:
// 1m30s is 90s, 1h 30m is 90m; one part is itself.
func plainDuration(parts []durationPart) duration {
	d := duration{units: len(parts), unit: parts[0].unit, number: parts[0].number}
	for _, p := range parts {
		d.ms += p.milliseconds()
		if durationUnits[p.unit] < durationUnits[d.unit] {
			d.unit = p.unit
		}
	}
	if len(parts) > 1 {
		d.number = strconv.FormatFloat(d.ms/durationUnits[d.unit], 'f', -1, 64)
	}
	return d
}

// suggestion is the duration to write instead: its number and unit (a negative's without the minus, a
// foreign spelling's in .ui's unit, two units' as one), and for a number written without its unit, the
// unit the scale reads it in.
func (d duration) suggestion(scale durationScale) string {
	if d.unit != "" {
		return d.number + d.unit
	}
	if number, err := strconv.ParseFloat(d.number, 64); err == nil && number >= 100 && scale == durationMilliseconds {
		return d.number + "ms"
	}
	return d.number + "s"
}

// reason says what makes two units or a foreign spelling no duration of .ui's, for the component to go
// on with what to write (suggestion): `duration="5phút" có đơn vị phút, .ui viết là m: viết duration="5m"`.
func (d duration) reason() string {
	switch {
	case d.iso:
		return "là cách viết ISO 8601, .ui viết một số và một đơn vị (ms, s, m hay h)"
	case d.units > 1:
		count := strconv.Itoa(d.units)
		if d.units == 2 {
			count = "hai"
		}
		return "có " + count + " đơn vị, .ui viết một số và một đơn vị (ms, s, m hay h)"
	case d.foreign != "":
		return "có đơn vị " + d.foreign + ", .ui viết là " + d.unit
	}
	return "có khoảng trắng trước đơn vị, .ui viết số liền đơn vị"
}
