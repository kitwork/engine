package work

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
	"github.com/kitwork/engine/value"
)

const (
	structIRVersion          = 2
	structIRMaximumVersion   = kitdbsql.SchemaVersion8
	structIRPartitionVersion = kitdbsql.SchemaVersion8
)

// StructDef is Kitwork's normalized, backend-neutral schema contract. The
// authoring syntax is struct({ ... }); storage engines consume this IR rather
// than inspecting arbitrary application code or dialect-specific DDL.
type StructDef struct {
	Version            int                       `json:"version"`
	ID                 string                    `json:"id"`
	Name               string                    `json:"name"`
	Hash               string                    `json:"hash"`
	NextFieldTag       uint32                    `json:"nextFieldTag"`
	Fields             []StructFieldDef          `json:"fields"`
	UniqueConstraints  []StructUniqueConstraint  `json:"uniqueConstraints,omitempty"`
	ForeignConstraints []StructForeignConstraint `json:"foreignConstraints,omitempty"`
	CheckConstraints   []StructCheckConstraint   `json:"checkConstraints,omitempty"`
	Partition          *kitdbsql.Partition       `json:"partition,omitempty"`

	columns map[string]*ColumnSpec
	byName  map[string]int
	byTag   map[uint32]int
	byID    map[string]int
	byAlias map[string]int

	// catalogHash keeps the physical predecessor identity while an older
	// catalog is normalized in memory. It never enters the persisted IR.
	catalogHash         string
	catalogNeedsUpgrade bool
	// catalogOwned marks a definition the proxy adopted from the durable
	// catalog rather than from a source declaration. Such a definition follows
	// the catalog and never reconciles it: a reader that snapshotted it before a
	// concurrent DDL committed must not plan the struct back to what it saw.
	catalogOwned   bool
	constraintErr  string
	compiledChecks []compiledStructCheck
}

// StructFieldDef is the deterministic field representation shared by storage,
// migration, validation, search and future compiler metadata.
type StructFieldDef struct {
	ID           string              `json:"id"`
	Tag          uint32              `json:"tag"`
	Name         string              `json:"name"`
	Aliases      []string            `json:"aliases,omitempty"`
	Position     int                 `json:"position"`
	Kind         string              `json:"kind"`
	Primary      bool                `json:"primary,omitempty"`
	PrimaryOrder int                 `json:"primaryOrder,omitempty"`
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
	Analytics    bool                `json:"analytics,omitempty"`

	// From is an authoring-only migration hint and never enters the persisted
	// catalog or schema hash.
	From string `json:"-"`
}

