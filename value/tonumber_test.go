package value

import (
	"math"
	"testing"
	"time"
)

// StringToNumber reads text as JavaScript's Number(text) does (15/10/2026). The rows are node's answers
// (compiler/javascript_parity_test.go runs the same texts through ++ and isNaN against node itself).
func TestStringToNumberAsInJavaScript(t *testing.T) {
	for _, row := range []struct {
		text string
		want string
	}{
		{"5", "5"}, {"-5", "-5"}, {"+5", "5"}, {"007", "7"}, {"1.5", "1.5"}, {".5", "0.5"}, {"5.", "5"},
		{"-.5", "-0.5"}, {"1e3", "1000"}, {"1E-2", "0.01"}, {"2e+2", "200"}, {"1e400", "Infinity"},
		{"-1e400", "-Infinity"}, {"1e-400", "0"},
		{"", "0"}, {"   ", "0"}, {" 12 ", "12"}, {"\t\n12\r\n", "12"}, {"\u00a012\u3000", "12"}, {"\ufeff7", "7"},
		{"Infinity", "Infinity"}, {"+Infinity", "Infinity"}, {"-Infinity", "-Infinity"},
		{"0x1f", "31"}, {"0X1F", "31"}, {"0o17", "15"}, {"0b101", "5"}, {"0xffffffffffffffffff", "4.722366482869645e+21"},
		{"a", "NaN"}, {"5a", "NaN"}, {"1_000", "NaN"}, {"inf", "NaN"}, {"infinity", "NaN"}, {"NaN", "NaN"},
		{"-0x10", "NaN"}, {"0x", "NaN"}, {"0b2", "NaN"}, {".", "NaN"}, {"e5", "NaN"}, {"1e", "NaN"}, {"1e+", "NaN"},
		{"+-1", "NaN"}, {"1 2", "NaN"}, {"\u00855", "NaN"}, {"0x1p3", "NaN"}, {"1,5", "NaN"},
	} {
		got := New(StringToNumber(row.text)).Text()
		if got != row.want {
			t.Errorf("StringToNumber(%q) = %s, want %s", row.text, got, row.want)
		}
	}
	if n := StringToNumber("-0"); n != 0 || !math.Signbit(n) {
		t.Errorf(`StringToNumber("-0") = %v, want -0`, n)
	}
}

// ToNumber of every kind Kitwork holds. CONTROL: a number is itself.
func TestToNumberOfEveryKind(t *testing.T) {
	at := time.Date(2026, 10, 15, 8, 0, 0, 123_456_789, time.UTC)
	failure := InvalidFailure(TypeCode, "boom")
	for _, row := range []struct {
		name string
		in   Value
		want string
	}{
		{"CONTROL: a number", New(4.5), "4.5"},
		{"null", NewNil(), "0"},
		{"true", ToBool(true), "1"},
		{"false", ToBool(false), "0"},
		{"a string", NewString(" 42 "), "42"},
		{"a string that is no number", NewString("a"), "NaN"},
		{"a time", New(at), "1792051200123"},
		{"a duration", New(1500 * time.Microsecond), "1.5"},
		// 16/10/2026 (the owner's E2): an array is a number through its text, as in JavaScript (it was NaN)
		{"an empty array", New([]Value{}), "0"},
		{"an array of one number", New([]Value{New(5)}), "5"},
		{"an array of one text", New([]Value{NewString(" 7 ")}), "7"},
		{"an array of an array", New([]Value{New([]Value{NewString("8")})}), "8"},
		{"an array of null", New([]Value{NewNil()}), "0"},
		{"an array of two numbers", New([]Value{New(1), New(2)}), "NaN"},
		{"an array of an object", New([]Value{New(map[string]Value{})}), "NaN"},
		{"an array of true", New([]Value{ToBool(true)}), "NaN"},
		{"an object", New(map[string]Value{}), "NaN"},
		{"a function", NewFunc(func(args ...Value) Value { return New(1) }), "NaN"},
		{"bytes", New([]byte("5")), "NaN"},
	} {
		got := ToNumber(row.in)
		if got.K != Number || got.Text() != row.want {
			t.Errorf("%s: ToNumber = %#v (%q), want the number %s", row.name, got, got.Text(), row.want)
		}
	}
	if got := ToNumber(failure); got.K != Invalid {
		t.Errorf("a failure: ToNumber = %#v, want the failure itself", got)
	}
}

