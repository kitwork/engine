package work

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"

	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
)

const (
	kitDBCatalogNamespace byte = 0x01
	kitDBRowNamespace     byte = 0x10
	kitDBUniqueNamespace  byte = 0x20
	kitDBIndexNamespace   byte = 0x30

	kitDBMutationRowLimit = 10_000
)

type kitDBReader interface {
	Get(key []byte) ([]byte, bool, error)
}

type kitDBStoredRow struct {
	key    []byte
	values map[string]value.Value
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
	if t.definition == nil {
		return fmt.Errorf("KitDB table %q has no struct definition", t.table)
	}
	managed, err := t.kitDBManaged()
	if err != nil {
		return err
	}
	managed.writeMu.Lock()
	defer managed.writeMu.Unlock()

	names := make([]string, 0, len(t.definitions))
	for name := range t.definitions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		definition := t.definitions[name]
		if err := validateKitDBStruct(definition); err != nil {
			return err
		}
		if err := ensureKitDBCatalog(managed.database, definition); err != nil {
			return err
		}
	}
	return nil
}

func validateKitDBStruct(definition *StructDef) error {
	if definition == nil || definition.Name == "" || len(definition.columns) == 0 {
		return fmt.Errorf("kitdb: invalid struct definition")
	}
	if err := validateSchema(definition.columns); err != nil {
		return fmt.Errorf("kitdb: struct %q: %w", definition.Name, err)
	}
	primary := 0
	for _, field := range definition.Fields {
		if field.Primary {
			primary++
		}
		if field.Reference != nil {
			for _, policy := range []struct{ label, action string }{
				{"onDelete", field.Reference.OnDelete},
				{"onUpdate", field.Reference.OnUpdate},
			} {
				normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(policy.action), " ", ""))
				switch normalized {
				case "", "noaction", "restrict":
				default:
					return fmt.Errorf("kitdb: struct %q field %q: %s %q is not implemented yet; constraint refused", definition.Name, field.Name, policy.label, policy.action)
				}
			}
		}
	}
	if primary != 1 {
		return fmt.Errorf("kitdb: struct %q needs exactly one .key() field (found %d)", definition.Name, primary)
	}
	return nil
}

func ensureKitDBCatalog(database *kitdbengine.DB, definition *StructDef) error {
	key, err := kitDBCatalogKey(definition)
	if err != nil {
		return err
	}
	current, found, err := database.Get(key)
	if err != nil {
		return err
	}
	if found {
		var stored StructDef
		if err := json.Unmarshal(current, &stored); err != nil {
			return fmt.Errorf("kitdb: decode struct catalog %q: %w", definition.Name, err)
		}
		if stored.Hash == definition.Hash {
			return nil
		}
		hasRows, err := kitDBHasRows(database, definition)
		if err != nil {
			return err
		}
		if hasRows {
			return fmt.Errorf("kitdb: struct %q changed from %s to %s; explicit migration is required", definition.Name, shortSchemaHash(stored.Hash), shortSchemaHash(definition.Hash))
		}
	}
	encoded, err := json.Marshal(definition)
	if err != nil {
		return fmt.Errorf("kitdb: encode struct catalog %q: %w", definition.Name, err)
	}
	tx, err := database.Begin()
	if err != nil {
		return err
	}
	if err := tx.Put(key, encoded); err != nil {
		_ = tx.Rollback()
		return err
	}
	_, err = tx.Commit()
	return err
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
	key, err := kitDBCatalogKey(t.definition)
	if err != nil {
		return kitDBError(err)
	}
	encoded, found, err := managed.database.Get(key)
	if err != nil {
		return kitDBError(err)
	}
	action := "create"
	destructive := false
	willApply := true
	from := ""
	if found {
		var stored StructDef
		if err := json.Unmarshal(encoded, &stored); err != nil {
			return kitDBError(err)
		}
		if stored.Hash == t.definition.Hash {
			return value.New([]value.Value{})
		}
		action = "change"
		destructive = true
		willApply = false
		from = stored.Hash
	}
	return value.New([]value.Value{value.New(map[string]value.Value{
		"action": value.New(action), "from": value.New(from), "to": value.New(t.definition.Hash),
		"destructive": value.New(destructive), "willApply": value.New(willApply),
	})})
}

