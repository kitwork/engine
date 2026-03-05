package database

import (
	"database/sql"
	"fmt"
	"time"
)

func Connect(cfg *Config) (*sql.DB, error) {
	return cfg.Connect()
}

type Config struct {
	Driver   string `yaml:"driver"` // postgres, mysql, sqlite
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
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s timezone=%s connect_timeout=%d",
		d.Host, d.Port, d.User, d.Password, d.Name, d.SSLMode, d.Timezone, d.Timeout)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(d.MaxOpen)
	db.SetMaxIdleConns(d.MaxIdle)
	db.SetConnMaxLifetime(time.Duration(d.Lifetime) * time.Minute)

	if err := db.Ping(); err != nil {
		return nil, err
	}
	return db, nil
}

func (d *Config) DSN() string {
	return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s timezone=%s connect_timeout=%d",
		d.Host, d.Port, d.User, d.Password, d.Name, d.SSLMode, d.Timezone, d.Timeout)
}
