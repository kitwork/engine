package work

// Cache/persist expiry: a TTL can be a rolling DURATION ("5m", "1h") or pinned to a wall-clock
// BOUNDARY ("nextday 03:00", "daily 03:00", "weekly", "monthly", "hourly", or a bare "03:00").
// Boundary expiry aligns the cache to a data-refresh schedule so every visitor after the boundary
// gets fresh output — unlike a rolling TTL, which drifts from the first request.
//
// parseExpiry returns a resolver evaluated at SAVE time: given `now`, it yields the duration until
// the entry should expire (0 = never). A nil resolver means "not cacheable".

import (
	"strconv"
	"strings"
	"time"

	"github.com/kitwork/engine/value"
)

// constExpiry is the resolver for a fixed duration (d = 0 → never).
func constExpiry(d time.Duration) func(time.Time) time.Duration {
	return func(time.Time) time.Duration { return d }
}

// expiryOf resolves a .cache()/.persist() argument list: no arg → forever; otherwise parse the
// spec (nil if unparseable → the directive stores no resolver, i.e. no caching — fail-safe).
func expiryOf(args ...value.Value) func(time.Time) time.Duration {
	if len(args) == 0 {
		return constExpiry(0) // forever
	}
	return parseExpiry(args[0])
}

// parseExpiry turns a .cache()/.persist() argument into a resolver, or nil if unparseable.
func parseExpiry(v value.Value) func(time.Time) time.Duration {
	if v.IsNumeric() {
		return constExpiry(time.Duration(v.N) * time.Millisecond)
	}
	s := strings.TrimSpace(strings.ToLower(v.Text()))
	if s == "" {
		return nil
	}
	if d, err := ParseDuration(s); err == nil && d > 0 { // rolling duration: "5m", "1h30m"
		return constExpiry(d)
	}
	return calendarExpiry(s) // boundary: "nextday 03:00", "weekly", "03:00", …
}

// parseHM reads "HH:MM" (or "HH") → hour, minute.
func parseHM(f string) (h, m int, ok bool) {
	parts := strings.SplitN(f, ":", 2)
	hh, err := strconv.Atoi(parts[0])
	if err != nil || hh < 0 || hh > 23 {
		return 0, 0, false
	}
	if len(parts) == 2 {
		mm, err := strconv.Atoi(parts[1])
		if err != nil || mm < 0 || mm > 59 {
			return 0, 0, false
		}
		return hh, mm, true
	}
	return hh, 0, true
}

var weekdays = map[string]time.Weekday{
	"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
}

// calendarExpiry parses a boundary spec into a resolver, or nil if unrecognised.
func calendarExpiry(s string) func(time.Time) time.Duration {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return nil
	}
	kind := fields[0]

	// Pull an HH:MM and an optional weekday out of the remaining fields.
	hh, mm := 0, 0
	wd, haveWd := time.Monday, false
	for _, f := range fields[1:] {
		if h, m, ok := parseHM(f); ok {
			hh, mm = h, m
		} else if d, ok := weekdays[f]; ok {
			wd, haveWd = d, true
		}
	}

	atToday := func(now time.Time) time.Time {
		return time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, now.Location())
	}

	switch kind {
	case "hourly":
		return func(now time.Time) time.Duration {
			return now.Truncate(time.Hour).Add(time.Hour).Sub(now)
		}
	case "daily", "midnight", "today":
		return func(now time.Time) time.Duration {
			next := atToday(now)
			if !next.After(now) {
				next = next.AddDate(0, 0, 1)
			}
			return next.Sub(now)
		}
	case "nextday", "tomorrow":
		return func(now time.Time) time.Duration {
			return atToday(now).AddDate(0, 0, 1).Sub(now)
		}
	case "weekly":
		target := time.Monday
		if haveWd {
			target = wd
		}
		return func(now time.Time) time.Duration {
			ahead := (int(target) - int(now.Weekday()) + 7) % 7
			next := atToday(now).AddDate(0, 0, ahead)
			if !next.After(now) {
				next = next.AddDate(0, 0, 7)
			}
			return next.Sub(now)
		}
	case "monthly":
		return func(now time.Time) time.Duration {
			return time.Date(now.Year(), now.Month()+1, 1, hh, mm, 0, 0, now.Location()).Sub(now)
		}
	default:
		if h, m, ok := parseHM(kind); ok { // bare "03:00" → next occurrence today/tomorrow
			hh, mm = h, m
			return func(now time.Time) time.Duration {
				next := atToday(now)
				if !next.After(now) {
					next = next.AddDate(0, 0, 1)
				}
				return next.Sub(now)
			}
		}
	}
	return nil
}
