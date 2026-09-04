package pgwire

import "testing"

func TestReadQueryRecognizesReturningWithoutMistakingColumnName(t *testing.T) {
	if !isReadQuery(`WITH chosen AS (SELECT 1) SELECT * FROM chosen`) {
		t.Fatal("WITH SELECT was not recognized as a row-producing query")
	}
	for _, source := range []string{
		`UPDATE products SET title = 'changed' RETURNING id`,
		`DELETE FROM products WHERE id = 1 RETURNING *`,
		`INSERT INTO products (id) VALUES (1) RETURNING id`,
	} {
		if !isReadQuery(source) {
			t.Fatalf("row-producing query not recognized: %s", source)
		}
	}
	for _, source := range []string{
		`UPDATE products SET "returning" = 'value' WHERE id = 1`,
		`INSERT INTO products ("returning") VALUES ('value')`,
		`DELETE FROM returning WHERE id = 1`,
	} {
		if isReadQuery(source) {
			t.Fatalf("non-row query was mistaken for RETURNING: %s", source)
		}
	}
}
