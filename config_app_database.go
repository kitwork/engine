package engine

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/kitwork/engine/kitdb/relational"
)

const maximumAppDatabaseCacheDuration = 24 * time.Hour

// AppDatabaseConfig is one KitDB file or managed root owned by the Kitwork
// process. Port zero keeps the database embedded; a positive port also exposes
// the PostgreSQL-compatible listener on Host. KitSQL exposes the same database
// through the Kitwork HTTP(S) listener when the web surface is present.
type AppDatabaseConfig struct {
	Alias         string
	Path          string
	Host          string
	Port          int
	User          string
	Password      string
	KitSQL        bool
	MemoryBytes   int64
	Concurrency   int
	Warm          bool
	WarmDatabases []string
	Cache         relational.QueryCacheOptions
}

func parseAppDatabaseConfigs(source interface{}) ([]AppDatabaseConfig, error) {
	items, ok := source.([]interface{})
	if !ok {
		return nil, fmt.Errorf("app.database: owned database declarations must be an array")
	}
	result := make([]AppDatabaseConfig, 0, len(items))
	for index, item := range items {
		object, ok := item.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("app.database declaration %d must be an object", index+1)
		}
		config, err := parseAppDatabaseConfig(object)
		if err != nil {
			return nil, fmt.Errorf("app.database declaration %d: %w", index+1, err)
		}
		result = append(result, config)
	}
	return result, nil
}

func parseAppDatabaseConfig(source map[string]interface{}) (AppDatabaseConfig, error) {
	config := AppDatabaseConfig{Host: "127.0.0.1", User: "kitdb"}
	known := map[string]bool{
		"path": true, "alias": true, "host": true, "port": true, "user": true, "password": true,
		"kitsql": true, "memory": true, "concurrency": true, "warm": true, "cache": true,
	}
	for key := range source {
		if !known[key] {
			return config, fmt.Errorf("unknown option %q", key)
		}
	}
	path, ok := source["path"].(string)
	if !ok || strings.TrimSpace(path) == "" {
		return config, fmt.Errorf("path is required")
	}
	config.Path = path
	if value, found := source["alias"]; found {
		alias, ok := value.(string)
		if !ok || strings.TrimSpace(alias) == "" || strings.ContainsAny(alias, "\x00/\\") {
			return config, fmt.Errorf("alias must be a non-empty logical name")
		}
		config.Alias = strings.TrimSpace(alias)
	}
	if value, found := source["host"]; found {
		host, ok := value.(string)
		if !ok || strings.TrimSpace(host) == "" {
			return config, fmt.Errorf("host must be a non-empty string")
		}
		config.Host = strings.TrimSpace(host)
	}
	if value, found := source["port"]; found {
		port, err := strictConfigInteger(value)
		if err != nil || port < 1 || port > 65_535 {
			return config, fmt.Errorf("port must be between 1 and 65535")
		}
		config.Port = port
	}
	if value, found := source["user"]; found {
		user, ok := value.(string)
		if !ok || strings.TrimSpace(user) == "" {
			return config, fmt.Errorf("user must be a non-empty string")
		}
		config.User = strings.TrimSpace(user)
	}
	if value, found := source["password"]; found {
		password, ok := value.(string)
		if !ok {
			return config, fmt.Errorf("password must be a string")
		}
		config.Password = password
	}
	if value, found := source["kitsql"]; found {
		enabled, ok := value.(bool)
		if !ok {
			return config, fmt.Errorf("kitsql must be a boolean")
		}
		config.KitSQL = enabled
	}
	if (config.Port != 0 || config.KitSQL) && config.Password == "" {
		return config, fmt.Errorf("password is required when port or kitsql is configured")
	}
	if value, found := source["memory"]; found {
		memory, err := parseConfigBytes(value)
		if err != nil {
			return config, fmt.Errorf("memory: %w", err)
		}
		config.MemoryBytes = memory
	}
	if value, found := source["concurrency"]; found {
		if text, ok := value.(string); ok && strings.EqualFold(strings.TrimSpace(text), "auto") {
			config.Concurrency = 0
		} else {
			concurrency, err := strictConfigInteger(value)
			if err != nil || concurrency < 1 || concurrency > 1_024 {
				return config, fmt.Errorf("concurrency must be \"auto\" or between 1 and 1024")
			}
			config.Concurrency = concurrency
		}
	}
	if value, found := source["warm"]; found {
		switch warm := value.(type) {
		case bool:
			config.Warm = warm
		case string:
			if strings.TrimSpace(warm) == "" {
				return config, fmt.Errorf("warm database name cannot be empty")
			}
			config.WarmDatabases = []string{warm}
		case []interface{}:
			for _, item := range warm {
				name, ok := item.(string)
				if !ok || strings.TrimSpace(name) == "" {
					return config, fmt.Errorf("warm must contain non-empty database names")
				}
				config.WarmDatabases = append(config.WarmDatabases, name)
			}
		default:
			return config, fmt.Errorf("warm must be a boolean, database name or array of names")
		}
	}
	if value, found := source["cache"]; found {
		cache, err := parseAppDatabaseCache(value)
		if err != nil {
			return config, err
		}
		config.Cache = cache
	}
	return config, nil
}

