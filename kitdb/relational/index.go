package relational

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbrecord "github.com/kitwork/engine/kitdb/record"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

type secondaryIndex struct {
	name     string
	id       string
	fields   []kitdbsql.Field
	filter   []kitdbsql.IndexCondition
	implicit bool
}

type indexMemberBinding struct {
	field  kitdbsql.Field
	member kitdbsql.IndexMember
}

type rowAccessKind uint8

const (
	rowAccessScan rowAccessKind = iota
	rowAccessPrimary
	rowAccessUnique
	rowAccessSecondary
)

type rowAccess struct {
	kind           rowAccessKind
	name           string
	lookup         []byte
	options        kitdbengine.RangeOptions
	orderCovered   bool
	equalityPrefix int
	rangeField     string
	schema         kitdbsql.Schema
	rowGeneration  uint64
}

func collectSecondaryIndexes(schema kitdbsql.Schema) ([]secondaryIndex, error) {
	groups := make(map[string][]indexMemberBinding)
	indexes := make([]secondaryIndex, 0)
	fields := append([]kitdbsql.Field(nil), schema.Fields...)
	sort.Slice(fields, func(left, right int) bool { return fields[left].Position < fields[right].Position })
	for _, field := range fields {
		for _, member := range field.Indexes {
			if member.Name == "" {
				name := "idx_" + schema.Name + "_" + field.Name
				identity := member.ID
				if identity == "" {
					identity = kitdbsql.StableSchemaID("index", schema.ID+":"+name+":"+field.Name)
				}
				indexes = append(indexes, secondaryIndex{
					name: name, id: identity, fields: []kitdbsql.Field{field},
					filter: append([]kitdbsql.IndexCondition(nil), member.Filter...),
				})
				continue
			}
			groups[member.Name] = append(groups[member.Name], indexMemberBinding{field: field, member: member})
		}
	}
	for name, members := range groups {
		allOrdered := true
		for _, member := range members {
			if member.member.Order == 0 {
				allOrdered = false
				break
			}
		}
		sort.SliceStable(members, func(left, right int) bool {
			if allOrdered {
				return members[left].member.Order < members[right].member.Order
			}
			return members[left].field.Position < members[right].field.Position
		})
		index := secondaryIndex{name: name}
		for _, member := range members {
			if member.member.ID != "" {
				if index.id != "" && index.id != member.member.ID {
					return nil, fmt.Errorf("kitdb: index %q members disagree on identity", name)
				}
				index.id = member.member.ID
			}
			if len(member.member.Filter) != 0 {
				if len(index.filter) != 0 && !equalIndexFilters(index.filter, member.member.Filter) {
					return nil, fmt.Errorf("kitdb: index %q members disagree on filter", name)
				}
				index.filter = append([]kitdbsql.IndexCondition(nil), member.member.Filter...)
			}
			index.fields = append(index.fields, member.field)
		}
		if index.id == "" {
			columns := make([]string, len(index.fields))
			for position, field := range index.fields {
				columns[position] = field.Name
			}
			index.id = kitdbsql.StableSchemaID("index", schema.ID+":"+name+":"+strings.Join(columns, ","))
		}
		indexes = append(indexes, index)
	}
	if implicit, needed := implicitPrimaryIndex(schema, indexes); needed {
		indexes = append(indexes, implicit)
	}
	sort.Slice(indexes, func(left, right int) bool { return indexes[left].name < indexes[right].name })
	return indexes, nil
}

func implicitPrimaryIndex(schema kitdbsql.Schema, indexes []secondaryIndex) (secondaryIndex, bool) {
	primary := schema.PrimaryFields()
	if len(primary) == 0 {
		return secondaryIndex{}, false
	}
	for _, field := range primary {
		if !sortableIndexKind(field.Kind) {
			return secondaryIndex{}, false
		}
	}
	for _, index := range indexes {
		if len(index.filter) != 0 || len(index.fields) < len(primary) {
			continue
		}
		matched := true
		for position := range primary {
			if index.fields[position].ID != primary[position].ID {
				matched = false
				break
			}
		}
		if matched {
			return secondaryIndex{}, false
		}
	}
	identities := make([]string, len(primary))
	for position, field := range primary {
		identities[position] = field.ID
	}
	identity := schema.ID + ":implicit-primary-order"
	if len(primary) > 1 {
		identity += ":" + strings.Join(identities, ",")
	}
	return secondaryIndex{
		name: schema.Name + "_pkey", id: kitdbsql.StableSchemaID("index", identity),
		fields: primary, implicit: true,
	}, true
}

