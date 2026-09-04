package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
	"github.com/lib/pq"
)

const SchemaReportFormat = "kitdb-postgres-schema/v1"

type SchemaReport struct {
	Format               string             `json:"format"`
	Database             string             `json:"database"`
	Schema               string             `json:"schema"`
	Table                string             `json:"table"`
	EstimatedRows        int64              `json:"estimated_rows"`
	TotalBytes           int64              `json:"total_bytes"`
	HeapBytes            int64              `json:"heap_bytes"`
	IndexBytes           int64              `json:"index_bytes"`
	ToastBytes           int64              `json:"toast_bytes"`
	Columns              []ColumnReport     `json:"columns"`
	PrimaryKey           []string           `json:"primary_key,omitempty"`
	Constraints          []ConstraintReport `json:"constraints,omitempty"`
	Indexes              []IndexReport      `json:"indexes,omitempty"`
	Triggers             []TriggerReport    `json:"triggers,omitempty"`
	FullText             FullTextReport     `json:"full_text"`
	RelationalCompatible bool               `json:"relational_compatible"`
	Blockers             []string           `json:"blockers,omitempty"`
	Warnings             []string           `json:"warnings,omitempty"`
}

type ColumnReport struct {
	Position      int      `json:"position"`
	Name          string   `json:"name"`
	DataType      string   `json:"data_type"`
	InternalType  string   `json:"internal_type"`
	TypeKind      string   `json:"type_kind"`
	Nullable      bool     `json:"nullable"`
	Default       string   `json:"default,omitempty"`
	Identity      string   `json:"identity,omitempty"`
	Generated     string   `json:"generated,omitempty"`
	Collation     string   `json:"collation,omitempty"`
	AverageWidth  int64    `json:"average_width,omitempty"`
	Distinct      *float64 `json:"distinct_estimate,omitempty"`
	Precision     *int     `json:"precision,omitempty"`
	Scale         *int     `json:"scale,omitempty"`
	TimePrecision *int     `json:"time_precision,omitempty"`
	TextLength    *int     `json:"text_length,omitempty"`
	Enum          []string `json:"enum,omitempty"`
	TargetKind    string   `json:"target_kind,omitempty"`
	Mapping       string   `json:"mapping"`
	MappingReason string   `json:"mapping_reason,omitempty"`
}

type ConstraintReport struct {
	Name              string   `json:"name"`
	Kind              string   `json:"kind"`
	Columns           []string `json:"columns,omitempty"`
	ReferencedTable   string   `json:"referenced_table,omitempty"`
	ReferencedColumns []string `json:"referenced_columns,omitempty"`
	Definition        string   `json:"definition"`
	Validated         bool     `json:"validated"`
	Deferrable        bool     `json:"deferrable"`
	InitiallyDeferred bool     `json:"initially_deferred"`
}

type IndexReport struct {
	Name       string   `json:"name"`
	Method     string   `json:"method"`
	Columns    []string `json:"columns,omitempty"`
	Definition string   `json:"definition"`
	Predicate  string   `json:"predicate,omitempty"`
	Primary    bool     `json:"primary"`
	Unique     bool     `json:"unique"`
	Valid      bool     `json:"valid"`
	Ready      bool     `json:"ready"`
	TotalBytes int64    `json:"total_bytes"`
	FullText   bool     `json:"full_text"`
}

type TriggerReport struct {
	Name       string `json:"name"`
	Enabled    string `json:"enabled"`
	Definition string `json:"definition"`
	FullText   bool   `json:"full_text"`
}

type FullTextReport struct {
	Detected        bool     `json:"detected"`
	VectorColumns   []string `json:"vector_columns,omitempty"`
	IndexNames      []string `json:"index_names,omitempty"`
	TriggerNames    []string `json:"trigger_names,omitempty"`
	RequiresRebuild bool     `json:"requires_rebuild"`
}

