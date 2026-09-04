package work

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
)

func TestKitDBIndexCodecBuildResumesBeyondAtomicMigrationLimit(t *testing.T) {
	definition := bindStructDef("items", nil, map[string]*ColumnSpec{
		"id":   {kind: "text", primary: true, seq: 1},
		"code": {kind: "text", seq: 2, indexes: []colIndexRef{{}}},
	})
	path := filepath.Join(t.TempDir(), "codec-resume.kitdb")
	database, err := kitdbengine.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := commitKitDBCatalog(database, definition, nil, ""); err != nil {
		t.Fatal(err)
	}

	const total = kitDBAtomicMigrationRows + 17
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	index := collectIndexes(definition.Name, definition.columns)[0]
	for number := 0; number < total; number++ {
		row := map[string]value.Value{
			"id": value.New(fmt.Sprintf("p%05d", number)), "code": value.New(fmt.Sprintf("c%03d", number%127)),
		}
		rowKey, err := kitDBRowKey(definition, row["id"])
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := encodeKitDBValidatedRow(definition, row, nil)
		if err != nil {
			t.Fatal(err)
		}
		legacyKey, err := legacyKitDBSecondaryIndexKey(definition, index, row)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Put(rowKey, encoded); err != nil {
			t.Fatal(err)
		}
		if err := tx.Put(legacyKey, rowKey); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestKitDBIndexLifecycleCrashHelper$", "-test.count=1")
	command.Env = append(os.Environ(), "KITDB_INDEX_CRASH_HELPER="+path)
	output, err := command.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != kitDBIndexCrashExitCode {
		t.Fatalf("crash helper err=%v output=%s", err, output)
	}
	database, err = kitdbengine.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	state, found, err := loadKitDBIndexBuildState(database, definition, kitDBIndexBuildCodec)
	if err != nil || !found {
		t.Fatalf("codec state found=%t err=%v", found, err)
	}
	if state.Phase != kitDBIndexBuildPhaseRows || state.Rows != 0 || len(state.Progress) != 0 {
		t.Fatalf("codec admission = %#v", state)
	}
	statuses, err := loadKitDBIndexBuildStatuses(database, definition)
	if err != nil || len(statuses) != 1 || statuses[0].Mode != "codec" ||
		statuses[0].Phase != "building" || statuses[0].ProcessedRows != 0 || statuses[0].HasCursor {
		t.Fatalf("codec admission status=%#v err=%v", statuses, err)
	}
	if !advanceKitDBSecondaryIndexForTest(t, database) {
		t.Fatal("codec worker completed unexpectedly in its first large-table chunk")
	}
	state, found, err = loadKitDBIndexBuildState(database, definition, kitDBIndexBuildCodec)
	if err != nil || !found || state.Rows != kitDBIndexBuildRowLimit || len(state.Progress) == 0 {
		t.Fatalf("codec worker progress=%#v found=%t err=%v", state, found, err)
	}
	inactive, err := loadKitDBInactiveIndexes(database, definition)
	if err != nil {
		t.Fatal(err)
	}
	access, err := (&SchemaTable{
		table: definition.Name, columns: definition.columns, definition: definition, inactiveIndexes: inactive,
	}).planKitDBAccess(query.ExecutionPlan{Conditions: []query.Condition{{
		Column: "code", Operator: "=", Value: "c001", Logic: "AND",
	}}})
	if err != nil || access.kind != kitDBAccessScan {
		t.Fatalf("building codec access = %#v, err=%v", access, err)
	}
	completeKitDBSecondaryIndexForTest(t, database)
	if _, building, err := loadKitDBIndexBuildState(database, definition, kitDBIndexBuildCodec); err != nil || building {
		t.Fatalf("codec build still present=%t err=%v", building, err)
	}
	markerKey, _ := kitDBSecondaryIndexCodecMarkerKey(definition)
	marker, found, err := database.Get(markerKey)
	if err != nil || !found || !bytes.Equal(marker, kitDBSecondaryIndexCodecMarker) {
		t.Fatalf("codec marker=%x found=%t err=%v", marker, found, err)
	}
	legacy := countKitDBLegacyIndexKeys(t, database, definition)
	if legacy != 0 {
		t.Fatalf("legacy index keys after resumable cleanup = %d", legacy)
	}
	for _, number := range []int{0, total / 2, total - 1} {
		row := map[string]value.Value{
			"id": value.New(fmt.Sprintf("p%05d", number)), "code": value.New(fmt.Sprintf("c%03d", number%127)),
		}
		rowKey, _ := kitDBRowKey(definition, row["id"])
		entries, err := kitDBSecondaryIndexEntries(definition, row, rowKey)
		if err != nil || len(entries) != 1 {
			t.Fatalf("v2 entries=%#v err=%v", entries, err)
		}
		owner, found, err := database.Get(entries[0].key)
		if err != nil || !found || !bytes.Equal(owner, rowKey) {
			t.Fatalf("v2 owner=%x found=%t err=%v", owner, found, err)
		}
	}
	inactive, err = loadKitDBInactiveIndexes(database, definition)
	if err != nil {
		t.Fatal(err)
	}
	access, err = (&SchemaTable{
		table: definition.Name, columns: definition.columns, definition: definition, inactiveIndexes: inactive,
	}).planKitDBAccess(query.ExecutionPlan{Conditions: []query.Condition{{
		Column: "code", Operator: "=", Value: "c001", Logic: "AND",
	}}})
	if err != nil || access.kind != kitDBAccessIndex {
		t.Fatalf("published codec access = %#v, err=%v", access, err)
	}
	before, err := database.LastTransaction()
	if err != nil {
		t.Fatal(err)
	}
	if ready, err := ensureKitDBSecondaryIndexCodec(database, definition); err != nil || !ready {
		t.Fatalf("idempotent codec ready=%t err=%v", ready, err)
	}
	if after, err := database.LastTransaction(); err != nil || after != before {
		t.Fatalf("idempotent codec advanced %d -> %d, err=%v", before, after, err)
	}
}

const kitDBIndexCrashExitCode = 73

func TestKitDBIndexLifecycleCrashHelper(t *testing.T) {
	path := os.Getenv("KITDB_INDEX_CRASH_HELPER")
	if path == "" {
		return
	}
	definition := bindStructDef("items", nil, map[string]*ColumnSpec{
		"id":   {kind: "text", primary: true, seq: 1},
		"code": {kind: "text", seq: 2, indexes: []colIndexRef{{}}},
	})
	database, err := kitdbengine.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(74)
	}
	ready, err := ensureKitDBSecondaryIndexCodec(database, definition)
	if err != nil || ready {
		fmt.Fprintf(os.Stderr, "ready=%t err=%v\n", ready, err)
		os.Exit(75)
	}
	// Deliberately skip DB.Close and every test defer. The parent must recover
	// the synced admission intent from a freshly opened process.
	os.Exit(kitDBIndexCrashExitCode)
}

