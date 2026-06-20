package database

import (
	"database/sql"
	"strings"
)

var System *sql.DB
var Configs map[string]Config = make(map[string]Config)

// DomainRedirect returns the `redirect_to` target for a host from the system
// `domain` table, or "" when there is none (NULL/empty/no row/no system DB).
func DomainRedirect(domain string) (target string, err error) {
	if System != nil {
		var rt sql.NullString
		query := "SELECT redirect_to FROM domain WHERE hostname = $1"
		err = System.QueryRow(query, domain).Scan(&rt)
		if err == nil && rt.Valid {
			target = strings.TrimSpace(rt.String)
		}
	}
	return
}

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
