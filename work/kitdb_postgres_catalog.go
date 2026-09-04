package work

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"

	"github.com/kitwork/engine/kitdb/pgwire"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
	"github.com/kitwork/engine/value"
)

type kitDBPostgresCatalogKind uint8

const (
	kitDBPostgresCatalogNone kitDBPostgresCatalogKind = iota
	kitDBPostgresCatalogTables
	kitDBPostgresCatalogColumns
	kitDBPostgresCatalogNamespaces
	kitDBPostgresCatalogDatabase
	kitDBPostgresCatalogTypes
	kitDBPostgresCatalogIndexes
	kitDBPostgresCatalogConstraints
	kitDBPostgresCatalogConstraintColumns
	kitDBPostgresCatalogEmpty
)

type kitDBPostgresCatalogProjection struct {
	expression string
	name       string
	kind       string
}

type kitDBPostgresCatalogRecord map[string]value.Value

// catalogQuery projects KitDB's durable Schema IR as virtual PostgreSQL
// catalogs. No pg_catalog rows are persisted in the database file.
func (session *kitDBPostgresSession) catalogQuery(
	source string,
	parameters []pgwire.Parameter,
) (pgwire.Result, bool, error) {
	return session.catalogQueryWithDatabase(session.database, source, parameters)
}

func (session *kitDBPostgresSession) catalogQueryWithDatabase(
	database *dbProxy,
	source string,
	parameters []pgwire.Parameter,
) (pgwire.Result, bool, error) {
	kind := classifyKitDBPostgresCatalog(source)
	if kind == kitDBPostgresCatalogNone {
		return pgwire.Result{}, false, nil
	}
	projections, err := parseKitDBPostgresCatalogProjections(source, kind)
	if err != nil {
		return pgwire.Result{}, true, pgwire.NewError("42601", err.Error())
	}
	definitions := map[string]*StructDef{}
	if database != nil && kind != kitDBPostgresCatalogDatabase {
		if err := database.refreshKitDBCatalogDefinitions(); err != nil {
			return pgwire.Result{}, true, kitDBPostgresError(err)
		}
		_, definitions = database.schemaSnapshot()
	}
	relationTuples := map[string]value.Value(nil)
	if kind == kitDBPostgresCatalogTables && kitDBPostgresCatalogNeedsRelationTuples(projections) {
		relationTuples, err = loadKitDBPostgresRelationTuples(database, definitions)
		if err != nil {
			return pgwire.Result{}, true, kitDBPostgresError(err)
		}
	}
	records := session.kitDBPostgresCatalogRecords(kind, definitions, relationTuples)
	records = filterKitDBPostgresCatalogRecords(source, parameters, records)
	if kind == kitDBPostgresCatalogDatabase && kitDBPostgresCatalogUsesCurrentDatabase(source) {
		current := strings.ToLower(session.databaseName)
		selected := records[:0]
		for _, record := range records {
			if kitDBPostgresCatalogRecordText(record, "datname") == current {
				selected = append(selected, record)
			}
		}
		records = selected
	}

	if len(projections) == 1 && strings.Contains(strings.ToLower(projections[0].expression), "count(") {
		projections[0].kind = "integer"
		records = []kitDBPostgresCatalogRecord{{
			strings.ToLower(projections[0].name): value.New(len(records)),
		}}
	}
	result := pgwire.Result{
		Columns:    make([]pgwire.Column, len(projections)),
		Rows:       make([][]pgwire.Field, 0, len(records)),
		CommandTag: fmt.Sprintf("SELECT %d", len(records)),
	}
	for index, projection := range projections {
		result.Columns[index] = kitDBPostgresColumn(kitDBRemoteColumn{
			name: projection.name, kind: projection.kind,
		})
	}
	for _, record := range records {
		row := make([]pgwire.Field, len(projections))
		for index, projection := range projections {
			item := kitDBPostgresCatalogValue(record, projection)
			row[index] = kitDBPostgresField(item, projection.kind)
		}
		result.Rows = append(result.Rows, row)
	}
	return result, true, nil
}

