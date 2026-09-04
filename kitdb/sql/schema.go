package sql

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	SchemaVersion1       = 1
	SchemaVersion2       = 2
	SchemaVersion3       = 3
	SchemaVersion4       = 4
	SchemaVersion5       = 5
	SchemaVersion6       = 6
	SchemaVersion7       = 7
	SchemaVersion8       = 8
	CurrentSchemaVersion = SchemaVersion8
)

// Schema is KitDB's storage-neutral catalog contract. Frontends may keep richer
// authoring objects, but durable catalog JSON must decode into this shape
// without importing Kitwork runtime values.
type Schema struct {
	Version            int                 `json:"version"`
	ID                 string              `json:"id"`
	Name               string              `json:"name"`
	Hash               string              `json:"hash"`
	NextFieldTag       uint32              `json:"nextFieldTag"`
	Fields             []Field             `json:"fields"`
	UniqueConstraints  []UniqueConstraint  `json:"uniqueConstraints,omitempty"`
	ForeignConstraints []ForeignConstraint `json:"foreignConstraints,omitempty"`
	CheckConstraints   []CheckConstraint   `json:"checkConstraints,omitempty"`
	Partition          *Partition          `json:"partition,omitempty"`
}

type Field struct {
	Sequence      *SequenceDefault `json:"sequence,omitempty"`
	ID            string           `json:"id"`
	Tag           uint32           `json:"tag"`
	Name          string           `json:"name"`
	Aliases       []string         `json:"aliases,omitempty"`
	Position      int              `json:"position"`
	Kind          string           `json:"kind"`
	Precision     int              `json:"precision,omitempty"`
	Scale         int              `json:"scale,omitempty"`
	TimePrecision *int             `json:"timePrecision,omitempty"`
	TextLength    *int             `json:"textLength,omitempty"`
	ExactUUID     bool             `json:"exactUUID,omitempty"`
	Primary       bool             `json:"primary,omitempty"`
	PrimaryOrder  int              `json:"primaryOrder,omitempty"`
	NotNull       bool             `json:"notNull,omitempty"`
	Unique        bool             `json:"unique,omitempty"`
	HasDefault    bool             `json:"hasDefault,omitempty"`
	Default       json.RawMessage  `json:"default,omitempty"`
	DefaultNow    bool             `json:"defaultNow,omitempty"`
	Updated       bool             `json:"updated,omitempty"`
	Enum          []string         `json:"enum,omitempty"`
	Indexes       []IndexMember    `json:"indexes,omitempty"`
	Reference     *Reference       `json:"reference,omitempty"`
	Searchable    bool             `json:"searchable,omitempty"`
	SearchWeight  int              `json:"searchWeight,omitempty"`
	Analytics     bool             `json:"analytics,omitempty"`
}

// SequenceDefault binds by immutable sequence identity, never by a mutable
// name. Name is retained for SQL metadata; Mode controls explicit-value rules.
type SequenceDefault struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Mode string `json:"mode"`
}

type IndexMember struct {
	Name   string           `json:"name,omitempty"`
	ID     string           `json:"id,omitempty"`
	Order  int              `json:"order,omitempty"`
	Filter []IndexCondition `json:"filter,omitempty"`
}

type IndexCondition struct {
	Field string          `json:"field"`
	Value json.RawMessage `json:"value"`
}

type Reference struct {
	Struct   string `json:"struct"`
	Field    string `json:"field"`
	OnDelete string `json:"onDelete,omitempty"`
	OnUpdate string `json:"onUpdate,omitempty"`
}

type UniqueConstraint struct {
	Version int      `json:"version"`
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Fields  []uint32 `json:"fields"`
}

type ForeignConstraint struct {
	Version        int      `json:"version"`
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Fields         []uint32 `json:"fields"`
	TargetStruct   string   `json:"targetStruct"`
	TargetStructID string   `json:"targetStructId"`
	TargetFields   []string `json:"targetFields"`
	OnDelete       string   `json:"onDelete,omitempty"`
	OnUpdate       string   `json:"onUpdate,omitempty"`
}

