package value

import (
	"math"
	"testing"
	"time"
)

// Truthy is JavaScript's truthiness (14/10/2026): a negative number is true, and only 0, -0 and NaN are
// false among numbers. A time and a duration, numbers underneath, follow the same rule, so a moment
// before 1970 and a negative duration are true (they were false, as N > 0 decided every scalar). The
// rows the change left alone are the controls: false, null, a failure, "", a positive number, objects.
// The language itself is compared with node in compiler/javascript_parity_test.go.
func TestTruthyIsJavaScripts(t *testing.T) {
	for _, row := range []struct {
		name string
		v    Value
		want bool
	}{
		{"-1", New(-1), true},
		{"-0.5", New(-0.5), true},
		{"negative infinity", New(math.Inf(-1)), true},
		{"0", New(0), false},
		{"-0", New(math.Copysign(0, -1)), false},
		{"NaN", New(math.NaN()), false},
		{"1", New(1), true},
		{"a time before 1970", New(time.Date(1969, 7, 20, 20, 17, 0, 0, time.UTC)), true},
		{"the time 1970-01-01T00:00:00Z", New(time.Unix(0, 0)), false},
		{"a time after 1970", New(time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)), true},
		{"a negative duration", New(-time.Second), true},
		{"a zero duration", New(time.Duration(0)), false},
		{"a positive duration", New(time.Minute), true},
		{"true", ToBool(true), true},
		{"false", ToBool(false), false},
		{"null", Value{K: Nil}, false},
		{"a failure", Value{K: Invalid}, false},
		{"\"\"", NewString(""), false},
		{"\"0\"", NewString("0"), true},
		{"[]", New(&[]Value{}), true},
		{"{}", New(map[string]Value{}), true},
	} {
		if got := row.v.Truthy(); got != row.want {
			t.Errorf("Truthy(%s) = %v, want %v", row.name, got, row.want)
		}
	}
}

// TypeOf is JavaScript's typeof for every kind (14/10/2026): null is "undefined" (Kitwork's one value for
// null and undefined), a time and a duration are "object" as a Date is, and so is every value of the host.
func TestTypeOfNamesEveryKind(t *testing.T) {
	for _, row := range []struct {
		name string
		v    Value
		want string
	}{
		{"number", New(-1.5), "number"},
		{"string", NewString(""), "string"},
		{"boolean", ToBool(false), "boolean"},
		{"null", Value{K: Nil}, "undefined"},
		{"a failure", Value{K: Invalid}, "undefined"},
		{"function", NewFunc(func(...Value) Value { return Value{K: Nil} }), "function"},
		{"safe", NewSafeFunction(), "function"},
		{"array", New(&[]Value{}), "object"},
		{"object", New(map[string]Value{}), "object"},
		{"time", New(time.Unix(0, 0)), "object"},
		{"duration", New(time.Second), "object"},
		{"bytes", New([]byte("a")), "object"},
		{"proxy", Value{K: Proxy}, "object"},
		{"struct", Value{K: Struct}, "object"},
	} {
		if got := row.v.TypeOf(); got != row.want {
			t.Errorf("TypeOf(%s) = %q, want %q", row.name, got, row.want)
		}
	}
}