func classifyKitDBPostgresCatalog(source string) kitDBPostgresCatalogKind {
	normalized := normalizeKitDBPostgresSQL(source)
	normalized = strings.NewReplacer(`"`, "", "`", "", "[", "", "]", "").Replace(normalized)
	switch {
	case strings.Contains(normalized, "information_schema.key_column_usage"):
		return kitDBPostgresCatalogConstraintColumns
	case strings.Contains(normalized, "information_schema.table_constraints"),
		strings.Contains(normalized, "information_schema.constraint_column_usage"),
		strings.Contains(normalized, "pg_catalog.pg_constraint"),
		strings.Contains(normalized, " from pg_constraint"),
		strings.Contains(normalized, " join pg_constraint"):
		return kitDBPostgresCatalogConstraints
	case strings.Contains(normalized, "pg_catalog.pg_indexes"),
		strings.Contains(normalized, " from pg_indexes"),
		strings.Contains(normalized, "pg_catalog.pg_index"),
		strings.Contains(normalized, " from pg_index"),
		strings.Contains(normalized, " join pg_index"),
		strings.Contains(normalized, "relkind='i'"),
		strings.Contains(normalized, "relkind = 'i'"):
		return kitDBPostgresCatalogIndexes
	case strings.Contains(normalized, "information_schema.columns"),
		strings.Contains(normalized, "pg_catalog.pg_attribute"),
		strings.Contains(normalized, " from pg_attribute"):
		return kitDBPostgresCatalogColumns
	case strings.Contains(normalized, "pg_catalog.pg_enum"),
		strings.Contains(normalized, " join pg_enum"),
		strings.Contains(normalized, " from pg_enum"):
		return kitDBPostgresCatalogEmpty
	case strings.Contains(normalized, "pg_catalog.pg_proc"),
		strings.Contains(normalized, " from pg_proc"):
		return kitDBPostgresCatalogEmpty
	case strings.Contains(normalized, "information_schema.tables"),
		strings.Contains(normalized, "pg_catalog.pg_tables"),
		strings.Contains(normalized, "pg_catalog.pg_class"),
		strings.Contains(normalized, " from pg_tables"),
		strings.Contains(normalized, " from pg_class"):
		return kitDBPostgresCatalogTables
	case strings.Contains(normalized, "information_schema.schemata"),
		strings.Contains(normalized, "pg_catalog.pg_namespace"),
		strings.Contains(normalized, " from pg_namespace"):
		return kitDBPostgresCatalogNamespaces
	case strings.Contains(normalized, "pg_catalog.pg_database"),
		strings.Contains(normalized, " from pg_database"):
		return kitDBPostgresCatalogDatabase
	case strings.Contains(normalized, "pg_catalog.pg_type"),
		strings.Contains(normalized, " from pg_type"):
		return kitDBPostgresCatalogTypes
	case strings.Contains(normalized, "information_schema."), strings.Contains(normalized, "pg_catalog."):
		return kitDBPostgresCatalogEmpty
	default:
		return kitDBPostgresCatalogNone
	}
}

