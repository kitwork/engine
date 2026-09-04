package work

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/kitwork/engine/value"
)

type kitDBPostgresIndexDescriptor struct {
	table   *StructDef
	name    string
	columns []StructFieldDef
	filter  []StructIndexCondition
	unique  bool
	primary bool
}

type kitDBPostgresConstraintDescriptor struct {
	table         *StructDef
	target        *StructDef
	id            string
	name          string
	typeName      string
	kind          string
	columns       []StructFieldDef
	targetColumns []StructFieldDef
	onDelete      string
	onUpdate      string
	check         string
}

func (session *kitDBPostgresSession) kitDBPostgresIndexRecords(
	definitions map[string]*StructDef,
) []kitDBPostgresCatalogRecord {
	indexes := kitDBPostgresIndexes(definitions)
	records := make([]kitDBPostgresCatalogRecord, 0, len(indexes))
	for _, index := range indexes {
		tableOID := kitDBPostgresCatalogOID(session.databaseName, index.table.ID, index.table.Name)
		indexOID := kitDBPostgresIndexOID(session.databaseName, index.table, index.name)
		columns := kitDBPostgresFieldNames(index.columns)
		positions := make([]string, len(index.columns))
		for position, field := range index.columns {
			positions[position] = strconv.Itoa(field.Position + 1)
		}
		condition := kitDBPostgresIndexCondition(index.filter)
		definition := kitDBPostgresIndexDefinition(index, condition)
		records = append(records, kitDBPostgresCatalogRecord{
			"oid": value.New(indexOID), "indexrelid": value.New(indexOID), "indrelid": value.New(tableOID),
			"table_oid": value.New(tableOID), "relnamespace": value.New(2200), "relowner": value.New(10),
			"relname": value.New(index.name), "relkind": value.New("i"), "relpersistence": value.New("p"),
			"relam": value.New(403), "relnatts": value.New(len(columns)),
			"indnatts": value.New(len(columns)), "indnkeyatts": value.New(len(columns)),
			"indisunique": value.New(index.unique), "indnullsnotdistinct": value.New(false),
			"indisprimary": value.New(index.primary), "indisexclusion": value.New(false),
			"indimmediate": value.New(true), "indisclustered": value.New(false),
			"indisvalid": value.New(true), "indcheckxmin": value.New(false),
			"indisready": value.New(true), "indislive": value.New(true), "indisreplident": value.New(index.primary),
			"indkey": value.New(strings.Join(positions, " ")), "indcollation": value.NewNil(),
			"indclass": value.NewNil(), "indoption": value.NewNil(), "indexprs": value.NewNil(),
			"indpred":    kitDBPostgresOptionalText(condition),
			"schemaname": value.New("public"), "tablename": value.New(index.table.Name),
			"table_name": value.New(index.table.Name), "indexname": value.New(index.name),
			"index_name": value.New(index.name), "tablespace": value.NewNil(),
			"indexdef": value.New(definition), "index_definition": value.New(definition),
			"amname": value.New("btree"), "index_algorithm": value.New("BTREE"),
			"is_unique": value.New(index.unique), "is_primary": value.New(index.primary),
			"column_name": value.New(strings.Join(columns, ",")), "condition": value.New(condition),
			"comment": value.NewNil(), "description": value.NewNil(),
		})
	}
	return records
}

func (session *kitDBPostgresSession) kitDBPostgresConstraintRecords(
	definitions map[string]*StructDef,
	perColumn bool,
) []kitDBPostgresCatalogRecord {
	constraints := kitDBPostgresConstraints(definitions)
	records := make([]kitDBPostgresCatalogRecord, 0, len(constraints))
	for _, constraint := range constraints {
		base := session.kitDBPostgresConstraintRecord(constraint)
		if !perColumn {
			records = append(records, base)
			continue
		}
		for position, field := range constraint.columns {
			record := cloneKitDBPostgresCatalogRecord(base)
			record["column_name"] = value.New(field.Name)
			record["ordinal_position"] = value.New(position + 1)
			if position < len(constraint.targetColumns) {
				record["position_in_unique_constraint"] = value.New(position + 1)
			} else {
				record["position_in_unique_constraint"] = value.NewNil()
			}
			records = append(records, record)
		}
	}
	return records
}

