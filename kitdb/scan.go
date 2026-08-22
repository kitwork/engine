package kitdb

import "fmt"

// Walk visits the current visible key/value snapshot in raw-key order.
// Callers must not retain the byte slices after the callback returns.
func (db *DB) Walk(emit func(key, value []byte) error) error {
	if emit == nil {
		return fmt.Errorf("kitdb: nil walk callback")
	}
	db.mu.RLock()
	defer db.mu.RUnlock()
	if err := db.stateErrorLocked(); err != nil {
		return err
	}
	return walkMergedRows(db.main, db.overlay, emit)
}
