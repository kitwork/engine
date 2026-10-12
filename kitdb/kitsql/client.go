package kitsql

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

const maximumResponseBytes = int64(32 << 20)
const maximumStreamControlBytes = int64(4096)

var errTransactionsUnsupported = errors.New("kitsql: protocol v1 does not support transactions across requests")

func init() {
	sql.Register("kitsql", networkDriver{})
}

// Open creates a database/sql pool backed by a kitsql:// endpoint.
func Open(dataSourceName string) (*sql.DB, error) {
	return OpenWithClient(dataSourceName, http.DefaultClient)
}

// OpenWithClient is Open with an explicit HTTP client. It is useful for
// private certificate authorities and deterministic protocol tests.
func OpenWithClient(dataSourceName string, client *http.Client) (*sql.DB, error) {
	connector, err := newConnector(dataSourceName, client)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(connector), nil
}

type networkDriver struct{}

func (networkDriver) Open(dataSourceName string) (driver.Conn, error) {
	connector, err := newConnector(dataSourceName, http.DefaultClient)
	if err != nil {
		return nil, err
	}
	return connector.Connect(context.Background())
}

type connector struct {
	endpoint string
	database string
	user     string
	password string
	client   *http.Client
}

func newConnector(dataSourceName string, sourceClient *http.Client) (*connector, error) {
	parsed, err := url.Parse(dataSourceName)
	if err != nil {
		return nil, fmt.Errorf("kitsql: parse connection URL: %w", err)
	}
	if parsed.Scheme != "kitsql" {
		return nil, fmt.Errorf("kitsql: connection URL must use kitsql://")
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("kitsql: connection URL requires a host")
	}
	if parsed.User == nil || parsed.User.Username() == "" {
		return nil, fmt.Errorf("kitsql: connection URL requires a user")
	}
	password, present := parsed.User.Password()
	if !present || password == "" {
		return nil, fmt.Errorf("kitsql: connection URL requires a password")
	}
	database := strings.Trim(parsed.EscapedPath(), "/")
	if database == "" || strings.Contains(database, "/") {
		return nil, fmt.Errorf("kitsql: connection URL requires exactly one database name")
	}
	database, err = url.PathUnescape(database)
	if err != nil || strings.TrimSpace(database) == "" || strings.ContainsAny(database, "\x00/\\") {
		return nil, fmt.Errorf("kitsql: invalid database name")
	}
	if parsed.Fragment != "" {
		return nil, fmt.Errorf("kitsql: connection URL must not contain a fragment")
	}
	query := parsed.Query()
	for key := range query {
		if key != "sslmode" {
			return nil, fmt.Errorf("kitsql: unsupported connection option %q", key)
		}
	}
	sslMode := query.Get("sslmode")
	if sslMode == "" {
		sslMode = "verify-full"
	}
	httpScheme := "https"
	switch sslMode {
	case "verify-full", "require":
	case "disable":
		if !hostIsLoopback(parsed.Hostname()) {
			return nil, fmt.Errorf("kitsql: sslmode=disable is allowed for loopback hosts only")
		}
		httpScheme = "http"
	default:
		return nil, fmt.Errorf("kitsql: sslmode must be verify-full, require or disable")
	}
	if sourceClient == nil {
		sourceClient = http.DefaultClient
	}
	client := *sourceClient
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return errors.New("kitsql: redirects are not allowed")
	}
	return &connector{
		endpoint: httpScheme + "://" + parsed.Host + Path,
		database: database,
		user:     parsed.User.Username(),
		password: password,
		client:   &client,
	}, nil
}

func hostIsLoopback(host string) bool {
	if strings.EqualFold(strings.TrimSpace(host), "localhost") {
		return true
	}
	address := net.ParseIP(strings.Trim(host, "[]"))
	return address != nil && address.IsLoopback()
}

func (connector *connector) Connect(context.Context) (driver.Conn, error) {
	return &connection{connector: connector}, nil
}

func (connector *connector) Driver() driver.Driver {
	return networkDriver{}
}

