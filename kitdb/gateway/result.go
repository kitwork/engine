package gateway

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kitwork/engine/kitdb/pgwire"
	sqlite "modernc.org/sqlite"
)

// PostgreSQL types the gateway answers with that pgwire does not name.
const (
	oidJSON              = pgwire.OIDJSON
	numericHeaderSize    = 4  // VARHDRSZ: PostgreSQL adds it to numeric(p,s)'s type modifier
	defaultDecimalDigits = 18 // the precision of a decimal whose precision is not known
)

// resultColumn is what the gateway knows of one result column before the values: its name, its kind from Columns
// (decimal, bool, json), else its declared type's affinity, else nothing (the values decide).
type resultColumn struct {
	name string
	// fixed is the type Columns gave (numeric, bool, json): values are rendered for it and it never changes.
	fixed uint32
	scale int
	// declared is the type the declared column type gives (int8, float8, text, bytea): it falls back to text when
	// a value does not fit it (SQLite lets a column hold any value).
	declared uint32
	modifier int32
}

// valueClass is the class of one SQLite value.
type valueClass int

const (
	classNull valueClass = iota
	classInteger
	classReal
	classText
	classBlob
)

func (column resultColumn) describe() pgwire.Column {
	oid := column.fixed
	if oid == 0 {
		oid = column.declared
	}
	if oid == 0 {
		oid = pgwire.OIDText
	}
	return pgwire.Column{Name: column.name, DataTypeOID: oid, DataTypeSize: typeSize(oid), TypeModifier: column.modifierFor(oid)}
}

func (column resultColumn) modifierFor(oid uint32) int32 {
	if oid == pgwire.OIDNumeric {
		return column.modifier
	}
	return 0
}

func typeSize(oid uint32) int16 {
	switch oid {
	case pgwire.OIDInt8, pgwire.OIDFloat8:
		return 8
	case pgwire.OIDInt4, pgwire.OIDOID, pgwire.OIDFloat4, oidXID, oidRegproc:
		return 4
	case pgwire.OIDInt2:
		return 2
	case pgwire.OIDBool, oidChar:
		return 1
	case oidName:
		return 64
	}
	return -1
}

// declaredType maps a declared column type to a PostgreSQL type by SQLite's affinity rules; 0 for NUMERIC affinity
// and no declared type, where only the values say what they are.
func declaredType(declared string) uint32 {
	upper := strings.ToUpper(declared)
	switch {
	case upper == "":
		return 0
	case strings.Contains(upper, "INT"):
		return pgwire.OIDInt8
	case strings.Contains(upper, "CHAR"), strings.Contains(upper, "CLOB"), strings.Contains(upper, "TEXT"):
		return pgwire.OIDText
	case strings.Contains(upper, "BLOB"):
		return pgwire.OIDBytea
	case strings.Contains(upper, "REAL"), strings.Contains(upper, "FLOA"), strings.Contains(upper, "DOUB"):
		return pgwire.OIDFloat8
	}
	return 0
}

// newResultColumn types a column from its source (schema, table, column; empty for an expression) and declared type.
func (session *Session) newResultColumn(name, declared, schema, table, origin string) resultColumn {
	column := resultColumn{name: name, declared: declaredType(declared)}
	if schema == "temp" {
		// A column of the catalog (catalog.go) answers with its PostgreSQL type: oid, name, bool, int2vector, ….
		if oid, known := catalogColumnType(table, origin); known {
			column.fixed = oid
		}
		return column
	}
	if session.columns == nil || table == "" || origin == "" {
		return column
	}
	kind, precision, scale, found := session.columns.Column(schema, table, origin)
	if !found {
		return column
	}
	switch kind {
	case "decimal":
		if precision <= 0 {
			precision = defaultDecimalDigits
		}
		if scale < 0 {
			scale = 0
		}
		precision = max(precision, scale)
		column.fixed, column.scale = pgwire.OIDNumeric, scale
		column.modifier = int32(precision<<16|scale) + numericHeaderSize
	case "bool":
		column.fixed = pgwire.OIDBool
	case "json":
		column.fixed = oidJSON
	}
	return column
}

