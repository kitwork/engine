package sql

import (
	"strings"
	"testing"
)

func TestDomainParser(t *testing.T) {
	plan, err := ParseStatement(`CREATE DOMAIN positive_amount AS NUMERIC(12,2) DEFAULT 0 NOT NULL CONSTRAINT positive CHECK (VALUE >= 0)`)
	if err != nil {
		t.Fatal(err)
	}
	domain := plan.CreateDomain
	if domain == nil || domain.Name != "positive_amount" || domain.Column.Type.Kind != "decimal" || domain.Column.Precision != 12 || domain.Column.Scale != 2 || !domain.Column.NotNull || !domain.Column.HasDefault || len(domain.Column.Checks) != 1 {
		t.Fatalf("domain = %#v", domain)
	}
	plan, err = ParseStatement(`CREATE TABLE products (id SERIAL PRIMARY KEY, price positive_amount DEFAULT 12)`)
	if err != nil || plan.CreateTable.Columns[1].DomainName != "positive_amount" {
		t.Fatalf("domain column = %#v: %v", plan, err)
	}
	plan, err = ParseStatement(`DROP DOMAIN IF EXISTS positive_amount RESTRICT`)
	if err != nil || !plan.DropDomain.IfExists {
		t.Fatalf("drop = %#v: %v", plan, err)
	}
	for _, query := range []string{
		`CREATE DOMAIN d INTEGER DEFAULT 1 DEFAULT 2`,
		`CREATE DOMAIN d INTEGER NULL NOT NULL`,
		`CREATE OR REPLACE DOMAIN d INTEGER`,
		`CREATE DOMAIN public.d INTEGER`,
		`CREATE DOMAIN d INTEGER CHECK ((SELECT 1) > 0)`,
		`CREATE DOMAIN d INTEGER DEFAULT nextval('s')`,
		`CREATE DOMAIN d INTEGER DEFAULT $1`,
		`DROP DOMAIN d CASCADE`,
		`ALTER DOMAIN d DROP NOT NULL`,
		`CREATE DOMAIN d INTEGER ` + strings.Repeat("CHECK (VALUE > 0) ", 17),
	} {
		if _, err := ParseStatement(query); err == nil {
			t.Errorf("accepted: %s", query)
		}
	}
}

func TestDomainSchemaVersionGate(t *testing.T) {
	schema := Schema{Version: SchemaVersion9, ID: "table", Name: "items", Hash: "hash", NextFieldTag: 2,
		Fields: []Field{{ID: "id", Tag: 1, Name: "id", Kind: "int32", Domain: &DomainReference{ID: StableSchemaID("domain", "positive"), Name: "positive", Hash: "hash"}}}}
	if err := schema.Validate(); err != nil {
		t.Fatal(err)
	}
	schema.Version = SchemaVersion8
	if err := schema.Validate(); err == nil {
		t.Fatal("older schema version accepted a domain reference")
	}
	schema.Version = SchemaVersion9
	schema.Fields[0].Domain.ID = "bad"
	if err := schema.Validate(); err == nil {
		t.Fatal("accepted invalid domain identity")
	}
}
