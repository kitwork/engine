package value

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Value implements the database/sql/driver.Valuer interface.
// This allowing Value objects to be used directly in SQL queries.
func (v Value) Value() (driver.Value, error) {
	switch v.K {
	case Invalid, Nil:
		return nil, nil
	case Bool:
		return v.N > 0, nil
	case Number:
		return v.N, nil
	case String:
		return v.V.(string), nil
	case Time:
		return time.Unix(0, int64(v.N)), nil
	case Duration: // Duration as int64 nanoseconds in DB? Or string? Usually int64.
		return int64(v.N), nil
	case Bytes:
		return v.Bytes(), nil
	case Array, Map:
		// Complex types are stored as JSON strings in DB
		return json.Marshal(v)
	default:
		return nil, fmt.Errorf("unsupported type for SQL: %s", v.K.String())
	}
}

// Scan implements the database/sql.Scanner interface.
// This allows reading SQL results directly into a Value object.
func (v *Value) Scan(src any) error {
	if src == nil {
		*v = Value{K: Nil}
		return nil
	}

	switch val := src.(type) {
	case int64:
		*v = New(float64(val))
	case float64:
		*v = New(val)
	case bool:
		*v = New(val)
	case []byte:
		// OPTIMIZATION: Handle bytes directly to avoid unnecessary string allocation
		// 1. Check UTF-8 validity directly on bytes
		if utf8.Valid(val) {
			// 2. Check for JSON pattern without converting to string
			// Find start/end non-whitespace bytes
			start, end := 0, len(val)-1
			for start <= end && (val[start] <= ' ') {
				start++
			}
			for end >= start && (val[end] <= ' ') {
				end--
			}

			// 3. Try Unmarshal if pattern matches
			if start < end && ((val[start] == '{' && val[end] == '}') || (val[start] == '[' && val[end] == ']')) {
				var i any
				// Use the slice directly, no allocation needed
				if err := json.Unmarshal(val[start:end+1], &i); err == nil {
					*v = New(i)
					return nil
				}
			}
			// 4. Fallback: It's a text string
			*v = New(string(val))
		} else {
			// Binary data -> Bytes Kind
			*v = New(val)
		}
	case string:
		// Try to parse as JSON if it looks like one
		trimmed := strings.TrimSpace(val)
		if (len(trimmed) > 1) && ((trimmed[0] == '{' && trimmed[len(trimmed)-1] == '}') || (trimmed[0] == '[' && trimmed[len(trimmed)-1] == ']')) {
			var i any
			if err := json.Unmarshal([]byte(val), &i); err == nil {
				*v = New(i)
				return nil
			}
		}
		*v = New(val)
	case time.Time:
		*v = New(val)
	default:
		*v = New(val)
	}
	return nil
}
