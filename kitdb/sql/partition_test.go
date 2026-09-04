package sql

import "testing"

func TestParseCreateTablePartitionPolicies(t *testing.T) {
	tests := []struct {
		sql      string
		strategy string
		field    string
	}{
		{
			sql:      `CREATE TABLE clicks (id BIGINT PRIMARY KEY, user_id INTEGER) PARTITION BY HASH (user_id)`,
			strategy: "hash",
			field:    "user_id",
		},
		{
			sql:      `CREATE TABLE events (id BIGINT PRIMARY KEY, sequence_id BIGINT) PARTITION BY RANGE (sequence_id)`,
			strategy: "range",
			field:    "sequence_id",
		},
	}
	for _, test := range tests {
		statement, err := ParseStatement(test.sql)
		if err != nil {
			t.Fatal(err)
		}
		if statement.CreateTable == nil || statement.CreateTable.Partition == nil ||
			statement.CreateTable.Partition.Strategy != test.strategy ||
			statement.CreateTable.Partition.Field != test.field {
			t.Fatalf("partition = %#v", statement.CreateTable)
		}
	}
}

func TestParseCreateTablePartitionRejectsInvalidPolicies(t *testing.T) {
	queries := []string{
		`CREATE TABLE events (id BIGINT PRIMARY KEY) PARTITION BY HASH`,
		`CREATE TABLE events (id BIGINT PRIMARY KEY) PARTITION BY LIST (id)`,
		`CREATE TABLE events (id BIGINT PRIMARY KEY) PARTITION BY HASH (id, id)`,
		`CREATE TABLE events (id BIGINT PRIMARY KEY) PARTITION BY HASH (missing)`,
		`CREATE TABLE events (id BIGINT PRIMARY KEY, label TEXT) PARTITION BY RANGE (label)`,
	}
	for _, query := range queries {
		if _, err := ParseStatement(query); err == nil {
			t.Fatalf("ParseStatement(%q) unexpectedly succeeded", query)
		}
	}
}

func TestParseAlterTablePartitionPolicies(t *testing.T) {
	for _, test := range []struct {
		sql      string
		action   AlterTableAction
		strategy string
		field    string
	}{
		{
			sql:      `ALTER TABLE clicks SET PARTITION BY HASH (user_id)`,
			action:   AlterTableSetPartitioning,
			strategy: "hash",
			field:    "user_id",
		},
		{
			sql:      `ALTER TABLE events SET PARTITION BY RANGE (created_at)`,
			action:   AlterTableSetPartitioning,
			strategy: "range",
			field:    "created_at",
		},
		{
			sql:    `ALTER TABLE events DROP PARTITIONING`,
			action: AlterTableDropPartitioning,
		},
	} {
		statement, err := ParseStatement(test.sql)
		if err != nil {
			t.Fatalf("ParseStatement(%q): %v", test.sql, err)
		}
		if statement.AlterTable == nil || statement.AlterTable.Action != test.action {
			t.Fatalf("ALTER plan = %#v", statement.AlterTable)
		}
		if test.strategy == "" {
			if statement.AlterTable.Partition != nil {
				t.Fatalf("DROP partition = %#v", statement.AlterTable.Partition)
			}
			continue
		}
		partition := statement.AlterTable.Partition
		if partition == nil || partition.Strategy != test.strategy || partition.Field != test.field {
			t.Fatalf("partition = %#v", partition)
		}
	}
}

func TestParseAlterTablePartitionRejectsInvalidPolicies(t *testing.T) {
	queries := []string{
		`ALTER TABLE events SET PARTITION HASH (id)`,
		`ALTER TABLE events SET PARTITION BY LIST (id)`,
		`ALTER TABLE events SET PARTITION BY HASH (id, tenant_id)`,
		`ALTER TABLE events DROP PARTITION`,
	}
	for _, query := range queries {
		if _, err := ParseStatement(query); err == nil {
			t.Fatalf("ParseStatement(%q) unexpectedly succeeded", query)
		}
	}
}

func TestIntegerPartitionBucketIsStableAndBounded(t *testing.T) {
	want := []uint32{32, 47, 1, 14, 21, 31}
	values := []int64{-1, 0, 1, 2, 42, 1<<53 - 1}
	for index, value := range values {
		got, err := IntegerPartitionBucket(value, PartitionHashBuckets)
		if err != nil {
			t.Fatal(err)
		}
		if got != want[index] {
			t.Fatalf("bucket(%d) = %d, want %d", value, got, want[index])
		}
	}
	if _, err := IntegerPartitionBucket(1, 0); err == nil {
		t.Fatal("zero hash buckets unexpectedly accepted")
	}
}

func TestSchemaPartitionValidationFailsClosed(t *testing.T) {
	base := Schema{
		Version: SchemaVersion8, ID: "events", Name: "events", NextFieldTag: 3,
		Fields: []Field{
			{ID: "event-id", Tag: 1, Name: "id", Position: 0, Kind: "bigint", Primary: true, NotNull: true, Unique: true},
			{ID: "event-owner", Tag: 2, Name: "owner", Position: 1, Kind: "integer"},
		},
		Partition: &Partition{Version: PartitionVersion1, Field: 2, Strategy: "hash", Buckets: PartitionHashBuckets},
	}
	valid := base
	if err := valid.RefreshHash(); err != nil {
		t.Fatal(err)
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*Schema)
	}{
		{"legacy schema", func(schema *Schema) { schema.Version = SchemaVersion7 }},
		{"missing field", func(schema *Schema) { schema.Partition.Field = 9 }},
		{"wrong family", func(schema *Schema) { schema.Partition.Field = 1; schema.Fields[0].Kind = "text" }},
		{"wrong buckets", func(schema *Schema) { schema.Partition.Buckets = 32 }},
		{"unknown strategy", func(schema *Schema) { schema.Partition.Strategy = "list" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			schema := base
			schema.Fields = append([]Field(nil), base.Fields...)
			partition := *base.Partition
			schema.Partition = &partition
			test.change(&schema)
			if err := schema.RefreshHash(); err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(); err == nil {
				t.Fatalf("invalid partition schema unexpectedly passed: %#v", schema)
			}
		})
	}
}
