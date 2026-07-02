package value

// Inline-error reshaping — turn a value that carries (or IS) an error into the explicit shapes a
// handler picks apart. Three sources of error are unified here:
//   - a Safe* DB result: the data with IsError/ErrorVal attached;
//   - a hard failure: a K==Invalid value whose message is in .V (e.g. `first()` on a DB error);
//   - success: a plain value, no error.
// So `db.users.first({email}).result()` works whether first() succeeded OR errored — no separate
// safeFirst() needed.

// Result reshapes the value into an OBJECT (a "Result", à la Rust) whose `.value` is the data and
// whose `.error` / `.isError` report the error through the standard accessors (null when none):
//
//	const r = db.users.first({ email }).result()
//	if (r.error) return ctx.status(503).json({ message: r.error.message })
//	return ctx.json(r.value)
func (v Value) Result(_ ...Value) Value {
	clean, rawErr, _ := splitInlineError(v)
	obj := New(map[string]Value{"value": clean})
	// Carry the error on the wrapper so the .error / .isError accessors surface it — a plain "error"
	// map field would be shadowed by the accessor (see navigation.go).
	if rawErr != nil {
		obj.IsError = true
		obj.ErrorVal = rawErr
	}
	return obj
}

// Safe reshapes the same value into a two-element ARRAY [data, error] for destructuring: the data
// (its error flag cleared, or null on a hard failure), then the {code, message} error (or null):
//
//	const [user, error] = db.users.first({ email }).safe()
//	if (error) return ctx.status(503)
//	return ctx.json(user)
func (v Value) Safe(_ ...Value) Value {
	clean, _, errValue := splitInlineError(v)
	return New([]Value{clean, errValue})
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
