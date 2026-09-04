package relational

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/kitwork/engine/id"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

const maximumExactInteger = int64(1<<53 - 1)

func integerKindBounds(kind string) (int64, int64) {
	switch kind {
	case "smallint":
		return math.MinInt16, math.MaxInt16
	case "int32":
		return math.MinInt32, math.MaxInt32
	case "bigint":
		return math.MinInt64, math.MaxInt64
	default:
		// This is the frozen contract of the original integer/serial/system
		// kinds. Do not widen it: their ordered keys still use float64.
		return -maximumExactInteger, maximumExactInteger
	}
}

func exactIntegerFieldKind(kind string) bool {
	switch kind {
	case "smallint", "int32", "bigint":
		return true
	default:
		return false
	}
}

func literalUsesClock(kind kitdbsql.LiteralKind) bool {
	switch kind {
	case kitdbsql.LiteralCurrentTimestamp, kitdbsql.LiteralCurrentDate,
		kitdbsql.LiteralCurrentTime, kitdbsql.LiteralLocalTimestamp:
		return true
	default:
		return false
	}
}

func resolveLiteral(literal kitdbsql.Literal, parameters []any) (any, error) {
	return resolveLiteralAt(literal, parameters, time.Now().UTC())
}

func resolveLiteralAt(literal kitdbsql.Literal, parameters []any, now time.Time) (any, error) {
	switch literal.Kind {
	case kitdbsql.LiteralNull:
		return nil, nil
	case kitdbsql.LiteralBoolean:
		return literal.Boolean, nil
	case kitdbsql.LiteralString:
		return literal.Text, nil
	case kitdbsql.LiteralNumber:
		if !strings.ContainsAny(literal.Text, ".eE") {
			integer, err := strconv.ParseInt(literal.Text, 10, 64)
			if err == nil {
				return integer, nil
			}
			return nil, fmt.Errorf("kitdb SQL: integer literal is outside int64: %q", literal.Text)
		}
		canonical, err := canonicalDecimal(literal.Text)
		if err != nil {
			return nil, fmt.Errorf("kitdb SQL: invalid number %q", literal.Text)
		}
		return exactDecimal(canonical), nil
	case kitdbsql.LiteralParameter:
		if literal.Parameter < 1 || literal.Parameter > len(parameters) {
			return nil, fmt.Errorf("kitdb SQL: parameter $%d is unavailable", literal.Parameter)
		}
		return parameters[literal.Parameter-1], nil
	case kitdbsql.LiteralCurrentTimestamp:
		return temporalFromTime(now, "timestamptz", nil)
	case kitdbsql.LiteralCurrentDate:
		return temporalFromTime(now, "date", nil)
	case kitdbsql.LiteralCurrentTime:
		return temporalFromTime(now, "time", nil)
	case kitdbsql.LiteralLocalTimestamp:
		return temporalFromTime(now, "timestamp", nil)
	default:
		return nil, fmt.Errorf("kitdb SQL: invalid literal")
	}
}

