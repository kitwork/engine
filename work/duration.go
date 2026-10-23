package work

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseDuration reads a duration an author writes: Go's ("30s", "1h30m", "250ms"), or a whole number of
// days, weeks, months of 30 days or years of 365 ("7d", "2w", "1mo", "1y"). A cache's ttl, a queue's and a
// schedule's keep and timeout, a send's delay all read it.
func ParseDuration(s string) (time.Duration, error) {
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
