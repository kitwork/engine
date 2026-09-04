package work

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbrecord "github.com/kitwork/engine/kitdb/record"
	"github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
)

const (
	kitDBRowNamespace    byte = kitdbrecord.RowNamespace
	kitDBUniqueNamespace byte = kitdbrecord.UniqueNamespace
	kitDBIndexNamespace  byte = kitdbrecord.IndexNamespace
	kitDBIndexCodecV2    byte = kitdbrecord.IndexCodecV2
	kitDBIndexCodecV3    byte = kitdbrecord.IndexCodecV3

	kitDBMutationRowLimit = 10_000
)

type kitDBReader interface {
	Get(key []byte) ([]byte, bool, error)
}

type kitDBStoredRow struct {
	key     []byte
	values  map[string]value.Value
	unknown []kitDBRawField
}

type kitDBIndexEntry struct {
	key   []byte
	value []byte
}

func (t *SchemaTable) kitDBManaged() (*managedKitDB, error) {
	if t == nil || t.tenant == nil {
		return nil, fmt.Errorf("kitdb: schema table is unavailable")
	}
	handle := kitDBForRequest(t.tenant, t.dbName, t.scope)
	if err := handle.requestError(); err != nil {
		return nil, err
	}
	return handle.database()
}

func (t *SchemaTable) ensureKitDBStruct() error {
	return t.ensureKitDBStructWithCheckpoint(checkpointKitDBBeforeWrite)
}

func (t *SchemaTable) ensureKitDBStructWithCheckpoint(
	checkpoint func(*managedKitDB) error,
) error {
	if t.definition == nil {
		return fmt.Errorf("KitDB table %q has no struct definition", t.table)
	}
	if t.transaction != nil {
		for _, definition := range t.definitions {
			if err := validateKitDBStruct(definition, t.definitions); err != nil {
				return err
			}
		}
		inactive, err := loadKitDBInactiveIndexes(t.transaction, t.definition)
		if err != nil {
			return err
		}
		t.inactiveIndexes = inactive
		generations, writes, epoch, err := loadKitDBIndexLayout(t.transaction, t.definition)
		if err != nil {
			return err
		}
		t.indexGenerations = generations
		t.writeIndexes = writes
		t.indexEpoch = epoch
		generation, rowWrites, rowEpoch, err := loadKitDBRowLayout(t.transaction, t.definition)
		if err != nil {
			return err
		}
		t.rowGeneration = generation
		t.writeRows = rowWrites
		t.rowEpoch = rowEpoch
		if err := t.loadKitDBStatistics(t.transaction); err != nil {
			return err
		}
		if len(t.inactiveIndexes) != 0 {
			t.transaction.wakeSecondaryIndex()
		}
		return nil
	}
	managed, err := t.kitDBManaged()
	if err != nil {
		return err
	}
	defer managed.Release()
	if checkpoint == nil {
		return fmt.Errorf("kitdb: checkpoint policy is unavailable")
	}
	if err := checkpoint(managed); err != nil {
		return err
	}
	managed.writeMu.Lock()
	defer managed.writeMu.Unlock()
	if t.catalogRequired {
		if _, found, err := managed.database.CatalogStructByID(t.definition.ID); err != nil {
			return err
		} else if !found {
			return fmt.Errorf("kitdb: struct %q no longer exists in the durable catalog", t.definition.Name)
		}
	}

	if err := ensureKitDBSchema(
		managed.database,
		t.definitions,
		t.migrate,
		func() error { return nil },
	); err != nil {
		if _, scheduleErr := wakeKitDBSecondaryIndexIfPending(managed); scheduleErr != nil {
			return errors.Join(err, scheduleErr)
		}
		if errors.Is(err, errKitDBRowMigrationPending) {
			if _, scheduleErr := scheduleKitDBRowMigration(managed); scheduleErr != nil {
				return errors.Join(err, scheduleErr)
			}
		}
		return err
	}
	inactive, err := loadKitDBInactiveIndexes(managed.database, t.definition)
	if err != nil {
		return err
	}
	t.inactiveIndexes = inactive
	generations, writes, epoch, err := loadKitDBIndexLayout(managed.database, t.definition)
	if err != nil {
		return err
	}
	t.indexGenerations = generations
	t.writeIndexes = writes
	t.indexEpoch = epoch
	generation, rowWrites, rowEpoch, err := loadKitDBRowLayout(managed.database, t.definition)
	if err != nil {
		return err
	}
	t.rowGeneration = generation
	t.writeRows = rowWrites
	t.rowEpoch = rowEpoch
	t.readyTransaction, err = managed.database.LastTransaction()
	if err != nil {
		return err
	}
	if err := t.loadKitDBStatistics(managed.database); err != nil {
		return err
	}
	if _, pendingErr := wakeKitDBSecondaryIndexIfPending(managed); pendingErr != nil {
		return pendingErr
	}
	return nil
}

func validateKitDBStruct(definition *StructDef, definitions map[string]*StructDef) error {
	if definition == nil || definition.Name == "" || len(definition.columns) == 0 {
		return fmt.Errorf("kitdb: invalid struct definition")
	}
	if err := validateSchema(definition.columns); err != nil {
		return fmt.Errorf("kitdb: struct %q: %w", definition.Name, err)
	}
	if err := validateKitDBFieldTags(definition); err != nil {
		return fmt.Errorf("kitdb: struct %q: %w", definition.Name, err)
	}
	if err := validateStructCheckConstraints(definition); err != nil {
		return fmt.Errorf("kitdb: struct %q: %w", definition.Name, err)
	}
	if err := validateStructUniqueConstraints(definition); err != nil {
		return fmt.Errorf("kitdb: struct %q: %w", definition.Name, err)
	}
	if err := validateStructIndexIdentities(definition); err != nil {
		return fmt.Errorf("kitdb: struct %q: %w", definition.Name, err)
	}
	if err := validateStructFieldReferences(definition, definitions); err != nil {
		return fmt.Errorf("kitdb: struct %q: %w", definition.Name, err)
	}
	if err := validateStructForeignConstraints(definition, definitions); err != nil {
		return fmt.Errorf("kitdb: struct %q: %w", definition.Name, err)
	}
	primary := definition.primaryFields()
	if len(primary) == 0 {
		return fmt.Errorf("kitdb: struct %q needs at least one .key() field", definition.Name)
	}
	if len(primary) == 1 {
		if primary[0].PrimaryOrder > 1 {
			return fmt.Errorf("kitdb: struct %q single .key() field position must be 1", definition.Name)
		}
		return nil
	}
	positions := make(map[int]string, len(primary))
	for _, field := range primary {
		if field.PrimaryOrder < 1 {
			return fmt.Errorf(
				"kitdb: struct %q composite key field %q needs an explicit .key(position)",
				definition.Name, field.Name,
			)
		}
		if previous := positions[field.PrimaryOrder]; previous != "" {
			return fmt.Errorf(
				"kitdb: struct %q key fields %q and %q repeat position %d",
				definition.Name, previous, field.Name, field.PrimaryOrder,
			)
		}
		positions[field.PrimaryOrder] = field.Name
	}
	for position := 1; position <= len(primary); position++ {
		if positions[position] == "" {
			return fmt.Errorf("kitdb: struct %q composite key is missing position %d", definition.Name, position)
		}
	}
	return nil
}

func validateStructFieldReferences(definition *StructDef, definitions map[string]*StructDef) error {
	for _, field := range definition.Fields {
		if field.Reference == nil {
			continue
		}
		target := definitions[field.Reference.Struct]
		if target == nil {
			return fmt.Errorf("field %q reference target struct %q is unavailable", field.Name, field.Reference.Struct)
		}
		targetField, found := kitDBField(target, field.Reference.Field)
		if !found {
			return fmt.Errorf(
				"field %q reference target %s.%s is unavailable",
				field.Name, field.Reference.Struct, field.Reference.Field,
			)
		}
		if !structFieldsHaveUniqueIdentity(target, []StructFieldDef{targetField}) {
			return fmt.Errorf(
				"field %q reference target %s.%s is not independently primary or unique",
				field.Name, target.Name, targetField.Name,
			)
		}
	}
	return nil
}

func validateStructIndexIdentities(definition *StructDef) error {
	if definition == nil {
		return fmt.Errorf("index identities are unavailable")
	}
	type declaredIdentity struct {
		id   string
		seen bool
	}
	declared := make(map[string]declaredIdentity)
	for _, field := range definition.Fields {
		for _, member := range field.Indexes {
			name := member.Name
			if name == "" {
				name = "idx_" + definition.Name + "_" + field.Name
			}
			key := strings.ToLower(name)
			identity := declared[key]
			if member.ID != "" && !validSchemaID(member.ID) {
				return fmt.Errorf("index %q has an invalid immutable identity", name)
			}
			if identity.seen && identity.id != member.ID {
				return fmt.Errorf("index %q members disagree on immutable identity", name)
			}
			declared[key] = declaredIdentity{id: member.ID, seen: true}
		}
	}
	owners := make(map[string]string)
	for _, index := range collectKitDBIndexes(definition) {
		identity := kitDBIndexStorageIdentity(definition, index)
		if previous := owners[identity]; previous != "" && !strings.EqualFold(previous, index.name) {
			return fmt.Errorf("indexes %q and %q share immutable storage identity", previous, index.name)
		}
		owners[identity] = index.name
	}
	return nil
}

func shortSchemaHash(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}

func (t *SchemaTable) kitDBPlan() value.Value {
	if t.definition == nil {
		return kitDBError(fmt.Errorf("struct %q is unavailable", t.table))
	}
	managed, err := t.kitDBManaged()
	if err != nil {
		return kitDBError(err)
	}
	defer managed.Release()
	steps, err := planKitDBCatalog(managed.database, t.definition, t.migrate)
	if err != nil {
		return kitDBError(err)
	}
	out := make([]value.Value, 0, len(steps))
	for _, step := range steps {
		out = append(out, value.New(map[string]value.Value{
			"action": value.New(step.Action), "field": value.New(step.Column),
			"from": value.New(step.From), "to": value.New(step.To),
			"destructive": value.New(step.Destructive), "willApply": value.New(step.WillApply),
		}))
	}
	return value.New(out)
}

func (t *SchemaTable) kitDBCreate(row map[string]value.Value) value.Value {
	created, err := t.createKitDBRow(row)
	if err != nil {
		return kitDBError(err)
	}
	return created
}

// createKitDBRow is the error-preserving form used by the remote SQL adapter.
// The public ORM still returns an in-band Value, while Hrana keeps sentinel
// causes needed for one safe retry across a concurrent catalog cutover.
func (t *SchemaTable) createKitDBRow(row map[string]value.Value) (value.Value, error) {
	if err := t.writeKitDBRow(row); err != nil {
		return value.Value{}, err
	}
	return coerceResult(t.columns, value.New(cloneKitDBRow(row))), nil
}

// writeKitDBRow is the allocation-light relational write primitive shared by
// create() and createMany(). It intentionally preserves the ordinary
// validation, constraint, row-codec, index and transaction path; bulk ingest
// must not become a second durability mechanism.
func (t *SchemaTable) writeKitDBRow(row map[string]value.Value) error {
	transaction, owned, err := t.kitDBWriteTransaction()
	if err != nil {
		return err
	}
	savepoint := transaction.Savepoint()
	fail := func(err error) error {
		transaction.RollbackTo(savepoint)
		if owned {
			_ = transaction.Rollback()
		}
		return err
	}
	if err := t.writeKitDBRowInTransaction(transaction, row); err != nil {
		return fail(err)
	}
	if err := t.invalidateKitDBStatistics(transaction); err != nil {
		return fail(err)
	}
	if owned {
		if _, err := transaction.Commit(); err != nil {
			return err
		}
		t.markKitDBStatisticsStale()
	}
	return nil
}

