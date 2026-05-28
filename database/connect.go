package database

import (
	"database/sql"
	"fmt"
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

	db, err := sql.Open(d.Type, dsn)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(d.MaxOpen)
	db.SetMaxIdleConns(d.MaxIdle)
	db.SetConnMaxLifetime(time.Duration(d.Lifetime) * time.Minute)

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
		// SQLite DSN is simply the file path (stored in Host or Name)
		path := d.Name
		if path == "" {
			path = d.Host
		}
		if path == "" {
			path = "kitwork.db"
		}
		return path, nil

	default:
		return "", fmt.Errorf("unsupported database type: %s", d.Type)
	}
}