// InspectTable reads one relation's catalog shape without scanning its rows.
func InspectTable(ctx context.Context, config Config, table string) (SchemaReport, error) {
	config, err := normalizeConfig(config)
	if err != nil {
		return SchemaReport{}, err
	}
	table = strings.TrimSpace(table)
	if table == "" {
		return SchemaReport{}, fmt.Errorf("postgres schema: table is required")
	}
	if strings.IndexByte(table, 0) >= 0 {
		return SchemaReport{}, fmt.Errorf("postgres schema: table contains NUL")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()

	database, err := sql.Open("postgres", config.URL)
	if err != nil {
		return SchemaReport{}, sourceError(config.URL, "initialize driver", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(0)
	database.SetConnMaxLifetime(config.Timeout)
	defer database.Close()
	if err := database.PingContext(ctx); err != nil {
		return SchemaReport{}, sourceError(config.URL, "connect", err)
	}
	tx, err := database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return SchemaReport{}, sourceError(config.URL, "begin read-only snapshot", err)
	}
	defer tx.Rollback()

	report := SchemaReport{Format: SchemaReportFormat, Schema: config.Schema, Table: table}
	var relationOID uint32
	if err := tx.QueryRowContext(ctx, `
		SELECT
			current_database()::text,
			c.oid,
			GREATEST(c.reltuples, 0)::bigint,
			pg_catalog.pg_total_relation_size(c.oid),
			pg_catalog.pg_relation_size(c.oid),
			pg_catalog.pg_indexes_size(c.oid),
			CASE WHEN c.reltoastrelid = 0 THEN 0 ELSE pg_catalog.pg_total_relation_size(c.reltoastrelid) END
		FROM pg_catalog.pg_class AS c
		JOIN pg_catalog.pg_namespace AS n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relname = $2 AND c.relkind IN ('r', 'p')
	`, config.Schema, table).Scan(
		&report.Database,
		&relationOID,
		&report.EstimatedRows,
		&report.TotalBytes,
		&report.HeapBytes,
		&report.IndexBytes,
		&report.ToastBytes,
	); err != nil {
		if err == sql.ErrNoRows {
			return SchemaReport{}, fmt.Errorf("postgres schema: table %q.%q does not exist", config.Schema, table)
		}
		return SchemaReport{}, sourceError(config.URL, "inspect table", err)
	}

	if err := inspectColumns(ctx, tx, relationOID, &report); err != nil {
		return SchemaReport{}, sourceError(config.URL, "inspect columns", err)
	}
	if err := inspectConstraints(ctx, tx, relationOID, &report); err != nil {
		return SchemaReport{}, sourceError(config.URL, "inspect constraints", err)
	}
	if err := inspectIndexes(ctx, tx, relationOID, &report); err != nil {
		return SchemaReport{}, sourceError(config.URL, "inspect indexes", err)
	}
	if err := inspectTriggers(ctx, tx, relationOID, &report); err != nil {
		return SchemaReport{}, sourceError(config.URL, "inspect triggers", err)
	}
	evaluateSchemaReport(&report)
	return report, nil
}

func inspectColumns(ctx context.Context, tx *sql.Tx, relationOID uint32, report *SchemaReport) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT
			a.attnum,
			a.attname::text,
			pg_catalog.format_type(a.atttypid, a.atttypmod),
			t.typname::text,
			t.typtype::text,
			NOT a.attnotnull,
			COALESCE(pg_catalog.pg_get_expr(d.adbin, d.adrelid), ''),
			a.attidentity::text,
			a.attgenerated::text,
			COALESCE(coll.collname::text, ''),
			COALESCE(stats.avg_width, 0)::bigint,
			stats.n_distinct::double precision,
			ARRAY(
				SELECT enum_value.enumlabel::text
				FROM pg_catalog.pg_enum AS enum_value
				WHERE enum_value.enumtypid = a.atttypid
				ORDER BY enum_value.enumsortorder
			)
		FROM pg_catalog.pg_attribute AS a
		JOIN pg_catalog.pg_type AS t ON t.oid = a.atttypid
		LEFT JOIN pg_catalog.pg_attrdef AS d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
		LEFT JOIN pg_catalog.pg_collation AS coll ON coll.oid = a.attcollation AND a.attcollation <> t.typcollation
		LEFT JOIN pg_catalog.pg_class AS source_table ON source_table.oid = a.attrelid
		LEFT JOIN pg_catalog.pg_namespace AS source_schema ON source_schema.oid = source_table.relnamespace
		LEFT JOIN pg_catalog.pg_stats AS stats
			ON stats.schemaname = source_schema.nspname
			AND stats.tablename = source_table.relname
			AND stats.attname = a.attname
		WHERE a.attrelid = $1 AND a.attnum > 0 AND NOT a.attisdropped
		ORDER BY a.attnum
	`, relationOID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			column   ColumnReport
			distinct sql.NullFloat64
		)
		if err := rows.Scan(
			&column.Position,
			&column.Name,
			&column.DataType,
			&column.InternalType,
			&column.TypeKind,
			&column.Nullable,
			&column.Default,
			&column.Identity,
			&column.Generated,
			&column.Collation,
			&column.AverageWidth,
			&distinct,
			pq.Array(&column.Enum),
		); err != nil {
			return err
		}
		if distinct.Valid {
			column.Distinct = &distinct.Float64
		}
		if column.InternalType == "numeric" {
			if precision, scale, constrained, parseErr := postgresNumericModifier(column.DataType); parseErr != nil {
				return fmt.Errorf("column %q: %w", column.Name, parseErr)
			} else if constrained {
				column.Precision, column.Scale = &precision, &scale
			}
		}
		if column.InternalType == "time" || column.InternalType == "timestamp" || column.InternalType == "timestamptz" {
			precision, constrained, parseErr := postgresTemporalModifier(column.DataType, column.InternalType)
			if parseErr != nil {
				return fmt.Errorf("column %q: %w", column.Name, parseErr)
			}
			if constrained {
				column.TimePrecision = &precision
			}
		}
		if column.InternalType == "varchar" || column.InternalType == "bpchar" {
			length, constrained, parseErr := postgresTextModifier(column.DataType, column.InternalType)
			if parseErr != nil {
				return fmt.Errorf("column %q: %w", column.Name, parseErr)
			}
			if constrained {
				column.TextLength = &length
			}
		}
		column.TargetKind, column.Mapping, column.MappingReason = mapColumn(column)
		report.Columns = append(report.Columns, column)
	}
	return rows.Err()
}

func inspectConstraints(ctx context.Context, tx *sql.Tx, relationOID uint32, report *SchemaReport) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT
			constraint_record.conname::text,
			constraint_record.contype::text,
			ARRAY(
				SELECT attribute.attname::text
				FROM unnest(constraint_record.conkey) WITH ORDINALITY AS key(attnum, position)
				JOIN pg_catalog.pg_attribute AS attribute
					ON attribute.attrelid = constraint_record.conrelid AND attribute.attnum = key.attnum
				ORDER BY key.position
			),
			COALESCE(constraint_record.confrelid::regclass::text, ''),
			ARRAY(
				SELECT attribute.attname::text
				FROM unnest(constraint_record.confkey) WITH ORDINALITY AS key(attnum, position)
				JOIN pg_catalog.pg_attribute AS attribute
					ON attribute.attrelid = constraint_record.confrelid AND attribute.attnum = key.attnum
				ORDER BY key.position
			),
			pg_catalog.pg_get_constraintdef(constraint_record.oid, true),
			constraint_record.convalidated,
			constraint_record.condeferrable,
			constraint_record.condeferred
		FROM pg_catalog.pg_constraint AS constraint_record
		WHERE constraint_record.conrelid = $1
		ORDER BY constraint_record.contype, constraint_record.conname
	`, relationOID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			constraint ConstraintReport
			kind       string
		)
		if err := rows.Scan(
			&constraint.Name,
			&kind,
			pq.Array(&constraint.Columns),
			&constraint.ReferencedTable,
			pq.Array(&constraint.ReferencedColumns),
			&constraint.Definition,
			&constraint.Validated,
			&constraint.Deferrable,
			&constraint.InitiallyDeferred,
		); err != nil {
			return err
		}
		constraint.Kind = constraintKind(kind)
		if kind == "p" {
			report.PrimaryKey = append([]string(nil), constraint.Columns...)
		}
		report.Constraints = append(report.Constraints, constraint)
	}
	return rows.Err()
}

