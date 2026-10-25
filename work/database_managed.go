package work

import (
	"fmt"
	"sort"
	"sync"

	"github.com/kitwork/engine/database/statement"
	"github.com/kitwork/engine/value"
)

// ManagedDatabase is the handle connection().schema({ … }) gives: the app's database, declared in its
// .shared/schema/ (KITWORK_DATABASE.md mục 1). It answers one call, the one the compiler writes for every
// statement on an imported handle (database_translate.go of the compiler):
//
//	db.list((orders) => orders.day === today)  →  db.#statement("<template>", today)
//
// A statement is checked when it first runs, before anything opens: its template is decoded, its names
// are checked against the live schema with the rules the compiler checked them with (the compiler read
// the schema file, this reads what its builders made), and each literal against its column's kind. Every
// run then binds the holes by column kind and runs it through the table's query (SchemaTable), on the
// app's KitDB in .database/application/. A query lambda is never run.
//
// A route's top-level const holds the handle and every request of the route shares it, so the cache of
// checked statements is a sync.Map and each statement builds its own query.
type ManagedDatabase struct {
	proxy   *dbProxy
	catalog *statement.Database
	// prepared holds, by template, the statement checked once (*preparedStatement).
	prepared sync.Map
}

// preparedStatement is a template checked against the live schema; failure is why it was refused.
type preparedStatement struct {
	statement *statement.Statement
	columns   map[string]*ColumnSpec
	failure   value.Value
}

func newManagedDatabase(proxy *dbProxy) *ManagedDatabase {
	return &ManagedDatabase{proxy: proxy, catalog: catalogOf(proxy)}
}

func (m *ManagedDatabase) OnGet(key string) value.Value {
	return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("db.%s không có: câu viết db.list((bảng) => …), bảng là tham số của lambda", key))
}

func (m *ManagedDatabase) OnCompare(_ string, _ value.Value) value.Value { return value.NewNil() }

// OnInvoke runs a translated statement. Any other call reached the handle without the compiler seeing
// it (a namespace import of kitwork/database, a handle that came another way): it is refused, and its
// lambda never runs.
func (m *ManagedDatabase) OnInvoke(method string, args ...value.Value) value.Value {
	if method != statement.Method {
		return value.InvalidFailure(value.UnsupportedCode, fmt.Sprintf("db.%s(…) chỉ chạy khi viết thẳng trên tên import từ app/.shared/schema/ (import { db } from \"app/.shared/schema/shop.work\"): câu database được dịch lúc biên dịch", method))
	}
	if len(args) == 0 || args[0].K != value.String {
		return value.InvalidFailure(value.InvalidArgumentCode, "câu database không có template: dịch lại")
	}
	prepared := m.prepare(args[0].Text())
	if prepared.failure.Failed() {
		return prepared.failure
	}
	return m.run(prepared, args[1:])
}

// prepare checks a template once, before anything opens, and keeps the result: a refused template is
// refused the same way every time.
func (m *ManagedDatabase) prepare(template string) *preparedStatement {
	if cached, ok := m.prepared.Load(template); ok {
		return cached.(*preparedStatement)
	}
	prepared := &preparedStatement{}
	decoded, err := statement.Decode(template)
	switch {
	case err != nil:
		prepared.failure = value.InvalidFailure(value.InvalidArgumentCode, err.Error())
	default:
		if err := m.catalog.CheckNames(decoded); err != nil {
			prepared.failure = value.InvalidFailure(value.InvalidArgumentCode, err.Error())
			break
		}
		tables, _ := m.proxy.schemaSnapshot()
		prepared.statement = decoded
		prepared.columns = tables[decoded.Table]
		if failure := checkLiterals(decoded, prepared.columns); failure.Failed() {
			prepared.statement, prepared.failure = nil, failure
		}
	}
	actual, _ := m.prepared.LoadOrStore(template, prepared)
	return actual.(*preparedStatement)
}

// catalogOf is the live schema of a database in the statement package's words: the tables, each with its
// columns in the order they were declared.
func catalogOf(proxy *dbProxy) *statement.Database {
	tables, _ := proxy.schemaSnapshot()
	names := make([]string, 0, len(tables))
	first := map[string]uint64{}
	for name, columns := range tables {
		names = append(names, name)
		for _, spec := range columns {
			if first[name] == 0 || spec.seq < first[name] {
				first[name] = spec.seq
			}
		}
	}
	sort.Slice(names, func(left, right int) bool {
		if first[names[left]] != first[names[right]] {
			return first[names[left]] < first[names[right]]
		}
		return names[left] < names[right]
	})
	catalog := &statement.Database{}
	for _, name := range names {
		columns := tables[name]
		table := statement.Table{Name: name}
		for _, column := range orderedColumns(columns) {
			table.Columns = append(table.Columns, catalogColumn(column, columns[column]))
		}
		catalog.Tables = append(catalog.Tables, table)
	}
	return catalog
}

// catalogColumn is a column of kitwork/schema in the spec's words.
func catalogColumn(name string, spec *ColumnSpec) statement.Column {
	kind := spec.builder
	if kind == "" {
		kind = spec.kind
	}
	return statement.Column{
		Name:           name,
		Kind:           kind,
		Nullable:       spec.nullable,
		Unique:         spec.unique,
		Search:         spec.searchable,
		Primary:        spec.primary,
		Generated:      spec.primary,
		WriteProtected: spec.writeProtected,
		HasDefault:     spec.hasDefault,
		Precision:      spec.precision,
		Scale:          spec.scale,
	}
}
