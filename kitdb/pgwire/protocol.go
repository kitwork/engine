// Package pgwire implements KitDB's bounded PostgreSQL wire compatibility
// profile. It owns transport framing only; SQL and storage remain behind the
// Session interface.
package pgwire

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

const (
	ProtocolVersion30 uint32 = 196608

	OIDBool        uint32 = kitdbsql.PostgreSQLOIDBool
	OIDBytea       uint32 = kitdbsql.PostgreSQLOIDBytea
	OIDInt8        uint32 = kitdbsql.PostgreSQLOIDInt8
	OIDInt2        uint32 = kitdbsql.PostgreSQLOIDInt2
	OIDInt4        uint32 = kitdbsql.PostgreSQLOIDInt4
	OIDText        uint32 = kitdbsql.PostgreSQLOIDText
	OIDBPChar      uint32 = kitdbsql.PostgreSQLOIDBPChar
	OIDVarchar     uint32 = kitdbsql.PostgreSQLOIDVarchar
	OIDOID         uint32 = kitdbsql.PostgreSQLOIDOID
	OIDFloat4      uint32 = kitdbsql.PostgreSQLOIDFloat4
	OIDFloat8      uint32 = kitdbsql.PostgreSQLOIDFloat8
	OIDDate        uint32 = kitdbsql.PostgreSQLOIDDate
	OIDTime        uint32 = kitdbsql.PostgreSQLOIDTime
	OIDTimestamp   uint32 = kitdbsql.PostgreSQLOIDTimestamp
	OIDNumeric     uint32 = kitdbsql.PostgreSQLOIDNumeric
	OIDUUID        uint32 = kitdbsql.PostgreSQLOIDUUID
	OIDJSON        uint32 = kitdbsql.PostgreSQLOIDJSON
	OIDJSONB       uint32 = kitdbsql.PostgreSQLOIDJSONB
	OIDTimestampTZ uint32 = kitdbsql.PostgreSQLOIDTimestampTZ
	OIDInterval    uint32 = kitdbsql.PostgreSQLOIDInterval
)

const (
	sslRequestCode    uint32 = 80877103
	cancelRequestCode uint32 = 80877102
	gssRequestCode    uint32 = 80877104
)

// Startup contains the bounded client parameters from PostgreSQL's initial
// packet. Unknown parameters are retained for compatibility but never trusted
// as storage paths or authorization facts.
type Startup struct {
	Parameters map[string]string
}

func (startup Startup) User() string     { return startup.Parameters["user"] }
func (startup Startup) Database() string { return startup.Parameters["database"] }

// Parameter is one value bound through the extended query protocol.
type Parameter struct {
	OID    uint32
	Format int16
	Data   []byte
	Null   bool
}

// Column describes one text-format result field.
type Column struct {
	Name         string
	DataTypeOID  uint32
	DataTypeSize int16
	TypeModifier int32
}

// Field is one text-format result value. Null is distinct from an empty byte
// slice because PostgreSQL encodes them differently.
type Field struct {
	Data []byte
	Null bool
}

// Result is the protocol-neutral output produced by a KitDB SQL adapter.
type Result struct {
	Columns    []Column
	Rows       [][]Field
	CommandTag string
}

// Authenticator maps untrusted startup data and a password to one bounded
// database session.
type Authenticator interface {
	Authenticate(ctx context.Context, startup Startup, password string) (Session, error)
}

// Session executes SQL after host-owned authentication and database routing.
type Session interface {
	Execute(ctx context.Context, query string, parameters []Parameter) (Result, error)
	Close() error
}

// DescribeSession is an optional metadata-only extension. Implementations can
// describe a prepared read without executing it with synthetic NULL values.
// This avoids an accidental table scan merely because a PostgreSQL client asks
// for RowDescription before binding parameters.
type DescribeSession interface {
	Describe(ctx context.Context, query string, parameters []Parameter) ([]Column, error)
}

// CopyInStream receives one PostgreSQL COPY FROM STDIN stream. Write may be
// called many times with arbitrary protocol frame boundaries; implementations
// must not assume a frame contains a complete row. Complete publishes the
// operation, while Abort must discard it and release every held resource.
type CopyInStream interface {
	Write(ctx context.Context, data []byte) error
	Complete(ctx context.Context) (Result, error)
	Abort(err error) error
}

// CopyInRequest describes the text or binary formats accepted by one COPY.
// KitDB's relational adapter currently advertises text fields only, but the
// transport keeps the PostgreSQL shape so a future binary decoder does not
// require another protocol change.
type CopyInRequest struct {
	Format        int16
	ColumnFormats []int16
	// Lifetime optionally carries a stricter adapter-owned boundary, such as
	// the lifetime of an explicit database transaction.
	Lifetime context.Context
	Stream   CopyInStream
}

