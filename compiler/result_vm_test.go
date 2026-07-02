package compiler

import "testing"

// .result() and .safe() must dispatch through the VM as receiver-bound prototype methods and produce
// the array / object shapes. The error-path reshaping is unit-tested in value/result_test.go; here we
// prove the VM plumbing (dispatch + receiver binding + destructure) on the success path.

func TestResultMethodVMDispatch(t *testing.T) {
	got := runResult(t, `
		const u = { id: 7, name: "ann" }
		const r = u.result()
		result = r.value.id
	`)
	wantNum(t, got, 7, ".result() → { value }, value read")
}

func TestSafeMethodVMDispatch(t *testing.T) {
	got := runResult(t, `
		const u = { id: 9 }
		const [data, err] = u.safe()
		result = data.id
	`)
	wantNum(t, got, 9, ".safe() → [data, error], data destructured")
}
