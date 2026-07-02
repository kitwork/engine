package value

import "testing"

// an error-carrying value, like SafeFirst returns on a DB failure (the data half + an attached error)
func erroredRecord() Value {
	v := New(map[string]Value{"id": New(7)})
	v.IsError = true
	v.ErrorVal = map[string]Value{"code": New("DATABASE_ERROR"), "message": New("boom")}
	return v
}

// .result() → OBJECT { value } carrying .error / .isError (à la Rust's Result).
func TestResultObjectShape(t *testing.T) {
	r := erroredRecord().Result()
	if r.Get("value").Get("id").Int() != 7 {
		t.Error(".value must hold the data")
	}
	if !r.Get("isError").Truthy() {
		t.Error(".isError must be true (carried on the wrapper)")
	}
	if got := r.Get("error").Get("message").String(); got != "boom" {
		t.Errorf(".error.message = %q, want boom (accessor, not a shadowed field)", got)
	}
}

func TestResultObjectSuccess(t *testing.T) {
	r := New(map[string]Value{"id": New(7)}).Result()
	if r.Get("error").K != Nil {
		t.Errorf(".error must be null on success, got kind %v", r.Get("error").K)
	}
	if r.Get("value").Get("id").Int() != 7 {
		t.Error(".value must hold the record on success")
	}
}

// .safe() → ARRAY [data, error] for destructuring.
func TestSafeArrayShape(t *testing.T) {
	arr := erroredRecord().Safe().Array()
	if len(arr) != 2 {
		t.Fatalf(".safe() must be [data, error], got len %d", len(arr))
	}
	if arr[0].IsError {
		t.Error("data half must have its error flag cleared")
	}
	if got := arr[1].Get("message").String(); got != "boom" {
		t.Errorf("error half .message = %q, want boom", got)
	}
}

func TestSafeArraySuccess(t *testing.T) {
	arr := New(map[string]Value{"id": New(7)}).Safe().Array()
	if arr[1].K != Nil {
		t.Errorf("error half must be null on success, got kind %v", arr[1].K)
	}
	if arr[0].Get("id").Int() != 7 {
		t.Error("data half must be the record itself")
	}
}

// A hard failure (Invalid, e.g. first() on a DB error) must be rescued by .result()/.safe() into a
// capturable shape — so no safeFirst() is needed.
func TestResultRescuesInvalid(t *testing.T) {
	r := Value{K: Invalid, V: "database query error: boom"}.Result()
	if r.Get("value").K != Nil {
		t.Error(".value must be null on a hard failure")
	}
	if got := r.Get("error").Get("message").String(); got != "database query error: boom" {
		t.Errorf(".error.message = %q, want the Invalid .V", got)
	}
}

func TestSafeRescuesInvalid(t *testing.T) {
	arr := Value{K: Invalid, V: "boom"}.Safe().Array()
	if arr[0].K != Nil {
		t.Error("data half must be null on a hard failure")
	}
	if got := arr[1].Get("message").String(); got != "boom" {
		t.Errorf("error half .message = %q, want boom", got)
	}
}

// Only result/safe pierce an Invalid; any other access stays Invalid (keeps bubbling).
func TestInvalidExposesOnlyResultSafe(t *testing.T) {
	bad := Value{K: Invalid, V: "boom"}
	if bad.Get("result").K != Func {
		t.Error(".result must dispatch even on Invalid")
	}
	if bad.Get("safe").K != Func {
		t.Error(".safe must dispatch even on Invalid")
	}
	if bad.Get("name").K != Invalid {
		t.Error("any other access on Invalid must stay Invalid (bubble)")
	}
}

// An error value (fail/new Error → Invalid) reads like an error object: .message + .isError, while
// anything else still stays Invalid (keeps bubbling).
func TestInvalidExposesMessageAndIsError(t *testing.T) {
	e := Value{K: Invalid, V: "Email trống"}
	if got := e.Get("message").String(); got != "Email trống" {
		t.Errorf(".message = %q, want 'Email trống'", got)
	}
	if !e.Get("isError").Truthy() {
		t.Error(".isError must be true on an error value")
	}
	if e.Get("foo").K != Invalid {
		t.Error("any other access on Invalid must stay Invalid (bubble)")
	}
}

func TestResultSafeRegistered(t *testing.T) {
	for _, n := range []string{"result", "safe"} {
		if _, ok := Map.Method(n); !ok {
			t.Errorf("%q not registered as a method", n)
		}
		if _, ok := Array.Method(n); !ok {
			t.Errorf("%q not registered for Array", n)
		}
	}
}
