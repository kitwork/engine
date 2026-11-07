package analytics

import "testing"

// TestRegistryBuildsWhatIsDeclared: a table's copy holds exactly the columns the database declares for it
// (kitwork_columnar, KITWORK_KITDB.md câu 13.11 (c)). Asked with none, the registry builds nothing and leaves no failed
// build behind (on 07/11/2026 a count(*) through engine2's port asked for a copy of no column, its build failed, and the
// failure held every build of shopping back for ten minutes); asked with columns, it builds a copy of those; a
// declaration that grows or narrows builds the copy again, and the copy before is not given meanwhile.
//
// CONTROL (07/11/2026): without the guard of no column in startBuild, "asked with no column: a failed build: …"; with
// a copy given when it holds the declared columns among others, "narrowed to amount: a copy given before its build".
func TestRegistryBuildsWhatIsDeclared(t *testing.T) {
	path, _ := ordersFile(t, 3000)
	registry := NewRegistry()
	registry.MinimumRows = 1
	defer registry.Close()
	if table, release := registry.Acquire(path, "orders", nil); table != nil {
		release()
		t.Fatal("a copy of no column")
	}
	registry.Wait()
	registry.mutex.Lock()
	failed := registry.files[path].tables["orders"].failed
	registry.mutex.Unlock()
	if !failed.IsZero() {
		t.Fatalf("asked with no column: a failed build: %v", failed)
	}
	declared := func(label string, columns ...string) {
		t.Helper()
		if table, release := registry.Acquire(path, "orders", columns); table != nil {
			release()
			t.Fatalf("%s: a copy given before its build", label)
		}
		registry.Wait()
		table, release := registry.Acquire(path, "orders", columns)
		if table == nil {
			t.Fatalf("%s: no copy after its build", label)
		}
		defer release()
		if got := len(table.Manifest().Columns); got != len(columns) {
			t.Fatalf("%s: the copy holds %d columns", label, got)
		}
		if !holdsAll(table.Manifest(), columns) {
			t.Fatalf("%s: the copy holds %v", label, table.Manifest().Columns)
		}
	}
	declared("amount", "amount")
	declared("grown to amount, price", "amount", "price")
	declared("narrowed to amount", "amount")
}
