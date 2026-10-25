package value

import (
	"context"
	"errors"
	"strings"
)

// The codes a failure carries: one closed set (14/10/2026), what safe(() => …) gives as error.code.
// Upper case with underscores, no vendor prefix, and nothing an application chooses: fail("…") is
// always FailCode. When two codes fit one failure the narrower wins: DENIED, TIMEOUT, CONFLICT, PARTIAL,
// NOT_FOUND and TOO_LARGE before DATABASE, NETWORK and INVALID_INPUT.
const (
	// FailCode is a program that stopped itself with fail("…").
	FailCode = "FAIL"
	// TypeCode is an operation on kinds it does not take: "a" * 2, an object that holds itself
	// written out whole (JSON.stringify).
	TypeCode = "TYPE"
	// InvalidArgumentCode is an engine API called the wrong way, an operation this connection does not
	// have included (an update without a where, createMany outside KitDB), and an operation past its
	// own size bound ("x".repeat past 8 MB): nothing was spent, so it can be caught.
	InvalidArgumentCode = "INVALID_ARGUMENT"
	// InvalidInputCode is data from outside that cannot be read: a body that is not JSON or is past its
	// limit, JSON.parse, BigInt, a token.
	InvalidInputCode = "INVALID_INPUT"
	// UnavailableCode is a service the engine has no connection to: not configured, or broken since it
	// was opened (a database not connected, Chrome, a search index).
	UnavailableCode = "UNAVAILABLE"
	// UnsupportedCode is an operation this engine or this connection does not do.
	UnsupportedCode = "UNSUPPORTED"
	// DatabaseCode is a query or a write that failed in the storage layer, a driver error included.
	DatabaseCode = "DATABASE"
	// ConflictCode is a write that met another: a transaction conflict, a unique constraint.
	ConflictCode = "CONFLICT"
	// PartialCode is a bulk write that stopped midway; its failure carries Progress.
	PartialCode = "PARTIAL"
	// TimeoutCode is an operation past its own time limit: a query, a transaction, an HTTP request.
	TimeoutCode = "TIMEOUT"
	// NetworkCode is an outside service that could not be reached.
	NetworkCode = "NETWORK"
	// DeniedCode is the engine refusing by policy: SSRF, a file outside the app's folder, an entity's
	// rights.
	DeniedCode = "DENIED"
	// NotFoundCode is a thing named exactly that is not there, from a function that promised to give it:
	// a file to read, copy or move, db.detail() finding no row (22/10/2026). A function that gives a thing
	// only when there is one (file.info, a storage get, db.first) gives null instead.
	NotFoundCode = "NOT_FOUND"
	// TooLargeCode is past the quota or the size ceiling of a place that stores (a file, disk(),
	// datastore()), or past the ceiling of a read into the VM's memory (22/10/2026). A ceiling of the
	// argument itself stays INVALID_ARGUMENT ("x".repeat past 8 MB), a body past its limit INVALID_INPUT.
	TooLargeCode = "TOO_LARGE"
	// RoutineCancelledCode is a routine stopped by routine.cancel(handle) that a routine.wait(handle) reads
	// (KITWORK_ROUTINE.md mục 6, 22/10/2026): the program asked for the stop, so it can be caught. It is not
	// CancelledCode, the request that is over, which nothing catches; SQLite's own CANCELLED is that one.
	RoutineCancelledCode = "CANCELLED"
	// DefaultFailureCode is the fallback of a failure made without a code. A test counts the places
	// that still make one (failure_ratchet_test.go) and only lets the count go down, to zero.
	DefaultFailureCode = "ERROR"

	// CancelledCode is not one of the set: a host call that failed because the request was cancelled
	// returns it, and the VM ends the run with EXECUTION_CANCELLED, which nothing catches.
	CancelledCode = "EXECUTION_CANCELLED"
)

