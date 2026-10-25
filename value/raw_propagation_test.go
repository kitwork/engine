package value

import "testing"

// Trust does not propagate (value.TrustedHTML): an engine-trusted string, once any String method has
// worked on it, is an ordinary string again, and a template escapes it. Until
// 25/10/2026 int(), float(), json("string"), replace() and replaceAll() returned the receiver's
// copy with Raw still set, and int()/float() kept the string in V too, so
// `x.highlight().replace("a", input)` and `x.highlight().int()` reached the page unescaped.
// CONTROL: the receiver itself carries the mark the test set.
func TestStringMethodsNeverPropagateRaw(t *testing.T) {
	raw := Trusted("<a>b</a> a")
	if !raw.IsTrusted() {
		t.Fatal("control: the receiver must carry the Raw mark")
	}
	arguments := []Value{New("a"), New("<script>")}

	for _, name := range []string{
		// every kind
		"string", "text", "toString", "int", "integer", "float", "toFloat", "json", "toJson", "len", "length",
		// the String table (kind.go, StandardMethod)
		"upper", "toUpperCase", "lower", "toLowerCase", "trim", "trimStart", "trimLeft", "trimEnd", "trimRight",
		"includes", "contains", "startsWith", "endsWith", "split", "replace", "replaceAll", "slice",
		"substring", "substr", "indexOf", "lastIndexOf", "charAt", "charCodeAt", "at", "repeat",
		"padStart", "padEnd", "concat", "capitalize",
	} {
		method, ok := String.StandardMethod(name)
		if !ok {
			t.Errorf("%s: not a String method", name)
			continue
		}
		result := method(raw, arguments...)
		if result.IsTrusted() {
			t.Errorf("%q.%s(): the result keeps the receiver's Raw mark", raw.String(), name)
		}
		if result.K == Array {
			for index, item := range result.Array() {
				if item.IsTrusted() {
					t.Errorf("%q.%s(): item %d keeps the receiver's Raw mark", raw.String(), name, index)
				}
			}
		}
		// json("string") is the method with an argument: the one-argument form is parsing, no mark
		if name == "json" || name == "toJson" {
			if result := method(raw, New("string")); result.IsTrusted() {
				t.Errorf("%q.%s(\"string\"): the result keeps the receiver's Raw mark", raw.String(), name)
			}
		}
	}
}

// A number made of a string is a number only: int(), float() and len() leave no text in V, so a
// template writes the number, never the text the receiver held (until 25/10/2026 int() of a string
// kept the string in V, and the page printed the string).
func TestNumberOfStringHoldsNoText(t *testing.T) {
	raw := Trusted("<a>b</a>")
	for name, result := range map[string]Value{
		"int":    raw.Integer(),
		"float":  raw.ToFloat(),
		"length": raw.Length(),
	} {
		if result.K != Number {
			t.Errorf("%s: kind %v, want Number", name, result.K)
		}
		if result.V != nil {
			t.Errorf("%s: V = %#v, want nil", name, result.V)
		}
		if result.IsTrusted() {
			t.Errorf("%s: Raw kept", name)
		}
	}
	if got := raw.Length().N; got != 8 {
		t.Errorf("length = %v, want 8", got)
	}
	// CONTROL: the methods still compute: a number's int() truncates, its float() is itself
	if got := New(-2.5).Integer().N; got != -2 {
		t.Errorf("(-2.5).int() = %v, want -2", got)
	}
	if got := New(2.5).ToFloat().N; got != 2.5 {
		t.Errorf("(2.5).float() = %v, want 2.5", got)
	}
	// CONTROL: replace and replaceAll still replace, and json("string") still serializes
	if got := raw.Replace(New("a"), New("x")).String(); got != "<x>b</a>" {
		t.Errorf("replace = %q", got)
	}
	if got := raw.ReplaceAll(New("a"), New("x")).String(); got != "<x>b</x>" {
		t.Errorf("replaceAll = %q", got)
	}
	// CONTROL: json("string") still serializes (encoding/json writes < and > as \u003c and \u003e)
	if got := raw.ToJson(New("string")).String(); got != `"\u003ca\u003eb\u003c/a\u003e"` {
		t.Errorf("json(\"string\") = %q", got)
	}
	// CONTROL: json() of text that is not JSON gives the text back, as an ordinary string
	if got := raw.ToJson(); got.String() != "<a>b</a>" || got.IsTrusted() {
		t.Errorf("json() of plain text = %q, Raw %v", got.String(), got.IsTrusted())
	}
}
