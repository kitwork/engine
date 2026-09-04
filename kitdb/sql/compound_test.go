package sql

import (
	"strings"
	"testing"
)

func TestParseUnionAllWithGlobalTail(t *testing.T) {
	statement, err := ParseStatement(`
		SELECT id AS item_id, name FROM active_items WHERE id >= $1
		UNION ALL
		SELECT id, name FROM archived_items WHERE id < $2
		UNION ALL
		SELECT 99, 'sentinel'
		ORDER BY item_id DESC, 2 ASC
		LIMIT 20 OFFSET 3
	`)
	if err != nil {
		t.Fatal(err)
	}
	plan := statement.Select
	if plan == nil || len(plan.SetOperations) != 2 || plan.SetOperations[0].Kind != "union all" {
		t.Fatalf("UNION ALL plan = %#v", plan)
	}
	if plan.Predicate == nil || plan.Predicate.Arguments[1].Literal.Parameter != 1 {
		t.Fatalf("first predicate = %#v", plan.Predicate)
	}
	second := plan.SetOperations[0].Select
	if second == nil || second.Table != "archived_items" || second.Predicate == nil ||
		second.Predicate.Arguments[1].Literal.Parameter != 2 {
		t.Fatalf("second branch = %#v", second)
	}
	third := plan.SetOperations[1].Select
	if third == nil || third.Table != "" || len(third.Projection) != 2 {
		t.Fatalf("third branch = %#v", third)
	}
	if len(plan.Order) != 2 || plan.Order[0].Column != "item_id" || !plan.Order[0].Descending ||
		plan.Order[1].Expression == nil || plan.Limit != 20 || plan.Offset != 3 {
		t.Fatalf("global tail = %#v", plan)
	}
	if len(second.Order) != 0 || second.HasLimit || second.Offset != 0 {
		t.Fatalf("tail leaked into branch = %#v", second)
	}
}

func TestParseUnionAllRejectsUnsupportedOrUnboundedShapes(t *testing.T) {
	for _, source := range []string{
		`SELECT 1 UNION SELECT 2`,
		`SELECT 1 UNION DISTINCT SELECT 2`,
		`SELECT * FROM products WHERE * SEARCH 'keyboard' UNION ALL SELECT * FROM archived_products`,
		`SELECT 1 UNION ALL VALUES (2)`,
	} {
		if _, err := ParseStatement(source); err == nil {
			t.Fatalf("accepted unsupported set query: %s", source)
		}
	}

	tooMany := "SELECT 0" + strings.Repeat(" UNION ALL SELECT 0", MaximumSetOperations+1)
	if _, err := ParseStatement(tooMany); err == nil || !strings.Contains(err.Error(), "set operations") {
		t.Fatalf("set-operation limit error = %v", err)
	}
}

func TestParseSelectBoundsJoinChain(t *testing.T) {
	source := "SELECT b.id FROM base b"
	for index := 0; index <= MaximumSelectJoins; index++ {
		alias := "j" + string(rune('a'+index))
		source += " JOIN joined " + alias + " ON " + alias + ".id = b.id"
	}
	if _, err := ParseStatement(source); err == nil || !strings.Contains(err.Error(), "JOIN clauses") {
		t.Fatalf("JOIN limit error = %v", err)
	}
}