// describeColumns types the result columns of a read statement from SQLite's column metadata, without running it
// (the columns of EXPLAIN have no source: their values decide).
func (session *Session) describeColumns(parsed statement) ([]resultColumn, error) {
	var information []sqlite.ColumnInfo
	err := session.connection.Raw(func(driverConnection any) error {
		describer, ok := driverConnection.(interface {
			ColumnInfo(query string) ([]sqlite.ColumnInfo, error)
		})
		if !ok {
			return nil
		}
		var err error
		information, err = describer.ColumnInfo(parsed.text)
		return err
	})
	if err != nil {
		return nil, sqliteError(err)
	}
	if information == nil {
		return nil, nil
	}
	columns := make([]resultColumn, len(information))
	for index, column := range information {
		columns[index] = session.newResultColumn(column.Name, column.DeclType, column.DatabaseName, column.TableName, column.OriginName)
	}
	return columns, nil
}

// read authorizes a read statement inside a read transaction (the statement's own when no block is open), then
// either describes its columns or runs it.
func (session *Session) read(
	ctx context.Context,
	parsed statement,
	parameters []pgwire.Parameter,
	describeOnly bool,
) (result pgwire.Result, described []pgwire.Column, err error) {
	arguments, err := bindParameters(parameters)
	if err != nil {
		return pgwire.Result{}, nil, err
	}
	if session.transaction == pgwire.TransactionIdle {
		if err := session.refreshCatalog(ctx); err != nil {
			return pgwire.Result{}, nil, err
		}
		if _, err := session.connection.ExecContext(ctx, "BEGIN"); err != nil {
			return pgwire.Result{}, nil, sqliteError(err)
		}
		defer func() {
			// A read transaction keeps nothing: ROLLBACK ends it whether the statement succeeded or not.
			_, _ = session.connection.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
		}()
	}
	if err := session.refreshSchemas(ctx); err != nil {
		return pgwire.Result{}, nil, err
	}
	if err := session.authorize(ctx, parsed, arguments); err != nil {
		return pgwire.Result{}, nil, err
	}
	columns, err := session.describeColumns(parsed)
	if err != nil {
		return pgwire.Result{}, nil, err
	}
	if describeOnly {
		described = make([]pgwire.Column, len(columns))
		for index, column := range columns {
			described[index] = column.describe()
		}
		return pgwire.Result{}, described, nil
	}
	result, err = session.run(ctx, parsed, arguments, columns)
	return result, nil, err
}

