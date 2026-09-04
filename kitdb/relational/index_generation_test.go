package relational

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"hash/crc32"
	"path/filepath"
	"sort"
	"testing"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func TestStandaloneReadsPublishedRowAndIndexGenerations(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "generated-index.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `
		CREATE TABLE products (
			merchant TEXT NOT NULL,
			id BIGINT NOT NULL,
			name TEXT,
			PRIMARY KEY (merchant, id)
		)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `
		CREATE INDEX products_merchant_id_idx ON products (merchant, id)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `
		INSERT INTO products (merchant, id, name) VALUES
		('tiki', 2, 'two'), ('shopee', 3, 'three'),
		('tiki', 1, 'one'), ('lazada', 4, 'four')
	`); err != nil {
		t.Fatal(err)
	}

	engine.mu.RLock()
	schema, err := engine.schemaLocked("products")
	engine.mu.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	indexes, err := collectSecondaryIndexes(schema)
	if err != nil || len(indexes) != 1 {
		t.Fatalf("secondary indexes = %#v, err=%v", indexes, err)
	}
	index := indexes[0]
	const generation = uint64(41)
	installGeneratedRowsAndIndex(t, engine, schema, index, generation)

	access := testSelectAccess(t, engine, `
		SELECT merchant, id FROM products ORDER BY merchant, id LIMIT 2
	`)
	if access.kind != rowAccessSecondary || access.name != "products_merchant_id_idx" ||
		!access.orderCovered || access.rowGeneration != generation {
		t.Fatalf("generated access = %#v", access)
	}
	wantedPrefix, err := secondaryIndexBasePrefixForGeneration(schema, index, generation)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(access.options.Prefix, wantedPrefix) {
		t.Fatalf("index prefix = %x, want %x", access.options.Prefix, wantedPrefix)
	}

	selected, err := engine.Execute(ctx, `
		SELECT merchant, id, name FROM products ORDER BY merchant, id LIMIT 4
	`)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.Rows) != 4 || selected.Rows[0][0] != "lazada" ||
		selected.Rows[1][0] != "shopee" || selected.Rows[2][1] != int64(1) {
		t.Fatalf("generated ordered rows = %#v", selected.Rows)
	}
	rangeQuery := `SELECT merchant,id FROM products WHERE merchant='tiki' AND id>=2 AND id<=2 ORDER BY id DESC LIMIT 1`
	rangeAccess := testSelectAccess(t, engine, rangeQuery)
	if rangeAccess.rowGeneration != generation || rangeAccess.rangeField != "id" ||
		!bytes.HasPrefix(rangeAccess.options.Start, wantedPrefix) || !bytes.HasPrefix(rangeAccess.options.End, wantedPrefix) {
		t.Fatalf("generated range access = %#v", rangeAccess)
	}
	ranged, err := engine.Execute(ctx, rangeQuery)
	if err != nil || len(ranged.Rows) != 1 || ranged.Rows[0][1] != int64(2) {
		t.Fatalf("generated range rows = %#v, %v", ranged.Rows, err)
	}
	if _, err := engine.Execute(ctx, `REINDEX TABLE products`); err != nil {
		t.Fatal(err)
	}
	if access := testSelectAccess(t, engine, `
		SELECT merchant, id FROM products ORDER BY merchant, id LIMIT 2
	`); access.kind != rowAccessSecondary || access.rowGeneration != generation {
		t.Fatalf("access after REINDEX = %#v", access)
	}
}

func TestIndexGenerationMetadataRejectsChecksumDamage(t *testing.T) {
	encoded := testIndexGenerationMetadata(map[string]uint64{
		"00112233445566778899aabbccddeeff": 7,
	})
	encoded[len(encoded)-1] ^= 0xff
	if _, err := decodeIndexGenerationMetadata(encoded); err == nil {
		t.Fatal("damaged index-generation metadata was accepted")
	}
}

func installGeneratedRowsAndIndex(
	t *testing.T,
	engine *Engine,
	schema kitdbsql.Schema,
	index secondaryIndex,
	generation uint64,
) {
	t.Helper()
	logicalPrefix, err := rowPrefix(schema, 0)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := engine.database.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := snapshot.Cursor(kitdbengine.RangeOptions{Prefix: logicalPrefix})
	if err != nil {
		_ = snapshot.Close()
		t.Fatal(err)
	}
	type storedRow struct {
		key   []byte
		value []byte
	}
	var rows []storedRow
	for cursor.Next() {
		rows = append(rows, storedRow{key: cursor.Key(), value: cursor.Value()})
	}
	if err := cursor.Err(); err != nil {
		_ = cursor.Close()
		_ = snapshot.Close()
		t.Fatal(err)
	}
	_ = cursor.Close()
	_ = snapshot.Close()

	v2Prefix, err := secondaryIndexBasePrefix(schema, index)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err = engine.database.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	indexCursor, err := snapshot.Cursor(kitdbengine.RangeOptions{Prefix: v2Prefix})
	if err != nil {
		_ = snapshot.Close()
		t.Fatal(err)
	}
	var oldIndexKeys [][]byte
	for indexCursor.Next() {
		oldIndexKeys = append(oldIndexKeys, indexCursor.Key())
	}
	if err := indexCursor.Err(); err != nil {
		_ = indexCursor.Close()
		_ = snapshot.Close()
		t.Fatal(err)
	}
	_ = indexCursor.Close()
	_ = snapshot.Close()

	transaction, err := engine.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		physical, err := physicalRowKey(schema, row.key, generation)
		if err != nil {
			_ = transaction.Rollback()
			t.Fatal(err)
		}
		decoded, err := decodeRow(schema, row.value)
		if err != nil {
			_ = transaction.Rollback()
			t.Fatal(err)
		}
		entry, applicable, err := secondaryIndexEntryForGeneration(
			schema, index, decoded.values, row.key, generation,
		)
		if err != nil || !applicable {
			_ = transaction.Rollback()
			t.Fatalf("generated index entry applicable=%t err=%v", applicable, err)
		}
		if err := transaction.Put(physical, row.value); err != nil {
			_ = transaction.Rollback()
			t.Fatal(err)
		}
		if err := transaction.Put(entry.key, entry.value); err != nil {
			_ = transaction.Rollback()
			t.Fatal(err)
		}
		if err := transaction.Delete(row.key); err != nil {
			_ = transaction.Rollback()
			t.Fatal(err)
		}
	}
	for _, key := range oldIndexKeys {
		if err := transaction.Delete(key); err != nil {
			_ = transaction.Rollback()
			t.Fatal(err)
		}
	}
	readyKey, err := indexReadyKey(schema, index)
	if err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	if err := transaction.Delete(readyKey); err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	rowMetadataKey, err := fixedKey(
		physicalNamespace,
		schema.ID,
		kitdbsql.StableSchemaID("physical", schema.ID+":row-generations"),
	)
	if err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	if err := transaction.Put(rowMetadataKey, testRowGenerationMetadata(generation)); err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	indexMetadataKey, err := indexGenerationMetadataKey(schema)
	if err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	if err := transaction.Put(indexMetadataKey, testIndexGenerationMetadata(map[string]uint64{
		secondaryIndexSignature(index): generation,
	})); err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	if _, err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
}

func testRowGenerationMetadata(generation uint64) []byte {
	encoded := []byte{'K', 'R', 'G', 'M', 1, 0, 0, 0}
	encoded = binary.BigEndian.AppendUint64(encoded, 1)
	encoded = binary.BigEndian.AppendUint64(encoded, generation)
	encoded = binary.AppendUvarint(encoded, 0)
	return binary.BigEndian.AppendUint32(encoded, crc32.Checksum(encoded, rowGenerationCRC))
}

func testIndexGenerationMetadata(active map[string]uint64) []byte {
	encoded := []byte{'K', 'I', 'G', 'M', 1, 0, 0, 0}
	encoded = binary.BigEndian.AppendUint64(encoded, 1)
	signatures := make([]string, 0, len(active))
	for signature := range active {
		signatures = append(signatures, signature)
	}
	sort.Strings(signatures)
	encoded = binary.AppendUvarint(encoded, uint64(len(signatures)))
	for _, signature := range signatures {
		identity, err := hex.DecodeString(signature)
		if err != nil || len(identity) != 16 {
			panic("invalid test index signature")
		}
		encoded = append(encoded, identity...)
		encoded = binary.BigEndian.AppendUint64(encoded, active[signature])
	}
	encoded = binary.AppendUvarint(encoded, 0)
	return binary.BigEndian.AppendUint32(encoded, crc32.Checksum(encoded, indexGenerationMetadataCRC))
}
