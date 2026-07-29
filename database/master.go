package database

import (
	"database/sql"
	"strings"
)

var System *sql.DB

// SystemDriver is the `type` of the connected system database ("postgres", "mysql", "sqlite", …),
// lowercased. A *sql.DB does not carry its dialect, and callers were reading `System != nil` as if
// it meant "Postgres" — true for every deployment so far, but it is an assumption, not a fact. The
// background stores (cron, queue) speak different SQL per dialect, so they must ask WHICH, not
// WHETHER. Empty when no system database is connected.
var SystemDriver string

var Configs map[string]Config = make(map[string]Config)

// SystemIsPostgres reports whether the connected system database speaks Postgres — the dialect that
// supports `$1` placeholders, `NOW()` and `FOR UPDATE SKIP LOCKED`.
func SystemIsPostgres() bool {
	return System != nil && (SystemDriver == "postgres" || SystemDriver == "postgresql" || SystemDriver == "pgx")
}

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
