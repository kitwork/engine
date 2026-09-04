package work

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/kitwork/engine/kitdb/pgwire"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
	requestscope "github.com/kitwork/engine/request"
	"github.com/kitwork/engine/value"
)

const kitDBPostgresCopyRecordBytes = 4 << 20

type kitDBPostgresCopyFormat uint8

const (
	kitDBPostgresCopyText kitDBPostgresCopyFormat = iota
	kitDBPostgresCopyCSV
)

type kitDBPostgresCopySpec struct {
	table        string
	columns      []string
	format       kitDBPostgresCopyFormat
	bulk         bool
	delimiter    byte
	null         string
	header       bool
	delimiterSet bool
	nullSet      bool
	progress     *kitDBPostgresCopyProgress
	progressMask uint16
}

type kitDBPostgresCopyProgress struct {
	id           string
	source       string
	sourceFormat string
	previous     string
	checksum     string
	chunk        uint64
	start        uint64
	end          uint64
	rows         uint64
	complete     bool
}

const (
	kitDBCopyProgressImport uint16 = 1 << iota
	kitDBCopyProgressSource
	kitDBCopyProgressSourceFormat
	kitDBCopyProgressChunk
	kitDBCopyProgressStart
	kitDBCopyProgressEnd
	kitDBCopyProgressRows
	kitDBCopyProgressPrevious
	kitDBCopyProgressChecksum
	kitDBCopyProgressComplete
	kitDBCopyProgressAll = (1 << iota) - 1
)

type kitDBPostgresCopyField struct {
	data   []byte
	quoted bool
}

// BeginCopyIn exposes COPY as an optional pgwire capability. COPY uses one
// ordinary KitDB record transaction, so malformed input, constraints, cancel,
// disconnect, and COMMIT all share the same rollback path as INSERT.
func (session *kitDBPostgresSession) BeginCopyIn(
	ctx context.Context,
	source string,
	parameters []pgwire.Parameter,
) (pgwire.CopyInRequest, bool, error) {
	spec, handled, err := parseKitDBPostgresCopy(source)
	if !handled {
		return pgwire.CopyInRequest{}, false, nil
	}
	if session.traceQuery != nil {
		session.traceQuery(session.databaseName, source, len(parameters))
	}
	if err != nil {
		session.failKitDBPostgresCopyTransaction()
		return pgwire.CopyInRequest{}, true, kitDBPostgresError(err)
	}
	if len(parameters) != 0 {
		session.failKitDBPostgresCopyTransaction()
		return pgwire.CopyInRequest{}, true, pgwire.NewError("42601", "COPY FROM STDIN does not accept parameters")
	}
	if session.nodeMode && !session.maintenance {
		if _, config, found := resolveServe(session.tenant, session.storageName); !found ||
			config.database == nil || config.engine != "kitdb" {
			return pgwire.CopyInRequest{}, true, pgwire.NewError(
				"3D000", fmt.Sprintf("KitDB database %q does not exist", session.databaseName),
			)
		}
	}
	if session.maintenance || session.database == nil {
		return pgwire.CopyInRequest{}, true, pgwire.NewError(
			"3D000", "KitDB maintenance database has no user structs",
		)
	}

	session.transactionMu.Lock()
	if session.transaction != nil {
		stream, streamErr := session.beginKitDBPostgresTransactionalCopyLocked(ctx, spec)
		if streamErr != nil {
			session.transactionMu.Unlock()
			return pgwire.CopyInRequest{}, true, streamErr
		}
		return kitDBPostgresCopyRequest(stream), true, nil
	}
	session.transactionMu.Unlock()
	if spec.bulk {
		return pgwire.CopyInRequest{}, true, pgwire.NewError(
			"25001", "KITDB_BULK COPY requires an explicit transaction",
		)
	}

	stream, err := session.beginKitDBPostgresAutocommitCopy(ctx, spec)
	if err != nil {
		return pgwire.CopyInRequest{}, true, err
	}
	return kitDBPostgresCopyRequest(stream), true, nil
}

func kitDBPostgresCopyRequest(stream *kitDBPostgresCopyStream) pgwire.CopyInRequest {
	return pgwire.CopyInRequest{
		Format:        0,
		ColumnFormats: make([]int16, len(stream.fields)),
		Lifetime:      stream.lifetime,
		Stream:        stream,
	}
}

func (session *kitDBPostgresSession) failKitDBPostgresCopyTransaction() {
	session.transactionMu.Lock()
	if session.transaction != nil {
		session.transaction.failed = true
	}
	session.transactionMu.Unlock()
}

