package sql

import (
	"strings"
	"testing"
)

func TestInsertExpressionParsing(t *testing.T) {
	plan, err := ParseStatement(`INSERT INTO items (id,amount,label) VALUES (1,2,'a'), (DEFAULT,$1 * 2,upper(?)) RETURNING *, amount + $2 AS doubled`)
	if err != nil {
		t.Fatal(err)
	}
	p := plan.Insert
	if p == nil || len(p.Rows) != 0 || len(p.Values) != 2 || p.Values[0][0].Literal.Text != "1" || p.Values[1][0].Literal.Kind != LiteralDefault || p.Values[1][1].Kind != "binary" || p.Values[1][2].Operator != "upper" || len(p.Returning) != 2 {
		t.Fatalf("expression plan: %#v", p)
	}
	plan, err = ParseStatement(`INSERT INTO items VALUES (-1,$1,'text'),(DEFAULT,NULL,?) RETURNING upper(label)`)
	if err != nil || len(plan.Insert.Values) != 0 || len(plan.Insert.Rows) != 2 || plan.Insert.Rows[0][0].Text != "-1" {
		t.Fatalf("literal compatibility: %#v %v", plan, err)
	}
	for _, source := range []string{
		`INSERT INTO items VALUES ()`,
		`INSERT INTO items VALUES (1 + 1),(2,3)`,
		`INSERT INTO items (id,label) VALUES (1 + 1)`,
		`INSERT INTO items VALUES (DEFAULT + 1)`,
		`INSERT INTO items VALUES (` + strings.Repeat("(", maximumExpressionDepth+1) + "1" + strings.Repeat(")", maximumExpressionDepth+1) + ")",
		`INSERT INTO items VALUES (coalesce(` + strings.Repeat("1,", maximumExpressionNodes) + "1))",
		`INSERT INTO items VALUES ` + strings.Repeat("(1),", MaximumInsertRows) + "(1)",
	} {
		if _, err := ParseStatement(source); err == nil {
			t.Fatalf("accepted invalid INSERT: %.120s", source)
		}
	}
}
