package relational

import (
	"context"
	"testing"
	"time"
)

func TestRelationalAdmissionDoesNotHoldCommitGate(t *testing.T) {
	engine := dependencyValidationEngine(t)
	ctx := context.Background()
	writer := concurrencyBegin(t, engine)
	if _, err := writer.Execute(ctx, "UPDATE sink SET value=1 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	for range 31 {
		tx := concurrencyBegin(t, engine)
		defer tx.Rollback()
	}
	result := make(chan error, 1)
	waitCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	go func() {
		tx, err := engine.BeginTransaction(waitCtx, TransactionOptions{ReadOnly: true})
		if tx != nil {
			_ = tx.Rollback()
		}
		result <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		stats, err := engine.database.Stats()
		if err != nil {
			t.Fatal(err)
		}
		if stats.SnapshotWaiters == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("query did not queue")
		}
		time.Sleep(time.Millisecond)
	}
	commit := make(chan error, 1)
	go func() { _, err := writer.Commit(ctx); commit <- err }()
	select {
	case err := <-commit:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("snapshot waiter blocked commit")
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}
