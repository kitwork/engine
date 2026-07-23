package database

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	Alias    string `yaml:"alias"` // master, config, data_old, data
	Type     string `yaml:"type"`  //  postgres, mysql, sqlite
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Name     string `yaml:"name"`
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	SSLMode  string `yaml:"ssl"`
	Timezone string `yaml:"timezone"`
	Timeout  int    `yaml:"timeout"`
	MaxOpen  int    `yaml:"max_open"`
	MaxIdle  int    `yaml:"max_idle"`
	Lifetime int    `yaml:"lifetime"`
	MaxLimit int    `yaml:"max_limit"`
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
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(d.MaxOpen)
	db.SetMaxIdleConns(d.MaxIdle)
	db.SetConnMaxLifetime(time.Duration(d.Lifetime) * time.Minute)

	// A :memory: SQLite database exists PER CONNECTION — with a pool, every new conn would be a
	// fresh empty database. Pin the pool to one connection so it behaves like one database.
	if driver == "sqlite" && strings.Contains(dsn, ":memory:") {
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}

	// Print success connection log
	dbType := strings.ToLower(d.Type)
	if dbType == "sqlite" || dbType == "sqlite3" {
		fmt.Printf("Successfully connected to SQLite database: %s\n", dsn)
	} else {
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

	default:
		return "", fmt.Errorf("unsupported database type: %s", d.Type)
	}
}
