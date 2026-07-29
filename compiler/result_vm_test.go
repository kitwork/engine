package compiler

import "testing"

// .result() and .safe() must dispatch through the VM as receiver-bound prototype methods and produce
// the array / object shapes. The error-path reshaping is unit-tested in value/result_test.go; here we
// prove the VM plumbing (dispatch + receiver binding + destructure) on the success path.

// safe() is the single inline-error shape: an object with .value and .error. The array form it
// replaced is gone, so the destructuring spelling this test used to check goes with it.
func TestSafeMethodVMDispatch(t *testing.T) {
	got := runResult(t, `
		const u = { id: 7, name: "ann" }
		const r = u.safe()
		result = r.value.id
	`)
	wantNum(t, got, 7, ".safe() → { value }, value read")
}

// NOTE — there is no VM test for the ERROR side, and writing one exposed something worth recording.
//
// At the value layer, Value{K: Invalid}.Safe() rescues correctly (see value/result_test.go). From
// the VM it does not: `fail("boom").safe()` yields an object whose .error reads null, because the
// VM propagates an Invalid value before the method on it is reached. navigation.go documents .safe()
// as the door through an Invalid, and at the Go layer it is — but that door is not reachable from
// JS today.
//
// The same cause rules out a standalone safe(x): the argument is evaluated first, so an Invalid
// never arrives at the function. Making either work needs the VM to treat specific builtins as
// error-transparent, which is a change to evaluation, not to this package.