func (t *SchemaTable) kitDBCreate(row map[string]value.Value) value.Value {
	if err := validateKitDBRow(t.definition, row); err != nil {
		return kitDBError(err)
	}
	encoded, err := encodeKitDBRow(row)
	if err != nil {
		return kitDBError(err)
	}
	primary, _ := t.definition.primaryField()
	rowKey, err := kitDBRowKey(t.definition, row[primary.Name])
	if err != nil {
		return kitDBError(err)
	}
	managed, err := t.kitDBManaged()
	if err != nil {
		return kitDBError(err)
	}
	managed.writeMu.Lock()
	defer managed.writeMu.Unlock()
	if err := checkpointKitDBBeforeWrite(managed.database); err != nil {
		return kitDBError(err)
	}
	if err := t.validateKitDBConstraints(managed.database, row, nil); err != nil {
		return kitDBError(err)
	}
	if _, found, err := managed.database.Get(rowKey); err != nil {
		return kitDBError(err)
	} else if found {
		return kitDBError(fmt.Errorf("struct %q already has key %s", t.table, printableKitDBValue(row[primary.Name])))
	}
	entries, err := kitDBIndexEntries(t.definition, row, rowKey)
	if err != nil {
		return kitDBError(err)
	}
	tx, err := managed.database.Begin()
	if err != nil {
		return kitDBError(err)
	}
	if err := tx.Put(rowKey, encoded); err != nil {
		_ = tx.Rollback()
		return kitDBError(err)
	}
	for _, entry := range entries {
		if err := tx.Put(entry.key, entry.value); err != nil {
			_ = tx.Rollback()
			return kitDBError(err)
		}
	}
	if _, err := tx.Commit(); err != nil {
		return kitDBError(err)
	}
	return coerceResult(t.columns, value.New(cloneKitDBRow(row)))
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
		if found && len(field.Enum) != 0 && item.K == value.String {
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
	return validateKitDBJSON(value.New(row))
}

func encodeKitDBRow(row map[string]value.Value) ([]byte, error) {
	encoded, err := json.Marshal(value.New(row))
	if err != nil {
		return nil, fmt.Errorf("kitdb: encode row: %w", err)
	}
	if len(encoded) > kitDBDocumentLimit {
		return nil, fmt.Errorf("kitdb: row exceeds %d bytes", kitDBDocumentLimit)
	}
	return encoded, nil
}

func decodeKitDBRow(encoded []byte) (map[string]value.Value, error) {
	decoded := decodeKitDBValue(encoded)
	if decoded.K == value.Invalid {
		return nil, fmt.Errorf("%s", decoded.Text())
	}
	if decoded.K != value.Map {
		return nil, fmt.Errorf("kitdb: stored row is not an object")
	}
	return decoded.Map(), nil
}

func cloneKitDBRow(row map[string]value.Value) map[string]value.Value {
	cloned := make(map[string]value.Value, len(row))
	for name, item := range row {
		cloned[name] = item
	}
	return cloned
}

func (t *SchemaTable) validateKitDBConstraints(reader kitDBReader, row map[string]value.Value, previousRowKey []byte) error {
	primary, _ := t.definition.primaryField()
	primaryKey, err := kitDBRowKey(t.definition, row[primary.Name])
	if err != nil {
		return err
	}
	if existing, found, err := reader.Get(primaryKey); err != nil {
		return err
	} else if found && !bytes.Equal(primaryKey, previousRowKey) && len(existing) != 0 {
		return fmt.Errorf("kitdb: struct %q key %s already exists", t.table, printableKitDBValue(row[primary.Name]))
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
	var key []byte
	var err error
	if targetField.Primary {
		key, err = kitDBRowKey(target, item)
	} else {
		key, err = kitDBUniqueKey(target, targetField, item)
	}
	if err != nil {
		return err
	}
	_, exists, err := reader.Get(key)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("kitdb: struct %q field %q references missing %s.%s", t.table, field.Name, reference.Struct, reference.Field)
	}
	return nil
}

func kitDBField(definition *StructDef, name string) (StructFieldDef, bool) {
	if definition != nil {
		for _, field := range definition.Fields {
			if field.Name == name {
				return field, true
			}
		}
	}
	return StructFieldDef{}, false
}

func kitDBIndexEntries(definition *StructDef, row map[string]value.Value, rowKey []byte) ([]kitDBIndexEntry, error) {
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
	for _, index := range collectIndexes(definition.Name, definition.columns) {
		if !kitDBPartialIndexMatches(definition, row, index.filter) {
			continue
		}
		prefix, err := kitDBIndexPrefix(definition, index, row)
		if err != nil {
			return nil, err
		}
		primary, _ := definition.primaryField()
		component, err := kitDBScalarComponent(row[primary.Name])
		if err != nil {
			return nil, err
		}
		key := append(bytes.Clone(prefix), component...)
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

func kitDBCatalogKey(definition *StructDef) ([]byte, error) {
	return kitDBFixedKey(kitDBCatalogNamespace, definition.ID, "")
}

func kitDBRowPrefix(definition *StructDef) ([]byte, error) {
	return kitDBFixedKey(kitDBRowNamespace, definition.ID, "")
}

func kitDBRowKey(definition *StructDef, primary value.Value) ([]byte, error) {
	prefix, err := kitDBRowPrefix(definition)
	if err != nil {
		return nil, err
	}
	component, err := kitDBScalarComponent(primary)
	if err != nil {
		return nil, fmt.Errorf("kitdb: struct %q key: %w", definition.Name, err)
	}
	return append(prefix, component...), nil
}

func kitDBUniqueKey(definition *StructDef, field StructFieldDef, item value.Value) ([]byte, error) {
	prefix, err := kitDBFixedKey(kitDBUniqueNamespace, definition.ID, field.ID)
	if err != nil {
		return nil, err
	}
	component, err := kitDBScalarComponent(item)
	if err != nil {
		return nil, fmt.Errorf("kitdb: unique field %q: %w", field.Name, err)
	}
	return append(prefix, component...), nil
}

func kitDBIndexPrefix(definition *StructDef, index indexDef, row map[string]value.Value) ([]byte, error) {
	indexIdentity := stableSchemaID("index", definition.ID+":"+index.name+":"+strings.Join(index.columns, ","))
	prefix, err := kitDBFixedKey(kitDBIndexNamespace, definition.ID, indexIdentity)
	if err != nil {
		return nil, err
	}
	for _, column := range index.columns {
		item, found := row[column]
		if !found {
			item = value.NewNil()
		}
		component, err := kitDBScalarComponent(item)
		if err != nil {
			return nil, fmt.Errorf("kitdb: index %q field %q: %w", index.name, column, err)
		}
		prefix = append(prefix, component...)
	}
	return prefix, nil
}

func kitDBFixedKey(namespace byte, structID, childID string) ([]byte, error) {
	structure, err := hex.DecodeString(structID)
	if err != nil || len(structure) != 16 {
		return nil, fmt.Errorf("kitdb: invalid struct identity")
	}
	key := make([]byte, 1, 33)
	key[0] = namespace
	key = append(key, structure...)
	if childID != "" {
		child, err := hex.DecodeString(childID)
		if err != nil || len(child) != 16 {
			return nil, fmt.Errorf("kitdb: invalid child identity")
		}
		key = append(key, child...)
	}
	return key, nil
}

func kitDBScalarComponent(item value.Value) ([]byte, error) {
	payload := make([]byte, 1, 17)
	switch item.K {
	case value.Nil:
		payload[0] = 0
	case value.Bool:
		payload[0] = 1
		flag := byte(0)
		if item.N != 0 {
			flag = 1
		}
		payload = append(payload, flag)
	case value.Number:
		if math.IsNaN(item.N) || math.IsInf(item.N, 0) {
			return nil, fmt.Errorf("non-finite numbers cannot be keys")
		}
		payload[0] = 2
		numberValue := item.N
		if numberValue == 0 {
			numberValue = 0
		}
		bits := math.Float64bits(numberValue)
		if bits&(uint64(1)<<63) != 0 {
			bits = ^bits
		} else {
			bits ^= uint64(1) << 63
		}
		var number [8]byte
		binary.BigEndian.PutUint64(number[:], bits)
		payload = append(payload, number[:]...)
	case value.String:
		payload[0] = 3
		payload = append(payload, item.String()...)
	case value.Bytes:
		payload[0] = 4
		if data, ok := item.V.([]byte); ok {
			payload = append(payload, data...)
		}
	case value.Time, value.Duration:
		payload[0] = 5
		var number [8]byte
		binary.BigEndian.PutUint64(number[:], math.Float64bits(item.N))
		payload = append(payload, number[:]...)
	default:
		return nil, fmt.Errorf("value kind %s is not scalar", item.K)
	}
	component := make([]byte, binary.MaxVarintLen64, binary.MaxVarintLen64+len(payload))
	size := binary.PutUvarint(component, uint64(len(payload)))
	component = component[:size]
	component = append(component, payload...)
	return component, nil
}

func kitDBHasRows(reader kitDBReader, definition *StructDef) (bool, error) {
	database, ok := reader.(*kitdbengine.DB)
	if !ok {
		return false, fmt.Errorf("kitdb: row scan requires a database handle")
	}
	prefix, err := kitDBRowPrefix(definition)
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
	primary, ok := t.definition.primaryField()
	if !ok {
		return kitDBError(fmt.Errorf("struct %q has no key", t.table))
	}
	switch len(args) {
	case 0:
		return value.NewNil()
	case 1:
		if args[0].IsCallable() {
			t.Where(args[0])
		} else {
			t.Where(value.New(primary.Name), value.New("="), args[0])
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
	countField := ""
	switch len(args) {
	case 1:
		if args[0].K == value.String {
			countField = args[0].String()
			if !t.known(countField) {
				return kitDBError(fmt.Errorf("struct %q has no field %q", t.table, countField))
			}
		} else if args[0].IsCallable() {
			t.Where(args[0])
		}
	case 2, 3:
		t.Where(args...)
	}
	count := 0
	err := t.scanKitDBRows(t.builder().ExecutionPlan(), func(row kitDBStoredRow) (bool, error) {
		if countField == "" {
			count++
		} else if item, found := row.values[countField]; found && !item.IsNil() {
			count++
		}
		return false, nil
	})
	if err != nil {
		return kitDBError(err)
	}
	return value.New(count)
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
	limit := forceLimit
	if limit <= 0 {
		limit = plan.Limit
		if limit <= 0 {
			limit = query.DefaultDBLimit
		}
		maximum := plan.MaxLimit
		if maximum <= 0 {
			maximum = query.DefaultDBMaxLimit
		}
		if limit > maximum {
			limit = maximum
		}
	}
	if limit < 1 {
		return nil, nil
	}
	offset := plan.Offset
	if offset < 0 {
		offset = 0
	}
	needed := limit + offset
	rows := make([]kitDBStoredRow, 0, needed)
	ordered := len(plan.Orders) != 0
	err := t.scanKitDBRows(plan, func(row kitDBStoredRow) (bool, error) {
		rows = append(rows, row)
		if ordered {
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
	managed, err := t.kitDBManaged()
	if err != nil {
		return err
	}
	snapshot, err := managed.database.Snapshot()
	if err != nil {
		return err
	}
	defer snapshot.Close()

	consumeKey := func(rowKey []byte) (bool, error) {
		encoded, found, err := snapshot.Get(rowKey)
		if err != nil || !found {
			return false, err
		}
		return t.consumeKitDBRow(plan, rowKey, encoded, visit)
	}

	if rowKey, ok, err := t.kitDBPrimaryCandidate(plan); err != nil {
		return err
	} else if ok {
		_, err := consumeKey(rowKey)
		return err
	}
	if rowKey, ok, err := t.kitDBUniqueCandidate(snapshot, plan); err != nil {
		return err
	} else if ok {
		if len(rowKey) == 0 {
			return nil
		}
		_, err := consumeKey(rowKey)
		return err
	}
	if prefix, ok, err := t.kitDBIndexCandidate(plan); err != nil {
		return err
	} else if ok {
		cursor, err := snapshot.Cursor(kitdbengine.RangeOptions{Prefix: prefix})
		if err != nil {
			return err
		}
		defer cursor.Close()
		for cursor.Next() {
			if err := t.kitDBRequestError(); err != nil {
				return err
			}
			stop, err := consumeKey(cursor.Value())
			if err != nil || stop {
				return err
			}
		}
		return cursor.Err()
	}

	prefix, err := kitDBRowPrefix(t.definition)
	if err != nil {
		return err
	}
	cursor, err := snapshot.Cursor(kitdbengine.RangeOptions{Prefix: prefix})
	if err != nil {
		return err
	}
	defer cursor.Close()
	for cursor.Next() {
		if err := t.kitDBRequestError(); err != nil {
			return err
		}
		stop, err := t.consumeKitDBRow(plan, cursor.Key(), cursor.Value(), visit)
		if err != nil || stop {
			return err
		}
	}
	return cursor.Err()
}

func (t *SchemaTable) consumeKitDBRow(plan query.ExecutionPlan, rowKey, encoded []byte, visit func(kitDBStoredRow) (bool, error)) (bool, error) {
	row, err := decodeKitDBRow(encoded)
	if err != nil {
		return false, err
	}
	matches, err := t.kitDBMatches(row, plan.Conditions)
	if err != nil || !matches {
		return false, err
	}
	return visit(kitDBStoredRow{key: bytes.Clone(rowKey), values: row})
}

func (t *SchemaTable) kitDBPrimaryCandidate(plan query.ExecutionPlan) ([]byte, bool, error) {
	values, safe, err := t.kitDBEqualityValues(plan)
	if err != nil || !safe {
		return nil, false, err
	}
	primary, _ := t.definition.primaryField()
	item, found := values[primary.Name]
	if !found {
		return nil, false, nil
	}
	key, err := kitDBRowKey(t.definition, item)
	return key, true, err
}

func (t *SchemaTable) kitDBUniqueCandidate(reader kitDBReader, plan query.ExecutionPlan) ([]byte, bool, error) {
	values, safe, err := t.kitDBEqualityValues(plan)
	if err != nil || !safe {
		return nil, false, err
	}
	for _, field := range t.definition.Fields {
		item, found := values[field.Name]
		if !field.Unique || field.Primary || !found || item.IsNil() {
			continue
		}
		key, err := kitDBUniqueKey(t.definition, field, item)
		if err != nil {
			return nil, false, err
		}
		rowKey, exists, err := reader.Get(key)
		if err != nil {
			return nil, false, err
		}
		if !exists {
			return nil, true, nil
		}
		return rowKey, true, nil
	}
	return nil, false, nil
}

func (t *SchemaTable) kitDBIndexCandidate(plan query.ExecutionPlan) ([]byte, bool, error) {
	values, safe, err := t.kitDBEqualityValues(plan)
	if err != nil || !safe {
		return nil, false, err
	}
	indexes := collectIndexes(t.definition.Name, t.definition.columns)
	sort.SliceStable(indexes, func(left, right int) bool {
		return len(indexes[left].columns) > len(indexes[right].columns)
	})
	for _, index := range indexes {
		if len(index.filter) != 0 {
			continue
		}
		row := make(map[string]value.Value, len(index.columns))
		complete := true
		for _, column := range index.columns {
			item, found := values[column]
			if !found {
				complete = false
				break
			}
			row[column] = item
		}
		if !complete {
			continue
		}
		prefix, err := kitDBIndexPrefix(t.definition, index, row)
		return prefix, true, err
	}
	return nil, false, nil
}

func (t *SchemaTable) kitDBEqualityValues(plan query.ExecutionPlan) (map[string]value.Value, bool, error) {
	values := make(map[string]value.Value)
	for _, condition := range plan.Conditions {
		if !strings.EqualFold(condition.Logic, "and") || condition.IsColumn || !kitDBEqualityOperator(condition.Operator) {
			return nil, false, nil
		}
		column := kitDBColumnName(condition.Column)
		spec := t.columns[column]
		if spec == nil {
			return nil, false, fmt.Errorf("kitdb: struct %q has no field %q", t.table, column)
		}
		item := kitDBAnyValue(condition.Value)
		values[column] = coerceWrite(spec.kind, item)
	}
	return values, true, nil
}

func kitDBEqualityOperator(operator string) bool {
	switch strings.ToLower(strings.TrimSpace(operator)) {
	case "", "=", "==", "===":
		return true
	default:
		return false
	}
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
	if len(plan.Conditions) == 0 {
		return kitDBError(fmt.Errorf("missing WHERE clause for update"))
	}
	changes, err := t.kitDBUpdateChanges(args)
	if err != nil {
		return kitDBError(err)
	}
	managed, err := t.kitDBManaged()
	if err != nil {
		return kitDBError(err)
	}
	managed.writeMu.Lock()
	defer managed.writeMu.Unlock()
	if err := checkpointKitDBBeforeWrite(managed.database); err != nil {
		return kitDBError(err)
	}
	rows, err := t.collectKitDBMutationRows(plan)
	if err != nil {
		return kitDBError(err)
	}
	if len(rows) == 0 {
		return value.NewNil()
	}

	primary, _ := t.definition.primaryField()
	prepared := make([]kitDBStoredRow, 0, len(rows))
	uniqueClaims := make(map[string][]byte)
	for _, stored := range rows {
		updated := cloneKitDBRow(stored.values)
		for name, item := range changes {
			updated[name] = item
		}
		if err := validateKitDBRow(t.definition, updated); err != nil {
			return kitDBError(err)
		}
		newRowKey, err := kitDBRowKey(t.definition, updated[primary.Name])
		if err != nil {
			return kitDBError(err)
		}
		if !bytes.Equal(newRowKey, stored.key) {
			return kitDBError(fmt.Errorf("struct %q key field %q cannot be changed yet", t.table, primary.Name))
		}
		if err := t.validateKitDBConstraints(managed.database, updated, stored.key); err != nil {
			return kitDBError(err)
		}
		entries, err := kitDBIndexEntries(t.definition, updated, stored.key)
		if err != nil {
			return kitDBError(err)
		}
		for _, entry := range entries {
			if len(entry.key) == 0 || entry.key[0] != kitDBUniqueNamespace {
				continue
			}
			claim := string(entry.key)
			if owner, exists := uniqueClaims[claim]; exists && !bytes.Equal(owner, stored.key) {
				return kitDBError(fmt.Errorf("struct %q update creates duplicate unique values", t.table))
			}
			uniqueClaims[claim] = stored.key
		}
		prepared = append(prepared, kitDBStoredRow{key: stored.key, values: updated})
	}
	if err := t.rejectKitDBReferencedUpdates(rows, prepared); err != nil {
		return kitDBError(err)
	}

	tx, err := managed.database.Begin()
	if err != nil {
		return kitDBError(err)
	}
	for index, stored := range rows {
		if err := deleteKitDBIndexes(tx, t.definition, stored.values, stored.key); err != nil {
			_ = tx.Rollback()
			return kitDBError(err)
		}
		encoded, err := encodeKitDBRow(prepared[index].values)
		if err != nil {
			_ = tx.Rollback()
			return kitDBError(err)
		}
		if err := tx.Put(stored.key, encoded); err != nil {
			_ = tx.Rollback()
			return kitDBError(err)
		}
		entries, err := kitDBIndexEntries(t.definition, prepared[index].values, stored.key)
		if err != nil {
			_ = tx.Rollback()
			return kitDBError(err)
		}
		for _, entry := range entries {
			if err := tx.Put(entry.key, entry.value); err != nil {
				_ = tx.Rollback()
				return kitDBError(err)
			}
		}
	}
	if _, err := tx.Commit(); err != nil {
		return kitDBError(err)
	}
	return coerceResult(t.columns, value.New(cloneKitDBRow(prepared[0].values)))
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
		if spec.kind == "enum" && item.K == value.String && !inEnum(spec, item.String()) {
			return nil, fmt.Errorf("field %q must be one of %v", name, spec.enumVals)
		}
		changes[name] = coerceWrite(spec.kind, item)
	}
	applyTouch(t.columns, changes)
	return changes, nil
}

func (t *SchemaTable) kitDBDelete() value.Value {
	plan := t.builder().ExecutionPlan()
	if len(plan.Conditions) == 0 {
		return kitDBError(fmt.Errorf("missing WHERE clause for delete"))
	}
	managed, err := t.kitDBManaged()
	if err != nil {
		return kitDBError(err)
	}
	managed.writeMu.Lock()
	defer managed.writeMu.Unlock()
	if err := checkpointKitDBBeforeWrite(managed.database); err != nil {
		return kitDBError(err)
	}
	rows, err := t.collectKitDBMutationRows(plan)
	if err != nil {
		return kitDBError(err)
	}
	if len(rows) == 0 {
		return value.New(0)
	}
	if err := t.rejectKitDBReferencedDeletes(rows); err != nil {
		return kitDBError(err)
	}
	tx, err := managed.database.Begin()
	if err != nil {
		return kitDBError(err)
	}
	for _, row := range rows {
		if err := deleteKitDBIndexes(tx, t.definition, row.values, row.key); err != nil {
			_ = tx.Rollback()
			return kitDBError(err)
		}
		if err := tx.Delete(row.key); err != nil {
			_ = tx.Rollback()
			return kitDBError(err)
		}
	}
	if _, err := tx.Commit(); err != nil {
		return kitDBError(err)
	}
	return value.New(len(rows))
}

func (t *SchemaTable) collectKitDBMutationRows(plan query.ExecutionPlan) ([]kitDBStoredRow, error) {
	rows := make([]kitDBStoredRow, 0)
	err := t.scanKitDBRows(plan, func(row kitDBStoredRow) (bool, error) {
		rows = append(rows, row)
		if len(rows) > kitDBMutationRowLimit {
			return true, fmt.Errorf("kitdb: mutation matches more than %d rows", kitDBMutationRowLimit)
		}
		return false, nil
	})
	return rows, err
}

func deleteKitDBIndexes(tx *kitdbengine.Tx, definition *StructDef, row map[string]value.Value, rowKey []byte) error {
	entries, err := kitDBIndexEntries(definition, row, rowKey)
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

func (t *SchemaTable) rejectKitDBReferencedDeletes(rows []kitDBStoredRow) error {
	deleting := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		deleting[string(row.key)] = struct{}{}
	}
	for childName, childDefinition := range t.definitions {
		for _, field := range childDefinition.Fields {
			if field.Reference == nil || field.Reference.Struct != t.definition.Name {
				continue
			}
			targetField, found := kitDBField(t.definition, field.Reference.Field)
			if !found {
				return fmt.Errorf("kitdb: reference target %s.%s is unavailable", t.definition.Name, field.Reference.Field)
			}
			child := &SchemaTable{
				tenant: t.tenant, scope: t.scope, engine: "kitdb", dbName: t.dbName,
				table: childName, columns: childDefinition.columns,
				definition: childDefinition, definitions: t.definitions,
			}
			for _, parent := range rows {
				target, exists := parent.values[targetField.Name]
				if !exists {
					continue
				}
				plan := query.ExecutionPlan{Conditions: []query.Condition{{
					Column: field.Name, Operator: "=", Value: target, Logic: "AND",
				}}}
				var externalReference bool
				err := child.scanKitDBRows(plan, func(candidate kitDBStoredRow) (bool, error) {
					if childDefinition.ID == t.definition.ID {
						if _, removed := deleting[string(candidate.key)]; removed {
							return false, nil
						}
					}
					externalReference = true
					return true, nil
				})
				if err != nil {
					return err
				}
				if externalReference {
					return fmt.Errorf("kitdb: cannot delete %s; %s.%s still references it", t.definition.Name, childName, field.Name)
				}
			}
		}
	}
	return nil
}

func (t *SchemaTable) rejectKitDBReferencedUpdates(before, after []kitDBStoredRow) error {
	for childName, childDefinition := range t.definitions {
		for _, field := range childDefinition.Fields {
			if field.Reference == nil || field.Reference.Struct != t.definition.Name {
				continue
			}
			targetField, found := kitDBField(t.definition, field.Reference.Field)
			if !found {
				return fmt.Errorf("kitdb: reference target %s.%s is unavailable", t.definition.Name, field.Reference.Field)
			}
			child := &SchemaTable{
				tenant: t.tenant, scope: t.scope, engine: "kitdb", dbName: t.dbName,
				table: childName, columns: childDefinition.columns,
				definition: childDefinition, definitions: t.definitions,
			}
			for index := range before {
				oldValue, oldFound := before[index].values[targetField.Name]
				newValue, newFound := after[index].values[targetField.Name]
				if !oldFound {
					oldValue = value.NewNil()
				}
				if !newFound {
					newValue = value.NewNil()
				}
				if kitDBCompareValues(oldValue, newValue) == 0 {
					continue
				}
				plan := query.ExecutionPlan{Conditions: []query.Condition{{
					Column: field.Name, Operator: "=", Value: oldValue, Logic: "AND",
				}}}
				referenced := false
				err := child.scanKitDBRows(plan, func(_ kitDBStoredRow) (bool, error) {
					referenced = true
					return true, nil
				})
				if err != nil {
					return err
				}
				if referenced {
					return fmt.Errorf("kitdb: cannot update %s.%s; %s.%s still references it", t.definition.Name, targetField.Name, childName, field.Name)
				}
			}
		}
	}
	return nil
}
