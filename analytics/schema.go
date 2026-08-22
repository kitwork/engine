package analytics

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

// Kind describes the logical type of one column.
type Kind uint8

const (
	KindInvalid Kind = iota
	KindBool
	KindInt64
	KindFloat64
	KindText
	KindTime
)

func (kind Kind) String() string {
	switch kind {
	case KindBool:
		return "bool"
	case KindInt64:
		return "int64"
	case KindFloat64:
		return "float64"
	case KindText:
		return "text"
	case KindTime:
		return "time"
	default:
		return "invalid"
	}
}

// Column declares one analytics column.
type Column struct {
	Name     string
	Kind     Kind
	Nullable bool
}

// Schema describes the immutable column layout for one analytics store.
type Schema struct {
	Columns []Column
	index   map[string]int
}

var (
	ErrDuplicateColumn = errors.New("analytics: duplicate column")
	ErrUnknownColumn   = errors.New("analytics: unknown column")
	ErrTypeMismatch    = errors.New("analytics: type mismatch")
	ErrInvalidSchema   = errors.New("analytics: invalid schema")
)

// NewSchema validates the requested columns and returns an immutable schema.
func NewSchema(columns ...Column) (Schema, error) {
	if len(columns) == 0 {
		return Schema{}, fmt.Errorf("%w: empty schema", ErrInvalidSchema)
	}
	index := make(map[string]int, len(columns))
	copyColumns := make([]Column, len(columns))
	for position, column := range columns {
		if column.Name == "" {
			return Schema{}, fmt.Errorf("%w: column %d has an empty name", ErrInvalidSchema, position)
		}
		if column.Kind == KindInvalid {
			return Schema{}, fmt.Errorf("%w: column %q has an invalid kind", ErrInvalidSchema, column.Name)
		}
		if _, exists := index[column.Name]; exists {
			return Schema{}, fmt.Errorf("%w: %q", ErrDuplicateColumn, column.Name)
		}
		index[column.Name] = position
		copyColumns[position] = column
	}
	return Schema{Columns: copyColumns, index: index}, nil
}

// Column returns the declared column, if present.
func (schema Schema) Column(name string) (Column, bool) {
	position, exists := schema.index[name]
	if !exists {
		return Column{}, false
	}
	return schema.Columns[position], true
}

// Index returns the position of one declared column.
func (schema Schema) Index(name string) (int, bool) {
	position, exists := schema.index[name]
	return position, exists
}

// MustColumn returns the declared column or panics. It is meant for tests and
// package-internal setup.
func (schema Schema) MustColumn(name string) Column {
	column, ok := schema.Column(name)
	if !ok {
		panic(fmt.Sprintf("analytics: missing column %q", name))
	}
	return column
}

// Fingerprint returns a stable schema digest used by durable segment files.
func (schema Schema) Fingerprint() [32]byte {
	hash := sha256.New()
	var scratch [8]byte
	binary.LittleEndian.PutUint32(scratch[:4], uint32(len(schema.Columns)))
	_, _ = hash.Write(scratch[:4])
	for _, column := range schema.Columns {
		binary.LittleEndian.PutUint32(scratch[:4], uint32(len(column.Name)))
		_, _ = hash.Write(scratch[:4])
		_, _ = hash.Write([]byte(column.Name))
		scratch[0] = byte(column.Kind)
		if column.Nullable {
			scratch[1] = 1
		} else {
			scratch[1] = 0
		}
		_, _ = hash.Write(scratch[:2])
	}
	var digest [32]byte
	copy(digest[:], hash.Sum(nil))
	return digest
}
