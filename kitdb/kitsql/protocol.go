// Package kitsql implements KitSQL protocol v1 over HTTP(S).
package kitsql

import (
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"
)

const (
	Version = 1
	Path    = "/_kitsql/v1/query"
)

const (
	ModeQuery = "query"
	ModeExec  = "exec"
)

type Request struct {
	Version    int     `json:"version"`
	Database   string  `json:"database"`
	Mode       string  `json:"mode"`
	SQL        string  `json:"sql"`
	Parameters []Value `json:"parameters,omitempty"`
}

type Column struct {
	Name         string `json:"name"`
	DatabaseType string `json:"database_type,omitempty"`
}

type Response struct {
	Version  int       `json:"version"`
	Columns  []Column  `json:"columns,omitempty"`
	Rows     [][]Value `json:"rows,omitempty"`
	Affected int64     `json:"affected,omitempty"`
	Error    *Error    `json:"error,omitempty"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Value struct {
	Type  string `json:"type"`
	Value any    `json:"value,omitempty"`
}

func encodeValue(source any) (Value, error) {
	switch value := source.(type) {
	case nil:
		return Value{Type: "null"}, nil
	case bool:
		return Value{Type: "boolean", Value: value}, nil
	case int:
		return Value{Type: "integer", Value: strconv.FormatInt(int64(value), 10)}, nil
	case int8:
		return Value{Type: "integer", Value: strconv.FormatInt(int64(value), 10)}, nil
	case int16:
		return Value{Type: "integer", Value: strconv.FormatInt(int64(value), 10)}, nil
	case int32:
		return Value{Type: "integer", Value: strconv.FormatInt(int64(value), 10)}, nil
	case int64:
		return Value{Type: "integer", Value: strconv.FormatInt(value, 10)}, nil
	case uint:
		return encodeUnsigned(uint64(value))
	case uint8:
		return encodeUnsigned(uint64(value))
	case uint16:
		return encodeUnsigned(uint64(value))
	case uint32:
		return encodeUnsigned(uint64(value))
	case uint64:
		return encodeUnsigned(value)
	case float32:
		return encodeFloat(float64(value)), nil
	case float64:
		return encodeFloat(value), nil
	case string:
		return Value{Type: "text", Value: value}, nil
	case []byte:
		return Value{Type: "bytes", Value: base64.StdEncoding.EncodeToString(value)}, nil
	case time.Time:
		return Value{Type: "timestamp", Value: value.UTC().Format(time.RFC3339Nano)}, nil
	case time.Duration:
		return Value{Type: "duration", Value: value.String()}, nil
	default:
		return Value{}, fmt.Errorf("kitsql: unsupported value type %T", source)
	}
}

func encodeUnsigned(value uint64) (Value, error) {
	if value > math.MaxInt64 {
		return Value{}, fmt.Errorf("kitsql: unsigned integer %d exceeds int64", value)
	}
	return Value{Type: "integer", Value: strconv.FormatUint(value, 10)}, nil
}

func encodeFloat(value float64) Value {
	return Value{
		Type:  "float",
		Value: strconv.FormatFloat(value, 'g', -1, 64),
	}
}

func (value Value) decode() (driver.Value, error) {
	switch value.Type {
	case "null":
		if value.Value != nil {
			return nil, fmt.Errorf("kitsql: null value must not contain a payload")
		}
		return nil, nil
	case "boolean":
		result, ok := value.Value.(bool)
		if !ok {
			return nil, fmt.Errorf("kitsql: boolean payload must be a boolean")
		}
		return result, nil
	case "integer":
		text, err := valueText(value)
		if err != nil {
			return nil, err
		}
		result, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("kitsql: invalid integer: %w", err)
		}
		return result, nil
	case "float":
		text, err := valueText(value)
		if err != nil {
			return nil, err
		}
		result, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return nil, fmt.Errorf("kitsql: invalid float: %w", err)
		}
		return result, nil
	case "text":
		return valueText(value)
	case "bytes":
		text, err := valueText(value)
		if err != nil {
			return nil, err
		}
		result, err := base64.StdEncoding.DecodeString(text)
		if err != nil {
			return nil, fmt.Errorf("kitsql: invalid bytes: %w", err)
		}
		return result, nil
	case "timestamp":
		text, err := valueText(value)
		if err != nil {
			return nil, err
		}
		result, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			return nil, fmt.Errorf("kitsql: invalid timestamp: %w", err)
		}
		return result, nil
	case "duration":
		return valueText(value)
	default:
		return nil, fmt.Errorf("kitsql: unsupported value encoding %q", value.Type)
	}
}

func valueText(value Value) (string, error) {
	text, ok := value.Value.(string)
	if !ok {
		return "", fmt.Errorf("kitsql: %s payload must be a string", value.Type)
	}
	return text, nil
}

func decodeJSONValue(decoder *json.Decoder, destination any) error {
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	return decoder.Decode(destination)
}