type connection struct {
	connector *connector
}

func (connection *connection) Prepare(source string) (driver.Stmt, error) {
	return &statement{connection: connection, source: source}, nil
}

func (connection *connection) PrepareContext(_ context.Context, source string) (driver.Stmt, error) {
	return connection.Prepare(source)
}

func (*connection) Close() error { return nil }

func (*connection) Begin() (driver.Tx, error) { return nil, errTransactionsUnsupported }

func (*connection) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return nil, errTransactionsUnsupported
}

func (connection *connection) Ping(ctx context.Context) error {
	rows, err := connection.QueryContext(ctx, "SELECT 1", nil)
	if err != nil {
		return err
	}
	destination := make([]driver.Value, len(rows.Columns()))
	nextErr := rows.Next(destination)
	closeErr := rows.Close()
	if nextErr != nil && !errors.Is(nextErr, io.EOF) {
		return nextErr
	}
	return closeErr
}

func (connection *connection) ExecContext(
	ctx context.Context,
	source string,
	arguments []driver.NamedValue,
) (driver.Result, error) {
	response, err := connection.send(ctx, ModeExec, source, arguments)
	if err != nil {
		return nil, err
	}
	return executionResult{affected: response.Affected}, nil
}

func (connection *connection) QueryContext(
	ctx context.Context,
	source string,
	arguments []driver.NamedValue,
) (driver.Rows, error) {
	return connection.queryStream(ctx, source, arguments)
}

func (connection *connection) send(
	ctx context.Context,
	mode string,
	source string,
	arguments []driver.NamedValue,
) (Response, error) {
	httpResponse, err := connection.do(ctx, mode, source, arguments, MediaType)
	if err != nil {
		return Response{}, err
	}
	defer httpResponse.Body.Close()
	return decodeResponse(httpResponse)
}

func (connection *connection) do(
	ctx context.Context,
	mode string,
	source string,
	arguments []driver.NamedValue,
	accept string,
) (*http.Response, error) {
	parameters := make([]Value, len(arguments))
	for index, argument := range arguments {
		if argument.Name != "" {
			return nil, fmt.Errorf("kitsql: named parameters are not supported in protocol v1")
		}
		encoded, err := encodeValue(argument.Value)
		if err != nil {
			return nil, fmt.Errorf("kitsql: parameter %d: %w", index+1, err)
		}
		parameters[index] = encoded
	}
	payload, err := json.Marshal(Request{
		Version: Version, Database: connection.connector.database,
		Mode: mode, SQL: source, Parameters: parameters,
	})
	if err != nil {
		return nil, fmt.Errorf("kitsql: encode request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, connection.connector.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("kitsql: create request: %w", err)
	}
	request.Header.Set("Content-Type", MediaType)
	request.Header.Set("Accept", accept)
	request.Header.Set("KitSQL-Version", "1")
	request.SetBasicAuth(connection.connector.user, connection.connector.password)
	httpResponse, err := connection.connector.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("kitsql: request failed: %w", err)
	}
	return httpResponse, nil
}

func decodeResponse(httpResponse *http.Response) (Response, error) {
	decoder := json.NewDecoder(io.LimitReader(httpResponse.Body, maximumResponseBytes+1))
	var response Response
	if err := decodeJSONValue(decoder, &response); err != nil {
		return Response{}, fmt.Errorf("kitsql: decode response: %w", err)
	}
	if err := requireJSONEnd(decoder); err != nil {
		return Response{}, fmt.Errorf("kitsql: invalid response end: %w", err)
	}
	if decoder.InputOffset() > maximumResponseBytes {
		return Response{}, fmt.Errorf("kitsql: response exceeds the byte limit")
	}
	if response.Version != Version {
		return Response{}, fmt.Errorf("kitsql: unsupported response version %d", response.Version)
	}
	if response.Error != nil {
		return Response{}, &ProtocolError{Status: httpResponse.StatusCode, Code: response.Error.Code, Message: response.Error.Message}
	}
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		return Response{}, fmt.Errorf("kitsql: server returned HTTP %d", httpResponse.StatusCode)
	}
	return response, nil
}