type StructIndexMember struct {
	Name   string                 `json:"name,omitempty"`
	ID     string                 `json:"id,omitempty"`
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

// StructUniqueConstraint is a versioned, field-tag-based named relational
// constraint. Tags keep its identity stable when a field is deliberately
// renamed; one or more fields are supported.
type StructUniqueConstraint struct {
	Version int      `json:"version"`
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Fields  []uint32 `json:"fields"`
}

// StructForeignConstraint stores named local membership by stable field tag
// and the referenced key by stable field ID. Both sides therefore survive
// deliberate field renames without changing relational identity.
type StructForeignConstraint struct {
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

type columnUniqueGroup struct {
	name    string
	columns []string
}

type columnForeignGroup struct {
	name       string
	columns    []string
	references []*fkRef
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
// used by the SQLite schema backend.
func (d *Database) Kitdb(args ...value.Value) value.Value {
	return newDbProxy(d.tenant, d.requestScope, "kitdb", args...)
}

func bindStructDef(name string, source *StructDef, columns map[string]*ColumnSpec) *StructDef {
	return bindStructDefWithID(name, "", source, columns)
}

func bindStructDefWithID(name, id string, source *StructDef, columns map[string]*ColumnSpec) *StructDef {
	if id == "" {
		id = stableSchemaID("struct", name)
	}
	definition := &StructDef{
		Version: structIRVersion,
		ID:      id,
		Name:    name,
		columns: make(map[string]*ColumnSpec, len(columns)),
	}
	if source != nil && source.Version != 0 {
		definition.Version = source.Version
	}
	for column, spec := range columns {
		definition.columns[column] = spec
	}
	primaryCount := 0
	for _, spec := range columns {
		if spec.primary {
			primaryCount++
		}
	}
	for position, fieldName := range orderedColumns(columns) {
		spec := columns[fieldName]
		field := StructFieldDef{
			ID:           stableSchemaID("field", definition.ID+":"+fieldIdentityName(fieldName, spec)),
			Tag:          uint32(position + 1),
			Name:         fieldName,
			Position:     position,
			Kind:         spec.kind,
			Primary:      spec.primary,
			PrimaryOrder: spec.primaryOrder,
			NotNull:      spec.notNull || spec.primary,
			Unique:       spec.unique || (spec.primary && primaryCount == 1),
			HasDefault:   spec.hasDefault,
			Default:      spec.def,
			DefaultNow:   spec.defaultNow,
			Updated:      spec.touch,
			Enum:         append([]string(nil), spec.enumVals...),
			Searchable:   spec.searchable,
			SearchWeight: spec.searchWt,
			Analytics:    spec.analytics,
			From:         spec.from,
		}
		for _, index := range spec.indexes {
			member := StructIndexMember{Name: index.name, ID: index.id, Order: index.pos}
			for _, condition := range index.filter {
				member.Filter = append(member.Filter, StructIndexCondition{Field: condition.col, Value: condition.val})
			}
			sort.Slice(member.Filter, func(left, right int) bool {
				return member.Filter[left].Field < member.Filter[right].Field
			})
			field.Indexes = append(field.Indexes, member)
		}
		if spec.fk != nil && spec.fk.name == "" {
			field.Reference = &StructReferenceDef{
				Struct: spec.fk.table, Field: spec.fk.column,
				OnDelete: spec.fk.onDelete, OnUpdate: spec.fk.onUpdate,
			}
		}
		definition.Fields = append(definition.Fields, field)
		if spec.partition != "" {
			if definition.Partition != nil {
				if definition.constraintErr == "" {
					definition.constraintErr = "partition() can be declared on only one field per struct"
				}
				continue
			}
			typeInfo, found := kitdbsql.LookupKind(spec.kind)
			if !found || typeInfo.Family != kitdbsql.FamilyInteger {
				if definition.constraintErr == "" {
					definition.constraintErr = fmt.Sprintf("field %q: partition() currently requires an integer field", fieldName)
				}
				continue
			}
			partition := &kitdbsql.Partition{
				Version:  kitdbsql.PartitionVersion1,
				Field:    field.Tag,
				Strategy: spec.partition,
			}
			if spec.partition == "hash" {
				partition.Buckets = kitdbsql.PartitionHashBuckets
			}
			definition.Partition = partition
			definition.Version = structIRPartitionVersion
		}
	}
	if source != nil && len(source.Fields) != 0 {
		definition.CheckConstraints = cloneStructCheckConstraints(source.CheckConstraints)
		sourceNames := make(map[uint32]string, len(source.Fields))
		currentTags := make(map[string]uint32, len(definition.Fields))
		for _, field := range source.Fields {
			sourceNames[field.Tag] = field.Name
		}
		for _, field := range definition.Fields {
			currentTags[field.Name] = field.Tag
		}
		for index := range definition.CheckConstraints {
			if err := remapStructCheckFieldTags(
				&definition.CheckConstraints[index].Expression, sourceNames, currentTags,
			); err != nil && definition.constraintErr == "" {
				definition.constraintErr = err.Error()
			}
		}
	}
	if checkErr := appendColumnCheckConstraints(definition); checkErr != nil && definition.constraintErr == "" {
		definition.constraintErr = checkErr.Error()
	}
	groups, err := collectColumnUniqueGroups(columns)
	if err != nil {
		definition.constraintErr = err.Error()
	} else {
		tags := make(map[string]uint32, len(definition.Fields))
		for _, field := range definition.Fields {
			tags[field.Name] = field.Tag
		}
		for _, group := range groups {
			constraint := StructUniqueConstraint{
				Version: 1,
				ID:      stableSchemaID("unique", definition.ID+":"+strings.ToLower(group.name)),
				Name:    group.name,
				Fields:  make([]uint32, len(group.columns)),
			}
			for index, column := range group.columns {
				constraint.Fields[index] = tags[column]
			}
			definition.UniqueConstraints = append(definition.UniqueConstraints, constraint)
		}
	}
	foreignGroups, foreignErr := collectColumnForeignGroups(columns)
	if foreignErr != nil && definition.constraintErr == "" {
		definition.constraintErr = foreignErr.Error()
	}
	if foreignErr == nil {
		tags := make(map[string]uint32, len(definition.Fields))
		for _, field := range definition.Fields {
			tags[field.Name] = field.Tag
		}
		for _, group := range foreignGroups {
			constraint := StructForeignConstraint{
				Version:      1,
				ID:           stableSchemaID("foreign", definition.ID+":"+strings.ToLower(group.name)),
				Name:         group.name,
				Fields:       make([]uint32, len(group.columns)),
				TargetFields: make([]string, len(group.columns)),
			}
			for index, column := range group.columns {
				reference := group.references[index]
				constraint.Fields[index] = tags[column]
				constraint.TargetStruct = reference.table
				constraint.TargetStructID = reference.targetStructID
				if constraint.TargetStructID == "" {
					constraint.TargetStructID = stableSchemaID("struct", reference.table)
				}
				constraint.TargetFields[index] = reference.targetFieldID
				if constraint.TargetFields[index] == "" {
					constraint.TargetFields[index] = stableSchemaID(
						"field", constraint.TargetStructID+":"+fieldIdentityName(reference.column, reference.target),
					)
				}
				constraint.OnDelete, constraint.OnUpdate = reference.onDelete, reference.onUpdate
			}
			definition.ForeignConstraints = append(definition.ForeignConstraints, constraint)
		}
	}
	definition.NextFieldTag = uint32(len(definition.Fields) + 1)
	refreshStructHash(definition)
	if err := prepareStructCheckConstraints(definition); err != nil && definition.constraintErr == "" {
		definition.constraintErr = err.Error()
	}
	return definition
}

func fieldIdentityName(name string, spec *ColumnSpec) string {
	if spec != nil && spec.from != "" {
		return spec.from
	}
	return name
}

func refreshStructHash(definition *StructDef) {
	if definition == nil {
		return
	}
	encoded, _ := json.Marshal(struct {
		Version            int                       `json:"version"`
		ID                 string                    `json:"id"`
		Name               string                    `json:"name"`
		NextFieldTag       uint32                    `json:"nextFieldTag"`
		Fields             []StructFieldDef          `json:"fields"`
		UniqueConstraints  []StructUniqueConstraint  `json:"uniqueConstraints,omitempty"`
		ForeignConstraints []StructForeignConstraint `json:"foreignConstraints,omitempty"`
		CheckConstraints   []StructCheckConstraint   `json:"checkConstraints,omitempty"`
		Partition          *kitdbsql.Partition       `json:"partition,omitempty"`
	}{
		Version: definition.Version, ID: definition.ID, Name: definition.Name,
		NextFieldTag: definition.NextFieldTag, Fields: definition.Fields,
		UniqueConstraints: definition.UniqueConstraints, ForeignConstraints: definition.ForeignConstraints,
		CheckConstraints: definition.CheckConstraints,
		Partition:        definition.Partition,
	})
	digest := sha256.Sum256(encoded)
	definition.Hash = hex.EncodeToString(digest[:])
	refreshStructLookups(definition)
}

func collectColumnUniqueGroups(columns map[string]*ColumnSpec) ([]columnUniqueGroup, error) {
	type member struct {
		column string
		ref    colUniqueRef
		seq    uint64
	}
	type group struct {
		name    string
		members []member
	}
	groups := make(map[string]*group)
	for _, column := range orderedColumns(columns) {
		spec := columns[column]
		for _, ref := range spec.uniques {
			key := strings.ToLower(strings.TrimSpace(ref.name))
			if key == "" {
				return nil, fmt.Errorf("field %q has an empty tuple-unique name", column)
			}
			current := groups[key]
			if current == nil {
				current = &group{name: strings.TrimSpace(ref.name)}
				groups[key] = current
			}
			for _, existing := range current.members {
				if existing.column == column {
					return nil, fmt.Errorf("field %q repeats tuple-unique %q", column, current.name)
				}
			}
			current.members = append(current.members, member{column: column, ref: ref, seq: spec.seq})
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]columnUniqueGroup, 0, len(keys))
	for _, key := range keys {
		current := groups[key]
		positioned := 0
		positions := make(map[int]string, len(current.members))
		for _, item := range current.members {
			if item.ref.pos == 0 {
				continue
			}
			positioned++
			if previous := positions[item.ref.pos]; previous != "" {
				return nil, fmt.Errorf(
					"tuple-unique %q fields %q and %q share position %d",
					current.name, previous, item.column, item.ref.pos,
				)
			}
			positions[item.ref.pos] = item.column
		}
		if positioned != 0 && positioned != len(current.members) {
			return nil, fmt.Errorf("tuple-unique %q must position every field or none", current.name)
		}
		allPositioned := positioned == len(current.members)
		if allPositioned {
			for position := 1; position <= len(current.members); position++ {
				if positions[position] == "" {
					return nil, fmt.Errorf("tuple-unique %q positions must be contiguous from 1", current.name)
				}
			}
		}
		sort.SliceStable(current.members, func(left, right int) bool {
			if allPositioned {
				return current.members[left].ref.pos < current.members[right].ref.pos
			}
			if current.members[left].seq != current.members[right].seq {
				return current.members[left].seq < current.members[right].seq
			}
			return current.members[left].column < current.members[right].column
		})
		columns := make([]string, len(current.members))
		for index, item := range current.members {
			columns[index] = item.column
		}
		result = append(result, columnUniqueGroup{name: current.name, columns: columns})
	}
	return result, nil
}

func collectColumnForeignGroups(columns map[string]*ColumnSpec) ([]columnForeignGroup, error) {
	type member struct {
		column    string
		reference *fkRef
		seq       uint64
	}
	type group struct {
		name    string
		members []member
	}
	groups := make(map[string]*group)
	for _, column := range orderedColumns(columns) {
		spec := columns[column]
		if spec == nil || spec.fk == nil || spec.fk.name == "" {
			continue
		}
		name := strings.TrimSpace(spec.fk.name)
		key := strings.ToLower(name)
		if key == "" {
			return nil, fmt.Errorf("field %q has an empty composite foreign-key name", column)
		}
		current := groups[key]
		if current == nil {
			current = &group{name: name}
			groups[key] = current
		}
		current.members = append(current.members, member{column: column, reference: spec.fk, seq: spec.seq})
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]columnForeignGroup, 0, len(keys))
	for _, key := range keys {
		current := groups[key]
		positioned := 0
		positions := make(map[int]string, len(current.members))
		for _, item := range current.members {
			if item.reference.table == "" ||
				(item.reference.target == nil && item.reference.targetFieldID == "") {
				return nil, fmt.Errorf("composite foreign key %q field %q has an unresolved target", current.name, item.column)
			}
			if item.reference.pos == 0 {
				continue
			}
			positioned++
			if previous := positions[item.reference.pos]; previous != "" {
				return nil, fmt.Errorf("composite foreign key %q fields %q and %q share position %d", current.name, previous, item.column, item.reference.pos)
			}
			positions[item.reference.pos] = item.column
		}
		if positioned != 0 && positioned != len(current.members) {
			return nil, fmt.Errorf("composite foreign key %q must position every field or none", current.name)
		}
		allPositioned := positioned == len(current.members)
		if allPositioned {
			for position := 1; position <= len(current.members); position++ {
				if positions[position] == "" {
					return nil, fmt.Errorf("composite foreign key %q positions must be contiguous from 1", current.name)
				}
			}
		}
		sort.SliceStable(current.members, func(left, right int) bool {
			if allPositioned {
				return current.members[left].reference.pos < current.members[right].reference.pos
			}
			if current.members[left].seq != current.members[right].seq {
				return current.members[left].seq < current.members[right].seq
			}
			return current.members[left].column < current.members[right].column
		})
		first := current.members[0].reference
		targets := make(map[string]string, len(current.members))
		groupResult := columnForeignGroup{name: current.name}
		for _, item := range current.members {
			reference := item.reference
			if reference.table != first.table {
				return nil, fmt.Errorf("composite foreign key %q must reference one target struct", current.name)
			}
			if comparableFKActionName(reference.onDelete) != comparableFKActionName(first.onDelete) ||
				comparableFKActionName(reference.onUpdate) != comparableFKActionName(first.onUpdate) {
				return nil, fmt.Errorf("composite foreign key %q members must use the same actions", current.name)
			}
			targetKey := strings.ToLower(reference.column)
			if targetKey == "" {
				targetKey = reference.targetFieldID
			}
			if previous := targets[targetKey]; previous != "" {
				return nil, fmt.Errorf("composite foreign key %q repeats target field %q", current.name, previous)
			}
			targets[targetKey] = reference.column
			groupResult.columns = append(groupResult.columns, item.column)
			groupResult.references = append(groupResult.references, reference)
		}
		result = append(result, groupResult)
	}
	return result, nil
}

func validateStructUniqueConstraints(definition *StructDef) error {
	if definition == nil {
		return fmt.Errorf("unique constraints are unavailable")
	}
	if definition.constraintErr != "" {
		return fmt.Errorf("%s", definition.constraintErr)
	}
	tags := make(map[uint32]StructFieldDef, len(definition.Fields))
	reservedNames := make(map[string]string)
	seenFields := make(map[string]string, len(definition.UniqueConstraints)+len(definition.Fields))
	for _, field := range definition.Fields {
		tags[field.Tag] = field
		if field.Primary {
			name := definition.Name + "_pkey"
			reservedNames[strings.ToLower(name)] = name
			seenFields[fmt.Sprint(field.Tag)] = name
		} else if field.Unique {
			name := "unique_" + definition.Name + "_" + field.Name
			reservedNames[strings.ToLower(name)] = name
			seenFields[fmt.Sprint(field.Tag)] = name
		}
	}
	if definition.columns != nil {
		for _, index := range collectIndexes(definition.Name, definition.columns) {
			reservedNames[strings.ToLower(index.name)] = index.name
		}
	}
	seenNames := make(map[string]string, len(definition.UniqueConstraints))
	previousName := ""
	for _, constraint := range definition.UniqueConstraints {
		name := strings.TrimSpace(constraint.Name)
		key := strings.ToLower(name)
		if constraint.Version != 1 {
			return fmt.Errorf("tuple-unique %q uses unsupported version %d", constraint.Name, constraint.Version)
		}
		if name == "" || name != constraint.Name {
			return fmt.Errorf("tuple-unique has an invalid name %q", constraint.Name)
		}
		if previousName != "" && key <= previousName {
			return fmt.Errorf("tuple-unique constraints are not strictly ordered by name")
		}
		previousName = key
		if previous, duplicate := seenNames[key]; duplicate {
			return fmt.Errorf("tuple-unique constraints %q and %q share a name", previous, constraint.Name)
		}
		seenNames[key] = constraint.Name
		if reserved := reservedNames[key]; reserved != "" {
			return fmt.Errorf("tuple-unique %q conflicts with index %q", constraint.Name, reserved)
		}
		if constraint.ID != stableSchemaID("unique", definition.ID+":"+key) {
			return fmt.Errorf("tuple-unique %q has an invalid identity", constraint.Name)
		}
		if len(constraint.Fields) == 0 {
			return fmt.Errorf("named unique %q needs at least one field", constraint.Name)
		}
		if len(constraint.Fields) > len(definition.Fields) {
			return fmt.Errorf("tuple-unique %q has too many fields", constraint.Name)
		}
		memberTags := make(map[uint32]struct{}, len(constraint.Fields))
		setTags := append([]uint32(nil), constraint.Fields...)
		for _, tag := range constraint.Fields {
			field, found := tags[tag]
			if !found {
				return fmt.Errorf("tuple-unique %q references missing field tag %d", constraint.Name, tag)
			}
			if _, duplicate := memberTags[tag]; duplicate {
				return fmt.Errorf("tuple-unique %q repeats field %q", constraint.Name, field.Name)
			}
			memberTags[tag] = struct{}{}
		}
		sort.Slice(setTags, func(left, right int) bool { return setTags[left] < setTags[right] })
		parts := make([]string, len(setTags))
		for index, tag := range setTags {
			parts[index] = fmt.Sprint(tag)
		}
		signature := strings.Join(parts, ",")
		if previous, duplicate := seenFields[signature]; duplicate {
			return fmt.Errorf("tuple-unique constraints %q and %q cover the same fields", previous, constraint.Name)
		}
		seenFields[signature] = constraint.Name
	}
	if definition.columns != nil {
		groups, err := collectColumnUniqueGroups(definition.columns)
		if err != nil {
			return err
		}
		if len(groups) != len(definition.UniqueConstraints) {
			return fmt.Errorf("tuple-unique column memberships do not match the schema IR")
		}
		tagsByName := make(map[string]uint32, len(definition.Fields))
		for _, field := range definition.Fields {
			tagsByName[field.Name] = field.Tag
		}
		for index, group := range groups {
			constraint := definition.UniqueConstraints[index]
			if !strings.EqualFold(group.name, constraint.Name) || len(group.columns) != len(constraint.Fields) {
				return fmt.Errorf("tuple-unique %q column membership does not match the schema IR", group.name)
			}
			for position, column := range group.columns {
				if tagsByName[column] != constraint.Fields[position] {
					return fmt.Errorf("tuple-unique %q field order does not match the schema IR", group.name)
				}
			}
		}
	}
	return nil
}

func validateStructForeignConstraintShape(definition *StructDef) error {
	if definition == nil {
		return fmt.Errorf("foreign constraints are unavailable")
	}
	if definition.constraintErr != "" {
		return fmt.Errorf("%s", definition.constraintErr)
	}
	fieldsByTag := make(map[uint32]StructFieldDef, len(definition.Fields))
	for _, field := range definition.Fields {
		fieldsByTag[field.Tag] = field
	}
	seenNames := make(map[string]string, len(definition.ForeignConstraints))
	previousName := ""
	for _, constraint := range definition.ForeignConstraints {
		name := strings.TrimSpace(constraint.Name)
		key := strings.ToLower(name)
		if constraint.Version != 1 {
			return fmt.Errorf("composite foreign key %q uses unsupported version %d", constraint.Name, constraint.Version)
		}
		if name == "" || name != constraint.Name {
			return fmt.Errorf("composite foreign key has an invalid name %q", constraint.Name)
		}
		if previousName != "" && key <= previousName {
			return fmt.Errorf("composite foreign keys are not strictly ordered by name")
		}
		previousName = key
		if previous := seenNames[key]; previous != "" {
			return fmt.Errorf("composite foreign keys %q and %q share a name", previous, constraint.Name)
		}
		seenNames[key] = constraint.Name
		if constraint.ID != stableSchemaID("foreign", definition.ID+":"+key) {
			return fmt.Errorf("composite foreign key %q has an invalid identity", constraint.Name)
		}
		if len(constraint.Fields) == 0 || len(constraint.Fields) != len(constraint.TargetFields) {
			return fmt.Errorf("named foreign key %q must pair at least one local and target field", constraint.Name)
		}
		if constraint.TargetStruct == "" || constraint.TargetStructID == "" {
			return fmt.Errorf("composite foreign key %q has an incomplete target", constraint.Name)
		}
		if !validSchemaID(constraint.TargetStructID) {
			return fmt.Errorf("composite foreign key %q has an invalid target identity", constraint.Name)
		}
		if !validFKActionName(constraint.OnDelete) || !validFKActionName(constraint.OnUpdate) {
			return fmt.Errorf("composite foreign key %q has an unsupported action", constraint.Name)
		}
		localSeen := make(map[uint32]struct{}, len(constraint.Fields))
		targetSeen := make(map[string]struct{}, len(constraint.TargetFields))
		for index, tag := range constraint.Fields {
			field, found := fieldsByTag[tag]
			if !found {
				return fmt.Errorf("composite foreign key %q references missing local field tag %d", constraint.Name, tag)
			}
			if _, duplicate := localSeen[tag]; duplicate {
				return fmt.Errorf("composite foreign key %q repeats local field %q", constraint.Name, field.Name)
			}
			localSeen[tag] = struct{}{}
			targetID := constraint.TargetFields[index]
			if targetID == "" {
				return fmt.Errorf("composite foreign key %q has an empty target field identity", constraint.Name)
			}
			if _, duplicate := targetSeen[targetID]; duplicate {
				return fmt.Errorf("composite foreign key %q repeats a target field", constraint.Name)
			}
			targetSeen[targetID] = struct{}{}
			if (normalizeFKActionName(constraint.OnDelete) == "setnull" ||
				normalizeFKActionName(constraint.OnUpdate) == "setnull") && field.NotNull {
				return fmt.Errorf("composite foreign key %q cannot SET NULL on local field %q", constraint.Name, field.Name)
			}
		}
	}
	if definition.columns != nil {
		groups, err := collectColumnForeignGroups(definition.columns)
		if err != nil {
			return err
		}
		if len(groups) != len(definition.ForeignConstraints) {
			return fmt.Errorf("composite foreign-key column memberships do not match the schema IR")
		}
		fieldsByName := make(map[string]StructFieldDef, len(definition.Fields))
		for _, field := range definition.Fields {
			fieldsByName[field.Name] = field
		}
		for index, group := range groups {
			constraint := definition.ForeignConstraints[index]
			if !strings.EqualFold(group.name, constraint.Name) || len(group.columns) != len(constraint.Fields) {
				return fmt.Errorf("composite foreign key %q column membership does not match the schema IR", group.name)
			}
			for position, column := range group.columns {
				if fieldsByName[column].Tag != constraint.Fields[position] {
					return fmt.Errorf("composite foreign key %q field order does not match the schema IR", group.name)
				}
				if group.references[position].targetFieldID != "" &&
					group.references[position].targetFieldID != constraint.TargetFields[position] {
					return fmt.Errorf("composite foreign key %q target order does not match the schema IR", group.name)
				}
			}
		}
	}
	return nil
}

func validateStructForeignConstraints(definition *StructDef, definitions map[string]*StructDef) error {
	if err := validateStructForeignConstraintShape(definition); err != nil {
		return err
	}
	for _, constraint := range definition.ForeignConstraints {
		target := structDefinitionByIdentity(definitions, constraint.TargetStructID, constraint.TargetStruct)
		if target == nil {
			return fmt.Errorf("composite foreign key %q target struct %q is unavailable", constraint.Name, constraint.TargetStruct)
		}
		if target.Name != constraint.TargetStruct {
			return fmt.Errorf(
				"composite foreign key %q target name %q does not match identity %q",
				constraint.Name, constraint.TargetStruct, target.Name,
			)
		}
		localFields, targetFields, err := structForeignConstraintFields(definition, target, constraint)
		if err != nil {
			return err
		}
		for index := range localFields {
			if storageClass(localFields[index].Kind) != storageClass(targetFields[index].Kind) {
				return fmt.Errorf(
					"composite foreign key %q field %q does not match target %s.%s storage type",
					constraint.Name, localFields[index].Name, target.Name, targetFields[index].Name,
				)
			}
		}
		if !structFieldsHaveUniqueIdentity(target, targetFields) {
			if len(targetFields) > 1 {
				return fmt.Errorf(
					"composite foreign key %q target fields are not one ordered tuple-unique constraint",
					constraint.Name,
				)
			}
			return fmt.Errorf("named foreign key %q target fields are not one ordered unique constraint", constraint.Name)
		}
	}
	return nil
}

func structFieldsHaveUniqueIdentity(definition *StructDef, fields []StructFieldDef) bool {
	if definition == nil || len(fields) == 0 {
		return false
	}
	if primary := definition.primaryFields(); sameStructFieldIdentity(primary, fields) {
		return true
	}
	if len(fields) == 1 && fields[0].Unique {
		return true
	}
	for _, unique := range definition.UniqueConstraints {
		if len(unique.Fields) != len(fields) {
			continue
		}
		candidate, err := structUniqueConstraintFields(definition, unique)
		if err != nil {
			continue
		}
		matched := true
		for index := range candidate {
			if candidate[index].ID != fields[index].ID {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func sameStructFieldIdentity(left, right []StructFieldDef) bool {
	if len(left) == 0 || len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].ID != right[index].ID {
			return false
		}
	}
	return true
}

func structForeignConstraintFields(
	definition, target *StructDef,
	constraint StructForeignConstraint,
) ([]StructFieldDef, []StructFieldDef, error) {
	local := make([]StructFieldDef, len(constraint.Fields))
	referenced := make([]StructFieldDef, len(constraint.TargetFields))
	for index, tag := range constraint.Fields {
		fieldIndex, found := definition.byTag[tag]
		if !found {
			return nil, nil, fmt.Errorf("composite foreign key %q references missing local field tag %d", constraint.Name, tag)
		}
		local[index] = definition.Fields[fieldIndex]
		targetField, found := structFieldByID(target, constraint.TargetFields[index])
		if !found {
			return nil, nil, fmt.Errorf("composite foreign key %q references a missing target field", constraint.Name)
		}
		referenced[index] = targetField
	}
	return local, referenced, nil
}

func structDefinitionByIdentity(definitions map[string]*StructDef, id, name string) *StructDef {
	if definition := definitions[name]; definition != nil && definition.ID == id {
		return definition
	}
	for _, definition := range definitions {
		if definition != nil && definition.ID == id {
			return definition
		}
	}
	return nil
}

func structUniqueConstraintFields(
	definition *StructDef,
	constraint StructUniqueConstraint,
) ([]StructFieldDef, error) {
	fields := make([]StructFieldDef, len(constraint.Fields))
	for index, tag := range constraint.Fields {
		fieldIndex, found := definition.byTag[tag]
		if !found {
			return nil, fmt.Errorf("tuple-unique %q references missing field tag %d", constraint.Name, tag)
		}
		fields[index] = definition.Fields[fieldIndex]
	}
	return fields, nil
}

func refreshStructLookups(definition *StructDef) {
	if definition == nil {
		return
	}
	definition.byName = make(map[string]int, len(definition.Fields))
	definition.byTag = make(map[uint32]int, len(definition.Fields))
	definition.byID = make(map[string]int, len(definition.Fields))
	definition.byAlias = make(map[string]int, len(definition.Fields)*2)
	for index, field := range definition.Fields {
		definition.byName[field.Name] = index
		definition.byTag[field.Tag] = index
		definition.byID[field.ID] = index
		definition.byAlias[field.Name] = index
		for _, alias := range field.Aliases {
			definition.byAlias[alias] = index
		}
	}
}

func structFieldByID(definition *StructDef, id string) (StructFieldDef, bool) {
	if definition != nil {
		if index, found := definition.byID[id]; found {
			return definition.Fields[index], true
		}
		for _, field := range definition.Fields {
			if field.ID == id {
				return field, true
			}
		}
	}
	return StructFieldDef{}, false
}

func stableSchemaID(kind, name string) string {
	digest := sha256.Sum256([]byte("kitwork:schema:v1:" + kind + ":" + name))
	return hex.EncodeToString(digest[:16])
}

func validSchemaID(id string) bool {
	decoded, err := hex.DecodeString(id)
	return err == nil && len(decoded) == 16
}

func (definition *StructDef) primaryField() (StructFieldDef, bool) {
	fields := definition.primaryFields()
	if len(fields) == 0 {
		return StructFieldDef{}, false
	}
	return fields[0], true
}

func (definition *StructDef) primaryFields() []StructFieldDef {
	if definition == nil {
		return nil
	}
	fields := make([]StructFieldDef, 0, 1)
	for _, field := range definition.Fields {
		if field.Primary {
			fields = append(fields, field)
		}
	}
	sort.SliceStable(fields, func(left, right int) bool {
		leftOrder, rightOrder := fields[left].PrimaryOrder, fields[right].PrimaryOrder
		if leftOrder == 0 || rightOrder == 0 {
			return fields[left].Position < fields[right].Position
		}
		return leftOrder < rightOrder
	})
	return fields
}