func (t *SchemaTable) writeKitDBRowInTransaction(
	transaction *kitDBRecordTransaction,
	row map[string]value.Value,
) error {
	if err := validateKitDBRow(t.definition, row); err != nil {
		return err
	}
	rowKey, err := kitDBRowKeyForRow(t.definition, row)
	if err != nil {
		return err
	}
	if err := t.validateKitDBConstraints(transaction, row, nil); err != nil {
		return err
	}
	entries, err := t.kitDBIndexEntries(row, rowKey)
	if err != nil {
		return err
	}
	if err := t.putKitDBRow(transaction, rowKey, row, nil); err != nil {
		return err
	}
	for _, entry := range entries {
		if err := transaction.Put(entry.key, entry.value); err != nil {
			return err
		}
	}
	return nil
}

func validateKitDBRow(definition *StructDef, row map[string]value.Value) error {
	if definition == nil {
		return fmt.Errorf("kitdb: struct definition is unavailable")
	}
	for name := range row {
		if _, ok := definition.columns[name]; !ok {
			return fmt.Errorf("kitdb: struct %q has no field %q", definition.Name, name)
		}
	}
	for _, field := range definition.Fields {
		item, found := row[field.Name]
		if field.NotNull && (!found || item.IsNil()) {
			return fmt.Errorf("kitdb: struct %q field %q cannot be null", definition.Name, field.Name)
		}
		if found && !item.IsNil() {
			if err := validateKitDBFieldValue(field, item); err != nil {
				return fmt.Errorf("kitdb: struct %q field %q: %w", definition.Name, field.Name, err)
			}
		}
		if found && len(field.Enum) != 0 && !item.IsNil() {
			if item.K != value.String {
				return fmt.Errorf("kitdb: struct %q field %q choice must be a string or null", definition.Name, field.Name)
			}
			allowed := false
			for _, candidate := range field.Enum {
				if item.String() == candidate {
					allowed = true
					break
				}
			}
			if !allowed {
				return fmt.Errorf("kitdb: struct %q field %q must be one of %v", definition.Name, field.Name, field.Enum)
			}
		}
	}
	if err := validateKitDBChecks(definition, row); err != nil {
		return err
	}
	return validateKitDBJSON(value.New(row))
}

func validateKitDBFieldValue(field StructFieldDef, item value.Value) error {
	switch field.Kind {
	case "text", "varchar", "char", "kitid", "uuid", "date", "time", "ip", "mac":
		if item.K != value.String {
			return fmt.Errorf("expects text, got %s", item.K)
		}
	case "datetime":
		if item.K != value.String && item.K != value.Time {
			return fmt.Errorf("expects datetime text, got %s", item.K)
		}
	case "integer", "smallint", "int32", "serial", "year", "month", "day":
		if item.K != value.Number || math.IsNaN(item.N) || math.IsInf(item.N, 0) || item.N != math.Trunc(item.N) {
			return fmt.Errorf("expects an integer, got %s", printableKitDBValue(item))
		}
		minimum, maximum, _ := kitDBVMIntegerBounds(field.Kind)
		if item.N < minimum || item.N > maximum {
			return fmt.Errorf("expects %s in [%.0f,%.0f], got %s", field.Kind, minimum, maximum, printableKitDBValue(item))
		}
	case "float":
		if item.K != value.Number || math.IsNaN(item.N) || math.IsInf(item.N, 0) {
			return fmt.Errorf("expects a finite number, got %s", printableKitDBValue(item))
		}
	case "bool":
		if item.K == value.Bool {
			return nil
		}
		if item.K != value.Number || (item.N != 0 && item.N != 1) {
			return fmt.Errorf("expects a boolean, got %s", printableKitDBValue(item))
		}
	case "decimal":
		if item.K != value.String || !validKitDBDecimal(item.String()) {
			return fmt.Errorf("expects decimal text, got %s", printableKitDBValue(item))
		}
	case "enum":
		if item.K != value.String {
			return fmt.Errorf("expects choice text, got %s", item.K)
		}
	case "blob":
		if item.K != value.Bytes {
			return fmt.Errorf("expects bytes, got %s", item.K)
		}
	case "json", "jsonb", "array", "vector":
		return validateKitDBStructuredField(field.Kind, item)
	default:
		return fmt.Errorf("uses unsupported type %q", field.Kind)
	}
	return nil
}

func kitDBVMIntegerBounds(kind string) (float64, float64, bool) {
	switch kind {
	case "smallint":
		return math.MinInt16, math.MaxInt16, true
	case "int32":
		return math.MinInt32, math.MaxInt32, true
	case "integer", "serial", "year", "month", "day":
		return -(1<<53 - 1), 1<<53 - 1, true
	default:
		return 0, 0, false
	}
}

func validateKitDBStructuredField(kind string, item value.Value) error {
	decoded := item
	if item.K == value.String {
		if err := json.Unmarshal([]byte(item.String()), &decoded); err != nil {
			return fmt.Errorf("expects valid JSON for %s", kind)
		}
	}
	if kind == "array" || kind == "vector" {
		if decoded.K != value.Array {
			return fmt.Errorf("expects a JSON array, got %s", decoded.K)
		}
	}
	if kind == "vector" {
		for index, component := range decoded.Array() {
			if component.K != value.Number || math.IsNaN(component.N) || math.IsInf(component.N, 0) {
				return fmt.Errorf("vector component %d is not a finite number", index)
			}
		}
	}
	if err := validateKitDBJSON(decoded); err != nil {
		return err
	}
	return nil
}

func validKitDBDecimal(text string) bool {
	if text == "" {
		return false
	}
	position := 0
	if text[position] == '+' || text[position] == '-' {
		position++
		if position == len(text) {
			return false
		}
	}
	digits := 0
	for position < len(text) && text[position] >= '0' && text[position] <= '9' {
		position++
		digits++
	}
	if position < len(text) && text[position] == '.' {
		position++
		for position < len(text) && text[position] >= '0' && text[position] <= '9' {
			position++
			digits++
		}
	}
	if digits == 0 {
		return false
	}
	if position < len(text) && (text[position] == 'e' || text[position] == 'E') {
		position++
		if position < len(text) && (text[position] == '+' || text[position] == '-') {
			position++
		}
		exponentDigits := 0
		for position < len(text) && text[position] >= '0' && text[position] <= '9' {
			position++
			exponentDigits++
		}
		if exponentDigits == 0 {
			return false
		}
	}
	return position == len(text)
}

func cloneKitDBRow(row map[string]value.Value) map[string]value.Value {
	cloned := make(map[string]value.Value, len(row))
	for name, item := range row {
		cloned[name] = item
	}
	return cloned
}

func (t *SchemaTable) validateKitDBConstraints(reader kitDBReader, row map[string]value.Value, previousRowKey []byte) error {
	primaryKey, err := kitDBRowKeyForRow(t.definition, row)
	if err != nil {
		return err
	}
	if existing, found, err := t.getKitDBRow(reader, primaryKey); err != nil {
		return err
	} else if found && !bytes.Equal(primaryKey, previousRowKey) && len(existing) != 0 {
		return fmt.Errorf("kitdb: struct %q key %s already exists", t.table, printableKitDBPrimary(t.definition, row))
	}
	for _, field := range t.definition.Fields {
		item, found := row[field.Name]
		if field.Unique && !field.Primary && found && !item.IsNil() {
			key, err := kitDBUniqueKey(t.definition, field, item)
			if err != nil {
				return err
			}
			target, exists, err := reader.Get(key)
			if err != nil {
				return err
			}
			if exists && !bytes.Equal(target, previousRowKey) {
				return fmt.Errorf("kitdb: struct %q field %q must be unique", t.table, field.Name)
			}
		}
		if field.Reference != nil && found && !item.IsNil() {
			if err := t.validateKitDBReference(reader, field, item); err != nil {
				return err
			}
		}
	}
	for _, constraint := range t.definition.UniqueConstraints {
		key, applicable, err := kitDBCompositeUniqueKey(t.definition, constraint, row)
		if err != nil {
			return err
		}
		if !applicable {
			continue
		}
		target, exists, err := reader.Get(key)
		if err != nil {
			return err
		}
		if exists && !bytes.Equal(target, previousRowKey) {
			fields, err := structUniqueConstraintFields(t.definition, constraint)
			if err != nil {
				return err
			}
			names := make([]string, len(fields))
			for index, field := range fields {
				names[index] = field.Name
			}
			return fmt.Errorf(
				"kitdb: struct %q constraint %q requires fields (%s) to be unique",
				t.table, constraint.Name, strings.Join(names, ", "),
			)
		}
	}
	for _, constraint := range t.definition.ForeignConstraints {
		if err := t.validateKitDBCompositeReference(reader, constraint, row); err != nil {
			return err
		}
	}
	return nil
}

func (t *SchemaTable) validateKitDBReference(reader kitDBReader, field StructFieldDef, item value.Value) error {
	reference := field.Reference
	target := t.definitions[reference.Struct]
	if target == nil {
		return fmt.Errorf("kitdb: reference target %s.%s is unavailable", reference.Struct, reference.Field)
	}
	targetField, found := kitDBField(target, reference.Field)
	if !found {
		return fmt.Errorf("kitdb: reference target %s.%s is unavailable", reference.Struct, reference.Field)
	}
	primary := target.primaryFields()
	if targetField.Primary && (len(primary) != 1 || primary[0].ID != targetField.ID) {
		return fmt.Errorf("kitdb: reference target %s.%s is only part of a composite key", reference.Struct, reference.Field)
	}
	var key []byte
	var err error
	if len(primary) == 1 && primary[0].ID == targetField.ID {
		key, err = kitDBRowKey(target, item)
	} else {
		key, err = kitDBUniqueKey(target, targetField, item)
	}
	if err != nil {
		return err
	}
	var exists bool
	if len(primary) == 1 && primary[0].ID == targetField.ID {
		_, exists, err = getKitDBLogicalRow(reader, target, key)
	} else {
		_, exists, err = reader.Get(key)
	}
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("kitdb: struct %q field %q references missing %s.%s", t.table, field.Name, reference.Struct, reference.Field)
	}
	return nil
}

