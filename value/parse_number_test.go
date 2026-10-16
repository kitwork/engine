package value

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ParseFloatText, ParseIntText and ToInt32 read as JavaScript's parseFloat, parseInt and x | 0 (16/10/2026,
// the owner's E2). Each row holds node's answer; when node is on PATH it reads the same text and must give
// the same, so no row can agree with an answer JavaScript does not give. Until then parseFloat read the
// whole text and gave 0 for no number ("3.5px" was 0), and parseInt read 0x only with the radix 16, made a
// radix outside 2 to 36 ten and gave 0 for no digit.

var parseFloatRows = []struct{ text, want string }{
	{"3.5px", "3.5"}, {".5", "0.5"}, {"abc", "NaN"}, {"", "NaN"}, {"  -2.5e2kg", "-250"}, {"1e", "1"},
	{"1e+", "1"}, {"1E-2x", "0.01"}, {"1.2.3", "1.2"}, {"+.5", "0.5"}, {"-.5e1x", "-5"}, {"Infinity1", "Infinity"},
	{"-Infinityx", "-Infinity"}, {"+Infinity", "Infinity"}, {"Infinit", "NaN"}, {"infinity", "NaN"}, {"NaN", "NaN"},
	{"0x10", "0"}, {"1_000", "1"}, {"inf", "NaN"}, {"1e400", "Infinity"}, {"-1e400", "-Infinity"},
	{"1e-400", "0"}, {"-0", "-0"}, {"5.", "5"}, {".e1", "NaN"}, {".", "NaN"}, {"-", "NaN"}, {"+-1", "NaN"},
	{"\xc2\xa0 7", "7"}, {"\xef\xbb\xbf7", "7"}, {"\t\n12\r\n", "12"}, {"\xc2\x855", "NaN"}, {"007.50", "7.5"},
	{"1e5e3", "100000"}, {"123456789012345678901234567890", "1.2345678901234568e+29"}, {"9007199254740993", "9007199254740992"},
}

var parseIntRows = []struct {
	text  string
	radix float64 // 0 is no radix
	want  string
}{
	{"42", 0, "42"}, {"  -12", 0, "-12"}, {"+7", 0, "7"}, {"12px", 0, "12"}, {"0x1f", 0, "31"}, {"0X1F", 0, "31"},
	{"-0x1f", 0, "-31"}, {"+0x10", 0, "16"}, {"0x", 0, "NaN"}, {"0xg", 0, "NaN"}, {"abc", 0, "NaN"}, {"", 0, "NaN"},
	{"1e3", 0, "1"}, {"99999999999999999999", 0, "100000000000000000000"},
	{"123456789012345678901234567890", 0, "1.2345678901234568e+29"}, {"-0", 0, "-0"}, {"  0012", 0, "12"},
	{"08", 0, "8"}, {"0b11", 0, "0"}, {"1.9", 0, "1"}, {".5", 0, "NaN"}, {"\xc2\xa0\xef\xbb\xbf 9", 0, "9"},
	{"-", 0, "NaN"}, {"- 1", 0, "NaN"}, {"\xc2\x859", 0, "NaN"},
	{"0x1f", 16, "31"}, {"0x1f", 10, "0"}, {"ff", 16, "255"}, {"FF", 16, "255"}, {"-ff", 16, "-255"}, {"11", 2, "3"},
	{"7", 37, "NaN"}, {"7", 1, "NaN"}, {"7", -1, "NaN"}, {"z", 36, "35"}, {"Z", 36, "35"}, {"2", 2, "NaN"},
	{"12", 2, "1"}, {"10", 16.9, "16"}, {"10", math.NaN(), "10"}, {"10", math.Inf(1), "10"},
	{"10", 4294967312, "16"}, {"10", -4294967280, "16"}, {"0x10", 8, "0"}, {"777", 8, "511"},
	{"111111111111111111111111111111", 2, "1073741823"}, {strings.Repeat("f", 20), 16, "1.2089258196146292e+24"},
	{"zz", 37, "NaN"}, {"1" + strings.Repeat("0", 400), 0, "Infinity"},
}

var toInt32Rows = []struct {
	n    float64
	want int32
}{
	{16.9, 16}, {-16.9, -16}, {4294967312, 16}, {-4294967280, 16}, {2147483648, -2147483648}, {-2147483649, 2147483647},
	{math.NaN(), 0}, {math.Inf(1), 0}, {math.Inf(-1), 0}, {-1, -1}, {1e21, -559939584}, {0, 0},
}

// numberWord is n as String(n) writes it, with -0 as "-0".
func numberWord(n float64) string {
	if n == 0 && math.Signbit(n) {
		return "-0"
	}
	return NumberText(n)
}

func radixWord(radix float64) string {
	if radix == 0 {
		return "undefined"
	}
	return NumberText(radix)
}