type CheckConstraint struct {
	Version    int             `json:"version"`
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Expression CheckExpression `json:"expression"`
}

type CheckExpression struct {
	Kind      string            `json:"kind"`
	Literal   json.RawMessage   `json:"literal,omitempty"`
	Field     uint32            `json:"field,omitempty"`
	Operator  string            `json:"operator,omitempty"`
	Arguments []CheckExpression `json:"arguments,omitempty"`
	CaseBase  *CheckExpression  `json:"caseBase,omitempty"`
	Branches  []CheckCaseBranch `json:"branches,omitempty"`
	Fallback  *CheckExpression  `json:"fallback,omitempty"`
}

type CheckCaseBranch struct {
	When CheckExpression `json:"when"`
	Then CheckExpression `json:"then"`
}

// DecodeSchema proves that persisted catalog bytes are understandable without
// loading Kitwork. Unknown fields remain forward-compatible, while an unknown
// version or logical type fails closed.
func DecodeSchema(definition []byte) (Schema, error) {
	var schema Schema
	if err := json.Unmarshal(definition, &schema); err != nil {
		return Schema{}, fmt.Errorf("decode schema: %w", err)
	}
	if err := schema.Validate(); err != nil {
		return Schema{}, err
	}
	return schema, nil
}

// StableSchemaID derives the 16-byte identity used by the existing Kitwork
// struct frontend and by standalone SQL DDL. Keeping it here prevents schema
// identity from depending on either frontend.
func StableSchemaID(kind, name string) string {
	digest := sha256.Sum256([]byte("kitwork:schema:v1:" + kind + ":" + name))
	return hex.EncodeToString(digest[:16])
}

// RefreshHash publishes the deterministic logical schema hash used by the
// durable catalog. Hash itself is excluded from the digest by construction.
func (schema *Schema) RefreshHash() error {
	if schema == nil {
		return fmt.Errorf("schema is nil")
	}
	encoded, err := json.Marshal(struct {
		Version            int                 `json:"version"`
		ID                 string              `json:"id"`
		Name               string              `json:"name"`
		NextFieldTag       uint32              `json:"nextFieldTag"`
		Fields             []Field             `json:"fields"`
		UniqueConstraints  []UniqueConstraint  `json:"uniqueConstraints,omitempty"`
		ForeignConstraints []ForeignConstraint `json:"foreignConstraints,omitempty"`
		CheckConstraints   []CheckConstraint   `json:"checkConstraints,omitempty"`
		Partition          *Partition          `json:"partition,omitempty"`
	}{
		Version: schema.Version, ID: schema.ID, Name: schema.Name,
		NextFieldTag: schema.NextFieldTag, Fields: schema.Fields,
		UniqueConstraints:  schema.UniqueConstraints,
		ForeignConstraints: schema.ForeignConstraints,
		CheckConstraints:   schema.CheckConstraints,
		Partition:          schema.Partition,
	})
	if err != nil {
		return fmt.Errorf("encode schema hash: %w", err)
	}
	digest := sha256.Sum256(encoded)
	schema.Hash = hex.EncodeToString(digest[:])
	return nil
}

// VerifyHash rejects a catalog whose claimed logical identity does not match
// its definition. DecodeSchema intentionally remains forward-compatible;
// execution paths call VerifyHash before trusting field layout.
func (schema Schema) VerifyHash() error {
	claimed := schema.Hash
	if err := schema.RefreshHash(); err != nil {
		return err
	}
	if schema.Hash != claimed {
		return fmt.Errorf("schema %q hash mismatch", schema.Name)
	}
	return nil
}

// EncodeSchema validates and serializes one deterministic catalog definition.
func EncodeSchema(schema Schema) ([]byte, error) {
	if err := schema.RefreshHash(); err != nil {
		return nil, err
	}
	if err := schema.Validate(); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("encode schema: %w", err)
	}
	return encoded, nil
}

