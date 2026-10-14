package value

// safe(() => x) is how a program reads a failure (14/10/2026): it calls x's function with no arguments
// and always gives a plain object,
//
//	{ value: <what the function returned>, error: null }                      it finished
//	{ value: null, error: { message, code, at } }                             it failed, anywhere inside
//
// with error.progress as well for a PARTIAL failure (a bulk write that stopped midway). The object is
// an ordinary map: ctx.json(found) writes it, a .ui page reads it, const { value, error } = … takes it
// apart. It replaced x.safe(), whose result was a Go struct (SafeResult) that JSON wrote as null and a
// view dropped; x.safe() is now a compile error that says how to write it.
//
// Failures a host attached to data without stopping the program (database.entity() returning [] with
// DATABASE_ERROR, createMany returning its progress with KITDB_CREATE_MANY_PARTIAL) are gone with it:
// they are failures like any other now, DATABASE and PARTIAL, so a page that forgets safe answers 500
// and logs it rather than showing an empty list while the database is down.

// SafeFunction is the global safe. It has no Go body: the function it is given runs on the VM that
// called safe, in a frame of its own, on the same energy budget and the same rules for what stops the
// run, so the VM runs it (runtime/safe.go). A host handed the value has no VM to lend it (Call).
type SafeFunction struct{}

// NewSafeFunction is the value of the global safe, which the "kitwork" module also exports.
func NewSafeFunction() Value { return Value{K: Func, V: SafeFunction{}} }

// IsSafeFunction reports whether v is the global safe.
func IsSafeFunction(v Value) bool {
	_, ok := v.V.(SafeFunction)
	return ok && v.K == Func
}
