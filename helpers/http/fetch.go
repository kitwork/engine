package http

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kitwork/engine/value"
)

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

// Fetch is the tenant-agnostic builtin form — no cache tiers wired. Hosts that can scope stores
// per tenant bind FetchWith instead.
func Fetch(args ...value.Value) value.Value {
	return FetchWith(&HTTP{}, args...)
}

// FetchWith runs fetch(url[, options]) on a prepared client (NewClient), so { cache, persist }
// options land in the injected per-tenant tiers — the options-map spelling of the builder chain.
func FetchWith(h *HTTP, args ...value.Value) value.Value {
	if len(args) == 0 {
		return value.New(Response{Status: 0, Error: "fetch: url is required"})
	}
	urlStr := args[0].Text()

	method := "GET"
	var body value.Value

	if len(args) > 1 && args[1].IsMap() {
		opts := args[1].Map()
		if m, ok := opts["method"]; ok {
			method = strings.ToUpper(m.String())
		}
		if b, ok := opts["body"]; ok {
			body = b
		}
		if t, ok := opts["timeout"]; ok {
			if t.IsNumeric() {
				h.timeout = time.Duration(t.N) * time.Millisecond
			} else {
				if d, err := parseDuration(t.String()); err == nil {
					h.timeout = d
				}
			}
		}
		if hdrs, ok := opts["headers"]; ok && hdrs.IsMap() {
			h.headers = make(map[string]string)
			for k, v := range hdrs.Map() {
				h.headers[k] = v.String()
			}
		}
		if c, ok := opts["cache"]; ok {
			h.Cache(c)
		}
		if p, ok := opts["persist"]; ok {
			h.Persist(p)
		}
	}

	return h.do(method, urlStr, body)
}
