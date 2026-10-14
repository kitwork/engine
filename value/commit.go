package value

// CommitResult lets a committed effect ask the VM to run one continuation after the host-side
// operation completes. A zero result means that committing the effect requires no VM callback.
// Since .then and .catch went (14/10/2026) no committer of the engine sets Handler: the continuation
// stays part of VM v2's COMMIT contract (runtime/failure_boundary_test.go keeps it tested), for an
// effect that needs one; removing it is a change of that contract, not a cleanup.
//
// Failure, when it is a failure (IsError), is what the effect ended in: the COMMIT fails with it, as
// any instruction that leaves a failure does, so safe(() => …) catches it (an HTTP request that got no
// answer, 14/10/2026). A failing effect runs no continuation.
type CommitResult struct {
	Handler  *Lambda
	Argument Value
	Failure  Value
}

// Committer is implemented by values that collect work while an expression is being evaluated and
// perform that work only after the complete expression has been configured. COMMIT is a language
// boundary, not an HTTP instruction: the VM knows only this contract.
type Committer interface {
	Commit() CommitResult
}