// run runs a read statement and renders its values, stopping at MaxRows rows or MaxBytes bytes.
func (session *Session) run(ctx context.Context, parsed statement, arguments []any, columns []resultColumn) (pgwire.Result, error) {
	rows, err := session.connection.QueryContext(ctx, parsed.text, arguments...)
	if err != nil {
		return pgwire.Result{}, sqliteError(err)
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		return pgwire.Result{}, sqliteError(err)
	}
	if len(columns) != len(names) {
		types, _ := rows.ColumnTypes()
		columns = make([]resultColumn, len(names))
		for index, name := range names {
			declared := ""
			if index < len(types) {
				declared = types[index].DatabaseTypeName()
			}
			columns[index] = session.newResultColumn(name, declared, "", "", "")
		}
	}
	seen := make([][classBlob + 1]bool, len(columns))
	values := make([]any, len(columns))
	pointers := make([]any, len(columns))
	for index := range values {
		pointers[index] = &values[index]
	}
	var result pgwire.Result
	var bytes int64
	for rows.Next() {
		if len(result.Rows) >= session.options.MaxRows {
			return pgwire.Result{}, pgwire.NewError("54000", fmt.Sprintf("result exceeds %d rows", session.options.MaxRows))
		}
		if err := rows.Scan(pointers...); err != nil {
			return pgwire.Result{}, sqliteError(err)
		}
		row := make([]pgwire.Field, len(columns))
		for index, value := range values {
			class := classify(value)
			seen[index][class] = true
			row[index] = render(value, class, columns[index])
			bytes += int64(len(row[index].Data))
		}
		if bytes > session.options.MaxBytes {
			return pgwire.Result{}, pgwire.NewError("54000", fmt.Sprintf("result exceeds %d bytes", session.options.MaxBytes))
		}
		result.Rows = append(result.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return pgwire.Result{}, sqliteError(err)
	}
	result.Columns = make([]pgwire.Column, len(columns))
	for index, column := range columns {
		result.Columns[index] = finalColumn(column, seen[index])
	}
	result.CommandTag = fmt.Sprintf("SELECT %d", len(result.Rows))
	if parsed.kind == statementExplain {
		result.CommandTag = "EXPLAIN"
	}
	return result, nil
}

// finalColumn decides a column's type once its values are known: Columns' kind, else the declared type when every
// value fits it, else what the values are (all integers int8, numbers float8, all blobs bytea, anything else text).
func finalColumn(column resultColumn, seen [classBlob + 1]bool) pgwire.Column {
	if column.fixed != 0 {
		return column.describe()
	}
	fits := func(classes ...valueClass) bool {
		for class := classInteger; class <= classBlob; class++ {
			if !seen[class] {
				continue
			}
			allowed := false
			for _, candidate := range classes {
				allowed = allowed || candidate == class
			}
			if !allowed {
				return false
			}
		}
		return true
	}
	oid := pgwire.OIDText
	switch column.declared {
	case pgwire.OIDInt8:
		if fits(classInteger) {
			oid = pgwire.OIDInt8
		}
	case pgwire.OIDFloat8:
		if fits(classInteger, classReal) {
			oid = pgwire.OIDFloat8
		}
	case pgwire.OIDBytea:
		if fits(classBlob) {
			oid = pgwire.OIDBytea
		}
	case 0:
		switch {
		case seen[classInteger] && fits(classInteger):
			oid = pgwire.OIDInt8
		case (seen[classInteger] || seen[classReal]) && fits(classInteger, classReal):
			oid = pgwire.OIDFloat8
		case seen[classBlob] && fits(classBlob):
			oid = pgwire.OIDBytea
		}
	}
	return pgwire.Column{Name: column.name, DataTypeOID: oid, DataTypeSize: typeSize(oid)}
}

func classify(value any) valueClass {
	switch value.(type) {
	case nil:
		return classNull
	case int64:
		return classInteger
	case float64:
		return classReal
	case []byte:
		return classBlob
	}
	return classText
}

// render is one value in PostgreSQL's text format for its column.
func render(value any, class valueClass, column resultColumn) pgwire.Field {
	if class == classNull {
		return pgwire.Field{Null: true}
	}
	switch column.fixed {
	case pgwire.OIDNumeric:
		if text, ok := decimalText(value, column.scale); ok {
			return pgwire.Field{Data: []byte(text)}
		}
	case pgwire.OIDBool:
		if text, ok := booleanText(value); ok {
			return pgwire.Field{Data: []byte(text)}
		}
	}
	return pgwire.Field{Data: []byte(plainText(value))}
}

// plainText is a value as PostgreSQL writes its own type: integers in decimal, floats shortest-exact, blobs \x hex.
func plainText(value any) string {
	switch typed := value.(type) {
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		switch {
		case math.IsInf(typed, 1):
			return "Infinity"
		case math.IsInf(typed, -1):
			return "-Infinity"
		case math.IsNaN(typed):
			return "NaN"
		}
		return strconv.FormatFloat(typed, 'g', -1, 64)
	case []byte:
		return `\x` + hex.EncodeToString(typed)
	case string:
		return typed
	case time.Time:
		// modernc turns the text of a DATE, DATETIME or TIMESTAMP column into a time; it goes back as RFC 3339.
		return typed.Format(time.RFC3339Nano)
	}
	return fmt.Sprint(value)
}

var decimalPattern = regexp.MustCompile(`^([+-]?)([0-9]*)(?:\.([0-9]*))?$`)

// decimalText is a decimal of the given scale: the file stores it as the integer × 10^scale (450 at scale 2 is
// 4.50); a value written as text by an older file keeps its digits, padded to the scale.
func decimalText(value any, scale int) (string, bool) {
	switch typed := value.(type) {
	case int64:
		return formatScaled(typed, scale), true
	case float64:
		if math.IsInf(typed, 0) || math.IsNaN(typed) {
			return "", false
		}
		return strconv.FormatFloat(typed/math.Pow10(scale), 'f', scale, 64), true
	case string:
		parts := decimalPattern.FindStringSubmatch(strings.TrimSpace(typed))
		if parts == nil || (parts[2] == "" && parts[3] == "") {
			return "", false
		}
		whole, fraction := parts[2], parts[3]
		if whole == "" {
			whole = "0"
		}
		if len(fraction) < scale {
			fraction += strings.Repeat("0", scale-len(fraction))
		}
		text := whole
		if fraction != "" {
			text += "." + fraction
		}
		if parts[1] == "-" {
			text = "-" + text
		}
		return text, true
	}
	return "", false
}

// formatScaled writes value / 10^scale with exactly scale digits after the point.
func formatScaled(value int64, scale int) string {
	if scale <= 0 {
		return strconv.FormatInt(value, 10)
	}
	negative := value < 0
	magnitude := uint64(value)
	if negative {
		magnitude = uint64(-(value + 1)) + 1
	}
	digits := strconv.FormatUint(magnitude, 10)
	if len(digits) <= scale {
		digits = strings.Repeat("0", scale-len(digits)+1) + digits
	}
	text := digits[:len(digits)-scale] + "." + digits[len(digits)-scale:]
	if negative {
		text = "-" + text
	}
	return text
}

// booleanText is a boolean the file stores as 0 or 1 (or as text) in PostgreSQL's t/f.
func booleanText(value any) (string, bool) {
	switch typed := value.(type) {
	case int64:
		return map[bool]string{true: "t", false: "f"}[typed != 0], true
	case float64:
		return map[bool]string{true: "t", false: "f"}[typed != 0], true
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "t", "true", "1", "yes", "on":
			return "t", true
		case "f", "false", "0", "no", "off":
			return "f", true
		}
	}
	return "", false
}