func equalIndexFilters(left, right []kitdbsql.IndexCondition) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return bytes.Equal(leftJSON, rightJSON)
}

func secondaryIndexEntries(
	schema kitdbsql.Schema,
	row map[string]any,
	logicalRowKey []byte,
) ([]indexEntry, error) {
	indexes, err := collectSecondaryIndexes(schema)
	if err != nil {
		return nil, err
	}
	entries := make([]indexEntry, 0, len(indexes))
	for _, index := range indexes {
		entry, applicable, err := secondaryIndexEntry(schema, index, row, logicalRowKey)
		if err != nil {
			return nil, err
		}
		if !applicable {
			continue
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(left, right int) bool { return bytes.Compare(entries[left].key, entries[right].key) < 0 })
	return entries, nil
}

func secondaryIndexEntry(
	schema kitdbsql.Schema,
	index secondaryIndex,
	row map[string]any,
	logicalRowKey []byte,
) (indexEntry, bool, error) {
	return secondaryIndexEntryForGeneration(schema, index, row, logicalRowKey, 0)
}

func secondaryIndexEntryForGeneration(
	schema kitdbsql.Schema,
	index secondaryIndex,
	row map[string]any,
	logicalRowKey []byte,
	indexGeneration uint64,
) (indexEntry, bool, error) {
	matches, err := partialIndexMatches(schema, row, index.filter)
	if err != nil || !matches {
		return indexEntry{}, false, err
	}
	key, err := secondaryIndexRowPrefixForGeneration(schema, index, row, indexGeneration)
	if err != nil {
		return indexEntry{}, false, err
	}
	for _, primary := range schema.PrimaryFields() {
		component, err := orderedFieldScalarComponent(primary, row[primary.Name])
		if err != nil {
			return indexEntry{}, false, fmt.Errorf(
				"kitdb: index %q primary field %q: %w", index.name, primary.Name, err,
			)
		}
		key = append(key, component...)
	}
	return indexEntry{key: key, value: bytes.Clone(logicalRowKey)}, true, nil
}

func secondaryIndexBasePrefix(schema kitdbsql.Schema, index secondaryIndex) ([]byte, error) {
	return secondaryIndexBasePrefixForGeneration(schema, index, 0)
}

func secondaryIndexRowPrefix(
	schema kitdbsql.Schema,
	index secondaryIndex,
	row map[string]any,
) ([]byte, error) {
	return secondaryIndexRowPrefixForGeneration(schema, index, row, 0)
}

func secondaryIndexRowPrefixForGeneration(
	schema kitdbsql.Schema,
	index secondaryIndex,
	row map[string]any,
	indexGeneration uint64,
) ([]byte, error) {
	prefix, err := secondaryIndexBasePrefixForGeneration(schema, index, indexGeneration)
	if err != nil {
		return nil, err
	}
	for _, field := range index.fields {
		component, err := orderedFieldScalarComponent(field, row[field.Name])
		if err != nil {
			return nil, fmt.Errorf("kitdb: index %q field %q: %w", index.name, field.Name, err)
		}
		prefix = append(prefix, component...)
	}
	return prefix, nil
}

func partialIndexMatches(
	schema kitdbsql.Schema,
	row map[string]any,
	filters []kitdbsql.IndexCondition,
) (bool, error) {
	for _, filter := range filters {
		canonical, field, found := schema.FieldByName(filter.Field)
		if !found {
			return false, fmt.Errorf("kitdb: index filter references missing field %q", filter.Field)
		}
		decoder := json.NewDecoder(bytes.NewReader(filter.Value))
		decoder.UseNumber()
		var expected any
		if err := decoder.Decode(&expected); err != nil {
			return false, fmt.Errorf("kitdb: index filter for %q is invalid", canonical)
		}
		expected = normalizeJSONNumber(expected)
		if expected != nil {
			var err error
			expected, err = coerceField(field, expected)
			if err != nil {
				return false, err
			}
		}
		if compareValues(row[canonical], expected) != 0 {
			return false, nil
		}
	}
	return true, nil
}

func orderedScalarComponent(item any) ([]byte, error) {
	scalar, err := recordScalar(item)
	if err != nil {
		return nil, fmt.Errorf("value %T is not an indexable scalar", item)
	}
	return kitdbrecord.OrderedScalarComponent(scalar)
}

func orderedFieldScalarComponent(field kitdbsql.Field, item any) ([]byte, error) {
	scalar, err := fieldScalar(field, item)
	if err != nil {
		return nil, err
	}
	return kitdbrecord.OrderedScalarComponent(scalar)
}

func sortableIndexKind(kind string) bool {
	switch kind {
	case "text", "varchar", "char", "kitid", "uuid", "date", "time", "timestamp", "timestamptz", "ip", "mac", "enum",
		"integer", "smallint", "int32", "bigint", "float", "serial", "year", "month", "day", "bool":
		return true
	default:
		return false
	}
}

func indexReadyKey(schema kitdbsql.Schema, index secondaryIndex) ([]byte, error) {
	identity := kitdbsql.StableSchemaID("physical", schema.ID+":standalone-index-ready:"+index.id)
	return fixedKey(kitdbrecord.PhysicalNamespace, schema.ID, identity)
}

func indexReadyValue(schema kitdbsql.Schema) []byte {
	return []byte("KIR1:" + schema.Hash)
}

func markEmptyTableIndexesReady(transaction *kitdbengine.Tx, schema kitdbsql.Schema) error {
	indexes, err := collectSecondaryIndexes(schema)
	if err != nil {
		return err
	}
	for _, index := range indexes {
		key, err := indexReadyKey(schema, index)
		if err != nil {
			return err
		}
		if err := transaction.Put(key, indexReadyValue(schema)); err != nil {
			return err
		}
	}
	return nil
}

func indexIsReady(reader recordReader, schema kitdbsql.Schema, index secondaryIndex) (bool, error) {
	_, ready, err := secondaryIndexPhysicalLayout(reader, schema, index)
	return ready, err
}

func prefixEnd(prefix []byte) []byte {
	return kitdbrecord.PrefixEnd(prefix)
}

func (transaction *Transaction) planRowAccess(
	schema kitdbsql.Schema,
	generation uint64,
	conditions []boundCondition,
	orders []boundOrder,
) (rowAccess, error) {
	equality := make(map[string]any)
	for _, condition := range conditions {
		if condition.operator == "=" && condition.value != nil {
			equality[condition.field.Name] = condition.value
		}
	}

	primary := schema.PrimaryFields()
	primaryValues := make(map[string]any, len(primary))
	primaryReady := len(primary) != 0
	for _, field := range primary {
		item, found := equality[field.Name]
		if !found {
			primaryReady = false
			break
		}
		primaryValues[field.Name] = item
	}
	if primaryReady {
		key, err := rowKey(schema, primaryValues, generation)
		return rowAccess{
			kind: rowAccessPrimary, name: "PRIMARY", lookup: key,
			orderCovered: len(orders) != 0,
		}, err
	}

	for _, field := range schema.Fields {
		item, found := equality[field.Name]
		if field.Primary || !field.Unique || !found {
			continue
		}
		key, applicable, err := uniqueKey(schema, field.ID, []any{item})
		if err != nil {
			return rowAccess{}, err
		}
		if applicable {
			return rowAccess{
				kind: rowAccessUnique, name: "unique_" + schema.Name + "_" + field.Name,
				lookup: key, orderCovered: len(orders) != 0,
				schema: schema, rowGeneration: generation,
			}, nil
		}
	}
	for _, constraint := range schema.UniqueConstraints {
		values := make([]any, len(constraint.Fields))
		ready := len(values) != 0
		for position, tag := range constraint.Fields {
			field, found := fieldByTag(schema, tag)
			if !found {
				return rowAccess{}, fmt.Errorf("kitdb: unique constraint %q references missing field", constraint.Name)
			}
			item, found := equality[field.Name]
			if !found {
				ready = false
				break
			}
			values[position] = item
		}
		if !ready {
			continue
		}
		key, applicable, err := uniqueKey(schema, constraint.ID, values)
		if err != nil {
			return rowAccess{}, err
		}
		if applicable {
			return rowAccess{
				kind: rowAccessUnique, name: constraint.Name, lookup: key,
				orderCovered: len(orders) != 0,
				schema:       schema, rowGeneration: generation,
			}, nil
		}
	}

	indexes, err := collectSecondaryIndexes(schema)
	if err != nil {
		return rowAccess{}, err
	}
	bestScore := -1
	var best rowAccess
	for _, index := range indexes {
		if len(index.filter) != 0 {
			continue
		}
		indexGeneration, ready, err := secondaryIndexPhysicalLayout(transaction, schema, index)
		if err != nil {
			return rowAccess{}, err
		}
		if !ready {
			continue
		}
		prefix, err := secondaryIndexBasePrefixForGeneration(schema, index, indexGeneration)
		if err != nil {
			return rowAccess{}, err
		}
		equalityPrefix := 0
		for _, field := range index.fields {
			item, found := equality[field.Name]
			if !found {
				break
			}
			component, err := orderedFieldScalarComponent(field, item)
			if err != nil {
				return rowAccess{}, err
			}
			prefix = append(prefix, component...)
			equalityPrefix++
		}
		orderCovered, reverse := indexCoversOrder(index, equalityPrefix, equality, orders)
		options := kitdbengine.RangeOptions{Prefix: bytes.Clone(prefix), Reverse: reverse}
		rangeField := ""
		if equalityPrefix < len(index.fields) {
			field := index.fields[equalityPrefix]
			bounded, err := boundSecondaryIndexRange(&options, field, conditions)
			if err != nil {
				return rowAccess{}, err
			}
			if bounded {
				rangeField = field.Name
			}
		}
		if equalityPrefix == 0 && rangeField == "" && !orderCovered {
			continue
		}
		score := equalityPrefix*100 + 5
		if rangeField != "" {
			score += 30
		}
		if orderCovered {
			score += 20
		}
		if score <= bestScore {
			continue
		}
		bestScore = score
		best = rowAccess{
			kind: rowAccessSecondary, name: index.name,
			options:        options,
			orderCovered:   orderCovered,
			equalityPrefix: equalityPrefix, rangeField: rangeField,
			schema: schema, rowGeneration: generation,
		}
	}
	if bestScore >= 0 {
		return best, nil
	}
	prefix, err := rowPrefix(schema, generation)
	return rowAccess{kind: rowAccessScan, name: "row_scan", options: kitdbengine.RangeOptions{Prefix: prefix}}, err
}

// Only the first non-equality component is contiguous in composite index order.
// Keep every predicate as a residual check; these bounds only reduce candidates.
func boundSecondaryIndexRange(options *kitdbengine.RangeOptions, field kitdbsql.Field, conditions []boundCondition) (bool, error) {
	if !sortableIndexKind(field.Kind) {
		return false, nil
	}
	bounded := false
	for _, condition := range conditions {
		if condition.field.ID != field.ID || condition.value == nil {
			continue
		}
		switch condition.operator {
		case ">", ">=", "<", "<=":
		default:
			continue
		}
		component, err := orderedFieldScalarComponent(field, condition.value)
		if err != nil {
			return false, err
		}
		bound := append(bytes.Clone(options.Prefix), component...)
		switch condition.operator {
		case ">", ">=":
			// Advance past the entire equal-value tuple, including every row suffix.
			if condition.operator == ">" {
				bound = prefixEnd(bound)
			}
			if len(options.Start) == 0 || bytes.Compare(bound, options.Start) > 0 {
				options.Start = bound
			}
		case "<", "<=":
			if condition.operator == "<=" {
				bound = prefixEnd(bound)
			}
			if len(options.End) == 0 || bytes.Compare(bound, options.End) < 0 {
				options.End = bound
			}
		}
		bounded = true
	}
	if bounded && len(options.Start) == 0 {
		// SQL inequalities reject NULL, which sorts before every non-NULL scalar.
		null, err := orderedScalarComponent(nil)
		if err != nil {
			return false, err
		}
		options.Start = prefixEnd(append(bytes.Clone(options.Prefix), null...))
	}
	return bounded, nil
}

func indexCoversOrder(
	index secondaryIndex,
	equalityPrefix int,
	equality map[string]any,
	orders []boundOrder,
) (bool, bool) {
	if len(orders) == 0 {
		return false, false
	}
	remaining := make([]boundOrder, 0, len(orders))
	for _, order := range orders {
		if _, fixed := equality[order.field.Name]; fixed {
			continue
		}
		remaining = append(remaining, order)
	}
	if len(remaining) == 0 {
		return equalityPrefix == len(index.fields), false
	}
	position := equalityPrefix
	descending := remaining[0].descending
	for _, order := range remaining {
		if order.descending != descending {
			return false, false
		}
		for position < len(index.fields) {
			if _, fixed := equality[index.fields[position].Name]; !fixed {
				break
			}
			position++
		}
		if position >= len(index.fields) || index.fields[position].ID != order.field.ID {
			return false, false
		}
		position++
	}
	return true, descending
}

// Keep ordinary reads free of per-row measurement branches.
func (transaction *Transaction) walkAccessRows(
	access rowAccess,
	visit func(key, encoded []byte) (bool, error),
) error {
	return transaction.walkAccessRowsAdmitted(access, nil, visit)
}

// Admission accounts index entries as well as hydrated rows. Ordinary reads
// keep the nil fast path; bounded integrity probes must not hide index work.
func (transaction *Transaction) walkAccessRowsAdmitted(
	access rowAccess,
	admit func(key, value []byte) error,
	visit func(key, encoded []byte) (bool, error),
) error {
	get := func(key []byte) ([]byte, bool, error) {
		if admit != nil {
			if err := admit(key, nil); err != nil {
				return nil, false, err
			}
		}
		value, found, err := transaction.Get(key)
		if err == nil && admit != nil {
			err = admit(nil, value)
		}
		return value, found, err
	}
	resolveRowKey := func(logical []byte) ([]byte, error) {
		if access.rowGeneration == 0 {
			return logical, nil
		}
		return physicalRowKey(access.schema, logical, access.rowGeneration)
	}
	switch access.kind {
	case rowAccessPrimary:
		encoded, found, err := get(access.lookup)
		if err != nil || !found {
			return err
		}
		_, err = visit(access.lookup, encoded)
		return err
	case rowAccessUnique:
		rowKey, found, err := get(access.lookup)
		if err != nil || !found {
			return err
		}
		rowKey, err = resolveRowKey(rowKey)
		if err != nil {
			return err
		}
		encoded, found, err := get(rowKey)
		if err != nil || !found {
			return err
		}
		_, err = visit(rowKey, encoded)
		return err
	case rowAccessSecondary:
		return transaction.Scan(access.options, func(indexKey, logicalRowKey []byte) (bool, error) {
			if admit != nil {
				if err := admit(indexKey, logicalRowKey); err != nil {
					return false, err
				}
			}
			rowKey, err := resolveRowKey(logicalRowKey)
			if err != nil {
				return false, err
			}
			encoded, found, err := get(rowKey)
			if err != nil || !found {
				return false, err
			}
			return visit(rowKey, encoded)
		})
	default:
		if admit != nil {
			return transaction.Scan(access.options, func(key, value []byte) (bool, error) {
				if err := admit(key, value); err != nil {
					return false, err
				}
				return visit(key, value)
			})
		}
		return transaction.Scan(access.options, visit)
	}
}

func rowAccessExecutionStats(access rowAccess, observe bool) *ExecutionStats {
	if !observe {
		return nil
	}
	path := "sequential-scan"
	switch access.kind {
	case rowAccessPrimary:
		path = "primary-lookup"
	case rowAccessUnique:
		path = "unique-lookup"
	case rowAccessSecondary:
		path = "index-scan"
	}
	return &ExecutionStats{Path: path}
}

func (transaction *Transaction) walkAccessRowsObserved(
	access rowAccess,
	stats *ExecutionStats,
	visit func(key, encoded []byte) (bool, error),
) error {
	if stats == nil {
		return transaction.walkAccessRows(access, visit)
	}
	cursorStats := kitdbengine.CursorStats{}
	defer func() {
		stats.addCursorStats(cursorStats)
	}()
	visitRow := func(key, encoded []byte) (bool, error) {
		stats.RowsScanned++
		return visit(key, encoded)
	}
	pointLookup := func(key []byte) ([]byte, bool, error) {
		stats.PointLookups++
		return transaction.getWithStats(key, &cursorStats)
	}
	resolveRowKey := func(logical []byte) ([]byte, error) {
		if access.rowGeneration == 0 {
			return logical, nil
		}
		return physicalRowKey(access.schema, logical, access.rowGeneration)
	}
	switch access.kind {
	case rowAccessPrimary:
		encoded, found, err := pointLookup(access.lookup)
		if err != nil || !found {
			return err
		}
		_, err = visitRow(access.lookup, encoded)
		return err
	case rowAccessUnique:
		rowKey, found, err := pointLookup(access.lookup)
		if err != nil || !found {
			return err
		}
		stats.IndexEntriesScanned++
		rowKey, err = resolveRowKey(rowKey)
		if err != nil {
			return err
		}
		encoded, found, err := pointLookup(rowKey)
		if err != nil || !found {
			return err
		}
		_, err = visitRow(rowKey, encoded)
		return err
	case rowAccessSecondary:
		return transaction.scan(access.options, &cursorStats, func(_, logicalRowKey []byte) (bool, error) {
			stats.IndexEntriesScanned++
			rowKey, err := resolveRowKey(logicalRowKey)
			if err != nil {
				return false, err
			}
			encoded, found, err := pointLookup(rowKey)
			if err != nil || !found {
				return false, err
			}
			return visitRow(rowKey, encoded)
		})
	default:
		return transaction.scan(access.options, &cursorStats, visitRow)
	}
}
