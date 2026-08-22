package analytics

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Row is one analytics record.
type Row map[string]any

// ColumnStats summarize one immutable column segment.
type ColumnStats struct {
	Kind           Kind
	RowCount       int
	NullCount      int
	HasValues      bool
	BoolTrueCount  uint64
	BoolFalseCount uint64
	IntMin         int64
	IntMax         int64
	FloatMin       float64
	FloatMax       float64
	TextMin        string
	TextMax        string
	TimeMin        time.Time
	TimeMax        time.Time
}

type columnData struct {
	values []any
	stats  ColumnStats
}

func newColumnData(kind Kind) *columnData {
	return &columnData{stats: ColumnStats{Kind: kind}}
}

func (column *columnData) appendValue(kind Kind, value any) error {
	if value == nil {
		column.stats.RowCount++
		column.stats.NullCount++
		column.values = append(column.values, nil)
		return nil
	}
	canonical, err := canonicalForColumn(kind, value)
	if err != nil {
		return err
	}
	column.stats.RowCount++
	column.stats.HasValues = true
	switch kind {
	case KindBool:
		typed := canonical.(bool)
		if typed {
			column.stats.BoolTrueCount++
		} else {
			column.stats.BoolFalseCount++
		}
	case KindInt64:
		typed := canonical.(int64)
		if column.stats.NullCount == column.stats.RowCount-1 && !column.stats.HasValues {
			column.stats.IntMin = typed
			column.stats.IntMax = typed
		} else if column.stats.RowCount == 1 || typed < column.stats.IntMin {
			column.stats.IntMin = typed
		} else if typed > column.stats.IntMax {
			column.stats.IntMax = typed
		}
	case KindFloat64:
		typed := canonical.(float64)
		if column.stats.RowCount == 1 || !column.stats.HasValues {
			column.stats.FloatMin = typed
			column.stats.FloatMax = typed
		} else {
			if typed < column.stats.FloatMin {
				column.stats.FloatMin = typed
			}
			if typed > column.stats.FloatMax {
				column.stats.FloatMax = typed
			}
		}
	case KindText:
		typed := canonical.(string)
		if !column.stats.HasValues || column.stats.TextMin == "" && column.stats.TextMax == "" && column.stats.RowCount == 1 {
			column.stats.TextMin = typed
			column.stats.TextMax = typed
		} else {
			if typed < column.stats.TextMin {
				column.stats.TextMin = typed
			}
			if typed > column.stats.TextMax {
				column.stats.TextMax = typed
			}
		}
	case KindTime:
		typed := canonical.(time.Time)
		if column.stats.TimeMin.IsZero() || typed.Before(column.stats.TimeMin) {
			column.stats.TimeMin = typed
		}
		if column.stats.TimeMax.IsZero() || typed.After(column.stats.TimeMax) {
			column.stats.TimeMax = typed
		}
	default:
		return fmt.Errorf("%w: unsupported column kind %s", ErrTypeMismatch, kind)
	}
	column.values = append(column.values, canonical)
	return nil
}

func canonicalForColumn(kind Kind, value any) (any, error) {
	switch kind {
	case KindBool:
		typed, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("%w: expected bool, got %T", ErrTypeMismatch, value)
		}
		return typed, nil
	case KindInt64:
		switch typed := value.(type) {
		case int:
			return int64(typed), nil
		case int8:
			return int64(typed), nil
		case int16:
			return int64(typed), nil
		case int32:
			return int64(typed), nil
		case int64:
			return typed, nil
		case uint:
			return int64(typed), nil
		case uint8:
			return int64(typed), nil
		case uint16:
			return int64(typed), nil
		case uint32:
			return int64(typed), nil
		case uint64:
			return int64(typed), nil
		default:
			return nil, fmt.Errorf("%w: expected int64, got %T", ErrTypeMismatch, value)
		}
	case KindFloat64:
		switch typed := value.(type) {
		case float32:
			return float64(typed), nil
		case float64:
			return typed, nil
		case int:
			return float64(typed), nil
		case int8:
			return float64(typed), nil
		case int16:
			return float64(typed), nil
		case int32:
			return float64(typed), nil
		case int64:
			return float64(typed), nil
		default:
			return nil, fmt.Errorf("%w: expected float64, got %T", ErrTypeMismatch, value)
		}
	case KindText:
		typed, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%w: expected string, got %T", ErrTypeMismatch, value)
		}
		return typed, nil
	case KindTime:
		typed, ok := value.(time.Time)
		if !ok {
			return nil, fmt.Errorf("%w: expected time.Time, got %T", ErrTypeMismatch, value)
		}
		return typed.UTC(), nil
	default:
		return nil, fmt.Errorf("%w: unsupported kind %s", ErrTypeMismatch, kind)
	}
}

