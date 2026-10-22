package work

import (
	"testing"
)

func TestDatabaseIntegration(t *testing.T) {
	kw := &KitWork{tenant: &Tenant{}}
	db := kw.legacyDatabase()
	if db == nil {
		t.Fatal("Expected Database adapter, got nil")
	}
}
