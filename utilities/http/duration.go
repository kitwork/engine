package http

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// parseDuration reads a duration as the router and the queue write one: "500ms", "10s", "2m", "1h",
// and "7d", "2w", "1mo", "1y".
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}

	d, err := time.ParseDuration(s)
	if err == nil {
		return d, nil
	}

	numStr := ""
	unit := ""
	for i, r := range s {
		if r >= '0' && r <= '9' {
			numStr += string(r)
		} else {
			unit = s[i:]
			break
		}
	}

	val, err := strconv.Atoi(numStr)
	if err != nil || val <= 0 {
		return 0, fmt.Errorf("invalid duration format: %s", s)
	}

	switch unit {
	case "d":
		return time.Duration(val) * 24 * time.Hour, nil
	case "w":
		return time.Duration(val) * 7 * 24 * time.Hour, nil
	case "mo":
		return time.Duration(val) * 30 * 24 * time.Hour, nil
	case "y":
		return time.Duration(val) * 365 * 24 * time.Hour, nil
	}

	return 0, fmt.Errorf("unknown duration unit: %s", unit)
}

// parseByteSize reads a size as the router writes one (work.ParseByteSize, which this package cannot
// import): a number of bytes, or a number with a unit, "512kb", "50mb", "1gb" (1024-based).
func parseByteSize(text string) (int64, bool) {
	s := strings.ToLower(strings.TrimSpace(text))
	units := []struct {
		suffix string
		size   int64
	}{{"gb", 1 << 30}, {"mb", 1 << 20}, {"kb", 1 << 10}, {"b", 1}}
	for _, unit := range units {
		if strings.HasSuffix(s, unit.suffix) {
			number, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(s, unit.suffix)), 64)
			if err != nil || number <= 0 {
				return 0, false
			}
			return int64(number * float64(unit.size)), true
		}
	}
	number, err := strconv.ParseInt(s, 10, 64)
	if err != nil || number <= 0 {
		return 0, false
	}
	return number, true
}

// sizeText writes a size the way a program writes one: "1kb", "10mb", or bytes.
func sizeText(n int64) string {
	switch {
	case n >= 1<<20 && n%(1<<20) == 0:
		return strconv.FormatInt(n>>20, 10) + "mb"
	case n >= 1<<10 && n%(1<<10) == 0:
		return strconv.FormatInt(n>>10, 10) + "kb"
	}
	return strconv.FormatInt(n, 10) + " bytes"
}
