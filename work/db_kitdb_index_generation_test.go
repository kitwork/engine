package work

import (
	"bytes"
	"errors"
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

const kitDBIndexGenerationCrashExitCode = 79

func generationReplacementDefinitions() (*StructDef, *StructDef) {
	stored := bindStructDef("products", nil, map[string]*ColumnSpec{
		"id":       {kind: "text", primary: true, seq: 1},
		"category": {kind: "text", seq: 2, indexes: []colIndexRef{{name: "products_lookup"}}},
		"price":    {kind: "integer", seq: 3},
	})
	target := bindStructDef("products", nil, map[string]*ColumnSpec{
		"id":       {kind: "text", primary: true, seq: 1},
		"category": {kind: "text", seq: 2},
		"price":    {kind: "integer", seq: 3, indexes: []colIndexRef{{name: "products_lookup"}}},
	})
	return stored, target
}

func TestKitDBPhysicalIndexGenerationReplacesAndCleansAfterHardCrash(t *testing.T) {
	stored, target := generationReplacementDefinitions()
	path := filepath.Join(t.TempDir(), "physical-generation.kitdb")
	database, err := kitdbengine.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := commitKitDBCatalog(database, stored, nil, ""); err != nil {
		t.Fatal(err)
	}
	if ready, err := ensureKitDBSecondaryIndexCodec(database, stored); err != nil || !ready {
		t.Fatalf("initial codec ready=%t err=%v", ready, err)
	}

	const total = kitDBAtomicMigrationRows + 11
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
		entries, err := kitDBSecondaryIndexEntries(stored, row, rowKey)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if err := tx.Put(entry.key, entry.value); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(os.Args[0], "-test.run=^TestKitDBPhysicalIndexGenerationCrashHelper$", "-test.count=1")
	command.Env = append(os.Environ(), "KITDB_INDEX_GENERATION_CRASH_HELPER="+path)
	output, err := command.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != kitDBIndexGenerationCrashExitCode {
		t.Fatalf("generation crash helper err=%v output=%s", err, output)
	}

	database, err = kitdbengine.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	state, found, err := loadKitDBIndexBuildState(database, target, kitDBIndexBuildSchema)
	if err != nil || !found {
		t.Fatalf("generation state found=%t err=%v", found, err)
	}
	if state.FormatVersion != kitDBIndexBuildVersion || state.Rows != 0 || len(state.Progress) != 0 ||
		len(state.Generations) != 1 || state.Generations[0] == 0 || len(state.RetiredPrefixes) != 1 {
		t.Fatalf("generation state=%#v", state)
	}
	oldIndex := collectIndexes(stored.Name, stored.columns)[0]
	newIndex := collectIndexes(target.Name, target.columns)[0]
	oldPrefix, err := kitDBIndexBasePrefix(stored, oldIndex)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(state.RetiredPrefixes[0], oldPrefix) {
		t.Fatalf("retired prefix=%x want=%x", state.RetiredPrefixes[0], oldPrefix)
	}
	if metadata, found, err := loadKitDBIndexGenerationMetadata(database, target); err != nil || found || metadata.Epoch != 0 {
		t.Fatalf("metadata published before cutover=%#v found=%t err=%v", metadata, found, err)
	}
	if !advanceKitDBSecondaryIndexForTest(t, database) {
		t.Fatal("generation worker completed unexpectedly in its first large-table chunk")
	}
	state, found, err = loadKitDBIndexBuildState(database, target, kitDBIndexBuildSchema)
	if err != nil || !found || state.Rows != kitDBIndexBuildRowLimit || len(state.Progress) == 0 {
		t.Fatalf("generation worker progress=%#v found=%t err=%v", state, found, err)
	}

	inactive, err := loadKitDBInactiveIndexes(database, target)
	if err != nil {
		t.Fatal(err)
	}
	generations, writeIndexes, epoch, err := loadKitDBIndexLayout(database, target)
	if err != nil {
		t.Fatal(err)
	}
	if epoch != 0 || generations[kitDBIndexSignature(newIndex)] != state.Generations[0] || len(writeIndexes) != 3 {
		t.Fatalf("building layout generations=%v writes=%#v epoch=%d", generations, writeIndexes, epoch)
	}
	buildingAccess, err := (&SchemaTable{
		table: target.Name, columns: target.columns, definition: target,
		inactiveIndexes: inactive, indexGenerations: generations,
	}).planKitDBAccess(query.ExecutionPlan{Conditions: []query.Condition{{
		Column: "price", Operator: "=", Value: 100, Logic: "AND",
	}}})
	if err != nil || buildingAccess.kind != kitDBAccessScan {
		t.Fatalf("building access=%#v err=%v", buildingAccess, err)
	}

	// A low row has already crossed the builder cursor while a high row has not.
	// Updating both must keep the active source and shadow target generations exact.
	tx, err = database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, number := range []int{1, total - 2} {
		previous := lifecycleProduct(number)
		next := cloneKitDBRow(previous)
		next["category"] = value.New("moved")
		next["price"] = value.New(number + 50_000)
		rowKey, _ := kitDBRowKey(target, previous["id"])
		oldEntries, err := kitDBIndexEntriesForPhysical(target, previous, rowKey, writeIndexes)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range oldEntries {
			if err := tx.Delete(entry.key); err != nil {
				t.Fatal(err)
			}
		}
		encoded, err := encodeKitDBValidatedRow(target, next, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Put(rowKey, encoded); err != nil {
			t.Fatal(err)
		}
		newEntries, err := kitDBIndexEntriesForPhysical(target, next, rowKey, writeIndexes)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range newEntries {
			if err := tx.Put(entry.key, entry.value); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	pinned, err := database.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.Close()
	for attempt := 0; attempt < 16; attempt++ {
		advanceKitDBSecondaryIndexForTest(t, database)
		entry, found, err := database.CatalogStructByID(target.ID)
		if err != nil {
			t.Fatal(err)
		}
		if found && entry.Hash == target.Hash {
			break
		}
	}
	entry, found, err := database.CatalogStructByID(target.ID)
	if err != nil || !found || entry.Hash != target.Hash {
		t.Fatalf("catalog cutover=%#v found=%t err=%v", entry, found, err)
	}
	metadata, found, err := loadKitDBIndexGenerationMetadata(database, target)
	if err != nil || !found || metadata.Epoch != 1 || len(metadata.Active) != 1 || len(metadata.Retired) != 1 {
		t.Fatalf("published metadata=%#v found=%t err=%v", metadata, found, err)
	}
	newGeneration := metadata.Active[kitDBIndexSignature(newIndex)]
	if newGeneration == 0 || newGeneration != state.Generations[0] {
		t.Fatalf("active generation=%d want=%d", newGeneration, state.Generations[0])
	}
	if err := validateKitDBIndexLayoutEpoch(database, target, 0); err == nil ||
		!strings.Contains(err.Error(), "advanced") || !errors.Is(err, errKitDBLayoutAdvanced) {
		t.Fatalf("stale epoch error=%v", err)
	}
	activeGenerations, _, activeEpoch, err := loadKitDBIndexLayout(database, target)
	if err != nil || activeEpoch != 1 || activeGenerations[kitDBIndexSignature(newIndex)] != newGeneration {
		t.Fatalf("active layout=%v epoch=%d err=%v", activeGenerations, activeEpoch, err)
	}
	activeAccess, err := (&SchemaTable{
		table: target.Name, columns: target.columns, definition: target,
		indexGenerations: activeGenerations,
	}).planKitDBAccess(query.ExecutionPlan{Conditions: []query.Condition{{
		Column: "price", Operator: "=", Value: 100, Logic: "AND",
	}}})
	expectedNewPrefix, prefixErr := kitDBIndexBasePrefixForGeneration(target, newIndex, newGeneration)
	if err != nil || prefixErr != nil || activeAccess.kind != kitDBAccessIndex ||
		!bytes.HasPrefix(activeAccess.options.Prefix, expectedNewPrefix) {
		t.Fatalf("active access=%#v err=%v", activeAccess, err)
	}
	assertKitDBIndexMatchesRows(t, database, target, newIndex)

	cleanupState, found, err := loadKitDBIndexBuildState(database, target, kitDBIndexBuildSchema)
	if err != nil || !found || cleanupState.Phase != kitDBIndexBuildPhaseCleanup {
		t.Fatalf("cleanup state=%#v found=%t err=%v", cleanupState, found, err)
	}
	advanceKitDBSecondaryIndexForTest(t, database)
	cleanupState, found, err = loadKitDBIndexBuildState(database, target, kitDBIndexBuildSchema)
	if err != nil || !found || cleanupState.Phase != kitDBIndexBuildPhaseCleanup || len(cleanupState.Progress) == 0 {
		t.Fatalf("bounded cleanup state=%#v found=%t err=%v", cleanupState, found, err)
	}
	completeKitDBSecondaryIndexForTest(t, database)
	if _, found, err := loadKitDBIndexBuildState(database, target, kitDBIndexBuildSchema); err != nil || found {
		t.Fatalf("cleanup remains found=%t err=%v", found, err)
	}
	metadata, found, err = loadKitDBIndexGenerationMetadata(database, target)
	if err != nil || !found || len(metadata.Retired) != 0 || metadata.Epoch != 1 {
		t.Fatalf("clean metadata=%#v found=%t err=%v", metadata, found, err)
	}
	if count := countKitDBKeysWithPrefix(t, database, oldPrefix); count != 0 {
		t.Fatalf("current retired generation keys=%d", count)
	}
	if count := countKitDBSnapshotKeysWithPrefix(t, pinned, oldPrefix); count != total {
		t.Fatalf("pinned retired generation keys=%d want=%d", count, total)
	}
	if err := validateKitDBWriteDefinition(database, stored); err == nil || !strings.Contains(err.Error(), "stale schema") {
		t.Fatalf("retired schema write error=%v", err)
	}
}

func TestKitDBPhysicalIndexGenerationCrashHelper(t *testing.T) {
	path := os.Getenv("KITDB_INDEX_GENERATION_CRASH_HELPER")
	if path == "" {
		return
	}
	_, target := generationReplacementDefinitions()
	database, err := kitdbengine.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(80)
	}
	definitions := map[string]*StructDef{target.Name: target}
	if err := ensureKitDBCatalog(database, target, definitions, true, func() error { return nil }); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(81)
	}
	// The parent must recover the committed KIBS admission intent without any
	// Close, checkpoint, row scan, or deferred cleanup from this process.
	os.Exit(kitDBIndexGenerationCrashExitCode)
}

func TestKitDBPhysicalIndexRemovalPublishesEmptyActiveLayout(t *testing.T) {
	stored := bindStructDef("items", nil, map[string]*ColumnSpec{
		"id":   {kind: "text", primary: true, seq: 1},
		"code": {kind: "text", seq: 2, indexes: []colIndexRef{{}}},
	})
	target := bindStructDef("items", nil, map[string]*ColumnSpec{
		"id":   {kind: "text", primary: true, seq: 1},
		"code": {kind: "text", seq: 2},
	})
	database, err := kitdbengine.Open(filepath.Join(t.TempDir(), "remove-generation.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := commitKitDBCatalog(database, stored, nil, ""); err != nil {
		t.Fatal(err)
	}
	if ready, err := ensureKitDBSecondaryIndexCodec(database, stored); err != nil || !ready {
		t.Fatalf("codec ready=%t err=%v", ready, err)
	}
	row := map[string]value.Value{"id": value.New("one"), "code": value.New("A")}
	rowKey, _ := kitDBRowKey(stored, row["id"])
	encoded, _ := encodeKitDBValidatedRow(stored, row, nil)
	entries, _ := kitDBSecondaryIndexEntries(stored, row, rowKey)
	tx, _ := database.Begin()
	_ = tx.Put(rowKey, encoded)
	_ = tx.Put(entries[0].key, entries[0].value)
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	definitions := map[string]*StructDef{target.Name: target}
	if err := ensureKitDBCatalog(database, target, definitions, true, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	advanceKitDBSecondaryIndexForTest(t, database)
	metadata, found, err := loadKitDBIndexGenerationMetadata(database, target)
	if err != nil || !found || metadata.Epoch != 1 || len(metadata.Active) != 0 || len(metadata.Retired) != 1 {
		t.Fatalf("removed metadata=%#v found=%t err=%v", metadata, found, err)
	}
	access, err := (&SchemaTable{
		table: target.Name, columns: target.columns, definition: target,
	}).planKitDBAccess(query.ExecutionPlan{Conditions: []query.Condition{{
		Column: "code", Operator: "=", Value: "A", Logic: "AND",
	}}})
	if err != nil || access.kind != kitDBAccessScan {
		t.Fatalf("removed access=%#v err=%v", access, err)
	}
}

func TestKitDBPhysicalIndexGenerationChainsWithoutReturningToV2(t *testing.T) {
	base := bindStructDef("records", nil, map[string]*ColumnSpec{
		"id":    {kind: "text", primary: true, seq: 1},
		"code":  {kind: "text", seq: 2},
		"score": {kind: "integer", seq: 3},
	})
	first := bindStructDef("records", nil, map[string]*ColumnSpec{
		"id":    {kind: "text", primary: true, seq: 1},
		"code":  {kind: "text", seq: 2, indexes: []colIndexRef{{name: "records_lookup"}}},
		"score": {kind: "integer", seq: 3},
	})
	second := bindStructDef("records", nil, map[string]*ColumnSpec{
		"id":    {kind: "text", primary: true, seq: 1},
		"code":  {kind: "text", seq: 2},
		"score": {kind: "integer", seq: 3, indexes: []colIndexRef{{name: "records_lookup"}}},
	})
	database, err := kitdbengine.Open(filepath.Join(t.TempDir(), "generation-chain.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := commitKitDBCatalog(database, base, nil, ""); err != nil {
		t.Fatal(err)
	}
	if ready, err := ensureKitDBSecondaryIndexCodec(database, base); err != nil || !ready {
		t.Fatalf("codec ready=%t err=%v", ready, err)
	}
	tx, _ := database.Begin()
	for number := 0; number < 32; number++ {
		row := map[string]value.Value{
			"id": value.New(fmt.Sprintf("r%02d", number)), "code": value.New(fmt.Sprintf("c%d", number%4)),
			"score": value.New(number),
		}
		rowKey, _ := kitDBRowKey(base, row["id"])
		encoded, _ := encodeKitDBValidatedRow(base, row, nil)
		_ = tx.Put(rowKey, encoded)
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := ensureKitDBCatalog(
		database, first, map[string]*StructDef{first.Name: first}, true, func() error { return nil },
	); err != nil {
		t.Fatal(err)
	}
	advanceKitDBSecondaryIndexForTest(t, database)
	firstMetadata, found, err := loadKitDBIndexGenerationMetadata(database, first)
	firstIndex := collectIndexes(first.Name, first.columns)[0]
	firstGeneration := firstMetadata.Active[kitDBIndexSignature(firstIndex)]
	if err != nil || !found || firstMetadata.Epoch != 1 || firstGeneration == 0 {
		t.Fatalf("first metadata=%#v found=%t err=%v", firstMetadata, found, err)
	}
	if err := ensureKitDBCatalog(
		database, second, map[string]*StructDef{second.Name: second}, true, func() error { return nil },
	); err != nil {
		t.Fatal(err)
	}
	advanceKitDBSecondaryIndexForTest(t, database)
	secondMetadata, found, err := loadKitDBIndexGenerationMetadata(database, second)
	secondIndex := collectIndexes(second.Name, second.columns)[0]
	secondGeneration := secondMetadata.Active[kitDBIndexSignature(secondIndex)]
	if err != nil || !found || secondMetadata.Epoch != 2 || secondGeneration == 0 ||
		secondGeneration == firstGeneration || len(secondMetadata.Retired) != 1 {
		t.Fatalf("second metadata=%#v found=%t err=%v", secondMetadata, found, err)
	}
	firstPrefix, err := kitDBIndexBasePrefixForGeneration(first, firstIndex, firstGeneration)
	if err != nil || !bytes.Equal(secondMetadata.Retired[0], firstPrefix) ||
		len(firstPrefix) < 35 || firstPrefix[34] != kitDBIndexCodecV3 {
		t.Fatalf("retired chained prefix=%x want=%x err=%v", secondMetadata.Retired[0], firstPrefix, err)
	}
	if count := countKitDBKeysWithPrefix(t, database, firstPrefix); count != 32 {
		t.Fatalf("retired v3 keys before cleanup=%d", count)
	}
	if err := ensureKitDBCatalog(
		database, second, map[string]*StructDef{second.Name: second}, false, func() error { return nil },
	); err != nil {
		t.Fatal(err)
	}
	completeKitDBSecondaryIndexForTest(t, database)
	if count := countKitDBKeysWithPrefix(t, database, firstPrefix); count != 0 {
		t.Fatalf("retired v3 keys after cleanup=%d", count)
	}
	assertKitDBIndexMatchesRows(t, database, second, secondIndex)
}

func TestKitDBIndexGenerationMetadataRejectsCorruption(t *testing.T) {
	metadata := kitDBIndexGenerationMetadata{
		Epoch: 7,
		Active: map[string]uint64{
			strings.Repeat("01", 16): 9,
			strings.Repeat("02", 16): 11,
		},
		Retired: [][]byte{[]byte("old/a"), []byte("old/b")},
	}
	encoded, err := encodeKitDBIndexGenerationMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeKitDBIndexGenerationMetadata(encoded)
	if err != nil || decoded.Epoch != metadata.Epoch || len(decoded.Active) != 2 || len(decoded.Retired) != 2 {
		t.Fatalf("decoded=%#v err=%v", decoded, err)
	}
	if epoch, err := decodeKitDBIndexGenerationEpoch(encoded); err != nil || epoch != metadata.Epoch {
		t.Fatalf("epoch=%d err=%v", epoch, err)
	}
	corrupt := bytes.Clone(encoded)
	corrupt[12] ^= 0xff
	if _, err := decodeKitDBIndexGenerationMetadata(corrupt); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("corrupt metadata error=%v", err)
	}
	if _, err := decodeKitDBIndexGenerationEpoch(corrupt); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("corrupt epoch error=%v", err)
	}
	for _, truncated := range [][]byte{nil, encoded[:8], encoded[:len(encoded)-5]} {
		if _, err := decodeKitDBIndexGenerationMetadata(truncated); err == nil {
			t.Fatalf("truncated metadata of %d bytes was accepted", len(truncated))
		}
	}
}

func countKitDBKeysWithPrefix(t *testing.T, database *kitdbengine.DB, prefix []byte) int {
	t.Helper()
	snapshot, err := database.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	return countKitDBSnapshotKeysWithPrefix(t, snapshot, prefix)
}

func countKitDBSnapshotKeysWithPrefix(t *testing.T, snapshot *kitdbengine.Snapshot, prefix []byte) int {
	t.Helper()
	cursor, err := snapshot.Cursor(kitdbengine.RangeOptions{Prefix: prefix})
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	count := 0
	for cursor.Next() {
		count++
	}
	if err := cursor.Err(); err != nil {
		t.Fatal(err)
	}
	return count
}