// closedCodes is the set error.code is drawn from.
var closedCodes = map[string]bool{
	FailCode:             true,
	NotAFunctionCode:     true,
	TypeCode:             true,
	InvalidArgumentCode:  true,
	InvalidInputCode:     true,
	UnavailableCode:      true,
	UnsupportedCode:      true,
	DatabaseCode:         true,
	ConflictCode:         true,
	PartialCode:          true,
	TimeoutCode:          true,
	NetworkCode:          true,
	DeniedCode:           true,
	NotFoundCode:         true,
	TooLargeCode:         true,
	RoutineCancelledCode: true,
	DefaultFailureCode:   true,
}

// ClosedCode is code when it is one of the set, and DefaultFailureCode for any other: a host value a
// new native forgot to give a code of the set still reads as one, so the set stays closed.
func ClosedCode(code string) string {
	if closedCodes[code] {
		return code
	}
	return DefaultFailureCode
}

// Failure is the bounded application-error envelope carried by a host value.
// VM failures use runtime.Diagnostic instead; the two contracts meet through
// Diagnostic.CauseCode without exposing arbitrary host metadata to the VM.
type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// Progress is what a PARTIAL failure had done before it stopped (createMany: inserted, batches,
	// next, failedRow, complete); the zero Value for every other code.
	Progress Value `json:"-"`
}

// Error is the failure's message, so a *Failure in V reads as a Go error wherever a payload is
// written (Text, Append, Interface, fmt's %v). A pointer receiver on purpose: a Failure by value is
// not an error, so nothing boxes one by accident.
func (f *Failure) Error() string {
	if f == nil {
		return ""
	}
	return f.Message
}

func NewFailure(code, message string) Failure {
	code = strings.TrimSpace(code)
	if code == "" {
		code = DefaultFailureCode
	}
	if strings.TrimSpace(message) == "" {
		message = "error"
	}
	return Failure{Code: code, Message: message}
}

// InvalidFailure is a hard application failure: K == Invalid with the Failure itself in V, one
// allocation, read back by Failure() and FailureFrom (its message by Text).
func InvalidFailure(code, message string) Value {
	failure := NewFailure(code, message)
	return Value{K: Invalid, V: &failure}
}

// PartialFailure is the failure of a bulk write that stopped midway: PartialCode, with progress (what
// was written durably) for safe(() => …) to hand on as error.progress.
func PartialFailure(message string, progress Value) Value {
	failure := NewFailure(PartialCode, message)
	failure.Progress = progress
	return Value{K: Invalid, V: &failure}
}

// FailureOfError is the failure of a Go error a host call returned, led by what failed ("call
// x: …"): the request's cancellation is CancelledCode, a deadline TimeoutCode, an error that names a
// code of its own (a FailureCode() string method) that code; any other is DefaultFailureCode, since
// nothing tells what it was.
func FailureOfError(what string, err error) Value {
	message := err.Error()
	if what != "" {
		message = what + ": " + message
	}
	var coded interface{ FailureCode() string }
	switch {
	case errors.Is(err, context.Canceled):
		return InvalidFailure(CancelledCode, message)
	case errors.Is(err, context.DeadlineExceeded):
		return InvalidFailure(TimeoutCode, message)
	case errors.As(err, &coded):
		return InvalidFailure(coded.FailureCode(), message)
	}
	return InvalidFailure(DefaultFailureCode, message)
}

func nameOrAnonymous(name string) string {
	if name == "" {
		return "<anonymous>"
	}
	return name
}

// FailureFrom is the application failure result carries, as a copy of its own (the *Failure in V is
// shared by every copy of the Value). A run's diagnostic reads as DefaultFailureCode with its text;
// false for a value that is no failure, for the zero Value, and for a failure built by hand around a
// string (tests), which has no code.
func FailureFrom(result Value) (Failure, bool) {
	if result.K != Invalid {
		return Failure{}, false
	}
	switch payload := result.V.(type) {
	case *Failure:
		if payload == nil {
			return Failure{}, false
		}
		failure := NewFailure(payload.Code, payload.Message)
		failure.Progress = payload.Progress
		return failure, true
	case error:
		return NewFailure(DefaultFailureCode, payload.Error()), true
	}
	return Failure{}, false
}
