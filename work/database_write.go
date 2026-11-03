package work

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/kitwork/engine/database/statement"
	kitdbsqlite "github.com/kitwork/engine/kitdb/sqlite"
	"github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
)

// update, delete and upsert (KITWORK_DATABASE.md mục 2, lát 3, 03/11/2026) on SQLite. A statement is one SQL statement
// on the table's file: through its writer, one commit of the history, when the engine keeps the file's history
// (database_history.go); in a transaction of its pool otherwise. update and delete give how many rows they changed;
// upsert gives the row as the file holds it after it.
//
//	db.where((orders) => orders.id === id).update((orders) => ({ amount: orders.amount + 1000 }))
//	  UPDATE "orders" SET "amount" = ("orders"."amount" + $1) WHERE "id" = $2
//	db.upsert((clickDays) => ({ linkId, day: today, count: clickDays.count + 1 }))
//	  INSERT INTO "clickDays" ("count", "day", "linkId", "id") VALUES ((0 + $1), $2, $3, $4)
//	  ON CONFLICT ("linkId", "day") DO UPDATE SET "count" = ("clickDays"."count" + $5)
//
// In the data a column is its value on the row the statement changes. The row upsert inserts has none: a column is
// null there, which + and - read as 0, as JavaScript reads null + 1.

// writeExecutor is what one write runs on: the writer's transaction (kitdbsqlite.Transaction) or a transaction of the
// pool (*sql.Tx).
type writeExecutor interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
}

// write runs fn as one write on the table's file.
func (t *SchemaTable) write(fn func(writeExecutor) error) error {
	if t.historyTx != nil {
		return fn(t.historyTx)
	}
	if t.keepsHistory() {
		database, err := t.tenant.historyDatabase(t.sqliteFile())
		if err != nil {
			return err
		}
		_, err = database.Write(t.writeContext(), historyBy(t), func(transaction *kitdbsqlite.Transaction) error {
			return fn(transaction)
		})
		return err
	}
	db := t.source().db()
	if db == nil {
		return fmt.Errorf("connection unavailable")
	}
	transaction, err := db.Begin()
	if err != nil {
		return err
	}
	if err := fn(transaction); err != nil {
		_ = transaction.Rollback()
		return err
	}
	return transaction.Commit()
}

// runWrite runs an update, a delete or an upsert.
func (m *ManagedDatabase) runWrite(prepared *preparedStatement, binder *statementBinder) value.Value {
	s := prepared.statement
	if m.proxy.engine == "kitdb" {
		return value.InvalidFailure(value.UnsupportedCode, fmt.Sprintf("%s trên KitDB chưa có trong engine hôm nay: database của app là SQLite từ 29/10/2026", s.Action))
	}
	table, failure := m.table(s.Table)
	if table == nil {
		return failure
	}
	if failure, ok := table.ready(); !ok {
		return failure
	}
	switch s.Action {
	case statement.ActionDelete:
		return runDelete(table, binder)
	case statement.ActionUpdate:
		return runUpdate(table, binder)
	}
	return runUpsert(table, binder)
}

func runDelete(table *SchemaTable, binder *statementBinder) value.Value {
	where, args, any, failure := binder.whereSQL(table, 1)
	if failure.Failed() || !any {
		return orZero(failure)
	}
	return table.affect("DELETE FROM "+quoteSearchIdentifier(table.table)+" WHERE "+where, args, "database delete error")
}

func runUpdate(table *SchemaTable, binder *statementBinder) value.Value {
	var args []any
	sets, written, failure := binder.dataSets(table, &args, false)
	if failure.Failed() {
		return failure
	}
	sets = append(sets, touchSets(table, written, &args)...)
	where, whereArgs, any, failure := binder.whereSQL(table, len(args)+1)
	if failure.Failed() || !any {
		return orZero(failure)
	}
	args = append(args, whereArgs...)
	return table.affect("UPDATE "+quoteSearchIdentifier(table.table)+" SET "+strings.Join(sets, ", ")+" WHERE "+where, args, "database update error")
}

