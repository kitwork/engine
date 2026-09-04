package pgwire

import (
	"fmt"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

// EncodeUUIDBinary emits PostgreSQL's 16-byte UUID binary representation.
func EncodeUUIDBinary(source string) ([]byte, error) {
	value, err := kitdbsql.ParseUUID(source)
	if err != nil {
		return nil, fmt.Errorf("pgwire: encode UUID: %w", err)
	}
	return append([]byte(nil), value[:]...), nil
}

// DecodeUUIDBinary returns PostgreSQL's canonical text form.
func DecodeUUIDBinary(data []byte) (string, error) {
	if len(data) != 16 {
		return "", fmt.Errorf("pgwire: UUID binary value has length %d, want 16", len(data))
	}
	var value [16]byte
	copy(value[:], data)
	return kitdbsql.FormatUUID(value), nil
}
