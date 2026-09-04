package postgres

import (
	"strings"
	"testing"
)

func TestBuildTargetPlanUsesNativeCompositeKey(t *testing.T) {
	source := SchemaReport{
		Format: SchemaReportFormat, Database: "source", Schema: "public", Table: "shopping",
		PrimaryKey: []string{"merchant", "id"},
		Columns: []ColumnReport{
			{Name: "merchant", TargetKind: "text", Mapping: "exact", Nullable: false},
			{Name: "id", TargetKind: "integer", Mapping: "conditional", Nullable: false},
			{Name: "search_vector", InternalType: "tsvector", Mapping: "derived"},
		},
		Indexes: []IndexReport{
			{Name: "shopping_pkey", Method: "btree", Columns: []string{"merchant", "id"}, Primary: true, Unique: true},
			{Name: "search_idx", Method: "gin", Columns: []string{"search_vector"}, FullText: true},
		},
		FullText: FullTextReport{Detected: true, RequiresRebuild: true},
	}
	plan, err := BuildTargetPlan(source)
	if err != nil {
		t.Fatalf("build target plan: %v", err)
	}
	if plan.PrimaryStrategy != "native-composite" || plan.SyntheticPrimary != "" ||
		len(plan.DerivedColumns) != 1 || len(plan.DeferredIndexes) != 0 {
		t.Fatalf("unexpected target plan: %+v", plan)
	}
	if !strings.Contains(plan.CreateTableSQL, `PRIMARY KEY ("merchant", "id")`) ||
		strings.Contains(plan.CreateTableSQL, `"_key"`) || strings.Contains(plan.CreateTableSQL, "search_vector") {
		t.Fatalf("unexpected CREATE TABLE: %s", plan.CreateTableSQL)
	}
}

func TestURLWithDatabasePreservesTargetSecret(t *testing.T) {
	overridden, err := URLWithDatabase("postgresql://kitdb:p%40ss@127.0.0.1:5440/kitdb?sslmode=disable", "shadow")
	if err != nil {
		t.Fatalf("override target URL: %v", err)
	}
	if !strings.Contains(overridden, "p%40ss") || !strings.Contains(overridden, "/shadow?") {
		t.Fatalf("unexpected target URL override")
	}
}

func TestPostgresNumericModifierPreservesTargetPrecisionScale(t *testing.T) {
	precision, scale, constrained, err := postgresNumericModifier("numeric(18, 4)")
	if err != nil || !constrained || precision != 18 || scale != 4 {
		t.Fatalf("numeric modifier = (%d,%d,%t), %v", precision, scale, constrained, err)
	}
	if _, _, constrained, err := postgresNumericModifier("numeric"); err != nil || constrained {
		t.Fatalf("unconstrained numeric = constrained:%t error:%v", constrained, err)
	}
	for _, source := range []string{"numeric(0,0)", "numeric(10,11)", "numeric(1001,2)", "numeric(10,-1)"} {
		if _, _, _, err := postgresNumericModifier(source); err == nil {
			t.Fatalf("accepted unsupported modifier %q", source)
		}
	}
	source := SchemaReport{
		Format: SchemaReportFormat, Database: "source", Schema: "public", Table: "ledger",
		PrimaryKey: []string{"id"},
		Columns: []ColumnReport{
			{Name: "id", TargetKind: "bigint", Mapping: "exact"},
			{Name: "amount", TargetKind: "decimal(18,4)", Mapping: "exact"},
		},
	}
	plan, err := BuildTargetPlan(source)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.CreateTableSQL, `"amount" DECIMAL(18,4)`) {
		t.Fatalf("target DDL = %s", plan.CreateTableSQL)
	}
}

