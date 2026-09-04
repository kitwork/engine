package pgwire

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

const (
	defaultMaxConnections      = 64
	defaultMaxConcurrentCopies = 2
	defaultMaxCopiesPerKey     = 1
	defaultMaxQueuedCopies     = 64
	defaultMaxQueuedPerKey     = 8
	defaultMaxMessageBytes     = 1 << 20
	defaultIdleTimeout         = 5 * time.Minute
	defaultQueryTimeout        = 30 * time.Second
	defaultCopyTimeout         = 30 * time.Minute
	defaultWriteTimeout        = 10 * time.Second
	defaultMaxCopyBytes        = int64(64 << 20)
	maxStartupBytes            = 64 << 10
)

var errTerminateConnection = errors.New("pgwire: terminate connection")

// Server is the first bounded KitDB PostgreSQL wire profile. It deliberately
// supports cleartext password exchange on loopback only. TLS and SCRAM belong
// in a later network-facing profile.
type Server struct {
	Authenticator             Authenticator
	MaxConnections            int
	MaxConcurrentCopies       int
	MaxConcurrentCopiesPerKey int
	MaxQueuedCopies           int
	MaxQueuedCopiesPerKey     int
	MaxMessageBytes           int
	MaxCopyBytes              int64
	IdleTimeout               time.Duration
	QueryTimeout              time.Duration
	CopyTimeout               time.Duration
	WriteTimeout              time.Duration
	CopyMetrics               *CopyMetrics
}

type serverRuntime struct {
	server        Server
	connections   sync.Map
	cancels       sync.Map
	nextPID       atomic.Uint32
	copyAdmission *copyAdmissionScheduler
}

// CopySnapshot is a race-free point-in-time view of server-wide COPY
// admission. Bytes count frontend CopyData payloads, not decoded row storage.
type CopySnapshot struct {
	Active          int64
	Peak            int64
	Queued          int64
	PeakQueued      int64
	Acquired        uint64
	Completed       uint64
	Failed          uint64
	WaitTimeouts    uint64
	Rejected        uint64
	Bytes           uint64
	WaitNanoseconds uint64
}

// CopyMetrics is optional shared telemetry for one Server. Its zero value is
// ready for use and Snapshot may be called concurrently with Serve.
type CopyMetrics struct {
	active          atomic.Int64
	peak            atomic.Int64
	queued          atomic.Int64
	peakQueued      atomic.Int64
	acquired        atomic.Uint64
	completed       atomic.Uint64
	failed          atomic.Uint64
	waitTimeouts    atomic.Uint64
	rejected        atomic.Uint64
	bytes           atomic.Uint64
	waitNanoseconds atomic.Uint64
}

func (metrics *CopyMetrics) Snapshot() CopySnapshot {
	if metrics == nil {
		return CopySnapshot{}
	}
	return CopySnapshot{
		Active: metrics.active.Load(), Peak: metrics.peak.Load(),
		Queued: metrics.queued.Load(), PeakQueued: metrics.peakQueued.Load(),
		Acquired: metrics.acquired.Load(), Completed: metrics.completed.Load(),
		Failed: metrics.failed.Load(), WaitTimeouts: metrics.waitTimeouts.Load(),
		Rejected: metrics.rejected.Load(),
		Bytes:    metrics.bytes.Load(), WaitNanoseconds: metrics.waitNanoseconds.Load(),
	}
}

type cancelSlot struct {
	secret uint32
	mu     sync.Mutex
	cancel context.CancelFunc
}

func (slot *cancelSlot) set(cancel context.CancelFunc) {
	slot.mu.Lock()
	slot.cancel = cancel
	slot.mu.Unlock()
}

func (slot *cancelSlot) clear() {
	slot.mu.Lock()
	slot.cancel = nil
	slot.mu.Unlock()
}

