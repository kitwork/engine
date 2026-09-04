package work

import (
	"testing"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func TestKitSQLLexerBridgeMatchesIndependentContract(t *testing.T) {
	sources := []string{
		`SELECT * FROM shopping WHERE merchant = $1 AND name LIKE '%coffee%' LIMIT 20;`,
		`CREATE TABLE products (id KITID PRIMARY KEY, status CHOICE('active', 'disabled'));`,
		"SELECT/* bounded */name -- comment\nFROM [shopping] WHERE price >= 10.5",
	}
	for _, source := range sources {
		publicTokens, publicErr := kitdbsql.Lex(source)
		bridgeTokens, bridgeErr := tokenizeKitSQL(source)
		if publicErr != nil || bridgeErr != nil {
			t.Fatalf("Lex(%q) errors: public=%v bridge=%v", source, publicErr, bridgeErr)
		}
		if len(publicTokens) != len(bridgeTokens) {
			t.Fatalf("Lex(%q) lengths: public=%d bridge=%d", source, len(publicTokens), len(bridgeTokens))
		}
		for index, public := range publicTokens {
			bridge := bridgeTokens[index]
			if public.Kind != bridge.kind || public.Text != bridge.text {
				t.Fatalf("Lex(%q) token %d: public=%#v bridge=%#v", source, index, public, bridge)
			}
		}
	}
}

func TestKitSQLLexerBridgePreservesErrors(t *testing.T) {
	for _, source := range []string{"", "/*", "'value", ":", "|"} {
		_, publicErr := kitdbsql.Lex(source)
		_, bridgeErr := tokenizeKitSQL(source)
		if publicErr == nil || bridgeErr == nil || publicErr.Error() != bridgeErr.Error() {
			t.Fatalf("Lex(%q) errors: public=%v bridge=%v", source, publicErr, bridgeErr)
		}
	}
}

func TestKitSQLStatementEnvelopeFeedsExistingParser(t *testing.T) {
	sources := []string{
		"SELECT 1",
		"SELECT * FROM products WHERE id = '1'",
		"INSERT INTO products (id) VALUES ('1')",
		"UPDATE products SET name = 'x' WHERE id = '1'",
		"DELETE FROM products WHERE id = '1'",
		"CREATE TABLE products (id KITID PRIMARY KEY)",
		"ALTER TABLE products ADD COLUMN name TEXT",
		"DROP TABLE products",
		"ANALYZE products",
		"PRAGMA table_info('products')",
		"EXPLAIN QUERY PLAN SELECT * FROM products",
	}
	for _, source := range sources {
		envelope, envelopeErr := kitdbsql.ParseEnvelope(source)
		if envelopeErr != nil {
			t.Fatalf("ParseEnvelope(%q): %v", source, envelopeErr)
		}
		if envelope.Kind == kitdbsql.StatementUnknown || envelope.Body <= 0 {
			t.Fatalf("ParseEnvelope(%q) = %#v", source, envelope)
		}
		if _, err := parseKitSQL(source, kitSQLBindings{}); err != nil {
			t.Fatalf("parseKitSQL(%q): %v", source, err)
		}
	}
}

func TestKitSQLStatementEnvelopePreservesDispatchErrors(t *testing.T) {
	for _, source := range []string{
		"VACUUM",
		"EXPLAIN DELETE FROM products",
		"EXPLAIN QUERY SELECT * FROM products",
	} {
		_, envelopeErr := kitdbsql.ParseEnvelope(source)
		_, parserErr := parseKitSQL(source, kitSQLBindings{})
		if envelopeErr == nil || parserErr == nil || envelopeErr.Error() != parserErr.Error() {
			t.Fatalf("dispatch errors for %q: envelope=%v parser=%v", source, envelopeErr, parserErr)
		}
	}
}

func TestKitSQLStatementEnvelopeDoesNotDowngradeExplainAnalyze(t *testing.T) {
	source := "EXPLAIN ANALYZE SELECT * FROM products"
	envelope, err := kitdbsql.ParseEnvelope(source)
	if err != nil || !envelope.ExplainAnalyze {
		t.Fatalf("standalone envelope = %#v, %v", envelope, err)
	}
	if _, err := parseKitSQL(source, kitSQLBindings{}); err == nil ||
		err.Error() != "kitdb SQL: EXPLAIN ANALYZE requires the standalone KitDB executor" {
		t.Fatalf("legacy bridge error = %v", err)
	}
}
