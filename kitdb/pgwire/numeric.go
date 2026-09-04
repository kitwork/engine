package pgwire

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
)

const (
	postgresNumericPositive uint16 = 0x0000
	postgresNumericNegative uint16 = 0x4000
	maximumNumericTextBytes        = 16_384
)

// EncodeNumericBinary emits PostgreSQL's base-10000 NUMERIC wire format.
// typeModifier is the RowDescription typmod, including PostgreSQL's +4 bias.
func EncodeNumericBinary(source string, typeModifier int32) ([]byte, error) {
	negative, integer, fraction, err := splitNumericText(source)
	if err != nil {
		return nil, err
	}
	displayScale := len(fraction)
	if typeModifier >= 4 {
		modifier := int(typeModifier - 4)
		declaredScale := modifier & 0xffff
		if declaredScale < displayScale {
			return nil, fmt.Errorf("numeric result exceeds declared scale")
		}
		displayScale = declaredScale
	}
	if displayScale > len(fraction) {
		fraction += strings.Repeat("0", displayScale-len(fraction))
	}
	leftPadding := (4 - len(integer)%4) % 4
	rightPadding := (4 - len(fraction)%4) % 4
	digits := strings.Repeat("0", leftPadding) + integer + fraction + strings.Repeat("0", rightPadding)
	integerGroups := (leftPadding + len(integer)) / 4
	groups := make([]uint16, 0, len(digits)/4)
	for offset := 0; offset < len(digits); offset += 4 {
		group, parseErr := strconv.ParseUint(digits[offset:offset+4], 10, 16)
		if parseErr != nil || group > 9_999 {
			return nil, fmt.Errorf("invalid numeric group")
		}
		groups = append(groups, uint16(group))
	}
	weight := integerGroups - 1
	for len(groups) != 0 && groups[0] == 0 {
		groups = groups[1:]
		weight--
	}
	for len(groups) != 0 && groups[len(groups)-1] == 0 {
		groups = groups[:len(groups)-1]
	}
	if len(groups) == 0 {
		weight, negative = 0, false
	}
	if len(groups) > 32_767 || weight < -32_768 || weight > 32_767 || displayScale > 65_535 {
		return nil, fmt.Errorf("numeric result exceeds PostgreSQL binary bounds")
	}
	result := make([]byte, 8+2*len(groups))
	binary.BigEndian.PutUint16(result[0:2], uint16(len(groups)))
	binary.BigEndian.PutUint16(result[2:4], uint16(int16(weight)))
	sign := postgresNumericPositive
	if negative {
		sign = postgresNumericNegative
	}
	binary.BigEndian.PutUint16(result[4:6], sign)
	binary.BigEndian.PutUint16(result[6:8], uint16(displayScale))
	for index, group := range groups {
		binary.BigEndian.PutUint16(result[8+2*index:], group)
	}
	return result, nil
}

// DecodeNumericBinary accepts finite PostgreSQL NUMERIC values and returns
// bounded plain decimal text. NaN and infinities fail closed.
func DecodeNumericBinary(data []byte) (string, error) {
	if len(data) < 8 || (len(data)-8)%2 != 0 {
		return "", fmt.Errorf("invalid binary numeric length")
	}
	count := int(int16(binary.BigEndian.Uint16(data[0:2])))
	weight := int(int16(binary.BigEndian.Uint16(data[2:4])))
	sign := binary.BigEndian.Uint16(data[4:6])
	scale := int(binary.BigEndian.Uint16(data[6:8]))
	if count < 0 || len(data) != 8+2*count ||
		(sign != postgresNumericPositive && sign != postgresNumericNegative) {
		return "", fmt.Errorf("invalid binary numeric header")
	}
	groups := make([]uint16, count)
	for index := range groups {
		groups[index] = binary.BigEndian.Uint16(data[8+2*index:])
		if groups[index] > 9_999 {
			return "", fmt.Errorf("invalid binary numeric digit")
		}
	}
	integerGroups := weight + 1
	var integer strings.Builder
	if integerGroups <= 0 {
		integer.WriteByte('0')
	} else {
		for position := 0; position < integerGroups; position++ {
			group := uint16(0)
			if position < len(groups) {
				group = groups[position]
			}
			if position == 0 {
				integer.WriteString(strconv.Itoa(int(group)))
			} else {
				fmt.Fprintf(&integer, "%04d", group)
			}
			if integer.Len() > maximumNumericTextBytes {
				return "", fmt.Errorf("binary numeric exceeds KitDB bounds")
			}
		}
	}
	var fraction strings.Builder
	for position := 0; fraction.Len() < scale; position++ {
		group := uint16(0)
		index := integerGroups + position
		if index >= 0 && index < len(groups) {
			group = groups[index]
		}
		fmt.Fprintf(&fraction, "%04d", group)
		if integer.Len()+fraction.Len() > maximumNumericTextBytes {
			return "", fmt.Errorf("binary numeric exceeds KitDB bounds")
		}
	}
	fractionText := fraction.String()
	if len(fractionText) > scale {
		fractionText = fractionText[:scale]
	}
	result := integer.String()
	if scale != 0 {
		result += "." + fractionText
	}
	if sign == postgresNumericNegative && numericTextIsNonZero(result) {
		result = "-" + result
	}
	return result, nil
}

func splitNumericText(source string) (negative bool, integer, fraction string, err error) {
	text := strings.TrimSpace(source)
	if len(text) == 0 || len(text) > maximumNumericTextBytes {
		return false, "", "", fmt.Errorf("invalid numeric text")
	}
	if text[0] == '+' || text[0] == '-' {
		negative = text[0] == '-'
		text = text[1:]
	}
	if text == "" || strings.ContainsAny(text, "eE") {
		return false, "", "", fmt.Errorf("numeric binary input must be plain decimal text")
	}
	parts := strings.Split(text, ".")
	if len(parts) > 2 {
		return false, "", "", fmt.Errorf("invalid numeric text")
	}
	integer = parts[0]
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if integer == "" {
		integer = "0"
	}
	if fraction == "" && len(parts) == 2 && parts[0] == "" {
		return false, "", "", fmt.Errorf("invalid numeric text")
	}
	for _, part := range []string{integer, fraction} {
		for _, digit := range part {
			if digit < '0' || digit > '9' {
				return false, "", "", fmt.Errorf("invalid numeric text")
			}
		}
	}
	integer = strings.TrimLeft(integer, "0")
	if integer == "" {
		integer = "0"
	}
	return negative, integer, fraction, nil
}

func numericTextIsNonZero(text string) bool {
	for _, digit := range text {
		if digit >= '1' && digit <= '9' {
			return true
		}
	}
	return false
}
