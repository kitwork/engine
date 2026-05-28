package database

import (
	"database/sql"
)

var System *sql.DB
var Default *sql.DB

func DomainSystemExists(domain string) (exists bool, err error) {
	if System != nil {
		query := "SELECT EXISTS(SELECT 1 FROM domain WHERE hostname = $1)"
		err = System.QueryRow(query, domain).Scan(&exists)
	}
	return
}

func IdentitySystem(domain string) (identity string, err error) {
	if System != nil {
		query := "SELECT identity FROM domain WHERE hostname = $1"
		err = System.QueryRow(query, domain).Scan(&identity)
	}
	return
}

func Connect(cfg *Config) (*sql.DB, error) {
	return cfg.Connect()
}