func TestPostgresTemporalModifierAndTargetKindsRemainDistinct(t *testing.T) {
	for _, test := range []struct {
		dataType string
		internal string
		want     int
	}{
		{"timestamp(3) without time zone", "timestamp", 3},
		{"timestamp(6) with time zone", "timestamptz", 6},
		{"time(0) without time zone", "time", 0},
	} {
		precision, constrained, err := postgresTemporalModifier(test.dataType, test.internal)
		if err != nil || !constrained || precision != test.want {
			t.Fatalf("temporal modifier %q = %d, %t, %v", test.dataType, precision, constrained, err)
		}
	}
	if _, constrained, err := postgresTemporalModifier("timestamp without time zone", "timestamp"); err != nil || constrained {
		t.Fatalf("unconstrained timestamp = constrained:%t error:%v", constrained, err)
	}
	for _, source := range []string{"timestamp(7) without time zone", "time(-1) without time zone"} {
		if _, _, err := postgresTemporalModifier(source, strings.Split(source, "(")[0]); err == nil {
			t.Fatalf("unsupported temporal modifier accepted: %q", source)
		}
	}

	p3, p6 := 3, 6
	source := SchemaReport{
		Format: SchemaReportFormat, Database: "source", Schema: "public", Table: "events",
		PrimaryKey: []string{"id"},
		Columns: []ColumnReport{
			{Name: "id", TargetKind: "bigint", Mapping: "exact"},
			{Name: "local_at", InternalType: "timestamp", TimePrecision: &p3},
			{Name: "occurred_at", InternalType: "timestamptz", TimePrecision: &p6},
			{Name: "elapsed", InternalType: "interval"},
		},
	}
	for index := 1; index < len(source.Columns); index++ {
		column := &source.Columns[index]
		column.TargetKind, column.Mapping, column.MappingReason = mapColumn(*column)
	}
	plan, err := BuildTargetPlan(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		`"local_at" TIMESTAMP(3)`, `"occurred_at" TIMESTAMPTZ(6)`, `"elapsed" INTERVAL`,
	} {
		if !strings.Contains(plan.CreateTableSQL, fragment) {
			t.Fatalf("target DDL %q misses %q", plan.CreateTableSQL, fragment)
		}
	}
}

func TestPostgresCharacterModifierAndTargetKindsRemainDistinct(t *testing.T) {
	for _, test := range []struct {
		dataType string
		internal string
		want     int
	}{
		{"character varying(64)", "varchar", 64},
		{"varchar(4)", "varchar", 4},
		{"character(8)", "bpchar", 8},
		{"bpchar(1)", "bpchar", 1},
	} {
		length, constrained, err := postgresTextModifier(test.dataType, test.internal)
		if err != nil || !constrained || length != test.want {
			t.Fatalf("character modifier %q = %d, %t, %v", test.dataType, length, constrained, err)
		}
	}
	if _, constrained, err := postgresTextModifier("character varying", "varchar"); err != nil || constrained {
		t.Fatalf("unconstrained varchar = constrained:%t error:%v", constrained, err)
	}
	for _, test := range []struct {
		dataType string
		internal string
	}{
		{"varchar(0)", "varchar"},
		{"character varying(10485761)", "varchar"},
		{"varchar(4) trailing", "varchar"},
		{"character(2,3)", "bpchar"},
		{"text(4)", "varchar"},
		{"text", "varchar"},
	} {
		if _, _, err := postgresTextModifier(test.dataType, test.internal); err == nil {
			t.Fatalf("unsupported character modifier accepted: %q", test.dataType)
		}
	}

	length4, length8 := 4, 8
	source := SchemaReport{
		Format: SchemaReportFormat, Database: "source", Schema: "public", Table: "labels",
		PrimaryKey: []string{"id"},
		Columns: []ColumnReport{
			{Name: "id", TargetKind: "bigint", Mapping: "exact"},
			{Name: "slug", InternalType: "varchar", TextLength: &length4},
			{Name: "code", InternalType: "bpchar", TextLength: &length8},
			{Name: "legacy", InternalType: "bpchar"},
		},
	}
	for index := 1; index < len(source.Columns); index++ {
		column := &source.Columns[index]
		column.TargetKind, column.Mapping, column.MappingReason = mapColumn(*column)
	}
	plan, err := BuildTargetPlan(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{`"slug" VARCHAR(4)`, `"code" CHAR(8)`, `"legacy" TEXT`} {
		if !strings.Contains(plan.CreateTableSQL, fragment) {
			t.Fatalf("target DDL %q misses %q", plan.CreateTableSQL, fragment)
		}
	}
	if source.Columns[3].Mapping != "semantic" {
		t.Fatalf("unconstrained bpchar mapping = %+v", source.Columns[3])
	}
}

func TestPostgresUUIDTargetUsesExactStandaloneType(t *testing.T) {
	column := ColumnReport{Name: "external_id", InternalType: "uuid", DataType: "uuid"}
	column.TargetKind, column.Mapping, column.MappingReason = mapColumn(column)
	if column.TargetKind != "uuid" || column.Mapping != "exact" {
		t.Fatalf("UUID mapping = %+v", column)
	}
	plan, err := BuildTargetPlan(SchemaReport{
		Format: SchemaReportFormat, Database: "source", Schema: "public", Table: "objects",
		PrimaryKey: []string{"external_id"}, Columns: []ColumnReport{column},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.CreateTableSQL, `"external_id" UUID PRIMARY KEY`) {
		t.Fatalf("UUID target DDL = %s", plan.CreateTableSQL)
	}
}
