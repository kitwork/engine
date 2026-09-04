package relational

import (
	"context"
	"path/filepath"
	"testing"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func TestAttachExecutePlanUsesBorrowedKernelDirectly(t *testing.T) {
	ctx := context.Background()
	database, err := kitdbengine.Open(filepath.Join(t.TempDir(), "embedded.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	engine, err := Attach(database, Options{})
	if err != nil {
		t.Fatal(err)
	}

	textType, found := kitdbsql.ResolveName("text")
	if !found {
		t.Fatal("text type is not registered")
	}
	create := &kitdbsql.ParsedStatement{
		Kind: kitdbsql.StatementCreate,
		CreateTable: &kitdbsql.CreateTableStatement{
			Name: "products",
			Columns: []kitdbsql.ColumnDefinition{
				{Name: "id", Type: textType, NotNull: true},
				{Name: "title", Type: textType, NotNull: true},
			},
			PrimaryKey: []string{"id"},
		},
	}
	if _, err := engine.ExecutePlan(ctx, create); err != nil {
		t.Fatal(err)
	}

	insert := &kitdbsql.ParsedStatement{
		Kind: kitdbsql.StatementInsert,
		Insert: &kitdbsql.InsertStatement{
			Table:   "products",
			Columns: []string{"id", "title"},
			Rows: [][]kitdbsql.Literal{{
				{Kind: kitdbsql.LiteralString, Text: "product-1"},
				{Kind: kitdbsql.LiteralString, Text: "Direct Go plan"},
			}},
		},
	}
	if result, err := engine.ExecutePlan(ctx, insert); err != nil {
		t.Fatal(err)
	} else if result.Affected != 1 {
		t.Fatalf("insert affected = %d, want 1", result.Affected)
	}

	transaction, err := engine.BeginTransaction(ctx, TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	update := &kitdbsql.ParsedStatement{
		Kind: kitdbsql.StatementUpdate,
		Update: &kitdbsql.UpdateStatement{
			Table: "products",
			Assignments: []kitdbsql.Assignment{{
				Column: "title",
				Value:  kitdbsql.Literal{Kind: kitdbsql.LiteralString, Text: "Typed transaction"},
			}},
			Conditions: []kitdbsql.Condition{{
				Column: "id", Operator: "=",
				Value: kitdbsql.Literal{Kind: kitdbsql.LiteralString, Text: "product-1"},
			}},
		},
	}
	if _, err := transaction.ExecutePlan(ctx, update); err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	if _, err := transaction.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	selectPlan := &kitdbsql.ParsedStatement{
		Kind: kitdbsql.StatementSelect,
		Select: &kitdbsql.SelectStatement{
			Table:      "products",
			Projection: []kitdbsql.Projection{{Name: "title"}},
			Conditions: []kitdbsql.Condition{{
				Column: "id", Operator: "=",
				Value: kitdbsql.Literal{Kind: kitdbsql.LiteralString, Text: "product-1"},
			}},
		},
	}
	result, err := engine.ExecutePlan(ctx, selectPlan)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 1 || len(result.Rows[0]) != 1 || result.Rows[0][0] != "Typed transaction" {
		t.Fatalf("select rows = %#v", result.Rows)
	}

	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Catalog(); err != nil {
		t.Fatalf("Attach transferred ownership of the borrowed kernel: %v", err)
	}
}

func TestExecutePlanRejectsNilPlan(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "nil-plan.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if _, err := engine.ExecutePlan(context.Background(), nil); err == nil {
		t.Fatal("nil execution plan was accepted")
	}
}