func (session *kitDBPostgresSession) kitDBPostgresConstraintRecord(
	constraint kitDBPostgresConstraintDescriptor,
) kitDBPostgresCatalogRecord {
	tableOID := kitDBPostgresCatalogOID(session.databaseName, constraint.table.ID, constraint.table.Name)
	constraintOID := kitDBPostgresCatalogOID(session.databaseName, "constraint", constraint.table.ID, constraint.id)
	indexOID := int64(0)
	if constraint.kind == "p" || constraint.kind == "u" {
		indexOID = kitDBPostgresIndexOID(session.databaseName, constraint.table, constraint.name)
	}
	targetOID := int64(0)
	parentName := value.NewNil()
	parentColumns := value.NewNil()
	parentSchema := value.NewNil()
	if constraint.target != nil {
		targetOID = kitDBPostgresCatalogOID(session.databaseName, constraint.target.ID, constraint.target.Name)
		parentName = value.New(constraint.target.Name)
		parentColumns = value.New(strings.Join(kitDBPostgresFieldNames(constraint.targetColumns), ","))
		parentSchema = value.New("public")
	}
	onUpdate := strings.ToLower(fkAction(constraint.onUpdate))
	onDelete := strings.ToLower(fkAction(constraint.onDelete))
	return kitDBPostgresCatalogRecord{
		"oid": value.New(constraintOID), "conname": value.New(constraint.name),
		"connamespace": value.New(2200), "contype": value.New(constraint.kind),
		"condeferrable": value.New(false), "condeferred": value.New(false), "convalidated": value.New(true),
		"conrelid": value.New(tableOID), "table_oid": value.New(tableOID), "contypid": value.New(0),
		"conindid": value.New(indexOID), "conparentid": value.New(0), "confrelid": value.New(targetOID),
		"confupdtype":   value.New(kitDBPostgresForeignActionCode(constraint.onUpdate)),
		"confdeltype":   value.New(kitDBPostgresForeignActionCode(constraint.onDelete)),
		"confmatchtype": value.New("s"), "conislocal": value.New(true), "coninhcount": value.New(0),
		"connoinherit": value.New(true), "conkey": value.New(kitDBPostgresAttributeNumbers(constraint.columns)),
		"confkey":            value.New(kitDBPostgresAttributeNumbers(constraint.targetColumns)),
		"conbin":             kitDBPostgresOptionalText(constraint.check),
		"constraint_catalog": value.New(session.databaseName), "constraint_schema": value.New("public"),
		"constraint_name": value.New(constraint.name), "constraint_type": value.New(constraint.typeName),
		"table_catalog": value.New(session.databaseName), "table_schema": value.New("public"),
		"table_name": value.New(constraint.table.Name), "is_deferrable": value.New("NO"),
		"initially_deferred": value.New("NO"), "enforced": value.New("YES"), "nulls_distinct": value.New("YES"),
		"child_schema": value.New("public"), "child_name": value.New(constraint.table.Name),
		"child_column":  value.New(strings.Join(kitDBPostgresFieldNames(constraint.columns), ",")),
		"parent_schema": parentSchema, "parent_name": parentName, "parent_column": parentColumns,
		"on_update": value.New(onUpdate), "on_delete": value.New(onDelete),
		"check_clause": kitDBPostgresOptionalText(constraint.check),
	}
}

func kitDBPostgresIndexes(definitions map[string]*StructDef) []kitDBPostgresIndexDescriptor {
	indexes := make([]kitDBPostgresIndexDescriptor, 0)
	for _, definition := range kitDBPostgresSortedDefinitions(definitions) {
		if primary := definition.primaryFields(); len(primary) != 0 {
			indexes = append(indexes, kitDBPostgresIndexDescriptor{
				table: definition, name: definition.Name + "_pkey", columns: primary,
				unique: true, primary: true,
			})
		}
		for _, field := range definition.Fields {
			if field.Unique && !field.Primary {
				indexes = append(indexes, kitDBPostgresIndexDescriptor{
					table: definition, name: "unique_" + definition.Name + "_" + field.Name,
					columns: []StructFieldDef{field}, unique: true,
				})
			}
		}
		for _, unique := range definition.UniqueConstraints {
			if fields, ok := kitDBPostgresFieldsByTags(definition, unique.Fields); ok {
				indexes = append(indexes, kitDBPostgresIndexDescriptor{
					table: definition, name: unique.Name, columns: fields, unique: true,
				})
			}
		}
		indexes = append(indexes, kitDBPostgresOrdinaryIndexes(definition)...)
	}
	sort.SliceStable(indexes, func(left, right int) bool {
		if indexes[left].table.Name != indexes[right].table.Name {
			return indexes[left].table.Name < indexes[right].table.Name
		}
		return indexes[left].name < indexes[right].name
	})
	return indexes
}

