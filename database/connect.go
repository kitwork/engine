package database

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	Alias    string `json:"alias" yaml:"alias"` // system, default, analytics, ...
	Type     string `json:"type" yaml:"type"`   // postgres, mysql, sqlite
	User     string `json:"user" yaml:"user"`
	Password string `json:"password" yaml:"password"`
	Name     string `json:"name" yaml:"name"`
	Host     string `json:"host" yaml:"host"`
	Port     int    `json:"port" yaml:"port"`
	SSLMode  string `json:"sslmode" yaml:"sslmode"`
	Timezone string `json:"timezone" yaml:"timezone"`
	Timeout  int    `json:"timeout" yaml:"timeout"`
	MaxOpen  int    `json:"max_open" yaml:"max_open"`
	MaxIdle  int    `json:"max_idle" yaml:"max_idle"`
	Lifetime int    `json:"lifetime" yaml:"lifetime"`
	MaxLimit int    `json:"max_limit" yaml:"max_limit"`
}

func (d *Config) Connect() (*sql.DB, error) {
	dsn, err := d.BuildDSN()
	if err != nil {
		return nil, err
	}

	// The registered driver name is "sqlite" (modernc.org/sqlite) — accept the common "sqlite3"
	// spelling in configs without requiring a second driver.
	driver := strings.ToLower(d.Type)
	if driver == "sqlite3" {
		driver = "sqlite"
	}
	// The Turso backend (the Rust rewrite of SQLite, reached over turso.tech/database/tursogo) is
	// GATED behind the `turso` build tag so the default build stays a pure-Go static binary with no
	// new dependency. When it is not compiled in, refuse with an ACTIONABLE message instead of
	// letting database/sql surface its cryptic `unknown driver "turso"`.
	if driver == "turso" && !driverRegistered("turso") {
		return nil, fmt.Errorf("database type %q needs the Turso backend compiled in — rebuild with `-tags turso` after `go get turso.tech/database/tursogo`; default builds stay pure-Go on modernc sqlite", d.Type)
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(d.MaxOpen)
	db.SetMaxIdleConns(d.MaxIdle)
	db.SetConnMaxLifetime(time.Duration(d.Lifetime) * time.Minute)

	// A :memory: SQLite database exists PER CONNECTION — with a pool, every new conn would be a
	// fresh empty database. Pin the pool to one connection so it behaves like one database.
	if (driver == "sqlite" || driver == "turso") && strings.Contains(dsn, ":memory:") {
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}

	// Print success connection log
	dbType := strings.ToLower(d.Type)
	switch {
	case dbType == "sqlite" || dbType == "sqlite3":
		fmt.Printf("Successfully connected to SQLite database: %s\n", dsn)
	case dbType == "turso":
		fmt.Printf("Successfully connected to Turso database: %s\n", dsn)
	default:
		fmt.Printf("Successfully connected to database (%s) at %s:%d (DB: %s)\n", d.Type, d.Host, d.Port, d.Name)
	}

	return db, nil
}

func (d *Config) DSN() string {
	dsn, _ := d.BuildDSN()
	return dsn
}

func (d *Config) BuildDSN() (string, error) {
	dbType := strings.ToLower(d.Type)
	switch dbType {
	case "postgres", "postgresql":
		sslMode := d.SSLMode
		if sslMode == "" {
			sslMode = "disable"
		}
		timeout := d.Timeout
		if timeout == 0 {
			timeout = 5
		}
		timezone := d.Timezone
		if timezone == "" {
			timezone = "Asia/Ho_Chi_Minh"
		}
		return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s timezone=%s connect_timeout=%d",
			d.Host, d.Port, d.User, d.Password, d.Name, sslMode, timezone, timeout), nil

	case "mysql":
		// Format: username:password@tcp(host:port)/dbname?charset=utf8mb4&parseTime=True&loc=Local
		timezone := d.Timezone
		if timezone == "" {
			timezone = "Local"
		}
		return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=%s",
			d.User, d.Password, d.Host, d.Port, d.Name, timezone), nil

	case "sqlite", "sqlite3":
		// SQLite DSN is the file path (stored in Name or Host), wrapped in a file: URI so pragmas
		// ride along. WAL + busy_timeout are what make two concurrent writers QUEUE instead of
		// erroring "database is locked" — the number-one SQLite footgun without them.
		path := d.Name
		if path == "" {
			path = d.Host
		}
		if path == "" {
			path = "kitwork.db"
		}
		if strings.Contains(path, ":memory:") {
			// In-memory: WAL is meaningless; the pool is pinned to 1 conn in Connect().
			return "file::memory:?_pragma=foreign_keys(1)", nil
		}
		return "file:" + filepath.ToSlash(path) +
			"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", nil

	case "turso":
		// Turso Database reads the SAME on-disk SQLite FILE FORMAT, but tursogo takes a PLAIN file
		// path — modernc's `file:...?_pragma=` DSN is modernc-specific and Turso would not parse it.
		// So the DSN is just the path; engine-level options (WAL, and MVCC via `BEGIN CONCURRENT` —
		// the one thing plain SQLite cannot do) are configured differently and MUST be verified
		// against tursogo when the dependency is wired in. Path selection mirrors the sqlite case.
		path := d.Name
		if path == "" {
			path = d.Host
		}
		if path == "" {
			path = "kitwork.db"
		}
		if strings.Contains(path, ":memory:") {
			return ":memory:", nil
		}
		return filepath.ToSlash(path), nil

	default:
		return "", fmt.Errorf("unsupported database type: %s", d.Type)
	}
}

// driverRegistered reports whether a database/sql driver of the given name is present in THIS binary.
// The Turso backend is gated behind the `turso` build tag: the blank import that registers the
// "turso" driver only compiles in with `-tags turso` (see turso_driver.go), so a default build
// answers false and Connect can return a clear "not compiled in" error rather than the cryptic
// database/sql default.
func driverRegistered(name string) bool {
	for _, d := range sql.Drivers() {
		if d == name {
			return true
		}
	}
	return false
}