// CopyInSession is optional. Sessions that do not implement it retain the
// ordinary Execute-only protocol surface and reject COPY through their SQL
// adapter as before.
type CopyInSession interface {
	BeginCopyIn(
		ctx context.Context,
		query string,
		parameters []Parameter,
	) (CopyInRequest, bool, error)
}

// CopyAdmission identifies one host-trusted scheduling partition. Sessions
// with the same key share a concurrency quota; Weight changes their relative
// service rate only after they are queued. Keys are never emitted to clients or
// copied into aggregate metrics. A host must return one stable weight for every
// shared key while requests under that key remain active or queued.
type CopyAdmission struct {
	Key    string
	Weight int
}

// CopyAdmissionSession optionally partitions COPY admission by authenticated
// tenant/database identity. Other sessions share one conservative default key.
type CopyAdmissionSession interface {
	CopyAdmission() CopyAdmission
}

const (
	TransactionIdle   byte = 'I'
	TransactionActive byte = 'T'
	TransactionFailed byte = 'E'
)

// TransactionStatusSession optionally reports PostgreSQL's ReadyForQuery
// status. Sessions that do not implement it remain in autocommit/idle mode.
type TransactionStatusSession interface {
	TransactionStatus() byte
}

// Error carries a stable SQLSTATE across the transport boundary.
type Error struct {
	Code    string
	Message string
}

func (err *Error) Error() string {
	if err == nil {
		return ""
	}
	return err.Message
}

// NewError creates a PostgreSQL-compatible error without exposing engine
// implementation details through the wire package.
func NewError(code, message string) error {
	return &Error{Code: code, Message: message}
}

func errorState(err error) string {
	var protocolErr *Error
	if errors.As(err, &protocolErr) && len(protocolErr.Code) == 5 {
		return protocolErr.Code
	}
	return "XX000"
}

type decoder struct {
	data     []byte
	position int
}

func (decoder *decoder) remaining() int {
	return len(decoder.data) - decoder.position
}

func (decoder *decoder) byte() (byte, error) {
	if decoder.remaining() < 1 {
		return 0, fmt.Errorf("pgwire: truncated byte")
	}
	value := decoder.data[decoder.position]
	decoder.position++
	return value, nil
}

func (decoder *decoder) int16() (int16, error) {
	if decoder.remaining() < 2 {
		return 0, fmt.Errorf("pgwire: truncated int16")
	}
	value := int16(binary.BigEndian.Uint16(decoder.data[decoder.position:]))
	decoder.position += 2
	return value, nil
}

func (decoder *decoder) uint32() (uint32, error) {
	if decoder.remaining() < 4 {
		return 0, fmt.Errorf("pgwire: truncated uint32")
	}
	value := binary.BigEndian.Uint32(decoder.data[decoder.position:])
	decoder.position += 4
	return value, nil
}

func (decoder *decoder) int32() (int32, error) {
	value, err := decoder.uint32()
	return int32(value), err
}

func (decoder *decoder) bytes(count int) ([]byte, error) {
	if count < 0 || decoder.remaining() < count {
		return nil, fmt.Errorf("pgwire: truncated %d-byte field", count)
	}
	value := decoder.data[decoder.position : decoder.position+count]
	decoder.position += count
	return value, nil
}

func (decoder *decoder) cstring() (string, error) {
	for index := decoder.position; index < len(decoder.data); index++ {
		if decoder.data[index] != 0 {
			continue
		}
		value := string(decoder.data[decoder.position:index])
		decoder.position = index + 1
		return value, nil
	}
	return "", fmt.Errorf("pgwire: unterminated string")
}

func (decoder *decoder) done() error {
	if decoder.remaining() != 0 {
		return fmt.Errorf("pgwire: message has %d trailing bytes", decoder.remaining())
	}
	return nil
}

type encoder []byte

func (encoder *encoder) byte(value byte) {
	*encoder = append(*encoder, value)
}

func (encoder *encoder) int16(value int16) {
	var data [2]byte
	binary.BigEndian.PutUint16(data[:], uint16(value))
	*encoder = append(*encoder, data[:]...)
}

func (encoder *encoder) uint32(value uint32) {
	var data [4]byte
	binary.BigEndian.PutUint32(data[:], value)
	*encoder = append(*encoder, data[:]...)
}

func (encoder *encoder) int32(value int32) {
	encoder.uint32(uint32(value))
}

func (encoder *encoder) cstring(value string) {
	*encoder = append(*encoder, value...)
	*encoder = append(*encoder, 0)
}