func (session *kitDBPostgresSession) beginKitDBPostgresTransactionalCopyLocked(
	ctx context.Context,
	spec kitDBPostgresCopySpec,
) (*kitDBPostgresCopyStream, error) {
	transaction := session.transaction
	if transaction.failed {
		if transaction.expired {
			return nil, pgwire.NewError("25P03", "KitDB transaction exceeded its lifetime; issue ROLLBACK")
		}
		return nil, pgwire.NewError("25P02", "current KitDB transaction is aborted; issue ROLLBACK")
	}
	if transaction.context == nil || transaction.context.Err() != nil {
		session.expireKitDBPostgresTransactionLocked(transaction)
		return nil, pgwire.NewError("25P03", "KitDB transaction exceeded its lifetime; issue ROLLBACK")
	}
	transaction.statements++
	if transaction.statements > kitDBRemoteTransactionStatementLimit {
		transaction.failed = true
		return nil, pgwire.NewError(
			"54000", fmt.Sprintf("KitDB transaction exceeds %d statements", kitDBRemoteTransactionStatementLimit),
		)
	}
	if session.readonly || transaction.readOnly {
		transaction.failed = true
		return nil, pgwire.NewError("25006", "database is read-only")
	}
	if transaction.mode == kitDBPostgresTransactionDDL {
		transaction.failed = true
		return nil, pgwire.NewError("0A000", "a staged KitDB schema transaction cannot run COPY")
	}
	if spec.bulk && transaction.mode != kitDBPostgresTransactionUndecided {
		transaction.failed = true
		return nil, pgwire.NewError(
			"25001", "KITDB_BULK COPY must be the first data statement in its transaction",
		)
	}
	var err error
	if transaction.mode == kitDBPostgresTransactionUndecided {
		if spec.bulk {
			transaction.database, transaction.record, err = session.database.beginKitDBBulkRecordTransaction(transaction.scope)
		} else {
			transaction.database, transaction.record, err = session.database.beginKitDBRecordTransaction(transaction.scope)
		}
		if err != nil {
			transaction.failed = true
			return nil, kitDBPostgresError(err)
		}
		transaction.mode = kitDBPostgresTransactionData
	}
	statementContext, releaseContext := kitDBPostgresStatementContext(ctx, transaction.context)
	stream, err := newKitDBPostgresCopyStream(
		statementContext, transaction.database, transaction.scope, transaction.record, spec,
	)
	if err != nil {
		releaseContext()
		transaction.failed = true
		return nil, kitDBPostgresError(err)
	}
	stream.savepoint = transaction.record.Savepoint()
	stream.explicit = transaction
	stream.releaseContext = releaseContext
	stream.unlock = session.transactionMu.Unlock
	stream.lifetime = transaction.context
	return stream, nil
}

func (session *kitDBPostgresSession) beginKitDBPostgresAutocommitCopy(
	ctx context.Context,
	spec kitDBPostgresCopySpec,
) (*kitDBPostgresCopyStream, error) {
	if session.readonly {
		return nil, pgwire.NewError("25006", "database is read-only")
	}
	if !session.tenant.beginRequest() {
		return nil, pgwire.NewError("57P01", "KitDB tenant is shutting down")
	}
	requestOpen := true
	cleanupRequest := func() {
		if requestOpen {
			requestOpen = false
			session.tenant.endRequest()
		}
	}
	lease, err := session.tenant.generationLease()
	if err != nil {
		cleanupRequest()
		return nil, pgwire.NewError("57P01", err.Error())
	}
	request := (&http.Request{}).WithContext(ctx)
	scope := requestscope.New(session.tenant, nil, request)
	if lease != nil && !scope.AddCleanup(lease.Release) {
		lease.Release()
		scope.Close()
		cleanupRequest()
		return nil, pgwire.NewError("57P01", "KitDB request scope is unavailable")
	}
	target, transaction, err := session.database.beginKitDBRecordTransaction(scope)
	if err != nil {
		scope.Close()
		cleanupRequest()
		return nil, kitDBPostgresError(err)
	}
	stream, err := newKitDBPostgresCopyStream(ctx, target, scope, transaction, spec)
	if err != nil {
		_ = transaction.Rollback()
		scope.Close()
		cleanupRequest()
		return nil, kitDBPostgresError(err)
	}
	stream.owned = true
	stream.scope = scope
	stream.requestDone = cleanupRequest
	return stream, nil
}

type kitDBPostgresCopyStream struct {
	mu             sync.Mutex
	ctx            context.Context
	table          *SchemaTable
	transaction    *kitDBRecordTransaction
	savepoint      kitDBRecordSavepoint
	explicit       *kitDBPostgresTransaction
	owned          bool
	fields         []StructFieldDef
	decoder        *kitDBPostgresCopyDecoder
	scope          *requestscope.Scope
	requestDone    func()
	releaseContext func()
	unlock         func()
	lifetime       context.Context
	done           bool
	importState    *kitDBImportState
}

