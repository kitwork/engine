package value

import (
	"testing"
	"time"
)

type invokeHost struct{ Count int }

func (*invokeHost) Name() string            { return "host" }
func (*invokeHost) Greet(who string) string { return "chào " + who }

// value.Invoke is the call after a dot for the VM (through nativeMethod) and for a .kitwork.html
// template. A name that is neither a function the value owns, nor a builtin of its kind, nor a Go method
// of a host value is not a function: the call is the NotAFunction failure (14/10/2026), where it gave
// the value read until then ({ x: 3 }.x() was 3, a missing name null).
func TestInvokeOfANameThatIsNotAFunctionFails(t *testing.T) {
	object := New(map[string]Value{"x": New(3), "pick": New(3), "a": New(1)})
	host := New(&invokeHost{Count: 2})
	cases := []struct {
		label    string
		receiver Value
		name     string
		want     string
	}{
		{"own data", object, "x", "x không phải hàm (là số): không gọi được"},
		{"missing name", object, "nope", "nope không phải hàm (là null): không gọi được"},
		{"string", NewString("abc"), "nope", "nope không phải hàm (là null): không gọi được"},
		{"host field", host, "count", "count không phải hàm (là số): không gọi được"},
		{"host missing", host, "nope", "nope không phải hàm (là null): không gọi được"},
	}
	for _, c := range cases {
		got := c.receiver.Invoke(c.name)
		message, ok := NotAFunctionFrom(got)
		if !ok || message != c.want || !got.IsError {
			t.Errorf("%s: Invoke(%q) = %#v, want the NotAFunction failure %q", c.label, c.name, got, c.want)
			continue
		}
		if failure, _ := FailureFrom(got); failure.Code != NotAFunctionCode || failure.Message != c.want {
			t.Errorf("%s: failure = %+v, want code %s", c.label, failure, NotAFunctionCode)
		}
	}

	// What is a function is called: the builtin past own data of its name (rule A), a host getter
	// (already called by Get) and a host method; a method of null is null; a failure passes on.
	if got := object.Invoke("pick", NewString("a")); got.K != Map || len(got.Map()) != 1 || got.Get("a").Int() != 1 {
		t.Errorf("object.pick(\"a\") = %s, want the builtin's {a: 1}", got.Text())
	}
	if got := host.Invoke("name"); got.Text() != "host" {
		t.Errorf("host.name() = %s, want the getter's host", got.Text())
	}
	if got := host.Invoke("greet", NewString("Quốc")); got.Text() != "chào Quốc" {
		t.Errorf("host.greet(\"Quốc\") = %s", got.Text())
	}
	if got := (Value{K: Nil}).Invoke("trim"); got.K != Nil {
		t.Errorf("null.trim() = %#v, want null", got)
	}
	failed := Value{K: Invalid, V: "boom"}
	if got := failed.Invoke("anything"); got.K != Invalid || got.Text() != "boom" {
		t.Errorf("a failure's method = %#v, want the failure itself", got)
	}
	if _, ok := NotAFunctionFrom(failed); ok {
		t.Error("a plain failure reads as NotAFunction")
	}
}

// The message says what the called value is, one word per kind, and never a string's text.
func TestNotAFunctionMessageNamesTheKind(t *testing.T) {
	cases := []struct {
		called Value
		kind   string
	}{
		{Value{K: Nil}, "null"},
		{New(3.5), "số"}, // a number is never shown either (14/10/2026): env.PIN() is a secret too
		{ToBool(true), "boolean true"},
		{NewString("sk-live-secret"), "chuỗi"},
		{New(map[string]Value{}), "object"},
		{New([]Value{New(1)}), "mảng"},
		{New(time.Unix(0, 0)), "thời điểm"},
		{New(time.Second), "khoảng thời gian"},
		{New([]byte("x")), "dãy byte"},
		{New(&invokeHost{}), "object của engine"},
		{Value{K: Invalid, V: "x"}, "lỗi"},
	}
	for _, c := range cases {
		want := "f không phải hàm (là " + c.kind + "): không gọi được"
		if got := NotAFunctionMessage("f", c.called); got != want {
			t.Errorf("%s: %q, want %q", c.called.K, got, want)
		}
	}
}