func runUpsert(table *SchemaTable, binder *statementBinder) value.Value {
	s := binder.s
	var args []any
	var columns, values []string
	given := map[string]bool{}
	for _, field := range s.Values {
		expression, failure := binder.dataSQL(field.Column, field.Value, &args, true)
		if failure.Failed() {
			return failure
		}
		columns = append(columns, quoteSearchIdentifier(field.Column))
		values = append(values, expression)
		given[field.Column] = true
	}
	// what an insert fills when the data leaves it out: the generated key, defaults, .created() and .updated()
	automatic := map[string]value.Value{}
	for name, spec := range table.columns {
		if given[name] {
			continue
		}
		if item, ok := autoValue(spec); ok {
			automatic[name] = coerceWrite(spec.kind, item)
		}
	}
	if message := sqliteWriteRow(table.table, table.columns, automatic); message != "" {
		return value.InvalidFailure(value.InvalidArgumentCode, message)
	}
	for _, name := range sortedRowKeys(automatic) {
		args = append(args, automatic[name].Interface())
		columns = append(columns, quoteSearchIdentifier(name))
		values = append(values, fmt.Sprintf("$%d", len(args)))
	}
	target := make([]string, len(s.Target))
	isTarget := map[string]bool{}
	for position, name := range s.Target {
		target[position], isTarget[name] = quoteSearchIdentifier(name), true
	}
	statementText := "INSERT INTO " + quoteSearchIdentifier(table.table) + " (" + strings.Join(columns, ", ") + ") VALUES (" +
		strings.Join(values, ", ") + ") ON CONFLICT (" + strings.Join(target, ", ") + ") DO "
	var sets []string
	for _, field := range s.Values {
		if isTarget[field.Column] {
			continue
		}
		expression, failure := binder.dataSQL(field.Column, field.Value, &args, false)
		if failure.Failed() {
			return failure
		}
		sets = append(sets, quoteSearchIdentifier(field.Column)+" = "+expression)
	}
	if len(sets) == 0 {
		statementText += "NOTHING"
	} else {
		statementText += "UPDATE SET " + strings.Join(append(sets, touchSets(table, given, &args)...), ", ")
	}
	// the row read back by the target, whose values the data writes as they are (the compiler keeps a column out of
	// them); IS, so a null meets null
	var keys []string
	var keyArgs []any
	for _, field := range s.Values {
		if !isTarget[field.Column] {
			continue
		}
		expression, failure := binder.dataSQL(field.Column, field.Value, &keyArgs, false)
		if failure.Failed() {
			return failure
		}
		keys = append(keys, quoteSearchIdentifier(field.Column)+" IS "+expression)
	}
	var rows []map[string]value.Value
	err := table.write(func(exec writeExecutor) error {
		if _, err := exec.Exec(statementText, args...); err != nil {
			return err
		}
		read, err := exec.Query("SELECT * FROM "+quoteSearchIdentifier(table.table)+" WHERE "+strings.Join(keys, " AND "), keyArgs...)
		if err != nil {
			return err
		}
		rows, err = scanValueRows(read)
		return err
	})
	if err != nil {
		return databaseWriteFailure("database upsert error", err)
	}
	if len(rows) == 0 {
		return value.NewNil()
	}
	return sqliteReadResult(table.columns, coerceResult(table.columns, value.New(rows[0])))
}

// affect runs one statement that changes rows and gives how many it changed.
func (t *SchemaTable) affect(text string, args []any, what string) value.Value {
	var affected int64
	err := t.write(func(exec writeExecutor) error {
		result, err := exec.Exec(text, args...)
		if err != nil {
			return err
		}
		affected, err = result.RowsAffected()
		return err
	})
	if err != nil {
		return databaseWriteFailure(what, err)
	}
	return value.New(affected)
}

func orZero(failure value.Value) value.Value {
	if failure.Failed() {
		return failure
	}
	return value.New(0)
}