func newKitDBPostgresCopyStream(
	ctx context.Context,
	database *dbProxy,
	scope *requestscope.Scope,
	transaction *kitDBRecordTransaction,
	spec kitDBPostgresCopySpec,
) (*kitDBPostgresCopyStream, error) {
	table, err := kitDBRemoteTable(database, scope, spec.table)
	if err != nil {
		return nil, err
	}
	if err := ensureKitDBRemoteTableReady(table); err != nil {
		return nil, err
	}
	fields, err := kitDBPostgresCopyFields(table.definition, spec.columns)
	if err != nil {
		return nil, err
	}
	importState, err := prepareKitDBImportState(transaction, table.definition, fields, spec.progress)
	if err != nil {
		return nil, err
	}
	stream := &kitDBPostgresCopyStream{
		ctx: ctx, table: table, transaction: transaction, fields: fields,
		importState: importState,
	}
	stream.decoder = newKitDBPostgresCopyDecoder(spec, fields, stream.writeRow)
	return stream, nil
}

func kitDBPostgresCopyFields(
	definition *StructDef,
	requested []string,
) ([]StructFieldDef, error) {
	if definition == nil {
		return nil, fmt.Errorf("kitdb COPY: struct is unavailable")
	}
	if len(requested) == 0 {
		return append([]StructFieldDef(nil), definition.Fields...), nil
	}
	fields := make([]StructFieldDef, 0, len(requested))
	seen := make(map[string]struct{}, len(requested))
	for _, name := range requested {
		canonical, field, err := kitDBRemoteField(definition, name)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(canonical)
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("kitdb COPY: duplicate column %q", name)
		}
		seen[key] = struct{}{}
		fields = append(fields, field)
	}
	return fields, nil
}

func (stream *kitDBPostgresCopyStream) Write(ctx context.Context, data []byte) error {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.done {
		return pgwire.NewError("55000", "KitDB COPY stream is closed")
	}
	if err := contextErrorEither(stream.ctx, ctx); err != nil {
		return kitDBPostgresError(err)
	}
	if err := stream.decoder.Write(data); err != nil {
		return kitDBPostgresError(err)
	}
	return nil
}

func (stream *kitDBPostgresCopyStream) Complete(ctx context.Context) (pgwire.Result, error) {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.done {
		return pgwire.Result{}, pgwire.NewError("55000", "KitDB COPY stream is closed")
	}
	if err := contextErrorEither(stream.ctx, ctx); err != nil {
		return pgwire.Result{}, stream.failLocked(err)
	}
	if err := stream.decoder.Complete(); err != nil {
		return pgwire.Result{}, stream.failLocked(err)
	}
	rows := stream.decoder.Rows()
	if stream.importState != nil && uint64(rows) != stream.decoder.spec.progress.rows {
		return pgwire.Result{}, stream.failLocked(fmt.Errorf(
			"kitdb import %q declared %d rows but COPY received %d",
			stream.importState.ID, stream.decoder.spec.progress.rows, rows,
		))
	}
	if err := putKitDBImportState(stream.transaction, stream.importState); err != nil {
		return pgwire.Result{}, stream.failLocked(err)
	}
	if rows > 0 {
		if err := stream.table.invalidateKitDBStatistics(stream.transaction); err != nil {
			return pgwire.Result{}, stream.failLocked(err)
		}
	}
	if err := contextErrorEither(stream.ctx, ctx); err != nil {
		return pgwire.Result{}, stream.failLocked(err)
	}
	if stream.owned {
		if _, err := stream.transaction.CommitContext(ctx); err != nil {
			return pgwire.Result{}, stream.failLocked(err)
		}
		stream.table.markKitDBStatisticsStale()
	}
	stream.done = true
	stream.cleanupLocked()
	return pgwire.Result{CommandTag: fmt.Sprintf("COPY %d", rows)}, nil
}

func (stream *kitDBPostgresCopyStream) Abort(err error) error {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.done {
		return nil
	}
	return stream.failLocked(err)
}

func (stream *kitDBPostgresCopyStream) failLocked(err error) error {
	if stream.done {
		return kitDBPostgresError(err)
	}
	stream.done = true
	if stream.owned {
		_ = stream.transaction.Rollback()
	} else {
		stream.transaction.RollbackTo(stream.savepoint)
		if stream.explicit != nil {
			stream.explicit.failed = true
		}
	}
	stream.cleanupLocked()
	return kitDBPostgresError(err)
}

func (stream *kitDBPostgresCopyStream) cleanupLocked() {
	if stream.releaseContext != nil {
		stream.releaseContext()
		stream.releaseContext = nil
	}
	if stream.scope != nil {
		stream.scope.Close()
		stream.scope = nil
	}
	if stream.requestDone != nil {
		stream.requestDone()
		stream.requestDone = nil
	}
	if stream.unlock != nil {
		stream.unlock()
		stream.unlock = nil
	}
}

