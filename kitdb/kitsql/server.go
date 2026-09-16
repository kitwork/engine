package kitsql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const (
	DefaultMaximumBodyBytes   = int64(1 << 20)
	DefaultMaximumResultBytes = int64(16 << 20)
	DefaultMaximumParameters  = 4_096
	DefaultMaximumRows        = 10_000
	DefaultMaximumConcurrent  = 64
	DefaultQueryTimeout       = 30 * time.Second
)

var (
	ErrUnauthorized     = errors.New("kitsql: unauthorized")
	ErrDatabaseNotFound = errors.New("kitsql: database not found")
)

type OpenDatabase func(
	ctx context.Context,
	user string,
	password string,
	database string,
) (*sql.DB, func() error, error)

type HandlerOptions struct {
	Next               http.Handler
	Open               OpenDatabase
	MaximumBodyBytes   int64
	MaximumResultBytes int64
	MaximumParameters  int
	MaximumRows        int
	MaximumConcurrent  int
	QueryTimeout       time.Duration
	AllowInsecureLocal bool
}

type Handler struct {
	next               http.Handler
	open               OpenDatabase
	maximumBodyBytes   int64
	maximumResultBytes int64
	maximumParameters  int
	maximumRows        int
	queryTimeout       time.Duration
	allowInsecureLocal bool
	admission          chan struct{}
}