// Segment is one immutable columnar snapshot.
type Segment struct {
	schema Schema
	rows   int
	cols   []columnData
}

func newSegment(schema Schema, cols []columnData, rows int) *Segment {
	return &Segment{
		schema: schema,
		rows:   rows,
		cols:   cols,
	}
}

// Rows returns the number of rows stored in the segment.
func (segment *Segment) Rows() int { return segment.rows }

// Stats returns the zone-map statistics for one column.
func (segment *Segment) Stats(column string) (ColumnStats, bool) {
	index, ok := segment.schema.Index(column)
	if !ok {
		return ColumnStats{}, false
	}
	return segment.cols[index].stats, true
}

func (segment *Segment) value(column string, row int) (any, bool) {
	index, ok := segment.schema.Index(column)
	if !ok || row < 0 || row >= segment.rows {
		return nil, false
	}
	value := segment.cols[index].values[row]
	if value == nil {
		return nil, false
	}
	return value, true
}

func (segment *Segment) row(row int) Row {
	result := make(Row, len(segment.schema.Columns))
	for position, column := range segment.schema.Columns {
		value := segment.cols[position].values[row]
		if value != nil {
			result[column.Name] = value
			continue
		}
		result[column.Name] = nil
	}
	return result
}

func (segment *Segment) mayMatch(predicates []Predicate) bool {
	for _, predicate := range predicates {
		index, ok := segment.schema.Index(predicate.Column())
		if !ok {
			return false
		}
		if !predicate.MayMatch(segment.cols[index].stats) {
			return false
		}
	}
	return true
}

// Builder appends rows into one immutable segment.
type Builder struct {
	schema Schema
	cols   []columnData
	rows   int
}

// NewBuilder creates a builder for the supplied schema.
func NewBuilder(schema Schema) *Builder {
	cols := make([]columnData, len(schema.Columns))
	for position, column := range schema.Columns {
		cols[position] = *newColumnData(column.Kind)
	}
	return &Builder{schema: schema, cols: cols}
}

// Add appends one row to the builder.
func (builder *Builder) Add(row Row) error {
	if row == nil {
		return fmt.Errorf("%w: nil row", ErrInvalidSchema)
	}
	for position, column := range builder.schema.Columns {
		value, exists := row[column.Name]
		if !exists {
			if !column.Nullable {
				return fmt.Errorf("%w: missing value for %q", ErrTypeMismatch, column.Name)
			}
			value = nil
		}
		if err := builder.cols[position].appendValue(column.Kind, value); err != nil {
			return fmt.Errorf("analytics: row %d column %q: %w", builder.rows, column.Name, err)
		}
	}
	builder.rows++
	return nil
}

// AddRows appends every row in order.
func (builder *Builder) AddRows(rows []Row) error {
	for _, row := range rows {
		if err := builder.Add(row); err != nil {
			return err
		}
	}
	return nil
}

// Build freezes the accumulated rows into one immutable segment.
func (builder *Builder) Build() (*Segment, error) {
	cols := make([]columnData, len(builder.cols))
	copy(cols, builder.cols)
	return newSegment(builder.schema, cols, builder.rows), nil
}

// Store is one in-memory analytics table composed of immutable segments.
type Store struct {
	mu       sync.RWMutex
	schema   Schema
	segments []*Segment
	rows     int
}

// NewStore creates an empty store for the supplied schema.
func NewStore(schema Schema) *Store {
	return &Store{schema: schema}
}

// Schema returns the store schema.
func (store *Store) Schema() Schema {
	return store.schema
}

// Rows returns the total number of visible rows.
func (store *Store) Rows() int {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return store.rows
}

// Segments returns the number of immutable segments.
func (store *Store) Segments() int {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return len(store.segments)
}