func (stream *kitDBPostgresCopyStream) writeRow(fields []kitDBPostgresCopyField, rowNumber int64) error {
	if err := contextError(stream.ctx); err != nil {
		return err
	}
	provided := make(map[string]value.Value, len(fields))
	for index, raw := range fields {
		item, err := decodeKitDBPostgresCopyValue(stream.decoder.spec, stream.fields[index], raw)
		if err != nil {
			return fmt.Errorf("kitdb COPY row %d column %q: %w", rowNumber, stream.fields[index].Name, err)
		}
		provided[stream.fields[index].Name] = item
	}
	row, message := fillRow(stream.table.columns, provided)
	if message != "" {
		return fmt.Errorf("kitdb COPY row %d: table %q %s", rowNumber, stream.table.table, message)
	}
	if err := stream.table.writeKitDBRowInTransaction(stream.transaction, row); err != nil {
		return fmt.Errorf("kitdb COPY row %d: %w", rowNumber, err)
	}
	return nil
}

type kitDBPostgresCopyDecoder struct {
	spec          kitDBPostgresCopySpec
	fieldCount    int
	emit          func([]kitDBPostgresCopyField, int64) error
	fields        []kitDBPostgresCopyField
	field         []byte
	fieldQuoted   bool
	inQuotes      bool
	afterQuote    bool
	escaped       bool
	skipLF        bool
	recordStarted bool
	recordBytes   int
	inputRows     int64
	rows          int64
	complete      bool
}

func newKitDBPostgresCopyDecoder(
	spec kitDBPostgresCopySpec,
	fields []StructFieldDef,
	emit func([]kitDBPostgresCopyField, int64) error,
) *kitDBPostgresCopyDecoder {
	return &kitDBPostgresCopyDecoder{spec: spec, fieldCount: len(fields), emit: emit}
}

func (decoder *kitDBPostgresCopyDecoder) Rows() int64 { return decoder.rows }