func (connection *connection) queryStream(
	ctx context.Context,
	source string,
	arguments []driver.NamedValue,
) (driver.Rows, error) {
	httpResponse, err := connection.do(ctx, ModeQuery, source, arguments, StreamMediaType)
	if err != nil {
		return nil, err
	}
	mediaType := strings.TrimSpace(strings.Split(httpResponse.Header.Get("Content-Type"), ";")[0])
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 || mediaType != StreamMediaType {
		defer httpResponse.Body.Close()
		response, err := decodeResponse(httpResponse)
		if err != nil {
			return nil, err
		}
		return &resultRows{columns: response.Columns, rows: response.Rows}, nil
	}
	decoder := json.NewDecoder(io.LimitReader(httpResponse.Body, maximumResponseBytes+maximumStreamControlBytes+1))
	var first StreamFrame
	if err := decodeJSONValue(decoder, &first); err != nil {
		_ = httpResponse.Body.Close()
		return nil, fmt.Errorf("kitsql: decode stream header: %w", err)
	}
	if first.Version != Version {
		_ = httpResponse.Body.Close()
		return nil, fmt.Errorf("kitsql: unsupported stream version %d", first.Version)
	}
	if first.Type == FrameError && first.Error != nil {
		_ = httpResponse.Body.Close()
		return nil, &ProtocolError{Status: httpResponse.StatusCode, Code: first.Error.Code, Message: first.Error.Message}
	}
	if first.Type != FrameColumns || len(first.Row) != 0 || first.Rows != 0 || first.Error != nil || decoder.InputOffset() > maximumResponseBytes {
		_ = httpResponse.Body.Close()
		return nil, fmt.Errorf("kitsql: stream must begin with a columns frame")
	}
	return &streamRows{
		columns: first.Columns,
		decoder: decoder,
		body:    httpResponse.Body,
		status:  httpResponse.StatusCode,
	}, nil
}

type ProtocolError struct {
	Status  int
	Code    string
	Message string
}

func (err *ProtocolError) Error() string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("kitsql: %s: %s", err.Code, err.Message)
}

// Retryable reports a known rejection, not an automatic replay policy. A
// conflict requires restarting the full logical transaction. Generic timeout,
// transport failure and uncertain durability must never blindly replay a write.
func (err *ProtocolError) Retryable() bool {
	return err != nil && (err.Code == "KSQL_BUSY" || err.Code == "KSQL_ADMISSION_TIMEOUT" || err.Code == "KSQL_CONFLICT")
}

type statement struct {
	connection *connection
	source     string
}

func (*statement) Close() error  { return nil }
func (*statement) NumInput() int { return -1 }

func (statement *statement) Exec(arguments []driver.Value) (driver.Result, error) {
	return statement.ExecContext(context.Background(), positionalArguments(arguments))
}

func (statement *statement) Query(arguments []driver.Value) (driver.Rows, error) {
	return statement.QueryContext(context.Background(), positionalArguments(arguments))
}

func (statement *statement) ExecContext(ctx context.Context, arguments []driver.NamedValue) (driver.Result, error) {
	return statement.connection.ExecContext(ctx, statement.source, arguments)
}

func (statement *statement) QueryContext(ctx context.Context, arguments []driver.NamedValue) (driver.Rows, error) {
	return statement.connection.QueryContext(ctx, statement.source, arguments)
}

func positionalArguments(arguments []driver.Value) []driver.NamedValue {
	result := make([]driver.NamedValue, len(arguments))
	for index, value := range arguments {
		result[index] = driver.NamedValue{Ordinal: index + 1, Value: value}
	}
	return result
}

type executionResult struct{ affected int64 }

func (executionResult) LastInsertId() (int64, error) {
	return 0, errors.New("kitsql: last insert id is not available; use RETURNING")
}

func (result executionResult) RowsAffected() (int64, error) { return result.affected, nil }

type resultRows struct {
	columns []Column
	rows    [][]Value
	index   int
}