func (slot *cancelSlot) invoke(secret uint32) {
	if slot.secret != secret {
		return
	}
	slot.mu.Lock()
	cancel := slot.cancel
	slot.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (server Server) withDefaults() Server {
	if server.MaxConnections <= 0 {
		server.MaxConnections = defaultMaxConnections
	}
	if server.MaxConcurrentCopies <= 0 {
		server.MaxConcurrentCopies = defaultMaxConcurrentCopies
	}
	if server.MaxConcurrentCopies > server.MaxConnections {
		server.MaxConcurrentCopies = server.MaxConnections
	}
	if server.MaxConcurrentCopiesPerKey <= 0 {
		server.MaxConcurrentCopiesPerKey = defaultMaxCopiesPerKey
	}
	if server.MaxConcurrentCopiesPerKey > server.MaxConcurrentCopies {
		server.MaxConcurrentCopiesPerKey = server.MaxConcurrentCopies
	}
	if server.MaxQueuedCopies <= 0 {
		server.MaxQueuedCopies = defaultMaxQueuedCopies
	}
	if server.MaxQueuedCopies > server.MaxConnections {
		server.MaxQueuedCopies = server.MaxConnections
	}
	if server.MaxQueuedCopiesPerKey <= 0 {
		server.MaxQueuedCopiesPerKey = defaultMaxQueuedPerKey
	}
	if server.MaxQueuedCopiesPerKey > server.MaxQueuedCopies {
		server.MaxQueuedCopiesPerKey = server.MaxQueuedCopies
	}
	if server.MaxMessageBytes <= 0 {
		server.MaxMessageBytes = defaultMaxMessageBytes
	}
	if server.IdleTimeout <= 0 {
		server.IdleTimeout = defaultIdleTimeout
	}
	if server.QueryTimeout <= 0 {
		server.QueryTimeout = defaultQueryTimeout
	}
	if server.CopyTimeout <= 0 {
		server.CopyTimeout = defaultCopyTimeout
	}
	if server.MaxCopyBytes <= 0 {
		server.MaxCopyBytes = defaultMaxCopyBytes
	}
	if server.WriteTimeout <= 0 {
		server.WriteTimeout = defaultWriteTimeout
	}
	if server.CopyMetrics == nil {
		server.CopyMetrics = &CopyMetrics{}
	}
	return server
}

// Serve accepts PostgreSQL protocol 3.0 connections until ctx is canceled.
// The cleartext local profile rejects non-loopback listeners by construction.
func (server Server) Serve(ctx context.Context, listener net.Listener) error {
	server = server.withDefaults()
	if ctx == nil {
		return fmt.Errorf("pgwire: context is nil")
	}
	if server.Authenticator == nil {
		return fmt.Errorf("pgwire: authenticator is nil")
	}
	if err := ValidateCleartextListener(listener); err != nil {
		return err
	}

	runtime := &serverRuntime{server: server}
	runtime.copyAdmission = newCopyAdmissionScheduler(
		server.MaxConcurrentCopies,
		server.MaxConcurrentCopiesPerKey,
		server.MaxQueuedCopies,
		server.MaxQueuedCopiesPerKey,
		server.CopyMetrics,
	)
	runtime.nextPID.Store(uint32(os.Getpid()) & 0x3fffffff)
	semaphore := make(chan struct{}, server.MaxConnections)
	var workers sync.WaitGroup
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = listener.Close()
			runtime.connections.Range(func(key, _ any) bool {
				_ = key.(net.Conn).Close()
				return true
			})
		case <-done:
		}
	}()
	defer func() {
		close(done)
		runtime.connections.Range(func(key, _ any) bool {
			_ = key.(net.Conn).Close()
			return true
		})
		workers.Wait()
	}()

	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Temporary() {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			return fmt.Errorf("pgwire: accept: %w", err)
		}

		select {
		case semaphore <- struct{}{}:
		case <-ctx.Done():
			_ = connection.Close()
			return nil
		default:
			writer := bufio.NewWriter(connection)
			_ = writeError(writer, NewError("53300", "too many KitDB PostgreSQL connections"))
			_ = writer.Flush()
			_ = connection.Close()
			continue
		}

		runtime.connections.Store(connection, struct{}{})
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer func() { <-semaphore }()
			defer runtime.connections.Delete(connection)
			defer connection.Close()
			_ = runtime.serveConnection(ctx, connection)
		}()
	}
}

// ValidateCleartextListener checks the network boundary without accepting a
// connection. Adapters use it before touching their local persistence so an
// invalid public listener fails before any startup side effect.
func ValidateCleartextListener(listener net.Listener) error {
	if listener == nil {
		return fmt.Errorf("pgwire: listener is nil")
	}
	if !loopbackListener(listener.Addr()) {
		return fmt.Errorf("pgwire: the cleartext profile accepts loopback listeners only")
	}
	return nil
}