func NewHandler(options HandlerOptions) (*Handler, error) {
	if options.Open == nil {
		return nil, fmt.Errorf("kitsql: database opener is required")
	}
	if options.MaximumBodyBytes == 0 {
		options.MaximumBodyBytes = DefaultMaximumBodyBytes
	}
	if options.MaximumResultBytes == 0 {
		options.MaximumResultBytes = DefaultMaximumResultBytes
	}
	if options.MaximumParameters == 0 {
		options.MaximumParameters = DefaultMaximumParameters
	}
	if options.MaximumRows == 0 {
		options.MaximumRows = DefaultMaximumRows
	}
	if options.MaximumConcurrent == 0 {
		options.MaximumConcurrent = DefaultMaximumConcurrent
	}
	if options.QueryTimeout == 0 {
		options.QueryTimeout = DefaultQueryTimeout
	}
	if options.MaximumBodyBytes < 1 || options.MaximumBodyBytes > 16<<20 {
		return nil, fmt.Errorf("kitsql: maximum body bytes must be between 1 and 16 MiB")
	}
	if options.MaximumResultBytes < 1 || options.MaximumResultBytes > 32<<20 {
		return nil, fmt.Errorf("kitsql: maximum result bytes must be between 1 and 32 MiB")
	}
	if options.MaximumParameters < 1 || options.MaximumParameters > 65_535 {
		return nil, fmt.Errorf("kitsql: maximum parameters must be between 1 and 65535")
	}
	if options.MaximumRows < 1 || options.MaximumRows > 100_000 {
		return nil, fmt.Errorf("kitsql: maximum rows must be between 1 and 100000")
	}
	if options.MaximumConcurrent < 1 || options.MaximumConcurrent > 4_096 {
		return nil, fmt.Errorf("kitsql: maximum concurrency must be between 1 and 4096")
	}
	if options.QueryTimeout < time.Millisecond || options.QueryTimeout > 10*time.Minute {
		return nil, fmt.Errorf("kitsql: query timeout must be between 1ms and 10m")
	}
	return &Handler{
		next:               options.Next,
		open:               options.Open,
		maximumBodyBytes:   options.MaximumBodyBytes,
		maximumResultBytes: options.MaximumResultBytes,
		maximumParameters:  options.MaximumParameters,
		maximumRows:        options.MaximumRows,
		queryTimeout:       options.QueryTimeout,
		allowInsecureLocal: options.AllowInsecureLocal,
		admission:          make(chan struct{}, options.MaximumConcurrent),
	}, nil
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != Path {
		if handler.next != nil {
			handler.next.ServeHTTP(writer, request)
			return
		}
		http.NotFound(writer, request)
		return
	}
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writeProtocolError(writer, http.StatusMethodNotAllowed, "KSQL_METHOD", "KitSQL accepts POST only")
		return
	}
	if request.TLS == nil && !(handler.allowInsecureLocal && requestIsLoopback(request)) {
		writeProtocolError(writer, http.StatusUpgradeRequired, "KSQL_TLS_REQUIRED", "KitSQL requires TLS outside loopback")
		return
	}
	contentType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0])
	if contentType != "application/vnd.kitsql+json" && contentType != "application/json" {
		writeProtocolError(writer, http.StatusUnsupportedMediaType, "KSQL_CONTENT_TYPE", "KitSQL requires application/vnd.kitsql+json")
		return
	}
	select {
	case handler.admission <- struct{}{}:
		defer func() { <-handler.admission }()
	default:
		writer.Header().Set("Retry-After", "1")
		writeProtocolError(writer, http.StatusTooManyRequests, "KSQL_BUSY", "KitSQL query capacity is busy")
		return
	}
	user, password, ok := request.BasicAuth()
	if !ok || user == "" {
		writer.Header().Set("WWW-Authenticate", `Basic realm="KitSQL"`)
		writeProtocolError(writer, http.StatusUnauthorized, "KSQL_AUTH", "KitSQL authentication is required")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, handler.maximumBodyBytes)
	decoder := json.NewDecoder(request.Body)
	var protocolRequest Request
	if err := decodeJSONValue(decoder, &protocolRequest); err != nil {
		writeProtocolError(writer, http.StatusBadRequest, "KSQL_REQUEST", "invalid KitSQL request: "+err.Error())
		return
	}
	if err := requireJSONEnd(decoder); err != nil {
		writeProtocolError(writer, http.StatusBadRequest, "KSQL_REQUEST", err.Error())
		return
	}
	if protocolRequest.Version != Version {
		writeProtocolError(writer, http.StatusBadRequest, "KSQL_VERSION", fmt.Sprintf("unsupported KitSQL version %d", protocolRequest.Version))
		return
	}
	protocolRequest.Database = strings.TrimSpace(protocolRequest.Database)
	if protocolRequest.Database == "" {
		writeProtocolError(writer, http.StatusBadRequest, "KSQL_DATABASE", "database is required")
		return
	}
	if strings.TrimSpace(protocolRequest.SQL) == "" {
		writeProtocolError(writer, http.StatusBadRequest, "KSQL_SQL", "SQL is required")
		return
	}
	if protocolRequest.Mode != ModeQuery && protocolRequest.Mode != ModeExec {
		writeProtocolError(writer, http.StatusBadRequest, "KSQL_MODE", "mode must be query or exec")
		return
	}
	if len(protocolRequest.Parameters) > handler.maximumParameters {
		writeProtocolError(writer, http.StatusRequestEntityTooLarge, "KSQL_PARAMETERS", "parameter count exceeds the configured limit")
		return
	}
	parameters := make([]any, len(protocolRequest.Parameters))
	for index, encoded := range protocolRequest.Parameters {
		value, err := encoded.decode()
		if err != nil {
			writeProtocolError(writer, http.StatusBadRequest, "KSQL_PARAMETER", fmt.Sprintf("parameter %d: %v", index+1, err))
			return
		}
		parameters[index] = value
	}
	ctx, cancel := context.WithTimeout(request.Context(), handler.queryTimeout)
	defer cancel()
	database, release, err := handler.open(ctx, user, password, protocolRequest.Database)
	if err != nil {
		switch {
		case errors.Is(err, ErrUnauthorized):
			writeProtocolError(writer, http.StatusUnauthorized, "KSQL_AUTH", "invalid KitSQL credentials")
		case errors.Is(err, ErrDatabaseNotFound):
			writeProtocolError(writer, http.StatusNotFound, "KSQL_DATABASE", "KitSQL database does not exist")
		default:
			writeProtocolError(writer, http.StatusServiceUnavailable, "KSQL_OPEN", err.Error())
		}
		return
	}
	if database == nil {
		writeProtocolError(writer, http.StatusServiceUnavailable, "KSQL_OPEN", "KitSQL database is unavailable")
		return
	}
	if release == nil {
		release = func() error { return nil }
	}
	defer release()
	if protocolRequest.Mode == ModeExec {
		handler.execute(writer, ctx, database, protocolRequest.SQL, parameters)
		return
	}
	handler.query(writer, ctx, database, protocolRequest.SQL, parameters)
}