func TestKitDBAdditiveIndexBuildSurvivesCrashAndInterleavedWrites(t *testing.T) {
	stored := bindStructDef("products", nil, map[string]*ColumnSpec{
		"id":       {kind: "text", primary: true, seq: 1},
		"category": {kind: "text", seq: 2},
		"price":    {kind: "integer", seq: 3},
	})
	target := bindStructDef("products", nil, map[string]*ColumnSpec{
		"id":       {kind: "text", primary: true, seq: 1},
		"category": {kind: "text", seq: 2, indexes: []colIndexRef{{}}},
		"price":    {kind: "integer", seq: 3},
	})
	path := filepath.Join(t.TempDir(), "schema-resume.kitdb")
	database, err := kitdbengine.Open(path)
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

	const total = kitDBIndexBuildRowLimit + 503
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for number := 0; number < total; number++ {
		row := lifecycleProduct(number)
		rowKey, _ := kitDBRowKey(stored, row["id"])
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
	definitions := map[string]*StructDef{target.Name: target}
	if err := ensureKitDBCatalog(database, target, definitions, true, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	entry, found, err := database.CatalogStructByID(stored.ID)
	if err != nil || !found || entry.Hash != stored.Hash {
		t.Fatalf("catalog published too early: hash=%s found=%t err=%v", entry.Hash, found, err)
	}
	state, found, err := loadKitDBIndexBuildState(database, target, kitDBIndexBuildSchema)
	if err != nil || !found || state.Rows != 0 || len(state.Progress) != 0 {
		t.Fatalf("schema build state=%#v found=%t err=%v", state, found, err)
	}
	plan, err := planKitDBCatalog(database, target, false)
	if err != nil || len(plan) != 1 || plan[0].Action != "index_build" || !plan[0].WillApply {
		t.Fatalf("schema build plan=%#v err=%v", plan, err)
	}
	if pending, found, err := kitDBPendingIndexDefinition(database, stored); err != nil || !found || pending.Hash != target.Hash {
		t.Fatalf("pending target=%#v found=%t err=%v", pending, found, err)
	}
	if err := validateKitDBWriteDefinition(database, stored); err == nil || !strings.Contains(err.Error(), "stale schema") {
		t.Fatalf("stale source write error=%v", err)
	}
	if err := validateKitDBWriteDefinition(database, target); err != nil {
		t.Fatalf("target write admission=%v", err)
	}
	inactive, err := loadKitDBInactiveIndexes(database, target)
	if err != nil {
		t.Fatal(err)
	}
	access, err := lifecycleCategoryAccess(target, inactive)
	if err != nil || access.kind != kitDBAccessScan {
		t.Fatalf("building schema access=%#v err=%v", access, err)
	}

	changed := bindStructDef("products", nil, map[string]*ColumnSpec{
		"id":       {kind: "text", primary: true, seq: 1},
		"category": {kind: "text", seq: 2, indexes: []colIndexRef{{}}},
		"price":    {kind: "integer", seq: 3, indexes: []colIndexRef{{}}},
	})
	err = ensureKitDBCatalog(database, changed, map[string]*StructDef{changed.Name: changed}, true, func() error { return nil })
	if err == nil || (!strings.Contains(err.Error(), "finish its pending index build") &&
		!strings.Contains(err.Error(), "different resumable index build")) {
		t.Fatalf("changed target error=%v", err)
	}
	_, writeIndexes, _, err := loadKitDBIndexLayout(database, target)
	if err != nil {
		t.Fatal(err)
	}

	write := func(tx *kitdbengine.Tx, previous, next map[string]value.Value, deleted bool) {
		t.Helper()
		rowKey, err := kitDBRowKey(target, previous["id"])
		if err != nil {
			t.Fatal(err)
		}
		oldEntries, err := kitDBSecondaryIndexEntriesForPhysical(target, previous, rowKey, writeIndexes)
		if err != nil {
			t.Fatal(err)
		}
		for _, indexEntry := range oldEntries {
			if err := tx.Delete(indexEntry.key); err != nil {
				t.Fatal(err)
			}
		}
		if deleted {
			if err := tx.Delete(rowKey); err != nil {
				t.Fatal(err)
			}
			return
		}
		encoded, err := encodeKitDBValidatedRow(target, next, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Put(rowKey, encoded); err != nil {
			t.Fatal(err)
		}
		newEntries, err := kitDBSecondaryIndexEntriesForPhysical(target, next, rowKey, writeIndexes)
		if err != nil {
			t.Fatal(err)
		}
		for _, indexEntry := range newEntries {
			if err := tx.Put(indexEntry.key, indexEntry.value); err != nil {
				t.Fatal(err)
			}
		}
	}
	tx, err = database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	low := lifecycleProduct(1)
	lowNext := cloneKitDBRow(low)
	lowNext["category"] = value.New("updated-low")
	write(tx, low, lowNext, false)
	high := lifecycleProduct(total - 10)
	highNext := cloneKitDBRow(high)
	highNext["category"] = value.New("updated-high")
	write(tx, high, highNext, false)
	removed := lifecycleProduct(total - 5)
	write(tx, removed, nil, true)
	inserted := map[string]value.Value{
		"id": value.New("a-new"), "category": value.New("inserted"), "price": value.New(777),
	}
	insertedKey, _ := kitDBRowKey(target, inserted["id"])
	insertedValue, _ := encodeKitDBValidatedRow(target, inserted, nil)
	if err := tx.Put(insertedKey, insertedValue); err != nil {
		t.Fatal(err)
	}
	insertedEntries, _ := kitDBSecondaryIndexEntriesForPhysical(target, inserted, insertedKey, writeIndexes)
	for _, indexEntry := range insertedEntries {
		if err := tx.Put(indexEntry.key, indexEntry.value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, indexEntry := range insertedEntries {
		owner, found, err := database.Get(indexEntry.key)
		if err != nil || !found || !bytes.Equal(owner, insertedKey) {
			t.Fatalf("admission dual-write owner=%x found=%t err=%v", owner, found, err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	database, err = kitdbengine.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	completeKitDBSecondaryIndexForTest(t, database)
	entry, found, err = database.CatalogStructByID(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Hash != target.Hash {
		t.Fatalf("catalog did not cut over: %s", entry.Hash)
	}
	if err := validateKitDBWriteDefinition(database, stored); err == nil || !strings.Contains(err.Error(), "stale schema") {
		t.Fatalf("post-cutover stale source write error=%v", err)
	}
	if err := validateKitDBWriteDefinition(database, target); err != nil {
		t.Fatalf("post-cutover target write admission=%v", err)
	}
	if _, found, err := loadKitDBIndexBuildState(database, target, kitDBIndexBuildSchema); err != nil || found {
		t.Fatalf("schema build remains found=%t err=%v", found, err)
	}
	assertKitDBIndexMatchesRows(t, database, target, collectIndexes(target.Name, target.columns)[0])
	inactive, err = loadKitDBInactiveIndexes(database, target)
	if err != nil {
		t.Fatal(err)
	}
	access, err = lifecycleCategoryAccess(target, inactive)
	if err != nil || access.kind != kitDBAccessIndex {
		t.Fatalf("published schema access=%#v err=%v", access, err)
	}
}

func TestKitDBIndexBuildStateRejectsCorruption(t *testing.T) {
	state := kitDBIndexBuildState{
		Mode: kitDBIndexBuildSchema, Phase: kitDBIndexBuildPhaseRows,
		StartedAt: 7, Rows: 11,
		SourceHash: strings.Repeat("01", 32), TargetHash: strings.Repeat("02", 32),
		Progress: []byte("row/11"), SourceDefinition: []byte(`{"version":2}`),
		TargetDefinition: []byte(`{"version":2}`),
		Indexes:          []string{strings.Repeat("03", 16)}, Generations: []uint64{9},
	}
	encoded, err := encodeKitDBIndexBuildState(state)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeKitDBIndexBuildState(encoded)
	if err != nil || decoded.TargetHash != state.TargetHash || !bytes.Equal(decoded.Progress, state.Progress) {
		t.Fatalf("decoded=%#v err=%v", decoded, err)
	}
	corrupt := bytes.Clone(encoded)
	corrupt[20] ^= 0xff
	if _, err := decodeKitDBIndexBuildState(corrupt); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("corrupt state error=%v", err)
	}
	for _, truncated := range [][]byte{nil, encoded[:20], encoded[:88]} {
		if _, err := decodeKitDBIndexBuildState(truncated); err == nil {
			t.Fatalf("truncated state of %d bytes was accepted", len(truncated))
		}
	}
	legacy := state
	legacy.FormatVersion = kitDBIndexBuildLegacyVersion
	legacy.SourceDefinition = nil
	legacy.Generations = nil
	legacyEncoded, err := encodeKitDBIndexBuildState(legacy)
	if err != nil {
		t.Fatal(err)
	}
	legacyDecoded, err := decodeKitDBIndexBuildState(legacyEncoded)
	if err != nil || legacyDecoded.FormatVersion != kitDBIndexBuildLegacyVersion ||
		len(legacyDecoded.Generations) != 1 || legacyDecoded.Generations[0] != 0 {
		t.Fatalf("legacy decoded=%#v err=%v", legacyDecoded, err)
	}
	legacyReencoded, err := encodeKitDBIndexBuildState(legacyDecoded)
	if err != nil || !bytes.Equal(legacyReencoded, legacyEncoded) {
		t.Fatalf("legacy reencode changed canonical bytes err=%v", err)
	}
}

func lifecycleProduct(number int) map[string]value.Value {
	return map[string]value.Value{
		"id": value.New(fmt.Sprintf("p%05d", number)), "category": value.New(fmt.Sprintf("c%d", number%7)),
		"price": value.New(number),
	}
}

func lifecycleCategoryAccess(definition *StructDef, inactive map[string]struct{}) (kitDBAccessPlan, error) {
	return (&SchemaTable{
		table: definition.Name, columns: definition.columns, definition: definition, inactiveIndexes: inactive,
	}).planKitDBAccess(query.ExecutionPlan{Conditions: []query.Condition{{
		Column: "category", Operator: "=", Value: "c1", Logic: "AND",
	}}})
}

func countKitDBLegacyIndexKeys(t *testing.T, database *kitdbengine.DB, definition *StructDef) int {
	t.Helper()
	prefix, err := kitDBFixedKey(kitDBIndexNamespace, definition.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := database.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	cursor, err := snapshot.Cursor(kitdbengine.RangeOptions{Prefix: prefix})
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	count := 0
	for cursor.Next() {
		key := cursor.Key()
		if len(key) > 33 && key[33] != 0 {
			count++
		}
	}
	if err := cursor.Err(); err != nil {
		t.Fatal(err)
	}
	return count
}

func assertKitDBIndexMatchesRows(
	t *testing.T,
	database *kitdbengine.DB,
	definition *StructDef,
	index indexDef,
) {
	t.Helper()
	expected := make(map[string]string)
	metadata, _, err := loadKitDBIndexGenerationMetadata(database, definition)
	if err != nil {
		t.Fatal(err)
	}
	generation := kitDBPhysicalIndexGeneration(metadata, index)
	rowPrefix, err := kitDBRowPrefix(definition)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := database.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	rowCursor, err := snapshot.Cursor(kitdbengine.RangeOptions{Prefix: rowPrefix})
	if err != nil {
		_ = snapshot.Close()
		t.Fatal(err)
	}
	for rowCursor.Next() {
		rowKey := rowCursor.Key()
		decoded, err := decodeKitDBRow(definition, rowCursor.Value())
		if err != nil {
			t.Fatal(err)
		}
		entries, err := kitDBSecondaryIndexEntriesForGenerations(
			definition,
			decoded.values,
			rowKey,
			[]indexDef{index},
			map[string]uint64{kitDBIndexSignature(index): generation},
		)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			expected[string(entry.key)] = string(entry.value)
		}
	}
	if err := rowCursor.Err(); err != nil {
		t.Fatal(err)
	}
	_ = rowCursor.Close()

	actual := make(map[string]string)
	indexPrefix, err := kitDBIndexBasePrefixForGeneration(definition, index, generation)
	if err != nil {
		t.Fatal(err)
	}
	indexCursor, err := snapshot.Cursor(kitdbengine.RangeOptions{Prefix: indexPrefix})
	if err != nil {
		_ = snapshot.Close()
		t.Fatal(err)
	}
	for indexCursor.Next() {
		actual[string(indexCursor.Key())] = string(indexCursor.Value())
	}
	if err := indexCursor.Err(); err != nil {
		t.Fatal(err)
	}
	_ = indexCursor.Close()
	_ = snapshot.Close()
	if len(actual) != len(expected) {
		t.Fatalf("index entry count=%d want=%d", len(actual), len(expected))
	}
	for key, owner := range expected {
		if actual[key] != owner {
			t.Fatalf("index key %x owner=%x want=%x", []byte(key), []byte(actual[key]), []byte(owner))
		}
	}
}