func inspectIndexes(ctx context.Context, tx *sql.Tx, relationOID uint32, report *SchemaReport) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT
			index_class.relname::text,
			access_method.amname::text,
			ARRAY(
				SELECT pg_catalog.pg_get_indexdef(index_record.indexrelid, position, true)
				FROM generate_series(1, index_record.indnkeyatts) AS position
				ORDER BY position
			),
			pg_catalog.pg_get_indexdef(index_record.indexrelid),
			COALESCE(pg_catalog.pg_get_expr(index_record.indpred, index_record.indrelid), ''),
			index_record.indisprimary,
			index_record.indisunique,
			index_record.indisvalid,
			index_record.indisready,
			pg_catalog.pg_relation_size(index_record.indexrelid)
		FROM pg_catalog.pg_index AS index_record
		JOIN pg_catalog.pg_class AS index_class ON index_class.oid = index_record.indexrelid
		JOIN pg_catalog.pg_am AS access_method ON access_method.oid = index_class.relam
		WHERE index_record.indrelid = $1
		ORDER BY index_record.indisprimary DESC, index_record.indisunique DESC, index_class.relname
	`, relationOID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var index IndexReport
		if err := rows.Scan(
			&index.Name,
			&index.Method,
			pq.Array(&index.Columns),
			&index.Definition,
			&index.Predicate,
			&index.Primary,
			&index.Unique,
			&index.Valid,
			&index.Ready,
			&index.TotalBytes,
		); err != nil {
			return err
		}
		index.FullText = indexLooksFullText(index, report.Columns)
		if index.FullText {
			report.FullText.IndexNames = append(report.FullText.IndexNames, index.Name)
		}
		report.Indexes = append(report.Indexes, index)
	}
	return rows.Err()
}

func inspectTriggers(ctx context.Context, tx *sql.Tx, relationOID uint32, report *SchemaReport) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT
			trigger_record.tgname::text,
			trigger_record.tgenabled::text,
			pg_catalog.pg_get_triggerdef(trigger_record.oid, true)
		FROM pg_catalog.pg_trigger AS trigger_record
		WHERE trigger_record.tgrelid = $1 AND NOT trigger_record.tgisinternal
		ORDER BY trigger_record.tgname
	`, relationOID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var trigger TriggerReport
		if err := rows.Scan(&trigger.Name, &trigger.Enabled, &trigger.Definition); err != nil {
			return err
		}
		lower := strings.ToLower(trigger.Definition)
		trigger.FullText = strings.Contains(lower, "tsvector") || strings.Contains(lower, "tsquery")
		if trigger.FullText {
			report.FullText.TriggerNames = append(report.FullText.TriggerNames, trigger.Name)
		}
		report.Triggers = append(report.Triggers, trigger)
	}
	return rows.Err()
}

