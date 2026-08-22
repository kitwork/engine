package analytics

import (
	"fmt"
	"time"
)

// Predicate is one immutable filter clause.
type Predicate interface {
	Column() string
	Matches(value any, ok bool) bool
	MayMatch(stats ColumnStats) bool
	predicateKind() Kind
}

type predicate struct {
	column string
	kind   Kind
	match  func(value any, ok bool) bool
	may    func(stats ColumnStats) bool
}

func (p predicate) Column() string                  { return p.column }
func (p predicate) Matches(value any, ok bool) bool { return p.match(value, ok) }
func (p predicate) MayMatch(stats ColumnStats) bool { return p.may(stats) }
func (p predicate) predicateKind() Kind             { return p.kind }

// Eq creates a typed equality predicate.
func Eq(column string, expected any) (Predicate, error) {
	kind, canonical, err := canonicalValue(expected)
	if err != nil {
		return nil, err
	}
	return predicate{
		column: column,
		kind:   kind,
		match: func(value any, ok bool) bool {
			if !ok {
				return false
			}
			switch kind {
			case KindBool:
				return value.(bool) == canonical.(bool)
			case KindInt64:
				return value.(int64) == canonical.(int64)
			case KindFloat64:
				return value.(float64) == canonical.(float64)
			case KindText:
				return value.(string) == canonical.(string)
			case KindTime:
				return value.(time.Time).Equal(canonical.(time.Time))
			default:
				return false
			}
		},
		may: func(stats ColumnStats) bool {
			if stats.Kind != kind {
				return false
			}
			if !stats.HasValues {
				return false
			}
			switch kind {
			case KindBool:
				switch canonical.(bool) {
				case true:
					return stats.BoolTrueCount > 0
				default:
					return stats.BoolFalseCount > 0
				}
			case KindInt64:
				value := canonical.(int64)
				return value >= stats.IntMin && value <= stats.IntMax
			case KindFloat64:
				value := canonical.(float64)
				return value >= stats.FloatMin && value <= stats.FloatMax
			case KindText:
				value := canonical.(string)
				return value >= stats.TextMin && value <= stats.TextMax
			case KindTime:
				value := canonical.(time.Time)
				return !value.Before(stats.TimeMin) && !value.After(stats.TimeMax)
			default:
				return false
			}
		},
	}, nil
}

// BetweenInt64 keeps values inside [minimum, maximum].
func BetweenInt64(column string, minimum, maximum int64) Predicate {
	if minimum > maximum {
		minimum, maximum = maximum, minimum
	}
	return predicate{
		column: column,
		kind:   KindInt64,
		match: func(value any, ok bool) bool {
			if !ok {
				return false
			}
			typed := value.(int64)
			return typed >= minimum && typed <= maximum
		},
		may: func(stats ColumnStats) bool {
			if stats.Kind != KindInt64 || !stats.HasValues {
				return false
			}
			return maximum >= stats.IntMin && minimum <= stats.IntMax
		},
	}
}

// BetweenFloat64 keeps values inside [minimum, maximum].
func BetweenFloat64(column string, minimum, maximum float64) Predicate {
	if minimum > maximum {
		minimum, maximum = maximum, minimum
	}
	return predicate{
		column: column,
		kind:   KindFloat64,
		match: func(value any, ok bool) bool {
			if !ok {
				return false
			}
			typed := value.(float64)
			return typed >= minimum && typed <= maximum
		},
		may: func(stats ColumnStats) bool {
			if stats.Kind != KindFloat64 || !stats.HasValues {
				return false
			}
			return maximum >= stats.FloatMin && minimum <= stats.FloatMax
		},
	}
}

// BetweenTime keeps values inside [minimum, maximum].
func BetweenTime(column string, minimum, maximum time.Time) Predicate {
	if minimum.After(maximum) {
		minimum, maximum = maximum, minimum
	}
	return predicate{
		column: column,
		kind:   KindTime,
		match: func(value any, ok bool) bool {
			if !ok {
				return false
			}
			typed := value.(time.Time)
			return !typed.Before(minimum) && !typed.After(maximum)
		},
		may: func(stats ColumnStats) bool {
			if stats.Kind != KindTime || !stats.HasValues {
				return false
			}
			return !maximum.Before(stats.TimeMin) && !minimum.After(stats.TimeMax)
		},
	}
}

// Prefix keeps text values that start with prefix.
func Prefix(column, prefix string) Predicate {
	upperBound := prefixUpperBound(prefix)
	return predicate{
		column: column,
		kind:   KindText,
		match: func(value any, ok bool) bool {
			if !ok {
				return false
			}
			return len(prefix) == 0 || len(value.(string)) >= len(prefix) && value.(string)[:len(prefix)] == prefix
		},
		may: func(stats ColumnStats) bool {
			if stats.Kind != KindText || !stats.HasValues {
				return false
			}
			if len(prefix) == 0 {
				return true
			}
			return stats.TextMax >= prefix && stats.TextMin < upperBound
		},
	}
}

func canonicalValue(value any) (Kind, any, error) {
	switch typed := value.(type) {
	case bool:
		return KindBool, typed, nil
	case int:
		return KindInt64, int64(typed), nil
	case int8:
		return KindInt64, int64(typed), nil
	case int16:
		return KindInt64, int64(typed), nil
	case int32:
		return KindInt64, int64(typed), nil
	case int64:
		return KindInt64, typed, nil
	case uint:
		return KindInt64, int64(typed), nil
	case uint8:
		return KindInt64, int64(typed), nil
	case uint16:
		return KindInt64, int64(typed), nil
	case uint32:
		return KindInt64, int64(typed), nil
	case uint64:
		return KindInt64, int64(typed), nil
	case float32:
		return KindFloat64, float64(typed), nil
	case float64:
		return KindFloat64, typed, nil
	case string:
		return KindText, typed, nil
	case time.Time:
		return KindTime, typed.UTC(), nil
	default:
		return KindInvalid, nil, fmt.Errorf("%w: unsupported predicate value type %T", ErrTypeMismatch, value)
	}
}

func prefixUpperBound(prefix string) string {
	if prefix == "" {
		return ""
	}
	bytes := []byte(prefix)
	for position := len(bytes) - 1; position >= 0; position-- {
		if bytes[position] != 0xff {
			bytes[position]++
			return string(bytes[:position+1])
		}
	}
	return prefix + "\x00"
}
