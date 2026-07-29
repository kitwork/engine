package value

// CommitResult lets a committed effect ask the VM to run one continuation after the host-side
// operation completes. A zero result means that committing the effect requires no VM callback.
type CommitResult struct {
	Handler  *Lambda
	Argument Value
}

// Committer is implemented by values that collect work while an expression is being evaluated and
// perform that work only after the complete expression has been configured. COMMIT is a language
// boundary, not an HTTP instruction: the VM knows only this contract.
type Committer interface {
	Commit() CommitResult
}