func mapColumn(column ColumnReport) (kind, mapping, reason string) {
	if len(column.Enum) > 0 || column.TypeKind == "e" {
		return "enum", "exact", "PostgreSQL enum labels become KitDB choice values"
	}
	switch column.InternalType {
	case "text":
		return "text", "exact", ""
	case "varchar":
		if column.TextLength != nil {
			return fmt.Sprintf("varchar(%d)", *column.TextLength), "exact", "declared character length is preserved"
		}
		return "varchar", "exact", ""
	case "bpchar":
		if column.TextLength != nil {
			return fmt.Sprintf("char(%d)", *column.TextLength), "exact", "blank padding and declared character length are preserved"
		}
		return "text", "semantic", "unconstrained bpchar has no SQL-standard CHAR spelling; values are preserved without blank-insignificant comparison"
	case "int2":
		return "smallint", "exact", ""
	case "int4":
		return "integer", "exact", ""
	case "int8":
		return "bigint", "exact", ""
	case "float4", "float8":
		return "float", "exact", "subject to IEEE-754 semantics already used by PostgreSQL floating types"
	case "numeric":
		if column.Precision != nil && column.Scale != nil {
			return fmt.Sprintf("decimal(%d,%d)", *column.Precision, *column.Scale), "exact",
				"precision/scale and exact value semantics are preserved without float64"
		}
		return "decimal", "exact", "stored as canonical decimal text rather than float64"
	case "bool":
		return "bool", "exact", ""
	case "timestamp":
		if column.TimePrecision != nil {
			return fmt.Sprintf("timestamp(%d)", *column.TimePrecision), "exact", "civil timestamp and declared microsecond precision are preserved"
		}
		return "timestamp", "exact", "civil timestamp semantics are preserved"
	case "timestamptz":
		if column.TimePrecision != nil {
			return fmt.Sprintf("timestamptz(%d)", *column.TimePrecision), "exact", "instant and declared microsecond precision are preserved in UTC"
		}
		return "timestamptz", "exact", "instant semantics are preserved in UTC"
	case "date":
		return "date", "exact", ""
	case "time":
		if column.TimePrecision != nil {
			return fmt.Sprintf("time(%d)", *column.TimePrecision), "exact", "declared microsecond precision is preserved"
		}
		return "time", "exact", ""
	case "interval":
		return "interval", "exact", "months, days and microseconds remain independent components"
	case "json":
		return "json", "exact", ""
	case "jsonb":
		return "jsonb", "semantic", "JSON value is preserved; PostgreSQL binary key ordering is not"
	case "bytea":
		return "blob", "exact", ""
	case "uuid":
		return "uuid", "exact", ""
	case "tsvector":
		return "", "derived", "PostgreSQL tsvector is rebuilt as a Kitwork search projection"
	case "_text", "_varchar", "_int2", "_int4", "_int8", "_float4", "_float8", "_bool", "_uuid":
		return "array", "semantic", "PostgreSQL array bounds are not preserved; element order and values are"
	default:
		return "", "unsupported", "no deterministic KitDB mapping is registered for this PostgreSQL type"
	}
}

