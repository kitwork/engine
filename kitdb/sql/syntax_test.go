package sql

import "testing"

func TestParseEnvelopeClassifiesSupportedStatements(t *testing.T) {
	tests := []struct {
		source  string
		kind    StatementKind
		body    string
		analyze bool
	}{
		{"SELECT * FROM products", StatementSelect, "*", false},
		{"WITH chosen AS (SELECT * FROM products) SELECT * FROM chosen", StatementSelect, "chosen", false},
		{"INSERT INTO products (id) VALUES ('1')", StatementInsert, "INTO", false},
		{"UPDATE products SET name = 'x'", StatementUpdate, "products", false},
		{"DELETE FROM products", StatementDelete, "FROM", false},
		{"CREATE TABLE products (id KITID)", StatementCreate, "TABLE", false},
		{"ALTER TABLE products ADD COLUMN name TEXT", StatementAlter, "TABLE", false},
		{"DROP TABLE products", StatementDrop, "TABLE", false},
		{"ANALYZE products", StatementAnalyze, "products", false},
		{"REINDEX TABLE products", StatementReindex, "TABLE", false},
		{"PRAGMA table_info('products')", StatementPragma, "table_info", false},
		{"EXPLAIN SELECT * FROM products", StatementExplain, "*", false},
		{"EXPLAIN QUERY PLAN SELECT * FROM products", StatementExplain, "*", false},
		{"EXPLAIN ANALYZE SELECT * FROM products", StatementExplain, "*", true},
		{"EXPLAIN ANALYZE WITH chosen AS (SELECT * FROM products) SELECT * FROM chosen", StatementExplain, "chosen", true},
	}
	for _, test := range tests {
		t.Run(test.kind.String()+"/"+test.body, func(t *testing.T) {
			envelope, err := ParseEnvelope(test.source)
			if err != nil {
				t.Fatal(err)
			}
			if envelope.Kind != test.kind || envelope.ExplainAnalyze != test.analyze ||
				envelope.Body >= len(envelope.Tokens) || envelope.Tokens[envelope.Body].Text != test.body {
				t.Fatalf("ParseEnvelope(%q) = kind %s body %d tokens %#v", test.source, envelope.Kind, envelope.Body, envelope.Tokens)
			}
		})
	}
}

func TestParseEnvelopePreservesDispatchErrors(t *testing.T) {
	tests := []struct {
		source string
		want   string
	}{
		{"VACUUM", "kitdb SQL: only SELECT/WITH, EXPLAIN [QUERY PLAN | ANALYZE] SELECT/WITH, INSERT, UPDATE, DELETE, ANALYZE, REINDEX, supported CREATE/ALTER/DROP and PRAGMA statements are accepted"},
		{"EXPLAIN DELETE FROM products", "kitdb SQL: EXPLAIN accepts SELECT or WITH only"},
		{"EXPLAIN QUERY SELECT * FROM products", `kitdb SQL: expected PLAN, got "SELECT"`},
		{"EXPLAIN ANALYZE DELETE FROM products", "kitdb SQL: EXPLAIN accepts SELECT or WITH only"},
		{"EXPLAIN QUERY PLAN ANALYZE SELECT * FROM products", "kitdb SQL: EXPLAIN QUERY PLAN cannot be combined with ANALYZE"},
	}
	for _, test := range tests {
		_, err := ParseEnvelope(test.source)
		if err == nil || err.Error() != test.want {
			t.Fatalf("ParseEnvelope(%q) error = %v, want %q", test.source, err, test.want)
		}
	}
}
