package kitdb

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func TestSnapshotOverlayValueOwnership(t *testing.T) {
	for _, recoverWAL := range []bool{false, true} {
		t.Run(fmt.Sprintf("recovered_%t", recoverWAL), func(t *testing.T) {
			testSnapshotOverlayValueOwnership(t, recoverWAL)
		})
	}
}

func testSnapshotOverlayValueOwnership(t *testing.T, recoverWAL bool) {
	db := memoryDB(t, OpenOptions{})
	key, value := []byte("kept"), bytes.Repeat([]byte{'a'}, 4096)
	tx := mustBegin(t, db)
	if err := tx.Put(key, value); err != nil {
		t.Fatal(err)
	}
	key[0], value[0] = 'x', 'x'
	if err := tx.Put([]byte("removed"), []byte("old")); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if recoverWAL {
		path := db.Path()
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		db = mustOpen(t, path)
		defer db.Close()
	}
	first, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	check := func(snapshot *Snapshot) {
		t.Helper()
		got, found, err := snapshot.Get([]byte("kept"))
		if err != nil || !found || !bytes.Equal(got, bytes.Repeat([]byte{'a'}, 4096)) {
			t.Fatalf("snapshot value length=%d found=%v error=%v", len(got), found, err)
		}
		got[0] = 'z'
		cursor, err := snapshot.Cursor(RangeOptions{Prefix: []byte("kept")})
		if err != nil {
			t.Fatal(err)
		}
		if !cursor.Next() {
			t.Fatalf("cursor: %v", cursor.Err())
		}
		copied := cursor.Value()
		copied[0] = 'z'
		_ = cursor.Close()
		_, err = snapshot.Scan(RangeOptions{Prefix: []byte("kept")}, func(_, borrowed []byte) (bool, error) {
			if len(borrowed) != 4096 || borrowed[0] != 'a' {
				return false, fmt.Errorf("caller-owned reads changed snapshot storage")
			}
			return false, nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	check(first)
	check(second)
	if got := requireValue(t, db, "kept"); got[0] != 'a' {
		t.Fatal("caller changed live overlay")
	}
	tx = mustBegin(t, db)
	if err := tx.Put([]byte("kept"), []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Delete([]byte("removed")); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	deleted, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer deleted.Close()
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	memoryPut(t, db, "removed", []byte("new"))
	if _, found, err := deleted.Get([]byte("removed")); err != nil || found {
		t.Fatalf("captured tombstone changed after reinsertion: %v %v", found, err)
	}
	if err := deleted.Close(); err != nil {
		t.Fatal(err)
	}
	check(first)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	check(second)
	if got, found, err := second.Get([]byte("removed")); err != nil || !found || string(got) != "old" {
		t.Fatalf("old deletion: %q %v %v", got, found, err)
	}
	if got := string(requireValue(t, db, "kept")); got != "replacement" {
		t.Fatalf("live value=%q", got)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	stats, err := db.Stats()
	if err != nil || stats.SnapshotBytes != 0 || stats.ActiveSnapshots != 0 {
		t.Fatalf("snapshot ownership did not drain: %+v %v", stats, err)
	}
}

func TestSnapshotOverlayConcurrentDatabases(t *testing.T) {
	const databases, writes = 4, 32
	var workers sync.WaitGroup
	failures := make(chan error, databases*2)
	start := make(chan struct{})
	var opened []*DB
	var captured []*Snapshot
	for tenant := range databases {
		db := mustOpen(t, filepath.Join(t.TempDir(), "tenant.kitdb"))
		defer db.Close()
		original := bytes.Repeat([]byte{byte('a' + tenant)}, 4096)
		memoryPut(t, db, "key", original)
		snapshot, err := db.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		defer snapshot.Close()
		opened = append(opened, db)
		captured = append(captured, snapshot)
		workers.Add(2)
		go func() {
			defer workers.Done()
			<-start
			for i := range writes {
				tx, err := db.Begin()
				if err != nil {
					failures <- err
					return
				}
				if err := tx.Put([]byte("key"), bytes.Repeat([]byte{byte(i)}, 128+i)); err != nil {
					_ = tx.Rollback()
					failures <- err
					return
				}
				if _, err := tx.Commit(); err != nil {
					failures <- err
					return
				}
				if i%8 == 7 {
					if _, err := db.Checkpoint(); err != nil {
						failures <- err
						return
					}
				}
			}
		}()
		go func() {
			defer workers.Done()
			<-start
			for range 256 {
				got, found, err := snapshot.Get([]byte("key"))
				if err != nil || !found || !bytes.Equal(got, original) {
					failures <- fmt.Errorf("tenant %d snapshot changed: found=%v error=%v", tenant, found, err)
					return
				}
			}
		}()
	}
	close(start)
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	for tenant, db := range opened {
		original := bytes.Repeat([]byte{byte('a' + tenant)}, 4096)
		got, found, err := captured[tenant].Get([]byte("key"))
		if err != nil || !found || !bytes.Equal(got, original) {
			t.Fatalf("tenant %d old value after all writes: %v %v", tenant, found, err)
		}
		if err := captured[tenant].Close(); err != nil {
			t.Fatal(err)
		}
		if err := db.Verify(); err != nil {
			t.Fatal(err)
		}
		path := db.Path()
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		reopened := mustOpen(t, path)
		want := bytes.Repeat([]byte{writes - 1}, 128+writes-1)
		if got := requireValue(t, reopened, "key"); !bytes.Equal(got, want) {
			t.Fatalf("tenant %d reopened value changed", tenant)
		}
		if err := reopened.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSnapshotOverlayCaptureAllocationEnvelope(t *testing.T) {
	db := memoryDB(t, OpenOptions{})
	tx := mustBegin(t, db)
	for i := range 2048 {
		if err := tx.Put([]byte(fmt.Sprintf("row/%08d", i)), bytes.Repeat([]byte{'v'}, 4096)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	allocations := testing.AllocsPerRun(3, func() {
		snapshot, err := db.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		if err := snapshot.Close(); err != nil {
			t.Fatal(err)
		}
	})
	if allocations > 100 {
		t.Fatalf("snapshot copies immutable values: %.0f allocations (limit 100)", allocations)
	}
	t.Logf("2048-entry overlay capture allocations: %.0f (limit 100)", allocations)
}
