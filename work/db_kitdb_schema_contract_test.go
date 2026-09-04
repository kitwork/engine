package work

import (
	"strings"
	"testing"
	"time"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
	"github.com/kitwork/engine/value"
)

func TestKitworkStructLowersIntoIndependentKitDBSchemaContract(t *testing.T) {
	status := &ColumnSpec{kind: "enum", enumVals: []string{"active", "disabled"}, seq: 2}
	status.Analytics()
	definition := bindStructDef("products", &StructDef{Version: structIRVersion}, map[string]*ColumnSpec{
		"id":     {kind: "kitid", primary: true, seq: 1},
		"status": status,
	})
	catalog, _, _, err := encodeKitDBMigrationAt(definition, nil, "", "", time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	contract, err := kitdbsql.DecodeSchema(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if contract.ID != definition.ID || contract.Hash != definition.Hash || contract.Name != "products" {
		t.Fatalf("contract identity = %#v; definition = %#v", contract, definition)
	}
	if len(contract.Fields) != 2 || contract.Fields[0].Kind != "kitid" || contract.Fields[1].Kind != "enum" || !contract.Fields[1].Analytics {
		t.Fatalf("contract fields = %#v", contract.Fields)
	}
}

func TestKitworkStructPartitionLowersIntoIndependentKitDBSchemaContract(t *testing.T) {
	partitioned := &ColumnSpec{kind: "integer", seq: 2}
	partitioned.Partition(value.New("hash"))
	definition := bindStructDef("clicks", &StructDef{Version: structIRVersion}, map[string]*ColumnSpec{
		"id":      {kind: "kitid", primary: true, seq: 1},
		"user_id": partitioned,
	})
	if definition.constraintErr != "" {
		t.Fatal(definition.constraintErr)
	}
	if definition.Version != kitdbsql.SchemaVersion8 || definition.Partition == nil ||
		definition.Partition.Strategy != "hash" ||
		definition.Partition.Buckets != kitdbsql.PartitionHashBuckets ||
		definition.Partition.Field != definition.Fields[1].Tag {
		t.Fatalf("partitioned definition = %#v", definition)
	}
	catalog, _, _, err := encodeKitDBMigrationAt(definition, nil, "", "", time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	contract, err := kitdbsql.DecodeSchema(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if contract.Partition == nil || *contract.Partition != *definition.Partition {
		t.Fatalf("contract partition = %#v, want %#v", contract.Partition, definition.Partition)
	}
}

func TestKitworkStructPartitionRejectsAmbiguousOrUnsupportedDeclarations(t *testing.T) {
	tests := []struct {
		name    string
		columns map[string]*ColumnSpec
		want    string
	}{
		{
			name: "bare strategy",
			columns: map[string]*ColumnSpec{
				"id": func() *ColumnSpec {
					column := &ColumnSpec{kind: "integer", primary: true, seq: 1}
					column.Partition()
					return column
				}(),
			},
			want: "requires one explicit strategy",
		},
		{
			name: "temporal strategy",
			columns: map[string]*ColumnSpec{
				"id": func() *ColumnSpec {
					column := &ColumnSpec{kind: "integer", primary: true, seq: 1}
					column.Partition(value.New("monthly"))
					return column
				}(),
			},
			want: "temporal partitions are not available",
		},
		{
			name: "text field",
			columns: map[string]*ColumnSpec{
				"id": {kind: "kitid", primary: true, seq: 1},
				"owner": func() *ColumnSpec {
					column := &ColumnSpec{kind: "text", seq: 2}
					column.Partition(value.New("hash"))
					return column
				}(),
			},
			want: "requires an integer field",
		},
		{
			name: "multiple fields",
			columns: map[string]*ColumnSpec{
				"tenant": {kind: "integer", partition: "hash", seq: 1},
				"owner":  {kind: "integer", partition: "range", seq: 2},
			},
			want: "only one field",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateSchema(test.columns); err != nil {
				if strings.Contains(err.Error(), test.want) {
					return
				}
				t.Fatalf("validation error = %q, want %q", err, test.want)
			}
			definition := bindStructDef("events", nil, test.columns)
			if !strings.Contains(definition.constraintErr, test.want) {
				t.Fatalf("constraint error = %q, want %q", definition.constraintErr, test.want)
			}
		})
	}
}

func TestKitworkAnalyticsRejectsUnsupportedFieldType(t *testing.T) {
	payload := &ColumnSpec{kind: "json", seq: 2}
	payload.Analytics()
	err := validateSchema(map[string]*ColumnSpec{
		"id":      {kind: "kitid", primary: true, seq: 1},
		"payload": payload,
	})
	if err == nil || !strings.Contains(err.Error(), `analytics() does not support type "json"`) {
		t.Fatalf("analytics validation error = %v", err)
	}
}