func (t *SchemaTable) validateKitDBCompositeReference(
	reader kitDBReader,
	constraint StructForeignConstraint,
	row map[string]value.Value,
) error {
	target := structDefinitionByIdentity(t.definitions, constraint.TargetStructID, constraint.TargetStruct)
	if target == nil {
		return fmt.Errorf("kitdb: composite foreign key %q target %q is unavailable", constraint.Name, constraint.TargetStruct)
	}
	localFields, targetFields, err := structForeignConstraintFields(t.definition, target, constraint)
	if err != nil {
		return err
	}
	targetRow := make(map[string]value.Value, len(targetFields))
	for index, field := range localFields {
		item, found := row[field.Name]
		if !found || item.IsNil() {
			return nil
		}
		targetRow[targetFields[index].Name] = item
	}
	if sameStructFieldIdentity(target.primaryFields(), targetFields) {
		key, keyErr := kitDBRowKeyForRow(target, targetRow)
		if keyErr != nil {
			return keyErr
		}
		_, exists, getErr := getKitDBLogicalRow(reader, target, key)
		if getErr != nil {
			return getErr
		}
		if !exists {
			names := make([]string, len(localFields))
			for index, field := range localFields {
				names[index] = field.Name
			}
			return fmt.Errorf(
				"kitdb: struct %q constraint %q fields (%s) reference a missing %s key",
				t.table, constraint.Name, strings.Join(names, ", "), target.Name,
			)
		}
		return nil
	}
	if len(targetFields) == 1 && targetFields[0].Unique {
		item := targetRow[targetFields[0].Name]
		var exists bool
		key, keyErr := kitDBUniqueKey(target, targetFields[0], item)
		if keyErr != nil {
			return keyErr
		}
		_, exists, err = reader.Get(key)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf(
				"kitdb: struct %q constraint %q field %q references a missing %s.%s value",
				t.table, constraint.Name, localFields[0].Name, target.Name, targetFields[0].Name,
			)
		}
		return nil
	}
	unique, found := structUniqueConstraintForFields(target, targetFields)
	if !found {
		return fmt.Errorf("kitdb: composite foreign key %q target tuple is not unique", constraint.Name)
	}
	key, applicable, err := kitDBCompositeUniqueKey(target, unique, targetRow)
	if err != nil {
		return err
	}
	if !applicable {
		return nil
	}
	_, exists, err := reader.Get(key)
	if err != nil {
		return err
	}
	if !exists {
		names := make([]string, len(localFields))
		for index, field := range localFields {
			names[index] = field.Name
		}
		return fmt.Errorf(
			"kitdb: struct %q constraint %q fields (%s) reference a missing %s tuple",
			t.table, constraint.Name, strings.Join(names, ", "), target.Name,
		)
	}
	return nil
}

func structUniqueConstraintForFields(
	definition *StructDef,
	fields []StructFieldDef,
) (StructUniqueConstraint, bool) {
	for _, constraint := range definition.UniqueConstraints {
		if len(constraint.Fields) != len(fields) {
			continue
		}
		candidate, err := structUniqueConstraintFields(definition, constraint)
		if err != nil {
			continue
		}
		matches := true
		for index := range candidate {
			if candidate[index].ID != fields[index].ID {
				matches = false
				break
			}
		}
		if matches {
			return constraint, true
		}
	}
	return StructUniqueConstraint{}, false
}

func kitDBField(definition *StructDef, name string) (StructFieldDef, bool) {
	if definition != nil {
		if index, found := definition.byName[name]; found {
			return definition.Fields[index], true
		}
		if index, found := definition.byAlias[name]; found {
			return definition.Fields[index], true
		}
		for _, field := range definition.Fields {
			if field.Name == name {
				return field, true
			}
			for _, alias := range field.Aliases {
				if alias == name {
					return field, true
				}
			}
		}
	}
	return StructFieldDef{}, false
}

func kitDBIndexEntries(definition *StructDef, row map[string]value.Value, rowKey []byte) ([]kitDBIndexEntry, error) {
	return kitDBIndexEntriesForPhysical(
		definition,
		row,
		rowKey,
		kitDBPhysicalIndexes(collectIndexes(definition.Name, definition.columns), nil),
	)
}

func (t *SchemaTable) kitDBIndexEntries(row map[string]value.Value, rowKey []byte) ([]kitDBIndexEntry, error) {
	physical := t.writeIndexes
	if physical == nil {
		physical = kitDBPhysicalIndexes(
			collectKitDBIndexes(t.definition),
			t.indexGenerations,
		)
	}
	return kitDBIndexEntriesForPhysical(t.definition, row, rowKey, physical)
}

func kitDBIndexEntriesForPhysical(
	definition *StructDef,
	row map[string]value.Value,
	rowKey []byte,
	indexes []kitDBPhysicalIndex,
) ([]kitDBIndexEntry, error) {
	entries := make([]kitDBIndexEntry, 0)
	for _, field := range definition.Fields {
		item, found := row[field.Name]
		if !field.Unique || field.Primary || !found || item.IsNil() {
			continue
		}
		key, err := kitDBUniqueKey(definition, field, item)
		if err != nil {
			return nil, err
		}
		entries = append(entries, kitDBIndexEntry{key: key, value: bytes.Clone(rowKey)})
	}
	for _, constraint := range definition.UniqueConstraints {
		key, applicable, err := kitDBCompositeUniqueKey(definition, constraint, row)
		if err != nil {
			return nil, err
		}
		if applicable {
			entries = append(entries, kitDBIndexEntry{key: key, value: bytes.Clone(rowKey)})
		}
	}
	secondary, err := kitDBSecondaryIndexEntriesForPhysical(definition, row, rowKey, indexes)
	if err != nil {
		return nil, err
	}
	return append(entries, secondary...), nil
}

func kitDBSecondaryIndexEntries(definition *StructDef, row map[string]value.Value, rowKey []byte) ([]kitDBIndexEntry, error) {
	return kitDBSecondaryIndexEntriesFor(definition, row, rowKey, collectIndexes(definition.Name, definition.columns))
}

func kitDBSecondaryIndexEntriesFor(
	definition *StructDef,
	row map[string]value.Value,
	rowKey []byte,
	indexes []indexDef,
) ([]kitDBIndexEntry, error) {
	return kitDBSecondaryIndexEntriesForPhysical(
		definition,
		row,
		rowKey,
		kitDBPhysicalIndexes(indexes, nil),
	)
}

func kitDBSecondaryIndexEntriesForGenerations(
	definition *StructDef,
	row map[string]value.Value,
	rowKey []byte,
	indexes []indexDef,
	generations map[string]uint64,
) ([]kitDBIndexEntry, error) {
	return kitDBSecondaryIndexEntriesForPhysical(
		definition,
		row,
		rowKey,
		kitDBPhysicalIndexes(indexes, generations),
	)
}

func kitDBSecondaryIndexEntriesForPhysical(
	definition *StructDef,
	row map[string]value.Value,
	rowKey []byte,
	indexes []kitDBPhysicalIndex,
) ([]kitDBIndexEntry, error) {
	entries := make([]kitDBIndexEntry, 0)
	for _, physical := range indexes {
		index := physical.index
		if !kitDBPartialIndexMatches(definition, row, index.filter) {
			continue
		}
		prefix, err := kitDBIndexPrefixForGeneration(definition, index, row, physical.generation)
		if err != nil {
			return nil, err
		}
		key := bytes.Clone(prefix)
		for _, primary := range definition.primaryFields() {
			component, err := kitDBOrderedFieldScalarComponent(primary, row[primary.Name])
			if err != nil {
				return nil, fmt.Errorf("kitdb: struct %q key field %q: %w", definition.Name, primary.Name, err)
			}
			key = append(key, component...)
		}
		entries = append(entries, kitDBIndexEntry{key: key, value: bytes.Clone(rowKey)})
	}
	return entries, nil
}

func kitDBPartialIndexMatches(definition *StructDef, row map[string]value.Value, filters []indexCond) bool {
	for _, filter := range filters {
		left, found := row[filter.col]
		if !found {
			left = value.NewNil()
		}
		spec := definition.columns[filter.col]
		right := filter.val
		if spec != nil {
			right = coerceWrite(spec.kind, right)
		}
		if kitDBCompareValues(left, right) != 0 {
			return false
		}
	}
	return true
}

func kitDBRowPrefix(definition *StructDef) ([]byte, error) {
	return kitDBFixedKey(kitDBRowNamespace, definition.ID, "")
}

func kitDBRowKey(definition *StructDef, primary value.Value) ([]byte, error) {
	fields := definition.primaryFields()
	if len(fields) != 1 {
		return nil, fmt.Errorf("kitdb: struct %q has a composite key; provide all key fields", definition.Name)
	}
	prefix, err := kitDBRowPrefix(definition)
	if err != nil {
		return nil, err
	}
	component, err := kitDBFieldScalarComponent(fields[0], primary)
	if err != nil {
		return nil, fmt.Errorf("kitdb: struct %q key: %w", definition.Name, err)
	}
	return append(prefix, component...), nil
}

func kitDBRowKeyForRow(definition *StructDef, row map[string]value.Value) ([]byte, error) {
	fields := definition.primaryFields()
	if len(fields) == 0 {
		return nil, fmt.Errorf("kitdb: struct %q has no key", definition.Name)
	}
	if len(fields) == 1 {
		item, found := row[fields[0].Name]
		if !found {
			return nil, fmt.Errorf("kitdb: struct %q key field %q is missing", definition.Name, fields[0].Name)
		}
		return kitDBRowKey(definition, item)
	}
	prefix, err := kitDBRowPrefix(definition)
	if err != nil {
		return nil, err
	}
	key := prefix
	for _, field := range fields {
		item, found := row[field.Name]
		if !found {
			return nil, fmt.Errorf("kitdb: struct %q key field %q is missing", definition.Name, field.Name)
		}
		component, err := kitDBFieldScalarComponent(field, item)
		if err != nil {
			return nil, fmt.Errorf("kitdb: struct %q key field %q: %w", definition.Name, field.Name, err)
		}
		key = append(key, component...)
	}
	return key, nil
}

func kitDBUniqueKey(definition *StructDef, field StructFieldDef, item value.Value) ([]byte, error) {
	prefix, err := kitDBFixedKey(kitDBUniqueNamespace, definition.ID, field.ID)
	if err != nil {
		return nil, err
	}
	component, err := kitDBFieldScalarComponent(field, item)
	if err != nil {
		return nil, fmt.Errorf("kitdb: unique field %q: %w", field.Name, err)
	}
	return append(prefix, component...), nil
}

func kitDBCompositeUniqueKey(
	definition *StructDef,
	constraint StructUniqueConstraint,
	row map[string]value.Value,
) ([]byte, bool, error) {
	prefix, err := kitDBFixedKey(kitDBUniqueNamespace, definition.ID, constraint.ID)
	if err != nil {
		return nil, false, err
	}
	fields, err := structUniqueConstraintFields(definition, constraint)
	if err != nil {
		return nil, false, fmt.Errorf("kitdb: constraint %q: %w", constraint.Name, err)
	}
	key := prefix
	for _, field := range fields {
		item, found := row[field.Name]
		if !found || item.IsNil() {
			return nil, false, nil
		}
		component, err := kitDBFieldScalarComponent(field, item)
		if err != nil {
			return nil, false, fmt.Errorf(
				"kitdb: constraint %q field %q: %w", constraint.Name, field.Name, err,
			)
		}
		key = append(key, component...)
	}
	return key, true, nil
}

func kitDBIndexPrefix(definition *StructDef, index indexDef, row map[string]value.Value) ([]byte, error) {
	return kitDBIndexPrefixForGeneration(definition, index, row, 0)
}

func kitDBIndexPrefixForGeneration(
	definition *StructDef,
	index indexDef,
	row map[string]value.Value,
	generation uint64,
) ([]byte, error) {
	prefix, err := kitDBIndexBasePrefixForGeneration(definition, index, generation)
	if err != nil {
		return nil, err
	}
	for _, column := range index.columns {
		item, found := row[column]
		if !found {
			item = value.NewNil()
		}
		field, fieldFound := kitDBField(definition, column)
		if !fieldFound {
			return nil, fmt.Errorf("kitdb: index %q field %q disappeared", index.name, column)
		}
		component, err := kitDBOrderedFieldScalarComponent(field, item)
		if err != nil {
			return nil, fmt.Errorf("kitdb: index %q field %q: %w", index.name, column, err)
		}
		prefix = append(prefix, component...)
	}
	return prefix, nil
}