// FieldByName resolves an exact field name first and then a case-insensitive
// name or durable alias. The canonical field name is returned with the field.
func (schema Schema) FieldByName(requested string) (string, Field, bool) {
	for _, field := range schema.Fields {
		if field.Name == requested {
			return field.Name, field, true
		}
		for _, alias := range field.Aliases {
			if alias == requested {
				return field.Name, field, true
			}
		}
	}
	var canonical string
	var result Field
	for _, field := range schema.Fields {
		matches := strings.EqualFold(field.Name, requested)
		if !matches {
			for _, alias := range field.Aliases {
				if strings.EqualFold(alias, requested) {
					matches = true
					break
				}
			}
		}
		if !matches {
			continue
		}
		if canonical != "" && canonical != field.Name {
			return "", Field{}, false
		}
		canonical, result = field.Name, field
	}
	return canonical, result, canonical != ""
}

// PrimaryFields returns the ordered primary-key tuple.
func (schema Schema) PrimaryFields() []Field {
	fields := make([]Field, 0, 1)
	for _, field := range schema.Fields {
		if field.Primary {
			fields = append(fields, field)
		}
	}
	slicesSortFields(fields)
	return fields
}

func slicesSortFields(fields []Field) {
	for index := 1; index < len(fields); index++ {
		for current := index; current > 0; current-- {
			left, right := fields[current-1], fields[current]
			leftOrder, rightOrder := left.PrimaryOrder, right.PrimaryOrder
			if leftOrder == 0 {
				leftOrder = left.Position + 1
			}
			if rightOrder == 0 {
				rightOrder = right.Position + 1
			}
			if leftOrder <= rightOrder {
				break
			}
			fields[current-1], fields[current] = fields[current], fields[current-1]
		}
	}
}

