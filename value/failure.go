package value

import "strings"

const DefaultFailureCode = "ERROR"

// Failure is the bounded application-error envelope carried by a host value.
// VM failures use runtime.Diagnostic instead; the two contracts meet through
// Diagnostic.CauseCode without exposing arbitrary host metadata to the VM.
type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
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

// WithFailure attaches one application failure to a successful or partial
// value while preserving the value itself.
func WithFailure(result Value, code, message string) Value {
	result.IsError = true
	result.ErrorVal = NewFailure(code, message)
	return result
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

// FailureFrom extracts the typed application failure attached to a Value.
// Legacy map metadata remains readable during migration, but new producers
// should use WithFailure or InvalidFailure.
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
	}
	return NewFailure(DefaultFailureCode, "error"), true
}

func failureFromPayload(payload any) (Failure, bool) {
	switch current := payload.(type) {
	case Failure:
		return NewFailure(current.Code, current.Message), true
	case *Failure:
		if current == nil {
			return Failure{}, false
		}
		return NewFailure(current.Code, current.Message), true
	case map[string]Value:
		code := ""
		message := ""
		if item, ok := current["code"]; ok {
			code = item.String()
		}
		if item, ok := current["message"]; ok {
			message = item.String()
		}
		return NewFailure(code, message), true
	default:
		return Failure{}, false
	}
}
