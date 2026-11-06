package work

import (
	"fmt"

	"github.com/kitwork/engine/database/statement"
	"github.com/kitwork/engine/value"
)

// kitwork/schema (KITWORK_DATABASE.md mục 1): table and the column builders an app's .shared/schema/
// declares its tables with.
//
//	import { table, id, string, decimal, date, timestamp } from "kitwork/schema";
//	const orders = table({ id: id(), email: string(), amount: decimal(12, 2), note: string().nullable(),
//	  day: date().index(), createdAt: timestamp().created() });
//
// Each builder is a getter giving a function, so a destructured import is the function itself. A column
// refuses null unless .nullable(); the compiler reads the same file and checks every statement against
// it, and the engine checks again with the columns these builders made (ManagedDatabase).
func (w *KitWork) Schema() *SchemaModule { return &SchemaModule{} }

// SchemaModule is kitwork/schema.
type SchemaModule struct{}

func (m *SchemaModule) Table() value.Value {
	return value.NewFunc(func(args ...value.Value) value.Value {
		return newStructDef(args, true)
	})
}

// Struct is the word table replaced on 29/10/2026: its call is the error that says so.
func (m *SchemaModule) Struct() value.Value {
	return value.NewFunc(func(_ ...value.Value) value.Value {
		return value.InvalidFailure(value.InvalidArgumentCode, statement.TableRenamed)
	})
}

func (m *SchemaModule) Id() value.Value        { return schemaColumn("id") }
func (m *SchemaModule) String() value.Value    { return schemaColumn("string") }
func (m *SchemaModule) Uuid() value.Value      { return schemaColumn("uuid") }
func (m *SchemaModule) Integer() value.Value   { return schemaColumn("integer") }
func (m *SchemaModule) Float() value.Value     { return schemaColumn("float") }
func (m *SchemaModule) Decimal() value.Value   { return schemaColumn("decimal") }
func (m *SchemaModule) Boolean() value.Value   { return schemaColumn("boolean") }
func (m *SchemaModule) Date() value.Value      { return schemaColumn("date") }
func (m *SchemaModule) Json() value.Value      { return schemaColumn("json") }
func (m *SchemaModule) Timestamp() value.Value { return schemaColumn("timestamp") }

// Text is the word string replaced on 06/11/2026: its call is the error that says so.
func (m *SchemaModule) Text() value.Value {
	return value.NewFunc(func(_ ...value.Value) value.Value {
		return value.InvalidFailure(value.InvalidArgumentCode, "text() là cách viết cũ: "+statement.LegacyBuilders["text"])
	})
}

// Bigint and Computed are words of the spec the engine does not have yet: their call is the error that
// says so, never a column of another kind.
func (m *SchemaModule) Bigint() value.Value   { return schemaColumnNotYet("bigint") }
func (m *SchemaModule) Computed() value.Value { return schemaColumnNotYet("computed") }

// schemaKinds maps a builder of the spec to the kind the storage layer knows it by.
var schemaKinds = map[string]string{
	"id":        "kitid",
	"string":    "text", // a string kept as SQLite's TEXT, PostgreSQL's text
	"uuid":      "uuid",
	"integer":   "integer",
	"float":     "float",
	"decimal":   "decimal",
	"boolean":   "bool",
	"date":      "date",
	"json":      "json",
	"timestamp": "datetime",
}

func schemaColumn(name string) value.Value {
	builder := statement.Builders[name]
	return value.NewFunc(func(args ...value.Value) value.Value {
		spec := &ColumnSpec{kind: schemaKinds[name], vocabulary: true, builder: name, primary: builder.Primary,
			timestamp: builder.Timestamp, seq: nextColSeq()}
		switch {
		case builder.Parameters > 0:
			if len(args) != builder.Parameters || !wholeNumber(args[0]) || !wholeNumber(args[1]) ||
				args[0].N < 1 || args[1].N > args[0].N {
				spec.declareErr = fmt.Sprintf("%s nhận độ dài và số lẻ là hai số nguyên: %s(12, 2)", name, name)
				break
			}
			spec.precision, spec.scale = int(args[0].N), int(args[1].N)
		case len(args) != 0:
			spec.declareErr = fmt.Sprintf("%s() không nhận đối số", name)
		}
		return value.New(spec)
	})
}

func schemaColumnNotYet(name string) value.Value {
	return value.NewFunc(func(_ ...value.Value) value.Value {
		return value.InvalidFailure(value.UnsupportedCode, fmt.Sprintf("%s() chưa có trong engine hôm nay (lát %d của KITWORK_DATABASE.md)", name, statement.BuildersNotYet[name]))
	})
}

func wholeNumber(item value.Value) bool {
	return item.K == value.Number && item.N == float64(int64(item.N))
}