func (handler *Handler) execute(
	writer http.ResponseWriter,
	ctx context.Context,
	database *sql.DB,
	source string,
	parameters []any,
) {
	result, err := database.ExecContext(ctx, source, parameters...)
	if err != nil {
		writeProtocolError(writer, http.StatusBadRequest, "KSQL_EXECUTION", err.Error())
		return
	}
	affected, err := result.RowsAffected()
	if err != nil {
		writeProtocolError(writer, http.StatusInternalServerError, "KSQL_RESULT", err.Error())
		return
	}
	writeProtocolJSON(writer, http.StatusOK, Response{Version: Version, Affected: affected})
}

func (handler *Handler) query(
	writer http.ResponseWriter,
	ctx context.Context,
	database *sql.DB,
	source string,
	parameters []any,
) {
	rows, err := database.QueryContext(ctx, source, parameters...)
	if err != nil {
		writeProtocolError(writer, http.StatusBadRequest, "KSQL_EXECUTION", err.Error())
		return
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		writeProtocolError(writer, http.StatusInternalServerError, "KSQL_RESULT", err.Error())
		return
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		writeProtocolError(writer, http.StatusInternalServerError, "KSQL_RESULT", err.Error())
		return
	}
	columns := make([]Column, len(names))
	for index, name := range names {
		columns[index] = Column{Name: name}
		if index < len(types) {
			columns[index].DatabaseType = types[index].DatabaseTypeName()
		}
	}
	resultRows := make([][]Value, 0)
	resultBytes := int64(0)
	for _, column := range columns {
		resultBytes += int64(len(column.Name) + len(column.DatabaseType) + 32)
	}
	for rows.Next() {
		if len(resultRows) >= handler.maximumRows {
			writeProtocolError(writer, http.StatusRequestEntityTooLarge, "KSQL_RESULT_LIMIT", "result exceeds the configured row limit")
			return
		}
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for index := range values {
			destinations[index] = &values[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			writeProtocolError(writer, http.StatusInternalServerError, "KSQL_RESULT", err.Error())
			return
		}
		encoded := make([]Value, len(values))
		for index, value := range values {
			resultBytes += estimatedSourceBytes(value)
			if resultBytes > handler.maximumResultBytes {
				writeProtocolError(writer, http.StatusRequestEntityTooLarge, "KSQL_RESULT_LIMIT", "result exceeds the configured byte limit")
				return
			}
			encoded[index], err = encodeValue(value)
			if err != nil {
				writeProtocolError(writer, http.StatusInternalServerError, "KSQL_RESULT", err.Error())
				return
			}
		}
		resultRows = append(resultRows, encoded)
	}
	if err := rows.Err(); err != nil {
		writeProtocolError(writer, http.StatusBadRequest, "KSQL_EXECUTION", err.Error())
		return
	}
	writeProtocolJSON(writer, http.StatusOK, Response{
		Version: Version,
		Columns: columns,
		Rows:    resultRows,
	})
}

func estimatedSourceBytes(value any) int64 {
	switch typed := value.(type) {
	case string:
		return int64(len(typed) + 32)
	case []byte:
		return int64((len(typed)+2)/3*4 + 32)
	case nil:
		return 16
	default:
		return 64
	}
}

func requestIsLoopback(request *http.Request) bool {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		host = request.RemoteAddr
	}
	address := net.ParseIP(strings.Trim(host, "[]"))
	return address != nil && address.IsLoopback()
}

func writeProtocolError(writer http.ResponseWriter, status int, code, message string) {
	writeProtocolJSON(writer, status, Response{
		Version: Version,
		Error:   &Error{Code: code, Message: message},
	})
}

func writeProtocolJSON(writer http.ResponseWriter, status int, response Response) {
	writer.Header().Set("Content-Type", "application/vnd.kitsql+json")
	writer.Header().Set("KitSQL-Version", "1")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(response)
}

func requireJSONEnd(decoder interface{ Decode(any) error }) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return fmt.Errorf("KitSQL request contains trailing JSON")
	}
	return fmt.Errorf("invalid trailing KitSQL request: %w", err)
}