func parseAppDatabaseCache(source interface{}) (relational.QueryCacheOptions, error) {
	var result relational.QueryCacheOptions
	switch value := source.(type) {
	case bool:
		if value {
			return result, fmt.Errorf("cache: true is ambiguous; provide a duration such as \"1m\"")
		}
		return result, nil
	case string:
		duration, err := parseCacheDuration(value)
		if err != nil {
			return result, fmt.Errorf("cache: %w", err)
		}
		result.Select, result.Search, result.Analytics = duration, duration, duration
		return result, nil
	case map[string]interface{}:
		for key := range value {
			switch key {
			case "select", "search", "analytics", "memory", "entries", "result":
			default:
				return result, fmt.Errorf("cache: unknown option %q", key)
			}
		}
		for key, target := range map[string]*time.Duration{
			"select": &result.Select, "search": &result.Search, "analytics": &result.Analytics,
		} {
			if item, found := value[key]; found {
				text, ok := item.(string)
				if !ok {
					return result, fmt.Errorf("cache.%s must be a duration string", key)
				}
				duration, err := parseCacheDuration(text)
				if err != nil {
					return result, fmt.Errorf("cache.%s: %w", key, err)
				}
				*target = duration
			}
		}
		if item, found := value["memory"]; found {
			memory, err := parseConfigBytes(item)
			if err != nil || memory == 0 {
				return result, fmt.Errorf("cache.memory must be a positive byte size")
			}
			result.MaximumBytes = memory
		}
		if item, found := value["entries"]; found {
			entries, err := strictConfigInteger(item)
			if err != nil || entries < 1 {
				return result, fmt.Errorf("cache.entries must be a positive integer")
			}
			result.MaximumEntries = entries
		}
		if item, found := value["result"]; found {
			maximum, err := parseConfigBytes(item)
			if err != nil || maximum == 0 {
				return result, fmt.Errorf("cache.result must be a positive byte size")
			}
			result.MaximumResult = maximum
		}
		if result.Select == 0 && result.Search == 0 && result.Analytics == 0 &&
			(result.MaximumBytes != 0 || result.MaximumEntries != 0 || result.MaximumResult != 0) {
			return result, fmt.Errorf("cache requires at least one of select, search or analytics")
		}
		return result, nil
	default:
		return result, fmt.Errorf("cache must be false, a duration string or an object")
	}
}

func parseCacheDuration(source string) (time.Duration, error) {
	duration, err := time.ParseDuration(strings.TrimSpace(source))
	if err != nil || duration <= 0 || duration > maximumAppDatabaseCacheDuration {
		return 0, fmt.Errorf("must be between 1ns and %s", maximumAppDatabaseCacheDuration)
	}
	return duration, nil
}

func parseConfigBytes(source interface{}) (int64, error) {
	if text, ok := source.(string); ok {
		text = strings.ToLower(strings.TrimSpace(text))
		if text == "auto" {
			return 0, nil
		}
		units := []struct {
			suffix string
			scale  int64
		}{{"gib", 1 << 30}, {"gb", 1 << 30}, {"mib", 1 << 20}, {"mb", 1 << 20}, {"kib", 1 << 10}, {"kb", 1 << 10}, {"b", 1}}
		for _, unit := range units {
			if strings.HasSuffix(text, unit.suffix) {
				number := strings.TrimSpace(strings.TrimSuffix(text, unit.suffix))
				value, err := strconv.ParseInt(number, 10, 64)
				if err != nil || value < 1 || value > math.MaxInt64/unit.scale {
					return 0, fmt.Errorf("invalid byte size %q", source)
				}
				return value * unit.scale, nil
			}
		}
		return 0, fmt.Errorf("invalid byte size %q; use b, kb, mb or gb", source)
	}
	value, err := strictConfigInteger64(source)
	if err != nil || value < 1 {
		return 0, fmt.Errorf("must be \"auto\" or a positive byte size")
	}
	return value, nil
}

func strictConfigInteger(source interface{}) (int, error) {
	value, err := strictConfigInteger64(source)
	if err != nil || value > math.MaxInt || value < math.MinInt {
		return 0, fmt.Errorf("not an integer")
	}
	return int(value), nil
}

func strictConfigInteger64(source interface{}) (int64, error) {
	switch value := source.(type) {
	case int:
		return int64(value), nil
	case int64:
		return value, nil
	case float64:
		if math.Trunc(value) != value || value > math.MaxInt64 || value < math.MinInt64 {
			return 0, fmt.Errorf("not an integer")
		}
		return int64(value), nil
	default:
		return 0, fmt.Errorf("not an integer")
	}
}
