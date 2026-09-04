package work

import (
	"strings"
	"testing"
)

func TestKitDBRecordTransactionSavepointCopiesAndBounds(t *testing.T) {
	transaction := &kitDBRecordTransaction{overlay: make(map[string]kitDBRecordMutation)}
	key := []byte("row/a")
	encoded := []byte("one")
	if err := transaction.Put(key, encoded); err != nil {
		t.Fatal(err)
	}
	key[0] = 'X'
	encoded[0] = 'X'
	if got := string(transaction.overlay["row/a"].value); got != "one" {
		t.Fatalf("transaction retained caller memory: %q", got)
	}

	savepoint := transaction.Savepoint()
	if err := transaction.Put([]byte("row/a"), []byte("two")); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Delete([]byte("row/b")); err != nil {
		t.Fatal(err)
	}
	transaction.RollbackTo(savepoint)
	if got := string(transaction.overlay["row/a"].value); got != "one" {
		t.Fatalf("savepoint restored row/a = %q, want one", got)
	}
	if _, found := transaction.overlay["row/b"]; found {
		t.Fatal("savepoint retained a later delete")
	}

	transaction.bytes = kitDBRecordTransactionByteLimit
	before := len(transaction.operations)
	if err := transaction.Put([]byte("row/c"), []byte("value")); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("byte limit error = %v", err)
	}
	if len(transaction.operations) != before {
		t.Fatal("byte-limit refusal retained an operation")
	}

	transaction.bytes = 0
	transaction.operations = make([]kitDBRecordOperation, kitDBRecordTransactionOperationLimit)
	transaction.overlay = make(map[string]kitDBRecordMutation)
	if err := transaction.Delete([]byte("row/d")); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("operation limit error = %v", err)
	}
	if _, found := transaction.overlay["row/d"]; found {
		t.Fatal("operation-limit refusal retained a mutation")
	}
}
