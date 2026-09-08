package work

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kitwork/engine/value"
)

const (
	sqliteInspectorMaxObjects        = 256
	sqliteInspectorMaxIndexes        = 128
	sqliteInspectorMaxForeignKeys    = 128
	sqliteInspectorMaxForeignKeyRows = 512
	sqliteInspectorMaxPage           = 1_000_000
	sqliteInspectorMaxOffset         = 1_000_000
	sqliteInspectorMaxNameBytes      = 1024
)

type sqliteInspectorSession struct {
	ctx                 context.Context
	conn                *sql.Conn
	cancel              context.CancelFunc
	queryOnlyPrevious   bool
	trustedPrevious     bool
	queryOnlyConfigured bool
	trustedConfigured   bool
	closed              bool
}

type sqliteInspectorObject struct {
	name         string
	kind         string
	columnCount  int
	withoutRowID bool
	strict       bool
}

type sqliteInspectorColumn struct {
	position     int
	name         string
	declaredType string
	notNull      bool
	defaultValue any
	primaryOrder int
	hidden       int
}

type sqliteInspectorForeignKey struct {
	id                int
	columns           []string
	targetTable       string
	targetColumns     []string
	onUpdate          string
	onDelete          string
	foreignKeyRowSeen int
}

// ReadCatalog returns a bounded, fixed-main-schema catalog for a database inspector. It never
// accepts a path, schema name, or SQL fragment from tenant code. Catalog inspection uses one
// read-only SQLite snapshot with trusted schema execution disabled.
func (s *SQLite) ReadCatalog() (result value.Value) {
	session, failure := s.openSQLiteInspectorSession()
	if failure.K != value.Nil {
		return failure
	}
	defer func() {
		if !session.close() {
			result = sqliteReadQueryFailure("DATABASE_UNAVAILABLE", "SQLite connection could not be reset safely")
		}
	}()

	tx, err := session.conn.BeginTx(session.context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return s.sqliteReadQueryContextFailure(err)
	}
	defer tx.Rollback()

	catalogVersion, err := sqliteInspectorSchemaVersion(session.context(), tx)
	if err != nil {
		return s.sqliteReadQueryContextFailure(err)
	}
	objects, err := sqliteInspectorObjects(session.context(), tx)
	if err != nil {
		return s.sqliteReadQueryContextFailure(err)
	}
	if len(objects) > sqliteInspectorMaxObjects {
		return sqliteReadQueryFailure("CATALOG_TOO_LARGE", "SQLite catalog exceeds the 256 object limit")
	}

	catalog := make([]map[string]any, 0, len(objects))
	resultBytes := 0
	for _, object := range objects {
		columns, metadataBytes, err := sqliteInspectorColumns(session.context(), tx, object.name)
		if err != nil {
			return s.sqliteReadQueryContextFailure(err)
		}
		if len(columns) == 0 || len(columns) > sqliteReadQueryMaxColumns {
			return sqliteReadQueryFailure("CATALOG_TOO_LARGE", "SQLite object exceeds the 128 column limit")
		}
		indexes, indexBytes, err := sqliteInspectorIndexes(session.context(), tx, object.name, columns)
		if err != nil {
			return s.sqliteReadQueryContextFailure(err)
		}
		foreignKeys, foreignKeyBytes, err := sqliteInspectorForeignKeys(session.context(), tx, object.name)
		if err != nil {
			return s.sqliteReadQueryContextFailure(err)
		}
		resultBytes += len(object.name)*2 + metadataBytes + indexBytes + foreignKeyBytes + 96
		if resultBytes > sqliteReadQueryMaxResultBytes {
			return sqliteReadQueryFailure("CATALOG_TOO_LARGE", "SQLite catalog exceeds the 2 MiB metadata limit")
		}
		catalog = append(catalog, map[string]any{
			"id":           object.kind + "-" + object.name,
			"name":         object.name,
			"type":         object.kind,
			"columns":      sqliteInspectorColumnMaps(columns),
			"indexes":      indexes,
			"foreignKeys":  foreignKeys,
			"withoutRowid": object.withoutRowID,
			"strict":       object.strict,
		})
	}
	if err := tx.Commit(); err != nil {
		return s.sqliteReadQueryContextFailure(err)
	}
	return value.New(map[string]any{
		"ok":             true,
		"catalogVersion": catalogVersion,
		"objects":        catalog,
	})
}