func (session *kitDBPostgresSession) kitDBPostgresCatalogRecords(
	kind kitDBPostgresCatalogKind,
	definitions map[string]*StructDef,
	relationTuples map[string]value.Value,
) []kitDBPostgresCatalogRecord {
	names := make([]string, 0, len(definitions))
	for name := range definitions {
		names = append(names, name)
	}
	sort.Strings(names)
	switch kind {
	case kitDBPostgresCatalogTables:
		records := make([]kitDBPostgresCatalogRecord, 0, len(names))
		for _, name := range names {
			definition := definitions[name]
			reltuples := value.New(-1)
			if estimate, found := relationTuples[name]; found {
				reltuples = estimate
			}
			hasIndex := false
			for _, field := range definition.Fields {
				hasIndex = hasIndex || field.Primary || field.Unique || len(field.Indexes) > 0
			}
			hasIndex = hasIndex || len(definition.UniqueConstraints) > 0
			oid := kitDBPostgresCatalogOID(session.databaseName, definition.ID, definition.Name)
			records = append(records, kitDBPostgresCatalogRecord{
				"oid": value.New(oid), "relname": value.New(name), "table_name": value.New(name),
				"tablename": value.New(name), "relkind": value.New("r"),
				"relnamespace": value.New(2200), "relowner": value.New(10),
				"relpersistence": value.New("p"), "relhasindex": value.New(hasIndex),
				"relnatts": value.New(len(definition.Fields)), "reltuples": reltuples,
				"nspname": value.New("public"), "table_schema": value.New("public"),
				"schemaname": value.New("public"), "schema_name": value.New("public"),
				"table_catalog": value.New(session.databaseName), "table_cat": value.New(session.databaseName),
				"table_schem": value.New("public"), "table_type": value.New("BASE TABLE"),
				"remarks": value.NewNil(), "description": value.NewNil(),
				"is_insertable_into": value.New("YES"), "tableowner": value.New(session.user),
				"tablespace": value.NewNil(), "hasindexes": value.New(hasIndex),
				"hasrules": value.New(false), "hastriggers": value.New(false),
				"rowsecurity": value.New(false), "is_typed": value.New("NO"),
				"self_referencing_column_name": value.NewNil(), "reference_generation": value.NewNil(),
				"user_defined_type_catalog": value.NewNil(), "user_defined_type_schema": value.NewNil(),
				"user_defined_type_name": value.NewNil(), "commit_action": value.NewNil(),
			})
		}
		return records
	case kitDBPostgresCatalogColumns:
		records := make([]kitDBPostgresCatalogRecord, 0)
		for _, name := range names {
			definition := definitions[name]
			tableOID := kitDBPostgresCatalogOID(session.databaseName, definition.ID, definition.Name)
			for _, field := range definition.Fields {
				typeName, udtName, typeOID := kitDBPostgresCatalogFieldType(field.Kind)
				defaultValue := value.NewNil()
				if field.HasDefault {
					defaultValue = value.New(field.Default.Text())
				} else if field.DefaultNow {
					defaultValue = value.New("CURRENT_TIMESTAMP")
				}
				nullable := "YES"
				if field.NotNull || field.Primary {
					nullable = "NO"
				}
				records = append(records, kitDBPostgresCatalogRecord{
					"table_catalog": value.New(session.databaseName), "table_schema": value.New("public"),
					"table_name": value.New(name), "column_name": value.New(field.Name),
					"ordinal_position": value.New(field.Position + 1), "column_default": defaultValue,
					"is_nullable": value.New(nullable), "data_type": value.New(typeName),
					"udt_catalog": value.New(session.databaseName), "udt_schema": value.New("pg_catalog"),
					"udt_name": value.New(udtName), "dtd_identifier": value.New(strconv.Itoa(field.Position + 1)),
					"attrelid": value.New(tableOID), "attname": value.New(field.Name),
					"atttypid": value.New(typeOID), "attnum": value.New(field.Position + 1),
					"attnotnull": value.New(field.NotNull || field.Primary),
					"atthasdef":  value.New(field.HasDefault || field.DefaultNow),
					"atttypmod":  value.New(-1), "attisdropped": value.New(false),
					"attidentity": value.New(""), "attgenerated": value.New(""),
					"typname": value.New(udtName), "typtype": value.New("b"),
					"typnamespace": value.New(11), "typelem": value.New(0),
					"nspname": value.New("public"), "relname": value.New(name),
					"is_self_referencing": value.New("NO"), "is_identity": value.New("NO"),
					"is_generated": value.New("NEVER"), "is_updatable": value.New("YES"),
				})
			}
		}
		return records
	case kitDBPostgresCatalogNamespaces:
		return []kitDBPostgresCatalogRecord{{
			"oid": value.New(2200), "nspname": value.New("public"), "nspowner": value.New(10),
			"schema_name": value.New("public"), "schema_owner": value.New(session.user),
			"catalog_name": value.New(session.databaseName),
		}}
	case kitDBPostgresCatalogDatabase:
		databaseNames := append([]string(nil), session.databaseNames...)
		if len(databaseNames) == 0 && session.databaseName != "" {
			databaseNames = []string{session.databaseName}
		}
		sort.Strings(databaseNames)
		records := make([]kitDBPostgresCatalogRecord, 0, len(databaseNames))
		for _, databaseName := range databaseNames {
			records = append(records, kitDBPostgresCatalogRecord{
				"oid":     value.New(kitDBPostgresCatalogOID(databaseName, "database")),
				"datname": value.New(databaseName), "database_name": value.New(databaseName),
				"datdba": value.New(10), "encoding": value.New(6), "datcollate": value.New("C"),
				"datctype": value.New("C"), "datistemplate": value.New(false), "datallowconn": value.New(true),
				"datconnlimit": value.New(-1), "dattablespace": value.New(1663),
			})
		}
		return records
	case kitDBPostgresCatalogTypes:
		types := []struct {
			oid  uint32
			name string
		}{
			{pgwire.OIDBool, "bool"},
			{pgwire.OIDBytea, "bytea"},
			{pgwire.OIDInt8, "int8"},
			{pgwire.OIDInt2, "int2"},
			{pgwire.OIDInt4, "int4"},
			{pgwire.OIDText, "text"},
			{pgwire.OIDOID, "oid"},
			{pgwire.OIDFloat4, "float4"},
			{pgwire.OIDFloat8, "float8"},
			{pgwire.OIDJSON, "json"},
			{pgwire.OIDDate, "date"},
			{pgwire.OIDTime, "time"},
			{pgwire.OIDTimestampTZ, "timestamptz"},
			{pgwire.OIDNumeric, "numeric"},
			{pgwire.OIDJSONB, "jsonb"},
		}
		records := make([]kitDBPostgresCatalogRecord, 0, len(types))
		for _, item := range types {
			records = append(records, kitDBPostgresCatalogRecord{
				"oid": value.New(item.oid), "typname": value.New(item.name),
				"type_oid": value.New(item.oid), "type_name": value.New(item.name),
				"typtype": value.New("b"), "typnamespace": value.New(11), "typelem": value.New(0),
			})
		}
		return records
	case kitDBPostgresCatalogIndexes:
		return session.kitDBPostgresIndexRecords(definitions)
	case kitDBPostgresCatalogConstraints:
		return session.kitDBPostgresConstraintRecords(definitions, false)
	case kitDBPostgresCatalogConstraintColumns:
		return session.kitDBPostgresConstraintRecords(definitions, true)
	default:
		return []kitDBPostgresCatalogRecord{}
	}
}