func postgresNumericModifier(dataType string) (precision, scale int, constrained bool, err error) {
	text := strings.ToLower(strings.TrimSpace(dataType))
	if text == "numeric" || text == "decimal" {
		return 0, 0, false, nil
	}
	open := strings.IndexByte(text, '(')
	if open < 0 || !strings.HasSuffix(text, ")") ||
		(text[:open] != "numeric" && text[:open] != "decimal") {
		return 0, 0, false, fmt.Errorf("unsupported PostgreSQL numeric modifier %q", dataType)
	}
	parts := strings.Split(text[open+1:len(text)-1], ",")
	if len(parts) < 1 || len(parts) > 2 {
		return 0, 0, false, fmt.Errorf("invalid PostgreSQL numeric modifier %q", dataType)
	}
	precision64, parseErr := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 32)
	if parseErr != nil {
		return 0, 0, false, fmt.Errorf("invalid PostgreSQL numeric precision %q", dataType)
	}
	scale64 := int64(0)
	if len(parts) == 2 {
		scale64, parseErr = strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 32)
		if parseErr != nil {
			return 0, 0, false, fmt.Errorf("invalid PostgreSQL numeric scale %q", dataType)
		}
	}
	if precision64 < 1 || precision64 > 1_000 || scale64 < 0 || scale64 > precision64 {
		return 0, 0, false, fmt.Errorf(
			"PostgreSQL numeric(%d,%d) is outside KitDB's bounded numeric(1..1000,0..precision) profile",
			precision64, scale64,
		)
	}
	return int(precision64), int(scale64), true, nil
}

func postgresTextModifier(dataType, internalType string) (length int, constrained bool, err error) {
	text := strings.ToLower(strings.TrimSpace(dataType))
	open := strings.IndexByte(text, '(')
	if open < 0 {
		validBase := internalType == "varchar" && (text == "varchar" || text == "character varying") ||
			internalType == "bpchar" && (text == "char" || text == "character" || text == "bpchar")
		if !validBase {
			return 0, false, fmt.Errorf("unsupported PostgreSQL character type %q", dataType)
		}
		return 0, false, nil
	}
	close := strings.IndexByte(text[open+1:], ')')
	if close < 0 {
		return 0, false, fmt.Errorf("invalid PostgreSQL character modifier %q", dataType)
	}
	close += open + 1
	if strings.TrimSpace(text[close+1:]) != "" {
		return 0, false, fmt.Errorf("invalid PostgreSQL character modifier %q", dataType)
	}
	base := strings.TrimSpace(text[:open])
	validBase := internalType == "varchar" && (base == "varchar" || base == "character varying") ||
		internalType == "bpchar" && (base == "char" || base == "character" || base == "bpchar")
	if !validBase {
		return 0, false, fmt.Errorf("unsupported PostgreSQL character modifier %q", dataType)
	}
	value, parseErr := strconv.ParseInt(strings.TrimSpace(text[open+1:close]), 10, 32)
	if parseErr != nil || value < 1 || value > kitdbsql.MaximumTextLength {
		return 0, false, fmt.Errorf(
			"PostgreSQL character length in %q is outside KitDB's 1..%d profile",
			dataType, kitdbsql.MaximumTextLength,
		)
	}
	return int(value), true, nil
}

