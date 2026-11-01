package work

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
)

// A decimal on SQLite is the integer its value times 10^scale (KITWORK_KITDB.md mục 8 and câu 13.1, decided
// 27/10/2026, made 29/10/2026): it compares, orders and sums as a number, exactly. Stored as its text it
// compared as text: amount > 5 found no row holding 35000 ("3" before "5"), and an order put 10 before 4.5,
// on every SQLite file of the host's catalogue (connection("tên")). Only the SQLite path converts: KitDB keeps
// its own codec, and the helpers both paths share (coerceWrite, coerceRead, storageClass) are left as they are.
//
// A decimal keeps 15 digits at most (KITWORK_DATABASE.md mục 1), so the integer and the number JavaScript
// reads back both hold it exactly; a value with more fractional digits than the column declares is rounded
// half away from zero (how to round is open, KITWORK_DATABASE.md câu 8.2).

// sqliteDecimalLimit is the largest integer a decimal of 15 digits stores.
const sqliteDecimalLimit = 999_999_999_999_999

// sqliteDecimal reports a decimal column and its scale.
func sqliteDecimal(spec *ColumnSpec) (int, bool) {
	if spec == nil || spec.kind != "decimal" {
		return 0, false
	}
	return spec.scale, true
}

// sqliteStorage is a column's SQLite type: INTEGER for a decimal, what storageClass says for any other.
func sqliteStorage(spec *ColumnSpec) string {
	if _, decimal := sqliteDecimal(spec); decimal {
		return "INTEGER"
	}
	return storageClass(spec.kind)
}

// sqliteWrite is a value as SQLite stores it in spec's column: the scaled integer for a decimal, what
// coerceWrite gives for any other kind; message says why a decimal has none.
func sqliteWrite(spec *ColumnSpec, item value.Value) (value.Value, string) {
	scale, decimal := sqliteDecimal(spec)
	if !decimal || item.IsNil() {
		return coerceWrite(spec.kind, item), ""
	}
	var number float64
	switch item.K {
	case value.Number:
		number = item.N
	case value.String:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(item.String()), 64)
		if err != nil {
			return item, "không phải số thập phân"
		}
		number = parsed
	default:
		return item, "không phải số thập phân"
	}
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return item, "không phải số thập phân"
	}
	scaled := math.Round(number * math.Pow10(scale))
	if math.Abs(scaled) > sqliteDecimalLimit {
		return item, "vượt 15 chữ số của decimal"
	}
	return value.New(scaled), ""
}

// sqliteRead is a stored value as JavaScript reads it: a decimal's integer divided by 10^scale. A decimal a
// table kept as text from before (a file the migration has not changed) reads as coerceRead gives it.
func sqliteRead(spec *ColumnSpec, item value.Value) value.Value {
	scale, decimal := sqliteDecimal(spec)
	if !decimal || item.K != value.Number {
		return item
	}
	return value.New(item.N / math.Pow10(scale))
}

// sqliteWriteRow converts the decimals of a row about to be written; the message names a column whose value
// has no stored form.
func sqliteWriteRow(table string, columns map[string]*ColumnSpec, row map[string]value.Value) string {
	for name, item := range row {
		spec := columns[name]
		if _, decimal := sqliteDecimal(spec); !decimal {
			continue
		}
		stored, message := sqliteWrite(spec, item)
		if message != "" {
			return fmt.Sprintf("bảng %s, cột %s: %s", table, name, message)
		}
		row[name] = stored
	}
	return ""
}

// sqliteReadResult reads the decimals of a result back: a row, or a list of rows.
func sqliteReadResult(columns map[string]*ColumnSpec, result value.Value) value.Value {
	switch result.K {
	case value.Array:
		for _, row := range result.Array() {
			sqliteReadRow(columns, row)
		}
	case value.Map:
		sqliteReadRow(columns, result)
	}
	return result
}

func sqliteReadRow(columns map[string]*ColumnSpec, row value.Value) {
	if row.K != value.Map {
		return
	}
	fields := row.Map()
	for name, spec := range columns {
		if item, found := fields[name]; found {
			fields[name] = sqliteRead(spec, item)
		}
	}
}

// sqliteWritePredicate converts the values a condition compares a decimal column with, so the column's
// integers meet integers; the message says why one has none.
func sqliteWritePredicate(table string, columns map[string]*ColumnSpec, predicate *query.Predicate) string {
	if predicate == nil {
		return ""
	}
	for index := range predicate.Children {
		if message := sqliteWritePredicate(table, columns, &predicate.Children[index]); message != "" {
			return message
		}
	}
	condition := &predicate.Condition
	spec := columns[condition.Column]
	if _, decimal := sqliteDecimal(spec); !decimal || condition.IsColumn {
		return ""
	}
	item, isValue := condition.Value.(value.Value)
	if !isValue {
		return ""
	}
	if item.K == value.Array {
		list := item.Array()
		stored := make([]value.Value, len(list))
		for index, entry := range list {
			converted, message := sqliteWrite(spec, entry)
			if message != "" {
				return fmt.Sprintf("bảng %s, cột %s: %s", table, condition.Column, message)
			}
			stored[index] = converted
		}
		condition.Value = value.New(stored)
		return ""
	}
	converted, message := sqliteWrite(spec, item)
	if message != "" {
		return fmt.Sprintf("bảng %s, cột %s: %s", table, condition.Column, message)
	}
	condition.Value = converted
	return ""
}
