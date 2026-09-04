package kitdb

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSnapshotScanKeysStreamsRangeWithoutValues(t *testing.T) {
	database := mustOpen(t, filepath.Join(t.TempDir(), "scan-keys.kitdb"))
	defer database.Close()
	transaction := mustBegin(t, database)
	for index := range 300 {
		key := fmt.Sprintf("row/%04d", index)
		if err := transaction.Put([]byte(key), []byte("value-that-key-only-scan-does-not-copy")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := database.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()

	var keys []string
	stats, err := snapshot.ScanKeys(RangeOptions{
		Start: []byte("row/0100"), End: []byte("row/0110"), Prefix: []byte("row/"),
	}, func(key []byte) (bool, error) {
		keys = append(keys, string(key))
		return false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := make([]string, 10)
	for index := range want {
		want[index] = fmt.Sprintf("row/%04d", 100+index)
	}
	if !reflect.DeepEqual(keys, want) || stats.GenerationEntriesVisited < uint64(len(want)) || stats.PageRecordsDecoded == 0 {
		t.Fatalf("ScanKeys keys=%v stats=%+v", keys, stats)
	}

	sentinel := errors.New("stop")
	_, err = snapshot.ScanKeys(RangeOptions{Prefix: []byte("row/")}, func([]byte) (bool, error) {
		return false, sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("ScanKeys visitor error = %v", err)
	}
	if _, err := snapshot.ScanKeys(RangeOptions{}, nil); err == nil {
		t.Fatal("ScanKeys accepted a nil visitor")
	}
}

func TestSnapshotScanKeysMatchesCursorAcrossGenerationsAndOverlay(t *testing.T) {
	database := mustOpen(t, filepath.Join(t.TempDir(), "scan-key-generations.kitdb"))
	defer database.Close()
	transaction := mustBegin(t, database)
	for index := range 300 {
		key := fmt.Sprintf("row/%04d", index)
		if err := transaction.Put([]byte(key), []byte("base")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Checkpoint(); err != nil {
		t.Fatal(err)
	}

	transaction = mustBegin(t, database)
	for _, key := range []string{"row/0040", "row/0120", "row/0250"} {
		if err := transaction.Put([]byte(key), []byte("updated")); err != nil {
			t.Fatal(err)
		}
	}
	if err := transaction.Delete([]byte("row/0121")); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Put([]byte("row/0121a"), []byte("inserted")); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Checkpoint(); err != nil {
		t.Fatal(err)
	}

	transaction = mustBegin(t, database)
	if err := transaction.Delete([]byte("row/0122")); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Put([]byte("row/0122a"), []byte("overlay")); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}

	snapshot, err := database.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	for _, options := range []RangeOptions{
		{Prefix: []byte("row/")},
		{Start: []byte("row/0118"), End: []byte("row/0126"), Prefix: []byte("row/")},
		{Start: []byte("row/0248"), Prefix: []byte("row/"), Limit: 5},
		{Start: []byte("row/0118"), End: []byte("row/0126"), Prefix: []byte("row/"), Reverse: true},
	} {
		cursor, err := snapshot.Cursor(options)
		if err != nil {
			t.Fatal(err)
		}
		var want []string
		for cursor.Next() {
			want = append(want, string(cursor.Key()))
		}
		if err := cursor.Err(); err != nil {
			cursor.Close()
			t.Fatal(err)
		}
		cursor.Close()

		var got []string
		if _, err := snapshot.ScanKeys(options, func(key []byte) (bool, error) {
			got = append(got, string(key))
			return false, nil
		}); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("ScanKeys options=%+v got=%v want=%v", options, got, want)
		}
	}
}