func loopbackListener(address net.Addr) bool {
	if address == nil {
		return false
	}
	host, _, err := net.SplitHostPort(address.String())
	if err != nil {
		return false
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

type wireConnection struct {
	runtime  *serverRuntime
	conn     net.Conn
	reader   *bufio.Reader
	writer   *bufio.Writer
	session  Session
	startup  Startup
	pid      uint32
	secret   uint32
	cancel   *cancelSlot
	prepared map[string]*preparedStatement
	portals  map[string]*portal
	failed   bool
}

type preparedStatement struct {
	query      string
	parameters []uint32
	columns    []Column
	described  bool
}

type portal struct {
	statement       *preparedStatement
	parameters      []Parameter
	resultFormats   []int16
	result          *Result
	position        int
	descriptionSent bool
}

func (runtime *serverRuntime) serveConnection(ctx context.Context, connection net.Conn) error {
	wire := &wireConnection{
		runtime: runtime, conn: connection,
		reader:   bufio.NewReaderSize(connection, 32<<10),
		writer:   bufio.NewWriterSize(connection, 32<<10),
		prepared: map[string]*preparedStatement{}, portals: map[string]*portal{},
	}
	if err := wire.start(ctx); err != nil {
		return err
	}
	defer wire.close()
	return wire.run(ctx)
}

func (wire *wireConnection) start(ctx context.Context) error {
	for {
		packet, err := wire.readStartup()
		if err != nil {
			return err
		}
		decoder := decoder{data: packet}
		code, err := decoder.uint32()
		if err != nil {
			return err
		}
		switch code {
		case sslRequestCode, gssRequestCode:
			if err := decoder.done(); err != nil {
				return err
			}
			if err := wire.writeRaw([]byte{'N'}); err != nil {
				return err
			}
			continue
		case cancelRequestCode:
			pid, err := decoder.uint32()
			if err != nil {
				return err
			}
			secret, err := decoder.uint32()
			if err != nil {
				return err
			}
			if err := decoder.done(); err != nil {
				return err
			}
			if item, found := wire.runtime.cancels.Load(pid); found {
				item.(*cancelSlot).invoke(secret)
			}
			return io.EOF
		case ProtocolVersion30:
			startup, err := decodeStartup(&decoder)
			if err != nil {
				_ = wire.sendError(NewError("08P01", err.Error()))
				return err
			}
			wire.startup = startup
			return wire.authenticate(ctx)
		default:
			err := NewError("0A000", fmt.Sprintf("unsupported PostgreSQL protocol version %d", code))
			_ = wire.sendError(err)
			return err
		}
	}
}

func decodeStartup(decoder *decoder) (Startup, error) {
	parameters := map[string]string{}
	for decoder.remaining() > 0 {
		key, err := decoder.cstring()
		if err != nil {
			return Startup{}, err
		}
		if key == "" {
			if err := decoder.done(); err != nil {
				return Startup{}, err
			}
			return Startup{Parameters: parameters}, nil
		}
		value, err := decoder.cstring()
		if err != nil {
			return Startup{}, err
		}
		if len(key) > 128 || len(value) > 4096 {
			return Startup{}, fmt.Errorf("pgwire: startup parameter is too large")
		}
		parameters[key] = value
	}
	return Startup{}, fmt.Errorf("pgwire: startup packet has no terminator")
}

func (wire *wireConnection) authenticate(ctx context.Context) error {
	var auth encoder
	auth.int32(3)
	if err := wire.send('R', auth); err != nil {
		return err
	}
	if err := wire.writer.Flush(); err != nil {
		return err
	}

	kind, body, err := wire.readMessage()
	if err != nil {
		return err
	}
	if kind != 'p' {
		err := NewError("08P01", "expected PostgreSQL password message")
		_ = wire.sendError(err)
		return err
	}
	passwordDecoder := decoder{data: body}
	password, err := passwordDecoder.cstring()
	if err != nil || passwordDecoder.done() != nil {
		protocolErr := NewError("08P01", "invalid PostgreSQL password message")
		_ = wire.sendError(protocolErr)
		return protocolErr
	}

	session, err := wire.runtime.server.Authenticator.Authenticate(ctx, wire.startup, password)
	if err != nil {
		_ = wire.sendError(err)
		return err
	}
	wire.session = session
	wire.pid = wire.runtime.nextPID.Add(1) & 0x7fffffff
	wire.secret = randomUint32()
	wire.cancel = &cancelSlot{secret: wire.secret}
	wire.runtime.cancels.Store(wire.pid, wire.cancel)

	var ok encoder
	ok.int32(0)
	if err := wire.send('R', ok); err != nil {
		return err
	}
	for _, parameter := range [][2]string{
		{"server_version", "16.0"},
		{"server_encoding", "UTF8"},
		{"client_encoding", "UTF8"},
		{"application_name", wire.startup.Parameters["application_name"]},
		{"DateStyle", "ISO, MDY"},
		{"integer_datetimes", "on"},
		{"standard_conforming_strings", "on"},
		{"TimeZone", "UTC"},
	} {
		if parameter[1] == "" && parameter[0] == "application_name" {
			continue
		}
		var payload encoder
		payload.cstring(parameter[0])
		payload.cstring(parameter[1])
		if err := wire.send('S', payload); err != nil {
			return err
		}
	}
	var key encoder
	key.uint32(wire.pid)
	key.uint32(wire.secret)
	if err := wire.send('K', key); err != nil {
		return err
	}
	if err := wire.ready(); err != nil {
		return err
	}
	return wire.writer.Flush()
}

func randomUint32() uint32 {
	var data [4]byte
	if _, err := rand.Read(data[:]); err != nil {
		return uint32(time.Now().UnixNano())
	}
	return binary.BigEndian.Uint32(data[:])
}

func (wire *wireConnection) close() {
	if wire.cancel != nil {
		wire.cancel.clear()
		wire.runtime.cancels.Delete(wire.pid)
	}
	if wire.session != nil {
		_ = wire.session.Close()
	}
}

func (wire *wireConnection) run(ctx context.Context) error {
	for {
		kind, body, err := wire.readMessage()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return nil
			}
			return err
		}
		if wire.failed && kind != 'S' && kind != 'X' {
			continue
		}
		switch kind {
		case 'Q':
			err = wire.simpleQuery(ctx, body)
		case 'P':
			err = wire.parse(body)
		case 'B':
			err = wire.bind(body)
		case 'D':
			err = wire.describe(ctx, body)
		case 'E':
			err = wire.execute(ctx, body)
		case 'C':
			err = wire.closeObject(body)
		case 'H':
			err = wire.emptyBody(body)
		case 'S':
			err = wire.emptyBody(body)
			wire.failed = false
			if err == nil {
				err = wire.ready()
			}
		case 'X':
			return nil
		default:
			err = NewError("0A000", fmt.Sprintf("PostgreSQL frontend message %q is not supported", kind))
		}
		if errors.Is(err, errTerminateConnection) {
			return nil
		}
		if err != nil {
			_ = wire.sendError(err)
			if kind != 'Q' {
				wire.failed = true
			}
		}
		if flushErr := wire.writer.Flush(); flushErr != nil {
			return flushErr
		}
	}
}

func (wire *wireConnection) simpleQuery(ctx context.Context, body []byte) error {
	decoder := decoder{data: body}
	query, err := decoder.cstring()
	if err != nil || decoder.done() != nil {
		return NewError("08P01", "invalid PostgreSQL Query message")
	}
	if emptyQuery(query) {
		if err := wire.send('I', nil); err != nil {
			return err
		}
		return wire.ready()
	}
	if result, handled, copyErr := wire.runCopyIn(ctx, query, nil); handled {
		if copyErr != nil {
			if errors.Is(copyErr, errTerminateConnection) {
				return copyErr
			}
			_ = wire.sendError(copyErr)
			return wire.ready()
		}
		if err := wire.sendCommand(result.CommandTag); err != nil {
			return err
		}
		return wire.ready()
	}
	result, err := wire.runQuery(ctx, query, nil)
	if err != nil {
		_ = wire.sendError(err)
		return wire.ready()
	}
	if err := wire.sendResult(result, 0, true); err != nil {
		return err
	}
	return wire.ready()
}

func emptyQuery(query string) bool {
	return strings.Trim(strings.TrimSpace(query), ";") == ""
}

func (wire *wireConnection) parse(body []byte) error {
	decoder := decoder{data: body}
	name, err := decoder.cstring()
	if err != nil {
		return NewError("08P01", err.Error())
	}
	query, err := decoder.cstring()
	if err != nil {
		return NewError("08P01", err.Error())
	}
	count, err := decoder.int16()
	if err != nil || count < 0 || count > 1024 {
		return NewError("08P01", "invalid PostgreSQL Parse parameter count")
	}
	parameterCount := postgresParameterCount(query)
	if int(count) > parameterCount {
		parameterCount = int(count)
	}
	parameters := make([]uint32, parameterCount)
	for index := 0; index < int(count); index++ {
		parameters[index], err = decoder.uint32()
		if err != nil {
			return NewError("08P01", err.Error())
		}
	}
	if err := decoder.done(); err != nil {
		return NewError("08P01", err.Error())
	}
	if name == "" {
		delete(wire.prepared, "")
		delete(wire.portals, "")
	} else if _, exists := wire.prepared[name]; exists {
		return NewError("42P05", "prepared statement already exists")
	}
	wire.prepared[name] = &preparedStatement{query: query, parameters: parameters}
	return wire.send('1', nil)
}

