//go:build turso

package database

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// Differential compat probe for the Turso backend. Built ONLY with `-tags turso`; it SKIPS until the
// tursogo dependency is actually wired in (the blank import in turso_driver.go uncommented after
// `go get turso.tech/database/tursogo`). Once the "turso" driver is registered, it runs the SAME
// DDL/DML/DQL shape the sqlite path relies on and asserts identical results — the point is to MEASURE
// the SQLite-compatibility gap of the Rust rewrite with our OWN tests, not to trust the README.
func TestTursoRoundtripDifferential(t *testing.T) {
	if !driverRegistered("turso") {
		t.Skip("turso driver not registered — run `go get turso.tech/database/tursogo`, uncomment the import in turso_driver.go, then `go test -tags turso ./database/...`")
	}

	dir := t.TempDir()
	dsn, err := (&Config{Type: "turso", Name: filepath.Join(dir, "diff.db")}).BuildDSN()
	if err != nil {
		t.Fatalf("turso BuildDSN: %v", err)
	}
	db, err := sql.Open("turso", dsn)
	if err != nil {
		t.Fatalf("open turso: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Fatalf("ping turso: %v", err)
	}

	// The exact SQL the query builder emits on sqlite: `$N` placeholders + `RETURNING`. SQLite accepts
	// `$name`/`$1` natively and RETURNING since 3.35; modernc runs this unchanged. If tursogo diverges
	// on either, THIS is the compat gap to record — assert the reason, not just that it failed.
	if _, err := db.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, age INTEGER)`); err != nil {
		t.Fatalf("CREATE on turso: %v", err)
	}
	var id int
	if err := db.QueryRow(`INSERT INTO users (name, age) VALUES ($1, $2) RETURNING id`, "An", 20).Scan(&id); err != nil {
		t.Fatalf("INSERT ... RETURNING with $N on turso (the builder's insert shape): %v", err)
	}
	if id != 1 {
		t.Errorf("RETURNING id = %d, want 1", id)
	}
	var name string
	if err := db.QueryRow(`SELECT name FROM users WHERE age > $1`, 18).Scan(&name); err != nil {
		t.Fatalf("SELECT with $N on turso: %v", err)
	}
	if name != "An" {
		t.Errorf("SELECT name = %q, want %q", name, "An")
	}
}