func kitDBPostgresOrdinaryIndexes(definition *StructDef) []kitDBPostgresIndexDescriptor {
	type member struct {
		field StructFieldDef
		index StructIndexMember
	}
	groups := make(map[string][]member)
	indexes := make([]kitDBPostgresIndexDescriptor, 0)
	for _, field := range definition.Fields {
		for _, index := range field.Indexes {
			if index.Name == "" {
				indexes = append(indexes, kitDBPostgresIndexDescriptor{
					table: definition, name: "idx_" + definition.Name + "_" + field.Name,
					columns: []StructFieldDef{field}, filter: append([]StructIndexCondition(nil), index.Filter...),
				})
				continue
			}
			groups[index.Name] = append(groups[index.Name], member{field: field, index: index})
		}
	}
	for name, members := range groups {
		allPositioned := true
		for _, item := range members {
			if item.index.Order == 0 {
				allPositioned = false
				break
			}
		}
		sort.SliceStable(members, func(left, right int) bool {
			if allPositioned {
				return members[left].index.Order < members[right].index.Order
			}
			return members[left].field.Position < members[right].field.Position
		})
		index := kitDBPostgresIndexDescriptor{table: definition, name: name}
		for _, item := range members {
			index.columns = append(index.columns, item.field)
			if len(index.filter) == 0 && len(item.index.Filter) != 0 {
				index.filter = append([]StructIndexCondition(nil), item.index.Filter...)
			}
		}
		indexes = append(indexes, index)
	}
	return indexes
}

func kitDBPostgresConstraints(definitions map[string]*StructDef) []kitDBPostgresConstraintDescriptor {
	constraints := make([]kitDBPostgresConstraintDescriptor, 0)
	for _, definition := range kitDBPostgresSortedDefinitions(definitions) {
		if primary := definition.primaryFields(); len(primary) != 0 {
			constraints = append(constraints, kitDBPostgresConstraintDescriptor{
				table: definition, id: "primary", name: definition.Name + "_pkey",
				typeName: "PRIMARY KEY", kind: "p", columns: primary,
			})
		}
		for _, field := range definition.Fields {
			if field.Unique && !field.Primary {
				name := "unique_" + definition.Name + "_" + field.Name
				constraints = append(constraints, kitDBPostgresConstraintDescriptor{
					table: definition, id: "unique:" + field.ID, name: name,
					typeName: "UNIQUE", kind: "u", columns: []StructFieldDef{field},
				})
			}
			if field.Reference != nil {
				target := kitDBPostgresDefinitionByName(definitions, field.Reference.Struct)
				targetField, found := kitDBPostgresFieldByName(target, field.Reference.Field)
				if target != nil && found {
					constraints = append(constraints, kitDBPostgresConstraintDescriptor{
						table: definition, target: target, id: "foreign:" + field.ID,
						name: definition.Name + "_" + field.Name + "_fkey", typeName: "FOREIGN KEY", kind: "f",
						columns: []StructFieldDef{field}, targetColumns: []StructFieldDef{targetField},
						onDelete: field.Reference.OnDelete, onUpdate: field.Reference.OnUpdate,
					})
				}
			}
		}
		for _, unique := range definition.UniqueConstraints {
			if fields, ok := kitDBPostgresFieldsByTags(definition, unique.Fields); ok {
				constraints = append(constraints, kitDBPostgresConstraintDescriptor{
					table: definition, id: unique.ID, name: unique.Name,
					typeName: "UNIQUE", kind: "u", columns: fields,
				})
			}
		}
		for _, foreign := range definition.ForeignConstraints {
			target := structDefinitionByIdentity(definitions, foreign.TargetStructID, foreign.TargetStruct)
			localFields, localOK := kitDBPostgresFieldsByTags(definition, foreign.Fields)
			targetFields, targetOK := kitDBPostgresFieldsByIDs(target, foreign.TargetFields)
			if target == nil || !localOK || !targetOK {
				continue
			}
			constraints = append(constraints, kitDBPostgresConstraintDescriptor{
				table: definition, target: target, id: foreign.ID, name: foreign.Name,
				typeName: "FOREIGN KEY", kind: "f", columns: localFields, targetColumns: targetFields,
				onDelete: foreign.OnDelete, onUpdate: foreign.OnUpdate,
			})
		}
		for _, check := range definition.CheckConstraints {
			expression, err := renderStructCheckExpression(definition, check.Expression)
			if err != nil {
				continue
			}
			constraints = append(constraints, kitDBPostgresConstraintDescriptor{
				table: definition, id: check.ID, name: check.Name,
				typeName: "CHECK", kind: "c", check: expression,
			})
		}
	}
	sort.SliceStable(constraints, func(left, right int) bool {
		if constraints[left].table.Name != constraints[right].table.Name {
			return constraints[left].table.Name < constraints[right].table.Name
		}
		if constraints[left].kind != constraints[right].kind {
			return constraints[left].kind < constraints[right].kind
		}
		return constraints[left].name < constraints[right].name
	})
	return constraints
}