func kitDBPostgresCatalogNeedsRelationTuples(projections []kitDBPostgresCatalogProjection) bool {
	for _, projection := range projections {
		if strings.Contains(strings.ToLower(projection.expression), "reltuples") {
			return true
		}
	}
	return false
}

func loadKitDBPostgresRelationTuples(
	database *dbProxy,
	definitions map[string]*StructDef,
) (map[string]value.Value, error) {
	estimates := make(map[string]value.Value, len(definitions))
	if database == nil || len(definitions) == 0 {
		return estimates, nil
	}
	if database.transaction != nil {
		return collectKitDBPostgresRelationTuples(database.transaction, definitions, estimates)
	}
	handle := kitDBForRequest(database.tenant, database.dbName, database.scope)
	if err := handle.requestError(); err != nil {
		return nil, err
	}
	managed, err := handle.database()
	if err != nil {
		return nil, err
	}
	defer managed.Release()
	return collectKitDBPostgresRelationTuples(managed.database, definitions, estimates)
}

func collectKitDBPostgresRelationTuples(
	reader kitDBReader,
	definitions map[string]*StructDef,
	estimates map[string]value.Value,
) (map[string]value.Value, error) {
	for name, definition := range definitions {
		statistics, found, _, err := loadKitDBStatistics(reader, definition)
		if err != nil {
			return nil, fmt.Errorf("kitdb postgres catalog: load statistics for %q: %w", name, err)
		}
		// reltuples is explicitly an estimate: retain the last analyzed value
		// when writes make it stale, and use PostgreSQL's -1 sentinel if absent.
		if found && statistics != nil && statistics.StructID == definition.ID {
			estimates[name] = value.New(statistics.Rows)
		}
	}
	return estimates, nil
}

func kitDBPostgresCatalogOID(parts ...string) int64 {
	hash := fnv.New32a()
	for _, part := range parts {
		_, _ = hash.Write([]byte(part))
		_, _ = hash.Write([]byte{0})
	}
	return int64(16384 + hash.Sum32()%0x3fff0000)
}

func kitDBPostgresCatalogFieldType(kind string) (string, string, int64) {
	if typeInfo, found := kitdbsql.LookupKind(kind); found {
		postgres := typeInfo.Catalog
		return postgres.DataType, postgres.UDTName, int64(postgres.OID)
	}
	return "text", "text", int64(pgwire.OIDText)
}

func filterKitDBPostgresCatalogRecords(
	source string,
	parameters []pgwire.Parameter,
	records []kitDBPostgresCatalogRecord,
) []kitDBPostgresCatalogRecord {
	if len(records) == 0 {
		return records
	}
	selectors := make(map[string]struct{}, len(parameters))
	for _, parameter := range parameters {
		if parameter.Null {
			continue
		}
		item, err := kitDBPostgresParameter(parameter)
		if err != nil || item.IsNil() || item.IsInvalid() {
			continue
		}
		selectors[strings.ToLower(strings.TrimSpace(item.Text()))] = struct{}{}
	}
	hasIdentitySelector := false
	for selector := range selectors {
		for _, record := range records {
			if selector == kitDBPostgresCatalogRecordText(record, "table_name") ||
				selector == kitDBPostgresCatalogRecordText(record, "relname") ||
				selector == kitDBPostgresCatalogRecordText(record, "attrelid") ||
				selector == kitDBPostgresCatalogRecordText(record, "oid") ||
				selector == kitDBPostgresCatalogRecordText(record, "datname") ||
				selector == kitDBPostgresCatalogRecordText(record, "database_name") {
				hasIdentitySelector = true
				break
			}
		}
	}
	literalName, hasLiteralName := kitDBPostgresCatalogLiteralName(source)
	literalOID, hasLiteralOID := kitDBPostgresCatalogLiteralValue(source, "oid", "conrelid", "indrelid", "attrelid")
	literalColumnName, hasLiteralColumnName := kitDBPostgresCatalogLiteralValue(source, "column_name", "attname")
	literalConstraintType, hasLiteralConstraintType := kitDBPostgresCatalogLiteralValue(source, "constraint_type")
	literalConstraintKind, hasLiteralConstraintKind := kitDBPostgresCatalogLiteralValue(source, "contype")
	filtered := make([]kitDBPostgresCatalogRecord, 0, len(records))
	for _, record := range records {
		identity := kitDBPostgresCatalogRecordText(record, "table_name")
		if identity == "" {
			identity = kitDBPostgresCatalogRecordText(record, "relname")
		}
		if identity == "" {
			identity = kitDBPostgresCatalogRecordText(record, "datname")
		}
		oid := kitDBPostgresCatalogRecordText(record, "oid")
		if oid == "" {
			oid = kitDBPostgresCatalogRecordText(record, "attrelid")
		}
		_, selectedByIdentity := selectors[identity]
		_, selectedByOID := selectors[oid]
		if hasIdentitySelector && !selectedByIdentity && !selectedByOID {
			continue
		}
		if hasLiteralName && identity != literalName {
			continue
		}
		if hasLiteralOID && !kitDBPostgresCatalogRecordMatches(record, literalOID,
			"table_oid", "conrelid", "indrelid", "attrelid", "oid", "indexrelid") {
			continue
		}
		if hasLiteralColumnName &&
			!kitDBPostgresCatalogRecordMatches(record, literalColumnName, "column_name", "attname") {
			continue
		}
		if hasLiteralConstraintType &&
			kitDBPostgresCatalogRecordText(record, "constraint_type") != literalConstraintType {
			continue
		}
		if hasLiteralConstraintKind &&
			kitDBPostgresCatalogRecordText(record, "contype") != literalConstraintKind {
			continue
		}
		filtered = append(filtered, record)
	}
	return filtered
}