func (schema Schema) Validate() error {
	if schema.Version < SchemaVersion1 || schema.Version > CurrentSchemaVersion {
		return fmt.Errorf("schema version %d is unsupported", schema.Version)
	}
	if schema.ID == "" || schema.Name == "" || schema.Hash == "" {
		return fmt.Errorf("schema identity is incomplete")
	}
	if len(schema.Fields) == 0 {
		return fmt.Errorf("schema %q has no fields", schema.Name)
	}

	ids := make(map[string]string, len(schema.Fields))
	names := make(map[string]string, len(schema.Fields)*2)
	tags := make(map[uint32]string, len(schema.Fields))
	positions := make(map[int]string, len(schema.Fields))
	intervalTags := make(map[uint32]string)
	fieldsByTag := make(map[uint32]Field, len(schema.Fields))
	var maximumTag uint32
	for _, field := range schema.Fields {
		if field.ID == "" || field.Name == "" {
			return fmt.Errorf("schema %q has a field with incomplete identity", schema.Name)
		}
		if previous := ids[field.ID]; previous != "" {
			return fmt.Errorf("schema %q fields %q and %q share id %q", schema.Name, previous, field.Name, field.ID)
		}
		ids[field.ID] = field.Name
		if previous := names[field.Name]; previous != "" {
			return fmt.Errorf("schema %q fields %q and %q share a name", schema.Name, previous, field.Name)
		}
		names[field.Name] = field.Name

		typeInfo, found := LookupKind(field.Kind)
		if !found || typeInfo.Kind != field.Kind {
			return fmt.Errorf("schema %q field %q has unsupported kind %q", schema.Name, field.Name, field.Kind)
		}
		if schema.Version < SchemaVersion5 {
			switch typeInfo.ID {
			case TypeTimestamp, TypeTimestampTZ, TypeInterval:
				return fmt.Errorf("schema %q field %q requires schema version 5 for kind %q", schema.Name, field.Name, field.Kind)
			}
		}
		if typeInfo.ID == TypeInterval {
			if field.Primary || field.Unique || len(field.Indexes) != 0 {
				return fmt.Errorf(
					"schema %q interval field %q cannot participate in primary, unique or secondary indexes",
					schema.Name, field.Name,
				)
			}
			intervalTags[field.Tag] = field.Name
		}
		if field.Precision != 0 || field.Scale != 0 {
			if schema.Version < SchemaVersion4 || typeInfo.Family != FamilyDecimal ||
				field.Precision < 1 || field.Precision > MaximumDecimalPrecision ||
				field.Scale < 0 || field.Scale > field.Precision {
				return fmt.Errorf(
					"schema %q field %q has invalid decimal precision/scale (%d,%d)",
					schema.Name, field.Name, field.Precision, field.Scale,
				)
			}
		}
		if field.TimePrecision != nil {
			if schema.Version < SchemaVersion5 || !temporalPrecisionKind(field.Kind) ||
				*field.TimePrecision < 0 || *field.TimePrecision > MaximumTemporalPrecision {
				return fmt.Errorf(
					"schema %q field %q has invalid temporal precision %d",
					schema.Name, field.Name, *field.TimePrecision,
				)
			}
		}
		if field.TextLength != nil {
			if schema.Version < SchemaVersion6 ||
				(typeInfo.ID != TypeVarchar && typeInfo.ID != TypeChar) ||
				*field.TextLength < 1 || *field.TextLength > MaximumTextLength {
				return fmt.Errorf(
					"schema %q field %q has invalid text length %d",
					schema.Name, field.Name, *field.TextLength,
				)
			}
		}
		if field.ExactUUID && (schema.Version < SchemaVersion7 || typeInfo.ID != TypeUUID) {
			return fmt.Errorf(
				"schema %q field %q has an invalid exact UUID profile",
				schema.Name, field.Name,
			)
		}
		if field.Sequence != nil {
			sequence := field.Sequence
			decoded, err := hex.DecodeString(sequence.ID)
			if schema.Version < SchemaVersion3 || err != nil || len(decoded) != 16 || sequence.ID != strings.ToLower(sequence.ID) ||
				sequence.Name == "" || !sequenceCompatibleKind(field.Kind) || field.HasDefault || field.DefaultNow || field.Updated {
				return fmt.Errorf("schema %q field %q has an invalid sequence default", schema.Name, field.Name)
			}
			switch sequence.Mode {
			case "default":
			case "serial", "always", "by_default":
				if !field.NotNull {
					return fmt.Errorf("schema %q generated field %q must be NOT NULL", schema.Name, field.Name)
				}
			default:
				return fmt.Errorf("schema %q field %q has an invalid identity mode", schema.Name, field.Name)
			}
		}
		if field.Searchable {
			if typeInfo.Family != FamilyText && typeInfo.Family != FamilyIdentifier &&
				typeInfo.Family != FamilyChoice {
				return fmt.Errorf("schema %q searchable field %q is not text-compatible", schema.Name, field.Name)
			}
			if field.SearchWeight < 0 || field.SearchWeight > 16 {
				return fmt.Errorf("schema %q searchable field %q has invalid weight %d", schema.Name, field.Name, field.SearchWeight)
			}
		} else if field.SearchWeight != 0 {
			return fmt.Errorf("schema %q field %q has a search weight but is not searchable", schema.Name, field.Name)
		}
		if field.Analytics {
			switch typeInfo.Family {
			case FamilyInteger, FamilySystem, FamilyFloat, FamilyBoolean, FamilyText, FamilyIdentifier, FamilyChoice:
			default:
				return fmt.Errorf("schema %q analytics field %q has unsupported kind %q", schema.Name, field.Name, field.Kind)
			}
		}
		if typeInfo.ID == TypeChoice && len(field.Enum) == 0 {
			return fmt.Errorf("schema %q choice field %q has no values", schema.Name, field.Name)
		}
		if typeInfo.ID == TypeChoice {
			choices := make(map[string]struct{}, len(field.Enum))
			for _, choice := range field.Enum {
				if choice == "" {
					return fmt.Errorf("schema %q choice field %q has an empty value", schema.Name, field.Name)
				}
				if _, duplicate := choices[choice]; duplicate {
					return fmt.Errorf("schema %q choice field %q repeats value %q", schema.Name, field.Name, choice)
				}
				choices[choice] = struct{}{}
			}
		}

		if schema.Version >= SchemaVersion2 {
			if field.Tag == 0 {
				return fmt.Errorf("schema %q field %q uses reserved tag 0", schema.Name, field.Name)
			}
			if previous := tags[field.Tag]; previous != "" {
				return fmt.Errorf("schema %q fields %q and %q share tag %d", schema.Name, previous, field.Name, field.Tag)
			}
			tags[field.Tag] = field.Name
			fieldsByTag[field.Tag] = field
			if field.Tag > maximumTag {
				maximumTag = field.Tag
			}
			if field.Position < 0 || field.Position >= len(schema.Fields) {
				return fmt.Errorf("schema %q field %q has invalid position %d", schema.Name, field.Name, field.Position)
			}
			if previous := positions[field.Position]; previous != "" {
				return fmt.Errorf("schema %q fields %q and %q share position %d", schema.Name, previous, field.Name, field.Position)
			}
			positions[field.Position] = field.Name
		}

		previousAlias := ""
		for index, alias := range field.Aliases {
			if alias == "" || alias == field.Name {
				return fmt.Errorf("schema %q field %q has invalid alias %q", schema.Name, field.Name, alias)
			}
			if index != 0 && alias <= previousAlias {
				return fmt.Errorf("schema %q field %q aliases are not strictly ordered", schema.Name, field.Name)
			}
			previousAlias = alias
			if previous := names[alias]; previous != "" {
				return fmt.Errorf("schema %q alias %q belongs to fields %q and %q", schema.Name, alias, previous, field.Name)
			}
			names[alias] = field.Name
		}
	}
	if schema.Version >= SchemaVersion2 && (schema.NextFieldTag == 0 || schema.NextFieldTag <= maximumTag) {
		return fmt.Errorf("schema %q next field tag %d must be greater than %d", schema.Name, schema.NextFieldTag, maximumTag)
	}
	if schema.Partition != nil {
		partition := schema.Partition
		field, found := fieldsByTag[partition.Field]
		typeInfo, typeFound := LookupKind(field.Kind)
		if schema.Version < SchemaVersion8 || partition.Version != PartitionVersion1 || !found || !typeFound || typeInfo.Family != FamilyInteger {
			return fmt.Errorf("schema %q has an invalid partition policy", schema.Name)
		}
		switch partition.Strategy {
		case "hash":
			if partition.Buckets != PartitionHashBuckets {
				return fmt.Errorf("schema %q HASH partition needs %d buckets", schema.Name, PartitionHashBuckets)
			}
		case "range":
			if partition.Buckets != 0 {
				return fmt.Errorf("schema %q RANGE partition cannot declare hash buckets", schema.Name)
			}
		default:
			return fmt.Errorf("schema %q has unsupported partition strategy %q", schema.Name, partition.Strategy)
		}
	}
	for _, constraint := range schema.UniqueConstraints {
		for _, tag := range constraint.Fields {
			if field := intervalTags[tag]; field != "" {
				return fmt.Errorf(
					"schema %q interval field %q cannot participate in primary, unique or secondary indexes",
					schema.Name, field,
				)
			}
		}
	}
	return nil
}

func sequenceCompatibleKind(kind string) bool {
	_, valid := SequenceDataTypeForKind(kind)
	return valid
}

func temporalPrecisionKind(kind string) bool {
	switch kind {
	case "time", "timestamp", "timestamptz":
		return true
	default:
		return false
	}
}
