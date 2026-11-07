package analytics

import "testing"

// TestRegistryCountOfNoColumnHoldsNoBuildBack: a query reading no column (count(*)) starts no build, so the next
// query's columns are built at once, and a count(*) after that reads the copy. On 07/11/2026, shopping's count(*)
// through the port asked for a copy of no column; its build failed ("a copy holds 1..127 columns"), the failure held
// every build of the table back for ten minutes, and GROUP BY brand right after ran on SQLite until it timed out.
//
// CONTROL (07/11/2026): without the guard in startBuild, "a count(*) left a failed build: 2026-11-07 …" and, with the
// failure's mark left out of the check, "no copy of amount after its build: a count(*) before it held the build back".
func TestRegistryCountOfNoColumnHoldsNoBuildBack(t *testing.T) {
	path, _ := ordersFile(t, 3000)
	registry := NewRegistry()
	registry.MinimumRows = 1
	defer registry.Close()
	if table, release := registry.Acquire(path, "orders", nil); table != nil {
		release()
		t.Fatal("a copy of no column before any build")
	}
	registry.Wait()
	registry.mutex.Lock()
	failed := registry.files[path].tables["orders"].failed
	registry.mutex.Unlock()
	if !failed.IsZero() {
		t.Fatalf("a count(*) left a failed build: %v", failed)
	}
	if table, release := registry.Acquire(path, "orders", []string{"amount"}); table != nil {
		release()
		t.Fatal("a copy holding amount before its build")
	}
	registry.Wait()
	table, release := registry.Acquire(path, "orders", []string{"amount"})
	defer release()
	if table == nil {
		t.Fatal("no copy of amount after its build: a count(*) before it held the build back")
	}
	counted, releaseCount := registry.Acquire(path, "orders", nil)
	defer releaseCount()
	if counted == nil {
		t.Fatal("a count(*) does not read the copy built for amount")
	}
}
