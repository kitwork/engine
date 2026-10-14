package value

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// x.safe() and its result, a Go struct (SafeResult), are gone (14/10/2026): safe(() => x) is a global
// the VM runs (runtime/safe.go) and gives a plain object. What stays in this package is the failure
// itself: its closed set of codes, PARTIAL's progress, and the global's value.

// Every code of the set is itself; any other code, an old vendor one included, reads as ERROR.
func TestClosedCodeKeepsTheSetAndFoldsTheRest(t *testing.T) {
	set := []string{FailCode, NotAFunctionCode, TypeCode, InvalidArgumentCode, InvalidInputCode,
		UnavailableCode, UnsupportedCode, DatabaseCode, ConflictCode, PartialCode, TimeoutCode,
		NetworkCode, DeniedCode, DefaultFailureCode}
	for _, code := range set {
		if got := ClosedCode(code); got != code {
			t.Errorf("ClosedCode(%q) = %q, want itself", code, got)
		}
	}
	if len(set) != len(closedCodes) {
		t.Errorf("the set has %d codes, the test knows %d: a code was added without a test", len(closedCodes), len(set))
	}
	for _, old := range []string{"KITDB_TRANSACTION_CONFLICT", "KITDB_CREATE_MANY_PARTIAL", "DATABASE_ERROR",
		"TYPE_ERROR", "RANGE_ERROR", "OUT_OF_STOCK", "", "ENERGY_LIMIT", CancelledCode, "fail"} {
		if got := ClosedCode(old); got != DefaultFailureCode {
			t.Errorf("ClosedCode(%q) = %q, want %s: the set stays closed", old, got, DefaultFailureCode)
		}
	}
}

// PARTIAL is the one failure with something done to report: its progress travels with it.
func TestPartialFailureCarriesProgress(t *testing.T) {
	progress := New(map[string]Value{"inserted": New(3), "next": New(3)})
	failed := PartialFailure("stopped at row 3", progress)
	if failed.K != Invalid || failed.Text() != "stopped at row 3" {
		t.Fatalf("PartialFailure = %s %q, want a failure with its message", failed.K, failed.Text())
	}
	failure, ok := FailureFrom(failed)
	if !ok || failure.Code != PartialCode || failure.Progress.Get("inserted").Int() != 3 {
		t.Fatalf("failure = %+v, want PARTIAL carrying inserted 3", failure)
	}
	// control: a failure of any other code carries no progress
	if other, _ := FailureFrom(InvalidFailure(DatabaseCode, "x")); other.Progress.K != Invalid {
		t.Fatalf("a DATABASE failure carries progress %s", other.Progress.Text())
	}
}

type namedCodeError struct{}

func (namedCodeError) Error() string       { return "named" }
func (namedCodeError) FailureCode() string { return ConflictCode }

// A Go error a host returns becomes a failure with the code it says: the request's cancellation, a
// deadline, a code the error names; ERROR only when nothing tells.
func TestFailureOfErrorReadsWhatTheErrorSays(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("query: %w", context.Canceled), CancelledCode},
		{fmt.Errorf("query: %w", context.DeadlineExceeded), TimeoutCode},
		{fmt.Errorf("wrapped: %w", namedCodeError{}), ConflictCode},
		{errors.New("something"), DefaultFailureCode},
	} {
		failure, _ := FailureFrom(FailureOfError("call x", c.err))
		if failure.Code != c.want || !strings.HasPrefix(failure.Message, "call x: ") {
			t.Errorf("FailureOfError(%v) = %+v, want %s led by the call", c.err, failure, c.want)
		}
	}
}

// .safe is a method of no kind any more, and a failure does not answer it: x.safe() is a compile error
// (compiler), and a template's {{ o.safe() }} is the NOT_A_FUNCTION error of any missing method.
func TestSafeIsNoMethodOfAnyKind(t *testing.T) {
	for _, k := range []Kind{Nil, Number, Bool, Time, Duration, String, Bytes, Map, Array, Struct, Func} {
		if _, ok := k.Method("safe"); ok {
			t.Errorf("safe is still a method of %v", k)
		}
		if _, ok := k.Method("result"); ok {
			t.Errorf("result is a method of %v", k)
		}
	}
	if got := (Value{K: Invalid, V: "boom"}).Get("safe"); got.K != Invalid {
		t.Errorf("a failure answers .safe with %s, want the failure itself", got.K)
	}
	if got := New(map[string]Value{"a": New(1)}).Invoke("safe"); got.K != Invalid {
		t.Fatalf("{ a: 1 }.safe() = %s, want the NOT_A_FUNCTION failure", got.K)
	} else if failure, _ := FailureFrom(got); failure.Code != NotAFunctionCode {
		t.Fatalf("{ a: 1 }.safe() failed with %+v, want %s", failure, NotAFunctionCode)
	}
}

// The global safe runs its function on the VM that called it; a host handed it has none to lend.
func TestSafeFunctionNeedsTheProgramThatCallsIt(t *testing.T) {
	safe := NewSafeFunction()
	if !IsSafeFunction(safe) || IsSafeFunction(New(1)) || IsSafeFunction(NewFunc(func(...Value) Value { return New(1) })) {
		t.Fatal("IsSafeFunction does not tell the global safe from other values")
	}
	got := safe.Call("safe", NewFunc(func(...Value) Value { return New(1) }))
	if failure, _ := FailureFrom(got); got.K != Invalid || failure.Code != InvalidArgumentCode ||
		!strings.Contains(failure.Message, "safe(() => …)") {
		t.Fatalf("safe called by a host = %s %+v, want INVALID_ARGUMENT saying how to call it", got.K, failure)
	}
}

// An operator on kinds it does not take is TYPE, saying the operator and both kinds and never a value
// (until 14/10/2026 it had no code and its whole message was "invalid").
func TestTypeFailureNamesOperatorAndKinds(t *testing.T) {
	for _, c := range []struct {
		got  Value
		want string
	}{
		{NewString("bí mật").Mul(New(2)), "phép * không dùng được giữa chuỗi và số"},
		{New(1).Sub(NewString("x")), "phép - không dùng được giữa số và chuỗi"},
		{New(7).Extend(New([]Value{New(1)})), "phép + không dùng được giữa số và mảng"},
		{New(map[string]Value{}).Div(New(2)), "phép / không dùng được giữa object và số"},
		{New(true).Mod(New(2)), "phép % không dùng được giữa boolean và số"},
	} {
		failure, ok := FailureFrom(c.got)
		if c.got.K != Invalid || !ok || failure.Code != TypeCode || failure.Message != c.want {
			t.Errorf("got %s %+v, want TYPE %q", c.got.K, failure, c.want)
		}
		if strings.Contains(failure.Message, "bí mật") || strings.Contains(failure.Message, "7") {
			t.Errorf("a type failure printed a value: %q", failure.Message)
		}
	}
	// control: the kinds an operator does take still compute
	if got := New(6).Mul(New(7)); got.K != Number || got.N != 42 {
		t.Fatalf("6 * 7 = %s %v", got.K, got.Text())
	}
}

// An error value still reads like an error object to a host, without being reshaped.
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

// ok is a plain key of data, not a reserved word: a column named ok reads as itself.
func TestOkDoesNotBecomeAReservedWord(t *testing.T) {
	row := New(map[string]Value{"ok": New("DU-LIEU-CUA-TOI")})
	if got := row.Get("ok").String(); got != "DU-LIEU-CUA-TOI" {
		t.Fatalf("a data field named ok was shadowed: got %q", got)
	}
}
