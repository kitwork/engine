package work

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/kitwork/engine/value"
)

const structIRVersion = 1

// StructDef is Kitwork's normalized, backend-neutral schema contract. The
// authoring syntax is struct({ ... }); storage engines consume this IR rather
// than inspecting arbitrary application code or dialect-specific DDL.
type StructDef struct {
	Version int              `json:"version"`
	ID      string           `json:"id"`
	Name    string           `json:"name"`
	Hash    string           `json:"hash"`
	Fields  []StructFieldDef `json:"fields"`

	columns map[string]*ColumnSpec
}

// StructFieldDef is the deterministic field representation shared by storage,
// migration, validation, search and future compiler metadata.
type StructFieldDef struct {
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	Position     int                 `json:"position"`
	Kind         string              `json:"kind"`
	Primary      bool                `json:"primary,omitempty"`
	NotNull      bool                `json:"notNull,omitempty"`
	Unique       bool                `json:"unique,omitempty"`
	HasDefault   bool                `json:"hasDefault,omitempty"`
	Default      value.Value         `json:"default,omitempty"`
	DefaultNow   bool                `json:"defaultNow,omitempty"`
	Updated      bool                `json:"updated,omitempty"`
	Enum         []string            `json:"enum,omitempty"`
	Indexes      []StructIndexMember `json:"indexes,omitempty"`
	Reference    *StructReferenceDef `json:"reference,omitempty"`
	Searchable   bool                `json:"searchable,omitempty"`
	SearchWeight int                 `json:"searchWeight,omitempty"`
}

type StructIndexMember struct {
	Name   string                 `json:"name,omitempty"`
	Order  int                    `json:"order,omitempty"`
	Filter []StructIndexCondition `json:"filter,omitempty"`
}

type StructIndexCondition struct {
	Field string      `json:"field"`
	Value value.Value `json:"value"`
}

type StructReferenceDef struct {
	Struct   string `json:"struct"`
	Field    string `json:"field"`
	OnDelete string `json:"onDelete,omitempty"`
	OnUpdate string `json:"onUpdate,omitempty"`
}

// Struct is the single canonical schema authoring form:
//
//	const products = struct({ id: id(), title: text().notNull() });
//
// The enclosing database declaration supplies the persistent struct name, so
// application code does not repeat "products" in two places.
func (d *Database) Struct() value.Value {
	return value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) != 1 || args[0].K != value.Map {
			return value.Value{K: value.Invalid, V: "db.struct: expects one field object"}
		}
		columns := make(map[string]*ColumnSpec, len(args[0].Map()))
		for name, field := range args[0].Map() {
			spec, ok := field.V.(*ColumnSpec)
			if !ok || spec == nil {
				return value.Value{K: value.Invalid, V: fmt.Sprintf("db.struct: field %q is not a column type", name)}
			}
			columns[name] = spec
		}
		if len(columns) == 0 {
			return value.Value{K: value.Invalid, V: "db.struct: needs at least one field"}
		}
		return value.Value{K: value.Proxy, V: &StructDef{Version: structIRVersion, columns: columns}}
	})
}

// OnGet exposes stable field objects, so ref(users.id) keeps working when a
// schema moves from a plain object to struct({ ... }).
func (definition *StructDef) OnGet(name string) value.Value {
	if definition != nil {
		if field := definition.columns[name]; field != nil {
			return value.New(field)
		}
	}
	return value.Value{K: value.Invalid, V: fmt.Sprintf("db.struct: no field %q", name)}
}

func (definition *StructDef) OnCompare(_ string, _ value.Value) value.Value {
	return value.NewNil()
}

func (definition *StructDef) OnInvoke(_ string, _ ...value.Value) value.Value {
	return value.Value{K: value.Invalid, V: "db.struct: a definition is not callable"}
}

// Kitdb binds named structs to KitDB while preserving the same db.<name> ORM
// used by the SQLite and Turso schema backends.
func (d *Database) Kitdb(args ...value.Value) value.Value {
	return newDbProxy(d.tenant, d.requestScope, "kitdb", args...)
}

func bindStructDef(name string, source *StructDef, columns map[string]*ColumnSpec) *StructDef {
	definition := &StructDef{
		Version: structIRVersion,
		ID:      stableSchemaID("struct", name),
		Name:    name,
		columns: make(map[string]*ColumnSpec, len(columns)),
	}
	if source != nil && source.Version != 0 {
		definition.Version = source.Version
	}
	for column, spec := range columns {
		definition.columns[column] = spec
	}
	for position, fieldName := range orderedColumns(columns) {
		spec := columns[fieldName]
		field := StructFieldDef{
			ID:           stableSchemaID("field", definition.ID+":"+fieldName),
			Name:         fieldName,
			Position:     position,
			Kind:         spec.kind,
			Primary:      spec.primary,
			NotNull:      spec.notNull || spec.primary,
			Unique:       spec.unique || spec.primary,
			HasDefault:   spec.hasDefault,
			Default:      spec.def,
			DefaultNow:   spec.defaultNow,
			Updated:      spec.touch,
			Enum:         append([]string(nil), spec.enumVals...),
			Searchable:   spec.searchable,
			SearchWeight: spec.searchWt,
		}
		for _, index := range spec.indexes {
			member := StructIndexMember{Name: index.name, Order: index.pos}
			for _, condition := range index.filter {
				member.Filter = append(member.Filter, StructIndexCondition{Field: condition.col, Value: condition.val})
			}
			sort.Slice(member.Filter, func(left, right int) bool {
				return member.Filter[left].Field < member.Filter[right].Field
			})
			field.Indexes = append(field.Indexes, member)
		}
		if spec.fk != nil {
			field.Reference = &StructReferenceDef{
				Struct: spec.fk.table, Field: spec.fk.column,
				OnDelete: spec.fk.onDelete, OnUpdate: spec.fk.onUpdate,
			}
		}
		definition.Fields = append(definition.Fields, field)
	}
	encoded, _ := json.Marshal(struct {
		Version int              `json:"version"`
		ID      string           `json:"id"`
		Name    string           `json:"name"`
		Fields  []StructFieldDef `json:"fields"`
	}{definition.Version, definition.ID, definition.Name, definition.Fields})
	digest := sha256.Sum256(encoded)
	definition.Hash = hex.EncodeToString(digest[:])
	return definition
}

func stableSchemaID(kind, name string) string {
	digest := sha256.Sum256([]byte("kitwork:schema:v1:" + kind + ":" + name))
	return hex.EncodeToString(digest[:16])
}

func (definition *StructDef) primaryField() (StructFieldDef, bool) {
	if definition == nil {
		return StructFieldDef{}, false
	}
	for _, field := range definition.Fields {
		if field.Primary {
			return field, true
		}
	}
	return StructFieldDef{}, false
}