func kitDBPostgresCatalogRecordText(record kitDBPostgresCatalogRecord, key string) string {
	item, found := record[key]
	if !found || item.IsNil() || item.IsInvalid() {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(item.Text()))
}

func kitDBPostgresCatalogLiteralName(source string) (string, bool) {
	return kitDBPostgresCatalogLiteralValue(
		source, "table_name", "tablename", "child_name", "relname", "datname", "database_name",
	)
}

func kitDBPostgresCatalogLiteralValue(source string, names ...string) (string, bool) {
	lower := strings.NewReplacer(`"`, "", "`", "", "[", "", "]", "").Replace(strings.ToLower(source))
	for _, name := range names {
		searchAt := 0
		for {
			fieldAt := strings.Index(lower[searchAt:], name)
			if fieldAt < 0 {
				break
			}
			fieldAt += searchAt
			if fieldAt > 0 && kitDBPostgresIdentifierByte(lower[fieldAt-1]) {
				searchAt = fieldAt + len(name)
				continue
			}
			fieldAt += len(name)
			if fieldAt < len(lower) && kitDBPostgresIdentifierByte(lower[fieldAt]) {
				searchAt = fieldAt
				continue
			}
			for fieldAt < len(lower) && (lower[fieldAt] == ' ' || lower[fieldAt] == '\t' || lower[fieldAt] == '\r' || lower[fieldAt] == '\n') {
				fieldAt++
			}
			if fieldAt >= len(lower) || lower[fieldAt] != '=' {
				searchAt = fieldAt
				continue
			}
			fieldAt++
			for fieldAt < len(lower) && (lower[fieldAt] == ' ' || lower[fieldAt] == '\t' || lower[fieldAt] == '\r' || lower[fieldAt] == '\n') {
				fieldAt++
			}
			if fieldAt >= len(lower) || lower[fieldAt] != '\'' {
				searchAt = fieldAt
				continue
			}
			fieldAt++
			end := fieldAt
			for end < len(lower) && lower[end] != '\'' {
				end++
			}
			if end < len(lower) {
				return strings.TrimSpace(lower[fieldAt:end]), true
			}
			break
		}
	}
	return "", false
}

func kitDBPostgresCatalogRecordMatches(record kitDBPostgresCatalogRecord, expected string, names ...string) bool {
	for _, name := range names {
		if kitDBPostgresCatalogRecordText(record, name) == expected {
			return true
		}
	}
	return false
}

func kitDBPostgresCatalogUsesCurrentDatabase(source string) bool {
	normalized := strings.NewReplacer(`"`, "", "`", "", "[", "", "]", "").Replace(normalizeKitDBPostgresSQL(source))
	return strings.Contains(normalized, "datname = current_database()") ||
		strings.Contains(normalized, "datname=current_database()")
}

