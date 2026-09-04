package relational

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"math"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbrecord "github.com/kitwork/engine/kitdb/record"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

var tableCountCRC = crc32.MakeTable(crc32.Castagnoli)

func tableCountKey(schema kitdbsql.Schema) ([]byte, error) {
	identity := kitdbsql.StableSchemaID("statistics", schema.ID+":row-count")
	return fixedKey(kitdbrecord.PhysicalNamespace, schema.ID, identity)
}

func encodeTableCount(count uint64) []byte {
	encoded := make([]byte, 16)
	copy(encoded[:4], "KRC1")
	binary.BigEndian.PutUint64(encoded[4:12], count)
	binary.BigEndian.PutUint32(encoded[12:16], crc32.Checksum(encoded[:12], tableCountCRC))
	return encoded
}

func readTableCount(reader recordReader, schema kitdbsql.Schema) (uint64, bool, error) {
	key, err := tableCountKey(schema)
	if err != nil {
		return 0, false, err
	}
	encoded, found, err := reader.Get(key)
	if err != nil || !found {
		return 0, false, err
	}
	if len(encoded) != 16 || !bytes.Equal(encoded[:4], []byte("KRC1")) ||
		crc32.Checksum(encoded[:12], tableCountCRC) != binary.BigEndian.Uint32(encoded[12:16]) {
		return 0, false, fmt.Errorf("kitdb: corrupt row-count statistics for table %q", schema.Name)
	}
	return binary.BigEndian.Uint64(encoded[4:12]), true, nil
}

func adjustTableCount(transaction *Transaction, schema kitdbsql.Schema, delta int64) error {
	count, found, err := readTableCount(transaction, schema)
	if err != nil || !found {
		return err
	}
	if delta < 0 {
		magnitude := uint64(-delta)
		if magnitude > count {
			return fmt.Errorf("kitdb: row-count statistics underflow for table %q", schema.Name)
		}
		count -= magnitude
	} else {
		if uint64(delta) > math.MaxUint64-count {
			return fmt.Errorf("kitdb: row-count statistics overflow for table %q", schema.Name)
		}
		count += uint64(delta)
	}
	key, err := tableCountKey(schema)
	if err != nil {
		return err
	}
	return transaction.Put(key, encodeTableCount(count))
}

func markEmptyTableCount(transaction *kitdbengine.Tx, schema kitdbsql.Schema) error {
	key, err := tableCountKey(schema)
	if err != nil {
		return err
	}
	return transaction.Put(key, encodeTableCount(0))
}

func (engine *Engine) executeAnalyze(ctx context.Context, plan *kitdbsql.AnalyzeStatement) (Result, error) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if err := engine.readyLocked(); err != nil {
		return Result{}, err
	}
	catalog, err := engine.database.Catalog()
	if err != nil {
		return Result{}, err
	}
	schemas := make([]kitdbsql.Schema, 0, len(catalog.Structs))
	if plan != nil && plan.Table != "" {
		schema, err := schemaFromCatalog(catalog, plan.Table)
		if err != nil {
			return Result{}, err
		}
		schemas = append(schemas, schema)
	} else {
		for _, entry := range catalog.Structs {
			schema, err := decodeCatalogSchema(entry.Definition)
			if err != nil {
				return Result{}, err
			}
			schemas = append(schemas, schema)
		}
	}
	counts := make([]uint64, len(schemas))
	for index, schema := range schemas {
		generation, err := activeRowGeneration(engine.database, schema)
		if err != nil {
			return Result{}, err
		}
		prefix, err := rowPrefix(schema, generation)
		if err != nil {
			return Result{}, err
		}
		snapshot, err := engine.database.Snapshot()
		if err != nil {
			return Result{}, err
		}
		cursor, err := snapshot.Cursor(kitdbengine.RangeOptions{Prefix: prefix})
		if err != nil {
			_ = snapshot.Close()
			return Result{}, err
		}
		for cursor.Next() {
			counts[index]++
			if counts[index]&4095 == 0 {
				if err := ctx.Err(); err != nil {
					_ = cursor.Close()
					_ = snapshot.Close()
					return Result{}, err
				}
			}
		}
		scanErr := cursor.Err()
		_ = cursor.Close()
		_ = snapshot.Close()
		if scanErr != nil {
			return Result{}, scanErr
		}
	}
	transaction, err := engine.database.Begin()
	if err != nil {
		return Result{}, err
	}
	for index, schema := range schemas {
		key, err := tableCountKey(schema)
		if err == nil {
			err = transaction.Put(key, encodeTableCount(counts[index]))
		}
		if err != nil {
			_ = transaction.Rollback()
			return Result{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		_ = transaction.Rollback()
		return Result{}, err
	}
	if _, err := transaction.Commit(); err != nil {
		return Result{}, err
	}
	return Result{CommandTag: "ANALYZE"}, nil
}
