package value

import "testing"

// The methods an object (map) gets from the engine: its own builtins and the conversions every kind
// has. Each must exist as a method, or the row below proves nothing.
var objectMethodNames = []string{
	"keys", "delete", "has", "merge", "pick", "omit",
	"string", "text", "toString", "int", "integer", "float", "toFloat", "json", "toJson", "len", "length",
}

// An object's own key wins over the method of the same name on a READ, as an own property shadows a
// prototype method in JavaScript. Before 13/10/2026 { delete: 3 }.delete was the builtin, and a key
// named after a method could be written but never read back. A CALL reaches the own key only when it
// holds a function (CallsOwn); an own key holding data leaves the call to the builtin.
func TestOwnKeyWinsOverObjectMethod(t *testing.T) {
	for _, name := range objectMethodNames {
		builtin, ok := Map.Method(name)
		if !ok {
			t.Fatalf("%s is not a method of an object: the row tests nothing", name)
		}

		own := New(map[string]Value{name: New(3)})
		if !own.HasOwn(name) {
			t.Fatalf("{ %s: 3 } does not report %q as its own key", name, name)
		}
		if got := own.Get(name); got.K != Number || got.N != 3 {
			t.Fatalf("{ %s: 3 }.%s = %s %v, want the own key 3", name, name, got.K, got.Text())
		}
		// Invoke is how a template calls a method ({{ o.keys() }}): an own key holding data does not
		// answer it, the builtin does, on the same object.
		if own.CallsOwn(name) {
			t.Fatalf("{ %s: 3 } reports that a call reaches its data", name)
		}
		got, want := own.Invoke(name, NewString("k")), builtin(own, NewString("k"))
		if got.K == Number && got.N == 3 {
			t.Fatalf("{ %s: 3 }.%s(\"k\") answered with the data 3", name, name)
		}
		if got.K != want.K || got.Text() != want.Text() {
			t.Fatalf("{ %s: 3 }.%s(\"k\") = %s %q, want the builtin's %s %q", name, name, got.K, got.Text(), want.K, want.Text())
		}

		// An own key holding a function is what a call reaches.
		fn := New(map[string]Value{name: NewFunc(func(...Value) Value { return NewString("own") })})
		if !fn.CallsOwn(name) {
			t.Fatalf("{ %s: f } does not report that a call reaches f", name)
		}
		if got := fn.Invoke(name).Text(); got != "own" {
			t.Fatalf("{ %s: f }.%s() = %q, want f's \"own\"", name, name, got)
		}

		// Control: an object without that key keeps the method.
		plain := New(map[string]Value{"a": New(1)})
		if plain.HasOwn(name) || plain.CallsOwn(name) {
			t.Fatalf("{ a: 1 } reports %q as its own key", name)
		}
		if name != "length" { // length reads as the number of keys, below
			if got := plain.Get(name); got.K != Func {
				t.Fatalf("{ a: 1 }.%s = %s, want the builtin method", name, got.K)
			}
		}
	}

	// An own key holding a host function is called through Invoke: the shape of new Date(), whose
	// toString used to give the object's own text instead of the date.
	date := New(map[string]Value{"toString": NewFunc(func(...Value) Value { return NewString("Thu Jan 01 1970") })})
	if got := date.Invoke("toString").Text(); got != "Thu Jan 01 1970" {
		t.Fatalf("an own toString was not called: got %q", got)
	}

	// The builtins still work on an object without the key.
	plain := New(map[string]Value{"a": New(1), "b": New(2)})
	if got := plain.Invoke("keys"); got.Len() != 2 {
		t.Fatalf("{ a, b }.keys() = %v, want two keys", got.Text())
	}
	if got := plain.Invoke("has", NewString("a")); !got.Truthy() {
		t.Fatal(`{ a, b }.has("a") is false`)
	}

	// Arrays and strings carry no named keys of their own: nothing to shadow.
	if New([]Value{New(1)}).HasOwn("keys") || NewString("keys").HasOwn("keys") {
		t.Fatal("an array or a string reported a named key of its own")
	}
}

// On a plain object an own key named error, length or isError is data (13/10/2026): { ok: false,
// error: "x" }.error is "x", { length: 5 }.length is 5. Since 14/10/2026 that holds for every object:
// no value carries an error the engine attached to data any more, so none keeps the engine's answers
// for those three. An object without the keys has the engine's length, and no error or isError.
func TestOwnErrorLengthAndIsErrorOnAPlainObject(t *testing.T) {
	keys := func() map[string]Value {
		return map[string]Value{"ok": New(false), "error": NewString("x"), "length": New(5), "isError": NewString("I")}
	}

	plain := New(keys())
	if got := plain.Get("error"); got.Text() != "x" {
		t.Fatalf(`{ error: "x" }.error = %q, want "x"`, got.Text())
	}
	if got := plain.Get("length"); got.K != Number || got.N != 5 {
		t.Fatalf("{ length: 5 }.length = %s %v, want 5", got.K, got.Text())
	}
	if got := plain.Get("isError"); got.Text() != "I" {
		t.Fatalf(`{ isError: "I" }.isError = %q, want "I"`, got.Text())
	}
	// length is a method too: with data in the key, a call is the builtin (the number of keys).
	if got := plain.Invoke("length"); got.K != Number || got.N != 4 {
		t.Fatalf("{ …, length: 5 }.length() = %s %v, want the builtin's 4", got.K, got.Text())
	}

	// A value marked as carrying an error (no producer makes one since 14/10/2026) answers with its
	// own keys too: the rule has no exception left.
	marked := New(keys())
	marked.IsError = true
	marked.ErrorVal = NewFailure("E_CODE", "it failed")
	if got := marked.Get("error"); got.Text() != "x" {
		t.Fatalf("a value marked IsError: .error = %s %v, want its own \"x\"", got.K, got.Text())
	}
	if got := marked.Get("length"); got.K != Number || got.N != 5 {
		t.Fatalf("a value marked IsError: .length = %s %v, want its own 5", got.K, got.Text())
	}
	if got := marked.Get("isError"); got.Text() != "I" {
		t.Fatalf("a value marked IsError: .isError = %s %v, want its own \"I\"", got.K, got.Text())
	}

	// Control: an object without those keys answers as before.
	bare := New(map[string]Value{"a": New(1), "b": New(2)})
	if got := bare.Get("error"); got.K != Nil {
		t.Fatalf("{ a, b }.error = %s, want null", got.K)
	}
	if got := bare.Get("length"); got.K != Number || got.N != 2 {
		t.Fatalf("{ a, b }.length = %s %v, want 2", got.K, got.Text())
	}
	if got := bare.Get("isError"); got.K != Nil {
		t.Fatalf("{ a, b }.isError = %s %v, want null (the engine answers isError on no data)", got.K, got.Text())
	}
}
