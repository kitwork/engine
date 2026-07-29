package value

import "testing"

// safe() is the ONLY inline-error shape. It replaced a pair — an object and a two-element array —
// that were similar enough the dispatch table's own comments ended up describing each other, which
// is the clearest evidence two spellings of one idea cost more than they gave.
//
// Each case below is a different way a failure can arrive, because the point of having one shape is
// that a handler never has to know which way it did.

// an error-carrying value: the data half plus an attached error
func erroredRecord() Value {
	v := New(map[string]Value{"id": New(7)})
	v.IsError = true
	v.ErrorVal = map[string]Value{"code": New("DATABASE_ERROR"), "message": New("boom")}
	return v
}

func TestSafeSplitsAnAttachedError(t *testing.T) {
	r := erroredRecord().Safe()

	if r.Get("value").Get("id").Int() != 7 {
		t.Error(".value must hold the data")
	}
	if !r.Get("isError").Truthy() {
		t.Error(".isError must be true (carried on the wrapper)")
	}
	// .error is an ACCESSOR, not a map field: a plain "error" key would be shadowed by it.
	if got := r.Get("error").Get("message").String(); got != "boom" {
		t.Errorf(".error.message = %q, want boom", got)
	}
	// The data must come back clean, or the caller re-discovers the failure it just handled.
	if r.Get("value").IsError {
		t.Error(".value still carries the error flag")
	}
}

func TestSafeOnSuccess(t *testing.T) {
	r := New(map[string]Value{"id": New(7)}).Safe()

	// Readable and null rather than missing: `if (check.error)` must be safe to write before
	// knowing whether anything failed.
	if r.Get("error").K != Nil {
		t.Errorf(".error must be null on success, got kind %v", r.Get("error").K)
	}
	if r.Get("value").Get("id").Int() != 7 {
		t.Error(".value must hold the record on success")
	}
	if r.IsError {
		t.Error("a plain value must not be reported as an error")
	}
}

// A hard failure — a broken query, fail("…"). This language has no try/catch, so without safe()
// the Invalid value keeps propagating and the request ends in an error page instead of a decision
// the author made.
func TestSafeRescuesAHardFailure(t *testing.T) {
	r := Value{K: Invalid, V: "database query error: boom"}.Safe()

	if r.Get("value").K != Nil {
		t.Error(".value must be null on a hard failure — there is no data")
	}
	if got := r.Get("error").Get("message").String(); got != "database query error: boom" {
		t.Errorf(".error.message = %q, want the Invalid .V", got)
	}
}

// CONTROL: safe() must change the SHAPE, not pass the value through. Without this every assertion
// above would also hold on an implementation that returns its input unchanged.
func TestSafeAlwaysWraps(t *testing.T) {
	r := New("hello").Safe()

	if r.K != Map {
		t.Fatalf("safe() must return an object, got kind %v", r.K)
	}
	if r.Get("value").String() != "hello" {
		t.Fatalf(".value = %q, want hello", r.Get("value").String())
	}
}

// safe() is the one door through an Invalid value; every other access stays Invalid so a bare
// failure keeps bubbling instead of silently reading as empty.
func TestInvalidExposesOnlySafe(t *testing.T) {
	bad := Value{K: Invalid, V: "boom"}

	if bad.Get("safe").K != Func {
		t.Error(".safe must dispatch even on Invalid")
	}
	if bad.Get("name").K != Invalid {
		t.Error("any other access on Invalid must stay Invalid (bubble)")
	}
}

// An error value still reads like an error object without being reshaped.
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

func TestSafeRegisteredAndResultGone(t *testing.T) {
	for _, k := range []Kind{Map, Array} {
		if _, ok := k.Method("safe"); !ok {
			t.Errorf("safe not registered for %v", k)
		}
		// Asserting the absence keeps a later "restore the old helper" from quietly bringing back
		// two shapes for one idea.
		if _, ok := k.Method("result"); ok {
			t.Errorf("result is back on %v — there must be exactly one inline-error shape", k)
		}
	}
}