// Append adds one immutable segment built from the supplied rows.
func (store *Store) Append(rows []Row) error {
	if len(rows) == 0 {
		return nil
	}
	builder := NewBuilder(store.schema)
	if err := builder.AddRows(rows); err != nil {
		return err
	}
	segment, err := builder.Build()
	if err != nil {
		return err
	}
	store.mu.Lock()
	store.segments = append(store.segments, segment)
	store.rows += segment.rows
	store.mu.Unlock()
	return nil
}

// ReplaceAll swaps the current store contents with one new immutable snapshot.
func (store *Store) ReplaceAll(rows []Row) error {
	builder := NewBuilder(store.schema)
	if err := builder.AddRows(rows); err != nil {
		return err
	}
	segment, err := builder.Build()
	if err != nil {
		return err
	}
	store.mu.Lock()
	store.segments = []*Segment{segment}
	store.rows = segment.rows
	store.mu.Unlock()
	return nil
}

func (store *Store) appendSegment(segment *Segment) {
	store.mu.Lock()
	store.segments = append(store.segments, segment)
	store.rows += segment.rows
	store.mu.Unlock()
}

func (store *Store) replaceSegments(segments []*Segment) {
	store.mu.Lock()
	store.segments = append([]*Segment(nil), segments...)
	total := 0
	for _, segment := range segments {
		total += segment.rows
	}
	store.rows = total
	store.mu.Unlock()
}

func (store *Store) snapshotSegments() []*Segment {
	store.mu.RLock()
	defer store.mu.RUnlock()
	segments := make([]*Segment, len(store.segments))
	copy(segments, store.segments)
	return segments
}

func (store *Store) normalizePredicates(predicates []Predicate) ([]Predicate, error) {
	normalized := make([]Predicate, len(predicates))
	for position, predicate := range predicates {
		if predicate == nil {
			return nil, fmt.Errorf("%w: nil predicate", ErrInvalidSchema)
		}
		column, ok := store.schema.Column(predicate.Column())
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrUnknownColumn, predicate.Column())
		}
		if predicate.predicateKind() != column.Kind {
			return nil, fmt.Errorf("%w: column %q expects %s, predicate uses %s", ErrTypeMismatch, column.Name, column.Kind, predicate.predicateKind())
		}
		normalized[position] = predicate
	}
	return normalized, nil
}

func (store *Store) scan(ctx context.Context, predicates []Predicate, consume func(*Segment, int) error) error {
	normalized, err := store.normalizePredicates(predicates)
	if err != nil {
		return err
	}
	segments := store.snapshotSegments()
	for _, segment := range segments {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !segment.mayMatch(normalized) {
			continue
		}
		for row := 0; row < segment.rows; row++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			matched := true
			for _, predicate := range normalized {
				value, ok := segment.value(predicate.Column(), row)
				if !predicate.Matches(value, ok) {
					matched = false
					break
				}
			}
			if !matched {
				continue
			}
			if err := consume(segment, row); err != nil {
				return err
			}
		}
	}
	return nil
}

// Scan materializes every matching row.
func (store *Store) Scan(ctx context.Context, predicates ...Predicate) ([]Row, error) {
	rows := make([]Row, 0)
	err := store.scan(ctx, predicates, func(segment *Segment, row int) error {
		rows = append(rows, segment.row(row))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// Count returns the number of matching rows.
func (store *Store) Count(ctx context.Context, predicates ...Predicate) (int64, error) {
	var total int64
	err := store.scan(ctx, predicates, func(segment *Segment, row int) error {
		_ = segment
		_ = row
		total++
		return nil
	})
	return total, err
}

// Compact merges all visible rows into one new immutable segment.
func (store *Store) Compact(ctx context.Context) error {
	rows, err := store.Scan(ctx)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		store.mu.Lock()
		store.segments = nil
		store.rows = 0
		store.mu.Unlock()
		return nil
	}
	return store.ReplaceAll(rows)
}

func sortedGroupColumns(columns []string) []string {
	result := append([]string(nil), columns...)
	sort.Strings(result)
	return result
}

func validateGroupColumns(schema Schema, columns []string) error {
	for _, column := range columns {
		if _, ok := schema.Column(column); !ok {
			return fmt.Errorf("%w: %q", ErrUnknownColumn, column)
		}
	}
	return nil
}

func validatePredicateColumns(schema Schema, predicates []Predicate) error {
	for _, predicate := range predicates {
		if _, ok := schema.Column(predicate.Column()); !ok {
			return fmt.Errorf("%w: %q", ErrUnknownColumn, predicate.Column())
		}
	}
	return nil
}
