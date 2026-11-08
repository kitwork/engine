package engine

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// AppDatabaseConfig is app.database(path, { … }) of application.work: the host's database port (KITWORK_KITDB.md
// câu 13.5), the PostgreSQL wire, reads and writes by GRANT, on the SQLite files of the host's .database/ (Path) and
// of every app's .database/. A positive Port opens the listener on Host; User and Password are the admin of every
// cluster; Concurrency bounds the statements running at once; TLS names the port's certificate files (câu 13.11);
// RequireChannelBinding is channelBinding: "require", a client of another machine taken with SCRAM-SHA-256-PLUS only;
// ClientAuthority is clientAuthority, the PEM file of the authorities whose certificate a client of another machine
// presents, its common name its user, as well as its password.
type AppDatabaseConfig struct {
	Path                  string
	Host                  string
	Port                  int
	User                  string
	Password              string
	Concurrency           int
	TLS                   *AppDatabaseTLS
	RequireChannelBinding bool
	ClientAuthority       string
	// Search is the full-text index of each table SEARCH reads on the port (app_database_search.go).
	Search []AppDatabaseSearch
}

// AppDatabaseTLS is tls: { certificate, key } of app.database: a PEM certificate file (its chain after it) and its
// key, as PostgreSQL's ssl_cert_file and ssl_key_file, relative to the manifest. Without it, a port listening beyond
// the loopback answers with the website's certificates (app_database_runtime.go appDatabaseTLS).
type AppDatabaseTLS struct {
	Certificate string
	Key         string
}

// AppDatabaseSearch is one table's full-text index of app.database's search: a table of a database of the host's
// .database/ (a folder), in a schema (public: kitwork.db), and the columns indexed with the weight of each.
//
//	search: { shopping: { shopping: { name: 5, brand: 2 } } }      database → table → { column: weight }
//	search: { shopping: { "billing.invoices": ["note"] } }         a schema other than public; weight 1
type AppDatabaseSearch struct {
	Database string
	Schema   string
	Table    string
	Columns  []AppDatabaseSearchColumn
}

// AppDatabaseSearchColumn is one indexed column and how much a match in it weighs (1 by default).
type AppDatabaseSearchColumn struct {
	Name  string
	Boost float64
}

// maxAppDatabaseSearchColumns is how many columns one table's index may have.
const maxAppDatabaseSearchColumns = 32

// appDatabaseRemovedOptions are the options of app.database the KitDB port had, each with why it went when the port
// moved to SQLite (30/10/2026).
var appDatabaseRemovedOptions = map[string]string{
	"alias":  "the port no longer publishes a database to the process under an alias; an app reads its own database with connection() of .shared/schema/",
	"kitsql": "the /_kitsql/v1/query endpoint went with KitDB; read over the PostgreSQL wire",
	"memory": "SQLite keeps its own page cache; the KitDB page cache it sized is gone",
	"warm":   "a SQLite database opens with the session that reads it; nothing is kept warm",
	"cache":  "the KitDB query cache is gone; the port answers each statement from the file",
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
	if len(result) > 1 {
		return nil, fmt.Errorf("app.database is declared %d times: the host has one database port, on one .database/ folder", len(result))
	}
	return result, nil
}

// parseAppDatabaseTLS reads tls: { certificate, key }, both paths.
func parseAppDatabaseTLS(value interface{}) (*AppDatabaseTLS, error) {
	source, ok := value.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("tls must be { certificate, key }: the port's certificate file and its key")
	}
	files := &AppDatabaseTLS{}
	for key, item := range source {
		text, isText := item.(string)
		switch key {
		case "certificate", "key":
			if !isText || strings.TrimSpace(text) == "" {
				return nil, fmt.Errorf("tls.%s must be the path of a file", key)
			}
			if key == "certificate" {
				files.Certificate = strings.TrimSpace(text)
			} else {
				files.Key = strings.TrimSpace(text)
			}
		default:
			return nil, fmt.Errorf("unknown option tls.%s: tls takes certificate and key", key)
		}
	}
	if files.Certificate == "" || files.Key == "" {
		return nil, fmt.Errorf("tls needs certificate and key: the port's certificate file and its key")
	}
	return files, nil
}

