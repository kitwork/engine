package sql

import (
	"encoding/hex"
	"fmt"
)

// ParseUUID accepts PostgreSQL's UUID input profile and returns its 128-bit
// value. Hyphens may follow any complete group of four hexadecimal digits;
// braces and upper-case digits are accepted, but output is always canonical.
func ParseUUID(source string) ([16]byte, error) {
	var value [16]byte
	if len(source) >= 2 && source[0] == '{' && source[len(source)-1] == '}' {
		source = source[1 : len(source)-1]
	}
	var digits [32]byte
	count := 0
	previousHyphen := false
	for index := 0; index < len(source); index++ {
		current := source[index]
		if current == '-' {
			if count == 0 || count == len(digits) || count%4 != 0 || previousHyphen {
				return value, fmt.Errorf("invalid UUID hyphen at byte %d", index)
			}
			previousHyphen = true
			continue
		}
		if !isUUIDHex(current) || count >= len(digits) {
			return value, fmt.Errorf("invalid UUID character at byte %d", index)
		}
		digits[count] = current
		count++
		previousHyphen = false
	}
	if count != len(digits) || previousHyphen {
		return value, fmt.Errorf("UUID must contain exactly 32 hexadecimal digits")
	}
	if _, err := hex.Decode(value[:], digits[:]); err != nil {
		return [16]byte{}, fmt.Errorf("decode UUID: %w", err)
	}
	return value, nil
}

// FormatUUID emits PostgreSQL's standard lower-case 8-4-4-4-12 form.
func FormatUUID(value [16]byte) string {
	encoded := make([]byte, 36)
	hex.Encode(encoded[0:8], value[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], value[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], value[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], value[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], value[10:16])
	return string(encoded)
}

// CanonicalUUID validates one UUID and returns its stable storage spelling.
func CanonicalUUID(source string) (string, error) {
	value, err := ParseUUID(source)
	if err != nil {
		return "", err
	}
	return FormatUUID(value), nil
}

func isUUIDHex(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'f' || value >= 'A' && value <= 'F'
}
