package value

import (
	"context"
	"errors"
	"strings"
)

// The codes a failure carries: one closed set (14/10/2026), what safe(() => …) gives as error.code.
// Upper case with underscores, no vendor prefix, and nothing an application chooses: fail("…") is
// always FailCode. When two codes fit one failure the narrower wins: DENIED, TIMEOUT, CONFLICT and
// PARTIAL before DATABASE, NETWORK and INVALID_INPUT.
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
	// DefaultFailureCode is the fallback of a failure made without a code. A test counts the places
	// that still make one (failure_ratchet_test.go) and only lets the count go down, to zero.
	DefaultFailureCode = "ERROR"

	// CancelledCode is not one of the set: a host call that failed because the request was cancelled
	// returns it, and the VM ends the run with EXECUTION_CANCELLED, which nothing catches.
	CancelledCode = "EXECUTION_CANCELLED"
)

// closedCodes is the set error.code is drawn from.
var closedCodes = map[string]bool{
	FailCode:            true,
	NotAFunctionCode:    true,
	TypeCode:            true,
	InvalidArgumentCode: true,
	InvalidInputCode:    true,
	UnavailableCode:     true,
	UnsupportedCode:     true,
	DatabaseCode:        true,
	ConflictCode:        true,
	PartialCode:         true,
	TimeoutCode:         true,
	NetworkCode:         true,
	DeniedCode:          true,
	DefaultFailureCode:  true,
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

// InvalidFailure creates a hard application failure. The message is kept in V
// for compatibility with existing Invalid formatting and in Failure so safe()
// can preserve the machine-readable code.
func InvalidFailure(code, message string) Value {
	failure := NewFailure(code, message)
	return Value{
		K:        Invalid,
		V:        failure.Message,
		IsError:  true,
		ErrorVal: failure,
	}
}

// PartialFailure is the failure of a bulk write that stopped midway: PartialCode, with progress (what
// was written durably) for safe(() => …) to hand on as error.progress.
func PartialFailure(message string, progress Value) Value {
	failure := InvalidFailure(PartialCode, message)
	attached := failure.ErrorVal.(Failure)
	attached.Progress = progress
	failure.ErrorVal = attached
	return failure
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

// FailureFrom extracts the typed application failure attached to a Value.
func FailureFrom(result Value) (Failure, bool) {
	if !result.IsError {
		return Failure{}, false
	}
	if failure, ok := failureFromPayload(result.ErrorVal); ok {
		return failure, true
	}
	if result.K == Invalid {
		if message, ok := result.V.(string); ok {
			return NewFailure(DefaultFailureCode, message), true
		}
		if failure, ok := result.V.(error); ok {
			return NewFailure(DefaultFailureCode, failure.Error()), true
		}
	}
	return NewFailure(DefaultFailureCode, "error"), true
}

func failureFromPayload(payload any) (Failure, bool) {
	switch current := payload.(type) {
	case Failure:
		failure := NewFailure(current.Code, current.Message)
		failure.Progress = current.Progress
		return failure, true
	case *Failure:
		if current == nil {
			return Failure{}, false
		}
		failure := NewFailure(current.Code, current.Message)
		failure.Progress = current.Progress
		return failure, true
	default:
		return Failure{}, false
	}
}
