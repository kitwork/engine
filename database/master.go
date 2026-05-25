package database

import (
	"database/sql"
)

var System *sql.DB
var Default *sql.DB

func DomainSystemExists(domain string) (exists bool, err error) {
	if System != nil {
		query := "SELECT EXISTS(SELECT 1 FROM domains WHERE domain_name = $1)"
		err = System.QueryRow(query, domain).Scan(&exists)
	}
	return
}

func IdentitySystem(domain string) (string, error) {
	return "", nil
}

func Connect(cfg *Config) (*sql.DB, error) {
	return cfg.Connect()
}
