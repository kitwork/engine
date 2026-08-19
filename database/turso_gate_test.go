package database

import (
	"path/filepath"
	"strings"
	"testing"

	// The sqlite CONTROL below opens a real file, so this test binary must register the modernc
	// driver — the production blank import lives in package work, not here. This mirrors the other
	// driver-using tests in the repo (e.g. work/entity_scope_test.go).
	_ "modernc.org/sqlite"
)

// The Turso backend is GATED behind the `turso` build tag. This file is the CONTROL test for the
// DEFAULT (pure-Go) build: it proves the gate is closed, and closed for the RIGHT reason, using live
// sqlite cases as controls so a false green (e.g. the whole harness silently misbehaving) shows up.
//
// Disable-the-code check (the discipline this project holds — see
// [[validate-tests-by-disabling-code]]): delete the `turso` gate branch in Connect and
// TestTursoGateClosedByDefault goes red, because sql.Open would surface the cryptic
// `unknown driver "turso"` instead of the actionable message pinned here. Delete the `turso` case in
// BuildDSN and TestTursoDSNShape goes red, because turso falls through to the unknown-type default.

func TestTursoDSNShape(t *testing.T) {
	// turso speaks a PLAIN file path (per tursogo), NOT modernc's `file:...?_pragma=` URI.
	dsn, err := (&Config{Type: "turso", Name: "app.db"}).BuildDSN()
	if err != nil {
		t.Fatalf("turso BuildDSN errored: %v", err)
	}
	if strings.Contains(dsn, "_pragma=") {
		t.Errorf("turso DSN must not carry modernc `_pragma=` options (tursogo would not parse them): %q", dsn)
	}
	if !strings.Contains(dsn, "app.db") {
		t.Errorf("turso DSN dropped the file name: %q", dsn)
	}

	// CONTROL 1 — sqlite still produces the modernc URI, i.e. adding turso did not disturb it.
	sq, err := (&Config{Type: "sqlite", Name: "app.db"}).BuildDSN()
	if err != nil {
		t.Fatalf("sqlite BuildDSN errored: %v", err)
	}
	if !strings.Contains(sq, "journal_mode(WAL)") {
		t.Errorf("sqlite DSN lost its WAL pragma — control broken: %q", sq)
	}

	// CONTROL 2 — an unknown type must STILL be rejected, so the turso positive above means something
	// (BuildDSN discriminates rather than accepting anything).
	if _, err := (&Config{Type: "nosuchdb"}).BuildDSN(); err == nil {
		t.Error("unknown database type was accepted by BuildDSN")
	}
}

func TestTursoGateClosedByDefault(t *testing.T) {
	// Skip-first so this stays correct in a `-tags turso` build with the dependency wired in: there
	// the driver IS registered and Connect legitimately proceeds, so the gate assertion does not apply.
	if driverRegistered("turso") {
		t.Skip("turso driver is compiled in (-tags turso + dependency) — this gate test covers the default build only")
	}

	dir := t.TempDir()

	// SUBJECT — turso is not compiled into the default build, so Connect must refuse it with an
	// actionable reason. Assert the REASON, not merely that err != nil.
	if _, err := (&Config{Type: "turso", Name: filepath.Join(dir, "t.db")}).Connect(); err == nil {
		t.Fatal("GATE OPEN: turso connected without its driver registered")
	} else {
		msg := err.Error()
		if !strings.Contains(msg, "-tags turso") {
			t.Errorf("turso gate error is not actionable — want it to name `-tags turso`, got: %q", msg)
		}
		if strings.Contains(msg, "unknown driver") {
			t.Errorf("gate leaked database/sql's cryptic error instead of the friendly one: %q", msg)
		}
	}

	// CONTROL — sqlite must connect fine in the SAME build. If this fails, the environment is broken
	// and the turso refusal above proves nothing.
	sq, err := (&Config{Type: "sqlite", Name: filepath.Join(dir, "c.db")}).Connect()
	if err != nil {
		t.Fatalf("CONTROL: sqlite failed to connect, so the turso refusal is not meaningful: %v", err)
	}
	sq.Close()
}
