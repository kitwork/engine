package analytics

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/kitwork/engine/kitdb"
)

// KitDBRowDecoder turns one KitDB key/value pair into a stable analytics row.
// The returned projection key is the identity used for upserts and deletes.
// Returning ok=false skips the record.
type KitDBRowDecoder func(key, value []byte) (projectionKey string, row Row, ok bool, err error)

// KitDBProjection keeps one analytics snapshot synchronized with a KitDB source.
type KitDBProjection struct {
	mu          sync.Mutex
	source      *kitdb.DB
	store       *DiskStore
	decode      KitDBRowDecoder
	records     map[string]Row
	unsubscribe func()
	lastTx      uint64
	lastErr     error
	closed      bool
}

// OpenKitDBProjection seeds one durable analytics projection from the current
// visible KitDB snapshot and then subscribes to future commits.
func OpenKitDBProjection(directory string, schema Schema, source *kitdb.DB, decoder KitDBRowDecoder) (*KitDBProjection, error) {
	if source == nil {
		return nil, fmt.Errorf("analytics: nil KitDB source")
	}
	if decoder == nil {
		return nil, fmt.Errorf("analytics: nil KitDB row decoder")
	}
	store, err := OpenDiskStore(directory, schema)
	if err != nil {
		return nil, err
	}
	projection := &KitDBProjection{
		source:  source,
		store:   store,
		decode:  decoder,
		records: make(map[string]Row),
	}
	unsubscribe, err := source.AddCommitListener(projection.handleCommit)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	projection.unsubscribe = unsubscribe
	if err := projection.seed(context.Background()); err != nil {
		unsubscribe()
		_ = store.Close()
		return nil, err
	}
	if last, err := source.LastTransaction(); err == nil {
		projection.lastTx = last
	}
	return projection, nil
}

// Close stops change-feed delivery and closes the durable store.
func (projection *KitDBProjection) Close() error {
	projection.mu.Lock()
	if projection.closed {
		projection.mu.Unlock()
		return nil
	}
	projection.closed = true
	unsubscribe := projection.unsubscribe
	projection.unsubscribe = nil
	store := projection.store
	projection.mu.Unlock()
	if unsubscribe != nil {
		unsubscribe()
	}
	if store != nil {
		return store.Close()
	}
	return nil
}

// Err returns the latest feed error, if one has occurred.
func (projection *KitDBProjection) Err() error {
	projection.mu.Lock()
	defer projection.mu.Unlock()
	return projection.lastErr
}

// Scan delegates to the durable analytics store.
func (projection *KitDBProjection) Scan(ctx context.Context, predicates ...Predicate) ([]Row, error) {
	return projection.store.Scan(ctx, predicates...)
}

// Count delegates to the durable analytics store.
func (projection *KitDBProjection) Count(ctx context.Context, predicates ...Predicate) (int64, error) {
	return projection.store.Count(ctx, predicates...)
}

// GroupBy delegates to the durable analytics store.
func (projection *KitDBProjection) GroupBy(ctx context.Context, groupColumns []string, predicates []Predicate, aggregates ...Aggregate) ([]GroupResult, error) {
	return projection.store.GroupBy(ctx, groupColumns, predicates, aggregates...)
}

func (projection *KitDBProjection) seed(ctx context.Context) error {
	records := make(map[string]Row)
	err := projection.source.Walk(func(key, value []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		projectionKey, row, ok, err := projection.decode(key, value)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		records[projectionKey] = cloneRow(row)
		return nil
	})
	if err != nil {
		return err
	}
	projection.mu.Lock()
	projection.records = records
	projection.mu.Unlock()
	return projection.flush()
}

func (projection *KitDBProjection) handleCommit(event kitdb.CommitEvent) {
	projection.mu.Lock()
	if projection.closed || projection.lastErr != nil {
		projection.mu.Unlock()
		return
	}
	for _, operation := range event.Operations {
		projectionKey, row, ok, err := projection.decode(operation.Key, operation.Value)
		if err != nil {
			projection.lastErr = err
			projection.mu.Unlock()
			return
		}
		if !ok {
			continue
		}
		switch operation.Kind {
		case kitdb.CommitOperationDelete:
			delete(projection.records, projectionKey)
		default:
			projection.records[projectionKey] = cloneRow(row)
		}
	}
	projection.lastTx = event.Transaction
	records := snapshotRowsLocked(projection.records)
	projection.mu.Unlock()

	if err := projection.store.ReplaceAll(records); err != nil {
		projection.fail(err)
	}
}

func (projection *KitDBProjection) flush() error {
	projection.mu.Lock()
	records := snapshotRowsLocked(projection.records)
	projection.mu.Unlock()
	return projection.store.ReplaceAll(records)
}

func (projection *KitDBProjection) fail(err error) {
	projection.mu.Lock()
	if projection.lastErr == nil {
		projection.lastErr = err
	}
	unsubscribe := projection.unsubscribe
	projection.unsubscribe = nil
	projection.mu.Unlock()
	if unsubscribe != nil {
		unsubscribe()
	}
}

func snapshotRowsLocked(records map[string]Row) []Row {
	keys := make([]string, 0, len(records))
	for key := range records {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	rows := make([]Row, 0, len(keys))
	for _, key := range keys {
		rows = append(rows, cloneRow(records[key]))
	}
	return rows
}

func cloneRow(row Row) Row {
	if row == nil {
		return nil
	}
	copyRow := make(Row, len(row))
	for key, value := range row {
		copyRow[key] = cloneValue(value)
	}
	return copyRow
}

func cloneValue(value any) any {
	switch typed := value.(type) {
	case []byte:
		return bytes.Clone(typed)
	case Row:
		return cloneRow(typed)
	default:
		return value
	}
}