func (rows *resultRows) Columns() []string {
	result := make([]string, len(rows.columns))
	for index, column := range rows.columns {
		result[index] = column.Name
	}
	return result
}

func (*resultRows) Close() error { return nil }

func (rows *resultRows) Next(destination []driver.Value) error {
	if rows.index >= len(rows.rows) {
		return io.EOF
	}
	current := rows.rows[rows.index]
	if len(current) != len(rows.columns) || len(destination) < len(current) {
		return fmt.Errorf("kitsql: malformed row width")
	}
	for index, encoded := range current {
		value, err := encoded.decode()
		if err != nil {
			return fmt.Errorf("kitsql: column %d: %w", index+1, err)
		}
		destination[index] = value
	}
	rows.index++
	return nil
}

func (rows *resultRows) ColumnTypeDatabaseTypeName(index int) string {
	if index < 0 || index >= len(rows.columns) {
		return ""
	}
	return rows.columns[index].DatabaseType
}

type streamRows struct {
	columns []Column
	decoder *json.Decoder
	body    io.ReadCloser
	status  int
	done    bool
	count   int64
}

func (rows *streamRows) Columns() []string {
	result := make([]string, len(rows.columns))
	for index, column := range rows.columns {
		result[index] = column.Name
	}
	return result
}

func (rows *streamRows) Close() error {
	if rows == nil || rows.body == nil {
		return nil
	}
	body := rows.body
	rows.body = nil
	rows.done = true
	return body.Close()
}

func (rows *streamRows) Next(destination []driver.Value) error {
	if rows == nil || rows.done {
		return io.EOF
	}
	var frame StreamFrame
	if err := decodeJSONValue(rows.decoder, &frame); err != nil {
		_ = rows.Close()
		if errors.Is(err, io.EOF) {
			return io.ErrUnexpectedEOF
		}
		return fmt.Errorf("kitsql: decode stream frame: %w", err)
	}
	if frame.Version != Version {
		_ = rows.Close()
		return fmt.Errorf("kitsql: unsupported stream version %d", frame.Version)
	}
	limit := maximumResponseBytes
	if frame.Type == FrameError {
		limit += maximumStreamControlBytes
	}
	if rows.decoder.InputOffset() > limit {
		_ = rows.Close()
		return fmt.Errorf("kitsql: stream exceeds the byte limit")
	}
	switch frame.Type {
	case FrameRow:
		if frame.Error != nil || len(frame.Columns) != 0 || frame.Rows != 0 {
			_ = rows.Close()
			return fmt.Errorf("kitsql: malformed row frame")
		}
		if len(frame.Row) != len(rows.columns) || len(destination) < len(frame.Row) {
			_ = rows.Close()
			return fmt.Errorf("kitsql: malformed streamed row width")
		}
		for index, encoded := range frame.Row {
			value, err := encoded.decode()
			if err != nil {
				_ = rows.Close()
				return fmt.Errorf("kitsql: column %d: %w", index+1, err)
			}
			destination[index] = value
		}
		rows.count++
		return nil
	case FrameComplete:
		_ = rows.Close()
		if frame.Rows != rows.count || frame.Error != nil || len(frame.Columns) != 0 || len(frame.Row) != 0 {
			return fmt.Errorf("kitsql: malformed completion frame: received %d rows, declared %d", rows.count, frame.Rows)
		}
		return io.EOF
	case FrameError:
		_ = rows.Close()
		if frame.Error == nil || len(frame.Columns) != 0 || len(frame.Row) != 0 || frame.Rows != 0 {
			return fmt.Errorf("kitsql: malformed stream error frame")
		}
		return &ProtocolError{Status: rows.status, Code: frame.Error.Code, Message: frame.Error.Message}
	default:
		_ = rows.Close()
		return fmt.Errorf("kitsql: unexpected stream frame %q", frame.Type)
	}
}

func (rows *streamRows) ColumnTypeDatabaseTypeName(index int) string {
	if index < 0 || index >= len(rows.columns) {
		return ""
	}
	return rows.columns[index].DatabaseType
}
