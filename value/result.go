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

// Safe reshapes a value into an OBJECT whose `.value` is the data and whose `.error` / `.isError`
// report the failure through the standard accessors (null when there is none). It works whether the
// call succeeded or failed, so no separate "safe" variant of each method is needed:
//
//	const check = database.entity().table("users").first({ email }).safe()
//	if (!check.ok) return ctx.status(503).json({ message: check.error.message })
//	return ctx.json(check.value)
//
// `ok` is carried even though `error` already answers the same question, because a JS author
// reaches for it by reflex — fetch() responses have had .ok for a decade. Without it the habit
// writes `if (!check.ok)`, reads undefined, and takes the failure branch on EVERY call including
// the successful ones. A missing property is falsy, so that bug is silent and looks like the
// database is down.
//
// It is a plain key on the wrapper, not a registered accessor, so unlike `error` it costs nobody
// the word "ok" as a column name.
func (v Value) Safe(_ ...Value) Value {
	clean, rawErr, _ := splitInlineError(v)
	obj := New(map[string]Value{
		"value": clean,
		"ok":    New(rawErr == nil),
	})
	// Carry the error on the WRAPPER so the .error / .isError accessors surface it — a plain "error"
	// map field would be shadowed by the accessor (see navigation.go).
	if rawErr != nil {
		obj.IsError = true
		obj.ErrorVal = rawErr
	}
	return obj
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