// ReadTablePage reads one bounded page from a canonical catalog member. The exact object name and
// kind must still exist in the same schema-version snapshot that the client obtained from
// ReadCatalog. Only after that membership check is the canonical identifier quoted by the engine.
func (s *SQLite) ReadTablePage(input value.Value) (result value.Value) {
	request, failure := sqliteInspectorPageRequest(input)
	if failure.K != value.Nil {
		return failure
	}
	session, failure := s.openSQLiteInspectorSession()
	if failure.K != value.Nil {
		return failure
	}
	defer func() {
		if !session.close() {
			result = sqliteReadQueryFailure("DATABASE_UNAVAILABLE", "SQLite connection could not be reset safely")
		}
	}()

	started := time.Now()
	tx, err := session.conn.BeginTx(session.context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return s.sqliteReadQueryContextFailure(err)
	}
	defer tx.Rollback()

	catalogVersion, err := sqliteInspectorSchemaVersion(session.context(), tx)
	if err != nil {
		return s.sqliteReadQueryContextFailure(err)
	}
	if request.catalogVersion != catalogVersion {
		return sqliteReadQueryFailure("SCHEMA_CHANGED", "SQLite schema changed; refresh the catalog and try again")
	}
	object, found, err := sqliteInspectorFindObject(session.context(), tx, request.name, request.kind)
	if err != nil {
		return s.sqliteReadQueryContextFailure(err)
	}
	if !found {
		return sqliteReadQueryFailure("OBJECT_NOT_FOUND", "SQLite table or view was not found in the current catalog")
	}
	columns, metadataBytes, err := sqliteInspectorColumns(session.context(), tx, object.name)
	if err != nil {
		return s.sqliteReadQueryContextFailure(err)
	}
	if len(columns) == 0 || len(columns) > sqliteReadQueryMaxColumns {
		return sqliteReadQueryFailure("RESULT_TOO_LARGE", "SQLite object exceeds the 128 column limit")
	}

	quotedObject := sqliteInspectorQuoteIdentifier(object.name)
	var totalRows int64
	if err := tx.QueryRowContext(session.context(), "SELECT COUNT(*) FROM main."+quotedObject).Scan(&totalRows); err != nil {
		return s.sqliteReadQueryContextFailure(err)
	}
	if totalRows < 0 || totalRows > sqliteJavaScriptMaxSafeInt {
		return sqliteReadQueryFailure("RESULT_TOO_LARGE", "SQLite row count exceeds the supported numeric range")
	}

	columnSQL := make([]string, len(columns))
	primary := make([]sqliteInspectorColumn, 0, len(columns))
	for index, column := range columns {
		columnSQL[index] = sqliteInspectorQuoteIdentifier(column.name)
		if column.primaryOrder > 0 {
			primary = append(primary, column)
		}
	}
	sort.SliceStable(primary, func(left, right int) bool {
		return primary[left].primaryOrder < primary[right].primaryOrder
	})
	orderSQL := ""
	if object.kind == "table" {
		if len(primary) == 0 {
			rowID, available := sqliteInspectorRowIDAlias(columns)
			if !available {
				return sqliteReadQueryFailure("UNSTABLE_ORDER", "SQLite table shadows every rowid alias and has no primary key")
			}
			orderSQL = " ORDER BY " + rowID
		} else {
			parts := make([]string, len(primary))
			for index, column := range primary {
				parts[index] = sqliteInspectorQuoteIdentifier(column.name)
			}
			orderSQL = " ORDER BY " + strings.Join(parts, ", ")
		}
	}
	offset := int64(request.page-1) * int64(request.pageSize)
	query := "SELECT " + strings.Join(columnSQL, ", ") + " FROM main." + quotedObject + orderSQL + " LIMIT ? OFFSET ?"
	rows, err := tx.QueryContext(session.context(), query, request.pageSize, offset)
	if err != nil {
		return s.sqliteReadQueryContextFailure(err)
	}
	defer rows.Close()

	resultRows := make([][]any, 0, request.pageSize)
	resultBytes := metadataBytes
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			return s.sqliteReadQueryContextFailure(err)
		}
		for index, raw := range values {
			cell, size, ok := sqliteReadQueryCell(raw)
			if !ok || resultBytes+size > sqliteReadQueryMaxResultBytes {
				return sqliteReadQueryFailure("RESULT_TOO_LARGE", "SQLite page exceeds the 2 MiB result limit")
			}
			values[index] = cell
			resultBytes += size
		}
		resultRows = append(resultRows, values)
	}
	if err := rows.Err(); err != nil {
		return s.sqliteReadQueryContextFailure(err)
	}
	if err := rows.Close(); err != nil {
		return s.sqliteReadQueryContextFailure(err)
	}

	pageCount := int64(0)
	if totalRows > 0 {
		pageCount = (totalRows + int64(request.pageSize) - 1) / int64(request.pageSize)
	}
	pageStart := int64(0)
	pageEnd := int64(0)
	if len(resultRows) > 0 {
		pageStart = offset + 1
		pageEnd = offset + int64(len(resultRows))
	}
	if err := tx.Commit(); err != nil {
		return s.sqliteReadQueryContextFailure(err)
	}
	return value.New(map[string]any{
		"ok":              true,
		"catalogVersion":  catalogVersion,
		"name":            object.name,
		"type":            object.kind,
		"columns":         sqliteInspectorColumnMaps(columns),
		"rows":            resultRows,
		"totalRows":       totalRows,
		"page":            request.page,
		"pageSize":        request.pageSize,
		"pageCount":       pageCount,
		"pageStart":       pageStart,
		"pageEnd":         pageEnd,
		"hasPreviousPage": pageCount > 0 && int64(request.page) > 1,
		"hasNextPage":     int64(request.page) < pageCount,
		"durationMs":      time.Since(started).Milliseconds(),
	})
}

