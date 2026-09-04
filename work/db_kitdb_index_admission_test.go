package work

import (
	"fmt"
	"path/filepath"
	"testing"

	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/value"
)

func TestKitDBIndexAdmissionIsOneMetadataCommitWithoutRowProgress(t *testing.T) {
	stored := bindStructDef("products", nil, map[string]*ColumnSpec{
		"id":       {kind: "text", primary: true, seq: 1},
		"category": {kind: "text", seq: 2},
	})
	target := bindStructDef("products", nil, map[string]*ColumnSpec{
		"id":       {kind: "text", primary: true, seq: 1},
		"category": {kind: "text", seq: 2, indexes: []colIndexRef{{name: "products_category"}}},
	})
	database, err := kitdbengine.Open(filepath.Join(t.TempDir(), "admission.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := commitKitDBCatalog(database, stored, nil, ""); err != nil {
		t.Fatal(err)
	}
	if ready, err := ensureKitDBSecondaryIndexCodec(database, stored); err != nil || !ready {
		t.Fatalf("initial codec ready=%t err=%v", ready, err)
	}

	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for number := 0; number < kitDBIndexBuildRowLimit+17; number++ {
		row := map[string]value.Value{
			"id":       value.New(fmt.Sprintf("p%05d", number)),
			"category": value.New(fmt.Sprintf("c%d", number%7)),
		}
		rowKey, err := kitDBRowKey(stored, row["id"])
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := encodeKitDBValidatedRow(stored, row, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Put(rowKey, encoded); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	before, err := database.LastTransaction()
	if err != nil {
		t.Fatal(err)
	}
	indexes := collectIndexes(target.Name, target.columns)
	admitted, err := admitKitDBIndexBuild(
		database, stored, target, kitDBIndexBuildSchema, indexes, nil,
	)
	if err != nil || !admitted {
		t.Fatalf("index admission admitted=%t err=%v", admitted, err)
	}
	after, err := database.LastTransaction()
	if err != nil || after != before+1 {
		t.Fatalf("index admission transaction %d -> %d err=%v", before, after, err)
	}
	state, found, err := loadKitDBIndexBuildState(database, target, kitDBIndexBuildSchema)
	if err != nil || !found || state.Rows != 0 || len(state.Progress) != 0 {
		t.Fatalf("index admission state=%#v found=%t err=%v", state, found, err)
	}
	statuses, err := loadKitDBIndexBuildStatuses(database, target)
	if err != nil || len(statuses) != 1 || statuses[0].Mode != "schema" ||
		statuses[0].Phase != "building" || statuses[0].ProcessedRows != 0 || statuses[0].HasCursor ||
		statuses[0].StartedTransaction != after || statuses[0].TargetPublished {
		t.Fatalf("index admission status=%#v err=%v", statuses, err)
	}

	admitted, err = admitKitDBIndexBuild(
		database, stored, target, kitDBIndexBuildSchema, indexes, nil,
	)
	if err != nil || admitted {
		t.Fatalf("idempotent admission admitted=%t err=%v", admitted, err)
	}
	if current, err := database.LastTransaction(); err != nil || current != after {
		t.Fatalf("idempotent admission transaction=%d want=%d err=%v", current, after, err)
	}
	if !advanceKitDBSecondaryIndexForTest(t, database) {
		t.Fatal("first worker chunk unexpectedly completed the large index")
	}
	state, found, err = loadKitDBIndexBuildState(database, target, kitDBIndexBuildSchema)
	if err != nil || !found || state.Rows != kitDBIndexBuildRowLimit || len(state.Progress) == 0 {
		t.Fatalf("first worker state=%#v found=%t err=%v", state, found, err)
	}
}
