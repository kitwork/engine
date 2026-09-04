package postgres

import (
	"strings"
	"testing"
)

func TestShoppingFullTextProfileQueryQuotesRelationNames(t *testing.T) {
	query := shoppingFullTextProfileQuery(`odd"schema`, `shopping"archive`)
	if !strings.Contains(query, `FROM "odd""schema"."shopping""archive"`) {
		t.Fatalf("query did not quote relation names: %s", query)
	}
	if !strings.Contains(query, `LIMIT $1`) || !strings.Contains(query, `$2`) {
		t.Fatalf("query lost bounded parameters: %s", query)
	}
}