type sqliteInspectorPageInput struct {
	kind           string
	name           string
	catalogVersion int64
	page           int
	pageSize       int
}

func sqliteInspectorPageRequest(input value.Value) (sqliteInspectorPageInput, value.Value) {
	invalid := func() (sqliteInspectorPageInput, value.Value) {
		return sqliteInspectorPageInput{}, sqliteReadQueryFailure("INVALID_REQUEST", "Expected exactly type, name, catalogVersion, page, and pageSize")
	}
	if input.K != value.Map {
		return invalid()
	}
	fields := input.Map()
	if len(fields) != 5 {
		return invalid()
	}
	kind, kindOK := fields["type"]
	name, nameOK := fields["name"]
	version, versionOK := fields["catalogVersion"]
	page, pageOK := fields["page"]
	pageSize, pageSizeOK := fields["pageSize"]
	if !kindOK || !nameOK || !versionOK || !pageOK || !pageSizeOK || kind.K != value.String || name.K != value.String {
		return invalid()
	}
	kindText := kind.String()
	nameText := name.String()
	if kindText != "table" && kindText != "view" || nameText == "" || len(nameText) > sqliteInspectorMaxNameBytes || !utf8.ValidString(nameText) || strings.ContainsRune(nameText, '\x00') {
		return invalid()
	}
	versionNumber, ok := sqliteInspectorInteger(version, 0, math.MaxInt32)
	if !ok {
		return invalid()
	}
	pageNumber, ok := sqliteInspectorInteger(page, 1, sqliteInspectorMaxPage)
	if !ok {
		return invalid()
	}
	pageSizeNumber, ok := sqliteInspectorInteger(pageSize, 1, sqliteReadQueryMaxRows)
	if !ok {
		return invalid()
	}
	if int64(pageNumber-1)*int64(pageSizeNumber) > sqliteInspectorMaxOffset {
		return invalid()
	}
	return sqliteInspectorPageInput{
		kind:           kindText,
		name:           nameText,
		catalogVersion: int64(versionNumber),
		page:           pageNumber,
		pageSize:       pageSizeNumber,
	}, value.Value{K: value.Nil}
}

func sqliteInspectorInteger(input value.Value, minimum, maximum int) (int, bool) {
	if input.K != value.Number || math.IsNaN(input.N) || math.IsInf(input.N, 0) || math.Trunc(input.N) != input.N {
		return 0, false
	}
	integer := int64(input.N)
	if integer < int64(minimum) || integer > int64(maximum) {
		return 0, false
	}
	return int(integer), true
}