func postgresParameterCount(query string) int {
	maximum := 0
	for index := 0; index < len(query); {
		if query[index] == '\'' || query[index] == '"' {
			quote := query[index]
			index++
			for index < len(query) {
				if query[index] != quote {
					index++
					continue
				}
				if index+1 < len(query) && query[index+1] == quote {
					index += 2
					continue
				}
				index++
				break
			}
			continue
		}
		if index+1 < len(query) && query[index] == '-' && query[index+1] == '-' {
			index += 2
			for index < len(query) && query[index] != '\n' && query[index] != '\r' {
				index++
			}
			continue
		}
		if index+1 < len(query) && query[index] == '/' && query[index+1] == '*' {
			index += 2
			for index+1 < len(query) && !(query[index] == '*' && query[index+1] == '/') {
				index++
			}
			if index+1 < len(query) {
				index += 2
			}
			continue
		}
		if query[index] != '$' {
			index++
			continue
		}
		value := 0
		cursor := index + 1
		for cursor < len(query) && query[cursor] >= '0' && query[cursor] <= '9' {
			value = value*10 + int(query[cursor]-'0')
			cursor++
		}
		if value > maximum {
			maximum = value
		}
		if cursor == index+1 {
			index++
		} else {
			index = cursor
		}
	}
	return maximum
}

func (wire *wireConnection) bind(body []byte) error {
	decoder := decoder{data: body}
	portalName, err := decoder.cstring()
	if err != nil {
		return NewError("08P01", err.Error())
	}
	statementName, err := decoder.cstring()
	if err != nil {
		return NewError("08P01", err.Error())
	}
	statement := wire.prepared[statementName]
	if statement == nil {
		return NewError("26000", "prepared statement does not exist")
	}
	formatCount, err := decoder.int16()
	if err != nil || formatCount < 0 || formatCount > 1024 {
		return NewError("08P01", "invalid PostgreSQL Bind format count")
	}
	formats := make([]int16, formatCount)
	for index := range formats {
		formats[index], err = decoder.int16()
		if err != nil || (formats[index] != 0 && formats[index] != 1) {
			return NewError("08P01", "unsupported PostgreSQL parameter format")
		}
	}
	parameterCount, err := decoder.int16()
	if err != nil || parameterCount < 0 || parameterCount > 1024 {
		return NewError("08P01", "invalid PostgreSQL Bind parameter count")
	}
	if int(parameterCount) != len(statement.parameters) {
		return NewError("08P01", fmt.Sprintf("Bind supplies %d parameters, statement requires %d", parameterCount, len(statement.parameters)))
	}
	parameters := make([]Parameter, parameterCount)
	for index := range parameters {
		length, readErr := decoder.int32()
		if readErr != nil || length < -1 || length > int32(wire.runtime.server.MaxMessageBytes) {
			return NewError("08P01", "invalid PostgreSQL Bind parameter length")
		}
		format := int16(0)
		if len(formats) == 1 {
			format = formats[0]
		} else if len(formats) == len(parameters) {
			format = formats[index]
		} else if len(formats) != 0 {
			return NewError("08P01", "Bind format count must be zero, one, or parameter count")
		}
		parameters[index] = Parameter{OID: statement.parameters[index], Format: format, Null: length == -1}
		if length >= 0 {
			data, readErr := decoder.bytes(int(length))
			if readErr != nil {
				return NewError("08P01", readErr.Error())
			}
			parameters[index].Data = append([]byte(nil), data...)
		}
	}
	resultFormatCount, err := decoder.int16()
	if err != nil || resultFormatCount < 0 || resultFormatCount > 1024 {
		return NewError("08P01", "invalid PostgreSQL Bind result format count")
	}
	resultFormats := make([]int16, resultFormatCount)
	for index := 0; index < int(resultFormatCount); index++ {
		format, readErr := decoder.int16()
		if readErr != nil || (format != 0 && format != 1) {
			return NewError("08P01", "unsupported PostgreSQL result format")
		}
		resultFormats[index] = format
	}
	if err := decoder.done(); err != nil {
		return NewError("08P01", err.Error())
	}
	if portalName == "" {
		delete(wire.portals, "")
	} else if _, exists := wire.portals[portalName]; exists {
		return NewError("42P03", "portal already exists")
	}
	wire.portals[portalName] = &portal{
		statement: statement, parameters: parameters, resultFormats: resultFormats,
		descriptionSent: statement.described,
	}
	return wire.send('2', nil)
}

func (wire *wireConnection) describe(ctx context.Context, body []byte) error {
	decoder := decoder{data: body}
	kind, err := decoder.byte()
	if err != nil {
		return NewError("08P01", err.Error())
	}
	name, err := decoder.cstring()
	if err != nil || decoder.done() != nil {
		return NewError("08P01", "invalid PostgreSQL Describe message")
	}
	switch kind {
	case 'S':
		statement := wire.prepared[name]
		if statement == nil {
			return NewError("26000", "prepared statement does not exist")
		}
		var parameters encoder
		parameters.int16(int16(len(statement.parameters)))
		for _, oid := range statement.parameters {
			parameters.uint32(oid)
		}
		if err := wire.send('t', parameters); err != nil {
			return err
		}
		if isReadQuery(statement.query) {
			nulls := make([]Parameter, len(statement.parameters))
			for index := range nulls {
				nulls[index] = Parameter{OID: statement.parameters[index], Null: true}
			}
			if describer, ok := wire.session.(DescribeSession); ok {
				columns, describeErr := describer.Describe(ctx, statement.query, nulls)
				if describeErr != nil {
					return describeErr
				}
				statement.columns = append([]Column(nil), columns...)
			} else {
				result, queryErr := wire.runQuery(ctx, statement.query, nulls)
				if queryErr != nil {
					return queryErr
				}
				statement.columns = append([]Column(nil), result.Columns...)
			}
			statement.described = true
			return wire.sendRowDescription(statement.columns, nil)
		}
		statement.described = true
		return wire.send('n', nil)
	case 'P':
		portal := wire.portals[name]
		if portal == nil {
			return NewError("34000", "portal does not exist")
		}
		if isReadQuery(portal.statement.query) {
			if portal.result == nil {
				result, queryErr := wire.runQuery(ctx, portal.statement.query, portal.parameters)
				if queryErr != nil {
					return queryErr
				}
				portal.result = &result
			}
			portal.descriptionSent = true
			return wire.sendRowDescription(portal.result.Columns, portal.resultFormats)
		}
		portal.descriptionSent = true
		return wire.send('n', nil)
	default:
		return NewError("08P01", "Describe target must be statement or portal")
	}
}

