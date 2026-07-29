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
//	if (!check.ok) return ctx.status(503).json({ message: check.error.message })
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
	value Value
	err   map[string]Value // {code, message}, or nil on success
}

// Ok reports that the call succeeded. Named for what a JS author reaches for by reflex: fetch()
// responses have carried .ok for a decade.
func (s *SafeResult) Ok() bool { return s.err == nil }

// IsError is Ok's opposite, kept because the same name means the same thing on a bare error value.
func (s *SafeResult) IsError() bool { return s.err != nil }

// Value is the data — null on a hard failure, since there is none.
func (s *SafeResult) Value() Value { return s.value }

// Error returns {code, message}, or null when nothing failed. It returns a Value rather than a
// string on purpose: a method named Error returning a string would make this type satisfy Go's
// error interface, and it is a result, not an error.
func (s *SafeResult) Error() Value {
	if s.err == nil {
		return Value{K: Nil}
	}
	return New(s.err)
}

// Safe reshapes a value — successful, carrying an attached error, or an outright failure — into one
// SafeResult, so a handler never has to know which of the three it was.
func (v Value) Safe(_ ...Value) Value {
	clean, rawErr, _ := splitInlineError(v)
	out := &SafeResult{value: clean}
	if m, ok := rawErr.(map[string]Value); ok {
		out.err = m
	}
	return New(out)
}

// splitInlineError peels any error off v. Returns the clean data; the raw error (a map[string]Value
// of {code, message}, or nil); and that error as a Value (a map, or null).
func splitInlineError(v Value) (clean Value, rawErr any, errValue Value) {
	errValue = Value{K: Nil}

	// Hard failure: an Invalid value (e.g. db query error). Message is in .V; there is no data.
	if v.K == Invalid {
		msg := "error"
		if s, ok := v.V.(string); ok && s != "" {
			msg = s
		}
		rawErr = map[string]Value{"code": New("ERROR"), "message": New(msg)}
		return Value{K: Nil}, rawErr, New(rawErr)
	}

	// Safe*-style: the data carries an attached error inline.
	if v.IsError && v.ErrorVal != nil {
		rawErr = v.ErrorVal
		errValue = New(v.ErrorVal)
	}

	clean = v
	clean.IsError = false
	clean.ErrorVal = nil
	return clean, rawErr, errValue
}
