package search

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

const (
	maxSchemaFields       = 1<<16 - 1
	maxFieldNameBytes     = 255
	maxAnalyzerIDBytes    = 255
	defaultFieldBoost     = 1.0
	schemaFingerprintSalt = "kitwork-search-schema-v1\x00"
)

// Token is one analyzed term. Byte offsets refer to the original input.
type Token struct {
	Term     string
	Position uint32
	Start    int
	End      int
}

// Analyzer converts text into index and query terms. Implementations must be
// safe for concurrent use and check ctx during bounded work. Identifier must
// change whenever analysis behavior changes because it participates in the
// persisted schema fingerprint.
type Analyzer interface {
	Identifier() string
	Analyze(ctx context.Context, text string, emit func(Token) bool) error
}

// Field describes one indexed text field.
type Field struct {
	Name     string
	Analyzer Analyzer
	Boost    float64
}

// FieldOption changes a text field declaration.
type FieldOption func(*Field)

// Boost gives a field a positive finite scoring multiplier.
func Boost(value float64) FieldOption {
	return func(field *Field) {
		field.Boost = value
	}
}

// Text declares an indexed text field.
func Text(name string, analyzer Analyzer, options ...FieldOption) Field {
	field := Field{Name: name, Analyzer: analyzer, Boost: defaultFieldBoost}
	for _, option := range options {
		if option != nil {
			option(&field)
		}
	}
	return field
}

// Schema is an immutable ordered set of fields. Field order is part of the
// file format and therefore part of the schema fingerprint.
type Schema struct {
	fields      []Field
	fieldByName map[string]uint16
	fingerprint [sha256.Size]byte
}

// NewSchema validates and freezes fields.
func NewSchema(fields ...Field) (Schema, error) {
	if len(fields) == 0 {
		return Schema{}, fmt.Errorf("search: schema requires at least one field")
	}
	if len(fields) > maxSchemaFields {
		return Schema{}, fmt.Errorf("search: schema has %d fields; maximum is %d", len(fields), maxSchemaFields)
	}

	frozen := make([]Field, len(fields))
	byName := make(map[string]uint16, len(fields))
	for i, field := range fields {
		if !utf8.ValidString(field.Name) || strings.TrimSpace(field.Name) == "" {
			return Schema{}, fmt.Errorf("search: field %d has an invalid name", i)
		}
		if len(field.Name) > maxFieldNameBytes {
			return Schema{}, fmt.Errorf("search: field %q exceeds %d bytes", field.Name, maxFieldNameBytes)
		}
		if _, exists := byName[field.Name]; exists {
			return Schema{}, fmt.Errorf("search: duplicate field %q", field.Name)
		}
		if field.Analyzer == nil {
			return Schema{}, fmt.Errorf("search: field %q has no analyzer", field.Name)
		}
		analyzerID := field.Analyzer.Identifier()
		if !utf8.ValidString(analyzerID) || analyzerID == "" || len(analyzerID) > maxAnalyzerIDBytes {
			return Schema{}, fmt.Errorf("search: field %q has an invalid analyzer identifier", field.Name)
		}
		if field.Boost == 0 {
			field.Boost = defaultFieldBoost
		}
		if math.IsNaN(field.Boost) || math.IsInf(field.Boost, 0) || field.Boost <= 0 {
			return Schema{}, fmt.Errorf("search: field %q has invalid boost %v", field.Name, field.Boost)
		}
		frozen[i] = field
		byName[field.Name] = uint16(i)
	}

	schema := Schema{fields: frozen, fieldByName: byName}
	schema.fingerprint = fingerprintSchema(frozen)
	return schema, nil
}

// Fields returns a detached copy of the schema fields.
func (schema Schema) Fields() []Field {
	return append([]Field(nil), schema.fields...)
}

// Fingerprint returns the stable identity persisted in every segment.
func (schema Schema) Fingerprint() [sha256.Size]byte {
	return schema.fingerprint
}

func (schema Schema) field(name string) (uint16, Field, bool) {
	id, ok := schema.fieldByName[name]
	if !ok || int(id) >= len(schema.fields) {
		return 0, Field{}, false
	}
	return id, schema.fields[id], true
}

func (schema Schema) valid() bool {
	return len(schema.fields) > 0 && len(schema.fields) == len(schema.fieldByName)
}

func fingerprintSchema(fields []Field) [sha256.Size]byte {
	hash := sha256.New()
	_, _ = hash.Write([]byte(schemaFingerprintSalt))
	var number [8]byte
	for _, field := range fields {
		binary.LittleEndian.PutUint32(number[:4], uint32(len(field.Name)))
		_, _ = hash.Write(number[:4])
		_, _ = hash.Write([]byte(field.Name))

		analyzerID := field.Analyzer.Identifier()
		binary.LittleEndian.PutUint32(number[:4], uint32(len(analyzerID)))
		_, _ = hash.Write(number[:4])
		_, _ = hash.Write([]byte(analyzerID))

		binary.LittleEndian.PutUint64(number[:], math.Float64bits(field.Boost))
		_, _ = hash.Write(number[:])
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result
}

// Document is one external identifier and its indexed field values.
type Document struct {
	ID     string
	Fields map[string]string
}