func isReadQuery(query string) bool {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(query)))
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "select", "with", "show", "explain", "pragma":
		return true
	case "insert", "update", "delete":
		if statement, err := kitdbsql.ParseStatement(query); err == nil {
			switch statement.Kind {
			case kitdbsql.StatementInsert:
				return len(statement.Insert.Returning) != 0
			case kitdbsql.StatementUpdate:
				return len(statement.Update.Returning) != 0
			case kitdbsql.StatementDelete:
				return len(statement.Delete.Returning) != 0
			}
		}
		tokens, err := kitdbsql.Lex(query)
		if err != nil {
			return false
		}
		for _, token := range tokens {
			quoted := token.Start >= 0 && token.Start < len(query) && query[token.Start] == '"'
			if !quoted && token.Kind == kitdbsql.TokenIdentifier && strings.EqualFold(token.Text, "returning") {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func (wire *wireConnection) execute(ctx context.Context, body []byte) error {
	decoder := decoder{data: body}
	name, err := decoder.cstring()
	if err != nil {
		return NewError("08P01", err.Error())
	}
	maximum, err := decoder.int32()
	if err != nil || maximum < 0 || decoder.done() != nil {
		return NewError("08P01", "invalid PostgreSQL Execute message")
	}
	portal := wire.portals[name]
	if portal == nil {
		return NewError("34000", "portal does not exist")
	}
	if portal.result == nil {
		result, handled, queryErr := wire.runCopyIn(ctx, portal.statement.query, portal.parameters)
		if !handled {
			result, queryErr = wire.runQuery(ctx, portal.statement.query, portal.parameters)
		}
		if queryErr != nil {
			return queryErr
		}
		portal.result = &result
	}
	if !portal.descriptionSent && len(portal.result.Columns) > 0 {
		if err := wire.sendRowDescription(portal.result.Columns, portal.resultFormats); err != nil {
			return err
		}
		portal.descriptionSent = true
	}
	start := portal.position
	end := len(portal.result.Rows)
	if maximum > 0 && end-start > int(maximum) {
		end = start + int(maximum)
	}
	for _, row := range portal.result.Rows[start:end] {
		if err := wire.sendDataRow(row, portal.result.Columns, portal.resultFormats); err != nil {
			return err
		}
	}
	portal.position = end
	if end < len(portal.result.Rows) {
		return wire.send('s', nil)
	}
	return wire.sendCommand(portal.result.CommandTag)
}

func (wire *wireConnection) closeObject(body []byte) error {
	decoder := decoder{data: body}
	kind, err := decoder.byte()
	if err != nil {
		return NewError("08P01", err.Error())
	}
	name, err := decoder.cstring()
	if err != nil || decoder.done() != nil {
		return NewError("08P01", "invalid PostgreSQL Close message")
	}
	switch kind {
	case 'S':
		delete(wire.prepared, name)
	case 'P':
		delete(wire.portals, name)
	default:
		return NewError("08P01", "Close target must be statement or portal")
	}
	return wire.send('3', nil)
}

func (wire *wireConnection) emptyBody(body []byte) error {
	if len(body) != 0 {
		return NewError("08P01", "PostgreSQL control message must be empty")
	}
	return nil
}

func (wire *wireConnection) runQuery(parent context.Context, query string, parameters []Parameter) (Result, error) {
	ctx, cancel := context.WithTimeout(parent, wire.runtime.server.QueryTimeout)
	wire.cancel.set(cancel)
	defer wire.cancel.clear()
	defer cancel()

	result, err := wire.session.Execute(ctx, query, parameters)
	if ctx.Err() != nil {
		return Result{}, NewError("57014", "KitDB query canceled or timed out")
	}
	return result, err
}

func (metrics *CopyMetrics) recordCopyAcquire(wait time.Duration) {
	metrics.acquired.Add(1)
	metrics.waitNanoseconds.Add(uint64(max(wait.Nanoseconds(), 0)))
	active := metrics.active.Add(1)
	for {
		peak := metrics.peak.Load()
		if active <= peak || metrics.peak.CompareAndSwap(peak, active) {
			return
		}
	}
}

func (metrics *CopyMetrics) recordCopyRelease(bytes int64, handled bool, err error) {
	if bytes > 0 {
		metrics.bytes.Add(uint64(bytes))
	}
	metrics.active.Add(-1)
	if !handled {
		return
	}
	if err == nil {
		metrics.completed.Add(1)
	} else {
		metrics.failed.Add(1)
	}
}

func (metrics *CopyMetrics) recordCopyWaitTimeout(wait time.Duration) {
	metrics.waitTimeouts.Add(1)
	metrics.waitNanoseconds.Add(uint64(max(wait.Nanoseconds(), 0)))
}

func copyInCandidate(query string) bool {
	query = strings.TrimSpace(query)
	if len(query) < len("copy") || !strings.EqualFold(query[:len("copy")], "copy") {
		return false
	}
	return len(query) == len("copy") || query[len("copy")] <= ' '
}

func (wire *wireConnection) runCopyIn(
	parent context.Context,
	query string,
	parameters []Parameter,
) (result Result, handled bool, resultErr error) {
	session, available := wire.session.(CopyInSession)
	if !available || !copyInCandidate(query) {
		return Result{}, false, nil
	}
	ctx, cancel := context.WithTimeout(parent, wire.runtime.server.CopyTimeout)
	wire.cancel.set(cancel)
	releaseCopy, admissionErr := wire.runtime.copyAdmission.acquire(ctx, copyAdmissionFor(wire.session))
	if admissionErr != nil {
		wire.cancel.clear()
		cancel()
		if errors.Is(admissionErr, errCopyAdmissionQueueFull) {
			return Result{}, true, NewError("53300", "KitDB COPY admission queue is full")
		}
		return Result{}, true, NewError("57014", "KitDB COPY admission canceled or timed out")
	}
	var total int64
	defer func() {
		releaseCopy(total, handled, resultErr)
	}()
	request, copyHandled, err := session.BeginCopyIn(ctx, query, parameters)
	handled = copyHandled
	if !handled {
		wire.cancel.clear()
		cancel()
		return Result{}, false, nil
	}
	if err != nil {
		wire.cancel.clear()
		cancel()
		return Result{}, true, err
	}
	if err := validateCopyInRequest(request); err != nil {
		wire.cancel.clear()
		cancel()
		if request.Stream != nil {
			_ = request.Stream.Abort(err)
		}
		return Result{}, true, err
	}
	if ctx.Err() != nil || (request.Lifetime != nil && request.Lifetime.Err() != nil) {
		err := NewError("57014", "KitDB COPY canceled or timed out")
		wire.cancel.clear()
		cancel()
		_ = request.Stream.Abort(err)
		return Result{}, true, err
	}
	deadlineDone := make(chan struct{})
	deadlineStop := context.AfterFunc(ctx, func() {
		_ = wire.conn.SetReadDeadline(time.Now())
		close(deadlineDone)
	})
	var lifetimeStop func() bool
	var lifetimeDone chan struct{}
	if request.Lifetime != nil {
		lifetimeDone = make(chan struct{})
		lifetimeStop = context.AfterFunc(request.Lifetime, func() {
			_ = wire.conn.SetReadDeadline(time.Now())
			close(lifetimeDone)
		})
	}
	defer func() {
		stopCopyDeadline(deadlineStop, deadlineDone)
		stopCopyDeadline(lifetimeStop, lifetimeDone)
		wire.cancel.clear()
		cancel()
		_ = wire.conn.SetReadDeadline(time.Time{})
	}()

	if err := wire.sendCopyInResponse(request); err != nil {
		_ = request.Stream.Abort(err)
		return Result{}, true, err
	}
	if err := wire.writer.Flush(); err != nil {
		_ = request.Stream.Abort(err)
		return Result{}, true, err
	}

	var copyErr error
	aborted := false
	draining := false
	abort := func(err error) {
		if aborted {
			return
		}
		aborted = true
		draining = true
		if abortErr := request.Stream.Abort(err); copyErr == nil && abortErr != nil {
			copyErr = abortErr
		}
	}
	for {
		readTimeout := wire.runtime.server.IdleTimeout
		if draining && readTimeout > wire.runtime.server.WriteTimeout {
			readTimeout = wire.runtime.server.WriteTimeout
		}
		kind, body, readErr := wire.readMessageWithin(readTimeout)
		if readErr != nil {
			if ctx.Err() != nil ||
				(request.Lifetime != nil && request.Lifetime.Err() != nil) {
				if copyErr == nil {
					copyErr = NewError("57014", "KitDB COPY canceled or timed out")
					abort(copyErr)
					draining = true
					continue
				}
			}
			abort(readErr)
			return Result{}, true, errTerminateConnection
		}
		switch kind {
		case 'd':
			total += int64(len(body))
			if copyErr == nil && total > wire.runtime.server.MaxCopyBytes {
				copyErr = NewError(
					"54000",
					fmt.Sprintf("KitDB COPY exceeds %d bytes", wire.runtime.server.MaxCopyBytes),
				)
				abort(copyErr)
			}
			if copyErr == nil {
				if writeErr := request.Stream.Write(ctx, body); writeErr != nil {
					copyErr = writeErr
					abort(copyErr)
				}
			}
		case 'c':
			if len(body) != 0 {
				copyErr = NewError("08P01", "invalid PostgreSQL CopyDone message")
				abort(copyErr)
				return Result{}, true, copyErr
			}
			if copyErr != nil {
				return Result{}, true, copyErr
			}
			completedResult, completeErr := request.Stream.Complete(ctx)
			if completeErr != nil {
				abort(completeErr)
				return Result{}, true, completeErr
			}
			return completedResult, true, nil
		case 'f':
			if copyErr == nil {
				message, failErr := copyFailMessage(body)
				if failErr != nil {
					copyErr = failErr
				} else {
					copyErr = NewError("57014", "PostgreSQL client aborted COPY: "+message)
				}
			}
			abort(copyErr)
			return Result{}, true, copyErr
		case 'H':
			if len(body) != 0 {
				copyErr = NewError("08P01", "invalid PostgreSQL Flush message during COPY")
				abort(copyErr)
				return Result{}, true, copyErr
			}
			if err := wire.writer.Flush(); err != nil {
				abort(err)
				return Result{}, true, err
			}
		case 'X':
			copyErr = io.EOF
			abort(copyErr)
			return Result{}, true, errTerminateConnection
		default:
			copyErr = NewError(
				"08P01",
				fmt.Sprintf("PostgreSQL frontend message %q is not valid during COPY", kind),
			)
			abort(copyErr)
			return Result{}, true, copyErr
		}
	}
}

func stopCopyDeadline(stop func() bool, done <-chan struct{}) {
	if stop == nil {
		return
	}
	if !stop() && done != nil {
		<-done
	}
}

func validateCopyInRequest(request CopyInRequest) error {
	if request.Stream == nil {
		return NewError("XX000", "KitDB COPY adapter returned no stream")
	}
	if request.Format != 0 && request.Format != 1 {
		return NewError("XX000", "KitDB COPY adapter returned an invalid format")
	}
	if len(request.ColumnFormats) > 32767 {
		return NewError("54000", "KitDB COPY has too many columns")
	}
	for _, format := range request.ColumnFormats {
		if format != 0 && format != 1 {
			return NewError("XX000", "KitDB COPY adapter returned an invalid column format")
		}
	}
	return nil
}

func (wire *wireConnection) sendCopyInResponse(request CopyInRequest) error {
	var payload encoder
	payload.byte(byte(request.Format))
	payload.int16(int16(len(request.ColumnFormats)))
	for _, format := range request.ColumnFormats {
		payload.int16(format)
	}
	return wire.send('G', payload)
}

func copyFailMessage(body []byte) (string, error) {
	decoder := decoder{data: body}
	message, err := decoder.cstring()
	if err != nil || decoder.done() != nil {
		return "", NewError("08P01", "invalid PostgreSQL CopyFail message")
	}
	if strings.TrimSpace(message) == "" {
		message = "client canceled the operation"
	}
	return message, nil
}

func (wire *wireConnection) sendResult(result Result, start int, description bool) error {
	if description && len(result.Columns) > 0 {
		if err := wire.sendRowDescription(result.Columns, nil); err != nil {
			return err
		}
	}
	for _, row := range result.Rows[start:] {
		if err := wire.sendDataRow(row, result.Columns, nil); err != nil {
			return err
		}
	}
	return wire.sendCommand(result.CommandTag)
}

func (wire *wireConnection) sendRowDescription(columns []Column, formats []int16) error {
	if len(columns) > 32767 {
		return NewError("54000", "too many result columns")
	}
	var payload encoder
	payload.int16(int16(len(columns)))
	for index, column := range columns {
		payload.cstring(column.Name)
		payload.uint32(0)
		payload.int16(0)
		oid := column.DataTypeOID
		if oid == 0 {
			oid = OIDText
		}
		payload.uint32(oid)
		size := column.DataTypeSize
		if size == 0 {
			size = -1
		}
		payload.int16(size)
		modifier := column.TypeModifier
		if modifier == 0 && oid != OIDTime && oid != OIDTimestamp && oid != OIDTimestampTZ {
			modifier = -1
		}
		payload.int32(modifier)
		format, err := resultFormat(formats, index, len(columns))
		if err != nil {
			return err
		}
		payload.int16(format)
	}
	return wire.send('T', payload)
}

func resultFormat(formats []int16, index, columns int) (int16, error) {
	switch len(formats) {
	case 0:
		return 0, nil
	case 1:
		return formats[0], nil
	case columns:
		return formats[index], nil
	default:
		return 0, NewError("08P01", "result format count must be zero, one, or column count")
	}
}

func (wire *wireConnection) sendDataRow(row []Field, columns []Column, formats []int16) error {
	if len(row) != len(columns) || len(row) > 32767 {
		return NewError("XX000", "KitDB adapter returned an invalid row shape")
	}
	var payload encoder
	payload.int16(int16(len(row)))
	for index, field := range row {
		if field.Null {
			payload.int32(-1)
			continue
		}
		format, err := resultFormat(formats, index, len(columns))
		if err != nil {
			return err
		}
		if format == 1 {
			field, err = binaryResultField(field, columns[index])
			if err != nil {
				return err
			}
		}
		if len(field.Data) > wire.runtime.server.MaxMessageBytes {
			return NewError("54000", "KitDB result field exceeds the pgwire limit")
		}
		payload.int32(int32(len(field.Data)))
		payload = append(payload, field.Data...)
	}
	return wire.send('D', payload)
}

func binaryResultField(field Field, column Column) (Field, error) {
	if field.Null {
		return field, nil
	}
	switch column.DataTypeOID {
	case OIDText, OIDVarchar, OIDBPChar, OIDJSON:
		return field, nil
	case OIDJSONB:
		data := make([]byte, len(field.Data)+1)
		data[0] = 1
		copy(data[1:], field.Data)
		return Field{Data: data}, nil
	case OIDBool:
		switch strings.ToLower(string(field.Data)) {
		case "t", "true", "1":
			return Field{Data: []byte{1}}, nil
		case "f", "false", "0":
			return Field{Data: []byte{0}}, nil
		default:
			return Field{}, NewError("22P03", "invalid boolean result")
		}
	case OIDBytea:
		if len(field.Data) >= 2 && string(field.Data[:2]) == `\x` {
			decoded := make([]byte, (len(field.Data)-2)/2)
			if _, err := hex.Decode(decoded, field.Data[2:]); err == nil {
				return Field{Data: decoded}, nil
			}
		}
		return field, nil
	case OIDInt2:
		value, err := strconv.ParseInt(string(field.Data), 10, 16)
		if err != nil {
			return Field{}, NewError("22P03", "invalid int2 result")
		}
		data := make([]byte, 2)
		binary.BigEndian.PutUint16(data, uint16(int16(value)))
		return Field{Data: data}, nil
	case OIDInt4:
		value, err := strconv.ParseInt(string(field.Data), 10, 32)
		if err != nil {
			return Field{}, NewError("22P03", "invalid int4 result")
		}
		data := make([]byte, 4)
		binary.BigEndian.PutUint32(data, uint32(int32(value)))
		return Field{Data: data}, nil
	case OIDInt8:
		value, err := strconv.ParseInt(string(field.Data), 10, 64)
		if err != nil {
			return Field{}, NewError("22P03", "invalid int8 result")
		}
		data := make([]byte, 8)
		binary.BigEndian.PutUint64(data, uint64(value))
		return Field{Data: data}, nil
	case OIDOID:
		value, err := strconv.ParseUint(string(field.Data), 10, 32)
		if err != nil {
			return Field{}, NewError("22P03", "invalid oid result")
		}
		data := make([]byte, 4)
		binary.BigEndian.PutUint32(data, uint32(value))
		return Field{Data: data}, nil
	case OIDFloat4:
		value, err := strconv.ParseFloat(string(field.Data), 32)
		if err != nil {
			return Field{}, NewError("22P03", "invalid float4 result")
		}
		data := make([]byte, 4)
		binary.BigEndian.PutUint32(data, math.Float32bits(float32(value)))
		return Field{Data: data}, nil
	case OIDFloat8:
		value, err := strconv.ParseFloat(string(field.Data), 64)
		if err != nil {
			return Field{}, NewError("22P03", "invalid float8 result")
		}
		data := make([]byte, 8)
		binary.BigEndian.PutUint64(data, math.Float64bits(value))
		return Field{Data: data}, nil
	case OIDNumeric:
		data, err := EncodeNumericBinary(string(field.Data), column.TypeModifier)
		if err != nil {
			return Field{}, NewError("22P03", "invalid numeric result")
		}
		return Field{Data: data}, nil
	case OIDDate:
		data, err := EncodeDateBinary(string(field.Data))
		if err != nil {
			return Field{}, NewError("22P03", "invalid date result")
		}
		return Field{Data: data}, nil
	case OIDTime:
		data, err := EncodeTimeBinary(string(field.Data))
		if err != nil {
			return Field{}, NewError("22P03", "invalid time result")
		}
		return Field{Data: data}, nil
	case OIDTimestamp, OIDTimestampTZ:
		data, err := EncodeTimestampBinary(string(field.Data), column.DataTypeOID == OIDTimestampTZ)
		if err != nil {
			return Field{}, NewError("22P03", "invalid timestamp result")
		}
		return Field{Data: data}, nil
	case OIDInterval:
		data, err := EncodeIntervalBinary(string(field.Data))
		if err != nil {
			return Field{}, NewError("22P03", "invalid interval result")
		}
		return Field{Data: data}, nil
	case OIDUUID:
		data, err := EncodeUUIDBinary(string(field.Data))
		if err != nil {
			return Field{}, NewError("22P03", "invalid UUID result")
		}
		return Field{Data: data}, nil
	default:
		return Field{}, NewError("0A000", fmt.Sprintf("binary result format is unavailable for OID %d", column.DataTypeOID))
	}
}

func (wire *wireConnection) sendCommand(tag string) error {
	if tag == "" {
		tag = "OK"
	}
	var payload encoder
	payload.cstring(tag)
	return wire.send('C', payload)
}

func (wire *wireConnection) ready() error {
	status := TransactionIdle
	if session, ok := wire.session.(TransactionStatusSession); ok {
		switch candidate := session.TransactionStatus(); candidate {
		case TransactionIdle, TransactionActive, TransactionFailed:
			status = candidate
		}
	}
	return wire.send('Z', encoder{status})
}

func (wire *wireConnection) sendError(err error) error {
	if err == nil {
		err = NewError("XX000", "unknown KitDB error")
	}
	if writeErr := writeError(wire.writer, err); writeErr != nil {
		return writeErr
	}
	return wire.writer.Flush()
}

func writeError(writer *bufio.Writer, err error) error {
	var payload encoder
	payload.byte('S')
	payload.cstring("ERROR")
	payload.byte('V')
	payload.cstring("ERROR")
	payload.byte('C')
	payload.cstring(errorState(err))
	payload.byte('M')
	payload.cstring(err.Error())
	payload.byte(0)
	return writeMessage(writer, 'E', payload)
}

func (wire *wireConnection) readStartup() ([]byte, error) {
	if err := wire.conn.SetReadDeadline(time.Now().Add(wire.runtime.server.IdleTimeout)); err != nil {
		return nil, err
	}
	var header [4]byte
	if _, err := io.ReadFull(wire.reader, header[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(header[:])
	if length < 8 || length > maxStartupBytes {
		return nil, fmt.Errorf("pgwire: invalid startup packet length %d", length)
	}
	body := make([]byte, int(length)-4)
	if _, err := io.ReadFull(wire.reader, body); err != nil {
		return nil, err
	}
	return body, nil
}

func (wire *wireConnection) readMessage() (byte, []byte, error) {
	return wire.readMessageWithin(wire.runtime.server.IdleTimeout)
}

func (wire *wireConnection) readMessageWithin(timeout time.Duration) (byte, []byte, error) {
	if err := wire.conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return 0, nil, err
	}
	kind, err := wire.reader.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	var header [4]byte
	if _, err := io.ReadFull(wire.reader, header[:]); err != nil {
		return 0, nil, err
	}
	length := binary.BigEndian.Uint32(header[:])
	if length < 4 || length-4 > uint32(wire.runtime.server.MaxMessageBytes) {
		return 0, nil, fmt.Errorf("pgwire: invalid message length %d", length)
	}
	body := make([]byte, int(length)-4)
	if _, err := io.ReadFull(wire.reader, body); err != nil {
		return 0, nil, err
	}
	return kind, body, nil
}

func (wire *wireConnection) send(kind byte, body []byte) error {
	if err := wire.conn.SetWriteDeadline(time.Now().Add(wire.runtime.server.WriteTimeout)); err != nil {
		return err
	}
	return writeMessage(wire.writer, kind, body)
}

func writeMessage(writer *bufio.Writer, kind byte, body []byte) error {
	if err := writer.WriteByte(kind); err != nil {
		return err
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(body)+4))
	if _, err := writer.Write(header[:]); err != nil {
		return err
	}
	_, err := writer.Write(body)
	return err
}

func (wire *wireConnection) writeRaw(data []byte) error {
	if err := wire.conn.SetWriteDeadline(time.Now().Add(wire.runtime.server.WriteTimeout)); err != nil {
		return err
	}
	if _, err := wire.writer.Write(data); err != nil {
		return err
	}
	return wire.writer.Flush()
}