func (s *SQLite) openSQLiteInspectorSession() (*sqliteInspectorSession, value.Value) {
	parent := context.Background()
	if s.requestScope != nil {
		parent = s.requestScope.Context()
	}
	ctx, cancel := context.WithTimeout(parent, sqliteReadQueryTimeout)
	db := s.db()
	if db == nil {
		cancel()
		return nil, sqliteReadQueryFailure("DATABASE_UNAVAILABLE", "SQLite database is unavailable")
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		cancel()
		return nil, s.sqliteReadQueryContextFailure(err)
	}
	session := &sqliteInspectorSession{ctx: ctx, conn: conn, cancel: cancel}
	var queryOnly int
	if err := conn.QueryRowContext(ctx, "PRAGMA query_only").Scan(&queryOnly); err != nil {
		conn.Close()
		cancel()
		return nil, s.sqliteReadQueryContextFailure(err)
	}
	var trustedSchema int
	if err := conn.QueryRowContext(ctx, "PRAGMA trusted_schema").Scan(&trustedSchema); err != nil {
		conn.Close()
		cancel()
		return nil, s.sqliteReadQueryContextFailure(err)
	}
	session.queryOnlyPrevious = queryOnly != 0
	session.trustedPrevious = trustedSchema != 0
	// Mark each policy as needing restoration before attempting its PRAGMA. An execution error does
	// not prove SQLite left the connection unchanged, so the setup failure path must reset or discard
	// the connection just like an ordinary completed inspection.
	session.queryOnlyConfigured = true
	if _, err := conn.ExecContext(ctx, "PRAGMA query_only=ON"); err != nil {
		if !session.close() {
			return nil, sqliteReadQueryFailure("DATABASE_UNAVAILABLE", "SQLite connection could not be reset safely")
		}
		return nil, s.sqliteReadQueryContextFailure(err)
	}
	session.trustedConfigured = true
	if _, err := conn.ExecContext(ctx, "PRAGMA trusted_schema=OFF"); err != nil {
		if !session.close() {
			return nil, sqliteReadQueryFailure("DATABASE_UNAVAILABLE", "SQLite connection could not be reset safely")
		}
		return nil, s.sqliteReadQueryContextFailure(err)
	}
	return session, value.Value{K: value.Nil}
}

func (s *sqliteInspectorSession) context() context.Context {
	return s.ctx
}

func (s *sqliteInspectorSession) close() bool {
	if s == nil || s.closed {
		return true
	}
	s.closed = true
	resetCtx, resetCancel := context.WithTimeout(context.Background(), sqliteReadQueryResetTimeout)
	defer resetCancel()
	resetOK := true
	if s.trustedConfigured {
		statement := "PRAGMA trusted_schema=OFF"
		if s.trustedPrevious {
			statement = "PRAGMA trusted_schema=ON"
		}
		if _, err := s.conn.ExecContext(resetCtx, statement); err != nil {
			resetOK = false
		}
	}
	if s.queryOnlyConfigured {
		statement := "PRAGMA query_only=OFF"
		if s.queryOnlyPrevious {
			statement = "PRAGMA query_only=ON"
		}
		if _, err := s.conn.ExecContext(resetCtx, statement); err != nil {
			resetOK = false
		}
	}
	if !resetOK {
		_ = s.conn.Raw(func(any) error { return driver.ErrBadConn })
	}
	_ = s.conn.Close()
	s.cancel()
	return resetOK
}

func sqliteInspectorSchemaVersion(ctx context.Context, tx *sql.Tx) (int64, error) {
	var version int64
	if err := tx.QueryRowContext(ctx, "PRAGMA main.schema_version").Scan(&version); err != nil {
		return 0, err
	}
	if version < 0 || version > math.MaxInt32 {
		return 0, fmt.Errorf("sqlite: unsupported schema version")
	}
	return version, nil
}