// bindParameters turns the client's parameters into SQLite values, bound by position to $1…$n. A parameter without
// a type (OID 0, as lib/pq sends) or a text one stays text, so SQLite's affinity compares it as the column wants.
func bindParameters(parameters []pgwire.Parameter) ([]any, error) {
	arguments := make([]any, len(parameters))
	for index, parameter := range parameters {
		value, err := parameterValue(parameter)
		if err != nil {
			return nil, err
		}
		arguments[index] = value
	}
	return arguments, nil
}

func parameterValue(parameter pgwire.Parameter) (any, error) {
	if parameter.Null {
		return nil, nil
	}
	data := parameter.Data
	invalid := func(kind string) error {
		return pgwire.NewError("22P02", fmt.Sprintf("invalid input syntax for type %s: %q", kind, string(data)))
	}
	if parameter.Format == 1 {
		switch parameter.OID {
		case pgwire.OIDInt2:
			if len(data) != 2 {
				return nil, pgwire.NewError("22P03", "invalid binary int2 parameter")
			}
			return int64(int16(binary.BigEndian.Uint16(data))), nil
		case pgwire.OIDInt4:
			if len(data) != 4 {
				return nil, pgwire.NewError("22P03", "invalid binary int4 parameter")
			}
			return int64(int32(binary.BigEndian.Uint32(data))), nil
		case pgwire.OIDInt8:
			if len(data) != 8 {
				return nil, pgwire.NewError("22P03", "invalid binary int8 parameter")
			}
			return int64(binary.BigEndian.Uint64(data)), nil
		case pgwire.OIDFloat4:
			if len(data) != 4 {
				return nil, pgwire.NewError("22P03", "invalid binary float4 parameter")
			}
			return float64(math.Float32frombits(binary.BigEndian.Uint32(data))), nil
		case pgwire.OIDFloat8:
			if len(data) != 8 {
				return nil, pgwire.NewError("22P03", "invalid binary float8 parameter")
			}
			return math.Float64frombits(binary.BigEndian.Uint64(data)), nil
		case pgwire.OIDBool:
			if len(data) != 1 {
				return nil, pgwire.NewError("22P03", "invalid binary bool parameter")
			}
			return int64(data[0] & 1), nil
		case pgwire.OIDBytea:
			return append([]byte(nil), data...), nil
		case 0, pgwire.OIDText, pgwire.OIDVarchar, pgwire.OIDBPChar, pgwire.OIDJSON:
			return string(data), nil
		case pgwire.OIDJSONB:
			if len(data) == 0 || data[0] != 1 {
				return nil, pgwire.NewError("22P03", "invalid binary jsonb parameter")
			}
			return string(data[1:]), nil
		}
		return nil, pgwire.NewError("0A000", fmt.Sprintf("binary parameters of type OID %d are not supported", parameter.OID))
	}
	text := string(data)
	switch parameter.OID {
	case pgwire.OIDInt2, pgwire.OIDInt4, pgwire.OIDInt8, pgwire.OIDOID:
		value, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
		if err != nil {
			return nil, invalid("bigint")
		}
		return value, nil
	case pgwire.OIDFloat4, pgwire.OIDFloat8:
		value, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
		if err != nil {
			return nil, invalid("double precision")
		}
		return value, nil
	case pgwire.OIDBool:
		switch strings.ToLower(strings.TrimSpace(text)) {
		case "t", "true", "1", "yes", "on", "y":
			return int64(1), nil
		case "f", "false", "0", "no", "off", "n":
			return int64(0), nil
		}
		return nil, invalid("boolean")
	case pgwire.OIDBytea:
		if !strings.HasPrefix(text, `\x`) {
			return []byte(text), nil
		}
		value, err := hex.DecodeString(text[2:])
		if err != nil {
			return nil, invalid("bytea")
		}
		return value, nil
	}
	return text, nil
}