func kitDBIndexBasePrefix(definition *StructDef, index indexDef) ([]byte, error) {
	return kitDBIndexBasePrefixForGeneration(definition, index, 0)
}

func kitDBIndexBasePrefixForGeneration(
	definition *StructDef,
	index indexDef,
	generation uint64,
) ([]byte, error) {
	indexIdentity := kitDBIndexStorageIdentity(definition, index)
	prefix, err := kitDBFixedKey(kitDBIndexNamespace, definition.ID, indexIdentity)
	if err != nil {
		return nil, err
	}
	// Legacy components begin with a non-zero uvarint length. The zero byte
	// makes ordered codecs disjoint. Generation zero retains the v2 layout;
	// physical generations use v3 plus one ordered fixed-width identity.
	if generation == 0 {
		prefix = append(prefix, 0, kitDBIndexCodecV2)
		return prefix, nil
	}
	prefix = append(prefix, 0, kitDBIndexCodecV3)
	prefix = binary.BigEndian.AppendUint64(prefix, generation)
	return prefix, nil
}

func kitDBIndexStorageIdentity(definition *StructDef, index indexDef) string {
	if index.id != "" {
		return index.id
	}
	return stableSchemaID("index", definition.ID+":"+index.name+":"+strings.Join(index.columns, ","))
}

// kitDBOrderedScalarComponent is self-delimiting and preserves the logical
// ordering used by kitDBCompareValues. Text and bytes use zero escaping so
// prefixes and embedded zero bytes remain ordered without a length prefix.
func kitDBOrderedScalarComponent(item value.Value) ([]byte, error) {
	scalar, err := kitDBRecordScalar(item)
	if err != nil {
		return nil, err
	}
	return kitdbrecord.OrderedScalarComponent(scalar)
}

func kitDBOrderedFieldScalarComponent(field StructFieldDef, item value.Value) ([]byte, error) {
	scalar, err := kitDBFieldRecordScalar(field, item)
	if err != nil {
		return nil, err
	}
	return kitdbrecord.OrderedScalarComponent(scalar)
}

func kitDBRecordScalar(item value.Value) (kitdbrecord.Scalar, error) {
	switch item.K {
	case value.Nil:
		return kitdbrecord.Scalar{Kind: kitdbrecord.ScalarNil}, nil
	case value.Bool:
		return kitdbrecord.Scalar{Kind: kitdbrecord.ScalarBool, Bool: item.N != 0}, nil
	case value.Number:
		return kitdbrecord.Scalar{Kind: kitdbrecord.ScalarNumber, Number: item.N}, nil
	case value.String:
		return kitdbrecord.Scalar{Kind: kitdbrecord.ScalarText, Text: item.String()}, nil
	case value.Bytes:
		data, _ := item.V.([]byte)
		return kitdbrecord.Scalar{Kind: kitdbrecord.ScalarBytes, Bytes: data}, nil
	case value.Time, value.Duration:
		return kitdbrecord.Scalar{Kind: kitdbrecord.ScalarTemporal, Number: item.N}, nil
	default:
		return kitdbrecord.Scalar{}, fmt.Errorf("value kind %s is not scalar", item.K)
	}
}

func kitDBFieldRecordScalar(field StructFieldDef, item value.Value) (kitdbrecord.Scalar, error) {
	if (field.Kind == "smallint" || field.Kind == "int32") && item.K == value.Number {
		if err := validateKitDBFieldValue(field, item); err != nil {
			return kitdbrecord.Scalar{}, err
		}
		return kitdbrecord.Scalar{Kind: kitdbrecord.ScalarInteger, Integer: int64(item.N)}, nil
	}
	return kitDBRecordScalar(item)
}

func kitDBFixedKey(namespace byte, structID, childID string) ([]byte, error) {
	return kitdbrecord.FixedKey(namespace, structID, childID)
}

func kitDBScalarComponent(item value.Value) ([]byte, error) {
	scalar, err := kitDBRecordScalar(item)
	if err != nil {
		return nil, err
	}
	return kitdbrecord.ScalarComponent(scalar)
}

func kitDBFieldScalarComponent(field StructFieldDef, item value.Value) ([]byte, error) {
	scalar, err := kitDBFieldRecordScalar(field, item)
	if err != nil {
		return nil, err
	}
	return kitdbrecord.ScalarComponent(scalar)
}

func kitDBHasRows(reader kitDBReader, definition *StructDef) (bool, error) {
	database, ok := reader.(*kitdbengine.DB)
	if !ok {
		return false, fmt.Errorf("kitdb: row scan requires a database handle")
	}
	generation, _, err := loadKitDBActiveRowLayout(database, definition)
	if err != nil {
		return false, err
	}
	prefix, err := kitDBPhysicalRowPrefix(definition, generation)
	if err != nil {
		return false, err
	}
	snapshot, err := database.Snapshot()
	if err != nil {
		return false, err
	}
	defer snapshot.Close()
	cursor, err := snapshot.Cursor(kitdbengine.RangeOptions{Prefix: prefix, Limit: 1})
	if err != nil {
		return false, err
	}
	defer cursor.Close()
	found := cursor.Next()
	return found, cursor.Err()
}

func printableKitDBValue(item value.Value) string {
	if item.K == value.String {
		return fmt.Sprintf("%q", item.String())
	}
	return item.Text()
}