// NaN, Infinity and -Infinity are written as JavaScript writes them, in text and in toFixed; Infinity
// equals itself and NaN is ordered with nothing (15/10/2026). CONTROLS: a fraction and a whole number
// keep their text, 1 <= 2 is true.
func TestNonFiniteNumbersAsInJavaScript(t *testing.T) {
	infinity, minus, nan := New(math.Inf(1)), New(math.Inf(-1)), New(math.NaN())
	for _, row := range []struct {
		got, want string
	}{
		{infinity.Text(), "Infinity"},
		{minus.Text(), "-Infinity"},
		{nan.Text(), "NaN"},
		{New([]Value{infinity, nan}).Text(), "[Infinity, NaN]"},
		{infinity.ToFixed(New(2)).Text(), "Infinity"},
		{minus.ToFixed(New(2)).Text(), "-Infinity"},
		{nan.ToFixed().Text(), "NaN"},
		{New(1.25).Text(), "1.25"},
		{New(1.26).ToFixed(New(1)).Text(), "1.3"},
		{New(1e21).Text(), "1e+21"},
	} {
		if row.got != row.want {
			t.Errorf("text %q, want %q", row.got, row.want)
		}
	}
	for _, row := range []struct {
		name string
		got  bool
		want bool
	}{
		{"Infinity === Infinity", infinity.Equal(infinity), true},
		{"-Infinity === -Infinity", minus.Equal(minus), true},
		{"Infinity === -Infinity", infinity.Equal(minus), false},
		{"Infinity === 1e308", infinity.Equal(New(1e308)), false},
		{"NaN === NaN", nan.Equal(nan), false},
		{"NaN < 1", nan.Less(New(1)), false},
		{"NaN <= 1", nan.LessEqual(New(1)), false},
		{"NaN >= 1", nan.GreaterEqual(New(1)), false},
		{"1 <= NaN", New(1).LessEqual(nan), false},
		{"1 >= NaN", New(1).GreaterEqual(nan), false},
		{"NaN <= NaN", nan.LessEqual(nan), false},
		{"null <= NaN", NewNil().LessEqual(nan), false},
		{"Infinity >= Infinity", infinity.GreaterEqual(infinity), true},
		{"CONTROL: 1 <= 2", New(1).LessEqual(New(2)), true},
		{"CONTROL: 2 >= 2", New(2).GreaterEqual(New(2)), true},
	} {
		if row.got != row.want {
			t.Errorf("%s = %v, want %v", row.name, row.got, row.want)
		}
	}
}

// A position, a length or a count of a native reads a number through Int and Int64: JavaScript's
// ToIntegerOrInfinity, clamped (15/10/2026, the review of L2). Go's int() of NaN and the infinities is
// -9223372036854775808, which made "7".padStart(1 / 0) panic. .int() keeps NaN and the infinities.
// CONTROL: whole numbers, fractions and negative numbers as int() gave them.
func TestIntOfNaNAndInfinity(t *testing.T) {
	for _, row := range []struct {
		in        float64
		wantInt   int
		wantInt64 int64
	}{
		{math.NaN(), 0, 0},
		{math.Inf(1), math.MaxInt, math.MaxInt64},
		{math.Inf(-1), math.MinInt, math.MinInt64},
		{1e300, math.MaxInt, math.MaxInt64},
		{-1e300, math.MinInt, math.MinInt64},
		{9223372036854775807, math.MaxInt, math.MaxInt64},
		{12.9, 12, 12},
		{-12.9, -12, -12},
		{0, 0, 0},
	} {
		if got := New(row.in).Int(); got != row.wantInt {
			t.Errorf("Int of %v = %d, want %d", row.in, got, row.wantInt)
		}
		if got := New(row.in).Int64(); got != row.wantInt64 {
			t.Errorf("Int64 of %v = %d, want %d", row.in, got, row.wantInt64)
		}
	}
	for _, row := range []struct{ in, want float64 }{{math.Inf(1), math.Inf(1)}, {math.Inf(-1), math.Inf(-1)}, {1e300, 1e300}, {-2.5, -2}, {7.9, 7}} {
		if got := New(row.in).Integer(); got.N != row.want {
			t.Errorf("(%v).int() = %v, want %v", row.in, got.N, row.want)
		}
	}
	if got := New(math.NaN()).Integer(); got.N == got.N {
		t.Errorf("(NaN).int() = %v, want NaN", got.N)
	}
}

// A number's pattern (n.toString("#,##0.00"), "%d") writes NaN, Infinity and -Infinity as words, as
// String(n) does (15/10/2026: Go wrote "+,Inf." and "%!d(float64=+Inf)"). CONTROL: a finite number keeps
// its pattern.
func TestAPatternOfANonFiniteNumberIsItsWord(t *testing.T) {
	for _, row := range []struct {
		n       float64
		pattern string
		want    string
	}{
		{math.Inf(1), "#,##0.00", "Infinity"},
		{math.Inf(-1), "%d", "-Infinity"},
		{math.NaN(), "#,##0.00", "NaN"},
		{1234.5, "#,##0.00", "1,234.50"},
	} {
		if got := New(row.n).ToString(NewString(row.pattern)).Text(); got != row.want {
			t.Errorf("(%v).toString(%q) = %q, want %q", row.n, row.pattern, got, row.want)
		}
	}
}

// A date made by new Date() is known by its own valueOf (NewDateValueOf); the valueOf copied into another
// object makes it no date (15/10/2026). CONTROL: the date itself.
func TestADateIsKnownByItsOwnValueOf(t *testing.T) {
	date := map[string]Value{}
	date["valueOf"] = NewDateValueOf(date, 1500)
	if got := ToNumber(New(date)); got.N != 1500 {
		t.Errorf("CONTROL: ToNumber of the date = %v, want 1500", got.N)
	}
	copied := map[string]Value{"valueOf": date["valueOf"]}
	if got := ToNumber(New(copied)); got.N == got.N {
		t.Errorf("ToNumber of an object holding the date's valueOf = %v, want NaN", got.N)
	}
}

// chunk(Infinity) is one chunk of every item: the largest int Int gives it would overflow i + size
// (15/10/2026). CONTROL: chunk(2).
func TestChunkOfInfinity(t *testing.T) {
	items := func() Value { return New([]Value{New(1), New(2), New(3)}) }
	if got := items().Chunk(New(math.Inf(1))).Text(); got != "[[1, 2, 3]]" {
		t.Errorf("chunk(Infinity) = %s, want [[1, 2, 3]]", got)
	}
	if got := items().Chunk(New(2)).Text(); got != "[[1, 2], [3]]" {
		t.Errorf("CONTROL: chunk(2) = %s, want [[1, 2], [3]]", got)
	}
}