func kitDBPostgresSortedDefinitions(definitions map[string]*StructDef) []*StructDef {
	names := make([]string, 0, len(definitions))
	for name := range definitions {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]*StructDef, 0, len(names))
	for _, name := range names {
		if definitions[name] != nil {
			result = append(result, definitions[name])
		}
	}
	return result
}

func kitDBPostgresFieldsByTags(definition *StructDef, tags []uint32) ([]StructFieldDef, bool) {
	fields := make([]StructFieldDef, len(tags))
	for position, tag := range tags {
		found := false
		for _, field := range definition.Fields {
			if field.Tag == tag {
				fields[position], found = field, true
				break
			}
		}
		if !found {
			return nil, false
		}
	}
	return fields, true
}

func kitDBPostgresFieldsByIDs(definition *StructDef, ids []string) ([]StructFieldDef, bool) {
	if definition == nil {
		return nil, false
	}
	fields := make([]StructFieldDef, len(ids))
	for position, id := range ids {
		field, found := structFieldByID(definition, id)
		if !found {
			return nil, false
		}
		fields[position] = field
	}
	return fields, true
}

func kitDBPostgresDefinitionByName(definitions map[string]*StructDef, name string) *StructDef {
	if definition := definitions[name]; definition != nil {
		return definition
	}
	for candidate, definition := range definitions {
		if strings.EqualFold(candidate, name) || definition != nil && strings.EqualFold(definition.Name, name) {
			return definition
		}
	}
	return nil
}

func kitDBPostgresFieldByName(definition *StructDef, name string) (StructFieldDef, bool) {
	if definition != nil {
		for _, field := range definition.Fields {
			if strings.EqualFold(field.Name, name) {
				return field, true
			}
		}
	}
	return StructFieldDef{}, false
}

func kitDBPostgresFieldNames(fields []StructFieldDef) []string {
	names := make([]string, len(fields))
	for index, field := range fields {
		names[index] = field.Name
	}
	return names
}

func kitDBPostgresIndexOID(databaseName string, table *StructDef, indexName string) int64 {
	return kitDBPostgresCatalogOID(databaseName, "index", table.ID, strings.ToLower(indexName))
}

func kitDBPostgresIndexDefinition(index kitDBPostgresIndexDescriptor, condition string) string {
	columns := make([]string, len(index.columns))
	for position, field := range index.columns {
		columns[position] = kitDBPostgresQuoteIdentifier(field.Name)
	}
	modifier := ""
	if index.unique {
		modifier = "UNIQUE "
	}
	definition := fmt.Sprintf(
		"CREATE %sINDEX %s ON %s.%s USING btree (%s)",
		modifier,
		kitDBPostgresQuoteIdentifier(index.name),
		kitDBPostgresQuoteIdentifier("public"),
		kitDBPostgresQuoteIdentifier(index.table.Name),
		strings.Join(columns, ", "),
	)
	if condition != "" {
		definition += " WHERE " + condition
	}
	return definition
}

func kitDBPostgresIndexCondition(filter []StructIndexCondition) string {
	if len(filter) == 0 {
		return ""
	}
	conditions := append([]StructIndexCondition(nil), filter...)
	sort.Slice(conditions, func(left, right int) bool {
		return conditions[left].Field < conditions[right].Field
	})
	parts := make([]string, 0, len(conditions))
	for _, condition := range conditions {
		field := kitDBPostgresQuoteIdentifier(condition.Field)
		if condition.Value.IsNil() {
			parts = append(parts, field+" IS NULL")
		} else {
			parts = append(parts, field+" = "+sqlLiteral(condition.Value))
		}
	}
	return strings.Join(parts, " AND ")
}

func kitDBPostgresQuoteIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func kitDBPostgresAttributeNumbers(fields []StructFieldDef) string {
	numbers := make([]string, len(fields))
	for index, field := range fields {
		numbers[index] = strconv.Itoa(field.Position + 1)
	}
	return "{" + strings.Join(numbers, ",") + "}"
}

func kitDBPostgresForeignActionCode(action string) string {
	switch normalizeFKActionName(action) {
	case "restrict":
		return "r"
	case "cascade":
		return "c"
	case "setnull":
		return "n"
	case "setdefault":
		return "d"
	default:
		return "a"
	}
}

func kitDBPostgresOptionalText(text string) value.Value {
	if text == "" {
		return value.NewNil()
	}
	return value.New(text)
}

func cloneKitDBPostgresCatalogRecord(source kitDBPostgresCatalogRecord) kitDBPostgresCatalogRecord {
	cloned := make(kitDBPostgresCatalogRecord, len(source))
	for name, item := range source {
		cloned[name] = item
	}
	return cloned
}