func printableKitDBPrimary(definition *StructDef, row map[string]value.Value) string {
	fields := definition.primaryFields()
	if len(fields) == 1 {
		return printableKitDBValue(row[fields[0].Name])
	}
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		parts = append(parts, field.Name+"="+printableKitDBValue(row[field.Name]))
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func kitDBCompareValues(left, right value.Value) int {
	if left.IsNil() || right.IsNil() {
		switch {
		case left.IsNil() && right.IsNil():
			return 0
		case left.IsNil():
			return -1
		default:
			return 1
		}
	}
	if left.K == value.Number && right.K == value.Number {
		switch {
		case left.N < right.N:
			return -1
		case left.N > right.N:
			return 1
		default:
			return 0
		}
	}
	if left.K == value.Bool && right.K == value.Bool {
		return kitDBCompareFloat(left.N, right.N)
	}
	if left.K == value.String && right.K == value.String {
		return strings.Compare(left.String(), right.String())
	}
	if reflect.DeepEqual(left.Interface(), right.Interface()) {
		return 0
	}
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return bytes.Compare(leftJSON, rightJSON)
}

func kitDBCompareFloat(left, right float64) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func (t *SchemaTable) kitDBList(args ...value.Value) value.Value {
	if len(args) == 1 && args[0].K == value.Number {
		t.Limit(args[0].Int())
	} else if len(args) != 0 {
		t.Where(args...)
	}
	rows, err := t.readKitDBRows(t.builder().ExecutionPlan(), 0)
	if err != nil {
		return kitDBError(err)
	}
	result := make([]value.Value, 0, len(rows))
	for _, row := range rows {
		result = append(result, value.New(cloneKitDBRow(row.values)))
	}
	return coerceResult(t.columns, value.New(result))
}

func (t *SchemaTable) kitDBFirst(args ...value.Value) value.Value {
	if len(args) != 0 {
		t.Where(args...)
	}
	rows, err := t.readKitDBRows(t.builder().ExecutionPlan(), 1)
	if err != nil {
		return kitDBError(err)
	}
	if len(rows) == 0 {
		return value.NewNil()
	}
	return coerceResult(t.columns, value.New(cloneKitDBRow(rows[0].values)))
}

func (t *SchemaTable) kitDBFind(args ...value.Value) value.Value {
	primary := t.definition.primaryFields()
	if len(primary) == 0 {
		return kitDBError(fmt.Errorf("struct %q has no key", t.table))
	}
	if len(primary) > 1 {
		switch {
		case len(args) == 1 && args[0].K == value.Map:
			provided := args[0].Map()
			for _, field := range primary {
				item, found := provided[field.Name]
				if !found {
					return kitDBError(fmt.Errorf("struct %q composite key needs field %q", t.table, field.Name))
				}
				t.Where(value.New(field.Name), value.New("="), item)
			}
			return t.kitDBFirst()
		case len(args) == len(primary):
			for index, field := range primary {
				t.Where(value.New(field.Name), value.New("="), args[index])
			}
			return t.kitDBFirst()
		case len(args) == 1 && args[0].IsCallable():
			t.Where(args[0])
			return t.kitDBFirst()
		default:
			return kitDBError(fmt.Errorf(
				"struct %q find expects all %d composite key values or one key object", t.table, len(primary),
			))
		}
	}
	switch len(args) {
	case 0:
		return value.NewNil()
	case 1:
		if args[0].IsCallable() {
			t.Where(args[0])
		} else {
			t.Where(value.New(primary[0].Name), value.New("="), args[0])
		}
	case 2:
		t.Where(args...)
	case 3:
		t.Where(args...)
	default:
		return value.NewNil()
	}
	return t.kitDBFirst()
}

func (t *SchemaTable) kitDBCount(args ...value.Value) value.Value {
	countField := "*"
	switch len(args) {
	case 1:
		if args[0].K == value.String {
			countField = args[0].String()
		} else if args[0].IsCallable() {
			t.Where(args[0])
		}
	case 2, 3:
		t.Where(args...)
	}
	results, err := t.kitDBAggregates([]kitDBAggregateRequest{{operation: "count", field: countField}})
	if err != nil {
		return kitDBError(err)
	}
	return results[0]
}

func (t *SchemaTable) kitDBExists(args ...value.Value) value.Value {
	if len(args) != 0 {
		t.Where(args...)
	}
	found := false
	err := t.scanKitDBRows(t.builder().ExecutionPlan(), func(_ kitDBStoredRow) (bool, error) {
		found = true
		return true, nil
	})
	if err != nil {
		return kitDBError(err)
	}
	return value.New(found)
}

func (t *SchemaTable) readKitDBRows(plan query.ExecutionPlan, forceLimit int) ([]kitDBStoredRow, error) {
	limit := kitDBEffectiveLimit(plan, forceLimit)
	if limit < 1 {
		return nil, nil
	}
	access, err := t.planKitDBAccess(plan)
	if err != nil {
		return nil, err
	}
	offset := plan.Offset
	if offset < 0 {
		offset = 0
	}
	needed := limit + offset
	rows := make([]kitDBStoredRow, 0, needed)
	needsSort := len(plan.Orders) != 0 && !access.orderCovered
	err = t.scanKitDBRowsUsing(plan, access, func(row kitDBStoredRow) (bool, error) {
		rows = append(rows, row)
		if needsSort {
			sort.SliceStable(rows, func(left, right int) bool {
				return t.compareKitDBRows(rows[left], rows[right], plan.Orders) < 0
			})
			if len(rows) > needed {
				rows = rows[:needed]
			}
			return false, nil
		}
		return len(rows) >= needed, nil
	})
	if err != nil {
		return nil, err
	}
	if offset >= len(rows) {
		return []kitDBStoredRow{}, nil
	}
	rows = rows[offset:]
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func kitDBEffectiveLimit(plan query.ExecutionPlan, forceLimit int) int {
	if forceLimit > 0 {
		return forceLimit
	}
	limit := plan.Limit
	if limit <= 0 {
		limit = query.DefaultDBLimit
	}
	maximum := plan.MaxLimit
	if maximum <= 0 {
		maximum = query.DefaultDBMaxLimit
	}
	if limit > maximum {
		return maximum
	}
	return limit
}

func (t *SchemaTable) compareKitDBRows(left, right kitDBStoredRow, orders []query.OrderQuery) int {
	for _, order := range orders {
		leftValue, leftFound := left.values[kitDBColumnName(order.Column)]
		rightValue, rightFound := right.values[kitDBColumnName(order.Column)]
		if !leftFound {
			leftValue = value.NewNil()
		}
		if !rightFound {
			rightValue = value.NewNil()
		}
		comparison := kitDBCompareValues(leftValue, rightValue)
		if comparison != 0 {
			if strings.EqualFold(order.Direction, "desc") {
				return -comparison
			}
			return comparison
		}
	}
	return bytes.Compare(left.key, right.key)
}

func (t *SchemaTable) scanKitDBRows(plan query.ExecutionPlan, visit func(kitDBStoredRow) (bool, error)) error {
	access, err := t.planKitDBAccess(plan)
	if err != nil {
		return err
	}
	return t.scanKitDBRowsUsing(plan, access, visit)
}

func (t *SchemaTable) scanKitDBRowsUsing(
	plan query.ExecutionPlan,
	access kitDBAccessPlan,
	visit func(kitDBStoredRow) (bool, error),
) error {
	if t.transaction != nil {
		return t.scanKitDBRowsFrom(t.transaction, plan, access, visit)
	}
	managed, err := t.kitDBManaged()
	if err != nil {
		return err
	}
	defer managed.Release()
	managed.writeMu.RLock()
	if err := validateKitDBIndexLayoutEpoch(managed.database, t.definition, t.indexEpoch); err != nil {
		managed.writeMu.RUnlock()
		return err
	}
	if err := validateKitDBRowLayoutEpoch(managed.database, t.definition, t.rowEpoch); err != nil {
		managed.writeMu.RUnlock()
		return err
	}
	snapshot, err := managed.database.Snapshot()
	managed.writeMu.RUnlock()
	if err != nil {
		return err
	}
	defer snapshot.Close()
	return t.scanKitDBRowsFrom(kitDBSnapshotReader{snapshot: snapshot}, plan, access, visit)
}

func (t *SchemaTable) scanKitDBRowsFrom(
	reader kitDBRecordReader,
	plan query.ExecutionPlan,
	access kitDBAccessPlan,
	visit func(kitDBStoredRow) (bool, error),
) error {
	consumeKey := func(rowKey []byte) (bool, error) {
		physicalKey, err := kitDBPhysicalRowKey(t.definition, rowKey, t.rowGeneration)
		if err != nil {
			return false, err
		}
		encoded, found, err := reader.Get(physicalKey)
		if err != nil || !found {
			return false, err
		}
		return t.consumeKitDBRow(plan, rowKey, encoded, visit)
	}

	switch access.kind {
	case kitDBAccessPrimary:
		_, err := consumeKey(access.lookup)
		return err
	case kitDBAccessUnique:
		rowKey, found, err := reader.Get(access.lookup)
		if err != nil || !found {
			return err
		}
		if len(rowKey) == 0 {
			return nil
		}
		_, err = consumeKey(rowKey)
		return err
	case kitDBAccessIndex, kitDBAccessIndexPrefix, kitDBAccessIndexRange, kitDBAccessIndexOrder:
		return reader.Scan(access.options, func(_, rowKey []byte) (bool, error) {
			if err := t.kitDBRequestError(); err != nil {
				return false, err
			}
			return consumeKey(rowKey)
		})
	case kitDBAccessScan:
		physicalPrefix, err := kitDBPhysicalRowPrefix(t.definition, t.rowGeneration)
		if err != nil {
			return err
		}
		options := access.options
		options.Prefix = physicalPrefix
		options.Start = nil
		options.End = nil
		return reader.Scan(options, func(physicalKey, encoded []byte) (bool, error) {
			if err := t.kitDBRequestError(); err != nil {
				return false, err
			}
			rowKey, err := kitDBLogicalRowKey(t.definition, physicalKey, t.rowGeneration)
			if err != nil {
				return false, err
			}
			return t.consumeKitDBRow(plan, rowKey, encoded, visit)
		})
	default:
		return fmt.Errorf("kitdb: unsupported access plan %q", access.kind)
	}
}

func (t *SchemaTable) consumeKitDBRow(plan query.ExecutionPlan, rowKey, encoded []byte, visit func(kitDBStoredRow) (bool, error)) (bool, error) {
	row, err := decodeKitDBRow(t.definition, encoded)
	if err != nil {
		return false, err
	}
	matches, err := t.kitDBPlanMatches(row.values, plan)
	if err != nil || !matches {
		return false, err
	}
	return visit(kitDBStoredRow{
		key: bytes.Clone(rowKey), values: row.values, unknown: row.unknown,
	})
}

type kitDBAccessKind string

const (
	kitDBAccessPrimary     kitDBAccessKind = "primary"
	kitDBAccessUnique      kitDBAccessKind = "unique"
	kitDBAccessIndex       kitDBAccessKind = "index"
	kitDBAccessIndexPrefix kitDBAccessKind = "index_prefix"
	kitDBAccessIndexRange  kitDBAccessKind = "index_range"
	kitDBAccessIndexOrder  kitDBAccessKind = "index_order"
	kitDBAccessScan        kitDBAccessKind = "scan"
)

type kitDBAccessPlan struct {
	kind                  kitDBAccessKind
	name                  string
	indexSignature        string
	fields                []string
	lookup                []byte
	options               kitdbengine.RangeOptions
	equalityPrefix        int
	rangeField            string
	orderCovered          bool
	estimatedRows         uint64
	hasEstimate           bool
	estimateKind          string
	statisticsState       string
	statisticsTransaction uint64
}

type kitDBBound struct {
	value     value.Value
	inclusive bool
}

type kitDBColumnConstraint struct {
	equal    value.Value
	hasEqual bool
	lower    kitDBBound
	hasLower bool
	upper    kitDBBound
	hasUpper bool
}

// planKitDBAccess is pure schema/query planning. Execution and explain consume
// this same value so diagnostics cannot drift from the path a read will take.
func (t *SchemaTable) planKitDBAccess(plan query.ExecutionPlan) (kitDBAccessPlan, error) {
	constraints, safe, err := t.kitDBIndexConstraints(plan)
	if err != nil {
		return kitDBAccessPlan{}, err
	}
	if safe {
		primary := t.definition.primaryFields()
		primaryRow := make(map[string]value.Value, len(primary))
		primaryNames := make([]string, len(primary))
		primaryUsable := len(primary) != 0
		for index, field := range primary {
			constraint := constraints[field.Name]
			if constraint == nil || !constraint.hasEqual || !kitDBIndexValueCompatible(field, constraint.equal) {
				primaryUsable = false
				break
			}
			primaryRow[field.Name] = constraint.equal
			primaryNames[index] = field.Name
		}
		if primaryUsable {
			key, err := kitDBRowKeyForRow(t.definition, primaryRow)
			access := kitDBAccessPlan{
				kind: kitDBAccessPrimary, name: "PRIMARY", fields: primaryNames, lookup: key,
				orderCovered: len(plan.Orders) != 0,
			}
			return t.estimateKitDBAccess(access, plan), err
		}
		for _, field := range t.definition.Fields {
			constraint := constraints[field.Name]
			if !field.Unique || field.Primary || constraint == nil || !constraint.hasEqual ||
				constraint.equal.IsNil() || !kitDBIndexValueCompatible(field, constraint.equal) {
				continue
			}
			key, err := kitDBUniqueKey(t.definition, field, constraint.equal)
			access := kitDBAccessPlan{
				kind:         kitDBAccessUnique,
				name:         "unique_" + t.definition.Name + "_" + field.Name,
				fields:       []string{field.Name},
				lookup:       key,
				orderCovered: len(plan.Orders) != 0,
			}
			return t.estimateKitDBAccess(access, plan), err
		}
		for _, unique := range t.definition.UniqueConstraints {
			fields, err := structUniqueConstraintFields(t.definition, unique)
			if err != nil {
				return kitDBAccessPlan{}, err
			}
			row := make(map[string]value.Value, len(fields))
			names := make([]string, len(fields))
			usable := true
			for index, field := range fields {
				constraint := constraints[field.Name]
				if constraint == nil || !constraint.hasEqual || constraint.equal.IsNil() ||
					!kitDBIndexValueCompatible(field, constraint.equal) {
					usable = false
					break
				}
				row[field.Name] = constraint.equal
				names[index] = field.Name
			}
			if !usable {
				continue
			}
			key, applicable, err := kitDBCompositeUniqueKey(t.definition, unique, row)
			if err != nil {
				return kitDBAccessPlan{}, err
			}
			if !applicable {
				continue
			}
			access := kitDBAccessPlan{
				kind: kitDBAccessUnique, name: unique.Name, fields: names, lookup: key,
				orderCovered: len(plan.Orders) != 0,
			}
			return t.estimateKitDBAccess(access, plan), nil
		}

		bestScore := -1
		best := kitDBAccessPlan{}
		for _, index := range collectKitDBIndexes(t.definition) {
			if _, inactive := t.inactiveIndexes[kitDBIndexSignature(index)]; inactive {
				continue
			}
			if len(index.filter) != 0 {
				continue
			}
			candidate, score, useful, err := t.planKitDBSecondaryIndex(index, constraints, plan.Orders)
			if err != nil {
				return kitDBAccessPlan{}, err
			}
			if !useful {
				continue
			}
			candidate = t.estimateKitDBAccess(candidate, plan)
			if score < bestScore || (score == bestScore && !kitDBAccessEstimateBetter(candidate, best)) {
				continue
			}
			best, bestScore = candidate, score
		}
		if bestScore >= 0 {
			return best, nil
		}
	}
	prefix, err := kitDBRowPrefix(t.definition)
	access := kitDBAccessPlan{kind: kitDBAccessScan, options: kitdbengine.RangeOptions{Prefix: prefix}}
	return t.estimateKitDBAccess(access, plan), err
}

func (t *SchemaTable) planKitDBSecondaryIndex(
	index indexDef,
	constraints map[string]*kitDBColumnConstraint,
	orders []query.OrderQuery,
) (kitDBAccessPlan, int, bool, error) {
	prefix, err := kitDBIndexBasePrefixForGeneration(
		t.definition,
		index,
		t.indexGenerations[kitDBIndexSignature(index)],
	)
	if err != nil {
		return kitDBAccessPlan{}, 0, false, err
	}
	equalityPrefix := 0
	for _, column := range index.columns {
		constraint := constraints[column]
		field, found := kitDBField(t.definition, column)
		if !found || constraint == nil || !constraint.hasEqual ||
			!kitDBIndexValueCompatible(field, constraint.equal) {
			break
		}
		component, err := kitDBOrderedFieldScalarComponent(field, constraint.equal)
		if err != nil {
			return kitDBAccessPlan{}, 0, false, err
		}
		prefix = append(prefix, component...)
		equalityPrefix++
	}

	access := kitDBAccessPlan{
		kind: kitDBAccessIndexPrefix, name: index.name,
		indexSignature: kitDBIndexSignature(index),
		fields:         append([]string(nil), index.columns...),
		options:        kitdbengine.RangeOptions{Prefix: bytes.Clone(prefix)},
		equalityPrefix: equalityPrefix,
	}
	if equalityPrefix == len(index.columns) {
		access.kind = kitDBAccessIndex
	}
	if equalityPrefix < len(index.columns) {
		column := index.columns[equalityPrefix]
		field, _ := kitDBField(t.definition, column)
		constraint := constraints[column]
		if constraint != nil && kitDBSortableIndexKind(field.Kind) &&
			(constraint.hasLower || constraint.hasUpper) {
			access.kind = kitDBAccessIndexRange
			access.rangeField = column
			if constraint.hasLower {
				component, err := kitDBOrderedFieldScalarComponent(field, constraint.lower.value)
				if err != nil {
					return kitDBAccessPlan{}, 0, false, err
				}
				start := append(bytes.Clone(prefix), component...)
				if !constraint.lower.inclusive {
					start = kitDBPrefixEnd(start)
				}
				access.options.Start = start
			}
			if constraint.hasUpper {
				component, err := kitDBOrderedFieldScalarComponent(field, constraint.upper.value)
				if err != nil {
					return kitDBAccessPlan{}, 0, false, err
				}
				end := append(bytes.Clone(prefix), component...)
				if constraint.upper.inclusive {
					end = kitDBPrefixEnd(end)
				}
				access.options.End = end
			}
		}
	}

	access.orderCovered, access.options.Reverse = t.kitDBIndexCoversOrder(
		index, equalityPrefix, constraints, orders,
	)
	useful := equalityPrefix != 0 || access.rangeField != "" || access.orderCovered
	if !useful {
		return kitDBAccessPlan{}, 0, false, nil
	}
	if equalityPrefix == 0 && access.rangeField == "" && access.orderCovered {
		access.kind = kitDBAccessIndexOrder
	}
	score := equalityPrefix * 100
	if access.rangeField != "" {
		score += 30
	}
	if access.orderCovered {
		score += 20
	}
	if equalityPrefix == len(index.columns) {
		score += 5
	}
	return access, score, true, nil
}

func (t *SchemaTable) kitDBIndexCoversOrder(
	index indexDef,
	equalityPrefix int,
	constraints map[string]*kitDBColumnConstraint,
	orders []query.OrderQuery,
) (bool, bool) {
	if len(orders) == 0 {
		return false, false
	}
	remaining := make([]query.OrderQuery, 0, len(orders))
	for _, order := range orders {
		column := kitDBColumnName(order.Column)
		if constraint := constraints[column]; constraint != nil && constraint.hasEqual {
			continue
		}
		remaining = append(remaining, order)
	}
	if len(remaining) == 0 {
		return equalityPrefix == len(index.columns), false
	}
	indexPosition := equalityPrefix
	direction := ""
	for _, order := range remaining {
		currentDirection := strings.ToLower(strings.TrimSpace(order.Direction))
		if currentDirection == "" {
			currentDirection = "asc"
		}
		if currentDirection != "asc" && currentDirection != "desc" {
			return false, false
		}
		if direction == "" {
			direction = currentDirection
		} else if direction != currentDirection {
			return false, false
		}
		for indexPosition < len(index.columns) {
			constraint := constraints[index.columns[indexPosition]]
			if constraint == nil || !constraint.hasEqual {
				break
			}
			indexPosition++
		}
		if indexPosition >= len(index.columns) || index.columns[indexPosition] != kitDBColumnName(order.Column) {
			return false, false
		}
		field, found := kitDBField(t.definition, index.columns[indexPosition])
		if !found || !kitDBSortableIndexKind(field.Kind) {
			return false, false
		}
		indexPosition++
	}
	return true, direction == "desc"
}

func (t *SchemaTable) kitDBIndexConstraints(plan query.ExecutionPlan) (map[string]*kitDBColumnConstraint, bool, error) {
	constraints := make(map[string]*kitDBColumnConstraint)
	safe := true
	for index, condition := range plan.Conditions {
		column := kitDBColumnName(condition.Column)
		spec := t.columns[column]
		if spec == nil {
			return nil, false, fmt.Errorf("kitdb: struct %q has no field %q", t.table, column)
		}
		if index > 0 && !strings.EqualFold(strings.TrimSpace(condition.Logic), "and") {
			safe = false
		}
		if condition.IsColumn {
			continue
		}
		item := kitDBAnyValue(condition.Value)
		item = coerceWrite(spec.kind, item)
		field, found := kitDBField(t.definition, column)
		if !found || !kitDBIndexValueCompatible(field, item) {
			continue
		}
		constraint := constraints[column]
		if constraint == nil {
			constraint = &kitDBColumnConstraint{}
			constraints[column] = constraint
		}
		switch strings.ToLower(strings.TrimSpace(condition.Operator)) {
		case "", "=", "==", "===":
			constraint.equal, constraint.hasEqual = item, true
		case ">", ">=":
			if kitDBSortableIndexKind(field.Kind) {
				kitDBTightenLowerBound(constraint, item, strings.TrimSpace(condition.Operator) == ">=")
			}
		case "<", "<=":
			if kitDBSortableIndexKind(field.Kind) {
				kitDBTightenUpperBound(constraint, item, strings.TrimSpace(condition.Operator) == "<=")
			}
		}
	}
	return constraints, safe, nil
}

func kitDBTightenLowerBound(constraint *kitDBColumnConstraint, item value.Value, inclusive bool) {
	if !constraint.hasLower {
		constraint.lower, constraint.hasLower = kitDBBound{value: item, inclusive: inclusive}, true
		return
	}
	comparison := kitDBCompareValues(item, constraint.lower.value)
	if comparison > 0 || (comparison == 0 && !inclusive && constraint.lower.inclusive) {
		constraint.lower = kitDBBound{value: item, inclusive: inclusive}
	}
}

func kitDBTightenUpperBound(constraint *kitDBColumnConstraint, item value.Value, inclusive bool) {
	if !constraint.hasUpper {
		constraint.upper, constraint.hasUpper = kitDBBound{value: item, inclusive: inclusive}, true
		return
	}
	comparison := kitDBCompareValues(item, constraint.upper.value)
	if comparison < 0 || (comparison == 0 && !inclusive && constraint.upper.inclusive) {
		constraint.upper = kitDBBound{value: item, inclusive: inclusive}
	}
}

func kitDBIndexValueCompatible(field StructFieldDef, item value.Value) bool {
	if item.IsNil() {
		return true
	}
	return validateKitDBFieldValue(field, item) == nil
}

func kitDBSortableIndexKind(kind string) bool {
	switch kind {
	case "text", "varchar", "char", "kitid", "uuid", "date", "time", "ip", "mac", "enum",
		"integer", "smallint", "int32", "float", "serial", "year", "month", "day", "bool":
		return true
	default:
		return false
	}
}

func kitDBPrefixEnd(prefix []byte) []byte {
	return kitdbrecord.PrefixEnd(prefix)
}

func (t *SchemaTable) kitDBMatches(row map[string]value.Value, conditions []query.Condition) (bool, error) {
	if len(conditions) == 0 {
		return true, nil
	}
	result := false
	for index, condition := range conditions {
		matched, err := t.kitDBConditionMatches(row, condition)
		if err != nil {
			return false, err
		}
		if index == 0 {
			result = matched
		} else if strings.EqualFold(condition.Logic, "or") {
			result = result || matched
		} else {
			result = result && matched
		}
	}
	return result, nil
}

func (t *SchemaTable) kitDBConditionMatches(row map[string]value.Value, condition query.Condition) (bool, error) {
	column := kitDBColumnName(condition.Column)
	spec := t.columns[column]
	if spec == nil {
		return false, fmt.Errorf("kitdb: struct %q has no field %q", t.table, column)
	}
	left, found := row[column]
	if !found {
		left = value.NewNil()
	}
	var right value.Value
	if condition.IsColumn {
		rightColumn := kitDBColumnName(fmt.Sprint(condition.Value))
		if _, ok := t.columns[rightColumn]; !ok {
			return false, fmt.Errorf("kitdb: struct %q has no field %q", t.table, rightColumn)
		}
		right, found = row[rightColumn]
		if !found {
			right = value.NewNil()
		}
	} else {
		right = coerceWrite(spec.kind, kitDBAnyValue(condition.Value))
	}
	operator := strings.ToLower(strings.TrimSpace(condition.Operator))
	switch operator {
	case "", "=", "==", "===":
		return kitDBCompareValues(left, right) == 0, nil
	case "!=", "!==", "<>":
		return kitDBCompareValues(left, right) != 0, nil
	case ">":
		return kitDBCompareValues(left, right) > 0, nil
	case ">=":
		return kitDBCompareValues(left, right) >= 0, nil
	case "<":
		return kitDBCompareValues(left, right) < 0, nil
	case "<=":
		return kitDBCompareValues(left, right) <= 0, nil
	case "like":
		return kitDBLike(left.Text(), right.Text()), nil
	case "not like":
		return !kitDBLike(left.Text(), right.Text()), nil
	case "in":
		return kitDBContains(right, left), nil
	case "not in":
		return !kitDBContains(right, left), nil
	default:
		return false, fmt.Errorf("kitdb: operator %q is not supported", condition.Operator)
	}
}

func kitDBAnyValue(input any) value.Value {
	if input == nil {
		return value.NewNil()
	}
	if item, ok := input.(value.Value); ok {
		return item
	}
	return value.New(input)
}

func kitDBContains(collection, item value.Value) bool {
	if collection.K == value.Array {
		for _, candidate := range collection.Array() {
			if kitDBCompareValues(candidate, item) == 0 {
				return true
			}
		}
		return false
	}
	raw := collection.Interface()
	reflected := reflect.ValueOf(raw)
	if reflected.IsValid() && (reflected.Kind() == reflect.Array || reflected.Kind() == reflect.Slice) {
		for index := 0; index < reflected.Len(); index++ {
			if kitDBCompareValues(value.New(reflected.Index(index).Interface()), item) == 0 {
				return true
			}
		}
	}
	return false
}

func kitDBLike(text, pattern string) bool {
	textRunes, patternRunes := []rune(text), []rune(pattern)
	textIndex, patternIndex := 0, 0
	star, retry := -1, 0
	for textIndex < len(textRunes) {
		switch {
		case patternIndex < len(patternRunes) && (patternRunes[patternIndex] == '_' || patternRunes[patternIndex] == textRunes[textIndex]):
			textIndex++
			patternIndex++
		case patternIndex < len(patternRunes) && patternRunes[patternIndex] == '%':
			star = patternIndex
			patternIndex++
			retry = textIndex
		case star >= 0:
			patternIndex = star + 1
			retry++
			textIndex = retry
		default:
			return false
		}
	}
	for patternIndex < len(patternRunes) && patternRunes[patternIndex] == '%' {
		patternIndex++
	}
	return patternIndex == len(patternRunes)
}

func kitDBColumnName(column string) string {
	if dot := strings.LastIndexByte(column, '.'); dot >= 0 {
		return column[dot+1:]
	}
	return column
}

func (t *SchemaTable) kitDBRequestError() error {
	handle := kitDBForRequest(t.tenant, t.dbName, t.scope)
	return handle.requestError()
}

func (t *SchemaTable) kitDBUpdate(args ...value.Value) value.Value {
	plan := t.builder().ExecutionPlan()
	if len(plan.Conditions) == 0 && plan.Predicate == nil {
		return kitDBError(fmt.Errorf("missing WHERE clause for update"))
	}
	changes, err := t.kitDBUpdateChanges(args)
	if err != nil {
		return kitDBError(err)
	}
	prepared, err := t.kitDBUpdateMatching(plan, nil, func(kitDBStoredRow) (map[string]value.Value, error) {
		return changes, nil
	})
	if err != nil {
		return kitDBError(err)
	}
	if len(prepared) == 0 {
		return value.NewNil()
	}
	return coerceResult(t.columns, value.New(cloneKitDBRow(prepared[0].values)))
}

type kitDBMutationMatcher func(kitDBStoredRow) (bool, error)
type kitDBMutationUpdater func(kitDBStoredRow) (map[string]value.Value, error)

const kitDBReferentialActionDepthLimit = 64

// kitDBReferentialActionContext bounds one top-level mutation and detects a
// recursive update that reaches the same row before its earlier action has
// completed. Delete cycles terminate naturally because staged deletes are
// hidden by the transaction overlay.
type kitDBReferentialActionContext struct {
	depth  int
	rows   int
	active map[string]struct{}
}

func (context *kitDBReferentialActionContext) enter(
	operation string,
	definition *StructDef,
	rows []kitDBStoredRow,
) (func(), error) {
	if context == nil || definition == nil {
		return func() {}, fmt.Errorf("kitdb: referential action context is unavailable")
	}
	if context.depth >= kitDBReferentialActionDepthLimit {
		return func() {}, fmt.Errorf("kitdb: referential action exceeds depth %d", kitDBReferentialActionDepthLimit)
	}
	if len(rows) > kitDBMutationRowLimit-context.rows {
		return func() {}, fmt.Errorf("kitdb: referential action affects more than %d rows", kitDBMutationRowLimit)
	}
	if context.active == nil {
		context.active = make(map[string]struct{})
	}
	keys := make([]string, len(rows))
	for index, row := range rows {
		key := operation + "\x00" + definition.ID + "\x00" + string(row.key)
		if _, found := context.active[key]; found {
			return func() {}, fmt.Errorf(
				"kitdb: referential %s cycle revisits struct %q key %s",
				operation, definition.Name, printableKitDBPrimary(definition, row.values),
			)
		}
		keys[index] = key
	}
	for _, key := range keys {
		context.active[key] = struct{}{}
	}
	context.depth++
	context.rows += len(rows)
	return func() {
		for _, key := range keys {
			delete(context.active, key)
		}
		context.depth--
	}, nil
}

// kitDBUpdateMatching is the single row-update pipeline for the ORM and SQL-light adapter.
// Matching, expression evaluation, constraint checks, index maintenance, and publication all
// observe one transaction snapshot and roll back to one savepoint on failure.
func (t *SchemaTable) kitDBUpdateMatching(
	plan query.ExecutionPlan,
	match kitDBMutationMatcher,
	changesFor kitDBMutationUpdater,
) ([]kitDBStoredRow, error) {
	if len(plan.Conditions) == 0 && plan.Predicate == nil && match == nil {
		return nil, fmt.Errorf("missing WHERE clause for update")
	}
	if changesFor == nil {
		return nil, fmt.Errorf("update has no fields")
	}
	previousReferential := t.referential
	if t.referential == nil {
		t.referential = &kitDBReferentialActionContext{}
	}
	defer func() { t.referential = previousReferential }()
	transaction, owned, err := t.kitDBWriteTransaction()
	if err != nil {
		return nil, err
	}
	previousTransaction := t.transaction
	t.transaction = transaction
	defer func() { t.transaction = previousTransaction }()
	savepoint := transaction.Savepoint()
	fail := func(err error) ([]kitDBStoredRow, error) {
		transaction.RollbackTo(savepoint)
		if owned {
			_ = transaction.Rollback()
		}
		return nil, err
	}
	rows, err := t.collectKitDBMutationRowsMatching(plan, match)
	if err != nil {
		return fail(err)
	}
	if len(rows) == 0 {
		if owned {
			_ = transaction.Rollback()
		}
		return nil, nil
	}
	leaveReferential, err := t.referential.enter("update", t.definition, rows)
	if err != nil {
		return fail(err)
	}
	defer leaveReferential()

	prepared := make([]kitDBStoredRow, 0, len(rows))
	uniqueClaims := make(map[string][]byte)
	for _, stored := range rows {
		changes, err := changesFor(stored)
		if err != nil {
			return fail(err)
		}
		if len(changes) == 0 {
			return fail(fmt.Errorf("update has no fields"))
		}
		updated := cloneKitDBRow(stored.values)
		for name, item := range changes {
			updated[name] = item
		}
		if err := validateKitDBRow(t.definition, updated); err != nil {
			return fail(err)
		}
		newRowKey, err := kitDBRowKeyForRow(t.definition, updated)
		if err != nil {
			return fail(err)
		}
		if !bytes.Equal(newRowKey, stored.key) {
			return fail(fmt.Errorf("struct %q key fields cannot be changed yet", t.table))
		}
		if err := t.validateKitDBConstraints(transaction, updated, stored.key); err != nil {
			return fail(err)
		}
		entries, err := t.kitDBIndexEntries(updated, stored.key)
		if err != nil {
			return fail(err)
		}
		for _, entry := range entries {
			if len(entry.key) == 0 || entry.key[0] != kitDBUniqueNamespace {
				continue
			}
			claim := string(entry.key)
			if owner, exists := uniqueClaims[claim]; exists && !bytes.Equal(owner, stored.key) {
				return fail(fmt.Errorf("struct %q update creates duplicate unique values", t.table))
			}
			uniqueClaims[claim] = stored.key
		}
		prepared = append(prepared, kitDBStoredRow{
			key: stored.key, values: updated, unknown: cloneKitDBRawFields(stored.unknown),
		})
	}
	for index, stored := range rows {
		if err := t.deleteKitDBIndexes(transaction, stored.values, stored.key); err != nil {
			return fail(err)
		}
		if err := t.putKitDBRow(
			transaction,
			stored.key,
			prepared[index].values,
			prepared[index].unknown,
		); err != nil {
			return fail(err)
		}
		entries, err := t.kitDBIndexEntries(prepared[index].values, stored.key)
		if err != nil {
			return fail(err)
		}
		for _, entry := range entries {
			if err := transaction.Put(entry.key, entry.value); err != nil {
				return fail(err)
			}
		}
	}
	if err := t.applyKitDBReferencedUpdates(rows, prepared); err != nil {
		return fail(err)
	}
	if err := t.invalidateKitDBStatistics(transaction); err != nil {
		return fail(err)
	}
	if owned {
		if _, err := transaction.Commit(); err != nil {
			return nil, err
		}
		t.markKitDBStatisticsStale()
	}
	return prepared, nil
}

func (t *SchemaTable) kitDBUpdateChanges(args []value.Value) (map[string]value.Value, error) {
	var changes map[string]value.Value
	switch {
	case len(args) == 1 && args[0].K == value.Map:
		changes = cloneKitDBRow(args[0].Map())
	case len(args) == 2 && args[0].K == value.String:
		name := args[0].String()
		spec := t.columns[name]
		if spec == nil {
			return nil, fmt.Errorf("struct %q has no field %q", t.table, name)
		}
		changes = map[string]value.Value{name: coerceWrite(spec.kind, args[1])}
	default:
		return nil, fmt.Errorf("update expects an object or field and value")
	}
	if len(changes) == 0 {
		return nil, fmt.Errorf("update has no fields")
	}
	for name, item := range changes {
		spec := t.columns[name]
		if spec == nil {
			return nil, fmt.Errorf("struct %q has no field %q", t.table, name)
		}
		if spec.kind == "enum" && !item.IsNil() {
			if item.K != value.String {
				return nil, fmt.Errorf("field %q choice must be a string or null", name)
			}
			if !inEnum(spec, item.String()) {
				return nil, fmt.Errorf("field %q must be one of %v", name, spec.enumVals)
			}
		}
		changes[name] = coerceWrite(spec.kind, item)
	}
	applyTouch(t.columns, changes)
	return changes, nil
}

func (t *SchemaTable) kitDBDelete() value.Value {
	plan := t.builder().ExecutionPlan()
	if len(plan.Conditions) == 0 && plan.Predicate == nil {
		return kitDBError(fmt.Errorf("missing WHERE clause for delete"))
	}
	rows, err := t.kitDBDeleteMatching(plan, nil)
	if err != nil {
		return kitDBError(err)
	}
	return value.New(len(rows))
}

// kitDBDeleteMatching shares the same bounded target selection and transactional publication
// path as ordinary ORM deletes while allowing the SQL-light adapter to add a residual matcher.
func (t *SchemaTable) kitDBDeleteMatching(
	plan query.ExecutionPlan,
	match kitDBMutationMatcher,
) ([]kitDBStoredRow, error) {
	if len(plan.Conditions) == 0 && plan.Predicate == nil && match == nil {
		return nil, fmt.Errorf("missing WHERE clause for delete")
	}
	previousReferential := t.referential
	if t.referential == nil {
		t.referential = &kitDBReferentialActionContext{}
	}
	defer func() { t.referential = previousReferential }()
	transaction, owned, err := t.kitDBWriteTransaction()
	if err != nil {
		return nil, err
	}
	previousTransaction := t.transaction
	t.transaction = transaction
	defer func() { t.transaction = previousTransaction }()
	savepoint := transaction.Savepoint()
	fail := func(err error) ([]kitDBStoredRow, error) {
		transaction.RollbackTo(savepoint)
		if owned {
			_ = transaction.Rollback()
		}
		return nil, err
	}
	rows, err := t.collectKitDBMutationRowsMatching(plan, match)
	if err != nil {
		return fail(err)
	}
	if len(rows) == 0 {
		if owned {
			_ = transaction.Rollback()
		}
		return nil, nil
	}
	leaveReferential, err := t.referential.enter("delete", t.definition, rows)
	if err != nil {
		return fail(err)
	}
	defer leaveReferential()
	for _, row := range rows {
		if err := t.deleteKitDBIndexes(transaction, row.values, row.key); err != nil {
			return fail(err)
		}
		if err := t.deleteKitDBRow(transaction, row.key); err != nil {
			return fail(err)
		}
	}
	if err := t.applyKitDBReferencedDeletes(rows); err != nil {
		return fail(err)
	}
	if err := t.invalidateKitDBStatistics(transaction); err != nil {
		return fail(err)
	}
	if owned {
		if _, err := transaction.Commit(); err != nil {
			return nil, err
		}
		t.markKitDBStatisticsStale()
	}
	return rows, nil
}

func (t *SchemaTable) collectKitDBMutationRows(plan query.ExecutionPlan) ([]kitDBStoredRow, error) {
	return t.collectKitDBMutationRowsMatching(plan, nil)
}

func (t *SchemaTable) collectKitDBMutationRowsMatching(
	plan query.ExecutionPlan,
	match kitDBMutationMatcher,
) ([]kitDBStoredRow, error) {
	rows := make([]kitDBStoredRow, 0)
	err := t.scanKitDBRows(plan, func(row kitDBStoredRow) (bool, error) {
		if match != nil {
			matched, err := match(row)
			if err != nil {
				return true, err
			}
			if !matched {
				return false, nil
			}
		}
		rows = append(rows, row)
		if len(rows) > kitDBMutationRowLimit {
			return true, fmt.Errorf("kitdb: mutation matches more than %d rows", kitDBMutationRowLimit)
		}
		return false, nil
	})
	return rows, err
}

type kitDBWriter interface {
	Put(key, encoded []byte) error
	Delete(key []byte) error
}

func deleteKitDBIndexes(tx kitDBWriter, definition *StructDef, row map[string]value.Value, rowKey []byte) error {
	return deleteKitDBIndexesForPhysical(
		tx,
		definition,
		row,
		rowKey,
		kitDBPhysicalIndexes(collectIndexes(definition.Name, definition.columns), nil),
	)
}

func deleteKitDBIndexesForPhysical(
	tx kitDBWriter,
	definition *StructDef,
	row map[string]value.Value,
	rowKey []byte,
	indexes []kitDBPhysicalIndex,
) error {
	entries, err := kitDBIndexEntriesForPhysical(definition, row, rowKey, indexes)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := tx.Delete(entry.key); err != nil {
			return err
		}
	}
	return nil
}

func (t *SchemaTable) deleteKitDBIndexes(
	tx kitDBWriter,
	row map[string]value.Value,
	rowKey []byte,
) error {
	entries, err := t.kitDBIndexEntries(row, rowKey)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := tx.Delete(entry.key); err != nil {
			return err
		}
	}
	return nil
}