func (decoder *kitDBPostgresCopyDecoder) Write(data []byte) error {
	if decoder.complete {
		return pgwire.NewError("55000", "KitDB COPY decoder is closed")
	}
	for _, current := range data {
		if decoder.skipLF {
			decoder.skipLF = false
			if current == '\n' {
				continue
			}
		}
		decoder.recordBytes++
		if decoder.recordBytes > kitDBPostgresCopyRecordBytes {
			return pgwire.NewError(
				"54000", fmt.Sprintf("KitDB COPY record exceeds %d bytes", kitDBPostgresCopyRecordBytes),
			)
		}
		var err error
		if decoder.spec.format == kitDBPostgresCopyCSV {
			err = decoder.writeCSVByte(current)
		} else {
			err = decoder.writeTextByte(current)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (decoder *kitDBPostgresCopyDecoder) writeTextByte(current byte) error {
	if decoder.escaped {
		decoder.field = append(decoder.field, current)
		decoder.escaped = false
		decoder.recordStarted = true
		return nil
	}
	if current == '\\' {
		decoder.field = append(decoder.field, current)
		decoder.escaped = true
		decoder.recordStarted = true
		return nil
	}
	switch current {
	case decoder.spec.delimiter:
		decoder.finishField(false)
		decoder.recordStarted = true
	case '\n':
		return decoder.finishRecord(false)
	case '\r':
		decoder.skipLF = true
		return decoder.finishRecord(false)
	default:
		decoder.field = append(decoder.field, current)
		decoder.recordStarted = true
	}
	return nil
}

func (decoder *kitDBPostgresCopyDecoder) writeCSVByte(current byte) error {
	const quote = byte('"')
	if decoder.inQuotes {
		if current == quote {
			decoder.inQuotes = false
			decoder.afterQuote = true
			return nil
		}
		decoder.field = append(decoder.field, current)
		decoder.recordStarted = true
		return nil
	}
	if decoder.afterQuote {
		switch current {
		case quote:
			decoder.field = append(decoder.field, quote)
			decoder.inQuotes = true
			decoder.afterQuote = false
			decoder.recordStarted = true
			return nil
		case decoder.spec.delimiter:
			decoder.finishField(true)
			decoder.afterQuote = false
			decoder.recordStarted = true
			return nil
		case '\n':
			decoder.afterQuote = false
			return decoder.finishRecord(true)
		case '\r':
			decoder.afterQuote = false
			decoder.skipLF = true
			return decoder.finishRecord(true)
		default:
			return pgwire.NewError("22P04", "KitDB COPY CSV has data after a closing quote")
		}
	}
	if len(decoder.field) == 0 && !decoder.fieldQuoted && current == quote {
		decoder.fieldQuoted = true
		decoder.inQuotes = true
		decoder.recordStarted = true
		return nil
	}
	switch current {
	case quote:
		return pgwire.NewError("22P04", "KitDB COPY CSV has a quote inside an unquoted field")
	case decoder.spec.delimiter:
		decoder.finishField(false)
		decoder.recordStarted = true
	case '\n':
		return decoder.finishRecord(false)
	case '\r':
		decoder.skipLF = true
		return decoder.finishRecord(false)
	default:
		decoder.field = append(decoder.field, current)
		decoder.recordStarted = true
	}
	return nil
}

func (decoder *kitDBPostgresCopyDecoder) finishField(quoted bool) {
	decoder.fields = append(decoder.fields, kitDBPostgresCopyField{
		data: append([]byte(nil), decoder.field...), quoted: decoder.fieldQuoted || quoted,
	})
	decoder.field = decoder.field[:0]
	decoder.fieldQuoted = false
}

func (decoder *kitDBPostgresCopyDecoder) finishRecord(quoted bool) error {
	decoder.finishField(quoted)
	decoder.inputRows++
	rowNumber := decoder.inputRows
	fields := decoder.fields
	if decoder.spec.header && decoder.inputRows == 1 {
		decoder.resetRecord()
		return nil
	}
	if len(fields) != decoder.fieldCount {
		decoder.resetRecord()
		return pgwire.NewError(
			"22P04",
			fmt.Sprintf("KitDB COPY row %d has %d fields; expected %d", rowNumber, len(fields), decoder.fieldCount),
		)
	}
	if err := decoder.emit(fields, rowNumber); err != nil {
		decoder.resetRecord()
		return err
	}
	decoder.rows++
	decoder.resetRecord()
	return nil
}

func (decoder *kitDBPostgresCopyDecoder) resetRecord() {
	decoder.fields = decoder.fields[:0]
	decoder.field = decoder.field[:0]
	decoder.fieldQuoted = false
	decoder.inQuotes = false
	decoder.afterQuote = false
	decoder.escaped = false
	decoder.recordStarted = false
	decoder.recordBytes = 0
}

func (decoder *kitDBPostgresCopyDecoder) Complete() error {
	if decoder.complete {
		return pgwire.NewError("55000", "KitDB COPY decoder is closed")
	}
	decoder.complete = true
	if decoder.inQuotes {
		return pgwire.NewError("22P04", "KitDB COPY CSV ends inside a quoted field")
	}
	if decoder.spec.format == kitDBPostgresCopyText && decoder.escaped {
		return pgwire.NewError("22P04", "KitDB COPY text ends after an escape character")
	}
	if decoder.afterQuote {
		decoder.afterQuote = false
	}
	if decoder.recordStarted || len(decoder.fields) != 0 || len(decoder.field) != 0 || decoder.fieldQuoted {
		return decoder.finishRecord(decoder.spec.format == kitDBPostgresCopyCSV)
	}
	return nil
}

func decodeKitDBPostgresCopyValue(
	spec kitDBPostgresCopySpec,
	field StructFieldDef,
	raw kitDBPostgresCopyField,
) (value.Value, error) {
	if !raw.quoted && string(raw.data) == spec.null {
		return value.NULL, nil
	}
	data := raw.data
	var err error
	if spec.format == kitDBPostgresCopyText {
		data, err = decodeKitDBPostgresCopyText(data)
		if err != nil {
			return value.Value{}, pgwire.NewError("22P04", err.Error())
		}
	}
	if field.Kind == "blob" {
		if len(data) >= 2 && data[0] == '\\' && data[1] == 'x' {
			decoded := make([]byte, hex.DecodedLen(len(data)-2))
			if _, err := hex.Decode(decoded, data[2:]); err != nil {
				return value.Value{}, pgwire.NewError("22P02", "invalid hexadecimal blob")
			}
			return value.New(decoded), nil
		}
		return value.New(append([]byte(nil), data...)), nil
	}
	if !utf8.Valid(data) {
		return value.Value{}, pgwire.NewError("22021", "COPY field is not valid UTF-8")
	}
	text := string(data)
	switch field.Kind {
	case "integer", "smallint", "int32", "serial", "year", "month", "day":
		bits := 64
		if field.Kind == "smallint" {
			bits = 16
		} else if field.Kind == "int32" {
			bits = 32
		}
		parsed, err := strconv.ParseInt(text, 10, bits)
		if err != nil {
			return value.Value{}, pgwire.NewError("22P02", "invalid integer input "+strconv.Quote(text))
		}
		return value.New(parsed), nil
	case "float":
		parsed, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return value.Value{}, pgwire.NewError("22P02", "invalid floating-point input "+strconv.Quote(text))
		}
		return value.New(parsed), nil
	case "bool":
		switch strings.ToLower(strings.TrimSpace(text)) {
		case "1", "t", "true", "y", "yes", "on":
			return value.TRUE, nil
		case "0", "f", "false", "n", "no", "off":
			return value.FALSE, nil
		default:
			return value.Value{}, pgwire.NewError("22P02", "invalid boolean input "+strconv.Quote(text))
		}
	default:
		return value.New(text), nil
	}
}

func decodeKitDBPostgresCopyText(raw []byte) ([]byte, error) {
	decoded := make([]byte, 0, len(raw))
	for index := 0; index < len(raw); index++ {
		if raw[index] != '\\' {
			decoded = append(decoded, raw[index])
			continue
		}
		index++
		if index >= len(raw) {
			return nil, fmt.Errorf("KitDB COPY text ends after an escape character")
		}
		switch raw[index] {
		case 'b':
			decoded = append(decoded, '\b')
		case 'f':
			decoded = append(decoded, '\f')
		case 'n':
			decoded = append(decoded, '\n')
		case 'r':
			decoded = append(decoded, '\r')
		case 't':
			decoded = append(decoded, '\t')
		case 'v':
			decoded = append(decoded, '\v')
		case 'x':
			start := index + 1
			end := start
			for end < len(raw) && end < start+2 && isKitDBCopyHex(raw[end]) {
				end++
			}
			if end == start {
				return nil, fmt.Errorf("KitDB COPY text has an invalid hexadecimal escape")
			}
			parsed, _ := strconv.ParseUint(string(raw[start:end]), 16, 8)
			decoded = append(decoded, byte(parsed))
			index = end - 1
		default:
			if raw[index] >= '0' && raw[index] <= '7' {
				start := index
				end := start
				for end < len(raw) && end < start+3 && raw[end] >= '0' && raw[end] <= '7' {
					end++
				}
				parsed, _ := strconv.ParseUint(string(raw[start:end]), 8, 8)
				decoded = append(decoded, byte(parsed))
				index = end - 1
			} else {
				decoded = append(decoded, raw[index])
			}
		}
	}
	return decoded, nil
}

func isKitDBCopyHex(value byte) bool {
	return (value >= '0' && value <= '9') || (value >= 'a' && value <= 'f') ||
		(value >= 'A' && value <= 'F')
}

func parseKitDBPostgresCopy(source string) (kitDBPostgresCopySpec, bool, error) {
	trimmed := strings.TrimSpace(source)
	if !kitDBPostgresCopyPrefix(trimmed) {
		return kitDBPostgresCopySpec{}, false, nil
	}
	tokens, err := kitdbsql.Lex(trimmed)
	if err != nil {
		return kitDBPostgresCopySpec{}, true, err
	}
	cursor, err := kitdbsql.NewCursor(tokens, 0)
	if err != nil {
		return kitDBPostgresCopySpec{}, true, err
	}
	parser := &kitSQLParser{cursor: cursor, bindings: kitSQLBindings{named: map[string]value.Value{}}}
	if !parser.acceptKeyword("copy") {
		return kitDBPostgresCopySpec{}, true, fmt.Errorf("kitdb SQL: expected COPY")
	}
	spec := kitDBPostgresCopySpec{format: kitDBPostgresCopyText}
	spec.table, err = parser.identifier()
	if err != nil {
		return kitDBPostgresCopySpec{}, true, err
	}
	if parser.acceptSymbol("(") {
		spec.columns, err = parser.identifierList(")")
		if err != nil {
			return kitDBPostgresCopySpec{}, true, err
		}
	}
	if err := parser.expectKeyword("from"); err != nil {
		return kitDBPostgresCopySpec{}, true, fmt.Errorf("kitdb SQL: COPY supports FROM STDIN only")
	}
	if err := parser.expectKeyword("stdin"); err != nil {
		return kitDBPostgresCopySpec{}, true, fmt.Errorf("kitdb SQL: COPY supports FROM STDIN only")
	}
	if parser.acceptKeyword("with") {
		if err := parseKitDBPostgresCopyOptions(parser, &spec); err != nil {
			return kitDBPostgresCopySpec{}, true, err
		}
	}
	if parser.acceptSymbol(";") && parser.peek().kind != kitSQLTokenEOF {
		return kitDBPostgresCopySpec{}, true, fmt.Errorf("kitdb SQL: multiple statements are not supported")
	}
	if parser.peek().kind != kitSQLTokenEOF {
		return kitDBPostgresCopySpec{}, true, fmt.Errorf("kitdb SQL: unexpected token %q", parser.peek().text)
	}
	if !spec.delimiterSet {
		if spec.format == kitDBPostgresCopyCSV {
			spec.delimiter = ','
		} else {
			spec.delimiter = '\t'
		}
	}
	if !spec.nullSet {
		if spec.format == kitDBPostgresCopyCSV {
			spec.null = ""
		} else {
			spec.null = `\N`
		}
	}
	if spec.header && spec.format != kitDBPostgresCopyCSV {
		return kitDBPostgresCopySpec{}, true, fmt.Errorf("kitdb SQL: COPY HEADER requires FORMAT csv")
	}
	if spec.format == kitDBPostgresCopyText && spec.delimiter == '\\' {
		return kitDBPostgresCopySpec{}, true, fmt.Errorf("kitdb SQL: COPY text DELIMITER cannot be a backslash")
	}
	if err := validateKitDBPostgresCopyProgress(&spec); err != nil {
		return kitDBPostgresCopySpec{}, true, err
	}
	return spec, true, nil
}

func kitDBPostgresCopyPrefix(source string) bool {
	for {
		source = strings.TrimSpace(source)
		switch {
		case strings.HasPrefix(source, "--"):
			if end := strings.IndexAny(source, "\r\n"); end >= 0 {
				source = source[end+1:]
				continue
			}
			return false
		case strings.HasPrefix(source, "/*"):
			if end := strings.Index(source[2:], "*/"); end >= 0 {
				source = source[end+4:]
				continue
			}
			return false
		}
		break
	}
	if len(source) < len("copy") || !strings.EqualFold(source[:len("copy")], "copy") {
		return false
	}
	return len(source) == len("copy") || strings.ContainsRune(" \t\r\n", rune(source[len("copy")]))
}

func parseKitDBPostgresCopyOptions(parser *kitSQLParser, spec *kitDBPostgresCopySpec) error {
	if !parser.acceptSymbol("(") {
		return fmt.Errorf("kitdb SQL: COPY WITH options must use parentheses")
	}
	seen := map[string]struct{}{}
	for {
		name, err := parser.identifier()
		if err != nil {
			return err
		}
		name = strings.ToLower(name)
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("kitdb SQL: duplicate COPY option %q", name)
		}
		seen[name] = struct{}{}
		parser.acceptSymbol("=")
		switch name {
		case "format":
			format, err := kitDBPostgresCopyOptionText(parser, name)
			if err != nil {
				return err
			}
			switch strings.ToLower(format) {
			case "text":
				spec.format = kitDBPostgresCopyText
			case "csv":
				spec.format = kitDBPostgresCopyCSV
			default:
				return fmt.Errorf("kitdb SQL: COPY FORMAT must be text or csv")
			}
		case "delimiter":
			delimiter, err := kitDBPostgresCopyOptionText(parser, name)
			if err != nil {
				return err
			}
			if len(delimiter) != 1 || delimiter[0] == 0 || delimiter[0] == '\n' || delimiter[0] == '\r' || delimiter[0] == '"' {
				return fmt.Errorf("kitdb SQL: COPY DELIMITER must be one non-quote byte")
			}
			spec.delimiter, spec.delimiterSet = delimiter[0], true
		case "null":
			null, err := kitDBPostgresCopyOptionText(parser, name)
			if err != nil {
				return err
			}
			if len(null) > 64 || strings.ContainsAny(null, "\r\n") {
				return fmt.Errorf("kitdb SQL: COPY NULL marker is invalid")
			}
			spec.null, spec.nullSet = null, true
		case "header":
			spec.header = true
			if parser.peek().kind != kitSQLTokenSymbol ||
				(parser.peek().text != "," && parser.peek().text != ")") {
				header, err := kitDBPostgresCopyOptionBool(parser, name)
				if err != nil {
					return err
				}
				spec.header = header
			}
		case "kitdb_bulk":
			bulk, err := kitDBPostgresCopyOptionBool(parser, name)
			if err != nil {
				return err
			}
			spec.bulk = bulk
		case "kitdb_import":
			progress := ensureKitDBPostgresCopyProgress(spec)
			progress.id, err = kitDBPostgresCopyOptionText(parser, name)
			spec.progressMask |= kitDBCopyProgressImport
			if err != nil {
				return err
			}
		case "kitdb_source":
			progress := ensureKitDBPostgresCopyProgress(spec)
			progress.source, err = kitDBPostgresCopyOptionText(parser, name)
			spec.progressMask |= kitDBCopyProgressSource
			if err != nil {
				return err
			}
		case "kitdb_source_format":
			progress := ensureKitDBPostgresCopyProgress(spec)
			progress.sourceFormat, err = kitDBPostgresCopyOptionText(parser, name)
			progress.sourceFormat = strings.ToLower(progress.sourceFormat)
			spec.progressMask |= kitDBCopyProgressSourceFormat
			if err != nil {
				return err
			}
		case "kitdb_chunk":
			progress := ensureKitDBPostgresCopyProgress(spec)
			progress.chunk, err = kitDBPostgresCopyOptionUint(parser, name)
			spec.progressMask |= kitDBCopyProgressChunk
			if err != nil {
				return err
			}
		case "kitdb_start":
			progress := ensureKitDBPostgresCopyProgress(spec)
			progress.start, err = kitDBPostgresCopyOptionUint(parser, name)
			spec.progressMask |= kitDBCopyProgressStart
			if err != nil {
				return err
			}
		case "kitdb_end":
			progress := ensureKitDBPostgresCopyProgress(spec)
			progress.end, err = kitDBPostgresCopyOptionUint(parser, name)
			spec.progressMask |= kitDBCopyProgressEnd
			if err != nil {
				return err
			}
		case "kitdb_rows":
			progress := ensureKitDBPostgresCopyProgress(spec)
			progress.rows, err = kitDBPostgresCopyOptionUint(parser, name)
			spec.progressMask |= kitDBCopyProgressRows
			if err != nil {
				return err
			}
		case "kitdb_previous":
			progress := ensureKitDBPostgresCopyProgress(spec)
			progress.previous, err = kitDBPostgresCopyOptionText(parser, name)
			spec.progressMask |= kitDBCopyProgressPrevious
			if err != nil {
				return err
			}
		case "kitdb_checksum":
			progress := ensureKitDBPostgresCopyProgress(spec)
			progress.checksum, err = kitDBPostgresCopyOptionText(parser, name)
			spec.progressMask |= kitDBCopyProgressChecksum
			if err != nil {
				return err
			}
		case "kitdb_complete":
			progress := ensureKitDBPostgresCopyProgress(spec)
			progress.complete, err = kitDBPostgresCopyOptionBool(parser, name)
			spec.progressMask |= kitDBCopyProgressComplete
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("kitdb SQL: unsupported COPY option %q", name)
		}
		if parser.acceptSymbol(")") {
			break
		}
		if err := parser.expectSymbol(","); err != nil {
			return err
		}
	}
	return nil
}

func ensureKitDBPostgresCopyProgress(spec *kitDBPostgresCopySpec) *kitDBPostgresCopyProgress {
	if spec.progress == nil {
		spec.progress = &kitDBPostgresCopyProgress{}
	}
	return spec.progress
}

func validateKitDBPostgresCopyProgress(spec *kitDBPostgresCopySpec) error {
	if spec.progress == nil {
		return nil
	}
	if spec.progressMask != kitDBCopyProgressAll {
		return fmt.Errorf("kitdb SQL: resumable COPY requires every KITDB_IMPORT progress option")
	}
	progress := spec.progress
	if err := validateKitDBImportLabel("id", progress.id, kitDBImportIDLimit); err != nil {
		return err
	}
	if err := validateKitDBImportLabel("source", progress.source, kitDBImportSourceLimit); err != nil {
		return err
	}
	if progress.sourceFormat != "csv" && progress.sourceFormat != "jsonl" {
		return fmt.Errorf("kitdb SQL: KITDB_SOURCE_FORMAT must be csv or jsonl")
	}
	var err error
	progress.previous, err = normalizeKitDBImportChecksum("previous checksum", progress.previous)
	if err != nil {
		return err
	}
	progress.checksum, err = normalizeKitDBImportChecksum("chunk checksum", progress.checksum)
	if err != nil {
		return err
	}
	if progress.chunk == 0 || progress.end < progress.start {
		return fmt.Errorf("kitdb SQL: resumable COPY has an invalid chunk or source offset")
	}
	if progress.rows > kitDBRecordTransactionOperationLimit {
		return fmt.Errorf("kitdb SQL: resumable COPY declares too many rows")
	}
	if progress.rows == 0 && !progress.complete {
		return fmt.Errorf("kitdb SQL: an empty resumable COPY chunk must complete the import")
	}
	if progress.rows > 0 && progress.previous == progress.checksum {
		return fmt.Errorf("kitdb SQL: a nonempty resumable COPY chunk did not advance its checksum")
	}
	if progress.rows > 0 && progress.start == progress.end {
		return fmt.Errorf("kitdb SQL: a nonempty resumable COPY chunk did not advance its source offset")
	}
	return nil
}

func kitDBPostgresCopyOptionText(parser *kitSQLParser, name string) (string, error) {
	token := parser.take()
	if token.kind != kitSQLTokenIdentifier && token.kind != kitSQLTokenString {
		return "", fmt.Errorf("kitdb SQL: COPY %s expects text", strings.ToUpper(name))
	}
	return token.text, nil
}

func kitDBPostgresCopyOptionBool(parser *kitSQLParser, name string) (bool, error) {
	token := parser.take()
	text := strings.ToLower(token.text)
	switch text {
	case "true", "on", "1":
		return true, nil
	case "false", "off", "0":
		return false, nil
	default:
		return false, fmt.Errorf("kitdb SQL: COPY %s expects true or false", strings.ToUpper(name))
	}
}

func kitDBPostgresCopyOptionUint(parser *kitSQLParser, name string) (uint64, error) {
	token := parser.take()
	if token.kind != kitSQLTokenNumber && token.kind != kitSQLTokenIdentifier {
		return 0, fmt.Errorf("kitdb SQL: COPY %s expects a nonnegative integer", strings.ToUpper(name))
	}
	parsed, err := strconv.ParseUint(token.text, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("kitdb SQL: COPY %s expects a nonnegative integer", strings.ToUpper(name))
	}
	return parsed, nil
}