func coerceField(field kitdbsql.Field, item any) (any, error) {
	if item == nil {
		return nil, nil
	}
	typeInfo, found := kitdbsql.LookupKind(field.Kind)
	if !found {
		return nil, fmt.Errorf("field %q uses unsupported type %q", field.Name, field.Kind)
	}
	if typeInfo.ID == kitdbsql.TypeUUID && field.ExactUUID {
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("field %q expects UUID text, got %T", field.Name, item)
		}
		canonical, err := kitdbsql.CanonicalUUID(text)
		if err != nil {
			return nil, fmt.Errorf("field %q expects a valid UUID: %w", field.Name, err)
		}
		return canonical, nil
	}
	switch typeInfo.Family {
	case kitdbsql.FamilyText:
		return coerceTextField(field, item, false)
	case kitdbsql.FamilyIdentifier, kitdbsql.FamilyNetwork:
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("field %q expects text, got %T", field.Name, item)
		}
		return text, nil
	case kitdbsql.FamilyInteger, kitdbsql.FamilySystem:
		integer, err := integerValue(item)
		minimum, maximum := integerKindBounds(field.Kind)
		if err != nil || integer < minimum || integer > maximum {
			return nil, fmt.Errorf(
				"field %q expects %s in [%d,%d], got %v", field.Name, field.Kind, minimum, maximum, item,
			)
		}
		return integer, nil
	case kitdbsql.FamilyFloat:
		number, err := floatValue(item)
		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
			return nil, fmt.Errorf("field %q expects a finite number, got %v", field.Name, item)
		}
		return number, nil
	case kitdbsql.FamilyDecimal:
		var text string
		switch current := item.(type) {
		case string:
			text = current
		case exactDecimal:
			text = string(current)
		case json.Number:
			text = current.String()
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			integer, err := integerValue(item)
			if err != nil {
				return nil, fmt.Errorf("field %q expects a decimal: %w", field.Name, err)
			}
			text = strconv.FormatInt(integer, 10)
		default:
			number, err := floatValue(item)
			if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
				return nil, fmt.Errorf("field %q expects a decimal, got %v", field.Name, item)
			}
			text = strconv.FormatFloat(number, 'g', -1, 64)
		}
		canonical, err := coerceDecimal(field, text)
		if err != nil {
			return nil, fmt.Errorf("field %q expects decimal text, got %q: %w", field.Name, text, err)
		}
		return canonical, nil
	case kitdbsql.FamilyBoolean:
		truth, err := booleanValue(item)
		if err != nil {
			return nil, fmt.Errorf("field %q expects a boolean, got %v", field.Name, item)
		}
		if truth {
			return int64(1), nil
		}
		return int64(0), nil
	case kitdbsql.FamilyChoice:
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("field %q expects choice text, got %T", field.Name, item)
		}
		for _, choice := range field.Enum {
			if text == choice {
				return text, nil
			}
		}
		return nil, fmt.Errorf("field %q must be one of %v", field.Name, field.Enum)
	case kitdbsql.FamilyTemporal:
		return coerceTemporalField(field, item)
	case kitdbsql.FamilyJSON, kitdbsql.FamilyArray, kitdbsql.FamilyVector:
		return structuredValue(field, item)
	case kitdbsql.FamilyBinary:
		data, ok := item.([]byte)
		if !ok {
			return nil, fmt.Errorf("field %q expects bytes, got %T", field.Name, item)
		}
		return bytes.Clone(data), nil
	default:
		return nil, fmt.Errorf("field %q uses unsupported type %q", field.Name, field.Kind)
	}
}