type kitDBReferentialRelation struct {
	child        *SchemaTable
	localFields  []StructFieldDef
	targetFields []StructFieldDef
	name         string
	onDelete     string
	onUpdate     string
}

type kitDBReferentialTarget struct {
	next []value.Value
}

func (t *SchemaTable) kitDBReferentialRelations() ([]kitDBReferentialRelation, error) {
	names := make([]string, 0, len(t.definitions))
	for name := range t.definitions {
		names = append(names, name)
	}
	sort.Strings(names)
	children := make(map[string]*SchemaTable)
	childFor := func(name string, definition *StructDef) (*SchemaTable, error) {
		if child := children[name]; child != nil {
			return child, nil
		}
		child := &SchemaTable{
			tenant: t.tenant, scope: t.scope, engine: "kitdb", dbName: t.dbName,
			table: name, columns: definition.columns, definition: definition,
			definitions: t.definitions, transaction: t.transaction, referential: t.referential,
		}
		if err := child.ensureKitDBStruct(); err != nil {
			return nil, err
		}
		children[name] = child
		return child, nil
	}

	relations := make([]kitDBReferentialRelation, 0)
	for _, childName := range names {
		childDefinition := t.definitions[childName]
		if childDefinition == nil {
			continue
		}
		for _, field := range childDefinition.Fields {
			if field.Reference == nil || field.Reference.Struct != t.definition.Name {
				continue
			}
			targetField, found := kitDBField(t.definition, field.Reference.Field)
			if !found {
				return nil, fmt.Errorf("kitdb: reference target %s.%s is unavailable", t.definition.Name, field.Reference.Field)
			}
			child, err := childFor(childName, childDefinition)
			if err != nil {
				return nil, err
			}
			relations = append(relations, kitDBReferentialRelation{
				child: child, localFields: []StructFieldDef{field}, targetFields: []StructFieldDef{targetField},
				onDelete: field.Reference.OnDelete, onUpdate: field.Reference.OnUpdate,
			})
		}
		for _, constraint := range childDefinition.ForeignConstraints {
			if constraint.TargetStructID != t.definition.ID {
				continue
			}
			localFields, targetFields, err := structForeignConstraintFields(childDefinition, t.definition, constraint)
			if err != nil {
				return nil, err
			}
			child, err := childFor(childName, childDefinition)
			if err != nil {
				return nil, err
			}
			relations = append(relations, kitDBReferentialRelation{
				child: child, localFields: localFields, targetFields: targetFields, name: constraint.Name,
				onDelete: constraint.OnDelete, onUpdate: constraint.OnUpdate,
			})
		}
	}
	return relations, nil
}