// SQLite result codes the gateway turns into SQLSTATEs.
const (
	resultError     = 1
	resultBusy      = 5
	resultLocked    = 6
	resultNoMemory  = 7
	resultReadOnly  = 8
	resultInterrupt = 9
	resultTooBig    = 18
)

var resultCodeSuffix = regexp.MustCompile(`\s*\(\d+\)(\s*\(SQLITE_[A-Z]+\))?$`)

// cleanMessage is SQLite's message without modernc's result code suffix and generic prefix.
func cleanMessage(message string) string {
	message = resultCodeSuffix.ReplaceAllString(message, "")
	return strings.TrimPrefix(message, "SQL logic error: ")
}

// protocolError keeps an error that already carries a SQLSTATE; any other becomes XX000 with what was being done.
func protocolError(err error, doing string) error {
	var protocol *pgwire.Error
	if errors.As(err, &protocol) {
		return err
	}
	return pgwire.NewError("XX000", doing+": "+err.Error())
}

// sqliteError gives a SQLite error the SQLSTATE PostgreSQL would use.
func sqliteError(err error) error {
	if err == nil {
		return nil
	}
	var protocol *pgwire.Error
	if errors.As(err, &protocol) {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return pgwire.NewError("57014", "canceling statement due to user request")
	}
	if errors.Is(err, sql.ErrConnDone) {
		return pgwire.NewError("08003", "the session's SQLite connection is closed")
	}
	message := cleanMessage(err.Error())
	if strings.HasPrefix(message, "missing named argument") || strings.HasPrefix(message, "missing argument with index") {
		return pgwire.NewError("42P02", "a parameter of the statement has no value: "+message)
	}
	var failure *sqlite.Error
	if !errors.As(err, &failure) {
		return pgwire.NewError("XX000", message)
	}
	switch failure.Code() & 0xff {
	case resultInterrupt:
		return pgwire.NewError("57014", "canceling statement due to user request")
	case resultReadOnly:
		return pgwire.NewError("25006", "cannot execute a write in a read-only transaction")
	case resultBusy, resultLocked:
		return pgwire.NewError("55P03", message)
	case resultNoMemory:
		return pgwire.NewError("53200", message)
	case resultTooBig:
		return pgwire.NewError("54000", "a value exceeds the bytes a result may hold: "+message)
	case resultError:
		switch {
		case strings.HasPrefix(message, "no such table"):
			return pgwire.NewError("42P01", message)
		case strings.HasPrefix(message, "no such column"):
			return pgwire.NewError("42703", message)
		case strings.HasPrefix(message, "no such function"), strings.HasPrefix(message, "wrong number of arguments"):
			return pgwire.NewError("42883", message)
		case strings.Contains(message, "syntax error"), strings.Contains(message, "incomplete input"),
			strings.Contains(message, "unrecognized token"):
			return pgwire.NewError("42601", message)
		}
	}
	return pgwire.NewError("XX000", message)
}
