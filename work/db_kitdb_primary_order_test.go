package work

import (
	"bytes"
	"fmt"
	"path/filepath"
	"testing"

	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
)

func TestKitDBImplicitPrimaryOrderUpgradesV2AndResumesAfterRestart(t *testing.T) {
	definition := bindStructDef("products", nil, map[string]*ColumnSpec{
		"id":    {kind: "text", primary: true, seq: 1},
		"title": {kind: "text", seq: 2},
	})
	path := filepath.Join(t.TempDir(), "primary-order-v2.kitdb")
	database, err := kitdbengine.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := commitKitDBCatalog(database, definition, nil, ""); err != nil {
		t.Fatal(err)
	}
	markerKey, err := kitDBSecondaryIndexCodecMarkerKey(definition)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Put(markerKey, kitDBSecondaryIndexCodecMarkerV2); err != nil {
		t.Fatal(err)
	}
	for number := 0; number < kitDBIndexBuildRowLimit+17; number++ {
		row := map[string]value.Value{
			"id": value.New(fmt.Sprintf("p%05d", number)), "title": value.New("Product"),
		}
		rowKey, keyErr := kitDBRowKey(definition, row["id"])
		if keyErr != nil {
			t.Fatal(keyErr)
		}
		encoded, encodeErr := encodeKitDBValidatedRow(definition, row, nil)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		if putErr := tx.Put(rowKey, encoded); putErr != nil {
			t.Fatal(putErr)
		}
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	ready, err := ensureKitDBSecondaryIndexCodec(database, definition)
	if err != nil || ready {
		t.Fatalf("v3 admission ready=%t err=%v", ready, err)
	}
	state, found, err := loadKitDBIndexBuildState(database, definition, kitDBIndexBuildCodec)
	if err != nil || !found || state.Phase != kitDBIndexBuildPhaseRows || len(state.Indexes) != 1 {
		t.Fatalf("v3 build state=%#v found=%t err=%v", state, found, err)
	}
	marker, found, err := database.Get(markerKey)
	if err != nil || !found || !bytes.Equal(marker, kitDBSecondaryIndexCodecMarkerV2) {
		t.Fatalf("marker before cutover=%x found=%t err=%v", marker, found, err)
	}
	assertKitDBPrimaryOrderAccess(t, database, definition, kitDBAccessScan)

	if !advanceKitDBSecondaryIndexForTest(t, database) {
		t.Fatal("primary-order build completed before its final chunk")
	}
	state, found, err = loadKitDBIndexBuildState(database, definition, kitDBIndexBuildCodec)
	if err != nil || !found || state.Rows != kitDBIndexBuildRowLimit || len(state.Progress) == 0 {
		t.Fatalf("first primary-order chunk=%#v found=%t err=%v", state, found, err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = kitdbengine.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	assertKitDBPrimaryOrderAccess(t, database, definition, kitDBAccessScan)
	completeKitDBSecondaryIndexForTest(t, database)
	marker, found, err = database.Get(markerKey)
	if err != nil || !found || !bytes.Equal(marker, kitDBSecondaryIndexCodecMarker) {
		t.Fatalf("marker after cutover=%x found=%t err=%v", marker, found, err)
	}
	if _, found, err := loadKitDBIndexBuildState(database, definition, kitDBIndexBuildCodec); err != nil || found {
		t.Fatalf("completed primary-order state found=%t err=%v", found, err)
	}
	assertKitDBPrimaryOrderAccess(t, database, definition, kitDBAccessIndexOrder)
	implicit := kitDBImplicitPrimaryIndexes(definition)
	if len(implicit) != 1 {
		t.Fatalf("implicit primary indexes=%#v", implicit)
	}
	assertKitDBIndexMatchesRows(t, database, definition, implicit[0])
}

func TestKitDBExplicitPrimaryLeadingIndexAvoidsDuplicatePhysicalOrderPath(t *testing.T) {
	definition := bindStructDef("shopping", nil, map[string]*ColumnSpec{
		"_key": {
			kind: "text", primary: true, seq: 1,
			indexes: []colIndexRef{{name: "shopping_key_order_idx"}},
		},
		"name": {kind: "text", seq: 2},
	})
	indexes := collectKitDBIndexes(definition)
	if len(indexes) != 1 || indexes[0].name != "shopping_key_order_idx" || indexes[0].implicitPrimary {
		t.Fatalf("physical indexes=%#v", indexes)
	}
	if implicit := kitDBImplicitPrimaryIndexes(definition); len(implicit) != 0 {
		t.Fatalf("duplicate implicit primary indexes=%#v", implicit)
	}
}

func assertKitDBPrimaryOrderAccess(
	t *testing.T,
	database *kitdbengine.DB,
	definition *StructDef,
	want kitDBAccessKind,
) {
	t.Helper()
	inactive, err := loadKitDBInactiveIndexes(database, definition)
	if err != nil {
		t.Fatal(err)
	}
	generations, _, _, err := loadKitDBIndexLayout(database, definition)
	if err != nil {
		t.Fatal(err)
	}
	access, err := (&SchemaTable{
		table: definition.Name, columns: definition.columns, definition: definition,
		inactiveIndexes: inactive, indexGenerations: generations,
	}).planKitDBAccess(query.ExecutionPlan{Orders: []query.OrderQuery{{Column: "id", Direction: "asc"}}})
	if err != nil || access.kind != want {
		t.Fatalf("primary order access=%#v err=%v want=%s", access, err, want)
	}
	if want == kitDBAccessIndexOrder &&
		(access.name != "products_pkey" || !access.orderCovered || access.options.Reverse) {
		t.Fatalf("published primary order access=%#v", access)
	}
}