func kitDBReferentialTuple(
	row map[string]value.Value,
	fields []StructFieldDef,
) (string, []value.Value, bool, error) {
	encoded := make([]byte, 0, len(fields)*10)
	items := make([]value.Value, len(fields))
	for index, field := range fields {
		item, found := row[field.Name]
		if !found || item.IsNil() {
			return "", nil, false, nil
		}
		component, err := kitDBFieldScalarComponent(field, item)
		if err != nil {
			return "", nil, false, fmt.Errorf("kitdb: referential field %q: %w", field.Name, err)
		}
		encoded = append(encoded, component...)
		items[index] = item
	}
	return string(encoded), items, true, nil
}

func (relation kitDBReferentialRelation) targets(
	before, after []kitDBStoredRow,
) (map[string]kitDBReferentialTarget, error) {
	targets := make(map[string]kitDBReferentialTarget, len(before))
	for index := range before {
		key, oldValues, applicable, err := kitDBReferentialTuple(before[index].values, relation.targetFields)
		if err != nil {
			return nil, err
		}
		if !applicable {
			continue
		}
		var next []value.Value
		if after != nil {
			_, next, _, err = kitDBReferentialTupleAllowNull(after[index].values, relation.targetFields)
			if err != nil {
				return nil, err
			}
			changed := false
			for fieldIndex := range oldValues {
				if kitDBCompareValues(oldValues[fieldIndex], next[fieldIndex]) != 0 {
					changed = true
					break
				}
			}
			if !changed {
				continue
			}
		}
		targets[key] = kitDBReferentialTarget{next: next}
	}
	return targets, nil
}

