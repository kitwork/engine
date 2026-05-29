package engine

import (
	"encoding/json"
	"fmt"

	"github.com/kitwork/engine/database"
)

type Config struct {
	Port       int               `json:"port" yaml:"port"`
	Root       string            `json:"root" yaml:"root"`
	Databases  []database.Config `json:"database" yaml:"database"`
	Domains    []string          `json:"domains" yaml:"domains"`
	MaxEnergy  uint64            `json:"max_energy" yaml:"max_energy"`
	HotReload  bool              `json:"hot_reload" yaml:"hot_reload"`
	Hostname   string            `json:"hostname" yaml:"hostname"`
	AllowLocal bool              `json:"allow_local" yaml:"allow_local"`
}

func ParseConfig(raw map[string]interface{}) (*Config, error) {
	cfg := &Config{
		Port:      8080,
		Root:      ".",
		MaxEnergy: 10000000,
	}

	if val, ok := raw["port"]; ok {
		cfg.Port = coerceInt(val, 8080)
	}
	if val, ok := raw["root"]; ok {
		if s, ok := val.(string); ok {
			cfg.Root = s
		}
	}
	if val, ok := raw["max_energy"]; ok {
		cfg.MaxEnergy = coerceUint64(val, 10000000)
	}
	if val, ok := raw["hot_reload"]; ok {
		if b, ok := val.(bool); ok {
			cfg.HotReload = b
		}
	}
	if val, ok := raw["hostname"]; ok {
		if s, ok := val.(string); ok {
			cfg.Hostname = s
		}
	}
	if val, ok := raw["allow_local"]; ok {
		if b, ok := val.(bool); ok {
			cfg.AllowLocal = b
		}
	}

	// Dynamic domain/domains mapping
	var rawDomains interface{}
	if val, ok := raw["domains"]; ok {
		rawDomains = val
	} else if val, ok := raw["domain"]; ok {
		rawDomains = val
	}
	if rawDomains != nil {
		cfg.Domains = coerceStringSlice(rawDomains)
	}

	// Dynamic database/databases mapping
	var rawDB interface{}
	if val, ok := raw["database"]; ok {
		rawDB = val
	} else if val, ok := raw["databases"]; ok {
		rawDB = val
	}
	if rawDB != nil {
		dbs, err := parseDatabases(rawDB)
		if err != nil {
			return nil, err
		}
		cfg.Databases = dbs
	}

	return cfg, nil
}

func coerceInt(val interface{}, def int) int {
	switch v := val.(type) {
	case int:
		return v
	case float64:
		return int(v)
	case int64:
		return int(v)
	}
	return def
}

func coerceUint64(val interface{}, def uint64) uint64 {
	switch v := val.(type) {
	case uint64:
		return v
	case int:
		return uint64(v)
	case float64:
		return uint64(v)
	case int64:
		return uint64(v)
	}
	return def
}

func coerceStringSlice(val interface{}) []string {
	if val == nil {
		return nil
	}
	if s, ok := val.(string); ok {
		return []string{s}
	}
	switch v := val.(type) {
	case []interface{}:
		res := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				res = append(res, s)
			}
		}
		return res
	case []string:
		return v
	}
	return nil
}

func parseDatabases(dbVal interface{}) ([]database.Config, error) {
	if dbVal == nil {
		return nil, nil
	}

	jsonData, err := json.Marshal(dbVal)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal database config: %w", err)
	}

	// 1. Try slice/list format: []database.Config
	var sliceConfigs []database.Config
	if err := json.Unmarshal(jsonData, &sliceConfigs); err == nil && len(sliceConfigs) > 0 {
		return sliceConfigs, nil
	}

	// 2. Try nested map format: map[string]database.Config
	var mapConfigs map[string]database.Config
	if err := json.Unmarshal(jsonData, &mapConfigs); err == nil && isMultipleConfig(mapConfigs) {
		var result []database.Config
		for alias, db := range mapConfigs {
			db.Alias = alias
			result = append(result, db)
		}
		return result, nil
	}

	// 3. Try flat map format: database.Config
	var singleDB database.Config
	if err := json.Unmarshal(jsonData, &singleDB); err == nil && singleDB.Type != "" {
		if singleDB.Alias == "" {
			singleDB.Alias = "default"
		}
		return []database.Config{singleDB}, nil
	}

	return nil, fmt.Errorf("unsupported database configuration structure")
}

func isMultipleConfig(m map[string]database.Config) bool {
	if _, ok := m["type"]; ok {
		return false
	}
	if _, ok := m["host"]; ok {
		return false
	}
	return len(m) > 0
}
