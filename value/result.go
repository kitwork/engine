package value

// Inline-error handling — turn a value that carries (or IS) an error into one explicit shape a
// handler picks apart. Three sources are unified:
//   - a value with IsError/ErrorVal attached;
//   - a hard failure: a K==Invalid value whose message is in .V (a failed query, fail("…"));
//   - success: a plain value, no error.
//
// ONE shape, deliberately. There were two — an object and a two-element array for destructuring —
// and they were confusing enough that the dispatch table's own comments ended up describing each
// other. Two spellings of one idea is a tax on every reader, and the array form additionally
// depended on destructuring, which this language only accepts after `const`.

// SafeResult is what safe() hands back: the data, plus whether it arrived.
//
//	const check = database.entity().table("users").where("email", e).first().safe()
//	if (!check.ok) return ctx.status(503).json({ message: check.error })
//	return ctx.json(check.value)
//
// A STRUCT rather than a map, and that choice is what makes `.ok` read correctly without parens.
// Property access on a Go value reaches these methods through reflection, and a method taking no
// arguments is INVOKED rather than returned (the getter pattern in navigation.go) — so `check.ok`
// is a real boolean. A method registered on the Kind table instead would hand back a function
// value, and a function is truthy, so `if (!check.ok)` would be false forever and a genuine
// failure would be skipped in silence.
//
// The other half of the choice: these methods belong to THIS type. Kind methods are global, so
// naming one "ok" would shadow a column called ok in everyone's data — the way `error` already
// shadows one called error on every map in the system.
type SafeResult struct {
	value   Value
	failure *Failure
}

// Ok reports that the call succeeded. Named for what a JS author reaches for by reflex: fetch()
// responses have carried .ok for a decade.
func (s *SafeResult) Ok() bool { return s.failure == nil }

// IsError is Ok's opposite, kept because the same name means the same thing on a bare error value.
func (s *SafeResult) IsError() bool { return s.failure != nil }

// Value is the data — null on a hard failure, since there is none.
func (s *SafeResult) Value() Value { return s.value }

// Error is the MESSAGE, or null when nothing failed — not a {code, message} object, so it drops
// straight into a response without a second hop:
//
//	if (!check.ok) return ctx.status(503).json({ message: check.error })
//
// Nesting the object there produced {"message":{"code":…,"message":…}}, which reads as a bug in the
// handler rather than a description of the failure. The code is still reachable as .code for the
// callers that branch on it, which are far fewer than the ones that just want to say what happened.
//
// It returns a Value rather than a string deliberately: a method named Error returning a string
// would make this type satisfy Go's error interface, and it is a result, not an error.
func (s *SafeResult) Error() Value {
	if s.failure == nil {
		return Value{K: Nil}
	}
	return New(s.failure.Message)
}

// Code is the machine-readable half, for a handler that branches on the kind of failure rather
// than reporting it.
func (s *SafeResult) Code() Value {
	if s.failure == nil {
		return Value{K: Nil}
	}
	return New(s.failure.Code)
}

// Safe reshapes a value — successful, carrying an attached error, or an outright failure — into one
// SafeResult, so a handler never has to know which of the three it was.
func (v Value) Safe(_ ...Value) Value {
	clean, failure := splitInlineError(v)
	return New(&SafeResult{value: clean, failure: failure})
}

// splitInlineError peels the typed application failure off v and returns clean
// data plus an immutable failure envelope.
func splitInlineError(v Value) (clean Value, failure *Failure) {
	// Hard failure: an Invalid value (e.g. db query error). Message is in .V; there is no data.
	if v.K == Invalid {
		if attached, ok := FailureFrom(v); ok {
			return Value{K: Nil}, &attached
		}
		msg := "error"
		if s, ok := v.V.(string); ok && s != "" {
			msg = s
		}
		fallback := NewFailure(DefaultFailureCode, msg)
		return Value{K: Nil}, &fallback
	}

	// Safe*-style: the data carries an attached error inline.
	if attached, ok := FailureFrom(v); ok {
		failure = &attached
	}

	clean = v
	clean.IsError = false
	clean.ErrorVal = nil
	return clean, failure
}