func TestParseFloatTextAsInJavaScript(t *testing.T) {
	for _, row := range parseFloatRows {
		if got := numberWord(ParseFloatText(row.text)); got != row.want {
			t.Errorf("parseFloat(%q) = %s, want %s", row.text, got, row.want)
		}
	}
	var sources []string
	for _, row := range parseFloatRows {
		sources = append(sources, "parseFloat("+quoteForNode(row.text)+")")
	}
	checkWithNode(t, sources, func(i int) string { return parseFloatRows[i].want })
}

func TestParseIntTextAsInJavaScript(t *testing.T) {
	for _, row := range parseIntRows {
		if got := numberWord(ParseIntText(row.text, ToInt32(row.radix))); got != row.want {
			t.Errorf("parseInt(%q, %s) = %s, want %s", row.text, radixWord(row.radix), got, row.want)
		}
	}
	var sources []string
	for _, row := range parseIntRows {
		sources = append(sources, "parseInt("+quoteForNode(row.text)+", "+radixWord(row.radix)+")")
	}
	checkWithNode(t, sources, func(i int) string { return parseIntRows[i].want })
}

func TestToInt32AsInJavaScript(t *testing.T) {
	for _, row := range toInt32Rows {
		if got := ToInt32(row.n); got != row.want {
			t.Errorf("ToInt32(%v) = %d, want %d", row.n, got, row.want)
		}
	}
	var sources []string
	for _, row := range toInt32Rows {
		sources = append(sources, "("+NumberText(row.n)+") | 0")
	}
	checkWithNode(t, sources, func(i int) string { return NumberText(float64(toInt32Rows[i].want)) })
}

// ParseText is the text parseInt and parseFloat read of a value, as JavaScript's String gives it, an array
// as far as a number reads it. CONTROL: a string is itself.
func TestParseTextOfEveryKind(t *testing.T) {
	selfHolding := []Value{}
	selfHolding = append(selfHolding, Value{})
	selfHolding[0] = Value{K: Array, V: &selfHolding}
	deep := New([]Value{New(5)})
	for i := 0; i < MaxDepth+2; i++ {
		deep = New([]Value{deep})
	}
	for _, row := range []struct {
		name string
		in   Value
		want string
	}{
		{"CONTROL: a string", NewString(" 12px"), " 12px"},
		{"a number", New(1e21), "1e+21"},
		{"a small number", New(0.0000005), "5e-7"},
		{"null", NewNil(), "null"},
		{"true", ToBool(true), "true"},
		{"an array of a text and a number", New([]Value{NewString(" 15 "), New(3)}), " 15 ,"},
		{"an array of one number", New([]Value{New(2.5)}), "2.5"},
		{"an empty array", New([]Value{}), ""},
		{"an array of an empty array", New([]Value{New([]Value{})}), ""},
		{"an array of null and more", New([]Value{NewNil(), New(5)}), ","},
		{"an array of a pair", New([]Value{New([]Value{New(5), New(6)})}), "5,"},
		{"an object", New(map[string]Value{"a": New(1)}), "[object Object]"},
		{"a function", NewFunc(func(args ...Value) Value { return New(1) }), "[object Object]"},
		{"an array that holds itself", Value{K: Array, V: &selfHolding}, ""},
		{"an array nested past MaxDepth", deep, deepText},
	} {
		if got := ParseText(row.in); got != row.want {
			t.Errorf("%s: ParseText = %q, want %q", row.name, got, row.want)
		}
	}
	if got := ToNumber(deep); got.N == got.N {
		t.Errorf("ToNumber of an array nested past MaxDepth = %v, want NaN", got.N)
	}
	if got := ToNumber(Value{K: Array, V: &selfHolding}); got.N != 0 {
		t.Errorf("ToNumber of an array that holds itself = %v, want 0, as JavaScript's join writes it \"\"", got.N)
	}
}

func quoteForNode(text string) string {
	encoded, _ := json.Marshal(text)
	return string(encoded)
}

// checkWithNode runs each JavaScript expression in node and compares String of its value (-0 as "-0")
// with want(i). Without node only the Go side is checked.
func checkWithNode(t *testing.T, sources []string, want func(int) string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Logf("node is not on PATH: the rows are checked against Kitwork only")
		return
	}
	script := "const word = (v) => Object.is(v, -0) ? \"-0\" : String(v);\nprocess.stdout.write(JSON.stringify([" +
		strings.Join(func() []string {
			out := make([]string, len(sources))
			for i, source := range sources {
				out[i] = "word(" + source + ")"
			}
			return out
		}(), ",\n") + "]));\n"
	path := filepath.Join(t.TempDir(), "numbers.js")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	output, err := exec.CommandContext(ctx, node, path).Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	var results []string
	if err := json.Unmarshal(output, &results); err != nil || len(results) != len(sources) {
		t.Fatalf("node gave %q (%v)", output, err)
	}
	for i, source := range sources {
		if results[i] != want(i) {
			t.Errorf("node: %s = %s, the row says %s", source, results[i], want(i))
		}
	}
}
