package relational

import (
	"testing"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func TestLocalRoleUpsertStoredReadClassification(t *testing.T) {
	for _, test := range []struct {
		expression string
		stored     bool
	}{
		{"excluded.n", false}, {"EXCLUDED.n+1", false}, {"42", false}, {"$1", false},
		{"n", true}, {"items.n", true}, {"public.items.n", true}, {"excluded.n+n", true},
		{"CAST(excluded.n AS BIGINT)", false}, {"CAST(n AS BIGINT)", true},
	} {
		q := "INSERT INTO items(id,n) VALUES(1,2) ON CONFLICT(id) DO UPDATE SET n=" + test.expression
		st, err := kitdbsql.ParseStatement(q)
		if err != nil {
			t.Fatal(q, err)
		}
		expr := st.Insert.Conflict.Assignments[0].Expression
		if got := localExpressionReadsStored(expr, true); got != test.stored {
			t.Fatalf("%s: stored=%v want %v", q, got, test.stored)
		}
	}
}