func parseKitDBPostgresCatalogProjections(
	source string,
	kind kitDBPostgresCatalogKind,
) ([]kitDBPostgresCatalogProjection, error) {
	source = firstKitDBPostgresCatalogSelect(source)
	selectAt := findKitDBPostgresTopLevelKeyword(source, "select", 0)
	if selectAt < 0 {
		return nil, fmt.Errorf("kitdb postgres catalog: expected SELECT")
	}
	fromAt := findKitDBPostgresTopLevelKeyword(source, "from", selectAt+len("select"))
	if fromAt < 0 {
		return nil, fmt.Errorf("kitdb postgres catalog: expected FROM")
	}
	list := strings.TrimSpace(source[selectAt+len("select") : fromAt])
	if strings.HasPrefix(strings.ToLower(list), "distinct ") {
		list = strings.TrimSpace(list[len("distinct "):])
	}
	items := splitKitDBPostgresTopLevel(list)
	projections := make([]kitDBPostgresCatalogProjection, 0, len(items))
	for _, item := range items {
		expression := strings.TrimSpace(item)
		if expression == "" {
			continue
		}
		if expression == "*" || strings.HasSuffix(expression, ".*") {
			for _, name := range kitDBPostgresCatalogStar(kind, source) {
				projections = append(projections, kitDBPostgresCatalogProjection{
					expression: name, name: name, kind: kitDBPostgresCatalogProjectionKind(name, name),
				})
			}
			continue
		}
		name := kitDBPostgresCatalogProjectionName(expression)
		projections = append(projections, kitDBPostgresCatalogProjection{
			expression: expression, name: name,
			kind: kitDBPostgresCatalogProjectionKind(name, expression),
		})
	}
	if len(projections) == 0 || len(projections) > 128 {
		return nil, fmt.Errorf("kitdb postgres catalog: projection count is outside 1..128")
	}
	return projections, nil
}

// TablePlus wraps its base-table and materialized-view discovery branches in
// parentheses joined by UNION. KitDB has no materialized views, so projecting
// the first catalog SELECT is both exact and avoids teaching the SQL-light
// executor a catalog-only set operator.
func firstKitDBPostgresCatalogSelect(source string) string {
	trimmed := strings.TrimSpace(source)
	if len(trimmed) < 2 || trimmed[0] != '(' {
		return source
	}
	depth := 0
	quote := byte(0)
	for index := 0; index < len(trimmed); index++ {
		current := trimmed[index]
		if quote != 0 {
			if current == quote {
				if index+1 < len(trimmed) && trimmed[index+1] == quote {
					index++
					continue
				}
				quote = 0
			}
			continue
		}
		if current == '\'' || current == '"' {
			quote = current
			continue
		}
		switch current {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				candidate := strings.TrimSpace(trimmed[1:index])
				if findKitDBPostgresTopLevelKeyword(candidate, "select", 0) >= 0 {
					return candidate
				}
				return source
			}
		}
	}
	return source
}

func findKitDBPostgresTopLevelKeyword(source, keyword string, start int) int {
	depth := 0
	quote := byte(0)
	for index := start; index < len(source); index++ {
		current := source[index]
		if quote != 0 {
			if current == quote {
				if index+1 < len(source) && source[index+1] == quote {
					index++
					continue
				}
				quote = 0
			}
			continue
		}
		if current == '\'' || current == '"' {
			quote = current
			continue
		}
		switch current {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		}
		if depth != 0 || !strings.EqualFold(source[index:minInt(index+len(keyword), len(source))], keyword) {
			continue
		}
		beforeOK := index == 0 || !kitDBPostgresIdentifierByte(source[index-1])
		after := index + len(keyword)
		afterOK := after == len(source) || !kitDBPostgresIdentifierByte(source[after])
		if beforeOK && afterOK {
			return index
		}
	}
	return -1
}

func splitKitDBPostgresTopLevel(source string) []string {
	parts := make([]string, 0, 8)
	start, depth := 0, 0
	quote := byte(0)
	for index := 0; index < len(source); index++ {
		current := source[index]
		if quote != 0 {
			if current == quote {
				if index+1 < len(source) && source[index+1] == quote {
					index++
					continue
				}
				quote = 0
			}
			continue
		}
		if current == '\'' || current == '"' {
			quote = current
			continue
		}
		switch current {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				parts = append(parts, source[start:index])
				start = index + 1
			}
		}
	}
	parts = append(parts, source[start:])
	return parts
}

func kitDBPostgresCatalogProjectionName(expression string) string {
	if asAt := findLastKitDBPostgresTopLevelAS(expression); asAt >= 0 {
		return trimKitDBPostgresIdentifier(expression[asAt+2:])
	}
	words := strings.Fields(expression)
	if len(words) > 1 {
		candidate := trimKitDBPostgresIdentifier(words[len(words)-1])
		if candidate != "" && !strings.ContainsAny(candidate, "()+-*/%:<>=") && !strings.EqualFold(candidate, "end") {
			return candidate
		}
	}
	base := expression
	if cast := strings.Index(base, "::"); cast >= 0 {
		base = base[:cast]
	}
	if dot := strings.LastIndex(base, "."); dot >= 0 {
		base = base[dot+1:]
	}
	name := trimKitDBPostgresIdentifier(base)
	if name == "" || strings.ContainsAny(name, "()+-*/%:<>=") {
		return "?column?"
	}
	return name
}

