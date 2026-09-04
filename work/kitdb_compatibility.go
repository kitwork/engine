package work

// KitDBRelationalCompatibility records the durable relational encodings owned
// by Kitwork above the opaque KitDB key/value kernel. It is included in release
// evidence so an adapter change cannot silently redefine a KitDB 1.x file.
type KitDBRelationalCompatibility struct {
	SchemaIRRead           KitDBCompatibilityVersionRange `json:"schema_ir_read"`
	SchemaIRWrite          uint16                         `json:"schema_ir_write"`
	RowReadLegacyJSON      bool                           `json:"row_read_legacy_json"`
	RowWrite               uint16                         `json:"row_write"`
	NodeCatalogRead        KitDBCompatibilityVersionRange `json:"node_catalog_read"`
	NodeCatalogWrite       uint16                         `json:"node_catalog_write"`
	Statistics             uint16                         `json:"statistics"`
	ImportProgress         uint16                         `json:"import_progress"`
	IndexBuildRead         KitDBCompatibilityVersionRange `json:"index_build_read"`
	IndexBuildWrite        uint16                         `json:"index_build_write"`
	IndexGeneration        uint16                         `json:"index_generation"`
	RowMigration           uint16                         `json:"row_migration"`
	RowGeneration          uint16                         `json:"row_generation"`
	SQLProfile             string                         `json:"sql_profile"`
	PostgreSQLWireProtocol uint32                         `json:"postgresql_wire_protocol"`
}

// KitDBCompatibilityVersionRange is an inclusive readable encoding range.
type KitDBCompatibilityVersionRange struct {
	Minimum uint16 `json:"minimum"`
	Maximum uint16 `json:"maximum"`
}

// CurrentKitDBRelationalCompatibility returns the relational compatibility
// profile compiled into the Kitwork adapter.
func CurrentKitDBRelationalCompatibility() KitDBRelationalCompatibility {
	return KitDBRelationalCompatibility{
		SchemaIRRead:           KitDBCompatibilityVersionRange{Minimum: 1, Maximum: structIRMaximumVersion},
		SchemaIRWrite:          structIRMaximumVersion,
		RowReadLegacyJSON:      true,
		RowWrite:               uint16(kitDBBinaryRowVersion),
		NodeCatalogRead:        KitDBCompatibilityVersionRange{Minimum: kitDBNodeCatalogVersionV1, Maximum: kitDBNodeCatalogVersion},
		NodeCatalogWrite:       kitDBNodeCatalogVersion,
		Statistics:             uint16(kitDBStatisticsVersion),
		ImportProgress:         uint16(kitDBImportStateVersion),
		IndexBuildRead:         KitDBCompatibilityVersionRange{Minimum: uint16(kitDBIndexBuildLegacyVersion), Maximum: uint16(kitDBIndexBuildVersion)},
		IndexBuildWrite:        uint16(kitDBIndexBuildVersion),
		IndexGeneration:        uint16(kitDBIndexGenerationMetadataVersion),
		RowMigration:           uint16(kitDBRowMigrationVersion),
		RowGeneration:          uint16(kitDBRowGenerationMetadataVersion),
		SQLProfile:             "sql-light/v1",
		PostgreSQLWireProtocol: 196608,
	}
}