func structuredValue(field kitdbsql.Field, item any) (string, error) {
	var encoded []byte
	var err error
	switch current := item.(type) {
	case string:
		encoded = []byte(current)
	case json.RawMessage:
		encoded = bytes.Clone(current)
	default:
		encoded, err = json.Marshal(current)
		if err != nil {
			return "", fmt.Errorf("field %q expects JSON: %w", field.Name, err)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return "", fmt.Errorf("field %q expects valid JSON", field.Name)
	}
	if field.Kind == "array" || field.Kind == "vector" {
		items, ok := decoded.([]any)
		if !ok {
			return "", fmt.Errorf("field %q expects a JSON array", field.Name)
		}
		if field.Kind == "vector" {
			for index, component := range items {
				if _, err := floatValue(component); err != nil {
					return "", fmt.Errorf("field %q vector component %d is not numeric", field.Name, index)
				}
			}
		}
	}
	compact := bytes.Buffer{}
	if err := json.Compact(&compact, encoded); err != nil {
		return "", fmt.Errorf("field %q expects valid JSON", field.Name)
	}
	return compact.String(), nil
}

func readField(field kitdbsql.Field, item any) any {
	if item == nil {
		return nil
	}
	if temporal, ok := item.(exactTemporal); ok {
		return temporal.text
	}
	typeInfo, found := kitdbsql.LookupKind(field.Kind)
	if found {
		switch typeInfo.Family {
		case kitdbsql.FamilyInteger, kitdbsql.FamilySystem:
			if integer, err := integerValue(item); err == nil {
				return integer
			}
		case kitdbsql.FamilyFloat:
			if number, err := floatValue(item); err == nil {
				return number
			}
		case kitdbsql.FamilyBoolean:
			truth, err := booleanValue(item)
			if err == nil {
				return truth
			}
		}
	}
	switch field.Kind {
	case "bool":
		truth, err := booleanValue(item)
		if err == nil {
			return truth
		}
	case "json", "jsonb", "array", "vector":
		text, ok := item.(string)
		if ok {
			decoder := json.NewDecoder(strings.NewReader(text))
			decoder.UseNumber()
			var decoded any
			if decoder.Decode(&decoded) == nil {
				return normalizeJSONNumber(decoded)
			}
		}
	}
	return item
}

func fillRow(schema kitdbsql.Schema, provided map[string]any, now time.Time) (map[string]any, error) {
	row := make(map[string]any, len(schema.Fields))
	for requested, item := range provided {
		canonical, field, found := schema.FieldByName(requested)
		if !found {
			return nil, fmt.Errorf("kitdb: struct %q has no field %q", schema.Name, requested)
		}
		coerced, err := coerceField(field, item)
		if err != nil {
			return nil, err
		}
		row[canonical] = coerced
	}
	for _, field := range schema.Fields {
		if _, exists := row[field.Name]; exists {
			continue
		}
		var item any
		generated := true
		switch {
		case field.Sequence != nil:
			return nil, fmt.Errorf("kitdb: sequence default for %q requires transactional execution", field.Name)
		case field.Kind == "kitid":
			item = id.Entity()
		case field.Kind == "uuid":
			item = newUUID()
		case field.DefaultNow:
			item = now.UTC()
		case field.Kind == "year":
			item = int64(now.UTC().Year())
		case field.Kind == "month":
			item = int64(now.UTC().Month())
		case field.Kind == "day":
			item = int64(now.UTC().Day())
		case field.HasDefault:
			decoder := json.NewDecoder(bytes.NewReader(field.Default))
			decoder.UseNumber()
			if err := decoder.Decode(&item); err != nil {
				return nil, fmt.Errorf("kitdb: decode default for field %q: %w", field.Name, err)
			}
			item = normalizeJSONNumber(item)
		default:
			generated = false
		}
		if generated {
			coerced, err := coerceField(field, item)
			if err != nil {
				return nil, err
			}
			row[field.Name] = coerced
			continue
		}
		if field.NotNull {
			return nil, fmt.Errorf("kitdb: struct %q field %q cannot be null", schema.Name, field.Name)
		}
	}
	return row, nil
}

func integerValue(item any) (int64, error) {
	switch current := item.(type) {
	case int:
		return int64(current), nil
	case int8:
		return int64(current), nil
	case int16:
		return int64(current), nil
	case int32:
		return int64(current), nil
	case int64:
		return current, nil
	case uint:
		if uint64(current) > math.MaxInt64 {
			return 0, fmt.Errorf("overflow")
		}
		return int64(current), nil
	case uint8:
		return int64(current), nil
	case uint16:
		return int64(current), nil
	case uint32:
		return int64(current), nil
	case uint64:
		if current > math.MaxInt64 {
			return 0, fmt.Errorf("overflow")
		}
		return int64(current), nil
	case float32:
		return integralFloat(float64(current))
	case float64:
		return integralFloat(current)
	case json.Number:
		return current.Int64()
	case string:
		return strconv.ParseInt(strings.TrimSpace(current), 10, 64)
	default:
		return 0, fmt.Errorf("not an integer")
	}
}

func integralFloat(number float64) (int64, error) {
	// MaxInt64 rounds up to 2^63 as float64, so the upper bound is exclusive.
	if math.IsNaN(number) || math.IsInf(number, 0) || number != math.Trunc(number) || number < -0x1p63 || number >= 0x1p63 {
		return 0, fmt.Errorf("not an integer")
	}
	return int64(number), nil
}

func floatValue(item any) (float64, error) {
	switch current := item.(type) {
	case float64:
		return current, nil
	case float32:
		return float64(current), nil
	case json.Number:
		return current.Float64()
	case exactDecimal:
		return strconv.ParseFloat(string(current), 64)
	case string:
		return strconv.ParseFloat(strings.TrimSpace(current), 64)
	default:
		integer, err := integerValue(item)
		return float64(integer), err
	}
}

func booleanValue(item any) (bool, error) {
	switch current := item.(type) {
	case bool:
		return current, nil
	case int64:
		if current == 0 || current == 1 {
			return current == 1, nil
		}
	case int:
		if current == 0 || current == 1 {
			return current == 1, nil
		}
	case float64:
		if current == 0 || current == 1 {
			return current == 1, nil
		}
	case string:
		switch strings.ToLower(strings.TrimSpace(current)) {
		case "true", "t", "1", "yes", "y", "on":
			return true, nil
		case "false", "f", "0", "no", "n", "off":
			return false, nil
		}
	}
	return false, fmt.Errorf("not a boolean")
}

func validDecimal(text string) bool {
	_, err := canonicalDecimal(text)
	return err == nil
}

const (
	maximumDecimalDigits   = 4_096
	maximumDecimalExponent = 4_096
	maximumDecimalText     = maximumDecimalDigits + maximumDecimalExponent + 3
)

func canonicalDecimal(source string) (string, error) {
	text := strings.TrimSpace(source)
	if text == "" {
		return "", fmt.Errorf("empty decimal")
	}
	negative := false
	if text[0] == '+' || text[0] == '-' {
		negative = text[0] == '-'
		text = text[1:]
		if text == "" {
			return "", fmt.Errorf("decimal has no digits")
		}
	}
	exponent := 0
	if marker := strings.IndexAny(text, "eE"); marker >= 0 {
		if strings.IndexAny(text[marker+1:], "eE") >= 0 || marker == len(text)-1 {
			return "", fmt.Errorf("invalid decimal exponent")
		}
		parsed, err := strconv.ParseInt(text[marker+1:], 10, 32)
		if err != nil || parsed < -maximumDecimalExponent || parsed > maximumDecimalExponent {
			return "", fmt.Errorf("decimal exponent is out of range")
		}
		exponent = int(parsed)
		text = text[:marker]
	}
	if text == "" {
		return "", fmt.Errorf("decimal has no mantissa")
	}
	dot := strings.IndexByte(text, '.')
	if dot >= 0 && strings.IndexByte(text[dot+1:], '.') >= 0 {
		return "", fmt.Errorf("decimal has multiple points")
	}
	integerDigits := len(text)
	if dot >= 0 {
		integerDigits = dot
		text = text[:dot] + text[dot+1:]
	}
	if text == "" || len(text) > maximumDecimalDigits {
		return "", fmt.Errorf("decimal digit count is out of range")
	}
	for _, digit := range text {
		if digit < '0' || digit > '9' {
			return "", fmt.Errorf("decimal contains a non-digit")
		}
	}
	decimalPosition := integerDigits + exponent
	leading := 0
	for leading < len(text) && text[leading] == '0' {
		leading++
		decimalPosition--
	}
	if leading == len(text) {
		return "0", nil
	}
	digits := text[leading:]
	for len(digits) > 1 && digits[len(digits)-1] == '0' {
		digits = digits[:len(digits)-1]
	}
	var canonical string
	switch {
	case decimalPosition <= 0:
		canonical = "0." + strings.Repeat("0", -decimalPosition) + digits
	case decimalPosition >= len(digits):
		canonical = digits + strings.Repeat("0", decimalPosition-len(digits))
	default:
		canonical = digits[:decimalPosition] + "." + digits[decimalPosition:]
	}
	if len(canonical) > maximumDecimalText {
		return "", fmt.Errorf("canonical decimal is too large")
	}
	if negative {
		canonical = "-" + canonical
	}
	return canonical, nil
}

func newUUID() string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		panic("kitdb: crypto/rand unavailable: " + err.Error())
	}
	data[6] = data[6]&0x0f | 0x40
	data[8] = data[8]&0x3f | 0x80
	return kitdbsql.FormatUUID(data)
}