func sqliteInspectorObjects(ctx context.Context, tx *sql.Tx) ([]sqliteInspectorObject, error) {
	rows, err := tx.QueryContext(ctx, "PRAGMA main.table_list")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	objects := make([]sqliteInspectorObject, 0, 16)
	for rows.Next() {
		var schema, name, kind string
		var columnCount, withoutRowID, strict int
		if err := rows.Scan(&schema, &name, &kind, &columnCount, &withoutRowID, &strict); err != nil {
			return nil, err
		}
		if schema != "main" || kind != "table" && kind != "view" || sqliteInspectorInternalName(name) {
			continue
		}
		if len(name) > sqliteInspectorMaxNameBytes || !utf8.ValidString(name) || strings.ContainsRune(name, '\x00') {
			return nil, fmt.Errorf("sqlite: catalog contains an unsupported object name")
		}
		objects = append(objects, sqliteInspectorObject{
			name:         name,
			kind:         kind,
			columnCount:  columnCount,
			withoutRowID: withoutRowID != 0,
			strict:       strict != 0,
		})
		if len(objects) > sqliteInspectorMaxObjects {
			return objects, nil
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(objects, func(left, right int) bool {
		if objects[left].kind != objects[right].kind {
			return objects[left].kind == "table"
		}
		leftName := strings.ToLower(objects[left].name)
		rightName := strings.ToLower(objects[right].name)
		if leftName == rightName {
			return objects[left].name < objects[right].name
		}
		return leftName < rightName
	})
	return objects, nil
}

func sqliteInspectorFindObject(ctx context.Context, tx *sql.Tx, requestedName, requestedKind string) (sqliteInspectorObject, bool, error) {
	if sqliteInspectorInternalName(requestedName) {
		return sqliteInspectorObject{}, false, nil
	}
	objects, err := sqliteInspectorObjects(ctx, tx)
	if err != nil {
		return sqliteInspectorObject{}, false, err
	}
	if len(objects) > sqliteInspectorMaxObjects {
		return sqliteInspectorObject{}, false, fmt.Errorf("sqlite: catalog exceeds object limit")
	}
	for _, object := range objects {
		if object.name == requestedName && object.kind == requestedKind {
			return object, true, nil
		}
	}
	return sqliteInspectorObject{}, false, nil
}

func sqliteInspectorColumns(ctx context.Context, tx *sql.Tx, objectName string) ([]sqliteInspectorColumn, int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT cid, name, type, "notnull", dflt_value, pk, hidden FROM pragma_table_xinfo(?, 'main') ORDER BY cid`, objectName)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	columns := make([]sqliteInspectorColumn, 0, 16)
	metadataBytes := 0
	for rows.Next() {
		var position, notNull, primaryOrder, hidden int
		var name, declaredType string
		var defaultValue any
		if err := rows.Scan(&position, &name, &declaredType, &notNull, &defaultValue, &primaryOrder, &hidden); err != nil {
			return nil, 0, err
		}
		// hidden=1 is a virtual-table-only implementation column. Virtual/shadow objects are excluded,
		// but keep this guard so a future SQLite table-list change cannot expose it accidentally.
		if hidden == 1 {
			continue
		}
		if len(name) > sqliteInspectorMaxNameBytes || len(declaredType) > sqliteInspectorMaxNameBytes || !utf8.ValidString(name) || !utf8.ValidString(declaredType) {
			return nil, 0, fmt.Errorf("sqlite: catalog contains oversized or invalid column metadata")
		}
		if text, ok := defaultValue.(string); ok {
			if len(text) > sqliteReadQueryMaxCellBytes || !utf8.ValidString(text) {
				return nil, 0, fmt.Errorf("sqlite: catalog contains oversized or invalid default metadata")
			}
			metadataBytes += len(text)
		}
		metadataBytes += len(name) + len(declaredType) + 48
		columns = append(columns, sqliteInspectorColumn{
			position:     position,
			name:         name,
			declaredType: declaredType,
			notNull:      notNull != 0,
			defaultValue: defaultValue,
			primaryOrder: primaryOrder,
			hidden:       hidden,
		})
		if len(columns) > sqliteReadQueryMaxColumns || metadataBytes > sqliteReadQueryMaxResultBytes {
			return columns, metadataBytes, nil
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return columns, metadataBytes, nil
}

func sqliteInspectorColumnMaps(columns []sqliteInspectorColumn) []map[string]any {
	result := make([]map[string]any, len(columns))
	for index, column := range columns {
		declaredType := strings.ToUpper(strings.TrimSpace(column.declaredType))
		if declaredType == "" {
			declaredType = "ANY"
		}
		defaultValue := any("—")
		if column.defaultValue != nil {
			defaultValue = fmt.Sprint(column.defaultValue)
		}
		keyLabel := ""
		if column.primaryOrder > 0 {
			keyLabel = "PRIMARY KEY"
		}
		result[index] = map[string]any{
			"key":          column.name,
			"position":     index + 1,
			"name":         column.name,
			"type":         declaredType,
			"nullable":     map[bool]string{true: "No", false: "Yes"}[column.notNull || column.primaryOrder > 0],
			"keyLabel":     keyLabel,
			"defaultValue": defaultValue,
			"primaryOrder": column.primaryOrder,
			"generated":    column.hidden == 2 || column.hidden == 3,
		}
	}
	return result
}

func sqliteInspectorIndexes(ctx context.Context, tx *sql.Tx, objectName string, columns []sqliteInspectorColumn) ([]map[string]any, int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT seq, name, "unique", origin, partial FROM pragma_index_list(?, 'main') ORDER BY seq`, objectName)
	if err != nil {
		return nil, 0, err
	}
	type listedIndex struct {
		name    string
		unique  bool
		origin  string
		partial bool
	}
	listed := make([]listedIndex, 0, 8)
	primaryListed := false
	for rows.Next() {
		var sequence, unique, partial int
		var name, origin string
		if err := rows.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
			rows.Close()
			return nil, 0, err
		}
		if len(name) > sqliteInspectorMaxNameBytes || !utf8.ValidString(name) {
			rows.Close()
			return nil, 0, fmt.Errorf("sqlite: catalog contains an unsupported index name")
		}
		if origin == "pk" {
			primaryListed = true
		}
		listed = append(listed, listedIndex{name: name, unique: unique != 0, origin: origin, partial: partial != 0})
		if len(listed) > sqliteInspectorMaxIndexes {
			rows.Close()
			return nil, 0, fmt.Errorf("sqlite: object exceeds index limit")
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, 0, err
	}
	if err := rows.Close(); err != nil {
		return nil, 0, err
	}

	result := make([]map[string]any, 0, len(listed)+1)
	metadataBytes := 0
	primaryColumns := make([]sqliteInspectorColumn, 0, len(columns))
	for _, column := range columns {
		if column.primaryOrder > 0 {
			primaryColumns = append(primaryColumns, column)
		}
	}
	sort.SliceStable(primaryColumns, func(left, right int) bool {
		return primaryColumns[left].primaryOrder < primaryColumns[right].primaryOrder
	})
	if len(primaryColumns) > 0 && !primaryListed {
		names := make([]string, len(primaryColumns))
		for index, column := range primaryColumns {
			names[index] = column.name
		}
		indexName := objectName + "_pkey"
		result = append(result, map[string]any{
			"name": indexName, "columns": strings.Join(names, ", "), "kind": "Primary key", "method": "btree", "unique": true, "partial": false,
		})
		metadataBytes += len(indexName) + len(strings.Join(names, ", ")) + 48
	}
	for _, index := range listed {
		columnRows, err := tx.QueryContext(ctx, `SELECT seqno, cid, name, "key" FROM pragma_index_xinfo(?, 'main') ORDER BY seqno`, index.name)
		if err != nil {
			return nil, 0, err
		}
		columnNames := make([]string, 0, 4)
		for columnRows.Next() {
			var sequence, columnID, keyColumn int
			var name sql.NullString
			if err := columnRows.Scan(&sequence, &columnID, &name, &keyColumn); err != nil {
				columnRows.Close()
				return nil, 0, err
			}
			if keyColumn == 0 {
				continue
			}
			columnName := "<expression>"
			if name.Valid {
				columnName = name.String
			}
			if len(columnName) > sqliteInspectorMaxNameBytes || !utf8.ValidString(columnName) {
				columnRows.Close()
				return nil, 0, fmt.Errorf("sqlite: catalog contains unsupported index metadata")
			}
			columnNames = append(columnNames, columnName)
			if len(columnNames) > sqliteReadQueryMaxColumns {
				columnRows.Close()
				return nil, 0, fmt.Errorf("sqlite: index exceeds column limit")
			}
		}
		if err := columnRows.Err(); err != nil {
			columnRows.Close()
			return nil, 0, err
		}
		if err := columnRows.Close(); err != nil {
			return nil, 0, err
		}
		kind := "Index"
		if index.origin == "pk" {
			kind = "Primary key"
		} else if index.unique {
			kind = "Unique"
		}
		columnText := strings.Join(columnNames, ", ")
		result = append(result, map[string]any{
			"name": index.name, "columns": columnText, "kind": kind, "method": "btree", "unique": index.unique, "partial": index.partial,
		})
		metadataBytes += len(index.name) + len(columnText) + len(kind) + 32
	}
	return result, metadataBytes, nil
}

func sqliteInspectorForeignKeys(ctx context.Context, tx *sql.Tx, objectName string) ([]map[string]any, int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, seq, "table", "from", "to", on_update, on_delete FROM pragma_foreign_key_list(?, 'main') ORDER BY id, seq`, objectName)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	groups := make([]sqliteInspectorForeignKey, 0, 4)
	byID := make(map[int]int)
	rowCount := 0
	for rows.Next() {
		var id, sequence int
		var targetTable, sourceColumn, onUpdate, onDelete string
		var targetColumn sql.NullString
		if err := rows.Scan(&id, &sequence, &targetTable, &sourceColumn, &targetColumn, &onUpdate, &onDelete); err != nil {
			return nil, 0, err
		}
		rowCount++
		if rowCount > sqliteInspectorMaxForeignKeyRows {
			return nil, 0, fmt.Errorf("sqlite: object exceeds foreign-key metadata limit")
		}
		for _, text := range []string{targetTable, sourceColumn, targetColumn.String, onUpdate, onDelete} {
			if len(text) > sqliteInspectorMaxNameBytes || !utf8.ValidString(text) {
				return nil, 0, fmt.Errorf("sqlite: catalog contains unsupported foreign-key metadata")
			}
		}
		groupIndex, exists := byID[id]
		if !exists {
			if len(groups) == sqliteInspectorMaxForeignKeys {
				return nil, 0, fmt.Errorf("sqlite: object exceeds foreign-key limit")
			}
			groupIndex = len(groups)
			byID[id] = groupIndex
			groups = append(groups, sqliteInspectorForeignKey{id: id, targetTable: targetTable, onUpdate: strings.ToUpper(onUpdate), onDelete: strings.ToUpper(onDelete)})
		}
		groups[groupIndex].columns = append(groups[groupIndex].columns, sourceColumn)
		if targetColumn.Valid {
			groups[groupIndex].targetColumns = append(groups[groupIndex].targetColumns, targetColumn.String)
		}
		groups[groupIndex].foreignKeyRowSeen++
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	result := make([]map[string]any, len(groups))
	metadataBytes := 0
	for index, group := range groups {
		name := fmt.Sprintf("fk_%s_%d", objectName, group.id)
		columns := strings.Join(group.columns, ", ")
		targetColumns := strings.Join(group.targetColumns, ", ")
		target := "main." + group.targetTable
		if targetColumns != "" {
			target += " (" + targetColumns + ")"
		}
		result[index] = map[string]any{
			"name": name, "columns": columns, "target": target, "onUpdate": group.onUpdate, "onDelete": group.onDelete,
			"targetTable": group.targetTable, "targetColumns": targetColumns,
		}
		metadataBytes += len(name) + len(columns) + len(target) + len(group.onUpdate) + len(group.onDelete) + 48
	}
	return result, metadataBytes, nil
}

func sqliteInspectorQuoteIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func sqliteInspectorRowIDAlias(columns []sqliteInspectorColumn) (string, bool) {
	shadowed := make(map[string]bool, 3)
	for _, column := range columns {
		shadowed[strings.ToLower(column.name)] = true
	}
	for _, candidate := range []string{"rowid", "_rowid_", "oid"} {
		if !shadowed[candidate] {
			// These are engine-owned fixed tokens, not caller-supplied identifiers. They must remain
			// unquoted so SQLite resolves the hidden row identifier rather than a string literal.
			return candidate, true
		}
	}
	return "", false
}

func sqliteInspectorInternalName(name string) bool {
	return strings.HasPrefix(strings.ToLower(name), "sqlite_")
}
