package engine

import "testing"

func TestParseConfigPreservesDatabaseFieldNames(t *testing.T) {
	cfg, err := ParseConfig(map[string]interface{}{
		"databases": []interface{}{
			map[string]interface{}{
				"alias":     "system",
				"type":      "postgres",
				"host":      "db.internal",
				"port":      float64(5432),
				"sslmode":   "verify-full",
				"max_open":  float64(20),
				"max_idle":  float64(5),
				"lifetime":  float64(30),
				"max_limit": float64(120),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Databases) != 1 {
		t.Fatalf("database count = %d, want 1", len(cfg.Databases))
	}
	got := cfg.Databases[0]
	if got.Alias != "system" || got.Type != "postgres" || got.Host != "db.internal" {
		t.Fatalf("database identity fields = %+v", got)
	}
	if got.Port != 5432 || got.SSLMode != "verify-full" {
		t.Fatalf("database transport fields = %+v", got)
	}
	if got.MaxOpen != 20 || got.MaxIdle != 5 || got.Lifetime != 30 || got.MaxLimit != 120 {
		t.Fatalf("database pool fields = %+v", got)
	}
}

func TestParseConfigPreservesNestedDatabaseAliases(t *testing.T) {
	cfg, err := ParseConfig(map[string]interface{}{
		"databases": map[string]interface{}{
			"system": map[string]interface{}{
				"type":    "postgres",
				"host":    "system.internal",
				"sslmode": "require",
			},
			"analytics": map[string]interface{}{
				"type": "sqlite",
				"name": "analytics.db",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Databases) != 2 {
		t.Fatalf("database count = %d, want 2", len(cfg.Databases))
	}
	byAlias := make(map[string]string, len(cfg.Databases))
	for _, db := range cfg.Databases {
		byAlias[db.Alias] = db.Type
	}
	if byAlias["system"] != "postgres" || byAlias["analytics"] != "sqlite" {
		t.Fatalf("database aliases = %#v", byAlias)
	}
}