// touchSets stamps each .updated() column the data did not write.
func touchSets(table *SchemaTable, written map[string]bool, args *[]any) []string {
	touched := map[string]value.Value{}
	applyTouch(table.columns, touched)
	var sets []string
	for _, name := range sortedRowKeys(touched) {
		if written[name] {
			continue
		}
		*args = append(*args, touched[name].Interface())
		sets = append(sets, fmt.Sprintf("%s = $%d", quoteSearchIdentifier(name), len(*args)))
	}
	return sets
}

func sortedRowKeys(row map[string]value.Value) []string {
	names := make([]string, 0, len(row))
	for name := range row {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// dataSets binds the data of an update as SET clauses.
func (b *statementBinder) dataSets(table *SchemaTable, args *[]any, inserted bool) ([]string, map[string]bool, value.Value) {
	written := map[string]bool{}
	var sets []string
	for _, field := range b.s.Values {
		expression, failure := b.dataSQL(field.Column, field.Value, args, inserted)
		if failure.Failed() {
			return nil, nil, failure
		}
		sets = append(sets, quoteSearchIdentifier(field.Column)+" = "+expression)
		written[field.Column] = true
	}
	return sets, written, value.Value{}
}

// whereSQL binds the statement's condition as SQL, its arguments numbered from first; any is false when no row can
// match.
func (b *statementBinder) whereSQL(table *SchemaTable, first int) (string, []any, bool, value.Value) {
	predicate, truth, failure := b.predicate(b.s.Where)
	switch {
	case failure.Failed():
		return "", nil, false, failure
	case truth == bindFalse:
		return "", nil, false, value.Value{}
	case truth == bindTrue || predicate == nil:
		return "1=1", nil, true, value.Value{}
	}
	if message := sqliteWritePredicate(table.table, table.columns, predicate); message != "" {
		return "", nil, false, value.InvalidFailure(value.InvalidArgumentCode, message)
	}
	text, args := query.PredicateSQL(*predicate, first)
	return text, args, true, value.Value{}
}

// dataSQL binds one value of update or upsert data as SQL for column, its arguments appended to args. On the row the
// statement changes a column is the row's value; on the row upsert inserts (inserted) a column is null, 0 under + or
// -. A value is checked against column's kind; under + or - null is 0, as JavaScript reads it.
func (b *statementBinder) dataSQL(column string, node *statement.Node, args *[]any, inserted bool) (string, value.Value) {
	spec := b.columns[column]
	refused := func(site string) value.Value {
		return value.InvalidFailure(value.InvalidArgumentCode,
			fmt.Sprintf("cột %s của %s không nhận null (giá trị ở %s); cột được bỏ trống khai .nullable()", column, b.s.Table, site))
	}
	var render func(node *statement.Node, operand bool) (string, value.Value)
	render = func(node *statement.Node, operand bool) (string, value.Value) {
		switch node.Op {
		case statement.OpColumn:
			switch {
			case inserted && operand:
				return "0", value.Value{}
			case inserted:
				return "NULL", value.Value{}
			}
			return quoteSearchIdentifier(b.s.Table) + "." + quoteSearchIdentifier(node.Column), value.Value{}
		case statement.OpAdd, statement.OpSubtract:
			left, failure := render(node.Args[0], true)
			if failure.Failed() {
				return "", failure
			}
			right, failure := render(node.Args[1], true)
			if failure.Failed() {
				return "", failure
			}
			return "(" + left + " " + node.Op + " " + right + ")", value.Value{}
		}
		item, site := b.value(node)
		if item.IsNil() {
			switch {
			case operand:
				return "0", value.Value{}
			case spec != nil && spec.notNull:
				return "", refused(site)
			}
			return "NULL", value.Value{}
		}
		stored, failure := bindValue(column, spec, item, site)
		if failure.Failed() {
			return "", failure
		}
		if _, decimal := sqliteDecimal(spec); decimal {
			converted, message := sqliteWrite(spec, stored)
			if message != "" {
				return "", value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("bảng %s, cột %s: %s", b.s.Table, column, message))
			}
			stored = converted
		}
		*args = append(*args, stored.Interface())
		return fmt.Sprintf("$%d", len(*args)), value.Value{}
	}
	return render(node, false)
}
