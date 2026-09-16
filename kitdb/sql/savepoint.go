package sql

// SavepointStatement runs only inside an explicitly owned data transaction.
type SavepointStatement struct {
	Action string
	Name   string
}