func parseAppDatabaseConfig(source map[string]interface{}) (AppDatabaseConfig, error) {
	config := AppDatabaseConfig{Host: "127.0.0.1", User: "kitdb"}
	known := map[string]bool{
		"path": true, "host": true, "port": true, "user": true, "password": true, "concurrency": true, "search": true,
		"tls": true, "channelBinding": true, "clientAuthority": true,
	}
	for key := range source {
		if why, removed := appDatabaseRemovedOptions[key]; removed {
			return config, fmt.Errorf("option %q is gone (30/10/2026, the port runs on SQLite, KITWORK_KITDB.md 13.5): %s", key, why)
		}
		if !known[key] {
			return config, fmt.Errorf("unknown option %q: app.database takes path, host, port, user, password, concurrency, search, tls, channelBinding, clientAuthority", key)
		}
	}
	path, ok := source["path"].(string)
	if !ok || strings.TrimSpace(path) == "" {
		return config, fmt.Errorf("path is required: the host's .database/ folder")
	}
	config.Path = path
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
	if config.Port == 0 {
		return config, fmt.Errorf("port is required: app.database is the host's database port")
	}
	if config.Password == "" {
		return config, fmt.Errorf("password is required: user and password are the admin of every cluster on the port")
	}
	if value, found := source["tls"]; found {
		files, err := parseAppDatabaseTLS(value)
		if err != nil {
			return config, err
		}
		config.TLS = files
	}
	if value, found := source["clientAuthority"]; found {
		authority, ok := value.(string)
		if !ok || strings.TrimSpace(authority) == "" {
			return config, fmt.Errorf("clientAuthority must be the path of the PEM file of the authorities that sign the clients' certificates")
		}
		config.ClientAuthority = strings.TrimSpace(authority)
	}
	if value, found := source["channelBinding"]; found {
		switch value {
		case "prefer":
		case "require":
			config.RequireChannelBinding = true
		default:
			return config, fmt.Errorf("channelBinding must be \"prefer\" (the default: SCRAM-SHA-256-PLUS offered over TLS) or " +
				"\"require\" (a client of another machine authenticates with it only)")
		}
	}
	if value, found := source["search"]; found {
		search, err := parseAppDatabaseSearch(value)
		if err != nil {
			return config, err
		}
		config.Search = search
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
	return config, nil
}

// parseAppDatabaseSearch reads search: database → table ("name" or "schema.name") → columns, as a list (weight 1) or
// an object of weights. The result is in a fixed order: databases, then tables, by name; columns as listed, or by name
// from an object.
func parseAppDatabaseSearch(source interface{}) ([]AppDatabaseSearch, error) {
	databases, ok := source.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("search must be an object: { database: { table: [columns] } }")
	}
	var result []AppDatabaseSearch
	for _, database := range sortedConfigKeys(databases) {
		if !plainConfigName(database) {
			return nil, fmt.Errorf("search: %q is not a database of the host's .database/ folder (a folder name)", database)
		}
		tables, ok := databases[database].(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("search.%s must be an object: { table: [columns] }", database)
		}
		for _, key := range sortedConfigKeys(tables) {
			declaration := AppDatabaseSearch{Database: database, Schema: "public", Table: key}
			if schema, table, qualified := strings.Cut(key, "."); qualified {
				declaration.Schema, declaration.Table = schema, table
			}
			if !plainConfigName(declaration.Schema) || !plainConfigName(declaration.Table) {
				return nil, fmt.Errorf("search.%s: %q is not a table name (table or schema.table)", database, key)
			}
			where := "search." + database + "." + key
			switch columns := tables[key].(type) {
			case []interface{}:
				for _, item := range columns {
					name, ok := item.(string)
					if !ok || strings.TrimSpace(name) == "" {
						return nil, fmt.Errorf("%s: each column is a name", where)
					}
					declaration.Columns = append(declaration.Columns, AppDatabaseSearchColumn{Name: strings.TrimSpace(name), Boost: 1})
				}
			case map[string]interface{}:
				for _, name := range sortedConfigKeys(columns) {
					weight, ok := columns[name].(float64)
					if integer, isInteger := columns[name].(int); isInteger {
						weight, ok = float64(integer), true
					}
					if !ok || weight <= 0 || weight > 1000 || strings.TrimSpace(name) == "" {
						return nil, fmt.Errorf("%s.%s: the weight of a column is a number from more than 0 to 1000", where, name)
					}
					declaration.Columns = append(declaration.Columns, AppDatabaseSearchColumn{Name: strings.TrimSpace(name), Boost: weight})
				}
			default:
				return nil, fmt.Errorf("%s: write the columns as [\"name\", \"brand\"] or { name: 5, brand: 2 }", where)
			}
			if len(declaration.Columns) == 0 || len(declaration.Columns) > maxAppDatabaseSearchColumns {
				return nil, fmt.Errorf("%s: an index has from 1 to %d columns", where, maxAppDatabaseSearchColumns)
			}
			seen := map[string]bool{}
			for _, column := range declaration.Columns {
				if seen[strings.ToLower(column.Name)] {
					return nil, fmt.Errorf("%s: column %q is named twice", where, column.Name)
				}
				seen[strings.ToLower(column.Name)] = true
			}
			result = append(result, declaration)
		}
	}
	return result, nil
}

func sortedConfigKeys(object map[string]interface{}) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// plainConfigName is a folder, schema or table name: letters, digits, '_', '-' and '.', not "." or "..".
func plainConfigName(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 128 {
		return false
	}
	for _, character := range name {
		switch {
		case character >= 'a' && character <= 'z', character >= 'A' && character <= 'Z', character >= '0' && character <= '9',
			character == '_', character == '-', character == '.':
		default:
			return false
		}
	}
	return true
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
