package work

import (
	"encoding/json"
	"testing"
)

func TestKitDBV1RelationalCompatibilityProfile(t *testing.T) {
	profile := CurrentKitDBRelationalCompatibility()
	if profile.SchemaIRRead != (KitDBCompatibilityVersionRange{Minimum: 1, Maximum: structIRMaximumVersion}) ||
		profile.SchemaIRWrite != structIRMaximumVersion || !profile.RowReadLegacyJSON || profile.RowWrite != 2 ||
		profile.NodeCatalogRead != (KitDBCompatibilityVersionRange{Minimum: 1, Maximum: 2}) ||
		profile.NodeCatalogWrite != 2 || profile.Statistics != 1 ||
		profile.ImportProgress != 1 ||
		profile.IndexBuildRead != (KitDBCompatibilityVersionRange{Minimum: 1, Maximum: 2}) ||
		profile.IndexBuildWrite != 2 || profile.IndexGeneration != 1 ||
		profile.RowMigration != 1 || profile.RowGeneration != 1 ||
		profile.SQLProfile != "sql-light/v1" || profile.PostgreSQLWireProtocol != 196608 {
		t.Fatalf("relational compatibility = %#v", profile)
	}
	encoded, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	var decoded KitDBRelationalCompatibility
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != profile {
		t.Fatalf("JSON round trip = %#v, want %#v", decoded, profile)
	}
}