func kitDBReferentialTupleAllowNull(
	row map[string]value.Value,
	fields []StructFieldDef,
) (string, []value.Value, bool, error) {
	encoded := make([]byte, 0, len(fields)*10)
	items := make([]value.Value, len(fields))
	allPresent := true
	for index, field := range fields {
		item, found := row[field.Name]
		if !found {
			item = value.NewNil()
			allPresent = false
		}
		component, err := kitDBFieldScalarComponent(field, item)
		if err != nil {
			return "", nil, false, fmt.Errorf("kitdb: referential field %q: %w", field.Name, err)
		}
		encoded = append(encoded, component...)
		items[index] = item
	}
	return string(encoded), items, allPresent, nil
}

func (relation kitDBReferentialRelation) matcher(
	targets map[string]kitDBReferentialTarget,
) kitDBMutationMatcher {
	return func(row kitDBStoredRow) (bool, error) {
		key, _, applicable, err := kitDBReferentialTuple(row.values, relation.localFields)
		if err != nil || !applicable {
			return false, err
		}
		_, found := targets[key]
		return found, nil
	}
}

func (relation kitDBReferentialRelation) description() string {
	if relation.name != "" {
		return fmt.Sprintf("%s constraint %q", relation.child.table, relation.name)
	}
	return fmt.Sprintf("%s.%s", relation.child.table, relation.localFields[0].Name)
}

func (relation kitDBReferentialRelation) restrict(
	operation, parent string,
	targets map[string]kitDBReferentialTarget,
) error {
	if len(targets) == 0 {
		return nil
	}
	referenced := false
	err := relation.child.scanKitDBRows(query.ExecutionPlan{}, func(row kitDBStoredRow) (bool, error) {
		matched, err := relation.matcher(targets)(row)
		if err != nil || !matched {
			return false, err
		}
		referenced = true
		return true, nil
	})
	if err != nil {
		return err
	}
	if referenced {
		return fmt.Errorf("kitdb: cannot %s %s; %s still references it", operation, parent, relation.description())
	}
	return nil
}

func (relation kitDBReferentialRelation) updateChanges(
	action string,
	targets map[string]kitDBReferentialTarget,
) (kitDBMutationUpdater, error) {
	fixed := make(map[string]value.Value, len(relation.localFields))
	switch action {
	case "setnull":
		for _, field := range relation.localFields {
			fixed[field.Name] = value.NewNil()
		}
	case "setdefault":
		for _, field := range relation.localFields {
			spec := relation.child.columns[field.Name]
			item := value.NewNil()
			if generated, found := autoValue(spec); found {
				item = coerceWrite(spec.kind, generated)
			}
			fixed[field.Name] = item
		}
	case "cascade":
	default:
		return nil, fmt.Errorf("kitdb: unsupported referential update action %q", action)
	}
	touches := make(map[string]value.Value)
	applyTouch(relation.child.columns, touches)
	return func(row kitDBStoredRow) (map[string]value.Value, error) {
		changes := cloneKitDBRow(fixed)
		if action == "cascade" {
			key, _, applicable, err := kitDBReferentialTuple(row.values, relation.localFields)
			if err != nil {
				return nil, err
			}
			target, found := targets[key]
			if !applicable || !found || len(target.next) != len(relation.localFields) {
				return nil, fmt.Errorf("kitdb: referential cascade target is unavailable")
			}
			for index, field := range relation.localFields {
				changes[field.Name] = target.next[index]
			}
		}
		for field, item := range touches {
			if _, explicit := changes[field]; !explicit {
				changes[field] = item
			}
		}
		return changes, nil
	}, nil
}

func (t *SchemaTable) applyKitDBReferencedDeletes(rows []kitDBStoredRow) error {
	relations, err := t.kitDBReferentialRelations()
	if err != nil {
		return err
	}
	for pass := 0; pass < 2; pass++ {
		for _, relation := range relations {
			action := comparableFKActionName(relation.onDelete)
			restrictive := action == "restrict" || action == "noaction"
			if (pass == 0) != restrictive {
				continue
			}
			targets, err := relation.targets(rows, nil)
			if err != nil || len(targets) == 0 {
				if err != nil {
					return err
				}
				continue
			}
			if restrictive {
				if err := relation.restrict("delete", t.definition.Name, targets); err != nil {
					return err
				}
				continue
			}
			matcher := relation.matcher(targets)
			switch action {
			case "cascade":
				if _, err := relation.child.kitDBDeleteMatching(query.ExecutionPlan{}, matcher); err != nil {
					return err
				}
			case "setnull", "setdefault":
				updater, err := relation.updateChanges(action, targets)
				if err != nil {
					return err
				}
				if _, err := relation.child.kitDBUpdateMatching(query.ExecutionPlan{}, matcher, updater); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (t *SchemaTable) applyKitDBReferencedUpdates(before, after []kitDBStoredRow) error {
	relations, err := t.kitDBReferentialRelations()
	if err != nil {
		return err
	}
	for pass := 0; pass < 2; pass++ {
		for _, relation := range relations {
			action := comparableFKActionName(relation.onUpdate)
			restrictive := action == "restrict" || action == "noaction"
			if (pass == 0) != restrictive {
				continue
			}
			targets, err := relation.targets(before, after)
			if err != nil || len(targets) == 0 {
				if err != nil {
					return err
				}
				continue
			}
			if restrictive {
				if err := relation.restrict("update", t.definition.Name, targets); err != nil {
					return err
				}
				continue
			}
			updater, err := relation.updateChanges(action, targets)
			if err != nil {
				return err
			}
			if _, err := relation.child.kitDBUpdateMatching(
				query.ExecutionPlan{}, relation.matcher(targets), updater,
			); err != nil {
				return err
			}
		}
	}
	return nil
}