func findLastKitDBPostgresTopLevelAS(source string) int {
	found := -1
	start := 0
	for {
		index := findKitDBPostgresTopLevelKeyword(source, "as", start)
		if index < 0 {
			return found
		}
		found = index
		start = index + 2
	}
}

func trimKitDBPostgresIdentifier(source string) string {
	source = strings.TrimSpace(strings.TrimSuffix(source, ";"))
	if len(source) >= 2 {
		if (source[0] == '"' && source[len(source)-1] == '"') ||
			(source[0] == '`' && source[len(source)-1] == '`') ||
			(source[0] == '[' && source[len(source)-1] == ']') {
			source = source[1 : len(source)-1]
		}
	}
	return source
}

func kitDBPostgresIdentifierByte(value byte) bool {
	return value == '_' || value >= '0' && value <= '9' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func kitDBPostgresCatalogStar(kind kitDBPostgresCatalogKind, source string) []string {
	normalized := strings.NewReplacer(`"`, "", "`", "", "[", "", "]", "").Replace(strings.ToLower(source))
	switch kind {
	case kitDBPostgresCatalogTables:
		if strings.Contains(normalized, "information_schema.tables") {
			return []string{
				"table_catalog", "table_schema", "table_name", "table_type",
				"self_referencing_column_name", "reference_generation",
				"user_defined_type_catalog", "user_defined_type_schema", "user_defined_type_name",
				"is_insertable_into", "is_typed", "commit_action",
			}
		}
		if strings.Contains(normalized, "pg_catalog.pg_tables") || strings.Contains(normalized, " from pg_tables") {
			return []string{
				"schemaname", "tablename", "tableowner", "tablespace",
				"hasindexes", "hasrules", "hastriggers", "rowsecurity",
			}
		}
		return []string{"oid", "relname", "relnamespace", "relkind", "relowner", "relpersistence", "relhasindex", "relnatts", "reltuples"}
	case kitDBPostgresCatalogColumns:
		if strings.Contains(normalized, "information_schema.columns") {
			return []string{
				"table_catalog", "table_schema", "table_name", "column_name", "ordinal_position",
				"column_default", "is_nullable", "data_type", "character_maximum_length",
				"character_octet_length", "numeric_precision", "numeric_precision_radix", "numeric_scale",
				"datetime_precision", "interval_type", "interval_precision", "character_set_catalog",
				"character_set_schema", "character_set_name", "collation_catalog", "collation_schema",
				"collation_name", "domain_catalog", "domain_schema", "domain_name", "udt_catalog",
				"udt_schema", "udt_name", "scope_catalog", "scope_schema", "scope_name",
				"maximum_cardinality", "dtd_identifier", "is_self_referencing", "is_identity",
				"identity_generation", "identity_start", "identity_increment", "identity_maximum",
				"identity_minimum", "identity_cycle", "is_generated", "generation_expression",
				"is_updatable",
			}
		}
		return []string{"attrelid", "attname", "atttypid", "attnum", "attnotnull", "atthasdef", "atttypmod", "attisdropped", "attidentity", "attgenerated"}
	case kitDBPostgresCatalogNamespaces:
		if strings.Contains(normalized, "information_schema.schemata") {
			return []string{"catalog_name", "schema_name", "schema_owner", "default_character_set_catalog", "default_character_set_schema", "default_character_set_name", "sql_path"}
		}
		return []string{"oid", "nspname", "nspowner"}
	case kitDBPostgresCatalogDatabase:
		return []string{"oid", "datname", "datdba", "encoding", "datcollate", "datctype", "datistemplate", "datallowconn", "datconnlimit", "dattablespace"}
	case kitDBPostgresCatalogTypes:
		return []string{"oid", "typname", "typnamespace", "typtype", "typelem"}
	case kitDBPostgresCatalogIndexes:
		if strings.Contains(normalized, "pg_catalog.pg_indexes") || strings.Contains(normalized, " from pg_indexes") {
			return []string{"schemaname", "tablename", "indexname", "tablespace", "indexdef"}
		}
		if strings.Contains(normalized, "pg_catalog.pg_index") || strings.Contains(normalized, " from pg_index") {
			return []string{
				"indexrelid", "indrelid", "indnatts", "indnkeyatts", "indisunique", "indnullsnotdistinct",
				"indisprimary", "indisexclusion", "indimmediate", "indisclustered", "indisvalid", "indcheckxmin",
				"indisready", "indislive", "indisreplident", "indkey", "indcollation", "indclass",
				"indoption", "indexprs", "indpred",
			}
		}
		return []string{"oid", "relname", "relnamespace", "relkind", "relowner", "relpersistence", "relam", "relnatts"}
	case kitDBPostgresCatalogConstraints:
		if strings.Contains(normalized, "information_schema.table_constraints") {
			return []string{
				"constraint_catalog", "constraint_schema", "constraint_name", "table_catalog", "table_schema",
				"table_name", "constraint_type", "is_deferrable", "initially_deferred", "enforced",
				"nulls_distinct",
			}
		}
		if strings.Contains(normalized, "information_schema.constraint_column_usage") {
			return []string{
				"table_catalog", "table_schema", "table_name", "column_name",
				"constraint_catalog", "constraint_schema", "constraint_name",
			}
		}
		return []string{
			"oid", "conname", "connamespace", "contype", "condeferrable", "condeferred", "convalidated",
			"conrelid", "contypid", "conindid", "conparentid", "confrelid", "confupdtype", "confdeltype",
			"confmatchtype", "conislocal", "coninhcount", "connoinherit", "conkey", "confkey", "conbin",
		}
	case kitDBPostgresCatalogConstraintColumns:
		return []string{
			"constraint_catalog", "constraint_schema", "constraint_name", "table_catalog", "table_schema",
			"table_name", "column_name", "ordinal_position", "position_in_unique_constraint",
		}
	default:
		return []string{"table_catalog", "table_schema", "table_name", "table_type"}
	}
}

func kitDBPostgresCatalogProjectionKind(name, expression string) string {
	key := strings.ToLower(name)
	normalizedExpression := strings.ToLower(expression)
	if strings.Contains(normalizedExpression, "::int8") || strings.Contains(normalizedExpression, "::bigint") {
		return "integer"
	}
	switch key {
	case "oid", "type_oid", "attrelid", "atttypid", "relnamespace", "relowner", "nspowner", "datdba", "dattablespace", "typnamespace", "typelem",
		"indexrelid", "indrelid", "table_oid", "connamespace", "conrelid", "contypid", "conindid", "conparentid", "confrelid", "relam":
		return "oid"
	case "attnum", "atttypmod", "relnatts", "ordinal_position", "position_in_unique_constraint", "encoding", "datconnlimit", "numeric_precision", "numeric_scale", "datetime_precision", "indnatts", "indnkeyatts", "coninhcount":
		return "integer"
	case "reltuples":
		return "float"
	case "relhasindex", "hasindexes", "hasrules", "hastriggers", "rowsecurity",
		"attnotnull", "atthasdef", "attisdropped", "datistemplate", "datallowconn",
		"is_unique", "is_primary", "indisunique", "indnullsnotdistinct", "indisprimary", "indisexclusion",
		"indimmediate", "indisclustered", "indisvalid", "indcheckxmin", "indisready", "indislive",
		"indisreplident", "condeferrable", "condeferred", "convalidated", "conislocal", "connoinherit":
		return "bool"
	}
	if strings.Contains(normalizedExpression, "count(") {
		return "integer"
	}
	return "text"
}

func kitDBPostgresCatalogValue(
	record kitDBPostgresCatalogRecord,
	projection kitDBPostgresCatalogProjection,
) value.Value {
	key := strings.ToLower(projection.name)
	if item, found := record[key]; found {
		return item
	}
	expression := strings.ToLower(projection.expression)
	if strings.Contains(expression, "format_type") {
		return record["data_type"]
	}
	if strings.Contains(expression, "pg_get_indexdef") {
		return record["index_definition"]
	}
	for _, candidate := range []string{
		"table_catalog", "table_schema", "table_name", "table_type", "column_name", "ordinal_position",
		"column_default", "is_nullable", "data_type", "udt_catalog", "udt_schema", "udt_name",
		"schemaname", "tablename", "tableowner", "tablespace", "hasindexes", "hasrules", "hastriggers",
		"rowsecurity", "oid", "relname", "relnamespace", "relkind", "nspname", "attrelid", "attname",
		"atttypid", "attnum", "attnotnull", "atthasdef", "atttypmod", "reltuples", "typname", "type_oid", "type_name",
		"datname", "database_name", "index_name", "index_algorithm", "is_unique", "is_primary",
		"index_definition", "indexdef", "indexname", "condition", "constraint_catalog", "constraint_schema",
		"constraint_name", "constraint_type", "conname", "contype", "conrelid", "conindid", "confrelid",
		"confupdtype", "confdeltype", "child_schema", "child_name", "child_column", "parent_schema",
		"parent_name", "parent_column", "on_update", "on_delete", "position_in_unique_constraint",
	} {
		if strings.Contains(expression, candidate) {
			if item, found := record[candidate]; found {
				return item
			}
		}
	}
	if strings.Contains(expression, "obj_description") || strings.Contains(expression, "description") ||
		strings.HasPrefix(strings.TrimSpace(expression), "null") {
		return value.NewNil()
	}
	if strings.Contains(expression, "has_") || strings.Contains(expression, "privilege") {
		return value.New(true)
	}
	return value.NewNil()
}

func minInt(first, second int) int {
	if first < second {
		return first
	}
	return second
}