func postgresTemporalModifier(dataType, internalType string) (precision int, constrained bool, err error) {
	text := strings.ToLower(strings.TrimSpace(dataType))
	open := strings.IndexByte(text, '(')
	if open < 0 {
		return 0, false, nil
	}
	close := strings.IndexByte(text[open+1:], ')')
	if close < 0 {
		return 0, false, fmt.Errorf("invalid PostgreSQL temporal modifier %q", dataType)
	}
	close += open + 1
	base := strings.TrimSpace(text[:open])
	if base != internalType && !(internalType == "timestamptz" && base == "timestamp") {
		return 0, false, fmt.Errorf("unsupported PostgreSQL temporal modifier %q", dataType)
	}
	value, parseErr := strconv.ParseInt(strings.TrimSpace(text[open+1:close]), 10, 32)
	if parseErr != nil || value < 0 || value > 6 {
		return 0, false, fmt.Errorf("PostgreSQL temporal precision in %q is outside KitDB's 0..6 profile", dataType)
	}
	return int(value), true, nil
}

func constraintKind(kind string) string {
	switch kind {
	case "p":
		return "primary"
	case "u":
		return "unique"
	case "f":
		return "foreign"
	case "c":
		return "check"
	case "x":
		return "exclusion"
	default:
		return "unknown"
	}
}

func indexLooksFullText(index IndexReport, columns []ColumnReport) bool {
	lower := strings.ToLower(index.Definition + " " + index.Predicate)
	if strings.Contains(lower, "to_tsvector") || strings.Contains(lower, "tsvector_ops") ||
		strings.Contains(lower, "gin_trgm_ops") || strings.Contains(lower, "gist_trgm_ops") {
		return true
	}
	for _, column := range columns {
		if column.InternalType == "tsvector" && strings.Contains(lower, strings.ToLower(column.Name)) {
			return true
		}
	}
	return false
}

func evaluateSchemaReport(report *SchemaReport) {
	for _, column := range report.Columns {
		if column.InternalType == "tsvector" {
			report.FullText.VectorColumns = append(report.FullText.VectorColumns, column.Name)
		}
		switch column.Mapping {
		case "unsupported":
			report.Blockers = append(report.Blockers, fmt.Sprintf("column %q uses unsupported type %s", column.Name, column.DataType))
		case "conditional", "semantic":
			report.Warnings = append(report.Warnings, fmt.Sprintf("column %q needs %s mapping review: %s", column.Name, column.Mapping, column.MappingReason))
		case "derived":
			report.Warnings = append(report.Warnings, fmt.Sprintf("column %q is derived and will be rebuilt instead of copied", column.Name))
		}
	}
	if len(report.PrimaryKey) == 0 {
		report.Blockers = append(report.Blockers, "table has no primary key; resumable keyset migration needs a stable key")
	}
	for _, constraint := range report.Constraints {
		if !constraint.Validated {
			report.Blockers = append(report.Blockers, fmt.Sprintf("constraint %q is not validated", constraint.Name))
		}
		if constraint.Deferrable {
			report.Warnings = append(report.Warnings, fmt.Sprintf("constraint %q is deferrable and needs immediate-semantics review", constraint.Name))
		}
		if constraint.Kind == "exclusion" || constraint.Kind == "unknown" {
			report.Blockers = append(report.Blockers, fmt.Sprintf("constraint %q uses unsupported kind %s", constraint.Name, constraint.Kind))
		}
	}
	report.FullText.Detected = len(report.FullText.VectorColumns) > 0 ||
		len(report.FullText.IndexNames) > 0 || len(report.FullText.TriggerNames) > 0
	report.FullText.RequiresRebuild = report.FullText.Detected
	if report.FullText.Detected {
		report.Warnings = append(report.Warnings, "PostgreSQL full-text structures are derived and must be rebuilt with the Kitwork search engine")
	}
	report.RelationalCompatible = len(report.Blockers) == 0
}
